package edgecontrol

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"
)

// SafetyBoundaryClass identifies whether a runtime Channel or registered Point lies
// inside the soft real-time control plane or hardware PLC protection boundary.
type SafetyBoundaryClass string

const (
	SafetyBoundarySoftRealtime SafetyBoundaryClass = "SOFT_REALTIME"
	SafetyBoundaryHardRealtime SafetyBoundaryClass = "HARD_REALTIME_SAFETY"
)

// HardRealtimeProtectionCategory classifies hardware-owned safety protection points
// that soft real-time Cloud or Edge software must never override or actuate.
type HardRealtimeProtectionCategory string

const (
	HardRealtimeCategoryPLCEStop         HardRealtimeProtectionCategory = "PLC_ESTOP"
	HardRealtimeCategoryFireInterlock    HardRealtimeProtectionCategory = "FIRE_INTERLOCK"
	HardRealtimeCategoryHighPressureTrip HardRealtimeProtectionCategory = "HIGH_PRESSURE_TRIP"
	HardRealtimeCategoryFreezeProtection HardRealtimeProtectionCategory = "FREEZE_PROTECTION"
	HardRealtimeCategoryHardwareSafety   HardRealtimeProtectionCategory = "HARDWARE_SAFETY_PROTECTION"
)

// ReasonHardRealtimeSafetyBoundary is the canonical machine-readable reason code
// returned when Cloud or Edge software attempts to control a hard real-time point.
const ReasonHardRealtimeSafetyBoundary = "HARD_REALTIME_SAFETY_BOUNDARY"

// ErrHardRealtimeSafetyBoundary indicates a violation of EDGE-003 (soft real-time
// Cloud/Edge software attempting to register, bind, or write a hard safety point).
var ErrHardRealtimeSafetyBoundary = errors.New("hard real-time safety protection point cannot be controlled by soft real-time software")

// ValidSafetyBoundaryClass reports whether class is empty or a supported boundary value.
func ValidSafetyBoundaryClass(class SafetyBoundaryClass) bool {
	switch class {
	case "", SafetyBoundarySoftRealtime, SafetyBoundaryHardRealtime:
		return true
	default:
		return false
	}
}

// ValidHardRealtimeProtectionCategory reports whether category is a supported classification.
func ValidHardRealtimeProtectionCategory(category HardRealtimeProtectionCategory) bool {
	switch category {
	case HardRealtimeCategoryPLCEStop,
		HardRealtimeCategoryFireInterlock,
		HardRealtimeCategoryHighPressureTrip,
		HardRealtimeCategoryFreezeProtection,
		HardRealtimeCategoryHardwareSafety:
		return true
	default:
		return false
	}
}

