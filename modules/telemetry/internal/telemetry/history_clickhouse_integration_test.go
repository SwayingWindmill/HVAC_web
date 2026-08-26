package telemetry

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
)

func TestPostgresOutboxProjectsClickHouseHistoryExactlyOnce(t *testing.T) {
	runtimeURL, adminURL := postgresTestURLs(t)
	historyURL := os.Getenv("S2_TELEMETRY_HISTORY_DATABASE_URL")
	clickHouseURL := os.Getenv("S2_CLICKHOUSE_HTTP_URL")
	if historyURL == "" || clickHouseURL == "" {
		t.Skip("S2 ClickHouse history integration environment is not configured")
	}
	ctx := t.Context()
	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	resetIngestState(t, admin)
	const partition = "tb-history-clickhouse-integration"
	if _, err := admin.Exec(ctx, `DELETE FROM telemetry_runtime.telemetry_history_outbox WHERE payload ->> 'source_partition' = $1`, partition); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `DELETE FROM telemetry_runtime.source_observations WHERE source_partition = $1`, partition); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `DELETE FROM telemetry_runtime.source_positions WHERE source_partition = $1`, partition); err != nil {
		t.Fatal(err)
	}

	store, err := OpenPostgresStore(ctx, runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	baselineAt := time.Date(2026, 7, 29, 7, 59, 55, 0, time.UTC)
	if _, err := store.EvaluateAndRead(ctx, telemetryauth.Target{DeviceID: deviceA}, baselineAt); err != nil {
		t.Fatal(err)
	}
	observedAt := time.Date(2026, 7, 29, 8, 0, 2, 0, time.UTC)
	candidate := ingestCandidate(
		"018f2e00-9300-7000-8000-000000000001", integrationA, partition, 1, SourcePathPoll,
		"tb-device-org-a-site-1", "zone.temperature", json.RawMessage(`24.75`), "NUMBER", "Cel",
		observedAt.Add(-2*time.Second), observedAt,
	)
	receipt, err := store.AcceptObservation(ctx, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Status != ObservationAccepted {
		t.Fatalf("receipt=%#v", receipt)
	}

	repository, err := OpenHistoryPostgresRepository(ctx, historyURL)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	sink, err := NewClickHouseHistorySink(ClickHouseHistoryConfig{
		BaseURL: clickHouseURL, Database: "telemetry_history", Table: "observations",
		Username: os.Getenv("S2_CLICKHOUSE_USERNAME"), Password: os.Getenv("S2_CLICKHOUSE_PASSWORD"),
	})
	if err != nil {
		t.Fatal(err)
	}
	clock := observedAt.Add(time.Second)
	relay, err := NewHistoryRelay(HistoryRelayConfig{
		Repository: repository, Sink: sink, BatchSize: 16,
		LeaseFor: 30 * time.Second, RetryAfter: time.Second, MaxAttempts: 4,
		Now: func() time.Time { return clock },
	})
	if err != nil {
		t.Fatal(err)
	}
	if projected, err := relay.RelayOnce(ctx); err != nil || projected != 1 {
		t.Fatalf("first projection=%d err=%v", projected, err)
	}
	assertClickHouseObservation(t, clickHouseURL, candidate.Position.EventID, "1\t24.75\tACCEPTED\tGOOD")
	assertClickHouseHourly(t, clickHouseURL, "1\t24.75\t24.75\t24.75")

	if _, err := admin.Exec(ctx, `
UPDATE telemetry_runtime.telemetry_history_outbox
SET delivery_state = 'PENDING', available_at = $2, published_at = NULL,
    lease_id = NULL, leased_until = NULL, last_error_code = NULL
WHERE event_id = $1::uuid
`, receipt.ObservationID, clock.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(2 * time.Second)
	if projected, err := relay.RelayOnce(ctx); err != nil || projected != 1 {
		t.Fatalf("retry projection=%d err=%v", projected, err)
	}
	assertClickHouseObservation(t, clickHouseURL, candidate.Position.EventID, "1\t24.75\tACCEPTED\tGOOD")
	assertClickHouseHourly(t, clickHouseURL, "1\t24.75\t24.75\t24.75")

	var state string
	var attempts int
	if err := admin.QueryRow(ctx, `
SELECT delivery_state, attempts
FROM telemetry_runtime.telemetry_history_outbox
WHERE event_id = $1::uuid
`, receipt.ObservationID).Scan(&state, &attempts); err != nil {
		t.Fatal(err)
	}
	if state != "PUBLISHED" || attempts != 2 {
		t.Fatalf("history outbox state=%s attempts=%d", state, attempts)
	}
}

func assertClickHouseObservation(t *testing.T, baseURL, sourceEventID, expected string) {
	t.Helper()
	query := fmt.Sprintf(`
SELECT count(), any(value_number), any(acceptance_status), any(quality)
FROM telemetry_history.observations
WHERE source_event_id = toUUID('%s')
FORMAT TSVRaw
`, sourceEventID)
	if actual := clickHouseQuery(t, baseURL, query); actual != expected {
		t.Fatalf("ClickHouse observation=%q expected=%q", actual, expected)
	}
}

func assertClickHouseHourly(t *testing.T, baseURL, expected string) {
	t.Helper()
	query := fmt.Sprintf(`
SELECT sample_count, average_value, minimum_value, maximum_value
FROM telemetry_history.numeric_hourly
WHERE owning_organization_id = toUUID('%s')
  AND site_id = toUUID('%s')
  AND device_id = toUUID('%s')
  AND telemetry_key = 'zone.temperature'
  AND hour = toDateTime('2026-07-29 08:00:00', 'UTC')
FORMAT TSVRaw
`, orgA, siteA, deviceA)
	if actual := clickHouseQuery(t, baseURL, query); actual != expected {
		t.Fatalf("ClickHouse hourly=%q expected=%q", actual, expected)
	}
}

func clickHouseQuery(t *testing.T, baseURL, query string) string {
	t.Helper()
	endpoint, err := url.Parse(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	values := endpoint.Query()
	values.Set("query", query)
	endpoint.RawQuery = values.Encode()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, endpoint.String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if username := os.Getenv("S2_CLICKHOUSE_USERNAME"); username != "" {
		request.SetBasicAuth(username, os.Getenv("S2_CLICKHOUSE_PASSWORD"))
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 16<<10))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		t.Fatalf("ClickHouse query returned %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	return strings.TrimSpace(string(body))
}
