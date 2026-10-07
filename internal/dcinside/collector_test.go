package dcinside

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	appclock "github.com/porkyx/jackpot/internal/clock"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type testTimer struct {
	ch    chan time.Time
	stops atomic.Int32
}

func (t *testTimer) C() <-chan time.Time { return t.ch }
func (t *testTimer) Stop() bool          { return t.stops.Add(1) == 1 }

type testClock struct {
	mu        sync.Mutex
	timers    []*testTimer
	durations []time.Duration
	created   chan time.Duration
	invalid   int
	manual    bool
}

func (c *testClock) Now() time.Time { return time.Date(2026, 10, 6, 7, 0, 0, 0, time.UTC) }
func (c *testClock) NewTimer(d time.Duration) appclock.Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.durations = append(c.durations, d)
	if c.invalid == 1 {
		return nil
	}
	t := &testTimer{ch: make(chan time.Time, 1)}
	c.timers = append(c.timers, t)
	if c.invalid == 2 {
		t.ch = nil
	}
	if !c.manual && (d < 15*time.Second || d == 30*time.Second) && d != 0 {
		t.ch <- c.Now()
	}
	if c.created != nil {
		c.created <- d
	}
	return t
}
func (c *testClock) assertStopped(t *testing.T) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, timer := range c.timers {
		if timer.stops.Load() != 1 {
			t.Fatalf("timer %d stops=%d", i, timer.stops.Load())
		}
	}
}

type trackedBody struct {
	io.Reader
	closed atomic.Int32
}

func (b *trackedBody) Close() error { b.closed.Add(1); return nil }
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "dcinside", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func articleHTML(kind GalleryKind, gallery, no string) string {
	ref, err := NormalizeArticle("https://gall.dcinside.com/" + map[GalleryKind]string{General: "board", Minor: "mgallery/board", Mini: "mini/board", Person: "person/board"}[kind] + "/view/?id=" + gallery + "&no=" + no)
	if err != nil {
		panic(err)
	}
	return `<html><head><link rel="canonical" href="` + ref.CanonicalURL + `"></head><body><p class="gallname" data-gallid="` + gallery + `">테스트 갤러리</p><span class="title_subject">한글 &amp; 제목</span><div class="gall_writer" data-loc="view" data-nick="작성자" data-uid="writer" data-ip=""><span><img src="https://nstatic.dcinside.com/fix_nik.gif"></span><span class="gall_date" title="2026-10-06 12:00:00"></span></div><input id="e_s_n_o" value="test-read-nonce"></body></html>`
}
func oneRow(id, no string) map[string]any {
	return map[string]any{"no": id, "parent": no, "name": "P" + id, "user_id": "U" + id, "ip": "", "reg_date": "2025.10.06 12:00:00", "nicktype": "20", "memo": "comment" + id, "depth": 0, "c_no": 0, "is_delete": "0", "del_yn": "N"}
}
func wireJSON(total, page, last int, rows ...map[string]any) []byte {
	nav := ""
	if total > 0 {
		nav = "<em>" + strconv.Itoa(page) + "</em>"
		if last > page {
			nav += `<a href="javascript:viewComments(` + strconv.Itoa(last) + `,'D',true)">last</a>`
		}
	}
	b, _ := json.Marshal(map[string]any{"total_cnt": total, "comment_cnt": 0, "comments": rows, "pagination": nav, "allow_reply": 1})
	return b
}
func code(t *testing.T, err error, want string) {
	t.Helper()
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != want {
		t.Fatalf("error=%v want=%s", err, want)
	}
}
func collectorFor(t *testing.T, handler transportFunc) (*Collector, *testClock) {
	t.Helper()
	clock := &testClock{}
	c, err := NewCollector(handler, clock)
	if err != nil {
		t.Fatal(err)
	}
	return c, clock
}

