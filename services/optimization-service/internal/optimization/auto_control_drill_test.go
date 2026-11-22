package optimization

import (
	"math"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandauth"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/intelligencemodel"
)

// syncDrillTelemetry updates live point and observation timestamps for a simulated minute tick.
func syncDrillTelemetry(
	now time.Time,
	telemetry *fakeTelemetryReader,
	health *fakePointHealthChecker,
	supplyWaterC *float64,
	mutators ...func([]TelemetryLivePoint),
) {
	sampled := now.Add(-15 * time.Second).UTC()
	received := now.Add(-14 * time.Second).UTC()

	health.mu.Lock()
	pts := make([]TelemetryLivePoint, len(health.livePoints))
	copy(pts, health.livePoints)
	health.mu.Unlock()

	for i := range pts {
		pts[i].SampledAt = sampled
		pts[i].ReceivedAt = received
		if supplyWaterC != nil && pts[i].TelemetryKey == "supply_water_temperature" {
			v := *supplyWaterC
			pts[i].NumericValue = &v
		}
	}
	for _, mutate := range mutators {
		mutate(pts)
	}
	health.setLivePoints(pts)

	if telemetry.obs.SupplyTemp != nil {
		supplyCopy := *telemetry.obs.SupplyTemp
		supplyCopy.SampledAt = sampled
		if supplyWaterC != nil {
			supplyCopy.Value = *supplyWaterC
		}
		telemetry.obs.SupplyTemp = &supplyCopy
	}
	if len(telemetry.obs.ZoneTemps) > 0 {
		zones := make(map[string]TelemetryEvidence, len(telemetry.obs.ZoneTemps))
		for k, ev := range telemetry.obs.ZoneTemps {
			ev.SampledAt = sampled
			zones[k] = ev
		}
		telemetry.obs.ZoneTemps = zones
	}
}

func setNumericLivePoint(key string, value float64) func([]TelemetryLivePoint) {
	return func(pts []TelemetryLivePoint) {
		for i := range pts {
			if pts[i].TelemetryKey == key {
				v := value
				pts[i].NumericValue = &v
			}
		}
	}
}

func setTextLivePoint(key, value string) func([]TelemetryLivePoint) {
	return func(pts []TelemetryLivePoint) {
		for i := range pts {
			if pts[i].TelemetryKey == key {
				v := value
				pts[i].TextValue = &v
				pts[i].BoolValue = nil
			}
		}
	}
}

func newSingleLoopDrillFixture(
	t *testing.T,
	now time.Time,
) (*time.Time, *fakeAutoControlStore, *fakePointHealthChecker, *fakeTelemetryReader, *fakeDispatcher, *memoryFlywheelStore, *AutoController) {
	t.Helper()
	curNow := now
	lease := testAutoLease(now)
	lease.ExpiresAt = now.Add(8 * time.Hour)
	store := &fakeAutoControlStore{
		lease:   lease,
		siteCtx: testAutoSiteCtx(),
	}
	health := &fakePointHealthChecker{
		livePoints: testLivePoints(now, 7.0, 7.0, 23.0),
	}
	telemetry := &fakeTelemetryReader{
		obs: testLiveObservation(now, 7.0, 23.0),
	}
	disp := &fakeDispatcher{
		decision:  commandauth.Decision{Allowed: true, ReasonCode: commandauth.ReasonAllowAutoLease},
		grant:     "grant-drill-1",
		commandID: "01990000-7700-7000-8000-000000000956",
	}
	fw := &memoryFlywheelStore{}
	ctrl, err := NewAutoController(store, telemetry, disp, func() time.Time { return curNow }, nil)
	if err != nil {
		t.Fatalf("NewAutoController failed: %v", err)
	}
	ctrl.WithPointHealth(health).WithFlywheelStore(fw)
	return &curNow, store, health, telemetry, disp, fw, ctrl
}

