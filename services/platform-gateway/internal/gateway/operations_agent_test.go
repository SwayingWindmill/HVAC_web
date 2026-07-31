package gateway

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/analyticsmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/libs/sessionstore"
)

type operationsGatewayFixture struct {
	handler         http.Handler
	organizationID  string
	siteID          string
	sessionID       string
	gatewaySigner   *ecdsa.PrivateKey
	iamCalls        atomic.Int32
	operationsCalls atomic.Int32
	denySite        atomic.Bool
	lastUpstream    atomic.Pointer[http.Request]
}

func newOperationsGatewayFixture(t *testing.T, rateLimit int) *operationsGatewayFixture {
	t.Helper()
	now := time.Date(2026, 7, 31, 8, 0, 0, 0, time.UTC)
	fixture := &operationsGatewayFixture{
		organizationID: "018f3e00-1000-7000-8000-000000000001",
		siteID:         "018f3e00-2000-7000-8000-000000000001",
		gatewaySigner:  commandTestSigner(t),
	}
	store := sessionstore.NewMemoryStore()
	configured := NewHandler(Config{
		Now: func() time.Time { return now },
		Identity: &IdentityConfig{
			OIDCIssuer: "https://issuer.example.test", OIDCClientID: "client",
			OIDCRedirectURI: "https://web.example.test/api/v1/auth/callback",
			PublicOrigin:    "https://web.example.test", IAMURL: "https://iam.example.test",
			IAMAudience: "iam-service", ExecutingWorkloadSPIFFE: "spiffe://hvac.local/platform-gateway",
			PolicyRevision: "identity-policy-1", DelegationSigner: fixture.gatewaySigner,
			TokenEncryptionKey: make([]byte, 32), SessionStore: store,
			SessionTTL: time.Hour, DelegationTTL: 30 * time.Second,
			IAMHTTPClient: fixture.iamClient(t, now),
		},
		Analytics: &AnalyticsConfig{QueryAudience: "telemetry-query-service"},
		Operations: &OperationsAgentConfig{
			BaseURL: "https://operations.example.test", Audience: "operations-agent-service",
			WorkloadSPIFFEID: "spiffe://hvac.local/operations-agent-service",
			HTTPClient:       fixture.operationsClient(t, now), Timeout: time.Second,
			RateLimitPerMinute: rateLimit,
		},
	})
	fixture.handler = configured
	controller := configured.(*handler)
	ciphertext, err := controller.identity.encryptBytes([]byte("operations-csrf-fixture"))
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateSession(context.Background(), sessionstore.Session{
		ID: "operations-session",
		Principal: identitycontext.UserPrincipal{
			Subject: "operations-user", Issuer: "https://issuer.example.test", Roles: []string{"operator"},
		},
		ActingOrganizationID: fixture.organizationID,
		CSRFTokenCiphertext:  ciphertext,
		ExpiresAt:            now.Add(time.Hour),
	}, sessionstore.MutationContext{
		Action: "SESSION_CREATED", Result: "SUCCEEDED", PolicyRevision: "identity-policy-1",
		CorrelationID: "operations-fixture", TraceID: strings.Repeat("a", 32),
		Traceparent:      "00-" + strings.Repeat("a", 32) + "-" + strings.Repeat("b", 16) + "-01",
		ExecutingService: "platform-gateway", ExecutingSPIFFEID: "spiffe://hvac.local/platform-gateway", OccurredAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.sessionID = created.ID
	return fixture
}

func (fixture *operationsGatewayFixture) authenticate(request *http.Request, mutation bool) {
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: fixture.sessionID})
	if mutation {
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", "https://web.example.test")
		request.Header.Set("X-CSRF-Token", "operations-csrf-fixture")
	}
}

