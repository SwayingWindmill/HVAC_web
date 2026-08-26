package history

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/telemetryhistorymodel"
)

type aggregateRow struct {
	TelemetryKey   string   `json:"telemetry_key"`
	PointID        string   `json:"point_id"`
	PointRevision  uint64   `json:"point_revision"`
	PointType      string   `json:"point_type"`
	Unit           *string  `json:"unit"`
	PeriodStart    string   `json:"period_start"`
	PeriodEnd      string   `json:"period_end"`
	Good           int64    `json:"good_count"`
	Partial        int64    `json:"partial_count"`
	Estimated      int64    `json:"estimated_count"`
	Manual         int64    `json:"manual_count"`
	Stale          int64    `json:"stale_count"`
	Invalid        int64    `json:"invalid_count"`
	TotalCount     int64    `json:"total_count"`
	IncludedCount  int64    `json:"included_count"`
	Average        *float64 `json:"average"`
	Minimum        *float64 `json:"minimum"`
	Maximum        *float64 `json:"maximum"`
	First          *float64 `json:"first"`
	Last           *float64 `json:"last"`
	DeltaSum       *float64 `json:"delta_sum"`
	DeltaCount     int64    `json:"delta_count"`
	ResetCount     int64    `json:"reset_count"`
	RolloverCount  int64    `json:"rollover_count"`
	ExcludedCount  int64    `json:"excluded_transition_count"`
	StateValueType *string  `json:"state_value_type"`
	StateLastValue *string  `json:"state_last_value"`
	StateSamples   int64    `json:"state_sample_count"`
	StateChanges   int64    `json:"state_change_count"`
}

type aggregateSnapshotRow struct {
	SnapshotAt          string  `json:"snapshot_at"`
	ProjectionWatermark *string `json:"projection_watermark"`
}

type calendarPeriod struct {
	Start time.Time
	End   time.Time
}

func (client *Client) QueryDeviceHistoryAggregate(ctx context.Context, query telemetryhistorymodel.DeviceHistoryAggregateQuery) (telemetryhistorymodel.DeviceHistoryAggregateResponse, error) {
	if client == nil || client.endpoint == nil || client.httpClient == nil {
		return telemetryhistorymodel.DeviceHistoryAggregateResponse{}, errors.New("ClickHouse history client is closed")
	}
	canonical, err := query.Canonical()
	if err != nil {
		return telemetryhistorymodel.DeviceHistoryAggregateResponse{}, err
	}
	periods, err := buildCalendarPeriods(canonical)
	if err != nil {
		return telemetryhistorymodel.DeviceHistoryAggregateResponse{}, err
	}
	if len(periods) > telemetryhistorymodel.MaximumAggregateBuckets {
		return telemetryhistorymodel.DeviceHistoryAggregateResponse{}, errors.New("history aggregate query exceeds bucket limit")
	}
	snapshotAt, watermark, err := client.aggregateSnapshot(ctx, canonical)
	if err != nil {
		return telemetryhistorymodel.DeviceHistoryAggregateResponse{}, err
	}
	queries := []string{
		client.gaugeAggregateQuery(canonical, snapshotAt),
		client.counterAggregateQuery(canonical, snapshotAt),
		client.stateAggregateQuery(canonical, snapshotAt),
	}
	rows := make([]aggregateRow, 0)
	for _, sql := range queries {
		payload, err := client.execute(ctx, sql)
		if err != nil {
			return telemetryhistorymodel.DeviceHistoryAggregateResponse{}, err
		}
		decoded, err := decodeAggregateRows(payload)
		if err != nil {
			return telemetryhistorymodel.DeviceHistoryAggregateResponse{}, err
		}
		rows = append(rows, decoded...)
	}
	buckets := make([]telemetryhistorymodel.DeviceHistoryAggregateBucket, 0, len(rows))
	for _, row := range rows {
		bucket, err := decodeAggregateRow(row)
		if err != nil {
			return telemetryhistorymodel.DeviceHistoryAggregateResponse{}, err
		}
		buckets = append(buckets, bucket)
	}
	sort.Slice(buckets, func(i, j int) bool {
		left, right := buckets[i], buckets[j]
		if left.TelemetryKey != right.TelemetryKey {
			return left.TelemetryKey < right.TelemetryKey
		}
		if left.PointID != right.PointID {
			return left.PointID < right.PointID
		}
		if left.PointRevision != right.PointRevision {
			return left.PointRevision < right.PointRevision
		}
		return left.PeriodStart.Before(right.PeriodStart)
	})
	if len(buckets) > telemetryhistorymodel.MaximumAggregateBuckets {
		return telemetryhistorymodel.DeviceHistoryAggregateResponse{}, errors.New("history aggregate result exceeds bucket limit")
	}
	response := telemetryhistorymodel.DeviceHistoryAggregateResponse{
		SchemaVersion: 1,
		TenantID:      canonical.TenantID,
		SiteID:        canonical.SiteID,
		DeviceID:      canonical.DeviceID,
		Buckets:       buckets,
		Metadata: telemetryhistorymodel.DeviceHistoryAggregateMetadata{
			RequestedFrom:       canonical.From,
			RequestedTo:         canonical.To,
			Granularity:         canonical.Granularity,
			Timezone:            canonical.Timezone,
			QualityPolicy:       canonical.QualityPolicy,
			ProjectionWatermark: watermark,
			ReturnedBuckets:     len(buckets),
		},
	}
	if err := response.ValidateFor(canonical); err != nil {
		return telemetryhistorymodel.DeviceHistoryAggregateResponse{}, fmt.Errorf("validate ClickHouse history aggregate response: %w", err)
	}
	return response, nil
}

