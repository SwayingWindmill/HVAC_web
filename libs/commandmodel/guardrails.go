package commandmodel

import (
	"fmt"
	"math"
	"strings"
	"time"
)

const (
	DefaultMaxPointFreshness      = 90 * time.Second
	DefaultChilledWaterSupplyMinC = 5.0
	DefaultChilledWaterSupplyMaxC = 15.0
	DefaultPumpFrequencyMinHz     = 28.0
	DefaultPumpFrequencyMaxHz     = 50.0
	DefaultFanSpeedMinPct         = 25.0
	DefaultFanSpeedMaxPct         = 100.0
	DefaultValveOpeningMinPct     = 15.0
	DefaultValveOpeningMaxPct     = 100.0
	DefaultChilledWaterMaxStepC   = 1.0
	DefaultFrequencyMaxStepHz     = 2.5
	DefaultFanSpeedMaxStepPct     = 10.0
	DefaultChilledWaterDeadbandC  = 0.2
	DefaultFrequencyDeadbandHz    = 0.5
	DefaultFanSpeedDeadbandPct    = 2.0
)

type GuardrailVerdict string

const (
	GuardrailVerdictPass         GuardrailVerdict = "PASS"
	GuardrailVerdictClamped      GuardrailVerdict = "CLAMPED"
	GuardrailVerdictDeadbandSkip GuardrailVerdict = "DEADBAND_SKIP"
	GuardrailVerdictVetoed       GuardrailVerdict = "VETOED"
)

const (
	guardrailStageValidity          = "validity"
	guardrailStageFreshness         = "freshness"
	guardrailStageFaultAndLocalVeto = "fault_and_local_veto"
	guardrailStageManualBackoff     = "manual_backoff"
	guardrailStageInterlockAndDwell = "interlock_and_dwell"
	guardrailStageHardClamp         = "hard_clamp"
	guardrailStageSlewRate          = "slew_rate"
	guardrailStageDeadband          = "deadband"
)

type GuardrailStageResult struct {
	Stage  string  `json:"stage"`
	Action string  `json:"action"`
	Before float64 `json:"before"`
	After  float64 `json:"after"`
	Reason string  `json:"reason,omitempty"`
}

type GuardrailPointSample struct {
	Key         string
	Value       *float64
	BoolValue   *bool
	StringValue string
	Quality     string
	SampledAt   time.Time
	Present     bool
}

type GuardrailInput struct {
	Now                  time.Time
	Capability           Capability
	DeviceID             string
	ProposedValue        float64
	CurrentSetpoint      *float64
	CurrentMeasured      *float64
	Points               []GuardrailPointSample
	MaxPointAge          time.Duration
	HardMin              *float64
	HardMax              *float64
	MaxStep              *float64
	Deadband             *float64
	ManualBackoffUntil   *time.Time
	DeviceFaultActive    bool
	CabinetLocalManual   bool
	DeviceOffline        bool
	HydraulicInterlockOK *bool
	LastTransitionAt     *time.Time
	MinDwellDuration     time.Duration
}

type GuardrailResult struct {
	Verdict       GuardrailVerdict       `json:"verdict"`
	Allowed       bool                   `json:"allowed"`
	ReasonCode    string                 `json:"reasonCode,omitempty"`
	Detail        string                 `json:"detail,omitempty"`
	RawValue      float64                `json:"rawValue"`
	AdjustedValue float64                `json:"adjustedValue"`
	Trace         []GuardrailStageResult `json:"trace"`
}

