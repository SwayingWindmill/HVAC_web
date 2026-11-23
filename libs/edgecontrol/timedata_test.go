package edgecontrol

import (
	"os"
	"path/filepath"
	"strings"
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

func TestFileTimedataRecoversTruncatedTrailingLineAndContinuesCleanly(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenFileTimedata(dir, PriorityLow)
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime()
	desc := timedataDescriptor("chiller01", "Power", "point-power", PriorityHigh, AccessReadOnly)
	if err := runtime.Register(desc); err != nil {
		t.Fatal(err)
	}
	base := time.Unix(6000, 0).UTC()
	for i := 1; i <= 3; i++ {
		at := base.Add(time.Duration(i) * time.Second)
		if err := runtime.PublishNext(desc.Address(), Sample{Value: DoubleValue(float64(i * 10)), Quality: QualityGood, ObservedAt: at, Sequence: uint64(i)}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.RecordImage(runtime.SwitchProcessImage(at)); err != nil {
			t.Fatal(err)
		}
	}

	historyPath := filepath.Join(dir, "history.jsonl")
	f, err := os.OpenFile(historyPath, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"sequence":4,"address":"chiller01/Power"`); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	_ = f.Close()

	reopened, err := OpenFileTimedata(dir, PriorityLow)
	if err != nil {
		t.Fatalf("OpenFileTimedata failed on truncated trailing line: %v", err)
	}
	if reopened.LatestSequence() != 3 {
		t.Fatalf("LatestSequence() = %d, want 3", reopened.LatestSequence())
	}

	at4 := base.Add(4 * time.Second)
	if err := runtime.PublishNext(desc.Address(), Sample{Value: DoubleValue(40), Quality: QualityGood, ObservedAt: at4, Sequence: 4}); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.RecordImage(runtime.SwitchProcessImage(at4)); err != nil {
		t.Fatalf("RecordImage after crash recovery failed: %v", err)
	}

	raw, err := os.ReadFile(historyPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"sequence":4,"address":"chiller01/Power"`+"\n") == false && len(strings.Split(strings.TrimSpace(string(raw)), "\n")) != 4 {
		t.Fatalf("expected 4 clean lines after truncation and append, got:\n%s", string(raw))
	}

	reopened2, err := OpenFileTimedata(dir, PriorityLow)
	if err != nil {
		t.Fatalf("second reopen failed: %v", err)
	}
	if reopened2.LatestSequence() != 4 {
		t.Fatalf("LatestSequence() after append = %d, want 4", reopened2.LatestSequence())
	}
}

func TestFileTimedataQueryAfterSequenceBatchedAndLookup(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenFileTimedata(dir, PriorityLow)
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime()
	high := timedataDescriptor("chiller01", "Power", "point-power", PriorityHigh, AccessReadOnly)
	low := timedataDescriptor("chiller01", "Temp", "point-temp", PriorityLow, AccessReadOnly)
	for _, desc := range []ChannelDescriptor{high, low} {
		if err := runtime.Register(desc); err != nil {
			t.Fatal(err)
		}
	}

	base := time.Unix(7000, 0).UTC()
	const totalCycles = 1500
	for i := 1; i <= totalCycles; i++ {
		at := base.Add(time.Duration(i) * time.Second)
		_ = runtime.PublishNext(high.Address(), Sample{Value: DoubleValue(float64(i)), Quality: QualityGood, ObservedAt: at, Sequence: uint64(i)})
		_ = runtime.PublishNext(low.Address(), Sample{Value: DoubleValue(float64(i) * 0.1), Quality: QualityGood, ObservedAt: at, Sequence: uint64(i)})
		if _, err := store.RecordImage(runtime.SwitchProcessImage(at)); err != nil {
			t.Fatal(err)
		}
	}

	var cursor uint64
	var drained []TimedataRecord
	for {
		batch, err := store.QueryAfterSequence(cursor, 50, func(rec TimedataRecord) bool {
			return rec.Address == high.Address()
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(batch) == 0 {
			break
		}
		drained = append(drained, batch...)
		cursor = batch[len(batch)-1].Sequence
	}
	if len(drained) != totalCycles {
		t.Fatalf("drained %d HIGH records, want %d", len(drained), totalCycles)
	}
	if seq, ok := store.LookupSequence(high.Address(), base.Add(100*time.Second), 0); !ok || seq == 0 {
		t.Fatalf("expected LookupSequence hit for 100s observation, got seq=%d ok=%v", seq, ok)
	}
}

func TestFileTimedataPurgeClearsMemoryAndDisk(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenFileTimedata(dir, PriorityLow)
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime()
	desc := timedataDescriptor("chiller01", "Power", "point-power", PriorityHigh, AccessReadOnly)
	if err := runtime.Register(desc); err != nil {
		t.Fatal(err)
	}
	at := time.Unix(8000, 0).UTC()
	_ = runtime.PublishNext(desc.Address(), Sample{Value: DoubleValue(42.5), Quality: QualityGood, ObservedAt: at, Sequence: 1})
	if _, err := store.RecordImage(runtime.SwitchProcessImage(at)); err != nil {
		t.Fatal(err)
	}
	if store.LatestSequence() == 0 {
		t.Fatal("expected non-zero LatestSequence before Purge")
	}

	if err := store.Purge(); err != nil {
		t.Fatalf("Purge failed: %v", err)
	}
	if store.LatestSequence() != 0 {
		t.Fatalf("expected LatestSequence=0 after Purge, got %d", store.LatestSequence())
	}
	if _, ok := store.Latest(desc.Address()); ok {
		t.Fatal("expected Latest to miss after Purge")
	}
	reopened, err := OpenFileTimedata(dir, PriorityLow)
	if err != nil {
		t.Fatalf("OpenFileTimedata after Purge failed: %v", err)
	}
	if reopened.LatestSequence() != 0 {
		t.Fatalf("expected clean disk reopen LatestSequence=0, got %d", reopened.LatestSequence())
	}
}
