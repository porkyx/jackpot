package application

import (
	"context"

	"github.com/porkyx/jackpot/internal/contracts"
	"github.com/porkyx/jackpot/internal/dcinside"
	"github.com/porkyx/jackpot/internal/selection"
)

type DCCollector interface {
	Collect(context.Context, string, func(dcinside.Progress)) (dcinside.Snapshot, error)
}
type DCCollectorAdapter struct{ Collector DCCollector }

func (adapter DCCollectorAdapter) Collect(ctx context.Context, url string, progress func(CollectionProgress)) (CollectionSnapshot, error) {
	if adapter.Collector == nil {
		return CollectionSnapshot{}, contracts.NewFault(contracts.InvalidState)
	}
	snapshot, err := adapter.Collector.Collect(ctx, url, func(value dcinside.Progress) {
		if progress != nil {
			progress(CollectionProgress{Pages: value.Page, Comments: value.AcceptedComments})
		}
	})
	if err != nil {
		return CollectionSnapshot{}, err
	}
	if !snapshot.Complete {
		return CollectionSnapshot{}, contracts.NewFault(contracts.InvalidState)
	}
	result := CollectionSnapshot{Article: contracts.ArticleData{URL: snapshot.Article.Ref.CanonicalURL, Title: snapshot.Article.Title, GalleryID: snapshot.Article.Ref.GalleryID, GalleryName: snapshot.Article.GalleryName, GalleryKind: string(snapshot.Article.Ref.Kind), Number: snapshot.Article.Ref.ArticleNo, PostedAt: snapshot.Article.PostedAt}, CollectedAt: snapshot.CollectedAt, Pages: snapshot.Pages, Deleted: snapshot.DeletedComments, Unsupported: snapshot.UnsupportedComments, Comments: make([]selection.InputComment, 0, len(snapshot.Comments))}
	if author := snapshot.Article.Author; author != nil {
		result.Article.AuthorNickname = author.Nickname
		if author.Identifier != "" {
			id := author.Identifier
			result.Article.AuthorIdentifier = &id
			result.Author = &selection.ParticipantKey{Nickname: author.Nickname, Identifier: id, Anonymous: author.ParticipantKind == "anonymous"}
		}
	}
	for _, comment := range snapshot.Comments {
		result.Comments = append(result.Comments, selection.InputComment{ID: comment.ID, ParentID: comment.ParentID, Nickname: comment.Nickname, Identifier: comment.Identifier, ParticipantKind: selection.ParticipantKind(comment.ParticipantKind), BadgeCategory: contracts.BadgeCategory(comment.BadgeCategory), Kind: selection.CommentKind(comment.Kind), Text: comment.Text, PostedAt: comment.PostedAt, MediaURLs: comment.MediaURLs})
	}
	return result, nil
}
