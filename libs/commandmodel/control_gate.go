package commandmodel

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// PreconditionMode makes explicit whether a command requires fresh reported telemetry
// before admission or executes under an audited emergency bypass.
type PreconditionMode string

const (
	PreconditionRequireCurrentState PreconditionMode = "REQUIRE_CURRENT_STATE"
	PreconditionEmergencyBypass     PreconditionMode = "EMERGENCY_BYPASS"
)

type SiteControlMode string

const (
	SiteControlModeRemoteAuto       SiteControlMode = "REMOTE_AUTO"
	SiteControlModeManualOverride   SiteControlMode = "MANUAL_OVERRIDE"
	SiteControlModeSafeShutdownExit SiteControlMode = "SAFE_SHUTDOWN_EXIT"
)

type AssetControlLockMode string

const (
	AssetControlLockUnlocked    AssetControlLockMode = "UNLOCKED"
	AssetControlLockManual      AssetControlLockMode = "MANUAL_LOCK"
	AssetControlLockMaintenance AssetControlLockMode = "MAINTENANCE_LOCK"
)

type GatePhase string

const (
	GatePhaseSubmit      GatePhase = "SUBMIT"
	GatePhasePreDispatch GatePhase = "PRE_DISPATCH"
)

type ControlLockSnapshot struct {
	AssetID        string
	LockMode       AssetControlLockMode
	BoundDeviceIDs []string
}

type MaintenanceWindowSnapshot struct {
	ID        string
	Title     string
	StartDate string
	EndDate   string
	Reason    string
}

// LivePointObservation holds real telemetry state for either the target control feedback point
// or a peer interlock point. Nil indicates missing telemetry (never synthesized).
type LivePointObservation struct {
	EvaluationAvailability string
	Presence               string
	Readiness              string
	Freshness              string
	Quality                string
	BusinessRevision       uint64
	CurrentValue           *float64
	ReportedValue          ScalarValue
	ObservedAt             time.Time
}

// GateInput captures every input required by the single pure control admission and
// pre-dispatch safety evaluator.
type GateInput struct {
	CommandID               string
	IdempotencyKey          string
	Phase                   GatePhase
	AssetID                 string
	DeviceID                string
	Capability              Capability
	CapabilityRevision      string
	ParameterKey            string
	ActuationMode           string
	AllowEmergencyDoRelease bool
	Parameters              CommandParameters
	PriorityTier            PriorityTier
	CommandSource           CommandSource
	SiteMode                SiteControlMode
	AssetLocks              []ControlLockSnapshot
	ActiveMaintenanceWindow *MaintenanceWindowSnapshot
	PreconditionMode        PreconditionMode
	BypassReason            string
	HardRealtimeProtected   bool
	TargetState             *LivePointObservation
	Interlocks              []InterlockCheck
	PeerStates              map[string]*LivePointObservation // keyed by deviceID + "\x00" + key
	DeliveryMode            DeliveryMode
	TTLSeconds              *int
	ExpiresAt               *time.Time
	Now                     time.Time
	RequireStepUp           bool
	StepUpVerified          bool
}

type GateDecision struct {
	Risk             RiskLevel
	PreconditionMode PreconditionMode
	BypassReason     string
	EvidenceID       string
	StepUpRequired   bool
}

type GateRejection struct {
	HTTPStatus int
	Code       string
	Title      string
	Detail     string
	Retryable  bool
}

