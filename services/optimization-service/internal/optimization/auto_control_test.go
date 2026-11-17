package optimization

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandauth"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/intelligencemodel"
)

type fakePointHealthChecker struct {
	mu  sync.Mutex
	err error
}

func (checker *fakePointHealthChecker) setErr(err error) {
	checker.mu.Lock()
	defer checker.mu.Unlock()
	checker.err = err
}

func (checker *fakePointHealthChecker) CheckTelemetryPointHealth(context.Context, string, string, []string, []string) error {
	checker.mu.Lock()
	defer checker.mu.Unlock()
	return checker.err
}

type fakeAutoControlStore struct {
	mu               sync.Mutex
	lease            *AutoControlLeaseRecord
	leaseErr         error
	siteCtx          AutoControlSiteContext
	siteCtxErr       error
	claimHook        func(claimOwner string)
	dispatchedRecs   []Recommendation
	monitors         []OptimizationExecutionMonitorRecord
	claimCalls       int
	releaseCalls     int
	heartbeatCalls   int
	unhealthyCalls   int
	verifiedCalls    int
	trippedRecords   []tripRecordCall
	recordMonitorSeq int
}

type tripRecordCall struct {
	MonitorID               string
	ClaimOwner              string
	NewStatus               string
	TripReason              string
	RollbackCommandIntentID *string
	TrippedAt               time.Time
}

func (store *fakeAutoControlStore) ActiveAutoControlLease(context.Context, string, string, time.Time) (*AutoControlLeaseRecord, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.lease, store.leaseErr
}

func (store *fakeAutoControlStore) LoadAutoControlSiteContext(context.Context, string, string, string, time.Time) (AutoControlSiteContext, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.siteCtx, store.siteCtxErr
}

func (store *fakeAutoControlStore) RecordAutomatedDispatch(
	_ context.Context,
	recommendation Recommendation,
	leaseID string,
	target SetpointCommandTarget,
	commandIntentID string,
	baselineSupplyC, appliedSupplyC, restoreSupplyC, zoneMaxC, supplyMaxC float64,
	at time.Time,
) (OptimizationExecutionMonitorRecord, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.dispatchedRecs = append(store.dispatchedRecs, recommendation)
	store.recordMonitorSeq++
	monitorID := "01990000-9900-7000-8000-000000000001"
	if store.recordMonitorSeq > 1 {
		monitorID = "01990000-9900-7000-8000-000000000002"
	}
	mon := OptimizationExecutionMonitorRecord{
		ID:                  monitorID,
		TenantID:            recommendation.TenantID,
		SiteID:              recommendation.SiteID,
		LeaseID:             leaseID,
		RecommendationID:    recommendation.ID,
		CommandIntentID:     commandIntentID,
		DeviceID:            target.DeviceID,
		TargetCapability:    string(commandmodel.CapabilitySetChilledWaterTemperatureSetpoint),
		BaselineSupplyTempC: &baselineSupplyC,
		AppliedSupplyTempC:  &appliedSupplyC,
		RestoreSupplyTempC:  &restoreSupplyC,
		ZoneTempMaxC:        zoneMaxC,
		SupplyTempMaxC:      supplyMaxC,
		StartedAt:           at.UTC(),
		WindowEndsAt:        at.UTC().Add(DefaultExecutionMonitorWindow),
		Status:              ExecutionMonitorObserving,
	}
	store.monitors = append(store.monitors, mon)
	return mon, nil
}

func (store *fakeAutoControlStore) ListObservingExecutionMonitors(_ context.Context, limit int) ([]OptimizationExecutionMonitorRecord, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	out := make([]OptimizationExecutionMonitorRecord, 0, len(store.monitors))
	for _, mon := range store.monitors {
		if mon.Status == ExecutionMonitorObserving {
			out = append(out, mon)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (store *fakeAutoControlStore) ClaimObservingExecutionMonitors(_ context.Context, claimOwner string, leaseDuration time.Duration, now time.Time, limit int) ([]OptimizationExecutionMonitorRecord, error) {
	owner := strings.TrimSpace(claimOwner)
	if owner == "" || leaseDuration <= 0 {
		return nil, errors.New("execution monitor claim owner and positive lease duration are required")
	}
	nowUTC := now.UTC()
	until := nowUTC.Add(leaseDuration)
	store.mu.Lock()
	store.claimCalls++
	hook := store.claimHook
	out := make([]OptimizationExecutionMonitorRecord, 0, len(store.monitors))
	for idx := range store.monitors {
		mon := &store.monitors[idx]
		if mon.Status != ExecutionMonitorObserving {
			continue
		}
		if mon.ClaimUntil != nil && mon.ClaimUntil.After(nowUTC) {
			continue
		}
		ownerCopy := owner
		untilCopy := until
		mon.ClaimOwner = &ownerCopy
		mon.ClaimUntil = &untilCopy
		out = append(out, *mon)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	store.mu.Unlock()
	if hook != nil {
		hook(owner)
	}
	return out, nil
}

func matchesClaimOwnership(mon OptimizationExecutionMonitorRecord, claimOwner string) bool {
	if mon.Status != ExecutionMonitorObserving {
		return false
	}
	if mon.ClaimOwner == nil {
		return true
	}
	return *mon.ClaimOwner == strings.TrimSpace(claimOwner)
}

func (store *fakeAutoControlStore) ReleaseMonitorClaim(_ context.Context, monitorID, _, _, claimOwner string, _ time.Time) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.releaseCalls++
	for idx := range store.monitors {
		if store.monitors[idx].ID != monitorID {
			continue
		}
		if !matchesClaimOwnership(store.monitors[idx], claimOwner) {
			return ErrExecutionMonitorClaimLost
		}
		store.monitors[idx].ClaimOwner = nil
		store.monitors[idx].ClaimUntil = nil
		return nil
	}
	return ErrExecutionMonitorClaimLost
}

func (store *fakeAutoControlStore) RecordMonitorHealthyHeartbeat(_ context.Context, monitorID, _, _, claimOwner string, checkedAt time.Time) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.heartbeatCalls++
	for idx := range store.monitors {
		if store.monitors[idx].ID != monitorID {
			continue
		}
		if !matchesClaimOwnership(store.monitors[idx], claimOwner) {
			return ErrExecutionMonitorClaimLost
		}
		t := checkedAt.UTC()
		store.monitors[idx].TelemetryUnhealthySince = nil
		store.monitors[idx].LastCheckedAt = &t
		store.monitors[idx].ClaimOwner = nil
		store.monitors[idx].ClaimUntil = nil
		return nil
	}
	return ErrExecutionMonitorClaimLost
}

func (store *fakeAutoControlStore) RecordMonitorTelemetryUnhealthy(_ context.Context, monitorID, _, _, claimOwner string, unhealthySince, checkedAt time.Time) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.unhealthyCalls++
	for idx := range store.monitors {
		if store.monitors[idx].ID != monitorID {
			continue
		}
		if !matchesClaimOwnership(store.monitors[idx], claimOwner) {
			return ErrExecutionMonitorClaimLost
		}
		if store.monitors[idx].TelemetryUnhealthySince == nil {
			u := unhealthySince.UTC()
			store.monitors[idx].TelemetryUnhealthySince = &u
		}
		c := checkedAt.UTC()
		store.monitors[idx].LastCheckedAt = &c
		store.monitors[idx].ClaimOwner = nil
		store.monitors[idx].ClaimUntil = nil
		return nil
	}
	return ErrExecutionMonitorClaimLost
}

func (store *fakeAutoControlStore) CompleteMonitorVerified(_ context.Context, monitorID, _, _, claimOwner string, checkedAt time.Time) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.verifiedCalls++
	for idx := range store.monitors {
		if store.monitors[idx].ID != monitorID {
			continue
		}
		if !matchesClaimOwnership(store.monitors[idx], claimOwner) {
			return ErrExecutionMonitorClaimLost
		}
		c := checkedAt.UTC()
		store.monitors[idx].Status = ExecutionMonitorVerified
		store.monitors[idx].TelemetryUnhealthySince = nil
		store.monitors[idx].LastCheckedAt = &c
		store.monitors[idx].ClaimOwner = nil
		store.monitors[idx].ClaimUntil = nil
		return nil
	}
	return ErrExecutionMonitorClaimLost
}

