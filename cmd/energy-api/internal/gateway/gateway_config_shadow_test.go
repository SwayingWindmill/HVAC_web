package gateway

import (
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

type configShadowGatewayFixture struct {
	*commandGatewayFixture
	gatewayID          string
	upstreamCalls      atomic.Int32
	upstreamStatus     int
	upstreamBody       string
	lastUpstreamMethod string
	lastUpstreamPath   string
	lastUpstreamBody   string
}

func newConfigShadowGatewayFixture(t *testing.T) *configShadowGatewayFixture {
	t.Helper()
	base := newCommandGatewayFixture(t)
	now := time.Date(2026, 11, 17, 2, 0, 0, 0, time.UTC)
	snapshot, err := ownershipregistry.Parse([]byte(`{
		"registryVersion": 1,
		"registryRevision": 91,
		"routes": [
			{"method":"GET","path":"/api/v1/gateways/{gatewayId}/config-shadow","owner":"connectivity","publicIngress":"platform-gateway","revision":1,"rollout":{"mode":"all"},"compatibilityMode":"native","allowedScopeDimensions":["tenant","site","principal"]},
			{"method":"PUT","path":"/api/v1/gateways/{gatewayId}/config-shadow","owner":"connectivity","publicIngress":"platform-gateway","revision":1,"rollout":{"mode":"all"},"compatibilityMode":"native","allowedScopeDimensions":["tenant","site","principal"]}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	base.handler.routeManager = ownershipregistry.NewManager(snapshot, ownershipregistry.NewMemoryAuditSink(), func() time.Time { return now })
	fix := &configShadowGatewayFixture{
		commandGatewayFixture: base,
		gatewayID:             "018f3d00-2000-7000-8000-000000000001",
		upstreamStatus:        http.StatusOK,
		upstreamBody:          `{"tenantId":"` + base.tenantID + `","gatewayId":"018f3d00-2000-7000-8000-000000000001","syncStatus":"UNCONFIGURED","drift":[]}`,
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
			if request.Header.Get("X-Delegation-Grant") == "" {
				t.Fatal("missing X-Delegation-Grant")
			}
			if request.Body != nil {
				raw, _ := io.ReadAll(request.Body)
				fix.lastUpstreamBody = string(raw)
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

func TestGatewayConfigShadowProxyEndpoints(t *testing.T) {
	fix := newConfigShadowGatewayFixture(t)

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/gateways/"+fix.gatewayID+"/config-shadow", nil)
	fix.authenticate(getReq, false)
	getRec := httptest.NewRecorder()
	fix.handler.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK || fix.lastUpstreamMethod != http.MethodGet || fix.lastUpstreamPath != "/internal/v1/gateways/"+fix.gatewayID+"/config-shadow" {
		t.Fatalf("GET status=%d method=%s path=%s body=%s", getRec.Code, fix.lastUpstreamMethod, fix.lastUpstreamPath, getRec.Body.String())
	}

	// PUT without CSRF is rejected before IAM/Connectivity.
	fix.upstreamCalls.Store(0)
	fix.iamCalls.Store(0)
	noCSRF := httptest.NewRequest(http.MethodPut, "/api/v1/gateways/"+fix.gatewayID+"/config-shadow", strings.NewReader(`{"reason":"test"}`))
	fix.authenticate(noCSRF, false)
	noCSRFRec := httptest.NewRecorder()
	fix.handler.ServeHTTP(noCSRFRec, noCSRF)
	if noCSRFRec.Code != http.StatusForbidden || fix.upstreamCalls.Load() != 0 || fix.iamCalls.Load() != 0 {
		t.Fatalf("PUT without CSRF status=%d iam=%d upstream=%d", noCSRFRec.Code, fix.iamCalls.Load(), fix.upstreamCalls.Load())
	}

	// PUT with CSRF forwards and maps 409 conflict into ProblemDetails.
	fix.upstreamStatus = http.StatusConflict
	fix.upstreamBody = `{"code":"CONFIG_SHADOW_REVISION_CONFLICT","message":"revision mismatch"}`
	putReq := httptest.NewRequest(http.MethodPut, "/api/v1/gateways/"+fix.gatewayID+"/config-shadow", strings.NewReader(`{"expectedDesiredRevision":1,"reason":"update"}`))
	fix.authenticate(putReq, true)
	putRec := httptest.NewRecorder()
	fix.handler.ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusConflict || !strings.Contains(putRec.Header().Get("Content-Type"), "application/problem+json") {
		t.Fatalf("expected 409 ProblemDetails, got %d (%s)", putRec.Code, putRec.Body.String())
	}
	var problem map[string]any
	if err := json.Unmarshal(putRec.Body.Bytes(), &problem); err != nil || problem["code"] != "CONFIG_SHADOW_REVISION_CONFLICT" {
		t.Fatalf("unexpected problem payload: %s", putRec.Body.String())
	}

	// Upstream 500 internal error must be masked in ProblemDetails.
	fix.upstreamStatus = http.StatusInternalServerError
	fix.upstreamBody = `{"code":"CONFIG_SHADOW_SERVICE_UNAVAILABLE","message":"pq: leaked db internal error"}`
	errReq := httptest.NewRequest(http.MethodGet, "/api/v1/gateways/"+fix.gatewayID+"/config-shadow", nil)
	fix.authenticate(errReq, false)
	errRec := httptest.NewRecorder()
	fix.handler.ServeHTTP(errRec, errReq)
	if errRec.Code != http.StatusInternalServerError || strings.Contains(errRec.Body.String(), "leaked") || !strings.Contains(errRec.Body.String(), "Gateway config shadow service is unavailable.") {
		t.Fatalf("expected masked 500 ProblemDetails, got %d body=%s", errRec.Code, errRec.Body.String())
	}
}

func TestGatewayConfigShadowRateLimitAndSafeLogPath(t *testing.T) {
	fix := newConfigShadowGatewayFixture(t)
	fix.handler.rateLimiter = limitpolicy.NewLimiter(limitpolicy.NewMemoryCounter(1000), &limitpolicy.Policy{
		Version: 1,
		Limits: []limitpolicy.Limit{
			{Dimension: limitpolicy.DimensionRest, Window: time.Minute, Burst: 100},
			{Dimension: limitpolicy.DimensionCommandWrite, Window: time.Minute, Burst: 1, FailClosed: true},
		},
	})
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPut, "/api/v1/gateways/"+fix.gatewayID+"/config-shadow", strings.NewReader(`{"reason":"rate test"}`))
		req.RemoteAddr = "10.0.0.1:1001"
		fix.authenticate(req, true)
		rec := httptest.NewRecorder()
		fix.handler.ServeHTTP(rec, req)
		if i == 0 && rec.Code != http.StatusOK {
			t.Fatalf("first PUT expected 200, got %d", rec.Code)
		}
		if i == 1 && rec.Code != http.StatusTooManyRequests {
			t.Fatalf("second PUT expected 429, got %d", rec.Code)
		}
	}
	if got := safeLogPath("/api/v1/gateways/" + fix.gatewayID + "/config-shadow"); got != "/api/v1/gateways/{gatewayId}/config-shadow" {
		t.Fatalf("safeLogPath = %q", got)
	}
}
