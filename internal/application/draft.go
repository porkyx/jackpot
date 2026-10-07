// Package application owns the Go-session draft and temporary collection work.
// It is the only bridge from collected comments to the durable RoundLifecycle.
package application

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/porkyx/jackpot/internal/contracts"
	"github.com/porkyx/jackpot/internal/selection"
)

type CollectionSnapshot struct {
	Article                     contracts.ArticleData
	Author                      *selection.ParticipantKey
	Comments                    []selection.InputComment
	Pages, Deleted, Unsupported uint32
	CollectedAt                 time.Time
}
type CollectionProgress struct{ Pages, Comments uint32 }
type Collector interface {
	Collect(context.Context, string, func(CollectionProgress)) (CollectionSnapshot, error)
}
type DraftOptions struct {
	Session   contracts.BackendSessionID
	Now       func() time.Time
	NewID     func() string
	Collector Collector
	Publish   func(contracts.StateNotice) error
}
type draftOperation struct {
	fingerprint [32]byte
	data        contracts.DraftData
}
type DraftService struct {
	mu           sync.Mutex
	options      DraftOptions
	draft        contracts.DraftData
	participants []selection.Participant
	author       *selection.ParticipantKey
	operations   map[contracts.OperationID]draftOperation
	order        []contracts.OperationID
	cancel       context.CancelFunc
	workers      sync.WaitGroup
	closed       bool
}

