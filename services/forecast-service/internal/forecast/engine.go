package forecast

import (
	"context"
	"math"
	"time"
)

const (
	modelFamilyBaseline           = "BASELINE_7D"
	modelFamilyCalendarAR         = "CALENDAR_AR"
	modelFamilyWeatherChangePoint = "WEATHER_CHANGE_POINT"

	weatherModeNone          = "NONE"
	weatherModeSiteCausal    = "SITE_CAUSAL_PERSISTENCE"
	weatherModeForecastNWP   = "FORECAST_WEATHER"
	weatherModeSiteAndNWP    = "SITE_AND_FORECAST_WEATHER"
	alignmentModePeriodStart = "PERIOD_START"

	calendarTauDays        = 7.0
	weatherFitTauDays      = 14.0
	dayTypeShrinkPrior     = 0.50
	holidayShrinkPrior     = 0.25
	activeBiasHalfLifeH    = 24.0
	activeLevelDecayH      = 96.0
	activeBiasPriorHours   = 4.0
	shortARHalfLifeH       = 2.5
	shortAREWMAHalfLifeH   = 0.75
	changePointCoolLowC    = 15.0
	changePointCoolHighC   = 24.0
	changePointWetBulbC    = 14.0
	weatherRidgeLambda     = 25.0
	minWeatherFitWeight    = 32.0
	minDistinctWetBulbN    = 16
	maxWeatherBetaSlope    = 30.0
	maxScaleShift          = 0.80
	weatherDiurnalLookback = 5 * 24 * time.Hour
	weatherDiurnalMaxAge   = 12 * time.Hour
	weatherAnomalyDecayH   = 30.0
)

// ExecutionMetadata describes the model and weather conditions used during a forecast run.
type ExecutionMetadata struct {
	ModelFamily           string `json:"modelFamily,omitempty"`
	WeatherMode           string `json:"weatherMode,omitempty"`
	WeatherSource         string `json:"weatherSource,omitempty"`
	WeatherFallbackReason string `json:"weatherFallbackReason,omitempty"`
	AlignedTo             string `json:"alignedTo,omitempty"`
}

// MetadataForecastEngine is implemented by engines that publish structured model/weather metadata.
type MetadataForecastEngine interface {
	ForecastEngine
	GenerateWithMetadata(context.Context, Request, time.Time) ([]Point, ExecutionMetadata, error)
}

// CalendarAREngine implements Candidate B: weekday/weekend/CN-holiday recency-weighted
// slot profile combined with active-regime level scaling and damped short-lag residual AR.
type CalendarAREngine struct{}

func (e CalendarAREngine) Generate(ctx context.Context, request Request, generatedAt time.Time) ([]Point, error) {
	points, _, err := e.GenerateWithMetadata(ctx, request, generatedAt)
	return points, err
}

func (CalendarAREngine) GenerateWithMetadata(_ context.Context, request Request, generatedAt time.Time) ([]Point, ExecutionMetadata, error) {
	return runStatisticalEngine(request, generatedAt, false)
}

// WeatherChangePointEngine implements Candidate C: Calendar+AR augmented with activity-gated
// piecewise-linear outdoor dry-bulb and wet-bulb change-point ridge regression, Stull wet-bulb
// estimation, and graceful fallback to Calendar+AR when weather data is unavailable.
type WeatherChangePointEngine struct{}

func (e WeatherChangePointEngine) Generate(ctx context.Context, request Request, generatedAt time.Time) ([]Point, error) {
	points, _, err := e.GenerateWithMetadata(ctx, request, generatedAt)
	return points, err
}

func (WeatherChangePointEngine) GenerateWithMetadata(_ context.Context, request Request, generatedAt time.Time) ([]Point, ExecutionMetadata, error) {
	return runStatisticalEngine(request, generatedAt, true)
}

type calendarProfile struct {
	slotsPerDay int
	minutes     int
	slotDur     time.Duration
	loc         *time.Location
	splitDay    bool
	meanAll     []float64
	meanRegular []float64
	meanType    [3][]float64
	weightType  [3][]float64
	pMin        float64
	span        float64
	activity    []float64
}

