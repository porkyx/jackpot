package platform_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/porkyx/jackpot/internal/platform"
)

func TestSaveOutcomeSavedAndCancelledAreDistinctAndReadOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "한글 결과.png")
	for _, outcome := range []platform.SaveOutcome{{Status: platform.Saved, Path: path}, {Status: platform.SaveCancelled}} {
		before := outcome
		if err := outcome.Validate(); err != nil {
			t.Fatal(err)
		}
		if outcome != before {
			t.Fatal("validation changed outcome")
		}
	}
}
func TestSaveOutcomeRejectsInvalidStatusAndContradictoryPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "result.png")
	for _, outcome := range []platform.SaveOutcome{{}, {Status: "unknown"}, {Status: platform.Saved}, {Status: platform.Saved, Path: "relative.png"}, {Status: platform.SaveCancelled, Path: path}} {
		if err := outcome.Validate(); !errors.Is(err, platform.ErrInvalidSaveOutcome) {
			t.Fatalf("%+v: %v", outcome, err)
		}
	}
}
