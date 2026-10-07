package dcinside

import (
	"net/url"
	"path"
	"strings"

	"golang.org/x/net/html"
)

// gallog_icon is the HTML rendered by DC's comment.js. These filenames were
// observed in its public HTML and verified against the official static assets.
// The badge is separate from the UID/IP namespace used for participant identity.
func commentBadgeCategory(icon, participantKind string) string {
	if participantKind == "anonymous" || len(icon) > 4096 {
		return participantKind
	}
	category := participantKind
	z := html.NewTokenizer(strings.NewReader(icon))
	for {
		kind := z.Next()
		if kind == html.ErrorToken {
			return category
		}
		if kind != html.StartTagToken && kind != html.SelfClosingTagToken {
			continue
		}
		token := z.Token()
		if token.Data != "img" {
			continue
		}
		for _, attr := range token.Attr {
			if attr.Key != "src" {
				continue
			}
			parsed, err := url.Parse(attr.Val)
			if err != nil || parsed.User != nil || parsed.Scheme != "" && parsed.Scheme != "https" && parsed.Scheme != "http" {
				continue
			}
			name := path.Base(parsed.Path)
			if parsed.Host != "" {
				if !strings.EqualFold(parsed.Host, "nstatic.dcinside.com") || parsed.Path != "/dc/w/images/"+name {
					continue
				}
			} else if parsed.Path != name && parsed.Path != "/dc/w/images/"+name {
				continue
			}
			switch name {
			case "managernik.gif", "fix_managernik.gif":
				return "main_manager"
			case "sub_managernik.gif", "fix_sub_managernik.gif":
				category = "sub_manager"
			case "newnik.gif", "fix_newnik.gif":
				if category != "sub_manager" {
					category = "new_account"
				}
			}
		}
	}
}
