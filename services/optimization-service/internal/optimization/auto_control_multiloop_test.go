package optimization

import (
	"context"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandauth"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/intelligencemodel"
)

type multiLoopSeqDispatcher struct {
	mu           sync.Mutex
	grantReqs    []commandauth.WorkloadDecisionRequest
	submitInputs []AutoControlDispatchInput
	decision     commandauth.Decision
	grant        string
	seq          int
}

func (d *multiLoopSeqDispatcher) RequestCommandGrant(_ context.Context, req commandauth.WorkloadDecisionRequest) (string, commandauth.Decision, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.grantReqs = append(d.grantReqs, req)
	return d.grant, d.decision, nil
}

func (d *multiLoopSeqDispatcher) SubmitAutoCommand(_ context.Context, _ string, input AutoControlDispatchInput) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.submitInputs = append(d.submitInputs, input)
	d.seq++
	return fmt.Sprintf("01990000-9900-7000-8000-%012d", d.seq), nil
}

func testMultiLoopSetup(now time.Time) (
	*fakeAutoControlStore,
	*fakePointHealthChecker,
	*fakeTelemetryReader,
	*multiLoopSeqDispatcher,
	*memoryFlywheelStore,
	*AutoController,
) {
	chwDevID := "01990000-6000-7000-8000-000000000002"
	fanDevID := "01990000-6000-7000-8000-000000000003"
	sampledAt := now.Add(-15 * time.Second).UTC()
	receivedAt := now.Add(-14 * time.Second).UTC()

	lease := testAutoLease(now)
	lease.MaxCommandsPerHour = 6
	lease.AllowedCapabilities = []string{
		string(commandmodel.CapabilitySetChilledWaterTemperatureSetpoint),
		string(commandmodel.CapabilitySetFrequency),
		string(commandmodel.CapabilitySetFanSpeed),
	}
	freqMin := 28.0
	freqMax := 50.0
	freqStep := 2.5
	freqDb := 0.5
	fanMin := 25.0
	fanMax := 100.0
	fanStep := 10.0
	fanDb := 2.0
	lease.PumpFreqMinHz = &freqMin
	lease.PumpFreqMaxHz = &freqMax
	lease.MaxFreqStepHz = &freqStep
	lease.FreqDeadbandHz = &freqDb
	lease.FanSpeedMinPct = &fanMin
	lease.FanSpeedMaxPct = &fanMax
	lease.MaxFanStepPct = &fanStep
	lease.FanDeadbandPct = &fanDb

	siteCtx := testAutoSiteCtx()
	siteCtx.DeviceIDs = append(siteCtx.DeviceIDs, chwDevID, fanDevID)
	siteCtx.LeaseFreqDeadbandHz = &freqDb
	siteCtx.LeaseFanDeadbandPct = &fanDb
	siteCtx.ActiveLoopMonitors = map[string]bool{}
	siteCtx.AuxiliaryTargets = []AuxiliaryLoopTarget{
		{
			LoopKey:    LoopKeyChilledWaterPump,
			Capability: commandmodel.CapabilitySetFrequency,
			AssetType:  AssetTypeCHWP,
			Target: SetpointCommandTarget{
				DeviceID:         chwDevID,
				PointID:          "01990000-7000-7000-8000-000000000002",
				PointCode:        "chwp_freq_sp",
				FeedbackPointKey: "frequency_hz",
				AssetID:          "01990000-4000-7000-8000-000000000002",
				SiteControlMode:  SiteControlModeRemoteAuto,
			},
		},
		{
			LoopKey:    LoopKeyCoolingTowerFan,
			Capability: commandmodel.CapabilitySetFanSpeed,
			AssetType:  AssetTypeCoolingTower,
			Target: SetpointCommandTarget{
				DeviceID:         fanDevID,
				PointID:          "01990000-7000-7000-8000-000000000003",
				PointCode:        "ct_fan_speed_sp",
				FeedbackPointKey: "fan_speed_pct",
				AssetID:          "01990000-4000-7000-8000-000000000003",
				SiteControlMode:  SiteControlModeRemoteAuto,
			},
		},
	}

	points := testLivePoints(now, 7.0, 7.0, 23.0)
	points = append(points,
		TelemetryLivePoint{
			DeviceID:         chwDevID,
			TelemetryKey:     "frequency_hz",
			NumericValue:     float64Ptr(42.0),
			Unit:             "Hz",
			Quality:          "GOOD",
			Freshness:        "FRESH",
			DevicePresence:   "ONLINE",
			BusinessRevision: 501,
			PointRevision:    11,
			SampledAt:        sampledAt,
			ReceivedAt:       receivedAt,
		},
		TelemetryLivePoint{
			DeviceID:         fanDevID,
			TelemetryKey:     "fan_speed_pct",
			NumericValue:     float64Ptr(60.0),
			Unit:             "%",
			Quality:          "GOOD",
			Freshness:        "FRESH",
			DevicePresence:   "ONLINE",
			BusinessRevision: 601,
			PointRevision:    21,
			SampledAt:        sampledAt,
			ReceivedAt:       receivedAt,
		},
	)

	store := &fakeAutoControlStore{
		lease:   lease,
		siteCtx: siteCtx,
	}
	health := &fakePointHealthChecker{livePoints: points}
	telemetry := &fakeTelemetryReader{
		obs: AutoControlLiveObservation{
			SupplyTemp: &TelemetryEvidence{
				Value: 7.0,
			},
			ZoneTemps: map[string]TelemetryEvidence{
				"zone_temperature_a": {Value: 23.0},
			},
			RunningChillers: 1,
			ChillersHealthy: true,
		},
	}
	disp := &multiLoopSeqDispatcher{
		grant: "grant-multi-1",
		decision: commandauth.Decision{
			Allowed: true,
		},
	}
	fw := &memoryFlywheelStore{}
	ctrl, err := NewAutoController(store, telemetry, disp, func() time.Time { return now }, nil)
	if err != nil {
		panic(err)
	}
	ctrl.WithPointHealth(health).WithFlywheelStore(fw)
	return store, health, telemetry, disp, fw, ctrl
}

