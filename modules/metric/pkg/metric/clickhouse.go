package metric

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

type ClickHouseStore struct {
	base           *url.URL
	user, password string
	client         *http.Client
}

func NewClickHouseStore(baseURL, user, password string, client *http.Client) (*ClickHouseStore, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("metric ClickHouse URL must be absolute http/https")
	}
	if client == nil {
		client = newDefaultClickHouseHTTPClient(15 * time.Second)
	}
	return &ClickHouseStore{base: u, user: user, password: password, client: client}, nil
}

func newDefaultClickHouseHTTPClient(timeout time.Duration) *http.Client {
	var transport *http.Transport
	if base, ok := http.DefaultTransport.(*http.Transport); ok && base != nil {
		transport = base.Clone()
	} else {
		transport = &http.Transport{}
	}
	transport.DialContext = (&net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext
	transport.MaxConnsPerHost = 16
	transport.MaxIdleConns = 64
	transport.MaxIdleConnsPerHost = 16
	transport.IdleConnTimeout = 90 * time.Second
	transport.TLSHandshakeTimeout = 10 * time.Second
	transport.ResponseHeaderTimeout = 30 * time.Second
	transport.ExpectContinueTimeout = 1 * time.Second
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}
}

func (s *ClickHouseStore) Ping(ctx context.Context) error {
	_, err := s.query(ctx, "SELECT 1")
	return err
}

func (s *ClickHouseStore) query(ctx context.Context, sql string) ([]byte, error) {
	return s.doSQL(ctx, http.MethodGet, sql)
}

func (s *ClickHouseStore) doSQL(ctx context.Context, method, sql string) ([]byte, error) {
	return doClickHouseHTTPQuery(ctx, s.base, s.user, s.password, s.client, method, sql, 1<<20, "metric ClickHouse query")
}

func isClickHouseSelectQuery(sql string) bool {
	upper := strings.ToUpper(strings.TrimSpace(sql))
	return strings.HasPrefix(upper, "SELECT") || strings.HasPrefix(upper, "WITH")
}

func doClickHouseHTTPQuery(ctx context.Context, base *url.URL, user, password string, client *http.Client, method, sql string, maxBodyBytes int64, label string) ([]byte, error) {
	u := *base
	q := u.Query()
	q.Set("query", sql)
	q.Set("date_time_output_format", "iso")
	selectQuery := method == http.MethodGet || isClickHouseSelectQuery(sql)
	if selectQuery {
		q.Set("enable_http_compression", "1")
	}
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, method, u.String(), nil)
	if err != nil {
		return nil, err
	}
	if selectQuery {
		req.Header.Set("Accept-Encoding", "gzip")
	}
	if user != "" {
		req.SetBasicAuth(user, password)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	const maxDrainBytes = 4096
	bodyReader := io.Reader(io.LimitReader(resp.Body, maxBodyBytes))
	if strings.EqualFold(strings.TrimSpace(resp.Header.Get("Content-Encoding")), "gzip") {
		gz, gzErr := gzip.NewReader(bodyReader)
		if gzErr != nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxDrainBytes))
			if resp.StatusCode/100 != 2 {
				return nil, fmt.Errorf("%s returned %d", label, resp.StatusCode)
			}
			return nil, fmt.Errorf("%s gzip decode: %w", label, gzErr)
		}
		defer gz.Close()
		bodyReader = io.LimitReader(gz, maxBodyBytes)
	}
	body, err := io.ReadAll(bodyReader)
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxDrainBytes))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("%s returned %d: %s", label, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

const qualityEligibleFilter = "quality = 'GOOD' AND isFinite(value_number)"

func quote(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }
func aggregateExpr(name string) string {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "SUM":
		return "sumIf(value_number, " + qualityEligibleFilter + ")"
	case "MIN":
		return "minIf(value_number, " + qualityEligibleFilter + ")"
	case "MAX":
		return "maxIf(value_number, " + qualityEligibleFilter + ")"
	case "FIRST":
		return "argMinIf(value_number, tuple(sampled_at, observation_id), " + qualityEligibleFilter + ")"
	case "LAST":
		return "argMaxIf(value_number, tuple(sampled_at, observation_id), " + qualityEligibleFilter + ")"
	default:
		return "avgIf(value_number, " + qualityEligibleFilter + ")"
	}
}

func parseFiniteFloat(raw string) (float64, error) {
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, err
	}
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, errors.New("non-finite float in ClickHouse response")
	}
	return v, nil
}

