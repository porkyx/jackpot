package platform

import (
	"context"
	"errors"
	"path/filepath"
)

type SaveStatus string

const (
	Saved         SaveStatus = "saved"
	SaveCancelled SaveStatus = "cancelled"
)

type SaveOutcome struct {
	Status SaveStatus
	Path   string
}

var ErrInvalidSaveOutcome = errors.New("invalid OS save outcome")

func (outcome SaveOutcome) Validate() error {
	switch outcome.Status {
	case Saved:
		if !filepath.IsAbs(outcome.Path) {
			return ErrInvalidSaveOutcome
		}
	case SaveCancelled:
		if outcome.Path != "" {
			return ErrInvalidSaveOutcome
		}
	default:
		return ErrInvalidSaveOutcome
	}
	return nil
}

type PNGExport struct {
	SuggestedFilename string
	Bytes             []byte
}

// Wails/filesystem adapters implement this OS boundary in the export ticket.
// The application validates article URLs and PNG format/limits before calling it.
type Exports interface {
	SavePNG(context.Context, PNGExport) (SaveOutcome, error)
	CopyText(context.Context, string) error
	OpenArticle(context.Context, string) error
}
