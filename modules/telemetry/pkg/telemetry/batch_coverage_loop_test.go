package telemetry

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/limitpolicy"
	"github.com/quanlaihe/hvac-web/libs/observability"
)

const testBatchSourceSPIFFE = "spiffe://hvac.local/mqtt-telemetry-adapter"

type fakeBatchObservationAcceptor struct {
	mu               sync.Mutex
	seenIDs          map[string]map[string]bool
	batchCalls       [][]ObservationCandidate
	filterCalls      int
	filterErr        error
	batchErr         error
	forceStatus      ObservationStatus
	forceQuarantined bool
}

func newFakeBatchObservationAcceptor() *fakeBatchObservationAcceptor {
	return &fakeBatchObservationAcceptor{seenIDs: map[string]map[string]bool{}}
}

func (f *fakeBatchObservationAcceptor) markSeen(sourceID string, eventIDs ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.seenIDs[sourceID] == nil {
		f.seenIDs[sourceID] = map[string]bool{}
	}
	for _, id := range eventIDs {
		f.seenIDs[sourceID][id] = true
	}
}

func (f *fakeBatchObservationAcceptor) FilterUnseenObservations(_ context.Context, sourceID string, eventIDs []string) (map[string]bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.filterCalls++
	if f.filterErr != nil {
		return nil, f.filterErr
	}
	unseen := make(map[string]bool)
	for _, id := range eventIDs {
		if !f.seenIDs[sourceID][id] {
			unseen[id] = true
		}
	}
	return unseen, nil
}

func (f *fakeBatchObservationAcceptor) AcceptHistoricalObservation(_ context.Context, candidate ObservationCandidate) (ObservationReceipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.seenIDs[candidate.SourceID][candidate.Position.EventID] {
		return ObservationReceipt{ObservationID: candidate.Position.EventID, Status: ObservationDuplicate, Quality: QualityGood}, nil
	}
	return ObservationReceipt{ObservationID: candidate.Position.EventID, Status: ObservationAccepted, Quality: QualityGood}, nil
}

func (f *fakeBatchObservationAcceptor) AcceptHistoricalObservationBatch(ctx context.Context, candidates []ObservationCandidate) ([]ObservationReceipt, error) {
	receipts := make([]ObservationReceipt, len(candidates))
	for i, c := range candidates {
		r, err := f.AcceptHistoricalObservation(ctx, c)
		if err != nil {
			return nil, err
		}
		receipts[i] = r
	}
	return receipts, nil
}

func (f *fakeBatchObservationAcceptor) AcceptObservationBatch(_ context.Context, candidates []ObservationCandidate) ([]ObservationReceipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.batchErr != nil {
		return nil, f.batchErr
	}
	copied := slices.Clone(candidates)
	f.batchCalls = append(f.batchCalls, copied)
	receipts := make([]ObservationReceipt, len(candidates))
	for i, c := range candidates {
		deviceID := ""
		if c.Device != nil {
			deviceID = c.Device.DeviceID
		}
		switch {
		case f.forceQuarantined:
			receipts[i] = ObservationReceipt{Status: ObservationQuarantined, QuarantineReason: "ASSET_NOT_REGISTERED"}
		case f.forceStatus != "":
			receipts[i] = ObservationReceipt{Status: f.forceStatus, Quality: QualityGood, DeviceID: deviceID}
		case f.seenIDs[c.SourceID][c.Position.EventID]:
			receipts[i] = ObservationReceipt{Status: ObservationDuplicate, Quality: QualityGood, DeviceID: deviceID}
		default:
			receipts[i] = ObservationReceipt{Status: ObservationAccepted, Quality: QualityGood, DeviceID: deviceID}
		}
	}
	return receipts, nil
}

type fakeCoverageAndEvidenceAcceptor struct {
	mu          sync.Mutex
	coverage    []SourceCoverageReport
	coverageErr error
	events      []RuntimeEventEvidence
	eventErr    error
}

func (f *fakeCoverageAndEvidenceAcceptor) ReportSourceCoverage(_ context.Context, report SourceCoverageReport) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.coverageErr != nil {
		return f.coverageErr
	}
	f.coverage = append(f.coverage, report)
	return nil
}

