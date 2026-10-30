package optimization

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

const (
	testGatewaySPIFFE = "spiffe://hvac.local/platform-gateway"
	testTenant        = "01990000-3000-7000-8000-000000000001"
	testOtherTenant   = "01990000-3000-7000-8000-000000000002"
	testSite          = "01990000-5000-7000-8000-000000000001"
	testOtherSite     = "01990000-5000-7000-8000-0000000000ff"
)

// oneTenantRecommendations holds a published recommendation for testTenant's testSite only.
type oneTenantRecommendations struct{ memoryEvaluationStore }

func (store *oneTenantRecommendations) LatestRecommendation(_ context.Context, tenantID, siteID string) (PublishedRecommendation, error) {
	if tenantID != testTenant || siteID != testSite {
		return PublishedRecommendation{}, ErrOptimizationNotFound
	}
	return PublishedRecommendation{RunID: "01990000-1950-7000-8000-000000000101"}, nil
}

// testSite's schedule last fired at 12:20 and found no zone temperature.
func (store *oneTenantRecommendations) LatestPreparation(_ context.Context, tenantID, siteID string) (PreparationOutcome, error) {
	if tenantID != testTenant || siteID != testSite {
		return PreparationOutcome{}, ErrPreparationNotFound
	}
	reason := string(ReasonZoneTempMissing)
	fired := time.Date(2026, 10, 30, 12, 20, 0, 0, time.UTC)
	completed := fired.Add(2 * time.Second)
	return PreparationOutcome{Trigger: TriggerSchedule, Status: "FAILED", Reason: &reason, FiredAt: fired, CompletedAt: &completed}, nil
}

// optimizationFixture serves Optimization behind the Gateway's contexts and signs them as the
// Gateway does.
type optimizationFixture struct {
	handler  http.Handler
	prepared *preparationStoreStub
	signer   *ecdsa.PrivateKey
	now      time.Time
	// audience the contexts are signed for; Optimization's own unless a test changes it.
	audience string
}

func newOptimizationFixture(t *testing.T) optimizationFixture {
	t.Helper()
	at := time.Date(2026, 8, 28, 15, 0, 0, 0, time.UTC)
	service, err := NewDefaultService(&oneTenantRecommendations{}, &captureEvaluationSink{}, func() time.Time { return at })
	if err != nil {
		t.Fatal(err)
	}
	store := &preparationStoreStub{
		definition: testPreparationDefinition(),
		result: PreparedOptimization{
			OptimizationRunID: "01990000-1950-7000-8000-000000000101",
			InputSnapshotID:   "01990000-1930-7000-8000-000000000101",
			Status:            "PENDING",
		},
	}
	preparer, err := NewPreparer(store, &authoritativeInputStub{state: testAuthoritativeState(at)}, func() time.Time { return at })
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHTTPHandler(service, preparer, AuthConfig{GatewayPublicKey: &signer.PublicKey, GatewaySPIFFE: testGatewaySPIFFE, Now: func() time.Time { return at }})
	if err != nil {
		t.Fatal(err)
	}
	return optimizationFixture{handler: handler, prepared: store, signer: signer, now: at, audience: Audience}
}

// call presents a context signed for presenter, action, Tenant and Site from the peer workload, or
// none when action is empty. It also sends the X-Tenant-ID a caller could always write; that must
// decide nothing.
func (fixture optimizationFixture) call(t *testing.T, method, path, body, peer, presenter, action, tenantID, siteID string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	request := httptest.NewRequest(method, path, reader)
	uri, _ := url.Parse(peer)
	request.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{{URIs: []*url.URL{uri}}}}
	request.Header.Set("X-Tenant-ID", testTenant)
	if action != "" {
		token, err := identitycontext.SignDelegation(fixture.signer, identitycontext.DelegationClaims{
			Issuer: testGatewaySPIFFE, Subject: "operator", SubjectIssuer: "https://identity.example.test",
			ExecutingService: presenter, Audience: fixture.audience, TenantID: tenantID,
			Actions: []string{action}, Scopes: []string{"tenant:" + tenantID, "site:" + siteID},
			PolicyRevision: "policy-1", SessionID: "session-1", IssuedAt: fixture.now.Add(-time.Second).Unix(),
			ExpiresAt: fixture.now.Add(30 * time.Second).Unix(), TokenID: "token-1",
		})
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set(ContextHeader, token)
	}
	response := httptest.NewRecorder()
	fixture.handler.ServeHTTP(response, request)
	return response
}

