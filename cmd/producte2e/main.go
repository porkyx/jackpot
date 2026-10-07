// producte2e is an isolated Windows verification host for the production UI.
// The application, parser, entropy, SQL, IPC and file writer are real.
// Only network delivery, save selection, clipboard and browser launch are fixtures.
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
)

const fixtureURL = "https://gall.dcinside.com/board/view/?id=producte2e&no=1"

type mutableClock struct{ offset atomic.Int64 }

func (clock *mutableClock) Now() time.Time {
	return time.Now().UTC().Add(time.Duration(clock.offset.Load()) * time.Second)
}
func (clock *mutableClock) NewTimer(duration time.Duration) appclock.Timer {
	return (appclock.System{}).NewTimer(duration)
}
func (clock *mutableClock) advance(seconds int64) error {
	if seconds < -7*86400 || seconds > 7*86400 {
		return errors.New("clock advance out of bounds")
	}
	for {
		before := clock.offset.Load()
		after := before + seconds
		if after < -365*86400 || after > 365*86400 {
			return errors.New("total clock offset out of bounds")
		}
		if clock.offset.CompareAndSwap(before, after) {
			return nil
		}
	}
}

type fixtureTransport struct {
	network      fixtureNetwork
	large        bool
	badges       bool
	commentCount int
	calls        atomic.Uint32
	closed       atomic.Uint32
}
type fixtureBody struct {
	io.Reader
	closed *atomic.Uint32
	once   sync.Once
}

func (body *fixtureBody) Close() error { body.once.Do(func() { body.closed.Add(1) }); return nil }

const fixtureLiteralPayload = `<img src=x onerror=window.__jackpotInjected=true><script>window.__jackpotInjected=true</script> &nbsp;`
const fixtureTitle = "한글 Alpha 로컬 추첨 검증 " + fixtureLiteralPayload
const fixtureAuthorMemo = "작성자 댓글 " + fixtureLiteralPayload

