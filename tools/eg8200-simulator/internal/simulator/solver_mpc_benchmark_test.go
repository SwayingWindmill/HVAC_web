package simulator

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

type coupledMPCClientSpec struct {
	name      string
	endpoint  string
	client    *http.Client
	callTimes []time.Duration
}

type coupledPlantPayload struct {
	Chillers               []coupledChillerSpecPayload `json:"chillers"`
	CHWPRatedKW            float64                     `json:"chwpRatedKw"`
	CHWPRatedFlowM3H       float64                     `json:"chwpRatedFlowM3h"`
	CHWPStaticHeadFrac     float64                     `json:"chwpStaticHeadFrac"`
	CHWPMinHz              float64                     `json:"chwpMinHz"`
	CHWPMaxHz              float64                     `json:"chwpMaxHz"`
	CWPRatedKW             float64                     `json:"cwpRatedKw"`
	CWPRatedFlowM3H        float64                     `json:"cwpRatedFlowM3h"`
	CWPStaticHeadFrac      float64                     `json:"cwpStaticHeadFrac"`
	CWPMinHz               float64                     `json:"cwpMinHz"`
	CWPMaxHz               float64                     `json:"cwpMaxHz"`
	TowerRatedKW           float64                     `json:"towerRatedKw"`
	TowerDesignHeatRejKW   float64                     `json:"towerDesignHeatRejKw"`
	TowerMinApproachC      float64                     `json:"towerMinApproachC"`
	TowerLoadApproachCoeff float64                     `json:"towerLoadApproachCoeff"`
	TowerNonlinearExp      float64                     `json:"towerNonlinearExp"`
	TowerLowFanPenaltyC    float64                     `json:"towerLowFanPenaltyC"`
	TowerMinFanPct         float64                     `json:"towerMinFanPct"`
	TowerMaxFanPct         float64                     `json:"towerMaxFanPct"`
	ZoneThermalMassKWhPerC float64                     `json:"zoneThermalMassKwhPerC"`
	ZoneSetpointC          float64                     `json:"zoneSetpointC"`
	DesignSupplyTempC      float64                     `json:"designSupplyTempC"`
	CoilApproachMinC       float64                     `json:"coilApproachMinC"`
	CoilFlowExp            float64                     `json:"coilFlowExp"`
}

type coupledChillerSpecPayload struct {
	ID             string  `json:"id"`
	RatedCoolingKW float64 `json:"ratedCoolingKw"`
	BaseCOP        float64 `json:"baseCop"`
}

type coupledHorizonSlotPayload struct {
	DurationHours   float64 `json:"durationHours"`
	CoolingLoadKW   float64 `json:"coolingLoadKw"`
	AmbientDryBulbC float64 `json:"ambientDryBulbC"`
	AmbientWetBulbC float64 `json:"ambientWetBulbC"`
	RelHumidityPct  float64 `json:"relHumidityPct"`
	TariffRateCNY   float64 `json:"tariffRateCny"`
}

type coupledContextRequest struct {
	PrecoolingEnabled bool                        `json:"precoolingEnabled"`
	COPScale          float64                     `json:"copScale"`
	Plant             coupledPlantPayload         `json:"plant"`
	Horizon           []coupledHorizonSlotPayload `json:"horizon"`
}

type solverBaselinePayload struct {
	DailyEnergyKWh float64 `json:"dailyEnergyKWh"`
	DailyCost      float64 `json:"dailyCost"`
	SupplyTempC    float64 `json:"supplyTempC"`
	ZoneTempC      float64 `json:"zoneTempC"`
}

type solverConstraintsPayload struct {
	SupplyTempMinC    float64 `json:"supplyTempMinC"`
	SupplyTempMaxC    float64 `json:"supplyTempMaxC"`
	ZoneTempMinC      float64 `json:"zoneTempMinC"`
	ZoneTempMaxC      float64 `json:"zoneTempMaxC"`
	MaxSupplyTempStep float64 `json:"maxSupplyTempStepC"`
}

type solverResponseModelPayload struct {
	DailyEnergyDeltaPerSupplyTempC float64 `json:"dailyEnergyDeltaKWhPerSupplyTempC"`
	ZoneTempDeltaPerSupplyTempC    float64 `json:"zoneTempDeltaCPerSupplyTempC"`
	EnergyUncertaintyP90KWh        float64 `json:"energyUncertaintyP90KWh"`
	ZoneTempUncertaintyP90C        float64 `json:"zoneTempUncertaintyP90C"`
}

type coupledSolveHTTPRequest struct {
	TenantID             string                     `json:"tenantId"`
	SiteID               string                     `json:"siteId"`
	SubjectType          string                     `json:"subjectType"`
	SubjectID            string                     `json:"subjectId"`
	OptimizationRunID    string                     `json:"optimizationRunId"`
	InputSnapshotID      string                     `json:"inputSnapshotId"`
	InputChecksum        string                     `json:"inputChecksum"`
	PolicyVersionID      string                     `json:"policyVersionId"`
	TopologyVersionID    string                     `json:"topologyVersionId"`
	LoadForecastSnapID   string                     `json:"loadForecastSnapshotId"`
	TariffVersionID      string                     `json:"tariffVersionId"`
	DeploymentRevisionID string                     `json:"deploymentRevisionId"`
	Objective            string                     `json:"objective"`
	Horizon              string                     `json:"horizon"`
	HorizonMinutes       int                        `json:"horizonMinutes"`
	Granularity          string                     `json:"granularity"`
	ValidFrom            time.Time                  `json:"validFrom"`
	Baseline             solverBaselinePayload      `json:"baseline"`
	Constraints          solverConstraintsPayload   `json:"constraints"`
	ResponseModel        solverResponseModelPayload `json:"responseModel"`
	CoupledContext       coupledContextRequest      `json:"coupledContext"`
}