func (f *fakeCoverageAndEvidenceAcceptor) AcceptRuntimeEvent(_ context.Context, evidence RuntimeEventEvidence) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.eventErr != nil {
		return f.eventErr
	}
	f.events = append(f.events, evidence)
	return nil
}

func performSPIFFEPostForPeer(t *testing.T, h http.Handler, spiffeID, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(encoded))
	req.Header.Set("Content-Type", "application/json")
	uri, err := url.Parse(spiffeID)
	if err != nil {
		t.Fatalf("parse SPIFFE URI: %v", err)
	}
	cert := &x509.Certificate{URIs: []*url.URL{uri}}
	req.TLS = &tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{cert},
		VerifiedChains:   [][]*x509.Certificate{{cert}},
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func performSPIFFEPost(t *testing.T, h http.Handler, path string, body any) *httptest.ResponseRecorder {
	return performSPIFFEPostForPeer(t, h, testBatchSourceSPIFFE, path, body)
}

func validObservationMap(sourceID, eventID string) map[string]any {
	return map[string]any{
		"sourceId":           sourceID,
		"sourcePath":         "PUSH",
		"externalEntityType": "DEVICE",
		"externalId":         "METER-01",
		"device": map[string]any{
			"tenantId": "018f2d00-0000-7000-8000-000000000001",
			"siteId":   "018f2e00-1000-7000-8000-000000000001",
			"deviceId": "018f2e00-3000-7000-8000-000000000001",
		},
		"point": map[string]any{
			"pointId":       "018f2e00-3100-7000-8000-000000000001",
			"pointType":     "TELEMETRY",
			"valueType":     "NUMBER",
			"pointRevision": 1,
		},
		"telemetryKey": "zone.temperature",
		"value":        23.5,
		"valueType":    "NUMBER",
		"wireQuality":  0,
		"sampledAt":    "2026-11-16T10:00:00Z",
		"sourcePosition": map[string]any{
			"partition": "gw-1:METER-01:zone.temperature",
			"offset":    1,
			"eventId":   eventID,
		},
	}
}

