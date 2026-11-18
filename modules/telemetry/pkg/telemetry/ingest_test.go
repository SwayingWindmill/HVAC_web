package telemetry

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

const (
	ingestTenantA = "018f2d00-0000-7000-8000-000000000001"
	sourceA       = "EG8200-COMMERCIAL-001"
	eventA        = "018f2e00-8000-7000-8000-000000000011"
)

func TestEvaluateObservationFailsClosedOnIdentity(t *testing.T) {
	now := time.Date(2026, 7, 24, 2, 0, 0, 0, time.UTC)
	candidate := validObservationCandidate(now)

	tests := []struct {
		name       string
		mutate     func(*ObservationFacts)
		quarantine QuarantineReason
		device     string
	}{
		{name: "unregistered Device", mutate: func(f *ObservationFacts) { f.Device, f.Point = nil, nil }, quarantine: QuarantineMappingNotFound},
		{name: "Device known in another scope", mutate: func(f *ObservationFacts) { f.DeviceConflict = true }, quarantine: QuarantineMappingConflict},
		{name: "unregistered Point", mutate: func(f *ObservationFacts) { f.Point = nil }, quarantine: QuarantinePointMappingNotFound, device: deviceA},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			facts := validObservationFacts()
			test.mutate(&facts)
			decision := EvaluateObservation(candidate, facts, now)
			if decision.Status != ObservationQuarantined || decision.QuarantineReason != test.quarantine || decision.DeviceID != test.device {
				t.Fatalf("decision=%#v", decision)
			}
			if decision.PointID != "" || decision.ReplaceLatest || decision.ReevaluateSnapshot {
				t.Fatalf("identity failure mutated Device state: %#v", decision)
			}
			if !decision.AdvancePosition {
				t.Fatal("new quarantined source position must be receipted")
			}
		})
	}
}

func TestEvaluateObservationFreezesPositionSemantics(t *testing.T) {
	now := time.Date(2026, 7, 24, 2, 0, 0, 0, time.UTC)
	candidate := validObservationCandidate(now)
	facts := validObservationFacts()

	facts.EventAlreadySeen = true
	decision := EvaluateObservation(candidate, facts, now)
	assertObservationDecision(t, decision, ObservationDuplicate, QualityInvalid, []QualityReason{QualityReasonDuplicate}, false, false)

	facts.EventAlreadySeen = false
	facts.CurrentPosition = &SourcePositionHead{Offset: candidate.Position.Offset, EventID: "018f2e00-8000-7000-8000-000000000099"}
	decision = EvaluateObservation(candidate, facts, now)
	assertObservationDecision(t, decision, ObservationDuplicate, QualityInvalid, []QualityReason{QualityReasonReplayed}, false, false)

	facts.CurrentPosition = &SourcePositionHead{Offset: candidate.Position.Offset + 1, EventID: "018f2e00-8000-7000-8000-000000000099"}
	decision = EvaluateObservation(candidate, facts, now)
	assertObservationDecision(t, decision, ObservationOutOfOrder, QualityInvalid, []QualityReason{QualityReasonOutOfOrder}, false, false)

	facts.CurrentPosition = &SourcePositionHead{Offset: candidate.Position.Offset - 1, EventID: "018f2e00-8000-7000-8000-000000000099"}
	latest := now.Add(-30 * time.Second)
	facts.LatestSampledAt = &latest
	candidate.SampledAt = latest.Add(-time.Second)
	decision = EvaluateObservation(candidate, facts, now)
	assertObservationDecision(t, decision, ObservationOutOfOrder, QualityGood, []QualityReason{QualityReasonOutOfOrder}, true, false)
}

func TestEvaluateObservationValidationAndQuality(t *testing.T) {
	now := time.Date(2026, 7, 24, 2, 0, 0, 0, time.UTC)
	base := validObservationCandidate(now)
	facts := validObservationFacts()

	tests := []struct {
		name    string
		mutate  func(*ObservationCandidate, *ObservationFacts)
		status  ObservationStatus
		quality ObservationQuality
		reason  QualityReason
		replace bool
	}{
		{name: "declared type mismatch", mutate: func(c *ObservationCandidate, _ *ObservationFacts) { c.ValueType = "STRING" }, status: ObservationRejected, quality: QualityInvalid, reason: QualityReasonTypeMismatch},
		{name: "payload type mismatch", mutate: func(c *ObservationCandidate, _ *ObservationFacts) { c.Value = json.RawMessage(`"hot"`) }, status: ObservationRejected, quality: QualityInvalid, reason: QualityReasonTypeMismatch},
		{name: "public value limit", mutate: func(c *ObservationCandidate, f *ObservationFacts) {
			value, _ := json.Marshal(strings.Repeat("x", 4097))
			c.Value, c.ValueType = value, "STRING"
			f.Policy.ValueType = "STRING"
			f.Policy.Unit = nil
			c.Unit = nil
		}, status: ObservationRejected, quality: QualityInvalid, reason: QualityReasonTypeMismatch},
		{name: "unit mismatch", mutate: func(c *ObservationCandidate, _ *ObservationFacts) { unit := "degF"; c.Unit = &unit }, status: ObservationRejected, quality: QualityInvalid, reason: QualityReasonUnitMismatch},
		{name: "below range", mutate: func(c *ObservationCandidate, _ *ObservationFacts) { c.Value = json.RawMessage(`-80`) }, status: ObservationRejected, quality: QualityInvalid, reason: QualityReasonOutOfRange},
		{name: "future clock", mutate: func(c *ObservationCandidate, _ *ObservationFacts) { c.SampledAt = now.Add(31 * time.Second) }, status: ObservationRejected, quality: QualityInvalid, reason: QualityReasonClockAhead},
		{name: "source lag stale", mutate: func(c *ObservationCandidate, _ *ObservationFacts) { c.SampledAt = now.Add(-11 * time.Minute) }, status: ObservationAccepted, quality: QualityStale, reason: QualityReasonSourceLagExceeded, replace: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := base
			candidate.Value = append(json.RawMessage(nil), base.Value...)
			localFacts := facts
			policy := *facts.Policy
			localFacts.Policy = &policy
			test.mutate(&candidate, &localFacts)
			decision := EvaluateObservation(candidate, localFacts, now)
			assertObservationDecision(t, decision, test.status, test.quality, []QualityReason{test.reason}, true, test.replace)
			if test.status == ObservationRejected && !decision.ReevaluateSnapshot {
				t.Fatal("mapped rejected evidence must reevaluate rejected-only Snapshot state")
			}
		})
	}
}

