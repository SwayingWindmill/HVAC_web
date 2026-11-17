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
	lease            *AutoControlLeaseRecord
	leaseErr         error
	siteCtx          AutoControlSiteContext
	siteCtxErr       error
	dispatchedRecs   []Recommendation
	monitors         []OptimizationExecutionMonitorRecord
	heartbeatCalls   int
	unhealthyCalls   int
	verifiedCalls    int
	trippedRecords   []tripRecordCall
	recordMonitorSeq int
}

type tripRecordCall struct {
	MonitorID               string
	NewStatus               string
	TripReason              string
	RollbackCommandIntentID *string
	TrippedAt               time.Time
}

func (store *fakeAutoControlStore) ActiveAutoControlLease(context.Context, string, string, time.Time) (*AutoControlLeaseRecord, error) {
	return store.lease, store.leaseErr
}

func (store *fakeAutoControlStore) LoadAutoControlSiteContext(context.Context, string, string, string, time.Time) (AutoControlSiteContext, error) {
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

func (store *fakeAutoControlStore) ListObservingExecutionMonitors(context.Context, int) ([]OptimizationExecutionMonitorRecord, error) {
	out := make([]OptimizationExecutionMonitorRecord, 0, len(store.monitors))
	for _, mon := range store.monitors {
		if mon.Status == ExecutionMonitorObserving {
			out = append(out, mon)
		}
	}
	return out, nil
}

func (store *fakeAutoControlStore) RecordMonitorHealthyHeartbeat(_ context.Context, monitorID, _, _ string, checkedAt time.Time) error {
	store.heartbeatCalls++
	for idx := range store.monitors {
		if store.monitors[idx].ID == monitorID {
			t := checkedAt.UTC()
			store.monitors[idx].TelemetryUnhealthySince = nil
			store.monitors[idx].LastCheckedAt = &t
		}
	}
	return nil
}

func (store *fakeAutoControlStore) RecordMonitorTelemetryUnhealthy(_ context.Context, monitorID, _, _ string, unhealthySince, checkedAt time.Time) error {
	store.unhealthyCalls++
	for idx := range store.monitors {
		if store.monitors[idx].ID == monitorID {
			if store.monitors[idx].TelemetryUnhealthySince == nil {
				u := unhealthySince.UTC()
				store.monitors[idx].TelemetryUnhealthySince = &u
			}
			c := checkedAt.UTC()
			store.monitors[idx].LastCheckedAt = &c
		}
	}
	return nil
}

func (store *fakeAutoControlStore) CompleteMonitorVerified(_ context.Context, monitorID, _, _ string, checkedAt time.Time) error {
	store.verifiedCalls++
	for idx := range store.monitors {
		if store.monitors[idx].ID == monitorID {
			c := checkedAt.UTC()
			store.monitors[idx].Status = "VERIFIED"
			store.monitors[idx].TelemetryUnhealthySince = nil
			store.monitors[idx].LastCheckedAt = &c
		}
	}
	return nil
}

func (store *fakeAutoControlStore) TripMonitorAndLease(_ context.Context, monitor OptimizationExecutionMonitorRecord, newStatus, tripReason string, rollbackCommandIntentID *string, trippedAt time.Time) error {
	store.trippedRecords = append(store.trippedRecords, tripRecordCall{
		MonitorID:               monitor.ID,
		NewStatus:               newStatus,
		TripReason:              tripReason,
		RollbackCommandIntentID: rollbackCommandIntentID,
		TrippedAt:               trippedAt.UTC(),
	})
	if store.lease != nil {
		store.lease.Status = "TRIPPED"
	}
	for idx := range store.monitors {
		if store.monitors[idx].ID == monitor.ID {
			store.monitors[idx].Status = newStatus
			r := tripReason
			store.monitors[idx].TripReason = &r
			store.monitors[idx].RollbackCommandIntentID = rollbackCommandIntentID
		}
	}
	return nil
}

type fakeTelemetryReader struct {
	obs AutoControlLiveObservation
	err error
}

func (reader *fakeTelemetryReader) ReadLiveAutoControlObservation(context.Context, string, string, InputMapping, StagingTopology, time.Time) (AutoControlLiveObservation, error) {
	return reader.obs, reader.err
}

type fakeDispatcher struct {
	grantReqs    []commandauth.WorkloadDecisionRequest
	submitInputs []AutoControlDispatchInput
	decision     commandauth.Decision
	grant        string
	commandID    string
	grantErr     error
	submitErr    error
}

func (disp *fakeDispatcher) RequestCommandGrant(_ context.Context, req commandauth.WorkloadDecisionRequest) (string, commandauth.Decision, error) {
	disp.grantReqs = append(disp.grantReqs, req)
	return disp.grant, disp.decision, disp.grantErr
}

func (disp *fakeDispatcher) SubmitAutoCommand(_ context.Context, _ string, input AutoControlDispatchInput) (string, error) {
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
