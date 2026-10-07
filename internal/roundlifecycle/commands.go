package roundlifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
)

func publicFingerprint(kind string, payload any) ([32]byte, error) {
	raw, err := json.Marshal(struct {
		Kind    string
		Payload any
	}{kind, payload})
	if err != nil || len(raw) > 100<<20 {
		return [32]byte{}, contracts.NewFault(contracts.InvalidInput)
	}
	return sha256.Sum256(raw), nil
}
func (service *Service) userContext(ctx context.Context, session contracts.BackendSessionID, id contracts.CollectionID) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if service.closed.Load() {
		return contracts.NewFault(contracts.InvalidState)
	}
	if session != service.options.Session {
		return contracts.NewFault(contracts.BackendSessionChanged)
	}
	return nil
}
func (service *Service) replay(ctx context.Context, identity OperationIdentity) (*RoundRecord, error) {
	operation, err := service.options.Storage.ReadOperation(ctx, identity.ID)
	if err != nil {
		return nil, storageError(err)
	}
	if operation.Status == contracts.OperationUnknown {
		return nil, nil
	}
	if operation.Operation.Kind != identity.Kind || operation.Operation.PublicFingerprint != identity.PublicFingerprint {
		return nil, ErrOperationConflict
	}
	round, err := service.options.Storage.ReadRound(ctx, operation.RoundID)
	if err != nil {
		return nil, storageError(err)
	}
	if operation.Status == contracts.OperationFailed {
		return &round, contracts.NewFault(operation.FailureCode)
	}
	return &round, nil
}
func (service *Service) reader() (LifecycleReader, error) {
	reader, ok := service.options.Storage.(LifecycleReader)
	if !ok {
		return nil, contracts.NewFault(contracts.InvalidState)
	}
	return reader, nil
}
func (service *Service) Rerun(ctx context.Context, request RerunRequest) (RoundRecord, error) {
	done, workErr := service.beginWork()
	if workErr != nil {
		return RoundRecord{}, workErr
	}
	defer done()
	if request.OperationID == "" || request.Context.Validate() != nil {
		return RoundRecord{}, contracts.NewFault(contracts.InvalidInput)
	}
	if err := service.userContext(ctx, request.Context.BackendSessionID, request.Context.CollectionID); err != nil {
		return RoundRecord{}, err
	}
	public := request
	public.Context.BackendSessionID = ""
	public.OperationID = ""
	hash, err := publicFingerprint("Rerun", public)
	if err != nil {
		return RoundRecord{}, err
	}
	identity := OperationIdentity{ID: request.OperationID, Kind: "Rerun", PublicFingerprint: hash}
	if round, err := service.replay(ctx, identity); err != nil {
		return RoundRecord{}, err
	} else if round != nil {
		return *round, nil
	}
	reader, err := service.reader()
	if err != nil {
		return RoundRecord{}, err
	}
	rounds, err := reader.ListRounds(ctx, request.Context.CollectionID)
	if err != nil {
		return RoundRecord{}, storageError(err)
	}
	if len(rounds) == 0 {
		return RoundRecord{}, contracts.NewFault(contracts.InvalidState)
	}
	latest := rounds[len(rounds)-1]
	if latest.Revision != request.Context.Revision {
		return RoundRecord{}, contracts.NewFault(contracts.StaleRevision)
	}
	if latest.State != contracts.Completed && latest.State != contracts.Cancelled {
		return RoundRecord{}, contracts.NewFault(contracts.InvalidState)
	}
	if latest.State == contracts.Completed && request.Mode != ImmediateMode {
		return RoundRecord{}, contracts.NewFault(contracts.InvalidInput)
	}
	if latest.Number == math.MaxUint32 {
		return RoundRecord{}, contracts.NewFault(contracts.InvalidState)
	}
	collection, err := service.options.Storage.ReadCollection(ctx, request.Context.CollectionID)
	if err != nil {
		return RoundRecord{}, storageError(err)
	}
	used := make(map[contracts.ParticipantID]bool)
	for _, round := range rounds {
		if round.State == contracts.Completed {
			if round.Outcome == nil || ValidateOutcome(round.Input, *round.Outcome) != nil {
				return RoundRecord{}, contracts.NewFault(contracts.InvalidState)
			}
			for _, winner := range round.Outcome.Winners {
				if used[winner.ParticipantID] {
					return RoundRecord{}, contracts.NewFault(contracts.InvalidState)
				}
				used[winner.ParticipantID] = true
			}
		}
	}
	input := RoundInput{Prizes: append([]Prize(nil), request.Prizes...), Message: request.Message, Mode: request.Mode, CandidateIDs: make([]contracts.ParticipantID, 0)}
	input.BadgeRules = contracts.CloneBadgeRules(collection.Filters.BadgeRules)
	for _, participant := range collection.Participants {
		drawable, err := ParticipantDrawable(participant, input.BadgeRules)
		if err != nil {
			return RoundRecord{}, err
		}
		if drawable && !used[participant.ID] {
			input.CandidateIDs = append(input.CandidateIDs, participant.ID)
			if input.BadgeRules != nil {
				category, err := contracts.ResolveBadgeCategory(participant.BadgeCategory, participant.Kind)
				if err != nil {
					return RoundRecord{}, err
				}
				input.CandidateCategories = append(input.CandidateCategories, category)
			}
		}
	}
	if err = ValidateRoundInput(input); err != nil {
		return RoundRecord{}, err
	}
	state := contracts.Executing
	if request.Mode == ReservationMode {
		state = contracts.PendingSchedule
	}
	admission, err := service.recordAdmission(ctx, AdmitRoundRequest{Operation: identity, CollectionID: collection.ID, ExpectedRevision: request.Context.Revision, RoundID: contracts.RoundID(service.options.NewID()), Number: latest.Number + 1, Attempt: 1, Input: input, InitialState: state, AdmittedAt: service.options.Clock().UTC()})
	if err != nil {
		return RoundRecord{}, storageError(err)
	}
	if !admission.Replay {
		service.publish(admission)
	}
	return service.ExecuteAdmission(ctx, admission)
}
func (service *Service) SetSchedule(ctx context.Context, request SetScheduleRequest) (RoundRecord, error) {
	done, workErr := service.beginWork()
	if workErr != nil {
		return RoundRecord{}, workErr
	}
	defer done()
	if request.OperationID == "" || request.Context.Validate() != nil {
		return RoundRecord{}, contracts.NewFault(contracts.InvalidInput)
	}
	if err := service.userContext(ctx, request.Context.BackendSessionID, request.Context.CollectionID); err != nil {
		return RoundRecord{}, err
	}
	public := struct {
		Context           contracts.RoundContext
		ScheduledAt       *time.Time
		QuickDelaySeconds *uint32
		Timezone          string
	}{Context: request.Context, QuickDelaySeconds: request.QuickDelaySeconds, Timezone: request.Timezone}
	public.Context.BackendSessionID = ""
	if !request.ScheduledAt.IsZero() {
		scheduled := request.ScheduledAt
		public.ScheduledAt = &scheduled
	}
	hash, err := publicFingerprint("SetSchedule", public)
	if err != nil {
		return RoundRecord{}, err
	}
	identity := OperationIdentity{ID: request.OperationID, Kind: "SetSchedule", PublicFingerprint: hash}
	if replay, err := service.replay(ctx, identity); err != nil {
		return RoundRecord{}, err
	} else if replay != nil {
		return *replay, nil
	}
	acceptedAt := service.options.Clock().UTC()
	scheduledAt, err := scheduleTime(request, acceptedAt)
	if err != nil {
		return RoundRecord{}, err
	}
	result, err := service.options.Storage.SetSchedule(ctx, ScheduleRequest{Operation: identity, CollectionID: request.Context.CollectionID, RoundID: request.Context.RoundID, ExpectedRevision: request.Context.Revision, ExpectedVersion: request.Context.Version, AcceptedAt: acceptedAt, ScheduledAt: scheduledAt, Timezone: request.Timezone})
	if err != nil {
		return RoundRecord{}, storageError(err)
	}
	service.publish(Admission{Operation: OperationRecord{Operation: identity}, Round: result})
	return result, nil
}
func (service *Service) CancelSchedule(ctx context.Context, request CancelRequest) (RoundRecord, error) {
	done, workErr := service.beginWork()
	if workErr != nil {
		return RoundRecord{}, workErr
	}
	defer done()
	if request.OperationID == "" || request.Context.Validate() != nil {
		return RoundRecord{}, contracts.NewFault(contracts.InvalidInput)
	}
	if err := service.userContext(ctx, request.Context.BackendSessionID, request.Context.CollectionID); err != nil {
		return RoundRecord{}, err
	}
	public := request
	public.Context.BackendSessionID = ""
	public.OperationID = ""
	hash, err := publicFingerprint("CancelSchedule", public)
	if err != nil {
		return RoundRecord{}, err
	}
	identity := OperationIdentity{ID: request.OperationID, Kind: "CancelSchedule", PublicFingerprint: hash}
	if replay, err := service.replay(ctx, identity); err != nil {
		return RoundRecord{}, err
	} else if replay != nil {
		return *replay, nil
	}
	result, err := service.options.Storage.CancelSchedule(ctx, CancelScheduleRequest{Operation: identity, CollectionID: request.Context.CollectionID, RoundID: request.Context.RoundID, ExpectedRevision: request.Context.Revision, ExpectedVersion: request.Context.Version, AcceptedAt: service.options.Clock().UTC()})
	if err != nil {
		return RoundRecord{}, storageError(err)
	}
	service.publish(Admission{Operation: OperationRecord{Operation: identity}, Round: result})
	return result, nil
}
func (service *Service) RetryRound(ctx context.Context, request RetryRequest) (RoundRecord, error) {
	done, workErr := service.beginWork()
	if workErr != nil {
		return RoundRecord{}, workErr
	}
	defer done()
	if request.OperationID == "" || request.Context.Validate() != nil {
		return RoundRecord{}, contracts.NewFault(contracts.InvalidInput)
	}
	if err := service.userContext(ctx, request.Context.BackendSessionID, request.Context.CollectionID); err != nil {
		return RoundRecord{}, err
	}
	public := request
	public.Context.BackendSessionID = ""
	public.OperationID = ""
	hash, err := publicFingerprint("RetryRound", public)
	if err != nil {
		return RoundRecord{}, err
	}
	identity := OperationIdentity{ID: request.OperationID, Kind: "RetryRound", PublicFingerprint: hash}
	if replay, err := service.replay(ctx, identity); err != nil {
		return RoundRecord{}, err
	} else if replay != nil {
		return *replay, nil
	}
	round, err := service.options.Storage.ReadRound(ctx, request.Context.RoundID)
	if err != nil {
		return RoundRecord{}, storageError(err)
	}
	if round.CollectionID != request.Context.CollectionID || round.Revision != request.Context.Revision || round.Version != request.Context.Version {
		return RoundRecord{}, contracts.NewFault(contracts.StaleRevision)
	}
	if round.State != contracts.Failed || round.Outcome != nil || round.Attempt == math.MaxUint32 {
		return RoundRecord{}, contracts.NewFault(contracts.InvalidState)
	}
	admission, err := service.recordAdmission(ctx, AdmitRoundRequest{Operation: identity, CollectionID: round.CollectionID, RoundID: round.ID, ExpectedRevision: round.Revision, ExpectedVersion: round.Version, Number: round.Number, Attempt: round.Attempt + 1, Input: round.Input, InitialState: contracts.Executing, AdmittedAt: service.options.Clock().UTC()})
	if err != nil {
		return RoundRecord{}, storageError(err)
	}
	if !admission.Replay {
		service.publish(admission)
	}
	return service.ExecuteAdmission(ctx, admission)
}
func (service *Service) ExecuteDue(ctx context.Context, request ExecuteDueRequest) (RoundRecord, error) {
	done, workErr := service.beginWork()
	if workErr != nil {
		return RoundRecord{}, workErr
	}
	defer done()
	if err := contextError(ctx); err != nil {
		return RoundRecord{}, err
	}
	if service.closed.Load() {
		return RoundRecord{}, contracts.NewFault(contracts.InvalidState)
	}
	round, err := service.options.Storage.ReadRound(ctx, request.RoundID)
	if err != nil {
		return RoundRecord{}, storageError(err)
	}
	if round.State != contracts.Scheduled {
		return round, nil
	}
	if round.ScheduledAt == nil {
		return RoundRecord{}, contracts.NewFault(contracts.InvalidState)
	}
	if service.options.Clock().Before(*round.ScheduledAt) {
		return round, nil
	}
	id := contracts.OperationID(fmt.Sprintf("due:%s:%d", round.ID, round.Attempt))
	hash, err := publicFingerprint("ExecuteDue", struct {
		RoundID contracts.RoundID
		Attempt uint32
	}{round.ID, round.Attempt})
	if err != nil {
		return RoundRecord{}, err
	}
	admission, err := service.recordAdmission(ctx, AdmitRoundRequest{Operation: OperationIdentity{ID: id, Kind: "ExecuteDue", PublicFingerprint: hash}, CollectionID: round.CollectionID, RoundID: round.ID, ExpectedRevision: round.Revision, ExpectedVersion: round.Version, Number: round.Number, Attempt: round.Attempt, Input: round.Input, InitialState: contracts.Executing, AdmittedAt: service.options.Clock().UTC(), Session: service.options.Session})
	if err != nil {
		return RoundRecord{}, storageError(err)
	}
	if !admission.Replay {
		service.publish(admission)
	}
	return service.ExecuteAdmission(ctx, admission)
}
func (service *Service) Recover(ctx context.Context, _ RecoverRequest) (RecoveryReport, error) {
	done, workErr := service.beginWork()
	if workErr != nil {
		return RecoveryReport{}, workErr
	}
	defer done()
	if err := contextError(ctx); err != nil {
		return RecoveryReport{}, err
	}
	if service.closed.Load() {
		return RecoveryReport{}, contracts.NewFault(contracts.InvalidState)
	}
	// A duplicate timer/startup wake waits for the current ordered batch. Holding
	// the gate through ExecuteDue keeps deadline/ID ordering across Recover calls.
	select {
	case service.recoveryGate <- struct{}{}:
		defer func() { <-service.recoveryGate }()
	case <-ctx.Done():
		return RecoveryReport{}, ctx.Err()
	}
	if err := contextError(ctx); err != nil {
		return RecoveryReport{}, err
	}
	if service.closed.Load() {
		return RecoveryReport{}, contracts.NewFault(contracts.InvalidState)
	}
	reader, err := service.reader()
	if err != nil {
		return RecoveryReport{}, err
	}
	rounds, err := reader.ListRecoverableRounds(ctx)
	if err != nil {
		return RecoveryReport{}, storageError(err)
	}
	report := RecoveryReport{Completed: make([]contracts.RoundID, 0), Failed: make([]contracts.RoundID, 0), Due: make([]contracts.RoundID, 0)}
	for _, snapshot := range rounds {
		round, err := service.options.Storage.ReadRound(ctx, snapshot.ID)
		if err != nil {
			return report, storageError(err)
		}
		if round.State == contracts.Completed {
			if round.Outcome == nil || ValidateOutcome(round.Input, *round.Outcome) != nil {
				return report, contracts.NewFault(contracts.InvalidState)
			}
			report.Completed = append(report.Completed, round.ID)
			continue
		}
		if round.State == contracts.Scheduled {
			if round.ScheduledAt == nil {
				return report, contracts.NewFault(contracts.InvalidState)
			}
			if !service.options.Clock().Before(*round.ScheduledAt) {
				report.Due = append(report.Due, round.ID)
				result, err := service.ExecuteDue(ctx, ExecuteDueRequest{RoundID: round.ID})
				if err != nil {
					return report, err
				}
				if result.State == contracts.Completed {
					report.Completed = append(report.Completed, round.ID)
				}
			}
			continue
		}
		if round.State != contracts.Executing {
			continue
		}
		failed, operationID, err := service.recoverExecuting(ctx, round.ID)
		if err != nil {
			return report, err
		}
		if failed != nil {
			report.Failed = append(report.Failed, failed.ID)
			service.publish(Admission{Operation: OperationRecord{Operation: OperationIdentity{ID: operationID}}, Round: *failed})
		}
	}
	return report, nil
}

