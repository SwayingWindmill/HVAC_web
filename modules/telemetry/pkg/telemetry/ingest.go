package telemetry

import (
	"encoding/json"
	"slices"
	"time"
)

type SourcePath string

const (
	SourcePathWebhook        SourcePath = "WEBHOOK"
	SourcePathPush           SourcePath = "PUSH"
	SourcePathPoll           SourcePath = "POLL"
	SourcePathReconciliation SourcePath = "RECONCILIATION"
	SourcePathHistoryReplay  SourcePath = "HISTORY_REPLAY"
)

func (path SourcePath) Valid() bool {
	return path == SourcePathWebhook || path == SourcePathPush || path == SourcePathPoll || path == SourcePathReconciliation || path == SourcePathHistoryReplay
}

type ObservationStatus string

const (
	ObservationAccepted    ObservationStatus = "ACCEPTED"
	ObservationRejected    ObservationStatus = "REJECTED"
	ObservationQuarantined ObservationStatus = "QUARANTINED"
	ObservationDuplicate   ObservationStatus = "DUPLICATE"
	ObservationOutOfOrder  ObservationStatus = "OUT_OF_ORDER"
)

type ObservationQuality string

const (
	QualityGood      ObservationQuality = "GOOD"
	QualityPartial   ObservationQuality = "PARTIAL"
	QualityEstimated ObservationQuality = "ESTIMATED"
	QualityManual    ObservationQuality = "MANUAL"
	QualityStale     ObservationQuality = "STALE"
	QualityInvalid   ObservationQuality = "INVALID"
)

type QualityReason string

const (
	QualityReasonSourceUntrusted   QualityReason = "SOURCE_UNTRUSTED"
	QualityReasonTypeMismatch      QualityReason = "TYPE_MISMATCH"
	QualityReasonUnitMismatch      QualityReason = "UNIT_MISMATCH"
	QualityReasonOutOfRange        QualityReason = "OUT_OF_RANGE"
	QualityReasonClockAhead        QualityReason = "CLOCK_AHEAD"
	QualityReasonClockBehind       QualityReason = "CLOCK_BEHIND"
	QualityReasonSourceLagExceeded QualityReason = "SOURCE_LAG_EXCEEDED"
	QualityReasonDuplicate         QualityReason = "DUPLICATE"
	QualityReasonOutOfOrder        QualityReason = "OUT_OF_ORDER"
	QualityReasonReplayed          QualityReason = "REPLAYED"
)

type QuarantineReason string

const (
	QuarantineMappingNotFound       QuarantineReason = "MAPPING_NOT_FOUND"
	QuarantineMappingConflict       QuarantineReason = "MAPPING_CONFLICT"
	QuarantineMappingQuarantined    QuarantineReason = "MAPPING_QUARANTINED"
	QuarantineMappingRetired        QuarantineReason = "MAPPING_RETIRED"
	QuarantinePointMappingNotFound  QuarantineReason = "POINT_MAPPING_NOT_FOUND"
	QuarantinePointMappingConflict  QuarantineReason = "POINT_MAPPING_CONFLICT"
	QuarantinePointMappingQuarantined QuarantineReason = "POINT_MAPPING_QUARANTINED"
	QuarantinePolicyNotConfigured   QuarantineReason = "POLICY_NOT_CONFIGURED"
)

type SourcePosition struct {
	Partition string
	Offset    int64
	EventID   string
}

type SourcePositionHead struct {
	Offset  int64
	EventID string
}

type ObservationCandidate struct {
	IntegrationInstanceID string
	SourcePath            SourcePath
	ExternalEntityType    string
	ExternalID            string
	TelemetryKey          string
	Value                 json.RawMessage
	ValueType             string
	Unit                  *string
	WireQuality           uint8
	SampledAt             time.Time
	ReceivedAt            time.Time
	Position              SourcePosition
}

type RuntimeBinding struct {
	TenantID              string
	DeviceID              string
	SiteID                string
	IntegrationInstanceID string
	ExternalEntityType    string
	ExternalID            string
	Status                string
	ValidFrom             time.Time
	ValidTo               *time.Time
}

type RuntimePointBinding struct {
	TenantID                string
	SiteID                  string
	PointID                 string
	SensorID                *string
	DeviceID                string
	TelemetryKey            string
	PointType               string
	ValueType               string
	Unit                    *string
	CounterDecreaseMode     *string
	CounterRolloverModulus  *float64
	Status                  string
	PointRevision           int64
	ValidFrom               time.Time
	ValidTo                 *time.Time
}

