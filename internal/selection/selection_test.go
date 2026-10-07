package selection

import (
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
)

func when() time.Time { return time.Date(2026, 10, 6, 3, 30, 0, 0, time.UTC) }
func input(id, nickname, identifier string, kind ParticipantKind, body string) InputComment {
	at := when()
	return InputComment{ID: id, Nickname: nickname, Identifier: identifier, ParticipantKind: kind, Kind: Text, Text: body, PostedAt: &at}
}
func participants(t *testing.T, values ...InputComment) []Participant {
	t.Helper()
	out, err := BuildParticipants(values)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func selection(t *testing.T, values []Participant, filters Filters, author *ParticipantKey) Selection {
	t.Helper()
	out, err := Evaluate(values, filters, author)
	if err != nil {
		t.Fatal(err)
	}
	if out.Included+out.Excluded != len(values) {
		t.Fatal("count partition")
	}
	return out
}
func TestBuildGroupsStructuredIdentityInFirstCommentOrder(t *testing.T) {
	p := participants(t,
		input("1", "a|b", "c", Fixed, "first"), input("2", "a", "b|c", Fixed, "second"),
		input("3", "a|b", "c", SemiFixed, "third"), input("4", "a|b", "c", Anonymous, "fourth"),
		input("5", "changed nickname", "c", Fixed, "fifth"), input("1", "a|b", "c", Fixed, "first duplicate"))
	if len(p) != 4 || len(p[0].Comments) != 2 || p[0].Comments[1].ID != "3" || p[1].Comments[0].ID != "2" || p[2].Kind != Anonymous || p[3].Key.Nickname != "changed nickname" {
		t.Fatalf("wrong order/group: %+v", p)
	}
	seen := map[contracts.ParticipantID]bool{}
	for _, value := range p {
		if seen[value.ID] || value.ID != ParticipantID(value.Key) || value.Manual != DefaultManual() {
			t.Fatal("identity/default invariant")
		}
		seen[value.ID] = true
	}
	empty, err := BuildParticipants(nil)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatal("nil is valid empty snapshot")
	}
}
func TestBuildClonesCommentPointersAndNormalizesVoiceDcconDisplay(t *testing.T) {
	parent := "parent"
	at := when()
	urls := []string{"image"}
	voice := input("voice", "voice", "uid", Fixed, "raw voice")
	voice.Kind = Voice
	dccon := input("dc", "dc", "uid", Fixed, "")
	dccon.Kind = Dccon
	dccon.ParentID = &parent
	dccon.PostedAt = &at
	dccon.MediaURLs = urls
	p := participants(t, voice, dccon)
	parent = "changed"
	at = at.Add(time.Hour)
	urls[0] = "changed"
	if p[0].Comments[0].Text != "[보플]" || p[1].Comments[0].Text != "[디시콘]" || *p[1].Comments[0].ParentID != "parent" || !p[1].Comments[0].PostedAt.Equal(when()) || p[1].Comments[0].MediaURLs[0] != "image" {
		t.Fatal("adopted values alias raw payload")
	}
	if selection(t, p, Filters{ExcludeDcconOnly: true}, nil).Rows[0].Classification.Group != Unclassified {
		t.Fatal("voice is text for dccon filter")
	}
}
func TestMalformedBuildAndParticipantValuesRejectAtomically(t *testing.T) {
	base := input("id", "nick", "uid", Fixed, "body")
	cases := []InputComment{base}
	for _, change := range []func(*InputComment){
		func(v *InputComment) { v.ID = "" }, func(v *InputComment) { v.Nickname = "" }, func(v *InputComment) { v.Identifier = "" },
		func(v *InputComment) { v.ParticipantKind = "invalid" }, func(v *InputComment) { v.Kind = "invalid" }, func(v *InputComment) { v.Text = "" },
		func(v *InputComment) { v.Text = strings.Repeat("x", (64<<10)+1) }, func(v *InputComment) { v.Nickname = string([]byte{0xff}) },
		func(v *InputComment) { v.Identifier = string([]byte{0xff}) }, func(v *InputComment) { v.Text = string([]byte{0xff}) },
		func(v *InputComment) { v.ID = string([]byte{0xff}) }, func(v *InputComment) { empty := ""; v.ParentID = &empty }, func(v *InputComment) { zero := time.Time{}; v.PostedAt = &zero },
	} {
		value := base
		change(&value)
		cases = append(cases, value)
	}
	for index, value := range cases[1:] {
		if got, err := BuildParticipants([]InputComment{base, value}); err == nil || got != nil {
			t.Fatalf("case%d partial success", index)
		}
	}
	if _, err := BuildParticipants(make([]InputComment, MaxComments+1)); err == nil {
		t.Fatal("comment bound")
	}
	p := participants(t, base)
	for _, change := range []func(*Participant){func(v *Participant) { v.ID = "other" }, func(v *Participant) { v.Kind = Anonymous }, func(v *Participant) { v.Kind = "invalid" }, func(v *Participant) { v.Comments = nil }, func(v *Participant) { v.Key.Identifier = "" }, func(v *Participant) { v.Comments = []Comment{{}} }, func(v *Participant) { v.Comments = make([]Comment, MaxComments+1) }} {
		bad := p[0]
		change(&bad)
		if _, err := Evaluate([]Participant{p[0], bad}, Filters{}, nil); err == nil {
			t.Fatal("invalid participant accepted")
		}
	}
	if _, err := Evaluate([]Participant{p[0], p[0]}, Filters{}, nil); err == nil {
		t.Fatal("duplicate participant accepted")
	}
	if _, err := Evaluate(make([]Participant, MaxComments+1), Filters{}, nil); err == nil {
		t.Fatal("participant bound")
	}
	author := ParticipantKey{}
	if _, err := Evaluate(p, Filters{}, &author); err == nil {
		t.Fatal("invalid identified author")
	}
}
func TestAutomaticPriorityUsesAllCommentsAndIncludeIsNotWhitelist(t *testing.T) {
	cutoff := when().Add(time.Minute)
	late := cutoff.Add(time.Second)
	anon := input("1", "author", "ip", Anonymous, "late BAD good")
	anon.PostedAt = &late
	anon.Kind = Dccon
	p := participants(t, anon)
	author := p[0].Key
	filter := Filters{ExcludeAnonymous: true, ExcludeAuthor: true, ExcludeDcconOnly: true, TimeCut: &cutoff, ExcludeKeywords: []string{"[디시콘]"}, IncludeKeywords: []string{"[디시콘]"}}
	expected := []Reason{AnonymousReason, AuthorReason, DcconReason, TimeCutReason, ExcludeKeywordReason, IncludeKeywordReason, NoReason}
	for index, reason := range expected {
		if got := selection(t, p, filter, &author).Rows[0].Classification.Reason; got != reason {
			t.Fatalf("priority%d=%s want%s", index, got, reason)
		}
		switch index {
		case 0:
			filter.ExcludeAnonymous = false
		case 1:
			filter.ExcludeAuthor = false
		case 2:
			filter.ExcludeDcconOnly = false
		case 3:
			filter.TimeCut = nil
		case 4:
			filter.ExcludeKeywords = nil
		case 5:
			filter.IncludeKeywords = []string{"not present"}
		}
	}
	text := input("early", "person", "uid", Fixed, "early good")
	text.PostedAt = &[]time.Time{cutoff.Add(-time.Nanosecond)}[0]
	later := input("late", "person", "uid", Fixed, "BAD")
	later.PostedAt = &late
	both := participants(t, text, later)
	result := selection(t, both, Filters{TimeCut: &cutoff, ExcludeDcconOnly: true, ExcludeKeywords: []string{"BAD"}, IncludeKeywords: []string{"good"}}, nil)
	if result.Rows[0].Classification.Reason != ExcludeKeywordReason {
		t.Fatal("late keyword must still exclude")
	}
}
func TestAutomaticIndependentConditionsAndExactTimeBoundary(t *testing.T) {
	fixed := participants(t, input("1", "nick", "uid", Fixed, "GOOD [a-z]+"))
	author := fixed[0].Key
	other := author
	other.Identifier = "other"
	for _, test := range []struct {
		filter Filters
		author *ParticipantKey
		reason Reason
	}{
		{Filters{ExcludeAnonymous: true}, nil, NoReason}, {Filters{ExcludeAuthor: true}, nil, NoReason}, {Filters{ExcludeAuthor: true}, &other, NoReason}, {Filters{ExcludeAuthor: true}, &author, AuthorReason},
		{Filters{ExcludeKeywords: []string{"good"}}, nil, NoReason}, {Filters{ExcludeKeywords: []string{"[a-z]+"}}, nil, ExcludeKeywordReason}, {Filters{IncludeKeywords: []string{"GOOD"}}, nil, IncludeKeywordReason}, {Filters{IncludeKeywords: []string{"absent"}}, nil, NoReason},
	} {
		r := selection(t, fixed, test.filter, test.author)
		if r.Rows[0].Classification.Reason != test.reason {
			t.Fatalf("condition: %+v", test)
		}
		if r.AuthorIdentified != (test.author != nil) {
			t.Fatal("author evidence")
		}
	}
	cutoff := when().Add(time.Minute)
	for _, delta := range []time.Duration{-time.Nanosecond, 0, time.Nanosecond} {
		at := cutoff.Add(delta)
		v := input("t", "nick", "uid", Fixed, "text")
		v.PostedAt = &at
		r := selection(t, participants(t, v), Filters{TimeCut: &cutoff}, nil)
		want := NoReason
		if delta >= 0 {
			want = TimeCutReason
		}
		if r.Rows[0].Classification.Reason != want {
			t.Fatal("exclusive next minute")
		}
	}
	missing := input("missing", "nick", "uid", Fixed, "text")
	missing.PostedAt = nil
	if _, err := Evaluate(participants(t, missing), Filters{TimeCut: &cutoff}, nil); err == nil {
		t.Fatal("unknown timestamp cannot prove exclusion")
	}
	empty, err := Evaluate(nil, DefaultFilters(), nil)
	if err != nil || empty.Included != 0 || empty.Excluded != 0 || empty.AuthorIdentified {
		t.Fatal("empty partition")
	}
}
func TestManualTwoValuesSurviveGroupChangesAndBulkIgnoresSearch(t *testing.T) {
	p := participants(t, input("1", "unclassified", "u", Fixed, "plain"), input("2", "included", "i", Fixed, "good"), input("3", "excluded", "e", Fixed, "bad"))
	filters := Filters{IncludeKeywords: []string{"good"}, ExcludeKeywords: []string{"bad"}}
	before := CloneParticipants(p)
	toggled, err := ToggleManual(p, p[2].ID, filters, nil)
	if err != nil || !toggled[2].Manual.OverrideExcluded || !toggled[2].Manual.ManualIncluded {
		t.Fatal("excluded toggle")
	}
	toggled, err = ToggleManual(toggled, p[1].ID, filters, nil)
	if err != nil || toggled[1].Manual.ManualIncluded || toggled[1].Manual.OverrideExcluded {
		t.Fatal("included toggle")
	}
	without := selection(t, toggled, ResetFilters(), nil)
	if without.Rows[1].Classification.Included || !without.Rows[2].Classification.Included {
		t.Fatal("filter reset changed manual state")
	}
	restored := selection(t, toggled, filters, nil)
	if !restored.Rows[2].Classification.Included || restored.Rows[1].Classification.Included {
		t.Fatal("manual values were not preserved")
	}
	if len(Search(restored.Rows, "UNCLASSIFIED")) != 1 {
		t.Fatal("search")
	}
	bulk, err := SetUnclassified(toggled, filters, nil, false)
	if err != nil || bulk[0].Manual.ManualIncluded || bulk[1].Manual != toggled[1].Manual || bulk[2].Manual != toggled[2].Manual {
		t.Fatal("bulk scope")
	}
	twice, err := SetUnclassified(bulk, filters, nil, false)
	if err != nil || !reflect.DeepEqual(bulk, twice) {
		t.Fatal("bulk idempotence")
	}
	reset := ResetManual(bulk)
	for _, value := range reset {
		if value.Manual != DefaultManual() {
			t.Fatal("manual reset")
		}
	}
	if !reflect.DeepEqual(p, before) {
		t.Fatal("mutation of input")
	}
	if _, err := ToggleManual(p, "missing", filters, nil); err == nil {
		t.Fatal("missing id")
	}
	invalidFilter := Filters{IncludeKeywords: []string{strings.Repeat("x", 101)}}
	if _, err := ToggleManual(p, p[0].ID, invalidFilter, nil); err == nil {
		t.Fatal("toggle must validate filter")
	}
	if _, err := SetUnclassified(p, invalidFilter, nil, true); err == nil {
		t.Fatal("bulk must validate filter")
	}
}
func TestManualTruthTableAndSearchPreserveEligibility(t *testing.T) {
	for _, manual := range []ManualState{{false, false}, {false, true}, {true, false}, {true, true}} {
		p := participants(t, input("1", "ÄNick", "uID", Fixed, "good bad"))
		p[0].Manual = manual
		for _, excluded := range []bool{false, true} {
			filter := Filters{IncludeKeywords: []string{"good"}}
			if excluded {
				filter.ExcludeKeywords = []string{"bad"}
			}
			r := selection(t, p, filter, nil)
			want := manual.ManualIncluded
			if excluded {
				want = manual.OverrideExcluded
			}
			if r.Rows[0].Classification.Included != want {
				t.Fatal("manual truth table")
			}
			if len(Search(r.Rows, "änick")) != 1 || len(Search(r.Rows, "UID")) != 1 || len(Search(r.Rows, "absent")) != 0 || len(Search(r.Rows, "")) != 1 {
				t.Fatal("case-insensitive view")
			}
			if r.Included+r.Excluded != 1 {
				t.Fatal("query changed counts")
			}
		}
	}
}
func TestKeywordsTrimExactDuplicateUTF16BoundsAndIdempotentDelete(t *testing.T) {
	keywords, err := NormalizeKeywords([]string{" ", " 한글 ", "한글", "Case", "case"})
	if err != nil || !reflect.DeepEqual(keywords, []string{"한글", "Case", "case"}) {
		t.Fatal("trim/exact duplicate")
	}
	full := make([]string, 100)
	for i := range full {
		full[i] = string(rune(0xAC00 + i))
	}
	if _, err := AddKeyword(full, full[0]); err != nil {
		t.Fatal("duplicate at max")
	}
	if _, err := AddKeyword(full, "new"); err == nil {
		t.Fatal("keyword count")
	}
	for _, text := range []string{strings.Repeat("가", 100), strings.Repeat("😀", 50)} {
		if _, err := AddKeyword(nil, text); err != nil {
			t.Fatal("UTF16 max")
		}
	}
	for _, text := range []string{strings.Repeat("가", 101), strings.Repeat("😀", 51), string([]byte{0xff})} {
		if _, err := AddKeyword(nil, text); err == nil {
			t.Fatal("invalid keyword")
		}
	}
	deleted := RemoveKeyword(keywords, "한글")
	if !reflect.DeepEqual(deleted, RemoveKeyword(deleted, "한글")) || len(keywords) != 3 {
		t.Fatal("delete idempotence/input preservation")
	}
	zero := time.Time{}
	if _, err := ValidateFilters(Filters{TimeCut: &zero}); err == nil {
		t.Fatal("zero cutoff")
	}
	if _, err := ValidateFilters(Filters{ExcludeKeywords: []string{strings.Repeat("x", 101)}}); err == nil {
		t.Fatal("exclude validation")
	}
	if !DefaultFilters().ExcludeAuthor || ResetFilters().ExcludeAuthor {
		t.Fatal("default and reset are different")
	}
}
func TestKSTCutoffMinuteEndDateRolloverAndInputBounds(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 5, 0, 0, time.FixedZone("KST", 9*3600))
	cut, err := CutoffKST(now, 0, 23, 59)
	want := time.Date(2026, 1, 1, 15, 0, 0, 0, time.UTC)
	if err != nil || !cut.Equal(want) {
		t.Fatal("minute-end/day rollover")
	}
	old, err := CutoffKST(now, 365, 0, 0)
	if err != nil || !old.Equal(time.Date(2024, 12, 31, 15, 1, 0, 0, time.UTC)) {
		t.Fatalf("365 day:%s %v", old, err)
	}
	for _, v := range [][3]int{{-1, 0, 0}, {366, 0, 0}, {0, -1, 0}, {0, 24, 0}, {0, 0, -1}, {0, 0, 60}} {
		if _, err := CutoffKST(now, v[0], v[1], v[2]); err == nil {
			t.Fatal("cutoff range")
		}
	}
	if _, err := CutoffKST(time.Time{}, 0, 0, 0); err == nil {
		t.Fatal("zero clock")
	}
}
func TestFreezeOwnsNestedValuesAndRejectsInsufficientOrInvalidInputs(t *testing.T) {
	a := input("1", "one", "uid1", Fixed, "body")
	parent := "parent"
	a.ParentID = &parent
	a.MediaURLs = []string{"media"}
	p := participants(t, a, input("2", "two", "uid2", Fixed, "body"))
	cut := when().Add(time.Minute)
	filters := Filters{TimeCut: &cut, IncludeKeywords: []string{"body"}}
	author := ParticipantKey{Nickname: "absent", Identifier: "absent"}
	items := Items{Mode: Multiple, Multiple: []Item{{ID: "one", Name: " ", Count: 1}}}
	frozen, err := Freeze(p, filters, &author, items, " message ", true)
	if err != nil {
		t.Fatal(err)
	}
	p[0].Comments[0].Text = "mutated"
	*p[0].Comments[0].ParentID = "mutated"
	p[0].Comments[0].MediaURLs[0] = "mutated"
	*p[0].Comments[0].PostedAt = time.Time{}
	filters.IncludeKeywords[0] = "mutated"
	cut = cut.Add(time.Hour)
	author.Nickname = "changed"
	items.Multiple[0].Name = "changed"
	first := frozen.Selection.Rows[0].Participant.Comments[0]
	if first.Text != "body" || *first.ParentID != "parent" || first.MediaURLs[0] != "media" || !first.PostedAt.Equal(when()) || frozen.Filters.IncludeKeywords[0] != "body" || frozen.Author.Nickname != "absent" || frozen.Items[0].Name != " " || frozen.Message != " message " || frozen.RulesVersion != RulesVersion {
		t.Fatal("frozen input alias")
	}
	valid := participants(t, input("3", "one", "u", Fixed, "body"))
	if _, err := Freeze(valid, Filters{}, nil, DefaultItems(), "", true); err == nil {
		t.Fatal("initial needs2")
	}
	for _, v := range []struct {
		filter  Filters
		items   Items
		message string
	}{{Filters{IncludeKeywords: []string{strings.Repeat("x", 101)}}, DefaultItems(), ""}, {Filters{}, Items{}, ""}, {Filters{}, DefaultItems(), strings.Repeat("x", 21)}} {
		if _, err := Freeze(valid, v.filter, nil, v.items, v.message, false); err == nil {
			t.Fatal("freeze invalid")
		}
	}
}
func TestPureEvaluationIsDeterministicUnderConcurrentReaders(t *testing.T) {
	p := participants(t, input("1", "a", "1", Fixed, "good"), input("2", "b", "2", Anonymous, "bad"))
	filter := Filters{ExcludeAnonymous: true, IncludeKeywords: []string{"good"}}
	want := selection(t, p, filter, nil)
	var group sync.WaitGroup
	for i := 0; i < 32; i++ {
		group.Go(func() {
			for repeat := 0; repeat < 20; repeat++ {
				got, err := Evaluate(p, filter, nil)
				if err != nil || !reflect.DeepEqual(got, want) {
					t.Error("concurrent pure result")
				}
			}
		})
	}
	group.Wait()
}
func FuzzStructuredKeysAndKeywordsNeverPanic(f *testing.F) {
	for _, value := range []string{"", "a|b", "한글", "😀", string([]byte{0xff})} {
		f.Add(value)
	}
	f.Fuzz(func(t *testing.T, value string) {
		key := ParticipantKey{Nickname: value, Identifier: "id"}
		if key.Validate() == nil {
			if ParticipantID(key) != ParticipantID(key) {
				t.Fatal("unstable key")
			}
		}
		normalized, err := NormalizeKeywords([]string{value, value})
		if err == nil {
			if len(normalized) > 1 {
				t.Fatal("duplicate")
			}
			again, err := NormalizeKeywords(normalized)
			if err != nil || !reflect.DeepEqual(normalized, again) {
				t.Fatal("normalization invariant")
			}
		}
	})
}