// The active operation is durable Go-owned state, never a caller supplied actor.
type activeOperationReader interface {
	ReadActiveOperation(context.Context, contracts.RoundID) (OperationRecord, error)
}

func pendingOperationForRound(ctx context.Context, storage Storage, round RoundRecord) (contracts.OperationID, error) {
	reader, ok := storage.(activeOperationReader)
	if !ok {
		return "", contracts.NewFault(contracts.InvalidState)
	}
	operation, err := reader.ReadActiveOperation(ctx, round.ID)
	if err != nil {
		return "", storageError(err)
	}
	if operation.Status != contracts.OperationPending || operation.CollectionID != round.CollectionID || operation.RoundID != round.ID {
		return "", contracts.NewFault(contracts.InvalidState)
	}
	return operation.Operation.ID, nil
}

func (service *Service) recoverExecuting(ctx context.Context, id contracts.RoundID) (*RoundRecord, contracts.OperationID, error) {
	service.admissionMu.Lock()
	defer service.admissionMu.Unlock()
	round, err := service.options.Storage.ReadRound(ctx, id)
	if err != nil {
		return nil, "", storageError(err)
	}
	if round.State != contracts.Executing {
		return nil, "", nil
	}
	service.mu.Lock()
	live := service.admittedRounds[id] || (round.Claim != nil && service.executingClaims[*round.Claim])
	service.mu.Unlock()
	if live {
		return nil, "", nil
	}
	operationID := contracts.OperationID("")
	if round.Claim != nil {
		operationID = round.Claim.OperationID
	} else {
		operationID, err = pendingOperationForRound(ctx, service.options.Storage, round)
		if err != nil {
			return nil, "", err
		}
	}
	failed, err := service.options.Storage.RecordAttemptFailure(ctx, AttemptFailureRequest{CollectionID: round.CollectionID, RoundID: round.ID, OperationID: operationID, ExpectedVersion: round.Version, ExpectedRevision: round.Revision, Attempt: round.Attempt, Claim: round.Claim, Code: contracts.InvalidState, FailedAt: service.options.Clock().UTC()})
	if err != nil {
		var fault contracts.Fault
		if errors.As(err, &fault) && fault.Code == contracts.StaleRevision {
			return nil, "", nil
		}
		return nil, "", storageError(err)
	}
	return &failed, operationID, nil
}

