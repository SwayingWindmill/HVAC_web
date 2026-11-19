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
	minValidCOP              = 1.0
	maxValidCOP              = 12.0
	minValidChillerPowerKW   = 5.0
	minValidCoolingLoadKW    = 10.0
	minValidChwFlowM3H       = 1.0
)

// ControlTickSample is one control-interval plant observation used to compare closed-loop AI
// performance against baseline control under matched weather and cooling load conditions.
type ControlTickSample struct {
	Mode            ControlTickMode `json:"mode"`
	OutdoorWetBulbC *float64        `json:"outdoorWetBulbC"`
	CoolingLoadKW   *float64        `json:"coolingLoadKW"`
	ChillerPowerKW  *float64        `json:"chillerPowerKW"`
	ObservedCOP     *float64        `json:"observedCOP"`
	ChwSupplyTempC  *float64        `json:"chwSupplyTempC,omitempty"`
	ChwReturnTempC  *float64        `json:"chwReturnTempC,omitempty"`
	ChwFlowM3H      *float64        `json:"chwFlowM3H,omitempty"`
	Incomplete      bool            `json:"incomplete,omitempty"`
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
	MatchedBinCount           int                `json:"matchedBinCount"`
	MatchedAISamples          int                `json:"matchedAiSamples"`
	MatchedBaselineSamples    int                `json:"matchedBaselineSamples"`
	WeightedBaselineCOP       *float64           `json:"weightedBaselineCOP"`
	WeightedAICOP             *float64           `json:"weightedAiCOP"`
	WeightedCOPUpliftPct      *float64           `json:"weightedCopUpliftPct"`
	TotalEstimatedSavedKWh    float64            `json:"totalEstimatedSavedKWh"`
	Incomplete                bool               `json:"incomplete,omitempty"`
	ExcludedIncompleteSamples int                `json:"excludedIncompleteSamples,omitempty"`
	Cells                     []BivariateCOPCell `json:"cells"`
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
	bins, excludedIncomplete := accumulateBivariateBins(samples, wbStep, loadStep)

	cells := make([]BivariateCOPCell, 0, len(bins))
	summary := BivariateCOPSummary{
		Incomplete:                excludedIncomplete > 0,
		ExcludedIncompleteSamples: excludedIncomplete,
		Cells:                     cells,
	}

	var totalAICoolingKWh, totalAIElecKWh, totalCounterfactualBaseElecKWh, totalSavedKWh float64

	for key, acc := range bins {
		if acc.baseline.samples < minSamples || acc.ai.samples < minSamples {
			continue
		}
		if !isFinitePositive(acc.baseline.coolingKWh) || !isFinitePositive(acc.baseline.elecKWh) ||
			!isFinitePositive(acc.ai.coolingKWh) || !isFinitePositive(acc.ai.elecKWh) {
			continue
		}
		baseCOP := acc.baseline.coolingKWh / acc.baseline.elecKWh
		aiCOP := acc.ai.coolingKWh / acc.ai.elecKWh
		if !isValidCOP(baseCOP) || !isValidCOP(aiCOP) {
			continue
		}
		upliftPct := ((aiCOP - baseCOP) / baseCOP) * 100.0
		counterfactualBaseElecKWh := acc.ai.coolingKWh / baseCOP
		savedKWh := counterfactualBaseElecKWh - acc.ai.elecKWh
		if !isFinite(upliftPct) || !isFinitePositive(counterfactualBaseElecKWh) || !isFinite(savedKWh) {
			continue
		}

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

	if len(cells) > 0 && isFinitePositive(totalAICoolingKWh) && isFinitePositive(totalAIElecKWh) && isFinitePositive(totalCounterfactualBaseElecKWh) {
		rawBaseCOP := totalAICoolingKWh / totalCounterfactualBaseElecKWh
		rawAICOP := totalAICoolingKWh / totalAIElecKWh
		rawUpliftPct := ((rawAICOP - rawBaseCOP) / rawBaseCOP) * 100.0
		if isValidCOP(rawBaseCOP) && isValidCOP(rawAICOP) && isFinite(rawUpliftPct) && isFinite(totalSavedKWh) {
			weightedBaseCOP := roundScale(rawBaseCOP, 1e4)
			weightedAICOP := roundScale(rawAICOP, 1e4)
			weightedUpliftPct := roundScale(rawUpliftPct, 1e4)
			summary.WeightedBaselineCOP = &weightedBaseCOP
			summary.WeightedAICOP = &weightedAICOP
			summary.WeightedCOPUpliftPct = &weightedUpliftPct
			summary.TotalEstimatedSavedKWh = roundScale(totalSavedKWh, 1e4)
		}
	}

	return summary
}

func accumulateBivariateBins(samples []ControlTickSample, wbStep, loadStep float64) (map[bivariateBinKey]*bivariateBinAccumulator, int) {
	bins := make(map[bivariateBinKey]*bivariateBinAccumulator)
	excludedIncomplete := 0
	for _, sample := range samples {
		isAI, included := classifyControlMode(sample.Mode)
		if !included {
			continue
		}
		if sample.Incomplete {
			excludedIncomplete++
			continue
		}
		if sample.OutdoorWetBulbC == nil || !isFinite(*sample.OutdoorWetBulbC) {
			continue
		}
		if sample.CoolingLoadKW == nil || !isFinite(*sample.CoolingLoadKW) || *sample.CoolingLoadKW < minValidCoolingLoadKW {
			continue
		}
		if sample.ChwFlowM3H != nil && (!isFinite(*sample.ChwFlowM3H) || *sample.ChwFlowM3H < minValidChwFlowM3H) {
			continue
		}
		if !validChwTemperatures(sample) {
			continue
		}
		if math.IsNaN(sample.DurationHours) || math.IsInf(sample.DurationHours, 0) {
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
	return bins, excludedIncomplete
}

func validChwTemperatures(sample ControlTickSample) bool {
	if sample.ChwSupplyTempC != nil && !isFinite(*sample.ChwSupplyTempC) {
		return false
	}
	if sample.ChwReturnTempC != nil && !isFinite(*sample.ChwReturnTempC) {
		return false
	}
	if sample.ChwSupplyTempC != nil && sample.ChwReturnTempC != nil {
		return *sample.ChwReturnTempC > *sample.ChwSupplyTempC
	}
	return true
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
	if sample.ChillerPowerKW != nil {
		powerKW := *sample.ChillerPowerKW
		if !isFinite(powerKW) || powerKW < minValidChillerPowerKW {
			return 0, false
		}
	}
	if sample.ObservedCOP != nil {
		cop := *sample.ObservedCOP
		return cop, isValidCOP(cop)
	}
	if sample.ChillerPowerKW == nil {
		return 0, false
	}
	ratio := loadKW / *sample.ChillerPowerKW
	return ratio, isValidCOP(ratio)
}

func isValidCOP(cop float64) bool {
	return isFinite(cop) && cop >= minValidCOP && cop <= maxValidCOP
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
