package forecast

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"slices"
	"testing"
	"time"
)

type songyaxuanPoint15m struct {
	T                  string   `json:"t"`
	SiteLoadKW         *float64 `json:"siteLoadKw"`
	MeterTotalPowerKW  *float64 `json:"meterTotalPowerKw"`
	OutdoorDryBulbT15C *float64 `json:"outdoorDryBulbT15C"`
	OutdoorWetBulbC    *float64 `json:"outdoorWetBulbC"`
}

type songyaxuanDataset struct {
	Series15m []songyaxuanPoint15m `json:"series15m"`
}

type backtestStep struct {
	ts       time.Time
	loadKW   *float64
	siteLoad *float64
	dryBulbC *float64
	wetBulbC *float64
}

type backtestMetrics struct {
	Name          string
	Count15m      int
	CVRMSE15mPct  float64
	NMBE15mPct    float64
	Peak0921MAEKW float64
	CVRMSE1hPct   float64
	LateNovBias   float64
}

type backtestMode int

const (
	modeBaseline7d backtestMode = iota
	modeBaselineOldPeriodEndBug
	modeCalendarAR
	modeWeatherCausalPersistence
	modeWeatherWithForecast
	modeWeatherWithheld
)

func runRollingCausalBacktest(
	ctx context.Context,
	steps []backtestStep,
	startEval time.Time,
	mode backtestMode,
	siteLoadOnly bool,
) backtestMetrics {
	loc := resolveSiteLocation("Asia/Shanghai")
	byTS := make(map[int64]backtestStep, len(steps))
	var obsAll []Observation
	var wxAll []WeatherObservation
	wxByTS := make(map[int64]WeatherObservation, len(steps))

	for _, st := range steps {
		key := st.ts.UTC().Unix()
		byTS[key] = st
		if st.loadKW != nil {
			obsAll = append(obsAll, Observation{ObservedAt: st.ts.UTC(), Value: *st.loadKW})
		}
		if st.dryBulbC != nil || st.wetBulbC != nil {
			clean, ok := sanitizeWeatherObservation(WeatherObservation{
				ObservedAt: st.ts.UTC(),
				DryBulbC:   st.dryBulbC,
				WetBulbC:   st.wetBulbC,
				Source:     WeatherSourceSiteSensors,
			})
			if ok {
				wxAll = append(wxAll, clean)
				wxByTS[key] = clean
			}
		}
	}

	var sq15, sumErr15, sumAct15, peakAbsErr, novErr, novAct float64
	var n15, nPeak, nNov int
	var sq1h, sumAct1h float64
	var n1h int
	nov15 := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)

	firstOrigin := startEval.UTC().Truncate(time.Hour).Add(7 * time.Minute)
	lastTS := steps[len(steps)-1].ts.UTC()

	obsR, obsL := 0, 0
	wxR, wxL := 0, 0

	for origin := firstOrigin; !origin.After(lastTS.Add(-15 * time.Minute)); origin = origin.Add(time.Hour) {
		cutoff := origin.Add(-historyLookbackDays * 24 * time.Hour)
		if mode == modeBaseline7d || mode == modeBaselineOldPeriodEndBug {
			cutoff = origin.Add(-7 * 24 * time.Hour)
		}
		for obsR < len(obsAll) && !obsAll[obsR].ObservedAt.Add(15*time.Minute).After(origin) {
			obsR++
		}
		for obsL < obsR && obsAll[obsL].ObservedAt.Before(cutoff) {
			obsL++
		}
		for wxR < len(wxAll) && !wxAll[wxR].ObservedAt.After(origin) {
			wxR++
		}
		for wxL < wxR && wxAll[wxL].ObservedAt.Before(cutoff) {
			wxL++
		}
		if obsR-obsL < 96 {
			continue
		}
		obsSlice := obsAll[obsL:obsR]
		req := Request{
			TenantID: "01990000-3000-7000-8000-000000000001", SiteID: "01990000-5000-7000-8000-000000000001",
			SubjectType: "SITE", SubjectID: "01990000-5000-7000-8000-000000000001", Target: "SITE_LOAD",
			ForecastJobID: "01990000-1700-7000-8000-000000000001", ForecastSnapshotID: "01990000-1710-7000-8000-000000000001",
			DeploymentID: "01990000-1720-7000-8000-000000000001", ModelID: "01990000-1730-7000-8000-000000000001",
			ModelVersionID: "01990000-1740-7000-8000-000000000001", ModelVersion: 1,
			FeatureSetVersionID: "01990000-1750-7000-8000-000000000001", FeatureSetVersion: 1,
			InputSnapshotID: "01990000-1760-7000-8000-000000000001", TopologyVersionID: "01990000-1770-7000-8000-000000000001",
			ForecastOrigin: origin, HorizonMinutes: 1440, Granularity: "15MIN", Timezone: "Asia/Shanghai", Unit: "kW",
		}

		var pts []Point
		var err error
		switch mode {
		case modeBaselineOldPeriodEndBug:
			shifted := make([]Observation, len(obsSlice))
			for i, o := range obsSlice {
				shifted[i] = Observation{ObservedAt: o.ObservedAt.Add(15 * time.Minute), Value: o.Value}
			}
			req.Observations = shifted
			pts, err = BaselineEngine{}.Generate(ctx, req, origin)
		case modeBaseline7d:
			req.Observations = obsSlice
			pts, err = BaselineEngine{}.Generate(ctx, req, origin)
		case modeCalendarAR:
			req.Observations = obsSlice
			pts, err = (CalendarAREngine{}).Generate(ctx, req, origin)
		case modeWeatherCausalPersistence:
			req.Observations = obsSlice
			req.WeatherObservations = slices.Clone(wxAll[wxL:wxR])
			pts, err = (WeatherChangePointEngine{}).Generate(ctx, req, origin)
		case modeWeatherWithForecast:
			req.Observations = obsSlice
			wxCombined := slices.Clone(wxAll[wxL:wxR])
			firstFor := origin.Truncate(15 * time.Minute).Add(15 * time.Minute)
			for i := range 96 {
				fts := firstFor.Add(time.Duration(i) * 15 * time.Minute)
				if fw, ok := wxByTS[fts.Unix()]; ok {
					fw.Source = WeatherSourceOpenMeteo
					wxCombined = append(wxCombined, fw)
				}
			}
			req.WeatherObservations = wxCombined
			pts, err = (WeatherChangePointEngine{}).Generate(ctx, req, origin)
		case modeWeatherWithheld:
			req.Observations = obsSlice
			req.WeatherObservations = nil
			req.WeatherFallbackReason = "WEATHER_WITHHELD"
			pts, err = (WeatherChangePointEngine{}).Generate(ctx, req, origin)
		}
		if err != nil {
			continue
		}

		type pair struct{ pred, act float64 }
		hourBuckets := make(map[int64][]pair)
		for _, pt := range pts {
			fts := pt.ForecastFor.UTC()
			st, ok := byTS[fts.Unix()]
			if !ok {
				continue
			}
			actPtr := st.loadKW
			if siteLoadOnly {
				actPtr = st.siteLoad
			}
			if actPtr == nil {
				continue
			}
			act := *actPtr
			e := pt.Value - act
			sq15 += e * e
			sumErr15 += e
			sumAct15 += act
			n15++
			if hr := fts.In(loc).Hour(); hr >= 9 && hr < 21 {
				peakAbsErr += math.Abs(e)
				nPeak++
			}
			if !fts.Before(nov15) {
				novErr += e
				novAct += act
				nNov++
			}
			hk := fts.Truncate(time.Hour).Unix()
			hourBuckets[hk] = append(hourBuckets[hk], pair{pt.Value, act})
		}
		for _, pairs := range hourBuckets {
			if len(pairs) == 4 {
				var sp, sa float64
				for _, p := range pairs {
					sp += p.pred
					sa += p.act
				}
				mp, ma := sp/4.0, sa/4.0
				sq1h += (mp - ma) * (mp - ma)
				sumAct1h += ma
				n1h++
			}
		}
	}

	mean15 := sumAct15 / float64(n15)
	rmse15 := math.Sqrt(sq15 / float64(n15))
	mean1h := sumAct1h / float64(n1h)
	rmse1h := math.Sqrt(sq1h / float64(n1h))
	novBias := 0.0
	if nNov > 0 && novAct > 0 {
		novBias = 100.0 * (novErr / float64(nNov)) / (novAct / float64(nNov))
	}
	return backtestMetrics{
		Count15m:      n15,
		CVRMSE15mPct:  100.0 * rmse15 / mean15,
		NMBE15mPct:    100.0 * (sumErr15 / float64(n15)) / mean15,
		Peak0921MAEKW: peakAbsErr / float64(nPeak),
		CVRMSE1hPct:   100.0 * rmse1h / mean1h,
		LateNovBias:   novBias,
	}
}