func TestEvaluateHistoricalObservationPreservesCurrentTruth(t *testing.T) {
	now := time.Date(2026, 8, 28, 15, 30, 0, 0, time.UTC)
	candidate := validObservationCandidate(now)
	candidate.SourcePath = SourcePathHistoryReplay
	candidate.SampledAt = now.Add(-30 * 24 * time.Hour)
	facts := validObservationFacts()
	latest := now.Add(-time.Minute)
	facts.LatestSampledAt = &latest

	decision := EvaluateHistoricalObservation(candidate, facts, now)
	assertObservationDecision(t, decision, ObservationAccepted, QualityGood, nil, true, false)
	if decision.DeviceID != deviceA || decision.EmitPresenceSignal || decision.ReevaluateSnapshot {
		t.Fatalf("historical replay mutated Current semantics: %#v", decision)
	}

	candidate.SampledAt = now.Add(31 * time.Second)
	decision = EvaluateHistoricalObservation(candidate, facts, now)
	assertObservationDecision(t, decision, ObservationRejected, QualityInvalid, []QualityReason{QualityReasonClockAhead}, true, false)
	if decision.EmitPresenceSignal || decision.ReevaluateSnapshot {
		t.Fatalf("rejected historical replay mutated Current semantics: %#v", decision)
	}
}

func TestEvaluateObservationAcceptsGoodCandidate(t *testing.T) {
	now := time.Date(2026, 7, 24, 2, 0, 0, 0, time.UTC)
	decision := EvaluateObservation(validObservationCandidate(now), validObservationFacts(), now)
	assertObservationDecision(t, decision, ObservationAccepted, QualityGood, nil, true, true)
	if decision.DeviceID != deviceA || !decision.EmitPresenceSignal || !decision.ReevaluateSnapshot {
		t.Fatalf("decision=%#v", decision)
	}
}

func TestEvaluateObservationSuppressesPresenceSignalForCopiedReadings(t *testing.T) {
	now := time.Date(2026, 7, 24, 2, 0, 0, 0, time.UTC)
	facts := validObservationFacts()
	policy := *facts.Policy
	policy.QualityChecks = &QualityChecks{Copied: true}
	facts.Policy = &policy

	copied := EvaluateObservation(validObservationCandidate(now), facts, now)
	assertObservationDecision(t, copied, ObservationAccepted, QualityGood, nil, true, true)
	if copied.EmitPresenceSignal {
		t.Fatalf("copied observation emitted presence signal: %#v", copied)
	}
	if !copied.ReevaluateSnapshot {
		t.Fatalf("copied observation must still reevaluate snapshot: %#v", copied)
	}

	ownPolicy := *facts.Policy
	ownPolicy.QualityChecks = &QualityChecks{Copied: false}
	facts.Policy = &ownPolicy
	own := EvaluateObservation(validObservationCandidate(now), facts, now)
	if !own.EmitPresenceSignal {
		t.Fatalf("own observation must emit presence signal: %#v", own)
	}
}

func assertObservationDecision(t *testing.T, decision ObservationDecision, status ObservationStatus, quality ObservationQuality, reasons []QualityReason, advance, replace bool) {
	t.Helper()
	if decision.Status != status || decision.Quality != quality || decision.AdvancePosition != advance || decision.ReplaceLatest != replace {
		t.Fatalf("decision=%#v", decision)
	}
	if len(decision.QualityReasons) != len(reasons) {
		t.Fatalf("reasons=%#v", decision.QualityReasons)
	}
	for index := range reasons {
		if decision.QualityReasons[index] != reasons[index] {
			t.Fatalf("reasons=%#v", decision.QualityReasons)
		}
	}
}

func validObservationCandidate(now time.Time) ObservationCandidate {
	unit := "Cel"
	return ObservationCandidate{
		SourceID:           sourceA,
		SourcePath:         SourcePathWebhook,
		ExternalEntityType: "DEVICE",
		ExternalID:         "CHILLER-01",
		TelemetryKey:       "zone.temperature",
		Value:              json.RawMessage(`23.5`),
		ValueType:          "NUMBER",
		Unit:               &unit,
		SampledAt:          now.Add(-5 * time.Second),
		ReceivedAt:         now,
		Position: SourcePosition{
			Partition: "tb-telemetry-0",
			Offset:    100,
			EventID:   eventA,
		},
	}
}

