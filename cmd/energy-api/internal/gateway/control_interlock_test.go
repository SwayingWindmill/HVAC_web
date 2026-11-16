package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/cmd/energy-api/internal/platformapi"
	"github.com/quanlaihe/hvac-web/cmd/energy-api/internal/s2telemetryapi"
	"github.com/quanlaihe/hvac-web/libs/commandauth"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/libs/sessionstore"
	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
)

type interlockGatewayFixture struct {
	handler             *handler
	sessionID           string
	tenantID            string
	siteID              string
	principalID         string
	chillerAssetID      string
	pumpAssetID         string
	chillerDeviceID     string
	pumpDeviceID        string
	chillerRunStateID   string
	chillerStartPointID string
	pumpRunStateID      string
	pumpStopPointID     string
	controlRelStartID   string
	controlRelStopID    string
	commandID           string
	chillerRunState     string
	pumpRunState        string
	pumpStale           bool
	commandCalls        atomic.Int32
	listedCommands      []commandView
	now                 time.Time
}

func newInterlockGatewayFixture(t *testing.T) *interlockGatewayFixture {
	t.Helper()
	now := time.Date(2026, 11, 14, 15, 0, 0, 0, time.UTC)
	gatewaySigner := commandTestSigner(t)
	iamSigner := commandTestSigner(t)
	f := &interlockGatewayFixture{
		tenantID:            "019a7000-1000-7000-8000-000000000001",
		siteID:              "019a7000-2000-7000-8000-000000000001",
		principalID:         "019a7000-5000-7000-8000-000000000001",
		chillerAssetID:      "019a7000-6000-7000-8000-000000000001",
		pumpAssetID:         "019a7000-6000-7000-8000-000000000002",
		chillerDeviceID:     "019a7000-3000-7000-8000-00000000000e",
		pumpDeviceID:        "019a7000-3000-7000-8000-00000000000f",
		chillerRunStateID:   "019a7000-7000-7000-8000-000000000001",
		chillerStartPointID: "019a7000-7000-7000-8000-000000000002",
		pumpRunStateID:      "019a7000-7000-7000-8000-000000000003",
		pumpStopPointID:     "019a7000-7000-7000-8000-000000000004",
		controlRelStartID:   "019a7000-9000-7000-8000-000000000001",
		controlRelStopID:    "019a7000-9000-7000-8000-000000000002",
		commandID:           "019a7000-aaaa-7000-8000-000000000001",
		chillerRunState:     "STOPPED",
		pumpRunState:        "RUNNING",
		now:                 now,
	}

	iamClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		parent, err := identitycontext.VerifyDelegation(&gatewaySigner.PublicKey, request.Header.Get("X-Delegation-Grant"))
		if err != nil {
			t.Fatalf("invalid gateway delegation: %v", err)
		}
		switch request.URL.Path {
		case "/internal/v1/principal/current":
			principal := identitycontext.UserPrincipal{Subject: parent.Subject, Issuer: parent.SubjectIssuer, Roles: []string{"operator"}}
			return telemetryJSONResponse(http.StatusOK, identitycontext.InternalPrincipalResponse{
				PrincipalID: f.principalID, Principal: principal,
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
		case "/internal/v1/registry/decision":
			var input registryauth.DecisionRequest
			if json.NewDecoder(request.Body).Decode(&input) != nil {
				t.Fatal("invalid Registry decision request")
			}
			decision := registryauth.Decision{
				Allowed: true, PrincipalID: f.principalID, SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject,
				TenantID: f.tenantID, AllowedSiteIDs: []string{f.siteID},
				Actions: []registryauth.Action{input.Action}, PolicyRevision: "registry-read:1/iam:1",
				ReasonCode: registryauth.ReasonAllowSiteRole, DecidedAt: now.Format(time.RFC3339Nano),
			}
			claims := registryauth.GrantClaims{
				Issuer: "spiffe://hvac.local/iam-service", Presenter: "spiffe://hvac.local/platform-gateway", Audience: "platform-core-service",
				PrincipalID: f.principalID, SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject,
				TenantID: f.tenantID, AllowedSiteIDs: []string{f.siteID},
				Actions: []registryauth.Action{input.Action}, PolicyRevision: "registry-read:1/iam:1",
				DecisionReason: registryauth.ReasonAllowSiteRole, SessionID: parent.SessionID, ParentTokenID: parent.TokenID,
				IssuedAt: now.Unix(), ExpiresAt: now.Add(30 * time.Second).Unix(), TokenID: randomURLToken(16),
			}
			return telemetryJSONResponse(http.StatusOK, registryauth.DecisionResponse{Decision: decision, DelegationGrant: commandUnsignedRegistryGrant(claims)}), nil
		case telemetryDecisionPath:
			var input telemetryauth.DecisionRequest
			if json.NewDecoder(request.Body).Decode(&input) != nil || len(input.Targets) != 1 {
				t.Fatal("invalid Telemetry decision request")
			}
			target := input.Targets[0]
			canonical, _ := telemetryauth.CanonicalTargets(input.Targets)
			digest, _ := telemetryauth.ScopeDigest(input.Action, input.TenantID, canonical)
			decision := telemetryauth.Decision{
				Allowed: true, PrincipalID: f.principalID, SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject,
				TenantID: f.tenantID, Action: input.Action, ScopeDigest: digest,
				PolicyRevision: "identity-policy-1", ReasonCode: telemetryauth.ReasonAllowExactScope,
				DecidedAt: now.Format(time.RFC3339Nano), Targets: []telemetryauth.AuthorizedTarget{{
					TenantID: f.tenantID, SiteID: f.siteID, DeviceID: target.DeviceID, Keys: target.Keys,
				}},
			}
			claims := telemetryauth.GrantClaims{
				Issuer: "spiffe://hvac.local/iam-service", Presenter: "spiffe://hvac.local/platform-gateway", Audience: "telemetry-runtime-service",
				PrincipalID: f.principalID, SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject,
				TenantID: f.tenantID, ActorChain: []telemetryauth.Actor{{Service: "platform-gateway", SPIFFEID: "spiffe://hvac.local/platform-gateway"}},
				Action: input.Action, ScopeDigest: digest, TargetCount: 1, KeyCount: len(target.Keys), PolicyRevision: "identity-policy-1",
				SessionID: parent.SessionID, ParentTokenID: parent.TokenID, RequestID: request.Header.Get("X-Request-ID"),
				TraceID: traceIDFromTraceparent(request.Header.Get("Traceparent")), Route: telemetryPublicRoute(input.Action),
				IssuedAt: now.Unix(), ExpiresAt: now.Add(30 * time.Second).Unix(), TokenID: randomURLToken(16),
			}
			return telemetryJSONResponse(http.StatusOK, telemetryauth.DecisionResponse{Decision: decision, DelegationGrant: unsignedTelemetryGrant(claims)}), nil
		case commandDecisionPath:
			var input commandauth.DecisionRequest
			if json.NewDecoder(request.Body).Decode(&input) != nil {
				t.Fatal("invalid Command decision request")
			}
			profile, _ := commandmodel.CapabilityProfileFor(input.Capability)
			decision := commandauth.Decision{
				Allowed: true, PrincipalID: f.principalID, SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject,
				TenantID: f.tenantID, SiteID: f.siteID, DeviceID: input.DeviceID,
				Capability: input.Capability, CapabilityRevision: profile.Revision,
				Purpose: input.Purpose, MaximumRisk: commandmodel.RiskHigh,
				PolicyRevision: "identity-policy-1", EmergencyRevocationRevision: 3,
				ReasonCode: commandauth.ReasonAllowExactCapability, DecidedAt: now.Format(time.RFC3339Nano),
			}
			grantID := randomURLToken(16)
			grant, err := commandauth.SignGrant(iamSigner, commandauth.GrantClaims{
				Issuer: "spiffe://hvac.local/iam-service", Presenter: "spiffe://hvac.local/platform-gateway", Audience: "command-service",
				GrantID: grantID, Purpose: decision.Purpose, PrincipalID: f.principalID,
				TenantID: f.tenantID, SiteID: f.siteID, DeviceID: input.DeviceID,
				Capability: decision.Capability, MaximumRisk: decision.MaximumRisk, CapabilityRevision: decision.CapabilityRevision,
				PolicyRevision: decision.PolicyRevision, EmergencyRevocationRevision: 3,
				IssuedAt: now.Unix(), ExpiresAt: now.Add(25 * time.Second).Unix(), TokenID: grantID,
			})
			if err != nil {
				t.Fatal(err)
			}
			return telemetryJSONResponse(http.StatusOK, commandauth.DecisionResponse{Decision: decision, DelegationGrant: grant}), nil
		default:
			t.Fatalf("unexpected IAM path %s", request.URL.Path)
			return nil, io.EOF
		}
	})}

	identityClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var payload struct {
			Code string `json:"code"`
		}
		if json.NewDecoder(request.Body).Decode(&payload) != nil || payload.Code != "123456" {
			return telemetryJSONResponse(http.StatusUnauthorized, map[string]any{"error": "mfa_code_invalid"}), nil
		}
		return telemetryJSONResponse(http.StatusOK, map[string]any{"verified": true, "method": "TOTP"}), nil
	})}

	registryClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		createdAt := now.Add(-time.Hour).Format("2006-01-02T15:04:05.000Z")
		updatedAt := now.Format("2006-01-02T15:04:05.000Z")
		chillerAsset := platformapi.Asset{ID: f.chillerAssetID, TenantID: f.tenantID, SiteID: f.siteID, Code: "chiller-a", DisplayName: "Chiller A", AssetType: "CHILLER", Status: "ACTIVE", Revision: 1, CreatedAt: createdAt, UpdatedAt: updatedAt}
		pumpAsset := platformapi.Asset{ID: f.pumpAssetID, TenantID: f.tenantID, SiteID: f.siteID, Code: "chwp-1", DisplayName: "CHWP 1", AssetType: "PUMP", Status: "ACTIVE", Revision: 1, CreatedAt: createdAt, UpdatedAt: updatedAt}
		chillerDevice := platformapi.Device{ID: f.chillerDeviceID, TenantID: f.tenantID, SiteID: f.siteID, Code: "ctrl-chiller-a", DisplayName: "Chiller A Ctrl", DeviceType: "CHILLER_CONTROLLER", Status: "ACTIVE", Revision: 1, CreatedAt: createdAt, UpdatedAt: updatedAt}
		pumpDevice := platformapi.Device{ID: f.pumpDeviceID, TenantID: f.tenantID, SiteID: f.siteID, Code: "ctrl-chwp-1", DisplayName: "CHWP 1 Ctrl", DeviceType: "PUMP_CONTROLLER", Status: "ACTIVE", Revision: 1, CreatedAt: createdAt, UpdatedAt: updatedAt}
		assetModel := platformapi.SiteAssetModel{
			SchemaVersion: 2, TenantID: f.tenantID, SiteID: f.siteID,
			Spaces:  []platformapi.Space{},
			Assets:  []platformapi.Asset{chillerAsset, pumpAsset},
			Devices: []platformapi.Device{chillerDevice, pumpDevice},
			Sensors: []platformapi.Sensor{},
			TelemetryPoints: []platformapi.TelemetryPoint{
				{ID: f.chillerRunStateID, TenantID: f.tenantID, SiteID: f.siteID, ReportingDeviceID: f.chillerDeviceID, PointCode: "run_state", SourceKey: "run_state", DisplayName: "Chiller A run state", PointType: "STATE", ValueType: "STRING", Writable: false, SampleIntervalMS: 60000, PublishIntervalMS: 60000, StaleAfterMS: 180000, SourceMetadata: map[string]any{}, Status: "ACTIVE", Revision: 1, CreatedAt: createdAt, UpdatedAt: updatedAt},
				{
					ID: f.chillerStartPointID, TenantID: f.tenantID, SiteID: f.siteID, ReportingDeviceID: f.chillerDeviceID,
					PointCode: "start", SourceKey: "u14", DisplayName: "Start Chiller A", PointType: "COMMAND", ValueType: "BOOLEAN", Writable: true,
					SampleIntervalMS: 60000, PublishIntervalMS: 60000, StaleAfterMS: 180000,
					SourceMetadata: map[string]any{
						"capability": "START", "capabilityRevision": "capability:start:v1", "feedbackPointKey": "run_state",
						"interlockChecks": []any{
							map[string]any{"deviceId": f.pumpDeviceID, "key": "run_state", "expected": "RUNNING", "reason": "chilled water pump must run before starting chiller"},
						},
					},
					Status: "ACTIVE", Revision: 1, CreatedAt: createdAt, UpdatedAt: updatedAt,
				},
				{ID: f.pumpRunStateID, TenantID: f.tenantID, SiteID: f.siteID, ReportingDeviceID: f.pumpDeviceID, PointCode: "run_state", SourceKey: "run_state", DisplayName: "CHWP 1 run state", PointType: "STATE", ValueType: "STRING", Writable: false, SampleIntervalMS: 60000, PublishIntervalMS: 60000, StaleAfterMS: 180000, SourceMetadata: map[string]any{}, Status: "ACTIVE", Revision: 1, CreatedAt: createdAt, UpdatedAt: updatedAt},
				{
					ID: f.pumpStopPointID, TenantID: f.tenantID, SiteID: f.siteID, ReportingDeviceID: f.pumpDeviceID,
					PointCode: "stop", SourceKey: "u11", DisplayName: "Stop CHWP 1", PointType: "COMMAND", ValueType: "BOOLEAN", Writable: true,
					SampleIntervalMS: 60000, PublishIntervalMS: 60000, StaleAfterMS: 180000,
					SourceMetadata: map[string]any{
						"capability": "STOP", "capabilityRevision": "capability:stop:v1", "feedbackPointKey": "run_state",
						"interlockChecks": []any{
							map[string]any{"deviceId": f.chillerDeviceID, "key": "run_state", "expected": "STOPPED", "reason": "chiller must stop before closing water pump"},
						},
					},
					Status: "ACTIVE", Revision: 1, CreatedAt: createdAt, UpdatedAt: updatedAt,
				},
			},
			Relationships: []platformapi.AssetRelationship{
				{ID: f.controlRelStartID, TenantID: f.tenantID, SiteID: f.siteID, FromType: "POINT", FromID: f.chillerStartPointID, ToType: "ASSET", ToID: f.chillerAssetID, Role: "CONTROLS", Status: "ACTIVE", ValidFrom: createdAt, Revision: 1, CreatedAt: createdAt, UpdatedAt: updatedAt},
				{ID: f.controlRelStopID, TenantID: f.tenantID, SiteID: f.siteID, FromType: "POINT", FromID: f.pumpStopPointID, ToType: "ASSET", ToID: f.pumpAssetID, Role: "CONTROLS", Status: "ACTIVE", ValidFrom: createdAt, Revision: 1, CreatedAt: createdAt, UpdatedAt: updatedAt},
			},
			Counts: platformapi.AssetModelCounts{
				Spaces: 0, Assets: 2, DeviceEndpoints: 2, PhysicalSensors: 0, Points: 4,
			},
		}
		switch request.URL.Path {
		case "/internal/v1/registry/assets/" + f.chillerAssetID:
			return telemetryJSONResponse(http.StatusOK, chillerAsset), nil
		case "/internal/v1/registry/assets/" + f.pumpAssetID:
			return telemetryJSONResponse(http.StatusOK, pumpAsset), nil
		case "/internal/v1/registry/sites/" + f.siteID + "/asset-model":
			return telemetryJSONResponse(http.StatusOK, assetModel), nil
		case "/internal/v1/registry/sites/" + f.siteID + "/control-state":
			return telemetryJSONResponse(http.StatusOK, siteControlStateView{TenantID: f.tenantID, SiteID: f.siteID, ControlMode: siteControlModeRemoteAuto, AssetLocks: []assetControlLockView{}}), nil
		default:
			t.Fatalf("unexpected Registry request %s", request.URL.Path)
			return nil, io.EOF
		}
	})}

	telemetryClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		val := f.chillerRunState
		deviceID := f.chillerDeviceID
		freshness := "FRESH"
		readiness := s2telemetryapi.TelemetryReadinessCurrent
		if strings.Contains(request.URL.Path, f.pumpDeviceID) {
			deviceID = f.pumpDeviceID
			val = f.pumpRunState
			if f.pumpStale {
				freshness = "STALE"
				readiness = s2telemetryapi.TelemetryReadinessDegraded
			}
		}
		rawVal, _ := json.Marshal(val)
		state := s2telemetryapi.DevicePresenceStateOnline
		display := s2telemetryapi.DeviceDisplayStateOnline
		instant := s2telemetryapi.Instant(now.Add(-time.Second).Format(time.RFC3339Nano))
		policy := s2telemetryapi.PolicyRevision(5)
		snapshot := s2telemetryapi.DeviceObservationSnapshot{
			SchemaVersion: 1, TenantId: s2telemetryapi.UUIDv7(f.tenantID), SiteId: s2telemetryapi.UUIDv7(f.siteID), DeviceId: s2telemetryapi.UUIDv7(deviceID),
			BusinessRevision: 17, EvaluatedAt: s2telemetryapi.Instant(now.Format(time.RFC3339Nano)),
			EvaluationAvailability: s2telemetryapi.EvaluationAvailabilityAvailable, AvailabilityReasons: []s2telemetryapi.AvailabilityReasonCode{},
			Presence:           s2telemetryapi.PresenceSnapshot{Applicability: s2telemetryapi.PresenceApplicabilityApplicable, CurrentState: &state, LastSeenAt: &instant, PolicyRevision: &policy},
			TelemetryReadiness: readiness,
			DisplayState:       &display,
			Values: []s2telemetryapi.TelemetryKeyState{{Present: &s2telemetryapi.TelemetryPresentState{
				Key: s2telemetryapi.TelemetryKey("run_state"), State: "PRESENT", Value: json.RawMessage(rawVal), ValueType: "STRING",
				SampledAt: instant, ReceivedAt: instant, Freshness: freshness, Quality: s2telemetryapi.TelemetryQualityGood,
				QualityReasons: []s2telemetryapi.QualityReasonCode{}, PolicyRevision: policy,
			}}},
		}
		return telemetryJSONResponse(http.StatusOK, snapshot), nil
	})}

	commandClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == internalCommandsPath && request.Method == http.MethodGet {
			listed := f.listedCommands
			if listed == nil {
				listed = []commandView{}
			}
			return telemetryJSONResponse(http.StatusOK, map[string][]commandView{"data": listed}), nil
		}
		f.commandCalls.Add(1)
		var input internalCommandCreate
		if json.NewDecoder(request.Body).Decode(&input) != nil {
			t.Fatal("invalid internalCommandCreate payload")
		}
		profile, _ := commandmodel.CapabilityProfileFor(input.Capability)
		return telemetryJSONResponse(http.StatusAccepted, commandView{
			SchemaVersion: 1, CommandID: f.commandID, TenantID: f.tenantID, SiteID: f.siteID,
			DeviceID: input.DeviceID, PointID: input.PointID, Capability: input.Capability,
			CapabilityRevision: profile.Revision, Status: commandmodel.IntentQueued,
			Risk: commandmodel.RiskHigh, ApprovalPolicy: commandmodel.ApprovalNone,
			Parameters: input.Parameters, DeviceCommandSequence: 1, Version: 3, SnapshotRevision: 1,
			Transitions: []commandTransitionView{
				{ToStatus: commandmodel.IntentSubmitted, Reason: "COMMAND_SUBMITTED", ActorType: "PRINCIPAL", OccurredAt: now.Add(-2 * time.Second), Version: 1},
				{FromStatus: commandStatusPointer(commandmodel.IntentSubmitted), ToStatus: commandmodel.IntentValidating, Reason: "COMMAND_VALIDATING", ActorType: "WORKLOAD", OccurredAt: now.Add(-time.Second), Version: 2},
				{FromStatus: commandStatusPointer(commandmodel.IntentValidating), ToStatus: commandmodel.IntentQueued, Reason: "COMMAND_QUEUED", ActorType: "WORKLOAD", OccurredAt: now, Version: 3},
			},
			CreatedAt: now.Add(-2 * time.Second), UpdatedAt: now,
		}), nil
	})}

	store := sessionstore.NewMemoryStore()
	configured := NewHandler(Config{
		Now: func() time.Time { return now },
		Identity: &IdentityConfig{
			OIDCIssuer: "https://issuer.example.test", OIDCClientID: "client", OIDCRedirectURI: "https://web.example.test/api/v1/auth/callback",
			PublicOrigin: "https://web.example.test", IAMURL: "https://iam.example.test", IAMAudience: "iam-service",
			ExecutingWorkloadSPIFFE: "spiffe://hvac.local/platform-gateway", PolicyRevision: "identity-policy-1", DelegationSigner: gatewaySigner,
			TokenEncryptionKey: make([]byte, 32), SessionStore: store, SessionTTL: time.Hour, DelegationTTL: 30 * time.Second,
			IAMHTTPClient: iamClient, OIDCHTTPClient: identityClient,
		},
		Registry:  &RegistryConfig{CoreBaseURL: "https://registry.example.test", CoreHTTPClient: registryClient, CoreTimeout: time.Second},
		Telemetry: &TelemetryConfig{RuntimeBaseURL: "https://telemetry.example.test", RuntimeHTTPClient: telemetryClient, RuntimeAudience: "telemetry-runtime-service", Timeout: time.Second},
		Command:   &CommandConfig{BackendBaseURL: "https://command.example.test", BackendHTTPClient: commandClient, BackendAudience: "command-service", IAMGrantIssuer: "spiffe://hvac.local/iam-service", Timeout: time.Second},
	}).(*handler)
	f.handler = configured
	csrfCiphertext, err := configured.identity.encryptBytes([]byte(commandTestCSRFValue()))
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateSession(context.Background(), sessionstore.Session{
		Client: sessionstore.ClientBrowser,
		ID:     "interlock-session", Principal: identitycontext.UserPrincipal{Subject: "operator-1", Issuer: "https://issuer.example.test", Roles: []string{"operator"}},
		TenantID: f.tenantID, CSRFTokenCiphertext: csrfCiphertext, ExpiresAt: now.Add(time.Hour),
	}, sessionstore.MutationContext{
		Action: "SESSION_CREATED", Result: "SUCCEEDED", PolicyRevision: "identity-policy-1", CorrelationID: "interlock-fixture",
		TraceID: strings.Repeat("a", 32), Traceparent: "00-" + strings.Repeat("a", 32) + "-" + strings.Repeat("b", 16) + "-01",
		ExecutingService: "platform-gateway", ExecutingSPIFFEID: "spiffe://hvac.local/platform-gateway", OccurredAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.sessionID = created.ID
	return f
}

func (f *interlockGatewayFixture) submit(assetID, pointID, code, key string) *httptest.ResponseRecorder {
	body := `{"assetId":"` + assetID + `","commandPointId":"` + pointID + `","parameters":{}`
	if code != "" {
		body += `,"verificationCode":"` + code + `"`
	}
	body += `}`
	req := httptest.NewRequest(http.MethodPost, publicCommandsPath, strings.NewReader(body))
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: f.sessionID})
	req.Header.Set("Origin", "https://web.example.test")
	req.Header.Set("X-CSRF-Token", commandTestCSRFValue())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

