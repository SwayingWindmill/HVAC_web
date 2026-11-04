// Package nameplate is the equipment nameplate and the physics checks that use it.
// A field listed in Unconfirmed is not a rated value: load ratio, pump-power estimates
// and the low-delta-T power gate must not treat it as known.
package nameplate

import "math"

const (
	KindWetBulbDryBulb     = "WET_BULB_DRY_BULB"
	KindWetBulbCondenser   = "WET_BULB_CONDENSER"
	KindPowerNameplate     = "POWER_NAMEPLATE"
	KindCOPCarnot          = "COP_CARNOT"
	KindChilledDirection   = "CHW_DIRECTION"
	KindCondenserDirection = "CW_DIRECTION"
	KindHeatBalance        = "HEAT_BALANCE"
	KindPumpWithoutChiller = "PUMP_WITHOUT_CHILLER"

	ReasonWetBulbDry         = "PHYSICS_WET_BULB_DRY"
	ReasonWetBulbCondenser   = "PHYSICS_WET_BULB_CONDENSER"
	ReasonPower              = "PHYSICS_POWER"
	ReasonCOP                = "PHYSICS_COP"
	ReasonChilledDirection   = "PHYSICS_CHW_DIRECTION"
	ReasonCondenserDirection = "PHYSICS_CW_DIRECTION"
	ReasonHeatBalance        = "PHYSICS_HEAT_BALANCE"

	// DefaultPowerAboveRatedFraction is how far measured power may sit above the
	// nameplate before the reading is suspect. 15% covers supply voltage and a hot
	// condenser; the Songyaxuan chiller C peak of 191.8 kW is 50% above 127.5 kW.
	// This is not the low-delta-T running floor (power above 10% of rated).
	DefaultPowerAboveRatedFraction = 0.15

	// DefaultCarnotFraction is the share of the water-side Carnot COP a reading may
	// reach. Water temperatures overstate the refrigerant Carnot, and a water-cooled
	// machine typically lands near 0.45–0.65 of that figure; 0.70 still accepts a
	// part-load COP above the nameplate (Songyaxuan's median 5.02 against 4.49)
	// and rejects a COP that has climbed up to the water-side Carnot.
	DefaultCarnotFraction = 0.70

	// DefaultWetBulbCondenserMarginK is how far a wet bulb must sit below the warmest
	// condenser entering water. A tower approach under 1 K is not credible.
	DefaultWetBulbCondenserMarginK = 1.0

	// DefaultDirectionEpsilonK is smaller than the 0.1 ℃ display step. A 0.9 K
	// header drop still passes; a reversed or flat pair while the machine runs does not.
	DefaultDirectionEpsilonK = 0.05

	// DefaultHeatBalanceFraction is the residual |cooling + power − condenser| / scale
	// that is still accepted. A few tenths of a degree on a small rise is already
	// ~10%; past 25% the balance is no longer useful.
	DefaultHeatBalanceFraction = 0.25

	// DefaultHeatBalanceMinimumKW skips the balance when the plant is essentially off,
	// so sensor noise is not divided into a large residual.
	DefaultHeatBalanceMinimumKW = 10.0

	// WaterHeatFactorKW is kW per (m³/h · K) for water, ρ·cp/3600 ≈ 1.163.
	// Songyaxuan's gateway uses the same factor for cooling load.
	WaterHeatFactorKW = 1.163

	BasisPower   = "POWER"
	BasisCurrent = "CURRENT"
)

