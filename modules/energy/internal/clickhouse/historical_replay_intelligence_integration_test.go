package clickhouse

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/modules/energy/internal/energy"
)

func TestHistoricalReplayCounterHistoryProjectsEnergyFacts(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("ANALYTICS_CLICKHOUSE_TEST_URL"))
	if baseURL == "" {
		t.Skip("ANALYTICS_CLICKHOUSE_TEST_URL is not configured")
	}
	client := &http.Client{Timeout: 15 * time.Second}
	reader, err := NewReader(ReaderConfig{
		BaseURL: baseURL, SourceDatabase: "telemetry_history", SourceTable: "counter_deltas",
		AnalyticsDatabase: "analytics", AnalyticsTable: "energy_interval_facts",
		Username: "analytics_projector_reader", HTTPClient: client,
	})
	if err != nil {
		t.Fatal(err)
	}
	writer, err := NewWriter(WriterConfig{
		BaseURL: baseURL, Database: "analytics", Table: "energy_interval_facts",
		Username: "analytics_projector_writer", HTTPClient: client,
	})
	if err != nil {
		t.Fatal(err)
	}
	resolver := integrationBindingResolver{
		tenantID: "018f1d00-0000-7000-8000-000000000001",
		siteID:   "018f1e00-1000-7000-8000-000000000001",
		deviceID: "018f1e00-4000-7000-8000-000000000001",
		pointID:  "01990000-3481-7000-8000-000000000002",
	}
	projector, err := energy.NewProjector(energy.ProjectorConfig{
		CounterSource: reader, BindingResolver: resolver, FactSink: writer, BatchSize: 32,
		Now: func() time.Time { return time.Date(2026, 8, 28, 17, 35, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	projected, err := projector.ProjectOnce(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if projected != 4 {
		t.Fatalf("replay energy projected=%d want=4", projected)
	}
	if projectedAgain, projectErr := projector.ProjectOnce(t.Context()); projectErr != nil || projectedAgain != 0 {
		t.Fatalf("replay energy second projection=%d err=%v", projectedAgain, projectErr)
	}

	payload := executeClickHouse(t, client, baseURL, envOr("ANALYTICS_CLICKHOUSE_TEST_ADMIN_USERNAME", "telemetry_history"), os.Getenv("ANALYTICS_CLICKHOUSE_TEST_ADMIN_PASSWORD"), `
SELECT energy_kwh,quality
FROM analytics.energy_interval_facts
WHERE tenant_id=toUUID('018f1d00-0000-7000-8000-000000000001')
  AND site_id=toUUID('018f1e00-1000-7000-8000-000000000001')
  AND device_id=toUUID('018f1e00-4000-7000-8000-000000000001')
  AND point_id=toUUID('01990000-3481-7000-8000-000000000002')
ORDER BY period_end
FORMAT JSONEachRow`)
	decoder := json.NewDecoder(bytes.NewReader(payload))
	var rows []struct {
		EnergyKWh float64 `json:"energy_kwh"`
		Quality   string  `json:"quality"`
	}
	for {
		var row struct {
			EnergyKWh float64 `json:"energy_kwh"`
			Quality   string  `json:"quality"`
		}
		if err := decoder.Decode(&row); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
	}
	want := []float64{10, 15, 15, 20}
	if len(rows) != len(want) {
		t.Fatalf("replay energy facts=%#v", rows)
	}
	for index := range want {
		if rows[index].EnergyKWh != want[index] || rows[index].Quality != energy.FactQualityValid {
			t.Fatalf("replay energy fact[%d]=%#v", index, rows[index])
		}
	}

	history := strings.TrimSpace(string(executeClickHouse(t, client, baseURL, envOr("ANALYTICS_CLICKHOUSE_TEST_ADMIN_USERNAME", "telemetry_history"), os.Getenv("ANALYTICS_CLICKHOUSE_TEST_ADMIN_PASSWORD"), `
SELECT toString(count()) || '|' || toString(countIf(source_path='HISTORY_REPLAY'))
FROM telemetry_history.observations
WHERE device_id=toUUID('018f1e00-4000-7000-8000-000000000001')
  AND point_id=toUUID('01990000-3481-7000-8000-000000000002')
FORMAT TSVRaw`)))
	if history != "5|5" {
		t.Fatalf("replay counter history=%q", history)
	}
}
