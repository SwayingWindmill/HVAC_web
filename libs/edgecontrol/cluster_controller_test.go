package edgecontrol

import (
	"context"
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