func collectorNil(collector Collector) bool {
	if collector == nil {
		return true
	}
	value := reflect.ValueOf(collector)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
func NewDraftService(options DraftOptions) (*DraftService, error) {
	if options.Session == "" || options.Now == nil || options.NewID == nil || collectorNil(options.Collector) {
		return nil, contracts.NewFault(contracts.InvalidInput)
	}
	if _, err := contracts.NewResponseHeader(options.Session, options.Now()); err != nil {
		return nil, err
	}
	return &DraftService{options: options, operations: make(map[contracts.OperationID]draftOperation)}, nil
}
func (service *DraftService) Close() error {
	service.mu.Lock()
	service.closed = true
	cancel := service.cancel
	service.cancel = nil
	service.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	service.workers.Wait()
	return nil
}
func cloneDraft(data contracts.DraftData) contracts.DraftData {
	data.Filters.BadgeRules = contracts.CloneBadgeRules(data.Filters.BadgeRules)
	data.Filters.IncludeKeywords = append([]string{}, data.Filters.IncludeKeywords...)
	data.Filters.ExcludeKeywords = append([]string{}, data.Filters.ExcludeKeywords...)
	data.Prizes.Multiple = append([]contracts.PrizeInput{}, data.Prizes.Multiple...)
	if data.Filters.TimeCut != nil {
		value := *data.Filters.TimeCut
		data.Filters.TimeCut = &value
	}
	if data.Article != nil {
		article := *data.Article
		if article.PostedAt != nil {
			value := *article.PostedAt
			article.PostedAt = &value
		}
		if article.AuthorIdentifier != nil {
			value := *article.AuthorIdentifier
			article.AuthorIdentifier = &value
		}
		data.Article = &article
	}
	if data.Summary.Snapshot != nil {
		value := *data.Summary.Snapshot
		data.Summary.Snapshot = &value
	}
	if data.Summary.CollectionID != nil {
		value := *data.Summary.CollectionID
		data.Summary.CollectionID = &value
	}
	if data.Load != nil {
		value := *data.Load
		if value.FailureCode != nil {
			code := *value.FailureCode
			value.FailureCode = &code
		}
		data.Load = &value
	}
	return data
}
func (service *DraftService) Summary() *contracts.DraftSummary {
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.draft.Summary.DraftID == "" {
		return nil
	}
	data := cloneDraft(service.draft)
	return &data.Summary
}
func requestContext(ctx context.Context) error {
	if ctx == nil {
		return contracts.NewFault(contracts.InvalidInput)
	}
	return ctx.Err()
}
func hashPublic(value any) ([32]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(raw), nil
}
func (service *DraftService) guard() error {
	if service.closed {
		return contracts.NewFault(contracts.InvalidState)
	}
	return nil
}
func (service *DraftService) replay(id contracts.OperationID, hash [32]byte) (contracts.DraftData, bool, error) {
	entry, found := service.operations[id]
	if !found {
		return contracts.DraftData{}, false, nil
	}
	if entry.fingerprint != hash {
		return contracts.DraftData{}, false, contracts.NewFault(contracts.InvalidInput)
	}
	if entry.data.Load != nil && entry.data.Load.OperationID == id && service.draft.Load != nil && service.draft.Load.OperationID == id {
		return cloneDraft(service.draft), true, nil
	}
	return cloneDraft(entry.data), true, nil
}
func (service *DraftService) record(id contracts.OperationID, hash [32]byte) {
	service.operations[id] = draftOperation{hash, cloneDraft(service.draft)}
	service.order = append(service.order, id)
	for len(service.order) > 128 {
		old := service.order[0]
		service.order = service.order[1:]
		if service.draft.Load != nil && old == service.draft.Load.OperationID && (service.draft.Load.State == "loading" || service.draft.Load.State == "cancelling") {
			service.order = append(service.order, old)
			continue
		}
		delete(service.operations, old)
	}
}
func (service *DraftService) publish(id contracts.OperationID) {
	if service.options.Publish == nil {
		return
	}
	summary := service.Summary()
	if summary == nil {
		return
	}
	_ = service.options.Publish(contracts.StateNotice{BackendSessionID: service.options.Session, EntityKind: "draft", EntityID: string(summary.DraftID), Revision: summary.Revision, OperationID: &id})
}
func (service *DraftService) Create(ctx context.Context, request contracts.CreateDraftRequest) (contracts.DraftData, error) {
	if err := requestContext(ctx); err != nil {
		return contracts.DraftData{}, err
	}
	if err := request.Validate(); err != nil {
		return contracts.DraftData{}, err
	}
	hash, err := hashPublic(request)
	if err != nil {
		return contracts.DraftData{}, err
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if err = service.guard(); err != nil {
		return contracts.DraftData{}, err
	}
	if result, replay, err := service.replay(request.OperationID, hash); err != nil || replay {
		return result, err
	}
	if request.BackendSessionID != service.options.Session {
		return contracts.DraftData{}, contracts.NewFault(contracts.BackendSessionChanged)
	}
	if *request.ExpectedRevision != 0 {
		return contracts.DraftData{}, contracts.NewFault(contracts.StaleRevision)
	}
	if service.draft.Summary.DraftID != "" && service.draft.Summary.State != contracts.DraftFinalized {
		service.record(request.OperationID, hash)
		return cloneDraft(service.draft), nil
	}
	id := service.options.NewID()
	if id == "" {
		return contracts.DraftData{}, contracts.NewFault(contracts.InvalidState)
	}
	service.draft = contracts.DraftData{Summary: contracts.DraftSummary{DraftContext: contracts.DraftContext{BackendSessionID: service.options.Session, DraftID: contracts.DraftID(id)}, State: contracts.DraftEmpty},
		Filters: contracts.FilterConfiguration{ExcludeAuthor: true, IncludeKeywords: []string{}, ExcludeKeywords: []string{}},
		Prizes:  contracts.PrizeConfiguration{Mode: "single", Single: contracts.PrizeInput{ID: "single", Count: 1}, Multiple: []contracts.PrizeInput{{ID: "item-1", Count: 1}}, DrawMode: "immediate"}}
	service.participants = nil
	service.author = nil
	service.record(request.OperationID, hash)
	return cloneDraft(service.draft), nil
}
func (service *DraftService) Get(ctx context.Context, query contracts.DraftQuery) (contracts.DraftData, error) {
	if err := requestContext(ctx); err != nil {
		return contracts.DraftData{}, err
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if err := service.guard(); err != nil {
		return contracts.DraftData{}, err
	}
	if query.BackendSessionID != service.options.Session {
		return contracts.DraftData{}, contracts.NewFault(contracts.BackendSessionChanged)
	}
	if query.DraftID == "" || query.DraftID != service.draft.Summary.DraftID {
		return contracts.DraftData{}, contracts.NewFault(contracts.StaleArticleContext)
	}
	return cloneDraft(service.draft), nil
}
func (service *DraftService) check(header contracts.DraftMutationHeader) error {
	if err := service.guard(); err != nil {
		return err
	}
	if err := header.Validate(); err != nil {
		return err
	}
	expected := contracts.DraftContext{BackendSessionID: header.BackendSessionID, DraftID: header.DraftID, Revision: *header.ExpectedRevision, ArticleGeneration: *header.ArticleGeneration}
	if err := contracts.CheckDraftContext(service.draft.Summary.DraftContext, expected); err != nil {
		return err
	}
	if service.draft.Summary.State == contracts.DraftFinalized {
		return contracts.NewFault(contracts.InvalidState)
	}
	return nil
}
func asFilters(filters contracts.FilterConfiguration) selection.Filters {
	return selection.Filters{BadgeRules: contracts.CloneBadgeRules(filters.BadgeRules), ExcludeAnonymous: filters.ExcludeAnonymous, ExcludeAuthor: filters.ExcludeAuthor, ExcludeDcconOnly: filters.ExcludeDcconOnly, TimeCut: filters.TimeCut, IncludeKeywords: filters.IncludeKeywords, ExcludeKeywords: filters.ExcludeKeywords}
}
func (service *DraftService) selection() (selection.Selection, error) {
	return selection.Evaluate(service.participants, asFilters(service.draft.Filters), service.author)
}
func (service *DraftService) counts() error {
	selected, err := service.selection()
	if err != nil {
		return err
	}
	service.draft.Participants = uint32(len(selected.Rows))
	service.draft.Included = uint32(selected.Included)
	service.draft.Excluded = uint32(selected.Excluded)
	service.draft.AuthorIdentifiable = selected.AuthorIdentified
	return nil
}
func (service *DraftService) Load(ctx context.Context, request contracts.LoadArticleRequest) (contracts.DraftData, error) {
	if err := requestContext(ctx); err != nil {
		return contracts.DraftData{}, err
	}
	if err := request.Validate(); err != nil {
		return contracts.DraftData{}, err
	}
	if strings.TrimSpace(request.URL) == "" {
		return contracts.DraftData{}, contracts.NewFault(contracts.InvalidInput)
	}
	hash, err := hashPublic(request)
	if err != nil {
		return contracts.DraftData{}, err
	}
	service.mu.Lock()
	if result, replay, err := service.replay(request.OperationID, hash); err != nil || replay {
		service.mu.Unlock()
		return result, err
	}
	if err = service.check(request.DraftMutationHeader); err != nil {
		service.mu.Unlock()
		return contracts.DraftData{}, err
	}
	if service.cancel != nil {
		service.mu.Unlock()
		return contracts.DraftData{}, contracts.NewFault(contracts.InvalidState)
	}
	revision, err := contracts.Increment(service.draft.Summary.Revision)
	if err != nil {
		service.mu.Unlock()
		return contracts.DraftData{}, err
	}
	work, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	service.cancel = cancel
	service.draft.Summary.Revision = revision
	service.draft.Summary.State = contracts.DraftLoading
	service.draft.Load = &contracts.LoadProgress{OperationID: request.OperationID, State: "loading"}
	service.record(request.OperationID, hash)
	result := cloneDraft(service.draft)
	service.workers.Add(1)
	service.mu.Unlock()
	go service.collect(work, cancel, request)
	service.publish(request.OperationID)
	return result, nil
}
func (service *DraftService) collect(ctx context.Context, cancel context.CancelFunc, request contracts.LoadArticleRequest) {
	defer service.workers.Done()
	defer cancel()
	snapshot, err := service.options.Collector.Collect(ctx, request.URL, func(progress CollectionProgress) {
		service.mu.Lock()
		if !service.closed && service.draft.Load != nil && service.draft.Load.OperationID == request.OperationID && service.draft.Load.State == "loading" {
			service.draft.Load.Sequence++
			service.draft.Load.Pages = progress.Pages
			service.draft.Load.Comments = progress.Comments
		}
		service.mu.Unlock()
	})
	var participants []selection.Participant
	if err == nil {
		participants, err = selection.BuildParticipants(snapshot.Comments)
	}
	service.mu.Lock()
	if service.closed || service.draft.Load == nil || service.draft.Load.OperationID != request.OperationID {
		service.mu.Unlock()
		return
	}
	if ctx.Err() != nil {
		err = ctx.Err()
	} else if service.draft.Load.State == "cancelling" {
		// Cancellation is admitted under mu before its context signal is sent.
		err = context.Canceled
	}
	_, snapshotOffset := snapshot.CollectedAt.Zone()
	if err == nil && (snapshotOffset != 0 || snapshot.Pages > 20 || snapshot.CollectedAt.IsZero() || snapshot.CollectedAt.Year() < 1 || snapshot.CollectedAt.Year() > 9999) {
		err = contracts.NewFault(contracts.InvalidState)
	}
	next, incrementErr := contracts.Increment(service.draft.Summary.Revision)
	if incrementErr != nil {
		err = incrementErr
	}
	if incrementErr == nil {
		service.draft.Summary.Revision = next
	}
	service.cancel = nil
	service.draft.Load.Sequence++
	if err == nil {
		generation, generationErr := contracts.Increment(service.draft.Summary.ArticleGeneration)
		if generationErr != nil {
			err = generationErr
		} else {
			oldDraft, oldParticipants, oldAuthor := cloneDraft(service.draft), service.participants, service.author
			service.participants = participants
			service.author = snapshot.Author
			service.draft.Article = &snapshot.Article
			service.draft.Summary.Snapshot = &contracts.SnapshotSummary{SnapshotID: contracts.SnapshotID(string(request.OperationID)), CollectedAt: snapshot.CollectedAt, Complete: true, Pages: snapshot.Pages, AcceptedComments: uint32(len(snapshot.Comments)), DeletedComments: snapshot.Deleted, UnsupportedComments: snapshot.Unsupported}
			service.draft.Summary.ArticleGeneration = generation
			err = service.counts()
			if err != nil {
				service.draft = oldDraft
				service.participants = oldParticipants
				service.author = oldAuthor
			}
		}
	}
	if err == nil {
		service.draft.Summary.State = contracts.DraftReady
		service.draft.Load.State = "completed"
	} else {
		service.draft.Summary.State = contracts.DraftEmpty
		if service.draft.Summary.Snapshot != nil {
			service.draft.Summary.State = contracts.DraftReady
		}
		service.draft.Load.State = "failed"
		if errors.Is(err, context.Canceled) {
			service.draft.Load.State = "cancelled"
		} else {
			code := contracts.InvalidInput
			var fault contracts.Fault
			if errors.As(err, &fault) && fault.Code.Validate() == nil {
				code = fault.Code
			}
			service.draft.Load.FailureCode = &code
		}
	}
	entry := service.operations[request.OperationID]
	entry.data = cloneDraft(service.draft)
	service.operations[request.OperationID] = entry
	service.mu.Unlock()
	service.publish(request.OperationID)
}
func validatePrizes(configuration contracts.PrizeConfiguration) error {
	if configuration.Mode != "single" && configuration.Mode != "multiple" {
		return contracts.NewFault(contracts.InvalidInput)
	}
	if configuration.DrawMode != "immediate" && configuration.DrawMode != "reservation" {
		return contracts.NewFault(contracts.InvalidInput)
	}
	if len(configuration.Multiple) < 1 || len(configuration.Multiple) > 10 {
		return contracts.NewFault(contracts.InvalidInput)
	}
	seen := map[string]bool{}
	for _, item := range append([]contracts.PrizeInput{configuration.Single}, configuration.Multiple...) {
		if item.ID == "" || seen[item.ID] || item.Count < 1 || item.Count > 10 || len(utf16.Encode([]rune(item.Name))) > 20 {
			return contracts.NewFault(contracts.InvalidInput)
		}
		seen[item.ID] = true
	}
	return nil
}
func (service *DraftService) Edit(ctx context.Context, request contracts.DraftEditRequest) (contracts.DraftData, error) {
	if err := requestContext(ctx); err != nil {
		return contracts.DraftData{}, err
	}
	if err := request.Validate(); err != nil {
		return contracts.DraftData{}, err
	}
	hash, err := hashPublic(request)
	if err != nil {
		return contracts.DraftData{}, err
	}
	service.mu.Lock()
	if result, replay, err := service.replay(request.OperationID, hash); err != nil || replay {
		service.mu.Unlock()
		return result, err
	}
	if err = service.check(request.DraftMutationHeader); err != nil {
		service.mu.Unlock()
		return contracts.DraftData{}, err
	}
	if request.Kind == "CancelLoad" {
		if service.cancel == nil {
			service.record(request.OperationID, hash)
			result := cloneDraft(service.draft)
			service.mu.Unlock()
			return result, nil
		}
		cancel := service.cancel
		service.draft.Load.State = "cancelling"
		service.record(request.OperationID, hash)
		result := cloneDraft(service.draft)
		service.mu.Unlock()
		cancel()
		return result, nil
	}
	if service.cancel != nil {
		service.mu.Unlock()
		return contracts.DraftData{}, contracts.NewFault(contracts.InvalidState)
	}
	previous, participants, author := cloneDraft(service.draft), service.participants, service.author
	switch request.Kind {
	case "UpdateFilters":
		if request.Filters == nil {
			err = contracts.NewFault(contracts.InvalidInput)
		} else {
			service.draft.Filters = *request.Filters
		}
	case "SetPrizes":
		if request.Prizes == nil {
			err = contracts.NewFault(contracts.InvalidInput)
		} else if err = validatePrizes(*request.Prizes); err == nil {
			service.draft.Prizes = *request.Prizes
		}
	case "ToggleParticipant":
		service.participants, err = selection.ToggleManual(service.participants, request.ParticipantID, asFilters(service.draft.Filters), service.author)
	case "SetAllUnclassified":
		if request.Included == nil {
			err = contracts.NewFault(contracts.InvalidInput)
		} else {
			service.participants, err = selection.SetUnclassified(service.participants, asFilters(service.draft.Filters), service.author, *request.Included)
		}
	case "ResetManual":
		service.participants = selection.ResetManual(service.participants)
	case "ResetFilters":
		service.draft.Filters = contracts.FilterConfiguration{IncludeKeywords: []string{}, ExcludeKeywords: []string{}}
	case "ResetArticle":
		service.draft.Summary.ArticleGeneration, err = contracts.Increment(service.draft.Summary.ArticleGeneration)
		if err == nil {
			service.draft.Summary.Snapshot = nil
			service.draft.Summary.State = contracts.DraftEmpty
			service.draft.Article = nil
			service.draft.Load = nil
			service.participants = nil
			service.author = nil
			service.draft.Filters = contracts.FilterConfiguration{IncludeKeywords: []string{}, ExcludeKeywords: []string{}}
		}
	default:
		err = contracts.NewFault(contracts.InvalidInput)
	}
	if err == nil {
		err = service.counts()
	}
	if err == nil {
		service.draft.Summary.Revision, err = contracts.Increment(service.draft.Summary.Revision)
	}
	if err != nil {
		service.draft = previous
		service.participants = participants
		service.author = author
		service.mu.Unlock()
		return contracts.DraftData{}, err
	}
	service.draft = cloneDraft(service.draft)
	service.record(request.OperationID, hash)
	result := cloneDraft(service.draft)
	service.mu.Unlock()
	service.publish(request.OperationID)
	return result, nil
}
func (service *DraftService) Participants(ctx context.Context, query contracts.ParticipantQuery) (contracts.ParticipantsPage, error) {
	if err := requestContext(ctx); err != nil {
		return contracts.ParticipantsPage{}, err
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if err := service.guard(); err != nil {
		return contracts.ParticipantsPage{}, err
	}
	if err := contracts.CheckDraftContext(service.draft.Summary.DraftContext, query.DraftContext); err != nil {
		return contracts.ParticipantsPage{}, err
	}
	if query.Limit < 1 || query.Limit > 100 || len(query.Query) > 1000 {
		return contracts.ParticipantsPage{}, contracts.NewFault(contracts.InvalidInput)
	}
	if query.Group != "" && query.Group != "unclassified" && query.Group != "included" && query.Group != "excluded" {
		return contracts.ParticipantsPage{}, contracts.NewFault(contracts.InvalidInput)
	}
	selected, err := service.selection()
	if err != nil {
		return contracts.ParticipantsPage{}, err
	}
	page := contracts.ParticipantsPage{Context: service.draft.Summary.DraftContext, Offset: query.Offset, Rows: []contracts.ParticipantData{}}
	search := strings.ToLower(query.Query)
	for _, row := range selected.Rows {
		if query.Group != "" && string(row.Classification.Group) != query.Group {
			continue
		}
		page.Total++
		if !strings.Contains(strings.ToLower(row.Participant.Key.Nickname), search) && !strings.Contains(strings.ToLower(row.Participant.Key.Identifier), search) {
			continue
		}
		index := page.Matched
		page.Matched++
		if index < query.Offset || uint32(len(page.Rows)) >= query.Limit {
			continue
		}
		data, err := participantData(row)
		if err != nil {
			return contracts.ParticipantsPage{}, err
		}
		page.Rows = append(page.Rows, data)
	}
	return page, nil
}
func participantData(row selection.Row) (contracts.ParticipantData, error) {
	texts := make([]string, 0, min(len(row.Participant.Comments), contracts.MaxParticipantPreviews))
	for _, comment := range row.Participant.Comments[:min(len(row.Participant.Comments), contracts.MaxParticipantPreviews)] {
		text := comment.Text
		if text == "" && comment.Kind == selection.Dccon {
			text = "[디시콘]"
		}
		if text == "" && comment.Kind == selection.Voice {
			text = "[보플]"
		}
		texts = append(texts, text)
	}
	previews, err := contracts.PreviewTexts(texts)
	if err != nil {
		return contracts.ParticipantData{}, err
	}
	category, err := contracts.ResolveBadgeCategory(row.Participant.BadgeCategory, string(row.Participant.Kind))
	if err != nil {
		return contracts.ParticipantData{}, err
	}
	return contracts.ParticipantData{BadgeCategory: category, ID: row.Participant.ID, Nickname: row.Participant.Key.Nickname, PublicIdentifier: row.Participant.Key.Identifier, Kind: string(row.Participant.Kind), Classification: string(row.Classification.Group), Reason: string(row.Classification.Reason), Included: row.Classification.Included, CommentCount: uint32(len(row.Participant.Comments)), Previews: previews}, nil
}
func (service *DraftService) Comments(ctx context.Context, query contracts.ParticipantCommentsQuery) (contracts.CommentsPage, error) {
	if err := requestContext(ctx); err != nil {
		return contracts.CommentsPage{}, err
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if err := service.guard(); err != nil {
		return contracts.CommentsPage{}, err
	}
	if err := contracts.CheckDraftContext(service.draft.Summary.DraftContext, query.DraftContext); err != nil {
		return contracts.CommentsPage{}, err
	}
	if query.Limit < 1 || query.Limit > 50 || query.ParticipantID == "" {
		return contracts.CommentsPage{}, contracts.NewFault(contracts.InvalidInput)
	}
	for _, participant := range service.participants {
		if participant.ID != query.ParticipantID {
			continue
		}
		page := contracts.CommentsPage{Context: service.draft.Summary.DraftContext, ParticipantID: participant.ID, Total: uint32(len(participant.Comments)), Offset: query.Offset, Rows: []contracts.CommentData{}}
		for index, comment := range participant.Comments {
			if uint32(index) < query.Offset || uint32(len(page.Rows)) >= query.Limit {
				continue
			}
			data := contracts.CommentData{ID: comment.ID, ParentID: comment.ParentID, Kind: string(comment.Kind), Text: comment.Text, PostedAt: comment.PostedAt, MediaURLs: append([]string{}, comment.MediaURLs...)}
			if data.ParentID != nil {
				value := *data.ParentID
				data.ParentID = &value
			}
			if data.PostedAt != nil {
				value := *data.PostedAt
				data.PostedAt = &value
			}
			page.Rows = append(page.Rows, data)
		}
		return page, nil
	}
	return contracts.CommentsPage{}, contracts.NewFault(contracts.InvalidInput)
}

// Operation verifies a previously admitted ephemeral command in this Go session.
// It exposes no raw input, fingerprint or secret and never resends a mutation.
func (service *DraftService) Operation(ctx context.Context, id contracts.OperationID) (contracts.DraftOperationData, error) {
	if err := requestContext(ctx); err != nil {
		return contracts.DraftOperationData{}, err
	}
	if id == "" {
		return contracts.DraftOperationData{}, contracts.NewFault(contracts.InvalidInput)
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if err := service.guard(); err != nil {
		return contracts.DraftOperationData{}, err
	}
	entry, found := service.operations[id]
	result := contracts.DraftOperationData{OperationID: id, State: contracts.OperationUnknown}
	if !found {
		return result, nil
	}
	data := cloneDraft(entry.data)
	summary := data.Summary
	result.Summary = &summary
	result.State = contracts.OperationSucceeded
	if data.Load != nil && data.Load.OperationID == id {
		if data.Load.State == "loading" || data.Load.State == "cancelling" {
			result.State = contracts.OperationPending
		}
		if data.Load.State == "failed" {
			result.State = contracts.OperationFailed
			result.FailureCode = data.Load.FailureCode
		}
	}
	return result, nil
}
