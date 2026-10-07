package roundlifecycle

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
)

var commandPortFailure = errors.New("test-owned port failure")

// Each optional seam refuses unexpected calls; exact call counts distinguish
// refusal from an accidentally successful continuation. Entropy is a counted port.
type commandPorts struct {
	Storage
	mu          sync.Mutex
	calls       map[string]int
	operation   func(context.Context, contracts.OperationID) (OperationRecord, error)
	round       func(context.Context, contracts.RoundID) (RoundRecord, error)
	collection  func(context.Context, contracts.CollectionID) (FrozenCollection, error)
	list        func(context.Context, contracts.CollectionID) ([]RoundRecord, error)
	recoverable func(context.Context) ([]RoundRecord, error)
	find        func(context.Context, contracts.DraftID) (contracts.CollectionID, error)
	active      func(context.Context, contracts.RoundID) (OperationRecord, error)
	admit       func(context.Context, AdmitRoundRequest) (Admission, error)
	claim       func(context.Context, ClaimRequest) (*ClaimToken, error)
	commit      func(context.Context, CommitOutcomeRequest) (RoundRecord, error)
	failure     func(context.Context, AttemptFailureRequest) (RoundRecord, error)
}

func (p *commandPorts) call(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.calls == nil {
		p.calls = map[string]int{}
	}
	p.calls[name]++
}
func (p *commandPorts) counts() map[string]int {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := map[string]int{}
	for k, v := range p.calls {
		out[k] = v
	}
	return out
}
func (p *commandPorts) ReadOperation(ctx context.Context, id contracts.OperationID) (OperationRecord, error) {
	p.call("operation")
	if p.operation != nil {
		return p.operation(ctx, id)
	}
	return OperationRecord{}, commandPortFailure
}
func (p *commandPorts) ReadRound(ctx context.Context, id contracts.RoundID) (RoundRecord, error) {
	p.call("round")
	if p.round != nil {
		return p.round(ctx, id)
	}
	return RoundRecord{}, commandPortFailure
}
func (p *commandPorts) ReadCollection(ctx context.Context, id contracts.CollectionID) (FrozenCollection, error) {
	p.call("collection")
	if p.collection != nil {
		return p.collection(ctx, id)
	}
	return FrozenCollection{}, commandPortFailure
}
func (p *commandPorts) ListRounds(ctx context.Context, id contracts.CollectionID) ([]RoundRecord, error) {
	p.call("list")
	if p.list != nil {
		return p.list(ctx, id)
	}
	return nil, commandPortFailure
}
func (p *commandPorts) ListRecoverableRounds(ctx context.Context) ([]RoundRecord, error) {
	p.call("recoverable")
	if p.recoverable != nil {
		return p.recoverable(ctx)
	}
	return nil, commandPortFailure
}
func (p *commandPorts) FindCollectionByDraft(ctx context.Context, id contracts.DraftID) (contracts.CollectionID, error) {
	p.call("find")
	if p.find != nil {
		return p.find(ctx, id)
	}
	return "", ErrNotFound
}
func (p *commandPorts) ListCollections(context.Context, uint32, uint32) ([]CollectionRecord, error) {
	p.call("collections")
	return nil, commandPortFailure
}
func (p *commandPorts) ReadActiveOperation(ctx context.Context, id contracts.RoundID) (OperationRecord, error) {
	p.call("active")
	if p.active != nil {
		return p.active(ctx, id)
	}
	return OperationRecord{}, commandPortFailure
}
func (p *commandPorts) AdmitRound(ctx context.Context, r AdmitRoundRequest) (Admission, error) {
	p.call("admit")
	if p.admit != nil {
		return p.admit(ctx, r)
	}
	return Admission{}, commandPortFailure
}
func (p *commandPorts) ClaimAttempt(ctx context.Context, r ClaimRequest) (*ClaimToken, error) {
	p.call("claim")
	if p.claim != nil {
		return p.claim(ctx, r)
	}
	return nil, commandPortFailure
}
func (p *commandPorts) CommitOutcome(ctx context.Context, r CommitOutcomeRequest) (RoundRecord, error) {
	p.call("commit")
	if p.commit != nil {
		return p.commit(ctx, r)
	}
	return RoundRecord{}, commandPortFailure
}
func (p *commandPorts) RecordAttemptFailure(ctx context.Context, r AttemptFailureRequest) (RoundRecord, error) {
	p.call("failure")
	if p.failure != nil {
		return p.failure(ctx, r)
	}
	return RoundRecord{}, commandPortFailure
}
func (p *commandPorts) SetSchedule(context.Context, ScheduleRequest) (RoundRecord, error) {
	p.call("schedule")
	return RoundRecord{}, commandPortFailure
}
func (p *commandPorts) CancelSchedule(context.Context, CancelScheduleRequest) (RoundRecord, error) {
	p.call("cancel")
	return RoundRecord{}, commandPortFailure
}

type commandEntropy struct {
	calls  atomic.Int32
	sample func(context.Context, uint64) (uint64, error)
}

func (e *commandEntropy) Intn(ctx context.Context, n uint64) (uint64, error) {
	e.calls.Add(1)
	if e.sample != nil {
		return e.sample(ctx, n)
	}
	return 0, nil
}

type commandServiceEvidence struct {
	entropy      *commandEntropy
	notices, ids atomic.Int32
}

