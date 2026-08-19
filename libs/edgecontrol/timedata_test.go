package edgecontrol

import (
	"testing"
	"time"
)

func timedataDescriptor(componentID, channelID, pointID string, localPriority DataPriority, access AccessMode) ChannelDescriptor {
	return ChannelDescriptor{
		ComponentID:               componentID,
		ChannelID:                 channelID,
		PointID:                   pointID,
		DataType:                  DataTypeDouble,
		Access:                    access,
		Unit:                      "kW",
		Category:                  ChannelCategoryOpenemsType,
		PollPriority:              PriorityHigh,
		LocalPersistencePriority:  localPriority,
		RemotePersistencePriority: localPriority,
		AggregationPriority:       PriorityMedium,
		ResendPriority:            localPriority,
	}
}

func TestFileTimedataFiltersByLocalPersistenceAndExcludesWriteOnly(t *testing.T) {
	store, err := OpenFileTimedata(t.TempDir(), PriorityHigh)
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime()
	high := timedataDescriptor("chiller01", "Power", "point-power", PriorityHigh, AccessReadOnly)
	low := timedataDescriptor("chiller01", "Diagnostic", "point-diagnostic", PriorityLow, AccessReadOnly)
	writeOnly := timedataDescriptor("chiller01", "Setpoint", "point-setpoint", PriorityVeryHigh, AccessWriteOnly)
	for _, descriptor := range []ChannelDescriptor{high, low, writeOnly} {
		if err := runtime.Register(descriptor); err != nil {
			t.Fatal(err)
		}
	}
	observed := time.Unix(5000, 0).UTC()
	for index, descriptor := range []ChannelDescriptor{high, low, writeOnly} {
		if err := runtime.PublishNext(descriptor.Address(), Sample{Value: DoubleValue(float64(index + 1)), Quality: QualityGood, ObservedAt: observed, Sequence: 1}); err != nil {
			t.Fatal(err)
		}
	}
	written, err := store.RecordImage(runtime.SwitchProcessImage(observed))
	if err != nil {
		t.Fatal(err)
	}
	if written != 1 {
		t.Fatalf("local persistence policy wrote %d records, want 1", written)
	}
	if _, ok := store.Latest(high.Address()); !ok {
		t.Fatal("eligible high-priority Channel was not persisted")
	}
	if _, ok := store.Latest(low.Address()); ok {
		t.Fatal("low-priority Channel bypassed local persistence threshold")
	}
	if _, ok := store.Latest(writeOnly.Address()); ok {
		t.Fatal("write-only Channel was persisted")
	}
}

func TestFileTimedataPersistsHighResolutionHistoryAndRestoresLatest(t *testing.T) {
	directory := t.TempDir()
	store, err := OpenFileTimedata(directory, PriorityLow)
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime()
	descriptor := timedataDescriptor("chwp01", "Power", "point-chwp-power", PriorityHigh, AccessReadOnly)
	if err := runtime.Register(descriptor); err != nil {
		t.Fatal(err)
	}
	base := time.Unix(5100, 0).UTC()
	for index, value := range []float64{10, 11, 12} {
		observed := base.Add(time.Duration(index) * time.Second)
		if err := runtime.PublishNext(descriptor.Address(), Sample{Value: DoubleValue(value), Quality: QualityGood, ObservedAt: observed, Sequence: uint64(index + 1)}); err != nil {
			t.Fatal(err)
		}
		if written, err := store.RecordImage(runtime.SwitchProcessImage(observed)); err != nil || written != 1 {
			t.Fatalf("record sample %d: written=%d err=%v", index, written, err)
		}
	}

	history, err := store.History(descriptor.Address(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 3 || history[0].Sample.Value.Double != 10 || history[1].Sample.Value.Double != 11 || history[2].Sample.Value.Double != 12 {
		t.Fatalf("high-resolution history was not preserved: %#v", history)
	}

	reopened, err := OpenFileTimedata(directory, PriorityLow)
	if err != nil {
		t.Fatal(err)
	}
	latest, ok := reopened.Latest(descriptor.Address())
	if !ok || latest.Sample.Value.Double != 12 || latest.PointID != descriptor.PointID || latest.Sequence != 3 {
		t.Fatalf("latest value did not survive restart: %#v ok=%v", latest, ok)
	}

	observed := base.Add(3 * time.Second)
	if err := runtime.PublishNext(descriptor.Address(), Sample{Value: DoubleValue(13), Quality: QualityGood, ObservedAt: observed, Sequence: 4}); err != nil {
		t.Fatal(err)
	}
	if written, err := reopened.RecordImage(runtime.SwitchProcessImage(observed)); err != nil || written != 1 {
		t.Fatalf("record after reopen: written=%d err=%v", written, err)
	}
	latest, _ = reopened.Latest(descriptor.Address())
	if latest.Sequence != 4 || latest.Sample.Value.Double != 13 {
		t.Fatalf("durable Timedata sequence did not continue after restart: %#v", latest)
	}
}

func TestFileTimedataDoesNotDuplicateUnchangedProcessImage(t *testing.T) {
	store, err := OpenFileTimedata(t.TempDir(), PriorityLow)
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime()
	descriptor := timedataDescriptor("chiller01", "Power", "point-chiller-power", PriorityHigh, AccessReadOnly)
	if err := runtime.Register(descriptor); err != nil {
		t.Fatal(err)
	}
	observed := time.Unix(5200, 0).UTC()
	if err := runtime.PublishNext(descriptor.Address(), Sample{Value: DoubleValue(220), Quality: QualityGood, ObservedAt: observed, Sequence: 1}); err != nil {
		t.Fatal(err)
	}
	if written, err := store.RecordImage(runtime.SwitchProcessImage(observed)); err != nil || written != 1 {
		t.Fatalf("record first image: written=%d err=%v", written, err)
	}
	// Channel UPDATE fires every Cycle, but local Timedata must not duplicate the
	// identical canonical observation just because another Process Image was cut.
	if written, err := store.RecordImage(runtime.SwitchProcessImage(observed.Add(time.Second))); err != nil || written != 0 {
		t.Fatalf("unchanged process image duplicated Timedata: written=%d err=%v", written, err)
	}
}

func TestFileTimedataQueryRangeSelectsTimeAndChannels(t *testing.T) {
	store, err := OpenFileTimedata(t.TempDir(), PriorityLow)
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime()
	first := timedataDescriptor("chwp01", "Power", "point-power-1", PriorityHigh, AccessReadOnly)
	second := timedataDescriptor("cwp01", "Power", "point-power-2", PriorityHigh, AccessReadOnly)
	for _, descriptor := range []ChannelDescriptor{first, second} {
		if err := runtime.Register(descriptor); err != nil {
			t.Fatal(err)
		}
	}
	base := time.Unix(5300, 0).UTC()
	for sequence := 1; sequence <= 3; sequence++ {
		observed := base.Add(time.Duration(sequence-1) * time.Minute)
		for index, descriptor := range []ChannelDescriptor{first, second} {
			if err := runtime.PublishNext(descriptor.Address(), Sample{Value: DoubleValue(float64(sequence*10 + index)), Quality: QualityGood, ObservedAt: observed, Sequence: uint64(sequence)}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := store.RecordImage(runtime.SwitchProcessImage(observed)); err != nil {
			t.Fatal(err)
		}
	}

	records, err := store.QueryRange(base.Add(time.Minute), base.Add(3*time.Minute), []string{first.Address()}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[0].Address != first.Address() || records[0].Sample.Value.Double != 20 || records[1].Sample.Value.Double != 30 {
		t.Fatalf("range query returned wrong records: %#v", records)
	}
}
