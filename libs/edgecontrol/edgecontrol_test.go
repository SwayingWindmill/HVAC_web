package edgecontrol

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func testDescriptor(component, channel, point string, dataType DataType, access AccessMode) ChannelDescriptor {
	return ChannelDescriptor{
		ComponentID:               component,
		ChannelID:                 channel,
		PointID:                   point,
		DataType:                  dataType,
		Unit:                      "",
		Access:                    access,
		Category:                  ChannelCategoryOpenemsType,
		PollPriority:              PriorityHigh,
		LocalPersistencePriority:  PriorityHigh,
		RemotePersistencePriority: PriorityHigh,
		AggregationPriority:       PriorityLow,
		ResendPriority:            PriorityHigh,
	}
}

func TestProcessImageIsStableWithinCycle(t *testing.T) {
	runtime := NewRuntime()
	descriptor := testDescriptor("chwp01", "Frequency", "point-frequency", DataTypeDouble, AccessReadWrite)
	if err := runtime.Register(descriptor); err != nil {
		t.Fatal(err)
	}
	observed := time.Unix(100, 0).UTC()
	if err := runtime.PublishNext(descriptor.Address(), Sample{Value: DoubleValue(40), Quality: QualityGood, ObservedAt: observed, Sequence: 1}); err != nil {
		t.Fatal(err)
	}
	first := runtime.SwitchProcessImage(time.Unix(101, 0).UTC())
	firstSnapshot, ok := first.Get(descriptor.Address())
	if !ok || !firstSnapshot.HasValue || firstSnapshot.Sample.Value.Double != 40 {
		t.Fatalf("unexpected first process image: %#v", firstSnapshot)
	}

	if err := runtime.PublishNext(descriptor.Address(), Sample{Value: DoubleValue(42), Quality: QualityGood, ObservedAt: time.Unix(102, 0).UTC(), Sequence: 2}); err != nil {
		t.Fatal(err)
	}
	stillFirst, _ := first.Get(descriptor.Address())
	if stillFirst.Sample.Value.Double != 40 {
		t.Fatalf("process image mutated inside cycle: got %v", stillFirst.Sample.Value.Double)
	}

	second := runtime.SwitchProcessImage(time.Unix(103, 0).UTC())
	secondSnapshot, _ := second.Get(descriptor.Address())
	if second.Cycle() != first.Cycle()+1 || secondSnapshot.Sample.Value.Double != 42 {
		t.Fatalf("next value was not promoted at cycle boundary: cycle=%d value=%v", second.Cycle(), secondSnapshot.Sample.Value.Double)
	}
}

