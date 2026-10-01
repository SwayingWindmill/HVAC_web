package telemetry

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const mqttSourceSPIFFE = "spiffe://hvac.local/mqtt-telemetry-adapter"
const replaySourceSPIFFE = "spiffe://hvac.local/historical-replay-runner"

type fakeObservationAcceptor struct {
	candidates []ObservationCandidate
	receipt    ObservationReceipt
	err        error
}

func (fake *fakeObservationAcceptor) AcceptObservation(_ context.Context, candidate ObservationCandidate) (ObservationReceipt, error) {
	fake.candidates = append(fake.candidates, candidate)
	return fake.receipt, fake.err
}

type fakeHistoricalObservationAcceptor struct {
	candidates []ObservationCandidate
	receipt    ObservationReceipt
	err        error
}

func (fake *fakeHistoricalObservationAcceptor) AcceptHistoricalObservation(_ context.Context, candidate ObservationCandidate) (ObservationReceipt, error) {
	fake.candidates = append(fake.candidates, candidate)
	return fake.receipt, fake.err
}

func TestParseSourceAuthenticatorJSONRequiresExactSPIFFEAndIntegrationBindings(t *testing.T) {
	authenticator, err := ParseSourceAuthenticatorJSON(`{"` + mqttSourceSPIFFE + `":["` + integrationA + `"]}`)
	if err != nil {
		t.Fatal(err)
	}
	if !authenticator.AllowsSource(mqttSourceSPIFFE, integrationA) || authenticator.AllowsSource(mqttSourceSPIFFE, "018f2e00-6000-7000-8000-000000000099") {
		t.Fatal("source authenticator scope is not exact")
	}
	for _, raw := range []string{
		`{}`,
		`{"https://source.invalid":["` + integrationA + `"]}`,
		`{"spiffe://hvac.local":["` + integrationA + `"]}`,
		`{"spiffe://hvac.local/?scope=all":["` + integrationA + `"]}`,
		`{"spiffe://hvac.local/mqtt-telemetry-adapter#shadow":["` + integrationA + `"]}`,
		`{"spiffe://user@hvac.local/mqtt-telemetry-adapter":["` + integrationA + `"]}`,
		`{"` + mqttSourceSPIFFE + `":["not-a-uuid"]}`,
		`{"` + mqttSourceSPIFFE + `":["` + integrationA + `","` + integrationA + `"]}`,
		`{"` + mqttSourceSPIFFE + `":["` + integrationA + `"]}{}`,
	} {
		if _, err := ParseSourceAuthenticatorJSON(raw); err == nil {
			t.Fatalf("invalid source bindings accepted: %s", raw)
		}
	}
}

