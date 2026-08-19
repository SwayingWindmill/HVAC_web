package telemetryhistorymodel

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

const (
	DeviceHistoryAction        = "telemetry.history.read"
	MaximumHistoryRange        = 24 * time.Hour
	MaximumHistoryKeys         = 8
	MaximumHistoryPageSize     = 500
	MaximumHistoryResponseRows = MaximumHistoryPageSize
	MaximumHistoryCursorBytes  = 4096
)

var (
	telemetryKeyPattern    = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.:-]{0,127}$`)
	qualityReasonPattern   = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
	sourcePartitionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,255}$`)
)

type DeviceHistoryRequest struct {
	DeviceID string    `json:"deviceId"`
	Keys     []string  `json:"keys"`
	From     time.Time `json:"from"`
	To       time.Time `json:"to"`
	PageSize int       `json:"pageSize"`
	Cursor   *string   `json:"cursor,omitempty"`
}

func (request DeviceHistoryRequest) Validate() error {
	return validateHistorySelection(request.DeviceID, request.Keys, request.From, request.To, request.PageSize, request.Cursor)
}

func (request DeviceHistoryRequest) Complete(tenantID, siteID string) (DeviceHistoryQuery, error) {
	query := DeviceHistoryQuery{
		TenantID: tenantID,
		SiteID:   siteID,
		DeviceID: request.DeviceID,
		Keys:     append([]string(nil), request.Keys...),
		From:     request.From,
		To:       request.To,
		PageSize: request.PageSize,
		Cursor:   copyString(request.Cursor),
	}
	return query.Canonical()
}

type DeviceHistoryQuery struct {
	TenantID string    `json:"tenantId"`
	SiteID   string    `json:"siteId"`
	DeviceID string    `json:"deviceId"`
	Keys     []string  `json:"keys"`
	From     time.Time `json:"from"`
	To       time.Time `json:"to"`
	PageSize int       `json:"pageSize"`
	Cursor   *string   `json:"cursor,omitempty"`
}

func (query DeviceHistoryQuery) Validate() error {
	if !validUUIDv7(query.TenantID) || !validUUIDv7(query.SiteID) {
		return errors.New("history scope identifiers must be UUIDv7 values")
	}
	return validateHistorySelection(query.DeviceID, query.Keys, query.From, query.To, query.PageSize, query.Cursor)
}

func validateHistorySelection(deviceID string, keys []string, from, to time.Time, pageSize int, cursor *string) error {
	if !validUUIDv7(deviceID) {
		return errors.New("history device identifier must be a UUIDv7 value")
	}
	if len(keys) == 0 || len(keys) > MaximumHistoryKeys {
		return fmt.Errorf("history query must contain between 1 and %d keys", MaximumHistoryKeys)
	}
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if !telemetryKeyPattern.MatchString(key) {
			return errors.New("history telemetry key syntax is invalid")
		}
		if _, duplicate := seen[key]; duplicate {
			return errors.New("history telemetry key is duplicated")
		}
		seen[key] = struct{}{}
	}
	if from.IsZero() || to.IsZero() || !from.Before(to) {
		return errors.New("history range must be non-empty")
	}
	if _, offset := from.Zone(); offset != 0 {
		return errors.New("history from must use UTC")
	}
	if _, offset := to.Zone(); offset != 0 {
		return errors.New("history to must use UTC")
	}
	if to.Sub(from) > MaximumHistoryRange {
		return errors.New("history range exceeds 24 hours")
	}
	if pageSize < 1 || pageSize > MaximumHistoryPageSize {
		return fmt.Errorf("history page size must be between 1 and %d", MaximumHistoryPageSize)
	}
	if cursor != nil {
		value := strings.TrimSpace(*cursor)
		if value == "" || len(value) > MaximumHistoryCursorBytes {
			return errors.New("history cursor is invalid")
		}
	}
	return nil
}

func (query DeviceHistoryQuery) Canonical() (DeviceHistoryQuery, error) {
	if err := query.Validate(); err != nil {
		return DeviceHistoryQuery{}, err
	}
	canonical := query
	canonical.Keys = append([]string(nil), query.Keys...)
	slices.Sort(canonical.Keys)
	canonical.From = canonical.From.UTC()
	canonical.To = canonical.To.UTC()
	canonical.Cursor = copyString(query.Cursor)
	if canonical.Cursor != nil {
		trimmed := strings.TrimSpace(*canonical.Cursor)
		canonical.Cursor = &trimmed
	}
	return canonical, nil
}