func (client *Client) aggregateSnapshot(ctx context.Context, query telemetryhistorymodel.DeviceHistoryAggregateQuery) (time.Time, *time.Time, error) {
	payload, err := client.execute(ctx, client.aggregateSnapshotQuery(query))
	if err != nil {
		return time.Time{}, nil, err
	}
	var row aggregateSnapshotRow
	decoder := json.NewDecoder(bytes.NewReader(payload))
	if err := decoder.Decode(&row); err != nil {
		return time.Time{}, nil, fmt.Errorf("decode ClickHouse aggregate snapshot: %w", err)
	}
	snapshotAt, err := parseClickHouseTime(row.SnapshotAt)
	if err != nil {
		return time.Time{}, nil, fmt.Errorf("decode ClickHouse aggregate snapshot time: %w", err)
	}
	var watermark *time.Time
	if row.ProjectionWatermark != nil && strings.TrimSpace(*row.ProjectionWatermark) != "" {
		parsed, err := parseClickHouseTime(*row.ProjectionWatermark)
		if err != nil {
			return time.Time{}, nil, fmt.Errorf("decode ClickHouse aggregate projection watermark: %w", err)
		}
		watermark = &parsed
	}
	return snapshotAt, watermark, nil
}

func decodeAggregateRows(payload []byte) ([]aggregateRow, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	rows := make([]aggregateRow, 0)
	for {
		var row aggregateRow
		if err := decoder.Decode(&row); errors.Is(err, io.EOF) {
			return rows, nil
		} else if err != nil {
			return nil, fmt.Errorf("decode ClickHouse history aggregate row: %w", err)
		}
		rows = append(rows, row)
	}
}

