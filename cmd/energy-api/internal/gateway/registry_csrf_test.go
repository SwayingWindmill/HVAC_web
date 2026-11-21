package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/cmd/energy-api/internal/platformapi"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/libs/sessionstore"
)

type registryWriteCase struct {
	name           string
	method         string
	path           string
	template       string
	expectedStatus int
}

type registryCSRFFixture struct {
	*commandGatewayFixture
	miniProgramSessionID string
	spaceID              string
	sensorID             string
	coreCalls            atomic.Int32
}

func newRegistryCSRFFixture(t *testing.T) *registryCSRFFixture {
	t.Helper()
	base := newCommandGatewayFixture(t)
	snapshotRaw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "contracts", "ownership", "route-ownership.v1.json"))
	if err != nil {
		t.Fatalf("read route-ownership.v1.json: %v", err)
	}
	snapshot, err := ownershipregistry.Parse(snapshotRaw)
	if err != nil {
		t.Fatalf("parse route-ownership.v1.json: %v", err)
	}
	now := base.now
	base.handler.routeManager = ownershipregistry.NewManager(snapshot, ownershipregistry.NewMemoryAuditSink(), func() time.Time { return now })

	fix := &registryCSRFFixture{
		commandGatewayFixture: base,
		miniProgramSessionID:  "registry-miniprogram-session",
		spaceID:               "018f3e00-2100-7000-8000-000000000001",
		sensorID:              "018f3e00-5100-7000-8000-000000000001",
	}

	miniCSRFCiphertext, err := base.handler.identity.encryptBytes([]byte("registry-miniprogram-csrf"))
	if err != nil {
		t.Fatalf("encrypt mini program csrf token: %v", err)
	}
	if _, err := base.store.CreateSession(context.Background(), sessionstore.Session{
		Client:              sessionstore.ClientMiniProgram,
		ID:                  fix.miniProgramSessionID,
		Principal:           identitycontext.UserPrincipal{Subject: "mini-admin", Issuer: "https://issuer.example.test", Roles: []string{"admin"}},
		TenantID:            base.tenantID,
		CSRFTokenCiphertext: miniCSRFCiphertext,
		ExpiresAt:           now.Add(time.Hour),
	}, sessionstore.MutationContext{
		Action:            "SESSION_CREATED",
		Result:            "SUCCEEDED",
		PolicyRevision:    "identity-policy-1",
		CorrelationID:     "registry-csrf-mini",
		TraceID:           strings.Repeat("a", 32),
		Traceparent:       "00-" + strings.Repeat("a", 32) + "-" + strings.Repeat("b", 16) + "-01",
		ExecutingService:  "platform-gateway",
		ExecutingSPIFFEID: "spiffe://hvac.local/platform-gateway",
		OccurredAt:        now,
	}); err != nil {
		t.Fatalf("create mini program session: %v", err)
	}

	base.handler.identity.config.IAMHTTPClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		base.iamCalls.Add(1)
		parent, err := identitycontext.VerifyDelegation(&base.gatewaySigner.PublicKey, request.Header.Get("X-Delegation-Grant"))
		if err != nil {
			return jsonTestResponse(http.StatusUnauthorized, map[string]any{"code": "IAM_DELEGATION_INVALID"}), nil
		}
		var input registryauth.DecisionRequest
		if json.NewDecoder(request.Body).Decode(&input) != nil {
			return jsonTestResponse(http.StatusBadRequest, map[string]any{"code": "IAM_REQUEST_INVALID"}), nil
		}
		reason := registryauth.ReasonAllowSiteRole
		if input.Action.TenantScoped() {
			reason = registryauth.ReasonAllowTenantRole
		}
		claims := registryauth.GrantClaims{
			Issuer:         "spiffe://hvac.local/iam-service",
			Presenter:      "spiffe://hvac.local/platform-gateway",
			Audience:       "platform-core-service",
			PrincipalID:    base.principalID,
			SubjectIssuer:  parent.SubjectIssuer,
			Subject:        parent.Subject,
			TenantID:       base.tenantID,
			AllowedSiteIDs: []string{base.siteID},
			Actions:        []registryauth.Action{input.Action},
			PolicyRevision: "registry-write:1/iam:1",
			DecisionReason: reason,
			SessionID:      parent.SessionID,
			ParentTokenID:  parent.TokenID,
			IssuedAt:       now.Unix(),
			ExpiresAt:      now.Add(30 * time.Second).Unix(),
			TokenID:        randomURLToken(16),
		}
		decision := registryauth.Decision{
			Allowed:        true,
			PrincipalID:    base.principalID,
			SubjectIssuer:  parent.SubjectIssuer,
			Subject:        parent.Subject,
			TenantID:       base.tenantID,
			AllowedSiteIDs: []string{base.siteID},
			Actions:        []registryauth.Action{input.Action},
			PolicyRevision: "registry-write:1/iam:1",
			ReasonCode:     reason,
			DecidedAt:      now.Format(time.RFC3339Nano),
		}
		return telemetryJSONResponse(http.StatusOK, registryauth.DecisionResponse{
			Decision:        decision,
			DelegationGrant: commandUnsignedRegistryGrant(claims),
		}), nil
	})}

	base.handler.registry = newRegistryController(&RegistryConfig{
		CoreBaseURL: "https://registry.example.test",
		CoreHTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			fix.coreCalls.Add(1)
			status := http.StatusOK
			if request.Method == http.MethodPost && !strings.HasSuffix(request.URL.Path, "/imports/dry-run") {
				status = http.StatusCreated
			}
			return telemetryJSONResponse(status, map[string]any{"id": base.siteID, "revision": 1}), nil
		})},
	})

	return fix
}

