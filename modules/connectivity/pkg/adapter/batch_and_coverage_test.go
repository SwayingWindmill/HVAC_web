package adapter

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type spyIdentities struct {
	*fakeIdentities
	childIDs    []string
	childrenErr error
}

func newSpyIdentities(children ...string) *spyIdentities {
	return &spyIdentities{
		fakeIdentities: newFakeIdentities(),
		childIDs:       children,
	}
}

func (s *spyIdentities) ListGatewayChildDeviceIDs(_ context.Context, _ string) ([]string, error) {
	if s.childrenErr != nil {
		return nil, s.childrenErr
	}
	return append([]string(nil), s.childIDs...), nil
}

type fakeCoverageLister struct {
	mu        sync.Mutex
	deviceIDs []string
}

func (l *fakeCoverageLister) ListAllActiveManagedDeviceIDs(_ context.Context) ([]string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.deviceIDs...), nil
}

func TestProcessorAttachesEventDeviceAndCoverageScope(t *testing.T) {
	childA := "018f3e00-4000-7000-8000-000000000011"
	childB := "018f3e00-4000-7000-8000-000000000012"
	spy := newSpyIdentities(testGatewayA, childA, childB)
	telemetry := &fakeTelemetry{}
	processor, err := NewProcessor(spy, telemetry)
	if err != nil {
		t.Fatalf("NewProcessor: %v", err)
	}

	lwtPayload, err := json.Marshal(map[string]any{
		"schemaVersion": "2.0",
		"messageId":     testMessageID,
		"gatewayId":     testGatewayA,
		"timestamp":     1700000000000,
		"sequence":      9,
		"payload": map[string]any{
			"eventType":  "LWT",
			"sourceType": "GATEWAY",
			"sourceId":   testGatewayA,
			"eventTime":  1700000000000,
			"severity":   "WARNING",
			"data":       map[string]any{"reason": "broker_connection_lost", "sessionId": testMessageID},
		},
	})
	if err != nil {
		t.Fatalf("marshal LWT payload: %v", err)
	}
	if _, err := processor.Process(context.Background(), "hvac/v1/"+testGatewayA+"/up/event", lwtPayload); err != nil {
		t.Fatalf("Process LWT failed: %v", err)
	}

	telemetry.mu.Lock()
	defer telemetry.mu.Unlock()
	if len(telemetry.events) != 1 {
		t.Fatalf("events count = %d, want 1", len(telemetry.events))
	}
	evt := telemetry.events[0]
	if evt.EventTime != "2023-11-14T22:13:20Z" {
		t.Fatalf("LWT EventTime = %q, want preserved edge timestamp 2023-11-14T22:13:20Z", evt.EventTime)
	}
	if evt.Device == nil || evt.Device.DeviceID != testGatewayA {
		t.Fatalf("expected gateway Device %s, got %+v", testGatewayA, evt.Device)
	}
	if len(evt.CoverageDeviceIDs) != 2 || evt.CoverageDeviceIDs[0] != childA || evt.CoverageDeviceIDs[1] != childB {
		t.Fatalf("expected child coverage IDs [%s %s], got %v", childA, childB, evt.CoverageDeviceIDs)
	}
}

func TestRuntimeReportsCoverageOnSubscribeHeartbeatLossAndShutdown(t *testing.T) {
	broker := newFakeBroker(t)
	identities := newFakeIdentities()
	telemetry := &fakeTelemetry{}
	processor, err := NewProcessor(identities, telemetry)
	if err != nil {
		t.Fatalf("NewProcessor: %v", err)
	}
	lister := &fakeCoverageLister{deviceIDs: []string{testDeviceID}}
	runtime, err := NewRuntime(
		Config{
			MQTT: broker.mqtt,
			TelemetryRuntime: TelemetryRuntimeConfig{
				BaseURL:    "https://telemetry.local",
				CAFile:     broker.mqtt.CAFile,
				CertFile:   broker.mqtt.CertFile,
				KeyFile:    broker.mqtt.KeyFile,
				ServerName: "telemetry.local",
			},
			QueueCapacity: 4,
		},
		processor,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		nil,
		WithCoverageReporter(lister, telemetry),
	)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	runtime.coverageInterval = 25 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() {
		runErr <- runtime.Run(ctx)
	}()

	conn1 := broker.awaitConnection(t)
	waitCondition(t, 3*time.Second, func() bool {
		return runtime.Ready()
	}, "runtime readiness")

	waitCondition(t, 3*time.Second, func() bool {
		telemetry.mu.Lock()
		defer telemetry.mu.Unlock()
		availCount := 0
		for _, rep := range telemetry.coverageReports {
			if rep.Available {
				availCount++
			}
		}
		return availCount >= 2 // subscribe + at least 1 periodic heartbeat
	}, "subscribe and heartbeat coverage reports")

	// Drop connection abruptly and wait for connectionLost unavailable coverage report.
	_ = conn1.conn.Close()
	waitCondition(t, 3*time.Second, func() bool {
		telemetry.mu.Lock()
		defer telemetry.mu.Unlock()
		for _, rep := range telemetry.coverageReports {
			if !rep.Available && rep.ReasonCode != nil && *rep.ReasonCode == "SOURCE_UNAVAILABLE" {
				return true
			}
		}
		return false
	}, "connectionLost unavailable coverage report")

	_ = broker.awaitConnection(t)
	cancel()
	if err := <-runErr; err != nil {
		t.Fatalf("runtime.Run exit error: %v", err)
	}

	telemetry.mu.Lock()
	defer telemetry.mu.Unlock()
	last := telemetry.coverageReports[len(telemetry.coverageReports)-1]
	if last.Available || last.ReasonCode == nil || *last.ReasonCode != "SOURCE_UNAVAILABLE" {
		t.Fatalf("final shutdown coverage report = %+v, want unavailable SOURCE_UNAVAILABLE", last)
	}
}