// EvaluateGuardrails executes the deterministic 8-stage (0..7) physical safety
// guardrail chain for a proposed numeric control value.
func EvaluateGuardrails(in GuardrailInput) GuardrailResult {
	trace := make([]GuardrailStageResult, 0, 8)
	now := in.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	// Stage 0: validity
	if !isFiniteGuardrailFloat(in.ProposedValue) {
		return vetoGuardrail(
			trace,
			0,
			guardrailStageValidity,
			"invalid_proposed_value",
			"proposed value must be a finite number",
			0,
		)
	}
	raw := in.ProposedValue
	val := raw
	trace = append(trace, passStage(guardrailStageValidity, val))

	// Stage 1: freshness
	maxAge := in.MaxPointAge
	if maxAge <= 0 {
		maxAge = DefaultMaxPointFreshness
	}
	if detail, bad := inspectPointFreshness(in.Points, now, maxAge); bad {
		return vetoGuardrail(trace, raw, guardrailStageFreshness, "stale_or_bad_telemetry", detail, val)
	}
	trace = append(trace, passStage(guardrailStageFreshness, val))

	// Stage 2: fault_and_local_veto
	if in.DeviceOffline {
		return vetoGuardrail(trace, raw, guardrailStageFaultAndLocalVeto, "device_offline", "target device is offline", val)
	}
	if reasonDetail, blocked := inspectFaultAndLocalState(in); blocked {
		return vetoGuardrail(trace, raw, guardrailStageFaultAndLocalVeto, "device_fault_or_local_manual", reasonDetail, val)
	}
	trace = append(trace, passStage(guardrailStageFaultAndLocalVeto, val))

	// Stage 3: manual_backoff
	if in.ManualBackoffUntil != nil && !in.ManualBackoffUntil.IsZero() && !in.ManualBackoffUntil.Before(now) {
		return vetoGuardrail(
			trace,
			raw,
			guardrailStageManualBackoff,
			"manual_override_backoff",
			fmt.Sprintf("manual override backoff active until %s", in.ManualBackoffUntil.UTC().Format(time.RFC3339)),
			val,
		)
	}
	trace = append(trace, passStage(guardrailStageManualBackoff, val))

	// Stage 4: interlock_and_dwell
	if in.HydraulicInterlockOK != nil && !*in.HydraulicInterlockOK {
		return vetoGuardrail(
			trace,
			raw,
			guardrailStageInterlockAndDwell,
			"hydraulic_interlock_blocked",
			"hydraulic interlock prerequisite is not satisfied",
			val,
		)
	}
	if in.MinDwellDuration > 0 && in.LastTransitionAt != nil && !in.LastTransitionAt.IsZero() {
		if now.Sub(*in.LastTransitionAt) < in.MinDwellDuration {
			return vetoGuardrail(
				trace,
				raw,
				guardrailStageInterlockAndDwell,
				"min_dwell_interlock_active",
				"minimum dwell duration since last transition has not elapsed",
				val,
			)
		}
	}
	trace = append(trace, passStage(guardrailStageInterlockAndDwell, val))

	// Stage 5: hard_clamp
	bounds, validBounds := resolveHardBounds(in.Capability, in.HardMin, in.HardMax)
	if !validBounds {
		return vetoGuardrail(
			trace,
			raw,
			guardrailStageHardClamp,
			"invalid_hard_bounds",
			"effective physical hard bounds [min, max] are empty or invalid",
			val,
		)
	}
	hardClamped := false
	beforeHard := val
	if bounds.hasMin && val < bounds.minVal {
		val = bounds.minVal
		hardClamped = true
	}
	if bounds.hasMax && val > bounds.maxVal {
		val = bounds.maxVal
		hardClamped = true
	}
	if hardClamped {
		trace = append(trace, GuardrailStageResult{
			Stage:  guardrailStageHardClamp,
			Action: string(GuardrailVerdictClamped),
			Before: beforeHard,
			After:  val,
			Reason: "hard_bounds_clamped",
		})
	} else {
		trace = append(trace, passStage(guardrailStageHardClamp, val))
	}

	ref := resolveGuardrailReference(in)

	// Stage 6: slew_rate
	slewClamped := false
	maxStep, maxStepOK := resolveMaxStep(in.Capability, in.MaxStep)
	if !maxStepOK {
		return vetoGuardrail(trace, raw, guardrailStageSlewRate, "invalid_max_step", "maxStep override must be positive and finite", val)
	}
	if ref != nil && maxStep > 0 {
		beforeSlew := val
		// Absorb binary representation error at exact step boundaries (e.g. 8.2 + 1.1).
		eps := 1e-9 * math.Max(1.0, math.Abs(*ref)+maxStep)
		upper := roundGuardrailDecimal(*ref + maxStep)
		lower := roundGuardrailDecimal(*ref - maxStep)
		if val > *ref+maxStep+eps {
			val = clampToBounds(upper, bounds)
			slewClamped = val != beforeSlew
		} else if val < *ref-maxStep-eps {
			val = clampToBounds(lower, bounds)
			slewClamped = val != beforeSlew
		}
		if slewClamped {
			trace = append(trace, GuardrailStageResult{
				Stage:  guardrailStageSlewRate,
				Action: string(GuardrailVerdictClamped),
				Before: beforeSlew,
				After:  val,
				Reason: "slew_rate_clamped",
			})
		} else {
			trace = append(trace, passStage(guardrailStageSlewRate, val))
		}
	} else {
		trace = append(trace, passStage(guardrailStageSlewRate, val))
	}

	// Stage 7: deadband
	deadband, deadbandOK := resolveDeadband(in.Capability, in.Deadband)
	if !deadbandOK {
		return vetoGuardrail(trace, raw, guardrailStageDeadband, "invalid_deadband", "deadband override must be positive and finite", val)
	}
	if ref != nil && deadband > 0 {
		eps := 1e-9 * math.Max(1.0, math.Abs(*ref)+deadband)
		if math.Abs(val-*ref) < deadband-eps {
			trace = append(trace, GuardrailStageResult{
				Stage:  guardrailStageDeadband,
				Action: string(GuardrailVerdictDeadbandSkip),
				Before: val,
				After:  val,
				Reason: "within_deadband",
			})
			return GuardrailResult{
				Verdict:       GuardrailVerdictDeadbandSkip,
				Allowed:       false,
				ReasonCode:    "within_deadband",
				Detail:        fmt.Sprintf("adjusted value %.4g is within deadband %.4g of reference %.4g", val, deadband, *ref),
				RawValue:      raw,
				AdjustedValue: val,
				Trace:         trace,
			}
		}
	}
	trace = append(trace, passStage(guardrailStageDeadband, val))

	if hardClamped || slewClamped {
		reason := "hard_bounds_clamped"
		detail := "proposed value clamped to physical hard bounds"
		switch {
		case hardClamped && slewClamped:
			reason = "hard_and_slew_clamped"
			detail = "proposed value clamped to physical hard bounds and slew-rate step limit"
		case slewClamped:
			reason = "slew_rate_clamped"
			detail = "proposed value clamped to slew-rate step limit"
		}
		return GuardrailResult{
			Verdict:       GuardrailVerdictClamped,
			Allowed:       true,
			ReasonCode:    reason,
			Detail:        detail,
			RawValue:      raw,
			AdjustedValue: val,
			Trace:         trace,
		}
	}

	return GuardrailResult{
		Verdict:       GuardrailVerdictPass,
		Allowed:       true,
		RawValue:      raw,
		AdjustedValue: val,
		Trace:         trace,
	}
}

