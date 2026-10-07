package dcinside

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestCommentBadgeUsesOfficialDisplayIconsWithoutChangingPublicIdentity(t *testing.T) {
	ref, _ := NormalizeArticle("gall.dcinside.com/test/1")
	for _, tc := range []struct{ file, nicktype, kind, category string }{
		{"fix_nik.gif", "20", "fixed", "fixed"},
		{"nik.gif", "00", "semi_fixed", "semi_fixed"},
		{"managernik.gif", "00", "semi_fixed", "main_manager"},
		{"fix_managernik.gif", "20", "fixed", "main_manager"},
		{"sub_managernik.gif", "00", "semi_fixed", "sub_manager"},
		{"fix_sub_managernik.gif", "20", "fixed", "sub_manager"},
		{"newnik.gif", "00", "semi_fixed", "new_account"},
		{"fix_newnik.gif", "20", "fixed", "new_account"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			row := oneRow("1", "1")
			row["nicktype"] = tc.nicktype
			row["gallog_icon"] = `<a><img src="//nstatic.dcinside.com/dc/w/images/` + tc.file + `" style="cursor:pointer;margin-left:2px;"></a>`
			raw, _ := json.Marshal(row)
			before := bytes.Clone(raw)
			got, err := parseComment(context.Background(), raw, ref)
			if err != nil || got.comment.BadgeCategory != tc.category || got.comment.ParticipantKind != tc.kind || got.comment.Identifier != "U1" || got.comment.Nickname != "P1" || !bytes.Equal(raw, before) {
				t.Fatalf("category/kind/identity/input changed: %+v %v", got, err)
			}
		})
	}
}

func TestOptionalUnknownBadgeFallsBackWithoutFailingOrReadingMemoIcons(t *testing.T) {
	ref, _ := NormalizeArticle("gall.dcinside.com/test/1")
	for _, icon := range []any{nil, false, map[string]string{"src": "newnik.gif"}, "", "newnik.gif", "new_account", `<span title="managernik.gif">newnik.gif</span>`, `<img data-src="newnik.gif">`, `<img src="future_badge.gif">`, `<img src="newnik.gif.png">`, `<img src="https://evil.test/dc/w/images/newnik.gif">`, `<img src="https://nstatic.dcinside.com/wrong/newnik.gif">`, `<img src="/wrong/newnik.gif">`, `<img src="javascript:newnik.gif">`, `<img src="https://user@nstatic.dcinside.com/dc/w/images/newnik.gif">`, `<img src="%zz">`, `<img src="newnik.gif"`, strings.Repeat("x", 4097)} {
		row := oneRow("1", "1")
		row["nicktype"] = "00"
		row["memo"] = `<img class="written_dccon" src="https://nstatic.dcinside.com/dc/w/images/newnik.gif">`
		row["gallog_icon"] = icon
		raw, _ := json.Marshal(row)
		got, err := parseComment(context.Background(), raw, ref)
		if err != nil || got.comment.BadgeCategory != "semi_fixed" || got.comment.ParticipantKind != "semi_fixed" || got.comment.Identifier != "U1" || got.comment.Kind != "dccon" {
			t.Fatalf("optional display metadata became identity/error: icon=%T category=%q err=%v", icon, got.comment.BadgeCategory, err)
		}
	}
	row := oneRow("1", "1")
	delete(row, "gallog_icon")
	raw, _ := json.Marshal(row)
	got, err := parseComment(context.Background(), raw, ref)
	if err != nil || got.comment.BadgeCategory != "fixed" {
		t.Fatal("missing optional badge no longer uses identity kind", got, err)
	}
}

