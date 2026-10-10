package telemetry

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const historyInsertFailureCode = "CLICKHOUSE_INSERT_FAILED"

type HistoryObservation struct {
	ObservationID          string    `json:"observation_id"`
	TenantID               *string   `json:"tenant_id"`
	SiteID                 *string   `json:"site_id"`
	DeviceID               *string   `json:"device_id"`
	PointID                *string   `json:"point_id"`
	SensorID               *string   `json:"sensor_id"`
	SourceID               string    `json:"source_id"`
	SourceEventID          string    `json:"source_event_id"`
	SourcePartition        string    `json:"source_partition"`
	SourceOffset           int64     `json:"source_offset"`
	SourcePath             string    `json:"source_path"`
	TelemetryKey           string    `json:"telemetry_key"`
	PointType              *string   `json:"point_type"`
	PointRevision          *int64    `json:"point_revision"`
	CounterDecreaseMode    *string   `json:"counter_decrease_mode"`
	CounterRolloverModulus *float64  `json:"counter_rollover_modulus"`
	ValueType              *string   `json:"value_type"`
	Unit                   *string   `json:"unit"`
	ValueJSON              *string   `json:"value_json"`
	ValueNumber            *float64  `json:"value_number"`
	ValueString            *string   `json:"value_string"`
	ValueBoolean           *uint8    `json:"value_boolean"`
	SampledAt              time.Time `json:"sampled_at"`
	ReceivedAt             time.Time `json:"received_at"`
	AcceptanceStatus       string    `json:"acceptance_status"`
	Quality                string    `json:"quality"`
	QualityReasons         []string  `json:"quality_reasons"`
	PayloadSHA256          string    `json:"payload_sha256"`
	// HistorySequence is the batch's History Sequence, assigned when the batch is claimed.
	// It is absent from outbox payloads and set only on the ClickHouse insert.
	HistorySequence uint64 `json:"history_sequence,omitempty"`
}

type HistoryBatch struct {
	LeaseID string
	BatchID string
	// Sequence increases with every new batch. Batches reach ClickHouse one at a time, so it
	// orders history by visibility, which readers use as a durable cursor.
	Sequence     uint64
	Observations []HistoryObservation
}

type HistoryRepository interface {
	ClaimHistoryBatch(context.Context, int, time.Time, time.Duration, int) (HistoryBatch, error)
	MarkHistoryBatchPublished(context.Context, string, time.Time) error
	RetryHistoryBatch(context.Context, string, time.Time, string, int) error
}

type HistorySink interface {
	InsertObservations(context.Context, uint64, []HistoryObservation) error
}

type HistoryRelayConfig struct {
	Repository  HistoryRepository
	Sink        HistorySink
	BatchSize   int
	LeaseFor    time.Duration
	RetryAfter  time.Duration
	MaxAttempts int
	Now         func() time.Time
}

type HistoryRelay struct {
	repository  HistoryRepository
	sink        HistorySink
	batchSize   int
	leaseFor    time.Duration
	retryAfter  time.Duration
	maxAttempts int
	now         func() time.Time
}

func NewHistoryRelay(config HistoryRelayConfig) (*HistoryRelay, error) {
	if config.Repository == nil || config.Sink == nil {
		return nil, errors.New("history relay repository and sink are required")
	}
	if config.BatchSize < 1 || config.BatchSize > 4096 {
		return nil, errors.New("history relay batch size must be between 1 and 4096")
	}
	if config.LeaseFor < time.Second || config.LeaseFor > 10*time.Minute {
		return nil, errors.New("history relay lease duration must be between 1 second and 10 minutes")
	}
	if config.RetryAfter < time.Second || config.RetryAfter > time.Hour {
		return nil, errors.New("history relay retry delay must be between 1 second and 1 hour")
	}
	if config.MaxAttempts < 1 || config.MaxAttempts > 100 {
		return nil, errors.New("history relay max attempts must be between 1 and 100")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &HistoryRelay{
		repository: config.Repository, sink: config.Sink, batchSize: config.BatchSize,
		leaseFor: config.LeaseFor, retryAfter: config.RetryAfter, maxAttempts: config.MaxAttempts,
		now: config.Now,
	}, nil
}

func (relay *HistoryRelay) RelayOnce(ctx context.Context) (int, error) {
	if relay == nil {
		return 0, errors.New("history relay is nil")
	}
	now := relay.now().UTC()
	batch, err := relay.repository.ClaimHistoryBatch(ctx, relay.batchSize, now, relay.leaseFor, relay.maxAttempts)
	if err != nil {
		return 0, fmt.Errorf("claim telemetry history batch: %w", err)
	}
	if len(batch.Observations) == 0 {
		return 0, nil
	}
	if batch.LeaseID == "" {
		return 0, errors.New("claimed telemetry history batch has no lease ID")
	}
	if err := relay.sink.InsertObservations(ctx, batch.Sequence, batch.Observations); err != nil {
		retryAt := relay.now().UTC().Add(relay.retryAfter)
		if retryErr := relay.repository.RetryHistoryBatch(ctx, batch.LeaseID, retryAt, historyInsertFailureCode, relay.maxAttempts); retryErr != nil {
			return 0, errors.Join(fmt.Errorf("insert telemetry history: %w", err), fmt.Errorf("retry telemetry history batch: %w", retryErr))
		}
		return 0, fmt.Errorf("insert telemetry history: %w", err)
	}
	if err := relay.repository.MarkHistoryBatchPublished(ctx, batch.LeaseID, relay.now().UTC()); err != nil {
		return 0, fmt.Errorf("mark telemetry history batch published: %w", err)
	}
	return len(batch.Observations), nil
}

// Run drains a backlog (full batches) immediately; otherwise it waits the poll interval
// so a trickle of observations is written as one insert per interval. Every insert is a
// ClickHouse part, multiplied by the rollup views, and tiny frequent parts exhaust
// ClickHouse memory. The shared loop keeps the combined worker and standalone projector
// consistent.
func (relay *HistoryRelay) Run(ctx context.Context, idleInterval time.Duration, report func(int, error)) {
	for ctx.Err() == nil {
		passContext, cancel := context.WithTimeout(ctx, min(15*time.Second, relay.leaseFor/2))
		published, err := relay.RelayOnce(passContext)
		cancel()
		report(published, err)
		if err == nil && published == relay.batchSize {
			continue
		}
		timer := time.NewTimer(idleInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
