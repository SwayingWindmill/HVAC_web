package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

type stubLiveObservationReader struct {
	mu       sync.Mutex
	requests []OptimizationLiveObservationsRequest
	response OptimizationLiveObservationsResponse
	err      error
}

func (stub *stubLiveObservationReader) ReadOptimizationLiveObservations(_ context.Context, req OptimizationLiveObservationsRequest) (OptimizationLiveObservationsResponse, error) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	stub.requests = append(stub.requests, OptimizationLiveObservationsRequest{
		TenantID:      req.TenantID,
		SiteID:        req.SiteID,
		TelemetryKeys: append([]string(nil), req.TelemetryKeys...),
	})
	return stub.response, stub.err
}

func (stub *stubLiveObservationReader) snapshotRequests() []OptimizationLiveObservationsRequest {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	out := make([]OptimizationLiveObservationsRequest, len(stub.requests))
	copy(out, stub.requests)
	return out
}

type fakeLiveObservationStore struct {
	fakeSnapshotStore
	resolved       map[string]resolvedNumericSitePoint
	ambiguousKeys  []string
	resolveErr     error
	latestByPoint  map[string]OptimizationLiveObservation
	readLatestErr  error
	resolveCalls   int
	readPointCalls [][]string
}

func (fake *fakeLiveObservationStore) ResolveNumericSitePoints(_ context.Context, _, _ string, _ []string) (map[string]resolvedNumericSitePoint, []string, error) {
	fake.resolveCalls++
	if fake.resolveErr != nil {
		return nil, nil, fake.resolveErr
	}
	out := make(map[string]resolvedNumericSitePoint, len(fake.resolved))
	for k, v := range fake.resolved {
		out[k] = v
	}
	return out, append([]string(nil), fake.ambiguousKeys...), nil
}

func (fake *fakeLiveObservationStore) ReadLatestNumericObservations(_ context.Context, _, _ string, pointIDs []string) ([]OptimizationLiveObservation, error) {
	fake.readPointCalls = append(fake.readPointCalls, append([]string(nil), pointIDs...))
	if fake.readLatestErr != nil {
		return nil, fake.readLatestErr
	}
	out := make([]OptimizationLiveObservation, 0, len(pointIDs))
	for _, id := range pointIDs {
		if obs, ok := fake.latestByPoint[id]; ok {
			out = append(out, obs)
		}
	}
	return out, nil
}

func TestOptimizationLiveObservationsAuthAndValidation(t *testing.T) {
	reader := &stubLiveObservationReader{
		response: OptimizationLiveObservationsResponse{Observations: map[string]OptimizationLiveObservation{}},
	}
	handler := NewHandler(ServerConfig{LiveObservationReader: reader})
	validBody := `{"tenantId":"` + tenantA + `","siteId":"` + siteA + `","telemetryKeys":["supply_water_temperature","zone.t53"]}`

	t.Run("method not allowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, InternalOptimizationLiveObservationsPath, nil)
		req.TLS = verifiedTLSState(DefaultOptimizationServiceSPIFFE)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != http.MethodPost {
			t.Fatalf("status=%d allow=%q body=%s", rec.Code, rec.Header().Get("Allow"), rec.Body.String())
		}
	})

	t.Run("forged identity header rejected", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, InternalOptimizationLiveObservationsPath, bytes.NewBufferString(validBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Principal", "spoofed")
		req.TLS = verifiedTLSState(DefaultOptimizationServiceSPIFFE)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "TELEMETRY_FORGED_IDENTITY_HEADER") {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("missing or wrong SPIFFE rejected", func(t *testing.T) {
		for _, peer := range []string{"", gatewaySPIFFE, operationsSPIFFE} {
			req := httptest.NewRequest(http.MethodPost, InternalOptimizationLiveObservationsPath, bytes.NewBufferString(validBody))
			req.Header.Set("Content-Type", "application/json")
			if peer != "" {
				req.TLS = verifiedTLSState(peer)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "TELEMETRY_WORKLOAD_IDENTITY_INVALID") {
				t.Fatalf("peer=%q status=%d body=%s", peer, rec.Code, rec.Body.String())
			}
		}
	})

	t.Run("unconfigured reader and typed-nil PostgresStore return 503", func(t *testing.T) {
		for _, cfg := range []ServerConfig{{}, {Store: (*PostgresStore)(nil)}} {
			unconfigured := NewHandler(cfg)
			req := httptest.NewRequest(http.MethodPost, InternalOptimizationLiveObservationsPath, bytes.NewBufferString(validBody))
			req.Header.Set("Content-Type", "application/json")
			req.TLS = verifiedTLSState(DefaultOptimizationServiceSPIFFE)
			rec := httptest.NewRecorder()
			unconfigured.ServeHTTP(rec, req)
			if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "TELEMETRY_LIVE_OBSERVATIONS_UNAVAILABLE") {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
		}
	})

	t.Run("malformed or invalid payloads rejected with 400", func(t *testing.T) {
		tooManyKeys := make([]string, maximumOptimizationLiveScopeKeys+1)
		for i := range tooManyKeys {
			tooManyKeys[i] = `"k` + string(rune('a'+(i%26))) + `"`
		}
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
			{name: "blank telemetryKey", contentType: "application/json", body: `{"tenantId":"` + tenantA + `","siteId":"` + siteA + `","telemetryKeys":["  "]}`},
			{name: "too many keys", contentType: "application/json", body: `{"tenantId":"` + tenantA + `","siteId":"` + siteA + `","telemetryKeys":[` + strings.Join(tooManyKeys, ",") + `]}`},
		}
		for _, tc := range badCases {
			req := httptest.NewRequest(http.MethodPost, InternalOptimizationLiveObservationsPath, bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", tc.contentType)
			req.TLS = verifiedTLSState(DefaultOptimizationServiceSPIFFE)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("%s: status=%d body=%s", tc.name, rec.Code, rec.Body.String())
			}
		}
	})

	if len(reader.snapshotRequests()) != 0 {
		t.Fatalf("rejected requests must not invoke reader: %+v", reader.snapshotRequests())
	}
}