func validObservationFacts() ObservationFacts {
	unit := "Cel"
	sensorID := "018f2e00-3200-7000-8000-000000000001"
	minimum, maximum := -50.0, 100.0
	return ObservationFacts{
		Device: &ResolvedDevice{TenantID: ingestTenantA, SiteID: siteA, DeviceID: deviceA},
		Point: &ResolvedPoint{
			PointID: "018f2e00-3100-7000-8000-000000000001", SensorID: &sensorID,
			PointType: "TELEMETRY", ValueType: "NUMBER", Unit: &unit, PointRevision: 1,
		},
		Policy: &ObservationPolicy{
			Revision:           5,
			ValueType:          "NUMBER",
			Unit:               &unit,
			MinimumNumber:      &minimum,
			MaximumNumber:      &maximum,
			MaxFutureClockSkew: 30 * time.Second,
			MaxSourceLag:       10 * time.Minute,
		},
	}
}

func TestCounterSemanticsAreSnapshottedIntoHistory(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	candidate := validObservationCandidate(now)
	facts := validObservationFacts()
	mode := "ROLLOVER"
	modulus := 10000.0
	facts.Point.PointType = "COUNTER"
	facts.Point.CounterDecreaseMode = &mode
	facts.Point.CounterRolloverModulus = &modulus
	facts.Point.PointRevision = 7

	decision := EvaluateObservation(candidate, facts, now)
	if decision.Status != ObservationAccepted || decision.PointType != "COUNTER" || decision.PointRevision != 7 {
		t.Fatalf("decision=%#v", decision)
	}
	if decision.CounterDecreaseMode != mode || decision.CounterRolloverModulus == nil || *decision.CounterRolloverModulus != modulus {
		t.Fatalf("counter decision semantics=%#v", decision)
	}

	observation, err := buildHistoryObservation(
		"018f2e00-9100-7000-8000-000000000010",
		candidate,
		decision,
		strings.Repeat("a", 64),
	)
	if err != nil {
		t.Fatal(err)
	}
	if observation.PointType == nil || *observation.PointType != "COUNTER" || observation.PointRevision == nil || *observation.PointRevision != 7 {
		t.Fatalf("history point semantics=%#v", observation)
	}
	if observation.CounterDecreaseMode == nil || *observation.CounterDecreaseMode != mode || observation.CounterRolloverModulus == nil || *observation.CounterRolloverModulus != modulus {
		t.Fatalf("history counter semantics=%#v", observation)
	}
}

func TestEvaluateObservationTracksNonNumericUnchangedSince(t *testing.T) {
	t0 := time.Date(2026, 10, 27, 8, 0, 0, 0, time.UTC)
	candidate := validObservationCandidate(t0.Add(5 * time.Second))
	candidate.TelemetryKey = "run_state"
	candidate.ValueType = "STRING"
	candidate.Unit = nil
	candidate.Value = json.RawMessage(`"RUNNING"`)
	candidate.SampledAt = t0

	facts := validObservationFacts()
	facts.Point.PointType = "STATE"
	facts.Point.ValueType = "STRING"
	facts.Point.Unit = nil
	facts.Policy.ValueType = "STRING"
	facts.Policy.Unit = nil
	facts.Policy.MinimumNumber = nil
	facts.Policy.MaximumNumber = nil

	first := EvaluateObservation(candidate, facts, candidate.ReceivedAt)
	if first.Status != ObservationAccepted || !first.UnchangedSince.Equal(t0) {
		t.Fatalf("initial run_state decision=%#v", first)
	}

	// Repeating "RUNNING" preserves the earlier unchanged_since.
	t1 := t0.Add(5 * time.Minute)
	repeat := candidate
	repeat.SampledAt = t1
	repeat.ReceivedAt = t1.Add(time.Second)
	repeatFacts := facts
	repeatFacts.LatestSampledAt = &t0
	repeatFacts.PreviousSample = &PriorSample{Value: json.RawMessage(`"RUNNING"`), SampledAt: t0, UnchangedSince: t0}
	held := EvaluateObservation(repeat, repeatFacts, repeat.ReceivedAt)
	if held.Status != ObservationAccepted || !held.UnchangedSince.Equal(t0) {
		t.Fatalf("held run_state unchanged_since=%s, want %s", held.UnchangedSince, t0)
	}

	// Missing unchanged_since on prior row falls back to prior SampledAt.
	repeatFacts.PreviousSample = &PriorSample{Value: json.RawMessage(`"RUNNING"`), SampledAt: t0}
	fallback := EvaluateObservation(repeat, repeatFacts, repeat.ReceivedAt)
	if fallback.Status != ObservationAccepted || !fallback.UnchangedSince.Equal(t0) {
		t.Fatalf("fallback run_state unchanged_since=%s, want %s", fallback.UnchangedSince, t0)
	}

	// Transitioning to "STOPPED" resets unchanged_since to the transition timestamp.
	t2 := t0.Add(10 * time.Minute)
	stopped := repeat
	stopped.Value = json.RawMessage(`"STOPPED"`)
	stopped.SampledAt = t2
	stopped.ReceivedAt = t2.Add(time.Second)
	stoppedFacts := repeatFacts
	stoppedFacts.LatestSampledAt = &t1
	stoppedFacts.PreviousSample = &PriorSample{Value: json.RawMessage(`"RUNNING"`), SampledAt: t1, UnchangedSince: t0}
	changed := EvaluateObservation(stopped, stoppedFacts, stopped.ReceivedAt)
	if changed.Status != ObservationAccepted || !changed.UnchangedSince.Equal(t2) {
		t.Fatalf("changed run_state unchanged_since=%s, want %s", changed.UnchangedSince, t2)
	}
}

