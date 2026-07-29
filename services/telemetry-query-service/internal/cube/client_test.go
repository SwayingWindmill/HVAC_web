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
	"github.com/quanlaihe/hvac-web/services/telemetry-query-service/internal/analytics"
)

type fixedTokenFactory struct{}

func (fixedTokenFactory) Token(context.Context, analytics.CallerContext, analyticsmodel.EnergySeriesQuery) (string, error) {
	return "cube-token", nil
}

func TestClientMapsEnergyProductQueryToFixedCubeMembers(t *testing.T) {
	var captured loadRequest
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
		if err := json.NewDecoder(request.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{
			"data":[
				{"energy_usage.period_start.day":"2026-07-01T00:00:00.000","energy_usage.energy_valid_kwh":"123.5","energy_usage.valid_count":"10","energy_usage.suspect_count":"2","energy_usage.invalid_count":"1"},
				{"energy_usage.period_start.day":"2026-07-02T00:00:00.000","energy_usage.energy_valid_kwh":"98.25","energy_usage.valid_count":"8","energy_usage.suspect_count":"1","energy_usage.invalid_count":"0"}
			],
			"lastRefreshTime":"2026-07-03T00:05:00.000Z"
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
	if len(captured.Query.Measures) != 4 || captured.Query.Measures[0] != "energy_usage.energy_valid_kwh" {
		t.Fatalf("measures = %#v", captured.Query.Measures)
	}
	if len(captured.Query.TimeDimensions) != 1 || captured.Query.TimeDimensions[0].Granularity != "day" {
		t.Fatalf("time dimensions = %#v", captured.Query.TimeDimensions)
	}
	if !hasCubeFilter(captured.Query.Filters, "energy_usage.organization_id", testCubeOrganizationID) ||
		!hasCubeFilter(captured.Query.Filters, "energy_usage.site_id", testCubeSiteID) {
		t.Fatalf("filters = %#v", captured.Query.Filters)
	}
	if len(response.Points) != 2 || response.Points[0].EnergyKWh != 123.5 {
		t.Fatalf("points = %#v", response.Points)
	}
	if response.Points[0].PeriodEnd.Sub(response.Points[0].PeriodStart) != 24*time.Hour {
		t.Fatalf("period = %s to %s", response.Points[0].PeriodStart, response.Points[0].PeriodEnd)
	}
	if response.Metadata.DatasetRevision != "energy-daily:v1" || !response.Metadata.Partial || response.Metadata.QualitySummary.Valid != 18 || response.Metadata.QualitySummary.Suspect != 3 {
		t.Fatalf("metadata = %#v", response.Metadata)
	}
	if response.Metadata.DataWatermark != nil || response.Metadata.AggregateWatermark != nil {
		t.Fatalf("Cube refresh time must not be presented as a telemetry watermark: %#v", response.Metadata)
	}
}

func TestCubeQuerySelectsQualitySpecificEnergyMeasure(t *testing.T) {
	productQuery := validCubeEnergyQuery()
	validOnly := buildCubeQuery(productQuery)
	if validOnly.Measures[0] != "energy_usage.energy_valid_kwh" {
		t.Fatalf("valid-only measure = %q", validOnly.Measures[0])
	}
	productQuery.QualityPolicy = analyticsmodel.QualityPolicyValidAndSuspect
	withSuspect := buildCubeQuery(productQuery)
	if withSuspect.Measures[0] != "energy_usage.energy_valid_and_suspect_kwh" {
		t.Fatalf("valid-and-suspect measure = %q", withSuspect.Measures[0])
	}
	for _, filter := range withSuspect.Filters {
		if filter.Member == "energy_usage.quality" {
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
	if claims["organizationId"] != testCubeOrganizationID || claims["siteId"] != testCubeSiteID || claims["principalId"] != "user-1" || claims["policyRevision"] != "policy-1" {
		t.Fatalf("claims = %#v", claims)
	}
	siteIDs, ok := claims["siteIds"].([]any)
	if !ok || len(siteIDs) != 1 || siteIDs[0] != testCubeSiteID {
		t.Fatalf("siteIds = %#v", claims["siteIds"])
	}
}

const (
	testCubeOrganizationID = "018f1e00-0000-7000-8000-000000000001"
	testCubeSiteID         = "018f1e00-1000-7000-8000-000000000001"
)

func validCubeEnergyQuery() analyticsmodel.EnergySeriesQuery {
	return analyticsmodel.EnergySeriesQuery{
		OrganizationID: testCubeOrganizationID,
		SiteID:         testCubeSiteID,
		EnergyType:     analyticsmodel.EnergyTypeElectricity,
		Granularity:    analyticsmodel.GranularityDay,
		Timezone:       "Asia/Shanghai",
		From:           time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		To:             time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC),
		QualityPolicy:  analyticsmodel.QualityPolicyValidOnly,
	}
}

func hasCubeFilter(filters []cubeFilter, member, value string) bool {
	for _, filter := range filters {
		if filter.Member == member && len(filter.Values) == 1 && filter.Values[0] == value {
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
