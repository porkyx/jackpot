package roundlifecycle

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"

	"github.com/porkyx/jackpot/internal/draw"
)

// Unused methods intentionally remain loud through the embedded nil interface.
// A configuration refusal must never invoke a storage effect.
type boundaryStorage struct {
	Storage
	operation                      func(context.Context, contracts.OperationID) (OperationRecord, error)
	round                          func(context.Context, contracts.RoundID) (RoundRecord, error)
	admit                          func(context.Context, AdmitRoundRequest) (Admission, error)
	operations, rounds, admissions atomic.Int32
}

func (store *boundaryStorage) ReadOperation(ctx context.Context, id contracts.OperationID) (OperationRecord, error) {
	store.operations.Add(1)
	return store.operation(ctx, id)
}
func (store *boundaryStorage) ReadRound(ctx context.Context, id contracts.RoundID) (RoundRecord, error) {
	store.rounds.Add(1)
	return store.round(ctx, id)
}
func (store *boundaryStorage) AdmitRound(ctx context.Context, request AdmitRoundRequest) (Admission, error) {
	store.admissions.Add(1)
	return store.admit(ctx, request)
}

type boundaryEntropy struct{ draw.Entropy }
type boundaryReader struct {
	*boundaryStorage
	LifecycleReader
	reads atomic.Int32
}

func (reader *boundaryReader) ListRecoverableRounds(context.Context) ([]RoundRecord, error) {
	reader.reads.Add(1)
	return []RoundRecord{}, nil
}

type boundaryActiveReader struct {
	*boundaryStorage
	operation OperationRecord
	err       error
	reads     atomic.Int32
}

func (reader *boundaryActiveReader) ReadActiveOperation(context.Context, contracts.RoundID) (OperationRecord, error) {
	reader.reads.Add(1)
	return reader.operation, reader.err
}

