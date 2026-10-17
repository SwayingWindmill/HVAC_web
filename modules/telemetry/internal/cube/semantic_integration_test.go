package cube

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/analyticsmodel"
	"github.com/quanlaihe/hvac-web/modules/telemetry/internal/analytics"
)

// One question, several measures of a view: run-analytics-cube-tests.mjs has projected the Site's
// electricity (2 kWh in each of the 12:00 and 13:00 buckets) and cooling at four times it, so each
// hour reads 2 kWh, 8 kWh and a COP of 4. The Site is the query's, whatever the token allows.
func TestCubeAnswersAMultiMeasureSemanticQueryForTheQueriedSite(t *testing.T) {
	cubeURL := strings.TrimSpace(os.Getenv("ANALYTICS_CUBE_TEST_URL"))
	if cubeURL == "" {
		t.Skip("ANALYTICS_CUBE_TEST_URL is not configured")
	}
	const tenantID, siteID = "018f4f00-0100-7000-8000-000000000001", "018f4f00-1000-7000-8000-000000000001"
	factory, err := NewHMACTokenFactory([]byte(os.Getenv("ANALYTICS_CUBE_TEST_KEY")), nil)
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(Config{BaseURL: cubeURL, DatasetRevision: "integration", TokenFactory: factory})
	if err != nil {
		t.Fatal(err)
	}
	caller := analytics.CallerContext{PrincipalID: "analytics-integration-user", PolicyRevision: "analytics-integration:1"}
	query := analyticsmodel.SemanticQuery{
		TenantID: tenantID, SiteID: siteID, View: "site_energy",
		Measures:    []string{"electricity_valid_kwh", "cooling_valid_kwh", "cop"},
		Granularity: analyticsmodel.GranularityHour, Timezone: "UTC",
		From: time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC), To: time.Date(2026, 7, 29, 14, 0, 0, 0, time.UTC),
	}

	response, err := client.QuerySemantic(context.Background(), caller, query)
	if err != nil {
		t.Fatal(err)
	}

	wantColumns := []analyticsmodel.SemanticColumn{
		{Name: "period_end", Title: "时段", Kind: analyticsmodel.SemanticColumnTime},
		{Name: "electricity_valid_kwh", Title: "用电量", Kind: analyticsmodel.SemanticColumnMeasure},
		{Name: "cooling_valid_kwh", Title: "供冷量", Kind: analyticsmodel.SemanticColumnMeasure},
		{Name: "cop", Title: "综合能效", Kind: analyticsmodel.SemanticColumnMeasure},
	}
	wantRows := [][]any{
		{"2026-07-29T12:00:00Z", 2.0, 8.0, 4.0},
		{"2026-07-29T13:00:00Z", 2.0, 8.0, 4.0},
	}
	if !reflect.DeepEqual(response.Columns, wantColumns) || !reflect.DeepEqual(response.Rows, wantRows) {
		t.Fatalf("columns = %+v\nrows = %+v", response.Columns, response.Rows)
	}
	if response.Metadata.DataWatermark == nil || response.Metadata.DatasetRevision == "" {
		t.Fatalf("metadata = %+v", response.Metadata)
	}

	other := query
	other.SiteID = "018f4f00-1000-7000-8000-000000000099"
	elsewhere, err := client.QuerySemantic(context.Background(), caller, other)
	if err != nil {
		t.Fatal(err)
	}
	if len(elsewhere.Rows) != 0 {
		t.Fatalf("another Site's query read %+v", elsewhere.Rows)
	}
}
