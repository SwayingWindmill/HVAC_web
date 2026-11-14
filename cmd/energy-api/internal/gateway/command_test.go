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

	"github.com/quanlaihe/hvac-web/cmd/energy-api/internal/platformapi"
	"github.com/quanlaihe/hvac-web/cmd/energy-api/internal/s2telemetryapi"
	"github.com/quanlaihe/hvac-web/libs/commandauth"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/libs/sessionstore"
	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
)

func TestGatewayCreateCommandDerivesAuthorityAndCurrentState(t *testing.T) {
	fixture := newCommandGatewayFixture(t)
	body := `{"assetId":"` + fixture.assetID + `","commandPointId":"` + fixture.commandPointID + `","parameters":{"setpointC":24.5}}`
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
	if fixture.iamCalls.Load() != 5 || fixture.registryCalls.Load() != 3 || fixture.telemetryCalls.Load() != 1 || fixture.commandCalls.Load() != 1 {
		t.Fatalf("calls iam=%d registry=%d telemetry=%d command=%d", fixture.iamCalls.Load(), fixture.registryCalls.Load(), fixture.telemetryCalls.Load(), fixture.commandCalls.Load())
	}
	var view commandView
	if json.NewDecoder(recorder.Body).Decode(&view) != nil || view.CommandID != fixture.commandID ||
		view.TenantID != fixture.tenantID || view.SiteID != fixture.siteID || view.SnapshotRevision != 17 {
		t.Fatalf("unexpected view %#v", view)
	}
}

