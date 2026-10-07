package contracts

import "time"

// Product DTOs expose read projections only. Frozen inputs and verifiers never
// cross IPC; the application constructs them from its confirmed draft.
type ArticleData struct {
	URL              string     `json:"url"`
	Title            string     `json:"title"`
	GalleryID        string     `json:"galleryId"`
	GalleryName      string     `json:"galleryName"`
	GalleryKind      string     `json:"galleryKind"`
	Number           string     `json:"number"`
	AuthorNickname   string     `json:"authorNickname"`
	AuthorIdentifier *string    `json:"authorIdentifier"`
	PostedAt         *time.Time `json:"postedAt"`
}
type FilterConfiguration struct {
	BadgeRules       *BadgeRules `json:"badgeRules,omitempty"`
	ExcludeAnonymous bool        `json:"excludeAnonymous"`
	ExcludeAuthor    bool        `json:"excludeAuthor"`
	ExcludeDcconOnly bool        `json:"excludeDcconOnly"`
	TimeCut          *time.Time  `json:"timeCut"`
	IncludeKeywords  []string    `json:"includeKeywords"`
	ExcludeKeywords  []string    `json:"excludeKeywords"`
}
type PrizeInput struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Count uint32 `json:"count"`
}
type PrizeConfiguration struct {
	Mode     string       `json:"mode"`
	Single   PrizeInput   `json:"single"`
	Multiple []PrizeInput `json:"multiple"`
	DrawMode string       `json:"drawMode"`
}
type LoadProgress struct {
	OperationID OperationID `json:"operationId"`
	Sequence    uint64      `json:"sequence"`
	Pages       uint32      `json:"pages"`
	Comments    uint32      `json:"comments"`
	State       string      `json:"state"`
	FailureCode *ErrorCode  `json:"failureCode"`
}
type DraftData struct {
	Summary            DraftSummary        `json:"summary"`
	Article            *ArticleData        `json:"article"`
	Filters            FilterConfiguration `json:"filters"`
	Prizes             PrizeConfiguration  `json:"prizes"`
	Participants       uint32              `json:"participants"`
	Included           uint32              `json:"included"`
	Excluded           uint32              `json:"excluded"`
	AuthorIdentifiable bool                `json:"authorIdentifiable"`
	Load               *LoadProgress       `json:"load"`
}
type DraftResponse Envelope[DraftData]

func (response DraftResponse) Validate() error { return Envelope[DraftData](response).Validate() }

type DraftQuery struct {
	BackendSessionID BackendSessionID `json:"backendSessionId"`
	DraftID          DraftID          `json:"draftId"`
}
type CreateDraftRequest struct{ MutationHeader }
type LoadArticleRequest struct {
	DraftMutationHeader
	URL string `json:"url"`
}
type DraftEditRequest struct {
	DraftMutationHeader
	Kind          string               `json:"kind"`
	Filters       *FilterConfiguration `json:"filters"`
	Prizes        *PrizeConfiguration  `json:"prizes"`
	ParticipantID ParticipantID        `json:"participantId"`
	Included      *bool                `json:"included"`
}
type ParticipantQuery struct {
	DraftContext
	Query  string `json:"query"`
	Offset uint32 `json:"offset"`
	Limit  uint32 `json:"limit"`
	Group  string `json:"group"`
}
type ParticipantData struct {
	BadgeCategory    BadgeCategory `json:"badgeCategory"`
	ID               ParticipantID `json:"id"`
	Nickname         string        `json:"nickname"`
	PublicIdentifier string        `json:"publicIdentifier"`
	Kind             string        `json:"kind"`
	Classification   string        `json:"classification"`
	Reason           string        `json:"reason"`
	Included         bool          `json:"included"`
	CommentCount     uint32        `json:"commentCount"`
	Previews         []string      `json:"previews"`
}
type ParticipantsPage struct {
	Context DraftContext      `json:"context"`
	Total   uint32            `json:"total"`
	Matched uint32            `json:"matched"`
	Offset  uint32            `json:"offset"`
	Rows    []ParticipantData `json:"rows"`
}
type ParticipantsResponse Envelope[ParticipantsPage]

func (response ParticipantsResponse) Validate() error {
	return Envelope[ParticipantsPage](response).Validate()
}

