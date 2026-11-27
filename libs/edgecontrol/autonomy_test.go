package edgecontrol

import (
	"context"
	"math"
	"slices"
	"sync"
	"testing"
	"time"
)

type stubAutonomyDeviceAdapter struct {
	mu                 sync.Mutex
	component          ComponentDescriptor
	channels           []ChannelDescriptor
	samples            map[string]Sample
	values             map[string]Value
	preserveObservedAt bool
}

func newStubAutonomyDeviceAdapter(component ComponentDescriptor, channels []ChannelDescriptor) *stubAutonomyDeviceAdapter {
	return &stubAutonomyDeviceAdapter{
		component: cloneComponent(component),
		channels:  slices.Clone(channels),
		samples:   make(map[string]Sample, len(channels)),
		values:    make(map[string]Value, len(channels)),
	}
}

func (a *stubAutonomyDeviceAdapter) Component() ComponentDescriptor {
	return cloneComponent(a.component)
}

func (a *stubAutonomyDeviceAdapter) Channels() []ChannelDescriptor {
	return slices.Clone(a.channels)
}

func (a *stubAutonomyDeviceAdapter) SetValue(address string, val Value, quality Quality, at time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.values[address] = val
	a.samples[address] = Sample{Value: val, Quality: quality, ObservedAt: at}
}

func (a *stubAutonomyDeviceAdapter) ReadValue(address string) (Value, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	val, ok := a.values[address]
	return val, ok
}

func (a *stubAutonomyDeviceAdapter) Poll(_ context.Context, at time.Time) ([]ChannelUpdate, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	updates := make([]ChannelUpdate, 0, len(a.channels))
	for _, ch := range a.channels {
		addr := ch.Address()
		sample, ok := a.samples[addr]
		if !ok {
			continue
		}
		if !a.preserveObservedAt || sample.ObservedAt.IsZero() {
			sample.ObservedAt = at
		}
		updates = append(updates, ChannelUpdate{Address: addr, Sample: sample})
	}
	return updates, nil
}

func (a *stubAutonomyDeviceAdapter) Apply(_ context.Context, _ ProcessImage, decisions []Decision) ([]DeviceWriteResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	results := make([]DeviceWriteResult, 0, len(decisions))
	for _, d := range decisions {
		if !d.Accepted || d.Effective == nil {
			continue
		}
		applied := *d.Effective
		a.values[d.Address] = applied
		if prev, ok := a.samples[d.Address]; ok {
			prev.Value = applied
			a.samples[d.Address] = prev
		} else {
			a.samples[d.Address] = Sample{Value: applied, Quality: QualityGood}
		}
		results = append(results, DeviceWriteResult{
			Address:      d.Address,
			Success:      true,
			Code:         "APPLIED",
			AppliedValue: &applied,
		})
	}
	return results, nil
}

type intentRequestController struct {
	id   string
	reqs map[string]Value
}

func (c *intentRequestController) ID() string { return c.id }

func (c *intentRequestController) Run(_ context.Context, _ ProcessImage, plan *ControlPlan) error {
	for addr, val := range c.reqs {
		if _, _, err := plan.Request(addr, c.id, val); err != nil {
			return err
		}
	}
	return nil
}

func testChannelDesc(componentID, channelID, pointID string, dataType DataType, access AccessMode) ChannelDescriptor {
	return ChannelDescriptor{
		ComponentID:               componentID,
		ChannelID:                 channelID,
		PointID:                   pointID,
		DataType:                  dataType,
		Access:                    access,
		Category:                  ChannelCategoryOpenemsType,
		PollPriority:              PriorityVeryHigh,
		LocalPersistencePriority:  PriorityVeryHigh,
		RemotePersistencePriority: PriorityVeryHigh,
		AggregationPriority:       PriorityVeryHigh,
		ResendPriority:            PriorityVeryHigh,
	}
}

func newAutonomyTestHost(t *testing.T) (*Host, *stubAutonomyDeviceAdapter, *stubAutonomyDeviceAdapter) {
	t.Helper()
	chiller := newStubAutonomyDeviceAdapter(
		ComponentDescriptor{
			ID:        "CH-01",
			Enabled:   true,
			Kind:      ComponentDeviceDriver,
			Type:      "CHILLER",
			FactoryID: DeclarativeModbusFactoryID,
			Version:   "1.0.0",
			Profiles:  []CapabilityProfileID{ProfileChiller},
		},
		[]ChannelDescriptor{
			testChannelDesc("CH-01", "start_command", "ch:start", DataTypeBoolean, AccessReadWrite),
			testChannelDesc("CH-01", "stop_command", "ch:stop", DataTypeBoolean, AccessReadWrite),
			testChannelDesc("CH-01", "run_state", "ch:run", DataTypeBoolean, AccessReadOnly),
			testChannelDesc("CH-01", "fault_active", "ch:fault", DataTypeBoolean, AccessReadOnly),
			testChannelDesc("CH-01", "chw_supply_setpoint_c", "ch:sp", DataTypeDouble, AccessReadWrite),
		},
	)
	pump := newStubAutonomyDeviceAdapter(
		ComponentDescriptor{
			ID:        "CHWP-01",
			Enabled:   true,
			Kind:      ComponentDeviceDriver,
			Type:      "VARIABLE_SPEED_PUMP",
			FactoryID: DeclarativeModbusFactoryID,
			Version:   "1.0.0",
			Profiles:  []CapabilityProfileID{ProfileVariableSpeedPump},
		},
		[]ChannelDescriptor{
			testChannelDesc("CHWP-01", "run_state", "pump:run", DataTypeBoolean, AccessReadOnly),
			testChannelDesc("CHWP-01", "flow_m3h", "pump:flow", DataTypeDouble, AccessReadOnly),
			testChannelDesc("CHWP-01", "fault_active", "pump:fault", DataTypeBoolean, AccessReadOnly),
			testChannelDesc("CHWP-01", "frequency_setpoint_hz", "pump:freq", DataTypeDouble, AccessReadWrite),
		},
	)

	host, err := NewHost()
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	if err := host.RegisterAdapter(chiller); err != nil {
		t.Fatalf("RegisterAdapter chiller error = %v", err)
	}
	if err := host.RegisterAdapter(pump); err != nil {
		t.Fatalf("RegisterAdapter pump error = %v", err)
	}
	return host, chiller, pump
}

