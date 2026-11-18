package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/limitpolicy"
	"github.com/quanlaihe/hvac-web/libs/observability"
)

const mqttSourceSPIFFE = "spiffe://hvac.local/mqtt-telemetry-adapter"
const replaySourceSPIFFE = "spiffe://hvac.local/historical-replay-runner"

// resolvedIdentityJSON is the Device and Point Connectivity resolved for CHILLER-01/zone.temperature.
const resolvedIdentityJSON = `"device":{"tenantId":"` + ingestTenantA + `","siteId":"` + siteA + `","deviceId":"` + deviceA + `"},` +
	`"point":{"pointId":"018f2e00-3100-7000-8000-000000000001","sensorId":null,"pointType":"TELEMETRY","valueType":"NUMBER","unit":"Cel","counterDecreaseMode":null,"counterRolloverModulus":null,"pointRevision":3}`

type fakeObservationAcceptor struct {
	batches    [][]ObservationCandidate
	candidates []ObservationCandidate
	receipts   []ObservationReceipt
	receiptFn  func([]ObservationCandidate) ([]ObservationReceipt, error)
	err        error
}

func (fake *fakeObservationAcceptor) AcceptObservationBatch(_ context.Context, candidates []ObservationCandidate) ([]ObservationReceipt, error) {
	copied := append([]ObservationCandidate(nil), candidates...)
	fake.batches = append(fake.batches, copied)
	fake.candidates = append(fake.candidates, copied...)
	if fake.receiptFn != nil {
		return fake.receiptFn(copied)
	}
	if fake.err != nil {
		return nil, fake.err
	}
	if len(fake.receipts) > 0 {
		return append([]ObservationReceipt(nil), fake.receipts...), nil
	}
	return make([]ObservationReceipt, len(candidates)), nil
}

type fakeHistoricalObservationAcceptor struct {
	candidates []ObservationCandidate
	batches    [][]ObservationCandidate
	receipt    ObservationReceipt
	receipts   []ObservationReceipt
	err        error
}

func (fake *fakeHistoricalObservationAcceptor) AcceptHistoricalObservation(_ context.Context, candidate ObservationCandidate) (ObservationReceipt, error) {
	fake.candidates = append(fake.candidates, candidate)
	return fake.receipt, fake.err
}

func (fake *fakeHistoricalObservationAcceptor) AcceptHistoricalObservationBatch(_ context.Context, candidates []ObservationCandidate) ([]ObservationReceipt, error) {
	copied := append([]ObservationCandidate(nil), candidates...)
	fake.batches = append(fake.batches, copied)
	fake.candidates = append(fake.candidates, copied...)
	if fake.err != nil {
		return nil, fake.err
	}
	if len(fake.receipts) > 0 {
		return append([]ObservationReceipt(nil), fake.receipts...), nil
	}
	return make([]ObservationReceipt, len(candidates)), nil
}