func (store *fakeAutoControlStore) TripMonitorAndLease(_ context.Context, monitor OptimizationExecutionMonitorRecord, claimOwner, newStatus, tripReason string, rollbackCommandIntentID *string, trippedAt time.Time) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	for idx := range store.monitors {
		if store.monitors[idx].ID != monitor.ID {
			continue
		}
		if !matchesClaimOwnership(store.monitors[idx], claimOwner) {
			return ErrExecutionMonitorClaimLost
		}
		store.trippedRecords = append(store.trippedRecords, tripRecordCall{
			MonitorID:               monitor.ID,
			ClaimOwner:              claimOwner,
			NewStatus:               newStatus,
			TripReason:              tripReason,
			RollbackCommandIntentID: rollbackCommandIntentID,
			TrippedAt:               trippedAt.UTC(),
		})
		if store.lease != nil {
			store.lease.Status = "TRIPPED"
		}
		store.monitors[idx].Status = newStatus
		r := tripReason
		store.monitors[idx].TripReason = &r
		store.monitors[idx].RollbackCommandIntentID = rollbackCommandIntentID
		t := trippedAt.UTC()
		store.monitors[idx].LastCheckedAt = &t
		store.monitors[idx].ClaimOwner = nil
		store.monitors[idx].ClaimUntil = nil
		return nil
	}
	return ErrExecutionMonitorClaimLost
}

type fakeTelemetryReader struct {
	obs AutoControlLiveObservation
	err error
}

func (reader *fakeTelemetryReader) ReadLiveAutoControlObservation(context.Context, string, string, InputMapping, StagingTopology, time.Time) (AutoControlLiveObservation, error) {
	return reader.obs, reader.err
}

type fakeDispatcher struct {
	mu           sync.Mutex
	grantReqs    []commandauth.WorkloadDecisionRequest
	submitInputs []AutoControlDispatchInput
	decision     commandauth.Decision
	grant        string
	commandID    string
	grantErr     error
	submitErr    error
}

func (disp *fakeDispatcher) RequestCommandGrant(_ context.Context, req commandauth.WorkloadDecisionRequest) (string, commandauth.Decision, error) {
	disp.mu.Lock()
	defer disp.mu.Unlock()
	disp.grantReqs = append(disp.grantReqs, req)
	return disp.grant, disp.decision, disp.grantErr
}

func (disp *fakeDispatcher) SubmitAutoCommand(_ context.Context, _ string, input AutoControlDispatchInput) (string, error) {
	disp.mu.Lock()
	defer disp.mu.Unlock()
	disp.submitInputs = append(disp.submitInputs, input)
	return disp.commandID, disp.submitErr
}

func testAutoLease(now time.Time) *AutoControlLeaseRecord {
	return &AutoControlLeaseRecord{
		ID:                  "01990000-8800-7000-8000-000000000011",
		TenantID:            "01990000-3000-7000-8000-000000000001",
		SiteID:              "01990000-5000-7000-8000-000000000001",
		Status:              "ACTIVE",
		ArmedBy:             "01990000-1000-7000-8000-000000000001",
		ArmedAt:             now.Add(-10 * time.Minute),
		ExpiresAt:           now.Add(4 * time.Hour),
		AllowedCapabilities: []string{string(commandmodel.CapabilitySetChilledWaterTemperatureSetpoint)},
		MaxSetpointStepC:    1.5,
		SupplyTempMinC:      6.0,
		SupplyTempMaxC:      12.0,
		ZoneTempMaxC:        26.0,
		MinRunningChillers:  1,
		MaxCommandsPerHour:  2,
		Revision:            1,
	}
}

func testAutoSiteCtx() AutoControlSiteContext {
	return AutoControlSiteContext{
		SiteControlMode: SiteControlModeRemoteAuto,
		InputMapping: InputMapping{
			SupplyTemperatureKey: "supply_water_temperature",
			ZoneTemperatureKeys:  []string{"zone_temperature_a"},
		},
		SetpointTarget: &SetpointCommandTarget{
			DeviceID:         "01990000-6000-7000-8000-000000000001",
			PointID:          "01990000-7000-7000-8000-000000000001",
			PointCode:        "chw_sp",
			FeedbackPointKey: "chilled_water_supply_temp_sp",
			AssetID:          "01990000-4000-7000-8000-000000000001",
			SiteControlMode:  SiteControlModeRemoteAuto,
		},
		Topology: StagingTopology{SiteControlMode: SiteControlModeRemoteAuto},
	}
}

