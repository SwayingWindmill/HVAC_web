package commandmodel

import (
	"math"
	"strconv"
	"strings"
)

// IsFaultOrAlarmPointKey reports whether a telemetry key represents an equipment fault or alarm signal.
func IsFaultOrAlarmPointKey(rawKey string) bool {
	key := strings.ToLower(strings.TrimSpace(rawKey))
	if key == "" || strings.Contains(key, "reset") {
		return false
	}
	return strings.Contains(key, "fault") || strings.Contains(key, "alarm")
}

// IsLocalManualPointKey reports whether a telemetry key conveys local/remote or HOA cabinet mode.
func IsLocalManualPointKey(rawKey string) bool {
	key := strings.ToLower(strings.TrimSpace(rawKey))
	if key == "" {
		return false
	}
	return strings.Contains(key, "local_remote") ||
		strings.Contains(key, "localremote") ||
		strings.Contains(key, "local_manual") ||
		strings.Contains(key, "localmanual") ||
		strings.Contains(key, "cabinet_local") ||
		strings.Contains(key, "cabinetlocal") ||
		strings.Contains(key, "local_mode") ||
		strings.Contains(key, "localmode") ||
		strings.Contains(key, "manual_override") ||
		strings.Contains(key, "manualoverride") ||
		strings.Contains(key, "manual_mode") ||
		strings.Contains(key, "manualmode") ||
		strings.Contains(key, "local_control") ||
		strings.Contains(key, "localcontrol") ||
		strings.Contains(key, "control_mode") ||
		strings.Contains(key, "controlmode") ||
		strings.Contains(key, "cabinet_mode") ||
		strings.Contains(key, "cabinetmode") ||
		strings.Contains(key, "hoa") ||
		key == "local" ||
		key == "manual"
}

// DiagnosticValueIndicatesActiveFault decodes a fault or alarm telemetry value across boolean, numeric, and text encodings.
func DiagnosticValueIndicatesActiveFault(boolVal *bool, numVal *float64, strVal string) bool {
	if boolVal != nil {
		return *boolVal
	}
	if numVal != nil && !math.IsNaN(*numVal) && !math.IsInf(*numVal, 0) {
		return *numVal != 0
	}
	norm := strings.ToUpper(strings.TrimSpace(strVal))
	if norm == "" {
		return false
	}
	switch norm {
	case "OK", "NORMAL", "NONE", "CLEAR", "CLEARED", "NO_FAULT", "NOFAULT", "INACTIVE", "OFF", "HEALTHY", "FALSE":
		return false
	}
	if n, err := strconv.ParseFloat(norm, 64); err == nil {
		return n != 0
	}
	return true
}

// DiagnosticPointIndicatesLocalManual decodes a local/remote or HOA mode telemetry value,
// honoring remote-enable polarity (false/0 => local manual) for `local_remote*` / `remote*` keys.
func DiagnosticPointIndicatesLocalManual(rawKey string, boolVal *bool, numVal *float64, strVal string) bool {
	key := strings.ToLower(strings.TrimSpace(rawKey))
	isRemotePolarityKey := strings.Contains(key, "remote")
	isExplicitLocalFlagKey := !isRemotePolarityKey && (strings.Contains(key, "local") || strings.Contains(key, "manual"))

	if isRemotePolarityKey {
		if boolVal != nil {
			return !*boolVal
		}
		if numVal != nil && !math.IsNaN(*numVal) && !math.IsInf(*numVal, 0) {
			return *numVal == 0
		}
	} else if isExplicitLocalFlagKey {
		if boolVal != nil {
			return *boolVal
		}
		if numVal != nil && !math.IsNaN(*numVal) && !math.IsInf(*numVal, 0) {
			return *numVal != 0
		}
	}

	norm := strings.ToUpper(strings.TrimSpace(strVal))
	if norm == "" {
		return false
	}
	if isRemotePolarityKey {
		switch norm {
		case "LOCAL", "MANUAL", "LOCAL_MANUAL", "HAND", "FALSE":
			return true
		case "REMOTE", "AUTO", "REMOTE_AUTO", "TRUE":
			return false
		}
		if n, err := strconv.ParseFloat(norm, 64); err == nil {
			return n == 0
		}
		return false
	}
	if !isExplicitLocalFlagKey {
		return strings.Contains(norm, "LOCAL") || strings.Contains(norm, "MANUAL") || norm == "HAND"
	}
	switch norm {
	case "FALSE", "REMOTE", "AUTO", "REMOTE_AUTO", "OFF", "NORMAL", "NONE", "INACTIVE":
		return false
	default:
		if n, err := strconv.ParseFloat(norm, 64); err == nil {
			return n != 0
		}
		return true
	}
}
