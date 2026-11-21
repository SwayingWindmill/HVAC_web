package commandmodel

import (
	"testing"
	"time"
)

func safeNumObs(now time.Time, v float64) *LivePointObservation {
	return &LivePointObservation{
		EvaluationAvailability: "AVAILABLE",
		Presence:               "ONLINE",
		Readiness:              "CURRENT",
		Freshness:              "FRESH",
		Quality:                "GOOD",
		BusinessRevision:       17,
		CurrentValue:           &v,
		ReportedValue:          NumberScalar(v),
		ObservedAt:             now,
	}
}

func safeStateObs(now time.Time, state string) *LivePointObservation {
	return &LivePointObservation{
		EvaluationAvailability: "AVAILABLE",
		Presence:               "ONLINE",
		Readiness:              "CURRENT",
		Freshness:              "FRESH",
		Quality:                "GOOD",
		BusinessRevision:       19,
		ReportedValue:          TextScalar(state),
		ObservedAt:             now,
	}
}

func TestEvaluateControlGateThreeTierLocksSharedAcrossPhases(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	maint := &MaintenanceWindowSnapshot{ID: "mw-1", Title: "Weekly", StartDate: "2026-11-16", EndDate: "2026-11-16", Reason: "cleaning"}
	assetLock := ControlLockSnapshot{AssetID: "asset-1", LockMode: AssetControlLockMaintenance, BoundDeviceIDs: []string{"dev-1"}}
	ctrlLock := ControlLockSnapshot{AssetID: "other-asset", LockMode: AssetControlLockManual, BoundDeviceIDs: []string{"dev-1"}}

	tests := []struct {
		name      string
		in        GateInput
		wantCode  string
		wantStep  bool
		wantAllow bool
	}{
		{
			name: "tier1 START blocked by MANUAL_OVERRIDE at submit",
			in: GateInput{
				Phase: GatePhaseSubmit, AssetID: "asset-1", DeviceID: "dev-1",
				Capability: CapabilityStart, SiteMode: SiteControlModeManualOverride,
				TargetState: safeStateObs(now, "STOPPED"), StepUpVerified: true,
			},
			wantCode: "COMMAND_CONTROL_LOCKED",
		},
		{
			name: "tier1 SET_FREQUENCY blocked by active maintenance window at pre-dispatch",
			in: GateInput{
				Phase: GatePhasePreDispatch, CommandID: "cmd-1", DeviceID: "dev-1",
				Capability: CapabilitySetFrequency, ParameterKey: ParameterFrequencyHz,
				Parameters:              CommandParameters{ParameterFrequencyHz: 40},
				SiteMode:                SiteControlModeRemoteAuto,
				ActiveMaintenanceWindow: maint,
				TargetState:             safeStateObs(now, "RUNNING"),
			},
			wantCode: "DISPATCH_SAFETY_MAINTENANCE_WINDOW",
		},
		{
			name: "tier1 locked asset blocks BEFORE TOTP step-up is requested",
			in: GateInput{
				Phase: GatePhaseSubmit, AssetID: "asset-1", DeviceID: "dev-1",
				Capability: CapabilityStart, SiteMode: SiteControlModeRemoteAuto,
				AssetLocks:  []ControlLockSnapshot{assetLock},
				TargetState: safeStateObs(now, "STOPPED"), StepUpVerified: false,
			},
			wantCode: "COMMAND_CONTROL_LOCKED",
			wantStep: false,
		},
		{
			name: "tier2a STOP allowed in MANUAL_OVERRIDE and active maintenance window",
			in: GateInput{
				Phase: GatePhaseSubmit, AssetID: "asset-1", DeviceID: "dev-1",
				Capability: CapabilityStop, SiteMode: SiteControlModeManualOverride,
				ActiveMaintenanceWindow: maint,
				TargetState:             safeStateObs(now, "RUNNING"), StepUpVerified: true,
			},
			wantAllow: true,
		},
		{
			name: "tier2a STOP still blocked by asset lock at pre-dispatch",
			in: GateInput{
				Phase: GatePhasePreDispatch, CommandID: "cmd-2", AssetID: "asset-1", DeviceID: "dev-1",
				Capability: CapabilityStop, SiteMode: SiteControlModeRemoteAuto,
				AssetLocks:  []ControlLockSnapshot{assetLock},
				TargetState: safeStateObs(now, "RUNNING"),
			},
			wantCode: "DISPATCH_SAFETY_CONTROL_LOCKED",
		},
		{
			name: "tier2b RESET_FAULT allowed in maintenance window under REMOTE_AUTO",
			in: GateInput{
				Phase: GatePhaseSubmit, AssetID: "asset-1", DeviceID: "dev-1",
				Capability: CapabilityResetFault, SiteMode: SiteControlModeRemoteAuto,
				ActiveMaintenanceWindow: maint, TargetState: safeStateObs(now, "FAULT"),
			},
			wantAllow: true,
		},
		{
			name: "tier2b RESET_FAULT blocked in MANUAL_OVERRIDE",
			in: GateInput{
				Phase: GatePhaseSubmit, AssetID: "asset-1", DeviceID: "dev-1",
				Capability: CapabilityResetFault, SiteMode: SiteControlModeManualOverride,
				TargetState: safeStateObs(now, "FAULT"),
			},
			wantCode: "COMMAND_CONTROL_LOCKED",
		},
		{
			name: "tier3 EMERGENCY_DO_RELEASE rejected on regular /commands",
			in: GateInput{
				Phase: GatePhaseSubmit, AssetID: "asset-1", DeviceID: "dev-1",
				Capability: CapabilityStop, ActuationMode: "EMERGENCY_DO_RELEASE",
				AllowEmergencyDoRelease: false, TargetState: safeStateObs(now, "RUNNING"),
			},
			wantCode: "COMMAND_REQUEST_INVALID",
		},
		{
			name: "tier3 EMERGENCY_DO_RELEASE on shutdown bypasses site mode, maint window, shared controller lock and degraded telemetry",
			in: GateInput{
				Phase: GatePhaseSubmit, AssetID: "asset-1", DeviceID: "dev-1",
				Capability: CapabilityStop, ActuationMode: "EMERGENCY_DO_RELEASE",
				AllowEmergencyDoRelease: true, SiteMode: SiteControlModeSafeShutdownExit,
				AssetLocks: []ControlLockSnapshot{ctrlLock}, ActiveMaintenanceWindow: maint,
				PreconditionMode: PreconditionEmergencyBypass, BypassReason: "telemetry offline during emergency shutdown",
				TargetState: nil, StepUpVerified: true,
			},
			wantAllow: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			decision, rejection := EvaluateControlGate(tc.in)
			if tc.wantAllow {
				if rejection != nil || decision.StepUpRequired {
					t.Fatalf("got rejection=%+v decision=%+v, want allow", rejection, decision)
				}
				return
			}
			if rejection == nil || rejection.Code != tc.wantCode {
				t.Fatalf("rejection=%+v, want code %q", rejection, tc.wantCode)
			}
			if decision.StepUpRequired != tc.wantStep {
				t.Fatalf("StepUpRequired=%v, want %v", decision.StepUpRequired, tc.wantStep)
			}
		})
	}
}

