package query

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/analyticsmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/services/telemetry-query-service/internal/analytics"
)

const (
	testPresenter           = "spiffe://hvac.local/platform-gateway"
	testOperationsPresenter = "spiffe://hvac.local/operations-agent-service"
	testAudience            = "telemetry-query-service"
	testOrganizationID      = "018f1e00-0000-7000-8000-000000000001"
	testSiteID              = "018f1e00-1000-7000-8000-000000000001"
	testPrincipalID         = "018f1e00-2000-7000-8000-000000000001"
)

type engineStub struct {
	response analyticsmodel.EnergySeriesResponse
	err      error
	caller   analytics.CallerContext
	query    analyticsmodel.EnergySeriesQuery
	calls    int
}

func (stub *engineStub) QueryEnergySeries(_ context.Context, caller analytics.CallerContext, query analyticsmodel.EnergySeriesQuery) (analyticsmodel.EnergySeriesResponse, error) {
	stub.calls++
	stub.caller = caller
	stub.query = query
	return stub.response, stub.err
}

func TestHandlerReturnsAuthorizedEnergySeries(t *testing.T) {
	now := time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
	query := validEnergyQuery()
	engine := &engineStub{response: analyticsmodel.EnergySeriesResponse{
		SchemaVersion: 1,
		Points:        []analyticsmodel.EnergySeriesPoint{{PeriodStart: query.From, PeriodEnd: query.From.Add(24 * time.Hour), EnergyKWh: 123.5}},
		Metadata: analyticsmodel.EnergySeriesMetadata{
			RequestedGranularity: query.Granularity,
			ActualGranularity:    query.Granularity,
			DatasetRevision:      "energy-daily:v1",
		},
	}}
	harness := newServerHarness(t, now, engine)
	response := harness.serve(t, query, nil, analyticsmodel.EnergySeriesAction)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
	}
	if engine.calls != 1 || engine.query.SiteID != testSiteID {
		t.Fatalf("engine calls=%d query=%#v", engine.calls, engine.query)
	}
	if engine.caller.PrincipalID != testPrincipalID || engine.caller.PolicyRevision != "analytics-policy:1" {
		t.Fatalf("caller = %#v", engine.caller)
	}
	var body analyticsmodel.EnergySeriesResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Points) != 1 || body.Points[0].EnergyKWh != 123.5 {
		t.Fatalf("body = %#v", body)
	}
}

func TestHandlerAcceptsGatewayIssuedGrantForOperationsAgentPresenter(t *testing.T) {
	now := time.Date(2026, 7, 31, 8, 0, 0, 0, time.UTC)
	query := validEnergyQuery()
	engine := &engineStub{response: analyticsmodel.EnergySeriesResponse{
		SchemaVersion: 1,
		Metadata: analyticsmodel.EnergySeriesMetadata{
			RequestedGranularity: query.Granularity,
			ActualGranularity:    query.Granularity,
			DatasetRevision:      "energy-daily:v1",
		},
	}}
	harness := newServerHarness(t, now, engine)
	response := harness.serveAsPresenter(t, query, testOperationsPresenter)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
	}
	if engine.calls != 1 {
		t.Fatalf("engine calls = %d", engine.calls)
	}
}

func TestHandlerRejectsExpiredDelegation(t *testing.T) {
	now := time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
	engine := &engineStub{}
	harness := newServerHarness(t, now, engine)
	query := validEnergyQuery()
	response := harness.serveWithGrantTimes(t, query, query, nil, analyticsmodel.EnergySeriesAction, testPrincipalID, now.Add(-time.Minute), now.Add(-time.Second))
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusForbidden, response.Body.String())
	}
	if engine.calls != 0 {
		t.Fatalf("engine calls = %d", engine.calls)
	}
}

func TestHandlerRejectsDelegationWithoutInternalPrincipalID(t *testing.T) {
	now := time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
	engine := &engineStub{}
	harness := newServerHarness(t, now, engine)
	query := validEnergyQuery()
	response := harness.serveWithGrantTimes(t, query, query, nil, analyticsmodel.EnergySeriesAction, "", now.Add(-time.Second), now.Add(30*time.Second))
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusForbidden, response.Body.String())
	}
	if engine.calls != 0 {
		t.Fatalf("engine calls = %d", engine.calls)
	}
}

func TestHandlerFailsClosedForScopeMismatchAndForgedHeaders(t *testing.T) {
	now := time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		headers    http.Header
		action     string
		mutate     func(*analyticsmodel.EnergySeriesQuery)
		wantStatus int
	}{
		{"wrong action", nil, "telemetry.snapshot.read", nil, http.StatusForbidden},
		{"forged site header", http.Header{"X-Site-ID": []string{testSiteID}}, analyticsmodel.EnergySeriesAction, nil, http.StatusBadRequest},
		{"scope changed after grant", nil, analyticsmodel.EnergySeriesAction, func(query *analyticsmodel.EnergySeriesQuery) {
			query.SiteID = "018f1e00-1000-7000-8000-000000000002"
		}, http.StatusForbidden},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			engine := &engineStub{}
			harness := newServerHarness(t, now, engine)
			query := validEnergyQuery()
			grantQuery := query
			if test.mutate != nil {
				test.mutate(&query)
			}
			response := harness.serveWithGrantQuery(t, query, grantQuery, test.headers, test.action)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, test.wantStatus, response.Body.String())
			}
			if engine.calls != 0 {
				t.Fatalf("engine calls = %d", engine.calls)
			}
		})
	}
}

