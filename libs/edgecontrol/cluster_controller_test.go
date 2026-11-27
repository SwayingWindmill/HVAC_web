package edgecontrol

import (
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"
)

func clusterChannel(comp, id, ptID string, dt DataType, access AccessMode) ChannelDescriptor {
	return ChannelDescriptor{
		ComponentID: comp, ChannelID: id, PointID: ptID,
		DataType: dt, Access: access, Category: ChannelCategoryOpenemsType,
		PollPriority: PriorityHigh, LocalPersistencePriority: PriorityHigh, RemotePersistencePriority: PriorityHigh,
		AggregationPriority: PriorityHigh, ResendPriority: PriorityHigh,
	}
}

func makeClusterChillerAdapter(id string, at time.Time, runState string, coolingKW float64, fault bool) *stubAutonomyDeviceAdapter {
	chans := []ChannelDescriptor{
		clusterChannel(id, "RunState", id+":run", DataTypeString, AccessReadOnly),
		clusterChannel(id, "Fault", id+":fault", DataTypeBoolean, AccessReadOnly),
		clusterChannel(id, "Start", id+":start", DataTypeBoolean, AccessReadWrite),
		clusterChannel(id, "Stop", id+":stop", DataTypeBoolean, AccessReadWrite),
		clusterChannel(id, "SupplySp", id+":sp", DataTypeDouble, AccessReadWrite),
		clusterChannel(id, "LoadLimit", id+":limit", DataTypeDouble, AccessReadWrite),
		clusterChannel(id, "CapacityKW", id+":cap", DataTypeDouble, AccessReadOnly),
	}
	ad := newStubAutonomyDeviceAdapter(ComponentDescriptor{
		ID: id, Enabled: true, Kind: ComponentDeviceDriver, Type: "CHILLER", FactoryID: DeclarativeModbusFactoryID, Version: "1.0.0",
		Profiles: []CapabilityProfileID{ProfileChiller},
	}, chans)
	ad.SetValue(id+"/RunState", StringValue(runState), QualityGood, at)
	ad.SetValue(id+"/Fault", BooleanValue(fault), QualityGood, at)
	ad.SetValue(id+"/Start", BooleanValue(false), QualityGood, at)
	ad.SetValue(id+"/Stop", BooleanValue(false), QualityGood, at)
	ad.SetValue(id+"/SupplySp", DoubleValue(7.0), QualityGood, at)
	ad.SetValue(id+"/LoadLimit", DoubleValue(80.0), QualityGood, at)
	ad.SetValue(id+"/CapacityKW", DoubleValue(coolingKW), QualityGood, at)
	return ad
}

