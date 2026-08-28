package simulator

import (
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

func TestATV630ProductionBridgeAdapterConformanceOverRealTCP(t *testing.T) {
	config := testPlantConfig()
	config.ChilledWaterPump.InitiallyRunning = false
	plant := NewPlant(config, testStaticScenario(), time.Date(2026, 8, 29, 1, 0, 0, 0, time.UTC))
	plant.Tick(2 * time.Minute)

	endpoint := reserveTCPAddress(t)
	server, err := NewVirtualATV630Server(endpoint, plant)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Stop() })

	bridge, err := edgecontrol.NewModbusTCPBridge(edgecontrol.ModbusTCPBridgeConfig{
		Endpoint: endpoint,
		Timeout:  250 * time.Millisecond,
		Retries:  1,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bridge.Close() })

	adapter, err := edgecontrol.NewATV630DeviceAdapter(edgecontrol.ATV630DeviceAdapterConfig{
		ComponentID: "chwp01",
		Alias:       "CHWP-01 ATV630",
		UnitID:      1,
		Transport:   bridge,
		PointIDs: edgecontrol.ATV630PointIDs{
			RunState: "point-run-state", FaultCode: "point-fault-code",
			StartCommand: "point-start-command", StopCommand: "point-stop-command", ResetFaultCommand: "point-reset-fault-command",
			Frequency: "point-frequency", FrequencySetpoint: "point-frequency-setpoint",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	host, err := edgecontrol.NewHost()
	if err != nil {
		t.Fatal(err)
	}
	if err := host.RegisterAdapter(adapter); err != nil {
		t.Fatal(err)
	}
	controller, err := edgecontrol.NewIntentController("cloud-command", host.IntentStore())
	if err != nil {
		t.Fatal(err)
	}
	if err := host.Start([]edgecontrol.ControllerBinding{{Priority: 100, Controller: controller}}); err != nil {
		t.Fatal(err)
	}

	at := time.Date(2026, 8, 29, 1, 5, 0, 0, time.UTC)
	initial := runATV630ConformanceCycle(t, host, at)
	assertATV630ProcessValue(t, initial, "chwp01/RunState", edgecontrol.StringValue("STOPPED"))
	initialFrequency := atv630ProcessDouble(t, initial, "chwp01/Frequency")
	if initialFrequency >= 1 {
		t.Fatalf("stopped CHWP had material residual frequency before conformance commands: %.3f Hz", initialFrequency)
	}
	physicalBeforeWrites := plant.Snapshot().Devices[config.ChilledWaterPump.ID]["frequencyHz"].(float64)

	putATV630Intent(t, host, edgecontrol.ControlIntent{
		ID: "set-frequency-40", Address: "chwp01/FrequencySetpoint", Requested: edgecontrol.DoubleValue(40),
		IssuedAt: at.Add(time.Second), ExpiresAt: at.Add(time.Minute), Source: "CLOUD_COMMAND",
	})
	putATV630Intent(t, host, edgecontrol.ControlIntent{
		ID: "start-chwp", Address: "chwp01/StartCommand", Requested: edgecontrol.BooleanValue(true),
		IssuedAt: at.Add(time.Second), ExpiresAt: at.Add(time.Minute), Source: "CLOUD_COMMAND",
	})

	for step := 1; step <= 3; step++ {
		cycle := runATV630ConformanceCycle(t, host, at.Add(time.Duration(step)*time.Second))
		if cycle.Cycle.Halted || len(cycle.WriteResults) != 2 {
			t.Fatalf("governed START step %d failed: %#v", step, cycle)
		}
	}
	if !host.IntentStore().Revoke("start-chwp") || !host.IntentStore().Revoke("set-frequency-40") {
		t.Fatal("failed to revoke completed START/frequency intents")
	}
	if physical := plant.Snapshot().Devices[config.ChilledWaterPump.ID]["frequencyHz"].(float64); physical != physicalBeforeWrites {
		t.Fatalf("protocol writes mutated physical readback before Plant time advanced: before=%.3f after=%.3f Hz", physicalBeforeWrites, physical)
	}

	plant.Tick(20 * time.Second)
	running := runATV630ConformanceCycle(t, host, at.Add(5*time.Second))
	assertATV630ProcessValue(t, running, "chwp01/RunState", edgecontrol.StringValue("RUNNING"))
	frequency := atv630ProcessDouble(t, running, "chwp01/Frequency")
	if frequency <= 0 || frequency >= 40 {
		t.Fatalf("later independent RFR did not reflect reacting Plant dynamics: %.3f Hz", frequency)
	}
	if flow := plant.Snapshot().Devices[config.ChilledWaterPump.ID]["flowRateM3h"].(float64); flow <= 0 {
		t.Fatalf("protocol START/LFR did not change the reacting CHWP model: flow=%.3f", flow)
	}

	putATV630Intent(t, host, edgecontrol.ControlIntent{
		ID: "stop-chwp", Address: "chwp01/StopCommand", Requested: edgecontrol.BooleanValue(true),
		IssuedAt: at.Add(6 * time.Second), ExpiresAt: at.Add(30 * time.Second), Source: "CLOUD_COMMAND",
	})
	stopping := runATV630ConformanceCycle(t, host, at.Add(7*time.Second))
	if stopping.Cycle.Halted || len(stopping.WriteResults) != 1 || !stopping.WriteResults[0].Success {
		t.Fatalf("governed STOP failed: %#v", stopping)
	}
	if !host.IntentStore().Revoke("stop-chwp") {
		t.Fatal("failed to revoke completed STOP intent")
	}
	plant.Tick(20 * time.Second)
	stopped := runATV630ConformanceCycle(t, host, at.Add(8*time.Second))
	assertATV630ProcessValue(t, stopped, "chwp01/RunState", edgecontrol.StringValue("STOPPED"))
	if afterStop := atv630ProcessDouble(t, stopped, "chwp01/Frequency"); afterStop >= frequency {
		t.Fatalf("later RFR did not decay after governed STOP: before=%.3f after=%.3f", frequency, afterStop)
	}

	if !plant.SetFault(config.ChilledWaterPump.ID, "16") {
		t.Fatal("failed to inject physical ATV630 fault")
	}
	faulted := runATV630ConformanceCycle(t, host, at.Add(9*time.Second))
	assertATV630ProcessValue(t, faulted, "chwp01/RunState", edgecontrol.StringValue("FAULT"))
	assertATV630ProcessValue(t, faulted, "chwp01/FaultCode", edgecontrol.StringValue("16"))

	putATV630Intent(t, host, edgecontrol.ControlIntent{
		ID: "reset-fault", Address: "chwp01/ResetFaultCommand", Requested: edgecontrol.BooleanValue(true),
		IssuedAt: at.Add(10 * time.Second), ExpiresAt: at.Add(30 * time.Second), Source: "CLOUD_COMMAND",
	})
	reset := runATV630ConformanceCycle(t, host, at.Add(11*time.Second))
	if reset.Cycle.Halted || len(reset.WriteResults) != 1 || !reset.WriteResults[0].Success {
		t.Fatalf("governed RESET_FAULT failed: %#v", reset)
	}
	if !host.IntentStore().Revoke("reset-fault") {
		t.Fatal("failed to revoke completed reset intent")
	}
	cleared := runATV630ConformanceCycle(t, host, at.Add(12*time.Second))
	assertATV630ProcessValue(t, cleared, "chwp01/RunState", edgecontrol.StringValue("STOPPED"))
	assertATV630ProcessValue(t, cleared, "chwp01/FaultCode", edgecontrol.StringValue(""))

	if err := server.Stop(); err != nil {
		t.Fatal(err)
	}
	failed, err := host.RunCycle(t.Context(), at.Add(13*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(failed.PollResults) != 1 || failed.PollResults[0].Error == nil {
		t.Fatalf("disconnected ATV630 endpoint did not surface a production poll failure: %#v", failed.PollResults)
	}
	message := failed.PollResults[0].Error.Error()
	for _, want := range []string{endpoint, "ATV630 ETA/RFR", "function=3", "address=3201"} {
		if !strings.Contains(message, want) {
			t.Fatalf("ATV630 failure lost authoritative mapping context %q: %v", want, failed.PollResults[0].Error)
		}
	}
}

func runATV630ConformanceCycle(t *testing.T, host *edgecontrol.Host, at time.Time) edgecontrol.HostCycleResult {
	t.Helper()
	result, err := host.RunCycle(t.Context(), at)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.PollResults) != 1 || result.PollResults[0].Error != nil {
		t.Fatalf("production ATV630 poll failed: %#v", result.PollResults)
	}
	return result
}

func putATV630Intent(t *testing.T, host *edgecontrol.Host, intent edgecontrol.ControlIntent) {
	t.Helper()
	if _, err := host.IntentStore().Put(intent); err != nil {
		t.Fatal(err)
	}
}

func assertATV630ProcessValue(t *testing.T, result edgecontrol.HostCycleResult, address string, want edgecontrol.Value) {
	t.Helper()
	value, ok := result.Cycle.Image.Get(address)
	if !ok || !value.HasValue || value.Sample.Value != want {
		t.Fatalf("unexpected %s process value: %#v want=%#v", address, value, want)
	}
}

func atv630ProcessDouble(t *testing.T, result edgecontrol.HostCycleResult, address string) float64 {
	t.Helper()
	value, ok := result.Cycle.Image.Get(address)
	if !ok || !value.HasValue || value.Sample.Value.Type != edgecontrol.DataTypeDouble {
		t.Fatalf("missing numeric process value %s: %#v", address, value)
	}
	return value.Sample.Value.Double
}
