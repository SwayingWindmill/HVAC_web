package relay

import (
	"context"
	"strconv"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/sessionstore"
	"github.com/segmentio/kafka-go"
)

type KafkaPublisher struct {
	writer *kafka.Writer
}

func NewKafkaPublisher(brokers []string) *KafkaPublisher {
	return &KafkaPublisher{writer: &kafka.Writer{
		Addr:                   kafka.TCP(brokers...),
		Balancer:               &kafka.Hash{},
		RequiredAcks:           kafka.RequireAll,
		Async:                  false,
		AllowAutoTopicCreation: false,
		BatchSize:              1,
		BatchTimeout:           10 * time.Millisecond,
		WriteTimeout:           10 * time.Second,
		ReadTimeout:            10 * time.Second,
	}}
}

func (publisher *KafkaPublisher) Close() error {
	return publisher.writer.Close()
}

func (publisher *KafkaPublisher) Publish(ctx context.Context, record sessionstore.OutboxRecord) error {
	return publisher.writer.WriteMessages(ctx, kafka.Message{
		Topic: record.Topic,
		Key:   []byte(record.PartitionKey),
		Value: append([]byte(nil), record.Payload...),
		Time:  record.CreatedAt.UTC(),
		Headers: []kafka.Header{
			{Key: "traceparent", Value: []byte(observability.Traceparent(ctx))},
			{Key: "correlation-id", Value: []byte(record.CorrelationID)},
			{Key: "causation-id", Value: []byte(record.CausationID)},
			{Key: "message-id", Value: []byte(record.MessageID)},
			{Key: "schema-version", Value: []byte(strconv.FormatUint(uint64(record.SchemaVersion), 10))},
			{Key: "aggregate-version", Value: []byte(strconv.FormatUint(record.AggregateVersion, 10))},
			{Key: "organization-id", Value: []byte(record.OrganizationID)},
			{Key: "content-type", Value: []byte("application/x-protobuf")},
		},
	})
}
