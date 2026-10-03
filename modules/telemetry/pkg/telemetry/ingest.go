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
	QuarantineMappingNotFound      QuarantineReason = "MAPPING_NOT_FOUND"
	QuarantineMappingConflict      QuarantineReason = "MAPPING_CONFLICT"
	QuarantinePointMappingNotFound QuarantineReason = "POINT_MAPPING_NOT_FOUND"
	QuarantinePolicyNotConfigured  QuarantineReason = "POLICY_NOT_CONFIGURED"
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

// ObservationCandidate is one value from a source. Live sources (Connectivity) send the
// Device and Point they resolved from Registry; history replay names only the Device and
// key and Telemetry resolves the Point from its own last accepted mapping.
type ObservationCandidate struct {
	SourceID           string
	SourcePath         SourcePath
	ExternalEntityType string
	ExternalID         string
	Device             *ResolvedDevice
	Point              *ResolvedPoint
	TelemetryKey       string
	Value              json.RawMessage
	ValueType          string
	Unit               *string
	WireQuality        uint8
	SampledAt          time.Time
	ReceivedAt         time.Time
	Position           SourcePosition
}

type ResolvedDevice struct {
	TenantID string
	SiteID   string
	DeviceID string
}

type ResolvedPoint struct {
	PointID                string
	SensorID               *string
	PointType              string
	ValueType              string
	Unit                   *string
	CounterDecreaseMode    *string
	CounterRolloverModulus *float64
	PointRevision          int64
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
	Device           *ResolvedDevice
	DeviceConflict   bool
	Point            *ResolvedPoint
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

	decision, resolved := resolveObservationIdentity(facts)
	if !resolved {
		return decision
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

	decision, resolved := resolveObservationIdentity(facts)
	if !resolved {
		return decision
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

// resolveObservationIdentity starts a quarantined decision for the resolved Device and
// Point; resolved is false when the observation must stay quarantined for its identity.
func resolveObservationIdentity(facts ObservationFacts) (ObservationDecision, bool) {
	decision := ObservationDecision{Status: ObservationQuarantined, Quality: QualityInvalid, AdvancePosition: true}
	switch {
	case facts.DeviceConflict:
		decision.QuarantineReason = QuarantineMappingConflict
		return decision, false
	case facts.Device == nil:
		decision.QuarantineReason = QuarantineMappingNotFound
		return decision, false
	}
	decision.TenantID, decision.SiteID, decision.DeviceID = facts.Device.TenantID, facts.Device.SiteID, facts.Device.DeviceID
	if facts.Point == nil {
		decision.QuarantineReason = QuarantinePointMappingNotFound
		return decision, false
	}
	point := facts.Point
	decision.PointID = point.PointID
	decision.PointType = point.PointType
	decision.PointRevision = point.PointRevision
	if point.CounterDecreaseMode != nil {
		decision.CounterDecreaseMode = *point.CounterDecreaseMode
	}
	if point.CounterRolloverModulus != nil {
		modulus := *point.CounterRolloverModulus
		decision.CounterRolloverModulus = &modulus
	}
	if point.SensorID != nil {
		decision.SensorID = *point.SensorID
	}
	return decision, true
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