func TestEvaluateControlGatePreconditionsInterlocksAndStepUpOrdering(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)

	t.Run("emergency bypass forbidden on START", func(t *testing.T) {
		_, rejection := EvaluateControlGate(GateInput{
			Phase: GatePhaseSubmit, Capability: CapabilityStart,
			PreconditionMode: PreconditionEmergencyBypass, BypassReason: "offline",
		})
		if rejection == nil || rejection.Code != "COMMAND_REQUEST_INVALID" {
			t.Fatalf("got rejection=%+v, want COMMAND_REQUEST_INVALID", rejection)
		}
	})

	t.Run("blocked peer interlock returns 409 before 428 STEP_UP_REQUIRED", func(t *testing.T) {
		checks := []InterlockCheck{{DeviceID: "peer-1", Key: "run_state", Expected: "RUNNING", Reason: "pump must run"}}
		decision, rejection := EvaluateControlGate(GateInput{
			Phase: GatePhaseSubmit, Capability: CapabilityStart, SiteMode: SiteControlModeRemoteAuto,
			TargetState:    safeStateObs(now, "STOPPED"),
			Interlocks:     checks,
			PeerStates:     map[string]*LivePointObservation{PeerStateKey("peer-1", "run_state"): safeStateObs(now, "STOPPED")},
			StepUpVerified: false,
		})
		if rejection == nil || rejection.Code != "COMMAND_INTERLOCK_BLOCKED" || decision.StepUpRequired {
			t.Fatalf("got decision=%+v rejection=%+v, want COMMAND_INTERLOCK_BLOCKED without stepUp", decision, rejection)
		}
	})

	t.Run("high delta setpoint demands step-up after passing hard checks", func(t *testing.T) {
		decision, rejection := EvaluateControlGate(GateInput{
			Phase: GatePhaseSubmit, Capability: CapabilitySetTemperatureSetpoint,
			ParameterKey: ParameterSetpointC,
			Parameters:   CommandParameters{ParameterSetpointC: 24.5},
			SiteMode:     SiteControlModeRemoteAuto,
			TargetState:  safeNumObs(now, 22.0), // delta 2.5 -> RiskHigh
		})
		if rejection != nil || !decision.StepUpRequired || decision.Risk != RiskHigh {
			t.Fatalf("got decision=%+v rejection=%+v, want StepUpRequired RiskHigh", decision, rejection)
		}
	})

	t.Run("pre-dispatch pulse stop without interlocks bypasses stale run_state", func(t *testing.T) {
		stale := safeStateObs(now, "RUNNING")
		stale.Freshness = "STALE"
		decision, rejection := EvaluateControlGate(GateInput{
			Phase: GatePhasePreDispatch, CommandID: "cmd-pulse", Capability: CapabilityStop,
			ActuationMode: "PULSE", SiteMode: SiteControlModeSafeShutdownExit,
			TargetState: stale,
		})
		if rejection != nil || decision.EvidenceID != "shutdown-pulse-bypass:cmd-pulse" {
			t.Fatalf("got decision=%+v rejection=%+v", decision, rejection)
		}
	})

	t.Run("pre-dispatch honors explicit EMERGENCY_BYPASS without PULSE when interlocks empty", func(t *testing.T) {
		decision, rejection := EvaluateControlGate(GateInput{
			Phase: GatePhasePreDispatch, CommandID: "cmd-emerg-bypass", Capability: CapabilityStopUnitSequence,
			PreconditionMode: PreconditionEmergencyBypass, BypassReason: "chiller telemetry offline",
			SiteMode: SiteControlModeSafeShutdownExit, TargetState: nil,
		})
		if rejection != nil || decision.PreconditionMode != PreconditionEmergencyBypass || decision.EvidenceID != "emergency-precondition-bypass:cmd-emerg-bypass" {
			t.Fatalf("got decision=%+v rejection=%+v", decision, rejection)
		}
	})

	t.Run("pre-dispatch EMERGENCY_BYPASS still rejects when interlock peer state unavailable", func(t *testing.T) {
		_, rejection := EvaluateControlGate(GateInput{
			Phase: GatePhasePreDispatch, CommandID: "cmd-emerg-interlock", Capability: CapabilityStop,
			PreconditionMode: PreconditionEmergencyBypass, BypassReason: "pump telemetry offline",
			Interlocks: []InterlockCheck{{DeviceID: "peer-1", Key: "run_state", Expected: "STOPPED", Reason: "chiller must stop first"}},
		})
		if rejection == nil || rejection.Code != "DISPATCH_SAFETY_STATE_UNAVAILABLE" {
			t.Fatalf("got rejection=%+v, want DISPATCH_SAFETY_STATE_UNAVAILABLE", rejection)
		}
	})
}