type ParticipantCommentsQuery struct {
	DraftContext
	ParticipantID ParticipantID `json:"participantId"`
	Offset        uint32        `json:"offset"`
	Limit         uint32        `json:"limit"`
}
type CommentData struct {
	ID        string     `json:"id"`
	ParentID  *string    `json:"parentId"`
	Kind      string     `json:"kind"`
	Text      string     `json:"text"`
	PostedAt  *time.Time `json:"postedAt"`
	MediaURLs []string   `json:"mediaUrls"`
}
type CommentsPage struct {
	Context       DraftContext  `json:"context"`
	ParticipantID ParticipantID `json:"participantId"`
	Total         uint32        `json:"total"`
	Offset        uint32        `json:"offset"`
	Rows          []CommentData `json:"rows"`
}
type CommentsResponse Envelope[CommentsPage]

func (response CommentsResponse) Validate() error { return Envelope[CommentsPage](response).Validate() }

type CreateCollectionRequest struct {
	DraftMutationHeader
	AfterSequence uint64 `json:"afterSequence"`
	Mode          string `json:"mode"`
}
type CollectionQuery struct {
	CollectionID CollectionID `json:"collectionId"`
	RoundOffset  uint32       `json:"roundOffset"`
	RoundLimit   uint32       `json:"roundLimit"`
	RoundID      RoundID      `json:"roundId"`
}
type RoundQuery struct {
	RoundID RoundID `json:"roundId"`
}
type WinnerData struct {
	Participant ParticipantData `json:"participant"`
	PrizeID     string          `json:"prizeId"`
	PrizeName   string          `json:"prizeName"`
	Slot        uint32          `json:"slot"`
}
type RoundData struct {
	CollectionID     CollectionID `json:"collectionId"`
	RoundID          RoundID      `json:"roundId"`
	Number           uint32       `json:"number"`
	Attempt          uint32       `json:"attempt"`
	State            RoundState   `json:"state"`
	Revision         Revision     `json:"revision"`
	RoundVersion     RoundVersion `json:"roundVersion"`
	Mode             string       `json:"mode"`
	Message          string       `json:"message"`
	Prizes           []PrizeInput `json:"prizes"`
	ScheduledAt      *time.Time   `json:"scheduledAt"`
	ExecutedAt       *time.Time   `json:"executedAt"`
	FailureCode      *ErrorCode   `json:"failureCode"`
	Winners          []WinnerData `json:"winners"`
	AlgorithmVersion string       `json:"algorithmVersion"`
	AppVersion       string       `json:"appVersion"`
}
type CollectionData struct {
	CollectionID     CollectionID        `json:"collectionId"`
	Revision         Revision            `json:"revision"`
	Article          ArticleData         `json:"article"`
	Snapshot         SnapshotSummary     `json:"snapshot"`
	Filters          FilterConfiguration `json:"filters"`
	ParticipantCount uint32              `json:"participantCount"`
	SelectedCount    uint32              `json:"selectedCount"`
	RemainingCount   uint32              `json:"remainingCount"`
	Rounds           []RoundData         `json:"rounds"`
	RoundTotal       uint32              `json:"roundTotal"`
	RoundOffset      uint32              `json:"roundOffset"`
	LatestRound      RoundData           `json:"latestRound"`
}
type CollectionResponse Envelope[CollectionData]

func (response CollectionResponse) Validate() error {
	return Envelope[CollectionData](response).Validate()
}

type RoundResponse Envelope[RoundData]

func (response RoundResponse) Validate() error { return Envelope[RoundData](response).Validate() }

type CollectionCommandRequest struct {
	QuickDelaySeconds *uint32 `json:"quickDelaySeconds"`
	MutationHeader
	CollectionID    CollectionID  `json:"collectionId"`
	RoundID         RoundID       `json:"roundId"`
	ExpectedVersion *RoundVersion `json:"expectedVersion"`
	Prizes          []PrizeInput  `json:"prizes"`
	Message         string        `json:"message"`
	Mode            string        `json:"mode"`
	ScheduledAt     *time.Time    `json:"scheduledAt"`
}
type DraftOperationData struct {
	OperationID OperationID    `json:"operationId"`
	State       OperationState `json:"state"`
	Summary     *DraftSummary  `json:"summary"`
	FailureCode *ErrorCode     `json:"failureCode"`
}
type DraftOperationResponse Envelope[DraftOperationData]

func (response DraftOperationResponse) Validate() error {
	return Envelope[DraftOperationData](response).Validate()
}
