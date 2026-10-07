package desktop

import (
	"context"
	"errors"
	"fmt"
	"github.com/porkyx/jackpot/internal/contracts"
	"github.com/porkyx/jackpot/internal/platform"
	"strings"
	"sync"
	"testing"
)

type clipboardFaultPort struct {
	mu      sync.Mutex
	calls   int
	fail    func(int) bool
	text    string
	entered chan struct{}
}

func (p *clipboardFaultPort) SavePNG(context.Context, platform.PNGExport) (platform.SaveOutcome, error) {
	panic("clipboard test called file port")
}
func (p *clipboardFaultPort) OpenArticle(context.Context, string) error {
	panic("clipboard test opened browser")
}
func (p *clipboardFaultPort) CopyText(ctx context.Context, text string) error {
	p.mu.Lock()
	p.calls++
	call := p.calls
	blocked := p.entered
	p.mu.Unlock()
	if blocked != nil {
		close(blocked)
		<-ctx.Done()
		return ctx.Err()
	}
	if p.fail(call) {
		return errors.New("private clipboard native handle and secret path")
	}
	p.mu.Lock()
	p.text = text
	p.mu.Unlock()
	return ctx.Err()
}
func TestClipboardFirstNthAndContinuousFaultsPreservePreviousTextAndAllowSafeRetry(t *testing.T) {
	for _, failure := range []string{"first", "third", "continuous"} {
		t.Run(failure, func(t *testing.T) {
			p := &clipboardFaultPort{text: "previous contents", fail: func(call int) bool {
				return failure == "continuous" || failure == "first" && call == 1 || failure == "third" && call == 3
			}}
			service, err := NewExportService(p, "clipboard-fault", exportNow)
			if err != nil {
				t.Fatal(err)
			}
			previous := p.text
			for call := 1; call <= 5; call++ {
				wanted := fmt.Sprintf("한글😀\n회차%d", call)
				response, err := service.CopyText(context.Background(), contracts.TextExportRequest{Text: wanted})
				if err != nil {
					t.Fatal(err)
				}
				if p.fail(call) {
					if response.OK || response.Data != nil || response.Code != contracts.StorageUnavailable || response.MessageKey != "StorageUnavailable" {
						t.Fatal("native failure acknowledged", response)
					}
					if p.text != previous {
						t.Fatal("failed clipboard write changed previous bytes", p.text, previous)
					}
					if strings.Contains(fmt.Sprint(response), "private") {
						t.Fatal("native details exposed")
					}
				} else {
					if !response.OK || response.Data == nil || response.Data.Status != "copied" || p.text != wanted {
						t.Fatal("copy differs", response, p.text)
					}
					previous = wanted
				}
				if p.calls != call {
					t.Fatal("clipboard retried or skipped dependency", p.calls, call)
				}
			}
			p.fail = func(int) bool { return false }
			response, err := service.CopyText(context.Background(), contracts.TextExportRequest{Text: "same snapshot safe retry"})
			if err != nil || !response.OK || p.calls != 6 || p.text != "same snapshot safe retry" {
				t.Fatal("failed copy leaked busy gate", response, err, p.calls, p.text)
			}
		})
	}
}
func TestClipboardCancellationReleasesGateAndDuplicateHasNoNativeEffect(t *testing.T) {
	p := &clipboardFaultPort{text: "existing", entered: make(chan struct{}), fail: func(int) bool { return false }}
	service, err := NewExportService(p, "clipboard-fault", exportNow)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan contracts.ExportResponse, 1)
	go func() {
		response, err := service.CopyText(ctx, contracts.TextExportRequest{Text: "cancelled snapshot"})
		if err != nil {
			t.Error(err)
		}
		done <- response
	}()
	<-p.entered
	duplicate, err := service.CopyText(context.Background(), contracts.TextExportRequest{Text: "duplicate"})
	if err != nil || duplicate.OK || duplicate.Code != contracts.InvalidState {
		t.Fatal("duplicate admitted", duplicate, err)
	}
	cancel()
	cancelled := <-done
	if cancelled.OK || cancelled.Code != contracts.StorageUnavailable || p.calls != 1 || p.text != "existing" {
		t.Fatal("cancelled or duplicate affected clipboard", cancelled, p.calls, p.text)
	}
	p.entered = nil
	response, err := service.CopyText(context.Background(), contracts.TextExportRequest{Text: "new round snapshot"})
	if err != nil || !response.OK || p.calls != 2 || p.text != "new round snapshot" {
		t.Fatal("copy did not release admission after cancel", response, err, p.calls, p.text)
	}
}
