package history

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	type recordedQuery struct {
		sql    string
		params url.Values
	}
	var mu sync.Mutex
	queries := make([]recordedQuery, 0, 3)
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
		queries = append(queries, recordedQuery{sql: string(payload), params: request.URL.Query()})
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
	if !strings.Contains(queries[0].sql, "max(projected_at)") || strings.Contains(queries[0].sql, "max(sampled_at)") {
		t.Fatalf("snapshot query does not use projector watermark:\n%s", queries[0].sql)
	}
	for _, marker := range []string{
		"tenant_id = toUUID({tenant_id:UUID})",
		"site_id = toUUID({site_id:UUID})",
		"device_id = toUUID({device_id:UUID})",
		"telemetry_key IN {keys:Array(String)}",
		"acceptance_status IN ('ACCEPTED', 'OUT_OF_ORDER')",
		"value_type IN ('NUMBER', 'STRING', 'BOOLEAN', 'JSON')",
		"ORDER BY telemetry_key, history_source.sampled_at, toString(observation_id)",
		"LIMIT {limit:UInt64}",
		"projected_at < parseDateTime64BestEffort({snapshot_ts:String}, 3, 'UTC')",
	} {
		if !strings.Contains(queries[1].sql, marker) {
			t.Fatalf("first page query missing %q:\n%s", marker, queries[1].sql)
		}
	}
	for _, forbidden := range []string{
		historyTenantID,
		historySiteID,
		historyDeviceID,
		"zone.mode",
		"2026-08-19T01:00:00.000Z",
		"2026-08-19T02:00:00.000Z",
		"2026-08-19T01:10:00.000Z",
	} {
		if strings.Contains(queries[0].sql, forbidden) || strings.Contains(queries[1].sql, forbidden) || strings.Contains(queries[2].sql, forbidden) {
			t.Fatalf("raw parameter %q leaked into SQL", forbidden)
		}
	}
	if queries[1].params.Get("param_tenant_id") != historyTenantID ||
		queries[1].params.Get("param_site_id") != historySiteID ||
		queries[1].params.Get("param_device_id") != historyDeviceID ||
		queries[1].params.Get("param_keys") != "['zone.mode']" ||
		queries[1].params.Get("param_from_ts") != "2026-08-19T01:00:00.000Z" ||
		queries[1].params.Get("param_to_ts") != "2026-08-19T02:00:00.000Z" ||
		queries[1].params.Get("param_snapshot_ts") != "2026-08-19T01:10:00.000Z" ||
		queries[1].params.Get("param_limit") != "3" {
		t.Fatalf("unexpected page 1 query params: %#v", queries[1].params)
	}
	if !strings.Contains(queries[2].sql, "toString(observation_id) > {cursor_observation_id:String}") ||
		strings.Contains(queries[2].sql, "018f2e00-5000-7000-8000-000000000002") ||
		strings.Contains(queries[2].sql, "now64(3)") {
		t.Fatalf("cursor page did not reuse fixed parameterized snapshot/keyset:\n%s", queries[2].sql)
	}
	if queries[2].params.Get("param_cursor_key") != "zone.mode" ||
		queries[2].params.Get("param_cursor_sampled_at") != "2026-08-19T01:00:00.000Z" ||
		queries[2].params.Get("param_cursor_observation_id") != "018f2e00-5000-7000-8000-000000000002" {
		t.Fatalf("unexpected cursor params: %#v", queries[2].params)
	}
}

