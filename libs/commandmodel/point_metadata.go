package commandmodel

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ErrInvalidPointMetadata indicates a malformed or out-of-bounds field in point sourceMetadata.
var ErrInvalidPointMetadata = errors.New("invalid command point source metadata")

// ErrInvalidInterlockMetadata indicates invalid or missing fields inside point sourceMetadata interlockChecks.
var ErrInvalidInterlockMetadata = errors.New("invalid interlockChecks metadata")

// CommandPointMetadata holds validated typed control metadata extracted from a COMMAND point's sourceMetadata.
type CommandPointMetadata struct {
	Capability         Capability
	CapabilityRevision string
	FeedbackPointKey   string
	ActuationMode      string
	ControlKind        string
	ParameterKey       string
	Minimum            *float64
	Maximum            *float64
	Step               *float64
	Channel            string
	CoilAddress        *int
	PulseDurationMS    int
	InterlockChecks    []InterlockCheck
}

var commandControlMetadataKeys = []string{
	"capability",
	"capabilityRevision",
	"feedbackPointKey",
	"actuationMode",
	"controlKind",
	"parameterKey",
	"minimum",
	"maximum",
	"step",
	"channel",
	"coilAddress",
	"pulseDurationMs",
	"interlockChecks",
}

// HasCommandControlMetadata reports whether raw contains any typed command-control keys.
func HasCommandControlMetadata(raw map[string]any) bool {
	for _, key := range commandControlMetadataKeys {
		if v, ok := raw[key]; ok && v != nil {
			return true
		}
	}
	return false
}

