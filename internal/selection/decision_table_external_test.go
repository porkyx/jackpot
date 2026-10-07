package selection_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	sel "github.com/porkyx/jackpot/internal/selection"
)

func tableParticipant(kind sel.ParticipantKind, commentKind sel.CommentKind, at *time.Time) sel.Participant {
	key := sel.ParticipantKey{Nickname: "참가자", Identifier: "public", Anonymous: kind == sel.Anonymous}
	// ID construction only prepares a valid public input. Expected classifications
	// below are literal specification tables and use no production decision helper.
	return sel.Participant{ID: sel.ParticipantID(key), Key: key, Kind: kind, Comments: []sel.Comment{{ID: "comment", Kind: commentKind, Text: "literal [a-z]+ MATCH", PostedAt: at}}, Manual: sel.DefaultManual()}
}

// Every row is a literal oracle for the six independent matching facts in
// priority order: anonymous, author, dccon-only, late, exclude, include.
// This table does not calculate the first matching rule with an implementation
// loop or call classify/contains to obtain an expected result.
var reasonTable64 = [64]sel.Reason{
	sel.NoReason, sel.AnonymousReason, sel.AuthorReason, sel.AnonymousReason,
	sel.DcconReason, sel.AnonymousReason, sel.AuthorReason, sel.AnonymousReason,
	sel.TimeCutReason, sel.AnonymousReason, sel.AuthorReason, sel.AnonymousReason,
	sel.DcconReason, sel.AnonymousReason, sel.AuthorReason, sel.AnonymousReason,
	sel.ExcludeKeywordReason, sel.AnonymousReason, sel.AuthorReason, sel.AnonymousReason,
	sel.DcconReason, sel.AnonymousReason, sel.AuthorReason, sel.AnonymousReason,
	sel.TimeCutReason, sel.AnonymousReason, sel.AuthorReason, sel.AnonymousReason,
	sel.DcconReason, sel.AnonymousReason, sel.AuthorReason, sel.AnonymousReason,
	sel.IncludeKeywordReason, sel.AnonymousReason, sel.AuthorReason, sel.AnonymousReason,
	sel.DcconReason, sel.AnonymousReason, sel.AuthorReason, sel.AnonymousReason,
	sel.TimeCutReason, sel.AnonymousReason, sel.AuthorReason, sel.AnonymousReason,
	sel.DcconReason, sel.AnonymousReason, sel.AuthorReason, sel.AnonymousReason,
	sel.ExcludeKeywordReason, sel.AnonymousReason, sel.AuthorReason, sel.AnonymousReason,
	sel.DcconReason, sel.AnonymousReason, sel.AuthorReason, sel.AnonymousReason,
	sel.TimeCutReason, sel.AnonymousReason, sel.AuthorReason, sel.AnonymousReason,
	sel.DcconReason, sel.AnonymousReason, sel.AuthorReason, sel.AnonymousReason,
}
var groupTable = map[sel.Reason]sel.Group{
	sel.NoReason: sel.Unclassified, sel.IncludeKeywordReason: sel.AutoIncluded,
	sel.AnonymousReason: sel.AutoExcluded, sel.AuthorReason: sel.AutoExcluded,
	sel.DcconReason: sel.AutoExcluded, sel.TimeCutReason: sel.AutoExcluded, sel.ExcludeKeywordReason: sel.AutoExcluded,
}
var manualTable = map[sel.Group][4]bool{
	sel.Unclassified: {false, false, true, true}, sel.AutoIncluded: {false, false, true, true}, sel.AutoExcluded: {false, true, false, true},
}

