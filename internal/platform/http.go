package platform

import (
	"net"
	"net/http"
	"time"
)

// Collection uses http.RoundTripper as its transport port and owns host,
// redirect, body-size and whole-request deadline checks. RoundTrip itself never
// follows redirects. Each app gets a transport with independent idle connections.
func NewHTTPTransport() *http.Transport {
	return &http.Transport{
		Proxy:             http.ProxyFromEnvironment,
		DialContext:       (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2: true,
		MaxIdleConns:      1, MaxIdleConnsPerHost: 1, MaxConnsPerHost: 1,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
}