func TestEvaluateObservationSourceUntrustedSkipsFlatlineAndRecordsRejectedHealth(t *testing.T) {
	t0 := time.Date(2026, 10, 27, 8, 0, 0, 0, time.UTC)
	at := t0.Add(5 * time.Hour)

	candidate := validObservationCandidate(at.Add(time.Second))
	candidate.SampledAt = at
	candidate.WireQuality = 1
	candidate.Value = json.RawMessage(`23.5`)

	facts := validObservationFacts()
	facts.LatestSampledAt = &t0
	facts.Previous = &PriorNumeric{Value: 23.5, SampledAt: t0, UnchangedSince: t0}
	facts.PreviousSample = &PriorSample{Value: json.RawMessage(`23.5`), SampledAt: t0, UnchangedSince: t0}
	facts.Policy.QualityChecks = &QualityChecks{Flatline: 3 * time.Hour}

	accepted := EvaluateObservation(candidate, facts, candidate.ReceivedAt)
	assertObservationDecision(t, accepted, ObservationAccepted, QualityPartial, []QualityReason{QualityReasonSourceUntrusted}, true, true)
	if !accepted.RecordHealth || accepted.HealthStatus != HealthSuspect {
		t.Fatalf("untrusted accepted health=%#v", accepted)
	}

	// A wire-untrusted sample that is also rejected (type mismatch) must record BAD point health.
	rejected := candidate
	rejected.Value = json.RawMessage(`"bad"`)
	rejectedDecision := EvaluateObservation(rejected, facts, rejected.ReceivedAt)
	assertObservationDecision(t, rejectedDecision, ObservationRejected, QualityInvalid, []QualityReason{QualityReasonSourceUntrusted, QualityReasonTypeMismatch}, true, false)
	if !rejectedDecision.RecordHealth || rejectedDecision.HealthStatus != HealthBad || rejectedDecision.HealthScore != 0 {
		t.Fatalf("rejected untrusted candidate must record BAD health: %#v", rejectedDecision)
	}
	if len(rejectedDecision.HealthReasons) != 2 || rejectedDecision.HealthReasons[0] != QualityReasonSourceUntrusted || rejectedDecision.HealthReasons[1] != QualityReasonTypeMismatch {
		t.Fatalf("rejected untrusted health reasons=%#v", rejectedDecision.HealthReasons)
	}

	// Older rejected untrusted samples must not overwrite newer point health.
	older := rejected
	older.SampledAt = t0.Add(-time.Minute)
	olderDecision := EvaluateObservation(older, facts, rejected.ReceivedAt)
	if olderDecision.RecordHealth {
		t.Fatalf("older rejected untrusted candidate must not record health: %#v", olderDecision)
	}
}

func TestPostgresStoreBatchBoundaryValidation(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	store := &PostgresStore{}
	live := validObservationCandidate(now)

	if _, err := store.AcceptObservationBatch(t.Context(), nil); err == nil {
		t.Fatal("expected empty live batch rejection")
	}
	if _, err := store.AcceptObservationBatch(t.Context(), make([]ObservationCandidate, MaxObservationBatchSize+1)); err == nil {
		t.Fatal("expected oversized live batch rejection")
	}
	replay := live
	replay.SourcePath = SourcePathHistoryReplay
	if _, err := store.AcceptObservationBatch(t.Context(), []ObservationCandidate{live, replay}); err == nil {
		t.Fatal("expected HISTORY_REPLAY rejection in live batch")
	}

	if _, err := store.AcceptHistoricalObservationBatch(t.Context(), nil); err == nil {
		t.Fatal("expected empty historical batch rejection")
	}
	if _, err := store.AcceptHistoricalObservationBatch(t.Context(), make([]ObservationCandidate, MaxObservationBatchSize+1)); err == nil {
		t.Fatal("expected oversized historical batch rejection")
	}
	if _, err := store.AcceptHistoricalObservationBatch(t.Context(), []ObservationCandidate{live}); err == nil {
		t.Fatal("expected non-HISTORY_REPLAY rejection in historical batch")
	}
	badEntity := replay
	badEntity.ExternalEntityType = "ASSET"
	if _, err := store.AcceptHistoricalObservationBatch(t.Context(), []ObservationCandidate{badEntity}); err == nil {
		t.Fatal("expected non-DEVICE rejection in historical batch")
	}
	withDevice := replay
	withDevice.Device = &ResolvedDevice{TenantID: ingestTenantA, SiteID: siteA, DeviceID: deviceA}
	if _, err := store.AcceptHistoricalObservationBatch(t.Context(), []ObservationCandidate{withDevice}); err == nil {
		t.Fatal("expected caller-supplied Device rejection in historical batch")
	}
	withPoint := replay
	withPoint.Point = &ResolvedPoint{PointID: "018f2e00-3100-7000-8000-000000000001", PointType: "TELEMETRY", ValueType: "NUMBER", PointRevision: 1}
	if _, err := store.AcceptHistoricalObservationBatch(t.Context(), []ObservationCandidate{withPoint}); err == nil {
		t.Fatal("expected caller-supplied Point rejection in historical batch")
	}
}

