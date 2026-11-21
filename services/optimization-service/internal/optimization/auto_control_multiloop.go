package optimization

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandauth"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/intelligencemodel"
)

type auxiliaryLoopBounds struct {
	unit     string
	hardMin  float64
	hardMax  float64
	maxStep  float64
	deadband float64
}

func normalizeMonitorLoopKey(raw string, capability commandmodel.Capability) string {
	trimmed := strings.ToUpper(strings.TrimSpace(raw))
	if trimmed != "" {
		return trimmed
	}
	switch capability {
	case commandmodel.CapabilitySetFrequency:
		return LoopKeyChilledWaterPump
	case commandmodel.CapabilitySetFanSpeed:
		return LoopKeyCoolingTowerFan
	default:
		return LoopKeyPlant
	}
}

func effectiveMonitorCapability(monitor OptimizationExecutionMonitorRecord) commandmodel.Capability {
	clean := strings.ToUpper(strings.TrimSpace(monitor.TargetCapability))
	if clean == "" {
		return commandmodel.CapabilitySetChilledWaterTemperatureSetpoint
	}
	return commandmodel.Capability(clean)
}

func effectiveMonitorTargetUnit(monitor OptimizationExecutionMonitorRecord) string {
	switch strings.TrimSpace(monitor.TargetUnit) {
	case TargetUnitHz:
		return TargetUnitHz
	case TargetUnitPct:
		return TargetUnitPct
	case TargetUnitC:
		return TargetUnitC
	}
	switch effectiveMonitorCapability(monitor) {
	case commandmodel.CapabilitySetFrequency:
		return TargetUnitHz
	case commandmodel.CapabilitySetFanSpeed:
		return TargetUnitPct
	default:
		return TargetUnitC
	}
}

func targetUnitForCapability(capability commandmodel.Capability) string {
	switch capability {
	case commandmodel.CapabilitySetFrequency:
		return TargetUnitHz
	case commandmodel.CapabilitySetFanSpeed:
		return TargetUnitPct
	default:
		return TargetUnitC
	}
}

func commandParametersForCapability(capability commandmodel.Capability, value float64) (commandmodel.CommandParameters, bool) {
	switch capability {
	case commandmodel.CapabilitySetChilledWaterTemperatureSetpoint, commandmodel.CapabilitySetTemperatureSetpoint:
		return commandmodel.CommandParameters{commandmodel.ParameterSetpointC: value}, true
	case commandmodel.CapabilitySetFrequency:
		return commandmodel.CommandParameters{commandmodel.ParameterFrequencyHz: value}, true
	case commandmodel.CapabilitySetFanSpeed:
		return commandmodel.CommandParameters{commandmodel.ParameterFanSpeedPct: value}, true
	default:
		return nil, false
	}
}

func fallbackVerificationKeyForCapability(capability commandmodel.Capability, mapping InputMapping) string {
	switch capability {
	case commandmodel.CapabilitySetFrequency:
		return "frequency_hz"
	case commandmodel.CapabilitySetFanSpeed:
		return "fan_speed_pct"
	default:
		if clean := strings.TrimSpace(mapping.SupplyTemperatureKey); clean != "" {
			return clean
		}
		return "supply_water_temperature"
	}
}