func TestAutonomyPolicyConfigValidation(t *testing.T) {
	t.Parallel()

	def := DefaultAutonomyPolicyConfig()
	if err := def.Validate(); err != nil {
		t.Fatalf("DefaultAutonomyPolicyConfig() invalid: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*AutonomyPolicyConfig)
	}{
		{name: "grace too low", mutate: func(p *AutonomyPolicyConfig) { p.OfflineGraceSeconds = 5 }},
		{name: "setpoint too low", mutate: func(p *AutonomyPolicyConfig) { p.DefaultChilledWaterSupplySetpointC = 3.5 }},
		{name: "setpoint nan", mutate: func(p *AutonomyPolicyConfig) { p.DefaultChilledWaterSupplySetpointC = math.NaN() }},
		{name: "min above max freq", mutate: func(p *AutonomyPolicyConfig) { p.MinPumpFrequencyHz = 45; p.MaxPumpFrequencyHz = 30 }},
		{name: "default below min freq", mutate: func(p *AutonomyPolicyConfig) { p.MinPumpFrequencyHz = 25; p.DefaultPumpFrequencyHz = 22 }},
		{name: "negative min run", mutate: func(p *AutonomyPolicyConfig) { p.MinRunSeconds = -1 }},
		{name: "snapshot too small", mutate: func(p *AutonomyPolicyConfig) { p.SnapshotIntervalSeconds = 10 }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultAutonomyPolicyConfig()
			tc.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatalf("expected validation error for %s", tc.name)
			}
		})
	}
}

func TestHydraulicInterlockSafetyShortCycleAndFrequencyClamp(t *testing.T) {
	t.Parallel()

	host, chiller, pump := newAutonomyTestHost(t)

	policy := DefaultAutonomyPolicyConfig()
	policy.MinPumpFrequencyHz = 22
	policy.MaxPumpFrequencyHz = 48
	policy.MinRunSeconds = 60
	policy.MinStopSeconds = 45

	interlock, err := NewHydraulicInterlockController(HydraulicInterlockConfig{
		Chillers: []ChillerInterlockBinding{{
			ComponentID:  "CH-01",
			StartCommand: "CH-01/start_command",
			StopCommand:  "CH-01/stop_command",
			RunState:     "CH-01/run_state",
			FaultChannel: "CH-01/fault_active",
		}},
		ChilledWaterPumps: []PumpInterlockBinding{{
			ComponentID:      "CHWP-01",
			RunState:         "CHWP-01/run_state",
			FlowFeedback:     "CHWP-01/flow_m3h",
			MinFlowRate:      10.0,
			FaultChannel:     "CHWP-01/fault_active",
			FrequencyCommand: "CHWP-01/frequency_setpoint_hz",
		}},
		Policy: policy,
	})
	if err != nil {
		t.Fatalf("NewHydraulicInterlockController() error = %v", err)
	}

	intents := &intentRequestController{id: "cloud-intents", reqs: map[string]Value{}}
	if err := host.Start([]ControllerBinding{
		{Priority: 0, Controller: interlock},
		{Priority: 100, Controller: intents},
	}); err != nil {
		t.Fatalf("host.Start() error = %v", err)
	}

	t0 := time.Date(2026, 11, 17, 14, 0, 0, 0, time.UTC)

	chiller.SetValue("CH-01/run_state", BooleanValue(false), QualityGood, t0)
	chiller.SetValue("CH-01/start_command", BooleanValue(false), QualityGood, t0)
	chiller.SetValue("CH-01/stop_command", BooleanValue(false), QualityGood, t0)
	chiller.SetValue("CH-01/fault_active", BooleanValue(false), QualityGood, t0)
	pump.SetValue("CHWP-01/run_state", BooleanValue(false), QualityGood, t0)
	pump.SetValue("CHWP-01/flow_m3h", DoubleValue(0), QualityGood, t0)
	pump.SetValue("CHWP-01/fault_active", BooleanValue(false), QualityGood, t0)
	pump.SetValue("CHWP-01/frequency_setpoint_hz", DoubleValue(25), QualityGood, t0)

	intents.reqs["CH-01/start_command"] = BooleanValue(true)
	intents.reqs["CHWP-01/frequency_setpoint_hz"] = DoubleValue(12)

	if _, err := host.RunCycle(context.Background(), t0); err != nil {
		t.Fatalf("cycle 1 RunCycle error = %v", err)
	}
	if startVal, ok := chiller.ReadValue("CH-01/start_command"); !ok || startVal.Boolean {
		t.Fatalf("expected CH-01/start_command blocked when CHW flow=0, got %+v", startVal)
	}
	if freqVal, ok := pump.ReadValue("CHWP-01/frequency_setpoint_hz"); !ok || freqVal.Double != 22 {
		t.Fatalf("expected frequency clamped to min 22Hz, got %+v", freqVal)
	}

	t1 := t0.Add(2 * time.Second)
	pump.SetValue("CHWP-01/run_state", BooleanValue(true), QualityGood, t1)
	pump.SetValue("CHWP-01/flow_m3h", DoubleValue(45), QualityGood, t1)
	intents.reqs["CHWP-01/frequency_setpoint_hz"] = DoubleValue(55)
	if _, err := host.RunCycle(context.Background(), t1); err != nil {
		t.Fatalf("cycle 2 RunCycle error = %v", err)
	}
	if startVal, _ := chiller.ReadValue("CH-01/start_command"); !startVal.Boolean {
		t.Fatalf("expected CH-01/start_command allowed with proven CHW flow")
	}
	if freqVal, _ := pump.ReadValue("CHWP-01/frequency_setpoint_hz"); freqVal.Double != 48 {
		t.Fatalf("expected frequency clamped to max 48Hz, got %v", freqVal.Double)
	}

	t2 := t1.Add(time.Second)
	chiller.SetValue("CH-01/run_state", BooleanValue(true), QualityGood, t2)
	delete(intents.reqs, "CH-01/start_command")
	if _, err := host.RunCycle(context.Background(), t2); err != nil {
		t.Fatalf("cycle 3 RunCycle error = %v", err)
	}

	t3 := t2.Add(10 * time.Second)
	intents.reqs["CH-01/stop_command"] = BooleanValue(true)
	if _, err := host.RunCycle(context.Background(), t3); err != nil {
		t.Fatalf("cycle 4 RunCycle error = %v", err)
	}
	if stopVal, _ := chiller.ReadValue("CH-01/stop_command"); stopVal.Boolean {
		t.Fatalf("expected stop_command denied inside MinRunSeconds window")
	}

	t4 := t3.Add(5 * time.Second)
	delete(intents.reqs, "CH-01/stop_command")
	pump.SetValue("CHWP-01/flow_m3h", DoubleValue(2.0), QualityGood, t4)
	if _, err := host.RunCycle(context.Background(), t4); err != nil {
		t.Fatalf("cycle 5 emergency RunCycle error = %v", err)
	}
	if stopVal, _ := chiller.ReadValue("CH-01/stop_command"); !stopVal.Boolean {
		t.Fatalf("expected emergency stop_command=true overriding MinRunSeconds on CHW flow loss")
	}

	t5 := t4.Add(time.Second)
	chiller.SetValue("CH-01/run_state", BooleanValue(false), QualityGood, t5)
	chiller.SetValue("CH-01/start_command", BooleanValue(false), QualityGood, t5)
	chiller.SetValue("CH-01/stop_command", BooleanValue(false), QualityGood, t5)
	pump.SetValue("CHWP-01/flow_m3h", DoubleValue(40.0), QualityGood, t5)
	intents.reqs["CH-01/start_command"] = BooleanValue(true)
	if _, err := host.RunCycle(context.Background(), t5); err != nil {
		t.Fatalf("cycle 6 post-stop RunCycle error = %v", err)
	}
	if startVal, _ := chiller.ReadValue("CH-01/start_command"); startVal.Boolean {
		t.Fatalf("expected start_command blocked by MinStopSeconds immediately after stop")
	}

	t6 := t5.Add(46 * time.Second)
	if _, err := host.RunCycle(context.Background(), t6); err != nil {
		t.Fatalf("cycle 7 post-MinStop RunCycle error = %v", err)
	}
	if startVal, _ := chiller.ReadValue("CH-01/start_command"); !startVal.Boolean {
		t.Fatalf("expected start_command allowed after MinStopSeconds expired")
	}

	updatedPolicy := policy
	updatedPolicy.MinRunSeconds = 120
	if err := interlock.UpdatePolicy(updatedPolicy); err != nil {
		t.Fatalf("UpdatePolicy error = %v", err)
	}
	if interlock.Policy().MinRunSeconds != 120 {
		t.Fatalf("expected MinRunSeconds 120 after UpdatePolicy")
	}
}