func (fixture *operationsGatewayFixture) iamClient(t *testing.T, now time.Time) *http.Client {
	t.Helper()
	return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		fixture.iamCalls.Add(1)
		parent, err := identitycontext.VerifyDelegation(&fixture.gatewaySigner.PublicKey, request.Header.Get("X-Delegation-Grant"))
		if err != nil {
			t.Fatalf("invalid parent delegation: %v", err)
		}
		switch request.URL.Path {
		case "/internal/v1/registry-read/decision":
			if len(parent.Actions) != 1 || parent.Actions[0] != "registry:authorize" {
				t.Fatalf("unexpected Registry parent claims: %+v", parent)
			}
			var input registryauth.DecisionRequest
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil ||
				(input.Action != registryauth.ActionSiteRead && input.Action != registryauth.ActionEquipmentList) {
				t.Fatal("invalid Registry authorization request")
			}
			allowedSites := []string{fixture.siteID}
			allowed := !fixture.denySite.Load()
			if !allowed {
				allowedSites = nil
			}
			decision := registryauth.Decision{
				Allowed: allowed, PrincipalID: "018f3e00-5000-7000-8000-000000000001",
				SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject,
				ActingOrganizationID: fixture.organizationID, AllowedSiteIDs: allowedSites,
				Actions: []registryauth.Action{input.Action}, PolicyRevision: "identity-policy-1",
				ReasonCode: registryauth.ReasonAllowSiteRole, DecidedAt: now.Format(time.RFC3339Nano),
			}
			claims := registryauth.GrantClaims{
				Issuer: "spiffe://hvac.local/iam-service", Presenter: input.GrantPresenter,
				Audience: "platform-core-service", PrincipalID: decision.PrincipalID,
				SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject,
				ActingOrganizationID: fixture.organizationID, AllowedSiteIDs: []string{fixture.siteID},
				Actions: []registryauth.Action{input.Action}, PolicyRevision: "identity-policy-1",
				DecisionReason: registryauth.ReasonAllowSiteRole, SessionID: parent.SessionID,
				ParentTokenID: parent.TokenID, IssuedAt: now.Unix(), ExpiresAt: now.Add(30 * time.Second).Unix(),
				TokenID: randomURLToken(16),
			}
			return telemetryJSONResponse(http.StatusOK, registryauth.DecisionResponse{
				Decision: decision, DelegationGrant: commandUnsignedRegistryGrant(claims),
			}), nil
		case analyticsDecisionPath:
			if len(parent.Actions) != 1 || parent.Actions[0] != analyticsAuthorizeAction {
				t.Fatalf("unexpected Analytics parent claims: %+v", parent)
			}
			var input analyticsmodel.AuthorizationDecisionRequest
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil ||
				input.ActingOrganizationID != fixture.organizationID || input.Action != analyticsmodel.EnergySeriesAction {
				t.Fatal("invalid Analytics authorization request")
			}
			allowed := !fixture.denySite.Load() && input.SiteID == fixture.siteID
			reason := analyticsmodel.AuthorizationReasonDenyAction
			if allowed {
				reason = analyticsmodel.AuthorizationReasonAllowSiteBinding
			}
			return telemetryJSONResponse(http.StatusOK, analyticsmodel.AuthorizationDecisionResponse{
				Decision: analyticsmodel.AuthorizationDecision{
					Allowed: allowed, PrincipalID: "018f3e00-5000-7000-8000-000000000001",
					SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject,
					ActingOrganizationID: fixture.organizationID, SiteID: input.SiteID, Action: input.Action,
					PolicyRevision: "analytics-policy-7", ReasonCode: reason, DecidedAt: now.Format(time.RFC3339Nano),
				},
			}), nil
		default:
			t.Fatalf("unexpected IAM path %s", request.URL.Path)
			return nil, io.EOF
		}
	})}
}