type mpcPlannedSlotResponse struct {
	DurationHours      float64 `json:"durationHours"`
	SupplyTempC        float64 `json:"supplyTempC"`
	CHWPFreqHz         float64 `json:"chwpFreqHz"`
	CWPFreqHz          float64 `json:"cwpFreqHz"`
	TowerFanPct        float64 `json:"towerFanPct"`
	TargetChillers     int     `json:"targetChillers"`
	ProjectedZoneTempC float64 `json:"projectedZoneTempC"`
	OperatingMode      string  `json:"operatingMode"`
}

type coupledSolveHTTPResponse struct {
	SolverVersion    string `json:"solverVersion"`
	VerificationPlan struct {
		TargetRunningChillers int                      `json:"targetRunningChillers"`
		MPCSlots              []mpcPlannedSlotResponse `json:"mpcSlots"`
	} `json:"verificationPlan"`
}

type coupledMPCBenchmarkArm struct {
	spec              coupledMPCClientSpec
	precoolingEnabled bool
	plantCfg          CoupledChillerPlantConfig

	schedCtrl    *edgecontrol.EnergyScheduleController
	fallbackCtrl *edgecontrol.AutonomousFallbackController
	follower     *coupledMPCPlanFollower

	lastCloudEvalAt   time.Time
	lastRollbackAt    time.Time
	currentSetpointC  float64
	lastDayEnergyKWh  float64
	lastDayCostCNY    float64
	trailingCostCNY   float64
	prevEnergyKWh     float64
	windowEnergyStart float64
	windowStartAt     time.Time

	copScale         float64
	copSampleCount   int
	lastObservedRunN int
	lastStageSeenAt  time.Time
}

func (a *coupledMPCBenchmarkArm) Name() string { return a.spec.name }

func (a *coupledMPCBenchmarkArm) ConfigureHost(host *edgecontrol.Host, _ *CoupledChillerPlant, cfg CoupledChillerPlantConfig, start time.Time) error {
	a.plantCfg = cfg
	a.currentSetpointC = 7.0
	a.lastDayEnergyKWh = 3500.0
	a.lastDayCostCNY = 2800.0
	a.windowStartAt = start
	a.copScale = 1.0
	a.copSampleCount = 0
	a.spec.callTimes = nil

	autonomyPolicy := edgecontrol.DefaultAutonomyPolicyConfig()
	autonomyPolicy.Enabled = true
	autonomyPolicy.OfflineGraceSeconds = 60
	autonomyPolicy.DefaultChilledWaterSupplySetpointC = 7.0
	autonomyPolicy.DefaultPumpFrequencyHz = 40.0
	autonomyPolicy.MinPumpFrequencyHz = 28.0
	autonomyPolicy.MaxPumpFrequencyHz = 50.0
	autonomyPolicy.MinRunSeconds = 600
	autonomyPolicy.MinStopSeconds = 600

	interlock, err := buildCoupledHydraulicInterlockWithPolicy(cfg, autonomyPolicy)
	if err != nil {
		return err
	}

	chillerFallbacks := make([]edgecontrol.ChillerFallbackTarget, len(cfg.Chillers))
	for i, ch := range cfg.Chillers {
		chillerFallbacks[i] = edgecontrol.ChillerFallbackTarget{
			ComponentID:        ch.ID,
			SupplySetpointAddr: ch.ID + "/SupplySp",
			RunStateAddr:       ch.ID + "/RunState",
			FaultAddr:          ch.ID + "/Fault",
		}
	}
	pumpFallbacks := []edgecontrol.PumpFallbackTarget{
		{ComponentID: cfg.ChilledWaterPump.ID, FrequencyAddr: cfg.ChilledWaterPump.ID + "/FreqSp", RunStateAddr: cfg.ChilledWaterPump.ID + "/RunState", FaultAddr: cfg.ChilledWaterPump.ID + "/Fault"},
		{ComponentID: cfg.CoolingWaterPump.ID, FrequencyAddr: cfg.CoolingWaterPump.ID + "/FreqSp", RunStateAddr: cfg.CoolingWaterPump.ID + "/RunState", FaultAddr: cfg.CoolingWaterPump.ID + "/Fault"},
	}
	towerTargets := []edgecontrol.ClusterTowerBinding{
		{ComponentID: cfg.CoolingTower.ID, FanSpeedAddr: cfg.CoolingTower.ID + "/FanSp", RunStateAddr: cfg.CoolingTower.ID + "/RunState", FaultAddr: cfg.CoolingTower.ID + "/Fault"},
	}

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
	schedCtrl.UpdateTargets(chillerFallbacks, pumpFallbacks, towerTargets)
	fallbackCtrl.SetScheduleActiveFn(schedCtrl.IsActiveAt)

	follower := &coupledMPCPlanFollower{
		id:        "coupled-mpc-follower",
		cfg:       cfg,
		schedCtrl: schedCtrl,
		defaultSlot: mpcPlannedSlotResponse{
			SupplyTempC:    7.0,
			CHWPFreqHz:     38.0,
			CWPFreqHz:      40.0,
			TowerFanPct:    60.0,
			TargetChillers: len(cfg.Chillers),
			OperatingMode:  "NORMAL",
		},
	}

	a.schedCtrl = schedCtrl
	a.fallbackCtrl = fallbackCtrl
	a.follower = follower

	initialSlots := make([]mpcPlannedSlotResponse, 24)
	for i := range initialSlots {
		initialSlots[i] = follower.defaultSlot
	}
	if err := a.installCloudMPCSchedule(start, initialSlots); err != nil {
		return err
	}

	return host.Start([]edgecontrol.ControllerBinding{
		{Priority: 10, Critical: true, Controller: interlock},
		{Priority: 20, Controller: fallbackCtrl},
		{Priority: 50, Controller: schedCtrl},
		{Priority: 60, Controller: follower},
	})
}

