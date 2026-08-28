package simulator

import (
	"os"
	"testing"
)

func TestGeneratedCentralPlantConfigLoadsCanonicalObservedAndCommandPoints(t *testing.T) {
	file, err := os.Open("../../configs/central-plant.local.json")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	config, err := DecodeConfig(file)
	if err != nil {
		t.Fatalf("generated central-plant config is invalid: %v", err)
	}
	if len(config.Points) != 61 {
		t.Fatalf("expected 61 canonical Points, got %d", len(config.Points))
	}
	commandCount := 0
	pointIDs := map[string]struct{}{}
	for _, point := range config.Points {
		if point.PointType == "COMMAND" {
			commandCount++
		}
		if _, duplicate := pointIDs[point.PointID]; duplicate {
			t.Fatalf("duplicate canonical pointId %s", point.PointID)
		}
		pointIDs[point.PointID] = struct{}{}
	}
	if commandCount != 17 {
		t.Fatalf("expected 17 COMMAND Points, got %d", commandCount)
	}
	scheduler, err := NewMeasurementScheduler(config)
	if err != nil {
		t.Fatal(err)
	}
	if len(scheduler.points) != 44 {
		t.Fatalf("COMMAND Points leaked into measurement scheduler: scheduled=%d", len(scheduler.points))
	}
}