type counterSQLConfig struct {
	overrideRateCap   float64
	overrideJitterTol float64
	overrideMode      string
	overrideModulus   float64
}

func counterQueryConfig(b Binding) counterSQLConfig {
	cfg := counterSQLConfig{}
	counter, _ := b.SourceDefinition["counter"].(map[string]any)
	if counter == nil {
		return cfg
	}
	if v, ok := asFinitePositiveFloat(counter["maxPowerKw"]); ok {
		cfg.overrideRateCap = v
	} else if v, ok := asFinitePositiveFloat(counter["maxStep"]); ok {
		if winSec, hasWin := asFinitePositiveFloat(counter["maxStepWindowSeconds"]); hasWin {
			cfg.overrideRateCap = v / (winSec / 3600.0)
		} else {
			cfg.overrideRateCap = v * 4.0
		}
	}
	if v, ok := asFinitePositiveFloat(counter["jitterTolerance"]); ok {
		cfg.overrideJitterTol = v
	}
	if mode, ok := counter["decreaseMode"].(string); ok {
		cfg.overrideMode = strings.ToUpper(strings.TrimSpace(mode))
	}
	if v, ok := asFinitePositiveFloat(counter["rolloverModulus"]); ok {
		cfg.overrideModulus = v
	}
	return cfg
}

func asFinitePositiveFloat(raw any) (float64, bool) {
	switch v := raw.(type) {
	case float64:
		return v, !math.IsNaN(v) && !math.IsInf(v, 0) && v > 0
	case int:
		return float64(v), v > 0
	case int64:
		return float64(v), v > 0
	case json.Number:
		parsed, err := v.Float64()
		return parsed, err == nil && !math.IsNaN(parsed) && !math.IsInf(parsed, 0) && parsed > 0
	default:
		return 0, false
	}
}