func TestAutonomousFallbackControllerGraceFaultAndReconnectYield(t *testing.T) {
	t.Parallel()

	host, chiller, pump := newAutonomyTestHost(t)

	policy := DefaultAutonomyPolicyConfig()
	policy.Enabled = true
	policy.OfflineGraceSeconds = 30
	policy.DefaultChilledWaterSupplySetpointC = 7.5
	policy.DefaultPumpFrequencyHz = 36.0
	policy.MinPumpFrequencyHz = 20.0
	policy.MaxPumpFrequencyHz = 50.0

	interlock, err := NewHydraulicInterlockController(HydraulicInterlockConfig{
		Chillers: []ChillerInterlockBinding{{
			ComponentID:  "CH-01",
			StartCommand: "CH-01/start_command",
			StopCommand:  "CH-01/stop_command",
			RunState:     "CH-01/run_state",
			FaultChannel: "CH-01/fault_active",
		}},
		ChilledWaterPumps: []PumpInterlockBinding{{
			ComponentID:      "CHWP-01",
			RunState:         "CHWP-01/run_state",
			FlowFeedback:     "CHWP-01/flow_m3h",
			FrequencyCommand: "CHWP-01/frequency_setpoint_hz",
		}},
		Policy: policy,
	})
	if err != nil {
		t.Fatalf("NewHydraulicInterlockController() error = %v", err)
	}

	t0 := time.Date(2026, 11, 17, 15, 0, 0, 0, time.UTC)
	fallback, err := NewAutonomousFallbackController(AutonomousFallbackConfig{
		Chillers: []ChillerFallbackTarget{{
			ComponentID:        "CH-01",
			SupplySetpointAddr: "CH-01/chw_supply_setpoint_c",
			RunStateAddr:       "CH-01/run_state",
			FaultAddr:          "CH-01/fault_active",
		}},
		Pumps: []PumpFallbackTarget{{
			ComponentID:   "CHWP-01",
			FrequencyAddr: "CHWP-01/frequency_setpoint_hz",
			RunStateAddr:  "CHWP-01/run_state",
			FaultAddr:     "CHWP-01/fault_active",
		}},
		Policy:                     policy,
		CloudConnected:             true,
		LastCloudContactAt:         t0,
		SetpointRampRateCPerSecond: 1.0,
		FrequencyRampRateHzPerSec:  5.0,
	})
	if err != nil {
		t.Fatalf("NewAutonomousFallbackController() error = %v", err)
	}

	intents := &intentRequestController{id: "cloud-intents", reqs: map[string]Value{
		"CH-01/chw_supply_setpoint_c":   DoubleValue(11.5),
		"CHWP-01/frequency_setpoint_hz": DoubleValue(26.0),
	}}

	if err := host.Start([]ControllerBinding{
		{Priority: 0, Controller: interlock},
		{Priority: 50, Controller: fallback},
		{Priority: 100, Controller: intents},
	}); err != nil {
		t.Fatalf("host.Start() error = %v", err)
	}

	chiller.SetValue("CH-01/run_state", BooleanValue(true), QualityGood, t0)
	chiller.SetValue("CH-01/fault_active", BooleanValue(false), QualityGood, t0)
	chiller.SetValue("CH-01/chw_supply_setpoint_c", DoubleValue(11.5), QualityGood, t0)
	pump.SetValue("CHWP-01/run_state", BooleanValue(true), QualityGood, t0)
	pump.SetValue("CHWP-01/flow_m3h", DoubleValue(40), QualityGood, t0)
	pump.SetValue("CHWP-01/fault_active", BooleanValue(false), QualityGood, t0)
	pump.SetValue("CHWP-01/frequency_setpoint_hz", DoubleValue(26.0), QualityGood, t0)

	if _, err := host.RunCycle(context.Background(), t0); err != nil {
		t.Fatalf("online RunCycle error = %v", err)
	}
	if sp, _ := chiller.ReadValue("CH-01/chw_supply_setpoint_c"); sp.Double != 11.5 {
		t.Fatalf("expected cloud setpoint 11.5 while online, got %v", sp.Double)
	}

	tDisconnect := t0.Add(10 * time.Second)
	fallback.SetCloudConnected(false, tDisconnect)
	if _, err := host.RunCycle(context.Background(), tDisconnect.Add(25*time.Second)); err != nil {
		t.Fatalf("grace period RunCycle error = %v", err)
	}
	if st := fallback.StatusAt(tDisconnect.Add(25 * time.Second)); st.FallbackActive {
		t.Fatalf("fallback should stay inactive within OfflineGraceSeconds: %+v", st)
	}
	if sp, _ := chiller.ReadValue("CH-01/chw_supply_setpoint_c"); sp.Double != 11.5 {
		t.Fatalf("expected cloud setpoint 11.5 during grace, got %v", sp.Double)
	}

	tGrace0 := tDisconnect.Add(30 * time.Second)
	if _, err := host.RunCycle(context.Background(), tGrace0); err != nil {
		t.Fatalf("grace entry RunCycle error = %v", err)
	}
	if st := fallback.StatusAt(tGrace0); !st.FallbackActive {
		t.Fatalf("expected FallbackActive=true at grace expiry")
	}

	tStep1 := tGrace0.Add(time.Second)
	if _, err := host.RunCycle(context.Background(), tStep1); err != nil {
		t.Fatalf("ramp step 1 RunCycle error = %v", err)
	}
	if sp, _ := chiller.ReadValue("CH-01/chw_supply_setpoint_c"); math.Abs(sp.Double-10.5) > 1e-6 {
		t.Fatalf("expected smooth ramp to 10.5 after 1s, got %v", sp.Double)
	}
	if fr, _ := pump.ReadValue("CHWP-01/frequency_setpoint_hz"); math.Abs(fr.Double-31.0) > 1e-6 {
		t.Fatalf("expected smooth ramp to 31.0Hz after 1s, got %v", fr.Double)
	}

	tConverged := tStep1.Add(10 * time.Second)
	if _, err := host.RunCycle(context.Background(), tConverged); err != nil {
		t.Fatalf("converged RunCycle error = %v", err)
	}
	if sp, _ := chiller.ReadValue("CH-01/chw_supply_setpoint_c"); math.Abs(sp.Double-7.5) > 1e-6 {
		t.Fatalf("expected converged setpoint 7.5, got %v", sp.Double)
	}
	if fr, _ := pump.ReadValue("CHWP-01/frequency_setpoint_hz"); math.Abs(fr.Double-36.0) > 1e-6 {
		t.Fatalf("expected converged freq 36.0Hz, got %v", fr.Double)
	}

	tFault := tConverged.Add(2 * time.Second)
	chiller.SetValue("CH-01/fault_active", BooleanValue(true), QualityGood, tFault)
	chiller.SetValue("CH-01/chw_supply_setpoint_c", DoubleValue(12.0), QualityGood, tFault)
	delete(intents.reqs, "CH-01/chw_supply_setpoint_c")
	if _, err := host.RunCycle(context.Background(), tFault); err != nil {
		t.Fatalf("faulted fallback RunCycle error = %v", err)
	}
	if sp, _ := chiller.ReadValue("CH-01/chw_supply_setpoint_c"); sp.Double != 12.0 {
		t.Fatalf("fallback must skip faulted chiller rather than driving setpoints, got %v", sp.Double)
	}

	tReconnect := tFault.Add(2 * time.Second)
	chiller.SetValue("CH-01/fault_active", BooleanValue(false), QualityGood, tReconnect)
	intents.reqs["CH-01/chw_supply_setpoint_c"] = DoubleValue(9.5)
	intents.reqs["CHWP-01/frequency_setpoint_hz"] = DoubleValue(42.0)
	fallback.RecordCloudContact(tReconnect)

	if _, err := host.RunCycle(context.Background(), tReconnect); err != nil {
		t.Fatalf("reconnect RunCycle error = %v", err)
	}
	if st := fallback.StatusAt(tReconnect); st.FallbackActive || !st.CloudConnected {
		t.Fatalf("expected fallback yielded after reconnect: %+v", st)
	}
	if sp, _ := chiller.ReadValue("CH-01/chw_supply_setpoint_c"); sp.Double != 9.5 {
		t.Fatalf("expected cloud setpoint 9.5 immediately after reconnect yield, got %v", sp.Double)
	}
	if fr, _ := pump.ReadValue("CHWP-01/frequency_setpoint_hz"); fr.Double != 42.0 {
		t.Fatalf("expected cloud freq 42.0Hz immediately after reconnect yield, got %v", fr.Double)
	}
}

