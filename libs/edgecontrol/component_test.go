package edgecontrol

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func registerPumpChannels(t *testing.T, runtime *Runtime, componentID string) map[SemanticChannel]string {
	t.Helper()
	bindings := map[SemanticChannel]string{}
	register := func(semantic SemanticChannel, channelID, pointID string, dataType DataType, unit string, access AccessMode) {
		descriptor := testDescriptor(componentID, channelID, pointID, dataType, access)
		descriptor.Unit = unit
		if err := runtime.Register(descriptor); err != nil {
			t.Fatal(err)
		}
		bindings[semantic] = descriptor.Address()
	}
	register(SemanticRunState, "RunState", "point-run-state", DataTypeString, "", AccessReadOnly)
	register(SemanticFaultCode, "FaultCode", "point-fault-code", DataTypeString, "", AccessReadOnly)
	register(SemanticStartCommand, "StartCommand", "point-start-command", DataTypeBoolean, "", AccessWriteOnly)
	register(SemanticStopCommand, "StopCommand", "point-stop-command", DataTypeBoolean, "", AccessWriteOnly)
	register(SemanticResetFaultCommand, "ResetFaultCommand", "point-reset-fault-command", DataTypeBoolean, "", AccessWriteOnly)
	register(SemanticFrequency, "Frequency", "point-frequency", DataTypeDouble, "Hz", AccessReadOnly)
	register(SemanticFrequencySetpoint, "FrequencySetpoint", "point-frequency-setpoint", DataTypeDouble, "Hz", AccessWriteOnly)
	return bindings
}

func TestStandardVariableSpeedPumpProfileMatchesCommandCapabilities(t *testing.T) {
	registry, err := NewStandardCapabilityRegistry()
	if err != nil {
		t.Fatal(err)
	}
	profile, ok := registry.Get(ProfileVariableSpeedPump)
	if !ok {
		t.Fatal("VARIABLE_SPEED_PUMP profile was not registered")
	}
	controls := map[string]ControlCapabilitySpec{}
	for _, control := range profile.Controls {
		controls[control.CapabilityID] = control
	}
	for _, capability := range []string{"START", "STOP", "RESET_FAULT", "SET_FREQUENCY"} {
		if _, ok := controls[capability]; !ok {
			t.Fatalf("missing pump control capability %s", capability)
		}
	}
	frequency := controls["SET_FREQUENCY"]
	if frequency.ParameterKey != "frequencyHz" || frequency.Minimum == nil || *frequency.Minimum != 20 || frequency.Maximum == nil || *frequency.Maximum != 50 || frequency.Step == nil || *frequency.Step != 0.5 {
		t.Fatalf("unexpected SET_FREQUENCY profile: %#v", frequency)
	}
}

func TestComponentRegistryValidatesCapabilityChannels(t *testing.T) {
	runtime := NewRuntime()
	capabilities, err := NewStandardCapabilityRegistry()
	if err != nil {
		t.Fatal(err)
	}
	registry, err := NewComponentRegistry(runtime, capabilities)
	if err != nil {
		t.Fatal(err)
	}
	bindings := registerPumpChannels(t, runtime, "chwp01")
	if err := registry.Register(ComponentDescriptor{
		ID: "chwp01", Alias: "Chilled Water Pump 01", Enabled: true,
		Kind: ComponentDeviceDriver, Type: "SIMULATED_VFD_PUMP", FactoryID: "SIMULATED_VFD_PUMP", Version: "v1",
		Profiles: []CapabilityProfileID{ProfileVariableSpeedPump}, ChannelBindings: bindings,
	}); err != nil {
		t.Fatalf("valid pump component was rejected: %v", err)
	}

	otherBindings := registerPumpChannelsWithUniquePoints(t, runtime, "chwp02", "2")
	delete(otherBindings, SemanticFrequencySetpoint)
	if err := registry.Register(ComponentDescriptor{
		ID: "chwp02", Alias: "Chilled Water Pump 02", Enabled: true,
		Kind: ComponentDeviceDriver, Type: "SIMULATED_VFD_PUMP", FactoryID: "SIMULATED_VFD_PUMP", Version: "v1",
		Profiles: []CapabilityProfileID{ProfileVariableSpeedPump}, ChannelBindings: otherBindings,
	}); err == nil || !strings.Contains(err.Error(), "missing required semantic channel FrequencySetpoint") {
		t.Fatalf("missing required channel was not rejected: %v", err)
	}
}

