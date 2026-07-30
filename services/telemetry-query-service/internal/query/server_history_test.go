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

func TestDeviceHistoryRouteVerifiesExactGrantAndPreservesMetadata(t *testing.T) {
	now := time.Date(2026, 7, 30, 6, 0, 0, 0, time.UTC)
	from := now.Add(-6 * time.Hour)
	query := telemetryhistorymodel.DeviceHistoryQuery{
		ActingOrganizationID: testOrganizationID,
		OwningOrganizationID: testOrganizationID,
		SiteID:               testSiteID,
		DeviceID:             "018f1e00-4000-7000-8000-000000000001",
		Keys:                 []string{"zone.temperature"},
		From:                 from,
		To:                   now,
		MaxPointsPerKey:      100,
	}
	unit := "Cel"
	watermark := now
	historyEngine := &historyEngineStub{response: telemetryhistorymodel.DeviceHistoryResponse{
		SchemaVersion:        1,
		OwningOrganizationID: query.OwningOrganizationID,
		SiteID:               query.SiteID,
		DeviceID:             query.DeviceID,
		Series: []telemetryhistorymodel.DeviceHistorySeries{{
			Key: query.Keys[0],
			Points: []telemetryhistorymodel.DeviceHistoryPoint{{
				ObservationID:  "018f1e00-8000-7000-8000-000000000001",
				SampledAt:      from.Add(time.Hour),
				ReceivedAt:     from.Add(time.Hour + time.Second),
				Value:          22.5,
				Unit:           &unit,
				Quality:        telemetryhistorymodel.QualityGood,
				QualityReasons: []string{},
				Revision:       7,
			}},
		}},
		Metadata: telemetryhistorymodel.DeviceHistoryMetadata{
			RequestedFrom:   query.From,
			RequestedTo:     query.To,
			DataWatermark:   &watermark,
			DatasetRevision: "telemetry-history:v1:7",
			Partial:         false,
			MaxPointsPerKey: query.MaxPointsPerKey,
			ReturnedPoints:  1,
			TruncatedKeys:   []string{},
		},
	}}
	signer, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(ServerConfig{
		Engine:                 &engineStub{response: analyticsmodel.EnergySeriesResponse{}},
		HistoryEngine:          historyEngine,
		DelegationPublicKey:    signer.Public(),
		AllowedPresenterSPIFFE: testPresenter,
		Audience:               testAudience,
		Now:                    func() time.Time { return now },
	})
	scope, err := query.ScopeDigest()
	if err != nil {
		t.Fatal(err)
	}
	grant, err := identitycontext.SignDelegation(signer, identitycontext.DelegationClaims{
		Issuer:               testPresenter,
		Subject:              "user-1",
		SubjectIssuer:        "issuer-test",
		PrincipalID:          testPrincipalID,
		ExecutingService:     testPresenter,
		Audience:             testAudience,
		ActingOrganizationID: query.ActingOrganizationID,
		Actions:              []string{telemetryhistorymodel.DeviceHistoryAction},
		Scopes:               []string{scope},
		PolicyRevision:       "telemetry-access:2",
		SessionID:            "session-test",
		IssuedAt:             now.Add(-time.Second).Unix(),
		ExpiresAt:            now.Add(30 * time.Second).Unix(),
		TokenID:              "token-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(query)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, DeviceHistoryPath, bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Delegation-Grant", grant)
	spiffe, _ := url.Parse(testPresenter)
	request.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{{URIs: []*url.URL{spiffe}}}}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", recorder.Code, recorder.Body.String())
	}
	if historyEngine.calls != 1 || historyEngine.query.DeviceID != query.DeviceID || len(historyEngine.query.Keys) != 1 {
		t.Fatalf("history engine call = %#v", historyEngine)
	}
	if recorder.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("cache control = %q", recorder.Header().Get("Cache-Control"))
	}
	var response telemetryhistorymodel.DeviceHistoryResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Metadata.DatasetRevision != "telemetry-history:v1:7" || response.Metadata.ReturnedPoints != 1 {
		t.Fatalf("response = %#v", response)
	}
}

func TestDeviceHistoryRouteRejectsGrantForDifferentRange(t *testing.T) {
	now := time.Date(2026, 7, 30, 6, 0, 0, 0, time.UTC)
	query := telemetryhistorymodel.DeviceHistoryQuery{
		ActingOrganizationID: testOrganizationID,
		OwningOrganizationID: testOrganizationID,
		SiteID:               testSiteID,
		DeviceID:             "018f1e00-4000-7000-8000-000000000001",
		Keys:                 []string{"zone.temperature"},
		From:                 now.Add(-time.Hour),
		To:                   now,
		MaxPointsPerKey:      100,
	}
	grantQuery := query
	grantQuery.From = query.From.Add(-time.Hour)
	signer, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	historyEngine := &historyEngineStub{}
	handler := NewHandler(ServerConfig{
		Engine:                 &engineStub{},
		HistoryEngine:          historyEngine,
		DelegationPublicKey:    signer.Public(),
		AllowedPresenterSPIFFE: testPresenter,
		Audience:               testAudience,
		Now:                    func() time.Time { return now },
	})
	scope, err := grantQuery.ScopeDigest()
	if err != nil {
		t.Fatal(err)
	}
	grant, err := identitycontext.SignDelegation(signer, identitycontext.DelegationClaims{
		Issuer: testPresenter, Subject: "user-1", SubjectIssuer: "issuer-test", PrincipalID: testPrincipalID,
		ExecutingService: testPresenter, Audience: testAudience, ActingOrganizationID: query.ActingOrganizationID,
		Actions: []string{telemetryhistorymodel.DeviceHistoryAction}, Scopes: []string{scope}, PolicyRevision: "telemetry-access:2",
		SessionID: "session-test", IssuedAt: now.Add(-time.Second).Unix(), ExpiresAt: now.Add(30 * time.Second).Unix(), TokenID: "token-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(query)
	request := httptest.NewRequest(http.MethodPost, DeviceHistoryPath, bytes.NewReader(payload))
	request.Header.Set("X-Delegation-Grant", grant)
	spiffe, _ := url.Parse(testPresenter)
	request.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{{URIs: []*url.URL{spiffe}}}}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden || historyEngine.calls != 0 {
		t.Fatalf("status = %d; calls=%d; body=%s", recorder.Code, historyEngine.calls, recorder.Body.String())
	}
}
