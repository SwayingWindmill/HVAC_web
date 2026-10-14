package simulator

import (
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"time"
)

// ScenarioModeDaily drives the plant from the Site's wall clock: an office building's
// occupied-hours load over a day whose dry and wet bulb swing with the sun, each date a
// little warmer or cooler than the last. It gives energy models days that differ.
const ScenarioModeDaily ScenarioMode = "DAILY"

type DailyCurve struct {
	Mean      float64 `json:"mean"`
	Amplitude float64 `json:"amplitude"`
	PeakHour  float64 `json:"peakHour"`
}

type DailyLoad struct {
	UnoccupiedKW float64 `json:"unoccupied"`
	OccupiedKW   float64 `json:"occupied"`
	OccupiedFrom float64 `json:"occupiedFrom"`
	OccupiedTo   float64 `json:"occupiedTo"`
	// PerDryBulbC is the load each degree of dry bulb above its daily mean adds.
	PerDryBulbC float64 `json:"perDryBulbC"`
}

type DailyProfile struct {
	Timezone      string     `json:"timezone"`
	DryBulbC      DailyCurve `json:"dryBulbC"`
	WetBulbC      DailyCurve `json:"wetBulbC"`
	DayToDayC     float64    `json:"dayToDayC"`
	CoolingLoadKW DailyLoad  `json:"coolingLoadKw"`

	location *time.Location
}

func decodeDailyProfile(data []byte) (DailyProfile, error) {
	var profile DailyProfile
	if err := decodeStrictJSON(data, &profile); err != nil {
		return DailyProfile{}, fmt.Errorf("decode DAILY profile: %w", err)
	}
	return profile, profile.validate()
}

func (profile *DailyProfile) validate() error {
	location, err := time.LoadLocation(profile.Timezone)
	if err != nil || profile.Timezone == "" {
		return errors.New("DAILY timezone must be an IANA zone")
	}
	profile.location = location
	for name, value := range map[string]float64{
		"dryBulbC.amplitude":       profile.DryBulbC.Amplitude,
		"wetBulbC.amplitude":       profile.WetBulbC.Amplitude,
		"dayToDayC":                profile.DayToDayC,
		"coolingLoadKw.unoccupied": profile.CoolingLoadKW.UnoccupiedKW,
		"coolingLoadKw.occupied":   profile.CoolingLoadKW.OccupiedKW,
	} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return fmt.Errorf("DAILY %s must be a finite non-negative number", name)
		}
	}
	if profile.WetBulbC.Mean > profile.DryBulbC.Mean {
		return errors.New("DAILY wet bulb mean must not exceed dry bulb mean")
	}
	load := profile.CoolingLoadKW
	if load.OccupiedFrom < 1 || load.OccupiedTo > 23 || load.OccupiedFrom >= load.OccupiedTo {
		return errors.New("DAILY occupied hours must lie within 01–23 with occupiedFrom before occupiedTo")
	}
	return nil
}

// dayOffset is the date's own departure from the mean weather, the same all day.
func (profile DailyProfile) dayOffset(local time.Time) float64 {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(local.Format(time.DateOnly)))
	unit := float64(hash.Sum32()%10_000) / 9_999
	return (unit*2 - 1) * profile.DayToDayC
}

// occupancy is 0 at night and 1 in occupied hours, easing over the hour around each edge.
func occupancy(hour, from, to float64) float64 {
	ease := func(edge float64) float64 {
		switch {
		case hour <= edge-0.5:
			return 0
		case hour >= edge+0.5:
			return 1
		default:
			return 0.5 - 0.5*math.Cos(math.Pi*(hour-(edge-0.5)))
		}
	}
	return ease(from) - ease(to)
}

func (profile DailyProfile) inputsAt(at time.Time) ScenarioInputs {
	local := at.In(profile.location)
	hour := float64(local.Hour()) + float64(local.Minute())/60 + float64(local.Second())/3600
	offset := profile.dayOffset(local)
	swing := func(curve DailyCurve) float64 {
		return curve.Amplitude * math.Cos(2*math.Pi*(hour-curve.PeakHour)/24)
	}
	dry := profile.DryBulbC.Mean + swing(profile.DryBulbC) + offset
	wet := math.Min(profile.WetBulbC.Mean+swing(profile.WetBulbC)+0.6*offset, dry)
	load := profile.CoolingLoadKW
	coolingLoad := load.UnoccupiedKW + (load.OccupiedKW-load.UnoccupiedKW)*occupancy(hour, load.OccupiedFrom, load.OccupiedTo) +
		load.PerDryBulbC*(dry-profile.DryBulbC.Mean)
	return ScenarioInputs{AmbientDryBulbC: dry, AmbientWetBulbC: wet, CoolingLoadKW: math.Max(coolingLoad, 0)}
}

// InputsFor is the scenario's inputs elapsed since the plant started, at wall-clock instant at.
func (scenario Scenario) InputsFor(elapsed time.Duration, at time.Time) ScenarioInputs {
	if scenario.Mode == ScenarioModeDaily {
		return scenario.Daily.inputsAt(at)
	}
	return scenario.InputsAt(elapsed)
}