func (s *ClickHouseStore) ReadPoint(ctx context.Context, b Binding, pointID string, start, end time.Time) (Input, error) {
	scope := fmt.Sprintf(`tenant_id=toUUID(%s) AND site_id=toUUID(%s) AND point_id=toUUID(%s) AND acceptance_status IN ('ACCEPTED','OUT_OF_ORDER') AND value_number IS NOT NULL`, quote(b.TenantID), quote(b.SiteID), quote(pointID))
	from, to := quote(start.UTC().Format(time.RFC3339Nano)), quote(end.UTC().Format(time.RFC3339Nano))
	// A counter's steps start from its last reading before the window, as Energy Interval Facts
	// do: the step into the window ends inside it. Steps across revisions/units are skipped, and
	// hourly/daily metrics skip >1h cross-gap jumps. arrayFold also isolates bus zero-drops, tiny
	// communication jitter rollbacks, and single-point spikes exceeding physical step-rate bounds.
	stepGuard := "segment = previous"
	switch strings.ToUpper(strings.TrimSpace(b.Granularity)) {
	case "HOUR", "DAY":
		stepGuard = "segment = previous AND gap_ms <= 3600000"
	}
	cfg := counterQueryConfig(b)
	overrideRateSQL := "0.0"
	if cfg.overrideRateCap > 0 {
		overrideRateSQL = strconv.FormatFloat(cfg.overrideRateCap, 'g', -1, 64)
	}
	overrideJitterSQL := "0.0"
	if cfg.overrideJitterTol > 0 {
		overrideJitterSQL = strconv.FormatFloat(cfg.overrideJitterTol, 'g', -1, 64)
	}
	overrideModeSQL := quote(cfg.overrideMode)
	overrideModulusSQL := strconv.FormatFloat(cfg.overrideModulus, 'g', -1, 64)

	rateExpr := fmt.Sprintf(`if(%[1]s > 0.0, if(__UNIT__ IN ('Wh','W·h'), %[1]s * 1000.0, if(__UNIT__ IN ('MWh','MW·h'), %[1]s / 1000.0, %[1]s)), multiIf(__UNIT__ IN ('Wh','W·h'), 100000000.0, __UNIT__ IN ('MWh','MW·h'), 100.0, __UNIT__ = 'GJ', 360.0, __UNIT__ = 'MJ', 360000.0, __UNIT__ IN ('RTh','RT·h'), 30000.0, 100000.0))`, overrideRateSQL)
	jitterExpr := fmt.Sprintf(`if(%[1]s > 0.0, %[1]s, multiIf(__UNIT__ IN ('Wh','W·h'), 500.0, __UNIT__ IN ('MWh','MW·h'), 0.0005, __UNIT__ = 'GJ', 0.15, __UNIT__ = 'MJ', 150.0, __UNIT__ IN ('m3','m³','h','count'), 0.05, 0.5))`, overrideJitterSQL)
	modeExpr := fmt.Sprintf(`if(%[1]s != '', %[1]s, upper(trimBoth(__MODE__)))`, overrideModeSQL)
	modulusExpr := fmt.Sprintf(`if(%[1]s > 0.0, %[1]s, __MODULUS__)`, overrideModulusSQL)

	sql := fmt.Sprintf(`WITH (SELECT arraySort(x -> (x.1, x.2), groupArray((sampled_at, observation_id, assumeNotNull(value_number), toString((point_revision, ifNull(unit, ''))), toString(ifNull(unit, '')), toString(ifNull(counter_decrease_mode, '')), toFloat64(ifNull(counter_rollover_modulus, 0.0))))) FROM (SELECT sampled_at, observation_id, value_number, point_revision, unit, counter_decrease_mode, counter_rollover_modulus FROM telemetry_history.observations WHERE %[2]s AND %[6]s AND sampled_at < parseDateTime64BestEffort(%[3]s, 3, 'UTC') ORDER BY sampled_at DESC, observation_id DESC LIMIT 8)) AS anchor, arraySort(x -> (x.1, x.2), groupArrayIf((sampled_at, observation_id, assumeNotNull(value_number), toString((point_revision, ifNull(unit, '')))), %[6]s)) AS readings, arraySort(x -> (x.1, x.2), groupArrayIf((sampled_at, observation_id, toString(ifNull(unit, '')), toString(ifNull(counter_decrease_mode, '')), toFloat64(ifNull(counter_rollover_modulus, 0.0))), %[6]s)) AS reading_meta, arrayConcat(if(length(anchor) > 0, [anchor[length(anchor)].3], []), arrayMap(x -> x.3, readings)) AS ordered, arrayConcat(if(length(anchor) > 0, [anchor[length(anchor)].4], []), arrayMap(x -> x.4, readings)) AS segments, arrayConcat(if(length(anchor) > 0, [toUnixTimestamp64Milli(anchor[length(anchor)].1)], []), arrayMap(x -> toUnixTimestamp64Milli(x.1), readings)) AS sampled_ms, arrayMap((d, segment, previous, gap_ms) -> if(%[5]s, d, 0), arrayDifference(ordered), segments, arrayPushFront(arrayPopBack(segments), ''), arrayDifference(sampled_ms)) AS raw_steps, arrayConcat(arrayMap(x -> tuple(toFloat64(x.3), toString(x.4), toInt64(toUnixTimestamp64Milli(x.1)), toUInt8(0), toString(x.5), toString(x.6), toFloat64(x.7)), anchor), arrayMap((x, meta) -> tuple(toFloat64(x.3), toString(x.4), toInt64(toUnixTimestamp64Milli(x.1)), toUInt8(1), toString(meta.3), toString(meta.4), toFloat64(meta.5)), readings, reading_meta)) AS seq_entries, arrayFold((acc, el) -> multiIf(acc.3 = 0, tuple(acc.1, acc.2, toUInt8(1), toFloat64(el.1), toString(el.2), toInt64(el.3)), el.2 != acc.5, tuple(if(el.4 = 1, arrayPushBack(acc.1, toFloat64(0.0)), acc.1), if(el.4 = 1, arrayPushBack(acc.2, toFloat64(el.1)), acc.2), toUInt8(1), toFloat64(el.1), toString(el.2), toInt64(el.3)), el.1 = 0 AND acc.4 > 0, tuple(if(el.4 = 1, arrayPushBack(acc.1, toFloat64(0.0)), acc.1), if(el.4 = 1, arrayPushBack(acc.2, toFloat64(acc.4)), acc.2), acc.3, acc.4, acc.5, acc.6), el.1 > acc.4 AND (el.1 - acc.4) > ((%[7]s) * greatest(toFloat64(el.3 - acc.6) / 3600000.0, 1.0 / 60.0)), tuple(if(el.4 = 1, arrayPushBack(acc.1, toFloat64(0.0)), acc.1), if(el.4 = 1, arrayPushBack(acc.2, toFloat64(acc.4)), acc.2), acc.3, acc.4, acc.5, acc.6), el.1 > 0 AND el.1 < acc.4 AND (acc.4 - el.1) <= (%[8]s), tuple(if(el.4 = 1, arrayPushBack(acc.1, toFloat64(0.0)), acc.1), if(el.4 = 1, arrayPushBack(acc.2, toFloat64(acc.4)), acc.2), acc.3, acc.4, acc.5, toInt64(el.3)), el.1 < acc.4 AND (%[9]s) IN ('RESET','RESET_TO_ZERO','METER_REPLACEMENT') AND el.1 > ((%[7]s) * greatest(toFloat64(el.3 - acc.6) / 3600000.0, 1.0 / 60.0)), tuple(if(el.4 = 1, arrayPushBack(acc.1, toFloat64(0.0)), acc.1), if(el.4 = 1, arrayPushBack(acc.2, toFloat64(acc.4)), acc.2), acc.3, acc.4, acc.5, acc.6), el.1 < acc.4 AND (%[9]s) = 'ROLLOVER' AND ((%[10]s) <= 0.0 OR acc.4 > (%[10]s) OR el.1 >= (%[10]s) OR (((%[10]s) - acc.4) + el.1) > ((%[7]s) * greatest(toFloat64(el.3 - acc.6) / 3600000.0, 1.0 / 60.0))), tuple(if(el.4 = 1, arrayPushBack(acc.1, toFloat64(0.0)), acc.1), if(el.4 = 1, arrayPushBack(acc.2, toFloat64(acc.4)), acc.2), acc.3, acc.4, acc.5, acc.6), el.2 = acc.5 AND (%[11]s), tuple(if(el.4 = 1, arrayPushBack(acc.1, toFloat64(el.1 - acc.4)), acc.1), if(el.4 = 1, arrayPushBack(acc.2, toFloat64(el.1)), acc.2), toUInt8(1), toFloat64(el.1), toString(el.2), toInt64(el.3)), tuple(if(el.4 = 1, arrayPushBack(acc.1, toFloat64(0.0)), acc.1), if(el.4 = 1, arrayPushBack(acc.2, toFloat64(el.1)), acc.2), toUInt8(1), toFloat64(el.1), toString(el.2), toInt64(el.3))), seq_entries, tuple(CAST([], 'Array(Float64)'), CAST([], 'Array(Float64)'), toUInt8(0), toFloat64(0.0), toString(''), toInt64(0))) AS folded, folded.1 AS steps, folded.2 AS clean_ordered SELECT %[1]s AS value, count() AS n, countIf(%[6]s) AS good, argMinIf(value_number,tuple(sampled_at,observation_id),%[6]s) AS first_value, argMaxIf(value_number,tuple(sampled_at,observation_id),%[6]s) AS last_value, dateDiff('millisecond',minIf(sampled_at,%[6]s),maxIf(sampled_at,%[6]s))/1000.0 AS duration_seconds, avgIf(value_number,%[6]s)*dateDiff('millisecond',minIf(sampled_at,%[6]s),maxIf(sampled_at,%[6]s))/3600000.0 AS integral, arraySum(arrayFilter(d -> d > 0, steps)) AS rise, length(arrayFilter(d -> d < 0, steps)) AS drops, arraySum(arrayFilter(d -> d < 0, steps)) AS drop_sum, arraySum(arrayFilter((v, d) -> d < 0, clean_ordered, steps)) AS value_after_drops FROM telemetry_history.observations WHERE %[2]s AND sampled_at >= parseDateTime64BestEffort(%[3]s, 3, 'UTC') AND sampled_at < parseDateTime64BestEffort(%[4]s, 3, 'UTC') FORMAT TabSeparated`,
		aggregateExpr(b.Aggregation),
		scope,
		from,
		to,
		stepGuard,
		qualityEligibleFilter,
		strings.ReplaceAll(rateExpr, "__UNIT__", "el.5"),
		strings.ReplaceAll(jitterExpr, "__UNIT__", "el.5"),
		strings.ReplaceAll(modeExpr, "__MODE__", "el.6"),
		strings.ReplaceAll(modulusExpr, "__MODULUS__", "el.7"),
		strings.NewReplacer("segment", "el.2", "previous", "acc.5", "gap_ms", "(el.3 - acc.6)").Replace(stepGuard),
	)
	body, err := s.query(ctx, sql)
	if err != nil {
		return Input{}, err
	}
	parts := strings.Split(strings.TrimSpace(string(body)), "\t")
	// An aggregate over no quality-eligible rows is NULL, which TabSeparated writes as \N.
	if len(parts) != 11 || parts[0] == "nan" || parts[0] == "" || parts[0] == `\N` || parts[3] == `\N` || parts[4] == `\N` {
		return Input{}, errors.New("metric point dependency has no numeric observations")
	}
	v, err := parseFiniteFloat(parts[0])
	if err != nil {
		return Input{}, err
	}
	n, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || n <= 0 {
		return Input{}, errors.New("metric point dependency is empty")
	}
	good, err := parseFiniteFloat(parts[2])
	if err != nil || good <= 0 {
		return Input{}, errors.New("metric point dependency has no numeric observations")
	}
	first, err := parseFiniteFloat(parts[3])
	if err != nil {
		return Input{}, err
	}
	last, err := parseFiniteFloat(parts[4])
	if err != nil {
		return Input{}, err
	}
	duration, err := parseFiniteFloat(parts[5])
	if err != nil {
		return Input{}, err
	}
	integral, err := parseFiniteFloat(parts[6])
	if err != nil {
		return Input{}, err
	}
	rise, err := parseFiniteFloat(parts[7])
	if err != nil {
		return Input{}, err
	}
	drops, err := strconv.ParseInt(parts[8], 10, 64)
	if err != nil {
		return Input{}, err
	}
	dropSum, err := parseFiniteFloat(parts[9])
	if err != nil {
		return Input{}, err
	}
	valueAfterDrops, err := parseFiniteFloat(parts[10])
	if err != nil {
		return Input{}, err
	}
	q := "GOOD"
	c := 1.0
	if good < float64(n) {
		q = "PARTIAL"
		c = good / float64(n)
	}
	return Input{Reference: "point:" + pointID, Value: v, FirstValue: first, LastValue: last, Count: n, DurationSeconds: duration, Integral: integral, Rise: rise, Drops: drops, DropSum: dropSum, ValueAfterDrops: valueAfterDrops, Quality: q, Completeness: c}, nil
}