func TestRuntimeGenerationGuardSuppressesStaleCoverageReports(t *testing.T) {
	identities := newFakeIdentities()
	telemetry := &fakeTelemetry{}
	processor, err := NewProcessor(identities, telemetry)
	if err != nil {
		t.Fatalf("NewProcessor: %v", err)
	}
	lister := &fakeCoverageLister{deviceIDs: []string{testDeviceID}}
	runtime, err := NewRuntime(
		Config{
			MQTT: MQTTConfig{
				BrokerURL: "tls://localhost:8883", ClientID: "connectivity-gen-test",
				CAFile: "ca.pem", CertFile: "client.pem", KeyFile: "client.key", ServerName: "mqtt.local",
			},
			TelemetryRuntime: TelemetryRuntimeConfig{
				BaseURL: "https://telemetry.local",
				CAFile:  "ca.pem", CertFile: "client.pem", KeyFile: "client.key", ServerName: "telemetry.local",
			},
			QueueCapacity: 4,
		},
		processor,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		nil,
		WithCoverageReporter(lister, telemetry),
	)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}

	gen1 := runtime.connGen.Add(1)
	gen2 := runtime.connGen.Add(1)
	runtime.recordConnectionState(nil, true, true, nil)

	// Stale generation 1 disconnect must be suppressed while generation 2 is active and Ready().
	runtime.reportCoverage(context.Background(), false, "SOURCE_UNAVAILABLE", gen1, false)
	// Even a non-ready stale generation 1 report must be suppressed after gen 2 starts.
	runtime.recordConnectionState(nil, false, false, nil)
	runtime.reportCoverage(context.Background(), false, "SOURCE_UNAVAILABLE", gen1, false)
	// Generation 2 connect report must also be suppressed while !Ready().
	runtime.reportCoverage(context.Background(), true, "", gen2, false)

	telemetry.mu.Lock()
	if len(telemetry.coverageReports) != 0 {
		t.Fatalf("expected 0 stale coverage reports, got %+v", telemetry.coverageReports)
	}
	telemetry.mu.Unlock()

	// Forced shutdown report always emits SOURCE_UNAVAILABLE.
	runtime.reportCoverage(context.Background(), false, "SOURCE_UNAVAILABLE", 0, true)
	telemetry.mu.Lock()
	defer telemetry.mu.Unlock()
	if len(telemetry.coverageReports) != 1 || telemetry.coverageReports[0].Available {
		t.Fatalf("forced shutdown coverage report missing: %+v", telemetry.coverageReports)
	}
}

func TestTelemetryRuntimeClientBatchAndCoverageHTTP(t *testing.T) {
	var seenBatch int
	var seenCoverage bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case sourceObservationBatchPath:
			var req observationBatchRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			seenBatch = len(req.Observations)
			receipts := make([]ObservationReceipt, len(req.Observations))
			for i := range receipts {
				receipts[i] = ObservationReceipt{Status: "ACCEPTED", Quality: "GOOD"}
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(observationBatchResponse{Receipts: receipts})
		case sourceCoveragePath:
			seenCoverage = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"accepted":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	client := &TelemetryRuntimeClient{
		baseURL: server.URL,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{RootCAs: roots},
			},
		},
	}

	receipts, err := client.AcceptObservationBatch(context.Background(), []Observation{
		{SourceID: testGatewayA, SourcePath: "PUSH", TelemetryKey: "temp", Value: 21.0, ValueType: "NUMBER"},
		{SourceID: testGatewayA, SourcePath: "PUSH", TelemetryKey: "hum", Value: 50.0, ValueType: "NUMBER"},
	})
	if err != nil || len(receipts) != 2 || seenBatch != 2 {
		t.Fatalf("AcceptObservationBatch = (%+v, %v), seenBatch=%d", receipts, err, seenBatch)
	}

	reason := "SOURCE_UNAVAILABLE"
	if err := client.ReportSourceCoverage(context.Background(), SourceCoverageReport{
		SourceID:   "connectivity-test",
		DeviceIDs:  []string{testDeviceID},
		Available:  false,
		ReasonCode: &reason,
	}); err != nil || !seenCoverage {
		t.Fatalf("ReportSourceCoverage err=%v seenCoverage=%v", err, seenCoverage)
	}
}

func waitCondition(t *testing.T, timeout time.Duration, cond func() bool, label string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s (%s)", label, fmt.Sprintf("after %s", timeout))
}
