package platform

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestWebView2RuntimePreflightDecisionAndSafeNotice(t *testing.T) {
	privateErr := errors.New("private registry and username details")
	for _, tc := range []struct {
		name, version        string
		detect, notice, want error
		calls                int
		message              string
	}{
		{name: "installed", version: "154.0.4258.53"},
		{name: "missing", want: ErrWebView2Missing, calls: 1, message: WebView2RuntimeMissingMessage},
		{name: "detection failure", detect: privateErr, want: ErrWebView2Unavailable, calls: 1, message: WebView2RuntimeUnavailableMessage},
		{name: "version with detection error", version: "154.0.4258.53", detect: privateErr, want: ErrWebView2Unavailable, calls: 1, message: WebView2RuntimeUnavailableMessage},
		{name: "missing notice failure", notice: privateErr, want: ErrWebView2Missing, calls: 1, message: WebView2RuntimeMissingMessage},
		{name: "detection notice failure", detect: privateErr, notice: privateErr, want: ErrWebView2Unavailable, calls: 1, message: WebView2RuntimeUnavailableMessage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			probes, notices := 0, 0
			err := CheckWebView2Runtime(func() (string, error) { probes++; return tc.version, tc.detect }, func(title, message string) error {
				notices++
				if title != WebView2RuntimeNoticeTitle || message != tc.message || !strings.Contains(message, WebView2RuntimeURL) || strings.Contains(message, privateErr.Error()) {
					t.Fatal("unsafe or incorrect notice", title, message)
				}
				return tc.notice
			})
			if probes != 1 || notices != tc.calls || !errors.Is(err, tc.want) {
				t.Fatal("incorrect detection/notice/error", probes, notices, err)
			}
			if (tc.notice != nil) != errors.Is(err, ErrWebView2Notice) || errors.Is(err, privateErr) {
				t.Fatal("notice failure/privacy invariant", err)
			}
		})
	}
}

func TestWebView2RuntimeNilDependenciesFailBeforeEffects(t *testing.T) {
	probe := func() (string, error) { t.Fatal("nil dependency must not probe"); return "", nil }
	notice := func(string, string) error { t.Fatal("nil dependency must not show"); return nil }
	for _, err := range []error{CheckWebView2Runtime(nil, notice), CheckWebView2Runtime(probe, nil), CheckWebView2Runtime(nil, nil)} {
		if !errors.Is(err, ErrWebView2Unavailable) {
			t.Fatal(err)
		}
	}
}

func TestWebView2RuntimeConcurrentCallsKeepIndependentDecisions(t *testing.T) {
	const count = 32
	errs := make(chan error, count)
	var workers sync.WaitGroup
	for i := 0; i < count; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			notices := 0
			err := CheckWebView2Runtime(func() (string, error) { return "", nil }, func(string, string) error { notices++; return nil })
			if !errors.Is(err, ErrWebView2Missing) || notices != 1 {
				errs <- errors.New("concurrent decision altered")
			}
		}()
	}
	workers.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

func TestWebView2RuntimeNativeNoticeResultFailureAndDismissal(t *testing.T) {
	for _, result := range []int{0, 1, 2, 6, 7} {
		err := webView2RuntimeNoticeResult(result)
		if (result == 0) != errors.Is(err, ErrWebView2Notice) {
			t.Fatal("native notice result misclassified", result, err)
		}
	}
}

func TestWebView2RuntimeDependencyFirstNthContinuousFailureIsReadOnly(t *testing.T) {
	for _, failures := range [][]int{{1}, {3}, {1, 2, 3, 4}} {
		for call := 1; call <= 4; call++ {
			fail := false
			for _, n := range failures {
				if call == n {
					fail = true
				}
			}
			notices := 0
			err := CheckWebView2Runtime(func() (string, error) {
				if fail {
					return "", errors.New("controlled detector failure")
				}
				return "154.0.4258.53", nil
			}, func(string, string) error { notices++; return nil })
			if fail != errors.Is(err, ErrWebView2Unavailable) || notices != map[bool]int{true: 1, false: 0}[fail] {
				t.Fatal("detector failure altered later decision", call, err)
			}
			err = CheckWebView2Runtime(func() (string, error) { return "", nil }, func(string, string) error {
				if fail {
					return errors.New("controlled notice failure")
				}
				return nil
			})
			if !errors.Is(err, ErrWebView2Missing) || fail != errors.Is(err, ErrWebView2Notice) {
				t.Fatal("notice failure altered runtime decision", call, err)
			}
		}
	}
}
