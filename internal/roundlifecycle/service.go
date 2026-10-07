package roundlifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/porkyx/jackpot/internal/contracts"
	"github.com/porkyx/jackpot/internal/draw"
)

type Service struct {
	options         ServiceOptions
	recoveryGate    chan struct{}
	mu              sync.Mutex
	executingClaims map[ClaimToken]bool
	admittedRounds  map[contracts.RoundID]bool
	admissionMu     sync.Mutex
	closed          atomic.Bool
	executions      sync.WaitGroup
	cancel          context.CancelFunc
	shutdownOnce    sync.Once
	shutdownDone    chan struct{}
}

func NewService(options ServiceOptions) (*Service, error) {
	if nilPort(options.Storage) || options.Session == "" {
		return nil, contracts.NewFault(contracts.InvalidInput)
	}
	if nilPort(options.Entropy) {
		options.Entropy = draw.CryptoEntropy{}
	}
	if options.Clock == nil {
		options.Clock = time.Now
	}
	if options.NewID == nil {
		options.NewID = uuid.NewString
	}
	if options.ExecutionContext == nil {
		options.ExecutionContext = context.Background()
	}
	if options.AppVersion == "" {
		options.AppVersion = "0.1.0"
	}
	executionContext, cancel := context.WithCancel(options.ExecutionContext)
	options.ExecutionContext = executionContext
	return &Service{options: options, recoveryGate: make(chan struct{}, 1), executingClaims: make(map[ClaimToken]bool), admittedRounds: make(map[contracts.RoundID]bool), cancel: cancel, shutdownDone: make(chan struct{})}, nil
}
func contextError(ctx context.Context) error {
	if ctx == nil {
		return contracts.NewFault(contracts.InvalidInput)
	}
	return ctx.Err()
}
func storageError(err error) error {
	if err == nil {
		return nil
	}
	var fault contracts.Fault
	if errors.As(err, &fault) || errors.Is(err, ErrOperationConflict) || errors.Is(err, ErrAlreadyFinalized) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return contracts.NewFault(contracts.StorageUnavailable)
}

type PreparedCreate struct {
	mu          sync.Mutex
	owner       *Service
	request     CreateCollectionRequest
	fingerprint [32]byte
	replay      *Admission
	closed      bool
}

func (prepared *PreparedCreate) Close() {
	if prepared == nil {
		return
	}
	prepared.mu.Lock()
	defer prepared.mu.Unlock()
	prepared.close()
}
func (prepared *PreparedCreate) close() {
	if prepared.closed {
		return
	}
	prepared.closed = true
	prepared.request = CreateCollectionRequest{}
	prepared.replay = nil
}
func (prepared *PreparedCreate) Replay() (Admission, bool) {
	prepared.mu.Lock()
	defer prepared.mu.Unlock()
	if prepared.replay == nil {
		return Admission{}, false
	}
	return *prepared.replay, true
}