func fixtureArticle() string {
	return `<html><head><link rel="canonical" href="` + fixtureURL + `"></head><body><p class="gallname" data-gallid="producte2e">E2E 테스트 갤러리</p><span class="title_subject">` + html.EscapeString(fixtureTitle) + `</span><div class="gall_writer" data-loc="view" data-nick="작성자" data-uid="writer" data-ip=""><span><img src="https://nstatic.dcinside.com/fix_nik.gif"></span><span class="gall_date" title="2026-10-06 12:00:00"></span></div><input id="e_s_n_o" value="e2e-read-nonce"></body></html>`
}
func fixtureComments() ([]byte, error) {
	people := []struct{ name, user, ip, nick, memo string }{{"작성자", "writer", "", "20", html.EscapeString(fixtureAuthorMemo)}, {"고정 참가자 가", "fixed-a", "", "20", "hello 참가합니다"}, {"고정 참가자 나", "fixed-b", "", "20", "hello 다음 댓글"}, {"익명 참가자", "", "192.0.2.1", "00", "참가합니다"}, {"반고정 참가자", "semi-a", "", "10", "hello 반고정 댓글"}, {"디시콘 참가자", "dccon-a", "", "20", `<img class="written_dccon" src="https://dcimg5.dcinside.com/dccon.php?no=1">`}}
	rows := make([]map[string]any, 0, len(people))
	for index, person := range people {
		rows = append(rows, map[string]any{"no": fmt.Sprint(index + 1), "parent": "1", "name": person.name, "user_id": person.user, "ip": person.ip, "reg_date": "2026.10.06 12:00:00", "nicktype": person.nick, "memo": person.memo, "depth": 0, "c_no": 0, "is_delete": "0", "del_yn": "N"})
	}
	return json.Marshal(map[string]any{"total_cnt": len(rows), "comment_cnt": 0, "comments": rows, "pagination": "<em>1</em>", "allow_reply": 1})
}
func (transport *fixtureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request != nil && request.Body != nil {
		defer request.Body.Close()
	}
	if request == nil || request.URL == nil || request.Context() == nil {
		return nil, errors.New("invalid fixture request")
	}
	if err := request.Context().Err(); err != nil {
		return nil, err
	}
	articleURL, galleryID, articleNo := transport.articleIdentity()
	var raw []byte
	var err error
	switch {
	case request.Method == http.MethodGet && request.URL.String() == articleURL:
		transport.network.begin()
		if transport.large {
			raw = []byte(largeArticle())
		} else {
			raw = []byte(fixtureArticle())
		}
	case request.Method == http.MethodPost && request.URL.String() == "https://gall.dcinside.com/board/comment/":
		if request.Body == nil {
			return nil, errors.New("missing fixture request body")
		}
		body, readErr := io.ReadAll(io.LimitReader(request.Body, (1<<20)+1))
		if readErr != nil {
			return nil, readErr
		}
		if len(body) > 1<<20 {
			return nil, errors.New("fixture request body limit")
		}
		form, parseErr := url.ParseQuery(string(body))
		page, pageErr := strconv.Atoi(form.Get("comment_page"))
		if pageErr != nil || strconv.Itoa(page) != form.Get("comment_page") || page < 1 || page > 20 || !transport.large && page != 1 {
			return nil, errors.New("unexpected fixture page")
		}
		if parseErr != nil || form.Get("id") != galleryID || form.Get("no") != articleNo || form.Get("e_s_n_o") != "e2e-read-nonce" || form.Get("_GALLTYPE_") != "G" || form.Get("memo") != "" || form.Get("name") != "" {
			return nil, errors.New("unexpected fixture comment request")
		}
		if transport.large {
			if page == 2 {
				intercepted, failure := transport.network.page2(request.Context())
				if failure != nil {
					return nil, failure
				}
				if intercepted {
					return transport.failureResponse(request), nil
				}
			}
			count := transport.commentCount
			if count == 0 {
				count = largeCommentCount
			}
			raw, err = localComments(page, count)
		} else if transport.badges {
			raw, err = badgeComments()
		} else {
			raw, err = fixtureComments()
		}
	default:
		return nil, errors.New("fixture host only accepts its documented read-only article")
	}
	if err != nil {
		return nil, err
	}
	transport.calls.Add(1)
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json; charset=utf-8"}}, Body: &fixtureBody{Reader: strings.NewReader(string(raw)), closed: &transport.closed}, Request: request, ContentLength: int64(len(raw))}, nil
}

type fixtureExports struct {
	mu                                           sync.Mutex
	workDir, savePath, copiedText, openedArticle string
	saves, copies, opens                         uint32
}

