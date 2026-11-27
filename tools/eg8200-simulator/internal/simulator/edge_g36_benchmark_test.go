package simulator

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

// G36TrimRespondArm drives the plant using ChillerPlantClusterController's closed-loop
// ASHRAE Guideline 36 Trim & Respond (CHW DeltaT + Zone reset, CW DeltaT, Tower Approach)
// with fixed LoadLimit positive-feedback deadlock and full cooling-tower LocalRemoteMode interlock.
type G36TrimRespondArm struct {
	schedCtrl    *edgecontrol.EnergyScheduleController
	fallbackCtrl *edgecontrol.AutonomousFallbackController
	clusterCtrl  *edgecontrol.ChillerPlantClusterController

	lastLeaseRenewAt time.Time
}

func (a *G36TrimRespondArm) Name() string { return "G36TrimRespond" }

func (a *G36TrimRespondArm) ConfigureHost(host *edgecontrol.Host, _ *CoupledChillerPlant, cfg CoupledChillerPlantConfig, start time.Time) error {
	autonomyPolicy := edgecontrol.DefaultAutonomyPolicyConfig()
	autonomyPolicy.Enabled = true
	autonomyPolicy.OfflineGraceSeconds = 60
	autonomyPolicy.DefaultChilledWaterSupplySetpointC = 7.0
	autonomyPolicy.DefaultPumpFrequencyHz = 42.0
	autonomyPolicy.MinPumpFrequencyHz = 25.0
	autonomyPolicy.MaxPumpFrequencyHz = 50.0
	autonomyPolicy.MinRunSeconds = 600
	autonomyPolicy.MinStopSeconds = 600

	chillers := make([]edgecontrol.ChillerInterlockBinding, len(cfg.Chillers))
	chillerFallbacks := make([]edgecontrol.ChillerFallbackTarget, len(cfg.Chillers))
	clusterChillers := make([]edgecontrol.ClusterChillerBinding, len(cfg.Chillers))
	for i, ch := range cfg.Chillers {
		chillers[i] = edgecontrol.ChillerInterlockBinding{
			ComponentID:      ch.ID,
			StartCommand:     ch.ID + "/Start",
			StopCommand:      ch.ID + "/Stop",
			SetpointCommand:  ch.ID + "/SupplySp",
			LoadLimitCommand: ch.ID + "/LoadLimit",
			RunState:         ch.ID + "/RunState",
			FaultChannel:     ch.ID + "/Fault",
			LocalRemoteMode:  ch.ID + "/LocalRemoteMode",
		}
		chillerFallbacks[i] = edgecontrol.ChillerFallbackTarget{
			ComponentID:        ch.ID,
			SupplySetpointAddr: ch.ID + "/SupplySp",
			RunStateAddr:       ch.ID + "/RunState",
			FaultAddr:          ch.ID + "/Fault",
		}
		clusterChillers[i] = edgecontrol.ClusterChillerBinding{
			ComponentID:         ch.ID,
			RatedCoolingKW:      ch.RatedCoolingCapacityKW,
			StartAddr:           ch.ID + "/Start",
			StopAddr:            ch.ID + "/Stop",
			RunStateAddr:        ch.ID + "/RunState",
			FaultAddr:           ch.ID + "/Fault",
			SetpointAddr:        ch.ID + "/SupplySp",
			LoadLimitAddr:       ch.ID + "/LoadLimit",
			CoolingCapacityAddr: ch.ID + "/CapacityKW",
			PowerAddr:           ch.ID + "/PowerKW",
			LeavingCHWTempAddr:  ch.ID + "/LeavingCHWTempC",
			EnteringCHWTempAddr: ch.ID + "/EnteringCHWTempC",
			LeavingCWTempAddr:   ch.ID + "/LeavingCWTempC",
			EnteringCWTempAddr:  ch.ID + "/EnteringCWTempC",
			LocalRemoteModeAddr: ch.ID + "/LocalRemoteMode",
		}
	}

	chwPumps := []edgecontrol.PumpInterlockBinding{{
		ComponentID:      cfg.ChilledWaterPump.ID,
		StartCommand:     cfg.ChilledWaterPump.ID + "/Start",
		StopCommand:      cfg.ChilledWaterPump.ID + "/Stop",
		RunState:         cfg.ChilledWaterPump.ID + "/RunState",
		FlowFeedback:     cfg.ChilledWaterPump.ID + "/Flow",
		MinFlowRate:      cfg.ChilledWaterPump.RatedFlowM3H * 0.35,
		FaultChannel:     cfg.ChilledWaterPump.ID + "/Fault",
		FrequencyCommand: cfg.ChilledWaterPump.ID + "/FreqSp",
		LocalRemoteMode:  cfg.ChilledWaterPump.ID + "/LocalRemoteMode",
	}}
	cwPumps := []edgecontrol.PumpInterlockBinding{{
		ComponentID:      cfg.CoolingWaterPump.ID,
		StartCommand:     cfg.CoolingWaterPump.ID + "/Start",
		StopCommand:      cfg.CoolingWaterPump.ID + "/Stop",
		RunState:         cfg.CoolingWaterPump.ID + "/RunState",
		FlowFeedback:     cfg.CoolingWaterPump.ID + "/Flow",
		MinFlowRate:      cfg.CoolingWaterPump.RatedFlowM3H * 0.35,
		FaultChannel:     cfg.CoolingWaterPump.ID + "/Fault",
		FrequencyCommand: cfg.CoolingWaterPump.ID + "/FreqSp",
		LocalRemoteMode:  cfg.CoolingWaterPump.ID + "/LocalRemoteMode",
	}}
	towers := []edgecontrol.CoolingTowerInterlockBinding{{
		ComponentID:     cfg.CoolingTower.ID,
		StartCommand:    cfg.CoolingTower.ID + "/Start",
		StopCommand:     cfg.CoolingTower.ID + "/Stop",
		FanSpeedCommand: cfg.CoolingTower.ID + "/FanSp",
		RunState:        cfg.CoolingTower.ID + "/RunState",
		FaultChannel:    cfg.CoolingTower.ID + "/Fault",
		LocalRemoteMode: cfg.CoolingTower.ID + "/LocalRemoteMode",
	}}

	interlock, err := edgecontrol.NewHydraulicInterlockController(edgecontrol.HydraulicInterlockConfig{
		Policy:            autonomyPolicy,
		Chillers:          chillers,
		ChilledWaterPumps: chwPumps,
		CoolingWaterPumps: cwPumps,
		CoolingTowers:     towers,
	})
	if err != nil {
		return err
	}

	pumpFallbacks := []edgecontrol.PumpFallbackTarget{
		{ComponentID: cfg.ChilledWaterPump.ID, FrequencyAddr: cfg.ChilledWaterPump.ID + "/FreqSp", RunStateAddr: cfg.ChilledWaterPump.ID + "/RunState", FaultAddr: cfg.ChilledWaterPump.ID + "/Fault"},
		{ComponentID: cfg.CoolingWaterPump.ID, FrequencyAddr: cfg.CoolingWaterPump.ID + "/FreqSp", RunStateAddr: cfg.CoolingWaterPump.ID + "/RunState", FaultAddr: cfg.CoolingWaterPump.ID + "/Fault"},
	}
	clusterPumps := []edgecontrol.ClusterPumpBinding{
		{
			ComponentID:         cfg.ChilledWaterPump.ID,
			Loop:                edgecontrol.ClusterPumpLoopCHW,
			FrequencyAddr:       cfg.ChilledWaterPump.ID + "/FreqSp",
			ActualFrequencyAddr: cfg.ChilledWaterPump.ID + "/Frequency",
			RunStateAddr:        cfg.ChilledWaterPump.ID + "/RunState",
			FaultAddr:           cfg.ChilledWaterPump.ID + "/Fault",
			FlowAddr:            cfg.ChilledWaterPump.ID + "/Flow",
			LocalRemoteModeAddr: cfg.ChilledWaterPump.ID + "/LocalRemoteMode",
		},
		{
			ComponentID:         cfg.CoolingWaterPump.ID,
			Loop:                edgecontrol.ClusterPumpLoopCW,
			FrequencyAddr:       cfg.CoolingWaterPump.ID + "/FreqSp",
			ActualFrequencyAddr: cfg.CoolingWaterPump.ID + "/Frequency",
			RunStateAddr:        cfg.CoolingWaterPump.ID + "/RunState",
			FaultAddr:           cfg.CoolingWaterPump.ID + "/Fault",
			FlowAddr:            cfg.CoolingWaterPump.ID + "/Flow",
			LocalRemoteModeAddr: cfg.CoolingWaterPump.ID + "/LocalRemoteMode",
		},
	}
	clusterTowers := []edgecontrol.ClusterTowerBinding{{
		ComponentID:           cfg.CoolingTower.ID,
		FanSpeedAddr:          cfg.CoolingTower.ID + "/FanSp",
		ActualFanSpeedAddr:    cfg.CoolingTower.ID + "/FanSpeed",
		RunStateAddr:          cfg.CoolingTower.ID + "/RunState",
		FaultAddr:             cfg.CoolingTower.ID + "/Fault",
		LeavingWaterTempAddr:  cfg.CoolingTower.ID + "/LeavingWaterTempC",
		EnteringWaterTempAddr: cfg.CoolingTower.ID + "/EnteringWaterTempC",
		LocalRemoteModeAddr:   cfg.CoolingTower.ID + "/LocalRemoteMode",
	}}

	fallbackCtrl, err := edgecontrol.NewAutonomousFallbackController(edgecontrol.AutonomousFallbackConfig{
		Policy:                     autonomyPolicy,
		CloudConnected:             true,
		LastCloudContactAt:         start,
		SetpointRampRateCPerSecond: 0.05,
		FrequencyRampRateHzPerSec:  0.5,
		Chillers:                   chillerFallbacks,
		Pumps:                      pumpFallbacks,
	})
	if err != nil {
		return err
	}

	schedCtrl := edgecontrol.NewEnergyScheduleController("")
	schedCtrl.UpdateTargets(chillerFallbacks, pumpFallbacks, clusterTowers)
	fallbackCtrl.SetScheduleActiveFn(schedCtrl.IsActiveAt)

	clusterPolicy := edgecontrol.DefaultClusterControlPolicy()
	clusterPolicy.Enabled = true
	clusterPolicy.MinRunningChillers = 1
	clusterPolicy.MaxRunningChillers = len(cfg.Chillers)
	clusterPolicy.StageUpLoadPct = 85
	clusterPolicy.StageDownLoadPct = 40
	clusterPolicy.StageDwellSeconds = 300
	clusterPolicy.DefaultSupplySetpointC = 7.0
	clusterPolicy.MinPumpFrequencyHz = 25.0
	clusterPolicy.MaxPumpFrequencyHz = 50.0
	clusterPolicy.MinTowerFanSpeedPct = 30.0
	clusterPolicy.MaxTowerFanSpeedPct = 100.0
	clusterPolicy.TargetChwDeltaTC = 4.5
	clusterPolicy.TargetCwDeltaTC = 4.2
	clusterPolicy.TargetTowerApproachC = 3.2
	clusterPolicy.TrimRespondIntervalSeconds = 180
	clusterPolicy.MaxPumpSlewHzPerMin = 3.0
	clusterPolicy.MaxTowerSlewPctPerMin = 8.0
	clusterPolicy.EnableSupplyTempReset = true
	clusterPolicy.MinSupplySetpointC = 6.5
	clusterPolicy.MaxSupplySetpointC = 10.5
	clusterPolicy.TargetZoneTempMaxC = 24.75

	clusterCtrl, err := edgecontrol.NewChillerPlantClusterController(edgecontrol.ClusterControllerConfig{
		Policy:        clusterPolicy,
		Chillers:      clusterChillers,
		Pumps:         clusterPumps,
		Towers:        clusterTowers,
		PlantLoadAddr: cfg.BTUMeterID + "/CoolingCapacityKW",
		Sensors: edgecontrol.ClusterSensorBindings{
			CHWSupplyTempAddr:   cfg.BTUMeterID + "/SupplyTempC",
			CHWReturnTempAddr:   cfg.BTUMeterID + "/ReturnTempC",
			CHWDeltaTAddr:       cfg.BTUMeterID + "/DeltaTC",
			CWLeavingTempAddr:   cfg.CoolingTower.ID + "/LeavingWaterTempC",
			CWEnteringTempAddr:  cfg.CoolingTower.ID + "/EnteringWaterTempC",
			WetBulbTempAddr:     cfg.WeatherStationID + "/WetBulbC",
			OutdoorTempAddr:     cfg.WeatherStationID + "/DryBulbC",
			OutdoorHumidityAddr: cfg.WeatherStationID + "/RelativeHumidityPct",
			ZoneTempAddr:        cfg.ZoneSensorID + "/ZoneTempC",
		},
	})
	if err != nil {
		return err
	}
	clusterCtrl.SetScheduleOverrideFn(schedCtrl.ActiveOverrideAt)

	a.schedCtrl = schedCtrl
	a.fallbackCtrl = fallbackCtrl
	a.clusterCtrl = clusterCtrl

	if err := a.renewEdgeLeaseSchedule(start); err != nil {
		return err
	}

	return host.Start([]edgecontrol.ControllerBinding{
		{Priority: 10, Critical: true, Controller: interlock},
		{Priority: 20, Controller: fallbackCtrl},
		{Priority: 50, Controller: schedCtrl},
		{Priority: 60, Controller: clusterCtrl},
	})
}

