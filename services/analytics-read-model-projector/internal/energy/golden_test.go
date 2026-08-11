package energy

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"
)

type goldenDataset struct {
	SchemaVersion int          `json:"schemaVersion"`
	TelemetryKey  string       `json:"telemetryKey"`
	Cases         []goldenCase `json:"cases"`
}

type goldenCase struct {
	ID                string   `json:"id"`
	PreviousValue     float64  `json:"previousValue"`
	CurrentValue      float64  `json:"currentValue"`
	PreviousQuality   string   `json:"previousQuality"`
	CurrentQuality    string   `json:"currentQuality"`
	ExpectedEnergyKWh float64  `json:"expectedEnergyKWh"`
	ExpectedQuality   string   `json:"expectedQuality"`
	ExpectedReasons   []string `json:"expectedReasons"`
}

func TestPhase1EnergyGoldenDataset(t *testing.T) {
	raw, err := os.ReadFile("testdata/phase1-golden.v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var dataset goldenDataset
	if err := json.Unmarshal(raw, &dataset); err != nil {
		t.Fatal(err)
	}
	if dataset.SchemaVersion != 1 || dataset.TelemetryKey != CumulativeElectricityTelemetryKey || len(dataset.Cases) < 4 {
		t.Fatalf("invalid phase1 golden dataset: %#v", dataset)
	}
	start := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	for index, testCase := range dataset.Cases {
		t.Run(testCase.ID, func(t *testing.T) {
			fact, err := BuildFact(Candidate{
				PreviousObservationID: "018f3e00-0000-7000-8000-000000000001",
				CurrentObservationID:  "018f3e00-0000-7000-8000-000000000002",
				TenantID:              "018f3d00-0000-7000-8000-000000000001",
				OrganizationID:        "018f3e00-0000-7000-8000-000000000001",
				SiteID:                "018f3e00-1000-7000-8000-000000000001",
				DeviceID:              "018f3e00-4000-7000-8000-000000000005",
				PointID:               "018f3e00-5000-7000-8000-000000000005",
				TelemetryKey:          dataset.TelemetryKey,
				PreviousValue:         testCase.PreviousValue,
				CurrentValue:          testCase.CurrentValue,
				PreviousQuality:       testCase.PreviousQuality,
				CurrentQuality:        testCase.CurrentQuality,
				PreviousSampledAt:     start.Add(time.Duration(index) * time.Hour),
				CurrentSampledAt:      start.Add(time.Duration(index+1) * time.Hour),
				SourceOffset:          uint64(index + 1),
			}, start.Add(24*time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			if fact.EnergyKWh != testCase.ExpectedEnergyKWh {
				t.Fatalf("energy_kwh=%v want=%v", fact.EnergyKWh, testCase.ExpectedEnergyKWh)
			}
			if fact.Quality != testCase.ExpectedQuality {
				t.Fatalf("quality=%s want=%s", fact.Quality, testCase.ExpectedQuality)
			}
			if !reflect.DeepEqual(fact.QualityReasons, testCase.ExpectedReasons) {
				t.Fatalf("quality_reasons=%v want=%v", fact.QualityReasons, testCase.ExpectedReasons)
			}
			if fact.EnergyKWh < 0 {
				t.Fatalf("golden dataset produced negative interval energy: %v", fact.EnergyKWh)
			}
		})
	}
}
