package analyticsmodel

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func assertClosePtr(t *testing.T, name string, got *float64, want float64) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s = nil, want %v", name, want)
	}
	if math.Abs(*got-want) > 1e-6 {
		t.Fatalf("%s = %v, want %v", name, *got, want)
	}
}

func TestComputeLayeredEfficiencyNominalAndJSONTags(t *testing.T) {
	got := ComputeLayeredEfficiency(LayeredEfficiencyInput{
		CoolingKWh:                 500,
		PlantElectricityKWh:        100,
		ChillerElectricityKWh:      70,
		ChilledPumpElectricityKWh:  12.5,
		CoolingPumpElectricityKWh:  10,
		CoolingTowerElectricityKWh: 7.5,
	})

	assertClosePtr(t, "PlantCOP", got.PlantCOP, 5.0)
	assertClosePtr(t, "ChillerCOP", got.ChillerCOP, 500.0/70.0)
	assertClosePtr(t, "ChilledPumpWTF", got.ChilledPumpWTF, 40.0)
	assertClosePtr(t, "CoolingPumpWTF", got.CoolingPumpWTF, 50.0)
	assertClosePtr(t, "TowerPowerShare", got.TowerPowerShare, 0.075)
	if got.SynthesizedTotal {
		t.Fatalf("expected SynthesizedTotal = false, got true")
	}

	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(raw)
	for _, key := range []string{`"plantCOP":`, `"chillerCOP":`, `"chilledPumpWTF":`, `"coolingPumpWTF":`, `"towerPowerShare":`} {
		if !strings.Contains(encoded, key) {
			t.Fatalf("expected JSON key %s in %s", key, encoded)
		}
	}
}

func TestComputeLayeredEfficiencyDeadbandsBoundsAndNonFiniteGuards(t *testing.T) {
	t.Run("cooling below deadband suppresses all cooling ratios while allowing tower share", func(t *testing.T) {
		got := ComputeLayeredEfficiency(LayeredEfficiencyInput{
			CoolingKWh:                 0.99,
			PlantElectricityKWh:        10,
			ChillerElectricityKWh:      8,
			ChilledPumpElectricityKWh:  1,
			CoolingPumpElectricityKWh:  1,
			CoolingTowerElectricityKWh: 0,
		})
		if got.PlantCOP != nil || got.ChillerCOP != nil || got.ChilledPumpWTF != nil || got.CoolingPumpWTF != nil {
			t.Fatalf("expected all COP/WTF ratios nil below cooling deadband, got %+v", got)
		}
		assertClosePtr(t, "TowerPowerShare", got.TowerPowerShare, 0.0)
	})

	t.Run("boundary values included and out-of-bounds excluded", func(t *testing.T) {
		atBounds := ComputeLayeredEfficiency(LayeredEfficiencyInput{
			CoolingKWh:                 15.0,
			PlantElectricityKWh:        1.25, // PlantCOP = 12.0
			ChillerElectricityKWh:      1.0,  // ChillerCOP = 15.0
			ChilledPumpElectricityKWh:  0.5,  // ChilledPumpWTF = 30.0
			CoolingPumpElectricityKWh:  0.49, // below 0.5 deadband
			CoolingTowerElectricityKWh: 1.25, // TowerPowerShare = 1.0
		})
		assertClosePtr(t, "PlantCOP", atBounds.PlantCOP, 12.0)
		assertClosePtr(t, "ChillerCOP", atBounds.ChillerCOP, 15.0)
		assertClosePtr(t, "ChilledPumpWTF", atBounds.ChilledPumpWTF, 30.0)
		if atBounds.CoolingPumpWTF != nil {
			t.Fatalf("expected CoolingPumpWTF nil below 0.5 kWh deadband, got %v", *atBounds.CoolingPumpWTF)
		}
		assertClosePtr(t, "TowerPowerShare", atBounds.TowerPowerShare, 1.0)

		exceedsBounds := ComputeLayeredEfficiency(LayeredEfficiencyInput{
			CoolingKWh:                 101.0,
			PlantElectricityKWh:        8.0,  // PlantCOP = 12.625 > 12
			ChillerElectricityKWh:      6.5,  // ChillerCOP = 15.53 > 15
			ChilledPumpElectricityKWh:  1.0,  // ChilledPumpWTF = 101 > 100
			CoolingPumpElectricityKWh:  1.01, // CoolingPumpWTF = 100
			CoolingTowerElectricityKWh: 8.5,  // Tower > Plant -> nil
		})
		if exceedsBounds.PlantCOP != nil || exceedsBounds.ChillerCOP != nil || exceedsBounds.ChilledPumpWTF != nil || exceedsBounds.TowerPowerShare != nil {
			t.Fatalf("expected out-of-bound metrics suppressed, got %+v", exceedsBounds)
		}
		assertClosePtr(t, "CoolingPumpWTF", exceedsBounds.CoolingPumpWTF, 100.0)

		belowMinCOP := ComputeLayeredEfficiency(LayeredEfficiencyInput{
			CoolingKWh:            5.0,
			PlantElectricityKWh:   6.0, // 0.833 < 1
			ChillerElectricityKWh: 5.5, // 0.909 < 1
		})
		if belowMinCOP.PlantCOP != nil || belowMinCOP.ChillerCOP != nil {
			t.Fatalf("expected sub-1 COP suppressed, got %+v", belowMinCOP)
		}
	})

	t.Run("NaN and Inf and negative inputs are suppressed cleanly", func(t *testing.T) {
		got := ComputeLayeredEfficiency(LayeredEfficiencyInput{
			CoolingKWh:                 math.NaN(),
			PlantElectricityKWh:        math.Inf(1),
			ChillerElectricityKWh:      math.Inf(-1),
			ChilledPumpElectricityKWh:  math.NaN(),
			CoolingPumpElectricityKWh:  -2.0,
			CoolingTowerElectricityKWh: -1.0,
		})
		if got.PlantCOP != nil || got.ChillerCOP != nil || got.ChilledPumpWTF != nil || got.CoolingPumpWTF != nil || got.TowerPowerShare != nil {
			t.Fatalf("expected all non-finite/negative metrics suppressed, got %+v", got)
		}

		towerNegative := ComputeLayeredEfficiency(LayeredEfficiencyInput{
			CoolingKWh:                 50,
			PlantElectricityKWh:        10,
			CoolingTowerElectricityKWh: -0.1,
		})
		assertClosePtr(t, "PlantCOP", towerNegative.PlantCOP, 5.0)
		if towerNegative.TowerPowerShare != nil {
			t.Fatalf("expected negative tower power suppressed, got %v", *towerNegative.TowerPowerShare)
		}
	})

	t.Run("synthesized total preserves flag while computing valid plant COP", func(t *testing.T) {
		got := ComputeLayeredEfficiency(LayeredEfficiencyInput{
			CoolingKWh:                 450,
			PlantElectricityKWh:        90,
			ChillerElectricityKWh:      60,
			CoolingTowerElectricityKWh: 9,
			SynthesizedTotal:           true,
		})
		if !got.SynthesizedTotal {
			t.Fatal("expected SynthesizedTotal = true")
		}
		assertClosePtr(t, "PlantCOP", got.PlantCOP, 5.0)
		assertClosePtr(t, "ChillerCOP", got.ChillerCOP, 7.5)
		assertClosePtr(t, "TowerPowerShare", got.TowerPowerShare, 0.1)
	})
}

