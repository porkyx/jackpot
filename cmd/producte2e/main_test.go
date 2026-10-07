package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	appclock "github.com/porkyx/jackpot/internal/clock"
	"github.com/porkyx/jackpot/internal/dcinside"
	"github.com/porkyx/jackpot/internal/platform"
)

func TestProductE2EFixtureUsesRealCollectorParserAndClosesAllBodies(t *testing.T) {
	transport := new(fixtureTransport)
	collector, err := dcinside.NewCollector(transport, appclock.System{})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := collector.Collect(context.Background(), fixtureURL, nil)
	if err != nil || !snapshot.Complete || snapshot.Pages != 1 || len(snapshot.Comments) != 6 || snapshot.Article.Title != fixtureTitle || snapshot.Article.Author == nil || snapshot.Article.Author.Identifier != "writer" || snapshot.Comments[5].Kind != "dccon" || snapshot.Comments[3].ParticipantKind != "anonymous" || snapshot.Comments[4].ParticipantKind != "semi_fixed" {
		t.Fatal(snapshot, err)
	}
	if transport.calls.Load() != 2 || transport.closed.Load() != 2 {
		t.Fatal("HTTP body leaked", transport.calls.Load(), transport.closed.Load())
	}
}
func TestProductE2EFixtureRejectsUnexpectedNetworkAndCommentWriteShape(t *testing.T) {
	transport := new(fixtureTransport)
	if _, err := transport.RoundTrip(nil); err == nil {
		t.Fatal("nil request")
	}
	if _, err := transport.RoundTrip(&http.Request{}); err == nil {
		t.Fatal("nil URL")
	}
	for _, entry := range []struct{ method, uri, body string }{{"GET", "https://example.com/", ""}, {"POST", fixtureURL, ""}, {"POST", "https://gall.dcinside.com/board/comment/", "id=producte2e&no=1&memo=write"}, {"POST", "https://gall.dcinside.com/board/comment/", "%invalid"}} {
		request, _ := http.NewRequest(entry.method, entry.uri, strings.NewReader(entry.body))
		if _, err := transport.RoundTrip(request); err == nil {
			t.Fatal("unexpected request allowed")
		}
	}
	request, _ := http.NewRequest(http.MethodPost, "https://gall.dcinside.com/board/comment/", nil)
	if _, err := transport.RoundTrip(request); err == nil {
		t.Fatal("nil body")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request, _ = http.NewRequestWithContext(ctx, http.MethodGet, fixtureURL, nil)
	if _, err := transport.RoundTrip(request); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if transport.calls.Load() != 0 || transport.closed.Load() != 0 {
		t.Fatal("rejection generated fixture response")
	}
	counter := &transport.closed
	body := &fixtureBody{Reader: strings.NewReader("x"), closed: counter}
	body.Close()
	body.Close()
	if counter.Load() != 1 {
		t.Fatal("body close not idempotent")
	}
}
func TestProductE2EClockAdvanceIsBoundedAndRaceSafe(t *testing.T) {
	clock := new(mutableClock)
	before := clock.Now()
	if err := clock.advance(60); err != nil {
		t.Fatal(err)
	}
	after := clock.Now()
	if after.Before(before.Add(60*time.Second)) || after.After(before.Add(61*time.Second)) {
		t.Fatal("offset not applied")
	}
	for _, offset := range []int64{-7*86400 - 1, 7*86400 + 1} {
		previous := clock.offset.Load()
		if err := clock.advance(offset); err == nil || clock.offset.Load() != previous {
			t.Fatal("bad offset accepted")
		}
	}
	clock.offset.Store(365 * 86400)
	if err := clock.advance(1); err == nil {
		t.Fatal("total max")
	}
	clock.offset.Store(-365 * 86400)
	if err := clock.advance(-1); err == nil {
		t.Fatal("total min")
	}
	clock.offset.Store(0)
	var group sync.WaitGroup
	for index := 0; index < 16; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for count := 0; count < 50; count++ {
				if err := clock.advance(1); err != nil {
					t.Error(err)
				}
				clock.Now()
			}
		}()
	}
	group.Wait()
	if clock.offset.Load() != 800 {
		t.Fatal("lost clock update")
	}
	timer := clock.NewTimer(time.Hour)
	if timer.C() == nil || !timer.Stop() {
		t.Fatal("native timer")
	}
}
func TestProductE2EPathGuardsPermitOnlyEmptyOrOwnedRestartDirectory(t *testing.T) {
	workspace := t.TempDir()
	assets := filepath.Join(workspace, "frontend", "dist")
	workDir := filepath.Join(workspace, ".task", "product-run-test")
	if err := os.MkdirAll(assets, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assets, "index.html"), []byte("production"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, restart, err := validatePathsAt(assets, workDir, 9222, workspace); err != nil || restart {
		t.Fatal(restart, err)
	}
	for _, entry := range []struct {
		asset, work string
		port        int
	}{{"relative", workDir, 9222}, {assets, "relative", 9222}, {assets, workDir, 0}, {assets, workDir, 65536}, {assets, filepath.Join(workspace, "product-run-outside"), 9222}, {assets, filepath.Join(workspace, ".task", "wrong"), 9222}, {filepath.Join(workspace, "missing"), workDir, 9222}} {
		if _, _, err := validatePathsAt(entry.asset, entry.work, entry.port, workspace); err == nil {
			t.Fatal("path guard accepted", entry)
		}
	}
	if err := os.WriteFile(filepath.Join(workDir, "product.sqlite3"), []byte("owned test bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := validatePathsAt(assets, workDir, 9222, workspace); err == nil {
		t.Fatal("pre-existing directory lacks sentinel")
	}
	marker, _ := json.Marshal(manifest{Version: 1, Workspace: workspace})
	if err := os.WriteFile(filepath.Join(workDir, "producte2e-manifest.json"), marker, 0600); err != nil {
		t.Fatal(err)
	}
	if _, restart, err := validatePathsAt(assets, workDir, 9222, workspace); err != nil || !restart {
		t.Fatal(restart, err)
	}
	for _, name := range []string{"jackpot-backup-1742.sqlite3", "jackpot-backup-with-hyphen.sqlite3"} {
		if err := os.WriteFile(filepath.Join(workDir, name), []byte("owned migration backup"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, restart, err := validatePathsAt(assets, workDir, 9222, workspace); err != nil || !restart {
			t.Fatal("owned migration backup rejected", restart, err)
		}
	}
	for _, name := range []string{"jackpot-backup-.sqlite3", "jackpot-backup-x.sqlite3-other", "other-backup-x.sqlite3"} {
		path := filepath.Join(workDir, name)
		if err := os.WriteFile(path, []byte("unknown"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := validatePathsAt(assets, workDir, 9222, workspace); err == nil {
			t.Fatal("unknown backup accepted", name)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	directory := filepath.Join(workDir, "jackpot-backup-dir.sqlite3")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := validatePathsAt(assets, workDir, 9222, workspace); err == nil {
		t.Fatal("backup directory accepted")
	}
	if err := os.Remove(directory); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "unowned.txt"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := validatePathsAt(assets, workDir, 9222, workspace); err == nil {
		t.Fatal("unknown artifact accepted")
	}
	if err := os.Remove(filepath.Join(workDir, "unowned.txt")); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"invalid", `{"version":2,"workspace":"wrong"}`, strings.Repeat("x", 4097)} {
		if err := os.WriteFile(filepath.Join(workDir, "producte2e-manifest.json"), []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := validatePathsAt(assets, workDir, 9222, workspace); err == nil {
			t.Fatal("bad sentinel accepted")
		}
	}
}
func TestProductE2EPNGPortUsesActualAtomicWriterAndCapturesExternalActions(t *testing.T) {
	work := t.TempDir()
	exports := &fixtureExports{workDir: work}
	var imageBytes bytes.Buffer
	if err := png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 2, 3))); err != nil {
		t.Fatal(err)
	}
	data := imageBytes.Bytes()
	result, err := exports.SavePNG(context.Background(), platform.PNGExport{SuggestedFilename: "ignored.png", Bytes: data})
	if err != nil || result.Status != platform.Saved || result.Path != filepath.Join(work, "result.png") {
		t.Fatal(result, err)
	}
	actual, err := os.ReadFile(result.Path)
	if err != nil || !bytes.Equal(actual, data) {
		t.Fatal("atomic file content", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = exports.SavePNG(ctx, platform.PNGExport{Bytes: []byte("bad")}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	actual, err = os.ReadFile(result.Path)
	if err != nil || !bytes.Equal(actual, data) {
		t.Fatal("cancel changed PNG")
	}
	if err = exports.CopyText(context.Background(), "safe result text"); err != nil {
		t.Fatal(err)
	}
	if err = exports.OpenArticle(context.Background(), fixtureURL); err != nil {
		t.Fatal(err)
	}
	if exports.copiedText != "safe result text" || exports.openedArticle != fixtureURL || exports.saves != 1 || exports.copies != 1 || exports.opens != 1 {
		t.Fatal("capture mismatch")
	}
	if err = exports.CopyText(nil, "x"); err == nil {
		t.Fatal("nil copy")
	}
	if err = exports.OpenArticle(nil, fixtureURL); err == nil {
		t.Fatal("nil browser")
	}
	if err = exports.CopyText(ctx, "x"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err = exports.OpenArticle(ctx, fixtureURL); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(work)
	if err != nil || len(entries) != 1 || entries[0].Name() != "result.png" {
		t.Fatal("staged file leaked", entries, err)
	}
	_, _ = io.Discard.Write(actual)
	_, _ = url.Parse(fixtureURL)
}
