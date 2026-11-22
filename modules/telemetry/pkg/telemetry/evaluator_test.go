package telemetry

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/modules/telemetry/pkg/telemetryapi"
)

const (
	deviceA = "018f2e00-3000-7000-8000-000000000001"
	tenantA = "018f2d00-0000-7000-8000-000000000001"
	orgA    = "018f2e00-0000-7000-8000-000000000001"
	siteA   = "018f2e00-1000-7000-8000-000000000001"
)

func TestEvaluateCanonicalPresenceFreshnessAndQuality(t *testing.T) {
	now := time.Date(2026, 7, 24, 1, 0, 0, 0, time.UTC)
	base := DeviceFacts{
		DeviceID:      deviceA,
		TenantID:      tenantA,
		SiteID:        siteA,
		Applicability: telemetryapi.PresenceApplicabilityApplicable,
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
				Quality:        telemetryapi.TelemetryQualityStale,
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
			if got := result.Snapshot.Values[1].Present; got == nil || got.Key != "zone.humidity" || got.Freshness != "STALE" || got.Quality != telemetryapi.TelemetryQualityStale {
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
		DeviceID: deviceA, TenantID: tenantA, SiteID: siteA,
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
		DeviceID: deviceA, TenantID: tenantA, SiteID: siteA,
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
		DeviceID: deviceA, TenantID: tenantA, SiteID: siteA,
		Applicability:     telemetryapi.PresenceApplicabilityApplicable,
		PresencePolicy:    &PresencePolicy{Revision: 2, OnlineWithin: time.Minute, OfflineAfter: 3 * time.Minute},
		Coverage:          Coverage{Available: true, ContinuousSince: ptrTime(now.Add(-time.Hour))},
		PresenceSignals:   []PresenceSignal{{Type: "SOURCE_ACTIVITY", ObservedAt: now.Add(-10 * time.Second)}},
		FreshnessPolicies: map[string]FreshnessPolicy{"zone.temperature": {Revision: 5, FreshFor: 5 * time.Minute, Configured: true}},
		Latest: map[string]LatestObservation{"zone.temperature": {
			Value: json.RawMessage(`23.5`), ValueType: "NUMBER", SampledAt: now.Add(-10 * time.Second), ReceivedAt: now.Add(-9 * time.Second),
			Quality:        telemetryapi.TelemetryQualityStale,
			QualityReasons: []telemetryapi.QualityReasonCode{telemetryapi.QualityReasonCodeSourceLagExceeded, telemetryapi.QualityReasonCodeSourceLagExceeded},
		}},
	}
	if _, err := EvaluateCanonical(facts, 1, now); err == nil {
		t.Fatal("duplicate quality reasons were accepted")
	}
	facts.Latest["zone.temperature"] = LatestObservation{
		Value: json.RawMessage(`23.5`), ValueType: "NUMBER", SampledAt: now.Add(-10 * time.Second), ReceivedAt: now.Add(-9 * time.Second),
		Quality: telemetryapi.TelemetryQualityStale, QualityReasons: []telemetryapi.QualityReasonCode{"UNKNOWN_REASON"},
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
		DeviceID: deviceA, TenantID: tenantA, SiteID: siteA,
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

func TestCopiedReadingsDoNotKeepDeviceOnlineAndSweepStabilizesAtOffline(t *testing.T) {
	base := time.Date(2026, 11, 8, 8, 13, 0, 0, time.UTC)
	policy := &PresencePolicy{
		Revision: 1, OnlineWithin: 150 * time.Second, OfflineAfter: 300 * time.Second,
		AcceptedSignalTypes: []string{"SOURCE_ACTIVITY"},
	}
	facts := DeviceFacts{
		DeviceID: deviceA, TenantID: tenantA, SiteID: siteA,
		Applicability:  telemetryapi.PresenceApplicabilityApplicable,
		PresencePolicy: policy,
		Coverage:       Coverage{Available: true, ContinuousSince: ptrTime(base.Add(-time.Hour))},
		PresenceSignals: []PresenceSignal{
			{Type: "SOURCE_ACTIVITY", ObservedAt: base},
		},
		FreshnessPolicies: map[string]FreshnessPolicy{
			"active_energy_total":     {Revision: 1, FreshFor: 10 * time.Minute, Configured: true},
			"chw_leaving_temperature": {Revision: 1, FreshFor: 10 * time.Minute, Configured: true},
		},
		Latest: map[string]LatestObservation{
			"active_energy_total": {
				Value: json.RawMessage(`100`), ValueType: "NUMBER", Unit: ptrString("kWh"),
				SampledAt: base, ReceivedAt: base, Quality: telemetryapi.TelemetryQualityGood,
			},
		},
	}

	online, err := EvaluateCanonical(facts, 1, base.Add(time.Minute))
	if err != nil || !equalPresence(online.Snapshot.Presence.CurrentState, ptrPresence(telemetryapi.DevicePresenceStateOnline)) {
		t.Fatalf("expected ONLINE at +1m, got %v err=%v", online.Snapshot.Presence.CurrentState, err)
	}
	facts.LastKnownPresence = online.Snapshot.Presence.LastKnown

	// Copied readings keep updating Latest without emitting a new PresenceSignal.
	atSixMinutes := base.Add(6 * time.Minute)
	facts.Latest["chw_leaving_temperature"] = LatestObservation{
		Value: json.RawMessage(`7.2`), ValueType: "NUMBER", Unit: ptrString("Cel"),
		SampledAt: atSixMinutes, ReceivedAt: atSixMinutes, Quality: telemetryapi.TelemetryQualityGood,
	}
	offline, err := EvaluateCanonical(facts, 2, atSixMinutes)
	if err != nil || !equalPresence(offline.Snapshot.Presence.CurrentState, ptrPresence(telemetryapi.DevicePresenceStateOffline)) {
		t.Fatalf("expected OFFLINE after offline_after with only copied readings, got %v err=%v", offline.Snapshot.Presence.CurrentState, err)
	}
	facts.LastKnownPresence = offline.Snapshot.Presence.LastKnown

	// Re-evaluating one minute later while still OFFLINE keeps the exact same StateDigest.
	nextSweep, err := EvaluateCanonical(facts, 2, base.Add(7*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if nextSweep.StateDigest != offline.StateDigest {
		t.Fatalf("subsequent sweep while OFFLINE changed StateDigest: %s != %s", nextSweep.StateDigest, offline.StateDigest)
	}
}

func TestEvaluateCanonicalEmitsUnchangedSinceAndRefreshSnapshotAtProjectsStale(t *testing.T) {
	t0 := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	heldSince := t0.Add(-20 * time.Minute)
	facts := DeviceFacts{
		DeviceID:      deviceA,
		TenantID:      tenantA,
		SiteID:        siteA,
		Applicability: telemetryapi.PresenceApplicabilityApplicable,
		PresencePolicy: &PresencePolicy{
			Revision: 1, OnlineWithin: 3 * time.Minute, OfflineAfter: 6 * time.Minute,
		},
		Coverage:        Coverage{Available: true, ContinuousSince: ptrTime(t0.Add(-time.Hour))},
		PresenceSignals: []PresenceSignal{{Type: "SOURCE_ACTIVITY", ObservedAt: t0}},
		FreshnessPolicies: map[string]FreshnessPolicy{
			"zone.humidity":    {Revision: 1, FreshFor: 10 * time.Minute, Configured: true},
			"zone.temperature": {Revision: 1, FreshFor: 5 * time.Minute, Configured: true},
		},
		Latest: map[string]LatestObservation{
			"zone.temperature": {
				Value: json.RawMessage(`22.5`), ValueType: "NUMBER", Unit: ptrString("Cel"),
				SampledAt: t0, ReceivedAt: t0.Add(time.Second), UnchangedSince: ptrTime(heldSince),
				Quality: telemetryapi.TelemetryQualityGood,
				Health:  &PointHealth{Status: HealthHealthy, Score: 100},
			},
			"zone.humidity": {
				Value: json.RawMessage(`55`), ValueType: "NUMBER", Unit: ptrString("%RH"),
				SampledAt: t0, ReceivedAt: t0.Add(time.Second), UnchangedSince: ptrTime(t0),
				Quality: telemetryapi.TelemetryQualityPartial,
				QualityReasons: []telemetryapi.QualityReasonCode{
					telemetryapi.QualityReasonCodeLowConfidence,
				},
				Health: &PointHealth{
					Status:  HealthSuspect,
					Score:   40,
					Reasons: []QualityReason{QualityReasonLowConfidence},
				},
			},
		},
	}

	eval, err := EvaluateCanonical(facts, 4, t0.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	temp := eval.Snapshot.Values[1].Present
	hum := eval.Snapshot.Values[0].Present
	if temp == nil || temp.UnchangedSince == nil || *temp.UnchangedSince != instant(heldSince) || temp.FreshWithinSeconds == nil || *temp.FreshWithinSeconds != 300 {
		t.Fatalf("temperature present=%#v", temp)
	}
	if hum == nil || hum.FreshWithinSeconds == nil || *hum.FreshWithinSeconds != 600 || hum.Freshness != "FRESH" {
		t.Fatalf("humidity present=%#v", hum)
	}

	stillFresh := RefreshSnapshotAt(eval.Snapshot, t0.Add(4*time.Minute))
	if stillFresh.Values[1].Present.Freshness != "FRESH" || stillFresh.BusinessRevision != 4 {
		t.Fatalf("stillFresh=%#v", stillFresh)
	}

	expiredTemp := RefreshSnapshotAt(eval.Snapshot, t0.Add(6*time.Minute))
	if expiredTemp.BusinessRevision != 4 || expiredTemp.EvaluatedAt != eval.Snapshot.EvaluatedAt {
		t.Fatalf("read projection mutated revision/evaluatedAt: rev=%d evaluatedAt=%s", expiredTemp.BusinessRevision, expiredTemp.EvaluatedAt)
	}
	if expiredTemp.Values[1].Present.Freshness != "STALE" || expiredTemp.Values[1].Present.Health.Status != HealthStale || expiredTemp.Values[1].Present.Health.Score != 20 {
		t.Fatalf("expired temperature=%#v", expiredTemp.Values[1].Present)
	}
	if expiredTemp.Values[0].Present.Freshness != "FRESH" || expiredTemp.TelemetryReadiness != telemetryapi.TelemetryReadinessDegraded || expiredTemp.DisplayState == nil || *expiredTemp.DisplayState != telemetryapi.DeviceDisplayStateStale {
		t.Fatalf("expiredTemp readiness=%s display=%v hum=%#v", expiredTemp.TelemetryReadiness, expiredTemp.DisplayState, expiredTemp.Values[0].Present)
	}
	// Original snapshot slice remains unmutated.
	if eval.Snapshot.Values[1].Present.Freshness != "FRESH" {
		t.Fatalf("original cached snapshot mutated: %#v", eval.Snapshot.Values[1].Present)
	}

	// SUSPECT/BAD point keeps HealthStatus=SUSPECT even when Freshness transitions to STALE.
	bothExpired := RefreshSnapshotAt(eval.Snapshot, t0.Add(11*time.Minute))
	if bothExpired.Values[0].Present.Freshness != "STALE" || bothExpired.Values[0].Present.Health.Status != HealthSuspect || bothExpired.Values[0].Present.Health.Score != 40 {
		t.Fatalf("suspect expired humidity=%#v", bothExpired.Values[0].Present)
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

func TestEvaluateCanonicalLowPowerSleepingPresence(t *testing.T) {
	now := time.Date(2026, 7, 24, 1, 0, 0, 0, time.UTC)
	base := DeviceFacts{
		DeviceID:      deviceA,
		TenantID:      tenantA,
		SiteID:        siteA,
		Applicability: telemetryapi.PresenceApplicabilityApplicable,
		PresencePolicy: &PresencePolicy{
			Revision:            3,
			OnlineWithin:        2 * time.Minute,
			SleepCycle:          15 * time.Minute,
			OfflineAfter:        20 * time.Minute,
			AcceptedSignalTypes: []string{"SOURCE_ACTIVITY", "EXPLICIT_CONNECT", "EXPLICIT_SLEEP", "EXPLICIT_DISCONNECT"},
		},
		Coverage: Coverage{Available: true, ContinuousSince: ptrTime(now.Add(-30 * time.Minute))},
		FreshnessPolicies: map[string]FreshnessPolicy{
			"zone.temperature": {Revision: 5, FreshFor: 30 * time.Minute, Configured: true},
		},
		Latest: map[string]LatestObservation{
			"zone.temperature": {
				Value: json.RawMessage(`23.5`), ValueType: "NUMBER", Unit: ptrString("Cel"),
				SampledAt: now.Add(-30 * time.Second), ReceivedAt: now.Add(-28 * time.Second),
				Quality: telemetryapi.TelemetryQualityGood,
			},
		},
	}

	tests := []struct {
		name         string
		mutate       func(*DeviceFacts)
		availability telemetryapi.EvaluationAvailability
		presence     *telemetryapi.DevicePresenceState
		display      *telemetryapi.DeviceDisplayState
	}{
		{
			name: "recent activity within OnlineWithin stays ONLINE",
			mutate: func(facts *DeviceFacts) {
				facts.PresenceSignals = []PresenceSignal{{Type: "SOURCE_ACTIVITY", ObservedAt: now.Add(-1 * time.Minute)}}
			},
			availability: telemetryapi.EvaluationAvailabilityAvailable,
			presence:     ptrPresence(telemetryapi.DevicePresenceStateOnline),
			display:      ptrDisplay(telemetryapi.DeviceDisplayStateOnline),
		},
		{
			name: "elapsed OnlineWithin inside SleepCycle becomes SLEEPING",
			mutate: func(facts *DeviceFacts) {
				facts.PresenceSignals = []PresenceSignal{{Type: "SOURCE_ACTIVITY", ObservedAt: now.Add(-5 * time.Minute)}}
			},
			availability: telemetryapi.EvaluationAvailabilityAvailable,
			presence:     ptrPresence(telemetryapi.DevicePresenceStateSleeping),
			display:      ptrDisplay(telemetryapi.DeviceDisplayStateSleeping),
		},
		{
			name: "explicit sleep transitions immediately to SLEEPING even inside OnlineWithin",
			mutate: func(facts *DeviceFacts) {
				facts.PresenceSignals = []PresenceSignal{{Type: "EXPLICIT_SLEEP", ObservedAt: now.Add(-10 * time.Second)}}
			},
			availability: telemetryapi.EvaluationAvailabilityAvailable,
			presence:     ptrPresence(telemetryapi.DevicePresenceStateSleeping),
			display:      ptrDisplay(telemetryapi.DeviceDisplayStateSleeping),
		},
		{
			name: "missed wakeup past OfflineAfter with continuous coverage becomes OFFLINE",
			mutate: func(facts *DeviceFacts) {
				facts.PresenceSignals = []PresenceSignal{{Type: "EXPLICIT_SLEEP", ObservedAt: now.Add(-21 * time.Minute)}}
			},
			availability: telemetryapi.EvaluationAvailabilityAvailable,
			presence:     ptrPresence(telemetryapi.DevicePresenceStateOffline),
			display:      ptrDisplay(telemetryapi.DeviceDisplayStateOffline),
		},
		{
			name: "missed wakeup past OfflineAfter with coverage gap becomes UNAVAILABLE",
			mutate: func(facts *DeviceFacts) {
				facts.Coverage = Coverage{Available: true, ContinuousSince: ptrTime(now.Add(-10 * time.Minute))}
				facts.PresenceSignals = []PresenceSignal{{Type: "EXPLICIT_SLEEP", ObservedAt: now.Add(-21 * time.Minute)}}
			},
			availability: telemetryapi.EvaluationAvailabilityUnavailable,
			presence:     nil,
			display:      ptrDisplay(telemetryapi.DeviceDisplayStateUnavailable),
		},
		{
			name: "wakeup connect or activity after EXPLICIT_SLEEP restores ONLINE",
			mutate: func(facts *DeviceFacts) {
				facts.PresenceSignals = []PresenceSignal{
					{Type: "EXPLICIT_SLEEP", ObservedAt: now.Add(-10 * time.Minute)},
					{Type: "EXPLICIT_CONNECT", ObservedAt: now.Add(-5 * time.Second)},
				}
			},
			availability: telemetryapi.EvaluationAvailabilityAvailable,
			presence:     ptrPresence(telemetryapi.DevicePresenceStateOnline),
			display:      ptrDisplay(telemetryapi.DeviceDisplayStateOnline),
		},
		{
			name: "always-on device with SleepCycle 0 still evaluates UNKNOWN between OnlineWithin and OfflineAfter",
			mutate: func(facts *DeviceFacts) {
				facts.PresencePolicy.SleepCycle = 0
				facts.PresenceSignals = []PresenceSignal{{Type: "SOURCE_ACTIVITY", ObservedAt: now.Add(-5 * time.Minute)}}
			},
			availability: telemetryapi.EvaluationAvailabilityAvailable,
			presence:     ptrPresence(telemetryapi.DevicePresenceStateUnknown),
			display:      ptrDisplay(telemetryapi.DeviceDisplayStateUnknown),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			facts := cloneFacts(base)
			if test.mutate != nil {
				test.mutate(&facts)
			}
			result, err := EvaluateCanonical(facts, 8, now)
			if err != nil {
				t.Fatal(err)
			}
			if result.Snapshot.EvaluationAvailability != test.availability {
				t.Fatalf("availability=%s, want %s", result.Snapshot.EvaluationAvailability, test.availability)
			}
			if !equalPresence(result.Snapshot.Presence.CurrentState, test.presence) {
				t.Fatalf("presence=%v, want %v", result.Snapshot.Presence.CurrentState, test.presence)
			}
			if !equalDisplay(result.Snapshot.DisplayState, test.display) {
				t.Fatalf("display=%v, want %v", result.Snapshot.DisplayState, test.display)
			}
		})
	}
}
