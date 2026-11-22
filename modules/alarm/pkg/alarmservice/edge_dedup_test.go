package alarmservice

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPEdgeRuleSyncCheckerCachesSuccessAndErrorsWithTTL(t *testing.T) {
	var httpCalls atomic.Int32
	var statusCode atomic.Int32
	statusCode.Store(http.StatusOK)

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpCalls.Add(1)
		code := int(statusCode.Load())
		if code != http.StatusOK {
			w.WriteHeader(code)
			return
		}
		_ = json.NewEncoder(w).Encode(EdgeSiteRuleSyncStatus{
			SchemaVersion: 1,
			TenantID:      r.URL.Query().Get("tenantId"),
			SiteID:        "site-1",
			Gateways: []EdgeGatewayRuleSyncSummary{{
				GatewayID:               "gw-1",
				Online:                  true,
				EdgeRulesSynced:         true,
				ReportedBundleRevision:  2,
				ReportedEdgeOnlyRuleIDs: []string{"rule-edge-1"},
			}},
		})
	}))
	defer srv.Close()

	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	checker, err := NewHTTPEdgeRuleSyncChecker(HTTPEdgeRuleSyncCheckerConfig{
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
		CacheTTL:   5 * time.Second,
		Timeout:    time.Second,
		Now:        func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	s1, err := checker.GetSiteEdgeRuleSyncStatus(context.Background(), "tenant-1", "site-1")
	if err != nil || len(s1.Gateways) != 1 || httpCalls.Load() != 1 {
		t.Fatalf("first fetch s=%+v err=%v calls=%d", s1, err, httpCalls.Load())
	}
	s2, err := checker.GetSiteEdgeRuleSyncStatus(context.Background(), "tenant-1", "site-1")
	if err != nil || len(s2.Gateways) != 1 || httpCalls.Load() != 1 {
		t.Fatalf("cached fetch should not hit HTTP: calls=%d err=%v", httpCalls.Load(), err)
	}

	now = now.Add(6 * time.Second)
	statusCode.Store(http.StatusServiceUnavailable)
	if _, err := checker.GetSiteEdgeRuleSyncStatus(context.Background(), "tenant-1", "site-1"); err == nil || httpCalls.Load() != 2 {
		t.Fatalf("expected error on 503 after TTL expiry: err=%v calls=%d", err, httpCalls.Load())
	}
	if _, err := checker.GetSiteEdgeRuleSyncStatus(context.Background(), "tenant-1", "site-1"); err == nil || httpCalls.Load() != 2 {
		t.Fatalf("expected negative TTL cache hit without extra HTTP call: calls=%d", httpCalls.Load())
	}
}

func TestLoadConnectivityEdgeRuleSyncCheckerModes(t *testing.T) {
	t.Setenv("ALARM_CONNECTIVITY_SERVICE_URL", "")
	t.Setenv("CONNECTIVITY_SERVICE_URL", "")
	c, err := LoadConnectivityEdgeRuleSyncChecker(nil)
	if err != nil || c != nil {
		t.Fatalf("unset URL expected (nil, nil), got (%v, %v)", c, err)
	}

	t.Setenv("ALARM_CONNECTIVITY_SERVICE_URL", "off")
	c, err = LoadConnectivityEdgeRuleSyncChecker(nil)
	if err != nil || c != nil {
		t.Fatalf("off URL expected (nil, nil), got (%v, %v)", c, err)
	}

	// Inherited default URL skips gracefully if gateway CA file is absent.
	t.Setenv("ALARM_CONNECTIVITY_SERVICE_URL", "")
	t.Setenv("CONNECTIVITY_SERVICE_URL", "https://connectivity:8448")
	t.Setenv("CONNECTIVITY_SERVER_CA", "/nonexistent/gateway-ca.crt")
	c, err = LoadConnectivityEdgeRuleSyncChecker(nil)
	if err != nil || c != nil {
		t.Fatalf("inherited URL with missing CA should skip gracefully, got (%v, %v)", c, err)
	}

	// Explicitly configured ALARM_CONNECTIVITY_SERVICE_URL fails closed when CA is missing.
	t.Setenv("ALARM_CONNECTIVITY_SERVICE_URL", "https://connectivity:8448")
	if _, err := LoadConnectivityEdgeRuleSyncChecker(nil); err == nil {
		t.Fatal("expected explicit ALARM_CONNECTIVITY_SERVICE_URL with missing CA to fail closed")
	}
}