func TestIndependentDecisionTable64MatchingFactsBy4ManualValues(t *testing.T) {
	cut := time.Date(2026, 10, 6, 3, 31, 0, 0, time.UTC)
	early := cut.Add(-time.Nanosecond)
	late := cut
	for mask, wantReason := range reasonTable64 {
		kind := sel.Fixed
		if mask&1 != 0 {
			kind = sel.Anonymous
		}
		commentKind := sel.Text
		if mask&4 != 0 {
			commentKind = sel.Dccon
		}
		at := early
		if mask&8 != 0 {
			at = late
		}
		participant := tableParticipant(kind, commentKind, &at)
		author := participant.Key
		if mask&2 == 0 {
			author.Identifier = "another"
		}
		exclude := "ABSENT"
		if mask&16 != 0 {
			exclude = "[a-z]+"
		}
		include := "absent"
		if mask&32 != 0 {
			include = "MATCH"
		}
		filters := sel.Filters{ExcludeAnonymous: true, ExcludeAuthor: true, ExcludeDcconOnly: true, TimeCut: &cut, ExcludeKeywords: []string{exclude}, IncludeKeywords: []string{include}}
		for manual, wantIncluded := range manualTable[groupTable[wantReason]] {
			participant.Manual = sel.ManualState{ManualIncluded: manual&2 != 0, OverrideExcluded: manual&1 != 0}
			before := participant.Manual
			result, err := sel.Evaluate([]sel.Participant{participant}, filters, &author)
			if err != nil || len(result.Rows) != 1 {
				t.Fatalf("facts%06b/manual%d: rows/error %+v %v", mask, manual, result, err)
			}
			want := sel.Classification{Group: groupTable[wantReason], Reason: wantReason, Included: wantIncluded}
			if got := result.Rows[0].Classification; got != want {
				t.Fatalf("facts%06b/manual%d: got%+v want%+v", mask, manual, got, want)
			}
			if result.Included+result.Excluded != 1 || result.Included != map[bool]int{false: 0, true: 1}[wantIncluded] || participant.Manual != before {
				t.Fatalf("facts%06b/manual%d count/input invariant", mask, manual)
			}
		}
	}
}

func TestIndependentMCDCGatingPairsEachAtomicCondition(t *testing.T) {
	cut := time.Date(2026, 10, 6, 3, 31, 0, 0, time.UTC)
	early := cut.Add(-time.Nanosecond)
	late := cut
	fixed := tableParticipant(sel.Fixed, sel.Text, &early)
	anon := tableParticipant(sel.Anonymous, sel.Text, &early)
	dccon := tableParticipant(sel.Fixed, sel.Dccon, &early)
	voice := tableParticipant(sel.Fixed, sel.Voice, &early)
	lateParticipant := tableParticipant(sel.Fixed, sel.Text, &late)
	same := fixed.Key
	other := same
	other.Identifier = "another"
	cases := []struct {
		name        string
		participant sel.Participant
		filters     sel.Filters
		author      *sel.ParticipantKey
		reason      sel.Reason
	}{
		{"anonymous flag true", anon, sel.Filters{ExcludeAnonymous: true}, nil, sel.AnonymousReason},
		{"anonymous flag false", anon, sel.Filters{}, nil, sel.NoReason},
		{"anonymous kind false", fixed, sel.Filters{ExcludeAnonymous: true}, nil, sel.NoReason},
		{"author all true", fixed, sel.Filters{ExcludeAuthor: true}, &same, sel.AuthorReason},
		{"author flag false", fixed, sel.Filters{}, &same, sel.NoReason},
		{"author pointer false", fixed, sel.Filters{ExcludeAuthor: true}, nil, sel.NoReason},
		{"author equality false", fixed, sel.Filters{ExcludeAuthor: true}, &other, sel.NoReason},
		{"dccon all true", dccon, sel.Filters{ExcludeDcconOnly: true}, nil, sel.DcconReason},
		{"dccon flag false", dccon, sel.Filters{}, nil, sel.NoReason},
		{"dccon text witness", fixed, sel.Filters{ExcludeDcconOnly: true}, nil, sel.NoReason},
		{"dccon voice witness", voice, sel.Filters{ExcludeDcconOnly: true}, nil, sel.NoReason},
		{"time all true", lateParticipant, sel.Filters{TimeCut: &cut}, nil, sel.TimeCutReason},
		{"time pointer false", lateParticipant, sel.Filters{}, nil, sel.NoReason},
		{"time before witness", fixed, sel.Filters{TimeCut: &cut}, nil, sel.NoReason},
		{"exclude literal match", fixed, sel.Filters{ExcludeKeywords: []string{"[a-z]+"}}, nil, sel.ExcludeKeywordReason},
		{"exclude literal mismatch", fixed, sel.Filters{ExcludeKeywords: []string{"a.*z"}}, nil, sel.NoReason},
		{"exclude case mismatch", fixed, sel.Filters{ExcludeKeywords: []string{"match"}}, nil, sel.NoReason},
		{"include literal match", fixed, sel.Filters{IncludeKeywords: []string{"MATCH"}}, nil, sel.IncludeKeywordReason},
		{"include mismatch unclassified", fixed, sel.Filters{IncludeKeywords: []string{"absent"}}, nil, sel.NoReason},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			result, err := sel.Evaluate([]sel.Participant{test.participant}, test.filters, test.author)
			if err != nil || len(result.Rows) != 1 {
				t.Fatalf("unexpected error %v", err)
			}
			want := sel.Classification{Group: groupTable[test.reason], Reason: test.reason, Included: test.reason == sel.NoReason || test.reason == sel.IncludeKeywordReason}
			if result.Rows[0].Classification != want {
				t.Fatalf("got%+v want%+v", result.Rows[0].Classification, want)
			}
		})
	}
}

