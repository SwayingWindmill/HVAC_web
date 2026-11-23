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
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
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

// A semantic query whose answer would exceed the row limit is the caller's to narrow: 422, not
// retryable, rather than a 503 the Agent would retry.
func TestHandlerRefusesASemanticQueryTooLargeToAnswer(t *testing.T) {
	now := time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
	harness := newServerHarness(t, now, &engineStub{err: fmt.Errorf("query Cube: %w", analyticsmodel.ErrSemanticRowLimit)})
	query := analyticsmodel.SemanticQuery{
		TenantID: testTenantID, SiteID: testSiteID, View: "equipment_energy",
		Measures: []string{"energy_valid_kwh"}, Dimensions: []string{"equipment_id", "quality"},
		Granularity: analyticsmodel.GranularityHour, Timezone: "Asia/Shanghai",
		From: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC),
	}
	payload, err := json.Marshal(query)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := query.ScopeDigest()
	if err != nil {
		t.Fatal(err)
	}
	grant, err := identitycontext.SignDelegation(harness.signer, identitycontext.DelegationClaims{
		Issuer: testPresenter, Subject: "user-1", SubjectIssuer: "issuer-test", PrincipalID: testPrincipalID,
		ExecutingService: testOperationsPresenter, Audience: testAudience, TenantID: testTenantID,
		Actions: []string{analyticsmodel.EnergySeriesAction}, Scopes: []string{scope}, PolicyRevision: "analytics-policy:1",
		SessionID: "session-test", IssuedAt: now.Add(-time.Second).Unix(), ExpiresAt: now.Add(30 * time.Second).Unix(), TokenID: "token-semantic",
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, SemanticQueryPath, bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Delegation-Grant", grant)
	spiffe, _ := url.Parse(testOperationsPresenter)
	request.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{{URIs: []*url.URL{spiffe}}}}
	response := httptest.NewRecorder()
	harness.handler.ServeHTTP(response, request)

	var problem struct {
		Code      string `json:"code"`
		Retryable bool   `json:"retryable"`
	}
	if err := json.NewDecoder(response.Body).Decode(&problem); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusUnprocessableEntity || problem.Code != "ANALYTICS_RESULT_TOO_LARGE" || problem.Retryable {
		t.Fatalf("status = %d, problem = %+v", response.Code, problem)
	}
}

type enrichmentHistoryEngineStub struct {
	historyEngineStub
	enrichmentResponse analyticsmodel.ReportEnrichmentResponse
	enrichmentErr      error
	enrichmentQuery    analyticsmodel.EnergySeriesQuery
	enrichmentCalls    int
}

func (stub *enrichmentHistoryEngineStub) ReportEnrichment(_ context.Context, query analyticsmodel.EnergySeriesQuery) (analyticsmodel.ReportEnrichmentResponse, error) {
	stub.enrichmentCalls++
	stub.enrichmentQuery = query
	return stub.enrichmentResponse, stub.enrichmentErr
}