func TestChillerPlantClusterControllerStagingWearAndSafetyOverrides(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 11, 22, 8, 0, 0, 0, time.UTC)

	t.Run("stages up lowest-runtime standby chiller, rejects before dwell, stages down highest-runtime", func(t *testing.T) {
		ch1 := makeClusterChillerAdapter("ch-1", t0, "RUNNING", 450, false)
		ch2 := makeClusterChillerAdapter("ch-2", t0, "STOPPED", 0, false)
		ch3 := makeClusterChillerAdapter("ch-3", t0, "STOPPED", 0, false)
		pump1 := newStubAutonomyDeviceAdapter(ComponentDescriptor{
			ID: "chwp-1", Enabled: true, Kind: ComponentDeviceDriver, Type: "PUMP", FactoryID: DeclarativeModbusFactoryID, Version: "1.0.0",
			Profiles: []CapabilityProfileID{ProfileVariableSpeedPump},
		}, []ChannelDescriptor{
			clusterChannel("chwp-1", "RunState", "chwp1:run", DataTypeString, AccessReadOnly),
			clusterChannel("chwp-1", "Flow", "chwp1:flow", DataTypeDouble, AccessReadOnly),
			clusterChannel("chwp-1", "FreqSp", "chwp1:freq", DataTypeDouble, AccessReadWrite),
		})
		pump1.SetValue("chwp-1/RunState", StringValue("RUNNING"), QualityGood, t0)
		pump1.SetValue("chwp-1/Flow", DoubleValue(120.0), QualityGood, t0)
		pump1.SetValue("chwp-1/FreqSp", DoubleValue(35.0), QualityGood, t0)

		tower1 := newStubAutonomyDeviceAdapter(ComponentDescriptor{
			ID: "ct-1", Enabled: true, Kind: ComponentDeviceDriver, Type: "TOWER", FactoryID: DeclarativeModbusFactoryID, Version: "1.0.0",
			Profiles: []CapabilityProfileID{ProfileCoolingTower},
		}, []ChannelDescriptor{
			clusterChannel("ct-1", "RunState", "ct1:run", DataTypeString, AccessReadOnly),
			clusterChannel("ct-1", "FanSp", "ct1:fan", DataTypeDouble, AccessReadWrite),
		})
		tower1.SetValue("ct-1/RunState", StringValue("RUNNING"), QualityGood, t0)
		tower1.SetValue("ct-1/FanSp", DoubleValue(50.0), QualityGood, t0)

		host, err := NewHost()
		if err != nil {
			t.Fatal(err)
		}
		for _, ad := range []DeviceAdapter{ch1, ch2, ch3, pump1, tower1} {
			if err := host.RegisterAdapter(ad); err != nil {
				t.Fatal(err)
			}
		}

		interlockPolicy := DefaultAutonomyPolicyConfig()
		interlockPolicy.Enabled = true
		interlock, err := NewHydraulicInterlockController(HydraulicInterlockConfig{
			Policy: interlockPolicy,
			Chillers: []ChillerInterlockBinding{
				{ComponentID: "ch-1", StartCommand: "ch-1/Start", StopCommand: "ch-1/Stop", RunState: "ch-1/RunState", FaultChannel: "ch-1/Fault"},
				{ComponentID: "ch-2", StartCommand: "ch-2/Start", StopCommand: "ch-2/Stop", RunState: "ch-2/RunState", FaultChannel: "ch-2/Fault"},
				{ComponentID: "ch-3", StartCommand: "ch-3/Start", StopCommand: "ch-3/Stop", RunState: "ch-3/RunState", FaultChannel: "ch-3/Fault"},
			},
			ChilledWaterPumps: []PumpInterlockBinding{{ComponentID: "chwp-1", RunState: "chwp-1/RunState", FlowFeedback: "chwp-1/Flow", FrequencyCommand: "chwp-1/FreqSp"}},
		})
		if err != nil {
			t.Fatal(err)
		}

		policy := DefaultClusterControlPolicy()
		policy.Enabled = true
		policy.StageUpLoadPct = 85
		policy.StageDownLoadPct = 40
		policy.StageDwellSeconds = 300
		cluster, err := NewChillerPlantClusterController(ClusterControllerConfig{
			Policy: policy,
			Chillers: []ClusterChillerBinding{
				{ComponentID: "ch-1", RatedCoolingKW: 500, StartAddr: "ch-1/Start", StopAddr: "ch-1/Stop", RunStateAddr: "ch-1/RunState", FaultAddr: "ch-1/Fault", SetpointAddr: "ch-1/SupplySp", LoadLimitAddr: "ch-1/LoadLimit", CoolingCapacityAddr: "ch-1/CapacityKW"},
				{ComponentID: "ch-2", RatedCoolingKW: 500, StartAddr: "ch-2/Start", StopAddr: "ch-2/Stop", RunStateAddr: "ch-2/RunState", FaultAddr: "ch-2/Fault", SetpointAddr: "ch-2/SupplySp", LoadLimitAddr: "ch-2/LoadLimit", CoolingCapacityAddr: "ch-2/CapacityKW"},
				{ComponentID: "ch-3", RatedCoolingKW: 500, StartAddr: "ch-3/Start", StopAddr: "ch-3/Stop", RunStateAddr: "ch-3/RunState", FaultAddr: "ch-3/Fault", SetpointAddr: "ch-3/SupplySp", LoadLimitAddr: "ch-3/LoadLimit", CoolingCapacityAddr: "ch-3/CapacityKW"},
			},
			Pumps:  []ClusterPumpBinding{{ComponentID: "chwp-1", FrequencyAddr: "chwp-1/FreqSp", RunStateAddr: "chwp-1/RunState"}},
			Towers: []ClusterTowerBinding{{ComponentID: "ct-1", FanSpeedAddr: "ct-1/FanSp", RunStateAddr: "ct-1/RunState"}},
		})
		if err != nil {
			t.Fatal(err)
		}

		// Seed ch-1 and ch-2 with higher accumulated runtime so wear-leveling starts ch-3 first and later stages down ch-1 first.
		cluster.mu.Lock()
		cluster.runtimeSec["ch-1"] = 500
		cluster.runtimeSec["ch-2"] = 1200
		cluster.runtimeSec["ch-3"] = 100
		cluster.mu.Unlock()

		if err := host.Start([]ControllerBinding{
			{Priority: 10, Critical: true, Controller: interlock},
			{Priority: 60, Controller: cluster},
		}); err != nil {
			t.Fatal(err)
		}

		// Load 450kW / 500kW rated = 90% >= 85%: starts ch-3 (lower runtime), sets pump/tower proportional load.
		if _, err := host.RunCycle(context.Background(), t0); err != nil {
			t.Fatal(err)
		}
		vStart3, _ := ch3.ReadValue("ch-3/Start")
		vStart2, _ := ch2.ReadValue("ch-2/Start")
		if !vStart3.Boolean || vStart2.Boolean {
			t.Fatalf("expected lowest-runtime standby ch-3 started over ch-2, got ch2=%v ch3=%v", vStart2.Boolean, vStart3.Boolean)
		}
		vFreq, _ := pump1.ReadValue("chwp-1/FreqSp")
		wantFreq := 28.0 + (50.0-28.0)*0.90
		if math.Abs(vFreq.Double-wantFreq) > 1e-6 {
			t.Fatalf("pump frequency = %v, want %v", vFreq.Double, wantFreq)
		}

		// Simulate ch-3 now running, load drops to 300kW / 1000kW = 30% <= 40%, yet within 300s dwell -> blocked!
		ch3.SetValue("ch-3/RunState", StringValue("RUNNING"), QualityGood, t0.Add(60*time.Second))
		ch1.SetValue("ch-1/CapacityKW", DoubleValue(150), QualityGood, t0.Add(60*time.Second))
		ch3.SetValue("ch-3/CapacityKW", DoubleValue(150), QualityGood, t0.Add(60*time.Second))
		if _, err := host.RunCycle(context.Background(), t0.Add(60*time.Second)); err != nil {
			t.Fatal(err)
		}
		vStop1, _ := ch1.ReadValue("ch-1/Stop")
		vStop3, _ := ch3.ReadValue("ch-3/Stop")
		if vStop1.Boolean || vStop3.Boolean {
			t.Fatal("expected dwell timer to prevent premature stage-down at +60s")
		}

		// Advance past 300s dwell (+360s): stops ch-3 because its cumulative runtime in this run? Actually ch-1 was running + ch-1/ch-3 cumulative: ch-1=360s, ch-3=300s -> ch-1 stops!
		ch1.SetValue("ch-1/CapacityKW", DoubleValue(150), QualityGood, t0.Add(360*time.Second))
		ch3.SetValue("ch-3/CapacityKW", DoubleValue(150), QualityGood, t0.Add(360*time.Second))
		if _, err := host.RunCycle(context.Background(), t0.Add(360*time.Second)); err != nil {
			t.Fatal(err)
		}
		vStop1After, _ := ch1.ReadValue("ch-1/Stop")
		if !vStop1After.Boolean {
			t.Fatal("expected highest-runtime running chiller ch-1 staged down once dwell elapsed")
		}

		// Verify HydraulicInterlock Priority-10 denial blocks cluster stage-up when chilled water flow is lost!
		pump1.SetValue("chwp-1/RunState", StringValue("STOPPED"), QualityGood, t0.Add(700*time.Second))
		pump1.SetValue("chwp-1/Flow", DoubleValue(0), QualityGood, t0.Add(700*time.Second))
		ch1.SetValue("ch-1/RunState", StringValue("STOPPED"), QualityGood, t0.Add(700*time.Second))
		ch3.SetValue("ch-3/RunState", StringValue("STOPPED"), QualityGood, t0.Add(700*time.Second))
		ch3.SetValue("ch-3/Start", BooleanValue(false), QualityGood, t0.Add(700*time.Second))
		ch2.SetValue("ch-2/Start", BooleanValue(false), QualityGood, t0.Add(700*time.Second))
		if _, err := host.RunCycle(context.Background(), t0.Add(700*time.Second)); err != nil {
			t.Fatal(err)
		}
		vStart2Blocked, _ := ch2.ReadValue("ch-2/Start")
		vStart3Blocked, _ := ch3.ReadValue("ch-3/Start")
		if vStart2Blocked.Boolean || vStart3Blocked.Boolean {
			t.Fatal("Priority-10 HydraulicInterlock denial must prevent cluster start without CHW flow")
		}
	})

	t.Run("policy validation catches invalid hysteresis and bounds", func(t *testing.T) {
		p := DefaultClusterControlPolicy()
		p.StageDownLoadPct = 80
		p.StageUpLoadPct = 85
		if err := p.Validate(); err == nil {
			t.Fatal("expected insufficient stage hysteresis error")
		}
	})
}

