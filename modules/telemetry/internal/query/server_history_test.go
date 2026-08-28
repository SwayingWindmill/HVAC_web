package query

import (
	"bytes"
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
)

func TestDeviceHistoryRouteVerifiesExactGrantAndPreservesTypedObservation(t *testing.T) {
	now := time.Date(2026, 8, 19, 6, 0, 0, 0, time.UTC)
	query := telemetryhistorymodel.DeviceHistoryQuery{
		TenantID: testTenantID, SiteID: testSiteID, DeviceID: "018f1e00-4000-7000-8000-000000000001",
		Keys: []string{"zone.mode"}, From: now.Add(-time.Hour), To: now, PageSize: 100,
	}
	watermark := now.Add(-time.Minute)
	historyEngine := &historyEngineStub{response: telemetryhistorymodel.DeviceHistoryResponse{
		SchemaVersion: 2, TenantID: query.TenantID, SiteID: query.SiteID, DeviceID: query.DeviceID,
		Observations: []telemetryhistorymodel.DeviceHistoryObservation{{
			ObservationID: "018f1e00-8000-7000-8000-000000000001", TelemetryKey: "zone.mode",
			PointID: "018f1e00-5000-7000-8000-000000000001", PointType: telemetryhistorymodel.PointTypeState, PointRevision: 7,
			SampledAt: now.Add(-30 * time.Minute), ReceivedAt: now.Add(-30*time.Minute + time.Second),
			Acceptance: telemetryhistorymodel.AcceptanceOutOfOrder, ValueType: telemetryhistorymodel.ValueTypeString, Value: json.RawMessage(`"COOL"`),
			Quality: telemetryhistorymodel.QualityGood, QualityReasons: []string{},
			SourcePosition: telemetryhistorymodel.SourcePosition{Partition: "mqtt:gw:device:zone.mode", Offset: 42, EventID: "018f1e00-8000-7000-8000-000000000001"},
		}},
		Metadata: telemetryhistorymodel.DeviceHistoryMetadata{RequestedFrom: query.From, RequestedTo: query.To, ProjectionWatermark: &watermark, PageSize: query.PageSize, ReturnedObservations: 1},
	}}
	signer := newHistorySigner(t)
	handler := NewHandler(ServerConfig{Engine: &engineStub{response: analyticsmodel.EnergySeriesResponse{}}, HistoryEngine: historyEngine, DelegationPublicKey: signer.Public(), AllowedPresenterSPIFFE: testPresenter, Audience: testAudience, Now: func() time.Time { return now }})
	grant := signHistoryGrant(t, signer, now, query.TenantID, telemetryhistorymodel.DeviceHistoryAction, query.ScopeDigest)

	payload, _ := json.Marshal(query)
	request := historyRequest(DeviceHistoryPath, payload, grant)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || historyEngine.calls != 1 {
		t.Fatalf("status=%d calls=%d body=%s", recorder.Code, historyEngine.calls, recorder.Body.String())
	}
	var response telemetryhistorymodel.DeviceHistoryResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if len(response.Observations) != 1 || response.Observations[0].Acceptance != telemetryhistorymodel.AcceptanceOutOfOrder || response.Observations[0].PointRevision != 7 || response.Metadata.ProjectionWatermark == nil {
		t.Fatalf("response=%#v", response)
	}
}

func TestDeviceHistoryAggregateRouteBindsTimezoneQualityAndPointType(t *testing.T) {
	now := time.Date(2026, 8, 19, 6, 0, 0, 0, time.UTC)
	query := telemetryhistorymodel.DeviceHistoryAggregateQuery{
		TenantID: testTenantID, SiteID: testSiteID, DeviceID: "018f1e00-4000-7000-8000-000000000001",
		Keys: []string{"zone.temperature"}, From: now.Add(-24 * time.Hour), To: now,
		Granularity: telemetryhistorymodel.AggregateGranularityDay, Timezone: "Asia/Singapore", QualityPolicy: telemetryhistorymodel.AggregateQualityValidOnly,
	}
	watermark := now.Add(-time.Minute)
	historyEngine := &historyEngineStub{aggregateResponse: telemetryhistorymodel.DeviceHistoryAggregateResponse{
		SchemaVersion: 1, TenantID: query.TenantID, SiteID: query.SiteID, DeviceID: query.DeviceID,
		Buckets: []telemetryhistorymodel.DeviceHistoryAggregateBucket{{
			TelemetryKey: "zone.temperature", PointID: "018f1e00-5000-7000-8000-000000000001", PointRevision: 3,
			PointType: telemetryhistorymodel.PointTypeTelemetry, PeriodStart: query.From, PeriodEnd: query.To,
			Quality: telemetryhistorymodel.AggregateQualitySummary{Good: 4}, Completeness: 1,
			Gauge: &telemetryhistorymodel.GaugeAggregate{Average: 22, Minimum: 20, Maximum: 24, First: 21, Last: 23, SampleCount: 4},
		}},
		Metadata: telemetryhistorymodel.DeviceHistoryAggregateMetadata{RequestedFrom: query.From, RequestedTo: query.To, Granularity: query.Granularity, Timezone: query.Timezone, QualityPolicy: query.QualityPolicy, ProjectionWatermark: &watermark, ReturnedBuckets: 1},
	}}
	signer := newHistorySigner(t)
	handler := NewHandler(ServerConfig{Engine: &engineStub{}, HistoryEngine: historyEngine, DelegationPublicKey: signer.Public(), AllowedPresenterSPIFFE: testPresenter, Audience: testAudience, Now: func() time.Time { return now }})
	grant := signHistoryGrant(t, signer, now, query.TenantID, telemetryhistorymodel.DeviceHistoryAggregateAction, query.ScopeDigest)

	payload, _ := json.Marshal(query)
	request := historyRequest(DeviceHistoryAggregatePath, payload, grant)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || historyEngine.aggregateCalls != 1 {
		t.Fatalf("status=%d calls=%d body=%s", recorder.Code, historyEngine.aggregateCalls, recorder.Body.String())
	}
	if historyEngine.aggregateQuery.Timezone != "Asia/Singapore" || historyEngine.aggregateQuery.QualityPolicy != telemetryhistorymodel.AggregateQualityValidOnly {
		t.Fatalf("query=%#v", historyEngine.aggregateQuery)
	}
}

func newHistorySigner(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	signer, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

func signHistoryGrant(t *testing.T, signer *ecdsa.PrivateKey, now time.Time, tenantID, action string, digest func() (string, error)) string {
	t.Helper()
	scope, err := digest()
	if err != nil {
		t.Fatal(err)
	}
	grant, err := identitycontext.SignDelegation(signer, identitycontext.DelegationClaims{
		Issuer: testPresenter, Subject: "user-1", SubjectIssuer: "issuer-test", PrincipalID: testPrincipalID,
		ExecutingService: testPresenter, Audience: testAudience, TenantID: tenantID,
		Actions: []string{action}, Scopes: []string{scope}, PolicyRevision: "telemetry-access:2", SessionID: "session-test",
		IssuedAt: now.Add(-time.Second).Unix(), ExpiresAt: now.Add(30 * time.Second).Unix(), TokenID: "token-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	return grant
}

func historyRequest(path string, payload []byte, grant string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Delegation-Grant", grant)
	spiffe, _ := url.Parse(testPresenter)
	request.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{{URIs: []*url.URL{spiffe}}}}
	return request
}