// EvaluateControlGate is the single source of truth for 3-tier site/asset locks, maintenance
// windows, current-state/emergency-bypass preconditions, peer Control Interlocks, and step-up.
func EvaluateControlGate(in GateInput) (GateDecision, *GateRejection) {
	phase := in.Phase
	if phase == "" {
		phase = GatePhaseSubmit
	}
	if in.HardRealtimeProtected {
		if phase == GatePhasePreDispatch {
			return GateDecision{}, &GateRejection{
				HTTPStatus: http.StatusConflict,
				Code:       "DISPATCH_SAFETY_HARD_REALTIME_BOUNDARY",
				Title:      "Hard real-time safety boundary violation",
				Detail:     "Soft real-time cloud/edge control cannot target PLC or hardware safety protection points.",
			}
		}
		return GateDecision{}, &GateRejection{
			HTTPStatus: http.StatusForbidden,
			Code:       "HARD_REALTIME_SAFETY_BOUNDARY",
			Title:      "Hard real-time safety boundary violation",
			Detail:     "Soft real-time cloud/edge control cannot target PLC or hardware safety protection points.",
		}
	}
	precond := in.PreconditionMode
	if precond == "" {
		precond = PreconditionRequireCurrentState
	}

	profile, supported := CapabilityProfileFor(in.Capability)
	if !supported {
		return GateDecision{}, rejectInvalidCapability(phase)
	}
	if rev := strings.TrimSpace(in.CapabilityRevision); rev != "" && rev != profile.Revision {
		return GateDecision{}, rejectInvalidCapability(phase)
	}

	actuation := strings.ToUpper(strings.TrimSpace(in.ActuationMode))
	isEmergencyDo := in.Capability == CapabilityStop && actuation == "EMERGENCY_DO_RELEASE"
	if isEmergencyDo && phase == GatePhaseSubmit && !in.AllowEmergencyDoRelease {
		return GateDecision{}, &GateRejection{
			HTTPStatus: http.StatusBadRequest,
			Code:       "COMMAND_REQUEST_INVALID",
			Title:      "Command request invalid",
			Detail:     "EMERGENCY_DO_RELEASE points must be triggered via POST /api/v1/sites/{siteId}/control-shutdown with explicit secondary risk confirmation.",
		}
	}

	if !ParametersValid(in.Capability, in.Parameters) {
		return GateDecision{}, rejectInvalidParameters(phase)
	}
	if phase == GatePhaseSubmit && profile.ParameterKey != "" && in.ParameterKey != profile.ParameterKey {
		return GateDecision{}, rejectInvalidParameters(phase)
	}

	bypassLocks := isEmergencyDo && (in.AllowEmergencyDoRelease || phase == GatePhasePreDispatch)
	if !bypassLocks {
		if rejection := evaluateThreeTierLocks(in, phase); rejection != nil {
			return GateDecision{}, rejection
		}
	}

	if phase == GatePhasePreDispatch {
		return evaluatePreDispatchGate(in, profile, precond, actuation)
	}
	return evaluateSubmitGate(in, profile, precond)
}

func evaluateThreeTierLocks(in GateInput, phase GatePhase) *GateRejection {
	isStop := in.Capability == CapabilityStop || in.Capability == CapabilityStopUnitSequence
	isMaintAllowed := isStop || in.Capability == CapabilityResetFault

	if in.SiteMode != "" && in.SiteMode != SiteControlModeRemoteAuto && !isStop {
		if phase == GatePhasePreDispatch {
			return &GateRejection{HTTPStatus: http.StatusConflict, Code: "DISPATCH_SAFETY_CONTROL_LOCKED"}
		}
		return &GateRejection{
			HTTPStatus: http.StatusConflict,
			Code:       "COMMAND_CONTROL_LOCKED",
			Title:      "Site control exit active",
			Detail:     "Remote commands are blocked while Site control mode is " + string(in.SiteMode) + ".",
		}
	}

	for _, lock := range in.AssetLocks {
		if lock.LockMode == "" || lock.LockMode == AssetControlLockUnlocked {
			continue
		}
		matchesAsset := in.AssetID != "" && lock.AssetID == in.AssetID
		matchesDevice := in.DeviceID != "" && containsGateDevice(lock.BoundDeviceIDs, in.DeviceID)
		if matchesAsset || matchesDevice {
			if phase == GatePhasePreDispatch {
				return &GateRejection{HTTPStatus: http.StatusConflict, Code: "DISPATCH_SAFETY_CONTROL_LOCKED"}
			}
			return &GateRejection{
				HTTPStatus: http.StatusConflict,
				Code:       "COMMAND_CONTROL_LOCKED",
				Title:      "Asset control locked",
				Detail:     "Commands targeting this Asset are blocked while control lock is " + string(lock.LockMode) + ".",
			}
		}
	}

	if in.ActiveMaintenanceWindow != nil && !isMaintAllowed {
		if phase == GatePhasePreDispatch {
			return &GateRejection{HTTPStatus: http.StatusConflict, Code: "DISPATCH_SAFETY_MAINTENANCE_WINDOW"}
		}
		return &GateRejection{
			HTTPStatus: http.StatusConflict,
			Code:       "COMMAND_CONTROL_LOCKED",
			Title:      "Site maintenance window active",
			Detail:     fmt.Sprintf("Only STOP, STOP_UNIT_SEQUENCE, and RESET_FAULT commands are permitted during active maintenance window %q (%s..%s).", in.ActiveMaintenanceWindow.Title, in.ActiveMaintenanceWindow.StartDate, in.ActiveMaintenanceWindow.EndDate),
		}
	}
	return nil
}

