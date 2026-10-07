package desktop

import (
	"context"
	"errors"
	"time"

	draftapp "github.com/porkyx/jackpot/internal/application"
	"github.com/porkyx/jackpot/internal/contracts"
	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
)

type ProductReader interface {
	rl.ReadStorage
	rl.LifecycleReader
	ReadCollectionState(context.Context, contracts.CollectionID) (rl.FrozenCollection, []rl.RoundRecord, contracts.Revision, error)
	ReadCollectionSnapshot(context.Context, contracts.CollectionID) (rl.FrozenCollection, contracts.Revision, error)
	ReadCollectionPage(context.Context, contracts.CollectionID, rl.CollectionPageQuery) (rl.CollectionPage, error)
}
type RoundService struct {
	draft     *draftapp.DraftService
	lifecycle *rl.Service
	reader    ProductReader
	session   contracts.BackendSessionID
	now       func() time.Time
}

func NewRoundService(draft *draftapp.DraftService, lifecycle *rl.Service, reader ProductReader, session contracts.BackendSessionID, now func() time.Time) (*RoundService, error) {
	if draft == nil || lifecycle == nil || nilDependency(reader) || session == "" || now == nil {
		return nil, contracts.NewFault(contracts.InvalidInput)
	}
	return &RoundService{draft: draft, lifecycle: lifecycle, reader: reader, session: session, now: now}, nil
}
func (service *RoundService) header(ctx context.Context) (contracts.ResponseHeader, error) {
	if ctx == nil {
		return contracts.ResponseHeader{}, contracts.NewFault(contracts.InvalidInput)
	}
	if err := ctx.Err(); err != nil {
		return contracts.ResponseHeader{}, err
	}
	return contracts.NewResponseHeader(service.session, service.now())
}
func safeProductError(err error) error {
	if errors.Is(err, rl.ErrOperationConflict) || errors.Is(err, rl.ErrNotFound) {
		return contracts.NewFault(contracts.InvalidInput)
	}
	if errors.Is(err, rl.ErrAlreadyFinalized) {
		return contracts.NewFault(contracts.InvalidState)
	}
	return err
}
func articleProjection(article rl.ArticleSnapshot) contracts.ArticleData {
	return contracts.ArticleData{URL: article.URL, Title: article.Title, GalleryID: article.GalleryID, GalleryName: article.GalleryName, GalleryKind: article.GalleryKind, Number: article.Number, AuthorNickname: article.AuthorNickname, AuthorIdentifier: article.AuthorIdentifier, PostedAt: article.PostedAt}
}
func filterProjection(filters rl.FilterSnapshot) contracts.FilterConfiguration {
	return contracts.FilterConfiguration{BadgeRules: contracts.CloneBadgeRules(filters.BadgeRules), ExcludeAnonymous: filters.ExcludeAnonymous, ExcludeAuthor: filters.ExcludeAuthor, ExcludeDcconOnly: filters.ExcludeDcconOnly, TimeCut: filters.TimeCut, IncludeKeywords: append([]string{}, filters.IncludeKeywords...), ExcludeKeywords: append([]string{}, filters.ExcludeKeywords...)}
}
func roundProjection(round rl.RoundRecord, participants map[contracts.ParticipantID]rl.ParticipantSnapshot) (contracts.RoundData, error) {
	data := contracts.RoundData{CollectionID: round.CollectionID, RoundID: round.ID, Number: round.Number, Attempt: round.Attempt, State: round.State, Revision: round.Revision, RoundVersion: round.Version, Mode: round.Input.Mode, Message: round.Input.Message, ScheduledAt: round.ScheduledAt, Prizes: []contracts.PrizeInput{}, Winners: []contracts.WinnerData{}}
	names := map[rl.PrizeID]string{}
	for _, prize := range round.Input.Prizes {
		data.Prizes = append(data.Prizes, contracts.PrizeInput{ID: string(prize.ID), Name: prize.Name, Count: prize.Count})
		names[prize.ID] = prize.Name
	}
	if round.FailureCode != "" {
		code := round.FailureCode
		data.FailureCode = &code
	}
	if round.Outcome != nil {
		executed := round.Outcome.ExecutedAt
		data.ExecutedAt = &executed
		data.AlgorithmVersion = round.Outcome.AlgorithmVersion
		data.AppVersion = round.Outcome.AppVersion
		for _, winner := range round.Outcome.Winners {
			participant, found := participants[winner.ParticipantID]
			if !found {
				return contracts.RoundData{}, contracts.NewFault(contracts.InvalidState)
			}
			name, found := names[winner.PrizeID]
			if !found {
				return contracts.RoundData{}, contracts.NewFault(contracts.InvalidState)
			}
			category, err := contracts.ResolveBadgeCategory(participant.BadgeCategory, participant.Kind)
			if err != nil {
				return contracts.RoundData{}, contracts.NewFault(contracts.InvalidState)
			}
			data.Winners = append(data.Winners, contracts.WinnerData{Participant: contracts.ParticipantData{BadgeCategory: category, ID: participant.ID, Nickname: participant.Nickname, PublicIdentifier: participant.PublicIdentifier, Kind: participant.Kind, Classification: participant.Classification, Reason: participant.Reason, Included: participant.Included, CommentCount: uint32(len(participant.Comments)), Previews: []string{}}, PrizeID: string(winner.PrizeID), PrizeName: name, Slot: winner.Slot})
		}
	}
	return data, nil
}
func (service *RoundService) collection(ctx context.Context, id contracts.CollectionID) (contracts.CollectionData, error) {
	return service.collectionPage(ctx, id, rl.CollectionPageQuery{Limit: 50})
}
func (service *RoundService) collectionPage(ctx context.Context, id contracts.CollectionID, query rl.CollectionPageQuery) (contracts.CollectionData, error) {
	if id == "" {
		return contracts.CollectionData{}, contracts.NewFault(contracts.InvalidInput)
	}
	page, err := service.reader.ReadCollectionPage(ctx, id, query)
	if err != nil {
		return contracts.CollectionData{}, safeProductError(err)
	}
	frozen := page.Frozen
	data := contracts.CollectionData{CollectionID: id, Revision: page.Revision, Article: articleProjection(frozen.Article), Snapshot: frozen.Snapshot, Filters: filterProjection(frozen.Filters), ParticipantCount: uint32(len(frozen.Participants)), Rounds: []contracts.RoundData{}, RoundTotal: page.Total, RoundOffset: page.Offset}
	participants := map[contracts.ParticipantID]rl.ParticipantSnapshot{}
	drawableCount := uint32(0)
	for _, participant := range frozen.Participants {
		participants[participant.ID] = participant
		if participant.Included {
			data.SelectedCount++
		}
		drawable, err := rl.ParticipantDrawable(participant, frozen.Filters.BadgeRules)
		if err != nil {
			return contracts.CollectionData{}, contracts.NewFault(contracts.InvalidState)
		}
		if drawable {
			drawableCount++
		}
	}
	if page.ConsumedCount > drawableCount {
		return contracts.CollectionData{}, contracts.NewFault(contracts.InvalidState)
	}
	data.RemainingCount = drawableCount - page.ConsumedCount
	data.LatestRound, err = roundProjection(page.LatestRound, participants)
	if err != nil {
		return contracts.CollectionData{}, err
	}
	for _, round := range page.Rounds {
		projection, err := roundProjection(round, participants)
		if err != nil {
			return contracts.CollectionData{}, err
		}
		data.Rounds = append(data.Rounds, projection)
	}
	return data, nil
}
func collectionResponse(header contracts.ResponseHeader, data contracts.CollectionData, operation *contracts.OperationID, err error) (contracts.CollectionResponse, error) {
	if err != nil {
		return contracts.CollectionResponse(failureEnvelope[contracts.CollectionData](header, safeProductError(err))), nil
	}
	revision := data.Revision
	response := contracts.CollectionResponse{ResponseHeader: header, OK: true, OperationID: operation, Revision: &revision, Data: &data}
	if operation == nil {
		if err := response.Validate(); err != nil {
			return response, err
		}
		checked, checkErr := checkedQuery(contracts.Envelope[contracts.CollectionData](response))
		return contracts.CollectionResponse(checked), checkErr
	}
	return response, response.Validate()
}
func (service *RoundService) GetCollection(ctx context.Context, request contracts.CollectionQuery) (contracts.CollectionResponse, error) {
	header, err := service.header(ctx)
	if err != nil {
		return contracts.CollectionResponse{}, err
	}
	limit := request.RoundLimit
	if limit == 0 {
		limit = 50
	}
	if limit > 50 || (request.RoundID != "" && request.RoundOffset != 0) {
		return collectionResponse(header, contracts.CollectionData{}, nil, contracts.NewFault(contracts.InvalidInput))
	}
	data, err := service.collectionPage(ctx, request.CollectionID, rl.CollectionPageQuery{Offset: request.RoundOffset, Limit: limit, RoundID: request.RoundID})
	return collectionResponse(header, data, nil, err)
}
func (service *RoundService) CreateCollection(ctx context.Context, request contracts.CreateCollectionRequest) (contracts.CollectionResponse, error) {
	header, err := service.header(ctx)
	if err != nil {
		return contracts.CollectionResponse{}, err
	}
	round, err := service.draft.CreateCollection(ctx, request, service.lifecycle)
	if err != nil {
		var finalized *rl.FinalizedError
		if errors.As(err, &finalized) {
			data, queryErr := service.collection(ctx, finalized.CollectionID)
			return collectionResponse(header, data, &request.OperationID, queryErr)
		}
		return collectionResponse(header, contracts.CollectionData{}, &request.OperationID, err)
	}
	data, err := service.collection(ctx, round.CollectionID)
	return collectionResponse(header, data, &request.OperationID, err)
}
func (service *RoundService) bootstrapProjection(ctx context.Context) (*contracts.DraftSummary, []contracts.ResultReference, error) {
	records, err := service.reader.ListCollections(ctx, 0, 16)
	if err != nil {
		return nil, nil, err
	}
	results := make([]contracts.ResultReference, 0, len(records))
	for _, record := range records {
		if record.LatestRound != nil {
			results = append(results, contracts.ResultReference{CollectionID: record.ID, RoundID: record.LatestRound.ID, Revision: record.Revision})
		}
	}
	return service.draft.Summary(), results, nil
}

