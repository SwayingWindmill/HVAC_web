package edgecontrol

import (
	"context"
	"testing"
	"time"
)

func TestRemoteIntentLeaseExpiresAndRelinquishesControl(t *testing.T) {
	runtime := NewRuntime()
	descriptor := testDescriptor("chwp01", "FrequencySetpoint", "point-frequency-command", DataTypeDouble, AccessWriteOnly)
	descriptor.Unit = "Hz"
	if err := runtime.Register(descriptor); err != nil {
		t.Fatal(err)
	}
	store, err := NewIntentStore(runtime)
	if err != nil {
		t.Fatal(err)
	}
	issued := time.Unix(1000, 0).UTC()
	if changed, err := store.Put(ControlIntent{
		ID: "command-1", Address: descriptor.Address(), Requested: DoubleValue(42),
		IssuedAt: issued, ExpiresAt: issued.Add(time.Minute), Source: "CLOUD_COMMAND",
	}); err != nil || !changed {
		t.Fatalf("put intent failed: changed=%v err=%v", changed, err)
	}
	controller, err := NewIntentController("cloud-command", store)
	if err != nil {
		t.Fatal(err)
	}
	scheduler, err := NewScheduler([]ControllerBinding{{Priority: 100, Controller: controller}})
	if err != nil {
		t.Fatal(err)
	}
	cycle, err := NewCycle(runtime, scheduler, nil)
	if err != nil {
		t.Fatal(err)
	}
	active := cycle.RunOnce(context.Background(), issued.Add(30*time.Second))
	if len(active.Decisions) != 1 || !active.Decisions[0].Accepted || active.Decisions[0].Effective == nil || active.Decisions[0].Effective.Double != 42 {
		t.Fatalf("leased intent was not active: %#v", active.Decisions)
	}
	expired := cycle.RunOnce(context.Background(), issued.Add(time.Minute))
	if len(expired.Decisions) != 0 {
		t.Fatalf("expired remote intent remained sticky: %#v", expired.Decisions)
	}
}

func TestIntentStoreIsIdempotentAndRejectsStaleReplacement(t *testing.T) {
	runtime := NewRuntime()
	descriptor := testDescriptor("chwp01", "FrequencySetpoint", "point-frequency-command", DataTypeDouble, AccessWriteOnly)
	if err := runtime.Register(descriptor); err != nil {
		t.Fatal(err)
	}
	store, err := NewIntentStore(runtime)
	if err != nil {
		t.Fatal(err)
	}
	issued := time.Unix(1100, 0).UTC()
	first := ControlIntent{ID: "command-1", Address: descriptor.Address(), Requested: DoubleValue(40), IssuedAt: issued, ExpiresAt: issued.Add(time.Minute), Source: "CLOUD_COMMAND"}
	if changed, err := store.Put(first); err != nil || !changed {
		t.Fatalf("first put failed: changed=%v err=%v", changed, err)
	}
	if changed, err := store.Put(first); err != nil || changed {
		t.Fatalf("idempotent replay changed state: changed=%v err=%v", changed, err)
	}
	stale := ControlIntent{ID: "command-stale", Address: descriptor.Address(), Requested: DoubleValue(38), IssuedAt: issued.Add(-time.Second), ExpiresAt: issued.Add(time.Minute), Source: "CLOUD_COMMAND"}
	if changed, err := store.Put(stale); err == nil || changed {
		t.Fatalf("stale replacement was accepted: changed=%v err=%v", changed, err)
	}
	newer := ControlIntent{ID: "command-2", Address: descriptor.Address(), Requested: DoubleValue(44), IssuedAt: issued.Add(time.Second), ExpiresAt: issued.Add(2 * time.Minute), Source: "CLOUD_COMMAND"}
	if changed, err := store.Put(newer); err != nil || !changed {
		t.Fatalf("newer replacement failed: changed=%v err=%v", changed, err)
	}
	active := store.Active(issued.Add(10 * time.Second))
	if len(active) != 1 || active[0].ID != "command-2" {
		t.Fatalf("unexpected active intent: %#v", active)
	}
}

func TestSafetyControllerConstrainsRemoteIntent(t *testing.T) {
	runtime := NewRuntime()
	descriptor := testDescriptor("chwp01", "FrequencySetpoint", "point-frequency-command", DataTypeDouble, AccessWriteOnly)
	if err := runtime.Register(descriptor); err != nil {
		t.Fatal(err)
	}
	store, _ := NewIntentStore(runtime)
	issued := time.Unix(1200, 0).UTC()
	_, _ = store.Put(ControlIntent{ID: "command-1", Address: descriptor.Address(), Requested: DoubleValue(50), IssuedAt: issued, ExpiresAt: issued.Add(time.Minute), Source: "CLOUD_COMMAND"})
	intentController, _ := NewIntentController("cloud-command", store)
	max := 43.0
	safety := controllerFunc{id: "safety", run: func(_ context.Context, _ ProcessImage, plan *ControlPlan) error {
		_, err := plan.ConstrainNumber(descriptor.Address(), "safety", "SAFETY_LIMIT", nil, &max)
		return err
	}}
	scheduler, err := NewScheduler([]ControllerBinding{
		{Priority: 0, Critical: true, Controller: safety},
		{Priority: 100, Controller: intentController},
	})
	if err != nil {
		t.Fatal(err)
	}
	cycle, _ := NewCycle(runtime, scheduler, nil)
	result := cycle.RunOnce(context.Background(), issued.Add(time.Second))
	if result.Halted || len(result.Decisions) != 1 || result.Decisions[0].Effective == nil || result.Decisions[0].Effective.Double != 43 {
		t.Fatalf("safety did not constrain remote intent: %#v", result)
	}
}
