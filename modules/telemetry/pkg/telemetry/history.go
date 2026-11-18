package telemetry

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const (
	historyInsertFailureCode      = "CLICKHOUSE_INSERT_FAILED"
	historyPoisonRowFailureCode   = "CLICKHOUSE_POISON_ROW"
	historyMaxAttemptsFailureCode = "CLICKHOUSE_MAX_ATTEMPTS_EXCEEDED"
	historyPayloadInvalidCode     = "HISTORY_PAYLOAD_INVALID"
	historyRowTooLargeCode        = "HISTORY_ROW_TOO_LARGE"
)

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
	MaxAttempts  int
	Observations []HistoryObservation
}

type HistoryRepository interface {
	ClaimHistoryBatch(context.Context, int, time.Time, time.Duration) (HistoryBatch, error)
	MarkHistoryBatchPublished(context.Context, string, time.Time) error
	MarkHistoryEventsPublished(context.Context, string, []string, time.Time) error
	DeadLetterHistoryEvents(context.Context, string, []string, string, time.Time) error
	RetryHistoryBatch(context.Context, string, time.Time, string) error
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

// HistoryRelay retries transient failures in History Sequence order (indefinitely when MaxAttempts == 0,
// or up to MaxAttempts when positive) so duplicates are avoided, and binary-splits permanent failures
// to quarantine poison pills.
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
	if config.MaxAttempts < 0 {
		return nil, errors.New("history relay max attempts must not be negative")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &HistoryRelay{
		repository: config.Repository, sink: config.Sink, batchSize: config.BatchSize,
		leaseFor: config.LeaseFor, retryAfter: config.RetryAfter, maxAttempts: config.MaxAttempts, now: config.Now,
	}, nil
}

func (relay *HistoryRelay) RelayOnce(ctx context.Context) (int, error) {
	if relay == nil {
		return 0, errors.New("history relay is nil")
	}
	now := relay.now().UTC()
	batch, err := relay.repository.ClaimHistoryBatch(ctx, relay.batchSize, now, relay.leaseFor)
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
		if IsPermanentHistoryError(err) {
			return relay.handlePermanentBatchFailure(ctx, batch, err)
		}
		if relay.maxAttempts > 0 && batch.MaxAttempts >= relay.maxAttempts && ctx.Err() == nil {
			settleCtx, cancelSettle := historySettleContext(ctx)
			dlErr := relay.repository.DeadLetterHistoryEvents(settleCtx, batch.LeaseID, historyObservationIDs(batch.Observations), historyMaxAttemptsFailureCode, relay.now().UTC())
			cancelSettle()
			if dlErr != nil {
				return 0, errors.Join(fmt.Errorf("insert telemetry history: %w", err), fmt.Errorf("dead-letter exhausted telemetry history batch: %w", dlErr))
			}
			return 0, fmt.Errorf("dead-letter telemetry history batch after %d attempts: %w", batch.MaxAttempts, err)
		}
		retryAt := relay.now().UTC().Add(relay.retryAfter)
		if ctx.Err() != nil {
			retryAt = relay.now().UTC()
		}
		settleCtx, cancelSettle := historySettleContext(ctx)
		retryErr := relay.repository.RetryHistoryBatch(settleCtx, batch.LeaseID, retryAt, historyInsertFailureCode)
		cancelSettle()
		if retryErr != nil {
			return 0, errors.Join(fmt.Errorf("insert telemetry history: %w", err), fmt.Errorf("retry telemetry history batch: %w", retryErr))
		}
		return 0, fmt.Errorf("insert telemetry history: %w", err)
	}
	settleCtx, cancelSettle := historySettleContext(ctx)
	err = relay.repository.MarkHistoryBatchPublished(settleCtx, batch.LeaseID, relay.now().UTC())
	cancelSettle()
	if err != nil {
		return 0, fmt.Errorf("mark telemetry history batch published: %w", err)
	}
	return len(batch.Observations), nil
}

func historySettleContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
}