func TestGatewayCommandInterlockEnforcement(t *testing.T) {
	t.Run("chiller start blocked when pump is stopped", func(t *testing.T) {
		f := newInterlockGatewayFixture(t)
		f.pumpRunState = "STOPPED"
		rec := f.submit(f.chillerAssetID, f.chillerStartPointID, "123456", "interlock-block-start")
		if rec.Code != http.StatusConflict || f.commandCalls.Load() != 0 {
			t.Fatalf("status=%d commands=%d body=%s", rec.Code, f.commandCalls.Load(), rec.Body.String())
		}
		var problem platformapi.ProblemDetails
		if json.NewDecoder(rec.Body).Decode(&problem) != nil || problem.Code != "COMMAND_INTERLOCK_BLOCKED" {
			t.Fatalf("unexpected problem=%#v", problem)
		}
	})

	t.Run("pump stop blocked when chiller is running", func(t *testing.T) {
		f := newInterlockGatewayFixture(t)
		f.chillerRunState = "RUNNING"
		rec := f.submit(f.pumpAssetID, f.pumpStopPointID, "123456", "interlock-block-pump")
		if rec.Code != http.StatusConflict || f.commandCalls.Load() != 0 {
			t.Fatalf("status=%d commands=%d body=%s", rec.Code, f.commandCalls.Load(), rec.Body.String())
		}
		var problem platformapi.ProblemDetails
		if json.NewDecoder(rec.Body).Decode(&problem) != nil || problem.Code != "COMMAND_INTERLOCK_BLOCKED" {
			t.Fatalf("unexpected problem=%#v", problem)
		}
	})

	t.Run("stale peer telemetry returns 409 COMMAND_INTERLOCK_UNVERIFIABLE", func(t *testing.T) {
		f := newInterlockGatewayFixture(t)
		f.pumpStale = true
		rec := f.submit(f.chillerAssetID, f.chillerStartPointID, "123456", "interlock-stale-peer")
		if rec.Code != http.StatusConflict || f.commandCalls.Load() != 0 {
			t.Fatalf("status=%d commands=%d body=%s", rec.Code, f.commandCalls.Load(), rec.Body.String())
		}
		var problem platformapi.ProblemDetails
		if json.NewDecoder(rec.Body).Decode(&problem) != nil || problem.Code != "COMMAND_INTERLOCK_UNVERIFIABLE" {
			t.Fatalf("unexpected problem=%#v", problem)
		}
	})

	t.Run("satisfied interlocks with TOTP forwards command", func(t *testing.T) {
		f := newInterlockGatewayFixture(t)
		rec := f.submit(f.chillerAssetID, f.chillerStartPointID, "123456", "interlock-allow-start")
		if rec.Code != http.StatusAccepted || f.commandCalls.Load() != 1 {
			t.Fatalf("status=%d commands=%d body=%s", rec.Code, f.commandCalls.Load(), rec.Body.String())
		}
	})
}

