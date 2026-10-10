package telemetry

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Measures transport overhead at a controlled 1 ms request latency, not database capacity.
func BenchmarkClickHouseHistoryTransport(b *testing.B) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		requests.Add(1)
		time.Sleep(time.Millisecond)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	sink, err := NewClickHouseHistorySink(ClickHouseHistoryConfig{BaseURL: server.URL, Database: "telemetry_history", Table: "observations", HTTPClient: server.Client()})
	if err != nil {
		b.Fatal(err)
	}
	rows := make([]HistoryObservation, 256)
	for i := range rows {
		rows[i] = HistoryObservation{ObservationID: fmt.Sprintf("018f2e00-9100-7000-8000-%012d", i), PayloadSHA256: strings.Repeat("a", 64)}
	}
	for b.Loop() {
		if err := sink.InsertObservations(b.Context(), 1, rows); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(requests.Load())/float64(b.N), "requests/batch")
}
