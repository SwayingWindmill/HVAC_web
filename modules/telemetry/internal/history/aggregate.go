package history

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
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
	StateValues    []string `json:"state_values"`
	MaxProjectedAt *string  `json:"max_projected_at,omitempty"`
}

type aggregateSnapshotRow struct {
	SnapshotAt            string  `json:"snapshot_at"`
	ProjectionWatermark   *string `json:"projection_watermark"`
	HasConcurrentWrites   uint8   `json:"has_concurrent_writes"`
	RawGaugeIncludedCount *int64  `json:"raw_gauge_included_count"`
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
	if len(periods) > canonical.Granularity.MaxBucketsPerKey() {
		return telemetryhistorymodel.DeviceHistoryAggregateResponse{}, errors.New("history aggregate query exceeds bucket limit")
	}
	snapshotAt, watermark, hasConcurrentWrites, rawGaugeIncludedCount, err := client.aggregateSnapshot(ctx, canonical)
	if err != nil {
		return telemetryhistorymodel.DeviceHistoryAggregateResponse{}, err
	}
	gaugeSQL, usedRollup := client.gaugeAggregateQueryForSnapshot(canonical, snapshotAt, periods, hasConcurrentWrites)
	var rows []aggregateRow
	needRawFallback := false
	gaugePayload, err := client.execute(ctx, gaugeSQL)
	if err != nil {
		if !usedRollup || !isRollupTableUnavailable(err) {
			return telemetryhistorymodel.DeviceHistoryAggregateResponse{}, err
		}
		needRawFallback = true
	} else {
		rows, err = decodeAggregateRows(gaugePayload)
		if err != nil {
			return telemetryhistorymodel.DeviceHistoryAggregateResponse{}, err
		}
		if usedRollup && (rollupHasConcurrentWrites(rows, snapshotAt) ||
			(rawGaugeIncludedCount != nil && sumAggregateIncludedCount(rows) != *rawGaugeIncludedCount)) {
			needRawFallback = true
		}
	}
	if needRawFallback {
		rawPayload, rawErr := client.execute(ctx, client.rawGaugeAggregateQuery(canonical, snapshotAt))
		if rawErr != nil {
			return telemetryhistorymodel.DeviceHistoryAggregateResponse{}, rawErr
		}
		rows, err = decodeAggregateRows(rawPayload)
		if err != nil {
			return telemetryhistorymodel.DeviceHistoryAggregateResponse{}, err
		}
	}
	for _, sql := range []string{
		client.counterAggregateQuery(canonical, snapshotAt),
		client.stateAggregateQuery(canonical, snapshotAt),
	} {
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

func (client *Client) aggregateSnapshot(ctx context.Context, query telemetryhistorymodel.DeviceHistoryAggregateQuery) (time.Time, *time.Time, bool, *int64, error) {
	payload, err := client.execute(ctx, client.aggregateSnapshotQuery(query))
	if err != nil {
		return time.Time{}, nil, false, nil, err
	}
	var row aggregateSnapshotRow
	decoder := json.NewDecoder(bytes.NewReader(payload))
	if err := decoder.Decode(&row); err != nil {
		return time.Time{}, nil, false, nil, fmt.Errorf("decode ClickHouse aggregate snapshot: %w", err)
	}
	snapshotAt, err := parseClickHouseTime(row.SnapshotAt)
	if err != nil {
		return time.Time{}, nil, false, nil, fmt.Errorf("decode ClickHouse aggregate snapshot time: %w", err)
	}
	var watermark *time.Time
	if row.ProjectionWatermark != nil && strings.TrimSpace(*row.ProjectionWatermark) != "" {
		parsed, err := parseClickHouseTime(*row.ProjectionWatermark)
		if err != nil {
			return time.Time{}, nil, false, nil, fmt.Errorf("decode ClickHouse aggregate projection watermark: %w", err)
		}
		watermark = &parsed
	}
	return snapshotAt, watermark, row.HasConcurrentWrites != 0, row.RawGaugeIncludedCount, nil
}

func isRollupTableUnavailable(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	if strings.Contains(msg, "UNKNOWN_TABLE") || strings.Contains(msg, "Code: 60") {
		return true
	}
	var chErr *clickHouseQueryError
	if errors.As(err, &chErr) {
		return chErr.statusCode == http.StatusNotFound ||
			strings.Contains(chErr.body, "UNKNOWN_TABLE") ||
			strings.Contains(chErr.body, "Code: 60")
	}
	return false
}

func sumAggregateIncludedCount(rows []aggregateRow) int64 {
	var total int64
	for _, row := range rows {
		total += row.IncludedCount
	}
	return total
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
	case telemetryhistorymodel.PointTypeTelemetry, telemetryhistorymodel.PointTypeSetting:
		if row.StateValueType != nil && row.StateLastValue != nil {
			state, err := buildStateAggregate(row)
			if err != nil {
				return telemetryhistorymodel.DeviceHistoryAggregateBucket{}, err
			}
			bucket.State = state
			break
		}
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
		state, err := buildStateAggregate(row)
		if err != nil {
			return telemetryhistorymodel.DeviceHistoryAggregateBucket{}, err
		}
		bucket.State = state
	default:
		return telemetryhistorymodel.DeviceHistoryAggregateBucket{}, errors.New("ClickHouse returned a non-aggregatable Point type")
	}
	return bucket, nil
}

func buildStateAggregate(row aggregateRow) (*telemetryhistorymodel.StateAggregate, error) {
	if row.StateValueType == nil || row.StateLastValue == nil {
		return nil, errors.New("ClickHouse state aggregate is invalid")
	}
	countsByRaw := make(map[string]int64)
	for _, raw := range row.StateValues {
		if !json.Valid([]byte(raw)) {
			return nil, errors.New("ClickHouse state aggregate value_json is invalid")
		}
		countsByRaw[raw]++
	}
	if len(countsByRaw) == 0 && row.StateSamples > 0 {
		countsByRaw[*row.StateLastValue] = row.StateSamples
	}
	counts := make([]telemetryhistorymodel.StateCount, 0, len(countsByRaw))
	for raw, count := range countsByRaw {
		counts = append(counts, telemetryhistorymodel.StateCount{
			Value: json.RawMessage(raw),
			Count: count,
		})
	}
	sort.Slice(counts, func(i, j int) bool {
		if counts[i].Count != counts[j].Count {
			return counts[i].Count > counts[j].Count
		}
		return string(counts[i].Value) < string(counts[j].Value)
	})
	return &telemetryhistorymodel.StateAggregate{
		ValueType:   telemetryhistorymodel.ValueType(*row.StateValueType),
		LastValue:   json.RawMessage(*row.StateLastValue),
		SampleCount: row.StateSamples,
		ChangeCount: row.StateChanges,
		StateCounts: counts,
	}, nil
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
	cursor, ok := telemetryhistorymodel.AlignCalendarCursor(query.From.In(location), query.Granularity, location)
	if !ok {
		return nil, errors.New("history aggregate granularity is invalid")
	}
	periods := make([]calendarPeriod, 0)
	maxPeriods := query.Granularity.MaxBucketsPerKey()
	for cursor.Before(query.To.In(location)) {
		next := telemetryhistorymodel.StepCalendarCursor(cursor, query.Granularity)
		start, end := cursor.UTC(), next.UTC()
		if end.After(query.From) && start.Before(query.To) {
			if start.Before(query.From) {
				start = query.From
			}
			if end.After(query.To) {
				end = query.To
			}
			periods = append(periods, calendarPeriod{Start: start, End: end})
			if len(periods) > maxPeriods {
				return periods, nil
			}
		}
		cursor = next
	}
	return periods, nil
}

func (client *Client) aggregateSnapshotQuery(query telemetryhistorymodel.DeviceHistoryAggregateQuery) string {
	included := aggregateQualityPredicate(query.QualityPolicy, "quality")
	return fmt.Sprintf(`WITH now64(3) AS snapshot_cutoff
SELECT
  formatDateTime(snapshot_cutoff, '%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ', 'UTC') AS snapshot_at,
  if(countIf(projected_at < snapshot_cutoff) = 0, CAST(NULL, 'Nullable(String)'), formatDateTime(maxIf(projected_at, projected_at < snapshot_cutoff), '%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ', 'UTC')) AS projection_watermark,
  if(countIf(projected_at >= snapshot_cutoff) > 0, toUInt8(1), toUInt8(0)) AS has_concurrent_writes,
  toInt64(countIf(projected_at < snapshot_cutoff AND ifNull(point_type, '') IN ('TELEMETRY', 'SETTING') AND ifNull(value_type, '') = 'NUMBER' AND value_number IS NOT NULL AND ifNull(isFinite(value_number), 0) = 1 AND %s)) AS raw_gauge_included_count
FROM %s.%s
WHERE tenant_id = toUUID('%s')
  AND site_id = toUUID('%s')
  AND device_id = toUUID('%s')
  AND telemetry_key IN (%s)
  AND sampled_at >= parseDateTime64BestEffort('%s', 3, 'UTC')
  AND sampled_at < parseDateTime64BestEffort('%s', 3, 'UTC')
  AND acceptance_status IN ('ACCEPTED', 'OUT_OF_ORDER')
  AND point_id IS NOT NULL AND point_revision IS NOT NULL
  AND point_type IN ('TELEMETRY', 'COUNTER', 'STATE', 'SETTING')
FORMAT JSONEachRow`, included, client.database, client.table, query.TenantID, query.SiteID, query.DeviceID, quotedKeys(query.Keys), formatClickHouseTime(query.From), formatClickHouseTime(query.To))
}

func (client *Client) gaugeAggregateQueryForSnapshot(query telemetryhistorymodel.DeviceHistoryAggregateQuery, snapshotAt time.Time, periods []calendarPeriod, hasConcurrentWrites bool) (string, bool) {
	if rollupTable, ok := client.gaugeRollupTable(query, periods, hasConcurrentWrites); ok {
		return client.rollupGaugeAggregateQuery(query, rollupTable), true
	}
	return client.rawGaugeAggregateQuery(query, snapshotAt), false
}

func (client *Client) gaugeRollupTable(query telemetryhistorymodel.DeviceHistoryAggregateQuery, periods []calendarPeriod, hasConcurrentWrites bool) (string, bool) {
	if client == nil || client.database != "telemetry_history" || client.table != "observations" || hasConcurrentWrites {
		return "", false
	}
	var step time.Duration
	var table string
	switch query.Granularity {
	case telemetryhistorymodel.AggregateGranularityMinute, telemetryhistorymodel.AggregateGranularityFiveMinute:
		step = time.Minute
		table = "telemetry_history.numeric_gauge_1min_states"
	case telemetryhistorymodel.AggregateGranularityQuarterHour,
		telemetryhistorymodel.AggregateGranularityHour,
		telemetryhistorymodel.AggregateGranularityDay,
		telemetryhistorymodel.AggregateGranularityWeek,
		telemetryhistorymodel.AggregateGranularityMonth:
		step = 15 * time.Minute
		table = "telemetry_history.numeric_gauge_15min_states"
	default:
		return "", false
	}
	location, err := time.LoadLocation(query.Timezone)
	if err != nil {
		return "", false
	}
	if !alignedToStepAndTimezone(query.From, location, step) || !alignedToStepAndTimezone(query.To, location, step) {
		return "", false
	}
	for _, period := range periods {
		if !alignedToStepAndTimezone(period.Start, location, step) || !alignedToStepAndTimezone(period.End, location, step) {
			return "", false
		}
	}
	return table, true
}

func alignedToStepAndTimezone(ts time.Time, location *time.Location, step time.Duration) bool {
	utc := ts.UTC()
	if utc.Nanosecond() != 0 || utc.Second() != 0 {
		return false
	}
	stepMinutes := int(step / time.Minute)
	if stepMinutes <= 0 || utc.Minute()%stepMinutes != 0 {
		return false
	}
	_, offsetSeconds := utc.In(location).Zone()
	stepSeconds := int(step / time.Second)
	return offsetSeconds%stepSeconds == 0
}

func rollupHasConcurrentWrites(rows []aggregateRow, snapshotAt time.Time) bool {
	for _, row := range rows {
		if row.MaxProjectedAt == nil || strings.TrimSpace(*row.MaxProjectedAt) == "" {
			continue
		}
		parsed, err := parseClickHouseTime(*row.MaxProjectedAt)
		if err != nil || !parsed.Before(snapshotAt) {
			return true
		}
	}
	return false
}

func (client *Client) rollupGaugeAggregateQuery(query telemetryhistorymodel.DeviceHistoryAggregateQuery, rollupTable string) string {
	bucket, _ := aggregateBucketExpressionsFor(query, "rollup.bucket", "")
	_, end := aggregateBucketExpressionsFor(query, "rollup.bucket", bucket)
	prefix := "usable"
	if query.QualityPolicy == telemetryhistorymodel.AggregateQualityValidOnly {
		prefix = "valid"
	}
	fromUTC := query.From.UTC().Format("2006-01-02 15:04:05")
	toUTC := query.To.UTC().Format("2006-01-02 15:04:05")
	return fmt.Sprintf(`SELECT
  rollup.telemetry_key AS telemetry_key,
  toString(rollup.point_id) AS point_id,
  rollup.point_revision AS point_revision,
  rollup.point_type AS point_type,
  anyMerge(rollup.unit_state) AS unit,
  formatDateTime(%[1]s, '%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ', 'UTC') AS period_start,
  formatDateTime(%[2]s, '%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ', 'UTC') AS period_end,
  countIfMerge(rollup.good_count) AS good_count,
  countIfMerge(rollup.partial_count) AS partial_count,
  countIfMerge(rollup.estimated_count) AS estimated_count,
  countIfMerge(rollup.manual_count) AS manual_count,
  countIfMerge(rollup.stale_count) AS stale_count,
  countIfMerge(rollup.invalid_count) AS invalid_count,
  countMerge(rollup.total_count) AS total_count,
  countIfMerge(rollup.%[3]s_count) AS included_count,
  avgIfMerge(rollup.%[3]s_avg) AS average,
  minIfMerge(rollup.%[3]s_min) AS minimum,
  maxIfMerge(rollup.%[3]s_max) AS maximum,
  argMinIfMerge(rollup.%[3]s_first) AS first,
  argMaxIfMerge(rollup.%[3]s_last) AS last,
  CAST(NULL, 'Nullable(Float64)') AS delta_sum,
  toInt64(0) AS delta_count,
  toInt64(0) AS reset_count,
  toInt64(0) AS rollover_count,
  toInt64(0) AS excluded_transition_count,
  CAST(NULL, 'Nullable(String)') AS state_value_type,
  CAST(NULL, 'Nullable(String)') AS state_last_value,
  toInt64(0) AS state_sample_count,
  toInt64(0) AS state_change_count,
  CAST([], 'Array(String)') AS state_values,
  formatDateTime(maxMerge(rollup.max_projected_at), '%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ', 'UTC') AS max_projected_at
FROM %[4]s AS rollup
WHERE rollup.tenant_id = toUUID('%[5]s')
  AND rollup.site_id = toUUID('%[6]s')
  AND rollup.device_id = toUUID('%[7]s')
  AND rollup.telemetry_key IN (%[8]s)
  AND rollup.point_type IN ('TELEMETRY', 'SETTING')
  AND rollup.bucket >= toDateTime('%[9]s', 'UTC')
  AND rollup.bucket < toDateTime('%[10]s', 'UTC')
GROUP BY rollup.telemetry_key, toString(rollup.point_id), rollup.point_revision, rollup.point_type, %[1]s
HAVING countIfMerge(rollup.%[3]s_count) > 0
FORMAT JSONEachRow`, bucket, end, prefix, rollupTable, query.TenantID, query.SiteID, query.DeviceID, quotedKeys(query.Keys), fromUTC, toUTC)
}

func (client *Client) rawGaugeAggregateQuery(query telemetryhistorymodel.DeviceHistoryAggregateQuery, snapshotAt time.Time) string {
	bucket, end := aggregateBucketExpressions(query)
	included := aggregateQualityPredicate(query.QualityPolicy, "quality")
	return fmt.Sprintf(`WITH %s AS bucket
SELECT
  telemetry_key,
  toString(assumeNotNull(point_id)) AS point_id,
  assumeNotNull(point_revision) AS point_revision,
  assumeNotNull(point_type) AS point_type,
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
  toInt64(0) AS state_change_count,
  CAST([], 'Array(String)') AS state_values
FROM %s.%s
WHERE tenant_id = toUUID('%s') AND site_id = toUUID('%s') AND device_id = toUUID('%s')
  AND telemetry_key IN (%s)
  AND sampled_at >= parseDateTime64BestEffort('%s', 3, 'UTC') AND sampled_at < parseDateTime64BestEffort('%s', 3, 'UTC')
  AND projected_at < parseDateTime64BestEffort('%s', 3, 'UTC')
  AND acceptance_status IN ('ACCEPTED', 'OUT_OF_ORDER') AND point_type IN ('TELEMETRY', 'SETTING')
  AND point_id IS NOT NULL AND point_revision IS NOT NULL AND value_type = 'NUMBER' AND value_number IS NOT NULL AND isFinite(value_number)
GROUP BY telemetry_key, point_id, point_revision, point_type, bucket
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
  toInt64(0) AS state_change_count,
  CAST([], 'Array(String)') AS state_values
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
    AND acceptance_status IN ('ACCEPTED', 'OUT_OF_ORDER')
    AND ((point_type = 'STATE' AND value_type IN ('STRING', 'BOOLEAN', 'JSON', 'NUMBER')) OR (point_type IN ('TELEMETRY', 'SETTING') AND value_type IN ('STRING', 'BOOLEAN', 'JSON')))
    AND point_id IS NOT NULL AND point_revision IS NOT NULL AND value_json IS NOT NULL
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
  assumeNotNull(point_type) AS point_type,
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
  countIf(%s AND previous_value_json IS NOT NULL AND value_json != previous_value_json) AS state_change_count,
  groupArrayIf(assumeNotNull(value_json), %s) AS state_values
FROM scoped
GROUP BY telemetry_key, point_id, point_revision, point_type, bucket
HAVING included_count > 0
FORMAT JSONEachRow`, client.database, client.table, query.TenantID, query.SiteID, query.DeviceID, quotedKeys(query.Keys), formatClickHouseTime(query.To), formatClickHouseTime(snapshotAt), bucket, formatClickHouseTime(query.From), end, included, included, included, included, included, included)
}

func aggregateBucketExpressions(query telemetryhistorymodel.DeviceHistoryAggregateQuery) (string, string) {
	return aggregateBucketExpressionsFor(query, "sampled_at", "bucket")
}

func aggregateBucketExpressionsFor(query telemetryhistorymodel.DeviceHistoryAggregateQuery, sourceColumn, bucketAlias string) (string, string) {
	switch query.Granularity {
	case telemetryhistorymodel.AggregateGranularityMinute:
		return fmt.Sprintf("toStartOfInterval(%s, INTERVAL 1 MINUTE, '%s')", sourceColumn, query.Timezone), fmt.Sprintf("addMinutes(%s, 1)", bucketAlias)
	case telemetryhistorymodel.AggregateGranularityFiveMinute:
		return fmt.Sprintf("toStartOfInterval(%s, INTERVAL 5 MINUTE, '%s')", sourceColumn, query.Timezone), fmt.Sprintf("addMinutes(%s, 5)", bucketAlias)
	case telemetryhistorymodel.AggregateGranularityQuarterHour:
		return fmt.Sprintf("toStartOfInterval(%s, INTERVAL 15 MINUTE, '%s')", sourceColumn, query.Timezone), fmt.Sprintf("addMinutes(%s, 15)", bucketAlias)
	case telemetryhistorymodel.AggregateGranularityHour:
		return fmt.Sprintf("toStartOfHour(%s, '%s')", sourceColumn, query.Timezone), fmt.Sprintf("addHours(%s, 1)", bucketAlias)
	case telemetryhistorymodel.AggregateGranularityDay:
		return fmt.Sprintf("toStartOfDay(%s, '%s')", sourceColumn, query.Timezone), fmt.Sprintf("addDays(%s, 1)", bucketAlias)
	case telemetryhistorymodel.AggregateGranularityWeek:
		return fmt.Sprintf("toStartOfWeek(%s, 1, '%s')", sourceColumn, query.Timezone), fmt.Sprintf("addWeeks(%s, 1)", bucketAlias)
	case telemetryhistorymodel.AggregateGranularityMonth:
		return fmt.Sprintf("toStartOfMonth(%s, '%s')", sourceColumn, query.Timezone), fmt.Sprintf("addMonths(%s, 1)", bucketAlias)
	default:
		return sourceColumn, sourceColumn
	}
}

func aggregateQualityPredicate(policy telemetryhistorymodel.AggregateQualityPolicy, column string) string {
	if policy == telemetryhistorymodel.AggregateQualityValidOnly {
		return column + " = 'GOOD'"
	}
	return column + " IN ('GOOD', 'PARTIAL', 'ESTIMATED', 'MANUAL')"
}
