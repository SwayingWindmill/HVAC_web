package metric

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// A window before a point has any history aggregates to NULL, which ClickHouse writes as \N;
// it once surfaced as a ParseFloat error that every backfill of an empty window retried.
func TestReadPointReportsAnEmptyWindowAsNoObservations(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("\\N\t0\t0\t\\N\t\\N\t\\N\t\\N\t0\t0\t0\t0\n"))
	}))
	defer server.Close()
	store, err := NewClickHouseStore(server.URL, "", "", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	_, err = store.ReadPoint(context.Background(), Binding{TenantID: "t", SiteID: "s", Aggregation: "AVG"}, "p", start, start.Add(15*time.Minute))
	if err == nil || !strings.Contains(err.Error(), "no numeric observations") {
		t.Fatalf("empty window err=%v", err)
	}
}

// A counter that restarts inside a window still delivered the energy before the restart: the
// window's delta is every rise between consecutive readings plus what counted after each drop,
// never only last minus first.
func TestCounterDeltaAddsEveryRiseAcrossARestartInTheWindow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body := request.URL.Query().Get("query")
		if body == "" {
			raw, _ := io.ReadAll(request.Body)
			body = string(raw)
		}
		if !strings.Contains(body, "arrayDifference") {
			t.Errorf("counter read does not step through consecutive readings:\n%s", body)
		}
		// The working arrays once leaked into the output as two extra columns, and every
		// read was then taken for an empty window.
		outer := body[strings.LastIndex(body, "SELECT ")+len("SELECT "):]
		selected := outer[:strings.Index(outer, " FROM ")]
		if columns := strings.Count(selected, " AS "); columns != 11 {
			t.Errorf("point read selects %d columns, want 11:\n%s", columns, selected)
		}
		// value, n, good, first, last, duration, integral, rise, drops, drop_sum, value_after_drops
		_, _ = writer.Write([]byte("1.27\t5194\t5194\t0.002\t1.27\t10440\t0\t523.998\t1\t-522.746\t0.012\n"))
	}))
	defer server.Close()
	store, err := NewClickHouseStore(server.URL, "", "", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 10, 16, 0, 0, 0, time.UTC)
	binding := Binding{TenantID: "t", SiteID: "s", Aggregation: "LAST", CalculationMethod: "DELTA", SourceDefinition: map[string]any{"counter": map[string]any{"decreaseMode": "RESET"}}}
	input, err := store.ReadPoint(context.Background(), binding, "p", start, start.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	delta, err := counterDelta(binding, input)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(delta-524.01) > 1e-9 {
		t.Fatalf("delta=%v, want the 523.998 kWh of rises plus 0.012 kWh counted after the restart", delta)
	}

	binding.SourceDefinition = map[string]any{}
	if _, err := counterDelta(binding, input); err == nil {
		t.Fatal("a counter that drops without a declared decrease mode must be rejected")
	}
}

func TestReadPointGuardsHourlyAndDailyStepsAgainstCrossGapJumps(t *testing.T) {
	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		queries = append(queries, request.URL.Query().Get("query"))
		_, _ = writer.Write([]byte("10\t2\t2\t5\t10\t300\t0\t5\t0\t0\t0\n"))
	}))
	defer server.Close()
	store, err := NewClickHouseStore(server.URL, "", "", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 11, 12, 6, 0, 0, 0, time.UTC)
	for _, granularity := range []string{"HOUR", "DAY", "MONTH"} {
		if _, err := store.ReadPoint(context.Background(), Binding{
			TenantID: "t", SiteID: "s", Granularity: granularity, Aggregation: "LAST", CalculationMethod: "DELTA",
		}, "p", start, start.Add(time.Hour)); err != nil {
			t.Fatalf("%s read failed: %v", granularity, err)
		}
	}
	if !strings.Contains(queries[0], "gap_ms <= 3600000") || !strings.Contains(queries[1], "gap_ms <= 3600000") {
		t.Fatalf("HOUR/DAY counter queries must exclude >1h cross-gap steps:\nHOUR=%s\nDAY=%s", queries[0], queries[1])
	}
	if strings.Contains(queries[2], "gap_ms <= 3600000") {
		t.Fatalf("MONTH counter query must keep cross-gap steps inside monthly total:\n%s", queries[2])
	}
}