// AllowsEmergencyPreconditionBypass reports whether a command satisfies the capability
// emergency-bypass contract or the scoped protection-rule auto-rollback bypass rule.
func AllowsEmergencyPreconditionBypass(
	capability Capability,
	priorityTier PriorityTier,
	commandSource CommandSource,
	idempotencyKey string,
	bypassReason string,
) bool {
	if strings.TrimSpace(bypassReason) == "" {
		return false
	}
	if profile, ok := CapabilityProfileFor(capability); ok && profile.SupportsEmergencyBypass {
		return true
	}
	return priorityTier == PriorityProtectionRule &&
		commandSource == SourceProtectionRule &&
		(capability == CapabilitySetChilledWaterTemperatureSetpoint ||
			capability == CapabilitySetFrequency ||
			capability == CapabilitySetFanSpeed) &&
		strings.HasPrefix(idempotencyKey, "auto-rollback:")
}

func isAutomatedWorkloadWithoutOverride(in GateInput, precond PreconditionMode) bool {
	if precond == PreconditionEmergencyBypass || in.PriorityTier == PrioritySafetyInterlock || in.CommandSource == SourceSafetyInterlock {
		return false
	}
	if in.CommandSource == SourceOperator || in.PriorityTier == PriorityManualOverride {
		return false
	}
	if in.CommandSource == SourceProtectionRule && in.PriorityTier == PriorityProtectionRule && strings.HasPrefix(in.IdempotencyKey, "auto-rollback:") {
		return false
	}
	if in.CommandSource == SourceOptimization || in.CommandSource == SourceSchedule || in.CommandSource == SourceProtectionRule {
		return true
	}
	return in.PriorityTier == PriorityAIOptimization || in.PriorityTier == PrioritySchedule || in.PriorityTier == PriorityProtectionRule
}