func (a *G36TrimRespondArm) OnCloudConnectivityChanged(connected bool, at time.Time) {
	if a.schedCtrl != nil {
		a.schedCtrl.SetCloudConnected(connected, at)
	}
	if a.fallbackCtrl != nil {
		a.fallbackCtrl.SetCloudConnected(connected, at)
	}
}

func (a *G36TrimRespondArm) OnStep(_ context.Context, step BenchmarkStepContext) error {
	if !step.CloudConnected {
		return nil
	}
	if a.lastLeaseRenewAt.IsZero() || step.Now.Sub(a.lastLeaseRenewAt) >= 30*time.Minute {
		return a.renewEdgeLeaseSchedule(step.Now)
	}
	return nil
}

func (a *G36TrimRespondArm) OnCycleResult(edgecontrol.HostCycleResult, BenchmarkStepContext) {}

func (a *G36TrimRespondArm) renewEdgeLeaseSchedule(at time.Time) error {
	a.lastLeaseRenewAt = at
	return a.schedCtrl.UpdateSchedule(&edgecontrol.EdgeEnergySchedule{
		ScheduleID:                "edge-g36-baseline-lease",
		Version:                   at.Unix(),
		IssuedAt:                  at,
		ValidFrom:                 at.Add(-time.Minute),
		ValidUntil:                at.Add(24 * time.Hour),
		LeaseExpiresAt:            at.Add(65 * time.Minute),
		ContinueOfflineWhileValid: true,
		Slots: []edgecontrol.EnergyScheduleSlot{
			{
				SlotID:      "all-day",
				StartMinute: 0,
				EndMinute:   1440,
				Mode:        edgecontrol.ScheduleModeNormal,
			},
		},
	})
}

