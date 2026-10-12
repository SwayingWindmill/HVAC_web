package fdd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestEvaluateHTTPReadsTheLoopEvidenceItself(t *testing.T) {
	store := &memoryStore{}
	input := validEvaluation()
	evidence := &loopEvidence{readings: validReadings(input)}
	service := newTestService(t, store, evidence, time.Now)
	handler, err := NewHTTPHandler(service)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/fdd/evaluate/low-delta-t", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if evidence.query.DeviceID != input.DeviceID || len(store.findings) != 1 {
		t.Fatalf("evidence query=%#v findings=%#v", evidence.query, store.findings)
	}
}

func TestEvaluateHTTPRejectsCallerSuppliedEvidence(t *testing.T) {
	store := &memoryStore{}
	input := validEvaluation()
	evidence := &loopEvidence{readings: validReadings(input)}
	service := newTestService(t, store, evidence, time.Now)
	handler, err := NewHTTPHandler(service)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{
		"tenantId":"01990000-3000-7000-8000-000000000001",
		"siteId":"01990000-5000-7000-8000-000000000001",
		"assetId":"01990000-6000-7000-8000-000000000001",
		"deviceId":"01990000-6100-7000-8000-000000000001",
		"evaluationFrom":"2026-08-19T10:00:00Z",
		"evaluationTo":"2026-08-19T10:15:00Z",
		"ruleRevisionId":"fdd-low-delta-t/v1",
		"minimumDeltaTC":5,
		"evidence":[]
	}`)
	request := httptest.NewRequest(http.MethodPost, "/v1/fdd/evaluate/low-delta-t", bytes.NewReader(body))
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if evidence.query.DeviceID != "" || len(store.findings) != 0 {
		t.Fatalf("caller evidence reached evaluation: query=%#v findings=%#v", evidence.query, store.findings)
	}
}