// ClassifyHardRealtimeProtection determines whether point metadata or canonical names
// identify a PLC / firmware hardware safety protection point (EDGE-003).
func ClassifyHardRealtimeProtection(pointCode, semanticOrCapability string, metadata map[string]any) (HardRealtimeProtectionCategory, bool) {
	explicitCategory := HardRealtimeCategoryHardwareSafety
	if rawCat, ok := metadata["hardRealtimeCategory"].(string); ok {
		normalizedCat := HardRealtimeProtectionCategory(strings.ToUpper(strings.TrimSpace(rawCat)))
		if ValidHardRealtimeProtectionCategory(normalizedCat) {
			explicitCategory = normalizedCat
		}
	}

	if boundary, ok := metadata["safetyBoundary"].(string); ok {
		if SafetyBoundaryClass(strings.ToUpper(strings.TrimSpace(boundary))) == SafetyBoundaryHardRealtime {
			return explicitCategory, true
		}
	}
	if protected, ok := metadata["hardRealtimeProtected"].(bool); ok && protected {
		return explicitCategory, true
	}
	if owner, ok := metadata["safetyOwner"].(string); ok {
		switch strings.ToUpper(strings.TrimSpace(owner)) {
		case "PLC", "SAFETY_HARDWARE", "HARDWARE", "FIRE_PANEL":
			return explicitCategory, true
		}
	}
	if rawCat, ok := metadata["hardRealtimeCategory"].(string); ok {
		normalizedCat := HardRealtimeProtectionCategory(strings.ToUpper(strings.TrimSpace(rawCat)))
		if ValidHardRealtimeProtectionCategory(normalizedCat) {
			return normalizedCat, true
		}
	}

	tokens := splitIdentifierTokens(pointCode)
	tokens = append(tokens, splitIdentifierTokens(semanticOrCapability)...)
	if len(tokens) == 0 {
		return "", false
	}

	if matchesPLCEStopTokens(tokens) {
		return HardRealtimeCategoryPLCEStop, true
	}
	if matchesFireInterlockTokens(tokens) {
		return HardRealtimeCategoryFireInterlock, true
	}
	if matchesHighPressureTripTokens(tokens) {
		return HardRealtimeCategoryHighPressureTrip, true
	}
	if matchesFreezeProtectionTokens(tokens) {
		return HardRealtimeCategoryFreezeProtection, true
	}
	if matchesHardwareSafetyTokens(tokens) {
		return HardRealtimeCategoryHardwareSafety, true
	}
	return "", false
}

// IsHardRealtimeChannel reports whether a ChannelDescriptor belongs to the hard
// real-time safety boundary by explicit SafetyBoundary tag or ChannelID classification.
func IsHardRealtimeChannel(desc ChannelDescriptor) bool {
	if desc.SafetyBoundary == SafetyBoundaryHardRealtime {
		return true
	}
	_, protected := ClassifyHardRealtimeProtection(desc.ChannelID, "", nil)
	return protected
}

// ValidatePointSafetyBoundary validates EDGE-003 metadata fields and blocks writable,
// COMMAND, or SETTING points that target hardware protection logic.
func ValidatePointSafetyBoundary(pointCode, pointType string, writable bool, metadata map[string]any) error {
	if rawBoundary, exists := metadata["safetyBoundary"]; exists && rawBoundary != nil {
		str, ok := rawBoundary.(string)
		if !ok {
			return fmtError("safetyBoundary must be a string")
		}
		normalized := SafetyBoundaryClass(strings.ToUpper(strings.TrimSpace(str)))
		if !ValidSafetyBoundaryClass(normalized) {
			return fmtError("unsupported safetyBoundary " + str)
		}
	}
	if rawProtected, exists := metadata["hardRealtimeProtected"]; exists && rawProtected != nil {
		if _, ok := rawProtected.(bool); !ok {
			return fmtError("hardRealtimeProtected must be a boolean")
		}
	}
	if rawOwner, exists := metadata["safetyOwner"]; exists && rawOwner != nil {
		if _, ok := rawOwner.(string); !ok {
			return fmtError("safetyOwner must be a string")
		}
	}
	if rawCat, exists := metadata["hardRealtimeCategory"]; exists && rawCat != nil {
		str, ok := rawCat.(string)
		if !ok {
			return fmtError("hardRealtimeCategory must be a string")
		}
		trimmed := strings.TrimSpace(str)
		if trimmed != "" {
			normalized := HardRealtimeProtectionCategory(strings.ToUpper(trimmed))
			if !ValidHardRealtimeProtectionCategory(normalized) {
				return fmtError("unsupported hardRealtimeCategory " + str)
			}
		}
	}

	normalizedType := strings.ToUpper(strings.TrimSpace(pointType))
	cat, protected := ClassifyHardRealtimeProtection(pointCode, "", metadata)
	if !protected {
		return nil
	}
	if writable || normalizedType == "COMMAND" || normalizedType == "SETTING" {
		return fmt.Errorf("%w: category=%s point=%s", ErrHardRealtimeSafetyBoundary, cat, pointCode)
	}
	return nil
}

