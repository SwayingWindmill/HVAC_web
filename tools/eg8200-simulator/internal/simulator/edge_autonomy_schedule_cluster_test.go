package simulator

import (
	"context"
	"math"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

type simPlantTestAdapter struct {
	mu        sync.Mutex
	component edgecontrol.ComponentDescriptor
	channels  []edgecontrol.ChannelDescriptor
	samples   map[string]edgecontrol.Sample
	values    map[string]edgecontrol.Value
}

func newSimPlantTestAdapter(component edgecontrol.ComponentDescriptor, channels []edgecontrol.ChannelDescriptor) *simPlantTestAdapter {
	return &simPlantTestAdapter{
		component: component,
		channels:  slices.Clone(channels),
		samples:   make(map[string]edgecontrol.Sample, len(channels)),
		values:    make(map[string]edgecontrol.Value, len(channels)),
	}
}

func (a *simPlantTestAdapter) Component() edgecontrol.ComponentDescriptor { return a.component }
func (a *simPlantTestAdapter) Channels() []edgecontrol.ChannelDescriptor {
	return slices.Clone(a.channels)
}

func (a *simPlantTestAdapter) Set(address string, val edgecontrol.Value, quality edgecontrol.Quality, at time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.values[address] = val
	a.samples[address] = edgecontrol.Sample{Value: val, Quality: quality, ObservedAt: at}
}

func (a *simPlantTestAdapter) Read(address string) (edgecontrol.Value, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	v, ok := a.values[address]
	return v, ok
}

func (a *simPlantTestAdapter) Poll(_ context.Context, at time.Time) ([]edgecontrol.ChannelUpdate, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]edgecontrol.ChannelUpdate, 0, len(a.channels))
	for _, ch := range a.channels {
		addr := ch.Address()
		sample, ok := a.samples[addr]
		if !ok {
			continue
		}
		if sample.ObservedAt.IsZero() {
			sample.ObservedAt = at
		}
		out = append(out, edgecontrol.ChannelUpdate{Address: addr, Sample: sample})
	}
	return out, nil
}

func (a *simPlantTestAdapter) Apply(_ context.Context, _ edgecontrol.ProcessImage, decisions []edgecontrol.Decision) ([]edgecontrol.DeviceWriteResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	results := make([]edgecontrol.DeviceWriteResult, 0, len(decisions))
	for _, d := range decisions {
		if !d.Accepted || d.Effective == nil {
			continue
		}
		val := *d.Effective
		a.values[d.Address] = val
		prev := a.samples[d.Address]
		prev.Value = val
		prev.Quality = edgecontrol.QualityGood
		a.samples[d.Address] = prev
		results = append(results, edgecontrol.DeviceWriteResult{
			Address:      d.Address,
			Success:      true,
			Code:         "APPLIED",
			AppliedValue: &val,
		})
	}
	return results, nil
}

func simChannel(comp, id, ptID string, dt edgecontrol.DataType, access edgecontrol.AccessMode, poll, resend edgecontrol.DataPriority) edgecontrol.ChannelDescriptor {
	return edgecontrol.ChannelDescriptor{
		ComponentID:               comp,
		ChannelID:                 id,
		PointID:                   ptID,
		DataType:                  dt,
		Access:                    access,
		Category:                  edgecontrol.ChannelCategoryOpenemsType,
		PollPriority:              poll,
		LocalPersistencePriority:  poll,
		RemotePersistencePriority: resend,
		AggregationPriority:       resend,
		ResendPriority:            resend,
	}
}