func resolveAuxiliaryLoopBounds(capability commandmodel.Capability, lease *AutoControlLeaseRecord) (auxiliaryLoopBounds, bool) {
	switch capability {
	case commandmodel.CapabilitySetFrequency:
		minHz := commandmodel.DefaultPumpFrequencyMinHz
		maxHz := commandmodel.DefaultPumpFrequencyMaxHz
		maxStep := commandmodel.DefaultFrequencyMaxStepHz
		deadband := commandmodel.DefaultFrequencyDeadbandHz
		if lease != nil {
			if lease.PumpFreqMinHz != nil && finite(*lease.PumpFreqMinHz) {
				minHz = math.Max(minHz, *lease.PumpFreqMinHz)
			}
			if lease.PumpFreqMaxHz != nil && finite(*lease.PumpFreqMaxHz) {
				maxHz = math.Min(maxHz, *lease.PumpFreqMaxHz)
			}
			if lease.MaxFreqStepHz != nil && finite(*lease.MaxFreqStepHz) && *lease.MaxFreqStepHz > 0 {
				maxStep = *lease.MaxFreqStepHz
			}
			if lease.FreqDeadbandHz != nil && finite(*lease.FreqDeadbandHz) && *lease.FreqDeadbandHz > 0 {
				deadband = *lease.FreqDeadbandHz
			}
		}
		if minHz > maxHz {
			return auxiliaryLoopBounds{}, false
		}
		return auxiliaryLoopBounds{
			unit:     TargetUnitHz,
			hardMin:  minHz,
			hardMax:  maxHz,
			maxStep:  maxStep,
			deadband: deadband,
		}, true
	case commandmodel.CapabilitySetFanSpeed:
		minPct := commandmodel.DefaultFanSpeedMinPct
		maxPct := commandmodel.DefaultFanSpeedMaxPct
		maxStep := commandmodel.DefaultFanSpeedMaxStepPct
		deadband := commandmodel.DefaultFanSpeedDeadbandPct
		if lease != nil {
			if lease.FanSpeedMinPct != nil && finite(*lease.FanSpeedMinPct) {
				minPct = math.Max(minPct, *lease.FanSpeedMinPct)
			}
			if lease.FanSpeedMaxPct != nil && finite(*lease.FanSpeedMaxPct) {
				maxPct = math.Min(maxPct, *lease.FanSpeedMaxPct)
			}
			if lease.MaxFanStepPct != nil && finite(*lease.MaxFanStepPct) && *lease.MaxFanStepPct > 0 {
				maxStep = *lease.MaxFanStepPct
			}
			if lease.FanDeadbandPct != nil && finite(*lease.FanDeadbandPct) && *lease.FanDeadbandPct > 0 {
				deadband = *lease.FanDeadbandPct
			}
		}
		if minPct > maxPct {
			return auxiliaryLoopBounds{}, false
		}
		return auxiliaryLoopBounds{
			unit:     TargetUnitPct,
			hardMin:  minPct,
			hardMax:  maxPct,
			maxStep:  maxStep,
			deadband: deadband,
		}, true
	default:
		return auxiliaryLoopBounds{}, false
	}
}

func hasEligibleAuxiliaryTarget(
	lease *AutoControlLeaseRecord,
	siteCtx AutoControlSiteContext,
	targets []intelligencemodel.OptimizationAuxiliaryTarget,
) bool {
	if lease == nil || len(targets) == 0 || len(siteCtx.AuxiliaryTargets) == 0 {
		return false
	}
	for _, aux := range targets {
		capName := commandmodel.Capability(strings.TrimSpace(aux.Capability))
		if !slices.Contains(lease.AllowedCapabilities, string(capName)) {
			continue
		}
		loopKey := normalizeMonitorLoopKey(aux.LoopKey, capName)
		if _, matched := findMatchingAuxiliaryTarget(siteCtx, loopKey, capName); matched {
			return true
		}
	}
	return false
}

func findMatchingAuxiliaryTarget(
	siteCtx AutoControlSiteContext,
	loopKey string,
	capability commandmodel.Capability,
) (AuxiliaryLoopTarget, bool) {
	cleanLoop := strings.ToUpper(strings.TrimSpace(loopKey))
	for _, item := range siteCtx.AuxiliaryTargets {
		if strings.EqualFold(strings.TrimSpace(item.LoopKey), cleanLoop) && item.Capability == capability {
			return item, true
		}
	}
	for _, item := range siteCtx.AuxiliaryTargets {
		if item.Capability == capability && cleanLoop == "" {
			return item, true
		}
	}
	return AuxiliaryLoopTarget{}, false
}

func resolveMonitorCommandTarget(monitor OptimizationExecutionMonitorRecord, siteCtx AutoControlSiteContext) *SetpointCommandTarget {
	capability := effectiveMonitorCapability(monitor)
	if capability == commandmodel.CapabilitySetChilledWaterTemperatureSetpoint ||
		capability == commandmodel.CapabilitySetTemperatureSetpoint {
		return siteCtx.SetpointTarget
	}
	loopKey := normalizeMonitorLoopKey(monitor.LoopKey, capability)
	for _, aux := range siteCtx.AuxiliaryTargets {
		if strings.TrimSpace(monitor.DeviceID) != "" && aux.Target.DeviceID == monitor.DeviceID && aux.Capability == capability {
			copyTarget := aux.Target
			return &copyTarget
		}
	}
	if matched, ok := findMatchingAuxiliaryTarget(siteCtx, loopKey, capability); ok {
		copyTarget := matched.Target
		return &copyTarget
	}
	return nil
}

