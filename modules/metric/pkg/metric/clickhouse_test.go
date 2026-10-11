package metric

import (
	"context"
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
		_, _ = writer.Write([]byte("\\N\t0\t0\t\\N\t\\N\t\\N\t\\N\n"))
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