func TestIndependentKeywordORAcrossDifferentWordsAndComments(t *testing.T) {
	at := time.Date(2026, 10, 6, 3, 30, 0, 0, time.UTC)
	base := tableParticipant(sel.Fixed, sel.Text, &at)
	base.Comments = []sel.Comment{{ID: "first", Kind: sel.Text, Text: "early plain", PostedAt: &at}, {ID: "second", Kind: sel.Text, Text: "late TARGET", PostedAt: &at}}
	for _, filters := range []sel.Filters{{IncludeKeywords: []string{"absent", "TARGET"}}, {ExcludeKeywords: []string{"absent", "TARGET"}}} {
		result, err := sel.Evaluate([]sel.Participant{base}, filters, nil)
		if err != nil {
			t.Fatal(err)
		}
		want := sel.IncludeKeywordReason
		if len(filters.ExcludeKeywords) > 0 {
			want = sel.ExcludeKeywordReason
		}
		if result.Rows[0].Classification.Reason != want {
			t.Fatalf("later word/comment OR got%s want%s", result.Rows[0].Classification.Reason, want)
		}
	}
}

func TestIndependentUnknownTimeRequiresEvidenceAndKeepsPriority(t *testing.T) {
	cut := time.Date(2026, 10, 6, 3, 31, 0, 0, time.UTC)
	early := cut.Add(-time.Nanosecond)
	participant := tableParticipant(sel.Fixed, sel.Text, nil)
	if actual, err := sel.Evaluate([]sel.Participant{participant}, sel.Filters{TimeCut: &cut}, nil); err == nil || len(actual.Rows) != 0 {
		t.Fatal("unknown time cannot fabricate exclusion")
	}
	participant.Comments = append(participant.Comments, sel.Comment{ID: "known", Kind: sel.Text, Text: "known early", PostedAt: &early})
	actual, err := sel.Evaluate([]sel.Participant{participant}, sel.Filters{TimeCut: &cut}, nil)
	if err != nil || actual.Rows[0].Classification.Reason != sel.NoReason {
		t.Fatal("known-before witness must prove inclusion without invented missing time")
	}
	anonymous := tableParticipant(sel.Anonymous, sel.Text, nil)
	actual, err = sel.Evaluate([]sel.Participant{anonymous}, sel.Filters{ExcludeAnonymous: true, TimeCut: &cut}, nil)
	if err != nil || actual.Rows[0].Classification.Reason != sel.AnonymousReason {
		t.Fatal("higher priority exclusion is already proven")
	}
}

