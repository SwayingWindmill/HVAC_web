package relay_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/sessionstore"
	"github.com/quanlaihe/hvac-web/services/outbox-relay/internal/relay"
)

func TestRelayMarksPublishedOnlyAfterBrokerAck(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	repository := &fakeRepository{record: fixtureRecord(now)}
	publisher := &fakePublisher{}
	worker := relay.New(repository, publisher, relay.Config{Owner: "relay-01", Now: func() time.Time { return now }})
	published, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !published || publisher.calls != 1 || repository.published != 1 || repository.failed != 0 {
		t.Fatalf("unexpected relay result: published=%v publisher=%d marked=%d failed=%d", published, publisher.calls, repository.published, repository.failed)
	}
}

func TestRelayLeavesOutboxPendingOnBrokerFailure(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	repository := &fakeRepository{record: fixtureRecord(now)}
	publisher := &fakePublisher{err: errors.New("broker unavailable")}
	worker := relay.New(repository, publisher, relay.Config{Owner: "relay-01", Now: func() time.Time { return now }, RetryDelay: time.Second})
	published, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if published || repository.published != 0 || repository.failed != 1 || repository.lastErrorCode != "BROKER_PUBLISH_FAILED" {
		t.Fatalf("broker outage did not preserve the pending Outbox: %#v", repository)
	}
}

func TestRelayAllowsDuplicateAfterAckBeforeDatabaseMark(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	repository := &fakeRepository{record: fixtureRecord(now), markPublishedErr: errors.New("database disconnected after broker ack")}
	publisher := &fakePublisher{}
	worker := relay.New(repository, publisher, relay.Config{Owner: "relay-01", Now: func() time.Time { return now }})
	if _, err := worker.RunOnce(context.Background()); err == nil {
		t.Fatal("expected database mark failure")
	}
	repository.markPublishedErr = nil
	repository.claimed = false
	if published, err := worker.RunOnce(context.Background()); err != nil || !published {
		t.Fatalf("retry failed: published=%v err=%v", published, err)
	}
	if publisher.calls != 2 || repository.published != 1 {
		t.Fatalf("at-least-once retry window was not preserved: publisher=%d marked=%d", publisher.calls, repository.published)
	}
}

type fakeRepository struct {
	record           sessionstore.OutboxRecord
	claimed          bool
	published        int
	failed           int
	lastErrorCode    string
	markPublishedErr error
}

func (repository *fakeRepository) ClaimPending(_ context.Context, owner string, _ time.Time, _ time.Duration) (sessionstore.OutboxRecord, error) {
	if repository.claimed || repository.published > 0 {
		return sessionstore.OutboxRecord{}, sessionstore.ErrNoPendingOutbox
	}
	repository.claimed = true
	record := repository.record
	record.PublishAttempts++
	return record, nil
}

func (repository *fakeRepository) MarkPublished(_ context.Context, _, _ string, _ time.Time) error {
	if repository.markPublishedErr != nil {
		return repository.markPublishedErr
	}
	repository.published++
	return nil
}

func (repository *fakeRepository) MarkFailed(_ context.Context, _, _, errorCode string, _ time.Time) error {
	repository.failed++
	repository.lastErrorCode = errorCode
	repository.claimed = false
	return nil
}

type fakePublisher struct {
	calls int
	err   error
}

func (publisher *fakePublisher) Publish(_ context.Context, _ sessionstore.OutboxRecord) error {
	publisher.calls++
	return publisher.err
}

func fixtureRecord(now time.Time) sessionstore.OutboxRecord {
	return sessionstore.OutboxRecord{
		MessageID:        "message-01",
		Topic:            "control.security.session.v1",
		PartitionKey:     "bff-session:session-01",
		SchemaVersion:    1,
		AggregateType:    "bff-session",
		AggregateID:      "session-01",
		AggregateVersion: 1,
		OrganizationID:   "org-01",
		Payload:          []byte("protobuf-payload"),
		CreatedAt:        now,
	}
}