func commandContext(request contracts.CollectionCommandRequest, session contracts.BackendSessionID, round bool) (contracts.RoundContext, error) {
	if err := request.MutationHeader.Validate(); err != nil {
		return contracts.RoundContext{}, err
	}
	if request.BackendSessionID != session {
		return contracts.RoundContext{}, contracts.NewFault(contracts.BackendSessionChanged)
	}
	context := contracts.RoundContext{CollectionContext: contracts.CollectionContext{BackendSessionID: request.BackendSessionID, CollectionID: request.CollectionID, Revision: *request.ExpectedRevision}, RoundID: request.RoundID}
	if err := context.CollectionContext.Validate(); err != nil {
		return contracts.RoundContext{}, err
	}
	if round {
		if request.ExpectedVersion == nil {
			return contracts.RoundContext{}, contracts.NewFault(contracts.InvalidInput)
		}
		context.Version = *request.ExpectedVersion
		if err := context.Validate(); err != nil {
			return contracts.RoundContext{}, err
		}
	}
	return context, nil
}
func (service *RoundService) Rerun(ctx context.Context, request contracts.CollectionCommandRequest) (contracts.CollectionResponse, error) {
	header, err := service.header(ctx)
	if err != nil {
		return contracts.CollectionResponse{}, err
	}
	context, err := commandContext(request, service.session, false)
	if err != nil {
		return collectionResponse(header, contracts.CollectionData{}, nil, err)
	}
	prizes := make([]rl.Prize, 0, len(request.Prizes))
	for _, item := range request.Prizes {
		prizes = append(prizes, rl.Prize{ID: rl.PrizeID(item.ID), Name: item.Name, Count: item.Count})
	}
	_, err = service.lifecycle.Rerun(ctx, rl.RerunRequest{OperationID: request.OperationID, Context: context.CollectionContext, Prizes: prizes, Message: request.Message, Mode: request.Mode})
	if err != nil {
		return collectionResponse(header, contracts.CollectionData{}, nil, err)
	}
	data, err := service.collection(ctx, request.CollectionID)
	return collectionResponse(header, data, &request.OperationID, err)
}
func (service *RoundService) SetSchedule(ctx context.Context, request contracts.CollectionCommandRequest) (contracts.CollectionResponse, error) {
	header, err := service.header(ctx)
	if err != nil {
		return contracts.CollectionResponse{}, err
	}
	context, err := commandContext(request, service.session, true)
	if err != nil {
		return collectionResponse(header, contracts.CollectionData{}, nil, err)
	}
	if (request.ScheduledAt == nil) == (request.QuickDelaySeconds == nil) {
		return collectionResponse(header, contracts.CollectionData{}, nil, contracts.NewFault(contracts.InvalidInput))
	}
	var scheduledAt time.Time
	if request.ScheduledAt != nil {
		scheduledAt = *request.ScheduledAt
	}
	_, err = service.lifecycle.SetSchedule(ctx, rl.SetScheduleRequest{OperationID: request.OperationID, Context: context, ScheduledAt: scheduledAt, QuickDelaySeconds: request.QuickDelaySeconds, Timezone: "Asia/Seoul"})
	if err != nil {
		return collectionResponse(header, contracts.CollectionData{}, nil, err)
	}
	data, err := service.collection(ctx, request.CollectionID)
	return collectionResponse(header, data, &request.OperationID, err)
}
func (service *RoundService) CancelSchedule(ctx context.Context, request contracts.CollectionCommandRequest) (contracts.CollectionResponse, error) {
	header, err := service.header(ctx)
	if err != nil {
		return contracts.CollectionResponse{}, err
	}
	context, err := commandContext(request, service.session, true)
	if err != nil {
		return collectionResponse(header, contracts.CollectionData{}, nil, err)
	}
	_, err = service.lifecycle.CancelSchedule(ctx, rl.CancelRequest{OperationID: request.OperationID, Context: context})
	if err != nil {
		return collectionResponse(header, contracts.CollectionData{}, nil, err)
	}
	data, err := service.collection(ctx, request.CollectionID)
	return collectionResponse(header, data, &request.OperationID, err)
}
func (service *RoundService) RetryRound(ctx context.Context, request contracts.CollectionCommandRequest) (contracts.CollectionResponse, error) {
	header, err := service.header(ctx)
	if err != nil {
		return contracts.CollectionResponse{}, err
	}
	context, err := commandContext(request, service.session, true)
	if err != nil {
		return collectionResponse(header, contracts.CollectionData{}, nil, err)
	}
	_, err = service.lifecycle.RetryRound(ctx, rl.RetryRequest{OperationID: request.OperationID, Context: context})
	if err != nil {
		return collectionResponse(header, contracts.CollectionData{}, nil, err)
	}
	data, err := service.collection(ctx, request.CollectionID)
	return collectionResponse(header, data, &request.OperationID, err)
}

type roundBootstrapSource struct{ service *RoundService }

func (source roundBootstrapSource) BootstrapProjection(ctx context.Context) (*contracts.DraftSummary, []contracts.ResultReference, error) {
	return source.service.bootstrapProjection(ctx)
}
func RoundBootstrapSource(service *RoundService) BootstrapSource {
	return roundBootstrapSource{service}
}
