package telemetry

import (
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const retentionDevice = "018f2e00-3000-7000-8000-0000000000d1"

func retentionID(suffix int) string {
	return fmt.Sprintf("018f2e00-9900-7000-8000-0000000000%02d", suffix)
}

// Pruning removes only what nothing reads any more: presence signals older than an hour
// that a newer signal of the same type supersedes, publications older than an hour whose
// every delivery finished, history events an hour after ClickHouse has them, and received
// observations a week old whose event is gone. The last signal of a long-offline Device
// stays, because presence reports when it was last seen; an undelivered event keeps its
// observation.
func TestPostgresPruneRemovesOnlyFinishedHistory(t *testing.T) {
	runtimeURL, adminURL := postgresTestURLs(t)
	ctx := t.Context()
	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	cleanup := func() {
		_, _ = admin.Exec(ctx, `DELETE FROM telemetry_runtime.telemetry_history_outbox WHERE event_id::text LIKE '018f2e00-9900-%'`)
		_, _ = admin.Exec(ctx, `DELETE FROM telemetry_runtime.source_observations WHERE observation_id::text LIKE '018f2e00-9900-%'`)
		_, _ = admin.Exec(ctx, `DELETE FROM telemetry_runtime.telemetry_publication_outbox WHERE event_id::text LIKE '018f2e00-9900-%'`)
		_, _ = admin.Exec(ctx, `DELETE FROM telemetry_runtime.presence_signals WHERE signal_id::text LIKE '018f2e00-9900-%'`)
		_, _ = admin.Exec(ctx, `DELETE FROM telemetry_runtime.devices WHERE device_id = $1::uuid`, retentionDevice)
	}
	cleanup()
	defer cleanup()

	// Earlier than every other fixture, so the cutoff only reaches this test's rows.
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	if _, err := admin.Exec(ctx, `INSERT INTO telemetry_runtime.devices (device_id, tenant_id, site_id, updated_at) VALUES ($1::uuid, $2::uuid, $3::uuid, $4)`,
		retentionDevice, tenantA, siteA, now); err != nil {
		t.Fatal(err)
	}
	signal := func(suffix int, deviceID string, observedAt time.Time) {
		t.Helper()
		if _, err := admin.Exec(ctx, `
INSERT INTO telemetry_runtime.presence_signals
  (signal_id, device_id, signal_type, observed_at, received_at, accepted, policy_revision, source_event_id, created_at)
VALUES ($1::uuid, $2::uuid, 'SOURCE_ACTIVITY', $3, $3, true, 1, $1::uuid, $3)`, retentionID(suffix), deviceID, observedAt); err != nil {
			t.Fatal(err)
		}
	}
	signal(1, deviceA, now.Add(-48*time.Hour))         // superseded and old: pruned
	signal(2, deviceA, now.Add(-10*time.Minute))       // recent: kept
	signal(3, retentionDevice, now.Add(-72*time.Hour)) // last signal of an offline Device: kept

	// Device snapshot publications start with the latest cache PENDING (an insert
	// trigger); the relays then record each delivery.
	publication := func(suffix int, revision int64, delivery, latestCache string, alarm *string, createdAt time.Time) {
		t.Helper()
		if _, err := admin.Exec(ctx, `
INSERT INTO telemetry_runtime.telemetry_publication_outbox
  (event_id, device_id, business_revision, event_family, payload, payload_sha256, delivery_state, available_at, created_at)
VALUES ($1::uuid, $2::uuid, $3, 'hvac.telemetry.device-snapshot.v1', '{}'::jsonb, repeat('a', 64), 'PENDING', $4, $4)`,
			retentionID(suffix), deviceA, revision, createdAt); err != nil {
			t.Fatal(err)
		}
		if _, err := admin.Exec(ctx, `
UPDATE telemetry_runtime.telemetry_publication_outbox
SET delivery_state = $2,
    published_at = CASE WHEN $2 = 'PUBLISHED' THEN created_at END,
    latest_cache_state = $3,
    latest_cache_materialized_at = CASE WHEN $3 = 'MATERIALIZED' THEN created_at END,
    alarm_delivery_state = $4,
    alarm_published_at = CASE WHEN $4 = 'PUBLISHED' THEN created_at END
WHERE event_id = $1::uuid`, retentionID(suffix), delivery, latestCache, alarm); err != nil {
			t.Fatal(err)
		}
	}
	published := "PUBLISHED"
	old := now.Add(-48 * time.Hour)
	publication(11, 900011, "PUBLISHED", "MATERIALIZED", &published, old)               // every delivery finished: pruned
	publication(12, 900012, "PENDING", "MATERIALIZED", nil, old)                        // still to publish: kept
	publication(13, 900013, "DEAD", "MATERIALIZED", nil, old)                           // failure evidence: kept
	publication(14, 900014, "PUBLISHED", "PENDING", nil, old)                           // latest cache pending: kept
	publication(15, 900015, "PUBLISHED", "MATERIALIZED", nil, now.Add(-10*time.Minute)) // recent: kept

	// A rejected observation has no history event; an accepted one carries one until pruned.
	observation := func(suffix int, createdAt time.Time, event string) {
		t.Helper()
		if _, err := admin.Exec(ctx, `
INSERT INTO telemetry_runtime.source_observations
  (observation_id, source_id, source_event_id, source_partition, source_offset, telemetry_key, sampled_at, received_at,
   acceptance_status, payload_sha256, created_at, source_path, quality)
VALUES ($1::uuid, 'retention-test', $1::uuid, 'retention', $2::bigint, 'temperature', $3::timestamptz, $3::timestamptz, 'REJECTED', repeat('a', 64), $3::timestamptz, 'PUSH', 'INVALID')`,
			retentionID(suffix), suffix, createdAt); err != nil {
			t.Fatal(err)
		}
		if event == "" {
			return
		}
		if _, err := admin.Exec(ctx, `
INSERT INTO telemetry_runtime.telemetry_history_outbox
  (event_id, payload, outbox_payload_sha256, delivery_state, available_at, published_at, created_at)
VALUES ($1::uuid, '{}'::jsonb, repeat('a', 64), $2::text, $3::timestamptz, CASE WHEN $2::text = 'PUBLISHED' THEN $3::timestamptz END, $3::timestamptz)`,
			retentionID(suffix), event, createdAt); err != nil {
			t.Fatal(err)
		}
	}
	observation(21, now.Add(-10*24*time.Hour), "PUBLISHED") // delivered long ago: event and observation pruned
	observation(22, now.Add(-10*24*time.Hour), "PENDING")   // undelivered: event and observation kept
	observation(23, now.Add(-2*24*time.Hour), "PUBLISHED")  // delivered, observation within the week: event pruned
	observation(24, now.Add(-10*24*time.Hour), "")          // rejected long ago, no event: observation pruned

	store, err := OpenPostgresStore(ctx, runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	pruned, err := store.PruneRuntimeHistory(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if pruned != (PrunedRuntimeHistory{PresenceSignals: 1, Publications: 1, HistoryEvents: 2, SourceObservations: 2}) {
		t.Fatalf("pruned=%#v", pruned)
	}
	var remaining string
	if err := admin.QueryRow(ctx, `
SELECT string_agg(right(id, 2), ',' ORDER BY id) FROM (
  SELECT signal_id::text AS id FROM telemetry_runtime.presence_signals WHERE signal_id::text LIKE '018f2e00-9900-%'
  UNION ALL
  SELECT event_id::text FROM telemetry_runtime.telemetry_publication_outbox WHERE event_id::text LIKE '018f2e00-9900-%'
  UNION ALL
  SELECT 'o' || observation_id::text FROM telemetry_runtime.source_observations WHERE observation_id::text LIKE '018f2e00-9900-%'
  UNION ALL
  SELECT 'h' || event_id::text FROM telemetry_runtime.telemetry_history_outbox WHERE event_id::text LIKE '018f2e00-9900-%'
) rows`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != "02,03,12,13,14,15,22,22,23" {
		t.Fatalf("remaining=%s", remaining)
	}
}
