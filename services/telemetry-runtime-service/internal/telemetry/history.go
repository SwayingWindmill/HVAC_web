package telemetry

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const historyInsertFailureCode = "CLICKHOUSE_INSERT_FAILED"

type HistoryObservation struct {
	ObservationID         string    `json:"observation_id"`
	TenantID              *string   `json:"tenant_id"`
	OwningOrganizationID  *string   `json:"owning_organization_id"`
	SiteID                *string   `json:"site_id"`
	DeviceID              *string   `json:"device_id"`
	PointID               *string   `json:"point_id"`
	SensorID              *string   `json:"sensor_id"`
	IntegrationInstanceID string    `json:"integration_instance_id"`
	SourceEventID         string    `json:"source_event_id"`
	SourcePartition       string    `json:"source_partition"`
	SourceOffset          int64     `json:"source_offset"`
	SourcePath            string    `json:"source_path"`
	TelemetryKey          string    `json:"telemetry_key"`
	ValueType             *string   `json:"value_type"`
	Unit                  *string   `json:"unit"`
	ValueJSON             *string   `json:"value_json"`
	ValueNumber           *float64  `json:"value_number"`
	ValueString           *string   `json:"value_string"`
	ValueBoolean          *uint8    `json:"value_boolean"`
	SampledAt             time.Time `json:"sampled_at"`
	ReceivedAt            time.Time `json:"received_at"`
	AcceptanceStatus      string    `json:"acceptance_status"`
	Quality               string    `json:"quality"`
	QualityReasons        []string  `json:"quality_reasons"`
	PayloadSHA256         string    `json:"payload_sha256"`
}

type HistoryBatch struct {
	LeaseID      string
	Observations []HistoryObservation
}

type HistoryRepository interface {
	ClaimHistoryBatch(context.Context, int, time.Time, time.Duration) (HistoryBatch, error)
	MarkHistoryBatchPublished(context.Context, string, time.Time) error
	RetryHistoryBatch(context.Context, string, time.Time, string, int) error
}

type HistorySink interface {
	InsertObservations(context.Context, []HistoryObservation) error
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
	if config.MaxAttempts < 1 || config.MaxAttempts > 100 {
		return nil, errors.New("history relay max attempts must be between 1 and 100")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &HistoryRelay{
		repository: config.Repository, sink: config.Sink, batchSize: config.BatchSize,
		leaseFor: config.LeaseFor, retryAfter: config.RetryAfter, maxAttempts: config.MaxAttempts,
		now: config.Now,
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
	if err := relay.sink.InsertObservations(ctx, batch.Observations); err != nil {
		retryAt := now.Add(relay.retryAfter)
		if retryErr := relay.repository.RetryHistoryBatch(ctx, batch.LeaseID, retryAt, historyInsertFailureCode, relay.maxAttempts); retryErr != nil {
			return 0, errors.Join(fmt.Errorf("insert telemetry history: %w", err), fmt.Errorf("retry telemetry history batch: %w", retryErr))
		}
		return 0, fmt.Errorf("insert telemetry history: %w", err)
	}
	if err := relay.repository.MarkHistoryBatchPublished(ctx, batch.LeaseID, now); err != nil {
		return 0, fmt.Errorf("mark telemetry history batch published: %w", err)
	}
	return len(batch.Observations), nil
}