func newCommandService(t *testing.T, p Storage) (*Service, *commandServiceEvidence) {
	t.Helper()
	e := &commandServiceEvidence{entropy: &commandEntropy{}}
	s, err := NewService(ServiceOptions{Storage: p, Session: "boundary-session", Entropy: e.entropy, Clock: func() time.Time { return commandTime() }, NewID: func() string {
		if e.ids.Add(1) == 1 {
			return "collection"
		}
		return "round"
	}, Publish: func(context.Context, contracts.StateNotice) error { e.notices.Add(1); return nil }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return s, e
}
func commandTime() time.Time { return time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC) }
func commandRound() RoundRecord {
	return RoundRecord{CollectionID: "collection", ID: "round", Number: 1, Attempt: 1, State: contracts.Executing, Revision: 1, Version: 1, Input: RoundInput{CandidateIDs: []contracts.ParticipantID{"a", "b", "c"}, Prizes: []Prize{{ID: "prize", Name: "name", Count: 1}}, Mode: ImmediateMode}}
}
func commandClaim() ClaimToken {
	return ClaimToken{CollectionID: "collection", RoundID: "round", OperationID: "operation", Session: "boundary-session", Attempt: 1, Version: 1}
}
func commandCompleted(id contracts.RoundID) RoundRecord {
	r := commandRound()
	r.ID = id
	r.State = contracts.Completed
	r.Outcome = &Outcome{Winners: []Winner{{ParticipantID: "a", PrizeID: "prize", Slot: 1}}, ExecutedAt: commandTime(), AlgorithmVersion: "sample", AppVersion: "test"}
	return r
}
func commandContext() contracts.RoundContext {
	return contracts.RoundContext{CollectionContext: contracts.CollectionContext{BackendSessionID: "boundary-session", CollectionID: "collection", Revision: 1}, RoundID: "round", Version: 1}
}
func assertCommandCalls(t *testing.T, p *commandPorts, want map[string]int) {
	t.Helper()
	if actual := p.counts(); !reflect.DeepEqual(actual, want) {
		t.Fatalf("port calls=%v want=%v", actual, want)
	}
}
func assertCommandOwnersEmpty(t *testing.T, s *Service, e *commandServiceEvidence, wantRNG, wantNotices int32) {
	t.Helper()
	s.mu.Lock()
	claims, admitted := len(s.executingClaims), len(s.admittedRounds)
	s.mu.Unlock()
	if claims != 0 || admitted != 0 || len(s.recoveryGate) != 0 || e.entropy.calls.Load() != wantRNG || e.notices.Load() != wantNotices {
		t.Fatalf("owners claims=%d admitted=%d gate=%d RNG=%d notices=%d", claims, admitted, len(s.recoveryGate), e.entropy.calls.Load(), e.notices.Load())
	}
}
func commandUnknown(context.Context, contracts.OperationID) (OperationRecord, error) {
	return OperationRecord{Status: contracts.OperationUnknown}, nil
}

func TestCommandReplayReadFailuresPreserveStateAndNeverExecute(t *testing.T) {
	for _, kind := range []string{"Rerun", "CancelSchedule", "RetryRound"} {
		for _, where := range []string{"operation", "round"} {
			t.Run(kind+"/"+where, func(t *testing.T) {
				rc := commandContext()
				rr := RerunRequest{OperationID: "operation", Context: rc.CollectionContext, Prizes: commandRound().Input.Prizes, Mode: ImmediateMode}
				cr := CancelRequest{OperationID: "operation", Context: rc}
				retry := RetryRequest{OperationID: "operation", Context: rc}
				var payload any
				switch kind {
				case "Rerun":
					pub := rr
					pub.Context.BackendSessionID = ""
					pub.OperationID = ""
					payload = pub
				case "CancelSchedule":
					pub := cr
					pub.Context.BackendSessionID = ""
					pub.OperationID = ""
					payload = pub
				case "RetryRound":
					pub := retry
					pub.Context.BackendSessionID = ""
					pub.OperationID = ""
					payload = pub
				}
				hash, err := publicFingerprint(kind, payload)
				if err != nil {
					t.Fatal(err)
				}
				p := &commandPorts{operation: func(context.Context, contracts.OperationID) (OperationRecord, error) {
					if where == "operation" {
						return OperationRecord{}, commandPortFailure
					}
					return OperationRecord{Operation: OperationIdentity{ID: "operation", Kind: kind, PublicFingerprint: hash}, Status: contracts.OperationSucceeded, CollectionID: "collection", RoundID: "round"}, nil
				}, round: func(context.Context, contracts.RoundID) (RoundRecord, error) {
					return RoundRecord{}, commandPortFailure
				}}
				s, e := newCommandService(t, p)

				var result RoundRecord
				switch kind {
				case "Rerun":
					result, err = s.Rerun(context.Background(), rr)
				case "CancelSchedule":
					result, err = s.CancelSchedule(context.Background(), cr)
				case "RetryRound":
					result, err = s.RetryRound(context.Background(), retry)
				}
				requireBoundaryFault(t, err, contracts.StorageUnavailable)
				if !reflect.DeepEqual(result, RoundRecord{}) {
					t.Fatal("read failure returned a result or revoked established authority")
				}
				want := map[string]int{"operation": 1}
				if where == "round" {
					want["round"] = 1
				}
				assertCommandCalls(t, p, want)
				assertCommandOwnersEmpty(t, s, e, 0, 0)
			})
		}
	}
}

func TestCommandRerunRefusesMissingEmptyOrCorruptHistoryBeforeAdmission(t *testing.T) {
	t.Run("missing-reader", func(t *testing.T) {
		p := boundaryPreparingStorage()
		s, e := newCommandService(t, p)

		_, err := s.Rerun(context.Background(), RerunRequest{OperationID: "rerun", Context: commandContext().CollectionContext, Mode: ImmediateMode})
		requireBoundaryFault(t, err, contracts.InvalidState)
		if p.operations.Load() != 1 || p.rounds.Load() != 0 || p.admissions.Load() != 0 {
			t.Fatal("unexpected storage effect")
		}
		assertCommandOwnersEmpty(t, s, e, 0, 0)
	})
	for _, test := range []struct {
		name          string
		rounds        []RoundRecord
		collectionErr error
		code          contracts.ErrorCode
	}{
		{name: "empty", rounds: []RoundRecord{}, code: contracts.InvalidState},
		{name: "collection-error", rounds: []RoundRecord{commandCompleted("round")}, collectionErr: commandPortFailure, code: contracts.StorageUnavailable},
		{name: "nil-outcome", rounds: []RoundRecord{func() RoundRecord { r := commandCompleted("round"); r.Outcome = nil; return r }()}, code: contracts.InvalidState},
		{name: "invalid-outcome", rounds: []RoundRecord{func() RoundRecord { r := commandCompleted("round"); r.Outcome.Winners[0].Slot = 2; return r }()}, code: contracts.InvalidState},
		{name: "repeated-winner-across-valid-rounds", rounds: []RoundRecord{commandCompleted("old"), commandCompleted("round")}, code: contracts.InvalidState},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := &commandPorts{operation: commandUnknown, list: func(context.Context, contracts.CollectionID) ([]RoundRecord, error) { return test.rounds, nil }, collection: func(context.Context, contracts.CollectionID) (FrozenCollection, error) {
				return boundaryCreateRequest().Collection, test.collectionErr
			}}
			s, e := newCommandService(t, p)

			result, err := s.Rerun(context.Background(), RerunRequest{OperationID: "rerun", Context: commandContext().CollectionContext, Prizes: commandRound().Input.Prizes, Mode: ImmediateMode})
			requireBoundaryFault(t, err, test.code)
			if !reflect.DeepEqual(result, RoundRecord{}) {
				t.Fatal("invalid history returned result")
			}
			want := map[string]int{"operation": 1, "list": 1}
			if len(test.rounds) > 0 {
				want["collection"] = 1
			}
			assertCommandCalls(t, p, want)
			assertCommandOwnersEmpty(t, s, e, 0, 0)

		})
	}
}

