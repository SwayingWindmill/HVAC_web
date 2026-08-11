package history

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/telemetryhistorymodel"
)

const (
	historyOrganizationID       = "018f1e00-0000-7000-8000-000000000001"
	historySiteID               = "018f1e00-1000-7000-8000-000000000001"
	historyDeviceID             = "018f1e00-4000-7000-8000-000000000001"
	historyObservationID        = "018f1e00-8000-7000-8000-000000000001"
	historyPointID              = "018f1e00-5000-7000-8000-000000000001"
	historySensorID             = "018f1e00-6000-7000-8000-000000000001"
	historyReplacementPointID   = "018f1e00-5000-7000-8000-000000000002"
	historyReplacementSensorID  = "018f1e00-6000-7000-8000-000000000002"
)

func TestClickHouseHistoryClientUsesFixedScopedQueriesAndBuildsMetadata(t *testing.T) {
	var mu sync.Mutex
	queries := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		payload, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		query := string(payload)
		mu.Lock()
		queries = append(queries, query)
		index := len(queries)
		mu.Unlock()
		writer.Header().Set("Content-Type", "application/x-ndjson")
		if index == 1 {
			_, _ = writer.Write([]byte(`{"observation_id":"018f1e00-8000-7000-8000-000000000001","point_id":"018f1e00-5000-7000-8000-000000000001","sensor_id":"018f1e00-6000-7000-8000-000000000001","telemetry_key":"zone.temperature","sampled_at":"2026-07-30T01:00:00.000Z","received_at":"2026-07-30T01:00:01.000Z","value":22.5,"unit":"Cel","quality":"GOOD","quality_reasons":[],"revision":7,"total_count":2}
{"observation_id":"018f1e00-8000-7000-8000-000000000002","point_id":"018f1e00-5000-7000-8000-000000000002","sensor_id":"018f1e00-6000-7000-8000-000000000002","telemetry_key":"zone.temperature","sampled_at":"2026-07-30T02:00:00.000Z","received_at":"2026-07-30T02:00:01.000Z","value":23.0,"unit":"Cel","quality":"SUSPECT","quality_reasons":["SENSOR_DRIFT"],"revision":8,"total_count":2}
`))
			return
		}
		_, _ = writer.Write([]byte(`{"data_watermark":"2026-07-30T05:30:00.000Z","maximum_revision":9}
`))
	}))
	defer server.Close()

	client, err := NewClient(Config{
		BaseURL: server.URL, Database: "telemetry_history", Table: "observations",
		Username: "history_reader", Password: "secret", DatasetRevision: "telemetry-history:v1", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC)
	query := telemetryhistorymodel.DeviceHistoryQuery{
		ActingOrganizationID: historyOrganizationID, OwningOrganizationID: historyOrganizationID, SiteID: historySiteID, DeviceID: historyDeviceID,
		Keys: []string{"zone.temperature"}, From: from, To: from.Add(6 * time.Hour), MaxPointsPerKey: 2,
	}
	response, err := client.QueryDeviceHistory(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Series) != 1 || len(response.Series[0].Points) != 2 || response.Metadata.ReturnedPoints != 2 || response.Metadata.DatasetRevision != "telemetry-history:v1:9" || !response.Metadata.Partial {
		t.Fatalf("response = %#v", response)
	}
	if len(response.Metadata.TruncatedKeys) != 0 {
		t.Fatalf("truncated keys = %#v", response.Metadata.TruncatedKeys)
	}
	if response.Series[0].Points[0].PointID != historyPointID || response.Series[0].Points[0].SensorID == nil || *response.Series[0].Points[0].SensorID != historySensorID {
		t.Fatalf("first historical identity = %#v", response.Series[0].Points[0])
	}
	if response.Series[0].Points[1].PointID != historyReplacementPointID || response.Series[0].Points[1].SensorID == nil || *response.Series[0].Points[1].SensorID != historyReplacementSensorID {
		t.Fatalf("replacement historical identity = %#v", response.Series[0].Points[1])
	}
	mu.Lock()
	defer mu.Unlock()
	if len(queries) != 2 {
		t.Fatalf("queries = %d", len(queries))
	}
	for _, queryText := range queries {
		for _, marker := range []string{
			"owning_organization_id = toUUID('" + historyOrganizationID + "')",
			"site_id = toUUID('" + historySiteID + "')",
			"device_id = toUUID('" + historyDeviceID + "')",
			"telemetry_key IN ('zone.temperature')",
			"acceptance_status = 'ACCEPTED'",
			"value_number IS NOT NULL",
		} {
			if !strings.Contains(queryText, marker) {
				t.Fatalf("query missing %q:\n%s", marker, queryText)
			}
		}
		if strings.Contains(queryText, "SELECT *") || strings.Contains(queryText, "system.") {
			t.Fatalf("query escaped fixed boundary:\n%s", queryText)
		}
	}
	if !strings.Contains(queries[0], "row_number() OVER (PARTITION BY telemetry_key") || !strings.Contains(queries[0], "WHERE row_number <= 2") {
		t.Fatalf("point query is not independently bounded per key:\n%s", queries[0])
	}
	for _, marker := range []string{"toString(point_id) AS point_id", "AS sensor_id"} {
		if !strings.Contains(queries[0], marker) {
			t.Fatalf("point query omitted historical identity %q:\n%s", marker, queries[0])
		}
	}
}

func TestClickHouseHistoryClientMarksTruncationAndRejectsUnrequestedKeys(t *testing.T) {
	responses := []string{
		`{"observation_id":"018f1e00-8000-7000-8000-000000000001","point_id":"018f1e00-5000-7000-8000-000000000001","sensor_id":null,"telemetry_key":"zone.temperature","sampled_at":"2026-07-30T01:00:00.000Z","received_at":"2026-07-30T01:00:01.000Z","value":22.5,"unit":"Cel","quality":"GOOD","quality_reasons":[],"revision":7,"total_count":3}
`,
		`{"data_watermark":"2026-07-30T06:00:00.000Z","maximum_revision":7}
`,
	}
	index := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte(responses[index]))
		index++
	}))
	defer server.Close()
	client, err := NewClient(Config{BaseURL: server.URL, Database: "telemetry_history", Table: "observations", DatasetRevision: "telemetry-history:v1", HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC)
	query := telemetryhistorymodel.DeviceHistoryQuery{ActingOrganizationID: historyOrganizationID, OwningOrganizationID: historyOrganizationID, SiteID: historySiteID, DeviceID: historyDeviceID, Keys: []string{"zone.temperature"}, From: from, To: from.Add(6 * time.Hour), MaxPointsPerKey: 1}
	response, err := client.QueryDeviceHistory(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	if !response.Metadata.Partial || len(response.Metadata.TruncatedKeys) != 1 || response.Metadata.TruncatedKeys[0] != "zone.temperature" {
		t.Fatalf("metadata = %#v", response.Metadata)
	}
}
