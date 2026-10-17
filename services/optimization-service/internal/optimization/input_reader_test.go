package optimization

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/analyticsmodel"
)

// The baseline's price is the latest Site-local day that has ended, whose energy has all arrived
// and every kWh of which was priced: a day with energy no Tariff period covers would understate it,
// and a day priced before all its energy arrived would price only part of its mix of hours.
func TestOptimizationInputReaderPricesTheLatestFullyPricedDay(t *testing.T) {
	shanghai, _ := time.LoadLocation("Asia/Shanghai")
	at := time.Date(2026, 10, 17, 15, 0, 0, 0, shanghai)
	day := func(date int, energy float64, cost *analyticsmodel.EnergyCost) analyticsmodel.EnergySeriesPoint {
		start := time.Date(2026, 10, date, 0, 0, 0, 0, shanghai)
		return analyticsmodel.EnergySeriesPoint{PeriodStart: start.UTC(), PeriodEnd: start.AddDate(0, 0, 1).UTC(), EnergyKWh: energy, Cost: cost}
	}
	priced := func(amount float64) *analyticsmodel.EnergyCost {
		return &analyticsmodel.EnergyCost{AmountCNY: amount, Periods: []analyticsmodel.PeriodCost{}}
	}
	// The data reaches 10-16 18:00, so 10-16 is not complete; 10-14 has energy no period covers.
	watermark := time.Date(2026, 10, 16, 18, 0, 0, 0, shanghai).UTC()
	series := &energySeriesStub{response: analyticsmodel.EnergySeriesResponse{SchemaVersion: 1, Points: []analyticsmodel.EnergySeriesPoint{
		day(13, 2600, priced(1980)),
		day(14, 2500, &analyticsmodel.EnergyCost{AmountCNY: 1700, UnpricedKWh: 120, Periods: []analyticsmodel.PeriodCost{}}),
		day(15, 2400, priced(1820.5)),
		day(16, 1800, priced(1300)),
	}, Metadata: analyticsmodel.EnergySeriesMetadata{DatasetRevision: "local-development-v1:42", DataWatermark: &watermark}}}
	reader := &ClickHouseInputReader{energy: series}

	evidence, err := reader.readPricedDay(t.Context(), OptimizationStateQuery{
		TenantID: "01990000-3000-7000-8000-000000000001", SiteID: "01990000-5000-7000-8000-000000000001", Timezone: "Asia/Shanghai", At: at.UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := EnergyDayEvidence{
		PeriodStart: time.Date(2026, 10, 15, 0, 0, 0, 0, shanghai).UTC(), PeriodEnd: time.Date(2026, 10, 16, 0, 0, 0, 0, shanghai).UTC(),
		EnergyKWh: 2400, CostCNY: 1820.5, DatasetRevision: "local-development-v1:42", DataWatermark: watermark,
	}
	if evidence != want {
		t.Fatalf("evidence = %#v, want %#v", evidence, want)
	}
	// The Site's last seven completed days, by Site-local day, valid data only, electricity.
	asked := series.asked
	if asked.EnergyType != analyticsmodel.EnergyTypeElectricity || asked.Granularity != analyticsmodel.GranularityDay || asked.Timezone != "Asia/Shanghai" ||
		asked.QualityPolicy != analyticsmodel.QualityPolicyValidOnly || !asked.To.Equal(time.Date(2026, 10, 17, 0, 0, 0, 0, shanghai)) ||
		!asked.From.Equal(time.Date(2026, 10, 10, 0, 0, 0, 0, shanghai)) {
		t.Fatalf("asked = %#v", asked)
	}
}

type energySeriesStub struct {
	asked    analyticsmodel.EnergySeriesQuery
	response analyticsmodel.EnergySeriesResponse
}

func (stub *energySeriesStub) Series(_ context.Context, query analyticsmodel.EnergySeriesQuery) (analyticsmodel.EnergySeriesResponse, error) {
	stub.asked = query
	return stub.response, nil
}

func TestOptimizationTelemetryReaderDoesNotFallbackToOlderGoodQuality(t *testing.T) {
	at := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		query, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		if strings.Contains(string(query), "quality='GOOD'") || strings.Contains(string(query), "quality = 'GOOD'") {
			t.Error("authoritative input query must not skip the latest non-GOOD observation")
		}
		writer.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = writer.Write([]byte(`{"observation_id":"01990000-2940-7000-8000-000000000001","device_id":"01990000-2900-7000-8000-000000000001","point_id":"01990000-2910-7000-8000-000000000001","telemetry_key":"btu_meter.supply_water_temperature","point_revision":3,"sampled_at":"2026-08-28T11:59:40.000Z","received_at":"2026-08-28T11:59:41.000Z","value":7,"unit":"Cel","quality":"STALE","source_event_id":"01990000-2930-7000-8000-000000000001","source_partition":"optimization-owner","source_offset":2}` + "\n"))
	}))
	defer server.Close()

	reader, err := NewClickHouseInputReader(InputReaderConfig{BaseURL: server.URL, Energy: &energySeriesStub{}})
	if err != nil {
		t.Fatal(err)
	}
	observation, err := reader.readTelemetry(t.Context(), OptimizationStateQuery{
		TenantID:    "018f1d00-0000-7000-8000-000000000001",
		SiteID:      "018f1e00-1000-7000-8000-000000000001",
		SubjectType: "SITE",
		SubjectID:   "018f1e00-1000-7000-8000-000000000001",
		At:          at,
	}, "btu_meter.supply_water_temperature")
	if err != nil {
		t.Fatal(err)
	}
	if observation.Quality != "STALE" {
		t.Fatalf("quality=%q", observation.Quality)
	}
	if err := validateTelemetryEvidence(observation, "btu_meter.supply_water_temperature", at); err == nil {
		t.Fatal("latest non-GOOD authoritative observation must block Optimization preparation")
	}
}
