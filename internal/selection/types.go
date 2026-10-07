// Package selection owns participant identity, pure classification and frozen draw inputs.
package selection

import (
	"github.com/porkyx/jackpot/internal/contracts"
	"time"
)

const RulesVersion = "normal-draw-selection-v1"
const MaxComments = 100000
const MaxKeywords = 100

type ParticipantKind string

const (
	Fixed     ParticipantKind = "fixed"
	SemiFixed ParticipantKind = "semi_fixed"
	Anonymous ParticipantKind = "anonymous"
)

type CommentKind string

const (
	Text  CommentKind = "text"
	Dccon CommentKind = "dccon"
	Voice CommentKind = "voice"
)

// Login IDs and public IP identifiers occupy separate identity namespaces.
// Fixed and semi-fixed share the login-ID namespace; nickname is never discarded.
type ParticipantKey struct {
	Nickname   string `json:"nickname"`
	Identifier string `json:"identifier"`
	Anonymous  bool   `json:"anonymous"`
}
type InputComment struct {
	BadgeCategory   contracts.BadgeCategory `json:"badgeCategory,omitempty"`
	ID              string                  `json:"id"`
	ParentID        *string                 `json:"parentId"`
	Nickname        string                  `json:"nickname"`
	Identifier      string                  `json:"identifier"`
	ParticipantKind ParticipantKind         `json:"participantKind"`
	Kind            CommentKind             `json:"kind"`
	Text            string                  `json:"text"`
	PostedAt        *time.Time              `json:"postedAt"`
	MediaURLs       []string                `json:"mediaUrls"`
}
type Comment struct {
	ID        string      `json:"id"`
	ParentID  *string     `json:"parentId"`
	Kind      CommentKind `json:"kind"`
	Text      string      `json:"text"`
	PostedAt  *time.Time  `json:"postedAt"`
	MediaURLs []string    `json:"mediaUrls"`
}
type ManualState struct {
	ManualIncluded   bool `json:"manualIncluded"`
	OverrideExcluded bool `json:"overrideExcluded"`
}
type Participant struct {
	BadgeCategory contracts.BadgeCategory `json:"badgeCategory,omitempty"`
	ID            contracts.ParticipantID `json:"id"`
	Key           ParticipantKey          `json:"key"`
	Kind          ParticipantKind         `json:"kind"`
	Comments      []Comment               `json:"comments"`
	Manual        ManualState             `json:"manual"`
}
type Filters struct {
	BadgeRules       *contracts.BadgeRules `json:"badgeRules,omitempty"`
	ExcludeAnonymous bool                  `json:"excludeAnonymous"`
	ExcludeAuthor    bool                  `json:"excludeAuthor"`
	ExcludeDcconOnly bool                  `json:"excludeDcconOnly"`
	TimeCut          *time.Time            `json:"timeCut"`
	IncludeKeywords  []string              `json:"includeKeywords"`
	ExcludeKeywords  []string              `json:"excludeKeywords"`
}
type Group string

const (
	Unclassified Group = "unclassified"
	AutoIncluded Group = "included"
	AutoExcluded Group = "excluded"
)

type Reason string

const (
	NoReason             Reason = ""
	AnonymousReason      Reason = "anonymous"
	BadgeCategoryReason  Reason = "badge_category"
	AuthorReason         Reason = "author"
	DcconReason          Reason = "dccon"
	TimeCutReason        Reason = "time_cut"
	ExcludeKeywordReason Reason = "exclude_keyword"
	IncludeKeywordReason Reason = "include_keyword"
)

type Classification struct {
	Group    Group  `json:"group"`
	Reason   Reason `json:"reason"`
	Included bool   `json:"included"`
}
type Row struct {
	Participant    Participant    `json:"participant"`
	Classification Classification `json:"classification"`
}
type Selection struct {
	Rows             []Row `json:"rows"`
	Included         int   `json:"included"`
	Excluded         int   `json:"excluded"`
	AuthorIdentified bool  `json:"authorIdentified"`
}
type ItemMode string

const (
	Single   ItemMode = "single"
	Multiple ItemMode = "multiple"
)

type Item struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Count int    `json:"count"`
}
type Items struct {
	Mode        ItemMode `json:"mode"`
	SingleCount int      `json:"singleCount"`
	SingleID    string   `json:"singleId"`
	SingleName  string   `json:"singleName"`
	Multiple    []Item   `json:"multiple"`
}
type FrozenSelection struct {
	Selection    Selection       `json:"selection"`
	Filters      Filters         `json:"filters"`
	Author       *ParticipantKey `json:"author"`
	Items        []Item          `json:"items"`
	Message      string          `json:"message"`
	RulesVersion string          `json:"rulesVersion"`
}
