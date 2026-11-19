package fdd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

// newTestHandler serves the Gateway's requests: send builds one presented by the Gateway with its context.
func newTestHandler(t *testing.T, store Store, evidence EvidenceSource, now time.Time) (http.Handler, func(method, path string, body []byte, action string) *http.Request) {
	t.Helper()
	fixture := newAuthFixture(t, now)
	handler, err := NewHTTPHandler(newTestService(t, store, evidence, func() time.Time { return now }), fixture.config())
	if err != nil {
		t.Fatal(err)
	}
	input := validEvaluation()
	send := func(method, path string, body []byte, action string) *http.Request {
		request := presentedBy(httptest.NewRequest(method, path, bytes.NewReader(body)), testGatewaySPIFFE)
		request.Header.Set(ContextHeader, fixture.context(t, testGatewaySPIFFE, action, input.TenantID, input.SiteID))
		return request
	}
	return handler, send
}

// An operator's evaluation names the loop; FDD diagnoses it now under the rule in effect.
func TestEvaluateHTTPDiagnosesTheLoopNowUnderTheRuleInEffect(t *testing.T) {
	store := newStore()
	input := validEvaluation()
	evidence := &loopEvidence{samples: validSamples(input)}
	handler, send := newTestHandler(t, store, evidence, input.At)
	body, err := json.Marshal(map[string]string{"tenantId": input.TenantID, "siteId": input.SiteID, "assetId": input.AssetID, "deviceId": input.DeviceID})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, send(http.MethodPost, "/v1/fdd/evaluate/low-delta-t", body, WriteAction))

	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var result EvaluationResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || !reflect.DeepEqual(result.Rule, testRule) {
		t.Fatalf("result=%s err=%v", response.Body.String(), err)
	}
	if evidence.query.LoopDeviceID != input.DeviceID || !evidence.query.To.Equal(input.At) || len(store.findings) != 1 {
		t.Fatalf("evidence query=%#v findings=%#v", evidence.query, store.findings)
	}
}

// The caller cannot bring its own threshold, window or evidence.
func TestEvaluateHTTPRejectsACallerThreshold(t *testing.T) {
	store := newStore()
	input := validEvaluation()
	evidence := &loopEvidence{samples: validSamples(input)}
	handler, send := newTestHandler(t, store, evidence, input.At)
	body := []byte(`{
		"tenantId":"01990000-3000-7000-8000-000000000001",
		"siteId":"01990000-5000-7000-8000-000000000001",
		"assetId":"01990000-6000-7000-8000-000000000001",
		"deviceId":"01990000-6100-7000-8000-000000000001",
		"minimumDeltaTC":1
	}`)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, send(http.MethodPost, "/v1/fdd/evaluate/low-delta-t", body, WriteAction))

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if evidence.query.LoopDeviceID != "" || len(store.findings) != 0 {
		t.Fatalf("a caller threshold reached evaluation: query=%#v findings=%#v", evidence.query, store.findings)
	}
}

func TestTheRuleInEffectIsReadable(t *testing.T) {
	input := validEvaluation()
	read := func(store Store) *httptest.ResponseRecorder {
		handler, send := newTestHandler(t, store, &loopEvidence{}, input.At)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, send(http.MethodGet, "/v1/sites/"+input.SiteID+"/fdd/rules/low-delta-t", nil, ReadAction))
		return response
	}
	response := read(newStore())
	var rule RuleRevision
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &rule) != nil || !reflect.DeepEqual(rule, testRule) {
		t.Fatalf("rule=%d %s", response.Code, response.Body.String())
	}
	if missing := read(&memoryStore{}); missing.Code != http.StatusNotFound {
		t.Fatalf("without a rule=%d", missing.Code)
	}
}