func (controller *AutoController) dispatchAuxiliaryLoops(
	ctx context.Context,
	recommendation Recommendation,
	lease *AutoControlLeaseRecord,
	siteCtx AutoControlSiteContext,
	healthSnapshot OptimizationPointHealthSnapshot,
	liveState autoControlLiveState,
	decisionID, traceID, executionMode string,
	now time.Time,
) []AuxiliaryLoopDispatchOutcome {
	if len(recommendation.AuxiliaryTargets) == 0 || lease == nil {
		return nil
	}
	outcomes := make([]AuxiliaryLoopDispatchOutcome, 0, len(recommendation.AuxiliaryTargets))
	for _, rawAux := range recommendation.AuxiliaryTargets {
		outcomes = append(outcomes, controller.dispatchSingleAuxiliaryLoop(
			ctx, recommendation, rawAux, lease, siteCtx, healthSnapshot, liveState, decisionID, traceID, executionMode, now,
		))
	}
	return outcomes
}

func (controller *AutoController) dispatchSingleAuxiliaryLoop(
	ctx context.Context,
	recommendation Recommendation,
	rawAux intelligencemodel.OptimizationAuxiliaryTarget,
	lease *AutoControlLeaseRecord,
	siteCtx AutoControlSiteContext,
	healthSnapshot OptimizationPointHealthSnapshot,
	liveState autoControlLiveState,
	decisionID, traceID, executionMode string,
	now time.Time,
) AuxiliaryLoopDispatchOutcome {
	capName := commandmodel.Capability(strings.TrimSpace(rawAux.Capability))
	loopKey := normalizeMonitorLoopKey(rawAux.LoopKey, capName)
	outcome := AuxiliaryLoopDispatchOutcome{
		LoopKey:    loopKey,
		Capability: string(capName),
	}

	bounds, supported := resolveAuxiliaryLoopBounds(capName, lease)
	if !supported {
		outcome.SkipReason = "AUX_CAPABILITY_UNSUPPORTED"
		return outcome
	}
	if !slices.Contains(lease.AllowedCapabilities, string(capName)) {
		outcome.SkipReason = "CAPABILITY_NOT_LEASED"
		return outcome
	}
	if !finite(rawAux.TargetValue) || rawAux.TargetValue <= 0 {
		outcome.SkipReason = "AUX_TARGET_VALUE_INVALID"
		return outcome
	}
	auxTarget, found := findMatchingAuxiliaryTarget(siteCtx, loopKey, capName)
	if !found {
		outcome.SkipReason = "AUX_COMMAND_TARGET_MISSING"
		return outcome
	}
	outcome.DeviceID = auxTarget.Target.DeviceID
	if auxTarget.Target.AssetLocked || hasAnyAssetLock(siteCtx.Topology.AssetControlLocks) {
		outcome.SkipReason = "ASSET_CONTROL_LOCKED"
		return outcome
	}
	if executionMode == ExecutionModeClosedLoop && siteCtx.ActiveLoopMonitors[loopKey] {
		outcome.SkipReason = "ACTIVE_MONITOR_EXISTS"
		return outcome
	}

	auxLive := extractAuxiliaryTargetLiveState(healthSnapshot.LivePoints, auxTarget)
	var currentFeedbackPtr *float64
	if auxLive.Feedback != nil && auxLive.Feedback.NumericValue != nil && finite(*auxLive.Feedback.NumericValue) {
		v := *auxLive.Feedback.NumericValue
		currentFeedbackPtr = &v
	}

	hydraulicInterlockOK := liveState.RunningChillers >= lease.MinRunningChillers
	guardrailPoints := buildAuxiliaryGuardrailPointSamples(siteCtx, liveState, auxTarget, auxLive)
	guardrailResult := commandmodel.EvaluateGuardrails(commandmodel.GuardrailInput{
		Now:                  now,
		Capability:           capName,
		DeviceID:             auxTarget.Target.DeviceID,
		ProposedValue:        rawAux.TargetValue,
		CurrentSetpoint:      currentFeedbackPtr,
		CurrentMeasured:      currentFeedbackPtr,
		Points:               guardrailPoints,
		MaxPointAge:          DefaultAutoControlMaxPointAge,
		HardMin:              float64Ptr(bounds.hardMin),
		HardMax:              float64Ptr(bounds.hardMax),
		MaxStep:              float64Ptr(bounds.maxStep),
		Deadband:             float64Ptr(bounds.deadband),
		ManualBackoffUntil:   lease.ManualBackoffUntil,
		DeviceFaultActive:    liveState.DeviceFaultActive || auxLive.DeviceFaultActive,
		CabinetLocalManual:   liveState.CabinetLocalManual || auxLive.CabinetLocalManual,
		DeviceOffline:        liveState.DeviceOffline || auxLive.DeviceOffline,
		HydraulicInterlockOK: boolPtr(hydraulicInterlockOK),
	})
	guardrailCopy := guardrailResult
	traceBytes := encodeOptimizationJSON(guardrailResult.Trace)
	if len(traceBytes) == 0 || string(traceBytes) == "null" {
		traceBytes = []byte("[]")
	}

	siteCopy := siteCtx
	liveCopy := liveState
	fwState := dispatchFlywheelState{
		recommendation:   recommendation,
		lease:            lease,
		siteCtx:          &siteCopy,
		liveState:        &liveCopy,
		guardrail:        &guardrailCopy,
		guardrailTrace:   traceBytes,
		loopKey:          loopKey,
		baselineSupplyC:  currentFeedbackPtr,
		recommendedValue: float64Ptr(rawAux.TargetValue),
		decisionID:       decisionID,
		traceID:          traceID,
		now:              now,
	}

	skipAux := func(reason string, dispatched, shadowRecorded bool) AuxiliaryLoopDispatchOutcome {
		controller.recordDispatchFlywheel(ctx, &fwState, executionMode, reason, dispatched, shadowRecorded)
		outcome.SkipReason = reason
		outcome.Dispatched = dispatched
		outcome.ShadowRecorded = shadowRecorded
		return outcome
	}

	switch guardrailResult.Verdict {
	case commandmodel.GuardrailVerdictDeadbandSkip:
		return skipAux(skipReasonOrDefault(guardrailResult.ReasonCode, "DEADBAND_SKIP"), false, false)
	case commandmodel.GuardrailVerdictVetoed:
		return skipAux(skipReasonOrDefault(guardrailResult.ReasonCode, "GUARDRAIL_VETOED"), false, false)
	}
	if !guardrailResult.Allowed {
		return skipAux(skipReasonOrDefault(guardrailResult.ReasonCode, "GUARDRAIL_VETOED"), false, false)
	}

	if auxLive.Feedback == nil || !isLivePointDispatchable(*auxLive.Feedback, now, DefaultAutoControlMaxPointAge) {
		return skipAux("AUX_TELEMETRY_REVALIDATION_FAILED", false, false)
	}
	baselineVal := *auxLive.Feedback.NumericValue

	appliedVal := rawAux.TargetValue
	var clampedPtr *float64
	if guardrailResult.Verdict == commandmodel.GuardrailVerdictClamped {
		if !finite(guardrailResult.AdjustedValue) || guardrailResult.AdjustedValue <= 0 {
			return skipAux("GUARDRAIL_INVALID_ADJUSTMENT", false, false)
		}
		appliedVal = guardrailResult.AdjustedValue
		clampedCopy := appliedVal
		clampedPtr = &clampedCopy
	} else if finite(guardrailResult.AdjustedValue) && guardrailResult.AdjustedValue > 0 {
		appliedVal = guardrailResult.AdjustedValue
	}
	if appliedVal < bounds.hardMin || appliedVal > bounds.hardMax {
		return skipAux("TARGET_OUTSIDE_LEASE_BOUNDS", false, false)
	}

	params, ok := commandParametersForCapability(capName, appliedVal)
	if !ok {
		return skipAux("CAPABILITY_UNSUPPORTED", false, false)
	}
	profile, ok := commandmodel.CapabilityProfileFor(capName)
	if !ok {
		return skipAux("CAPABILITY_UNSUPPORTED", false, false)
	}

	dispatchReg := AutomatedDispatchRegistration{
		Recommendation:   recommendation,
		LeaseID:          lease.ID,
		Target:           auxTarget.Target,
		TargetCapability: capName,
		LoopKey:          loopKey,
		TargetUnit:       bounds.unit,
		BaselineSupplyC:  baselineVal,
		AppliedSupplyC:   appliedVal,
		RestoreSupplyC:   baselineVal,
		ZoneMaxC:         lease.ZoneTempMaxC,
		SupplyMinC:       bounds.hardMin,
		SupplyMaxC:       bounds.hardMax,
		RawRecommendedC:  rawAux.TargetValue,
		ClampedC:         clampedPtr,
		GuardrailVerdict: string(guardrailResult.Verdict),
		GuardrailTrace:   traceBytes,
		ExecutionMode:    executionMode,
		DecisionID:       decisionID,
		TraceID:          traceID,
		At:               now,
	}

	if executionMode == ExecutionModeShadow {
		shadowResult, shadowErr := controller.store.RecordShadowMonitor(ctx, dispatchReg)
		if shadowErr != nil {
			outcome.SkipReason = "SHADOW_PERSIST_FAILED"
			return outcome
		}
		if shadowResult.SkipReason != "" {
			return skipAux(shadowResult.SkipReason, false, false)
		}
		fwState.monitorID = shadowResult.Monitor.ID
		fwState.appliedSupplyC = float64Ptr(appliedVal)
		controller.recordDispatchFlywheel(ctx, &fwState, ExecutionModeShadow, "", false, true)
		outcome.ShadowRecorded = true
		outcome.MonitorID = shadowResult.Monitor.ID
		return outcome
	}

	currentVal := baselineVal
	grantReq := commandauth.WorkloadDecisionRequest{
		TenantID:           recommendation.TenantID,
		SiteID:             recommendation.SiteID,
		DeviceID:           auxTarget.Target.DeviceID,
		Capability:         capName,
		CapabilityRevision: profile.Revision,
		Purpose:            commandmodel.AuthorizationCommandSubmit,
		Parameters:         params,
		CurrentValue:       &currentVal,
		IsRollback:         false,
	}
	grant, decision, err := controller.dispatcher.RequestCommandGrant(ctx, grantReq)
	if err != nil {
		outcome.SkipReason = "IAM_GRANT_ERROR"
		return outcome
	}
	if !decision.Allowed || grant == "" {
		return skipAux("IAM_DENIED:"+string(decision.ReasonCode), false, false)
	}

	regResult, err := controller.store.PreRegisterAutomatedDispatch(ctx, dispatchReg)
	if err != nil {
		outcome.SkipReason = "MONITOR_PREREGISTER_FAILED"
		return outcome
	}
	if regResult.SkipReason != "" {
		return skipAux(regResult.SkipReason, false, false)
	}
	monitor := regResult.Monitor
	outcome.MonitorID = monitor.ID

	verificationKey := strings.TrimSpace(auxTarget.Target.FeedbackPointKey)
	if verificationKey == "" {
		verificationKey = fallbackVerificationKeyForCapability(capName, siteCtx.InputMapping)
	}
	availability, presence, readiness, quality := livePointCommandCurrentState(*auxLive.Feedback, true, now)

	commandIntentID, submitErr := controller.dispatcher.SubmitAutoCommand(ctx, grant, AutoControlDispatchInput{
		TenantID:               recommendation.TenantID,
		SiteID:                 recommendation.SiteID,
		DeviceID:               auxTarget.Target.DeviceID,
		PointID:                auxTarget.Target.PointID,
		Capability:             capName,
		Parameters:             params,
		VerificationPointKey:   verificationKey,
		IdempotencyKey:         "auto-opt:" + recommendation.ID + ":" + strings.ToLower(loopKey),
		IsRollback:             false,
		PreconditionMode:       commandmodel.PreconditionRequireCurrentState,
		EvaluationAvailability: availability,
		Presence:               presence,
		Readiness:              readiness,
		Quality:                quality,
		CurrentValue:           float64Ptr(currentVal),
		BusinessRevision:       uint64(auxLive.Feedback.BusinessRevision),
		ObservedAt:             auxLive.Feedback.SampledAt.UTC(),
		DecisionID:             decisionID,
		TraceID:                traceID,
	})
	if submitErr != nil || strings.TrimSpace(commandIntentID) == "" {
		errToRecord := submitErr
		if errToRecord == nil {
			errToRecord = errorsNewSubmitEmpty()
		}
		failCtx, failCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		if markErr := controller.store.MarkAutomatedDispatchFailed(failCtx, recommendation.TenantID, recommendation.SiteID, monitor.ID, errToRecord.Error(), now); markErr != nil {
			controller.logf("aux monitor %s mark submit failed error: %v", monitor.ID, markErr)
		}
		failCancel()
		fwState.monitorID = monitor.ID
		return skipAux("SUBMIT_FAILED", false, false)
	}

	activateCtx, activateCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	if err := controller.store.ActivateAutomatedDispatch(activateCtx, recommendation, monitor.ID, commandIntentID, now); err != nil {
		activateCancel()
		outcome.CommandIntentID = commandIntentID
		outcome.SkipReason = "ACTIVATE_MONITOR_FAILED"
		return outcome
	}
	activateCancel()

	fwState.monitorID = monitor.ID
	fwState.appliedSupplyC = float64Ptr(appliedVal)
	controller.recordDispatchFlywheel(ctx, &fwState, ExecutionModeClosedLoop, "", true, false)
	controller.logf("optimization auxiliary loop dispatched loop=%s capability=%s site=%s command=%s monitor=%s target=%.2f%s",
		loopKey, capName, recommendation.SiteID, commandIntentID, monitor.ID, appliedVal, bounds.unit)
	outcome.Dispatched = true
	outcome.CommandIntentID = commandIntentID
	return outcome
}

