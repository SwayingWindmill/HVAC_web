package cube

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/analyticsmodel"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/modules/telemetry/internal/analytics"
)

type fixedTokenFactory struct{}

func (fixedTokenFactory) Token(context.Context, analytics.CallerContext, analyticsmodel.EnergySeriesQuery) (string, error) {
	return "cube-token", nil
}

func TestClientReadsTheSiteTotalFromTheSiteEnergyView(t *testing.T) {
	var captured []loadRequest
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/cubejs-api/v1/load" || request.Method != http.MethodPost {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		if authorization := request.Header.Get("Authorization"); authorization != "cube-token" {
			t.Fatalf("Authorization = %q", authorization)
		}
		if traceparent := request.Header.Get("traceparent"); traceparent == "" {
			t.Fatal("traceparent was not propagated to Cube")
		}
		var decoded loadRequest
		if err := json.NewDecoder(request.Body).Decode(&decoded); err != nil {
			t.Fatal(err)
		}
		captured = append(captured, decoded)
		writer.Header().Set("Content-Type", "application/json")
		if answeredCostQuery(writer, decoded) {
			return
		}
		if len(decoded.Query.Measures) == 2 && decoded.Query.Measures[0] == "site_energy.max_data_watermark" {
			_, _ = writer.Write([]byte(`{"data":[{"site_energy.max_data_watermark":"2026-08-01T00:00:00.000Z","site_energy.max_dataset_revision":"1722470400000"}]}`))
			return
		}
		_, _ = writer.Write([]byte(`{
			"data":[
				{"site_energy.period_end.day":"2026-07-01T00:00:00.000","site_energy.energy_valid_kwh":"123.5","site_energy.valid_count":"10","site_energy.suspect_count":"2","site_energy.invalid_count":"1"},
				{"site_energy.period_end.day":"2026-07-02T00:00:00.000","site_energy.energy_valid_kwh":"98.25","site_energy.valid_count":"8","site_energy.suspect_count":"1","site_energy.invalid_count":"0"}
			]
		}`))
	}))
	defer server.Close()

	client, err := NewClient(Config{
		BaseURL: server.URL, DatasetRevision: "energy-daily:v1", TokenFactory: fixedTokenFactory{}, HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	productQuery := validCubeEnergyQuery()
	telemetry := observability.NewRuntime(observability.RuntimeConfig{Service: "telemetry-query-service-test"})
	defer func() { _ = telemetry.Shutdown(context.Background()) }()
	ctx, span := telemetry.Tracer.Start(context.Background(), "test.energy-series", observability.SpanKindClient, nil)
	defer span.End()
	response, err := client.QueryEnergySeries(ctx, analytics.CallerContext{PrincipalID: "user-1", PolicyRevision: "policy-1"}, productQuery)
	if err != nil {
		t.Fatal(err)
	}
	if len(captured) != 3 {
		t.Fatalf("Cube request count = %d", len(captured))
	}
	seriesQuery := captured[0].Query
	metadataQuery := captured[1].Query
	if len(seriesQuery.Measures) != 4 || seriesQuery.Measures[0] != "site_energy.energy_valid_kwh" {
		t.Fatalf("series measures = %#v", seriesQuery.Measures)
	}
	if len(seriesQuery.TimeDimensions) != 1 || seriesQuery.TimeDimensions[0].Granularity != "day" || seriesQuery.TimeDimensions[0].Dimension != "site_energy.period_end" {
		t.Fatalf("time dimensions = %#v", seriesQuery.TimeDimensions)
	}
	if dateRange := seriesQuery.TimeDimensions[0].DateRange; len(dateRange) != 2 ||
		dateRange[0] != "2026-07-01T00:00:00.000" || dateRange[1] != "2026-07-02T23:59:59.999" {
		t.Fatalf("date range = %#v, want Asia/Shanghai wall time", dateRange)
	}
	// The Tenant is the token's: Cube's row policy applies it and the views do not expose it.
	for _, filter := range seriesQuery.Filters {
		if strings.HasSuffix(filter.Member, ".tenant_id") {
			t.Fatalf("filters name the Tenant: %#v", seriesQuery.Filters)
		}
	}
	if !hasCubeFilter(seriesQuery.Filters, "site_energy.site_id", testCubeSiteID) ||
		!hasCubeFilter(metadataQuery.Filters, "site_energy.site_id", testCubeSiteID) {
		t.Fatalf("filters = %#v", seriesQuery.Filters)
	}
	if response.Devices != nil {
		t.Fatalf("devices = %#v, want none unless a breakdown is asked for", response.Devices)
	}
	if len(metadataQuery.Measures) != 2 || len(metadataQuery.TimeDimensions) != 0 || metadataQuery.Measures[0] != "site_energy.max_data_watermark" {
		t.Fatalf("metadata query = %#v", metadataQuery)
	}
	// The watermark only needs facts from the window's start on; an unbounded query reads the Site's whole history.
	if !hasCubeFilterOperator(metadataQuery.Filters, "site_energy.period_end", "afterOrOnDate", "2026-07-01T00:00:00.000") {
		t.Fatalf("metadata filters = %#v, want period_end from the window's start in Asia/Shanghai wall time", metadataQuery.Filters)
	}
	if len(response.Points) != 2 || response.Points[0].EnergyKWh != 123.5 {
		t.Fatalf("points = %#v", response.Points)
	}
	if !response.Points[0].PeriodStart.Equal(productQuery.From) {
		t.Fatalf("first period start = %s, want %s", response.Points[0].PeriodStart, productQuery.From)
	}
	if response.Points[0].PeriodEnd.Sub(response.Points[0].PeriodStart) != 24*time.Hour {
		t.Fatalf("period = %s to %s", response.Points[0].PeriodStart, response.Points[0].PeriodEnd)
	}
	if response.Metadata.DatasetRevision != "energy-daily:v1:1722470400000" || response.Metadata.Partial || response.Metadata.QualitySummary.Valid != 18 || response.Metadata.QualitySummary.Suspect != 3 {
		t.Fatalf("metadata = %#v", response.Metadata)
	}
	expectedWatermark := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	if response.Metadata.DataWatermark == nil || response.Metadata.AggregateWatermark == nil ||
		!response.Metadata.DataWatermark.Equal(expectedWatermark) || !response.Metadata.AggregateWatermark.Equal(expectedWatermark) ||
		!response.Metadata.DataWatermark.After(productQuery.To) {
		t.Fatalf("metadata watermarks = %#v", response.Metadata)
	}
}

func TestClientBreaksTheSiteDownByMeteredDevice(t *testing.T) {
	const chiller = "018f1e00-4000-7000-8000-000000000011"
	const pump = "018f1e00-4000-7000-8000-000000000012"
	var breakdownQuery *cubeQuery
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var decoded loadRequest
		if err := json.NewDecoder(request.Body).Decode(&decoded); err != nil {
			t.Fatal(err)
		}
		writer.Header().Set("Content-Type", "application/json")
		if answeredCostQuery(writer, decoded) {
			return
		}
		switch {
		case decoded.Query.Measures[0] == "site_energy.max_data_watermark":
			_, _ = writer.Write([]byte(`{"data":[{"site_energy.max_data_watermark":"2026-08-01T00:00:00.000Z","site_energy.max_dataset_revision":"1"}]}`))
		case len(decoded.Query.Dimensions) == 1:
			breakdownQuery = &decoded.Query
			_, _ = writer.Write([]byte(`{"data":[
				{"equipment_energy.equipment_id":"` + pump + `","equipment_energy.period_end.day":"2026-07-01T00:00:00.000","equipment_energy.energy_valid_kwh":"20"},
				{"equipment_energy.equipment_id":"` + chiller + `","equipment_energy.period_end.day":"2026-07-02T00:00:00.000","equipment_energy.energy_valid_kwh":"70"},
				{"equipment_energy.equipment_id":"` + chiller + `","equipment_energy.period_end.day":"2026-07-01T00:00:00.000","equipment_energy.energy_valid_kwh":"80"}
			]}`))
		default:
			_, _ = writer.Write([]byte(`{"data":[
				{"site_energy.period_end.day":"2026-07-01T00:00:00.000","site_energy.energy_valid_kwh":"123.5","site_energy.valid_count":"10","site_energy.suspect_count":"0","site_energy.invalid_count":"0"},
				{"site_energy.period_end.day":"2026-07-02T00:00:00.000","site_energy.energy_valid_kwh":"98.25","site_energy.valid_count":"8","site_energy.suspect_count":"0","site_energy.invalid_count":"0"}
			]}`))
		}
	}))
	defer server.Close()
	client, err := NewClient(Config{BaseURL: server.URL, DatasetRevision: "energy-daily:v1", TokenFactory: fixedTokenFactory{}, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	productQuery := validCubeEnergyQuery()
	productQuery.Breakdown = analyticsmodel.EnergyBreakdownDevice
	response, err := client.QueryEnergySeries(context.Background(), analytics.CallerContext{PrincipalID: "user-1", PolicyRevision: "policy-1"}, productQuery)
	if err != nil {
		t.Fatal(err)
	}
	// Sub-meters come from the equipment view, whose cube holds BRANCH facts only.
	if breakdownQuery == nil || breakdownQuery.Dimensions[0] != "equipment_energy.equipment_id" ||
		!hasCubeFilter(breakdownQuery.Filters, "equipment_energy.site_id", testCubeSiteID) {
		t.Fatalf("breakdown query = %#v", breakdownQuery)
	}
	if len(response.Points) != 2 || response.Points[0].EnergyKWh != 123.5 {
		t.Fatalf("site points = %#v", response.Points)
	}
	if len(response.Devices) != 2 || response.Devices[0].DeviceID != chiller || response.Devices[1].DeviceID != pump {
		t.Fatalf("devices = %#v", response.Devices)
	}
	chillerPoints := response.Devices[0].Points
	if len(chillerPoints) != 2 || chillerPoints[0].EnergyKWh != 80 || chillerPoints[1].EnergyKWh != 70 ||
		!chillerPoints[0].PeriodStart.Equal(productQuery.From) || chillerPoints[0].PeriodEnd.Sub(chillerPoints[0].PeriodStart) != 24*time.Hour {
		t.Fatalf("chiller points = %#v", chillerPoints)
	}
}

func TestClientMarksCoveredRangePartialWhenSeriesRowsAreMissing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var decoded loadRequest
		if err := json.NewDecoder(request.Body).Decode(&decoded); err != nil {
			t.Fatal(err)
		}
		writer.Header().Set("Content-Type", "application/json")
		if answeredCostQuery(writer, decoded) {
			return
		}
		if len(decoded.Query.TimeDimensions) == 0 {
			_, _ = writer.Write([]byte(`{"data":[{"site_energy.max_data_watermark":"2026-08-02T00:00:00.000Z","site_energy.max_dataset_revision":"1722556800000"}]}`))
			return
		}
		_, _ = writer.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()
	client, err := NewClient(Config{
		BaseURL: server.URL, DatasetRevision: "energy-daily:v1", TokenFactory: fixedTokenFactory{}, HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	productQuery := validCubeEnergyQuery()
	response, err := client.QueryEnergySeries(context.Background(), analytics.CallerContext{PrincipalID: "user-1", PolicyRevision: "policy-1"}, productQuery)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Points) != 0 || !response.Metadata.Partial || response.Metadata.DataWatermark == nil || !response.Metadata.DataWatermark.After(productQuery.To) {
		t.Fatalf("response = %#v", response)
	}
}

func TestRequestedBucketCoverageRejectsGap(t *testing.T) {
	query := validCubeEnergyQuery()
	location, err := time.LoadLocation(query.Timezone)
	if err != nil {
		t.Fatal(err)
	}
	points := []analyticsmodel.EnergySeriesPoint{
		{PeriodStart: time.Date(2026, 7, 1, 0, 0, 0, 0, location).UTC()},
		{PeriodStart: time.Date(2026, 7, 3, 0, 0, 0, 0, location).UTC()},
	}
	query.To = time.Date(2026, 7, 4, 0, 0, 0, 0, location).UTC()
	if coversRequestedBuckets(points, query, location) {
		t.Fatal("coversRequestedBuckets() = true with a missing day bucket")
	}
}

func TestCubeQuerySelectsQualitySpecificEnergyMeasure(t *testing.T) {
	productQuery := validCubeEnergyQuery()
	validOnly := buildSeriesQuery(productQuery, time.UTC)
	if validOnly.Measures[0] != "site_energy.energy_valid_kwh" {
		t.Fatalf("valid-only measure = %q", validOnly.Measures[0])
	}
	productQuery.QualityPolicy = analyticsmodel.QualityPolicyValidAndSuspect
	withSuspect := buildSeriesQuery(productQuery, time.UTC)
	if withSuspect.Measures[0] != "site_energy.energy_valid_and_suspect_kwh" {
		t.Fatalf("valid-and-suspect measure = %q", withSuspect.Measures[0])
	}
	for _, filter := range withSuspect.Filters {
		if filter.Member == "site_energy.quality" {
			t.Fatal("query-level quality filter would hide excluded quality counts")
		}
	}
}

func TestClientRequiresCubeOriginWithoutPath(t *testing.T) {
	_, err := NewClient(Config{
		BaseURL: "https://cube.example/cubejs-api", DatasetRevision: "energy-daily:v1", TokenFactory: fixedTokenFactory{},
	})
	if err == nil {
		t.Fatal("NewClient() error = nil")
	}
}

func TestClientRejectsCubeResponseBeyondBudget(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(strings.Repeat("x", int(maximumCubeResponseSize+1))))
	}))
	defer server.Close()
	client, err := NewClient(Config{
		BaseURL: server.URL, DatasetRevision: "energy-daily:v1", TokenFactory: fixedTokenFactory{}, HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.QueryEnergySeries(context.Background(), analytics.CallerContext{PrincipalID: "user-1", PolicyRevision: "policy-1"}, validCubeEnergyQuery())
	if err == nil || !strings.Contains(err.Error(), "response budget") {
		t.Fatalf("QueryEnergySeries() error = %v", err)
	}
}

func TestHMACTokenFactoryBindsSecurityContext(t *testing.T) {
	now := time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
	factory, err := NewHMACTokenFactory([]byte("[REDACTED_SECRET_32_BYTES_LONG_XX]"), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	token, err := factory.Token(context.Background(), analytics.CallerContext{PrincipalID: "user-1", PolicyRevision: "policy-1"}, validCubeEnergyQuery())
	if err != nil {
		t.Fatal(err)
	}
	claims, err := decodeUnsignedClaims(token)
	if err != nil {
		t.Fatal(err)
	}
	if claims["tenantId"] != testCubeTenantID || claims["siteId"] != testCubeSiteID || claims["principalId"] != "user-1" || claims["policyRevision"] != "policy-1" {
		t.Fatalf("claims = %#v", claims)
	}
	siteIDs, ok := claims["siteIds"].([]any)
	if !ok || len(siteIDs) != 1 || siteIDs[0] != testCubeSiteID {
		t.Fatalf("siteIds = %#v", claims["siteIds"])
	}
}

const (
	testCubeTenantID = "018f1d00-0000-7000-8000-000000000001"
	testCubeSiteID   = "018f1e00-1000-7000-8000-000000000001"
)

func validCubeEnergyQuery() analyticsmodel.EnergySeriesQuery {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		panic(err)
	}
	return analyticsmodel.EnergySeriesQuery{
		TenantID:      testCubeTenantID,
		SiteID:        testCubeSiteID,
		EnergyType:    analyticsmodel.EnergyTypeElectricity,
		Granularity:   analyticsmodel.GranularityDay,
		Timezone:      "Asia/Shanghai",
		From:          time.Date(2026, 7, 1, 0, 0, 0, 0, location).UTC(),
		To:            time.Date(2026, 7, 3, 0, 0, 0, 0, location).UTC(),
		QualityPolicy: analyticsmodel.QualityPolicyValidOnly,
	}
}

func hasCubeFilter(filters []cubeFilter, member, value string) bool {
	return hasCubeFilterOperator(filters, member, "equals", value)
}

func hasCubeFilterOperator(filters []cubeFilter, member, operator, value string) bool {
	for _, filter := range filters {
		if filter.Member == member && filter.Operator == operator && len(filter.Values) == 1 && filter.Values[0] == value {
			return true
		}
	}
	return false
}

func decodeUnsignedClaims(token string) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("unexpected JWT part count %d", len(parts))
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, err
	}
	return claims, nil
}