func buildCalendarProfile(observations []Observation, origin time.Time, loc *time.Location, minutes int, tauDays float64, splitDay bool) calendarProfile {
	slotsPerDay := 1440 / minutes
	slotDur := time.Duration(minutes) * time.Minute
	anchorAll := make([]float64, slotsPerDay)
	hasAnchorAll := make([]bool, slotsPerDay)
	deltaSumAll := make([]float64, slotsPerDay)
	weightAll := make([]float64, slotsPerDay)

	var anchorType [3][]float64
	var hasAnchorType [3][]bool
	var deltaSumType [3][]float64
	var weightType [3][]float64
	for k := range 3 {
		anchorType[k] = make([]float64, slotsPerDay)
		hasAnchorType[k] = make([]bool, slotsPerDay)
		deltaSumType[k] = make([]float64, slotsPerDay)
		weightType[k] = make([]float64, slotsPerDay)
	}
	cutoff := origin.Add(-historyLookbackDays * 24 * time.Hour)
	for _, obs := range observations {
		if obs.ObservedAt.Before(cutoff) || obs.ObservedAt.After(origin) {
			continue
		}
		ageDays := math.Max(0, origin.Sub(obs.ObservedAt).Hours()/24.0)
		w := math.Exp(-ageDays / tauDays)
		s := localSlotIndex(obs.ObservedAt, loc, minutes)
		if !hasAnchorAll[s] {
			anchorAll[s] = obs.Value
			hasAnchorAll[s] = true
		}
		deltaSumAll[s] += w * (obs.Value - anchorAll[s])
		weightAll[s] += w
		if splitDay {
			dk := classifyDay(obs.ObservedAt, loc)
			if !hasAnchorType[dk][s] {
				anchorType[dk][s] = obs.Value
				hasAnchorType[dk][s] = true
			}
			deltaSumType[dk][s] += w * (obs.Value - anchorType[dk][s])
			weightType[dk][s] += w
		}
	}

	var globalSum, globalWeight float64
	meanAll := make([]float64, slotsPerDay)
	for s := range slotsPerDay {
		if weightAll[s] > 0 {
			meanAll[s] = anchorAll[s] + deltaSumAll[s]/weightAll[s]
			globalSum += weightAll[s] * meanAll[s]
			globalWeight += weightAll[s]
		}
	}
	globalMean := 0.0
	if globalWeight > 0 {
		globalMean = globalSum / globalWeight
	}
	var meanType [3][]float64
	for k := range 3 {
		meanType[k] = make([]float64, slotsPerDay)
	}
	meanRegular := make([]float64, slotsPerDay)
	for s := range slotsPerDay {
		if weightAll[s] == 0 {
			meanAll[s] = globalMean
		}
		for k := range 3 {
			if weightType[k][s] > 0 {
				meanType[k][s] = anchorType[k][s] + deltaSumType[k][s]/weightType[k][s]
			}
		}
		regW := weightType[dayWorkday][s] + weightType[dayWeekend][s]
		if regW > 0 {
			meanRegular[s] = blendAnchored(meanType[dayWorkday][s], weightType[dayWorkday][s], meanType[dayWeekend][s], weightType[dayWeekend][s])
		} else {
			meanRegular[s] = meanAll[s]
		}
	}
	pMin, pMax := meanAll[0], meanAll[0]
	for _, v := range meanAll[1:] {
		if v < pMin {
			pMin = v
		}
		if v > pMax {
			pMax = v
		}
	}
	span := math.Max(1.0, pMax-pMin)
	activity := make([]float64, slotsPerDay)
	for s, v := range meanAll {
		activity[s] = math.Max(0, math.Min(1.0, (v-pMin)/span))
	}
	return calendarProfile{
		slotsPerDay: slotsPerDay,
		minutes:     minutes,
		slotDur:     slotDur,
		loc:         loc,
		splitDay:    splitDay,
		meanAll:     meanAll,
		meanRegular: meanRegular,
		meanType:    meanType,
		weightType:  weightType,
		pMin:        pMin,
		span:        span,
		activity:    activity,
	}
}