func errorsNewSubmitEmpty() error {
	return fmt.Errorf("gateway auto-command returned empty commandId")
}

func resolveMonitorLiveFeedback(
	points []TelemetryLivePoint,
	siteCtx AutoControlSiteContext,
	liveState autoControlLiveState,
	monitor OptimizationExecutionMonitorRecord,
) (*TelemetryLivePoint, bool, bool) {
	capName := effectiveMonitorCapability(monitor)
	if capName == commandmodel.CapabilitySetChilledWaterTemperatureSetpoint ||
		capName == commandmodel.CapabilitySetTemperatureSetpoint {
		return liveState.Setpoint, liveState.DeviceFaultActive, liveState.CabinetLocalManual
	}
	target := resolveMonitorCommandTarget(monitor, siteCtx)
	configuredKey := ""
	if target != nil {
		configuredKey = target.FeedbackPointKey
	}
	auxTarget := AuxiliaryLoopTarget{
		LoopKey:    normalizeMonitorLoopKey(monitor.LoopKey, capName),
		Capability: capName,
		Target: SetpointCommandTarget{
			DeviceID:         monitor.DeviceID,
			FeedbackPointKey: configuredKey,
		},
	}
	auxLive := extractAuxiliaryTargetLiveState(points, auxTarget)
	return auxLive.Feedback, auxLive.DeviceFaultActive, auxLive.CabinetLocalManual
}

