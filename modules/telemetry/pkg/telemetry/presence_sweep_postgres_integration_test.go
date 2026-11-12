package telemetry

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/modules/telemetry/pkg/telemetryapi"
)

func sweepDeviceID(suffix int) string {
	return fmt.Sprintf("018f2e00-9700-7000-8000-0000000000%02d", suffix)
}

func sweepPointID(suffix int) string {
	return fmt.Sprintf("018f2e00-9710-7000-8000-0000000000%02d", suffix)
}

func sweepEventID(suffix int) string {
	return fmt.Sprintf("018f2e00-9720-7000-8000-000000000%03d", suffix)
}

func TestPostgresPresenceSweepAndCopiedReadings(t *testing.T) {
	runtimeURL, adminURL := postgresTestURLs(t)
	ctx := t.Context()
	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()

	const (
		sourceID  = "presence-sweep-test"
		partition = "presence-sweep-797"
	)
	deviceCopiedOnly := sweepDeviceID(1)
	deviceSilent := sweepDeviceID(2)
	deviceOwnActive := sweepDeviceID(3)

	cleanup := func() {
		_, _ = admin.Exec(ctx, `DELETE FROM telemetry_runtime.telemetry_history_outbox WHERE payload ->> 'source_partition' = $1`, partition)
		_, _ = admin.Exec(ctx, `DELETE FROM telemetry_runtime.source_delivery_evidence WHERE source_partition = $1`, partition)
		_, _ = admin.Exec(ctx, `DELETE FROM telemetry_runtime.source_observations WHERE source_partition = $1`, partition)
		_, _ = admin.Exec(ctx, `DELETE FROM telemetry_runtime.source_positions WHERE source_id = $1`, sourceID)
		_, _ = admin.Exec(ctx, `DELETE FROM telemetry_runtime.telemetry_publication_outbox WHERE device_id::text LIKE '018f2e00-9700-%'`)
		_, _ = admin.Exec(ctx, `DELETE FROM telemetry_runtime.recovery_cursors WHERE subscription_id IN (SELECT subscription_id FROM telemetry_runtime.telemetry_subscriptions WHERE device_id::text LIKE '018f2e00-9700-%')`)
		_, _ = admin.Exec(ctx, `DELETE FROM telemetry_runtime.telemetry_subscriptions WHERE device_id::text LIKE '018f2e00-9700-%'`)
		_, _ = admin.Exec(ctx, `DELETE FROM telemetry_runtime.device_observation_snapshots WHERE device_id::text LIKE '018f2e00-9700-%'`)
		_, _ = admin.Exec(ctx, `DELETE FROM telemetry_runtime.device_presence WHERE device_id::text LIKE '018f2e00-9700-%'`)
		_, _ = admin.Exec(ctx, `DELETE FROM telemetry_runtime.latest_accepted_telemetry WHERE device_id::text LIKE '018f2e00-9700-%'`)
		_, _ = admin.Exec(ctx, `DELETE FROM telemetry_runtime.presence_signals WHERE device_id::text LIKE '018f2e00-9700-%'`)
		_, _ = admin.Exec(ctx, `DELETE FROM telemetry_runtime.observation_coverage WHERE device_id::text LIKE '018f2e00-9700-%'`)
		_, _ = admin.Exec(ctx, `DELETE FROM telemetry_runtime.freshness_policies WHERE device_id::text LIKE '018f2e00-9700-%'`)
		_, _ = admin.Exec(ctx, `DELETE FROM telemetry_runtime.presence_policies WHERE device_id::text LIKE '018f2e00-9700-%'`)
		_, _ = admin.Exec(ctx, `DELETE FROM telemetry_runtime.points WHERE point_id::text LIKE '018f2e00-9710-%'`)
		_, _ = admin.Exec(ctx, `DELETE FROM telemetry_runtime.devices WHERE device_id::text LIKE '018f2e00-9700-%'`)
	}
	cleanup()
	defer cleanup()

	// Earlier than fixture timestamps so the sweep only transitions this test's devices.
	base := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	for _, deviceID := range []string{deviceCopiedOnly, deviceSilent, deviceOwnActive} {
		if _, err := admin.Exec(ctx, `
INSERT INTO telemetry_runtime.devices (device_id, tenant_id, site_id, presence_applicability, updated_at)
VALUES ($1::uuid, $2::uuid, $3::uuid, 'APPLICABLE', $4)`, deviceID, tenantA, siteA, base); err != nil {
			t.Fatal(err)
		}
		if _, err := admin.Exec(ctx, `
INSERT INTO telemetry_runtime.presence_policies (
  device_id, policy_revision, online_within_seconds, offline_after_seconds,
  coverage_required, accepted_signal_types, max_future_clock_skew_seconds, max_source_lag_seconds, updated_at
) VALUES ($1::uuid, 1, 60, 180, true, ARRAY['SOURCE_ACTIVITY']::text[], 60, 86400, $2)`, deviceID, base); err != nil {
			t.Fatal(err)
		}
		if _, err := admin.Exec(ctx, `
INSERT INTO telemetry_runtime.observation_coverage (device_id, available, continuous_since, reason_code, source_revision, updated_at)
VALUES ($1::uuid, true, $2, NULL, 1, $3)`, deviceID, base.Add(-time.Hour), base); err != nil {
			t.Fatal(err)
		}
		if _, err := admin.Exec(ctx, `
INSERT INTO telemetry_runtime.freshness_policies (
  device_id, telemetry_key, policy_revision, fresh_within_seconds, configured,
  expected_sample_interval_seconds, value_type, expected_unit, quality_checks, updated_at
) VALUES ($1::uuid, 'active_energy_total', 1, 600, true, 60, 'NUMBER', 'kWh', NULL, $2)`, deviceID, base); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := admin.Exec(ctx, `
INSERT INTO telemetry_runtime.freshness_policies (
  device_id, telemetry_key, policy_revision, fresh_within_seconds, configured,
  expected_sample_interval_seconds, value_type, expected_unit, quality_checks, updated_at
) VALUES ($1::uuid, 'chw_leaving_temperature', 1, 600, true, 60, 'NUMBER', 'Cel', '{"copied":true}'::jsonb, $2)`, deviceCopiedOnly, base); err != nil {
		t.Fatal(err)
	}

	store, err := OpenPostgresStore(ctx, runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	var nextOffset int64
	send := func(deviceID, pointID, key, rawValue, unit, pointType string, at time.Time) ObservationReceipt {
		t.Helper()
		nextOffset++
		var counterDecreaseMode *string
		if pointType == "COUNTER" {
			mode := "INVALID"
			counterDecreaseMode = &mode
		}
		receipt, err := store.AcceptObservation(ctx, ObservationCandidate{
			SourceID:           sourceID,
			SourcePath:         SourcePathPush,
			ExternalEntityType: "DEVICE",
			ExternalID:         deviceID,
			Device:             &ResolvedDevice{TenantID: tenantA, SiteID: siteA, DeviceID: deviceID},
			Point:              &ResolvedPoint{PointID: pointID, PointType: pointType, ValueType: "NUMBER", Unit: &unit, CounterDecreaseMode: counterDecreaseMode, PointRevision: 1},
			TelemetryKey:       key,
			Value:              json.RawMessage(rawValue),
			ValueType:          "NUMBER",
			Unit:               &unit,
			SampledAt:          at,
			ReceivedAt:         at,
			Position:           SourcePosition{Partition: partition, Offset: nextOffset, EventID: sweepEventID(int(nextOffset))},
		})
		if err != nil {
			t.Fatal(err)
		}
		if receipt.Status != ObservationAccepted {
			t.Fatalf("unexpected receipt=%#v", receipt)
		}
		return receipt
	}

	assertDevicePresence := func(deviceID, wantApplicability, wantState string, wantSignals, wantRevision, wantOutbox int64) {
		t.Helper()
		var applicability string
		var state, snapshotState *string
		var signals, revision, outbox int64
		if err := admin.QueryRow(ctx, `
SELECT dp.applicability, dp.current_state, s.snapshot #>> '{presence,currentState}', s.business_revision,
       (SELECT count(*) FROM telemetry_runtime.presence_signals ps WHERE ps.device_id = s.device_id AND ps.accepted),
       (SELECT count(*) FROM telemetry_runtime.telemetry_publication_outbox o WHERE o.device_id = s.device_id AND o.subscription_id IS NULL)
FROM telemetry_runtime.device_observation_snapshots s
JOIN telemetry_runtime.device_presence dp USING (device_id)
WHERE s.device_id = $1::uuid`, deviceID).Scan(&applicability, &state, &snapshotState, &revision, &signals, &outbox); err != nil {
			t.Fatal(err)
		}
		gotState := ""
		if state != nil {
			gotState = *state
		}
		gotSnapshotState := ""
		if snapshotState != nil {
			gotSnapshotState = *snapshotState
		}
		if applicability != wantApplicability || gotState != wantState || gotSnapshotState != wantState || signals != wantSignals || revision != wantRevision || outbox != wantOutbox {
			t.Fatalf("device %s: applicability=%s state=%s snapshotState=%s signals=%d revision=%d outbox=%d (want %s/%s/%d/%d/%d)",
				deviceID, applicability, gotState, gotSnapshotState, signals, revision, outbox,
				wantApplicability, wantState, wantSignals, wantRevision, wantOutbox)
		}
	}

	// Subscribe to deviceSilent and drain any prior pending outbox rows so RelayOnce isolates this device's sweep delta.
	relayNow := base
	transport := &RecordingRealtimeTransport{}
	realtime, err := NewRealtimeService(RealtimeConfig{
		Repository: store, Transport: transport,
		PublicEndpoint:         "wss://realtime.example.test/connection/websocket",
		CapabilityHMACKey:      []byte(strings.Repeat("c", 32)),
		ConnectionTokenHMACKey: []byte(strings.Repeat("t", 32)),
		Now:                    func() time.Time { return relayNow },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := realtime.Bootstrap(ctx, AccessContext{
		PrincipalID: realtimeTestPrincipal, Subject: "subject-sweep", SubjectIssuer: "https://issuer.example.test",
		SessionID: "session-sweep", TenantID: tenantA, PolicyRevision: "telemetry-access:1",
	}, telemetryapi.SubscriptionBootstrapRequest{Subscriptions: []telemetryapi.SubscriptionTargetRequest{
		{ClientSubscriptionId: "silent-energy", DeviceId: telemetryapi.UUIDv7(deviceSilent), Keys: []telemetryapi.TelemetryKey{"active_energy_total"}},
	}}); err != nil {
		t.Fatalf("bootstrap subscription: %v", err)
	}
	if _, err := admin.Exec(ctx, `UPDATE telemetry_runtime.telemetry_publication_outbox SET delivery_state = 'PUBLISHED', published_at = $1 WHERE delivery_state = 'PENDING' AND device_id::text NOT LIKE '018f2e00-9700-%'`, base); err != nil {
		t.Fatal(err)
	}

	// At t=0, all three devices report an own reading and become ONLINE.
	send(deviceCopiedOnly, sweepPointID(1), "active_energy_total", "100", "kWh", "COUNTER", base)
	send(deviceSilent, sweepPointID(3), "active_energy_total", "500", "kWh", "COUNTER", base)
	send(deviceOwnActive, sweepPointID(4), "active_energy_total", "900", "kWh", "COUNTER", base)
	assertDevicePresence(deviceCopiedOnly, "APPLICABLE", "ONLINE", 1, 1, 1)
	assertDevicePresence(deviceSilent, "APPLICABLE", "ONLINE", 1, 1, 1)
	assertDevicePresence(deviceOwnActive, "APPLICABLE", "ONLINE", 1, 1, 1)
	if _, err := realtime.RelayOnce(ctx, 64); err != nil {
		t.Fatalf("initial relay: %v", err)
	}
	transport.Publications = nil

	// Over the next 3 minutes:
	// - deviceCopiedOnly receives ONLY copied temperatures (at +60s and +120s);
	// - deviceSilent receives nothing at all;
	// - deviceOwnActive keeps reporting its own energy readings (+60s, +120s, +180s).
	send(deviceCopiedOnly, sweepPointID(2), "chw_leaving_temperature", "7.1", "Cel", "TELEMETRY", base.Add(60*time.Second))
	send(deviceCopiedOnly, sweepPointID(2), "chw_leaving_temperature", "7.2", "Cel", "TELEMETRY", base.Add(120*time.Second))
	var copiedLatest string
	if err := admin.QueryRow(ctx, `SELECT value::text FROM telemetry_runtime.latest_accepted_telemetry WHERE device_id = $1::uuid AND telemetry_key = 'chw_leaving_temperature'`, deviceCopiedOnly).Scan(&copiedLatest); err != nil || copiedLatest != "7.2" {
		t.Fatalf("copied latest=%q err=%v", copiedLatest, err)
	}
	// At +120s (between 60s online_within and 180s offline_after), copied reading did not emit a signal, so state became UNKNOWN.
	assertDevicePresence(deviceCopiedOnly, "APPLICABLE", "UNKNOWN", 1, 3, 3)

	send(deviceOwnActive, sweepPointID(4), "active_energy_total", "901", "kWh", "COUNTER", base.Add(60*time.Second))
	send(deviceOwnActive, sweepPointID(4), "active_energy_total", "902", "kWh", "COUNTER", base.Add(120*time.Second))
	send(deviceOwnActive, sweepPointID(4), "active_energy_total", "903", "kWh", "COUNTER", base.Add(180*time.Second))
	assertDevicePresence(deviceOwnActive, "APPLICABLE", "ONLINE", 4, 4, 4)

	// Periodic sweep at +185s: offline_after (180s) has elapsed for both deviceCopiedOnly and deviceSilent.
	relayNow = base.Add(185 * time.Second)
	swept, err := store.SweepDuePresence(ctx, relayNow, 64)
	if err != nil {
		t.Fatal(err)
	}
	if swept != 2 {
		t.Fatalf("swept=%d, want 2", swept)
	}
	assertDevicePresence(deviceCopiedOnly, "APPLICABLE", "OFFLINE", 1, 4, 4)
	assertDevicePresence(deviceSilent, "APPLICABLE", "OFFLINE", 1, 2, 2)
	assertDevicePresence(deviceOwnActive, "APPLICABLE", "ONLINE", 4, 4, 4)

	if _, err := realtime.RelayOnce(ctx, 64); err != nil {
		t.Fatalf("sweep relay: %v", err)
	}
	if len(transport.Publications) != 1 {
		t.Fatalf("sweep transport publications=%d, want 1", len(transport.Publications))
	}
	sweepDelta := transport.Publications[0].Publication
	if sweepDelta.Presence.CurrentState == nil || *sweepDelta.Presence.CurrentState != telemetryapi.DevicePresenceStateOffline {
		t.Fatalf("sweep delta presence=%#v, want OFFLINE", sweepDelta.Presence)
	}
	if len(sweepDelta.TelemetryChanges) != 0 {
		t.Fatalf("presence-only sweep delta emitted telemetryChanges=%#v, want empty", sweepDelta.TelemetryChanges)
	}

	// A sweep running with an older evaluatedAt than a committed snapshot never regresses that snapshot backwards.
	staleSwept, err := store.SweepDuePresence(ctx, base.Add(100*time.Second), 64)
	if err != nil || staleSwept != 0 {
		t.Fatalf("staleSwept=%d err=%v, want 0", staleSwept, err)
	}
	assertDevicePresence(deviceSilent, "APPLICABLE", "OFFLINE", 1, 2, 2)
	assertDevicePresence(deviceOwnActive, "APPLICABLE", "ONLINE", 4, 4, 4)

	// Another copied reading after offline_after updates the copied point value, but keeps the device OFFLINE.
	send(deviceCopiedOnly, sweepPointID(2), "chw_leaving_temperature", "7.3", "Cel", "TELEMETRY", base.Add(200*time.Second))
	assertDevicePresence(deviceCopiedOnly, "APPLICABLE", "OFFLINE", 1, 5, 5)

	// Subsequent sweep at +210s: nothing changed, so 0 devices are swept and revisions/outbox stay untouched.
	sweptAgain, err := store.SweepDuePresence(ctx, base.Add(210*time.Second), 64)
	if err != nil {
		t.Fatal(err)
	}
	if sweptAgain != 0 {
		t.Fatalf("sweptAgain=%d, want 0", sweptAgain)
	}
	assertDevicePresence(deviceSilent, "APPLICABLE", "OFFLINE", 1, 2, 2)
	assertDevicePresence(deviceOwnActive, "APPLICABLE", "ONLINE", 4, 4, 4)

	// When deviceCopiedOnly finally sends its own reading again, it returns ONLINE.
	send(deviceCopiedOnly, sweepPointID(1), "active_energy_total", "101", "kWh", "COUNTER", base.Add(220*time.Second))
	assertDevicePresence(deviceCopiedOnly, "APPLICABLE", "ONLINE", 2, 6, 6)

	// A device reclassified to NOT_APPLICABLE by onboarding is reconciled once on the next sweep.
	if _, err := admin.Exec(ctx, `UPDATE telemetry_runtime.devices SET presence_applicability = 'NOT_APPLICABLE', updated_at = $2 WHERE device_id = $1::uuid`, deviceSilent, base.Add(225*time.Second)); err != nil {
		t.Fatal(err)
	}
	reconciled, err := store.SweepDuePresence(ctx, base.Add(225*time.Second), 64)
	if err != nil || reconciled != 1 {
		t.Fatalf("reconciled=%d err=%v, want 1", reconciled, err)
	}
	assertDevicePresence(deviceSilent, "NOT_APPLICABLE", "", 1, 3, 3)

	reconciledAgain, err := store.SweepDuePresence(ctx, base.Add(230*time.Second), 64)
	if err != nil || reconciledAgain != 0 {
		t.Fatalf("reconciledAgain=%d err=%v, want 0", reconciledAgain, err)
	}
}
