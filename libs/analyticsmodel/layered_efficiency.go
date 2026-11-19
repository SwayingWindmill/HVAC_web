package analyticsmodel

import "math"

const (
	minEfficiencyCoolingKWh = 1.0
	minPlantElectricityKWh  = 1.0
	minChillerElecKWh       = 1.0
	minPumpElectricityKWh   = 0.5

	minValidPlantCOP   = 1.0
	maxValidPlantCOP   = 12.0
	minValidChillerCOP = 1.0
	maxValidChillerCOP = 15.0
	maxValidPumpWTF    = 100.0

	defaultMeterImbalanceThreshold = 0.05
)

// LayeredEfficiencyInput holds the thermal cooling and electrical sub-meter kWh totals
// needed to evaluate plant-wide and subsystem efficiency layers for a bucket or period.
type LayeredEfficiencyInput struct {
	CoolingKWh                 float64 `json:"coolingKWh"`
	PlantElectricityKWh        float64 `json:"plantElectricityKWh"`
	ChillerElectricityKWh      float64 `json:"chillerElectricityKWh"`
	ChilledPumpElectricityKWh  float64 `json:"chilledPumpElectricityKWh"`
	CoolingPumpElectricityKWh  float64 `json:"coolingPumpElectricityKWh"`
	CoolingTowerElectricityKWh float64 `json:"coolingTowerElectricityKWh"`
	SynthesizedTotal           bool    `json:"synthesizedTotal,omitempty"`
}

// LayeredEfficiency reports plant COP alongside chiller, pump WTF, and cooling tower share
// ratios, suppressing standby/noise regimes and physically implausible outliers to nil.
type LayeredEfficiency struct {
	PlantCOP         *float64 `json:"plantCOP"`
	ChillerCOP       *float64 `json:"chillerCOP"`
	ChilledPumpWTF   *float64 `json:"chilledPumpWTF"`
	CoolingPumpWTF   *float64 `json:"coolingPumpWTF"`
	TowerPowerShare  *float64 `json:"towerPowerShare"`
	SynthesizedTotal bool     `json:"synthesizedTotal,omitempty"`
}

// ComputeLayeredEfficiency calculates layered plant, chiller, pump WTF, and cooling tower
// metrics from interval or period energy totals while enforcing standby deadbands and bounds.
func ComputeLayeredEfficiency(input LayeredEfficiencyInput) LayeredEfficiency {
	return LayeredEfficiency{
		PlantCOP:         boundedRatio(input.CoolingKWh, input.PlantElectricityKWh, minEfficiencyCoolingKWh, minPlantElectricityKWh, minValidPlantCOP, maxValidPlantCOP, true),
		ChillerCOP:       boundedRatio(input.CoolingKWh, input.ChillerElectricityKWh, minEfficiencyCoolingKWh, minChillerElecKWh, minValidChillerCOP, maxValidChillerCOP, true),
		ChilledPumpWTF:   boundedRatio(input.CoolingKWh, input.ChilledPumpElectricityKWh, minEfficiencyCoolingKWh, minPumpElectricityKWh, 0, maxValidPumpWTF, false),
		CoolingPumpWTF:   boundedRatio(input.CoolingKWh, input.CoolingPumpElectricityKWh, minEfficiencyCoolingKWh, minPumpElectricityKWh, 0, maxValidPumpWTF, false),
		TowerPowerShare:  computeTowerPowerShare(input.CoolingTowerElectricityKWh, input.PlantElectricityKWh),
		SynthesizedTotal: input.SynthesizedTotal,
	}
}

func boundedRatio(numerator, denominator, minNum, minDen, minRatio, maxRatio float64, inclusiveMin bool) *float64 {
	if !isFinite(numerator) || !isFinite(denominator) || numerator < minNum || denominator < minDen {
		return nil
	}
	ratio := numerator / denominator
	if !isFinite(ratio) || ratio > maxRatio {
		return nil
	}
	if inclusiveMin {
		if ratio < minRatio {
			return nil
		}
	} else if ratio <= minRatio {
		return nil
	}
	val := normalizeZero(ratio)
	return &val
}

