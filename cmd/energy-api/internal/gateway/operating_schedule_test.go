package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

func TestOperatingScheduleRoutesMatchPublicContract(t *testing.T) {
	siteID := "01990000-5000-7000-8000-000000000001"
	exceptionID := "01990000-7000-7000-8000-000000000001"

	valid := []struct {
		method, path, kind, internalPath string
		write                            bool
	}{
		{http.MethodGet, "/api/v1/sites/" + siteID + "/operating-schedule", "schedule-get", "/internal/v1/registry/sites/" + siteID + "/operating-schedule", false},
		{http.MethodPut, "/api/v1/sites/" + siteID + "/operating-schedule", "schedule-put", "/internal/v1/registry/sites/" + siteID + "/operating-schedule", true},
		{http.MethodGet, "/api/v1/sites/" + siteID + "/operating-exceptions", "exceptions-list", "/internal/v1/registry/sites/" + siteID + "/operating-exceptions", false},
		{http.MethodPost, "/api/v1/sites/" + siteID + "/operating-exceptions", "exceptions-create", "/internal/v1/registry/sites/" + siteID + "/operating-exceptions", true},
		{http.MethodPost, "/api/v1/sites/" + siteID + "/operating-exceptions/" + exceptionID + "/decide", "exceptions-decide", "/internal/v1/registry/sites/" + siteID + "/operating-exceptions/" + exceptionID + "/decide", true},
	}
	for _, tc := range valid {
		route, ok := matchPublicOperatingScheduleRoute(tc.method, tc.path)
		if !ok || route.siteID != siteID || route.kind != tc.kind || route.internalPath != tc.internalPath || route.write != tc.write {
			t.Fatalf("%s %s resolved as %#v ok=%t", tc.method, tc.path, route, ok)
		}
	}

	invalid := []struct{ method, path string }{
		{http.MethodPost, "/api/v1/sites/" + siteID + "/operating-schedule"},
		{http.MethodDelete, "/api/v1/sites/" + siteID + "/operating-schedule"},
		{http.MethodPut, "/api/v1/sites/" + siteID + "/operating-exceptions"},
		{http.MethodGet, "/api/v1/sites/" + siteID + "/operating-exceptions/" + exceptionID + "/decide"},
		{http.MethodPost, "/api/v1/sites/" + siteID + "/operating-exceptions/" + exceptionID + "/approve"},
		{http.MethodGet, "/api/v1/sites/" + siteID + "/operating-schedule/"},
	}
	for _, tc := range invalid {
		if _, ok := matchPublicOperatingScheduleRoute(tc.method, tc.path); ok {
			t.Fatalf("%s %s unexpectedly matched an operating schedule route", tc.method, tc.path)
		}
	}
}

func TestOperatingScheduleRequiredActionByPayload(t *testing.T) {
	// Create exception requires schedule.request unless autoApprove is true.
	action, code, err := resolveOperatingExceptionCreateAction([]byte(`{"title":"国庆停机","kind":"HOLIDAY_SHUTDOWN","startDate":"2026-10-01","endDate":"2026-10-03","replacementSlots":[],"meta":{"expectedRevision":0,"idempotencyKey":"exc-00000001","reason":"节假日停机"}}`))
	if err != nil || action != registryauth.ActionScheduleRequest || code != "SCHEDULE_REQUEST_FORBIDDEN" {
		t.Fatalf("normal create action=%q code=%q err=%v", action, code, err)
	}
	action, code, err = resolveOperatingExceptionCreateAction([]byte(`{"title":"国庆停机","kind":"HOLIDAY_SHUTDOWN","startDate":"2026-10-01","endDate":"2026-10-03","replacementSlots":[],"autoApprove":true,"meta":{"expectedRevision":0,"idempotencyKey":"exc-00000002","reason":"节假日停机"}}`))
	if err != nil || action != registryauth.ActionScheduleManage || code != "SCHEDULE_MANAGE_FORBIDDEN" {
		t.Fatalf("autoApprove create action=%q code=%q err=%v", action, code, err)
	}

	// Decide exception: APPROVE and REJECT require schedule.manage; CANCEL allows fallback to schedule.request.
	decideApprove, allowRequestFallback, err := resolveOperatingExceptionDecideAction([]byte(`{"decision":"APPROVE","meta":{"expectedRevision":1,"idempotencyKey":"dec-00000001","reason":"同意"}}`))
	if err != nil || decideApprove != registryauth.ActionScheduleManage || allowRequestFallback {
		t.Fatalf("APPROVE decide action=%q fallback=%v err=%v", decideApprove, allowRequestFallback, err)
	}
	decideCancel, allowRequestFallback, err := resolveOperatingExceptionDecideAction([]byte(`{"decision":"CANCEL","meta":{"expectedRevision":1,"idempotencyKey":"dec-00000002","reason":"取消"}}`))
	if err != nil || decideCancel != registryauth.ActionScheduleManage || !allowRequestFallback {
		t.Fatalf("CANCEL decide action=%q fallback=%v err=%v", decideCancel, allowRequestFallback, err)
	}
	if _, _, err := resolveOperatingExceptionDecideAction([]byte(`{"decision":"DELETE"}`)); err == nil {
		t.Fatal("invalid decision DELETE was accepted")
	}
}

func TestGatewayBlocksForgedActorDisplayHeaderAndForwardsBackendConflicts(t *testing.T) {
	h := NewHandler(Config{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sites/01990000-5000-7000-8000-000000000001/operating-exceptions", strings.NewReader(`{}`))
	req.Header.Set("X-Actor-Display-Name", "伪造主管")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("forged X-Actor-Display-Name status=%d body=%s", rec.Code, rec.Body.String())
	}

	for _, tc := range []struct {
		status int
		code   string
	}{
		{http.StatusBadRequest, "REGISTRY_MUTATION_INVALID"},
		{http.StatusBadRequest, "REGISTRY_QUERY_INVALID"},
		{http.StatusForbidden, "OPERATING_EXCEPTION_FORBIDDEN"},
		{http.StatusConflict, "REGISTRY_REVISION_CONFLICT"},
		{http.StatusConflict, "REGISTRY_BINDING_CONFLICT"},
		{http.StatusConflict, "OPERATING_EXCEPTION_STATE_CONFLICT"},
	} {
		resp := &http.Response{
			StatusCode: tc.status,
			Header:     http.Header{"Content-Type": []string{"application/problem+json"}},
			Body:       io.NopCloser(strings.NewReader(`{"code":"` + tc.code + `","detail":"refused"}`)),
		}
		out := httptest.NewRecorder()
		writeOperatingScheduleBackendResponse(out, httptest.NewRequest(http.MethodPost, "/api/v1/sites/x/operating-exceptions", nil), resp, 2<<20)
		var problem struct {
			Code   string `json:"code"`
			Status int    `json:"status"`
		}
		if err := json.Unmarshal(out.Body.Bytes(), &problem); err != nil || out.Code != tc.status || problem.Code != tc.code {
			t.Fatalf("passthrough %d %s -> got %d %s", tc.status, tc.code, out.Code, out.Body.String())
		}
	}
}
