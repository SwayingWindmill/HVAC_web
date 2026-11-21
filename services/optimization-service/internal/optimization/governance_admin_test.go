package optimization

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"
)

type memoryCanaryBindingStore struct {
	mu       sync.Mutex
	bindings map[string]CanaryBindingRecord
}

type stubSiteVisibility struct {
	tenantID string
	siteID   string
}

func (s stubSiteVisibility) CheckSiteVisible(_ context.Context, tenantID, siteID string) (bool, error) {
	return tenantID == s.tenantID && siteID == s.siteID, nil
}

func newMemoryCanaryBindingStore() *memoryCanaryBindingStore {
	return &memoryCanaryBindingStore{bindings: map[string]CanaryBindingRecord{}}
}

func (s *memoryCanaryBindingStore) key(tenantID, siteID, loopKey string) string {
	return tenantID + "|" + siteID + "|" + loopKey
}

func (s *memoryCanaryBindingStore) ListCanaryBindings(_ context.Context, tenantID, siteID string) ([]CanaryBindingRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []CanaryBindingRecord{}
	for _, b := range s.bindings {
		if b.TenantID == tenantID && b.SiteID == siteID {
			out = append(out, b)
		}
	}
	return out, nil
}

func (s *memoryCanaryBindingStore) UpsertCanaryBinding(_ context.Context, binding CanaryBindingRecord) (CanaryBindingRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := s.key(binding.TenantID, binding.SiteID, binding.LoopKey)
	if prev, ok := s.bindings[k]; ok {
		binding.ID = prev.ID
		binding.CreatedAt = prev.CreatedAt
	}
	s.bindings[k] = binding
	return binding, nil
}

func (s *memoryCanaryBindingStore) DeleteCanaryBinding(_ context.Context, tenantID, siteID, loopKey string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := s.key(tenantID, siteID, loopKey)
	if _, ok := s.bindings[k]; !ok {
		return false, nil
	}
	delete(s.bindings, k)
	return true, nil
}

func TestCanaryBindingValidationAndAllowlist(t *testing.T) {
	t.Parallel()

	allowed := []string{"ai-solver-service:8090", "solver.internal"}
	if got, err := NormalizeCanaryLoopKey("chw_pump"); err != nil || got != "CHW_PUMP" {
		t.Fatalf("NormalizeCanaryLoopKey = %q, %v", got, err)
	}
	if _, err := NormalizeCanaryLoopKey("../bad"); err == nil {
		t.Fatalf("expected invalid loopKey to fail")
	}

	if got, err := NormalizeCanaryServingRef("builtin://chiller-cop-model", allowed); err != nil || got != ChillerCOPBuiltinSolverRef {
		t.Fatalf("builtin ref failed: %q, %v", got, err)
	}
	if _, err := NormalizeCanaryServingRef("builtin://untrusted-plugin", allowed); err == nil {
		t.Fatalf("expected unknown builtin ref to fail")
	}
	if got, err := NormalizeCanaryServingRef("http://ai-solver-service:8090/", allowed); err != nil || got != "http://ai-solver-service:8090" {
		t.Fatalf("allowlisted host failed: %q, %v", got, err)
	}
	if _, err := NormalizeCanaryServingRef("http://evil.example.com:8090", allowed); err == nil {
		t.Fatalf("expected non-allowlisted host error")
	}
	if _, err := NormalizeCanaryServingRef("http://user:pass@solver.internal", allowed); err == nil {
		t.Fatalf("expected userinfo URL rejection")
	}
	if _, err := NormalizeCanaryServingRef("https://solver.internal?leak=1", allowed); err == nil {
		t.Fatalf("expected query URL rejection")
	}
}