func (a *coupledMPCBenchmarkArm) OnCloudConnectivityChanged(connected bool, at time.Time) {
	if a.schedCtrl != nil {
		a.schedCtrl.SetCloudConnected(connected, at)
	}
	if a.fallbackCtrl != nil {
		a.fallbackCtrl.SetCloudConnected(connected, at)
	}
}

func (a *coupledMPCBenchmarkArm) OnCycleResult(edgecontrol.HostCycleResult, BenchmarkStepContext) {}

func (a *coupledMPCBenchmarkArm) OnStep(ctx context.Context, step BenchmarkStepContext) error {
	// Accumulate observed energy/cost strictly from meter telemetry (never TruthState).
	obsEnergy := readObservedTelemetry(step.Observed, step.Spec.PlantConfig.PowerMeterID, "energyKwh", a.prevEnergyKWh)
	deltaE := obsEnergy - a.prevEnergyKWh
	if deltaE > 0 {
		a.trailingCostCNY += deltaE * step.TariffRateCNY
	}
	a.prevEnergyKWh = obsEnergy
	if !a.windowStartAt.IsZero() && step.Now.Sub(a.windowStartAt) >= 24*time.Hour {
		a.lastDayEnergyKWh = math.Max(100, obsEnergy-a.windowEnergyStart)
		a.lastDayCostCNY = math.Max(100, a.trailingCostCNY)
		a.windowEnergyStart = obsEnergy
		a.trailingCostCNY = 0
		a.windowStartAt = step.Now
	}

	// Online closed-loop grey-box residual COP scale calibration from observed chiller meters.
	a.updateOnlineResidualFromObserved(step)

	if !step.CloudConnected {
		return nil
	}

	obsZoneC := readObservedTelemetry(step.Observed, step.Spec.PlantConfig.ZoneSensorID, "zoneTemperatureC", 24.0)
	obsSupplyC := readObservedTelemetry(step.Observed, step.Spec.PlantConfig.BTUMeterID, "supplyWaterTemperatureC", a.currentSetpointC)
	zoneMaxC := step.Spec.PlantConfig.ZoneTempMaxC

	// Safety monitor trip/rollback only when observed zone approaches upper comfort ceiling.
	if obsZoneC > zoneMaxC-0.1 && a.currentSetpointC > 7.0 {
		a.currentSetpointC = 7.0
		a.lastRollbackAt = step.Now
		a.lastCloudEvalAt = step.Now
		slot := mpcPlannedSlotResponse{
			SupplyTempC:    7.0,
			CHWPFreqHz:     46.0,
			CWPFreqHz:      46.0,
			TowerFanPct:    80.0,
			TargetChillers: len(step.Spec.PlantConfig.Chillers),
			OperatingMode:  "NORMAL",
		}
		slots := make([]mpcPlannedSlotResponse, 24)
		for i := range slots {
			slots[i] = slot
		}
		return a.installCloudMPCSchedule(step.Now, slots)
	}

	if !a.lastCloudEvalAt.IsZero() && step.Now.Sub(a.lastCloudEvalAt) < time.Hour {
		return nil
	}
	if !a.lastRollbackAt.IsZero() && step.Now.Sub(a.lastRollbackAt) < 30*time.Minute {
		return nil
	}
	a.lastCloudEvalAt = step.Now

	horizon := make([]coupledHorizonSlotPayload, 24)
	for h := 0; h < 24; h++ {
		off := step.Elapsed + time.Duration(h)*time.Hour
		atH := step.Now.Add(time.Duration(h) * time.Hour)
		// Unbiased nominal weather/load forecast (contains natural plant mismatch & ripple uncertainty).
		fc := step.Spec.InputsFor(off, atH)
		rh := clamp(100.0-5.0*(fc.AmbientDryBulbC-fc.AmbientWetBulbC), 10.0, 98.0)
		dayIdx := int(off / (24 * time.Hour))
		rate := step.Spec.Tariff.RateAt(atH, dayIdx)
		horizon[h] = coupledHorizonSlotPayload{
			DurationHours:   1.0,
			CoolingLoadKW:   fc.CoolingLoadKW,
			AmbientDryBulbC: fc.AmbientDryBulbC,
			AmbientWetBulbC: fc.AmbientWetBulbC,
			RelHumidityPct:  rh,
			TariffRateCNY:   rate,
		}
	}

	plantSpec := buildCoupledPlantPayload(step.Spec.PlantConfig)
	payload := coupledSolveHTTPRequest{
		TenantID:             "01990000-3000-7000-8000-000000000001",
		SiteID:               "01990000-5000-7000-8000-000000000001",
		SubjectType:          "SITE",
		SubjectID:            "01990000-5000-7000-8000-000000000001",
		OptimizationRunID:    fmt.Sprintf("01990000-8000-7000-8000-%012x", step.Now.Unix()),
		InputSnapshotID:      "01990000-9000-7000-8000-000000000001",
		InputChecksum:        strings.Repeat("a", 64),
		PolicyVersionID:      "01990000-a000-7000-8000-000000000001",
		TopologyVersionID:    "01990000-b000-7000-8000-000000000001",
		LoadForecastSnapID:   "01990000-c000-7000-8000-000000000001",
		TariffVersionID:      "01990000-d000-7000-8000-000000000001",
		DeploymentRevisionID: "01990000-e000-7000-8000-000000000001",
		Objective:            "COST",
		Horizon:              "DAY_AHEAD",
		HorizonMinutes:       1440,
		Granularity:          "15MIN",
		ValidFrom:            step.Now.UTC(),
		Baseline: solverBaselinePayload{
			DailyEnergyKWh: math.Max(100, a.lastDayEnergyKWh),
			DailyCost:      math.Max(10, a.lastDayCostCNY),
			SupplyTempC:    clamp(obsSupplyC, 5.0, 10.5),
			ZoneTempC:      clamp(obsZoneC, step.Spec.PlantConfig.ZoneTempMinC, step.Spec.PlantConfig.ZoneTempMaxC),
		},
		Constraints: solverConstraintsPayload{
			SupplyTempMinC:    5.0,
			SupplyTempMaxC:    10.5,
			ZoneTempMinC:      step.Spec.PlantConfig.ZoneTempMinC,
			ZoneTempMaxC:      step.Spec.PlantConfig.ZoneTempMaxC,
			MaxSupplyTempStep: 1.5,
		},
		ResponseModel: solverResponseModelPayload{
			DailyEnergyDeltaPerSupplyTempC: -180.0,
			ZoneTempDeltaPerSupplyTempC:    0.35,
			EnergyUncertaintyP90KWh:        60.0,
			ZoneTempUncertaintyP90C:        0.25,
		},
		CoupledContext: coupledContextRequest{
			PrecoolingEnabled: a.precoolingEnabled,
			COPScale:          a.copScale,
			Plant:             plantSpec,
			Horizon:           horizon,
		},
	}

	rawReq, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	t0 := time.Now()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.spec.endpoint+"/v1/solve", bytes.NewReader(rawReq))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	res, err := a.spec.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("%s HTTP solve error: %w", a.spec.name, err)
	}
	body, readErr := io.ReadAll(res.Body)
	_ = res.Body.Close()
	a.spec.callTimes = append(a.spec.callTimes, time.Since(t0))
	if readErr != nil {
		return readErr
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s HTTP %d: %s", a.spec.name, res.StatusCode, strings.TrimSpace(string(body)))
	}
	var parsed coupledSolveHTTPResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return fmt.Errorf("%s decode response: %w", a.spec.name, err)
	}
	if len(parsed.VerificationPlan.MPCSlots) == 0 {
		return fmt.Errorf("%s returned empty mpcSlots", a.spec.name)
	}
	a.currentSetpointC = parsed.VerificationPlan.MPCSlots[0].SupplyTempC
	return a.installCloudMPCSchedule(step.Now, parsed.VerificationPlan.MPCSlots)
}