// COPGuardrailOptions defines thermal/standby guardrails and site timezone for COP queries.
type COPGuardrailOptions struct {
	SiteTimezone  string
	MinPowerKW    float64
	MinCoolingKW  float64
	MinDeltaTK    float64
	MinCOP        float64
	MaxCOP        float64
	DeltaTPointID string
	SupplyPointID string
	ReturnPointID string
}

// COPAggregate holds energy-weighted COP results over aligned time buckets.
type COPAggregate struct {
	COP             float64
	CoolingKWh      float64
	ElectricKWh     float64
	EligibleBuckets int64
	TotalBuckets    int64
	Quality         string
	Completeness    float64
}

// ReadEnergyWeightedCOP computes sum(cooling_kw * dt) / sum(power_kw * dt) over aligned
// site-timezone minute buckets, filtering standby low power, negative/deadband delta-T,
// non-finite values, and out-of-range COPs instead of averaging instantaneous ratios.
func (s *ClickHouseStore) ReadEnergyWeightedCOP(ctx context.Context, b Binding, coolingPointID, powerPointID string, start, end time.Time, opts COPGuardrailOptions) (COPAggregate, error) {
	if !end.After(start) {
		return COPAggregate{}, errors.New("COP window end must follow start")
	}
	tz := strings.TrimSpace(opts.SiteTimezone)
	if tz == "" {
		tz = "Asia/Shanghai"
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return COPAggregate{}, fmt.Errorf("invalid COP site timezone %q: %w", tz, err)
	}
	minPower := opts.MinPowerKW
	if minPower <= 0 {
		minPower = 5.0
	}
	minCooling := opts.MinCoolingKW
	if minCooling < 0 {
		minCooling = 0
	}
	minCOP := opts.MinCOP
	if minCOP <= 0 {
		minCOP = 0.5
	}
	maxCOP := opts.MaxCOP
	if maxCOP <= 0 {
		maxCOP = 12.0
	}
	minDeltaT := opts.MinDeltaTK
	if minDeltaT <= 0 {
		minDeltaT = 0.5
	}

	from, to := quote(start.UTC().Format(time.RFC3339Nano)), quote(end.UTC().Format(time.RFC3339Nano))
	tzSQL := quote(tz)
	deltaTSelectSQL := ", 1 AS delta_t_ok"
	deltaTFilterSQL := ""
	if opts.DeltaTPointID != "" {
		deltaTSelectSQL = fmt.Sprintf(`, avgIf(value_number, point_id = toUUID(%s) AND %s) AS dt_k`, quote(opts.DeltaTPointID), qualityEligibleFilter)
		deltaTFilterSQL = fmt.Sprintf(` AND dt_k IS NOT NULL AND dt_k >= %g`, minDeltaT)
	} else if opts.SupplyPointID != "" && opts.ReturnPointID != "" {
		deltaTSelectSQL = fmt.Sprintf(`, avgIf(value_number, point_id = toUUID(%s) AND %s) AS ret_c, avgIf(value_number, point_id = toUUID(%s) AND %s) AS sup_c`, quote(opts.ReturnPointID), qualityEligibleFilter, quote(opts.SupplyPointID), qualityEligibleFilter)
		deltaTFilterSQL = fmt.Sprintf(` AND ret_c IS NOT NULL AND sup_c IS NOT NULL AND (ret_c - sup_c) >= %g`, minDeltaT)
	}
	pointIDs := []string{fmt.Sprintf("toUUID(%s)", quote(coolingPointID)), fmt.Sprintf("toUUID(%s)", quote(powerPointID))}
	if opts.DeltaTPointID != "" {
		pointIDs = append(pointIDs, fmt.Sprintf("toUUID(%s)", quote(opts.DeltaTPointID)))
	}
	if opts.SupplyPointID != "" && opts.ReturnPointID != "" {
		pointIDs = append(pointIDs, fmt.Sprintf("toUUID(%s)", quote(opts.SupplyPointID)), fmt.Sprintf("toUUID(%s)", quote(opts.ReturnPointID)))
	}

	sql := fmt.Sprintf(`WITH buckets AS (
  SELECT
    toStartOfMinute(toTimeZone(sampled_at, %[4]s)) AS local_bucket,
    avgIf(value_number, point_id = toUUID(%[5]s) AND %[8]s) AS cooling_kw,
    avgIf(value_number, point_id = toUUID(%[6]s) AND %[8]s) AS power_kw%[9]s
  FROM telemetry_history.observations
  WHERE tenant_id = toUUID(%[1]s)
    AND site_id = toUUID(%[2]s)
    AND point_id IN (%[7]s)
    AND acceptance_status IN ('ACCEPTED', 'OUT_OF_ORDER')
    AND value_number IS NOT NULL
    AND sampled_at >= parseDateTime64BestEffort(%[3]s, 3, 'UTC')
    AND sampled_at < parseDateTime64BestEffort(%[10]s, 3, 'UTC')
  GROUP BY local_bucket
)
SELECT
  sumIf(cooling_kw / 60.0, cooling_kw IS NOT NULL AND power_kw IS NOT NULL AND isFinite(cooling_kw) AND isFinite(power_kw) AND cooling_kw >= %[11]g AND power_kw >= %[12]g%[13]s) AS cooling_kwh,
  sumIf(power_kw / 60.0, cooling_kw IS NOT NULL AND power_kw IS NOT NULL AND isFinite(cooling_kw) AND isFinite(power_kw) AND cooling_kw >= %[11]g AND power_kw >= %[12]g%[13]s) AS electric_kwh,
  countIf(cooling_kw IS NOT NULL AND power_kw IS NOT NULL AND isFinite(cooling_kw) AND isFinite(power_kw) AND cooling_kw >= %[11]g AND power_kw >= %[12]g%[13]s) AS eligible_buckets,
  count() AS total_buckets
FROM buckets
FORMAT TabSeparated`,
		quote(b.TenantID), quote(b.SiteID), from, tzSQL, quote(coolingPointID), quote(powerPointID),
		strings.Join(pointIDs, ", "), qualityEligibleFilter, deltaTSelectSQL, to,
		minCooling, minPower, deltaTFilterSQL,
	)

	body, err := s.query(ctx, sql)
	if err != nil {
		return COPAggregate{}, err
	}
	parts := strings.Split(strings.TrimSpace(string(body)), "\t")
	if len(parts) != 4 {
		return COPAggregate{}, errors.New("unexpected ClickHouse COP response")
	}
	coolingKWh, err := parseFiniteFloat(parts[0])
	if err != nil {
		return COPAggregate{}, err
	}
	electricKWh, err := parseFiniteFloat(parts[1])
	if err != nil {
		return COPAggregate{}, err
	}
	eligible, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return COPAggregate{}, err
	}
	total, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil {
		return COPAggregate{}, err
	}
	if eligible <= 0 || electricKWh <= 0 {
		return COPAggregate{}, errors.New("COP window has no active cooling/power buckets above standby deadband")
	}
	cop := coolingKWh / electricKWh
	if math.IsNaN(cop) || math.IsInf(cop, 0) {
		return COPAggregate{}, errors.New("COP calculation produced non-finite result")
	}
	if cop < minCOP || cop > maxCOP {
		return COPAggregate{}, fmt.Errorf("energy-weighted COP %.3f outside physical bounds [%.2f, %.2f]", cop, minCOP, maxCOP)
	}
	quality := "GOOD"
	completeness := 1.0
	if total > 0 && eligible < total {
		quality = "PARTIAL"
		completeness = float64(eligible) / float64(total)
	}
	return COPAggregate{
		COP:             cop,
		CoolingKWh:      coolingKWh,
		ElectricKWh:     electricKWh,
		EligibleBuckets: eligible,
		TotalBuckets:    total,
		Quality:         quality,
		Completeness:    completeness,
	}, nil
}
func metricResultWhereClause(result Result) string {
	predicates := make([]string, 0, 9)
	if result.Binding.TenantID != "" {
		predicates = append(predicates, fmt.Sprintf("tenant_id=toUUID(%s)", quote(result.Binding.TenantID)))
	}
	if result.Binding.SiteID != "" {
		predicates = append(predicates, fmt.Sprintf("site_id=toUUID(%s)", quote(result.Binding.SiteID)))
	}
	if result.Binding.MetricID != "" {
		predicates = append(predicates, fmt.Sprintf("metric_id=toUUID(%s)", quote(result.Binding.MetricID)))
	}
	if result.Binding.SubjectType != "" {
		predicates = append(predicates, fmt.Sprintf("subject_type=%s", quote(result.Binding.SubjectType)))
	}
	if result.Binding.SubjectID != "" {
		predicates = append(predicates, fmt.Sprintf("subject_id=toUUID(%s)", quote(result.Binding.SubjectID)))
	}
	if result.Binding.Granularity != "" {
		predicates = append(predicates, fmt.Sprintf("granularity=%s", quote(result.Binding.Granularity)))
	}
	if !result.PeriodStart.IsZero() {
		predicates = append(predicates, fmt.Sprintf("period_start=parseDateTime64BestEffort(%s,3,'UTC')", quote(result.PeriodStart.UTC().Format(time.RFC3339Nano))))
	}
	if !result.PeriodEnd.IsZero() {
		predicates = append(predicates, fmt.Sprintf("period_end=parseDateTime64BestEffort(%s,3,'UTC')", quote(result.PeriodEnd.UTC().Format(time.RFC3339Nano))))
	}
	predicates = append(predicates, fmt.Sprintf("result_id=toUUID(%s)", quote(result.ResultID)))
	return strings.Join(predicates, " AND ")
}

