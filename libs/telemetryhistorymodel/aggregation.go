package telemetryhistorymodel

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	_ "time/tzdata"
)

const (
	DeviceHistoryAggregateAction      = "telemetry.history.aggregate"
	MaximumAggregateRange             = 366 * 24 * time.Hour
	MaximumFineAggregateBucketsPerKey = 720
	MaximumAggregateBucketsPerKey     = 1000
	MaximumAggregateBuckets           = 5760
)

type AggregateGranularity string

const (
	AggregateGranularityMinute      AggregateGranularity = "MINUTE"
	AggregateGranularityFiveMinute  AggregateGranularity = "FIVE_MINUTE"
	AggregateGranularityQuarterHour AggregateGranularity = "QUARTER_HOUR"
	AggregateGranularityHour        AggregateGranularity = "HOUR"
	AggregateGranularityDay         AggregateGranularity = "DAY"
	AggregateGranularityWeek        AggregateGranularity = "WEEK"
	AggregateGranularityMonth       AggregateGranularity = "MONTH"
)

func (value AggregateGranularity) Valid() bool {
	switch value {
	case AggregateGranularityMinute, AggregateGranularityFiveMinute, AggregateGranularityQuarterHour, AggregateGranularityHour, AggregateGranularityDay, AggregateGranularityWeek, AggregateGranularityMonth:
		return true
	default:
		return false
	}
}

func (value AggregateGranularity) MaxBucketsPerKey() int {
	switch value {
	case AggregateGranularityMinute, AggregateGranularityFiveMinute:
		return MaximumFineAggregateBucketsPerKey
	default:
		return MaximumAggregateBucketsPerKey
	}
}

type AggregateQualityPolicy string

const (
	AggregateQualityValidOnly AggregateQualityPolicy = "VALID_ONLY"
	AggregateQualityUsable    AggregateQualityPolicy = "USABLE"
)

func (value AggregateQualityPolicy) Valid() bool {
	return value == AggregateQualityValidOnly || value == AggregateQualityUsable
}

type DeviceHistoryAggregateRequest struct {
	DeviceID      string                 `json:"deviceId"`
	Keys          []string               `json:"keys"`
	From          time.Time              `json:"from"`
	To            time.Time              `json:"to"`
	Granularity   AggregateGranularity   `json:"granularity"`
	Timezone      string                 `json:"timezone"`
	QualityPolicy AggregateQualityPolicy `json:"qualityPolicy"`
}

func (request DeviceHistoryAggregateRequest) Validate() error {
	return validateAggregateSelection(request.DeviceID, request.Keys, request.From, request.To, request.Granularity, request.Timezone, request.QualityPolicy)
}

func (request DeviceHistoryAggregateRequest) Complete(tenantID, siteID string) (DeviceHistoryAggregateQuery, error) {
	query := DeviceHistoryAggregateQuery{
		TenantID: tenantID, SiteID: siteID, DeviceID: request.DeviceID, Keys: append([]string(nil), request.Keys...),
		From: request.From, To: request.To, Granularity: request.Granularity, Timezone: request.Timezone, QualityPolicy: request.QualityPolicy,
	}
	return query.Canonical()
}

type DeviceHistoryAggregateQuery struct {
	TenantID      string                 `json:"tenantId"`
	SiteID        string                 `json:"siteId"`
	DeviceID      string                 `json:"deviceId"`
	Keys          []string               `json:"keys"`
	From          time.Time              `json:"from"`
	To            time.Time              `json:"to"`
	Granularity   AggregateGranularity   `json:"granularity"`
	Timezone      string                 `json:"timezone"`
	QualityPolicy AggregateQualityPolicy `json:"qualityPolicy"`
}

func (query DeviceHistoryAggregateQuery) Validate() error {
	if !validUUIDv7(query.TenantID) || !validUUIDv7(query.SiteID) {
		return errors.New("history aggregate scope identifiers must be UUIDv7 values")
	}
	return validateAggregateSelection(query.DeviceID, query.Keys, query.From, query.To, query.Granularity, query.Timezone, query.QualityPolicy)
}

func validateAggregateSelection(deviceID string, keys []string, from, to time.Time, granularity AggregateGranularity, timezone string, qualityPolicy AggregateQualityPolicy) error {
	if !validUUIDv7(deviceID) {
		return errors.New("history aggregate device identifier must be UUIDv7")
	}
	if len(keys) == 0 || len(keys) > MaximumHistoryKeys {
		return errors.New("history aggregate keys are invalid")
	}
	seen := map[string]struct{}{}
	for _, key := range keys {
		if !telemetryKeyPattern.MatchString(key) {
			return errors.New("history aggregate telemetry key syntax is invalid")
		}
		if _, duplicate := seen[key]; duplicate {
			return errors.New("history aggregate telemetry key is duplicated")
		}
		seen[key] = struct{}{}
	}
	if from.IsZero() || to.IsZero() || !from.Before(to) || to.Sub(from) > MaximumAggregateRange {
		return errors.New("history aggregate range is invalid")
	}
	if _, offset := from.Zone(); offset != 0 {
		return errors.New("history aggregate from must use UTC")
	}
	if _, offset := to.Zone(); offset != 0 {
		return errors.New("history aggregate to must use UTC")
	}
	if !granularity.Valid() || !qualityPolicy.Valid() {
		return errors.New("history aggregate policy is invalid")
	}
	timezone = strings.TrimSpace(timezone)
	if timezone == "" || timezone == "Local" {
		return errors.New("history aggregate timezone is invalid")
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return errors.New("history aggregate timezone is invalid")
	}
	if exceedsAggregateBucketLimit(from.UTC(), to.UTC(), granularity, location) {
		return errors.New("history aggregate query exceeds bucket limit")
	}
	return nil
}