func TestReadPointFiltersGoodFiniteSamplesAndCompleteness(t *testing.T) {
	var seenQueries []string
	responses := []string{
		"12.5\t4\t3\t10\t15\t1800\t6.25\t5\t0\t0\t0\n",
		"0\t3\t0\t\\N\t\\N\t0\t\\N\t0\t0\t0\t0\n",
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		query := request.URL.Query().Get("query")
		seenQueries = append(seenQueries, query)
		idx := len(seenQueries) - 1
		_, _ = writer.Write([]byte(responses[idx]))
	}))
	defer server.Close()

	store, err := NewClickHouseStore(server.URL, "", "", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)

	input, err := store.ReadPoint(context.Background(), Binding{TenantID: "t", SiteID: "s", Aggregation: "AVG"}, "p", start, start.Add(time.Hour))
	if err != nil {
		t.Fatalf("mixed quality ReadPoint failed: %v", err)
	}
	if input.Quality != "PARTIAL" || math.Abs(input.Completeness-0.75) > 1e-9 || input.Count != 4 || input.Value != 12.5 {
		t.Fatalf("unexpected mixed input=%+v", input)
	}
	sql := seenQueries[0]
	for _, marker := range []string{
		"avgIf(value_number, quality = 'GOOD' AND isFinite(value_number))",
		"groupArrayIf((sampled_at, observation_id, assumeNotNull(value_number), toString((point_revision, ifNull(unit, '')))), quality = 'GOOD' AND isFinite(value_number))",
		"argMinIf(value_number,tuple(sampled_at,observation_id),quality = 'GOOD' AND isFinite(value_number)) AS first_value",
		"argMaxIf(value_number,tuple(sampled_at,observation_id),quality = 'GOOD' AND isFinite(value_number)) AS last_value",
		"minIf(sampled_at,quality = 'GOOD' AND isFinite(value_number))",
		"maxIf(sampled_at,quality = 'GOOD' AND isFinite(value_number))",
		"count() AS n",
		"countIf(quality = 'GOOD' AND isFinite(value_number)) AS good",
	} {
		if !strings.Contains(sql, marker) {
			t.Fatalf("ReadPoint SQL missing %q:\n%s", marker, sql)
		}
	}
	anchorSQL := sql[:strings.Index(sql, "AS anchor")]
	if !strings.Contains(anchorSQL, "quality = 'GOOD' AND isFinite(value_number)") {
		t.Fatalf("anchor subquery missing quality filter:\n%s", anchorSQL)
	}

	if _, err := store.ReadPoint(context.Background(), Binding{TenantID: "t", SiteID: "s", Aggregation: "SUM"}, "p", start, start.Add(time.Hour)); err == nil || !strings.Contains(err.Error(), "no numeric observations") {
		t.Fatalf("all-invalid SUM window must reject with no numeric observations, got err=%v", err)
	}
	if !strings.Contains(seenQueries[1], "sumIf(value_number, quality = 'GOOD' AND isFinite(value_number))") {
		t.Fatalf("SUM aggregation missing sumIf filter:\n%s", seenQueries[1])
	}
}

