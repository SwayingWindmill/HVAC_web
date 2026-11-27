package forecast

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

type weatherHistoryStub struct {
	obs []WeatherObservation
	err error
}

func (s weatherHistoryStub) ReadWeatherSeries(context.Context, WeatherHistoryQuery) ([]WeatherObservation, error) {
	if s.err != nil {
		return nil, s.err
	}
	return slices.Clone(s.obs), nil
}

func TestPeriodStartAlignmentAtNonBoundaryOrigin(t *testing.T) {
	// Cron runs at minute :07 (12:07:00Z). Facts complete by 12:00:00Z ([11:45, 12:00)).
	origin := time.Date(2026, 10, 12, 4, 7, 0, 0, time.UTC) // 12:07 CST
	facts := make([]MetricFact, 0, 2*96)
	for at := origin.Truncate(15 * time.Minute).Add(-48 * time.Hour); at.Before(origin.Truncate(15 * time.Minute)); at = at.Add(15 * time.Minute) {
		cst := at.In(resolveSiteLocation("Asia/Shanghai"))
		val := 10.0
		if cst.Hour() == 12 && cst.Minute() == 15 {
			val = 220.0
		} else if cst.Hour() == 12 && cst.Minute() == 0 {
			val = 95.0
		}
		facts = append(facts, MetricFact{
			ResultID:     "01990000-1400-7000-8000-000000000001",
			PeriodStart:  at,
			PeriodEnd:    at.Add(15 * time.Minute),
			CalculatedAt: at.Add(16 * time.Minute),
			Value:        val,
			Unit:         "kW",
			Quality:      "GOOD",
			Completeness: 1,
			Revision:     1,
		})
	}
	store := &preparationStoreStub{definition: preparationDefinition()}
	history := &metricHistoryStub{facts: facts}
	preparer, err := NewPreparer(store, history, func() time.Time { return origin })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := preparer.Prepare(t.Context(), preparationRequest()); err != nil {
		t.Fatal(err)
	}
	execIn, err := executionInput(store.created[0].FeatureValues)
	if err != nil {
		t.Fatal(err)
	}
	req := validRequest()
	req.ForecastOrigin = origin
	req.Observations = execIn.Observations
	req.Timezone = "Asia/Shanghai"

	sink := &captureSink{}
	pub := &memoryPublicationStore{}
	service, err := NewService(sink, pub, func() time.Time { return origin })
	if err != nil {
		t.Fatal(err)
	}
	points, err := service.Forecast(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	// First future interval after 12:07 CST is [12:15, 12:30) CST -> PeriodStart 04:15:00Z!
	wantFirst := time.Date(2026, 10, 12, 4, 15, 0, 0, time.UTC)
	if !points[0].ForecastFor.Equal(wantFirst) {
		t.Fatalf("first ForecastFor=%s, want %s (exact interval PeriodStart)", points[0].ForecastFor, wantFirst)
	}
	if math.Abs(points[0].Value-220.0) > 1e-6 {
		t.Fatalf("12:15 CST PeriodStart forecast=%.2f, want 220.0 (not 12:00 CST value 95.0)", points[0].Value)
	}
	if pub.completed[0].Metadata.AlignedTo != alignmentModePeriodStart {
		t.Fatalf("expected AlignedTo=%s, got %q", alignmentModePeriodStart, pub.completed[0].Metadata.AlignedTo)
	}
}

func TestWeekdayWeekendAndCNHolidayDifferentiation(t *testing.T) {
	// Origin: 2026-10-08 00:00 CST (right after 2026-10-01..07 National Day & before 2026-10-10 makeup workday).
	loc := resolveSiteLocation("Asia/Shanghai")
	origin := time.Date(2026, 10, 8, 0, 0, 0, 0, loc).UTC()
	var obs []Observation
	for at := origin.Add(-21 * 24 * time.Hour); at.Before(origin); at = at.Add(15 * time.Minute) {
		cst := at.In(loc)
		val := 5.0
		if cst.Hour() >= 10 && cst.Hour() < 20 {
			switch classifyDay(at, loc) {
			case dayWorkday:
				val = 100.0
			case dayWeekend:
				val = 180.0
			case dayHoliday:
				val = 230.0
			}
		}
		obs = append(obs, Observation{ObservedAt: at, Value: val})
	}
	// Forecast across Oct 8 (Thu workday), Oct 9 (Fri workday), Oct 10 (Sat makeup workday!), Oct 11 (Sun weekend)
	req := validRequest()
	req.ForecastOrigin = origin
	req.HorizonMinutes = 4 * 1440
	req.Timezone = "Asia/Shanghai"
	req.Observations = obs

	points, meta, err := (WeatherChangePointEngine{}).GenerateWithMetadata(t.Context(), req, origin)
	if err != nil {
		t.Fatal(err)
	}
	if meta.ModelFamily != modelFamilyWeatherChangePoint {
		t.Fatalf("modelFamily=%q", meta.ModelFamily)
	}
	findAt := func(year, month, day, hour int) float64 {
		target := time.Date(year, time.Month(month), day, hour, 0, 0, 0, loc).UTC()
		for _, pt := range points {
			if pt.ForecastFor.Equal(target) {
				return pt.Value
			}
		}
		t.Fatalf("missing point for %s", target)
		return 0
	}
	thuVal := findAt(2026, 10, 8, 12)
	satMakeupVal := findAt(2026, 10, 10, 12)
	sunWeekendVal := findAt(2026, 10, 11, 12)
	if math.Abs(thuVal-100.0) > 25.0 {
		t.Fatalf("workday Thu forecast=%.2f, want near 100", thuVal)
	}
	if math.Abs(satMakeupVal-100.0) > 25.0 {
		t.Fatalf("makeup workday Sat 2026-10-10 forecast=%.2f, want workday-like ~100", satMakeupVal)
	}
	if sunWeekendVal <= thuVal {
		t.Fatalf("weekend Sun 2026-10-11 (%.2f) should exceed workday (%.2f)", sunWeekendVal, thuVal)
	}

	// Normal post-holiday week where weekend history is recent (origin 2026-10-15 00:00 CST): Sat 2026-10-17 must show strong weekend lift.
	origin2 := time.Date(2026, 10, 15, 0, 0, 0, 0, loc).UTC()
	var obs2 []Observation
	for at := origin2.Add(-21 * 24 * time.Hour); at.Before(origin2); at = at.Add(15 * time.Minute) {
		cst := at.In(loc)
		val := 5.0
		if cst.Hour() >= 10 && cst.Hour() < 20 {
			switch classifyDay(at, loc) {
			case dayWorkday:
				val = 100.0
			case dayWeekend:
				val = 180.0
			case dayHoliday:
				val = 230.0
			}
		}
		obs2 = append(obs2, Observation{ObservedAt: at, Value: val})
	}
	req2 := validRequest()
	req2.ForecastOrigin = origin2
	req2.HorizonMinutes = 4 * 1440
	req2.Timezone = "Asia/Shanghai"
	req2.Observations = obs2
	points2, _, err := (WeatherChangePointEngine{}).GenerateWithMetadata(t.Context(), req2, origin2)
	if err != nil {
		t.Fatal(err)
	}
	findAt2 := func(year, month, day, hour int) float64 {
		target := time.Date(year, time.Month(month), day, hour, 0, 0, 0, loc).UTC()
		for _, pt := range points2 {
			if pt.ForecastFor.Equal(target) {
				return pt.Value
			}
		}
		t.Fatalf("missing point for %s", target)
		return 0
	}
	thu2 := findAt2(2026, 10, 15, 12)
	satWeekend2 := findAt2(2026, 10, 17, 12)
	if satWeekend2 <= thu2+35.0 {
		t.Fatalf("weekend Sat 2026-10-17 (%.2f) should be substantially higher than workday Thu (%.2f)", satWeekend2, thu2)
	}
	// Statutory holiday test (origin 2026-10-02 00:00 CST with Oct 1 holiday + prior Sep history): Oct 2 holiday must exceed prior workday baseline.
	origin3 := time.Date(2026, 10, 2, 0, 0, 0, 0, loc).UTC()
	var obs3 []Observation
	for at := origin3.Add(-21 * 24 * time.Hour); at.Before(origin3); at = at.Add(15 * time.Minute) {
		cst := at.In(loc)
		val := 5.0
		if cst.Hour() >= 10 && cst.Hour() < 20 {
			switch classifyDay(at, loc) {
			case dayWorkday:
				val = 100.0
			case dayWeekend:
				val = 180.0
			case dayHoliday:
				val = 230.0
			}
		}
		obs3 = append(obs3, Observation{ObservedAt: at, Value: val})
	}
	req3 := validRequest()
	req3.ForecastOrigin = origin3
	req3.HorizonMinutes = 1440
	req3.Timezone = "Asia/Shanghai"
	req3.Observations = obs3
	points3, _, err := (WeatherChangePointEngine{}).GenerateWithMetadata(t.Context(), req3, origin3)
	if err != nil {
		t.Fatal(err)
	}
	targetHol := time.Date(2026, 10, 2, 12, 0, 0, 0, loc).UTC()
	var holVal float64
	foundHol := false
	for _, pt := range points3 {
		if pt.ForecastFor.Equal(targetHol) {
			holVal = pt.Value
			foundHol = true
			break
		}
	}
	if !foundHol || holVal <= 200.0 {
		t.Fatalf("holiday 2026-10-02 12:00 forecast=%.2f (found=%v), want > 200", holVal, foundHol)
	}
}

func TestWeatherChangePointAndResidualARAdaptDuringColdSnap(t *testing.T) {
	loc := resolveSiteLocation("Asia/Shanghai")
	origin := time.Date(2026, 11, 19, 8, 0, 0, 0, loc).UTC()
	var obs []Observation
	var wx []WeatherObservation
	for at := origin.Add(-20 * 24 * time.Hour); at.Before(origin); at = at.Add(15 * time.Minute) {
		cst := at.In(loc)
		// Warm 28C before Nov 18, cold snap 14C starting Nov 18
		temp := 28.0
		if !cst.Before(time.Date(2026, 11, 18, 0, 0, 0, 0, loc)) {
			temp = 14.0
		}
		load := 2.0
		if cst.Hour() >= 10 && cst.Hour() < 20 {
			load = 35.0 + 10.0*math.Max(0, temp-15.0) // 165 kW warm, 35 kW cold!
		}
		obs = append(obs, Observation{ObservedAt: at, Value: load})
		tempCopy := temp
		wx = append(wx, WeatherObservation{ObservedAt: at, DryBulbC: &tempCopy, Source: WeatherSourceSiteSensors})
	}
	// Provide Open-Meteo style cold weather for the next 24h (14C)
	for at := origin.Add(15 * time.Minute); !at.After(origin.Add(24 * time.Hour)); at = at.Add(15 * time.Minute) {
		temp := 14.0
		wx = append(wx, WeatherObservation{ObservedAt: at, DryBulbC: &temp, Source: WeatherSourceOpenMeteo})
	}

	req := validRequest()
	req.ForecastOrigin = origin
	req.Timezone = "Asia/Shanghai"
	req.Observations = obs
	req.WeatherObservations = wx
	req.WeatherSource = WeatherSourceSiteAndOpenMeteo

	baselinePts, err := BaselineEngine{}.Generate(t.Context(), req, origin)
	if err != nil {
		t.Fatal(err)
	}
	upgradedPts, meta, err := (WeatherChangePointEngine{}).GenerateWithMetadata(t.Context(), req, origin)
	if err != nil {
		t.Fatal(err)
	}
	if meta.WeatherMode != weatherModeSiteAndNWP {
		t.Fatalf("weatherMode=%q, want %q", meta.WeatherMode, weatherModeSiteAndNWP)
	}
	noon := time.Date(2026, 11, 19, 12, 0, 0, 0, loc).UTC()
	var baseNoon, upNoon float64
	for i := range baselinePts {
		if baselinePts[i].ForecastFor.Equal(noon) {
			baseNoon = baselinePts[i].Value
			upNoon = upgradedPts[i].Value
		}
	}
	// True load at 14C noon is 35 kW. 7d baseline sits way above 130 kW; upgraded should track near 35 kW!
	if math.Abs(upNoon-35.0) >= math.Abs(baseNoon-35.0) || upNoon > 65.0 {
		t.Fatalf("cold snap adaptation failed: upgraded=%.2f baseline=%.2f (true=35.0)", upNoon, baseNoon)
	}
}

func TestStullWetBulbAndPhysicalSanityBounds(t *testing.T) {
	// Stull reference: T=20C, RH=50% -> Tw ≈ 13.7C; T=30C, RH=70% -> Tw ≈ 25.5C
	tw1, ok1 := StullWetBulbC(20.0, 50.0)
	if !ok1 || math.Abs(tw1-13.7) > 0.3 {
		t.Fatalf("Stull(20C,50%%)=%.3f ok=%v", tw1, ok1)
	}
	tw2, ok2 := StullWetBulbC(30.0, 70.0)
	if !ok2 || math.Abs(tw2-25.5) > 0.3 {
		t.Fatalf("Stull(30C,70%%)=%.3f ok=%v", tw2, ok2)
	}
	if _, ok := StullWetBulbC(106.5, 50.0); ok {
		t.Fatal("expected 106.5C dry-bulb spike to be rejected")
	}
	if _, ok := StullWetBulbC(311.1, 50.0); ok {
		t.Fatal("expected unscaled 311.1C dry-bulb reading to be rejected")
	}

	// Sanitize rejects dry-bulb spikes & wet-bulb > dry-bulb + 0.5, and derives Stull WB from T+RH.
	now := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	spike := 106.5
	unscaled := 311.1
	validDry := 28.0
	validRH := 65.0
	impossibleWB := 32.0
	if _, ok := sanitizeWeatherObservation(WeatherObservation{ObservedAt: now, DryBulbC: &spike}); ok {
		t.Fatal("spike 106.5C should be filtered")
	}
	if _, ok := sanitizeWeatherObservation(WeatherObservation{ObservedAt: now, DryBulbC: &unscaled}); ok {
		t.Fatal("unscaled 311.1C should be filtered")
	}
	derived, ok := sanitizeWeatherObservation(WeatherObservation{ObservedAt: now, DryBulbC: &validDry, WetBulbC: &impossibleWB, RelativeHumidityPct: &validRH})
	if !ok || derived.DryBulbC == nil || *derived.DryBulbC != 28.0 || derived.WetBulbC == nil {
		t.Fatalf("expected valid dry bulb with Stull wet bulb fallback after dropping impossible WB, got %#v", derived)
	}
	if *derived.WetBulbC >= 28.0 || *derived.WetBulbC < 20.0 {
		t.Fatalf("unexpected derived Stull wet bulb=%.2f", *derived.WetBulbC)
	}
}

func TestOfflineWeatherMissingGracefulDegradation(t *testing.T) {
	// Mock Open-Meteo server returning HTTP 503 (never touches external internet!)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	origin := time.Date(2026, 11, 5, 4, 7, 0, 0, time.UTC)
	provider, err := NewOpenMeteoProvider(OpenMeteoConfig{
		Enabled:       true,
		BaseURL:       server.URL,
		DefaultCoords: &SiteCoordinates{Latitude: 22.54, Longitude: 114.06},
		HTTPClient:    server.Client(),
		Clock:         func() time.Time { return origin },
	})
	if err != nil {
		t.Fatal(err)
	}

	facts := make([]MetricFact, 0, 2*96)
	for at := origin.Truncate(15 * time.Minute).Add(-48 * time.Hour); at.Before(origin.Truncate(15 * time.Minute)); at = at.Add(15 * time.Minute) {
		facts = append(facts, metricFact(at.Add(15*time.Minute), 45.0, 1))
	}
	store := &preparationStoreStub{definition: preparationDefinition()}
	history := &metricHistoryStub{facts: facts}
	preparer, err := NewPreparerWithOptions(store, history, func() time.Time { return origin }, PreparerOptions{
		WeatherHistory:  weatherHistoryStub{err: errors.New("clickhouse weather outage")},
		WeatherForecast: provider,
		DefaultTimezone: "Asia/Shanghai",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Must NOT fail or stall when both site weather and Open-Meteo fail!
	prepared, err := preparer.Prepare(t.Context(), preparationRequest())
	if err != nil {
		t.Fatalf("expected graceful degradation without weather, got %v", err)
	}
	if prepared.Status != "PENDING" {
		t.Fatalf("status=%q", prepared.Status)
	}
	frozen := store.created[0]
	if frozen.FeatureValues.WeatherSource != WeatherSourceNone || !strings.Contains(frozen.FeatureValues.WeatherFallbackReason, "SITE_WEATHER_UNAVAILABLE") || !strings.Contains(frozen.FeatureValues.WeatherFallbackReason, "OPEN_METEO_UNAVAILABLE") {
		t.Fatalf("unexpected fallback metadata: source=%q reason=%q", frozen.FeatureValues.WeatherSource, frozen.FeatureValues.WeatherFallbackReason)
	}
	execIn, err := executionInput(frozen.FeatureValues)
	if err != nil {
		t.Fatal(err)
	}
	req := validRequest()
	req.ForecastOrigin = origin
	req.Observations = execIn.Observations
	req.WeatherObservations = execIn.WeatherObservations
	req.WeatherSource = execIn.WeatherSource
	req.WeatherFallbackReason = execIn.WeatherFallbackReason
	req.Timezone = execIn.Timezone

	points, meta, err := (WeatherChangePointEngine{}).GenerateWithMetadata(t.Context(), req, origin)
	if err != nil {
		t.Fatalf("degraded forecast failed: %v", err)
	}
	if points[0].Quality != "VALID" || meta.WeatherMode != weatherModeNone {
		t.Fatalf("unexpected degraded quality/meta: q=%q meta=%#v", points[0].Quality, meta)
	}
	if math.Abs(points[0].Value-45.0) > 1e-6 {
		t.Fatalf("degraded value=%.2f, want 45.0", points[0].Value)
	}
}

func TestOpenMeteoProviderParsesAndComputesStullFromMockHTTP(t *testing.T) {
	origin := time.Date(2026, 11, 20, 4, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.RawQuery, "latitude=22.54") {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"hourly":{"time":["2026-11-20T04:00","2026-11-20T05:00"],"temperature_2m":[20.0,22.0],"relative_humidity_2m":[50.0,60.0]}}`))
	}))
	defer server.Close()

	provider, err := NewOpenMeteoProvider(OpenMeteoConfig{
		Enabled:       true,
		BaseURL:       server.URL,
		DefaultCoords: &SiteCoordinates{Latitude: 22.54, Longitude: 114.06},
		HTTPClient:    server.Client(),
		Clock:         func() time.Time { return origin },
	})
	if err != nil {
		t.Fatal(err)
	}
	obs, issuedAt, err := provider.ForecastWeather(t.Context(), WeatherForecastRequest{
		SiteID: "s-1", From: origin, To: origin.Add(time.Hour), GranularityMinutes: 15,
	})
	if err != nil || !issuedAt.Equal(origin) {
		t.Fatalf("ForecastWeather err=%v issuedAt=%s", err, issuedAt)
	}
	if len(obs) != 5 || obs[0].DryBulbC == nil || *obs[0].DryBulbC != 20.0 || obs[0].WetBulbC == nil || math.Abs(*obs[0].WetBulbC-13.7) > 0.3 {
		t.Fatalf("unexpected Open-Meteo interpolated Stull output: %#v", obs)
	}
}

func TestClickHouseWeatherHistoryQueryBoundsAndDecode(t *testing.T) {
	var seenSQL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 8192)
		n, _ := r.Body.Read(buf)
		seenSQL = string(buf[:n])
		_, _ = w.Write([]byte(`{"bucket_utc":"2026-10-16T04:00:00Z","dry_bulb_c":28.5,"wet_bulb_c":null,"relative_humidity_pct":65.0}` + "\n"))
	}))
	defer server.Close()

	reader, err := NewClickHouseHistoryReader(HistoryConfig{
		BaseURL: server.URL, Username: "forecast_service_reader", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 10, 16, 8, 0, 0, 0, time.UTC)
	obs, err := reader.ReadWeatherSeries(t.Context(), WeatherHistoryQuery{
		TenantID: "01990000-3000-7000-8000-000000000001",
		SiteID:   "01990000-5000-7000-8000-000000000001",
		From:     from, To: to, GranularityMinutes: 15,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(seenSQL, "FROM telemetry_history.observations AS obs") || !strings.Contains(seenSQL, "ambient_temp") || !strings.Contains(seenSQL, "received_at <=") {
		t.Fatalf("unexpected weather history SQL:\n%s", seenSQL)
	}
	if len(obs) != 1 || obs[0].DryBulbC == nil || *obs[0].DryBulbC != 28.5 || obs[0].WetBulbC == nil {
		t.Fatalf("expected Stull wet-bulb derived from ClickHouse dry_bulb+RH, got %#v", obs)
	}
}

func TestCausalityFutureValuesNeverAffectPastForecasts(t *testing.T) {
	loc := resolveSiteLocation("Asia/Shanghai")
	origin := time.Date(2026, 11, 18, 8, 0, 0, 0, loc).UTC()

	var pastObs []Observation
	var pastWx []WeatherObservation
	for at := origin.Add(-14 * 24 * time.Hour); at.Before(origin); at = at.Add(15 * time.Minute) {
		cst := at.In(loc)
		val := 5.0
		if cst.Hour() >= 10 && cst.Hour() < 20 {
			val = 120.0
		}
		pastObs = append(pastObs, Observation{ObservedAt: at, Value: val})
		tDry := 24.0
		pastWx = append(pastWx, WeatherObservation{ObservedAt: at, DryBulbC: &tDry, Source: WeatherSourceSiteSensors})
	}

	req1 := validRequest()
	req1.ForecastOrigin = origin
	req1.Timezone = "Asia/Shanghai"
	req1.Observations = pastObs
	req1.WeatherObservations = pastWx

	// Construct req2 with identical past facts, plus future weather spikes AFTER origin.
	req2 := req1
	wx2 := slices.Clone(pastWx)
	extremeHot := 48.0
	wx2 = append(wx2, WeatherObservation{ObservedAt: origin.Add(15 * time.Minute), DryBulbC: &extremeHot, Source: WeatherSourceOpenMeteo})
	req2.WeatherObservations = wx2

	pts1, err := (WeatherChangePointEngine{}).Generate(t.Context(), req1, origin)
	if err != nil {
		t.Fatal(err)
	}
	// Mutating future NWP weather should NOT change the fitted beta or historical baseline;
	// furthermore, with only past observations/weather, CalendarAREngine must be bit-for-bit identical
	// regardless of any weather mutation, and WeatherChangePointEngine with identical inputs <= origin is bit-for-bit identical.
	req3 := req1
	req3.Observations = slices.Clone(pastObs)
	req3.WeatherObservations = slices.Clone(pastWx)
	pts3, err := (WeatherChangePointEngine{}).Generate(t.Context(), req3, origin)
	if err != nil {
		t.Fatal(err)
	}
	for i := range pts1 {
		if pts1[i].Value != pts3[i].Value || !pts1[i].ForecastFor.Equal(pts3[i].ForecastFor) {
			t.Fatalf("non-deterministic forecast at index %d: %v vs %v", i, pts1[i], pts3[i])
		}
	}
	// Also verify Request.Validate rejects future load observations > ForecastOrigin (zero future load leakage).
	reqFutureLoad := req1
	reqFutureLoad.Observations = append(slices.Clone(pastObs), Observation{ObservedAt: origin.Add(15 * time.Minute), Value: 999.0})
	if err := reqFutureLoad.Validate(); err == nil {
		t.Fatal("expected Request.Validate to reject future load observations after ForecastOrigin")
	}
	// And verify CalendarAREngine ignores future weather observations completely.
	cal1, err := (CalendarAREngine{}).Generate(t.Context(), req1, origin)
	if err != nil {
		t.Fatal(err)
	}
	cal2, err := (CalendarAREngine{}).Generate(t.Context(), req2, origin)
	if err != nil {
		t.Fatal(err)
	}
	for i := range cal1 {
		if cal1[i].Value != cal2[i].Value {
			t.Fatalf("CalendarAREngine changed with future weather at %d", i)
		}
	}
}
