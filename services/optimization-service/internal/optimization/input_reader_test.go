package optimization

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/analyticsmodel"
	"github.com/quanlaihe/hvac-web/libs/planttwin"
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
	}, "btu_meter.supply_water_temperature", ReasonSupplyTempMissing)
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

// 松雅轩's policy once mapped the zone to "temperature", a key the Site does not report: the reader
// says the zone temperature is what is missing, not that some input is.
func TestOptimizationInputReaderNamesTheTelemetryKeyWithNoObservation(t *testing.T) {
	shanghai, _ := time.LoadLocation("Asia/Shanghai")
	at := time.Date(2026, 10, 30, 20, 20, 0, 0, shanghai)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		query, _ := io.ReadAll(request.Body)
		writer.Header().Set("Content-Type", "application/x-ndjson")
		if strings.Contains(string(query), "telemetry_key='supply_water_temperature'") {
			_, _ = writer.Write([]byte(`{"observation_id":"01990000-2940-7000-8000-000000000001","device_id":"01990000-2900-7000-8000-000000000001","point_id":"01990000-2910-7000-8000-000000000001","telemetry_key":"supply_water_temperature","point_revision":1,"sampled_at":"2026-10-30T12:19:00.000Z","received_at":"2026-10-30T12:19:01.000Z","value":6.1,"unit":"Cel","quality":"GOOD","source_event_id":"01990000-2930-7000-8000-000000000001","source_partition":"telemetry-0","source_offset":2}` + "\n"))
		}
	}))
	defer server.Close()
	day := time.Date(2026, 10, 29, 0, 0, 0, 0, shanghai)
	watermark := at.Add(-time.Hour).UTC()
	energy := &energySeriesStub{response: analyticsmodel.EnergySeriesResponse{SchemaVersion: 1, Points: []analyticsmodel.EnergySeriesPoint{{
		PeriodStart: day.UTC(), PeriodEnd: day.AddDate(0, 0, 1).UTC(), EnergyKWh: 1118, Cost: &analyticsmodel.EnergyCost{AmountCNY: 690, Periods: []analyticsmodel.PeriodCost{}},
	}}, Metadata: analyticsmodel.EnergySeriesMetadata{DatasetRevision: "songyaxuan:1", DataWatermark: &watermark}}}
	reader, err := NewClickHouseInputReader(InputReaderConfig{BaseURL: server.URL, Energy: energy})
	if err != nil {
		t.Fatal(err)
	}
	_, err = reader.ReadOptimizationState(t.Context(), OptimizationStateQuery{
		TenantID: "01990000-3000-7000-8000-000000000001", SiteID: "01990000-5000-7000-8000-000000000001",
		SubjectType: "SITE", SubjectID: "01990000-5000-7000-8000-000000000001", Timezone: "Asia/Shanghai", At: at.UTC(),
		SupplyTemperatureKey: "supply_water_temperature", ZoneTemperatureKey: "temperature",
	})
	if reason, ok := PreparationReasonOf(err); !ok || reason != ReasonZoneTempMissing {
		t.Fatalf("reason=%q ok=%v err=%v", reason, ok, err)
	}
}

