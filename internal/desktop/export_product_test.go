package desktop

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"github.com/porkyx/jackpot/internal/contracts"
	"github.com/porkyx/jackpot/internal/platform"
	"image"
	"image/color"
	"image/png"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type exportProbe struct {
	status  platform.SaveOutcome
	err     error
	calls   atomic.Int32
	started chan struct{}
	release chan struct{}
	article string
}

func (p *exportProbe) SavePNG(ctx context.Context, v platform.PNGExport) (platform.SaveOutcome, error) {
	p.calls.Add(1)
	if p.started != nil {
		close(p.started)
		select {
		case <-p.release:
		case <-ctx.Done():
			return platform.SaveOutcome{}, ctx.Err()
		}
	}
	return p.status, p.err
}
func (p *exportProbe) CopyText(context.Context, string) error { p.calls.Add(1); return p.err }
func (p *exportProbe) OpenArticle(_ context.Context, url string) error {
	p.calls.Add(1)
	p.article = url
	return p.err
}
func exportNow() time.Time { return time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC) }
func pngRequest(t *testing.T) contracts.PNGRequest {
	t.Helper()
	var buffer bytes.Buffer
	picture := image.NewRGBA(image.Rect(0, 0, 2, 2))
	picture.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err := png.Encode(&buffer, picture); err != nil {
		t.Fatal(err)
	}
	return contracts.PNGRequest{SuggestedFilename: "한글.png", DataBase64: base64.StdEncoding.EncodeToString(buffer.Bytes())}
}
func exportService(t *testing.T, p *exportProbe) *ExportService {
	t.Helper()
	s, e := NewExportService(p, "export-session", exportNow)
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func TestPNGExportPreservesSaveCancelAndFailureWithoutLeakingPath(t *testing.T) {
	for _, c := range []struct {
		name   string
		status platform.SaveOutcome
		err    error
		want   string
		ok     bool
	}{{"saved", platform.SaveOutcome{Status: platform.Saved, Path: filepath.Join(t.TempDir(), "secret.png")}, nil, "saved", true}, {"cancelled", platform.SaveOutcome{Status: platform.SaveCancelled}, nil, "cancelled", true}, {"OS failure", platform.SaveOutcome{}, errors.New("private directory"), "", false}, {"invalid outcome", platform.SaveOutcome{Status: platform.Saved, Path: "relative.png"}, nil, "", false}} {
		t.Run(c.name, func(t *testing.T) {
			p := &exportProbe{status: c.status, err: c.err}
			r, e := exportService(t, p).SavePNG(context.Background(), pngRequest(t))
			if e != nil || r.OK != c.ok || p.calls.Load() != 1 {
				t.Fatalf("%+v/%v", r, e)
			}
			if c.ok && r.Data.Status != c.want {
				t.Fatalf("%+v", r)
			}
			if !c.ok && (r.Code != contracts.StorageUnavailable || r.Data != nil) {
				t.Fatalf("unsafe failure %+v", r)
			}
		})
	}
}
func TestPNGExportRejectsMalformedFilenameAndEncodingBeforeOS(t *testing.T) {
	valid := pngRequest(t)
	for _, name := range []string{"", "../a.png", "a\\b.png", "a:foo.png", "a.png\x00", "a.png\r", "a.gif", strings.Repeat("x", 201) + ".png", string([]byte{255}) + ".png"} {
		t.Run("name"+name, func(t *testing.T) {
			p := &exportProbe{}
			input := valid
			input.SuggestedFilename = name
			r, e := exportService(t, p).SavePNG(context.Background(), input)
			if e != nil || r.OK || r.Code != contracts.InvalidInput || p.calls.Load() != 0 {
				t.Fatalf("%+v/%v", r, e)
			}
		})
	}
	for _, data := range []string{"", "%%%", base64.StdEncoding.EncodeToString([]byte("not PNG")), strings.Repeat("A", base64.StdEncoding.EncodedLen(maxPNGBytes)+1), valid.DataBase64[:len(valid.DataBase64)-4] + "AAAA"} {
		t.Run("encoding", func(t *testing.T) {
			p := &exportProbe{}
			input := valid
			input.DataBase64 = data
			r, e := exportService(t, p).SavePNG(context.Background(), input)
			if e != nil || r.OK || p.calls.Load() != 0 {
				t.Fatalf("%+v/%v", r, e)
			}
		})
	}
}
func TestExportValidationAndCancellationDoNotTouchOS(t *testing.T) {
	var typedNil *exportProbe
	for _, p := range []platform.Exports{nil, typedNil} {
		if s, e := NewExportService(p, "session", exportNow); s != nil || e == nil {
			t.Fatal("nil admitted")
		}
	}
	if _, e := NewExportService(&exportProbe{}, "", exportNow); e == nil {
		t.Fatal("empty session")
	}
	if _, e := NewExportService(&exportProbe{}, "session", nil); e == nil {
		t.Fatal("nil clock")
	}
	p := &exportProbe{}
	s := exportService(t, p)
	for _, ctx := range []context.Context{nil, func() context.Context { c, cancel := context.WithCancel(context.Background()); cancel(); return c }()} {
		if _, e := s.SavePNG(ctx, pngRequest(t)); e == nil {
			t.Fatal("invalid context")
		}
		if _, e := s.CopyText(ctx, contracts.TextExportRequest{Text: "result"}); e == nil {
			t.Fatal("invalid context")
		}
		if _, e := s.OpenArticle(ctx, contracts.ArticleExportRequest{URL: "https://gall.dcinside.com/board/view/?id=tree&no=1"}); e == nil {
			t.Fatal("invalid context")
		}
	}
	if p.calls.Load() != 0 {
		t.Fatal("OS side effects")
	}
}
func TestCopyTextAndArticleValidateAndSanitizeFailures(t *testing.T) {
	p := &exportProbe{}
	s := exportService(t, p)
	for _, text := range []string{"", "a\x00b", string([]byte{255}), strings.Repeat("x", 1024*1024+1)} {
		r, e := s.CopyText(context.Background(), contracts.TextExportRequest{Text: text})
		if e != nil || r.OK {
			t.Fatalf("%+v/%v", r, e)
		}
	}
	for _, url := range []string{"", "file:///secret", "javascript:alert(1)", "https://evil.invalid/board/view/?id=tree&no=1"} {
		r, e := s.OpenArticle(context.Background(), contracts.ArticleExportRequest{URL: url})
		if e != nil || r.OK {
			t.Fatalf("%+v/%v", r, e)
		}
	}
	if p.calls.Load() != 0 {
		t.Fatal("validation invoked OS")
	}
	r, e := s.CopyText(context.Background(), contracts.TextExportRequest{Text: "Jackpot 로컬 결과"})
	if e != nil || !r.OK || r.Data.Status != "copied" {
		t.Fatalf("%+v/%v", r, e)
	}
	r, e = s.OpenArticle(context.Background(), contracts.ArticleExportRequest{URL: "gall.dcinside.com/board/view/?id=tree&no=1"})
	if e != nil || !r.OK || r.Data.Status != "opened" || !strings.HasPrefix(p.article, "https://gall.dcinside.com/") {
		t.Fatalf("%+v/%v/%s", r, e, p.article)
	}
	p.err = errors.New("OS private value")
	r, _ = s.CopyText(context.Background(), contracts.TextExportRequest{Text: "text"})
	if r.OK || r.MessageKey != "StorageUnavailable" {
		t.Fatalf("unsafe %+v", r)
	}
	r, _ = s.OpenArticle(context.Background(), contracts.ArticleExportRequest{URL: p.article})
	if r.OK {
		t.Fatal("open failure accepted")
	}
}
func TestBusyExportRejectsDuplicatesAndReleasesAfterCancellation(t *testing.T) {
	p := &exportProbe{started: make(chan struct{}), release: make(chan struct{})}
	s := exportService(t, p)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, e := s.SavePNG(ctx, pngRequest(t)); done <- e }()
	<-p.started
	r, e := s.SavePNG(context.Background(), pngRequest(t))
	if e != nil || r.OK || p.calls.Load() != 1 {
		t.Fatalf("duplicate %+v/%v", r, e)
	}
	r, e = s.CopyText(context.Background(), contracts.TextExportRequest{Text: "duplicate"})
	if e != nil || r.OK || p.calls.Load() != 1 {
		t.Fatal("copy while save busy")
	}
	cancel()
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	r, e = s.CopyText(context.Background(), contracts.TextExportRequest{Text: "after cancel"})
	if e != nil || !r.OK || p.calls.Load() != 2 {
		t.Fatalf("gate leaked %+v/%v", r, e)
	}
}
func FuzzPNGExportValidation(f *testing.F) {
	f.Add("result.png", "%%%%")
	f.Add("../secret.png", "")
	f.Fuzz(func(t *testing.T, name, data string) {
		if len(name) > 4096 || len(data) > 65536 {
			return
		}
		_, _ = validatePNG(contracts.PNGRequest{SuggestedFilename: name, DataBase64: data})
	})
}