func TestAutoControllerMultiLoopDispatchClampShadowAndVetoes(t *testing.T) {
	now := time.Date(2026, 11, 21, 5, 0, 0, 0, time.UTC)

	t.Run("dispatches PLANT plus slew-clamped CHW_PUMP and pass-through CT_FAN recording negative sample", func(t *testing.T) {
		store, _, _, disp, fw, ctrl := testMultiLoopSetup(now)
		rec := testAutoRecommendation(now.Add(-2 * time.Minute))
		rec.Candidate.SupplyTempC = 8.0
		rec.AuxiliaryTargets = []intelligencemodel.OptimizationAuxiliaryTarget{
			{LoopKey: LoopKeyChilledWaterPump, Capability: string(commandmodel.CapabilitySetFrequency), TargetValue: 22.0, Unit: "Hz"}, // below 28Hz min & delta 20 > 2.5Hz step -> clamped to 39.5Hz
			{LoopKey: LoopKeyCoolingTowerFan, Capability: string(commandmodel.CapabilitySetFanSpeed), TargetValue: 66.0, Unit: "%"},
		}

		out, err := ctrl.TryDispatch(t.Context(), rec, now)
		if err != nil {
			t.Fatalf("TryDispatch error: %v", err)
		}
		if !out.Dispatched || len(out.AuxiliaryOutcomes) != 2 {
			t.Fatalf("expected plant dispatched + 2 aux outcomes, got %#v", out)
		}
		chwOut, fanOut := out.AuxiliaryOutcomes[0], out.AuxiliaryOutcomes[1]
		if !chwOut.Dispatched || !fanOut.Dispatched {
			t.Fatalf("expected both aux loops dispatched, got chw=%#v fan=%#v", chwOut, fanOut)
		}
		if len(disp.submitInputs) != 3 {
			t.Fatalf("expected 3 gateway submissions, got %d", len(disp.submitInputs))
		}
		chwSub := disp.submitInputs[1]
		if math.Abs(chwSub.Parameters[commandmodel.ParameterFrequencyHz]-39.5) > 1e-6 {
			t.Fatalf("expected CHW_PUMP frequency slew-clamped to 39.5 Hz, got %+v", chwSub.Parameters)
		}
		fanSub := disp.submitInputs[2]
		if math.Abs(fanSub.Parameters[commandmodel.ParameterFanSpeedPct]-66.0) > 1e-6 {
			t.Fatalf("expected CT_FAN speed 66.0%%, got %+v", fanSub.Parameters)
		}
		if len(store.monitors) != 3 {
			t.Fatalf("expected 3 execution monitors, got %d", len(store.monitors))
		}
		if store.monitors[1].LoopKey != LoopKeyChilledWaterPump || store.monitors[1].TargetUnit != TargetUnitHz {
			t.Fatalf("unexpected pump monitor metadata: %#v", store.monitors[1])
		}
		if store.monitors[2].LoopKey != LoopKeyCoolingTowerFan || store.monitors[2].TargetUnit != TargetUnitPct {
			t.Fatalf("unexpected fan monitor metadata: %#v", store.monitors[2])
		}
		if len(fw.samples) == 0 || fw.samples[0].SampleKind != NegativeSampleGuardrailClamped {
			t.Fatalf("expected guardrail clamped flywheel negative sample for CHW_PUMP, got %#v", fw.samples)
		}
		if len(fw.ticks) != 3 {
			t.Fatalf("expected 3 control ticks (PLANT, CHW_PUMP, CT_FAN), got %d", len(fw.ticks))
		}
	})

	t.Run("SHADOW lease records SHADOW_RECORDED auxiliary monitors without dispatching commands", func(t *testing.T) {
		store, _, _, disp, _, ctrl := testMultiLoopSetup(now)
		store.lease.ExecutionMode = ExecutionModeShadow
		rec := testAutoRecommendation(now.Add(-2 * time.Minute))
		rec.Candidate.SupplyTempC = 8.0
		rec.AuxiliaryTargets = []intelligencemodel.OptimizationAuxiliaryTarget{
			{LoopKey: LoopKeyChilledWaterPump, Capability: string(commandmodel.CapabilitySetFrequency), TargetValue: 40.5, Unit: "Hz"},
			{LoopKey: LoopKeyCoolingTowerFan, Capability: string(commandmodel.CapabilitySetFanSpeed), TargetValue: 66.0, Unit: "%"},
		}
		out, err := ctrl.TryDispatch(t.Context(), rec, now)
		if err != nil {
			t.Fatalf("TryDispatch error: %v", err)
		}
		if out.Dispatched || !out.ShadowRecorded || len(disp.submitInputs) != 0 {
			t.Fatalf("expected shadow mode zero dispatches, got out=%#v submits=%d", out, len(disp.submitInputs))
		}
		if len(store.monitors) != 3 {
			t.Fatalf("expected 3 shadow monitors, got %d", len(store.monitors))
		}
		for idx, auxOut := range out.AuxiliaryOutcomes {
			if auxOut.Dispatched || !auxOut.ShadowRecorded {
				t.Fatalf("aux[%d] expected ShadowRecorded, got %#v", idx, auxOut)
			}
			if store.monitors[idx+1].Status != ExecutionMonitorShadowRecorded {
				t.Fatalf("monitor[%d] expected SHADOW_RECORDED, got %s", idx+1, store.monitors[idx+1].Status)
			}
		}
	})

	t.Run("auxiliary loops still evaluate on primary DEADBAND_SKIP and honor fault/deadband/backoff vetoes", func(t *testing.T) {
		_, health, _, disp, _, ctrl := testMultiLoopSetup(now)
		// Faulty CT_FAN device and within-deadband PUMP (42.2 vs 42.0 -> diff 0.2 <= 0.5), while second recommendation moves PUMP to 40.5 after resetting fault.
		health.livePoints = append(health.livePoints, TelemetryLivePoint{
			DeviceID:         "01990000-6000-7000-8000-000000000003",
			TelemetryKey:     "fault_code",
			NumericValue:     float64Ptr(12),
			Quality:          "GOOD",
			Freshness:        "FRESH",
			DevicePresence:   "ONLINE",
			BusinessRevision: 700,
			PointRevision:    1,
			SampledAt:        now.Add(-10 * time.Second),
			ReceivedAt:       now.Add(-9 * time.Second),
		})
		rec := testAutoRecommendation(now.Add(-2 * time.Minute))
		rec.Candidate.SupplyTempC = 7.1 // primary DEADBAND_SKIP vs 7.0C
		rec.AuxiliaryTargets = []intelligencemodel.OptimizationAuxiliaryTarget{
			{LoopKey: LoopKeyChilledWaterPump, Capability: string(commandmodel.CapabilitySetFrequency), TargetValue: 42.2, Unit: "Hz"}, // deadband skip
			{LoopKey: LoopKeyCoolingTowerFan, Capability: string(commandmodel.CapabilitySetFanSpeed), TargetValue: 66.0, Unit: "%"},    // fault veto
		}
		out, err := ctrl.TryDispatch(t.Context(), rec, now)
		if err != nil {
			t.Fatalf("TryDispatch error: %v", err)
		}
		if out.Dispatched || out.SkipReason != "within_deadband" {
			t.Fatalf("expected primary within_deadband, got %#v", out)
		}
		if len(out.AuxiliaryOutcomes) != 2 {
			t.Fatalf("expected 2 aux outcomes on primary deadband skip, got %#v", out.AuxiliaryOutcomes)
		}
		if out.AuxiliaryOutcomes[0].SkipReason != "within_deadband" || out.AuxiliaryOutcomes[1].SkipReason != "device_fault_or_local_manual" {
			t.Fatalf("unexpected aux skip reasons: %#v", out.AuxiliaryOutcomes)
		}
		if len(disp.submitInputs) != 0 {
			t.Fatalf("expected zero commands dispatched, got %d", len(disp.submitInputs))
		}
	})
}

