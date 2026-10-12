package simulator

import (
	"math"
	"os"
	"strings"
	"testing"
	"time"
)

const dailyScenarioJSON = `{
	"schemaVersion":1,
	"mode":"DAILY",
	"daily":{
		"timezone":"Asia/Shanghai",
		"dryBulbC":{"mean":31,"amplitude":4,"peakHour":15},
		"wetBulbC":{"mean":26,"amplitude":2,"peakHour":15},
		"dayToDayC":2,
		"coolingLoadKw":{"unoccupied":300,"occupied":860,"occupiedFrom":8,"occupiedTo":20,"perDryBulbC":25}
	}
}`

func decodeDaily(t *testing.T) Scenario {
	t.Helper()
	scenario, err := DecodeScenarioJSON(strings.NewReader(dailyScenarioJSON))
	if err != nil {
		t.Fatal(err)
	}
	return scenario
}

// shanghai is a Site-local wall-clock instant on 12 October 2026.
func shanghai(hour, minute int) time.Time {
	return time.Date(2026, 10, 12, hour-8, minute, 0, 0, time.UTC)
}

func TestDailyScenarioFollowsTheSitesClock(t *testing.T) {
	scenario := decodeDaily(t)
	afternoon := scenario.InputsFor(0, shanghai(15, 0))
	night := scenario.InputsFor(0, shanghai(3, 0))
	offset := afternoon.AmbientDryBulbC - 35 // the day's own offset, the same all day
	if offset < -2 || offset > 2 {
		t.Fatalf("day offset out of range: %v", offset)
	}
	if got := night.AmbientDryBulbC; math.Abs(got-(27+offset)) > 1e-9 {
		t.Fatalf("03:00 dry bulb = %v, want %v (mean − amplitude + day offset)", got, 27+offset)
	}
	if afternoon.AmbientWetBulbC > afternoon.AmbientDryBulbC || night.AmbientWetBulbC > night.AmbientDryBulbC {
		t.Fatalf("wet bulb above dry bulb: %#v %#v", afternoon, night)
	}
	// Occupied afternoon load sits well above the unoccupied night, and warmer air adds load.
	if afternoon.CoolingLoadKW < 800 || night.CoolingLoadKW > 300 {
		t.Fatalf("load afternoon=%v night=%v", afternoon.CoolingLoadKW, night.CoolingLoadKW)
	}
}

func TestDailyScenarioRampsIntoOccupancyAndVariesByDay(t *testing.T) {
	scenario := decodeDaily(t)
	before := scenario.InputsFor(0, shanghai(7, 0)).CoolingLoadKW
	during := scenario.InputsFor(0, shanghai(8, 0)).CoolingLoadKW
	after := scenario.InputsFor(0, shanghai(9, 0)).CoolingLoadKW
	if !(before < during && during < after) {
		t.Fatalf("occupancy ramp not smooth: 07:00=%v 08:00=%v 09:00=%v", before, during, after)
	}
	today := scenario.InputsFor(0, shanghai(15, 0)).AmbientDryBulbC
	if again := scenario.InputsFor(0, shanghai(15, 0)); again.AmbientDryBulbC != today {
		t.Fatal("the same instant must give the same weather")
	}
	differs := false
	for day := 1; day <= 7 && !differs; day++ {
		differs = scenario.InputsFor(0, shanghai(15, 0).AddDate(0, 0, day)).AmbientDryBulbC != today
	}
	if !differs {
		t.Fatal("a week of afternoons should not all share one temperature")
	}
}

func TestDailyScenarioRejectsAnIncompleteProfile(t *testing.T) {
	for _, raw := range []string{
		strings.Replace(dailyScenarioJSON, `"timezone":"Asia/Shanghai"`, `"timezone":"Mars/Base"`, 1),
		strings.Replace(dailyScenarioJSON, `"occupiedFrom":8,"occupiedTo":20`, `"occupiedFrom":20,"occupiedTo":8`, 1),
		strings.Replace(dailyScenarioJSON, `"wetBulbC":{"mean":26`, `"wetBulbC":{"mean":32`, 1),
		strings.Replace(dailyScenarioJSON, `"mode":"DAILY"`, `"mode":"DAILY","inputs":{"ambientDryBulbC":34,"ambientWetBulbC":27,"coolingLoadKw":864}`, 1),
	} {
		if _, err := DecodeScenarioJSON(strings.NewReader(raw)); err == nil {
			t.Fatalf("accepted invalid DAILY scenario: %s", raw)
		}
	}
}

func TestDailyScenarioDrivesThePlantByWallClock(t *testing.T) {
	config := testPlantConfig()
	scenario := decodeDaily(t)
	plant := NewPlant(config, scenario, shanghai(3, 0))
	night := plant.TickTo(shanghai(3, 10))
	day := plant.TickTo(shanghai(15, 0))
	if day.Devices["WEATHER-STATION-01"]["ambientDryBulbTemperatureC"] == night.Devices["WEATHER-STATION-01"]["ambientDryBulbTemperatureC"] {
		t.Fatal("weather station reads the same at night and in the afternoon")
	}
}

// The local Sites run these days; a broken file would stop a simulator at start.
func TestLocalDailyScenariosDecode(t *testing.T) {
	for _, name := range []string{"scenario-daily-a.json", "scenario-daily-b.json"} {
		file, err := os.Open("../../configs/" + name)
		if err != nil {
			t.Fatal(err)
		}
		scenario, err := DecodeScenarioJSON(file)
		_ = file.Close()
		if err != nil || scenario.Mode != ScenarioModeDaily {
			t.Fatalf("%s: mode=%q err=%v", name, scenario.Mode, err)
		}
	}
}