func TestHTTPCanaryBindingCRUDLifecycleAndRBAC(t *testing.T) {
	t.Parallel()

	fixture := newOptimizationFixture(t)
	store := newMemoryCanaryBindingStore()
	fixture.service.
		WithCanaryStore(store).
		WithSiteVisibility(stubSiteVisibility{tenantID: testTenant, siteID: testSite}).
		WithAllowedSolverHosts([]string{"ai-solver-service:8090"}).
		WithSolverResolver(&stubActiveSolverResolver{
			selection: ActiveSolverSelection{
				ServingRef: ChillerCOPBuiltinSolverRef,
				Source:     SolverSourceDeploymentBinding,
				LoopKey:    DefaultSolverLoopKey,
			},
		})

	listPath := "/v1/sites/" + testSite + "/optimization/canary-bindings"
	putPath := listPath + "/plant"

	// 1. PUT requires WriteAction.
	forbiddenWrite := fixture.call(t, http.MethodPut, putPath, `{"servingRef":"http://ai-solver-service:8090","status":"ACTIVE"}`, testGatewaySPIFFE, testGatewaySPIFFE, ReadAction, testTenant, testSite)
	if forbiddenWrite.Code != http.StatusForbidden {
		t.Fatalf("PUT with ReadAction status=%d, want 403", forbiddenWrite.Code)
	}

	// 2. Host outside allowlist returns 422 optimization_solver_host_not_allowed.
	badHost := fixture.call(t, http.MethodPut, putPath, `{"servingRef":"http://evil.example.com:8090","status":"ACTIVE"}`, testGatewaySPIFFE, testGatewaySPIFFE, WriteAction, testTenant, testSite)
	if badHost.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unallowlisted host status=%d body=%s, want 422", badHost.Code, badHost.Body.String())
	}

	// 3. Valid upsert persists normalized loopKey=PLANT and returns 200.
	created := fixture.call(t, http.MethodPut, putPath, `{"servingRef":"http://ai-solver-service:8090","status":"ACTIVE"}`, testGatewaySPIFFE, testGatewaySPIFFE, WriteAction, testTenant, testSite)
	if created.Code != http.StatusOK {
		t.Fatalf("upsert status=%d body=%s", created.Code, created.Body.String())
	}
	var saved CanaryBindingRecord
	if err := json.Unmarshal(created.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.LoopKey != "PLANT" || saved.ServingRef != "http://ai-solver-service:8090" || saved.Status != CanaryStatusActive {
		t.Fatalf("unexpected saved binding: %+v", saved)
	}

	// 4. GET lists allowedSolverHosts and saved item.
	listed := fixture.call(t, http.MethodGet, listPath, "", testGatewaySPIFFE, testGatewaySPIFFE, ReadAction, testTenant, testSite)
	if listed.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", listed.Code, listed.Body.String())
	}
	var listBody CanaryBindingList
	if err := json.Unmarshal(listed.Body.Bytes(), &listBody); err != nil {
		t.Fatal(err)
	}
	if len(listBody.Items) != 1 || len(listBody.AllowedSolverHosts) != 1 || listBody.AllowedSolverHosts[0] != "ai-solver-service:8090" {
		t.Fatalf("unexpected canary list: %+v", listBody)
	}

	// 5. DELETE removes binding and subsequent DELETE returns 404.
	deleted := fixture.call(t, http.MethodDelete, listPath+"/PLANT", "", testGatewaySPIFFE, testGatewaySPIFFE, WriteAction, testTenant, testSite)
	if deleted.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", deleted.Code, deleted.Body.String())
	}
	missing := fixture.call(t, http.MethodDelete, listPath+"/PLANT", "", testGatewaySPIFFE, testGatewaySPIFFE, WriteAction, testTenant, testSite)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("second delete status=%d, want 404", missing.Code)
	}
}

