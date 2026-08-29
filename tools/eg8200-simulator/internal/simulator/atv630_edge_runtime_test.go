package simulator

import (
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

func TestATV630EdgeRuntimeKeepsOneStartIntentUntilETAConfirmsOperationEnabled(t *testing.T) {
	config := loadGeneratedCentralPlantConfig(t)
	config.Plant.ChilledWaterPump.InitiallyRunning = false
	at := time.Unix(7_500, 0).UTC()
	plant := NewPlant(config.Plant, config.Scenario, at)
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

	runtime, err := NewATV630EdgeControlRuntime(config, bridge, 1)
	if err != nil {
		t.Fatal(err)
	}
	outcomeCh, err := runtime.SubmitCommand(EdgeCommandIntentRequest{
		CommandID: "single-start", DeviceID: config.Plant.ChilledWaterPump.ID, CommandCode: "START",
		IssuedAt: at, ExpiresAt: at.Add(30 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}

	for step := 1; step <= 3; step++ {
		cycle := runtime.RunCycle(t.Context(), at.Add(time.Duration(step)*time.Second))
		if cycle.Cycle.Halted {
			t.Fatalf("ATV630 START halted at DriveCom step %d: %#v", step, cycle.Cycle)
		}
		select {
		case outcome := <-outcomeCh:
			t.Fatalf("START completed before operation-enabled ETA at step %d: %#v", step, outcome)
		default:
		}
	}

	completedCycle := runtime.RunCycle(t.Context(), at.Add(4*time.Second))
	if completedCycle.Cycle.Halted {
		t.Fatalf("ATV630 START completion cycle halted: %#v", completedCycle.Cycle)
	}
	select {
	case outcome := <-outcomeCh:
		if !outcome.Accepted || outcome.Code != "APPLIED" {
			t.Fatalf("single START intent did not complete after ETA confirmation: %#v", outcome)
		}
	case <-time.After(time.Second):
		t.Fatal("single START intent remained pending after operation-enabled ETA")
	}
}
