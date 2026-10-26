package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
)

const realtimePublicPrefix = "/realtime/"

// withRealtimeProxy keeps the browser-facing realtime boundary inside energy-api.
// The current Centrifugo transport remains an internal implementation detail and
// is not exposed directly by Nginx.
func withRealtimeProxy(next http.Handler, logger *slog.Logger) (http.Handler, error) {
	rawUpstream := strings.TrimSpace(os.Getenv("REALTIME_UPSTREAM_URL"))
	if rawUpstream == "" {
		rawUpstream = "http://centrifugo:8000"
	}
	upstream, err := url.Parse(rawUpstream)
	if err != nil || upstream.Scheme == "" || upstream.Host == "" {
		return nil, fmt.Errorf("invalid REALTIME_UPSTREAM_URL %q", rawUpstream)
	}

	proxy := &httputil.ReverseProxy{
		Rewrite: func(proxyRequest *httputil.ProxyRequest) {
			proxyRequest.SetURL(upstream)
			proxyRequest.Out.URL.Path = "/connection/" + strings.TrimPrefix(proxyRequest.In.URL.Path, realtimePublicPrefix)
			proxyRequest.Out.URL.RawPath = ""
			// Keep the browser's Host, as the proxy did before Rewrite; SetURL would replace it with the upstream's.
			proxyRequest.Out.Host = proxyRequest.In.Host
			// Rewrite drops the inbound X-Forwarded-For; copy it so SetXForwarded appends to nginx's chain like Director did.
			proxyRequest.Out.Header["X-Forwarded-For"] = proxyRequest.In.Header["X-Forwarded-For"]
			proxyRequest.SetXForwarded()
		},
		ErrorHandler: func(writer http.ResponseWriter, request *http.Request, proxyErr error) {
			logger.Warn("energy_api_realtime_upstream_unavailable", "error_code", "REALTIME_UPSTREAM_UNAVAILABLE")
			http.Error(writer, http.StatusText(http.StatusBadGateway), http.StatusBadGateway)
		},
	}

	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, realtimePublicPrefix) {
			proxy.ServeHTTP(writer, request)
			return
		}
		next.ServeHTTP(writer, request)
	}), nil
}
