package forecast

import (
	"os"
	"testing"
	"time"
)

func TestClickHouseForecastSinkPersistsTraceableBaseline(t *testing.T) {
	baseURL := os.Getenv("FORECAST_CLICKHOUSE_TEST_URL")
	if baseURL == "" {
		t.Skip("FORECAST_CLICKHOUSE_TEST_URL is not set")
	}
	sink, err := NewClickHouseSink(ClickHouseConfig{
		BaseURL:  baseURL,
		Database: "analytics",
		Table:    "forecast_series",
		Username: "forecast_service_writer",
	})
	if err != nil {
		t.Fatal(err)
	}
	generatedAt := time.Date(2026, 8, 13, 0, 1, 0, 0, time.UTC)
	service, err := NewService(sink, &memoryPublicationStore{}, func() time.Time { return generatedAt })
	if err != nil {
		t.Fatal(err)
	}
	request := validRequest()
	request.HorizonMinutes = 60
	points, err := service.Forecast(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 4 {
		t.Fatalf("points=%d", len(points))
	}
	if points[0].Quality != "VALID" || points[0].LowerBound == nil || points[0].UpperBound == nil || points[3].HorizonMinutes != 60 {
		t.Fatalf("points=%#v", points)
	}
}
