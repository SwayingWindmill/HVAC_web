package forecast

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// The writer account may only insert forecasts (least privilege); reading a published forecast
// back with it answered 502 to every latest-forecast request.
func TestSinkReadsWithTheReaderAccountAndWritesWithTheWriter(t *testing.T) {
	var mutex sync.Mutex
	users := map[string]string{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		user, _, _ := request.BasicAuth()
		mutex.Lock()
		users[request.Method] = user
		mutex.Unlock()
		if request.Method == http.MethodGet {
			_, _ = writer.Write([]byte("0\n"))
		}
	}))
	defer server.Close()
	sink, err := NewClickHouseSink(ClickHouseConfig{
		BaseURL: server.URL, Username: "forecast_service_writer", ReaderUsername: "forecast_service_reader", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	request := validRequest()
	points, err := BaselineEngine{}.Generate(t.Context(), request, request.ForecastOrigin)
	if err != nil {
		t.Fatal(err)
	}
	if err := sink.InsertForecastPoints(t.Context(), points); err != nil {
		t.Fatal(err)
	}
	scope := ForecastJobScope{
		TenantID:       request.TenantID,
		SiteID:         request.SiteID,
		Target:         request.Target,
		SubjectType:    request.SubjectType,
		SubjectID:      request.SubjectID,
		ForecastJobID:  request.ForecastJobID,
		ForecastOrigin: request.ForecastOrigin,
	}
	if _, err := sink.HasForecastJob(t.Context(), scope, len(points)); err != nil {
		t.Fatal(err)
	}
	if users[http.MethodPost] != "forecast_service_writer" || users[http.MethodGet] != "forecast_service_reader" {
		t.Fatalf("ClickHouse users by method=%v", users)
	}
}

func TestForecastClickHouseLookupsIncludeLeadingPrimaryKeyPredicates(t *testing.T) {
	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		query := request.URL.Query().Get("query")
		queries = append(queries, query)
		if strings.Contains(query, "SELECT count()") {
			_, _ = io.WriteString(writer, "1\n")
			return
		}
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sink, err := NewClickHouseSink(ClickHouseConfig{
		BaseURL:        server.URL,
		ReaderUsername: "forecast_service_reader",
		HTTPClient:     server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	request := validRequest()
	scope := ForecastJobScope{
		TenantID:       request.TenantID,
		SiteID:         request.SiteID,
		Target:         request.Target,
		SubjectType:    request.SubjectType,
		SubjectID:      request.SubjectID,
		ForecastJobID:  request.ForecastJobID,
		ForecastOrigin: request.ForecastOrigin,
	}
	if _, err := sink.HasForecastJob(t.Context(), scope, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := sink.ReadForecastJob(t.Context(), scope); err != nil {
		t.Fatal(err)
	}
	if len(queries) != 2 {
		t.Fatalf("expected 2 queries, got %d", len(queries))
	}
	for i, sql := range queries {
		for _, marker := range []string{
			"tenant_id=toUUID('" + request.TenantID + "')",
			"site_id=toUUID('" + request.SiteID + "')",
			"target='" + request.Target + "'",
			"subject_type='" + request.SubjectType + "'",
			"subject_id=toUUID('" + request.SubjectID + "')",
			"forecast_origin=parseDateTime64BestEffort('",
			"forecast_job_id=toUUID('" + request.ForecastJobID + "')",
		} {
			if !strings.Contains(sql, marker) {
				t.Fatalf("query %d missing %q:\n%s", i, marker, sql)
			}
		}
	}
}
