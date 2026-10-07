package dcinside

import (
	"bytes"
	"context"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

type articlePage struct {
	article   Article
	nonce     string
	canonical string
}

func attributes(token html.Token) map[string]string {
	result := make(map[string]string, len(token.Attr))
	for _, attribute := range token.Attr {
		result[attribute.Key] = attribute.Val
	}
	return result
}
func hasClass(value, expected string) bool {
	for _, entry := range strings.Fields(value) {
		if entry == expected {
			return true
		}
	}
	return false
}
func parseArticle(ctx context.Context, body []byte, ref ArticleRef) (articlePage, error) {
	page := articlePage{article: Article{Ref: ref}}
	z := html.NewTokenizer(bytes.NewReader(body))
	capture := ""
	captureTag := ""
	captureDepth := 0
	var captureText strings.Builder
	writerDepth := 0
	depth := 0
	var authorAttrs map[string]string
	authorIcon := ""
	for {
		if err := context.Cause(ctx); err != nil {
			return articlePage{}, err
		}
		kind := z.Next()
		if kind == html.ErrorToken {
			if z.Err() != io.EOF {
				return articlePage{}, fault("InvalidCollectionResponse", 0, z.Err())
			}
			break
		}
		token := z.Token()
		switch kind {
		case html.StartTagToken, html.SelfClosingTagToken:
			attr := attributes(token)
			if kind == html.StartTagToken && !voidTag(token.Data) {
				depth++
			}
			if token.Data == "link" && strings.EqualFold(attr["rel"], "canonical") {
				page.canonical = attr["href"]
			}
			if token.Data == "input" && (attr["id"] == "e_s_n_o" || attr["name"] == "e_s_n_o") {
				page.nonce = attr["value"]
			}
			if hasClass(attr["class"], "title_subject") {
				capture = "title"
				captureTag = token.Data
				captureDepth = depth
				captureText.Reset()
			}
			if hasClass(attr["class"], "gallname") && attr["data-gallid"] == ref.GalleryID {
				capture = "gallery"
				captureTag = token.Data
				captureDepth = depth
				captureText.Reset()
			}
			if hasClass(attr["class"], "gall_date") && page.article.PostedAt == nil && writerDepth != 0 {
				if stamp, err := time.ParseInLocation("2006-01-02 15:04:05", attr["title"], time.FixedZone("KST", 9*3600)); err == nil {
					stamp = stamp.UTC()
					page.article.PostedAt = &stamp
				}
			}
			if hasClass(attr["class"], "gall_writer") && attr["data-loc"] == "view" && authorAttrs == nil {
				authorAttrs = attr
				writerDepth = depth
			}
			if token.Data == "img" && writerDepth != 0 {
				authorIcon += " " + attr["src"]
			}
		case html.TextToken:
			if capture != "" {
				captureText.WriteString(token.Data)
			}
		case html.EndTagToken:
			if capture != "" && token.Data == captureTag && depth == captureDepth {
				switch capture {
				case "title":
					page.article.Title = strings.TrimSpace(captureText.String())
				case "gallery":
					page.article.GalleryName = strings.TrimSpace(captureText.String())
				}
				capture = ""
			}
			if writerDepth == depth {
				writerDepth = 0
			}
			if depth > 0 {
				depth--
			}
		}
	}
	if page.article.Title == "" || page.nonce == "" || len(page.nonce) > 4096 {
		return articlePage{}, fault("InvalidCollectionResponse", 0, nil)
	}
	if authorAttrs != nil {
		id := authorAttrs["data-uid"]
		participantKind := "semi_fixed"
		if id == "" {
			id = authorAttrs["data-ip"]
			participantKind = "anonymous"
		} else if strings.Contains(authorIcon, "fix_") {
			participantKind = "fixed"
		}
		if id != "" && authorAttrs["data-nick"] != "" {
			page.article.Author = &Author{Nickname: authorAttrs["data-nick"], Identifier: id, ParticipantKind: participantKind}
		}
	}
	if page.canonical != "" {
		canonical, err := NormalizeArticle(page.canonical)
		if err != nil || canonical.GalleryID != ref.GalleryID || canonical.ArticleNo != ref.ArticleNo {
			return articlePage{}, fault("InvalidCollectionResponse", 0, nil)
		}
		page.article.Ref = canonical
		page.article.Ref.RequestURL = ref.RequestURL
	}
	return page, nil
}

// parseMemo returns plain display text. Markup is never used as product HTML.
func parseMemo(ctx context.Context, raw string) (text, kind string, media []string, err error) {
	kind = "text"
	media = []string{}
	z := html.NewTokenizer(strings.NewReader(raw))
	var out strings.Builder
	blocked := 0
	dccon := false
	unsupported := false
	for {
		if e := context.Cause(ctx); e != nil {
			return "", "", nil, e
		}
		t := z.Next()
		if t == html.ErrorToken {
			if z.Err() != io.EOF {
				return "", "", nil, z.Err()
			}
			break
		}
		token := z.Token()
		switch t {
		case html.StartTagToken, html.SelfClosingTagToken:
			if token.Data == "script" || token.Data == "style" {
				blocked++
				continue
			}
			if blocked > 0 {
				continue
			}
			if token.Data == "br" {
				out.WriteByte('\n')
			}
			if token.Data == "img" {
				attr := attributes(token)
				if hasClass(attr["class"], "written_dccon") {
					dccon = true
					if safeMediaURL(attr["src"]) {
						media = append(media, attr["src"])
					}
				} else {
					unsupported = true
				}
			}
			if token.Data == "iframe" || token.Data == "audio" {
				unsupported = true
			}
		case html.EndTagToken:
			if (token.Data == "script" || token.Data == "style") && blocked > 0 {
				blocked--
			}
		case html.TextToken:
			if blocked == 0 {
				out.WriteString(token.Data)
			}
		}
	}
	text = strings.TrimSpace(out.String())
	if dccon {
		kind = "dccon"
		if text == "" {
			text = "[디시콘]"
		}
	}
	if unsupported && text == "" && !dccon {
		kind = "unsupported"
	}
	return
}
func safeMediaURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "dcimg5.dcinside.com" || host == "dcimg4.dcinside.com" || host == "dcimg1.dcinside.com" || host == "dccon.dcinside.com" || host == "image.dcinside.com"
}
func parseDate(raw string) (*time.Time, error) {
	if len(raw) == 19 {
		value, err := time.ParseInLocation("2006.01.02 15:04:05", raw, time.FixedZone("KST", 9*3600))
		if err != nil {
			return nil, err
		}
		value = value.UTC()
		return &value, nil
	}
	if len(raw) == 14 {
		// Validate month/day/time using leap year 2000; preserve unknown year.
		if _, err := time.Parse("2006.01.02 15:04:05", "2000."+raw); err != nil {
			return nil, err
		}
		return nil, nil
	}
	return nil, fault("InvalidCollectionResponse", 0, nil)
}

