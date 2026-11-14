package gateway

import (
	"context"
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