func testLiveObservation(now time.Time, supplyC, zoneC float64) AutoControlLiveObservation {
	return AutoControlLiveObservation{
		SupplyTemp: &TelemetryEvidence{
			PointRevision: 42,
			SampledAt:     now.Add(-30 * time.Second),
			Value:         supplyC,
			Quality:       "GOOD",
		},
		ZoneTemps: map[string]TelemetryEvidence{
			"zone_temperature_a": {
				PointRevision: 12,
				SampledAt:     now.Add(-30 * time.Second),
				Value:         zoneC,
				Quality:       "GOOD",
			},
		},
		RunningChillers: 2,
		ChillersHealthy: true,
	}
}

func testAutoRecommendation(createdAt time.Time) Recommendation {
	validUntil := createdAt.Add(intelligencemodel.DefaultRecommendationTTL)
	return Recommendation{
		ID:                 "01990000-2000-7000-8000-000000000001",
		TenantID:           "01990000-3000-7000-8000-000000000001",
		SiteID:             "01990000-5000-7000-8000-000000000001",
		InputSnapshotID:    "01990000-1930-7000-8000-000000000001",
		DeploymentRevision: "01990000-1960-7000-8000-000000000001",
		Approval:           intelligencemodel.RecommendationDraft,
		Baseline: intelligencemodel.RecommendationBaseline{
			DailyEnergyKWh: 2400, DailyCost: 360, SupplyTempC: 7.0, ZoneTempC: 23.0,
		},
		Objective: intelligencemodel.RecommendationObjective{Kind: "COST"},
		Constraints: []intelligencemodel.RecommendationConstraint{
			{Kind: "SUPPLY_TEMP", MinC: float64Ptr(6.0)},
		},
		Candidate: intelligencemodel.RecommendationCandidate{
			SupplyTempC: 8.0, ProjectedZoneTempC: 23.3, DailyEnergyKWh: 2220, DailyCost: 333,
		},
		ExpectedImpact:   intelligencemodel.RecommendationExpectedImpact{EnergySavingKWhPerDay: 180.0, CostSavingPerDay: 27.0},
		Uncertainty:      intelligencemodel.RecommendationUncertainty{EnergyP90KWh: 60.0, ZoneTempP90C: 0.2},
		Risk:             intelligencemodel.RecommendationRisk{Level: "LOW", ComfortMarginC: 1.5},
		RollbackPlan:     intelligencemodel.RecommendationRollbackPlan{RestoreSupplyTempC: float64Ptr(7.0)},
		VerificationPlan: intelligencemodel.RecommendationVerificationPlan{WindowMinutes: 30},
		CreatedAt:        createdAt,
		ValidUntil:       &validUntil,
	}
}