func canonicalCreate(request CreateCollectionRequest) ([32]byte, error) {
	collection := request.Collection
	collection.ID = ""
	public := struct {
		Kind       string
		DraftID    contracts.DraftID
		Revision   contracts.Revision
		Generation contracts.ArticleGeneration
		Collection FrozenCollection
		Input      RoundInput
	}{"CreateCollection", request.Context.DraftID, request.Context.Revision, request.Context.ArticleGeneration, collection, request.Input}
	raw, err := json.Marshal(public)
	if err != nil {
		return [32]byte{}, err
	}
	if len(raw) > 100<<20 {
		return [32]byte{}, contracts.NewFault(contracts.InvalidInput)
	}
	return sha256.Sum256(raw), nil
}
func validateCreate(request CreateCollectionRequest) error {
	if request.OperationID == "" || request.Context.Validate() != nil || request.Collection.SourceDraftID != request.Context.DraftID || request.Collection.FinalizedDraftRevision != request.Context.Revision || request.Collection.ArticleGeneration != request.Context.ArticleGeneration || !request.Collection.Snapshot.Complete || request.Collection.Snapshot.SnapshotID == "" || len(request.Collection.Participants) > 100000 || len(request.Input.CandidateIDs) < 2 {
		return contracts.NewFault(contracts.InvalidInput)
	}
	if err := ValidateRoundInput(request.Input); err != nil {
		return err
	}
	if err := ValidateBadgeInput(request.Collection, request.Input); err != nil {
		return err
	}
	ids := make(map[contracts.ParticipantID]bool, len(request.Collection.Participants))
	included := make([]contracts.ParticipantID, 0)
	for _, participant := range request.Collection.Participants {
		if participant.ID == "" || ids[participant.ID] {
			return contracts.NewFault(contracts.InvalidInput)
		}
		ids[participant.ID] = true
		drawable, err := ParticipantDrawable(participant, request.Input.BadgeRules)
		if err != nil {
			return err
		}
		if drawable {
			included = append(included, participant.ID)
		}
	}
	if len(included) != len(request.Input.CandidateIDs) {
		return contracts.NewFault(contracts.InvalidInput)
	}
	for index, id := range included {
		if request.Input.CandidateIDs[index] != id {
			return contracts.NewFault(contracts.InvalidInput)
		}
	}
	return nil
}
func (service *Service) PrepareCreate(ctx context.Context, request CreateCollectionRequest) (*PreparedCreate, error) {
	done, err := service.beginWork()
	if err != nil {
		return nil, err
	}
	defer done()
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if service.closed.Load() {
		return nil, contracts.NewFault(contracts.InvalidState)
	}
	if request.Context.BackendSessionID != service.options.Session {
		return nil, contracts.NewFault(contracts.BackendSessionChanged)
	}
	if err := validateCreate(request); err != nil {
		return nil, err
	}
	fingerprint, err := canonicalCreate(request)
	if err != nil {
		return nil, contracts.NewFault(contracts.InvalidInput)
	}
	prepared := &PreparedCreate{owner: service, request: request, fingerprint: fingerprint}
	successful := false
	defer func() {
		if !successful {
			prepared.Close()
		}
	}()
	if service.closed.Load() {
		return nil, contracts.NewFault(contracts.InvalidState)
	}
	operation, err := service.options.Storage.ReadOperation(ctx, request.OperationID)
	if err != nil {
		return nil, storageError(err)
	}
	if operation.Status != contracts.OperationUnknown {
		if operation.Operation.Kind != "CreateCollection" || operation.Operation.PublicFingerprint != fingerprint {
			return nil, ErrOperationConflict
		}
		round, err := service.options.Storage.ReadRound(ctx, operation.RoundID)
		if err != nil {
			return nil, storageError(err)
		}
		prepared.replay = &Admission{Operation: operation, Round: round, Replay: true}
		successful = true
		return prepared, nil
	}
	if reader, ok := service.options.Storage.(LifecycleReader); ok {
		id, err := reader.FindCollectionByDraft(ctx, request.Context.DraftID)
		if err == nil {
			return nil, &FinalizedError{CollectionID: id}
		}
		if !errors.Is(err, ErrNotFound) {
			return nil, storageError(err)
		}
	}
	// Freeze private Go-owned input before the caller can reuse its slices.
	raw, err := json.Marshal(request.Collection)
	if err != nil {
		return nil, contracts.NewFault(contracts.InvalidInput)
	}
	var ownedCollection FrozenCollection
	if err = json.Unmarshal(raw, &ownedCollection); err != nil {
		return nil, contracts.NewFault(contracts.InvalidInput)
	}
	prepared.request.Collection = ownedCollection
	raw, err = json.Marshal(request.Input)
	if err != nil {
		return nil, contracts.NewFault(contracts.InvalidInput)
	}
	var ownedInput RoundInput
	if err = json.Unmarshal(raw, &ownedInput); err != nil {
		return nil, contracts.NewFault(contracts.InvalidInput)
	}
	prepared.request.Input = ownedInput
	successful = true
	return prepared, nil
}
func (service *Service) AdmitCreate(ctx context.Context, prepared *PreparedCreate, actual FrozenCollection) (Admission, error) {
	done, workErr := service.beginWork()
	if workErr != nil {
		return Admission{}, workErr
	}
	defer done()
	if prepared == nil {
		return Admission{}, contracts.NewFault(contracts.InvalidInput)
	}
	prepared.mu.Lock()
	defer prepared.mu.Unlock()
	if prepared.owner != service || prepared.closed {
		return Admission{}, contracts.NewFault(contracts.InvalidState)
	}
	defer prepared.close()
	if prepared.replay != nil {
		return *prepared.replay, nil
	}
	if err := contextError(ctx); err != nil {
		return Admission{}, err
	}
	if service.closed.Load() {
		return Admission{}, contracts.NewFault(contracts.InvalidState)
	}
	current := prepared.request
	current.Collection = actual
	hash, err := canonicalCreate(current)
	if err != nil || hash != prepared.fingerprint {
		return Admission{}, contracts.NewFault(contracts.StaleRevision)
	}
	collection := prepared.request.Collection
	collection.ID = contracts.CollectionID(service.options.NewID())
	state := contracts.Executing
	if prepared.request.Input.Mode == ReservationMode {
		state = contracts.PendingSchedule
	}
	admission, err := service.recordAdmission(ctx, AdmitRoundRequest{Operation: OperationIdentity{ID: prepared.request.OperationID, Kind: "CreateCollection", PublicFingerprint: prepared.fingerprint}, CollectionID: collection.ID, ExpectedRevision: prepared.request.Context.Revision, NewCollection: &collection, RoundID: contracts.RoundID(service.options.NewID()), Number: 1, Attempt: 1, Input: prepared.request.Input, InitialState: state, AdmittedAt: service.options.Clock().UTC()})
	if err != nil {
		return Admission{}, storageError(err)
	}
	if admission.Replay {
		return Admission{}, ErrOperationConflict
	}
	service.publish(admission)
	return admission, nil
}
func (service *Service) publish(admission Admission) {
	if service.options.Publish == nil {
		return
	}
	id := admission.Operation.Operation.ID
	_ = service.options.Publish(service.options.ExecutionContext, contracts.StateNotice{BackendSessionID: service.options.Session, EntityKind: contracts.CollectionEntity, EntityID: string(admission.Round.CollectionID), Revision: admission.Round.Revision, OperationID: &id})
}
func (service *Service) CreateCollection(ctx context.Context, request CreateCollectionRequest) (RoundRecord, error) {
	prepared, err := service.PrepareCreate(ctx, request)
	if err != nil {
		return RoundRecord{}, err
	}
	defer prepared.Close()
	admission, err := service.AdmitCreate(ctx, prepared, request.Collection)
	if err != nil {
		return RoundRecord{}, err
	}
	return service.ExecuteAdmission(ctx, admission)
}
func (service *Service) ExecuteAdmission(_ context.Context, admission Admission) (RoundRecord, error) {
	if admission.Replay || admission.Round.State != contracts.Executing {
		if admission.Operation.Status == contracts.OperationFailed {
			return admission.Round, contracts.NewFault(admission.Operation.FailureCode)
		}
		return admission.Round, nil
	}
	service.mu.Lock()
	if service.closed.Load() {
		service.mu.Unlock()
		return RoundRecord{}, contracts.NewFault(contracts.InvalidState)
	}
	service.executions.Add(1)
	service.mu.Unlock()
	defer service.executions.Done()
	ctx := service.options.ExecutionContext
	round := admission.Round
	admittedID := admission.Round.ID
	defer func() { service.mu.Lock(); delete(service.admittedRounds, admittedID); service.mu.Unlock() }()
	claim := round.Claim
	var err error
	if claim == nil {
		claim, err = service.options.Storage.ClaimAttempt(ctx, ClaimRequest{CollectionID: round.CollectionID, RoundID: round.ID, OperationID: admission.Operation.Operation.ID, ExpectedRevision: round.Revision, ExpectedVersion: round.Version, Attempt: round.Attempt, Session: service.options.Session})
	} else if claim.Session != service.options.Session || claim.OperationID != admission.Operation.Operation.ID {
		return RoundRecord{}, contracts.NewFault(contracts.InvalidState)
	}
	if err != nil {
		return RoundRecord{}, storageError(err)
	}
	if claim == nil {
		result, err := service.options.Storage.ReadRound(ctx, round.ID)
		return result, storageError(err)
	}
	service.mu.Lock()
	if service.executingClaims[*claim] {
		service.mu.Unlock()
		result, err := service.options.Storage.ReadRound(ctx, round.ID)
		return result, storageError(err)
	}
	service.executingClaims[*claim] = true
	service.mu.Unlock()
	defer func() { service.mu.Lock(); delete(service.executingClaims, *claim); service.mu.Unlock() }()
	round, err = service.options.Storage.ReadRound(ctx, round.ID)
	if err != nil {
		return RoundRecord{}, storageError(err)
	}
	if round.State != contracts.Executing || round.Claim == nil || *round.Claim != *claim {
		return round, nil
	}
	selected, err := draw.SampleCategories(ctx, round.Input.CandidateIDs, round.Input.CandidateCategories, drawPrizes(round.Input.Prizes), round.Input.BadgeRules, service.options.Entropy)
	if err != nil {
		if ctx.Err() != nil {
			return RoundRecord{}, ctx.Err()
		}
		failed, writeErr := service.options.Storage.RecordAttemptFailure(ctx, AttemptFailureRequest{CollectionID: round.CollectionID, RoundID: round.ID, OperationID: claim.OperationID, ExpectedVersion: claim.Version, Attempt: claim.Attempt, Claim: claim, ExpectedRevision: round.Revision, Code: contracts.InvalidState, FailedAt: service.options.Clock().UTC()})
		if writeErr != nil {
			return RoundRecord{}, storageError(writeErr)
		}
		service.publish(Admission{Operation: admission.Operation, Round: failed})
		return failed, contracts.NewFault(contracts.InvalidState)
	}
	outcome := Outcome{Winners: make([]Winner, len(selected)), ExecutedAt: service.options.Clock().UTC(), AlgorithmVersion: draw.AlgorithmVersion, AppVersion: service.options.AppVersion}
	if round.Input.BadgeRules != nil && round.Input.BadgeRules.WeightingEnabled {
		outcome.AlgorithmVersion = draw.CategoryAlgorithmVersion
	}
	for index, winner := range selected {
		outcome.Winners[index] = Winner{ParticipantID: winner.ParticipantID, PrizeID: PrizeID(winner.PrizeID), Slot: winner.Slot}
	}
	completed, err := service.options.Storage.CommitOutcome(ctx, CommitOutcomeRequest{Claim: *claim, ExpectedRevision: round.Revision, Outcome: outcome})
	if err != nil {
		observed, readErr := service.options.Storage.ReadRound(ctx, round.ID)
		if readErr == nil && observed.State == contracts.Completed && observed.Outcome != nil {
			completed = observed
		} else {
			// A successful fresh read after the write transaction ended proves
			// there is no committed result. Preserve the failed attempt atomically.
			if readErr == nil && observed.State == contracts.Executing && observed.Claim != nil && *observed.Claim == *claim {
				failed, writeErr := service.options.Storage.RecordAttemptFailure(ctx, AttemptFailureRequest{CollectionID: observed.CollectionID, RoundID: observed.ID, OperationID: claim.OperationID, ExpectedVersion: observed.Version, ExpectedRevision: observed.Revision, Attempt: observed.Attempt, Claim: claim, Code: contracts.StorageUnavailable, FailedAt: service.options.Clock().UTC()})
				if writeErr != nil {
					return RoundRecord{}, storageError(writeErr)
				}
				service.publish(Admission{Operation: admission.Operation, Round: failed})
				return failed, storageError(err)
			}
			return RoundRecord{}, storageError(err)
		}
	}
	service.publish(Admission{Operation: admission.Operation, Round: completed})
	return completed, nil
}
func (service *Service) Close(ctx context.Context) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	service.shutdownOnce.Do(func() {
		service.mu.Lock()
		service.closed.Store(true)
		service.mu.Unlock()
		go func() { service.executions.Wait(); service.cancel(); close(service.shutdownDone) }()
	})
	select {
	case <-service.shutdownDone:
		return nil
	case <-ctx.Done():
		service.cancel()
		return ctx.Err()
	}
}