func TestOptimizationLiveObservationsAutoDiscoveryCacheHitFallbackAndAmbiguity(t *testing.T) {
	pointSupply := "018f2e00-3100-7000-8000-000000000001"
	pointZone53 := "018f2e00-3100-7000-8000-000000000002"
	now := time.Date(2026, 11, 18, 4, 0, 0, 0, time.UTC)

	supplyObs := OptimizationLiveObservation{
		ObservationID:   "018f2e00-8000-7000-8000-000000000101",
		DeviceID:        deviceA,
		PointID:         pointSupply,
		TelemetryKey:    "supply_water_temperature",
		PointRevision:   2,
		SampledAt:       now.Add(-30 * time.Second).Format(time.RFC3339Nano),
		ReceivedAt:      now.Add(-29 * time.Second).Format(time.RFC3339Nano),
		Value:           6.7,
		Unit:            "Cel",
		Quality:         "GOOD",
		QualityReasons:  []string{},
		HealthStatus:    HealthHealthy,
		SourceEventID:   "018f2e00-8000-7000-8000-000000000201",
		SourcePartition: "p0",
		SourceOffset:    12,
	}
	zone53Obs := OptimizationLiveObservation{
		ObservationID:   "018f2e00-8000-7000-8000-000000000102",
		DeviceID:        deviceA,
		PointID:         pointZone53,
		TelemetryKey:    "zone.t53",
		PointRevision:   1,
		SampledAt:       now.Add(-15 * time.Second).Format(time.RFC3339Nano),
		ReceivedAt:      now.Add(-14 * time.Second).Format(time.RFC3339Nano),
		Value:           23.4,
		Unit:            "Cel",
		Quality:         "STALE",
		QualityReasons:  []string{"SOURCE_LAG_EXCEEDED"},
		HealthStatus:    HealthStale,
		HealthReasons:   []string{"SOURCE_LAG_EXCEEDED"},
		SourceEventID:   "018f2e00-8000-7000-8000-000000000202",
		SourcePartition: "p0",
		SourceOffset:    13,
	}

	cache := &memoryLatestCache{}
	if err := cache.PutSiteLiveObservations(t.Context(), tenantA, siteA, []OptimizationLiveObservation{supplyObs}); err != nil {
		t.Fatal(err)
	}

	store := &fakeLiveObservationStore{
		resolved: map[string]resolvedNumericSitePoint{
			"supply_water_temperature": {DeviceID: deviceA, PointID: pointSupply, TelemetryKey: "supply_water_temperature", PointRevision: 2},
			"zone.t53":                 {DeviceID: deviceA, PointID: pointZone53, TelemetryKey: "zone.t53", PointRevision: 1},
		},
		ambiguousKeys: []string{"zone.ambiguous"},
		latestByPoint: map[string]OptimizationLiveObservation{
			pointSupply: supplyObs,
			pointZone53: zone53Obs,
		},
	}

	handler := NewHandler(ServerConfig{
		Store:                            store,
		LatestCache:                      cache,
		AllowedOptimizationServiceSPIFFE: "spiffe://hvac.local/custom-opt",
	})

	reqBody := `{"tenantId":" ` + strings.ToUpper(tenantA) + ` ","siteId":"` + siteA + `","telemetryKeys":[" supply_water_temperature ","zone.t53","zone.ambiguous","zone.t53"]}`
	req := httptest.NewRequest(http.MethodPost, InternalOptimizationLiveObservationsPath, bytes.NewBufferString(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.TLS = verifiedTLSState("spiffe://hvac.local/custom-opt")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp OptimizationLiveObservationsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(resp.AmbiguousKeys, []string{"zone.ambiguous"}) {
		t.Fatalf("ambiguousKeys=%v", resp.AmbiguousKeys)
	}
	if len(resp.Observations) != 2 {
		t.Fatalf("observations=%+v", resp.Observations)
	}
	if got := resp.Observations["supply_water_temperature"]; got.Value != 6.7 || got.Quality != "GOOD" || got.HealthStatus != HealthHealthy {
		t.Fatalf("supply obs=%+v", got)
	}
	if got := resp.Observations["zone.t53"]; got.Value != 23.4 || got.Quality != "STALE" || got.HealthStatus != HealthStale || !slices.Equal(got.QualityReasons, []string{"SOURCE_LAG_EXCEEDED"}) {
		t.Fatalf("zone obs=%+v", got)
	}
	if len(store.readPointCalls) != 1 || !slices.Equal(store.readPointCalls[0], []string{pointZone53}) {
		t.Fatalf("expected PG read only for cache-missed pointZone53, got %+v", store.readPointCalls)
	}

	req2 := httptest.NewRequest(http.MethodPost, InternalOptimizationLiveObservationsPath, bytes.NewBufferString(`{"tenantId":"`+tenantA+`","siteId":"`+siteA+`","telemetryKeys":["supply_water_temperature","zone.t53"]}`))
	req2.Header.Set("Content-Type", "application/json")
	req2.TLS = verifiedTLSState("spiffe://hvac.local/custom-opt")
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("second status=%d body=%s", rec2.Code, rec2.Body.String())
	}
	if len(store.readPointCalls) != 1 {
		t.Fatalf("expected Redis backfill hit with no extra PG read, got %+v", store.readPointCalls)
	}

	store.resolved["supply_water_temperature"] = resolvedNumericSitePoint{
		DeviceID: deviceA, PointID: pointSupply, TelemetryKey: "supply_water_temperature", PointRevision: 3,
	}
	updatedSupply := supplyObs
	updatedSupply.PointRevision = 3
	updatedSupply.Value = 7.2
	store.latestByPoint[pointSupply] = updatedSupply

	req3 := httptest.NewRequest(http.MethodPost, InternalOptimizationLiveObservationsPath, bytes.NewBufferString(`{"tenantId":"`+tenantA+`","siteId":"`+siteA+`","telemetryKeys":["supply_water_temperature"]}`))
	req3.Header.Set("Content-Type", "application/json")
	req3.TLS = verifiedTLSState("spiffe://hvac.local/custom-opt")
	rec3 := httptest.NewRecorder()
	handler.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Fatalf("third status=%d body=%s", rec3.Code, rec3.Body.String())
	}
	var resp3 OptimizationLiveObservationsResponse
	if err := json.NewDecoder(rec3.Body).Decode(&resp3); err != nil {
		t.Fatal(err)
	}
	if resp3.Observations["supply_water_temperature"].PointRevision != 3 || resp3.Observations["supply_water_temperature"].Value != 7.2 {
		t.Fatalf("expected refreshed revision 3 observation, got %+v", resp3.Observations["supply_water_temperature"])
	}

	cache.getErr = errors.New("redis connection refused")
	cache.putErr = errors.New("redis connection refused")
	req4 := httptest.NewRequest(http.MethodPost, InternalOptimizationLiveObservationsPath, bytes.NewBufferString(`{"tenantId":"`+tenantA+`","siteId":"`+siteA+`","telemetryKeys":["supply_water_temperature"]}`))
	req4.Header.Set("Content-Type", "application/json")
	req4.TLS = verifiedTLSState("spiffe://hvac.local/custom-opt")
	rec4 := httptest.NewRecorder()
	handler.ServeHTTP(rec4, req4)
	if rec4.Code != http.StatusOK {
		t.Fatalf("expected 200 fallback when Redis fails, got %d body=%s", rec4.Code, rec4.Body.String())
	}

	store.resolveErr = errors.New("postgres down")
	req5 := httptest.NewRequest(http.MethodPost, InternalOptimizationLiveObservationsPath, bytes.NewBufferString(`{"tenantId":"`+tenantA+`","siteId":"`+siteA+`","telemetryKeys":["supply_water_temperature"]}`))
	req5.Header.Set("Content-Type", "application/json")
	req5.TLS = verifiedTLSState("spiffe://hvac.local/custom-opt")
	rec5 := httptest.NewRecorder()
	handler.ServeHTTP(rec5, req5)
	if rec5.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when PG fails, got %d body=%s", rec5.Code, rec5.Body.String())
	}
}

func TestPostgresStoreLiveObservationNilGuards(t *testing.T) {
	var nilStore *PostgresStore
	if _, _, err := nilStore.ResolveNumericSitePoints(t.Context(), tenantA, siteA, []string{"k"}); err == nil {
		t.Fatal("expected nil store error from ResolveNumericSitePoints")
	}
	if _, err := nilStore.ReadLatestNumericObservations(t.Context(), tenantA, siteA, []string{deviceA}); err == nil {
		t.Fatal("expected nil store error from ReadLatestNumericObservations")
	}
}
