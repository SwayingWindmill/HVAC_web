package clickhouse

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/modules/energy/internal/energy"
)

func TestWriterInsertConditionAnchorsEmptyNoOp(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	writer, err := NewWriter(WriterConfig{
		BaseURL: server.URL, Database: "analytics", Table: "energy_interval_facts", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.InsertConditionAnchors(context.Background(), nil); err != nil {
		t.Fatalf("empty InsertConditionAnchors failed: %v", err)
	}
	if called {
		t.Fatal("expected empty InsertConditionAnchors to issue 0 HTTP requests")
	}
}

func TestWriterInsertConditionAnchorsEncodesNullableJSONEachRowAndDedup(t *testing.T) {
	var queries []string
	var bodies []string
	var tokens []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, _, ok := r.BasicAuth()
		if !ok || user != "analytics_projector_writer" {
			t.Fatalf("BasicAuth user=%q ok=%v", user, ok)
		}
		q, err := url.QueryUnescape(r.URL.Query().Get("query"))
		if err != nil {
			t.Fatal(err)
		}
		queries = append(queries, q)
		tokens = append(tokens, r.URL.Query().Get("insert_deduplication_token"))
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		bodies = append(bodies, string(raw))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	writer, err := NewWriter(WriterConfig{
		BaseURL: server.URL, Database: "analytics", Table: "energy_interval_facts",
		Username: "analytics_projector_writer", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}

	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}

	wet := 23.5
	dry := 30.25
	plr := 0.72
	aiShare := 0.85
	hourUTC := time.Date(2026, 10, 12, 4, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2026, 10, 12, 5, 1, 2, 345_000_000, time.UTC)

	anchors := []energy.SiteConditionAnchor{
		{
			TenantID:    "018f4d00-0000-7000-8000-000000000001",
			SiteID:      "018f4e00-1000-7000-8000-000000000002",
			Hour:        hourUTC.Add(time.Hour),
			SampleCount: 1,
			CalcVersion: 2,
			UpdatedAt:   updatedAt,
		},
		{
			TenantID:          "018f4d00-0000-7000-8000-000000000001",
			SiteID:            "018f4e00-1000-7000-8000-000000000001",
			Hour:              hourUTC.In(shanghai).Add(15 * time.Minute),
			OutdoorWetBulbC:   &wet,
			OutdoorDryBulbC:   &dry,
			PlantLoadRatio:    &plr,
			AIClosedLoopShare: &aiShare,
			SampleCount:       6,
			CalcVersion:       4,
			UpdatedAt:         updatedAt.In(shanghai),
		},
	}

	if err := writer.InsertConditionAnchors(context.Background(), anchors); err != nil {
		t.Fatalf("InsertConditionAnchors failed: %v", err)
	}
	if err := writer.InsertConditionAnchors(context.Background(), anchors); err != nil {
		t.Fatalf("retry InsertConditionAnchors failed: %v", err)
	}

	if len(queries) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(queries))
	}
	wantQuery := "INSERT INTO analytics.site_condition_anchors_hourly FORMAT JSONEachRow"
	if queries[0] != wantQuery {
		t.Fatalf("query = %q, want %q", queries[0], wantQuery)
	}
	if strings.Contains(strings.ToUpper(queries[0]), "ALTER TABLE") {
		t.Fatalf("unexpected mutation in query: %q", queries[0])
	}
	if len(tokens[0]) != 64 || tokens[0] != tokens[1] {
		t.Fatalf("expected stable 64-hex dedup token across retries, got %q vs %q", tokens[0], tokens[1])
	}

	lines := strings.Split(strings.TrimSpace(bodies[0]), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 JSONEachRow lines, got %d: %q", len(lines), bodies[0])
	}

	var first map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("decode first row: %v", err)
	}
	if first["site_id"] != "018f4e00-1000-7000-8000-000000000001" ||
		first["hour"] != "2026-10-12 04:00:00" ||
		first["updated_at"] != "2026-10-12 05:01:02.345" ||
		first["outdoor_wet_bulb_c"] != 23.5 ||
		first["outdoor_dry_bulb_c"] != 30.25 ||
		first["plant_load_ratio"] != 0.72 ||
		first["ai_closed_loop_share"] != 0.85 ||
		first["sample_count"] != float64(6) ||
		first["calc_version"] != float64(4) {
		t.Fatalf("unexpected first JSON row: %+v", first)
	}

	var second map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatalf("decode second row: %v", err)
	}
	for _, nullableKey := range []string{"outdoor_wet_bulb_c", "outdoor_dry_bulb_c", "plant_load_ratio", "ai_closed_loop_share"} {
		val, exists := second[nullableKey]
		if !exists || val != nil {
			t.Fatalf("expected explicit JSON null for %s, exists=%v val=%v (raw=%s)", nullableKey, exists, val, lines[1])
		}
	}
}

