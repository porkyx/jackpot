package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const largeParticipantCount = 100
const largeCommentCount = 200
const largeCommentPages = 2
const largeGalleryID = "producte2eaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const largeArticleNo = "2147483647"
const largeFixtureURL = "https://gall.dcinside.com/board/view/?id=" + largeGalleryID + "&no=" + largeArticleNo

func (transport *fixtureTransport) articleIdentity() (string, string, string) {
	if transport.large {
		return largeFixtureURL, largeGalleryID, largeArticleNo
	}
	return fixtureURL, "producte2e", "1"
}

func largeArticle() string {
	title := "로컬 댓글 추첨 한글 😀 👨‍👩‍👧‍👦 é 👍🏽 검증"
	return strings.NewReplacer(fixtureURL, largeFixtureURL, `data-gallid="producte2e"`, `data-gallid="`+largeGalleryID+`"`, "한글 Alpha 로컬 추첨 검증", title).Replace(fixtureArticle())
}

// Participant0 has60comments to exercise the50-row detail boundary.
// Participants1..58 have1 each and the rest2 each: exactly100/200.
func largeParticipantForComment(index int) int {
	if index < 60 {
		return 0
	}
	if index < 118 {
		return index - 59
	}
	return 59 + (index-118)/2
}
func largeComments(page int) ([]byte, error) {
	return localComments(page, largeCommentCount)
}
func localComments(page, count int) ([]byte, error) {
	if count != 100 && count != 200 {
		return nil, errors.New("local fixture comment count must be 100 or 200")
	}
	if page < 1 || page > largeCommentPages {
		return nil, errors.New("large fixture page out of bounds")
	}
	perPage := count / largeCommentPages
	rows := make([]map[string]any, 0, perPage)
	for index := (page - 1) * perPage; index < page*perPage; index++ {
		person := largeParticipantForComment(index)
		memo := fmt.Sprintf("댓글 %05d 한글 😀 <b>저장된 본문</b>", index)
		if index == 0 {
			memo = `<img class="written_dccon" src="https://dcimg5.dcinside.com/test-owned-image.png">`
		}
		rows = append(rows, map[string]any{"no": fmt.Sprint(index + 1), "parent": largeArticleNo, "name": fmt.Sprintf("참가자 %05d 한글 😀 %s", person, strings.Repeat("긴닉네임👨‍👩‍👧‍👦 ", 4)), "user_id": fmt.Sprintf("stress-user-%05d", person), "ip": "", "reg_date": "2026.10.06 12:00:00", "nicktype": "20", "memo": memo, "depth": 0, "c_no": 0, "is_delete": "0", "del_yn": "N"})
	}
	nav := fmt.Sprintf("<em>%d</em>", page)
	if page < largeCommentPages {
		nav += `<a href="javascript:viewComments(2,'D',true)">last</a>`
	}
	return json.Marshal(map[string]any{"total_cnt": count, "comment_cnt": 0, "comments": rows, "pagination": nav, "allow_reply": 1})
}
