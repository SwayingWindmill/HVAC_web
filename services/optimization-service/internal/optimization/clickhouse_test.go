package optimization

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestOptimizationClickHouseHasEvaluationIncludesLeadingPrimaryKeyPredicates(t *testing.T) {
	var seenQuery string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		seenQuery = request.URL.Query().Get("query")
		_, _ = io.WriteString(writer, "1\n")
	}))
	defer server.Close()

	sink, err := NewClickHouseSink(server.URL, "optimization_service_reader", "", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	eval := Evaluation{
		EvaluationID:      "01990000-1980-7000-8000-000000000101",
		TenantID:          "01990000-3000-7000-8000-000000000001",
		SiteID:            "01990000-5000-7000-8000-000000000001",
		OptimizationRunID: "01990000-1950-7000-8000-000000000001",
	}
	present, err := sink.HasEvaluation(t.Context(), eval)
	if err != nil || !present {
		t.Fatalf("HasEvaluation=%t err=%v", present, err)
	}
	for _, marker := range []string{
		"tenant_id=toUUID('" + eval.TenantID + "')",
		"site_id=toUUID('" + eval.SiteID + "')",
		"optimization_run_id=toUUID('" + eval.OptimizationRunID + "')",
		"evaluation_id=toUUID('" + eval.EvaluationID + "')",
	} {
		if !strings.Contains(seenQuery, marker) {
			t.Fatalf("HasEvaluation query missing %q:\n%s", marker, seenQuery)
		}
	}
}

func TestOptimizationClickHouseDeterministicDeduplicationAndPooledTransport(t *testing.T) {
	defaultSink, err := NewClickHouseSink("http://127.0.0.1:8123", "optimization_service_writer", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	transport, ok := defaultSink.client.Transport.(*http.Transport)
	if !ok || transport == nil || transport.MaxIdleConnsPerHost != 32 || transport.MaxIdleConns != 64 {
		t.Fatalf("expected pooled default transport, got %#v", defaultSink.client.Transport)
	}

	var queries []url.Values
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		queries = append(queries, request.URL.Query())
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sink, err := NewClickHouseSink(server.URL, "optimization_service_writer", "", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	eval := Evaluation{
		EvaluationID:      "01990000-1980-7000-8000-000000000101",
		TenantID:          "01990000-3000-7000-8000-000000000001",
		SiteID:            "01990000-5000-7000-8000-000000000001",
		OptimizationRunID: "01990000-1950-7000-8000-000000000001",
		RecommendationID:  "01990000-1990-7000-8000-000000000101",
		GeneratedAt:       time.Date(2026, 8, 20, 4, 0, 0, 0, time.UTC),
	}
	if err := sink.InsertEvaluation(t.Context(), eval); err != nil {
		t.Fatal(err)
	}
	plus8 := time.FixedZone("UTC+8", 8*3600)
	eval.GeneratedAt = eval.GeneratedAt.In(plus8)
	if err := sink.InsertEvaluation(t.Context(), eval); err != nil {
		t.Fatal(err)
	}
	if len(queries) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(queries))
	}
	tok0 := queries[0].Get("insert_deduplication_token")
	tok1 := queries[1].Get("insert_deduplication_token")
	if len(tok0) != 64 || tok0 != tok1 {
		t.Fatalf("expected matching 64-hex dedup tokens, got %q vs %q", tok0, tok1)
	}
	if queries[0].Get("async_insert_deduplicate") != "1" || queries[0].Get("insert_deduplicate") != "1" {
		t.Fatalf("missing dedup query settings: %v", queries[0])
	}
}
