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

	"github.com/quanlaihe/hvac-web/modules/energy/internal/energy"
)

func TestReaderListsCanonicalCounterDeltas(t *testing.T) {
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
			"FROM telemetry_history.counter_deltas",
			"LEFT ANTI JOIN analytics.energy_interval_facts",
			"fact.source_previous_observation_id = delta.previous_observation_id",
			"delta.transition_type IN ('INCREASE', 'UNCHANGED', 'RECOVERY', 'RESET', 'ROLLOVER')",
			"delta.source_event_id",
			"delta.previous_quality_reasons",
			"ORDER BY delta.sampled_at, delta.source_offset, delta.observation_id",
			"LIMIT 32",
			"FORMAT JSONEachRow",
		} {
			if !strings.Contains(query, required) {
				t.Fatalf("query missing %q:\n%s", required, query)
			}
		}
		writer.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = io.WriteString(writer, `{"previous_observation_id":"018f4e00-3000-7000-8000-000000000001","current_observation_id":"018f4e00-3000-7000-8000-000000000002","tenant_id":"018f4d00-0000-7000-8000-000000000001","site_id":"018f4e00-1000-7000-8000-000000000001","device_id":"018f4e00-2000-7000-8000-000000000001","point_id":"018f4e00-2100-7000-8000-000000000001","sensor_id":"018f4e00-2200-7000-8000-000000000001","telemetry_key":"site.energy.total","point_revision":3,"unit":"kWh","counter_decrease_mode":"RESET_TO_ZERO","counter_rollover_modulus":null,"previous_value":100.25,"previous_quality":"GOOD","previous_quality_reasons":[],"previous_sampled_at":"2026-07-29T12:55:00.000Z","current_sampled_at":"2026-07-29T13:00:00.000Z","current_received_at":"2026-07-29T13:00:01.000Z","current_quality":"PARTIAL","current_quality_reasons":["SOURCE_LAG_EXCEEDED"],"source_event_id":"018f4e00-3100-7000-8000-000000000001","source_partition":"telemetry-0","source_offset":1722258003000,"transition_type":"INCREASE","delta_value":2.75}`+"\n")
	}))
	defer server.Close()

	reader, err := NewReader(ReaderConfig{
		BaseURL: server.URL, SourceDatabase: "telemetry_history", SourceTable: "counter_deltas",
		AnalyticsDatabase: "analytics", AnalyticsTable: "energy_interval_facts",
		Username: "analytics_reader", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	deltas, err := reader.ListDeltas(context.Background(), 32)
	if err != nil {
		t.Fatal(err)
	}
	if len(deltas) != 1 {
		t.Fatalf("deltas=%#v", deltas)
	}
	delta := deltas[0]
	if delta.CurrentObservationID != "018f4e00-3000-7000-8000-000000000002" || delta.DeltaValue == nil || *delta.DeltaValue != 2.75 || delta.CurrentSourceOffset != 1722258003000 {
		t.Fatalf("delta=%#v", delta)
	}
	if delta.CurrentQuality != energy.SourceQualityPartial || !delta.CurrentSampledAt.Equal(time.Date(2026, 7, 29, 13, 0, 0, 0, time.UTC)) {
		t.Fatalf("delta=%#v", delta)
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
		for {
			var row map[string]any
			if err := decoder.Decode(&row); err == io.EOF {
				break
			} else if err != nil {
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
	fact, err := energy.BuildFact(validDelta(), validBinding(), time.Date(2026, 7, 29, 13, 0, 3, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if err := sink.InsertFacts(context.Background(), []energy.EnergyIntervalFact{fact}); err != nil {
		t.Fatal(err)
	}
	if len(captured) != 1 || captured[0]["source_current_observation_id"] != fact.CurrentObservationID || captured[0]["energy_kwh"] != 2.75 {
		t.Fatalf("captured=%#v", captured)
	}
}

func TestWriterRejectsInconsistentFactMetadata(t *testing.T) {
	fact, err := energy.BuildFact(validDelta(), validBinding(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	fact.DatasetRevision++
	if err := validateFact(fact); err == nil {
		t.Fatal("validateFact() error = nil")
	}
	fact, err = energy.BuildFact(validDelta(), validBinding(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	fact.DataWatermark = fact.DataWatermark.Add(time.Millisecond)
	if err := validateFact(fact); err == nil {
		t.Fatal("validateFact() error = nil")
	}
}

func TestReaderAndWriterRejectUnsafeConfiguration(t *testing.T) {
	if _, err := NewReader(ReaderConfig{BaseURL: "https://clickhouse.example/path", SourceDatabase: "telemetry_history", SourceTable: "counter_deltas", AnalyticsDatabase: "analytics", AnalyticsTable: "energy_interval_facts"}); err == nil {
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
	fact, err := energy.BuildFact(validDelta(), validBinding(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := sink.InsertFacts(context.Background(), []energy.EnergyIntervalFact{fact}); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("InsertFacts() error = %v", err)
	}
}

func validDelta() energy.CounterDelta {
	delta := 2.75
	return energy.CounterDelta{
		PreviousObservationID: "018f4e00-3000-7000-8000-000000000001",
		CurrentObservationID:  "018f4e00-3000-7000-8000-000000000002",
		TenantID:              "018f4d00-0000-7000-8000-000000000001", SiteID: "018f4e00-1000-7000-8000-000000000001",
		DeviceID: "018f4e00-2000-7000-8000-000000000001", PointID: "018f4e00-2100-7000-8000-000000000001",
		SensorID: "018f4e00-2200-7000-8000-000000000001", TelemetryKey: "site.energy.total", PointRevision: 3,
		Unit: "kWh", CounterDecreaseMode: "RESET_TO_ZERO", PreviousValue: 100.25,
		PreviousQuality: energy.SourceQualityGood, PreviousSampledAt: time.Date(2026, 7, 29, 12, 55, 0, 0, time.UTC),
		CurrentQuality: energy.SourceQualityGood, CurrentSampledAt: time.Date(2026, 7, 29, 13, 0, 0, 0, time.UTC),
		CurrentSourceEventID: "018f4e00-3100-7000-8000-000000000001", CurrentSourcePartition: "telemetry-0",
		CurrentSourceOffset: 1722258003000, TransitionType: energy.TransitionIncrease, DeltaValue: &delta,
	}
}

func validBinding() energy.BindingResolution {
	return energy.BindingResolution{
		Status: energy.BindingMatch, TenantID: "018f4d00-0000-7000-8000-000000000001", SiteID: "018f4e00-1000-7000-8000-000000000001",
		MeterID: "018f4e00-1100-7000-8000-000000000001", MeterBindingID: "018f4e00-1200-7000-8000-000000000001",
		TopologyVersionID: "018f4e00-1300-7000-8000-000000000001", BindingVersion: 4,
		EnergyTypeID: "018f4e00-1400-7000-8000-000000000001", EnergyType: energy.EnergyTypeElectricity,
		MeterRole: energy.MeterRolePrimary, Direction: "IMPORT", DeviceID: "018f4e00-2000-7000-8000-000000000001",
		PointID: "018f4e00-2100-7000-8000-000000000001", PointType: energy.PointTypeCounter,
		EffectiveFrom: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}