// #612: Optimization trusted any caller's X-Tenant-ID. It now serves only the Gateway, with a
// context the Gateway signed for the Site, and takes the Tenant from that context.
func TestOptimizationServesOnlyTheGatewayWithAContextForTheSite(t *testing.T) {
	fixture := newOptimizationFixture(t)
	latest := "/v1/sites/" + testSite + "/optimization/recommendations/latest"
	read := func(peer, presenter, action, tenantID, siteID string) int {
		return fixture.call(t, http.MethodGet, latest, "", peer, presenter, action, tenantID, siteID).Code
	}
	if status := read(testGatewaySPIFFE, testGatewaySPIFFE, ReadAction, testTenant, testSite); status != http.StatusOK {
		t.Fatalf("the Gateway could not read the Site's recommendation with its context: %d", status)
	}
	if status := read(testGatewaySPIFFE, testGatewaySPIFFE, ReadAction, testOtherTenant, testSite); status != http.StatusNotFound {
		t.Fatalf("another Tenant's context read this Tenant's recommendation: %d", status)
	}
	other := "spiffe://hvac.local/telemetry-worker"
	if status := read(other, other, ReadAction, testTenant, testSite); status != http.StatusForbidden {
		t.Fatalf("another workload read the recommendation: %d", status)
	}
	if status := read(testGatewaySPIFFE, testGatewaySPIFFE, ReadAction, testTenant, testOtherSite); status != http.StatusForbidden {
		t.Fatalf("a context for another Site read this Site: %d", status)
	}
	if status := read(testGatewaySPIFFE, testGatewaySPIFFE, WriteAction, testTenant, testSite); status != http.StatusForbidden {
		t.Fatalf("a run context read the recommendation: %d", status)
	}

	if status := read(testGatewaySPIFFE, testGatewaySPIFFE, "", testTenant, testSite); status != http.StatusForbidden {
		t.Fatalf("the Gateway's workload was served without a context: %d", status)
	}
	elsewhere := fixture
	elsewhere.audience = "fdd-service"
	if status := elsewhere.call(t, http.MethodGet, latest, "", testGatewaySPIFFE, testGatewaySPIFFE, ReadAction, testTenant, testSite).Code; status != http.StatusForbidden {
		t.Fatalf("a context for another owner was accepted: %d", status)
	}

	bare := httptest.NewRequest(http.MethodGet, latest, nil)
	bare.Header.Set("X-Tenant-ID", testTenant)
	response := httptest.NewRecorder()
	fixture.handler.ServeHTTP(response, bare)
	if response.Code != http.StatusForbidden {
		t.Fatalf("a request with only X-Tenant-ID was served: %d", response.Code)
	}
}

// A run is prepared for the Tenant and Site its context covers, and only on a run context.
func TestOptimizationRunsOnlyForTheSiteItsContextCovers(t *testing.T) {
	fixture := newOptimizationFixture(t)
	run := func(body, action, tenantID, siteID string) int {
		return fixture.call(t, http.MethodPost, "/v1/optimize", body, testGatewaySPIFFE, testGatewaySPIFFE, action, tenantID, siteID).Code
	}
	if status := run(`{"siteId":"`+testSite+`"}`, ReadAction, testTenant, testSite); status != http.StatusForbidden {
		t.Fatalf("a read context ran Optimization: %d", status)
	}
	if status := run(`{"siteId":"`+testOtherSite+`"}`, WriteAction, testTenant, testSite); status != http.StatusForbidden {
		t.Fatalf("a run for another Site than its context ran: %d", status)
	}
	if len(fixture.prepared.created) != 0 {
		t.Fatalf("a refused run was prepared: %#v", fixture.prepared.created)
	}
	if status := run(`{"siteId":"`+testSite+`"}`, WriteAction, testOtherTenant, testSite); status != http.StatusAccepted {
		t.Fatalf("the Gateway's run context was refused: %d", status)
	}
	if len(fixture.prepared.created) != 1 || fixture.prepared.created[0].TenantID != testOtherTenant || fixture.prepared.created[0].SiteID != testSite {
		t.Fatalf("the run was not prepared for its context's Tenant and Site: %#v", fixture.prepared.created)
	}
}

