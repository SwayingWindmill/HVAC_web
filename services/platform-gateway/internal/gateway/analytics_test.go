package gateway

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
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
	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
	"github.com/quanlaihe/hvac-web/libs/sessionstore"
)

const (
	analyticsGatewayTenant       = "018f1d00-0000-7000-8000-000000000001"
	analyticsGatewayOtherTenant  = "018f1d00-0000-7000-8000-000000000002"
	analyticsGatewayOrganization = "018f1e00-0000-7000-8000-000000000001"
	analyticsGatewaySite         = "018f1e00-1000-7000-8000-000000000001"
	analyticsGatewayOtherSite    = "018f1e00-1000-7000-8000-000000000002"
	analyticsGatewayPrincipal    = "018f1e00-2000-7000-8000-000000000001"
	analyticsGatewaySPIFFE       = "spiffe://hvac.local/platform-gateway"
	analyticsGatewayCSRF         = "analytics-csrf-token"
)

type analyticsGatewayFixture struct {
	handler    http.Handler
	sessionID  string
	now        time.Time
	signer     *ecdsa.PrivateKey
	iamCalls   atomic.Int32
	queryCalls atomic.Int32
	denySite   bool
}

func TestGatewayEnergySeriesSignsExactGrantAndPreservesResponse(t *testing.T) {
	fixture := newAnalyticsGatewayFixture(t, analyticsFixtureOptions{})
	query := validGatewayEnergyQuery(fixture.now)
	recorder := fixture.request(t, query, true)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response analyticsmodel.EnergySeriesResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.SchemaVersion != 1 || len(response.Points) != 1 || response.Points[0].EnergyKWh != 12.5 || response.Metadata.DatasetRevision != "energy:v1:7" {
		t.Fatalf("response=%#v", response)
	}
	if fixture.iamCalls.Load() != 1 || fixture.queryCalls.Load() != 1 {
		t.Fatalf("calls iam=%d query=%d", fixture.iamCalls.Load(), fixture.queryCalls.Load())
	}
	if recorder.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("Cache-Control=%q", recorder.Header().Get("Cache-Control"))
	}
}

func TestGatewayEnergySeriesRunsThroughQueryServiceRouteOwnership(t *testing.T) {
	fixture := newAnalyticsGatewayFixture(t, analyticsFixtureOptions{routeOwnership: true})
	recorder := fixture.request(t, validGatewayEnergyQuery(fixture.now), true)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if fixture.iamCalls.Load() != 1 || fixture.queryCalls.Load() != 1 {
		t.Fatalf("calls iam=%d query=%d", fixture.iamCalls.Load(), fixture.queryCalls.Load())
	}
}

func TestGatewayEnergySeriesRejectsTenantMismatchFromIAM(t *testing.T) {
	fixture := newAnalyticsGatewayFixture(t, analyticsFixtureOptions{})
	query := validGatewayEnergyQuery(fixture.now)
	query.TenantID = analyticsGatewayOtherTenant
	recorder := fixture.request(t, query, true)
	assertAnalyticsProblem(t, recorder, http.StatusServiceUnavailable, "ANALYTICS_UNAVAILABLE")
	if fixture.iamCalls.Load() != 1 || fixture.queryCalls.Load() != 0 {
		t.Fatalf("unexpected upstream calls iam=%d query=%d", fixture.iamCalls.Load(), fixture.queryCalls.Load())
	}
}

func TestGatewayEnergySeriesRejectsUnauthorizedSite(t *testing.T) {
	fixture := newAnalyticsGatewayFixture(t, analyticsFixtureOptions{denySite: true})
	query := validGatewayEnergyQuery(fixture.now)
	query.SiteID = analyticsGatewayOtherSite
	recorder := fixture.request(t, query, true)
	assertAnalyticsProblem(t, recorder, http.StatusForbidden, "ANALYTICS_ACCESS_DENIED")
	if fixture.iamCalls.Load() != 1 || fixture.queryCalls.Load() != 0 {
		t.Fatalf("calls iam=%d query=%d", fixture.iamCalls.Load(), fixture.queryCalls.Load())
	}
}

