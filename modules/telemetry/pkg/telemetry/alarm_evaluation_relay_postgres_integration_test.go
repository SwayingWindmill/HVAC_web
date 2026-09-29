package telemetry

import (
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
