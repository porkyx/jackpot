package dcinside_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/porkyx/jackpot/internal/application"
	appclock "github.com/porkyx/jackpot/internal/clock"
	"github.com/porkyx/jackpot/internal/dcinside"
	"github.com/porkyx/jackpot/internal/selection"
)

type miniRegisteredTransport func(*http.Request) (*http.Response, error)

func (transport miniRegisteredTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

type miniRegisteredBody struct {
	io.Reader
	closed *atomic.Int32
}

func (body *miniRegisteredBody) Close() error { body.closed.Add(1); return nil }

// This tiny fixture preserves observed public field shapes, not user content:
// mini comments with a public login ID may omit IP and have nicktype "00".
func TestMiniPublicLoginIdentityWithoutIPSurvivesCollectorAdapterAndSelection(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("..", "..", "testdata", "dcinside", "mini-registered-page1.json"))
	if err != nil {
		t.Fatal(err)
	}
	original := bytes.Clone(fixture)
	const canonical = "https://gall.dcinside.com/mini/board/view/?id=fixture&no=9008"
	const article = `<html><head><link rel="canonical" href="` + canonical + `"></head><body><p class="gallname" data-gallid="fixture">테스트 미니 갤러리</p><span class="title_subject">합성 제목</span><div class="gall_writer" data-loc="view" data-nick="Participant_1" data-uid="user_1" data-ip=""><img src="fix_nik.gif"><span class="gall_date" title="2026-10-06 12:00:00"></span></div><input id="e_s_n_o" value="synthetic-read-nonce"></body></html>`
	var calls, closed atomic.Int32
	transport := miniRegisteredTransport(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		var body []byte
		switch request.Method {
		case http.MethodGet:
			if request.URL.String() != canonical {
				t.Fatal("page parameter changed canonical article identity")
			}
			body = []byte(article)
		case http.MethodPost:
			if request.URL.String() != "https://gall.dcinside.com/board/comment/" || request.Header.Get("Referer") != canonical {
				t.Fatal("unexpected comment read endpoint")
			}
			raw, readErr := io.ReadAll(request.Body)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if err := request.Body.Close(); err != nil {
				t.Fatal(err)
			}
			form, formErr := url.ParseQuery(string(raw))
			if formErr != nil || form.Get("_GALLTYPE_") != "MI" || form.Get("no") != "9008" || form.Get("comment_page") != "1" || form.Get("e_s_n_o") != "synthetic-read-nonce" || form.Has("memo") || form.Has("password") || form.Has("name") {
				t.Fatal("invalid read-only mini comment form")
			}
			body = fixture
		default:
			t.Fatal("unexpected mutation method")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: &miniRegisteredBody{Reader: bytes.NewReader(body), closed: &closed}}, nil
	})
	collector, err := dcinside.NewCollector(transport, appclock.System{})
	if err != nil {
		t.Fatal(err)
	}
	progress := []application.CollectionProgress{}
	snapshot, err := (application.DCCollectorAdapter{Collector: collector}).Collect(context.Background(), canonical+"&page=1", func(value application.CollectionProgress) { progress = append(progress, value) })
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Article.URL != canonical || snapshot.Article.GalleryKind != "MI" || snapshot.Pages != 1 || len(snapshot.Comments) != 4 || snapshot.Deleted != 0 || snapshot.Unsupported != 0 || len(progress) != 1 || progress[0].Comments != 4 {
		t.Fatal("incomplete mini collection projection")
	}
	for _, comment := range snapshot.Comments {
		if comment.Identifier == "" || comment.ParticipantKind == selection.Anonymous || comment.PostedAt != nil {
			t.Fatal("public login ID was discarded or missing year fabricated")
		}
	}
	if snapshot.Comments[0].ParticipantKind != selection.Fixed || snapshot.Comments[1].ParticipantKind != selection.SemiFixed || snapshot.Comments[2].Kind != selection.Dccon || snapshot.Comments[2].Text != "[디시콘]" || snapshot.Comments[2].ParentID == nil || *snapshot.Comments[2].ParentID != "7002" || len(snapshot.Comments[2].MediaURLs) != 0 {
		t.Fatal("login kind, reply or safe dccon projection changed")
	}
	participants, err := selection.BuildParticipants(snapshot.Comments)
	if err != nil || len(participants) != 3 || len(participants[1].Comments) != 2 {
		t.Fatal("nickname plus public login ID no longer groups repeated replies")
	}
	classified, err := selection.Evaluate(participants, selection.DefaultFilters(), snapshot.Author)
	if err != nil || classified.Included != 2 || classified.Excluded != 1 || classified.Rows[0].Classification.Reason != selection.AuthorReason {
		t.Fatal("mini author matching lost the same public login identity")
	}
	if !bytes.Equal(fixture, original) || calls.Load() != 2 || closed.Load() != 2 || !reflect.DeepEqual(participants[1].Key, selection.ParticipantKey{Nickname: "Participant_2", Identifier: "user_2"}) || strings.Contains(snapshot.Article.URL, "page=") {
		t.Fatal("input mutation, extra request or response-body cleanup failure")
	}
}