func TestGatewayEnergySeriesRequiresCSRFAndValidQueryBudget(t *testing.T) {
	fixture := newAnalyticsGatewayFixture(t, analyticsFixtureOptions{})
	query := validGatewayEnergyQuery(fixture.now)
	missingCSRF := fixture.request(t, query, false)
	assertAnalyticsProblem(t, missingCSRF, http.StatusForbidden, "CSRF_REQUIRED")

	query.To = query.From.Add(367 * 24 * time.Hour)
	invalidRange := fixture.request(t, query, true)
	assertAnalyticsProblem(t, invalidRange, http.StatusUnprocessableEntity, "ANALYTICS_QUERY_INVALID")
	if fixture.iamCalls.Load() != 0 || fixture.queryCalls.Load() != 0 {
		t.Fatalf("unexpected upstream calls iam=%d query=%d", fixture.iamCalls.Load(), fixture.queryCalls.Load())
	}
}

func TestGatewayEnergySeriesMapsIAMInternalRejectionAndUnavailable(t *testing.T) {
	invalidFixture := newAnalyticsGatewayFixture(t, analyticsFixtureOptions{iamStatus: http.StatusForbidden})
	invalid := invalidFixture.request(t, validGatewayEnergyQuery(invalidFixture.now), true)
	assertAnalyticsProblem(t, invalid, http.StatusBadGateway, "ANALYTICS_AUTHORIZATION_INVALID")
	if invalidFixture.queryCalls.Load() != 0 {
		t.Fatalf("query calls=%d", invalidFixture.queryCalls.Load())
	}

	unavailableFixture := newAnalyticsGatewayFixture(t, analyticsFixtureOptions{iamStatus: http.StatusServiceUnavailable})
	unavailable := unavailableFixture.request(t, validGatewayEnergyQuery(unavailableFixture.now), true)
	assertAnalyticsProblem(t, unavailable, http.StatusServiceUnavailable, "ANALYTICS_UNAVAILABLE")
	if unavailableFixture.queryCalls.Load() != 0 {
		t.Fatalf("query calls=%d", unavailableFixture.queryCalls.Load())
	}
}

func TestGatewayEnergySeriesMapsQueryTimeoutAndInvalidResponse(t *testing.T) {
	timeoutFixture := newAnalyticsGatewayFixture(t, analyticsFixtureOptions{queryTimeout: 10 * time.Millisecond, queryMode: "timeout"})
	timedOut := timeoutFixture.request(t, validGatewayEnergyQuery(timeoutFixture.now), true)
	assertAnalyticsProblem(t, timedOut, http.StatusGatewayTimeout, "ANALYTICS_TIMEOUT")

	invalidFixture := newAnalyticsGatewayFixture(t, analyticsFixtureOptions{queryMode: "invalid"})
	invalid := invalidFixture.request(t, validGatewayEnergyQuery(invalidFixture.now), true)
	assertAnalyticsProblem(t, invalid, http.StatusBadGateway, "ANALYTICS_RESPONSE_INVALID")
}

type analyticsFixtureOptions struct {
	denySite       bool
	iamStatus      int
	queryMode      string
	queryTimeout   time.Duration
	routeOwnership bool
}

