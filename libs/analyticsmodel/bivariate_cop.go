package analyticsmodel

import (
	"cmp"
	"math"
	"slices"
)

// ControlTickMode identifies the control regime active during an optimization control tick.
type ControlTickMode string

const (
	ModeAIClosedLoop   ControlTickMode = "AI_CLOSED_LOOP"
	ModeAIShadow       ControlTickMode = "AI_SHADOW"
	ModeRuleBaseline   ControlTickMode = "RULE_BASELINE"
	ModeManualOverride ControlTickMode = "MANUAL_OVERRIDE"
)

const (
	defaultTickDurationHours = 0.25
	defaultWetBulbStepC      = 2.0
	defaultCoolingLoadStepKW = 200.0
	defaultMinSamplesPerMode = 1
	maxValidCOP              = 25.0
	minValidChillerPowerKW   = 0.5
)

// ControlTickSample is one control-interval plant observation used to compare closed-loop AI
// performance against baseline control under matched weather and cooling load conditions.
type ControlTickSample struct {
	Mode            ControlTickMode `json:"mode"`
	OutdoorWetBulbC *float64        `json:"outdoorWetBulbC"`
	CoolingLoadKW   *float64        `json:"coolingLoadKW"`
	ChillerPowerKW  *float64        `json:"chillerPowerKW"`
	ObservedCOP     *float64        `json:"observedCOP"`
	DurationHours   float64         `json:"durationHours"`
}

// BivariateBinConfig controls outdoor wet-bulb and cooling-load discretization and per-bin
// minimum sample thresholds.
type BivariateBinConfig struct {
	WetBulbStepC         float64 `json:"wetBulbStepC"`
	CoolingLoadStepKW    float64 `json:"coolingLoadStepKW"`
	MinSamplesPerModeBin int     `json:"minSamplesPerModeBin"`
}

// BivariateCOPCell summarizes one matched (wet-bulb, cooling-load) bin comparing AI closed-loop
// operation against baseline operation.
type BivariateCOPCell struct {
	WetBulbMinC       float64 `json:"wetBulbMinC"`
	WetBulbMaxC       float64 `json:"wetBulbMaxC"`
	CoolingLoadMinKW  float64 `json:"coolingLoadMinKW"`
	CoolingLoadMaxKW  float64 `json:"coolingLoadMaxKW"`
	BaselineSamples   int     `json:"baselineSamples"`
	AISamples         int     `json:"aiSamples"`
	BaselineCOP       float64 `json:"baselineCOP"`
	AICOP             float64 `json:"aiCOP"`
	COPUpliftPct      float64 `json:"copUpliftPct"`
	EstimatedSavedKWh float64 `json:"estimatedSavedKWh"`
}

// BivariateCOPSummary reports weather- and load-normalized COP uplift and estimated electricity
// savings across all matched bivariate operating bins.
type BivariateCOPSummary struct {
	MatchedBinCount        int                `json:"matchedBinCount"`
	MatchedAISamples       int                `json:"matchedAISamples"`
	MatchedBaselineSamples int                `json:"matchedBaselineSamples"`
	WeightedBaselineCOP    *float64           `json:"weightedBaselineCOP"`
	WeightedAICOP          *float64           `json:"weightedAICOP"`
	WeightedCOPUpliftPct   *float64           `json:"weightedCOPUpliftPct"`
	TotalEstimatedSavedKWh float64            `json:"totalEstimatedSavedKWh"`
	Cells                  []BivariateCOPCell `json:"cells"`
}

type bivariateBinKey struct {
	wbBucket   float64
	loadBucket float64
}

type modeAccumulator struct {
	samples    int
	coolingKWh float64
	elecKWh    float64
}

type bivariateBinAccumulator struct {
	baseline modeAccumulator
	ai       modeAccumulator
}

