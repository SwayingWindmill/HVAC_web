package history

import (
	"context"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/analyticsmodel"
)

func assertClosePtr(t *testing.T, name string, got *float64, want float64) {
	t.Helper()
	if got == nil || math.Abs(*got-want) > 1e-6 {
		t.Fatalf("%s = %v, want %v", name, got, want)
	}
}

func TestClickHouseReportEnrichmentComputesBalanceEfficiencyAndAnchors(t *testing.T) {
	type capturedRequest struct {
		sql    string
		params url.Values
	}
	var mu sync.Mutex
	captured := make([]capturedRequest, 0, 3)

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		sql := string(body)
		mu.Lock()
		captured = append(captured, capturedRequest{sql: sql, params: request.URL.Query()})
		mu.Unlock()

		writer.Header().Set("Content-Type", "application/x-ndjson")
		switch {
		case strings.Contains(sql, "analytics.site_meter_balance_hourly"):
			_, _ = writer.Write([]byte(`{"hour_count":"720","total_site_kwh":1000,"total_submeter_kwh":960,"synthesized_point_count":"0"}` + "\n"))
		case strings.Contains(sql, "analytics.energy_hourly_effective"):
			_, _ = writer.Write([]byte(strings.Join([]string{
				`{"energy_type":"cooling","accounting_scope":"SITE","telemetry_key":"","in_synthesized_total":0,"total_kwh":5000}`,
				`{"energy_type":"electricity","accounting_scope":"SITE","telemetry_key":"","in_synthesized_total":0,"total_kwh":1000}`,
				`{"energy_type":"electricity","accounting_scope":"BRANCH","telemetry_key":"chiller.energy","in_synthesized_total":0,"total_kwh":700}`,
				`{"energy_type":"electricity","accounting_scope":"BRANCH","telemetry_key":"chilled_pump.energy","in_synthesized_total":0,"total_kwh":125}`,
				`{"energy_type":"electricity","accounting_scope":"BRANCH","telemetry_key":"cwp.energy","in_synthesized_total":0,"total_kwh":100}`,
				`{"energy_type":"electricity","accounting_scope":"BRANCH","telemetry_key":"cooling_tower.energy","in_synthesized_total":0,"total_kwh":75}`,
			}, "\n") + "\n"))
		case strings.Contains(sql, "analytics.site_condition_anchors_hourly_latest"):
			_, _ = writer.Write([]byte(`{"anchor_count":"720","mean_wet_bulb_c":21.5,"mean_dry_bulb_c":28.2,"mean_load_ratio":0.68,"mean_ai_closed_loop_share":0.82}` + "\n"))
		default:
			t.Fatalf("unexpected ClickHouse SQL:\n%s", sql)
		}
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL, Database: "telemetry_history", Table: "observations", HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	query := analyticsmodel.EnergySeriesQuery{
		TenantID:      historyTenantID,
		SiteID:        historySiteID,
		EnergyType:    analyticsmodel.EnergyTypeElectricity,
		Granularity:   analyticsmodel.GranularityHour,
		Timezone:      "UTC",
		From:          from,
		To:            from.Add(30 * 24 * time.Hour),
		QualityPolicy: analyticsmodel.QualityPolicyValidOnly,
	}

	got, err := client.ReportEnrichment(context.Background(), query)
	if err != nil {
		t.Fatalf("ReportEnrichment failed: %v", err)
	}
	if len(captured) != 3 {
		t.Fatalf("expected 3 ClickHouse queries, got %d", len(captured))
	}
	for i, req := range captured {
		if req.params.Get("param_tenant_id") != historyTenantID || req.params.Get("param_site_id") != historySiteID {
			t.Fatalf("query %d missing UUID params: %v", i, req.params)
		}
		if req.params.Get("param_from_ts") != "2026-09-01T00:00:00.000Z" || req.params.Get("param_to_ts") != "2026-10-01T00:00:00.000Z" {
			t.Fatalf("query %d unexpected timestamp params: %v", i, req.params)
		}
		if !strings.Contains(req.sql, "'UTC'") {
			t.Fatalf("query %d missing explicit UTC timezone:\n%s", i, req.sql)
		}
		if strings.Contains(strings.ToLower(req.sql), "core_registry") {
			t.Fatalf("query %d violated cross-schema boundary:\n%s", i, req.sql)
		}
	}

	if got.MeterBalance == nil || got.MeterBalance.Status != analyticsmodel.MeterBalanceBalanced {
		t.Fatalf("unexpected meter balance: %#v", got.MeterBalance)
	}
	assertClosePtr(t, "ImbalanceRatio", got.MeterBalance.ImbalanceRatio, 0.04)

	if got.LayeredEfficiency == nil || got.LayeredEfficiency.SynthesizedTotal {
		t.Fatalf("unexpected layered efficiency: %#v", got.LayeredEfficiency)
	}
	assertClosePtr(t, "PlantCOP", got.LayeredEfficiency.PlantCOP, 5.0)
	assertClosePtr(t, "ChillerCOP", got.LayeredEfficiency.ChillerCOP, 5000.0/700.0)
	assertClosePtr(t, "ChilledPumpWTF", got.LayeredEfficiency.ChilledPumpWTF, 40.0)
	assertClosePtr(t, "CoolingPumpWTF", got.LayeredEfficiency.CoolingPumpWTF, 50.0)
	assertClosePtr(t, "TowerPowerShare", got.LayeredEfficiency.TowerPowerShare, 0.075)

	if got.ConditionAnchors == nil {
		t.Fatal("expected ConditionAnchors to be populated")
	}
	assertClosePtr(t, "MeanWetBulbC", got.ConditionAnchors.MeanWetBulbC, 21.5)
	assertClosePtr(t, "MeanDryBulbC", got.ConditionAnchors.MeanDryBulbC, 28.2)
	assertClosePtr(t, "MeanLoadRatio", got.ConditionAnchors.MeanLoadRatio, 0.68)
	assertClosePtr(t, "AIClosedLoopShare", got.ConditionAnchors.AIClosedLoopShare, 0.82)
}

