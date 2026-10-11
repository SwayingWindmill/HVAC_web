package forecast

import (
	"net/http"
	"net/http/httptest"
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
	if _, err := sink.HasForecastJob(t.Context(), request.ForecastJobID, len(points)); err != nil {
		t.Fatal(err)
	}
	if users[http.MethodPost] != "forecast_service_writer" || users[http.MethodGet] != "forecast_service_reader" {
		t.Fatalf("ClickHouse users by method=%v", users)
	}
}
