package energy

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
	"time"
)

type phase1GoldenDataset struct {
	SchemaVersion int                `json:"schemaVersion"`
	Cases         []phase1GoldenCase `json:"cases"`
}

type phase1GoldenCase struct {
	Name                   string     `json:"name"`
	TransitionType         string     `json:"transitionType"`
	DeltaValueKWh          *float64   `json:"deltaValueKWh"`
	CurrentQuality         string     `json:"currentQuality"`
	CurrentQualityReasons  []string   `json:"currentQualityReasons"`
	ExpectFact             bool       `json:"expectFact"`
	ExpectedEnergyKWh      float64    `json:"expectedEnergyKWh"`
	ExpectedQuality        string     `json:"expectedQuality"`
	ExpectedQualityReasons []string   `json:"expectedQualityReasons"`
}

func TestPhase1EnergyGoldenDataset(t *testing.T) {
	payload, err := os.ReadFile("testdata/phase1-golden.v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var dataset phase1GoldenDataset
	if err := json.Unmarshal(payload, &dataset); err != nil {
		t.Fatal(err)
	}
	if dataset.SchemaVersion != 1 || len(dataset.Cases) == 0 {
		t.Fatalf("invalid golden dataset: %#v", dataset)
	}

	projectedAt := time.Date(2026, 8, 26, 0, 0, 0, 0, time.UTC)
	for _, testCase := range dataset.Cases {
		t.Run(testCase.Name, func(t *testing.T) {
			delta := validDelta()
			delta.TransitionType = TransitionType(testCase.TransitionType)
			delta.DeltaValue = testCase.DeltaValueKWh
			delta.CurrentQuality = testCase.CurrentQuality
			delta.CurrentQualityReasons = append([]string(nil), testCase.CurrentQualityReasons...)

			fact, err := BuildFact(delta, validBinding(), projectedAt)
			if !testCase.ExpectFact {
				if err == nil {
					t.Fatalf("BuildFact() error = nil, fact=%#v", fact)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if fact.EnergyKWh != testCase.ExpectedEnergyKWh || fact.Quality != testCase.ExpectedQuality {
				t.Fatalf("fact=%#v", fact)
			}
			if !slices.Equal(fact.QualityReasons, testCase.ExpectedQualityReasons) {
				t.Fatalf("quality reasons=%#v want=%#v", fact.QualityReasons, testCase.ExpectedQualityReasons)
			}
		})
	}
}
