package telemetry

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/services/telemetry-runtime-service/pkg/telemetryapi"
)

const (
	deviceA = "018f2e00-3000-7000-8000-000000000001"
	orgA    = "018f2e00-0000-7000-8000-000000000001"
	siteA   = "018f2e00-1000-7000-8000-000000000001"
)

func TestEvaluateCanonicalPresenceFreshnessAndQuality(t *testing.T) {
	now := time.Date(2026, 7, 24, 1, 0, 0, 0, time.UTC)
	base := DeviceFacts{
		DeviceID:             deviceA,
		OwningOrganizationID: orgA,
		SiteID:               siteA,
		Applicability:        telemetryapi.PresenceApplicabilityApplicable,
		PresencePolicy: &PresencePolicy{
			Revision: 2, OnlineWithin: time.Minute, OfflineAfter: 3 * time.Minute,
		},
		Coverage: Coverage{Available: true, ContinuousSince: ptrTime(now.Add(-10 * time.Minute))},
		FreshnessPolicies: map[string]FreshnessPolicy{
			"zone.temperature": {Revision: 5, FreshFor: 5 * time.Minute, Configured: true},
			"zone.humidity":    {Revision: 5, FreshFor: 5 * time.Minute, Configured: true},
			"duct.pressure":    {Revision: 5, FreshFor: 2 * time.Minute, Configured: true},
		},
		Latest: map[string]LatestObservation{
			"zone.temperature": {
				Value: json.RawMessage(`23.5`), ValueType: "NUMBER", Unit: ptrString("Cel"),
				SampledAt: now.Add(-30 * time.Second), ReceivedAt: now.Add(-28 * time.Second),
				Quality: telemetryapi.TelemetryQualityGood,
			},
			"zone.humidity": {
				Value: json.RawMessage(`61`), ValueType: "NUMBER", Unit: ptrString("%RH"),
				SampledAt: now.Add(-10 * time.Minute), ReceivedAt: now.Add(-9 * time.Minute),
				Quality:        telemetryapi.TelemetryQualitySuspect,
				QualityReasons: []telemetryapi.QualityReasonCode{telemetryapi.QualityReasonCodeSourceLagExceeded},
			},
		},
		RejectedKeys: map[string]bool{"duct.pressure": true},
	}

	tests := []struct {
		name         string
		mutate       func(*DeviceFacts)
		availability telemetryapi.EvaluationAvailability
		presence     *telemetryapi.DevicePresenceState
		readiness    telemetryapi.TelemetryReadiness
		display      *telemetryapi.DeviceDisplayState
	}{
		{
			name:         "no signal remains unknown with degraded and rejected evidence",
			availability: telemetryapi.EvaluationAvailabilityAvailable,
			presence:     ptrPresence(telemetryapi.DevicePresenceStateUnknown),
			readiness:    telemetryapi.TelemetryReadinessIncomplete,
			display:      ptrDisplay(telemetryapi.DeviceDisplayStateUnknown),
		},
		{
			name: "recent accepted signal is online",
			mutate: func(facts *DeviceFacts) {
				facts.PresenceSignals = []PresenceSignal{{Type: "SOURCE_ACTIVITY", ObservedAt: now.Add(-20 * time.Second)}}
			},
			availability: telemetryapi.EvaluationAvailabilityAvailable,
			presence:     ptrPresence(telemetryapi.DevicePresenceStateOnline),
			readiness:    telemetryapi.TelemetryReadinessIncomplete,
			display:      ptrDisplay(telemetryapi.DeviceDisplayStateUnknown),
		},
		{
			name: "offline requires continuous coverage",
			mutate: func(facts *DeviceFacts) {
				facts.PresenceSignals = []PresenceSignal{{Type: "SOURCE_ACTIVITY", ObservedAt: now.Add(-4 * time.Minute)}}
			},
			availability: telemetryapi.EvaluationAvailabilityAvailable,
			presence:     ptrPresence(telemetryapi.DevicePresenceStateOffline),
			readiness:    telemetryapi.TelemetryReadinessIncomplete,
			display:      ptrDisplay(telemetryapi.DeviceDisplayStateOffline),
		},
		{
			name: "coverage interruption is unavailable not offline",
			mutate: func(facts *DeviceFacts) {
				facts.PresenceSignals = []PresenceSignal{{Type: "SOURCE_ACTIVITY", ObservedAt: now.Add(-4 * time.Minute)}}
				facts.Coverage = Coverage{Available: false, Reason: telemetryapi.AvailabilityReasonCodeObservationCoverageGap}
			},
			availability: telemetryapi.EvaluationAvailabilityUnavailable,
			presence:     nil,
			readiness:    telemetryapi.TelemetryReadinessIncomplete,
			display:      ptrDisplay(telemetryapi.DeviceDisplayStateUnavailable),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			facts := cloneFacts(base)
			if test.mutate != nil {
				test.mutate(&facts)
			}
			result, err := EvaluateCanonical(facts, 7, now)
			if err != nil {
				t.Fatal(err)
			}
			if result.Snapshot.EvaluationAvailability != test.availability {
				t.Fatalf("availability=%s", result.Snapshot.EvaluationAvailability)
			}
			if !equalPresence(result.Snapshot.Presence.CurrentState, test.presence) {
				t.Fatalf("presence=%v", result.Snapshot.Presence.CurrentState)
			}
			if result.Snapshot.TelemetryReadiness != test.readiness {
				t.Fatalf("readiness=%s", result.Snapshot.TelemetryReadiness)
			}
			if !equalDisplay(result.Snapshot.DisplayState, test.display) {
				t.Fatalf("display=%v", result.Snapshot.DisplayState)
			}
			if len(result.Snapshot.Values) != 3 {
				t.Fatalf("values=%d", len(result.Snapshot.Values))
			}
			if got := result.Snapshot.Values[0].Missing; got == nil || got.Key != "duct.pressure" || got.MissingReason != "ONLY_REJECTED_CANDIDATES" {
				t.Fatalf("rejected=%#v", got)
			}
			if got := result.Snapshot.Values[1].Present; got == nil || got.Key != "zone.humidity" || got.Freshness != "STALE" || got.Quality != telemetryapi.TelemetryQualitySuspect {
				t.Fatalf("humidity=%#v", got)
			}
			if got := result.Snapshot.Values[2].Present; got == nil || got.Key != "zone.temperature" || got.Freshness != "FRESH" {
				t.Fatalf("temperature=%#v", got)
			}
		})
	}
}

