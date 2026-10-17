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
	"github.com/quanlaihe/hvac-web/libs/telemetryhistorymodel"
	"github.com/quanlaihe/hvac-web/modules/telemetry/internal/analytics"
)

const (
	testPresenter           = "spiffe://hvac.local/platform-gateway"
	testOperationsPresenter = "spiffe://hvac.local/operations-agent-service"
	testAudience            = "telemetry-query-service"
	testTenantID            = "018f1d00-0000-7000-8000-000000000001"
	testOrganizationID      = "018f1e00-0000-7000-8000-000000000001"
	testSiteID              = "018f1e00-1000-7000-8000-000000000001"
	testPrincipalID         = "018f1e00-2000-7000-8000-000000000001"
	testIAM                 = "spiffe://hvac.local/iam-service"
	testReportWorkload      = "spiffe://hvac.local/report-service"
	testReportPrincipalID   = "018f1e00-2000-7000-8000-0000000000bb"
)

type engineStub struct {
	response analyticsmodel.EnergySeriesResponse
	err      error
	caller   analytics.CallerContext
	query    analyticsmodel.EnergySeriesQuery
	calls    int
}

func (stub *engineStub) QuerySemantic(context.Context, analytics.CallerContext, analyticsmodel.SemanticQuery) (analyticsmodel.SemanticQueryResponse, error) {
	stub.calls++
	return analyticsmodel.SemanticQueryResponse{}, stub.err
}

func (stub *engineStub) QueryEnergySeries(_ context.Context, caller analytics.CallerContext, query analyticsmodel.EnergySeriesQuery) (analyticsmodel.EnergySeriesResponse, error) {
	stub.calls++
	stub.caller = caller
	stub.query = query
	return stub.response, stub.err
}

type historyEngineStub struct {
	response          telemetryhistorymodel.DeviceHistoryResponse
	aggregateResponse telemetryhistorymodel.DeviceHistoryAggregateResponse
	err               error
	query             telemetryhistorymodel.DeviceHistoryQuery
	aggregateQuery    telemetryhistorymodel.DeviceHistoryAggregateQuery
	calls             int
	aggregateCalls    int
}

func (stub *historyEngineStub) QueryDeviceHistory(_ context.Context, query telemetryhistorymodel.DeviceHistoryQuery) (telemetryhistorymodel.DeviceHistoryResponse, error) {
	stub.calls++
	stub.query = query
	return stub.response, stub.err
}

func (stub *historyEngineStub) QueryDeviceHistoryAggregate(_ context.Context, query telemetryhistorymodel.DeviceHistoryAggregateQuery) (telemetryhistorymodel.DeviceHistoryAggregateResponse, error) {
	stub.aggregateCalls++
	stub.aggregateQuery = query
	return stub.aggregateResponse, stub.err
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
	handler   http.Handler
	signer    crypto.Signer
	iamSigner crypto.Signer
	now       time.Time
}

func newServerHarness(t *testing.T, now time.Time, engine analytics.EnergySeriesEngine) serverHarness {
	t.Helper()
	signer, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	iamSigner, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return serverHarness{
		handler: NewHandler(ServerConfig{
			Engine:                            engine,
			HistoryEngine:                     &historyEngineStub{},
			DelegationPublicKey:               signer.Public(),
			DelegationIssuerSPIFFE:            testPresenter,
			WorkloadGrantPublicKey:            iamSigner.Public(),
			WorkloadGrantIssuerSPIFFE:         testIAM,
			AllowedPresenterSPIFFE:            testPresenter,
			AdditionalAllowedPresenterSPIFFEs: []string{testOperationsPresenter, testReportWorkload},
			Audience:                          testAudience,
			Now:                               func() time.Time { return now },
		}),
		signer:    signer,
		iamSigner: iamSigner,
		now:       now,
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
		Issuer:           testPresenter,
		Subject:          "user-1",
		SubjectIssuer:    "issuer-test",
		PrincipalID:      principalID,
		ExecutingService: presenterSPIFFE,
		Audience:         testAudience,
		TenantID:         testTenantID,
		Actions:          []string{action},
		Scopes:           []string{scope},
		PolicyRevision:   "analytics-policy:1",
		SessionID:        "session-test",
		IssuedAt:         issuedAt.Unix(),
		ExpiresAt:        expiresAt.Unix(),
		TokenID:          "token-test",
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
		TenantID:      testTenantID,
		SiteID:        testSiteID,
		EnergyType:    analyticsmodel.EnergyTypeElectricity,
		Granularity:   analyticsmodel.GranularityDay,
		Timezone:      "Asia/Shanghai",
		From:          time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		To:            time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		QualityPolicy: analyticsmodel.QualityPolicyValidOnly,
	}
}