type ObservationPolicy struct {
	Revision               int64
	PresencePolicyRevision int64
	ValueType              string
	Unit                   *string
	MinimumNumber          *float64
	MaximumNumber          *float64
	MaxFutureClockSkew     time.Duration
	MaxSourceLag           time.Duration
}

type ObservationFacts struct {
	Bindings         []RuntimeBinding
	PointBindings    []RuntimePointBinding
	Policy           *ObservationPolicy
	CurrentPosition  *SourcePositionHead
	EventAlreadySeen bool
	LatestSampledAt  *time.Time
}

type ObservationDecision struct {
	TenantID               string
	PointID                string
	SensorID               string
	PointType              string
	PointRevision          int64
	CounterDecreaseMode    string
	CounterRolloverModulus *float64
	Status                 ObservationStatus
	Quality                ObservationQuality
	QualityReasons         []QualityReason
	QuarantineReason       QuarantineReason
	DeviceID               string
	SiteID                 string
	PolicyRevision         int64
	PresencePolicyRevision int64
	AdvancePosition        bool
	ReplaceLatest          bool
	EmitPresenceSignal     bool
	ReevaluateSnapshot     bool
}

func EvaluateObservation(candidate ObservationCandidate, facts ObservationFacts, evaluatedAt time.Time) ObservationDecision {
	if facts.EventAlreadySeen {
		return terminalObservation(ObservationDuplicate, QualityInvalid, QualityReasonDuplicate, false)
	}
	if facts.CurrentPosition != nil {
		switch {
		case candidate.Position.Offset < facts.CurrentPosition.Offset:
			return terminalObservation(ObservationOutOfOrder, QualityInvalid, QualityReasonOutOfOrder, false)
		case candidate.Position.Offset == facts.CurrentPosition.Offset:
			return terminalObservation(ObservationDuplicate, QualityInvalid, QualityReasonReplayed, false)
		}
	}

	binding, quarantine := resolveRuntimeBinding(candidate, facts.Bindings)
	if quarantine != "" {
		return ObservationDecision{
			Status: ObservationQuarantined, Quality: QualityInvalid, QuarantineReason: quarantine, AdvancePosition: true,
		}
	}
	decision := ObservationDecision{
		TenantID: binding.TenantID, DeviceID: binding.DeviceID, SiteID: binding.SiteID,
		Status: ObservationQuarantined, Quality: QualityInvalid, AdvancePosition: true,
	}
	pointBinding, pointQuarantine := resolveRuntimePointBinding(candidate, binding, facts.PointBindings)
	if pointQuarantine != "" {
		decision.QuarantineReason = pointQuarantine
		return decision
	}
	decision.PointID = pointBinding.PointID
	decision.PointType = pointBinding.PointType
	decision.PointRevision = pointBinding.PointRevision
	if pointBinding.CounterDecreaseMode != nil {
		decision.CounterDecreaseMode = *pointBinding.CounterDecreaseMode
	}
	if pointBinding.CounterRolloverModulus != nil {
		modulus := *pointBinding.CounterRolloverModulus
		decision.CounterRolloverModulus = &modulus
	}
	if pointBinding.SensorID != nil {
		decision.SensorID = *pointBinding.SensorID
	}
	if facts.Policy == nil || facts.Policy.Revision < 1 {
		decision.QuarantineReason = QuarantinePolicyNotConfigured
		return decision
	}
	decision.PolicyRevision = facts.Policy.Revision
	decision.PresencePolicyRevision = facts.Policy.PresencePolicyRevision

	reasons, rejected := validateObservation(candidate, *facts.Policy, evaluatedAt)
	decision.QualityReasons = reasons
	decision.QuarantineReason = ""
	if rejected {
		decision.Status = ObservationRejected
		decision.Quality = QualityInvalid
		decision.ReevaluateSnapshot = true
		return decision
	}

	decision.Quality = QualityGood
	if slices.Contains(reasons, QualityReasonSourceUntrusted) {
		decision.Quality = QualityPartial
	}
	if slices.Contains(reasons, QualityReasonSourceLagExceeded) || slices.Contains(reasons, QualityReasonClockBehind) {
		decision.Quality = QualityStale
	}
	if facts.LatestSampledAt != nil && !candidate.SampledAt.After(facts.LatestSampledAt.UTC()) {
		decision.Status = ObservationOutOfOrder
		decision.QualityReasons = append(decision.QualityReasons, QualityReasonOutOfOrder)
		return decision
	}

	decision.Status = ObservationAccepted
	decision.ReevaluateSnapshot = true
	decision.ReplaceLatest = true
	decision.EmitPresenceSignal = true
	return decision
}

