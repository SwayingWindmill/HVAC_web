package adapter

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestHTTPPipelineMovesThingsBoardTelemetryIntoS2OverMTLS(t *testing.T) {
	const sampleTimestamp = int64(1785225600000)
	thingsBoard := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("X-Authorization") != "Bearer local-test-jwt-1234567890" {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		if request.URL.Path != "/api/plugins/telemetry/DEVICE/tb-chiller-01/values/timeseries" {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"powerKw":[{"ts":1785225600000,"value":"212.5"}]}`)
	}))
	defer thingsBoard.Close()

	files, serverTLS := createTestPKI(t)
	var accepted Observation
	telemetryRuntimeServer := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.TLS == nil || len(request.TLS.PeerCertificates) == 0 || len(request.TLS.PeerCertificates[0].URIs) != 1 || request.TLS.PeerCertificates[0].URIs[0].String() != "spiffe://hvac.local/thingsboard-telemetry-adapter" {
			http.Error(writer, "untrusted", http.StatusUnauthorized)
			return
		}
		if err := json.NewDecoder(request.Body).Decode(&accepted); err != nil {
			http.Error(writer, "invalid", http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"status":"ACCEPTED","quality":"GOOD","qualityReasons":[],"stateChanged":true,"positionAdvanced":true}`))
	}))
	telemetryRuntimeServer.TLS = serverTLS
	telemetryRuntimeServer.StartTLS()
	defer telemetryRuntimeServer.Close()

	unit := "kW"
	config := validConfig()
	config.CheckpointFile = filepath.Join(t.TempDir(), "checkpoint.json")
	config.ThingsBoard.BaseURL = thingsBoard.URL
	config.TelemetryRuntime = TelemetryRuntimeConfig{
		BaseURL:    telemetryRuntimeServer.URL,
		CAFile:     files.ca,
		CertFile:   files.clientCert,
		KeyFile:    files.clientKey,
		ServerName: "localhost",
	}
	config.Devices = []DeviceMapping{{
		ThingsBoardDeviceID: "tb-chiller-01",
		ExternalID:          "tb-chiller-01",
		Points: []PointMapping{{
			SourceKey:    "powerKw",
			TelemetryKey: "chiller.power",
			ValueType:    "NUMBER",
			Unit:         &unit,
		}},
	}}

	tbClient, err := NewThingsBoardClient(thingsBoard.URL, tokenProviderFunc(func(context.Context) (string, error) {
		return "local-test-jwt-1234567890", nil
	}), thingsBoard.Client())
	if err != nil {
		t.Fatal(err)
	}
	s2HTTPClient, err := NewMTLSHTTPClient(config.TelemetryRuntime)
	if err != nil {
		t.Fatal(err)
	}
	s2Client, err := NewTelemetryRuntimeClient(telemetryRuntimeServer.URL, s2HTTPClient)
	if err != nil {
		t.Fatal(err)
	}
	checkpoints, err := OpenCheckpointStore(config.CheckpointFile)
	if err != nil {
		t.Fatal(err)
	}
	pipeline, err := NewPipeline(config, tbClient, s2Client, checkpoints, func() time.Time {
		return time.UnixMilli(sampleTimestamp + 1000)
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := pipeline.PollOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.ObservationCount != 1 || report.StatusCounts["ACCEPTED"] != 1 {
		t.Fatalf("unexpected report %#v", report)
	}
	if accepted.ExternalID != "tb-chiller-01" || accepted.TelemetryKey != "chiller.power" || accepted.Value != 212.5 || accepted.SourcePosition.Offset != sampleTimestamp {
		t.Fatalf("unexpected accepted observation %#v", accepted)
	}
	if !uuidV7Pattern.MatchString(accepted.SourcePosition.EventID) {
		t.Fatalf("invalid event UUIDv7 %q", accepted.SourcePosition.EventID)
	}
}
