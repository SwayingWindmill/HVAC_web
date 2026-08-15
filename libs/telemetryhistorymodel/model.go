package telemetryhistorymodel

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"
)

const (
	DeviceHistoryAction        = "telemetry.history.read"
	MaximumHistoryRange        = 24 * time.Hour
	MaximumHistoryKeys         = 8
	MaximumPointsPerKey        = 500
	MaximumHistoryResponseRows = MaximumHistoryKeys * MaximumPointsPerKey
)

var (
	telemetryKeyPattern  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.:-]{0,127}$`)
	qualityReasonPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
)

type DeviceHistoryRequest struct {
	DeviceID        string    `json:"deviceId"`
	Keys            []string  `json:"keys"`
	From            time.Time `json:"from"`
	To              time.Time `json:"to"`
	MaxPointsPerKey int       `json:"maxPointsPerKey"`
}

func (request DeviceHistoryRequest) Validate() error {
	return validateHistorySelection(request.DeviceID, request.Keys, request.From, request.To, request.MaxPointsPerKey)
}

func (request DeviceHistoryRequest) Complete(tenantID, siteID string) (DeviceHistoryQuery, error) {
	query := DeviceHistoryQuery{
		TenantID:             tenantID,
		SiteID:               siteID,
		DeviceID:             request.DeviceID,
		Keys:                 append([]string(nil), request.Keys...),
		From:                 request.From,
		To:                   request.To,
		MaxPointsPerKey:      request.MaxPointsPerKey,
	}
	return query.Canonical()
}

type DeviceHistoryQuery struct {
	TenantID             string    `json:"tenantId"`
	SiteID               string    `json:"siteId"`
	DeviceID             string    `json:"deviceId"`
	Keys                 []string  `json:"keys"`
	From                 time.Time `json:"from"`
	To                   time.Time `json:"to"`
	MaxPointsPerKey      int       `json:"maxPointsPerKey"`
}

func (query DeviceHistoryQuery) Validate() error {
	if !validUUIDv7(query.TenantID) || !validUUIDv7(query.SiteID) {
		return errors.New("history scope identifiers must be UUIDv7 values")
	}
	return validateHistorySelection(query.DeviceID, query.Keys, query.From, query.To, query.MaxPointsPerKey)
}

func validateHistorySelection(deviceID string, keys []string, from, to time.Time, maxPointsPerKey int) error {
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
	if maxPointsPerKey < 1 || maxPointsPerKey > MaximumPointsPerKey {
		return fmt.Errorf("history point limit must be between 1 and %d", MaximumPointsPerKey)
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

type DeviceHistoryPoint struct {
	ObservationID  string    `json:"observationId"`
	PointID        string    `json:"pointId"`
	SensorID       *string   `json:"sensorId"`
	SampledAt      time.Time `json:"sampledAt"`
	ReceivedAt     time.Time `json:"receivedAt"`
	Value          float64   `json:"value"`
	Unit           *string   `json:"unit"`
	Quality        Quality   `json:"quality"`
	QualityReasons []string  `json:"qualityReasons"`
	Revision       uint64    `json:"revision"`
}

type DeviceHistorySeries struct {
	Key    string               `json:"key"`
	Points []DeviceHistoryPoint `json:"points"`
}

type DeviceHistoryMetadata struct {
	RequestedFrom   time.Time  `json:"requestedFrom"`
	RequestedTo     time.Time  `json:"requestedTo"`
	DataWatermark   *time.Time `json:"dataWatermark"`
	DatasetRevision string     `json:"datasetRevision"`
	Partial         bool       `json:"partial"`
	MaxPointsPerKey int        `json:"maxPointsPerKey"`
	ReturnedPoints  int        `json:"returnedPoints"`
	TruncatedKeys   []string   `json:"truncatedKeys"`
}

type DeviceHistoryResponse struct {
	SchemaVersion int                   `json:"schemaVersion"`
	TenantID      string                `json:"tenantId"`
	SiteID        string                `json:"siteId"`
	DeviceID      string                `json:"deviceId"`
	Series        []DeviceHistorySeries `json:"series"`
	Metadata      DeviceHistoryMetadata `json:"metadata"`
}

func (response DeviceHistoryResponse) ValidateFor(query DeviceHistoryQuery) error {
	canonical, err := query.Canonical()
	if err != nil {
		return err
	}
	if response.SchemaVersion != 1 || response.TenantID != canonical.TenantID || response.SiteID != canonical.SiteID || response.DeviceID != canonical.DeviceID {
		return errors.New("history response scope is invalid")
	}
	metadata := response.Metadata
	if !metadata.RequestedFrom.Equal(canonical.From) || !metadata.RequestedTo.Equal(canonical.To) || metadata.MaxPointsPerKey != canonical.MaxPointsPerKey || strings.TrimSpace(metadata.DatasetRevision) == "" || len(metadata.DatasetRevision) > 128 {
		return errors.New("history response metadata is invalid")
	}
	if metadata.DataWatermark == nil {
		if !metadata.Partial {
			return errors.New("history response without a watermark must be partial")
		}
	} else {
		if metadata.DataWatermark.IsZero() {
			return errors.New("history response watermark is invalid")
		}
		if _, offset := metadata.DataWatermark.Zone(); offset != 0 {
			return errors.New("history response watermark must use UTC")
		}
		if metadata.DataWatermark.Before(canonical.To) && !metadata.Partial {
			return errors.New("history response watermark does not cover a complete response")
		}
	}
	if len(response.Series) != len(canonical.Keys) {
		return errors.New("history response must contain exactly one series per requested key")
	}
	allowedKeys := make(map[string]struct{}, len(canonical.Keys))
	for _, key := range canonical.Keys {
		allowedKeys[key] = struct{}{}
	}
	seenSeries := make(map[string]struct{}, len(response.Series))
	returned := 0
	hasEmptySeries := false
	for index, series := range response.Series {
		if series.Key != canonical.Keys[index] {
			return errors.New("history response series order is invalid")
		}
		if _, allowed := allowedKeys[series.Key]; !allowed {
			return errors.New("history response contains an unrequested key")
		}
		if _, duplicate := seenSeries[series.Key]; duplicate {
			return errors.New("history response contains a duplicate series")
		}
		seenSeries[series.Key] = struct{}{}
		if series.Points == nil || len(series.Points) > canonical.MaxPointsPerKey {
			return errors.New("history response point count is invalid")
		}
		if len(series.Points) == 0 {
			hasEmptySeries = true
		}
		previous := time.Time{}
		for _, point := range series.Points {
			if !validUUIDv7(point.ObservationID) {
				return errors.New("history response observation ID is invalid")
			}
			if !validUUIDv7(point.PointID) {
				return errors.New("history response point ID is invalid")
			}
			if point.SensorID != nil && !validUUIDv7(*point.SensorID) {
				return errors.New("history response sensor ID is invalid")
			}
			if point.SampledAt.Before(canonical.From) || !point.SampledAt.Before(canonical.To) {
				return errors.New("history response sample is outside the requested range")
			}
			if point.ReceivedAt.IsZero() {
				return errors.New("history response receive time is invalid")
			}
			if math.IsNaN(point.Value) || math.IsInf(point.Value, 0) {
				return errors.New("history response numeric value is invalid")
			}
			if point.Unit != nil && len(*point.Unit) > 64 {
				return errors.New("history response unit is too long")
			}
			if !point.Quality.Valid() {
				return errors.New("history response quality is invalid")
			}
			if point.QualityReasons == nil || len(point.QualityReasons) > 16 {
				return errors.New("history response quality reasons must be explicit and bounded")
			}
			seenReasons := make(map[string]struct{}, len(point.QualityReasons))
			for _, reason := range point.QualityReasons {
				if !qualityReasonPattern.MatchString(reason) {
					return errors.New("history response quality reason is invalid")
				}
				if _, duplicate := seenReasons[reason]; duplicate {
					return errors.New("history response quality reason is duplicated")
				}
				seenReasons[reason] = struct{}{}
			}
			if _, offset := point.SampledAt.Zone(); offset != 0 {
				return errors.New("history response sample time must use UTC")
			}
			if _, offset := point.ReceivedAt.Zone(); offset != 0 {
				return errors.New("history response receive time must use UTC")
			}
			if !previous.IsZero() && point.SampledAt.Before(previous) {
				return errors.New("history response points are not ordered")
			}
			previous = point.SampledAt
			returned++
		}
	}
	if len(seenSeries) != len(allowedKeys) {
		return errors.New("history response is missing a requested series")
	}
	if hasEmptySeries && !metadata.Partial {
		return errors.New("history response with an empty series must be partial")
	}
	if returned != metadata.ReturnedPoints || returned > MaximumHistoryResponseRows {
		return errors.New("history response returned point count is inconsistent")
	}
	seenTruncated := map[string]struct{}{}
	for _, key := range metadata.TruncatedKeys {
		if _, allowed := allowedKeys[key]; !allowed {
			return errors.New("history response truncation references an unrequested key")
		}
		if _, duplicate := seenTruncated[key]; duplicate {
			return errors.New("history response truncation key is duplicated")
		}
		seenTruncated[key] = struct{}{}
	}
	if len(metadata.TruncatedKeys) > 0 && !metadata.Partial {
		return errors.New("truncated history response must be partial")
	}
	return nil
}

func validUUIDv7(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	compact := strings.ReplaceAll(value, "-", "")
	decoded, err := hex.DecodeString(compact)
	return err == nil && len(decoded) == 16 && decoded[6]>>4 == 7 && decoded[8]>>6 == 2
}