func evaluateSubmitGate(in GateInput, profile CapabilityProfile, precond PreconditionMode) (GateDecision, *GateRejection) {
	reason := strings.TrimSpace(in.BypassReason)
	mode, _, _, deliveryErr := ValidateDeliveryAndTTL(in.Capability, in.DeliveryMode, in.TTLSeconds, in.ExpiresAt, in.Now)
	if deliveryErr != nil {
		return GateDecision{}, &GateRejection{
			HTTPStatus: http.StatusBadRequest,
			Code:       "COMMAND_REQUEST_INVALID",
			Title:      "Command request invalid",
			Detail:     deliveryErr.Error(),
		}
	}

	onlineLive := isSubmitStateOnline(in.TargetState)
	deferredOffline := false

	switch precond {
	case PreconditionEmergencyBypass:
		if !AllowsEmergencyPreconditionBypass(in.Capability, in.PriorityTier, in.CommandSource, in.IdempotencyKey, reason) {
			return GateDecision{}, &GateRejection{
				HTTPStatus: http.StatusBadRequest,
				Code:       "COMMAND_REQUEST_INVALID",
				Title:      "Command request invalid",
				Detail:     "Emergency telemetry precondition bypass is only allowed on shutdown capabilities or protection-rule auto-rollback with an explicit reason.",
			}
		}
	case PreconditionRequireCurrentState:
		if mode == DeliveryModeQueueIfOffline && !onlineLive {
			if isOnlineBadQualityState(in.TargetState) {
				if rejection := validateSubmitLiveState(in.TargetState); rejection != nil {
					return GateDecision{}, rejection
				}
			}
			deferredOffline = true
		} else if rejection := validateSubmitLiveState(in.TargetState); rejection != nil {
			return GateDecision{}, rejection
		}
	default:
		return GateDecision{}, rejectInvalidParameters(GatePhaseSubmit)
	}

	if !deferredOffline {
		if rejection := evaluatePeerInterlocks(in, GatePhaseSubmit); rejection != nil {
			return GateDecision{}, rejection
		}
	}

	var currentVal *float64
	if onlineLive && in.TargetState != nil {
		currentVal = in.TargetState.CurrentValue
	}
	risk, _, ok := EvaluateRiskLevel(in.Capability, in.Parameters, currentVal)
	if !ok {
		if precond == PreconditionEmergencyBypass &&
			AllowsEmergencyPreconditionBypass(in.Capability, in.PriorityTier, in.CommandSource, in.IdempotencyKey, reason) &&
			currentVal == nil {
			risk = profile.BaseRisk
		} else if deferredOffline && currentVal == nil {
			if !validateStaticSetpointBounds(profile, in.Parameters) {
				return GateDecision{}, rejectInvalidParameters(GatePhaseSubmit)
			}
			risk = profile.BaseRisk
		} else {
			return GateDecision{}, rejectInvalidParameters(GatePhaseSubmit)
		}
	}
	stepUpRequired := (risk == RiskHigh || risk == RiskCritical || in.RequireStepUp) && !in.StepUpVerified
	return GateDecision{
		Risk:             risk,
		PreconditionMode: precond,
		BypassReason:     reason,
		StepUpRequired:   stepUpRequired,
	}, nil
}