func registerPumpChannelsWithUniquePoints(t *testing.T, runtime *Runtime, componentID, suffix string) map[SemanticChannel]string {
	t.Helper()
	bindings := map[SemanticChannel]string{}
	register := func(semantic SemanticChannel, channelID, pointID string, dataType DataType, unit string, access AccessMode) {
		descriptor := testDescriptor(componentID, channelID, pointID+suffix, dataType, access)
		descriptor.Unit = unit
		if err := runtime.Register(descriptor); err != nil {
			t.Fatal(err)
		}
		bindings[semantic] = descriptor.Address()
	}
	register(SemanticRunState, "RunState", "point-run-state", DataTypeString, "", AccessReadOnly)
	register(SemanticFaultCode, "FaultCode", "point-fault-code", DataTypeString, "", AccessReadOnly)
	register(SemanticStartCommand, "StartCommand", "point-start-command", DataTypeBoolean, "", AccessWriteOnly)
	register(SemanticStopCommand, "StopCommand", "point-stop-command", DataTypeBoolean, "", AccessWriteOnly)
	register(SemanticResetFaultCommand, "ResetFaultCommand", "point-reset-fault-command", DataTypeBoolean, "", AccessWriteOnly)
	register(SemanticFrequency, "Frequency", "point-frequency", DataTypeDouble, "Hz", AccessReadOnly)
	register(SemanticFrequencySetpoint, "FrequencySetpoint", "point-frequency-setpoint", DataTypeDouble, "Hz", AccessWriteOnly)
	return bindings
}

func TestEdgeManifestIsSelfDescribingAndDeterministic(t *testing.T) {
	runtime := NewRuntime()
	capabilities, err := NewStandardCapabilityRegistry()
	if err != nil {
		t.Fatal(err)
	}
	registry, err := NewComponentRegistry(runtime, capabilities)
	if err != nil {
		t.Fatal(err)
	}
	bindings := registerPumpChannels(t, runtime, "chwp01")
	if err := registry.Register(ComponentDescriptor{
		ID: "chwp01", Alias: "Chilled Water Pump 01", Enabled: true,
		Kind: ComponentDeviceDriver, Type: "SIMULATED_VFD_PUMP", FactoryID: "SIMULATED_VFD_PUMP", Version: "v1",
		Profiles: []CapabilityProfileID{ProfileVariableSpeedPump}, ChannelBindings: bindings,
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(ComponentDescriptor{ID: "modbus-tcp-1", Alias: "Modbus TCP", Enabled: true, Kind: ComponentProtocolBridge, Type: "MODBUS_TCP", FactoryID: "MODBUS_TCP", Version: "v1"}); err != nil {
		t.Fatal(err)
	}
	at := time.Unix(500, 0).UTC()
	manifest, err := registry.Manifest("edge-local-01", "manifest:v1", at)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.SchemaVersion != 1 || manifest.EdgeID != "edge-local-01" || !manifest.GeneratedAt.Equal(at) {
		t.Fatalf("unexpected manifest identity: %#v", manifest)
	}
	if len(manifest.Components) != 2 || manifest.Components[0].ID != "chwp01" || manifest.Components[1].ID != "modbus-tcp-1" {
		t.Fatalf("manifest components are not deterministic: %#v", manifest.Components)
	}
	if len(manifest.CapabilityProfiles) != 6 || len(manifest.Channels) != 7 {
		t.Fatalf("manifest is incomplete: profiles=%d channels=%d", len(manifest.CapabilityProfiles), len(manifest.Channels))
	}
	payload, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{`"pointId":"point-frequency"`, `"id":"VARIABLE_SPEED_PUMP"`, `"channelBindings"`} {
		if !strings.Contains(string(payload), token) {
			t.Fatalf("manifest JSON is missing %s: %s", token, payload)
		}
	}
}
