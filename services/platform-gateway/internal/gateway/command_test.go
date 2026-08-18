package gateway

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandauth"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/libs/sessionstore"
	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
	"github.com/quanlaihe/hvac-web/services/platform-gateway/pkg/platformapi"
	"github.com/quanlaihe/hvac-web/services/platform-gateway/pkg/s2telemetryapi"
)

func TestGatewayCreateCommandDerivesAuthorityAndCurrentState(t *testing.T) {
	fixture := newCommandGatewayFixture(t)
	body := `{"equipmentId":"` + fixture.equipmentID + `","commandPointId":"` + fixture.commandPointID + `","parameters":{"setpointC":24.5}}`
	request := httptest.NewRequest(http.MethodPost, publicCommandsPath, strings.NewReader(body))
	fixture.authenticate(request, true)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "command-request-1")
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Location") != "/api/v1/commands/"+fixture.commandID {
		t.Fatalf("location=%s", recorder.Header().Get("Location"))
	}
	if fixture.iamCalls.Load() != 4 || fixture.registryCalls.Load() != 2 || fixture.telemetryCalls.Load() != 1 || fixture.commandCalls.Load() != 1 {
		t.Fatalf("calls iam=%d registry=%d telemetry=%d command=%d", fixture.iamCalls.Load(), fixture.registryCalls.Load(), fixture.telemetryCalls.Load(), fixture.commandCalls.Load())
	}
	var view commandView
	if json.NewDecoder(recorder.Body).Decode(&view) != nil || view.CommandID != fixture.commandID ||
		view.OrganizationID != fixture.organizationID || view.SiteID != fixture.siteID || view.SnapshotRevision != 17 {
		t.Fatalf("unexpected view %#v", view)
	}
}

func TestGatewayCreateCommandFailsBeforeUpstreamsWithoutCSRF(t *testing.T) {
	fixture := newCommandGatewayFixture(t)
	body := `{"equipmentId":"` + fixture.equipmentID + `","commandPointId":"` + fixture.commandPointID + `","parameters":{"setpointC":24.5}}`
	request := httptest.NewRequest(http.MethodPost, publicCommandsPath, strings.NewReader(body))
	fixture.authenticate(request, false)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "command-request-2")
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if fixture.totalUpstreamCalls() != 0 {
		t.Fatal("upstream called before CSRF validation")
	}
}

func TestGatewayRejectsBrowserCommandAuthorityHeaders(t *testing.T) {
	fixture := newCommandGatewayFixture(t)
	request := httptest.NewRequest(http.MethodPost, publicCommandsPath, strings.NewReader(`{}`))
	fixture.authenticate(request, true)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "command-request-3")
	request.Header.Set("X-Command-Grant", "caller-supplied")
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if fixture.totalUpstreamCalls() != 0 {
		t.Fatal("caller-supplied internal header reached upstream")
	}
}

func TestGatewayUnsafeCurrentStateStopsBeforeCommandAuthorization(t *testing.T) {
	fixture := newCommandGatewayFixture(t)
	fixture.unsafeState.Store(true)
	body := `{"equipmentId":"` + fixture.equipmentID + `","commandPointId":"` + fixture.commandPointID + `","parameters":{"setpointC":24.5}}`
	request := httptest.NewRequest(http.MethodPost, publicCommandsPath, strings.NewReader(body))
	fixture.authenticate(request, true)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "command-request-4")
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if fixture.iamCalls.Load() != 3 || fixture.registryCalls.Load() != 2 || fixture.telemetryCalls.Load() != 1 || fixture.commandCalls.Load() != 0 {
		t.Fatalf("unsafe calls iam=%d registry=%d telemetry=%d command=%d", fixture.iamCalls.Load(), fixture.registryCalls.Load(), fixture.telemetryCalls.Load(), fixture.commandCalls.Load())
	}
}

func TestGatewayGetCommandUsesScopedReadContext(t *testing.T) {
	fixture := newCommandGatewayFixture(t)
	request := httptest.NewRequest(http.MethodGet, publicCommandsPath+"/"+fixture.commandID, nil)
	fixture.authenticate(request, false)
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if fixture.commandCalls.Load() != 1 || fixture.iamCalls.Load()+fixture.registryCalls.Load()+fixture.telemetryCalls.Load() != 0 {
		t.Fatal("command read called unrelated upstreams")
	}
}

func TestGatewayGetCommandRejectsCrossOrganizationProjection(t *testing.T) {
	fixture := newCommandGatewayFixture(t)
	fixture.crossOrganizationView.Store(true)
	request := httptest.NewRequest(http.MethodGet, publicCommandsPath+"/"+fixture.commandID, nil)
	fixture.authenticate(request, false)
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "018f3e00-1000-7000-8000-000000000002") {
		t.Fatal("cross-Organization Command projection leaked its scope")
	}
}