func TestClickHouseInputReaderReadsShadowEvaluationForPublishedRun(t *testing.T) {
	var seenQuery string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		seenQuery = string(body)
		writer.Header().Set("Content-Type", "application/x-ndjson")
		if strings.Contains(seenQuery, "01990000-1950-7000-8000-000000000101") {
			_, _ = writer.Write([]byte(`{"evaluationId":"01990000-1980-7000-8000-000000000101","optimizationRunId":"01990000-1950-7000-8000-000000000101","recommendationId":"01990000-1990-7000-8000-000000000101","generatedAt":"2026-08-28T15:00:00.000Z","evaluationJSON":"{\"solver\":\"CHILLER_COP_MODEL\",\"mode\":\"SHADOW\",\"outcome\":\"NO_USABLE_MODEL\"}"}` + "\n"))
		}
	}))
	defer server.Close()

	reader, err := NewClickHouseInputReader(InputReaderConfig{BaseURL: server.URL, Energy: &energySeriesStub{}})
	if err != nil {
		t.Fatal(err)
	}
	record, err := reader.ReadShadowEvaluation(t.Context(), ShadowEvaluationQuery{
		TenantID:          "01990000-3000-7000-8000-000000000001",
		SiteID:            "01990000-5000-7000-8000-000000000001",
		OptimizationRunID: "01990000-1950-7000-8000-000000000101",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(seenQuery, "quality = 'SHADOW'") || record.EvaluationID != "01990000-1980-7000-8000-000000000101" {
		t.Fatalf("seenQuery=%s record=%#v", seenQuery, record)
	}
	_, err = reader.ReadShadowEvaluation(t.Context(), ShadowEvaluationQuery{
		TenantID:          "01990000-3000-7000-8000-000000000001",
		SiteID:            "01990000-5000-7000-8000-000000000001",
		OptimizationRunID: "01990000-1950-7000-8000-000000000999",
	})
	if !errors.Is(err, ErrShadowEvaluationNotFound) {
		t.Fatalf("missing shadow err=%v, want ErrShadowEvaluationNotFound", err)
	}
}

func TestOptimizationInputReaderEnforcesMaxAgeMultiZoneAndHealthGates(t *testing.T) {
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 30, 20, 20, 0, 0, shanghai)
	day := time.Date(2026, 10, 29, 0, 0, 0, 0, shanghai)
	watermark := at.Add(-time.Hour).UTC()
	energy := &energySeriesStub{response: analyticsmodel.EnergySeriesResponse{SchemaVersion: 1, Points: []analyticsmodel.EnergySeriesPoint{{
		PeriodStart: day.UTC(), PeriodEnd: day.AddDate(0, 0, 1).UTC(), EnergyKWh: 1118, Cost: &analyticsmodel.EnergyCost{AmountCNY: 690, Periods: []analyticsmodel.PeriodCost{}},
	}}, Metadata: analyticsmodel.EnergySeriesMetadata{DatasetRevision: "songyaxuan:1", DataWatermark: &watermark}}}

	baseObs := func(pointID, key string, sampledAt time.Time, value float64, reasons string) string {
		return `{"observation_id":"01990000-2940-7000-8000-000000000001","device_id":"01990000-2900-7000-8000-000000000001","point_id":"` + pointID + `","telemetry_key":"` + key + `","point_revision":1,"sampled_at":"` + sampledAt.UTC().Format("2006-01-02T15:04:05.000Z") + `","received_at":"` + at.Add(-5*time.Second).UTC().Format("2006-01-02T15:04:05.000Z") + `","value":` + strconv.FormatFloat(value, 'f', -1, 64) + `,"unit":"Cel","quality":"GOOD","quality_reasons":` + reasons + `,"source_event_id":"01990000-2930-7000-8000-000000000001","source_partition":"telemetry-0","source_offset":2}` + "\n"
	}

	t.Run("rejects_days_old_good_reading", func(t *testing.T) {
		var seenSQL string
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Error(err)
				writer.WriteHeader(http.StatusInternalServerError)
				return
			}
			seenSQL = string(body)
			writer.Header().Set("Content-Type", "application/x-ndjson")
			if _, err := writer.Write([]byte(baseObs("01990000-2910-7000-8000-000000000001", "supply_water_temperature", at.Add(-48*time.Hour), 7.0, "[]"))); err != nil {
				t.Error(err)
			}
		}))
		defer server.Close()
		reader, err := NewClickHouseInputReader(InputReaderConfig{BaseURL: server.URL, Energy: energy})
		if err != nil {
			t.Fatal(err)
		}
		_, err = reader.ReadOptimizationState(t.Context(), OptimizationStateQuery{
			TenantID: "01990000-3000-7000-8000-000000000001", SiteID: "01990000-5000-7000-8000-000000000001",
			SubjectType: "SITE", SubjectID: "01990000-5000-7000-8000-000000000001", Timezone: "Asia/Shanghai", At: at.UTC(),
			SupplyTemperatureKey: "supply_water_temperature", ZoneTemperatureKey: "zone.t53",
		})
		if reason, ok := PreparationReasonOf(err); !ok || reason != ReasonSupplyTempMissing {
			t.Fatalf("expected SUPPLY_TEMPERATURE_MISSING for days-old GOOD reading, got reason=%q ok=%v err=%v", reason, ok, err)
		}
		if !strings.Contains(seenSQL, "observation.sampled_at >=") {
			t.Fatalf("expected SQL staleness lower bound on sampled_at, got: %s", seenSQL)
		}
	})

	t.Run("multi_zone_selects_max_and_fails_closed_when_any_zone_missing_or_unhealthy", func(t *testing.T) {
		t54Sampled := at.Add(-2 * time.Minute)
		t54Reasons := "[]"
		includeT54 := true
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			query, err := io.ReadAll(request.Body)
			if err != nil {
				t.Error(err)
				writer.WriteHeader(http.StatusInternalServerError)
				return
			}
			sql := string(query)
			writer.Header().Set("Content-Type", "application/x-ndjson")
			switch {
			case strings.Contains(sql, "telemetry_key='supply_water_temperature'"):
				if _, err := writer.Write([]byte(baseObs("01990000-2910-7000-8000-000000000001", "supply_water_temperature", at.Add(-1*time.Minute), 7.0, "[]"))); err != nil {
					t.Error(err)
				}
			case strings.Contains(sql, "telemetry_key='zone.t53'"):
				if _, err := writer.Write([]byte(baseObs("01990000-2910-7000-8000-000000000002", "zone.t53", at.Add(-1*time.Minute), 22.0, "[]"))); err != nil {
					t.Error(err)
				}
			case strings.Contains(sql, "telemetry_key='zone.t54'") && includeT54:
				if _, err := writer.Write([]byte(baseObs("01990000-2910-7000-8000-000000000003", "zone.t54", t54Sampled, 24.8, t54Reasons))); err != nil {
					t.Error(err)
				}
			}
		}))
		defer server.Close()
		reader, err := NewClickHouseInputReader(InputReaderConfig{BaseURL: server.URL, Energy: energy})
		if err != nil {
			t.Fatal(err)
		}
		query := OptimizationStateQuery{
			TenantID: "01990000-3000-7000-8000-000000000001", SiteID: "01990000-5000-7000-8000-000000000001",
			SubjectType: "SITE", SubjectID: "01990000-5000-7000-8000-000000000001", Timezone: "Asia/Shanghai", At: at.UTC(),
			SupplyTemperatureKey: "supply_water_temperature", ZoneTemperatureKey: "zone.t53",
			ZoneTemperatureKeys: []string{"zone.t53", "zone.t54"},
		}
		state, err := reader.ReadOptimizationState(t.Context(), query)
		if err != nil {
			t.Fatal(err)
		}
		if state.ZoneTemperature.Value != 24.8 || state.ZoneTemperature.TelemetryKey != "zone.t54" || len(state.ZoneTemperatures) != 2 {
			t.Fatalf("expected worst-case max zone t54=24.8 across 2 zones, got %+v", state)
		}

		t54Reasons = `["PROBE_OFFSET"]`
		_, err = reader.ReadOptimizationState(t.Context(), query)
		if reason, ok := PreparationReasonOf(err); !ok || reason != ReasonInputsInvalid {
			t.Fatalf("expected INPUTS_INVALID on PROBE_OFFSET, got reason=%q ok=%v err=%v", reason, ok, err)
		}
		t54Reasons = "[]"

		includeT54 = false
		_, err = reader.ReadOptimizationState(t.Context(), query)
		if reason, ok := PreparationReasonOf(err); !ok || reason != ReasonZoneTempMissing {
			t.Fatalf("expected ZONE_TEMPERATURE_MISSING when t54 missing, got reason=%q ok=%v err=%v", reason, ok, err)
		}
	})
}

