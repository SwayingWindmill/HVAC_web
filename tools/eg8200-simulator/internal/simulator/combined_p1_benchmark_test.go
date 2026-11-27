package simulator

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

// CombinedCloudMPCAndEdgeG36Arm wires the production Go coupled grey-box MPC
// cloud outer loop (builtin://coupled-plant-mpc-v1 via --coupled-mpc-http) to
// the production ASHRAE Guideline 36 Trim & Respond edge cluster inner loop.
type CombinedCloudMPCAndEdgeG36Arm struct {
	edgeG36  *G36TrimRespondArm
	cloudMPC *coupledMPCBenchmarkArm
}

func (a *CombinedCloudMPCAndEdgeG36Arm) Name() string {
	return a.cloudMPC.spec.name
}

func (a *CombinedCloudMPCAndEdgeG36Arm) ConfigureHost(
	host *edgecontrol.Host,
	plant *CoupledChillerPlant,
	cfg CoupledChillerPlantConfig,
	start time.Time,
) error {
	if err := a.edgeG36.ConfigureHost(host, plant, cfg, start); err != nil {
		return err
	}

	a.cloudMPC.plantCfg = cfg
	a.cloudMPC.schedCtrl = a.edgeG36.schedCtrl
	a.cloudMPC.fallbackCtrl = a.edgeG36.fallbackCtrl
	if a.cloudMPC.follower == nil {
		a.cloudMPC.follower = &coupledMPCPlanFollower{cfg: cfg, schedCtrl: a.edgeG36.schedCtrl}
	}
	a.cloudMPC.currentSetpointC = 7.0
	a.cloudMPC.lastDayEnergyKWh = 3500.0
	a.cloudMPC.lastDayCostCNY = 2800.0
	a.cloudMPC.windowStartAt = start
	a.cloudMPC.copScale = 1.0
	a.cloudMPC.copSampleCount = 0
	a.cloudMPC.spec.callTimes = nil
	a.cloudMPC.lastCloudEvalAt = time.Time{}
	a.cloudMPC.lastRollbackAt = time.Time{}

	initialSlots := make([]mpcPlannedSlotResponse, 24)
	for i := range initialSlots {
		initialSlots[i] = mpcPlannedSlotResponse{
			SupplyTempC:    7.0,
			CHWPFreqHz:     42.0,
			CWPFreqHz:      42.0,
			TowerFanPct:    70.0,
			TargetChillers: len(cfg.Chillers),
			OperatingMode:  string(edgecontrol.ScheduleModeNormal),
		}
	}
	return a.cloudMPC.installCloudMPCSchedule(start, initialSlots)
}

func (a *CombinedCloudMPCAndEdgeG36Arm) OnCloudConnectivityChanged(connected bool, at time.Time) {
	a.edgeG36.OnCloudConnectivityChanged(connected, at)
}

func (a *CombinedCloudMPCAndEdgeG36Arm) OnStep(ctx context.Context, step BenchmarkStepContext) error {
	if err := a.cloudMPC.OnStep(ctx, step); err != nil {
		return err
	}
	if step.CloudConnected && a.cloudMPC.currentSetpointC > 7.0 {
		obsZoneC := readObservedTelemetry(step.Observed, step.Spec.PlantConfig.ZoneSensorID, "zoneTemperatureC", 24.0)
		if obsZoneC > step.Spec.PlantConfig.ZoneTempMaxC-0.15 {
			a.cloudMPC.currentSetpointC = 7.0
			a.cloudMPC.lastRollbackAt = step.Now
			a.cloudMPC.lastCloudEvalAt = step.Now
			slot := mpcPlannedSlotResponse{
				SupplyTempC:    7.0,
				CHWPFreqHz:     46.0,
				CWPFreqHz:      46.0,
				TowerFanPct:    80.0,
				TargetChillers: len(step.Spec.PlantConfig.Chillers),
				OperatingMode:  string(edgecontrol.ScheduleModeNormal),
			}
			slots := make([]mpcPlannedSlotResponse, 24)
			for i := range slots {
				slots[i] = slot
			}
			return a.cloudMPC.installCloudMPCSchedule(step.Now, slots)
		}
	}
	return nil
}

func (a *CombinedCloudMPCAndEdgeG36Arm) OnCycleResult(res edgecontrol.HostCycleResult, step BenchmarkStepContext) {
	a.edgeG36.OnCycleResult(res, step)
}