func TestGatewayApproveCommandDerivesIdentityRoleAndExactGrant(t *testing.T) {
	fixture := newCommandGatewayFixture(t)
	fixture.approvalPending.Store(true)
	request := httptest.NewRequest(http.MethodPost, publicCommandsPath+"/"+fixture.commandID+"/approve", strings.NewReader(`{}`))
	fixture.authenticate(request, true)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if fixture.iamCalls.Load() != 2 || fixture.registryCalls.Load() != 1 || fixture.telemetryCalls.Load() != 0 || fixture.commandCalls.Load() != 2 {
		t.Fatalf("approval calls iam=%d registry=%d telemetry=%d command=%d", fixture.iamCalls.Load(), fixture.registryCalls.Load(), fixture.telemetryCalls.Load(), fixture.commandCalls.Load())
	}
	var view commandView
	if json.NewDecoder(recorder.Body).Decode(&view) != nil || view.Status != commandmodel.IntentQueued || view.ApprovalCount != 1 || view.RequiredApprovalCount != 1 || len(view.Transitions) != 5 {
		t.Fatalf("unexpected approved view %#v", view)
	}
}

func TestGatewayHighRiskApprovalRequiresStepUpBeforeAuthorizationUpstreams(t *testing.T) {
	fixture := newCommandGatewayFixture(t)
	fixture.approvalPending.Store(true)
	fixture.highRiskApproval.Store(true)
	request := httptest.NewRequest(http.MethodPost, publicCommandsPath+"/"+fixture.commandID+"/approve", strings.NewReader(`{}`))
	fixture.authenticate(request, true)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusPreconditionRequired {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var problem platformapi.ProblemDetails
	if err := json.NewDecoder(recorder.Body).Decode(&problem); err != nil || problem.Code != "STEP_UP_REQUIRED" {
		t.Fatalf("unexpected step-up problem %#v err=%v", problem, err)
	}
	if fixture.commandCalls.Load() != 1 || fixture.iamCalls.Load()+fixture.registryCalls.Load()+fixture.telemetryCalls.Load() != 0 {
		t.Fatalf("step-up boundary reached authorization upstreams: iam=%d registry=%d telemetry=%d command=%d", fixture.iamCalls.Load(), fixture.registryCalls.Load(), fixture.telemetryCalls.Load(), fixture.commandCalls.Load())
	}
}

func TestGatewayApprovalRejectsBrowserAuthorityBeforeUpstreams(t *testing.T) {
	fixture := newCommandGatewayFixture(t)
	request := httptest.NewRequest(http.MethodPost, publicCommandsPath+"/"+fixture.commandID+"/approve", strings.NewReader(`{"principalId":"caller-supplied"}`))
	fixture.authenticate(request, true)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if fixture.totalUpstreamCalls() != 0 {
		t.Fatal("browser approval authority reached an upstream")
	}
}

func TestGatewayApprovalRequiresCSRFBeforeCommandRead(t *testing.T) {
	fixture := newCommandGatewayFixture(t)
	request := httptest.NewRequest(http.MethodPost, publicCommandsPath+"/"+fixture.commandID+"/approve", strings.NewReader(`{}`))
	fixture.authenticate(request, false)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if fixture.totalUpstreamCalls() != 0 {
		t.Fatal("approval read occurred before CSRF validation")
	}
}

func TestCommandRegistryDecisionUsesRegistryRouteOwnership(t *testing.T) {
	fixture := newCommandGatewayFixture(t)
	snapshot, err := ownershipregistry.Parse([]byte(`{
		"registryVersion":1,
		"registryRevision":44,
		"routes":[{
			"method":"GET",
			"path":"/api/v1/devices/{deviceId}",
			"owner":"platform-core-service",
			"publicIngress":"platform-gateway",
			"revision":9,
			"rollout":{"mode":"all"},
			"compatibilityMode":"native",
			"allowedScopeDimensions":["tenant","site","device","principal"]
		}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	fixture.handler.routeManager = ownershipregistry.NewManager(snapshot, ownershipregistry.NewMemoryAuditSink(), func() time.Time {
		return time.Date(2026, 7, 26, 15, 0, 0, 0, time.UTC)
	})
	route, _, ok := matchPublicRegistryRoute("/api/v1/devices/" + fixture.deviceID)
	if !ok {
		t.Fatal("device Registry route did not match")
	}
	request := httptest.NewRequest(http.MethodPost, publicCommandsPath, nil)
	decision, failure := fixture.handler.commandRegistryDecision(request, bffSession{Session: sessionstore.Session{ActingOrganizationID: fixture.organizationID}}, route, fixture.deviceID)
	if failure != nil {
		t.Fatalf("failure=%+v", *failure)
	}
	if decision.SelectedOwner != ownershipregistry.OwnerCore ||
		decision.PathTemplate != platformapi.GetDevicePathTemplate || decision.RegistryRevision != 44 || decision.RouteRevision != 9 {
		t.Fatalf("unexpected Registry decision %+v", decision)
	}
}

type commandGatewayFixture struct {
	handler               *handler
	sessionID             string
	tenantID              string
	organizationID        string
	siteID                string
	equipmentID           string
	deviceID              string
	commandPointID        string
	feedbackPointID       string
	controlRelationshipID string
	commandID             string
	principalID           string
	gatewaySigner         *ecdsa.PrivateKey
	iamSigner             *ecdsa.PrivateKey
	iamCalls              atomic.Int32
	registryCalls         atomic.Int32
	telemetryCalls        atomic.Int32
	commandCalls          atomic.Int32
	unsafeState           atomic.Bool
	crossOrganizationView atomic.Bool
	approvalPending       atomic.Bool
	highRiskApproval      atomic.Bool
	approvalCompleted     atomic.Bool
}

func newCommandGatewayFixture(t *testing.T) *commandGatewayFixture {
	t.Helper()
	now := time.Date(2026, 7, 26, 15, 0, 0, 0, time.UTC)
	gatewaySigner := commandTestSigner(t)
	iamSigner := commandTestSigner(t)
	fixture := &commandGatewayFixture{
		tenantID:              "018f3d00-1000-7000-8000-000000000001",
		organizationID:        "018f3e00-1000-7000-8000-000000000001",
		siteID:                "018f3e00-2000-7000-8000-000000000001",
		deviceID:              "018f3e00-3000-7000-8000-000000000001",
		commandID:             "018f3e00-4000-7000-8000-000000000001",
		principalID:           "018f3e00-5000-7000-8000-000000000001",
		equipmentID:           "018f3e00-6000-7000-8000-000000000001",
		commandPointID:        "018f3e00-7000-7000-8000-000000000001",
		feedbackPointID:       "018f3e00-8000-7000-8000-000000000001",
		controlRelationshipID: "018f3e00-9000-7000-8000-000000000001",
		gatewaySigner:         gatewaySigner,
		iamSigner:             iamSigner,
	}
	store := sessionstore.NewMemoryStore()
	configured := NewHandler(Config{
		Now: func() time.Time { return now },
		Identity: &IdentityConfig{
			OIDCIssuer: "https://issuer.example.test", OIDCClientID: "client", OIDCRedirectURI: "https://web.example.test/api/v1/auth/callback",
			PublicOrigin: "https://web.example.test", IAMURL: "https://iam.example.test", IAMAudience: "iam-service",
			ExecutingWorkloadSPIFFE: "spiffe://hvac.local/platform-gateway", PolicyRevision: "identity-policy-1", DelegationSigner: gatewaySigner,
			TokenEncryptionKey: make([]byte, 32), SessionStore: store, SessionTTL: time.Hour, DelegationTTL: 30 * time.Second,
			IAMHTTPClient: fixture.commandIAMClient(t, now),
		},
		Registry:  &RegistryConfig{CoreBaseURL: "https://registry.example.test", CoreHTTPClient: fixture.commandRegistryClient(t, now), CoreTimeout: time.Second},
		Telemetry: &TelemetryConfig{RuntimeBaseURL: "https://telemetry.example.test", RuntimeHTTPClient: fixture.commandTelemetryClient(t, now), RuntimeAudience: "telemetry-runtime-service", Timeout: time.Second},
		Command:   &CommandConfig{BackendBaseURL: "https://command.example.test", BackendHTTPClient: fixture.commandBackendClient(t, now), BackendAudience: "command-service", IAMGrantIssuer: "spiffe://hvac.local/iam-service", Timeout: time.Second},
	}).(*handler)
	fixture.handler = configured
	csrfCiphertext, err := configured.identity.encryptBytes([]byte(commandTestCSRFValue()))
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateSession(context.Background(), sessionstore.Session{
		ID: "command-session", Principal: identitycontext.UserPrincipal{Subject: "command-user", Issuer: "https://issuer.example.test", Roles: []string{"operator"}},
		ActingOrganizationID: fixture.organizationID, CSRFTokenCiphertext: csrfCiphertext, ExpiresAt: now.Add(time.Hour),
	}, sessionstore.MutationContext{
		Action: "SESSION_CREATED", Result: "SUCCEEDED", PolicyRevision: "identity-policy-1", CorrelationID: "command-fixture",
		TraceID: strings.Repeat("a", 32), Traceparent: "00-" + strings.Repeat("a", 32) + "-" + strings.Repeat("b", 16) + "-01",
		ExecutingService: "platform-gateway", ExecutingSPIFFEID: "spiffe://hvac.local/platform-gateway", OccurredAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.sessionID = created.ID
	return fixture
}

func (fixture *commandGatewayFixture) authenticate(request *http.Request, includeCSRF bool) {
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: fixture.sessionID})
	if includeCSRF {
		request.Header.Set("Origin", "https://web.example.test")
		request.Header.Set("X-CSRF-Token", commandTestCSRFValue())
	}
}

func (fixture *commandGatewayFixture) totalUpstreamCalls() int32 {
	return fixture.iamCalls.Load() + fixture.registryCalls.Load() + fixture.telemetryCalls.Load() + fixture.commandCalls.Load()
}

func commandTestCSRFValue() string {
	return strings.Join([]string{"command", "csrf", "fixture"}, "-")
}

func (fixture *commandGatewayFixture) commandIAMClient(t *testing.T, now time.Time) *http.Client {
	t.Helper()
	return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		fixture.iamCalls.Add(1)
		assertCommandInternalHeaders(t, request)
		parent, err := identitycontext.VerifyDelegation(&fixture.gatewaySigner.PublicKey, request.Header.Get("X-Delegation-Grant"))
		if err != nil {
			t.Fatalf("invalid gateway delegation: %v", err)
		}
		switch request.URL.Path {
		case "/internal/v1/registry-read/decision":
			return fixture.registryDecisionResponse(t, request, parent, now), nil
		case telemetryDecisionPath:
			return fixture.telemetryDecisionResponse(t, request, parent, now), nil
		case commandDecisionPath:
			return fixture.commandDecisionResponse(t, request, parent, now), nil
		default:
			t.Fatalf("unexpected IAM path %s", request.URL.Path)
			return nil, io.EOF
		}
	})}
}

func (fixture *commandGatewayFixture) registryDecisionResponse(t *testing.T, request *http.Request, parent identitycontext.DelegationClaims, now time.Time) *http.Response {
	t.Helper()
	var input registryauth.DecisionRequest
	if json.NewDecoder(request.Body).Decode(&input) != nil ||
		(input.Action != registryauth.ActionEquipmentRead && input.Action != registryauth.ActionAssetModelRead && input.Action != registryauth.ActionDeviceRead) {
		t.Fatal("invalid Registry decision request")
	}
	decision := registryauth.Decision{
		Allowed: true, PrincipalID: fixture.principalID, SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject,
		ActingOrganizationID: fixture.organizationID, AllowedSiteIDs: []string{fixture.siteID},
		Actions: []registryauth.Action{input.Action}, PolicyRevision: "identity-policy-1",
		ReasonCode: registryauth.ReasonAllowSiteRole, DecidedAt: now.Format(time.RFC3339Nano),
	}
	claims := registryauth.GrantClaims{
		Issuer: "spiffe://hvac.local/iam-service", Presenter: "spiffe://hvac.local/platform-gateway", Audience: "platform-core-service",
		PrincipalID: fixture.principalID, SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject,
		ActingOrganizationID: fixture.organizationID, AllowedSiteIDs: []string{fixture.siteID},
		Actions: []registryauth.Action{input.Action}, PolicyRevision: "identity-policy-1",
		DecisionReason: registryauth.ReasonAllowSiteRole, SessionID: parent.SessionID, ParentTokenID: parent.TokenID,
		IssuedAt: now.Unix(), ExpiresAt: now.Add(30 * time.Second).Unix(), TokenID: randomURLToken(16),
	}
	return telemetryJSONResponse(http.StatusOK, registryauth.DecisionResponse{Decision: decision, DelegationGrant: commandUnsignedRegistryGrant(claims)})
}

func (fixture *commandGatewayFixture) telemetryDecisionResponse(t *testing.T, request *http.Request, parent identitycontext.DelegationClaims, now time.Time) *http.Response {
	t.Helper()
	var input telemetryauth.DecisionRequest
	if json.NewDecoder(request.Body).Decode(&input) != nil || input.Action != telemetryauth.ActionSnapshotRead || len(input.Targets) != 1 || input.Targets[0].DeviceID != fixture.deviceID {
		t.Fatal("invalid Telemetry decision request")
	}
	canonical, _ := telemetryauth.CanonicalTargets(input.Targets)
	digest, _ := telemetryauth.ScopeDigest(input.Action, input.ActingOrganizationID, canonical)
	decision := telemetryauth.Decision{
		Allowed: true, PrincipalID: fixture.principalID, SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject,
		ActingOrganizationID: fixture.organizationID, Action: input.Action, ScopeDigest: digest,
		PolicyRevision: "identity-policy-1", ReasonCode: telemetryauth.ReasonAllowExactScope,
		DecidedAt: now.Format(time.RFC3339Nano), Targets: []telemetryauth.AuthorizedTarget{{
			TenantID: fixture.tenantID, SiteID: fixture.siteID, DeviceID: fixture.deviceID, Keys: []string{defaultCommandTemperatureKey},
		}},
	}
	claims := telemetryauth.GrantClaims{
		Issuer: "spiffe://hvac.local/iam-service", Presenter: "spiffe://hvac.local/platform-gateway", Audience: "telemetry-runtime-service",
		PrincipalID: fixture.principalID, SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject,
		ActingOrganizationID: fixture.organizationID, ActorChain: []telemetryauth.Actor{{Service: "platform-gateway", SPIFFEID: "spiffe://hvac.local/platform-gateway"}},
		Action: input.Action, ScopeDigest: digest, TargetCount: 1, KeyCount: 1, PolicyRevision: "identity-policy-1",
		SessionID: parent.SessionID, ParentTokenID: parent.TokenID, RequestID: request.Header.Get("X-Request-ID"),
		TraceID: traceIDFromTraceparent(request.Header.Get("Traceparent")), Route: telemetryPublicRoute(input.Action),
		IssuedAt: now.Unix(), ExpiresAt: now.Add(30 * time.Second).Unix(), TokenID: randomURLToken(16),
	}
	return telemetryJSONResponse(http.StatusOK, telemetryauth.DecisionResponse{Decision: decision, DelegationGrant: unsignedTelemetryGrant(claims)})
}

func (fixture *commandGatewayFixture) commandDecisionResponse(t *testing.T, request *http.Request, parent identitycontext.DelegationClaims, now time.Time) *http.Response {
	t.Helper()
	var input commandauth.DecisionRequest
	if json.NewDecoder(request.Body).Decode(&input) != nil ||
		(input.Purpose != commandmodel.AuthorizationCommandSubmit && input.Purpose != commandmodel.AuthorizationCommandApprove) ||
		input.DeviceID != fixture.deviceID || input.SiteID != fixture.siteID {
		t.Fatal("invalid Command decision request")
	}
	decision := commandauth.Decision{
		Allowed: true, PrincipalID: fixture.principalID, SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject,
		ActingOrganizationID: fixture.organizationID, SiteID: fixture.siteID, DeviceID: fixture.deviceID,
		Capability: commandmodel.CapabilitySetTemperatureSetpoint, CapabilityRevision: "capability:set-temperature-setpoint:v1",
		Purpose: input.Purpose, MaximumRisk: commandmodel.RiskHigh,
		PolicyRevision: "identity-policy-1", EmergencyRevocationRevision: 3,
		ReasonCode: commandauth.ReasonAllowExactCapability, DecidedAt: now.Format(time.RFC3339Nano),
	}
	grantID := randomURLToken(16)
	grant, err := commandauth.SignGrant(fixture.iamSigner, commandauth.GrantClaims{
		Issuer: "spiffe://hvac.local/iam-service", Presenter: "spiffe://hvac.local/platform-gateway", Audience: "command-service",
		GrantID: grantID, Purpose: decision.Purpose, PrincipalID: fixture.principalID,
		OrganizationID: fixture.organizationID, SiteID: fixture.siteID, DeviceID: fixture.deviceID,
		Capability: decision.Capability, MaximumRisk: decision.MaximumRisk, CapabilityRevision: decision.CapabilityRevision,
		PolicyRevision: decision.PolicyRevision, EmergencyRevocationRevision: 3,
		IssuedAt: now.Unix(), ExpiresAt: now.Add(25 * time.Second).Unix(), TokenID: grantID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return telemetryJSONResponse(http.StatusOK, commandauth.DecisionResponse{Decision: decision, DelegationGrant: grant})
}

func (fixture *commandGatewayFixture) commandRegistryClient(t *testing.T, now time.Time) *http.Client {
	t.Helper()
	createdAt := now.Add(-time.Hour).Format("2006-01-02T15:04:05.000Z")
	updatedAt := now.Format("2006-01-02T15:04:05.000Z")
	unit := "Cel"
	equipment := platformapi.Equipment{
		ID: fixture.equipmentID, TenantID: fixture.tenantID, SiteID: fixture.siteID,
		Code: "AHU-01", DisplayName: "AHU 01", EquipmentType: "AHU", Status: "ACTIVE", Revision: 7,
		CreatedAt: createdAt, UpdatedAt: updatedAt,
	}
	device := platformapi.Device{
		ID: fixture.deviceID, TenantID: fixture.tenantID, SiteID: fixture.siteID,
		Code: "AHU-01-CTRL", DisplayName: "AHU 01 Controller", DeviceType: "AHU_CONTROLLER", Status: "ACTIVE", Revision: 7,
		CreatedAt: createdAt, UpdatedAt: updatedAt,
	}
	commandPoint := platformapi.TelemetryPoint{
		ID: fixture.commandPointID, TenantID: fixture.tenantID, SiteID: fixture.siteID, ReportingDeviceID: fixture.deviceID,
		PointCode: "zone_temperature_setpoint_command", SourceKey: "zone.temperature.setpoint.command", DisplayName: "Zone temperature setpoint command",
		PointType: "COMMAND", ValueType: "NUMBER", Unit: &unit, Writable: true, SampleIntervalMS: 1000, PublishIntervalMS: 1000, StaleAfterMS: 3000,
		SourceMetadata: map[string]any{
			"capability": commandmodel.CapabilitySetTemperatureSetpoint, "capabilityRevision": "capability:set-temperature-setpoint:v1",
			"feedbackSourceKey": defaultCommandTemperatureKey, "parameterKey": commandmodel.ParameterSetpointC,
		},
		Status: "ACTIVE", Revision: 1, CreatedAt: createdAt, UpdatedAt: updatedAt,
	}
	feedbackPoint := platformapi.TelemetryPoint{
		ID: fixture.feedbackPointID, TenantID: fixture.tenantID, SiteID: fixture.siteID, ReportingDeviceID: fixture.deviceID,
		PointCode: "zone_temperature", SourceKey: defaultCommandTemperatureKey, DisplayName: "Zone temperature",
		PointType: "TELEMETRY", ValueType: "NUMBER", Unit: &unit, Writable: false, SampleIntervalMS: 1000, PublishIntervalMS: 1000, StaleAfterMS: 3000,
		SourceMetadata: map[string]any{}, Status: "ACTIVE", Revision: 1, CreatedAt: createdAt, UpdatedAt: updatedAt,
	}
	assetModel := platformapi.SiteAssetModel{
		SchemaVersion: 2, TenantID: fixture.tenantID, SiteID: fixture.siteID, Equipment: []platformapi.Equipment{equipment}, Devices: []platformapi.Device{device},
		Areas: []platformapi.Area{}, Sensors: []platformapi.Sensor{}, TelemetryPoints: []platformapi.TelemetryPoint{commandPoint, feedbackPoint},
		Relationships: []platformapi.AssetRelationship{{
			ID: fixture.controlRelationshipID, TenantID: fixture.tenantID, SiteID: fixture.siteID,
			FromType: "POINT", FromID: fixture.commandPointID, ToType: "EQUIPMENT", ToID: fixture.equipmentID, Role: "CONTROLS", Status: "ACTIVE",
			ValidFrom: createdAt, ValidTo: nil, Revision: 1, CreatedAt: createdAt, UpdatedAt: updatedAt,
		}},
		Counts: platformapi.AssetModelCounts{Equipment: 1, DeviceEndpoints: 1, PhysicalSensors: 0, Points: 2},
	}
	return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		fixture.registryCalls.Add(1)
		assertCommandInternalHeaders(t, request)
		if request.Header.Get("X-Delegation-Grant") == "" {
			t.Fatal("missing Registry delegation grant")
		}
		switch request.URL.Path {
		case "/internal/v1/registry/equipment/" + fixture.equipmentID:
			return telemetryJSONResponse(http.StatusOK, equipment), nil
		case "/internal/v1/registry/sites/" + fixture.siteID + "/asset-model":
			return telemetryJSONResponse(http.StatusOK, assetModel), nil
		case "/internal/v1/registry/devices/" + fixture.deviceID:
			return telemetryJSONResponse(http.StatusOK, device), nil
		default:
			t.Fatalf("unexpected Registry request %s", request.URL.Path)
			return nil, io.EOF
		}
	})}
}

func (fixture *commandGatewayFixture) commandTelemetryClient(t *testing.T, now time.Time) *http.Client {
	t.Helper()
	return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		fixture.telemetryCalls.Add(1)
		assertCommandInternalHeaders(t, request)
		if request.URL.Path != internalTelemetrySinglePrefix+fixture.deviceID+"/observation-snapshot" || request.URL.Query().Get("key") != defaultCommandTemperatureKey {
			t.Fatalf("unexpected Telemetry Runtime request %s?%s", request.URL.Path, request.URL.RawQuery)
		}
		state := s2telemetryapi.DevicePresenceStateOnline
		readiness := s2telemetryapi.TelemetryReadinessCurrent
		freshness := "FRESH"
		if fixture.unsafeState.Load() {
			readiness = s2telemetryapi.TelemetryReadinessDegraded
			freshness = "STALE"
		}
		unit := "Cel"
		display := s2telemetryapi.DeviceDisplayStateOnline
		instant := s2telemetryapi.Instant(now.Add(-time.Second).Format(time.RFC3339Nano))
		policy := s2telemetryapi.PolicyRevision(5)
		snapshot := s2telemetryapi.DeviceObservationSnapshot{
			SchemaVersion: 1, TenantId: s2telemetryapi.UUIDv7(fixture.tenantID), SiteId: s2telemetryapi.UUIDv7(fixture.siteID), DeviceId: s2telemetryapi.UUIDv7(fixture.deviceID),
			BusinessRevision: 17, EvaluatedAt: s2telemetryapi.Instant(now.Format(time.RFC3339Nano)),
			EvaluationAvailability: s2telemetryapi.EvaluationAvailabilityAvailable, AvailabilityReasons: []s2telemetryapi.AvailabilityReasonCode{},
			Presence:           s2telemetryapi.PresenceSnapshot{Applicability: s2telemetryapi.PresenceApplicabilityApplicable, CurrentState: &state, LastSeenAt: &instant, PolicyRevision: &policy},
			TelemetryReadiness: readiness,
			DisplayState:       &display,
			Values: []s2telemetryapi.TelemetryKeyState{{Present: &s2telemetryapi.TelemetryPresentState{
				Key: s2telemetryapi.TelemetryKey(defaultCommandTemperatureKey), State: "PRESENT", Value: json.RawMessage(`23.0`), ValueType: "NUMBER", Unit: &unit,
				SampledAt: instant, ReceivedAt: instant, Freshness: freshness, Quality: s2telemetryapi.TelemetryQualityGood,
				QualityReasons: []s2telemetryapi.QualityReasonCode{}, PolicyRevision: policy,
			}}},
		}
		return telemetryJSONResponse(http.StatusOK, snapshot), nil
	})}
}

func (fixture *commandGatewayFixture) commandBackendClient(t *testing.T, now time.Time) *http.Client {
	t.Helper()
	return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		fixture.commandCalls.Add(1)
		assertCommandInternalHeaders(t, request)
		switch request.Method {
		case http.MethodPost:
			claims, err := commandauth.VerifyGrant(&fixture.iamSigner.PublicKey, request.Header.Get("X-Command-Grant"))
			if strings.HasSuffix(request.URL.Path, "/approve") {
				if err != nil || claims.Purpose != commandmodel.AuthorizationCommandApprove || claims.DeviceID != fixture.deviceID {
					t.Fatalf("invalid backend approval grant claims=%#v err=%v", claims, err)
				}
				var input internalCommandApproval
				if json.NewDecoder(request.Body).Decode(&input) != nil || input.OrganizationID != fixture.organizationID || input.SiteID != fixture.siteID ||
					input.DeviceID != fixture.deviceID || input.PrincipalID != fixture.principalID || input.ApproverRole != "operator" {
					t.Fatalf("unexpected internal approval input %#v", input)
				}
				fixture.approvalPending.Store(false)
				fixture.approvalCompleted.Store(true)
				return telemetryJSONResponse(http.StatusOK, fixture.commandView(now)), nil
			}
			if err != nil || claims.Purpose != commandmodel.AuthorizationCommandSubmit || claims.DeviceID != fixture.deviceID {
				t.Fatalf("invalid backend Command grant claims=%#v err=%v", claims, err)
			}
			var input internalCommandCreate
			if json.NewDecoder(request.Body).Decode(&input) != nil || input.OrganizationID != fixture.organizationID || input.SiteID != fixture.siteID || input.PrincipalID != fixture.principalID || input.CurrentState.BusinessRevision != 17 || input.CurrentState.CurrentValue == nil || *input.CurrentState.CurrentValue != 23 || input.Capability != commandmodel.CapabilitySetTemperatureSetpoint || input.Parameters[commandmodel.ParameterSetpointC] != 24.5 || input.VerificationPointKey != defaultCommandTemperatureKey || input.IdempotencyKey == "" {
				t.Fatalf("unexpected internal command input %#v", input)
			}
			response := telemetryJSONResponse(http.StatusAccepted, fixture.commandView(now))
			response.Header.Set("Location", "/api/v1/commands/"+fixture.commandID)
			return response, nil
		case http.MethodGet:
			if request.Header.Get("X-Acting-Organization-ID") != fixture.organizationID {
				t.Fatal("missing acting organization for command read")
			}
			claims, err := identitycontext.VerifyDelegation(&fixture.gatewaySigner.PublicKey, request.Header.Get("X-Command-Read-Context"))
			if err != nil || claims.ActingOrganizationID != fixture.organizationID || len(claims.Actions) != 1 || claims.Actions[0] != "command:read" || !commandTestContains(claims.Scopes, "organization:"+fixture.organizationID) || !commandTestContains(claims.Scopes, "command:"+fixture.commandID) {
				t.Fatalf("invalid command read context claims=%#v err=%v", claims, err)
			}
			return telemetryJSONResponse(http.StatusOK, fixture.commandView(now)), nil
		default:
			t.Fatalf("unexpected command backend method %s", request.Method)
			return nil, io.EOF
		}
	})}
}

func (fixture *commandGatewayFixture) commandView(now time.Time) commandView {
	organizationID := fixture.organizationID
	if fixture.crossOrganizationView.Load() {
		organizationID = "018f3e00-1000-7000-8000-000000000002"
	}
	status := commandmodel.IntentQueued
	risk := commandmodel.RiskLow
	policy := commandmodel.ApprovalNone
	approvalCount, requiredCount := 0, 0
	version := uint64(3)
	thirdStatus := commandmodel.IntentQueued
	transitions := []commandTransitionView{
		{ToStatus: commandmodel.IntentSubmitted, Reason: "COMMAND_SUBMITTED", ActorType: "PRINCIPAL", OccurredAt: now.Add(-3 * time.Second), Version: 1},
		{FromStatus: commandStatusPointer(commandmodel.IntentSubmitted), ToStatus: commandmodel.IntentValidating, Reason: "COMMAND_VALIDATING", ActorType: "WORKLOAD", OccurredAt: now.Add(-2 * time.Second), Version: 2},
	}
	if fixture.approvalPending.Load() || fixture.approvalCompleted.Load() {
		risk = commandmodel.RiskMedium
		if fixture.highRiskApproval.Load() {
			risk = commandmodel.RiskHigh
		}
		policy = commandmodel.ApprovalSingleApprover
		requiredCount = 1
		thirdStatus = commandmodel.IntentAwaitingApproval
		status = commandmodel.IntentAwaitingApproval
	}
	transitions = append(transitions, commandTransitionView{
		FromStatus: commandStatusPointer(commandmodel.IntentValidating), ToStatus: thirdStatus,
		Reason: "COMMAND_GOVERNANCE_EVALUATED", ActorType: "WORKLOAD", OccurredAt: now.Add(-time.Second), Version: 3,
	})
	if fixture.approvalCompleted.Load() {
		status = commandmodel.IntentQueued
		approvalCount = 1
		version = 5
		transitions = append(transitions,
			commandTransitionView{FromStatus: commandStatusPointer(commandmodel.IntentAwaitingApproval), ToStatus: commandmodel.IntentApproved, Reason: "APPROVAL_THRESHOLD_MET", ActorType: "PRINCIPAL", OccurredAt: now, Version: 4},
			commandTransitionView{FromStatus: commandStatusPointer(commandmodel.IntentApproved), ToStatus: commandmodel.IntentQueued, Reason: "COMMAND_QUEUED", ActorType: "WORKLOAD", OccurredAt: now, Version: 5},
		)
	}
	return commandView{
		SchemaVersion: 1, CommandID: fixture.commandID, OrganizationID: organizationID,
		SiteID: fixture.siteID, DeviceID: fixture.deviceID, PointID: fixture.commandPointID,
		Capability: commandmodel.CapabilitySetTemperatureSetpoint, CapabilityRevision: "capability:set-temperature-setpoint:v1",
		Status: status, Risk: risk, ApprovalPolicy: policy, ApprovalCount: approvalCount, RequiredApprovalCount: requiredCount,
		Parameters: commandmodel.CommandParameters{commandmodel.ParameterSetpointC: 24.5}, DeviceCommandSequence: 1, Version: version, SnapshotRevision: 17,
		Transitions: transitions, CreatedAt: now.Add(-3 * time.Second), UpdatedAt: now,
	}
}

func commandStatusPointer(value commandmodel.IntentStatus) *commandmodel.IntentStatus {
	return &value
}

func commandTestSigner(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func commandUnsignedRegistryGrant(claims registryauth.GrantClaims) string {
	claims.Version = registryauth.GrantVersion
	payload, _ := json.Marshal(claims)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString([]byte("test-proof"))
}

func commandTestContains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func assertCommandInternalHeaders(t *testing.T, request *http.Request) {
	t.Helper()
	for _, header := range []string{"Cookie", "X-CSRF-Token", "Origin", "Idempotency-Key", "X-Admin", "X-Principal", "X-Roles"} {
		if request.Header.Get(header) != "" {
			t.Fatalf("browser authority leaked to internal request header %s", header)
		}
	}
}

var _ = bytes.NewReader