type guardrailBounds struct {
	hasMin bool
	minVal float64
	hasMax bool
	maxVal float64
}

func resolveHardBounds(capability Capability, hardMin, hardMax *float64) (guardrailBounds, bool) {
	var b guardrailBounds
	switch capability {
	case CapabilitySetChilledWaterTemperatureSetpoint:
		b = guardrailBounds{hasMin: true, minVal: DefaultChilledWaterSupplyMinC, hasMax: true, maxVal: DefaultChilledWaterSupplyMaxC}
	case CapabilitySetFrequency:
		b = guardrailBounds{hasMin: true, minVal: DefaultPumpFrequencyMinHz, hasMax: true, maxVal: DefaultPumpFrequencyMaxHz}
	case CapabilitySetFanSpeed:
		b = guardrailBounds{hasMin: true, minVal: DefaultFanSpeedMinPct, hasMax: true, maxVal: DefaultFanSpeedMaxPct}
	case CapabilitySetOpening:
		b = guardrailBounds{hasMin: true, minVal: DefaultValveOpeningMinPct, hasMax: true, maxVal: DefaultValveOpeningMaxPct}
	default:
		if profile, ok := CapabilityProfileFor(capability); ok && profile.ParameterKey != "" {
			b = guardrailBounds{hasMin: true, minVal: profile.Minimum, hasMax: true, maxVal: profile.Maximum}
		}
	}

	if hardMin != nil {
		if !isFiniteGuardrailFloat(*hardMin) {
			return guardrailBounds{}, false
		}
		if !b.hasMin || *hardMin > b.minVal {
			b.minVal = *hardMin
			b.hasMin = true
		}
	}
	if hardMax != nil {
		if !isFiniteGuardrailFloat(*hardMax) {
			return guardrailBounds{}, false
		}
		if !b.hasMax || *hardMax < b.maxVal {
			b.maxVal = *hardMax
			b.hasMax = true
		}
	}
	if b.hasMin && b.hasMax && b.minVal > b.maxVal {
		return guardrailBounds{}, false
	}
	return b, true
}

func clampToBounds(val float64, b guardrailBounds) float64 {
	if b.hasMin && val < b.minVal {
		return b.minVal
	}
	if b.hasMax && val > b.maxVal {
		return b.maxVal
	}
	return val
}