func TestAutoControllerClosedLoopFaultDrills(t *testing.T) {
	base := time.Date(2026, 11, 22, 2, 0, 0, 0, time.UTC)

	t.Run("shadow to closed loop tolerates 10 minute water thermal lag across 30 minute monitor window", func(t *testing.T) {
		curNow, store, health, telemetry, disp, fw, ctrl := newSingleLoopDrillFixture(t, base)
		store.lease.ExecutionMode = ExecutionModeShadow

		shadowRec := testAutoRecommendation(base.Add(-time.Minute))
		shadowRec.ID = "rec-drill-1-shadow"
		shadowRec.Candidate.SupplyTempC = 7.5
		shadowOut, err := ctrl.TryDispatch(t.Context(), shadowRec, base)
		if err != nil {
			t.Fatalf("SHADOW TryDispatch failed: %v", err)
		}
		if shadowOut.Dispatched || !shadowOut.ShadowRecorded || shadowOut.ExecutionMode != ExecutionModeShadow {
			t.Fatalf("expected SHADOW_RECORDED without dispatch, got %+v", shadowOut)
		}
		if len(disp.submitInputs) != 0 || len(store.monitors) != 1 || store.monitors[0].Status != ExecutionMonitorShadowRecorded {
			t.Fatalf("SHADOW mode must record SHADOW_RECORDED without submitting commands: submits=%d monitors=%+v", len(disp.submitInputs), store.monitors)
		}
		if len(fw.ticks) != 1 || fw.ticks[0].ControlMode != ControlModeAIShadow {
			t.Fatalf("expected 1 AI_SHADOW control tick, got %+v", fw.ticks)
		}

		store.lease.ExecutionMode = ExecutionModeClosedLoop
		closedRec := testAutoRecommendation(base.Add(-time.Minute))
		closedRec.ID = "rec-drill-1-closed"
		closedRec.Candidate.SupplyTempC = 7.5
		closedOut, err := ctrl.TryDispatch(t.Context(), closedRec, base)
		if err != nil {
			t.Fatalf("CLOSED_LOOP TryDispatch failed: %v", err)
		}
		if !closedOut.Dispatched || closedOut.ExecutionMode != ExecutionModeClosedLoop || len(disp.submitInputs) != 1 {
			t.Fatalf("expected CLOSED_LOOP dispatch, got out=%+v submits=%d", closedOut, len(disp.submitInputs))
		}
		if math.Abs(disp.submitInputs[0].Parameters[commandmodel.ParameterSetpointC]-7.5) > 1e-6 {
			t.Fatalf("expected 7.5C setpoint command, got %+v", disp.submitInputs[0].Parameters)
		}
		if len(store.monitors) != 2 || store.monitors[1].Status != ExecutionMonitorObserving {
			t.Fatalf("expected 2 monitors with closed-loop OBSERVING, got %+v", store.monitors)
		}

		// Register accepts 7.5C setpoint while measured chilled water lags toward 7.5C over ~10 minutes.
		for minute := 1; minute <= 30; minute++ {
			stepNow := base.Add(time.Duration(minute) * time.Minute)
			*curNow = stepNow
			supplyLag := 7.0 + 0.5*(1.0-math.Exp(-float64(minute)/4.0))
			syncDrillTelemetry(stepNow, telemetry, health, &supplyLag, setNumericLivePoint("chilled_water_supply_temp_sp", 7.5))

			summary, passErr := ctrl.RunMonitorPass(t.Context(), stepNow)
			if passErr != nil {
				t.Fatalf("RunMonitorPass at minute %d failed: %v", minute, passErr)
			}
			if summary.RolledBack != 0 || summary.Tripped != 0 || summary.ManualOverrides != 0 {
				t.Fatalf("thermal lag at minute %d must not trip/rollback, got summary=%+v", minute, summary)
			}
			if minute < 30 {
				if summary.Verified != 0 || store.monitors[1].Status != ExecutionMonitorObserving {
					t.Fatalf("expected OBSERVING before 30m at minute %d, got summary=%+v status=%s", minute, summary, store.monitors[1].Status)
				}
			} else if summary.Verified != 1 || store.monitors[1].Status != ExecutionMonitorVerified {
				t.Fatalf("expected VERIFIED at minute 30, got summary=%+v status=%s", summary, store.monitors[1].Status)
			}
		}
	})

	t.Run("cabinet LOCAL switch records MANUAL_OVERRIDE triggers 2h backoff and recovers after REMOTE", func(t *testing.T) {
		curNow, store, health, telemetry, disp, fw, ctrl := newSingleLoopDrillFixture(t, base)
		store.lease.ExecutionMode = ExecutionModeClosedLoop

		rec := testAutoRecommendation(base.Add(-time.Minute))
		rec.ID = "rec-drill-2-initial"
		rec.Candidate.SupplyTempC = 7.5
		out, err := ctrl.TryDispatch(t.Context(), rec, base)
		if err != nil || !out.Dispatched {
			t.Fatalf("initial dispatch failed: out=%+v err=%v", out, err)
		}

		for minute := 1; minute <= 5; minute++ {
			stepNow := base.Add(time.Duration(minute) * time.Minute)
			*curNow = stepNow
			supply := 7.2
			syncDrillTelemetry(stepNow, telemetry, health, &supply, setNumericLivePoint("chilled_water_supply_temp_sp", 7.5))
			summary, passErr := ctrl.RunMonitorPass(t.Context(), stepNow)
			if passErr != nil {
				t.Fatalf("RunMonitorPass at minute %d failed: %v", minute, passErr)
			}
			if summary.ManualOverrides != 0 || store.monitors[0].Status != ExecutionMonitorObserving {
				t.Fatalf("expected clean OBSERVING at minute %d, got summary=%+v status=%s", minute, summary, store.monitors[0].Status)
			}
		}

		// Operator switches physical cabinet selector to LOCAL at minute 6.
		at6 := base.Add(6 * time.Minute)
		*curNow = at6
		supply6 := 7.3
		syncDrillTelemetry(at6, telemetry, health, &supply6,
			setNumericLivePoint("chilled_water_supply_temp_sp", 7.5),
			setTextLivePoint("local_remote_mode", "LOCAL"),
		)
		summary6, err := ctrl.RunMonitorPass(t.Context(), at6)
		if err != nil {
			t.Fatalf("RunMonitorPass at LOCAL switch failed: %v", err)
		}
		if summary6.ManualOverrides != 1 || summary6.RolledBack != 0 || len(disp.submitInputs) != 1 {
			t.Fatalf("expected MANUAL_OVERRIDE without remote rollback, got summary=%+v submits=%d", summary6, len(disp.submitInputs))
		}
		if store.monitors[0].Status != ExecutionMonitorManualOverride {
			t.Fatalf("expected monitor status MANUAL_OVERRIDE, got %s", store.monitors[0].Status)
		}
		wantBackoffUntil := at6.Add(DefaultCabinetManualBackoffWindow)
		if store.lease.ManualBackoffUntil == nil || !store.lease.ManualBackoffUntil.Equal(wantBackoffUntil) {
			t.Fatalf("expected 2h ManualBackoffUntil=%v, got %v", wantBackoffUntil, store.lease.ManualBackoffUntil)
		}
		if len(fw.samples) == 0 || fw.samples[len(fw.samples)-1].SampleKind != NegativeSampleManualOverride {
			t.Fatalf("expected NegativeSampleManualOverride in flywheel store, got %+v", fw.samples)
		}

		// Restoring REMOTE at minute 30 stays blocked until 2h backoff expires.
		at30 := base.Add(30 * time.Minute)
		*curNow = at30
		supply30 := 7.0
		syncDrillTelemetry(at30, telemetry, health, &supply30,
			setNumericLivePoint("chilled_water_supply_temp_sp", 7.0),
			setTextLivePoint("local_remote_mode", "REMOTE"),
		)
		backoffRec := testAutoRecommendation(at30.Add(-time.Minute))
		backoffRec.ID = "rec-drill-2-backoff"
		backoffRec.Candidate.SupplyTempC = 7.8
		backoffOut, err := ctrl.TryDispatch(t.Context(), backoffRec, at30)
		if err != nil {
			t.Fatalf("TryDispatch during backoff returned error: %v", err)
		}
		if backoffOut.Dispatched || backoffOut.SkipReason != "manual_override_backoff" {
			t.Fatalf("expected manual_override_backoff skip, got %+v", backoffOut)
		}

		// Once 2h backoff passes (+2h07m from base > 6m + 2h), dispatch recovers cleanly.
		at127 := base.Add(2*time.Hour + 7*time.Minute)
		*curNow = at127
		syncDrillTelemetry(at127, telemetry, health, &supply30,
			setNumericLivePoint("chilled_water_supply_temp_sp", 7.0),
			setTextLivePoint("local_remote_mode", "REMOTE"),
		)
		recoverRec := testAutoRecommendation(at127.Add(-time.Minute))
		recoverRec.ID = "rec-drill-2-recovered"
		recoverRec.Candidate.SupplyTempC = 7.8
		recoverOut, err := ctrl.TryDispatch(t.Context(), recoverRec, at127)
		if err != nil {
			t.Fatalf("TryDispatch after backoff expiry failed: %v", err)
		}
		if !recoverOut.Dispatched || len(disp.submitInputs) != 2 {
			t.Fatalf("expected dispatch recovery after 2h backoff, got out=%+v submits=%d", recoverOut, len(disp.submitInputs))
		}
	})

	t.Run("read-only register lock holds baseline feedback across convergence grace then rolls back multi-loop", func(t *testing.T) {
		store, health, telemetry, disp, fw, ctrl := testMultiLoopSetup(base)
		store.lease.ExpiresAt = base.Add(8 * time.Hour)

		rec := testAutoRecommendation(base.Add(-time.Minute))
		rec.ID = "rec-drill-3-readonly"
		rec.Candidate.SupplyTempC = 7.8
		rec.AuxiliaryTargets = []intelligencemodel.OptimizationAuxiliaryTarget{
			{LoopKey: LoopKeyChilledWaterPump, Capability: string(commandmodel.CapabilitySetFrequency), TargetValue: 38.0, Unit: "Hz"},
		}
		out, err := ctrl.TryDispatch(t.Context(), rec, base)
		if err != nil {
			t.Fatalf("multi-loop TryDispatch failed: %v", err)
		}
		if !out.Dispatched || len(out.AuxiliaryOutcomes) != 1 || !out.AuxiliaryOutcomes[0].Dispatched {
			t.Fatalf("expected primary and CHW_PUMP dispatches, got %+v", out)
		}
		if len(disp.submitInputs) != 2 || len(store.monitors) != 2 {
			t.Fatalf("expected 2 commands and 2 monitors, got submits=%d monitors=%d", len(disp.submitInputs), len(store.monitors))
		}
		if math.Abs(disp.submitInputs[0].Parameters[commandmodel.ParameterSetpointC]-7.8) > 1e-6 ||
			math.Abs(disp.submitInputs[1].Parameters[commandmodel.ParameterFrequencyHz]-39.5) > 1e-6 {
			t.Fatalf("unexpected initial commands: %+v", disp.submitInputs)
		}

		// Both registers ACK commands at gateway yet physical feedback stays locked at baseline (7.0C / 42.0Hz).
		baselineSupply := 7.0
		for _, minute := range []int{5, 9} {
			stepNow := base.Add(time.Duration(minute) * time.Minute)
			syncDrillTelemetry(stepNow, telemetry, health, &baselineSupply)
			summary, passErr := ctrl.RunMonitorPass(t.Context(), stepNow)
			if passErr != nil {
				t.Fatalf("RunMonitorPass at minute %d failed: %v", minute, passErr)
			}
			if summary.RolledBack != 0 || summary.Tripped != 0 || summary.ManualOverrides != 0 {
				t.Fatalf("expected grace observation at minute %d without tamper/rollback, got %+v", minute, summary)
			}
			for _, mon := range store.monitors {
				if mon.Status != ExecutionMonitorObserving {
					t.Fatalf("expected monitor %s OBSERVING during grace, got %s", mon.LoopKey, mon.Status)
				}
			}
		}

		// After the 10-minute convergence grace expires, both loops trip and roll back to baseline.
		at11 := base.Add(11 * time.Minute)
		syncDrillTelemetry(at11, telemetry, health, &baselineSupply)
		summary11, err := ctrl.RunMonitorPass(t.Context(), at11)
		if err != nil {
			t.Fatalf("RunMonitorPass at minute 11 failed: %v", err)
		}
		if summary11.RolledBack != 2 || summary11.Tripped != 0 {
			t.Fatalf("expected both loops rolled back cleanly at minute 11, got %+v", summary11)
		}
		if len(disp.submitInputs) != 4 {
			t.Fatalf("expected 4 total commands (2 initial + 2 rollback), got %d", len(disp.submitInputs))
		}
		sawPlantRollback := false
		sawPumpRollback := false
		for _, sub := range disp.submitInputs[2:] {
			if !sub.IsRollback {
				t.Fatalf("expected rollback command flag set, got %+v", sub)
			}
			if sub.Capability == commandmodel.CapabilitySetChilledWaterTemperatureSetpoint &&
				math.Abs(sub.Parameters[commandmodel.ParameterSetpointC]-7.0) < 1e-6 {
				sawPlantRollback = true
			}
			if sub.Capability == commandmodel.CapabilitySetFrequency &&
				math.Abs(sub.Parameters[commandmodel.ParameterFrequencyHz]-42.0) < 1e-6 {
				sawPumpRollback = true
			}
		}
		if !sawPlantRollback || !sawPumpRollback {
			t.Fatalf("expected rollbacks restoring 7.0C and 42.0Hz, got %+v", disp.submitInputs[2:])
		}
		for _, mon := range store.monitors {
			if mon.Status != ExecutionMonitorTrippedRolledBack || mon.TripReason == nil || *mon.TripReason != "setpoint_tracking_deviation" {
				t.Fatalf("expected TRIPPED_ROLLED_BACK(setpoint_tracking_deviation) on %s, got status=%s reason=%v", mon.LoopKey, mon.Status, mon.TripReason)
			}
		}
		if store.lease.Status != "TRIPPED" {
			t.Fatalf("expected lease status TRIPPED after tracking deviation rollback, got %s", store.lease.Status)
		}
		sawTrackingSample := false
		for _, sample := range fw.samples {
			if sample.SampleKind == NegativeSampleTrackingDeviation {
				sawTrackingSample = true
				break
			}
		}
		if !sawTrackingSample {
			t.Fatalf("expected NegativeSampleTrackingDeviation recorded in flywheel samples, got %+v", fw.samples)
		}
	})
}