func makeTestCommandHistoryEntry(
	id, tenantID, siteID, deviceID, pointID string,
	cap commandmodel.Capability,
	status commandmodel.IntentStatus,
	updatedAt time.Time,
) commandView {
	profile, _ := commandmodel.CapabilityProfileFor(cap)
	createdAt := updatedAt.Add(-2 * time.Second)
	return commandView{
		SchemaVersion:         1,
		CommandID:             id,
		TenantID:              tenantID,
		SiteID:                siteID,
		DeviceID:              deviceID,
		PointID:               pointID,
		Capability:            cap,
		CapabilityRevision:    profile.Revision,
		Status:                status,
		Risk:                  commandmodel.RiskHigh,
		ApprovalPolicy:        commandmodel.ApprovalNone,
		Parameters:            commandmodel.CommandParameters{},
		DeviceCommandSequence: 1,
		Version:               3,
		SnapshotRevision:      1,
		CreatedAt:             createdAt,
		UpdatedAt:             updatedAt,
		Transitions: []commandTransitionView{
			{ToStatus: commandmodel.IntentSubmitted, Reason: "COMMAND_SUBMITTED", ActorType: "PRINCIPAL", OccurredAt: createdAt, Version: 1},
			{FromStatus: commandStatusPointer(commandmodel.IntentSubmitted), ToStatus: commandmodel.IntentValidating, Reason: "COMMAND_VALIDATING", ActorType: "WORKLOAD", OccurredAt: createdAt.Add(time.Second), Version: 2},
			{FromStatus: commandStatusPointer(commandmodel.IntentValidating), ToStatus: status, Reason: "COMMAND_SETTLED", ActorType: "WORKLOAD", OccurredAt: updatedAt, Version: 3},
		},
	}
}

type plantGuardTestFixture struct {
	handler              *handler
	sessionID            string
	tenantID             string
	siteID               string
	chillerAAssetID      string
	chillerBAssetID      string
	chwp1AssetID         string
	cwp1AssetID          string
	tower1AssetID        string
	chillerADevID        string
	chillerBDevID        string
	chwp1DevID           string
	cwp1DevID            string
	tower1DevID          string
	chillerAStartPointID string
	chillerAStopPointID  string
	chillerAStopSeqPtID  string
	chillerBStopPointID  string
	chwp1StopPointID     string
	cwp1StopSeqPointID   string
	tower1StopPointID    string
	now                  time.Time
	runStates            map[string]string
	listedCommands       []commandView
	operatingSchedule    *commandOperatingSchedule
	operatingExceptions  []commandOperatingException
	siteControlMode      string
	dispatched           []internalCommandCreate
}

