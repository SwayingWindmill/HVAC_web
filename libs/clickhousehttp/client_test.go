package clickhousehttp

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestParseURLHelpersRejectInvalidOrigins(t *testing.T) {
	if _, err := ParseOriginURL("http://user:pass@127.0.0.1:8123"); err == nil {
		t.Fatal("expected userinfo URL rejection")
	}
	if _, err := ParseOriginURL("http://127.0.0.1:8123/path"); err == nil {
		t.Fatal("expected non-root path rejection")
	}
	if _, err := ParseOriginURL("ftp://127.0.0.1:8123"); err == nil {
		t.Fatal("expected non-http scheme rejection")
	}
	if u, err := ParseOriginURL("https://clickhouse.internal:8443/"); err != nil || u.Host != "clickhouse.internal:8443" {
		t.Fatalf("unexpected ParseOriginURL result u=%v err=%v", u, err)
	}
	if _, err := ParseBaseURL("http://127.0.0.1:8123", true); err != nil {
		t.Fatalf("unexpected ParseBaseURL error: %v", err)
	}
}

func TestPooledTransportProfilesPreserveTuning(t *testing.T) {
	svc := NewPooledHTTPClient(ServiceTransportConfig(10 * time.Second))
	svcTr, ok := svc.Transport.(*http.Transport)
	if !ok || svcTr.MaxIdleConns != 64 || svcTr.MaxIdleConnsPerHost != 32 || svc.CheckRedirect == nil {
		t.Fatalf("unexpected service transport: %#v", svc.Transport)
	}

	dp := NewPooledHTTPClient(DataPlaneTransportConfig(8 * time.Second))
	dpTr, ok := dp.Transport.(*http.Transport)
	if !ok || dp.Timeout != 8*time.Second || dpTr.MaxConnsPerHost != 16 || dpTr.MaxIdleConnsPerHost != 16 || dpTr.ResponseHeaderTimeout != 30*time.Second || dpTr.DialContext == nil {
		t.Fatalf("unexpected data-plane transport: %#v", dp.Transport)
	}
}

func TestClientPingQueryStreamInsertAndCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "reader" || pass != "secret" {
			http.Error(w, "denied", http.StatusForbidden)
			return
		}
		if r.Method == http.MethodGet && r.URL.Query().Get("query") == "SELECT 1" {
			if r.URL.Query().Get("enable_http_compression") == "1" {
				w.Header().Set("Content-Encoding", "gzip")
				gz := gzip.NewWriter(w)
				_, _ = io.WriteString(gz, "1\n")
				_ = gz.Close()
				return
			}
			_, _ = io.WriteString(w, "1\n")
			return
		}
		if r.Header.Get("X-Trace-Header") == "trace-1" {
			body, _ := io.ReadAll(r.Body)
			if strings.Contains(string(body), "OVERSIZED") {
				_, _ = io.WriteString(w, "0123456789ABCDEF")
				return
			}
			w.Header().Set("Content-Encoding", "gzip")
			gz := gzip.NewWriter(w)
			_, _ = io.WriteString(gz, "ok-stream\n")
			_ = gz.Close()
			return
		}
		if r.Header.Get("Content-Encoding") == "gzip" {
			gz, err := gzip.NewReader(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			raw, _ := io.ReadAll(gz)
			_ = gz.Close()
			if !strings.Contains(string(raw), `"k":1`) {
				http.Error(w, "bad batch", http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	base, err := ParseOriginURL(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(base, server.Client(), Credentials{Username: "reader", Password: "secret"})
	if err := client.Ping(t.Context(), true); err != nil {
		t.Fatalf("compressed ping failed: %v", err)
	}
	if err := client.WithCredentials("bad", "pw").Ping(t.Context(), false); err == nil {
		t.Fatal("expected bad credentials ping failure")
	} else {
		var statusErr *StatusError
		if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusForbidden {
			t.Fatalf("unexpected ping status error: %v", err)
		}
	}

	_, err = client.Query(t.Context(), QueryRequest{
		Method:           http.MethodPost,
		SQL:              "SELECT OVERSIZED",
		MaxResponseBytes: 8,
		PrepareHeader:    func(_ context.Context, h http.Header) { h.Set("X-Trace-Header", "trace-1") },
	})
	var tooLarge *ResponseTooLargeError
	if !errors.As(err, &tooLarge) || tooLarge.LimitBytes != 8 {
		t.Fatalf("expected ResponseTooLargeError(8), got %v", err)
	}

	stream, err := client.OpenStream(t.Context(), StreamRequest{
		SQL:           "SELECT 1",
		Params:        url.Values{"param_a": {"b"}},
		PrepareHeader: func(_ context.Context, h http.Header) { h.Set("X-Trace-Header", "trace-1") },
	})
	if err != nil {
		t.Fatalf("OpenStream failed: %v", err)
	}
	streamed, err := io.ReadAll(stream)
	closeErr := stream.Close()
	if err != nil || closeErr != nil || string(streamed) != "ok-stream\n" {
		t.Fatalf("unexpected stream output %q readErr=%v closeErr=%v", streamed, err, closeErr)
	}

	rawBatch := []byte("{\"k\":1}\n")
	compressed, err := CompressGzipBestSpeed(rawBatch)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(compressed, rawBatch) || len(SHA256Hex(rawBatch)) != 64 {
		t.Fatalf("unexpected compression or digest output")
	}
	if err := client.Insert(t.Context(), InsertRequest{
		Params:         url.Values{"query": {"INSERT INTO t FORMAT JSONEachRow"}},
		Body:           compressed,
		CompressedGzip: true,
	}); err != nil {
		t.Fatalf("gzip insert failed: %v", err)
	}
}