func TestOptimizationHTTPRejectsCallerAuthoredInputs(t *testing.T) {
	fixture := newOptimizationFixture(t)
	body := `{"siteId":"` + testSite + `","baseline":{"dailyEnergyKWh":1}}`
	response := fixture.call(t, http.MethodPost, "/v1/optimize", body, testGatewaySPIFFE, testGatewaySPIFFE, WriteAction, testTenant, testSite)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

// The Site's latest schedule firing reads back with the input it was missing, on a read context
// for the Site only.
func TestOptimizationServesTheLatestPreparationWithItsReason(t *testing.T) {
	fixture := newOptimizationFixture(t)
	latest := "/v1/sites/" + testSite + "/optimization/preparations/latest"
	response := fixture.call(t, http.MethodGet, latest, "", testGatewaySPIFFE, testGatewaySPIFFE, ReadAction, testTenant, testSite)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	want := `{"trigger":"SCHEDULE","status":"FAILED","reason":"ZONE_TEMPERATURE_MISSING","firedAt":"2026-10-30T12:20:00Z","completedAt":"2026-10-30T12:20:02Z"}`
	if got := strings.TrimSpace(response.Body.String()); got != want {
		t.Fatalf("body=%s\nwant=%s", got, want)
	}
	if status := fixture.call(t, http.MethodGet, latest, "", testGatewaySPIFFE, testGatewaySPIFFE, ReadAction, testOtherTenant, testSite).Code; status != http.StatusNotFound {
		t.Fatalf("another Tenant read this Site's preparation: %d", status)
	}
	if status := fixture.call(t, http.MethodGet, latest, "", testGatewaySPIFFE, testGatewaySPIFFE, ReadAction, testTenant, testOtherSite).Code; status != http.StatusForbidden {
		t.Fatalf("a context for another Site read this Site's preparation: %d", status)
	}
	if status := fixture.call(t, http.MethodGet, latest, "", testGatewaySPIFFE, testGatewaySPIFFE, WriteAction, testTenant, testSite).Code; status != http.StatusForbidden {
		t.Fatalf("a run context read the preparation: %d", status)
	}
}

// A firing records its reason as the job's error code; codes recorded before reasons existed, or
// that are not reasons, read back as a plain failure.
func TestOptimizationPreparationOutcomeFromTheFiringJob(t *testing.T) {
	fired := time.Date(2026, 10, 30, 12, 20, 0, 0, time.UTC)
	code := func(value string) *string { return &value }
	cases := []struct {
		state      string
		code       *string
		wantStatus string
		wantReason string
	}{
		{"SUCCEEDED", nil, "PREPARED", ""},
		{"RUNNING", nil, "PENDING", ""},
		{"FAILED", code(PreparationFailureCode(unavailable(ReasonModelDeploymentMissing, "none"))), "FAILED", "MODEL_DEPLOYMENT_MISSING"},
		{"FAILED", code("OPTIMIZATION_INPUT_UNAVAILABLE"), "FAILED", PreparationFailedCode},
		{"FAILED", code(PreparationFailureCode(errors.New("persist failed"))), "FAILED", PreparationFailedCode},
	}
	for _, test := range cases {
		outcome := preparationOutcome(test.state, test.code, PreparationOutcome{FiredAt: fired})
		reason := ""
		if outcome.Reason != nil {
			reason = *outcome.Reason
		}
		if outcome.Trigger != TriggerSchedule || outcome.Status != test.wantStatus || reason != test.wantReason {
			t.Fatalf("%s/%v: outcome=%#v reason=%q", test.state, test.code, outcome, reason)
		}
	}
}