func (fix *registryCSRFFixture) writeCases() []registryWriteCase {
	sitePrefix := "/api/v1/sites/" + fix.siteID
	return []registryWriteCase{
		{name: "createSite", method: http.MethodPost, path: "/api/v1/sites", template: "/api/v1/sites", expectedStatus: http.StatusCreated},
		{name: "updateSite", method: http.MethodPatch, path: sitePrefix, template: "/api/v1/sites/{siteId}", expectedStatus: http.StatusOK},
		{name: "createSiteSpace", method: http.MethodPost, path: sitePrefix + "/spaces", template: "/api/v1/sites/{siteId}/spaces", expectedStatus: http.StatusCreated},
		{name: "updateSiteSpace", method: http.MethodPatch, path: sitePrefix + "/spaces/" + fix.spaceID, template: "/api/v1/sites/{siteId}/spaces/{spaceId}", expectedStatus: http.StatusOK},
		{name: "createSiteAsset", method: http.MethodPost, path: sitePrefix + "/assets", template: "/api/v1/sites/{siteId}/assets", expectedStatus: http.StatusCreated},
		{name: "updateSiteAsset", method: http.MethodPatch, path: sitePrefix + "/assets/" + fix.assetID, template: "/api/v1/sites/{siteId}/assets/{assetId}", expectedStatus: http.StatusOK},
		{name: "createSiteDevice", method: http.MethodPost, path: sitePrefix + "/devices", template: "/api/v1/sites/{siteId}/devices", expectedStatus: http.StatusCreated},
		{name: "updateSiteDevice", method: http.MethodPatch, path: sitePrefix + "/devices/" + fix.deviceID, template: "/api/v1/sites/{siteId}/devices/{deviceId}", expectedStatus: http.StatusOK},
		{name: "replaceDeviceHardware", method: http.MethodPost, path: sitePrefix + "/devices/" + fix.deviceID + "/replace-hardware", template: "/api/v1/sites/{siteId}/devices/{deviceId}/replace-hardware", expectedStatus: http.StatusCreated},
		{name: "createSiteSensor", method: http.MethodPost, path: sitePrefix + "/sensors", template: "/api/v1/sites/{siteId}/sensors", expectedStatus: http.StatusCreated},
		{name: "updateSiteSensor", method: http.MethodPatch, path: sitePrefix + "/sensors/" + fix.sensorID, template: "/api/v1/sites/{siteId}/sensors/{sensorId}", expectedStatus: http.StatusOK},
		{name: "createSitePoint", method: http.MethodPost, path: sitePrefix + "/points", template: "/api/v1/sites/{siteId}/points", expectedStatus: http.StatusCreated},
		{name: "updateSitePoint", method: http.MethodPatch, path: sitePrefix + "/points/" + registryTestPointID, template: "/api/v1/sites/{siteId}/points/{pointId}", expectedStatus: http.StatusOK},
		{name: "rebindRegistryResource", method: http.MethodPost, path: sitePrefix + "/bindings/rebind", template: "/api/v1/sites/{siteId}/bindings/rebind", expectedStatus: http.StatusCreated},
		{name: "releaseRegistryTemplateRevision", method: http.MethodPost, path: "/api/v1/templates/revisions", template: "/api/v1/templates/revisions", expectedStatus: http.StatusCreated},
		{name: "assignRegistryTemplateRevision", method: http.MethodPost, path: "/api/v1/templates/assignments", template: "/api/v1/templates/assignments", expectedStatus: http.StatusCreated},
		{name: "planRegistryImport", method: http.MethodPost, path: sitePrefix + "/imports/dry-run", template: "/api/v1/sites/{siteId}/imports/dry-run", expectedStatus: http.StatusOK},
		{name: "commitRegistryImport", method: http.MethodPost, path: sitePrefix + "/imports/commit", template: "/api/v1/sites/{siteId}/imports/commit", expectedStatus: http.StatusCreated},
		{name: "beginRegistryRetirement", method: http.MethodPost, path: sitePrefix + "/retire", template: "/api/v1/sites/{siteId}/retire", expectedStatus: http.StatusCreated},
	}
}

