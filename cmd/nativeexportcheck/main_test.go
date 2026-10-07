//go:build windows

package main

import (
	"bytes"
	"context"
	"errors"
	"github.com/porkyx/jackpot/internal/desktop"
	"github.com/porkyx/jackpot/internal/platform"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type fileExports struct {
	path      string
	calls     int
	failFirst bool
}

func (exports *fileExports) SavePNG(ctx context.Context, input platform.PNGExport) (platform.SaveOutcome, error) {
	exports.calls++
	if exports.failFirst {
		return platform.SaveOutcome{}, errors.New("injected first failure")
	}
	if exports.calls == 1 {
		return platform.SaveOutcome{Status: platform.SaveCancelled}, nil
	}
	if err := platform.WriteFileAtomic(ctx, exports.path, input.Bytes); err != nil {
		return platform.SaveOutcome{}, err
	}
	return platform.SaveOutcome{Status: platform.Saved, Path: exports.path}, nil
}
func (*fileExports) CopyText(context.Context, string) error { return errors.New("forbidden clipboard") }
func (*fileExports) OpenArticle(context.Context, string) error {
	return errors.New("forbidden browser")
}
func TestFixtureIsDeterministicDifferentAndValidPNG(t *testing.T) {
	first, err := fixture(false)
	if err != nil {
		t.Fatal(err)
	}
	again, err := fixture(false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := fixture(true)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, again) || bytes.Equal(first, second) {
		t.Fatal("fixture identity failed")
	}
	for _, data := range [][]byte{first, second} {
		config, err := png.DecodeConfig(bytes.NewReader(data))
		if err != nil || config.Width != 4 || config.Height != 2 {
			t.Fatal("invalid fixture")
		}
		if _, err = png.Decode(bytes.NewReader(data)); err != nil {
			t.Fatal(err)
		}
	}
}
func TestHarnessFileSequenceVerifiesFailedAtomicPreservationAndReleasesHandle(t *testing.T) {
	work := t.TempDir()
	exports := &fileExports{path: filepath.Join(work, "한글 결과 공백.png")}
	service, err := desktop.NewExportService(exports, "native-export-session", func() time.Time { return time.Date(2026, 10, 6, 1, 0, 0, 0, time.UTC) })
	if err != nil {
		t.Fatal(err)
	}
	report, err := verify(work, service)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Cancelled || !report.Saved || !report.FailedReplacePreserved || !report.Replaced || !report.StageFilesClean || report.Width != 4 || report.Height != 2 || len(report.SHA256) != 64 || exports.calls != 4 {
		t.Fatalf("invalid report: %+v", report)
	}
	// The delete-denied handle was closed and no stage survives.
	if err = os.Rename(exports.path, exports.path+".moved"); err != nil {
		t.Fatal(err)
	}
}
func TestHarnessFirstFailureDoesNotCreateDestination(t *testing.T) {
	work := t.TempDir()
	exports := &fileExports{path: filepath.Join(work, "한글 결과 공백.png"), failFirst: true}
	service, err := desktop.NewExportService(exports, "native-export-session", time.Now)
	if err != nil {
		t.Fatal(err)
	}
	report, err := verify(work, service)
	if err == nil || report.Cancelled || exports.calls != 1 {
		t.Fatal("first failure hidden")
	}
	if _, err = os.Stat(exports.path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("first failure created file")
	}
}
func TestPathValidationRejectsRelativeMissingWrongPrefixAndNonempty(t *testing.T) {
	work := filepath.Join(t.TempDir(), "native-run-export-test")
	if err := os.Mkdir(work, 0700); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "report.json")
	if err := checkPaths(work, out); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{"", out}, {"relative", out}, {work, "relative"}, {filepath.Join(t.TempDir(), "other"), out}, {filepath.Join(t.TempDir(), "native-run-export-missing"), out}} {
		if err := checkPaths(pair[0], pair[1]); err == nil {
			t.Fatal("accepted invalid path")
		}
	}
	if err := os.WriteFile(filepath.Join(work, "extra"), []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := checkPaths(work, out); err == nil {
		t.Fatal("accepted nonempty work directory")
	}
}
func TestStageLeakIsDetected(t *testing.T) {
	work := t.TempDir()
	if err := cleanStages(work); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, ".jackpot-export-leak"), []byte("stage"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := cleanStages(work); err == nil {
		t.Fatal("stage leak hidden")
	}
}
