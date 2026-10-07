//go:build windows

package platform

import (
	"github.com/wailsapp/go-webview2/webviewloader"
	"github.com/wailsapp/wails/v3/pkg/w32"
)

// DetectWebView2Runtime uses the public official loader with an empty fixed
// runtime path, matching the pinned Wails installed-runtime discovery order.
func DetectWebView2Runtime() (string, error) {
	return webviewloader.GetAvailableCoreWebView2BrowserVersionString("")
}

func ShowWebView2RuntimeNotice(title, message string) error {
	// Wails w32 uses its first string argument as MessageBoxW lpText.
	return webView2RuntimeNoticeResult(w32.MessageBox(0, message, title, w32.MB_OK|w32.MB_ICONERROR))
}
