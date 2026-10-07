package main

import (
	"context"
	"crypto/rand"
	"embed"
	"errors"
	"log"
	"os"
	"time"

	draftapp "github.com/porkyx/jackpot/internal/application"
	appclock "github.com/porkyx/jackpot/internal/clock"
	"github.com/porkyx/jackpot/internal/contracts"
	"github.com/porkyx/jackpot/internal/dcinside"
	"github.com/porkyx/jackpot/internal/desktop"
	"github.com/porkyx/jackpot/internal/draw"
	"github.com/porkyx/jackpot/internal/platform"
	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
	"github.com/porkyx/jackpot/internal/scheduler"
	"github.com/porkyx/jackpot/internal/singleinstance"
	"github.com/porkyx/jackpot/internal/storage/sqlite"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() (err error) {
	if err := platform.PreflightWebView2Runtime(); err != nil {
		return err
	}
	path, err := sqlite.UserDataPath(os.UserConfigDir)
	if err != nil {
		return err
	}
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
	if _, err := store.Migrate(context.Background()); err != nil {
		return err
	}
	session := contracts.BackendSessionID(rand.Text())
	appContext, stopApp := context.WithCancel(context.Background())
	defer stopApp()
	transport := platform.NewHTTPTransport()
	defer transport.CloseIdleConnections()
	collector, err := dcinside.NewCollector(transport, appclock.System{})
	if err != nil {
		return err
	}
	var app *application.App
	publish := func(notice contracts.StateNotice) error {
		if err := notice.Validate(); err != nil {
			return err
		}
		if app != nil {
			app.Event.Emit(contracts.StateChangedEvent, notice)
		}
		return nil
	}
	draft, err := draftapp.NewDraftService(draftapp.DraftOptions{Session: session, Now: time.Now, NewID: rand.Text, Collector: draftapp.DCCollectorAdapter{Collector: collector}, Publish: publish})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, draft.Close()) }()
	lifecycle, err := rl.NewService(rl.ServiceOptions{Storage: store, Session: session, Entropy: draw.CryptoEntropy{}, Clock: time.Now, NewID: rand.Text, Publish: func(_ context.Context, notice contracts.StateNotice) error { return publish(notice) }, ExecutionContext: appContext, AppVersion: "0.1.0"})
	if err != nil {
		return err
	}
	defer func() {
		stopApp()
		closeContext, cancelClose := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelClose()
		err = errors.Join(err, lifecycle.Close(closeContext))
	}()
	draftIPC, err := desktop.NewDraftService(draft, session, time.Now)
	if err != nil {
		return err
	}
	roundIPC, err := desktop.NewRoundService(draft, lifecycle, store, session, time.Now)
	if err != nil {
		return err
	}
	service, err := desktop.NewService(session, time.Now, store, desktop.RoundBootstrapSource(roundIPC))
	if err != nil {
		return err
	}
	recovery, err := desktop.NewRecoveryService(session, time.Now, store)
	if err != nil {
		return err
	}
	exportIPC, err := desktop.NewExportService(desktop.NativeExports{App: func() *application.App { return app }}, session, time.Now)
	if err != nil {
		return err
	}
	application.RegisterEvent[contracts.StateNotice](contracts.StateChangedEvent)
	app = application.New(application.Options{
		Name:        "Jackpot",
		Description: "로컬 일반 추첨",
		Assets:      application.AssetOptions{Handler: application.AssetFileServerFS(assets)},
		Services:    []application.Service{application.NewService(service), application.NewService(recovery), application.NewService(draftIPC), application.NewService(roundIPC), application.NewService(exportIPC)},
	})
	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title: "Jackpot", Width: 1100, Height: 800,
		MinWidth: 1024, MinHeight: 720, URL: "/",
	})
	closeGuard, err := desktop.NewScheduledCloseGuard(func(ctx context.Context) (uint32, error) {
		_, total, queryErr := store.ListCollectionPage(ctx, 0, 1, "", "scheduled")
		return total, queryErr
	}, func(message string) bool {
		confirmed := false
		dialog := app.Dialog.Question().SetTitle("Jackpot 종료").SetMessage(message).AttachToWindow(window)
		// The pinned Windows dialog matches callbacks to MessageBox's Yes/No labels.
		dialog.AddButton("Yes").OnClick(func() { confirmed = true })
		dialog.AddButton("No").SetAsDefault().SetAsCancel()
		dialog.Show()
		return confirmed
	})
	if err != nil {
		return err
	}
	removeCloseHook := window.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		closeContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if !closeGuard.Permit(closeContext) {
			event.Cancel()
		}
	})
	defer removeCloseHook()
	wake, signalWake := scheduler.NewWakeSignal()
	removeWakeHook := app.Event.OnApplicationEvent(events.Common.SystemDidWake, func(*application.ApplicationEvent) { signalWake() })
	defer removeWakeHook()
	schedulerDone := make(chan error, 1)
	go func() {
		schedulerDone <- scheduler.RunWithWake(appContext, appclock.System{}, lifecycle, wake, func(error) { log.Print("예약 상태 확인을 완료하지 못했습니다.") })
	}()
	runErr := app.Run()
	stopApp()
	schedulerErr := <-schedulerDone
	if errors.Is(schedulerErr, context.Canceled) {
		schedulerErr = nil
	}
	return errors.Join(runErr, schedulerErr)
}
