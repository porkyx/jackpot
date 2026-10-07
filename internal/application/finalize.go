package application

import (
	"context"

	"github.com/porkyx/jackpot/internal/contracts"
	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
	"github.com/porkyx/jackpot/internal/selection"
)

func (service *DraftService) freeze(request contracts.CreateCollectionRequest) (rl.FrozenCollection, rl.RoundInput, error) {
	if err := request.Validate(); err != nil {
		return rl.FrozenCollection{}, rl.RoundInput{}, err
	}
	if err := service.guard(); err != nil {
		return rl.FrozenCollection{}, rl.RoundInput{}, err
	}
	expected := contracts.DraftContext{BackendSessionID: request.BackendSessionID, DraftID: request.DraftID, Revision: *request.ExpectedRevision, ArticleGeneration: *request.ArticleGeneration}
	actual := service.draft.Summary.DraftContext
	// An admitted draft retains its immutable public inputs. This permits an
	// identical lost-response replay without reopening it or generating entropy.
	if service.draft.Summary.State == contracts.DraftFinalized && actual.Revision > 0 {
		actual.Revision--
	}
	if err := contracts.CheckDraftContext(actual, expected); err != nil {
		return rl.FrozenCollection{}, rl.RoundInput{}, err
	}
	if err := contracts.ValidateCounter(contracts.Revision(request.AfterSequence)); err != nil {
		return rl.FrozenCollection{}, rl.RoundInput{}, err
	}
	if service.draft.Summary.State != contracts.DraftReady && service.draft.Summary.State != contracts.DraftFinalized {
		return rl.FrozenCollection{}, rl.RoundInput{}, contracts.NewFault(contracts.InvalidState)
	}
	if service.draft.Article == nil || service.draft.Summary.Snapshot == nil || !service.draft.Summary.Snapshot.Complete || request.Mode != service.draft.Prizes.DrawMode {
		return rl.FrozenCollection{}, rl.RoundInput{}, contracts.NewFault(contracts.InvalidInput)
	}
	items := selection.Items{Mode: selection.ItemMode(service.draft.Prizes.Mode), SingleCount: int(service.draft.Prizes.Single.Count), SingleID: service.draft.Prizes.Single.ID, SingleName: service.draft.Prizes.Single.Name, Multiple: make([]selection.Item, 0, len(service.draft.Prizes.Multiple))}
	for _, item := range service.draft.Prizes.Multiple {
		items.Multiple = append(items.Multiple, selection.Item{ID: item.ID, Name: item.Name, Count: int(item.Count)})
	}
	frozen, err := selection.Freeze(service.participants, asFilters(service.draft.Filters), service.author, items, "", true)
	if err != nil {
		return rl.FrozenCollection{}, rl.RoundInput{}, err
	}
	article := service.draft.Article
	collection := rl.FrozenCollection{SourceDraftID: expected.DraftID, FinalizedDraftRevision: expected.Revision, ArticleGeneration: expected.ArticleGeneration, Snapshot: *service.draft.Summary.Snapshot,
		Article: rl.ArticleSnapshot{URL: article.URL, Title: article.Title, GalleryID: article.GalleryID, GalleryName: article.GalleryName, GalleryKind: article.GalleryKind, Number: article.Number, AuthorNickname: article.AuthorNickname, AuthorIdentifier: article.AuthorIdentifier, PostedAt: article.PostedAt},
		Filters: rl.FilterSnapshot{ExcludeAnonymous: frozen.Filters.ExcludeAnonymous, ExcludeAuthor: frozen.Filters.ExcludeAuthor, ExcludeDcconOnly: frozen.Filters.ExcludeDcconOnly, TimeCut: frozen.Filters.TimeCut, IncludeKeywords: frozen.Filters.IncludeKeywords, ExcludeKeywords: frozen.Filters.ExcludeKeywords, RulesVersion: frozen.RulesVersion}, Participants: make([]rl.ParticipantSnapshot, 0, len(frozen.Selection.Rows))}
	collection.Filters.BadgeRules = contracts.CloneBadgeRules(frozen.Filters.BadgeRules)
	input := rl.RoundInput{BadgeRules: contracts.CloneBadgeRules(frozen.Filters.BadgeRules), CandidateIDs: []contracts.ParticipantID{}, Prizes: make([]rl.Prize, 0, len(frozen.Items)), Mode: request.Mode}
	for _, item := range frozen.Items {
		input.Prizes = append(input.Prizes, rl.Prize{ID: rl.PrizeID(item.ID), Name: item.Name, Count: uint32(item.Count)})
	}
	for _, row := range frozen.Selection.Rows {
		participant := row.Participant
		entry := rl.ParticipantSnapshot{ID: participant.ID, Nickname: participant.Key.Nickname, PublicIdentifier: participant.Key.Identifier, Kind: string(participant.Kind), Included: row.Classification.Included, Manual: &rl.ManualStateSnapshot{ManualIncluded: participant.Manual.ManualIncluded, OverrideExcluded: participant.Manual.OverrideExcluded}, Classification: string(row.Classification.Group), Reason: string(row.Classification.Reason), Comments: make([]rl.CommentSnapshot, 0, len(participant.Comments))}
		for _, comment := range participant.Comments {
			entry.Comments = append(entry.Comments, rl.CommentSnapshot{ID: comment.ID, ParentID: comment.ParentID, Kind: string(comment.Kind), Text: comment.Text, PostedAt: comment.PostedAt, MediaURLs: comment.MediaURLs})
		}
		entry.BadgeCategory = participant.BadgeCategory
		collection.Participants = append(collection.Participants, entry)
		drawable, err := rl.ParticipantDrawable(entry, input.BadgeRules)
		if err != nil {
			return rl.FrozenCollection{}, rl.RoundInput{}, err
		}
		if drawable {
			input.CandidateIDs = append(input.CandidateIDs, entry.ID)
			if input.BadgeRules != nil {
				category, err := contracts.ResolveBadgeCategory(entry.BadgeCategory, entry.Kind)
				if err != nil {
					return rl.FrozenCollection{}, rl.RoundInput{}, err
				}
				input.CandidateCategories = append(input.CandidateCategories, category)
			}
		}
	}
	return collection, input, nil
}
func (service *DraftService) CreateCollection(ctx context.Context, request contracts.CreateCollectionRequest, lifecycle *rl.Service) (rl.RoundRecord, error) {
	if err := requestContext(ctx); err != nil {
		return rl.RoundRecord{}, err
	}
	if lifecycle == nil {
		return rl.RoundRecord{}, contracts.NewFault(contracts.InvalidInput)
	}
	service.mu.Lock()
	frozen, input, err := service.freeze(request)
	service.mu.Unlock()
	if err != nil {
		return rl.RoundRecord{}, err
	}
	captured := contracts.DraftContext{BackendSessionID: request.BackendSessionID, DraftID: request.DraftID, Revision: *request.ExpectedRevision, ArticleGeneration: *request.ArticleGeneration}
	prepared, err := lifecycle.PrepareCreate(ctx, rl.CreateCollectionRequest{OperationID: request.OperationID, Context: captured, Collection: frozen, Input: input})
	if err != nil {
		return rl.RoundRecord{}, err
	}
	defer prepared.Close()
	if admission, replay := prepared.Replay(); replay {
		return lifecycle.ExecuteAdmission(ctx, admission)
	}
	service.mu.Lock()
	if err = contracts.CheckDraftContext(service.draft.Summary.DraftContext, captured); err != nil {
		service.mu.Unlock()
		return rl.RoundRecord{}, err
	}
	if service.closed || service.draft.Summary.State != contracts.DraftReady {
		service.mu.Unlock()
		return rl.RoundRecord{}, contracts.NewFault(contracts.InvalidState)
	}
	if _, err = contracts.Increment(service.draft.Summary.Revision); err != nil {
		service.mu.Unlock()
		return rl.RoundRecord{}, err
	}
	admission, err := lifecycle.AdmitCreate(ctx, prepared, frozen)
	if err != nil {
		service.mu.Unlock()
		return rl.RoundRecord{}, err
	}
	service.draft.Summary.State = contracts.DraftFinalized
	id := admission.Round.CollectionID
	service.draft.Summary.CollectionID = &id
	service.draft.Summary.Revision++
	service.mu.Unlock()
	service.publish(request.OperationID)
	// Once admission commits the draft stays sealed, including execution failure.
	return lifecycle.ExecuteAdmission(ctx, admission)
}