func exceedsAggregateBucketLimit(from, to time.Time, granularity AggregateGranularity, location *time.Location) bool {
	limit := granularity.MaxBucketsPerKey()
	cursor, ok := alignCalendarCursor(from.In(location), granularity, location)
	if !ok {
		return true
	}
	toLocal := to.In(location)
	count := 0
	for cursor.Before(toLocal) {
		next := stepCalendarCursor(cursor, granularity)
		if next.UTC().After(from) && cursor.UTC().Before(to) {
			count++
			if count > limit {
				return true
			}
		}
		cursor = next
	}
	return false
}

func alignCalendarCursor(startLocal time.Time, granularity AggregateGranularity, location *time.Location) (time.Time, bool) {
	switch granularity {
	case AggregateGranularityMinute:
		return time.Date(startLocal.Year(), startLocal.Month(), startLocal.Day(), startLocal.Hour(), startLocal.Minute(), 0, 0, location), true
	case AggregateGranularityFiveMinute:
		return time.Date(startLocal.Year(), startLocal.Month(), startLocal.Day(), startLocal.Hour(), startLocal.Minute()-startLocal.Minute()%5, 0, 0, location), true
	case AggregateGranularityQuarterHour:
		return time.Date(startLocal.Year(), startLocal.Month(), startLocal.Day(), startLocal.Hour(), startLocal.Minute()-startLocal.Minute()%15, 0, 0, location), true
	case AggregateGranularityHour:
		return time.Date(startLocal.Year(), startLocal.Month(), startLocal.Day(), startLocal.Hour(), 0, 0, 0, location), true
	case AggregateGranularityDay:
		return time.Date(startLocal.Year(), startLocal.Month(), startLocal.Day(), 0, 0, 0, 0, location), true
	case AggregateGranularityWeek:
		dayStart := time.Date(startLocal.Year(), startLocal.Month(), startLocal.Day(), 0, 0, 0, 0, location)
		daysSinceMonday := (int(dayStart.Weekday()) + 6) % 7
		return dayStart.AddDate(0, 0, -daysSinceMonday), true
	case AggregateGranularityMonth:
		return time.Date(startLocal.Year(), startLocal.Month(), 1, 0, 0, 0, 0, location), true
	default:
		return time.Time{}, false
	}
}

func stepCalendarCursor(value time.Time, granularity AggregateGranularity) time.Time {
	switch granularity {
	case AggregateGranularityMinute:
		return value.Add(time.Minute)
	case AggregateGranularityFiveMinute:
		return value.Add(5 * time.Minute)
	case AggregateGranularityQuarterHour:
		return value.Add(15 * time.Minute)
	case AggregateGranularityHour:
		return value.Add(time.Hour)
	case AggregateGranularityDay:
		return value.AddDate(0, 0, 1)
	case AggregateGranularityWeek:
		return value.AddDate(0, 0, 7)
	case AggregateGranularityMonth:
		return value.AddDate(0, 1, 0)
	default:
		return value
	}
}

func (query DeviceHistoryAggregateQuery) Canonical() (DeviceHistoryAggregateQuery, error) {
	if err := query.Validate(); err != nil {
		return DeviceHistoryAggregateQuery{}, err
	}
	canonical := query
	canonical.Keys = append([]string(nil), query.Keys...)
	slices.Sort(canonical.Keys)
	canonical.From = query.From.UTC()
	canonical.To = query.To.UTC()
	canonical.Timezone = strings.TrimSpace(query.Timezone)
	return canonical, nil
}

