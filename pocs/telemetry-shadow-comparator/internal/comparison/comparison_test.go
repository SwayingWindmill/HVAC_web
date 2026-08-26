package comparison

import (
	"encoding/json"
	"testing"

	"github.com/quanlaihe/hvac-web/modules/telemetry/pkg/telemetryapi"
)

func TestComparePromotionEligibleWithClassifiedSemanticDifference(t *testing.T) {
	active := true
	input := Input{
		SchemaVersion:                1,
		RegistryRevision:             9,
		RouteRevision:                3,
		Cohort:                       "shadow-bucket-17",
		ExpectedSampleIntervalMillis: 60_000,
		NumericAbsoluteTolerance:     0.01,
		Mappings:                     []DeviceMapping{{LegacyDeviceID: "legacy-a", DeviceID: "018f2e00-3000-7000-8000-000000000001"}},
		LegacyDevices: []LegacyDevice{{
			LegacyDeviceID: "legacy-a",
			Active:         &active,
			Values: map[string]LegacySample{
				"temperature": {TimestampMillis: 1784937600000, Value: json.RawMessage(`22.5`)},
			},
		}},
		S2Snapshots: []telemetryapi.DeviceObservationSnapshot{snapshotFixture(
			"018f2e00-3000-7000-8000-000000000001",
			"temperature",
			json.RawMessage(`22.505`),
			"2026-07-25T00:00:30.000Z",
			"ONLINE",
			"STALE",
			"AVAILABLE",
		)},
	}

	report, err := Compare(input)
	if err != nil {
		t.Fatal(err)
	}
	if !report.PromotionEligible {
		t.Fatalf("expected promotion-eligible report: %#v", report)
	}
	if report.AcceptedValueAgreementRate != 1 || report.TimestampAgreementRate != 1 {
		t.Fatalf("agreement rates = value %v timestamp %v", report.AcceptedValueAgreementRate, report.TimestampAgreementRate)
	}
	if len(report.SemanticDifferences) != 1 || report.SemanticDifferences[0].Classification != "LEGACY_ACTIVE_COARSE_VS_S2_STALE" || !report.SemanticDifferences[0].Expected {
		t.Fatalf("semantic classification = %#v", report.SemanticDifferences)
	}
	if report.SideEffects.DatabaseWrites != 0 || report.SideEffects.Publications != 0 || report.SideEffects.Subscriptions != 0 || report.SideEffects.TokensMinted != 0 || report.SideEffects.MappingsRepaired != 0 || report.SideEffects.ServingPathUsed {
		t.Fatalf("comparator reported side effects: %#v", report.SideEffects)
	}
}

func TestCompareBlocksMappingMissingExtraAndUnclassifiedDifferences(t *testing.T) {
	active := true
	input := Input{
		SchemaVersion:                1,
		RegistryRevision:             9,
		RouteRevision:                3,
		Cohort:                       "shadow-bucket-18",
		ExpectedSampleIntervalMillis: 60_000,
		Mappings: []DeviceMapping{
			{LegacyDeviceID: "legacy-a", DeviceID: "018f2e00-3000-7000-8000-000000000001"},
			{LegacyDeviceID: "legacy-missing", DeviceID: "018f2e00-3000-7000-8000-000000000002"},
		},
		LegacyDevices: []LegacyDevice{{
			LegacyDeviceID: "legacy-a",
			Active:         &active,
			Values:         map[string]LegacySample{"temperature": {TimestampMillis: 1784937600000, Value: json.RawMessage(`22.5`)}},
		}},
		S2Snapshots: []telemetryapi.DeviceObservationSnapshot{
			snapshotFixture("018f2e00-3000-7000-8000-000000000001", "temperature", json.RawMessage(`23.0`), "2026-07-25T00:03:00.000Z", "OFFLINE", "OFFLINE", "AVAILABLE"),
			snapshotFixture("018f2e00-3000-7000-8000-000000000099", "temperature", json.RawMessage(`20.0`), "2026-07-25T00:00:00.000Z", "ONLINE", "ONLINE", "AVAILABLE"),
		},
	}

	report, err := Compare(input)
	if err != nil {
		t.Fatal(err)
	}
	if report.PromotionEligible {
		t.Fatal("invalid shadow report became promotion eligible")
	}
	if len(report.MappingMismatches) == 0 || len(report.MissingDevices) != 0 || len(report.ExtraDevices) != 1 {
		t.Fatalf("mapping/missing/extra report = %#v %#v %#v", report.MappingMismatches, report.MissingDevices, report.ExtraDevices)
	}
	if len(report.ValueDifferences) == 0 || len(report.TimestampDifferences) == 0 || report.UnclassifiedDifferenceCount != 1 {
		t.Fatalf("difference report = values %#v timestamps %#v unclassified %d", report.ValueDifferences, report.TimestampDifferences, report.UnclassifiedDifferenceCount)
	}
}

