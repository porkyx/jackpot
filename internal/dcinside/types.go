package dcinside

import (
	"fmt"
	"time"
)

type GalleryKind string

const (
	General GalleryKind = "G"
	Minor   GalleryKind = "M"
	Mini    GalleryKind = "MI"
	Person  GalleryKind = "PR"
)

type ArticleRef struct {
	Kind         GalleryKind
	GalleryID    string
	ArticleNo    string
	CanonicalURL string
	RequestURL   string
}
type Author struct {
	Nickname        string
	Identifier      string
	ParticipantKind string
}
type Article struct {
	Ref         ArticleRef
	Title       string
	GalleryName string
	Author      *Author
	PostedAt    *time.Time
}
type Comment struct {
	ID              string
	ParentID        *string
	Nickname        string
	Identifier      string
	ParticipantKind string
	BadgeCategory   string
	Kind            string
	Text            string
	PostedAt        *time.Time
	DateText        string
	MediaURLs       []string
}
type Snapshot struct {
	Article             Article
	Comments            []Comment
	CollectedAt         time.Time
	Complete            bool
	Pages               uint32
	DeletedComments     uint32
	UnsupportedComments uint32
}
type Progress struct {
	Page                uint32
	AcceptedComments    uint32
	DeletedComments     uint32
	UnsupportedComments uint32
}
type Error struct {
	Code  string
	Page  uint32
	cause error
}

func (e *Error) Error() string {
	if e == nil {
		return "InvalidCollectionResponse"
	}
	return e.Code
}
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}
func fault(code string, page uint32, cause error) error {
	return &Error{Code: code, Page: page, cause: cause}
}
func invariant(ok bool, message string) {
	if !ok {
		panic(fmt.Sprintf("dcinside invariant: %s", message))
	}
}
