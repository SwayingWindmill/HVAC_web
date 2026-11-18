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
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

type gatewayCredentialsFixture struct {
	*commandGatewayFixture
	gatewayID               string
	credentialID            string
	upstreamCalls           atomic.Int32
	lastUpstreamMethod      string
	lastUpstreamPath        string
	lastUpstreamBody        string
	lastUpstreamTraceparent string
	upstreamStatus          int
	upstreamBody            string
}

func newGatewayCredentialsFixture(t *testing.T) *gatewayCredentialsFixture {
	t.Helper()
	base := newCommandGatewayFixture(t)
	now := time.Date(2026, 7, 26, 15, 0, 0, 0, time.UTC)
	snapshot, err := ownershipregistry.Parse([]byte(`{
		"registryVersion": 1,
		"registryRevision": 91,
		"routes": [
			{"method":"GET","path":"/api/v1/gateways/{gatewayId}/credential","owner":"connectivity","publicIngress":"platform-gateway","revision":1,"rollout":{"mode":"all"},"compatibilityMode":"native","allowedScopeDimensions":["tenant","site","principal"]},
			{"method":"GET","path":"/api/v1/gateways/{gatewayId}/credentials","owner":"connectivity","publicIngress":"platform-gateway","revision":1,"rollout":{"mode":"all"},"compatibilityMode":"native","allowedScopeDimensions":["tenant","site","principal"]},
			{"method":"POST","path":"/api/v1/gateways/{gatewayId}/credentials/{credentialId}/revoke","owner":"connectivity","publicIngress":"platform-gateway","revision":1,"rollout":{"mode":"all"},"compatibilityMode":"native","allowedScopeDimensions":["tenant","site","principal"]},
			{"method":"POST","path":"/api/v1/gateways/{gatewayId}/enrollment-code","owner":"connectivity","publicIngress":"platform-gateway","revision":1,"rollout":{"mode":"all"},"compatibilityMode":"native","allowedScopeDimensions":["tenant","site","principal"]},
			{"method":"POST","path":"/api/v1/gateways/{gatewayId}/revoke","owner":"connectivity","publicIngress":"platform-gateway","revision":1,"rollout":{"mode":"all"},"compatibilityMode":"native","allowedScopeDimensions":["tenant","site","principal"]},
			{"method":"POST","path":"/api/v1/gateways/{gatewayId}/rotate","owner":"connectivity","publicIngress":"platform-gateway","revision":1,"rollout":{"mode":"all"},"compatibilityMode":"native","allowedScopeDimensions":["tenant","site","principal"]}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	base.handler.routeManager = ownershipregistry.NewManager(snapshot, ownershipregistry.NewMemoryAuditSink(), func() time.Time { return now })

	fix := &gatewayCredentialsFixture{
		commandGatewayFixture: base,
		gatewayID:             "018f3d00-2000-7000-8000-000000000001",
		credentialID:          "018f3d00-3000-7000-8000-000000000009",
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
			} else {
				fix.lastUpstreamBody = ""
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

func TestGatewayCredentialProxyRoutesAndCSRF(t *testing.T) {
	fix := newGatewayCredentialsFixture(t)

	// List credentials requires no CSRF, reads upstream list.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/gateways/"+fix.gatewayID+"/credentials", nil)
	fix.authenticate(req, false)
	rec := httptest.NewRecorder()
	fix.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || fix.lastUpstreamMethod != http.MethodGet || fix.lastUpstreamPath != "/internal/v1/gateways/"+fix.gatewayID+"/credentials" {
		t.Fatalf("GET credentials got status=%d method=%s path=%s body=%s", rec.Code, fix.lastUpstreamMethod, fix.lastUpstreamPath, rec.Body.String())
	}

	// Single-cert revoke without CSRF is rejected before touching IAM or Connectivity.
	beforeIAM := fix.iamCalls.Load()
	beforeUpstream := fix.upstreamCalls.Load()
	noCSRF := httptest.NewRequest(http.MethodPost, "/api/v1/gateways/"+fix.gatewayID+"/credentials/"+fix.credentialID+"/revoke", strings.NewReader(`{"reason":"rotated"}`))
	fix.authenticate(noCSRF, false)
	rec = httptest.NewRecorder()
	fix.handler.ServeHTTP(rec, noCSRF)
	if rec.Code != http.StatusForbidden || fix.iamCalls.Load() != beforeIAM || fix.upstreamCalls.Load() != beforeUpstream {
		t.Fatalf("expected 403 without CSRF before upstream, got status=%d iam=%d->%d conn=%d->%d", rec.Code, beforeIAM, fix.iamCalls.Load(), beforeUpstream, fix.upstreamCalls.Load())
	}

	// Single-cert revoke with CSRF forwards optional JSON body.
	revokeCert := httptest.NewRequest(http.MethodPost, "/api/v1/gateways/"+fix.gatewayID+"/credentials/"+fix.credentialID+"/revoke", strings.NewReader(`{"reason":"key compromised"}`))
	fix.authenticate(revokeCert, true)
	revokeCert.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	fix.handler.ServeHTTP(rec, revokeCert)
	if rec.Code != http.StatusOK || fix.lastUpstreamMethod != http.MethodPost || fix.lastUpstreamPath != "/internal/v1/gateways/"+fix.gatewayID+"/credentials/"+fix.credentialID+"/revoke" || fix.lastUpstreamBody != `{"reason":"key compromised"}` {
		t.Fatalf("POST credential revoke got status=%d method=%s path=%s body=%q", rec.Code, fix.lastUpstreamMethod, fix.lastUpstreamPath, fix.lastUpstreamBody)
	}

	// Rotate enrollment forwards cleanly with CSRF.
	rotate := httptest.NewRequest(http.MethodPost, "/api/v1/gateways/"+fix.gatewayID+"/rotate", nil)
	fix.authenticate(rotate, true)
	rec = httptest.NewRecorder()
	fix.handler.ServeHTTP(rec, rotate)
	if rec.Code != http.StatusOK || fix.lastUpstreamMethod != http.MethodPost || fix.lastUpstreamPath != "/internal/v1/gateways/"+fix.gatewayID+"/rotate" {
		t.Fatalf("POST rotate got status=%d method=%s path=%s", rec.Code, fix.lastUpstreamMethod, fix.lastUpstreamPath)
	}

	// safeLogPath templates gateway credential paths without leaking raw IDs.
	if got := safeLogPath("/api/v1/gateways/" + fix.gatewayID + "/credentials/" + fix.credentialID + "/revoke"); got != "/api/v1/gateways/{gatewayId}/credentials/{credentialId}/revoke" {
		t.Fatalf("safeLogPath=%q", got)
	}
	if got := safeLogPath("/api/v1/gateways/" + fix.gatewayID + "/rotate"); got != "/api/v1/gateways/{gatewayId}/rotate" {
		t.Fatalf("safeLogPath=%q", got)
	}
}

func TestGatewayCredentialsPropagatesTraceparent(t *testing.T) {
	fix := newGatewayCredentialsFixture(t)
	const wantTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	req := httptest.NewRequest(http.MethodGet, "/api/v1/gateways/"+fix.gatewayID+"/credentials", nil)
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