func TestAutonomousFallbackIgnoresBadQualityZeroSetpoint(t *testing.T) {
	t.Parallel()

	host, chiller, pump := newAutonomyTestHost(t)

	policy := DefaultAutonomyPolicyConfig()
	policy.Enabled = true
	policy.OfflineGraceSeconds = 10
	policy.DefaultChilledWaterSupplySetpointC = 7.5
	policy.DefaultPumpFrequencyHz = 36.0

	t0 := time.Date(2026, 11, 17, 16, 0, 0, 0, time.UTC)
	fallback, err := NewAutonomousFallbackController(AutonomousFallbackConfig{
		Chillers: []ChillerFallbackTarget{{
			ComponentID:        "CH-01",
			SupplySetpointAddr: "CH-01/chw_supply_setpoint_c",
			RunStateAddr:       "CH-01/run_state",
			FaultAddr:          "CH-01/fault_active",
		}},
		Pumps: []PumpFallbackTarget{{
			ComponentID:   "CHWP-01",
			FrequencyAddr: "CHWP-01/frequency_setpoint_hz",
			RunStateAddr:  "CHWP-01/run_state",
			FaultAddr:     "CHWP-01/fault_active",
		}},
		Policy:                     policy,
		CloudConnected:             false,
		LastCloudContactAt:         t0.Add(-20 * time.Second),
		SetpointRampRateCPerSecond: 1.0,
		FrequencyRampRateHzPerSec:  5.0,
	})
	if err != nil {
		t.Fatalf("NewAutonomousFallbackController() error = %v", err)
	}
	if err := host.Start([]ControllerBinding{{Priority: 50, Controller: fallback}}); err != nil {
		t.Fatalf("host.Start() error = %v", err)
	}

	chiller.SetValue("CH-01/run_state", BooleanValue(true), QualityGood, t0)
	chiller.SetValue("CH-01/fault_active", BooleanValue(false), QualityGood, t0)
	chiller.SetValue("CH-01/chw_supply_setpoint_c", DoubleValue(0), QualityBad, t0)
	pump.SetValue("CHWP-01/run_state", BooleanValue(true), QualityGood, t0)
	pump.SetValue("CHWP-01/fault_active", BooleanValue(false), QualityGood, t0)
	pump.SetValue("CHWP-01/frequency_setpoint_hz", DoubleValue(0), QualityBad, t0)

	for step := 0; step < 3; step++ {
		at := t0.Add(time.Duration(step) * time.Second)
		res, err := host.RunCycle(context.Background(), at)
		if err != nil {
			t.Fatalf("step %d RunCycle error = %v", step, err)
		}
		for _, d := range res.Cycle.Decisions {
			if d.Address == "CH-01/chw_supply_setpoint_c" && d.Effective != nil && d.Effective.Double != 7.5 {
				t.Fatalf("step %d must not ramp chilled-water setpoint from QualityBad 0C, got %v", step, d.Effective.Double)
			}
			if d.Address == "CHWP-01/frequency_setpoint_hz" && d.Effective != nil && d.Effective.Double != 36.0 {
				t.Fatalf("step %d must not ramp pump frequency from QualityBad 0Hz, got %v", step, d.Effective.Double)
			}
		}
	}

	tGood := t0.Add(3 * time.Second)
	chiller.SetValue("CH-01/chw_supply_setpoint_c", DoubleValue(10.5), QualityGood, tGood)
	res, err := host.RunCycle(context.Background(), tGood)
	if err != nil {
		t.Fatalf("QualityGood RunCycle error = %v", err)
	}
	var gotSP float64
	for _, d := range res.Cycle.Decisions {
		if d.Address == "CH-01/chw_supply_setpoint_c" && d.Effective != nil {
			gotSP = d.Effective.Double
		}
	}
	if gotSP != 10.5 {
		t.Fatalf("expected bumpless seed 10.5 once QualityGood arrives, got %v", gotSP)
	}
}