func TestMissingTimeRequiresEvidenceAndDoesNotOverrideEarlierReason(t *testing.T) {
	cutoff := when().Add(time.Minute)
	before := cutoff.Add(-time.Nanosecond)
	known := input("known", "nick", "uid", Fixed, "text")
	known.PostedAt = &before
	missing := input("missing", "nick", "uid", Fixed, "text")
	missing.PostedAt = nil
	if selection(t, participants(t, known, missing), Filters{TimeCut: &cutoff}, nil).Rows[0].Classification.Reason != NoReason {
		t.Fatal("known-before witness suffices")
	}
	anon := missing
	anon.ParticipantKind = Anonymous
	if selection(t, participants(t, anon), Filters{TimeCut: &cutoff, ExcludeAnonymous: true}, nil).Rows[0].Classification.Reason != AnonymousReason {
		t.Fatal("earlier reason must not be replaced by time failure")
	}
	after := cutoff
	known.PostedAt = &after
	if result, err := Evaluate(participants(t, known, missing), Filters{TimeCut: &cutoff}, nil); err == nil || len(result.Rows) != 0 {
		t.Fatal("unknown time must not be guessed or partially published")
	}
}
func TestAggregateLimitsAndFreezeValidationAreAtomic(t *testing.T) {
	body := strings.Repeat("x", 64<<10)
	comments := make([]InputComment, 1601)
	for i := range comments {
		comments[i] = input(string(rune(0x1000+i)), "nick", "uid", Fixed, body)
	}
	if _, err := BuildParticipants(comments[:1600]); err != nil {
		t.Fatal("exact100MiB accepted text limit")
	}
	if value, err := BuildParticipants(comments); err == nil || value != nil {
		t.Fatal("aggregate payload overflow")
	}
	p := participants(t, input("one", "one", "uid1", Fixed, "text"), input("two", "two", "uid2", Fixed, "text"))
	for i := range p {
		template := p[i].Comments[0]
		p[i].Comments = make([]Comment, 50001)
		for j := range p[i].Comments {
			p[i].Comments[j] = template
			p[i].Comments[j].ID = string(rune(0x1000 + j))
		}
	}
	if result, err := Evaluate(p, Filters{}, nil); err == nil || len(result.Rows) != 0 {
		t.Fatal("aggregate adopted comment count")
	}
	invalidParticipant := participants(t, input("p", "one", "uid", Fixed, "text"))
	invalidParticipant[0].ID = "invalid"
	if _, err := Freeze(invalidParticipant, Filters{}, nil, DefaultItems(), "", false); err == nil {
		t.Fatal("freeze participant validation")
	}
}

func TestEvaluateRejectsOneCommentAssignedToTwoParticipants(t *testing.T) {
	p := participants(t, input("one", "one", "uid1", Fixed, "text"), input("two", "two", "uid2", Fixed, "text"))
	p[1].Comments[0].ID = p[0].Comments[0].ID
	if result, err := Evaluate(p, Filters{}, nil); err == nil || len(result.Rows) != 0 {
		t.Fatal("duplicate adopted comment must not increase lottery weight")
	}
}
