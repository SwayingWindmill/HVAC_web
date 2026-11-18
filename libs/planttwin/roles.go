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
	RoleActivePower         SemanticRole = "ACTIVE_POWER"
	RoleRunState            SemanticRole = "RUN_STATE"
	RoleCHWLeavingTemp      SemanticRole = "CHW_LEAVING_TEMP"
	RoleCHWEnteringTemp     SemanticRole = "CHW_ENTERING_TEMP"
	RoleCWLeavingTemp       SemanticRole = "CW_LEAVING_TEMP"
	RoleCWEnteringTemp      SemanticRole = "CW_ENTERING_TEMP"
	RoleSupplyWaterTemp     SemanticRole = "SUPPLY_WATER_TEMP"
	RoleReturnWaterTemp     SemanticRole = "RETURN_WATER_TEMP"
	RoleCoolingLoad         SemanticRole = "COOLING_LOAD"
	RoleWaterFlow           SemanticRole = "WATER_FLOW"
	RoleZoneTemp            SemanticRole = "ZONE_TEMP"
	RoleValveFeedback       SemanticRole = "VALVE_FEEDBACK"
	RoleValveCommand        SemanticRole = "VALVE_COMMAND"
	RoleVFDFrequency        SemanticRole = "VFD_FREQUENCY"
	RoleVFDFrequencyCommand SemanticRole = "VFD_FREQUENCY_COMMAND"
	RoleHeaderDiffPressure  SemanticRole = "HEADER_DIFF_PRESSURE"
	RoleBranchFlow          SemanticRole = "BRANCH_FLOW"
)

// WaterLoop names a plant hydraulic circuit grouping assets and parallel branches.
type WaterLoop string

const (
	WaterLoopChilledWater     WaterLoop = "CHILLED_WATER"
	WaterLoopCondenserWater   WaterLoop = "CONDENSER_WATER"
	WaterLoopCoolingTowerLoop WaterLoop = "COOLING_TOWER_LOOP"
)

const (
	RelationshipServes      = "SERVES"
	RelationshipIsolates    = "ISOLATES"
	RelationshipFeedsHeader = "FEEDS_HEADER"

	BindingDescribes = "DESCRIBES"
	BindingControls  = "CONTROLS"
	BindingMeters    = "METERS"

	AssetTypeChiller                    = "CHILLER"
	AssetTypeChilledWaterPump           = "CHILLED_WATER_PUMP"
	AssetTypeCoolingWaterPump           = "COOLING_WATER_PUMP"
	AssetTypeCoolingTower               = "COOLING_TOWER"
	AssetTypeChilledWaterLoop           = "CHILLED_WATER_LOOP"
	AssetTypeCoolingWaterLoop           = "COOLING_WATER_LOOP"
	AssetTypeIsolationValve             = "ISOLATION_VALVE"
	AssetTypeMotorizedValve             = "MOTORIZED_VALVE"
	AssetTypeDifferentialPressureSensor = "DIFFERENTIAL_PRESSURE_SENSOR"

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
	RoleValveFeedback,
	RoleValveCommand,
	RoleVFDFrequency,
	RoleVFDFrequencyCommand,
	RoleHeaderDiffPressure,
	RoleBranchFlow,
}

// AllSemanticRoles returns a fresh copy of the 17 canonical semantic roles.
func AllSemanticRoles() []SemanticRole {
	out := make([]SemanticRole, len(canonicalSemanticRoles))
	copy(out, canonicalSemanticRoles)
	return out
}

// IsValid reports whether r matches one of the canonical semantic roles.
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
		RoleZoneTemp,
		RoleValveFeedback,
		RoleValveCommand,
		RoleVFDFrequency,
		RoleVFDFrequencyCommand,
		RoleHeaderDiffPressure,
		RoleBranchFlow:
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

var canonicalWaterLoops = []WaterLoop{
	WaterLoopChilledWater,
	WaterLoopCondenserWater,
	WaterLoopCoolingTowerLoop,
}

// AllWaterLoops returns a fresh copy of the canonical hydraulic water loop identifiers.
func AllWaterLoops() []WaterLoop {
	out := make([]WaterLoop, len(canonicalWaterLoops))
	copy(out, canonicalWaterLoops)
	return out
}

// IsValid reports whether w matches one of the canonical hydraulic water loops.
func (w WaterLoop) IsValid() bool {
	switch w {
	case WaterLoopChilledWater, WaterLoopCondenserWater, WaterLoopCoolingTowerLoop:
		return true
	default:
		return false
	}
}

// String returns the canonical water loop string.
func (w WaterLoop) String() string {
	return string(w)
}

// ParseWaterLoop normalizes raw input and validates it against the canonical water loop set.
func ParseWaterLoop(raw string) (WaterLoop, error) {
	loop := WaterLoop(strings.ToUpper(strings.TrimSpace(raw)))
	if !loop.IsValid() {
		return "", fmt.Errorf("unknown water loop %q", raw)
	}
	return loop, nil
}

var canonicalRelationshipRoles = []string{
	RelationshipServes,
	RelationshipIsolates,
	RelationshipFeedsHeader,
}

// AllRelationshipRoles returns a fresh copy of the canonical asset relationship roles.
func AllRelationshipRoles() []string {
	out := make([]string, len(canonicalRelationshipRoles))
	copy(out, canonicalRelationshipRoles)
	return out
}

// IsValidRelationshipRole reports whether role matches a canonical hydraulic relationship role.
func IsValidRelationshipRole(role string) bool {
	switch role {
	case RelationshipServes, RelationshipIsolates, RelationshipFeedsHeader:
		return true
	default:
		return false
	}
}

// ParseRelationshipRole normalizes raw input and validates it against the canonical relationship role set.
func ParseRelationshipRole(raw string) (string, error) {
	role := strings.ToUpper(strings.TrimSpace(raw))
	if !IsValidRelationshipRole(role) {
		return "", fmt.Errorf("unknown relationship role %q", raw)
	}
	return role, nil
}
