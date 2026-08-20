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

	"github.com/quanlaihe/hvac-web/libs/analyticsmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/libs/sessionstore"
)

type fakeOperationsRateLimiter struct {
	max   int32
	count atomic.Int32
	fail  atomic.Bool
}

func (limiter *fakeOperationsRateLimiter) Allow(context.Context, string) (bool, error) {
	if limiter.fail.Load() {
		return false, errors.New("limit backend unavailable")
	}
	return limiter.count.Add(1) <= limiter.max, nil
}

type operationsGatewayFixture struct {
	handler          http.Handler
	tenantID         string
	siteID           string
	sessionID        string
	gatewaySigner    *ecdsa.PrivateKey
	limiter          *fakeOperationsRateLimiter
	iamCalls         atomic.Int32
	operationsCalls  atomic.Int32
	denySite         atomic.Bool
	invalidStream    atomic.Bool
	invalidIdentity  atomic.Bool
	rejectUnsafe     atomic.Bool
	lastUpstream     atomic.Pointer[http.Request]
	lastUpstreamBody atomic.Pointer[string]
}

func newOperationsGatewayFixture(t *testing.T, rateLimit int) *operationsGatewayFixture {
	t.Helper()
	now := time.Date(2026, 7, 31, 8, 0, 0, 0, time.UTC)
	fixture := &operationsGatewayFixture{
		tenantID:      "018f3d00-0000-7000-8000-000000000001",
		siteID:        "018f3e00-2000-7000-8000-000000000001",
		gatewaySigner: commandTestSigner(t),
		limiter:       &fakeOperationsRateLimiter{max: int32(rateLimit)},
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
				(input.Action != registryauth.ActionSiteRead && input.Action != registryauth.ActionAssetList) {
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
				reason = analyticsmodel.AuthorizationReasonAllowSiteBinding
			}
			return telemetryJSONResponse(http.StatusOK, analyticsmodel.AuthorizationDecisionResponse{
				Decision: analyticsmodel.AuthorizationDecision{
					Allowed: allowed, PrincipalID: "018f3e00-5000-7000-8000-000000000001",
					SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject,
					TenantID: fixture.tenantID, SiteID: input.SiteID, Action: input.Action,
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
		if claims.Audience != "operations-agent-service" || claims.TenantID != fixture.tenantID ||
			!operationsContains(claims.Actions, "operations:investigate") || !operationsContains(claims.Scopes, "site:"+fixture.siteID) ||
			claims.IssuedAt != now.Unix() {
			t.Fatalf("unexpected Operations claims: %+v", claims)
		}
		if request.Header.Get("X-Tenant-ID") != fixture.tenantID || request.Header.Get("X-Route-Policy-Revision") != "0" {
			t.Fatal("missing authoritative Operations headers")
		}
		if fixture.rejectUnsafe.Load() && strings.HasSuffix(request.URL.Path, ":advance") {
			body := `{"type":"urn:hvac:operations-agent:untrusted_content_rejected","title":"Untrusted content rejected","status":422,"code":"UNTRUSTED_CONTENT_REJECTED","detail":"Runtime output attempted to alter the bounded Operations Agent control policy."}`
			return &http.Response{
				StatusCode: http.StatusUnprocessableEntity,
				Header:     http.Header{"Content-Type": []string{"application/problem+json"}},
				Body:       io.NopCloser(strings.NewReader(body)),
			}, nil
		}
		if strings.HasSuffix(request.URL.Path, ":submit-operator-input") {
			raw, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatalf("failed to read Operator Input body: %v", err)
			}
			body := string(raw)
			fixture.lastUpstreamBody.Store(&body)
			if request.Header.Get("Idempotency-Key") == "" {
				t.Fatal("Operator Input Idempotency-Key was not forwarded")
			}
		}
		if strings.HasSuffix(request.URL.Path, "/events") {
			if fixture.invalidStream.Load() {
				unsafe := "id: 9:0\nevent: RUN_STARTED\ndata: {\"type\":\"RUN_STARTED\",\"threadId\":\"investigation-001\",\"runId\":\"run-001\",\"checkpoint\":{}}\n\n"
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(unsafe))}, nil
			}
			investigation := `{"schemaVersion":1,"id":"investigation-001","scope":{"tenantId":"` + fixture.tenantID + `","siteId":"` + fixture.siteID + `","assetId":null,"deviceId":null},"status":"COMPLETED","revision":9,"createdAt":1,"activeRun":null,"outcome":"SUPPORTED_SITE_FINDING","resourceBudget":null,"evidence":[],"analysisReferences":[],"findings":[],"operatorInputRequest":null,"acceptedOperatorInputs":[]}`
			plan := `{"schemaVersion":1,"id":"site-night-energy-investigation","label":"Site night-energy investigation","completedSteps":4,"totalSteps":4,"progressPercent":100,"steps":[{"id":"READ_SITE_CONTEXT","label":"Read authoritative Site context","status":"COMPLETED"},{"id":"READ_ENERGY_SERIES","label":"Read authoritative night-energy periods","status":"COMPLETED"},{"id":"ANALYZE","label":"Run deterministic night-energy analysis","status":"COMPLETED"},{"id":"COMMIT_RESULT","label":"Commit Evidence, Analysis and Finding","status":"COMPLETED"}]}`
			stream := "id: 9:0\nevent: RUN_STARTED\ndata: {\"type\":\"RUN_STARTED\",\"threadId\":\"investigation-001\",\"runId\":\"run-001\"}\n\n" +
				"id: 9:1\nevent: STATE_SNAPSHOT\ndata: {\"type\":\"STATE_SNAPSHOT\",\"snapshot\":{\"schemaVersion\":\"operations-investigation-ui/v1\",\"investigation\":" + investigation + ",\"plan\":" + plan + ",\"toolActivities\":[]}}\n\n" +
				"id: 9:2\nevent: RUN_FINISHED\ndata: {\"type\":\"RUN_FINISHED\",\"threadId\":\"investigation-001\",\"runId\":\"run-001\",\"outcome\":{\"type\":\"success\"}}\n\n"
			if fixture.invalidIdentity.Load() {
				stream = strings.Replace(stream, "id: 9:1", "id: 9:2", 1)
			}
			recoveryHeaders := http.Header{
				"Content-Type":                   []string{"text/event-stream; charset=utf-8"},
				"X-Operations-Recovery-Mode":     []string{"FULL_SNAPSHOT"},
				"X-Operations-Recovery-Reason":   []string{"INITIAL"},
				"X-Operations-Snapshot-Position": []string{"9:1"},
				"X-Operations-Latest-Position":   []string{"9:2"},
			}
			requestedPosition := strings.TrimSpace(request.Header.Get("Last-Event-ID"))
			if requestedPosition == "9:2" {
				recoveryHeaders.Set("X-Operations-Recovery-Mode", "RESUME")
				recoveryHeaders.Set("X-Operations-Recovery-Reason", "VALID")
				recoveryHeaders.Set("X-Operations-Replay-From", requestedPosition)
			} else if requestedPosition != "" {
				recoveryHeaders.Set("X-Operations-Recovery-Reason", "UNKNOWN")
			}
			return &http.Response{StatusCode: http.StatusOK, Header: recoveryHeaders, Body: io.NopCloser(strings.NewReader(stream))}, nil
		}
		body := `{"schemaVersion":1,"id":"investigation-001","scope":{"tenantId":"` + fixture.tenantID + `","siteId":"` + fixture.siteID + `","assetId":null,"deviceId":null},"status":"COMPLETED","revision":9,"createdAt":1,"activeRun":null,"outcome":"SUPPORTED_SITE_FINDING","resourceBudget":null,"evidence":[],"analysisReferences":[],"findings":[],"operatorInputRequest":null,"acceptedOperatorInputs":[],"toolReceipts":[]}`
		status := http.StatusOK
		if strings.HasSuffix(request.URL.Path, "/operations/investigations") {
			if request.Method == http.MethodGet {
				body = `{"schemaVersion":1,"investigations":[{"schemaVersion":1,"id":"investigation-001","scope":{"tenantId":"` + fixture.tenantID + `","siteId":"` + fixture.siteID + `","assetId":null,"deviceId":null},"status":"COMPLETED","revision":9,"createdAt":1,"outcome":"SUPPORTED_SITE_FINDING","resourceBudget":null,"evidenceCount":2,"analysisReferenceCount":1,"findingCount":1,"toolReceiptCount":4,"acceptedOperatorInputCount":0}]}`
			} else {
				status = http.StatusCreated
			}
		} else if strings.HasSuffix(request.URL.Path, ":submit-operator-input") {
			body = `{"outcome":"COMMITTED","investigation":{"schemaVersion":1,"id":"investigation-001","scope":{"tenantId":"` + fixture.tenantID + `","siteId":"` + fixture.siteID + `","assetId":null,"deviceId":null},"status":"RUNNING","revision":10,"createdAt":1,"activeRun":{"id":"run-001","status":"ACTIVE","startedAt":1},"outcome":null,"resourceBudget":null,"evidence":[],"analysisReferences":[],"findings":[],"operatorInputRequest":null,"acceptedOperatorInputs":[{"schemaVersion":1,"recordType":"OPERATOR_INPUT_ACCEPTED","id":"operator-input-record-001","investigationId":"investigation-001","recordedAt":2,"requestId":"operator-input-request-001","runId":"run-001","idempotencyKey":"operator-input-idempotency-001","inputKind":"SITE_NIGHT_ENERGY_SCOPE_CONFIRMATION","inputDigest":"sha256:0000000000000000000000000000000000000000000000000000000000000000","scope":{"tenantId":"` + fixture.tenantID + `","siteId":"` + fixture.siteID + `","assetId":null,"deviceId":null},"values":{"analysisScope":"SITE_ONLY","operatorNote":"Proceed with Site-only authority."},"provenance":{"actorType":"OPERATOR","source":"PLATFORM_GATEWAY","authorizationDecisionId":"allow-site","policyRevision":"identity-policy-1","submittedAt":2}}],"toolReceipts":[]}}`
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

func TestOperationsGatewayRejectsCallerSuppliedRunBudgetWidening(t *testing.T) {
	fixture := newOperationsGatewayFixture(t, 30)
	path := "/api/v1/sites/" + fixture.siteID + "/operations/investigations/investigation-001:advance"
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader("{\"resourceBudget\":{\"limits\":{\"toolRequests\":999999}}}"))
	fixture.authenticate(request, true)
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("caller-supplied budget widening was not rejected: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if fixture.iamCalls.Load() != 0 || fixture.operationsCalls.Load() != 0 {
		t.Fatalf("caller-supplied budget widening reached upstreams: IAM=%d Operations=%d", fixture.iamCalls.Load(), fixture.operationsCalls.Load())
	}
}

func TestOperationsGatewayPreservesTypedSafetyRejection(t *testing.T) {
	fixture := newOperationsGatewayFixture(t, 30)
	fixture.rejectUnsafe.Store(true)
	path := "/api/v1/sites/" + fixture.siteID + "/operations/investigations/investigation-001:advance"
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
	fixture.authenticate(request, true)
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(recorder.Body.String(), `"code":"UNTRUSTED_CONTENT_REJECTED"`) {
		t.Fatalf("typed safety rejection was not preserved: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	for _, forbidden := range []string{"rawPrompt", "instructions", "ownerPayload", "modelOutput"} {
		if strings.Contains(recorder.Body.String(), forbidden) {
			t.Fatalf("safety response disclosed forbidden content field %q", forbidden)
		}
	}
	if fixture.iamCalls.Load() != 1 || fixture.operationsCalls.Load() != 1 {
		t.Fatalf("unexpected safety rejection upstream counts IAM=%d Operations=%d", fixture.iamCalls.Load(), fixture.operationsCalls.Load())
	}
}

func TestOperationsGatewaySubmitsBoundedOperatorInputWithIdempotency(t *testing.T) {
	fixture := newOperationsGatewayFixture(t, 30)
	path := "/api/v1/sites/" + fixture.siteID + "/operations/investigations/investigation-001:submit-operator-input"
	body := `{"schemaVersion":1,"requestId":"operator-input-request-001","expectedRevision":9,"values":{"analysisScope":"SITE_ONLY","operatorNote":"Proceed with Site-only authority."}}`
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	fixture.authenticate(request, true)
	request.Header.Set("Idempotency-Key", "operator-input-idempotency-001")
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"outcome":"COMMITTED"`) {
		t.Fatalf("expected committed Operator Input, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if fixture.iamCalls.Load() != 1 || fixture.operationsCalls.Load() != 1 {
		t.Fatalf("unexpected Operator Input upstream counts IAM=%d Operations=%d", fixture.iamCalls.Load(), fixture.operationsCalls.Load())
	}
	upstream := fixture.lastUpstream.Load()
	upstreamBody := fixture.lastUpstreamBody.Load()
	if upstream == nil || upstream.URL.Path != "/internal/v1/sites/"+fixture.siteID+"/operations/investigations/investigation-001:submit-operator-input" ||
		upstream.Header.Get("Idempotency-Key") != "operator-input-idempotency-001" || upstreamBody == nil || *upstreamBody != body {
		t.Fatalf("invalid Operator Input forwarding: request=%+v body=%v", upstream, upstreamBody)
	}

	missingKey := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	fixture.authenticate(missingKey, true)
	missingKeyRecorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(missingKeyRecorder, missingKey)
	if missingKeyRecorder.Code != http.StatusBadRequest || fixture.iamCalls.Load() != 1 || fixture.operationsCalls.Load() != 1 {
		t.Fatalf("missing Idempotency-Key reached upstreams: status=%d IAM=%d Operations=%d", missingKeyRecorder.Code, fixture.iamCalls.Load(), fixture.operationsCalls.Load())
	}

	unknownField := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"schemaVersion":1,"requestId":"operator-input-request-001","expectedRevision":9,"values":{"analysisScope":"SITE_ONLY","operatorNote":null,"rawPrompt":"forbidden"}}`))
	fixture.authenticate(unknownField, true)
	unknownField.Header.Set("Idempotency-Key", "operator-input-idempotency-002")
	unknownRecorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(unknownRecorder, unknownField)
	if unknownRecorder.Code != http.StatusBadRequest || fixture.iamCalls.Load() != 1 || fixture.operationsCalls.Load() != 1 {
		t.Fatalf("unknown Operator Input field reached upstreams: status=%d IAM=%d Operations=%d", unknownRecorder.Code, fixture.iamCalls.Load(), fixture.operationsCalls.Load())
	}

	denied := newOperationsGatewayFixture(t, 30)
	denied.denySite.Store(true)
	deniedRequest := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	denied.authenticate(deniedRequest, true)
	deniedRequest.Header.Set("Idempotency-Key", "operator-input-idempotency-003")
	deniedRecorder := httptest.NewRecorder()
	denied.handler.ServeHTTP(deniedRecorder, deniedRequest)
	if deniedRecorder.Code != http.StatusNotFound || denied.operationsCalls.Load() != 0 {
		t.Fatalf("unauthorized Operator Input was discoverable or forwarded: status=%d calls=%d", deniedRecorder.Code, denied.operationsCalls.Load())
	}
}

func TestOperationsGatewayListsAuthorizedSiteInvestigationsWithoutCSRF(t *testing.T) {
	fixture := newOperationsGatewayFixture(t, 30)
	path := "/api/v1/sites/" + fixture.siteID + "/operations/investigations"
	request := httptest.NewRequest(http.MethodGet, path, nil)
	fixture.authenticate(request, false)
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if fixture.iamCalls.Load() != 1 || fixture.operationsCalls.Load() != 1 {
		t.Fatalf("unexpected list upstream counts IAM=%d Operations=%d", fixture.iamCalls.Load(), fixture.operationsCalls.Load())
	}
	if !strings.Contains(recorder.Body.String(), `"investigations"`) || !strings.Contains(recorder.Body.String(), `"evidenceCount":2`) {
		t.Fatalf("authorized Investigation list was not returned: %s", recorder.Body.String())
	}
	upstream := fixture.lastUpstream.Load()
	if upstream == nil || upstream.Method != http.MethodGet || upstream.URL.Path != "/internal/v1/sites/"+fixture.siteID+"/operations/investigations" {
		t.Fatalf("invalid Operations list forwarding: %+v", upstream)
	}
}

func TestOperationsGatewayStreamsValidatedCommittedEvents(t *testing.T) {
	fixture := newOperationsGatewayFixture(t, 30)
	path := "/api/v1/sites/" + fixture.siteID + "/operations/investigations/investigation-001/events"
	request := httptest.NewRequest(http.MethodGet, path, nil)
	fixture.authenticate(request, false)
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.HasPrefix(recorder.Header().Get("Content-Type"), "text/event-stream") ||
		!strings.Contains(recorder.Header().Get("Cache-Control"), "no-store") ||
		recorder.Header().Get("X-Accel-Buffering") != "no" ||
		recorder.Header().Get("X-Operations-Recovery-Mode") != "FULL_SNAPSHOT" ||
		recorder.Header().Get("X-Operations-Recovery-Reason") != "INITIAL" ||
		recorder.Header().Get("X-Operations-Snapshot-Position") != "9:1" ||
		recorder.Header().Get("X-Operations-Latest-Position") != "9:2" {
		t.Fatalf("invalid stream headers: %v", recorder.Header())
	}
	body := recorder.Body.String()
	for _, expected := range []string{"event: RUN_STARTED", "event: STATE_SNAPSHOT", "event: RUN_FINISHED"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("missing event %q: %s", expected, body)
		}
	}
	for _, forbidden := range []string{
		"checkpoint", "providerMessage", "points", "metadata", "delegationGrant",
		"rawPrompt", "instructions", "ownerPayload", "modelOutput", "allowedReadTools",
		"effectPolicy", "scopePolicy", "untrustedContentPolicy",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("unsafe stream field %q crossed the Gateway", forbidden)
		}
	}
	upstream := fixture.lastUpstream.Load()
	if upstream == nil || upstream.URL.Path != "/internal/v1/sites/"+fixture.siteID+"/operations/investigations/investigation-001/events" ||
		!strings.Contains(upstream.Header.Get("Accept"), "text/event-stream") {
		t.Fatalf("invalid Operations stream forwarding: %+v", upstream)
	}

	firstIAMCalls := fixture.iamCalls.Load()
	reconnect := httptest.NewRequest(http.MethodGet, path, nil)
	reconnect.Header.Set("Last-Event-ID", "9:2")
	fixture.authenticate(reconnect, false)
	reconnectRecorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(reconnectRecorder, reconnect)
	if reconnectRecorder.Code != http.StatusOK ||
		reconnectRecorder.Header().Get("X-Operations-Recovery-Mode") != "RESUME" ||
		reconnectRecorder.Header().Get("X-Operations-Replay-From") != "9:2" {
		t.Fatalf("invalid reconnect response: status=%d headers=%v body=%s", reconnectRecorder.Code, reconnectRecorder.Header(), reconnectRecorder.Body.String())
	}
	if fixture.operationsCalls.Load() != 2 || fixture.iamCalls.Load() <= firstIAMCalls {
		t.Fatalf("reconnect did not reauthorize: IAM before=%d after=%d Operations=%d", firstIAMCalls, fixture.iamCalls.Load(), fixture.operationsCalls.Load())
	}
	upstream = fixture.lastUpstream.Load()
	if upstream == nil || upstream.Header.Get("Last-Event-ID") != "9:2" {
		t.Fatalf("recovery position was not forwarded upstream: %+v", upstream)
	}
}

