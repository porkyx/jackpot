package dcinside

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var galleryIDPattern = regexp.MustCompile("^[A-Za-z0-9_]{1,64}$")
var articleNumberPattern = regexp.MustCompile("^[1-9][0-9]{0,15}$")

// NormalizeArticle validates an article URL without making a network request.
// Mobile /board URLs are ambiguous between G and M; Collect resolves the kind
// from the same-article canonical URL instead of guessing the gallery type.
func NormalizeArticle(raw string) (ArticleRef, error) {
	bad := func() (ArticleRef, error) { return ArticleRef{}, fault("InvalidArticleURL", 0, nil) }
	if strings.ContainsAny(raw, "\r\n\t\\") {
		return bad()
	}
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 4096 || strings.ContainsAny(raw, "\r\n\t\\") {
		return bad()
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Opaque != "" || (u.Scheme != "http" && u.Scheme != "https") || u.Port() != "" {
		return bad()
	}
	host := strings.ToLower(u.Hostname())
	if host != "gall.dcinside.com" && host != "m.dcinside.com" {
		return bad()
	}
	if u.RawPath != "" || strings.Contains(u.EscapedPath(), "%") {
		return bad()
	}
	kind := General
	gallery := ""
	number := ""
	prefixes := map[string]GalleryKind{"/board/view/": General, "/mgallery/board/view/": Minor, "/mini/board/view/": Mini, "/person/board/view/": Person}
	if k, ok := prefixes[u.Path]; ok {
		if host != "gall.dcinside.com" {
			return bad()
		}
		query, err := url.ParseQuery(u.RawQuery)
		if err != nil || len(query["id"]) != 1 || len(query["no"]) != 1 {
			return bad()
		}
		kind = k
		gallery = query.Get("id")
		number = query.Get("no")
	} else {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if u.Path != "/"+strings.Join(parts, "/") {
			return bad()
		}
		switch {
		case len(parts) == 2 && host == "gall.dcinside.com":
			gallery = parts[0]
			number = parts[1]
		case len(parts) == 3:
			switch parts[0] {
			case "board":
				if host != "m.dcinside.com" {
					return bad()
				}
				kind = General
			case "mgallery":
				if host != "gall.dcinside.com" {
					return bad()
				}
				kind = Minor
			case "mini", "n":
				kind = Mini
			case "person":
				kind = Person
			default:
				return bad()
			}
			gallery = parts[1]
			number = parts[2]
		default:
			return bad()
		}
	}
	if !galleryIDPattern.MatchString(gallery) || !articleNumberPattern.MatchString(number) {
		return bad()
	}
	n, err := strconv.ParseUint(number, 10, 64)
	if err != nil || n > 9007199254740991 {
		return bad()
	}
	prefix := map[GalleryKind]string{General: "/board/view/", Minor: "/mgallery/board/view/", Mini: "/mini/board/view/", Person: "/person/board/view/"}[kind]
	canonical := "https://gall.dcinside.com" + prefix + "?" + url.Values{"id": {gallery}, "no": {number}}.Encode()
	request := canonical
	if host == "m.dcinside.com" {
		request = "https://m.dcinside.com" + u.Path
	}
	return ArticleRef{Kind: kind, GalleryID: gallery, ArticleNo: number, CanonicalURL: canonical, RequestURL: request}, nil
}
