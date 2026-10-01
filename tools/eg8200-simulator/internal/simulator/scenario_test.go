package simulator

import (
	"strings"
	"testing"
	"time"
)

func TestDecodeScenarioJSONUsesStrictStaticContract(t *testing.T) {
	raw := `{
		"schemaVersion":1,
		"mode":"STATIC",
		"name":"summer-design",
		"description":"design point",
		"inputs":{"ambientDryBulbC":35,"ambientWetBulbC":28,"coolingLoadKw":850}
	}`
	scenario, err := DecodeScenarioJSON(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if scenario.Mode != ScenarioModeStatic || scenario.Name != "summer-design" || scenario.Description != "design point" {
		t.Fatalf("unexpected STATIC scenario metadata: %#v", scenario)
	}
	if got := scenario.InputsAt(24 * time.Hour); got != (ScenarioInputs{AmbientDryBulbC: 35, AmbientWetBulbC: 28, CoolingLoadKW: 850}) {
		t.Fatalf("STATIC inputs changed over time: %#v", got)
	}
}

func TestDecodeScenarioJSONScenarioIsStepwiseAndHoldsFinalStep(t *testing.T) {
	raw := `{
		"schemaVersion":1,
		"mode":"SCENARIO",
		"steps":[
			{"offset":"0s","inputs":{"ambientDryBulbC":30,"ambientWetBulbC":24,"coolingLoadKw":420}},
			{"offset":"1h","inputs":{"ambientDryBulbC":33,"ambientWetBulbC":25,"coolingLoadKw":560}}
		]
	}`
	scenario, err := DecodeScenarioJSON(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if got := scenario.InputsAt(59 * time.Minute); got.CoolingLoadKW != 420 {
		t.Fatalf("scenario interpolated or advanced early: %#v", got)
	}
	if got := scenario.InputsAt(time.Hour); got.CoolingLoadKW != 560 {
		t.Fatalf("scenario did not switch at step boundary: %#v", got)
	}
	if got := scenario.InputsAt(48 * time.Hour); got.CoolingLoadKW != 560 {
		t.Fatalf("scenario did not hold final step: %#v", got)
	}
}

func TestDecodeScenarioJSONRejectsNonCanonicalShapes(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "missing mode",
			raw:  `{"schemaVersion":1,"inputs":{"ambientDryBulbC":35,"ambientWetBulbC":28,"coolingLoadKw":850}}`,
			want: "mode is required",
		},
		{
			name: "load fraction alias",
			raw:  `{"schemaVersion":1,"mode":"STATIC","inputs":{"ambientDryBulbC":35,"ambientWetBulbC":28,"coolingLoadKw":850,"loadFraction":0.7}}`,
			want: "unknown field",
		},
		{
			name: "static with steps",
			raw:  `{"schemaVersion":1,"mode":"STATIC","inputs":{"ambientDryBulbC":35,"ambientWetBulbC":28,"coolingLoadKw":850},"steps":[]}`,
			want: "must not contain steps",
		},
		{
			name: "scenario with top level inputs",
			raw:  `{"schemaVersion":1,"mode":"SCENARIO","inputs":{"ambientDryBulbC":35,"ambientWetBulbC":28,"coolingLoadKw":850},"steps":[{"offset":"0s","inputs":{"ambientDryBulbC":30,"ambientWetBulbC":24,"coolingLoadKw":420}}]}`,
			want: "must not contain top-level inputs",
		},
		{
			name: "missing cooling load",
			raw:  `{"schemaVersion":1,"mode":"STATIC","inputs":{"ambientDryBulbC":35,"ambientWetBulbC":28}}`,
			want: "require ambientDryBulbC, ambientWetBulbC and coolingLoadKw",
		},
		{
			name: "invalid wet bulb",
			raw:  `{"schemaVersion":1,"mode":"STATIC","inputs":{"ambientDryBulbC":25,"ambientWetBulbC":28,"coolingLoadKw":850}}`,
			want: "ambientWetBulbC must not exceed ambientDryBulbC",
		},
		{
			name: "negative cooling load",
			raw:  `{"schemaVersion":1,"mode":"STATIC","inputs":{"ambientDryBulbC":35,"ambientWetBulbC":28,"coolingLoadKw":-1}}`,
			want: "coolingLoadKw must be non-negative",
		},
		{
			name: "first step not zero",
			raw:  `{"schemaVersion":1,"mode":"SCENARIO","steps":[{"offset":"1m","inputs":{"ambientDryBulbC":30,"ambientWetBulbC":24,"coolingLoadKw":420}}]}`,
			want: "first step must start at 0s",
		},
		{
			name: "unordered steps",
			raw:  `{"schemaVersion":1,"mode":"SCENARIO","steps":[{"offset":"0s","inputs":{"ambientDryBulbC":30,"ambientWetBulbC":24,"coolingLoadKw":420}},{"offset":"1h","inputs":{"ambientDryBulbC":33,"ambientWetBulbC":25,"coolingLoadKw":560}},{"offset":"30m","inputs":{"ambientDryBulbC":32,"ambientWetBulbC":25,"coolingLoadKw":500}}]}`,
			want: "step offsets must be strictly increasing",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := DecodeScenarioJSON(strings.NewReader(test.raw))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q error, got %v", test.want, err)
			}
		})
	}
}