func TestChannelEventsFollowNextUpdateChangeLifecycle(t *testing.T) {
	runtime := NewRuntime()
	descriptor := testDescriptor("chwp01", "Frequency", "point-frequency-events", DataTypeDouble, AccessReadOnly)
	if err := runtime.Register(descriptor); err != nil {
		t.Fatal(err)
	}
	events := make([]ChannelEventType, 0)
	for _, eventType := range []ChannelEventType{ChannelEventNextValue, ChannelEventUpdate, ChannelEventChange} {
		eventType := eventType
		_, err := runtime.Subscribe(descriptor.Address(), eventType, func(event ChannelEvent) {
			events = append(events, event.Type)
			// Event callbacks must not run under the Runtime mutex. Re-entering a
			// read method here would deadlock if dispatch happened while locked.
			if _, ok := runtime.Descriptor(event.Address); !ok {
				t.Fatalf("event references unknown channel %s", event.Address)
			}
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	first := Sample{Value: DoubleValue(40), Quality: QualityGood, ObservedAt: time.Unix(110, 0).UTC(), Sequence: 1}
	if err := runtime.PublishNext(descriptor.Address(), first); err != nil {
		t.Fatal(err)
	}
	runtime.SwitchProcessImage(time.Unix(111, 0).UTC())
	if got, want := events, []ChannelEventType{ChannelEventNextValue, ChannelEventUpdate, ChannelEventChange}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected first Channel event lifecycle: got=%v want=%v", got, want)
	}

	// A new observation with the same value is still an UPDATE, but not a CHANGE.
	events = events[:0]
	second := Sample{Value: DoubleValue(40), Quality: QualityGood, ObservedAt: time.Unix(112, 0).UTC(), Sequence: 2}
	if err := runtime.PublishNext(descriptor.Address(), second); err != nil {
		t.Fatal(err)
	}
	runtime.SwitchProcessImage(time.Unix(113, 0).UTC())
	if got, want := events, []ChannelEventType{ChannelEventNextValue, ChannelEventUpdate}; !reflect.DeepEqual(got, want) {
		t.Fatalf("same value should update without change: got=%v want=%v", got, want)
	}

	// OpenEMS nextProcessImage() invokes onUpdate every cycle even when no new
	// asynchronous next value was set.
	events = events[:0]
	runtime.SwitchProcessImage(time.Unix(114, 0).UTC())
	if got, want := events, []ChannelEventType{ChannelEventUpdate}; !reflect.DeepEqual(got, want) {
		t.Fatalf("cycle without new input must still update active Channel: got=%v want=%v", got, want)
	}
}

func TestChannelPastSamplesRetainOpenEMSRuntimeWindow(t *testing.T) {
	runtime := NewRuntime()
	descriptor := testDescriptor("chwp01", "Power", "point-power-history", DataTypeDouble, AccessReadOnly)
	if err := runtime.Register(descriptor); err != nil {
		t.Fatal(err)
	}
	base := time.Unix(1000, 0).UTC()
	for index, offset := range []time.Duration{0, 5 * time.Minute, 5*time.Minute + 11*time.Second} {
		if err := runtime.PublishNext(descriptor.Address(), Sample{
			Value: DoubleValue(float64(index + 1)), Quality: QualityGood, ObservedAt: base.Add(offset), Sequence: uint64(index + 1),
		}); err != nil {
			t.Fatal(err)
		}
		runtime.SwitchProcessImage(base.Add(offset).Add(time.Millisecond))
	}
	past := runtime.PastSamples(descriptor.Address())
	if len(past) != 2 || past[0].Value.Double != 2 || past[1].Value.Double != 3 {
		t.Fatalf("runtime history did not retain the OpenEMS 5m10s window: %#v", past)
	}
	if !runtime.Unregister(descriptor.Address()) || runtime.Unregister(descriptor.Address()) {
		t.Fatal("channel unregister/deactivate semantics are not idempotent")
	}
	if _, ok := runtime.Descriptor(descriptor.Address()); ok {
		t.Fatal("unregistered Channel remained active")
	}
}

func TestHigherPriorityConstraintClampsLowerPriorityIntent(t *testing.T) {
	runtime := NewRuntime()
	descriptor := testDescriptor("chwp01", "FrequencySetpoint", "point-frequency-command", DataTypeDouble, AccessWriteOnly)
	if err := runtime.Register(descriptor); err != nil {
		t.Fatal(err)
	}
	image := runtime.SwitchProcessImage(time.Unix(200, 0).UTC())
	plan := NewControlPlan(image)
	max := 43.0
	if changed, err := plan.ConstrainNumber(descriptor.Address(), "safety", "SAFETY_LIMIT", nil, &max); err != nil || !changed {
		t.Fatalf("constraint failed: changed=%v err=%v", changed, err)
	}
	decision, changed, err := plan.Request(descriptor.Address(), "cloud-intent", DoubleValue(50))
	if err != nil || !changed {
		t.Fatalf("request failed: changed=%v err=%v", changed, err)
	}
	if !decision.Accepted || decision.Effective == nil || decision.Effective.Double != 43 {
		t.Fatalf("expected clamped 43 Hz decision, got %#v", decision)
	}
	if len(decision.ConstraintReasons) != 1 || decision.ConstraintReasons[0].Reason != "SAFETY_LIMIT" {
		t.Fatalf("constraint evidence missing: %#v", decision.ConstraintReasons)
	}

	later, changed, err := plan.Request(descriptor.Address(), "manual", DoubleValue(35))
	if err != nil {
		t.Fatal(err)
	}
	if changed || later.ControllerID != "cloud-intent" || later.Effective == nil || later.Effective.Double != 43 {
		t.Fatalf("lower-priority request overwrote decision: %#v changed=%v", later, changed)
	}
}

func TestLaterConstraintIsValidatedAgainstExistingDecision(t *testing.T) {
	runtime := NewRuntime()
	descriptor := testDescriptor("chwp01", "FrequencySetpoint", "point-late-constraint", DataTypeDouble, AccessWriteOnly)
	if err := runtime.Register(descriptor); err != nil {
		t.Fatal(err)
	}
	plan := NewControlPlan(runtime.SwitchProcessImage(time.Unix(250, 0).UTC()))
	decision, changed, err := plan.Request(descriptor.Address(), "first-controller", DoubleValue(40))
	if err != nil || !changed || decision.Effective == nil || decision.Effective.Double != 40 {
		t.Fatalf("initial request failed: decision=%#v changed=%v err=%v", decision, changed, err)
	}

	compatibleMax := 45.0
	if changed, err := plan.ConstrainNumber(descriptor.Address(), "later-controller", "COMPATIBLE_LIMIT", nil, &compatibleMax); err != nil || !changed {
		t.Fatalf("compatible later constraint was not accepted: changed=%v err=%v", changed, err)
	}
	conflictingMin := 42.0
	if changed, err := plan.ConstrainNumber(descriptor.Address(), "conflicting-controller", "CONFLICTING_LIMIT", &conflictingMin, nil); !errors.Is(err, ErrConstraintConflict) || changed {
		t.Fatalf("conflicting later constraint was not rejected: changed=%v err=%v", changed, err)
	}
	decisions := plan.Decisions()
	if len(decisions) != 1 || decisions[0].Effective == nil || decisions[0].Effective.Double != 40 {
		t.Fatalf("rejected constraint changed established feasible target: %#v", decisions)
	}
}

func TestInterlockDenyBlocksLowerPriorityStart(t *testing.T) {
	runtime := NewRuntime()
	descriptor := testDescriptor("chiller01", "Start", "point-start-command", DataTypeBoolean, AccessWriteOnly)
	if err := runtime.Register(descriptor); err != nil {
		t.Fatal(err)
	}
	plan := NewControlPlan(runtime.SwitchProcessImage(time.Unix(300, 0).UTC()))
	if changed, err := plan.Deny(descriptor.Address(), "interlock", "CHILLED_WATER_PUMP_NOT_RUNNING"); err != nil || !changed {
		t.Fatalf("deny failed: changed=%v err=%v", changed, err)
	}
	decision, changed, err := plan.Request(descriptor.Address(), "cloud-intent", BooleanValue(true))
	if err != nil || !changed {
		t.Fatalf("request failed: changed=%v err=%v", changed, err)
	}
	if decision.Accepted || decision.Effective != nil || len(decision.ConstraintReasons) != 1 {
		t.Fatalf("interlocked start was not rejected: %#v", decision)
	}
}

type controllerFunc struct {
	id  string
	run func(context.Context, ProcessImage, *ControlPlan) error
}

func (controller controllerFunc) ID() string { return controller.id }
func (controller controllerFunc) Run(ctx context.Context, image ProcessImage, plan *ControlPlan) error {
	return controller.run(ctx, image, plan)
}

func TestAllControllersObserveTheSameImmutableCycleImage(t *testing.T) {
	runtime := NewRuntime()
	descriptor := testDescriptor("chwp01", "Frequency", "point-cycle-snapshot", DataTypeDouble, AccessReadOnly)
	if err := runtime.Register(descriptor); err != nil {
		t.Fatal(err)
	}
	firstSample := Sample{Value: DoubleValue(40), Quality: QualityGood, ObservedAt: time.Unix(320, 0).UTC(), Sequence: 1}
	if err := runtime.PublishNext(descriptor.Address(), firstSample); err != nil {
		t.Fatal(err)
	}
	observed := make([]float64, 0, 2)
	controllers := []ControllerBinding{
		{Controller: controllerFunc{id: "first", run: func(_ context.Context, image ProcessImage, _ *ControlPlan) error {
			snapshot, _ := image.Get(descriptor.Address())
			observed = append(observed, snapshot.Sample.Value.Double)
			return runtime.PublishNext(descriptor.Address(), Sample{Value: DoubleValue(42), Quality: QualityGood, ObservedAt: time.Unix(321, 0).UTC(), Sequence: 2})
		}}},
		{Controller: controllerFunc{id: "second", run: func(_ context.Context, image ProcessImage, _ *ControlPlan) error {
			snapshot, _ := image.Get(descriptor.Address())
			observed = append(observed, snapshot.Sample.Value.Double)
			return nil
		}}},
	}
	scheduler, err := NewScheduler(controllers)
	if err != nil {
		t.Fatal(err)
	}
	cycle, err := NewCycle(runtime, scheduler, nil)
	if err != nil {
		t.Fatal(err)
	}
	result := cycle.RunOnce(context.Background(), time.Unix(320, 0).UTC())
	if result.Halted || !reflect.DeepEqual(observed, []float64{40, 40}) {
		t.Fatalf("controllers did not share one immutable Process Image: halted=%v observed=%v", result.Halted, observed)
	}
}

func TestSchedulerPreservesConfiguredOrderAndDeduplicates(t *testing.T) {
	runtime := NewRuntime()
	order := make([]string, 0)
	first := controllerFunc{id: "first", run: func(context.Context, ProcessImage, *ControlPlan) error { order = append(order, "first"); return nil }}
	second := controllerFunc{id: "second", run: func(context.Context, ProcessImage, *ControlPlan) error { order = append(order, "second"); return nil }}
	scheduler, err := NewScheduler([]ControllerBinding{
		{Priority: 100, Controller: first},
		{Priority: 0, Controller: second},
		{Priority: -100, Controller: first},
	})
	if err != nil {
		t.Fatal(err)
	}
	cycle, err := NewCycle(runtime, scheduler, nil)
	if err != nil {
		t.Fatal(err)
	}
	result := cycle.RunOnce(context.Background(), time.Unix(340, 0).UTC())
	if result.Halted {
		t.Fatal("configured scheduler unexpectedly halted")
	}
	if got, want := order, []string{"first", "second"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("scheduler did not preserve FixedOrder semantics: got=%v want=%v", got, want)
	}
	if len(result.ControllerResults) != 2 {
		t.Fatalf("duplicate controller executed more than once: %#v", result.ControllerResults)
	}
}

type outputWriterFunc func(context.Context, ProcessImage, []Decision) error

func (writer outputWriterFunc) Apply(ctx context.Context, image ProcessImage, decisions []Decision) error {
	return writer(ctx, image, decisions)
}

func TestCyclePhasesFollowProcessControllersWriteOrder(t *testing.T) {
	runtime := NewRuntime()
	descriptor := testDescriptor("chwp01", "FrequencySetpoint", "point-cycle-phase", DataTypeDouble, AccessWriteOnly)
	if err := runtime.Register(descriptor); err != nil {
		t.Fatal(err)
	}
	observed := time.Unix(350, 0).UTC()
	if err := runtime.PublishNext(descriptor.Address(), Sample{Value: DoubleValue(40), Quality: QualityGood, ObservedAt: observed, Sequence: 1}); err != nil {
		t.Fatal(err)
	}
	sequence := make([]string, 0)
	scheduler, err := NewScheduler([]ControllerBinding{{
		Priority: 10,
		Controller: controllerFunc{id: "controller", run: func(_ context.Context, image ProcessImage, plan *ControlPlan) error {
			sequence = append(sequence, "CONTROLLER")
			snapshot, ok := image.Get(descriptor.Address())
			if !ok || !snapshot.HasValue || snapshot.Sample.Value.Double != 40 {
				return errors.New("controller did not receive promoted Process Image")
			}
			_, _, requestErr := plan.Request(descriptor.Address(), "controller", DoubleValue(42))
			return requestErr
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	cycle, err := NewCycle(runtime, scheduler, outputWriterFunc(func(_ context.Context, _ ProcessImage, decisions []Decision) error {
		sequence = append(sequence, "OUTPUT")
		if len(decisions) != 1 || decisions[0].Effective == nil || decisions[0].Effective.Double != 42 {
			return errors.New("unexpected output decision")
		}
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	for _, phase := range orderedCyclePhases {
		phase := phase
		if err := cycle.AddHook(CycleHookBinding{ID: "trace-" + string(phase), Phase: phase, Hook: func(context.Context, CyclePhaseContext) error {
			sequence = append(sequence, string(phase))
			return nil
		}}); err != nil {
			t.Fatal(err)
		}
	}
	result := cycle.RunOnce(context.Background(), time.Unix(351, 0).UTC())
	if result.Halted || result.OutputError != nil {
		t.Fatalf("cycle unexpectedly failed: halted=%v output=%v", result.Halted, result.OutputError)
	}
	want := []string{
		string(CyclePhaseBeforeProcessImage),
		string(CyclePhaseAfterProcessImage),
		string(CyclePhaseBeforeControllers),
		"CONTROLLER",
		string(CyclePhaseAfterControllers),
		string(CyclePhaseBeforeWrite),
		string(CyclePhaseExecuteWrite),
		"OUTPUT",
		string(CyclePhaseAfterWrite),
	}
	if !reflect.DeepEqual(sequence, want) {
		t.Fatalf("unexpected cycle lifecycle: got=%v want=%v", sequence, want)
	}
	if len(result.PhaseResults) != len(orderedCyclePhases) || result.Duration <= 0 {
		t.Fatalf("cycle evidence is incomplete: phases=%d duration=%s", len(result.PhaseResults), result.Duration)
	}
}

func TestOrdinaryControllerFailureIsIsolated(t *testing.T) {
	runtime := NewRuntime()
	called := false
	scheduler, err := NewScheduler([]ControllerBinding{
		{Priority: 0, Controller: controllerFunc{id: "faulty", run: func(context.Context, ProcessImage, *ControlPlan) error { return errors.New("controller failed") }}},
		{Priority: 10, Controller: controllerFunc{id: "healthy", run: func(context.Context, ProcessImage, *ControlPlan) error { called = true; return nil }}},
	})
	if err != nil {
		t.Fatal(err)
	}
	cycle, err := NewCycle(runtime, scheduler, nil)
	if err != nil {
		t.Fatal(err)
	}
	result := cycle.RunOnce(context.Background(), time.Unix(360, 0).UTC())
	if result.Halted || !called || len(result.ControllerResults) != 2 || result.ControllerResults[0].Error == nil {
		t.Fatalf("ordinary controller error was not isolated: halted=%v called=%v results=%#v", result.Halted, called, result.ControllerResults)
	}
}

func TestCriticalControllerFailureHaltsCycleFailClosed(t *testing.T) {
	runtime := NewRuntime()
	descriptor := testDescriptor("chwp01", "FrequencySetpoint", "point-frequency-command", DataTypeDouble, AccessWriteOnly)
	if err := runtime.Register(descriptor); err != nil {
		t.Fatal(err)
	}
	called := false
	scheduler, err := NewScheduler([]ControllerBinding{
		{
			Priority: 0,
			Critical: true,
			Controller: controllerFunc{id: "safety", run: func(context.Context, ProcessImage, *ControlPlan) error {
				return errors.New("safety input unavailable")
			}},
		},
		{
			Priority: 100,
			Controller: controllerFunc{id: "cloud-intent", run: func(_ context.Context, _ ProcessImage, plan *ControlPlan) error {
				called = true
				_, _, requestErr := plan.Request(descriptor.Address(), "cloud-intent", DoubleValue(45))
				return requestErr
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	cycle, err := NewCycle(runtime, scheduler, nil)
	if err != nil {
		t.Fatal(err)
	}
	result := cycle.RunOnce(context.Background(), time.Unix(400, 0).UTC())
	if !result.Halted || called || len(result.Decisions) != 0 {
		t.Fatalf("critical controller failure did not halt fail-closed: halted=%v called=%v decisions=%#v", result.Halted, called, result.Decisions)
	}
}

func TestCycleRejectsRegressingClockBeforeProcessImage(t *testing.T) {
	runtime := NewRuntime()
	scheduler, err := NewScheduler(nil)
	if err != nil {
		t.Fatal(err)
	}
	cycle, err := NewCycle(runtime, scheduler, nil)
	if err != nil {
		t.Fatal(err)
	}
	firstAt := time.Unix(500, 0).UTC()
	first := cycle.RunOnce(context.Background(), firstAt)
	if first.ClockError != nil || first.Halted {
		t.Fatalf("first cycle failed: %#v", first)
	}
	regressed := cycle.RunOnce(context.Background(), firstAt.Add(-time.Second))
	if !regressed.Halted || !errors.Is(regressed.ClockError, ErrCycleClockRegression) {
		t.Fatalf("regressing Edge clock was not rejected: %#v", regressed)
	}
	if regressed.Image.Cycle() != 0 {
		t.Fatalf("regressing clock advanced the Process Image: cycle=%d", regressed.Image.Cycle())
	}
}

func TestRejectedDeviceWriteMarksCycleHalted(t *testing.T) {
	runtime := NewRuntime()
	descriptor := testDescriptor("chwp01", "FrequencySetpoint", "point-rejected-write", DataTypeDouble, AccessWriteOnly)
	if err := runtime.Register(descriptor); err != nil {
		t.Fatal(err)
	}
	scheduler, err := NewScheduler([]ControllerBinding{{Controller: controllerFunc{id: "controller", run: func(_ context.Context, _ ProcessImage, plan *ControlPlan) error {
		_, _, err := plan.Request(descriptor.Address(), "controller", DoubleValue(42))
		return err
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	cycle, err := NewCycle(runtime, scheduler, outputWriterFunc(func(context.Context, ProcessImage, []Decision) error {
		return errors.New("device rejected write")
	}))
	if err != nil {
		t.Fatal(err)
	}
	result := cycle.RunOnce(context.Background(), time.Unix(600, 0).UTC())
	if !result.Halted || result.OutputError == nil {
		t.Fatalf("rejected device write did not fail closed: %#v", result)
	}
}