func splitIdentifierTokens(raw string) []string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	runes := []rune(trimmed)
	var tokens []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			tokens = append(tokens, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}
	for i, r := range runes {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			flush()
			continue
		}
		if i > 0 && len(cur) > 0 && unicode.IsUpper(r) {
			prev := runes[i-1]
			nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
			if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && nextLower) {
				flush()
			}
		}
		cur = append(cur, r)
	}
	flush()
	return tokens
}

func hasAdjacentPair(tokens []string, first, second string) bool {
	for i := 0; i+1 < len(tokens); i++ {
		if tokens[i] == first && tokens[i+1] == second {
			return true
		}
	}
	return false
}

func hasAnyToken(tokens []string, candidates ...string) bool {
	for _, candidate := range candidates {
		if slices.Contains(tokens, candidate) {
			return true
		}
	}
	return false
}

func matchesPLCEStopTokens(tokens []string) bool {
	if slices.Contains(tokens, "estop") || hasAdjacentPair(tokens, "e", "stop") {
		return true
	}
	if slices.Contains(tokens, "plc") && hasAnyToken(tokens, "emergency", "estop", "shutdown") {
		return true
	}
	return false
}

func matchesFireInterlockTokens(tokens []string) bool {
	if !slices.Contains(tokens, "fire") {
		return false
	}
	return hasAnyToken(
		tokens,
		"interlock", "trip", "alarm", "bypass", "override", "mask", "cutout",
		"relay", "shutoff", "shutdown", "linkage", "damper", "safety",
		"protect", "protection", "panel", "signal", "link", "stop",
	)
}

func matchesHighPressureTripTokens(tokens []string) bool {
	if hasAdjacentPair(tokens, "safety", "trip") ||
		hasAdjacentPair(tokens, "hard", "trip") ||
		hasAdjacentPair(tokens, "trip", "bypass") ||
		hasAdjacentPair(tokens, "trip", "override") ||
		hasAdjacentPair(tokens, "trip", "mask") {
		return true
	}
	hasPressureAnchor := hasAdjacentPair(tokens, "high", "pressure") ||
		slices.Contains(tokens, "hp") ||
		hasAdjacentPair(tokens, "discharge", "pressure") ||
		hasAdjacentPair(tokens, "relief", "valve") ||
		slices.Contains(tokens, "breaker")
	if !hasPressureAnchor {
		return false
	}
	return hasAnyToken(tokens, "trip", "cutout", "interlock", "switch", "bypass", "override", "mask", "protect", "protection")
}

func matchesFreezeProtectionTokens(tokens []string) bool {
	if slices.Contains(tokens, "freezestat") {
		return true
	}
	hasFreezeAnchor := hasAnyToken(tokens, "freeze", "antifreeze", "frost") ||
		hasAdjacentPair(tokens, "anti", "freeze") ||
		(hasAdjacentPair(tokens, "low", "temp") && slices.Contains(tokens, "cutout"))
	if !hasFreezeAnchor {
		return false
	}
	return hasAnyToken(tokens, "protect", "protection", "stat", "trip", "interlock", "cutout", "bypass", "override", "mask", "switch", "alarm", "hard")
}

func matchesHardwareSafetyTokens(tokens []string) bool {
	adjacentPairs := [][2]string{
		{"hard", "realtime"},
		{"plc", "safety"},
		{"plc", "protect"},
		{"plc", "protection"},
		{"plc", "interlock"},
		{"safety", "plc"},
		{"safety", "relay"},
		{"safety", "circuit"},
		{"safety", "bypass"},
		{"safety", "override"},
		{"safety", "mask"},
		{"hard", "interlock"},
		{"hard", "safety"},
		{"hardware", "interlock"},
		{"hardware", "protection"},
		{"protection", "bypass"},
		{"protection", "override"},
		{"protection", "mask"},
	}
	for _, pair := range adjacentPairs {
		if hasAdjacentPair(tokens, pair[0], pair[1]) {
			return true
		}
	}
	return false
}
