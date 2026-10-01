package edgecontrol

import (
	"testing"
	"time"
)

func TestHostRunsProductionAdapterThroughReadControlWriteReadbackCycle(t *testing.T) {
	host, err := NewHost()
	if err != nil {
		t.Fatal(err)
	}
	driver := newFakePumpDriver(ComponentDeviceDriver)
	if err := host.RegisterAdapter(driver); err != nil {
		t.Fatal(err)
	}

	intentController, err := NewIntentController("cloud-command", host.IntentStore())
	if err != nil {
		t.Fatal(err)
	}
	if err := host.Start([]ControllerBinding{{Priority: 100, Controller: intentController}}); err != nil {
		t.Fatal(err)
	}

	at := time.Unix(5_000, 0).UTC()
	_, err = host.IntentStore().Put(ControlIntent{
		ID:        "set-frequency-45",
		Address:   "chwp01/FrequencySetpoint",
		Requested: DoubleValue(45),
		IssuedAt:  at,
		ExpiresAt: at.Add(time.Minute),
		Source:    "CLOUD_COMMAND",
	})
	if err != nil {
		t.Fatal(err)
	}

	first, err := host.RunCycle(t.Context(), at.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(first.PollResults) != 1 || first.PollResults[0].Error != nil {
		t.Fatalf("production adapter poll failed: %#v", first.PollResults)
	}
	frequency, ok := first.Cycle.Image.Get("chwp01/Frequency")
	if !ok || !frequency.HasValue || frequency.Sample.Value.Double != 40 {
		t.Fatalf("controllers did not receive the polled stable Process Image: %#v", frequency)
	}
	if len(first.WriteResults) != 1 || !first.WriteResults[0].Success || driver.frequency != 45 {
		t.Fatalf("governed write did not reach the production adapter at execute-write: results=%#v frequency=%v", first.WriteResults, driver.frequency)
	}
	if got := phaseOrder(first.Cycle.PhaseResults); got != "BEFORE_PROCESS_IMAGE,AFTER_PROCESS_IMAGE,BEFORE_CONTROLLERS,AFTER_CONTROLLERS,BEFORE_WRITE,EXECUTE_WRITE,AFTER_WRITE" {
		t.Fatalf("unexpected Cycle causality: %s", got)
	}

	second, err := host.RunCycle(t.Context(), at.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	frequency, _ = second.Cycle.Image.Get("chwp01/Frequency")
	if !frequency.HasValue || frequency.Sample.Value.Double != 45 {
		t.Fatalf("later independent poll did not read back the governed write: %#v", frequency)
	}
}

func phaseOrder(results []CyclePhaseResult) string {
	var order string
	for _, result := range results {
		if order != "" {
			order += ","
		}
		order += string(result.Phase)
	}
	return order
}
