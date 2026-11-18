package identity

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type trackingReadCloser struct {
	reader  io.Reader
	drained bool
	closed  bool
}

func (t *trackingReadCloser) Read(p []byte) (int, error) {
	n, err := t.reader.Read(p)
	if err == io.EOF {
		t.drained = true
	}
	return n, err
}

func (t *trackingReadCloser) Close() error {
	t.closed = true
	return nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestLoadWeChatConfiguresClonedBoundedTransport(t *testing.T) {
	secretPath := filepath.Join(t.TempDir(), "wechat-secret")
	if err := os.WriteFile(secretPath, []byte("secret-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	client, err := LoadWeChat("wx-app-id", secretPath, "formal")
	if err != nil {
		t.Fatal(err)
	}
	if client == nil || client.Client == nil {
		t.Fatal("expected initialized WeChat HTTP client")
	}
	if client.Client.Timeout != 5*time.Second {
		t.Fatalf("Timeout = %v, want 5s", client.Client.Timeout)
	}
	if client.Client.CheckRedirect == nil || !errors.Is(client.Client.CheckRedirect(nil, nil), http.ErrUseLastResponse) {
		t.Fatal("expected CheckRedirect to refuse redirects")
	}
	tr, ok := client.Client.Transport.(*http.Transport)
	if !ok || tr == http.DefaultTransport {
		t.Fatal("expected cloned *http.Transport distinct from http.DefaultTransport")
	}
	if tr.DialContext == nil || tr.TLSHandshakeTimeout <= 0 || tr.IdleConnTimeout <= 0 || tr.MaxIdleConnsPerHost <= 0 {
		t.Fatalf("expected explicit transport bounds, got %+v", tr)
	}
}

func TestWeChatOpenIDAndPostDrainResponseBodyBeforeClose(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{name: "bad status", status: http.StatusBadGateway, body: "upstream error payload"},
		{name: "json with trailing whitespace", status: http.StatusOK, body: `{"openid":"o1","errcode":0}` + "   \n"},
	}

	for _, tc := range cases {
		t.Run("OpenID/"+tc.name, func(t *testing.T) {
			body := &trackingReadCloser{reader: strings.NewReader(tc.body)}
			wc := &WeChat{
				AppID:   "wx-app",
				Secret:  "sec",
				BaseURL: "https://api.weixin.qq.com",
				Client: &http.Client{
					Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
						return &http.Response{StatusCode: tc.status, Body: body, Header: make(http.Header)}, nil
					}),
				},
			}
			_, _ = wc.OpenID(context.Background(), "code-1")
			if !body.drained || !body.closed {
				t.Fatalf("OpenID body drained=%v closed=%v, want both true", body.drained, body.closed)
			}
		})

		t.Run("post/"+tc.name, func(t *testing.T) {
			body := &trackingReadCloser{reader: strings.NewReader(tc.body)}
			wc := &WeChat{
				AppID:   "wx-app",
				Secret:  "sec",
				BaseURL: "https://api.weixin.qq.com",
				Client: &http.Client{
					Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
						return &http.Response{StatusCode: tc.status, Body: body, Header: make(http.Header)}, nil
					}),
				},
			}
			var out map[string]any
			_ = wc.post(context.Background(), "/cgi-bin/stable_token", []byte(`{}`), &out)
			if !body.drained || !body.closed {
				t.Fatalf("post body drained=%v closed=%v, want both true", body.drained, body.closed)
			}
		})
	}
}

func TestBoundedPoolConfigSetsExplicitLimits(t *testing.T) {
	cfg, err := boundedPoolConfig("postgres://user:pass@127.0.0.1:5432/hvac_identity?sslmode=disable&pool_max_conns=128")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxConns != 16 || cfg.MinConns != 1 {
		t.Fatalf("MaxConns=%d MinConns=%d, want 16 and 1", cfg.MaxConns, cfg.MinConns)
	}
	if cfg.MaxConnLifetime != 30*time.Minute || cfg.MaxConnIdleTime != 5*time.Minute || cfg.HealthCheckPeriod != 30*time.Second {
		t.Fatalf("unexpected pool durations: lifetime=%v idle=%v health=%v", cfg.MaxConnLifetime, cfg.MaxConnIdleTime, cfg.HealthCheckPeriod)
	}
}
