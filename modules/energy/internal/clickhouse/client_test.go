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

func TestWriterInsertDeduplicationTokenIgnoresRebuildRunID(t *testing.T) {
	var tokens []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		tokens = append(tokens, request.URL.Query().Get("insert_deduplication_token"))
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
	fact.FactRevision = 2
	run1 := "01990000-2a00-7000-8000-000000000001"
	run2 := "01990000-2a00-7000-8000-000000000002"
	fact.RebuildRunID = &run1
	if err := sink.InsertFacts(context.Background(), []energy.EnergyIntervalFact{fact}); err != nil {
		t.Fatal(err)
	}
	fact.RebuildRunID = &run2
	if err := sink.InsertFacts(context.Background(), []energy.EnergyIntervalFact{fact}); err != nil {
		t.Fatal(err)
	}
	fact.FactRevision = 3
	if err := sink.InsertFacts(context.Background(), []energy.EnergyIntervalFact{fact}); err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 3 {
		t.Fatalf("tokens=%v, want 3 entries", tokens)
	}
	if tokens[0] != tokens[1] {
		t.Fatalf("expected identical dedup token across RebuildRunID retries: %q vs %q", tokens[0], tokens[1])
	}
	if tokens[1] == tokens[2] {
		t.Fatalf("expected different dedup token for next revision: %q", tokens[2])
	}
}