type pageNavigation struct {
	current int
	last    int
}

func parseNavigation(ctx context.Context, raw string) (pageNavigation, error) {
	result := pageNavigation{}
	z := html.NewTokenizer(strings.NewReader(raw))
	current := false
	for {
		if err := context.Cause(ctx); err != nil {
			return result, err
		}
		kind := z.Next()
		if kind == html.ErrorToken {
			if z.Err() != io.EOF {
				return result, z.Err()
			}
			break
		}
		token := z.Token()
		if kind == html.StartTagToken {
			if token.Data == "em" {
				current = true
			}
			if token.Data == "a" {
				call := attributes(token)["href"]
				if !strings.HasPrefix(call, "javascript:viewComments(") {
					return result, fault("InvalidCollectionResponse", 0, nil)
				}
				rest := strings.TrimPrefix(call, "javascript:viewComments(")
				comma := strings.IndexByte(rest, ',')
				if comma < 0 {
					return result, fault("InvalidCollectionResponse", 0, nil)
				}
				n, err := strconv.Atoi(strings.TrimSpace(rest[:comma]))
				if err != nil || n < 1 || n > 100000 {
					return result, fault("InvalidCollectionResponse", 0, nil)
				}
				if n > result.last {
					result.last = n
				}
			}
		}
		if kind == html.TextToken && current {
			n, err := strconv.Atoi(strings.TrimSpace(token.Data))
			if err != nil || n < 1 || result.current != 0 {
				return result, fault("InvalidCollectionResponse", 0, nil)
			}
			result.current = n
		}
		if kind == html.EndTagToken && token.Data == "em" {
			current = false
		}
	}
	if result.current > result.last {
		result.last = result.current
	}
	return result, nil
}

func voidTag(tag string) bool {
	switch tag {
	case "area", "base", "br", "col", "embed", "hr", "img", "input", "link", "meta", "param", "source", "track", "wbr":
		return true
	}
	return false
}
