package gateway

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmauth"
	"github.com/quanlaihe/hvac-web/libs/analyticsmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/limitpolicy"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/libs/sessionstore"
	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
	"github.com/quanlaihe/hvac-web/libs/telemetryhistorymodel"
	"github.com/quanlaihe/hvac-web/libs/workorderauth"
)

type fakeOperationsRateLimiter struct {
	max   int32
	count atomic.Int32
	fail  atomic.Bool
}

func (limiter *fakeOperationsRateLimiter) Allow(context.Context, string, time.Duration, int) (bool, error) {
	if limiter.fail.Load() {
		return false, errors.New("limit backend unavailable")
	}
	return limiter.count.Add(1) <= limiter.max, nil
}

type operationsGatewayFixture struct {
	handler              http.Handler
	tenantID             string
	siteID               string
	deviceID             string
	sessionID            string
	gatewaySigner        *ecdsa.PrivateKey
	counter              *fakeOperationsRateLimiter
	limiter              *limitpolicy.Limiter
	iamCalls             atomic.Int32
	operationsCalls      atomic.Int32
	denySite             atomic.Bool
	invalidStream        atomic.Bool
	invalidIdentity      atomic.Bool
	rejectUnsafe         atomic.Bool
	auditRead            atomic.Bool
	usageExtraField      atomic.Bool
	usageTooManyFailures atomic.Bool
	lastUpstream         atomic.Pointer[http.Request]
	lastUpstreamBody     atomic.Pointer[string]
	lastOwner            atomic.Pointer[http.Request]
	registryActions      []registryauth.Action
}

