package optimization

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