func decodeAggregateRow(row aggregateRow) (telemetryhistorymodel.DeviceHistoryAggregateBucket, error) {
	periodStart, err := parseClickHouseTime(row.PeriodStart)
	if err != nil {
		return telemetryhistorymodel.DeviceHistoryAggregateBucket{}, fmt.Errorf("decode aggregate period start: %w", err)
	}
	periodEnd, err := parseClickHouseTime(row.PeriodEnd)
	if err != nil {
		return telemetryhistorymodel.DeviceHistoryAggregateBucket{}, fmt.Errorf("decode aggregate period end: %w", err)
	}
	if row.TotalCount < 1 || row.IncludedCount < 1 || row.IncludedCount > row.TotalCount {
		return telemetryhistorymodel.DeviceHistoryAggregateBucket{}, errors.New("ClickHouse aggregate counts are invalid")
	}
	bucket := telemetryhistorymodel.DeviceHistoryAggregateBucket{
		TelemetryKey:  row.TelemetryKey,
		PointID:       row.PointID,
		PointRevision: row.PointRevision,
		PointType:     telemetryhistorymodel.PointType(row.PointType),
		Unit:          row.Unit,
		PeriodStart:   periodStart,
		PeriodEnd:     periodEnd,
		Quality: telemetryhistorymodel.AggregateQualitySummary{
			Good: row.Good, Partial: row.Partial, Estimated: row.Estimated,
			Manual: row.Manual, Stale: row.Stale, Invalid: row.Invalid,
		},
		Completeness: float64(row.IncludedCount) / float64(row.TotalCount),
	}
	switch bucket.PointType {
	case telemetryhistorymodel.PointTypeTelemetry:
		if row.Average == nil || row.Minimum == nil || row.Maximum == nil || row.First == nil || row.Last == nil || !finite(*row.Average, *row.Minimum, *row.Maximum, *row.First, *row.Last) {
			return telemetryhistorymodel.DeviceHistoryAggregateBucket{}, errors.New("ClickHouse gauge aggregate is invalid")
		}
		bucket.Gauge = &telemetryhistorymodel.GaugeAggregate{
			Average: *row.Average, Minimum: *row.Minimum, Maximum: *row.Maximum,
			First: *row.First, Last: *row.Last, SampleCount: row.IncludedCount,
		}
	case telemetryhistorymodel.PointTypeCounter:
		if row.DeltaSum == nil || !finite(*row.DeltaSum) {
			return telemetryhistorymodel.DeviceHistoryAggregateBucket{}, errors.New("ClickHouse counter aggregate is invalid")
		}
		bucket.Counter = &telemetryhistorymodel.CounterAggregate{
			DeltaSum: *row.DeltaSum, DeltaCount: row.DeltaCount, ResetCount: row.ResetCount,
			RolloverCount: row.RolloverCount, ExcludedTransitionCount: row.ExcludedCount,
		}
	case telemetryhistorymodel.PointTypeState:
		if row.StateValueType == nil || row.StateLastValue == nil {
			return telemetryhistorymodel.DeviceHistoryAggregateBucket{}, errors.New("ClickHouse state aggregate is invalid")
		}
		bucket.State = &telemetryhistorymodel.StateAggregate{
			ValueType: telemetryhistorymodel.ValueType(*row.StateValueType),
			LastValue: json.RawMessage(*row.StateLastValue), SampleCount: row.StateSamples, ChangeCount: row.StateChanges,
		}
	default:
		return telemetryhistorymodel.DeviceHistoryAggregateBucket{}, errors.New("ClickHouse returned a non-aggregatable Point type")
	}
	return bucket, nil
}

func finite(values ...float64) bool {
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return false
		}
	}
	return true
}

func buildCalendarPeriods(query telemetryhistorymodel.DeviceHistoryAggregateQuery) ([]calendarPeriod, error) {
	location, err := time.LoadLocation(query.Timezone)
	if err != nil {
		return nil, errors.New("history aggregate timezone is invalid")
	}
	startLocal := query.From.In(location)
	var cursor time.Time
	switch query.Granularity {
	case telemetryhistorymodel.AggregateGranularityHour:
		cursor = time.Date(startLocal.Year(), startLocal.Month(), startLocal.Day(), startLocal.Hour(), 0, 0, 0, location)
	case telemetryhistorymodel.AggregateGranularityDay:
		cursor = time.Date(startLocal.Year(), startLocal.Month(), startLocal.Day(), 0, 0, 0, 0, location)
	case telemetryhistorymodel.AggregateGranularityWeek:
		dayStart := time.Date(startLocal.Year(), startLocal.Month(), startLocal.Day(), 0, 0, 0, 0, location)
		daysSinceMonday := (int(dayStart.Weekday()) + 6) % 7
		cursor = dayStart.AddDate(0, 0, -daysSinceMonday)
	case telemetryhistorymodel.AggregateGranularityMonth:
		cursor = time.Date(startLocal.Year(), startLocal.Month(), 1, 0, 0, 0, 0, location)
	default:
		return nil, errors.New("history aggregate granularity is invalid")
	}
	periods := make([]calendarPeriod, 0)
	for cursor.Before(query.To.In(location)) {
		next := nextCalendarBoundary(cursor, query.Granularity)
		start, end := cursor.UTC(), next.UTC()
		if end.After(query.From) && start.Before(query.To) {
			if start.Before(query.From) {
				start = query.From
			}
			if end.After(query.To) {
				end = query.To
			}
			periods = append(periods, calendarPeriod{Start: start, End: end})
			if len(periods) > telemetryhistorymodel.MaximumAggregateBuckets {
				return periods, nil
			}
		}
		cursor = next
	}
	return periods, nil
}

