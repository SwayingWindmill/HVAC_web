package telemetry

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
	"github.com/quanlaihe/hvac-web/modules/telemetry/pkg/telemetryapi"
)

const (
	ingestPartitionA = "tb-ticket-04-a"
	ingestPartitionB = "tb-ticket-04-b"
	integrationB     = "018f2e00-6000-7000-8000-000000000002"
)

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
		ingestEvent(101), integrationA, ingestPartitionA, 1, SourcePathWebhook,
		"tb-device-org-a-site-1", "zone.temperature", json.RawMessage(`24.0`), "NUMBER", "Cel",
		acceptedAt.Add(-2*time.Second), acceptedAt,
	)
	receipt := acceptObservationViaHTTP(t, store, accepted)
	if receipt.Status != ObservationAccepted || receipt.Quality != QualityGood || receipt.BusinessRevision != 2 || !receipt.StateChanged || !receipt.PositionAdvanced || receipt.DeviceID != deviceA {
		t.Fatalf("accepted receipt=%#v", receipt)
	}
	assertObservationRow(t, admin, accepted.Position.EventID, "ACCEPTED", "GOOD", "WEBHOOK", true)
	assertHistoryOutbox(t, admin, accepted.Position.EventID, "ACCEPTED", orgA, siteA, deviceA, true)
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
		ingestEvent(199), integrationA, ingestPartitionB, 1, SourcePathPush,
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
	assertHistoryOutbox(t, admin, lateSample.Position.EventID, "OUT_OF_ORDER", orgA, siteA, deviceA, true)
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
		ingestEvent(104), integrationA, ingestPartitionA, 2, SourcePathPush,
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
	assertHistoryOutbox(t, admin, rejected.Position.EventID, "REJECTED", orgA, siteA, deviceA, false)
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
		ingestEvent(106), integrationA, ingestPartitionA, 4, SourcePathReconciliation,
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

	outageAt := suspectReceivedAt.Add(time.Minute)
	outage, err := store.ReportCoverage(ctx, CoverageReport{
		IntegrationInstanceID: integrationA, ExternalEntityType: "DEVICE", ExternalID: "tb-device-org-a-site-1",
		Available: false, Reason: telemetryapi.AvailabilityReasonCodeSourceUnavailable,
		SourceRevision: 2, ReportedAt: outageAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if outage.Status != "APPLIED" || outage.BusinessRevision != 5 || !outage.StateChanged {
		t.Fatalf("outage=%#v", outage)
	}
	assertSnapshotAvailability(t, admin, deviceA, "UNAVAILABLE", "SOURCE_UNAVAILABLE", "")
	assertIngestRevision(t, admin, deviceA, 5, 5)

	recoveryAt := outageAt.Add(time.Minute)
	recoveryStart := recoveryAt
	recovery, err := store.ReportCoverage(ctx, CoverageReport{
		IntegrationInstanceID: integrationA, ExternalEntityType: "DEVICE", ExternalID: "tb-device-org-a-site-1",
		Available: true, ContinuousSince: &recoveryStart, SourceRevision: 3, ReportedAt: recoveryAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if recovery.Status != "APPLIED" || recovery.BusinessRevision != 6 || !recovery.StateChanged {
		t.Fatalf("recovery=%#v", recovery)
	}
	assertSnapshotAvailability(t, admin, deviceA, "UNAVAILABLE", "OBSERVATION_COVERAGE_GAP", "")
	assertIngestRevision(t, admin, deviceA, 6, 6)

	recentAt := recoveryAt.Add(6 * time.Second)
	recent := ingestCandidate(
		ingestEvent(107), integrationA, ingestPartitionA, 5, SourcePathPoll,
		"tb-device-org-a-site-1", "zone.temperature", json.RawMessage(`25.5`), "NUMBER", "Cel",
		recentAt.Add(-time.Second), recentAt,
	)
	recentReceipt, err := store.AcceptObservation(ctx, recent)
	if err != nil {
		t.Fatal(err)
	}
	if recentReceipt.BusinessRevision != 7 || !recentReceipt.StateChanged {
		t.Fatalf("recent=%#v", recentReceipt)
	}
	assertSnapshotAvailability(t, admin, deviceA, "AVAILABLE", "", "ONLINE")
	assertIngestRevision(t, admin, deviceA, 7, 7)

	missing := ingestCandidate(
		ingestEvent(108), integrationA, "tb-ticket-04-missing", 1, SourcePathWebhook,
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
  ON o.integration_instance_id = q.integration_instance_id
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
	assertIngestRevision(t, admin, deviceA, 7, 7)

	coverageQuarantineAt := recentAt.Add(1500 * time.Millisecond)
	coverageMissingReport := CoverageReport{
		IntegrationInstanceID: integrationA, ExternalEntityType: "DEVICE", ExternalID: "tb-device-coverage-missing",
		Available: false, Reason: telemetryapi.AvailabilityReasonCodeSourceUnavailable,
		SourceRevision: 1, ReportedAt: coverageQuarantineAt,
	}
	coverageQuarantine, err := store.ReportCoverage(ctx, coverageMissingReport)
	if err != nil {
		t.Fatal(err)
	}
	if coverageQuarantine.Status != "QUARANTINED" || coverageQuarantine.QuarantineReason != QuarantineMappingNotFound || coverageQuarantine.EvidenceID == "" || coverageQuarantine.DeviceID != "" {
		t.Fatalf("coverage quarantine=%#v", coverageQuarantine)
	}
	var coverageKind string
	var coverageDeviceID, coverageKey *string
	if err := admin.QueryRow(ctx, `
SELECT evidence ->> 'kind', device_id::text, telemetry_key
FROM telemetry_runtime.ingest_quarantine
WHERE quarantine_id = $1::uuid
`, coverageQuarantine.EvidenceID).Scan(&coverageKind, &coverageDeviceID, &coverageKey); err != nil {
		t.Fatal(err)
	}
	if coverageKind != "OBSERVATION_COVERAGE_REPORT" || coverageDeviceID != nil || coverageKey != nil {
		t.Fatalf("coverage evidence kind=%s device=%v key=%v", coverageKind, coverageDeviceID, coverageKey)
	}
	coverageQuarantineAgain, err := store.ReportCoverage(ctx, coverageMissingReport)
	if err != nil {
		t.Fatal(err)
	}
	if coverageQuarantineAgain.EvidenceID != coverageQuarantine.EvidenceID {
		t.Fatalf("coverage quarantine evidence was not idempotent: first=%s second=%s", coverageQuarantine.EvidenceID, coverageQuarantineAgain.EvidenceID)
	}
	assertIngestRevision(t, admin, deviceA, 7, 7)

	extremeFutureReceivedAt := recentAt.Add(1750 * time.Millisecond)
	extremeFuture := ingestCandidate(
		ingestEvent(111), integrationA, "tb-ticket-04-future-clock", 1, SourcePathPush,
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
	assertObservationRow(t, admin, extremeFuture.Position.EventID, "REJECTED", "REJECTED", "PUSH", false)
	var extremeFutureSampledAt time.Time
	if err := admin.QueryRow(ctx, `SELECT sampled_at FROM telemetry_runtime.source_observations WHERE source_event_id = $1::uuid`, extremeFuture.Position.EventID).Scan(&extremeFutureSampledAt); err != nil {
		t.Fatal(err)
	}
	if !extremeFutureSampledAt.Equal(extremeFuture.SampledAt) {
		t.Fatalf("future-clock evidence sampledAt=%s expected=%s", extremeFutureSampledAt, extremeFuture.SampledAt)
	}
	assertIngestRevision(t, admin, deviceA, 7, 7)

	bAt := recentAt.Add(2 * time.Second)
	organizationB := ingestCandidate(
		ingestEvent(109), integrationB, ingestPartitionB, 1, SourcePathPush,
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
	if !strings.Contains(organizationBSnapshot, orgB) || strings.Contains(organizationBSnapshot, orgA) || strings.Contains(organizationBSnapshot, deviceA) {
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
	assertIngestRevision(t, admin, deviceA, 7, 7)
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
WHERE integration_instance_id = $1::uuid AND source_partition = $2
`, integrationA, ingestPartitionA).Scan(&headOffset); err != nil {
		t.Fatal(err)
	}
	if headOffset != 5 {
		t.Fatalf("rollback source position=%d", headOffset)
	}

	committedAfterRollback, err := store.AcceptObservation(ctx, rollbackCandidate)
	if err != nil {
		t.Fatal(err)
	}
	if committedAfterRollback.BusinessRevision != 8 || !committedAfterRollback.StateChanged {
		t.Fatalf("committed after rollback=%#v", committedAfterRollback)
	}
	assertIngestRevision(t, admin, deviceA, 8, 8)

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
	assertIngestRevision(t, admin, deviceA, 8, 8)

	if _, err := admin.Exec(ctx, `
UPDATE telemetry_runtime.telemetry_publication_outbox
SET attempts = attempts + 2, last_error_code = 'CENTRIFUGO_UNAVAILABLE', available_at = available_at + interval '5 seconds'
WHERE device_id = $1::uuid AND business_revision = 8 AND subscription_id IS NULL
`, deviceA); err != nil {
		t.Fatal(err)
	}
	assertIngestRevision(t, admin, deviceA, 8, 8)
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

func ingestCandidate(eventID, integrationID, partition string, offset int64, sourcePath SourcePath, externalID, key string, value json.RawMessage, valueType, unit string, sampledAt, receivedAt time.Time) ObservationCandidate {
	return ObservationCandidate{
		IntegrationInstanceID: integrationID, SourcePath: sourcePath,
		ExternalEntityType: "DEVICE", ExternalID: externalID, TelemetryKey: key,
		Value: append(json.RawMessage(nil), value...), ValueType: valueType, Unit: &unit,
		SampledAt: sampledAt, ReceivedAt: receivedAt,
		Position: SourcePosition{Partition: partition, Offset: offset, EventID: eventID},
	}
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

func assertHistoryOutbox(t *testing.T, admin *pgxpool.Pool, sourceEventID, status, organizationID, siteID, deviceID string, valuePresent bool) {
	t.Helper()
	var deliveryState, actualStatus string
	var actualOrganizationID, actualSiteID, actualDeviceID, value *string
	if err := admin.QueryRow(t.Context(), `
SELECT delivery_state,
       payload ->> 'acceptance_status',
       payload ->> 'owning_organization_id',
       payload ->> 'site_id',
       payload ->> 'device_id',
       COALESCE(payload ->> 'value_json', payload ->> 'value_number', payload ->> 'value_string', payload ->> 'value_boolean')
FROM telemetry_runtime.telemetry_history_outbox
WHERE payload ->> 'source_event_id' = $1
`, sourceEventID).Scan(&deliveryState, &actualStatus, &actualOrganizationID, &actualSiteID, &actualDeviceID, &value); err != nil {
		t.Fatal(err)
	}
	optionalMatches := func(actual *string, expected string) bool {
		if expected == "" {
			return actual == nil
		}
		return actual != nil && *actual == expected
	}
	if deliveryState != "PENDING" || actualStatus != status ||
		!optionalMatches(actualOrganizationID, organizationID) || !optionalMatches(actualSiteID, siteID) || !optionalMatches(actualDeviceID, deviceID) ||
		(value != nil) != valuePresent {
		t.Fatalf("history outbox state=%s status=%s organization=%v site=%v device=%v value=%v", deliveryState, actualStatus, actualOrganizationID, actualSiteID, actualDeviceID, value)
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