func (a *coupledMPCBenchmarkArm) updateOnlineResidualFromObserved(step BenchmarkStepContext) {
	totalCoolingKW := 0.0
	totalPowerKW := 0.0
	ratedCap := 0.0
	runningN := 0
	cwInSum := 0.0
	cwOutSum := 0.0

	for _, chCfg := range step.Spec.PlantConfig.Chillers {
		dev, ok := step.Observed.Devices[chCfg.ID]
		if !ok {
			continue
		}
		stateStr, _ := dev["runState"].(string)
		if !strings.EqualFold(strings.TrimSpace(stateStr), "RUNNING") {
			continue
		}
		qKW, _ := dev["coolingCapacityKw"].(float64)
		pKW, _ := dev["powerKw"].(float64)
		cwIn, _ := dev["enteringCoolingWaterTemperatureC"].(float64)
		cwOut, _ := dev["leavingCoolingWaterTemperatureC"].(float64)
		if qKW > 5 && pKW > 1 {
			runningN++
			totalCoolingKW += qKW
			totalPowerKW += pKW
			ratedCap += chCfg.RatedCoolingCapacityKW
			cwInSum += cwIn
			cwOutSum += cwOut
		}
	}
	if runningN != a.lastObservedRunN {
		a.lastObservedRunN = runningN
		a.lastStageSeenAt = step.Now
		return
	}
	if runningN == 0 || step.Now.Sub(a.lastStageSeenAt) < 20*time.Minute {
		return
	}
	if totalCoolingKW < 100 || totalPowerKW < 15 || ratedCap <= 0 {
		return
	}
	obsSupplyC := readObservedTelemetry(step.Observed, step.Spec.PlantConfig.BTUMeterID, "supplyWaterTemperatureC", 7.0)
	cwInAvg := cwInSum / float64(runningN)
	cwOutAvg := cwOutSum / float64(runningN)
	cwEff := 0.55*cwInAvg + 0.45*cwOutAvg
	liftNomCOP := clamp(5.7+0.17*(obsSupplyC-7.0)-0.13*(cwEff-30.0)-0.0025*math.Pow(math.Max(0, cwEff-30.0), 2), 1.8, 10.5)
	plr := totalCoolingKW / ratedCap
	nomPowerKW := ratedCap * (0.09 + 0.57*plr + 0.34*plr*plr) / liftNomCOP
	rawScale := clamp(nomPowerKW/totalPowerKW, 0.60, 1.25)

	const alpha = 0.20
	if a.copSampleCount == 0 {
		a.copScale = clamp(0.5+0.5*rawScale, 0.60, 1.25)
	} else {
		a.copScale = clamp((1.0-alpha)*a.copScale+alpha*rawScale, 0.60, 1.25)
	}
	a.copSampleCount++
}

