package clickhouse

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/services/analytics-read-model-projector/internal/energy"
)

func TestReaderListsOnlyUnprojectedCumulativeEnergyCandidates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		username, _, ok := request.BasicAuth()
		if !ok || username != "analytics_reader" {
			t.Fatalf("BasicAuth username = %q, present = %v", username, ok)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		query := string(body)
		for _, required := range []string{
			"FROM telemetry_history.observations",
			"LEFT ANTI JOIN analytics.energy_interval_facts",
			"PARTITION BY tenant_id, owning_organization_id, site_id, point_id, sensor_id, device_id, telemetry_key",
			"telemetry_key = 'hvac_meter.energy'",
			"acceptance_status = 'ACCEPTED'",
			"AND isFinite(value_number)",
			"ORDER BY sampled_at, source_offset, observation_id",
			"LIMIT 32",
			"FORMAT JSONEachRow",
		} {
			if !strings.Contains(query, required) {
				t.Fatalf("query missing %q:\n%s", required, query)
			}
		}
		writer.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = io.WriteString(writer, `{"previous_observation_id":"018f4e00-3000-7000-8000-000000000001","current_observation_id":"018f4e00-3000-7000-8000-000000000002","tenant_id":"018f4d00-0000-7000-8000-000000000001","organization_id":"018f4e00-0000-7000-8000-000000000001","site_id":"018f4e00-1000-7000-8000-000000000001","device_id":"018f4e00-2000-7000-8000-000000000001","point_id":"018f4e00-2100-7000-8000-000000000001","sensor_id":"018f4e00-2200-7000-8000-000000000001","telemetry_key":"hvac_meter.energy","previous_value":100.25,"current_value":103,"previous_quality":"GOOD","current_quality":"SUSPECT","previous_quality_reasons":[],"current_quality_reasons":["SOURCE_LAG_EXCEEDED"],"previous_sampled_at":"2026-07-29T12:55:00.000Z","current_sampled_at":"2026-07-29T13:00:00.000Z","source_offset":1722258003000}`+"\n")
	}))
	defer server.Close()

	reader, err := NewReader(ReaderConfig{
		BaseURL: server.URL, SourceDatabase: "telemetry_history", SourceTable: "observations",
		AnalyticsDatabase: "analytics", AnalyticsTable: "energy_interval_facts",
		Username: "analytics_reader", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := reader.ListCandidates(context.Background(), 32)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates=%#v", candidates)
	}
	candidate := candidates[0]
	if candidate.CurrentObservationID != "018f4e00-3000-7000-8000-000000000002" || candidate.CurrentValue != 103 || candidate.SourceOffset != 1722258003000 {
		t.Fatalf("candidate=%#v", candidate)
	}
	if candidate.CurrentQuality != energy.SourceQualitySuspect || !candidate.CurrentSampledAt.Equal(time.Date(2026, 7, 29, 13, 0, 0, 0, time.UTC)) {
		t.Fatalf("candidate=%#v", candidate)
	}
}

func TestWriterBatchesEnergyFactsWithDeterministicDeduplication(t *testing.T) {
	var captured []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		username, _, ok := request.BasicAuth()
		if !ok || username != "analytics_writer" {
			t.Fatalf("BasicAuth username = %q, present = %v", username, ok)
		}
		query, err := url.QueryUnescape(request.URL.Query().Get("query"))
		if err != nil {
			t.Fatal(err)
		}
		if query != "INSERT INTO analytics.energy_interval_facts FORMAT JSONEachRow" {
			t.Fatalf("query=%q", query)
		}
		if token := request.URL.Query().Get("insert_deduplication_token"); len(token) != 64 {
			t.Fatalf("dedup token=%q", token)
		}
		decoder := json.NewDecoder(request.Body)
		for decoder.More() {
			var row map[string]any
			if err := decoder.Decode(&row); err != nil {
				t.Fatal(err)
			}
			captured = append(captured, row)
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	sink, err := NewWriter(WriterConfig{
		BaseURL: server.URL, Database: "analytics", Table: "energy_interval_facts",
		Username: "analytics_writer", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	fact, err := energy.BuildFact(validCandidate(), time.Date(2026, 7, 29, 13, 0, 3, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if err := sink.InsertFacts(context.Background(), []energy.Fact{fact}); err != nil {
		t.Fatal(err)
	}
	if len(captured) != 1 || captured[0]["source_current_observation_id"] != fact.SourceCurrentObservationID || captured[0]["energy_kwh"] != 2.75 {
		t.Fatalf("captured=%#v", captured)
	}
}

func TestWriterRejectsInconsistentFactMetadata(t *testing.T) {
	fact, err := energy.BuildFact(validCandidate(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	fact.DatasetRevision++
	if err := validateFact(fact); err == nil {
		t.Fatal("validateFact() error = nil")
	}
	fact, err = energy.BuildFact(validCandidate(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	fact.DataWatermark = fact.DataWatermark.Add(time.Millisecond)
	if err := validateFact(fact); err == nil {
		t.Fatal("validateFact() error = nil")
	}
}

func TestReaderAndWriterRejectUnsafeConfiguration(t *testing.T) {
	if _, err := NewReader(ReaderConfig{BaseURL: "https://clickhouse.example/path", SourceDatabase: "telemetry_history", SourceTable: "observations", AnalyticsDatabase: "analytics", AnalyticsTable: "energy_interval_facts"}); err == nil {
		t.Fatal("NewReader() error = nil")
	}
	if _, err := NewWriter(WriterConfig{BaseURL: "https://clickhouse.example", Database: "analytics; DROP DATABASE analytics", Table: "energy_interval_facts"}); err == nil {
		t.Fatal("NewWriter() error = nil")
	}
}

func TestWriterSurfacesClickHouseFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "table is read only", http.StatusForbidden)
	}))
	defer server.Close()
	sink, err := NewWriter(WriterConfig{BaseURL: server.URL, Database: "analytics", Table: "energy_interval_facts", HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	fact, err := energy.BuildFact(validCandidate(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := sink.InsertFacts(context.Background(), []energy.Fact{fact}); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("InsertFacts() error = %v", err)
	}
}

func validCandidate() energy.Candidate {
	sensorID := "018f4e00-2200-7000-8000-000000000001"
	return energy.Candidate{
		PreviousObservationID: "018f4e00-3000-7000-8000-000000000001",
		CurrentObservationID:  "018f4e00-3000-7000-8000-000000000002",
		TenantID:              "018f4d00-0000-7000-8000-000000000001",
		OrganizationID:        "018f4e00-0000-7000-8000-000000000001",
		SiteID:                "018f4e00-1000-7000-8000-000000000001",
		DeviceID:              "018f4e00-2000-7000-8000-000000000001",
		PointID:               "018f4e00-2100-7000-8000-000000000001",
		SensorID:              &sensorID,
		TelemetryKey:          energy.CumulativeElectricityTelemetryKey,
		PreviousValue:         100.25,
		CurrentValue:          103,
		PreviousQuality:       energy.SourceQualityGood,
		CurrentQuality:        energy.SourceQualityGood,
		PreviousSampledAt:     time.Date(2026, 7, 29, 12, 55, 0, 0, time.UTC),
		CurrentSampledAt:      time.Date(2026, 7, 29, 13, 0, 0, 0, time.UTC),
		SourceOffset:          1722258003000,
	}
}