func TestSevenDayCombinedCloudMPCAndEdgeG36(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	goBin := filepath.Join(t.TempDir(), "optimization-solver")
	buildCmd := exec.CommandContext(t.Context(), "go", "build", "-o", goBin, "./services/optimization-service/cmd/optimization-service")
	buildCmd.Dir = repoRoot
	if out, buildErr := buildCmd.CombinedOutput(); buildErr != nil {
		t.Fatalf("go build optimization-service failed: %v\n%s", buildErr, string(out))
	}

	goCmd := exec.CommandContext(t.Context(), goBin, "--coupled-mpc-http")
	goCmd.Dir = repoRoot
	goCmd.Env = append(goCmd.Environ(), "COUPLED_SOLVER_HTTP_ADDR=127.0.0.1:0")
	goEndpoint, goStderrPath := startLoopbackSolverProcess(t, goCmd, "GO_SOLVER_LISTENING=")

	goTransport := &http.Transport{MaxIdleConns: 8, MaxIdleConnsPerHost: 4}
	goClient := &http.Client{Timeout: 20 * time.Second, Transport: goTransport}
	defer goTransport.CloseIdleConnections()

	runSuite := func(label string, precooling bool) []BenchmarkMetrics {
		spec := DefaultSevenDayMismatchSpec()
		spec.PlantConfig.PrecoolingCoupling = precooling

		combinedArm := &CombinedCloudMPCAndEdgeG36Arm{
			edgeG36: &G36TrimRespondArm{},
			cloudMPC: &coupledMPCBenchmarkArm{
				spec: coupledMPCClientSpec{
					name:     "GoCoupledMPC+EdgeG36",
					endpoint: goEndpoint,
					client:   goClient,
				},
				precoolingEnabled: precooling,
			},
		}

		arms := []BenchmarkArm{
			&FixedBaselineArm{TowerFanPct: 75},
			&LegacyLinearHeuristicArm{},
			&G36TrimRespondArm{},
			combinedArm,
		}

		results := make([]BenchmarkMetrics, 0, len(arms))
		for _, arm := range arms {
			m, runErr := RunPlantBenchmark(t.Context(), spec, arm)
			if runErr != nil {
				rawErr, _ := os.ReadFile(goStderrPath)
				t.Fatalf("[%s] arm %s failed: %v\n--- solver stderr ---\n%s",
					label, arm.Name(), runErr, tailLines(string(rawErr), 30))
			}
			results = append(results, m)
		}

		avgMs := 0.0
		if calls := combinedArm.cloudMPC.spec.callTimes; len(calls) > 0 {
			var sum time.Duration
			for _, d := range calls {
				sum += d
			}
			avgMs = float64(sum.Microseconds()) / float64(len(calls)) / 1000.0
		}
		t.Logf("\n=== %s ===\n%sGoCoupledMPC HTTP latency: %.2f ms/step (%d calls)\n",
			label, FormatBenchmarkMarkdownTable(results), avgMs, len(combinedArm.cloudMPC.spec.callTimes))

		fixed := results[0]
		legacy := results[1]
		combined := results[3]

		if combined.FalseTrips != 0 {
			t.Fatalf("[%s] combined false trips = %d, want 0", label, combined.FalseTrips)
		}
		if combined.InterlockViolations != 0 {
			t.Fatalf("[%s] combined interlock violations = %d, want 0", label, combined.InterlockViolations)
		}
		if !combined.OutageHandledCleanly {
			t.Fatalf("[%s] combined did not handle 2h cloud outage cleanly: %+v", label, combined)
		}
		if !combined.LocalOverrideHandledCleanly {
			t.Fatalf("[%s] combined did not handle LOCAL cabinet override cleanly: %+v", label, combined)
		}
		if combined.ZoneExceedMinutes != 0 {
			t.Fatalf("[%s] combined zone exceedance %.1f min != 0 (max=%.2fC min=%.2fC)",
				label, combined.ZoneExceedMinutes, combined.MaxTrueZoneTempC, combined.MinTrueZoneTempC)
		}
		if combined.TotalEnergyKWh >= fixed.TotalEnergyKWh || combined.TotalEnergyKWh >= legacy.TotalEnergyKWh {
			t.Fatalf("[%s] combined energy %.2f kWh must beat Fixed (%.2f) and Legacy (%.2f)",
				label, combined.TotalEnergyKWh, fixed.TotalEnergyKWh, legacy.TotalEnergyKWh)
		}
		if combined.TotalCostCNY >= fixed.TotalCostCNY || combined.TotalCostCNY >= legacy.TotalCostCNY {
			t.Fatalf("[%s] combined cost %.2f CNY must beat Fixed (%.2f) and Legacy (%.2f)",
				label, combined.TotalCostCNY, fixed.TotalCostCNY, legacy.TotalCostCNY)
		}
		if combined.ActuatorReversals >= legacy.ActuatorReversals/2 {
			t.Fatalf("[%s] combined reversals %d must drop sharply vs Legacy %d",
				label, combined.ActuatorReversals, legacy.ActuatorReversals)
		}
		return results
	}

	runSuite("Combined Standard 7-Day Plant-Model Mismatch (PrecoolingCoupling=false)", false)
	runSuite("Combined Pre-Cooling Coupled 7-Day Plant-Model Mismatch (PrecoolingCoupling=true)", true)
}
