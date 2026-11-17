package commandformal

import (
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/modules/command/pkg/commandservice"
)

func TestSingleCommandProtocolInvariants(t *testing.T) {
	report, err := CheckSingleCommandProtocol(FaultHooks{})
	if err != nil {
		t.Fatalf("CheckSingleCommandProtocol errored: %v", err)
	}
	if !report.Passed {
		t.Fatalf("CheckSingleCommandProtocol failed: %s", report.Counterexample.Error())
	}
	if report.VisitedStates == 0 || report.Transitions == 0 {
		t.Fatalf("expected non-empty BFS exploration: states=%d transitions=%d", report.VisitedStates, report.Transitions)
	}
}

func TestMultiCommandDeviceExclusivityInvariants(t *testing.T) {
	report, err := CheckMultiCommandDeviceExclusivity(FaultHooks{})
	if err != nil {
		t.Fatalf("CheckMultiCommandDeviceExclusivity errored: %v", err)
	}
	if !report.Passed {
		t.Fatalf("CheckMultiCommandDeviceExclusivity failed: %s", report.Counterexample.Error())
	}
	if report.VisitedStates == 0 || report.Transitions == 0 {
		t.Fatalf("expected non-empty BFS exploration: states=%d transitions=%d", report.VisitedStates, report.Transitions)
	}
}

func TestReplyRecoveryQueueDrainInvariants(t *testing.T) {
	report, err := CheckReplyRecoveryQueueDrain(FaultHooks{})
	if err != nil {
		t.Fatalf("CheckReplyRecoveryQueueDrain errored: %v", err)
	}
	if !report.Passed {
		t.Fatalf("CheckReplyRecoveryQueueDrain failed: %s", report.Counterexample.Error())
	}
	if report.VisitedStates == 0 || report.Transitions == 0 {
		t.Fatalf("expected non-empty recovery verification: states=%d transitions=%d", report.VisitedStates, report.Transitions)
	}
}

func TestPriorityArbitrationAndLocksInvariants(t *testing.T) {
	report, err := CheckPriorityArbitrationAndLocks(FaultHooks{})
	if err != nil {
		t.Fatalf("CheckPriorityArbitrationAndLocks errored: %v", err)
	}
	if !report.Passed {
		t.Fatalf("CheckPriorityArbitrationAndLocks failed: %s", report.Counterexample.Error())
	}
	if report.VisitedStates == 0 || report.Transitions == 0 {
		t.Fatalf("expected non-empty priority arbitration exploration: states=%d transitions=%d", report.VisitedStates, report.Transitions)
	}
}

func TestFormalPropertiesCanFailOnBrokenTransitions(t *testing.T) {
	cases := []struct {
		name     string
		wantProp PropertyID
		hooks    FaultHooks
	}{
		{
			name:     "P1 expired authorization grant must fail",
			wantProp: PropertyP1,
			hooks:    FaultHooks{BypassExpiredGrantOnClaim: true},
		},
		{
			name:     "P2 post-arm MQTT republish must fail",
			wantProp: PropertyP2,
			hooks:    FaultHooks{AllowPostArmRepublish: true},
		},
		{
			name:     "P3 OUTCOME_UNKNOWN without freeze must fail",
			wantProp: PropertyP3,
			hooks:    FaultHooks{UnfreezeWithoutReconcile: true},
		},
		{
			name:     "P4 ACK without EdgeExecution evidence must fail",
			wantProp: PropertyP4,
			hooks:    FaultHooks{AllowAckWithoutEdgeExecution: true},
		},
		{
			name:     "P5 post-deadline verification success must fail",
			wantProp: PropertyP5,
			hooks:    FaultHooks{IgnoreVerificationDeadline: true},
		},
		{
			name:     "P6 stale REPLIED starvation must fail",
			wantProp: PropertyP6,
			hooks:    FaultHooks{DropStaleReplyRecoveryAsError: true},
		},
		{
			name:     "P1 cancelled intent dispatch must fail",
			wantProp: PropertyP1,
			hooks:    FaultHooks{AllowDispatchCancelledIntent: true},
		},
		{
			name:     "P1 transient safety evaluation failure treated terminal must fail",
			wantProp: PropertyP1,
			hooks:    FaultHooks{TreatSafetyEvalFailureAsTerminal: true},
		},
		{
			name:     "P3 aborting non-sequence acknowledged command must fail",
			wantProp: PropertyP3,
			hooks:    FaultHooks{AllowAbortNonSequenceCommand: true},
		},
		{
			name:     "P6 single-batch recovery stopping at LIMIT 50 must fail",
			wantProp: PropertyP6,
			hooks:    FaultHooks{FailDrainedRecoveryPastBatchLimit: true},
		},
		{
			name:     "P1 submit under higher priority lock must fail",
			wantProp: PropertyP1,
			hooks:    FaultHooks{AllowSubmitUnderHigherPriorityLock: true},
		},
		{
			name:     "P1 claim bypassing priority lock must fail",
			wantProp: PropertyP1,
			hooks:    FaultHooks{BypassPriorityLockOnClaim: true},
		},
		{
			name:     "P3 SAFETY_INTERLOCK leaving lower-priority queued intent unpreempted must fail",
			wantProp: PropertyP3,
			hooks:    FaultHooks{SkipSafetyInterlockPreemption: true},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			report, err := RunAll(tc.hooks)
			if err != nil {
				t.Fatalf("mutation run errored: %v", err)
			}
			if report.Passed || report.Counterexample == nil {
				t.Fatalf("expected %s violation under fault injection, but verifier passed", tc.wantProp)
			}
			if report.Counterexample.Property != tc.wantProp {
				t.Fatalf("property=%s want=%s (%s)", report.Counterexample.Property, tc.wantProp, report.Counterexample.Error())
			}
		})
	}
}

