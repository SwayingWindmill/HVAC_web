package commandmodel

import (
	"encoding/json"
	"math"
	"testing"
	"time"
)

func float64Ptr(v float64) *float64 { return &v }
func boolPtr(v bool) *bool          { return &v }
func timePtr(t time.Time) *time.Time {
	return &t
}

func TestEvaluateGuardrailsStagesAndVerdicts(t *testing.T) {
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	freshSample := GuardrailPointSample{
		Key:       "chw_supply_temp_c",
		Value:     float64Ptr(7.5),
		Quality:   "GOOD",
		SampledAt: now.Add(-15 * time.Second),
		Present:   true,
	}

	tests := []struct {
		name           string
		in             GuardrailInput
		wantVerdict    GuardrailVerdict
		wantAllowed    bool
		wantReasonCode string
		wantAdjusted   float64
		wantLastStage  string
		wantTraceLen   int
	}{
		{
			name: "stage 0 vetoes NaN proposed value",
			in: GuardrailInput{
				Now:           now,
				Capability:    CapabilitySetChilledWaterTemperatureSetpoint,
				ProposedValue: math.NaN(),
			},
			wantVerdict:    GuardrailVerdictVetoed,
			wantAllowed:    false,
			wantReasonCode: "invalid_proposed_value",
			wantAdjusted:   0,
			wantLastStage:  "validity",
			wantTraceLen:   1,
		},
		{
			name: "stage 0 vetoes +Inf proposed value",
			in: GuardrailInput{
				Now:           now,
				Capability:    CapabilitySetFrequency,
				ProposedValue: math.Inf(1),
			},
			wantVerdict:    GuardrailVerdictVetoed,
			wantAllowed:    false,
			wantReasonCode: "invalid_proposed_value",
			wantAdjusted:   0,
			wantLastStage:  "validity",
			wantTraceLen:   1,
		},
		{
			name: "stage 1 vetoes missing telemetry point",
			in: GuardrailInput{
				Now:           now,
				Capability:    CapabilitySetChilledWaterTemperatureSetpoint,
				ProposedValue: 8.0,
				Points: []GuardrailPointSample{
					{Key: "chw_supply_temp_c", Quality: "GOOD", SampledAt: now, Present: false},
				},
			},
			wantVerdict:    GuardrailVerdictVetoed,
			wantAllowed:    false,
			wantReasonCode: "stale_or_bad_telemetry",
			wantAdjusted:   8.0,
			wantLastStage:  "freshness",
			wantTraceLen:   2,
		},
		{
			name: "stage 1 vetoes zero timestamp point",
			in: GuardrailInput{
				Now:           now,
				Capability:    CapabilitySetChilledWaterTemperatureSetpoint,
				ProposedValue: 8.0,
				Points: []GuardrailPointSample{
					{Key: "chw_supply_temp_c", Quality: "GOOD", Present: true},
				},
			},
			wantVerdict:    GuardrailVerdictVetoed,
			wantAllowed:    false,
			wantReasonCode: "stale_or_bad_telemetry",
			wantAdjusted:   8.0,
			wantLastStage:  "freshness",
			wantTraceLen:   2,
		},
		{
			name: "stage 1 vetoes stale telemetry exceeding default 90s freshness",
			in: GuardrailInput{
				Now:           now,
				Capability:    CapabilitySetChilledWaterTemperatureSetpoint,
				ProposedValue: 8.0,
				Points: []GuardrailPointSample{
					{Key: "chw_supply_temp_c", Quality: "GOOD", SampledAt: now.Add(-91 * time.Second), Present: true},
				},
			},
			wantVerdict:    GuardrailVerdictVetoed,
			wantAllowed:    false,
			wantReasonCode: "stale_or_bad_telemetry",
			wantAdjusted:   8.0,
			wantLastStage:  "freshness",
			wantTraceLen:   2,
		},
		{
			name: "stage 1 vetoes bad quality telemetry",
			in: GuardrailInput{
				Now:           now,
				Capability:    CapabilitySetChilledWaterTemperatureSetpoint,
				ProposedValue: 8.0,
				Points: []GuardrailPointSample{
					{Key: "chw_supply_temp_c", Quality: "UNCERTAIN", SampledAt: now.Add(-5 * time.Second), Present: true},
				},
			},
			wantVerdict:    GuardrailVerdictVetoed,
			wantAllowed:    false,
			wantReasonCode: "stale_or_bad_telemetry",
			wantAdjusted:   8.0,
			wantLastStage:  "freshness",
			wantTraceLen:   2,
		},
		{
			name: "stage 1 respects exact 90s boundary as fresh",
			in: GuardrailInput{
				Now:           now,
				Capability:    CapabilitySetChilledWaterTemperatureSetpoint,
				ProposedValue: 8.0,
				Points: []GuardrailPointSample{
					{Key: "chw_supply_temp_c", Quality: "GOOD", SampledAt: now.Add(-DefaultMaxPointFreshness), Present: true},
				},
			},
			wantVerdict:   GuardrailVerdictPass,
			wantAllowed:   true,
			wantAdjusted:  8.0,
			wantLastStage: "deadband",
			wantTraceLen:  8,
		},
		{
			name: "stage 1 respects custom MaxPointAge",
			in: GuardrailInput{
				Now:           now,
				Capability:    CapabilitySetChilledWaterTemperatureSetpoint,
				ProposedValue: 8.0,
				MaxPointAge:   20 * time.Second,
				Points: []GuardrailPointSample{
					{Key: "chw_supply_temp_c", Quality: "GOOD", SampledAt: now.Add(-25 * time.Second), Present: true},
				},
			},
			wantVerdict:    GuardrailVerdictVetoed,
			wantAllowed:    false,
			wantReasonCode: "stale_or_bad_telemetry",
			wantAdjusted:   8.0,
			wantLastStage:  "freshness",
			wantTraceLen:   2,
		},
		{
			name: "stage 2 vetoes offline device",
			in: GuardrailInput{
				Now:           now,
				Capability:    CapabilitySetFrequency,
				ProposedValue: 40.0,
				DeviceOffline: true,
				Points:        []GuardrailPointSample{freshSample},
			},
			wantVerdict:    GuardrailVerdictVetoed,
			wantAllowed:    false,
			wantReasonCode: "device_offline",
			wantAdjusted:   40.0,
			wantLastStage:  "fault_and_local_veto",
			wantTraceLen:   3,
		},
		{
			name: "stage 2 vetoes active device fault flag",
			in: GuardrailInput{
				Now:               now,
				Capability:        CapabilitySetFrequency,
				ProposedValue:     40.0,
				DeviceFaultActive: true,
			},
			wantVerdict:    GuardrailVerdictVetoed,
			wantAllowed:    false,
			wantReasonCode: "device_fault_or_local_manual",
			wantAdjusted:   40.0,
			wantLastStage:  "fault_and_local_veto",
			wantTraceLen:   3,
		},
		{
			name: "stage 2 vetoes cabinet local manual flag",
			in: GuardrailInput{
				Now:                now,
				Capability:         CapabilitySetFrequency,
				ProposedValue:      40.0,
				CabinetLocalManual: true,
			},
			wantVerdict:    GuardrailVerdictVetoed,
			wantAllowed:    false,
			wantReasonCode: "device_fault_or_local_manual",
			wantAdjusted:   40.0,
			wantLastStage:  "fault_and_local_veto",
			wantTraceLen:   3,
		},
		{
			name: "stage 2 vetoes fault point in Points when bool true",
			in: GuardrailInput{
				Now:           now,
				Capability:    CapabilitySetFrequency,
				ProposedValue: 40.0,
				Points: []GuardrailPointSample{
					freshSample,
					{Key: "chiller_fault_active", BoolValue: boolPtr(true), Quality: "GOOD", SampledAt: now.Add(-5 * time.Second), Present: true},
				},
			},
			wantVerdict:    GuardrailVerdictVetoed,
			wantAllowed:    false,
			wantReasonCode: "device_fault_or_local_manual",
			wantAdjusted:   40.0,
			wantLastStage:  "fault_and_local_veto",
			wantTraceLen:   3,
		},
		{
			name: "stage 2 vetoes alarm point in Points when numeric nonzero",
			in: GuardrailInput{
				Now:           now,
				Capability:    CapabilitySetFrequency,
				ProposedValue: 40.0,
				Points: []GuardrailPointSample{
					{Key: "high_pressure_alarm", Value: float64Ptr(1), Quality: "GOOD", SampledAt: now, Present: true},
				},
			},
			wantVerdict:    GuardrailVerdictVetoed,
			wantAllowed:    false,
			wantReasonCode: "device_fault_or_local_manual",
			wantAdjusted:   40.0,
			wantLastStage:  "fault_and_local_veto",
			wantTraceLen:   3,
		},
		{
			name: "stage 2 vetoes local manual point in Points via mode string",
			in: GuardrailInput{
				Now:           now,
				Capability:    CapabilitySetFrequency,
				ProposedValue: 40.0,
				Points: []GuardrailPointSample{
					{Key: "control_mode", StringValue: "LOCAL", Quality: "GOOD", SampledAt: now, Present: true},
				},
			},
			wantVerdict:    GuardrailVerdictVetoed,
			wantAllowed:    false,
			wantReasonCode: "device_fault_or_local_manual",
			wantAdjusted:   40.0,
			wantLastStage:  "fault_and_local_veto",
			wantTraceLen:   3,
		},
		{
			name: "stage 2 allows clear fault code and remote control mode points",
			in: GuardrailInput{
				Now:           now,
				Capability:    CapabilitySetFrequency,
				ProposedValue: 40.0,
				Points: []GuardrailPointSample{
					{Key: "fault_code", StringValue: "0", Quality: "GOOD", SampledAt: now, Present: true},
					{Key: "alarm_active", BoolValue: boolPtr(false), Quality: "GOOD", SampledAt: now, Present: true},
					{Key: "control_mode", StringValue: "REMOTE_AUTO", Quality: "GOOD", SampledAt: now, Present: true},
				},
			},
			wantVerdict:   GuardrailVerdictPass,
			wantAllowed:   true,
			wantAdjusted:  40.0,
			wantLastStage: "deadband",
			wantTraceLen:  8,
		},
		{
			name: "stage 3 vetoes when manual backoff equals now",
			in: GuardrailInput{
				Now:                now,
				Capability:         CapabilitySetChilledWaterTemperatureSetpoint,
				ProposedValue:      8.0,
				ManualBackoffUntil: timePtr(now),
			},
			wantVerdict:    GuardrailVerdictVetoed,
			wantAllowed:    false,
			wantReasonCode: "manual_override_backoff",
			wantAdjusted:   8.0,
			wantLastStage:  "manual_backoff",
			wantTraceLen:   4,
		},
		{
			name: "stage 3 vetoes when manual backoff is in the future",
			in: GuardrailInput{
				Now:                now,
				Capability:         CapabilitySetChilledWaterTemperatureSetpoint,
				ProposedValue:      8.0,
				ManualBackoffUntil: timePtr(now.Add(5 * time.Minute)),
			},
			wantVerdict:    GuardrailVerdictVetoed,
			wantAllowed:    false,
			wantReasonCode: "manual_override_backoff",
			wantAdjusted:   8.0,
			wantLastStage:  "manual_backoff",
			wantTraceLen:   4,
		},
		{
			name: "stage 3 allows when manual backoff has expired",
			in: GuardrailInput{
				Now:                now,
				Capability:         CapabilitySetChilledWaterTemperatureSetpoint,
				ProposedValue:      8.0,
				ManualBackoffUntil: timePtr(now.Add(-time.Second)),
			},
			wantVerdict:   GuardrailVerdictPass,
			wantAllowed:   true,
			wantAdjusted:  8.0,
			wantLastStage: "deadband",
			wantTraceLen:  8,
		},
		{
			name: "stage 4 vetoes when hydraulic interlock is false",
			in: GuardrailInput{
				Now:                  now,
				Capability:           CapabilitySetChilledWaterTemperatureSetpoint,
				ProposedValue:        8.0,
				HydraulicInterlockOK: boolPtr(false),
			},
			wantVerdict:    GuardrailVerdictVetoed,
			wantAllowed:    false,
			wantReasonCode: "hydraulic_interlock_blocked",
			wantAdjusted:   8.0,
			wantLastStage:  "interlock_and_dwell",
			wantTraceLen:   5,
		},
		{
			name: "stage 4 vetoes when min dwell has not elapsed",
			in: GuardrailInput{
				Now:              now,
				Capability:       CapabilitySetChilledWaterTemperatureSetpoint,
				ProposedValue:    8.0,
				LastTransitionAt: timePtr(now.Add(-4 * time.Minute)),
				MinDwellDuration: 10 * time.Minute,
			},
			wantVerdict:    GuardrailVerdictVetoed,
			wantAllowed:    false,
			wantReasonCode: "min_dwell_interlock_active",
			wantAdjusted:   8.0,
			wantLastStage:  "interlock_and_dwell",
			wantTraceLen:   5,
		},
		{
			name: "stage 4 allows once exact min dwell has elapsed",
			in: GuardrailInput{
				Now:                  now,
				Capability:           CapabilitySetChilledWaterTemperatureSetpoint,
				ProposedValue:        8.0,
				HydraulicInterlockOK: boolPtr(true),
				LastTransitionAt:     timePtr(now.Add(-10 * time.Minute)),
				MinDwellDuration:     10 * time.Minute,
			},
			wantVerdict:   GuardrailVerdictPass,
			wantAllowed:   true,
			wantAdjusted:  8.0,
			wantLastStage: "deadband",
			wantTraceLen:  8,
		},
		{
			name: "stage 5 clamps CHW below physical floor 5C even if HardMin requests 2C",
			in: GuardrailInput{
				Now:           now,
				Capability:    CapabilitySetChilledWaterTemperatureSetpoint,
				ProposedValue: 3.5,
				HardMin:       float64Ptr(2.0),
			},
			wantVerdict:    GuardrailVerdictClamped,
			wantAllowed:    true,
			wantReasonCode: "hard_bounds_clamped",
			wantAdjusted:   5.0,
			wantLastStage:  "deadband",
			wantTraceLen:   8,
		},
		{
			name: "stage 5 raises CHW floor when HardMin is stricter than 5C",
			in: GuardrailInput{
				Now:           now,
				Capability:    CapabilitySetChilledWaterTemperatureSetpoint,
				ProposedValue: 5.5,
				HardMin:       float64Ptr(6.5),
			},
			wantVerdict:    GuardrailVerdictClamped,
			wantAllowed:    true,
			wantReasonCode: "hard_bounds_clamped",
			wantAdjusted:   6.5,
			wantLastStage:  "deadband",
			wantTraceLen:   8,
		},
		{
			name: "stage 5 clamps VFD below 28Hz floor and above 50Hz ceiling",
			in: GuardrailInput{
				Now:           now,
				Capability:    CapabilitySetFrequency,
				ProposedValue: 22.0,
				HardMin:       float64Ptr(20.0), // cannot undercut physical 28Hz floor
			},
			wantVerdict:    GuardrailVerdictClamped,
			wantAllowed:    true,
			wantReasonCode: "hard_bounds_clamped",
			wantAdjusted:   28.0,
			wantLastStage:  "deadband",
			wantTraceLen:   8,
		},
		{
			name: "stage 5 clamps fan speed to [25, 100]",
			in: GuardrailInput{
				Now:           now,
				Capability:    CapabilitySetFanSpeed,
				ProposedValue: 18.0,
			},
			wantVerdict:    GuardrailVerdictClamped,
			wantAllowed:    true,
			wantReasonCode: "hard_bounds_clamped",
			wantAdjusted:   25.0,
			wantLastStage:  "deadband",
			wantTraceLen:   8,
		},
		{
			name: "stage 5 clamps valve opening to [15, 100]",
			in: GuardrailInput{
				Now:           now,
				Capability:    CapabilitySetOpening,
				ProposedValue: 112.0,
			},
			wantVerdict:    GuardrailVerdictClamped,
			wantAllowed:    true,
			wantReasonCode: "hard_bounds_clamped",
			wantAdjusted:   100.0,
			wantLastStage:  "deadband",
			wantTraceLen:   8,
		},
		{
			name: "stage 5 vetoes empty hard bounds intersection",
			in: GuardrailInput{
				Now:           now,
				Capability:    CapabilitySetChilledWaterTemperatureSetpoint,
				ProposedValue: 7.0,
				HardMin:       float64Ptr(16.0), // above physical max 15C
			},
			wantVerdict:    GuardrailVerdictVetoed,
			wantAllowed:    false,
			wantReasonCode: "invalid_hard_bounds",
			wantAdjusted:   7.0,
			wantLastStage:  "hard_clamp",
			wantTraceLen:   6,
		},
		{
			name: "stage 6 clamps CHW upward slew to +1.0C from CurrentSetpoint",
			in: GuardrailInput{
				Now:             now,
				Capability:      CapabilitySetChilledWaterTemperatureSetpoint,
				ProposedValue:   11.0,
				CurrentSetpoint: float64Ptr(7.0),
			},
			wantVerdict:    GuardrailVerdictClamped,
			wantAllowed:    true,
			wantReasonCode: "slew_rate_clamped",
			wantAdjusted:   8.0,
			wantLastStage:  "deadband",
			wantTraceLen:   8,
		},
		{
			name: "stage 6 falls back to CurrentMeasured when CurrentSetpoint is nil and respects 2.5Hz step",
			in: GuardrailInput{
				Now:             now,
				Capability:      CapabilitySetFrequency,
				ProposedValue:   32.0,
				CurrentMeasured: float64Ptr(40.1),
			},
			wantVerdict:    GuardrailVerdictClamped,
			wantAllowed:    true,
			wantReasonCode: "slew_rate_clamped",
			wantAdjusted:   37.6,
			wantLastStage:  "deadband",
			wantTraceLen:   8,
		},
		{
			name: "stage 6 re-clamps against hard floor after slew so slew never exits hard bounds",
			in: GuardrailInput{
				Now:             now,
				Capability:      CapabilitySetFrequency,
				ProposedValue:   35.0,
				CurrentMeasured: float64Ptr(20.0), // e.g. cold startup below 28Hz floor
			},
			wantVerdict:    GuardrailVerdictClamped,
			wantAllowed:    true,
			wantReasonCode: "slew_rate_clamped",
			wantAdjusted:   28.0, // 20 + 2.5 = 22.5, re-clamped up to 28.0Hz hard floor
			wantLastStage:  "deadband",
			wantTraceLen:   8,
		},
		{
			name: "stage 6 does not falsely clamp on binary float boundary 8.2 + 1.1",
			in: GuardrailInput{
				Now:             now,
				Capability:      CapabilitySetChilledWaterTemperatureSetpoint,
				ProposedValue:   9.3,
				CurrentSetpoint: float64Ptr(8.2),
				MaxStep:         float64Ptr(1.1),
			},
			wantVerdict:   GuardrailVerdictPass,
			wantAllowed:   true,
			wantAdjusted:  9.3,
			wantLastStage: "deadband",
			wantTraceLen:  8,
		},
		{
			name: "stage 7 returns DEADBAND_SKIP when change is smaller than default CHW deadband 0.2C",
			in: GuardrailInput{
				Now:             now,
				Capability:      CapabilitySetChilledWaterTemperatureSetpoint,
				ProposedValue:   7.15,
				CurrentSetpoint: float64Ptr(7.0),
			},
			wantVerdict:    GuardrailVerdictDeadbandSkip,
			wantAllowed:    false,
			wantReasonCode: "within_deadband",
			wantAdjusted:   7.15,
			wantLastStage:  "deadband",
			wantTraceLen:   8,
		},
		{
			name: "stage 7 does not skip on exact binary float deadband boundary 6.3 - 6.1 == 0.2",
			in: GuardrailInput{
				Now:             now,
				Capability:      CapabilitySetChilledWaterTemperatureSetpoint,
				ProposedValue:   6.3,
				CurrentSetpoint: float64Ptr(6.1),
			},
			wantVerdict:   GuardrailVerdictPass,
			wantAllowed:   true,
			wantAdjusted:  6.3,
			wantLastStage: "deadband",
			wantTraceLen:  8,
		},
		{
			name: "stage 7 returns DEADBAND_SKIP after stage 5 clamp lands within deadband",
			in: GuardrailInput{
				Now:             now,
				Capability:      CapabilitySetChilledWaterTemperatureSetpoint,
				ProposedValue:   4.0,
				CurrentSetpoint: float64Ptr(5.1),
			},
			wantVerdict:    GuardrailVerdictDeadbandSkip,
			wantAllowed:    false,
			wantReasonCode: "within_deadband",
			wantAdjusted:   5.0,
			wantLastStage:  "deadband",
			wantTraceLen:   8,
		},
		{
			name: "happy path passes all 8 stages cleanly",
			in: GuardrailInput{
				Now:                  now,
				Capability:           CapabilitySetChilledWaterTemperatureSetpoint,
				DeviceID:             "chiller-1",
				ProposedValue:        8.0,
				CurrentSetpoint:      float64Ptr(7.5),
				Points:               []GuardrailPointSample{freshSample},
				ManualBackoffUntil:   timePtr(now.Add(-time.Minute)),
				HydraulicInterlockOK: boolPtr(true),
				LastTransitionAt:     timePtr(now.Add(-15 * time.Minute)),
				MinDwellDuration:     10 * time.Minute,
			},
			wantVerdict:   GuardrailVerdictPass,
			wantAllowed:   true,
			wantAdjusted:  8.0,
			wantLastStage: "deadband",
			wantTraceLen:  8,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := EvaluateGuardrails(tc.in)
			if got.Verdict != tc.wantVerdict || got.Allowed != tc.wantAllowed {
				t.Fatalf("verdict=%s allowed=%v, want verdict=%s allowed=%v (reason=%q detail=%q)",
					got.Verdict, got.Allowed, tc.wantVerdict, tc.wantAllowed, got.ReasonCode, got.Detail)
			}
			if tc.wantReasonCode != "" && got.ReasonCode != tc.wantReasonCode {
				t.Fatalf("reasonCode=%q, want %q", got.ReasonCode, tc.wantReasonCode)
			}
			if got.AdjustedValue != tc.wantAdjusted {
				t.Fatalf("adjustedValue=%v, want %v", got.AdjustedValue, tc.wantAdjusted)
			}
			if len(got.Trace) != tc.wantTraceLen {
				t.Fatalf("trace len=%d, want %d (%+v)", len(got.Trace), tc.wantTraceLen, got.Trace)
			}
			if got.Trace[len(got.Trace)-1].Stage != tc.wantLastStage {
				t.Fatalf("last trace stage=%q, want %q", got.Trace[len(got.Trace)-1].Stage, tc.wantLastStage)
			}
			if _, err := json.Marshal(got); err != nil {
				t.Fatalf("result must marshal cleanly to JSON: %v", err)
			}
		})
	}
}

