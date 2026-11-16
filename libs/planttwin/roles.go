// Package planttwin provides the canonical semantic plant twin model, hydraulic
// topology graph, operating schedule evaluator, and ADR 0016 read-view loaders.
package planttwin

import (
	"fmt"
	"strings"
)

// SemanticRole names the physical meaning of a telemetry or setting point in the plant.
type SemanticRole string

const (
	RoleActivePower     SemanticRole = "ACTIVE_POWER"
	RoleRunState        SemanticRole = "RUN_STATE"
	RoleCHWLeavingTemp  SemanticRole = "CHW_LEAVING_TEMP"
	RoleCHWEnteringTemp SemanticRole = "CHW_ENTERING_TEMP"
	RoleCWLeavingTemp   SemanticRole = "CW_LEAVING_TEMP"
	RoleCWEnteringTemp  SemanticRole = "CW_ENTERING_TEMP"
	RoleSupplyWaterTemp SemanticRole = "SUPPLY_WATER_TEMP"
	RoleReturnWaterTemp SemanticRole = "RETURN_WATER_TEMP"
	RoleCoolingLoad     SemanticRole = "COOLING_LOAD"
	RoleWaterFlow       SemanticRole = "WATER_FLOW"
	RoleZoneTemp        SemanticRole = "ZONE_TEMP"
)

const (
	RelationshipServes = "SERVES"

	BindingDescribes = "DESCRIBES"
	BindingControls  = "CONTROLS"
	BindingMeters    = "METERS"

	AssetTypeChiller          = "CHILLER"
	AssetTypeChilledWaterPump = "CHILLED_WATER_PUMP"
	AssetTypeCoolingWaterPump = "COOLING_WATER_PUMP"
	AssetTypeCoolingTower     = "COOLING_TOWER"
	AssetTypeChilledWaterLoop = "CHILLED_WATER_LOOP"
	AssetTypeCoolingWaterLoop = "COOLING_WATER_LOOP"

	SubjectTypeSite  = "SITE"
	SubjectTypeSpace = "SPACE"
	SubjectTypeAsset = "ASSET"

	PointTypeCommand = "COMMAND"

	ControlModeRemoteAuto       = "REMOTE_AUTO"
	ControlModeManualOverride   = "MANUAL_OVERRIDE"
	ControlModeSafeShutdownExit = "SAFE_SHUTDOWN_EXIT"

	LockModeUnlocked        = "UNLOCKED"
	LockModeManualLock      = "MANUAL_LOCK"
	LockModeMaintenanceLock = "MAINTENANCE_LOCK"

	ExceptionHolidayShutdown   = "HOLIDAY_SHUTDOWN"
	ExceptionMaintenanceWindow = "MAINTENANCE_WINDOW"
	ExceptionOvertimeCooling   = "OVERTIME_COOLING"

	OperatingStateScheduledOpen   = "SCHEDULED_OPEN"
	OperatingStatePreCool         = "PRE_COOL"
	OperatingStateCoastDown       = "COAST_DOWN"
	OperatingStateExceptionClosed = "EXCEPTION_CLOSED"
	OperatingStateOutsideSchedule = "OUTSIDE_SCHEDULE"
	OperatingStateUnconfigured    = "UNCONFIGURED"
)

var canonicalSemanticRoles = []SemanticRole{
	RoleActivePower,
	RoleRunState,
	RoleCHWLeavingTemp,
	RoleCHWEnteringTemp,
	RoleCWLeavingTemp,
	RoleCWEnteringTemp,
	RoleSupplyWaterTemp,
	RoleReturnWaterTemp,
	RoleCoolingLoad,
	RoleWaterFlow,
	RoleZoneTemp,
}

// AllSemanticRoles returns a fresh copy of the 11 canonical semantic roles.
func AllSemanticRoles() []SemanticRole {
	out := make([]SemanticRole, len(canonicalSemanticRoles))
	copy(out, canonicalSemanticRoles)
	return out
}

// IsValid reports whether r matches one of the 11 canonical semantic roles.
func (r SemanticRole) IsValid() bool {
	switch r {
	case RoleActivePower,
		RoleRunState,
		RoleCHWLeavingTemp,
		RoleCHWEnteringTemp,
		RoleCWLeavingTemp,
		RoleCWEnteringTemp,
		RoleSupplyWaterTemp,
		RoleReturnWaterTemp,
		RoleCoolingLoad,
		RoleWaterFlow,
		RoleZoneTemp:
		return true
	default:
		return false
	}
}

// String returns the canonical role string.
func (r SemanticRole) String() string {
	return string(r)
}

// ParseSemanticRole normalizes raw input and validates it against the canonical role set.
func ParseSemanticRole(raw string) (SemanticRole, error) {
	role := SemanticRole(strings.ToUpper(strings.TrimSpace(raw)))
	if !role.IsValid() {
		return "", fmt.Errorf("unknown semantic role %q", raw)
	}
	return role, nil
}