func assertRegistryProblem(t *testing.T, recorder *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()
	if recorder.Code != wantStatus {
		t.Fatalf("status=%d want=%d body=%s", recorder.Code, wantStatus, recorder.Body.String())
	}
	var problem platformapi.ProblemDetails
	if err := json.NewDecoder(recorder.Body).Decode(&problem); err != nil {
		t.Fatalf("decode problem: %v body=%s", err, recorder.Body.String())
	}
	if problem.Code != wantCode {
		t.Fatalf("problem code=%q want=%q body=%s", problem.Code, wantCode, recorder.Body.String())
	}
}

func TestRegistryWriteRoutesEnforceOriginAndCSRF(t *testing.T) {
	fix := newRegistryCSRFFixture(t)
	cases := fix.writeCases()
	if len(cases) != 19 {
		t.Fatalf("expected 19 Registry write cases, got %d", len(cases))
	}

	serve := func(method, path string, configure func(*http.Request)) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		configure(req)
		rec := httptest.NewRecorder()
		fix.handler.ServeHTTP(rec, req)
		return rec
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			beforeIAM := fix.iamCalls.Load()
			beforeCore := fix.coreCalls.Load()

			missingOrigin := serve(tc.method, tc.path, func(req *http.Request) {
				req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: fix.sessionID})
				req.Header.Set("X-CSRF-Token", commandTestCSRFValue())
			})
			assertRegistryProblem(t, missingOrigin, http.StatusForbidden, "ORIGIN_NOT_ALLOWED")

			wrongOrigin := serve(tc.method, tc.path, func(req *http.Request) {
				req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: fix.sessionID})
				req.Header.Set("Origin", "https://attacker.example.test")
				req.Header.Set("X-CSRF-Token", commandTestCSRFValue())
			})
			assertRegistryProblem(t, wrongOrigin, http.StatusForbidden, "ORIGIN_NOT_ALLOWED")

			missingCSRF := serve(tc.method, tc.path, func(req *http.Request) {
				req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: fix.sessionID})
				req.Header.Set("Origin", "https://web.example.test")
			})
			assertRegistryProblem(t, missingCSRF, http.StatusForbidden, "CSRF_TOKEN_INVALID")

			wrongCSRF := serve(tc.method, tc.path, func(req *http.Request) {
				req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: fix.sessionID})
				req.Header.Set("Origin", "https://web.example.test")
				req.Header.Set("X-CSRF-Token", "wrong-csrf-token")
			})
			assertRegistryProblem(t, wrongCSRF, http.StatusForbidden, "CSRF_TOKEN_INVALID")

			if fix.iamCalls.Load() != beforeIAM || fix.coreCalls.Load() != beforeCore {
				t.Fatalf("rejected browser requests reached upstream: iam=%d->%d core=%d->%d", beforeIAM, fix.iamCalls.Load(), beforeCore, fix.coreCalls.Load())
			}

			validBrowser := serve(tc.method, tc.path, func(req *http.Request) {
				fix.authenticate(req, true)
			})
			if validBrowser.Code != tc.expectedStatus {
				t.Fatalf("valid browser status=%d want=%d body=%s", validBrowser.Code, tc.expectedStatus, validBrowser.Body.String())
			}

			validMiniProgram := serve(tc.method, tc.path, func(req *http.Request) {
				req.Header.Set("Authorization", "Bearer "+fix.miniProgramSessionID)
			})
			if validMiniProgram.Code != tc.expectedStatus {
				t.Fatalf("valid mini program status=%d want=%d body=%s", validMiniProgram.Code, tc.expectedStatus, validMiniProgram.Body.String())
			}

			if fix.iamCalls.Load() != beforeIAM+2 || fix.coreCalls.Load() != beforeCore+2 {
				t.Fatalf("expected 2 upstream calls for browser+miniprogram, got iam=%d core=%d", fix.iamCalls.Load()-beforeIAM, fix.coreCalls.Load()-beforeCore)
			}
		})
	}
}