func TestAnonymousIdentityOverridesBadgeAndMissingIdentityStillFailsClosed(t *testing.T) {
	ref, _ := NormalizeArticle("gall.dcinside.com/test/1")
	for _, icon := range []string{`<img src="fix_nik.gif">`, `<img src="managernik.gif">`, `<img src="sub_managernik.gif">`, `<img src="newnik.gif">`, ""} {
		row := oneRow("1", "1")
		row["user_id"], row["ip"], row["gallog_icon"] = "", "198.51", icon
		raw, _ := json.Marshal(row)
		got, err := parseComment(context.Background(), raw, ref)
		if err != nil || got.comment.BadgeCategory != "anonymous" || got.comment.ParticipantKind != "anonymous" || got.comment.Identifier != "198.51" {
			t.Fatal("icon crossed the IP identity namespace", got, err)
		}
		row["ip"] = ""
		raw, _ = json.Marshal(row)
		_, err = parseComment(context.Background(), raw, ref)
		code(t, err, "MissingParticipantIdentity")
	}
	for _, skip := range []string{"advertising", "deleted"} {
		row := oneRow("1", "1")
		row["user_id"], row["ip"], row["gallog_icon"] = "", "", `<img src="newnik.gif">`
		if skip == "advertising" {
			row["nicktype"] = "COMMENT_BOY"
		} else {
			row["del_yn"] = "Y"
		}
		raw, _ := json.Marshal(row)
		got, err := parseComment(context.Background(), raw, ref)
		if err != nil || got.comment.BadgeCategory != "" || skip == "advertising" && !got.advertising || skip == "deleted" && !got.deleted {
			t.Fatal("skipped row acquired a participant badge", got, err)
		}
	}
}

func TestBadgeSourceFormsBoundAndManagerPrecedence(t *testing.T) {
	for _, source := range []string{"newnik.gif", "/dc/w/images/newnik.gif", "https://nstatic.dcinside.com/dc/w/images/newnik.gif?v=1", "http://nstatic.dcinside.com/dc/w/images/newnik.gif", "//NSTATIC.DCINSIDE.COM/dc/w/images/newnik.gif"} {
		if got := commentBadgeCategory(`<IMG alt="badge" SRC="`+source+`"/>`, "fixed"); got != "new_account" {
			t.Fatal("official source form lost", source, got)
		}
	}
	icon := `<img src="newnik.gif">`
	if got := commentBadgeCategory(icon+strings.Repeat(" ", 4096-len(icon)), "fixed"); got != "new_account" {
		t.Fatal("exact optional metadata bound lost", got)
	}
	if got := commentBadgeCategory(icon+strings.Repeat(" ", 4097-len(icon)), "fixed"); got != "fixed" {
		t.Fatal("oversize optional metadata did not fall back", got)
	}
	for _, files := range [][]string{{"newnik.gif", "sub_managernik.gif", "managernik.gif"}, {"managernik.gif", "sub_managernik.gif", "newnik.gif"}, {"sub_managernik.gif", "newnik.gif"}, {"newnik.gif", "sub_managernik.gif"}} {
		var icon strings.Builder
		for _, file := range files {
			icon.WriteString(`<img src="` + file + `">`)
		}
		want := "sub_manager"
		if len(files) == 3 {
			want = "main_manager"
		}
		if got := commentBadgeCategory(icon.String(), "semi_fixed"); got != want {
			t.Fatal("official role precedence changed with HTML order", files, got)
		}
	}
}

func FuzzCommentBadgeMetadataPreservesIdentityAndNeverExecutesMarkup(f *testing.F) {
	for _, icon := range []string{"", `<img src="newnik.gif">`, `<img src="fix_sub_managernik.gif">`, `<script><img src="newnik.gif"></script>`, `<img src="%zz">`} {
		f.Add(icon)
	}
	f.Fuzz(func(t *testing.T, icon string) {
		if len(icon) > 8192 {
			return
		}
		ref, _ := NormalizeArticle("gall.dcinside.com/test/1")
		row := oneRow("1", "1")
		// nicktype 20 keeps identity stable even if arbitrary metadata contains fix_.
		row["gallog_icon"] = icon
		raw, _ := json.Marshal(row)
		before := bytes.Clone(raw)
		got, err := parseComment(context.Background(), raw, ref)
		if err != nil || got.comment.ParticipantKind != "fixed" || got.comment.Identifier != "U1" || got.comment.Nickname != "P1" || !bytes.Equal(before, raw) {
			t.Fatal("optional badge changed a required identity or mutated input", err)
		}
		if !reflect.DeepEqual(got.comment.MediaURLs, []string{}) || got.comment.Kind != "text" || got.comment.Text != "comment1" {
			t.Fatal("badge markup leaked into comment content")
		}
		switch got.comment.BadgeCategory {
		case "fixed", "main_manager", "sub_manager", "new_account":
		default:
			t.Fatal("unknown badge category", got.comment.BadgeCategory)
		}
	})
}