func TestAutoControllerTryDispatchPreconditionsAndHappyPath(t *testing.T) {
	now := time.Date(2026, 11, 16, 4, 0, 0, 0, time.UTC)

	t.Run("default MANUAL_CONFIRM skips when no active lease", func(t *testing.T) {
		store := &fakeAutoControlStore{siteCtx: testAutoSiteCtx()}
		disp := &fakeDispatcher{}
		ctrl, err := NewAutoController(store, &fakeTelemetryReader{}, disp, func() time.Time { return now }, nil)
		if err != nil {
			t.Fatal(err)
		}
		ctrl.WithPointHealth(&fakePointHealthChecker{})
		out, err := ctrl.TryDispatch(context.Background(), testAutoRecommendation(now.Add(-5*time.Minute)), now)
		if err != nil {
			t.Fatal(err)
		}
		if out.Dispatched || out.SkipReason != "MANUAL_CONFIRM" || len(disp.submitInputs) != 0 {
			t.Fatalf("unexpected outcome: %+v", out)
		}
	})

	cases := []struct {
		name       string
		mutateRec  func(*Recommendation)
		mutateCtx  func(*AutoControlSiteContext)
		mutateObs  func(*AutoControlLiveObservation)
		healthErr  error
		wantReason string
	}{
		{
			name: "expired 65m recommendation rejected",
			mutateRec: func(rec *Recommendation) {
				expired := now.Add(-time.Second)
				rec.ValidUntil = &expired
			},
			wantReason: "RECOMMENDATION_EXPIRED",
		},
		{
			name: "supply drift above 0.5C rejected",
			mutateObs: func(obs *AutoControlLiveObservation) {
				obs.SupplyTemp.Value = 7.6
			},
			wantReason: "SUPPLY_TEMP_DRIFT_EXCEEDED",
		},
		{
			name: "chiller rated power above 90 percent rejected",
			mutateObs: func(obs *AutoControlLiveObservation) {
				obs.ChillerPowerTrip = &ChillerPowerViolation{AssetCode: "chiller-a", PowerKW: 120, RatedPowerKW: 127.5}
			},
			wantReason: "CHILLER_POWER_RATIO_EXCEEDED",
		},
		{
			name: "rate limit reached rejected",
			mutateCtx: func(ctx *AutoControlSiteContext) {
				ctx.RecentAutoCountHour = 2
			},
			wantReason: "RATE_LIMIT_REACHED",
		},
		{
			name: "asset lock rejected",
			mutateCtx: func(ctx *AutoControlSiteContext) {
				ctx.SetpointTarget.AssetLocked = true
			},
			wantReason: "ASSET_CONTROL_LOCKED",
		},
		{
			name: "site exit mode rejected",
			mutateCtx: func(ctx *AutoControlSiteContext) {
				ctx.SiteControlMode = "MANUAL_OVERRIDE"
			},
			wantReason: "SITE_CONTROL_EXIT_ACTIVE",
		},
		{
			name:       "unhealthy telemetry rejected",
			healthErr:  errors.New("PROBE_OFFSET"),
			wantReason: "TELEMETRY_UNHEALTHY",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := testAutoRecommendation(now.Add(-5 * time.Minute))
			siteCtx := testAutoSiteCtx()
			obs := testLiveObservation(now, 7.2, 23.0)
			if tc.mutateRec != nil {
				tc.mutateRec(&rec)
			}
			if tc.mutateCtx != nil {
				tc.mutateCtx(&siteCtx)
			}
			if tc.mutateObs != nil {
				tc.mutateObs(&obs)
			}
			store := &fakeAutoControlStore{
				lease:   testAutoLease(now),
				siteCtx: siteCtx,
			}
			disp := &fakeDispatcher{}
			ctrl, err := NewAutoController(store, &fakeTelemetryReader{obs: obs}, disp, func() time.Time { return now }, nil)
			if err != nil {
				t.Fatal(err)
			}
			ctrl.WithPointHealth(&fakePointHealthChecker{err: tc.healthErr})
			out, err := ctrl.TryDispatch(context.Background(), rec, now)
			if err != nil {
				t.Fatal(err)
			}
			if out.Dispatched || out.SkipReason != tc.wantReason {
				t.Fatalf("got outcome=%+v want SkipReason=%s", out, tc.wantReason)
			}
			if len(disp.submitInputs) != 0 {
				t.Fatalf("expected no submitted commands on %s", tc.wantReason)
			}
		})
	}

	t.Run("happy path revalidates, approves, dispatches auto-opt and starts 30m monitor", func(t *testing.T) {
		store := &fakeAutoControlStore{
			lease:   testAutoLease(now),
			siteCtx: testAutoSiteCtx(),
		}
		obs := testLiveObservation(now, 7.2, 23.5)
		disp := &fakeDispatcher{
			decision:  commandauth.Decision{Allowed: true, ReasonCode: commandauth.ReasonAllowAutoLease},
			grant:     "header.sig",
			commandID: "01990000-7700-7000-8000-000000000099",
		}
		ctrl, err := NewAutoController(store, &fakeTelemetryReader{obs: obs}, disp, func() time.Time { return now }, nil)
		if err != nil {
			t.Fatal(err)
		}
		ctrl.WithPointHealth(&fakePointHealthChecker{})
		rec := testAutoRecommendation(now.Add(-5 * time.Minute))
		out, err := ctrl.TryDispatch(context.Background(), rec, now)
		if err != nil {
			t.Fatal(err)
		}
		if !out.Dispatched || out.CommandIntentID != disp.commandID || out.MonitorID == "" {
			t.Fatalf("expected dispatch outcome, got %+v", out)
		}
		if len(disp.submitInputs) != 1 || disp.submitInputs[0].IdempotencyKey != "auto-opt:"+rec.ID || disp.submitInputs[0].IsRollback {
			t.Fatalf("unexpected command submit payload: %+v", disp.submitInputs)
		}
		if len(store.dispatchedRecs) != 1 || store.dispatchedRecs[0].Approval != intelligencemodel.RecommendationApproved || store.dispatchedRecs[0].Revalidation == nil {
			t.Fatalf("expected revalidated APPROVED recommendation, got %+v", store.dispatchedRecs)
		}
		if err := store.dispatchedRecs[0].CanCreateCommand(now); err != nil {
			t.Fatalf("recorded recommendation failed CanCreateCommand: %v", err)
		}
		if len(store.monitors) != 1 || store.monitors[0].WindowEndsAt.Sub(store.monitors[0].StartedAt) != DefaultExecutionMonitorWindow {
			t.Fatalf("expected 30m OBSERVING monitor, got %+v", store.monitors)
		}
	})
}

