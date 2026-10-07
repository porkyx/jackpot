package dcinside

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type blockingBody struct {
	entered  chan struct{}
	closed   chan struct{}
	once     sync.Once
	readOnce sync.Once
	closes   atomic.Int32
}

func (b *blockingBody) Read([]byte) (int, error) {
	b.readOnce.Do(func() { close(b.entered) })
	<-b.closed
	return 0, io.ErrClosedPipe
}
func (b *blockingBody) Close() error {
	b.closes.Add(1)
	b.once.Do(func() { close(b.closed) })
	return nil
}
func (c *testClock) fire(t *testing.T, d time.Duration) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, value := range c.durations {
		if value == d {
			c.timers[i].ch <- c.Now()
			return
		}
	}
	t.Fatalf("timer %v missing", d)
}

func TestCancellationAndBothControlledDeadlinesCloseBlockedBodyAndTimers(t *testing.T) {
	for _, mode := range []string{"cancel", "request timeout", "collection timeout"} {
		t.Run(mode, func(t *testing.T) {
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			body := &blockingBody{entered: make(chan struct{}), closed: make(chan struct{})}
			c, clock := collectorFor(t, func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: body}, nil
			})
			done := make(chan error, 1)
			go func() {
				snapshot, err := c.Collect(parent, "gall.dcinside.com/test/1", nil)
				if !reflect.DeepEqual(snapshot, Snapshot{}) {
					done <- errors.New("partial snapshot")
					return
				}
				done <- err
			}()
			<-body.entered
			switch mode {
			case "cancel":
				cancel()
			case "request timeout":
				clock.fire(t, 15*time.Second)
			case "collection timeout":
				clock.fire(t, 5*time.Minute)
			}
			err := <-done
			want := "CollectionCancelled"
			cause := context.Canceled
			if mode != "cancel" {
				want = "CollectionTimeout"
				cause = context.DeadlineExceeded
			}
			code(t, err, want)
			if !errors.Is(err, cause) {
				t.Fatal(err)
			}
			if body.closes.Load() != 1 {
				t.Fatal(body.closes.Load())
			}
			clock.assertStopped(t)
		})
	}
}
func TestCancellationDuringTransportWaitBackoffAndProgressStopsSubsequentRequests(t *testing.T) {
	for _, mode := range []string{"transport", "backoff", "progress"} {
		t.Run(mode, func(t *testing.T) {
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			clock := &testClock{manual: true, created: make(chan time.Duration, 10)}
			entered := make(chan struct{})
			calls := atomic.Int32{}
			c, err := NewCollector(transportFunc(func(request *http.Request) (*http.Response, error) {
				n := calls.Add(1)
				switch mode {
				case "transport":
					close(entered)
					<-request.Context().Done()
					return nil, request.Context().Err()
				case "backoff":
					return &http.Response{StatusCode: 503, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}, nil
				default:
					raw := articleHTML(General, "test", "1")
					if n == 2 {
						raw = string(wireJSON(2, 1, 2, oneRow("1", "1")))
					}
					return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(raw))}, nil
				}
			}), clock)
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				_, err := c.Collect(parent, "gall.dcinside.com/test/1", func(Progress) { cancel() })
				done <- err
			}()
			switch mode {
			case "transport":
				<-entered
				cancel()
			case "backoff":
				for duration := range clock.created {
					if duration == 250*time.Millisecond {
						cancel()
						break
					}
				}
			}
			err = <-done
			code(t, err, "CollectionCancelled")
			want := int32(1)
			if mode == "progress" {
				want = 2
			}
			if calls.Load() != want {
				t.Fatal(calls.Load())
			}
			clock.assertStopped(t)
		})
	}
}
func TestInvalidDependenciesAndPreCancelledContextNeverCallTransport(t *testing.T) {
	var typedNil *http.Transport
	for _, transport := range []http.RoundTripper{nil, typedNil} {
		if _, err := NewCollector(transport, &testClock{}); err == nil {
			t.Fatal("nil transport accepted")
		}
	}
	if _, err := NewCollector(transportFunc(func(*http.Request) (*http.Response, error) { panic("called") }), nil); err == nil {
		t.Fatal("nil clock")
	}
	c, clock := collectorFor(t, func(*http.Request) (*http.Response, error) { panic("invalid admission called transport") })
	if _, err := c.Collect(nil, "gall.dcinside.com/test/1", nil); err == nil {
		t.Fatal("nil context accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.Collect(ctx, "gall.dcinside.com/test/1", nil)
	code(t, err, "CollectionCancelled")
	_, err = c.Collect(context.Background(), "http://localhost/test/1", nil)
	code(t, err, "InvalidArticleURL")
	if len(clock.timers) != 0 {
		t.Fatal("invalid admission allocated timer")
	}
	for _, mode := range []int{1, 2} {
		clock := &testClock{invalid: mode}
		c, _ := NewCollector(transportFunc(func(*http.Request) (*http.Response, error) { panic("invalid timer called transport") }), clock)
		if _, err := c.Collect(context.Background(), "gall.dcinside.com/test/1", nil); err == nil {
			t.Fatal("invalid timer accepted")
		}
		clock.assertStopped(t)
	}
}
func TestTransportMalformedResponseAndFailureAlwaysClosesAcquiredBody(t *testing.T) {
	for _, mode := range []string{"nil response", "nil body", "response and error", "read failure", "invalid gzip", "encoding", "missing location", "http4xx", "zero status"} {
		t.Run(mode, func(t *testing.T) {
			body := &trackedBody{Reader: strings.NewReader("body")}
			calls := 0
			c, clock := collectorFor(t, func(*http.Request) (*http.Response, error) {
				calls++
				header := http.Header{}
				response := &http.Response{StatusCode: 200, Header: header, Body: body}
				switch mode {
				case "nil response":
					return nil, nil
				case "nil body":
					response.Body = nil
				case "response and error":
					return response, errors.New("dependency failure")
				case "read failure":
					body.Reader = errorReader{}
				case "invalid gzip":
					header.Set("Content-Encoding", "gzip")
				case "encoding":
					header.Set("Content-Encoding", "br")
				case "missing location":
					response.StatusCode = 302
				case "http4xx":
					response.StatusCode = 422
				case "zero status":
					response.StatusCode = 0
				}
				return response, nil
			})
			_, err := c.Collect(context.Background(), "gall.dcinside.com/test/1", nil)
			if err == nil {
				t.Fatal("invalid response succeeded")
			}
			wantCalls := 1
			if mode == "response and error" || mode == "read failure" {
				wantCalls = 3
			}
			if calls != wantCalls {
				t.Fatal(calls)
			}
			wantClosed := wantCalls
			if mode == "nil response" || mode == "nil body" {
				wantClosed = 0
			}
			if int(body.closed.Load()) != wantClosed {
				t.Fatal(body.closed.Load(), wantClosed)
			}
			clock.assertStopped(t)
		})
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func TestArticleParserDoesNotInferOptionalMetadataFromUnrelatedListRows(t *testing.T) {
	ref, _ := NormalizeArticle("gall.dcinside.com/test/1")
	html := `<span class="title_subject">outer<span>inner</span>end</span><input id="e_s_n_o" value="token"><div class="gall_writer" data-loc="view" data-nick="unknown" data-uid="" data-ip=""></div><div class="gall_writer" data-loc="view_list" data-nick="unrelated" data-uid="uid"><span class="gall_date" title="2025-01-01 00:00:00"></span><img src="fix_nik.gif"></div>`
	page, err := parseArticle(context.Background(), []byte(html), ref)
	if err != nil || page.article.Title != "outerinnerend" || page.article.Author != nil || page.article.PostedAt != nil || page.article.GalleryName != "" {
		t.Fatalf("%+v %v", page, err)
	}
	for _, raw := range []string{`<input id="e_s_n_o" value="token">`, `<span class="title_subject">title</span>`, `<span class="title_subject">title</span><input id="e_s_n_o" value="token"><link rel="canonical" href="https://evil.test/test/1">`, `<span class="title_subject">title</span><input id="e_s_n_o" value="token"><link rel="canonical" href="https://gall.dcinside.com/test/2">`} {
		if _, err := parseArticle(context.Background(), []byte(raw), ref); err == nil {
			t.Fatal("bad article accepted")
		}
	}
}
func TestStrictCommentFieldRelationDeletionAndPageSchemaFailures(t *testing.T) {
	ref, _ := NormalizeArticle("gall.dcinside.com/test/1")
	for _, mutate := range []func(map[string]any){func(r map[string]any) { delete(r, "no") }, func(r map[string]any) { r["parent"] = "2" }, func(r map[string]any) { delete(r, "name") }, func(r map[string]any) { r["name"] = 33 }, func(r map[string]any) { r["name"] = "" }, func(r map[string]any) { r["user_id"] = ""; r["ip"] = "" }, func(r map[string]any) { r["depth"] = 2 }, func(r map[string]any) { r["depth"] = -1 }, func(r map[string]any) { r["depth"] = 1; r["c_no"] = "1" }, func(r map[string]any) { r["depth"] = 1; r["c_no"] = 0 }, func(r map[string]any) { delete(r, "is_delete") }, func(r map[string]any) { r["reg_date"] = "bad" }, func(r map[string]any) { r["memo"] = false }} {
		row := oneRow("1", "1")
		mutate(row)
		raw, _ := json.Marshal(row)
		if _, err := parseComment(context.Background(), raw, ref); err == nil {
			t.Fatal("malformed comment accepted", row)
		}
	}
	row := oneRow("2", "1")
	row["depth"] = 1
	row["c_no"] = "1"
	row["user_id"] = ""
	row["ip"] = "198.1"
	raw, _ := json.Marshal(row)
	result, err := parseComment(context.Background(), raw, ref)
	if err != nil || result.comment.ParentID == nil || *result.comment.ParentID != "1" || result.comment.ParticipantKind != "anonymous" {
		t.Fatal(result, err)
	}
	row["del_yn"] = "Y"
	row["name"] = ""
	row["reg_date"] = ""
	raw, _ = json.Marshal(row)
	result, err = parseComment(context.Background(), raw, ref)
	if err != nil || !result.deleted {
		t.Fatal(result, err)
	}
	for _, raw := range []string{"null", "[]", "{", `{"total_cnt":-1,"comments":[],"pagination":null,"allow_reply":1}`, `{"total_cnt":100001,"comments":[],"pagination":null,"allow_reply":1}`, `{"total_cnt":0,"comments":{},"pagination":null,"allow_reply":1}`, `{"total_cnt":0,"comments":null,"pagination":{},"allow_reply":1}`, `{"total_cnt":0,"comments":null,"pagination":null,"allow_reply":2}`, `{"total_cnt":0,"comments":null,"pagination":null,"allow_reply":1}{}`} {
		if _, err := parseCommentPage(context.Background(), []byte(raw)); err == nil {
			t.Fatal("invalid page accepted", raw)
		}
	}
	for _, raw := range []string{`<a href="https://evil.test">1</a>`, `<a href="javascript:viewComments(x,'D',true)">1</a>`, `<em>0</em>`, `<em>1</em><em>2</em>`, `<a href="javascript:viewComments(100001,'D',true)">1</a>`, `<a href="javascript:viewComments(2)">1</a>`} {
		if _, err := parseNavigation(context.Background(), raw); err == nil {
			t.Fatal("invalid navigation accepted", raw)
		}
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(context.DeadlineExceeded)
	for _, parser := range []func() error{func() error { _, e := parseCommentPage(ctx, wireJSON(0, 0, 0)); return e }, func() error { _, e := parseArticle(ctx, []byte(articleHTML(General, "test", "1")), ref); return e }, func() error { _, _, _, e := parseMemo(ctx, "safe"); return e }, func() error { _, e := parseNavigation(ctx, ""); return e }} {
		if err := parser(); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("deadline cause changed", err)
		}
	}
}
func TestCollectorIndependentHTTPServerUsesActualTransportAndSequentialForm(t *testing.T) {
	var calls atomic.Int32
	var open atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if open.Add(1) != 1 {
			t.Error("parallel page requests")
		}
		defer open.Add(-1)
		calls.Add(1)
		if r.Method == "GET" {
			http.SetCookie(w, &http.Cookie{Name: "wire", Value: "test", Path: "/"})
			_, _ = io.WriteString(w, articleHTML(General, "test", "1"))
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.Form.Get("comment_page") != "1" || r.Form.Get("e_s_n_o") != "test-read-nonce" || r.Form.Get("no") != "1" {
			t.Error(r.Form)
		}
		if _, err := r.Cookie("wire"); err != nil {
			t.Error("cookie missing", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(wireJSON(1, 1, 1, oneRow("1", "1")))
	}))
	defer server.Close()
	target, _ := url.Parse(server.URL)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	c, clock := collectorFor(t, func(request *http.Request) (*http.Response, error) {
		clone := request.Clone(request.Context())
		clone.URL.Host = target.Host
		clone.URL.Scheme = target.Scheme
		return transport.RoundTrip(clone)
	})
	// Cookies are attached against the original allowed hostname before the test
	// adapter forwards bytes to the isolated loopback server.
	snapshot, err := c.Collect(context.Background(), "gall.dcinside.com/test/1", nil)
	if err != nil || !snapshot.Complete || len(snapshot.Comments) != 1 || calls.Load() != 2 || open.Load() != 0 {
		t.Fatalf("%+v %v calls%d", snapshot, err, calls.Load())
	}
	clock.assertStopped(t)
}
func TestAttemptAlreadySpentByteBudgetStopsReadAndClosesBody(t *testing.T) {
	body := &trackedBody{Reader: bytes.NewReader([]byte("a"))}
	c, clock := collectorFor(t, func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: body}, nil
	})
	jar, _ := cookiejar.New(nil)
	used := int64(maxTotalBytes) + 1
	_, _, _, err := c.attempt(context.Background(), "GET", "https://gall.dcinside.com/test/1", "", "", jar, &used)
	code(t, err, "CollectionLimitExceeded")
	if body.closed.Load() != 1 {
		t.Fatal("body leaked")
	}
	clock.assertStopped(t)
}

func TestMediaURLBooleanGuardsAndErrorProjection(t *testing.T) {
	for _, raw := range []string{"http://dcimg5.dcinside.com/a", "https://x@dcimg5.dcinside.com/a", "https://dcimg5.dcinside.com:443/a", "https://dcimg5.dcinside.com.evil/a", "javascript:alert(1)", "https://[bad/a"} {
		if safeMediaURL(raw) {
			t.Fatal("unsafe media", raw)
		}
	}
	var absent *Error
	if absent.Error() != "InvalidCollectionResponse" || absent.Unwrap() != nil {
		t.Fatal("nil error projection")
	}
	cause := errors.New("secret=never-project")
	err := fault("CollectionTransportError", 2, cause)
	if err.Error() != "CollectionTransportError" || !errors.Is(err, cause) {
		t.Fatal("unsafe error projection")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("production invariant silently ignored")
		}
	}()
	invariant(false, "expected test invariant")
}
func TestConcurrentIndependentCollectionsDoNotShareCookiesSnapshotsOrTimers(t *testing.T) {
	c, clock := collectorFor(t, func(request *http.Request) (*http.Response, error) {
		ref, err := NormalizeArticle(request.URL.String())
		header := http.Header{}
		var raw []byte
		if request.Method == "GET" {
			if err != nil {
				t.Fatal(err)
			}
			header.Set("Set-Cookie", "per_article="+ref.ArticleNo+"; Path=/; Secure")
			raw = []byte(articleHTML(General, "test", ref.ArticleNo))
		} else {
			payload, _ := io.ReadAll(request.Body)
			form, _ := urlParse(string(payload))
			cookie, e := request.Cookie("per_article")
			if e != nil || cookie.Value != form["no"] {
				t.Error("cookie crossed collection", cookie, e, form["no"])
			}
			raw = wireJSON(1, 1, 1, oneRow(form["no"], form["no"]))
		}
		return &http.Response{StatusCode: 200, Header: header, Body: io.NopCloser(bytes.NewReader(raw))}, nil
	})
	var workers sync.WaitGroup
	for i := 1; i <= 16; i++ {
		number := strconv.Itoa(i)
		workers.Go(func() {
			snapshot, err := c.Collect(context.Background(), "gall.dcinside.com/test/"+number, nil)
			if err != nil || len(snapshot.Comments) != 1 || snapshot.Comments[0].ID != number {
				t.Errorf("concurrent collection invariant: %v", err)
			}
		})
	}
	workers.Wait()
	clock.assertStopped(t)
}
func TestProgressPanicStillReleasesAllOwnedTimers(t *testing.T) {
	c, clock := collectorFor(t, func(r *http.Request) (*http.Response, error) {
		raw := []byte(articleHTML(General, "test", "1"))
		if r.Method == "POST" {
			raw = wireJSON(0, 0, 0)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(raw))}, nil
	})
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("progress panic hidden")
			}
		}()
		_, _ = c.Collect(context.Background(), "gall.dcinside.com/test/1", func(Progress) { panic("injected observer defect") })
	}()
	clock.assertStopped(t)
}
func TestScalarAndCardinalityExactLimitRejectMalformedOrPlusOne(t *testing.T) {
	for _, raw := range []string{"null", "true", `"unterminated`, "-1", "1.2"} {
		if _, err := nonnegative(json.RawMessage(raw)); err == nil {
			t.Fatal("invalid scalar accepted", raw)
		}
	}
	for _, total := range []int{maxComments, maxComments + 1} {
		raw, _ := json.Marshal(map[string]any{"total_cnt": total, "comments": nil, "pagination": nil, "allow_reply": 1})
		page, err := parseCommentPage(context.Background(), raw)
		if total == maxComments {
			if err != nil || page.total != total {
				t.Fatal(page, err)
			}
		} else {
			code(t, err, "CollectionLimitExceeded")
		}
	}
	rows := bytes.Repeat([]byte("null,"), maxComments+1)
	rows = rows[:len(rows)-1]
	raw := append([]byte(`{"total_cnt":0,"comments":[`), rows...)
	raw = append(raw, []byte(`],"pagination":null,"allow_reply":1}`)...)
	_, err := parseCommentPage(context.Background(), raw)
	code(t, err, "CollectionLimitExceeded")
	row := oneRow("1", "1")
	row["name"] = strings.Repeat("n", 4097)
	body, _ := json.Marshal(row)
	ref, _ := NormalizeArticle("gall.dcinside.com/test/1")
	_, err = parseComment(context.Background(), body, ref)
	code(t, err, "CollectionLimitExceeded")
}

