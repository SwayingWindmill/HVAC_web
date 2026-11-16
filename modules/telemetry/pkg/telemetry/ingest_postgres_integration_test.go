package telemetry

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
)

const (
	ingestPartitionA = "tb-ticket-04-a"
	ingestPartitionB = "tb-ticket-04-b"
	sourceB          = "EG8200-COMMERCIAL-002"
)

func TestPostgresHistoricalReplayPreservesCurrentTruth(t *testing.T) {
	runtimeURL, adminURL := postgresTestURLs(t)
	ctx := t.Context()
	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	resetIngestState(t, admin)

	store, err := OpenPostgresStore(ctx, runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	baselineAt := time.Date(2026, 7, 24, 0, 0, 5, 0, time.UTC)
	if _, err := store.EvaluateAndRead(ctx, telemetryauth.Target{DeviceID: deviceA}, baselineAt); err != nil {
		t.Fatal(err)
	}
	liveAt := time.Date(2026, 7, 24, 0, 1, 2, 0, time.UTC)
	live := ingestCandidate(
		ingestEvent(201), sourceA, ingestPartitionA, 1, SourcePathWebhook,
		"mqtt-device-tenant-a-site-1", "zone.temperature", json.RawMessage(`24.0`), "NUMBER", "Cel",
		liveAt.Add(-2*time.Second), liveAt,
	)
	liveReceipt, err := store.AcceptObservation(ctx, live)
	if err != nil || liveReceipt.Status != ObservationAccepted || liveReceipt.BusinessRevision != 2 {
		t.Fatalf("live receipt=%#v err=%v", liveReceipt, err)
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

	replay := ingestCandidate(
		ingestEvent(202), sourceA, "tb-ticket-04-replay", 1, SourcePathHistoryReplay,
		"mqtt-device-tenant-a-site-1", "zone.temperature", json.RawMessage(`21.5`), "NUMBER", "Cel",
		time.Date(2026, 7, 23, 0, 10, 0, 0, time.UTC), liveAt.Add(time.Minute),
	)
	replayReceipt, err := store.AcceptHistoricalObservation(ctx, replay)
	if err != nil {
		t.Fatal(err)
	}
	if replayReceipt.Status != ObservationAccepted || replayReceipt.Quality != QualityGood || replayReceipt.BusinessRevision != 0 || replayReceipt.StateChanged || !replayReceipt.PositionAdvanced {
		t.Fatalf("replay receipt=%#v", replayReceipt)
	}
	assertObservationRow(t, admin, replay.Position.EventID, "ACCEPTED", "GOOD", "HISTORY_REPLAY", true)
	assertHistoryOutbox(t, admin, replay.Position.EventID, "ACCEPTED", tenantA, siteA, deviceA, true)

	duplicate, err := store.AcceptHistoricalObservation(ctx, replay)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate.Status != ObservationDuplicate || duplicate.ObservationID != replayReceipt.ObservationID || duplicate.BusinessRevision != 0 || duplicate.StateChanged {
		t.Fatalf("replay duplicate=%#v", duplicate)
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

// Two Gateways ingest for different Devices at once while commands read reported state:
// work on one Device must not fail because of work on another.
func TestPostgresConcurrentDevicesDoNotConflict(t *testing.T) {
	runtimeURL, adminURL := postgresTestURLs(t)
	ctx := t.Context()
	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	resetIngestState(t, admin)
	store, err := OpenPostgresStore(ctx, runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	const observations = 60
	start := time.Date(2026, 7, 24, 1, 0, 0, 0, time.UTC)
	ingest := func(eventBase int, sourceID, partition, externalID string) error {
		for index := range observations {
			sampledAt := start.Add(time.Duration(index) * time.Second)
			candidate := ingestCandidate(
				ingestEvent(eventBase+index), sourceID, partition, int64(index+1), SourcePathPush,
				externalID, "zone.temperature", json.RawMessage(fmt.Sprintf("%d.5", 20+index%5)), "NUMBER", "Cel",
				sampledAt, sampledAt.Add(time.Second),
			)
			if _, err := store.AcceptObservation(ctx, candidate); err != nil {
				return fmt.Errorf("%s observation %d: %w", externalID, index, err)
			}
		}
		return nil
	}
	errs := make(chan error, 3)
	done := make(chan struct{})
	var writers sync.WaitGroup
	writers.Add(2)
	go func() {
		defer writers.Done()
		errs <- ingest(300, sourceA, "tb-ticket-04-concurrent-a", "tb-device-org-a-site-1")
	}()
	go func() {
		defer writers.Done()
		errs <- ingest(400, sourceB, "tb-ticket-04-concurrent-b", "tb-device-org-b-site-1")
	}()
	go func() {
		reads := 0
		for {
			select {
			case <-done:
				errs <- nil
				return
			default:
			}
			device := []string{deviceA, deviceB}[reads%2]
			if _, err := store.EvaluateAndRead(ctx, telemetryauth.Target{DeviceID: device}, start.Add(2*time.Minute)); err != nil {
				errs <- fmt.Errorf("reported state read %d: %w", reads, err)
				return
			}
			reads++
		}
	}()
	writers.Wait()
	close(done)
	for range 3 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
}

func TestPostgresIngestEndToEnd(t *testing.T) {
	runtimeURL, adminURL := postgresTestURLs(t)
	ctx := t.Context()
	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	resetIngestState(t, admin)

	store, err := OpenPostgresStore(ctx, runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	baselineAt := time.Date(2026, 7, 23, 0, 0, 5, 0, time.UTC)
	baseline, err := store.EvaluateAndRead(ctx, telemetryauth.Target{DeviceID: deviceA}, baselineAt)
	if err != nil {
		t.Fatal(err)
	}
	if baseline.Snapshot.BusinessRevision != 1 || !baseline.StateChanged {
		t.Fatalf("baseline=%#v", baseline)
	}
	assertIngestRevision(t, admin, deviceA, 1, 1)

	acceptedAt := time.Date(2026, 7, 24, 0, 1, 2, 0, time.UTC)
	accepted := ingestCandidate(
		ingestEvent(101), sourceA, ingestPartitionA, 1, SourcePathWebhook,
		"tb-device-org-a-site-1", "zone.temperature", json.RawMessage(`24.0`), "NUMBER", "Cel",
		acceptedAt.Add(-2*time.Second), acceptedAt,
	)
	receipt := acceptObservationViaHTTP(t, store, accepted)
	if receipt.Status != ObservationAccepted || receipt.Quality != QualityGood || receipt.BusinessRevision != 2 || !receipt.StateChanged || !receipt.PositionAdvanced || receipt.DeviceID != deviceA {
		t.Fatalf("accepted receipt=%#v", receipt)
	}
	assertObservationRow(t, admin, accepted.Position.EventID, "ACCEPTED", "GOOD", "WEBHOOK", true)
	assertHistoryOutbox(t, admin, accepted.Position.EventID, "ACCEPTED", tenantA, siteA, deviceA, true)
	assertIngestRevision(t, admin, deviceA, 2, 2)
	var latestValue string
	var latestQuality string
	var latestRevision int64
	if err := admin.QueryRow(ctx, `
SELECT value::text, quality, business_revision
FROM telemetry_runtime.latest_accepted_telemetry
WHERE device_id = $1::uuid AND telemetry_key = 'zone.temperature'
`, deviceA).Scan(&latestValue, &latestQuality, &latestRevision); err != nil {
		t.Fatal(err)
	}
	if latestValue != "24.0" || latestQuality != "GOOD" || latestRevision != 2 {
		t.Fatalf("latest=%s/%s/%d", latestValue, latestQuality, latestRevision)
	}

	duplicate, err := store.AcceptObservation(ctx, accepted)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate.Status != ObservationDuplicate || duplicate.EvidenceID == "" || duplicate.ObservationID != receipt.ObservationID || duplicate.PositionAdvanced || duplicate.BusinessRevision != 0 || duplicate.StateChanged {
		t.Fatalf("duplicate=%#v", duplicate)
	}
	assertDeliveryEvidence(t, admin, duplicate.EvidenceID, "DUPLICATE", "DUPLICATE")
	duplicateAgain, err := store.AcceptObservation(ctx, accepted)
	if err != nil {
		t.Fatal(err)
	}
	if duplicateAgain.EvidenceID != duplicate.EvidenceID {
		t.Fatalf("duplicate evidence was not idempotent: first=%s second=%s", duplicate.EvidenceID, duplicateAgain.EvidenceID)
	}
	assertIngestRevision(t, admin, deviceA, 2, 2)

	replay := accepted
	replay.Position.EventID = ingestEvent(102)
	replay.ReceivedAt = acceptedAt.Add(time.Second)
	replayed, err := store.AcceptObservation(ctx, replay)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Status != ObservationDuplicate || len(replayed.QualityReasons) != 1 || replayed.QualityReasons[0] != QualityReasonReplayed || replayed.EvidenceID == "" || replayed.BusinessRevision != 0 || replayed.StateChanged {
		t.Fatalf("replayed=%#v", replayed)
	}
	assertDeliveryEvidence(t, admin, replayed.EvidenceID, "DUPLICATE", "REPLAYED")

	lower := accepted
	lower.Position.EventID = ingestEvent(103)
	lower.Position.Offset = 0
	lower.ReceivedAt = acceptedAt.Add(2 * time.Second)
	outOfOrder, err := store.AcceptObservation(ctx, lower)
	if err != nil {
		t.Fatal(err)
	}
	if outOfOrder.Status != ObservationOutOfOrder || outOfOrder.EvidenceID == "" || outOfOrder.PositionAdvanced {
		t.Fatalf("lower offset=%#v", outOfOrder)
	}
	assertDeliveryEvidence(t, admin, outOfOrder.EvidenceID, "OUT_OF_ORDER", "OUT_OF_ORDER")
	assertIngestRevision(t, admin, deviceA, 2, 2)

	lateSample := ingestCandidate(
		ingestEvent(199), sourceA, ingestPartitionB, 1, SourcePathPush,
		"tb-device-org-a-site-1", "zone.temperature", json.RawMessage(`23.5`), "NUMBER", "Cel",
		accepted.SampledAt.Add(-time.Second), acceptedAt.Add(3*time.Second),
	)
	lateReceipt, err := store.AcceptObservation(ctx, lateSample)
	if err != nil {
		t.Fatal(err)
	}
	if lateReceipt.Status != ObservationOutOfOrder || !lateReceipt.PositionAdvanced || lateReceipt.BusinessRevision != 0 || lateReceipt.StateChanged {
		t.Fatalf("late sampled observation=%#v", lateReceipt)
	}
	assertObservationRow(t, admin, lateSample.Position.EventID, "OUT_OF_ORDER", "GOOD", "PUSH", true)
	assertHistoryOutbox(t, admin, lateSample.Position.EventID, "OUT_OF_ORDER", tenantA, siteA, deviceA, true)
	assertIngestRevision(t, admin, deviceA, 2, 2)
	if err := admin.QueryRow(ctx, `
SELECT value::text, quality, business_revision
FROM telemetry_runtime.latest_accepted_telemetry
WHERE device_id = $1::uuid AND telemetry_key = 'zone.temperature'
`, deviceA).Scan(&latestValue, &latestQuality, &latestRevision); err != nil {
		t.Fatal(err)
	}
	if latestValue != "24.0" || latestQuality != "GOOD" || latestRevision != 2 {
		t.Fatalf("late sampled observation regressed Current: %s/%s/%d", latestValue, latestQuality, latestRevision)
	}

	rejectedAt := acceptedAt.Add(10 * time.Second)
	rejected := ingestCandidate(
		ingestEvent(104), sourceA, ingestPartitionA, 2, SourcePathPush,
		"tb-device-org-a-site-1", "zone.humidity", json.RawMessage(`"invalid"`), "NUMBER", "%RH",
		rejectedAt.Add(-time.Second), rejectedAt,
	)
	rejectedReceipt, err := store.AcceptObservation(ctx, rejected)
	if err != nil {
		t.Fatal(err)
	}
	if rejectedReceipt.Status != ObservationRejected || rejectedReceipt.Quality != QualityInvalid || rejectedReceipt.BusinessRevision != 3 || !rejectedReceipt.StateChanged {
		t.Fatalf("rejected=%#v", rejectedReceipt)
	}
	assertObservationRow(t, admin, rejected.Position.EventID, "REJECTED", "INVALID", "PUSH", false)
	assertHistoryOutbox(t, admin, rejected.Position.EventID, "REJECTED", tenantA, siteA, deviceA, false)
	assertIngestRevision(t, admin, deviceA, 3, 3)
	var missingReason string
	if err := admin.QueryRow(ctx, `
SELECT value ->> 'missingReason'
FROM telemetry_runtime.device_observation_snapshots s,
     jsonb_array_elements(s.snapshot -> 'values') value
WHERE s.device_id = $1::uuid AND value ->> 'key' = 'zone.humidity'
`, deviceA).Scan(&missingReason); err != nil {
		t.Fatal(err)
	}
	if missingReason != "ONLY_REJECTED_CANDIDATES" {
		t.Fatalf("missingReason=%s", missingReason)
	}
	var rejectedRaw *string
	if err := admin.QueryRow(ctx, `SELECT value::text FROM telemetry_runtime.source_observations WHERE source_event_id = $1::uuid`, rejected.Position.EventID).Scan(&rejectedRaw); err != nil {
		t.Fatal(err)
	}
	if rejectedRaw != nil {
		t.Fatalf("rejected evidence retained raw telemetry: %s", *rejectedRaw)
	}

	rejectedAgain := rejected
	rejectedAgain.Position.EventID = ingestEvent(105)
	rejectedAgain.Position.Offset = 3
	rejectedAgain.ReceivedAt = rejectedAt.Add(time.Second)
	rejectedAgain.SampledAt = rejected.SampledAt.Add(time.Second)
	repeatRejected, err := store.AcceptObservation(ctx, rejectedAgain)
	if err != nil {
		t.Fatal(err)
	}
	if repeatRejected.Status != ObservationRejected || repeatRejected.BusinessRevision != 3 || repeatRejected.StateChanged {
		t.Fatalf("repeat rejected=%#v", repeatRejected)
	}
	assertIngestRevision(t, admin, deviceA, 3, 3)

	suspectReceivedAt := time.Date(2026, 7, 24, 0, 20, 0, 0, time.UTC)
	suspect := ingestCandidate(
		ingestEvent(106), sourceA, ingestPartitionA, 4, SourcePathReconciliation,
		"tb-device-org-a-site-1", "zone.temperature", json.RawMessage(`25.0`), "NUMBER", "Cel",
		time.Date(2026, 7, 24, 0, 2, 0, 0, time.UTC), suspectReceivedAt,
	)
	suspectReceipt, err := store.AcceptObservation(ctx, suspect)
	if err != nil {
		t.Fatal(err)
	}
	if suspectReceipt.Status != ObservationAccepted || suspectReceipt.Quality != QualityStale || len(suspectReceipt.QualityReasons) != 1 || suspectReceipt.QualityReasons[0] != QualityReasonSourceLagExceeded || suspectReceipt.BusinessRevision != 4 {
		t.Fatalf("suspect=%#v", suspectReceipt)
	}
	assertObservationRow(t, admin, suspect.Position.EventID, "ACCEPTED", "STALE", "RECONCILIATION", true)
	assertIngestRevision(t, admin, deviceA, 4, 4)

	recentAt := suspectReceivedAt.Add(time.Minute)
	recent := ingestCandidate(
		ingestEvent(107), sourceA, ingestPartitionA, 5, SourcePathPoll,
		"tb-device-org-a-site-1", "zone.temperature", json.RawMessage(`25.5`), "NUMBER", "Cel",
		recentAt.Add(-time.Second), recentAt,
	)
	recentReceipt, err := store.AcceptObservation(ctx, recent)
	if err != nil {
		t.Fatal(err)
	}
	if recentReceipt.BusinessRevision != 5 || !recentReceipt.StateChanged {
		t.Fatalf("recent=%#v", recentReceipt)
	}
	assertSnapshotAvailability(t, admin, deviceA, "AVAILABLE", "", "ONLINE")
	assertIngestRevision(t, admin, deviceA, 5, 5)

	missing := ingestCandidate(
		ingestEvent(108), sourceA, "tb-ticket-04-missing", 1, SourcePathWebhook,
		"tb-device-missing", "zone.temperature", json.RawMessage(`999.123`), "NUMBER", "Cel",
		recentAt, recentAt.Add(time.Second),
	)
	quarantined, err := store.AcceptObservation(ctx, missing)
	if err != nil {
		t.Fatal(err)
	}
	if quarantined.Status != ObservationQuarantined || quarantined.QuarantineReason != QuarantineMappingNotFound || quarantined.DeviceID != "" || !quarantined.PositionAdvanced {
		t.Fatalf("quarantined=%#v", quarantined)
	}
	var quarantineEvidence string
	var quarantinedValue *string
	if err := admin.QueryRow(ctx, `
SELECT q.evidence::text, o.value::text
FROM telemetry_runtime.ingest_quarantine q
JOIN telemetry_runtime.source_observations o
  ON o.source_id = q.source_id
 AND o.source_event_id = $1::uuid
WHERE q.external_id = 'tb-device-missing'
ORDER BY q.detected_at DESC LIMIT 1
`, missing.Position.EventID).Scan(&quarantineEvidence, &quarantinedValue); err != nil {
		t.Fatal(err)
	}
	if quarantinedValue != nil || strings.Contains(quarantineEvidence, "999.123") {
		t.Fatalf("quarantine leaked raw telemetry: value=%v evidence=%s", quarantinedValue, quarantineEvidence)
	}
	assertHistoryOutbox(t, admin, missing.Position.EventID, "QUARANTINED", "", "", "", false)
	assertIngestRevision(t, admin, deviceA, 5, 5)

	extremeFutureReceivedAt := recentAt.Add(1750 * time.Millisecond)
	extremeFuture := ingestCandidate(
		ingestEvent(111), sourceA, "tb-ticket-04-future-clock", 1, SourcePathPush,
		"tb-device-org-a-site-1", "zone.temperature", json.RawMessage(`26.5`), "NUMBER", "Cel",
		extremeFutureReceivedAt.Add(48*time.Hour), extremeFutureReceivedAt,
	)
	extremeFutureReceipt, err := store.AcceptObservation(ctx, extremeFuture)
	if err != nil {
		t.Fatal(err)
	}
	if extremeFutureReceipt.Status != ObservationRejected || extremeFutureReceipt.Quality != QualityInvalid ||
		len(extremeFutureReceipt.QualityReasons) != 1 || extremeFutureReceipt.QualityReasons[0] != QualityReasonClockAhead ||
		!extremeFutureReceipt.PositionAdvanced {
		t.Fatalf("extreme future-clock receipt=%#v", extremeFutureReceipt)
	}
	assertObservationRow(t, admin, extremeFuture.Position.EventID, "REJECTED", "INVALID", "PUSH", false)
	var extremeFutureSampledAt time.Time
	if err := admin.QueryRow(ctx, `SELECT sampled_at FROM telemetry_runtime.source_observations WHERE source_event_id = $1::uuid`, extremeFuture.Position.EventID).Scan(&extremeFutureSampledAt); err != nil {
		t.Fatal(err)
	}
	if !extremeFutureSampledAt.Equal(extremeFuture.SampledAt) {
		t.Fatalf("future-clock evidence sampledAt=%s expected=%s", extremeFutureSampledAt, extremeFuture.SampledAt)
	}
	assertIngestRevision(t, admin, deviceA, 5, 5)

	bAt := recentAt.Add(2 * time.Second)
	organizationB := ingestCandidate(
		ingestEvent(109), sourceB, ingestPartitionB, 1, SourcePathPush,
		"tb-device-org-b-site-1", "zone.temperature", json.RawMessage(`21.0`), "NUMBER", "Cel",
		bAt.Add(-time.Second), bAt,
	)
	organizationBReceipt, err := store.AcceptObservation(ctx, organizationB)
	if err != nil {
		t.Fatal(err)
	}
	if organizationBReceipt.Status != ObservationAccepted || organizationBReceipt.DeviceID != deviceB || organizationBReceipt.BusinessRevision != 1 {
		t.Fatalf("Organization B receipt=%#v", organizationBReceipt)
	}
	var organizationBSnapshot string
	if err := admin.QueryRow(ctx, `SELECT snapshot::text FROM telemetry_runtime.device_observation_snapshots WHERE device_id = $1::uuid`, deviceB).Scan(&organizationBSnapshot); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(organizationBSnapshot, tenantB) || strings.Contains(organizationBSnapshot, tenantA) || strings.Contains(organizationBSnapshot, deviceA) {
		t.Fatalf("Organization B isolation failed: %s", organizationBSnapshot)
	}

	var duplicateOutboxID string
	if err := admin.QueryRow(ctx, `
SELECT event_id::text
FROM telemetry_runtime.telemetry_publication_outbox
WHERE device_id = $1::uuid AND subscription_id IS NULL
ORDER BY business_revision LIMIT 1
`, deviceA).Scan(&duplicateOutboxID); err != nil {
		t.Fatal(err)
	}
	rollbackCandidate := recent
	rollbackCandidate.Position.EventID = ingestEvent(110)
	rollbackCandidate.Position.Offset = 6
	rollbackCandidate.Value = json.RawMessage(`26.0`)
	rollbackCandidate.SampledAt = recentAt.Add(2 * time.Second)
	rollbackCandidate.ReceivedAt = recentAt.Add(3 * time.Second)
	generatedIDs := []string{
		"018f2e00-8900-7000-8000-000000000001",
		"018f2e00-8900-7000-8000-000000000002",
		duplicateOutboxID,
	}
	generatedIndex := 0
	failingStore := NewPostgresStore(store.pool, func(time.Time) (string, error) {
		if generatedIndex >= len(generatedIDs) {
			return "", errors.New("unexpected UUID request")
		}
		value := generatedIDs[generatedIndex]
		generatedIndex++
		return value, nil
	})
	if _, err := failingStore.AcceptObservation(ctx, rollbackCandidate); err == nil {
		t.Fatal("outbox failure unexpectedly committed observation")
	}
	assertIngestRevision(t, admin, deviceA, 5, 5)
	var rollbackRows int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM telemetry_runtime.source_observations WHERE source_event_id = $1::uuid`, rollbackCandidate.Position.EventID).Scan(&rollbackRows); err != nil {
		t.Fatal(err)
	}
	if rollbackRows != 0 {
		t.Fatalf("rollback observation rows=%d", rollbackRows)
	}
	var rollbackHistoryRows int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM telemetry_runtime.telemetry_history_outbox WHERE payload ->> 'source_event_id' = $1`, rollbackCandidate.Position.EventID).Scan(&rollbackHistoryRows); err != nil {
		t.Fatal(err)
	}
	if rollbackHistoryRows != 0 {
		t.Fatalf("rollback history outbox rows=%d", rollbackHistoryRows)
	}
	var headOffset int64
	if err := admin.QueryRow(ctx, `
SELECT source_offset FROM telemetry_runtime.source_positions
WHERE source_id = $1 AND source_partition = $2
`, sourceA, ingestPartitionA).Scan(&headOffset); err != nil {
		t.Fatal(err)
	}
	if headOffset != 5 {
		t.Fatalf("rollback source position=%d", headOffset)
	}

	committedAfterRollback, err := store.AcceptObservation(ctx, rollbackCandidate)
	if err != nil {
		t.Fatal(err)
	}
	if committedAfterRollback.BusinessRevision != 6 || !committedAfterRollback.StateChanged {
		t.Fatalf("committed after rollback=%#v", committedAfterRollback)
	}
	assertIngestRevision(t, admin, deviceA, 6, 6)

	store.Close()
	reopened, err := OpenPostgresStore(ctx, runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err := admin.Exec(ctx, `
SELECT pg_terminate_backend(pid)
FROM pg_stat_activity
WHERE usename = 's2_telemetry_service' AND pid <> pg_backend_pid()
`); err != nil {
		t.Fatal(err)
	}
	afterRestart, err := reopened.AcceptObservation(ctx, rollbackCandidate)
	if err != nil {
		t.Fatal(err)
	}
	if afterRestart.Status != ObservationDuplicate || afterRestart.EvidenceID == "" {
		t.Fatalf("restart duplicate=%#v", afterRestart)
	}
	assertIngestRevision(t, admin, deviceA, 6, 6)

	if _, err := admin.Exec(ctx, `
UPDATE telemetry_runtime.telemetry_publication_outbox
SET attempts = attempts + 2, last_error_code = 'CENTRIFUGO_UNAVAILABLE', available_at = available_at + interval '5 seconds'
WHERE device_id = $1::uuid AND business_revision = 6 AND subscription_id IS NULL
`, deviceA); err != nil {
		t.Fatal(err)
	}
	assertIngestRevision(t, admin, deviceA, 6, 6)
}

func resetIngestState(t *testing.T, admin *pgxpool.Pool) {
	t.Helper()
	ctx := t.Context()
	statements := []string{
		`DELETE FROM telemetry_runtime.telemetry_history_outbox WHERE payload ->> 'source_partition' LIKE 'tb-ticket-04%'`,
		`DELETE FROM telemetry_runtime.source_delivery_evidence WHERE source_partition LIKE 'tb-ticket-04%'`,
		`DELETE FROM telemetry_runtime.ingest_quarantine WHERE detected_at >= '2026-07-24T00:00:00Z'`,
		`DELETE FROM telemetry_runtime.presence_signals WHERE created_at >= '2026-07-24T00:00:00Z'`,
		`DELETE FROM telemetry_runtime.source_observations WHERE source_partition LIKE 'tb-ticket-04%'`,
		`DELETE FROM telemetry_runtime.source_positions WHERE source_partition LIKE 'tb-ticket-04%'`,
		`DELETE FROM telemetry_runtime.telemetry_publication_outbox WHERE subscription_id IS NULL AND device_id IN ('` + deviceA + `'::uuid, '` + deviceB + `'::uuid)`,
		`DELETE FROM telemetry_runtime.device_observation_snapshots WHERE device_id IN ('` + deviceA + `'::uuid, '` + deviceB + `'::uuid)`,
		`DELETE FROM telemetry_runtime.device_presence WHERE device_id IN ('` + deviceA + `'::uuid, '` + deviceB + `'::uuid)`,
		`DELETE FROM telemetry_runtime.latest_accepted_telemetry WHERE device_id = '` + deviceA + `'::uuid AND telemetry_key = 'zone.humidity'`,
		`DELETE FROM telemetry_runtime.latest_accepted_telemetry WHERE device_id = '` + deviceB + `'::uuid`,
		`UPDATE telemetry_runtime.latest_accepted_telemetry SET value = '23.5'::jsonb, value_type = 'NUMBER', unit = 'Cel', sampled_at = '2026-07-23T00:00:00Z', received_at = '2026-07-23T00:00:02Z', freshness = 'FRESH', quality = 'GOOD', quality_reasons = '{}', business_revision = 1, policy_revision = 5, updated_at = '2026-07-23T00:00:05Z' WHERE device_id = '` + deviceA + `'::uuid AND telemetry_key = 'zone.temperature'`,
		`UPDATE telemetry_runtime.observation_coverage SET available = true, continuous_since = '2026-07-22T23:00:00Z', reason_code = NULL, source_revision = 1, updated_at = '2026-07-23T00:00:05Z' WHERE device_id = '` + deviceA + `'::uuid`,
		`UPDATE telemetry_runtime.observation_coverage SET available = false, continuous_since = NULL, reason_code = 'OBSERVATION_COVERAGE_GAP', source_revision = 1, updated_at = '2026-07-23T00:00:05Z' WHERE device_id = '` + deviceB + `'::uuid`,
	}
	for _, statement := range statements {
		if _, err := admin.Exec(ctx, statement); err != nil {
			t.Fatalf("reset ingest state: %v\n%s", err, statement)
		}
	}
}

// ingestCandidate builds a source observation carrying the identity Connectivity would
// resolve for the fixture Device; history replay names the Device ID instead.
func ingestCandidate(eventID, sourceID, partition string, offset int64, sourcePath SourcePath, externalID, key string, value json.RawMessage, valueType, unit string, sampledAt, receivedAt time.Time) ObservationCandidate {
	candidate := ObservationCandidate{
		SourceID: sourceID, SourcePath: sourcePath,
		ExternalEntityType: "DEVICE", ExternalID: externalID, TelemetryKey: key,
		Value: append(json.RawMessage(nil), value...), ValueType: valueType, Unit: &unit,
		SampledAt: sampledAt, ReceivedAt: receivedAt,
		Position: SourcePosition{Partition: partition, Offset: offset, EventID: eventID},
	}
	device, known := fixtureDevices[externalID]
	if !known {
		return candidate
	}
	if sourcePath == SourcePathHistoryReplay {
		candidate.SourceID, candidate.ExternalID = historicalReplaySourceID, device.DeviceID
		return candidate
	}
	candidate.Device = &device
	if pointID, registered := fixturePoints[device.DeviceID+"/"+key]; registered {
		candidate.Point = &ResolvedPoint{PointID: pointID, PointType: "TELEMETRY", ValueType: valueType, Unit: &unit, PointRevision: 1}
	}
	return candidate
}

var fixtureDevices = map[string]ResolvedDevice{
	"tb-device-org-a-site-1":      {TenantID: tenantA, SiteID: siteA, DeviceID: deviceA},
	"mqtt-device-tenant-a-site-1": {TenantID: tenantA, SiteID: siteA, DeviceID: deviceA},
	"tb-device-org-b-site-1":      {TenantID: tenantB, SiteID: "018f2e00-1000-7000-8000-000000000003", DeviceID: deviceB},
}

var fixturePoints = map[string]string{
	deviceA + "/zone.temperature": "018f2e00-3100-7000-8000-000000000001",
	deviceA + "/zone.humidity":    "018f2e00-3100-7000-8000-000000000002",
	deviceB + "/zone.temperature": "018f2e00-3100-7000-8000-000000000004",
}

func ingestEvent(suffix int) string {
	return fmt.Sprintf("018f2e00-8800-7000-8000-000000000%03d", suffix)
}

func assertObservationRow(t *testing.T, admin *pgxpool.Pool, eventID, status, quality, sourcePath string, valuePresent bool) {
	t.Helper()
	var actualStatus, actualQuality, actualSourcePath string
	var value *string
	if err := admin.QueryRow(t.Context(), `
SELECT acceptance_status, quality, source_path, value::text
FROM telemetry_runtime.source_observations
WHERE source_event_id = $1::uuid
`, eventID).Scan(&actualStatus, &actualQuality, &actualSourcePath, &value); err != nil {
		t.Fatal(err)
	}
	if actualStatus != status || actualQuality != quality || actualSourcePath != sourcePath || (value != nil) != valuePresent {
		t.Fatalf("observation=%s/%s/%s value=%v", actualStatus, actualQuality, actualSourcePath, value)
	}
}

func assertHistoryOutbox(t *testing.T, admin *pgxpool.Pool, sourceEventID, status, tenantID, siteID, deviceID string, valuePresent bool) {
	t.Helper()
	var deliveryState, actualStatus string
	var actualTenantID, actualSiteID, actualDeviceID, value *string
	if err := admin.QueryRow(t.Context(), `
SELECT delivery_state,
       payload ->> 'acceptance_status',
       payload ->> 'tenant_id',
       payload ->> 'site_id',
       payload ->> 'device_id',
       COALESCE(payload ->> 'value_json', payload ->> 'value_number', payload ->> 'value_string', payload ->> 'value_boolean')
FROM telemetry_runtime.telemetry_history_outbox
WHERE payload ->> 'source_event_id' = $1
`, sourceEventID).Scan(&deliveryState, &actualStatus, &actualTenantID, &actualSiteID, &actualDeviceID, &value); err != nil {
		t.Fatal(err)
	}
	optionalMatches := func(actual *string, expected string) bool {
		if expected == "" {
			return actual == nil
		}
		return actual != nil && *actual == expected
	}
	if deliveryState != "PENDING" || actualStatus != status ||
		!optionalMatches(actualTenantID, tenantID) || !optionalMatches(actualSiteID, siteID) || !optionalMatches(actualDeviceID, deviceID) ||
		(value != nil) != valuePresent {
		t.Fatalf("history outbox state=%s status=%s tenant=%v site=%v device=%v value=%v", deliveryState, actualStatus, actualTenantID, actualSiteID, actualDeviceID, value)
	}
}

func assertDeliveryEvidence(t *testing.T, admin *pgxpool.Pool, evidenceID, status, reason string) {
	t.Helper()
	var actualStatus, actualReason string
	if err := admin.QueryRow(t.Context(), `
SELECT delivery_status, quality_reason
FROM telemetry_runtime.source_delivery_evidence
WHERE evidence_id = $1::uuid
`, evidenceID).Scan(&actualStatus, &actualReason); err != nil {
		t.Fatal(err)
	}
	if actualStatus != status || actualReason != reason {
		t.Fatalf("delivery evidence=%s/%s", actualStatus, actualReason)
	}
}

func assertIngestRevision(t *testing.T, admin *pgxpool.Pool, deviceID string, revision, outboxCount int64) {
	t.Helper()
	var snapshotRevision, presenceRevision, maximumLatestRevision, actualOutboxCount int64
	if err := admin.QueryRow(t.Context(), `
SELECT s.business_revision, p.business_revision,
       COALESCE((SELECT max(business_revision) FROM telemetry_runtime.latest_accepted_telemetry WHERE device_id = s.device_id), 0),
       (SELECT count(*) FROM telemetry_runtime.telemetry_publication_outbox WHERE device_id = s.device_id AND subscription_id IS NULL)
FROM telemetry_runtime.device_observation_snapshots s
JOIN telemetry_runtime.device_presence p USING (device_id)
WHERE s.device_id = $1::uuid
`, deviceID).Scan(&snapshotRevision, &presenceRevision, &maximumLatestRevision, &actualOutboxCount); err != nil {
		t.Fatal(err)
	}
	if snapshotRevision != revision || presenceRevision != revision || maximumLatestRevision != revision || actualOutboxCount != outboxCount {
		t.Fatalf("revision state snapshot=%d presence=%d latest=%d outbox=%d", snapshotRevision, presenceRevision, maximumLatestRevision, actualOutboxCount)
	}
}

func assertSnapshotAvailability(t *testing.T, admin *pgxpool.Pool, deviceID, availability, reason, presence string) {
	t.Helper()
	var actualAvailability string
	var reasons []string
	var currentPresence *string
	if err := admin.QueryRow(t.Context(), `
SELECT evaluation_availability, availability_reasons, snapshot #>> '{presence,currentState}'
FROM telemetry_runtime.device_observation_snapshots
WHERE device_id = $1::uuid
`, deviceID).Scan(&actualAvailability, &reasons, &currentPresence); err != nil {
		t.Fatal(err)
	}
	if actualAvailability != availability {
		t.Fatalf("availability=%s", actualAvailability)
	}
	if reason == "" {
		if len(reasons) != 0 {
			t.Fatalf("reasons=%#v", reasons)
		}
	} else if len(reasons) != 1 || reasons[0] != reason {
		t.Fatalf("reasons=%#v", reasons)
	}
	if presence == "" {
		if currentPresence != nil && *currentPresence != "" {
			t.Fatalf("presence=%v", currentPresence)
		}
	} else if currentPresence == nil || *currentPresence != presence {
		t.Fatalf("presence=%v expected=%s", currentPresence, presence)
	}
}

func batchDeviceID(suffix int) string {
	return fmt.Sprintf("018f2e00-9800-7000-8000-0000000000%02d", suffix)
}

func batchPointID(suffix int) string {
	return fmt.Sprintf("018f2e00-9810-7000-8000-0000000000%02d", suffix)
}

func batchEventID(suffix int) string {
	return fmt.Sprintf("018f2e00-9820-7000-8000-000000000%03d", suffix)
}

func cleanupBatchIntegrationState(t *testing.T, admin *pgxpool.Pool) {
	t.Helper()
	ctx := t.Context()
	statements := []string{
		`DELETE FROM telemetry_runtime.probe_offset_estimates WHERE pair_code LIKE 'chw-batch-deadlock-%'`,
		`DELETE FROM telemetry_runtime.probe_pair_windows WHERE pair_code LIKE 'chw-batch-deadlock-%'`,
		`DELETE FROM telemetry_runtime.probe_pairs WHERE pair_code LIKE 'chw-batch-deadlock-%'`,
		`DELETE FROM telemetry_runtime.telemetry_history_outbox WHERE payload ->> 'source_partition' LIKE 'tb-batch-test-%'`,
		`DELETE FROM telemetry_runtime.source_delivery_evidence WHERE source_partition LIKE 'tb-batch-test-%'`,
		`DELETE FROM telemetry_runtime.ingest_quarantine WHERE evidence ->> 'sourcePartition' LIKE 'tb-batch-test-%' OR device_id::text LIKE '018f2e00-9800-%'`,
		`DELETE FROM telemetry_runtime.source_observations WHERE source_partition LIKE 'tb-batch-test-%'`,
		`DELETE FROM telemetry_runtime.source_positions WHERE source_partition LIKE 'tb-batch-test-%'`,
		`DELETE FROM telemetry_runtime.telemetry_publication_outbox WHERE device_id::text LIKE '018f2e00-9800-%'`,
		`DELETE FROM telemetry_runtime.device_observation_snapshots WHERE device_id::text LIKE '018f2e00-9800-%'`,
		`DELETE FROM telemetry_runtime.device_presence WHERE device_id::text LIKE '018f2e00-9800-%'`,
		`DELETE FROM telemetry_runtime.latest_accepted_telemetry WHERE device_id::text LIKE '018f2e00-9800-%'`,
		`DELETE FROM telemetry_runtime.presence_signals WHERE device_id::text LIKE '018f2e00-9800-%'`,
		`DELETE FROM telemetry_runtime.observation_coverage WHERE device_id::text LIKE '018f2e00-9800-%'`,
		`DELETE FROM telemetry_runtime.freshness_policies WHERE device_id::text LIKE '018f2e00-9800-%'`,
		`DELETE FROM telemetry_runtime.presence_policies WHERE device_id::text LIKE '018f2e00-9800-%'`,
		`DELETE FROM telemetry_runtime.points WHERE device_id::text LIKE '018f2e00-9800-%'`,
		`DELETE FROM telemetry_runtime.devices WHERE device_id::text LIKE '018f2e00-9800-%'`,
	}
	for _, statement := range statements {
		if _, err := admin.Exec(ctx, statement); err != nil {
			t.Fatalf("cleanup batch integration state: %v\n%s", err, statement)
		}
	}
}

func seedBatchIntegrationDevice(t *testing.T, admin *pgxpool.Pool, deviceID string, base time.Time) {
	t.Helper()
	ctx := t.Context()
	if _, err := admin.Exec(ctx, `
INSERT INTO telemetry_runtime.devices (device_id, tenant_id, site_id, presence_applicability, updated_at)
VALUES ($1::uuid, $2::uuid, $3::uuid, 'APPLICABLE', $4)`, deviceID, tenantA, siteA, base); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `
INSERT INTO telemetry_runtime.presence_policies (
  device_id, policy_revision, online_within_seconds, offline_after_seconds,
  coverage_required, accepted_signal_types, max_future_clock_skew_seconds, max_source_lag_seconds, updated_at
) VALUES ($1::uuid, 1, 60, 180, true, ARRAY['SOURCE_ACTIVITY']::text[], 30, 600, $2)`, deviceID, base); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `
INSERT INTO telemetry_runtime.observation_coverage (device_id, available, continuous_since, reason_code, source_revision, updated_at)
VALUES ($1::uuid, true, $2, NULL, 1, $3)`, deviceID, base.Add(-time.Hour), base); err != nil {
		t.Fatal(err)
	}
}

func seedBatchFreshnessPolicy(t *testing.T, admin *pgxpool.Pool, deviceID, key, valueType string, unit *string, minVal, maxVal *float64, checks []byte, base time.Time) {
	t.Helper()
	if _, err := admin.Exec(t.Context(), `
INSERT INTO telemetry_runtime.freshness_policies (
  device_id, telemetry_key, policy_revision, fresh_within_seconds, configured,
  expected_sample_interval_seconds, value_type, expected_unit, minimum_number, maximum_number, quality_checks, updated_at
) VALUES ($1::uuid, $2, 1, 600, true, 60, $3, $4, $5, $6, $7::jsonb, $8)`, deviceID, key, valueType, unit, minVal, maxVal, checks, base); err != nil {
		t.Fatal(err)
	}
}

func batchCandidate(eventSuffix int, partition string, offset int64, deviceID, pointID, key string, value json.RawMessage, valueType string, unit *string, pointType string, sampledAt, receivedAt time.Time) ObservationCandidate {
	return ObservationCandidate{
		SourceID: sourceA, SourcePath: SourcePathPush,
		ExternalEntityType: "DEVICE", ExternalID: deviceID,
		Device:       &ResolvedDevice{TenantID: tenantA, SiteID: siteA, DeviceID: deviceID},
		Point:        &ResolvedPoint{PointID: pointID, PointType: pointType, ValueType: valueType, Unit: unit, PointRevision: 1},
		TelemetryKey: key, Value: value, ValueType: valueType, Unit: unit,
		SampledAt: sampledAt, ReceivedAt: receivedAt,
		Position: SourcePosition{Partition: partition, Offset: offset, EventID: batchEventID(eventSuffix)},
	}
}

func TestPostgresBatchMultiPointSingleDeviceAdvancesRevisionOnce(t *testing.T) {
	runtimeURL, adminURL := postgresTestURLs(t)
	ctx := t.Context()
	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	cleanupBatchIntegrationState(t, admin)
	defer cleanupBatchIntegrationState(t, admin)

	base := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	dev1 := batchDeviceID(1)
	seedBatchIntegrationDevice(t, admin, dev1, base)
	cel, rh, pa := "Cel", "%RH", "Pa"
	seedBatchFreshnessPolicy(t, admin, dev1, "zone.temperature", "NUMBER", &cel, nil, nil, nil, base)
	seedBatchFreshnessPolicy(t, admin, dev1, "zone.humidity", "NUMBER", &rh, nil, nil, nil, base)
	seedBatchFreshnessPolicy(t, admin, dev1, "duct.pressure", "NUMBER", &pa, nil, nil, nil, base)

	store, err := OpenPostgresStore(ctx, runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	at := base.Add(10 * time.Second)
	const partition = "tb-batch-test-multipoint"
	receipts := acceptObservationBatchViaHTTP(t, store, []ObservationCandidate{
		batchCandidate(1, partition, 1, dev1, batchPointID(1), "zone.temperature", json.RawMessage(`23.5`), "NUMBER", &cel, "TELEMETRY", at, at),
		batchCandidate(2, partition, 2, dev1, batchPointID(2), "zone.humidity", json.RawMessage(`52.0`), "NUMBER", &rh, "TELEMETRY", at, at),
		batchCandidate(3, partition, 3, dev1, batchPointID(3), "duct.pressure", json.RawMessage(`210.0`), "NUMBER", &pa, "TELEMETRY", at, at),
	})
	if len(receipts) != 3 {
		t.Fatalf("receipts=%#v", receipts)
	}
	for index, receipt := range receipts {
		if receipt.Status != ObservationAccepted || receipt.BusinessRevision != 1 || !receipt.StateChanged || !receipt.PositionAdvanced {
			t.Fatalf("receipt[%d]=%#v", index, receipt)
		}
	}
	assertIngestRevision(t, admin, dev1, 1, 1)
	var latestCount, obsCount, outboxCount int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM telemetry_runtime.latest_accepted_telemetry WHERE device_id = $1::uuid`, dev1).Scan(&latestCount); err != nil || latestCount != 3 {
		t.Fatalf("latestCount=%d err=%v", latestCount, err)
	}
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM telemetry_runtime.source_observations WHERE source_partition = $1`, partition).Scan(&obsCount); err != nil || obsCount != 3 {
		t.Fatalf("obsCount=%d err=%v", obsCount, err)
	}
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM telemetry_runtime.telemetry_history_outbox WHERE payload ->> 'source_partition' = $1`, partition).Scan(&outboxCount); err != nil || outboxCount != 3 {
		t.Fatalf("outboxCount=%d err=%v", outboxCount, err)
	}
}

func TestPostgresBatchInBatchDuplicateReplayedAndOutOfOrder(t *testing.T) {
	runtimeURL, adminURL := postgresTestURLs(t)
	ctx := t.Context()
	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	cleanupBatchIntegrationState(t, admin)
	defer cleanupBatchIntegrationState(t, admin)

	base := time.Date(2026, 7, 15, 1, 0, 0, 0, time.UTC)
	dev1 := batchDeviceID(2)
	seedBatchIntegrationDevice(t, admin, dev1, base)
	cel := "Cel"
	seedBatchFreshnessPolicy(t, admin, dev1, "zone.temperature", "NUMBER", &cel, nil, nil, nil, base)

	store, err := OpenPostgresStore(ctx, runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	const partition = "tb-batch-test-dedup"
	c0 := batchCandidate(11, partition, 10, dev1, batchPointID(11), "zone.temperature", json.RawMessage(`22.0`), "NUMBER", &cel, "TELEMETRY", base.Add(10*time.Second), base.Add(15*time.Second))
	c1 := batchCandidate(12, partition, 11, dev1, batchPointID(11), "zone.temperature", json.RawMessage(`23.0`), "NUMBER", &cel, "TELEMETRY", base.Add(12*time.Second), base.Add(15*time.Second))
	c2DupEvent := c0
	c2DupEvent.Position.Offset = 12
	c3ReplayedOffset := batchCandidate(13, partition, 11, dev1, batchPointID(11), "zone.temperature", json.RawMessage(`23.5`), "NUMBER", &cel, "TELEMETRY", base.Add(13*time.Second), base.Add(15*time.Second))
	c4LowerOffset := batchCandidate(14, partition, 9, dev1, batchPointID(11), "zone.temperature", json.RawMessage(`21.0`), "NUMBER", &cel, "TELEMETRY", base.Add(14*time.Second), base.Add(15*time.Second))
	c5LateSample := batchCandidate(15, partition, 12, dev1, batchPointID(11), "zone.temperature", json.RawMessage(`20.0`), "NUMBER", &cel, "TELEMETRY", base.Add(5*time.Second), base.Add(15*time.Second))

	receipts, err := store.AcceptObservationBatch(ctx, []ObservationCandidate{c0, c1, c2DupEvent, c3ReplayedOffset, c4LowerOffset, c5LateSample})
	if err != nil {
		t.Fatal(err)
	}
	if receipts[0].Status != ObservationAccepted || receipts[1].Status != ObservationAccepted || receipts[1].BusinessRevision != 1 {
		t.Fatalf("accepted receipts=%#v", receipts[:2])
	}
	if receipts[2].Status != ObservationDuplicate || receipts[2].ObservationID != receipts[0].ObservationID || receipts[2].EvidenceID == "" || receipts[2].PositionAdvanced {
		t.Fatalf("dupEvent receipt=%#v", receipts[2])
	}
	if receipts[3].Status != ObservationDuplicate || len(receipts[3].QualityReasons) != 1 || receipts[3].QualityReasons[0] != QualityReasonReplayed || receipts[3].EvidenceID == "" || receipts[3].PositionAdvanced {
		t.Fatalf("replayedOffset receipt=%#v", receipts[3])
	}
	if receipts[4].Status != ObservationOutOfOrder || len(receipts[4].QualityReasons) != 1 || receipts[4].QualityReasons[0] != QualityReasonOutOfOrder || receipts[4].EvidenceID == "" || receipts[4].PositionAdvanced {
		t.Fatalf("lowerOffset receipt=%#v", receipts[4])
	}
	if receipts[5].Status != ObservationOutOfOrder || receipts[5].Quality != QualityGood || !receipts[5].PositionAdvanced || receipts[5].BusinessRevision != 0 {
		t.Fatalf("lateSample receipt=%#v", receipts[5])
	}
	var latestVal string
	var headOffset int64
	if err := admin.QueryRow(ctx, `SELECT value::text FROM telemetry_runtime.latest_accepted_telemetry WHERE device_id = $1::uuid AND telemetry_key = 'zone.temperature'`, dev1).Scan(&latestVal); err != nil || latestVal != "23.0" {
		t.Fatalf("latest=%q err=%v", latestVal, err)
	}
	if err := admin.QueryRow(ctx, `SELECT source_offset FROM telemetry_runtime.source_positions WHERE source_id = $1 AND source_partition = $2`, sourceA, partition).Scan(&headOffset); err != nil || headOffset != 12 {
		t.Fatalf("headOffset=%d err=%v", headOffset, err)
	}
}

func TestPostgresBatchSamePointHealthAndCrossPointQualityVisibility(t *testing.T) {
	runtimeURL, adminURL := postgresTestURLs(t)
	ctx := t.Context()
	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	cleanupBatchIntegrationState(t, admin)
	defer cleanupBatchIntegrationState(t, admin)

	base := time.Date(2026, 7, 15, 2, 0, 0, 0, time.UTC)
	dev1 := batchDeviceID(3)
	seedBatchIntegrationDevice(t, admin, dev1, base)
	cel, kw := "Cel", "kW"
	minTemp, maxTemp := -50.0, 100.0
	seedBatchFreshnessPolicy(t, admin, dev1, "run_state", "STRING", nil, nil, nil, nil, base)
	seedBatchFreshnessPolicy(t, admin, dev1, "cw_in", "NUMBER", &cel, nil, nil, nil, base)
	seedBatchFreshnessPolicy(t, admin, dev1, "zone.temperature", "NUMBER", &cel, &minTemp, &maxTemp, nil, base)
	derivedChecks, _ := json.Marshal(map[string]any{"inputs": []map[string]any{{"deviceId": dev1, "key": "zone.temperature"}}})
	seedBatchFreshnessPolicy(t, admin, dev1, "derived.cooling", "NUMBER", &kw, nil, nil, derivedChecks, base)
	supplyChecks, _ := json.Marshal(map[string]any{
		"physics": []map[string]any{{
			"kind": "CW_DIRECTION", "epsilonK": 0.05,
			"peers":   []map[string]any{{"role": "entering", "deviceId": dev1, "key": "cw_in"}},
			"running": []map[string]any{{"deviceId": dev1, "key": "run_state", "equals": "RUNNING"}},
		}},
	})
	seedBatchFreshnessPolicy(t, admin, dev1, "cw_out", "NUMBER", &cel, nil, nil, supplyChecks, base)

	store, err := OpenPostgresStore(ctx, runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	const partition = "tb-batch-test-quality"
	t0 := base.Add(10 * time.Second)
	receipts, err := store.AcceptObservationBatch(ctx, []ObservationCandidate{
		batchCandidate(21, partition, 1, dev1, batchPointID(21), "run_state", json.RawMessage(`"RUNNING"`), "STRING", nil, "STATE", t0, t0),
		batchCandidate(22, partition, 2, dev1, batchPointID(22), "cw_in", json.RawMessage(`28.0`), "NUMBER", &cel, "TELEMETRY", t0.Add(time.Second), t0.Add(time.Second)),
		batchCandidate(23, partition, 3, dev1, batchPointID(23), "zone.temperature", json.RawMessage(`24.0`), "NUMBER", &cel, "TELEMETRY", t0.Add(time.Second), t0.Add(time.Second)),
		batchCandidate(24, partition, 4, dev1, batchPointID(23), "zone.temperature", json.RawMessage(`500.0`), "NUMBER", &cel, "TELEMETRY", t0.Add(2*time.Second), t0.Add(2*time.Second)),
		batchCandidate(25, partition, 5, dev1, batchPointID(24), "derived.cooling", json.RawMessage(`420.0`), "NUMBER", &kw, "TELEMETRY", t0.Add(3*time.Second), t0.Add(3*time.Second)),
		batchCandidate(26, partition, 6, dev1, batchPointID(25), "cw_out", json.RawMessage(`27.0`), "NUMBER", &cel, "TELEMETRY", t0.Add(4*time.Second), t0.Add(4*time.Second)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if receipts[2].Status != ObservationAccepted || receipts[3].Status != ObservationRejected ||
		receipts[4].Status != ObservationAccepted || receipts[4].Quality != QualityPartial ||
		receipts[5].Status != ObservationAccepted || receipts[5].Quality != QualityPartial {
		t.Fatalf("unexpected quality receipts=%#v", receipts)
	}
	if len(receipts[4].QualityReasons) != 1 || receipts[4].QualityReasons[0] != QualityReasonDerivedInput {
		t.Fatalf("derived.cooling reasons=%#v", receipts[4].QualityReasons)
	}
	if len(receipts[5].QualityReasons) != 1 || receipts[5].QualityReasons[0] != QualityReasonPhysicsCwDirection {
		t.Fatalf("cw_out reasons=%#v", receipts[5].QualityReasons)
	}

	var tempVal, tempHealth string
	if err := admin.QueryRow(ctx, `SELECT value::text, health_status FROM telemetry_runtime.latest_accepted_telemetry WHERE device_id = $1::uuid AND telemetry_key = 'zone.temperature'`, dev1).Scan(&tempVal, &tempHealth); err != nil || tempVal != "24.0" || tempHealth != HealthBad {
		t.Fatalf("zone.temperature value=%q health=%q err=%v", tempVal, tempHealth, err)
	}
	assertIngestRevision(t, admin, dev1, 1, 1)
}

func TestPostgresBatchMixedValidAndQuarantined(t *testing.T) {
	runtimeURL, adminURL := postgresTestURLs(t)
	ctx := t.Context()
	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	cleanupBatchIntegrationState(t, admin)
	defer cleanupBatchIntegrationState(t, admin)

	base := time.Date(2026, 7, 15, 3, 0, 0, 0, time.UTC)
	dev1 := batchDeviceID(4)
	seedBatchIntegrationDevice(t, admin, dev1, base)
	cel := "Cel"
	seedBatchFreshnessPolicy(t, admin, dev1, "zone.temperature", "NUMBER", &cel, nil, nil, nil, base)

	store, err := OpenPostgresStore(ctx, runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	const partition = "tb-batch-test-quarantine"
	at := base.Add(10 * time.Second)
	valid := batchCandidate(31, partition, 1, dev1, batchPointID(31), "zone.temperature", json.RawMessage(`22.5`), "NUMBER", &cel, "TELEMETRY", at, at)
	unmappedDev := ObservationCandidate{
		SourceID: sourceA, SourcePath: SourcePathPush, ExternalEntityType: "DEVICE", ExternalID: "missing-device",
		TelemetryKey: "zone.temperature", Value: json.RawMessage(`99.9`), ValueType: "NUMBER", Unit: &cel,
		SampledAt: at, ReceivedAt: at, Position: SourcePosition{Partition: partition, Offset: 2, EventID: batchEventID(32)},
	}
	unmappedPt := batchCandidate(33, partition, 3, dev1, batchPointID(32), "zone.temperature", json.RawMessage(`23.0`), "NUMBER", &cel, "TELEMETRY", at, at)
	unmappedPt.Point = nil
	conflictDev := batchCandidate(34, partition, 4, dev1, batchPointID(31), "zone.temperature", json.RawMessage(`24.0`), "NUMBER", &cel, "TELEMETRY", at, at)
	conflictDev.Device = &ResolvedDevice{TenantID: tenantB, SiteID: siteA, DeviceID: dev1}
	unconfiguredPol := batchCandidate(35, partition, 5, dev1, batchPointID(33), "unconfigured.key", json.RawMessage(`25.0`), "NUMBER", &cel, "TELEMETRY", at, at)

	receipts, err := store.AcceptObservationBatch(ctx, []ObservationCandidate{valid, unmappedDev, unmappedPt, conflictDev, unconfiguredPol})
	if err != nil {
		t.Fatal(err)
	}
	wantReasons := []QuarantineReason{"", QuarantineMappingNotFound, QuarantinePointMappingNotFound, QuarantineMappingConflict, QuarantinePolicyNotConfigured}
	if receipts[0].Status != ObservationAccepted || receipts[0].BusinessRevision != 1 || receipts[0].DeviceID != dev1 {
		t.Fatalf("valid receipt=%#v", receipts[0])
	}
	for index := 1; index < len(receipts); index++ {
		if receipts[index].Status != ObservationQuarantined || receipts[index].QuarantineReason != wantReasons[index] || receipts[index].DeviceID != "" || !receipts[index].PositionAdvanced {
			t.Fatalf("quarantine[%d]=%#v want=%s", index, receipts[index], wantReasons[index])
		}
	}
	var qCount int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM telemetry_runtime.ingest_quarantine WHERE evidence ->> 'sourcePartition' = $1`, partition).Scan(&qCount); err != nil || qCount != 4 {
		t.Fatalf("quarantine count=%d err=%v", qCount, err)
	}
}

func TestPostgresBatchOverlappingConcurrentDevicesAndReadsDoNotDeadlock(t *testing.T) {
	runtimeURL, adminURL := postgresTestURLs(t)
	ctx := t.Context()
	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	cleanupBatchIntegrationState(t, admin)
	defer cleanupBatchIntegrationState(t, admin)

	base := time.Date(2026, 7, 15, 4, 0, 0, 0, time.UTC)
	dev1, dev2 := batchDeviceID(5), batchDeviceID(6)
	cel := "Cel"
	for _, devID := range []string{dev1, dev2} {
		seedBatchIntegrationDevice(t, admin, devID, base)
		seedBatchFreshnessPolicy(t, admin, devID, "zone.temperature", "NUMBER", &cel, nil, nil, nil, base)
	}

	store, err := OpenPostgresStore(ctx, runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	const rounds = 20
	errs := make(chan error, 3)
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	runBatchWorker := func(eventBase int, partA, partB string, reverse bool) {
		defer wg.Done()
		for round := range rounds {
			at := base.Add(time.Duration(round+1) * time.Second)
			cA := batchCandidate(eventBase+round*2, partA, int64(round+1), dev1, batchPointID(41), "zone.temperature", json.RawMessage(`22.5`), "NUMBER", &cel, "TELEMETRY", at, at)
			cB := batchCandidate(eventBase+round*2+1, partB, int64(round+1), dev2, batchPointID(42), "zone.temperature", json.RawMessage(`23.5`), "NUMBER", &cel, "TELEMETRY", at, at)
			batch := []ObservationCandidate{cA, cB}
			if reverse {
				batch = []ObservationCandidate{cB, cA}
			}
			if _, err := store.AcceptObservationBatch(ctx, batch); err != nil {
				errs <- fmt.Errorf("round %d reverse=%v: %w", round, reverse, err)
				return
			}
		}
		errs <- nil
	}
	go runBatchWorker(100, "tb-batch-test-conc-a1", "tb-batch-test-conc-a2", false)
	go runBatchWorker(200, "tb-batch-test-conc-b1", "tb-batch-test-conc-b2", true)
	go func() {
		reads := 0
		for {
			select {
			case <-done:
				errs <- nil
				return
			default:
			}
			dev := []string{dev1, dev2}[reads%2]
			if _, err := store.EvaluateAndRead(ctx, telemetryauth.Target{DeviceID: dev}, base.Add(time.Minute)); err != nil {
				errs <- fmt.Errorf("EvaluateAndRead %d: %w", reads, err)
				return
			}
			reads++
		}
	}()
	wg.Wait()
	close(done)
	for range 3 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
}

func TestPostgresHistoricalObservationBatchPreservesSnapshot(t *testing.T) {
	runtimeURL, adminURL := postgresTestURLs(t)
	ctx := t.Context()
	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	cleanupBatchIntegrationState(t, admin)
	defer cleanupBatchIntegrationState(t, admin)

	base := time.Date(2026, 7, 15, 5, 0, 0, 0, time.UTC)
	dev1 := batchDeviceID(7)
	seedBatchIntegrationDevice(t, admin, dev1, base)
	cel := "Cel"
	seedBatchFreshnessPolicy(t, admin, dev1, "zone.temperature", "NUMBER", &cel, nil, nil, nil, base)

	store, err := OpenPostgresStore(ctx, runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	liveAt := base.Add(time.Minute)
	live := batchCandidate(301, "tb-batch-test-hist-live", 1, dev1, batchPointID(51), "zone.temperature", json.RawMessage(`24.5`), "NUMBER", &cel, "TELEMETRY", liveAt, liveAt)
	liveReceipt, err := store.AcceptObservation(ctx, live)
	if err != nil || liveReceipt.Status != ObservationAccepted || liveReceipt.BusinessRevision != 1 {
		t.Fatalf("live=%#v err=%v", liveReceipt, err)
	}

	var beforeValue, beforeSHA string
	var beforeSignals int64
	if err := admin.QueryRow(ctx, `SELECT value::text FROM telemetry_runtime.latest_accepted_telemetry WHERE device_id = $1::uuid AND telemetry_key = 'zone.temperature'`, dev1).Scan(&beforeValue); err != nil {
		t.Fatal(err)
	}
	if err := admin.QueryRow(ctx, `SELECT snapshot_sha256 FROM telemetry_runtime.device_observation_snapshots WHERE device_id = $1::uuid`, dev1).Scan(&beforeSHA); err != nil {
		t.Fatal(err)
	}
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM telemetry_runtime.presence_signals WHERE device_id = $1::uuid`, dev1).Scan(&beforeSignals); err != nil {
		t.Fatal(err)
	}

	const replayPartition = "tb-batch-test-hist-replay"
	r0 := ObservationCandidate{
		SourceID: historicalReplaySourceID, SourcePath: SourcePathHistoryReplay,
		ExternalEntityType: "DEVICE", ExternalID: dev1, TelemetryKey: "zone.temperature",
		Value: json.RawMessage(`20.0`), ValueType: "NUMBER", Unit: &cel,
		SampledAt: base.Add(-2 * time.Hour), ReceivedAt: liveAt.Add(time.Minute),
		Position: SourcePosition{Partition: replayPartition, Offset: 1, EventID: batchEventID(302)},
	}
	r1 := r0
	r1.Value = json.RawMessage(`20.5`)
	r1.SampledAt = base.Add(-time.Hour)
	r1.Position = SourcePosition{Partition: replayPartition, Offset: 2, EventID: batchEventID(303)}
	r2Dup := r0
	r2Dup.Position.Offset = 3

	receipts, err := store.AcceptHistoricalObservationBatch(ctx, []ObservationCandidate{r0, r1, r2Dup})
	if err != nil {
		t.Fatal(err)
	}
	if receipts[0].Status != ObservationAccepted || receipts[0].BusinessRevision != 0 || receipts[0].StateChanged || !receipts[0].PositionAdvanced ||
		receipts[1].Status != ObservationAccepted || receipts[1].BusinessRevision != 0 || receipts[1].StateChanged || !receipts[1].PositionAdvanced ||
		receipts[2].Status != ObservationDuplicate || receipts[2].ObservationID != receipts[0].ObservationID || receipts[2].PositionAdvanced {
		t.Fatalf("historical batch receipts=%#v", receipts)
	}
	assertObservationRow(t, admin, r0.Position.EventID, "ACCEPTED", "GOOD", "HISTORY_REPLAY", true)
	assertObservationRow(t, admin, r1.Position.EventID, "ACCEPTED", "GOOD", "HISTORY_REPLAY", true)
	assertHistoryOutbox(t, admin, r0.Position.EventID, "ACCEPTED", tenantA, siteA, dev1, true)
	assertHistoryOutbox(t, admin, r1.Position.EventID, "ACCEPTED", tenantA, siteA, dev1, true)

	var afterValue, afterSHA string
	var afterSignals int64
	if err := admin.QueryRow(ctx, `SELECT value::text FROM telemetry_runtime.latest_accepted_telemetry WHERE device_id = $1::uuid AND telemetry_key = 'zone.temperature'`, dev1).Scan(&afterValue); err != nil {
		t.Fatal(err)
	}
	if err := admin.QueryRow(ctx, `SELECT snapshot_sha256 FROM telemetry_runtime.device_observation_snapshots WHERE device_id = $1::uuid`, dev1).Scan(&afterSHA); err != nil {
		t.Fatal(err)
	}
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM telemetry_runtime.presence_signals WHERE device_id = $1::uuid`, dev1).Scan(&afterSignals); err != nil {
		t.Fatal(err)
	}
	if afterValue != beforeValue || afterSHA != beforeSHA || afterSignals != beforeSignals {
		t.Fatalf("historical batch mutated current state: value=%s->%s sha=%s->%s signals=%d->%d", beforeValue, afterValue, beforeSHA, afterSHA, beforeSignals, afterSignals)
	}
}

func TestPostgresBatchAndProbeOffsetSweepDoNotDeadlock(t *testing.T) {
	runtimeURL, adminURL := postgresTestURLs(t)
	ctx := t.Context()
	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	cleanupBatchIntegrationState(t, admin)
	defer cleanupBatchIntegrationState(t, admin)

	base := time.Date(2026, 7, 15, 6, 0, 0, 0, time.UTC)
	dev1, dev2 := batchDeviceID(8), batchDeviceID(9)
	cel := "Cel"
	for _, devID := range []string{dev1, dev2} {
		seedBatchIntegrationDevice(t, admin, devID, base)
		seedBatchFreshnessPolicy(t, admin, devID, "chw_supply_temperature", "NUMBER", &cel, nil, nil, nil, base)
		seedBatchFreshnessPolicy(t, admin, devID, "chw_return_temperature", "NUMBER", &cel, nil, nil, nil, base)
		seedBatchFreshnessPolicy(t, admin, devID, "pump_running", "BOOLEAN", nil, nil, nil, nil, base)
		seedBatchFreshnessPolicy(t, admin, devID, "chiller_state", "STRING", nil, nil, nil, nil, base)
	}
	for index, pair := range []struct {
		code, supplyDev, returnDev string
	}{
		{"chw-batch-deadlock-fwd", dev1, dev2},
		{"chw-batch-deadlock-rev", dev2, dev1},
	} {
		pairJSON, err := json.Marshal(map[string]any{
			"supply":         map[string]any{"deviceId": pair.supplyDev, "key": "chw_supply_temperature"},
			"return":         map[string]any{"deviceId": pair.returnDev, "key": "chw_return_temperature"},
			"pumps":          []map[string]any{{"deviceId": dev1, "key": "pump_running", "equals": "true"}},
			"chillers":       []map[string]any{{"deviceId": dev2, "key": "chiller_state", "equals": "IDLE"}},
			"stableBandK":    0.2,
			"minimumMinutes": 10,
			"minimumSamples": 4,
			"maxGapMinutes":  15,
			"offsetAlarmK":   0.2,
			"driftAlarmK":    0.1,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := admin.Exec(ctx, `
INSERT INTO telemetry_runtime.probe_pairs (site_id, pair_code, config, updated_at)
VALUES ($1::uuid, $2, $3::jsonb, $4)`, siteA, pair.code, pairJSON, base.Add(time.Duration(index)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}

	store, err := OpenPostgresStore(ctx, runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	const rounds = 20
	errs := make(chan error, 2)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for round := range rounds {
			at := base.Add(time.Duration(round+1) * time.Minute)
			batch := []ObservationCandidate{
				batchCandidate(400+round*4, "tb-batch-test-probe-a", int64(round*2+1), dev1, batchPointID(61), "pump_running", json.RawMessage(`true`), "BOOLEAN", nil, "STATE", at, at),
				batchCandidate(401+round*4, "tb-batch-test-probe-a", int64(round*2+2), dev1, batchPointID(62), "chw_supply_temperature", json.RawMessage(`7.0`), "NUMBER", &cel, "TELEMETRY", at, at),
				batchCandidate(402+round*4, "tb-batch-test-probe-b", int64(round*2+1), dev2, batchPointID(63), "chiller_state", json.RawMessage(`"IDLE"`), "STRING", nil, "STATE", at, at),
				batchCandidate(403+round*4, "tb-batch-test-probe-b", int64(round*2+2), dev2, batchPointID(64), "chw_return_temperature", json.RawMessage(`7.3`), "NUMBER", &cel, "TELEMETRY", at, at),
			}
			if _, err := store.AcceptObservationBatch(ctx, batch); err != nil {
				errs <- fmt.Errorf("batch round %d: %w", round, err)
				return
			}
		}
		errs <- nil
	}()
	go func() {
		step := 0
		for {
			select {
			case <-done:
				errs <- nil
				return
			default:
			}
			if _, err := store.SweepProbeOffsets(ctx, base.Add(time.Duration(step+1)*time.Minute)); err != nil {
				errs <- fmt.Errorf("SweepProbeOffsets step %d: %w", step, err)
				return
			}
			step++
		}
	}()
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
}