func TestNewClientRejectsInvalidIdentifiersAndURLs(t *testing.T) {
	badConfigs := []Config{
		{BaseURL: "http://127.0.0.1:8123", Database: "", Table: "observations"},
		{BaseURL: "http://127.0.0.1:8123", Database: "telemetry_history; DROP TABLE observations", Table: "observations"},
		{BaseURL: "http://127.0.0.1:8123", Database: "telemetry-history", Table: "observations"},
		{BaseURL: "http://127.0.0.1:8123", Database: "1db", Table: "observations"},
		{BaseURL: "http://127.0.0.1:8123", Database: "telemetry_history", Table: "obs.bad"},
		{BaseURL: "http://127.0.0.1:8123/query", Database: "telemetry_history", Table: "observations"},
		{BaseURL: "http://user:pass@127.0.0.1:8123", Database: "telemetry_history", Table: "observations"},
	}
	for _, cfg := range badConfigs {
		if _, err := NewClient(cfg); err == nil {
			t.Fatalf("expected NewClient to reject config %+v", cfg)
		}
	}
	if _, err := NewClient(Config{BaseURL: "http://127.0.0.1:8123", Database: "_telemetry_2", Table: "observations_v1"}); err != nil {
		t.Fatalf("expected valid identifiers to succeed: %v", err)
	}
	if got := clickHouseStringArray([]string{"b'2", `a\1`}); got != `['a\\1','b\'2']` {
		t.Fatalf("clickHouseStringArray() = %q", got)
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

func TestNewClientInitializesTunedDefaultHTTPTransport(t *testing.T) {
	client, err := NewClient(Config{
		BaseURL:  "http://127.0.0.1:8123",
		Database: "telemetry_history",
		Table:    "observations",
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.httpClient == nil {
		t.Fatal("expected default HTTPClient to be initialized")
	}
	if client.httpClient.Timeout != 8*time.Second {
		t.Fatalf("Timeout=%s, want 8s", client.httpClient.Timeout)
	}
	transport, ok := client.httpClient.Transport.(*http.Transport)
	if !ok || transport == nil {
		t.Fatalf("Transport=%T, want *http.Transport", client.httpClient.Transport)
	}
	if transport.MaxConnsPerHost != 16 ||
		transport.MaxIdleConns != 64 ||
		transport.MaxIdleConnsPerHost != 16 ||
		transport.IdleConnTimeout != 90*time.Second ||
		transport.TLSHandshakeTimeout != 10*time.Second ||
		transport.ResponseHeaderTimeout != 30*time.Second ||
		transport.ExpectContinueTimeout != time.Second ||
		transport.DialContext == nil {
		t.Fatalf("unexpected transport tuning: MaxConnsPerHost=%d MaxIdleConns=%d MaxIdleConnsPerHost=%d IdleConnTimeout=%s TLSHandshakeTimeout=%s ResponseHeaderTimeout=%s ExpectContinueTimeout=%s DialContext=%v",
			transport.MaxConnsPerHost, transport.MaxIdleConns, transport.MaxIdleConnsPerHost, transport.IdleConnTimeout, transport.TLSHandshakeTimeout, transport.ResponseHeaderTimeout, transport.ExpectContinueTimeout, transport.DialContext != nil)
	}

	custom := &http.Client{Timeout: 3 * time.Second}
	withCustom, err := NewClient(Config{
		BaseURL:    "http://127.0.0.1:8123",
		Database:   "telemetry_history",
		Table:      "observations",
		HTTPClient: custom,
	})
	if err != nil {
		t.Fatal(err)
	}
	if withCustom.httpClient != custom {
		t.Fatal("expected custom HTTPClient to be preserved")
	}
}

func pointRowJSON(observationID, acceptance, valueType, valueJSON, sampledAt string) string {
	return `{"observation_id":"` + observationID + `","point_id":"` + historyPointID + `","sensor_id":null,"telemetry_key":"zone.mode","point_type":"STATE","point_revision":7,"sampled_at":"` + sampledAt + `","received_at":"2026-08-19T01:05:00.000Z","acceptance_status":"` + acceptance + `","value_type":"` + valueType + `","value_json":"` + valueJSON + `","unit":null,"quality":"GOOD","quality_reasons":[],"source_event_id":"` + observationID + `","source_partition":"mqtt:gateway:device:zone.mode","source_offset":42}
`
}

func TestClickHouseHistoryClientPingAndGzipSelectDecompression(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "hist_user" || pass != "hist_pass" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Header.Get("Accept-Encoding") != "gzip" {
			t.Fatalf("expected Accept-Encoding: gzip, got %q", r.Header.Get("Accept-Encoding"))
		}
		if r.URL.Query().Get("enable_http_compression") != "1" {
			t.Fatalf("expected enable_http_compression=1, got %v", r.URL.Query())
		}
		w.Header().Set("Content-Encoding", "gzip")
		gz := gzip.NewWriter(w)
		if r.URL.Query().Get("query") == "SELECT 1" {
			_, _ = io.WriteString(gz, "1\n")
			_ = gz.Close()
			return
		}
		sqlBytes, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		sql := string(sqlBytes)
		if strings.Contains(sql, "snapshot_cutoff") {
			_, _ = io.WriteString(gz, `{"snapshot_at":"2026-08-19T01:10:00.000Z","projection_watermark":null}`+"\n")
		} else {
			_, _ = io.WriteString(gz, pointRowJSON("018f2e00-5000-7000-8000-000000000001", "ACCEPTED", "STRING", `\"COOL\"`, "2026-08-19T01:00:00.000Z"))
		}
		_ = gz.Close()
	}))
	defer server.Close()

	client, err := NewClient(Config{
		BaseURL:  server.URL,
		Database: "telemetry_history",
		Table:    "observations",
		Username: "hist_user",
		Password: "hist_pass",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Ping(context.Background()); err != nil {
		t.Fatalf("Ping failed: %v", err)
	}
	resp, err := client.QueryDeviceHistory(context.Background(), telemetryhistorymodel.DeviceHistoryQuery{
		TenantID: historyTenantID,
		SiteID:   historySiteID,
		DeviceID: historyDeviceID,
		Keys:     []string{"zone.mode"},
		From:     time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC),
		To:       time.Date(2026, 8, 19, 2, 0, 0, 0, time.UTC),
		PageSize: 10,
	})
	if err != nil {
		t.Fatalf("QueryDeviceHistory failed: %v", err)
	}
	if len(resp.Observations) != 1 || resp.Observations[0].ObservationID != "018f2e00-5000-7000-8000-000000000001" {
		t.Fatalf("unexpected gzip response observations: %+v", resp.Observations)
	}
}
