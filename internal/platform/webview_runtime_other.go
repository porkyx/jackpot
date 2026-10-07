//go:build !windows

package platform

func DetectWebView2Runtime() (string, error)         { return "", ErrWebView2Unavailable }
func ShowWebView2RuntimeNotice(string, string) error { return ErrWebView2Notice }
