package nameplate

import (
	"math"
	"testing"
)

func hitachi() Plate {
	return Plate{
		Manufacturer: "日立", Model: "RCU180SC2", Year: 2007, Refrigerant: "R22",
		RatedCapacityKW: 572, RatedPowerKW: 127.5, RatedCurrentA: 225, StartingCurrentA: 222,
		Supply: "380V/3PH/50Hz", HighSidePressureMPa: 2.2, LowSidePressureMPa: 1.3, CondenserAreaM2: 60.6,
	}
}

func kaiquanUnassigned() Plate {
	return Plate{
		Manufacturer: "凯泉", Speed: "FIXED", Role: "UNKNOWN",
		Unconfirmed: []string{"role", "model", "ratedPowerKw", "ratedCurrentA", "efficiency", "ratedSpeedRpm"},
		Candidates: []Plate{
			{Model: "YE3-180L-4", RatedPowerKW: 22, RatedCurrentA: 41.8, Efficiency: 0.93, RatedSpeedRPM: 1477, Speed: "FIXED"},
			{Model: "YE3-160L-4", RatedPowerKW: 15, RatedCurrentA: 28.8, Efficiency: 0.921, RatedSpeedRPM: 1460, Speed: "FIXED"},
		},
	}
}

func f64(value float64) *float64 { return &value }

func TestSongyaxuanChillerNameplateFeedsLoadRatioAndLeavesThePumpUnassigned(t *testing.T) {
	plate := hitachi()
	power, ok := plate.ProjectedRatedPower()
	capacity, capOK := plate.ProjectedRatedCapacity()
	if !ok || power != 127.5 || !capOK || capacity != 572 {
		t.Fatalf("projected ratings power=%v %v capacity=%v %v", power, ok, capacity, capOK)
	}
	measured := 191.8
	ratio, basis, ok := LoadRatio(&measured, nil, plate)
	if !ok || basis != BasisPower || math.Abs(ratio-191.8/127.5) > 1e-9 {
		t.Fatalf("chiller C load ratio = %v %s %v", ratio, basis, ok)
	}
	// No power meter, current over the nameplate running current.
	current := 180.0
	ratio, basis, ok = LoadRatio(nil, &current, plate)
	if !ok || basis != BasisCurrent || math.Abs(ratio-180.0/225.0) > 1e-9 {
		t.Fatalf("current load ratio = %v %s %v", ratio, basis, ok)
	}
	if !ExceedsRated(191.8, 127.5, DefaultPowerAboveRatedFraction) {
		t.Fatal("191.8 kW should exceed 127.5 kW by more than 15%")
	}
	if ExceedsRated(140, 127.5, DefaultPowerAboveRatedFraction) {
		t.Fatal("140 kW is inside 15% of 127.5 kW")
	}

	pump := kaiquanUnassigned()
	if _, ok := pump.ProjectedRatedPower(); ok {
		t.Fatal("an unconfirmed motor must not become the asset's rated power")
	}
	if _, ok := EstimatePumpPowerKW(pump, true); ok {
		t.Fatal("a running pump with an unconfirmed motor has no power estimate")
	}
	if kw, ok := EstimatePumpPowerKW(pump, false); !ok || kw != 0 {
		t.Fatalf("stopped pump = %v %v", kw, ok)
	}
	confirmed := pump.Candidates[0]
	confirmed.Speed = "FIXED"
	kw, ok := EstimatePumpPowerKW(confirmed, true)
	if !ok || math.Abs(kw-22/0.93) > 1e-9 {
		t.Fatalf("YE3-180L-4 electrical input = %v %v", kw, ok)
	}
}

func TestSongyaxuanWetBulbIsNotAWetBulb(t *testing.T) {
	// t15 sits 4–7 K above condenser entering water, and above the dry bulb.
	if violates, skipped := Violates(Observation{Kind: KindWetBulbDryBulb, Value: 32, DryBulbC: f64(30)}); !violates || skipped {
		t.Fatalf("wet bulb above dry bulb: %v %v", violates, skipped)
	}
	if violates, skipped := Violates(Observation{Kind: KindWetBulbDryBulb, Value: 28, DryBulbC: f64(30)}); violates || skipped {
		t.Fatalf("wet bulb below dry bulb: %v %v", violates, skipped)
	}
	running := true
	aboveWater, skipped := Violates(Observation{
		Kind: KindWetBulbCondenser, Value: 32, CondenserEnteringC: []float64{26, 27}, Running: &running,
	})
	if !aboveWater || skipped {
		t.Fatalf("wet bulb 5 K above condenser water: %v %v", aboveWater, skipped)
	}
	// Above only the colder of the two sensors: the warm one can still be a real approach.
	if violates, skipped := Violates(Observation{
		Kind: KindWetBulbCondenser, Value: 28, CondenserEnteringC: []float64{26, 32}, Running: &running,
	}); violates || skipped {
		t.Fatalf("wet bulb below the warmer condenser water: %v %v", violates, skipped)
	}
	stopped := false
	if violates, skipped := Violates(Observation{
		Kind: KindWetBulbCondenser, Value: 32, CondenserEnteringC: []float64{26}, Running: &stopped,
	}); violates || !skipped {
		t.Fatalf("towers not running must not judge the wet bulb: %v %v", violates, skipped)
	}
}