func TestEvaluateMeterImbalanceHTTPDefaultAndExplicitWindow(t *testing.T) {
	store := newStore()
	input := validEvaluation()
	h0 := input.At.Add(-3 * time.Hour)
	evidence := &meterBalanceEvidence{
		hours: []MeterBalanceHour{
			makeBalanceHour(h0, 100, 80),
			makeBalanceHour(h0.Add(time.Hour), 100, 80),
		},
	}
	handler, send := newTestHandler(t, store, evidence, input.At)

	body, err := json.Marshal(map[string]string{
		"tenantId": input.TenantID,
		"siteId":   input.SiteID,
	})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, send(http.MethodPost, "/v1/fdd/evaluate/meter-imbalance", body, WriteAction))
	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var result MeterImbalanceResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode result: %v body=%s", err, response.Body.String())
	}
	if result.Status != "FINDING" || result.ImbalanceStatus != "UNMETERED_GAP" || result.AffectedHours != 2 || result.Finding == nil {
		t.Fatalf("unexpected meter imbalance result=%#v", result)
	}
	if !evidence.lastFrom.Equal(input.At.Add(-24*time.Hour)) || !evidence.lastTo.Equal(input.At) {
		t.Fatalf("default trailing window got [%s, %s), want [%s, %s)", evidence.lastFrom, evidence.lastTo, input.At.Add(-24*time.Hour), input.At)
	}
	if len(store.findings) != 1 {
		t.Fatalf("expected 1 persisted finding, got %d", len(store.findings))
	}

	explicitFrom := input.At.Add(-6 * time.Hour)
	explicitTo := input.At.Add(-time.Hour)
	explicitBody, err := json.Marshal(map[string]any{
		"tenantId": input.TenantID,
		"siteId":   input.SiteID,
		"fromUtc":  explicitFrom,
		"toUtc":    explicitTo,
	})
	if err != nil {
		t.Fatal(err)
	}
	explicitResp := httptest.NewRecorder()
	handler.ServeHTTP(explicitResp, send(http.MethodPost, "/v1/fdd/evaluate/meter-imbalance", explicitBody, WriteAction))
	if explicitResp.Code != http.StatusCreated {
		t.Fatalf("explicit status=%d body=%s", explicitResp.Code, explicitResp.Body.String())
	}
	if !evidence.lastFrom.Equal(explicitFrom) || !evidence.lastTo.Equal(explicitTo) {
		t.Fatalf("explicit window got [%s, %s), want [%s, %s)", evidence.lastFrom, evidence.lastTo, explicitFrom, explicitTo)
	}
}

func TestEvaluateMeterImbalanceHTTPValidationAndScopeGuards(t *testing.T) {
	store := newStore()
	input := validEvaluation()
	evidence := &meterBalanceEvidence{}
	handler, send := newTestHandler(t, store, evidence, input.At)

	unknownField := []byte(`{"tenantId":"01990000-3000-7000-8000-000000000001","siteId":"01990000-5000-7000-8000-000000000001","maxImbalanceRatio":0.01}`)
	if resp := serve(handler, send(http.MethodPost, "/v1/fdd/evaluate/meter-imbalance", unknownField, WriteAction)); resp.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status=%d, want 400", resp.Code)
	}

	badWindow, _ := json.Marshal(map[string]any{
		"tenantId": input.TenantID,
		"siteId":   input.SiteID,
		"fromUtc":  input.At,
		"toUtc":    input.At.Add(-time.Hour),
	})
	if resp := serve(handler, send(http.MethodPost, "/v1/fdd/evaluate/meter-imbalance", badWindow, WriteAction)); resp.Code != http.StatusBadRequest {
		t.Fatalf("inverted window status=%d, want 400", resp.Code)
	}

	otherSite, _ := json.Marshal(map[string]string{
		"tenantId": input.TenantID,
		"siteId":   "01990000-5000-7000-8000-0000000000ff",
	})
	if resp := serve(handler, send(http.MethodPost, "/v1/fdd/evaluate/meter-imbalance", otherSite, WriteAction)); resp.Code != http.StatusForbidden {
		t.Fatalf("cross-site status=%d, want 403", resp.Code)
	}
	if evidence.queryCalled != 0 || len(store.findings) != 0 {
		t.Fatalf("rejected requests should not query evidence or write findings: calls=%d findings=%d", evidence.queryCalled, len(store.findings))
	}
}