func newPlantGuardTestFixture(t *testing.T, localNow time.Time) *plantGuardTestFixture {
	t.Helper()
	gatewaySigner := commandTestSigner(t)
	iamSigner := commandTestSigner(t)
	f := &plantGuardTestFixture{
		tenantID:             "019a9000-1000-7000-8000-000000000001",
		siteID:               "019a9000-2000-7000-8000-000000000001",
		chillerAAssetID:      "019a9000-6000-7000-8000-000000000001",
		chillerBAssetID:      "019a9000-6000-7000-8000-000000000002",
		chwp1AssetID:         "019a9000-6000-7000-8000-000000000003",
		cwp1AssetID:          "019a9000-6000-7000-8000-000000000004",
		tower1AssetID:        "019a9000-6000-7000-8000-000000000005",
		chillerADevID:        "019a9000-3000-7000-8000-000000000001",
		chillerBDevID:        "019a9000-3000-7000-8000-000000000002",
		chwp1DevID:           "019a9000-3000-7000-8000-000000000003",
		cwp1DevID:            "019a9000-3000-7000-8000-000000000004",
		tower1DevID:          "019a9000-3000-7000-8000-000000000005",
		chillerAStartPointID: "019a9000-7100-7000-8000-000000000001",
		chillerAStopPointID:  "019a9000-7100-7000-8000-000000000002",
		chillerAStopSeqPtID:  "019a9000-7100-7000-8000-000000000003",
		chillerBStopPointID:  "019a9000-7100-7000-8000-000000000004",
		chwp1StopPointID:     "019a9000-7100-7000-8000-000000000005",
		cwp1StopSeqPointID:   "019a9000-7100-7000-8000-000000000006",
		tower1StopPointID:    "019a9000-7100-7000-8000-000000000007",
		now:                  localNow.UTC(),
		siteControlMode:      siteControlModeRemoteAuto,
		runStates: map[string]string{
			"019a9000-3000-7000-8000-000000000001": "STOPPED",
			"019a9000-3000-7000-8000-000000000002": "STOPPED",
			"019a9000-3000-7000-8000-000000000003": "RUNNING",
			"019a9000-3000-7000-8000-000000000004": "RUNNING",
			"019a9000-3000-7000-8000-000000000005": "RUNNING",
		},
	}

	principalID := "019a9000-5000-7000-8000-000000000001"
	iamClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		parent, err := identitycontext.VerifyDelegation(&gatewaySigner.PublicKey, request.Header.Get("X-Delegation-Grant"))
		if err != nil {
			t.Fatalf("invalid gateway delegation: %v", err)
		}
		now := f.now
		switch request.URL.Path {
		case "/internal/v1/principal/current":
			principal := identitycontext.UserPrincipal{Subject: parent.Subject, Issuer: parent.SubjectIssuer, Roles: []string{"site-manager"}}
			return telemetryJSONResponse(http.StatusOK, identitycontext.InternalPrincipalResponse{
				PrincipalID: principalID, Principal: principal,
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
		case "/internal/v1/registry/decision":
			var input registryauth.DecisionRequest
			if json.NewDecoder(request.Body).Decode(&input) != nil {
				t.Fatal("invalid registry decision")
			}
			decision := registryauth.Decision{
				Allowed: true, PrincipalID: principalID, SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject,
				TenantID: f.tenantID, AllowedSiteIDs: []string{f.siteID}, Actions: []registryauth.Action{input.Action},
				PolicyRevision: "registry-read:1/iam:1", ReasonCode: registryauth.ReasonAllowSiteRole, DecidedAt: now.Format(time.RFC3339Nano),
			}
			claims := registryauth.GrantClaims{
				Issuer: "spiffe://hvac.local/iam-service", Presenter: "spiffe://hvac.local/platform-gateway", Audience: "platform-core-service",
				PrincipalID: principalID, SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject, TenantID: f.tenantID,
				AllowedSiteIDs: []string{f.siteID}, Actions: []registryauth.Action{input.Action}, PolicyRevision: "registry-read:1/iam:1",
				DecisionReason: registryauth.ReasonAllowSiteRole, SessionID: parent.SessionID, ParentTokenID: parent.TokenID,
				IssuedAt: now.Unix(), ExpiresAt: now.Add(30 * time.Second).Unix(), TokenID: randomURLToken(16),
			}
			return telemetryJSONResponse(http.StatusOK, registryauth.DecisionResponse{Decision: decision, DelegationGrant: commandUnsignedRegistryGrant(claims)}), nil
		case telemetryDecisionPath:
			var input telemetryauth.DecisionRequest
			if json.NewDecoder(request.Body).Decode(&input) != nil || len(input.Targets) != 1 {
				t.Fatal("invalid telemetry decision")
			}
			target := input.Targets[0]
			canonical, _ := telemetryauth.CanonicalTargets(input.Targets)
			digest, _ := telemetryauth.ScopeDigest(input.Action, input.TenantID, canonical)
			decision := telemetryauth.Decision{
				Allowed: true, PrincipalID: principalID, SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject,
				TenantID: f.tenantID, Action: input.Action, ScopeDigest: digest, PolicyRevision: "identity-policy-1",
				ReasonCode: telemetryauth.ReasonAllowExactScope, DecidedAt: now.Format(time.RFC3339Nano),
				Targets: []telemetryauth.AuthorizedTarget{{TenantID: f.tenantID, SiteID: f.siteID, DeviceID: target.DeviceID, Keys: target.Keys}},
			}
			claims := telemetryauth.GrantClaims{
				Issuer: "spiffe://hvac.local/iam-service", Presenter: "spiffe://hvac.local/platform-gateway", Audience: "telemetry-runtime-service",
				PrincipalID: principalID, SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject, TenantID: f.tenantID,
				ActorChain: []telemetryauth.Actor{{Service: "platform-gateway", SPIFFEID: "spiffe://hvac.local/platform-gateway"}},
				Action:     input.Action, ScopeDigest: digest, TargetCount: 1, KeyCount: len(target.Keys), PolicyRevision: "identity-policy-1",
				SessionID: parent.SessionID, ParentTokenID: parent.TokenID, RequestID: request.Header.Get("X-Request-ID"),
				TraceID: traceIDFromTraceparent(request.Header.Get("Traceparent")), Route: telemetryPublicRoute(input.Action),
				IssuedAt: now.Unix(), ExpiresAt: now.Add(30 * time.Second).Unix(), TokenID: randomURLToken(16),
			}
			return telemetryJSONResponse(http.StatusOK, telemetryauth.DecisionResponse{Decision: decision, DelegationGrant: unsignedTelemetryGrant(claims)}), nil
		case commandDecisionPath:
			var input commandauth.DecisionRequest
			if json.NewDecoder(request.Body).Decode(&input) != nil {
				t.Fatal("invalid command decision")
			}
			profile, _ := commandmodel.CapabilityProfileFor(input.Capability)
			decision := commandauth.Decision{
				Allowed: true, PrincipalID: principalID, SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject,
				TenantID: f.tenantID, SiteID: f.siteID, DeviceID: input.DeviceID, Capability: input.Capability,
				CapabilityRevision: profile.Revision, Purpose: input.Purpose, MaximumRisk: commandmodel.RiskHigh,
				PolicyRevision: "identity-policy-1", EmergencyRevocationRevision: 3, ReasonCode: commandauth.ReasonAllowExactCapability,
				DecidedAt: now.Format(time.RFC3339Nano),
			}
			grantID := randomURLToken(16)
			grant, err := commandauth.SignGrant(iamSigner, commandauth.GrantClaims{
				Issuer: "spiffe://hvac.local/iam-service", Presenter: "spiffe://hvac.local/platform-gateway", Audience: "command-service",
				GrantID: grantID, Purpose: decision.Purpose, PrincipalID: principalID, TenantID: f.tenantID, SiteID: f.siteID, DeviceID: input.DeviceID,
				Capability: decision.Capability, MaximumRisk: decision.MaximumRisk, CapabilityRevision: decision.CapabilityRevision,
				PolicyRevision: decision.PolicyRevision, EmergencyRevocationRevision: 3, IssuedAt: now.Unix(), ExpiresAt: now.Add(25 * time.Second).Unix(), TokenID: grantID,
			})
			if err != nil {
				t.Fatal(err)
			}
			return telemetryJSONResponse(http.StatusOK, commandauth.DecisionResponse{Decision: decision, DelegationGrant: grant}), nil
		default:
			t.Fatalf("unexpected IAM path %s", request.URL.Path)
			return nil, io.EOF
		}
	})}

	identityClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var payload struct {
			Code string `json:"code"`
		}
		if json.NewDecoder(request.Body).Decode(&payload) != nil || payload.Code != "123456" {
			return telemetryJSONResponse(http.StatusUnauthorized, map[string]any{"error": "mfa_code_invalid"}), nil
		}
		return telemetryJSONResponse(http.StatusOK, map[string]any{"verified": true, "method": "TOTP"}), nil
	})}

	registryClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		ts := f.now.Format("2006-01-02T15:04:05.000Z")
		startProf, _ := commandmodel.CapabilityProfileFor(commandmodel.CapabilityStart)
		stopProf, _ := commandmodel.CapabilityProfileFor(commandmodel.CapabilityStop)
		stopSeqProf, _ := commandmodel.CapabilityProfileFor(commandmodel.CapabilityStopUnitSequence)
		assets := []platformapi.Asset{
			{ID: f.chillerAAssetID, TenantID: f.tenantID, SiteID: f.siteID, Code: "chiller-a", DisplayName: "Chiller A", AssetType: "CHILLER", Status: "ACTIVE", Revision: 1, CreatedAt: ts, UpdatedAt: ts},
			{ID: f.chillerBAssetID, TenantID: f.tenantID, SiteID: f.siteID, Code: "chiller-b", DisplayName: "Chiller B", AssetType: "CHILLER", Status: "ACTIVE", Revision: 1, CreatedAt: ts, UpdatedAt: ts},
			{ID: f.chwp1AssetID, TenantID: f.tenantID, SiteID: f.siteID, Code: "chwp-1", DisplayName: "CHWP 1", AssetType: "CHILLED_WATER_PUMP", Status: "ACTIVE", Revision: 1, CreatedAt: ts, UpdatedAt: ts},
			{ID: f.cwp1AssetID, TenantID: f.tenantID, SiteID: f.siteID, Code: "cwp-1", DisplayName: "CWP 1", AssetType: "COOLING_WATER_PUMP", Status: "ACTIVE", Revision: 1, CreatedAt: ts, UpdatedAt: ts},
			{ID: f.tower1AssetID, TenantID: f.tenantID, SiteID: f.siteID, Code: "tower-1", DisplayName: "Tower 1", AssetType: "COOLING_TOWER", Status: "ACTIVE", Revision: 1, CreatedAt: ts, UpdatedAt: ts},
		}
		devices := []platformapi.Device{
			{ID: f.chillerADevID, TenantID: f.tenantID, SiteID: f.siteID, Code: "ctrl-chiller-a", DisplayName: "Ctrl A", DeviceType: "EDGE_GATEWAY", Status: "ACTIVE", Revision: 1, CreatedAt: ts, UpdatedAt: ts},
			{ID: f.chillerBDevID, TenantID: f.tenantID, SiteID: f.siteID, Code: "ctrl-chiller-b", DisplayName: "Ctrl B", DeviceType: "EDGE_GATEWAY", Status: "ACTIVE", Revision: 1, CreatedAt: ts, UpdatedAt: ts},
			{ID: f.chwp1DevID, TenantID: f.tenantID, SiteID: f.siteID, Code: "ctrl-chwp-1", DisplayName: "Ctrl CHWP 1", DeviceType: "EDGE_GATEWAY", Status: "ACTIVE", Revision: 1, CreatedAt: ts, UpdatedAt: ts},
			{ID: f.cwp1DevID, TenantID: f.tenantID, SiteID: f.siteID, Code: "ctrl-cwp-1", DisplayName: "Ctrl CWP 1", DeviceType: "EDGE_GATEWAY", Status: "ACTIVE", Revision: 1, CreatedAt: ts, UpdatedAt: ts},
			{ID: f.tower1DevID, TenantID: f.tenantID, SiteID: f.siteID, Code: "ctrl-tower-1", DisplayName: "Ctrl Tower 1", DeviceType: "EDGE_GATEWAY", Status: "ACTIVE", Revision: 1, CreatedAt: ts, UpdatedAt: ts},
		}
		points := []platformapi.TelemetryPoint{
			{ID: "019a9000-7000-7000-8000-000000000001", TenantID: f.tenantID, SiteID: f.siteID, ReportingDeviceID: f.chillerADevID, PointCode: "run_state", SourceKey: "run_state", DisplayName: "Chiller A run", PointType: "STATE", ValueType: "STRING", Writable: false, SampleIntervalMS: 60000, PublishIntervalMS: 60000, StaleAfterMS: 180000, SourceMetadata: map[string]any{}, Status: "ACTIVE", Revision: 1, CreatedAt: ts, UpdatedAt: ts},
			{ID: "019a9000-7000-7000-8000-000000000002", TenantID: f.tenantID, SiteID: f.siteID, ReportingDeviceID: f.chillerBDevID, PointCode: "run_state", SourceKey: "run_state", DisplayName: "Chiller B run", PointType: "STATE", ValueType: "STRING", Writable: false, SampleIntervalMS: 60000, PublishIntervalMS: 60000, StaleAfterMS: 180000, SourceMetadata: map[string]any{}, Status: "ACTIVE", Revision: 1, CreatedAt: ts, UpdatedAt: ts},
			{ID: "019a9000-7000-7000-8000-000000000003", TenantID: f.tenantID, SiteID: f.siteID, ReportingDeviceID: f.chwp1DevID, PointCode: "run_state", SourceKey: "run_state", DisplayName: "CHWP 1 run", PointType: "STATE", ValueType: "STRING", Writable: false, SampleIntervalMS: 60000, PublishIntervalMS: 60000, StaleAfterMS: 180000, SourceMetadata: map[string]any{}, Status: "ACTIVE", Revision: 1, CreatedAt: ts, UpdatedAt: ts},
			{ID: "019a9000-7000-7000-8000-000000000004", TenantID: f.tenantID, SiteID: f.siteID, ReportingDeviceID: f.cwp1DevID, PointCode: "run_state", SourceKey: "run_state", DisplayName: "CWP 1 run", PointType: "STATE", ValueType: "STRING", Writable: false, SampleIntervalMS: 60000, PublishIntervalMS: 60000, StaleAfterMS: 180000, SourceMetadata: map[string]any{}, Status: "ACTIVE", Revision: 1, CreatedAt: ts, UpdatedAt: ts},
			{ID: "019a9000-7000-7000-8000-000000000005", TenantID: f.tenantID, SiteID: f.siteID, ReportingDeviceID: f.tower1DevID, PointCode: "run_state", SourceKey: "run_state", DisplayName: "Tower 1 run", PointType: "STATE", ValueType: "STRING", Writable: false, SampleIntervalMS: 60000, PublishIntervalMS: 60000, StaleAfterMS: 180000, SourceMetadata: map[string]any{}, Status: "ACTIVE", Revision: 1, CreatedAt: ts, UpdatedAt: ts},
			{ID: f.chillerAStartPointID, TenantID: f.tenantID, SiteID: f.siteID, ReportingDeviceID: f.chillerADevID, PointCode: "start_a", SourceKey: "start_a", DisplayName: "Start A", PointType: "COMMAND", ValueType: "BOOLEAN", Writable: true, SampleIntervalMS: 60000, PublishIntervalMS: 60000, StaleAfterMS: 180000, SourceMetadata: map[string]any{"capability": "START", "capabilityRevision": startProf.Revision, "feedbackPointKey": "run_state"}, Status: "ACTIVE", Revision: 1, CreatedAt: ts, UpdatedAt: ts},
			{ID: f.chillerAStopPointID, TenantID: f.tenantID, SiteID: f.siteID, ReportingDeviceID: f.chillerADevID, PointCode: "stop_a", SourceKey: "stop_a", DisplayName: "Stop A", PointType: "COMMAND", ValueType: "BOOLEAN", Writable: true, SampleIntervalMS: 60000, PublishIntervalMS: 60000, StaleAfterMS: 180000, SourceMetadata: map[string]any{"capability": "STOP", "capabilityRevision": stopProf.Revision, "feedbackPointKey": "run_state"}, Status: "ACTIVE", Revision: 1, CreatedAt: ts, UpdatedAt: ts},
			{ID: f.chillerAStopSeqPtID, TenantID: f.tenantID, SiteID: f.siteID, ReportingDeviceID: f.chillerADevID, PointCode: "stop_seq_a", SourceKey: "stop_seq_a", DisplayName: "Stop Seq A", PointType: "COMMAND", ValueType: "BOOLEAN", Writable: true, SampleIntervalMS: 60000, PublishIntervalMS: 60000, StaleAfterMS: 180000, SourceMetadata: map[string]any{"capability": "STOP_UNIT_SEQUENCE", "capabilityRevision": stopSeqProf.Revision, "feedbackPointKey": "run_state"}, Status: "ACTIVE", Revision: 1, CreatedAt: ts, UpdatedAt: ts},
			{ID: f.chillerBStopPointID, TenantID: f.tenantID, SiteID: f.siteID, ReportingDeviceID: f.chillerBDevID, PointCode: "stop_b", SourceKey: "stop_b", DisplayName: "Stop B", PointType: "COMMAND", ValueType: "BOOLEAN", Writable: true, SampleIntervalMS: 60000, PublishIntervalMS: 60000, StaleAfterMS: 180000, SourceMetadata: map[string]any{"capability": "STOP", "capabilityRevision": stopProf.Revision, "feedbackPointKey": "run_state"}, Status: "ACTIVE", Revision: 1, CreatedAt: ts, UpdatedAt: ts},
			{ID: f.chwp1StopPointID, TenantID: f.tenantID, SiteID: f.siteID, ReportingDeviceID: f.chwp1DevID, PointCode: "stop_chwp1", SourceKey: "stop_chwp1", DisplayName: "Stop CHWP 1", PointType: "COMMAND", ValueType: "BOOLEAN", Writable: true, SampleIntervalMS: 60000, PublishIntervalMS: 60000, StaleAfterMS: 180000, SourceMetadata: map[string]any{"capability": "STOP", "capabilityRevision": stopProf.Revision, "feedbackPointKey": "run_state", "interlockChecks": []any{map[string]any{"deviceId": f.chillerADevID, "key": "run_state", "expected": "STOPPED", "reason": "chiller-a must stop before chwp-1"}}}, Status: "ACTIVE", Revision: 1, CreatedAt: ts, UpdatedAt: ts},
			{ID: f.cwp1StopSeqPointID, TenantID: f.tenantID, SiteID: f.siteID, ReportingDeviceID: f.cwp1DevID, PointCode: "stop_cwp1", SourceKey: "stop_cwp1", DisplayName: "Stop CWP 1", PointType: "COMMAND", ValueType: "BOOLEAN", Writable: true, SampleIntervalMS: 60000, PublishIntervalMS: 60000, StaleAfterMS: 180000, SourceMetadata: map[string]any{"capability": "STOP_UNIT_SEQUENCE", "capabilityRevision": stopSeqProf.Revision, "feedbackPointKey": "run_state"}, Status: "ACTIVE", Revision: 1, CreatedAt: ts, UpdatedAt: ts},
			{ID: f.tower1StopPointID, TenantID: f.tenantID, SiteID: f.siteID, ReportingDeviceID: f.tower1DevID, PointCode: "stop_tower1", SourceKey: "stop_tower1", DisplayName: "Stop Tower 1", PointType: "COMMAND", ValueType: "BOOLEAN", Writable: true, SampleIntervalMS: 60000, PublishIntervalMS: 60000, StaleAfterMS: 180000, SourceMetadata: map[string]any{"capability": "STOP", "capabilityRevision": stopProf.Revision, "feedbackPointKey": "run_state"}, Status: "ACTIVE", Revision: 1, CreatedAt: ts, UpdatedAt: ts},
		}
		rels := []platformapi.AssetRelationship{
			{ID: "019a9000-9100-7000-8000-000000000001", TenantID: f.tenantID, SiteID: f.siteID, FromType: "POINT", FromID: f.chillerAStartPointID, ToType: "ASSET", ToID: f.chillerAAssetID, Role: "CONTROLS", Status: "ACTIVE", ValidFrom: ts, Revision: 1, CreatedAt: ts, UpdatedAt: ts},
			{ID: "019a9000-9100-7000-8000-000000000002", TenantID: f.tenantID, SiteID: f.siteID, FromType: "POINT", FromID: f.chillerAStopPointID, ToType: "ASSET", ToID: f.chillerAAssetID, Role: "CONTROLS", Status: "ACTIVE", ValidFrom: ts, Revision: 1, CreatedAt: ts, UpdatedAt: ts},
			{ID: "019a9000-9100-7000-8000-000000000003", TenantID: f.tenantID, SiteID: f.siteID, FromType: "POINT", FromID: f.chillerAStopSeqPtID, ToType: "ASSET", ToID: f.chillerAAssetID, Role: "CONTROLS", Status: "ACTIVE", ValidFrom: ts, Revision: 1, CreatedAt: ts, UpdatedAt: ts},
			{ID: "019a9000-9100-7000-8000-000000000004", TenantID: f.tenantID, SiteID: f.siteID, FromType: "POINT", FromID: f.chillerBStopPointID, ToType: "ASSET", ToID: f.chillerBAssetID, Role: "CONTROLS", Status: "ACTIVE", ValidFrom: ts, Revision: 1, CreatedAt: ts, UpdatedAt: ts},
			{ID: "019a9000-9100-7000-8000-000000000005", TenantID: f.tenantID, SiteID: f.siteID, FromType: "POINT", FromID: f.chwp1StopPointID, ToType: "ASSET", ToID: f.chwp1AssetID, Role: "CONTROLS", Status: "ACTIVE", ValidFrom: ts, Revision: 1, CreatedAt: ts, UpdatedAt: ts},
			{ID: "019a9000-9100-7000-8000-000000000006", TenantID: f.tenantID, SiteID: f.siteID, FromType: "POINT", FromID: f.cwp1StopSeqPointID, ToType: "ASSET", ToID: f.cwp1AssetID, Role: "CONTROLS", Status: "ACTIVE", ValidFrom: ts, Revision: 1, CreatedAt: ts, UpdatedAt: ts},
			{ID: "019a9000-9100-7000-8000-000000000007", TenantID: f.tenantID, SiteID: f.siteID, FromType: "POINT", FromID: f.tower1StopPointID, ToType: "ASSET", ToID: f.tower1AssetID, Role: "CONTROLS", Status: "ACTIVE", ValidFrom: ts, Revision: 1, CreatedAt: ts, UpdatedAt: ts},
			// chwp1 links via interlockChecks + SERVES; cwp1 and tower1 link via SERVES -> chillerA/B.
			{ID: "019a9000-9200-7000-8000-000000000001", TenantID: f.tenantID, SiteID: f.siteID, FromType: "ASSET", FromID: f.cwp1AssetID, ToType: "ASSET", ToID: f.chillerAAssetID, Role: "SERVES", Status: "ACTIVE", ValidFrom: ts, Revision: 1, CreatedAt: ts, UpdatedAt: ts},
			{ID: "019a9000-9200-7000-8000-000000000002", TenantID: f.tenantID, SiteID: f.siteID, FromType: "ASSET", FromID: f.tower1AssetID, ToType: "ASSET", ToID: f.chillerAAssetID, Role: "SERVES", Status: "ACTIVE", ValidFrom: ts, Revision: 1, CreatedAt: ts, UpdatedAt: ts},
		}
		model := platformapi.SiteAssetModel{
			SchemaVersion: 2, TenantID: f.tenantID, SiteID: f.siteID,
			Spaces: []platformapi.Space{}, Assets: assets, Devices: devices, Sensors: []platformapi.Sensor{},
			TelemetryPoints: points, Relationships: rels,
			Counts: platformapi.AssetModelCounts{Assets: len(assets), DeviceEndpoints: len(devices), Points: len(points)},
		}
		switch request.URL.Path {
		case "/internal/v1/registry/sites/" + f.siteID + "/asset-model":
			return telemetryJSONResponse(http.StatusOK, model), nil
		case "/internal/v1/registry/sites/" + f.siteID + "/control-state":
			return telemetryJSONResponse(http.StatusOK, siteControlStateView{TenantID: f.tenantID, SiteID: f.siteID, ControlMode: f.siteControlMode, AssetLocks: []assetControlLockView{}}), nil
		case "/internal/v1/registry/sites/" + f.siteID + "/control-mode":
			var body struct {
				ControlMode string `json:"controlMode"`
			}
			if json.NewDecoder(request.Body).Decode(&body) == nil && body.ControlMode != "" {
				f.siteControlMode = body.ControlMode
			}
			return telemetryJSONResponse(http.StatusOK, siteControlStateView{TenantID: f.tenantID, SiteID: f.siteID, ControlMode: f.siteControlMode, Revision: 1, AssetLocks: []assetControlLockView{}}), nil
		case "/internal/v1/registry/sites/" + f.siteID + "/operating-schedule":
			if f.operatingSchedule == nil {
				return telemetryJSONResponse(http.StatusOK, commandOperatingSchedule{Timezone: "Asia/Shanghai", Slots: []commandOperatingScheduleSlot{}}), nil
			}
			return telemetryJSONResponse(http.StatusOK, f.operatingSchedule), nil
		case "/internal/v1/registry/sites/" + f.siteID + "/operating-exceptions":
			items := f.operatingExceptions
			if items == nil {
				items = []commandOperatingException{}
			}
			return telemetryJSONResponse(http.StatusOK, commandOperatingExceptionList{Items: items}), nil
		default:
			for _, a := range assets {
				if request.URL.Path == "/internal/v1/registry/assets/"+a.ID {
					return telemetryJSONResponse(http.StatusOK, a), nil
				}
			}
			t.Fatalf("unexpected Registry request %s", request.URL.Path)
			return nil, io.EOF
		}
	})}

	telemetryClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		parts := strings.Split(strings.Trim(request.URL.Path, "/"), "/")
		deviceID := parts[len(parts)-2]
		val := f.runStates[deviceID]
		if val == "" {
			val = "STOPPED"
		}
		rawVal, _ := json.Marshal(val)
		state := s2telemetryapi.DevicePresenceStateOnline
		display := s2telemetryapi.DeviceDisplayStateOnline
		instant := s2telemetryapi.Instant(f.now.Add(-time.Second).Format(time.RFC3339Nano))
		policy := s2telemetryapi.PolicyRevision(5)
		snapshot := s2telemetryapi.DeviceObservationSnapshot{
			SchemaVersion: 1, TenantId: s2telemetryapi.UUIDv7(f.tenantID), SiteId: s2telemetryapi.UUIDv7(f.siteID), DeviceId: s2telemetryapi.UUIDv7(deviceID),
			BusinessRevision: 17, EvaluatedAt: s2telemetryapi.Instant(f.now.Format(time.RFC3339Nano)),
			EvaluationAvailability: s2telemetryapi.EvaluationAvailabilityAvailable, AvailabilityReasons: []s2telemetryapi.AvailabilityReasonCode{},
			Presence:           s2telemetryapi.PresenceSnapshot{Applicability: s2telemetryapi.PresenceApplicabilityApplicable, CurrentState: &state, LastSeenAt: &instant, PolicyRevision: &policy},
			TelemetryReadiness: s2telemetryapi.TelemetryReadinessCurrent, DisplayState: &display,
			Values: []s2telemetryapi.TelemetryKeyState{{Present: &s2telemetryapi.TelemetryPresentState{
				Key: s2telemetryapi.TelemetryKey("run_state"), State: "PRESENT", Value: json.RawMessage(rawVal), ValueType: "STRING",
				SampledAt: instant, ReceivedAt: instant, Freshness: "FRESH", Quality: s2telemetryapi.TelemetryQualityGood,
				QualityReasons: []s2telemetryapi.QualityReasonCode{}, PolicyRevision: policy,
			}}},
		}
		return telemetryJSONResponse(http.StatusOK, snapshot), nil
	})}

	seq := 0
	commandClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == internalCommandsPath && request.Method == http.MethodGet {
			listed := f.listedCommands
			if listed == nil {
				listed = []commandView{}
			}
			return telemetryJSONResponse(http.StatusOK, map[string][]commandView{"data": listed}), nil
		}
		if request.URL.Path == internalCommandsCancelPendingPath && request.Method == http.MethodPost {
			return telemetryJSONResponse(http.StatusOK, map[string]any{"cancelledCommandIds": []string{}}), nil
		}
		if request.URL.Path == internalCommandsPath && request.Method == http.MethodPost {
			var input internalCommandCreate
			if json.NewDecoder(request.Body).Decode(&input) != nil {
				t.Fatal("invalid internalCommandCreate payload")
			}
			f.dispatched = append(f.dispatched, input)
			seq++
			cmdID := fmt.Sprintf("019a9000-cccc-7000-8000-%012d", seq)
			return telemetryJSONResponse(http.StatusAccepted, makeTestCommandHistoryEntry(cmdID, f.tenantID, f.siteID, input.DeviceID, input.PointID, input.Capability, commandmodel.IntentQueued, f.now)), nil
		}
		t.Fatalf("unexpected command call %s %s", request.Method, request.URL.Path)
		return nil, io.EOF
	})}

	store := sessionstore.NewMemoryStore()
	configured := NewHandler(Config{
		Now: func() time.Time { return f.now },
		Identity: &IdentityConfig{
			OIDCIssuer: "https://issuer.example.test", OIDCClientID: "client", OIDCRedirectURI: "https://web.example.test/api/v1/auth/callback",
			PublicOrigin: "https://web.example.test", IAMURL: "https://iam.example.test", IAMAudience: "iam-service",
			ExecutingWorkloadSPIFFE: "spiffe://hvac.local/platform-gateway", PolicyRevision: "identity-policy-1", DelegationSigner: gatewaySigner,
			TokenEncryptionKey: make([]byte, 32), SessionStore: store, SessionTTL: time.Hour, DelegationTTL: 30 * time.Second,
			IAMHTTPClient: iamClient, OIDCHTTPClient: identityClient,
		},
		Registry:  &RegistryConfig{CoreBaseURL: "https://registry.example.test", CoreHTTPClient: registryClient, CoreTimeout: time.Second},
		Telemetry: &TelemetryConfig{RuntimeBaseURL: "https://telemetry.example.test", RuntimeHTTPClient: telemetryClient, RuntimeAudience: "telemetry-runtime-service", Timeout: time.Second},
		Command:   &CommandConfig{BackendBaseURL: "https://command.example.test", BackendHTTPClient: commandClient, BackendAudience: "command-service", IAMGrantIssuer: "spiffe://hvac.local/iam-service", Timeout: time.Second},
	}).(*handler)
	f.handler = configured

	csrfCiphertext, err := configured.identity.encryptBytes([]byte(commandTestCSRFValue()))
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateSession(context.Background(), sessionstore.Session{
		Client: sessionstore.ClientBrowser, ID: "plant-guard-session",
		Principal:           identitycontext.UserPrincipal{Subject: "manager-1", Issuer: "https://issuer.example.test", Roles: []string{"site-manager"}},
		TenantID:            f.tenantID,
		CSRFTokenCiphertext: csrfCiphertext,
		ExpiresAt:           f.now.Add(2 * time.Hour),
		AuthenticationACR:   authenticationACRMFA,
		AuthenticationAMR:   []string{"pwd", "otp"},
		AuthenticationTime:  f.now,
	}, sessionstore.MutationContext{
		Action: "SESSION_CREATED", Result: "SUCCEEDED", PolicyRevision: "identity-policy-1", CorrelationID: "plant-guard-fixture",
		TraceID: strings.Repeat("a", 32), Traceparent: "00-" + strings.Repeat("a", 32) + "-" + strings.Repeat("b", 16) + "-01",
		ExecutingService: "platform-gateway", ExecutingSPIFFEID: "spiffe://hvac.local/platform-gateway", OccurredAt: f.now,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.sessionID = created.ID
	return f
}

func (f *plantGuardTestFixture) submit(assetID, pointID, key string) *httptest.ResponseRecorder {
	body := fmt.Sprintf(`{"assetId":%q,"commandPointId":%q,"parameters":{},"verificationCode":"123456"}`, assetID, pointID)
	req := httptest.NewRequest(http.MethodPost, publicCommandsPath, strings.NewReader(body))
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: f.sessionID})
	req.Header.Set("Origin", "https://web.example.test")
	req.Header.Set("X-CSRF-Token", commandTestCSRFValue())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