func TestClusterControllerLoadLimitNo30PercentDeadlock(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 11, 27, 2, 0, 0, 0, time.UTC)
	ch1 := makeClusterChillerAdapter("ch-1", t0, "RUNNING", 150, false) // 150 / 500 = 30% low part-load
	pump1 := newStubAutonomyDeviceAdapter(ComponentDescriptor{
		ID: "chwp-1", Enabled: true, Kind: ComponentDeviceDriver, Type: "PUMP",
		FactoryID: DeclarativeModbusFactoryID, Version: "1.0.0", Profiles: []CapabilityProfileID{ProfileVariableSpeedPump},
	}, []ChannelDescriptor{
		clusterChannel("chwp-1", "RunState", "chwp1:run", DataTypeString, AccessReadOnly),
		clusterChannel("chwp-1", "FreqSp", "chwp1:freq", DataTypeDouble, AccessReadWrite),
	})
	pump1.SetValue("chwp-1/RunState", StringValue("RUNNING"), QualityGood, t0)
	pump1.SetValue("chwp-1/FreqSp", DoubleValue(35.0), QualityGood, t0)

	host, err := NewHost()
	if err != nil {
		t.Fatal(err)
	}
	if err := host.RegisterAdapter(ch1); err != nil {
		t.Fatal(err)
	}
	if err := host.RegisterAdapter(pump1); err != nil {
		t.Fatal(err)
	}

	policy := DefaultClusterControlPolicy()
	policy.Enabled = true
	policy.MinRunningChillers = 1
	policy.MaxRunningChillers = 1

	var activeOverride *ClusterScheduleOverride
	cluster, err := NewChillerPlantClusterController(ClusterControllerConfig{
		Policy: policy,
		Chillers: []ClusterChillerBinding{{
			ComponentID: "ch-1", RatedCoolingKW: 500, StartAddr: "ch-1/Start", StopAddr: "ch-1/Stop",
			RunStateAddr: "ch-1/RunState", FaultAddr: "ch-1/Fault", SetpointAddr: "ch-1/SupplySp",
			LoadLimitAddr: "ch-1/LoadLimit", CoolingCapacityAddr: "ch-1/CapacityKW",
		}},
		Pumps: []ClusterPumpBinding{{ComponentID: "chwp-1", FrequencyAddr: "chwp-1/FreqSp", RunStateAddr: "chwp-1/RunState"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	cluster.SetScheduleOverrideFn(func(time.Time) *ClusterScheduleOverride { return activeOverride })

	if err := host.Start([]ControllerBinding{{Priority: 60, Controller: cluster}}); err != nil {
		t.Fatal(err)
	}

	// Run 10 cycles at low part-load (100 kW = 20% of 500 kW): LoadLimit must remain 100%, never shrinking to 30%.
	for i := 0; i < 10; i++ {
		at := t0.Add(time.Duration(i) * time.Minute)
		ch1.SetValue("ch-1/CapacityKW", DoubleValue(100), QualityGood, at)
		if _, err := host.RunCycle(context.Background(), at); err != nil {
			t.Fatal(err)
		}
		lim, _ := ch1.ReadValue("ch-1/LoadLimit")
		if lim.Double != 100.0 {
			t.Fatalf("cycle %d: expected LoadLimit=100%% at low part-load (no 30%% trap), got %v", i, lim.Double)
		}
	}

	// Explicit PEAK_SHAVE TargetCoolingLoadKW = 350 kW on 500 kW rated -> explicit 70% cap, then clears back to 100%.
	capKW := 350.0
	activeOverride = &ClusterScheduleOverride{Mode: ScheduleModePeakShave, TargetCoolingLoadKW: &capKW}
	if _, err := host.RunCycle(context.Background(), t0.Add(11*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if lim, _ := ch1.ReadValue("ch-1/LoadLimit"); math.Abs(lim.Double-70.0) > 1e-6 {
		t.Fatalf("expected explicit PEAK_SHAVE demand cap 70%%, got %v", lim.Double)
	}

	activeOverride = nil
	if _, err := host.RunCycle(context.Background(), t0.Add(12*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if lim, _ := ch1.ReadValue("ch-1/LoadLimit"); lim.Double != 100.0 {
		t.Fatalf("expected LoadLimit restored to 100%% once PEAK_SHAVE ends, got %v", lim.Double)
	}
}

func TestClusterControllerG36TrimRespondLoopsAndBumplessFallback(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 11, 27, 8, 0, 0, 0, time.UTC)
	ch1 := makeClusterChillerAdapter("ch-1", t0, "RUNNING", 300, false)
	chwp := newStubAutonomyDeviceAdapter(ComponentDescriptor{
		ID: "chwp-1", Enabled: true, Kind: ComponentDeviceDriver, Type: "PUMP",
		FactoryID: DeclarativeModbusFactoryID, Version: "1.0.0", Profiles: []CapabilityProfileID{ProfileVariableSpeedPump},
	}, []ChannelDescriptor{
		clusterChannel("chwp-1", "RunState", "chwp1:run", DataTypeString, AccessReadOnly),
		clusterChannel("chwp-1", "Frequency", "chwp1:act", DataTypeDouble, AccessReadOnly),
		clusterChannel("chwp-1", "LocalRemoteMode", "chwp1:mode", DataTypeString, AccessReadOnly),
		clusterChannel("chwp-1", "FreqSp", "chwp1:freq", DataTypeDouble, AccessReadWrite),
	})
	chwp.SetValue("chwp-1/RunState", StringValue("RUNNING"), QualityGood, t0)
	chwp.SetValue("chwp-1/Frequency", DoubleValue(40.0), QualityGood, t0)
	chwp.SetValue("chwp-1/LocalRemoteMode", StringValue("REMOTE"), QualityGood, t0)
	chwp.SetValue("chwp-1/FreqSp", DoubleValue(40.0), QualityGood, t0)

	cwp := newStubAutonomyDeviceAdapter(ComponentDescriptor{
		ID: "cwp-1", Enabled: true, Kind: ComponentDeviceDriver, Type: "PUMP",
		FactoryID: DeclarativeModbusFactoryID, Version: "1.0.0", Profiles: []CapabilityProfileID{ProfileVariableSpeedPump},
	}, []ChannelDescriptor{
		clusterChannel("cwp-1", "RunState", "cwp1:run", DataTypeString, AccessReadOnly),
		clusterChannel("cwp-1", "Frequency", "cwp1:act", DataTypeDouble, AccessReadOnly),
		clusterChannel("cwp-1", "FreqSp", "cwp1:freq", DataTypeDouble, AccessReadWrite),
	})
	cwp.SetValue("cwp-1/RunState", StringValue("RUNNING"), QualityGood, t0)
	cwp.SetValue("cwp-1/Frequency", DoubleValue(42.0), QualityGood, t0)
	cwp.SetValue("cwp-1/FreqSp", DoubleValue(42.0), QualityGood, t0)

	tower := newStubAutonomyDeviceAdapter(ComponentDescriptor{
		ID: "ct-1", Enabled: true, Kind: ComponentDeviceDriver, Type: "TOWER",
		FactoryID: DeclarativeModbusFactoryID, Version: "1.0.0", Profiles: []CapabilityProfileID{ProfileCoolingTower},
	}, []ChannelDescriptor{
		clusterChannel("ct-1", "RunState", "ct1:run", DataTypeString, AccessReadOnly),
		clusterChannel("ct-1", "FanSpeed", "ct1:act", DataTypeDouble, AccessReadOnly),
		clusterChannel("ct-1", "FanSp", "ct1:fan", DataTypeDouble, AccessReadWrite),
	})
	tower.SetValue("ct-1/RunState", StringValue("RUNNING"), QualityGood, t0)
	tower.SetValue("ct-1/FanSpeed", DoubleValue(60.0), QualityGood, t0)
	tower.SetValue("ct-1/FanSp", DoubleValue(60.0), QualityGood, t0)

	sensors := newStubAutonomyDeviceAdapter(ComponentDescriptor{
		ID: "plant-sensors", Enabled: true, Kind: ComponentDeviceDriver, Type: "THERMAL_ENERGY_METER",
		FactoryID: DeclarativeModbusFactoryID, Version: "1.0.0", Profiles: []CapabilityProfileID{ProfileThermalEnergyMeter},
	}, []ChannelDescriptor{
		clusterChannel("plant-sensors", "chw_sup", "s:chws", DataTypeDouble, AccessReadOnly),
		clusterChannel("plant-sensors", "chw_ret", "s:chwr", DataTypeDouble, AccessReadOnly),
		clusterChannel("plant-sensors", "chw_dp", "s:dp", DataTypeDouble, AccessReadOnly),
		clusterChannel("plant-sensors", "cw_sup", "s:cws", DataTypeDouble, AccessReadOnly),
		clusterChannel("plant-sensors", "cw_ret", "s:cwr", DataTypeDouble, AccessReadOnly),
		clusterChannel("plant-sensors", "wet_bulb", "s:wb", DataTypeDouble, AccessReadOnly),
		clusterChannel("plant-sensors", "dry_bulb", "s:db", DataTypeDouble, AccessReadOnly),
	})
	setSensors := func(at time.Time, chwSup, chwRet, dp, cwSup, cwRet, wb, db float64, wbQuality Quality) {
		sensors.SetValue("plant-sensors/chw_sup", DoubleValue(chwSup), QualityGood, at)
		sensors.SetValue("plant-sensors/chw_ret", DoubleValue(chwRet), QualityGood, at)
		sensors.SetValue("plant-sensors/chw_dp", DoubleValue(dp), QualityGood, at)
		sensors.SetValue("plant-sensors/cw_sup", DoubleValue(cwSup), QualityGood, at)
		sensors.SetValue("plant-sensors/cw_ret", DoubleValue(cwRet), QualityGood, at)
		sensors.SetValue("plant-sensors/wet_bulb", DoubleValue(wb), wbQuality, at)
		sensors.SetValue("plant-sensors/dry_bulb", DoubleValue(db), QualityGood, at)
	}
	setSensors(t0, 7.0, 11.5, 120.0, 29.2, 33.4, 26.0, 31.0, QualityGood)

	host, err := NewHost()
	if err != nil {
		t.Fatal(err)
	}
	for _, ad := range []DeviceAdapter{ch1, chwp, cwp, tower, sensors} {
		if err := host.RegisterAdapter(ad); err != nil {
			t.Fatal(err)
		}
	}

	autonomyPolicy := DefaultAutonomyPolicyConfig()
	autonomyPolicy.Enabled = true
	autonomyPolicy.MinPumpFrequencyHz = 25
	autonomyPolicy.MaxPumpFrequencyHz = 50
	interlock, err := NewHydraulicInterlockController(HydraulicInterlockConfig{
		Policy: autonomyPolicy,
		Chillers: []ChillerInterlockBinding{{
			ComponentID: "ch-1", StartCommand: "ch-1/Start", StopCommand: "ch-1/Stop",
			RunState: "ch-1/RunState", FaultChannel: "ch-1/Fault",
		}},
		ChilledWaterPumps: []PumpInterlockBinding{{
			ComponentID: "chwp-1", RunState: "chwp-1/RunState", FrequencyCommand: "chwp-1/FreqSp",
			LocalRemoteMode: "chwp-1/LocalRemoteMode",
		}},
		CoolingWaterPumps: []PumpInterlockBinding{{
			ComponentID: "cwp-1", RunState: "cwp-1/RunState", FrequencyCommand: "cwp-1/FreqSp",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	policy := DefaultClusterControlPolicy()
	policy.Enabled = true
	policy.MinPumpFrequencyHz = 25.0
	policy.MaxPumpFrequencyHz = 50.0
	policy.TargetChwDeltaTC = 4.5
	policy.TargetChwDeltaPKPa = 120.0
	policy.TargetCwDeltaTC = 4.2
	policy.TargetTowerApproachC = 3.2
	policy.TrimRespondIntervalSeconds = 180
	policy.MaxPumpSlewHzPerMin = 3.0
	policy.MaxTowerSlewPctPerMin = 8.0

	cluster, err := NewChillerPlantClusterController(ClusterControllerConfig{
		Policy: policy,
		Chillers: []ClusterChillerBinding{{
			ComponentID: "ch-1", RatedCoolingKW: 500, StartAddr: "ch-1/Start", StopAddr: "ch-1/Stop",
			RunStateAddr: "ch-1/RunState", FaultAddr: "ch-1/Fault", SetpointAddr: "ch-1/SupplySp",
			LoadLimitAddr: "ch-1/LoadLimit", CoolingCapacityAddr: "ch-1/CapacityKW",
		}},
		Pumps: []ClusterPumpBinding{
			{ComponentID: "chwp-1", Loop: ClusterPumpLoopCHW, FrequencyAddr: "chwp-1/FreqSp", ActualFrequencyAddr: "chwp-1/Frequency", RunStateAddr: "chwp-1/RunState", LocalRemoteModeAddr: "chwp-1/LocalRemoteMode"},
			{ComponentID: "cwp-1", Loop: ClusterPumpLoopCW, FrequencyAddr: "cwp-1/FreqSp", ActualFrequencyAddr: "cwp-1/Frequency", RunStateAddr: "cwp-1/RunState"},
		},
		Towers: []ClusterTowerBinding{{
			ComponentID: "ct-1", FanSpeedAddr: "ct-1/FanSp", ActualFanSpeedAddr: "ct-1/FanSpeed", RunStateAddr: "ct-1/RunState",
		}},
		Sensors: ClusterSensorBindings{
			CHWSupplyTempAddr:  "plant-sensors/chw_sup",
			CHWReturnTempAddr:  "plant-sensors/chw_ret",
			CHWDeltaPAddr:      "plant-sensors/chw_dp",
			CWLeavingTempAddr:  "plant-sensors/cw_sup",
			CWEnteringTempAddr: "plant-sensors/cw_ret",
			WetBulbTempAddr:    "plant-sensors/wet_bulb",
			OutdoorTempAddr:    "plant-sensors/dry_bulb",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := host.Start([]ControllerBinding{
		{Priority: 10, Critical: true, Controller: interlock},
		{Priority: 60, Controller: cluster},
	}); err != nil {
		t.Fatal(err)
	}

	// Cycle 0 (t0): bumpless initial seed from live frequency/speed.
	if _, err := host.RunCycle(context.Background(), t0); err != nil {
		t.Fatal(err)
	}
	if fq, _ := chwp.ReadValue("chwp-1/FreqSp"); fq.Double != 40.0 {
		t.Fatalf("expected bumpless initial CHWP=40.0Hz, got %v", fq.Double)
	}

	// Inside deadband (CHW DeltaT=4.6C, DP=120 kPa, CW DeltaT=4.2C, Approach=3.2C): hold steady!
	t1 := t0.Add(3 * time.Minute)
	setSensors(t1, 7.0, 11.6, 120.0, 29.2, 33.4, 26.0, 31.0, QualityGood)
	if _, err := host.RunCycle(context.Background(), t1); err != nil {
		t.Fatal(err)
	}
	if fq, _ := chwp.ReadValue("chwp-1/FreqSp"); fq.Double != 40.0 {
		t.Fatalf("expected deadband hold at 40.0Hz, got %v", fq.Double)
	}
	if fq, _ := cwp.ReadValue("cwp-1/FreqSp"); fq.Double != 42.0 {
		t.Fatalf("expected CWP deadband hold at 42.0Hz, got %v", fq.Double)
	}
	if fan, _ := tower.ReadValue("ct-1/FanSp"); fan.Double != 60.0 {
		t.Fatalf("expected tower deadband hold at 60%%, got %v", fan.Double)
	}

	// High CHW DeltaT (6.2C) + low DP (95 kPa) + high CW DeltaT (5.8C) + wide Approach (5.2C):
	// Respond UP smoothly (bounded by max step & slew limit).
	t2 := t1.Add(3 * time.Minute)
	setSensors(t2, 7.0, 13.2, 95.0, 31.2, 37.0, 26.0, 31.0, QualityGood)
	if _, err := host.RunCycle(context.Background(), t2); err != nil {
		t.Fatal(err)
	}
	if fq, _ := chwp.ReadValue("chwp-1/FreqSp"); fq.Double <= 40.0 || fq.Double > 43.5 {
		t.Fatalf("expected smooth CHWP Respond step in (40,43.5], got %v", fq.Double)
	}
	if fq, _ := cwp.ReadValue("cwp-1/FreqSp"); fq.Double <= 42.0 || fq.Double > 45.5 {
		t.Fatalf("expected smooth CWP Respond step in (42,45.5], got %v", fq.Double)
	}
	if fan, _ := tower.ReadValue("ct-1/FanSp"); fan.Double <= 60.0 || fan.Double > 68.0 {
		t.Fatalf("expected smooth tower Respond step in (60,68], got %v", fan.Double)
	}

	// Missing secondary WetBulb sensor (QualityBad): tower falls back safely to DryBulb target without panic or jump.
	t3 := t2.Add(3 * time.Minute)
	fanBeforeFallback, _ := tower.ReadValue("ct-1/FanSp")
	setSensors(t3, 7.0, 11.5, 120.0, 29.2, 33.4, 26.0, 31.0, QualityBad)
	if _, err := host.RunCycle(context.Background(), t3); err != nil {
		t.Fatal(err)
	}
	if fanAfter, _ := tower.ReadValue("ct-1/FanSp"); math.Abs(fanAfter.Double-fanBeforeFallback.Double) > 4.0 {
		t.Fatalf("missing secondary WetBulb caused abrupt jump from %.2f to %.2f", fanBeforeFallback.Double, fanAfter.Double)
	}

	// Local manual takeover on CHWP at 47.0Hz: interlock denies remote writes, cluster syncs bumplessly on REMOTE return.
	t4 := t3.Add(3 * time.Minute)
	chwp.SetValue("chwp-1/LocalRemoteMode", StringValue("LOCAL"), QualityGood, t4)
	chwp.SetValue("chwp-1/Frequency", DoubleValue(47.0), QualityGood, t4)
	chwp.SetValue("chwp-1/FreqSp", DoubleValue(47.0), QualityGood, t4)
	setSensors(t4, 7.0, 9.5, 140.0, 29.0, 32.0, 26.0, 31.0, QualityGood)
	if _, err := host.RunCycle(context.Background(), t4); err != nil {
		t.Fatal(err)
	}
	if fq, _ := chwp.ReadValue("chwp-1/FreqSp"); fq.Double != 47.0 {
		t.Fatalf("LOCAL manual CHWP dial 47.0Hz must be preserved, got %v", fq.Double)
	}

	t5 := t4.Add(3 * time.Minute)
	chwp.SetValue("chwp-1/LocalRemoteMode", StringValue("REMOTE"), QualityGood, t5)
	setSensors(t5, 7.0, 9.5, 140.0, 29.0, 32.0, 26.0, 31.0, QualityGood)
	if _, err := host.RunCycle(context.Background(), t5); err != nil {
		t.Fatal(err)
	}
	if fq, _ := chwp.ReadValue("chwp-1/FreqSp"); fq.Double < 45.0 || fq.Double >= 47.0 {
		t.Fatalf("expected bumpless trim down from 47.0Hz after REMOTE restore, got %v", fq.Double)
	}
}

func TestClusterControlPolicyWave2JSONCompatibilityAndResetBounds(t *testing.T) {
	t.Parallel()

	// Exact Wave-2 11-field JSON payload (omits all G36 optional fields) must decode and validate cleanly.
	wave2JSON := `{"enabled":true,"minRunningChillers":1,"maxRunningChillers":3,"stageUpLoadPct":85,"stageDownLoadPct":40,"stageDwellSeconds":300,"defaultSupplySetpointC":7.0,"minPumpFrequencyHz":28,"maxPumpFrequencyHz":50,"minTowerFanSpeedPct":30,"maxTowerFanSpeedPct":100}`
	var decoded ClusterControlPolicy
	if err := json.Unmarshal([]byte(wave2JSON), &decoded); err != nil {
		t.Fatalf("Wave 2 ClusterControlPolicy unmarshal failed: %v", err)
	}
	if err := decoded.Validate(); err != nil {
		t.Fatalf("Wave 2 ClusterControlPolicy validation failed: %v", err)
	}

	bad := DefaultClusterControlPolicy()
	bad.TargetChwDeltaTC = 0.5
	if err := bad.Validate(); err == nil {
		t.Fatal("expected out-of-range targetChwDeltaTC to fail validation")
	}
	badApproach := DefaultClusterControlPolicy()
	badApproach.TargetTowerApproachC = 20.0
	if err := badApproach.Validate(); err == nil {
		t.Fatal("expected out-of-range targetTowerApproachC to fail validation")
	}
}