func TestHTTPNegativeSamplesPaginationAndFiltering(t *testing.T) {
	t.Parallel()

	fixture := newOptimizationFixture(t)
	fw := &memoryFlywheelStore{
		samples: []NegativeSampleRecord{
			{
				ID:                    "01990000-0001-7000-8000-000000000001",
				TenantID:              testTenant,
				SiteID:                testSite,
				DecisionID:            "01990000-1000-7000-8000-000000000001",
				TraceID:               "11111111111111111111111111111111",
				SampleKind:            NegativeSampleGuardrailClamped,
				ReasonCode:            "step_rate_limit",
				SolverServingRef:      ChillerCOPBuiltinSolverRef,
				RawRecommendedValue:   ptr(10.5),
				AppliedOrClampedValue: ptr(8.5),
				GuardrailTrace:        json.RawMessage(`[{"stage":"slew_rate","action":"clamped","before":10.5,"after":8.5,"reason":"max step 1C"}]`),
				FeatureSnapshot: OptimizationFeatureSnapshot{
					SupplyTempC:    ptr(7.5),
					SupplyTempMinC: 5.5,
					SupplyTempMaxC: 12.0,
				},
				OccurredAt: fixture.now.Add(-1 * time.Hour),
			},
			{
				ID:                    "01990000-0002-7000-8000-000000000002",
				TenantID:              testTenant,
				SiteID:                testSite,
				DecisionID:            "01990000-1000-7000-8000-000000000002",
				SampleKind:            NegativeSampleGuardrailClamped,
				ReasonCode:            "min_clamp",
				SolverServingRef:      DefaultBuiltinSolverRef,
				RawRecommendedValue:   ptr(4.8),
				AppliedOrClampedValue: ptr(5.5),
				OccurredAt:            fixture.now.Add(-3 * time.Hour),
			},
			{
				ID:                    "01990000-0003-7000-8000-000000000003",
				TenantID:              testTenant,
				SiteID:                testSite,
				DecisionID:            "01990000-1000-7000-8000-000000000003",
				SampleKind:            NegativeSampleManualOverride,
				ReasonCode:            "manual_backoff",
				SolverServingRef:      ChillerCOPBuiltinSolverRef,
				RawRecommendedValue:   ptr(8.0),
				AppliedOrClampedValue: ptr(7.0),
				OccurredAt:            fixture.now.Add(-2 * time.Hour),
			},
		},
	}
	fixture.service.WithGovernanceFlywheel(fw)

	base := "/v1/sites/" + testSite + "/optimization/negative-samples"
	page1Resp := fixture.call(t, http.MethodGet, base+"?sampleKind=GUARDRAIL_CLAMPED&limit=1&offset=0", "", testGatewaySPIFFE, testGatewaySPIFFE, ReadAction, testTenant, testSite)
	if page1Resp.Code != http.StatusOK {
		t.Fatalf("page1 status=%d body=%s", page1Resp.Code, page1Resp.Body.String())
	}
	var page1 NegativeSamplePage
	if err := json.Unmarshal(page1Resp.Body.Bytes(), &page1); err != nil {
		t.Fatal(err)
	}
	if page1.Total != 2 || !page1.HasMore || len(page1.Items) != 1 || page1.Items[0].ID != "01990000-0001-7000-8000-000000000001" {
		t.Fatalf("unexpected page1: %+v", page1)
	}
	if len(page1.Items[0].GuardrailTrace) != 1 || page1.Items[0].GuardrailTrace[0].Stage != "slew_rate" {
		t.Fatalf("expected decoded guardrailTrace, got %+v", page1.Items[0].GuardrailTrace)
	}

	page2Resp := fixture.call(t, http.MethodGet, base+"?sampleKind=GUARDRAIL_CLAMPED&limit=1&offset=1", "", testGatewaySPIFFE, testGatewaySPIFFE, ReadAction, testTenant, testSite)
	if page2Resp.Code != http.StatusOK {
		t.Fatalf("page2 status=%d body=%s", page2Resp.Code, page2Resp.Body.String())
	}
	var page2 NegativeSamplePage
	if err := json.Unmarshal(page2Resp.Body.Bytes(), &page2); err != nil {
		t.Fatal(err)
	}
	if page2.HasMore || len(page2.Items) != 1 || page2.Items[0].GuardrailTrace == nil {
		t.Fatalf("unexpected page2: %+v", page2)
	}

	badKind := fixture.call(t, http.MethodGet, base+"?sampleKind=BOGUS", "", testGatewaySPIFFE, testGatewaySPIFFE, ReadAction, testTenant, testSite)
	if badKind.Code != http.StatusBadRequest {
		t.Fatalf("bad sampleKind status=%d, want 400", badKind.Code)
	}
}