func TestEvaluateGuardrailsTraceOrder(t *testing.T) {
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	res := EvaluateGuardrails(GuardrailInput{
		Now:             now,
		Capability:      CapabilitySetFrequency,
		ProposedValue:   55.0,
		CurrentSetpoint: float64Ptr(45.0),
	})
	if res.Verdict != GuardrailVerdictClamped || !res.Allowed || res.AdjustedValue != 47.5 {
		t.Fatalf("unexpected result: %+v", res)
	}
	wantStages := []string{
		"validity",
		"freshness",
		"fault_and_local_veto",
		"manual_backoff",
		"interlock_and_dwell",
		"hard_clamp",
		"slew_rate",
		"deadband",
	}
	if len(res.Trace) != len(wantStages) {
		t.Fatalf("trace length=%d, want %d", len(res.Trace), len(wantStages))
	}
	for i, stage := range wantStages {
		if res.Trace[i].Stage != stage {
			t.Fatalf("trace[%d].Stage=%q, want %q", i, res.Trace[i].Stage, stage)
		}
	}
	if res.Trace[5].Action != string(GuardrailVerdictClamped) || res.Trace[5].Before != 55.0 || res.Trace[5].After != 50.0 {
		t.Fatalf("unexpected hard_clamp trace: %+v", res.Trace[5])
	}
	if res.Trace[6].Action != string(GuardrailVerdictClamped) || res.Trace[6].Before != 50.0 || res.Trace[6].After != 47.5 {
		t.Fatalf("unexpected slew_rate trace: %+v", res.Trace[6])
	}
	if res.ReasonCode != "hard_and_slew_clamped" {
		t.Fatalf("reasonCode=%q, want hard_and_slew_clamped", res.ReasonCode)
	}
}

