package report

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/analyticsmodel"
	"github.com/quanlaihe/hvac-web/libs/energyseries"
)

// A fake IAM that signs a grant per query and a fake query service that serves it, as the report reads them.
func queryHoursFixture(t *testing.T, siteBound bool) (*QueryHours, *[]analyticsmodel.EnergySeriesQuery) {
	return queryHoursFixtureWithEnrichment(t, siteBound, func(writer http.ResponseWriter) {
		_ = json.NewEncoder(writer).Encode(analyticsmodel.ReportEnrichmentResponse{})
	})
}

func queryHoursFixtureWithEnrichment(t *testing.T, siteBound bool, writeEnrichment func(http.ResponseWriter)) (*QueryHours, *[]analyticsmodel.EnergySeriesQuery) {
	t.Helper()
	asked := []analyticsmodel.EnergySeriesQuery{}
	iam := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/internal/v1/analytics/workload-decision" || request.Header.Get("X-Delegation-Grant") != "" {
			t.Errorf("IAM request = %s, delegation %q", request.URL.Path, request.Header.Get("X-Delegation-Grant"))
		}
		var query analyticsmodel.EnergySeriesQuery
		if err := json.NewDecoder(request.Body).Decode(&query); err != nil {
			t.Fatal(err)
		}
		asked = append(asked, query)
		if !siteBound {
			writer.WriteHeader(http.StatusNotFound)
			_, _ = writer.Write([]byte(`{"code":"IAM_RESOURCE_NOT_FOUND"}`))
			return
		}
		_ = json.NewEncoder(writer).Encode(map[string]string{"delegationGrant": "grant-" + string(query.EnergyType), "expiresAt": "2026-10-01T00:00:30Z"})
	}))
	t.Cleanup(iam.Close)
	hour := func(at string, kwh float64) analyticsmodel.EnergySeriesPoint {
		start, _ := time.Parse(time.RFC3339, at)
		return analyticsmodel.EnergySeriesPoint{PeriodStart: start, PeriodEnd: start.Add(time.Hour), EnergyKWh: kwh}
	}
	query := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body analyticsmodel.EnergySeriesQuery
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		switch request.URL.Path {
		case "/internal/v1/analytics/energy-series":
			if request.Header.Get("X-Delegation-Grant") != "grant-"+string(body.EnergyType) {
				t.Errorf("query request = %s with grant %q for %s", request.URL.Path, request.Header.Get("X-Delegation-Grant"), body.EnergyType)
			}
			points := []analyticsmodel.EnergySeriesPoint{hour("2026-09-01T02:00:00Z", 40), hour("2026-09-01T03:00:00Z", 55.5)}
			points[0].Cost = &fortyKWhCost
			var crossGap *analyticsmodel.EnergyCrossGap
			if body.EnergyType == analyticsmodel.EnergyTypeElectricity {
				crossGap = &analyticsmodel.EnergyCrossGap{EnergyKWh: 80, Cost: &fortyKWhCost}
			} else {
				points = []analyticsmodel.EnergySeriesPoint{hour("2026-09-01T02:00:00Z", 180), hour("2026-09-01T04:00:00Z", 12)}
			}
			_ = json.NewEncoder(writer).Encode(analyticsmodel.EnergySeriesResponse{SchemaVersion: 1, Points: points, CrossGap: crossGap})
		case "/internal/v1/analytics/report-enrichment":
			if request.Header.Get("X-Delegation-Grant") != "grant-"+string(body.EnergyType) {
				t.Errorf("enrichment request = %s with grant %q", request.URL.Path, request.Header.Get("X-Delegation-Grant"))
			}
			writeEnrichment(writer)
		default:
			t.Errorf("unexpected query service path %s", request.URL.Path)
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(query.Close)
	return NewQueryHours(energyseries.New(energyseries.Config{IAMURL: iam.URL, QueryURL: query.URL, IAMClient: iam.Client(), QueryClient: query.Client()})), &asked
}

// The query service's price for the first hour's electricity, carried into the report as it is.
var fortyKWhCost = analyticsmodel.EnergyCost{AmountCNY: 41.2, UnpricedKWh: 0, Periods: []analyticsmodel.PeriodCost{{PeriodCode: "PEAK", EnergyKWh: 40, AmountCNY: 41.2}}}