func blendAnchored(primary float64, primaryW float64, fallback float64, priorW float64) float64 {
	if primaryW <= 0 {
		return fallback
	}
	if priorW <= 0 || primary == fallback {
		return primary
	}
	return (primaryW*primary + priorW*fallback) / (primaryW + priorW)
}

func (p calendarProfile) predictSlot(at time.Time) float64 {
	s := localSlotIndex(at, p.loc, p.minutes)
	mAll := p.meanAll[s]
	if !p.splitDay {
		return mAll
	}
	mReg := p.meanRegular[s]
	mWork := blendAnchored(p.meanType[dayWorkday][s], p.weightType[dayWorkday][s], mReg, dayTypeShrinkPrior)
	mWeekend := blendAnchored(p.meanType[dayWeekend][s], p.weightType[dayWeekend][s], mReg, dayTypeShrinkPrior)
	switch classifyDay(at, p.loc) {
	case dayWorkday:
		return mWork
	case dayWeekend:
		return mWeekend
	default:
		return blendAnchored(p.meanType[dayHoliday][s], p.weightType[dayHoliday][s], mWeekend, holidayShrinkPrior)
	}
}

func (p calendarProfile) meanLoad() float64 {
	var total float64
	for _, v := range p.meanAll {
		total += v
	}
	return total / float64(len(p.meanAll))
}

type weatherSample struct {
	dryC       float64
	wetC       float64
	hasDry     bool
	hasWet     bool
	source     string
	observedAt time.Time
}

type weatherRegressionModel struct {
	enabled       bool
	hasDistinctWB bool
	beta          [3]float64
	c1Bar         []float64
	c2Bar         []float64
	wbBar         []float64
	pastBySlot    map[int64]weatherSample
	futureBySlot  map[int64]weatherSample
	diurnalDry    []float64
	hasDiurnalDry []bool
	latestDryC    float64
	latestDryAt   time.Time
	latestDryAnom float64
	hasLatestDry  bool
	hasFutureNWP  bool
	hasPastSiteWx bool
	loc           *time.Location
	minutes       int
	slotDur       time.Duration
}

func weatherFeatures(dryC, wetC float64, hasWet bool) (float64, float64, float64) {
	c1 := math.Max(0, dryC-changePointCoolLowC)
	c2 := math.Max(0, dryC-changePointCoolHighC)
	wb := 0.0
	if hasWet {
		wb = math.Max(0, wetC-changePointWetBulbC)
	}
	return c1, c2, wb
}