func TestSourceIngestAntiLivelockSkipsRateLimitWhenAllObservationsSeen(t *testing.T) {
	seenEvent1 := "0198a100-0000-7000-8000-000000000011"
	seenEvent2 := "0198a100-0000-7000-8000-000000000012"
	unseenEvent1 := "0198a100-0000-7000-8000-000000000013"
	unseenEvent2 := "0198a100-0000-7000-8000-000000000014"

	acceptor := newFakeBatchObservationAcceptor()
	acceptor.markSeen("gw-livelock", seenEvent1, seenEvent2)

	const replaySPIFFE = "spiffe://hvac.local/historical-replay-runner"
	const replayDataset = "018f2e00-7000-7000-8000-000000000001"
	const replayDevice = "018f2e00-3000-7000-8000-000000000001"
	replayCandidate1, err := normalizeHistoricalReplayObservation(historicalReplayObservationRequest{
		ReplayDatasetID: replayDataset,
		DeviceID:        replayDevice,
		TelemetryKey:    "zone.temperature",
		Value:           json.RawMessage(`23.5`),
		ValueType:       "NUMBER",
		SampledAt:       "2026-11-16T10:00:00Z",
		Offset:          1,
	}, time.Date(2026, 11, 16, 10, 0, 5, 0, time.UTC))
	if err != nil {
		t.Fatalf("compute historical candidate: %v", err)
	}
	acceptor.markSeen(historicalReplaySourceID, replayCandidate1.Position.EventID)

	limiter := limitpolicy.NewLimiter(limitpolicy.NewMemoryCounter(64), &limitpolicy.Policy{
		Version: 1,
		Limits: []limitpolicy.Limit{
			{Dimension: limitpolicy.DimensionTelemetryIngest, Window: time.Hour, Burst: 1, FailClosed: true},
		},
	})
	h := NewHandler(ServerConfig{
		ObservationAcceptor:           acceptor,
		HistoricalObservationAcceptor: acceptor,
		AllowedSourceSPIFFE:           testBatchSourceSPIFFE,
		AllowedHistoricalReplaySPIFFE: replaySPIFFE,
		RateLimiter:                   limiter,
		Metrics:                       observability.NewRegistry(),
		Now:                           func() time.Time { return time.Date(2026, 11, 16, 10, 0, 5, 0, time.UTC) },
	})

	// 1. All-seen batch MUST NOT consume the 1 available burst token.
	recSeenOnly := performSPIFFEPost(t, h, InternalSourceObservationBatchPath, map[string]any{
		"observations": []any{
			validObservationMap("gw-livelock", seenEvent1),
			validObservationMap("gw-livelock", seenEvent2),
		},
	})
	if recSeenOnly.Code != http.StatusOK {
		t.Fatalf("all-seen batch status = %d (%s), want 200", recSeenOnly.Code, recSeenOnly.Body.String())
	}

	// 2. Mixed batch with unseenEvent1 consumes the single burst token and succeeds.
	recMixed := performSPIFFEPost(t, h, InternalSourceObservationBatchPath, map[string]any{
		"observations": []any{
			validObservationMap("gw-livelock", seenEvent1),
			validObservationMap("gw-livelock", unseenEvent1),
		},
	})
	if recMixed.Code != http.StatusOK {
		t.Fatalf("mixed batch status = %d (%s), want 200", recMixed.Code, recMixed.Body.String())
	}

	// 3. Budget is now 0: another unseen batch returns 429 without invoking AcceptObservationBatch.
	rec429 := performSPIFFEPost(t, h, InternalSourceObservationBatchPath, map[string]any{
		"observations": []any{
			validObservationMap("gw-livelock", unseenEvent2),
		},
	})
	if rec429.Code != http.StatusTooManyRequests {
		t.Fatalf("over-budget unseen batch status = %d, want 429", rec429.Code)
	}
	if len(acceptor.batchCalls) != 2 {
		t.Fatalf("batch acceptor call count = %d, want 2 (429 must short-circuit)", len(acceptor.batchCalls))
	}

	// 4. Even with exhausted quota, an all-seen retry still succeeds (200 OK).
	recSeenRetry := performSPIFFEPost(t, h, InternalSourceObservationBatchPath, map[string]any{
		"observations": []any{
			validObservationMap("gw-livelock", seenEvent1),
			validObservationMap("gw-livelock", seenEvent2),
		},
	})
	if recSeenRetry.Code != http.StatusOK {
		t.Fatalf("all-seen batch retry under exhausted limit = %d (%s), want 200", recSeenRetry.Code, recSeenRetry.Body.String())
	}

	// 5. Historical replay skips quota when seen and enforces quota when unseen.
	recReplaySeen := performSPIFFEPostForPeer(t, h, replaySPIFFE, InternalHistoricalReplayObservationPath, map[string]any{
		"replayDatasetId": replayDataset,
		"deviceId":        replayDevice,
		"telemetryKey":    "zone.temperature",
		"value":           23.5,
		"valueType":       "NUMBER",
		"sampledAt":       "2026-11-16T10:00:00Z",
		"offset":          1,
	})
	if recReplaySeen.Code != http.StatusOK {
		t.Fatalf("already-seen historical replay status = %d (%s), want 200", recReplaySeen.Code, recReplaySeen.Body.String())
	}
	recReplayUnseen1 := performSPIFFEPostForPeer(t, h, replaySPIFFE, InternalHistoricalReplayObservationPath, map[string]any{
		"replayDatasetId": replayDataset,
		"deviceId":        replayDevice,
		"telemetryKey":    "zone.temperature",
		"value":           24.0,
		"valueType":       "NUMBER",
		"sampledAt":       "2026-11-16T10:01:00Z",
		"offset":          2,
	})
	if recReplayUnseen1.Code != http.StatusOK {
		t.Fatalf("first unseen historical replay status = %d (%s), want 200", recReplayUnseen1.Code, recReplayUnseen1.Body.String())
	}
	recReplayUnseen2 := performSPIFFEPostForPeer(t, h, replaySPIFFE, InternalHistoricalReplayObservationPath, map[string]any{
		"replayDatasetId": replayDataset,
		"deviceId":        replayDevice,
		"telemetryKey":    "zone.temperature",
		"value":           24.5,
		"valueType":       "NUMBER",
		"sampledAt":       "2026-11-16T10:02:00Z",
		"offset":          3,
	})
	if recReplayUnseen2.Code != http.StatusTooManyRequests {
		t.Fatalf("exhausted unseen historical replay status = %d, want 429", recReplayUnseen2.Code)
	}

	// 6. Unseen filter DB failure fails closed with 503.
	acceptor.filterErr = errors.New("db unreachable")
	recFilterErr := performSPIFFEPost(t, h, InternalSourceObservationBatchPath, map[string]any{
		"observations": []any{validObservationMap("gw-livelock", seenEvent1)},
	})
	if recFilterErr.Code != http.StatusServiceUnavailable {
		t.Fatalf("filter error status = %d, want 503", recFilterErr.Code)
	}
}

