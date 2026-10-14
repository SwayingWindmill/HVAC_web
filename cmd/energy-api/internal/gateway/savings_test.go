package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSavingsRoutesNameTheOwnerPathAndWhetherTheyWrite(t *testing.T) {
	siteID := "01990000-5000-7000-8000-000000000001"
	measureID := "01990000-7000-7000-8000-000000000001"
	base := "/api/v1/sites/" + siteID + "/savings"
	for _, test := range []struct {
		method, path, internalPath string
		write                      bool
	}{
		{http.MethodGet, base + "/measures", "/measures", false},
		{http.MethodPost, base + "/measures", "/measures", true},
		{http.MethodGet, base + "/measures/" + measureID, "/measures/" + measureID, false},
		{http.MethodPost, base + "/measures/" + measureID + "/implement", "/measures/" + measureID + "/implement", true},
		{http.MethodPost, base + "/measures/" + measureID + "/refit", "/measures/" + measureID + "/refit", true},
		{http.MethodPost, base + "/measures/" + measureID + "/end", "/measures/" + measureID + "/end", true},
		{http.MethodGet, base + "/summary", "/summary", false},
	} {
		route, ok := matchPublicSavingsRoute(test.method, test.path)
		if !ok || route.siteID != siteID || route.internalPath != test.internalPath || route.write != test.write {
			t.Fatalf("%s %s resolved as %#v ok=%t", test.method, test.path, route, ok)
		}
	}
	for _, test := range []struct{ method, path string }{
		{http.MethodPost, base + "/summary"},
		{http.MethodPost, base + "/measures/" + measureID},
		{http.MethodGet, base + "/measures/" + measureID + "/implement"},
		{http.MethodPost, base + "/measures/" + measureID + "/approve"},
		{http.MethodGet, base + "/measures/"},
	} {
		if _, ok := matchPublicSavingsRoute(test.method, test.path); ok {
			t.Fatalf("%s %s unexpectedly matched a savings route", test.method, test.path)
		}
	}
}

func TestSavingsRefusalsReachTheOperator(t *testing.T) {
	h := &handler{}
	recorder := httptest.NewRecorder()
	h.writeSavingsResult(recorder, httptest.NewRequest(http.MethodPost, "/", nil), []byte(`{"code":"savings_reporting_overlap","status":409}`), http.StatusConflict, nil)
	var problem struct {
		Code   string `json:"code"`
		Status int    `json:"status"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil || recorder.Code != http.StatusConflict || problem.Code != "SAVINGS_REPORTING_OVERLAP" {
		t.Fatalf("refusal = %d %s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	h.writeSavingsResult(recorder, httptest.NewRequest(http.MethodGet, "/", nil), nil, 0, errors.New("dial tcp: refused"))
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("an unreachable owner = %d", recorder.Code)
	}
}