func fitWeatherModel(
	observations []Observation,
	wxInput []WeatherObservation,
	origin time.Time,
	loc *time.Location,
	minutes int,
	calRecent calendarProfile,
) weatherRegressionModel {
	slotDur := time.Duration(minutes) * time.Minute
	slotsPerDay := calRecent.slotsPerDay
	model := weatherRegressionModel{
		c1Bar:         make([]float64, slotsPerDay),
		c2Bar:         make([]float64, slotsPerDay),
		wbBar:         make([]float64, slotsPerDay),
		pastBySlot:    make(map[int64]weatherSample),
		futureBySlot:  make(map[int64]weatherSample),
		diurnalDry:    make([]float64, slotsPerDay),
		hasDiurnalDry: make([]bool, slotsPerDay),
		loc:           loc,
		minutes:       minutes,
		slotDur:       slotDur,
	}
	if len(wxInput) == 0 {
		return model
	}

	for _, raw := range wxInput {
		clean, ok := sanitizeWeatherObservation(raw)
		if !ok || clean.DryBulbC == nil {
			continue
		}
		key := clean.ObservedAt.Truncate(slotDur).Unix()
		sample := weatherSample{
			dryC:       *clean.DryBulbC,
			hasDry:     true,
			source:     clean.Source,
			observedAt: time.Unix(key, 0).UTC(),
		}
		if clean.WetBulbC != nil {
			sample.wetC = *clean.WetBulbC
			sample.hasWet = true
		}
		if !sample.observedAt.After(origin) {
			model.pastBySlot[key] = sample
			model.hasPastSiteWx = true
		} else {
			model.futureBySlot[key] = sample
			model.hasFutureNWP = true
		}
	}
	if len(model.pastBySlot) == 0 {
		return model
	}

	calLong := buildCalendarProfile(observations, origin, loc, minutes, weatherFitTauDays, calRecent.splitDay)

	// Build recency-weighted weather baselines (for calRecent alignment and calLong fitting).
	c1SumCal := make([]float64, slotsPerDay)
	c2SumCal := make([]float64, slotsPerDay)
	wbSumCal := make([]float64, slotsPerDay)
	wSumCal := make([]float64, slotsPerDay)

	c1SumFit := make([]float64, slotsPerDay)
	c2SumFit := make([]float64, slotsPerDay)
	wbSumFit := make([]float64, slotsPerDay)
	wSumFit := make([]float64, slotsPerDay)

	type pairedSample struct {
		obs  Observation
		slot int
		wFit float64
		c1   float64
		c2   float64
		wbc  float64
	}
	cutoff := origin.Add(-historyLookbackDays * 24 * time.Hour)
	var pairs []pairedSample
	distinctWBCount := 0

	for _, obs := range observations {
		if obs.ObservedAt.Before(cutoff) || obs.ObservedAt.After(origin) {
			continue
		}
		key := obs.ObservedAt.Truncate(slotDur).Unix()
		wx, ok := model.pastBySlot[key]
		if !ok || !wx.hasDry {
			continue
		}
		if wx.hasWet && math.Abs(wx.wetC-wx.dryC) > 0.2 {
			distinctWBCount++
		}
		ageDays := math.Max(0, origin.Sub(obs.ObservedAt).Hours()/24.0)
		wCal := math.Exp(-ageDays / calendarTauDays)
		wFit := math.Exp(-ageDays / weatherFitTauDays)
		s := localSlotIndex(obs.ObservedAt, loc, minutes)
		c1, c2, wbc := weatherFeatures(wx.dryC, wx.wetC, wx.hasWet)

		c1SumCal[s] += wCal * c1
		c2SumCal[s] += wCal * c2
		wbSumCal[s] += wCal * wbc
		wSumCal[s] += wCal

		c1SumFit[s] += wFit * c1
		c2SumFit[s] += wFit * c2
		wbSumFit[s] += wFit * wbc
		wSumFit[s] += wFit

		pairs = append(pairs, pairedSample{obs: obs, slot: s, wFit: wFit, c1: c1, c2: c2, wbc: wbc})
	}
	if len(pairs) == 0 {
		return model
	}

	model.c1Bar, model.c2Bar, model.wbBar = normalizeSlotBaselines(c1SumCal, c2SumCal, wbSumCal, wSumCal)
	c1FitBar, c2FitBar, wbFitBar := normalizeSlotBaselines(c1SumFit, c2SumFit, wbSumFit, wSumFit)
	model.hasDistinctWB = distinctWBCount >= minDistinctWetBulbN

	dim := 2
	if model.hasDistinctWB {
		dim = 3
	}
	var xtx [3][3]float64
	var xty [3]float64
	var effN float64

	for _, pair := range pairs {
		act := calRecent.activity[pair.slot]
		if act < 0.08 {
			continue
		}
		x := [3]float64{
			act * (pair.c1 - c1FitBar[pair.slot]),
			act * (pair.c2 - c2FitBar[pair.slot]),
			0,
		}
		if dim == 3 {
			x[2] = act * (pair.wbc - wbFitBar[pair.slot])
		}
		yRes := pair.obs.Value - calLong.predictSlot(pair.obs.ObservedAt)
		effN += pair.wFit
		for i := range dim {
			xty[i] += pair.wFit * x[i] * yRes
			for j := range dim {
				xtx[i][j] += pair.wFit * x[i] * x[j]
			}
		}
	}

	if effN >= minWeatherFitWeight {
		for i := range dim {
			xtx[i][i] += weatherRidgeLambda
		}
		solved := solveLinear3x3(xtx, xty, dim)
		// Physical cooling monotonicity bounds: non-negative slope with bounded sensitivity.
		solved[0] = math.Max(0, math.Min(maxWeatherBetaSlope, solved[0]))
		solved[1] = math.Max(-0.4*solved[0], math.Min(maxWeatherBetaSlope, solved[1]))
		if dim == 3 {
			solved[2] = math.Max(0, math.Min(maxWeatherBetaSlope, solved[2]))
		}
		model.beta = solved
		model.enabled = true
	}

	// Build causal diurnal dry-bulb persistence profile from the past 5 days.
	tdSum := make([]float64, slotsPerDay)
	tdW := make([]float64, slotsPerDay)
	diurnalCutoff := origin.Add(-weatherDiurnalLookback)
	for _, wx := range model.pastBySlot {
		if !wx.hasDry || wx.observedAt.Before(diurnalCutoff) || wx.observedAt.After(origin) {
			continue
		}
		ageDays := origin.Sub(wx.observedAt).Hours() / 24.0
		w := math.Exp(-ageDays / 2.0)
		s := localSlotIndex(wx.observedAt, loc, minutes)
		tdSum[s] += w * wx.dryC
		tdW[s] += w
		if !model.hasLatestDry || wx.observedAt.After(model.latestDryAt) {
			model.hasLatestDry = true
			model.latestDryAt = wx.observedAt
			model.latestDryC = wx.dryC
		}
	}
	for s := range slotsPerDay {
		if tdW[s] > 0 {
			model.diurnalDry[s] = tdSum[s] / tdW[s]
			model.hasDiurnalDry[s] = true
		} else if model.hasLatestDry {
			model.diurnalDry[s] = model.latestDryC
			model.hasDiurnalDry[s] = true
		}
	}
	if model.hasLatestDry && origin.Sub(model.latestDryAt) <= 6*time.Hour {
		sLat := localSlotIndex(model.latestDryAt, loc, minutes)
		if model.hasDiurnalDry[sLat] {
			model.latestDryAnom = model.latestDryC - model.diurnalDry[sLat]
		}
	}
	return model
}

