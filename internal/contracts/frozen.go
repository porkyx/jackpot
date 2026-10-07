package contracts

// Frozen queries address only the immutable durable collection, so read access
// survives a Go session restart. A supplied revision fences the whole page.
type FrozenParticipantsQuery struct {
	CollectionID     CollectionID `json:"collectionId"`
	ExpectedRevision *Revision    `json:"expectedRevision"`
	Query            string       `json:"query"`
	Group            string       `json:"group"`
	Offset           uint32       `json:"offset"`
	Limit            uint32       `json:"limit"`
}
type FrozenCommentsQuery struct {
	CollectionID     CollectionID  `json:"collectionId"`
	ExpectedRevision *Revision     `json:"expectedRevision"`
	ParticipantID    ParticipantID `json:"participantId"`
	Offset           uint32        `json:"offset"`
	Limit            uint32        `json:"limit"`
}
type FrozenParticipantsPage struct {
	CollectionID CollectionID      `json:"collectionId"`
	Revision     Revision          `json:"revision"`
	Total        uint32            `json:"total"`
	Matched      uint32            `json:"matched"`
	Offset       uint32            `json:"offset"`
	Rows         []ParticipantData `json:"rows"`
}
type FrozenCommentsPage struct {
	CollectionID  CollectionID  `json:"collectionId"`
	Revision      Revision      `json:"revision"`
	ParticipantID ParticipantID `json:"participantId"`
	Total         uint32        `json:"total"`
	Offset        uint32        `json:"offset"`
	Rows          []CommentData `json:"rows"`
}
type FrozenParticipantsResponse Envelope[FrozenParticipantsPage]

func (response FrozenParticipantsResponse) Validate() error {
	return Envelope[FrozenParticipantsPage](response).Validate()
}

type FrozenCommentsResponse Envelope[FrozenCommentsPage]

func (response FrozenCommentsResponse) Validate() error {
	return Envelope[FrozenCommentsPage](response).Validate()
}