func TestCollectBatchLockKeysAndEvaluateBatchCandidatesInMemory(t *testing.T) {
	t0 := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	unitCel := "Cel"
	dev := ResolvedDevice{TenantID: ingestTenantA, SiteID: siteA, DeviceID: deviceA}
	ptRun := ResolvedPoint{PointID: "018f2e00-3100-7000-8000-000000000010", PointType: "STATE", ValueType: "STRING", PointRevision: 1}
	ptTemp := ResolvedPoint{PointID: "018f2e00-3100-7000-8000-000000000011", PointType: "TELEMETRY", ValueType: "NUMBER", Unit: &unitCel, PointRevision: 1}
	ptTempV2 := ResolvedPoint{PointID: "018f2e00-3100-7000-8000-000000000011", PointType: "TELEMETRY", ValueType: "NUMBER", Unit: &unitCel, PointRevision: 2}
	ptCool := ResolvedPoint{PointID: "018f2e00-3100-7000-8000-000000000012", PointType: "TELEMETRY", ValueType: "NUMBER", Unit: &unitCel, PointRevision: 1}

	c0 := ObservationCandidate{
		SourceID: sourceA, SourcePath: SourcePathPush, ExternalEntityType: "DEVICE", ExternalID: "dev-a",
		Device: &dev, Point: &ptRun, TelemetryKey: "run_state",
		Value: json.RawMessage(`"RUNNING"`), ValueType: "STRING",
		SampledAt: t0, ReceivedAt: t0, Position: SourcePosition{Partition: "p0", Offset: 1, EventID: "018f2e00-8000-7000-8000-000000000001"},
	}
	c1 := ObservationCandidate{
		SourceID: sourceA, SourcePath: SourcePathPush, ExternalEntityType: "DEVICE", ExternalID: "dev-a",
		Device: &dev, Point: &ptTemp, TelemetryKey: "zone.temperature",
		Value: json.RawMessage(`24.0`), ValueType: "NUMBER", Unit: &unitCel,
		SampledAt: t0.Add(time.Second), ReceivedAt: t0.Add(time.Second),
		Position: SourcePosition{Partition: "p0", Offset: 2, EventID: "018f2e00-8000-7000-8000-000000000002"},
	}
	c2 := ObservationCandidate{
		SourceID: sourceA, SourcePath: SourcePathPush, ExternalEntityType: "DEVICE", ExternalID: "dev-a",
		Device: &dev, Point: &ptTempV2, TelemetryKey: "zone.temperature",
		Value: json.RawMessage(`500.0`), ValueType: "NUMBER", Unit: &unitCel,
		SampledAt: t0.Add(2 * time.Second), ReceivedAt: t0.Add(2 * time.Second),
		Position: SourcePosition{Partition: "p0", Offset: 3, EventID: "018f2e00-8000-7000-8000-000000000003"},
	}
	c3 := ObservationCandidate{
		SourceID: sourceA, SourcePath: SourcePathPush, ExternalEntityType: "DEVICE", ExternalID: "dev-a",
		Device: &dev, Point: &ptCool, TelemetryKey: "derived.temp",
		Value: json.RawMessage(`10.0`), ValueType: "NUMBER", Unit: &unitCel,
		SampledAt: t0.Add(3 * time.Second), ReceivedAt: t0.Add(3 * time.Second),
		Position: SourcePosition{Partition: "p0", Offset: 4, EventID: "018f2e00-8000-7000-8000-000000000004"},
	}
	c4Dup := c1
	c4Dup.Position.Offset = 5
	c5Unmapped := ObservationCandidate{
		SourceID: sourceA, SourcePath: SourcePathPush, ExternalEntityType: "DEVICE", ExternalID: "missing-dev",
		TelemetryKey: "zone.temperature", Value: json.RawMessage(`20.0`), ValueType: "NUMBER", Unit: &unitCel,
		SampledAt: t0.Add(4 * time.Second), ReceivedAt: t0.Add(4 * time.Second),
		Position: SourcePosition{Partition: "p0", Offset: 6, EventID: "018f2e00-8000-7000-8000-000000000005"},
	}

	candidates := []ObservationCandidate{c0, c1, c2, c3, c4Dup, c5Unmapped}
	lockKeys := collectBatchLockKeys(candidates)
	if len(lockKeys) != 7 {
		t.Fatalf("expected 7 deduplicated lock keys (1 partition + 5 events + 1 device), got %d: %#v", len(lockKeys), lockKeys)
	}

	minTemp, maxTemp := -50.0, 100.0
	state := &batchWorkingState{
		seenEvents:    map[sourceEventKey]string{},
		positions:     map[sourcePartitionKey]SourcePositionHead{},
		knownDevices:  map[string]ResolvedDevice{},
		knownPoints:   map[deviceKey]ResolvedPoint{},
		latestByPoint: map[deviceKey]latestPointState{},
		policies: map[deviceKey]loadedPolicy{
			{deviceID: deviceA, key: "run_state"}: {
				policy: ObservationPolicy{Revision: 1, PresencePolicyRevision: 1, ValueType: "STRING", MaxFutureClockSkew: 30 * time.Second, MaxSourceLag: 10 * time.Minute},
			},
			{deviceID: deviceA, key: "zone.temperature"}: {
				policy: ObservationPolicy{Revision: 1, PresencePolicyRevision: 1, ValueType: "NUMBER", Unit: &unitCel, MinimumNumber: &minTemp, MaximumNumber: &maxTemp, MaxFutureClockSkew: 30 * time.Second, MaxSourceLag: 10 * time.Minute},
			},
			{deviceID: deviceA, key: "derived.temp"}: {
				policy: ObservationPolicy{
					Revision: 1, PresencePolicyRevision: 1, ValueType: "NUMBER", Unit: &unitCel,
					MaxFutureClockSkew: 30 * time.Second, MaxSourceLag: 10 * time.Minute,
					QualityChecks: &QualityChecks{
						Running: []OperatingExpect{{DeviceID: deviceA, Key: "run_state", Equals: "RUNNING"}},
						Inputs:  []PointRef{{DeviceID: deviceA, Key: "zone.temperature"}},
					},
				},
			},
		},
	}

	payloadSHAs := make([]string, len(candidates))
	for index, candidate := range candidates {
		sha, err := observationPayloadSHA(candidate)
		if err != nil {
			t.Fatal(err)
		}
		payloadSHAs[index] = sha
	}
	seq := 0
	genID := func(time.Time) (string, error) {
		seq++
		return ingestEvent(seq), nil
	}

	receipts, plan, err := evaluateBatchCandidates(candidates, payloadSHAs, state, EvaluateObservation, genID)
	if err != nil {
		t.Fatal(err)
	}
	if receipts[0].Status != ObservationAccepted || receipts[1].Status != ObservationAccepted ||
		receipts[2].Status != ObservationRejected || receipts[3].Status != ObservationAccepted ||
		receipts[3].Quality != QualityPartial || len(receipts[3].QualityReasons) != 1 || receipts[3].QualityReasons[0] != QualityReasonDerivedInput ||
		receipts[4].Status != ObservationDuplicate || receipts[4].ObservationID != receipts[1].ObservationID ||
		receipts[5].Status != ObservationQuarantined {
		t.Fatalf("unexpected batch receipts: %#v", receipts)
	}
	if len(plan.devices) != 1 || len(plan.positions) != 1 || plan.positions[0].candidate.Position.Offset != 6 {
		t.Fatalf("unexpected deduped devices/positions: devices=%d positions=%#v", len(plan.devices), plan.positions)
	}
	if len(plan.points) != 3 {
		t.Fatalf("expected 3 deduped points, got %d", len(plan.points))
	}
	for _, pt := range plan.points {
		if pt.key == "zone.temperature" && pt.point.PointRevision != 2 {
			t.Fatalf("expected last point revision kept to prevent 21000, got %d", pt.point.PointRevision)
		}
	}
	if len(plan.latestUpdates) != 4 || !plan.latestUpdates[0].replaceLatest || !plan.latestUpdates[1].replaceLatest || plan.latestUpdates[2].replaceLatest || !plan.latestUpdates[3].replaceLatest {
		t.Fatalf("unexpected ordered latest updates: %#v", plan.latestUpdates)
	}
	if len(plan.reevalDeviceOrder) != 1 || plan.reevalDeviceOrder[0] != deviceA || !plan.maxReceivedAtByDevice[deviceA].Equal(t0.Add(3*time.Second)) {
		t.Fatalf("unexpected reeval plan: order=%v max=%v", plan.reevalDeviceOrder, plan.maxReceivedAtByDevice)
	}
}