func nextCalendarBoundary(value time.Time, granularity telemetryhistorymodel.AggregateGranularity) time.Time {
	switch granularity {
	case telemetryhistorymodel.AggregateGranularityHour:
		return value.Add(time.Hour)
	case telemetryhistorymodel.AggregateGranularityDay:
		return value.AddDate(0, 0, 1)
	case telemetryhistorymodel.AggregateGranularityWeek:
		return value.AddDate(0, 0, 7)
	case telemetryhistorymodel.AggregateGranularityMonth:
		return value.AddDate(0, 1, 0)
	default:
		return value
	}
}

func (client *Client) aggregateSnapshotQuery(query telemetryhistorymodel.DeviceHistoryAggregateQuery) string {
	return fmt.Sprintf(`WITH now64(3) AS snapshot_at
SELECT
  formatDateTime(snapshot_at, '%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ', 'UTC') AS snapshot_at,
  if(count() = 0, CAST(NULL, 'Nullable(String)'), formatDateTime(max(projected_at), '%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ', 'UTC')) AS projection_watermark
FROM %s.%s
WHERE tenant_id = toUUID('%s')
  AND site_id = toUUID('%s')
  AND device_id = toUUID('%s')
  AND telemetry_key IN (%s)
  AND sampled_at >= parseDateTime64BestEffort('%s', 3, 'UTC')
  AND sampled_at < parseDateTime64BestEffort('%s', 3, 'UTC')
  AND projected_at < snapshot_at
  AND acceptance_status IN ('ACCEPTED', 'OUT_OF_ORDER')
  AND point_id IS NOT NULL AND point_revision IS NOT NULL
  AND point_type IN ('TELEMETRY', 'COUNTER', 'STATE')
FORMAT JSONEachRow`, client.database, client.table, query.TenantID, query.SiteID, query.DeviceID, quotedKeys(query.Keys), formatClickHouseTime(query.From), formatClickHouseTime(query.To))
}

func (client *Client) gaugeAggregateQuery(query telemetryhistorymodel.DeviceHistoryAggregateQuery, snapshotAt time.Time) string {
	bucket, end := aggregateBucketExpressions(query)
	included := aggregateQualityPredicate(query.QualityPolicy, "quality")
	return fmt.Sprintf(`WITH %s AS bucket
SELECT
  telemetry_key,
  toString(assumeNotNull(point_id)) AS point_id,
  assumeNotNull(point_revision) AS point_revision,
  'TELEMETRY' AS point_type,
  any(unit) AS unit,
  formatDateTime(bucket, '%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ', 'UTC') AS period_start,
  formatDateTime(%s, '%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ', 'UTC') AS period_end,
  countIf(quality = 'GOOD') AS good_count,
  countIf(quality = 'PARTIAL') AS partial_count,
  countIf(quality = 'ESTIMATED') AS estimated_count,
  countIf(quality = 'MANUAL') AS manual_count,
  countIf(quality = 'STALE') AS stale_count,
  countIf(quality = 'INVALID') AS invalid_count,
  count() AS total_count,
  countIf(%s) AS included_count,
  avgIf(assumeNotNull(value_number), %s) AS average,
  minIf(assumeNotNull(value_number), %s) AS minimum,
  maxIf(assumeNotNull(value_number), %s) AS maximum,
  argMinIf(assumeNotNull(value_number), tuple(sampled_at, toString(observation_id)), %s) AS first,
  argMaxIf(assumeNotNull(value_number), tuple(sampled_at, toString(observation_id)), %s) AS last,
  CAST(NULL, 'Nullable(Float64)') AS delta_sum,
  toInt64(0) AS delta_count,
  toInt64(0) AS reset_count,
  toInt64(0) AS rollover_count,
  toInt64(0) AS excluded_transition_count,
  CAST(NULL, 'Nullable(String)') AS state_value_type,
  CAST(NULL, 'Nullable(String)') AS state_last_value,
  toInt64(0) AS state_sample_count,
  toInt64(0) AS state_change_count
FROM %s.%s
WHERE tenant_id = toUUID('%s') AND site_id = toUUID('%s') AND device_id = toUUID('%s')
  AND telemetry_key IN (%s)
  AND sampled_at >= parseDateTime64BestEffort('%s', 3, 'UTC') AND sampled_at < parseDateTime64BestEffort('%s', 3, 'UTC')
  AND projected_at < parseDateTime64BestEffort('%s', 3, 'UTC')
  AND acceptance_status IN ('ACCEPTED', 'OUT_OF_ORDER') AND point_type = 'TELEMETRY'
  AND point_id IS NOT NULL AND point_revision IS NOT NULL AND value_type = 'NUMBER' AND value_number IS NOT NULL AND isFinite(value_number)
GROUP BY telemetry_key, point_id, point_revision, bucket
HAVING included_count > 0
FORMAT JSONEachRow`, bucket, end, included, included, included, included, included, included,
		client.database, client.table, query.TenantID, query.SiteID, query.DeviceID, quotedKeys(query.Keys), formatClickHouseTime(query.From), formatClickHouseTime(query.To), formatClickHouseTime(snapshotAt))
}

