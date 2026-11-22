package gateway

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
	"github.com/quanlaihe/hvac-web/modules/audit/pkg/auditserver"
)

func TestGatewayProvisioningProxyRoutesCSRFAndAudit(t *testing.T) {
	fix := newGatewayCredentialsFixture(t)
	now := time.Date(2026, 11, 23, 0, 30, 0, 0, time.UTC)
	snapshot, err := ownershipregistry.Parse([]byte(`{
		"registryVersion": 1,
		"registryRevision": 93,
		"routes": [
			{"method":"GET","path":"/api/v1/provisioning/batches","owner":"connectivity","publicIngress":"platform-gateway","revision":1,"rollout":{"mode":"all"},"compatibilityMode":"native","allowedScopeDimensions":["tenant","site","principal"]},
			{"method":"POST","path":"/api/v1/provisioning/batches","owner":"connectivity","publicIngress":"platform-gateway","revision":1,"rollout":{"mode":"all"},"compatibilityMode":"native","allowedScopeDimensions":["tenant","site","principal"]},
			{"method":"GET","path":"/api/v1/provisioning/batches/{batchId}","owner":"connectivity","publicIngress":"platform-gateway","revision":1,"rollout":{"mode":"all"},"compatibilityMode":"native","allowedScopeDimensions":["tenant","site","principal"]},
			{"method":"POST","path":"/api/v1/provisioning/batches/{batchId}/devices","owner":"connectivity","publicIngress":"platform-gateway","revision":1,"rollout":{"mode":"all"},"compatibilityMode":"native","allowedScopeDimensions":["tenant","site","principal"]},
			{"method":"POST","path":"/api/v1/provisioning/batches/{batchId}/revoke","owner":"connectivity","publicIngress":"platform-gateway","revision":1,"rollout":{"mode":"all"},"compatibilityMode":"native","allowedScopeDimensions":["tenant","site","principal"]}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	fix.handler.routeManager = ownershipregistry.NewManager(snapshot, ownershipregistry.NewMemoryAuditSink(), func() time.Time { return now })
	recorder := attachTestAuditHandler(t, fix.handler, fix.gatewaySigner, fix.now)

	const batchID = "0194f800-7000-7000-8000-000000000001"
	const oneTimeSecret = "super-secret-product-key-never-log-in-audit"
	var lastQuery string
	fix.handler.connectivity = &ConnectivityConfig{
		BaseURL: "https://connectivity.example.test",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			fix.upstreamCalls.Add(1)
			fix.lastUpstreamMethod = request.Method
			fix.lastUpstreamPath = request.URL.Path
			lastQuery = request.URL.RawQuery
			if request.Header.Get("X-Delegation-Grant") == "" {
				t.Fatal("missing X-Delegation-Grant header to Connectivity")
			}
			if request.Body != nil {
				raw, _ := io.ReadAll(request.Body)
				fix.lastUpstreamBody = string(raw)
			} else {
				fix.lastUpstreamBody = ""
			}
			respBody := `{"items":[]}`
			if request.Method == http.MethodPost && request.URL.Path == "/internal/v1/provisioning/batches" {
				respBody = `{"batch":{"id":"` + batchID + `","productKey":"THERM-V1"},"productSecret":"` + oneTimeSecret + `"}`
			} else if request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, batchID) {
				respBody = `{"batch":{"id":"` + batchID + `"},"devices":[]}`
			} else if request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/revoke") {
				respBody = `{"id":"` + batchID + `","status":"REVOKED"}`
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(respBody)),
			}, nil
		})},
	}

	// GET list forwards ?siteId= query and omits audit.
	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/provisioning/batches?siteId="+fix.siteID, nil)
	fix.authenticate(listReq, false)
	listRec := httptest.NewRecorder()
	fix.handler.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK || fix.lastUpstreamPath != "/internal/v1/provisioning/batches" || lastQuery != "siteId="+fix.siteID || listRec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("GET batches status=%d path=%q query=%q cache=%q", listRec.Code, fix.lastUpstreamPath, lastQuery, listRec.Header().Get("Cache-Control"))
	}

	// POST create without CSRF fails 403 before touching Connectivity.
	beforeConn := fix.upstreamCalls.Load()
	noCSRF := httptest.NewRequest(http.MethodPost, "/api/v1/provisioning/batches", strings.NewReader(`{"batchName":"B1","productKey":"THERM-V1"}`))
	fix.authenticate(noCSRF, false)
	noCSRFRec := httptest.NewRecorder()
	fix.handler.ServeHTTP(noCSRFRec, noCSRF)
	if noCSRFRec.Code != http.StatusForbidden || fix.upstreamCalls.Load() != beforeConn {
		t.Fatalf("POST without CSRF status=%d calls=%d->%d", noCSRFRec.Code, beforeConn, fix.upstreamCalls.Load())
	}

	// POST create with CSRF succeeds and records audit without leaking one-time productSecret.
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/provisioning/batches", strings.NewReader(`{"siteId":"`+fix.siteID+`","batchName":"B1","productKey":"THERM-V1"}`))
	fix.authenticate(createReq, true)
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	fix.handler.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusOK || createRec.Header().Get("Cache-Control") != "no-store" || !strings.Contains(createRec.Body.String(), oneTimeSecret) {
		t.Fatalf("POST create status=%d body=%s", createRec.Code, createRec.Body.String())
	}

	// GET detail, POST devices, POST revoke all proxy cleanly.
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/provisioning/batches/"+batchID, nil)
	fix.authenticate(getReq, false)
	getRec := httptest.NewRecorder()
	fix.handler.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK || fix.lastUpstreamPath != "/internal/v1/provisioning/batches/"+batchID {
		t.Fatalf("GET detail status=%d path=%q", getRec.Code, fix.lastUpstreamPath)
	}

	regReq := httptest.NewRequest(http.MethodPost, "/api/v1/provisioning/batches/"+batchID+"/devices", strings.NewReader(`{"devices":[]}`))
	fix.authenticate(regReq, true)
	regReq.Header.Set("Content-Type", "application/json")
	regRec := httptest.NewRecorder()
	fix.handler.ServeHTTP(regRec, regReq)
	if regRec.Code != http.StatusOK || fix.lastUpstreamPath != "/internal/v1/provisioning/batches/"+batchID+"/devices" {
		t.Fatalf("POST devices status=%d path=%q", regRec.Code, fix.lastUpstreamPath)
	}

	revReq := httptest.NewRequest(http.MethodPost, "/api/v1/provisioning/batches/"+batchID+"/revoke", nil)
	fix.authenticate(revReq, true)
	revRec := httptest.NewRecorder()
	fix.handler.ServeHTTP(revRec, revReq)
	if revRec.Code != http.StatusOK || fix.lastUpstreamPath != "/internal/v1/provisioning/batches/"+batchID+"/revoke" {
		t.Fatalf("POST revoke status=%d path=%q", revRec.Code, fix.lastUpstreamPath)
	}

	events, payloads := recorder.snapshot()
	if len(events) != 3 {
		t.Fatalf("expected 3 audit events for write operations, got %d", len(events))
	}
	if events[0].Category != auditserver.AggregateTypeGatewayCredential || events[0].Action != "JITP_BATCH_CREATED" || events[0].TargetID != "provisioning:batch:"+batchID {
		t.Fatalf("unexpected create audit event: %#v", events[0])
	}
	for _, p := range payloads {
		if bytes.Contains(p, []byte(oneTimeSecret)) {
			t.Fatalf("one-time productSecret leaked into domain audit payload: %s", p)
		}
	}

	if got := safeLogPath("/api/v1/provisioning/batches/" + batchID + "/devices"); got != "/api/v1/provisioning/batches/{batchId}/devices" {
		t.Fatalf("safeLogPath=%q", got)
	}
}