func makeSimChiller(id string, at time.Time, runState string, coolingKW float64) *simPlantTestAdapter {
	ad := newSimPlantTestAdapter(
		edgecontrol.ComponentDescriptor{
			ID:        id,
			Enabled:   true,
			Kind:      edgecontrol.ComponentDeviceDriver,
			Type:      "CHILLER",
			FactoryID: edgecontrol.DeclarativeModbusFactoryID,
			Version:   "1.0.0",
			Profiles:  []edgecontrol.CapabilityProfileID{edgecontrol.ProfileChiller},
		},
		[]edgecontrol.ChannelDescriptor{
			simChannel(id, "RunState", id+":run", edgecontrol.DataTypeString, edgecontrol.AccessReadOnly, edgecontrol.PriorityHigh, edgecontrol.PriorityHigh),
			simChannel(id, "Fault", id+":fault", edgecontrol.DataTypeBoolean, edgecontrol.AccessReadOnly, edgecontrol.PriorityVeryHigh, edgecontrol.PriorityVeryHigh),
			simChannel(id, "Start", id+":start", edgecontrol.DataTypeBoolean, edgecontrol.AccessReadWrite, edgecontrol.PriorityHigh, edgecontrol.PriorityHigh),
			simChannel(id, "Stop", id+":stop", edgecontrol.DataTypeBoolean, edgecontrol.AccessReadWrite, edgecontrol.PriorityHigh, edgecontrol.PriorityHigh),
			simChannel(id, "SupplySp", id+":sp", edgecontrol.DataTypeDouble, edgecontrol.AccessReadWrite, edgecontrol.PriorityHigh, edgecontrol.PriorityHigh),
			simChannel(id, "LoadLimit", id+":limit", edgecontrol.DataTypeDouble, edgecontrol.AccessReadWrite, edgecontrol.PriorityHigh, edgecontrol.PriorityHigh),
			simChannel(id, "CapacityKW", id+":cap", edgecontrol.DataTypeDouble, edgecontrol.AccessReadOnly, edgecontrol.PriorityHigh, edgecontrol.PriorityMedium),
		},
	)
	ad.Set(id+"/RunState", edgecontrol.StringValue(runState), edgecontrol.QualityGood, at)
	ad.Set(id+"/Fault", edgecontrol.BooleanValue(false), edgecontrol.QualityGood, at)
	ad.Set(id+"/Start", edgecontrol.BooleanValue(false), edgecontrol.QualityGood, at)
	ad.Set(id+"/Stop", edgecontrol.BooleanValue(false), edgecontrol.QualityGood, at)
	ad.Set(id+"/SupplySp", edgecontrol.DoubleValue(7.0), edgecontrol.QualityGood, at)
	ad.Set(id+"/LoadLimit", edgecontrol.DoubleValue(80.0), edgecontrol.QualityGood, at)
	ad.Set(id+"/CapacityKW", edgecontrol.DoubleValue(coolingKW), edgecontrol.QualityGood, at)
	return ad
}

func makeSimPump(id string, at time.Time, runState string, flow float64) *simPlantTestAdapter {
	ad := newSimPlantTestAdapter(
		edgecontrol.ComponentDescriptor{
			ID:        id,
			Enabled:   true,
			Kind:      edgecontrol.ComponentDeviceDriver,
			Type:      "PUMP",
			FactoryID: edgecontrol.DeclarativeModbusFactoryID,
			Version:   "1.0.0",
			Profiles:  []edgecontrol.CapabilityProfileID{edgecontrol.ProfileVariableSpeedPump},
		},
		[]edgecontrol.ChannelDescriptor{
			simChannel(id, "RunState", id+":run", edgecontrol.DataTypeString, edgecontrol.AccessReadOnly, edgecontrol.PriorityHigh, edgecontrol.PriorityHigh),
			simChannel(id, "Flow", id+":flow", edgecontrol.DataTypeDouble, edgecontrol.AccessReadOnly, edgecontrol.PriorityVeryHigh, edgecontrol.PriorityVeryHigh),
			simChannel(id, "Fault", id+":fault", edgecontrol.DataTypeBoolean, edgecontrol.AccessReadOnly, edgecontrol.PriorityVeryHigh, edgecontrol.PriorityVeryHigh),
			simChannel(id, "FreqSp", id+":freq", edgecontrol.DataTypeDouble, edgecontrol.AccessReadWrite, edgecontrol.PriorityHigh, edgecontrol.PriorityHigh),
		},
	)
	ad.Set(id+"/RunState", edgecontrol.StringValue(runState), edgecontrol.QualityGood, at)
	ad.Set(id+"/Flow", edgecontrol.DoubleValue(flow), edgecontrol.QualityGood, at)
	ad.Set(id+"/Fault", edgecontrol.BooleanValue(false), edgecontrol.QualityGood, at)
	ad.Set(id+"/FreqSp", edgecontrol.DoubleValue(35.0), edgecontrol.QualityGood, at)
	return ad
}

