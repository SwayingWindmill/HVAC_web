package telemetry

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/quanlaihe/hvac-web/modules/telemetry/pkg/telemetryapi"
)

type PresencePolicy struct {
	Revision            int64
	OnlineWithin        time.Duration
	OfflineAfter        time.Duration
	AcceptedSignalTypes []string
}

type PresenceSignal struct {
	Type       string
	ObservedAt time.Time
}

type Coverage struct {
	Available       bool
	ContinuousSince *time.Time
	Reason          telemetryapi.AvailabilityReasonCode
}

type FreshnessPolicy struct {
	Revision   int64
	FreshFor   time.Duration
	Configured bool
}

type LatestObservation struct {
	Value          json.RawMessage
	ValueType      string
	Unit           *string
	SampledAt      time.Time
	ReceivedAt     time.Time
	Quality        telemetryapi.TelemetryQuality
	QualityReasons []telemetryapi.QualityReasonCode
}

type DeviceFacts struct {
	DeviceID      string
	TenantID      string
	SiteID        string
	Applicability        telemetryapi.PresenceApplicability
	PresencePolicy       *PresencePolicy
	Coverage             Coverage
	PresenceSignals      []PresenceSignal
	LastKnownPresence    *telemetryapi.LastKnownPresence
	FreshnessPolicies    map[string]FreshnessPolicy
	Latest               map[string]LatestObservation
	RejectedKeys         map[string]bool
}

type CanonicalEvaluation struct {
	Snapshot    telemetryapi.DeviceObservationSnapshot
	StateDigest string
}

