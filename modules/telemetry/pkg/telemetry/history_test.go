package telemetry

import (
	"context"
	"errors"
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
		Repository:  repository,
		Sink:        sink,
		BatchSize:   64,
		LeaseFor:    30 * time.Second,
		RetryAfter:  2 * time.Second,
		MaxAttempts: 8,
		Now:         func() time.Time { return now },
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
		Repository:  repository,
		Sink:        sink,
		BatchSize:   32,
		LeaseFor:    15 * time.Second,
		RetryAfter:  5 * time.Second,
		MaxAttempts: 4,
		Now:         func() time.Time { return now },
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
	if !repository.retryAt.Equal(now.Add(5*time.Second)) || repository.errorCode != "CLICKHOUSE_INSERT_FAILED" || repository.maxAttempts != 4 {
		t.Fatalf("retry=%s code=%s max=%d", repository.retryAt, repository.errorCode, repository.maxAttempts)
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
	failedLeaseID    string
	retryAt          time.Time
	errorCode        string
	maxAttempts      int
}

func TestHistoryRelayRunDrainsWithoutPollingDelay(t *testing.T) {
	repo := &historyRepositoryStub{batch: HistoryBatch{LeaseID: "lease", Observations: []HistoryObservation{{ObservationID: "observation"}}}}
	relay, err := NewHistoryRelay(HistoryRelayConfig{Repository: repo, Sink: &historySinkStub{}, BatchSize: 1, LeaseFor: 30 * time.Second, RetryAfter: time.Second, MaxAttempts: 3})
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
	relay, err := NewHistoryRelay(HistoryRelayConfig{Repository: repo, Sink: &historySinkStub{}, BatchSize: 4, LeaseFor: 30 * time.Second, RetryAfter: time.Second, MaxAttempts: 3})
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

func (repository *historyRepositoryStub) ClaimHistoryBatch(context.Context, int, time.Time, time.Duration, int) (HistoryBatch, error) {
	return repository.batch, repository.claimErr
}

func (repository *historyRepositoryStub) MarkHistoryBatchPublished(_ context.Context, leaseID string, publishedAt time.Time) error {
	repository.publishedLeaseID = leaseID
	repository.publishedAt = publishedAt
	return nil
}

func (repository *historyRepositoryStub) RetryHistoryBatch(_ context.Context, leaseID string, retryAt time.Time, errorCode string, maxAttempts int) error {
	repository.failedLeaseID = leaseID
	repository.retryAt = retryAt
	repository.errorCode = errorCode
	repository.maxAttempts = maxAttempts
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
