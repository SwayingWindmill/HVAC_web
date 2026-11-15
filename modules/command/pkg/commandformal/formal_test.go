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

	for _, cap := range []commandmodel.Capability{commandmodel.CapabilityStart, commandmodel.CapabilityStop} {
		dec, err := commandservice.DecideVerificationResolution(
			makeSyntheticIntentForCapability(1, cap, commandmodel.IntentDispatching, true, now),
			commandmodel.VerificationEnvelope{VerificationPointKey: setpointVerificationKey, AcknowledgedAt: now, EdgeExecution: pulseEdge},
			commandmodel.VerificationResult{Outcome: commandmodel.VerificationTimedOut, EvidenceID: "ev-pulse-timeout"},
			deadline,
			deadline.Add(time.Second),
		)
		if err != nil {
			t.Fatal(err)
		}
		if dec.IntentFinal != commandmodel.IntentFailed || dec.Reason != commandmodel.ReasonPulseDeliveredPowerUnconfirmed || dec.FreezeControlGroup {
			t.Fatalf("%s pulse timeout = %+v, want FAILED without freeze", cap, dec)
		}

		// Level coil or missing edge MUST freeze OUTCOME_UNKNOWN.
		levelDec, err := commandservice.DecideVerificationResolution(
			makeSyntheticIntentForCapability(1, cap, commandmodel.IntentDispatching, true, now),
			commandmodel.VerificationEnvelope{VerificationPointKey: setpointVerificationKey, AcknowledgedAt: now, EdgeExecution: levelEdge},
			commandmodel.VerificationResult{Outcome: commandmodel.VerificationTimedOut, EvidenceID: "ev-level-timeout"},
			deadline,
			deadline.Add(time.Second),
		)
		if err != nil {
			t.Fatal(err)
		}
		if levelDec.IntentFinal != commandmodel.IntentOutcomeUnknown || !levelDec.FreezeControlGroup {
			t.Fatalf("%s level timeout = %+v, want OUTCOME_UNKNOWN with freeze", cap, levelDec)
		}
	}

	// Unit-sequence MUST freeze OUTCOME_UNKNOWN even with pulse dual readback.
	seqDec, err := commandservice.DecideVerificationResolution(
		makeSyntheticIntentForCapability(1, commandmodel.CapabilityStartUnitSequence, commandmodel.IntentDispatching, true, now),
		commandmodel.VerificationEnvelope{VerificationPointKey: setpointVerificationKey, AcknowledgedAt: now, EdgeExecution: pulseEdge},
		commandmodel.VerificationResult{Outcome: commandmodel.VerificationTimedOut, EvidenceID: "ev-seq-timeout"},
		deadline,
		deadline.Add(time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	if seqDec.IntentFinal != commandmodel.IntentOutcomeUnknown || !seqDec.FreezeControlGroup {
		t.Fatalf("unit-sequence timeout = %+v, want OUTCOME_UNKNOWN with freeze", seqDec)
	}
}
