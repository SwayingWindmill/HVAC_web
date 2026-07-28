package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

type sourceFunc func(context.Context, string, []string, time.Time, time.Time, int) (map[string][]ThingsBoardSample, error)

func (function sourceFunc) FetchTimeseries(ctx context.Context, deviceID string, keys []string, start, end time.Time, limit int) (map[string][]ThingsBoardSample, error) {
	return function(ctx, deviceID, keys, start, end, limit)
}

type sinkFunc func(context.Context, Observation) (ObservationReceipt, error)

func (function sinkFunc) AcceptObservation(ctx context.Context, observation Observation) (ObservationReceipt, error) {
	return function(ctx, observation)
}

func TestPipelineMovesThingsBoardSamplesToS2AndPersistsCheckpoint(t *testing.T) {
	config := validConfig()
	config.CheckpointFile = filepath.Join(t.TempDir(), "checkpoint.json")
	config.Devices[0].Points = append(config.Devices[0].Points, PointMapping{SourceKey: "runState", TelemetryKey: "hvac.run_state", ValueType: "STRING"})
	checkpoints, err := OpenCheckpointStore(config.CheckpointFile)
	if err != nil {
		t.Fatal(err)
	}
	source := sourceFunc(func(context.Context, string, []string, time.Time, time.Time, int) (map[string][]ThingsBoardSample, error) {
		return map[string][]ThingsBoardSample{
			"powerKw": {
				{Timestamp: 1000, Value: json.RawMessage(`"10.5"`)},
				{Timestamp: 2000, Value: json.RawMessage(`"12.5"`)},
			},
			"runState": {{Timestamp: 2000, Value: json.RawMessage(`"RUNNING"`)}},
		}, nil
	})
	var observations []Observation
	sink := sinkFunc(func(_ context.Context, observation Observation) (ObservationReceipt, error) {
		observations = append(observations, observation)
		return ObservationReceipt{Status: "ACCEPTED", PositionAdvanced: true}, nil
	})
	pipeline, err := NewPipeline(config, source, sink, checkpoints, func() time.Time { return time.UnixMilli(3000) })
	if err != nil {
		t.Fatal(err)
	}
	report, err := pipeline.PollOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.ObservationCount != 3 || report.StatusCounts["ACCEPTED"] != 3 || len(observations) != 3 {
		t.Fatalf("unexpected poll report %#v observations=%d", report, len(observations))
	}
	if observations[0].SourcePath != "POLL" || observations[0].ExternalEntityType != "DEVICE" || observations[0].Value != 10.5 {
		t.Fatalf("unexpected first observation %#v", observations[0])
	}
	if !uuidV7Pattern.MatchString(observations[0].SourcePosition.EventID) || observations[0].SourcePosition.Partition != "thingsboard:tb-device-1:powerKw" {
		t.Fatalf("unexpected source position %#v", observations[0].SourcePosition)
	}

	secondReport, err := pipeline.PollOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if secondReport.ObservationCount != 0 || len(observations) != 3 {
		t.Fatalf("checkpoint did not suppress replay: %#v observations=%d", secondReport, len(observations))
	}
	reopened, err := OpenCheckpointStore(config.CheckpointFile)
	if err != nil {
		t.Fatal(err)
	}
	if offset, ok := reopened.Offset("thingsboard:tb-device-1:powerKw"); !ok || offset != 2000 {
		t.Fatalf("checkpoint was not persisted: offset=%d ok=%v", offset, ok)
	}
}

func TestPipelineDoesNotAdvanceCheckpointWhenS2Fails(t *testing.T) {
	config := validConfig()
	config.CheckpointFile = filepath.Join(t.TempDir(), "checkpoint.json")
	checkpoints, err := OpenCheckpointStore(config.CheckpointFile)
	if err != nil {
		t.Fatal(err)
	}
	source := sourceFunc(func(context.Context, string, []string, time.Time, time.Time, int) (map[string][]ThingsBoardSample, error) {
		return map[string][]ThingsBoardSample{"powerKw": {{Timestamp: 1000, Value: json.RawMessage(`"10.5"`)}}}, nil
	})
	sink := sinkFunc(func(context.Context, Observation) (ObservationReceipt, error) {
		return ObservationReceipt{}, errors.New("runtime unavailable")
	})
	pipeline, err := NewPipeline(config, source, sink, checkpoints, func() time.Time { return time.UnixMilli(3000) })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pipeline.PollOnce(context.Background()); err == nil {
		t.Fatal("expected S2 failure")
	}
	if _, ok := checkpoints.Offset("thingsboard:tb-device-1:powerKw"); ok {
		t.Fatal("failed S2 delivery advanced checkpoint")
	}
}
