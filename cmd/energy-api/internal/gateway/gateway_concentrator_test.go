package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/limitpolicy"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

type concentratorGatewayFixture struct {
	*commandGatewayFixture
	gatewayID               string
	upstreamCalls           atomic.Int32
	upstreamStatus          int
	upstreamBody            string
	upstreamErr             error
	lastUpstreamPath        string
	lastUpstreamMethod      string
	lastUpstreamBody        string
	lastUpstreamTraceparent string
}

func newConcentratorGatewayFixture(t *testing.T) *concentratorGatewayFixture {
	t.Helper()
	base := newCommandGatewayFixture(t)
	now := time.Date(2026, 7, 26, 15, 0, 0, 0, time.UTC)
	snapshot, err := ownershipregistry.Parse([]byte(`{
		"registryVersion": 1,
		"registryRevision": 90,
		"routes": [
			{"method":"GET","path":"/api/v1/gateways/{gatewayId}/concentrators/{unitId}/status","owner":"connectivity","publicIngress":"platform-gateway","revision":1,"rollout":{"mode":"all"},"compatibilityMode":"native","allowedScopeDimensions":["tenant","site"]},
			{"method":"PUT","path":"/api/v1/gateways/{gatewayId}/concentrators/{unitId}/params","owner":"connectivity","publicIngress":"platform-gateway","revision":1,"rollout":{"mode":"all"},"compatibilityMode":"native","allowedScopeDimensions":["tenant","site"]},
			{"method":"GET","path":"/api/v1/gateways/{gatewayId}/concentrators/{unitId}/eids","owner":"connectivity","publicIngress":"platform-gateway","revision":1,"rollout":{"mode":"all"},"compatibilityMode":"native","allowedScopeDimensions":["tenant","site"]},
			{"method":"PUT","path":"/api/v1/gateways/{gatewayId}/concentrators/{unitId}/eids","owner":"connectivity","publicIngress":"platform-gateway","revision":1,"rollout":{"mode":"all"},"compatibilityMode":"native","allowedScopeDimensions":["tenant","site"]}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	base.handler.routeManager = ownershipregistry.NewManager(snapshot, ownershipregistry.NewMemoryAuditSink(), func() time.Time { return now })

	fix := &concentratorGatewayFixture{
		commandGatewayFixture: base,
		gatewayID:             "018f3d00-2000-7000-8000-000000000001",
		upstreamStatus:        http.StatusOK,
		upstreamBody:          `{"gatewayId":"018f3d00-2000-7000-8000-000000000001","unitId":1,"ok":true}`,
	}
	base.handler.identity.config.IAMHTTPClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		base.iamCalls.Add(1)
		parent, err := identitycontext.VerifyDelegation(&base.gatewaySigner.PublicKey, request.Header.Get("X-Delegation-Grant"))
		if err != nil {
			return nil, err
		}
		var input registryauth.DecisionRequest
		if json.NewDecoder(request.Body).Decode(&input) != nil {
			t.Fatal("invalid Registry decision request")
		}
		if input.Action != registryauth.ActionDeviceRead && input.Action != registryauth.ActionDeviceWrite {
			t.Fatalf("unexpected Registry action %q", input.Action)
		}
		claims := registryauth.GrantClaims{
			Issuer: "spiffe://hvac.local/iam-service", Presenter: "spiffe://hvac.local/platform-gateway", Audience: "platform-core-service",
			PrincipalID: base.principalID, SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject,
			TenantID: base.tenantID, AllowedSiteIDs: []string{base.siteID},
			Actions: []registryauth.Action{input.Action}, PolicyRevision: "registry-read:1/iam:1",
			DecisionReason: registryauth.ReasonAllowSiteRole, SessionID: parent.SessionID, ParentTokenID: parent.TokenID,
			IssuedAt: now.Unix(), ExpiresAt: now.Add(30 * time.Second).Unix(), TokenID: randomURLToken(16),
		}
		decision := registryauth.Decision{
			Allowed: true, PrincipalID: base.principalID, SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject,
			TenantID: base.tenantID, AllowedSiteIDs: []string{base.siteID},
			Actions: []registryauth.Action{input.Action}, PolicyRevision: "registry-read:1/iam:1",
			ReasonCode: registryauth.ReasonAllowSiteRole, DecidedAt: now.Format(time.RFC3339Nano),
		}
		return telemetryJSONResponse(http.StatusOK, registryauth.DecisionResponse{Decision: decision, DelegationGrant: commandUnsignedRegistryGrant(claims)}), nil
	})}
	base.handler.connectivity = &ConnectivityConfig{
		BaseURL: "https://connectivity.example.test",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			fix.upstreamCalls.Add(1)
			fix.lastUpstreamMethod = request.Method
			fix.lastUpstreamPath = request.URL.Path
			fix.lastUpstreamTraceparent = request.Header.Get("traceparent")
			if request.Header.Get("X-Delegation-Grant") == "" {
				t.Fatal("missing X-Delegation-Grant header to Connectivity")
			}
			if request.Body != nil {
				raw, _ := io.ReadAll(request.Body)
				fix.lastUpstreamBody = string(raw)
			}
			if fix.upstreamErr != nil {
				return nil, fix.upstreamErr
			}
			return &http.Response{
				StatusCode: fix.upstreamStatus,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(fix.upstreamBody)),
			}, nil
		})},
	}
	return fix
}

func TestGatewayConcentratorProxyEndpoints(t *testing.T) {
	fix := newConcentratorGatewayFixture(t)

	for _, tc := range []struct {
		name     string
		method   string
		op       string
		body     string
		wantPath string
	}{
		{"read status", http.MethodGet, "status", "", "/internal/v1/gateways/" + fix.gatewayID + "/concentrators/1/status"},
		{"write params", http.MethodPut, "params", `{"params":{"rfTxPower":20},"reason":"tune tx power"}`, "/internal/v1/gateways/" + fix.gatewayID + "/concentrators/1/params"},
		{"read eids", http.MethodGet, "eids", "", "/internal/v1/gateways/" + fix.gatewayID + "/concentrators/1/eids"},
		{"write eids", http.MethodPut, "eids", `{"slots":[{"slot":1,"eid":33661717}],"reason":"bind slot 1"}`, "/internal/v1/gateways/" + fix.gatewayID + "/concentrators/1/eids"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var bodyReader io.Reader
			if tc.body != "" {
				bodyReader = strings.NewReader(tc.body)
			}
			req := httptest.NewRequest(tc.method, "/api/v1/gateways/"+fix.gatewayID+"/concentrators/1/"+tc.op, bodyReader)
			fix.authenticate(req, tc.method == http.MethodPut)
			if tc.method == http.MethodPut {
				req.Header.Set("Content-Type", "application/json")
			}
			rec := httptest.NewRecorder()
			fix.handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			if rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("expected Cache-Control: no-store, got %q", rec.Header().Get("Cache-Control"))
			}
			if fix.lastUpstreamMethod != tc.method || fix.lastUpstreamPath != tc.wantPath {
				t.Fatalf("upstream got %s %s, want %s %s", fix.lastUpstreamMethod, fix.lastUpstreamPath, tc.method, tc.wantPath)
			}
		})
	}
}

func TestGatewayConcentratorWriteRequiresCSRFBeforeUpstream(t *testing.T) {
	fix := newConcentratorGatewayFixture(t)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/gateways/"+fix.gatewayID+"/concentrators/1/params", strings.NewReader(`{"params":{"rfTxPower":20},"reason":"test"}`))
	fix.authenticate(req, false)
	rec := httptest.NewRecorder()
	fix.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if fix.iamCalls.Load() != 0 || fix.upstreamCalls.Load() != 0 {
		t.Fatalf("expected 0 upstream calls without CSRF, got iam=%d conn=%d", fix.iamCalls.Load(), fix.upstreamCalls.Load())
	}
}

func TestGatewayConcentratorMapsUpstreamProblemAndTimeouts(t *testing.T) {
	fix := newConcentratorGatewayFixture(t)
	fix.upstreamStatus = http.StatusConflict
	fix.upstreamBody = `{"code":"CONCENTRATOR_UID_PROTECTED","message":"unitId 2 is protected on RS485 bus"}`
	req := httptest.NewRequest(http.MethodGet, "/api/v1/gateways/"+fix.gatewayID+"/concentrators/2/status", nil)
	fix.authenticate(req, false)
	rec := httptest.NewRecorder()
	fix.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Header().Get("Content-Type"), "application/problem+json") {
		t.Fatalf("expected problem+json, got %q", rec.Header().Get("Content-Type"))
	}
	var problem map[string]any
	if json.NewDecoder(rec.Body).Decode(&problem) != nil || problem["code"] != "CONCENTRATOR_UID_PROTECTED" || problem["retryable"] != false {
		t.Fatalf("unexpected problem body: %#v", problem)
	}

	fix.upstreamErr = context.DeadlineExceeded
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/gateways/"+fix.gatewayID+"/concentrators/1/status", nil)
	fix.authenticate(req2, false)
	rec2 := httptest.NewRecorder()
	fix.handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusGatewayTimeout {
		t.Fatalf("status=%d body=%s", rec2.Code, rec2.Body.String())
	}
	var problem2 map[string]any
	if json.NewDecoder(rec2.Body).Decode(&problem2) != nil || problem2["code"] != "CONCENTRATOR_RPC_TIMEOUT" || problem2["retryable"] != true {
		t.Fatalf("unexpected timeout problem body: %#v", problem2)
	}

	fix.upstreamErr = nil
	fix.upstreamCalls.Store(0)
	oversized := bytes.Repeat([]byte("a"), (128<<10)+16)
	req3 := httptest.NewRequest(http.MethodPut, "/api/v1/gateways/"+fix.gatewayID+"/concentrators/1/eids", bytes.NewReader(oversized))
	fix.authenticate(req3, true)
	rec3 := httptest.NewRecorder()
	fix.handler.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusRequestEntityTooLarge || fix.upstreamCalls.Load() != 0 {
		t.Fatalf("oversized status=%d calls=%d", rec3.Code, fix.upstreamCalls.Load())
	}
}

func TestGatewayConcentratorRejectsMissingConnectivityConfig(t *testing.T) {
	fix := newConcentratorGatewayFixture(t)
	fix.handler.connectivity = nil
	req := httptest.NewRequest(http.MethodGet, "/api/v1/gateways/"+fix.gatewayID+"/concentrators/1/status", nil)
	fix.authenticate(req, false)
	rec := httptest.NewRecorder()
	fix.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "CONNECTIVITY_UNAVAILABLE") {
		t.Fatalf("expected CONNECTIVITY_UNAVAILABLE, got %s", rec.Body.String())
	}
}

func TestGatewayConcentratorRejectsDotSegmentsWithoutUpstreamCall(t *testing.T) {
	fix := newConcentratorGatewayFixture(t)
	cases := []struct {
		name   string
		method string
		path   string
		csrf   bool
		body   string
	}{
		{name: "gateway dot", method: http.MethodGet, path: "/api/v1/gateways/./concentrators/1/status"},
		{name: "gateway dotdot", method: http.MethodGet, path: "/api/v1/gateways/../concentrators/1/eids"},
		{name: "unit dot", method: http.MethodPut, path: "/api/v1/gateways/" + fix.gatewayID + "/concentrators/./params", csrf: true, body: `{"reason":"x"}`},
		{name: "unit dotdot", method: http.MethodPut, path: "/api/v1/gateways/" + fix.gatewayID + "/concentrators/../eids", csrf: true, body: `{"reason":"x","slots":[]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var reqBody io.Reader
			if tc.body != "" {
				reqBody = strings.NewReader(tc.body)
			}
			req := httptest.NewRequest(tc.method, tc.path, reqBody)
			fix.authenticate(req, tc.csrf)
			rec := httptest.NewRecorder()
			fix.handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "CONCENTRATOR_REQUEST_INVALID") {
				t.Fatalf("expected 400 CONCENTRATOR_REQUEST_INVALID, got %d body=%s", rec.Code, rec.Body.String())
			}
			if fix.upstreamCalls.Load() != 0 || fix.iamCalls.Load() != 0 {
				t.Fatalf("expected 0 upstream and IAM calls, got upstream=%d iam=%d", fix.upstreamCalls.Load(), fix.iamCalls.Load())
			}
		})
	}
}

