package audit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/segmentio/kafka-go"
)

type ConsumerConfig struct {
	Brokers       []string
	Topic         string
	GroupID       string
	Logger        *slog.Logger
	Observability *observability.Runtime
	Now           func() time.Time
}

type Consumer struct {
	reader        *kafka.Reader
	store         *Store
	logger        *slog.Logger
	observability *observability.Runtime
	now           func() time.Time
}

func NewConsumer(store *Store, config ConsumerConfig) *Consumer {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	if config.Observability == nil {
		config.Observability = observability.NewRuntime(observability.RuntimeConfig{Service: "audit-ledger-service"})
	}
	return &Consumer{
		reader: kafka.NewReader(kafka.ReaderConfig{
			Brokers:     append([]string(nil), config.Brokers...),
			Topic:       config.Topic,
			GroupID:     config.GroupID,
			MinBytes:    1,
			MaxBytes:    10 << 20,
			MaxWait:     time.Second,
			StartOffset: kafka.FirstOffset,
		}),
		store:         store,
		logger:        logger,
		observability: config.Observability,
		now:           now,
	}
}

func (consumer *Consumer) Close() error {
	return consumer.reader.Close()
}

func (consumer *Consumer) Run(ctx context.Context) error {
	for {
		message, err := consumer.reader.FetchMessage(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil
			}
			consumer.logger.Warn("audit_consumer_fetch_failed", "error_code", "BROKER_FETCH_FAILED")
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(500 * time.Millisecond):
				continue
			}
		}
		messageContext := observability.ContextWithRemoteParent(ctx, consumer.observability.Tracer, kafkaHeader(message.Headers, "traceparent"), "")
		messageContext, span := observability.Start(messageContext, "kafka.audit.consume", observability.SpanKindConsumer, map[string]any{
			"messaging.system": "kafka", "messaging.destination.name": message.Topic,
			"messaging.operation": "process", "messaging.kafka.partition": message.Partition,
			"event.correlation_id": kafkaHeader(message.Headers, "correlation-id"),
			"event.causation_id":   kafkaHeader(message.Headers, "causation-id"),
		})
		inserted, err := consumer.store.Consume(messageContext, message.Value, MessageMetadata{
			Topic:      message.Topic,
			Partition:  message.Partition,
			Offset:     message.Offset,
			ReceivedAt: consumer.now().UTC(),
		})
		messageID := auditMessageID(message.Headers)
		if err != nil {
			span.SetStatus("error", "AUDIT_TRANSACTION_FAILED")
			span.End()
			_ = consumer.observability.Metrics.AddCounter("s0_audit_ingestion_total", "Audit ingestion attempts.", map[string]string{"service": "audit-ledger-service", "result": "error"}, 1)
			consumer.logger.Error("audit_consumer_transaction_failed", "error_code", "AUDIT_TRANSACTION_FAILED", "message_id", messageID)
			return err
		}
		if err := consumer.reader.CommitMessages(ctx, message); err != nil {
			span.SetStatus("error", "OFFSET_COMMIT_FAILED")
			span.End()
			consumer.logger.Warn("audit_consumer_commit_failed", "error_code", "OFFSET_COMMIT_FAILED", "message_id", messageID)
			return err
		}
		span.SetStatus("ok", "")
		span.End()
		_ = consumer.observability.Metrics.AddCounter("s0_audit_ingestion_total", "Audit ingestion attempts.", map[string]string{"service": "audit-ledger-service", "result": "ok"}, 1)
		_ = consumer.observability.Metrics.ObserveHistogram("s0_audit_ingestion_latency_seconds", "End-to-end Audit ingestion latency.", map[string]string{"service": "audit-ledger-service"}, consumer.now().UTC().Sub(message.Time.UTC()).Seconds(), nil)
		_ = consumer.observability.Metrics.SetGauge("s0_audit_consumer_offset", "Latest committed Audit consumer offset.", map[string]string{"service": "audit-ledger-service", "topic": message.Topic, "partition": fmt.Sprintf("%d", message.Partition)}, float64(message.Offset))
		consumer.logger.Info("audit_event_consumed", "message_id", messageID, "inserted", inserted, "partition", message.Partition, "offset", message.Offset)
	}
}

func kafkaHeader(headers []kafka.Header, key string) string {
	for _, header := range headers {
		if header.Key == key {
			return string(header.Value)
		}
	}
	return ""
}

func auditMessageID(headers []kafka.Header) string {
	for _, header := range headers {
		if header.Key != "message-id" {
			continue
		}
		value := string(header.Value)
		if len(value) < 16 || len(value) > 128 {
			return "invalid"
		}
		for _, character := range value {
			if (character < 'a' || character > 'z') &&
				(character < 'A' || character > 'Z') &&
				(character < '0' || character > '9') &&
				character != '_' && character != '-' {
				return "invalid"
			}
		}
		return value
	}
	return "missing"
}