func (query DeviceHistoryAggregateQuery) ScopeDigest() (string, error) {
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

type AggregateQualitySummary struct {
	Good      int64 `json:"good"`
	Partial   int64 `json:"partial"`
	Estimated int64 `json:"estimated"`
	Manual    int64 `json:"manual"`
	Stale     int64 `json:"stale"`
	Invalid   int64 `json:"invalid"`
}

type GaugeAggregate struct {
	Average     float64 `json:"average"`
	Minimum     float64 `json:"minimum"`
	Maximum     float64 `json:"maximum"`
	First       float64 `json:"first"`
	Last        float64 `json:"last"`
	SampleCount int64   `json:"sampleCount"`
}

type CounterAggregate struct {
	DeltaSum                float64 `json:"deltaSum"`
	DeltaCount              int64   `json:"deltaCount"`
	ResetCount              int64   `json:"resetCount"`
	RolloverCount           int64   `json:"rolloverCount"`
	ExcludedTransitionCount int64   `json:"excludedTransitionCount"`
}

type StateAggregate struct {
	ValueType   ValueType       `json:"valueType"`
	LastValue   json.RawMessage `json:"lastValue"`
	SampleCount int64           `json:"sampleCount"`
	ChangeCount int64           `json:"changeCount"`
}

type DeviceHistoryAggregateBucket struct {
	TelemetryKey  string                  `json:"telemetryKey"`
	PointID       string                  `json:"pointId"`
	PointRevision uint64                  `json:"pointRevision"`
	PointType     PointType               `json:"pointType"`
	Unit          *string                 `json:"unit"`
	PeriodStart   time.Time               `json:"periodStart"`
	PeriodEnd     time.Time               `json:"periodEnd"`
	Quality       AggregateQualitySummary `json:"quality"`
	Completeness  float64                 `json:"completeness"`
	Gauge         *GaugeAggregate         `json:"gauge,omitempty"`
	Counter       *CounterAggregate       `json:"counter,omitempty"`
	State         *StateAggregate         `json:"state,omitempty"`
}

type DeviceHistoryAggregateMetadata struct {
	RequestedFrom       time.Time              `json:"requestedFrom"`
	RequestedTo         time.Time              `json:"requestedTo"`
	Granularity         AggregateGranularity   `json:"granularity"`
	Timezone            string                 `json:"timezone"`
	QualityPolicy       AggregateQualityPolicy `json:"qualityPolicy"`
	ProjectionWatermark *time.Time             `json:"projectionWatermark"`
	ReturnedBuckets     int                    `json:"returnedBuckets"`
}

type DeviceHistoryAggregateResponse struct {
	SchemaVersion int                            `json:"schemaVersion"`
	TenantID      string                         `json:"tenantId"`
	SiteID        string                         `json:"siteId"`
	DeviceID      string                         `json:"deviceId"`
	Buckets       []DeviceHistoryAggregateBucket `json:"buckets"`
	Metadata      DeviceHistoryAggregateMetadata `json:"metadata"`
}

func (response DeviceHistoryAggregateResponse) ValidateFor(query DeviceHistoryAggregateQuery) error {
	canonical, err := query.Canonical()
	if err != nil {
		return err
	}
	if response.SchemaVersion != 1 || response.TenantID != canonical.TenantID || response.SiteID != canonical.SiteID || response.DeviceID != canonical.DeviceID {
		return errors.New("history aggregate response scope is invalid")
	}
	metadata := response.Metadata
	if !metadata.RequestedFrom.Equal(canonical.From) || !metadata.RequestedTo.Equal(canonical.To) || metadata.Granularity != canonical.Granularity || metadata.Timezone != canonical.Timezone || metadata.QualityPolicy != canonical.QualityPolicy || metadata.ReturnedBuckets != len(response.Buckets) || len(response.Buckets) > MaximumAggregateBuckets {
		return errors.New("history aggregate response metadata is invalid")
	}
	if metadata.ProjectionWatermark != nil {
		if metadata.ProjectionWatermark.IsZero() {
			return errors.New("history aggregate projection watermark is invalid")
		}
		if _, offset := metadata.ProjectionWatermark.Zone(); offset != 0 {
			return errors.New("history aggregate projection watermark must use UTC")
		}
	}
	allowed := map[string]struct{}{}
	for _, key := range canonical.Keys {
		allowed[key] = struct{}{}
	}
	for _, bucket := range response.Buckets {
		if _, ok := allowed[bucket.TelemetryKey]; !ok || !validUUIDv7(bucket.PointID) || bucket.PointRevision < 1 || !bucket.PointType.Valid() || bucket.PeriodStart.IsZero() || !bucket.PeriodStart.Before(bucket.PeriodEnd) || bucket.Completeness < 0 || bucket.Completeness > 1 {
			return errors.New("history aggregate bucket is invalid")
		}
		kindCount := 0
		if bucket.Gauge != nil {
			kindCount++
		}
		if bucket.Counter != nil {
			kindCount++
		}
		if bucket.State != nil {
			kindCount++
		}
		if kindCount != 1 {
			return errors.New("history aggregate bucket must contain exactly one point-type result")
		}
		switch bucket.PointType {
		case PointTypeTelemetry, PointTypeSetting:
			if bucket.Gauge != nil {
				break
			}
			if bucket.State != nil && (bucket.State.ValueType == ValueTypeString || bucket.State.ValueType == ValueTypeBoolean || bucket.State.ValueType == ValueTypeJSON) && validateTypedValue(bucket.State.ValueType, bucket.State.LastValue) == nil {
				break
			}
			return errors.New("telemetry/setting aggregate requires gauge or discrete state result")
		case PointTypeCounter:
			if bucket.Counter == nil {
				return errors.New("counter aggregate requires counter result")
			}
		case PointTypeState:
			if bucket.State == nil || !bucket.State.ValueType.Valid() || validateTypedValue(bucket.State.ValueType, bucket.State.LastValue) != nil {
				return errors.New("state aggregate requires typed state result")
			}
		default:
			return errors.New("setting/command points are not aggregate history inputs")
		}
	}
	return nil
}