func TestGatewayConcentratorExtendsWriteDeadlineBeyondServerTimeout(t *testing.T) {
	fix := newConcentratorGatewayFixture(t)
	fix.handler.connectivity.HTTPClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		fix.upstreamCalls.Add(1)
		time.Sleep(80 * time.Millisecond)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(fix.upstreamBody)),
		}, nil
	})}

	srv := httptest.NewUnstartedServer(fix.handler)
	srv.Config.WriteTimeout = 25 * time.Millisecond
	srv.Start()
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/gateways/"+fix.gatewayID+"/concentrators/1/status", nil)
	if err != nil {
		t.Fatal(err)
	}
	fix.authenticate(req, false)

	resp, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("expected extended write deadline to finish 80ms upstream call under 25ms WriteTimeout, got err=%v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != fix.upstreamBody {
		t.Fatalf("status=%d body=%s", resp.StatusCode, string(body))
	}
}

func TestGatewayConcentratorRateLimitAndSafeLogPath(t *testing.T) {
	fix := newConcentratorGatewayFixture(t)
	fix.handler.rateLimiter = limitpolicy.NewLimiter(limitpolicy.NewMemoryCounter(1000), &limitpolicy.Policy{
		Version: 1,
		Limits: []limitpolicy.Limit{
			{Dimension: limitpolicy.DimensionRest, Window: time.Minute, Burst: 100},
			{Dimension: limitpolicy.DimensionCommandWrite, Window: time.Minute, Burst: 1, FailClosed: true},
		},
	})

	req1 := httptest.NewRequest(http.MethodPut, "/api/v1/gateways/"+fix.gatewayID+"/concentrators/1/params", strings.NewReader(`{"reason":"tune"}`))
	req1.RemoteAddr = "10.0.0.1:1001"
	fix.authenticate(req1, true)
	rec1 := httptest.NewRecorder()
	fix.handler.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first write expected 200, got %d body=%s", rec1.Code, rec1.Body.String())
	}

	req2 := httptest.NewRequest(http.MethodPut, "/api/v1/gateways/"+fix.gatewayID+"/concentrators/1/params", strings.NewReader(`{"reason":"tune"}`))
	req2.RemoteAddr = "10.0.0.2:1002"
	fix.authenticate(req2, true)
	rec2 := httptest.NewRecorder()
	fix.handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("second tenant write expected 429, got %d body=%s", rec2.Code, rec2.Body.String())
	}

	readFix := newConcentratorGatewayFixture(t)
	readFix.handler.rateLimiter = limitpolicy.NewLimiter(limitpolicy.NewMemoryCounter(1000), &limitpolicy.Policy{
		Version: 1,
		Limits: []limitpolicy.Limit{
			{Dimension: limitpolicy.DimensionRest, Window: time.Minute, Burst: 1},
		},
	})
	readReq1 := httptest.NewRequest(http.MethodGet, "/api/v1/gateways/"+readFix.gatewayID+"/concentrators/1/status", nil)
	readReq1.RemoteAddr = "10.0.1.1:1001"
	readFix.authenticate(readReq1, false)
	readRec1 := httptest.NewRecorder()
	readFix.handler.ServeHTTP(readRec1, readReq1)
	if readRec1.Code != http.StatusOK {
		t.Fatalf("first read expected 200, got %d body=%s", readRec1.Code, readRec1.Body.String())
	}
	readReq2 := httptest.NewRequest(http.MethodGet, "/api/v1/gateways/"+readFix.gatewayID+"/concentrators/1/status", nil)
	readReq2.RemoteAddr = "10.0.1.2:1002"
	readFix.authenticate(readReq2, false)
	readRec2 := httptest.NewRecorder()
	readFix.handler.ServeHTTP(readRec2, readReq2)
	if readRec2.Code != http.StatusTooManyRequests {
		t.Fatalf("second tenant read expected 429, got %d body=%s", readRec2.Code, readRec2.Body.String())
	}

	for path, want := range map[string]string{
		"/api/v1/gateways/" + fix.gatewayID + "/concentrators/1/status": "/api/v1/gateways/{gatewayId}/concentrators/{unitId}/status",
		"/api/v1/gateways/" + fix.gatewayID + "/concentrators/1/params": "/api/v1/gateways/{gatewayId}/concentrators/{unitId}/params",
		"/api/v1/gateways/" + fix.gatewayID + "/concentrators/1/eids":   "/api/v1/gateways/{gatewayId}/concentrators/{unitId}/eids",
	} {
		if got := safeLogPath(path); got != want {
			t.Fatalf("safeLogPath(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestGatewayConcentratorPropagatesTraceparent(t *testing.T) {
	fix := newConcentratorGatewayFixture(t)
	const wantTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	req := httptest.NewRequest(http.MethodGet, "/api/v1/gateways/"+fix.gatewayID+"/concentrators/1/status", nil)
	fix.authenticate(req, false)
	req.Header.Set("traceparent", "00-"+wantTraceID+"-00f067aa0ba902b7-01")
	rec := httptest.NewRecorder()
	fix.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	parsed, ok := observability.ParseTraceparent(fix.lastUpstreamTraceparent, "")
	if !ok || parsed.TraceID != wantTraceID {
		t.Fatalf("connectivity traceparent=%q (ok=%t), want traceId=%s", fix.lastUpstreamTraceparent, ok, wantTraceID)
	}
}