func EvaluateHistoricalObservation(candidate ObservationCandidate, facts ObservationFacts, evaluatedAt time.Time) ObservationDecision {
	if facts.EventAlreadySeen {
		return terminalObservation(ObservationDuplicate, QualityInvalid, QualityReasonDuplicate, false)
	}
	if facts.CurrentPosition != nil {
		switch {
		case candidate.Position.Offset < facts.CurrentPosition.Offset:
			return terminalObservation(ObservationOutOfOrder, QualityInvalid, QualityReasonOutOfOrder, false)
		case candidate.Position.Offset == facts.CurrentPosition.Offset:
			return terminalObservation(ObservationDuplicate, QualityInvalid, QualityReasonReplayed, false)
		}
	}

	binding, quarantine := resolveRuntimeBinding(candidate, facts.Bindings)
	if quarantine != "" {
		return ObservationDecision{Status: ObservationQuarantined, Quality: QualityInvalid, QuarantineReason: quarantine, AdvancePosition: true}
	}
	decision := ObservationDecision{
		TenantID: binding.TenantID, DeviceID: binding.DeviceID, SiteID: binding.SiteID,
		Status: ObservationQuarantined, Quality: QualityInvalid, AdvancePosition: true,
	}
	pointBinding, pointQuarantine := resolveRuntimePointBinding(candidate, binding, facts.PointBindings)
	if pointQuarantine != "" {
		decision.QuarantineReason = pointQuarantine
		return decision
	}
	decision.PointID = pointBinding.PointID
	decision.PointType = pointBinding.PointType
	decision.PointRevision = pointBinding.PointRevision
	if pointBinding.CounterDecreaseMode != nil {
		decision.CounterDecreaseMode = *pointBinding.CounterDecreaseMode
	}
	if pointBinding.CounterRolloverModulus != nil {
		modulus := *pointBinding.CounterRolloverModulus
		decision.CounterRolloverModulus = &modulus
	}
	if pointBinding.SensorID != nil {
		decision.SensorID = *pointBinding.SensorID
	}
	if facts.Policy == nil || facts.Policy.Revision < 1 {
		decision.QuarantineReason = QuarantinePolicyNotConfigured
		return decision
	}
	decision.PolicyRevision = facts.Policy.Revision
	decision.PresencePolicyRevision = facts.Policy.PresencePolicyRevision

	reasons, rejected := validateObservation(candidate, *facts.Policy, evaluatedAt)
	reasons = slices.DeleteFunc(reasons, func(reason QualityReason) bool { return reason == QualityReasonSourceLagExceeded })
	decision.QualityReasons = reasons
	decision.QuarantineReason = ""
	if rejected {
		decision.Status = ObservationRejected
		decision.Quality = QualityInvalid
		return decision
	}

	decision.Status = ObservationAccepted
	decision.Quality = QualityGood
	if slices.Contains(reasons, QualityReasonSourceUntrusted) {
		decision.Quality = QualityPartial
	}
	return decision
}

func terminalObservation(status ObservationStatus, quality ObservationQuality, reason QualityReason, advance bool) ObservationDecision {
	return ObservationDecision{Status: status, Quality: quality, QualityReasons: []QualityReason{reason}, AdvancePosition: advance}
}

func resolveRuntimeBinding(candidate ObservationCandidate, bindings []RuntimeBinding) (RuntimeBinding, QuarantineReason) {
	active := make([]RuntimeBinding, 0, 1)
	hasQuarantined := false
	hasRetired := false
	observedAt := candidate.ReceivedAt.UTC()
	for _, binding := range bindings {
		if binding.IntegrationInstanceID != candidate.IntegrationInstanceID || binding.ExternalEntityType != candidate.ExternalEntityType || binding.ExternalID != candidate.ExternalID {
			continue
		}
		switch binding.Status {
		case "ACTIVE":
			if !binding.ValidFrom.IsZero() && observedAt.Before(binding.ValidFrom.UTC()) {
				continue
			}
			if binding.ValidTo != nil && !observedAt.Before(binding.ValidTo.UTC()) {
				hasRetired = true
				continue
			}
			active = append(active, binding)
		case "QUARANTINED":
			hasQuarantined = true
		case "RETIRED":
			hasRetired = true
		}
	}
	if len(active) > 1 {
		return RuntimeBinding{}, QuarantineMappingConflict
	}
	if len(active) == 1 {
		return active[0], ""
	}
	if hasQuarantined {
		return RuntimeBinding{}, QuarantineMappingQuarantined
	}
	if hasRetired {
		return RuntimeBinding{}, QuarantineMappingRetired
	}
	return RuntimeBinding{}, QuarantineMappingNotFound
}