func TestPartLoadCOPAboveNameplateStaysInsideTheCarnotFraction(t *testing.T) {
	// 572 / 127.5 ≈ 4.49 nameplate. A measured 5.02 at 7 ℃ evaporator and 30 ℃ condenser
	// is part load with cool water, not an impossible COP.
	nominal := 572 / 127.5
	if nominal >= 5.02 {
		t.Fatalf("fixture nominal %v should sit under the measured median", nominal)
	}
	cop := 5.02
	if !COPPlausible(cop, 7, f64(30)) {
		limit, _ := CarnotCOP(7, 30)
		t.Fatalf("5.02 should pass 0.70 of Carnot %v", limit)
	}
	// Same temperatures, a COP up near the water-side Carnot does not.
	if COPPlausible(10, 7, f64(30)) {
		t.Fatal("COP 10 at a 23 K lift should fail the Carnot fraction")
	}
	cooling := 5.02 * 127.5
	if violates, skipped := Violates(Observation{
		Kind: KindCOPCarnot, Value: cooling, PowersKW: []float64{127.5},
		EvaporatorC: f64(7), CondenserC: []float64{30},
	}); violates || skipped {
		t.Fatalf("plant COP 5.02: violates=%v skipped=%v", violates, skipped)
	}
	if violates, skipped := Violates(Observation{
		Kind: KindCOPCarnot, Value: 10 * 127.5, PowersKW: []float64{127.5},
		EvaporatorC: f64(7), CondenserC: []float64{30},
	}); !violates || skipped {
		t.Fatalf("plant COP 10: violates=%v skipped=%v", violates, skipped)
	}
	// No condenser temperature: the old ceiling of 12 still applies, and 5.02 passes.
	if !COPPlausible(5.02, 7, nil) || COPPlausible(12.1, 7, nil) {
		t.Fatal("without a condenser temperature the ceiling stays at 12")
	}
}

func TestChilledAndCondenserDirectionAndTheHeaderBypass(t *testing.T) {
	running := true
	// Header drop of 0.9 K is supply below return. Idle chillers passing water explain
	// why it is smaller than the running machines' 1.3–2.8 K; it is not reversed.
	if violates, skipped := Violates(Observation{
		Kind: KindChilledDirection, Value: 6.5, ReturnC: f64(7.4), Running: &running,
	}); violates || skipped {
		t.Fatalf("0.9 K header drop: violates=%v skipped=%v", violates, skipped)
	}
	if violates, skipped := Violates(Observation{
		Kind: KindChilledDirection, Value: 12, ReturnC: f64(7), Running: &running,
	}); !violates || skipped {
		t.Fatalf("reversed chilled water: violates=%v skipped=%v", violates, skipped)
	}
	stopped := false
	if violates, skipped := Violates(Observation{
		Kind: KindChilledDirection, Value: 12, ReturnC: f64(7), Running: &stopped,
	}); violates || !skipped {
		t.Fatalf("stopped chiller is not judged: violates=%v skipped=%v", violates, skipped)
	}
	if violates, skipped := Violates(Observation{
		Kind: KindCondenserDirection, Value: 33, EnteringC: f64(28), Running: &running,
	}); violates || skipped {
		t.Fatalf("condenser rise: violates=%v skipped=%v", violates, skipped)
	}
	if violates, skipped := Violates(Observation{
		Kind: KindCondenserDirection, Value: 27, EnteringC: f64(28), Running: &running,
	}); !violates || skipped {
		t.Fatalf("reversed condenser: violates=%v skipped=%v", violates, skipped)
	}
}

func TestHeatBalanceUsesCoolingPowerAndCondenserHeat(t *testing.T) {
	// 500 + 120 = 620 against a condenser that matches. A condenser at 1000 kW does not.
	if violates, skipped := Violates(Observation{
		Kind: KindHeatBalance, Value: 500, PowersKW: []float64{120}, CondenserHeatKW: f64(620),
	}); violates || skipped {
		t.Fatalf("closed balance: violates=%v skipped=%v", violates, skipped)
	}
	heat, ok := WaterHeatKW(100, 28, 34)
	if !ok {
		t.Fatal("water heat")
	}
	if violates, skipped := Violates(Observation{
		Kind: KindHeatBalance, Value: 500, PowersKW: []float64{70, 50},
		FlowM3H: f64(100), HeatEnteringC: f64(28), HeatLeavingC: f64(34),
	}); violates || skipped {
		t.Fatalf("balance from flow (%v kW): violates=%v skipped=%v", heat, violates, skipped)
	}
	if violates, skipped := Violates(Observation{
		Kind: KindHeatBalance, Value: 500, PowersKW: []float64{120}, CondenserHeatKW: f64(1000),
	}); !violates || skipped {
		t.Fatalf("open balance: violates=%v skipped=%v", violates, skipped)
	}
	if violates, skipped := Violates(Observation{
		Kind: KindHeatBalance, Value: 500, PowersKW: []float64{120},
	}); violates || !skipped {
		t.Fatalf("no condenser heat must skip: violates=%v skipped=%v", violates, skipped)
	}
}

func TestPumpRunningWithoutAChillerIsADiagnostic(t *testing.T) {
	if !PumpWithoutChiller(true, false) {
		t.Fatal("pump on, chillers off")
	}
	if PumpWithoutChiller(true, true) || PumpWithoutChiller(false, false) {
		t.Fatal("pump with a chiller, or both stopped, is not the finding")
	}
	if _, ok := QualityReason(KindPumpWithoutChiller); ok {
		t.Fatal("the pump finding must not mark the run-state reading")
	}
	for _, kind := range []string{KindWetBulbDryBulb, KindWetBulbCondenser, KindPowerNameplate, KindCOPCarnot, KindChilledDirection, KindCondenserDirection, KindHeatBalance} {
		if _, ok := QualityReason(kind); !ok {
			t.Fatalf("%s has no quality reason", kind)
		}
	}
}