func TestEvaluateGuardrailsHardeningChecks(t *testing.T) {
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)

	t.Run("vetoes future timestamp in Points", func(t *testing.T) {
		res := EvaluateGuardrails(GuardrailInput{
			Now:           now,
			Capability:    CapabilitySetChilledWaterTemperatureSetpoint,
			ProposedValue: 8.0,
			Points: []GuardrailPointSample{
				{Key: "chw_supply_temp_c", Quality: "GOOD", SampledAt: now.Add(30 * time.Second), Present: true},
			},
		})
		if res.Allowed || res.ReasonCode != "stale_or_bad_telemetry" {
			t.Fatalf("expected future timestamp veto, got %+v", res)
		}
	})

	t.Run("vetoes non-positive MaxStep and Deadband overrides instead of disabling", func(t *testing.T) {
		resStep := EvaluateGuardrails(GuardrailInput{
			Now:             now,
			Capability:      CapabilitySetChilledWaterTemperatureSetpoint,
			ProposedValue:   8.0,
			CurrentSetpoint: float64Ptr(7.0),
			MaxStep:         float64Ptr(0),
		})
		if resStep.Allowed || resStep.ReasonCode != "invalid_max_step" {
			t.Fatalf("expected invalid_max_step veto, got %+v", resStep)
		}

		resDB := EvaluateGuardrails(GuardrailInput{
			Now:             now,
			Capability:      CapabilitySetChilledWaterTemperatureSetpoint,
			ProposedValue:   8.0,
			CurrentSetpoint: float64Ptr(7.0),
			Deadband:        float64Ptr(-0.1),
		})
		if resDB.Allowed || resDB.ReasonCode != "invalid_deadband" {
			t.Fatalf("expected invalid_deadband veto, got %+v", resDB)
		}
	})

	t.Run("honors remote polarity on local_remote_mode", func(t *testing.T) {
		localBool := EvaluateGuardrails(GuardrailInput{
			Now:           now,
			Capability:    CapabilitySetChilledWaterTemperatureSetpoint,
			ProposedValue: 8.0,
			Points: []GuardrailPointSample{
				{Key: "local_remote_mode", BoolValue: boolPtr(false), Quality: "GOOD", SampledAt: now, Present: true},
			},
		})
		if localBool.Allowed || localBool.ReasonCode != "device_fault_or_local_manual" {
			t.Fatalf("expected local_remote_mode=false veto, got %+v", localBool)
		}

		remoteBool := EvaluateGuardrails(GuardrailInput{
			Now:           now,
			Capability:    CapabilitySetChilledWaterTemperatureSetpoint,
			ProposedValue: 8.0,
			Points: []GuardrailPointSample{
				{Key: "local_remote_mode", BoolValue: boolPtr(true), Quality: "GOOD", SampledAt: now, Present: true},
			},
		})
		if !remoteBool.Allowed || remoteBool.Verdict != GuardrailVerdictPass {
			t.Fatalf("expected local_remote_mode=true pass, got %+v", remoteBool)
		}
	})
}
