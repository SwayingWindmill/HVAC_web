package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

type TimeseriesSource interface {
	FetchTimeseries(context.Context, string, []string, time.Time, time.Time, int) (map[string][]ThingsBoardSample, error)
}

type ObservationSink interface {
	AcceptObservation(context.Context, Observation) (ObservationReceipt, error)
}

type Pipeline struct {
	config      Config
	source      TimeseriesSource
	sink        ObservationSink
	checkpoints *CheckpointStore
	now         func() time.Time
}

type PollReport struct {
	DeviceCount      int            `json:"deviceCount"`
	ObservationCount int            `json:"observationCount"`
	StatusCounts     map[string]int `json:"statusCounts"`
	CompletedAt      time.Time      `json:"completedAt"`
}

func NewPipeline(config Config, source TimeseriesSource, sink ObservationSink, checkpoints *CheckpointStore, now func() time.Time) (*Pipeline, error) {
	if source == nil || sink == nil || checkpoints == nil {
		return nil, errors.New("adapter pipeline dependencies are incomplete")
	}
	if now == nil {
		now = time.Now
	}
	return &Pipeline{config: config, source: source, sink: sink, checkpoints: checkpoints, now: now}, nil
}

func (pipeline *Pipeline) PollOnce(ctx context.Context) (PollReport, error) {
	end := pipeline.now().UTC()
	report := PollReport{DeviceCount: len(pipeline.config.Devices), StatusCounts: map[string]int{}, CompletedAt: end}
	for _, device := range pipeline.config.Devices {
		keys := make([]string, 0, len(device.Points))
		defaultStart := end.Add(-pipeline.config.LookbackDuration())
		start := end
		allCheckpointed := true
		for _, point := range device.Points {
			keys = append(keys, point.SourceKey)
			offset, ok := pipeline.checkpoints.Offset(partitionFor(device.ThingsBoardDeviceID, point.SourceKey))
			if !ok {
				allCheckpointed = false
				continue
			}
			candidate := time.UnixMilli(offset + 1).UTC()
			if candidate.Before(start) {
				start = candidate
			}
		}
		if !allCheckpointed {
			start = defaultStart
		}
		if start.After(end) {
			continue
		}
		timeseries, err := pipeline.source.FetchTimeseries(ctx, device.ThingsBoardDeviceID, keys, start, end, pipeline.config.PageLimit)
		if err != nil {
			return report, fmt.Errorf("fetch ThingsBoard telemetry for %s: %w", device.ThingsBoardDeviceID, err)
		}
		for _, point := range device.Points {
			partition := partitionFor(device.ThingsBoardDeviceID, point.SourceKey)
			lastOffset, hasCheckpoint := pipeline.checkpoints.Offset(partition)
			for _, sample := range timeseries[point.SourceKey] {
				if hasCheckpoint && sample.Timestamp <= lastOffset {
					continue
				}
				value, canonical, err := normalizeValue(sample.Value, point.ValueType)
				if err != nil {
					return report, fmt.Errorf("normalize %s/%s at %d: %w", device.ThingsBoardDeviceID, point.SourceKey, sample.Timestamp, err)
				}
				eventID, err := deterministicEventID(sample.Timestamp, partition, canonical)
				if err != nil {
					return report, err
				}
				observation := Observation{
					IntegrationInstanceID: pipeline.config.IntegrationInstanceID,
					SourcePath:            "POLL",
					ExternalEntityType:    "DEVICE",
					ExternalID:            device.ExternalID,
					TelemetryKey:          point.TelemetryKey,
					Value:                 value,
					ValueType:             strings.ToUpper(point.ValueType),
					Unit:                  cloneUnit(point.Unit),
					SampledAt:             time.UnixMilli(sample.Timestamp).UTC().Format(time.RFC3339Nano),
					SourcePosition: SourcePosition{
						Partition: partition,
						Offset:    sample.Timestamp,
						EventID:   eventID,
					},
				}
				receipt, err := pipeline.sink.AcceptObservation(ctx, observation)
				if err != nil {
					return report, fmt.Errorf("accept S2 observation for %s/%s: %w", device.ThingsBoardDeviceID, point.SourceKey, err)
				}
				if err := pipeline.checkpoints.Advance(partition, sample.Timestamp); err != nil {
					return report, err
				}
				lastOffset, hasCheckpoint = sample.Timestamp, true
				report.ObservationCount++
				report.StatusCounts[receipt.Status]++
			}
		}
	}
	return report, nil
}

func normalizeValue(raw json.RawMessage, valueType string) (any, []byte, error) {
	valueType = strings.ToUpper(strings.TrimSpace(valueType))
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil || ensureJSONEOF(decoder) != nil {
		return nil, nil, errors.New("ThingsBoard value is invalid JSON")
	}
	var normalized any
	switch valueType {
	case "NUMBER":
		number, err := normalizedNumber(decoded)
		if err != nil {
			return nil, nil, err
		}
		normalized = number
	case "BOOLEAN":
		boolean, err := normalizedBoolean(decoded)
		if err != nil {
			return nil, nil, err
		}
		normalized = boolean
	case "STRING":
		text, ok := decoded.(string)
		if !ok {
			return nil, nil, errors.New("expected string telemetry value")
		}
		normalized = text
	case "JSON":
		if text, ok := decoded.(string); ok && json.Valid([]byte(text)) {
			innerDecoder := json.NewDecoder(strings.NewReader(text))
			innerDecoder.UseNumber()
			if err := innerDecoder.Decode(&normalized); err != nil || ensureJSONEOF(innerDecoder) != nil {
				return nil, nil, errors.New("expected JSON telemetry value")
			}
		} else {
			normalized = decoded
		}
	default:
		return nil, nil, errors.New("unsupported telemetry valueType")
	}
	canonical, err := json.Marshal(normalized)
	if err != nil {
		return nil, nil, fmt.Errorf("encode normalized telemetry value: %w", err)
	}
	return normalized, canonical, nil
}

func normalizedNumber(value any) (float64, error) {
	var number float64
	switch typed := value.(type) {
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return 0, errors.New("expected numeric telemetry value")
		}
		number = parsed
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err != nil {
			return 0, errors.New("expected numeric telemetry value")
		}
		number = parsed
	default:
		return 0, errors.New("expected numeric telemetry value")
	}
	if math.IsNaN(number) || math.IsInf(number, 0) {
		return 0, errors.New("numeric telemetry value must be finite")
	}
	return number, nil
}

func normalizedBoolean(value any) (bool, error) {
	switch typed := value.(type) {
	case bool:
		return typed, nil
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(typed))
		if err != nil {
			return false, errors.New("expected boolean telemetry value")
		}
		return parsed, nil
	default:
		return false, errors.New("expected boolean telemetry value")
	}
}

func cloneUnit(unit *string) *string {
	if unit == nil {
		return nil
	}
	value := strings.TrimSpace(*unit)
	return &value
}