func TestDeleteMetricResultSubmitsAsyncMutation(t *testing.T) {
	var method, query string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		method = request.Method
		query = request.URL.Query().Get("query")
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	store, err := NewClickHouseStore(server.URL, "", "", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteMetricResult(context.Background(), "t1", "s1", "r1"); err != nil {
		t.Fatalf("DeleteMetricResult failed: %v", err)
	}
	if method != http.MethodPost {
		t.Fatalf("expected POST, got %s", method)
	}
	if !strings.HasPrefix(query, "ALTER TABLE analytics.metric_result_facts DELETE WHERE ") {
		t.Fatalf("unexpected delete SQL: %s", query)
	}
	if strings.Contains(strings.ToLower(query), "mutations_sync") {
		t.Fatalf("DeleteMetricResult must not set mutations_sync: %s", query)
	}
}

func TestMetricResultLookupsIncludeLeadingPrimaryKeyPredicates(t *testing.T) {
	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		sql := request.URL.Query().Get("query")
		seen = append(seen, sql)
		if strings.Contains(sql, "SELECT count()") {
			_, _ = io.WriteString(writer, "1\n")
			return
		}
		_, _ = io.WriteString(writer, "42.5\tkWh\tGOOD\t1\t2026-08-10 01:05:00.000\t2\n")
	}))
	defer server.Close()

	store, err := NewClickHouseStore(server.URL, "", "", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	result := Result{
		ResultID:    "01990000-1111-7000-8000-000000000001",
		PeriodStart: time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC),
		PeriodEnd:   time.Date(2026, 8, 10, 1, 0, 0, 0, time.UTC),
		Revision:    2,
		Binding: Binding{
			TenantID:    "01990000-3000-7000-8000-000000000001",
			SiteID:      "01990000-5000-7000-8000-000000000001",
			MetricID:    "01990000-4000-7000-8000-000000000001",
			SubjectType: "SITE",
			SubjectID:   "01990000-5000-7000-8000-000000000001",
			Granularity: "HOUR",
		},
	}
	present, err := store.HasMetricResult(context.Background(), result)
	if err != nil || !present {
		t.Fatalf("HasMetricResult=%t err=%v", present, err)
	}
	loaded, err := store.ReadMetricResult(context.Background(), result)
	if err != nil || loaded.Value != 42.5 {
		t.Fatalf("ReadMetricResult=%#v err=%v", loaded, err)
	}
	if len(seen) != 2 {
		t.Fatalf("expected 2 queries, got %d", len(seen))
	}
	for i, sql := range seen {
		for _, marker := range []string{
			"tenant_id=toUUID('01990000-3000-7000-8000-000000000001')",
			"site_id=toUUID('01990000-5000-7000-8000-000000000001')",
			"metric_id=toUUID('01990000-4000-7000-8000-000000000001')",
			"subject_type='SITE'",
			"subject_id=toUUID('01990000-5000-7000-8000-000000000001')",
			"granularity='HOUR'",
			"period_start=parseDateTime64BestEffort('2026-08-10T00:00:00Z',3,'UTC')",
			"period_end=parseDateTime64BestEffort('2026-08-10T01:00:00Z',3,'UTC')",
			"result_id=toUUID('01990000-1111-7000-8000-000000000001')",
		} {
			if !strings.Contains(sql, marker) {
				t.Fatalf("query %d missing %q:\n%s", i, marker, sql)
			}
		}
	}
}

