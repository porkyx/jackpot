package desktop

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
)

func TestScheduledCloseRejectsMissingDependenciesAndInactiveContext(t *testing.T) {
	for _, missing := range []string{"count", "confirm", "both"} {
		t.Run(missing, func(t *testing.T) {
			count := func(context.Context) (uint32, error) { return 0, nil }
			confirm := func(string) bool { return true }
			if missing == "count" || missing == "both" {
				count = nil
			}
			if missing == "confirm" || missing == "both" {
				confirm = nil
			}
			if guard, err := NewScheduledCloseGuard(count, confirm); err == nil || guard != nil {
				t.Fatal("invalid guard accepted")
			}
		})
	}
	var calls atomic.Int32
	guard, err := NewScheduledCloseGuard(func(context.Context) (uint32, error) { calls.Add(1); return 0, nil }, func(string) bool { t.Fatal("inactive context showed dialog"); return false })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, input := range []context.Context{nil, ctx} {
		if guard.Permit(input) {
			t.Fatal("inactive context allowed close")
		}
	}
	if calls.Load() != 0 {
		t.Fatal("inactive context reached storage")
	}
}
func TestScheduledCloseZeroCountUsesFreshReadonlyQueryWithoutDialog(t *testing.T) {
	var calls atomic.Int32
	guard, err := NewScheduledCloseGuard(func(context.Context) (uint32, error) { calls.Add(1); return 0, nil }, func(string) bool { t.Fatal("zero reservations showed dialog"); return false })
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 3; attempt++ {
		if !guard.Permit(context.Background()) {
			t.Fatal("empty reservation close refused")
		}
	}
	if calls.Load() != 3 {
		t.Fatal("close reused stale count")
	}
}
func TestScheduledCloseShowsExactCountAndHonorsBothChoices(t *testing.T) {
	for _, count := range []uint32{1, 2, ^uint32(0)} {
		for _, choice := range []bool{false, true} {
			t.Run(fmt.Sprintf("count%d-choice%v", count, choice), func(t *testing.T) {
				queries, dialogs := 0, 0
				guard, err := NewScheduledCloseGuard(func(context.Context) (uint32, error) { queries++; return count, nil }, func(message string) bool {
					dialogs++
					if !strings.Contains(message, fmt.Sprintf("예정 예약이 %d건 있습니다.", count)) || !strings.Contains(message, "앱을 종료하면 예약은 실행되지 않습니다.") || !strings.Contains(message, "다음 시작 시 만료 예약을 한 번씩 지연 실행합니다.") {
						t.Fatal("close policy omitted count or recovery", message)
					}
					return choice
				})
				if err != nil || guard.Permit(context.Background()) != choice || queries != 1 || dialogs != 1 {
					t.Fatal("close choice/count changed", err, queries, dialogs)
				}
			})
		}
	}
}
func TestScheduledCloseFirstNthContinuousReadFailuresShowUnknownWithoutPrivateError(t *testing.T) {
	for _, mode := range []string{"first", "third", "continuous"} {
		t.Run(mode, func(t *testing.T) {
			queries, dialogs := 0, 0
			guard, err := NewScheduledCloseGuard(func(context.Context) (uint32, error) {
				queries++
				if mode == "continuous" || mode == "first" && queries == 1 || mode == "third" && queries == 3 {
					return 99, errors.New("C:/private/database-secret")
				}
				return 0, nil
			}, func(message string) bool {
				dialogs++
				if !strings.Contains(message, "예정 예약 수를 확인할 수 없습니다.") || strings.Contains(message, "99") || strings.Contains(message, "private") {
					t.Fatal("failed count fabricated number or leaked error", message)
				}
				return false
			})
			if err != nil {
				t.Fatal(err)
			}
			for attempt := 1; attempt <= 4; attempt++ {
				want := mode != "continuous" && !(mode == "first" && attempt == 1) && !(mode == "third" && attempt == 3)
				if guard.Permit(context.Background()) != want {
					t.Fatal("read failure changed close policy", attempt)
				}
			}
			expected := 1
			if mode == "continuous" {
				expected = 4
			}
			if dialogs != expected {
				t.Fatal("read retried or duplicated dialog", dialogs)
			}
		})
	}
}
func TestScheduledCloseUnknownCountStillAllowsExplicitExit(t *testing.T) {
	guard, err := NewScheduledCloseGuard(func(context.Context) (uint32, error) { return 0, context.DeadlineExceeded }, func(string) bool { return true })
	if err != nil || !guard.Permit(context.Background()) {
		t.Fatal("storage failure made application impossible to close", err)
	}
}
func TestScheduledCloseConcurrentEventsCannotDuplicateReadOrDialogAndReleaseAfterCancel(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var queries, dialogs atomic.Int32
	guard, err := NewScheduledCloseGuard(func(context.Context) (uint32, error) { queries.Add(1); return 1, nil }, func(string) bool {
		if dialogs.Add(1) == 1 {
			close(entered)
			<-release
		}
		return false
	})
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan bool, 1)
	go func() { result <- guard.Permit(context.Background()) }()
	<-entered
	duplicate := guard.Permit(context.Background())
	close(release)
	first := <-result
	if first || duplicate || queries.Load() != 1 || dialogs.Load() != 1 {
		t.Fatal("duplicate close confirmation admitted")
	}
	if guard.Permit(context.Background()) || queries.Load() != 2 || dialogs.Load() != 2 {
		t.Fatal("cancel retained close guard")
	}
}

func TestScheduledCloseErrorWithZeroCountIndependentlyRequiresConfirmation(t *testing.T) {
	for _, choice := range []bool{false, true} {
		dialogs := 0
		guard, err := NewScheduledCloseGuard(func(context.Context) (uint32, error) { return 0, context.DeadlineExceeded }, func(message string) bool {
			dialogs++
			if !strings.Contains(message, "예정 예약 수를 확인할 수 없습니다.") || strings.Contains(message, "예정 예약이 0건") {
				t.Fatal("zero-valued failed count was treated as known", message)
			}
			return choice
		})
		if err != nil || guard.Permit(context.Background()) != choice || dialogs != 1 {
			t.Fatal("count zero masked independent read error", err, dialogs)
		}
	}
}
