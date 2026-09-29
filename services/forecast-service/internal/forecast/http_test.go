package forecast

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestForecastHTTPRejectsCallerAuthoredSnapshotInputs(t *testing.T) {
	handler := testForecastHTTPHandler(t)
	body := `{"siteId":"01990000-5000-7000-8000-000000000001","subjectType":"SITE","subjectId":"01990000-5000-7000-8000-000000000001","target":"SITE_LOAD","inputSnapshotId":"01990000-1760-7000-8000-000000000001","observations":[{"value":9999}]}`
	request := httptest.NewRequest(http.MethodPost, "/v1/forecast", strings.NewReader(body))
	request.Header.Set("X-Tenant-ID", "01990000-3000-7000-8000-000000000001")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestForecastHTTPPreparesServerOwnedJob(t *testing.T) {
	handler := testForecastHTTPHandler(t)
	body := `{"siteId":"01990000-5000-7000-8000-000000000001","subjectType":"SITE","subjectId":"01990000-5000-7000-8000-000000000001","target":"SITE_LOAD"}`
	request := httptest.NewRequest(http.MethodPost, "/v1/forecast", strings.NewReader(body))
	request.Header.Set("X-Tenant-ID", "01990000-3000-7000-8000-000000000001")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || !strings.Contains(response.Body.String(), `"status":"PENDING"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func testForecastHTTPHandler(t *testing.T) http.Handler {
	t.Helper()
	origin := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	store := &preparationStoreStub{definition: preparationDefinition()}
	history := &metricHistoryStub{facts: []MetricFact{
		metricFact(origin.Add(-60*time.Minute), 760, 1),
		metricFact(origin.Add(-45*time.Minute), 775, 1),
		metricFact(origin.Add(-30*time.Minute), 790, 1),
		metricFact(origin.Add(-15*time.Minute), 805, 1),
	}}
	preparer, err := NewPreparer(store, history, func() time.Time { return origin })
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(&captureSink{}, &memoryPublicationStore{}, func() time.Time { return origin })
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHTTPHandler(service, preparer)
	if err != nil {
		t.Fatal(err)
	}
	return handler.Routes()
}