// Plate is one machine's nameplate. Candidates are motor plates that might belong
// to this machine when the assignment is not confirmed.
type Plate struct {
	Manufacturer        string   `json:"manufacturer,omitempty"`
	Model               string   `json:"model,omitempty"`
	Year                int      `json:"year,omitempty"`
	Refrigerant         string   `json:"refrigerant,omitempty"`
	RatedCapacityKW     float64  `json:"ratedCapacityKw,omitempty"`
	RatedPowerKW        float64  `json:"ratedPowerKw,omitempty"`
	RatedCurrentA       float64  `json:"ratedCurrentA,omitempty"`
	StartingCurrentA    float64  `json:"startingCurrentA,omitempty"`
	Supply              string   `json:"supply,omitempty"`
	HighSidePressureMPa float64  `json:"highSidePressureMpa,omitempty"`
	LowSidePressureMPa  float64  `json:"lowSidePressureMpa,omitempty"`
	CondenserAreaM2     float64  `json:"condenserAreaM2,omitempty"`
	Efficiency          float64  `json:"efficiency,omitempty"`
	RatedSpeedRPM       float64  `json:"ratedSpeedRpm,omitempty"`
	Speed               string   `json:"speed,omitempty"`
	Role                string   `json:"role,omitempty"`
	Unconfirmed         []string `json:"unconfirmed,omitempty"`
	Candidates          []Plate  `json:"candidates,omitempty"`
	Pending             []string `json:"pending,omitempty"`
	Note                string   `json:"note,omitempty"`
	Catalog             string   `json:"catalog,omitempty"`
}

// Confirmed reports whether field may be used as a rated value.
func (plate Plate) Confirmed(field string) bool {
	for _, name := range plate.Unconfirmed {
		if name == field {
			return false
		}
	}
	return true
}

// ProjectedRatedPower is the rated kilowatts written onto the asset column.
func (plate Plate) ProjectedRatedPower() (float64, bool) {
	if !plate.Confirmed("ratedPowerKw") || plate.RatedPowerKW <= 0 || math.IsNaN(plate.RatedPowerKW) {
		return 0, false
	}
	return plate.RatedPowerKW, true
}

// ProjectedRatedCapacity is the rated cooling kilowatts written onto the asset column.
func (plate Plate) ProjectedRatedCapacity() (float64, bool) {
	if !plate.Confirmed("ratedCapacityKw") || plate.RatedCapacityKW <= 0 || math.IsNaN(plate.RatedCapacityKW) {
		return 0, false
	}
	return plate.RatedCapacityKW, true
}

// LoadRatio is measured power over rated power, or measured current over rated
// current when power is absent. An unconfirmed denominator is not used.
func LoadRatio(powerKW, currentA *float64, plate Plate) (float64, string, bool) {
	if powerKW != nil && plate.Confirmed("ratedPowerKw") && plate.RatedPowerKW > 0 && finite(*powerKW) {
		return *powerKW / plate.RatedPowerKW, BasisPower, true
	}
	if currentA != nil && plate.Confirmed("ratedCurrentA") && plate.RatedCurrentA > 0 && finite(*currentA) {
		return *currentA / plate.RatedCurrentA, BasisCurrent, true
	}
	return 0, "", false
}

// EstimatePumpPowerKW is the electrical input of a fixed-speed pump that has no meter.
// The motor nameplate kilowatts are shaft output, so input is rated power / efficiency.
// A stopped pump is 0. A running pump whose power or efficiency is unconfirmed has no estimate.
func EstimatePumpPowerKW(plate Plate, running bool) (float64, bool) {
	if !running {
		return 0, true
	}
	if plate.Speed != "FIXED" || !plate.Confirmed("ratedPowerKw") || !plate.Confirmed("efficiency") {
		return 0, false
	}
	if plate.RatedPowerKW <= 0 || plate.Efficiency <= 0 || plate.Efficiency > 1 || math.IsNaN(plate.Efficiency) {
		return 0, false
	}
	return plate.RatedPowerKW / plate.Efficiency, true
}

// ExceedsRated is measured > rated × (1 + fraction). Zero or negative measured
// power is not an exceedance. fraction is the allowed share above the nameplate, not a floor.
func ExceedsRated(measured, rated, fraction float64) bool {
	if measured <= 0 || rated <= 0 || !finite(measured) || !finite(rated) || fraction < 0 {
		return false
	}
	return measured > rated*(1+fraction)
}

// CarnotCOP is T_evap / (T_cond − T_evap) on water temperatures in °C.
// A lift under 0.5 K is not a usable bound.
func CarnotCOP(evaporatorC, condenserC float64) (float64, bool) {
	if !finite(evaporatorC) || !finite(condenserC) {
		return 0, false
	}
	te := evaporatorC + 273.15
	tc := condenserC + 273.15
	if te <= 0 || tc < te+0.5 {
		return 0, false
	}
	return te / (tc - te), true
}

