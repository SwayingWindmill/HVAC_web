package optimization

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testOptimizationHTTPHandler(t *testing.T) *HTTPHandler {
	t.Helper()
	at := time.Date(2026, 8, 28, 15, 0, 0, 0, time.UTC)
	publication := &memoryEvaluationStore{}
	service, err := NewDefaultService(publication, &captureEvaluationSink{}, func() time.Time { return at })
	if err != nil {
		t.Fatal(err)
	}
	store := &preparationStoreStub{
		definition: testPreparationDefinition(),
		result: PreparedOptimization{
			OptimizationRunID: "01990000-1950-7000-8000-000000000101",
			InputSnapshotID:   "01990000-1930-7000-8000-000000000101",
			Status:            "PENDING",
		},
	}
	preparer, err := NewPreparer(store, &authoritativeInputStub{state: testAuthoritativeState(at)}, func() time.Time { return at })
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHTTPHandler(service, preparer)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestOptimizationHTTPRejectsCallerAuthoredInputs(t *testing.T) {
	handler := testOptimizationHTTPHandler(t)
	body := `{"siteId":"01990000-5000-7000-8000-000000000001","baseline":{"dailyEnergyKWh":1}}`
	request := httptest.NewRequest(http.MethodPost, "/v1/optimize", strings.NewReader(body))
	request.Header.Set("X-Tenant-ID", "01990000-3000-7000-8000-000000000001")
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestOptimizationHTTPPreparesServerOwnedRun(t *testing.T) {
	handler := testOptimizationHTTPHandler(t)
	body := `{"siteId":"01990000-5000-7000-8000-000000000001"}`
	request := httptest.NewRequest(http.MethodPost, "/v1/optimize", strings.NewReader(body))
	request.Header.Set("X-Tenant-ID", "01990000-3000-7000-8000-000000000001")
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