func newAnalyticsGatewayFixture(t *testing.T, options analyticsFixtureOptions) *analyticsGatewayFixture {
	t.Helper()
	now := time.Date(2026, 7, 30, 2, 0, 0, 0, time.UTC)
	signer, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &analyticsGatewayFixture{now: now, signer: signer, denySite: options.denySite}
	iamClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		fixture.iamCalls.Add(1)
		if request.URL.Path != analyticsDecisionPath || request.Method != http.MethodPost {
			t.Fatalf("IAM request=%s %s", request.Method, request.URL.Path)
		}
		for _, header := range []string{"Cookie", "X-CSRF-Token", "Origin", "X-Organization-ID", "X-Site-ID"} {
			if request.Header.Get(header) != "" {
				t.Fatalf("browser authority leaked to IAM header %s", header)
			}
		}
		if request.Header.Get("traceparent") == "" {
			t.Fatal("traceparent was not propagated to IAM")
		}
		parent, err := identitycontext.VerifyDelegation(&signer.PublicKey, request.Header.Get("X-Delegation-Grant"))
		if err != nil {
			t.Fatalf("parent delegation: %v", err)
		}
		if len(parent.Actions) != 1 || parent.Actions[0] != analyticsAuthorizeAction || parent.ActingOrganizationID != analyticsGatewayOrganization || parent.Audience != "iam-service" {
			t.Fatalf("parent claims=%#v", parent)
		}
		if options.iamStatus != 0 {
			return analyticsHTTPResponse(options.iamStatus, map[string]any{"code": "IAM_TEST_FAILURE"}), nil
		}
		var input analyticsmodel.AuthorizationDecisionRequest
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
			t.Fatal(err)
		}
		allowed := !fixture.denySite && input.SiteID == analyticsGatewaySite
		reason := analyticsmodel.AuthorizationReasonDenyAction
		if allowed {
			reason = analyticsmodel.AuthorizationReasonAllowSiteBinding
		}
		return analyticsHTTPResponse(http.StatusOK, analyticsmodel.AuthorizationDecisionResponse{Decision: analyticsmodel.AuthorizationDecision{
			Allowed: allowed, PrincipalID: analyticsGatewayPrincipal, SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject,
			ActingOrganizationID: parent.ActingOrganizationID, TenantID: analyticsGatewayTenant, SiteID: input.SiteID, Action: input.Action,
			PolicyRevision: "analytics-policy-7", ReasonCode: reason, DecidedAt: now.Format(time.RFC3339Nano),
		}}), nil
	})}
	queryClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		fixture.queryCalls.Add(1)
		if request.URL.Path != internalEnergySeriesPath || request.Method != http.MethodPost {
			t.Fatalf("query request=%s %s", request.Method, request.URL.Path)
		}
		for _, header := range []string{"Cookie", "X-CSRF-Token", "Origin", "X-Organization-ID", "X-Site-ID"} {
			if request.Header.Get(header) != "" {
				t.Fatalf("browser authority leaked to Query Service header %s", header)
			}
		}
		if request.Header.Get("traceparent") == "" {
			t.Fatal("traceparent was not propagated to Telemetry Query Service")
		}
		var query analyticsmodel.EnergySeriesQuery
		if err := json.NewDecoder(request.Body).Decode(&query); err != nil {
			t.Fatal(err)
		}
		grant, err := identitycontext.VerifyDelegation(&signer.PublicKey, request.Header.Get("X-Delegation-Grant"))
		if err != nil {
			t.Fatalf("query delegation: %v", err)
		}
		digest, err := query.ScopeDigest()
		if err != nil {
			t.Fatal(err)
		}
		if grant.Audience != "telemetry-query-service" || grant.PrincipalID != analyticsGatewayPrincipal || len(grant.Actions) != 1 || grant.Actions[0] != analyticsmodel.EnergySeriesAction || len(grant.Scopes) != 1 || grant.Scopes[0] != digest || grant.ActingOrganizationID != analyticsGatewayOrganization || grant.TenantID != query.TenantID || grant.PolicyRevision != "analytics-policy-7" {
			t.Fatalf("query grant=%#v digest=%s", grant, digest)
		}
		switch options.queryMode {
		case "timeout":
			<-request.Context().Done()
			return nil, request.Context().Err()
		case "invalid":
			return analyticsHTTPResponse(http.StatusOK, map[string]any{"schemaVersion": 1, "points": []any{}, "metadata": map[string]any{"datasetRevision": ""}}), nil
		default:
			watermark := query.To
			return analyticsHTTPResponse(http.StatusOK, analyticsmodel.EnergySeriesResponse{
				SchemaVersion: 1,
				Points:        []analyticsmodel.EnergySeriesPoint{{PeriodStart: query.From, PeriodEnd: query.From.Add(24 * time.Hour), EnergyKWh: 12.5}},
				Metadata: analyticsmodel.EnergySeriesMetadata{
					RequestedGranularity: query.Granularity, ActualGranularity: query.Granularity,
					DataWatermark: &watermark, AggregateWatermark: &watermark, DatasetRevision: "energy:v1:7", Partial: false,
					QualitySummary: analyticsmodel.QualitySummary{Valid: 1},
				},
			}), nil
		}
	})}
	store := sessionstore.NewMemoryStore()
	timeout := options.queryTimeout
	if timeout == 0 {
		timeout = time.Second
	}
	var routeManager *ownershipregistry.Manager
	if options.routeOwnership {
		snapshot, err := ownershipregistry.Parse([]byte(`{
			"registryVersion":1,"registryRevision":1,"routes":[{
				"method":"POST","path":"/api/v1/analytics/energy-series","owner":"telemetry-query-service","publicIngress":"platform-gateway","revision":1,
				"rollout":{"mode":"all"},"compatibilityMode":"native",
				"allowedScopeDimensions":["tenant","site","principal"]
			}]
		}`))
		if err != nil {
			t.Fatal(err)
		}
		routeManager = ownershipregistry.NewManager(snapshot, ownershipregistry.NewMemoryAuditSink(), func() time.Time { return now })
	}
	configured := NewHandler(Config{
		Now:          func() time.Time { return now },
		RouteManager: routeManager,
		Identity: &IdentityConfig{
			OIDCIssuer: "https://issuer.example.test", OIDCClientID: "client", OIDCRedirectURI: "https://web.example.test/api/v1/auth/callback",
			PublicOrigin: "https://web.example.test", IAMURL: "https://iam.example.test", IAMAudience: "iam-service",
			ExecutingWorkloadSPIFFE: analyticsGatewaySPIFFE, PolicyRevision: "identity-policy-1", DelegationSigner: signer,
			TokenEncryptionKey: make([]byte, 32), SessionStore: store, SessionTTL: time.Hour, DelegationTTL: 30 * time.Second, IAMHTTPClient: iamClient,
		},
		Analytics: &AnalyticsConfig{QueryBaseURL: "https://query.example.test", QueryHTTPClient: queryClient, QueryAudience: "telemetry-query-service", Timeout: timeout},
	}).(*handler)
	csrfCiphertext, err := configured.identity.encryptBytes([]byte(analyticsGatewayCSRF))
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateSession(context.Background(), sessionstore.Session{
		ID: "analytics-session", Principal: identitycontext.UserPrincipal{Subject: "energy-user", Issuer: "https://issuer.example.test", Roles: []string{"energy-analyst"}},
		ActingOrganizationID: analyticsGatewayOrganization, CSRFTokenCiphertext: csrfCiphertext, ExpiresAt: now.Add(time.Hour),
	}, sessionstore.MutationContext{Action: "SESSION_CREATED", Result: "SUCCEEDED", PolicyRevision: "identity-policy-1", CorrelationID: "analytics-fixture", TraceID: strings.Repeat("a", 32), Traceparent: "00-" + strings.Repeat("a", 32) + "-" + strings.Repeat("b", 16) + "-01", ExecutingService: "platform-gateway", ExecutingSPIFFEID: analyticsGatewaySPIFFE, OccurredAt: now})
	if err != nil {
		t.Fatal(err)
	}
	fixture.handler = configured
	fixture.sessionID = created.ID
	return fixture
}

