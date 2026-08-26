package telemetry

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

const (
	ingestTenantA = "018f2d00-0000-7000-8000-000000000001"
	integrationA  = "018f2e00-6000-7000-8000-000000000001"
	eventA        = "018f2e00-8000-7000-8000-000000000011"
)

func TestEvaluateObservationFailsClosedOnMapping(t *testing.T) {
	now := time.Date(2026, 7, 24, 2, 0, 0, 0, time.UTC)
	candidate := validObservationCandidate(now)

	tests := []struct {
		name       string
		bindings   []RuntimeBinding
		status     ObservationStatus
		quarantine QuarantineReason
	}{
		{name: "missing", status: ObservationQuarantined, quarantine: QuarantineMappingNotFound},
		{name: "quarantined", bindings: []RuntimeBinding{bindingWithStatus("QUARANTINED")}, status: ObservationQuarantined, quarantine: QuarantineMappingQuarantined},
		{name: "retired", bindings: []RuntimeBinding{bindingWithStatus("RETIRED")}, status: ObservationQuarantined, quarantine: QuarantineMappingRetired},
		{name: "future validity", bindings: []RuntimeBinding{bindingValidBetween("ACTIVE", now.Add(time.Second), nil)}, status: ObservationQuarantined, quarantine: QuarantineMappingNotFound},
		{name: "expired validity", bindings: []RuntimeBinding{bindingValidBetween("ACTIVE", now.Add(-time.Hour), ptrTime(now))}, status: ObservationQuarantined, quarantine: QuarantineMappingRetired},
		{name: "conflicting active", bindings: []RuntimeBinding{bindingWithStatus("ACTIVE"), bindingWithStatus("ACTIVE")}, status: ObservationQuarantined, quarantine: QuarantineMappingConflict},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision := EvaluateObservation(candidate, ObservationFacts{Bindings: test.bindings}, now)
			if decision.Status != test.status || decision.QuarantineReason != test.quarantine {
				t.Fatalf("decision=%#v", decision)
			}
			if decision.DeviceID != "" || decision.ReplaceLatest || decision.ReevaluateSnapshot {
				t.Fatalf("mapping failure leaked or mutated Device state: %#v", decision)
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

func TestEvaluateObservationAcceptsGoodCandidate(t *testing.T) {
	now := time.Date(2026, 7, 24, 2, 0, 0, 0, time.UTC)
	decision := EvaluateObservation(validObservationCandidate(now), validObservationFacts(), now)
	assertObservationDecision(t, decision, ObservationAccepted, QualityGood, nil, true, true)
	if decision.DeviceID != deviceA || !decision.EmitPresenceSignal || !decision.ReevaluateSnapshot {
		t.Fatalf("decision=%#v", decision)
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
		IntegrationInstanceID: integrationA,
		SourcePath:            SourcePathWebhook,
		ExternalEntityType:    "DEVICE",
		ExternalID:            "tb-device-org-a-site-1",
		TelemetryKey:          "zone.temperature",
		Value:                 json.RawMessage(`23.5`),
		ValueType:             "NUMBER",
		Unit:                  &unit,
		SampledAt:             now.Add(-5 * time.Second),
		ReceivedAt:            now,
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
		Bindings: []RuntimeBinding{bindingWithStatus("ACTIVE")},
		PointBindings: []RuntimePointBinding{{
			TenantID: ingestTenantA, SiteID: siteA,
			PointID: "018f2e00-3100-7000-8000-000000000001", SensorID: &sensorID, DeviceID: deviceA,
			TelemetryKey: "zone.temperature", PointType: "TELEMETRY", ValueType: "NUMBER", Unit: &unit, Status: "ACTIVE", PointRevision: 1,
		}},
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
	facts.PointBindings[0].PointType = "COUNTER"
	facts.PointBindings[0].CounterDecreaseMode = &mode
	facts.PointBindings[0].CounterRolloverModulus = &modulus
	facts.PointBindings[0].PointRevision = 7

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

func bindingWithStatus(status string) RuntimeBinding {
	return bindingValidBetween(status, time.Time{}, nil)
}

func bindingValidBetween(status string, validFrom time.Time, validTo *time.Time) RuntimeBinding {
	return RuntimeBinding{
		TenantID:              ingestTenantA,
		DeviceID:              deviceA,
		SiteID:                siteA,
		IntegrationInstanceID: integrationA,
		ExternalEntityType:    "DEVICE",
		ExternalID:            "tb-device-org-a-site-1",
		Status:                status,
		ValidFrom:             validFrom,
		ValidTo:               validTo,
	}
}