func TestWriterAllowsInvalidationFactRevisionAndRejectsInitialInvalidDecrease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
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

	delta := validDelta()
	delta.TransitionType = energy.TransitionInvalidDecrease
	delta.DeltaValue = nil
	delta.ExistingFactFound = true
	delta.ExistingFactEnergyKWh = 2.75
	delta.ExistingFactTransitionType = energy.TransitionIncrease
	delta.ExistingFactQuality = energy.FactQualityValid
	delta.ExistingFactRevision = 0

	fact, err := energy.BuildFact(delta, validBinding(), time.Date(2026, 7, 29, 13, 0, 3, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if err := sink.InsertFacts(context.Background(), []energy.EnergyIntervalFact{fact}); err == nil {
		t.Fatal("expected rev-0 invalidation fact without incremented revision to be rejected")
	}
	fact.FactRevision = 1
	if err := sink.InsertFacts(context.Background(), []energy.EnergyIntervalFact{fact}); err != nil {
		t.Fatalf("expected rev-1 invalidation fact to succeed, got %v", err)
	}
}

func TestReaderQueriesCheckDeltaValueInvalidationAndWindowBoundaries(t *testing.T) {
	var seenWindowParams url.Values
	var seenWindowSQL string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		seenWindowSQL = string(body)
		seenWindowParams = request.URL.Query()
		_, _ = io.WriteString(writer, `{"previous_observation_id":"018f4e00-3000-7000-8000-000000000001","current_observation_id":"018f4e00-3000-7000-8000-000000000002","tenant_id":"018f4d00-0000-7000-8000-000000000001","site_id":"018f4e00-1000-7000-8000-000000000001","device_id":"018f4e00-2000-7000-8000-000000000001","point_id":"018f4e00-2100-7000-8000-000000000001","sensor_id":null,"telemetry_key":"site.energy.total","point_revision":3,"unit":"kWh","counter_decrease_mode":"RESET_TO_ZERO","counter_rollover_modulus":null,"previous_value":100,"previous_quality":"GOOD","previous_quality_reasons":[],"previous_sampled_at":"2026-07-29T12:00:00Z","current_sampled_at":"2026-07-29T13:30:00Z","current_received_at":"2026-07-29T13:30:05Z","current_quality":"GOOD","current_quality_reasons":[],"source_event_id":"018f4e00-3100-7000-8000-000000000001","source_partition":"telemetry-0","source_offset":42,"transition_type":"INVALID_DECREASE","delta_value":null,"existing_fact_found":1,"existing_fact_meter_binding_id":"018f4e00-1200-7000-8000-000000000001","existing_fact_previous_observation_id":"018f4e00-3000-7000-8000-000000000001","existing_fact_energy_kwh":12.5,"existing_fact_transition_type":"INCREASE","existing_fact_quality":"VALID","existing_fact_revision":1}`+"\n")
	}))
	defer server.Close()

	reader, err := NewReader(ReaderConfig{
		BaseURL: server.URL, SourceDatabase: "telemetry_history",
		AnalyticsDatabase: "analytics", AnalyticsTable: "energy_interval_facts",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	counterSQL := reader.counterDeltaQuery()
	for _, needle := range []string{
		"existing_fact_energy_kwh",
		"existing_fact_transition_type",
		"existing_fact_quality",
		"abs(ifNull(existing.existing_fact_energy_kwh, 0.0) - ifNull(delta.delta_value, 0.0)) > 1e-6",
		"existing.existing_fact_quality != 'INVALID'",
		"delta.transition_type IN ('INCREASE', 'UNCHANGED', 'RECOVERY', 'RESET', 'ROLLOVER')",
	} {
		if !strings.Contains(counterSQL, needle) {
			t.Fatalf("counterDeltaQuery missing %q:\n%s", needle, counterSQL)
		}
	}
	if strings.Contains(strings.ToUpper(counterSQL), "ALTER TABLE") {
		t.Fatalf("unexpected ClickHouse mutation in counterDeltaQuery:\n%s", counterSQL)
	}

	window := energy.DirtyWindow{
		TenantID: "018f4d00-0000-7000-8000-000000000001",
		SiteID:   "018f4e00-1000-7000-8000-000000000001",
		PointIDs: []string{"018f4e00-2100-7000-8000-000000000001"},
		Start:    time.Date(2026, 7, 29, 13, 0, 0, 0, time.UTC),
		End:      time.Date(2026, 7, 29, 14, 0, 0, 0, time.UTC),
	}
	deltas, err := reader.WindowDeltas(context.Background(), window)
	if err != nil {
		t.Fatal(err)
	}
	if len(deltas) != 1 || deltas[0].ExistingFactEnergyKWh != 12.5 || deltas[0].ExistingFactQuality != energy.FactQualityValid || deltas[0].DeltaValue != nil {
		t.Fatalf("unexpected decoded window deltas: %+v", deltas)
	}
	if !strings.Contains(seenWindowSQL, "delta.sampled_at > {until:DateTime64(3, 'UTC')}") {
		t.Fatalf("windowDeltaQuery missing T_end+1 successor guard:\n%s", seenWindowSQL)
	}
	if seenWindowParams.Get("param_since") != "2026-07-29 13:00:00.000" || seenWindowParams.Get("param_until") != "2026-07-29 14:00:00.000" {
		t.Fatalf("unexpected window params: %v", seenWindowParams)
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

func TestWriterInsertsHourlyFactsV2AndAvoidsDeleteMutations(t *testing.T) {
	var queries []string
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		q, _ := url.QueryUnescape(request.URL.Query().Get("query"))
		queries = append(queries, q)
		raw, _ := io.ReadAll(request.Body)
		bodies = append(bodies, string(raw))
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

	site := energy.PricedSite{TenantID: "018f4d00-0000-7000-8000-000000000001", SiteID: "018f4e00-1000-7000-8000-000000000001"}
	if err := sink.DropPriceWindowsBefore(context.Background(), energy.PriceWindowSync{PricedSite: site, Generation: 9}); err != nil {
		t.Fatalf("DropPriceWindowsBefore failed: %v", err)
	}
	if err := sink.DropTotalSubmetersBefore(context.Background(), energy.TotalSubmeterSync{PricedSite: site, Generation: 9}); err != nil {
		t.Fatalf("DropTotalSubmetersBefore failed: %v", err)
	}
	if len(queries) != 0 {
		t.Fatalf("expected append-only Drop*Before no-ops to issue 0 ClickHouse mutations, got %v", queries)
	}

	hour := time.Date(2026, 7, 29, 13, 0, 0, 0, time.UTC)
	err = sink.InsertHourlyFactsV2(context.Background(), []energy.HourlyFactV2{{
		TenantID:        site.TenantID,
		SiteID:          site.SiteID,
		EnergyType:      energy.EnergyTypeElectricity,
		AccountingScope: energy.AccountingScopeSite,
		PointID:         "018f4e00-2100-7000-8000-000000000001",
		Quality:         energy.FactQualityValid,
		TariffPeriod:    "PEAK",
		Hour:            hour,
		EnergyKWh:       12.345678,
		CostCNY:         15.432098,
		IntervalCount:   12,
		CalcVersion:     3,
		DataWatermark:   hour.Add(55 * time.Minute),
		DatasetRevision: 42,
		UpdatedAt:       hour.Add(time.Hour),
	}})
	if err != nil {
		t.Fatalf("InsertHourlyFactsV2 failed: %v", err)
	}
	if len(queries) != 1 || !strings.Contains(queries[0], "analytics.energy_hourly_facts_v2") {
		t.Fatalf("unexpected query list: %v", queries)
	}
	if !strings.Contains(bodies[0], `"energy_kwh":"12.345678"`) ||
		!strings.Contains(bodies[0], `"cost_cny":"15.432098"`) ||
		!strings.Contains(bodies[0], `"calc_version":3`) ||
		!strings.Contains(bodies[0], `"load_device_id":"00000000-0000-0000-0000-000000000000"`) {
		t.Fatalf("unexpected Decimal64/calc_version payload: %s", bodies[0])
	}
}

func TestReaderHourlyPowerProfileQueriesUTCAndOptionalDeviceFilter(t *testing.T) {
	var seenSQL []string
	var seenParams []url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seenSQL = append(seenSQL, string(body))
		seenParams = append(seenParams, r.URL.Query())
		_, _ = io.WriteString(w, `{"hour_start":"2026-10-12T13:00:00Z","weight":120.5}`+"\n"+
			`{"hour_start":"2026-10-12T14:00:00Z","weight":240.25}`+"\n")
	}))
	defer server.Close()

	reader, err := NewReader(ReaderConfig{
		BaseURL: server.URL, SourceDatabase: "telemetry_history",
		AnalyticsDatabase: "analytics", AnalyticsTable: "energy_interval_facts",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}

	tenantID := "018f4d00-0000-7000-8000-000000000001"
	siteID := "018f4e00-1000-7000-8000-000000000001"
	deviceID := "018f4e00-2000-7000-8000-000000000001"
	from := time.Date(2026, 10, 12, 13, 0, 0, 0, time.UTC)
	to := from.Add(2 * time.Hour)

	weights, err := reader.HourlyPowerProfile(context.Background(), tenantID, siteID, deviceID, from, to)
	if err != nil {
		t.Fatalf("HourlyPowerProfile(device) failed: %v", err)
	}
	if len(weights) != 2 || weights[0].Weight != 120.5 || weights[1].Weight != 240.25 ||
		!weights[0].HourStart.Equal(from) || !weights[1].HourStart.Equal(from.Add(time.Hour)) {
		t.Fatalf("unexpected weights: %+v", weights)
	}

	// Empty deviceID omits device_id predicate.
	if _, err := reader.HourlyPowerProfile(context.Background(), tenantID, siteID, "", from, to); err != nil {
		t.Fatalf("HourlyPowerProfile(site-wide) failed: %v", err)
	}
	if len(seenSQL) != 2 {
		t.Fatalf("expected 2 queries, got %d", len(seenSQL))
	}
	for _, needle := range []string{
		"FROM analytics.hourly_active_power_profile AS p",
		"p.tenant_id = {tenant:UUID}",
		"p.site_id = {site:UUID}",
		"p.hour_utc >= {from:DateTime64(3, 'UTC')}",
		"p.hour_utc < {to:DateTime64(3, 'UTC')}",
		"sum(p.mean_kw * toFloat64(p.sample_count)) / nullIf(toFloat64(sum(p.sample_count)), 0) AS weight",
		"GROUP BY p.hour_utc",
	} {
		if !strings.Contains(seenSQL[0], needle) {
			t.Fatalf("HourlyPowerProfile SQL missing %q:\n%s", needle, seenSQL[0])
		}
	}
	if strings.Contains(seenSQL[0], "telemetry_history.observations") {
		t.Fatalf("HourlyPowerProfile must query SQL SECURITY DEFINER view, not telemetry_history.observations:\n%s", seenSQL[0])
	}
	if !strings.Contains(seenSQL[0], "p.device_id = {device:UUID}") || seenParams[0].Get("param_device") != deviceID {
		t.Fatalf("expected device_id filter on device-scoped query, sql=%s params=%v", seenSQL[0], seenParams[0])
	}
	if strings.Contains(seenSQL[1], "p.device_id") || seenParams[1].Get("param_device") != "" {
		t.Fatalf("unexpected device_id filter when deviceID is empty, sql=%s params=%v", seenSQL[1], seenParams[1])
	}
	// Ensure no alias shadowing on hour_utc/mean_kw/sample_count.
	if strings.Contains(seenSQL[0], "AS hour_utc") || strings.Contains(seenSQL[0], "AS mean_kw") || strings.Contains(seenSQL[0], "AS sample_count") {
		t.Fatalf("ClickHouse alias shadowing detected in SQL:\n%s", seenSQL[0])
	}
	if _, err := reader.HourlyPowerProfile(context.Background(), "", siteID, "", from, to); err == nil {
		t.Fatal("expected error for missing tenantID")
	}
	if _, err := reader.HourlyPowerProfile(context.Background(), tenantID, siteID, "", to, from); err == nil {
		t.Fatal("expected error for unordered window")
	}
}

func TestReaderDeduplicatesDeltaFilterArrays(t *testing.T) {
	tenantID := "018f4d00-0000-7000-8000-000000000001"
	siteID := "018f4e00-1000-7000-8000-000000000001"
	pointA := "018f4e00-2100-7000-8000-000000000001"
	pointB := "018f4e00-2100-7000-8000-000000000002"
	var seenNextBatchParams url.Values
	var seenWindowParams url.Values
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		sql := string(body)
		query := request.URL.Query()
		switch {
		case strings.Contains(sql, "energy_projection_checkpoints"):
		case strings.Contains(sql, "counter_arrivals"):
			for _, row := range []map[string]any{
				{"history_sequence": 100, "observation_id": "018f4e00-3000-7000-8000-000000000001", "tenant_id": tenantID, "site_id": siteID, "point_id": pointA, "sampled_at": "2026-10-13 04:00:00.000"},
				{"history_sequence": 101, "observation_id": "018f4e00-3000-7000-8000-000000000002", "tenant_id": tenantID, "site_id": siteID, "point_id": pointA, "sampled_at": "2026-10-13 04:01:00.000"},
				{"history_sequence": 102, "observation_id": "018f4e00-3000-7000-8000-000000000003", "tenant_id": tenantID, "site_id": siteID, "point_id": pointB, "sampled_at": "2026-10-13 04:02:00.000"},
				{"history_sequence": 103, "observation_id": "018f4e00-3000-7000-8000-000000000004", "tenant_id": tenantID, "site_id": siteID, "point_id": pointA, "sampled_at": "2026-10-13 04:03:00.000"},
			} {
				if err := json.NewEncoder(writer).Encode(row); err != nil {
					t.Error(err)
				}
			}
		case strings.Contains(sql, "has({observations:Array(UUID)}"):
			seenNextBatchParams = query
		case strings.Contains(sql, "counter_deltas_from"):
			seenWindowParams = query
		default:
			t.Errorf("unexpected query %s", sql)
		}
	}))
	defer server.Close()

	reader, err := NewReader(ReaderConfig{
		BaseURL: server.URL, SourceDatabase: "telemetry_history",
		AnalyticsDatabase: "analytics", AnalyticsTable: "energy_interval_facts",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.NextBatch(context.Background(), 256); err != nil {
		t.Fatalf("NextBatch() error = %v", err)
	}
	if got := seenNextBatchParams.Get("param_tenants"); got != "['"+tenantID+"']" {
		t.Fatalf("param_tenants = %s, want deduplicated single tenant", got)
	}
	if got := seenNextBatchParams.Get("param_sites"); got != "['"+siteID+"']" {
		t.Fatalf("param_sites = %s, want deduplicated single site", got)
	}
	if got := seenNextBatchParams.Get("param_points"); got != "['"+pointA+"','"+pointB+"']" {
		t.Fatalf("param_points = %s, want deduplicated points", got)
	}
	if got := seenNextBatchParams.Get("param_observations"); got != "['018f4e00-3000-7000-8000-000000000001','018f4e00-3000-7000-8000-000000000002','018f4e00-3000-7000-8000-000000000003','018f4e00-3000-7000-8000-000000000004']" {
		t.Fatalf("param_observations = %s, want all 4 observations kept", got)
	}

	start := time.Date(2026, 10, 13, 4, 0, 0, 0, time.UTC)
	if _, err := reader.WindowDeltas(context.Background(), energy.DirtyWindow{
		TenantID: tenantID,
		SiteID:   siteID,
		PointIDs: []string{pointA, pointB, pointA},
		Start:    start,
		End:      start.Add(time.Hour),
	}); err != nil {
		t.Fatalf("WindowDeltas() error = %v", err)
	}
	if got := seenWindowParams.Get("param_points"); got != "['"+pointA+"','"+pointB+"']" {
		t.Fatalf("WindowDeltas param_points = %s, want deduplicated points", got)
	}
}