func TestSevenDayPlantMismatchEdgeG36VsOpenLoop(t *testing.T) {
	spec := DefaultSevenDayMismatchSpec()
	ctx := context.Background()

	arms := []BenchmarkArm{
		&FixedBaselineArm{TowerFanPct: 75},
		&LegacyLinearHeuristicArm{},
		&G36TrimRespondArm{},
	}

	results := make([]BenchmarkMetrics, 0, len(arms))
	for _, arm := range arms {
		metrics, err := RunPlantBenchmark(ctx, spec, arm)
		if err != nil {
			t.Fatalf("benchmark arm %s failed: %v", arm.Name(), err)
		}
		if metrics.ElapsedMs >= 5000 {
			t.Fatalf("arm %s exceeded 5s budget: %d ms", arm.Name(), metrics.ElapsedMs)
		}
		if metrics.FalseTrips != 0 {
			t.Fatalf("arm %s experienced %d false trips", arm.Name(), metrics.FalseTrips)
		}
		if metrics.InterlockViolations != 0 {
			t.Fatalf("arm %s experienced %d interlock violations", arm.Name(), metrics.InterlockViolations)
		}
		if !metrics.OutageHandledCleanly {
			t.Fatalf("arm %s failed 2h cloud outage check: %+v", arm.Name(), metrics)
		}
		if !metrics.LocalOverrideHandledCleanly {
			t.Fatalf("arm %s failed 1h LOCAL override check: %+v", arm.Name(), metrics)
		}
		results = append(results, metrics)
	}

	mdTable := FormatBenchmarkMarkdownTable(results)
	jsonReport, err := FormatBenchmarkJSON(results)
	if err != nil {
		t.Fatalf("FormatBenchmarkJSON failed: %v", err)
	}
	t.Logf("\n=== 7-Day Edge G36 Trim & Respond vs Open-Loop Benchmark (Markdown) ===\n%s\n=== JSON ===\n%s", mdTable, jsonReport)

	legacy := results[1]
	g36 := results[2]

	if g36.ZoneExceedMinutes != 0 {
		t.Fatalf("G36TrimRespond expected 0 zone exceedance minutes, got %.1f (maxZone=%.2fC)", g36.ZoneExceedMinutes, g36.MaxTrueZoneTempC)
	}
	if g36.TotalEnergyKWh >= legacy.TotalEnergyKWh {
		t.Fatalf("G36TrimRespond (%.2f kWh) must save energy vs LegacyOpenLoop (%.2f kWh)", g36.TotalEnergyKWh, legacy.TotalEnergyKWh)
	}
	if g36.TotalCostCNY >= legacy.TotalCostCNY {
		t.Fatalf("G36TrimRespond (%.2f CNY) must lower cost vs LegacyOpenLoop (%.2f CNY)", g36.TotalCostCNY, legacy.TotalCostCNY)
	}
	if g36.ActuatorChanges >= legacy.ActuatorChanges/2 {
		t.Fatalf("G36TrimRespond actuator changes (%d) must drop by >50%% vs LegacyOpenLoop (%d)", g36.ActuatorChanges, legacy.ActuatorChanges)
	}
	if g36.ActuatorReversals >= legacy.ActuatorReversals/3 {
		t.Fatalf("G36TrimRespond reversals (%d) must drop sharply vs LegacyOpenLoop (%d)", g36.ActuatorReversals, legacy.ActuatorReversals)
	}
}

