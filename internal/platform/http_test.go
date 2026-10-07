package platform_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/porkyx/jackpot/internal/platform"
)

func TestHTTPTransportPreservesFirstNthAndContinuousHTTPFailuresWithoutRetry(t *testing.T) {
	for _, failureAt := range []int32{1, 3, 0} {
		t.Run(fmt.Sprint(failureAt), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count := calls.Add(1)
				if failureAt == 0 || count == failureAt {
					w.WriteHeader(503)
				}
				fmt.Fprint(w, count)
			}))
			defer server.Close()
			transport := platform.NewHTTPTransport()
			defer transport.CloseIdleConnections()
			for attempt := int32(1); attempt <= 4; attempt++ {
				request, err := http.NewRequestWithContext(context.Background(), "GET", server.URL, nil)
				if err != nil {
					t.Fatal(err)
				}
				response, err := transport.RoundTrip(request)
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(response.Body)
				closeErr := response.Body.Close()
				if err != nil || closeErr != nil {
					t.Fatalf("body cleanup: %v/%v", err, closeErr)
				}
				failed := failureAt == 0 || attempt == failureAt
				if (response.StatusCode == 503) != failed || string(body) != strconv.Itoa(int(attempt)) {
					t.Fatalf("response altered: %d/%q", response.StatusCode, body)
				}
			}
			if calls.Load() != 4 {
				t.Fatalf("hidden retry/duplicate: %d", calls.Load())
			}
		})
	}
}

func TestHTTPTransportNeverFollowsRedirectAndKeepsItsLocation(t *testing.T) {
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1) }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer source.Close()
	transport := platform.NewHTTPTransport()
	defer transport.CloseIdleConnections()
	request, err := http.NewRequest("GET", source.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 302 || response.Header.Get("Location") != target.URL || redirected.Load() != 0 {
		t.Fatal("transport crossed redirect boundary")
	}
}

func TestHTTPTransportPreCancellationDoesNotReachServer(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer server.Close()
	transport := platform.NewHTTPTransport()
	defer transport.CloseIdleConnections()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request, err := http.NewRequestWithContext(ctx, "GET", server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := transport.RoundTrip(request)
	if response != nil || !errors.Is(err, context.Canceled) || calls.Load() != 0 {
		t.Fatalf("cancel changed state: %v/%v/%d", response, err, calls.Load())
	}
}

func TestHTTPTransportCancellationClosesRequestAndServerWorker(t *testing.T) {
	entered := make(chan struct{})
	closed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done(); close(closed) }))
	defer server.Close()
	transport := platform.NewHTTPTransport()
	defer transport.CloseIdleConnections()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, "GET", server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		response, err := transport.RoundTrip(request)
		if response != nil {
			_ = response.Body.Close()
		}
		done <- err
	}()
	<-entered
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	<-closed
}

func TestHTTPTransportsHaveSeparateConnectionOwnership(t *testing.T) {
	first, second := platform.NewHTTPTransport(), platform.NewHTTPTransport()
	defer first.CloseIdleConnections()
	defer second.CloseIdleConnections()
	if first == second {
		t.Fatal("shared mutable transport")
	}
	first.MaxIdleConns = 99
	if second.MaxIdleConns != 1 || second.MaxConnsPerHost != 1 {
		t.Fatal("transport configuration leaked between owners")
	}
}
