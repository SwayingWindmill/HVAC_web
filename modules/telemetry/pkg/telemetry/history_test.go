package telemetry

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
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
	splitRelay, _ := NewHistoryRelay(HistoryRelayConfig{
		Repository: splitRepo,
		Sink: &selectiveHistorySink{fn: func(rows []HistoryObservation) error {
			call++
			switch call {
			case 1:
				return fmt.Errorf("%w: initial poison", ErrHistoryBatchPermanent)
			case 2:
				return nil // left half [0,1] succeeds
			default:
				return errors.New("503 ClickHouse restarted")
			}
		}},
		BatchSize: 16, LeaseFor: 30 * time.Second, RetryAfter: 5 * time.Second,
		Now: func() time.Time { return now },
	})
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
		MaxAttempts:  defaultMaxHistoryAttempts,
		Observations: obs,
	}}
	relay, err := NewHistoryRelay(HistoryRelayConfig{
		Repository: repo,
		Sink:       &historySinkStub{err: errors.New("ClickHouse history insert returned 500")},
		BatchSize:  16, LeaseFor: 30 * time.Second, RetryAfter: 5 * time.Second,
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
	encoded, digest, err := encodeHistoryObservation(validObs)
	if err != nil {
		t.Fatal(err)
	}

	got, size, code := validateClaimedHistoryRow(validObs.ObservationID, encoded, digest)
	if code != "" || size != len(encoded)+1 || got.ObservationID != validObs.ObservationID {
		t.Fatalf("valid row rejected: code=%q size=%d", code, size)
	}

	if _, _, code := validateClaimedHistoryRow(validObs.ObservationID, []byte(`{invalid`), digest); code != historyPayloadInvalidCode {
		t.Fatalf("corrupt JSON code=%q, want %q", code, historyPayloadInvalidCode)
	}
	if _, _, code := validateClaimedHistoryRow(validObs.ObservationID, encoded, strings.Repeat("b", 64)); code != historyPayloadInvalidCode {
		t.Fatalf("bad digest code=%q, want %q", code, historyPayloadInvalidCode)
	}
	if _, _, code := validateClaimedHistoryRow("018f2e00-9000-7000-8000-000000000499", encoded, digest); code != historyPayloadInvalidCode {
		t.Fatalf("mismatched observation ID code=%q, want %q", code, historyPayloadInvalidCode)
	}

	oversized := validObs
	oversized.ValueString = stringPointer(strings.Repeat("z", maxHistoryBatchBytes))
	oversizedPayload, oversizedDigest, err := encodeHistoryObservation(oversized)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, code := validateClaimedHistoryRow(oversized.ObservationID, oversizedPayload, oversizedDigest); code != historyRowTooLargeCode {
		t.Fatalf("oversized row code=%q, want %q", code, historyRowTooLargeCode)
	}
}