func (relay *HistoryRelay) handlePermanentBatchFailure(ctx context.Context, batch HistoryBatch, batchErr error) (int, error) {
	var publishedIDs, deadIDs []string
	var transientErr error
	if len(batch.Observations) == 1 {
		deadIDs = []string{batch.Observations[0].ObservationID}
	} else {
		mid := len(batch.Observations) / 2
		publishedIDs, deadIDs, transientErr = relay.flushWithBinarySplit(ctx, batch.Sequence, batch.Observations[:mid])
		if transientErr == nil {
			rightPublished, rightDead, rightErr := relay.flushWithBinarySplit(ctx, batch.Sequence, batch.Observations[mid:])
			publishedIDs = append(publishedIDs, rightPublished...)
			deadIDs = append(deadIDs, rightDead...)
			transientErr = rightErr
		}
	}

	now := relay.now().UTC()
	var persistErrs []error
	if len(publishedIDs) > 0 {
		settleCtx, cancelSettle := historySettleContext(ctx)
		err := relay.repository.MarkHistoryEventsPublished(settleCtx, batch.LeaseID, publishedIDs, now)
		cancelSettle()
		if err != nil {
			persistErrs = append(persistErrs, fmt.Errorf("mark split telemetry history events published: %w", err))
		}
	}
	if len(deadIDs) > 0 {
		settleCtx, cancelSettle := historySettleContext(ctx)
		err := relay.repository.DeadLetterHistoryEvents(settleCtx, batch.LeaseID, deadIDs, historyPoisonRowFailureCode, now)
		cancelSettle()
		if err != nil {
			persistErrs = append(persistErrs, fmt.Errorf("dead-letter poison telemetry history events: %w", err))
		}
	}
	if transientErr != nil {
		if relay.maxAttempts > 0 && batch.MaxAttempts >= relay.maxAttempts && ctx.Err() == nil {
			remaining := remainingHistoryIDs(batch.Observations, publishedIDs, deadIDs)
			if len(remaining) > 0 {
				settleCtx, cancelSettle := historySettleContext(ctx)
				dlErr := relay.repository.DeadLetterHistoryEvents(settleCtx, batch.LeaseID, remaining, historyMaxAttemptsFailureCode, now)
				cancelSettle()
				if dlErr != nil {
					persistErrs = append(persistErrs, fmt.Errorf("dead-letter remaining telemetry history events: %w", dlErr))
				}
			}
			persistErrs = append(persistErrs, fmt.Errorf("dead-letter telemetry history split remainder after %d attempts: %w", batch.MaxAttempts, transientErr))
		} else {
			retryAt := now.Add(relay.retryAfter)
			if ctx.Err() != nil {
				retryAt = now
			}
			settleCtx, cancelSettle := historySettleContext(ctx)
			retryErr := relay.repository.RetryHistoryBatch(settleCtx, batch.LeaseID, retryAt, historyInsertFailureCode)
			cancelSettle()
			if retryErr != nil {
				persistErrs = append(persistErrs, fmt.Errorf("retry telemetry history split remainder: %w", retryErr))
			}
			persistErrs = append(persistErrs, fmt.Errorf("insert telemetry history during split: %w", transientErr))
		}
	}
	if len(persistErrs) > 0 {
		return len(publishedIDs), errors.Join(append([]error{fmt.Errorf("insert telemetry history: %w", batchErr)}, persistErrs...)...)
	}
	return len(publishedIDs), nil
}

func (relay *HistoryRelay) flushWithBinarySplit(ctx context.Context, sequence uint64, rows []HistoryObservation) (publishedIDs, deadIDs []string, transientErr error) {
	if len(rows) == 0 {
		return nil, nil, nil
	}
	err := relay.sink.InsertObservations(ctx, sequence, rows)
	if err == nil {
		return historyObservationIDs(rows), nil, nil
	}
	if !IsPermanentHistoryError(err) {
		return nil, nil, err
	}
	if len(rows) == 1 {
		return nil, []string{rows[0].ObservationID}, nil
	}
	mid := len(rows) / 2
	leftPublished, leftDead, leftErr := relay.flushWithBinarySplit(ctx, sequence, rows[:mid])
	if leftErr != nil {
		return leftPublished, leftDead, leftErr
	}
	rightPublished, rightDead, rightErr := relay.flushWithBinarySplit(ctx, sequence, rows[mid:])
	return append(leftPublished, rightPublished...), append(leftDead, rightDead...), rightErr
}

func historyObservationIDs(rows []HistoryObservation) []string {
	ids := make([]string, len(rows))
	for index, row := range rows {
		ids[index] = row.ObservationID
	}
	return ids
}

func remainingHistoryIDs(rows []HistoryObservation, publishedIDs, deadIDs []string) []string {
	handled := make(map[string]struct{}, len(publishedIDs)+len(deadIDs))
	for _, id := range publishedIDs {
		handled[id] = struct{}{}
	}
	for _, id := range deadIDs {
		handled[id] = struct{}{}
	}
	var remaining []string
	for _, row := range rows {
		if _, ok := handled[row.ObservationID]; !ok {
			remaining = append(remaining, row.ObservationID)
		}
	}
	return remaining
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