func TestIndependentFrozenSnapshotOwnsEveryMutableInput(t *testing.T) {
	at := time.Date(2026, 10, 6, 3, 30, 0, 0, time.UTC)
	parent := "parent"
	participant := tableParticipant(sel.Fixed, sel.Text, &at)
	participant.Comments[0].ParentID = &parent
	participant.Comments[0].MediaURLs = []string{"https://example.invalid/image"}
	second := participant
	second.Key.Identifier = "second"
	second.ID = sel.ParticipantID(second.Key)
	second.Comments = []sel.Comment{{ID: "second", Kind: sel.Text, Text: "MATCH", PostedAt: &at}}
	people := []sel.Participant{participant, second}
	author := sel.ParticipantKey{Nickname: "other", Identifier: "author"}
	filters := sel.Filters{TimeCut: &at, IncludeKeywords: []string{"MATCH"}}
	cut := at.Add(time.Minute)
	filters.TimeCut = &cut
	items := sel.Items{Mode: sel.Multiple, Multiple: []sel.Item{{ID: "a", Name: "첫째", Count: 1}, {ID: "b", Name: "둘째", Count: 1}}}
	frozen, err := sel.Freeze(people, filters, &author, items, "  축하  ", true)
	if err != nil {
		t.Fatal(err)
	}
	people[0].Comments[0].Text = "changed"
	people[0].Comments[0].MediaURLs[0] = "changed"
	parent = "changed"
	at = at.Add(time.Hour)
	cut = cut.Add(time.Hour)
	filters.IncludeKeywords[0] = "changed"
	author.Identifier = "changed"
	items.Multiple[0].Name = "changed"
	if frozen.Selection.Rows[0].Participant.Comments[0].Text != "literal [a-z]+ MATCH" || frozen.Selection.Rows[0].Participant.Comments[0].MediaURLs[0] != "https://example.invalid/image" || *frozen.Selection.Rows[0].Participant.Comments[0].ParentID != "parent" || frozen.Author.Identifier != "author" || frozen.Filters.IncludeKeywords[0] != "MATCH" || frozen.Items[0].Name != "첫째" || frozen.Message != "  축하  " {
		t.Fatal("frozen snapshot aliases caller-owned values")
	}
	if !frozen.Selection.Rows[0].Participant.Comments[0].PostedAt.Equal(time.Date(2026, 10, 6, 3, 30, 0, 0, time.UTC)) || !frozen.Filters.TimeCut.Equal(time.Date(2026, 10, 6, 3, 31, 0, 0, time.UTC)) {
		t.Fatal("frozen timestamp pointer alias")
	}
	if !reflect.DeepEqual([]string{frozen.Items[0].ID, frozen.Items[1].ID}, []string{"a", "b"}) || frozen.Selection.Included != 2 {
		t.Fatal("immutable ordering/count")
	}
}

func TestIndependentUTF16LengthCasesAndSingleConfiguredName(t *testing.T) {
	for _, sample := range []struct {
		text  string
		units int
	}{{"", 0}, {"한", 1}, {"😀", 2}, {"a😀한", 4}, {strings.Repeat("😀", 10), 20}, {strings.Repeat("😀", 10) + "a", 21}} {
		if got := sel.UTF16Length(sample.text); got != sample.units {
			t.Fatalf("%q units%d want%d", sample.text, got, sample.units)
		}
		prizes, err := sel.ValidateItems(sel.Items{Mode: sel.Single, SingleCount: 1, SingleID: "custom", SingleName: sample.text}, 2, true)
		if sample.units <= 20 {
			if err != nil || len(prizes) != 1 || prizes[0].ID != "custom" {
				t.Fatalf("valid units%d lost configured ID: %v", sample.units, err)
			}
			want := sample.text
			if want == "" {
				want = "상품"
			}
			if prizes[0].Name != want {
				t.Fatalf("name%q want%q", prizes[0].Name, want)
			}
		} else if err == nil || prizes != nil {
			t.Fatal("21 UTF16 must reject atomically")
		}
	}
}
