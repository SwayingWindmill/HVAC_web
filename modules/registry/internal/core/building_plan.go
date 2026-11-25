package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strings"
)

// SiteBuildingRoom declares one rectangular architectural space on a floor for the 3D digital twin.
type SiteBuildingRoom struct {
	ID   string     `json:"id"`
	Name string     `json:"name"`
	Kind string     `json:"kind"`
	X    [2]float64 `json:"x"`
	Z    [2]float64 `json:"z"`
}

// SiteBuildingFloor declares one floor slab and its rooms in metres.
type SiteBuildingFloor struct {
	Level  int                `json:"level"`
	Label  string             `json:"label"`
	Height float64            `json:"height"`
	Rooms  []SiteBuildingRoom `json:"rooms"`
}

// SiteBuildingPlan declares the terminal-side building attached to a Site's digital twin.
type SiteBuildingPlan struct {
	Name   string              `json:"name"`
	Width  float64             `json:"width"`
	Depth  float64             `json:"depth"`
	Floors []SiteBuildingFloor `json:"floors"`
}

// OptionalBuildingPlan distinguishes omitted field (preserve existing value on PATCH),
// explicit JSON null (clear value), and a concrete SiteBuildingPlan object.
type OptionalBuildingPlan struct {
	Set   bool
	Value *SiteBuildingPlan
}

// UnmarshalJSON decodes null or a strict SiteBuildingPlan object and marks Set = true.
func (o *OptionalBuildingPlan) UnmarshalJSON(data []byte) error {
	o.Set = true
	trimmed := bytes.TrimSpace(data)
	if bytes.Equal(trimmed, []byte("null")) {
		o.Value = nil
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	var plan SiteBuildingPlan
	if err := dec.Decode(&plan); err != nil {
		return ErrInvalidMutation
	}
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ErrInvalidMutation
	}
	o.Value = &plan
	return nil
}

// MarshalJSON encodes the explicit value or null.
func (o OptionalBuildingPlan) MarshalJSON() ([]byte, error) {
	if !o.Set || o.Value == nil {
		return []byte("null"), nil
	}
	return json.Marshal(o.Value)
}

func isValidBuildingRoomKind(kind string) bool {
	switch kind {
	case "hall", "private", "smallPrivate", "kitchen", "plantRoom", "service", "corridor", "core", "shaft":
		return true
	default:
		return false
	}
}

func isFinitePositiveBounded(v, maxVal float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v > 0 && v <= maxVal
}

func isFiniteNonNegBounded(v, maxVal float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 && v <= maxVal
}

// Validate enforces structural, room-kind, and geometric bounds invariants for a SiteBuildingPlan.
func (p SiteBuildingPlan) Validate() error {
	if strings.TrimSpace(p.Name) == "" || len(p.Name) > 128 {
		return ErrInvalidMutation
	}
	if !isFinitePositiveBounded(p.Width, 1000) || !isFinitePositiveBounded(p.Depth, 1000) {
		return ErrInvalidMutation
	}
	if len(p.Floors) < 1 || len(p.Floors) > 100 {
		return ErrInvalidMutation
	}
	seenLevels := make(map[int]struct{}, len(p.Floors))
	for _, floor := range p.Floors {
		if floor.Level < 1 || floor.Level > 200 {
			return ErrInvalidMutation
		}
		if _, dup := seenLevels[floor.Level]; dup {
			return ErrInvalidMutation
		}
		seenLevels[floor.Level] = struct{}{}
		if strings.TrimSpace(floor.Label) == "" || len(floor.Label) > 128 {
			return ErrInvalidMutation
		}
		if !isFinitePositiveBounded(floor.Height, 30) {
			return ErrInvalidMutation
		}
		if len(floor.Rooms) < 1 || len(floor.Rooms) > 200 {
			return ErrInvalidMutation
		}
		seenRooms := make(map[string]struct{}, len(floor.Rooms))
		for _, room := range floor.Rooms {
			if strings.TrimSpace(room.ID) == "" || len(room.ID) > 64 {
				return ErrInvalidMutation
			}
			if _, dup := seenRooms[room.ID]; dup {
				return ErrInvalidMutation
			}
			seenRooms[room.ID] = struct{}{}
			if strings.TrimSpace(room.Name) == "" || len(room.Name) > 128 || !isValidBuildingRoomKind(room.Kind) {
				return ErrInvalidMutation
			}
			if !isFiniteNonNegBounded(room.X[0], p.Width) || !isFiniteNonNegBounded(room.X[1], p.Width) || room.X[0] >= room.X[1] {
				return ErrInvalidMutation
			}
			if !isFiniteNonNegBounded(room.Z[0], p.Depth) || !isFiniteNonNegBounded(room.Z[1], p.Depth) || room.Z[0] >= room.Z[1] {
				return ErrInvalidMutation
			}
		}
	}
	return nil
}
