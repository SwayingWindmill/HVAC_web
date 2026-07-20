package audit

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/segmentio/kafka-go"
)

type ConsumerConfig struct {
	Brokers []string
	Topic   string
	GroupID string
	Logger  *slog.Logger
	Now     func() time.Time
}

type Consumer struct {
	reader *kafka.Reader
	store  *Store
	logger *slog.Logger
	now    func() time.Time
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
		store:  store,
		logger: logger,
		now:    now,
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
		inserted, err := consumer.store.Consume(ctx, message.Value, MessageMetadata{
			Topic:      message.Topic,
			Partition:  message.Partition,
			Offset:     message.Offset,
			ReceivedAt: consumer.now().UTC(),
		})
		messageID := auditMessageID(message.Headers)
		if err != nil {
			consumer.logger.Error("audit_consumer_transaction_failed", "error_code", "AUDIT_TRANSACTION_FAILED", "message_id", messageID)
			return err
		}
		if err := consumer.reader.CommitMessages(ctx, message); err != nil {
			consumer.logger.Warn("audit_consumer_commit_failed", "error_code", "OFFSET_COMMIT_FAILED", "message_id", messageID)
			return err
		}
		consumer.logger.Info("audit_event_consumed", "message_id", messageID, "inserted", inserted, "partition", message.Partition, "offset", message.Offset)
	}
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