func TestSourceCoverageAndMQTTEventEndpoints(t *testing.T) {
	sink := &fakeCoverageAndEvidenceAcceptor{}
	h := NewHandler(ServerConfig{
		CoverageAcceptor:     sink,
		MQTTEvidenceAcceptor: sink,
		AllowedSourceSPIFFE:  testBatchSourceSPIFFE,
		Metrics:              observability.NewRegistry(),
		Now:                  func() time.Time { return time.Date(2026, 11, 16, 10, 0, 5, 0, time.UTC) },
	})

	reason := "SOURCE_UNAVAILABLE"
	covRec := performSPIFFEPost(t, h, InternalSourceCoveragePath, map[string]any{
		"sourceId":       "connectivity-1",
		"deviceIds":      []string{"018f2e00-3000-7000-8000-000000000001"},
		"available":      false,
		"reasonCode":     reason,
		"sourceRevision": 3,
	})
	if covRec.Code != http.StatusOK {
		t.Fatalf("coverage report status = %d (%s), want 200", covRec.Code, covRec.Body.String())
	}
	if len(sink.coverage) != 1 || sink.coverage[0].SourceRevision != 3 || sink.coverage[0].Available {
		t.Fatalf("unexpected captured coverage report: %+v", sink.coverage)
	}

	sink.coverageErr = ErrInvalidCoverageReport
	badCov := performSPIFFEPost(t, h, InternalSourceCoveragePath, map[string]any{
		"sourceId":  "connectivity-1",
		"deviceIds": []string{"018f2e00-3000-7000-8000-000000000001"},
		"available": true,
	})
	if badCov.Code != http.StatusBadRequest {
		t.Fatalf("invalid coverage status = %d, want 400", badCov.Code)
	}

	evtRec := performSPIFFEPost(t, h, InternalMQTTRuntimeEventPath, map[string]any{
		"tenantId":   "018f2d00-0000-7000-8000-000000000001",
		"siteId":     "018f2e00-1000-7000-8000-000000000001",
		"gatewayId":  "018f3e00-4000-7000-8000-000000000100",
		"messageId":  "0198a100-0000-7000-8000-000000000021",
		"sequence":   4,
		"eventType":  "LWT",
		"sourceType": "GATEWAY",
		"sourceId":   "018f3e00-4000-7000-8000-000000000100",
		"eventTime":  "2026-11-16T10:00:00Z",
		"severity":   "WARNING",
		"data":       map[string]any{"reason": "UNEXPECTED_DISCONNECT"},
		"device": map[string]any{
			"tenantId": "018f2d00-0000-7000-8000-000000000001",
			"siteId":   "018f2e00-1000-7000-8000-000000000001",
			"deviceId": "018f3e00-4000-7000-8000-000000000100",
		},
		"coverageDeviceIds": []string{"018f2e00-3000-7000-8000-000000000001"},
	})
	if evtRec.Code != http.StatusOK {
		t.Fatalf("runtime event status = %d (%s), want 200", evtRec.Code, evtRec.Body.String())
	}
	if len(sink.events) != 1 || sink.events[0].Device == nil || len(sink.events[0].CoverageDeviceIDs) != 1 {
		t.Fatalf("runtime event scope not preserved: %+v", sink.events)
	}

	sink.eventErr = ErrRuntimeEventInvalid
	badEvt := performSPIFFEPost(t, h, InternalMQTTRuntimeEventPath, map[string]any{
		"tenantId":   "018f2d00-0000-7000-8000-000000000001",
		"siteId":     "018f2e00-1000-7000-8000-000000000001",
		"gatewayId":  "018f3e00-4000-7000-8000-000000000100",
		"messageId":  "0198a100-0000-7000-8000-000000000022",
		"sequence":   5,
		"eventType":  "LWT",
		"sourceType": "GATEWAY",
		"sourceId":   "018f3e00-4000-7000-8000-000000000100",
		"eventTime":  "2026-11-16T10:00:00Z",
		"severity":   "WARNING",
		"data":       map[string]any{},
	})
	if badEvt.Code != http.StatusBadRequest {
		t.Fatalf("invalid runtime event status = %d, want 400", badEvt.Code)
	}
}