func resolveGuardrailReference(in GuardrailInput) *float64 {
	if in.CurrentSetpoint != nil && isFiniteGuardrailFloat(*in.CurrentSetpoint) {
		v := *in.CurrentSetpoint
		return &v
	}
	if in.CurrentMeasured != nil && isFiniteGuardrailFloat(*in.CurrentMeasured) {
		v := *in.CurrentMeasured
		return &v
	}
	return nil
}

func resolveMaxStep(capability Capability, override *float64) (float64, bool) {
	if override != nil {
		if !isFiniteGuardrailFloat(*override) || *override <= 0 {
			return 0, false
		}
		return *override, true
	}
	switch capability {
	case CapabilitySetChilledWaterTemperatureSetpoint:
		return DefaultChilledWaterMaxStepC, true
	case CapabilitySetFrequency:
		return DefaultFrequencyMaxStepHz, true
	case CapabilitySetFanSpeed:
		return DefaultFanSpeedMaxStepPct, true
	default:
		return 0, true
	}
}

func resolveDeadband(capability Capability, override *float64) (float64, bool) {
	if override != nil {
		if !isFiniteGuardrailFloat(*override) || *override <= 0 {
			return 0, false
		}
		return *override, true
	}
	switch capability {
	case CapabilitySetChilledWaterTemperatureSetpoint:
		return DefaultChilledWaterDeadbandC, true
	case CapabilitySetFrequency:
		return DefaultFrequencyDeadbandHz, true
	case CapabilitySetFanSpeed:
		return DefaultFanSpeedDeadbandPct, true
	default:
		return 0, true
	}
}

func inspectPointFreshness(points []GuardrailPointSample, now time.Time, maxAge time.Duration) (string, bool) {
	futureLimit := now.Add(5 * time.Second)
	for _, p := range points {
		label := strings.TrimSpace(p.Key)
		if label == "" {
			label = "<unnamed>"
		}
		if !p.Present {
			return fmt.Sprintf("point %q is missing", label), true
		}
		if p.SampledAt.IsZero() {
			return fmt.Sprintf("point %q has zero sampledAt timestamp", label), true
		}
		if p.SampledAt.After(futureLimit) {
			return fmt.Sprintf("point %q sampledAt timestamp is in the future", label), true
		}
		if now.Sub(p.SampledAt) > maxAge {
			return fmt.Sprintf("point %q is stale (age %s exceeds max %s)", label, now.Sub(p.SampledAt).Round(time.Second), maxAge), true
		}
		if !strings.EqualFold(strings.TrimSpace(p.Quality), "GOOD") {
			return fmt.Sprintf("point %q quality %q is not GOOD", label, p.Quality), true
		}
		if p.Value != nil && !isFiniteGuardrailFloat(*p.Value) {
			return fmt.Sprintf("point %q numeric value is non-finite", label), true
		}
	}
	return "", false
}

func inspectFaultAndLocalState(in GuardrailInput) (string, bool) {
	if in.DeviceFaultActive {
		return "device fault is active", true
	}
	if in.CabinetLocalManual {
		return "cabinet is in local manual mode", true
	}
	for _, p := range in.Points {
		if IsFaultOrAlarmPointKey(p.Key) && DiagnosticValueIndicatesActiveFault(p.BoolValue, p.Value, p.StringValue) {
			return fmt.Sprintf("telemetry point %q reports active fault or alarm", p.Key), true
		}
		if IsLocalManualPointKey(p.Key) && DiagnosticPointIndicatesLocalManual(p.Key, p.BoolValue, p.Value, p.StringValue) {
			return fmt.Sprintf("telemetry point %q reports local manual mode", p.Key), true
		}
	}
	return "", false
}

func passStage(stage string, val float64) GuardrailStageResult {
	return GuardrailStageResult{
		Stage:  stage,
		Action: string(GuardrailVerdictPass),
		Before: val,
		After:  val,
	}
}

func vetoGuardrail(trace []GuardrailStageResult, raw float64, stage, reasonCode, detail string, currentVal float64) GuardrailResult {
	trace = append(trace, GuardrailStageResult{
		Stage:  stage,
		Action: string(GuardrailVerdictVetoed),
		Before: currentVal,
		After:  currentVal,
		Reason: reasonCode,
	})
	return GuardrailResult{
		Verdict:       GuardrailVerdictVetoed,
		Allowed:       false,
		ReasonCode:    reasonCode,
		Detail:        detail,
		RawValue:      raw,
		AdjustedValue: currentVal,
		Trace:         trace,
	}
}

func isFiniteGuardrailFloat(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

func roundGuardrailDecimal(v float64) float64 {
	return math.Round(v*1e9) / 1e9
}