func (s *ClickHouseStore) HasMetricResult(ctx context.Context, result Result) (bool, error) {
	body, err := s.query(ctx, fmt.Sprintf(`SELECT count() FROM analytics.metric_result_facts WHERE %s FORMAT TabSeparated`, metricResultWhereClause(result)))
	if err != nil {
		return false, err
	}
	count, err := strconv.ParseInt(strings.TrimSpace(string(body)), 10, 64)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *ClickHouseStore) ReadMetricResult(ctx context.Context, result Result) (Result, error) {
	body, err := s.query(ctx, fmt.Sprintf(`SELECT value_number,unit,quality,completeness,calculated_at,revision FROM analytics.metric_result_facts WHERE %s LIMIT 1 FORMAT TabSeparated`, metricResultWhereClause(result)))
	if err != nil {
		return Result{}, err
	}
	parts := strings.Split(strings.TrimSpace(string(body)), "\t")
	if len(parts) != 6 || parts[0] == "\\N" || parts[0] == "" {
		return Result{}, errors.New("metric result fact is unavailable")
	}
	value, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return Result{}, err
	}
	completeness, err := strconv.ParseFloat(parts[3], 64)
	if err != nil {
		return Result{}, err
	}
	calculatedAt, err := time.Parse(time.RFC3339Nano, parts[4])
	if err != nil {
		calculatedAt, err = time.Parse("2006-01-02 15:04:05.000", parts[4])
		if err != nil {
			return Result{}, err
		}
	}
	revision, err := strconv.ParseUint(parts[5], 10, 64)
	if err != nil {
		return Result{}, err
	}
	if result.Revision != 0 && revision != result.Revision {
		return Result{}, errors.New("metric result fact revision does not match PostgreSQL publication evidence")
	}
	result.Revision = revision
	result.Value = value
	result.Binding.Unit = parts[1]
	result.Quality = parts[2]
	result.Completeness = completeness
	result.CalculatedAt = calculatedAt.UTC()
	return result, nil
}

