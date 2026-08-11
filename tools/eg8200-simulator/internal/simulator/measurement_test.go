package simulator

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestMeasurementSchedulerPublishesPointsIndependently(t *testing.T) {
	config := testConfig()
	config.Points = config.Points[:2]
	start := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	plant := NewPlant(config.Plant, start)
	scheduler, err := NewMeasurementScheduler(config)
	if err != nil {
		t.Fatal(err)
	}

	first, err := scheduler.Observe(plant.Tick(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || first[0].ObservedAt != first[1].ObservedAt {
		t.Fatalf("initial independent point publication mismatch: %#v", first)
	}
	if first[0].SensorID == first[1].SensorID || first[0].Sequence != 1 || first[1].Sequence != 1 {
		t.Fatalf("Sensor identity or sequence was not preserved: %#v", first)
	}

	second, err := scheduler.Observe(plant.Tick(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 0 {
		t.Fatalf("points published before their independent schedules: %#v", second)
	}

	third, err := scheduler.Observe(plant.Tick(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(third) != 1 || third[0].TelemetryKey != "chiller.leaving_chilled_water_temperature" || third[0].Sequence != 3 {
		t.Fatalf("fast Sensor point did not publish independently: %#v", third)
	}

	for index := 0; index < 2; index++ {
		if _, err := scheduler.Observe(plant.Tick(time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	sixth, err := scheduler.Observe(plant.Tick(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(sixth) != 1 || sixth[0].TelemetryKey != "chiller.power" || sixth[0].Sequence != 3 {
		t.Fatalf("slow Sensor retained the wrong independent schedule: %#v", sixth)
	}
}

func TestMeasurementSchedulerRejectsMissingSourcePoint(t *testing.T) {
	config := testConfig()
	config.Points[0].SourceKey = "missingSource"
	scheduler, err := NewMeasurementScheduler(config)
	if err != nil {
		t.Fatal(err)
	}
	plant := NewPlant(config.Plant, time.Now().UTC())
	if _, err := scheduler.Observe(plant.Tick(time.Second)); err == nil {
		t.Fatal("expected missing source point failure")
	}
}

func TestMeasurementSchedulerContinuesPersistedSequences(t *testing.T) {
	config := testConfig()
	config.Points = config.Points[:1]
	pointKey := pointReference(config.Points[0].DeviceID, config.Points[0].TelemetryKey)
	scheduler, err := NewMeasurementSchedulerWithSequences(config, map[string]uint64{pointKey: 41})
	if err != nil {
		t.Fatal(err)
	}
	plant := NewPlant(config.Plant, time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC))
	measurements, err := scheduler.Observe(plant.Tick(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(measurements) != 1 || measurements[0].Sequence != 42 {
		t.Fatalf("persisted sequence did not continue monotonically: %#v", measurements)
	}
	if got := scheduler.Sequences()[pointKey]; got != 42 {
		t.Fatalf("scheduler sequence snapshot=%d want=42", got)
	}
}

func TestMeasurementSequenceStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "measurement-sequences.v1.json")
	want := map[string]uint64{"METER-01/hvac_meter.energy": 128, "CHILLER-01/chiller.power": 77}
	if err := SaveMeasurementSequences(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadMeasurementSequences(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sequence state round trip=%v want=%v", got, want)
	}
}
