package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"runtime/metrics"
	"strconv"
	"strings"
	"testing"
)

func TestFixtureNetworkControlsRejectUnknownAndDefaultHost(t *testing.T) {
	network := new(fixtureNetwork)
	for _, mode := range []string{"", "fail", "block-page2 ", "NORMAL"} {
		if network.arm(mode) == nil {
			t.Fatal("unsupported mode accepted")
		}
	}
	probe := &Probe{transport: &fixtureTransport{}}
	if _, err := probe.ControlFixture("normal"); err == nil {
		t.Fatal("default host enabled controls")
	}
	probe.transport = nil
	if _, err := probe.ControlFixture("normal"); err == nil {
		t.Fatal("nil transport enabled controls")
	}
	if hit, err := network.page2(context.Background()); hit || err != nil {
		t.Fatal("unarmed page intercepted")
	}
}
func TestFixturePage2FailureIsOneLoadOnlyAndReturnedBodyCloses(t *testing.T) {
	transport := &fixtureTransport{large: true}
	for attempt := 0; attempt < 2; attempt++ {
		if err := transport.network.arm("fail-page2"); err != nil {
			t.Fatal(err)
		}
		transport.network.begin()
		req, _ := http.NewRequest(http.MethodPost, "https://gall.dcinside.com/board/comment/", strings.NewReader("id="+largeGalleryID+"&no="+largeArticleNo+"&e_s_n_o=e2e-read-nonce&_GALLTYPE_=G&comment_page=2"))
		response, err := transport.RoundTrip(req)
		req.Body.Close()
		if err != nil || response.StatusCode != 400 {
			t.Fatal("page2 failure not delivered", err)
		}
		raw, err := io.ReadAll(response.Body)
		if err != nil || string(raw) != "fixture page2 rejected" {
			t.Fatal("failure body changed")
		}
		response.Body.Close()
		response.Body.Close()
		if transport.calls.Load() != uint32(attempt+1) || transport.closed.Load() != uint32(attempt+1) {
			t.Fatal("response lifetime imbalance")
		}
		transport.network.begin()
		if hit, err := transport.network.page2(context.Background()); hit || err != nil {
			t.Fatal("fault leaked into next load")
		}
	}
	if report := transport.network.snapshot(); report.Page2Entered != 2 || report.Page2Cancelled != 0 || report.Blocked {
		t.Fatal("failure counters changed", report)
	}
}
func TestFixtureBlockedPageWaitsForRealContextCancellationAndCanRearm(t *testing.T) {
	network := new(fixtureNetwork)
	if err := network.arm("block-page2"); err != nil {
		t.Fatal(err)
	}
	entered := network.barrier
	network.begin()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		hit, err := network.page2(ctx)
		if !hit {
			done <- errors.New("barrier not intercepted")
			return
		}
		done <- err
	}()
	<-entered
	state := network.snapshot()
	if !state.Blocked || state.Page2Entered != 1 || state.Page2Cancelled != 0 {
		t.Fatal("request barrier not live", state)
	}
	if network.arm("normal") == nil {
		t.Fatal("live request rearmed")
	}
	select {
	case <-done:
		t.Fatal("request ended before cancellation")
	default:
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal("context error lost", err)
	}
	state = network.snapshot()
	if state.Blocked || state.Active != "" || state.Page2Cancelled != 1 {
		t.Fatal("cancel cleanup missing", state)
	}
	if err := network.arm("normal"); err != nil {
		t.Fatal(err)
	}
	network.begin()
	if hit, err := network.page2(context.Background()); hit || err != nil {
		t.Fatal("normal fixture intercepted")
	}
}

type countedFixtureRequestBody struct {
	io.Reader
	closes int
}

func (body *countedFixtureRequestBody) Close() error { body.closes++; return nil }

type fixtureRequestReadFailure struct{ cause error }

func (reader fixtureRequestReadFailure) Read([]byte) (int, error) { return 0, reader.cause }

func TestFixtureTransportClosesRequestBodyOnNilURLCancelledRejectedAndReadFailure(t *testing.T) {
	for _, reason := range []string{"nil-url", "cancelled", "rejected", "read-failure"} {
		t.Run(reason, func(t *testing.T) {
			body := &countedFixtureRequestBody{Reader: strings.NewReader("request")}
			request, _ := http.NewRequest(http.MethodPost, "https://gall.dcinside.com/board/comment/", body)
			cause := errors.New("request read failed")
			switch reason {
			case "nil-url":
				request.URL = nil
			case "cancelled":
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				request = request.WithContext(ctx)
			case "rejected":
				request.Method = http.MethodPut
			case "read-failure":
				body.Reader = fixtureRequestReadFailure{cause}
			}
			response, err := (&fixtureTransport{}).RoundTrip(request)
			if err == nil || response != nil || body.closes != 1 {
				t.Fatal("request body lifetime invalid", err, body.closes)
			}
			if reason == "read-failure" && !errors.Is(err, cause) {
				t.Fatal("read error lost", err)
			}
			if reason == "cancelled" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation lost", err)
			}
		})
	}
}
func TestFixtureRequestBodyExactLimitAndOverflowCloseWithoutPartialResponse(t *testing.T) {
	prefix := "id=" + largeGalleryID + "&no=" + largeArticleNo + "&e_s_n_o=e2e-read-nonce&_GALLTYPE_=G&comment_page=1&padding="
	for _, size := range []int{1 << 20, (1 << 20) + 1} {
		body := &countedFixtureRequestBody{Reader: strings.NewReader(prefix + strings.Repeat("x", size-len(prefix)))}
		request, _ := http.NewRequest(http.MethodPost, "https://gall.dcinside.com/board/comment/", body)
		transport := &fixtureTransport{large: true}
		response, err := transport.RoundTrip(request)
		if body.closes != 1 {
			t.Fatal("boundary request body leaked", body.closes)
		}
		if size > 1<<20 {
			if err == nil || response != nil || transport.calls.Load() != 0 {
				t.Fatal("oversize fixture request partly succeeded", err)
			}
		} else {
			if err != nil || response == nil {
				t.Fatal("exact body boundary rejected", err)
			}
			response.Body.Close()
			if transport.calls.Load() != 1 || transport.closed.Load() != 1 {
				t.Fatal("exact boundary response lifetime")
			}
		}
	}
}

func TestMemoryProbeReportsActualRuntimeLimitWithoutChangingIt(t *testing.T) {
	samples := []metrics.Sample{{Name: "/gc/gomemlimit:bytes"}}
	metrics.Read(samples)
	before := samples[0].Value.Uint64()
	report := (&Probe{}).Memory()
	metrics.Read(samples)
	if report.MemoryLimitBytes != strconv.FormatUint(before, 10) || samples[0].Value.Uint64() != before || report.Goroutines < 1 || report.DBOpen != 0 || report.DBInUse != 0 {
		t.Fatal("read-only runtime memory observation invalid", report)
	}
}
