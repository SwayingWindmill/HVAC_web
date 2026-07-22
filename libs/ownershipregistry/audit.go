package ownershipregistry

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type AuditRecord struct {
	MessageID           string
	EventType           string
	RouteKey            string
	Method              string
	PathTemplate        string
	SelectedOwner       string
	PreviousOwner       string
	RegistryRevision    int64
	PreviousRevision    int64
	RouteRevision       int64
	CompatibilityMode   string
	CohortBucket        *int
	OrganizationID      string
	InitiatingSubject   string
	InitiatingIssuer    string
	ExecutingService    string
	ExecutingSPIFFEID   string
	PolicyRevision      string
	CorrelationID       string
	TraceID             string
	OutcomeCode         string
	PrimaryStatus       int
	SecondaryStatus     int
	PrimaryBodySHA256   string
	SecondaryBodySHA256 string
	SemanticEqual       *bool
	OccurredAt          time.Time
}

type AuditSink interface {
	Record(context.Context, AuditRecord) error
}

type MemoryAuditSink struct {
	mu      sync.Mutex
	records []AuditRecord
	failure error
}

func NewMemoryAuditSink() *MemoryAuditSink {
	return &MemoryAuditSink{}
}

func (sink *MemoryAuditSink) Record(_ context.Context, record AuditRecord) error {
	if err := validateAuditRecord(record); err != nil {
		return err
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.failure != nil {
		return sink.failure
	}
	if record.MessageID == "" {
		record.MessageID = randomAuditID()
	}
	record.OccurredAt = record.OccurredAt.UTC()
	sink.records = append(sink.records, cloneAuditRecord(record))
	return nil
}

func (sink *MemoryAuditSink) Records() []AuditRecord {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	result := make([]AuditRecord, len(sink.records))
	for index, record := range sink.records {
		result[index] = cloneAuditRecord(record)
	}
	return result
}

func (sink *MemoryAuditSink) SetFailure(err error) {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.failure = err
}

type PostgresAuditSink struct {
	pool *pgxpool.Pool
}

func OpenPostgresAudit(ctx context.Context, dsn string) (*PostgresAuditSink, error) {
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	config.MaxConns = 4
	config.MinConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &PostgresAuditSink{pool: pool}, nil
}

func (sink *PostgresAuditSink) Close() {
	sink.pool.Close()
}

func (sink *PostgresAuditSink) Record(ctx context.Context, record AuditRecord) error {
	if err := validateAuditRecord(record); err != nil {
		return err
	}
	if record.MessageID == "" {
		record.MessageID = randomAuditID()
	}
	_, err := sink.pool.Exec(ctx, `
		INSERT INTO gateway.route_audit_records (
			message_id, event_type, route_key, method, path_template, selected_owner,
			previous_owner, registry_revision, previous_revision, route_revision,
			compatibility_mode, cohort_bucket, organization_id, initiating_subject,
			initiating_issuer, executing_service, executing_spiffe_id, policy_revision,
			correlation_id, trace_id, outcome_code, primary_status, secondary_status,
			primary_body_sha256, secondary_body_sha256, semantic_equal, occurred_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27)
	`, record.MessageID, record.EventType, record.RouteKey, record.Method, record.PathTemplate,
		record.SelectedOwner, record.PreviousOwner, record.RegistryRevision,
		record.PreviousRevision, record.RouteRevision, record.CompatibilityMode,
		record.CohortBucket, record.OrganizationID, record.InitiatingSubject,
		record.InitiatingIssuer, record.ExecutingService, record.ExecutingSPIFFEID,
		record.PolicyRevision, record.CorrelationID, record.TraceID, record.OutcomeCode,
		nullableStatus(record.PrimaryStatus), nullableStatus(record.SecondaryStatus),
		nullableString(record.PrimaryBodySHA256), nullableString(record.SecondaryBodySHA256),
		record.SemanticEqual, record.OccurredAt.UTC())
	return err
}

func validateAuditRecord(record AuditRecord) error {
	switch record.EventType {
	case "ROUTE_DECIDED", "ROUTE_POLICY_CHANGED":
	case "ROUTE_SHADOW_COMPARED", "ROUTE_FALLBACK_EXECUTED":
		if record.PreviousOwner == "" || record.OutcomeCode == "" || record.PrimaryStatus < 100 || record.SecondaryStatus < 100 || len(record.PrimaryBodySHA256) != 64 || len(record.SecondaryBodySHA256) != 64 {
			return errors.New("route comparison audit evidence is incomplete")
		}
		if record.EventType == "ROUTE_SHADOW_COMPARED" && record.SemanticEqual == nil {
			return errors.New("route shadow comparison result is missing")
		}
	default:
		return errors.New("route audit event type is invalid")
	}
	if record.RouteKey == "" || record.Method == "" || record.PathTemplate == "" || record.SelectedOwner == "" || record.RegistryRevision < 1 || record.RouteRevision < 1 {
		return errors.New("route audit ownership fields are incomplete")
	}
	if record.ExecutingService == "" || record.OccurredAt.IsZero() {
		return errors.New("route audit actor or timestamp is incomplete")
	}
	return nil
}

func cloneAuditRecord(record AuditRecord) AuditRecord {
	if record.CohortBucket != nil {
		bucket := *record.CohortBucket
		record.CohortBucket = &bucket
	}
	if record.SemanticEqual != nil {
		semanticEqual := *record.SemanticEqual
		record.SemanticEqual = &semanticEqual
	}
	return record
}

func nullableStatus(value int) any {
	if value == 0 {
		return nil
	}
	return value
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func randomAuditID() string {
	buffer := make([]byte, 24)
	if _, err := rand.Read(buffer); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(buffer)
}
