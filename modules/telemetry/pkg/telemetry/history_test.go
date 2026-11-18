package telemetry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
)

func TestHistoryRelayPublishesClaimedObservations(t *testing.T) {
	now := time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
	repository := &historyRepositoryStub{batch: HistoryBatch{
		LeaseID:      "018f2e00-9000-7000-8000-000000000001",
		Observations: []HistoryObservation{{ObservationID: "018f2e00-9000-7000-8000-000000000002"}},
	}}
	sink := &historySinkStub{}
	relay, err := NewHistoryRelay(HistoryRelayConfig{
		Repository: repository,
		Sink:       sink,
		BatchSize:  64,
		LeaseFor:   30 * time.Second,
		RetryAfter: 2 * time.Second,
		Now:        func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	published, err := relay.RelayOnce(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if published != 1 {
		t.Fatalf("published=%d", published)
	}
	if len(sink.inserted) != 1 || sink.inserted[0].ObservationID != repository.batch.Observations[0].ObservationID {
		t.Fatalf("inserted=%#v", sink.inserted)
	}
	if repository.publishedLeaseID != repository.batch.LeaseID || !repository.publishedAt.Equal(now) {
		t.Fatalf("published lease=%q at=%s", repository.publishedLeaseID, repository.publishedAt)
	}
	if repository.failedLeaseID != "" {
		t.Fatalf("unexpected failed lease=%s", repository.failedLeaseID)
	}
}

func TestHistoryRelayRetriesClaimWhenClickHouseInsertFails(t *testing.T) {
	now := time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
	repository := &historyRepositoryStub{batch: HistoryBatch{
		LeaseID:      "018f2e00-9000-7000-8000-000000000003",
		Observations: []HistoryObservation{{ObservationID: "018f2e00-9000-7000-8000-000000000004"}},
	}}
	sink := &historySinkStub{err: errors.New("clickhouse unavailable")}
	relay, err := NewHistoryRelay(HistoryRelayConfig{
		Repository: repository,
		Sink:       sink,
		BatchSize:  32,
		LeaseFor:   15 * time.Second,
		RetryAfter: 5 * time.Second,
		Now:        func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	published, err := relay.RelayOnce(t.Context())
	if err == nil || published != 0 {
		t.Fatalf("published=%d err=%v", published, err)
	}
	if repository.failedLeaseID != repository.batch.LeaseID {
		t.Fatalf("failed lease=%q", repository.failedLeaseID)
	}
	if !repository.retryAt.Equal(now.Add(5*time.Second)) || repository.errorCode != "CLICKHOUSE_INSERT_FAILED" {
		t.Fatalf("retry=%s code=%s", repository.retryAt, repository.errorCode)
	}
	if repository.publishedLeaseID != "" {
		t.Fatalf("unexpected published lease=%s", repository.publishedLeaseID)
	}
}

type historyRepositoryStub struct {
	batch            HistoryBatch
	claimErr         error
	publishedLeaseID string
	publishedAt      time.Time
	publishedEvents  []string
	deadEvents       []string
	deadLetterCode   string
	deadLetteredAt   time.Time
	failedLeaseID    string
	retryAt          time.Time
	errorCode        string
}

func TestHistoryRelayRunDrainsWithoutPollingDelay(t *testing.T) {
	repo := &historyRepositoryStub{batch: HistoryBatch{LeaseID: "lease", Observations: []HistoryObservation{{ObservationID: "observation"}}}}
	relay, err := NewHistoryRelay(HistoryRelayConfig{Repository: repo, Sink: &historySinkStub{}, BatchSize: 1, LeaseFor: 30 * time.Second, RetryAfter: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	passes := 0
	relay.Run(ctx, time.Hour, func(published int, err error) {
		if err != nil || published != 1 {
			t.Fatalf("pass=%d err=%v", published, err)
		}
		passes++
		if passes == 2 {
			cancel()
		}
	})
	if passes != 2 {
		t.Fatalf("busy channel waited for poll interval; passes=%d", passes)
	}
}

// A trickle of observations is written once per poll interval, not as soon as each
// arrives: every insert is a ClickHouse part, multiplied by the rollup views.
func TestHistoryRelayRunWaitsAfterAPartialBatch(t *testing.T) {
	repo := &historyRepositoryStub{batch: HistoryBatch{LeaseID: "lease", Observations: []HistoryObservation{{ObservationID: "observation"}}}}
	relay, err := NewHistoryRelay(HistoryRelayConfig{Repository: repo, Sink: &historySinkStub{}, BatchSize: 4, LeaseFor: 30 * time.Second, RetryAfter: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	passes := 0
	relay.Run(ctx, time.Hour, func(published int, err error) {
		if err != nil || published != 1 {
			t.Fatalf("pass=%d err=%v", published, err)
		}
		passes++
	})
	if passes != 1 {
		t.Fatalf("a partial batch was followed by another insert before the poll interval; passes=%d", passes)
	}
}

func (repository *historyRepositoryStub) ClaimHistoryBatch(context.Context, int, time.Time, time.Duration) (HistoryBatch, error) {
	return repository.batch, repository.claimErr
}

func (repository *historyRepositoryStub) MarkHistoryBatchPublished(_ context.Context, leaseID string, publishedAt time.Time) error {
	repository.publishedLeaseID = leaseID
	repository.publishedAt = publishedAt
	return nil
}

func (repository *historyRepositoryStub) MarkHistoryEventsPublished(_ context.Context, leaseID string, eventIDs []string, publishedAt time.Time) error {
	repository.publishedLeaseID = leaseID
	repository.publishedAt = publishedAt
	repository.publishedEvents = append(repository.publishedEvents, eventIDs...)
	return nil
}

func (repository *historyRepositoryStub) DeadLetterHistoryEvents(_ context.Context, leaseID string, eventIDs []string, errorCode string, deadLetteredAt time.Time) error {
	repository.failedLeaseID = leaseID
	repository.deadEvents = append(repository.deadEvents, eventIDs...)
	repository.deadLetterCode = errorCode
	repository.deadLetteredAt = deadLetteredAt
	return nil
}

func (repository *historyRepositoryStub) RetryHistoryBatch(_ context.Context, leaseID string, retryAt time.Time, errorCode string) error {
	repository.failedLeaseID = leaseID
	repository.retryAt = retryAt
	repository.errorCode = errorCode
	return nil
}

type historySinkStub struct {
	inserted []HistoryObservation
	err      error
}

func (sink *historySinkStub) InsertObservations(_ context.Context, _ uint64, observations []HistoryObservation) error {
	sink.inserted = append([]HistoryObservation(nil), observations...)
	return sink.err
}

type selectiveHistorySink struct {
	fn func([]HistoryObservation) error
}

func (sink *selectiveHistorySink) InsertObservations(_ context.Context, _ uint64, observations []HistoryObservation) error {
	return sink.fn(observations)
}

func TestHistoryRelayBinarySplitsPermanentBatchAndDeadLettersPoisonPill(t *testing.T) {
	now := time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
	obs := []HistoryObservation{
		{ObservationID: "018f2e00-9000-7000-8000-000000000001"},
		{ObservationID: "018f2e00-9000-7000-8000-000000000002"},
		{ObservationID: "018f2e00-9000-7000-8000-000000000003"},
		{ObservationID: "018f2e00-9000-7000-8000-000000000004"},
	}
	poisonID := obs[1].ObservationID
	repository := &historyRepositoryStub{batch: HistoryBatch{
		LeaseID:      "018f2e00-9000-7000-8000-000000000099",
		Sequence:     42,
		MaxAttempts:  1,
		Observations: obs,
	}}
	sink := &selectiveHistorySink{fn: func(rows []HistoryObservation) error {
		for _, row := range rows {
			if row.ObservationID == poisonID {
				return fmt.Errorf("%w: poison row %s", ErrHistoryBatchPermanent, poisonID)
			}
		}
		return nil
	}}
	relay, err := NewHistoryRelay(HistoryRelayConfig{
		Repository: repository, Sink: sink, BatchSize: 16, LeaseFor: 30 * time.Second, RetryAfter: 5 * time.Second,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	published, err := relay.RelayOnce(t.Context())
	if err != nil {
		t.Fatalf("expected poison pill split to recover cleanly, got %v", err)
	}
	if published != 3 {
		t.Fatalf("published=%d, want 3", published)
	}
	wantPublished := []string{obs[0].ObservationID, obs[2].ObservationID, obs[3].ObservationID}
	if !slices.Equal(repository.publishedEvents, wantPublished) {
		t.Fatalf("publishedEvents=%v, want %v", repository.publishedEvents, wantPublished)
	}
	if !slices.Equal(repository.deadEvents, []string{poisonID}) || repository.deadLetterCode != historyPoisonRowFailureCode || !repository.deadLetteredAt.Equal(now) {
		t.Fatalf("deadEvents=%v code=%q at=%s", repository.deadEvents, repository.deadLetterCode, repository.deadLetteredAt)
	}
}

func TestHistoryRelayDeadLettersSinglePoisonRowAndTransientRemainder(t *testing.T) {
	now := time.Date(2026, 7, 29, 8, 5, 0, 0, time.UTC)
	singleRepo := &historyRepositoryStub{batch: HistoryBatch{
		LeaseID:      "018f2e00-9000-7000-8000-000000000101",
		Observations: []HistoryObservation{{ObservationID: "018f2e00-9000-7000-8000-000000000102"}},
	}}
	singleRelay, err := NewHistoryRelay(HistoryRelayConfig{
		Repository: singleRepo,
		Sink:       &historySinkStub{err: fmt.Errorf("%w: bad json", ErrHistoryBatchPermanent)},
		BatchSize:  16, LeaseFor: 30 * time.Second, RetryAfter: 5 * time.Second,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	published, err := singleRelay.RelayOnce(t.Context())
	if err != nil || published != 0 {
		t.Fatalf("single poison row published=%d err=%v", published, err)
	}
	if !slices.Equal(singleRepo.deadEvents, []string{"018f2e00-9000-7000-8000-000000000102"}) {
		t.Fatalf("single poison deadEvents=%v", singleRepo.deadEvents)
	}

	// Partial transient failure during split must publish confirmed left half and retry remaining right half.
	obs := []HistoryObservation{
		{ObservationID: "018f2e00-9000-7000-8000-000000000201"},
		{ObservationID: "018f2e00-9000-7000-8000-000000000202"},
		{ObservationID: "018f2e00-9000-7000-8000-000000000203"},
		{ObservationID: "018f2e00-9000-7000-8000-000000000204"},
	}
	splitRepo := &historyRepositoryStub{batch: HistoryBatch{
		LeaseID: "018f2e00-9000-7000-8000-000000000299", MaxAttempts: 2, Observations: obs,
	}}
	call := 0
	splitRelay, err := NewHistoryRelay(HistoryRelayConfig{
		Repository: splitRepo,
		Sink: &selectiveHistorySink{fn: func(rows []HistoryObservation) error {
			call++
			switch call {
			case 1:
				return fmt.Errorf("%w: initial poison", ErrHistoryBatchPermanent)
			case 2:
				return nil
			default:
				return errors.New("503 ClickHouse restarted")
			}
		}},
		BatchSize: 16, LeaseFor: 30 * time.Second, RetryAfter: 5 * time.Second,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	published, err = splitRelay.RelayOnce(t.Context())
	if err == nil || published != 2 {
		t.Fatalf("partial transient split published=%d err=%v", published, err)
	}
	if !slices.Equal(splitRepo.publishedEvents, []string{obs[0].ObservationID, obs[1].ObservationID}) {
		t.Fatalf("left half not persisted: %v", splitRepo.publishedEvents)
	}
	if len(splitRepo.deadEvents) != 0 || splitRepo.errorCode != historyInsertFailureCode {
		t.Fatalf("transient right half mishandled: dead=%v code=%q", splitRepo.deadEvents, splitRepo.errorCode)
	}
}

func TestHistoryRelayDeadLettersExhaustedBatchAtMaxAttempts(t *testing.T) {
	now := time.Date(2026, 7, 29, 8, 10, 0, 0, time.UTC)
	obs := []HistoryObservation{
		{ObservationID: "018f2e00-9000-7000-8000-000000000301"},
		{ObservationID: "018f2e00-9000-7000-8000-000000000302"},
	}
	repo := &historyRepositoryStub{batch: HistoryBatch{
		LeaseID:      "018f2e00-9000-7000-8000-000000000399",
		MaxAttempts:  16,
		Observations: obs,
	}}
	relay, err := NewHistoryRelay(HistoryRelayConfig{
		Repository: repo,
		Sink:       &historySinkStub{err: errors.New("ClickHouse history insert returned 500")},
		BatchSize:  16, LeaseFor: 30 * time.Second, RetryAfter: 5 * time.Second, MaxAttempts: 16,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	published, err := relay.RelayOnce(t.Context())
	if err == nil || published != 0 {
		t.Fatalf("published=%d err=%v", published, err)
	}
	if !slices.Equal(repo.deadEvents, []string{obs[0].ObservationID, obs[1].ObservationID}) || repo.deadLetterCode != historyMaxAttemptsFailureCode {
		t.Fatalf("exhausted batch not dead-lettered cleanly: dead=%v code=%q", repo.deadEvents, repo.deadLetterCode)
	}
}

func TestValidateClaimedHistoryRow(t *testing.T) {
	validObs := HistoryObservation{
		ObservationID:   "018f2e00-9000-7000-8000-000000000401",
		SourceID:        "source-a",
		SourceEventID:   "018f2e00-9000-7000-8000-000000000402",
		SourcePartition: "p0",
		SourcePath:      "MQTT",
		TelemetryKey:    "zone.temperature",
		SampledAt:       time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC),
		ReceivedAt:      time.Date(2026, 7, 29, 8, 0, 1, 0, time.UTC),
		Quality:         "GOOD",
		QualityReasons:  []string{},
		PayloadSHA256:   strings.Repeat("a", 64),
	}
	encoded, _, err := encodeHistoryObservation(validObs)
	if err != nil {
		t.Fatal(err)
	}

	got, size, code := validateClaimedHistoryRow(validObs.ObservationID, encoded)
	if code != "" || size != len(encoded)+1 || got.ObservationID != validObs.ObservationID {
		t.Fatalf("valid row rejected: code=%q size=%d", code, size)
	}

	negZeroPayload := strings.Replace(string(encoded), `"source_offset":0`, `"source_offset":0`, 1)
	negZeroObs := validObs
	zero := 0.0
	negZeroObs.ValueNumber = &zero
	negZeroEncoded, _, err := encodeHistoryObservation(negZeroObs)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, code := validateClaimedHistoryRow(negZeroObs.ObservationID, []byte(negZeroPayload[:0]+strings.Replace(string(negZeroEncoded), `"value_number":0`, `"value_number":-0.0`, 1))); code != "" {
		t.Fatalf("IEEE-754 -0.0 normalized JSONB row failed validation: code=%q", code)
	}

	if _, _, code := validateClaimedHistoryRow(validObs.ObservationID, []byte(`{invalid`)); code != historyPayloadInvalidCode {
		t.Fatalf("corrupt JSON code=%q, want %q", code, historyPayloadInvalidCode)
	}
	if _, _, code := validateClaimedHistoryRow("018f2e00-9000-7000-8000-000000000499", encoded); code != historyPayloadInvalidCode {
		t.Fatalf("mismatched observation ID code=%q, want %q", code, historyPayloadInvalidCode)
	}

	invalidScopeObs := validObs
	invalidScopeObs.AcceptanceStatus = string(ObservationAccepted)
	invalidScopePayload, _, err := encodeHistoryObservation(invalidScopeObs)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, code := validateClaimedHistoryRow(invalidScopeObs.ObservationID, invalidScopePayload); code != historyPayloadInvalidCode {
		t.Fatalf("invalid accepted scope code=%q, want %q", code, historyPayloadInvalidCode)
	}

	oversized := validObs
	oversized.ValueString = stringPointer(strings.Repeat("z", maxHistoryBatchBytes))
	oversizedPayload, _, err := encodeHistoryObservation(oversized)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, code := validateClaimedHistoryRow(oversized.ObservationID, oversizedPayload); code != historyRowTooLargeCode {
		t.Fatalf("oversized row code=%q, want %q", code, historyRowTooLargeCode)
	}
}

func TestHistoryRelaySettlesLeaseWithUncancelledContextWhenCancelledDuringInsert(t *testing.T) {
	now := time.Date(2026, 7, 29, 8, 15, 0, 0, time.UTC)
	obs := []HistoryObservation{{ObservationID: "018f2e00-9000-7000-8000-000000000501"}}
	ctx, cancel := context.WithCancel(t.Context())
	repo := &contextCheckingHistoryRepo{batch: HistoryBatch{
		LeaseID: "018f2e00-9000-7000-8000-000000000599", MaxAttempts: 1, Observations: obs,
	}}
	sink := &selectiveHistorySink{fn: func(_ []HistoryObservation) error {
		cancel()
		return context.Canceled
	}}
	relay, err := NewHistoryRelay(HistoryRelayConfig{
		Repository: repo, Sink: sink, BatchSize: 16, LeaseFor: 30 * time.Second, RetryAfter: 5 * time.Second, MaxAttempts: 1,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := relay.RelayOnce(ctx); err == nil {
		t.Fatal("expected RelayOnce error on cancelled sink")
	}
	if !repo.retrySawActiveCtx || !repo.retryAt.Equal(now) {
		t.Fatalf("expected immediate retry with active settleCtx at %s, got activeCtx=%t retryAt=%s", now, repo.retrySawActiveCtx, repo.retryAt)
	}
	if len(repo.deadEvents) != 0 {
		t.Fatalf("cancelled shutdown insert must not dead-letter batch, dead=%v", repo.deadEvents)
	}
}

type contextCheckingHistoryRepo struct {
	historyRepositoryStub
	retrySawActiveCtx bool
}

func (r *contextCheckingHistoryRepo) RetryHistoryBatch(ctx context.Context, leaseID string, retryAt time.Time, errorCode string) error {
	r.retrySawActiveCtx = ctx.Err() == nil
	return r.historyRepositoryStub.RetryHistoryBatch(ctx, leaseID, retryAt, errorCode)
}

func TestHistoryRelayConfigPipelineDepthDefaultsAndClamping(t *testing.T) {
	base := HistoryRelayConfig{
		Repository: &historyRepositoryStub{},
		Sink:       &historySinkStub{},
		BatchSize:  16,
		LeaseFor:   30 * time.Second,
		RetryAfter: time.Second,
	}
	cases := []struct {
		in   int
		want int
	}{
		{in: -3, want: 2},
		{in: 0, want: 2},
		{in: 1, want: 1},
		{in: 2, want: 2},
		{in: 3, want: 3},
		{in: 4, want: 4},
		{in: 9, want: 4},
	}
	for _, tc := range cases {
		cfg := base
		cfg.PipelineDepth = tc.in
		relay, err := NewHistoryRelay(cfg)
		if err != nil {
			t.Fatalf("PipelineDepth=%d err=%v", tc.in, err)
		}
		if relay.pipelineDepth != tc.want {
			t.Fatalf("PipelineDepth=%d got=%d, want %d", tc.in, relay.pipelineDepth, tc.want)
		}
	}
}

type fakePipelinedBatch struct {
	batch       HistoryBatch
	state       string // PENDING, IN_FLIGHT, PUBLISHED, DEAD
	availableAt time.Time
	attempts    int
	errorCode   string
}

type fakePipelinedHistoryRepo struct {
	mu                sync.Mutex
	batches           []*fakePipelinedBatch
	nextLeaseSeq      int
	activeInFlight    int
	maxObservedFlight int
	inFlightLimitHit  bool
	releasedBatchIDs  []string
	releaseActiveCtx  []bool
	retriedLeaseIDs   []string
	publishedSeqs     []uint64
	markPublishErr    map[uint64]error
	claimHook         func(batch HistoryBatch)
}

func newFakePipelinedHistoryRepo(batches []HistoryBatch) *fakePipelinedHistoryRepo {
	items := make([]*fakePipelinedBatch, len(batches))
	for i, b := range batches {
		items[i] = &fakePipelinedBatch{
			batch: b,
			state: "PENDING",
		}
	}
	return &fakePipelinedHistoryRepo{
		batches:        items,
		markPublishErr: make(map[uint64]error),
	}
}

func (r *fakePipelinedHistoryRepo) ClaimHistoryBatch(ctx context.Context, maxEvents int, now time.Time, leaseFor time.Duration) (HistoryBatch, error) {
	return r.ClaimPipelinedHistoryBatch(ctx, maxEvents, now, leaseFor, 1)
}

func (r *fakePipelinedHistoryRepo) ClaimPipelinedHistoryBatch(_ context.Context, _ int, now time.Time, _ time.Duration, maxInFlight int) (HistoryBatch, error) {
	r.mu.Lock()
	if r.activeInFlight >= maxInFlight {
		r.inFlightLimitHit = true
		r.mu.Unlock()
		return HistoryBatch{}, nil
	}
	var claimed HistoryBatch
	for _, item := range r.batches {
		if item.state == "IN_FLIGHT" || item.state == "PUBLISHED" || item.state == "DEAD" {
			continue
		}
		if item.availableAt.After(now) {
			r.mu.Unlock()
			return HistoryBatch{}, nil
		}
		r.nextLeaseSeq++
		item.state = "IN_FLIGHT"
		item.attempts++
		item.batch.LeaseID = fmt.Sprintf("018f2e00-9000-7000-8000-%012d", r.nextLeaseSeq)
		item.batch.MaxAttempts = item.attempts
		r.activeInFlight++
		if r.activeInFlight > r.maxObservedFlight {
			r.maxObservedFlight = r.activeInFlight
		}
		claimed = item.batch
		break
	}
	hook := r.claimHook
	r.mu.Unlock()
	if len(claimed.Observations) > 0 && hook != nil {
		hook(claimed)
	}
	return claimed, nil
}

func (r *fakePipelinedHistoryRepo) ReleaseHistoryBatchLease(ctx context.Context, batchID string, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.releaseActiveCtx = append(r.releaseActiveCtx, ctx.Err() == nil)
	for _, item := range r.batches {
		if (item.batch.BatchID == batchID || item.batch.LeaseID == batchID) && item.state == "IN_FLIGHT" {
			item.state = "PENDING"
			item.availableAt = now
			item.errorCode = ""
			if item.attempts > 0 {
				item.attempts--
			}
			item.batch.LeaseID = ""
			r.activeInFlight--
			r.releasedBatchIDs = append(r.releasedBatchIDs, batchID)
			return nil
		}
	}
	return errors.New("telemetry history lease was not found")
}

func (r *fakePipelinedHistoryRepo) MarkHistoryBatchPublished(_ context.Context, leaseID string, _ time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, item := range r.batches {
		if item.batch.LeaseID == leaseID && item.state == "IN_FLIGHT" {
			if err := r.markPublishErr[item.batch.Sequence]; err != nil {
				delete(r.markPublishErr, item.batch.Sequence)
				return err
			}
			item.state = "PUBLISHED"
			item.batch.LeaseID = ""
			r.activeInFlight--
			r.publishedSeqs = append(r.publishedSeqs, item.batch.Sequence)
			return nil
		}
	}
	return errors.New("telemetry history lease was not found")
}

func (r *fakePipelinedHistoryRepo) MarkHistoryEventsPublished(_ context.Context, leaseID string, eventIDs []string, _ time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, item := range r.batches {
		if item.batch.LeaseID == leaseID && item.state == "IN_FLIGHT" {
			keep := make([]HistoryObservation, 0, len(item.batch.Observations))
			done := make(map[string]struct{}, len(eventIDs))
			for _, id := range eventIDs {
				done[id] = struct{}{}
			}
			for _, obs := range item.batch.Observations {
				if _, ok := done[obs.ObservationID]; !ok {
					keep = append(keep, obs)
				}
			}
			item.batch.Observations = keep
			if len(keep) == 0 {
				item.state = "PUBLISHED"
				item.batch.LeaseID = ""
				r.activeInFlight--
				r.publishedSeqs = append(r.publishedSeqs, item.batch.Sequence)
			}
			return nil
		}
	}
	return errors.New("telemetry history lease was not found")
}

func (r *fakePipelinedHistoryRepo) DeadLetterHistoryEvents(_ context.Context, leaseID string, eventIDs []string, errorCode string, _ time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, item := range r.batches {
		if item.batch.LeaseID == leaseID && item.state == "IN_FLIGHT" {
			dead := make(map[string]struct{}, len(eventIDs))
			for _, id := range eventIDs {
				dead[id] = struct{}{}
			}
			keep := make([]HistoryObservation, 0, len(item.batch.Observations))
			for _, obs := range item.batch.Observations {
				if _, ok := dead[obs.ObservationID]; !ok {
					keep = append(keep, obs)
				}
			}
			item.batch.Observations = keep
			item.errorCode = errorCode
			if len(keep) == 0 {
				item.state = "DEAD"
				item.batch.LeaseID = ""
				r.activeInFlight--
			}
			return nil
		}
	}
	return errors.New("telemetry history lease was not found")
}

func (r *fakePipelinedHistoryRepo) RetryHistoryBatch(_ context.Context, leaseID string, retryAt time.Time, errorCode string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, item := range r.batches {
		if item.batch.LeaseID == leaseID && item.state == "IN_FLIGHT" {
			item.state = "PENDING"
			item.availableAt = retryAt
			item.errorCode = errorCode
			item.batch.LeaseID = ""
			r.activeInFlight--
			r.retriedLeaseIDs = append(r.retriedLeaseIDs, leaseID)
			return nil
		}
	}
	return errors.New("telemetry history lease was not found")
}

type fakePreparedHistorySink struct {
	mu                sync.Mutex
	prepareSeqs       []uint64
	insertTokens      []string
	activeInserts     int32
	maxActiveInserts  int32
	prepareHook       func(sequence uint64)
	insertHook        func(token string) error
	insertObsFallback []uint64
}

func (s *fakePreparedHistorySink) PrepareObservations(sequence uint64, observations []HistoryObservation) ([]byte, string, error) {
	s.mu.Lock()
	s.prepareSeqs = append(s.prepareSeqs, sequence)
	hook := s.prepareHook
	s.mu.Unlock()
	if hook != nil {
		hook(sequence)
	}
	token := fmt.Sprintf("seq:%04d:%d", sequence, len(observations))
	return []byte(token), token, nil
}

func (s *fakePreparedHistorySink) InsertPrepared(_ context.Context, compressed []byte, token string) error {
	cur := atomic.AddInt32(&s.activeInserts, 1)
	for {
		prev := atomic.LoadInt32(&s.maxActiveInserts)
		if cur <= prev || atomic.CompareAndSwapInt32(&s.maxActiveInserts, prev, cur) {
			break
		}
	}
	defer atomic.AddInt32(&s.activeInserts, -1)
	if string(compressed) != token {
		return fmt.Errorf("prepared payload %q != token %q", string(compressed), token)
	}
	s.mu.Lock()
	s.insertTokens = append(s.insertTokens, token)
	hook := s.insertHook
	s.mu.Unlock()
	if hook != nil {
		return hook(token)
	}
	return nil
}

func (s *fakePreparedHistorySink) InsertObservations(_ context.Context, sequence uint64, _ []HistoryObservation) error {
	s.mu.Lock()
	s.insertObsFallback = append(s.insertObsFallback, sequence)
	s.mu.Unlock()
	return nil
}

func makeFakeBatch(seq uint64, count int) HistoryBatch {
	obs := make([]HistoryObservation, count)
	for i := range count {
		obs[i] = HistoryObservation{ObservationID: fmt.Sprintf("018f2e00-90%02d-7000-8000-%012d", seq, i+1)}
	}
	return HistoryBatch{
		BatchID:      fmt.Sprintf("018f2e00-80%02d-7000-8000-000000000001", seq),
		Sequence:     seq,
		Observations: obs,
	}
}

func TestHistoryRelayPipelineStrictSequenceOrderAndPreparedReuse(t *testing.T) {
	now := time.Date(2026, 7, 29, 9, 0, 0, 0, time.UTC)
	batches := []HistoryBatch{makeFakeBatch(1, 2), makeFakeBatch(2, 2), makeFakeBatch(3, 1)}
	repo := newFakePipelinedHistoryRepo(batches)

	batch1Inserting := make(chan struct{})
	batch2Prepared := make(chan struct{})
	sink := &fakePreparedHistorySink{}
	sink.prepareHook = func(sequence uint64) {
		if sequence == 2 {
			close(batch2Prepared)
		}
	}
	sink.insertHook = func(token string) error {
		if strings.HasPrefix(token, "seq:0001:") {
			close(batch1Inserting)
			select {
			case <-batch2Prepared:
			case <-time.After(2 * time.Second):
				return errors.New("stage 1 did not prefetch/prepare batch 2 while batch 1 inserted")
			}
		}
		if strings.HasPrefix(token, "seq:0002:") {
			repo.mu.Lock()
			published := slices.Clone(repo.publishedSeqs)
			repo.mu.Unlock()
			if len(published) != 1 || published[0] != 1 {
				return fmt.Errorf("batch 2 inserted before batch 1 committed in Postgres: published=%v", published)
			}
		}
		return nil
	}

	relay, err := NewHistoryRelay(HistoryRelayConfig{
		Repository:    repo,
		Sink:          sink,
		BatchSize:     4,
		LeaseFor:      30 * time.Second,
		RetryAfter:    5 * time.Second,
		PipelineDepth: 2,
		Now:           func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	published, err := relay.RelayPending(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if published != 5 {
		t.Fatalf("published=%d, want 5", published)
	}
	<-batch1Inserting
	if len(sink.insertObsFallback) != 0 {
		t.Fatalf("expected PreparedHistorySink path, got fallback InsertObservations: %v", sink.insertObsFallback)
	}
	wantTokens := []string{"seq:0001:2", "seq:0002:2", "seq:0003:1"}
	if !slices.Equal(sink.insertTokens, wantTokens) {
		t.Fatalf("insertTokens=%v, want %v", sink.insertTokens, wantTokens)
	}
	if !slices.Equal(repo.publishedSeqs, []uint64{1, 2, 3}) {
		t.Fatalf("publishedSeqs=%v, want [1 2 3]", repo.publishedSeqs)
	}
	if repo.maxObservedFlight > 2 || repo.activeInFlight != 0 {
		t.Fatalf("in-flight max=%d active=%d", repo.maxObservedFlight, repo.activeInFlight)
	}
}

func TestHistoryRelayPipelineReleasesPrefetchedLeaseOnStage2Failure(t *testing.T) {
	clock := time.Date(2026, 7, 29, 9, 5, 0, 0, time.UTC)
	batches := []HistoryBatch{makeFakeBatch(1, 2), makeFakeBatch(2, 2)}
	repo := newFakePipelinedHistoryRepo(batches)

	batch2Prepared := make(chan struct{})
	failBatch1 := true
	sink := &fakePreparedHistorySink{}
	sink.prepareHook = func(sequence uint64) {
		if sequence == 2 && failBatch1 {
			select {
			case <-batch2Prepared:
			default:
				close(batch2Prepared)
			}
		}
	}
	sink.insertHook = func(token string) error {
		if strings.HasPrefix(token, "seq:0001:") && failBatch1 {
			<-batch2Prepared
			return errors.New("503 ClickHouse temporarily unavailable")
		}
		return nil
	}

	relay, err := NewHistoryRelay(HistoryRelayConfig{
		Repository:    repo,
		Sink:          sink,
		BatchSize:     4,
		LeaseFor:      30 * time.Second,
		RetryAfter:    5 * time.Second,
		PipelineDepth: 2,
		Now:           func() time.Time { return clock },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := relay.RelayPending(t.Context()); err == nil {
		t.Fatal("expected RelayPending error when batch 1 insert failed")
	}

	repo.mu.Lock()
	released := slices.Clone(repo.releasedBatchIDs)
	b1State := repo.batches[0].state
	b1Available := repo.batches[0].availableAt
	b2State := repo.batches[1].state
	b2Attempts := repo.batches[1].attempts
	b2Available := repo.batches[1].availableAt
	active := repo.activeInFlight
	repo.mu.Unlock()

	if !slices.Equal(released, []string{batches[1].BatchID}) {
		t.Fatalf("releasedBatchIDs=%v, want [%s]", released, batches[1].BatchID)
	}
	if b1State != "PENDING" || !b1Available.Equal(clock.Add(5*time.Second)) {
		t.Fatalf("batch 1 state=%s available=%s", b1State, b1Available)
	}
	if b2State != "PENDING" || b2Attempts != 0 || !b2Available.Equal(clock) {
		t.Fatalf("batch 2 lease release polluted backoff: state=%s attempts=%d available=%s", b2State, b2Attempts, b2Available)
	}
	if active != 0 {
		t.Fatalf("leaked activeInFlight=%d", active)
	}
	if !slices.Equal(sink.insertTokens, []string{"seq:0001:2"}) {
		t.Fatalf("batch 2 must never reach ClickHouse when batch 1 fails: tokens=%v", sink.insertTokens)
	}

	// Before retryAt expires, batch 2 must NOT overtake blocked batch 1.
	clock = clock.Add(2 * time.Second)
	failBatch1 = false
	earlyPublished, err := relay.RelayPending(t.Context())
	if err != nil || earlyPublished != 0 {
		t.Fatalf("batch 2 overtook blocked batch 1: published=%d err=%v", earlyPublished, err)
	}

	// Once retryAt passes, batch 1 commits before batch 2.
	clock = clock.Add(4 * time.Second)
	published, err := relay.RelayPending(t.Context())
	if err != nil || published != 4 {
		t.Fatalf("recovery RelayPending published=%d err=%v", published, err)
	}
	if !slices.Equal(repo.publishedSeqs, []uint64{1, 2}) {
		t.Fatalf("publishedSeqs=%v, want [1 2]", repo.publishedSeqs)
	}

	// Also verify context cancellation during Stage 2 releases prefetched batch 2 with active settle context.
	ctx2, cancel2 := context.WithCancel(t.Context())
	batches2 := []HistoryBatch{makeFakeBatch(10, 1), makeFakeBatch(11, 1)}
	repo2 := newFakePipelinedHistoryRepo(batches2)
	b2Prefetched := make(chan struct{})
	sink2 := &fakePreparedHistorySink{}
	sink2.prepareHook = func(seq uint64) {
		if seq == 11 {
			close(b2Prefetched)
		}
	}
	sink2.insertHook = func(token string) error {
		if strings.HasPrefix(token, "seq:0010:") {
			<-b2Prefetched
			cancel2()
			return context.Canceled
		}
		return nil
	}
	relay2, err := NewHistoryRelay(HistoryRelayConfig{
		Repository: repo2, Sink: sink2, BatchSize: 4, LeaseFor: 30 * time.Second, RetryAfter: 5 * time.Second, PipelineDepth: 2,
		Now: func() time.Time { return clock },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := relay2.RelayPending(ctx2); err == nil {
		t.Fatal("expected error on cancelled context")
	}
	if len(repo2.releasedBatchIDs) != 1 || repo2.releasedBatchIDs[0] != batches2[1].BatchID {
		t.Fatalf("cancelled pipeline did not release prefetched batch: %v", repo2.releasedBatchIDs)
	}
	if len(repo2.releaseActiveCtx) != 1 || !repo2.releaseActiveCtx[0] {
		t.Fatalf("expected release to use uncanceled settle context: %v", repo2.releaseActiveCtx)
	}
	if repo2.activeInFlight != 0 {
		t.Fatalf("leaked activeInFlight after cancel: %d", repo2.activeInFlight)
	}
}

func TestHistoryRelayPipelineMaxInFlightBoundUnderConcurrency(t *testing.T) {
	now := time.Date(2026, 7, 29, 9, 10, 0, 0, time.UTC)
	for _, depth := range []int{2, 3, 4} {
		batches := make([]HistoryBatch, 20)
		for i := range batches {
			batches[i] = makeFakeBatch(uint64(i+1), 2)
		}
		repo := newFakePipelinedHistoryRepo(batches)
		repo.claimHook = func(_ HistoryBatch) {
			time.Sleep(100 * time.Microsecond)
		}
		sink := &fakePreparedHistorySink{
			prepareHook: func(_ uint64) {
				time.Sleep(100 * time.Microsecond)
			},
			insertHook: func(_ string) error {
				time.Sleep(200 * time.Microsecond)
				return nil
			},
		}
		relay, err := NewHistoryRelay(HistoryRelayConfig{
			Repository:    repo,
			Sink:          sink,
			BatchSize:     4,
			LeaseFor:      30 * time.Second,
			RetryAfter:    time.Second,
			PipelineDepth: depth,
			Now:           func() time.Time { return now },
		})
		if err != nil {
			t.Fatal(err)
		}
		published, err := relay.RelayPending(t.Context())
		if err != nil {
			t.Fatalf("depth=%d err=%v", depth, err)
		}
		if published != 40 {
			t.Fatalf("depth=%d published=%d, want 40", depth, published)
		}
		if repo.maxObservedFlight > depth {
			t.Fatalf("depth=%d maxObservedFlight=%d exceeded limit", depth, repo.maxObservedFlight)
		}
		if repo.inFlightLimitHit {
			t.Fatalf("depth=%d Stage 1 attempted claim beyond maxInFlight", depth)
		}
		if repo.activeInFlight != 0 {
			t.Fatalf("depth=%d activeInFlight=%d after drain", depth, repo.activeInFlight)
		}
		if atomic.LoadInt32(&sink.maxActiveInserts) > 1 {
			t.Fatalf("depth=%d concurrent ClickHouse inserts=%d, want <= 1", depth, sink.maxActiveInserts)
		}
		for i, seq := range repo.publishedSeqs {
			if seq != uint64(i+1) {
				t.Fatalf("depth=%d out-of-order publishedSeqs=%v", depth, repo.publishedSeqs)
			}
		}
	}
}

func TestHistoryRelayRunWithPipelinedRepositoryPartialVsBacklog(t *testing.T) {
	now := time.Date(2026, 7, 29, 9, 20, 0, 0, time.UTC)
	// Backlog of [2, 2, 1] with BatchSize=2 drains all 3 batches immediately, then sleeps after partial batch 1.
	batches := []HistoryBatch{makeFakeBatch(1, 2), makeFakeBatch(2, 2), makeFakeBatch(3, 1)}
	repo := newFakePipelinedHistoryRepo(batches)
	sink := &fakePreparedHistorySink{}
	relay, err := NewHistoryRelay(HistoryRelayConfig{
		Repository:    repo,
		Sink:          sink,
		BatchSize:     2,
		LeaseFor:      30 * time.Second,
		RetryAfter:    time.Second,
		PipelineDepth: 2,
		Now:           func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 250*time.Millisecond)
	defer cancel()
	var counts []int
	relay.Run(ctx, time.Hour, func(published int, err error) {
		if err != nil {
			t.Fatalf("unexpected Run error: %v", err)
		}
		counts = append(counts, published)
	})
	if !slices.Equal(counts, []int{2, 2, 1}) {
		t.Fatalf("Run reported=%v, want [2 2 1]", counts)
	}
}

func TestHistoryPostgresRepositoryClosedValidation(t *testing.T) {
	var nilRepo *HistoryPostgresRepository
	now := time.Date(2026, 7, 29, 9, 30, 0, 0, time.UTC)
	if _, err := nilRepo.ClaimPipelinedHistoryBatch(t.Context(), 10, now, 30*time.Second, 2); err == nil {
		t.Fatal("expected error for nil repository")
	}
	if err := nilRepo.ReleaseHistoryBatchLease(t.Context(), "018f2e00-8000-7000-8000-000000000001", now); err == nil {
		t.Fatal("expected error for nil repository release")
	}
	emptyRepo := NewHistoryPostgresRepository(nil, nil)
	if _, err := emptyRepo.ClaimPipelinedHistoryBatch(t.Context(), 10, now, 30*time.Second, 0); err == nil {
		t.Fatal("expected error for invalid maxInFlight / nil pool")
	}
	if err := emptyRepo.ReleaseHistoryBatchLease(t.Context(), "", now); err == nil {
		t.Fatal("expected error for empty batchID / nil pool")
	}
}

type fakeDepthPipelinedRepo struct {
	*fakePipelinedHistoryRepo
	pending, publishing, dead int
}

func (r *fakeDepthPipelinedRepo) HistoryOutboxDepths(_ context.Context) (int, int, int, error) {
	return r.pending, r.publishing, r.dead, nil
}

func TestHistoryRelayRecordsPipelineMetricsAndOutboxDepths(t *testing.T) {
	now := time.Date(2026, 7, 29, 9, 45, 0, 0, time.UTC)
	registry := observability.NewRegistry()
	metrics := NewPipelineMetrics(registry)
	inner := newFakePipelinedHistoryRepo([]HistoryBatch{
		makeFakeBatch(1, 2),
		makeFakeBatch(2, 2),
	})
	repo := &fakeDepthPipelinedRepo{
		fakePipelinedHistoryRepo: inner,
		pending:                  4,
		publishing:               1,
		dead:                     2,
	}
	sink := &fakePreparedHistorySink{}
	relay, err := NewHistoryRelay(HistoryRelayConfig{
		Repository:    repo,
		Sink:          sink,
		BatchSize:     2,
		LeaseFor:      30 * time.Second,
		RetryAfter:    time.Second,
		PipelineDepth: 2,
		Metrics:       metrics,
		Now:           func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	published, err := relay.RelayPending(t.Context())
	if err != nil || published != 4 {
		t.Fatalf("RelayPending=(%d, %v), want (4, nil)", published, err)
	}
	rec := httptest.NewRecorder()
	registry.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	for _, needle := range []string{
		`hvac_s2_history_rows_projected_total 4`,
		`hvac_s2_history_flush_duration_seconds_count 2`,
		`hvac_s2_history_outbox_depth{state="pending"} 4`,
		`hvac_s2_history_outbox_depth{state="publishing"} 1`,
		`hvac_s2_history_outbox_depth{state="dead_letter"} 2`,
	} {
		if !strings.Contains(body, needle) {
			t.Fatalf("missing %q in /metrics output:\n%s", needle, body)
		}
	}
}