func TestAutoControllerMultiLoopMonitorVerifyTamperAndRollback(t *testing.T) {
	now := time.Date(2026, 11, 21, 5, 0, 0, 0, time.UTC)

	t.Run("VFD monitor verifies cleanly without cross-unit 7C vs 28Hz false trip", func(t *testing.T) {
		store, _, _, _, _, ctrl := testMultiLoopSetup(now)
		rec := testAutoRecommendation(now.Add(-2 * time.Minute))
		rec.Candidate.SupplyTempC = 7.05 // skip PLANT
		rec.AuxiliaryTargets = []intelligencemodel.OptimizationAuxiliaryTarget{
			{LoopKey: LoopKeyChilledWaterPump, Capability: string(commandmodel.CapabilitySetFrequency), TargetValue: 40.0, Unit: "Hz"},
		}
		out, err := ctrl.TryDispatch(t.Context(), rec, now)
		if err != nil || len(out.AuxiliaryOutcomes) != 1 || !out.AuxiliaryOutcomes[0].Dispatched {
			t.Fatalf("expected VFD dispatch, got out=%#v err=%v", out, err)
		}

		// Update live feedback frequency to converged 40.0 Hz at end of 30m window while supply water stays 7.0 C (< 28.0 Hz!).
		verifyNow := now.Add(DefaultExecutionMonitorWindow)
		_, health, _, disp, _, _ := testMultiLoopSetup(verifyNow)
		for idx := range health.livePoints {
			if health.livePoints[idx].TelemetryKey == "frequency_hz" {
				health.livePoints[idx].NumericValue = float64Ptr(40.0)
			}
			health.livePoints[idx].SampledAt = verifyNow.Add(-10 * time.Second)
			health.livePoints[idx].ReceivedAt = verifyNow.Add(-9 * time.Second)
		}
		ctrl.pointHealth = health
		summary, passErr := ctrl.RunMonitorPass(t.Context(), verifyNow)
		if passErr != nil {
			t.Fatalf("RunMonitorPass err: %v", passErr)
		}
		if summary.Checked != 1 || summary.Verified != 1 || summary.RolledBack != 0 {
			t.Fatalf("expected VFD monitor VERIFIED without false supply temp trip, got summary=%#v tripped=%#v submits=%#v", summary, store.trippedRecords, disp.submitInputs)
		}
	})

	t.Run("auxiliary fan tamper triggers MANUAL_OVERRIDE and VFD zone violation rolls back ParameterFrequencyHz", func(t *testing.T) {
		store, health, _, disp, fw, ctrl := testMultiLoopSetup(now)
		rec := testAutoRecommendation(now.Add(-2 * time.Minute))
		rec.Candidate.SupplyTempC = 7.05 // skip PLANT
		rec.AuxiliaryTargets = []intelligencemodel.OptimizationAuxiliaryTarget{
			{LoopKey: LoopKeyCoolingTowerFan, Capability: string(commandmodel.CapabilitySetFanSpeed), TargetValue: 68.0, Unit: "%"},
		}
		if out, err := ctrl.TryDispatch(t.Context(), rec, now); err != nil || len(out.AuxiliaryOutcomes) != 1 || !out.AuxiliaryOutcomes[0].Dispatched {
			t.Fatalf("expected CT_FAN dispatch, got out=%#v err=%v", out, err)
		}

		tamperAt := now.Add(2 * time.Minute)
		for idx := range health.livePoints {
			if health.livePoints[idx].TelemetryKey == "fan_speed_pct" {
				health.livePoints[idx].NumericValue = float64Ptr(59.0)
			}
			health.livePoints[idx].SampledAt = tamperAt.Add(-10 * time.Second)
			health.livePoints[idx].ReceivedAt = tamperAt.Add(-9 * time.Second)
		}
		summaryManual, err := ctrl.RunMonitorPass(t.Context(), tamperAt)
		if err != nil || summaryManual.ManualOverrides != 1 {
			t.Fatalf("expected manual override detection on tampered CT fan speed, got summary=%#v err=%v", summaryManual, err)
		}
		if len(fw.samples) == 0 || fw.samples[len(fw.samples)-1].SampleKind != NegativeSampleManualOverride {
			t.Fatalf("expected MANUAL_OVERRIDE negative sample, got %#v", fw.samples)
		}

		_ = store
		store2, health2, tel2, disp2, _, ctrl2 := testMultiLoopSetup(now)
		recPump := testAutoRecommendation(now.Add(-2 * time.Minute))
		recPump.Candidate.SupplyTempC = 7.05
		recPump.AuxiliaryTargets = []intelligencemodel.OptimizationAuxiliaryTarget{
			{LoopKey: LoopKeyChilledWaterPump, Capability: string(commandmodel.CapabilitySetFrequency), TargetValue: 40.0, Unit: "Hz"},
		}
		if out, err := ctrl2.TryDispatch(t.Context(), recPump, now); err != nil || len(out.AuxiliaryOutcomes) != 1 || !out.AuxiliaryOutcomes[0].Dispatched {
			t.Fatalf("expected pump dispatch, got out=%#v err=%v", out, err)
		}
		tripAt := now.Add(3 * time.Minute)
		for idx := range health2.livePoints {
			switch health2.livePoints[idx].TelemetryKey {
			case "frequency_hz":
				health2.livePoints[idx].NumericValue = float64Ptr(40.0)
			case "zone_temperature_a":
				health2.livePoints[idx].NumericValue = float64Ptr(27.5) // > 26.0C max
			}
			health2.livePoints[idx].SampledAt = tripAt.Add(-10 * time.Second)
			health2.livePoints[idx].ReceivedAt = tripAt.Add(-9 * time.Second)
		}
		tel2.obs.ZoneTemps["zone_temperature_a"] = TelemetryEvidence{Value: 27.5}
		summaryTrip, err := ctrl2.RunMonitorPass(t.Context(), tripAt)
		if err != nil || summaryTrip.RolledBack != 1 {
			t.Fatalf("expected CHW_PUMP rollback on zone violation, got summary=%#v err=%v", summaryTrip, err)
		}
		if len(disp2.submitInputs) != 2 || !disp2.submitInputs[1].IsRollback ||
			disp2.submitInputs[1].Capability != commandmodel.CapabilitySetFrequency ||
			math.Abs(disp2.submitInputs[1].Parameters[commandmodel.ParameterFrequencyHz]-42.0) > 1e-6 ||
			store2.monitors[0].Status != ExecutionMonitorTrippedRolledBack {
			t.Fatalf("expected ParameterFrequencyHz=42.0 rollback, got submits=%#v monitors=%#v", disp2.submitInputs, store2.monitors)
		}
		_ = disp
	})
}