// EvaluateBivariateCOP compares AI closed-loop operation against rule/manual baseline operation
// within matched (outdoor wet-bulb, cooling load) bins. AI_SHADOW ticks are excluded from the
// comparison, and unmatched bins are discarded so weather/load shifts do not bias COP uplift.
func EvaluateBivariateCOP(samples []ControlTickSample, cfg BivariateBinConfig) BivariateCOPSummary {
	wbStep, loadStep, minSamples := normalizeBinConfig(cfg)
	bins := accumulateBivariateBins(samples, wbStep, loadStep)

	cells := make([]BivariateCOPCell, 0, len(bins))
	summary := BivariateCOPSummary{Cells: cells}

	var totalAICoolingKWh, totalAIElecKWh, totalCounterfactualBaseElecKWh, totalSavedKWh float64

	for key, acc := range bins {
		if acc.baseline.samples < minSamples || acc.ai.samples < minSamples {
			continue
		}
		baseCOP := acc.baseline.coolingKWh / acc.baseline.elecKWh
		aiCOP := acc.ai.coolingKWh / acc.ai.elecKWh
		upliftPct := ((aiCOP - baseCOP) / baseCOP) * 100.0
		counterfactualBaseElecKWh := acc.ai.coolingKWh / baseCOP
		savedKWh := counterfactualBaseElecKWh - acc.ai.elecKWh

		wbMin := key.wbBucket * wbStep
		loadMin := key.loadBucket * loadStep

		cells = append(cells, BivariateCOPCell{
			WetBulbMinC:       roundScale(wbMin, 1e6),
			WetBulbMaxC:       roundScale(wbMin+wbStep, 1e6),
			CoolingLoadMinKW:  roundScale(loadMin, 1e6),
			CoolingLoadMaxKW:  roundScale(loadMin+loadStep, 1e6),
			BaselineSamples:   acc.baseline.samples,
			AISamples:         acc.ai.samples,
			BaselineCOP:       roundScale(baseCOP, 1e4),
			AICOP:             roundScale(aiCOP, 1e4),
			COPUpliftPct:      roundScale(upliftPct, 1e4),
			EstimatedSavedKWh: roundScale(savedKWh, 1e4),
		})

		summary.MatchedBaselineSamples += acc.baseline.samples
		summary.MatchedAISamples += acc.ai.samples
		totalAICoolingKWh += acc.ai.coolingKWh
		totalAIElecKWh += acc.ai.elecKWh
		totalCounterfactualBaseElecKWh += counterfactualBaseElecKWh
		totalSavedKWh += savedKWh
	}

	slices.SortFunc(cells, func(a, b BivariateCOPCell) int {
		if c := cmp.Compare(a.WetBulbMinC, b.WetBulbMinC); c != 0 {
			return c
		}
		return cmp.Compare(a.CoolingLoadMinKW, b.CoolingLoadMinKW)
	})

	summary.MatchedBinCount = len(cells)
	summary.Cells = cells

	if len(cells) > 0 && isFinitePositive(totalAIElecKWh) && isFinitePositive(totalCounterfactualBaseElecKWh) {
		rawBaseCOP := totalAICoolingKWh / totalCounterfactualBaseElecKWh
		rawAICOP := totalAICoolingKWh / totalAIElecKWh
		weightedBaseCOP := roundScale(rawBaseCOP, 1e4)
		weightedAICOP := roundScale(rawAICOP, 1e4)
		weightedUpliftPct := roundScale(((rawAICOP-rawBaseCOP)/rawBaseCOP)*100.0, 1e4)
		summary.WeightedBaselineCOP = &weightedBaseCOP
		summary.WeightedAICOP = &weightedAICOP
		summary.WeightedCOPUpliftPct = &weightedUpliftPct
		summary.TotalEstimatedSavedKWh = roundScale(totalSavedKWh, 1e4)
	}

	return summary
}

func accumulateBivariateBins(samples []ControlTickSample, wbStep, loadStep float64) map[bivariateBinKey]*bivariateBinAccumulator {
	bins := make(map[bivariateBinKey]*bivariateBinAccumulator)
	for _, sample := range samples {
		isAI, included := classifyControlMode(sample.Mode)
		if !included {
			continue
		}
		if sample.OutdoorWetBulbC == nil || !isFinite(*sample.OutdoorWetBulbC) {
			continue
		}
		if sample.CoolingLoadKW == nil || !isFinitePositive(*sample.CoolingLoadKW) {
			continue
		}
		loadKW := *sample.CoolingLoadKW
		cop, ok := resolveTickCOP(sample, loadKW)
		if !ok {
			continue
		}
		durationHours := sample.DurationHours
		if !isFinitePositive(durationHours) {
			durationHours = defaultTickDurationHours
		}
		coolingKWh := loadKW * durationHours
		elecKWh := (loadKW / cop) * durationHours
		if !isFinitePositive(coolingKWh) || !isFinitePositive(elecKWh) {
			continue
		}

		wbBucket := normalizeZero(math.Floor(*sample.OutdoorWetBulbC / wbStep))
		loadBucket := normalizeZero(math.Floor(loadKW / loadStep))
		if !isFinite(wbBucket*wbStep) || !isFinite(loadBucket*loadStep) {
			continue
		}
		key := bivariateBinKey{wbBucket: wbBucket, loadBucket: loadBucket}
		acc := bins[key]
		if acc == nil {
			acc = &bivariateBinAccumulator{}
			bins[key] = acc
		}
		target := &acc.baseline
		if isAI {
			target = &acc.ai
		}
		target.samples++
		target.coolingKWh += coolingKWh
		target.elecKWh += elecKWh
	}
	return bins
}

func normalizeBinConfig(cfg BivariateBinConfig) (wbStep, loadStep float64, minSamples int) {
	wbStep = cfg.WetBulbStepC
	if !isFinitePositive(wbStep) {
		wbStep = defaultWetBulbStepC
	}
	loadStep = cfg.CoolingLoadStepKW
	if !isFinitePositive(loadStep) {
		loadStep = defaultCoolingLoadStepKW
	}
	minSamples = cfg.MinSamplesPerModeBin
	if minSamples <= 0 {
		minSamples = defaultMinSamplesPerMode
	}
	return wbStep, loadStep, minSamples
}

func classifyControlMode(mode ControlTickMode) (isAI bool, included bool) {
	switch mode {
	case ModeAIClosedLoop:
		return true, true
	case ModeRuleBaseline, ModeManualOverride:
		return false, true
	default:
		// AI_SHADOW and unknown modes are excluded from closed-loop COP comparison.
		return false, false
	}
}

func resolveTickCOP(sample ControlTickSample, loadKW float64) (float64, bool) {
	if sample.ObservedCOP != nil {
		cop := *sample.ObservedCOP
		return cop, isValidCOP(cop)
	}
	if sample.ChillerPowerKW == nil {
		return 0, false
	}
	powerKW := *sample.ChillerPowerKW
	if !isFinite(powerKW) || powerKW <= minValidChillerPowerKW {
		return 0, false
	}
	ratio := loadKW / powerKW
	return ratio, isValidCOP(ratio)
}

func isValidCOP(cop float64) bool {
	return isFinite(cop) && cop > 0 && cop <= maxValidCOP
}

func isFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func isFinitePositive(value float64) bool {
	return isFinite(value) && value > 0
}

func normalizeZero(value float64) float64 {
	if value == 0 {
		return 0
	}
	return value
}

func roundScale(value, scale float64) float64 {
	return normalizeZero(math.Round(value*scale) / scale)
}