func resolveRuntimePointBinding(candidate ObservationCandidate, deviceBinding RuntimeBinding, bindings []RuntimePointBinding) (RuntimePointBinding, QuarantineReason) {
	matched := make([]RuntimePointBinding, 0, 1)
	hasQuarantined := false
	sampledAt := candidate.SampledAt.UTC()
	for _, binding := range bindings {
		if binding.TenantID != deviceBinding.TenantID || binding.SiteID != deviceBinding.SiteID ||
			binding.DeviceID != deviceBinding.DeviceID || binding.TelemetryKey != candidate.TelemetryKey {
			continue
		}
		if !binding.ValidFrom.IsZero() && sampledAt.Before(binding.ValidFrom.UTC()) {
			continue
		}
		if binding.ValidTo != nil && !sampledAt.Before(binding.ValidTo.UTC()) {
			continue
		}
		switch binding.Status {
		case "ACTIVE", "RETIRED":
			matched = append(matched, binding)
		case "QUARANTINED":
			hasQuarantined = true
		}
	}
	if len(matched) > 1 {
		return RuntimePointBinding{}, QuarantinePointMappingConflict
	}
	if len(matched) == 1 {
		return matched[0], ""
	}
	if hasQuarantined {
		return RuntimePointBinding{}, QuarantinePointMappingQuarantined
	}
	return RuntimePointBinding{}, QuarantinePointMappingNotFound
}

func validateObservation(candidate ObservationCandidate, policy ObservationPolicy, evaluatedAt time.Time) ([]QualityReason, bool) {
	reasons := make([]QualityReason, 0, 5)
	rejected := false
	if candidate.WireQuality != 0 {
		reasons = append(reasons, QualityReasonSourceUntrusted)
	}

	actualType, number, validValue := observationValueType(candidate.Value)
	_, contractValueError := contractTelemetryValue(candidate.Value, candidate.ValueType)
	if !validValue || contractValueError != nil || candidate.ValueType != policy.ValueType || actualType != candidate.ValueType {
		reasons = append(reasons, QualityReasonTypeMismatch)
		rejected = true
	}
	if !equalOptionalString(candidate.Unit, policy.Unit) {
		reasons = append(reasons, QualityReasonUnitMismatch)
		rejected = true
	}
	if validValue && actualType == "NUMBER" {
		if policy.MinimumNumber != nil && number < *policy.MinimumNumber || policy.MaximumNumber != nil && number > *policy.MaximumNumber {
			reasons = append(reasons, QualityReasonOutOfRange)
			rejected = true
		}
	}
	clockReference := candidate.ReceivedAt
	if clockReference.IsZero() {
		clockReference = evaluatedAt
	}
	if candidate.SampledAt.After(clockReference.Add(policy.MaxFutureClockSkew)) {
		reasons = append(reasons, QualityReasonClockAhead)
		rejected = true
	}
	if policy.MaxSourceLag > 0 && clockReference.Sub(candidate.SampledAt) > policy.MaxSourceLag {
		reasons = append(reasons, QualityReasonSourceLagExceeded)
	}
	return canonicalIngestReasons(reasons), rejected
}

func observationValueType(raw json.RawMessage) (string, float64, bool) {
	var value any
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil || value == nil {
		return "", 0, false
	}
	switch typed := value.(type) {
	case float64:
		return "NUMBER", typed, true
	case string:
		return "STRING", 0, true
	case bool:
		return "BOOLEAN", 0, true
	case map[string]any, []any:
		return "JSON", 0, true
	default:
		return "", 0, false
	}
}

func equalOptionalString(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func canonicalIngestReasons(values []QualityReason) []QualityReason {
	order := map[QualityReason]int{
		QualityReasonSourceUntrusted: 1, QualityReasonTypeMismatch: 2, QualityReasonUnitMismatch: 3,
		QualityReasonOutOfRange: 4, QualityReasonClockAhead: 5, QualityReasonClockBehind: 6,
		QualityReasonSourceLagExceeded: 7, QualityReasonDuplicate: 8, QualityReasonOutOfOrder: 9, QualityReasonReplayed: 10,
	}
	seen := make(map[QualityReason]struct{}, len(values))
	result := make([]QualityReason, 0, len(values))
	for _, value := range values {
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	slices.SortFunc(result, func(left, right QualityReason) int { return order[left] - order[right] })
	return result
}
