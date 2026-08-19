package edgecontrol

import (
	"context"
	"testing"
	"time"
)

type fakePumpDriver struct {
	component ComponentDescriptor
	channels  []ChannelDescriptor
	sequence  uint64
	frequency float64
	applied   []Decision
}

func newFakePumpDriver() *fakePumpDriver {
	componentID := "chwp01"
	channels := []ChannelDescriptor{
		pumpChannel(componentID, "RunState", "point-run-state", DataTypeString, "", AccessReadOnly),
		pumpChannel(componentID, "FaultCode", "point-fault-code", DataTypeString, "", AccessReadOnly),
		pumpChannel(componentID, "StartCommand", "point-start-command", DataTypeBoolean, "", AccessWriteOnly),
		pumpChannel(componentID, "StopCommand", "point-stop-command", DataTypeBoolean, "", AccessWriteOnly),
		pumpChannel(componentID, "ResetFaultCommand", "point-reset-fault-command", DataTypeBoolean, "", AccessWriteOnly),
		pumpChannel(componentID, "Frequency", "point-frequency", DataTypeDouble, "Hz", AccessReadOnly),
		pumpChannel(componentID, "FrequencySetpoint", "point-frequency-setpoint", DataTypeDouble, "Hz", AccessWriteOnly),
	}
	return &fakePumpDriver{
		component: ComponentDescriptor{
			ID: componentID, Alias: "Fake Pump", Enabled: true,
			Kind: ComponentSimulator, Type: "FAKE_VFD_PUMP", FactoryID: "FAKE_VFD_PUMP", Version: "v1",
			Profiles: []CapabilityProfileID{ProfileVariableSpeedPump},
			ChannelBindings: map[SemanticChannel]string{
				SemanticRunState: channels[0].Address(), SemanticFaultCode: channels[1].Address(),
				SemanticStartCommand: channels[2].Address(), SemanticStopCommand: channels[3].Address(),
				SemanticResetFaultCommand: channels[4].Address(), SemanticFrequency: channels[5].Address(),
				SemanticFrequencySetpoint: channels[6].Address(),
			},
		},
		channels: channels, frequency: 40,
	}
}

func pumpChannel(componentID, channelID, pointID string, dataType DataType, unit string, access AccessMode) ChannelDescriptor {
	descriptor := testDescriptor(componentID, channelID, pointID, dataType, access)
	descriptor.Unit = unit
	return descriptor
}

func (driver *fakePumpDriver) Component() ComponentDescriptor {
	return cloneComponent(driver.component)
}
func (driver *fakePumpDriver) Channels() []ChannelDescriptor {
	return append([]ChannelDescriptor(nil), driver.channels...)
}

func (driver *fakePumpDriver) Poll(_ context.Context, at time.Time) ([]ChannelUpdate, error) {
	driver.sequence++
	return []ChannelUpdate{
		{Address: driver.channels[0].Address(), Sample: Sample{Value: StringValue("RUNNING"), Quality: QualityGood, ObservedAt: at, Sequence: driver.sequence}},
		{Address: driver.channels[1].Address(), Sample: Sample{Value: StringValue(""), Quality: QualityGood, ObservedAt: at, Sequence: driver.sequence}},
		{Address: driver.channels[5].Address(), Sample: Sample{Value: DoubleValue(driver.frequency), Quality: QualityGood, ObservedAt: at, Sequence: driver.sequence}},
	}, nil
}

func (driver *fakePumpDriver) Apply(_ context.Context, _ ProcessImage, decisions []Decision) ([]DeviceWriteResult, error) {
	results := make([]DeviceWriteResult, 0, len(decisions))
	for _, decision := range decisions {
		driver.applied = append(driver.applied, decision)
		if decision.Address == driver.channels[6].Address() && decision.Effective != nil {
			driver.frequency = decision.Effective.Double
			value := *decision.Effective
			results = append(results, DeviceWriteResult{Address: decision.Address, Success: true, Code: "APPLIED", AppliedValue: &value})
			continue
		}
		results = append(results, DeviceWriteResult{Address: decision.Address, Success: false, Code: "UNSUPPORTED"})
	}
	return results, nil
}

