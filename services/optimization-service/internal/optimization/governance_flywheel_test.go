package optimization

import (
	"context"
	"encoding/json"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandauth"
)

type memoryFlywheelStore struct {
	mu      sync.Mutex
	ticks   []ControlTickRecord
	samples []NegativeSampleRecord
}

func (store *memoryFlywheelStore) RecordControlTick(_ context.Context, tick ControlTickRecord) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.ticks = append(store.ticks, tick)
	return nil
}

func (store *memoryFlywheelStore) RecordNegativeSample(_ context.Context, sample NegativeSampleRecord) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.samples = append(store.samples, sample)
	return nil
}

func (store *memoryFlywheelStore) ListControlTicks(_ context.Context, tenantID, siteID string, from, to time.Time) ([]ControlTickRecord, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	out := make([]ControlTickRecord, 0, len(store.ticks))
	for _, tick := range store.ticks {
		if tick.TenantID != tenantID || tick.SiteID != siteID {
			continue
		}
		if tick.TickAt.Before(from) || tick.TickAt.After(to) {
			continue
		}
		out = append(out, tick)
	}
	return out, nil
}

func (store *memoryFlywheelStore) ListNegativeSamples(_ context.Context, tenantID, siteID string, from, to time.Time, limit int) ([]NegativeSampleRecord, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	out := make([]NegativeSampleRecord, 0, len(store.samples))
	for _, sample := range store.samples {
		if sample.TenantID != tenantID || sample.SiteID != siteID {
			continue
		}
		if sample.OccurredAt.Before(from) || sample.OccurredAt.After(to) {
			continue
		}
		out = append(out, sample)
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

type stubGovernancePublicationStore struct {
	memoryEvaluationStore
	visible bool
}

func (store *stubGovernancePublicationStore) CheckSiteVisible(_ context.Context, _, _ string) (bool, error) {
	return store.visible, nil
}

func TestAutoControllerTryDispatchControlTicksAndNegativeSamples(t *testing.T) {
	now := time.Date(2026, 11, 16, 6, 0, 0, 0, time.UTC)

	newCtrl := func(store *fakeAutoControlStore, health *fakePointHealthChecker, obs AutoControlLiveObservation, fw *memoryFlywheelStore, servingRef string) *AutoController {
		t.Helper()
		disp := &fakeDispatcher{
			decision:  commandauth.Decision{Allowed: true, ReasonCode: "GRANTED"},
			grant:     "grant-token",
			commandID: "01990000-7777-7000-8000-000000000001",
		}
		ctrl, err := NewAutoController(store, &fakeTelemetryReader{obs: obs}, disp, func() time.Time { return now }, nil)
		if err != nil {
			t.Fatal(err)
		}
		ctrl.WithPointHealth(health)
		ctrl.WithFlywheelStore(fw)
		ctrl.WithSolverResolver(&stubActiveSolverResolver{
			selection: ActiveSolverSelection{
				ServingRef: servingRef,
				Source:     SolverSourceSiteLoopCanary,
				LoopKey:    DefaultSolverLoopKey,
			},
		})
		return ctrl
	}

	t.Run("AI_CLOSED_LOOP records live COP metrics and skips duplicate ALREADY_DISPATCHED retry", func(t *testing.T) {
		fw := &memoryFlywheelStore{}
		store := &fakeAutoControlStore{
			lease:   testAutoLease(now),
			siteCtx: testAutoSiteCtx(),
		}
		obs := testLiveObservation(now, 7.1, 23.0)
		obs.OutdoorWetBulbC = float64Ptr(24.5)
		obs.CoolingLoadKW = float64Ptr(1000.0)
		obs.ChillerPowerKW = float64Ptr(200.0)

		ctrl := newCtrl(store, &fakePointHealthChecker{livePoints: testLivePoints(now, 7.1, 7.0, 23.0)}, obs, fw, "https://ml.internal:8443/v1/solve?token=secret")
		rec := testAutoRecommendation(now.Add(-2 * time.Minute))
		out, err := ctrl.TryDispatch(t.Context(), rec, now)
		if err != nil {
			t.Fatal(err)
		}
		if !out.Dispatched || out.ExecutionMode != ExecutionModeClosedLoop {
			t.Fatalf("unexpected outcome: %+v", out)
		}

		// Retrying an already-dispatched recommendation must NOT insert a second tick.
		rec.CommandIntentID = out.CommandIntentID
		if retryOut, err := ctrl.TryDispatch(t.Context(), rec, now.Add(time.Minute)); err != nil || retryOut.SkipReason != "ALREADY_DISPATCHED" {
			t.Fatalf("unexpected retry outcome: %+v err=%v", retryOut, err)
		}

		if len(fw.ticks) != 1 || len(fw.samples) != 0 {
			t.Fatalf("ticks=%d samples=%d", len(fw.ticks), len(fw.samples))
		}
		tick := fw.ticks[0]
		if tick.ControlMode != ControlModeAIClosedLoop || tick.SolverServingRef != "https://ml.internal:8443/v1/solve" || tick.LoopKey != DefaultSolverLoopKey {
			t.Fatalf("tick mode/ref mismatch: %+v", tick)
		}
		if tick.RecommendationID == nil || *tick.RecommendationID != rec.ID || tick.DecisionID != out.DecisionID {
			t.Fatalf("tick correlation mismatch: %+v", tick)
		}
		if tick.RecommendedSupplyTempC == nil || *tick.RecommendedSupplyTempC != 8.0 || tick.AppliedSupplyTempC == nil || *tick.AppliedSupplyTempC != 8.0 {
			t.Fatalf("tick setpoints mismatch: rec=%v applied=%v", tick.RecommendedSupplyTempC, tick.AppliedSupplyTempC)
		}
		snap := tick.FeatureSnapshot
		if snap.OutdoorWetBulbC == nil || *snap.OutdoorWetBulbC != 24.5 ||
			snap.CoolingLoadKW == nil || *snap.CoolingLoadKW != 1000.0 ||
			snap.ChillerPowerKW == nil || *snap.ChillerPowerKW != 200.0 ||
			snap.ObservedCOP == nil || math.Abs(*snap.ObservedCOP-5.0) > 1e-6 {
			t.Fatalf("unexpected snapshot physical metrics: %+v", snap)
		}
	})

	t.Run("AI_SHADOW records AI_SHADOW tick with applied target", func(t *testing.T) {
		fw := &memoryFlywheelStore{}
		lease := testAutoLease(now)
		lease.ExecutionMode = ExecutionModeShadow
		store := &fakeAutoControlStore{lease: lease, siteCtx: testAutoSiteCtx()}
		obs := testLiveObservation(now, 7.1, 23.0)
		ctrl := newCtrl(store, &fakePointHealthChecker{livePoints: testLivePoints(now, 7.1, 7.0, 23.0)}, obs, fw, ChillerCOPBuiltinSolverRef)

		out, err := ctrl.TryDispatch(t.Context(), testAutoRecommendation(now.Add(-2*time.Minute)), now)
		if err != nil {
			t.Fatal(err)
		}
		if !out.ShadowRecorded || out.ExecutionMode != ExecutionModeShadow {
			t.Fatalf("unexpected shadow outcome: %+v", out)
		}
		if len(fw.ticks) != 1 || fw.ticks[0].ControlMode != ControlModeAIShadow {
			t.Fatalf("expected AI_SHADOW tick, got %+v", fw.ticks)
		}
		if fw.ticks[0].AppliedSupplyTempC == nil || *fw.ticks[0].AppliedSupplyTempC != 8.0 {
			t.Fatalf("expected shadow appliedSupplyTempC 8.0, got %v", fw.ticks[0].AppliedSupplyTempC)
		}
	})

	t.Run("MANUAL_CONFIRM and DEADBAND_SKIP record RULE_BASELINE without false negative samples", func(t *testing.T) {
		fw := &memoryFlywheelStore{}
		store := &fakeAutoControlStore{siteCtx: testAutoSiteCtx()}
		obs := testLiveObservation(now, 7.1, 23.0)
		obs.OutdoorWetBulbC = float64Ptr(23.0)
		obs.CoolingLoadKW = float64Ptr(800.0)
		obs.ChillerPowerKW = float64Ptr(200.0)

		ctrl := newCtrl(store, &fakePointHealthChecker{livePoints: testLivePoints(now, 7.1, 7.0, 23.0)}, obs, fw, DefaultBuiltinSolverRef)
		out, err := ctrl.TryDispatch(t.Context(), testAutoRecommendation(now.Add(-2*time.Minute)), now)
		if err != nil || out.SkipReason != "MANUAL_CONFIRM" {
			t.Fatalf("expected MANUAL_CONFIRM, got %+v err=%v", out, err)
		}
		if len(fw.ticks) != 1 || fw.ticks[0].ControlMode != ControlModeRuleBaseline {
			t.Fatalf("expected RULE_BASELINE tick, got %+v", fw.ticks)
		}
		if fw.ticks[0].FeatureSnapshot.ObservedCOP == nil || math.Abs(*fw.ticks[0].FeatureSnapshot.ObservedCOP-4.0) > 1e-6 {
			t.Fatalf("expected enriched COP 4.0 on MANUAL_CONFIRM baseline tick, got %+v", fw.ticks[0].FeatureSnapshot)
		}

		// DEADBAND_SKIP records RULE_BASELINE and ZERO negative samples.
		store.lease = testAutoLease(now)
		nearRec := testAutoRecommendation(now.Add(-2 * time.Minute))
		nearRec.Candidate.SupplyTempC = 7.1
		deadbandOut, err := ctrl.TryDispatch(t.Context(), nearRec, now)
		if err != nil || deadbandOut.SkipReason == "" {
			t.Fatalf("expected deadband skip, got %+v err=%v", deadbandOut, err)
		}
		if len(fw.ticks) != 2 || fw.ticks[1].ControlMode != ControlModeRuleBaseline || len(fw.samples) != 0 {
			t.Fatalf("ticks=%+v samples=%d", fw.ticks, len(fw.samples))
		}
	})

	t.Run("MANUAL_OVERRIDE sources record MANUAL_OVERRIDE ticks and veto samples where applicable", func(t *testing.T) {
		fw := &memoryFlywheelStore{}
		obs := testLiveObservation(now, 7.1, 23.0)

		// 1) Site manual mode -> SITE_CONTROL_EXIT_ACTIVE -> MANUAL_OVERRIDE tick.
		manualSiteCtx := testAutoSiteCtx()
		manualSiteCtx.SiteControlMode = SiteControlModeManualOverride
		storeSiteManual := &fakeAutoControlStore{lease: testAutoLease(now), siteCtx: manualSiteCtx}
		ctrlSite := newCtrl(storeSiteManual, &fakePointHealthChecker{livePoints: testLivePoints(now, 7.1, 7.0, 23.0)}, obs, fw, DefaultBuiltinSolverRef)
		if out, err := ctrlSite.TryDispatch(t.Context(), testAutoRecommendation(now.Add(-2*time.Minute)), now); err != nil || out.SkipReason != "SITE_CONTROL_EXIT_ACTIVE" {
			t.Fatalf("site manual mismatch: %+v err=%v", out, err)
		}
		if len(fw.ticks) != 1 || fw.ticks[0].ControlMode != ControlModeManualOverride {
			t.Fatalf("expected MANUAL_OVERRIDE tick for site manual mode, got %+v", fw.ticks)
		}

		// 2) Cabinet local manual -> guardrail veto -> MANUAL_OVERRIDE tick + GUARDRAIL_VETOED sample.
		localPts := mutateLivePoint(testLivePoints(now, 7.1, 7.0, 23.0), "local_remote_mode", func(pt *TelemetryLivePoint) {
			pt.TextValue = strPtr("LOCAL")
		})
		ctrlLocal := newCtrl(&fakeAutoControlStore{lease: testAutoLease(now), siteCtx: testAutoSiteCtx()}, &fakePointHealthChecker{livePoints: localPts}, obs, fw, DefaultBuiltinSolverRef)
		if out, err := ctrlLocal.TryDispatch(t.Context(), testAutoRecommendation(now.Add(-2*time.Minute)), now); err != nil || out.SkipReason != "device_fault_or_local_manual" {
			t.Fatalf("local manual mismatch: %+v err=%v", out, err)
		}
		if len(fw.ticks) != 2 || fw.ticks[1].ControlMode != ControlModeManualOverride || !fw.ticks[1].FeatureSnapshot.CabinetLocalManual {
			t.Fatalf("expected MANUAL_OVERRIDE tick with CabinetLocalManual=true, got %+v", fw.ticks[1])
		}
		if len(fw.samples) != 1 || fw.samples[0].SampleKind != NegativeSampleGuardrailVetoed || fw.samples[0].ReasonCode != "device_fault_or_local_manual" {
			t.Fatalf("expected GUARDRAIL_VETOED sample, got %+v", fw.samples)
		}

		// 3) Lease ManualBackoffUntil active -> MANUAL_OVERRIDE tick + GUARDRAIL_VETOED sample.
		backoffLease := testAutoLease(now)
		backoffUntil := now.Add(30 * time.Minute)
		backoffLease.ManualBackoffUntil = &backoffUntil
		ctrlBackoff := newCtrl(&fakeAutoControlStore{lease: backoffLease, siteCtx: testAutoSiteCtx()}, &fakePointHealthChecker{livePoints: testLivePoints(now, 7.1, 7.0, 23.0)}, obs, fw, DefaultBuiltinSolverRef)
		if out, err := ctrlBackoff.TryDispatch(t.Context(), testAutoRecommendation(now.Add(-2*time.Minute)), now); err != nil || out.SkipReason != "manual_override_backoff" {
			t.Fatalf("backoff mismatch: %+v err=%v", out, err)
		}
		if len(fw.ticks) != 3 || fw.ticks[2].ControlMode != ControlModeManualOverride {
			t.Fatalf("expected MANUAL_OVERRIDE tick on backoff, got %+v", fw.ticks[2])
		}
		if len(fw.samples) != 2 || fw.samples[1].SampleKind != NegativeSampleGuardrailVetoed || fw.samples[1].ReasonCode != "manual_override_backoff" {
			t.Fatalf("expected GUARDRAIL_VETOED sample on backoff, got %+v", fw.samples[1])
		}
	})

	t.Run("GUARDRAIL_CLAMPED and device-fault GUARDRAIL_VETOED emit negative samples with trace and solver", func(t *testing.T) {
		fw := &memoryFlywheelStore{}
		obs := testLiveObservation(now, 7.1, 23.0)
		obs.CoolingLoadKW = float64Ptr(900.0)
		obs.ChillerPowerKW = float64Ptr(200.0)
		obs.OutdoorWetBulbC = float64Ptr(25.0)

		// Clamped recommendation: candidate 10.5 clamped to 8.5 (step 1.5 from 7.0).
		store := &fakeAutoControlStore{lease: testAutoLease(now), siteCtx: testAutoSiteCtx()}
		ctrl := newCtrl(store, &fakePointHealthChecker{livePoints: testLivePoints(now, 7.1, 7.0, 23.0)}, obs, fw, "https://canary.internal:8443/solve")
		clampedRec := testAutoRecommendation(now.Add(-2 * time.Minute))
		clampedRec.Candidate.SupplyTempC = 10.5

		out, err := ctrl.TryDispatch(t.Context(), clampedRec, now)
		if err != nil || !out.Dispatched {
			t.Fatalf("expected clamped dispatch to proceed, out=%+v err=%v", out, err)
		}
		if len(fw.samples) != 1 || fw.samples[0].SampleKind != NegativeSampleGuardrailClamped {
			t.Fatalf("expected GUARDRAIL_CLAMPED sample, got %+v", fw.samples)
		}
		s1 := fw.samples[0]
		if s1.MonitorID == nil || *s1.MonitorID != out.MonitorID || s1.DecisionID != out.DecisionID || s1.TraceID != out.TraceID {
			t.Fatalf("clamped sample correlation mismatch: %+v", s1)
		}
		if s1.RawRecommendedValue == nil || *s1.RawRecommendedValue != 10.5 || s1.AppliedOrClampedValue == nil || *s1.AppliedOrClampedValue != 8.5 {
			t.Fatalf("clamped sample values mismatch: raw=%v clamped=%v", s1.RawRecommendedValue, s1.AppliedOrClampedValue)
		}
		if s1.SolverServingRef != "https://canary.internal:8443/solve" || len(s1.GuardrailTrace) == 0 || string(s1.GuardrailTrace) == "[]" {
			t.Fatalf("clamped sample missing solver or guardrail trace: %+v", s1)
		}

		// Device fault -> RULE_BASELINE tick + GUARDRAIL_VETOED sample.
		faultPts := mutateLivePoint(testLivePoints(now, 7.1, 7.0, 23.0), "fault_code", func(pt *TelemetryLivePoint) {
			pt.TextValue = strPtr("E101")
		})
		ctrlFault := newCtrl(&fakeAutoControlStore{lease: testAutoLease(now), siteCtx: testAutoSiteCtx()}, &fakePointHealthChecker{livePoints: faultPts}, obs, fw, "https://canary.internal:8443/solve")
		faultOut, err := ctrlFault.TryDispatch(t.Context(), testAutoRecommendation(now.Add(-2*time.Minute)), now)
		if err != nil || faultOut.SkipReason != "device_fault_or_local_manual" {
			t.Fatalf("expected fault veto, out=%+v err=%v", faultOut, err)
		}
		if len(fw.ticks) != 2 || fw.ticks[1].ControlMode != ControlModeRuleBaseline {
			t.Fatalf("expected RULE_BASELINE tick on device fault, got %+v", fw.ticks[1])
		}
		if len(fw.samples) != 2 || fw.samples[1].SampleKind != NegativeSampleGuardrailVetoed || !fw.samples[1].FeatureSnapshot.DeviceFaultActive {
			t.Fatalf("expected GUARDRAIL_VETOED sample with DeviceFaultActive=true, got %+v", fw.samples[1])
		}
	})
}

func TestAutoControllerRunMonitorPassNegativeSamples(t *testing.T) {
	now := time.Date(2026, 11, 16, 8, 0, 0, 0, time.UTC)

	newMonitor := func(id string, startedAt time.Time, applied float64) OptimizationExecutionMonitorRecord {
		restore := 7.0
		return OptimizationExecutionMonitorRecord{
			ID:                  id,
			TenantID:            "01990000-3000-7000-8000-000000000001",
			SiteID:              "01990000-5000-7000-8000-000000000001",
			LeaseID:             "01990000-8800-7000-8000-000000000011",
			RecommendationID:    "01990000-2000-7000-8000-000000000001",
			CommandIntentID:     "01990000-7777-7000-8000-000000000099",
			DeviceID:            "01990000-6000-7000-8000-000000000001",
			TargetCapability:    "set_chilled_water_temperature_setpoint",
			BaselineSupplyTempC: ptr(7.0),
			AppliedSupplyTempC:  ptr(applied),
			RestoreSupplyTempC:  &restore,
			ZoneTempMaxC:        26.0,
			SupplyTempMinC:      6.0,
			SupplyTempMaxC:      12.0,
			GuardrailVerdict:    "PASS",
			GuardrailTrace:      json.RawMessage(`[{"stage":"deadband","action":"PASS"}]`),
			StartedAt:           startedAt,
			WindowEndsAt:        startedAt.Add(30 * time.Minute),
			Status:              ExecutionMonitorObserving,
			ExecutionMode:       ExecutionModeClosedLoop,
			DecisionID:          "01990000-9999-7000-8000-000000000011",
			TraceID:             "4bf92f3577b34da6a3ce929d0e0e4736",
		}
	}

	runCase := func(t *testing.T, mon OptimizationExecutionMonitorRecord, points []TelemetryLivePoint, wantKind string) NegativeSampleRecord {
		t.Helper()
		fw := &memoryFlywheelStore{}
		store := &fakeAutoControlStore{
			lease:    testAutoLease(now),
			siteCtx:  testAutoSiteCtx(),
			monitors: []OptimizationExecutionMonitorRecord{mon},
		}
		obs := testLiveObservation(now, 8.0, 23.0)
		obs.OutdoorWetBulbC = float64Ptr(24.0)
		obs.CoolingLoadKW = float64Ptr(840.0)
		obs.ChillerPowerKW = float64Ptr(210.0)

		disp := &fakeDispatcher{
			decision:  commandauth.Decision{Allowed: true, ReasonCode: "GRANTED"},
			grant:     "rollback-grant",
			commandID: "01990000-7777-7000-8000-000000000055",
		}
		ctrl, err := NewAutoController(store, &fakeTelemetryReader{obs: obs}, disp, func() time.Time { return now }, nil)
		if err != nil {
			t.Fatal(err)
		}
		ctrl.WithPointHealth(&fakePointHealthChecker{livePoints: points})
		ctrl.WithFlywheelStore(fw)
		ctrl.WithSolverResolver(&stubActiveSolverResolver{
			selection: ActiveSolverSelection{ServingRef: "https://ml.internal:8443/v1/solve", Source: SolverSourceSiteLoopCanary, LoopKey: DefaultSolverLoopKey},
		})
		if _, err := ctrl.RunMonitorPass(t.Context(), now); err != nil {
			t.Fatal(err)
		}
		if len(fw.samples) != 1 || fw.samples[0].SampleKind != wantKind {
			t.Fatalf("expected negative sample %s, got %+v", wantKind, fw.samples)
		}
		sample := fw.samples[0]
		if sample.DecisionID != mon.DecisionID || sample.TraceID != mon.TraceID || sample.SolverServingRef != "https://ml.internal:8443/v1/solve" {
			t.Fatalf("sample metadata mismatch: %+v", sample)
		}
		if sample.MonitorID == nil || *sample.MonitorID != mon.ID {
			t.Fatalf("expected monitorId %s, got %v", mon.ID, sample.MonitorID)
		}
		return sample
	}

	t.Run("MANUAL_OVERRIDE from cabinet setpoint tamper", func(t *testing.T) {
		mon := newMonitor("01990000-3333-7000-8000-000000000001", now.Add(-5*time.Minute), 8.0)
		// Applied=8.0, Restore=7.0, Live=6.5 -> tamper (>0.35C from applied, not equal to restore).
		sample := runCase(t, mon, testLivePoints(now, 8.0, 6.5, 23.0), NegativeSampleManualOverride)
		if sample.ReasonCode != "cabinet_setpoint_manual_override" {
			t.Fatalf("reasonCode=%q", sample.ReasonCode)
		}
	})

	t.Run("TRACKING_DEVIATION after 10m convergence grace", func(t *testing.T) {
		mon := newMonitor("01990000-3333-7000-8000-000000000002", now.Add(-15*time.Minute), 8.0)
		// Live setpoint=7.67 -> 0.33C deviation (>0.30C tracking threshold, <=0.35C tamper threshold).
		sample := runCase(t, mon, testLivePoints(now, 8.0, 7.67, 23.0), NegativeSampleTrackingDeviation)
		if sample.FeatureSnapshot.ObservedCOP == nil || math.Abs(*sample.FeatureSnapshot.ObservedCOP-4.0) > 1e-6 {
			t.Fatalf("expected COP 4.0 in sample snapshot, got %+v", sample.FeatureSnapshot)
		}
	})

	t.Run("MONITOR_TRIPPED on zone limit violation", func(t *testing.T) {
		mon := newMonitor("01990000-3333-7000-8000-000000000003", now.Add(-5*time.Minute), 8.0)
		sample := runCase(t, mon, testLivePoints(now, 8.0, 8.0, 26.8), NegativeSampleMonitorTripped)
		if sample.ReasonCode == "" {
			t.Fatal("expected non-empty reasonCode for tripped monitor")
		}
	})
}

func TestServiceGovernanceSummaryBivariateCOPAndFlywheel(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	tenantID := "01990000-3000-7000-8000-000000000001"
	siteID := "01990000-5000-7000-8000-000000000001"

	fw := &memoryFlywheelStore{
		ticks: []ControlTickRecord{
			// Bin (24..26 C, 600..800 kW): baseline COP 4.0, AI COP 4.4 -> +10%, saved 8.25 kWh
			{
				ID:          "t1",
				TenantID:    tenantID,
				SiteID:      siteID,
				ControlMode: ControlModeRuleBaseline,
				FeatureSnapshot: OptimizationFeatureSnapshot{
					OutdoorWetBulbC: ptr(24.5),
					CoolingLoadKW:   ptr(700.0),
					ChillerPowerKW:  ptr(175.0),
				},
				TickAt: now.Add(-48 * time.Hour),
			},
			{
				ID:          "t2",
				TenantID:    tenantID,
				SiteID:      siteID,
				ControlMode: ControlModeAIClosedLoop,
				FeatureSnapshot: OptimizationFeatureSnapshot{
					OutdoorWetBulbC: ptr(25.1),
					CoolingLoadKW:   ptr(792.0),
					ChillerPowerKW:  ptr(180.0),
				},
				TickAt: now.Add(-24 * time.Hour),
			},
			{
				ID:          "t3",
				TenantID:    tenantID,
				SiteID:      siteID,
				ControlMode: ControlModeAIClosedLoop,
				FeatureSnapshot: OptimizationFeatureSnapshot{
					OutdoorWetBulbC: ptr(25.3),
					CoolingLoadKW:   ptr(660.0),
					ChillerPowerKW:  ptr(150.0),
				},
				TickAt: now.Add(-12 * time.Hour),
			},
			// Unmatched bin (26..28 C, 800..1000 kW): excluded from matched bivariate cells
			{
				ID:          "t4",
				TenantID:    tenantID,
				SiteID:      siteID,
				ControlMode: ControlModeRuleBaseline,
				FeatureSnapshot: OptimizationFeatureSnapshot{
					OutdoorWetBulbC: ptr(26.2),
					CoolingLoadKW:   ptr(900.0),
					ChillerPowerKW:  ptr(200.0),
				},
				TickAt: now.Add(-6 * time.Hour),
			},
			// Shadow + manual override + out-of-window tick
			{ID: "t5", TenantID: tenantID, SiteID: siteID, ControlMode: ControlModeAIShadow, TickAt: now.Add(-2 * time.Hour)},
			{ID: "t6", TenantID: tenantID, SiteID: siteID, ControlMode: ControlModeManualOverride, TickAt: now.Add(-1 * time.Hour)},
			{ID: "old", TenantID: tenantID, SiteID: siteID, ControlMode: ControlModeRuleBaseline, TickAt: now.AddDate(0, 0, -20)},
		},
		samples: []NegativeSampleRecord{
			{ID: "s1", TenantID: tenantID, SiteID: siteID, SampleKind: NegativeSampleGuardrailClamped, OccurredAt: now.Add(-5 * time.Hour)},
			{ID: "s2", TenantID: tenantID, SiteID: siteID, SampleKind: NegativeSampleGuardrailVetoed, OccurredAt: now.Add(-4 * time.Hour)},
			{ID: "s3", TenantID: tenantID, SiteID: siteID, SampleKind: NegativeSampleManualOverride, OccurredAt: now.Add(-3 * time.Hour)},
			{ID: "s4", TenantID: tenantID, SiteID: siteID, SampleKind: NegativeSampleTrackingDeviation, OccurredAt: now.Add(-2 * time.Hour)},
			{ID: "s5", TenantID: tenantID, SiteID: siteID, SampleKind: NegativeSampleMonitorTripped, OccurredAt: now.Add(-1 * time.Hour)},
		},
	}

	svc, err := NewService(HVACRecommendationSolver{}, &stubGovernancePublicationStore{visible: true}, &captureEvaluationSink{}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	svc.WithGovernanceFlywheel(fw).WithSolverResolver(&stubActiveSolverResolver{
		selection: ActiveSolverSelection{
			ServingRef: "https://ml.internal:8443/v1/solve",
			Source:     SolverSourceSiteLoopCanary,
			LoopKey:    DefaultSolverLoopKey,
		},
	})

	summary, err := svc.GovernanceSummary(t.Context(), tenantID, siteID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if summary.WindowDays != DefaultGovernanceWindowDays {
		t.Fatalf("windowDays=%d, want 14", summary.WindowDays)
	}
	if summary.ActiveSolver.ServingRef != "https://ml.internal:8443/v1/solve" || summary.ActiveSolver.Source != SolverSourceSiteLoopCanary {
		t.Fatalf("activeSolver=%+v", summary.ActiveSolver)
	}
	if summary.TickCounts != (GovernanceTickCounts{AIClosedLoop: 2, AIShadow: 1, RuleBaseline: 2, ManualOverride: 1}) {
		t.Fatalf("tickCounts=%+v", summary.TickCounts)
	}
	cop := summary.COPComparison
	if cop.MatchedBinCount != 1 || cop.MatchedBaselineSamples != 1 || cop.MatchedAISamples != 2 {
		t.Fatalf("copComparison bins mismatch: %+v", cop)
	}
	if cop.WeightedBaselineCOP == nil || math.Abs(*cop.WeightedBaselineCOP-4.0) > 1e-4 {
		t.Fatalf("weightedBaselineCOP=%v, want 4.0", cop.WeightedBaselineCOP)
	}
	if cop.WeightedAICOP == nil || math.Abs(*cop.WeightedAICOP-4.4) > 1e-4 {
		t.Fatalf("weightedAICOP=%v, want 4.4", cop.WeightedAICOP)
	}
	if cop.WeightedCOPUpliftPct == nil || math.Abs(*cop.WeightedCOPUpliftPct-10.0) > 1e-2 {
		t.Fatalf("weightedCopUpliftPct=%v, want 10.0", cop.WeightedCOPUpliftPct)
	}
	if math.Abs(cop.TotalEstimatedSavedKWh-8.25) > 1e-4 {
		t.Fatalf("totalEstimatedSavedKWh=%v, want 8.25", cop.TotalEstimatedSavedKWh)
	}
	if len(cop.Cells) != 1 || cop.Cells[0].WetBulbMinC != 24.0 || cop.Cells[0].CoolingLoadMinKW != 600.0 {
		t.Fatalf("cells mismatch: %+v", cop.Cells)
	}
	if summary.Flywheel != (GovernanceFlywheelSummary{
		TotalSamples:      5,
		GuardrailClamped:  1,
		GuardrailVetoed:   1,
		ManualOverride:    1,
		MonitorTripped:    1,
		TrackingDeviation: 1,
	}) {
		t.Fatalf("flywheel summary mismatch: %+v", summary.Flywheel)
	}

	// Verify serialized JSON exactly matches OpenAPI additionalProperties:false schema (including deploymentRevisionId:null).
	payload, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	activeSolverMap, _ := decoded["activeSolver"].(map[string]any)
	if _, hasRev := activeSolverMap["deploymentRevisionId"]; !hasRev {
		t.Fatalf("activeSolver missing nullable deploymentRevisionId: %s", string(payload))
	}
	tickCountsMap, _ := decoded["tickCounts"].(map[string]any)
	if _, hasExtra := tickCountsMap["total"]; hasExtra {
		t.Fatalf("tickCounts must not emit extra 'total' field: %s", string(payload))
	}

	// Non-visible site returns ErrOptimizationNotFound.
	hiddenSvc, err := NewService(HVACRecommendationSolver{}, &stubGovernancePublicationStore{visible: false}, &captureEvaluationSink{}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hiddenSvc.GovernanceSummary(t.Context(), tenantID, siteID, 14); err != ErrOptimizationNotFound {
		t.Fatalf("expected ErrOptimizationNotFound for invisible site, got %v", err)
	}
}
