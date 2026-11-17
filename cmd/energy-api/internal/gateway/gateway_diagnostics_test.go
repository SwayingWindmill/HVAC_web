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
	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

type diagnosticsGatewayFixture struct {
	*commandGatewayFixture
	gatewayID               string
	upstreamCalls           atomic.Int32
	upstreamStatus          int
	upstreamBody            string
	upstreamErr             error
	lastUpstreamMethod      string
	lastUpstreamPath        string
	lastUpstreamRawQuery    string
	lastUpstreamContentType string
	lastUpstreamBody        string
	lastAuthAction          registryauth.Action
}

func newDiagnosticsGatewayFixture(t *testing.T) *diagnosticsGatewayFixture {
	t.Helper()
	base := newCommandGatewayFixture(t)
	now := time.Date(2026, 11, 17, 3, 0, 0, 0, time.UTC)
	snapshot, err := ownershipregistry.Parse([]byte(`{
		"registryVersion": 1,
		"registryRevision": 92,
		"routes": [
			{"method":"GET","path":"/api/v1/gateways/{gatewayId}/quarantine","owner":"connectivity","publicIngress":"platform-gateway","revision":1,"rollout":{"mode":"all"},"compatibilityMode":"native","allowedScopeDimensions":["tenant","site","principal"]},
			{"method":"GET","path":"/api/v1/gateways/{gatewayId}/packet-capture","owner":"connectivity","publicIngress":"platform-gateway","revision":1,"rollout":{"mode":"all"},"compatibilityMode":"native","allowedScopeDimensions":["tenant","site","principal"]},
			{"method":"POST","path":"/api/v1/gateways/{gatewayId}/packet-capture/start","owner":"connectivity","publicIngress":"platform-gateway","revision":1,"rollout":{"mode":"all"},"compatibilityMode":"native","allowedScopeDimensions":["tenant","site","principal"]},
			{"method":"POST","path":"/api/v1/gateways/{gatewayId}/packet-capture/stop","owner":"connectivity","publicIngress":"platform-gateway","revision":1,"rollout":{"mode":"all"},"compatibilityMode":"native","allowedScopeDimensions":["tenant","site","principal"]}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	base.handler.routeManager = ownershipregistry.NewManager(snapshot, ownershipregistry.NewMemoryAuditSink(), func() time.Time { return now })
	fix := &diagnosticsGatewayFixture{
		commandGatewayFixture: base,
		gatewayID:             "018f3d00-2000-7000-8000-000000000001",
		upstreamStatus:        http.StatusOK,
		upstreamBody:          `{"items":[]}`,
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
		fix.lastAuthAction = input.Action
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
			fix.lastUpstreamRawQuery = request.URL.RawQuery
			fix.lastUpstreamContentType = request.Header.Get("Content-Type")
			if request.Header.Get("X-Delegation-Grant") == "" {
				t.Fatal("missing X-Delegation-Grant")
			}
			fix.lastUpstreamBody = ""
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

func TestGatewayDiagnosticsProxyEndpoints(t *testing.T) {
	fix := newDiagnosticsGatewayFixture(t)

	qReq := httptest.NewRequest(http.MethodGet, "/api/v1/gateways/"+fix.gatewayID+"/quarantine?limit=15", nil)
	fix.authenticate(qReq, false)
	qRec := httptest.NewRecorder()
	fix.handler.ServeHTTP(qRec, qReq)
	if qRec.Code != http.StatusOK || fix.lastUpstreamMethod != http.MethodGet ||
		fix.lastUpstreamPath != "/internal/v1/gateways/"+fix.gatewayID+"/quarantine" ||
		fix.lastUpstreamRawQuery != "limit=15" || fix.lastAuthAction != registryauth.ActionDeviceRead {
		t.Fatalf("GET quarantine status=%d method=%s path=%s query=%q action=%s body=%s",
			qRec.Code, fix.lastUpstreamMethod, fix.lastUpstreamPath, fix.lastUpstreamRawQuery, fix.lastAuthAction, qRec.Body.String())
	}
	if qRec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("expected Cache-Control: no-store, got %q", qRec.Header().Get("Cache-Control"))
	}

	fix.upstreamBody = `{"gatewayId":"` + fix.gatewayID + `","active":false,"maxPackets":200,"droppedCount":0,"items":[]}`
	pcReq := httptest.NewRequest(http.MethodGet, "/api/v1/gateways/"+fix.gatewayID+"/packet-capture", nil)
	fix.authenticate(pcReq, false)
	pcRec := httptest.NewRecorder()
	fix.handler.ServeHTTP(pcRec, pcReq)
	if pcRec.Code != http.StatusOK || fix.lastUpstreamMethod != http.MethodGet ||
		fix.lastUpstreamPath != "/internal/v1/gateways/"+fix.gatewayID+"/packet-capture" ||
		fix.lastAuthAction != registryauth.ActionDeviceRead {
		t.Fatalf("GET packet-capture status=%d method=%s path=%s body=%s",
			pcRec.Code, fix.lastUpstreamMethod, fix.lastUpstreamPath, pcRec.Body.String())
	}

	startReq := httptest.NewRequest(http.MethodPost, "/api/v1/gateways/"+fix.gatewayID+"/packet-capture/start", strings.NewReader(`{"durationSeconds":120}`))
	fix.authenticate(startReq, true)
	startRec := httptest.NewRecorder()
	fix.handler.ServeHTTP(startRec, startReq)
	if startRec.Code != http.StatusOK || fix.lastUpstreamMethod != http.MethodPost ||
		fix.lastUpstreamPath != "/internal/v1/gateways/"+fix.gatewayID+"/packet-capture/start" ||
		fix.lastUpstreamBody != `{"durationSeconds":120}` ||
		fix.lastUpstreamContentType != "application/json" ||
		fix.lastAuthAction != registryauth.ActionDeviceWrite {
		t.Fatalf("POST packet-capture/start status=%d method=%s path=%s ct=%q body=%q resp=%s",
			startRec.Code, fix.lastUpstreamMethod, fix.lastUpstreamPath, fix.lastUpstreamContentType, fix.lastUpstreamBody, startRec.Body.String())
	}

	startEmptyReq := httptest.NewRequest(http.MethodPost, "/api/v1/gateways/"+fix.gatewayID+"/packet-capture/start", nil)
	fix.authenticate(startEmptyReq, true)
	startEmptyRec := httptest.NewRecorder()
	fix.handler.ServeHTTP(startEmptyRec, startEmptyReq)
	if startEmptyRec.Code != http.StatusOK || fix.lastUpstreamBody != "" {
		t.Fatalf("POST packet-capture/start empty status=%d body=%q resp=%s",
			startEmptyRec.Code, fix.lastUpstreamBody, startEmptyRec.Body.String())
	}

	stopReq := httptest.NewRequest(http.MethodPost, "/api/v1/gateways/"+fix.gatewayID+"/packet-capture/stop", nil)
	fix.authenticate(stopReq, true)
	stopRec := httptest.NewRecorder()
	fix.handler.ServeHTTP(stopRec, stopReq)
	if stopRec.Code != http.StatusOK || fix.lastUpstreamMethod != http.MethodPost ||
		fix.lastUpstreamPath != "/internal/v1/gateways/"+fix.gatewayID+"/packet-capture/stop" ||
		fix.lastAuthAction != registryauth.ActionDeviceWrite {
		t.Fatalf("POST packet-capture/stop status=%d method=%s path=%s resp=%s",
			stopRec.Code, fix.lastUpstreamMethod, fix.lastUpstreamPath, stopRec.Body.String())
	}
}

func TestGatewayDiagnosticsCSRFBodyBoundsAndUpstreamErrors(t *testing.T) {
	fix := newDiagnosticsGatewayFixture(t)

	for _, suffix := range []string{"/packet-capture/start", "/packet-capture/stop"} {
		fix.iamCalls.Store(0)
		fix.upstreamCalls.Store(0)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/gateways/"+fix.gatewayID+suffix, nil)
		fix.authenticate(req, false)
		rec := httptest.NewRecorder()
		fix.handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden || fix.iamCalls.Load() != 0 || fix.upstreamCalls.Load() != 0 {
			t.Fatalf("%s without CSRF status=%d iam=%d upstream=%d", suffix, rec.Code, fix.iamCalls.Load(), fix.upstreamCalls.Load())
		}
	}

	fix.upstreamCalls.Store(0)
	oversized := bytes.Repeat([]byte("a"), (64<<10)+16)
	overReq := httptest.NewRequest(http.MethodPost, "/api/v1/gateways/"+fix.gatewayID+"/packet-capture/start", bytes.NewReader(oversized))
	fix.authenticate(overReq, true)
	overRec := httptest.NewRecorder()
	fix.handler.ServeHTTP(overRec, overReq)
	if overRec.Code != http.StatusRequestEntityTooLarge || fix.upstreamCalls.Load() != 0 || !strings.Contains(overRec.Body.String(), "PAYLOAD_TOO_LARGE") {
		t.Fatalf("oversized status=%d calls=%d body=%s", overRec.Code, fix.upstreamCalls.Load(), overRec.Body.String())
	}

	for _, tc := range []struct {
		status  int
		code    string
		message string
	}{
		{http.StatusForbidden, "CREDENTIAL_FORBIDDEN", "gateway belongs to another site"},
		{http.StatusTooManyRequests, "DIAGNOSTICS_RATE_LIMITED", "too many diagnostics calls"},
	} {
		fix.upstreamStatus = tc.status
		fix.upstreamBody = `{"code":"` + tc.code + `","message":"` + tc.message + `"}`
		req := httptest.NewRequest(http.MethodGet, "/api/v1/gateways/"+fix.gatewayID+"/quarantine", nil)
		fix.authenticate(req, false)
		rec := httptest.NewRecorder()
		fix.handler.ServeHTTP(rec, req)
		if rec.Code != tc.status || !strings.Contains(rec.Header().Get("Content-Type"), "application/problem+json") {
			t.Fatalf("expected %d ProblemDetails, got %d (%s)", tc.status, rec.Code, rec.Body.String())
		}
		var problem map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil || problem["code"] != tc.code || problem["detail"] != tc.message || problem["retryable"] != false {
			t.Fatalf("unexpected problem payload: %s", rec.Body.String())
		}
	}

	for _, status5xx := range []int{http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable} {
		fix.upstreamStatus = status5xx
		fix.upstreamBody = `{"code":"DIAGNOSTICS_INTERNAL","message":"pq: leaked table connectivity.uplink_quarantine"}`
		req := httptest.NewRequest(http.MethodGet, "/api/v1/gateways/"+fix.gatewayID+"/packet-capture", nil)
		fix.authenticate(req, false)
		rec := httptest.NewRecorder()
		fix.handler.ServeHTTP(rec, req)
		if rec.Code != status5xx || strings.Contains(rec.Body.String(), "leaked") || !strings.Contains(rec.Body.String(), "Gateway diagnostics service is unavailable.") {
			t.Fatalf("expected masked %d ProblemDetails, got %d body=%s", status5xx, rec.Code, rec.Body.String())
		}
	}

	fix.upstreamErr = context.DeadlineExceeded
	timeoutReq := httptest.NewRequest(http.MethodGet, "/api/v1/gateways/"+fix.gatewayID+"/quarantine", nil)
	fix.authenticate(timeoutReq, false)
	timeoutRec := httptest.NewRecorder()
	fix.handler.ServeHTTP(timeoutRec, timeoutReq)
	if timeoutRec.Code != http.StatusGatewayTimeout || !strings.Contains(timeoutRec.Body.String(), "CONNECTIVITY_TIMEOUT") {
		t.Fatalf("expected 504 CONNECTIVITY_TIMEOUT, got %d body=%s", timeoutRec.Code, timeoutRec.Body.String())
	}

	fix.upstreamErr = nil
	fix.iamCalls.Store(0)
	fix.upstreamCalls.Store(0)
	dotReq := httptest.NewRequest(http.MethodGet, "/api/v1/gateways/../quarantine", nil)
	fix.authenticate(dotReq, false)
	dotRec := httptest.NewRecorder()
	fix.handler.ServeHTTP(dotRec, dotReq)
	if dotRec.Code != http.StatusBadRequest || fix.iamCalls.Load() != 0 || fix.upstreamCalls.Load() != 0 {
		t.Fatalf("dot segment status=%d iam=%d upstream=%d body=%s", dotRec.Code, fix.iamCalls.Load(), fix.upstreamCalls.Load(), dotRec.Body.String())
	}
}

func TestGatewayDiagnosticsRateLimitAndSafeLogPath(t *testing.T) {
	fix := newDiagnosticsGatewayFixture(t)
	fix.handler.rateLimiter = limitpolicy.NewLimiter(limitpolicy.NewMemoryCounter(1000), &limitpolicy.Policy{
		Version: 1,
		Limits: []limitpolicy.Limit{
			{Dimension: limitpolicy.DimensionRest, Window: time.Minute, Burst: 100},
			{Dimension: limitpolicy.DimensionCommandWrite, Window: time.Minute, Burst: 1, FailClosed: true},
		},
	})

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/gateways/"+fix.gatewayID+"/packet-capture/start", strings.NewReader(`{"durationSeconds":60}`))
		req.RemoteAddr = "10.0.0.1:1001"
		fix.authenticate(req, true)
		rec := httptest.NewRecorder()
		fix.handler.ServeHTTP(rec, req)
		if i == 0 && rec.Code != http.StatusOK {
			t.Fatalf("first POST start expected 200, got %d body=%s", rec.Code, rec.Body.String())
		}
		if i == 1 && rec.Code != http.StatusTooManyRequests {
			t.Fatalf("second POST start expected 429, got %d body=%s", rec.Code, rec.Body.String())
		}
	}

	for path, want := range map[string]string{
		"/api/v1/gateways/" + fix.gatewayID + "/quarantine":           "/api/v1/gateways/{gatewayId}/quarantine",
		"/api/v1/gateways/" + fix.gatewayID + "/packet-capture":       "/api/v1/gateways/{gatewayId}/packet-capture",
		"/api/v1/gateways/" + fix.gatewayID + "/packet-capture/start": "/api/v1/gateways/{gatewayId}/packet-capture/start",
		"/api/v1/gateways/" + fix.gatewayID + "/packet-capture/stop":  "/api/v1/gateways/{gatewayId}/packet-capture/stop",
	} {
		if got := safeLogPath(path); got != want {
			t.Fatalf("safeLogPath(%q) = %q, want %q", path, got, want)
		}
	}
}