func TestGatewayAuxiliaryStopStartupBlindWindowGuards(t *testing.T) {
	shanghai, _ := time.LoadLocation("Asia/Shanghai")
	base := time.Date(2026, 11, 16, 10, 0, 0, 0, shanghai)

	t.Run("blocks_chwp_cwp_and_tower_stop_during_inflight_chiller_start", func(t *testing.T) {
		for _, status := range []commandmodel.IntentStatus{
			commandmodel.IntentSubmitted, commandmodel.IntentQueued, commandmodel.IntentDispatching, commandmodel.IntentOutcomeUnknown,
		} {
			f := newPlantGuardTestFixture(t, base)
			f.listedCommands = []commandView{
				makeTestCommandHistoryEntry("019a9000-aaaa-7000-8000-000000000001", f.tenantID, f.siteID, f.chillerADevID, f.chillerAStartPointID, commandmodel.CapabilityStartUnitSequence, status, f.now.Add(-time.Minute)),
			}
			for _, tc := range []struct {
				assetID string
				pointID string
				name    string
			}{
				{f.chwp1AssetID, f.chwp1StopPointID, "chwp1"},
				{f.cwp1AssetID, f.cwp1StopSeqPointID, "cwp1"},
				{f.tower1AssetID, f.tower1StopPointID, "tower1"},
			} {
				rec := f.submit(tc.assetID, tc.pointID, "aux-inflight-"+tc.name+"-"+string(status))
				if rec.Code != http.StatusConflict {
					t.Fatalf("%s status=%s code=%d body=%s", tc.name, status, rec.Code, rec.Body.String())
				}
				var problem platformapi.ProblemDetails
				if json.NewDecoder(rec.Body).Decode(&problem) != nil || problem.Code != "COMMAND_INTERLOCK_BLOCKED" {
					t.Fatalf("%s status=%s code=%d problem=%#v", tc.name, status, rec.Code, problem)
				}
			}
			if len(f.dispatched) != 0 {
				t.Fatalf("expected zero commands dispatched during active chiller start")
			}
		}
	})

	t.Run("blocks_during_6m_blind_window_and_allows_after_6m_or_terminal_stop", func(t *testing.T) {
		f := newPlantGuardTestFixture(t, base)
		// Even with chiller telemetry still showing STOPPED, 3m since START SUCCEEDED is inside 6m blind window.
		startAt := f.now.Add(-3 * time.Minute)
		f.listedCommands = []commandView{
			makeTestCommandHistoryEntry("019a9000-aaaa-7000-8000-000000000002", f.tenantID, f.siteID, f.chillerADevID, f.chillerAStartPointID, commandmodel.CapabilityStart, commandmodel.IntentSucceeded, startAt),
		}
		recBlocked := f.submit(f.chwp1AssetID, f.chwp1StopPointID, "aux-blind-3m")
		if recBlocked.Code != http.StatusConflict {
			t.Fatalf("expected 409, got code=%d body=%s", recBlocked.Code, recBlocked.Body.String())
		}
		var problem platformapi.ProblemDetails
		if json.NewDecoder(recBlocked.Body).Decode(&problem) != nil ||
			problem.Code != "COMMAND_INTERLOCK_BLOCKED" || !strings.Contains(problem.Detail, "6-minute") {
			t.Fatalf("expected 409 COMMAND_INTERLOCK_BLOCKED with 6-minute explanation, code=%d problem=%#v", recBlocked.Code, problem)
		}

		// A newer terminal STOP supersedes the recent START.
		f.listedCommands = append(f.listedCommands,
			makeTestCommandHistoryEntry("019a9000-aaaa-7000-8000-000000000003", f.tenantID, f.siteID, f.chillerADevID, f.chillerAStopPointID, commandmodel.CapabilityStop, commandmodel.IntentSucceeded, startAt.Add(time.Minute)),
		)
		recSuperseded := f.submit(f.tower1AssetID, f.tower1StopPointID, "aux-superseded-stop")
		if recSuperseded.Code != http.StatusAccepted {
			t.Fatalf("expected superseded terminal STOP to allow aux STOP, code=%d body=%s", recSuperseded.Code, recSuperseded.Body.String())
		}

		// Without superseding STOP, advancing past exact 6m boundary allows aux STOP.
		fBoundary := newPlantGuardTestFixture(t, base)
		fBoundary.listedCommands = []commandView{
			makeTestCommandHistoryEntry("019a9000-aaaa-7000-8000-000000000004", fBoundary.tenantID, fBoundary.siteID, fBoundary.chillerADevID, fBoundary.chillerAStartPointID, commandmodel.CapabilityStart, commandmodel.IntentSucceeded, fBoundary.now.Add(-6*time.Minute)),
		}
		recAllowed := fBoundary.submit(fBoundary.chwp1AssetID, fBoundary.chwp1StopPointID, "aux-boundary-6m")
		if recAllowed.Code != http.StatusAccepted {
			t.Fatalf("expected 202 at exact 6m boundary, code=%d body=%s", recAllowed.Code, recAllowed.Body.String())
		}
	})
}