// ParseCommandPointMetadataJSON decodes raw JSON bytes and parses CommandPointMetadata.
func ParseCommandPointMetadataJSON(raw []byte) (CommandPointMetadata, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return CommandPointMetadata{}, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	var decoded map[string]any
	if err := decoder.Decode(&decoded); err != nil {
		return CommandPointMetadata{}, fmt.Errorf("%w: decode JSON: %v", ErrInvalidPointMetadata, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return CommandPointMetadata{}, fmt.Errorf("%w: trailing JSON tokens", ErrInvalidPointMetadata)
	}
	return ParseCommandPointMetadata(decoded)
}

// ParseCommandPointMetadata extracts strongly typed command fields from an untyped sourceMetadata map.
func ParseCommandPointMetadata(raw map[string]any) (CommandPointMetadata, error) {
	var out CommandPointMetadata
	if len(raw) == 0 {
		return out, nil
	}

	var err error
	if capStr, ok, err := parseMetadataString(raw, "capability"); err != nil {
		return CommandPointMetadata{}, err
	} else if ok {
		out.Capability = Capability(capStr)
	}
	if out.CapabilityRevision, _, err = parseMetadataString(raw, "capabilityRevision"); err != nil {
		return CommandPointMetadata{}, err
	}
	if out.FeedbackPointKey, _, err = parseMetadataString(raw, "feedbackPointKey"); err != nil {
		return CommandPointMetadata{}, err
	}
	if mode, ok, err := parseMetadataString(raw, "actuationMode"); err != nil {
		return CommandPointMetadata{}, err
	} else if ok {
		out.ActuationMode = strings.ToUpper(mode)
	}
	if kind, ok, err := parseMetadataString(raw, "controlKind"); err != nil {
		return CommandPointMetadata{}, err
	} else if ok {
		out.ControlKind = strings.ToUpper(kind)
	}
	if out.ParameterKey, _, err = parseMetadataString(raw, "parameterKey"); err != nil {
		return CommandPointMetadata{}, err
	}
	if out.Channel, _, err = parseMetadataString(raw, "channel"); err != nil {
		return CommandPointMetadata{}, err
	}

	if out.Minimum, err = parseMetadataFiniteFloat(raw, "minimum"); err != nil {
		return CommandPointMetadata{}, err
	}
	if out.Maximum, err = parseMetadataFiniteFloat(raw, "maximum"); err != nil {
		return CommandPointMetadata{}, err
	}
	if out.Minimum != nil && out.Maximum != nil && *out.Minimum > *out.Maximum {
		return CommandPointMetadata{}, fmt.Errorf("%w: minimum %v exceeds maximum %v", ErrInvalidPointMetadata, *out.Minimum, *out.Maximum)
	}
	if out.Step, err = parseMetadataFiniteFloat(raw, "step"); err != nil {
		return CommandPointMetadata{}, err
	}
	if out.Step != nil && *out.Step <= 0 {
		return CommandPointMetadata{}, fmt.Errorf("%w: step must be > 0", ErrInvalidPointMetadata)
	}

	if coil, err := parseMetadataInt(raw, "coilAddress"); err != nil {
		return CommandPointMetadata{}, err
	} else if coil != nil {
		if *coil < 0 {
			return CommandPointMetadata{}, fmt.Errorf("%w: coilAddress must be >= 0", ErrInvalidPointMetadata)
		}
		out.CoilAddress = coil
	}
	if pulse, err := parseMetadataInt(raw, "pulseDurationMs"); err != nil {
		return CommandPointMetadata{}, err
	} else if pulse != nil {
		if *pulse < 0 {
			return CommandPointMetadata{}, fmt.Errorf("%w: pulseDurationMs must be >= 0", ErrInvalidPointMetadata)
		}
		out.PulseDurationMS = *pulse
	}

	if out.InterlockChecks, err = parseMetadataInterlockChecks(raw); err != nil {
		return CommandPointMetadata{}, fmt.Errorf("%w: %w", ErrInvalidInterlockMetadata, err)
	}

	return out, nil
}

// ValidateCommandPointMetadata validates control metadata types and invariants for COMMAND points.
func ValidateCommandPointMetadata(raw map[string]any) error {
	if len(raw) == 0 || !HasCommandControlMetadata(raw) {
		return nil
	}
	meta, err := ParseCommandPointMetadata(raw)
	if err != nil {
		return err
	}
	if meta.ControlKind != "" && meta.ControlKind != "ACTION" && meta.ControlKind != "NUMBER" {
		return fmt.Errorf("%w: unsupported controlKind %q", ErrInvalidPointMetadata, meta.ControlKind)
	}
	if meta.Capability != "" {
		profile, ok := CapabilityProfileFor(meta.Capability)
		if !ok {
			return fmt.Errorf("%w: unsupported capability %q", ErrInvalidPointMetadata, meta.Capability)
		}
		if meta.CapabilityRevision != "" && meta.CapabilityRevision != profile.Revision {
			return fmt.Errorf("%w: capabilityRevision %q does not match %q", ErrInvalidPointMetadata, meta.CapabilityRevision, profile.Revision)
		}
	}
	if meta.ControlKind == "NUMBER" {
		if meta.ParameterKey == "" || meta.Minimum == nil || meta.Maximum == nil || meta.Step == nil || *meta.Step <= 0 {
			return fmt.Errorf("%w: NUMBER controlKind requires parameterKey, minimum, maximum, and positive step", ErrInvalidPointMetadata)
		}
	}
	for i, chk := range meta.InterlockChecks {
		if chk.DeviceID == "" || chk.Key == "" || chk.Expected == "" {
			return fmt.Errorf("%w: %w: interlockChecks[%d] requires deviceId, key, and expected", ErrInvalidPointMetadata, ErrInvalidInterlockMetadata, i)
		}
	}
	return nil
}

func parseMetadataString(raw map[string]any, key string) (string, bool, error) {
	val, exists := raw[key]
	if !exists || val == nil {
		return "", false, nil
	}
	str, ok := val.(string)
	if !ok {
		return "", false, fmt.Errorf("%w: %s must be a string", ErrInvalidPointMetadata, key)
	}
	return strings.TrimSpace(str), true, nil
}

func parseMetadataFiniteFloat(raw map[string]any, key string) (*float64, error) {
	val, exists := raw[key]
	if !exists || val == nil {
		return nil, nil
	}
	num, ok := float64FromMetadataAny(val)
	if !ok {
		return nil, fmt.Errorf("%w: %s must be a finite number", ErrInvalidPointMetadata, key)
	}
	return &num, nil
}

const maxMetadataIntFloat = float64(uint64(1) << (strconv.IntSize - 1))

func parseMetadataInt(raw map[string]any, key string) (*int, error) {
	val, exists := raw[key]
	if !exists || val == nil {
		return nil, nil
	}
	switch typed := val.(type) {
	case int:
		v := typed
		return &v, nil
	case int32:
		v := int(typed)
		return &v, nil
	case int64:
		if typed < math.MinInt || typed > math.MaxInt {
			return nil, fmt.Errorf("%w: %s overflows int", ErrInvalidPointMetadata, key)
		}
		v := int(typed)
		return &v, nil
	case json.Number:
		parsed, err := strconv.Atoi(typed.String())
		if err == nil {
			v := parsed
			return &v, nil
		}
		if errors.Is(err, strconv.ErrRange) {
			return nil, fmt.Errorf("%w: %s overflows int", ErrInvalidPointMetadata, key)
		}
		if f, err := typed.Float64(); err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) && math.Trunc(f) == f {
			if f < -maxMetadataIntFloat || f >= maxMetadataIntFloat {
				return nil, fmt.Errorf("%w: %s overflows int", ErrInvalidPointMetadata, key)
			}
			v := int(f)
			return &v, nil
		}
		return nil, fmt.Errorf("%w: %s must be an integer", ErrInvalidPointMetadata, key)
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) || math.Trunc(typed) != typed {
			return nil, fmt.Errorf("%w: %s must be an integer", ErrInvalidPointMetadata, key)
		}
		if typed < -maxMetadataIntFloat || typed >= maxMetadataIntFloat {
			return nil, fmt.Errorf("%w: %s overflows int", ErrInvalidPointMetadata, key)
		}
		v := int(typed)
		return &v, nil
	default:
		return nil, fmt.Errorf("%w: %s must be an integer", ErrInvalidPointMetadata, key)
	}
}