func TestCompareBlocksUnmatchedAcceptedValues(t *testing.T) {
	active := true
	input := Input{
		SchemaVersion:                1,
		RegistryRevision:             9,
		RouteRevision:                3,
		Cohort:                       "shadow-bucket-19",
		ExpectedSampleIntervalMillis: 60_000,
		Mappings:                     []DeviceMapping{{LegacyDeviceID: "legacy-a", DeviceID: "018f2e00-3000-7000-8000-000000000001"}},
		LegacyDevices: []LegacyDevice{{
			LegacyDeviceID: "legacy-a",
			Active:         &active,
			Values: map[string]LegacySample{
				"temperature": {TimestampMillis: 1784937600000, Value: json.RawMessage(`22.5`)},
				"humidity":    {TimestampMillis: 1784937600000, Value: json.RawMessage(`45`)},
			},
		}},
		S2Snapshots: []telemetryapi.DeviceObservationSnapshot{snapshotFixture(
			"018f2e00-3000-7000-8000-000000000001",
			"temperature",
			json.RawMessage(`22.5`),
			"2026-07-25T00:00:00.000Z",
			"ONLINE",
			"ONLINE",
			"AVAILABLE",
		)},
	}
	report, err := Compare(input)
	if err != nil {
		t.Fatal(err)
	}
	if report.PromotionEligible || report.UnmatchedAcceptedValues != 1 || report.AcceptedValueAgreementRate != 1 {
		t.Fatalf("unmatched accepted value did not block promotion: %#v", report)
	}
}

func TestCompareRejectsInvalidIdentityAndInterval(t *testing.T) {
	if _, err := Compare(Input{SchemaVersion: 1, RegistryRevision: 1, RouteRevision: 1, Cohort: "cohort"}); err == nil {
		t.Fatal("zero sample interval was accepted")
	}
	if _, err := Compare(Input{SchemaVersion: 2, RegistryRevision: 1, RouteRevision: 1, Cohort: "cohort", ExpectedSampleIntervalMillis: 1}); err == nil {
		t.Fatal("unknown comparison schema was accepted")
	}
}

func snapshotFixture(deviceID, key string, value json.RawMessage, sampledAt, presence, display, availability string) telemetryapi.DeviceObservationSnapshot {
	presenceState := telemetryapi.DevicePresenceState(presence)
	displayState := telemetryapi.DeviceDisplayState(display)
	policyRevision := telemetryapi.PolicyRevision(1)
	return telemetryapi.DeviceObservationSnapshot{
		SchemaVersion:          1,
		DeviceId:               telemetryapi.UUIDv7(deviceID),
		TenantId:               telemetryapi.UUIDv7("018f2e00-1000-7000-8000-000000000001"),
		SiteId:                 telemetryapi.UUIDv7("018f2e00-4000-7000-8000-000000000001"),
		BusinessRevision:       telemetryapi.BusinessRevision(1),
		EvaluatedAt:            telemetryapi.Instant(sampledAt),
		EvaluationAvailability: telemetryapi.EvaluationAvailability(availability),
		AvailabilityReasons:    []telemetryapi.AvailabilityReasonCode{},
		Presence: telemetryapi.PresenceSnapshot{
			Applicability:  telemetryapi.PresenceApplicability("APPLICABLE"),
			CurrentState:   &presenceState,
			LastSeenAt:     instantPointer(sampledAt),
			PolicyRevision: &policyRevision,
		},
		TelemetryReadiness: telemetryapi.TelemetryReadiness("CURRENT"),
		DisplayState:       &displayState,
		Values: []telemetryapi.TelemetryKeyState{{Present: &telemetryapi.TelemetryPresentState{
			Key:            telemetryapi.TelemetryKey(key),
			State:          "PRESENT",
			Value:          value,
			ValueType:      "NUMBER",
			SampledAt:      telemetryapi.Instant(sampledAt),
			ReceivedAt:     telemetryapi.Instant(sampledAt),
			Freshness:      "FRESH",
			Quality:        telemetryapi.TelemetryQuality("GOOD"),
			QualityReasons: []telemetryapi.QualityReasonCode{},
			PolicyRevision: policyRevision,
		}}},
	}
}

func instantPointer(value string) *telemetryapi.Instant {
	instant := telemetryapi.Instant(value)
	return &instant
}