func TestEvaluateControlGatePreDispatchStepDeltaRisk(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		in       GateInput
		wantCode string
	}{
		{
			name: "blocks AI optimization setpoint delta exceeding maximumDelta",
			in: GateInput{
				Phase:         GatePhasePreDispatch,
				CommandID:     "cmd-ai-over-delta",
				DeviceID:      "dev-1",
				Capability:    CapabilitySetChilledWaterTemperatureSetpoint,
				ParameterKey:  ParameterSetpointC,
				Parameters:    CommandParameters{ParameterSetpointC: 5.0},
				SiteMode:      SiteControlModeRemoteAuto,
				CommandSource: SourceOptimization,
				PriorityTier:  PriorityAIOptimization,
				TargetState:   safeNumObs(now, 10.0), // delta 5 > max 3
			},
			wantCode: "DISPATCH_SAFETY_PRE_DISPATCH_DELTA_EXCEEDED",
		},
		{
			name: "blocks schedule frequency delta exceeding maximumDelta",
			in: GateInput{
				Phase:         GatePhasePreDispatch,
				CommandID:     "cmd-sched-over-delta",
				DeviceID:      "dev-1",
				Capability:    CapabilitySetFrequency,
				ParameterKey:  ParameterFrequencyHz,
				Parameters:    CommandParameters{ParameterFrequencyHz: 30.0},
				SiteMode:      SiteControlModeRemoteAuto,
				CommandSource: SourceSchedule,
				PriorityTier:  PrioritySchedule,
				TargetState:   safeNumObs(now, 45.0), // delta 15 > max 10
			},
			wantCode: "DISPATCH_SAFETY_PRE_DISPATCH_DELTA_EXCEEDED",
		},
		{
			name: "allows AI optimization delta within maximumDelta",
			in: GateInput{
				Phase:         GatePhasePreDispatch,
				CommandID:     "cmd-ai-safe-delta",
				DeviceID:      "dev-1",
				Capability:    CapabilitySetChilledWaterTemperatureSetpoint,
				ParameterKey:  ParameterSetpointC,
				Parameters:    CommandParameters{ParameterSetpointC: 8.0},
				SiteMode:      SiteControlModeRemoteAuto,
				CommandSource: SourceOptimization,
				PriorityTier:  PriorityAIOptimization,
				TargetState:   safeNumObs(now, 10.0), // delta 2 <= max 3
			},
		},
		{
			name: "allows operator manual command at pre-dispatch even if live delta moved past maximumDelta",
			in: GateInput{
				Phase:         GatePhasePreDispatch,
				CommandID:     "cmd-op-manual",
				DeviceID:      "dev-1",
				Capability:    CapabilitySetChilledWaterTemperatureSetpoint,
				ParameterKey:  ParameterSetpointC,
				Parameters:    CommandParameters{ParameterSetpointC: 5.0},
				SiteMode:      SiteControlModeRemoteAuto,
				CommandSource: SourceOperator,
				PriorityTier:  PriorityManualOverride,
				TargetState:   safeNumObs(now, 10.0),
			},
		},
		{
			name: "allows emergency bypass auto-rollback without pre-dispatch delta rejection",
			in: GateInput{
				Phase:            GatePhasePreDispatch,
				CommandID:        "cmd-rollback-bypass",
				DeviceID:         "dev-1",
				Capability:       CapabilitySetChilledWaterTemperatureSetpoint,
				ParameterKey:     ParameterSetpointC,
				Parameters:       CommandParameters{ParameterSetpointC: 5.0},
				SiteMode:         SiteControlModeRemoteAuto,
				IdempotencyKey:   "auto-rollback:rec-1",
				CommandSource:    SourceProtectionRule,
				PriorityTier:     PriorityProtectionRule,
				PreconditionMode: PreconditionEmergencyBypass,
				BypassReason:     "revert failed AI change under degraded telemetry",
				TargetState:      safeNumObs(now, 10.0),
			},
		},
		{
			name: "allows healthy-telemetry auto-rollback setpoint delta above maximumDelta",
			in: GateInput{
				Phase:            GatePhasePreDispatch,
				CommandID:        "cmd-rollback-healthy",
				DeviceID:         "dev-1",
				Capability:       CapabilitySetChilledWaterTemperatureSetpoint,
				ParameterKey:     ParameterSetpointC,
				Parameters:       CommandParameters{ParameterSetpointC: 5.0},
				SiteMode:         SiteControlModeRemoteAuto,
				IdempotencyKey:   "auto-rollback:rec-healthy",
				CommandSource:    SourceProtectionRule,
				PriorityTier:     PriorityProtectionRule,
				PreconditionMode: PreconditionRequireCurrentState,
				TargetState:      safeNumObs(now, 10.0),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, rejection := EvaluateControlGate(tc.in)
			if tc.wantCode == "" {
				if rejection != nil {
					t.Fatalf("expected allow, got rejection=%+v", rejection)
				}
				return
			}
			if rejection == nil || rejection.Code != tc.wantCode {
				t.Fatalf("got rejection=%+v, want code %s", rejection, tc.wantCode)
			}
			if !TerminalPreSendRejection(rejection.Code) {
				t.Fatalf("expected %s to be a terminal pre-send rejection", rejection.Code)
			}
		})
	}
}