func computeTowerPowerShare(towerKWh, plantKWh float64) *float64 {
	if !isFinite(towerKWh) || !isFinite(plantKWh) || plantKWh < minPlantElectricityKWh || towerKWh < 0 || towerKWh > plantKWh {
		return nil
	}
	share := normalizeZero(math.Min(1.0, math.Max(0.0, towerKWh/plantKWh)))
	return &share
}

// MeterBalanceStatus classifies how closely site main-meter electricity matches sub-meter sums.
type MeterBalanceStatus string

const (
	MeterBalanceBalanced            MeterBalanceStatus = "BALANCED"
	MeterBalanceUnmeteredGap        MeterBalanceStatus = "UNMETERED_GAP"
	MeterBalanceSubmeterExceedsMain MeterBalanceStatus = "SUBMETER_EXCEEDS_MAIN"
	MeterBalanceNoMainMeter         MeterBalanceStatus = "NO_MAIN_METER"
	MeterBalanceSynthesized         MeterBalanceStatus = "SYNTHESIZED"
)

// MeterBalanceSummary summarizes the residual between physical main electricity and sub-meters.
type MeterBalanceSummary struct {
	SiteKWh        float64            `json:"siteKWh"`
	SubmeterSumKWh float64            `json:"submeterSumKWh"`
	DiffKWh        float64            `json:"diffKWh"`
	ImbalanceRatio *float64           `json:"imbalanceRatio"`
	Status         MeterBalanceStatus `json:"status"`
}

// ComputeMeterBalance compares physical site electricity against summed branch sub-meters.
// Synthesized totals cannot validate a physical main meter and report SYNTHESIZED.
func ComputeMeterBalance(siteKWh, submeterSumKWh float64, threshold float64, synthesized bool) MeterBalanceSummary {
	if !isFinite(threshold) || threshold <= 0 {
		threshold = defaultMeterImbalanceThreshold
	}
	if !isFinite(siteKWh) || !isFinite(submeterSumKWh) {
		safeSite := 0.0
		if isFinite(siteKWh) {
			safeSite = normalizeZero(siteKWh)
		}
		safeSub := 0.0
		if isFinite(submeterSumKWh) {
			safeSub = normalizeZero(submeterSumKWh)
		}
		status := MeterBalanceNoMainMeter
		if synthesized {
			status = MeterBalanceSynthesized
		}
		return MeterBalanceSummary{
			SiteKWh:        safeSite,
			SubmeterSumKWh: safeSub,
			DiffKWh:        0,
			ImbalanceRatio: nil,
			Status:         status,
		}
	}

	diffKWh := normalizeZero(siteKWh - submeterSumKWh)
	if synthesized {
		return MeterBalanceSummary{
			SiteKWh:        normalizeZero(siteKWh),
			SubmeterSumKWh: normalizeZero(submeterSumKWh),
			DiffKWh:        diffKWh,
			ImbalanceRatio: nil,
			Status:         MeterBalanceSynthesized,
		}
	}
	if siteKWh <= 0 {
		return MeterBalanceSummary{
			SiteKWh:        normalizeZero(siteKWh),
			SubmeterSumKWh: normalizeZero(submeterSumKWh),
			DiffKWh:        diffKWh,
			ImbalanceRatio: nil,
			Status:         MeterBalanceNoMainMeter,
		}
	}

	ratio := normalizeZero(diffKWh / siteKWh)
	status := MeterBalanceBalanced
	if math.Abs(ratio) > threshold {
		if ratio > 0 {
			status = MeterBalanceUnmeteredGap
		} else {
			status = MeterBalanceSubmeterExceedsMain
		}
	}
	return MeterBalanceSummary{
		SiteKWh:        normalizeZero(siteKWh),
		SubmeterSumKWh: normalizeZero(submeterSumKWh),
		DiffKWh:        diffKWh,
		ImbalanceRatio: &ratio,
		Status:         status,
	}
}