func TestOperationsGatewayPropagatesStreamCancellation(t *testing.T) {
	fixture := newOperationsGatewayFixture(t, 30)
	controller := fixture.handler.(*handler)
	started := make(chan struct{})
	cancelled := make(chan error, 1)
	controller.operations.httpClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		close(started)
		<-request.Context().Done()
		cancelled <- request.Context().Err()
		return nil, request.Context().Err()
	})}

	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/sites/"+fixture.siteID+"/operations/investigations/investigation-001/events",
		nil,
	).WithContext(ctx)
	fixture.authenticate(request, false)
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		fixture.handler.ServeHTTP(recorder, request)
		close(done)
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("Operations stream request did not reach the upstream")
	}
	cancel()

	select {
	case err := <-cancelled:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("upstream request was not cancelled: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("client cancellation did not reach the Operations Agent request")
	}
	select {
	case <-done:
		if recorder.Code != http.StatusBadGateway {
			t.Fatalf("expected cancellation to terminate the Gateway request, got %d", recorder.Code)
		}
	case <-time.After(time.Second):
		t.Fatal("Gateway handler did not return after client cancellation")
	}
}

func TestOperationsGatewayRejectsUnsafeOrUnauthorizedEventStreams(t *testing.T) {
	unsafeFixture := newOperationsGatewayFixture(t, 30)
	unsafeFixture.invalidStream.Store(true)
	path := "/api/v1/sites/" + unsafeFixture.siteID + "/operations/investigations/investigation-001/events"
	unsafeRequest := httptest.NewRequest(http.MethodGet, path, nil)
	unsafeFixture.authenticate(unsafeRequest, false)
	unsafeRecorder := httptest.NewRecorder()
	unsafeFixture.handler.ServeHTTP(unsafeRecorder, unsafeRequest)
	if unsafeRecorder.Code != http.StatusBadGateway || strings.Contains(unsafeRecorder.Body.String(), "checkpoint") {
		t.Fatalf("unsafe event stream was not rejected: status=%d body=%s", unsafeRecorder.Code, unsafeRecorder.Body.String())
	}

	identityFixture := newOperationsGatewayFixture(t, 30)
	identityFixture.invalidIdentity.Store(true)
	identityRequest := httptest.NewRequest(http.MethodGet, "/api/v1/sites/"+identityFixture.siteID+"/operations/investigations/investigation-001/events", nil)
	identityFixture.authenticate(identityRequest, false)
	identityRecorder := httptest.NewRecorder()
	identityFixture.handler.ServeHTTP(identityRecorder, identityRequest)
	if identityRecorder.Code != http.StatusBadGateway {
		t.Fatalf("out-of-order event identities were accepted: status=%d body=%s", identityRecorder.Code, identityRecorder.Body.String())
	}

	deniedFixture := newOperationsGatewayFixture(t, 30)
	deniedFixture.denySite.Store(true)
	deniedRequest := httptest.NewRequest(http.MethodGet, "/api/v1/sites/"+deniedFixture.siteID+"/operations/investigations/investigation-001/events", nil)
	deniedFixture.authenticate(deniedRequest, false)
	deniedRecorder := httptest.NewRecorder()
	deniedFixture.handler.ServeHTTP(deniedRecorder, deniedRequest)
	if deniedRecorder.Code != http.StatusNotFound || deniedFixture.operationsCalls.Load() != 0 {
		t.Fatalf("unauthorized stream was discoverable or forwarded: status=%d calls=%d", deniedRecorder.Code, deniedFixture.operationsCalls.Load())
	}
}