func (a *coupledMPCBenchmarkArm) installCloudMPCSchedule(at time.Time, horizonSlots []mpcPlannedSlotResponse) error {
	var hourSlots [24]mpcPlannedSlotResponse
	var filled [24]bool
	for h, sl := range horizonSlots {
		if h >= 24 {
			break
		}
		hourUTC := at.UTC().Add(time.Duration(h) * time.Hour).Hour()
		if !filled[hourUTC] {
			hourSlots[hourUTC] = sl
			filled[hourUTC] = true
		}
	}
	for h := 0; h < 24; h++ {
		if !filled[h] {
			hourSlots[h] = horizonSlots[0]
		}
	}
	a.follower.setHourlySlots(hourSlots)

	edgeSlots := make([]edgecontrol.EnergyScheduleSlot, 0, 24)
	for h := 0; h < 24; h++ {
		sl := hourSlots[h]
		sp := sl.SupplyTempC
		maxCh := sl.TargetChillers
		if maxCh < 1 {
			maxCh = len(a.plantCfg.Chillers)
		}
		mode := edgecontrol.ScheduleOperatingMode(sl.OperatingMode)
		if !mode.Valid() {
			mode = edgecontrol.ScheduleModeNormal
		}
		edgeSlots = append(edgeSlots, edgecontrol.EnergyScheduleSlot{
			SlotID:              fmt.Sprintf("utc-h%02d", h),
			StartMinute:         h * 60,
			EndMinute:           (h + 1) * 60,
			Mode:                mode,
			SupplyTempSetpointC: &sp,
			MaxRunningChillers:  &maxCh,
		})
	}
	sort.Slice(edgeSlots, func(i, j int) bool {
		return edgeSlots[i].StartMinute < edgeSlots[j].StartMinute
	})
	return a.schedCtrl.UpdateSchedule(&edgecontrol.EdgeEnergySchedule{
		ScheduleID:                a.spec.name + "-schedule",
		Version:                   at.Unix(),
		IssuedAt:                  at,
		ValidFrom:                 at.Add(-time.Minute),
		ValidUntil:                at.Add(24 * time.Hour),
		LeaseExpiresAt:            at.Add(6 * time.Hour), // covers 2h outage seamlessly via schedule slots
		ContinueOfflineWhileValid: true,
		Slots:                     edgeSlots,
	})
}

type coupledMPCPlanFollower struct {
	id          string
	cfg         CoupledChillerPlantConfig
	schedCtrl   *edgecontrol.EnergyScheduleController
	defaultSlot mpcPlannedSlotResponse
	hourSlots   [24]mpcPlannedSlotResponse

	initialized     bool
	lastSupplySP    float64
	lastCHWPHz      float64
	lastCWPHz       float64
	lastTowerFanPct float64
	lastLocalMode   bool
	chiller2On      bool
	lastStageAt     time.Time
}

func (c *coupledMPCPlanFollower) ID() string { return c.id }

func (c *coupledMPCPlanFollower) setHourlySlots(slots [24]mpcPlannedSlotResponse) {
	c.hourSlots = slots
}