func makeSimTower(id string, at time.Time) *simPlantTestAdapter {
	ad := newSimPlantTestAdapter(
		edgecontrol.ComponentDescriptor{
			ID:        id,
			Enabled:   true,
			Kind:      edgecontrol.ComponentDeviceDriver,
			Type:      "TOWER",
			FactoryID: edgecontrol.DeclarativeModbusFactoryID,
			Version:   "1.0.0",
			Profiles:  []edgecontrol.CapabilityProfileID{edgecontrol.ProfileCoolingTower},
		},
		[]edgecontrol.ChannelDescriptor{
			simChannel(id, "RunState", id+":run", edgecontrol.DataTypeString, edgecontrol.AccessReadOnly, edgecontrol.PriorityHigh, edgecontrol.PriorityHigh),
			simChannel(id, "Fault", id+":fault", edgecontrol.DataTypeBoolean, edgecontrol.AccessReadOnly, edgecontrol.PriorityVeryHigh, edgecontrol.PriorityVeryHigh),
			simChannel(id, "FanSp", id+":fan", edgecontrol.DataTypeDouble, edgecontrol.AccessReadWrite, edgecontrol.PriorityHigh, edgecontrol.PriorityHigh),
		},
	)
	ad.Set(id+"/RunState", edgecontrol.StringValue("RUNNING"), edgecontrol.QualityGood, at)
	ad.Set(id+"/Fault", edgecontrol.BooleanValue(false), edgecontrol.QualityGood, at)
	ad.Set(id+"/FanSp", edgecontrol.DoubleValue(50.0), edgecontrol.QualityGood, at)
	return ad
}