func TestAutoRollbackEmergencyPreconditionBypassScope(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)

	t.Run("AllowsEmergencyPreconditionBypass only permits scoped auto-rollback setpoint and auxiliary loops", func(t *testing.T) {
		for _, cap := range []Capability{
			CapabilitySetChilledWaterTemperatureSetpoint,
			CapabilitySetFrequency,
			CapabilitySetFanSpeed,
		} {
			if !AllowsEmergencyPreconditionBypass(
				cap,
				PriorityProtectionRule,
				SourceProtectionRule,
				"auto-rollback:action-1",
				"revert loop",
			) {
				t.Fatalf("expected scoped auto-rollback bypass to be allowed for %s", cap)
			}
		}
		cases := []struct {
			name string
			cap  Capability
			tier PriorityTier
			src  CommandSource
			idem string
			rsn  string
		}{
			{"wrong capability", CapabilitySetLoadLimit, PriorityProtectionRule, SourceProtectionRule, "auto-rollback:1", "reason"},
			{"wrong tier", CapabilitySetChilledWaterTemperatureSetpoint, PriorityAIOptimization, SourceProtectionRule, "auto-rollback:1", "reason"},
			{"wrong source", CapabilitySetChilledWaterTemperatureSetpoint, PriorityProtectionRule, SourceOptimization, "auto-rollback:1", "reason"},
			{"missing prefix", CapabilitySetChilledWaterTemperatureSetpoint, PriorityProtectionRule, SourceProtectionRule, "ai-action:1", "reason"},
			{"blank reason", CapabilitySetChilledWaterTemperatureSetpoint, PriorityProtectionRule, SourceProtectionRule, "auto-rollback:1", "   "},
		}
		for _, tc := range cases {
			if AllowsEmergencyPreconditionBypass(tc.cap, tc.tier, tc.src, tc.idem, tc.rsn) {
				t.Fatalf("%s: expected bypass denied", tc.name)
			}
		}
	})

	t.Run("submit and pre-dispatch accept auto-rollback setpoint bypass even with nil/stale state", func(t *testing.T) {
		submitDecision, submitRej := EvaluateControlGate(GateInput{
			Phase:            GatePhaseSubmit,
			CommandID:        "cmd-rb-1",
			AssetID:          "asset-1",
			DeviceID:         "dev-1",
			Capability:       CapabilitySetChilledWaterTemperatureSetpoint,
			ParameterKey:     ParameterSetpointC,
			Parameters:       CommandParameters{ParameterSetpointC: 7.5},
			SiteMode:         SiteControlModeRemoteAuto,
			IdempotencyKey:   "auto-rollback:rec-42",
			PriorityTier:     PriorityProtectionRule,
			CommandSource:    SourceProtectionRule,
			PreconditionMode: PreconditionEmergencyBypass,
			BypassReason:     "auto-rollback degraded telemetry",
			TargetState:      nil,
			StepUpVerified:   true,
		})
		if submitRej != nil || submitDecision.PreconditionMode != PreconditionEmergencyBypass {
			t.Fatalf("expected submit allow, got decision=%+v rejection=%+v", submitDecision, submitRej)
		}

		stale := safeNumObs(now, 9.0)
		stale.Freshness = "STALE"
		dispatchDecision, dispatchRej := EvaluateControlGate(GateInput{
			Phase:            GatePhasePreDispatch,
			CommandID:        "cmd-rb-1",
			AssetID:          "asset-1",
			DeviceID:         "dev-1",
			Capability:       CapabilitySetChilledWaterTemperatureSetpoint,
			ParameterKey:     ParameterSetpointC,
			Parameters:       CommandParameters{ParameterSetpointC: 7.5},
			SiteMode:         SiteControlModeRemoteAuto,
			IdempotencyKey:   "auto-rollback:rec-42",
			PriorityTier:     PriorityProtectionRule,
			CommandSource:    SourceProtectionRule,
			PreconditionMode: PreconditionEmergencyBypass,
			BypassReason:     "auto-rollback degraded telemetry",
			TargetState:      stale,
		})
		if dispatchRej != nil || dispatchDecision.EvidenceID != "emergency-precondition-bypass:cmd-rb-1" {
			t.Fatalf("expected pre-dispatch bypass allow, got decision=%+v rejection=%+v", dispatchDecision, dispatchRej)
		}
	})

	t.Run("submit and pre-dispatch reject non-rollback setpoint EMERGENCY_BYPASS", func(t *testing.T) {
		_, submitRej := EvaluateControlGate(GateInput{
			Phase:            GatePhaseSubmit,
			CommandID:        "cmd-ai-bypass",
			DeviceID:         "dev-1",
			Capability:       CapabilitySetChilledWaterTemperatureSetpoint,
			ParameterKey:     ParameterSetpointC,
			Parameters:       CommandParameters{ParameterSetpointC: 7.5},
			SiteMode:         SiteControlModeRemoteAuto,
			IdempotencyKey:   "ai-opt-1",
			PriorityTier:     PriorityAIOptimization,
			CommandSource:    SourceOptimization,
			PreconditionMode: PreconditionEmergencyBypass,
			BypassReason:     "bypass attempt",
		})
		if submitRej == nil || submitRej.Code != "COMMAND_REQUEST_INVALID" {
			t.Fatalf("expected COMMAND_REQUEST_INVALID on AI bypass, got %+v", submitRej)
		}

		stale := safeNumObs(now, 9.0)
		stale.Freshness = "STALE"
		_, dispatchRej := EvaluateControlGate(GateInput{
			Phase:            GatePhasePreDispatch,
			CommandID:        "cmd-ai-bypass",
			DeviceID:         "dev-1",
			Capability:       CapabilitySetChilledWaterTemperatureSetpoint,
			ParameterKey:     ParameterSetpointC,
			Parameters:       CommandParameters{ParameterSetpointC: 7.5},
			SiteMode:         SiteControlModeRemoteAuto,
			IdempotencyKey:   "ai-opt-1",
			PriorityTier:     PriorityAIOptimization,
			CommandSource:    SourceOptimization,
			PreconditionMode: PreconditionEmergencyBypass,
			BypassReason:     "bypass attempt",
			TargetState:      stale,
		})
		if dispatchRej == nil || dispatchRej.Code != "DISPATCH_SAFETY_STATE_NOT_FRESH" {
			t.Fatalf("expected DISPATCH_SAFETY_STATE_NOT_FRESH on AI stale state, got %+v", dispatchRej)
		}
	})
}
