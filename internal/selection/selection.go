package selection

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/porkyx/jackpot/internal/contracts"
)

func invalid() error               { return contracts.NewFault(contracts.InvalidInput) }
func UTF16Length(value string) int { return len(utf16.Encode([]rune(value))) }
func DefaultFilters() Filters {
	return Filters{ExcludeAuthor: true, IncludeKeywords: []string{}, ExcludeKeywords: []string{}}
}
func ResetFilters() Filters      { return Filters{IncludeKeywords: []string{}, ExcludeKeywords: []string{}} }
func DefaultManual() ManualState { return ManualState{ManualIncluded: true} }
func validKind(kind ParticipantKind) bool {
	return kind == Fixed || kind == SemiFixed || kind == Anonymous
}
func (key ParticipantKey) Validate() error {
	if key.Nickname == "" || key.Identifier == "" || !utf8.ValidString(key.Nickname) || !utf8.ValidString(key.Identifier) {
		return invalid()
	}
	return nil
}
func ParticipantID(key ParticipantKey) contracts.ParticipantID {
	// JSON's separately encoded fields prevent concatenation/delimiter collisions.
	encoded, err := json.Marshal(key)
	if err != nil {
		panic("selection: structured key serialization failed")
	}
	sum := sha256.Sum256(encoded)
	return contracts.ParticipantID("participant-" + hex.EncodeToString(sum[:]))
}
func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
func cloneComment(value Comment) Comment {
	value.ParentID = cloneString(value.ParentID)
	value.PostedAt = cloneTime(value.PostedAt)
	value.MediaURLs = append([]string{}, value.MediaURLs...)
	return value
}
func CloneParticipants(values []Participant) []Participant {
	out := make([]Participant, len(values))
	for i, value := range values {
		out[i] = value
		out[i].Comments = make([]Comment, len(value.Comments))
		for j, comment := range value.Comments {
			out[i].Comments[j] = cloneComment(comment)
		}
	}
	return out
}
func validateComment(comment Comment) error {
	if comment.ID == "" || !utf8.ValidString(comment.ID) || !utf8.ValidString(comment.Text) || len(comment.Text) > 64<<10 || (comment.ParentID != nil && *comment.ParentID == "") || (comment.PostedAt != nil && comment.PostedAt.IsZero()) {
		return invalid()
	}
	switch comment.Kind {
	case Text:
		if comment.Text == "" {
			return invalid()
		}
	case Dccon, Voice:
	default:
		return invalid()
	}
	return nil
}
func BuildParticipants(input []InputComment) ([]Participant, error) {
	if len(input) > MaxComments {
		return nil, invalid()
	}
	out := []Participant{}
	keys := make(map[ParticipantKey]int)
	ids := make(map[string]struct{})
	bytes := 0
	for _, value := range input {
		if !validKind(value.ParticipantKind) {
			return nil, invalid()
		}
		category, err := contracts.ResolveBadgeCategory(value.BadgeCategory, string(value.ParticipantKind))
		if err != nil {
			return nil, err
		}
		key := ParticipantKey{Nickname: value.Nickname, Identifier: value.Identifier, Anonymous: value.ParticipantKind == Anonymous}
		if err := key.Validate(); err != nil {
			return nil, err
		}
		comment := Comment{ID: value.ID, ParentID: value.ParentID, Kind: value.Kind, Text: value.Text, PostedAt: value.PostedAt, MediaURLs: value.MediaURLs}
		if err := validateComment(comment); err != nil {
			return nil, err
		}
		if _, duplicate := ids[comment.ID]; duplicate {
			continue
		}
		ids[comment.ID] = struct{}{}
		bytes += len(comment.Text)
		if bytes > 100<<20 {
			return nil, invalid()
		}
		if comment.Kind == Voice {
			comment.Text = "[보플]"
		} else if comment.Kind == Dccon {
			comment.Text = "[디시콘]"
		}
		index, exists := keys[key]
		if !exists {
			index = len(out)
			keys[key] = index
			out = append(out, Participant{ID: ParticipantID(key), Key: key, Kind: value.ParticipantKind, BadgeCategory: value.BadgeCategory, Comments: []Comment{}, Manual: DefaultManual()})
		} else if badgePriority(category) > badgePriority(out[index].BadgeCategory) {
			out[index].BadgeCategory = category
		}
		out[index].Comments = append(out[index].Comments, cloneComment(comment))
	}
	return out, nil
}
func validateParticipant(value Participant) error {
	if err := value.Key.Validate(); err != nil {
		return err
	}
	if !validKind(value.Kind) || value.Key.Anonymous != (value.Kind == Anonymous) || value.ID != ParticipantID(value.Key) || len(value.Comments) == 0 || len(value.Comments) > MaxComments {
		return invalid()
	}
	if _, err := contracts.ResolveBadgeCategory(value.BadgeCategory, string(value.Kind)); err != nil {
		return err
	}
	for _, comment := range value.Comments {
		if err := validateComment(comment); err != nil {
			return err
		}
	}
	return nil
}
func NormalizeKeywords(values []string) ([]string, error) {
	out := []string{}
	seen := map[string]struct{}{}
	for _, raw := range values {
		if !utf8.ValidString(raw) {
			return nil, invalid()
		}
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		if UTF16Length(value) > 100 {
			return nil, invalid()
		}
		if _, exists := seen[value]; exists {
			continue
		}
		if len(out) == MaxKeywords {
			return nil, invalid()
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out, nil
}
func AddKeyword(values []string, value string) ([]string, error) {
	return NormalizeKeywords(append(append([]string{}, values...), value))
}
func RemoveKeyword(values []string, value string) []string {
	out := []string{}
	for _, current := range values {
		if current != value {
			out = append(out, current)
		}
	}
	return out
}
func ValidateFilters(value Filters) (Filters, error) {
	if err := value.BadgeRules.Validate(); err != nil {
		return Filters{}, err
	}
	if value.TimeCut != nil && value.TimeCut.IsZero() {
		return Filters{}, invalid()
	}
	includes, err := NormalizeKeywords(value.IncludeKeywords)
	if err != nil {
		return Filters{}, err
	}
	excludes, err := NormalizeKeywords(value.ExcludeKeywords)
	if err != nil {
		return Filters{}, err
	}
	value.IncludeKeywords = includes
	value.ExcludeKeywords = excludes
	value.TimeCut = cloneTime(value.TimeCut)
	value.BadgeRules = contracts.CloneBadgeRules(value.BadgeRules)
	return value, nil
}
func CutoffKST(now time.Time, days, hour, minute int) (time.Time, error) {
	if now.IsZero() || days < 0 || days > 365 || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return time.Time{}, invalid()
	}
	kst := time.FixedZone("Asia/Seoul", 9*60*60)
	day := now.In(kst).AddDate(0, 0, -days)
	// Cutoff is exclusive: the entire chosen minute is included.
	return time.Date(day.Year(), day.Month(), day.Day(), hour, minute, 0, 0, kst).Add(time.Minute).UTC(), nil
}
func contains(comments []Comment, keywords []string) bool {
	for _, comment := range comments {
		for _, keyword := range keywords {
			if strings.Contains(comment.Text, keyword) {
				return true
			}
		}
	}
	return false
}
func classify(value Participant, filters Filters, author *ParticipantKey) (Classification, error) {
	category, err := contracts.ResolveBadgeCategory(value.BadgeCategory, string(value.Kind))
	if err != nil {
		return Classification{}, err
	}
	rule, err := filters.BadgeRules.Rule(category)
	if err != nil {
		return Classification{}, err
	}
	group, reason := Unclassified, NoReason
	hasText, beforeCut, missingTime := false, false, false
	for _, comment := range value.Comments {
		if comment.PostedAt == nil {
			missingTime = true
		}
		if comment.Kind == Text || comment.Kind == Voice {
			hasText = true
		}
		if filters.TimeCut != nil && comment.PostedAt != nil && comment.PostedAt.Before(*filters.TimeCut) {
			beforeCut = true
		}
	}
	switch {
	case filters.ExcludeAnonymous && value.Kind == Anonymous:
		group, reason = AutoExcluded, AnonymousReason
	case rule.Excluded:
		group, reason = AutoExcluded, BadgeCategoryReason
	case filters.ExcludeAuthor && author != nil && value.Key == *author:
		group, reason = AutoExcluded, AuthorReason
	case filters.ExcludeDcconOnly && !hasText:
		group, reason = AutoExcluded, DcconReason
	case filters.TimeCut != nil && !beforeCut:
		if missingTime {
			return Classification{}, invalid()
		}
		group, reason = AutoExcluded, TimeCutReason
	case contains(value.Comments, filters.ExcludeKeywords):
		group, reason = AutoExcluded, ExcludeKeywordReason
	case contains(value.Comments, filters.IncludeKeywords):
		group, reason = AutoIncluded, IncludeKeywordReason
	}
	included := value.Manual.ManualIncluded
	if group == AutoExcluded {
		included = value.Manual.OverrideExcluded
	}
	return Classification{Group: group, Reason: reason, Included: included}, nil
}

// Special badges supersede the base identity badge without changing its key.
func badgePriority(category contracts.BadgeCategory) int {
	switch category {
	case contracts.BadgeMainManager:
		return 3
	case contracts.BadgeSubManager:
		return 2
	case contracts.BadgeNewAccount:
		return 1
	default:
		return 0
	}
}
func Evaluate(participants []Participant, filters Filters, author *ParticipantKey) (Selection, error) {
	checked, err := ValidateFilters(filters)
	if err != nil {
		return Selection{}, err
	}
	if author != nil {
		if err := author.Validate(); err != nil {
			return Selection{}, err
		}
	}
	if len(participants) > MaxComments {
		return Selection{}, invalid()
	}
	result := Selection{Rows: make([]Row, 0, len(participants)), AuthorIdentified: author != nil}
	seen := make(map[contracts.ParticipantID]struct{}, len(participants))
	comments := 0
	commentIDs := make(map[string]struct{})
	for _, value := range participants {
		if err := validateParticipant(value); err != nil {
			return Selection{}, err
		}
		if _, duplicate := seen[value.ID]; duplicate {
			return Selection{}, invalid()
		}
		seen[value.ID] = struct{}{}
		comments += len(value.Comments)
		if comments > MaxComments {
			return Selection{}, invalid()
		}
		for _, comment := range value.Comments {
			if _, duplicate := commentIDs[comment.ID]; duplicate {
				return Selection{}, invalid()
			}
			commentIDs[comment.ID] = struct{}{}
		}
		classification, err := classify(value, checked, author)
		if err != nil {
			return Selection{}, err
		}
		result.Rows = append(result.Rows, Row{Participant: value, Classification: classification})
		if classification.Included {
			result.Included++
		} else {
			result.Excluded++
		}
	}
	if result.Included+result.Excluded != len(participants) {
		panic("selection: count partition invariant violated")
	}
	return result, nil
}
func ToggleManual(participants []Participant, id contracts.ParticipantID, filters Filters, author *ParticipantKey) ([]Participant, error) {
	result, err := Evaluate(participants, filters, author)
	if err != nil {
		return nil, err
	}
	out := append([]Participant{}, participants...)
	for index, row := range result.Rows {
		if row.Participant.ID == id {
			if row.Classification.Group == AutoExcluded {
				out[index].Manual.OverrideExcluded = !out[index].Manual.OverrideExcluded
			} else {
				out[index].Manual.ManualIncluded = !out[index].Manual.ManualIncluded
			}
			return out, nil
		}
	}
	return nil, invalid()
}
func SetUnclassified(participants []Participant, filters Filters, author *ParticipantKey, included bool) ([]Participant, error) {
	result, err := Evaluate(participants, filters, author)
	if err != nil {
		return nil, err
	}
	out := append([]Participant{}, participants...)
	for index, row := range result.Rows {
		if row.Classification.Group == Unclassified {
			out[index].Manual.ManualIncluded = included
		}
	}
	return out, nil
}
func ResetManual(participants []Participant) []Participant {
	out := append([]Participant{}, participants...)
	for i := range out {
		out[i].Manual = DefaultManual()
	}
	return out
}
func Search(rows []Row, query string) []Row {
	query = strings.ToLower(query)
	out := []Row{}
	for _, row := range rows {
		if strings.Contains(strings.ToLower(row.Participant.Key.Nickname), query) || strings.Contains(strings.ToLower(row.Participant.Key.Identifier), query) {
			out = append(out, row)
		}
	}
	return out
}
func Freeze(participants []Participant, filters Filters, author *ParticipantKey, items Items, message string, initial bool) (FrozenSelection, error) {
	checked, err := ValidateFilters(filters)
	if err != nil {
		return FrozenSelection{}, err
	}
	result, err := Evaluate(CloneParticipants(participants), checked, author)
	if err != nil {
		return FrozenSelection{}, err
	}
	prizes, err := ValidateItems(items, result.Included, initial)
	if err != nil {
		return FrozenSelection{}, err
	}
	if err := ValidateMessage(message); err != nil {
		return FrozenSelection{}, err
	}
	var authorCopy *ParticipantKey
	if author != nil {
		copy := *author
		authorCopy = &copy
	}
	return FrozenSelection{Selection: result, Filters: checked, Author: authorCopy, Items: prizes, Message: message, RulesVersion: RulesVersion}, nil
}