func TestClassifyPresenceSignalTypeAndCoverageReasonNormalization(t *testing.T) {
	if sig, ok := classifyPresenceSignalType("GATEWAY_CONNECTED"); !ok || sig != "EXPLICIT_CONNECT" {
		t.Fatalf("GATEWAY_CONNECTED -> (%q, %v)", sig, ok)
	}
	if sig, ok := classifyPresenceSignalType("LWT"); !ok || sig != "EXPLICIT_DISCONNECT" {
		t.Fatalf("LWT -> (%q, %v)", sig, ok)
	}
	for _, evType := range []string{"EXPLICIT_SLEEP", "SLEEPING", "DEVICE_SLEEPING", "ENTER_SLEEP"} {
		if sig, ok := classifyPresenceSignalType(evType); !ok || sig != "EXPLICIT_SLEEP" {
			t.Fatalf("%s -> (%q, %v)", evType, sig, ok)
		}
	}
	if _, ok := classifyPresenceSignalType("UNKNOWN_EVENT"); ok {
		t.Fatal("UNKNOWN_EVENT must not map to a presence signal")
	}
	code, err := normalizeCoverageReasonCode("GATEWAY_CREDENTIAL_REVOKED")
	if err != nil || code != "SOURCE_UNAVAILABLE" {
		t.Fatalf("normalizeCoverageReasonCode = (%q, %v), want SOURCE_UNAVAILABLE", code, err)
	}
	if _, err := normalizeCoverageReasonCode("NOT_A_REASON"); !errors.Is(err, ErrInvalidCoverageReport) {
		t.Fatalf("unsupported reason err = %v, want ErrInvalidCoverageReport", err)
	}
}

type validatingCoverageAcceptor struct {
	mu      sync.Mutex
	reports []SourceCoverageReport
}

func (a *validatingCoverageAcceptor) ReportSourceCoverage(_ context.Context, report SourceCoverageReport) error {
	normalized, err := validateAndNormalizeCoverageReport(report)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.reports = append(a.reports, normalized)
	return nil
}