func TestG36CoolingTowerLocalRemoteOverrideNoFalseTrips(t *testing.T) {
	t.Parallel()

	spec := DefaultSevenDayMismatchSpec()
	spec.Duration = 6 * time.Hour
	spec.OutageDuration = 0
	spec.LocalOverrideStart = 2 * time.Hour
	spec.LocalOverrideDur = time.Hour
	spec.LocalOverrideDevice = spec.PlantConfig.CoolingTower.ID
	spec.LocalManualTarget = 85.0

	plant, err := NewCoupledChillerPlant(spec.PlantConfig, spec.InputsFor, spec.StartLocal.UTC())
	if err != nil {
		t.Fatal(err)
	}
	host, err := edgecontrol.NewHost()
	if err != nil {
		t.Fatal(err)
	}
	for _, ad := range plant.BuildEdgeAdapters() {
		if err := host.RegisterAdapter(ad); err != nil {
			t.Fatal(err)
		}
	}
	arm := &G36TrimRespondArm{}
	if err := arm.ConfigureHost(host, plant, spec.PlantConfig, spec.StartLocal.UTC()); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	prevManual := false
	localRemoteAttempts := 0
	postRecovery := false
	totalSteps := int(spec.Duration / spec.Step)
	for i := 0; i < totalSteps; i++ {
		elapsed := time.Duration(i) * spec.Step
		now := spec.StartLocal.UTC().Add(elapsed)
		inLocal := elapsed >= spec.LocalOverrideStart && elapsed < spec.LocalOverrideStart+spec.LocalOverrideDur
		if inLocal && !prevManual {
			if !plant.SetControlCabinetMode(spec.LocalOverrideDevice, ControlCabinetModeLocal) ||
				!plant.SetLocalManualTarget(spec.LocalOverrideDevice, spec.LocalManualTarget) {
				t.Fatal("failed to engage LOCAL tower override")
			}
			prevManual = true
		} else if !inLocal && prevManual {
			if !plant.SetControlCabinetMode(spec.LocalOverrideDevice, ControlCabinetModeRemote) {
				t.Fatal("failed to restore REMOTE tower mode")
			}
			prevManual = false
		}

		res, err := host.RunCycle(ctx, now)
		if err != nil {
			t.Fatal(err)
		}
		if inLocal {
			for _, d := range res.Cycle.Decisions {
				if d.Address == spec.LocalOverrideDevice+"/FanSp" && d.Accepted {
					localRemoteAttempts++
				}
			}
		} else if !prevManual && elapsed >= spec.LocalOverrideStart+spec.LocalOverrideDur && elapsed < spec.LocalOverrideStart+spec.LocalOverrideDur+15*time.Minute {
			for _, wr := range res.WriteResults {
				if wr.Address == spec.LocalOverrideDevice+"/FanSp" && wr.Success {
					postRecovery = true
				}
			}
		}
		plant.Tick(spec.Step)
		if inLocal && math.Abs(plant.TruthState().TowerFanSetpointPct-spec.LocalManualTarget) > 1e-6 {
			t.Fatalf("LOCAL tower manual fan setpoint mutated to %.2f, want %.2f", plant.TruthState().TowerFanSetpointPct, spec.LocalManualTarget)
		}
	}
	truth := plant.TruthState()
	if localRemoteAttempts != 0 || truth.FalseTrips != 0 || truth.InterlockViolations != 0 || !postRecovery {
		t.Fatalf("tower local override failed: attempts=%d falseTrips=%d interlockViolations=%d postRecovery=%t",
			localRemoteAttempts, truth.FalseTrips, truth.InterlockViolations, postRecovery)
	}
}