func newOperationsGatewayFixture(t *testing.T, rateLimit int) *operationsGatewayFixture {
	t.Helper()
	now := time.Date(2026, 7, 31, 8, 0, 0, 0, time.UTC)
	counter := &fakeOperationsRateLimiter{max: int32(rateLimit)}
	limiter := limitpolicy.NewLimiter(counter, &limitpolicy.Policy{Version: 1, Limits: []limitpolicy.Limit{
		{Dimension: limitpolicy.DimensionOperationsAgent, Window: time.Minute, Burst: 1 << 20, FailClosed: true},
	}})
	fixture := &operationsGatewayFixture{
		tenantID:      "018f3d00-0000-7000-8000-000000000001",
		siteID:        "018f3e00-2000-7000-8000-000000000001",
		deviceID:      "018f3e00-3000-7000-8000-000000000001",
		gatewaySigner: commandTestSigner(t),
		counter:       counter,
		limiter:       limiter,
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
		// The Agent reads alarms from the owner itself; the Gateway only authorizes.
		Alarm:     &AlarmConfig{BackendBaseURL: "https://alarm.example.test"},
		WorkOrder: &WorkOrderConfig{BackendBaseURL: "https://work-order.example.test"},
		// The intelligence owners serve the Gateway, and FDD also the Agent, a Gateway-signed context.
		Intelligence: &IntelligenceConfig{
			FDDBaseURL: "https://fdd.example.test", ForecastBaseURL: "https://forecast.example.test",
			OptimizationBaseURL: "https://optimization.example.test", MVBaseURL: "https://mv.example.test",
			OwnerHTTPClient: ownerUpstream(&fixture.lastOwner),
			HTTPClient:      &http.Client{}, Timeout: time.Second,
		},
		Telemetry: &TelemetryConfig{RuntimeAudience: "telemetry-runtime-service"},
		Operations: &OperationsAgentConfig{
			BaseURL: "https://operations.example.test", Audience: "operations-agent-service",
			WorkloadSPIFFEID: "spiffe://hvac.local/operations-agent-service",
			HTTPClient:       fixture.operationsClient(t, now), Timeout: time.Second,
			RateLimiter: fixture.limiter,
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
		TenantID:            fixture.tenantID,
		CSRFTokenCiphertext: ciphertext,
		ExpiresAt:           now.Add(time.Hour),
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
		case "/internal/v1/registry/decision":
			if len(parent.Actions) != 1 || parent.Actions[0] != "registry:authorize" {
				t.Fatalf("unexpected Registry parent claims: %+v", parent)
			}
			var input registryauth.DecisionRequest
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil ||
				(input.Action != registryauth.ActionSiteRead && input.Action != registryauth.ActionAssetList && input.Action != registryauth.ActionAssetModelRead && input.Action != registryauth.ActionSavingsManage) {
				t.Fatal("invalid Registry authorization request")
			}
			fixture.registryActions = append(fixture.registryActions, input.Action)
			allowedSites := []string{fixture.siteID}
			allowed := !fixture.denySite.Load()
			if !allowed {
				allowedSites = nil
			}
			decision := registryauth.Decision{
				Allowed: allowed, PrincipalID: "018f3e00-5000-7000-8000-000000000001",
				SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject,
				TenantID: fixture.tenantID, AllowedSiteIDs: allowedSites,
				Actions: []registryauth.Action{input.Action}, PolicyRevision: "identity-policy-1",
				ReasonCode: registryauth.ReasonAllowSiteRole, DecidedAt: now.Format(time.RFC3339Nano),
			}
			claims := registryauth.GrantClaims{
				Issuer: "spiffe://hvac.local/iam-service", Presenter: input.GrantPresenter,
				Audience: "platform-core-service", PrincipalID: decision.PrincipalID,
				SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject,
				TenantID: fixture.tenantID, AllowedSiteIDs: []string{fixture.siteID},
				Actions: []registryauth.Action{input.Action}, PolicyRevision: "identity-policy-1",
				DecisionReason: registryauth.ReasonAllowSiteRole, SessionID: parent.SessionID,
				ParentTokenID: parent.TokenID, IssuedAt: now.Unix(), ExpiresAt: now.Add(30 * time.Second).Unix(),
				TokenID: randomURLToken(16),
			}
			return telemetryJSONResponse(http.StatusOK, registryauth.DecisionResponse{
				Decision: decision, DelegationGrant: commandUnsignedRegistryGrant(claims),
			}), nil
		case telemetryDecisionPath:
			if len(parent.Actions) != 1 || parent.Actions[0] != "telemetry:authorize" {
				t.Fatalf("unexpected Telemetry parent claims: %+v", parent)
			}
			var input telemetryauth.DecisionRequest
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil || input.TenantID != fixture.tenantID || (input.Action != telemetryauth.ActionSnapshotRead && input.Action != telemetryauth.ActionHistoryRead) || input.GrantPresenter != "spiffe://hvac.local/operations-agent-service" {
				t.Fatal("invalid Telemetry authorization request")
			}
			canonical, err := telemetryauth.CanonicalTargets(input.Targets)
			if err != nil || len(canonical) != 1 {
				t.Fatal("invalid Telemetry target selection")
			}
			allowed := !fixture.denySite.Load() && canonical[0].DeviceID == fixture.deviceID
			decision := telemetryauth.Decision{
				Allowed: allowed, PrincipalID: "018f3e00-5000-7000-8000-000000000001",
				SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject, TenantID: fixture.tenantID,
				Action: input.Action, PolicyRevision: "telemetry-access:1", DecidedAt: now.Format(time.RFC3339Nano),
			}
			if allowed {
				decision.ReasonCode = telemetryauth.ReasonAllowExactScope
				decision.ScopeDigest, _ = telemetryauth.ScopeDigest(input.Action, fixture.tenantID, canonical)
				decision.Targets = []telemetryauth.AuthorizedTarget{{TenantID: fixture.tenantID, SiteID: fixture.siteID, DeviceID: fixture.deviceID, Keys: canonical[0].Keys}}
			} else {
				decision.ReasonCode = telemetryauth.ReasonResourceNotFound
			}
			claims := telemetryauth.GrantClaims{
				Issuer: "spiffe://hvac.local/iam-service", Presenter: input.GrantPresenter, Audience: "telemetry-runtime-service",
				PrincipalID: decision.PrincipalID, SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject, TenantID: fixture.tenantID,
				ActorChain: []telemetryauth.Actor{{Service: "platform-gateway", SPIFFEID: "spiffe://hvac.local/platform-gateway"}},
				Action:     input.Action, ScopeDigest: decision.ScopeDigest, TargetCount: len(canonical), KeyCount: len(canonical[0].Keys),
				PolicyRevision: decision.PolicyRevision, SessionID: parent.SessionID, ParentTokenID: parent.TokenID,
				RequestID: request.Header.Get("X-Request-ID"), TraceID: traceIDFromTraceparent(request.Header.Get("Traceparent")), Route: telemetryPublicRoute(input.Action),
				IssuedAt: now.Unix(), ExpiresAt: now.Add(30 * time.Second).Unix(), TokenID: "token-telemetry-1",
			}
			delegation := ""
			if allowed {
				delegation = unsignedTelemetryGrant(claims)
			}
			return telemetryJSONResponse(http.StatusOK, telemetryauth.DecisionResponse{Decision: decision, DelegationGrant: delegation}), nil
		case workOrderDecisionPath:
			var input workorderauth.DecisionRequest
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil || input.TenantID != fixture.tenantID || input.Action != workorderauth.ActionList || input.WorkOrderID != "" {
				t.Fatal("invalid Work Order authorization request")
			}
			allowed := !fixture.denySite.Load() && input.SiteID == fixture.siteID
			decision := workorderauth.Decision{
				Allowed: allowed, SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject, TenantID: fixture.tenantID,
				SiteID: input.SiteID, Action: input.Action, PolicyRevision: "work-order-access:1", ReasonCode: workorderauth.ReasonDenyScope,
				DecidedAt: now.Format(time.RFC3339Nano),
			}
			if allowed {
				decision.PrincipalID, decision.ReasonCode = "018f3e00-5000-7000-8000-000000000001", workorderauth.ReasonAllowExactScope
			}
			return telemetryJSONResponse(http.StatusOK, workorderauth.DecisionResponse{Decision: decision}), nil
		case alarmDecisionPath:
			if len(parent.Actions) != 1 || parent.Actions[0] != "alarm:authorize" {
				t.Fatalf("unexpected Alarm parent claims: %+v", parent)
			}
			var input alarmauth.DecisionRequest
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil || input.TenantID != fixture.tenantID || input.Action != alarmauth.ActionRead || input.AlarmID != "" {
				t.Fatal("invalid Alarm authorization request")
			}
			allowed := !fixture.denySite.Load() && input.SiteID == fixture.siteID
			reason := alarmauth.ReasonDenyScope
			if allowed {
				reason = alarmauth.ReasonAllowExactScope
			}
			return telemetryJSONResponse(http.StatusOK, alarmauth.DecisionResponse{Decision: alarmauth.Decision{
				Allowed: allowed, PrincipalID: "018f3e00-5000-7000-8000-000000000001",
				SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject, TenantID: fixture.tenantID,
				SiteID: input.SiteID, Action: input.Action, PolicyRevision: "alarm-policy-3",
				ReasonCode: reason, DecidedAt: now.Format(time.RFC3339Nano),
			}}), nil
		case analyticsDecisionPath:
			if len(parent.Actions) != 1 || parent.Actions[0] != analyticsAuthorizeAction {
				t.Fatalf("unexpected Analytics parent claims: %+v", parent)
			}
			var input analyticsmodel.AuthorizationDecisionRequest
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil ||
				input.TenantID != fixture.tenantID || input.Action != analyticsmodel.EnergySeriesAction {
				t.Fatal("invalid Analytics authorization request")
			}
			allowed := !fixture.denySite.Load() && input.SiteID == fixture.siteID
			reason := analyticsmodel.AuthorizationReasonDenyAction
			if allowed {
				reason = analyticsmodel.AuthorizationReasonAllowRole
			}
			return telemetryJSONResponse(http.StatusOK, analyticsmodel.AuthorizationDecisionResponse{
				Decision: analyticsmodel.AuthorizationDecision{
					Allowed: allowed, PrincipalID: "018f3e00-5000-7000-8000-000000000001",
					SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject,
					TenantID: fixture.tenantID, SiteID: input.SiteID, Action: input.Action,
					PolicyRevision: "analytics-policy-7", ReasonCode: reason, DecidedAt: now.Format(time.RFC3339Nano),
				},
			}), nil
		case "/internal/v1/principal/current":
			principal := identitycontext.UserPrincipal{Subject: parent.Subject, Issuer: parent.SubjectIssuer, Roles: []string{"operator"}}
			capabilities := []identitycontext.Capability{identitycontext.CapabilitySiteRead}
			if fixture.auditRead.Load() {
				capabilities = append(capabilities, identitycontext.CapabilityAuditRead)
			}
			return telemetryJSONResponse(http.StatusOK, identitycontext.InternalPrincipalResponse{
				PrincipalID: "018f3e00-5000-7000-8000-000000000001", Principal: principal,
				Context: identitycontext.PrincipalContext{
					InitiatingPrincipal: principal, ExecutingServicePrincipal: identitycontext.ServicePrincipal{Service: "platform-gateway", SPIFFEID: parent.ExecutingService},
					TenantID: parent.TenantID, Audience: parent.Audience, PolicyRevision: parent.PolicyRevision,
					DelegationExpiresAt: time.Unix(parent.ExpiresAt, 0).UTC().Format(time.RFC3339),
				},
				Authorization: identitycontext.EffectiveAuthorization{
					CapabilitySetVersion: identitycontext.CapabilitySetVersion, PolicyRevision: "registry-read:1/iam:1", Capabilities: capabilities,
				},
			}), nil
		default:
			t.Fatalf("unexpected IAM path %s", request.URL.Path)
			return nil, io.EOF
		}
	})}
}