func (fixture *analyticsGatewayFixture) request(t *testing.T, query analyticsmodel.EnergySeriesQuery, includeCSRF bool) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(query)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, PublicEnergySeriesPath, bytes.NewReader(body))
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: fixture.sessionID})
	request.Header.Set("Origin", "https://web.example.test")
	request.Header.Set("Content-Type", "application/json")
	if includeCSRF {
		request.Header.Set("X-CSRF-Token", analyticsGatewayCSRF)
	}
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	return recorder
}

func validGatewayEnergyQuery(now time.Time) analyticsmodel.EnergySeriesQuery {
	return analyticsmodel.EnergySeriesQuery{
		TenantID: analyticsGatewayTenant, SiteID: analyticsGatewaySite, EnergyType: analyticsmodel.EnergyTypeElectricity,
		Granularity: analyticsmodel.GranularityDay, Timezone: "Asia/Shanghai", From: now.Add(-48 * time.Hour), To: now,
		QualityPolicy: analyticsmodel.QualityPolicyValidOnly,
	}
}

func analyticsHTTPResponse(status int, value any) *http.Response {
	body, _ := json.Marshal(value)
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(body))}
}

func assertAnalyticsProblem(t *testing.T, recorder *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if recorder.Code != status {
		t.Fatalf("status=%d want=%d body=%s", recorder.Code, status, recorder.Body.String())
	}
	var problem struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&problem); err != nil {
		t.Fatal(err)
	}
	if problem.Code != code {
		t.Fatalf("code=%q want=%q", problem.Code, code)
	}
}
