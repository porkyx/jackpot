//go:build windows

// nativeexportcheck verifies actual Wails SaveFile dialogs against isolated files.
// It never opens an application DB or modifies clipboard/browser state.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing/fstest"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
	"github.com/porkyx/jackpot/internal/desktop"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"golang.org/x/sys/windows"
)

type Report struct {
	Cancelled              bool   `json:"cancelled"`
	Saved                  bool   `json:"saved"`
	FailedReplacePreserved bool   `json:"failedReplacePreserved"`
	Replaced               bool   `json:"replaced"`
	StageFilesClean        bool   `json:"stageFilesClean"`
	Width                  int    `json:"width"`
	Height                 int    `json:"height"`
	SHA256                 string `json:"sha256"`
	Failure                string `json:"failure"`
}

func main() {
	work := flag.String("work-dir", "", "existing empty caller-owned native-run-export directory")
	out := flag.String("out", "", "absolute report path")
	flag.Parse()
	if err := run(*work, *out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func checkPaths(work, out string) error {
	if !filepath.IsAbs(work) || !filepath.IsAbs(out) || !strings.HasPrefix(filepath.Base(work), "native-run-export-") {
		return errors.New("invalid isolated export paths")
	}
	entries, err := os.ReadDir(work)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return errors.New("export work directory must be empty")
	}
	return nil
}
func fixture(alternate bool) ([]byte, error) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 2))
	ink := color.RGBA{R: 10, G: 90, B: 180, A: 255}
	if alternate {
		ink = color.RGBA{R: 190, G: 20, B: 50, A: 255}
	}
	for y := 0; y < 2; y++ {
		for x := 0; x < 4; x++ {
			img.SetRGBA(x, y, ink)
		}
	}
	var buffer bytes.Buffer
	err := png.Encode(&buffer, img)
	return buffer.Bytes(), err
}
func cleanStages(work string) error {
	matches, err := filepath.Glob(filepath.Join(work, ".jackpot-export-*"))
	if err != nil {
		return err
	}
	if len(matches) != 0 {
		return errors.New("atomic export stage leaked")
	}
	return nil
}
func verify(work string, service *desktop.ExportService) (report Report, err error) {
	first, err := fixture(false)
	if err != nil {
		return report, err
	}
	second, err := fixture(true)
	if err != nil {
		return report, err
	}
	path := filepath.Join(work, "한글 결과 공백.png")
	call := func(stage string, data []byte) (contracts.ExportResponse, error) {
		if err := os.WriteFile(filepath.Join(work, "phase"), []byte(stage), 0600); err != nil {
			return contracts.ExportResponse{}, err
		}
		return service.SavePNG(context.Background(), contracts.PNGRequest{SuggestedFilename: "검증 결과.png", DataBase64: base64.StdEncoding.EncodeToString(data)})
	}
	cancelled, err := call("cancel", first)
	if err != nil {
		return report, err
	}
	if !cancelled.OK || cancelled.Data == nil || cancelled.Data.Status != "cancelled" {
		return report, errors.New("actual dialog cancellation failed")
	}
	if _, err = os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		return report, errors.New("cancel created a destination")
	}
	report.Cancelled = true
	saved, err := call("save", first)
	if err != nil {
		return report, err
	}
	if !saved.OK || saved.Data == nil || saved.Data.Status != "saved" {
		return report, errors.New("actual dialog save failed")
	}
	stored, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(stored, first) {
		return report, errors.New("saved PNG bytes changed")
	}
	report.Saved = true
	if err = cleanStages(work); err != nil {
		return report, err
	}
	target, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return report, err
	}
	handle, err := windows.CreateFile(target, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return report, err
	}
	failed, callErr := call("replace-blocked", second)
	closeErr := windows.CloseHandle(handle)
	if callErr != nil || closeErr != nil {
		return report, errors.Join(callErr, closeErr)
	}
	if failed.OK {
		return report, errors.New("delete-denied atomic replace unexpectedly succeeded")
	}
	stored, err = os.ReadFile(path)
	if err != nil || !bytes.Equal(stored, first) {
		return report, errors.New("failed replacement lost prior PNG")
	}
	if err = cleanStages(work); err != nil {
		return report, err
	}
	report.FailedReplacePreserved = true
	replaced, err := call("replace", second)
	if err != nil {
		return report, err
	}
	if !replaced.OK || replaced.Data == nil || replaced.Data.Status != "saved" {
		return report, errors.New("actual confirmed overwrite failed")
	}
	stored, err = os.ReadFile(path)
	if err != nil || !bytes.Equal(stored, second) {
		return report, errors.New("replacement PNG differs")
	}
	config, err := png.DecodeConfig(bytes.NewReader(stored))
	if err != nil {
		return report, err
	}
	if _, err = png.Decode(bytes.NewReader(stored)); err != nil {
		return report, err
	}
	if err = cleanStages(work); err != nil {
		return report, err
	}
	hash := sha256.Sum256(stored)
	report.Width = config.Width
	report.Height = config.Height
	report.SHA256 = hex.EncodeToString(hash[:])
	report.Replaced = true
	report.StageFilesClean = true
	return report, nil
}
func run(work, out string) error {
	if err := checkPaths(work, out); err != nil {
		return err
	}
	assets := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<!doctype html><html lang=ko><meta charset=utf-8><title>Isolated export verification</title><body><h1>Jackpot native export verification</h1><p>This isolated process checks SaveFileDialog; no user database or clipboard is accessed.</p></body></html>")}}
	app := application.New(application.Options{Name: "Jackpot Export Verification", Windows: application.WindowsOptions{WebviewUserDataPath: filepath.Join(work, "webview-profile")}, Assets: application.AssetOptions{Handler: application.AssetFileServerFS(assets)}})
	service, err := desktop.NewExportService(desktop.NativeExports{App: func() *application.App { return app }}, "native-export-session", time.Now)
	if err != nil {
		return err
	}
	window := app.Window.NewWithOptions(application.WebviewWindowOptions{Title: "Jackpot Native Export Verification", Width: 640, Height: 400, URL: "/index.html"})
	done := make(chan error, 1)
	var once sync.Once
	start := func() {
		once.Do(func() {
			go func() {
				report, failure := verify(work, service)
				if failure != nil {
					report.Failure = failure.Error()
				}
				encoded, e := json.MarshalIndent(report, "", "  ")
				if e == nil {
					e = os.WriteFile(out, append(encoded, 10), 0600)
				}
				done <- errors.Join(failure, e)
				app.Quit()
			}()
		})
	}
	window.OnWindowEvent(events.Windows.WebViewNavigationCompleted, func(*application.WindowEvent) { start() })
	go func() {
		timer := time.NewTimer(90 * time.Second)
		defer timer.Stop()
		select {
		case <-timer.C:
			select {
			case done <- errors.New("native export verification timed out"):
			default:
			}
			app.Quit()
		case <-app.Context().Done():
		}
	}()
	err = app.Run()
	select {
	case result := <-done:
		return errors.Join(err, result)
	default:
		return errors.Join(err, errors.New("native export interrupted before result"))
	}
}