// COPExceedsCarnot reports whether cop is above fraction of the water-side Carnot COP.
func COPExceedsCarnot(cop, evaporatorC, condenserC, fraction float64) bool {
	if fraction <= 0 || fraction > 1 || cop <= 0 || !finite(cop) {
		return false
	}
	limit, ok := CarnotCOP(evaporatorC, condenserC)
	if !ok {
		return false
	}
	return cop > fraction*limit
}

// COPPlausible is the operating-condition bound used when a condenser temperature
// exists, and the previous fixed ceiling of 12 when it does not.
func COPPlausible(cop, evaporatorC float64, condenserC *float64) bool {
	if cop < 1 || !finite(cop) {
		return false
	}
	if condenserC == nil {
		return cop <= 12
	}
	if _, ok := CarnotCOP(evaporatorC, *condenserC); !ok {
		return cop <= 12
	}
	return !COPExceedsCarnot(cop, evaporatorC, *condenserC, DefaultCarnotFraction)
}

// WaterHeatKW is 1.163 × flow(m³/h) × (leaving − entering).
func WaterHeatKW(flowM3H, enteringC, leavingC float64) (float64, bool) {
	if flowM3H <= 0 || !finite(flowM3H) || !finite(enteringC) || !finite(leavingC) {
		return 0, false
	}
	delta := leavingC - enteringC
	if delta <= 0 {
		return 0, false
	}
	return WaterHeatFactorKW * flowM3H * delta, true
}

// HeatBalanceResidual is |cooling + power − condenser| / max(condenser, cooling+power).
func HeatBalanceResidual(coolingKW, powerKW, condenserKW float64) (float64, bool) {
	if coolingKW <= 0 || condenserKW <= 0 || powerKW < 0 || !finite(coolingKW) || !finite(powerKW) || !finite(condenserKW) {
		return 0, false
	}
	scale := math.Max(condenserKW, coolingKW+powerKW)
	if scale < DefaultHeatBalanceMinimumKW {
		return 0, false
	}
	return math.Abs(coolingKW+powerKW-condenserKW) / scale, true
}

// PumpWithoutChiller is a pump running while every chiller is stopped.
func PumpWithoutChiller(pumpRunning, anyChillerRunning bool) bool {
	return pumpRunning && !anyChillerRunning
}

// Observation is one physics check against the subject reading and its peers.
// A nil Running means the check is not gated. Fraction nil selects the default for the kind.
type Observation struct {
	Kind               string
	Value              float64
	Basis              string
	Rated              float64
	MarginK            *float64
	Fraction           *float64
	EpsilonK           *float64
	DryBulbC           *float64
	CondenserEnteringC []float64
	PowersKW           []float64
	EvaporatorC        *float64
	CondenserC         []float64
	ReturnC            *float64
	EnteringC          *float64
	CoolingKW          *float64
	CondenserHeatKW    *float64
	FlowM3H            *float64
	HeatEnteringC      *float64
	HeatLeavingC       *float64
	Running            *bool
}