func TestEvaluateEdgeReplayObservationIsolatesSnapshotAndIgnoresSourceLag(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	minVal, maxVal := -20.0, 50.0
	maxStep := 1.0
	unitCel := "Cel"
	dev := ResolvedDevice{TenantID: ingestTenantA, SiteID: siteA, DeviceID: deviceA}
	pt := ResolvedPoint{PointID: "018f2e00-3100-7000-8000-000000000001", PointType: "TELEMETRY", ValueType: "NUMBER", Unit: &unitCel, PointRevision: 1}
	priorTime := now.Add(-time.Minute)
	facts := ObservationFacts{
		Device:          &dev,
		Point:           &pt,
		LatestSampledAt: &priorTime,
		Previous:        &PriorNumeric{Value: 20.0, SampledAt: priorTime, UnchangedSince: now.Add(-10 * time.Hour)},
		InputsUntrusted: true,
		Policy: &ObservationPolicy{
			Revision: 2, PresencePolicyRevision: 3, ValueType: "NUMBER", Unit: &unitCel,
			MinimumNumber: &minVal, MaximumNumber: &maxVal,
			MaxFutureClockSkew: 30 * time.Second, MaxSourceLag: time.Minute,
			QualityChecks: &QualityChecks{
				MaxStep:   &maxStep,
				Sentinels: []float64{38.4},
				Flatline:  time.Minute,
				Inputs:    []PointRef{{DeviceID: deviceA, Key: "peer"}},
			},
		},
	}

	laggedCandidate := ObservationCandidate{
		SourceID: sourceA, SourcePath: SourcePathPush, ExternalEntityType: "DEVICE", ExternalID: "dev-a",
		Device: &dev, Point: &pt, TelemetryKey: "zone.temperature",
		Value: json.RawMessage(`26.5`), ValueType: "NUMBER", Unit: &unitCel,
		SampledAt: now.Add(-72 * time.Hour), ReceivedAt: now,
		Position:   SourcePosition{Partition: "p0", Offset: 10, EventID: eventA},
		EdgeReplay: true,
	}
	decision := EvaluateHistoricalObservation(laggedCandidate, facts, now)
	if decision.Status != ObservationAccepted || decision.Quality != QualityGood || len(decision.QualityReasons) != 0 {
		t.Fatalf("lagged edge replay degraded unexpectedly: %+v", decision)
	}
	if !decision.AdvancePosition || decision.ReplaceLatest || decision.ReevaluateSnapshot || decision.EmitPresenceSignal || decision.RecordHealth {
		t.Fatalf("edge replay mutated snapshot/latest/presence flags: %+v", decision)
	}

	sentinelCandidate := laggedCandidate
	sentinelCandidate.Value = json.RawMessage(`38.4`)
	sentinelDecision := EvaluateHistoricalObservation(sentinelCandidate, facts, now)
	if sentinelDecision.Status != ObservationAccepted || sentinelDecision.Quality != QualityPartial {
		t.Fatalf("sentinel edge replay expected ACCEPTED/PARTIAL, got %+v", sentinelDecision)
	}
	if sentinelDecision.ReplaceLatest || sentinelDecision.ReevaluateSnapshot || sentinelDecision.EmitPresenceSignal {
		t.Fatalf("sentinel edge replay mutated snapshot flags: %+v", sentinelDecision)
	}

	outOfRangeCandidate := laggedCandidate
	outOfRangeCandidate.Value = json.RawMessage(`150.0`)
	rejectedDecision := EvaluateHistoricalObservation(outOfRangeCandidate, facts, now)
	if rejectedDecision.Status != ObservationRejected || rejectedDecision.Quality != QualityInvalid {
		t.Fatalf("out-of-range edge replay expected REJECTED/INVALID, got %+v", rejectedDecision)
	}
}