func (c *coupledMPCPlanFollower) Run(_ context.Context, img edgecontrol.ProcessImage, plan *edgecontrol.ControlPlan) error {
	now := img.At()
	if now.IsZero() {
		now = time.Now().UTC()
	}

	slot := c.defaultSlot
	if c.schedCtrl != nil && c.schedCtrl.IsActiveAt(now) {
		slot = c.hourSlots[now.UTC().Hour()]
	}

	obsLoadKW := readImageDouble(img, c.cfg.BTUMeterID+"/CoolingCapacityKW", 400.0)
	obsZoneC := readImageDouble(img, c.cfg.ZoneSensorID+"/ZoneTempC", 24.0)
	chwpLocal := readImageString(img, c.cfg.ChilledWaterPump.ID+"/LocalRemoteMode") == ControlCabinetModeLocal

	targetSP := clamp(slot.SupplyTempC, 5.0, 10.5)
	if c.cfg.ZoneTempMaxC > 0 && obsZoneC > c.cfg.ZoneTempMaxC-0.15 {
		targetSP = math.Min(targetSP, 6.8)
	}

	// Guard real-time CHW coil capacity and delta-T between hourly cloud updates.
	totalRatedKW := c.cfg.totalRatedCoolingKW()
	coilKWPerC := totalRatedKW / math.Max(1.0, zoneSetpointC-zoneDesignSupplyWaterC)
	deltaTCoil := math.Max(2.0, obsZoneC-targetSP)
	reqCoilFrac := clamp(math.Pow((obsLoadKW*1.05)/(coilKWPerC*deltaTCoil), 1.0/0.72), 0.38, 1.0)
	reqFlowDTFrac := clamp(obsLoadKW/(waterHeatCapacityKWPerM3HDeltaC*7.5*math.Max(1.0, c.cfg.ChilledWaterPump.RatedFlowM3H)), 0.38, 1.0)
	minSafeCHWPHz := math.Ceil(math.Max(reqCoilFrac, reqFlowDTFrac)*50.0*2.0) / 2.0
	targetCHWP := clamp(math.Max(slot.CHWPFreqHz, minSafeCHWPHz), 28.0, 50.0)
	targetCWP := clamp(slot.CWPFreqHz, 28.0, 50.0)
	targetTower := clamp(slot.TowerFanPct, 25.0, 100.0)

	if obsZoneC > c.cfg.ZoneTempMaxC-0.15 {
		targetCHWP = math.Max(targetCHWP, 45.0)
	}

	// Chiller staging: CHILLER-01 stays lead; CHILLER-02 obeys dwell + hysteresis.
	desiredChillers := slot.TargetChillers
	if obsLoadKW >= 575.0 || (c.cfg.ZoneTempMaxC > 0 && obsZoneC > c.cfg.ZoneTempMaxC-0.15) {
		desiredChillers = len(c.cfg.Chillers)
	}
	ch1Fault := coupledFaultActive(img, c.cfg.Chillers[0].ID+"/Fault")
	if !ch1Fault {
		if run1, ok := coupledRunActive(img, c.cfg.Chillers[0].ID+"/RunState"); !ok || !run1 {
			_, _, _ = plan.Request(c.cfg.Chillers[0].ID+"/Start", c.id, edgecontrol.BooleanValue(true))
		}
	}
	if len(c.cfg.Chillers) > 1 {
		ch2ID := c.cfg.Chillers[1].ID
		ch2Fault := coupledFaultActive(img, ch2ID+"/Fault")
		if c.lastStageAt.IsZero() {
			c.chiller2On = true
			c.lastStageAt = now
		}
		wantCh2 := c.chiller2On
		if now.Sub(c.lastStageAt) >= 30*time.Minute {
			if desiredChillers >= 2 && !c.chiller2On {
				wantCh2 = true
			} else if desiredChillers <= 1 && c.chiller2On && obsLoadKW < 495.0 {
				wantCh2 = false
			}
		}
		if wantCh2 != c.chiller2On {
			c.chiller2On = wantCh2
			c.lastStageAt = now
		}
		if !ch2Fault {
			run2, ok := coupledRunActive(img, ch2ID+"/RunState")
			if c.chiller2On {
				if !ok || !run2 {
					_, _, _ = plan.Request(ch2ID+"/Start", c.id, edgecontrol.BooleanValue(true))
				}
			} else if !ok || run2 {
				_, _, _ = plan.Request(ch2ID+"/Stop", c.id, edgecontrol.BooleanValue(true))
			}
		}
	}

	// Apply anti-hunting deadbands across supply setpoint, VFD pumps, and tower fan.
	if !c.initialized || math.Abs(targetSP-c.lastSupplySP) >= 0.3 {
		for _, ch := range c.cfg.Chillers {
			if !coupledFaultActive(img, ch.ID+"/Fault") {
				_, _, _ = plan.Request(ch.ID+"/SupplySp", c.id, edgecontrol.DoubleValue(targetSP))
			}
		}
		c.lastSupplySP = targetSP
	}

	// Never issue remote writes when CHWP cabinet selector is LOCAL; immediately recover once REMOTE returns.
	if chwpLocal {
		c.lastLocalMode = true
	} else {
		recoveredFromLocal := c.lastLocalMode
		c.lastLocalMode = false
		if !c.initialized || recoveredFromLocal || targetCHWP >= c.lastCHWPHz+1.0 || targetCHWP <= c.lastCHWPHz-2.0 {
			_, _, _ = plan.Request(c.cfg.ChilledWaterPump.ID+"/FreqSp", c.id, edgecontrol.DoubleValue(targetCHWP))
			c.lastCHWPHz = targetCHWP
		}
	}

	if !c.initialized || math.Abs(targetCWP-c.lastCWPHz) >= 1.5 {
		_, _, _ = plan.Request(c.cfg.CoolingWaterPump.ID+"/FreqSp", c.id, edgecontrol.DoubleValue(targetCWP))
		c.lastCWPHz = targetCWP
	}
	if !c.initialized || math.Abs(targetTower-c.lastTowerFanPct) >= 4.0 {
		_, _, _ = plan.Request(c.cfg.CoolingTower.ID+"/FanSp", c.id, edgecontrol.DoubleValue(targetTower))
		c.lastTowerFanPct = targetTower
	}
	c.initialized = true
	return nil
}

