package dcinside

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	appclock "github.com/porkyx/jackpot/internal/clock"
)

const (
	maxPages        = 20
	maxPageBytes    = 10 << 20
	maxTotalBytes   = 100 << 20
	maxComments     = 100000
	maxCommentBytes = 64 << 10
)

type Collector struct {
	transport http.RoundTripper
	clock     appclock.Clock
}

func nilPort(value any) bool {
	if value == nil {
		return true
	}
	kind := reflect.ValueOf(value).Kind()
	switch kind {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflect.ValueOf(value).IsNil()
	default:
		return false
	}
}
func NewCollector(transport http.RoundTripper, clock appclock.Clock) (*Collector, error) {
	if nilPort(transport) || nilPort(clock) {
		return nil, fault("InvalidCollectorDependencies", 0, nil)
	}
	return &Collector{transport: transport, clock: clock}, nil
}
func timedContext(parent context.Context, clock appclock.Clock, duration time.Duration) (context.Context, func(), error) {
	ctx, cancel := context.WithCancelCause(parent)
	timer := clock.NewTimer(duration)
	if nilPort(timer) {
		cancel(appclock.ErrInvalidTimer)
		return nil, nil, appclock.ErrInvalidTimer
	}
	channel := timer.C()
	if channel == nil {
		timer.Stop()
		cancel(appclock.ErrInvalidTimer)
		return nil, nil, appclock.ErrInvalidTimer
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case <-ctx.Done():
		case <-channel:
			cancel(context.DeadlineExceeded)
		}
	}()
	var once sync.Once
	closeContext := func() { once.Do(func() { cancel(context.Canceled); timer.Stop(); <-done }) }
	return ctx, closeContext, nil
}
func collectionError(page uint32, err error) error {
	if errors.Is(err, context.Canceled) {
		return fault("CollectionCancelled", page, context.Canceled)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fault("CollectionTimeout", page, context.DeadlineExceeded)
	}
	var typed *Error
	if errors.As(err, &typed) {
		return fault(typed.Code, page, typed.cause)
	}
	return fault("CollectionTransportError", page, err)
}
func (collector *Collector) Collect(parent context.Context, raw string, progress func(Progress)) (Snapshot, error) {
	if parent == nil || collector == nil || nilPort(collector.transport) || nilPort(collector.clock) {
		return Snapshot{}, fault("InvalidCollectorDependencies", 0, nil)
	}
	if err := parent.Err(); err != nil {
		return Snapshot{}, collectionError(0, err)
	}
	ref, err := NormalizeArticle(raw)
	if err != nil {
		return Snapshot{}, err
	}
	ctx, release, err := timedContext(parent, collector.clock, 5*time.Minute)
	if err != nil {
		return Snapshot{}, collectionError(0, err)
	}
	defer release()
	jar, _ := cookiejar.New(nil)
	used := int64(0)
	body, finalRef, err := collector.article(ctx, ref, jar, &used)
	if err != nil {
		return Snapshot{}, collectionError(0, err)
	}
	parsed, err := parseArticle(ctx, body, finalRef)
	if err != nil {
		return Snapshot{}, collectionError(0, err)
	}
	ref = parsed.article.Ref
	ref.RequestURL = rawRequestURL(raw)
	parsed.article.Ref = ref
	snapshot := Snapshot{Article: parsed.article, Comments: []Comment{}}
	seen := make(map[string]rowResult)
	uniqueHumans := 0
	expected := -1
	for page := 1; page <= maxPages; page++ {
		if err := context.Cause(ctx); err != nil {
			return Snapshot{}, collectionError(uint32(page), err)
		}
		form := url.Values{"id": {ref.GalleryID}, "no": {ref.ArticleNo}, "cmt_id": {ref.GalleryID}, "cmt_no": {ref.ArticleNo}, "focus_cno": {""}, "focus_pno": {""}, "e_s_n_o": {parsed.nonce}, "comment_page": {strconv.Itoa(page)}, "sort": {"D"}, "prevCnt": {""}, "board_type": {""}, "_GALLTYPE_": {string(ref.Kind)}, "secret_article_key": {""}, "clean": {""}, "nptest": {""}}
		response, _, err := collector.request(ctx, http.MethodPost, "https://gall.dcinside.com/board/comment/", form.Encode(), ref.CanonicalURL, jar, &used)
		if err != nil {
			return Snapshot{}, collectionError(uint32(page), err)
		}
		wire, err := parseCommentPage(ctx, response)
		if err != nil {
			return Snapshot{}, collectionError(uint32(page), err)
		}
		if expected < 0 {
			expected = wire.total
		}
		if wire.total != expected {
			return Snapshot{}, fault("IncompleteCollection", uint32(page), nil)
		}
		if expected == 0 {
			if len(wire.rows) != 0 || wire.navigation.current != 0 || wire.navigation.last != 0 {
				return Snapshot{}, fault("IncompleteCollection", uint32(page), nil)
			}
		} else if wire.navigation.current != page || len(wire.rows) == 0 {
			return Snapshot{}, fault("IncompleteCollection", uint32(page), nil)
		}
		for _, rawComment := range wire.rows {
			if err := context.Cause(ctx); err != nil {
				return Snapshot{}, collectionError(uint32(page), err)
			}
			row, err := parseComment(ctx, rawComment, ref)
			if err != nil {
				return Snapshot{}, collectionError(uint32(page), err)
			}
			if row.advertising {
				continue
			}
			if old, ok := seen[row.comment.ID]; ok {
				if !reflect.DeepEqual(old, row) {
					return Snapshot{}, fault("IncompleteCollection", uint32(page), nil)
				}
				continue
			}
			if len(seen) >= maxComments {
				return Snapshot{}, fault("CollectionLimitExceeded", uint32(page), nil)
			}
			seen[row.comment.ID] = row
			if row.deleted {
				snapshot.DeletedComments++
				continue
			}
			uniqueHumans++
			if row.unsupported {
				snapshot.UnsupportedComments++
				continue
			}
			snapshot.Comments = append(snapshot.Comments, row.comment)
		}
		snapshot.Pages = uint32(page)
		if progress != nil {
			progress(Progress{Page: uint32(page), AcceptedComments: uint32(len(snapshot.Comments)), DeletedComments: snapshot.DeletedComments, UnsupportedComments: snapshot.UnsupportedComments})
		}
		if err := context.Cause(ctx); err != nil {
			return Snapshot{}, collectionError(uint32(page), err)
		}
		if wire.navigation.last <= page {
			if uniqueHumans != expected {
				return Snapshot{}, fault("IncompleteCollection", uint32(page), nil)
			}
			snapshot.Complete = true
			snapshot.CollectedAt = collector.clock.Now().UTC()
			invariant(snapshot.Pages >= 1 && snapshot.Pages <= maxPages, "complete page bounds")
			return snapshot, nil
		}
	}
	return Snapshot{}, fault("IncompleteCollection", maxPages, nil)
}
func rawRequestURL(raw string) string {
	ref, err := NormalizeArticle(raw)
	if err != nil {
		panic("validated article became invalid")
	}
	return ref.RequestURL
}
func (collector *Collector) article(ctx context.Context, original ArticleRef, jar http.CookieJar, used *int64) ([]byte, ArticleRef, error) {
	ref := original
	for redirect := 0; redirect <= 8; redirect++ {
		body, header, err := collector.request(ctx, http.MethodGet, ref.RequestURL, "", "", jar, used)
		if err != nil {
			return nil, ArticleRef{}, err
		}
		location := header.Get("Location")
		if location == "" {
			return body, ref, nil
		}
		base, _ := url.Parse(ref.RequestURL)
		target, err := base.Parse(location)
		if err != nil {
			return nil, ArticleRef{}, fault("InvalidArticleURL", 0, err)
		}
		next, err := NormalizeArticle(target.String())
		if err != nil || next.GalleryID != original.GalleryID || next.ArticleNo != original.ArticleNo {
			return nil, ArticleRef{}, fault("InvalidArticleURL", 0, err)
		}
		ref = next
	}
	return nil, ArticleRef{}, fault("CollectionRedirectLimit", 0, nil)
}
func retryDelay(header http.Header, attempt int, now time.Time) time.Duration {
	delay := 250 * time.Millisecond
	if attempt > 1 {
		delay = time.Second
	}
	if value := strings.TrimSpace(header.Get("Retry-After")); value != "" {
		if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds >= 0 {
			if seconds > 30 {
				seconds = 30
			}
			return time.Duration(seconds) * time.Second
		}
		if date, err := http.ParseTime(value); err == nil {
			delay = date.Sub(now)
			if delay < 0 {
				delay = 0
			}
			if delay > 30*time.Second {
				delay = 30 * time.Second
			}
		}
	}
	return delay
}
func (collector *Collector) request(ctx context.Context, method, target, payload, referer string, jar http.CookieJar, used *int64) ([]byte, http.Header, error) {
	for attempt := 1; attempt <= 3; attempt++ {
		body, header, status, err := collector.attempt(ctx, method, target, payload, referer, jar, used)
		if cause := context.Cause(ctx); cause != nil {
			return nil, nil, cause
		}
		var typed *Error
		if errors.As(err, &typed) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return nil, nil, err
		}
		transient := err != nil || status == 429 || (status >= 500 && status <= 599)
		if !transient {
			switch {
			case status >= 200 && status < 300:
				return body, header, nil
			case method == http.MethodGet && (status == 301 || status == 302 || status == 303 || status == 307 || status == 308):
				if header.Get("Location") == "" {
					return nil, nil, fault("InvalidCollectionResponse", 0, nil)
				}
				return body, header, nil
			case status == 404 || status == 410:
				return nil, nil, fault("ArticleNotFound", 0, nil)
			case status == 401 || status == 403:
				return nil, nil, fault("ArticleAccessDenied", 0, nil)
			default:
				return nil, nil, fault("CollectionHTTPError", 0, nil)
			}
		}
		if attempt == 3 {
			if err != nil {
				return nil, nil, err
			}
			return nil, nil, fault("CollectionHTTPError", 0, nil)
		}
		if err := appclock.Wait(ctx, validatedClock{collector.clock}, retryDelay(header, attempt, collector.clock.Now())); err != nil {
			if cause := context.Cause(ctx); cause != nil {
				return nil, nil, cause
			}
			return nil, nil, err
		}
	}
	panic("unreachable retry")
}
func (collector *Collector) attempt(parent context.Context, method, target, payload, referer string, jar http.CookieJar, used *int64) (body []byte, header http.Header, status int, err error) {
	ctx, release, err := timedContext(parent, collector.clock, 15*time.Second)
	if err != nil {
		return nil, nil, 0, fault("InvalidCollectorDependencies", 0, err)
	}
	defer release()
	request, err := http.NewRequestWithContext(ctx, method, target, strings.NewReader(payload))
	if err != nil {
		return nil, nil, 0, fault("InvalidArticleURL", 0, err)
	}
	request.Header.Set("User-Agent", "Jackpot/1.0 (Windows desktop)")
	if method == http.MethodPost {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
		request.Header.Set("X-Requested-With", "XMLHttpRequest")
	}
	if referer != "" {
		request.Header.Set("Referer", referer)
	}
	for _, cookie := range jar.Cookies(request.URL) {
		request.AddCookie(cookie)
	}
	response, roundTripErr := collector.transport.RoundTrip(request)
	if response != nil && response.Body != nil {
		var closeOnce sync.Once
		closeBody := func() { closeOnce.Do(func() { _ = response.Body.Close() }) }
		watcherDone := make(chan struct{})
		stopWatcher := make(chan struct{})
		go func() {
			defer close(watcherDone)
			select {
			case <-ctx.Done():
				closeBody()
			case <-stopWatcher:
			}
		}()
		defer func() { close(stopWatcher); closeBody(); <-watcherDone }()
		if roundTripErr != nil {
			return nil, nil, 0, roundTripErr
		}
		jar.SetCookies(request.URL, response.Cookies())
		header = response.Header
		status = response.StatusCode
		var reader io.Reader = response.Body
		if !response.Uncompressed && strings.EqualFold(header.Get("Content-Encoding"), "gzip") {
			compressed, e := gzip.NewReader(reader)
			if e != nil {
				return nil, header, status, fault("InvalidCollectionResponse", 0, e)
			}
			defer compressed.Close()
			reader = compressed
		} else if encoding := header.Get("Content-Encoding"); encoding != "" && encoding != "identity" && !response.Uncompressed {
			return nil, header, status, fault("InvalidCollectionResponse", 0, nil)
		}
		limit := int64(maxPageBytes)
		if remaining := int64(maxTotalBytes) - *used; remaining < limit {
			limit = remaining
		}
		if limit < 0 {
			return nil, header, status, fault("CollectionLimitExceeded", 0, nil)
		}
		data, e := io.ReadAll(io.LimitReader(reader, limit+1))
		*used += int64(len(data))
		if len(data) > int(limit) {
			return nil, header, status, fault("CollectionLimitExceeded", 0, nil)
		}
		if cause := context.Cause(ctx); cause != nil {
			return nil, header, status, cause
		}
		if e != nil {
			return nil, header, status, e
		}
		return bytes.Clone(data), header, status, nil
	}
	if response != nil && response.Body == nil {
		return nil, nil, 0, fault("InvalidCollectionResponse", 0, nil)
	}
	if roundTripErr != nil {
		if cause := context.Cause(ctx); cause != nil {
			return nil, nil, 0, cause
		}
		return nil, nil, 0, roundTripErr
	}
	return nil, nil, 0, fault("InvalidCollectionResponse", 0, nil)
}

type validatedClock struct{ appclock.Clock }

func (c validatedClock) NewTimer(d time.Duration) appclock.Timer {
	timer := c.Clock.NewTimer(d)
	if nilPort(timer) {
		return nil
	}
	return timer
}