func evaluatePreDispatchGate(in GateInput, profile CapabilityProfile, precond PreconditionMode, actuation string) (GateDecision, *GateRejection) {
	checkNow := in.Now
	if checkNow.IsZero() {
		checkNow = time.Now().UTC()
	} else {
		checkNow = checkNow.UTC()
	}
	if in.ExpiresAt != nil && !in.ExpiresAt.IsZero() && !checkNow.Before(in.ExpiresAt.UTC()) {
		return GateDecision{}, &GateRejection{
			HTTPStatus: http.StatusConflict,
			Code:       "COMMAND_TTL_EXPIRED",
			Title:      "Command TTL expired",
			Detail:     "Command survival TTL elapsed before dispatch.",
		}
	}

	if in.Capability == CapabilityStop && len(in.Interlocks) == 0 && actuation == "EMERGENCY_DO_RELEASE" {
		return GateDecision{
			PreconditionMode: precond,
			BypassReason:     strings.TrimSpace(in.BypassReason),
			EvidenceID:       "emergency-do-release:" + in.CommandID,
		}, nil
	}

	if failureCode := preDispatchStateFailureCode(in.TargetState); failureCode != "" {
		if precond == PreconditionEmergencyBypass &&
			AllowsEmergencyPreconditionBypass(in.Capability, in.PriorityTier, in.CommandSource, in.IdempotencyKey, in.BypassReason) &&
			len(in.Interlocks) == 0 {
			return GateDecision{
				PreconditionMode: PreconditionEmergencyBypass,
				BypassReason:     strings.TrimSpace(in.BypassReason),
				EvidenceID:       "emergency-precondition-bypass:" + in.CommandID,
			}, nil
		}
		if (in.Capability == CapabilityStop || in.Capability == CapabilityStopUnitSequence) && len(in.Interlocks) == 0 && (actuation == "PULSE" || actuation == "ABORT_UNIT_SEQUENCE") {
			bypassReason := strings.TrimSpace(in.BypassReason)
			if bypassReason == "" {
				bypassReason = "pulse shutdown dispatch bypass"
			}
			return GateDecision{
				PreconditionMode: PreconditionEmergencyBypass,
				BypassReason:     bypassReason,
				EvidenceID:       "shutdown-pulse-bypass:" + in.CommandID,
			}, nil
		}
		return GateDecision{}, &GateRejection{
			HTTPStatus: http.StatusConflict,
			Code:       failureCode,
			Title:      "Pre-dispatch state unsafe",
			Detail:     "Target device reported state is not safe for dispatch.",
		}
	}

	var decisionRisk RiskLevel
	if in.TargetState != nil && in.TargetState.CurrentValue != nil && profile.ParameterKey != "" {
		risk, _, ok := EvaluateRiskLevel(in.Capability, in.Parameters, in.TargetState.CurrentValue)
		if !ok && profile.MaximumDelta > 0 && isAutomatedWorkloadWithoutOverride(in, precond) {
			return GateDecision{}, &GateRejection{
				HTTPStatus: http.StatusConflict,
				Code:       "DISPATCH_SAFETY_PRE_DISPATCH_DELTA_EXCEEDED",
				Title:      "Pre-dispatch delta exceeded",
				Detail:     "Target parameter delta exceeds capability maximumDelta for automated command at pre-dispatch (PRE_DISPATCH_DELTA_EXCEEDED).",
			}
		}
		decisionRisk = risk
	}

	if rejection := evaluatePeerInterlocks(in, GatePhasePreDispatch); rejection != nil {
		return GateDecision{}, rejection
	}
	return GateDecision{
		Risk:             decisionRisk,
		PreconditionMode: precond,
		BypassReason:     strings.TrimSpace(in.BypassReason),
	}, nil
}

func isSubmitStateOnline(state *LivePointObservation) bool {
	if state == nil {
		return false
	}
	return state.EvaluationAvailability == "AVAILABLE" &&
		state.Presence == "ONLINE" &&
		state.Readiness == "CURRENT" &&
		state.Freshness == "FRESH" &&
		state.Quality == "GOOD" &&
		state.BusinessRevision != 0 &&
		!state.ObservedAt.IsZero()
}

func isOnlineBadQualityState(state *LivePointObservation) bool {
	if state == nil {
		return false
	}
	return state.EvaluationAvailability == "AVAILABLE" &&
		state.Presence == "ONLINE" &&
		state.Readiness == "CURRENT" &&
		state.Freshness == "FRESH" &&
		state.Quality != "GOOD"
}

func validateStaticSetpointBounds(profile CapabilityProfile, params CommandParameters) bool {
	if profile.ParameterKey == "" {
		return true
	}
	val, ok := params[profile.ParameterKey]
	if !ok {
		return false
	}
	return val >= profile.Minimum && val <= profile.Maximum
}

func validateSubmitLiveState(state *LivePointObservation) *GateRejection {
	if state == nil {
		return &GateRejection{
			HTTPStatus: http.StatusServiceUnavailable,
			Code:       "COMMAND_UNAVAILABLE",
			Title:      "Command unavailable",
			Detail:     "The current Device state is unavailable for control.",
			Retryable:  true,
		}
	}
	if !isSubmitStateOnline(state) {
		detail := "The current Device state does not satisfy the control preconditions."
		if strings.EqualFold(strings.TrimSpace(state.Presence), "SLEEPING") {
			detail = "Target Device is currently SLEEPING; use deliveryMode=QUEUE_IF_OFFLINE with ttlSeconds or retry after wake-up."
		}
		return &GateRejection{
			HTTPStatus: http.StatusConflict,
			Code:       "COMMAND_CURRENT_STATE_UNSAFE",
			Title:      "Current state unsafe",
			Detail:     detail,
		}
	}
	return nil
}