func buildCoupledPlantPayload(cfg CoupledChillerPlantConfig) coupledPlantPayload {
	chillers := make([]coupledChillerSpecPayload, len(cfg.Chillers))
	totalCap := 0.0
	weightedCOP := 0.0
	for i, ch := range cfg.Chillers {
		chillers[i] = coupledChillerSpecPayload{
			ID:             ch.ID,
			RatedCoolingKW: ch.RatedCoolingCapacityKW,
			BaseCOP:        ch.BaseCOP,
		}
		totalCap += ch.RatedCoolingCapacityKW
		weightedCOP += ch.RatedCoolingCapacityKW * ch.BaseCOP
	}
	baseCOP := weightedCOP / math.Max(1.0, totalCap)
	return coupledPlantPayload{
		Chillers:               chillers,
		CHWPRatedKW:            cfg.ChilledWaterPump.RatedPowerKW,
		CHWPRatedFlowM3H:       cfg.ChilledWaterPump.RatedFlowM3H,
		CHWPStaticHeadFrac:     0.10, // nominal grey-box prior; true mismatch unknown to solver
		CHWPMinHz:              28.0,
		CHWPMaxHz:              50.0,
		CWPRatedKW:             cfg.CoolingWaterPump.RatedPowerKW,
		CWPRatedFlowM3H:        cfg.CoolingWaterPump.RatedFlowM3H,
		CWPStaticHeadFrac:      0.12,
		CWPMinHz:               28.0,
		CWPMaxHz:               50.0,
		TowerRatedKW:           cfg.CoolingTower.RatedFanPowerKW,
		TowerDesignHeatRejKW:   totalCap * (1.0 + 1.0/baseCOP),
		TowerMinApproachC:      1.6,
		TowerLoadApproachCoeff: 1.8,
		TowerNonlinearExp:      1.25,
		TowerLowFanPenaltyC:    1.1,
		TowerMinFanPct:         25.0,
		TowerMaxFanPct:         100.0,
		ZoneThermalMassKWhPerC: totalCap * zoneThermalMassHours,
		ZoneSetpointC:          zoneSetpointC,
		DesignSupplyTempC:      zoneDesignSupplyWaterC,
		CoilApproachMinC:       4.5,
		CoilFlowExp:            0.72,
	}
}

func readImageDouble(img edgecontrol.ProcessImage, addr string, fallback float64) float64 {
	snap, ok := img.Get(addr)
	if !ok || !snap.HasValue || snap.Sample.Quality != edgecontrol.QualityGood {
		return fallback
	}
	if num, ok := snap.Sample.Value.NumericFloat64(); ok {
		return num
	}
	return fallback
}

func readImageString(img edgecontrol.ProcessImage, addr string) string {
	snap, ok := img.Get(addr)
	if !ok || !snap.HasValue || snap.Sample.Quality != edgecontrol.QualityGood {
		return ""
	}
	if snap.Sample.Value.Type == edgecontrol.DataTypeString {
		return strings.ToUpper(strings.TrimSpace(snap.Sample.Value.String))
	}
	return ""
}

func coupledFaultActive(img edgecontrol.ProcessImage, addr string) bool {
	snap, ok := img.Get(addr)
	if !ok || !snap.HasValue || snap.Sample.Quality != edgecontrol.QualityGood {
		return false
	}
	if snap.Sample.Value.Type == edgecontrol.DataTypeBoolean {
		return snap.Sample.Value.Boolean
	}
	return false
}

func startLoopbackSolverProcess(t *testing.T, cmd *exec.Cmd, listenPrefix string) (string, string) {
	t.Helper()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderrPath := filepath.Join(t.TempDir(), "solver.stderr.log")
	stderrFile, err := os.Create(stderrPath)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = stderrFile
	if err := cmd.Start(); err != nil {
		_ = stderrFile.Close()
		t.Fatalf("start %s failed: %v", listenPrefix, err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
		_ = stderrFile.Close()
	})

	listenCh := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(stdout)
		sent := false
		for sc.Scan() {
			line := sc.Text()
			if !sent {
				if after, ok := strings.CutPrefix(line, listenPrefix); ok {
					listenCh <- strings.TrimSpace(after)
					sent = true
				}
			}
		}
		_, _ = io.Copy(io.Discard, stdout)
	}()

	select {
	case addr := <-listenCh:
		return addr, stderrPath
	case <-time.After(15 * time.Second):
		rawErr, _ := os.ReadFile(stderrPath)
		t.Fatalf("timed out waiting for %s; stderr=%s", listenPrefix, string(rawErr))
		return "", stderrPath
	}
}

