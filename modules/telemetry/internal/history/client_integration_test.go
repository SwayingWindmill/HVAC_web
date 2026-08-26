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

func TestClickHouseHistoryClientQueriesTypedProjection(t *testing.T) {
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
		Username: username, Password: os.Getenv("HISTORY_QUERY_CLICKHOUSE_TEST_PASSWORD"), HTTPClient: &http.Client{Timeout: 15 * time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}
	query := telemetryhistorymodel.DeviceHistoryQuery{
		TenantID: "018f4f00-0100-7000-8000-000000000001",
		SiteID:   "018f4f00-1000-7000-8000-000000000001",
		DeviceID: "018f4f00-2000-7000-8000-000000000001",
		Keys:     []string{"hvac_meter.energy"},
		From:     time.Date(2026, 7, 29, 12, 50, 0, 0, time.UTC),
		To:       time.Date(2026, 7, 29, 13, 10, 0, 0, time.UTC),
		PageSize: 2,
	}
	response, err := client.QueryDeviceHistory(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	if err := response.ValidateFor(query); err != nil {
		t.Fatal(err)
	}
	for _, observation := range response.Observations {
		if observation.TelemetryKey != "hvac_meter.energy" || observation.PointRevision < 1 || !observation.Acceptance.Valid() || !observation.ValueType.Valid() {
			t.Fatalf("observation=%#v", observation)
		}
	}
}