func TestRegistryWriteRoutesInvariantNoCSRFBypass(t *testing.T) {
	fix := newRegistryCSRFFixture(t)
	snapshotRaw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "contracts", "ownership", "route-ownership.v1.json"))
	if err != nil {
		t.Fatalf("read route-ownership.v1.json: %v", err)
	}
	var doc struct {
		Routes []struct {
			Method string `json:"method"`
			Path   string `json:"path"`
			Owner  string `json:"owner"`
		} `json:"routes"`
	}
	if err := json.Unmarshal(snapshotRaw, &doc); err != nil {
		t.Fatalf("decode route-ownership.v1.json: %v", err)
	}

	replacer := strings.NewReplacer(
		"{siteId}", fix.siteID,
		"{spaceId}", fix.spaceID,
		"{assetId}", fix.assetID,
		"{deviceId}", fix.deviceID,
		"{sensorId}", fix.sensorID,
		"{pointId}", registryTestPointID,
		"{date}", "2026-10-01",
		"{versionId}", "018f3e00-9000-7000-8000-000000000001",
	)

	expectedByKey := make(map[string]registryWriteCase, 19)
	for _, tc := range fix.writeCases() {
		expectedByKey[tc.method+" "+tc.template] = tc
	}

	seenWriteKeys := map[string]string{}
	for _, entry := range doc.Routes {
		concretePath := replacer.Replace(entry.Path)
		matched, _, ok := matchPublicRegistryRoute(entry.Method, concretePath)
		if !ok {
			if entry.Owner == ownershipregistry.OwnerCore && entry.Method != http.MethodGet {
				_, isSchedule := matchPublicOperatingScheduleRoute(entry.Method, concretePath)
				_, isBilling := matchPublicElectricityBillingRoute(entry.Method, concretePath)
				_, isAccountingRecalc := matchPublicAccountingRecalcRoute(entry.Method, concretePath)
				_, isAccountingConfig := matchPublicAccountingConfigRoute(entry.Method, concretePath)
				_, isIntelligence := matchPublicIntelligenceRoute(entry.Method, concretePath)
				if !isSchedule && !isBilling && !isAccountingRecalc && !isAccountingConfig && !isIntelligence {
					t.Fatalf("unhandled non-GET platform-core-service route in ownership registry: %s %s", entry.Method, entry.Path)
				}
			}
			continue
		}
		if entry.Method == http.MethodGet {
			if matched.write {
				t.Fatalf("GET route unexpectedly marked write=true: %s %s", entry.Method, entry.Path)
			}
			continue
		}
		if !matched.write {
			t.Fatalf("non-GET registry route %s %s must set write=true", entry.Method, entry.Path)
		}
		seenWriteKeys[entry.Method+" "+matched.template] = concretePath

		beforeIAM := fix.iamCalls.Load()
		beforeCore := fix.coreCalls.Load()
		req := httptest.NewRequest(entry.Method, concretePath, strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		fix.authenticate(req, false)
		rec := httptest.NewRecorder()
		fix.handler.ServeHTTP(rec, req)
		assertRegistryProblem(t, rec, http.StatusForbidden, "ORIGIN_NOT_ALLOWED")
		if fix.iamCalls.Load() != beforeIAM || fix.coreCalls.Load() != beforeCore {
			t.Fatalf("bypass on %s %s reached upstream", entry.Method, entry.Path)
		}
	}

	if len(seenWriteKeys) != len(expectedByKey) {
		t.Fatalf("discovered %d write registry routes, expected %d", len(seenWriteKeys), len(expectedByKey))
	}
	for key := range expectedByKey {
		if _, ok := seenWriteKeys[key]; !ok {
			t.Fatalf("expected write route %s missing from route-ownership + matchPublicRegistryRoute", key)
		}
	}
}