func (query DeviceHistoryQuery) ScopeDigest() (string, error) {
	canonical, err := query.Canonical()
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func (query DeviceHistoryQuery) CursorScopeDigest() (string, error) {
	canonical, err := query.Canonical()
	if err != nil {
		return "", err
	}
	canonical.Cursor = nil
	payload, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

type Quality string

const (
	QualityGood      Quality = "GOOD"
	QualityPartial   Quality = "PARTIAL"
	QualityEstimated Quality = "ESTIMATED"
	QualityManual    Quality = "MANUAL"
	QualityStale     Quality = "STALE"
	QualityInvalid   Quality = "INVALID"
)

func (quality Quality) Valid() bool {
	switch quality {
	case QualityGood, QualityPartial, QualityEstimated, QualityManual, QualityStale, QualityInvalid:
		return true
	default:
		return false
	}
}

type Acceptance string

const (
	AcceptanceAccepted   Acceptance = "ACCEPTED"
	AcceptanceOutOfOrder Acceptance = "OUT_OF_ORDER"
)

func (value Acceptance) Valid() bool {
	return value == AcceptanceAccepted || value == AcceptanceOutOfOrder
}

type ValueType string

const (
	ValueTypeNumber  ValueType = "NUMBER"
	ValueTypeString  ValueType = "STRING"
	ValueTypeBoolean ValueType = "BOOLEAN"
	ValueTypeJSON    ValueType = "JSON"
)

func (value ValueType) Valid() bool {
	switch value {
	case ValueTypeNumber, ValueTypeString, ValueTypeBoolean, ValueTypeJSON:
		return true
	default:
		return false
	}
}

type PointType string

const (
	PointTypeTelemetry PointType = "TELEMETRY"
	PointTypeCounter   PointType = "COUNTER"
	PointTypeState     PointType = "STATE"
	PointTypeSetting   PointType = "SETTING"
	PointTypeCommand   PointType = "COMMAND"
)

func (value PointType) Valid() bool {
	switch value {
	case PointTypeTelemetry, PointTypeCounter, PointTypeState, PointTypeSetting, PointTypeCommand:
		return true
	default:
		return false
	}
}

type SourcePosition struct {
	Partition string `json:"partition"`
	Offset    int64  `json:"offset"`
	EventID   string `json:"eventId"`
}

func (position SourcePosition) validate() error {
	if !sourcePartitionPattern.MatchString(position.Partition) || position.Offset < 0 || !validUUIDv7(position.EventID) {
		return errors.New("history response source position is invalid")
	}
	return nil
}

type DeviceHistoryObservation struct {
	ObservationID  string          `json:"observationId"`
	TelemetryKey   string          `json:"telemetryKey"`
	PointID        string          `json:"pointId"`
	SensorID       *string         `json:"sensorId"`
	PointType      PointType       `json:"pointType"`
	PointRevision  uint64          `json:"pointRevision"`
	SampledAt      time.Time       `json:"sampledAt"`
	ReceivedAt     time.Time       `json:"receivedAt"`
	Acceptance     Acceptance      `json:"acceptance"`
	ValueType      ValueType       `json:"valueType"`
	Value          json.RawMessage `json:"value"`
	Unit           *string         `json:"unit"`
	Quality        Quality         `json:"quality"`
	QualityReasons []string        `json:"qualityReasons"`
	SourcePosition SourcePosition  `json:"sourcePosition"`
}

type DeviceHistoryMetadata struct {
	RequestedFrom        time.Time  `json:"requestedFrom"`
	RequestedTo          time.Time  `json:"requestedTo"`
	ProjectionWatermark  *time.Time `json:"projectionWatermark"`
	PageSize             int        `json:"pageSize"`
	ReturnedObservations int        `json:"returnedObservations"`
	NextCursor           *string    `json:"nextCursor"`
}

type DeviceHistoryResponse struct {
	SchemaVersion int                        `json:"schemaVersion"`
	TenantID      string                     `json:"tenantId"`
	SiteID        string                     `json:"siteId"`
	DeviceID      string                     `json:"deviceId"`
	Observations  []DeviceHistoryObservation `json:"observations"`
	Metadata      DeviceHistoryMetadata      `json:"metadata"`
}

func (response DeviceHistoryResponse) ValidateFor(query DeviceHistoryQuery) error {
	canonical, err := query.Canonical()
	if err != nil {
		return err
	}
	if response.SchemaVersion != 2 || response.TenantID != canonical.TenantID || response.SiteID != canonical.SiteID || response.DeviceID != canonical.DeviceID {
		return errors.New("history response scope is invalid")
	}
	metadata := response.Metadata
	if !metadata.RequestedFrom.Equal(canonical.From) || !metadata.RequestedTo.Equal(canonical.To) || metadata.PageSize != canonical.PageSize {
		return errors.New("history response metadata is invalid")
	}
	if metadata.ProjectionWatermark != nil {
		if metadata.ProjectionWatermark.IsZero() {
			return errors.New("history projection watermark is invalid")
		}
		if _, offset := metadata.ProjectionWatermark.Zone(); offset != 0 {
			return errors.New("history projection watermark must use UTC")
		}
	}
	if metadata.NextCursor != nil {
		cursor := strings.TrimSpace(*metadata.NextCursor)
		if cursor == "" || len(cursor) > MaximumHistoryCursorBytes {
			return errors.New("history next cursor is invalid")
		}
	}
	if response.Observations == nil || len(response.Observations) > canonical.PageSize || len(response.Observations) > MaximumHistoryResponseRows {
		return errors.New("history response observation count is invalid")
	}
	if metadata.ReturnedObservations != len(response.Observations) {
		return errors.New("history response returned observation count is inconsistent")
	}
	allowedKeys := make(map[string]struct{}, len(canonical.Keys))
	for _, key := range canonical.Keys {
		allowedKeys[key] = struct{}{}
	}
	seenObservations := make(map[string]struct{}, len(response.Observations))
	var previous *DeviceHistoryObservation
	for index := range response.Observations {
		observation := &response.Observations[index]
		if err := validateObservation(*observation, canonical, allowedKeys); err != nil {
			return err
		}
		if _, duplicate := seenObservations[observation.ObservationID]; duplicate {
			return errors.New("history response contains a duplicate observation")
		}
		seenObservations[observation.ObservationID] = struct{}{}
		if previous != nil && compareObservationOrder(*previous, *observation) >= 0 {
			return errors.New("history response observations are not in stable order")
		}
		previous = observation
	}
	if metadata.NextCursor != nil && len(response.Observations) == 0 {
		return errors.New("empty history response cannot contain a next cursor")
	}
	return nil
}

func validateObservation(observation DeviceHistoryObservation, query DeviceHistoryQuery, allowedKeys map[string]struct{}) error {
	if !validUUIDv7(observation.ObservationID) || !validUUIDv7(observation.PointID) {
		return errors.New("history response observation identity is invalid")
	}
	if observation.SensorID != nil && !validUUIDv7(*observation.SensorID) {
		return errors.New("history response sensor ID is invalid")
	}
	if _, allowed := allowedKeys[observation.TelemetryKey]; !allowed {
		return errors.New("history response contains an unrequested key")
	}
	if !observation.PointType.Valid() || observation.PointRevision < 1 {
		return errors.New("history response point semantics are invalid")
	}
	if observation.SampledAt.Before(query.From) || !observation.SampledAt.Before(query.To) {
		return errors.New("history response sample is outside the requested range")
	}
	if observation.ReceivedAt.IsZero() {
		return errors.New("history response receive time is invalid")
	}
	if _, offset := observation.SampledAt.Zone(); offset != 0 {
		return errors.New("history response sample time must use UTC")
	}
	if _, offset := observation.ReceivedAt.Zone(); offset != 0 {
		return errors.New("history response receive time must use UTC")
	}
	if !observation.Acceptance.Valid() || !observation.ValueType.Valid() || !observation.Quality.Valid() {
		return errors.New("history response typed state is invalid")
	}
	if err := validateTypedValue(observation.ValueType, observation.Value); err != nil {
		return err
	}
	if observation.Unit != nil && len(*observation.Unit) > 64 {
		return errors.New("history response unit is too long")
	}
	if observation.QualityReasons == nil || len(observation.QualityReasons) > 16 {
		return errors.New("history response quality reasons must be explicit and bounded")
	}
	seenReasons := make(map[string]struct{}, len(observation.QualityReasons))
	for _, reason := range observation.QualityReasons {
		if !qualityReasonPattern.MatchString(reason) {
			return errors.New("history response quality reason is invalid")
		}
		if _, duplicate := seenReasons[reason]; duplicate {
			return errors.New("history response quality reason is duplicated")
		}
		seenReasons[reason] = struct{}{}
	}
	return observation.SourcePosition.validate()
}

func validateTypedValue(valueType ValueType, raw json.RawMessage) error {
	if len(raw) == 0 || !json.Valid(raw) {
		return errors.New("history response value is invalid JSON")
	}
	switch valueType {
	case ValueTypeNumber:
		var value float64
		if json.Unmarshal(raw, &value) != nil {
			return errors.New("history response numeric value is invalid")
		}
	case ValueTypeString:
		var value string
		if json.Unmarshal(raw, &value) != nil {
			return errors.New("history response string value is invalid")
		}
	case ValueTypeBoolean:
		var value bool
		if json.Unmarshal(raw, &value) != nil {
			return errors.New("history response boolean value is invalid")
		}
	case ValueTypeJSON:
		var value any
		if json.Unmarshal(raw, &value) != nil {
			return errors.New("history response JSON value is invalid")
		}
		switch value.(type) {
		case map[string]any, []any:
		default:
			return errors.New("history response JSON value must be an object or array")
		}
	default:
		return errors.New("history response value type is invalid")
	}
	return nil
}

func compareObservationOrder(left, right DeviceHistoryObservation) int {
	if left.TelemetryKey < right.TelemetryKey {
		return -1
	}
	if left.TelemetryKey > right.TelemetryKey {
		return 1
	}
	if left.SampledAt.Before(right.SampledAt) {
		return -1
	}
	if left.SampledAt.After(right.SampledAt) {
		return 1
	}
	return strings.Compare(left.ObservationID, right.ObservationID)
}

func validUUIDv7(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	compact := strings.ReplaceAll(value, "-", "")
	decoded, err := hex.DecodeString(compact)
	return err == nil && len(decoded) == 16 && decoded[6]>>4 == 7 && decoded[8]>>6 == 2
}

func copyString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