func TestComputeMeterBalanceCases(t *testing.T) {
	t.Run("balanced within default 5 percent threshold", func(t *testing.T) {
		got := ComputeMeterBalance(100, 95, 0, false)
		if got.Status != MeterBalanceBalanced || got.DiffKWh != 5 {
			t.Fatalf("expected BALANCED with diff 5, got %+v", got)
		}
		assertClosePtr(t, "ImbalanceRatio", got.ImbalanceRatio, 0.05)
	})

	t.Run("positive unmetered gap exceeds threshold", func(t *testing.T) {
		got := ComputeMeterBalance(100, 80, 0, false)
		if got.Status != MeterBalanceUnmeteredGap || got.DiffKWh != 20 {
			t.Fatalf("expected UNMETERED_GAP with diff 20, got %+v", got)
		}
		assertClosePtr(t, "ImbalanceRatio", got.ImbalanceRatio, 0.20)
	})

	t.Run("negative submeter exceeds main anomaly", func(t *testing.T) {
		got := ComputeMeterBalance(100, 115, 0.05, false)
		if got.Status != MeterBalanceSubmeterExceedsMain || got.DiffKWh != -15 {
			t.Fatalf("expected SUBMETER_EXCEEDS_MAIN with diff -15, got %+v", got)
		}
		assertClosePtr(t, "ImbalanceRatio", got.ImbalanceRatio, -0.15)
	})

	t.Run("no main meter when site kwh non-positive", func(t *testing.T) {
		got := ComputeMeterBalance(0, 42, 0.05, false)
		if got.Status != MeterBalanceNoMainMeter || got.ImbalanceRatio != nil || got.DiffKWh != -42 {
			t.Fatalf("expected NO_MAIN_METER with nil ratio, got %+v", got)
		}
	})

	t.Run("synthesized site total suppresses imbalance comparison", func(t *testing.T) {
		got := ComputeMeterBalance(0, 120, 0.05, true)
		if got.Status != MeterBalanceSynthesized || got.ImbalanceRatio != nil {
			t.Fatalf("expected SYNTHESIZED with nil ratio even when siteKWh=0, got %+v", got)
		}
		gotNonZero := ComputeMeterBalance(100, 80, 0.05, true)
		if gotNonZero.Status != MeterBalanceSynthesized || gotNonZero.ImbalanceRatio != nil {
			t.Fatalf("expected SYNTHESIZED with nil ratio when synthesized=true, got %+v", gotNonZero)
		}
	})

	t.Run("NaN and Inf inputs degrade safely to finite JSON", func(t *testing.T) {
		got := ComputeMeterBalance(math.NaN(), math.Inf(1), math.NaN(), false)
		if got.Status != MeterBalanceNoMainMeter || got.ImbalanceRatio != nil {
			t.Fatalf("expected NO_MAIN_METER with nil ratio on NaN/Inf, got %+v", got)
		}
		if _, err := json.Marshal(got); err != nil {
			t.Fatalf("expected JSON-safe MeterBalanceSummary, got %v", err)
		}
	})
}