func TestLocalManualModeBlocksRemoteWritesAndRestoresOnRemote(t *testing.T) {
	t.Parallel()

	chiller := newStubAutonomyDeviceAdapter(
		ComponentDescriptor{
			ID:        "CH-01",
			Enabled:   true,
			Kind:      ComponentDeviceDriver,
			Type:      "CHILLER",
			FactoryID: DeclarativeModbusFactoryID,
			Version:   "1.0.0",
			Profiles:  []CapabilityProfileID{ProfileChiller},
		},
		[]ChannelDescriptor{
			testChannelDesc("CH-01", "start_command", "ch:start", DataTypeBoolean, AccessReadWrite),
			testChannelDesc("CH-01", "stop_command", "ch:stop", DataTypeBoolean, AccessReadWrite),
			testChannelDesc("CH-01", "chw_supply_setpoint_c", "ch:sp", DataTypeDouble, AccessReadWrite),
			testChannelDesc("CH-01", "load_limit_pct", "ch:limit", DataTypeDouble, AccessReadWrite),
			testChannelDesc("CH-01", "run_state", "ch:run", DataTypeBoolean, AccessReadOnly),
			testChannelDesc("CH-01", "fault_active", "ch:fault", DataTypeBoolean, AccessReadOnly),
			testChannelDesc("CH-01", "local_remote_mode", "ch:mode", DataTypeString, AccessReadOnly),
		},
	)
	pump := newStubAutonomyDeviceAdapter(
		ComponentDescriptor{
			ID:        "CHWP-01",
			Enabled:   true,
			Kind:      ComponentDeviceDriver,
			Type:      "VARIABLE_SPEED_PUMP",
			FactoryID: DeclarativeModbusFactoryID,
			Version:   "1.0.0",
			Profiles:  []CapabilityProfileID{ProfileVariableSpeedPump},
		},
		[]ChannelDescriptor{
			testChannelDesc("CHWP-01", "start_command", "pump:start", DataTypeBoolean, AccessReadWrite),
			testChannelDesc("CHWP-01", "stop_command", "pump:stop", DataTypeBoolean, AccessReadWrite),
			testChannelDesc("CHWP-01", "frequency_setpoint_hz", "pump:freq", DataTypeDouble, AccessReadWrite),
			testChannelDesc("CHWP-01", "run_state", "pump:run", DataTypeBoolean, AccessReadOnly),
			testChannelDesc("CHWP-01", "flow_m3h", "pump:flow", DataTypeDouble, AccessReadOnly),
			testChannelDesc("CHWP-01", "fault_active", "pump:fault", DataTypeBoolean, AccessReadOnly),
			testChannelDesc("CHWP-01", "local_remote_mode", "pump:mode", DataTypeBoolean, AccessReadOnly),
		},
	)

	host, err := NewHost()
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	if err := host.RegisterAdapter(chiller); err != nil {
		t.Fatalf("RegisterAdapter chiller error = %v", err)
	}
	if err := host.RegisterAdapter(pump); err != nil {
		t.Fatalf("RegisterAdapter pump error = %v", err)
	}

	interlock, err := NewHydraulicInterlockController(HydraulicInterlockConfig{
		Chillers: []ChillerInterlockBinding{{
			ComponentID:      "CH-01",
			StartCommand:     "CH-01/start_command",
			StopCommand:      "CH-01/stop_command",
			SetpointCommand:  "CH-01/chw_supply_setpoint_c",
			LoadLimitCommand: "CH-01/load_limit_pct",
			RunState:         "CH-01/run_state",
			FaultChannel:     "CH-01/fault_active",
			LocalRemoteMode:  "CH-01/local_remote_mode",
		}},
		ChilledWaterPumps: []PumpInterlockBinding{{
			ComponentID:      "CHWP-01",
			StartCommand:     "CHWP-01/start_command",
			StopCommand:      "CHWP-01/stop_command",
			RunState:         "CHWP-01/run_state",
			FlowFeedback:     "CHWP-01/flow_m3h",
			FaultChannel:     "CHWP-01/fault_active",
			FrequencyCommand: "CHWP-01/frequency_setpoint_hz",
			LocalRemoteMode:  "CHWP-01/local_remote_mode",
		}},
		Policy: DefaultAutonomyPolicyConfig(),
	})
	if err != nil {
		t.Fatalf("NewHydraulicInterlockController() error = %v", err)
	}

	intents := &intentRequestController{id: "cloud-intents", reqs: map[string]Value{}}
	if err := host.Start([]ControllerBinding{
		{Priority: 0, Critical: true, Controller: interlock},
		{Priority: 100, Controller: intents},
	}); err != nil {
		t.Fatalf("host.Start() error = %v", err)
	}

	t0 := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	chiller.SetValue("CH-01/run_state", BooleanValue(false), QualityGood, t0)
	chiller.SetValue("CH-01/fault_active", BooleanValue(false), QualityGood, t0)
	chiller.SetValue("CH-01/start_command", BooleanValue(false), QualityGood, t0)
	chiller.SetValue("CH-01/stop_command", BooleanValue(false), QualityGood, t0)
	chiller.SetValue("CH-01/chw_supply_setpoint_c", DoubleValue(7.0), QualityGood, t0)
	chiller.SetValue("CH-01/load_limit_pct", DoubleValue(80.0), QualityGood, t0)
	chiller.SetValue("CH-01/local_remote_mode", StringValue("REMOTE"), QualityGood, t0)

	pump.SetValue("CHWP-01/run_state", BooleanValue(true), QualityGood, t0)
	pump.SetValue("CHWP-01/flow_m3h", DoubleValue(42.0), QualityGood, t0)
	pump.SetValue("CHWP-01/fault_active", BooleanValue(false), QualityGood, t0)
	pump.SetValue("CHWP-01/start_command", BooleanValue(false), QualityGood, t0)
	pump.SetValue("CHWP-01/stop_command", BooleanValue(false), QualityGood, t0)
	pump.SetValue("CHWP-01/frequency_setpoint_hz", DoubleValue(35.0), QualityGood, t0)
	pump.SetValue("CHWP-01/local_remote_mode", BooleanValue(true), QualityGood, t0)

	now := t0
	step := func() CycleResult {
		t.Helper()
		now = now.Add(time.Second)
		res, err := host.RunCycle(context.Background(), now)
		if err != nil {
			t.Fatalf("RunCycle() error = %v", err)
		}
		return res.Cycle
	}

	// Verify all manual representations block writes on chiller/pump and preserve read-only channels.
	for _, token := range []string{"LOCAL", "manual", "Local_Manual", "Hand", "FALSE", "0"} {
		chiller.SetValue("CH-01/local_remote_mode", StringValue(token), QualityGood, now)
		pump.SetValue("CHWP-01/local_remote_mode", BooleanValue(false), QualityGood, now)
		intents.reqs = map[string]Value{
			"CH-01/start_command":           BooleanValue(true),
			"CH-01/stop_command":            BooleanValue(true),
			"CH-01/chw_supply_setpoint_c":   DoubleValue(9.0),
			"CH-01/load_limit_pct":          DoubleValue(60.0),
			"CHWP-01/start_command":         BooleanValue(true),
			"CHWP-01/stop_command":          BooleanValue(true),
			"CHWP-01/frequency_setpoint_hz": DoubleValue(42.0),
		}

		cycle := step()
		if len(cycle.Decisions) != 7 {
			t.Fatalf("token %q: expected 7 arbitrated decisions, got %d", token, len(cycle.Decisions))
		}
		for _, d := range cycle.Decisions {
			if d.Accepted || len(d.ConstraintReasons) == 0 || d.ConstraintReasons[0].Reason != autonomyReasonLocalManual {
				t.Fatalf("token %q: expected %s denial for %s, got %+v", token, autonomyReasonLocalManual, d.Address, d)
			}
		}
		blocked := interlock.Status().LastBlockedReasons
		for _, writableAddr := range []string{
			"CH-01/start_command", "CH-01/stop_command", "CH-01/chw_supply_setpoint_c", "CH-01/load_limit_pct",
			"CHWP-01/start_command", "CHWP-01/stop_command", "CHWP-01/frequency_setpoint_hz",
		} {
			if blocked[writableAddr] != autonomyReasonLocalManual {
				t.Fatalf("token %q: expected blocked[%s]=%s, got %+v", token, writableAddr, autonomyReasonLocalManual, blocked)
			}
		}
		for _, roAddr := range []string{"CH-01/run_state", "CH-01/local_remote_mode", "CHWP-01/flow_m3h"} {
			if _, found := blocked[roAddr]; found {
				t.Fatalf("read-only channel %s must not be recorded in blocked: %+v", roAddr, blocked)
			}
			if snap, ok := cycle.Image.Get(roAddr); !ok || !snap.HasValue {
				t.Fatalf("read-only channel %s must remain readable in ProcessImage", roAddr)
			}
		}
		if sp, _ := chiller.ReadValue("CH-01/chw_supply_setpoint_c"); sp.Double != 7.0 {
			t.Fatalf("token %q: chiller setpoint mutated under LOCAL_MANUAL: %v", token, sp.Double)
		}
		if fr, _ := pump.ReadValue("CHWP-01/frequency_setpoint_hz"); fr.Double != 35.0 {
			t.Fatalf("token %q: pump frequency mutated under LOCAL_MANUAL: %v", token, fr.Double)
		}
	}

	// Verify all REMOTE tokens clear denial immediately on the next cycle.
	for idx, remoteToken := range []string{"REMOTE", "auto", "Remote_Auto", "TRUE", "1"} {
		chiller.SetValue("CH-01/local_remote_mode", StringValue(remoteToken), QualityGood, now)
		pump.SetValue("CHWP-01/local_remote_mode", BooleanValue(true), QualityGood, now)
		targetSP := 8.0 + float64(idx)*0.2
		targetHz := 40.0 + float64(idx)*0.5
		intents.reqs = map[string]Value{
			"CH-01/chw_supply_setpoint_c":   DoubleValue(targetSP),
			"CH-01/load_limit_pct":          DoubleValue(75.0),
			"CHWP-01/frequency_setpoint_hz": DoubleValue(targetHz),
		}

		cycle := step()
		for _, d := range cycle.Decisions {
			if !d.Accepted {
				t.Fatalf("remote token %q: expected %s accepted immediately, got %+v", remoteToken, d.Address, d)
			}
		}
		if len(interlock.Status().LastBlockedReasons) != 0 {
			t.Fatalf("remote token %q: expected blocked reasons cleared, got %+v", remoteToken, interlock.Status().LastBlockedReasons)
		}
		if sp, _ := chiller.ReadValue("CH-01/chw_supply_setpoint_c"); sp.Double != targetSP {
			t.Fatalf("remote token %q: chiller setpoint = %v, want %v", remoteToken, sp.Double, targetSP)
		}
		if fr, _ := pump.ReadValue("CHWP-01/frequency_setpoint_hz"); fr.Double != targetHz {
			t.Fatalf("remote token %q: pump frequency = %v, want %v", remoteToken, fr.Double, targetHz)
		}
	}
}