func TestClickHouseReportEnrichmentSynthesizedEmptyAndErrors(t *testing.T) {
	var failClickHouse bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if failClickHouse {
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		body, _ := io.ReadAll(request.Body)
		sql := string(body)
		switch {
		case strings.Contains(sql, "analytics.site_meter_balance_hourly"):
			_, _ = writer.Write([]byte(`{"hour_count":"0","total_site_kwh":0,"total_submeter_kwh":0,"synthesized_point_count":"2"}` + "\n"))
		case strings.Contains(sql, "analytics.energy_hourly_effective"):
			_, _ = writer.Write([]byte(strings.Join([]string{
				`{"energy_type":"cooling","accounting_scope":"SITE","telemetry_key":"","in_synthesized_total":0,"total_kwh":1200}`,
				`{"energy_type":"electricity","accounting_scope":"BRANCH","telemetry_key":"chiller.energy","in_synthesized_total":1,"total_kwh":200}`,
				`{"energy_type":"electricity","accounting_scope":"BRANCH","telemetry_key":"tower.energy","in_synthesized_total":1,"total_kwh":40}`,
			}, "\n") + "\n"))
		case strings.Contains(sql, "analytics.site_condition_anchors_hourly_latest"):
			_, _ = writer.Write([]byte(`{"anchor_count":"0","mean_wet_bulb_c":null,"mean_dry_bulb_c":null,"mean_load_ratio":null,"mean_ai_closed_loop_share":null}` + "\n"))
		}
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL, Database: "telemetry_history", Table: "observations", HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	query := analyticsmodel.EnergySeriesQuery{
		TenantID:      historyTenantID,
		SiteID:        historySiteID,
		EnergyType:    analyticsmodel.EnergyTypeElectricity,
		Granularity:   analyticsmodel.GranularityHour,
		Timezone:      "UTC",
		From:          from,
		To:            from.Add(24 * time.Hour),
		QualityPolicy: analyticsmodel.QualityPolicyValidOnly,
	}

	got, err := client.ReportEnrichment(context.Background(), query)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.MeterBalance != nil {
		t.Fatalf("expected nil MeterBalance when hour_count=0, got %#v", got.MeterBalance)
	}
	if got.ConditionAnchors != nil {
		t.Fatalf("expected nil ConditionAnchors when anchor_count=0, got %#v", got.ConditionAnchors)
	}
	if got.LayeredEfficiency == nil || !got.LayeredEfficiency.SynthesizedTotal {
		t.Fatalf("expected SynthesizedTotal=true, got %#v", got.LayeredEfficiency)
	}
	assertClosePtr(t, "PlantCOP", got.LayeredEfficiency.PlantCOP, 5.0)
	assertClosePtr(t, "ChillerCOP", got.LayeredEfficiency.ChillerCOP, 6.0)
	assertClosePtr(t, "TowerPowerShare", got.LayeredEfficiency.TowerPowerShare, 40.0/240.0)

	failClickHouse = true
	if _, err := client.ReportEnrichment(context.Background(), query); err == nil {
		t.Fatal("expected error when ClickHouse returns 500")
	}

	invalid := query
	invalid.Timezone = "Invalid/Timezone"
	if _, err := client.ReportEnrichment(context.Background(), invalid); err == nil {
		t.Fatal("expected validation error for invalid query")
	}
}