func TestCommandPreparePreconditionsAndPortErrorsLeaveNoPreparedInputOrMutation(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, name := range []string{"nil-context", "canceled", "session", "round-input", "operation", "find", "replay-round"} {
		t.Run(name, func(t *testing.T) {
			request := boundaryCreateRequest()
			ctx := context.Context(context.Background())
			p := &commandPorts{operation: commandUnknown}
			s, e := newCommandService(t, p)
			want := map[string]int{}
			code := contracts.InvalidInput
			var wantErr error
			switch name {
			case "nil-context":
				ctx = nil
			case "canceled":
				ctx = canceled
				wantErr = context.Canceled
			case "session":
				request.Context.BackendSessionID = "other"
				code = contracts.BackendSessionChanged

			case "round-input":
				request.Input.Prizes[0].Count = 0
			case "operation":
				p.operation = func(context.Context, contracts.OperationID) (OperationRecord, error) {
					return OperationRecord{}, commandPortFailure
				}
				want["operation"] = 1
				code = contracts.StorageUnavailable
			case "find":
				p.find = func(context.Context, contracts.DraftID) (contracts.CollectionID, error) {
					return "", commandPortFailure
				}
				want["operation"] = 1
				want["find"] = 1
				code = contracts.StorageUnavailable
			case "replay-round":
				hash, err := canonicalCreate(request)
				if err != nil {
					t.Fatal(err)
				}
				p.operation = func(context.Context, contracts.OperationID) (OperationRecord, error) {
					return OperationRecord{Operation: OperationIdentity{Kind: "CreateCollection", PublicFingerprint: hash}, Status: contracts.OperationSucceeded, CollectionID: "collection", RoundID: "round"}, nil
				}
				p.collection = func(context.Context, contracts.CollectionID) (FrozenCollection, error) {
					return request.Collection, nil
				}
				want["operation"] = 1
				code = contracts.StorageUnavailable
				want["round"] = 1
			}
			prepared, err := s.PrepareCreate(ctx, request)
			if prepared != nil {
				prepared.Close()
				t.Fatal("failure yielded owned lease")
			}
			if wantErr != nil {
				if !errors.Is(err, wantErr) {
					t.Fatalf("error=%v", err)
				}
			} else {
				requireBoundaryFault(t, err, code)
			}
			assertCommandCalls(t, p, want)
			assertCommandOwnersEmpty(t, s, e, 0, 0)

		})
	}
}

type commandDoneObserved struct {
	context.Context
	entered chan struct{}
	once    sync.Once
}

func (c *commandDoneObserved) Done() <-chan struct{} {
	c.once.Do(func() { close(c.entered) })
	return c.Context.Done()
}