// workloadGrant is what IAM signs for a Workload Principal reading for itself (ADR 0017).
func (harness serverHarness) workloadGrant(t *testing.T, query analyticsmodel.EnergySeriesQuery, signer crypto.Signer, mutate func(*identitycontext.DelegationClaims)) string {
	t.Helper()
	scope, err := query.ScopeDigest()
	if err != nil {
		t.Fatal(err)
	}
	claims := identitycontext.DelegationClaims{
		Issuer: testIAM, Subject: testReportWorkload, SubjectIssuer: "spiffe://hvac.local", PrincipalID: testReportPrincipalID,
		ExecutingService: testReportWorkload, Audience: testAudience, TenantID: testTenantID,
		Actions: []string{analyticsmodel.EnergySeriesAction}, Scopes: []string{scope}, PolicyRevision: "analytics-policy:1",
		IssuedAt: harness.now.Add(-time.Second).Unix(), ExpiresAt: harness.now.Add(30 * time.Second).Unix(), TokenID: "token-workload",
	}
	if mutate != nil {
		mutate(&claims)
	}
	grant, err := identitycontext.SignDelegation(signer, claims)
	if err != nil {
		t.Fatal(err)
	}
	return grant
}

func (harness serverHarness) serveGrant(t *testing.T, query analyticsmodel.EnergySeriesQuery, grant, presenterSPIFFE string) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(query)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, EnergySeriesPath, bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Delegation-Grant", grant)
	spiffe, err := url.Parse(presenterSPIFFE)
	if err != nil {
		t.Fatal(err)
	}
	request.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{{URIs: []*url.URL{spiffe}}}}
	response := httptest.NewRecorder()
	harness.handler.ServeHTTP(response, request)
	return response
}

func TestHandlerServesAWorkloadReadingWithItsOwnIAMGrant(t *testing.T) {
	now := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	query := validEnergyQuery()
	engine := &engineStub{response: analyticsmodel.EnergySeriesResponse{SchemaVersion: 1, Metadata: analyticsmodel.EnergySeriesMetadata{
		RequestedGranularity: query.Granularity, ActualGranularity: query.Granularity, DatasetRevision: "energy-daily:v1",
	}}}
	harness := newServerHarness(t, now, engine)
	response := harness.serveGrant(t, query, harness.workloadGrant(t, query, harness.iamSigner, nil), testReportWorkload)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
	}
	if engine.calls != 1 || engine.caller.PrincipalID != testReportPrincipalID {
		t.Fatalf("engine calls=%d caller=%#v", engine.calls, engine.caller)
	}
}

func TestHandlerRefusesWorkloadGrantsThatAreNotTheWorkloadsOwn(t *testing.T) {
	now := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	query := validEnergyQuery()
	engine := &engineStub{}
	harness := newServerHarness(t, now, engine)
	cases := map[string]struct {
		grant     string
		presenter string
	}{
		"presented by another workload": {harness.workloadGrant(t, query, harness.iamSigner, nil), testOperationsPresenter},
		"subject is not the presenter":  {harness.workloadGrant(t, query, harness.iamSigner, func(c *identitycontext.DelegationClaims) { c.Subject = "user-1" }), testReportWorkload},
		"a Session stands behind it":    {harness.workloadGrant(t, query, harness.iamSigner, func(c *identitycontext.DelegationClaims) { c.SessionID = "session-1" }), testReportWorkload},
		"signed with the Gateway's key": {harness.workloadGrant(t, query, harness.signer, nil), testReportWorkload},
		"bound to another query":        {harness.workloadGrant(t, validEnergyQueryForSite("018f1e00-1000-7000-8000-000000000099"), harness.iamSigner, nil), testReportWorkload},
	}
	for name, testCase := range cases {
		response := harness.serveGrant(t, query, testCase.grant, testCase.presenter)
		if response.Code != http.StatusForbidden {
			t.Errorf("%s: status = %d, want %d; body=%s", name, response.Code, http.StatusForbidden, response.Body.String())
		}
	}
	if engine.calls != 0 {
		t.Fatalf("engine ran %d times for refused grants", engine.calls)
	}
}

func validEnergyQueryForSite(siteID string) analyticsmodel.EnergySeriesQuery {
	query := validEnergyQuery()
	query.SiteID = siteID
	return query
}
