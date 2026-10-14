package fdd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newTestHandler(t *testing.T, store Store, evidence EvidenceSource, now time.Time) http.Handler {
	t.Helper()
	handler, err := NewHTTPHandler(newTestService(t, store, evidence, func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

// An operator's evaluation names the loop; FDD diagnoses it now under the rule in effect.
func TestEvaluateHTTPDiagnosesTheLoopNowUnderTheRuleInEffect(t *testing.T) {
	store := newStore()
	input := validEvaluation()
	evidence := &loopEvidence{readings: validReadings(input)}
	handler := newTestHandler(t, store, evidence, input.At)
	body, err := json.Marshal(map[string]string{"tenantId": input.TenantID, "siteId": input.SiteID, "assetId": input.AssetID, "deviceId": input.DeviceID})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/fdd/evaluate/low-delta-t", bytes.NewReader(body)))

	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var result EvaluationResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.Rule != testRule {
		t.Fatalf("result=%s err=%v", response.Body.String(), err)
	}
	if evidence.query.DeviceID != input.DeviceID || !evidence.query.To.Equal(input.At) || len(store.findings) != 1 {
		t.Fatalf("evidence query=%#v findings=%#v", evidence.query, store.findings)
	}
}

// The caller cannot bring its own threshold, window or evidence.
func TestEvaluateHTTPRejectsACallerThreshold(t *testing.T) {
	store := newStore()
	input := validEvaluation()
	evidence := &loopEvidence{readings: validReadings(input)}
	handler := newTestHandler(t, store, evidence, input.At)
	body := []byte(`{
		"tenantId":"01990000-3000-7000-8000-000000000001",
		"siteId":"01990000-5000-7000-8000-000000000001",
		"assetId":"01990000-6000-7000-8000-000000000001",
		"deviceId":"01990000-6100-7000-8000-000000000001",
		"minimumDeltaTC":1
	}`)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/fdd/evaluate/low-delta-t", bytes.NewReader(body)))

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if evidence.query.DeviceID != "" || len(store.findings) != 0 {
		t.Fatalf("a caller threshold reached evaluation: query=%#v findings=%#v", evidence.query, store.findings)
	}
}

func TestTheRuleInEffectIsReadable(t *testing.T) {
	input := validEvaluation()
	read := func(store Store) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, "/v1/sites/"+input.SiteID+"/fdd/rules/low-delta-t", nil)
		request.Header.Set("X-Tenant-ID", input.TenantID)
		response := httptest.NewRecorder()
		newTestHandler(t, store, &loopEvidence{}, input.At).ServeHTTP(response, request)
		return response
	}
	response := read(newStore())
	var rule RuleRevision
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &rule) != nil || rule != testRule {
		t.Fatalf("rule=%d %s", response.Code, response.Body.String())
	}
	if missing := read(&memoryStore{}); missing.Code != http.StatusNotFound {
		t.Fatalf("without a rule=%d", missing.Code)
	}
}