func TestDriverHostSeparatesAsyncInputFromProcessImage(t *testing.T) {
	runtime := NewRuntime()
	capabilities, _ := NewStandardCapabilityRegistry()
	components, _ := NewComponentRegistry(runtime, capabilities)
	host, _ := NewDirectDeviceHost(runtime, components)
	driver := newFakePumpDriver()
	if err := host.RegisterAdapter(driver); err != nil {
		t.Fatal(err)
	}
	at := time.Unix(1300, 0).UTC()
	poll := host.PollOnce(context.Background(), at)
	if len(poll) != 1 || poll[0].Error != nil || poll[0].Updates != 3 {
		t.Fatalf("unexpected poll result: %#v", poll)
	}
	before := runtime.SwitchProcessImage(at.Add(time.Second))
	frequency, ok := before.Get("chwp01/Frequency")
	if !ok || !frequency.HasValue || frequency.Sample.Value.Double != 40 {
		t.Fatalf("driver input was not promoted into process image: %#v", frequency)
	}
	driver.frequency = 44
	poll = host.PollOnce(context.Background(), at.Add(2*time.Second))
	if poll[0].Error != nil {
		t.Fatal(poll[0].Error)
	}
	unchanged, _ := before.Get("chwp01/Frequency")
	if unchanged.Sample.Value.Double != 40 {
		t.Fatalf("previous process image was mutated: %#v", unchanged)
	}
	after := runtime.SwitchProcessImage(at.Add(3 * time.Second))
	updated, _ := after.Get("chwp01/Frequency")
	if updated.Sample.Value.Double != 44 {
		t.Fatalf("new driver input was not visible in the next process image: %#v", updated)
	}
}

func TestDriverOutputUsesArbitratedEffectiveValue(t *testing.T) {
	runtime := NewRuntime()
	capabilities, _ := NewStandardCapabilityRegistry()
	components, _ := NewComponentRegistry(runtime, capabilities)
	host, _ := NewDirectDeviceHost(runtime, components)
	driver := newFakePumpDriver()
	if err := host.RegisterAdapter(driver); err != nil {
		t.Fatal(err)
	}
	writer, _ := NewDirectDeviceOutputWriter(host)
	store, _ := NewIntentStore(runtime)
	issued := time.Unix(1400, 0).UTC()
	_, _ = store.Put(ControlIntent{
		ID: "command-1", Address: "chwp01/FrequencySetpoint", Requested: DoubleValue(50),
		IssuedAt: issued, ExpiresAt: issued.Add(time.Minute), Source: "CLOUD_COMMAND",
	})
	intentController, _ := NewIntentController("cloud-command", store)
	max := 43.0
	safety := controllerFunc{id: "safety", run: func(_ context.Context, _ ProcessImage, plan *ControlPlan) error {
		_, err := plan.ConstrainNumber("chwp01/FrequencySetpoint", "safety", "SAFETY_LIMIT", nil, &max)
		return err
	}}
	scheduler, err := NewScheduler([]ControllerBinding{
		{Priority: 0, Critical: true, Controller: safety},
		{Priority: 100, Controller: intentController},
	})
	if err != nil {
		t.Fatal(err)
	}
	cycle, _ := NewCycle(runtime, scheduler, writer)
	result := cycle.RunOnce(context.Background(), issued.Add(time.Second))
	if result.OutputError != nil || len(result.Decisions) != 1 {
		t.Fatalf("cycle output failed: %#v", result)
	}
	if len(driver.applied) != 1 || driver.applied[0].Requested.Double != 50 || driver.applied[0].Effective == nil || driver.applied[0].Effective.Double != 43 {
		t.Fatalf("driver did not receive arbitrated decision: %#v", driver.applied)
	}
	if driver.frequency != 43 {
		t.Fatalf("driver applied raw request instead of effective value: %v", driver.frequency)
	}
}