// Violates reports whether the observation breaks the check.
// skipped is true when a peer or the running gate is absent: absence is not a violation.
func Violates(obs Observation) (violates bool, skipped bool) {
	if obs.Running != nil && !*obs.Running {
		return false, true
	}
	switch obs.Kind {
	case KindWetBulbDryBulb:
		if obs.DryBulbC == nil || !finite(obs.Value) || !finite(*obs.DryBulbC) {
			return false, true
		}
		margin := 0.0
		if obs.MarginK != nil {
			margin = *obs.MarginK
		}
		return obs.Value > *obs.DryBulbC+margin, false
	case KindWetBulbCondenser:
		warmest, ok := maxFinite(obs.CondenserEnteringC)
		if !ok || !finite(obs.Value) {
			return false, true
		}
		margin := DefaultWetBulbCondenserMarginK
		if obs.MarginK != nil {
			margin = *obs.MarginK
		}
		return obs.Value > warmest-margin, false
	case KindPowerNameplate:
		fraction := DefaultPowerAboveRatedFraction
		if obs.Fraction != nil {
			fraction = *obs.Fraction
		}
		if obs.Rated <= 0 {
			return false, true
		}
		return ExceedsRated(obs.Value, obs.Rated, fraction), false
	case KindCOPCarnot:
		cooling := obs.Value
		if obs.CoolingKW != nil {
			cooling = *obs.CoolingKW
		}
		power := sumPositive(obs.PowersKW)
		evaporator, evapOK := one(obs.EvaporatorC)
		condenser, condOK := averageFinite(obs.CondenserC)
		if cooling <= 0 || power <= 0 || !evapOK || !condOK {
			return false, true
		}
		fraction := DefaultCarnotFraction
		if obs.Fraction != nil {
			fraction = *obs.Fraction
		}
		if _, ok := CarnotCOP(evaporator, condenser); !ok {
			return false, true
		}
		return COPExceedsCarnot(cooling/power, evaporator, condenser, fraction), false
	case KindChilledDirection:
		ret, ok := one(obs.ReturnC)
		if !ok || !finite(obs.Value) {
			return false, true
		}
		epsilon := DefaultDirectionEpsilonK
		if obs.EpsilonK != nil {
			epsilon = *obs.EpsilonK
		}
		return obs.Value > ret-epsilon, false
	case KindCondenserDirection:
		entering, ok := one(obs.EnteringC)
		if !ok || !finite(obs.Value) {
			return false, true
		}
		epsilon := DefaultDirectionEpsilonK
		if obs.EpsilonK != nil {
			epsilon = *obs.EpsilonK
		}
		return obs.Value < entering+epsilon, false
	case KindHeatBalance:
		cooling := obs.Value
		if obs.CoolingKW != nil {
			cooling = *obs.CoolingKW
		}
		power := sumPositive(obs.PowersKW)
		condenser, ok := condenserHeat(obs)
		if !ok {
			return false, true
		}
		residual, ok := HeatBalanceResidual(cooling, power, condenser)
		if !ok {
			return false, true
		}
		fraction := DefaultHeatBalanceFraction
		if obs.Fraction != nil {
			fraction = *obs.Fraction
		}
		return residual > fraction, false
	default:
		return false, true
	}
}

// QualityReason is the point-health reason a violating check writes. The pump
// check is a diagnostic alarm and does not mark the reading.
func QualityReason(kind string) (string, bool) {
	switch kind {
	case KindWetBulbDryBulb:
		return ReasonWetBulbDry, true
	case KindWetBulbCondenser:
		return ReasonWetBulbCondenser, true
	case KindPowerNameplate:
		return ReasonPower, true
	case KindCOPCarnot:
		return ReasonCOP, true
	case KindChilledDirection:
		return ReasonChilledDirection, true
	case KindCondenserDirection:
		return ReasonCondenserDirection, true
	case KindHeatBalance:
		return ReasonHeatBalance, true
	default:
		return "", false
	}
}

func condenserHeat(obs Observation) (float64, bool) {
	if obs.CondenserHeatKW != nil && *obs.CondenserHeatKW > 0 && finite(*obs.CondenserHeatKW) {
		return *obs.CondenserHeatKW, true
	}
	if obs.FlowM3H == nil || obs.HeatEnteringC == nil || obs.HeatLeavingC == nil {
		return 0, false
	}
	return WaterHeatKW(*obs.FlowM3H, *obs.HeatEnteringC, *obs.HeatLeavingC)
}

func one(value *float64) (float64, bool) {
	if value == nil || !finite(*value) {
		return 0, false
	}
	return *value, true
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func sumPositive(values []float64) float64 {
	sum := 0.0
	for _, value := range values {
		if value > 0 && finite(value) {
			sum += value
		}
	}
	return sum
}

func maxFinite(values []float64) (float64, bool) {
	max := 0.0
	ok := false
	for _, value := range values {
		if !finite(value) {
			continue
		}
		if !ok || value > max {
			max = value
			ok = true
		}
	}
	return max, ok
}

func averageFinite(values []float64) (float64, bool) {
	sum := 0.0
	count := 0.0
	for _, value := range values {
		if !finite(value) {
			continue
		}
		sum += value
		count++
	}
	if count == 0 {
		return 0, false
	}
	return sum / count, true
}
