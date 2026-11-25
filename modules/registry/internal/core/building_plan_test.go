package core

import (
	"encoding/json"
	"errors"
	"testing"
)

func sampleValidBuildingPlan() SiteBuildingPlan {
	return SiteBuildingPlan{
		Name:  "松雅轩",
		Width: 28,
		Depth: 18,
		Floors: []SiteBuildingFloor{
			{
				Level:  1,
				Label:  "1F 大厅",
				Height: 4.8,
				Rooms: []SiteBuildingRoom{
					{ID: "1-hall", Name: "大厅", Kind: "hall", X: [2]float64{0, 22}, Z: [2]float64{0, 18}},
					{ID: "1-shaft", Name: "管井", Kind: "shaft", X: [2]float64{26, 28}, Z: [2]float64{6, 8.5}},
				},
			},
		},
	}
}

func TestSiteBuildingPlanValidation(t *testing.T) {
	valid := sampleValidBuildingPlan()
	if err := valid.Validate(); err != nil {
		t.Fatalf("expected valid buildingPlan, got %v", err)
	}

	outOfBounds := sampleValidBuildingPlan()
	outOfBounds.Floors[0].Rooms[0].X = [2]float64{0, 29}
	if err := outOfBounds.Validate(); !errors.Is(err, ErrInvalidMutation) {
		t.Fatalf("expected ErrInvalidMutation for room exceeding building width, got %v", err)
	}

	badKind := sampleValidBuildingPlan()
	badKind.Floors[0].Rooms[0].Kind = "rooftop"
	if err := badKind.Validate(); !errors.Is(err, ErrInvalidMutation) {
		t.Fatalf("expected ErrInvalidMutation for unsupported room kind, got %v", err)
	}

	dupFloor := sampleValidBuildingPlan()
	dupFloor.Floors = append(dupFloor.Floors, SiteBuildingFloor{
		Level: 1, Label: "dup", Height: 4, Rooms: []SiteBuildingRoom{{ID: "r", Name: "R", Kind: "hall", X: [2]float64{0, 10}, Z: [2]float64{0, 10}}},
	})
	if err := dupFloor.Validate(); !errors.Is(err, ErrInvalidMutation) {
		t.Fatalf("expected ErrInvalidMutation for duplicate floor level, got %v", err)
	}

	dupRoom := sampleValidBuildingPlan()
	dupRoom.Floors[0].Rooms = append(dupRoom.Floors[0].Rooms, SiteBuildingRoom{
		ID: "1-hall", Name: "Dup", Kind: "service", X: [2]float64{22, 26}, Z: [2]float64{0, 6},
	})
	if err := dupRoom.Validate(); !errors.Is(err, ErrInvalidMutation) {
		t.Fatalf("expected ErrInvalidMutation for duplicate room id, got %v", err)
	}
}

func TestOptionalBuildingPlanJSONSemantics(t *testing.T) {
	var omitted SiteMutation
	if err := json.Unmarshal([]byte(`{"code":"site-b","displayName":"Site B","timezone":"Asia/Shanghai","status":"ACTIVE","meta":{"expectedRevision":1,"idempotencyKey":"site-b-opt-01","reason":"omit plan"}}`), &omitted); err != nil {
		t.Fatalf("unmarshal omitted: %v", err)
	}
	if omitted.BuildingPlan.Set || omitted.BuildingPlan.Value != nil {
		t.Fatalf("expected omitted BuildingPlan Set=false, got %+v", omitted.BuildingPlan)
	}
	omittedWire, _ := json.Marshal(omitted)
	if contains := string(omittedWire); contains != "" {
		var round map[string]any
		_ = json.Unmarshal(omittedWire, &round)
		if _, exists := round["buildingPlan"]; exists {
			t.Fatalf("expected omitted BuildingPlan absent in serialized JSON, got %s", contains)
		}
	}

	var cleared SiteMutation
	if err := json.Unmarshal([]byte(`{"code":"site-b","displayName":"Site B","timezone":"Asia/Shanghai","status":"ACTIVE","buildingPlan":null,"meta":{"expectedRevision":1,"idempotencyKey":"site-b-clr-01","reason":"clear plan"}}`), &cleared); err != nil {
		t.Fatalf("unmarshal null: %v", err)
	}
	if !cleared.BuildingPlan.Set || cleared.BuildingPlan.Value != nil {
		t.Fatalf("expected explicit null BuildingPlan Set=true Value=nil, got %+v", cleared.BuildingPlan)
	}
	clearedWire, _ := json.Marshal(cleared)
	var roundCleared map[string]any
	_ = json.Unmarshal(clearedWire, &roundCleared)
	if val, exists := roundCleared["buildingPlan"]; !exists || val != nil {
		t.Fatalf("expected explicit null buildingPlan key present in serialized JSON, got %s", string(clearedWire))
	}
}