func (client *Client) counterAggregateQuery(query telemetryhistorymodel.DeviceHistoryAggregateQuery, snapshotAt time.Time) string {
	bucket, end := aggregateBucketExpressions(query)
	currentIncluded := aggregateQualityPredicate(query.QualityPolicy, "quality")
	previousIncluded := aggregateQualityPredicate(query.QualityPolicy, "previous_quality")
	transitionIncluded := "(" + currentIncluded + " AND " + previousIncluded + ")"
	return fmt.Sprintf(`WITH ordered AS (
  SELECT *,
    lagInFrame(value_number) OVER point_window AS previous_value,
    lagInFrame(point_revision) OVER point_window AS previous_point_revision,
    lagInFrame(unit) OVER point_window AS previous_unit,
    lagInFrame(quality) OVER point_window AS previous_quality,
    max(value_number) OVER revision_window AS previous_max_value
  FROM %s.%s
  WHERE tenant_id = toUUID('%s') AND site_id = toUUID('%s') AND device_id = toUUID('%s')
    AND telemetry_key IN (%s)
    AND sampled_at < parseDateTime64BestEffort('%s', 3, 'UTC')
    AND projected_at < parseDateTime64BestEffort('%s', 3, 'UTC')
    AND acceptance_status IN ('ACCEPTED', 'OUT_OF_ORDER') AND point_type = 'COUNTER'
    AND point_id IS NOT NULL AND point_revision IS NOT NULL AND value_type = 'NUMBER' AND value_number IS NOT NULL AND isFinite(value_number)
  WINDOW point_window AS (PARTITION BY tenant_id, site_id, point_id ORDER BY sampled_at, observation_id ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW),
         revision_window AS (PARTITION BY tenant_id, site_id, point_id, point_revision, unit ORDER BY sampled_at, observation_id ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING)
), deltas AS (
  SELECT *,
    multiIf(
      previous_value IS NULL, 'INITIAL',
      previous_point_revision IS NULL OR point_revision != previous_point_revision, 'REVISION_BOUNDARY',
      ifNull(unit, '') != ifNull(previous_unit, ''), 'UNIT_BOUNDARY',
      counter_decrease_mode = 'INVALID' AND previous_max_value IS NOT NULL AND value_number < previous_max_value, 'INVALID_DECREASE',
      counter_decrease_mode = 'INVALID' AND previous_max_value IS NOT NULL AND previous_value < previous_max_value AND value_number >= previous_max_value, 'RECOVERY',
      value_number = previous_value, 'UNCHANGED',
      value_number > previous_value, 'INCREASE',
      counter_decrease_mode = 'RESET_TO_ZERO', 'RESET',
      counter_decrease_mode = 'ROLLOVER' AND counter_rollover_modulus IS NOT NULL AND previous_value >= 0 AND value_number >= 0 AND previous_value < counter_rollover_modulus AND value_number < counter_rollover_modulus, 'ROLLOVER',
      'INVALID_DECREASE') AS transition_type,
    multiIf(
      previous_value IS NULL, CAST(NULL, 'Nullable(Float64)'),
      previous_point_revision IS NULL OR point_revision != previous_point_revision, CAST(NULL, 'Nullable(Float64)'),
      ifNull(unit, '') != ifNull(previous_unit, ''), CAST(NULL, 'Nullable(Float64)'),
      counter_decrease_mode = 'INVALID' AND previous_max_value IS NOT NULL AND value_number < previous_max_value, CAST(NULL, 'Nullable(Float64)'),
      counter_decrease_mode = 'INVALID' AND previous_max_value IS NOT NULL AND previous_value < previous_max_value AND value_number >= previous_max_value, toNullable(value_number - previous_max_value),
      value_number >= previous_value, toNullable(value_number - previous_value),
      counter_decrease_mode = 'RESET_TO_ZERO', toNullable(value_number),
      counter_decrease_mode = 'ROLLOVER' AND counter_rollover_modulus IS NOT NULL AND previous_value >= 0 AND value_number >= 0 AND previous_value < counter_rollover_modulus AND value_number < counter_rollover_modulus, toNullable(counter_rollover_modulus - previous_value + value_number),
      CAST(NULL, 'Nullable(Float64)')) AS delta_value
  FROM ordered
), scoped AS (
  SELECT *, %s AS bucket
  FROM deltas
  WHERE sampled_at >= parseDateTime64BestEffort('%s', 3, 'UTC')
)
SELECT
  telemetry_key,
  toString(assumeNotNull(point_id)) AS point_id,
  assumeNotNull(point_revision) AS point_revision,
  'COUNTER' AS point_type,
  any(unit) AS unit,
  formatDateTime(bucket, '%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ', 'UTC') AS period_start,
  formatDateTime(%s, '%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ', 'UTC') AS period_end,
  countIf(quality = 'GOOD') AS good_count,
  countIf(quality = 'PARTIAL') AS partial_count,
  countIf(quality = 'ESTIMATED') AS estimated_count,
  countIf(quality = 'MANUAL') AS manual_count,
  countIf(quality = 'STALE') AS stale_count,
  countIf(quality = 'INVALID') AS invalid_count,
  count() AS total_count,
  countIf(%s) AS included_count,
  CAST(NULL, 'Nullable(Float64)') AS average,
  CAST(NULL, 'Nullable(Float64)') AS minimum,
  CAST(NULL, 'Nullable(Float64)') AS maximum,
  CAST(NULL, 'Nullable(Float64)') AS first,
  CAST(NULL, 'Nullable(Float64)') AS last,
  toNullable(sumIf(ifNull(delta_value, 0.0), %s)) AS delta_sum,
  countIf(delta_value IS NOT NULL AND %s) AS delta_count,
  countIf(transition_type = 'RESET' AND %s) AS reset_count,
  countIf(transition_type = 'ROLLOVER' AND %s) AS rollover_count,
  countIf(transition_type IN ('INVALID_DECREASE', 'REVISION_BOUNDARY', 'UNIT_BOUNDARY') OR (delta_value IS NOT NULL AND NOT %s)) AS excluded_transition_count,
  CAST(NULL, 'Nullable(String)') AS state_value_type,
  CAST(NULL, 'Nullable(String)') AS state_last_value,
  toInt64(0) AS state_sample_count,
  toInt64(0) AS state_change_count
FROM scoped
GROUP BY telemetry_key, point_id, point_revision, bucket
HAVING included_count > 0
FORMAT JSONEachRow`, client.database, client.table, query.TenantID, query.SiteID, query.DeviceID, quotedKeys(query.Keys), formatClickHouseTime(query.To), formatClickHouseTime(snapshotAt), bucket, formatClickHouseTime(query.From), end,
		currentIncluded, transitionIncluded, transitionIncluded, transitionIncluded, transitionIncluded, transitionIncluded)
}

