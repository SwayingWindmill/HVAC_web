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
