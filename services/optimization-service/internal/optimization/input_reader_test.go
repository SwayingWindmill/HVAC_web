package optimization

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOptimizationInputReaderDoesNotFallbackToOlderGoodQuality(t *testing.T) {
	at := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		query, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		if strings.Contains(string(query), "quality='GOOD'") || strings.Contains(string(query), "quality = 'GOOD'") {
			t.Error("authoritative input query must not skip the latest non-GOOD fact")
		}
		writer.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = writer.Write([]byte(`{"result_id":"01990000-2840-7000-8000-000000000001","metric_version_id":"01990000-2810-7000-8000-000000000001","metric_code":"daily_energy","period_start":"2026-08-27T12:00:00.000Z","period_end":"2026-08-28T11:59:00.000Z","calculated_at":"2026-08-28T11:59:10.000Z","value":2400,"unit":"kWh","quality":"PARTIAL","revision":3}` + "\n"))
	}))
	defer server.Close()

	reader, err := NewClickHouseInputReader(InputReaderConfig{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	fact, err := reader.readMetric(t.Context(), OptimizationStateQuery{
		TenantID:    "018f1d00-0000-7000-8000-000000000001",
		SiteID:      "018f1e00-1000-7000-8000-000000000001",
		SubjectType: "SITE",
		SubjectID:   "018f1e00-1000-7000-8000-000000000001",
		At:          at,
	}, "daily_energy")
	if err != nil {
		t.Fatal(err)
	}
	if fact.Quality != "PARTIAL" {
		t.Fatalf("quality=%q", fact.Quality)
	}
	if err := validateMetricEvidence(fact, "daily_energy", at); err == nil {
		t.Fatal("latest non-GOOD authoritative fact must block Optimization preparation")
	}
}

func TestOptimizationTelemetryReaderDoesNotFallbackToOlderGoodQuality(t *testing.T) {
	at := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		query, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		if strings.Contains(string(query), "quality='GOOD'") || strings.Contains(string(query), "quality = 'GOOD'") {
			t.Error("authoritative input query must not skip the latest non-GOOD observation")
		}
		writer.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = writer.Write([]byte(`{"observation_id":"01990000-2940-7000-8000-000000000001","device_id":"01990000-2900-7000-8000-000000000001","point_id":"01990000-2910-7000-8000-000000000001","telemetry_key":"btu_meter.supply_water_temperature","point_revision":3,"sampled_at":"2026-08-28T11:59:40.000Z","received_at":"2026-08-28T11:59:41.000Z","value":7,"unit":"Cel","quality":"STALE","source_event_id":"01990000-2930-7000-8000-000000000001","source_partition":"optimization-owner","source_offset":2}` + "\n"))
	}))
	defer server.Close()

	reader, err := NewClickHouseInputReader(InputReaderConfig{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	observation, err := reader.readTelemetry(t.Context(), OptimizationStateQuery{
		TenantID:    "018f1d00-0000-7000-8000-000000000001",
		SiteID:      "018f1e00-1000-7000-8000-000000000001",
		SubjectType: "SITE",
		SubjectID:   "018f1e00-1000-7000-8000-000000000001",
		At:          at,
	}, "btu_meter.supply_water_temperature")
	if err != nil {
		t.Fatal(err)
	}
	if observation.Quality != "STALE" {
		t.Fatalf("quality=%q", observation.Quality)
	}
	if err := validateTelemetryEvidence(observation, "btu_meter.supply_water_temperature", at); err == nil {
		t.Fatal("latest non-GOOD authoritative observation must block Optimization preparation")
	}
}