func (client *Client) stateAggregateQuery(query telemetryhistorymodel.DeviceHistoryAggregateQuery, snapshotAt time.Time) string {
	bucket, end := aggregateBucketExpressions(query)
	included := aggregateQualityPredicate(query.QualityPolicy, "quality")
	return fmt.Sprintf(`WITH ordered AS (
  SELECT *, lagInFrame(value_json) OVER point_window AS previous_value_json
  FROM %s.%s
  WHERE tenant_id = toUUID('%s') AND site_id = toUUID('%s') AND device_id = toUUID('%s')
    AND telemetry_key IN (%s)
    AND sampled_at < parseDateTime64BestEffort('%s', 3, 'UTC')
    AND projected_at < parseDateTime64BestEffort('%s', 3, 'UTC')
    AND acceptance_status IN ('ACCEPTED', 'OUT_OF_ORDER') AND point_type = 'STATE'
    AND point_id IS NOT NULL AND point_revision IS NOT NULL AND value_type IN ('STRING', 'BOOLEAN', 'JSON', 'NUMBER') AND value_json IS NOT NULL
  WINDOW point_window AS (PARTITION BY tenant_id, site_id, point_id, point_revision ORDER BY sampled_at, observation_id ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW)
), scoped AS (
  SELECT *, %s AS bucket
  FROM ordered
  WHERE sampled_at >= parseDateTime64BestEffort('%s', 3, 'UTC')
)
SELECT
  telemetry_key,
  toString(assumeNotNull(point_id)) AS point_id,
  assumeNotNull(point_revision) AS point_revision,
  'STATE' AS point_type,
  any(unit) AS unit,
  formatDateTime(bucket, '%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ', 'UTC') AS period_start,
  formatDateTime(%s, '%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ', 'UTC') AS period_end,
  countIf(quality = 'GOOD') AS good_count,
  countIf(quality = 'PARTIAL') AS partial_count,
  countIf(quality = 'ESTIMATED') AS estimated_count,
  countIf(quality = 'MANUAL') AS manual_count,
  countIf(quality = 'STALE') AS stale_count,
  countIf(quality = 'INVALID') AS invalid_count,
  count() AS total_count,
  countIf(%s) AS included_count,
  CAST(NULL, 'Nullable(Float64)') AS average,
  CAST(NULL, 'Nullable(Float64)') AS minimum,
  CAST(NULL, 'Nullable(Float64)') AS maximum,
  CAST(NULL, 'Nullable(Float64)') AS first,
  CAST(NULL, 'Nullable(Float64)') AS last,
  CAST(NULL, 'Nullable(Float64)') AS delta_sum,
  toInt64(0) AS delta_count,
  toInt64(0) AS reset_count,
  toInt64(0) AS rollover_count,
  toInt64(0) AS excluded_transition_count,
  toNullable(argMaxIf(assumeNotNull(value_type), tuple(sampled_at, toString(observation_id)), %s)) AS state_value_type,
  toNullable(argMaxIf(assumeNotNull(value_json), tuple(sampled_at, toString(observation_id)), %s)) AS state_last_value,
  countIf(%s) AS state_sample_count,
  countIf(%s AND previous_value_json IS NOT NULL AND value_json != previous_value_json) AS state_change_count
FROM scoped
GROUP BY telemetry_key, point_id, point_revision, bucket
HAVING included_count > 0
FORMAT JSONEachRow`, client.database, client.table, query.TenantID, query.SiteID, query.DeviceID, quotedKeys(query.Keys), formatClickHouseTime(query.To), formatClickHouseTime(snapshotAt), bucket, formatClickHouseTime(query.From), end, included, included, included, included, included)
}

