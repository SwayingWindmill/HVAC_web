package edgecontrol

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
)

// ErrInvalidPointMetadata indicates an invalid or non-finite field in an edge point's sourceMetadata.
var ErrInvalidPointMetadata = errors.New("invalid edge point source metadata")

// EdgePointSourceMetadata holds strongly typed command and telemetry tuning fields from PointConfig.SourceMetadata.
type EdgePointSourceMetadata struct {
	Capability           string
	CapabilityRevision   string
	FeedbackPointKey     string
	ActuationMode        string
	ControlKind          string
	ParameterKey         string
	SafetyBoundary       SafetyBoundaryClass
	HardRealtimeCategory HardRealtimeProtectionCategory
	Minimum              *float64
	Maximum              *float64
	Step                 *float64
	Deadband             *float64
}

// ParseEdgePointSourceMetadata validates and extracts typed metadata from an untyped map.
func ParseEdgePointSourceMetadata(raw map[string]any) (EdgePointSourceMetadata, error) {
	var out EdgePointSourceMetadata
	if len(raw) == 0 {
		return out, nil
	}

	var err error
	if out.Capability, err = edgeMetadataString(raw, "capability"); err != nil {
		return EdgePointSourceMetadata{}, err
	}
	if out.CapabilityRevision, err = edgeMetadataString(raw, "capabilityRevision"); err != nil {
		return EdgePointSourceMetadata{}, err
	}
	if out.FeedbackPointKey, err = edgeMetadataString(raw, "feedbackPointKey"); err != nil {
		return EdgePointSourceMetadata{}, err
	}
	if mode, err := edgeMetadataString(raw, "actuationMode"); err != nil {
		return EdgePointSourceMetadata{}, err
	} else if mode != "" {
		out.ActuationMode = strings.ToUpper(mode)
	}
	if kind, err := edgeMetadataString(raw, "controlKind"); err != nil {
		return EdgePointSourceMetadata{}, err
	} else if kind != "" {
		out.ControlKind = strings.ToUpper(kind)
	}
	if out.ParameterKey, err = edgeMetadataString(raw, "parameterKey"); err != nil {
		return EdgePointSourceMetadata{}, err
	}
	if boundary, err := edgeMetadataString(raw, "safetyBoundary"); err != nil {
		return EdgePointSourceMetadata{}, err
	} else if boundary != "" {
		normalized := SafetyBoundaryClass(strings.ToUpper(boundary))
		if !ValidSafetyBoundaryClass(normalized) {
			return EdgePointSourceMetadata{}, fmtError("unsupported safetyBoundary " + boundary)
		}
		out.SafetyBoundary = normalized
	}
	if category, err := edgeMetadataString(raw, "hardRealtimeCategory"); err != nil {
		return EdgePointSourceMetadata{}, err
	} else if category != "" {
		normalized := HardRealtimeProtectionCategory(strings.ToUpper(category))
		if !ValidHardRealtimeProtectionCategory(normalized) {
			return EdgePointSourceMetadata{}, fmtError("unsupported hardRealtimeCategory " + category)
		}
		out.HardRealtimeCategory = normalized
	}

	if out.Minimum, err = edgeMetadataFiniteFloat(raw, "minimum"); err != nil {
		return EdgePointSourceMetadata{}, err
	}
	if out.Maximum, err = edgeMetadataFiniteFloat(raw, "maximum"); err != nil {
		return EdgePointSourceMetadata{}, err
	}
	if out.Minimum != nil && out.Maximum != nil && *out.Minimum > *out.Maximum {
		return EdgePointSourceMetadata{}, fmtError("minimum exceeds maximum")
	}
	if out.Step, err = edgeMetadataFiniteFloat(raw, "step"); err != nil {
		return EdgePointSourceMetadata{}, err
	}
	if out.Step != nil && *out.Step <= 0 {
		return EdgePointSourceMetadata{}, fmtError("step must be > 0")
	}
	if out.Deadband, err = edgeMetadataFiniteFloat(raw, "deadband"); err != nil {
		return EdgePointSourceMetadata{}, err
	}
	return out, nil
}

func fmtError(detail string) error {
	return errors.New(ErrInvalidPointMetadata.Error() + ": " + detail)
}

func edgeMetadataString(raw map[string]any, key string) (string, error) {
	val, exists := raw[key]
	if !exists || val == nil {
		return "", nil
	}
	str, ok := val.(string)
	if !ok {
		return "", fmtError(key + " must be a string")
	}
	return strings.TrimSpace(str), nil
}

func edgeMetadataFiniteFloat(raw map[string]any, key string) (*float64, error) {
	val, exists := raw[key]
	if !exists || val == nil {
		return nil, nil
	}
	var num float64
	switch typed := val.(type) {
	case float64:
		num = typed
	case float32:
		num = float64(typed)
	case int:
		num = float64(typed)
	case int32:
		num = float64(typed)
	case int64:
		num = float64(typed)
	case uint:
		num = float64(typed)
	case uint32:
		num = float64(typed)
	case uint64:
		num = float64(typed)
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return nil, fmtError(key + " must be a finite number")
		}
		num = parsed
	default:
		return nil, fmtError(key + " must be a finite number")
	}
	if math.IsNaN(num) || math.IsInf(num, 0) {
		return nil, fmtError(key + " must be a finite number")
	}
	return &num, nil
}
