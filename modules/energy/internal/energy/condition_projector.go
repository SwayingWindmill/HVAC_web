package energy

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const (
	defaultConditionAnchorLookback = 3 * time.Hour
	maxConditionAnchorInsertBatch  = 4096
)

// ConditionInputReader loads raw hourly weather, cooling energy, and AI control inputs from ClickHouse.
type ConditionInputReader interface {
	ReadConditionInputs(ctx context.Context, from, to time.Time) ([]ConditionSampleInput, error)
}

// ConditionAnchorWriter persists aggregated hourly operating condition anchors.
type ConditionAnchorWriter interface {
	InsertConditionAnchors(ctx context.Context, anchors []SiteConditionAnchor) error
}

// ConditionAnchorProjectorConfig configures periodic hourly condition anchor projection.
type ConditionAnchorProjectorConfig struct {
	Reader   ConditionInputReader
	Writer   ConditionAnchorWriter
	Lookback time.Duration
	Now      func() time.Time
}

// ConditionAnchorProjector aggregates weather, load, and AI closed-loop share into
// analytics.site_condition_anchors_hourly.
type ConditionAnchorProjector struct {
	reader   ConditionInputReader
	writer   ConditionAnchorWriter
	lookback time.Duration
	now      func() time.Time
}

// NewConditionAnchorProjector constructs a validated ConditionAnchorProjector.
func NewConditionAnchorProjector(config ConditionAnchorProjectorConfig) (*ConditionAnchorProjector, error) {
	if config.Reader == nil || config.Writer == nil {
		return nil, errors.New("condition anchor projector requires a reader and a writer")
	}
	lookback := config.Lookback
	if lookback <= 0 {
		lookback = defaultConditionAnchorLookback
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return &ConditionAnchorProjector{
		reader:   config.Reader,
		writer:   config.Writer,
		lookback: lookback,
		now:      now,
	}, nil
}

// SyncOnce reads the lookback window up to now, aggregates valid site-hour condition anchors,
// and writes them idempotently to ClickHouse.
func (projector *ConditionAnchorProjector) SyncOnce(ctx context.Context) (int, error) {
	now := projector.now().UTC()
	from := now.Add(-projector.lookback).Truncate(time.Hour)
	if !from.Before(now) {
		from = now.Add(-projector.lookback)
	}
	inputs, err := projector.reader.ReadConditionInputs(ctx, from, now)
	if err != nil {
		return 0, fmt.Errorf("read condition inputs: %w", err)
	}
	if len(inputs) == 0 {
		return 0, nil
	}

	var calcVersion uint64
	if millis := now.UnixMilli(); millis > 0 {
		calcVersion = uint64(millis)
	} else {
		calcVersion = 1
	}
	for i := range inputs {
		if inputs[i].CalcVersion == 0 {
			inputs[i].CalcVersion = calcVersion
		}
		if inputs[i].UpdatedAt.IsZero() {
			inputs[i].UpdatedAt = now
		}
	}

	anchors := BuildSiteConditionAnchors(inputs)
	if len(anchors) == 0 {
		return 0, nil
	}
	for start := 0; start < len(anchors); start += maxConditionAnchorInsertBatch {
		end := min(start+maxConditionAnchorInsertBatch, len(anchors))
		if err := projector.writer.InsertConditionAnchors(ctx, anchors[start:end]); err != nil {
			return 0, fmt.Errorf("insert condition anchors: %w", err)
		}
	}
	return len(anchors), nil
}