func TestAutoControllerRunMonitorPass(t *testing.T) {
	base := time.Date(2026, 11, 16, 4, 0, 0, 0, time.UTC)

	t.Run("completes VERIFIED after 30m healthy window", func(t *testing.T) {
		applied := 8.0
		restore := 7.0
		store := &fakeAutoControlStore{
			lease:   testAutoLease(base),
			siteCtx: testAutoSiteCtx(),
			monitors: []OptimizationExecutionMonitorRecord{{
				ID:                 "01990000-9900-7000-8000-000000000001",
				TenantID:           "01990000-3000-7000-8000-000000000001",
				SiteID:             "01990000-5000-7000-8000-000000000001",
				LeaseID:            "01990000-8800-7000-8000-000000000011",
				DeviceID:           "01990000-6000-7000-8000-000000000001",
				TargetCapability:   string(commandmodel.CapabilitySetChilledWaterTemperatureSetpoint),
				AppliedSupplyTempC: &applied,
				RestoreSupplyTempC: &restore,
				ZoneTempMaxC:       26.0,
				SupplyTempMaxC:     12.0,
				StartedAt:          base,
				WindowEndsAt:       base.Add(30 * time.Minute),
				Status:             ExecutionMonitorObserving,
			}},
		}
		obs := testLiveObservation(base.Add(30*time.Minute), 8.0, 24.0)
		disp := &fakeDispatcher{}
		ctrl, err := NewAutoController(store, &fakeTelemetryReader{obs: obs}, disp, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		ctrl.WithPointHealth(&fakePointHealthChecker{})
		summary, err := ctrl.RunMonitorPass(context.Background(), base.Add(30*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if summary.Verified != 1 || summary.RolledBack != 0 || store.monitors[0].Status != "VERIFIED" || store.lease.Status != "ACTIVE" {
			t.Fatalf("unexpected verify summary=%+v monitor=%+v lease=%+v", summary, store.monitors[0], store.lease)
		}
	})

	violationCases := []struct {
		name       string
		supplyC    float64
		zoneC      float64
		powerTrip  *ChillerPowerViolation
		wantPrefix string
	}{
		{name: "zone over limit triggers rollback and trip", supplyC: 8.0, zoneC: 26.4, wantPrefix: "zone_temperature_exceeded_max"},
		{name: "supply over limit triggers rollback and trip", supplyC: 12.4, zoneC: 24.0, wantPrefix: "supply_temperature_exceeded_max"},
		{name: "chiller power over 90pct triggers rollback and trip", supplyC: 8.0, zoneC: 24.0, powerTrip: &ChillerPowerViolation{AssetCode: "chiller-a", PowerKW: 120, RatedPowerKW: 127.5}, wantPrefix: "chiller_power_exceeded_90pct"},
	}

	for _, tc := range violationCases {
		t.Run(tc.name, func(t *testing.T) {
			applied := 8.0
			restore := 7.0
			monID := "01990000-9900-7000-8000-000000000009"
			store := &fakeAutoControlStore{
				lease:   testAutoLease(base),
				siteCtx: testAutoSiteCtx(),
				monitors: []OptimizationExecutionMonitorRecord{{
					ID:                 monID,
					TenantID:           "01990000-3000-7000-8000-000000000001",
					SiteID:             "01990000-5000-7000-8000-000000000001",
					LeaseID:            "01990000-8800-7000-8000-000000000011",
					DeviceID:           "01990000-6000-7000-8000-000000000001",
					TargetCapability:   string(commandmodel.CapabilitySetChilledWaterTemperatureSetpoint),
					AppliedSupplyTempC: &applied,
					RestoreSupplyTempC: &restore,
					ZoneTempMaxC:       26.0,
					SupplyTempMaxC:     12.0,
					StartedAt:          base,
					WindowEndsAt:       base.Add(30 * time.Minute),
					Status:             ExecutionMonitorObserving,
				}},
			}
			obs := testLiveObservation(base.Add(10*time.Minute), tc.supplyC, tc.zoneC)
			obs.ChillerPowerTrip = tc.powerTrip
			disp := &fakeDispatcher{
				decision:  commandauth.Decision{Allowed: true, ReasonCode: commandauth.ReasonAllowAutoLease},
				grant:     "rollback.sig",
				commandID: "01990000-7700-7000-8000-000000000111",
			}
			ctrl, err := NewAutoController(store, &fakeTelemetryReader{obs: obs}, disp, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			ctrl.WithPointHealth(&fakePointHealthChecker{})
			summary, err := ctrl.RunMonitorPass(context.Background(), base.Add(10*time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			if summary.RolledBack != 1 || store.lease.Status != "TRIPPED" {
				t.Fatalf("expected rollback and tripped lease, summary=%+v lease=%+v", summary, store.lease)
			}
			if len(disp.submitInputs) != 1 || !disp.submitInputs[0].IsRollback || disp.submitInputs[0].IdempotencyKey != "auto-rollback:"+monID {
				t.Fatalf("unexpected rollback submit payload: %+v", disp.submitInputs)
			}
			if got := disp.submitInputs[0].Parameters[commandmodel.ParameterSetpointC]; got != restore {
				t.Fatalf("rollback setpoint=%v want %v", got, restore)
			}
			if len(store.trippedRecords) != 1 || !strings.HasPrefix(store.trippedRecords[0].TripReason, tc.wantPrefix) {
				t.Fatalf("unexpected trip records: %+v", store.trippedRecords)
			}
		})
	}

	t.Run("telemetry unhealthy over 5m trips and sends rollback while transient <=5m clears on recovery", func(t *testing.T) {
		applied := 8.0
		restore := 7.0
		monID := "01990000-9900-7000-8000-000000000010"
		store := &fakeAutoControlStore{
			lease:   testAutoLease(base),
			siteCtx: testAutoSiteCtx(),
			monitors: []OptimizationExecutionMonitorRecord{{
				ID:                 monID,
				TenantID:           "01990000-3000-7000-8000-000000000001",
				SiteID:             "01990000-5000-7000-8000-000000000001",
				LeaseID:            "01990000-8800-7000-8000-000000000011",
				DeviceID:           "01990000-6000-7000-8000-000000000001",
				TargetCapability:   string(commandmodel.CapabilitySetChilledWaterTemperatureSetpoint),
				AppliedSupplyTempC: &applied,
				RestoreSupplyTempC: &restore,
				ZoneTempMaxC:       26.0,
				SupplyTempMaxC:     12.0,
				StartedAt:          base,
				WindowEndsAt:       base.Add(30 * time.Minute),
				Status:             ExecutionMonitorObserving,
			}},
		}
		healthChecker := &fakePointHealthChecker{err: errors.New("OFFLINE")}
		reader := &fakeTelemetryReader{obs: testLiveObservation(base, 8.0, 24.0)}
		disp := &fakeDispatcher{
			decision:  commandauth.Decision{Allowed: true, ReasonCode: commandauth.ReasonAllowAutoLease},
			grant:     "rollback.sig",
			commandID: "01990000-7700-7000-8000-000000000222",
		}
		ctrl, err := NewAutoController(store, reader, disp, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		ctrl.WithPointHealth(healthChecker)

		// T+2m: first unhealthy sample sets TelemetryUnhealthySince, no trip yet.
		if _, err := ctrl.RunMonitorPass(context.Background(), base.Add(2*time.Minute)); err != nil {
			t.Fatal(err)
		}
		if store.monitors[0].TelemetryUnhealthySince == nil || store.lease.Status != "ACTIVE" {
			t.Fatalf("expected unhealthy timestamp recorded without tripping: %+v", store.monitors[0])
		}

		// T+4m: telemetry recovers -> clears TelemetryUnhealthySince.
		healthChecker.setErr(nil)
		if _, err := ctrl.RunMonitorPass(context.Background(), base.Add(4*time.Minute)); err != nil {
			t.Fatal(err)
		}
		if store.monitors[0].TelemetryUnhealthySince != nil || store.heartbeatCalls != 1 {
			t.Fatalf("expected unhealthy marker cleared on recovery: %+v", store.monitors[0])
		}

		// T+5m then T+11m: unhealthy for >5m triggers rollback + trip.
		healthChecker.setErr(errors.New("OFFLINE"))
		if _, err := ctrl.RunMonitorPass(context.Background(), base.Add(5*time.Minute)); err != nil {
			t.Fatal(err)
		}
		summary, err := ctrl.RunMonitorPass(context.Background(), base.Add(11*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if summary.RolledBack != 1 || store.lease.Status != "TRIPPED" || len(disp.submitInputs) != 1 || !disp.submitInputs[0].IsRollback {
			t.Fatalf("expected rollback on >5m unhealthy telemetry, summary=%+v disp=%+v", summary, disp.submitInputs)
		}
	})

	t.Run("rollback anchors risk delta on applied setpoint when live supply drifted over 3C", func(t *testing.T) {
		applied := 8.5
		restore := 7.0
		monID := "01990000-9900-7000-8000-000000000012"
		store := &fakeAutoControlStore{
			lease:   testAutoLease(base),
			siteCtx: testAutoSiteCtx(),
			monitors: []OptimizationExecutionMonitorRecord{{
				ID:                 monID,
				TenantID:           "01990000-3000-7000-8000-000000000001",
				SiteID:             "01990000-5000-7000-8000-000000000001",
				LeaseID:            "01990000-8800-7000-8000-000000000011",
				DeviceID:           "01990000-6000-7000-8000-000000000001",
				TargetCapability:   string(commandmodel.CapabilitySetChilledWaterTemperatureSetpoint),
				AppliedSupplyTempC: &applied,
				RestoreSupplyTempC: &restore,
				ZoneTempMaxC:       26.0,
				SupplyTempMaxC:     11.0,
				StartedAt:          base,
				WindowEndsAt:       base.Add(30 * time.Minute),
				Status:             ExecutionMonitorObserving,
			}},
		}
		// Physical supply water drifted to 11.4 C (>3.0C above RestoreSupplyTempC 7.0 C).
		obs := testLiveObservation(base.Add(8*time.Minute), 11.4, 24.0)
		disp := &fakeDispatcher{
			decision:  commandauth.Decision{Allowed: true, ReasonCode: commandauth.ReasonAllowAutoLease},
			grant:     "rollback.sig",
			commandID: "01990000-7700-7000-8000-000000000444",
		}
		ctrl, err := NewAutoController(store, &fakeTelemetryReader{obs: obs}, disp, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		ctrl.WithPointHealth(&fakePointHealthChecker{})
		summary, err := ctrl.RunMonitorPass(context.Background(), base.Add(8*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if summary.RolledBack != 1 || len(disp.grantReqs) != 1 || disp.grantReqs[0].CurrentValue == nil {
			t.Fatalf("expected rollback grant request after drift, summary=%+v grantReqs=%+v", summary, disp.grantReqs)
		}
		if *disp.grantReqs[0].CurrentValue != applied {
			t.Fatalf("expected rollback currentValue anchored to applied setpoint %v, got %v", applied, *disp.grantReqs[0].CurrentValue)
		}
		if len(disp.submitInputs) != 1 || disp.submitInputs[0].CurrentValue != applied || disp.submitInputs[0].Parameters[commandmodel.ParameterSetpointC] != restore {
			t.Fatalf("unexpected rollback command submit payload: %+v", disp.submitInputs)
		}
	})
}

func TestWorkloadCommandClientHTTP(t *testing.T) {
	iamSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != iamCommandWorkloadDecisionPath || r.Method != http.MethodPost {
			t.Fatalf("unexpected IAM path %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(commandauth.DecisionResponse{
			Decision:        commandauth.Decision{Allowed: true, ReasonCode: commandauth.ReasonAllowAutoLease},
			DelegationGrant: "signed.grant",
		})
	}))
	defer iamSrv.Close()

	gwSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/sites/01990000-5000-7000-8000-000000000001/optimization/auto-commands" {
			t.Fatalf("unexpected gateway path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"commandId":"01990000-7700-7000-8000-000000000333"}`))
	}))
	defer gwSrv.Close()

	client, err := NewWorkloadCommandClient(WorkloadCommandClientConfig{
		IAMURL:        iamSrv.URL,
		GatewayURL:    gwSrv.URL,
		IAMClient:     iamSrv.Client(),
		GatewayClient: gwSrv.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	grant, decision, err := client.RequestCommandGrant(context.Background(), commandauth.WorkloadDecisionRequest{
		TenantID:   "01990000-3000-7000-8000-000000000001",
		SiteID:     "01990000-5000-7000-8000-000000000001",
		DeviceID:   "01990000-6000-7000-8000-000000000001",
		Capability: commandmodel.CapabilitySetChilledWaterTemperatureSetpoint,
	})
	if err != nil || !decision.Allowed || grant != "signed.grant" {
		t.Fatalf("RequestCommandGrant failed: grant=%q dec=%+v err=%v", grant, decision, err)
	}
	cmdID, err := client.SubmitAutoCommand(context.Background(), grant, AutoControlDispatchInput{
		TenantID:             "01990000-3000-7000-8000-000000000001",
		SiteID:               "01990000-5000-7000-8000-000000000001",
		DeviceID:             "01990000-6000-7000-8000-000000000001",
		PointID:              "01990000-7000-7000-8000-000000000001",
		Capability:           commandmodel.CapabilitySetChilledWaterTemperatureSetpoint,
		Parameters:           commandmodel.CommandParameters{commandmodel.ParameterSetpointC: 8.0},
		VerificationPointKey: "chilled_water_supply_temp_sp",
		IdempotencyKey:       "auto-opt:01990000-2000-7000-8000-000000000001",
		CurrentValue:         7.0,
		BusinessRevision:     1,
		ObservedAt:           time.Date(2026, 11, 16, 4, 0, 0, 0, time.UTC),
	})
	if err != nil || cmdID != "01990000-7700-7000-8000-000000000333" {
		t.Fatalf("SubmitAutoCommand failed: cmdID=%q err=%v", cmdID, err)
	}
}

func TestAutoControllerWorkerDefaultsAndOverrides(t *testing.T) {
	ctrl, err := NewAutoController(&fakeAutoControlStore{}, &fakeTelemetryReader{}, &fakeDispatcher{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ctrl.effectiveWorkerID(), ":") {
		t.Fatalf("default workerID=%q, want hostname:pid", ctrl.effectiveWorkerID())
	}
	if ctrl.effectiveLeaseDuration() != DefaultExecutionMonitorClaimLease {
		t.Fatalf("default leaseDuration=%v, want %v", ctrl.effectiveLeaseDuration(), DefaultExecutionMonitorClaimLease)
	}
	ctrl.WithWorkerID("  replica-7  ").WithLeaseDuration(90 * time.Second)
	if ctrl.effectiveWorkerID() != "replica-7" {
		t.Fatalf("overridden workerID=%q, want replica-7", ctrl.effectiveWorkerID())
	}
	if ctrl.effectiveLeaseDuration() != 90*time.Second {
		t.Fatalf("overridden leaseDuration=%v, want 90s", ctrl.effectiveLeaseDuration())
	}
	ctrl.WithWorkerID("   ").WithLeaseDuration(0)
	if ctrl.effectiveWorkerID() != "replica-7" || ctrl.effectiveLeaseDuration() != 90*time.Second {
		t.Fatalf("blank/zero overrides should be ignored: workerID=%q lease=%v", ctrl.effectiveWorkerID(), ctrl.effectiveLeaseDuration())
	}
}

func TestAutoControllerMultiReplicaMonitorClaimPreventsDuplicateRollback(t *testing.T) {
	base := time.Date(2026, 11, 16, 4, 0, 0, 0, time.UTC)
	applied := 8.0
	restore := 7.0
	monID := "01990000-9900-7000-8000-000000000021"
	store := &fakeAutoControlStore{
		lease:   testAutoLease(base),
		siteCtx: testAutoSiteCtx(),
		monitors: []OptimizationExecutionMonitorRecord{{
			ID:                 monID,
			TenantID:           "01990000-3000-7000-8000-000000000001",
			SiteID:             "01990000-5000-7000-8000-000000000001",
			LeaseID:            "01990000-8800-7000-8000-000000000011",
			DeviceID:           "01990000-6000-7000-8000-000000000001",
			TargetCapability:   string(commandmodel.CapabilitySetChilledWaterTemperatureSetpoint),
			AppliedSupplyTempC: &applied,
			RestoreSupplyTempC: &restore,
			ZoneTempMaxC:       26.0,
			SupplyTempMaxC:     12.0,
			StartedAt:          base,
			WindowEndsAt:       base.Add(30 * time.Minute),
			Status:             ExecutionMonitorObserving,
		}},
	}
	obs := testLiveObservation(base.Add(10*time.Minute), 8.0, 26.8)
	disp := &fakeDispatcher{
		decision:  commandauth.Decision{Allowed: true, ReasonCode: commandauth.ReasonAllowAutoLease},
		grant:     "rollback.sig",
		commandID: "01990000-7700-7000-8000-000000000555",
	}

	ctrlA, err := NewAutoController(store, &fakeTelemetryReader{obs: obs}, disp, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctrlA.WithPointHealth(&fakePointHealthChecker{}).WithWorkerID("replica-a").WithLeaseDuration(45 * time.Second)

	ctrlB, err := NewAutoController(store, &fakeTelemetryReader{obs: obs}, disp, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctrlB.WithPointHealth(&fakePointHealthChecker{}).WithWorkerID("replica-b").WithLeaseDuration(45 * time.Second)

	start := make(chan struct{})
	var wg sync.WaitGroup
	summaries := make([]MonitorPassSummary, 2)
	errs := make([]error, 2)
	for idx, ctrl := range []*AutoController{ctrlA, ctrlB} {
		wg.Add(1)
		go func(i int, c *AutoController) {
			defer wg.Done()
			<-start
			summaries[i], errs[i] = c.RunMonitorPass(context.Background(), base.Add(10*time.Minute))
		}(idx, ctrl)
	}
	close(start)
	wg.Wait()

	for i, runErr := range errs {
		if runErr != nil {
			t.Fatalf("replica %d RunMonitorPass err=%v", i, runErr)
		}
	}
	totalRolledBack := summaries[0].RolledBack + summaries[1].RolledBack
	totalChecked := summaries[0].Checked + summaries[1].Checked
	if totalChecked != 1 || totalRolledBack != 1 {
		t.Fatalf("expected exactly one replica to claim and roll back monitor, summaries=%+v", summaries)
	}
	if len(disp.submitInputs) != 1 || len(store.trippedRecords) != 1 {
		t.Fatalf("expected single rollback and single trip event, submits=%d trips=%d", len(disp.submitInputs), len(store.trippedRecords))
	}
	if store.monitors[0].ClaimOwner != nil || store.monitors[0].ClaimUntil != nil {
		t.Fatalf("expected monitor claim released after trip, got owner=%v until=%v", store.monitors[0].ClaimOwner, store.monitors[0].ClaimUntil)
	}
}

func TestAutoControllerLeaseExpiryTakeoverFencesStaleReplicaAndReleasesOnError(t *testing.T) {
	base := time.Date(2026, 11, 16, 4, 0, 0, 0, time.UTC)
	monID := "01990000-9900-7000-8000-000000000022"
	tenantID := "01990000-3000-7000-8000-000000000001"
	siteID := "01990000-5000-7000-8000-000000000001"
	applied := 8.0
	restore := 7.0
	store := &fakeAutoControlStore{
		lease:   testAutoLease(base),
		siteCtx: testAutoSiteCtx(),
		monitors: []OptimizationExecutionMonitorRecord{{
			ID:                 monID,
			TenantID:           tenantID,
			SiteID:             siteID,
			LeaseID:            "01990000-8800-7000-8000-000000000011",
			DeviceID:           "01990000-6000-7000-8000-000000000001",
			TargetCapability:   string(commandmodel.CapabilitySetChilledWaterTemperatureSetpoint),
			AppliedSupplyTempC: &applied,
			RestoreSupplyTempC: &restore,
			ZoneTempMaxC:       26.0,
			SupplyTempMaxC:     12.0,
			StartedAt:          base,
			WindowEndsAt:       base.Add(30 * time.Minute),
			Status:             ExecutionMonitorObserving,
		}},
	}

	claimedA, err := store.ClaimObservingExecutionMonitors(context.Background(), "replica-a", 45*time.Second, base.Add(time.Minute), 10)
	if err != nil || len(claimedA) != 1 {
		t.Fatalf("replica-a claim failed: len=%d err=%v", len(claimedA), err)
	}
	// Before 45s lease expires, replica-b cannot claim the monitor.
	claimedB, err := store.ClaimObservingExecutionMonitors(context.Background(), "replica-b", 45*time.Second, base.Add(90*time.Second), 10)
	if err != nil || len(claimedB) != 0 {
		t.Fatalf("replica-b claimed active lease unexpectedly: len=%d err=%v", len(claimedB), err)
	}
	// Once lease expires at base+1m45s, replica-b takes over.
	claimedB, err = store.ClaimObservingExecutionMonitors(context.Background(), "replica-b", 45*time.Second, base.Add(2*time.Minute), 10)
	if err != nil || len(claimedB) != 1 || claimedB[0].ClaimOwner == nil || *claimedB[0].ClaimOwner != "replica-b" {
		t.Fatalf("replica-b takeover failed: claimed=%+v err=%v", claimedB, err)
	}

	// Stale replica-a is fenced out from every state transition and claim release.
	if err := store.RecordMonitorHealthyHeartbeat(context.Background(), monID, tenantID, siteID, "replica-a", base.Add(2*time.Minute)); !errors.Is(err, ErrExecutionMonitorClaimLost) {
		t.Fatalf("expected ErrExecutionMonitorClaimLost on stale heartbeat, got %v", err)
	}
	if err := store.RecordMonitorTelemetryUnhealthy(context.Background(), monID, tenantID, siteID, "replica-a", base.Add(2*time.Minute), base.Add(2*time.Minute)); !errors.Is(err, ErrExecutionMonitorClaimLost) {
		t.Fatalf("expected ErrExecutionMonitorClaimLost on stale unhealthy update, got %v", err)
	}
	if err := store.CompleteMonitorVerified(context.Background(), monID, tenantID, siteID, "replica-a", base.Add(30*time.Minute)); !errors.Is(err, ErrExecutionMonitorClaimLost) {
		t.Fatalf("expected ErrExecutionMonitorClaimLost on stale verify, got %v", err)
	}
	if err := store.TripMonitorAndLease(context.Background(), claimedA[0], "replica-a", ExecutionMonitorTripped, "stale_trip", nil, base.Add(2*time.Minute)); !errors.Is(err, ErrExecutionMonitorClaimLost) {
		t.Fatalf("expected ErrExecutionMonitorClaimLost on stale trip, got %v", err)
	}
	if err := store.ReleaseMonitorClaim(context.Background(), monID, tenantID, siteID, "replica-a", base.Add(2*time.Minute)); !errors.Is(err, ErrExecutionMonitorClaimLost) {
		t.Fatalf("expected ErrExecutionMonitorClaimLost on stale release, got %v", err)
	}
	if store.lease.Status != "ACTIVE" || store.monitors[0].Status != ExecutionMonitorObserving || *store.monitors[0].ClaimOwner != "replica-b" {
		t.Fatalf("stale replica mutated active state: lease=%+v monitor=%+v", store.lease, store.monitors[0])
	}

	// Current owner replica-b records healthy heartbeat and releases the claim.
	if err := store.RecordMonitorHealthyHeartbeat(context.Background(), monID, tenantID, siteID, "replica-b", base.Add(2*time.Minute)); err != nil {
		t.Fatalf("replica-b heartbeat failed: %v", err)
	}
	if store.monitors[0].ClaimOwner != nil || store.monitors[0].ClaimUntil != nil {
		t.Fatalf("expected claim cleared after healthy heartbeat, got %+v", store.monitors[0])
	}

	// Early exit on site context load error releases claim immediately for next pass.
	store.siteCtxErr = errors.New("db hiccup")
	beforeReleases := store.releaseCalls
	ctrl, err := NewAutoController(store, &fakeTelemetryReader{}, &fakeDispatcher{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctrl.WithPointHealth(&fakePointHealthChecker{}).WithWorkerID("replica-c")
	summary, err := ctrl.RunMonitorPass(context.Background(), base.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("RunMonitorPass returned unexpected error: %v", err)
	}
	if summary.Checked != 1 || store.releaseCalls != beforeReleases+1 || store.monitors[0].ClaimOwner != nil || store.monitors[0].ClaimUntil != nil {
		t.Fatalf("expected claim released on early error exit: summary=%+v releases=%d before=%d monitor=%+v", summary, store.releaseCalls, beforeReleases, store.monitors[0])
	}
}

func TestAutoControllerContextCancelReleasesClaimWithoutMarkingUnhealthy(t *testing.T) {
	base := time.Date(2026, 11, 16, 4, 0, 0, 0, time.UTC)
	monID := "01990000-9900-7000-8000-000000000023"
	unhealthySince := base.Add(-10 * time.Minute)
	store := &fakeAutoControlStore{
		lease:   testAutoLease(base),
		siteCtx: testAutoSiteCtx(),
		monitors: []OptimizationExecutionMonitorRecord{{
			ID:                      monID,
			TenantID:                "01990000-3000-7000-8000-000000000001",
			SiteID:                  "01990000-5000-7000-8000-000000000001",
			LeaseID:                 "01990000-8800-7000-8000-000000000011",
			DeviceID:                "01990000-6000-7000-8000-000000000001",
			TargetCapability:        string(commandmodel.CapabilitySetChilledWaterTemperatureSetpoint),
			ZoneTempMaxC:            26.0,
			SupplyTempMaxC:          12.0,
			StartedAt:               base.Add(-15 * time.Minute),
			WindowEndsAt:            base.Add(15 * time.Minute),
			TelemetryUnhealthySince: &unhealthySince,
			Status:                  ExecutionMonitorObserving,
		}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	checker := &cancellingPointHealthChecker{cancel: cancel}
	ctrl, err := NewAutoController(store, &fakeTelemetryReader{}, &fakeDispatcher{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctrl.WithPointHealth(checker).WithWorkerID("replica-cancel")

	if _, err := ctrl.RunMonitorPass(ctx, base); err != nil {
		t.Fatalf("RunMonitorPass err=%v", err)
	}
	if store.releaseCalls != 1 || len(store.trippedRecords) != 0 || store.monitors[0].Status != ExecutionMonitorObserving || store.monitors[0].ClaimOwner != nil {
		t.Fatalf("expected claim released without tripping on context cancel: releases=%d trips=%d mon=%+v", store.releaseCalls, len(store.trippedRecords), store.monitors[0])
	}
}

type cancellingPointHealthChecker struct {
	cancel context.CancelFunc
}

func (checker *cancellingPointHealthChecker) CheckTelemetryPointHealth(context.Context, string, string, []string, []string) error {
	checker.cancel()
	return context.Canceled
}
