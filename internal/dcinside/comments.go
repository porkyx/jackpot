package dcinside

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strconv"
	"strings"
)

type wirePage struct {
	total      int
	rows       []json.RawMessage
	navigation pageNavigation
}

func scalar(raw json.RawMessage) (string, error) {
	var value string
	if len(raw) > 0 && raw[0] == '"' {
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", err
		}
		return value, nil
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err != nil {
		return "", err
	}
	value = number.String()
	if value == "" {
		return "", fault("InvalidCollectionResponse", 0, nil)
	}
	return value, nil
}
func nonnegative(raw json.RawMessage) (int, error) {
	value, err := scalar(raw)
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 {
		return 0, fault("InvalidCollectionResponse", 0, nil)
	}
	return n, nil
}
func parseCommentPage(ctx context.Context, body []byte) (wirePage, error) {
	var fields map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&fields); err != nil {
		return wirePage{}, fault("InvalidCollectionResponse", 0, err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return wirePage{}, fault("InvalidCollectionResponse", 0, err)
	}
	if err := context.Cause(ctx); err != nil {
		return wirePage{}, err
	}
	if fields == nil || fields["total_cnt"] == nil || fields["comments"] == nil || fields["pagination"] == nil || fields["allow_reply"] == nil {
		return wirePage{}, fault("InvalidCollectionResponse", 0, nil)
	}
	total, err := nonnegative(fields["total_cnt"])
	if err != nil {
		return wirePage{}, fault("InvalidCollectionResponse", 0, err)
	}
	if total > maxComments {
		return wirePage{}, fault("CollectionLimitExceeded", 0, nil)
	}
	reply, err := nonnegative(fields["allow_reply"])
	if err != nil || reply > 1 {
		return wirePage{}, fault("InvalidCollectionResponse", 0, err)
	}
	var rows []json.RawMessage
	if !bytes.Equal(fields["comments"], []byte("null")) {
		if err := json.Unmarshal(fields["comments"], &rows); err != nil {
			return wirePage{}, fault("InvalidCollectionResponse", 0, err)
		}
	}
	if len(rows) > maxComments {
		return wirePage{}, fault("CollectionLimitExceeded", 0, nil)
	}
	var pagination string
	if !bytes.Equal(fields["pagination"], []byte("null")) {
		if err := json.Unmarshal(fields["pagination"], &pagination); err != nil {
			return wirePage{}, fault("InvalidCollectionResponse", 0, err)
		}
	}
	navigation, err := parseNavigation(ctx, pagination)
	if err != nil {
		return wirePage{}, err
	}
	return wirePage{total: total, rows: rows, navigation: navigation}, nil
}

type rowResult struct {
	comment     Comment
	deleted     bool
	unsupported bool
	advertising bool
}

func parseComment(ctx context.Context, raw json.RawMessage, article ArticleRef) (rowResult, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return rowResult{}, fault("InvalidCollectionResponse", 0, err)
	}
	get := func(key string) string { var value string; _ = json.Unmarshal(fields[key], &value); return value }
	if get("nicktype") == "COMMENT_BOY" {
		return rowResult{advertising: true}, nil
	}
	id, err := scalar(fields["no"])
	if err != nil || !articleNumberPattern.MatchString(id) {
		return rowResult{}, fault("InvalidCollectionResponse", 0, err)
	}
	parent, err := scalar(fields["parent"])
	if err != nil || parent != article.ArticleNo {
		return rowResult{}, fault("InvalidCollectionResponse", 0, err)
	}
	for _, field := range []string{"del_yn", "is_delete"} {
		var value string
		if fields[field] == nil || json.Unmarshal(fields[field], &value) != nil {
			return rowResult{}, fault("InvalidCollectionResponse", 0, nil)
		}
	}
	result := rowResult{comment: Comment{ID: id, MediaURLs: []string{}}}
	if get("del_yn") == "Y" || (get("is_delete") != "" && get("is_delete") != "0") {
		result.deleted = true
		return result, nil
	}
	for _, field := range []string{"name", "user_id", "ip", "reg_date", "nicktype", "memo", "depth", "c_no"} {
		if fields[field] == nil {
			return rowResult{}, fault("InvalidCollectionResponse", 0, nil)
		}
		if field != "depth" && field != "c_no" {
			var value string
			if json.Unmarshal(fields[field], &value) != nil {
				return rowResult{}, fault("InvalidCollectionResponse", 0, nil)
			}
		}
	}
	result.comment.Nickname = get("name")
	if result.comment.Nickname == "" {
		return rowResult{}, fault("MissingParticipantIdentity", 0, nil)
	}
	idMaterial := get("user_id")
	participantKind := "semi_fixed"
	if idMaterial == "" {
		idMaterial = get("ip")
		participantKind = "anonymous"
	} else if get("nicktype") == "20" || strings.Contains(get("gallog_icon"), "fix_") {
		participantKind = "fixed"
	}
	if idMaterial == "" {
		return rowResult{}, fault("MissingParticipantIdentity", 0, nil)
	}
	if len(result.comment.Nickname) > 4096 || len(idMaterial) > 4096 {
		return rowResult{}, fault("CollectionLimitExceeded", 0, nil)
	}
	result.comment.Identifier = idMaterial
	result.comment.ParticipantKind = participantKind
	result.comment.BadgeCategory = commentBadgeCategory(get("gallog_icon"), participantKind)
	depth, err := nonnegative(fields["depth"])
	if err != nil || depth > 1 {
		return rowResult{}, fault("InvalidCollectionResponse", 0, err)
	}
	if depth == 1 {
		relation, err := scalar(fields["c_no"])
		if err != nil || !articleNumberPattern.MatchString(relation) || relation == id {
			return rowResult{}, fault("InvalidCollectionResponse", 0, err)
		}
		result.comment.ParentID = &relation
	}
	date := get("reg_date")
	posted, err := parseDate(date)
	if err != nil {
		return rowResult{}, fault("InvalidCollectionResponse", 0, err)
	}
	result.comment.PostedAt = posted
	result.comment.DateText = date
	memo := get("memo")
	if len(memo) > maxCommentBytes {
		return rowResult{}, fault("CollectionLimitExceeded", 0, nil)
	}
	text, kind, media, err := parseMemo(ctx, memo)
	if err != nil {
		return rowResult{}, err
	}
	if len(text) > maxCommentBytes {
		return rowResult{}, fault("CollectionLimitExceeded", 0, nil)
	}
	voice := fields["voice"]
	if (len(voice) > 0 && !bytes.Equal(voice, []byte("null")) && !bytes.Equal(voice, []byte("false")) && !bytes.Equal(voice, []byte("0")) && !bytes.Equal(voice, []byte(`""`))) || get("vr_type") != "" || bytes.Equal(fields["vr_player"], []byte("true")) {
		text = "[보플]"
		kind = "voice"
		media = []string{}
	}
	if kind == "unsupported" {
		result.unsupported = true
	}
	result.comment.Text = text
	result.comment.Kind = kind
	result.comment.MediaURLs = media
	return result, nil
}
