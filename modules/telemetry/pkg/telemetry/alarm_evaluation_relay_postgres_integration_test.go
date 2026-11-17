package telemetry

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
)

func TestPostgresAlarmEvaluationDeliveryIsIndependentFromRealtimeDelivery(t *testing.T) {
	runtimeURL, adminURL := postgresTestURLs(t)
	ctx := t.Context()
	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	resetDeviceAState(t, admin)

	store, err := OpenPostgresStore(ctx, runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	committedAt := time.Date(2026, 8, 28, 16, 0, 0, 0, time.UTC)
	commit, err := store.EvaluateAndRead(ctx, telemetryauth.Target{DeviceID: deviceA, Keys: []string{"zone.temperature"}}, committedAt)
	if err != nil {
		t.Fatal(err)
	}
	if !commit.StateChanged {
		t.Fatal("expected canonical Device snapshot commit")
	}

	var eventID, realtimeState, alarmState string
	if err := admin.QueryRow(ctx, `
SELECT event_id::text, delivery_state, alarm_delivery_state
FROM telemetry_runtime.telemetry_publication_outbox
WHERE device_id=$1::uuid AND subscription_id IS NULL AND business_revision=$2
`, deviceA, int64(commit.Snapshot.BusinessRevision)).Scan(&eventID, &realtimeState, &alarmState); err != nil {
		t.Fatal(err)
	}
	if realtimeState != "PENDING" || alarmState != "PENDING" {
		t.Fatalf("canonical publication did not initialize independent delivery states: realtime=%s alarm=%s", realtimeState, alarmState)
	}

	alarmClaims, err := store.ClaimPendingAlarmEvaluations(ctx, "alarm-relay-a", 10, committedAt, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(alarmClaims) != 1 || alarmClaims[0].EventID != eventID {
		t.Fatalf("Alarm consumer did not claim canonical publication: %#v", alarmClaims)
	}

	realtimeClaims, err := store.ClaimPendingPublications(ctx, "realtime-relay-a", 10, committedAt, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !pendingPublicationContainsEvent(realtimeClaims, eventID) {
		t.Fatalf("realtime consumer was blocked by Alarm claim: %#v", realtimeClaims)
	}

	retryAt := committedAt.Add(5 * time.Second)
	if err := store.MarkAlarmEvaluationRetry(ctx, eventID, "alarm-relay-a", retryAt, "ALARM_EVALUATION_UNAVAILABLE"); err != nil {
		t.Fatal(err)
	}
	alarmClaims, err = store.ClaimPendingAlarmEvaluations(ctx, "alarm-relay-b", 10, retryAt.Add(-time.Nanosecond), 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(alarmClaims) != 0 {
		t.Fatalf("Alarm retry became claimable before alarm_available_at: %#v", alarmClaims)
	}
	alarmClaims, err = store.ClaimPendingAlarmEvaluations(ctx, "alarm-relay-b", 10, retryAt, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(alarmClaims) != 1 || alarmClaims[0].EventID != eventID {
		t.Fatalf("Alarm retry was not independently reclaimable: %#v", alarmClaims)
	}
	if err := store.MarkAlarmEvaluationDelivered(ctx, eventID, "alarm-relay-b", retryAt); err != nil {
		t.Fatal(err)
	}

	var realtimeAttempts, alarmAttempts int
	if err := admin.QueryRow(ctx, `
SELECT delivery_state, attempts, alarm_delivery_state, alarm_attempts
FROM telemetry_runtime.telemetry_publication_outbox
WHERE event_id=$1::uuid
`, eventID).Scan(&realtimeState, &realtimeAttempts, &alarmState, &alarmAttempts); err != nil {
		t.Fatal(err)
	}
	if realtimeState != "PENDING" || realtimeAttempts != 0 || alarmState != "PUBLISHED" || alarmAttempts != 2 {
		t.Fatalf("Alarm delivery mutated realtime state: realtime=%s/%d alarm=%s/%d", realtimeState, realtimeAttempts, alarmState, alarmAttempts)
	}

	if err := store.MarkPublicationPublished(ctx, eventID, "realtime-relay-a", retryAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := admin.QueryRow(ctx, `
SELECT delivery_state, attempts, alarm_delivery_state, alarm_attempts
FROM telemetry_runtime.telemetry_publication_outbox
WHERE event_id=$1::uuid
`, eventID).Scan(&realtimeState, &realtimeAttempts, &alarmState, &alarmAttempts); err != nil {
		t.Fatal(err)
	}
	if realtimeState != "PUBLISHED" || realtimeAttempts != 1 || alarmState != "PUBLISHED" || alarmAttempts != 2 {
		t.Fatalf("realtime delivery mutated Alarm state: realtime=%s/%d alarm=%s/%d", realtimeState, realtimeAttempts, alarmState, alarmAttempts)
	}
}

func pendingPublicationContainsEvent(publications []PendingPublication, eventID string) bool {
	for _, publication := range publications {
		if publication.EventID == eventID {
			return true
		}
	}
	return false
}

func TestPostgresAlarmEvaluationDeadLetterClearsLeaseAndStopsReclaim(t *testing.T) {
	runtimeURL, adminURL := postgresTestURLs(t)
	ctx := t.Context()
	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	resetDeviceAState(t, admin)

	store, err := OpenPostgresStore(ctx, runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	committedAt := time.Date(2026, 8, 28, 17, 0, 0, 0, time.UTC)
	commit, err := store.EvaluateAndRead(ctx, telemetryauth.Target{DeviceID: deviceA, Keys: []string{"zone.temperature"}}, committedAt)
	if err != nil {
		t.Fatal(err)
	}
	var eventID string
	if err := admin.QueryRow(ctx, `
SELECT event_id::text
FROM telemetry_runtime.telemetry_publication_outbox
WHERE device_id=$1::uuid AND subscription_id IS NULL AND business_revision=$2
`, deviceA, int64(commit.Snapshot.BusinessRevision)).Scan(&eventID); err != nil {
		t.Fatal(err)
	}

	// Also insert a malformed canonical row ahead of the valid row to confirm corrupt rows dead-letter instead of wedging claims.
	corruptEventID := "018f2e00-9800-7000-8000-000000000001"
	if _, err := admin.Exec(ctx, `
INSERT INTO telemetry_runtime.telemetry_publication_outbox (
  event_id, device_id, business_revision, subscription_id, event_family,
  payload, payload_sha256, delivery_state, available_at, attempts,
  last_error_code, published_at, alarm_delivery_state, alarm_available_at, created_at
) VALUES (
  $1::uuid, $2::uuid, 999, NULL, 'hvac.telemetry.device-snapshot.v1',
  '{"eventId":"invalid"}'::jsonb, repeat('c', 64), 'PENDING', $3, 0,
  NULL, NULL, 'PENDING', $4, $3
)
`, corruptEventID, deviceA, committedAt, committedAt.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `DELETE FROM telemetry_runtime.telemetry_publication_outbox WHERE event_id=$1::uuid`, corruptEventID)
	})

	claims, err := store.ClaimPendingAlarmEvaluations(ctx, "alarm-dlq-worker", 10, committedAt, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 1 || claims[0].EventID != eventID || claims[0].AlarmAttempts != 0 {
		t.Fatalf("expected valid claim after corrupt row isolation, got %#v", claims)
	}
	var corruptState, corruptError string
	if err := admin.QueryRow(ctx, `
SELECT alarm_delivery_state, alarm_last_error_code
FROM telemetry_runtime.telemetry_publication_outbox WHERE event_id=$1::uuid
`, corruptEventID).Scan(&corruptState, &corruptError); err != nil {
		t.Fatal(err)
	}
	if corruptState != "DEAD_LETTER" || corruptError != "ALARM_EVALUATION_PAYLOAD_INVALID" {
		t.Fatalf("corrupt row state=%s err=%s", corruptState, corruptError)
	}

	retryAt := committedAt.Add(5 * time.Second)
	if err := store.MarkAlarmEvaluationRetry(ctx, eventID, "alarm-dlq-worker", retryAt, "ALARM_EVALUATION_UNAVAILABLE"); err != nil {
		t.Fatal(err)
	}
	claims, err = store.ClaimPendingAlarmEvaluations(ctx, "alarm-dlq-worker", 10, retryAt, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 1 || claims[0].AlarmAttempts != 1 {
		t.Fatalf("expected AlarmAttempts=1 on reclaim, got %#v", claims)
	}

	deadAt := retryAt.Add(time.Second)
	if err := store.MarkAlarmEvaluationDeadLetter(ctx, eventID, "alarm-dlq-worker", deadAt, "ALARM_EVALUATION_PERMANENT_FAILURE"); err != nil {
		t.Fatal(err)
	}

	var alarmState, lastError string
	var alarmAttempts int
	var deadLetteredAt, publishedAt, claimUntil *time.Time
	var claimOwner *string
	if err := admin.QueryRow(ctx, `
SELECT alarm_delivery_state, alarm_attempts, alarm_last_error_code, alarm_dead_lettered_at, alarm_published_at, alarm_claim_owner, alarm_claim_until
FROM telemetry_runtime.telemetry_publication_outbox
WHERE event_id=$1::uuid
`, eventID).Scan(&alarmState, &alarmAttempts, &lastError, &deadLetteredAt, &publishedAt, &claimOwner, &claimUntil); err != nil {
		t.Fatal(err)
	}
	if alarmState != "DEAD_LETTER" || alarmAttempts != 2 || lastError != "ALARM_EVALUATION_PERMANENT_FAILURE" || deadLetteredAt == nil || publishedAt != nil || claimOwner != nil || claimUntil != nil {
		t.Fatalf("unexpected dead-letter row state: state=%s attempts=%d err=%s deadAt=%v pubAt=%v owner=%v until=%v",
			alarmState, alarmAttempts, lastError, deadLetteredAt, publishedAt, claimOwner, claimUntil)
	}

	claims, err = store.ClaimPendingAlarmEvaluations(ctx, "alarm-dlq-worker-2", 10, deadAt.Add(time.Hour), 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 0 {
		t.Fatalf("dead-lettered row was reclaimed: %#v", claims)
	}

	// Verify DB check constraint rejects DEAD_LETTER without alarm_dead_lettered_at and allows relay role update.
	txFail, err := admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := txFail.Exec(ctx, `
UPDATE telemetry_runtime.telemetry_publication_outbox
SET alarm_dead_lettered_at=NULL
WHERE event_id=$1::uuid
`, eventID); err == nil {
		_ = txFail.Rollback(ctx)
		t.Fatal("expected constraint rejection when clearing alarm_dead_lettered_at on DEAD_LETTER row")
	}
	_ = txFail.Rollback(ctx)

	txRelay, err := admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = txRelay.Rollback(ctx) }()
	if _, err := txRelay.Exec(ctx, `
UPDATE telemetry_runtime.telemetry_publication_outbox
SET alarm_delivery_state='PENDING', alarm_dead_lettered_at=NULL
WHERE event_id=$1::uuid
`, eventID); err != nil {
		t.Fatal(err)
	}
	if _, err := txRelay.Exec(ctx, `SET LOCAL ROLE s2_telemetry_relay`); err != nil {
		t.Fatal(err)
	}
	if _, err := txRelay.Exec(ctx, `
UPDATE telemetry_runtime.telemetry_publication_outbox
SET alarm_delivery_state='DEAD_LETTER', alarm_dead_lettered_at=$2, alarm_published_at=NULL,
    alarm_attempts=alarm_attempts+1, alarm_last_error_code='ALARM_EVALUATION_PERMANENT_FAILURE',
    alarm_claim_owner=NULL, alarm_claim_until=NULL
WHERE event_id=$1::uuid
`, eventID, deadAt); err != nil {
		t.Fatalf("s2_telemetry_relay must have UPDATE privilege on alarm_dead_lettered_at: %v", err)
	}
}