func TestCoolingTowerLocalRemoteInterlockRespectsTakeoverWithoutFalseChillerTrip(t *testing.T) {
	t.Parallel()

	chiller := newStubAutonomyDeviceAdapter(
		ComponentDescriptor{
			ID: "CH-01", Enabled: true, Kind: ComponentDeviceDriver, Type: "CHILLER",
			FactoryID: DeclarativeModbusFactoryID, Version: "1.0.0", Profiles: []CapabilityProfileID{ProfileChiller},
		},
		[]ChannelDescriptor{
			testChannelDesc("CH-01", "start_command", "ch:start", DataTypeBoolean, AccessReadWrite),
			testChannelDesc("CH-01", "stop_command", "ch:stop", DataTypeBoolean, AccessReadWrite),
			testChannelDesc("CH-01", "run_state", "ch:run", DataTypeBoolean, AccessReadOnly),
			testChannelDesc("CH-01", "fault_active", "ch:fault", DataTypeBoolean, AccessReadOnly),
		},
	)
	pump := newStubAutonomyDeviceAdapter(
		ComponentDescriptor{
			ID: "CHWP-01", Enabled: true, Kind: ComponentDeviceDriver, Type: "VARIABLE_SPEED_PUMP",
			FactoryID: DeclarativeModbusFactoryID, Version: "1.0.0", Profiles: []CapabilityProfileID{ProfileVariableSpeedPump},
		},
		[]ChannelDescriptor{
			testChannelDesc("CHWP-01", "run_state", "pump:run", DataTypeBoolean, AccessReadOnly),
			testChannelDesc("CHWP-01", "flow_m3h", "pump:flow", DataTypeDouble, AccessReadOnly),
			testChannelDesc("CHWP-01", "fault_active", "pump:fault", DataTypeBoolean, AccessReadOnly),
			testChannelDesc("CHWP-01", "frequency_setpoint_hz", "pump:freq", DataTypeDouble, AccessReadWrite),
		},
	)
	tower := newStubAutonomyDeviceAdapter(
		ComponentDescriptor{
			ID: "CT-01", Enabled: true, Kind: ComponentDeviceDriver, Type: "COOLING_TOWER",
			FactoryID: DeclarativeModbusFactoryID, Version: "1.0.0", Profiles: []CapabilityProfileID{ProfileCoolingTower},
		},
		[]ChannelDescriptor{
			testChannelDesc("CT-01", "start_command", "ct:start", DataTypeBoolean, AccessReadWrite),
			testChannelDesc("CT-01", "stop_command", "ct:stop", DataTypeBoolean, AccessReadWrite),
			testChannelDesc("CT-01", "fan_speed_setpoint_pct", "ct:fan", DataTypeDouble, AccessReadWrite),
			testChannelDesc("CT-01", "run_state", "ct:run", DataTypeBoolean, AccessReadOnly),
			testChannelDesc("CT-01", "fault_active", "ct:fault", DataTypeBoolean, AccessReadOnly),
			testChannelDesc("CT-01", "local_remote_mode", "ct:mode", DataTypeString, AccessReadOnly),
		},
	)

	host, err := NewHost()
	if err != nil {
		t.Fatal(err)
	}
	for _, ad := range []DeviceAdapter{chiller, pump, tower} {
		if err := host.RegisterAdapter(ad); err != nil {
			t.Fatal(err)
		}
	}

	policy := DefaultAutonomyPolicyConfig()
	policy.Enabled = true
	interlock, err := NewHydraulicInterlockController(HydraulicInterlockConfig{
		Policy: policy,
		Chillers: []ChillerInterlockBinding{{
			ComponentID: "CH-01", StartCommand: "CH-01/start_command", StopCommand: "CH-01/stop_command",
			RunState: "CH-01/run_state", FaultChannel: "CH-01/fault_active",
		}},
		ChilledWaterPumps: []PumpInterlockBinding{{
			ComponentID: "CHWP-01", RunState: "CHWP-01/run_state", FlowFeedback: "CHWP-01/flow_m3h",
			MinFlowRate: 10, FaultChannel: "CHWP-01/fault_active", FrequencyCommand: "CHWP-01/frequency_setpoint_hz",
		}},
		CoolingTowers: []CoolingTowerInterlockBinding{{
			ComponentID: "CT-01", StartCommand: "CT-01/start_command", StopCommand: "CT-01/stop_command",
			FanSpeedCommand: "CT-01/fan_speed_setpoint_pct", RunState: "CT-01/run_state",
			FaultChannel: "CT-01/fault_active", LocalRemoteMode: "CT-01/local_remote_mode",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	intents := &intentRequestController{id: "cloud-intents", reqs: map[string]Value{}}
	if err := host.Start([]ControllerBinding{
		{Priority: 0, Critical: true, Controller: interlock},
		{Priority: 100, Controller: intents},
	}); err != nil {
		t.Fatal(err)
	}

	t0 := time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)
	chiller.SetValue("CH-01/run_state", BooleanValue(true), QualityGood, t0)
	chiller.SetValue("CH-01/fault_active", BooleanValue(false), QualityGood, t0)
	chiller.SetValue("CH-01/start_command", BooleanValue(false), QualityGood, t0)
	chiller.SetValue("CH-01/stop_command", BooleanValue(false), QualityGood, t0)
	pump.SetValue("CHWP-01/run_state", BooleanValue(true), QualityGood, t0)
	pump.SetValue("CHWP-01/flow_m3h", DoubleValue(80), QualityGood, t0)
	pump.SetValue("CHWP-01/fault_active", BooleanValue(false), QualityGood, t0)
	pump.SetValue("CHWP-01/frequency_setpoint_hz", DoubleValue(35), QualityGood, t0)
	tower.SetValue("CT-01/run_state", BooleanValue(true), QualityGood, t0)
	tower.SetValue("CT-01/fault_active", BooleanValue(false), QualityGood, t0)
	tower.SetValue("CT-01/start_command", BooleanValue(false), QualityGood, t0)
	tower.SetValue("CT-01/stop_command", BooleanValue(false), QualityGood, t0)
	tower.SetValue("CT-01/fan_speed_setpoint_pct", DoubleValue(65), QualityGood, t0)
	tower.SetValue("CT-01/local_remote_mode", StringValue("LOCAL"), QualityGood, t0)

	// Operator flips tower to LOCAL: remote fan/start/stop writes are blocked, while running chiller is NOT tripped.
	intents.reqs = map[string]Value{
		"CT-01/start_command":          BooleanValue(true),
		"CT-01/stop_command":           BooleanValue(true),
		"CT-01/fan_speed_setpoint_pct": DoubleValue(30),
	}
	res, err := host.RunCycle(context.Background(), t0.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Cycle.Decisions) != 3 {
		t.Fatalf("expected 3 tower decisions, got %d", len(res.Cycle.Decisions))
	}
	for _, d := range res.Cycle.Decisions {
		if d.Accepted || len(d.ConstraintReasons) == 0 || d.ConstraintReasons[0].Reason != autonomyReasonLocalManual {
			t.Fatalf("expected %s denial for %s, got %+v", autonomyReasonLocalManual, d.Address, d)
		}
	}
	if fanVal, _ := tower.ReadValue("CT-01/fan_speed_setpoint_pct"); fanVal.Double != 65 {
		t.Fatalf("local tower fan speed mutated to %v, want 65", fanVal.Double)
	}
	if stopCh, _ := chiller.ReadValue("CH-01/stop_command"); stopCh.Boolean {
		t.Fatal("chiller must NOT be tripped during local tower panel takeover")
	}

	// Returning tower to REMOTE restores remote fan modulation immediately.
	t1 := t0.Add(2 * time.Second)
	tower.SetValue("CT-01/local_remote_mode", StringValue("REMOTE"), QualityGood, t1)
	intents.reqs = map[string]Value{"CT-01/fan_speed_setpoint_pct": DoubleValue(48)}
	resRemote, err := host.RunCycle(context.Background(), t1)
	if err != nil {
		t.Fatal(err)
	}
	if len(resRemote.Cycle.Decisions) != 1 || !resRemote.Cycle.Decisions[0].Accepted {
		t.Fatalf("expected REMOTE tower write accepted, got %+v", resRemote.Cycle.Decisions)
	}
	if fanVal, _ := tower.ReadValue("CT-01/fan_speed_setpoint_pct"); fanVal.Double != 48 {
		t.Fatalf("expected tower fan speed 48 after REMOTE restore, got %v", fanVal.Double)
	}

	// Faulted tower in REMOTE mode must deny remote StartCommand without touching local manual protection.
	t2 := t0.Add(3 * time.Second)
	tower.SetValue("CT-01/fault_active", BooleanValue(true), QualityGood, t2)
	intents.reqs = map[string]Value{"CT-01/start_command": BooleanValue(true)}
	resFault, err := host.RunCycle(context.Background(), t2)
	if err != nil {
		t.Fatal(err)
	}
	if len(resFault.Cycle.Decisions) != 1 || resFault.Cycle.Decisions[0].Accepted ||
		resFault.Cycle.Decisions[0].ConstraintReasons[0].Reason != autonomyReasonTowerFault {
		t.Fatalf("expected %s denial on faulted tower start, got %+v", autonomyReasonTowerFault, resFault.Cycle.Decisions)
	}
}
