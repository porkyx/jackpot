package dcinside

import (
	"context"
	"testing"
)

func FuzzNormalizeArticle(f *testing.F) {
	for _, seed := range []string{"gall.dcinside.com/test/1", "https://m.dcinside.com/board/test/1", "https://gall.dcinside.com/mini/board/view/?id=test&no=1", "https://user@gall.dcinside.com/test/1", "file:///C:/x", "https://gall.dcinside.com/x/1\n"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		if len(raw) > 4096 {
			return
		}
		ref, err := NormalizeArticle(raw)
		if err != nil {
			return
		}
		again, e := NormalizeArticle(ref.CanonicalURL)
		if e != nil || again.GalleryID != ref.GalleryID || again.ArticleNo != ref.ArticleNo || again.Kind != ref.Kind {
			t.Fatal("canonical identity unstable")
		}
	})
}
func FuzzParseArticle(f *testing.F) {
	f.Add(articleHTML(General, "test", "1"))
	f.Add("<script>unsafe</script>")
	f.Add("<span class=title_subject>x")
	f.Fuzz(func(t *testing.T, raw string) {
		if len(raw) > 64<<10 {
			return
		}
		ref, _ := NormalizeArticle("gall.dcinside.com/test/1")
		parsed, err := parseArticle(context.Background(), []byte(raw), ref)
		if err == nil && (parsed.article.Title == "" || parsed.nonce == "") {
			t.Fatal("incomplete article accepted")
		}
	})
}
func FuzzParseCommentPage(f *testing.F) {
	f.Add(string(wireJSON(1, 1, 1, oneRow("1", "1"))))
	f.Add(`{"total_cnt":0,"comments":null,"pagination":null,"allow_reply":1}`)
	f.Add("null")
	f.Fuzz(func(t *testing.T, raw string) {
		if len(raw) > 64<<10 {
			return
		}
		page, err := parseCommentPage(context.Background(), []byte(raw))
		if err == nil && (page.total < 0 || page.total > maxComments || len(page.rows) > maxComments) {
			t.Fatal("page bounds")
		}
	})
}
func FuzzCommentMemoAndDate(f *testing.F) {
	for _, seed := range []string{"text", "<img class=written_dccon src=javascript:unsafe>", "<script>unsafe</script><b>safe</b>", "10.06 12:00:00", "2025.10.06 12:00:00"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		if len(raw) > 64<<10 {
			return
		}
		text, kind, urls, err := parseMemo(context.Background(), raw)
		if err == nil {
			if len(text) > len(raw)+len("[디시콘]") || kind != "text" && kind != "dccon" && kind != "unsupported" {
				t.Fatal("memo invariant")
			}
			for _, u := range urls {
				if !safeMediaURL(u) {
					t.Fatal("unsafe media")
				}
			}
		}
		_, _ = parseDate(raw)
	})
}