func TestDecodeScenarioCSVParsesOnlyFixedScenarioSteps(t *testing.T) {
	raw := "offset,ambientDryBulbC,ambientWetBulbC,coolingLoadKw\n0s,30,24,420\n1h,33,25,560\n"
	scenario, err := DecodeScenarioCSV(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if scenario.Mode != ScenarioModeScenario || len(scenario.Steps) != 2 {
		t.Fatalf("unexpected CSV scenario: %#v", scenario)
	}
	if got := scenario.InputsAt(2 * time.Hour); got.CoolingLoadKW != 560 {
		t.Fatalf("CSV scenario did not use canonical step model: %#v", got)
	}
}

func TestDecodeScenarioCSVRejectsAliases(t *testing.T) {
	raw := "offset,ambientDryBulbC,ambientWetBulbC,loadFraction\n0s,30,24,0.5\n"
	_, err := DecodeScenarioCSV(strings.NewReader(raw))
	if err == nil || !strings.Contains(err.Error(), "header must be offset,ambientDryBulbC,ambientWetBulbC,coolingLoadKw") {
		t.Fatalf("expected canonical CSV header rejection, got %v", err)
	}
}

func TestScenarioInputsDrivePlantPhysics(t *testing.T) {
	scenario := Scenario{
		SchemaVersion: ScenarioSchemaVersion,
		Mode:          ScenarioModeScenario,
		Steps: []ScenarioStep{
			{Offset: 0, Inputs: ScenarioInputs{AmbientDryBulbC: 30, AmbientWetBulbC: 24, CoolingLoadKW: 420}},
			{Offset: 2 * time.Minute, Inputs: ScenarioInputs{AmbientDryBulbC: 36, AmbientWetBulbC: 28, CoolingLoadKW: 900}},
		},
	}
	if err := scenario.Validate(); err != nil {
		t.Fatal(err)
	}
	plant := NewPlant(testPlantConfig(), scenario, time.Unix(0, 0).UTC())
	before := plant.Tick(time.Minute).Devices["CHILLER-01"]["coolingCapacityKw"].(float64)
	after := plant.Tick(time.Minute + time.Second)
	afterCapacity := after.Devices["CHILLER-01"]["coolingCapacityKw"].(float64)
	if before <= 0 || before >= 420 || afterCapacity <= before || afterCapacity >= 900 {
		t.Fatalf("plant did not consume stepwise coolingLoadKw through physical dynamics: before=%v after=%v", before, afterCapacity)
	}
	if got := after.Devices["WEATHER-STATION-01"]["ambientDryBulbTemperatureC"]; got != 36.0 {
		t.Fatalf("plant did not consume scenario ambient inputs: %v", got)
	}
	settled := plant.Tick(10 * time.Minute).Devices["CHILLER-01"]["coolingCapacityKw"].(float64)
	if settled < 890 || settled > 900 {
		t.Fatalf("plant did not settle near final scenario cooling load: %v", settled)
	}
}