func normalizeSlotBaselines(c1Sum, c2Sum, wbSum, wSum []float64) ([]float64, []float64, []float64) {
	n := len(wSum)
	var totW, totC1, totC2, totWB float64
	for i := range n {
		totW += wSum[i]
		totC1 += c1Sum[i]
		totC2 += c2Sum[i]
		totWB += wbSum[i]
	}
	var g1, g2, gw float64
	if totW > 0 {
		g1, g2, gw = totC1/totW, totC2/totW, totWB/totW
	}
	b1 := make([]float64, n)
	b2 := make([]float64, n)
	bw := make([]float64, n)
	for i := range n {
		denom := wSum[i] + 0.5
		b1[i] = (c1Sum[i] + 0.5*g1) / denom
		b2[i] = (c2Sum[i] + 0.5*g2) / denom
		bw[i] = (wbSum[i] + 0.5*gw) / denom
	}
	return b1, b2, bw
}

func (m weatherRegressionModel) adjustment(at time.Time, wx weatherSample, activity []float64) float64 {
	if !m.enabled || !wx.hasDry {
		return 0
	}
	s := localSlotIndex(at, m.loc, m.minutes)
	act := activity[s]
	if act < 0.05 {
		return 0
	}
	c1, c2, wbc := weatherFeatures(wx.dryC, wx.wetC, wx.hasWet)
	adj := act * (m.beta[0]*(c1-m.c1Bar[s]) + m.beta[1]*(c2-m.c2Bar[s]))
	if m.hasDistinctWB {
		adj += act * m.beta[2] * (wbc - m.wbBar[s])
	}
	return adj
}

func (m weatherRegressionModel) futureSample(at time.Time, origin time.Time) (weatherSample, bool) {
	key := at.UTC().Truncate(m.slotDur).Unix()
	if wx, ok := m.futureBySlot[key]; ok && wx.hasDry {
		return wx, true
	}
	if m.hasLatestDry && origin.Sub(m.latestDryAt) <= weatherDiurnalMaxAge {
		s := localSlotIndex(at, m.loc, m.minutes)
		if m.hasDiurnalDry[s] {
			hours := math.Max(0, at.Sub(m.latestDryAt).Hours())
			tdPred := m.diurnalDry[s] + m.latestDryAnom*math.Exp(-hours/weatherAnomalyDecayH)
			return weatherSample{dryC: tdPred, hasDry: true, observedAt: at}, true
		}
	}
	return weatherSample{}, false
}