func TestSourceCoverageGatewayScopesHTTPContract(t *testing.T) {
	sink := &validatingCoverageAcceptor{}
	h := NewHandler(ServerConfig{
		CoverageAcceptor:    sink,
		AllowedSourceSPIFFE: testBatchSourceSPIFFE,
		Metrics:             observability.NewRegistry(),
		Now:                 func() time.Time { return time.Date(2026, 11, 16, 10, 0, 5, 0, time.UTC) },
	})

	dev1 := "018f2e00-3000-7000-8000-000000000001"
	dev2 := "018f2e00-3000-7000-8000-000000000002"
	dev3 := "018f2e00-3000-7000-8000-000000000003"
	gw1 := "018f3e00-4000-7000-8000-000000000100"
	gw2 := "018f3e00-4000-7000-8000-000000000200"

	validRec := performSPIFFEPost(t, h, InternalSourceCoveragePath, map[string]any{
		"sourceId":  "connectivity-1",
		"deviceIds": []string{dev2, dev1},
		"gatewayScopes": []map[string]any{
			{"gatewayId": " " + gw2 + " ", "deviceIds": []string{dev2}},
			{"gatewayId": gw1, "deviceIds": []string{dev1}},
		},
		"available":      true,
		"sourceRevision": 4,
	})
	if validRec.Code != http.StatusOK {
		t.Fatalf("valid gatewayScopes status = %d (%s), want 200", validRec.Code, validRec.Body.String())
	}
	if len(sink.reports) != 1 || len(sink.reports[0].GatewayScopes) != 2 {
		t.Fatalf("captured reports = %+v, want 1 report with 2 scopes", sink.reports)
	}
	if sink.reports[0].GatewayScopes[0].GatewayID != gw1 || !slices.Equal(sink.reports[0].GatewayScopes[0].DeviceIDs, []string{dev1}) {
		t.Fatalf("unexpected first scope: %+v", sink.reports[0].GatewayScopes[0])
	}
	if sink.reports[0].GatewayScopes[1].GatewayID != gw2 || !slices.Equal(sink.reports[0].GatewayScopes[1].DeviceIDs, []string{dev2}) {
		t.Fatalf("unexpected second scope: %+v", sink.reports[0].GatewayScopes[1])
	}

	tooLongGateway := ""
	for range 257 {
		tooLongGateway += "a"
	}
	tooManyScopes := make([]map[string]any, maximumCoverageDeviceIDs+1)
	for i := range tooManyScopes {
		tooManyScopes[i] = map[string]any{"gatewayId": gw1, "deviceIds": []string{dev1}}
	}

	invalidPayloads := []struct {
		name string
		body map[string]any
	}{
		{
			name: "empty_gateway_id",
			body: map[string]any{
				"sourceId":      "connectivity-1",
				"deviceIds":     []string{dev1},
				"gatewayScopes": []map[string]any{{"gatewayId": "   ", "deviceIds": []string{dev1}}},
				"available":     true,
			},
		},
		{
			name: "oversized_gateway_id",
			body: map[string]any{
				"sourceId":      "connectivity-1",
				"deviceIds":     []string{dev1},
				"gatewayScopes": []map[string]any{{"gatewayId": tooLongGateway, "deviceIds": []string{dev1}}},
				"available":     true,
			},
		},
		{
			name: "empty_scope_device_ids",
			body: map[string]any{
				"sourceId":      "connectivity-1",
				"deviceIds":     []string{dev1},
				"gatewayScopes": []map[string]any{{"gatewayId": gw1, "deviceIds": []string{}}},
				"available":     true,
			},
		},
		{
			name: "invalid_scope_device_uuid",
			body: map[string]any{
				"sourceId":      "connectivity-1",
				"deviceIds":     []string{dev1},
				"gatewayScopes": []map[string]any{{"gatewayId": gw1, "deviceIds": []string{"not-a-uuid"}}},
				"available":     true,
			},
		},
		{
			name: "contradictory_scope_device_absent_from_device_ids",
			body: map[string]any{
				"sourceId":      "connectivity-1",
				"deviceIds":     []string{dev1, dev2},
				"gatewayScopes": []map[string]any{{"gatewayId": gw1, "deviceIds": []string{dev1, dev3}}},
				"available":     true,
			},
		},
		{
			name: "unknown_field_in_scope",
			body: map[string]any{
				"sourceId":      "connectivity-1",
				"deviceIds":     []string{dev1},
				"gatewayScopes": []map[string]any{{"gatewayId": gw1, "deviceIds": []string{dev1}, "unknown": true}},
				"available":     true,
			},
		},
		{
			name: "too_many_scopes",
			body: map[string]any{
				"sourceId":      "connectivity-1",
				"deviceIds":     []string{dev1},
				"gatewayScopes": tooManyScopes,
				"available":     true,
			},
		},
	}

	for _, tc := range invalidPayloads {
		t.Run(tc.name, func(t *testing.T) {
			rec := performSPIFFEPost(t, h, InternalSourceCoveragePath, tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("%s status = %d (%s), want 400", tc.name, rec.Code, rec.Body.String())
			}
		})
	}
}