func TestNewClickHouseStoreInitializesTunedDefaultHTTPTransport(t *testing.T) {
	store, err := NewClickHouseStore("http://127.0.0.1:8123", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if store.client == nil {
		t.Fatal("expected default HTTP client to be initialized")
	}
	if store.client.Timeout != 15*time.Second {
		t.Fatalf("Timeout=%s, want 15s", store.client.Timeout)
	}
	transport, ok := store.client.Transport.(*http.Transport)
	if !ok || transport == nil {
		t.Fatalf("Transport=%T, want *http.Transport", store.client.Transport)
	}
	if transport.MaxIdleConns != 64 ||
		transport.MaxIdleConnsPerHost != 32 ||
		transport.IdleConnTimeout != 90*time.Second ||
		transport.TLSHandshakeTimeout != 10*time.Second ||
		transport.ExpectContinueTimeout != time.Second {
		t.Fatalf("unexpected transport tuning: MaxIdleConns=%d MaxIdleConnsPerHost=%d IdleConnTimeout=%s TLSHandshakeTimeout=%s ExpectContinueTimeout=%s",
			transport.MaxIdleConns, transport.MaxIdleConnsPerHost, transport.IdleConnTimeout, transport.TLSHandshakeTimeout, transport.ExpectContinueTimeout)
	}

	custom := &http.Client{Timeout: 4 * time.Second}
	withCustom, err := NewClickHouseStore("http://127.0.0.1:8123", "", "", custom)
	if err != nil {
		t.Fatal(err)
	}
	if withCustom.client != custom {
		t.Fatal("expected custom HTTP client to be preserved")
	}
}

func TestInsertMetricAndInsertMetricsUseAsyncInsertAndDedupToken(t *testing.T) {
	type capture struct {
		query url.Values
		enc   string
		rows  int
	}
	var mu sync.Mutex
	var captures []capture
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		raw, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		decoded := raw
		if request.Header.Get("Content-Encoding") == "gzip" {
			gz, err := gzip.NewReader(bytes.NewReader(raw))
			if err != nil {
				t.Fatal(err)
			}
			decoded, err = io.ReadAll(gz)
			_ = gz.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
		lines := strings.Split(strings.TrimSpace(string(decoded)), "\n")
		mu.Lock()
		captures = append(captures, capture{
			query: request.URL.Query(),
			enc:   request.Header.Get("Content-Encoding"),
			rows:  len(lines),
		})
		mu.Unlock()
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	store, err := NewClickHouseStore(server.URL, "metric_engine_writer", "[REDACTED_SECRET]", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	makeResult := func(id string, val float64, pad string) Result {
		return Result{
			RunID:        "run-" + id,
			ResultID:     id,
			PeriodStart:  start,
			PeriodEnd:    start.Add(time.Hour),
			CalculatedAt: start.Add(time.Hour),
			Value:        val,
			Quality:      "GOOD",
			Completeness: 1,
			Revision:     1,
			Inputs:       []Input{{Code: "x", Type: "POINT", Reference: pad, Value: val, Quality: "GOOD", Completeness: 1}},
			Binding: Binding{
				TenantID:        "018f4d00-0000-7000-8000-000000000001",
				SiteID:          "018f4e00-1000-7000-8000-000000000001",
				BindingID:       "018f4e00-1200-7000-8000-000000000001",
				BindingVersion:  1,
				MetricID:        "018f4e00-1300-7000-8000-000000000001",
				MetricVersionID: "018f4e00-1400-7000-8000-000000000001",
				MetricCode:      "hourly_energy",
				MetricVersion:   1,
				SubjectType:     "SITE",
				SubjectID:       "018f4e00-1000-7000-8000-000000000001",
				Granularity:     "HOUR",
				Unit:            "kWh",
			},
		}
	}

	r1 := makeResult("018f4e00-2000-7000-8000-000000000001", 10.5, "p1")
	if err := store.InsertMetric(context.Background(), r1); err != nil {
		t.Fatal(err)
	}

	var many []Result
	for i := range 20 {
		many = append(many, makeResult(fmt.Sprintf("018f4e00-2000-7000-8000-%012d", i+1), float64(i+1), strings.Repeat("p", 200)))
	}
	if err := store.InsertMetrics(context.Background(), many); err != nil {
		t.Fatal(err)
	}
	slices.Reverse(many)
	if err := store.InsertMetrics(context.Background(), many); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(captures) != 3 {
		t.Fatalf("captures=%d, want 3", len(captures))
	}
	for i, capItem := range captures {
		if capItem.query.Get("async_insert") != "1" ||
			capItem.query.Get("wait_for_async_insert") != "1" ||
			capItem.query.Get("async_insert_deduplicate") != "1" ||
			len(capItem.query.Get("insert_deduplication_token")) != 64 {
			t.Fatalf("capture[%d] missing async/dedup params: %v", i, capItem.query)
		}
	}
	if captures[0].rows != 1 || captures[0].enc != "" {
		t.Fatalf("single row capture unexpected: rows=%d enc=%q", captures[0].rows, captures[0].enc)
	}
	if captures[1].rows != 20 || captures[1].enc != "gzip" {
		t.Fatalf("batch capture unexpected: rows=%d enc=%q", captures[1].rows, captures[1].enc)
	}
	if captures[1].query.Get("insert_deduplication_token") != captures[2].query.Get("insert_deduplication_token") {
		t.Fatalf("token changed across row reorder: %s != %s",
			captures[1].query.Get("insert_deduplication_token"),
			captures[2].query.Get("insert_deduplication_token"))
	}
	if sha256.Size != 32 {
		t.Fatal("unexpected sha256 size")
	}
}

func TestReadPointEnforcesExplicitUTCAndCounterAnomalyCleaningSQL(t *testing.T) {
	var capturedSQL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedSQL = r.URL.Query().Get("query")
		_, _ = io.WriteString(w, "1002\t3\t3\t1000\t1002\t120\t2\t2\t0\t0\t0\n")
	}))
	defer server.Close()

	store, err := NewClickHouseStore(server.URL, "", "", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	shanghai := time.FixedZone("Asia/Shanghai", 8*3600)
	start := time.Date(2026, 8, 10, 12, 0, 0, 0, shanghai)
	end := start.Add(time.Hour)
	binding := Binding{
		TenantID:          "01990000-3000-7000-8000-000000000001",
		SiteID:            "01990000-5000-7000-8000-000000000001",
		Granularity:       "HOUR",
		Aggregation:       "LAST",
		CalculationMethod: "DELTA",
		SourceDefinition: map[string]any{
			"counter": map[string]any{
				"decreaseMode":    "RESET_TO_ZERO",
				"maxPowerKw":      150.0,
				"jitterTolerance": 0.25,
			},
		},
	}
	in, err := store.ReadPoint(context.Background(), binding, "01990000-4000-7000-8000-000000000001", start, end)
	if err != nil {
		t.Fatalf("ReadPoint failed: %v", err)
	}
	if in.Rise != 2 || in.Drops != 0 {
		t.Fatalf("unexpected ReadPoint output: %+v", in)
	}
	for _, marker := range []string{
		"parseDateTime64BestEffort('2026-08-10T04:00:00Z', 3, 'UTC')",
		"parseDateTime64BestEffort('2026-08-10T05:00:00Z', 3, 'UTC')",
		"arrayFold(",
		"el.1 = 0 AND acc.4 > 0",
		"150",
		"0.25",
		"RESET_TO_ZERO",
		"multiIf(",
	} {
		if !strings.Contains(capturedSQL, marker) {
			t.Fatalf("ReadPoint SQL missing %q:\n%s", marker, capturedSQL)
		}
	}
	if strings.Contains(capturedSQL, "mel.5ltiIf") {
		t.Fatalf("ReadPoint SQL corrupted multiIf identifier:\n%s", capturedSQL)
	}

	// Non-finite values returned by ClickHouse must be rejected rather than propagated.
	nanServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "10\t1\t1\t10\t10\t0\tinf\t0\t0\t0\t0\n")
	}))
	defer nanServer.Close()
	nanStore, err := NewClickHouseStore(nanServer.URL, "", "", nanServer.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := nanStore.ReadPoint(context.Background(), binding, "01990000-4000-7000-8000-000000000001", start, end); err == nil {
		t.Fatal("expected ReadPoint to reject non-finite inf column")
	}
}

