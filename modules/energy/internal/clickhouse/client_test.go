package clickhouse

import (
	"compress/gzip"
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

func TestWriterInsertsCoolingMeterFacts(t *testing.T) {
	var energyType any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var row map[string]any
		if err := json.NewDecoder(request.Body).Decode(&row); err != nil {
			t.Fatal(err)
		}
		energyType = row["energy_type"]
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	sink, err := NewWriter(WriterConfig{BaseURL: server.URL, Database: "analytics", Table: "energy_interval_facts", HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	binding := validBinding()
	binding.EnergyType = "cooling"
	fact, err := energy.BuildFact(validDelta(), binding, time.Date(2026, 7, 29, 13, 0, 3, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if err := sink.InsertFacts(context.Background(), []energy.EnergyIntervalFact{fact}); err != nil {
		t.Fatal(err)
	}
	if energyType != "cooling" {
		t.Fatalf("energy_type=%v", energyType)
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
	if _, err := NewReader(ReaderConfig{BaseURL: "https://clickhouse.example/path", SourceDatabase: "telemetry_history", AnalyticsDatabase: "analytics", AnalyticsTable: "energy_interval_facts"}); err == nil {
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

// A batch mixing weeks-old replayed history with live arrivals reads its deltas window by
// window, so no query sorts the weeks between them.
func TestReaderBoundsEachDeltaQueryToItsArrivalsSampleSpan(t *testing.T) {
	arrivals := []struct{ id, sampledAt string }{
		{"018f4e00-3000-7000-8000-000000000011", "2026-10-13 04:41:57.164"},
		{"018f4e00-3000-7000-8000-000000000012", "2026-08-24 16:00:00.000"},
		{"018f4e00-3000-7000-8000-000000000013", "2026-09-14 13:53:00.000"},
		{"018f4e00-3000-7000-8000-000000000014", "2026-08-24 16:35:00.000"},
	}
	type window struct{ since, until, observations string }
	var windows []window
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		query := request.URL.Query()
		switch {
		case strings.Contains(string(body), "energy_projection_checkpoints"):
		case strings.Contains(string(body), "counter_arrivals"):
			for _, arrival := range arrivals {
				if err := json.NewEncoder(writer).Encode(map[string]any{
					"history_sequence": 9540, "observation_id": arrival.id, "tenant_id": "018f4d00-0000-7000-8000-000000000001",
					"site_id": "018f4e00-1000-7000-8000-000000000001", "point_id": "018f4e00-2100-7000-8000-000000000001",
					"sampled_at": arrival.sampledAt,
				}); err != nil {
					t.Error(err)
				}
			}
		case strings.Contains(string(body), "counter_deltas_from"):
			windows = append(windows, window{query.Get("param_since"), query.Get("param_until"), query.Get("param_observations")})
		default:
			t.Errorf("unexpected query %s", body)
		}
	}))
	defer server.Close()
	reader, err := NewReader(ReaderConfig{BaseURL: server.URL, SourceDatabase: "telemetry_history", AnalyticsDatabase: "analytics", AnalyticsTable: "energy_interval_facts", HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	batch, err := reader.NextBatch(context.Background(), 256)
	if err != nil {
		t.Fatal(err)
	}
	want := []window{
		{"2026-08-24 16:00:00.000", "2026-08-24 16:35:00.000", "['" + arrivals[1].id + "','" + arrivals[3].id + "']"},
		{"2026-09-14 13:53:00.000", "2026-09-14 13:53:00.000", "['" + arrivals[2].id + "']"},
		{"2026-10-13 04:41:57.164", "2026-10-13 04:41:57.164", "['" + arrivals[0].id + "']"},
	}
	if len(windows) != len(want) {
		t.Fatalf("delta queries = %+v, want %+v", windows, want)
	}
	for index := range want {
		if windows[index] != want[index] {
			t.Fatalf("delta query %d = %+v, want %+v", index, windows[index], want[index])
		}
	}
	if batch.Through == nil || batch.Through.ObservationID != arrivals[3].id {
		t.Fatalf("through = %+v", batch.Through)
	}
}

func TestWriterAdvanceCheckpointEnablesAsyncInsertAndPooledTransport(t *testing.T) {
	var seenQuery url.Values
	var seenBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		seenQuery = request.URL.Query()
		_ = json.NewDecoder(request.Body).Decode(&seenBody)
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	writer, err := NewWriter(WriterConfig{
		BaseURL: server.URL, Database: "analytics", Table: "energy_interval_facts", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	err = writer.AdvanceCheckpoint(context.Background(), energy.ProjectionCursor{
		HistorySequence: 42,
		ObservationID:   "018f4e00-3000-7000-8000-000000000099",
	})
	if err != nil {
		t.Fatal(err)
	}
	if seenQuery.Get("async_insert") != "1" || seenQuery.Get("wait_for_async_insert") != "1" || seenQuery.Get("wait_end_of_query") != "1" {
		t.Fatalf("checkpoint query params=%v", seenQuery)
	}
	if seenBody["projection_name"] != energyProjection {
		t.Fatalf("checkpoint body=%v", seenBody)
	}

	reader, err := NewReader(ReaderConfig{
		BaseURL: "http://127.0.0.1:8123", SourceDatabase: "telemetry_history",
		AnalyticsDatabase: "analytics", AnalyticsTable: "energy_interval_facts",
	})
	if err != nil {
		t.Fatal(err)
	}
	transport, ok := reader.httpClient.Transport.(*http.Transport)
	if !ok || transport == nil || transport.MaxConnsPerHost != 16 || transport.MaxIdleConns != 64 || transport.MaxIdleConnsPerHost != 16 || transport.IdleConnTimeout != 90*time.Second || transport.ResponseHeaderTimeout != 30*time.Second || transport.DialContext == nil {
		t.Fatalf("unexpected reader transport=%#v", reader.httpClient.Transport)
	}

	deltaSQL := reader.counterDeltaQuery()
	if !strings.Contains(deltaSQL, "delta.sampled_at > delta.previous_sampled_at") {
		t.Fatalf("counterDeltaQuery missing elapsed timestamp guard:\n%s", deltaSQL)
	}
}

func TestWriterAppendsRebuildEventWithDeduplication(t *testing.T) {
	var seenQueries []url.Values
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		seenQueries = append(seenQueries, request.URL.Query())
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	sink, err := NewWriter(WriterConfig{
		BaseURL: server.URL, Database: "analytics", Table: "energy_interval_facts",
		Username: "analytics_projector_writer", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	recorded := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	event := energy.RebuildEvent{
		EventID:    "01990000-2a00-7000-8000-000000000901",
		RunID:      "01990000-2a00-7000-8000-000000000900",
		TenantID:   "018f4d00-0000-7000-8000-000000000001",
		SiteID:     "018f4e00-1000-7000-8000-000000000001",
		ScopeType:  energy.RebuildScopeTransitionCorrections,
		ReasonCode: energy.RebuildReasonLateArrival,
		TriggerRef: "seq:10",
		EventType:  energy.RebuildEventRunStarted,
		Detail:     "started",
		RecordedAt: recorded,
	}
	if err := sink.AppendRebuildEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	event.RecordedAt = recorded.In(time.FixedZone("UTC+8", 8*3600))
	if err := sink.AppendRebuildEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if len(seenQueries) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(seenQueries))
	}
	tok0 := seenQueries[0].Get("insert_deduplication_token")
	tok1 := seenQueries[1].Get("insert_deduplication_token")
	if len(tok0) != 64 || tok0 != tok1 {
		t.Fatalf("expected matching 64-hex dedup tokens, got %q vs %q", tok0, tok1)
	}
	if seenQueries[0].Get("async_insert_deduplicate") != "1" || seenQueries[0].Get("insert_deduplicate") != "1" {
		t.Fatalf("missing dedup params: %v", seenQueries[0])
	}
}

func TestReaderPingAndGzipSelectDecompression(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "reader_user" || pass != "reader_pass" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		q := r.URL.Query()
		if q.Get("query") == "SELECT 1" {
			_, _ = w.Write([]byte("1\n"))
			return
		}
		if q.Get("enable_http_compression") != "1" || r.Header.Get("Accept-Encoding") != "gzip" {
			t.Fatalf("expected gzip request negotiation, query=%v accept=%q", q, r.Header.Get("Accept-Encoding"))
		}
		w.Header().Set("Content-Encoding", "gzip")
		gz := gzip.NewWriter(w)
		_, _ = gz.Write([]byte(`{"tenant_id":"018f4d00-0000-7000-8000-000000000001","site_id":"018f4e00-1000-7000-8000-000000000001"}` + "\n"))
		_ = gz.Close()
	}))
	defer server.Close()

	reader, err := NewReader(ReaderConfig{
		BaseURL:           server.URL,
		SourceDatabase:    "telemetry_history",
		AnalyticsDatabase: "analytics",
		AnalyticsTable:    "energy_interval_facts",
		Username:          "reader_user",
		Password:          "reader_pass",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Ping(context.Background()); err != nil {
		t.Fatalf("Reader.Ping failed: %v", err)
	}
	sites, err := reader.PricedSites(context.Background(), time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("PricedSites with gzip failed: %v", err)
	}
	if len(sites) != 1 || sites[0].SiteID != "018f4e00-1000-7000-8000-000000000001" {
		t.Fatalf("unexpected sites=%+v", sites)
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
		MeterRole: energy.MeterRolePrimary, Direction: "IMPORT", AccountingScope: energy.AccountingScopeSite, DeviceID: "018f4e00-2000-7000-8000-000000000001",
		PointID: "018f4e00-2100-7000-8000-000000000001", PointType: energy.PointTypeCounter,
		EffectiveFrom: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}
