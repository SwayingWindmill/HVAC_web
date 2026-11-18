package telemetry

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

const (
	historyInsertFailureCode       = "CLICKHOUSE_INSERT_FAILED"
	historyTooManyPartsFailureCode = "CLICKHOUSE_TOO_MANY_PARTS"
	historyPoisonRowFailureCode    = "CLICKHOUSE_POISON_ROW"
	historyMaxAttemptsFailureCode  = "CLICKHOUSE_MAX_ATTEMPTS_EXCEEDED"
	historyPayloadInvalidCode      = "HISTORY_PAYLOAD_INVALID"
	historyRowTooLargeCode         = "HISTORY_ROW_TOO_LARGE"
	minClickHouseTooManyPartsDelay = 2 * time.Second
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

// PipelinedHistoryRepository optionally lets HistoryRelay prefetch and release ordered
// batches across a bounded in-flight pipeline.
type PipelinedHistoryRepository interface {
	ClaimPipelinedHistoryBatch(ctx context.Context, maxEvents int, now time.Time, leaseFor time.Duration, activeLeaseIDs []string, maxInFlight int) (HistoryBatch, error)
	ReleaseHistoryBatchLease(ctx context.Context, leaseID string, now time.Time) error
}

type HistorySink interface {
	InsertObservations(context.Context, uint64, []HistoryObservation) error
}

// PreparedHistorySink optionally splits deterministic encoding/compression from ClickHouse HTTP upload.
type PreparedHistorySink interface {
	PrepareObservations(sequence uint64, observations []HistoryObservation) (compressed []byte, token string, err error)
	InsertPrepared(ctx context.Context, compressed []byte, token string) error
}

// HistoryOutboxDepthProvider optionally exposes current outbox row counts by delivery state.
type HistoryOutboxDepthProvider interface {
	HistoryOutboxDepths(ctx context.Context) (pending, publishing, deadLetter int, err error)
}

type HistoryRelayConfig struct {
	Repository    HistoryRepository
	Sink          HistorySink
	BatchSize     int
	LeaseFor      time.Duration
	RetryAfter    time.Duration
	MaxAttempts   int
	PipelineDepth int
	Metrics       *PipelineMetrics
	Now           func() time.Time
}

// HistoryRelay retries transient failures in History Sequence order (indefinitely when MaxAttempts == 0,
// or up to MaxAttempts when positive) so duplicates are avoided, and binary-splits permanent failures
// to quarantine poison pills.
type HistoryRelay struct {
	repository    HistoryRepository
	sink          HistorySink
	batchSize     int
	leaseFor      time.Duration
	retryAfter    time.Duration
	maxAttempts   int
	pipelineDepth int
	metrics       *PipelineMetrics
	now           func() time.Time
}

func NewHistoryRelay(config HistoryRelayConfig) (*HistoryRelay, error) {
	if config.Repository == nil || config.Sink == nil {
		return nil, errors.New("history relay repository and sink are required")
	}
	if config.BatchSize < 1 || config.BatchSize > maxHistoryBatchRows {
		return nil, fmt.Errorf("history relay batch size must be between 1 and %d", maxHistoryBatchRows)
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
	pipelineDepth := config.PipelineDepth
	if pipelineDepth <= 0 {
		pipelineDepth = 2
	} else if pipelineDepth > 4 {
		pipelineDepth = 4
	}
	return &HistoryRelay{
		repository: config.Repository, sink: config.Sink, batchSize: config.BatchSize,
		leaseFor: config.LeaseFor, retryAfter: config.RetryAfter, maxAttempts: config.MaxAttempts,
		pipelineDepth: pipelineDepth, metrics: config.Metrics, now: config.Now,
	}, nil
}

type preparedHistoryBatch struct {
	batch      HistoryBatch
	compressed []byte
	token      string
	prepared   bool
	claimErr   error
	prepErr    error
}

func (relay *HistoryRelay) prepareBatch(batch HistoryBatch) preparedHistoryBatch {
	item := preparedHistoryBatch{batch: batch}
	if preparedSink, ok := relay.sink.(PreparedHistorySink); ok {
		compressed, token, prepErr := preparedSink.PrepareObservations(batch.Sequence, batch.Observations)
		if prepErr != nil {
			item.prepErr = prepErr
		} else {
			item.compressed = compressed
			item.token = token
			item.prepared = true
		}
	}
	return item
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
		relay.refreshOutboxDepths(ctx)
		return 0, nil
	}
	published, execErr := relay.executePreparedHistoryBatch(ctx, relay.prepareBatch(batch))
	relay.refreshOutboxDepths(ctx)
	return published, execErr
}

func (relay *HistoryRelay) refreshOutboxDepths(ctx context.Context) {
	if relay == nil || relay.metrics == nil {
		return
	}
	provider, ok := relay.repository.(HistoryOutboxDepthProvider)
	if !ok {
		return
	}
	depthCtx, cancel := historySettleContext(ctx)
	defer cancel()
	pending, publishing, deadLetter, err := provider.HistoryOutboxDepths(depthCtx)
	if err == nil {
		relay.metrics.SetHistoryOutboxDepths(pending, publishing, deadLetter)
	}
}

func (relay *HistoryRelay) executePreparedHistoryBatch(ctx context.Context, item preparedHistoryBatch) (int, error) {
	batch := item.batch
	if batch.LeaseID == "" {
		return 0, errors.New("claimed telemetry history batch has no lease ID")
	}
	start := time.Now()
	err := item.prepErr
	if err == nil {
		if item.prepared {
			preparedSink := relay.sink.(PreparedHistorySink)
			err = preparedSink.InsertPrepared(ctx, item.compressed, item.token)
		} else {
			err = relay.sink.InsertObservations(ctx, batch.Sequence, batch.Observations)
		}
	}
	if err != nil {
		if IsPermanentHistoryError(err) {
			return relay.handlePermanentBatchFailure(ctx, batch, err, start)
		}
		relay.metrics.ObserveHistoryFlushDuration(time.Since(start))
		if relay.maxAttempts > 0 && batch.MaxAttempts >= relay.maxAttempts && ctx.Err() == nil {
			settleCtx, cancelSettle := historySettleContext(ctx)
			dlErr := relay.repository.DeadLetterHistoryEvents(settleCtx, batch.LeaseID, historyObservationIDs(batch.Observations), historyMaxAttemptsFailureCode, relay.now().UTC())
			cancelSettle()
			if dlErr != nil {
				return 0, errors.Join(fmt.Errorf("insert telemetry history: %w", err), fmt.Errorf("dead-letter exhausted telemetry history batch: %w", dlErr))
			}
			return 0, fmt.Errorf("dead-letter telemetry history batch after %d attempts: %w", batch.MaxAttempts, err)
		}
		retryAt, errorCode := relay.retrySchedule(ctx, relay.now().UTC(), err)
		settleCtx, cancelSettle := historySettleContext(ctx)
		retryErr := relay.repository.RetryHistoryBatch(settleCtx, batch.LeaseID, retryAt, errorCode)
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
	relay.metrics.ObserveHistoryFlushDuration(time.Since(start))
	relay.metrics.AddHistoryRowsProjected(len(batch.Observations))
	return len(batch.Observations), nil
}

func (relay *HistoryRelay) retrySchedule(ctx context.Context, now time.Time, err error) (time.Time, string) {
	if ctx.Err() != nil {
		return now, historyInsertFailureCode
	}
	delay := relay.retryAfter
	code := historyInsertFailureCode
	if IsClickHouseTooManyPartsError(err) {
		code = historyTooManyPartsFailureCode
		if delay < minClickHouseTooManyPartsDelay {
			delay = minClickHouseTooManyPartsDelay
		}
	}
	return now.Add(delay), code
}

func historySettleContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
}

func (relay *HistoryRelay) handlePermanentBatchFailure(ctx context.Context, batch HistoryBatch, batchErr error, start time.Time) (int, error) {
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
			retryAt, errorCode := relay.retrySchedule(ctx, now, transientErr)
			settleCtx, cancelSettle := historySettleContext(ctx)
			retryErr := relay.repository.RetryHistoryBatch(settleCtx, batch.LeaseID, retryAt, errorCode)
			cancelSettle()
			if retryErr != nil {
				persistErrs = append(persistErrs, fmt.Errorf("retry telemetry history split remainder: %w", retryErr))
			}
			persistErrs = append(persistErrs, fmt.Errorf("insert telemetry history during split: %w", transientErr))
		}
	}
	relay.metrics.ObserveHistoryFlushDuration(time.Since(start))
	if len(publishedIDs) > 0 {
		relay.metrics.AddHistoryRowsProjected(len(publishedIDs))
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

// RelayPending drains all currently available batches in strict batch_sequence order,
// overlapping Stage 1 claim/prepare of subsequent batches with Stage 2 ClickHouse/PostgreSQL commits
// when PipelineDepth > 1 and the repository implements PipelinedHistoryRepository.
func (relay *HistoryRelay) RelayPending(ctx context.Context) (int, error) {
	if relay == nil {
		return 0, errors.New("history relay is nil")
	}
	if relay.pipelineDepth > 1 {
		if pipelinedRepo, ok := relay.repository.(PipelinedHistoryRepository); ok {
			total, _, err := relay.runPipelinedWave(ctx, pipelinedRepo, false, nil)
			relay.refreshOutboxDepths(ctx)
			return total, err
		}
	}
	total := 0
	for ctx.Err() == nil {
		published, err := relay.RelayOnce(ctx)
		total += published
		if err != nil {
			return total, err
		}
		if published == 0 {
			return total, nil
		}
	}
	return total, ctx.Err()
}

// Run drains a backlog (full batches) immediately; otherwise it waits the poll interval
// so a trickle of observations is written as one insert per interval. Every insert is a
// ClickHouse part, multiplied by the rollup views, and tiny frequent parts exhaust
// ClickHouse memory. The shared loop keeps the combined worker and standalone projector
// consistent.
func (relay *HistoryRelay) Run(ctx context.Context, idleInterval time.Duration, report func(int, error)) {
	pipelinedRepo, canPipeline := relay.repository.(PipelinedHistoryRepository)
	usePipeline := relay.pipelineDepth > 1 && canPipeline
	for ctx.Err() == nil {
		if usePipeline {
			_, cont, _ := relay.runPipelinedWave(ctx, pipelinedRepo, true, report)
			relay.refreshOutboxDepths(ctx)
			if cont && ctx.Err() == nil {
				continue
			}
		} else {
			passContext, cancel := context.WithTimeout(ctx, min(15*time.Second, relay.leaseFor/2))
			published, err := relay.RelayOnce(passContext)
			cancel()
			if report != nil {
				report(published, err)
			}
			if err == nil && published == relay.batchSize {
				continue
			}
		}
		if ctx.Err() != nil {
			return
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

func (relay *HistoryRelay) runPipelinedWave(
	ctx context.Context,
	pipelinedRepo PipelinedHistoryRepository,
	fullBatchOnly bool,
	report func(int, error),
) (int, bool, error) {
	depth := relay.pipelineDepth
	stepTimeout := min(15*time.Second, max(200*time.Millisecond, relay.leaseFor/time.Duration(depth+1)))

	stage1Ctx, cancelStage1 := context.WithCancel(ctx)
	defer cancelStage1()

	var leaseMu sync.Mutex
	activeLeases := make(map[string]struct{}, depth)
	snapshotActiveLeases := func() []string {
		leaseMu.Lock()
		defer leaseMu.Unlock()
		out := make([]string, 0, len(activeLeases))
		for id := range activeLeases {
			out = append(out, id)
		}
		return out
	}
	addActiveLease := func(leaseID string) {
		if leaseID == "" {
			return
		}
		leaseMu.Lock()
		activeLeases[leaseID] = struct{}{}
		leaseMu.Unlock()
	}
	removeActiveLease := func(leaseID string) {
		if leaseID == "" {
			return
		}
		leaseMu.Lock()
		delete(activeLeases, leaseID)
		leaseMu.Unlock()
	}

	slots := make(chan struct{}, depth)
	readyCh := make(chan preparedHistoryBatch, depth+1)
	stage1Done := make(chan struct{})

	go func() {
		defer close(stage1Done)
		defer close(readyCh)
		for {
			select {
			case <-stage1Ctx.Done():
				return
			case slots <- struct{}{}:
			}
			if stage1Ctx.Err() != nil {
				<-slots
				return
			}
			claimCtx, cancelClaim := context.WithTimeout(stage1Ctx, stepTimeout)
			batch, claimErr := pipelinedRepo.ClaimPipelinedHistoryBatch(
				claimCtx,
				relay.batchSize,
				relay.now().UTC(),
				relay.leaseFor,
				snapshotActiveLeases(),
				depth,
			)
			cancelClaim()
			if claimErr != nil {
				<-slots
				readyCh <- preparedHistoryBatch{claimErr: fmt.Errorf("claim telemetry history batch: %w", claimErr)}
				return
			}
			if len(batch.Observations) == 0 {
				<-slots
				readyCh <- preparedHistoryBatch{}
				return
			}
			addActiveLease(batch.LeaseID)
			item := relay.prepareBatch(batch)
			readyCh <- item
			if fullBatchOnly && len(batch.Observations) < relay.batchSize {
				return
			}
		}
	}()

	releaseLeftovers := func(extra []preparedHistoryBatch) error {
		cancelStage1()
		var releaseErrs []error
		releaseOne := func(item preparedHistoryBatch) {
			if len(item.batch.Observations) == 0 {
				return
			}
			defer removeActiveLease(item.batch.LeaseID)
			if item.batch.LeaseID == "" {
				return
			}
			settleCtx, cancelSettle := historySettleContext(ctx)
			relErr := pipelinedRepo.ReleaseHistoryBatchLease(settleCtx, item.batch.LeaseID, relay.now().UTC())
			cancelSettle()
			if relErr != nil {
				releaseErrs = append(releaseErrs, fmt.Errorf("release unused telemetry history batch lease: %w", relErr))
			}
		}
		for _, item := range extra {
			releaseOne(item)
		}
		for leftover := range readyCh {
			releaseOne(leftover)
		}
		<-stage1Done
		return errors.Join(releaseErrs...)
	}

	totalPublished := 0
	batchesProcessed := 0
	lastFull := false
	for item := range readyCh {
		if item.claimErr != nil {
			if report != nil {
				report(0, item.claimErr)
			}
			relErr := releaseLeftovers(nil)
			return totalPublished, false, errors.Join(item.claimErr, relErr)
		}
		if len(item.batch.Observations) == 0 {
			if (batchesProcessed == 0 || lastFull) && report != nil {
				report(0, nil)
			}
			relErr := releaseLeftovers(nil)
			return totalPublished, false, relErr
		}
		if ctx.Err() != nil {
			relErr := releaseLeftovers([]preparedHistoryBatch{item})
			return totalPublished, false, errors.Join(ctx.Err(), relErr)
		}

		stage2Ctx, cancelStage2 := context.WithTimeout(ctx, stepTimeout)
		published, execErr := relay.executePreparedHistoryBatch(stage2Ctx, item)
		cancelStage2()
		removeActiveLease(item.batch.LeaseID)
		batchesProcessed++
		totalPublished += published
		if report != nil {
			report(published, execErr)
		}
		if execErr != nil {
			relErr := releaseLeftovers(nil)
			return totalPublished, false, errors.Join(execErr, relErr)
		}
		<-slots
		lastFull = published == relay.batchSize
	}
	<-stage1Done
	return totalPublished, lastFull, nil
}