func TestHandlerServesReportEnrichmentWithWorkloadGrantAndOptionalInterface(t *testing.T) {
	now := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	query := validEnergyQuery()
	payload, err := json.Marshal(query)
	if err != nil {
		t.Fatal(err)
	}
	harness := newServerHarness(t, now, &engineStub{})
	grant := harness.workloadGrant(t, query, harness.iamSigner, nil)

	// Base historyEngineStub without ReportEnrichmentHistoryEngine fails with 501.
	unsupportedReq := httptest.NewRequest(http.MethodPost, ReportEnrichmentPath, bytes.NewReader(payload))
	unsupportedReq.Header.Set("Content-Type", "application/json")
	unsupportedReq.Header.Set("X-Delegation-Grant", grant)
	spiffe, _ := url.Parse(testReportWorkload)
	unsupportedReq.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{{URIs: []*url.URL{spiffe}}}}
	unsupportedRec := httptest.NewRecorder()
	harness.handler.ServeHTTP(unsupportedRec, unsupportedReq)
	if unsupportedRec.Code != http.StatusNotImplemented {
		t.Fatalf("unsupported status = %d, want 501; body=%s", unsupportedRec.Code, unsupportedRec.Body.String())
	}

	plantCOP := 4.9
	wetBulb := 22.1
	enrichmentEngine := &enrichmentHistoryEngineStub{
		enrichmentResponse: analyticsmodel.ReportEnrichmentResponse{
			LayeredEfficiency: &analyticsmodel.LayeredEfficiency{PlantCOP: &plantCOP},
			ConditionAnchors:  &analyticsmodel.ReportConditionAnchorsSummary{MeanWetBulbC: &wetBulb},
		},
	}
	signer := harness.signer
	iamSigner := harness.iamSigner
	handler := NewHandler(ServerConfig{
		Engine:                            &engineStub{},
		HistoryEngine:                     enrichmentEngine,
		DelegationPublicKey:               signer.Public(),
		WorkloadGrantPublicKey:            iamSigner.Public(),
		WorkloadGrantIssuerSPIFFE:         testIAM,
		AllowedPresenterSPIFFE:            testPresenter,
		AdditionalAllowedPresenterSPIFFEs: []string{testReportWorkload},
		Audience:                          testAudience,
		Now:                               func() time.Time { return now },
	})

	postEnrichment := func(body []byte, grantHeader string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, ReportEnrichmentPath, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if grantHeader != "" {
			req.Header.Set("X-Delegation-Grant", grantHeader)
		}
		req.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{{URIs: []*url.URL{spiffe}}}}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	okRec := postEnrichment(payload, grant)
	if okRec.Code != http.StatusOK || enrichmentEngine.enrichmentCalls != 1 || enrichmentEngine.enrichmentQuery.SiteID != query.SiteID {
		t.Fatalf("status=%d calls=%d query=%#v body=%s", okRec.Code, enrichmentEngine.enrichmentCalls, enrichmentEngine.enrichmentQuery, okRec.Body.String())
	}
	var decoded analyticsmodel.ReportEnrichmentResponse
	if err := json.NewDecoder(okRec.Body).Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.LayeredEfficiency == nil || decoded.LayeredEfficiency.PlantCOP == nil || *decoded.LayeredEfficiency.PlantCOP != 4.9 {
		t.Fatalf("decoded=%#v", decoded)
	}

	// Rejects unknown JSON fields strictly before dispatch.
	unknownRec := postEnrichment([]byte(`{"tenantId":"`+testTenantID+`","siteId":"`+testSiteID+`","extra":1}`), grant)
	if unknownRec.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status = %d, want 400", unknownRec.Code)
	}

	// Rejects tampered query that does not match grant scope.
	tampered := validEnergyQueryForSite("018f1e00-1000-7000-8000-000000000099")
	tamperedPayload, _ := json.Marshal(tampered)
	if rec := postEnrichment(tamperedPayload, grant); rec.Code != http.StatusForbidden {
		t.Fatalf("tampered status = %d, want 403", rec.Code)
	}

	// Engine error surfaces as 503 ANALYTICS_ENGINE_UNAVAILABLE.
	enrichmentEngine.enrichmentErr = errors.New("clickhouse unavailable")
	if rec := postEnrichment(payload, grant); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("engine error status = %d, want 503", rec.Code)
	}
}

type counterAuditEngineStub struct {
	historyEngineStub
	auditResponse telemetryhistorymodel.CounterAuditResponse
	auditErr      error
	auditQuery    telemetryhistorymodel.CounterAuditQuery
	auditCalls    int
}

func (stub *counterAuditEngineStub) QueryCounterAudit(_ context.Context, query telemetryhistorymodel.CounterAuditQuery) (telemetryhistorymodel.CounterAuditResponse, error) {
	stub.auditCalls++
	stub.auditQuery = query
	return stub.auditResponse, stub.auditErr
}