func TestGatewayChillerCooldownAndLastActiveChillerGuards(t *testing.T) {
	shanghai, _ := time.LoadLocation("Asia/Shanghai")
	// Monday 2026-11-16 10:00 CST (DayOfWeek=1, minute=600)
	base := time.Date(2026, 11, 16, 10, 0, 0, 0, shanghai)

	t.Run("chiller_min_run_20m_blocks_stop_and_allows_at_20m", func(t *testing.T) {
		f := newPlantGuardTestFixture(t, base)
		f.runStates[f.chillerADevID] = "RUNNING"
		f.listedCommands = []commandView{
			makeTestCommandHistoryEntry("019a9000-aaaa-7000-8000-000000000010", f.tenantID, f.siteID, f.chillerADevID, f.chillerAStartPointID, commandmodel.CapabilityStart, commandmodel.IntentSucceeded, f.now.Add(-12*time.Minute)),
		}
		rec := f.submit(f.chillerAAssetID, f.chillerAStopPointID, "chiller-stop-12m")
		if rec.Code != http.StatusConflict {
			t.Fatalf("expected 409, got code=%d body=%s", rec.Code, rec.Body.String())
		}
		var problem platformapi.ProblemDetails
		if json.NewDecoder(rec.Body).Decode(&problem) != nil ||
			problem.Code != "COMMAND_COOLDOWN_ACTIVE" || !strings.Contains(problem.Detail, "8m0s") {
			t.Fatalf("expected 409 COMMAND_COOLDOWN_ACTIVE (8m0s), code=%d problem=%#v", rec.Code, problem)
		}

		// At exact 20m boundary with no active cooling schedule, STOP is allowed.
		f.listedCommands[0].UpdatedAt = f.now.Add(-20 * time.Minute)
		recOk := f.submit(f.chillerAAssetID, f.chillerAStopPointID, "chiller-stop-20m")
		if recOk.Code != http.StatusAccepted {
			t.Fatalf("expected 202 at 20m min-run boundary, code=%d body=%s", recOk.Code, recOk.Body.String())
		}
	})

	t.Run("chiller_min_off_15m_and_max_2_starts_per_hour", func(t *testing.T) {
		f := newPlantGuardTestFixture(t, base)
		f.listedCommands = []commandView{
			makeTestCommandHistoryEntry("019a9000-aaaa-7000-8000-000000000011", f.tenantID, f.siteID, f.chillerADevID, f.chillerAStopPointID, commandmodel.CapabilityStopUnitSequence, commandmodel.IntentSucceeded, f.now.Add(-8*time.Minute)),
		}
		recMinOff := f.submit(f.chillerAAssetID, f.chillerAStartPointID, "chiller-start-8m")
		var problem platformapi.ProblemDetails
		if recMinOff.Code != http.StatusConflict || json.NewDecoder(recMinOff.Body).Decode(&problem) != nil ||
			problem.Code != "COMMAND_COOLDOWN_ACTIVE" || !strings.Contains(problem.Detail, "7m0s") {
			t.Fatalf("expected 409 COMMAND_COOLDOWN_ACTIVE min_off (7m0s), code=%d problem=%#v", recMinOff.Code, problem)
		}

		// After 15m min_off is satisfied, >= 2 starts within last 60m still trips max_starts_per_hour = 2.
		fRate := newPlantGuardTestFixture(t, base)
		fRate.listedCommands = []commandView{
			makeTestCommandHistoryEntry("019a9000-aaaa-7000-8000-000000000012", fRate.tenantID, fRate.siteID, fRate.chillerADevID, fRate.chillerAStartPointID, commandmodel.CapabilityStart, commandmodel.IntentSucceeded, fRate.now.Add(-50*time.Minute)),
			makeTestCommandHistoryEntry("019a9000-aaaa-7000-8000-000000000013", fRate.tenantID, fRate.siteID, fRate.chillerADevID, fRate.chillerAStartPointID, commandmodel.CapabilityStartUnitSequence, commandmodel.IntentSucceeded, fRate.now.Add(-25*time.Minute)),
			makeTestCommandHistoryEntry("019a9000-aaaa-7000-8000-000000000014", fRate.tenantID, fRate.siteID, fRate.chillerADevID, fRate.chillerAStopPointID, commandmodel.CapabilityStop, commandmodel.IntentSucceeded, fRate.now.Add(-16*time.Minute)),
		}
		recLimit := fRate.submit(fRate.chillerAAssetID, fRate.chillerAStartPointID, "chiller-start-rate-limit")
		if recLimit.Code != http.StatusConflict || json.NewDecoder(recLimit.Body).Decode(&problem) != nil ||
			problem.Code != "COMMAND_COOLDOWN_ACTIVE" || !strings.Contains(problem.Detail, "maximum of 2 starts per 60 minutes") {
			t.Fatalf("expected 409 COMMAND_COOLDOWN_ACTIVE max starts limit, code=%d problem=%#v", recLimit.Code, problem)
		}

		// Cancelled or failed starts do NOT count toward the 2 starts/hour limit.
		fRate.listedCommands[0].Status = commandmodel.IntentFailed
		recAfterFailedExcluded := fRate.submit(fRate.chillerAAssetID, fRate.chillerAStartPointID, "chiller-start-failed-excluded")
		if recAfterFailedExcluded.Code != http.StatusAccepted {
			t.Fatalf("expected failed start excluded from rate limit, code=%d body=%s", recAfterFailedExcluded.Code, recAfterFailedExcluded.Body.String())
		}
	})

	t.Run("last_active_chiller_protected_during_cooling_slot_unless_second_running_or_shutdown_exception", func(t *testing.T) {
		f := newPlantGuardTestFixture(t, base)
		f.runStates[f.chillerADevID] = "RUNNING"
		f.runStates[f.chillerBDevID] = "STOPPED"
		f.operatingSchedule = &commandOperatingSchedule{
			Timezone: "Asia/Shanghai",
			Slots:    []commandOperatingScheduleSlot{{DayOfWeek: 1, StartMinute: 480, EndMinute: 1260}},
		}

		recBlocked := f.submit(f.chillerAAssetID, f.chillerAStopSeqPtID, "last-chiller-blocked")
		if recBlocked.Code != http.StatusConflict {
			t.Fatalf("expected 409, got code=%d body=%s", recBlocked.Code, recBlocked.Body.String())
		}
		var problem platformapi.ProblemDetails
		if json.NewDecoder(recBlocked.Body).Decode(&problem) != nil ||
			problem.Code != "COMMAND_LAST_ACTIVE_CHILLER_PROTECTED" || !strings.Contains(problem.Detail, "PLANT_SAFE_SHUTDOWN") {
			t.Fatalf("expected 409 COMMAND_LAST_ACTIVE_CHILLER_PROTECTED, code=%d problem=%#v", recBlocked.Code, problem)
		}

		// When a 2nd chiller is ALSO RUNNING, stopping chiller-a is permitted.
		f.runStates[f.chillerBDevID] = "RUNNING"
		recTwoRunning := f.submit(f.chillerAAssetID, f.chillerAStopPointID, "two-chillers-running")
		if recTwoRunning.Code != http.StatusAccepted {
			t.Fatalf("expected 202 when second chiller is running, code=%d body=%s", recTwoRunning.Code, recTwoRunning.Body.String())
		}

		// With only 1 chiller running, an approved MAINTENANCE_WINDOW / HOLIDAY_SHUTDOWN bypasses last-chiller block.
		f.runStates[f.chillerBDevID] = "STOPPED"
		f.operatingExceptions = []commandOperatingException{{
			Kind: "MAINTENANCE_WINDOW", StartDate: "2026-11-16", EndDate: "2026-11-16", Status: "APPROVED",
		}}
		recMaint := f.submit(f.chillerAAssetID, f.chillerAStopPointID, "last-chiller-maint-exception")
		if recMaint.Code != http.StatusAccepted {
			t.Fatalf("expected 202 under approved MAINTENANCE_WINDOW, code=%d body=%s", recMaint.Code, recMaint.Body.String())
		}
	})

	t.Run("plant_safe_shutdown_bypasses_cooldowns_and_last_chiller_guard", func(t *testing.T) {
		f := newPlantGuardTestFixture(t, base)
		f.runStates[f.chillerADevID] = "RUNNING"
		f.operatingSchedule = &commandOperatingSchedule{
			Timezone: "Asia/Shanghai",
			Slots:    []commandOperatingScheduleSlot{{DayOfWeek: 1, StartMinute: 480, EndMinute: 1260}},
		}
		// Only 5m since chiller-a started AND chiller-a is the sole running chiller during active cooling slot.
		f.listedCommands = []commandView{
			makeTestCommandHistoryEntry("019a9000-aaaa-7000-8000-000000000020", f.tenantID, f.siteID, f.chillerADevID, f.chillerAStartPointID, commandmodel.CapabilityStart, commandmodel.IntentSucceeded, f.now.Add(-5*time.Minute)),
		}

		// Direct single-asset STOP is blocked by cooldown...
		recDirect := f.submit(f.chillerAAssetID, f.chillerAStopPointID, "direct-stop-blocked-5m")
		if recDirect.Code != http.StatusConflict {
			t.Fatalf("expected direct STOP blocked at 5m, got %d", recDirect.Code)
		}

		// ...but PLANT_SAFE_SHUTDOWN via /control-shutdown MUST succeed and dispatch STOP for chiller-a!
		shutdownReq := httptest.NewRequest(http.MethodPost, "/api/v1/sites/"+f.siteID+"/control-shutdown", strings.NewReader(`{"mode":"PLANT_SAFE_SHUTDOWN","reason":"应急全站安全停机","verificationCode":"123456"}`))
		shutdownReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: f.sessionID})
		shutdownReq.Header.Set("Origin", "https://web.example.test")
		shutdownReq.Header.Set("X-CSRF-Token", commandTestCSRFValue())
		shutdownReq.Header.Set("Content-Type", "application/json")
		shutdownRec := httptest.NewRecorder()
		f.handler.ServeHTTP(shutdownRec, shutdownReq)
		if shutdownRec.Code != http.StatusOK {
			t.Fatalf("PLANT_SAFE_SHUTDOWN must bypass cooldowns/last-chiller guard, status=%d body=%s", shutdownRec.Code, shutdownRec.Body.String())
		}
		var dispatchedPoints []string
		for _, d := range f.dispatched {
			dispatchedPoints = append(dispatchedPoints, d.PointID)
		}
		if !slices.Contains(dispatchedPoints, f.chillerAStopPointID) {
			t.Fatalf("expected PLANT_SAFE_SHUTDOWN to dispatch chiller-a STOP, got %v", dispatchedPoints)
		}
	})
}