func TestSyntheticMultiWeekCausalBacktest(t *testing.T) {
	// Deterministic 35-day multi-regime synthetic commercial chiller plant benchmark (no external files or network).
	loc := resolveSiteLocation("Asia/Shanghai")
	start := time.Date(2026, 9, 15, 0, 0, 0, 0, loc).UTC()
	var steps []backtestStep
	for i := range 35 * 96 {
		ts := start.Add(time.Duration(i) * 15 * time.Minute)
		cst := ts.In(loc)
		dayIdx := i / 96
		// Gradual autumn cooling + cold snap in last 10 days
		dailyTemp := 30.0 - 0.3*float64(dayIdx)
		if dayIdx >= 25 {
			dailyTemp -= 6.0
		}
		tDry := dailyTemp + 3.5*math.Sin(2*math.Pi*(float64(cst.Hour())-9)/24.0)
		tWB := tDry - 4.0
		load := 2.0
		if cst.Hour() >= 9 && cst.Hour() < 21 {
			dayMult := 1.0
			if classifyDay(ts, loc) != dayWorkday {
				dayMult = 1.35
			}
			load = (40.0 + 12.0*math.Max(0, tDry-15.0)) * dayMult
		}
		lCopy, tdCopy, twCopy := load, tDry, tWB
		steps = append(steps, backtestStep{ts: ts, loadKW: &lCopy, dryBulbC: &tdCopy, wetBulbC: &twCopy})
	}
	evalStart := start.Add(10 * 24 * time.Hour)
	mBase := runRollingCausalBacktest(t.Context(), steps, evalStart, modeBaseline7d, false)
	mCal := runRollingCausalBacktest(t.Context(), steps, evalStart, modeCalendarAR, false)
	mWx := runRollingCausalBacktest(t.Context(), steps, evalStart, modeWeatherWithForecast, false)
	mDegraded := runRollingCausalBacktest(t.Context(), steps, evalStart, modeWeatherWithheld, false)

	if mCal.CVRMSE15mPct >= mBase.CVRMSE15mPct {
		t.Fatalf("expected Calendar+AR (%.2f%%) to beat 7d Baseline (%.2f%%)", mCal.CVRMSE15mPct, mBase.CVRMSE15mPct)
	}
	if mWx.CVRMSE15mPct >= mCal.CVRMSE15mPct {
		t.Fatalf("expected Weather+ChangePoint (%.2f%%) to beat Calendar+AR (%.2f%%)", mWx.CVRMSE15mPct, mCal.CVRMSE15mPct)
	}
	if mDegraded.CVRMSE15mPct >= mBase.CVRMSE15mPct {
		t.Fatalf("expected degraded Weather+ChangePoint without weather (%.2f%%) to beat 7d Baseline (%.2f%%)", mDegraded.CVRMSE15mPct, mBase.CVRMSE15mPct)
	}
}

