package desktop_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
	"github.com/porkyx/jackpot/internal/desktop"
	"github.com/porkyx/jackpot/internal/storage/sqlite"
)

func pendingStore(t *testing.T) *sqlite.Store {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "data.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return store
}

type pendingReaderFunc func(context.Context, contracts.PendingOperationsRequest) (contracts.PendingOperationsPage, error)

func (read pendingReaderFunc) ListPendingOperations(ctx context.Context, request contracts.PendingOperationsRequest) (contracts.PendingOperationsPage, error) {
	return read(ctx, request)
}
func validNow() time.Time { return time.Date(2026, 10, 6, 5, 0, 0, 0, time.UTC) }

func TestServiceRejectsMissingPendingReader(t *testing.T) {
	service, err := desktop.NewService("session", validNow, nil)
	if service != nil || err == nil {
		t.Fatalf("%v/%v", service, err)
	}
}
func TestPendingServiceReturnsCommittedPageAndSamplesClockOnce(t *testing.T) {
	calls := 0
	reads := 0
	store := pendingStore(t)
	service, err := desktop.NewService("session", func() time.Time { calls++; return validNow() }, pendingReaderFunc(func(ctx context.Context, request contracts.PendingOperationsRequest) (contracts.PendingOperationsPage, error) {
		reads++
		return store.ListPendingOperations(ctx, request)
	}))
	if err != nil {
		t.Fatal(err)
	}
	limit := uint32(64)
	for i := 0; i < 2; i++ {
		response, err := service.ListPendingOperations(context.Background(), contracts.PendingOperationsRequest{Limit: &limit})
		if err != nil || !response.OK || response.Data == nil || response.Data.Operations == nil || response.Data.Cursor != nil {
			t.Fatalf("%+v/%v", response, err)
		}
		if err := response.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 || reads != 2 {
		t.Fatalf("clock=%d reads=%d", calls, reads)
	}
}

func TestServiceIndependentInvalidContextAndHeaderDoNotReachStore(t *testing.T) {
	for _, method := range []string{"bootstrap", "pending"} {
		for _, mode := range []string{"nil", "cancelled", "deadline", "invalid clock"} {
			t.Run(method+"/"+mode, func(t *testing.T) {
				ctx := context.Background()
				now := validNow
				reads := 0
				switch mode {
				case "nil":
					ctx = nil
				case "cancelled":
					var cancel context.CancelFunc
					ctx, cancel = context.WithCancel(ctx)
					cancel()
				case "deadline":
					var cancel context.CancelFunc
					ctx, cancel = context.WithDeadline(ctx, time.Time{})
					defer cancel()
				case "invalid clock":
					now = func() time.Time { return time.Time{} }
				}
				service, err := desktop.NewService("session", now, pendingReaderFunc(func(context.Context, contracts.PendingOperationsRequest) (contracts.PendingOperationsPage, error) {
					reads++
					return contracts.PendingOperationsPage{}, nil
				}))
				if err != nil {
					t.Fatal(err)
				}
				if method == "bootstrap" {
					response, err := service.Bootstrap(ctx)
					if err == nil || response.OK || response.Data != nil {
						t.Fatalf("%+v/%v", response, err)
					}
				} else {
					limit := uint32(64)
					response, err := service.ListPendingOperations(ctx, contracts.PendingOperationsRequest{Limit: &limit})
					if err == nil || response.OK || response.Data != nil {
						t.Fatalf("%+v/%v", response, err)
					}
				}
				if reads != 0 {
					t.Fatal("invalid context reached storage")
				}
			})
		}
	}
}

func TestPendingServiceRejectsInvalidRequestBeforeReading(t *testing.T) {
	reads := 0
	service, err := desktop.NewService("session", validNow, pendingReaderFunc(func(context.Context, contracts.PendingOperationsRequest) (contracts.PendingOperationsPage, error) {
		reads++
		return contracts.PendingOperationsPage{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	response, err := service.ListPendingOperations(context.Background(), contracts.PendingOperationsRequest{})
	if err != nil || response.OK || response.Data != nil || response.Code != contracts.InvalidInput || reads != 0 {
		t.Fatalf("%+v/%v/%d", response, err, reads)
	}
}

func TestServiceFirstNthContinuousStoreFailureIsSecretFreeEnvelope(t *testing.T) {
	for _, method := range []string{"bootstrap", "pending"} {
		for _, failureAt := range []int{1, 2, 0} {
			t.Run(method+"/"+string(rune('0'+failureAt)), func(t *testing.T) {
				reads := 0
				service, err := desktop.NewService("session", validNow, pendingReaderFunc(func(context.Context, contracts.PendingOperationsRequest) (contracts.PendingOperationsPage, error) {
					reads++
					if failureAt == 0 || reads == failureAt {
						return contracts.PendingOperationsPage{}, errors.New("synthetic-secret /private/path password")
					}
					return contracts.PendingOperationsPage{Operations: []contracts.OperationDescriptor{}}, nil
				}))
				if err != nil {
					t.Fatal(err)
				}
				for i := 1; i <= 3; i++ {
					var envelope contracts.Envelope[contracts.PendingOperationsPage]
					var wire []byte
					if method == "pending" {
						limit := uint32(64)
						response, err := service.ListPendingOperations(context.Background(), contracts.PendingOperationsRequest{Limit: &limit})
						if err != nil {
							t.Fatal(err)
						}
						envelope = contracts.Envelope[contracts.PendingOperationsPage](response)
						wire, _ = json.Marshal(response)
					} else {
						response, err := service.Bootstrap(context.Background())
						if err != nil {
							t.Fatal(err)
						}
						envelope.OK = response.OK
						envelope.Code = response.Code
						wire, _ = json.Marshal(response)
					}
					failed := failureAt == 0 || i == failureAt
					if envelope.OK == failed || failed && envelope.Code != contracts.StorageUnavailable {
						t.Fatalf("failure disguised: %s", wire)
					}
					if strings.Contains(string(wire), "synthetic-secret") || strings.Contains(string(wire), "private/path") || strings.Contains(string(wire), "password") {
						t.Fatalf("secret leaked: %s", wire)
					}
				}
			})
		}
	}
}

func TestServiceBadPageAndInvalidFaultCodeAreLoudSafeFailures(t *testing.T) {
	for _, method := range []string{"bootstrap", "pending"} {
		for _, mode := range []string{"nil page", "public fault", "unknown fault"} {
			t.Run(method+"/"+mode, func(t *testing.T) {
				want := contracts.InvalidState
				service, err := desktop.NewService("session", validNow, pendingReaderFunc(func(context.Context, contracts.PendingOperationsRequest) (contracts.PendingOperationsPage, error) {
					switch mode {
					case "public fault":
						want = contracts.StaleRevision
						return contracts.PendingOperationsPage{}, contracts.NewFault(contracts.StaleRevision)
					case "unknown fault":
						want = contracts.ProtocolError
						return contracts.PendingOperationsPage{}, contracts.Fault{Code: "unknown", MessageKey: "synthetic-secret"}
					default:
						return contracts.PendingOperationsPage{}, nil
					}
				}))
				if err != nil {
					t.Fatal(err)
				}
				var code contracts.ErrorCode
				var key string
				var ok bool
				if method == "bootstrap" {
					response, err := service.Bootstrap(context.Background())
					if err != nil {
						t.Fatal(err)
					}
					code = response.Code
					key = response.MessageKey
					ok = response.OK
				} else {
					limit := uint32(64)
					response, err := service.ListPendingOperations(context.Background(), contracts.PendingOperationsRequest{Limit: &limit})
					if err != nil {
						t.Fatal(err)
					}
					code = response.Code
					key = response.MessageKey
					ok = response.OK
				}
				if ok || code != want || key != string(want) {
					t.Fatalf("invalid fault: %t/%s/%s", ok, code, key)
				}
			})
		}
	}
}

func TestServiceRejectsMissingSession(t *testing.T) {
	service, err := desktop.NewService("", time.Now, nil)
	if err == nil || service != nil {
		t.Fatal(service, err)
	}
}
func TestServiceRejectsMissingClock(t *testing.T) {
	service, err := desktop.NewService("session", nil, nil)
	if err == nil || service != nil {
		t.Fatal(service, err)
	}
}
func TestBootstrapReturnsStableExplicitEmptyStateAndOneClockSample(t *testing.T) {
	calls := 0
	now := time.Date(2026, 10, 6, 5, 0, 0, 0, time.UTC)
	service, err := desktop.NewService("session", func() time.Time { calls++; return now }, pendingStore(t))
	if err != nil {
		t.Fatal(err)
	}
	var before string
	for i := 0; i < 2; i++ {
		result, err := service.Bootstrap(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !result.OK || result.Data == nil || result.BackendSessionID != "session" || result.Data.BackendNow != result.OccurredAt {
			t.Fatal("inconsistent bootstrap")
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			before = string(encoded)
		} else if before != string(encoded) {
			t.Fatal("bootstrap changed state")
		}
	}
	if calls != 2 {
		t.Fatal("mixed time samples", calls)
	}
}
func TestBootstrapRejectsFirstNthAndContinuousInvalidClockSamples(t *testing.T) {
	for _, failureAt := range []int{1, 2, 0} {
		t.Run(string(rune('0'+failureAt)), func(t *testing.T) {
			calls := 0
			service, err := desktop.NewService("session", func() time.Time {
				calls++
				if failureAt == 0 || calls == failureAt {
					return time.Time{}
				}
				return time.Date(2026, 10, 6, 5, 0, 0, 0, time.UTC)
			}, pendingStore(t))
			if err != nil {
				t.Fatal(err)
			}
			for call := 1; call <= 3; call++ {
				result, err := service.Bootstrap(context.Background())
				wantFailure := failureAt == 0 || call == failureAt
				if (err != nil) != wantFailure {
					t.Fatal("incorrect failure outcome")
				}
				if wantFailure && (result.Data != nil || result.OK) {
					t.Fatal("partial success published")
				}
			}
		})
	}
}