func TestNormalizeActualURLFixturesAndRejectBeforeNetwork(t *testing.T) {
	var fixtureCases struct {
		Cases []struct {
			ID, Input, Outcome, TypeResolvedBy string
			Normalized                         struct {
				GalleryKind  GalleryKind
				GalleryID    string `json:"galleryId"`
				ArticleNo    string
				CanonicalURL string `json:"canonicalUrl"`
			}
		}
	}
	if err := json.Unmarshal(fixture(t, "urls.json"), &fixtureCases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range fixtureCases.Cases {
		t.Run(tc.ID, func(t *testing.T) {
			ref, err := NormalizeArticle(tc.Input)
			if tc.Outcome == "reject" {
				code(t, err, "InvalidArticleURL")
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if ref.GalleryID != tc.Normalized.GalleryID || ref.ArticleNo != tc.Normalized.ArticleNo {
				t.Fatalf("%+v", ref)
			}
			if !strings.Contains(tc.TypeResolvedBy, "cannot distinguish") && (ref.Kind != tc.Normalized.GalleryKind || ref.CanonicalURL != tc.Normalized.CanonicalURL) {
				t.Fatalf("%+v want=%+v", ref, tc.Normalized)
			}
		})
	}
	for _, raw := range []string{"", "file:///C:/x", "https://user@gall.dcinside.com/board/view/?id=x&no=1", "https://gall.dcinside.com:443/board/view/?id=x&no=1", "https://gall.dcinside.com.evil/board/view/?id=x&no=1", "https://gall.dcinside.com/board/view/?id=x&no=0", "https://gall.dcinside.com/board/view/?id=x&no=9007199254740992", "https://gall.dcinside.com/board/view/?id=x&no=1&no=2", "https://gall.dcinside.com/%62oard/view/?id=x&no=1", "https://gall.dcinside.com/board/x/1", "https://gall.dcinside.com/board/view/?id=bad%2Fid&no=1", "https://gall.dcinside.com/x/1/", "https://gall.dcinside.com/x/1\n"} {
		if _, err := NormalizeArticle(raw); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
}
func TestCollectActualAnonymizedWireFixturesCompleteAndStrictIdentity(t *testing.T) {
	cases := []struct {
		name              string
		kind              GalleryKind
		no                string
		accepted, deleted int
		wantError         string
	}{
		{"general-page1.json", General, "9000", 10, 0, ""}, {"minor-page1.json", Minor, "9001", 5, 1, ""}, {"mini-page1.json", Mini, "9002", 0, 0, "MissingParticipantIdentity"}, {"person-page1.json", Person, "9003", 10, 1, ""}, {"general-extra-page1.json", General, "9004", 20, 0, ""}, {"minor-extra-page1.json", Minor, "9005", 18, 1, ""}, {"general-unbadged-page1.json", General, "9006", 2, 0, ""}, {"mini-zero-page1.json", Mini, "9007", 0, 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := fixture(t, tc.name)
			var bodies []*trackedBody
			calls := 0
			progress := []Progress{}
			c, clock := collectorFor(t, func(request *http.Request) (*http.Response, error) {
				calls++
				var raw []byte
				if request.Method == http.MethodGet {
					raw = []byte(articleHTML(tc.kind, "test", tc.no))
					return &http.Response{StatusCode: 200, Header: http.Header{"Set-Cookie": {"collector=test; Path=/; Secure"}}, Body: newTracked(raw, &bodies)}, nil
				}
				if request.URL.String() != "https://gall.dcinside.com/board/comment/" || request.Method != http.MethodPost {
					t.Fatal(request.URL, request.Method)
				}
				body, _ := io.ReadAll(request.Body)
				form, err := urlParse(string(body))
				if err != nil {
					t.Fatal(err)
				}
				if form["e_s_n_o"] != "test-read-nonce" || form["comment_page"] != "1" || form["no"] != tc.no || form["_GALLTYPE_"] != string(tc.kind) {
					t.Fatal(form)
				}
				if strings.Contains(string(body), "password") || form["memo"] != "" || form["name"] != "" {
					t.Fatal("comment write fields")
				}
				if cookie, err := request.Cookie("collector"); err != nil || cookie.Value != "test" {
					t.Fatal("article cookie omitted")
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: newTracked(data, &bodies)}, nil
			})
			raw := "https://gall.dcinside.com/" + map[GalleryKind]string{General: "board", Minor: "mgallery/board", Mini: "mini/board", Person: "person/board"}[tc.kind] + "/view/?id=test&no=" + tc.no
			snapshot, err := c.Collect(context.Background(), raw, func(p Progress) { progress = append(progress, p) })
			if tc.wantError != "" {
				code(t, err, tc.wantError)
				if !reflect.DeepEqual(snapshot, Snapshot{}) {
					t.Fatal("partial success")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if !snapshot.Complete || snapshot.Pages != 1 || len(snapshot.Comments) != tc.accepted || snapshot.DeletedComments != uint32(tc.deleted) || snapshot.CollectedAt != clock.Now() {
					t.Fatalf("%+v", snapshot)
				}
				if len(progress) != 1 || progress[0].AcceptedComments != uint32(tc.accepted) {
					t.Fatal(progress)
				}
				if snapshot.Article.Title != "한글 & 제목" || snapshot.Article.Author == nil || snapshot.Article.Author.ParticipantKind != "fixed" || snapshot.Article.PostedAt == nil || snapshot.Article.PostedAt.Hour() != 3 {
					t.Fatalf("%+v", snapshot.Article)
				}
				for _, comment := range snapshot.Comments {
					if comment.Nickname == "" || comment.Identifier == "" || comment.PostedAt != nil && comment.PostedAt.Location() != time.UTC {
						t.Fatal(comment)
					}
				}
			}
			if calls != 2 {
				t.Fatal(calls)
			}
			for _, b := range bodies {
				if b.closed.Load() != 1 {
					t.Fatal("body close", b.closed.Load())
				}
			}
			clock.assertStopped(t)
		})
	}
}
func newTracked(raw []byte, bodies *[]*trackedBody) *trackedBody {
	b := &trackedBody{Reader: bytes.NewReader(raw)}
	*bodies = append(*bodies, b)
	return b
}
func urlParse(raw string) (map[string]string, error) {
	values, err := url.ParseQuery(raw)
	result := map[string]string{}
	for key, value := range values {
		if len(value) != 1 {
			return nil, errors.New("duplicate form")
		}
		result[key] = value[0]
	}
	return result, err
}

func TestCollectSequentialDuplicatesLastPageAndTwentyPageLimit(t *testing.T) {
	for _, tc := range []struct {
		name               string
		pages, last, total int
		duplicate          bool
		wantError          string
	}{{"one", 1, 1, 1, false, ""}, {"duplicate", 2, 2, 2, true, ""}, {"twenty", 20, 20, 20, false, ""}, {"twenty-first remains", 20, 21, 21, false, "IncompleteCollection"}, {"terminal count mismatch", 1, 1, 2, false, "IncompleteCollection"}} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			commentPages := []int{}
			var bodies []*trackedBody
			c, clock := collectorFor(t, func(request *http.Request) (*http.Response, error) {
				calls++
				if request.Method == "GET" {
					return &http.Response{StatusCode: 200, Header: http.Header{}, Body: newTracked([]byte(articleHTML(General, "test", "1")), &bodies)}, nil
				}
				raw, _ := io.ReadAll(request.Body)
				form, _ := urlParse(string(raw))
				page, _ := strconv.Atoi(form["comment_page"])
				commentPages = append(commentPages, page)
				last := tc.last
				if page == tc.pages && tc.last == tc.pages {
					last = page
				}
				rows := []map[string]any{oneRow(strconv.Itoa(page), "1")}
				if tc.duplicate && page == 2 {
					rows = append([]map[string]any{oneRow("1", "1")}, rows...)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: newTracked(wireJSON(tc.total, page, last, rows...), &bodies)}, nil
			})
			snapshot, err := c.Collect(context.Background(), "gall.dcinside.com/test/1", nil)
			if tc.wantError != "" {
				code(t, err, tc.wantError)
				if len(snapshot.Comments) != 0 || snapshot.Complete {
					t.Fatal("partial list exposed")
				}
			} else if err != nil || !snapshot.Complete || len(snapshot.Comments) != tc.total || int(snapshot.Pages) != tc.pages {
				t.Fatalf("snapshot=%+v err=%v", snapshot, err)
			}
			if calls != tc.pages+1 {
				t.Fatal(calls)
			}
			for i, p := range commentPages {
				if p != i+1 {
					t.Fatal(commentPages)
				}
			}
			for _, b := range bodies {
				if b.closed.Load() != 1 {
					t.Fatal("body")
				}
			}
			clock.assertStopped(t)
		})
	}
}
func TestCollectPartialFailureOrChangedTotalNeverReturnsSnapshot(t *testing.T) {
	for _, failure := range []string{"http", "total", "different duplicate", "after-last", "parse", "identity"} {
		t.Run(failure, func(t *testing.T) {
			page := 0
			progress := 0
			c, clock := collectorFor(t, func(request *http.Request) (*http.Response, error) {
				raw := []byte(articleHTML(General, "test", "1"))
				status := 200
				if request.Method == "POST" {
					page++
					raw = wireJSON(2, 1, 2, oneRow("1", "1"))
					if page == 2 {
						switch failure {
						case "http":
							status = 403
						case "total":
							raw = wireJSON(3, 2, 2, oneRow("2", "1"))
						case "different duplicate":
							row := oneRow("1", "1")
							row["memo"] = "changed"
							raw = wireJSON(2, 2, 2, row)
						case "after-last":
							raw = fixture(t, "general-after-last-page.json")
						case "parse":
							raw = []byte(`{"comments":[]}`)
						case "identity":
							row := oneRow("2", "1")
							row["user_id"] = ""
							raw = wireJSON(2, 2, 2, row)
						}
					}
				}
				return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(raw))}, nil
			})
			snapshot, err := c.Collect(context.Background(), "gall.dcinside.com/test/1", func(Progress) { progress++ })
			if err == nil || !reflect.DeepEqual(snapshot, Snapshot{}) || progress != 1 || page != 2 {
				t.Fatalf("%+v %v progress%d page%d", snapshot, err, progress, page)
			}
			clock.assertStopped(t)
		})
	}
}
func TestArticleRedirectsValidateEveryDestinationAndResolveMinorMobile(t *testing.T) {
	for _, tc := range []struct {
		name, target string
		wantError    string
	}{{"minor", "https://gall.dcinside.com/mgallery/board/view/?id=test&no=1", ""}, {"host", "https://127.0.0.1/test/1", "InvalidArticleURL"}, {"credentials", "https://x@gall.dcinside.com/test/1", "InvalidArticleURL"}, {"different article", "https://gall.dcinside.com/test/2", "InvalidArticleURL"}, {"port", "https://gall.dcinside.com:443/test/1", "InvalidArticleURL"}, {"loop", "https://m.dcinside.com/board/test/1", "CollectionRedirectLimit"}} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			c, clock := collectorFor(t, func(request *http.Request) (*http.Response, error) {
				calls++
				header := http.Header{}
				body := []byte{}
				status := 302
				if request.URL.Host == "m.dcinside.com" {
					header.Set("Location", tc.target)
				} else if request.Method == "GET" {
					status = 200
					body = []byte(articleHTML(Minor, "test", "1"))
				} else {
					status = 200
					body = wireJSON(0, 0, 0)
				}
				return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(bytes.NewReader(body))}, nil
			})
			snapshot, err := c.Collect(context.Background(), "https://m.dcinside.com/board/test/1", nil)
			if tc.wantError != "" {
				code(t, err, tc.wantError)
				want := 1
				if tc.name == "loop" {
					want = 9
				}
				if calls != want {
					t.Fatal(calls)
				}
			} else if err != nil || snapshot.Article.Ref.Kind != Minor || calls != 3 || snapshot.Article.Ref.RequestURL != "https://m.dcinside.com/board/test/1" {
				t.Fatalf("%+v %v calls%d", snapshot, err, calls)
			}
			clock.assertStopped(t)
		})
	}
}
func TestRetriesOnlyTransientAndRespectBoundedDelays(t *testing.T) {
	for _, tc := range []struct {
		name           string
		statuses       []int
		transportError bool
		header         string
		wantCalls      int
		wantError      string
	}{{"first5xx", []int{503, 200, 200}, false, "", 3, ""}, {"second429", []int{200, 429, 200}, false, "999999", 3, ""}, {"continuous5xx", []int{500, 500, 500}, false, "", 3, "CollectionHTTPError"}, {"connection", []int{0, 200, 200}, true, "", 3, ""}, {"forbidden", []int{403}, false, "", 1, "ArticleAccessDenied"}, {"deleted", []int{404}, false, "", 1, "ArticleNotFound"}, {"schema", []int{200}, false, "", 1, "InvalidCollectionResponse"}} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			c, clock := collectorFor(t, func(request *http.Request) (*http.Response, error) {
				index := calls
				calls++
				status := tc.statuses[index]
				if tc.transportError && index == 0 {
					return nil, errors.New("connection refused")
				}
				raw := []byte(articleHTML(General, "test", "1"))
				if request.Method == "POST" {
					raw = wireJSON(0, 0, 0)
				}
				if tc.name == "schema" {
					raw = []byte("invalid")
				}
				header := http.Header{}
				header.Set("Retry-After", tc.header)
				return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(bytes.NewReader(raw))}, nil
			})
			snapshot, err := c.Collect(context.Background(), "gall.dcinside.com/test/1", nil)
			if tc.wantError != "" {
				code(t, err, tc.wantError)
			} else if err != nil || !snapshot.Complete {
				t.Fatalf("%+v %v", snapshot, err)
			}
			if calls != tc.wantCalls {
				t.Fatal(calls)
			}
			clock.assertStopped(t)
		})
	}
	now := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		raw     string
		attempt int
		want    time.Duration
	}{{"", 1, 250 * time.Millisecond}, {"", 2, time.Second}, {"0", 1, 0}, {"-1", 1, 250 * time.Millisecond}, {"invalid", 1, 250 * time.Millisecond}, {now.Add(60 * time.Second).Format(http.TimeFormat), 1, 30 * time.Second}, {now.Add(-time.Second).Format(http.TimeFormat), 1, 0}, {now.Add(time.Second).Format(http.TimeFormat), 1, time.Second}} {
		h := http.Header{}
		h.Set("Retry-After", tc.raw)
		if got := retryDelay(h, tc.attempt, now); got != tc.want {
			t.Fatalf("%q got%v want%v", tc.raw, got, tc.want)
		}
	}
}
func TestDecodedPageAndCumulativeByteBoundariesIncludingGzip(t *testing.T) {
	for _, tc := range []struct {
		name   string
		length int
		used   int64
		gzip   bool
		want   string
	}{{"exact page", maxPageBytes, 0, false, ""}, {"page plus one", maxPageBytes + 1, 0, false, "CollectionLimitExceeded"}, {"exact cumulative", maxPageBytes, maxTotalBytes - maxPageBytes, false, ""}, {"cumulative plus one", maxPageBytes, maxTotalBytes - maxPageBytes + 1, false, "CollectionLimitExceeded"}, {"gzip expanded", maxPageBytes + 1, 0, true, "CollectionLimitExceeded"}, {"gzip exact", maxPageBytes, 0, true, ""}} {
		t.Run(tc.name, func(t *testing.T) {
			raw := bytes.Repeat([]byte("a"), tc.length)
			header := http.Header{}
			if tc.gzip {
				var compressed bytes.Buffer
				writer := gzip.NewWriter(&compressed)
				_, _ = writer.Write(raw)
				_ = writer.Close()
				raw = compressed.Bytes()
				header.Set("Content-Encoding", "gzip")
			}
			body := &trackedBody{Reader: bytes.NewReader(raw)}
			c, clock := collectorFor(t, func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: header, Body: body}, nil
			})
			jar, _ := cookiejar.New(nil)
			used := tc.used
			data, _, _, err := c.attempt(context.Background(), "GET", "https://gall.dcinside.com/test/1", "", "", jar, &used)
			if tc.want != "" {
				code(t, err, tc.want)
			} else if err != nil || len(data) != tc.length {
				t.Fatal(len(data), err)
			}
			if body.closed.Load() != 1 {
				t.Fatal("response not closed")
			}
			if used < tc.used || used > maxTotalBytes+1 {
				t.Fatal(used)
			}
			clock.assertStopped(t)
		})
	}
}
func TestCommentBodyLimitAndMediaMappingNeverExecutesMarkup(t *testing.T) {
	ref, _ := NormalizeArticle("gall.dcinside.com/test/1")
	for _, length := range []int{0, maxCommentBytes, maxCommentBytes + 1} {
		row := oneRow("1", "1")
		row["memo"] = strings.Repeat("a", length)
		raw, _ := json.Marshal(row)
		result, err := parseComment(context.Background(), raw, ref)
		if length > maxCommentBytes {
			code(t, err, "CollectionLimitExceeded")
		} else if err != nil || len(result.comment.Text) != length {
			t.Fatal(length, err)
		}
	}
	for _, tc := range []struct {
		memo, kind, text string
		media            int
	}{{"<script>secret()</script><b>safe</b>", "text", "safe", 0}, {`<img class="written_dccon" src="https://dcimg5.dcinside.com/dccon.php?id=x">`, "dccon", "[디시콘]", 1}, {`<img class="written_dccon" src="javascript:alert(1)">`, "dccon", "[디시콘]", 0}, {`<img src="https://evil.test/x">`, "unsupported", "", 0}, {"hello<br>world", "text", "hello\nworld", 0}} {
		text, kind, media, err := parseMemo(context.Background(), tc.memo)
		if err != nil || text != tc.text || kind != tc.kind || len(media) != tc.media {
			t.Fatalf("%q %q %v %v", text, kind, media, err)
		}
	}
	row := oneRow("1", "1")
	row["voice"] = map[string]string{"url": "javascript:unsafe"}
	raw, _ := json.Marshal(row)
	result, err := parseComment(context.Background(), raw, ref)
	if err != nil || result.comment.Kind != "voice" || result.comment.Text != "[보플]" || len(result.comment.MediaURLs) != 0 {
		t.Fatal(result, err)
	}
}
func TestDatePreservesUnknownYearAndRejectsMalformedValues(t *testing.T) {
	for _, tc := range []struct {
		raw          string
		valid, known bool
	}{{"2025.10.06 12:00:00", true, true}, {"10.06 12:00:00", true, false}, {"02.29 23:59:59", true, false}, {"02.30 00:00:00", false, false}, {"2025.02.29 00:00:00", false, false}, {"", false, false}, {"10.06 24:00:00", false, false}} {
		value, err := parseDate(tc.raw)
		if (err == nil) != tc.valid || (value != nil) != tc.known {
			t.Fatal(tc.raw, value, err)
		}
	}
}