type serverHarness struct {
	handler http.Handler
	signer  crypto.Signer
	now     time.Time
}

func newServerHarness(t *testing.T, now time.Time, engine analytics.EnergySeriesEngine) serverHarness {
	t.Helper()
	signer, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return serverHarness{
		handler: NewHandler(ServerConfig{
			Engine:                            engine,
			DelegationPublicKey:               signer.Public(),
			DelegationIssuerSPIFFE:            testPresenter,
			AllowedPresenterSPIFFE:            testPresenter,
			AdditionalAllowedPresenterSPIFFEs: []string{testOperationsPresenter},
			Audience:                          testAudience,
			Now:                               func() time.Time { return now },
		}),
		signer: signer,
		now:    now,
	}
}

func (harness serverHarness) serve(t *testing.T, query analyticsmodel.EnergySeriesQuery, headers http.Header, action string) *httptest.ResponseRecorder {
	t.Helper()
	return harness.serveWithGrantQuery(t, query, query, headers, action)
}

func (harness serverHarness) serveAsPresenter(
	t *testing.T,
	query analyticsmodel.EnergySeriesQuery,
	presenterSPIFFE string,
) *httptest.ResponseRecorder {
	t.Helper()
	return harness.serveWithGrant(
		t,
		query,
		query,
		nil,
		analyticsmodel.EnergySeriesAction,
		testPrincipalID,
		harness.now.Add(-time.Second),
		harness.now.Add(30*time.Second),
		presenterSPIFFE,
	)
}

func (harness serverHarness) serveWithGrantQuery(t *testing.T, query, grantQuery analyticsmodel.EnergySeriesQuery, headers http.Header, action string) *httptest.ResponseRecorder {
	t.Helper()
	return harness.serveWithGrantTimes(t, query, grantQuery, headers, action, testPrincipalID, harness.now.Add(-time.Second), harness.now.Add(30*time.Second))
}

func (harness serverHarness) serveWithGrantTimes(t *testing.T, query, grantQuery analyticsmodel.EnergySeriesQuery, headers http.Header, action, principalID string, issuedAt, expiresAt time.Time) *httptest.ResponseRecorder {
	t.Helper()
	return harness.serveWithGrant(
		t,
		query,
		grantQuery,
		headers,
		action,
		principalID,
		issuedAt,
		expiresAt,
		testPresenter,
	)
}

func (harness serverHarness) serveWithGrant(
	t *testing.T,
	query,
	grantQuery analyticsmodel.EnergySeriesQuery,
	headers http.Header,
	action,
	principalID string,
	issuedAt,
	expiresAt time.Time,
	presenterSPIFFE string,
) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(query)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := grantQuery.ScopeDigest()
	if err != nil {
		t.Fatal(err)
	}
	grant, err := identitycontext.SignDelegation(harness.signer, identitycontext.DelegationClaims{
		Issuer:               testPresenter,
		Subject:              "user-1",
		SubjectIssuer:        "issuer-test",
		PrincipalID:          principalID,
		ExecutingService:     presenterSPIFFE,
		Audience:             testAudience,
		ActingOrganizationID: testOrganizationID,
		Actions:              []string{action},
		Scopes:               []string{scope},
		PolicyRevision:       "analytics-policy:1",
		SessionID:            "session-test",
		IssuedAt:             issuedAt.Unix(),
		ExpiresAt:            expiresAt.Unix(),
		TokenID:              "token-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, EnergySeriesPath, bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Delegation-Grant", grant)
	for name, values := range headers {
		request.Header[name] = append([]string(nil), values...)
	}
	spiffe, err := url.Parse(presenterSPIFFE)
	if err != nil {
		t.Fatal(err)
	}
	request.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{{URIs: []*url.URL{spiffe}}}}
	response := httptest.NewRecorder()
	harness.handler.ServeHTTP(response, request)
	return response
}

func validEnergyQuery() analyticsmodel.EnergySeriesQuery {
	return analyticsmodel.EnergySeriesQuery{
		OrganizationID: testOrganizationID,
		SiteID:         testSiteID,
		EnergyType:     analyticsmodel.EnergyTypeElectricity,
		Granularity:    analyticsmodel.GranularityDay,
		Timezone:       "Asia/Shanghai",
		From:           time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		To:             time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		QualityPolicy:  analyticsmodel.QualityPolicyValidOnly,
	}
}