func boundaryService(t *testing.T, storage Storage) *Service {
	t.Helper()
	var ids atomic.Uint32
	service, err := NewService(ServiceOptions{Storage: storage, Session: "boundary-session", Clock: func() time.Time { return time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC) }, NewID: func() string {
		if ids.Add(1) == 1 {
			return "collection"
		}
		return "round"
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := service.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return service
}
func requireBoundaryFault(t *testing.T, err error, code contracts.ErrorCode) {
	t.Helper()
	var fault contracts.Fault
	if !errors.As(err, &fault) || fault.Code != code {
		t.Fatalf("fault=%v; want %s", err, code)
	}
}
func boundaryCreateRequest() CreateCollectionRequest {
	return CreateCollectionRequest{OperationID: "create", Context: contracts.DraftContext{BackendSessionID: "boundary-session", DraftID: "draft", Revision: 1, ArticleGeneration: 1}, Collection: FrozenCollection{SourceDraftID: "draft", FinalizedDraftRevision: 1, ArticleGeneration: 1, Snapshot: contracts.SnapshotSummary{SnapshotID: "snapshot", Complete: true}, Participants: []ParticipantSnapshot{{ID: "a", Included: true}, {ID: "b", Included: true}}}, Input: RoundInput{CandidateIDs: []contracts.ParticipantID{"a", "b"}, Prizes: []Prize{{ID: "prize", Name: "name", Count: 1}}, Mode: ReservationMode}}
}
func boundaryPreparingStorage() *boundaryStorage {
	return &boundaryStorage{operation: func(_ context.Context, id contracts.OperationID) (OperationRecord, error) {
		return OperationRecord{Operation: OperationIdentity{ID: id}, Status: contracts.OperationUnknown}, nil
	}, admit: func(_ context.Context, request AdmitRoundRequest) (Admission, error) {
		return Admission{Operation: OperationRecord{Operation: request.Operation, Status: contracts.OperationSucceeded, CollectionID: request.CollectionID, RoundID: request.RoundID, Revision: 1}, Round: RoundRecord{CollectionID: request.CollectionID, ID: request.RoundID, State: request.InitialState, Number: request.Number, Attempt: request.Attempt, Revision: 1, Version: 1, Input: request.Input}}, nil
	}}
}

func TestNilPortDistinguishesEachTypedNilFromEmptyAndNonNilValues(t *testing.T) {
	var pointer *int
	var channel chan int
	var function func()
	var mapping map[string]int
	var slice []int
	cases := []struct {
		name  string
		value any
		nil   bool
	}{{"untyped", nil, true}, {"pointer", pointer, true}, {"channel", channel, true}, {"function", function, true}, {"map", mapping, true}, {"slice", slice, true}, {"non-nil-pointer", new(int), false}, {"non-nil-channel", make(chan int), false}, {"non-nil-function", func() {}, false}, {"empty-map", map[string]int{}, false}, {"empty-slice", []int{}, false}, {"zero", 0, false}, {"empty-string", "", false}, {"struct", struct{}{}, false}}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if actual := nilPort(test.value); actual != test.nil {
				t.Fatalf("nil=%t want=%t", actual, test.nil)
			}
		})
	}
}
func TestNewServiceRejectsMissingStorageOrSessionBeforeAnyEffect(t *testing.T) {
	var absent *boundaryStorage
	for _, test := range []struct {
		name    string
		storage Storage
		session contracts.BackendSessionID
	}{{"nil-storage", nil, "session"}, {"typed-nil-storage", absent, "session"}, {"empty-session", &boundaryStorage{}, ""}} {
		t.Run(test.name, func(t *testing.T) {
			service, err := NewService(ServiceOptions{Storage: test.storage, Session: test.session})
			if service != nil {
				t.Cleanup(func() {
					if closeErr := service.Close(context.Background()); closeErr != nil {
						t.Error(closeErr)
					}
				})
				t.Fatal("invalid service allocated")
			}
			requireBoundaryFault(t, err, contracts.InvalidInput)
		})
	}
}
func TestNewServiceDefaultsTypedNilPortsAndPreservesExplicitPorts(t *testing.T) {
	var missingEntropy *boundaryEntropy
	for _, test := range []struct {
		name string

		entropy draw.Entropy
	}{{"untyped-defaults", nil}, {"typed-defaults", missingEntropy}, {"explicit", &boundaryEntropy{}}} {
		t.Run(test.name, func(t *testing.T) {
			storage := &boundaryStorage{}
			service, err := NewService(ServiceOptions{Storage: storage, Session: "session", Entropy: test.entropy})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := service.Close(context.Background()); err != nil {
					t.Error(err)
				}
			}()
			if service.options.Clock == nil || service.options.NewID == nil || service.options.ExecutionContext == nil || service.options.AppVersion != "0.1.0" {
				t.Fatal("defaults missing")
			}
			if nilPort(service.options.Entropy) {
				t.Fatal("typed nil survived normalization")
			}
			if test.name == "explicit" {
				if service.options.Entropy != test.entropy {
					t.Fatal("explicit dependency replaced")
				}
			} else {

				if _, ok := service.options.Entropy.(draw.CryptoEntropy); !ok {
					t.Fatalf("default entropy=%T", service.options.Entropy)
				}
			}
			if storage.operations.Load()+storage.rounds.Load()+storage.admissions.Load() != 0 {
				t.Fatal("configuration touched storage")
			}
		})
	}
}
func TestRecoverRequiresLifecycleReaderBeforeAnyStorageEffect(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		storage := &boundaryStorage{}
		service := boundaryService(t, storage)
		report, err := service.Recover(context.Background(), RecoverRequest{})
		requireBoundaryFault(t, err, contracts.InvalidState)
		if !reflect.DeepEqual(report, RecoveryReport{}) || storage.operations.Load()+storage.rounds.Load()+storage.admissions.Load() != 0 || len(service.recoveryGate) != 0 {
			t.Fatal("missing capability had an effect or retained gate")
		}
	})
	t.Run("present-empty", func(t *testing.T) {
		storage := &boundaryReader{boundaryStorage: &boundaryStorage{}}
		service := boundaryService(t, storage)
		report, err := service.Recover(context.Background(), RecoverRequest{})
		if err != nil || storage.reads.Load() != 1 || len(service.recoveryGate) != 0 || report.Completed == nil || report.Failed == nil || report.Due == nil || len(report.Completed)+len(report.Failed)+len(report.Due) != 0 {
			t.Fatal("empty reader/gate invariant", report, err)
		}
	})
}
func TestPendingOperationCapabilityAndEachOwnershipOperandRejectIndependently(t *testing.T) {
	round := RoundRecord{CollectionID: "collection", ID: "round"}
	good := OperationRecord{Operation: OperationIdentity{ID: "operation"}, Status: contracts.OperationPending, CollectionID: round.CollectionID, RoundID: round.ID}
	t.Run("missing-capability", func(t *testing.T) {
		storage := &boundaryStorage{}
		id, err := pendingOperationForRound(context.Background(), storage, round)
		requireBoundaryFault(t, err, contracts.InvalidState)
		if id != "" || storage.operations.Load()+storage.rounds.Load()+storage.admissions.Load() != 0 {
			t.Fatal("missing capability touched storage")
		}
	})
	cases := []struct {
		name   string
		mutate func(*OperationRecord)
		err    error
		want   contracts.ErrorCode
	}{{"success", func(*OperationRecord) {}, nil, ""}, {"status", func(operation *OperationRecord) { operation.Status = contracts.OperationSucceeded }, nil, contracts.InvalidState}, {"collection", func(operation *OperationRecord) { operation.CollectionID = "other" }, nil, contracts.InvalidState}, {"round", func(operation *OperationRecord) { operation.RoundID = "other" }, nil, contracts.InvalidState}, {"read-fault", func(*OperationRecord) {}, errors.New("private read error"), contracts.StorageUnavailable}, {"cancellation", func(*OperationRecord) {}, context.Canceled, ""}}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			operation := good
			test.mutate(&operation)
			storage := &boundaryActiveReader{boundaryStorage: &boundaryStorage{}, operation: operation, err: test.err}
			id, err := pendingOperationForRound(context.Background(), storage, round)
			if storage.reads.Load() != 1 {
				t.Fatal("duplicate dependency call")
			}
			if test.name == "success" {
				if err != nil || id != good.Operation.ID {
					t.Fatal(id, err)
				}
			} else {
				if id != "" {
					t.Fatal("failure exposed operation")
				}
				if test.want != "" {
					requireBoundaryFault(t, err, test.want)
				} else if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			}
		})
	}
}
func TestPreparedCreateForeignOwnerRefusalPreservesOriginalOwnerAdmission(t *testing.T) {
	store := boundaryPreparingStorage()

	owner := boundaryService(t, store)
	other := boundaryService(t, boundaryPreparingStorage())
	request := boundaryCreateRequest()
	prepared, err := owner.PrepareCreate(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	rejected, err := other.AdmitCreate(context.Background(), prepared, request.Collection)
	requireBoundaryFault(t, err, contracts.InvalidState)
	if !reflect.DeepEqual(rejected, Admission{}) || prepared.closed || store.operations.Load() != 1 || store.admissions.Load() != 0 {
		t.Fatal("foreign owner consumed prepared input or effects")
	}
	accepted, err := owner.AdmitCreate(context.Background(), prepared, request.Collection)
	if err != nil || accepted.Round.State != contracts.PendingSchedule || accepted.Round.CollectionID != "collection" || store.admissions.Load() != 1 || !prepared.closed || !reflect.DeepEqual(prepared.request, CreateCollectionRequest{}) {
		t.Fatal("original owner did not admit once/release snapshot", accepted, err)
	}
	prepared.Close()
	if store.admissions.Load() != 1 {
		t.Fatal("double close changed admission")
	}
}
func TestPreparedCreateClosedRefusalAllowsNextPreparation(t *testing.T) {
	store := boundaryPreparingStorage()

	service := boundaryService(t, store)
	request := boundaryCreateRequest()
	prepared, err := service.PrepareCreate(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	prepared.Close()
	prepared.Close()
	rejected, err := service.AdmitCreate(context.Background(), prepared, request.Collection)
	requireBoundaryFault(t, err, contracts.InvalidState)
	if !reflect.DeepEqual(rejected, Admission{}) || store.admissions.Load() != 0 || !reflect.DeepEqual(prepared.request, CreateCollectionRequest{}) {
		t.Fatal("closed prepared affected storage or retained snapshot")
	}
	request.OperationID = "next"
	next, err := service.PrepareCreate(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	next.Close()
	if store.operations.Load() != 2 {
		t.Fatal("closed preparation blocked safe retry")
	}
}
func TestPreparedCreateCancelledAdmissionReleasesSnapshotWithoutDurableWrite(t *testing.T) {
	store := boundaryPreparingStorage()
	service := boundaryService(t, store)
	request := boundaryCreateRequest()
	prepared, err := service.PrepareCreate(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rejected, err := service.AdmitCreate(ctx, prepared, request.Collection)
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(rejected, Admission{}) || store.admissions.Load() != 0 || !prepared.closed || !reflect.DeepEqual(prepared.request, CreateCollectionRequest{}) {
		t.Fatal("cancelled admission side effects", rejected, err)
	}
}
func TestNilPreparedCreateRefusesBeforeStorageEffect(t *testing.T) {
	store := &boundaryStorage{}
	service := boundaryService(t, store)
	admission, err := service.AdmitCreate(context.Background(), nil, FrozenCollection{})
	requireBoundaryFault(t, err, contracts.InvalidInput)
	if !reflect.DeepEqual(admission, Admission{}) || store.operations.Load()+store.rounds.Load()+store.admissions.Load() != 0 {
		t.Fatal("nil prepared touched storage")
	}
	var prepared *PreparedCreate
	prepared.Close()
}

func TestAdmissionAcknowledgementReconciliationRequiresEveryIndependentOperand(t *testing.T) {
	lost := errors.New("private lost acknowledgement")
	fingerprint := [32]byte{1}
	request := AdmitRoundRequest{Operation: OperationIdentity{ID: "operation", Kind: "CreateCollection", PublicFingerprint: fingerprint}, CollectionID: "collection", RoundID: "round"}
	good := OperationRecord{Operation: request.Operation, Status: contracts.OperationPending, CollectionID: request.CollectionID, RoundID: request.RoundID, Revision: 1}
	cases := []struct {
		name              string
		change            func(*OperationRecord)
		readErr, roundErr error
		wantRecovered     bool
	}{{"success", func(*OperationRecord) {}, nil, nil, true}, {"read-failure", func(*OperationRecord) {}, errors.New("read failed"), nil, false}, {"unknown", func(operation *OperationRecord) { operation.Status = contracts.OperationUnknown }, nil, nil, false}, {"kind", func(operation *OperationRecord) { operation.Operation.Kind = "Rerun" }, nil, nil, false}, {"fingerprint", func(operation *OperationRecord) { operation.Operation.PublicFingerprint[0] = 2 }, nil, nil, false}, {"collection", func(operation *OperationRecord) { operation.CollectionID = "other" }, nil, nil, false}, {"round", func(operation *OperationRecord) { operation.RoundID = "other" }, nil, nil, false}, {"round-read-failure", func(*OperationRecord) {}, nil, errors.New("round read failed"), false}}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			operation := good
			test.change(&operation)
			round := RoundRecord{CollectionID: request.CollectionID, ID: request.RoundID, State: contracts.Executing}
			store := &boundaryStorage{admit: func(context.Context, AdmitRoundRequest) (Admission, error) { return Admission{}, lost }, operation: func(ctx context.Context, id contracts.OperationID) (OperationRecord, error) {
				if ctx.Err() != nil || id != request.Operation.ID {
					t.Fatal("reconciliation did not use live owning context/ID")
				}
				return operation, test.readErr
			}, round: func(ctx context.Context, id contracts.RoundID) (RoundRecord, error) {
				if ctx.Err() != nil || id != request.RoundID {
					t.Fatal("wrong committed read")
				}
				return round, test.roundErr
			}}
			service := boundaryService(t, store)
			cancelled, cancel := context.WithCancel(context.Background())
			cancel()
			admission, err := service.recordAdmission(cancelled, request)
			if store.admissions.Load() != 1 || store.operations.Load() != 1 {
				t.Fatal("duplicate admission/reconcile reads")
			}
			if test.wantRecovered {
				if err != nil || admission.Round.ID != request.RoundID || !service.admittedRounds[request.RoundID] || store.rounds.Load() != 1 {
					t.Fatal("committed ownership not recovered", admission, err)
				}
			} else {
				if !errors.Is(err, lost) || !reflect.DeepEqual(admission, Admission{}) || len(service.admittedRounds) != 0 {
					t.Fatal("unsafe reconciliation replaced original error", admission, err)
				}
				wantReads := int32(0)
				if test.name == "round-read-failure" {
					wantReads = 1
				}
				if store.rounds.Load() != wantReads {
					t.Fatal("unmatched operation read a round")
				}
			}
		})
	}
}

func TestAdmissionOwnershipFlagRequiresSuccessNonReplayAndExecutingIndependently(t *testing.T) {
	for _, test := range []struct {
		name     string
		state    contracts.RoundState
		replay   bool
		writeErr error
		wantLive bool
	}{{"live", contracts.Executing, false, nil, true}, {"replay", contracts.Executing, true, nil, false}, {"pending", contracts.PendingSchedule, false, nil, false}, {"unreconciled-error", contracts.Executing, false, errors.New("lost"), false}} {
		t.Run(test.name, func(t *testing.T) {
			request := AdmitRoundRequest{Operation: OperationIdentity{ID: "operation"}, RoundID: "round"}
			returned := Admission{Round: RoundRecord{ID: "round", State: test.state}, Replay: test.replay}
			storage := &boundaryStorage{admit: func(context.Context, AdmitRoundRequest) (Admission, error) { return returned, test.writeErr }, operation: func(context.Context, contracts.OperationID) (OperationRecord, error) {
				return OperationRecord{Status: contracts.OperationUnknown}, nil
			}}
			service := boundaryService(t, storage)
			actual, err := service.recordAdmission(context.Background(), request)
			if !reflect.DeepEqual(actual, returned) || !errors.Is(err, test.writeErr) || service.admittedRounds[request.RoundID] != test.wantLive || storage.admissions.Load() != 1 {
				t.Fatal("live ownership flag or result changed", actual, err, service.admittedRounds)
			}
			expectedReads := int32(0)
			if test.writeErr != nil {
				expectedReads = 1
			}
			if storage.operations.Load() != expectedReads {
				t.Fatal("unexpected reconciliation reads")
			}
		})
	}
}