func aggregateBucketExpressions(query telemetryhistorymodel.DeviceHistoryAggregateQuery) (string, string) {
	switch query.Granularity {
	case telemetryhistorymodel.AggregateGranularityHour:
		return fmt.Sprintf("toStartOfHour(sampled_at, '%s')", query.Timezone), "addHours(bucket, 1)"
	case telemetryhistorymodel.AggregateGranularityDay:
		return fmt.Sprintf("toStartOfDay(sampled_at, '%s')", query.Timezone), "addDays(bucket, 1)"
	case telemetryhistorymodel.AggregateGranularityWeek:
		return fmt.Sprintf("toStartOfWeek(sampled_at, 1, '%s')", query.Timezone), "addWeeks(bucket, 1)"
	case telemetryhistorymodel.AggregateGranularityMonth:
		return fmt.Sprintf("toStartOfMonth(sampled_at, '%s')", query.Timezone), "addMonths(bucket, 1)"
	default:
		return "sampled_at", "sampled_at"
	}
}

func aggregateQualityPredicate(policy telemetryhistorymodel.AggregateQualityPolicy, column string) string {
	if policy == telemetryhistorymodel.AggregateQualityValidOnly {
		return column + " = 'GOOD'"
	}
	return column + " IN ('GOOD', 'PARTIAL', 'ESTIMATED', 'MANUAL')"
}