func TestGatewayCreateCommandFailsBeforeUpstreamsWithoutCSRF(t *testing.T) {
	fixture := newCommandGatewayFixture(t)
	body := `{"assetId":"` + fixture.assetID + `","commandPointId":"` + fixture.commandPointID + `","parameters":{"setpointC":24.5}}`
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

func TestGatewayCreateCommandUsesReportedSettingAsFeedback(t *testing.T) {
	fixture := newCommandGatewayFixture(t, "SETTING")
	request := httptest.NewRequest(http.MethodPost, publicCommandsPath, strings.NewReader(`{"assetId":"`+fixture.assetID+`","commandPointId":"`+fixture.commandPointID+`","parameters":{"setpointC":24.5}}`))
	fixture.authenticate(request, true)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "reported-setpoint-command")
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("reported setpoint feedback rejected: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, publicCommandsPath, strings.NewReader(`{"assetId":"`+fixture.assetID+`","commandPointId":"`+fixture.feedbackPointID+`","parameters":{"setpointC":24.5}}`))
	fixture.authenticate(request, true)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "direct-reported-setpoint")
	recorder = httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound || fixture.commandCalls.Load() != 1 {
		t.Fatalf("reported SETTING became a command target: status=%d commands=%d", recorder.Code, fixture.commandCalls.Load())
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
	body := `{"assetId":"` + fixture.assetID + `","commandPointId":"` + fixture.commandPointID + `","parameters":{"setpointC":24.5}}`
	request := httptest.NewRequest(http.MethodPost, publicCommandsPath, strings.NewReader(body))
	fixture.authenticate(request, true)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "command-request-4")
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if fixture.iamCalls.Load() != 4 || fixture.registryCalls.Load() != 3 || fixture.telemetryCalls.Load() != 1 || fixture.commandCalls.Load() != 0 {
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

func TestGatewayListCommandsReadsAVisibleSitesCommands(t *testing.T) {
	fixture := newCommandGatewayFixture(t)
	request := httptest.NewRequest(http.MethodGet, publicCommandsPath+"?siteId="+fixture.siteID+"&limit=20", nil)
	fixture.authenticate(request, false)
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Data []commandView `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || len(body.Data) != 1 || body.Data[0].CommandID != fixture.commandID {
		t.Fatalf("list body=%s err=%v", recorder.Body.String(), err)
	}
	if fixture.commandCalls.Load() != 1 || fixture.registryCalls.Load() != 1 {
		t.Fatalf("command list calls command=%d registry=%d", fixture.commandCalls.Load(), fixture.registryCalls.Load())
	}
}

func TestGatewayListCommandsHidesASiteOutsideTheSession(t *testing.T) {
	fixture := newCommandGatewayFixture(t)
	request := httptest.NewRequest(http.MethodGet, publicCommandsPath+"?siteId=018f3e00-2000-7000-8000-000000000002&limit=20", nil)
	fixture.authenticate(request, false)
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound || fixture.commandCalls.Load() != 0 {
		t.Fatalf("status=%d commandCalls=%d body=%s", recorder.Code, fixture.commandCalls.Load(), recorder.Body.String())
	}
}

func TestGatewayListCommandsRejectsCrossTenantProjection(t *testing.T) {
	fixture := newCommandGatewayFixture(t)
	fixture.crossTenantView.Store(true)
	request := httptest.NewRequest(http.MethodGet, publicCommandsPath+"?siteId="+fixture.siteID+"&limit=20", nil)
	fixture.authenticate(request, false)
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestGatewayGetCommandRejectsCrossTenantProjection(t *testing.T) {
	fixture := newCommandGatewayFixture(t)
	fixture.crossTenantView.Store(true)
	request := httptest.NewRequest(http.MethodGet, publicCommandsPath+"/"+fixture.commandID, nil)
	fixture.authenticate(request, false)
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "018f3e00-1000-7000-8000-000000000002") {
		t.Fatal("cross-Tenant Command projection leaked its scope")
	}
}

// Approval is authorized by the approver's COMMAND_APPROVE grant alone; no role is read (#448).
func TestGatewayApproveCommandDerivesIdentityAndExactGrant(t *testing.T) {
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

// An OUTCOME_UNKNOWN command is settled with the caller's own Session-derived identity and a
// COMMAND_SUBMIT grant for its Device (#444).
func TestGatewayReconcileCommandUsesTheDevicesSubmitGrant(t *testing.T) {
	fixture := newCommandGatewayFixture(t)
	fixture.outcomeUnknown.Store(true)
	request := httptest.NewRequest(http.MethodPost, publicCommandsPath+"/"+fixture.commandID+"/reconcile", strings.NewReader(`{"outcome":"NOT_APPLIED"}`))
	fixture.authenticate(request, true)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var view commandView
	if json.NewDecoder(recorder.Body).Decode(&view) != nil || view.Status != commandmodel.IntentFailed || !fixture.reconciled.Load() {
		t.Fatalf("unexpected reconciled view %#v", view)
	}

	settled := newCommandGatewayFixture(t)
	request = httptest.NewRequest(http.MethodPost, publicCommandsPath+"/"+settled.commandID+"/reconcile", strings.NewReader(`{"outcome":"APPLIED"}`))
	settled.authenticate(request, true)
	request.Header.Set("Content-Type", "application/json")
	recorder = httptest.NewRecorder()
	settled.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict || settled.iamCalls.Load() != 0 {
		t.Fatalf("a command without an unknown outcome: status=%d iam=%d body=%s", recorder.Code, settled.iamCalls.Load(), recorder.Body.String())
	}

	forged := newCommandGatewayFixture(t)
	forged.outcomeUnknown.Store(true)
	request = httptest.NewRequest(http.MethodPost, publicCommandsPath+"/"+forged.commandID+"/reconcile", strings.NewReader(`{"outcome":"APPLIED","principalId":"caller-supplied"}`))
	forged.authenticate(request, true)
	request.Header.Set("Content-Type", "application/json")
	recorder = httptest.NewRecorder()
	forged.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest || forged.totalUpstreamCalls() != 0 {
		t.Fatalf("browser reconciliation authority: status=%d upstream=%d", recorder.Code, forged.totalUpstreamCalls())
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
	route, _, ok := matchPublicRegistryRoute(http.MethodGet, "/api/v1/devices/"+fixture.deviceID)
	if !ok {
		t.Fatal("device Registry route did not match")
	}
	request := httptest.NewRequest(http.MethodPost, publicCommandsPath, nil)
	decision, failure := fixture.handler.commandRegistryDecision(request, bffSession{Session: sessionstore.Session{TenantID: fixture.tenantID}}, route, fixture.deviceID)
	if failure != nil {
		t.Fatalf("failure=%+v", *failure)
	}
	if decision.SelectedOwner != ownershipregistry.OwnerCore ||
		decision.PathTemplate != platformapi.GetDevicePathTemplate || decision.RegistryRevision != 44 || decision.RouteRevision != 9 {
		t.Fatalf("unexpected Registry decision %+v", decision)
	}
}

type commandGatewayFixture struct {
	feedbackPointType     string
	handler               *handler
	sessionID             string
	tenantID              string
	siteID                string
	assetID               string
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
	identityCalls         atomic.Int32
	unsafeState           atomic.Bool
	crossTenantView       atomic.Bool
	approvalPending       atomic.Bool
	highRiskApproval      atomic.Bool
	outcomeUnknown        atomic.Bool
	reconciled            atomic.Bool
	approvalCompleted     atomic.Bool
	dispatching           atomic.Bool
	cancelled             atomic.Bool
	commandCapability     commandmodel.Capability
	expectedSetpoint      *float64
	totpResponder         func(code string) (int, map[string]any)
	denyScheduleManage    atomic.Bool
	siteControlMode       string
	siteControlRevision   int64
	assetLockMode         string
	assetLockRevision     int64
	cancelledPendingIDs   []string
	lastCancelPending     *internalCommandCancelPendingRequest
}

func newCommandGatewayFixture(t *testing.T, feedbackTypes ...string) *commandGatewayFixture {
	t.Helper()
	now := time.Date(2026, 7, 26, 15, 0, 0, 0, time.UTC)
	gatewaySigner := commandTestSigner(t)
	iamSigner := commandTestSigner(t)
	fixture := &commandGatewayFixture{
		tenantID:              "018f3d00-1000-7000-8000-000000000001",
		siteID:                "018f3e00-2000-7000-8000-000000000001",
		deviceID:              "018f3e00-3000-7000-8000-000000000001",
		commandID:             "50f5012a-fe6b-47d6-b3df-62ca69a98885",
		principalID:           "018f3e00-5000-7000-8000-000000000001",
		assetID:               "018f3e00-6000-7000-8000-000000000001",
		commandPointID:        "018f3e00-7000-7000-8000-000000000001",
		feedbackPointID:       "018f3e00-8000-7000-8000-000000000001",
		controlRelationshipID: "018f3e00-9000-7000-8000-000000000001",
		gatewaySigner:         gatewaySigner,
		iamSigner:             iamSigner,
	}
	if len(feedbackTypes) > 0 {
		fixture.feedbackPointType = feedbackTypes[0]
	}
	store := sessionstore.NewMemoryStore()
	configured := NewHandler(Config{
		Now: func() time.Time { return now },
		Identity: &IdentityConfig{
			OIDCIssuer: "https://issuer.example.test", OIDCClientID: "client", OIDCRedirectURI: "https://web.example.test/api/v1/auth/callback",
			PublicOrigin: "https://web.example.test", IAMURL: "https://iam.example.test", IAMAudience: "iam-service",
			ExecutingWorkloadSPIFFE: "spiffe://hvac.local/platform-gateway", PolicyRevision: "identity-policy-1", DelegationSigner: gatewaySigner,
			TokenEncryptionKey: make([]byte, 32), SessionStore: store, SessionTTL: time.Hour, DelegationTTL: 30 * time.Second,
			IAMHTTPClient: fixture.commandIAMClient(t, now), OIDCHTTPClient: fixture.commandIdentityClient(t, now),
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
		Client: sessionstore.ClientBrowser,
		ID:     "command-session", Principal: identitycontext.UserPrincipal{Subject: "command-user", Issuer: "https://issuer.example.test", Roles: []string{}},
		TenantID: fixture.tenantID, CSRFTokenCiphertext: csrfCiphertext, ExpiresAt: now.Add(time.Hour),
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
		case "/internal/v1/registry/decision":
			return fixture.registryDecisionResponse(t, request, parent, now), nil
		case telemetryDecisionPath:
			return fixture.telemetryDecisionResponse(t, request, parent, now), nil
		case commandDecisionPath:
			return fixture.commandDecisionResponse(t, request, parent, now), nil
		case "/internal/v1/principal/current":
			principal := identitycontext.UserPrincipal{Subject: parent.Subject, Issuer: parent.SubjectIssuer, Roles: []string{"operator"}}
			return telemetryJSONResponse(http.StatusOK, identitycontext.InternalPrincipalResponse{
				PrincipalID: fixture.principalID, Principal: principal,
				Context: identitycontext.PrincipalContext{
					InitiatingPrincipal: principal, ExecutingServicePrincipal: identitycontext.ServicePrincipal{Service: "platform-gateway", SPIFFEID: parent.ExecutingService},
					TenantID: parent.TenantID, Audience: parent.Audience, PolicyRevision: parent.PolicyRevision,
					DelegationExpiresAt: time.Unix(parent.ExpiresAt, 0).UTC().Format(time.RFC3339),
				},
				Authorization: identitycontext.EffectiveAuthorization{
					CapabilitySetVersion: identitycontext.CapabilitySetVersion, PolicyRevision: "registry-read:1/iam:1",
					Capabilities: []identitycontext.Capability{identitycontext.CapabilitySiteRead},
				},
			}), nil
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
		(input.Action != registryauth.ActionAssetRead && input.Action != registryauth.ActionAssetModelRead && input.Action != registryauth.ActionDeviceRead && input.Action != registryauth.ActionSiteRead && input.Action != registryauth.ActionScheduleManage) {
		t.Fatal("invalid Registry decision request")
	}
	if input.Action == registryauth.ActionScheduleManage && fixture.denyScheduleManage.Load() {
		decision := registryauth.Decision{
			Allowed: false, PrincipalID: fixture.principalID, SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject,
			TenantID: fixture.tenantID, Actions: []registryauth.Action{input.Action},
			PolicyRevision: "registry-read:1/iam:1", ReasonCode: registryauth.ReasonDenyActionNotGranted,
			DecidedAt: now.Format(time.RFC3339Nano),
		}
		return telemetryJSONResponse(http.StatusOK, registryauth.DecisionResponse{Decision: decision})
	}
	decision := registryauth.Decision{
		Allowed: true, PrincipalID: fixture.principalID, SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject,
		TenantID: fixture.tenantID, AllowedSiteIDs: []string{fixture.siteID},
		Actions: []registryauth.Action{input.Action}, PolicyRevision: "registry-read:1/iam:1",
		ReasonCode: registryauth.ReasonAllowSiteRole, DecidedAt: now.Format(time.RFC3339Nano),
	}
	claims := registryauth.GrantClaims{
		Issuer: "spiffe://hvac.local/iam-service", Presenter: "spiffe://hvac.local/platform-gateway", Audience: "platform-core-service",
		PrincipalID: fixture.principalID, SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject,
		TenantID: fixture.tenantID, AllowedSiteIDs: []string{fixture.siteID},
		Actions: []registryauth.Action{input.Action}, PolicyRevision: "registry-read:1/iam:1",
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
	digest, _ := telemetryauth.ScopeDigest(input.Action, input.TenantID, canonical)
	decision := telemetryauth.Decision{
		Allowed: true, PrincipalID: fixture.principalID, SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject,
		TenantID: fixture.tenantID, Action: input.Action, ScopeDigest: digest,
		PolicyRevision: "identity-policy-1", ReasonCode: telemetryauth.ReasonAllowExactScope,
		DecidedAt: now.Format(time.RFC3339Nano), Targets: []telemetryauth.AuthorizedTarget{{
			TenantID: fixture.tenantID, SiteID: fixture.siteID, DeviceID: fixture.deviceID, Keys: []string{"zone_temperature"},
		}},
	}
	claims := telemetryauth.GrantClaims{
		Issuer: "spiffe://hvac.local/iam-service", Presenter: "spiffe://hvac.local/platform-gateway", Audience: "telemetry-runtime-service",
		PrincipalID: fixture.principalID, SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject,
		TenantID: fixture.tenantID, ActorChain: []telemetryauth.Actor{{Service: "platform-gateway", SPIFFEID: "spiffe://hvac.local/platform-gateway"}},
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
	profile, ok := commandmodel.CapabilityProfileFor(input.Capability)
	if !ok {
		t.Fatalf("unexpected capability %q", input.Capability)
	}
	decision := commandauth.Decision{
		Allowed: true, PrincipalID: fixture.principalID, SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject,
		TenantID: fixture.tenantID, SiteID: fixture.siteID, DeviceID: fixture.deviceID,
		Capability: input.Capability, CapabilityRevision: profile.Revision,
		Purpose: input.Purpose, MaximumRisk: commandmodel.RiskHigh,
		PolicyRevision: "identity-policy-1", EmergencyRevocationRevision: 3,
		ReasonCode: commandauth.ReasonAllowExactCapability, DecidedAt: now.Format(time.RFC3339Nano),
	}
	grantID := randomURLToken(16)
	grant, err := commandauth.SignGrant(fixture.iamSigner, commandauth.GrantClaims{
		Issuer: "spiffe://hvac.local/iam-service", Presenter: "spiffe://hvac.local/platform-gateway", Audience: "command-service",
		GrantID: grantID, Purpose: decision.Purpose, PrincipalID: fixture.principalID,
		TenantID: fixture.tenantID, SiteID: fixture.siteID, DeviceID: fixture.deviceID,
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
	asset := platformapi.Asset{
		ID: fixture.assetID, TenantID: fixture.tenantID, SiteID: fixture.siteID,
		Code: "AHU-01", DisplayName: "AHU 01", AssetType: "AHU", Status: "ACTIVE", Revision: 7,
		CreatedAt: createdAt, UpdatedAt: updatedAt,
	}
	device := platformapi.Device{
		ID: fixture.deviceID, TenantID: fixture.tenantID, SiteID: fixture.siteID,
		Code: "AHU-01-CTRL", DisplayName: "AHU 01 Controller", DeviceType: "AHU_CONTROLLER", Status: "ACTIVE", Revision: 7,
		CreatedAt: createdAt, UpdatedAt: updatedAt,
	}
	return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		fixture.registryCalls.Add(1)
		assertCommandInternalHeaders(t, request)
		if request.Header.Get("X-Delegation-Grant") == "" {
			t.Fatal("missing Registry delegation grant")
		}
		capTarget := fixture.commandCapability
		if capTarget == "" {
			capTarget = commandmodel.CapabilitySetTemperatureSetpoint
		}
		targetProfile, _ := commandmodel.CapabilityProfileFor(capTarget)
		sourceMetadata := map[string]any{
			"capability": capTarget, "capabilityRevision": targetProfile.Revision,
			"feedbackSourceKey": "zoneTemperatureC", "feedbackPointKey": "zone_temperature",
		}
		if targetProfile.ParameterKey != "" {
			sourceMetadata["parameterKey"] = targetProfile.ParameterKey
		}
		commandPoint := platformapi.TelemetryPoint{
			ID: fixture.commandPointID, TenantID: fixture.tenantID, SiteID: fixture.siteID, ReportingDeviceID: fixture.deviceID,
			PointCode: "zone_temperature_setpoint_command", SourceKey: "zone.temperature.setpoint.command", DisplayName: "Zone temperature setpoint command",
			PointType: "COMMAND", ValueType: "NUMBER", Unit: &unit, Writable: true, SampleIntervalMS: 1000, PublishIntervalMS: 1000, StaleAfterMS: 3000,
			SourceMetadata: sourceMetadata,
			Status:         "ACTIVE", Revision: 1, CreatedAt: createdAt, UpdatedAt: updatedAt,
		}
		feedbackPoint := platformapi.TelemetryPoint{
			ID: fixture.feedbackPointID, TenantID: fixture.tenantID, SiteID: fixture.siteID, ReportingDeviceID: fixture.deviceID,
			PointCode: "zone_temperature", SourceKey: "zone.temperature", DisplayName: "Zone temperature",
			PointType: "TELEMETRY", ValueType: "NUMBER", Unit: &unit, Writable: false, SampleIntervalMS: 1000, PublishIntervalMS: 1000, StaleAfterMS: 3000,
			SourceMetadata: map[string]any{}, Status: "ACTIVE", Revision: 1, CreatedAt: createdAt, UpdatedAt: updatedAt,
		}
		if fixture.feedbackPointType != "" {
			feedbackPoint.PointType = fixture.feedbackPointType
		}
		assetModel := platformapi.SiteAssetModel{
			SchemaVersion: 2, TenantID: fixture.tenantID, SiteID: fixture.siteID, Assets: []platformapi.Asset{asset}, Devices: []platformapi.Device{device},
			Spaces: []platformapi.Space{}, Sensors: []platformapi.Sensor{}, TelemetryPoints: []platformapi.TelemetryPoint{commandPoint, feedbackPoint},
			Relationships: []platformapi.AssetRelationship{{
				ID: fixture.controlRelationshipID, TenantID: fixture.tenantID, SiteID: fixture.siteID,
				FromType: "POINT", FromID: fixture.commandPointID, ToType: "ASSET", ToID: fixture.assetID, Role: "CONTROLS", Status: "ACTIVE",
				ValidFrom: createdAt, ValidTo: nil, Revision: 1, CreatedAt: createdAt, UpdatedAt: updatedAt,
			}},
			Counts: platformapi.AssetModelCounts{Assets: 1, DeviceEndpoints: 1, PhysicalSensors: 0, Points: 2},
		}
		switch request.URL.Path {
		case "/internal/v1/registry/sites/" + fixture.siteID:
			return telemetryJSONResponse(http.StatusOK, platformapi.Site{
				ID: fixture.siteID, TenantID: fixture.tenantID, Code: "PLANT-01", DisplayName: "Central Plant", Timezone: "Asia/Shanghai",
				Status: "ACTIVE", Revision: 1, CreatedAt: createdAt, UpdatedAt: updatedAt,
			}), nil
		case "/internal/v1/registry/assets/" + fixture.assetID:
			return telemetryJSONResponse(http.StatusOK, asset), nil
		case "/internal/v1/registry/sites/" + fixture.siteID + "/asset-model":
			return telemetryJSONResponse(http.StatusOK, assetModel), nil
		case "/internal/v1/registry/devices/" + fixture.deviceID:
			return telemetryJSONResponse(http.StatusOK, device), nil
		case "/internal/v1/registry/sites/" + fixture.siteID + "/control-state":
			if request.Method != http.MethodGet {
				t.Fatalf("unexpected method %s for control-state", request.Method)
			}
			return telemetryJSONResponse(http.StatusOK, fixture.currentSiteControlState(updatedAt)), nil
		case "/internal/v1/registry/sites/" + fixture.siteID + "/control-mode":
			if request.Method != http.MethodPut {
				t.Fatalf("unexpected method %s for control-mode", request.Method)
			}
			var payload struct {
				ControlMode string              `json:"controlMode"`
				Reason      string              `json:"reason"`
				Meta        controlRegistryMeta `json:"meta"`
			}
			if json.NewDecoder(request.Body).Decode(&payload) != nil {
				return telemetryJSONResponse(http.StatusBadRequest, map[string]any{"code": "REGISTRY_MUTATION_INVALID"}), nil
			}
			if payload.Meta.ExpectedRevision != fixture.siteControlRevision {
				return telemetryJSONResponse(http.StatusConflict, map[string]any{"code": "REGISTRY_REVISION_CONFLICT", "detail": "The expected Registry revision is stale."}), nil
			}
			fixture.siteControlMode = payload.ControlMode
			fixture.siteControlRevision++
			state := fixture.currentSiteControlState(updatedAt)
			state.Reason = payload.Reason
			return telemetryJSONResponse(http.StatusOK, state), nil
		case "/internal/v1/registry/assets/" + fixture.assetID + "/control-lock":
			if request.Method != http.MethodPut {
				t.Fatalf("unexpected method %s for asset control-lock", request.Method)
			}
			var payload struct {
				LockMode string              `json:"lockMode"`
				Reason   string              `json:"reason"`
				Meta     controlRegistryMeta `json:"meta"`
			}
			if json.NewDecoder(request.Body).Decode(&payload) != nil {
				return telemetryJSONResponse(http.StatusBadRequest, map[string]any{"code": "REGISTRY_MUTATION_INVALID"}), nil
			}
			if payload.Meta.ExpectedRevision != fixture.assetLockRevision {
				return telemetryJSONResponse(http.StatusConflict, map[string]any{"code": "REGISTRY_REVISION_CONFLICT", "detail": "The expected Registry revision is stale."}), nil
			}
			fixture.assetLockMode = payload.LockMode
			fixture.assetLockRevision++
			lockID := "018f3e00-9900-7000-8000-000000000002"
			return telemetryJSONResponse(http.StatusOK, assetControlLockView{
				ID:             lockID,
				TenantID:       fixture.tenantID,
				SiteID:         fixture.siteID,
				AssetID:        fixture.assetID,
				LockMode:       fixture.assetLockMode,
				Reason:         payload.Reason,
				UpdatedBy:      &fixture.principalID,
				UpdatedAt:      &updatedAt,
				Revision:       fixture.assetLockRevision,
				BoundDeviceIDs: []string{fixture.deviceID},
			}), nil
		default:
			t.Fatalf("unexpected Registry request %s", request.URL.Path)
			return nil, io.EOF
		}
	})}
}

func (fixture *commandGatewayFixture) currentSiteControlState(updatedAt string) siteControlStateView {
	mode := fixture.siteControlMode
	if mode == "" {
		mode = siteControlModeRemoteAuto
	}
	locks := []assetControlLockView{}
	if fixture.assetLockMode != "" {
		lockID := "018f3e00-9900-7000-8000-000000000002"
		locks = append(locks, assetControlLockView{
			ID:             lockID,
			TenantID:       fixture.tenantID,
			SiteID:         fixture.siteID,
			AssetID:        fixture.assetID,
			LockMode:       fixture.assetLockMode,
			Reason:         "asset lock",
			UpdatedBy:      &fixture.principalID,
			UpdatedAt:      &updatedAt,
			Revision:       fixture.assetLockRevision,
			BoundDeviceIDs: []string{fixture.deviceID},
		})
	}
	var stateID *string
	var updatedBy, updatedAtPtr *string
	if fixture.siteControlRevision > 0 {
		id := "018f3e00-9900-7000-8000-000000000001"
		stateID = &id
		updatedBy = &fixture.principalID
		updatedAtPtr = &updatedAt
	}
	return siteControlStateView{
		ID:          stateID,
		TenantID:    fixture.tenantID,
		SiteID:      fixture.siteID,
		ControlMode: mode,
		Reason:      "",
		UpdatedBy:   updatedBy,
		UpdatedAt:   updatedAtPtr,
		Revision:    fixture.siteControlRevision,
		AssetLocks:  locks,
	}
}

func (fixture *commandGatewayFixture) commandTelemetryClient(t *testing.T, now time.Time) *http.Client {
	t.Helper()
	return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		fixture.telemetryCalls.Add(1)
		assertCommandInternalHeaders(t, request)
		if request.URL.Path != internalTelemetrySinglePrefix+fixture.deviceID+"/observation-snapshot" || request.URL.Query().Get("key") != "zone_temperature" {
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
				Key: s2telemetryapi.TelemetryKey("zone_temperature"), State: "PRESENT", Value: json.RawMessage(`23.0`), ValueType: "NUMBER", Unit: &unit,
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
			if request.URL.Path == internalCommandsCancelPendingPath {
				claims, err := identitycontext.VerifyDelegation(&fixture.gatewaySigner.PublicKey, request.Header.Get("X-Command-Control-Context"))
				if err != nil || claims.TenantID != fixture.tenantID || len(claims.Actions) != 1 || claims.Actions[0] != "command:cancel-pending" ||
					!commandTestContains(claims.Scopes, "tenant:"+fixture.tenantID) || !commandTestContains(claims.Scopes, "site:"+fixture.siteID) {
					t.Fatalf("invalid cancel-pending control context claims=%#v err=%v", claims, err)
				}
				var input internalCommandCancelPendingRequest
				if json.NewDecoder(request.Body).Decode(&input) != nil || input.TenantID != fixture.tenantID || input.SiteID != fixture.siteID || input.PrincipalID != fixture.principalID {
					t.Fatalf("unexpected internal cancel-pending input %#v", input)
				}
				fixture.lastCancelPending = &input
				cancelled := fixture.cancelledPendingIDs
				if cancelled == nil {
					cancelled = []string{}
				}
				return telemetryJSONResponse(http.StatusOK, map[string]any{"cancelledCommandIds": cancelled}), nil
			}
			claims, err := commandauth.VerifyGrant(&fixture.iamSigner.PublicKey, request.Header.Get("X-Command-Grant"))
			if strings.HasSuffix(request.URL.Path, "/reconcile") {
				if err != nil || claims.Purpose != commandmodel.AuthorizationCommandSubmit || claims.DeviceID != fixture.deviceID {
					t.Fatalf("invalid backend reconciliation grant claims=%#v err=%v", claims, err)
				}
				var input internalCommandReconciliation
				if json.NewDecoder(request.Body).Decode(&input) != nil || input.TenantID != fixture.tenantID || input.SiteID != fixture.siteID ||
					input.DeviceID != fixture.deviceID || input.PrincipalID != fixture.principalID || input.Outcome != commandmodel.ReconciledNotApplied {
					t.Fatalf("unexpected internal reconciliation input %#v", input)
				}
				fixture.reconciled.Store(true)
				return telemetryJSONResponse(http.StatusOK, fixture.commandView(now)), nil
			}
			if strings.HasSuffix(request.URL.Path, "/cancel") {
				if err != nil || claims.Purpose != commandmodel.AuthorizationCommandSubmit || claims.DeviceID != fixture.deviceID {
					t.Fatalf("invalid backend cancel grant claims=%#v err=%v", claims, err)
				}
				var input internalCommandCancel
				if json.NewDecoder(request.Body).Decode(&input) != nil || input.TenantID != fixture.tenantID || input.SiteID != fixture.siteID ||
					input.DeviceID != fixture.deviceID || input.PrincipalID != fixture.principalID {
					t.Fatalf("unexpected internal cancel input %#v", input)
				}
				fixture.approvalPending.Store(false)
				fixture.cancelled.Store(true)
				return telemetryJSONResponse(http.StatusOK, fixture.commandView(now)), nil
			}
			if strings.HasSuffix(request.URL.Path, "/approve") {
				if err != nil || claims.Purpose != commandmodel.AuthorizationCommandApprove || claims.DeviceID != fixture.deviceID {
					t.Fatalf("invalid backend approval grant claims=%#v err=%v", claims, err)
				}
				var input map[string]string
				if json.NewDecoder(request.Body).Decode(&input) != nil || len(input) != 4 || input["tenantId"] != fixture.tenantID || input["siteId"] != fixture.siteID ||
					input["deviceId"] != fixture.deviceID || input["principalId"] != fixture.principalID {
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
			wantCapability := fixture.commandCapability
			if wantCapability == "" {
				wantCapability = commandmodel.CapabilitySetTemperatureSetpoint
			}
			if json.NewDecoder(request.Body).Decode(&input) != nil || input.TenantID != fixture.tenantID || input.SiteID != fixture.siteID || input.PrincipalID != fixture.principalID || input.CurrentState.BusinessRevision != 17 || input.CurrentState.CurrentValue == nil || *input.CurrentState.CurrentValue != 23 || input.Capability != wantCapability || input.VerificationPointKey != "zone_temperature" || input.IdempotencyKey == "" {
				t.Fatalf("unexpected internal command input %#v", input)
			}
			if wantCapability == commandmodel.CapabilitySetTemperatureSetpoint {
				wantSetpoint := 24.5
				if fixture.expectedSetpoint != nil {
					wantSetpoint = *fixture.expectedSetpoint
				}
				if input.Parameters[commandmodel.ParameterSetpointC] != wantSetpoint {
					t.Fatalf("unexpected setpoint %v want %v", input.Parameters, wantSetpoint)
				}
			}
			response := telemetryJSONResponse(http.StatusAccepted, fixture.commandView(now))
			response.Header.Set("Location", "/api/v1/commands/"+fixture.commandID)
			return response, nil
		case http.MethodGet:
			if request.Header.Get("X-Tenant-ID") != fixture.tenantID {
				t.Fatal("missing tenant for command read")
			}
			if request.URL.Path == internalCommandsPath {
				claims, err := identitycontext.VerifyDelegation(&fixture.gatewaySigner.PublicKey, request.Header.Get("X-Command-Read-Context"))
				if err != nil || len(claims.Actions) != 1 || claims.Actions[0] != "command:read" || len(claims.Scopes) != 2 ||
					!commandTestContains(claims.Scopes, "tenant:"+fixture.tenantID) || !commandTestContains(claims.Scopes, "site:"+fixture.siteID) ||
					request.URL.Query().Get("siteId") != fixture.siteID || (request.URL.Query().Get("limit") != "20" && request.URL.Query().Get("limit") != "100") {
					t.Fatalf("invalid command list context claims=%#v query=%s err=%v", claims, request.URL.RawQuery, err)
				}
				return telemetryJSONResponse(http.StatusOK, map[string][]commandView{"data": {fixture.commandView(now)}}), nil
			}
			claims, err := identitycontext.VerifyDelegation(&fixture.gatewaySigner.PublicKey, request.Header.Get("X-Command-Read-Context"))
			if err != nil || claims.TenantID != fixture.tenantID || len(claims.Actions) != 1 || claims.Actions[0] != "command:read" || !commandTestContains(claims.Scopes, "tenant:"+fixture.tenantID) || !commandTestContains(claims.Scopes, "command:"+fixture.commandID) {
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
	tenantID := fixture.tenantID
	if fixture.crossTenantView.Load() {
		tenantID = "018f3d00-1000-7000-8000-000000000002"
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
	if fixture.cancelled.Load() {
		status = commandmodel.IntentCancelled
		version = 4
		transitions = append(transitions, commandTransitionView{
			FromStatus: commandStatusPointer(commandmodel.IntentQueued), ToStatus: commandmodel.IntentCancelled, Reason: "OPERATOR_CANCELLED", ActorType: "PRINCIPAL", OccurredAt: now, Version: 4,
		})
	} else if fixture.dispatching.Load() {
		status = commandmodel.IntentDispatching
		version = 4
		transitions = append(transitions, commandTransitionView{
			FromStatus: commandStatusPointer(commandmodel.IntentQueued), ToStatus: commandmodel.IntentDispatching, Reason: "DISPATCH_ATTEMPT_PREPARED", ActorType: "WORKLOAD", OccurredAt: now, Version: 4,
		})
	}
	if fixture.outcomeUnknown.Load() {
		status = commandmodel.IntentOutcomeUnknown
		version = 5
		transitions = append(transitions,
			commandTransitionView{FromStatus: commandStatusPointer(commandmodel.IntentQueued), ToStatus: commandmodel.IntentDispatching, Reason: "DISPATCH_ATTEMPT_PREPARED", ActorType: "WORKLOAD", OccurredAt: now, Version: 4},
			commandTransitionView{FromStatus: commandStatusPointer(commandmodel.IntentDispatching), ToStatus: commandmodel.IntentOutcomeUnknown, Reason: "LEASE_EXPIRED_WITHOUT_SEND_PROOF", ActorType: "WORKLOAD", OccurredAt: now, Version: 5},
		)
		if fixture.reconciled.Load() {
			status = commandmodel.IntentFailed
			version = 6
			transitions = append(transitions, commandTransitionView{FromStatus: commandStatusPointer(commandmodel.IntentOutcomeUnknown), ToStatus: commandmodel.IntentFailed, Reason: "OPERATOR_RECONCILED_NOT_APPLIED", ActorType: "PRINCIPAL", OccurredAt: now, Version: 6})
		}
	}
	capTarget := fixture.commandCapability
	if capTarget == "" {
		capTarget = commandmodel.CapabilitySetTemperatureSetpoint
	}
	profile, _ := commandmodel.CapabilityProfileFor(capTarget)
	params := commandmodel.CommandParameters{}
	if profile.ParameterKey != "" {
		setpoint := 24.5
		if fixture.expectedSetpoint != nil {
			setpoint = *fixture.expectedSetpoint
		}
		params[profile.ParameterKey] = setpoint
	}
	return commandView{
		SchemaVersion: 1, CommandID: fixture.commandID, TenantID: tenantID,
		SiteID: fixture.siteID, DeviceID: fixture.deviceID, PointID: fixture.commandPointID,
		Capability: capTarget, CapabilityRevision: profile.Revision,
		Status: status, Risk: risk, ApprovalPolicy: policy, ApprovalCount: approvalCount, RequiredApprovalCount: requiredCount,
		Parameters: params, DeviceCommandSequence: 1, Version: version, SnapshotRevision: 17,
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

func (fixture *commandGatewayFixture) commandIdentityClient(t *testing.T, now time.Time) *http.Client {
	t.Helper()
	return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		fixture.identityCalls.Add(1)
		assertCommandInternalHeaders(t, request)
		if request.Method != http.MethodPost || request.URL.Path != identityTOTPVerifyPath {
			t.Fatalf("unexpected identity request %s %s", request.Method, request.URL.Path)
		}
		claims, err := identitycontext.VerifyDelegation(&fixture.gatewaySigner.PublicKey, request.Header.Get("X-Delegation-Grant"))
		if err != nil {
			t.Fatalf("invalid identity delegation grant: %v", err)
		}
		if err := identitycontext.ValidateDelegationFromIssuer(
			claims, now, "spiffe://hvac.local/platform-gateway", "spiffe://hvac.local/platform-gateway",
			identityTOTPVerifyAudience, identityTOTPVerifyAction, "session:"+fixture.sessionID,
		); err != nil {
			t.Fatalf("invalid identity delegation scope/action: %v", err)
		}
		var payload struct {
			Code string `json:"code"`
		}
		if json.NewDecoder(request.Body).Decode(&payload) != nil {
			t.Fatal("invalid TOTP payload sent to identity-service")
		}
		if fixture.totpResponder != nil {
			status, body := fixture.totpResponder(payload.Code)
			return telemetryJSONResponse(status, body), nil
		}
		if payload.Code == "123456" {
			return telemetryJSONResponse(http.StatusOK, map[string]any{"verified": true, "method": "TOTP"}), nil
		}
		return telemetryJSONResponse(http.StatusUnauthorized, map[string]any{"error": "mfa_code_invalid"}), nil
	})}
}

func TestGatewayHighRiskSubmitRequiresValidTOTP(t *testing.T) {
	t.Run("missing or malformed code fails with 428 before identity or command upstreams", func(t *testing.T) {
		for _, code := range []string{"", "12345", "abcdef"} {
			fixture := newCommandGatewayFixture(t)
			fixture.commandCapability = commandmodel.CapabilityStart
			body := `{"assetId":"` + fixture.assetID + `","commandPointId":"` + fixture.commandPointID + `","parameters":{}`
			if code != "" {
				body += `,"verificationCode":"` + code + `"`
			}
			body += `}`
			req := httptest.NewRequest(http.MethodPost, publicCommandsPath, strings.NewReader(body))
			fixture.authenticate(req, true)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", "missing-totp-"+code)
			rec := httptest.NewRecorder()
			fixture.handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusPreconditionRequired {
				t.Fatalf("code=%q status=%d body=%s", code, rec.Code, rec.Body.String())
			}
			var problem platformapi.ProblemDetails
			if json.NewDecoder(rec.Body).Decode(&problem) != nil || problem.Code != "STEP_UP_REQUIRED" {
				t.Fatalf("unexpected problem %#v", problem)
			}
			if fixture.identityCalls.Load() != 0 || fixture.commandCalls.Load() != 0 {
				t.Fatalf("expected 0 identity/command calls, got identity=%d command=%d", fixture.identityCalls.Load(), fixture.commandCalls.Load())
			}
		}
	})

	t.Run("identity rejections map to 428 STEP_UP_REQUIRED without reaching command backend", func(t *testing.T) {
		cases := []struct {
			name   string
			status int
			errKey string
		}{
			{name: "wrong_code", status: http.StatusUnauthorized, errKey: "mfa_code_invalid"},
			{name: "replayed_code", status: http.StatusConflict, errKey: "mfa_code_replayed"},
			{name: "mfa_not_configured", status: http.StatusPreconditionFailed, errKey: "mfa_not_configured"},
			{name: "mfa_locked", status: http.StatusLocked, errKey: "mfa_locked"},
		}
		for _, tc := range cases {
			fixture := newCommandGatewayFixture(t)
			fixture.commandCapability = commandmodel.CapabilityStartUnitSequence
			fixture.totpResponder = func(code string) (int, map[string]any) {
				return tc.status, map[string]any{"error": tc.errKey}
			}
			body := `{"assetId":"` + fixture.assetID + `","commandPointId":"` + fixture.commandPointID + `","parameters":{},"verificationCode":"654321"}`
			req := httptest.NewRequest(http.MethodPost, publicCommandsPath, strings.NewReader(body))
			fixture.authenticate(req, true)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", "totp-"+tc.name)
			rec := httptest.NewRecorder()
			fixture.handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusPreconditionRequired {
				t.Fatalf("%s status=%d body=%s", tc.name, rec.Code, rec.Body.String())
			}
			var problem platformapi.ProblemDetails
			if json.NewDecoder(rec.Body).Decode(&problem) != nil || problem.Code != "STEP_UP_REQUIRED" {
				t.Fatalf("%s unexpected problem %#v", tc.name, problem)
			}
			if fixture.identityCalls.Load() != 1 || fixture.commandCalls.Load() != 0 {
				t.Fatalf("%s calls identity=%d command=%d", tc.name, fixture.identityCalls.Load(), fixture.commandCalls.Load())
			}
		}
	})

	t.Run("valid TOTP submits high-risk discrete and large-delta setpoint commands", func(t *testing.T) {
		for _, capability := range []commandmodel.Capability{
			commandmodel.CapabilityStart,
			commandmodel.CapabilityStop,
			commandmodel.CapabilityStartUnitSequence,
			commandmodel.CapabilityStopUnitSequence,
		} {
			fixture := newCommandGatewayFixture(t)
			fixture.commandCapability = capability
			body := `{"assetId":"` + fixture.assetID + `","commandPointId":"` + fixture.commandPointID + `","parameters":{},"verificationCode":"123456"}`
			req := httptest.NewRequest(http.MethodPost, publicCommandsPath, strings.NewReader(body))
			fixture.authenticate(req, true)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", "valid-discrete-"+string(capability))
			rec := httptest.NewRecorder()
			fixture.handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusAccepted || fixture.identityCalls.Load() != 1 || fixture.commandCalls.Load() != 1 {
				t.Fatalf("%s status=%d identity=%d command=%d body=%s", capability, rec.Code, fixture.identityCalls.Load(), fixture.commandCalls.Load(), rec.Body.String())
			}
		}

		// Delta 2.5C (25.5 vs 23) is RiskHigh and requires TOTP.
		fixture := newCommandGatewayFixture(t)
		highSetpoint := 25.5
		fixture.expectedSetpoint = &highSetpoint
		noCodeReq := httptest.NewRequest(http.MethodPost, publicCommandsPath, strings.NewReader(
			`{"assetId":"`+fixture.assetID+`","commandPointId":"`+fixture.commandPointID+`","parameters":{"setpointC":25.5}}`,
		))
		fixture.authenticate(noCodeReq, true)
		noCodeReq.Header.Set("Content-Type", "application/json")
		noCodeReq.Header.Set("Idempotency-Key", "high-delta-no-totp")
		rec := httptest.NewRecorder()
		fixture.handler.ServeHTTP(rec, noCodeReq)
		if rec.Code != http.StatusPreconditionRequired {
			t.Fatalf("high delta without totp status=%d body=%s", rec.Code, rec.Body.String())
		}

		withCodeReq := httptest.NewRequest(http.MethodPost, publicCommandsPath, strings.NewReader(
			`{"assetId":"`+fixture.assetID+`","commandPointId":"`+fixture.commandPointID+`","parameters":{"setpointC":25.5},"verificationCode":"123456"}`,
		))
		fixture.authenticate(withCodeReq, true)
		withCodeReq.Header.Set("Content-Type", "application/json")
		withCodeReq.Header.Set("Idempotency-Key", "high-delta-with-totp")
		rec = httptest.NewRecorder()
		fixture.handler.ServeHTTP(rec, withCodeReq)
		if rec.Code != http.StatusAccepted || fixture.identityCalls.Load() != 1 || fixture.commandCalls.Load() != 1 {
			t.Fatalf("high delta with totp status=%d identity=%d command=%d body=%s", rec.Code, fixture.identityCalls.Load(), fixture.commandCalls.Load(), rec.Body.String())
		}
	})
}

func TestGatewayCancelCommandRoutesAndEnforcesLifecycleAndAuthority(t *testing.T) {
	fixture := newCommandGatewayFixture(t)
	fixture.approvalPending.Store(true)

	// Valid empty-body cancel of an AWAITING_APPROVAL command succeeds with SUBMIT grant.
	req := httptest.NewRequest(http.MethodPost, publicCommandsPath+"/"+fixture.commandID+"/cancel", nil)
	fixture.authenticate(req, true)
	rec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || fixture.commandCalls.Load() != 2 || fixture.iamCalls.Load() != 2 {
		t.Fatalf("cancel awaiting failed: status=%d calls cmd=%d iam=%d body=%s", rec.Code, fixture.commandCalls.Load(), fixture.iamCalls.Load(), rec.Body.String())
	}
	var view commandView
	if json.NewDecoder(rec.Body).Decode(&view) != nil || view.Status != commandmodel.IntentCancelled {
		t.Fatalf("unexpected cancelled view %#v", view)
	}

	// Browser authority fields in cancel JSON body are rejected with 400 before upstreams.
	badBodyReq := httptest.NewRequest(http.MethodPost, publicCommandsPath+"/"+fixture.commandID+"/cancel", strings.NewReader(`{"principalId":"spoofed"}`))
	fixture.authenticate(badBodyReq, true)
	badBodyReq.Header.Set("Content-Type", "application/json")
	badBodyRec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(badBodyRec, badBodyReq)
	if badBodyRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on browser authority field in cancel, got %d body=%s", badBodyRec.Code, badBodyRec.Body.String())
	}

	// DISPATCHING commands return 409 COMMAND_CANCEL_INVALID before touching IAM/Registry.
	dispFixture := newCommandGatewayFixture(t)
	dispFixture.dispatching.Store(true)
	dispReq := httptest.NewRequest(http.MethodPost, publicCommandsPath+"/"+dispFixture.commandID+"/cancel", strings.NewReader(`{"reason":"TOO_LATE"}`))
	dispFixture.authenticate(dispReq, true)
	dispReq.Header.Set("Content-Type", "application/json")
	dispRec := httptest.NewRecorder()
	dispFixture.handler.ServeHTTP(dispRec, dispReq)
	if dispRec.Code != http.StatusConflict || dispFixture.iamCalls.Load() != 0 {
		t.Fatalf("expected 409 COMMAND_CANCEL_INVALID with 0 iam calls, got %d iam=%d body=%s", dispRec.Code, dispFixture.iamCalls.Load(), dispRec.Body.String())
	}
	var problem platformapi.ProblemDetails
	if json.NewDecoder(dispRec.Body).Decode(&problem) != nil || problem.Code != "COMMAND_CANCEL_INVALID" {
		t.Fatalf("unexpected conflict problem %#v", problem)
	}
}

var _ = bytes.NewReader
