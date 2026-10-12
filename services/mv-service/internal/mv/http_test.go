package mv

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func call(t *testing.T, handler http.Handler, method, path, body string) (int, map[string]any) {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set(headerTenant, testTenant)
	request.Header.Set(headerActor, testActor)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	var decoded map[string]any
	_ = json.Unmarshal(recorder.Body.Bytes(), &decoded)
	return recorder.Code, decoded
}

func TestTheMeasureLifecycleOverHTTP(t *testing.T) {
	implemented := time.Date(2026, 10, 15, 9, 0, 0, 0, shanghai)
	store := newMemoryStore()
	service := newTestService(t, store, plantHistory{changedAt: implemented, factor: 0.9})
	handler, err := NewHTTPHandler(service)
	if err != nil {
		t.Fatal(err)
	}
	base := "/v1/sites/" + testSite + "/savings/measures"

	status, created := call(t, handler, http.MethodPost, base, `{"name":"提高冷冻水供水温度","kind":"SETPOINT_RESET","plannedAt":"2026-10-15T09:00:00+08:00","investmentCny":0}`)
	if status != http.StatusCreated || created["status"] != "PLANNED" {
		t.Fatalf("register = %d %v", status, created)
	}
	id := created["id"].(string)

	status, measure := call(t, handler, http.MethodPost, base+"/"+id+"/implement", `{"implementedAt":"2026-10-15T09:00:00+08:00"}`)
	baseline, _ := measure["baseline"].(map[string]any)
	model, _ := baseline["model"].(map[string]any)
	if status != http.StatusOK || measure["status"] != "IMPLEMENTED" || baseline["trusted"] != true || baseline["samples"] != float64(336) || model["slope"].(float64) <= 0 {
		t.Fatalf("implement = %d %v", status, measure)
	}

	stored, _ := store.Measure(t.Context(), testTenant, testSite, id)
	if err := service.VerifyDay(t.Context(), stored, time.Date(2026, 10, 17, 0, 0, 0, 0, shanghai)); err != nil {
		t.Fatal(err)
	}
	status, detail := call(t, handler, http.MethodGet, base+"/"+id, "")
	days, _ := detail["days"].([]any)
	totals, _ := detail["totals"].(map[string]any)
	if status != http.StatusOK || len(days) != 1 || totals["avoidedKWh"].(float64) <= 0 {
		t.Fatalf("detail = %d %v", status, detail)
	}

	status, summary := call(t, handler, http.MethodGet, "/v1/sites/"+testSite+"/savings/summary?from=2026-10-01&to=2026-10-31", "")
	if status != http.StatusOK || summary["reporting"] != float64(1) || summary["totals"].(map[string]any)["days"] != float64(1) {
		t.Fatalf("summary = %d %v", status, summary)
	}

	if status, body := call(t, handler, http.MethodPost, base+"/"+id+"/refit", `{"from":"2026-10-01T00:00:00+08:00","to":"2026-10-08T00:00:00+08:00"}`); status != http.StatusConflict || body["code"] != "savings_baseline_locked" {
		t.Fatalf("refit after results = %d %v", status, body)
	}
}

func TestBadRequestsAreRefusedWithAReason(t *testing.T) {
	handler, err := NewHTTPHandler(newTestService(t, newMemoryStore(), plantHistory{}))
	if err != nil {
		t.Fatal(err)
	}
	base := "/v1/sites/" + testSite + "/savings/measures"
	for _, test := range []struct {
		method, path, body string
		status             int
	}{
		{http.MethodPost, base, `{"name":"","kind":"SETPOINT_RESET","plannedAt":"2026-10-15T09:00:00+08:00"}`, http.StatusBadRequest},
		{http.MethodPost, base, `{"name":"x","kind":"MAGIC","plannedAt":"2026-10-15T09:00:00+08:00"}`, http.StatusBadRequest},
		{http.MethodPost, base, `{"name":"x","kind":"OTHER","plannedAt":"2026-10-15T09:00:00+08:00","owner":"me"}`, http.StatusBadRequest},
		{http.MethodGet, base + "/01990000-7000-7000-8000-00000000000f", "", http.StatusNotFound},
		{http.MethodGet, "/v1/sites/" + testSite + "/savings/summary?from=2026-10-31&to=2026-10-01", "", http.StatusBadRequest},
	} {
		if status, body := call(t, handler, test.method, test.path, test.body); status != test.status {
			t.Fatalf("%s %s = %d %v, want %d", test.method, test.path, status, body, test.status)
		}
	}
}

func TestABaselineWithoutEnoughHoursCarriesNoModel(t *testing.T) {
	implemented := time.Date(2026, 10, 15, 9, 0, 0, 0, shanghai)
	// Only the last two days before implementation have data.
	history := plantHistory{changedAt: implemented, factor: 0.9, gapFrom: implemented.Add(-14 * 24 * time.Hour), gapTo: implemented.Add(-48 * time.Hour)}
	handler, err := NewHTTPHandler(newTestService(t, newMemoryStore(), history))
	if err != nil {
		t.Fatal(err)
	}
	base := "/v1/sites/" + testSite + "/savings/measures"
	_, created := call(t, handler, http.MethodPost, base, `{"name":"提高冷冻水供水温度","kind":"SETPOINT_RESET","plannedAt":"2026-10-15T09:00:00+08:00"}`)
	_, measure := call(t, handler, http.MethodPost, base+"/"+created["id"].(string)+"/implement", `{"implementedAt":"2026-10-15T09:00:00+08:00"}`)
	baseline, _ := measure["baseline"].(map[string]any)
	if baseline["trusted"] != false || baseline["samples"] != float64(48) || baseline["model"] != nil || baseline["reason"] != ReasonTooFewHours {
		t.Fatalf("baseline = %v", baseline)
	}
}
