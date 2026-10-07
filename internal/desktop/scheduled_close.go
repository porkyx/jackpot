package desktop

import (
	"context"
	"fmt"
	"sync"

	"github.com/porkyx/jackpot/internal/contracts"
)

// ScheduledCloseGuard owns only a native close confirmation. It never changes
// a reservation or starts execution, and concurrent close events share one dialog.
type ScheduledCloseGuard struct {
	mu      sync.Mutex
	count   func(context.Context) (uint32, error)
	confirm func(string) bool
}

func NewScheduledCloseGuard(count func(context.Context) (uint32, error), confirm func(string) bool) (*ScheduledCloseGuard, error) {
	if count == nil || confirm == nil {
		return nil, contracts.NewFault(contracts.InvalidInput)
	}
	return &ScheduledCloseGuard{count: count, confirm: confirm}, nil
}

func (guard *ScheduledCloseGuard) Permit(ctx context.Context) bool {
	if ctx == nil || ctx.Err() != nil {
		return false
	}
	if !guard.mu.TryLock() {
		return false
	}
	defer guard.mu.Unlock()
	count, err := guard.count(ctx)
	if err == nil && count == 0 {
		return true
	}
	message := "예정 예약 수를 확인할 수 없습니다."
	if err == nil {
		message = fmt.Sprintf("예정 예약이 %d건 있습니다.", count)
	}
	message += "\n앱을 종료하면 예약은 실행되지 않습니다. 다음 시작 시 만료 예약을 한 번씩 지연 실행합니다.\nJackpot을 종료할까요?"
	return guard.confirm(message)
}