type liveObsReaderStub struct {
	calls    int
	lastKeys []string
	result   OptimizationLiveObservationsResult
	err      error
}

func (stub *liveObsReaderStub) ReadLiveObservations(_ context.Context, _, _ string, telemetryKeys []string) (OptimizationLiveObservationsResult, error) {
	stub.calls++
	stub.lastKeys = append([]string(nil), telemetryKeys...)
	return stub.result, stub.err
}

func TestOptimizationInputReaderUsesLiveHotSnapshotAndFallbacksCleanly(t *testing.T) {
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 18, 12, 0, 0, 0, shanghai)
	day := time.Date(2026, 11, 17, 0, 0, 0, 0, shanghai)
	watermark := at.Add(-time.Hour).UTC()
	energy := &energySeriesStub{response: analyticsmodel.EnergySeriesResponse{SchemaVersion: 1, Points: []analyticsmodel.EnergySeriesPoint{{
		PeriodStart: day.UTC(), PeriodEnd: day.AddDate(0, 0, 1).UTC(), EnergyKWh: 1118, Cost: &analyticsmodel.EnergyCost{AmountCNY: 690, Periods: []analyticsmodel.PeriodCost{}},
	}}, Metadata: analyticsmodel.EnergySeriesMetadata{DatasetRevision: "songyaxuan:1", DataWatermark: &watermark}}}

	makeLive := func(pointID, key string, sampledAt, receivedAt time.Time, value float64, quality, health string, qualityReasons, healthReasons []string) OptimizationLiveObservation {
		return OptimizationLiveObservation{
			ObservationID:   "01990000-2940-7000-8000-000000000001",
			DeviceID:        "01990000-2900-7000-8000-000000000001",
			PointID:         pointID,
			TelemetryKey:    key,
			PointRevision:   2,
			SampledAt:       sampledAt.UTC().Format(time.RFC3339Nano),
			ReceivedAt:      receivedAt.UTC().Format(time.RFC3339Nano),
			Value:           value,
			Unit:            "Cel",
			Quality:         quality,
			QualityReasons:  qualityReasons,
			HealthStatus:    health,
			HealthReasons:   healthReasons,
			SourceEventID:   "01990000-2930-7000-8000-000000000001",
			SourcePartition: "p0",
			SourceOffset:    10,
		}
	}
	makeCH := func(pointID, key string, sampledAt time.Time, value float64) string {
		return `{"observation_id":"01990000-2940-7000-8000-000000000009","device_id":"01990000-2900-7000-8000-000000000001","point_id":"` + pointID + `","telemetry_key":"` + key + `","point_revision":1,"sampled_at":"` + sampledAt.UTC().Format("2006-01-02T15:04:05.000Z") + `","received_at":"` + sampledAt.Add(time.Second).UTC().Format("2006-01-02T15:04:05.000Z") + `","value":` + strconv.FormatFloat(value, 'f', -1, 64) + `,"unit":"Cel","quality":"GOOD","quality_reasons":[],"source_event_id":"01990000-2930-7000-8000-000000000009","source_partition":"telemetry-0","source_offset":99}` + "\n"
	}

	var chQueries atomic.Int32
	chServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		chQueries.Add(1)
		body, _ := io.ReadAll(request.Body)
		sql := string(body)
		writer.Header().Set("Content-Type", "application/x-ndjson")
		switch {
		case strings.Contains(sql, "telemetry_key='supply_water_temperature'"):
			_, _ = writer.Write([]byte(makeCH("01990000-2910-7000-8000-000000000001", "supply_water_temperature", at.Add(-time.Minute), 7.1)))
		case strings.Contains(sql, "telemetry_key='zone.t53'"):
			_, _ = writer.Write([]byte(makeCH("01990000-2910-7000-8000-000000000002", "zone.t53", at.Add(-time.Minute), 22.1)))
		case strings.Contains(sql, "telemetry_key='zone.t54'"):
			_, _ = writer.Write([]byte(makeCH("01990000-2910-7000-8000-000000000003", "zone.t54", at.Add(-time.Minute), 24.1)))
		}
	}))
	defer chServer.Close()

	baseQuery := OptimizationStateQuery{
		TenantID: "01990000-3000-7000-8000-000000000001", SiteID: "01990000-5000-7000-8000-000000000001",
		SubjectType: "SITE", SubjectID: "01990000-5000-7000-8000-000000000001", Timezone: "Asia/Shanghai", At: at.UTC(),
		SupplyTemperatureKey: "supply_water_temperature", ZoneTemperatureKey: "zone.t53",
		ZoneTemperatureKeys: []string{"zone.t53", "zone.t54"},
	}

	t.Run("batch_hit_avoids_clickhouse_and_picks_worst_zone", func(t *testing.T) {
		chQueries.Store(0)
		liveStub := &liveObsReaderStub{
			result: OptimizationLiveObservationsResult{
				Observations: map[string]OptimizationLiveObservation{
					"supply_water_temperature": makeLive("01990000-2910-7000-8000-000000000001", "supply_water_temperature", at.Add(-20*time.Second), at.Add(-19*time.Second), 6.5, "GOOD", "HEALTHY", []string{}, nil),
					"zone.t53":                 makeLive("01990000-2910-7000-8000-000000000002", "zone.t53", at.Add(-20*time.Second), at.Add(-19*time.Second), 22.4, "GOOD", "HEALTHY", []string{}, nil),
					"zone.t54":                 makeLive("01990000-2910-7000-8000-000000000003", "zone.t54", at.Add(-20*time.Second), at.Add(-19*time.Second), 25.2, "GOOD", "HEALTHY", []string{}, nil),
				},
			},
		}
		reader, err := NewClickHouseInputReader(InputReaderConfig{
			BaseURL:          chServer.URL,
			Energy:           energy,
			LiveObservations: liveStub,
			Now:              func() time.Time { return at.Add(5 * time.Second).UTC() },
		})
		if err != nil {
			t.Fatal(err)
		}
		state, err := reader.ReadOptimizationState(t.Context(), baseQuery)
		if err != nil {
			t.Fatal(err)
		}
		if chQueries.Load() != 0 || liveStub.calls != 1 {
			t.Fatalf("expected 0 ClickHouse queries and 1 batch live call, got ch=%d live=%d", chQueries.Load(), liveStub.calls)
		}
		if !slices.Equal(liveStub.lastKeys, []string{"supply_water_temperature", "zone.t53", "zone.t54"}) {
			t.Fatalf("unexpected live keys: %v", liveStub.lastKeys)
		}
		if state.SupplyTemperature.Value != 6.5 || state.ZoneTemperature.TelemetryKey != "zone.t54" || state.ZoneTemperature.Value != 25.2 {
			t.Fatalf("unexpected state: %+v", state)
		}
	})

	t.Run("partial_miss_and_future_timestamp_fallback_only_for_affected_keys", func(t *testing.T) {
		chQueries.Store(0)
		liveStub := &liveObsReaderStub{
			result: OptimizationLiveObservationsResult{
				Observations: map[string]OptimizationLiveObservation{
					"supply_water_temperature": makeLive("01990000-2910-7000-8000-000000000001", "supply_water_temperature", at.Add(-20*time.Second), at.Add(-19*time.Second), 6.5, "GOOD", "HEALTHY", []string{}, nil),
					"zone.t53":                 makeLive("01990000-2910-7000-8000-000000000002", "zone.t53", at.Add(2*time.Second), at.Add(3*time.Second), 29.9, "GOOD", "HEALTHY", []string{}, nil),
				},
			},
		}
		reader, err := NewClickHouseInputReader(InputReaderConfig{
			BaseURL:          chServer.URL,
			Energy:           energy,
			LiveObservations: liveStub,
			Now:              func() time.Time { return at.Add(5 * time.Second).UTC() },
		})
		if err != nil {
			t.Fatal(err)
		}
		state, err := reader.ReadOptimizationState(t.Context(), baseQuery)
		if err != nil {
			t.Fatal(err)
		}
		if chQueries.Load() != 2 {
			t.Fatalf("expected ClickHouse fallback for 2 keys (t53 future race, t54 missing), got %d", chQueries.Load())
		}
		if state.SupplyTemperature.Value != 6.5 || state.ZoneTemperatures[0].Value != 22.1 || state.ZoneTemperatures[1].Value != 24.1 {
			t.Fatalf("unexpected mixed state: %+v", state)
		}
	})

	t.Run("live_error_falls_back_cleanly_to_clickhouse", func(t *testing.T) {
		chQueries.Store(0)
		liveStub := &liveObsReaderStub{err: errors.New("telemetry runtime unavailable")}
		reader, err := NewClickHouseInputReader(InputReaderConfig{
			BaseURL:          chServer.URL,
			Energy:           energy,
			LiveObservations: liveStub,
			Now:              func() time.Time { return at.Add(5 * time.Second).UTC() },
		})
		if err != nil {
			t.Fatal(err)
		}
		state, err := reader.ReadOptimizationState(t.Context(), baseQuery)
		if err != nil {
			t.Fatal(err)
		}
		if chQueries.Load() != 3 || state.SupplyTemperature.Value != 7.1 || state.ZoneTemperature.Value != 24.1 {
			t.Fatalf("expected full ClickHouse fallback (3 queries), got ch=%d state=%+v", chQueries.Load(), state)
		}
	})

	t.Run("ambiguous_unhealthy_and_non_good_live_observations_fail_closed_without_clickhouse_fallback", func(t *testing.T) {
		chQueries.Store(0)
		liveStub := &liveObsReaderStub{
			result: OptimizationLiveObservationsResult{
				Observations: map[string]OptimizationLiveObservation{
					"supply_water_temperature": makeLive("01990000-2910-7000-8000-000000000001", "supply_water_temperature", at.Add(-20*time.Second), at.Add(-19*time.Second), 6.5, "GOOD", "HEALTHY", []string{}, nil),
					"zone.t54":                 makeLive("01990000-2910-7000-8000-000000000003", "zone.t54", at.Add(-20*time.Second), at.Add(-19*time.Second), 24.5, "GOOD", "HEALTHY", []string{}, nil),
				},
				AmbiguousKeys: []string{"zone.t53"},
			},
		}
		reader, _ := NewClickHouseInputReader(InputReaderConfig{
			BaseURL:          chServer.URL,
			Energy:           energy,
			LiveObservations: liveStub,
			Now:              func() time.Time { return at.Add(5 * time.Second).UTC() },
		})
		_, err := reader.ReadOptimizationState(t.Context(), baseQuery)
		if reason, ok := PreparationReasonOf(err); !ok || reason != ReasonZoneTempMissing || chQueries.Load() != 0 {
			t.Fatalf("expected ReasonZoneTempMissing with 0 CH calls on ambiguity, got ok=%v reason=%q ch=%d err=%v", ok, reason, chQueries.Load(), err)
		}

		liveStub.result.AmbiguousKeys = nil
		liveStub.result.Observations["zone.t53"] = makeLive("01990000-2910-7000-8000-000000000002", "zone.t53", at.Add(-20*time.Second), at.Add(-19*time.Second), 22.0, "GOOD", "SUSPECT", []string{}, []string{"PROBE_OFFSET"})
		_, err = reader.ReadOptimizationState(t.Context(), baseQuery)
		if reason, ok := PreparationReasonOf(err); !ok || reason != ReasonInputsInvalid || chQueries.Load() != 0 {
			t.Fatalf("expected ReasonInputsInvalid on PROBE_OFFSET, got ok=%v reason=%q ch=%d err=%v", ok, reason, chQueries.Load(), err)
		}

		liveStub.result.Observations["supply_water_temperature"] = makeLive("01990000-2910-7000-8000-000000000001", "supply_water_temperature", at.Add(-20*time.Second), at.Add(-19*time.Second), 6.5, "STALE", "STALE", []string{"SOURCE_LAG_EXCEEDED"}, nil)
		liveStub.result.Observations["zone.t53"] = makeLive("01990000-2910-7000-8000-000000000002", "zone.t53", at.Add(-20*time.Second), at.Add(-19*time.Second), 22.0, "GOOD", "HEALTHY", []string{}, nil)
		_, err = reader.ReadOptimizationState(t.Context(), baseQuery)
		if reason, ok := PreparationReasonOf(err); !ok || reason != ReasonSupplyTempMissing || chQueries.Load() != 0 {
			t.Fatalf("expected ReasonSupplyTempMissing on STALE supply, got ok=%v reason=%q ch=%d err=%v", ok, reason, chQueries.Load(), err)
		}
	})

	t.Run("historical_query_skips_live_observations_and_uses_clickhouse", func(t *testing.T) {
		chQueries.Store(0)
		liveStub := &liveObsReaderStub{}
		reader, _ := NewClickHouseInputReader(InputReaderConfig{
			BaseURL:          chServer.URL,
			Energy:           energy,
			LiveObservations: liveStub,
			Now:              func() time.Time { return at.Add(2 * time.Hour).UTC() },
		})
		state, err := reader.ReadOptimizationState(t.Context(), baseQuery)
		if err != nil {
			t.Fatal(err)
		}
		if liveStub.calls != 0 || chQueries.Load() != 3 || state.SupplyTemperature.Value != 7.1 {
			t.Fatalf("expected historical query to skip live reader, got live=%d ch=%d state=%+v", liveStub.calls, chQueries.Load(), state)
		}
	})
}

