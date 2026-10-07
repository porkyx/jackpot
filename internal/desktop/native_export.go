package desktop

import (
	"context"
	"errors"
	"github.com/porkyx/jackpot/internal/platform"
	"github.com/wailsapp/wails/v3/pkg/application"
	"path/filepath"
	"strings"
)

type NativeExports struct{ App func() *application.App }

func (exports NativeExports) app(ctx context.Context) (*application.App, error) {
	if ctx == nil || exports.App == nil {
		return nil, errors.New("export unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	app := exports.App()
	if app == nil {
		return nil, errors.New("export unavailable")
	}
	return app, nil
}
func (exports NativeExports) SavePNG(ctx context.Context, input platform.PNGExport) (platform.SaveOutcome, error) {
	app, err := exports.app(ctx)
	if err != nil {
		return platform.SaveOutcome{}, err
	}
	path, err := app.Dialog.SaveFile().SetFilename(input.SuggestedFilename).AddFilter("PNG 사진", "*.png").AllowsOtherFileTypes(false).PromptForSingleSelection()
	// Wails v3.0.0-beta.28's Windows CFD cancellation sentinel is internal
	// and is returned unchanged. Match only its exact value; other dialog
	// failures remain failures. The native harness verifies this boundary.
	if isNativeSaveCancelled(path, err) {
		return platform.SaveOutcome{Status: platform.SaveCancelled}, nil
	}
	if err != nil {
		return platform.SaveOutcome{}, err
	}
	if !filepath.IsAbs(path) || !strings.EqualFold(filepath.Ext(path), ".png") {
		return platform.SaveOutcome{}, platform.ErrInvalidSaveOutcome
	}
	if err = platform.WriteFileAtomic(ctx, path, input.Bytes); err != nil {
		return platform.SaveOutcome{}, err
	}
	return platform.SaveOutcome{Status: platform.Saved, Path: path}, nil
}
func (exports NativeExports) CopyText(ctx context.Context, text string) error {
	app, err := exports.app(ctx)
	if err != nil {
		return err
	}
	if !app.Clipboard.SetText(text) {
		return errors.New("clipboard unavailable")
	}
	return nil
}
func (exports NativeExports) OpenArticle(ctx context.Context, url string) error {
	app, err := exports.app(ctx)
	if err != nil {
		return err
	}
	return app.Browser.OpenURL(url)
}

func isNativeSaveCancelled(path string, err error) bool {
	return path == "" && (err == nil || err.Error() == "cancelled by user")
}