func TestQueryHoursReadsElectricityAndCoolingAsTheReportsOwnWorkload(t *testing.T) {
	reader, asked := queryHoursFixture(t, true)
	location, _ := time.LoadLocation("Asia/Shanghai")
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, location)
	to := time.Date(2026, 10, 1, 0, 0, 0, 0, location)

	monthEnergy, err := reader.Hours(context.Background(), testTenant, testSite, location, from, to)
	if err != nil {
		t.Fatal(err)
	}
	hours := monthEnergy.Hours
	if monthEnergy.ElectricityCrossGap == nil || monthEnergy.ElectricityCrossGap.EnergyKWh != 80 {
		t.Fatalf("crossGap = %#v", monthEnergy.ElectricityCrossGap)
	}
	// The same bucketing the report used: valid energy by Site-local hour.
	for _, query := range *asked {
		if query.TenantID != testTenant || query.SiteID != testSite || query.Granularity != analyticsmodel.GranularityHour ||
			query.Timezone != "Asia/Shanghai" || query.QualityPolicy != analyticsmodel.QualityPolicyValidOnly ||
			!query.From.Equal(from) || !query.To.Equal(to) || query.Breakdown != "" {
			t.Fatalf("query = %#v", query)
		}
	}
	if len(*asked) != 3 || (*asked)[0].EnergyType != analyticsmodel.EnergyTypeElectricity ||
		(*asked)[1].EnergyType != analyticsmodel.EnergyTypeCooling || (*asked)[2].EnergyType != analyticsmodel.EnergyTypeElectricity {
		t.Fatalf("asked IAM for %#v", *asked)
	}
	at := func(value string) time.Time { parsed, _ := time.Parse(time.RFC3339, value); return parsed }
	want := []Hour{
		{Start: at("2026-09-01T02:00:00Z"), ElectricityKWh: 40, CoolingKWh: 180, HasElectricity: true},
		{Start: at("2026-09-01T03:00:00Z"), ElectricityKWh: 55.5, HasElectricity: true},
		{Start: at("2026-09-01T04:00:00Z"), CoolingKWh: 12},
	}
	if len(hours) != len(want) {
		t.Fatalf("hours = %#v", hours)
	}
	if hours[0].Cost == nil || !reflect.DeepEqual(*hours[0].Cost, fortyKWhCost) || hours[2].Cost != nil {
		t.Fatalf("hour costs = %+v, %+v", hours[0].Cost, hours[2].Cost)
	}
	for index := range want {
		if !hours[index].Start.Equal(want[index].Start) || hours[index].ElectricityKWh != want[index].ElectricityKWh ||
			hours[index].CoolingKWh != want[index].CoolingKWh || hours[index].HasElectricity != want[index].HasElectricity {
			t.Fatalf("hour %d = %#v, want %#v", index, hours[index], want[index])
		}
	}
}

func TestQueryHoursSaysSoWhenTheWorkloadIsNotBoundToTheSite(t *testing.T) {
	reader, _ := queryHoursFixture(t, false)
	location, _ := time.LoadLocation("Asia/Shanghai")
	_, err := reader.Hours(context.Background(), testTenant, testSite, location, time.Date(2026, 9, 1, 0, 0, 0, 0, location), time.Date(2026, 10, 1, 0, 0, 0, 0, location))
	if !errors.Is(err, energyseries.ErrSiteNotBound) {
		t.Fatalf("err = %v, want ErrSiteNotBound", err)
	}
}

func TestQueryHoursMapsEnrichmentAndDegradesOnEnrichmentError(t *testing.T) {
	location, _ := time.LoadLocation("Asia/Shanghai")
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, location)
	to := time.Date(2026, 10, 1, 0, 0, 0, 0, location)

	plantCOP := 4.85
	chillerCOP := 6.4
	chwpWTF := 35.0
	cwpWTF := 42.0
	towerShare := 0.08
	wetBulb := 22.4
	dryBulb := 29.1
	loadRatio := 0.67
	aiShare := 0.78
	balance := analyticsmodel.ComputeMeterBalance(1000, 960, 0.05, false)

	reader, _ := queryHoursFixtureWithEnrichment(t, true, func(writer http.ResponseWriter) {
		_ = json.NewEncoder(writer).Encode(analyticsmodel.ReportEnrichmentResponse{
			LayeredEfficiency: &analyticsmodel.LayeredEfficiency{
				PlantCOP:        &plantCOP,
				ChillerCOP:      &chillerCOP,
				ChilledPumpWTF:  &chwpWTF,
				CoolingPumpWTF:  &cwpWTF,
				TowerPowerShare: &towerShare,
			},
			MeterBalance: &balance,
			ConditionAnchors: &analyticsmodel.ReportConditionAnchorsSummary{
				MeanWetBulbC:      &wetBulb,
				MeanDryBulbC:      &dryBulb,
				MeanLoadRatio:     &loadRatio,
				AIClosedLoopShare: &aiShare,
			},
		})
	})

	energy, err := reader.Hours(context.Background(), testTenant, testSite, location, from, to)
	if err != nil {
		t.Fatalf("Hours failed: %v", err)
	}
	if energy.LayeredEfficiency == nil || energy.LayeredEfficiency.PlantCOP == nil || *energy.LayeredEfficiency.PlantCOP != 4.85 {
		t.Fatalf("LayeredEfficiency = %#v", energy.LayeredEfficiency)
	}
	if energy.MeterBalance == nil || energy.MeterBalance.Status != string(analyticsmodel.MeterBalanceBalanced) || energy.MeterBalance.DiffKWh != 40 {
		t.Fatalf("MeterBalance = %#v", energy.MeterBalance)
	}
	if energy.ConditionAnchors == nil || energy.ConditionAnchors.AIClosedLoopShare == nil || *energy.ConditionAnchors.AIClosedLoopShare != 0.78 {
		t.Fatalf("ConditionAnchors = %#v", energy.ConditionAnchors)
	}

	degradedReader, _ := queryHoursFixtureWithEnrichment(t, true, func(writer http.ResponseWriter) {
		writer.WriteHeader(http.StatusServiceUnavailable)
	})
	degraded, err := degradedReader.Hours(context.Background(), testTenant, testSite, location, from, to)
	if err != nil {
		t.Fatalf("expected enrichment failure to degrade gracefully, got err=%v", err)
	}
	if len(degraded.Hours) != 3 {
		t.Fatalf("expected hourly energy intact on enrichment failure, got %#v", degraded.Hours)
	}
	if degraded.LayeredEfficiency != nil || degraded.MeterBalance != nil || degraded.ConditionAnchors != nil {
		t.Fatalf("expected nil enrichment sections on error, got eff=%#v mb=%#v ca=%#v", degraded.LayeredEfficiency, degraded.MeterBalance, degraded.ConditionAnchors)
	}
}
