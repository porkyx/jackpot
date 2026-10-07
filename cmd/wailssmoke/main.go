// wailssmoke runs actual native Windows IPC against an isolated temporary DB.
// It is a verification executable; it is never bound by the product app.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
	"github.com/porkyx/jackpot/internal/desktop"
	"github.com/porkyx/jackpot/internal/singleinstance"
	"github.com/porkyx/jackpot/internal/storage/sqlite"
	"github.com/wailsapp/wails/v3/pkg/application"
)

type Report struct {
	BootstrapReads   int    `json:"bootstrapReads"`
	PendingReads     int    `json:"pendingReads"`
	OperationReads   int    `json:"operationReads"`
	Notices          int    `json:"notices"`
	Session          string `json:"session"`
	Disposed         bool   `json:"disposed"`
	MainMounted      bool   `json:"mainMounted"`
	MainScreens      int    `json:"mainScreens"`
	MainResynced     bool   `json:"mainResynced"`
	MinimumWindow    bool   `json:"minimumWindow"`
	KeyboardVerified bool   `json:"keyboardVerified"`
	MainDisposed     bool   `json:"mainDisposed"`
	Failure          string `json:"failure"`
}
type Probe struct {
	app           *application.App
	report        chan Report
	once          sync.Once
	keyboardReady sync.Once
}

func (probe *Probe) KeyboardReady() error {
	probe.keyboardReady.Do(func() { fmt.Println("JACKPOT_NATIVE_KEYBOARD_READY") })
	return nil
}
func (probe *Probe) RequestNotice() error {
	notice := contracts.StateNotice{BackendSessionID: "native-smoke-session", EntityKind: contracts.DraftEntity, EntityID: "native-smoke-draft", Revision: 1}
	if err := notice.Validate(); err != nil {
		return err
	}
	// Wails returns cancellation, not success, from Emit.
	if probe.app.Event.Emit(contracts.StateChangedEvent, notice) {
		return errors.New("native event was cancelled")
	}
	return nil
}
func (probe *Probe) Report(report Report) { probe.once.Do(func() { probe.report <- report }) }

func main() {
	assets := flag.String("assets", "", "absolute isolated smoke asset directory")
	output := flag.String("out", "", "report path")
	cdpPort := flag.Int("cdp-port", 0, "isolated loopback debugging port; zero disables debugging")
	workspace := flag.String("work-dir", "", "absolute empty caller-owned native-run directory")
	flag.Parse()
	if err := run(*assets, *output, *cdpPort, *workspace); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(assets, output string, cdpPort int, workspace string) (err error) {
	if !filepath.IsAbs(assets) || output == "" || cdpPort < 0 || cdpPort > 65535 || !filepath.IsAbs(workspace) || !strings.HasPrefix(filepath.Base(workspace), "native-run-") {
		return errors.New("assets and report path required")
	}
	entries, err := os.ReadDir(workspace)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return errors.New("native work directory must be empty")
	}
	// The caller removes this directory after process exit, when WebView2 has
	// released its profile. Go owns and closes the SQLite store and OS lease.
	temporary := workspace
	path := filepath.Join(temporary, "smoke.sqlite3")
	lease, err := singleinstance.Acquire(context.Background(), path)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, lease.Close()) }()
	store, err := sqlite.Open(context.Background(), path)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, store.Close()) }()
	if _, err = store.Migrate(context.Background()); err != nil {
		return err
	}
	service, err := desktop.NewService("native-smoke-session", time.Now, store)
	if err != nil {
		return err
	}
	recovery, err := desktop.NewRecoveryService("native-smoke-session", time.Now, store)
	if err != nil {
		return err
	}
	application.RegisterEvent[contracts.StateNotice](contracts.StateChangedEvent)
	probe := &Probe{report: make(chan Report, 1)}
	windows := application.WindowsOptions{WebviewUserDataPath: filepath.Join(temporary, "webview-profile")}
	if cdpPort != 0 {
		windows.AdditionalBrowserArgs = []string{fmt.Sprintf("--remote-debugging-port=%d", cdpPort), "--remote-debugging-address=127.0.0.1"}
	}
	app := application.New(application.Options{Name: "Jackpot Native Verification", Windows: windows, Assets: application.AssetOptions{Handler: application.AssetFileServerFS(os.DirFS(assets))}, Services: []application.Service{application.NewService(service), application.NewService(recovery), application.NewService(probe)}})
	probe.app = app
	url := "/native-smoke.html"
	if cdpPort != 0 {
		url += "?keyboard=1"
	}
	app.Window.NewWithOptions(application.WebviewWindowOptions{Title: "Jackpot Native Verification", Width: 1100, Height: 800, MinWidth: 1024, MinHeight: 720, Hidden: true, URL: url})
	result := make(chan error, 1)
	go func() {
		deadline := 30 * time.Second
		if cdpPort != 0 {
			deadline = 90 * time.Second
		}
		timer := time.NewTimer(deadline)
		defer timer.Stop()
		var failure error
		select {
		case report := <-probe.report:
			data, e := json.MarshalIndent(report, "", "  ")
			failure = e
			if e == nil {
				failure = os.WriteFile(output, append(data, '\n'), 0600)
			}
			if report.Failure != "" || report.BootstrapReads != 2 || report.PendingReads != 1 || report.OperationReads != 1 || report.Notices != 1 || report.Session != "native-smoke-session" || !report.Disposed || !report.MainMounted || report.MainScreens != 3 || !report.MainResynced || !report.MinimumWindow || !report.MainDisposed {
				failure = errors.Join(failure, errors.New("native IPC lifecycle invariant failed"))
			}
			if cdpPort != 0 && !report.KeyboardVerified {
				failure = errors.Join(failure, errors.New("native trusted keyboard invariant failed"))
			}
		case <-timer.C:
			failure = errors.New("native WebView verification timed out")
		}
		result <- failure
		app.Quit()
	}()
	return errors.Join(app.Run(), <-result)
}
