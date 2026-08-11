package clickhouse

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/services/analytics-read-model-projector/internal/energy"
)

func TestCumulativeMeterProjectsAdditiveEnergyFactsIdempotently(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("ANALYTICS_CLICKHOUSE_TEST_URL"))
	if baseURL == "" {
		t.Skip("ANALYTICS_CLICKHOUSE_TEST_URL is not configured")
	}
	adminUsername := envOr("ANALYTICS_CLICKHOUSE_TEST_ADMIN_USERNAME", "telemetry_history")
	adminPassword := os.Getenv("ANALYTICS_CLICKHOUSE_TEST_ADMIN_PASSWORD")
	client := &http.Client{Timeout: 15 * time.Second}
	organizationID := "018f4f00-0000-7000-8000-000000000001"
	siteID := "018f4f00-1000-7000-8000-000000000001"
	deviceID := "018f4f00-2000-7000-8000-000000000001"
	partition := "analytics-integration-" + strings.ReplaceAll(t.Name(), "/", "-")

	observations := []map[string]any{
		historyObservation("018f4f00-3000-7000-8000-000000000001", "018f4f00-4000-7000-8000-000000000001", organizationID, siteID, deviceID, partition, 1722257700000, 100.0, "2026-07-29T12:55:00.000Z"),
		historyObservation("018f4f00-3000-7000-8000-000000000002", "018f4f00-4000-7000-8000-000000000002", organizationID, siteID, deviceID, partition, 1722258000000, 103.0, "2026-07-29T13:00:00.000Z"),
		historyObservation("018f4f00-3000-7000-8000-000000000003", "018f4f00-4000-7000-8000-000000000003", organizationID, siteID, deviceID, partition, 1722258300000, 1.0, "2026-07-29T13:05:00.000Z"),
	}
	insertJSONEachRow(t, client, baseURL, adminUsername, adminPassword, "telemetry_history.observations", observations)

	reader, err := NewReader(ReaderConfig{
		BaseURL: baseURL, SourceDatabase: "telemetry_history", SourceTable: "observations",
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
	projector, err := energy.NewProjector(energy.ProjectorConfig{Source: reader, Sink: writer, BatchSize: 32, Now: func() time.Time {
		return time.Date(2026, 7, 29, 13, 5, 2, 0, time.UTC)
	}})
	if err != nil {
		t.Fatal(err)
	}
	projected, err := projector.ProjectOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if projected != 2 {
		t.Fatalf("projected=%d", projected)
	}
	projected, err = projector.ProjectOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if projected != 0 {
		t.Fatalf("second projected=%d", projected)
	}

	query := fmt.Sprintf(`SELECT energy_kwh, quality, quality_reasons, dataset_revision
FROM analytics.energy_interval_facts
WHERE organization_id = toUUID('%s')
  AND site_id = toUUID('%s')
  AND device_id = toUUID('%s')
ORDER BY period_end
FORMAT JSONEachRow`, organizationID, siteID, deviceID)
	payload := executeClickHouse(t, client, baseURL, adminUsername, adminPassword, query)
	decoder := json.NewDecoder(bytes.NewReader(payload))
	var rows []struct {
		EnergyKWh       float64  `json:"energy_kwh"`
		Quality         string   `json:"quality"`
		QualityReasons  []string `json:"quality_reasons"`
		DatasetRevision uint64   `json:"dataset_revision"`
	}
	for {
		var row struct {
			EnergyKWh       float64  `json:"energy_kwh"`
			Quality         string   `json:"quality"`
			QualityReasons  []string `json:"quality_reasons"`
			DatasetRevision uint64   `json:"dataset_revision"`
		}
		if err := decoder.Decode(&row); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
	}
	if len(rows) != 2 || rows[0].EnergyKWh != 3 || rows[0].Quality != energy.QualityValid || rows[0].DatasetRevision != 1722258000000 {
		t.Fatalf("rows=%#v", rows)
	}
	if rows[1].EnergyKWh != 0 || rows[1].Quality != energy.QualitySuspect || len(rows[1].QualityReasons) != 1 || rows[1].QualityReasons[0] != energy.ReasonMeterResetOrRollback {
		t.Fatalf("rows=%#v", rows)
	}
}

func historyObservation(observationID, eventID, organizationID, siteID, deviceID, partition string, offset uint64, value float64, sampledAt string) map[string]any {
	return map[string]any{
		"observation_id":          observationID,
		"tenant_id":               "018f4f00-0100-7000-8000-000000000001",
		"owning_organization_id":  organizationID,
		"site_id":                 siteID,
		"device_id":               deviceID,
		"point_id":                "018f4f00-2100-7000-8000-000000000001",
		"sensor_id":               "018f4f00-2200-7000-8000-000000000001",
		"integration_instance_id": "018f4f00-5000-7000-8000-000000000001",
		"source_event_id":         eventID,
		"source_partition":        partition,
		"source_offset":           offset,
		"source_path":             "thingsboard/hvac-meter",
		"telemetry_key":           energy.CumulativeElectricityTelemetryKey,
		"value_type":              "NUMBER",
		"unit":                    "kWh",
		"value_json":              fmt.Sprintf("%g", value),
		"value_number":            value,
		"sampled_at":              sampledAt,
		"received_at":             sampledAt,
		"acceptance_status":       "ACCEPTED",
		"quality":                 energy.SourceQualityGood,
		"quality_reasons":         []string{},
		"payload_sha256":          strings.Repeat("a", 64),
	}
}

func insertJSONEachRow(t *testing.T, client *http.Client, baseURL, username, password, table string, rows []map[string]any) {
	t.Helper()
	var body bytes.Buffer
	encoder := json.NewEncoder(&body)
	for _, row := range rows {
		if err := encoder.Encode(row); err != nil {
			t.Fatal(err)
		}
	}
	endpoint, err := url.Parse(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := endpoint.Query()
	query.Set("query", "INSERT INTO "+table+" FORMAT JSONEachRow")
	query.Set("date_time_input_format", "best_effort")
	query.Set("wait_end_of_query", "1")
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, endpoint.String(), &body)
	if err != nil {
		t.Fatal(err)
	}
	request.SetBasicAuth(username, password)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		t.Fatalf("insert status=%d body=%s", response.StatusCode, payload)
	}
}

func executeClickHouse(t *testing.T, client *http.Client, baseURL, username, password, query string) []byte {
	t.Helper()
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, baseURL, strings.NewReader(query))
	if err != nil {
		t.Fatal(err)
	}
	request.SetBasicAuth(username, password)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		t.Fatalf("query status=%d body=%s", response.StatusCode, payload)
	}
	return payload
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
