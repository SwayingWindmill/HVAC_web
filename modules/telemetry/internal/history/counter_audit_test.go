package history

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/telemetryhistorymodel"
	"github.com/quanlaihe/hvac-web/modules/telemetry/pkg/telemetry"
)

const (
	auditTestTenantID = "018f3e00-1000-7000-8000-000000000001"
	auditTestSiteID   = "018f3e00-2000-7000-8000-000000000002"
	auditTestDeviceID = "018f3e00-3000-7000-8000-000000000003"
	auditTestPointA   = "018f3e00-4000-7000-8000-000000000004"
	auditTestPointB   = "018f3e00-4000-7000-8000-000000000005"
)

func TestQueryCounterAuditClassifiesAnomaliesUsesLookbackAndConvertsUnits(t *testing.T) {
	from := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	to := from.Add(2 * time.Hour)

	kwhUnit := "kWh"
	whUnit := "Wh"
	resetMode := "RESET_TO_ZERO"
	rolloverMode := "ROLLOVER"
	rolloverModulus := 1000.0

	rows := []counterAuditRow{
		// Point A (kWh): lookback anchor at -5m -> +5m normal increase (+10 kWh) -> +10m zero drop -> +15m spike (+100000) -> +20m valid reset (0 -> 25 with RESET_TO_ZERO? Anchor stayed at 110 so 25 is RESET).
		{
			ObservationID:  "018f3e00-5000-7000-8000-000000000001",
			DeviceID:       auditTestDeviceID,
			PointID:        auditTestPointA,
			TelemetryKey:   "chiller_energy_kwh",
			PointRevision:  1,
			Unit:           &kwhUnit,
			ValueNumber:    100,
			SampledAt:      from.Add(-5 * time.Minute).Format(time.RFC3339Nano),
			Quality:        string(telemetry.QualityGood),
			QualityReasons: []string{},
		},
		{
			ObservationID:  "018f3e00-5000-7000-8000-000000000002",
			DeviceID:       auditTestDeviceID,
			PointID:        auditTestPointA,
			TelemetryKey:   "chiller_energy_kwh",
			PointRevision:  1,
			Unit:           &kwhUnit,
			ValueNumber:    110,
			SampledAt:      from.Add(5 * time.Minute).Format(time.RFC3339Nano),
			Quality:        string(telemetry.QualityGood),
			QualityReasons: []string{},
		},
		{
			ObservationID:  "018f3e00-5000-7000-8000-000000000003",
			DeviceID:       auditTestDeviceID,
			PointID:        auditTestPointA,
			TelemetryKey:   "chiller_energy_kwh",
			PointRevision:  1,
			Unit:           &kwhUnit,
			ValueNumber:    0,
			SampledAt:      from.Add(10 * time.Minute).Format(time.RFC3339Nano),
			Quality:        string(telemetry.QualityGood),
			QualityReasons: []string{},
		},
		{
			ObservationID:  "018f3e00-5000-7000-8000-000000000004",
			DeviceID:       auditTestDeviceID,
			PointID:        auditTestPointA,
			TelemetryKey:   "chiller_energy_kwh",
			PointRevision:  1,
			Unit:           &kwhUnit,
			ValueNumber:    100000,
			SampledAt:      from.Add(15 * time.Minute).Format(time.RFC3339Nano),
			Quality:        string(telemetry.QualityGood),
			QualityReasons: []string{},
		},
		{
			ObservationID:       "018f3e00-5000-7000-8000-000000000005",
			DeviceID:            auditTestDeviceID,
			PointID:             auditTestPointA,
			TelemetryKey:        "chiller_energy_kwh",
			PointRevision:       1,
			Unit:                &kwhUnit,
			CounterDecreaseMode: &resetMode,
			ValueNumber:         25,
			SampledAt:           from.Add(20 * time.Minute).Format(time.RFC3339Nano),
			Quality:             string(telemetry.QualityGood),
			QualityReasons:      []string{},
		},
		// Point B (Wh): lookback anchor 900 -> +30m rollover to 200 with modulus 1000 -> delta 300 Wh = 0.3 kWh.
		{
			ObservationID:          "018f3e00-5000-7000-8000-000000000006",
			DeviceID:               auditTestDeviceID,
			PointID:                auditTestPointB,
			TelemetryKey:           "pump_energy_wh",
			PointRevision:          1,
			Unit:                   &whUnit,
			CounterDecreaseMode:    &rolloverMode,
			CounterRolloverModulus: &rolloverModulus,
			ValueNumber:            900,
			SampledAt:              from.Add(-10 * time.Minute).Format(time.RFC3339Nano),
			Quality:                string(telemetry.QualityGood),
			QualityReasons:         []string{},
		},
		{
			ObservationID:          "018f3e00-5000-7000-8000-000000000007",
			DeviceID:               auditTestDeviceID,
			PointID:                auditTestPointB,
			TelemetryKey:           "pump_energy_wh",
			PointRevision:          1,
			Unit:                   &whUnit,
			CounterDecreaseMode:    &rolloverMode,
			CounterRolloverModulus: &rolloverModulus,
			ValueNumber:            200,
			SampledAt:              from.Add(30 * time.Minute).Format(time.RFC3339Nano),
			Quality:                string(telemetry.QualityGood),
			QualityReasons:         []string{},
		},
	}

	var capturedSQL string
	var capturedParams map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		q := r.URL.Query()
		capturedSQL = string(bodyBytes)
		capturedParams = map[string]string{
			"tenant":        q.Get("param_tenant_id"),
			"site":          q.Get("param_site_id"),
			"device":        q.Get("param_device_id"),
			"lookback_from": q.Get("param_lookback_from_ts"),
			"to":            q.Get("param_to_ts"),
		}
		w.Header().Set("Content-Type", "application/json")
		for _, row := range rows {
			line, _ := json.Marshal(row)
			_, _ = w.Write(line)
			_, _ = w.Write([]byte("\n"))
		}
	}))
	defer server.Close()

	client, err := NewClient(Config{
		BaseURL:  server.URL,
		Database: "telemetry_history",
		Table:    "observations",
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	deviceID := auditTestDeviceID
	response, err := client.QueryCounterAudit(context.Background(), telemetryhistorymodel.CounterAuditQuery{
		TenantID: auditTestTenantID,
		SiteID:   auditTestSiteID,
		From:     from,
		To:       to,
		DeviceID: &deviceID,
	})
	if err != nil {
		t.Fatalf("QueryCounterAudit failed: %v", err)
	}

	if !strings.Contains(capturedSQL, "{lookback_from_ts:String}") || !strings.Contains(capturedSQL, "{device_id:UUID}") {
		t.Fatalf("expected parameterized SQL placeholders, got:\n%s", capturedSQL)
	}
	if strings.Contains(capturedSQL, auditTestTenantID) || strings.Contains(capturedSQL, auditTestDeviceID) {
		t.Fatalf("SQL must not inline parameter values: %s", capturedSQL)
	}
	if capturedParams["lookback_from"] != formatClickHouseTime(from.Add(-24*time.Hour)) {
		t.Fatalf("expected 24h lookback timestamp, got %q", capturedParams["lookback_from"])
	}

	// In-window samples: 4 for Point A (+5m, +10m, +15m, +20m) + 1 for Point B (+30m) = 5 total.
	if response.Summary.TotalSamples != 5 {
		t.Fatalf("expected 5 total in-window samples, got %d", response.Summary.TotalSamples)
	}
	// Anomalies: ZeroDrop (+10m), Spike (+15m), Reset (+20m), Rollover (+30m) = 4.
	if response.Summary.AnomalyCount != 4 ||
		response.Summary.ZeroDropCount != 1 ||
		response.Summary.SpikeCount != 1 ||
		response.Summary.ResetCount != 1 ||
		response.Summary.RolloverCount != 1 ||
		response.Summary.InvalidDecreaseCount != 0 {
		t.Fatalf("unexpected summary counts: %+v", response.Summary)
	}

	// Default anomaliesOnly=nil keeps only the 4 anomaly items, sorted newest-first (+30m, +20m, +15m, +10m).
	if len(response.Items) != 4 {
		t.Fatalf("expected 4 anomaly items, got %d", len(response.Items))
	}
	if response.Items[0].TransitionType != string(telemetry.TransitionRollover) {
		t.Fatalf("expected newest item first (ROLLOVER at +30m), got %+v", response.Items[0])
	}
	if response.Items[0].CleanedDeltaKWh == nil || *response.Items[0].CleanedDeltaKWh != 0.3 {
		t.Fatalf("expected rollover delta converted from 300 Wh to 0.3 kWh, got %v", response.Items[0].CleanedDeltaKWh)
	}
	if response.Items[1].TransitionType != string(telemetry.TransitionReset) {
		t.Fatalf("expected second item RESET (+20m), got %+v", response.Items[1])
	}
	if response.Items[1].CleanedDeltaKWh == nil || *response.Items[1].CleanedDeltaKWh != 25 {
		t.Fatalf("expected reset delta 25 kWh, got %v", response.Items[1].CleanedDeltaKWh)
	}
	if response.Items[2].TransitionType != string(telemetry.TransitionInvalidSpike) || response.Items[2].CleanedDeltaKWh != nil {
		t.Fatalf("expected quarantined spike item with nil cleanedDeltaKWh, got %+v", response.Items[2])
	}
	if response.Items[3].TransitionType != string(telemetry.TransitionInvalidZeroDrop) || response.Items[3].RawPrevious == nil || *response.Items[3].RawPrevious != 110 {
		t.Fatalf("expected zero drop item anchored on 110, got %+v", response.Items[3])
	}

	// Verify AnomaliesOnly=false returns all 5 items, and Limit=2 truncates deterministically.
	includeAll := false
	truncatedResp, err := client.QueryCounterAudit(context.Background(), telemetryhistorymodel.CounterAuditQuery{
		TenantID:      auditTestTenantID,
		SiteID:        auditTestSiteID,
		From:          from,
		To:            to,
		AnomaliesOnly: &includeAll,
		Limit:         2,
	})
	if err != nil {
		t.Fatalf("QueryCounterAudit anomaliesOnly=false failed: %v", err)
	}
	if !truncatedResp.Truncated || len(truncatedResp.Items) != 2 || truncatedResp.Summary.TotalSamples != 5 {
		t.Fatalf("expected truncated=true with 2 items and full summary 5, got truncated=%v items=%d summary=%+v", truncatedResp.Truncated, len(truncatedResp.Items), truncatedResp.Summary)
	}
}

func TestQueryCounterAuditEmptyRowsReturnsEmptySlice(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
	}))
	defer server.Close()

	client, err := NewClient(Config{
		BaseURL:  server.URL,
		Database: "telemetry_history",
		Table:    "observations",
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	from := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	response, err := client.QueryCounterAudit(context.Background(), telemetryhistorymodel.CounterAuditQuery{
		TenantID: auditTestTenantID,
		SiteID:   auditTestSiteID,
		From:     from,
		To:       from.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("empty QueryCounterAudit failed: %v", err)
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	if !strings.Contains(string(encoded), `"items":[]`) {
		t.Fatalf("expected non-null empty items JSON array, got: %s", string(encoded))
	}
}