func TestCommandPreparedAdmissionChangedSnapshotReplayRaceAndClosedOwnerRefuseSafely(t *testing.T) {
	for _, name := range []string{"changed", "unrepresentable-time", "replay-race", "closed"} {
		t.Run(name, func(t *testing.T) {
			p := &commandPorts{operation: commandUnknown, admit: func(_ context.Context, r AdmitRoundRequest) (Admission, error) {
				return Admission{Replay: true, Round: RoundRecord{CollectionID: r.CollectionID, ID: r.RoundID, State: contracts.PendingSchedule}}, nil
			}}
			s, e := newCommandService(t, p)
			req := boundaryCreateRequest()
			lease, err := s.PrepareCreate(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			defer lease.Close()
			actual := req.Collection
			code := contracts.StaleRevision
			switch name {
			case "changed":
				actual.Article.Title = "changed"
			case "unrepresentable-time":
				bad := time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
				actual.Article.PostedAt = &bad
			case "replay-race":
			case "closed":
				if err := s.Close(context.Background()); err != nil {
					t.Fatal(err)
				}
				code = contracts.InvalidState
			}
			a, err := s.AdmitCreate(context.Background(), lease, actual)
			if name == "replay-race" {
				if !errors.Is(err, ErrOperationConflict) {
					t.Fatalf("error=%v", err)
				}
			} else {
				requireBoundaryFault(t, err, code)
			}
			if !reflect.DeepEqual(a, Admission{}) {
				t.Fatal("refusal exposed admission")
			}
			lease.Close()
			if !lease.closed || !reflect.DeepEqual(lease.request, CreateCollectionRequest{}) {
				t.Fatal("private lease data retained")
			}
			want := map[string]int{"operation": 1, "find": 1}
			if name == "replay-race" {
				want["admit"] = 1
				if e.ids.Load() != 2 {
					t.Fatal("race generated incorrect IDs")
				}
			} else if e.ids.Load() != 0 {
				t.Fatal("refusal generated IDs")
			}
			assertCommandCalls(t, p, want)
			assertCommandOwnersEmpty(t, s, e, 0, 0)
		})
	}
}

func TestCommandExecuteAdmissionClaimErrorsAndEveryObservationFenceConsumeNoEntropy(t *testing.T) {
	for _, name := range []string{"closed", "foreign-session", "foreign-operation", "claim-error", "observed-terminal", "observed-no-claim", "observed-different-claim"} {
		t.Run(name, func(t *testing.T) {
			r := commandRound()
			claim := commandClaim()
			p := &commandPorts{claim: func(context.Context, ClaimRequest) (*ClaimToken, error) { return nil, commandPortFailure }}
			s, e := newCommandService(t, p)
			want := map[string]int{}
			var wantCode contracts.ErrorCode
			var expected RoundRecord
			switch name {
			case "closed":
				if err := s.Close(context.Background()); err != nil {
					t.Fatal(err)
				}
				wantCode = contracts.InvalidState
			case "foreign-session":
				claim.Session = "other"
				r.Claim = &claim
				wantCode = contracts.InvalidState
			case "foreign-operation":
				claim.OperationID = "other"
				r.Claim = &claim
				wantCode = contracts.InvalidState
			case "claim-error":
				want["claim"] = 1
				wantCode = contracts.StorageUnavailable
			default:
				r.Claim = &claim
				expected = commandRound()
				expected.Claim = &claim
				switch name {
				case "observed-terminal":
					expected.State = contracts.Cancelled
				case "observed-no-claim":
					expected.Claim = nil
				case "observed-different-claim":
					changed := claim
					changed.Version++
					expected.Claim = &changed
				}
				p.round = func(context.Context, contracts.RoundID) (RoundRecord, error) { return expected, nil }
				want["round"] = 1
			}
			result, err := s.ExecuteAdmission(context.Background(), Admission{Operation: OperationRecord{Operation: OperationIdentity{ID: "operation"}}, Round: r})
			if wantCode != "" {
				requireBoundaryFault(t, err, wantCode)
				if !reflect.DeepEqual(result, RoundRecord{}) {
					t.Fatal("failure returned computed round")
				}
			} else if err != nil || !reflect.DeepEqual(result, expected) {
				t.Fatalf("observation=%+v error=%v", result, err)
			}
			assertCommandCalls(t, p, want)
			assertCommandOwnersEmpty(t, s, e, 0, 0)
		})
	}
}

func TestCommandCommitThenFailureWriteErrorNeverPublishesUncommittedOutcome(t *testing.T) {
	r := commandRound()
	claim := commandClaim()
	r.Claim = &claim
	p := &commandPorts{round: func(context.Context, contracts.RoundID) (RoundRecord, error) { return r, nil }, commit: func(context.Context, CommitOutcomeRequest) (RoundRecord, error) {
		return RoundRecord{}, commandPortFailure
	}, failure: func(_ context.Context, req AttemptFailureRequest) (RoundRecord, error) {
		if req.Code != contracts.StorageUnavailable || req.Claim == nil || *req.Claim != claim || req.ExpectedRevision != r.Revision || req.ExpectedVersion != r.Version {
			t.Error("failure lost exact durable fence")
		}
		return RoundRecord{}, context.DeadlineExceeded
	}}
	s, e := newCommandService(t, p)
	result, err := s.ExecuteAdmission(context.Background(), Admission{Operation: OperationRecord{Operation: OperationIdentity{ID: "operation"}}, Round: r})
	if !errors.Is(err, context.DeadlineExceeded) || !reflect.DeepEqual(result, RoundRecord{}) {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	assertCommandCalls(t, p, map[string]int{"round": 2, "commit": 1, "failure": 1})
	assertCommandOwnersEmpty(t, s, e, 1, 0)
}

func TestCommandDuplicateLiveClaimReadsOnlyAndRecoveryCannotRetireOwner(t *testing.T) {
	r := commandRound()
	claim := commandClaim()
	r.Claim = &claim
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	p := &commandPorts{round: func(context.Context, contracts.RoundID) (RoundRecord, error) { return r, nil }, recoverable: func(context.Context) ([]RoundRecord, error) { return []RoundRecord{r}, nil }, commit: func(_ context.Context, req CommitOutcomeRequest) (RoundRecord, error) {
		completed := r
		completed.State = contracts.Completed
		completed.Outcome = &req.Outcome
		return completed, nil
	}}
	s, e := newCommandService(t, p)
	e.entropy.sample = func(ctx context.Context, _ uint64) (uint64, error) {
		if e.entropy.calls.Load() > 1 {
			return 0, commandPortFailure
		}
		close(entered)
		select {
		case <-release:
			return 0, nil
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	a := Admission{Operation: OperationRecord{Operation: OperationIdentity{ID: "operation"}}, Round: r}
	type outcome struct {
		round RoundRecord
		err   error
	}
	first := make(chan outcome, 1)
	go func() { r, err := s.ExecuteAdmission(context.Background(), a); first <- outcome{r, err} }()
	<-entered
	observed, err := s.ExecuteAdmission(context.Background(), a)
	if err != nil || !reflect.DeepEqual(observed, r) {
		t.Fatalf("duplicate observed=%+v err=%v", observed, err)
	}
	if e.entropy.calls.Load() != 1 {
		t.Fatal("duplicate consumed RNG")
	}
	report, err := s.Recover(context.Background(), RecoverRequest{})
	if err != nil || len(report.Failed) != 0 {
		t.Fatalf("live recovery=%+v err=%v", report, err)
	}
	releaseOnce.Do(func() { close(release) })
	done := <-first
	if done.err != nil || done.round.State != contracts.Completed || done.round.Outcome == nil {
		t.Fatalf("owner result=%+v err=%v", done.round, done.err)
	}
	assertCommandCalls(t, p, map[string]int{"round": 4, "recoverable": 1, "commit": 1})
	assertCommandOwnersEmpty(t, s, e, 1, 1)
}

func TestCommandRecoveryReadFailuresPreserveOnlyPreviouslyVerifiedResults(t *testing.T) {
	for _, failureAt := range []int{1, 2, 3} {
		t.Run([]string{"", "first", "Nth", "continuous"}[failureAt], func(t *testing.T) {
			rounds := []RoundRecord{commandCompleted("one"), commandCompleted("two"), commandCompleted("three")}
			var reads int
			p := &commandPorts{recoverable: func(context.Context) ([]RoundRecord, error) { return rounds, nil }, round: func(_ context.Context, id contracts.RoundID) (RoundRecord, error) {
				reads++
				if failureAt == 3 || reads == failureAt {
					return RoundRecord{}, commandPortFailure
				}
				for _, r := range rounds {
					if r.ID == id {
						return r, nil
					}
				}
				return RoundRecord{}, commandPortFailure
			}}
			s, e := newCommandService(t, p)
			report, err := s.Recover(context.Background(), RecoverRequest{})
			requireBoundaryFault(t, err, contracts.StorageUnavailable)
			wantCompleted := []contracts.RoundID{}
			wantReads := 1
			if failureAt == 2 {
				wantCompleted = []contracts.RoundID{"one"}
				wantReads = 2
			}
			if !reflect.DeepEqual(report.Completed, wantCompleted) || len(report.Failed) != 0 || len(report.Due) != 0 {
				t.Fatalf("partial report=%+v", report)
			}
			assertCommandCalls(t, p, map[string]int{"recoverable": 1, "round": wantReads})
			assertCommandOwnersEmpty(t, s, e, 0, 0)
		})
	}
}

func TestCommandRecoveryCompletedCorruptionScheduledNilAndTerminalSkip(t *testing.T) {
	for _, name := range []string{"completed", "nil-outcome", "invalid-outcome", "scheduled-nil", "cancelled", "failed", "pending"} {
		t.Run(name, func(t *testing.T) {
			r := commandCompleted("round")
			code := contracts.ErrorCode("")
			switch name {
			case "nil-outcome":
				r.Outcome = nil
				code = contracts.InvalidState
			case "invalid-outcome":
				r.Outcome.Winners[0].ParticipantID = "unknown"
				code = contracts.InvalidState
			case "scheduled-nil":
				r.State = contracts.Scheduled
				r.ScheduledAt = nil
				code = contracts.InvalidState
			case "cancelled":
				r.State = contracts.Cancelled
			case "failed":
				r.State = contracts.Failed
			case "pending":
				r.State = contracts.PendingSchedule
			}
			p := &commandPorts{recoverable: func(context.Context) ([]RoundRecord, error) { return []RoundRecord{r}, nil }, round: func(context.Context, contracts.RoundID) (RoundRecord, error) { return r, nil }}
			s, e := newCommandService(t, p)
			report, err := s.Recover(context.Background(), RecoverRequest{})
			if code != "" {
				requireBoundaryFault(t, err, code)
			} else if err != nil {
				t.Fatal(err)
			}
			want := []contracts.RoundID{}
			if name == "completed" {
				want = []contracts.RoundID{"round"}
			}
			if !reflect.DeepEqual(report.Completed, want) || len(report.Failed) != 0 || len(report.Due) != 0 {
				t.Fatalf("report=%+v", report)
			}
			assertCommandCalls(t, p, map[string]int{"recoverable": 1, "round": 1})
			assertCommandOwnersEmpty(t, s, e, 0, 0)
		})
	}
}

func TestCommandRecoveryDueRequeryFailureRetainsDueIDWithoutInventingFailure(t *testing.T) {
	r := commandRound()
	r.State = contracts.Scheduled
	due := commandTime()
	r.ScheduledAt = &due
	var reads int
	p := &commandPorts{recoverable: func(context.Context) ([]RoundRecord, error) { return []RoundRecord{r}, nil }, round: func(context.Context, contracts.RoundID) (RoundRecord, error) {
		reads++
		if reads == 2 {
			return RoundRecord{}, commandPortFailure
		}
		return r, nil
	}}
	s, e := newCommandService(t, p)
	report, err := s.Recover(context.Background(), RecoverRequest{})
	requireBoundaryFault(t, err, contracts.StorageUnavailable)
	if !reflect.DeepEqual(report.Due, []contracts.RoundID{"round"}) || len(report.Failed) != 0 || len(report.Completed) != 0 {
		t.Fatalf("partial report=%+v", report)
	}
	assertCommandCalls(t, p, map[string]int{"recoverable": 1, "round": 2})
	assertCommandOwnersEmpty(t, s, e, 0, 0)
}

func TestCommandRecoveryExecutingRequeryActiveAndWriteFencesAreDistinct(t *testing.T) {
	for _, name := range []string{"requery-error", "state-changed", "active-error", "stale-write", "write-error", "failed-commit"} {
		t.Run(name, func(t *testing.T) {
			r := commandRound()
			var reads int
			p := &commandPorts{recoverable: func(context.Context) ([]RoundRecord, error) { return []RoundRecord{r}, nil }, round: func(context.Context, contracts.RoundID) (RoundRecord, error) {
				reads++
				if reads == 2 {
					if name == "requery-error" {
						return RoundRecord{}, commandPortFailure
					}
					if name == "state-changed" {
						changed := r
						changed.State = contracts.Cancelled
						return changed, nil
					}
				}
				return r, nil
			}, active: func(context.Context, contracts.RoundID) (OperationRecord, error) {
				if name == "active-error" {
					return OperationRecord{}, commandPortFailure
				}
				return OperationRecord{Operation: OperationIdentity{ID: "operation"}, Status: contracts.OperationPending, CollectionID: r.CollectionID, RoundID: r.ID}, nil
			}, failure: func(_ context.Context, req AttemptFailureRequest) (RoundRecord, error) {
				if req.OperationID != "operation" || req.Claim != nil || req.ExpectedRevision != r.Revision || req.ExpectedVersion != r.Version || req.Attempt != r.Attempt || req.Code != contracts.InvalidState || !req.FailedAt.Equal(commandTime()) {
					t.Error("recovery lost owner/failure fence")
				}
				switch name {
				case "stale-write":
					return RoundRecord{}, contracts.NewFault(contracts.StaleRevision)
				case "write-error":
					return RoundRecord{}, commandPortFailure
				}
				failed := r
				failed.State = contracts.Failed
				failed.FailureCode = req.Code
				return failed, nil
			}}
			s, e := newCommandService(t, p)
			report, err := s.Recover(context.Background(), RecoverRequest{})
			want := map[string]int{"recoverable": 1, "round": 2}
			notices := int32(0)
			switch name {
			case "requery-error":
				requireBoundaryFault(t, err, contracts.StorageUnavailable)
			case "state-changed":
				if err != nil {
					t.Fatal(err)
				}
			default:
				want["active"] = 1
				if name == "active-error" {
					requireBoundaryFault(t, err, contracts.StorageUnavailable)
				} else {
					want["failure"] = 1
					if name == "write-error" {
						requireBoundaryFault(t, err, contracts.StorageUnavailable)
					} else if err != nil {
						t.Fatal(err)
					}
				}
			}
			wantFailed := []contracts.RoundID{}
			if name == "failed-commit" {
				wantFailed = []contracts.RoundID{"round"}
				notices = 1
			}
			if !reflect.DeepEqual(report.Failed, wantFailed) || len(report.Completed) != 0 || len(report.Due) != 0 {
				t.Fatalf("report=%+v", report)
			}
			assertCommandCalls(t, p, want)
			assertCommandOwnersEmpty(t, s, e, 0, notices)
		})
	}
}

func TestCommandCanceledOrNilReadsDoNotCallStorageOrAcquireRecoveryGate(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, ctx := range []context.Context{nil, canceled} {
		for _, kind := range []string{"due", "recover"} {
			t.Run(kind+func() string {
				if ctx == nil {
					return "/nil"
				}
				return "/canceled"
			}(), func(t *testing.T) {
				p := &commandPorts{}
				s, e := newCommandService(t, p)
				var err error
				switch kind {
				case "due":
					_, err = s.ExecuteDue(ctx, ExecuteDueRequest{RoundID: "round"})
				case "recover":
					_, err = s.Recover(ctx, RecoverRequest{})

				}
				if ctx == nil {
					requireBoundaryFault(t, err, contracts.InvalidInput)
				} else if !errors.Is(err, context.Canceled) {
					t.Fatalf("err=%v", err)
				}
				assertCommandCalls(t, p, map[string]int{})
				assertCommandOwnersEmpty(t, s, e, 0, 0)

			})
		}
	}
	t.Run("due-missing-time", func(t *testing.T) {
		r := commandRound()
		r.State = contracts.Scheduled
		p := &commandPorts{round: func(context.Context, contracts.RoundID) (RoundRecord, error) { return r, nil }}
		s, e := newCommandService(t, p)
		_, err := s.ExecuteDue(context.Background(), ExecuteDueRequest{RoundID: "round"})
		requireBoundaryFault(t, err, contracts.InvalidState)
		assertCommandCalls(t, p, map[string]int{"round": 1})
		assertCommandOwnersEmpty(t, s, e, 0, 0)
	})
	t.Run("schedule-time-marshal-error", func(t *testing.T) {
		p := &commandPorts{}
		s, e := newCommandService(t, p)

		_, err := s.SetSchedule(context.Background(), SetScheduleRequest{OperationID: "operation", Context: commandContext(), ScheduledAt: time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC), Timezone: "Asia/Seoul"})
		requireBoundaryFault(t, err, contracts.InvalidInput)
		assertCommandCalls(t, p, map[string]int{})
		assertCommandOwnersEmpty(t, s, e, 0, 0)
	})
}

func TestCommandRecoveryWaitingOwnerSeesCloseBeforeRequeryAndReleasesGate(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	p := &commandPorts{recoverable: func(context.Context) ([]RoundRecord, error) { close(entered); <-release; return []RoundRecord{}, nil }}
	s, e := newCommandService(t, p)
	first := make(chan error, 1)
	go func() { _, err := s.Recover(context.Background(), RecoverRequest{}); first <- err }()
	<-entered
	waiter := &commandDoneObserved{Context: context.Background(), entered: make(chan struct{})}
	second := make(chan error, 1)
	go func() { _, err := s.Recover(waiter, RecoverRequest{}); second <- err }()
	<-waiter.entered // Done evaluation proves the second owner reached the gate.
	closeBase, cancel := context.WithCancel(context.Background())
	defer cancel()
	closeContext := &commandDoneObserved{Context: closeBase, entered: make(chan struct{})}
	closing := make(chan error, 1)
	go func() { closing <- s.Close(closeContext) }()
	<-closeContext.entered // Close publishes its closed fence before waiting.
	if !s.closed.Load() {
		t.Fatal("close did not publish admission fence")
	}
	cancel()
	if err := <-closing; !errors.Is(err, context.Canceled) {
		t.Fatalf("close waiting error=%v", err)
	}
	releaseOnce.Do(func() { close(release) })
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	requireBoundaryFault(t, <-second, contracts.InvalidState)
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertCommandCalls(t, p, map[string]int{"recoverable": 1})
	assertCommandOwnersEmpty(t, s, e, 0, 0)
	if !errors.Is(s.options.ExecutionContext.Err(), context.Canceled) {
		t.Fatal("execution context survived closed owner")
	}
}

func TestCommandContinuousRecoveryReadFailureNeverRetainsGateOrChangesDurableState(t *testing.T) {
	durable := commandRound()
	p := &commandPorts{recoverable: func(context.Context) ([]RoundRecord, error) { return []RoundRecord{durable}, nil }, round: func(context.Context, contracts.RoundID) (RoundRecord, error) {
		return RoundRecord{}, commandPortFailure
	}}
	s, e := newCommandService(t, p)
	for attempt := 0; attempt < 3; attempt++ {
		report, err := s.Recover(context.Background(), RecoverRequest{})
		requireBoundaryFault(t, err, contracts.StorageUnavailable)
		if len(report.Completed) != 0 || len(report.Failed) != 0 || len(report.Due) != 0 {
			t.Fatal("failed read invented terminal state")
		}
		assertCommandOwnersEmpty(t, s, e, 0, 0)
	}
	assertCommandCalls(t, p, map[string]int{"recoverable": 3, "round": 3})
	if !reflect.DeepEqual(durable, commandRound()) {
		t.Fatal("read-only failure changed persisted observation")
	}
}

func TestCommandClosingPendingEntropyCancelsWithoutComputedOutcomeOrFailureWrite(t *testing.T) {
	r := commandRound()
	claim := commandClaim()
	r.Claim = &claim
	entered := make(chan struct{})
	p := &commandPorts{round: func(context.Context, contracts.RoundID) (RoundRecord, error) { return r, nil }}
	s, e := newCommandService(t, p)
	e.entropy.sample = func(ctx context.Context, _ uint64) (uint64, error) { close(entered); <-ctx.Done(); return 0, ctx.Err() }
	result := make(chan error, 1)
	go func() {
		round, err := s.ExecuteAdmission(context.Background(), Admission{Operation: OperationRecord{Operation: OperationIdentity{ID: "operation"}}, Round: r})
		if !reflect.DeepEqual(round, RoundRecord{}) {
			t.Error("cancel returned computed outcome")
		}
		result <- err
	}()
	<-entered
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	closeContext := &commandDoneObserved{Context: base, entered: make(chan struct{})}
	closing := make(chan error, 1)
	go func() { closing <- s.Close(closeContext) }()
	<-closeContext.entered
	cancel()
	if err := <-closing; !errors.Is(err, context.Canceled) {
		t.Fatalf("close error=%v", err)
	}
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("execute error=%v", err)
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertCommandCalls(t, p, map[string]int{"round": 1})
	assertCommandOwnersEmpty(t, s, e, 1, 0)
}

// Context observers return the real delegated error. They only place a channel
// barrier at a boundary where a normal caller cancellation/Close may interleave.
type commandErrObserved struct {
	context.Context
	calls atomic.Int32
	onErr func(int32)
}

func (c *commandErrObserved) Err() error { n := c.calls.Add(1); c.onErr(n); return c.Context.Err() }
func TestCommandPreparedAdmissionObservesCloseAfterWorkRegistration(t *testing.T) {
	p := &commandPorts{operation: commandUnknown}
	s, e := newCommandService(t, p)
	req := boundaryCreateRequest()
	lease, err := s.PrepareCreate(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	entered := make(chan struct{})
	ctx := &commandErrObserved{Context: context.Background(), onErr: func(int32) { close(entered); <-s.options.ExecutionContext.Done() }}
	result := make(chan error, 1)
	go func() { _, err := s.AdmitCreate(ctx, lease, req.Collection); result <- err }()
	<-entered
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	closeCtx := &commandDoneObserved{Context: base, entered: make(chan struct{})}
	closing := make(chan error, 1)
	go func() { closing <- s.Close(closeCtx) }()
	<-closeCtx.entered
	cancel()
	if err := <-closing; !errors.Is(err, context.Canceled) {
		t.Fatalf("close error=%v", err)
	}
	requireBoundaryFault(t, <-result, contracts.InvalidState)
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertCommandCalls(t, p, map[string]int{"operation": 1, "find": 1})
	assertCommandOwnersEmpty(t, s, e, 0, 0)
	if e.ids.Load() != 0 {
		t.Fatal("closed admission generated IDs or authority")
	}
}
func TestCommandRecoveryCancellationAfterGateAcquisitionReleasesOwner(t *testing.T) {
	p := &commandPorts{}
	s, e := newCommandService(t, p)
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	ctx := &commandErrObserved{Context: base, onErr: func(n int32) {
		if n == 2 {
			close(entered)
			<-s.options.ExecutionContext.Done()
			<-release
		}
	}}
	result := make(chan error, 1)
	go func() { _, err := s.Recover(ctx, RecoverRequest{}); result <- err }()
	<-entered
	if len(s.recoveryGate) != 1 {
		t.Fatal("cancellation boundary preceded acquisition")
	}
	closingBase, closingCancel := context.WithCancel(context.Background())
	defer closingCancel()
	closingCtx := &commandDoneObserved{Context: closingBase, entered: make(chan struct{})}
	closing := make(chan error, 1)
	go func() { closing <- s.Close(closingCtx) }()
	<-closingCtx.entered
	closingCancel()
	if err := <-closing; !errors.Is(err, context.Canceled) {
		t.Fatalf("close error=%v", err)
	}
	cancel()
	releaseOnce.Do(func() { close(release) })
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("recovery error=%v", err)
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertCommandCalls(t, p, map[string]int{})
	assertCommandOwnersEmpty(t, s, e, 0, 0)
}

func TestCommandCanonicalCreateEscapedCommentsOverHundredMiBRefuseBeforeAnyEffect(t *testing.T) {
	request := boundaryCreateRequest()
	// Each modeled memo (&lt; repeated 16Ki times) is <=64KiB, each decoded
	// comment is16KiB, and their aggregate wire/model bytes fit the100MiB job.
	// encoding/json's required HTML escaping expands '<' to six ASCII bytes.
	text := strings.Repeat("<", 16<<10)
	comments := make([]CommentSnapshot, 1100)
	for index := range comments {
		comments[index] = CommentSnapshot{ID: "comment-" + strconv.Itoa(index), Kind: "text", Text: text}
	}
	request.Collection.Participants[0].Comments = comments
	if len(text) > 64<<10 || len(text)*len(comments) > 100<<20 || len(text)*4*len(comments) > 100<<20 {
		t.Fatal("fixture exceeds collector text/job bounds")
	}
	p := &commandPorts{}
	s, e := newCommandService(t, p)
	lease, err := s.PrepareCreate(context.Background(), request)
	requireBoundaryFault(t, err, contracts.InvalidInput)
	if lease != nil {
		lease.Close()
		t.Fatal("oversized canonical bytes acquired lease")
	}
	assertCommandCalls(t, p, map[string]int{})
	assertCommandOwnersEmpty(t, s, e, 0, 0)
	if e.ids.Load() != 0 {
		t.Fatal("bound refusal had effects")
	}
}

func TestCommandTypedPublicOversizedStringsAreRejectedBeforeStorage(t *testing.T) {
	// String aliases and Context.Validate do not impose an ID length upper bound.
	// These direct Go callers are representable despite the actual UUID producer.
	payload := strings.Repeat("x", 100<<20)
	for _, kind := range []string{"rerun", "cancel", "retry", "due"} {
		t.Run(kind, func(t *testing.T) {
			p := &commandPorts{}
			s, e := newCommandService(t, p)

			var err error
			want := map[string]int{}
			switch kind {
			case "rerun":
				_, err = s.Rerun(context.Background(), RerunRequest{OperationID: "operation", Context: commandContext().CollectionContext, Message: payload, Mode: ImmediateMode})
			case "cancel":
				ctx := commandContext()
				ctx.RoundID = contracts.RoundID(payload)
				_, err = s.CancelSchedule(context.Background(), CancelRequest{OperationID: "operation", Context: ctx})
			case "retry":
				ctx := commandContext()
				ctx.RoundID = contracts.RoundID(payload)
				_, err = s.RetryRound(context.Background(), RetryRequest{OperationID: "operation", Context: ctx})
			case "due":
				r := commandRound()
				r.ID = contracts.RoundID(payload)
				r.State = contracts.Scheduled
				due := commandTime()
				r.ScheduledAt = &due
				p.round = func(context.Context, contracts.RoundID) (RoundRecord, error) { return r, nil }
				_, err = s.ExecuteDue(context.Background(), ExecuteDueRequest{RoundID: "request-round"})
				want["round"] = 1
			}
			requireBoundaryFault(t, err, contracts.InvalidInput)
			assertCommandCalls(t, p, want)
			assertCommandOwnersEmpty(t, s, e, 0, 0)
			if e.ids.Load() != 0 {
				t.Fatal("oversized input generated ID")
			}
		})
	}
}