func TestReadEnergyWeightedCOPUsesSiteTimezoneAndGuardrails(t *testing.T) {
	var lastSQL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastSQL = r.URL.Query().Get("query")
		_, _ = io.WriteString(w, "480.0\t100.0\t45\t60\n")
	}))
	defer server.Close()

	store, err := NewClickHouseStore(server.URL, "", "", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	binding := Binding{TenantID: "01990000-3000-7000-8000-000000000001", SiteID: "01990000-5000-7000-8000-000000000001"}
	start := time.Date(2026, 8, 10, 4, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	agg, err := store.ReadEnergyWeightedCOP(context.Background(), binding, "01990000-4000-7000-8000-000000000011", "01990000-4000-7000-8000-000000000012", start, end, COPGuardrailOptions{
		SiteTimezone:  "Asia/Shanghai",
		MinPowerKW:    5.0,
		MinDeltaTK:    0.5,
		SupplyPointID: "01990000-4000-7000-8000-000000000013",
		ReturnPointID: "01990000-4000-7000-8000-000000000014",
	})
	if err != nil {
		t.Fatalf("ReadEnergyWeightedCOP failed: %v", err)
	}
	if math.Abs(agg.COP-4.8) > 1e-9 || agg.Quality != "PARTIAL" || math.Abs(agg.Completeness-0.75) > 1e-9 {
		t.Fatalf("unexpected COP aggregate: %+v", agg)
	}
	for _, marker := range []string{
		"toStartOfMinute(toTimeZone(sampled_at, 'Asia/Shanghai'))",
		"parseDateTime64BestEffort('2026-08-10T04:00:00Z', 3, 'UTC')",
		"sumIf(cooling_kw / 60.0",
		"sumIf(power_kw / 60.0",
		"power_kw >= 5",
		"(ret_c - sup_c) >= 0.5",
	} {
		if !strings.Contains(lastSQL, marker) {
			t.Fatalf("COP SQL missing %q:\n%s", marker, lastSQL)
		}
	}

	if _, err := store.ReadEnergyWeightedCOP(context.Background(), binding, "p1", "p2", start, end, COPGuardrailOptions{SiteTimezone: "Invalid/Zone"}); err == nil {
		t.Fatal("expected invalid timezone error")
	}

	// Standby-only / zero eligible buckets must be rejected cleanly.
	standbyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "0\t0\t0\t60\n")
	}))
	defer standbyServer.Close()
	standbyStore, err := NewClickHouseStore(standbyServer.URL, "", "", standbyServer.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := standbyStore.ReadEnergyWeightedCOP(context.Background(), binding, "p1", "p2", start, end, COPGuardrailOptions{}); err == nil {
		t.Fatal("expected standby/zero-eligible COP window to be rejected")
	}
}