func TestHandlerServesCounterAudit(t *testing.T) {
	now := time.Date(2026, 10, 10, 8, 0, 0, 0, time.UTC)
	from := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	query := telemetryhistorymodel.CounterAuditQuery{
		TenantID: testTenantID,
		SiteID:   testSiteID,
		From:     from,
		To:       to,
		Limit:    50,
	}
	canonical, err := query.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	scope, err := canonical.ScopeDigest()
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(query)
	if err != nil {
		t.Fatal(err)
	}

	harness := newServerHarness(t, now, &engineStub{})
	grant, err := identitycontext.SignDelegation(harness.signer, identitycontext.DelegationClaims{
		Issuer:           testPresenter,
		Subject:          testPrincipalID,
		SubjectIssuer:    "spiffe://hvac.local",
		PrincipalID:      testPrincipalID,
		ExecutingService: testPresenter,
		Audience:         testAudience,
		TenantID:         testTenantID,
		Actions:          []string{analyticsmodel.EnergySeriesAction},
		Scopes:           []string{scope},
		PolicyRevision:   "analytics-policy:1",
		SessionID:        "session-test",
		IssuedAt:         now.Add(-time.Second).Unix(),
		ExpiresAt:        now.Add(30 * time.Second).Unix(),
		TokenID:          "token-counter-audit",
	})
	if err != nil {
		t.Fatal(err)
	}
	spiffe, _ := url.Parse(testPresenter)

	// Base historyEngineStub without CounterAuditHistoryEngine returns 501.
	unsupportedReq := httptest.NewRequest(http.MethodPost, CounterAuditPath, bytes.NewReader(payload))
	unsupportedReq.Header.Set("Content-Type", "application/json")
	unsupportedReq.Header.Set("X-Delegation-Grant", grant)
	unsupportedReq.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{{URIs: []*url.URL{spiffe}}}}
	unsupportedRec := httptest.NewRecorder()
	harness.handler.ServeHTTP(unsupportedRec, unsupportedReq)
	if unsupportedRec.Code != http.StatusNotImplemented {
		t.Fatalf("unsupported status = %d, want 501; body=%s", unsupportedRec.Code, unsupportedRec.Body.String())
	}

	auditEngine := &counterAuditEngineStub{
		auditResponse: telemetryhistorymodel.CounterAuditResponse{
			SiteID:    testSiteID,
			From:      from,
			To:        to,
			Summary:   telemetryhistorymodel.CounterAuditSummary{TotalSamples: 10, AnomalyCount: 1, ResetCount: 1},
			Items:     []telemetryhistorymodel.CounterAuditItem{},
			Truncated: false,
		},
	}
	handler := NewHandler(ServerConfig{
		Engine:                 &engineStub{},
		HistoryEngine:          auditEngine,
		DelegationPublicKey:    harness.signer.Public(),
		AllowedPresenterSPIFFE: testPresenter,
		Audience:               testAudience,
		Now:                    func() time.Time { return now },
	})

	postAudit := func(body []byte, grantHeader string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, CounterAuditPath, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if grantHeader != "" {
			req.Header.Set("X-Delegation-Grant", grantHeader)
		}
		req.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{{URIs: []*url.URL{spiffe}}}}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	okRec := postAudit(payload, grant)
	if okRec.Code != http.StatusOK || auditEngine.auditCalls != 1 || auditEngine.auditQuery.SiteID != testSiteID {
		t.Fatalf("status=%d calls=%d query=%#v body=%s", okRec.Code, auditEngine.auditCalls, auditEngine.auditQuery, okRec.Body.String())
	}
	if cacheControl := okRec.Header().Get("Cache-Control"); cacheControl != "private, no-store" {
		t.Fatalf("Cache-Control = %q, want private, no-store", cacheControl)
	}

	// Rejects unknown JSON fields strictly before dispatch.
	unknownRec := postAudit([]byte(`{"tenantId":"`+testTenantID+`","siteId":"`+testSiteID+`","extra":1}`), grant)
	if unknownRec.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status = %d, want 400", unknownRec.Code)
	}

	// Rejects tampered query that does not match grant scope.
	tampered := query
	tampered.Limit = 99
	tamperedPayload, _ := json.Marshal(tampered)
	if rec := postAudit(tamperedPayload, grant); rec.Code != http.StatusForbidden {
		t.Fatalf("tampered status = %d, want 403", rec.Code)
	}

	// Engine error surfaces as 503.
	auditEngine.auditErr = errors.New("clickhouse unavailable")
	if rec := postAudit(payload, grant); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("engine error status = %d, want 503", rec.Code)
	}
}

type lockedLogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestHandlerLogsClassifiedFailureOn503(t *testing.T) {
	now := time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
	signer, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	logBuf := &lockedLogBuffer{}
	engine := &engineStub{err: fmt.Errorf("cube query continue wait timed out: %w", context.DeadlineExceeded)}
	handler := NewHandler(ServerConfig{
		Engine:                 engine,
		HistoryEngine:          &historyEngineStub{},
		DelegationPublicKey:    signer.Public(),
		AllowedPresenterSPIFFE: testPresenter,
		Audience:               testAudience,
		Logger:                 slog.New(slog.NewJSONHandler(logBuf, nil)),
		Now:                    func() time.Time { return now },
	})
	harness := serverHarness{handler: handler, signer: signer, now: now}
	rec := harness.serve(t, validEnergyQuery(), nil, analyticsmodel.EnergySeriesAction)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	logged := logBuf.String()
	if !strings.Contains(logged, "analytics_energy_series_failed") || !strings.Contains(logged, "continue_wait_timeout") {
		t.Fatalf("logs = %s", logged)
	}
}