func scheduleTime(request SetScheduleRequest, acceptedAt time.Time) (time.Time, error) {
	if request.Timezone != "Asia/Seoul" || (request.QuickDelaySeconds == nil) == request.ScheduledAt.IsZero() {
		return time.Time{}, contracts.NewFault(contracts.InvalidInput)
	}
	if _, err := contracts.NewResponseHeader("schedule", acceptedAt); err != nil {
		return time.Time{}, contracts.NewFault(contracts.InvalidInput)
	}
	if request.QuickDelaySeconds != nil {
		switch *request.QuickDelaySeconds {
		case 10, 30, 60, 120:
			return acceptedAt.Add(time.Duration(*request.QuickDelaySeconds) * time.Second), nil
		default:
			return time.Time{}, contracts.NewFault(contracts.InvalidInput)
		}
	}
	scheduled := request.ScheduledAt.UTC()
	if _, err := contracts.NewResponseHeader("schedule", scheduled); err != nil {
		return time.Time{}, contracts.NewFault(contracts.InvalidInput)
	}
	if scheduled.Second() != 0 || scheduled.Nanosecond() != 0 {
		return time.Time{}, contracts.NewFault(contracts.InvalidInput)
	}
	kst := time.FixedZone("Asia/Seoul", 9*60*60)
	local := acceptedAt.In(kst)
	dayStart := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, kst)
	if scheduled.Before(dayStart) || !scheduled.Before(dayStart.AddDate(0, 0, 4)) || scheduled.Before(acceptedAt.Add(10*time.Second)) {
		return time.Time{}, contracts.NewFault(contracts.InvalidInput)
	}
	return scheduled, nil
}