func TestSourceModesReuseOneAcceptancePath(t *testing.T) {
	now := time.Date(2026, 7, 24, 2, 0, 5, 0, time.UTC)
	for _, sourcePath := range []SourcePath{SourcePathWebhook, SourcePathPush, SourcePathPoll, SourcePathReconciliation} {
		t.Run(string(sourcePath), func(t *testing.T) {
			acceptor := &fakeObservationAcceptor{receipts: []ObservationReceipt{{
				ObservationID: eventA, Status: ObservationAccepted, Quality: QualityGood,
				DeviceID: deviceA, BusinessRevision: 2, StateChanged: true, PositionAdvanced: true,
			}}}
			handler := NewHandler(ServerConfig{
				ObservationAcceptor: acceptor,
				AllowedSourceSPIFFE: mqttSourceSPIFFE,
				Now:                 func() time.Time { return now },
			})
			item := `{"sourceId":"` + sourceA + `","sourcePath":"` + string(sourcePath) + `","externalEntityType":"DEVICE","externalId":"CHILLER-01",` + resolvedIdentityJSON + `,"telemetryKey":"zone.temperature","value":23.5,"valueType":"NUMBER","unit":"Cel","sampledAt":"2026-07-24T02:00:00Z","sourcePosition":{"partition":"tb-telemetry-0","offset":100,"eventId":"` + eventA + `"}}`
			request := httptest.NewRequest(http.MethodPost, InternalSourceObservationBatchPath, strings.NewReader(`{"observations":[`+item+`]}`))
			request.Header.Set("Content-Type", "application/json")
			request.TLS = verifiedTLSState(mqttSourceSPIFFE)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			var response sourceObservationBatchResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if len(response.Receipts) != 1 || response.Receipts[0].ObservationID != eventA || response.Receipts[0].BusinessRevision != 2 {
				t.Fatalf("response=%#v", response)
			}
			if len(acceptor.candidates) != 1 {
				t.Fatalf("candidates=%#v", acceptor.candidates)
			}
			candidate := acceptor.candidates[0]
			if candidate.SourcePath != sourcePath || candidate.SourceID != sourceA || *candidate.Device != (ResolvedDevice{TenantID: ingestTenantA, SiteID: siteA, DeviceID: deviceA}) || candidate.Point.PointRevision != 3 || candidate.ReceivedAt != now || candidate.Position.Offset != 100 {
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
		Now:                           func() time.Time { return now },
	})
	body := `{"replayDatasetId":"01991f00-0000-7000-8000-000000000001","deviceId":"` + deviceA + `","telemetryKey":"zone.temperature","value":23.5,"valueType":"NUMBER","unit":"Cel","sampledAt":"2026-07-24T02:00:00Z","offset":7}`

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
	if candidate.SourcePath != SourcePathHistoryReplay || candidate.ExternalEntityType != "DEVICE" || candidate.ExternalID != deviceA || candidate.SourceID != historicalReplaySourceID || candidate.ReceivedAt != now || candidate.Position.Offset != 7 {
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
		AllowedSourceSPIFFE:           mqttSourceSPIFFE,
	})
	body := `{"replayDatasetId":"01991f00-0000-7000-8000-000000000001","deviceId":"` + deviceA + `","telemetryKey":"zone.temperature","value":23.5,"valueType":"NUMBER","unit":"Cel","sampledAt":"2026-07-24T02:00:00Z","offset":7}`
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
		ObservationAcceptor: &fakeObservationAcceptor{},
		AllowedSourceSPIFFE: mqttSourceSPIFFE,
	})
	body := `{"observations":[{"sourceId":"` + sourceA + `","sourcePath":"HISTORY_REPLAY","externalEntityType":"DEVICE","externalId":"CHILLER-01",` + resolvedIdentityJSON + `,"telemetryKey":"zone.temperature","value":23.5,"valueType":"NUMBER","unit":"Cel","sampledAt":"2026-07-24T02:00:00Z","sourcePosition":{"partition":"replay","offset":7,"eventId":"` + eventA + `"}}]}`
	request := httptest.NewRequest(http.MethodPost, InternalSourceObservationBatchPath, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.TLS = verifiedTLSState(mqttSourceSPIFFE)
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
		AllowedSourceSPIFFE: mqttSourceSPIFFE,
	})
	validBody := `{"observations":[{"sourceId":"` + sourceA + `","sourcePath":"WEBHOOK","externalEntityType":"DEVICE","externalId":"CHILLER-01",` + resolvedIdentityJSON + `,"telemetryKey":"zone.temperature","value":23.5,"valueType":"NUMBER","unit":"Cel","sampledAt":"2026-07-24T02:00:00Z","sourcePosition":{"partition":"tb-telemetry-0","offset":100,"eventId":"` + eventA + `"}}]}`

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
		{name: "replay workload", peer: replaySourceSPIFFE, body: validBody, status: http.StatusUnauthorized, code: "TELEMETRY_SOURCE_IDENTITY_INVALID"},
		{name: "forged integration header", peer: mqttSourceSPIFFE, body: validBody, header: "X-Integration-Instance-ID", status: http.StatusBadRequest, code: "TELEMETRY_FORGED_IDENTITY_HEADER"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, InternalSourceObservationBatchPath, strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			if test.header != "" {
				request.Header.Set(test.header, sourceA)
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
	validItem := `{"sourceId":"` + sourceA + `","sourcePath":"WEBHOOK","externalEntityType":"DEVICE","externalId":"CHILLER-01",` + resolvedIdentityJSON + `,"telemetryKey":"zone.temperature","value":23.5,"valueType":"NUMBER","unit":"Cel","sampledAt":"2026-07-24T02:00:00Z","sourcePosition":{"partition":"tb-telemetry-0","offset":100,"eventId":"` + eventA + `"}}`
	validBody := `{"observations":[` + validItem + `]}`
	tooManyItems := make([]string, MaxObservationBatchSize+1)
	for index := range tooManyItems {
		tooManyItems[index] = validItem
	}
	oversizedBatchBody := `{"observations":[` + strings.Join(tooManyItems, ",") + `]}`
	for _, test := range []struct {
		name        string
		acceptor    *fakeObservationAcceptor
		body        string
		contentType string
		status      int
		code        string
	}{
		{name: "malformed", acceptor: &fakeObservationAcceptor{}, body: `{}`, contentType: "application/json", status: http.StatusBadRequest, code: "TELEMETRY_SOURCE_REQUEST_INVALID"},
		{name: "empty batch", acceptor: &fakeObservationAcceptor{}, body: `{"observations":[]}`, contentType: "application/json", status: http.StatusBadRequest, code: "TELEMETRY_SOURCE_REQUEST_INVALID"},
		{name: "over max batch size", acceptor: &fakeObservationAcceptor{}, body: oversizedBatchBody, contentType: "application/json", status: http.StatusBadRequest, code: "TELEMETRY_SOURCE_REQUEST_INVALID"},
		{name: "unknown outer field", acceptor: &fakeObservationAcceptor{}, body: `{"observations":[` + validItem + `],"extra":1}`, contentType: "application/json", status: http.StatusBadRequest, code: "TELEMETRY_SOURCE_REQUEST_INVALID"},
		{name: "missing content type", acceptor: &fakeObservationAcceptor{}, body: validBody, status: http.StatusBadRequest, code: "TELEMETRY_SOURCE_REQUEST_INVALID"},
		{name: "JSON prefix bypass", acceptor: &fakeObservationAcceptor{}, body: validBody, contentType: "application/jsonp", status: http.StatusBadRequest, code: "TELEMETRY_SOURCE_REQUEST_INVALID"},
		{name: "store unavailable", acceptor: &fakeObservationAcceptor{err: errors.New("postgres unavailable")}, body: validBody, contentType: "application/json", status: http.StatusServiceUnavailable, code: "TELEMETRY_SOURCE_UNAVAILABLE"},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := NewHandler(ServerConfig{ObservationAcceptor: test.acceptor, AllowedSourceSPIFFE: mqttSourceSPIFFE})
			request := httptest.NewRequest(http.MethodPost, InternalSourceObservationBatchPath, strings.NewReader(test.body))
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

type recordingRateCounter struct {
	calls []string
	deny  map[string]bool
}

func (counter *recordingRateCounter) Allow(_ context.Context, key string, _ time.Duration, _ int) (bool, error) {
	counter.calls = append(counter.calls, key)
	return !counter.deny[key], nil
}

func TestSourceBatchChecksRateLimitOncePerUniqueSourceAndPreservesAlignment(t *testing.T) {
	now := time.Date(2026, 7, 24, 2, 0, 5, 0, time.UTC)
	counter := &recordingRateCounter{deny: map[string]bool{}}
	limiter := limitpolicy.NewLimiter(counter, &limitpolicy.Policy{
		Version: 1,
		Limits:  []limitpolicy.Limit{{Dimension: limitpolicy.DimensionTelemetryIngest, Window: time.Minute, Burst: 1, FailClosed: true}},
	})
	registry := observability.NewRegistry()
	acceptor := &fakeObservationAcceptor{
		receiptFn: func(candidates []ObservationCandidate) ([]ObservationReceipt, error) {
			receipts := make([]ObservationReceipt, len(candidates))
			for index, candidate := range candidates {
				if index == 1 {
					receipts[index] = ObservationReceipt{
						ObservationID: candidate.Position.EventID, Status: ObservationQuarantined, Quality: QualityInvalid,
						QualityReasons: []QualityReason{}, QuarantineReason: QuarantineMappingNotFound, PositionAdvanced: true,
					}
					continue
				}
				receipts[index] = ObservationReceipt{
					ObservationID: candidate.Position.EventID, Status: ObservationAccepted, Quality: QualityGood,
					QualityReasons: []QualityReason{}, DeviceID: deviceA, BusinessRevision: 4, StateChanged: true, PositionAdvanced: true,
				}
			}
			return receipts, nil
		},
	}
	handler := NewHandler(ServerConfig{
		ObservationAcceptor: acceptor,
		AllowedSourceSPIFFE: mqttSourceSPIFFE,
		Metrics:             registry,
		RateLimiter:         limiter,
		Now:                 func() time.Time { return now },
	})

	source2 := "EG8200-COMMERCIAL-002"
	item1 := `{"sourceId":"` + sourceA + `","sourcePath":"PUSH","externalEntityType":"DEVICE","externalId":"CHILLER-01",` + resolvedIdentityJSON + `,"telemetryKey":"zone.temperature","value":23.5,"valueType":"NUMBER","unit":"Cel","sampledAt":"2026-07-24T02:00:00Z","sourcePosition":{"partition":"p0","offset":1,"eventId":"018f2e00-8000-7000-8000-000000000001"}}`
	item2 := `{"sourceId":"` + sourceA + `","sourcePath":"PUSH","externalEntityType":"DEVICE","externalId":"CHILLER-99","device":null,"point":null,"telemetryKey":"zone.temperature","value":24.0,"valueType":"NUMBER","unit":"Cel","sampledAt":"2026-07-24T02:00:01Z","sourcePosition":{"partition":"p0","offset":2,"eventId":"018f2e00-8000-7000-8000-000000000002"}}`
	item3 := `{"sourceId":"` + source2 + `","sourcePath":"PUSH","externalEntityType":"DEVICE","externalId":"CHILLER-01",` + resolvedIdentityJSON + `,"telemetryKey":"zone.humidity","value":55.0,"valueType":"NUMBER","unit":"Cel","sampledAt":"2026-07-24T02:00:02Z","sourcePosition":{"partition":"p1","offset":1,"eventId":"018f2e00-8000-7000-8000-000000000003"}}`
	body := `{"observations":[` + item1 + `,` + item2 + `,` + item3 + `]}`

	request := httptest.NewRequest(http.MethodPost, InternalSourceObservationBatchPath, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.TLS = verifiedTLSState(mqttSourceSPIFFE)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(counter.calls) != 2 || counter.calls[0] != "telemetry-ingest:"+sourceA || counter.calls[1] != "telemetry-ingest:"+source2 {
		t.Fatalf("rate limiter calls=%#v, want once per unique sourceId", counter.calls)
	}
	var response sourceObservationBatchResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Receipts) != 3 ||
		response.Receipts[0].ObservationID != "018f2e00-8000-7000-8000-000000000001" || response.Receipts[0].Status != ObservationAccepted ||
		response.Receipts[1].ObservationID != "018f2e00-8000-7000-8000-000000000002" || response.Receipts[1].Status != ObservationQuarantined ||
		response.Receipts[2].ObservationID != "018f2e00-8000-7000-8000-000000000003" || response.Receipts[2].Status != ObservationAccepted {
		t.Fatalf("misaligned receipts=%#v", response.Receipts)
	}

	metricsRec := httptest.NewRecorder()
	registry.Handler().ServeHTTP(metricsRec, httptest.NewRequest("GET", "/metrics", nil))
	metricsBody := metricsRec.Body.String()
	if !strings.Contains(metricsBody, `hvac_s2_ingest_records_total{outcome="success",reason_family="none"} 2`) ||
		!strings.Contains(metricsBody, `hvac_s2_ingest_records_total{outcome="rejected",reason_family="scope"} 1`) ||
		!strings.Contains(metricsBody, `hvac_s2_quarantine_records_total{outcome="rejected",reason_family="scope"} 1`) {
		t.Fatalf("unexpected batch metrics:\n%s", metricsBody)
	}

	counter.deny["telemetry-ingest:"+source2] = true
	limitedReq := httptest.NewRequest(http.MethodPost, InternalSourceObservationBatchPath, strings.NewReader(body))
	limitedReq.Header.Set("Content-Type", "application/json")
	limitedReq.TLS = verifiedTLSState(mqttSourceSPIFFE)
	limitedRec := httptest.NewRecorder()
	handler.ServeHTTP(limitedRec, limitedReq)
	if limitedRec.Code != http.StatusTooManyRequests || !strings.Contains(limitedRec.Body.String(), "TELEMETRY_INGEST_RATE_LIMITED") {
		t.Fatalf("rate limited status=%d body=%s", limitedRec.Code, limitedRec.Body.String())
	}
	if len(acceptor.batches) != 1 {
		t.Fatalf("rate limited batch still reached acceptor: %d", len(acceptor.batches))
	}
}

func TestSourceBatchAllowsLargeBodyAboveLegacy96KiBLimit(t *testing.T) {
	items := make([]string, 300)
	for index := range items {
		items[index] = fmt.Sprintf(
			`{"sourceId":"%s","sourcePath":"PUSH","externalEntityType":"DEVICE","externalId":"CHILLER-01",%s,"telemetryKey":"zone.temperature","value":23.5,"valueType":"NUMBER","unit":"Cel","sampledAt":"2026-07-24T02:00:00Z","sourcePosition":{"partition":"p0","offset":%d,"eventId":"018f2e00-8000-7000-8000-%012d"}}`,
			sourceA, resolvedIdentityJSON, index+1, index+1,
		)
	}
	body := `{"observations":[` + strings.Join(items, ",") + `]}`
	if len(body) <= maximumSourceObservationSize {
		t.Fatalf("expected body > 96 KiB, got %d", len(body))
	}
	acceptor := &fakeObservationAcceptor{}
	handler := NewHandler(ServerConfig{
		ObservationAcceptor: acceptor,
		AllowedSourceSPIFFE: mqttSourceSPIFFE,
		Now:                 func() time.Time { return time.Date(2026, 7, 24, 2, 0, 5, 0, time.UTC) },
	})
	request := httptest.NewRequest(http.MethodPost, InternalSourceObservationBatchPath, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.TLS = verifiedTLSState(mqttSourceSPIFFE)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || len(acceptor.candidates) != 300 {
		t.Fatalf("large batch status=%d count=%d body=%s", recorder.Code, len(acceptor.candidates), recorder.Body.String())
	}
}

type fakeMQTTAcceptor struct {
	events []RuntimeEventEvidence
	err    error
}

func (fake *fakeMQTTAcceptor) AcceptRuntimeEvent(_ context.Context, evidence RuntimeEventEvidence) error {
	fake.events = append(fake.events, evidence)
	return fake.err
}

func TestMQTTRuntimeEventRouteForwardsResolvedDeviceAndMapsTransientErrors(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	acceptor := &fakeMQTTAcceptor{}
	handler := NewHandler(ServerConfig{
		MQTTEvidenceAcceptor: acceptor,
		AllowedSourceSPIFFE:  mqttSourceSPIFFE,
		Now:                  func() time.Time { return now },
	})
	body := `{"tenantId":"` + ingestTenantA + `","siteId":"` + siteA + `","gatewayId":"` + deviceA + `","messageId":"` + eventA + `","sequence":7,"eventType":"GATEWAY_DISCONNECTED","sourceType":"GATEWAY","sourceId":"` + deviceA + `","device":{"tenantId":"` + ingestTenantA + `","siteId":"` + siteA + `","deviceId":"` + deviceA + `"},"eventTime":"2026-11-16T09:59:59Z","severity":"WARNING","data":{}}`
	request := httptest.NewRequest(http.MethodPost, InternalMQTTRuntimeEventPath, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.TLS = verifiedTLSState(mqttSourceSPIFFE)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || len(acceptor.events) != 1 || acceptor.events[0].Device == nil || acceptor.events[0].Device.DeviceID != deviceA {
		t.Fatalf("status=%d events=%#v body=%s", recorder.Code, acceptor.events, recorder.Body.String())
	}

	acceptor.err = ErrRuntimeEventInvalid
	badReq := httptest.NewRequest(http.MethodPost, InternalMQTTRuntimeEventPath, strings.NewReader(body))
	badReq.Header.Set("Content-Type", "application/json")
	badReq.TLS = verifiedTLSState(mqttSourceSPIFFE)
	badRec := httptest.NewRecorder()
	handler.ServeHTTP(badRec, badReq)
	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("invalid runtime event status=%d, want 400", badRec.Code)
	}

	acceptor.err = errors.New("postgres unavailable")
	errReq := httptest.NewRequest(http.MethodPost, InternalMQTTRuntimeEventPath, strings.NewReader(body))
	errReq.Header.Set("Content-Type", "application/json")
	errReq.TLS = verifiedTLSState(mqttSourceSPIFFE)
	errRec := httptest.NewRecorder()
	handler.ServeHTTP(errRec, errReq)
	if errRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable runtime event status=%d, want 503", errRec.Code)
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

func TestSourceEndpointsPropagateTraceIDAndTraceparentFallback(t *testing.T) {
	now := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	obsAcceptor := &fakeObservationAcceptor{}
	eventAcceptor := &fakeMQTTAcceptor{}
	handler := NewHandler(ServerConfig{
		ObservationAcceptor:  obsAcceptor,
		MQTTEvidenceAcceptor: eventAcceptor,
		AllowedSourceSPIFFE:  mqttSourceSPIFFE,
		Tracer:               observability.NewTracer("telemetry-runtime-service", nil),
		Now:                  func() time.Time { return now },
	})

	itemTrace := "0123456789ABCDEF0123456789ABCDEF"
	headerTrace := "fedcba9876543210fedcba9876543210"
	traceparent := "00-" + headerTrace + "-0000000000000001-01"

	item1 := `{"sourceId":"` + sourceA + `","sourcePath":"PUSH","externalEntityType":"DEVICE","externalId":"CHILLER-01",` + resolvedIdentityJSON + `,"telemetryKey":"zone.temperature","value":23.5,"valueType":"NUMBER","unit":"Cel","sampledAt":"2026-11-17T09:59:55Z","sourcePosition":{"partition":"p0","offset":1,"eventId":"018f2e00-8000-7000-8000-000000000001"},"traceId":"` + itemTrace + `"}`
	item2 := `{"sourceId":"` + sourceA + `","sourcePath":"PUSH","externalEntityType":"DEVICE","externalId":"CHILLER-01",` + resolvedIdentityJSON + `,"telemetryKey":"zone.temperature","value":24.1,"valueType":"NUMBER","unit":"Cel","sampledAt":"2026-11-17T09:59:56Z","sourcePosition":{"partition":"p0","offset":2,"eventId":"018f2e00-8000-7000-8000-000000000002"}}`
	obsReq := httptest.NewRequest(http.MethodPost, InternalSourceObservationBatchPath, strings.NewReader(`{"observations":[`+item1+`,`+item2+`]}`))
	obsReq.Header.Set("Content-Type", "application/json")
	obsReq.Header.Set("traceparent", traceparent)
	obsReq.TLS = verifiedTLSState(mqttSourceSPIFFE)
	obsRec := httptest.NewRecorder()
	handler.ServeHTTP(obsRec, obsReq)
	if obsRec.Code != http.StatusOK {
		t.Fatalf("obs status=%d body=%s", obsRec.Code, obsRec.Body.String())
	}
	if got := obsRec.Header().Get("traceparent"); got != traceparent {
		t.Fatalf("response traceparent=%q, want %q", got, traceparent)
	}
	if len(obsAcceptor.candidates) != 2 {
		t.Fatalf("candidates=%d, want 2", len(obsAcceptor.candidates))
	}
	if obsAcceptor.candidates[0].TraceID != strings.ToLower(itemTrace) {
		t.Fatalf("candidate[0].TraceID=%q, want %q", obsAcceptor.candidates[0].TraceID, strings.ToLower(itemTrace))
	}
	if obsAcceptor.candidates[1].TraceID != headerTrace {
		t.Fatalf("candidate[1].TraceID=%q, want fallback %q", obsAcceptor.candidates[1].TraceID, headerTrace)
	}

	explicitEventBody := `{"tenantId":"` + ingestTenantA + `","siteId":"` + siteA + `","gatewayId":"` + deviceA + `","messageId":"` + eventA + `","sequence":7,"eventType":"GATEWAY_DISCONNECTED","sourceType":"GATEWAY","sourceId":"` + deviceA + `","eventTime":"2026-11-17T09:59:59Z","severity":"WARNING","data":{},"traceId":"` + itemTrace + `"}`
	evtReq1 := httptest.NewRequest(http.MethodPost, InternalMQTTRuntimeEventPath, strings.NewReader(explicitEventBody))
	evtReq1.Header.Set("Content-Type", "application/json")
	evtReq1.Header.Set("traceparent", traceparent)
	evtReq1.TLS = verifiedTLSState(mqttSourceSPIFFE)
	evtRec1 := httptest.NewRecorder()
	handler.ServeHTTP(evtRec1, evtReq1)
	if evtRec1.Code != http.StatusOK {
		t.Fatalf("event1 status=%d body=%s", evtRec1.Code, evtRec1.Body.String())
	}
	if got := evtRec1.Header().Get("traceparent"); got != traceparent {
		t.Fatalf("event1 response traceparent=%q, want %q", got, traceparent)
	}

	fallbackEventBody := `{"tenantId":"` + ingestTenantA + `","siteId":"` + siteA + `","gatewayId":"` + deviceA + `","messageId":"018f2e00-8000-7000-8000-000000000012","sequence":8,"eventType":"GATEWAY_CONNECTED","sourceType":"GATEWAY","sourceId":"` + deviceA + `","eventTime":"2026-11-17T10:00:00Z","severity":"INFO","data":{}}`
	evtReq2 := httptest.NewRequest(http.MethodPost, InternalMQTTRuntimeEventPath, strings.NewReader(fallbackEventBody))
	evtReq2.Header.Set("Content-Type", "application/json")
	evtReq2.Header.Set("traceparent", traceparent)
	evtReq2.TLS = verifiedTLSState(mqttSourceSPIFFE)
	evtRec2 := httptest.NewRecorder()
	handler.ServeHTTP(evtRec2, evtReq2)
	if evtRec2.Code != http.StatusOK {
		t.Fatalf("event2 status=%d body=%s", evtRec2.Code, evtRec2.Body.String())
	}

	evtReq3 := httptest.NewRequest(http.MethodPost, InternalMQTTRuntimeEventPath, strings.NewReader(fallbackEventBody))
	evtReq3.Header.Set("Content-Type", "application/json")
	evtReq3.Header.Set("traceparent", "00-00000000000000000000000000000000-0000000000000001-01")
	evtReq3.TLS = verifiedTLSState(mqttSourceSPIFFE)
	evtRec3 := httptest.NewRecorder()
	handler.ServeHTTP(evtRec3, evtReq3)
	if evtRec3.Code != http.StatusOK {
		t.Fatalf("event3 status=%d body=%s", evtRec3.Code, evtRec3.Body.String())
	}
	if got := evtRec3.Header().Get("traceparent"); got != "" {
		t.Fatalf("invalid traceparent echoed as %q", got)
	}

	if len(eventAcceptor.events) != 3 {
		t.Fatalf("events=%d, want 3", len(eventAcceptor.events))
	}
	if eventAcceptor.events[0].TraceID != strings.ToLower(itemTrace) {
		t.Fatalf("events[0].TraceID=%q, want %q", eventAcceptor.events[0].TraceID, strings.ToLower(itemTrace))
	}
	if eventAcceptor.events[1].TraceID != headerTrace {
		t.Fatalf("events[1].TraceID=%q, want fallback %q", eventAcceptor.events[1].TraceID, headerTrace)
	}
	if eventAcceptor.events[2].TraceID != "" {
		t.Fatalf("events[2].TraceID=%q, want empty", eventAcceptor.events[2].TraceID)
	}
}

func TestSourceRequestMapsEdgeReplayFlag(t *testing.T) {
	obsAcceptor := &fakeObservationAcceptor{}
	handler := NewHandler(ServerConfig{
		ObservationAcceptor: obsAcceptor,
		AllowedSourceSPIFFE: mqttSourceSPIFFE,
	})
	body := `{"observations":[
		{"sourceId":"` + sourceA + `","sourcePath":"PUSH","externalEntityType":"DEVICE","externalId":"CHILLER-01",` + resolvedIdentityJSON + `,"telemetryKey":"zone.temperature","value":23.5,"valueType":"NUMBER","unit":"Cel","sampledAt":"2026-07-24T01:00:00Z","sourcePosition":{"partition":"tb-telemetry-0","offset":100,"eventId":"` + eventA + `"},"edgeReplay":true},
		{"sourceId":"` + sourceA + `","sourcePath":"PUSH","externalEntityType":"DEVICE","externalId":"CHILLER-01",` + resolvedIdentityJSON + `,"telemetryKey":"zone.temperature","value":24.0,"valueType":"NUMBER","unit":"Cel","sampledAt":"2026-07-24T02:00:00Z","sourcePosition":{"partition":"tb-telemetry-0","offset":101,"eventId":"018f2e00-8000-7000-8000-000000000012"}}
	]}`
	request := httptest.NewRequest(http.MethodPost, InternalSourceObservationBatchPath, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.TLS = verifiedTLSState(mqttSourceSPIFFE)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(obsAcceptor.candidates) != 2 {
		t.Fatalf("candidates=%d, want 2", len(obsAcceptor.candidates))
	}
	if !obsAcceptor.candidates[0].EdgeReplay {
		t.Fatal("expected edgeReplay=true on first candidate")
	}
	if obsAcceptor.candidates[1].EdgeReplay {
		t.Fatal("expected edgeReplay=false when omitted on second candidate")
	}
}

func TestSourceBatchPopulatesSiteLiveCacheAndInvalidatesOnReject(t *testing.T) {
	now := time.Date(2026, 11, 18, 4, 15, 0, 0, time.UTC)
	pointTemp := "018f2e00-3100-7000-8000-000000000001"
	pointSuspect := "018f2e00-3100-7000-8000-000000000002"
	pointEdge := "018f2e00-3100-7000-8000-000000000003"
	pointText := "018f2e00-3100-7000-8000-000000000004"

	cache := &memoryLatestCache{}
	acceptor := &fakeObservationAcceptor{
		receipts: []ObservationReceipt{
			{
				ObservationID:    "018f2e00-9000-7000-8000-000000000001",
				Status:           ObservationAccepted,
				Quality:          QualityGood,
				QualityReasons:   []QualityReason{},
				DeviceID:         deviceA,
				PositionAdvanced: true,
			},
			{
				ObservationID:    "018f2e00-9000-7000-8000-000000000002",
				Status:           ObservationAccepted,
				Quality:          QualityPartial,
				QualityReasons:   []QualityReason{QualityReasonSpike},
				DeviceID:         deviceA,
				PositionAdvanced: true,
			},
			{
				ObservationID:    "018f2e00-9000-7000-8000-000000000003",
				Status:           ObservationAccepted,
				Quality:          QualityGood,
				QualityReasons:   []QualityReason{},
				DeviceID:         deviceA,
				PositionAdvanced: true,
			},
			{
				ObservationID:    "018f2e00-9000-7000-8000-000000000004",
				Status:           ObservationAccepted,
				Quality:          QualityGood,
				QualityReasons:   []QualityReason{},
				DeviceID:         deviceA,
				PositionAdvanced: true,
			},
		},
	}
	handler := NewHandler(ServerConfig{
		ObservationAcceptor: acceptor,
		LatestCache:         cache,
		AllowedSourceSPIFFE: mqttSourceSPIFFE,
		Now:                 func() time.Time { return now },
	})

	pointJSON := func(pointID, valueType, unit string) string {
		return `"device":{"tenantId":"` + ingestTenantA + `","siteId":"` + siteA + `","deviceId":"` + deviceA + `"},` +
			`"point":{"pointId":"` + pointID + `","sensorId":null,"pointType":"TELEMETRY","valueType":"` + valueType + `","unit":` + unit + `,"counterDecreaseMode":null,"counterRolloverModulus":null,"pointRevision":3}`
	}

	body := `{"observations":[` +
		`{"sourceId":"` + sourceA + `","sourcePath":"PUSH","externalEntityType":"DEVICE","externalId":"CHILLER-01",` + pointJSON(pointTemp, "NUMBER", `"Cel"`) + `,"telemetryKey":"supply_water_temperature","value":6.5,"valueType":"NUMBER","unit":"Cel","sampledAt":"2026-11-18T04:14:55Z","sourcePosition":{"partition":"p0","offset":1,"eventId":"018f2e00-8000-7000-8000-000000000001"}},` +
		`{"sourceId":"` + sourceA + `","sourcePath":"PUSH","externalEntityType":"DEVICE","externalId":"CHILLER-01",` + pointJSON(pointSuspect, "NUMBER", `"Cel"`) + `,"telemetryKey":"zone.t53","value":45.2,"valueType":"NUMBER","unit":"Cel","sampledAt":"2026-11-18T04:14:56Z","sourcePosition":{"partition":"p0","offset":2,"eventId":"018f2e00-8000-7000-8000-000000000002"}},` +
		`{"sourceId":"` + sourceA + `","sourcePath":"PUSH","externalEntityType":"DEVICE","externalId":"CHILLER-01",` + pointJSON(pointEdge, "NUMBER", `"Cel"`) + `,"telemetryKey":"zone.edge","value":22.0,"valueType":"NUMBER","unit":"Cel","sampledAt":"2026-11-18T04:10:00Z","sourcePosition":{"partition":"p0","offset":3,"eventId":"018f2e00-8000-7000-8000-000000000003"},"edgeReplay":true},` +
		`{"sourceId":"` + sourceA + `","sourcePath":"PUSH","externalEntityType":"DEVICE","externalId":"CHILLER-01",` + pointJSON(pointText, "STRING", `null`) + `,"telemetryKey":"chiller.mode","value":"COOL","valueType":"STRING","unit":null,"sampledAt":"2026-11-18T04:14:57Z","sourcePosition":{"partition":"p0","offset":4,"eventId":"018f2e00-8000-7000-8000-000000000004"}}` +
		`]}`
	req := httptest.NewRequest(http.MethodPost, InternalSourceObservationBatchPath, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.TLS = verifiedTLSState(mqttSourceSPIFFE)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	got, err := cache.GetSiteLiveObservations(t.Context(), ingestTenantA, siteA, []string{pointTemp, pointSuspect, pointEdge, pointText})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected only 2 live numeric observations cached, got %#v", got)
	}
	if got[pointTemp].HealthStatus != HealthHealthy || got[pointTemp].Value != 6.5 || got[pointTemp].Quality != "GOOD" || got[pointTemp].SourceOffset != 1 {
		t.Fatalf("unexpected cached pointTemp: %+v", got[pointTemp])
	}
	if got[pointSuspect].HealthStatus != HealthSuspect || !slices.Equal(got[pointSuspect].HealthReasons, []string{"SPIKE"}) || got[pointSuspect].Quality != "PARTIAL" {
		t.Fatalf("unexpected cached pointSuspect: %+v", got[pointSuspect])
	}

	acceptor.receipts = []ObservationReceipt{{
		ObservationID:    "018f2e00-9000-7000-8000-000000000005",
		Status:           ObservationRejected,
		Quality:          QualityInvalid,
		QualityReasons:   []QualityReason{QualityReasonOutOfRange},
		DeviceID:         deviceA,
		PositionAdvanced: true,
	}}
	rejectBody := `{"observations":[{"sourceId":"` + sourceA + `","sourcePath":"PUSH","externalEntityType":"DEVICE","externalId":"CHILLER-01",` + pointJSON(pointTemp, "NUMBER", `"Cel"`) + `,"telemetryKey":"supply_water_temperature","value":250.0,"valueType":"NUMBER","unit":"Cel","sampledAt":"2026-11-18T04:15:00Z","sourcePosition":{"partition":"p0","offset":5,"eventId":"018f2e00-8000-7000-8000-000000000005"}}]}`
	rejectReq := httptest.NewRequest(http.MethodPost, InternalSourceObservationBatchPath, strings.NewReader(rejectBody))
	rejectReq.Header.Set("Content-Type", "application/json")
	rejectReq.TLS = verifiedTLSState(mqttSourceSPIFFE)
	rejectRec := httptest.NewRecorder()
	handler.ServeHTTP(rejectRec, rejectReq)
	if rejectRec.Code != http.StatusOK {
		t.Fatalf("reject status=%d body=%s", rejectRec.Code, rejectRec.Body.String())
	}
	afterReject, err := cache.GetSiteLiveObservations(t.Context(), ingestTenantA, siteA, []string{pointTemp})
	if err != nil || len(afterReject) != 0 {
		t.Fatalf("expected pointTemp evicted from cache on OUT_OF_RANGE rejection, got=%#v err=%v", afterReject, err)
	}

	cache.putErr = errors.New("redis unavailable")
	acceptor.receipts = []ObservationReceipt{{
		ObservationID:    "018f2e00-9000-7000-8000-000000000006",
		Status:           ObservationAccepted,
		Quality:          QualityGood,
		QualityReasons:   []QualityReason{},
		DeviceID:         deviceA,
		PositionAdvanced: true,
	}}
	okReq := httptest.NewRequest(http.MethodPost, InternalSourceObservationBatchPath, strings.NewReader(`{"observations":[{"sourceId":"`+sourceA+`","sourcePath":"PUSH","externalEntityType":"DEVICE","externalId":"CHILLER-01",`+pointJSON(pointTemp, "NUMBER", `"Cel"`)+`,"telemetryKey":"supply_water_temperature","value":6.8,"valueType":"NUMBER","unit":"Cel","sampledAt":"2026-11-18T04:15:05Z","sourcePosition":{"partition":"p0","offset":6,"eventId":"018f2e00-8000-7000-8000-000000000006"}}]}`))
	okReq.Header.Set("Content-Type", "application/json")
	okReq.TLS = verifiedTLSState(mqttSourceSPIFFE)
	okRec := httptest.NewRecorder()
	handler.ServeHTTP(okRec, okReq)
	if okRec.Code != http.StatusOK {
		t.Fatalf("expected 200 even when Redis write fails, got %d body=%s", okRec.Code, okRec.Body.String())
	}
}