func EvaluateCanonical(facts DeviceFacts, revision int64, evaluatedAt time.Time) (CanonicalEvaluation, error) {
	if facts.DeviceID == "" || facts.TenantID == "" || facts.SiteID == "" || revision < 1 || evaluatedAt.IsZero() {
		return CanonicalEvaluation{}, errors.New("telemetry evaluation context is incomplete")
	}
	if facts.Applicability != telemetryapi.PresenceApplicabilityApplicable && facts.Applicability != telemetryapi.PresenceApplicabilityNotApplicable {
		return CanonicalEvaluation{}, errors.New("presence applicability is invalid")
	}

	presence, availability, reasons := evaluatePresence(facts, evaluatedAt)
	keys := make([]string, 0, len(facts.FreshnessPolicies))
	for key, policy := range facts.FreshnessPolicies {
		if policy.Configured {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	values := make([]telemetryapi.TelemetryKeyState, 0, len(keys))
	for _, key := range keys {
		state, err := evaluateKey(key, facts.FreshnessPolicies[key], facts.Latest[key], facts.RejectedKeys[key], evaluatedAt)
		if err != nil {
			return CanonicalEvaluation{}, err
		}
		values = append(values, state)
	}
	readiness := deriveReadiness(values)
	display := deriveDisplay(facts.Applicability, availability, presence.CurrentState, readiness)
	snapshot := telemetryapi.DeviceObservationSnapshot{
		SchemaVersion:          1,
		TenantId:               telemetryapi.UUIDv7(facts.TenantID),
		SiteId:                 telemetryapi.UUIDv7(facts.SiteID),
		DeviceId:               telemetryapi.UUIDv7(facts.DeviceID),
		BusinessRevision:       telemetryapi.BusinessRevision(revision),
		EvaluatedAt:            instant(evaluatedAt),
		EvaluationAvailability: availability,
		AvailabilityReasons:    reasons,
		Presence:               presence,
		TelemetryReadiness:     readiness,
		DisplayState:           display,
		Values:                 values,
	}
	digest, err := stateDigest(snapshot)
	if err != nil {
		return CanonicalEvaluation{}, err
	}
	return CanonicalEvaluation{Snapshot: snapshot, StateDigest: digest}, nil
}

func ProjectSnapshot(canonical telemetryapi.DeviceObservationSnapshot, requestedKeys []string) telemetryapi.DeviceObservationSnapshot {
	projected := canonical
	projected.Values = make([]telemetryapi.TelemetryKeyState, 0, len(requestedKeys))
	byKey := make(map[string]telemetryapi.TelemetryKeyState, len(canonical.Values))
	for _, state := range canonical.Values {
		if state.Present != nil {
			byKey[string(state.Present.Key)] = state
		} else if state.Missing != nil {
			byKey[string(state.Missing.Key)] = state
		}
	}
	for _, key := range requestedKeys {
		if state, ok := byKey[key]; ok {
			projected.Values = append(projected.Values, state)
			continue
		}
		projected.Values = append(projected.Values, telemetryapi.TelemetryKeyState{Missing: &telemetryapi.TelemetryMissingState{
			Key: telemetryapi.TelemetryKey(key), State: "MISSING", Freshness: "MISSING", MissingReason: "POLICY_NOT_CONFIGURED",
		}})
	}
	projected.TelemetryReadiness = deriveReadiness(projected.Values)
	projected.DisplayState = deriveDisplay(projected.Presence.Applicability, projected.EvaluationAvailability, projected.Presence.CurrentState, projected.TelemetryReadiness)
	return projected
}

func evaluatePresence(facts DeviceFacts, evaluatedAt time.Time) (telemetryapi.PresenceSnapshot, telemetryapi.EvaluationAvailability, []telemetryapi.AvailabilityReasonCode) {
	presence := telemetryapi.PresenceSnapshot{Applicability: facts.Applicability, LastKnown: cloneLastKnown(facts.LastKnownPresence)}
	if facts.Applicability == telemetryapi.PresenceApplicabilityNotApplicable {
		return presence, telemetryapi.EvaluationAvailabilityAvailable, []telemetryapi.AvailabilityReasonCode{}
	}

	policy := facts.PresencePolicy
	validPolicy := policy != nil && policy.Revision >= 1 && policy.OnlineWithin > 0 && policy.OfflineAfter > policy.OnlineWithin
	var lastSignal *PresenceSignal
	if validPolicy {
		lastSignal = greatestAcceptedSignal(facts.PresenceSignals, policy.AcceptedSignalTypes)
	}
	if lastSignal != nil {
		value := instant(lastSignal.ObservedAt)
		presence.LastSeenAt = &value
	}
	if !facts.Coverage.Available {
		reason := facts.Coverage.Reason
		if reason == "" {
			reason = telemetryapi.AvailabilityReasonCodeObservationCoverageGap
		}
		return presence, telemetryapi.EvaluationAvailabilityUnavailable, []telemetryapi.AvailabilityReasonCode{reason}
	}
	if !validPolicy {
		state := telemetryapi.DevicePresenceStateUnknown
		presence.CurrentState = &state
		return presence, telemetryapi.EvaluationAvailabilityAvailable, []telemetryapi.AvailabilityReasonCode{}
	}

	policyRevision := telemetryapi.PolicyRevision(policy.Revision)
	presence.PolicyRevision = &policyRevision
	state := telemetryapi.DevicePresenceStateUnknown
	if lastSignal != nil {
		if lastSignal.Type == "EXPLICIT_DISCONNECT" && signalTypeAllowed(lastSignal.Type, policy.AcceptedSignalTypes) {
			state = telemetryapi.DevicePresenceStateOffline
		} else {
			age := evaluatedAt.Sub(lastSignal.ObservedAt)
			switch {
			case age <= policy.OnlineWithin:
				state = telemetryapi.DevicePresenceStateOnline
			case age >= policy.OfflineAfter:
				decisionWindowStart := evaluatedAt.Add(-policy.OfflineAfter)
				if facts.Coverage.ContinuousSince == nil || facts.Coverage.ContinuousSince.After(decisionWindowStart) {
					return presence, telemetryapi.EvaluationAvailabilityUnavailable, []telemetryapi.AvailabilityReasonCode{telemetryapi.AvailabilityReasonCodeObservationCoverageGap}
				}
				state = telemetryapi.DevicePresenceStateOffline
			}
		}
	}
	presence.CurrentState = &state
	if state == telemetryapi.DevicePresenceStateOnline || state == telemetryapi.DevicePresenceStateOffline {
		lastSeen := lastSignal.ObservedAt
		presence.LastKnown = currentLastKnown(state, &lastSeen, evaluatedAt, &policyRevision)
	}
	return presence, telemetryapi.EvaluationAvailabilityAvailable, []telemetryapi.AvailabilityReasonCode{}
}

func evaluateKey(key string, policy FreshnessPolicy, latest LatestObservation, rejectedOnly bool, evaluatedAt time.Time) (telemetryapi.TelemetryKeyState, error) {
	policyRevision := telemetryapi.PolicyRevision(policy.Revision)
	if len(latest.Value) == 0 || latest.SampledAt.IsZero() || latest.ReceivedAt.IsZero() {
		reason := "NEVER_OBSERVED"
		if rejectedOnly {
			reason = "ONLY_REJECTED_CANDIDATES"
		}
		return telemetryapi.TelemetryKeyState{Missing: &telemetryapi.TelemetryMissingState{
			Key: telemetryapi.TelemetryKey(key), State: "MISSING", Freshness: "MISSING", MissingReason: reason, PolicyRevision: &policyRevision,
		}}, nil
	}
	freshness := telemetryapi.TelemetryFreshnessFresh
	if policy.FreshFor <= 0 || evaluatedAt.Sub(latest.SampledAt) > policy.FreshFor {
		freshness = telemetryapi.TelemetryFreshnessStale
	}
	value, err := contractTelemetryValue(latest.Value, latest.ValueType)
	if err != nil {
		return telemetryapi.TelemetryKeyState{}, err
	}
	quality := latest.Quality
	switch quality {
	case telemetryapi.TelemetryQualityGood,
		telemetryapi.TelemetryQualityPartial,
		telemetryapi.TelemetryQualityEstimated,
		telemetryapi.TelemetryQualityManual,
		telemetryapi.TelemetryQualityStale,
		telemetryapi.TelemetryQualityInvalid:
	default:
		return telemetryapi.TelemetryKeyState{}, errors.New("telemetry quality is invalid")
	}
	qualityReasons, err := canonicalQualityReasons(latest.QualityReasons)
	if err != nil {
		return telemetryapi.TelemetryKeyState{}, err
	}
	return telemetryapi.TelemetryKeyState{Present: &telemetryapi.TelemetryPresentState{
		Key: telemetryapi.TelemetryKey(key), State: "PRESENT", Value: value, ValueType: latest.ValueType,
		Unit: cloneString(latest.Unit), SampledAt: instant(latest.SampledAt), ReceivedAt: instant(latest.ReceivedAt),
		Freshness: string(freshness), Quality: quality, QualityReasons: qualityReasons, PolicyRevision: policyRevision,
	}}, nil
}

func canonicalQualityReasons(values []telemetryapi.QualityReasonCode) ([]telemetryapi.QualityReasonCode, error) {
	if len(values) > 16 {
		return nil, errors.New("telemetry quality reasons exceed the public contract limit")
	}
	seen := make(map[telemetryapi.QualityReasonCode]struct{}, len(values))
	result := make([]telemetryapi.QualityReasonCode, 0, len(values))
	for _, value := range values {
		switch value {
		case telemetryapi.QualityReasonCodeSourceUntrusted,
			telemetryapi.QualityReasonCodeTypeMismatch,
			telemetryapi.QualityReasonCodeUnitMismatch,
			telemetryapi.QualityReasonCodeOutOfRange,
			telemetryapi.QualityReasonCodeClockAhead,
			telemetryapi.QualityReasonCodeClockBehind,
			telemetryapi.QualityReasonCodeSourceLagExceeded,
			telemetryapi.QualityReasonCodeDuplicate,
			telemetryapi.QualityReasonCodeOutOfOrder,
			telemetryapi.QualityReasonCodeReplayed:
		default:
			return nil, errors.New("telemetry quality reason is invalid")
		}
		if _, duplicate := seen[value]; duplicate {
			return nil, errors.New("telemetry quality reasons contain a duplicate")
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	slices.Sort(result)
	return result, nil
}

func contractTelemetryValue(raw json.RawMessage, valueType string) (json.RawMessage, error) {
	var value any
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil || value == nil {
		return nil, errors.New("telemetry value is not valid JSON")
	}
	switch valueType {
	case "NUMBER":
		if _, ok := value.(float64); !ok {
			return nil, errors.New("telemetry NUMBER value does not match valueType")
		}
	case "STRING":
		text, ok := value.(string)
		if !ok || utf8.RuneCountInString(text) > 4096 {
			return nil, errors.New("telemetry STRING value does not match the public contract")
		}
	case "BOOLEAN":
		if _, ok := value.(bool); !ok {
			return nil, errors.New("telemetry BOOLEAN value does not match valueType")
		}
	case "JSON":
		switch typed := value.(type) {
		case map[string]any:
			if len(typed) > 64 {
				return nil, errors.New("telemetry JSON object exceeds the public contract limit")
			}
		case []any:
			if len(typed) > 256 {
				return nil, errors.New("telemetry JSON array exceeds the public contract limit")
			}
			for _, item := range typed {
				switch primitive := item.(type) {
				case float64, bool:
				case string:
					if utf8.RuneCountInString(primitive) > 4096 {
						return nil, errors.New("telemetry JSON array string exceeds the public contract limit")
					}
				default:
					return nil, errors.New("telemetry JSON array contains a non-contract item")
				}
			}
		default:
			return nil, errors.New("telemetry JSON value must be an object or primitive array")
		}
	default:
		return nil, errors.New("telemetry valueType is invalid")
	}
	return cloneJSON(raw), nil
}

func deriveReadiness(values []telemetryapi.TelemetryKeyState) telemetryapi.TelemetryReadiness {
	if len(values) == 0 {
		return telemetryapi.TelemetryReadinessNotApplicable
	}
	degraded := false
	for _, value := range values {
		if value.Missing != nil {
			return telemetryapi.TelemetryReadinessIncomplete
		}
		if value.Present == nil {
			return telemetryapi.TelemetryReadinessIncomplete
		}
		if value.Present.Freshness != string(telemetryapi.TelemetryFreshnessFresh) || value.Present.Quality != telemetryapi.TelemetryQualityGood {
			degraded = true
		}
	}
	if degraded {
		return telemetryapi.TelemetryReadinessDegraded
	}
	return telemetryapi.TelemetryReadinessCurrent
}

func deriveDisplay(applicability telemetryapi.PresenceApplicability, availability telemetryapi.EvaluationAvailability, presence *telemetryapi.DevicePresenceState, readiness telemetryapi.TelemetryReadiness) *telemetryapi.DeviceDisplayState {
	if applicability == telemetryapi.PresenceApplicabilityNotApplicable {
		return nil
	}
	state := telemetryapi.DeviceDisplayStateUnknown
	switch {
	case availability == telemetryapi.EvaluationAvailabilityUnavailable:
		state = telemetryapi.DeviceDisplayStateUnavailable
	case presence != nil && *presence == telemetryapi.DevicePresenceStateOffline:
		state = telemetryapi.DeviceDisplayStateOffline
	case presence == nil || *presence == telemetryapi.DevicePresenceStateUnknown || readiness == telemetryapi.TelemetryReadinessIncomplete:
		state = telemetryapi.DeviceDisplayStateUnknown
	case *presence == telemetryapi.DevicePresenceStateOnline && readiness == telemetryapi.TelemetryReadinessDegraded:
		state = telemetryapi.DeviceDisplayStateStale
	case *presence == telemetryapi.DevicePresenceStateOnline && (readiness == telemetryapi.TelemetryReadinessCurrent || readiness == telemetryapi.TelemetryReadinessNotApplicable):
		state = telemetryapi.DeviceDisplayStateOnline
	}
	return &state
}

func stateDigest(snapshot telemetryapi.DeviceObservationSnapshot) (string, error) {
	type lastKnownState struct {
		State          telemetryapi.DevicePresenceState `json:"state"`
		LastSeenAt     *telemetryapi.Instant            `json:"lastSeenAt"`
		PolicyRevision telemetryapi.PolicyRevision      `json:"policyRevision"`
	}
	var lastKnown *lastKnownState
	if snapshot.Presence.LastKnown != nil {
		lastKnown = &lastKnownState{State: snapshot.Presence.LastKnown.State, LastSeenAt: snapshot.Presence.LastKnown.LastSeenAt, PolicyRevision: snapshot.Presence.LastKnown.PolicyRevision}
	}
	payload := struct {
		TenantId               telemetryapi.UUIDv7                   `json:"tenantId"`
		SiteId                 telemetryapi.UUIDv7                   `json:"siteId"`
		DeviceId               telemetryapi.UUIDv7                   `json:"deviceId"`
		EvaluationAvailability telemetryapi.EvaluationAvailability   `json:"evaluationAvailability"`
		AvailabilityReasons    []telemetryapi.AvailabilityReasonCode `json:"availabilityReasons"`
		Applicability          telemetryapi.PresenceApplicability    `json:"applicability"`
		CurrentState           *telemetryapi.DevicePresenceState     `json:"currentState"`
		LastSeenAt             *telemetryapi.Instant                 `json:"lastSeenAt"`
		PresencePolicyRevision *telemetryapi.PolicyRevision          `json:"presencePolicyRevision"`
		LastKnown              *lastKnownState                       `json:"lastKnown"`
		TelemetryReadiness     telemetryapi.TelemetryReadiness       `json:"telemetryReadiness"`
		DisplayState           *telemetryapi.DeviceDisplayState      `json:"displayState"`
		Values                 []telemetryapi.TelemetryKeyState      `json:"values"`
	}{
		TenantId: snapshot.TenantId, SiteId: snapshot.SiteId, DeviceId: snapshot.DeviceId,
		EvaluationAvailability: snapshot.EvaluationAvailability, AvailabilityReasons: snapshot.AvailabilityReasons,
		Applicability: snapshot.Presence.Applicability, CurrentState: snapshot.Presence.CurrentState,
		LastSeenAt: snapshot.Presence.LastSeenAt, PresencePolicyRevision: snapshot.Presence.PolicyRevision, LastKnown: lastKnown,
		TelemetryReadiness: snapshot.TelemetryReadiness, DisplayState: snapshot.DisplayState, Values: snapshot.Values,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func currentLastKnown(state telemetryapi.DevicePresenceState, lastSeen *time.Time, evaluatedAt time.Time, policyRevision *telemetryapi.PolicyRevision) *telemetryapi.LastKnownPresence {
	var lastSeenAt *telemetryapi.Instant
	if lastSeen != nil {
		value := instant(*lastSeen)
		lastSeenAt = &value
	}
	revision := telemetryapi.PolicyRevision(0)
	if policyRevision != nil {
		revision = *policyRevision
	}
	return &telemetryapi.LastKnownPresence{State: state, LastSeenAt: lastSeenAt, EvaluatedAt: instant(evaluatedAt), PolicyRevision: revision}
}

func greatestAcceptedSignal(values []PresenceSignal, acceptedTypes []string) *PresenceSignal {
	var greatest *PresenceSignal
	for _, value := range values {
		if !signalTypeAllowed(value.Type, acceptedTypes) || value.ObservedAt.IsZero() {
			continue
		}
		value.ObservedAt = value.ObservedAt.UTC()
		if greatest == nil || value.ObservedAt.After(greatest.ObservedAt) {
			copyValue := value
			greatest = &copyValue
		}
	}
	return greatest
}

func signalTypeAllowed(signalType string, acceptedTypes []string) bool {
	if len(acceptedTypes) == 0 {
		return signalType == "SOURCE_ACTIVITY" || signalType == "EXPLICIT_CONNECT"
	}
	return slices.Contains(acceptedTypes, signalType)
}

func instant(value time.Time) telemetryapi.Instant {
	return telemetryapi.Instant(value.UTC().Truncate(time.Millisecond).Format("2006-01-02T15:04:05.000Z"))
}

func cloneLastKnown(value *telemetryapi.LastKnownPresence) *telemetryapi.LastKnownPresence {
	if value == nil {
		return nil
	}
	copyValue := *value
	if value.LastSeenAt != nil {
		copied := *value.LastSeenAt
		copyValue.LastSeenAt = &copied
	}
	return &copyValue
}
func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}
func cloneJSON(value json.RawMessage) json.RawMessage { return append(json.RawMessage(nil), value...) }
