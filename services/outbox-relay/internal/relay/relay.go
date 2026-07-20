package relay

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/sessionstore"
)

type Repository interface {
	ClaimPending(context.Context, string, time.Time, time.Duration) (sessionstore.OutboxRecord, error)
	MarkPublished(context.Context, string, string, time.Time) error
	MarkFailed(context.Context, string, string, string, time.Time) error
}

type Publisher interface {
	Publish(context.Context, sessionstore.OutboxRecord) error
}

type Config struct {
	Owner         string
	Lease         time.Duration
	IdleDelay     time.Duration
	RetryDelay    time.Duration
	Logger        *slog.Logger
	Observability *observability.Runtime
	Now           func() time.Time
}

type Relay struct {
	repository Repository
	publisher  Publisher
	config     Config
}

func New(repository Repository, publisher Publisher, config Config) *Relay {
	if repository == nil || publisher == nil {
		panic("relay repository and publisher are required")
	}
	if config.Owner == "" {
		config.Owner = "outbox-relay"
	}
	if config.Lease <= 0 {
		config.Lease = 30 * time.Second
	}
	if config.IdleDelay <= 0 {
		config.IdleDelay = 200 * time.Millisecond
	}
	if config.RetryDelay <= 0 {
		config.RetryDelay = time.Second
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	if config.Observability == nil {
		config.Observability = observability.NewRuntime(observability.RuntimeConfig{Service: "outbox-relay"})
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Relay{repository: repository, publisher: publisher, config: config}
}

func (relay *Relay) Run(ctx context.Context) error {
	for {
		published, err := relay.RunOnce(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil
			}
			return err
		}
		if published {
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(relay.config.IdleDelay):
		}
	}
}

func (relay *Relay) RunOnce(ctx context.Context) (bool, error) {
	now := relay.config.Now().UTC()
	record, err := relay.repository.ClaimPending(ctx, relay.config.Owner, now, relay.config.Lease)
	if errors.Is(err, sessionstore.ErrNoPendingOutbox) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	ctx = observability.ContextWithRemoteParent(ctx, relay.config.Observability.Tracer, record.Traceparent, "")
	ctx, span := observability.Start(ctx, "outbox.kafka.publish", observability.SpanKindProducer, map[string]any{
		"messaging.system": "kafka", "messaging.destination.name": record.Topic,
		"messaging.operation": "publish", "event.correlation_id": record.CorrelationID,
		"event.causation_id": record.CausationID,
	})
	defer span.End()
	_ = relay.config.Observability.Metrics.SetGauge("s0_outbox_oldest_age_seconds", "Age of the oldest claimed Outbox record.", map[string]string{"service": "outbox-relay"}, relay.config.Now().UTC().Sub(record.CreatedAt.UTC()).Seconds())
	if err := relay.publisher.Publish(ctx, record); err != nil {
		span.SetStatus("error", "BROKER_PUBLISH_FAILED")
		_ = relay.config.Observability.Metrics.AddCounter("s0_outbox_publish_total", "Outbox publish attempts.", map[string]string{"service": "outbox-relay", "result": "error"}, 1)
		relay.config.Logger.Warn("outbox_publish_failed",
			"message_id", record.MessageID,
			"aggregate_version", record.AggregateVersion,
			"error_code", "BROKER_PUBLISH_FAILED",
		)
		if markErr := relay.repository.MarkFailed(ctx, record.MessageID, relay.config.Owner, "BROKER_PUBLISH_FAILED", now.Add(relay.config.RetryDelay)); markErr != nil {
			return false, markErr
		}
		return false, nil
	}
	if err := relay.repository.MarkPublished(ctx, record.MessageID, relay.config.Owner, relay.config.Now().UTC()); err != nil {
		// The broker may already have accepted the record. Leaving the claim to
		// expire intentionally permits duplicate publication; Inbox idempotency
		// makes the downstream effect converge.
		return false, err
	}
	span.SetStatus("ok", "")
	_ = relay.config.Observability.Metrics.AddCounter("s0_outbox_publish_total", "Outbox publish attempts.", map[string]string{"service": "outbox-relay", "result": "ok"}, 1)
	relay.config.Logger.Info("outbox_published",
		"message_id", record.MessageID,
		"aggregate_version", record.AggregateVersion,
		"publish_attempt", record.PublishAttempts,
	)
	return true, nil
}
