package metric

import (
	"context"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
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
