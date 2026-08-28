package telemetry

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
	"github.com/quanlaihe/hvac-web/modules/telemetry/pkg/telemetryapi"
)

const (
	deviceB = "018f2e00-3000-7000-8000-000000000003"
	tenantB = "018f2d00-0000-7000-8000-000000000002"
	orgB    = "018f2e00-0000-7000-8000-000000000002"
)

func TestPostgresSnapshotTransactionRevisionAndRollback(t *testing.T) {
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
	target := telemetryauth.Target{DeviceID: deviceA, Keys: []string{"zone.humidity", "zone.temperature", "duct.pressure"}}
	firstAt := time.Date(2026, 7, 23, 0, 0, 5, 0, time.UTC)
	first, err := store.EvaluateAndRead(ctx, target, firstAt)
	if err != nil {
		t.Fatal(err)
	}
	if !first.StateChanged || first.PreviousRevision != 0 || first.Snapshot.BusinessRevision != 1 {
		t.Fatalf("first commit=%#v", first)
	}
	if first.Snapshot.DisplayState == nil || *first.Snapshot.DisplayState != telemetryapi.DeviceDisplayStateUnknown || first.Snapshot.TelemetryReadiness != telemetryapi.TelemetryReadinessIncomplete {
		t.Fatalf("first snapshot display/readiness=%v/%s", first.Snapshot.DisplayState, first.Snapshot.TelemetryReadiness)
	}
	if len(first.Snapshot.Values) != 3 || first.Snapshot.Values[0].Missing == nil || first.Snapshot.Values[0].Missing.Key != "zone.humidity" || first.Snapshot.Values[1].Present == nil || first.Snapshot.Values[1].Present.Key != "zone.temperature" || first.Snapshot.Values[2].Missing == nil || first.Snapshot.Values[2].Missing.MissingReason != "ONLY_REJECTED_CANDIDATES" {
		t.Fatalf("ordered snapshot values=%#v", first.Snapshot.Values)
	}
	assertCurrentTransactionState(t, admin, 1, 1, "ONLINE", "AVAILABLE")

	refresh, err := store.EvaluateAndRead(ctx, target, firstAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if refresh.StateChanged || refresh.PreviousRevision != 1 || refresh.Snapshot.BusinessRevision != 1 {
		t.Fatalf("refresh commit=%#v", refresh)
	}
	assertCurrentTransactionState(t, admin, 1, 1, "ONLINE", "AVAILABLE")

	if _, err := admin.Exec(ctx, `UPDATE telemetry_runtime.freshness_policies SET policy_revision = 6 WHERE device_id = $1::uuid AND telemetry_key = 'zone.temperature'`, deviceA); err != nil {
		t.Fatal(err)
	}
	policyChange, err := store.EvaluateAndRead(ctx, target, firstAt.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !policyChange.StateChanged || policyChange.Snapshot.BusinessRevision != 2 || policyChange.Snapshot.Values[1].Present == nil || policyChange.Snapshot.Values[1].Present.PolicyRevision != 6 {
		t.Fatalf("policy change=%#v", policyChange)
	}
	assertCurrentTransactionState(t, admin, 2, 2, "ONLINE", "AVAILABLE")

	if _, err := admin.Exec(ctx, `UPDATE telemetry_runtime.observation_coverage SET available = false, continuous_since = NULL, reason_code = 'OBSERVATION_COVERAGE_GAP', source_revision = source_revision + 1, updated_at = $2 WHERE device_id = $1::uuid`, deviceA, firstAt.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	outage, err := store.EvaluateAndRead(ctx, target, firstAt.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !outage.StateChanged || outage.Snapshot.BusinessRevision != 3 || outage.Snapshot.EvaluationAvailability != telemetryapi.EvaluationAvailabilityUnavailable || outage.Snapshot.Presence.CurrentState != nil || outage.Snapshot.DisplayState == nil || *outage.Snapshot.DisplayState != telemetryapi.DeviceDisplayStateUnavailable {
		t.Fatalf("outage=%#v", outage)
	}
	assertCurrentTransactionState(t, admin, 3, 3, "", "UNAVAILABLE")

	var duplicateEventID string
	if err := admin.QueryRow(ctx, `SELECT event_id::text FROM telemetry_runtime.telemetry_publication_outbox WHERE device_id = $1::uuid AND subscription_id IS NULL ORDER BY business_revision LIMIT 1`, deviceA).Scan(&duplicateEventID); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `UPDATE telemetry_runtime.observation_coverage SET available = true, continuous_since = $2, reason_code = NULL, source_revision = source_revision + 1, updated_at = $3 WHERE device_id = $1::uuid`, deviceA, firstAt.Add(-time.Hour), firstAt.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	failingStore := NewPostgresStore(store.pool, func(time.Time) (string, error) { return duplicateEventID, nil })
	if _, err := failingStore.EvaluateAndRead(ctx, target, firstAt.Add(4*time.Second)); err == nil {
		t.Fatal("duplicate outbox event unexpectedly committed")
	}
	assertCurrentTransactionState(t, admin, 3, 3, "", "UNAVAILABLE")

	recovered, err := store.EvaluateAndRead(ctx, target, firstAt.Add(4*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !recovered.StateChanged || recovered.Snapshot.BusinessRevision != 4 || recovered.Snapshot.EvaluationAvailability != telemetryapi.EvaluationAvailabilityAvailable {
		t.Fatalf("recovered=%#v", recovered)
	}
	assertCurrentTransactionState(t, admin, 4, 4, "ONLINE", "AVAILABLE")
}

func TestPostgresSnapshotTwoOrganizationIsolationAndRuntimeRole(t *testing.T) {
	runtimeURL, adminURL := postgresTestURLs(t)
	ctx := t.Context()
	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if _, err := admin.Exec(ctx, `DELETE FROM telemetry_runtime.telemetry_publication_outbox WHERE device_id = $1::uuid AND subscription_id IS NULL`, deviceB); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `DELETE FROM telemetry_runtime.device_observation_snapshots WHERE device_id = $1::uuid`, deviceB); err != nil {
		t.Fatal(err)
	}

	store, err := OpenPostgresStore(ctx, runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	commit, err := store.EvaluateAndRead(ctx, telemetryauth.Target{DeviceID: deviceB, Keys: []string{"zone.temperature"}}, time.Date(2026, 7, 23, 0, 0, 5, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if string(commit.Snapshot.TenantId) != tenantB || string(commit.Snapshot.DeviceId) != deviceB || commit.Snapshot.EvaluationAvailability != telemetryapi.EvaluationAvailabilityUnavailable || len(commit.Snapshot.Values) != 1 || commit.Snapshot.Values[0].Missing == nil {
		t.Fatalf("Organization B snapshot=%#v", commit.Snapshot)
	}
	encoded, err := jsonString(commit.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(encoded, orgA) || strings.Contains(encoded, deviceA) {
		t.Fatalf("Organization B snapshot disclosed Organization A: %s", encoded)
	}

	runtimePool, err := pgxpool.New(ctx, runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer runtimePool.Close()
	var count int
	err = runtimePool.QueryRow(ctx, `SELECT count(*) FROM telemetry_runtime.device_observation_snapshots`).Scan(&count)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "permission denied") {
		t.Fatalf("runtime login read without activation err=%v count=%d", err, count)
	}

	wrongURL := strings.Replace(runtimeURL, "s2_telemetry_service", "postgres", 1)
	if _, err := OpenPostgresStore(ctx, wrongURL); err == nil || !strings.Contains(err.Error(), "s2_telemetry_service") {
		t.Fatalf("wrong database identity err=%v", err)
	}
}

func resetDeviceAState(t *testing.T, admin *pgxpool.Pool) {
	t.Helper()
	ctx := t.Context()
	statements := []string{
		`DELETE FROM telemetry_runtime.telemetry_publication_outbox WHERE device_id = $1::uuid AND subscription_id IS NULL`,
		`DELETE FROM telemetry_runtime.device_observation_snapshots WHERE device_id = $1::uuid`,
		`DELETE FROM telemetry_runtime.device_presence WHERE device_id = $1::uuid`,
		`UPDATE telemetry_runtime.freshness_policies SET policy_revision = 5 WHERE device_id = $1::uuid AND telemetry_key = 'zone.temperature'`,
		`UPDATE telemetry_runtime.observation_coverage SET available = true, continuous_since = '2026-07-22T23:00:00Z', reason_code = NULL, source_revision = 1, updated_at = '2026-07-23T00:00:05Z' WHERE device_id = $1::uuid`,
		`UPDATE telemetry_runtime.latest_accepted_telemetry SET business_revision = 1, freshness = 'FRESH', policy_revision = 5, updated_at = '2026-07-23T00:00:05Z' WHERE device_id = $1::uuid`,
	}
	for _, statement := range statements {
		if _, err := admin.Exec(ctx, statement, deviceA); err != nil {
			t.Fatal(err)
		}
	}
}

func assertCurrentTransactionState(t *testing.T, admin *pgxpool.Pool, revision, nullOutboxCount int64, presence, availability string) {
	t.Helper()
	ctx := t.Context()
	var snapshotRevision, presenceRevision, latestRevision, outboxCount int64
	var currentPresence *string
	var currentAvailability string
	err := admin.QueryRow(ctx, `
SELECT s.business_revision, p.business_revision,
       COALESCE((SELECT max(business_revision) FROM telemetry_runtime.latest_accepted_telemetry WHERE device_id = s.device_id), 0),
       (SELECT count(*) FROM telemetry_runtime.telemetry_publication_outbox WHERE device_id = s.device_id AND subscription_id IS NULL),
       p.current_state, s.evaluation_availability
FROM telemetry_runtime.device_observation_snapshots s
JOIN telemetry_runtime.device_presence p USING (device_id)
WHERE s.device_id = $1::uuid
`, deviceA).Scan(&snapshotRevision, &presenceRevision, &latestRevision, &outboxCount, &currentPresence, &currentAvailability)
	if err != nil {
		t.Fatal(err)
	}
	if snapshotRevision != revision || presenceRevision != revision || latestRevision != revision || outboxCount != nullOutboxCount || currentAvailability != availability {
		t.Fatalf("transaction state snapshot=%d presence=%d latest=%d outbox=%d availability=%s", snapshotRevision, presenceRevision, latestRevision, outboxCount, currentAvailability)
	}
	if presence == "" {
		if currentPresence != nil {
			t.Fatalf("presence=%v", *currentPresence)
		}
	} else if currentPresence == nil || *currentPresence != presence {
		t.Fatalf("presence=%v expected=%s", currentPresence, presence)
	}
}

func postgresTestURLs(t *testing.T) (string, string) {
	t.Helper()
	runtimeURL := os.Getenv("S2_TELEMETRY_TEST_DATABASE_URL")
	adminURL := os.Getenv("S2_TELEMETRY_ADMIN_DATABASE_URL")
	if runtimeURL == "" || adminURL == "" {
		t.Skip("S2 Telemetry PostgreSQL integration environment is not configured")
	}
	return runtimeURL, adminURL
}

func jsonString(value any) (string, error) {
	encoded, err := json.Marshal(value)
	return string(encoded), err
}
