package history

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/telemetryhistorymodel"
)

func TestClickHouseHistoryClientQueriesBoundedRealProjection(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("HISTORY_QUERY_CLICKHOUSE_TEST_URL"))
	if baseURL == "" {
		t.Skip("HISTORY_QUERY_CLICKHOUSE_TEST_URL is not configured")
	}
	username := strings.TrimSpace(os.Getenv("HISTORY_QUERY_CLICKHOUSE_TEST_USERNAME"))
	if username == "" {
		username = "telemetry_query_history_reader"
	}
	client, err := NewClient(Config{
		BaseURL: baseURL, Database: "telemetry_history", Table: "observations",
		Username: username, Password: os.Getenv("HISTORY_QUERY_CLICKHOUSE_TEST_PASSWORD"),
		DatasetRevision: "telemetry-history:v1", HTTPClient: &http.Client{Timeout: 15 * time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}
	query := telemetryhistorymodel.DeviceHistoryQuery{
		ActingOrganizationID: "018f4f00-0000-7000-8000-000000000001",
		TenantID:             "018f4f00-0100-7000-8000-000000000001",
		SiteID:               "018f4f00-1000-7000-8000-000000000001",
		DeviceID:             "018f4f00-2000-7000-8000-000000000001",
		Keys:                 []string{"hvac_meter.energy"},
		From:                 time.Date(2026, 7, 29, 12, 50, 0, 0, time.UTC),
		To:                   time.Date(2026, 7, 29, 13, 10, 0, 0, time.UTC),
		MaxPointsPerKey:      2,
	}
	response, err := client.QueryDeviceHistory(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Series) != 1 || response.Series[0].Key != "hvac_meter.energy" || len(response.Series[0].Points) != 2 {
		t.Fatalf("series = %#v", response.Series)
	}
	points := response.Series[0].Points
	if points[0].ObservationID != "018f4f00-3000-7000-8000-000000000002" || points[0].PointID != "018f4f00-2100-7000-8000-000000000001" || points[0].SensorID == nil || *points[0].SensorID != "018f4f00-2200-7000-8000-000000000001" || points[0].Value != 103 || points[0].Quality != telemetryhistorymodel.QualityGood {
		t.Fatalf("first point = %#v", points[0])
	}
	if points[1].ObservationID != "018f4f00-3000-7000-8000-000000000003" || points[1].PointID != "018f4f00-2100-7000-8000-000000000001" || points[1].SensorID == nil || *points[1].SensorID != "018f4f00-2200-7000-8000-000000000001" || points[1].Value != 1 || points[1].Quality != telemetryhistorymodel.QualityGood {
		t.Fatalf("second point = %#v", points[1])
	}
	if !response.Metadata.Partial || response.Metadata.ReturnedPoints != 2 || response.Metadata.DatasetRevision != "telemetry-history:v1:1722258300000" || len(response.Metadata.TruncatedKeys) != 1 || response.Metadata.TruncatedKeys[0] != "hvac_meter.energy" {
		t.Fatalf("metadata = %#v", response.Metadata)
	}
	if response.Metadata.DataWatermark == nil || !response.Metadata.DataWatermark.Equal(time.Date(2026, 7, 29, 13, 5, 0, 0, time.UTC)) {
		t.Fatalf("watermark = %#v", response.Metadata.DataWatermark)
	}
}