type autoControlLiveDeviceStub struct {
	liveObsReaderStub
	deviceObs map[string]map[string]OptimizationLiveObservation
	devCalls  int
}

func (s *autoControlLiveDeviceStub) ReadDeviceLiveObservations(_ context.Context, _, _ string, _ []string) (map[string]map[string]OptimizationLiveObservation, error) {
	s.devCalls++
	return s.deviceObs, nil
}

func TestReadLiveAutoControlObservationUsesBatchedLiveObservationsIncludingWetBulb(t *testing.T) {
	at := time.Date(2026, 11, 14, 15, 0, 0, 0, time.UTC)
	runStr := "RUNNING"
	loadVal := 380.0
	powerVal := 85.0

	var chQueries atomic.Int32
	chServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		sql := string(body)
		if !strings.Contains(sql, "ORDER BY sampled_at ASC") {
			chQueries.Add(1)
		}
		writer.Header().Set("Content-Type", "application/x-ndjson")
	}))
	defer chServer.Close()

	twin := planttwin.SiteTwin{
		Scope:           planttwin.SiteScope{TenantID: testPlantTenantID, SiteID: testPlantSiteID},
		Timezone:        "Asia/Shanghai",
		SiteControlMode: planttwin.ControlModeRemoteAuto,
		Units: []planttwin.PlantUnit{
			{AssetID: testChillerAID, AssetCode: "chiller-a", AssetName: "A 机组", AssetType: planttwin.AssetTypeChiller},
		},
		Devices: []planttwin.TwinDevice{
			{DeviceID: testMeterAID, DeviceCode: "amp-a", SourceKey: "amp-a"},
			{DeviceID: testBTUDevID, DeviceCode: "plant-btu", SourceKey: "plant-btu"},
		},
		PointBindings: []planttwin.PointBinding{
			{PointID: "pt-load", DeviceID: testBTUDevID, DeviceCode: "plant-btu", DeviceSourceKey: "plant-btu", PointCode: "chw_load", WireSourceKey: "chw_load", PointType: "TELEMETRY", Unit: "kW", SubjectType: planttwin.SubjectTypeSite, BindingRole: planttwin.BindingDescribes, SemanticRole: planttwin.RoleCoolingLoad},
			{PointID: "pt-pa", DeviceID: testMeterAID, DeviceCode: "amp-a", DeviceSourceKey: "amp-a", PointCode: "p_kw", WireSourceKey: "p_kw", PointType: "TELEMETRY", Unit: "kW", SubjectType: planttwin.SubjectTypeAsset, AssetID: testChillerAID, BindingRole: planttwin.BindingMeters, SemanticRole: planttwin.RoleActivePower},
			{PointID: "pt-rs", DeviceID: testMeterAID, DeviceCode: "amp-a", DeviceSourceKey: "amp-a", PointCode: "run_state", WireSourceKey: "chiller.run_state", PointType: "TELEMETRY", SubjectType: planttwin.SubjectTypeAsset, AssetID: testChillerAID, BindingRole: planttwin.BindingDescribes, SemanticRole: planttwin.RoleRunState},
		},
	}

	sampled := at.Add(-15 * time.Second).UTC().Format(time.RFC3339Nano)
	received := at.Add(-14 * time.Second).UTC().Format(time.RFC3339Nano)
	stub := &autoControlLiveDeviceStub{
		liveObsReaderStub: liveObsReaderStub{
			result: OptimizationLiveObservationsResult{
				Observations: map[string]OptimizationLiveObservation{
					"supply_water_temperature": {ObservationID: "01990000-2940-7000-8000-000000000001", DeviceID: testBTUDevID, PointID: "01990000-2910-7000-8000-000000000001", TelemetryKey: "supply_water_temperature", PointRevision: 1, SampledAt: sampled, ReceivedAt: received, Value: 6.8, Unit: "Cel", Quality: "GOOD", HealthStatus: "HEALTHY", QualityReasons: []string{}, SourceEventID: "01990000-2930-7000-8000-000000000001", SourcePartition: "p0", SourceOffset: 10},
					"zone.t1":                  {ObservationID: "01990000-2940-7000-8000-000000000002", DeviceID: testBTUDevID, PointID: "01990000-2910-7000-8000-000000000002", TelemetryKey: "zone.t1", PointRevision: 1, SampledAt: sampled, ReceivedAt: received, Value: 23.1, Unit: "Cel", Quality: "GOOD", HealthStatus: "HEALTHY", QualityReasons: []string{}, SourceEventID: "01990000-2930-7000-8000-000000000002", SourcePartition: "p0", SourceOffset: 11},
					"outdoor_wet_bulb":         {ObservationID: "01990000-2940-7000-8000-000000000003", DeviceID: testBTUDevID, PointID: "01990000-2910-7000-8000-000000000003", TelemetryKey: "outdoor_wet_bulb", PointRevision: 1, SampledAt: sampled, ReceivedAt: received, Value: 21.5, Unit: "Cel", Quality: "GOOD", HealthStatus: "HEALTHY", QualityReasons: []string{}, SourceEventID: "01990000-2930-7000-8000-000000000003", SourcePartition: "p0", SourceOffset: 12},
				},
			},
		},
		deviceObs: map[string]map[string]OptimizationLiveObservation{
			testBTUDevID: {
				"chw_load": {DeviceID: testBTUDevID, SourceID: "plant-btu", TelemetryKey: "chw_load", ValueType: "NUMBER", Value: loadVal, ValueNumber: &loadVal, Unit: "kW", Quality: "GOOD", SampledAt: sampled, ReceivedAt: received},
			},
			testMeterAID: {
				"p_kw":              {DeviceID: testMeterAID, SourceID: "amp-a", TelemetryKey: "p_kw", ValueType: "NUMBER", Value: powerVal, ValueNumber: &powerVal, Unit: "kW", Quality: "GOOD", SampledAt: sampled, ReceivedAt: received},
				"chiller.run_state": {DeviceID: testMeterAID, SourceID: "amp-a", TelemetryKey: "chiller.run_state", ValueType: "STRING", ValueString: &runStr, Quality: "GOOD", SampledAt: sampled, ReceivedAt: received},
			},
		},
	}

	reader, err := NewClickHouseInputReader(InputReaderConfig{
		BaseURL:          chServer.URL,
		Energy:           &energySeriesStub{},
		LiveObservations: stub,
		Now:              func() time.Time { return at.Add(2 * time.Second).UTC() },
	})
	if err != nil {
		t.Fatal(err)
	}
	obs, err := reader.ReadLiveAutoControlObservation(
		t.Context(),
		testPlantTenantID,
		testPlantSiteID,
		InputMapping{SupplyTemperatureKey: "supply_water_temperature", ZoneTemperatureKeys: []string{"zone.t1"}},
		StagingTopology{Twin: &twin},
		at,
	)
	if err != nil {
		t.Fatalf("ReadLiveAutoControlObservation: %v", err)
	}
	if chQueries.Load() != 0 || stub.calls != 1 || stub.devCalls != 1 {
		t.Fatalf("expected 0 CH latest/wetbulb queries, got ch=%d siteCalls=%d devCalls=%d", chQueries.Load(), stub.calls, stub.devCalls)
	}
	if obs.SupplyTemp == nil || obs.SupplyTemp.Value != 6.8 || obs.ZoneTemps["zone.t1"].Value != 23.1 {
		t.Fatalf("unexpected supply/zone obs: %+v", obs)
	}
	if obs.OutdoorWetBulbC == nil || *obs.OutdoorWetBulbC != 21.5 {
		t.Fatalf("expected OutdoorWetBulbC=21.5, got %v", obs.OutdoorWetBulbC)
	}
	if obs.CoolingLoadKW == nil || *obs.CoolingLoadKW != 380 || obs.RunningChillers != 1 || obs.ChillerPowerKW == nil || *obs.ChillerPowerKW != 85 {
		t.Fatalf("unexpected staging totals from live cache: %+v", obs)
	}
}