func TestOperationsGatewayTypedSnapshotValidatorRejectsForgedAuthority(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	tenantID := "tenant-001"
	siteID := "site-001"
	investigationID := "investigation-001"
	scope := map[string]any{
		"tenantId": tenantID,
		"siteId":   siteID,
		"assetId":  nil,
		"deviceId": nil,
	}
	period := map[string]any{
		"localDate":       "2026-07-30",
		"from":            "2026-07-30T00:00:00Z",
		"to":              "2026-07-30T08:00:00Z",
		"expectedBuckets": 8,
	}
	snapshot := map[string]any{
		"schemaVersion":  1,
		"id":             investigationID,
		"scope":          scope,
		"status":         "COMPLETED",
		"revision":       9,
		"createdAt":      1,
		"activeRun":      nil,
		"outcome":        "UNABLE_TO_CONCLUDE",
		"resourceBudget": nil,
		"evidence": []any{map[string]any{
			"schemaVersion":           1,
			"recordType":              "EVIDENCE",
			"id":                      "evidence-001",
			"investigationId":         investigationID,
			"recordedAt":              2,
			"evidenceKind":            "SITE_ENERGY_SERIES_READINESS_ASSESSED",
			"classification":          "FACT",
			"statement":               "Site energy readiness was assessed.",
			"analysisReferenceDigest": nil,
			"sources": []any{map[string]any{
				"owner":            "telemetry-query-service",
				"scope":            scope,
				"requestId":        "energy-request-001",
				"registryRevision": nil,
				"datasetRevision":  "dataset-r17",
				"watermark": map[string]any{
					"data":      "2026-07-30T08:00:00Z",
					"aggregate": "2026-07-30T08:05:00Z",
				},
				"partial": false,
				"quality": map[string]any{
					"classification": "GOOD",
					"valid":          8,
					"suspect":        0,
					"invalid":        0,
				},
				"capturedAt":       2,
				"evaluatedAt":      3,
				"provenanceDigest": digest,
			}},
		}},
		"analysisReferences": []any{map[string]any{
			"schemaVersion":    1,
			"recordType":       "ANALYSIS_REFERENCE",
			"id":               "analysis-001",
			"investigationId":  investigationID,
			"recordedAt":       3,
			"analysisKind":     "SITE_NIGHT_ENERGY_COMPARISON",
			"authority":        "DETERMINISTIC_ALGORITHM",
			"algorithmVersion": "night-energy-v1",
			"policyVersion":    "quality-v1",
			"inputEvidenceIds": []any{"evidence-001"},
			"parameterDigest":  digest,
			"resultDigest":     digest,
			"executedAt":       3,
			"outcome":          "UNABLE_TO_CONCLUDE",
		}},
		"operatorInputRequest":   nil,
		"acceptedOperatorInputs": []any{},
		"findings": []any{map[string]any{
			"schemaVersion":        1,
			"recordType":           "FINDING",
			"id":                   "finding-001",
			"investigationId":      investigationID,
			"recordedAt":           4,
			"findingKind":          "UNABLE_TO_CONCLUDE",
			"classification":       "INFERENCE",
			"statement":            "Asset attribution is unsupported.",
			"evidenceIds":          []any{"evidence-001"},
			"analysisReferenceIds": []any{"analysis-001"},
			"conclusion": map[string]any{
				"status":     "UNABLE_TO_CONCLUDE",
				"scope":      "ASSET",
				"reasonCode": "ASSET_EVIDENCE_MISSING",
				"detail":     "Canonical Asset series are required.",
				"requiredNext": []any{map[string]any{
					"status":           "REQUIRED_NEXT",
					"kind":             "ASSET_ENERGY_PERIOD_COMPARISON",
					"owner":            "telemetry-query-service",
					"capability":       "analytics.energy.getAssetSeries",
					"tenantId":         tenantID,
					"siteId":           siteID,
					"assetIds":         []any{"asset-001"},
					"targetPeriod":     period,
					"baselinePeriod":   period,
					"requiredMetadata": []any{"DATASET_REVISION", "WATERMARK", "PARTIAL", "QUALITY", "CAPTURED_AT", "PAYLOAD_DIGEST"},
				}},
			},
		}},
		"toolReceipts": []any{map[string]any{
			"schemaVersion":   1,
			"recordType":      "TOOL_EXECUTION_RECEIPT",
			"id":              "receipt-001",
			"investigationId": investigationID,
			"recordedAt":      5,
			"logicalTool":     "analytics.getEnergySeries",
			"owner":           "telemetry-query-service",
			"requestId":       "energy-request-001",
			"attemptId":       "energy-attempt-001",
			"runId":           "run-001",
			"stepId":          "READ_ENERGY_SERIES",
			"startedAt":       4,
			"completedAt":     5,
			"resultCategory":  "SUCCEEDED",
			"metadata": map[string]any{
				"datasetRevision": "dataset-r17",
				"partial":         false,
			},
		}},
	}
	encode := func(value map[string]any) []byte {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	clone := func(value map[string]any) map[string]any {
		raw := encode(value)
		var result map[string]any
		if err := json.Unmarshal(raw, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	if err := validateOperationsSnapshot(encode(snapshot)); err != nil {
		t.Fatalf("valid typed Operations snapshot was rejected: %v", err)
	}

	exhausted := clone(snapshot)
	exhausted["resourceBudget"] = map[string]any{
		"schemaVersion":      1,
		"policyRevision":     "operations-agent-run-resource-policy/v1",
		"outcome":            "PARTIAL",
		"exhaustedDimension": "PAYLOAD_BYTES",
		"consumed":           1_025,
		"limit":              1_024,
	}
	if err := validateOperationsSnapshot(encode(exhausted)); err != nil {
		t.Fatalf("bounded Run resource exhaustion was rejected: %v", err)
	}

	leakedBudget := clone(exhausted)
	leakedBudget["resourceBudget"].(map[string]any)["usage"] = map[string]any{"payloadBytes": 1_025}
	if err := validateOperationsSnapshot(encode(leakedBudget)); err == nil {
		t.Fatal("internal Run resource usage crossed the Gateway")
	}

	nonExhaustedBudget := clone(exhausted)
	nonExhaustedBudget["resourceBudget"].(map[string]any)["consumed"] = 1_024
	if err := validateOperationsSnapshot(encode(nonExhaustedBudget)); err == nil {
		t.Fatal("non-exhausted Run resource outcome was accepted")
	}

	missingRevision := clone(snapshot)
	missingRevision["evidence"].([]any)[0].(map[string]any)["sources"].([]any)[0].(map[string]any)["datasetRevision"] = nil
	if err := validateOperationsSnapshot(encode(missingRevision)); err == nil {
		t.Fatal("telemetry Evidence without Dataset Revision was accepted")
	}

	crossScope := clone(snapshot)
	crossScope["evidence"].([]any)[0].(map[string]any)["sources"].([]any)[0].(map[string]any)["scope"].(map[string]any)["siteId"] = "site-other"
	if err := validateOperationsSnapshot(encode(crossScope)); err == nil {
		t.Fatal("cross-Site Evidence provenance was accepted")
	}

	danglingReference := clone(snapshot)
	danglingReference["analysisReferences"].([]any)[0].(map[string]any)["inputEvidenceIds"] = []any{"evidence-missing"}
	if err := validateOperationsSnapshot(encode(danglingReference)); err == nil {
		t.Fatal("dangling Analysis Evidence reference was accepted")
	}

	assetAuthority := clone(snapshot)
	finding := assetAuthority["findings"].([]any)[0].(map[string]any)
	finding["findingKind"] = "SITE_NIGHT_ENERGY_INCREASE"
	finding["conclusion"] = map[string]any{
		"status":   "SUPPORTED",
		"scope":    "ASSET",
		"tenantId": tenantID,
		"siteId":   siteID,
	}
	if err := validateOperationsSnapshot(encode(assetAuthority)); err == nil {
		t.Fatal("supported Asset root-cause authority was accepted")
	}

	ownerMismatch := clone(snapshot)
	requirement := ownerMismatch["findings"].([]any)[0].(map[string]any)["conclusion"].(map[string]any)["requiredNext"].([]any)[0].(map[string]any)
	requirement["owner"] = "registry"
	if err := validateOperationsSnapshot(encode(ownerMismatch)); err == nil {
		t.Fatal("required-next Owner and capability mismatch was accepted")
	}

	unsafeReceipt := clone(snapshot)
	unsafeReceipt["toolReceipts"].([]any)[0].(map[string]any)["metadata"] = map[string]any{
		"responsePayload": map[string]any{"raw": true},
	}
	if err := validateOperationsSnapshot(encode(unsafeReceipt)); err == nil {
		t.Fatal("nested sensitive Tool Receipt metadata was accepted")
	}
}

func TestOperationsGatewayRejectsRuntimeControlFieldsFromPublicProjections(t *testing.T) {
	for _, field := range []string{
		"rawPrompt",
		"instructions",
		"ownerPayload",
		"modelOutput",
		"allowedReadTools",
		"effectPolicy",
		"scopePolicy",
		"untrustedContentPolicy",
		"operationId",
		"acceptedOperations",
		"usage",
		"limits",
		"maximumQueryRangeMs",
	} {
		payload := map[string]any{field: "forbidden"}
		if err := inspectOperationsSnapshotPayload(payload); err == nil {
			t.Fatalf("snapshot accepted Runtime control field %q", field)
		}
		if err := inspectOperationsEventPayload(payload); err == nil {
			t.Fatalf("event accepted Runtime control field %q", field)
		}
	}
}

func TestParseOperationsEventIDRequiresCanonicalDecimalPositions(t *testing.T) {
	for _, candidate := range []string{"01:2", "1:02", "+1:2", "1:-2", "1:2:3", " 1:2"} {
		if _, _, err := parseOperationsEventID(candidate); err == nil {
			t.Fatalf("noncanonical Operations event position was accepted: %q", candidate)
		}
	}
	for _, candidate := range []string{"0:0", "1:2", "18446744073709551615:0"} {
		if _, _, err := parseOperationsEventID(candidate); err != nil {
			t.Fatalf("canonical Operations event position was rejected: %q: %v", candidate, err)
		}
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

func TestOperationsGatewayFailsClosedWhenLimitBackendUnavailable(t *testing.T) {
	fixture := newOperationsGatewayFixture(t, 30)
	fixture.limiter.fail.Store(true)
	path := "/api/v1/sites/" + fixture.siteID + "/operations/investigations/investigation-001"
	request := httptest.NewRequest(http.MethodGet, path, nil)
	fixture.authenticate(request, false)
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable || fixture.iamCalls.Load() != 0 || fixture.operationsCalls.Load() != 0 {
		t.Fatalf("limit backend failure did not fail closed: status=%d IAM=%d Operations=%d", recorder.Code, fixture.iamCalls.Load(), fixture.operationsCalls.Load())
	}
	if !strings.Contains(recorder.Body.String(), "OPERATIONS_LIMIT_UNAVAILABLE") {
		t.Fatalf("unexpected limit failure body: %s", recorder.Body.String())
	}
}

func TestOperationsToolAuthorizationIssuesExactOwnerGrants(t *testing.T) {
	fixture := newOperationsGatewayFixture(t, 30)
	serviceGrant := fixture.serviceDelegation(t)
	registryBody := `{"investigationId":"investigation-001","runId":"run-001","request":{"requestId":"registry-assets-001","tool":"registry.listSiteAssets","input":{"siteId":"` + fixture.siteID + `"}}}`
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
		claims.Audience != "telemetry-query-service" || claims.TenantID != fixture.tenantID ||
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

type failingOperationsTelemetryExporter struct{}

func (failingOperationsTelemetryExporter) Export(context.Context, []observability.SpanData) error {
	return errors.New("collector unavailable")
}

func (failingOperationsTelemetryExporter) Shutdown(context.Context) error { return nil }

func TestOperationsGatewayPropagatesCorrelatedRedactedTelemetry(t *testing.T) {
	fixture := newOperationsGatewayFixture(t, 30)
	exporter := &observability.MemoryExporter{}
	runtime := observability.NewRuntime(observability.RuntimeConfig{
		Service:  serviceName,
		Exporter: exporter,
	})
	fixture.handler.(*handler).observability = runtime
	incomingTraceID := strings.Repeat("1", 32)
	incomingSpanID := strings.Repeat("2", 16)
	path := "/api/v1/sites/" + fixture.siteID + "/operations/investigations/investigation-001/events"

	performRequest := func(lastEventID string) string {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("traceparent", "00-"+incomingTraceID+"-"+incomingSpanID+"-01")
		request.Header.Set("tracestate", "vendor=opaque")
		request.Header.Set("X-Request-ID", "request-operations-telemetry")
		if lastEventID != "" {
			request.Header.Set("Last-Event-ID", lastEventID)
		}
		fixture.authenticate(request, false)
		recorder := httptest.NewRecorder()
		fixture.handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
		}
		upstream := fixture.lastUpstream.Load()
		if upstream == nil {
			t.Fatal("Operations upstream request was not captured")
		}
		upstreamContext, ok := observability.ParseTraceparent(
			upstream.Header.Get("traceparent"),
			upstream.Header.Get("tracestate"),
		)
		if !ok || upstreamContext.TraceID != incomingTraceID || upstreamContext.SpanID == incomingSpanID {
			t.Fatalf("Operations upstream did not receive child trace context: %+v", upstreamContext)
		}
		if upstreamContext.TraceState != "vendor=opaque" {
			t.Fatalf("Operations upstream tracestate was not propagated: %q", upstreamContext.TraceState)
		}
		if lastEventID != "" && upstream.Header.Get("Last-Event-ID") != lastEventID {
			t.Fatalf("Operations reconnect position was not forwarded: %q", upstream.Header.Get("Last-Event-ID"))
		}
		return upstreamContext.SpanID
	}

	initialSpanID := performRequest("")
	reconnectSpanID := performRequest("9:2")
	if initialSpanID == reconnectSpanID {
		t.Fatal("reconnect reused the previous upstream span identity")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runtime.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	spans := exporter.Spans()
	gatewaySpans := make([]observability.SpanData, 0, 2)
	upstreamSpans := make([]observability.SpanData, 0, 2)
	recoverySpans := make([]observability.SpanData, 0, 2)
	for _, span := range spans {
		switch span.Name {
		case "http.gateway.request":
			gatewaySpans = append(gatewaySpans, span)
		case "operations.gateway.upstream":
			upstreamSpans = append(upstreamSpans, span)
		case "operations.gateway.recovery":
			recoverySpans = append(recoverySpans, span)
		}
	}
	if len(gatewaySpans) != 2 || len(upstreamSpans) != 2 || len(recoverySpans) != 2 {
		t.Fatalf("missing correlated Operations spans: %+v", spans)
	}
	expectedCorrelation := operationsTelemetryCorrelation("investigation", "investigation-001")
	for _, upstreamSpan := range upstreamSpans {
		if upstreamSpan.TraceID != incomingTraceID {
			t.Fatalf("upstream span left the incoming trace: %+v", upstreamSpan)
		}
		if upstreamSpan.Attributes["operations.investigation.correlation"] != expectedCorrelation {
			t.Fatalf("missing hashed Investigation correlation: %+v", upstreamSpan.Attributes)
		}
		parentFound := false
		for _, gatewaySpan := range gatewaySpans {
			if upstreamSpan.ParentSpanID == gatewaySpan.SpanID {
				parentFound = true
				break
			}
		}
		if !parentFound {
			t.Fatalf("upstream span was not a Gateway child: %+v", upstreamSpan)
		}
	}
	for _, recoverySpan := range recoverySpans {
		if recoverySpan.TraceID != incomingTraceID {
			t.Fatalf("recovery span left the incoming trace: %+v", recoverySpan)
		}
	}
	redactionSurface := append([]observability.SpanData(nil), spans...)
	for index := range redactionSurface {
		redactionSurface[index].StartTime = time.Time{}
		redactionSurface[index].EndTime = time.Time{}
	}
	serialized, err := json.Marshal(redactionSurface)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"investigation-001", "request-operations-telemetry", "operations-csrf-fixture",
		"operatorNote", "ownerPayload", "rawPrompt", "completion", "delegationGrant", "9:2",
	} {
		if strings.Contains(string(serialized), forbidden) {
			t.Fatalf("telemetry disclosed forbidden content %q: %s", forbidden, serialized)
		}
	}
	cardinality := runtime.Metrics.SeriesCardinality()
	if cardinality[operationsGatewayUpstreamRequests] != 1 ||
		cardinality[operationsGatewayUpstreamDuration] != 1 ||
		cardinality[operationsGatewayRecoveryTotal] != 2 {
		t.Fatalf("unexpected Operations metric cardinality: %+v", cardinality)
	}
}

func TestOperationsGatewayTelemetryExporterFailureDoesNotAlterResponse(t *testing.T) {
	fixture := newOperationsGatewayFixture(t, 30)
	runtime := observability.NewRuntime(observability.RuntimeConfig{
		Service:  serviceName,
		Exporter: failingOperationsTelemetryExporter{},
	})
	fixture.handler.(*handler).observability = runtime
	path := "/api/v1/sites/" + fixture.siteID + "/operations/investigations"
	request := httptest.NewRequest(http.MethodGet, path, nil)
	fixture.authenticate(request, false)
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("telemetry exporter failure altered response: %d %s", recorder.Code, recorder.Body.String())
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runtime.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if runtime.FailedExports() == 0 {
		t.Fatal("telemetry exporter failure was not isolated and counted")
	}
}