func preDispatchStateFailureCode(state *LivePointObservation) string {
	if state == nil {
		return "DISPATCH_SAFETY_STATE_UNAVAILABLE"
	}
	switch {
	case state.EvaluationAvailability != "AVAILABLE":
		return "DISPATCH_SAFETY_STATE_UNAVAILABLE"
	case strings.EqualFold(strings.TrimSpace(state.Presence), "SLEEPING"):
		return "DISPATCH_SAFETY_DEVICE_SLEEPING"
	case state.Presence != "ONLINE":
		return "DISPATCH_SAFETY_DEVICE_NOT_ONLINE"
	case state.Readiness != "CURRENT":
		return "DISPATCH_SAFETY_STATE_NOT_CURRENT"
	case state.Freshness != "FRESH":
		return "DISPATCH_SAFETY_STATE_NOT_FRESH"
	case state.Quality != "GOOD":
		return "DISPATCH_SAFETY_STATE_QUALITY_UNSAFE"
	case state.ObservedAt.IsZero():
		return "DISPATCH_SAFETY_STATE_TIMESTAMP_MISSING"
	default:
		return ""
	}
}

func evaluatePeerInterlocks(in GateInput, phase GatePhase) *GateRejection {
	for _, check := range in.Interlocks {
		peer := in.PeerStates[PeerStateKey(check.DeviceID, check.Key)]
		if peer == nil || preDispatchStateFailureCode(peer) != "" {
			if phase == GatePhasePreDispatch {
				return &GateRejection{HTTPStatus: http.StatusConflict, Code: "DISPATCH_SAFETY_INTERLOCK_STATE_UNAVAILABLE"}
			}
			detail := "Prerequisite peer device state is unavailable or stale for safe execution."
			if check.Reason != "" {
				detail += " (" + check.Reason + ")"
			}
			return &GateRejection{
				HTTPStatus: http.StatusConflict,
				Code:       "COMMAND_INTERLOCK_UNVERIFIABLE",
				Title:      "Command Interlock Precondition Unavailable",
				Detail:     detail,
			}
		}
		if !ScalarMatches(peer.ReportedValue, TextScalar(check.Expected), 0) {
			if phase == GatePhasePreDispatch {
				return &GateRejection{HTTPStatus: http.StatusConflict, Code: "DISPATCH_SAFETY_INTERLOCK_VIOLATED"}
			}
			detail := "Command is blocked by hydraulic interlock preconditions."
			if check.Reason != "" {
				detail += " " + check.Reason
			}
			return &GateRejection{
				HTTPStatus: http.StatusConflict,
				Code:       "COMMAND_INTERLOCK_BLOCKED",
				Title:      "Command Interlock Blocked",
				Detail:     detail,
			}
		}
	}
	return nil
}

// PeerStateKey builds the canonical lookup key for GateInput.PeerStates.
func PeerStateKey(deviceID, pointKey string) string {
	return deviceID + "\x00" + pointKey
}

func containsGateDevice(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func rejectInvalidCapability(phase GatePhase) *GateRejection {
	if phase == GatePhasePreDispatch {
		return &GateRejection{HTTPStatus: http.StatusConflict, Code: "DISPATCH_SAFETY_UNAVAILABLE"}
	}
	return &GateRejection{
		HTTPStatus: http.StatusServiceUnavailable,
		Code:       "COMMAND_UNAVAILABLE",
		Title:      "Command unavailable",
		Detail:     "The COMMAND Point does not declare a supported authoritative capability contract.",
		Retryable:  true,
	}
}

func rejectInvalidParameters(phase GatePhase) *GateRejection {
	if phase == GatePhasePreDispatch {
		return &GateRejection{HTTPStatus: http.StatusConflict, Code: "DISPATCH_SAFETY_UNAVAILABLE"}
	}
	return &GateRejection{
		HTTPStatus: http.StatusBadRequest,
		Code:       "COMMAND_REQUEST_INVALID",
		Title:      "Command request invalid",
		Detail:     "The Asset capability parameters are invalid.",
	}
}