func TestEvaluateCanonicalDigestIgnoresRefreshTimeButIncludesPolicyChange(t *testing.T) {
	now := time.Date(2026, 7, 24, 1, 0, 0, 0, time.UTC)
	facts := DeviceFacts{
		DeviceID: deviceA, OwningOrganizationID: orgA, SiteID: siteA,
		Applicability:     telemetryapi.PresenceApplicabilityApplicable,
		PresencePolicy:    &PresencePolicy{Revision: 2, OnlineWithin: time.Minute, OfflineAfter: 3 * time.Minute},
		Coverage:          Coverage{Available: true, ContinuousSince: ptrTime(now.Add(-time.Hour))},
		PresenceSignals:   []PresenceSignal{{Type: "SOURCE_ACTIVITY", ObservedAt: now.Add(-10 * time.Second)}},
		FreshnessPolicies: map[string]FreshnessPolicy{"zone.temperature": {Revision: 5, FreshFor: 5 * time.Minute, Configured: true}},
		Latest:            map[string]LatestObservation{"zone.temperature": {Value: json.RawMessage(`23.5`), ValueType: "NUMBER", SampledAt: now.Add(-10 * time.Second), ReceivedAt: now.Add(-9 * time.Second), Quality: telemetryapi.TelemetryQualityGood}},
	}
	first, err := EvaluateCanonical(facts, 4, now)
	if err != nil {
		t.Fatal(err)
	}
	refresh, err := EvaluateCanonical(facts, 4, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if first.StateDigest != refresh.StateDigest {
		t.Fatalf("refresh changed state digest: %s != %s", first.StateDigest, refresh.StateDigest)
	}
	facts.FreshnessPolicies["zone.temperature"] = FreshnessPolicy{Revision: 6, FreshFor: 5 * time.Minute, Configured: true}
	policyChange, err := EvaluateCanonical(facts, 4, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if first.StateDigest == policyChange.StateDigest {
		t.Fatal("policy revision change did not change state digest")
	}
}

func TestProjectSnapshotPreservesExactRequestedKeyOrderAndPresenceOnly(t *testing.T) {
	now := time.Date(2026, 7, 24, 1, 0, 0, 0, time.UTC)
	facts := DeviceFacts{
		DeviceID: deviceA, OwningOrganizationID: orgA, SiteID: siteA,
		Applicability:   telemetryapi.PresenceApplicabilityApplicable,
		PresencePolicy:  &PresencePolicy{Revision: 2, OnlineWithin: time.Minute, OfflineAfter: 3 * time.Minute},
		Coverage:        Coverage{Available: true, ContinuousSince: ptrTime(now.Add(-time.Hour))},
		PresenceSignals: []PresenceSignal{{Type: "SOURCE_ACTIVITY", ObservedAt: now.Add(-10 * time.Second)}},
		FreshnessPolicies: map[string]FreshnessPolicy{
			"zone.temperature": {Revision: 5, FreshFor: 5 * time.Minute, Configured: true},
			"zone.humidity":    {Revision: 5, FreshFor: 5 * time.Minute, Configured: true},
		},
		Latest: map[string]LatestObservation{
			"zone.temperature": {Value: json.RawMessage(`23.5`), ValueType: "NUMBER", SampledAt: now.Add(-10 * time.Second), ReceivedAt: now.Add(-9 * time.Second), Quality: telemetryapi.TelemetryQualityGood},
		},
	}
	canonical, err := EvaluateCanonical(facts, 9, now)
	if err != nil {
		t.Fatal(err)
	}
	selected := ProjectSnapshot(canonical.Snapshot, []string{"zone.humidity", "zone.temperature"})
	if len(selected.Values) != 2 || selected.Values[0].Missing == nil || selected.Values[0].Missing.Key != "zone.humidity" || selected.Values[0].Missing.MissingReason != "NEVER_OBSERVED" || selected.Values[1].Present == nil || selected.Values[1].Present.Key != "zone.temperature" {
		t.Fatalf("ordered values=%#v", selected.Values)
	}
	if selected.TelemetryReadiness != telemetryapi.TelemetryReadinessIncomplete || selected.DisplayState == nil || *selected.DisplayState != telemetryapi.DeviceDisplayStateUnknown {
		t.Fatalf("projection readiness/display=%s/%v", selected.TelemetryReadiness, selected.DisplayState)
	}
	presenceOnly := ProjectSnapshot(canonical.Snapshot, nil)
	if len(presenceOnly.Values) != 0 || presenceOnly.TelemetryReadiness != telemetryapi.TelemetryReadinessNotApplicable || presenceOnly.DisplayState == nil || *presenceOnly.DisplayState != telemetryapi.DeviceDisplayStateOnline {
		t.Fatalf("presence-only=%#v", presenceOnly)
	}
	notConfigured := ProjectSnapshot(canonical.Snapshot, []string{"outside.air"})
	if len(notConfigured.Values) != 1 || notConfigured.Values[0].Missing == nil || notConfigured.Values[0].Missing.MissingReason != "POLICY_NOT_CONFIGURED" || notConfigured.Values[0].Missing.PolicyRevision != nil {
		t.Fatalf("not-configured=%#v", notConfigured.Values)
	}
}

func TestEvaluateCanonicalRejectsNonContractQualityReasons(t *testing.T) {
	now := time.Date(2026, 7, 24, 1, 0, 0, 0, time.UTC)
	facts := DeviceFacts{
		DeviceID: deviceA, OwningOrganizationID: orgA, SiteID: siteA,
		Applicability:     telemetryapi.PresenceApplicabilityApplicable,
		PresencePolicy:    &PresencePolicy{Revision: 2, OnlineWithin: time.Minute, OfflineAfter: 3 * time.Minute},
		Coverage:          Coverage{Available: true, ContinuousSince: ptrTime(now.Add(-time.Hour))},
		PresenceSignals:   []PresenceSignal{{Type: "SOURCE_ACTIVITY", ObservedAt: now.Add(-10 * time.Second)}},
		FreshnessPolicies: map[string]FreshnessPolicy{"zone.temperature": {Revision: 5, FreshFor: 5 * time.Minute, Configured: true}},
		Latest: map[string]LatestObservation{"zone.temperature": {
			Value: json.RawMessage(`23.5`), ValueType: "NUMBER", SampledAt: now.Add(-10 * time.Second), ReceivedAt: now.Add(-9 * time.Second),
			Quality:        telemetryapi.TelemetryQualitySuspect,
			QualityReasons: []telemetryapi.QualityReasonCode{telemetryapi.QualityReasonCodeSourceLagExceeded, telemetryapi.QualityReasonCodeSourceLagExceeded},
		}},
	}
	if _, err := EvaluateCanonical(facts, 1, now); err == nil {
		t.Fatal("duplicate quality reasons were accepted")
	}
	facts.Latest["zone.temperature"] = LatestObservation{
		Value: json.RawMessage(`23.5`), ValueType: "NUMBER", SampledAt: now.Add(-10 * time.Second), ReceivedAt: now.Add(-9 * time.Second),
		Quality: telemetryapi.TelemetryQualitySuspect, QualityReasons: []telemetryapi.QualityReasonCode{"UNKNOWN_REASON"},
	}
	if _, err := EvaluateCanonical(facts, 1, now); err == nil {
		t.Fatal("unknown quality reason was accepted")
	}
	facts.Latest["zone.temperature"] = LatestObservation{
		Value: json.RawMessage(`"not-a-number"`), ValueType: "NUMBER", SampledAt: now.Add(-10 * time.Second), ReceivedAt: now.Add(-9 * time.Second),
		Quality: telemetryapi.TelemetryQualityGood,
	}
	if _, err := EvaluateCanonical(facts, 1, now); err == nil {
		t.Fatal("value and valueType mismatch was accepted")
	}
}

func TestEvaluatePresencePreservesLastKnownAndRequiresNamedDisconnect(t *testing.T) {
	now := time.Date(2026, 7, 24, 1, 0, 0, 0, time.UTC)
	lastKnownAt := telemetryapi.Instant(now.Add(-time.Minute).Format("2006-01-02T15:04:05.000Z"))
	previous := &telemetryapi.LastKnownPresence{
		State: telemetryapi.DevicePresenceStateOnline, LastSeenAt: &lastKnownAt,
		EvaluatedAt: telemetryapi.Instant(now.Add(-time.Minute).Format("2006-01-02T15:04:05.000Z")), PolicyRevision: 1,
	}
	facts := DeviceFacts{
		DeviceID: deviceA, OwningOrganizationID: orgA, SiteID: siteA,
		Applicability: telemetryapi.PresenceApplicabilityApplicable,
		PresencePolicy: &PresencePolicy{
			Revision: 2, OnlineWithin: time.Minute, OfflineAfter: 3 * time.Minute,
			AcceptedSignalTypes: []string{"SOURCE_ACTIVITY", "EXPLICIT_CONNECT"},
		},
		Coverage:          Coverage{Available: true, ContinuousSince: ptrTime(now.Add(-time.Hour))},
		LastKnownPresence: previous,
		PresenceSignals:   []PresenceSignal{{Type: "EXPLICIT_DISCONNECT", ObservedAt: now.Add(-10 * time.Second)}},
		FreshnessPolicies: map[string]FreshnessPolicy{},
		Latest:            map[string]LatestObservation{},
	}

	ignored, err := EvaluateCanonical(facts, 3, now)
	if err != nil {
		t.Fatal(err)
	}
	if ignored.Snapshot.Presence.CurrentState == nil || *ignored.Snapshot.Presence.CurrentState != telemetryapi.DevicePresenceStateUnknown {
		t.Fatalf("unaccepted disconnect state=%v", ignored.Snapshot.Presence.CurrentState)
	}
	if ignored.Snapshot.Presence.LastKnown == nil || ignored.Snapshot.Presence.LastKnown.State != telemetryapi.DevicePresenceStateOnline || ignored.Snapshot.Presence.LastKnown.PolicyRevision != 1 {
		t.Fatalf("UNKNOWN overwrote last known: %#v", ignored.Snapshot.Presence.LastKnown)
	}

	facts.PresencePolicy.AcceptedSignalTypes = append(facts.PresencePolicy.AcceptedSignalTypes, "EXPLICIT_DISCONNECT")
	disconnected, err := EvaluateCanonical(facts, 4, now)
	if err != nil {
		t.Fatal(err)
	}
	if disconnected.Snapshot.Presence.CurrentState == nil || *disconnected.Snapshot.Presence.CurrentState != telemetryapi.DevicePresenceStateOffline {
		t.Fatalf("accepted disconnect state=%v", disconnected.Snapshot.Presence.CurrentState)
	}
	if disconnected.Snapshot.Presence.LastKnown == nil || disconnected.Snapshot.Presence.LastKnown.State != telemetryapi.DevicePresenceStateOffline || disconnected.Snapshot.Presence.LastKnown.PolicyRevision != 2 {
		t.Fatalf("accepted disconnect last known=%#v", disconnected.Snapshot.Presence.LastKnown)
	}
}

func cloneFacts(value DeviceFacts) DeviceFacts {
	copyValue := value
	copyValue.PresenceSignals = append([]PresenceSignal(nil), value.PresenceSignals...)
	if value.PresencePolicy != nil {
		policy := *value.PresencePolicy
		policy.AcceptedSignalTypes = append([]string(nil), value.PresencePolicy.AcceptedSignalTypes...)
		copyValue.PresencePolicy = &policy
	}
	copyValue.FreshnessPolicies = map[string]FreshnessPolicy{}
	for key, policy := range value.FreshnessPolicies {
		copyValue.FreshnessPolicies[key] = policy
	}
	copyValue.Latest = map[string]LatestObservation{}
	for key, observation := range value.Latest {
		copyValue.Latest[key] = observation
	}
	copyValue.RejectedKeys = map[string]bool{}
	for key, rejected := range value.RejectedKeys {
		copyValue.RejectedKeys[key] = rejected
	}
	return copyValue
}

func ptrTime(value time.Time) *time.Time { return &value }
func ptrString(value string) *string     { return &value }
func ptrPresence(value telemetryapi.DevicePresenceState) *telemetryapi.DevicePresenceState {
	return &value
}
func ptrDisplay(value telemetryapi.DeviceDisplayState) *telemetryapi.DeviceDisplayState {
	return &value
}
func equalPresence(left, right *telemetryapi.DevicePresenceState) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}
func equalDisplay(left, right *telemetryapi.DeviceDisplayState) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}