func (fixture *operationsGatewayFixture) operationsClient(t *testing.T, now time.Time) *http.Client {
	t.Helper()
	return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		fixture.operationsCalls.Add(1)
		fixture.lastUpstream.Store(request.Clone(request.Context()))
		for _, forbidden := range []string{"Cookie", "Origin", "X-CSRF-Token", "X-Principal", "X-Roles"} {
			if request.Header.Get(forbidden) != "" {
				t.Fatalf("forbidden upstream header %s", forbidden)
			}
		}
		claims, err := identitycontext.VerifyDelegation(&fixture.gatewaySigner.PublicKey, request.Header.Get("X-Delegation-Grant"))
		if err != nil {
			t.Fatalf("invalid Operations delegation: %v", err)
		}
		if claims.Audience != "operations-agent-service" || claims.ActingOrganizationID != fixture.organizationID ||
			!operationsContains(claims.Actions, "operations:investigate") || !operationsContains(claims.Scopes, "site:"+fixture.siteID) ||
			claims.IssuedAt != now.Unix() {
			t.Fatalf("unexpected Operations claims: %+v", claims)
		}
		if request.Header.Get("X-Acting-Organization-ID") != fixture.organizationID || request.Header.Get("X-Route-Policy-Revision") != "0" {
			t.Fatal("missing authoritative Operations headers")
		}
		body := `{"schemaVersion":1,"id":"investigation-001","scope":{"organizationId":"` + fixture.organizationID + `","siteId":"` + fixture.siteID + `","equipmentId":null,"deviceId":null},"status":"COMPLETED","revision":9,"createdAt":1,"activeRun":null,"outcome":"SUPPORTED_SITE_FINDING","evidence":[],"analysisReferences":[],"findings":[],"toolReceipts":[]}`
		status := http.StatusOK
		if strings.HasSuffix(request.URL.Path, "/operations/investigations") {
			status = http.StatusCreated
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
}

func (fixture *operationsGatewayFixture) serviceDelegation(t *testing.T) string {
	t.Helper()
	controller := fixture.handler.(*handler)
	stored, err := controller.identity.store.GetSession(context.Background(), fixture.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := controller.operationsServiceDelegation(bffSession{Session: stored}, fixture.siteID)
	if err != nil {
		t.Fatal(err)
	}
	return grant
}

func (fixture *operationsGatewayFixture) authorizeTool(
	t *testing.T,
	body string,
	grant string,
	workloadSPIFFEID string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, InternalOperationsToolAuthorizationPath, strings.NewReader(body))
	var envelope struct {
		Request struct {
			RequestID string `json:"requestId"`
		} `json:"request"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Delegation-Grant", grant)
	request.Header.Set("X-Request-ID", envelope.Request.RequestID)
	request.TLS = verifiedWorkloadTLSState(t, workloadSPIFFEID)
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	return recorder
}

func operationsContains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func TestOperationsGatewayEnforcesSessionCSRFScopeAndServiceDelegation(t *testing.T) {
	fixture := newOperationsGatewayFixture(t, 30)
	path := "/api/v1/sites/" + fixture.siteID + "/operations/investigations"
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
	fixture.authenticate(request, true)
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if fixture.iamCalls.Load() != 1 || fixture.operationsCalls.Load() != 1 {
		t.Fatalf("unexpected upstream counts IAM=%d Operations=%d", fixture.iamCalls.Load(), fixture.operationsCalls.Load())
	}
	if strings.Contains(recorder.Body.String(), "lease") || strings.Contains(recorder.Body.String(), "checkpoint") || strings.Contains(recorder.Body.String(), "points") {
		t.Fatal("unsafe Operations state crossed the public contract")
	}

	missingCSRF := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
	fixture.authenticate(missingCSRF, false)
	missingRecorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(missingRecorder, missingCSRF)
	if missingRecorder.Code != http.StatusForbidden || fixture.operationsCalls.Load() != 1 {
		t.Fatalf("CSRF failure reached Operations upstream: status=%d calls=%d", missingRecorder.Code, fixture.operationsCalls.Load())
	}

	wrongContentType := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
	fixture.authenticate(wrongContentType, true)
	wrongContentType.Header.Set("Content-Type", "text/plain")
	wrongContentRecorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(wrongContentRecorder, wrongContentType)
	if wrongContentRecorder.Code != http.StatusUnsupportedMediaType || fixture.iamCalls.Load() != 1 || fixture.operationsCalls.Load() != 1 {
		t.Fatalf("content type failure reached upstreams: status=%d IAM=%d Operations=%d", wrongContentRecorder.Code, fixture.iamCalls.Load(), fixture.operationsCalls.Load())
	}
}

func TestOperationsGatewayRejectsChunkedGETBodyBeforeAuthorization(t *testing.T) {
	fixture := newOperationsGatewayFixture(t, 30)
	path := "/api/v1/sites/" + fixture.siteID + "/operations/investigations/investigation-001"
	request := httptest.NewRequest(http.MethodGet, path, io.NopCloser(strings.NewReader(`{}`)))
	request.ContentLength = -1
	fixture.authenticate(request, false)
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected body rejection, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if fixture.iamCalls.Load() != 0 || fixture.operationsCalls.Load() != 0 {
		t.Fatalf("invalid GET reached upstreams IAM=%d Operations=%d", fixture.iamCalls.Load(), fixture.operationsCalls.Load())
	}
}

func TestOperationsGatewayDenialIsNondiscoverableAndRateLimited(t *testing.T) {
	fixture := newOperationsGatewayFixture(t, 1)
	fixture.denySite.Store(true)
	path := "/api/v1/sites/" + fixture.siteID + "/operations/investigations/investigation-001"
	request := httptest.NewRequest(http.MethodGet, path, nil)
	fixture.authenticate(request, false)
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound || fixture.operationsCalls.Load() != 0 {
		t.Fatalf("Site denial was discoverable or reached upstream: status=%d calls=%d", recorder.Code, fixture.operationsCalls.Load())
	}

	fixture.denySite.Store(false)
	rateLimited := httptest.NewRequest(http.MethodGet, path, nil)
	fixture.authenticate(rateLimited, false)
	rateRecorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(rateRecorder, rateLimited)
	if rateRecorder.Code != http.StatusTooManyRequests || fixture.operationsCalls.Load() != 0 {
		t.Fatalf("expected rate limit before upstream, got %d", rateRecorder.Code)
	}
}

func TestOperationsToolAuthorizationIssuesExactOwnerGrants(t *testing.T) {
	fixture := newOperationsGatewayFixture(t, 30)
	serviceGrant := fixture.serviceDelegation(t)
	registryBody := `{"investigationId":"investigation-001","runId":"run-001","request":{"requestId":"registry-equipment-001","tool":"registry.listSiteEquipment","input":{"siteId":"` + fixture.siteID + `"}}}`
	registryResponse := fixture.authorizeTool(t, registryBody, serviceGrant, "spiffe://hvac.local/operations-agent-service")
	if registryResponse.Code != http.StatusOK {
		t.Fatalf("expected Registry grant, got %d: %s", registryResponse.Code, registryResponse.Body.String())
	}
	var registryGrant operationsToolAuthorizationResponse
	if err := json.Unmarshal(registryResponse.Body.Bytes(), &registryGrant); err != nil || registryGrant.DelegationGrant == "" || registryGrant.PolicyRevision != "identity-policy-1" {
		t.Fatalf("invalid Registry grant response: %+v err=%v", registryGrant, err)
	}

	query := analyticsmodel.EnergySeriesQuery{
		OrganizationID: fixture.organizationID,
		SiteID:         fixture.siteID,
		EnergyType:     analyticsmodel.EnergyTypeElectricity,
		Granularity:    analyticsmodel.GranularityHour,
		Timezone:       "Asia/Tokyo",
		From:           time.Date(2026, 7, 29, 13, 0, 0, 0, time.UTC),
		To:             time.Date(2026, 7, 29, 21, 0, 0, 0, time.UTC),
		QualityPolicy:  analyticsmodel.QualityPolicyValidOnly,
	}
	energyBody, err := json.Marshal(map[string]any{
		"investigationId": "investigation-001",
		"runId":           "run-001",
		"request": map[string]any{
			"requestId": "energy-target-001",
			"tool":      "analytics.getEnergySeries",
			"input":     query,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	energyResponse := fixture.authorizeTool(t, string(energyBody), serviceGrant, "spiffe://hvac.local/operations-agent-service")
	if energyResponse.Code != http.StatusOK {
		t.Fatalf("expected Energy grant, got %d: %s", energyResponse.Code, energyResponse.Body.String())
	}
	var energyGrant operationsToolAuthorizationResponse
	if err := json.Unmarshal(energyResponse.Body.Bytes(), &energyGrant); err != nil {
		t.Fatal(err)
	}
	if energyGrant.PolicyRevision != "analytics-policy-7" {
		t.Fatalf("Energy policy revision = %q", energyGrant.PolicyRevision)
	}
	claims, err := identitycontext.VerifyDelegation(&fixture.gatewaySigner.PublicKey, energyGrant.DelegationGrant)
	if err != nil {
		t.Fatalf("invalid Energy delegation: %v", err)
	}
	digest, err := query.ScopeDigest()
	if err != nil {
		t.Fatal(err)
	}
	if claims.Issuer != "spiffe://hvac.local/platform-gateway" ||
		claims.ExecutingService != "spiffe://hvac.local/operations-agent-service" ||
		claims.Audience != "telemetry-query-service" || claims.ActingOrganizationID != fixture.organizationID ||
		len(claims.Actions) != 1 || claims.Actions[0] != analyticsmodel.EnergySeriesAction ||
		len(claims.Scopes) != 1 || claims.Scopes[0] != digest || claims.PolicyRevision != "analytics-policy-7" {
		t.Fatalf("Energy grant is not exact: %+v digest=%s", claims, digest)
	}
	if fixture.iamCalls.Load() != 2 {
		t.Fatalf("expected two exact IAM decisions, got %d", fixture.iamCalls.Load())
	}
}

func TestOperationsToolAuthorizationFailsClosedBeforeIAM(t *testing.T) {
	fixture := newOperationsGatewayFixture(t, 30)
	serviceGrant := fixture.serviceDelegation(t)
	validBody := `{"investigationId":"investigation-001","runId":"run-001","request":{"requestId":"registry-site-001","tool":"registry.getSite","input":{"siteId":"` + fixture.siteID + `"}}}`

	missingContentType := httptest.NewRequest(http.MethodPost, InternalOperationsToolAuthorizationPath, strings.NewReader(validBody))
	missingContentType.Header.Set("X-Delegation-Grant", serviceGrant)
	missingContentType.TLS = verifiedWorkloadTLSState(t, "spiffe://hvac.local/operations-agent-service")
	missingContentRecorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(missingContentRecorder, missingContentType)
	if missingContentRecorder.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("expected content type rejection, got %d", missingContentRecorder.Code)
	}

	wrongWorkload := fixture.authorizeTool(t, validBody, serviceGrant, "spiffe://hvac.local/other-service")
	if wrongWorkload.Code != http.StatusUnauthorized {
		t.Fatalf("expected workload rejection, got %d", wrongWorkload.Code)
	}

	unknownInput := `{"investigationId":"investigation-001","runId":"run-001","request":{"requestId":"registry-site-001","tool":"registry.getSite","input":{"siteId":"` + fixture.siteID + `","payload":{"bypass":true}}}}`
	unknownResponse := fixture.authorizeTool(t, unknownInput, serviceGrant, "spiffe://hvac.local/operations-agent-service")
	if unknownResponse.Code != http.StatusBadRequest {
		t.Fatalf("expected strict input rejection, got %d: %s", unknownResponse.Code, unknownResponse.Body.String())
	}

	mismatchedRequestID := httptest.NewRequest(http.MethodPost, InternalOperationsToolAuthorizationPath, strings.NewReader(validBody))
	mismatchedRequestID.Header.Set("Content-Type", "application/json")
	mismatchedRequestID.Header.Set("X-Delegation-Grant", serviceGrant)
	mismatchedRequestID.Header.Set("X-Request-ID", "different-request-id")
	mismatchedRequestID.TLS = verifiedWorkloadTLSState(t, "spiffe://hvac.local/operations-agent-service")
	mismatchedRecorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(mismatchedRecorder, mismatchedRequestID)
	if mismatchedRecorder.Code != http.StatusBadRequest {
		t.Fatalf("expected request identity rejection, got %d", mismatchedRecorder.Code)
	}

	controller := fixture.handler.(*handler)
	claims, err := identitycontext.VerifyDelegation(&fixture.gatewaySigner.PublicKey, serviceGrant)
	if err != nil {
		t.Fatal(err)
	}
	claims.PolicyRevision = "identity-policy-stale"
	staleGrant, err := identitycontext.SignDelegation(controller.identity.config.DelegationSigner, claims)
	if err != nil {
		t.Fatal(err)
	}
	staleResponse := fixture.authorizeTool(t, validBody, staleGrant, "spiffe://hvac.local/operations-agent-service")
	if staleResponse.Code != http.StatusUnauthorized {
		t.Fatalf("expected stale policy rejection, got %d", staleResponse.Code)
	}

	forgedRequest := httptest.NewRequest(http.MethodPost, InternalOperationsToolAuthorizationPath, strings.NewReader(validBody))
	forgedRequest.Header.Set("X-Delegation-Grant", serviceGrant)
	forgedRequest.Header.Set("Cookie", "session=forged")
	forgedRequest.TLS = verifiedWorkloadTLSState(t, "spiffe://hvac.local/operations-agent-service")
	forgedRecorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(forgedRecorder, forgedRequest)
	if forgedRecorder.Code != http.StatusBadRequest {
		t.Fatalf("expected browser header rejection, got %d", forgedRecorder.Code)
	}
	if fixture.iamCalls.Load() != 0 {
		t.Fatalf("invalid internal requests reached IAM %d times", fixture.iamCalls.Load())
	}
}
