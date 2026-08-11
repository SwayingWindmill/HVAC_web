package telemetry

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestClickHouseHistorySinkUsesObservationIdentityAsDeduplicationToken(t *testing.T) {
	observationIDs := []string{
		"018f2e00-9100-7000-8000-000000000001",
		"018f2e00-9100-7000-8000-000000000002",
	}
	requests := make(map[string]url.Values, len(observationIDs))
	rows := make(map[string]HistoryObservation, len(observationIDs))
	var mutex sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		username, password, ok := request.BasicAuth()
		if !ok || username != "telemetry_history" || password != "[REDACTED_SECRET]" {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		if request.Method != http.MethodPost || request.Header.Get("Content-Type") != "application/x-ndjson" {
			http.Error(writer, "invalid request", http.StatusBadRequest)
			return
		}
		var row HistoryObservation
		if err := json.NewDecoder(request.Body).Decode(&row); err != nil {
			http.Error(writer, "invalid row", http.StatusBadRequest)
			return
		}
		mutex.Lock()
		requests[row.ObservationID] = request.URL.Query()
		rows[row.ObservationID] = row
		mutex.Unlock()
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	sink, err := NewClickHouseHistorySink(ClickHouseHistoryConfig{
		BaseURL:    server.URL,
		Database:   "telemetry_history",
		Table:      "observations",
		Username:   "telemetry_history",
		Password:   "[REDACTED_SECRET]",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	number := 812.5
	observations := []HistoryObservation{
		{
			ObservationID: observationIDs[0], TenantID: stringPointer("018f2d00-0000-7000-8000-000000000001"),
			OwningOrganizationID: stringPointer("018f2e00-0000-7000-8000-000000000001"), SiteID: stringPointer("018f2e00-1000-7000-8000-000000000001"), DeviceID: stringPointer("018f2e00-3000-7000-8000-000000000001"),
			PointID: stringPointer("018f2e00-3100-7000-8000-000000000001"), SensorID: stringPointer("018f2e00-3200-7000-8000-000000000001"),
			IntegrationInstanceID: "018f2e00-6000-7000-8000-000000000001",
			SourceEventID: "018f2e00-6200-7000-8000-000000000001", SourcePartition: "tb-a", SourceOffset: 10,
			SourcePath: "POLL", TelemetryKey: "chiller.power", ValueNumber: &number,
			SampledAt: time.Date(2026, 7, 29, 7, 59, 58, 0, time.UTC), ReceivedAt: time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC),
			AcceptanceStatus: "ACCEPTED", Quality: "GOOD", QualityReasons: []string{}, PayloadSHA256: strings.Repeat("a", 64),
		},
		{
			ObservationID: observationIDs[1], IntegrationInstanceID: "018f2e00-6000-7000-8000-000000000001",
			SourceEventID: "018f2e00-6200-7000-8000-000000000002", SourcePartition: "tb-a", SourceOffset: 11,
			SourcePath: "POLL", TelemetryKey: "chiller.cop",
			SampledAt: time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC), ReceivedAt: time.Date(2026, 7, 29, 8, 0, 1, 0, time.UTC),
			AcceptanceStatus: "REJECTED", Quality: "REJECTED", QualityReasons: []string{"OUT_OF_RANGE"}, PayloadSHA256: strings.Repeat("b", 64),
		},
	}
	if err := sink.InsertObservations(t.Context(), observations); err != nil {
		t.Fatal(err)
	}
	mutex.Lock()
	defer mutex.Unlock()
	if len(requests) != 2 || len(rows) != 2 {
		t.Fatalf("requests=%d rows=%d", len(requests), len(rows))
	}
	for _, observationID := range observationIDs {
		values, ok := requests[observationID]
		if !ok {
			t.Fatalf("missing request for %s", observationID)
		}
		if values.Get("insert_deduplication_token") != observationID {
			t.Fatalf("dedup token=%q", values.Get("insert_deduplication_token"))
		}
		if values.Get("date_time_input_format") != "best_effort" {
			t.Fatalf("date time input format=%q", values.Get("date_time_input_format"))
		}
		if values.Get("async_insert") != "1" || values.Get("wait_for_async_insert") != "1" || values.Get("async_insert_deduplicate") != "1" {
			t.Fatalf("async insert settings=%v", values)
		}
		if query := values.Get("query"); query != "INSERT INTO telemetry_history.observations FORMAT JSONEachRow" {
			t.Fatalf("query=%q", query)
		}
	}
	first := rows[observations[0].ObservationID]
	if first.ObservationID != observations[0].ObservationID || first.ValueNumber == nil || *first.ValueNumber != number {
		t.Fatalf("first row=%#v", first)
	}
}

func TestClickHouseHistorySinkValidatesEntireBatchBeforeInsert(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	sink, err := NewClickHouseHistorySink(ClickHouseHistoryConfig{
		BaseURL: server.URL, Database: "telemetry_history", Table: "observations", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	err = sink.InsertObservations(t.Context(), []HistoryObservation{
		{ObservationID: "018f2e00-9100-7000-8000-000000000003", PayloadSHA256: strings.Repeat("c", 64)},
		{ObservationID: "018f2e00-9100-7000-8000-000000000004", PayloadSHA256: "INVALID"},
	})
	if err == nil || !strings.Contains(err.Error(), "lowercase SHA-256") {
		t.Fatalf("err=%v", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("requests=%d", requests.Load())
	}
}

func TestClickHouseHistorySinkRejectsAcceptedObservationWithoutTenantScope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	sink, err := NewClickHouseHistorySink(ClickHouseHistoryConfig{
		BaseURL: server.URL, Database: "telemetry_history", Table: "observations", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	err = sink.InsertObservations(t.Context(), []HistoryObservation{{
		ObservationID: "018f2e00-9100-7000-8000-000000000006", AcceptanceStatus: "ACCEPTED",
		PayloadSHA256: strings.Repeat("e", 64),
	}})
	if err == nil || !strings.Contains(err.Error(), "requires UUIDv7 Tenant") {
		t.Fatalf("err=%v", err)
	}
}

func TestClickHouseHistorySinkRejectsFailedInsert(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "readonly", http.StatusForbidden)
	}))
	defer server.Close()
	sink, err := NewClickHouseHistorySink(ClickHouseHistoryConfig{
		BaseURL: server.URL, Database: "telemetry_history", Table: "observations", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	err = sink.InsertObservations(t.Context(), []HistoryObservation{{
		ObservationID: "018f2e00-9100-7000-8000-000000000005",
		PayloadSHA256: strings.Repeat("d", 64),
	}})
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("err=%v", err)
	}
}
