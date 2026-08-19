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
	historyTenantID = "018f2e00-1000-7000-8000-000000000001"
	historySiteID   = "018f2e00-2000-7000-8000-000000000001"
	historyDeviceID = "018f2e00-3000-7000-8000-000000000001"
	historyPointID  = "018f2e00-4000-7000-8000-000000000001"
)

func TestClickHouseHistoryClientPreservesTypedSameTimestampFactsAndStableCursorSnapshot(t *testing.T) {
	var mu sync.Mutex
	queries := make([]string, 0, 3)
	responses := []string{
		`{"snapshot_at":"2026-08-19T01:10:00.000Z","projection_watermark":"2026-08-19T01:09:00.000Z"}
`,
		pointRowJSON("018f2e00-5000-7000-8000-000000000001", "ACCEPTED", "STRING", `\"COOL\"`, "2026-08-19T01:00:00.000Z") +
			pointRowJSON("018f2e00-5000-7000-8000-000000000002", "OUT_OF_ORDER", "BOOLEAN", `true`, "2026-08-19T01:00:00.000Z") +
			pointRowJSON("018f2e00-5000-7000-8000-000000000003", "ACCEPTED", "JSON", `{\"mode\":\"AUTO\"}`, "2026-08-19T01:01:00.000Z"),
		pointRowJSON("018f2e00-5000-7000-8000-000000000003", "ACCEPTED", "JSON", `{\"mode\":\"AUTO\"}`, "2026-08-19T01:01:00.000Z"),
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		payload, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		queries = append(queries, string(payload))
		index := len(queries) - 1
		mu.Unlock()
		if index >= len(responses) {
			t.Fatalf("unexpected ClickHouse request %d", index)
		}
		writer.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = writer.Write([]byte(responses[index]))
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL, Database: "telemetry_history", Table: "observations", HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 8, 19, 1, 0, 0, 0, time.UTC)
	query := telemetryhistorymodel.DeviceHistoryQuery{
		TenantID: historyTenantID, SiteID: historySiteID, DeviceID: historyDeviceID,
		Keys: []string{"zone.mode"}, From: from, To: from.Add(time.Hour), PageSize: 2,
	}
	first, err := client.QueryDeviceHistory(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Observations) != 2 || first.Observations[0].ObservationID == first.Observations[1].ObservationID || first.Observations[1].Acceptance != telemetryhistorymodel.AcceptanceOutOfOrder || first.Metadata.NextCursor == nil {
		t.Fatalf("first=%#v", first)
	}
	if first.Observations[0].ValueType != telemetryhistorymodel.ValueTypeString || first.Observations[1].ValueType != telemetryhistorymodel.ValueTypeBoolean {
		t.Fatalf("typed observations=%#v", first.Observations)
	}
	query.Cursor = first.Metadata.NextCursor
	second, err := client.QueryDeviceHistory(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Observations) != 1 || second.Observations[0].ObservationID != "018f2e00-5000-7000-8000-000000000003" || second.Metadata.NextCursor != nil {
		t.Fatalf("second=%#v", second)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(queries) != 3 {
		t.Fatalf("queries=%d", len(queries))
	}
	if !strings.Contains(queries[0], "max(projected_at)") || strings.Contains(queries[0], "max(sampled_at)") {
		t.Fatalf("snapshot query does not use projector watermark:\n%s", queries[0])
	}
	for _, marker := range []string{
		"acceptance_status IN ('ACCEPTED', 'OUT_OF_ORDER')",
		"value_type IN ('NUMBER', 'STRING', 'BOOLEAN', 'JSON')",
		"ORDER BY telemetry_key, sampled_at, toString(observation_id)",
		"LIMIT 3",
		"projected_at < parseDateTime64BestEffort('2026-08-19T01:10:00.000Z'",
	} {
		if !strings.Contains(queries[1], marker) {
			t.Fatalf("first page query missing %q:\n%s", marker, queries[1])
		}
	}
	if !strings.Contains(queries[2], "toString(observation_id) > '018f2e00-5000-7000-8000-000000000002'") || strings.Contains(queries[2], "now64(3)") {
		t.Fatalf("cursor page did not reuse fixed snapshot/keyset:\n%s", queries[2])
	}
}

func TestHistoryCursorRejectsScopeDrift(t *testing.T) {
	from := time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)
	query := telemetryhistorymodel.DeviceHistoryQuery{TenantID: historyTenantID, SiteID: historySiteID, DeviceID: historyDeviceID, Keys: []string{"zone.mode"}, From: from, To: from.Add(time.Hour), PageSize: 10}
	last := telemetryhistorymodel.DeviceHistoryObservation{ObservationID: "018f2e00-5000-7000-8000-000000000001", TelemetryKey: "zone.mode", SampledAt: from.Add(time.Minute)}
	cursor, err := encodeCursor(query, from.Add(time.Hour), nil, last)
	if err != nil {
		t.Fatal(err)
	}
	drifted := query
	drifted.Keys = []string{"zone.temperature"}
	if _, err := decodeCursor(cursor, drifted); err == nil {
		t.Fatal("cursor scope drift was accepted")
	}
}

func pointRowJSON(observationID, acceptance, valueType, valueJSON, sampledAt string) string {
	return `{"observation_id":"` + observationID + `","point_id":"` + historyPointID + `","sensor_id":null,"telemetry_key":"zone.mode","point_type":"STATE","point_revision":7,"sampled_at":"` + sampledAt + `","received_at":"2026-08-19T01:05:00.000Z","acceptance_status":"` + acceptance + `","value_type":"` + valueType + `","value_json":"` + valueJSON + `","unit":null,"quality":"GOOD","quality_reasons":[],"source_event_id":"` + observationID + `","source_partition":"mqtt:gateway:device:zone.mode","source_offset":42}
`
}
