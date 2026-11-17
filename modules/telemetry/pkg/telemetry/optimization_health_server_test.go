package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
)

type stubPointHealthInspector struct {
	mu       sync.Mutex
	requests []OptimizationPointHealthRequest
	result   OptimizationPointHealthResult
	err      error
}

func (stub *stubPointHealthInspector) InspectOptimizationPointHealth(_ context.Context, req OptimizationPointHealthRequest) (OptimizationPointHealthResult, error) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	copied := OptimizationPointHealthRequest{
		TenantID:      req.TenantID,
		SiteID:        req.SiteID,
		DeviceIDs:     append([]string(nil), req.DeviceIDs...),
		TelemetryKeys: append([]string(nil), req.TelemetryKeys...),
	}
	stub.requests = append(stub.requests, copied)
	return stub.result, stub.err
}

func (stub *stubPointHealthInspector) snapshotRequests() []OptimizationPointHealthRequest {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	out := make([]OptimizationPointHealthRequest, len(stub.requests))
	copy(out, stub.requests)
	return out
}

func TestOptimizationPointHealthAuthAndValidation(t *testing.T) {
	inspector := &stubPointHealthInspector{result: OptimizationPointHealthResult{Healthy: true}}
	handler := NewHandler(ServerConfig{PointHealthInspector: inspector})

	validBody := `{"tenantId":"` + tenantA + `","siteId":"` + siteA + `","deviceIds":["` + deviceA + `"],"telemetryKeys":["hdr.sup_t","zone.temp"]}`

	t.Run("method not allowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, InternalOptimizationPointHealthPath, nil)
		req.TLS = verifiedTLSState(DefaultOptimizationServiceSPIFFE)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != http.MethodPost {
			t.Fatalf("status=%d allow=%q body=%s", rec.Code, rec.Header().Get("Allow"), rec.Body.String())
		}
	})

	t.Run("forged identity header rejected", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, InternalOptimizationPointHealthPath, bytes.NewBufferString(validBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Principal", "spoofed")
		req.TLS = verifiedTLSState(DefaultOptimizationServiceSPIFFE)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("missing or wrong SPIFFE rejected", func(t *testing.T) {
		for _, peer := range []string{"", gatewaySPIFFE, operationsSPIFFE} {
			req := httptest.NewRequest(http.MethodPost, InternalOptimizationPointHealthPath, bytes.NewBufferString(validBody))
			req.Header.Set("Content-Type", "application/json")
			if peer != "" {
				req.TLS = verifiedTLSState(peer)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("peer=%q status=%d body=%s", peer, rec.Code, rec.Body.String())
			}
		}
	})

	t.Run("unconfigured inspector returns 503", func(t *testing.T) {
		unconfigured := NewHandler(ServerConfig{})
		req := httptest.NewRequest(http.MethodPost, InternalOptimizationPointHealthPath, bytes.NewBufferString(validBody))
		req.Header.Set("Content-Type", "application/json")
		req.TLS = verifiedTLSState(DefaultOptimizationServiceSPIFFE)
		rec := httptest.NewRecorder()
		unconfigured.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("malformed or invalid payloads rejected with 400", func(t *testing.T) {
		badCases := []struct {
			name        string
			contentType string
			body        string
		}{
			{name: "wrong content type", contentType: "text/plain", body: validBody},
			{name: "unknown field", contentType: "application/json", body: `{"tenantId":"` + tenantA + `","siteId":"` + siteA + `","telemetryKeys":["a"],"extra":true}`},
			{name: "trailing json", contentType: "application/json", body: validBody + `{}`},
			{name: "invalid tenantId", contentType: "application/json", body: `{"tenantId":"not-uuid","siteId":"` + siteA + `","telemetryKeys":["a"]}`},
			{name: "invalid siteId", contentType: "application/json", body: `{"tenantId":"` + tenantA + `","siteId":"not-uuid","telemetryKeys":["a"]}`},
			{name: "invalid deviceId", contentType: "application/json", body: `{"tenantId":"` + tenantA + `","siteId":"` + siteA + `","deviceIds":["bad"],"telemetryKeys":["a"]}`},
			{name: "blank telemetryKey", contentType: "application/json", body: `{"tenantId":"` + tenantA + `","siteId":"` + siteA + `","telemetryKeys":["  "]}`},
		}
		for _, tc := range badCases {
			req := httptest.NewRequest(http.MethodPost, InternalOptimizationPointHealthPath, bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", tc.contentType)
			req.TLS = verifiedTLSState(DefaultOptimizationServiceSPIFFE)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("%s: status=%d body=%s", tc.name, rec.Code, rec.Body.String())
			}
		}
	})

	if len(inspector.snapshotRequests()) != 0 {
		t.Fatalf("rejected requests must not invoke inspector: %+v", inspector.snapshotRequests())
	}
}

func TestOptimizationPointHealthResponses(t *testing.T) {
	t.Run("forwards normalized scope and returns healthy", func(t *testing.T) {
		inspector := &stubPointHealthInspector{result: OptimizationPointHealthResult{Healthy: true}}
		handler := NewHandler(ServerConfig{
			PointHealthInspector:             inspector,
			AllowedOptimizationServiceSPIFFE: "spiffe://hvac.local/custom-opt",
		})
		body := `{"tenantId":" ` + tenantA + ` ","siteId":"` + siteA + `","deviceIds":["` + deviceA + `"],"telemetryKeys":[" hdr.sup_t ","zone.temp"]}`
		req := httptest.NewRequest(http.MethodPost, InternalOptimizationPointHealthPath, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req.TLS = verifiedTLSState("spiffe://hvac.local/custom-opt")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		var got OptimizationPointHealthResult
		if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		if !got.Healthy {
			t.Fatalf("expected healthy response, got %+v", got)
		}
		reqs := inspector.snapshotRequests()
		if len(reqs) != 1 || reqs[0].TenantID != tenantA || reqs[0].SiteID != siteA || !slices.Equal(reqs[0].DeviceIDs, []string{deviceA}) || !slices.Equal(reqs[0].TelemetryKeys, []string{"hdr.sup_t", "zone.temp"}) {
			t.Fatalf("unexpected forwarded request: %+v", reqs)
		}
	})

	t.Run("returns unhealthy point block details", func(t *testing.T) {
		inspector := &stubPointHealthInspector{
			result: OptimizationPointHealthResult{
				Healthy:      false,
				ReasonCode:   "PROBE_OFFSET_ALARM",
				TelemetryKey: "hdr.sup_t",
				BlockReason:  "DRIFT",
				Detail:       "active probe offset alarm (DRIFT) on point hdr.sup_t suppresses optimization",
			},
		}
		handler := NewHandler(ServerConfig{PointHealthInspector: inspector})
		body := `{"tenantId":"` + tenantA + `","siteId":"` + siteA + `","telemetryKeys":["hdr.sup_t"]}`
		req := httptest.NewRequest(http.MethodPost, InternalOptimizationPointHealthPath, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req.TLS = verifiedTLSState(DefaultOptimizationServiceSPIFFE)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		var got OptimizationPointHealthResult
		if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		if got.Healthy || got.ReasonCode != "PROBE_OFFSET_ALARM" || got.TelemetryKey != "hdr.sup_t" || got.BlockReason != "DRIFT" {
			t.Fatalf("unexpected unhealthy result: %+v", got)
		}
	})

	t.Run("inspector error returns 503", func(t *testing.T) {
		inspector := &stubPointHealthInspector{err: errors.New("database unavailable")}
		handler := NewHandler(ServerConfig{PointHealthInspector: inspector})
		body := `{"tenantId":"` + tenantA + `","siteId":"` + siteA + `","telemetryKeys":["hdr.sup_t"]}`
		req := httptest.NewRequest(http.MethodPost, InternalOptimizationPointHealthPath, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req.TLS = verifiedTLSState(DefaultOptimizationServiceSPIFFE)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
	})
}

func TestTelemetryHealthBlockReasonAndScopeHelpers(t *testing.T) {
	if reason, blocked := telemetryHealthBlockReason("HEALTHY", []string{"NORMAL"}); blocked || reason != "" {
		t.Fatalf("expected healthy point to pass, got %q %v", reason, blocked)
	}
	if reason, blocked := telemetryHealthBlockReason("BAD", nil); !blocked || reason != "BAD" {
		t.Fatalf("expected BAD to block, got %q %v", reason, blocked)
	}
	if reason, blocked := telemetryHealthBlockReason("SUSPECT", []string{"probe_offset"}); !blocked || reason != "PROBE_OFFSET" {
		t.Fatalf("expected PROBE_OFFSET reason to block, got %q %v", reason, blocked)
	}
	if got := normalizeUniqueStrings([]string{" a ", "", "b", "a"}); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("normalizeUniqueStrings=%v", got)
	}
	if got := normalizeUUIDStrings([]string{"not-uuid", tenantA, tenantA}); !slices.Equal(got, []string{tenantA}) {
		t.Fatalf("normalizeUUIDStrings=%v", got)
	}
	var nilStore *PostgresStore
	if _, err := nilStore.InspectOptimizationPointHealth(context.Background(), OptimizationPointHealthRequest{TenantID: tenantA, SiteID: siteA}); err == nil {
		t.Fatal("expected nil PostgresStore error")
	}
}