func (exports *fixtureExports) SavePNG(ctx context.Context, input platform.PNGExport) (platform.SaveOutcome, error) {
	path := filepath.Join(exports.workDir, "result.png")
	if err := platform.WriteFileAtomic(ctx, path, input.Bytes); err != nil {
		return platform.SaveOutcome{}, err
	}
	exports.mu.Lock()
	exports.savePath = path
	exports.saves++
	exports.mu.Unlock()
	return platform.SaveOutcome{Status: platform.Saved, Path: path}, nil
}
func (exports *fixtureExports) CopyText(ctx context.Context, text string) error {
	if ctx == nil {
		return errors.New("nil export context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	exports.mu.Lock()
	exports.copiedText = text
	exports.copies++
	exports.mu.Unlock()
	return nil
}
func (exports *fixtureExports) OpenArticle(ctx context.Context, article string) error {
	if ctx == nil {
		return errors.New("nil export context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	exports.mu.Lock()
	exports.openedArticle = article
	exports.opens++
	exports.mu.Unlock()
	return nil
}

type Counts struct {
	Collections  uint32 `json:"collections"`
	Operations   uint32 `json:"operations"`
	Rounds       uint32 `json:"rounds"`
	Attempts     uint32 `json:"attempts"`
	Results      uint32 `json:"results"`
	Winners      uint32 `json:"winners"`
	Participants uint32 `json:"participants"`
}
type CollectionReport struct {
	CollectionID                        string `json:"collectionId"`
	Revision                            uint64 `json:"revision"`
	RoundID                             string `json:"roundId"`
	Number, Attempt                     uint32
	State                               string  `json:"state"`
	Version                             uint64  `json:"roundVersion"`
	ScheduledAt                         *string `json:"scheduledAt"`
	FailureCode                         string  `json:"failureCode"`
	Participants, Included, WinnerCount uint32
	WinnerIDs                           []string `json:"winnerIds"`
}
type HostReport struct {
	FixtureNetwork                    FixtureNetworkReport `json:"fixtureNetwork"`
	Session                           string               `json:"session"`
	Now                               time.Time            `json:"now"`
	FixtureURL                        string               `json:"fixtureURL"`
	FixtureCalls, FixtureBodiesClosed uint32
	Counts                            Counts                     `json:"counts"`
	Latest                            []contracts.CollectionData `json:"latest"`
	ActiveDraft                       *contracts.DraftSummary    `json:"activeDraft"`
	CopiedText                        string                     `json:"copiedText"`
	OpenedArticle                     string                     `json:"openedArticle"`
	SavePath                          string                     `json:"savePath"`
	Saves, Copies, Opens              uint32
	AssetSHA256                       string            `json:"assetSHA256"`
	BoundaryModes                     map[string]string `json:"boundaryModes"`
}
type Probe struct {
	app         *application.App
	clock       *mutableClock
	session     contracts.BackendSessionID
	lifecycle   *rl.Service
	diagnostics *sql.DB
	roundIPC    *desktop.RoundService
	draft       *draftapp.DraftService
	exports     *fixtureExports
	transport   *fixtureTransport
	assetHash   string
	stopOnce    sync.Once
}

var errReportChanged = errors.New("report snapshot changed")

func (probe *Probe) Report(ctx context.Context) (HostReport, error) {
	for attempt := 0; attempt < 3; attempt++ {
		report, err := probe.reportOnce(ctx)
		if !errors.Is(err, errReportChanged) {
			return report, err
		}
	}
	return HostReport{}, errReportChanged
}
func (probe *Probe) reportOnce(ctx context.Context) (HostReport, error) {
	report := HostReport{FixtureNetwork: probe.transport.network.snapshot(), Session: string(probe.session), Now: probe.clock.Now(), FixtureURL: func() string { value, _, _ := probe.transport.articleIdentity(); return value }(), FixtureCalls: probe.transport.calls.Load(), FixtureBodiesClosed: probe.transport.closed.Load(), AssetSHA256: probe.assetHash, Latest: make([]contracts.CollectionData, 0), BoundaryModes: map[string]string{"draftSelection": "production Go owners", "collectorParser": "production DCInside parser", "network": "static HTTP RoundTripper fixture; no external network", "entropy": "real crypto/rand", "sqlite": "real WAL SQLite in isolated work directory", "ipc": "real Wails native IPC", "ui": "unmodified production frontend dist", "png": "real PNG validation and Windows atomic file replace; fixture save selection", "clipboard": "fixture capture; user clipboard untouched", "browser": "fixture capture; OS browser not opened"}}
	if probe.draft != nil {
		report.ActiveDraft = probe.draft.Summary()
	}
	heads := make([]CollectionReport, 0)
	tx, err := probe.diagnostics.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return HostReport{}, err
	}
	defer tx.Rollback()
	for _, entry := range []struct {
		table string
		count *uint32
	}{{"collections", &report.Counts.Collections}, {"operations", &report.Counts.Operations}, {"rounds", &report.Counts.Rounds}, {"round_attempts", &report.Counts.Attempts}, {"results", &report.Counts.Results}, {"winners", &report.Counts.Winners}, {"participants", &report.Counts.Participants}} {
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM "+entry.table).Scan(entry.count); err != nil {
			return HostReport{}, err
		}
	}
	rows, err := tx.QueryContext(ctx, "SELECT c.id,c.revision,r.id,r.number,r.attempt,r.state,r.version,r.scheduled_at,r.failure_code,(SELECT count(*) FROM participants p WHERE p.collection_id=c.id),(SELECT count(*) FROM participants p WHERE p.collection_id=c.id AND p.included=1),(SELECT count(*) FROM winners w WHERE w.collection_id=c.id) FROM collections c JOIN rounds r ON r.collection_id=c.id AND r.number=(SELECT max(x.number) FROM rounds x WHERE x.collection_id=c.id) ORDER BY c.created_at DESC,c.id DESC LIMIT 100")
	if err != nil {
		return HostReport{}, err
	}
	for rows.Next() {
		var record CollectionReport
		var scheduled sql.NullString
		if err = rows.Scan(&record.CollectionID, &record.Revision, &record.RoundID, &record.Number, &record.Attempt, &record.State, &record.Version, &scheduled, &record.FailureCode, &record.Participants, &record.Included, &record.WinnerCount); err != nil {
			rows.Close()
			return HostReport{}, err
		}
		if scheduled.Valid {
			value := scheduled.String
			record.ScheduledAt = &value
		}
		record.WinnerIDs = make([]string, 0)
		heads = append(heads, record)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return HostReport{}, err
	}
	if err = rows.Close(); err != nil {
		return HostReport{}, err
	}
	for index := range heads {
		winners, err := tx.QueryContext(ctx, "SELECT participant_id FROM winners WHERE collection_id=? ORDER BY participant_id", heads[index].CollectionID)
		if err != nil {
			return HostReport{}, err
		}
		for winners.Next() {
			var id string
			if err = winners.Scan(&id); err != nil {
				winners.Close()
				return HostReport{}, err
			}
			heads[index].WinnerIDs = append(heads[index].WinnerIDs, id)
		}
		if err = winners.Err(); err != nil {
			winners.Close()
			return HostReport{}, err
		}
		if err = winners.Close(); err != nil {
			return HostReport{}, err
		}
	}
	for _, head := range heads {
		response, queryErr := probe.roundIPC.GetCollection(ctx, contracts.CollectionQuery{CollectionID: contracts.CollectionID(head.CollectionID)})
		if queryErr != nil {
			return HostReport{}, queryErr
		}
		if !response.OK || response.Data == nil {
			return HostReport{}, errors.New("collection projection unavailable")
		}
		data := *response.Data
		if data.Revision != contracts.Revision(head.Revision) || len(data.Rounds) == 0 || string(data.Rounds[len(data.Rounds)-1].RoundID) != head.RoundID || uint64(data.Rounds[len(data.Rounds)-1].RoundVersion) != head.Version {
			return HostReport{}, errReportChanged
		}
		report.Latest = append(report.Latest, data)
	}
	if err = tx.Commit(); err != nil {
		return HostReport{}, err
	}
	probe.exports.mu.Lock()
	report.CopiedText = probe.exports.copiedText
	report.OpenedArticle = probe.exports.openedArticle
	report.SavePath = probe.exports.savePath
	report.Saves = probe.exports.saves
	report.Copies = probe.exports.copies
	report.Opens = probe.exports.opens
	probe.exports.mu.Unlock()
	return report, nil
}
func (probe *Probe) Advance(ctx context.Context, seconds int64) (HostReport, error) {
	if err := probe.clock.advance(seconds); err != nil {
		return HostReport{}, err
	}
	if _, err := probe.lifecycle.Recover(ctx, rl.RecoverRequest{}); err != nil {
		return HostReport{}, err
	}
	return probe.Report(ctx)
}
func (probe *Probe) Stop() error { probe.stopOnce.Do(func() { go probe.app.Quit() }); return nil }

type manifest struct {
	Version   uint32 `json:"version"`
	Workspace string `json:"workspace"`
}

func validatePaths(assets, workDir string, cdpPort int) (string, bool, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", false, err
	}
	cwd, err = filepath.EvalSymlinks(cwd)
	if err != nil {
		return "", false, err
	}
	return validatePathsAt(assets, workDir, cdpPort, cwd)
}
func validatePathsAt(assets, workDir string, cdpPort int, cwd string) (string, bool, error) {
	if !filepath.IsAbs(assets) || !filepath.IsAbs(workDir) || cdpPort < 1 || cdpPort > 65535 {
		return "", false, errors.New("absolute assets/work directory and loopback CDP port required")
	}
	workDir = filepath.Clean(workDir)
	if !strings.EqualFold(filepath.Dir(workDir), filepath.Join(cwd, ".task")) || !strings.HasPrefix(filepath.Base(workDir), "product-run-") {
		return "", false, errors.New("work directory must be workspace .task/product-run-*")
	}
	resolved, err := filepath.EvalSymlinks(workDir)
	if err != nil {
		return "", false, err
	}
	if !strings.EqualFold(resolved, workDir) {
		return "", false, errors.New("work directory aliases are forbidden")
	}
	if _, err = os.Stat(filepath.Join(assets, "index.html")); err != nil {
		return "", false, err
	}
	entries, err := os.ReadDir(workDir)
	if err != nil {
		return "", false, err
	}
	if len(entries) == 0 {
		return cwd, false, nil
	}
	marker, err := os.ReadFile(filepath.Join(workDir, "producte2e-manifest.json"))
	if err != nil || len(marker) > 4096 {
		return "", false, errors.New("existing work directory lacks verification sentinel")
	}
	var saved manifest
	if json.Unmarshal(marker, &saved) != nil || saved.Version != 1 || !strings.EqualFold(saved.Workspace, cwd) {
		return "", false, errors.New("work directory sentinel mismatch")
	}
	allowed := map[string]bool{"producte2e-manifest.json": true, "product.sqlite3": true, "product.sqlite3-wal": true, "product.sqlite3-shm": true, ".jackpot.instance.lock": true, "webview-profile": true, "result.png": true}
	for _, entry := range entries {
		isBackup := strings.HasPrefix(entry.Name(), "jackpot-backup-") && strings.HasSuffix(entry.Name(), ".sqlite3") && len(entry.Name()) > len("jackpot-backup-.sqlite3")
		if (!allowed[entry.Name()] && !isBackup) || (entry.IsDir() && entry.Name() != "webview-profile") {
			return "", false, errors.New("unexpected work directory entry")
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return "", false, errors.New("work directory symlink is forbidden")
		}
	}
	return cwd, true, nil
}
func main() {
	assets := flag.String("assets", "", "absolute production frontend/dist directory")
	workDir := flag.String("work-dir", "", "absolute workspace .task/product-run-* directory; first run empty")
	cdpPort := flag.Int("cdp-port", 0, "loopback WebView2 remote debugging port")
	large := flag.Bool("large-fixture", false, "isolated100 participant/200 comment local fixture across2pages")
	badges := flag.Bool("badge-fixture", false, "isolated twelve participants covering six actual badge markers")
	commentCount := flag.Int("fixture-comments", 200, "local fixture comment count: 100 or 200")
	flag.Parse()
	if err := run(*assets, *workDir, *cdpPort, *large, *commentCount, *badges); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(assets, workDir string, cdpPort int, large bool, commentCount int, badges bool) (err error) {
	if large && badges {
		return errors.New("large-fixture and badge-fixture cannot be combined")
	}
	if commentCount != 100 && commentCount != 200 {
		return errors.New("fixture-comments must be 100 or 200")
	}
	workspace, restart, err := validatePaths(assets, workDir, cdpPort)
	if err != nil {
		return err
	}
	path := filepath.Join(workDir, "product.sqlite3")
	lease, err := singleinstance.Acquire(context.Background(), path)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, lease.Close()) }()
	if !restart {
		raw, e := json.Marshal(manifest{Version: 1, Workspace: workspace})
		if e != nil {
			return e
		}
		if e = platform.WriteFileAtomic(context.Background(), filepath.Join(workDir, "producte2e-manifest.json"), raw); e != nil {
			return e
		}
	}
	store, err := sqlite.Open(context.Background(), path)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, store.Close()) }()
	if _, err = store.Migrate(context.Background()); err != nil {
		return err
	}
	uri := (&url.URL{Scheme: "file", Path: "/" + filepath.ToSlash(path)}).String() + "?mode=ro"
	diagnostics, err := sql.Open("sqlite", uri)
	if err != nil {
		return err
	}
	diagnostics.SetMaxOpenConns(1)
	defer func() { err = errors.Join(err, diagnostics.Close()) }()
	clock := new(mutableClock)
	transport := &fixtureTransport{large: large, badges: badges, commentCount: commentCount}
	collector, err := dcinside.NewCollector(transport, clock)
	if err != nil {
		return err
	}
	session := contracts.BackendSessionID(rand.Text())
	appContext, stopApp := context.WithCancel(context.Background())
	defer stopApp()
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
	draft, err := draftapp.NewDraftService(draftapp.DraftOptions{Session: session, Now: clock.Now, NewID: rand.Text, Collector: draftapp.DCCollectorAdapter{Collector: collector}, Publish: publish})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, draft.Close()) }()
	lifecycle, err := rl.NewService(rl.ServiceOptions{Storage: store, Session: session, Entropy: draw.CryptoEntropy{}, Clock: clock.Now, NewID: rand.Text, Publish: func(_ context.Context, notice contracts.StateNotice) error { return publish(notice) }, ExecutionContext: appContext, AppVersion: "0.1.0-product-e2e"})
	if err != nil {
		return err
	}
	defer func() {
		stopApp()
		closeContext, cancelClose := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelClose()
		err = errors.Join(err, lifecycle.Close(closeContext))
	}()
	if _, err = lifecycle.Recover(appContext, rl.RecoverRequest{}); err != nil {
		return err
	}
	draftIPC, err := desktop.NewDraftService(draft, session, clock.Now)
	if err != nil {
		return err
	}
	roundIPC, err := desktop.NewRoundService(draft, lifecycle, store, session, clock.Now)
	if err != nil {
		return err
	}
	base, err := desktop.NewService(session, clock.Now, store, desktop.RoundBootstrapSource(roundIPC))
	if err != nil {
		return err
	}
	recovery, err := desktop.NewRecoveryService(session, clock.Now, store)
	if err != nil {
		return err
	}
	exports := &fixtureExports{workDir: workDir}
	exportIPC, err := desktop.NewExportService(exports, session, clock.Now)
	if err != nil {
		return err
	}
	index, err := os.ReadFile(filepath.Join(assets, "index.html"))
	if err != nil {
		return err
	}
	hash := sha256.Sum256(index)
	probe := &Probe{clock: clock, session: session, lifecycle: lifecycle, diagnostics: diagnostics, roundIPC: roundIPC, draft: draft, exports: exports, transport: transport, assetHash: hex.EncodeToString(hash[:])}
	application.RegisterEvent[contracts.StateNotice](contracts.StateChangedEvent)
	app = application.New(application.Options{Name: "Jackpot Product E2E", Description: "Isolated production UI verification", Windows: application.WindowsOptions{WebviewUserDataPath: filepath.Join(workDir, "webview-profile"), AdditionalBrowserArgs: []string{fmt.Sprintf("--remote-debugging-port=%d", cdpPort), "--remote-debugging-address=127.0.0.1"}}, Assets: application.AssetOptions{Handler: application.AssetFileServerFS(os.DirFS(assets))}, Services: []application.Service{application.NewService(base), application.NewService(recovery), application.NewService(draftIPC), application.NewService(roundIPC), application.NewService(exportIPC), application.NewService(probe)}})
	probe.app = app
	app.Window.NewWithOptions(application.WebviewWindowOptions{Title: "Jackpot Product E2E", Width: 1100, Height: 800, MinWidth: 1024, MinHeight: 720, Hidden: false, URL: "/"})
	schedulerDone := make(chan error, 1)
	go func() {
		schedulerDone <- scheduler.Run(appContext, clock, lifecycle, func(error) { fmt.Fprintln(os.Stderr, "E2E scheduler recovery unavailable") })
	}()
	readyArticleURL, _, _ := transport.articleIdentity()
	fmt.Printf("JACKPOT_PRODUCT_E2E_READY session=%s fixture=%s\n", session, readyArticleURL)
	runErr := app.Run()
	stopApp()
	schedulerErr := <-schedulerDone
	if errors.Is(schedulerErr, context.Canceled) {
		schedulerErr = nil
	}
	return errors.Join(runErr, schedulerErr)
}