func TestCollectionUniqueCommentCountExactly100000AndPlusOne(t *testing.T) {
	for _, extra := range []bool{false, true} {
		t.Run(strconv.FormatBool(extra), func(t *testing.T) {
			c, clock := collectorFor(t, func(request *http.Request) (*http.Response, error) {
				if request.Method == "GET" {
					return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(articleHTML(General, "test", "1")))}, nil
				}
				payload, _ := io.ReadAll(request.Body)
				form, _ := urlParse(string(payload))
				page, _ := strconv.Atoi(form["comment_page"])
				rows := make([]map[string]any, 0, 5001)
				for i := (page-1)*5000 + 1; i <= page*5000; i++ {
					rows = append(rows, oneRow(strconv.Itoa(i), "1"))
				}
				if extra && page == 20 {
					rows = append(rows, oneRow("100001", "1"))
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(wireJSON(maxComments, page, 20, rows...)))}, nil
			})
			snapshot, err := c.Collect(context.Background(), "gall.dcinside.com/test/1", nil)
			if extra {
				code(t, err, "CollectionLimitExceeded")
				if !reflect.DeepEqual(snapshot, Snapshot{}) {
					t.Fatal("over-limit partial snapshot")
				}
			} else if err != nil || !snapshot.Complete || len(snapshot.Comments) != maxComments || snapshot.Pages != 20 || snapshot.Comments[0].ID != "1" || snapshot.Comments[maxComments-1].ID != "100000" {
				t.Fatal("unique limit invariant", err)
			}
			clock.assertStopped(t)
		})
	}
}
func TestUnsupportedHumanAndDeletedDuplicateAreCountedOnceWithoutChangingCompletion(t *testing.T) {
	c, clock := collectorFor(t, func(request *http.Request) (*http.Response, error) {
		raw := []byte(articleHTML(General, "test", "1"))
		if request.Method == "POST" {
			unsupported := oneRow("1", "1")
			unsupported["memo"] = `<img src="https://evil.test/x">`
			deleted := oneRow("2", "1")
			deleted["is_delete"] = "5"
			raw = wireJSON(1, 1, 1, unsupported, deleted, deleted)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(raw))}, nil
	})
	snapshot, err := c.Collect(context.Background(), "gall.dcinside.com/test/1", nil)
	if err != nil || !snapshot.Complete || len(snapshot.Comments) != 0 || snapshot.DeletedComments != 1 || snapshot.UnsupportedComments != 1 {
		t.Fatalf("%+v %v", snapshot, err)
	}
	clock.assertStopped(t)
}