func (service *Service) beginWork() (func(), error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.closed.Load() {
		return nil, contracts.NewFault(contracts.InvalidState)
	}
	service.executions.Add(1)
	return service.executions.Done, nil
}

// Recover and local admission share a short Go boundary, so a timer cannot
// retire the legitimate gap between admission commit and execution claim.
func (service *Service) recordAdmission(ctx context.Context, request AdmitRoundRequest) (Admission, error) {
	service.admissionMu.Lock()
	defer service.admissionMu.Unlock()
	admission, err := service.options.Storage.AdmitRound(ctx, request)
	if err != nil {
		// The SQL seam has ended its transaction before returning. Reconcile
		// an ambiguous acknowledgement through a fresh app-lifetime read.
		observed, readErr := service.options.Storage.ReadOperation(service.options.ExecutionContext, request.Operation.ID)
		if readErr == nil && observed.Status != contracts.OperationUnknown && observed.Operation.Kind == request.Operation.Kind && observed.Operation.PublicFingerprint == request.Operation.PublicFingerprint && observed.CollectionID == request.CollectionID && observed.RoundID == request.RoundID {
			round, roundErr := service.options.Storage.ReadRound(service.options.ExecutionContext, observed.RoundID)
			if roundErr == nil {
				admission = Admission{Operation: observed, Round: round}
				err = nil
			}
		}
	}
	if err == nil && !admission.Replay && admission.Round.State == contracts.Executing {
		service.mu.Lock()
		service.admittedRounds[admission.Round.ID] = true
		service.mu.Unlock()
	}
	return admission, err
}

func nilPort(value any) bool {
	if value == nil {
		return true
	}
	kind := reflect.ValueOf(value).Kind()
	switch kind {
	case reflect.Pointer, reflect.Interface, reflect.Chan, reflect.Func, reflect.Map, reflect.Slice:
		return reflect.ValueOf(value).IsNil()
	default:
		return false
	}
}