func (s *ClickHouseStore) InsertMetric(ctx context.Context, r Result) error {
	return s.InsertMetrics(ctx, []Result{r})
}

// InsertMetrics writes one or more metric results in a single async-insert block
// with a deterministic SHA-256 deduplication token across retries.
func (s *ClickHouseStore) InsertMetrics(ctx context.Context, results []Result) error {
	if s == nil || s.base == nil || s.client == nil {
		return errors.New("metric ClickHouse store is closed")
	}
	if len(results) == 0 {
		return nil
	}
	ordered := slices.Clone(results)
	slices.SortFunc(ordered, func(a, b Result) int {
		return strings.Compare(a.ResultID, b.ResultID)
	})
	var body bytes.Buffer
	enc := json.NewEncoder(&body)
	enc.SetEscapeHTML(false)
	for _, r := range ordered {
		provenance, err := json.Marshal(map[string]any{"calculationRunId": r.RunID, "inputs": SortedInputs(r.Inputs)})
		if err != nil {
			return fmt.Errorf("encode metric provenance: %w", err)
		}
		row := map[string]any{
			"result_id": r.ResultID, "tenant_id": r.Binding.TenantID, "site_id": r.Binding.SiteID,
			"subject_type": r.Binding.SubjectType, "subject_id": r.Binding.SubjectID,
			"metric_id": r.Binding.MetricID, "metric_version_id": r.Binding.MetricVersionID,
			"metric_code": r.Binding.MetricCode, "metric_version": r.Binding.MetricVersion,
			"metric_binding_id": r.Binding.BindingID, "binding_version": r.Binding.BindingVersion,
			"period_start": r.PeriodStart.UTC(), "period_end": r.PeriodEnd.UTC(), "calculated_at": r.CalculatedAt.UTC(),
			"granularity": r.Binding.Granularity, "value_type": "NUMBER",
			"value_json": strconv.FormatFloat(r.Value, 'g', -1, 64), "value_number": r.Value,
			"unit": r.Binding.Unit, "quality": r.Quality, "completeness": r.Completeness,
			"calculation_run_id": r.RunID, "revision": r.Revision, "provenance": string(provenance),
		}
		if err := enc.Encode(row); err != nil {
			return err
		}
	}

	raw := body.Bytes()
	token := fmt.Sprintf("%x", sha256.Sum256(raw))
	payload := raw
	compressed := false
	if len(ordered) > 1 && len(raw) >= 4096 {
		var gzBuf bytes.Buffer
		gz, err := gzip.NewWriterLevel(&gzBuf, gzip.BestSpeed)
		if err != nil {
			return fmt.Errorf("create gzip writer for metric batch: %w", err)
		}
		if _, err := gz.Write(raw); err != nil {
			_ = gz.Close()
			return fmt.Errorf("compress metric result batch: %w", err)
		}
		if err := gz.Close(); err != nil {
			return fmt.Errorf("finalize compressed metric result batch: %w", err)
		}
		payload = gzBuf.Bytes()
		compressed = true
	}

	u := *s.base
	q := u.Query()
	q.Set("database", "analytics")
	q.Set("query", "INSERT INTO analytics.metric_result_facts FORMAT JSONEachRow")
	q.Set("date_time_input_format", "best_effort")
	q.Set("async_insert", "1")
	q.Set("wait_for_async_insert", "1")
	q.Set("async_insert_deduplicate", "1")
	q.Set("insert_deduplication_token", token)
	q.Set("wait_end_of_query", "1")
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-ndjson")
	if compressed {
		req.Header.Set("Content-Encoding", "gzip")
	}
	if s.user != "" {
		req.SetBasicAuth(s.user, s.password)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("insert metric result returned %d: %s", resp.StatusCode, strings.TrimSpace(string(detail)))
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return nil
}