// A bucket whose intervals are all excluded by the quality policy has no energy under it; Cube
// answers null for the measure, and one such hour once failed a whole day's query.
func TestClientLeavesOutBucketsWithNoEnergyUnderThePolicy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var decoded loadRequest
		if err := json.NewDecoder(request.Body).Decode(&decoded); err != nil {
			t.Fatal(err)
		}
		writer.Header().Set("Content-Type", "application/json")
		if answeredCostQuery(writer, decoded) {
			return
		}
		if len(decoded.Query.TimeDimensions) == 0 {
			_, _ = writer.Write([]byte(`{"data":[{"site_energy.max_data_watermark":"2026-07-03T00:00:00.000Z","site_energy.max_dataset_revision":"1"}]}`))
			return
		}
		_, _ = writer.Write([]byte(`{"data":[
			{"site_energy.period_end.day":"2026-07-01T00:00:00.000","site_energy.energy_valid_kwh":"120.5","site_energy.valid_count":"96","site_energy.suspect_count":"0","site_energy.invalid_count":"0"},
			{"site_energy.period_end.day":"2026-07-02T00:00:00.000","site_energy.energy_valid_kwh":null,"site_energy.valid_count":"0","site_energy.suspect_count":"0","site_energy.invalid_count":"4"}
		]}`))
	}))
	defer server.Close()
	client, err := NewClient(Config{
		BaseURL: server.URL, DatasetRevision: "energy-daily:v1", TokenFactory: fixedTokenFactory{}, HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.QueryEnergySeries(context.Background(), analytics.CallerContext{PrincipalID: "user-1", PolicyRevision: "policy-1"}, validCubeEnergyQuery())
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Points) != 1 || response.Points[0].EnergyKWh != 120.5 || response.Metadata.QualitySummary.Invalid != 4 || !response.Metadata.Partial {
		t.Fatalf("response = %#v", response)
	}
}

// answeredCostQuery answers the electricity cost query with no rows, for tests about energy.
func answeredCostQuery(writer http.ResponseWriter, decoded loadRequest) bool {
	if !strings.HasPrefix(decoded.Query.Measures[0], costView+".") {
		return false
	}
	_, _ = writer.Write([]byte(`{"data":[]}`))
	return true
}
