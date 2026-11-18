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

func (s *ClickHouseStore) execute(ctx context.Context, sql string) error {
	_, err := s.doSQL(ctx, http.MethodPost, sql)
	return err
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

func (s *ClickHouseStore) ReadPoint(ctx context.Context, b Binding, pointID string, start, end time.Time) (Input, error) {
	scope := fmt.Sprintf(`tenant_id=toUUID(%s) AND site_id=toUUID(%s) AND point_id=toUUID(%s) AND acceptance_status IN ('ACCEPTED','OUT_OF_ORDER') AND value_number IS NOT NULL`, quote(b.TenantID), quote(b.SiteID), quote(pointID))
	from, to := quote(start.UTC().Format(time.RFC3339Nano)), quote(end.UTC().Format(time.RFC3339Nano))
	// A counter's steps start from its last reading before the window, as Energy Interval Facts
	// do: the step into the window ends inside it. A step between two Point revisions or units
	// compares readings that mean different things, so it counts as neither a rise nor a drop, as
	// counter_deltas marks it REVISION_BOUNDARY or UNIT_BOUNDARY. Hourly and daily energy Metrics
	// also skip cross-gap steps (> 1 hour between consecutive readings) so an outage recovery does
	// not dump multi-hour or multi-day kWh into the recovery hour or day.
	stepGuard := "segment = previous"
	switch strings.ToUpper(strings.TrimSpace(b.Granularity)) {
	case "HOUR", "DAY":
		stepGuard = "segment = previous AND gap_ms <= 3600000"
	}
	sql := fmt.Sprintf(`WITH (SELECT argMax((1, assumeNotNull(value_number), toString((point_revision, ifNull(unit, ''))), toUnixTimestamp64Milli(sampled_at)), (sampled_at, observation_id)) FROM telemetry_history.observations WHERE %[2]s AND %[6]s AND sampled_at < parseDateTime64BestEffort(%[3]s)) AS anchor, arraySort(x -> (x.1, x.2), groupArrayIf((sampled_at, observation_id, assumeNotNull(value_number), toString((point_revision, ifNull(unit, '')))), %[6]s)) AS readings, arrayConcat(if(ifNull(anchor.1 = 1, 0), [assumeNotNull(anchor.2)], []), arrayMap(x -> x.3, readings)) AS ordered, arrayConcat(if(ifNull(anchor.1 = 1, 0), [assumeNotNull(anchor.3)], []), arrayMap(x -> x.4, readings)) AS segments, arrayConcat(if(ifNull(anchor.1 = 1, 0), [assumeNotNull(anchor.4)], []), arrayMap(x -> toUnixTimestamp64Milli(x.1), readings)) AS sampled_ms, arrayMap((d, segment, previous, gap_ms) -> if(%[5]s, d, 0), arrayDifference(ordered), segments, arrayPushFront(arrayPopBack(segments), ''), arrayDifference(sampled_ms)) AS steps SELECT %[1]s AS value, count() AS n, countIf(%[6]s) AS good, argMinIf(value_number,tuple(sampled_at,observation_id),%[6]s) AS first_value, argMaxIf(value_number,tuple(sampled_at,observation_id),%[6]s) AS last_value, dateDiff('millisecond',minIf(sampled_at,%[6]s),maxIf(sampled_at,%[6]s))/1000.0 AS duration_seconds, avgIf(value_number,%[6]s)*dateDiff('millisecond',minIf(sampled_at,%[6]s),maxIf(sampled_at,%[6]s))/3600000.0 AS integral, arraySum(arrayFilter(d -> d > 0, steps)) AS rise, length(arrayFilter(d -> d < 0, steps)) AS drops, arraySum(arrayFilter(d -> d < 0, steps)) AS drop_sum, arraySum(arrayFilter((v, d) -> d < 0, ordered, steps)) AS value_after_drops FROM telemetry_history.observations WHERE %[2]s AND sampled_at >= parseDateTime64BestEffort(%[3]s) AND sampled_at < parseDateTime64BestEffort(%[4]s) FORMAT TabSeparated`, aggregateExpr(b.Aggregation), scope, from, to, stepGuard, qualityEligibleFilter)
	body, err := s.query(ctx, sql)
	if err != nil {
		return Input{}, err
	}
	parts := strings.Split(strings.TrimSpace(string(body)), "\t")
	// An aggregate over no quality-eligible rows is NULL, which TabSeparated writes as \N.
	if len(parts) != 11 || parts[0] == "nan" || parts[0] == "" || parts[0] == `\N` || parts[3] == `\N` || parts[4] == `\N` {
		return Input{}, errors.New("metric point dependency has no numeric observations")
	}
	v, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return Input{}, err
	}
	n, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || n <= 0 {
		return Input{}, errors.New("metric point dependency is empty")
	}
	good, _ := strconv.ParseFloat(parts[2], 64)
	if good <= 0 {
		return Input{}, errors.New("metric point dependency has no numeric observations")
	}
	first, err := strconv.ParseFloat(parts[3], 64)
	if err != nil {
		return Input{}, err
	}
	last, err := strconv.ParseFloat(parts[4], 64)
	if err != nil {
		return Input{}, err
	}
	duration, _ := strconv.ParseFloat(parts[5], 64)
	integral, _ := strconv.ParseFloat(parts[6], 64)
	rise, err := strconv.ParseFloat(parts[7], 64)
	if err != nil {
		return Input{}, err
	}
	drops, err := strconv.ParseInt(parts[8], 10, 64)
	if err != nil {
		return Input{}, err
	}
	dropSum, err := strconv.ParseFloat(parts[9], 64)
	if err != nil {
		return Input{}, err
	}
	valueAfterDrops, err := strconv.ParseFloat(parts[10], 64)
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

func (s *ClickHouseStore) DeleteMetricResult(ctx context.Context, tenantID, siteID, resultID string) error {
	return s.execute(ctx, fmt.Sprintf(`ALTER TABLE analytics.metric_result_facts DELETE WHERE tenant_id=toUUID(%s) AND site_id=toUUID(%s) AND result_id=toUUID(%s)`, quote(tenantID), quote(siteID), quote(resultID)))
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
