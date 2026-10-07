package platform

import "errors"

var (
	ErrWebView2Missing     = errors.New("WebView2 Runtime is not installed")
	ErrWebView2Unavailable = errors.New("WebView2 Runtime could not be checked")
	ErrWebView2Notice      = errors.New("WebView2 Runtime notice could not be shown")
)

const WebView2RuntimeURL = "https://developer.microsoft.com/en-us/microsoft-edge/webview2/"
const WebView2RuntimeNoticeTitle = "Jackpot 실행 안내"
const WebView2RuntimeMissingMessage = "Jackpot을 실행하려면 Microsoft Edge WebView2 Runtime이 필요합니다.\n\nWebView2 Runtime을 설치한 뒤 Jackpot을 다시 실행해 주세요.\n\n공식 설치 안내:\n" + WebView2RuntimeURL + "\n\nJackpot은 자동으로 다운로드하거나 설치하지 않습니다."
const WebView2RuntimeUnavailableMessage = "Microsoft Edge WebView2 Runtime을 확인하지 못해 Jackpot을 시작할 수 없습니다.\n\nWebView2 Runtime의 설치 상태와 실행 권한을 확인한 뒤 Jackpot을 다시 실행해 주세요.\n\n공식 설치 안내:\n" + WebView2RuntimeURL + "\n\nJackpot은 자동으로 다운로드하거나 설치하지 않습니다."

// PreflightWebView2Runtime checks the official local runtime loader and informs
// the user before the caller opens any application data. It never downloads,
// installs, opens a browser, or changes the system runtime configuration.
func PreflightWebView2Runtime() error {
	return CheckWebView2Runtime(DetectWebView2Runtime, ShowWebView2RuntimeNotice)
}

// CheckWebView2Runtime keeps the decision independent of OS effects. Tests can
// provide controlled detection without changing the installed system runtime.
func CheckWebView2Runtime(probe func() (string, error), inform func(string, string) error) error {
	if probe == nil || inform == nil {
		return ErrWebView2Unavailable
	}
	version, detectionErr := probe()
	if detectionErr == nil && version != "" {
		return nil
	}
	failure, message := ErrWebView2Missing, WebView2RuntimeMissingMessage
	if detectionErr != nil {
		failure, message = ErrWebView2Unavailable, WebView2RuntimeUnavailableMessage
	}
	if err := inform(WebView2RuntimeNoticeTitle, message); err != nil {
		return errors.Join(failure, ErrWebView2Notice)
	}
	return failure
}

// A zero MessageBoxW result is the API failure. Any nonzero result dismisses
// this acknowledgement-only notice, including a native titlebar dismissal.
func webView2RuntimeNoticeResult(result int) error {
	if result == 0 {
		return ErrWebView2Notice
	}
	return nil
}