func TestPulseDualReadbackVsNonPulseFormalBranches(t *testing.T) {
	now := baseModelTime
	deadline := now.Add(10 * time.Minute)
	pulseEdge := validPulseEdgeExecution("u14", 500)
	levelEdge := validLevelEdgeExecution("u11", 1)
	emergencyDoEdge := validEmergencyDoReleaseEdgeExecution()
	unitSeqEdge := validUnitSequenceAcceptedEdgeExecution("chiller-1")

	resolveTimedOut := func(cap commandmodel.Capability, edge *commandmodel.EdgeExecutionEvidence) commandservice.VerificationResolutionDecision {
		t.Helper()
		dec, err := commandservice.DecideVerificationResolutionWithEdge(
			makeSyntheticIntentForCapability(1, cap, commandmodel.IntentDispatching, true, now),
			commandmodel.VerificationEnvelope{VerificationPointKey: setpointVerificationKey, AcknowledgedAt: now, EdgeExecution: edge},
			edge,
			commandmodel.VerificationResult{Outcome: commandmodel.VerificationTimedOut, EvidenceID: "ev-timeout"},
			deadline,
			deadline.Add(time.Second),
		)
		if err != nil {
			t.Fatalf("%s DecideVerificationResolutionWithEdge failed: %v", cap, err)
		}
		return dec
	}

	assertUnconfirmedFailed := func(cap commandmodel.Capability, label string, edge *commandmodel.EdgeExecutionEvidence) {
		t.Helper()
		dec := resolveTimedOut(cap, edge)
		if dec.IntentFinal != commandmodel.IntentFailed || dec.Reason != commandmodel.ReasonPulseDeliveredPowerUnconfirmed || dec.FreezeControlGroup {
			t.Fatalf("%s (%s) timeout = %+v, want FAILED without freeze", cap, label, dec)
		}
	}

	assertFrozenUnknown := func(cap commandmodel.Capability, label string, edge *commandmodel.EdgeExecutionEvidence) {
		t.Helper()
		dec := resolveTimedOut(cap, edge)
		if dec.IntentFinal != commandmodel.IntentOutcomeUnknown || !dec.FreezeControlGroup {
			t.Fatalf("%s (%s) timeout = %+v, want OUTCOME_UNKNOWN with freeze", cap, label, dec)
		}
	}

	for _, cap := range []commandmodel.Capability{commandmodel.CapabilityStart, commandmodel.CapabilityStop} {
		assertUnconfirmedFailed(cap, "pulse dual", pulseEdge)
		assertFrozenUnknown(cap, "level coil", levelEdge)
		assertFrozenUnknown(cap, "missing edge", nil)
		assertFrozenUnknown(cap, "unit sequence acceptance", unitSeqEdge)
	}

	// Emergency DO release ("all-do:0") only exempts STOP from freezing OUTCOME_UNKNOWN.
	assertUnconfirmedFailed(commandmodel.CapabilityStop, "emergency DO release", emergencyDoEdge)
	assertFrozenUnknown(commandmodel.CapabilityStart, "emergency DO release", emergencyDoEdge)

	// Unit-sequence acceptance exempts START_UNIT_SEQUENCE / STOP_UNIT_SEQUENCE,
	// whereas pulse/level/emergency-DO/missing-edge on unit-sequence MUST freeze OUTCOME_UNKNOWN.
	for _, cap := range []commandmodel.Capability{commandmodel.CapabilityStartUnitSequence, commandmodel.CapabilityStopUnitSequence} {
		assertUnconfirmedFailed(cap, "unit sequence acceptance", unitSeqEdge)
		assertFrozenUnknown(cap, "pulse dual", pulseEdge)
		assertFrozenUnknown(cap, "level coil", levelEdge)
		assertFrozenUnknown(cap, "emergency DO release", emergencyDoEdge)
		assertFrozenUnknown(cap, "missing edge", nil)
	}

	// Setpoint and fault-reset capabilities MUST always freeze OUTCOME_UNKNOWN even with special edge texts.
	for _, cap := range []commandmodel.Capability{commandmodel.CapabilitySetTemperatureSetpoint, commandmodel.CapabilityResetFault} {
		assertFrozenUnknown(cap, "pulse dual", pulseEdge)
		assertFrozenUnknown(cap, "emergency DO release", emergencyDoEdge)
		assertFrozenUnknown(cap, "unit sequence acceptance", unitSeqEdge)
	}
}