func TestSourceModesReuseOneAcceptancePath(t *testing.T) {
	now := time.Date(2026, 7, 24, 2, 0, 5, 0, time.UTC)
	for _, sourcePath := range []SourcePath{SourcePathWebhook, SourcePathPush, SourcePathPoll, SourcePathReconciliation} {
		t.Run(string(sourcePath), func(t *testing.T) {
			acceptor := &fakeObservationAcceptor{receipt: ObservationReceipt{
				ObservationID: eventA, Status: ObservationAccepted, Quality: QualityGood,
				DeviceID: deviceA, BusinessRevision: 2, StateChanged: true, PositionAdvanced: true,
			}}
			handler := NewHandler(ServerConfig{
				ObservationAcceptor: acceptor,
				SourceAuthenticator: NewStaticSourceAuthenticator(map[string][]string{mqttSourceSPIFFE: {integrationA}}),
				Now:                 func() time.Time { return now },
			})
			body := `{"integrationInstanceId":"` + integrationA + `","sourcePath":"` + string(sourcePath) + `","externalEntityType":"DEVICE","externalId":"tb-device-org-a-site-1","telemetryKey":"zone.temperature","value":23.5,"valueType":"NUMBER","unit":"Cel","sampledAt":"2026-07-24T02:00:00Z","sourcePosition":{"partition":"tb-telemetry-0","offset":100,"eventId":"` + eventA + `"}}`
			request := httptest.NewRequest(http.MethodPost, InternalSourceObservationPath, strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.TLS = verifiedTLSState(mqttSourceSPIFFE)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if len(acceptor.candidates) != 1 {
				t.Fatalf("candidates=%#v", acceptor.candidates)
			}
			candidate := acceptor.candidates[0]
			if candidate.SourcePath != sourcePath || candidate.IntegrationInstanceID != integrationA || candidate.ReceivedAt != now || candidate.Position.Offset != 100 {
				t.Fatalf("candidate=%#v", candidate)
			}
		})
	}
}

func TestHistoricalReplayRouteOwnsProvenanceAndEventIdentity(t *testing.T) {
	now := time.Date(2026, 8, 28, 15, 30, 0, 0, time.UTC)
	acceptor := &fakeHistoricalObservationAcceptor{receipt: ObservationReceipt{
		ObservationID: eventA, Status: ObservationAccepted, Quality: QualityGood, DeviceID: deviceA, PositionAdvanced: true,
	}}
	handler := NewHandler(ServerConfig{
		HistoricalObservationAcceptor: acceptor,
		AllowedHistoricalReplaySPIFFE: replaySourceSPIFFE,
		SourceAuthenticator: NewStaticSourceAuthenticator(map[string][]string{replaySourceSPIFFE: {integrationA}}),
		Now:                 func() time.Time { return now },
	})
	body := `{"integrationInstanceId":"` + integrationA + `","replayDatasetId":"01991f00-0000-7000-8000-000000000001","deviceExternalId":"tb-device-org-a-site-1","telemetryKey":"zone.temperature","value":23.5,"valueType":"NUMBER","unit":"Cel","sampledAt":"2026-07-24T02:00:00Z","offset":7}`

	request := httptest.NewRequest(http.MethodPost, InternalHistoricalReplayObservationPath, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.TLS = verifiedTLSState(replaySourceSPIFFE)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(acceptor.candidates) != 1 {
		t.Fatalf("candidates=%#v", acceptor.candidates)
	}
	candidate := acceptor.candidates[0]
	if candidate.SourcePath != SourcePathHistoryReplay || candidate.ExternalEntityType != "DEVICE" || candidate.ExternalID != "tb-device-org-a-site-1" || candidate.ReceivedAt != now || candidate.Position.Offset != 7 {
		t.Fatalf("candidate=%#v", candidate)
	}
	if !strings.HasPrefix(candidate.Position.Partition, "history-replay:01991f00-0000-7000-8000-000000000001:") || !uuidV7Pattern.MatchString(candidate.Position.EventID) {
		t.Fatalf("position=%#v", candidate.Position)
	}

	request = httptest.NewRequest(http.MethodPost, InternalHistoricalReplayObservationPath, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.TLS = verifiedTLSState(replaySourceSPIFFE)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || len(acceptor.candidates) != 2 || acceptor.candidates[1].Position != candidate.Position {
		t.Fatalf("second replay status=%d candidates=%#v", recorder.Code, acceptor.candidates)
	}
}

func TestHistoricalReplayRequiresDedicatedWorkloadIdentity(t *testing.T) {
	acceptor := &fakeHistoricalObservationAcceptor{}
	handler := NewHandler(ServerConfig{
		HistoricalObservationAcceptor: acceptor,
		AllowedHistoricalReplaySPIFFE: replaySourceSPIFFE,
		SourceAuthenticator: NewStaticSourceAuthenticator(map[string][]string{mqttSourceSPIFFE: {integrationA}}),
	})
	body := `{"integrationInstanceId":"` + integrationA + `","replayDatasetId":"01991f00-0000-7000-8000-000000000001","deviceExternalId":"tb-device-org-a-site-1","telemetryKey":"zone.temperature","value":23.5,"valueType":"NUMBER","unit":"Cel","sampledAt":"2026-07-24T02:00:00Z","offset":7}`
	request := httptest.NewRequest(http.MethodPost, InternalHistoricalReplayObservationPath, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.TLS = verifiedTLSState(mqttSourceSPIFFE)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized || !strings.Contains(recorder.Body.String(), "TELEMETRY_SOURCE_IDENTITY_INVALID") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(acceptor.candidates) != 0 {
		t.Fatalf("non-replay workload reached Historical Replay acceptance: %#v", acceptor.candidates)
	}
}

func TestHistoricalReplayCannotEnterThroughLiveSourceRoute(t *testing.T) {
	handler := NewHandler(ServerConfig{
		ObservationAcceptor:  &fakeObservationAcceptor{},
		SourceAuthenticator: NewStaticSourceAuthenticator(map[string][]string{replaySourceSPIFFE: {integrationA}}),
	})
	body := `{"integrationInstanceId":"` + integrationA + `","sourcePath":"HISTORY_REPLAY","externalEntityType":"DEVICE","externalId":"tb-device-org-a-site-1","telemetryKey":"zone.temperature","value":23.5,"valueType":"NUMBER","unit":"Cel","sampledAt":"2026-07-24T02:00:00Z","sourcePosition":{"partition":"replay","offset":7,"eventId":"` + eventA + `"}}`
	request := httptest.NewRequest(http.MethodPost, InternalSourceObservationPath, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.TLS = verifiedTLSState(replaySourceSPIFFE)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "TELEMETRY_SOURCE_REQUEST_INVALID") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestSourceAuthenticationAndScopeFailClosed(t *testing.T) {
	acceptor := &fakeObservationAcceptor{}
	handler := NewHandler(ServerConfig{
		ObservationAcceptor: acceptor,
		SourceAuthenticator: NewStaticSourceAuthenticator(map[string][]string{mqttSourceSPIFFE: {integrationA}}),
	})
	validBody := `{"integrationInstanceId":"` + integrationA + `","sourcePath":"WEBHOOK","externalEntityType":"DEVICE","externalId":"tb-device-org-a-site-1","telemetryKey":"zone.temperature","value":23.5,"valueType":"NUMBER","unit":"Cel","sampledAt":"2026-07-24T02:00:00Z","sourcePosition":{"partition":"tb-telemetry-0","offset":100,"eventId":"` + eventA + `"}}`

	tests := []struct {
		name   string
		peer   string
		body   string
		header string
		status int
		code   string
	}{
		{name: "missing workload", body: validBody, status: http.StatusUnauthorized, code: "TELEMETRY_SOURCE_IDENTITY_INVALID"},
		{name: "wrong workload", peer: "spiffe://hvac.local/legacy-backend", body: validBody, status: http.StatusUnauthorized, code: "TELEMETRY_SOURCE_IDENTITY_INVALID"},
		{name: "wrong integration scope", peer: mqttSourceSPIFFE, body: strings.Replace(validBody, integrationA, "018f2e00-6000-7000-8000-000000000099", 1), status: http.StatusUnauthorized, code: "TELEMETRY_SOURCE_IDENTITY_INVALID"},
		{name: "forged integration header", peer: mqttSourceSPIFFE, body: validBody, header: "X-Integration-Instance-ID", status: http.StatusBadRequest, code: "TELEMETRY_FORGED_IDENTITY_HEADER"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, InternalSourceObservationPath, strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			if test.header != "" {
				request.Header.Set(test.header, integrationA)
			}
			if test.peer != "" {
				request.TLS = verifiedTLSState(test.peer)
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != test.status || !strings.Contains(recorder.Body.String(), test.code) {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
	if len(acceptor.candidates) != 0 {
		t.Fatalf("unauthorized source reached acceptance: %#v", acceptor.candidates)
	}
}

func TestSourceFailsClosedOnMalformedAndDependencyFailure(t *testing.T) {
	authenticator := NewStaticSourceAuthenticator(map[string][]string{mqttSourceSPIFFE: {integrationA}})
	validBody := `{"integrationInstanceId":"` + integrationA + `","sourcePath":"WEBHOOK","externalEntityType":"DEVICE","externalId":"tb-device-org-a-site-1","telemetryKey":"zone.temperature","value":23.5,"valueType":"NUMBER","unit":"Cel","sampledAt":"2026-07-24T02:00:00Z","sourcePosition":{"partition":"tb-telemetry-0","offset":100,"eventId":"` + eventA + `"}}`
	for _, test := range []struct {
		name        string
		acceptor    *fakeObservationAcceptor
		body        string
		contentType string
		status      int
		code        string
	}{
		{name: "malformed", acceptor: &fakeObservationAcceptor{}, body: `{}`, contentType: "application/json", status: http.StatusBadRequest, code: "TELEMETRY_SOURCE_REQUEST_INVALID"},
		{name: "missing content type", acceptor: &fakeObservationAcceptor{}, body: validBody, status: http.StatusBadRequest, code: "TELEMETRY_SOURCE_REQUEST_INVALID"},
		{name: "JSON prefix bypass", acceptor: &fakeObservationAcceptor{}, body: validBody, contentType: "application/jsonp", status: http.StatusBadRequest, code: "TELEMETRY_SOURCE_REQUEST_INVALID"},
		{name: "store unavailable", acceptor: &fakeObservationAcceptor{err: errors.New("postgres unavailable")}, body: validBody, contentType: "application/json", status: http.StatusServiceUnavailable, code: "TELEMETRY_SOURCE_UNAVAILABLE"},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := NewHandler(ServerConfig{ObservationAcceptor: test.acceptor, SourceAuthenticator: authenticator})
			request := httptest.NewRequest(http.MethodPost, InternalSourceObservationPath, strings.NewReader(test.body))
			if test.contentType != "" {
				request.Header.Set("Content-Type", test.contentType)
			}
			request.TLS = verifiedTLSState(mqttSourceSPIFFE)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != test.status || !strings.Contains(recorder.Body.String(), test.code) {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

type fakeCoverageReporter struct {
	reports []CoverageReport
	receipt CoverageReceipt
	err     error
}

func (fake *fakeCoverageReporter) ReportCoverage(_ context.Context, report CoverageReport) (CoverageReceipt, error) {
	fake.reports = append(fake.reports, report)
	return fake.receipt, fake.err
}

func TestSourceCoverageReportsOutageAndRecovery(t *testing.T) {
	now := time.Date(2026, 7, 24, 2, 10, 0, 0, time.UTC)
	authenticator := NewStaticSourceAuthenticator(map[string][]string{mqttSourceSPIFFE: {integrationA}})
	for _, test := range []struct {
		name      string
		body      string
		available bool
		reason    string
	}{
		{name: "outage", body: `{"integrationInstanceId":"` + integrationA + `","externalEntityType":"DEVICE","externalId":"tb-device-org-a-site-1","available":false,"continuousSince":null,"reason":"SOURCE_UNAVAILABLE","sourceRevision":2}`, reason: "SOURCE_UNAVAILABLE"},
		{name: "recovery", body: `{"integrationInstanceId":"` + integrationA + `","externalEntityType":"DEVICE","externalId":"tb-device-org-a-site-1","available":true,"continuousSince":"2026-07-24T02:10:00Z","reason":"","sourceRevision":3}`, available: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			reporter := &fakeCoverageReporter{receipt: CoverageReceipt{Status: "APPLIED", DeviceID: deviceA, BusinessRevision: 3, StateChanged: true}}
			handler := NewHandler(ServerConfig{CoverageReporter: reporter, SourceAuthenticator: authenticator, Now: func() time.Time { return now }})
			request := httptest.NewRequest(http.MethodPost, InternalSourceCoveragePath, strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			request.TLS = verifiedTLSState(mqttSourceSPIFFE)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if len(reporter.reports) != 1 || reporter.reports[0].Available != test.available || string(reporter.reports[0].Reason) != test.reason || reporter.reports[0].ReportedAt != now {
				t.Fatalf("reports=%#v", reporter.reports)
			}
			if test.available && (reporter.reports[0].ContinuousSince == nil || !reporter.reports[0].ContinuousSince.Equal(now)) {
				t.Fatalf("continuousSince=%#v", reporter.reports[0].ContinuousSince)
			}
		})
	}
}

func TestLegacyIngestPathDoesNotExist(t *testing.T) {
	handler := NewHandler(ServerConfig{})
	request := httptest.NewRequest(http.MethodPost, "/api/telemetry/ingest", strings.NewReader(`{}`))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound || !strings.Contains(recorder.Body.String(), "RESOURCE_NOT_FOUND") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