func runStatisticalEngine(request Request, generatedAt time.Time, useWeather bool) ([]Point, ExecutionMetadata, error) {
	meta := ExecutionMetadata{
		ModelFamily:           modelFamilyCalendarAR,
		WeatherMode:           weatherModeNone,
		WeatherSource:         request.WeatherSource,
		WeatherFallbackReason: request.WeatherFallbackReason,
		AlignedTo:             alignmentModePeriodStart,
	}
	if meta.WeatherSource == "" {
		meta.WeatherSource = WeatherSourceNone
	}
	if len(request.Observations) < 4 {
		if useWeather {
			meta.ModelFamily = modelFamilyWeatherChangePoint
		}
		return generateLastObservationFallback(request, generatedAt), meta, nil
	}
	minutes, err := granularityMinutes(request.Granularity)
	if err != nil {
		return nil, meta, err
	}
	slotsPerDay := 1440 / minutes
	origin := request.ForecastOrigin.UTC()
	if len(request.Observations) < slotsPerDay {
		trend, rmse, ok := linearTrend(request.Observations)
		if !ok {
			return generateLastObservationFallback(request, generatedAt), meta, nil
		}
		nonNegative := request.Target == "SITE_LOAD" || request.Target == "PV_GENERATION"
		points := generatePoints(request, generatedAt, "VALID", func(forecastFor time.Time) (float64, *float64, *float64) {
			value := trend(forecastFor)
			lower, upper := value-1.96*rmse, value+1.96*rmse
			if nonNegative {
				value, lower = math.Max(0, value), math.Max(0, lower)
			}
			return value, &lower, &upper
		})
		return points, meta, nil
	}

	loc := resolveSiteLocation(request.Timezone)
	splitDay := request.Target == "SITE_LOAD"
	cal := buildCalendarProfile(request.Observations, origin, loc, minutes, calendarTauDays, splitDay)

	var wxModel weatherRegressionModel
	if useWeather && splitDay {
		meta.ModelFamily = modelFamilyWeatherChangePoint
		wxModel = fitWeatherModel(request.Observations, request.WeatherObservations, origin, loc, minutes, cal)
		switch {
		case wxModel.enabled && wxModel.hasFutureNWP:
			if wxModel.hasPastSiteWx {
				meta.WeatherMode = weatherModeSiteAndNWP
			} else {
				meta.WeatherMode = weatherModeForecastNWP
			}
		case wxModel.enabled && wxModel.hasPastSiteWx:
			meta.WeatherMode = weatherModeSiteCausal
		default:
			meta.WeatherMode = weatherModeNone
			if meta.WeatherFallbackReason == "" {
				meta.WeatherFallbackReason = "INSUFFICIENT_PAIRED_WEATHER_HISTORY"
			}
		}
	}

	// Estimate active-regime level shift (last 96h) and short-lag EWMA residual (last 6h).
	var levelNum, levelDen float64
	type recentObs struct {
		ageH     float64
		value    float64
		basePred float64
	}
	var recent []recentObs
	var residSqSum, residW float64
	slotSqSum := make([]float64, slotsPerDay)
	slotWSum := make([]float64, slotsPerDay)

	cutoffRes := origin.Add(-14 * 24 * time.Hour)
	cutoffLevel := origin.Add(-96 * time.Hour)
	for _, obs := range request.Observations {
		if obs.ObservedAt.After(origin) {
			continue
		}
		base := cal.predictSlot(obs.ObservedAt)
		if wxModel.enabled {
			key := obs.ObservedAt.Truncate(cal.slotDur).Unix()
			if wx, ok := wxModel.pastBySlot[key]; ok {
				base = math.Max(0, base+wxModel.adjustment(obs.ObservedAt, wx, cal.activity))
			}
		}
		if !obs.ObservedAt.Before(cutoffRes) {
			r := obs.Value - base
			ageDays := origin.Sub(obs.ObservedAt).Hours() / 24.0
			w := math.Exp(-ageDays / 7.0)
			s := localSlotIndex(obs.ObservedAt, loc, minutes)
			residSqSum += w * r * r
			residW += w
			slotSqSum[s] += w * r * r
			slotWSum[s] += w
		}
		if !obs.ObservedAt.Before(cutoffLevel) {
			ageH := origin.Sub(obs.ObservedAt).Hours()
			if ageH <= 6.0 {
				recent = append(recent, recentObs{ageH: ageH, value: obs.Value, basePred: base})
			}
			if base > cal.pMin+0.10*cal.span {
				wb := math.Exp(-ageH / activeBiasHalfLifeH)
				levelNum += wb * (obs.Value - base)
				levelDen += wb * base
			}
		}
	}

	meanLoad := math.Max(10.0, cal.meanLoad())
	scaleShift := 0.0
	if levelDen > 0 {
		scaleShift = levelNum / (levelDen + activeBiasPriorHours*meanLoad)
		scaleShift = math.Max(-maxScaleShift, math.Min(maxScaleShift, scaleShift))
	}

	var rNum, rDen float64
	for _, item := range recent {
		w := math.Exp(-item.ageH / shortAREWMAHalfLifeH)
		r := item.value - math.Max(0, item.basePred*(1.0+scaleShift))
		rNum += w * r
		rDen += w
	}
	initResid := 0.0
	if rDen > 0 {
		initResid = rNum / rDen
	}

	globalSpread := 0.0
	if residW > 0 {
		globalSpread = math.Sqrt(residSqSum / residW)
	}
	nonNegative := request.Target == "SITE_LOAD" || request.Target == "PV_GENERATION"
	points := generatePoints(request, generatedAt, "VALID", func(forecastFor time.Time) (float64, *float64, *float64) {
		h := math.Max(0, forecastFor.Sub(origin).Hours())
		s := localSlotIndex(forecastFor, loc, minutes)
		base := cal.predictSlot(forecastFor)
		if wxModel.enabled {
			if wx, ok := wxModel.futureSample(forecastFor, origin); ok {
				base = math.Max(0, base+wxModel.adjustment(forecastFor, wx, cal.activity))
			}
		}
		levelFactor := 1.0 + scaleShift*math.Exp(-h/activeLevelDecayH)
		adjBase := math.Max(0, base*levelFactor)
		shortAR := initResid * math.Exp(-h/shortARHalfLifeH) * (0.2 + 0.8*cal.activity[s])
		value := adjBase + shortAR

		spread := globalSpread
		if slotWSum[s] > 0 {
			slotSpread := math.Sqrt((slotSqSum[s] + 0.5*globalSpread*globalSpread) / (slotWSum[s] + 0.5))
			spread = slotSpread
		}
		// Uncertainty grows gently with horizon while preserving tight near-term AR bands.
		horizonScale := 1.0 - 0.35*math.Exp(-h/3.0)
		halfBand := 1.96 * spread * horizonScale
		lower, upper := value-halfBand, value+halfBand
		if nonNegative {
			value = math.Max(0, value)
			lower = math.Max(0, lower)
			upper = math.Max(value, upper)
		}
		return value, &lower, &upper
	})
	return points, meta, nil
}

func solveLinear3x3(a [3][3]float64, b [3]float64, dim int) [3]float64 {
	var m [3][4]float64
	for i := range dim {
		for j := range dim {
			m[i][j] = a[i][j]
		}
		m[i][dim] = b[i]
	}
	for col := range dim {
		piv := col
		for r := col + 1; r < dim; r++ {
			if math.Abs(m[r][col]) > math.Abs(m[piv][col]) {
				piv = r
			}
		}
		m[col], m[piv] = m[piv], m[col]
		if math.Abs(m[col][col]) < 1e-12 {
			continue
		}
		inv := 1.0 / m[col][col]
		for j := col; j <= dim; j++ {
			m[col][j] *= inv
		}
		for r := range dim {
			if r == col {
				continue
			}
			factor := m[r][col]
			for j := col; j <= dim; j++ {
				m[r][j] -= factor * m[col][j]
			}
		}
	}
	var out [3]float64
	for i := range dim {
		out[i] = m[i][dim]
	}
	return out
}