func tamperThresholdForMonitor(monitor OptimizationExecutionMonitorRecord) float64 {
	switch effectiveMonitorTargetUnit(monitor) {
	case TargetUnitHz:
		return DefaultFrequencyTamperThresholdHz
	case TargetUnitPct:
		return DefaultFanSpeedTamperThresholdPct
	default:
		return DefaultCabinetTamperThresholdC
	}
}

func trackingThresholdForMonitor(monitor OptimizationExecutionMonitorRecord, siteCtx AutoControlSiteContext) float64 {
	switch effectiveMonitorTargetUnit(monitor) {
	case TargetUnitHz:
		leaseDeadband := 0.0
		if siteCtx.LeaseFreqDeadbandHz != nil && finite(*siteCtx.LeaseFreqDeadbandHz) {
			leaseDeadband = *siteCtx.LeaseFreqDeadbandHz
		}
		return math.Max(MaxFrequencyDeviationHz, leaseDeadband)
	case TargetUnitPct:
		leaseDeadband := 0.0
		if siteCtx.LeaseFanDeadbandPct != nil && finite(*siteCtx.LeaseFanDeadbandPct) {
			leaseDeadband = *siteCtx.LeaseFanDeadbandPct
		}
		return math.Max(MaxFanSpeedDeviationPct, leaseDeadband)
	default:
		return math.Max(MaxSetpointDeviationC, siteCtx.LeaseDeadbandC)
	}
}