func TestClickHouseOptionABoundary1200To1300Attribution(t *testing.T) {
	ddlPath := filepath.Join("..", "..", "..", "..", "infra", "telemetry", "clickhouse", "init", "021-energy-accounting-p0.sql")
	ddlBytes, err := os.ReadFile(ddlPath)
	if err != nil {
		t.Fatalf("read 021-energy-accounting-p0.sql: %v", err)
	}
	ddl := string(ddlBytes)
	for _, marker := range []string{
		"CREATE OR REPLACE VIEW telemetry_history.counter_deltas_from",
		"DEFINER = CURRENT_USER",
		"SQL SECURITY DEFINER",
		"lagInFrame(",
		"INVALID_ZERO_DROP",
		"INVALID_SPIKE",
		"toStartOfHour(addMilliseconds(period_end, -1)) AS hour",
		"toStartOfHour(addMilliseconds(prior.period_end, -1)) AS hour",
		"DROP VIEW IF EXISTS analytics.energy_facts_to_hourly",
		"DROP VIEW IF EXISTS analytics.energy_fact_corrections_to_hourly",
	} {
		if !strings.Contains(ddl, marker) {
			t.Fatalf("021-energy-accounting-p0.sql missing required marker %q", marker)
		}
	}

	backfillPath := filepath.Join("..", "..", "..", "..", "infra", "telemetry", "clickhouse", "ops", "backfill-energy-hourly-rollup.sh")
	backfillBytes, err := os.ReadFile(backfillPath)
	if err != nil {
		t.Fatalf("read backfill-energy-hourly-rollup.sh: %v", err)
	}
	backfill := string(backfillBytes)
	for _, marker := range []string{
		"toStartOfMonth(addMilliseconds(period_end, -1))",
		"toStartOfHour(addMilliseconds(period_end, -1)) AS hour",
		"AND addMilliseconds(period_end, -1) >= toDateTime64('$month 00:00:00', 3, 'UTC')",
		"AND addMilliseconds(period_end, -1) < toDateTime64('$month 00:00:00', 3, 'UTC') + INTERVAL 1 MONTH",
	} {
		if !strings.Contains(backfill, marker) {
			t.Fatalf("backfill-energy-hourly-rollup.sh missing Option A marker %q", marker)
		}
	}

	// Verify exact Option A (period_start, period_end] boundary semantics:
	// (12:00:00.000, 13:00:00.000] attributes to 12:00:00.000 UTC hour, while
	// (13:00:00.000, 13:00:00.001] attributes to 13:00:00.000 UTC hour.
	optionAHour := func(periodEnd time.Time) time.Time {
		return periodEnd.UTC().Add(-time.Millisecond).Truncate(time.Hour)
	}
	twelve := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	thirteen := time.Date(2026, 8, 10, 13, 0, 0, 0, time.UTC)
	if got := optionAHour(thirteen); !got.Equal(twelve) {
		t.Fatalf("Option A hour(13:00:00.000) = %v, want %v", got, twelve)
	}
	if got := optionAHour(thirteen.Add(time.Millisecond)); !got.Equal(thirteen) {
		t.Fatalf("Option A hour(13:00:00.001) = %v, want %v", got, thirteen)
	}
	monthBoundary := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	if got := optionAHour(monthBoundary); got.Month() != time.May || got.Hour() != 23 {
		t.Fatalf("Option A hour(2026-06-01 00:00:00.000) = %v, want 2026-05-31 23:00:00 UTC", got)
	}
}