// The Tool authorization route never calls the Agent; only the usage read (#597) is answered.
func (fixture *operationsGatewayFixture) operationsClient(t *testing.T, _ time.Time) *http.Client {
	t.Helper()
	return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		fixture.operationsCalls.Add(1)
		if strings.HasSuffix(request.URL.Path, "/operations/agent-usage") {
			fixture.lastUpstream.Store(request)
			days := make([]map[string]any, 14)
			for index := range days {
				days[index] = map[string]any{
					"date": time.Date(2026, 10, 5+index, 0, 0, 0, 0, time.UTC).Format("2006-01-02"), "completed": index, "failed": 1, "cancelled": 0,
					"averageDurationMs": 7000, "inputTokens": 1200, "outputTokens": 300,
					"tools": []map[string]any{{"toolName": "alarm_list", "calls": 2, "failed": 1, "failureReasons": fixture.usageReasons()}},
				}
			}
			if fixture.usageExtraField.Load() {
				days[0]["question"] = "10月17日用了多少电？"
			}
			return telemetryJSONResponse(http.StatusOK, map[string]any{"schemaVersion": 1, "timezone": "Asia/Shanghai", "days": days}), nil
		}
		t.Errorf("unexpected Operations Agent call %s %s", request.Method, request.URL.Path)
		return nil, http.ErrHandlerTimeout
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

func TestOperationsToolAuthorizationIssuesExactOwnerGrants(t *testing.T) {
	fixture := newOperationsGatewayFixture(t, 30)
	serviceGrant := fixture.serviceDelegation(t)
	registryBody := `{"sessionId":"session-001","runId":"run-001","request":{"requestId":"registry-assets-001","tool":"registry.listSiteAssets","input":{"siteId":"` + fixture.siteID + `"}}}`
	registryResponse := fixture.authorizeTool(t, registryBody, serviceGrant, "spiffe://hvac.local/operations-agent-service")
	if registryResponse.Code != http.StatusOK {
		t.Fatalf("expected Registry grant, got %d: %s", registryResponse.Code, registryResponse.Body.String())
	}
	var registryGrant operationsToolAuthorizationResponse
	if err := json.Unmarshal(registryResponse.Body.Bytes(), &registryGrant); err != nil || registryGrant.DelegationGrant == "" || registryGrant.PolicyRevision != "identity-policy-1" {
		t.Fatalf("invalid Registry grant response: %+v err=%v", registryGrant, err)
	}

	query := analyticsmodel.EnergySeriesQuery{
		TenantID:      fixture.tenantID,
		SiteID:        fixture.siteID,
		EnergyType:    analyticsmodel.EnergyTypeElectricity,
		Granularity:   analyticsmodel.GranularityHour,
		Timezone:      "Asia/Tokyo",
		From:          time.Date(2026, 7, 29, 13, 0, 0, 0, time.UTC),
		To:            time.Date(2026, 7, 29, 21, 0, 0, 0, time.UTC),
		QualityPolicy: analyticsmodel.QualityPolicyValidOnly,
	}
	energyBody, err := json.Marshal(map[string]any{
		"sessionId": "session-001",
		"runId":     "run-001",
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
		claims.Audience != "telemetry-query-service" || claims.TenantID != fixture.tenantID ||
		len(claims.Actions) != 1 || claims.Actions[0] != analyticsmodel.EnergySeriesAction ||
		len(claims.Scopes) != 1 || claims.Scopes[0] != digest || claims.PolicyRevision != "analytics-policy-7" {
		t.Fatalf("Energy grant is not exact: %+v digest=%s", claims, digest)
	}
	telemetryBody, err := json.Marshal(map[string]any{
		"sessionId": "session-001",
		"runId":     "run-001",
		"request": map[string]any{
			"requestId": "telemetry-current-001",
			"tool":      "telemetry.current.getDeviceObservationSnapshot",
			"input": map[string]any{
				"siteId":    fixture.siteID,
				"deviceId":  fixture.deviceID,
				"pointKeys": []string{"condenserPressureKPa"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	telemetryResponse := fixture.authorizeTool(t, string(telemetryBody), serviceGrant, "spiffe://hvac.local/operations-agent-service")
	if telemetryResponse.Code != http.StatusOK {
		t.Fatalf("expected Telemetry authorization, got %d: %s", telemetryResponse.Code, telemetryResponse.Body.String())
	}
	var telemetryAuthorization operationsToolAuthorizationResponse
	if err := json.Unmarshal(telemetryResponse.Body.Bytes(), &telemetryAuthorization); err != nil || telemetryAuthorization.DelegationGrant == "" || telemetryAuthorization.PolicyRevision != "telemetry-access:1" {
		t.Fatalf("invalid Telemetry authorization response: %+v err=%v", telemetryAuthorization, err)
	}
	if fixture.iamCalls.Load() != 3 {
		t.Fatalf("expected three exact IAM decisions, got %d", fixture.iamCalls.Load())
	}
}

func TestOperationsToolAuthorizationFailsClosedBeforeIAM(t *testing.T) {
	fixture := newOperationsGatewayFixture(t, 30)
	serviceGrant := fixture.serviceDelegation(t)
	validBody := `{"sessionId":"session-001","runId":"run-001","request":{"requestId":"registry-site-001","tool":"registry.getSite","input":{"siteId":"` + fixture.siteID + `"}}}`

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

	otherSiteID := fixture.siteID[:len(fixture.siteID)-1] + "2"
	crossSiteBody, err := json.Marshal(map[string]any{
		"sessionId": "session-001",
		"runId":     "run-001",
		"request": map[string]any{
			"requestId": "telemetry-cross-site-001",
			"tool":      "telemetry.current.getDeviceObservationSnapshot",
			"input": map[string]any{
				"siteId":    otherSiteID,
				"deviceId":  fixture.deviceID,
				"pointKeys": []string{"condenserPressureKPa"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	workloadID := fixture.handler.(*handler).operations.workloadSPIFFEID
	crossSiteResponse := fixture.authorizeTool(t, string(crossSiteBody), serviceGrant, workloadID)
	if crossSiteResponse.Code != http.StatusForbidden {
		t.Fatalf("expected cross-Site Telemetry rejection before IAM, got %d: %s", crossSiteResponse.Code, crossSiteResponse.Body.String())
	}
	if fixture.iamCalls.Load() != 0 {
		t.Fatalf("cross-Site Telemetry request reached IAM %d times", fixture.iamCalls.Load())
	}

	unknownInput := `{"sessionId":"session-001","runId":"run-001","request":{"requestId":"registry-site-001","tool":"registry.getSite","input":{"siteId":"` + fixture.siteID + `","payload":{"bypass":true}}}}`
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

func TestOperationsToolAuthorizationGrantsTheSiteAssetModelOnlyForTheOperatorsSite(t *testing.T) {
	fixture := newOperationsGatewayFixture(t, 30)
	serviceGrant := fixture.serviceDelegation(t)
	body := `{"sessionId":"session-001","runId":"run-001","request":{"requestId":"registry-asset-model-001","tool":"registry.getSiteAssetModel","input":{"siteId":"` + fixture.siteID + `"}}}`
	response := fixture.authorizeTool(t, body, serviceGrant, "spiffe://hvac.local/operations-agent-service")
	if response.Code != http.StatusOK {
		t.Fatalf("expected an asset model grant, got %d: %s", response.Code, response.Body.String())
	}
	if len(fixture.registryActions) != 1 || fixture.registryActions[0] != registryauth.ActionAssetModelRead {
		t.Fatalf("IAM was asked for %v, want asset-model.read", fixture.registryActions)
	}

	fixture.denySite.Store(true)
	denied := fixture.authorizeTool(t, strings.Replace(body, "registry-asset-model-001", "registry-asset-model-002", 2), serviceGrant, "spiffe://hvac.local/operations-agent-service")
	if denied.Code != http.StatusNotFound {
		t.Fatalf("a Site the operator cannot see must be not found, got %d", denied.Code)
	}
}

func TestOperationsToolAuthorizationGrantsDeviceHistoryAggregatesToTheAgentForTheOperatorsDevice(t *testing.T) {
	fixture := newOperationsGatewayFixture(t, 30)
	serviceGrant := fixture.serviceDelegation(t)
	query := telemetryhistorymodel.DeviceHistoryAggregateQuery{
		TenantID: fixture.tenantID, SiteID: fixture.siteID, DeviceID: fixture.deviceID,
		Keys: []string{"chw_supply_temp"},
		From: time.Date(2026, 7, 30, 15, 0, 0, 0, time.UTC), To: time.Date(2026, 7, 31, 15, 0, 0, 0, time.UTC),
		Granularity: telemetryhistorymodel.AggregateGranularityHour, Timezone: "Asia/Shanghai",
		QualityPolicy: telemetryhistorymodel.AggregateQualityValidOnly,
	}
	body := func(requestID string) string {
		raw, err := json.Marshal(map[string]any{
			"sessionId": "session-001",
			"runId":     "run-001",
			"request":   map[string]any{"requestId": requestID, "tool": "telemetry.history.aggregateDeviceHistory", "input": query},
		})
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}

	response := fixture.authorizeTool(t, body("history-001"), serviceGrant, "spiffe://hvac.local/operations-agent-service")
	if response.Code != http.StatusOK {
		t.Fatalf("expected a Device History grant, got %d: %s", response.Code, response.Body.String())
	}
	var issued operationsToolAuthorizationResponse
	if err := json.Unmarshal(response.Body.Bytes(), &issued); err != nil {
		t.Fatal(err)
	}
	claims, err := identitycontext.VerifyDelegation(&fixture.gatewaySigner.PublicKey, issued.DelegationGrant)
	if err != nil {
		t.Fatalf("invalid Device History delegation: %v", err)
	}
	digest, err := query.ScopeDigest()
	if err != nil {
		t.Fatal(err)
	}
	if claims.ExecutingService != "spiffe://hvac.local/operations-agent-service" ||
		claims.Audience != "telemetry-query-service" || claims.TenantID != fixture.tenantID ||
		len(claims.Actions) != 1 || claims.Actions[0] != telemetryhistorymodel.DeviceHistoryAggregateAction ||
		len(claims.Scopes) != 1 || claims.Scopes[0] != digest || issued.PolicyRevision != claims.PolicyRevision {
		t.Fatalf("Device History grant is not exact: %+v digest=%s", claims, digest)
	}

	fixture.denySite.Store(true)
	denied := fixture.authorizeTool(t, body("history-002"), serviceGrant, "spiffe://hvac.local/operations-agent-service")
	if denied.Code != http.StatusNotFound {
		t.Fatalf("a device the operator cannot see must be not found, got %d", denied.Code)
	}
}

// The Agent's semantic query (#573) is granted like an Energy Series read, bound to that query, and
// only for the operator's Site; a member the catalog does not list is refused before IAM.
func TestOperationsToolAuthorizationGrantsASemanticQueryOnlyForTheOperatorsSite(t *testing.T) {
	fixture := newOperationsGatewayFixture(t, 30)
	serviceGrant := fixture.serviceDelegation(t)
	query := analyticsmodel.SemanticQuery{
		TenantID: fixture.tenantID, SiteID: fixture.siteID, View: "site_energy",
		Measures: []string{"electricity_valid_kwh", "cooling_valid_kwh", "cop"}, Granularity: analyticsmodel.GranularityDay,
		Timezone: "Asia/Tokyo", From: time.Date(2026, 7, 28, 15, 0, 0, 0, time.UTC), To: time.Date(2026, 7, 30, 15, 0, 0, 0, time.UTC),
	}
	body := func(requestID string, input any) string {
		raw, err := json.Marshal(map[string]any{
			"sessionId": "session-001", "runId": "run-001",
			"request": map[string]any{"requestId": requestID, "tool": "analytics.semanticQuery", "input": input},
		})
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}

	response := fixture.authorizeTool(t, body("semantic-001", query), serviceGrant, "spiffe://hvac.local/operations-agent-service")
	if response.Code != http.StatusOK {
		t.Fatalf("expected a semantic query grant, got %d: %s", response.Code, response.Body.String())
	}
	var issued operationsToolAuthorizationResponse
	if err := json.Unmarshal(response.Body.Bytes(), &issued); err != nil {
		t.Fatal(err)
	}
	claims, err := identitycontext.VerifyDelegation(&fixture.gatewaySigner.PublicKey, issued.DelegationGrant)
	if err != nil {
		t.Fatalf("invalid semantic query delegation: %v", err)
	}
	digest, err := query.ScopeDigest()
	if err != nil {
		t.Fatal(err)
	}
	if claims.ExecutingService != "spiffe://hvac.local/operations-agent-service" || claims.Audience != "telemetry-query-service" ||
		claims.TenantID != fixture.tenantID || len(claims.Actions) != 1 || claims.Actions[0] != analyticsmodel.EnergySeriesAction ||
		len(claims.Scopes) != 1 || claims.Scopes[0] != digest {
		t.Fatalf("semantic query grant is not exact: %+v digest=%s", claims, digest)
	}

	elsewhere := query
	elsewhere.SiteID = "018f1e00-1000-7000-8000-0000000000ff"
	// The Agent's own delegation covers only the operator's Site, so another Site stops there.
	if response := fixture.authorizeTool(t, body("semantic-002", elsewhere), serviceGrant, "spiffe://hvac.local/operations-agent-service"); response.Code != http.StatusForbidden || strings.Contains(response.Body.String(), "delegationGrant") {
		t.Fatalf("another Site's semantic query got %d: %s", response.Code, response.Body.String())
	}

	decisions := fixture.iamCalls.Load()
	internal := query
	internal.View = "site_energy_facts"
	if response := fixture.authorizeTool(t, body("semantic-003", internal), serviceGrant, "spiffe://hvac.local/operations-agent-service"); response.Code != http.StatusBadRequest {
		t.Fatalf("an internal cube got %d: %s", response.Code, response.Body.String())
	}
	if fixture.iamCalls.Load() != decisions {
		t.Fatal("a semantic query outside the catalog reached IAM")
	}
}

// #594: the Agent lists the operator's Site's alarms from the alarm owner with a grant naming it.
func TestOperationsToolAuthorizationGrantsAnAlarmListOnlyForTheOperatorsSite(t *testing.T) {
	fixture := newOperationsGatewayFixture(t, 30)
	serviceGrant := fixture.serviceDelegation(t)
	body := func(requestID, siteID string) string {
		return `{"sessionId":"session-001","runId":"run-001","request":{"requestId":"` + requestID + `","tool":"alarm.listAlarms","input":{"siteId":"` + siteID + `","condition":"ACTIVE","limit":50}}}`
	}
	response := fixture.authorizeTool(t, body("alarm-001", fixture.siteID), serviceGrant, "spiffe://hvac.local/operations-agent-service")
	if response.Code != http.StatusOK {
		t.Fatalf("expected an alarm list grant, got %d: %s", response.Code, response.Body.String())
	}
	var issued operationsToolAuthorizationResponse
	if err := json.Unmarshal(response.Body.Bytes(), &issued); err != nil {
		t.Fatal(err)
	}
	claims, err := identitycontext.VerifyDelegation(&fixture.gatewaySigner.PublicKey, issued.DelegationGrant)
	if err != nil {
		t.Fatalf("invalid alarm list delegation: %v", err)
	}
	if claims.Issuer != "spiffe://hvac.local/platform-gateway" || claims.ExecutingService != "spiffe://hvac.local/operations-agent-service" ||
		claims.Audience != "alarm-service" || claims.TenantID != fixture.tenantID ||
		len(claims.Actions) != 1 || claims.Actions[0] != "alarm:read" ||
		len(claims.Scopes) != 2 || claims.Scopes[0] != "tenant:"+fixture.tenantID || claims.Scopes[1] != "site:"+fixture.siteID {
		t.Fatalf("alarm list grant is not exact: %+v", claims)
	}

	// Another Site stops at the Agent's own delegation, before IAM and before any alarm is read.
	decisions := fixture.iamCalls.Load()
	elsewhere := fixture.authorizeTool(t, body("alarm-002", "018f1e00-1000-7000-8000-0000000000ff"), serviceGrant, "spiffe://hvac.local/operations-agent-service")
	if elsewhere.Code != http.StatusForbidden || strings.Contains(elsewhere.Body.String(), "delegationGrant") || fixture.iamCalls.Load() != decisions {
		t.Fatalf("another Site's alarms got %d (IAM calls %d→%d): %s", elsewhere.Code, decisions, fixture.iamCalls.Load(), elsewhere.Body.String())
	}

	fixture.denySite.Store(true)
	if denied := fixture.authorizeTool(t, body("alarm-003", fixture.siteID), serviceGrant, "spiffe://hvac.local/operations-agent-service"); denied.Code != http.StatusNotFound {
		t.Fatalf("a Site whose alarms the operator may not read must be not found, got %d", denied.Code)
	}
}

// #595: the Agent lists the operator's Site's work orders from the owner with a context naming it.
func TestOperationsToolAuthorizationGrantsAWorkOrderListOnlyForTheOperatorsSite(t *testing.T) {
	fixture := newOperationsGatewayFixture(t, 30)
	serviceGrant := fixture.serviceDelegation(t)
	body := func(requestID, siteID string) string {
		return `{"sessionId":"session-001","runId":"run-001","request":{"requestId":"` + requestID + `","tool":"workorder.listWorkOrders","input":{"siteId":"` + siteID + `","limit":100}}}`
	}
	response := fixture.authorizeTool(t, body("work-order-001", fixture.siteID), serviceGrant, "spiffe://hvac.local/operations-agent-service")
	if response.Code != http.StatusOK {
		t.Fatalf("expected a work-order list grant, got %d: %s", response.Code, response.Body.String())
	}
	var issued operationsToolAuthorizationResponse
	if err := json.Unmarshal(response.Body.Bytes(), &issued); err != nil {
		t.Fatal(err)
	}
	claims, err := identitycontext.VerifyDelegation(&fixture.gatewaySigner.PublicKey, issued.DelegationGrant)
	if err != nil {
		t.Fatalf("invalid work-order delegation: %v", err)
	}
	if claims.ExecutingService != "spiffe://hvac.local/operations-agent-service" || claims.Audience != "work-order-service" ||
		len(claims.Actions) != 1 || claims.Actions[0] != "work-order:list" ||
		len(claims.Scopes) != 2 || claims.Scopes[0] != "tenant:"+fixture.tenantID || claims.Scopes[1] != "site:"+fixture.siteID {
		t.Fatalf("work-order list grant is not exact: %+v", claims)
	}

	decisions := fixture.iamCalls.Load()
	elsewhere := fixture.authorizeTool(t, body("work-order-002", "018f1e00-1000-7000-8000-0000000000ff"), serviceGrant, "spiffe://hvac.local/operations-agent-service")
	if elsewhere.Code != http.StatusForbidden || strings.Contains(elsewhere.Body.String(), "delegationGrant") || fixture.iamCalls.Load() != decisions {
		t.Fatalf("another Site's work orders got %d (IAM calls %d→%d)", elsewhere.Code, decisions, fixture.iamCalls.Load())
	}

	fixture.denySite.Store(true)
	if denied := fixture.authorizeTool(t, body("work-order-003", fixture.siteID), serviceGrant, "spiffe://hvac.local/operations-agent-service"); denied.Code != http.StatusNotFound {
		t.Fatalf("a Site whose work orders the operator may not read must be not found, got %d", denied.Code)
	}
}

// The usage figures' reasons for the one failed alarm read; with usageTooManyFailures they claim
// more failures than the read had.
func (fixture *operationsGatewayFixture) usageReasons() []map[string]any {
	count := 1
	if fixture.usageTooManyFailures.Load() {
		count = 2
	}
	return []map[string]any{{"code": "TOOL_OWNER_UNAVAILABLE", "count": count}}
}