func TestCoupledSolverPrototypeComparison(t *testing.T) {
	pythonBin, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed in PATH")
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	pyDir := filepath.Join(repoRoot, "services", "ai-solver-service")
	if _, statErr := os.Stat(filepath.Join(pyDir, "ai_solver", "main.py")); statErr != nil {
		t.Fatalf("python solver missing: %v", statErr)
	}

	// Compile Go coupled MPC HTTP solver binary once outside benchmark timing.
	goBin := filepath.Join(t.TempDir(), "optimization-solver")
	buildCmd := exec.CommandContext(t.Context(), "go", "build", "-o", goBin, "./services/optimization-service/cmd/optimization-service")
	buildCmd.Dir = repoRoot
	if out, buildErr := buildCmd.CombinedOutput(); buildErr != nil {
		t.Fatalf("go build optimization-service failed: %v\n%s", buildErr, string(out))
	}

	pyCmd := exec.CommandContext(t.Context(), pythonBin, "-m", "ai_solver.main")
	pyCmd.Dir = pyDir
	pyCmd.Env = append(os.Environ(), "PYTHONPATH="+pyDir, "AI_SOLVER_HTTP_ADDR=127.0.0.1:0")
	pyEndpoint, pyStderrPath := startLoopbackSolverProcess(t, pyCmd, "AI_SOLVER_LISTENING=")

	goCmd := exec.CommandContext(t.Context(), goBin, "--coupled-mpc-http")
	goCmd.Dir = repoRoot
	goCmd.Env = append(os.Environ(), "COUPLED_SOLVER_HTTP_ADDR=127.0.0.1:0")
	goEndpoint, goStderrPath := startLoopbackSolverProcess(t, goCmd, "GO_SOLVER_LISTENING=")

	pyTransport := &http.Transport{DisableKeepAlives: true}
	pyClient := &http.Client{Timeout: 20 * time.Second, Transport: pyTransport}
	goTransport := &http.Transport{MaxIdleConns: 8, MaxIdleConnsPerHost: 4}
	goClient := &http.Client{Timeout: 20 * time.Second, Transport: goTransport}
	defer pyTransport.CloseIdleConnections()
	defer goTransport.CloseIdleConnections()

	runSuite := func(label string, precooling bool) []BenchmarkMetrics {
		spec := DefaultSevenDayMismatchSpec()
		spec.PlantConfig.PrecoolingCoupling = precooling

		pyArm := &coupledMPCBenchmarkArm{
			spec: coupledMPCClientSpec{
				name:     "PythonCoupledMPC",
				endpoint: pyEndpoint,
				client:   pyClient,
			},
			precoolingEnabled: precooling,
		}
		goArm := &coupledMPCBenchmarkArm{
			spec: coupledMPCClientSpec{
				name:     "GoCoupledMPC",
				endpoint: goEndpoint,
				client:   goClient,
			},
			precoolingEnabled: precooling,
		}
		arms := []BenchmarkArm{
			&FixedBaselineArm{},
			&LegacyLinearHeuristicArm{},
			pyArm,
			goArm,
		}
		results := make([]BenchmarkMetrics, 0, len(arms))
		for _, arm := range arms {
			m, runErr := RunPlantBenchmark(t.Context(), spec, arm)
			if runErr != nil {
				pyLog, _ := os.ReadFile(pyStderrPath)
				goLog, _ := os.ReadFile(goStderrPath)
				t.Fatalf("[%s] %s failed: %v\n--- py stderr (tail) ---\n%s\n--- go stderr ---\n%s",
					label, arm.Name(), runErr, tailLines(string(pyLog), 30), tailLines(string(goLog), 30))
			}
			results = append(results, m)
		}

		avgMs := func(durs []time.Duration) float64 {
			if len(durs) == 0 {
				return 0
			}
			var sum time.Duration
			for _, d := range durs {
				sum += d
			}
			return float64(sum.Microseconds()) / float64(len(durs)) / 1000.0
		}

		t.Logf("\n=== %s ===\n%sSolver latency/call: PythonCoupledMPC=%.2f ms/step (%d calls), GoCoupledMPC=%.2f ms/step (%d calls)\n",
			label,
			FormatBenchmarkMarkdownTable(results),
			avgMs(pyArm.spec.callTimes), len(pyArm.spec.callTimes),
			avgMs(goArm.spec.callTimes), len(goArm.spec.callTimes),
		)

		fixed := results[0]
		legacy := results[1]
		py := results[2]
		goM := results[3]

		for _, m := range []BenchmarkMetrics{py, goM} {
			if m.FalseTrips != 0 {
				t.Fatalf("[%s] %s false trips = %d, want 0", label, m.Strategy, m.FalseTrips)
			}
			if m.InterlockViolations != 0 {
				t.Fatalf("[%s] %s interlock violations = %d, want 0", label, m.Strategy, m.InterlockViolations)
			}
			if !m.OutageHandledCleanly {
				t.Fatalf("[%s] %s did not handle cloud outage cleanly", label, m.Strategy)
			}
			if !m.LocalOverrideHandledCleanly {
				t.Fatalf("[%s] %s did not handle LOCAL cabinet override cleanly", label, m.Strategy)
			}
			if m.ZoneExceedMinutes > 0 {
				t.Fatalf("[%s] %s zone exceedance %.1f min > 0 (max=%.2f min=%.2f)", label, m.Strategy, m.ZoneExceedMinutes, m.MaxTrueZoneTempC, m.MinTrueZoneTempC)
			}
			if m.TotalEnergyKWh >= fixed.TotalEnergyKWh || m.TotalCostCNY >= fixed.TotalCostCNY {
				t.Fatalf("[%s] %s did not beat FixedBaseline: %+v vs %+v", label, m.Strategy, m, fixed)
			}
			if m.ActuatorReversals >= legacy.ActuatorReversals {
				t.Fatalf("[%s] %s reversals %d >= legacy %d", label, m.Strategy, m.ActuatorReversals, legacy.ActuatorReversals)
			}
		}
		return results
	}

	runSuite("Standard 7-Day Plant-Model Mismatch (PrecoolingCoupling=false)", false)
	runSuite("Pre-Cooling Coupled 7-Day Plant-Model Mismatch (PrecoolingCoupling=true)", true)
}

func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}
