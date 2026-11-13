package commandformal

import "testing"

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