func TestEvaluateBatchCandidatesIsolatesEdgeReplay(t *testing.T) {
	t0 := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	unitKWh := "kWh"
	dev := ResolvedDevice{TenantID: ingestTenantA, SiteID: siteA, DeviceID: deviceA}
	ptReplay := ResolvedPoint{PointID: "018f2e00-3100-7000-8000-000000000001", PointType: "COUNTER", ValueType: "NUMBER", Unit: &unitKWh, CounterDecreaseMode: stringPointer("RESET_TO_ZERO"), PointRevision: 1}
	ptLive := ResolvedPoint{PointID: "018f2e00-3100-7000-8000-000000000001", PointType: "COUNTER", ValueType: "NUMBER", Unit: &unitKWh, CounterDecreaseMode: stringPointer("RESET_TO_ZERO"), PointRevision: 2}

	replayCandidate := ObservationCandidate{
		SourceID: sourceA, SourcePath: SourcePathPush, ExternalEntityType: "DEVICE", ExternalID: "dev-a",
		Device: &dev, Point: &ptReplay, TelemetryKey: "energy.total",
		Value: json.RawMessage(`102.5`), ValueType: "NUMBER", Unit: &unitKWh,
		SampledAt: t0.Add(-48 * time.Hour), ReceivedAt: t0,
		Position:   SourcePosition{Partition: "p0", Offset: 1, EventID: ingestEvent(1)},
		EdgeReplay: true,
	}
	liveCandidate := ObservationCandidate{
		SourceID: sourceA, SourcePath: SourcePathPush, ExternalEntityType: "DEVICE", ExternalID: "dev-a",
		Device: &dev, Point: &ptLive, TelemetryKey: "energy.total",
		Value: json.RawMessage(`205.0`), ValueType: "NUMBER", Unit: &unitKWh,
		SampledAt: t0, ReceivedAt: t0,
		Position: SourcePosition{Partition: "p0", Offset: 2, EventID: ingestEvent(2)},
	}

	candidates := []ObservationCandidate{replayCandidate, liveCandidate}
	state := &batchWorkingState{
		seenEvents:    map[sourceEventKey]string{},
		positions:     map[sourcePartitionKey]SourcePositionHead{},
		knownDevices:  map[string]ResolvedDevice{deviceA: dev},
		knownPoints:   map[deviceKey]ResolvedPoint{},
		latestByPoint: map[deviceKey]latestPointState{},
		policies: map[deviceKey]loadedPolicy{
			{deviceID: deviceA, key: "energy.total"}: {
				policy: ObservationPolicy{Revision: 1, PresencePolicyRevision: 1, ValueType: "NUMBER", Unit: &unitKWh, MaxFutureClockSkew: 30 * time.Second, MaxSourceLag: time.Minute},
			},
		},
	}
	payloadSHAs := []string{strings.Repeat("a", 64), strings.Repeat("b", 64)}
	receiptsOnlyReplay, planOnlyReplay, err := evaluateBatchCandidates([]ObservationCandidate{replayCandidate}, payloadSHAs[:1], state, EvaluateObservation, func(time.Time) (string, error) {
		return ingestEvent(100), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if receiptsOnlyReplay[0].Status != ObservationAccepted || receiptsOnlyReplay[0].Quality != QualityGood {
		t.Fatalf("unexpected replay-only receipt: %#v", receiptsOnlyReplay)
	}
	if len(planOnlyReplay.devices) != 0 || len(planOnlyReplay.points) != 0 ||
		len(planOnlyReplay.latestUpdates) != 0 || len(planOnlyReplay.presenceSignals) != 0 ||
		len(planOnlyReplay.reevalDeviceOrder) != 0 {
		t.Fatalf("pure edge replay batch mutated non-history tables: %+v", planOnlyReplay)
	}
	if len(planOnlyReplay.historyOutbox) != 1 || len(planOnlyReplay.observations) != 1 || len(planOnlyReplay.positions) != 1 {
		t.Fatalf("pure edge replay batch missing history/observation/position rows: %+v", planOnlyReplay)
	}
	state.seenEvents = map[sourceEventKey]string{}
	state.positions = map[sourcePartitionKey]SourcePositionHead{}
	state.seenPointSamples = map[pointSampleKey]string{}

	seq := 0
	receipts, plan, err := evaluateBatchCandidates(candidates, payloadSHAs, state, EvaluateObservation, func(time.Time) (string, error) {
		seq++
		return ingestEvent(100 + seq), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if receipts[0].Status != ObservationAccepted || receipts[0].Quality != QualityGood || receipts[1].Status != ObservationAccepted {
		t.Fatalf("unexpected receipts: %#v", receipts)
	}
	if len(plan.devices) != 1 {
		t.Fatalf("expected only live candidate to touch devices, got %d", len(plan.devices))
	}
	if len(plan.points) != 1 || plan.points[0].point.PointRevision != 2 {
		t.Fatalf("edge replay polluted points metadata: %#v", plan.points)
	}
	if len(plan.historyOutbox) != 2 || len(plan.observations) != 2 || len(plan.positions) != 1 {
		t.Fatalf("unexpected outbox/observation/position counts: outbox=%d obs=%d pos=%d", len(plan.historyOutbox), len(plan.observations), len(plan.positions))
	}
	if len(plan.presenceSignals) != 1 || len(plan.latestUpdates) != 1 || len(plan.reevalDeviceOrder) != 1 {
		t.Fatalf("edge replay mutated presence/latest/snapshot plans: presence=%d latest=%d reeval=%d", len(plan.presenceSignals), len(plan.latestUpdates), len(plan.reevalDeviceOrder))
	}
}

func TestEvaluateObservationRejectsPhysicalBoundsAndRegisterSentinels(t *testing.T) {
	now := time.Date(2026, 7, 24, 2, 0, 0, 0, time.UTC)

	makeCase := func(unit, pointType, rawVal string) (ObservationCandidate, ObservationFacts) {
		c := validObservationCandidate(now)
		c.Unit = &unit
		c.Value = json.RawMessage(rawVal)
		f := validObservationFacts()
		f.Point.PointType = pointType
		f.Point.Unit = &unit
		f.Policy.Unit = &unit
		f.Policy.MinimumNumber = nil
		f.Policy.MaximumNumber = nil
		return c, f
	}

	sentinelCand, sentinelFacts := makeCase("Cel", "TELEMETRY", `65535`)
	sentinelDec := EvaluateObservation(sentinelCand, sentinelFacts, now)
	assertObservationDecision(t, sentinelDec, ObservationRejected, QualityInvalid, []QualityReason{QualityReasonOutOfRange, QualityReasonSentinel}, true, false)
	if !sentinelDec.RecordHealth || sentinelDec.HealthStatus != HealthBad || sentinelDec.HealthScore != 0 {
		t.Fatalf("sentinel health=%#v", sentinelDec)
	}

	if _, err := canonicalQualityReasons(healthReasonCodes(sentinelDec.QualityReasons)); err != nil {
		t.Fatalf("returned quality reasons rejected by canonical contract: %v", err)
	}

	for _, tc := range []struct {
		unit, val string
	}{
		{"%RH", `-5`},
		{"Hz", `150`},
		{"L/s", `35000`},
		{"Cel", `250`},
	} {
		cand, facts := makeCase(tc.unit, "TELEMETRY", tc.val)
		dec := EvaluateObservation(cand, facts, now)
		assertObservationDecision(t, dec, ObservationRejected, QualityInvalid, []QualityReason{QualityReasonOutOfRange}, true, false)
	}

	paCand, paFacts := makeCase("Pa", "TELEMETRY", `32767`)
	paDec := EvaluateObservation(paCand, paFacts, now)
	assertObservationDecision(t, paDec, ObservationAccepted, QualityGood, nil, true, true)

	sentinelCand.SourcePath = SourcePathHistoryReplay
	sentinelCand.SampledAt = now.Add(-48 * time.Hour)
	histDec := EvaluateHistoricalObservation(sentinelCand, sentinelFacts, now)
	assertObservationDecision(t, histDec, ObservationRejected, QualityInvalid, []QualityReason{QualityReasonOutOfRange, QualityReasonSentinel}, true, false)
}

func TestEvaluateObservationDeduplicatesAlreadyAcceptedSampleTimestamp(t *testing.T) {
	now := time.Date(2026, 7, 24, 2, 0, 0, 0, time.UTC)
	candidate := validObservationCandidate(now)
	facts := validObservationFacts()
	facts.SampleAlreadySeen = true

	liveDec := EvaluateObservation(candidate, facts, now)
	assertObservationDecision(t, liveDec, ObservationDuplicate, QualityInvalid, []QualityReason{QualityReasonDuplicate}, false, false)
	if liveDec.ReevaluateSnapshot || liveDec.EmitPresenceSignal || liveDec.PointID == "" {
		t.Fatalf("duplicate sample decision mutated snapshot/presence or omitted point: %#v", liveDec)
	}

	candidate.SourcePath = SourcePathHistoryReplay
	candidate.SampledAt = now.Add(-2 * time.Hour)
	histDec := EvaluateHistoricalObservation(candidate, facts, now)
	assertObservationDecision(t, histDec, ObservationDuplicate, QualityInvalid, []QualityReason{QualityReasonDuplicate}, false, false)
}

func TestPlanBatchPrefetchNilCacheInitializesCheckedPointSamples(t *testing.T) {
	now := time.Date(2026, 7, 24, 2, 0, 0, 0, time.UTC)
	dev := ResolvedDevice{TenantID: ingestTenantA, SiteID: siteA, DeviceID: deviceA}
	pt := ResolvedPoint{PointID: "018f2e00-3100-7000-8000-000000000001"}
	cand := validObservationCandidate(now)
	cand.Device = &dev
	cand.Point = &pt

	state, plan := planBatchPrefetch(nil, []ObservationCandidate{cand})
	if plan.checkedPointSampleSet == nil {
		t.Fatal("expected plan.checkedPointSampleSet to be initialized when metadataCache is nil")
	}
	if len(plan.knownPointSamples) != 1 {
		t.Fatalf("expected 1 known point sample for live candidate, got %d", len(plan.knownPointSamples))
	}

	replayCand := cand
	replayCand.SourcePath = SourcePathHistoryReplay
	replayCand.ExternalID = deviceA
	replayCand.Device = nil
	replayCand.Point = nil
	replayCand.SampledAt = now.Add(time.Minute)
	state.knownPoints[deviceKey{deviceID: deviceA, key: replayCand.TelemetryKey}] = ResolvedPoint{
		PointID: "018f2e00-3100-7000-8000-000000000002",
	}
	remaining := collectUncheckedPointSamples([]ObservationCandidate{cand, replayCand}, state, plan.checkedPointSampleSet)
	if len(remaining) != 1 || remaining[0].pointID != "018f2e00-3100-7000-8000-000000000002" {
		t.Fatalf("expected remaining follow-up point sample to resolve without nil map panic, got %+v", remaining)
	}
}
