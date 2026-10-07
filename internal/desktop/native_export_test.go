package desktop

import (
	"context"
	"errors"
	"fmt"
	"github.com/porkyx/jackpot/internal/platform"
	"github.com/wailsapp/wails/v3/pkg/application"
	"testing"
)

func TestNativeSaveCancellationMatchesOnlyPinnedWailsSentinelAndEmptySelection(t *testing.T) {
	for _, entry := range []struct {
		name, path string
		err        error
		cancelled  bool
	}{
		{"empty-success", "", nil, true},
		{"native-cancel", "", errors.New("cancelled by user"), true},
		{"selected-success", "C:/test.png", nil, false},
		{"selected-error", "C:/test.png", errors.New("cancelled by user"), false},
		{"permission", "", errors.New("permission denied"), false},
		{"similar-message", "", errors.New("cancelled by user (other dialog)"), false},
		{"wrapped-unknown", "", fmt.Errorf("unexpected: %w", errors.New("cancelled by user")), false},
		{"context-cancel", "", context.Canceled, false},
	} {
		t.Run(entry.name, func(t *testing.T) {
			if got := isNativeSaveCancelled(entry.path, entry.err); got != entry.cancelled {
				t.Fatal(got, entry.cancelled)
			}
		})
	}
}
func TestNativeExportsRejectMissingAppAndCancelledContextBeforeAnyOSAccess(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, entry := range []struct {
		ctx context.Context
		app func() *application.App
	}{
		{nil, nil}, {context.Background(), nil}, {context.Background(), func() *application.App { return nil }}, {cancelled, func() *application.App { t.Fatal("cancelled context called app"); return nil }},
	} {
		exports := NativeExports{App: entry.app}
		if _, err := exports.SavePNG(entry.ctx, platform.PNGExport{}); err == nil {
			t.Fatal("invalid save succeeded")
		}
		if err := exports.CopyText(entry.ctx, "text"); err == nil {
			t.Fatal("invalid copy succeeded")
		}
		if err := exports.OpenArticle(entry.ctx, "https://gall.dcinside.com/"); err == nil {
			t.Fatal("invalid open succeeded")
		}
	}
}
