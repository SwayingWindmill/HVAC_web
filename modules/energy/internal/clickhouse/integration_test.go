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

	"github.com/quanlaihe/hvac-web/modules/energy/internal/energy"
)

func TestCanonicalCounterDeltaProjectsEnergyFactsIdempotently(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("ANALYTICS_CLICKHOUSE_TEST_URL"))
	if baseURL == "" {
		t.Skip("ANALYTICS_CLICKHOUSE_TEST_URL is not configured")
	}
	adminUsername := envOr("ANALYTICS_CLICKHOUSE_TEST_ADMIN_USERNAME", "telemetry_history")
	adminPassword := os.Getenv("ANALYTICS_CLICKHOUSE_TEST_ADMIN_PASSWORD")
	client := &http.Client{Timeout: 15 * time.Second}
	tenantID := "018f4f00-0100-7000-8000-000000000001"
	siteID := "018f4f00-1000-7000-8000-000000000001"
	deviceID := "018f4f00-2000-7000-8000-000000000001"
	pointID := "018f4f00-2100-7000-8000-000000000001"
	partition := "analytics-integration-" + strings.ReplaceAll(t.Name(), "/", "-")

	observations := []map[string]any{
		historyObservation("018f4f00-3000-7000-8000-000000000001", "018f4f00-4000-7000-8000-000000000001", tenantID, siteID, deviceID, partition, 1722257700000, 100.0, "2026-07-29T12:55:00.000Z"),
		historyOutOfOrderObservation("018f4f00-3000-7000-8000-000000000004", "018f4f00-4000-7000-8000-000000000004", tenantID, siteID, deviceID, partition, 1722257880000, 102.0, "2026-07-29T12:58:00.000Z"),
		historyObservation("018f4f00-3000-7000-8000-000000000002", "018f4f00-4000-7000-8000-000000000002", tenantID, siteID, deviceID, partition, 1722258000000, 103.0, "2026-07-29T13:00:00.000Z"),
		historyObservation("018f4f00-3000-7000-8000-000000000003", "018f4f00-4000-7000-8000-000000000003", tenantID, siteID, deviceID, partition, 1722258300000, 1.0, "2026-07-29T13:05:00.000Z"),
	}
	insertJSONEachRow(t, client, baseURL, adminUsername, adminPassword, "telemetry_history.observations", inHistoryBatch(observations))

	reader, writer := integrationReaderWriter(t, client, baseURL)
	projector, err := energy.NewProjector(energy.ProjectorConfig{CounterSource: reader, BindingResolver: integrationBindingResolver{
		tenantID: tenantID, siteID: siteID, deviceID: deviceID, pointID: pointID,
	}, FactSink: writer, BatchSize: 32, Now: func() time.Time {
		return time.Date(2026, 7, 29, 13, 5, 2, 0, time.UTC)
	}})
	if err != nil {
		t.Fatal(err)
	}
	projected, err := projector.ProjectOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if projected != 3 {
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
WHERE tenant_id = toUUID('%s')
  AND site_id = toUUID('%s')
  AND device_id = toUUID('%s')
ORDER BY period_end
FORMAT JSONEachRow`, tenantID, siteID, deviceID)
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
	if len(rows) != 3 || rows[0].EnergyKWh != 2 || rows[0].Quality != energy.FactQualityValid || rows[0].DatasetRevision != 1722257880000 || len(rows[0].QualityReasons) != 1 || rows[0].QualityReasons[0] != "OUT_OF_ORDER" {
		t.Fatalf("rows=%#v", rows)
	}
	if rows[1].EnergyKWh != 1 || rows[1].Quality != energy.FactQualityValid || len(rows[1].QualityReasons) != 1 || rows[1].QualityReasons[0] != "OUT_OF_ORDER" || rows[2].EnergyKWh != 1 || rows[2].Quality != energy.FactQualityValid || len(rows[2].QualityReasons) != 0 {
		t.Fatalf("rows=%#v", rows)
	}
}

// A late Counter observation arrives in a later History batch: it gets its own fact, the
// successor's fact is corrected to the new predecessor, and replaying the batch after a lost
// checkpoint writes nothing twice.
func TestLateCounterObservationCorrectsSuccessorFactFromCheckpoint(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("ANALYTICS_CLICKHOUSE_TEST_URL"))
	if baseURL == "" {
		t.Skip("ANALYTICS_CLICKHOUSE_TEST_URL is not configured")
	}
	adminUsername := envOr("ANALYTICS_CLICKHOUSE_TEST_ADMIN_USERNAME", "telemetry_history")
	adminPassword := os.Getenv("ANALYTICS_CLICKHOUSE_TEST_ADMIN_PASSWORD")
	client := &http.Client{Timeout: 15 * time.Second}
	tenantID := "018f4f00-0100-7000-8000-000000000002"
	siteID := "018f4f00-1000-7000-8000-000000000002"
	deviceID := "018f4f00-2000-7000-8000-000000000002"
	pointID := "018f4f00-2100-7000-8000-000000000001"
	partition := "analytics-integration-" + t.Name()
	first, late, successor := "018f4f00-3000-7000-8000-000000000011", "018f4f00-3000-7000-8000-000000000012", "018f4f00-3000-7000-8000-000000000013"

	reader, writer := integrationReaderWriter(t, client, baseURL)
	projector, err := energy.NewProjector(energy.ProjectorConfig{CounterSource: reader, BindingResolver: integrationBindingResolver{
		tenantID: tenantID, siteID: siteID, deviceID: deviceID, pointID: pointID,
	}, FactSink: writer, RebuildEvents: writer, BatchSize: 32, Now: func() time.Time {
		return time.Date(2026, 7, 30, 13, 10, 0, 0, time.UTC)
	}})
	if err != nil {
		t.Fatal(err)
	}
	project := func(want int) {
		t.Helper()
		projected, err := projector.ProjectOnce(context.Background())
		if err != nil || projected != want {
			t.Fatalf("projected=%d err=%v, want %d", projected, err, want)
		}
	}

	insertJSONEachRow(t, client, baseURL, adminUsername, adminPassword, "telemetry_history.observations", inHistoryBatch([]map[string]any{
		historyObservation(first, "018f4f00-4000-7000-8000-000000000011", tenantID, siteID, deviceID, partition, 1, 100, "2026-07-30T12:55:00.000Z"),
		historyObservation(successor, "018f4f00-4000-7000-8000-000000000013", tenantID, siteID, deviceID, partition, 2, 110, "2026-07-30T13:05:00.000Z"),
	}))
	project(1)

	lateBatch := inHistoryBatch([]map[string]any{
		historyOutOfOrderObservation(late, "018f4f00-4000-7000-8000-000000000012", tenantID, siteID, deviceID, partition, 3, 104, "2026-07-30T13:00:00.000Z"),
	})
	insertJSONEachRow(t, client, baseURL, adminUsername, adminPassword, "telemetry_history.observations", lateBatch)
	project(2)
	project(0)

	facts := string(executeClickHouse(t, client, baseURL, adminUsername, adminPassword, fmt.Sprintf(`SELECT toString(source_current_observation_id), argMax(energy_kwh, fact_revision), max(fact_revision)
FROM analytics.energy_interval_facts WHERE tenant_id = toUUID('%s') AND site_id = toUUID('%s')
GROUP BY source_current_observation_id ORDER BY source_current_observation_id FORMAT TSVRaw`, tenantID, siteID)))
	if want := late + "\t4\t0\n" + successor + "\t6\t1\n"; facts != want {
		t.Fatalf("facts=%q want %q", facts, want)
	}
	lateSequence := lateBatch[0]["history_sequence"].(uint64)
	if checkpoint := projectionCheckpoint(t, client, baseURL, adminUsername, adminPassword); checkpoint != fmt.Sprintf("%d\t%s", lateSequence, late) {
		t.Fatalf("checkpoint=%q", checkpoint)
	}

	// Facts written, checkpoint lost: the batch is read again and nothing is written twice.
	executeClickHouse(t, client, baseURL, adminUsername, adminPassword, fmt.Sprintf(`INSERT INTO analytics.energy_projection_checkpoints (projection_name, history_sequence, observation_id, advanced_at)
VALUES ('energy_interval_facts', %d, toUUID('%s'), now64(3))`, lateSequence-1, successor))
	project(0)
	if checkpoint := projectionCheckpoint(t, client, baseURL, adminUsername, adminPassword); checkpoint != fmt.Sprintf("%d\t%s", lateSequence, late) {
		t.Fatalf("checkpoint after replay=%q", checkpoint)
	}
}

func integrationReaderWriter(t *testing.T, client *http.Client, baseURL string) (*Reader, *Writer) {
	t.Helper()
	reader, err := NewReader(ReaderConfig{
		BaseURL: baseURL, SourceDatabase: "telemetry_history",
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
	return reader, writer
}

// inHistoryBatch stamps rows as one Telemetry History batch, written after every earlier batch.
func inHistoryBatch(rows []map[string]any) []map[string]any {
	sequence := uint64(time.Now().UnixNano())
	for _, row := range rows {
		row["history_sequence"] = sequence
	}
	return rows
}

func projectionCheckpoint(t *testing.T, client *http.Client, baseURL, username, password string) string {
	t.Helper()
	return strings.TrimSpace(string(executeClickHouse(t, client, baseURL, username, password,
		"SELECT history_sequence, toString(observation_id) FROM analytics.energy_projection_checkpoints FINAL WHERE projection_name = 'energy_interval_facts' FORMAT TSVRaw")))
}

func historyObservation(observationID, eventID, tenantID, siteID, deviceID, partition string, offset uint64, value float64, sampledAt string) map[string]any {
	return map[string]any{
		"observation_id":        observationID,
		"tenant_id":             tenantID,
		"site_id":               siteID,
		"device_id":             deviceID,
		"point_id":              "018f4f00-2100-7000-8000-000000000001",
		"sensor_id":             "018f4f00-2200-7000-8000-000000000001",
		"source_id":             "gateway-a",
		"source_event_id":       eventID,
		"source_partition":      partition,
		"source_offset":         offset,
		"source_path":           "mqtt/hvac-meter",
		"telemetry_key":         "hvac_meter.energy",
		"point_type":            "COUNTER",
		"point_revision":        3,
		"counter_decrease_mode": "RESET_TO_ZERO",
		"value_type":            "NUMBER",
		"unit":                  "kWh",
		"value_json":            fmt.Sprintf("%g", value),
		"value_number":          value,
		"sampled_at":            sampledAt,
		"received_at":           sampledAt,
		"acceptance_status":     "ACCEPTED",
		"quality":               energy.SourceQualityGood,
		"quality_reasons":       []string{},
		"payload_sha256":        strings.Repeat("a", 64),
	}
}

func historyOutOfOrderObservation(observationID, eventID, tenantID, siteID, deviceID, partition string, offset uint64, value float64, sampledAt string) map[string]any {
	row := historyObservation(observationID, eventID, tenantID, siteID, deviceID, partition, offset, value, sampledAt)
	row["acceptance_status"] = "OUT_OF_ORDER"
	row["quality_reasons"] = []string{"OUT_OF_ORDER"}
	return row
}

type integrationBindingResolver struct {
	tenantID string
	siteID   string
	deviceID string
	pointID  string
}

func (resolver integrationBindingResolver) Resolve(_ context.Context, input energy.BindingResolveInput) (energy.BindingResolution, error) {
	return energy.BindingResolution{
		Status: energy.BindingMatch, TenantID: resolver.tenantID, SiteID: resolver.siteID,
		MeterID: "018f4f00-1100-7000-8000-000000000001", MeterBindingID: "018f4f00-1200-7000-8000-000000000001",
		TopologyVersionID: "018f4f00-1300-7000-8000-000000000001", BindingVersion: 1,
		EnergyTypeID: "018f4f00-1400-7000-8000-000000000001", EnergyType: energy.EnergyTypeElectricity,
		MeterRole: energy.MeterRolePrimary, Direction: "IMPORT", DeviceID: input.DeviceID, PointID: input.PointID,
		PointType: energy.PointTypeCounter, EffectiveFrom: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}, nil
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