func TestSongyaxuanCausalBacktest(t *testing.T) {
	path := os.Getenv("HVAC_SYX_SERIES_PATH")
	if path == "" {
		t.Skip("HVAC_SYX_SERIES_PATH is not set")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var dataset songyaxuanDataset
	if err := json.Unmarshal(raw, &dataset); err != nil {
		t.Fatal(err)
	}
	steps := make([]backtestStep, 0, len(dataset.Series15m))
	for _, r := range dataset.Series15m {
		ts, parseErr := time.Parse(time.RFC3339, r.T)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		load := r.SiteLoadKW
		if load == nil {
			load = r.MeterTotalPowerKW
		}
		steps = append(steps, backtestStep{
			ts:       ts.UTC(),
			loadKW:   load,
			siteLoad: r.SiteLoadKW,
			dryBulbC: r.OutdoorDryBulbT15C,
			wetBulbC: r.OutdoorWetBulbC,
		})
	}

	fullStart := steps[0].ts.Add(7 * 24 * time.Hour)
	siteLoadStart := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)

	candidates := []struct {
		label string
		mode  backtestMode
	}{
		{"A_old_bug (PeriodEnd shift)", modeBaselineOldPeriodEndBug},
		{"A_fixed (BaselineEngine 7d)", modeBaseline7d},
		{"B (Calendar+AR)", modeCalendarAR},
		{"C (Weather+CP causal persistence)", modeWeatherCausalPersistence},
		{"C (Weather+CP w/ forecast wx)", modeWeatherWithForecast},
		{"C (Weather+CP degraded no-weather)", modeWeatherWithheld},
	}

	for _, scope := range []struct {
		title        string
		start        time.Time
		siteLoadOnly bool
	}{
		{"Full Range (2026-09-21..11-27, stitched siteLoad/meterTotal)", fullStart, false},
		{"Production siteLoadKw Range (2026-10-08..11-27)", siteLoadStart, true},
	} {
		t.Logf("=== %s ===", scope.title)
		for _, c := range candidates {
			m := runRollingCausalBacktest(t.Context(), steps, scope.start, c.mode, scope.siteLoadOnly)
			t.Logf("%-36s | n=%6d | 15m_CV=%5.2f%% | NMBE=%+6.2f%% | 09-21_MAE=%5.2f kW | 1h_CV=%5.2f%% | LateNov_Bias=%+6.2f%%",
				c.label, m.Count15m, m.CVRMSE15mPct, m.NMBE15mPct, m.Peak0921MAEKW, m.CVRMSE1hPct, m.LateNovBias)
		}
	}
}
