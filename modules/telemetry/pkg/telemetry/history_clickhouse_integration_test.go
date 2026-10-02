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

func TestPostgresOutboxProjectsClickHouseHistoryDeduplicatesRetry(t *testing.T) {
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
		"018f2e00-9300-7000-8000-000000000001", sourceA, partition, 1, SourcePathPoll,
		"mqtt-device-tenant-a-site-1", "zone.temperature", json.RawMessage(`24.75`), "NUMBER", "Cel",
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
	batch, err := repository.ClaimHistoryBatch(ctx, 16, clock, 30*time.Second, 4)
	if err != nil || len(batch.Observations) != 1 {
		t.Fatalf("claim=%+v err=%v", batch, err)
	}
	if err := sink.InsertObservations(ctx, batch.Observations); err != nil {
		t.Fatal(err)
	}
	// Simulate a process dying after ClickHouse succeeds but before PG acknowledgement.
	assertClickHouseObservation(t, clickHouseURL, candidate.Position.EventID, "1\t24.75\tACCEPTED\tGOOD")
	assertClickHouseHourly(t, clickHouseURL, "1\t24.75\t24.75\t24.75")

	clock = clock.Add(31 * time.Second)
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
WHERE tenant_id = toUUID('%s')
  AND site_id = toUUID('%s')
  AND device_id = toUUID('%s')
  AND telemetry_key = 'zone.temperature'
  AND hour = toDateTime('2026-07-29 08:00:00', 'UTC')
FORMAT TSVRaw
`, tenantA, siteA, deviceA)
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
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, endpoint.String(), nil)
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

func TestPostgresHistoricalReplayProjectsClickHouseWithoutCurrentMutation(t *testing.T) {
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
	const partition = "tb-history-replay-integration"
	for _, statement := range []string{
		`DELETE FROM telemetry_runtime.telemetry_history_outbox WHERE payload ->> 'source_partition' = $1`,
		`DELETE FROM telemetry_runtime.source_delivery_evidence WHERE source_partition = $1`,
		`DELETE FROM telemetry_runtime.source_observations WHERE source_partition = $1`,
		`DELETE FROM telemetry_runtime.source_positions WHERE source_partition = $1`,
	} {
		if _, err := admin.Exec(ctx, statement, partition); err != nil {
			t.Fatal(err)
		}
	}

	store, err := OpenPostgresStore(ctx, runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	baselineAt := time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
	if _, err := store.EvaluateAndRead(ctx, telemetryauth.Target{DeviceID: deviceA}, baselineAt); err != nil {
		t.Fatal(err)
	}

	var latestValue, snapshotSHA string
	var latestRevision, snapshotRevision, presenceSignals int64
	if err := admin.QueryRow(ctx, `SELECT value::text, business_revision FROM telemetry_runtime.latest_accepted_telemetry WHERE device_id=$1::uuid AND telemetry_key='zone.temperature'`, deviceA).Scan(&latestValue, &latestRevision); err != nil {
		t.Fatal(err)
	}
	if err := admin.QueryRow(ctx, `SELECT business_revision, snapshot_sha256 FROM telemetry_runtime.device_observation_snapshots WHERE device_id=$1::uuid`, deviceA).Scan(&snapshotRevision, &snapshotSHA); err != nil {
		t.Fatal(err)
	}
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM telemetry_runtime.presence_signals WHERE device_id=$1::uuid`, deviceA).Scan(&presenceSignals); err != nil {
		t.Fatal(err)
	}

	receivedAt := baselineAt.Add(time.Minute)
	replay := ingestCandidate(
		"018f2e00-9300-7000-8000-000000000002", sourceA, partition, 1, SourcePathHistoryReplay,
		"mqtt-device-tenant-a-site-1", "zone.temperature", json.RawMessage(`21.5`), "NUMBER", "Cel",
		time.Date(2026, 7, 23, 0, 10, 0, 0, time.UTC), receivedAt,
	)
	receipt, err := store.AcceptHistoricalObservation(ctx, replay)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Status != ObservationAccepted || receipt.Quality != QualityGood || receipt.BusinessRevision != 0 || receipt.StateChanged {
		t.Fatalf("Historical Replay receipt=%#v", receipt)
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
	relay, err := NewHistoryRelay(HistoryRelayConfig{
		Repository: repository, Sink: sink, BatchSize: 16,
		LeaseFor: 30 * time.Second, RetryAfter: time.Second, MaxAttempts: 4,
		Now: func() time.Time { return receivedAt.Add(time.Second) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if projected, err := relay.RelayOnce(ctx); err != nil || projected != 1 {
		t.Fatalf("Historical Replay projection=%d err=%v", projected, err)
	}
	query := fmt.Sprintf(`
SELECT count(), any(value_number), any(acceptance_status), any(quality), any(source_path)
FROM telemetry_history.observations
WHERE source_event_id = toUUID('%s')
FORMAT TSVRaw
`, replay.Position.EventID)
	if actual := clickHouseQuery(t, clickHouseURL, query); actual != "1\t21.5\tACCEPTED\tGOOD\tHISTORY_REPLAY" {
		t.Fatalf("Historical Replay ClickHouse observation=%q", actual)
	}

	var afterLatestValue, afterSnapshotSHA string
	var afterLatestRevision, afterSnapshotRevision, afterPresenceSignals int64
	if err := admin.QueryRow(ctx, `SELECT value::text, business_revision FROM telemetry_runtime.latest_accepted_telemetry WHERE device_id=$1::uuid AND telemetry_key='zone.temperature'`, deviceA).Scan(&afterLatestValue, &afterLatestRevision); err != nil {
		t.Fatal(err)
	}
	if err := admin.QueryRow(ctx, `SELECT business_revision, snapshot_sha256 FROM telemetry_runtime.device_observation_snapshots WHERE device_id=$1::uuid`, deviceA).Scan(&afterSnapshotRevision, &afterSnapshotSHA); err != nil {
		t.Fatal(err)
	}
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM telemetry_runtime.presence_signals WHERE device_id=$1::uuid`, deviceA).Scan(&afterPresenceSignals); err != nil {
		t.Fatal(err)
	}
	if afterLatestValue != latestValue || afterLatestRevision != latestRevision || afterSnapshotRevision != snapshotRevision || afterSnapshotSHA != snapshotSHA || afterPresenceSignals != presenceSignals {
		t.Fatalf("Historical Replay mutated Current: latest=%s/%d -> %s/%d snapshot=%d/%s -> %d/%s presence=%d -> %d", latestValue, latestRevision, afterLatestValue, afterLatestRevision, snapshotRevision, snapshotSHA, afterSnapshotRevision, afterSnapshotSHA, presenceSignals, afterPresenceSignals)
	}
}