func TestSimulatorMultiChillerAutonomyScheduleClusterAndPreAggregation(t *testing.T) {
	t.Parallel()

	day := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
	start := day.Add(6 * time.Hour)

	ch1 := makeSimChiller("ch-1", start, "RUNNING", 300)
	ch2 := makeSimChiller("ch-2", start, "STOPPED", 0)
	pump1 := makeSimPump("chwp-1", start, "RUNNING", 140)
	pump2 := makeSimPump("chwp-2", start, "RUNNING", 140)
	tower1 := makeSimTower("ct-1", start)
	tower2 := makeSimTower("ct-2", start)
	weather := newSimPlantTestAdapter(
		edgecontrol.ComponentDescriptor{
			ID:        "ws-1",
			Enabled:   true,
			Kind:      edgecontrol.ComponentDeviceDriver,
			Type:      "WEATHER",
			FactoryID: edgecontrol.DeclarativeModbusFactoryID,
			Version:   "1.0.0",
			Profiles:  []edgecontrol.CapabilityProfileID{edgecontrol.ProfileWeatherStation},
		},
		[]edgecontrol.ChannelDescriptor{
			simChannel("ws-1", "outdoor_temp", "ws1:temp", edgecontrol.DataTypeDouble, edgecontrol.AccessReadOnly, edgecontrol.PriorityHigh, edgecontrol.PriorityLow),
		},
	)
	weather.Set("ws-1/outdoor_temp", edgecontrol.DoubleValue(21.0), edgecontrol.QualityGood, start)

	meter := newSimPlantTestAdapter(
		edgecontrol.ComponentDescriptor{
			ID:        "meter-1",
			Enabled:   true,
			Kind:      edgecontrol.ComponentDeviceDriver,
			Type:      "METER",
			FactoryID: edgecontrol.DeclarativeModbusFactoryID,
			Version:   "1.0.0",
			Profiles:  []edgecontrol.CapabilityProfileID{edgecontrol.ProfileElectricityMeter},
		},
		[]edgecontrol.ChannelDescriptor{
			simChannel("meter-1", "total_kwh", "meter1:kwh", edgecontrol.DataTypeDouble, edgecontrol.AccessReadOnly, edgecontrol.PriorityLow, edgecontrol.PriorityLow),
		},
	)
	meter.Set("meter-1/total_kwh", edgecontrol.DoubleValue(1024.5), edgecontrol.QualityGood, start)

	host, err := edgecontrol.NewHost()
	if err != nil {
		t.Fatal(err)
	}
	for _, ad := range []edgecontrol.DeviceAdapter{ch1, ch2, pump1, pump2, tower1, tower2, weather, meter} {
		if err := host.RegisterAdapter(ad); err != nil {
			t.Fatal(err)
		}
	}

	autonomyPolicy := edgecontrol.DefaultAutonomyPolicyConfig()
	autonomyPolicy.Enabled = true
	autonomyPolicy.OfflineGraceSeconds = 60
	autonomyPolicy.DefaultChilledWaterSupplySetpointC = 7.0
	autonomyPolicy.DefaultPumpFrequencyHz = 36.0
	autonomyPolicy.MinPumpFrequencyHz = 25.0
	autonomyPolicy.MaxPumpFrequencyHz = 50.0

	interlock, err := edgecontrol.NewHydraulicInterlockController(edgecontrol.HydraulicInterlockConfig{
		Policy: autonomyPolicy,
		Chillers: []edgecontrol.ChillerInterlockBinding{
			{ComponentID: "ch-1", StartCommand: "ch-1/Start", StopCommand: "ch-1/Stop", RunState: "ch-1/RunState", FaultChannel: "ch-1/Fault"},
			{ComponentID: "ch-2", StartCommand: "ch-2/Start", StopCommand: "ch-2/Stop", RunState: "ch-2/RunState", FaultChannel: "ch-2/Fault"},
		},
		ChilledWaterPumps: []edgecontrol.PumpInterlockBinding{
			{ComponentID: "chwp-1", RunState: "chwp-1/RunState", FlowFeedback: "chwp-1/Flow", FaultChannel: "chwp-1/Fault", FrequencyCommand: "chwp-1/FreqSp", MinFlowRate: 20},
			{ComponentID: "chwp-2", RunState: "chwp-2/RunState", FlowFeedback: "chwp-2/Flow", FaultChannel: "chwp-2/Fault", FrequencyCommand: "chwp-2/FreqSp", MinFlowRate: 20},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	chillerFallbacks := []edgecontrol.ChillerFallbackTarget{
		{ComponentID: "ch-1", SupplySetpointAddr: "ch-1/SupplySp", RunStateAddr: "ch-1/RunState", FaultAddr: "ch-1/Fault"},
		{ComponentID: "ch-2", SupplySetpointAddr: "ch-2/SupplySp", RunStateAddr: "ch-2/RunState", FaultAddr: "ch-2/Fault"},
	}
	pumpFallbacks := []edgecontrol.PumpFallbackTarget{
		{ComponentID: "chwp-1", FrequencyAddr: "chwp-1/FreqSp", RunStateAddr: "chwp-1/RunState", FaultAddr: "chwp-1/Fault"},
		{ComponentID: "chwp-2", FrequencyAddr: "chwp-2/FreqSp", RunStateAddr: "chwp-2/RunState", FaultAddr: "chwp-2/Fault"},
	}
	towerBindings := []edgecontrol.ClusterTowerBinding{
		{ComponentID: "ct-1", FanSpeedAddr: "ct-1/FanSp", RunStateAddr: "ct-1/RunState", FaultAddr: "ct-1/Fault"},
		{ComponentID: "ct-2", FanSpeedAddr: "ct-2/FanSp", RunStateAddr: "ct-2/RunState", FaultAddr: "ct-2/Fault"},
	}

	fallback, err := edgecontrol.NewAutonomousFallbackController(edgecontrol.AutonomousFallbackConfig{
		Policy:             autonomyPolicy,
		CloudConnected:     true,
		LastCloudContactAt: start,
		Chillers:           chillerFallbacks,
		Pumps:              pumpFallbacks,
	})
	if err != nil {
		t.Fatal(err)
	}

	preCoolSP := 6.0
	peakShaveSP := 8.5
	pumpMin := 32.0
	pumpMax := 44.0
	oneChiller := 1

	schedCtrl := edgecontrol.NewEnergyScheduleController("")
	if err := schedCtrl.UpdateSchedule(&edgecontrol.EdgeEnergySchedule{
		ScheduleID:                "sim-plant-sched",
		Version:                   1,
		IssuedAt:                  start,
		ValidFrom:                 start,
		ValidUntil:                start.Add(8 * time.Hour),
		LeaseExpiresAt:            day.Add(10 * time.Hour),
		ContinueOfflineWhileValid: true,
		Slots: []edgecontrol.EnergyScheduleSlot{
			{SlotID: "precool", StartMinute: 6 * 60, EndMinute: 8 * 60, Mode: edgecontrol.ScheduleModePreCool, SupplyTempSetpointC: &preCoolSP, PumpFrequencyMinHz: &pumpMin, PumpFrequencyMaxHz: &pumpMax},
			{SlotID: "peak", StartMinute: 8 * 60, EndMinute: 12 * 60, Mode: edgecontrol.ScheduleModePeakShave, SupplyTempSetpointC: &peakShaveSP, MaxRunningChillers: &oneChiller},
		},
	}); err != nil {
		t.Fatal(err)
	}
	schedCtrl.UpdateTargets(chillerFallbacks, pumpFallbacks, towerBindings)
	fallback.SetScheduleActiveFn(schedCtrl.IsActiveAt)

	clusterPolicy := edgecontrol.DefaultClusterControlPolicy()
	clusterPolicy.Enabled = true
	clusterPolicy.MinRunningChillers = 1
	clusterPolicy.MaxRunningChillers = 2
	clusterPolicy.StageUpLoadPct = 85
	clusterPolicy.StageDownLoadPct = 40
	clusterPolicy.StageDwellSeconds = 300
	cluster, err := edgecontrol.NewChillerPlantClusterController(edgecontrol.ClusterControllerConfig{
		Policy: clusterPolicy,
		Chillers: []edgecontrol.ClusterChillerBinding{
			{ComponentID: "ch-1", RatedCoolingKW: 500, StartAddr: "ch-1/Start", StopAddr: "ch-1/Stop", RunStateAddr: "ch-1/RunState", FaultAddr: "ch-1/Fault", SetpointAddr: "ch-1/SupplySp", LoadLimitAddr: "ch-1/LoadLimit", CoolingCapacityAddr: "ch-1/CapacityKW"},
			{ComponentID: "ch-2", RatedCoolingKW: 500, StartAddr: "ch-2/Start", StopAddr: "ch-2/Stop", RunStateAddr: "ch-2/RunState", FaultAddr: "ch-2/Fault", SetpointAddr: "ch-2/SupplySp", LoadLimitAddr: "ch-2/LoadLimit", CoolingCapacityAddr: "ch-2/CapacityKW"},
		},
		Pumps: []edgecontrol.ClusterPumpBinding{
			{ComponentID: "chwp-1", FrequencyAddr: "chwp-1/FreqSp", RunStateAddr: "chwp-1/RunState", FaultAddr: "chwp-1/Fault"},
			{ComponentID: "chwp-2", FrequencyAddr: "chwp-2/FreqSp", RunStateAddr: "chwp-2/RunState", FaultAddr: "chwp-2/Fault"},
		},
		Towers: towerBindings,
	})
	if err != nil {
		t.Fatal(err)
	}
	cluster.SetScheduleOverrideFn(schedCtrl.ActiveOverrideAt)

	intentController, err := edgecontrol.NewIntentController("cloud-command", host.IntentStore())
	if err != nil {
		t.Fatal(err)
	}
	if err := host.Start([]edgecontrol.ControllerBinding{
		{Priority: 10, Critical: true, Controller: interlock},
		{Priority: 20, Controller: fallback},
		{Priority: 40, Controller: intentController},
		{Priority: 50, Controller: schedCtrl},
		{Priority: 60, Controller: cluster},
	}); err != nil {
		t.Fatal(err)
	}

	agg, err := edgecontrol.NewWindowAggregator(edgecontrol.EdgeAggregationPolicy{
		Enabled: true,
		Points: []edgecontrol.AggregationPointPolicy{
			{DeviceKey: "ws-1", PointCode: "outdoor_temp", WindowSeconds: 60, EmitMinMax: true},
			{DeviceKey: "meter-1", PointCode: "*", WindowSeconds: 60},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	agg.SetChannelSemantics(map[string]edgecontrol.SemanticChannel{
		"meter-1/total_kwh": edgecontrol.SemanticEnergy,
	})

	observeImage := func(img edgecontrol.ProcessImage, at time.Time) (passedRaw []string, flushed []edgecontrol.AggregatedWindowOutput) {
		for _, snap := range img.Channels() {
			if !snap.HasValue {
				continue
			}
			pass, closed := agg.Observe(edgecontrol.AggregationSample{
				DeviceKey:  snap.Descriptor.ComponentID,
				PointCode:  snap.Descriptor.ChannelID,
				Address:    snap.Descriptor.Address(),
				Descriptor: snap.Descriptor,
				Value:      snap.Sample.Value,
				Quality:    snap.Sample.Quality,
				ObservedAt: snap.Sample.ObservedAt,
			})
			flushed = append(flushed, closed...)
			if pass {
				passedRaw = append(passedRaw, snap.Descriptor.Address())
			}
		}
		flushed = append(flushed, agg.FlushDue(at)...)
		return passedRaw, flushed
	}

	// 1. 06:00:00..06:00:40: feed 3 outdoor_temp samples inside 60s window; verify buffered & energy stays raw.
	temps := []float64{21.0, 25.0, 23.0}
	for i, tempVal := range temps {
		ts := start.Add(time.Duration(i*20) * time.Second)
		weather.Set("ws-1/outdoor_temp", edgecontrol.DoubleValue(tempVal), edgecontrol.QualityGood, ts)
		meter.Set("meter-1/total_kwh", edgecontrol.DoubleValue(1025+float64(i)), edgecontrol.QualityGood, ts)
		res, err := host.RunCycle(context.Background(), ts)
		if err != nil {
			t.Fatal(err)
		}
		raw, flushed := observeImage(res.Cycle.Image, ts)
		if len(flushed) != 0 {
			t.Fatalf("did not expect window flush inside minute at step %d: %+v", i, flushed)
		}
		if slices.Contains(raw, "ws-1/outdoor_temp") {
			t.Fatalf("expected ws-1/outdoor_temp aggregated inside minute, got raw %v", raw)
		}
		if !slices.Contains(raw, "meter-1/total_kwh") || !slices.Contains(raw, "ch-1/SupplySp") {
			t.Fatalf("expected energy and setpoint channels preserved raw, got %v", raw)
		}
	}

	// 2. 06:01:00: new outdoor_temp sample closes [06:00, 06:01) window (mean=23, min=21, max=25).
	tFlush := start.Add(time.Minute)
	weather.Set("ws-1/outdoor_temp", edgecontrol.DoubleValue(28.0), edgecontrol.QualityGood, tFlush)
	resFlush, err := host.RunCycle(context.Background(), tFlush)
	if err != nil {
		t.Fatal(err)
	}
	_, flushed := observeImage(resFlush.Cycle.Image, tFlush)
	if len(flushed) != 1 || math.Abs(flushed[0].Mean-23.0) > 1e-9 || flushed[0].Min != 21.0 || flushed[0].Max != 25.0 {
		t.Fatalf("unexpected 60s flushed window: %+v", flushed)
	}

	// 3. 06:02:00: raise load on ch-1 to 450kW (90% >= 85%): cluster stages up ch-2 & PRE_COOL clamps pump to 44.0Hz.
	tStageUp := start.Add(2 * time.Minute)
	ch1.Set("ch-1/CapacityKW", edgecontrol.DoubleValue(450.0), edgecontrol.QualityGood, tStageUp)
	if _, err := host.RunCycle(context.Background(), tStageUp); err != nil {
		t.Fatal(err)
	}
	if start2, _ := ch2.Read("ch-2/Start"); !start2.Boolean {
		t.Fatal("expected cluster controller to stage up standby chiller ch-2 at 90% load")
	}
	if fq, _ := pump1.Read("chwp-1/FreqSp"); fq.Double != 44.0 {
		t.Fatalf("expected PRE_COOL schedule constraint clamping pump freq to 44.0Hz, got %v", fq.Double)
	}

	// 4. 06:03:00 (within 300s dwell): load drops to 30% -> dwell blocks stage-down; 06:08:00 (>300s): ch-1 stops (higher runtime).
	tWithinDwell := tStageUp.Add(time.Minute)
	ch2.Set("ch-2/RunState", edgecontrol.StringValue("RUNNING"), edgecontrol.QualityGood, tWithinDwell)
	ch1.Set("ch-1/CapacityKW", edgecontrol.DoubleValue(150.0), edgecontrol.QualityGood, tWithinDwell)
	ch2.Set("ch-2/CapacityKW", edgecontrol.DoubleValue(150.0), edgecontrol.QualityGood, tWithinDwell)
	if _, err := host.RunCycle(context.Background(), tWithinDwell); err != nil {
		t.Fatal(err)
	}
	if s1, _ := ch1.Read("ch-1/Stop"); s1.Boolean {
		t.Fatal("expected 300s dwell timer to block stage-down at +60s")
	}

	tPastDwell := tStageUp.Add(6 * time.Minute)
	ch1.Set("ch-1/CapacityKW", edgecontrol.DoubleValue(150.0), edgecontrol.QualityGood, tPastDwell)
	ch2.Set("ch-2/CapacityKW", edgecontrol.DoubleValue(150.0), edgecontrol.QualityGood, tPastDwell)
	if _, err := host.RunCycle(context.Background(), tPastDwell); err != nil {
		t.Fatal(err)
	}
	if s1, _ := ch1.Read("ch-1/Stop"); !s1.Boolean {
		t.Fatal("expected highest-runtime chiller ch-1 staged down after 300s dwell")
	}
	ch1.Set("ch-1/RunState", edgecontrol.StringValue("STOPPED"), edgecontrol.QualityGood, tPastDwell.Add(time.Second))

	// 5. 08:30:00 PEAK_SHAVE -> setpoint 8.5C; offline at 09:00 retains 8.5C; lease expiry at 10:05 hands off to fallback 7.0C.
	tPeak := day.Add(8*time.Hour + 30*time.Minute)
	if _, err := host.RunCycle(context.Background(), tPeak); err != nil {
		t.Fatal(err)
	}
	if sp2, _ := ch2.Read("ch-2/SupplySp"); sp2.Double != 8.5 {
		t.Fatalf("expected PEAK_SHAVE setpoint 8.5C, got %v", sp2.Double)
	}

	tOffline := day.Add(9 * time.Hour)
	schedCtrl.SetCloudConnected(false, tOffline)
	fallback.SetCloudConnected(false, tOffline)
	if _, err := host.RunCycle(context.Background(), tOffline.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if !schedCtrl.IsActiveAt(tOffline.Add(5*time.Minute)) || fallback.StatusAt(tOffline.Add(5*time.Minute)).FallbackActive {
		t.Fatal("expected schedule continuation and fallback standby before lease expiry")
	}

	tExpired := day.Add(10*time.Hour + 5*time.Minute)
	if _, err := host.RunCycle(context.Background(), tExpired); err != nil {
		t.Fatal(err)
	}
	if schedCtrl.IsActiveAt(tExpired) || !fallback.StatusAt(tExpired).FallbackActive {
		t.Fatal("expected AutonomousFallbackController active after offline schedule lease expiry")
	}
	if sp2, _ := ch2.Read("ch-2/SupplySp"); sp2.Double != 7.0 {
		t.Fatalf("expected fallback supply setpoint 7.0C after lease expiry, got %v", sp2.Double)
	}

	// 6. Priority-10 HydraulicInterlock denies chiller START when all CHW pumps lose flow.
	tLossFlow := tExpired.Add(time.Minute)
	ch1.Set("ch-1/RunState", edgecontrol.StringValue("STOPPED"), edgecontrol.QualityGood, tLossFlow)
	ch2.Set("ch-2/RunState", edgecontrol.StringValue("STOPPED"), edgecontrol.QualityGood, tLossFlow)
	ch1.Set("ch-1/Start", edgecontrol.BooleanValue(false), edgecontrol.QualityGood, tLossFlow)
	ch2.Set("ch-2/Start", edgecontrol.BooleanValue(false), edgecontrol.QualityGood, tLossFlow)
	pump1.Set("chwp-1/RunState", edgecontrol.StringValue("STOPPED"), edgecontrol.QualityGood, tLossFlow)
	pump1.Set("chwp-1/Flow", edgecontrol.DoubleValue(0), edgecontrol.QualityGood, tLossFlow)
	pump2.Set("chwp-2/RunState", edgecontrol.StringValue("STOPPED"), edgecontrol.QualityGood, tLossFlow)
	pump2.Set("chwp-2/Flow", edgecontrol.DoubleValue(0), edgecontrol.QualityGood, tLossFlow)
	if _, err := host.RunCycle(context.Background(), tLossFlow); err != nil {
		t.Fatal(err)
	}
	st1, _ := ch1.Read("ch-1/Start")
	st2, _ := ch2.Read("ch-2/Start")
	if st1.Boolean || st2.Boolean {
		t.Fatal("Priority-10 HydraulicInterlock must block chiller stage-up when CHW flow=0")
	}
}
