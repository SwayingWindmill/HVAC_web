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