func TestWriterInsertConditionAnchorsPropagatesHTTPAndValidationErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "clickhouse insert denied", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	writer, err := NewWriter(WriterConfig{
		BaseURL: server.URL, Database: "analytics", Table: "energy_interval_facts", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}

	valid := energy.SiteConditionAnchor{
		TenantID:    "018f4d00-0000-7000-8000-000000000001",
		SiteID:      "018f4e00-1000-7000-8000-000000000001",
		Hour:        time.Date(2026, 10, 12, 4, 0, 0, 0, time.UTC),
		SampleCount: 1,
		CalcVersion: 1,
	}
	err = writer.InsertConditionAnchors(context.Background(), []energy.SiteConditionAnchor{valid})
	if err == nil || !strings.Contains(err.Error(), "503") || !strings.Contains(err.Error(), "clickhouse insert denied") {
		t.Fatalf("expected 503 error propagation, got %v", err)
	}

	var closed *Writer
	if err := closed.InsertConditionAnchors(context.Background(), nil); err == nil {
		t.Fatal("expected error on nil writer")
	}

	nan := math.NaN()
	for _, invalid := range []energy.SiteConditionAnchor{
		{SiteID: valid.SiteID, Hour: valid.Hour},
		{TenantID: valid.TenantID, Hour: valid.Hour},
		{TenantID: valid.TenantID, SiteID: valid.SiteID},
		{TenantID: valid.TenantID, SiteID: valid.SiteID, Hour: valid.Hour, OutdoorDryBulbC: &nan},
	} {
		if err := writer.InsertConditionAnchors(context.Background(), []energy.SiteConditionAnchor{invalid}); err == nil {
			t.Fatalf("expected validation error for %+v", invalid)
		}
	}
}

func TestReaderReadConditionInputsCombinesObservationsAndCoolingWithoutShadowing(t *testing.T) {
	var seenSQL string
	var seenParams url.Values

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		seenSQL = string(raw)
		seenParams = r.URL.Query()
		_, _ = io.WriteString(w,
			`{"tenant_id":"018f4d00-0000-7000-8000-000000000001","site_id":"018f4e00-1000-7000-8000-000000000001","hour_utc":"2026-10-12T04:00:00Z","outdoor_wet_bulb_c":23.5,"outdoor_dry_bulb_c":30.0,"cooling_kwh":640.5,"closed_loop_ticks":9,"total_control_ticks":10}`+"\n"+
				`{"tenant_id":"018f4d00-0000-7000-8000-000000000001","site_id":"018f4e00-1000-7000-8000-000000000002","hour_utc":"2026-10-12T05:00:00Z","outdoor_wet_bulb_c":null,"outdoor_dry_bulb_c":null,"cooling_kwh":320.0,"closed_loop_ticks":"0","total_control_ticks":"0"}`+"\n")
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

	from := time.Date(2026, 10, 12, 4, 15, 0, 0, time.UTC)
	to := time.Date(2026, 10, 12, 6, 0, 0, 0, time.UTC)
	inputs, err := reader.ReadConditionInputs(context.Background(), from, to)
	if err != nil {
		t.Fatalf("ReadConditionInputs failed: %v", err)
	}
	if len(inputs) != 2 {
		t.Fatalf("expected 2 condition inputs, got %d", len(inputs))
	}
	if inputs[0].SiteID != "018f4e00-1000-7000-8000-000000000001" ||
		inputs[0].OutdoorWetBulbC == nil || *inputs[0].OutdoorWetBulbC != 23.5 ||
		inputs[0].OutdoorDryBulbC == nil || *inputs[0].OutdoorDryBulbC != 30.0 ||
		inputs[0].CoolingKWh == nil || *inputs[0].CoolingKWh != 640.5 ||
		inputs[0].ClosedLoopTicks != 9 || inputs[0].TotalControlTicks != 10 {
		t.Fatalf("unexpected first input: %+v", inputs[0])
	}
	if inputs[1].SiteID != "018f4e00-1000-7000-8000-000000000002" ||
		inputs[1].OutdoorWetBulbC != nil || inputs[1].OutdoorDryBulbC != nil ||
		inputs[1].CoolingKWh == nil || *inputs[1].CoolingKWh != 320.0 ||
		inputs[1].ClosedLoopTicks != 0 || inputs[1].TotalControlTicks != 0 {
		t.Fatalf("unexpected second input: %+v", inputs[1])
	}

	if seenParams.Get("param_from") != "2026-10-12 04:15:00.000" ||
		seenParams.Get("param_cooling_from") != "2026-10-12 04:00:00.000" ||
		seenParams.Get("param_to") != "2026-10-12 06:00:00.000" {
		t.Fatalf("unexpected query params: %v", seenParams)
	}
	for _, needle := range []string{
		"FROM analytics.operating_condition_observations AS obs",
		"obs.hour_utc AS obs_hour_key",
		"obs.value_text AS obs_text_val",
		"FROM analytics.energy_hourly AS e",
		"FULL OUTER JOIN cooling_hourly AS c",
		"'weather.ambient_wet_bulb_temperature'",
		"'weather.ambient_dry_bulb_temperature'",
		"'control_mode'",
	} {
		if !strings.Contains(seenSQL, needle) {
			t.Fatalf("ReadConditionInputs SQL missing %q:\n%s", needle, seenSQL)
		}
	}
	if strings.Contains(seenSQL, "telemetry_history.observations") || strings.Contains(seenSQL, "value_string") || strings.Contains(seenSQL, "value_boolean") {
		t.Fatalf("ReadConditionInputs must query analytics.operating_condition_observations view columns only:\n%s", seenSQL)
	}
	if strings.Contains(seenSQL, "AS hour_utc\n") || strings.Contains(seenSQL, "AS value_text") {
		t.Fatalf("ClickHouse alias shadowing detected in ReadConditionInputs SQL:\n%s", seenSQL)
	}

	if _, err := reader.ReadConditionInputs(context.Background(), to, from); err == nil {
		t.Fatal("expected error for unordered window")
	}
	var closed *Reader
	if _, err := closed.ReadConditionInputs(context.Background(), from, to); err == nil {
		t.Fatal("expected error on nil reader")
	}
}