func float64FromMetadataAny(val any) (float64, bool) {
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
			return 0, false
		}
		num = parsed
	default:
		return 0, false
	}
	if math.IsNaN(num) || math.IsInf(num, 0) {
		return 0, false
	}
	return num, true
}

func parseMetadataInterlockChecks(raw map[string]any) ([]InterlockCheck, error) {
	val, exists := raw["interlockChecks"]
	if !exists || val == nil {
		return nil, nil
	}
	switch items := val.(type) {
	case []InterlockCheck:
		if len(items) == 0 {
			return nil, nil
		}
		out := make([]InterlockCheck, len(items))
		for i, c := range items {
			out[i] = InterlockCheck{
				DeviceID: strings.TrimSpace(c.DeviceID),
				Key:      strings.TrimSpace(c.Key),
				Expected: strings.TrimSpace(c.Expected),
				Reason:   strings.TrimSpace(c.Reason),
			}
		}
		return out, nil
	case []any:
		if len(items) == 0 {
			return nil, nil
		}
		out := make([]InterlockCheck, 0, len(items))
		for i, item := range items {
			obj, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%w: interlockChecks[%d] must be an object", ErrInvalidPointMetadata, i)
			}
			devID, _, err := parseMetadataString(obj, "deviceId")
			if err != nil {
				return nil, err
			}
			key, _, err := parseMetadataString(obj, "key")
			if err != nil {
				return nil, err
			}
			expected, _, err := parseMetadataString(obj, "expected")
			if err != nil {
				return nil, err
			}
			reason, _, err := parseMetadataString(obj, "reason")
			if err != nil {
				return nil, err
			}
			out = append(out, InterlockCheck{
				DeviceID: devID,
				Key:      key,
				Expected: expected,
				Reason:   reason,
			})
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%w: interlockChecks must be an array", ErrInvalidPointMetadata)
	}
}
