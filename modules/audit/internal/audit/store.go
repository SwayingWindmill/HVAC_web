package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/operationsauditevent"
	"github.com/quanlaihe/hvac-web/libs/sessionevent"
)

var (
	ErrRecordNotFound   = errors.New("audit record not found")
	ErrEnvelopeConflict = errors.New("duplicate message id has different protobuf bytes")
)

const zeroRecordHash = "0000000000000000000000000000000000000000000000000000000000000000"

// MessageMetadata is where an event came from: the PostgreSQL outbox topic, or operations-http.
// The inbox's partition_id and offset_value columns are left over from Kafka and always 0.
type MessageMetadata struct {
	Topic      string
	ReceivedAt time.Time
}

type Record struct {
	LedgerSequence     int64           `json:"ledgerSequence"`
	MessageID          string          `json:"messageId"`
	SchemaVersion      uint32          `json:"schemaVersion"`
	TenantID           string          `json:"tenantId"`
	AggregateType      string          `json:"aggregateType"`
	AggregateID        string          `json:"aggregateId"`
	AggregateVersion   uint64          `json:"aggregateVersion"`
	OccurredAt         time.Time       `json:"occurredAt"`
	InitiatingSubject  string          `json:"initiatingSubject"`
	InitiatingIssuer   string          `json:"initiatingIssuer"`
	ExecutingService   string          `json:"executingService"`
	ExecutingSPIFFEID  string          `json:"executingSpiffeId"`
	Action             string          `json:"action"`
	Result             string          `json:"result"`
	PolicyRevision     string          `json:"policyRevision"`
	CorrelationID      string          `json:"correlationId"`
	CausationID        string          `json:"causationId"`
	TraceID            string          `json:"traceId"`
	Traceparent        string          `json:"-"`
	ClientIP           string          `json:"clientIp,omitempty"`
	ClientKind         string          `json:"clientKind,omitempty"`
	ActorDisplay       string          `json:"actorDisplay,omitempty"`
	TargetLabel        string          `json:"targetLabel,omitempty"`
	ChangeSummary      json.RawMessage `json:"changeSummary,omitempty"`
	PayloadSHA256      string          `json:"payloadSha256"`
	PreviousRecordHash string          `json:"previousRecordHash"`
	RecordHash         string          `json:"recordHash"`
	RecordedAt         time.Time       `json:"recordedAt"`
}

type SearchFilter struct {
	Actor        string     `json:"actor,omitempty"`
	Action       string     `json:"action,omitempty"`
	Category     string     `json:"category,omitempty"`
	ResourceType string     `json:"resourceType,omitempty"`
	ResourceID   string     `json:"resourceId,omitempty"`
	Outcome      string     `json:"outcome,omitempty"`
	From         *time.Time `json:"from,omitempty"`
	To           *time.Time `json:"to,omitempty"`
	Limit        int        `json:"limit,omitempty"`
}

type SearchRecord struct {
	LedgerSequence int64           `json:"ledgerSequence"`
	MessageID      string          `json:"messageId"`
	TenantID       string          `json:"tenantId"`
	Actor          string          `json:"actor"`
	ActorDisplay   string          `json:"actorDisplay,omitempty"`
	ClientKind     string          `json:"clientKind,omitempty"`
	Action         string          `json:"action"`
	ResourceType   string          `json:"resourceType"`
	ResourceID     string          `json:"resourceId"`
	TargetLabel    string          `json:"targetLabel,omitempty"`
	Outcome        string          `json:"outcome"`
	PolicyRevision string          `json:"policyRevision"`
	CorrelationID  string          `json:"correlationId"`
	ChangeSummary  json.RawMessage `json:"changeSummary,omitempty"`
	OccurredAt     time.Time       `json:"occurredAt"`
}

type Store struct {
	consumer *pgxpool.Pool
	query    *pgxpool.Pool
}

func OpenStore(ctx context.Context, consumerDSN, queryDSN string) (*Store, error) {
	consumer, err := openPool(ctx, consumerDSN, 6)
	if err != nil {
		return nil, err
	}
	query, err := openPool(ctx, queryDSN, 6)
	if err != nil {
		consumer.Close()
		return nil, err
	}
	return &Store{consumer: consumer, query: query}, nil
}

func NewStore(consumer, query *pgxpool.Pool) *Store {
	return &Store{consumer: consumer, query: query}
}

func (store *Store) Close() {
	store.consumer.Close()
	if store.query != store.consumer {
		store.query.Close()
	}
}

type ledgerEntry struct {
	MessageID         string
	SchemaVersion     uint32
	TenantID          string
	AggregateType     string
	AggregateID       string
	AggregateVersion  uint64
	OccurredAt        time.Time
	InitiatingSubject string
	InitiatingIssuer  string
	ExecutingService  string
	ExecutingSPIFFEID string
	Action            string
	Result            string
	PolicyRevision    string
	CorrelationID     string
	CausationID       string
	TraceID           string
	Traceparent       string
	PayloadSHA256     string
	ClientIP          string
	ClientKind        string
	ActorDisplay      string
	TargetLabel       string
	ChangeSummary     []byte
}

func (store *Store) Consume(ctx context.Context, payload []byte, metadata MessageMetadata) (bool, error) {
	ctx, span := observability.Start(ctx, "postgres.audit.consume", observability.SpanKindClient, map[string]any{"db.system": "postgresql", "db.operation": "audit.consume"})
	defer span.End()
	event, err := sessionevent.UnmarshalBinary(payload)
	if err != nil {
		return false, fmt.Errorf("decode session audit protobuf: %w", err)
	}
	return store.appendLedgerRecord(ctx, ledgerEntry{
		MessageID:         event.MessageID,
		SchemaVersion:     event.SchemaVersion,
		TenantID:          event.TenantID,
		AggregateType:     event.AggregateType,
		AggregateID:       event.AggregateID,
		AggregateVersion:  event.AggregateVersion,
		OccurredAt:        time.UnixMilli(event.OccurredAtUnixMS).UTC(),
		InitiatingSubject: event.Actor.InitiatingSubject,
		InitiatingIssuer:  event.Actor.InitiatingIssuer,
		ExecutingService:  event.Actor.ExecutingService,
		ExecutingSPIFFEID: event.Actor.ExecutingSPIFFEID,
		Action:            event.Action,
		Result:            event.Result,
		PolicyRevision:    event.PolicyRevision,
		CorrelationID:     event.CorrelationID,
		CausationID:       event.CausationID,
		TraceID:           event.TraceID,
		Traceparent:       event.Traceparent,
		PayloadSHA256:     event.PayloadSHA256,
	}, payload, metadata)
}

func (store *Store) ConsumeOperations(
	ctx context.Context,
	payload []byte,
	metadata MessageMetadata,
) (bool, error) {
	ctx, span := observability.Start(ctx, "postgres.audit.consume", observability.SpanKindClient, map[string]any{
		"db.system": "postgresql", "db.operation": "audit.consume.operations",
	})
	defer span.End()
	event, err := operationsauditevent.Decode(payload)
	if err != nil {
		return false, err
	}
	return store.appendLedgerRecord(ctx, ledgerEntry{
		MessageID:         event.EventID,
		SchemaVersion:     uint32(event.SchemaVersion),
		TenantID:          event.TenantID,
		AggregateType:     AggregateTypeOperationsInvestigation,
		AggregateID:       event.AggregateID(),
		AggregateVersion:  event.AggregateVersion(),
		OccurredAt:        time.UnixMilli(event.OccurredAt).UTC(),
		InitiatingSubject: event.Actor.ActorID,
		InitiatingIssuer:  event.Actor.ActorIssuer,
		ExecutingService:  event.Actor.ExecutingService,
		ExecutingSPIFFEID: event.Actor.ExecutingSPIFFEID,
		Action:            event.Operation,
		Result:            event.Outcome,
		PolicyRevision:    event.PolicyRevision,
		CorrelationID:     event.CorrelationID(),
		CausationID:       event.AuthorizationDecisionID,
		PayloadSHA256:     event.PayloadSHA256(payload),
		ClientKind:        ClientKindSystem,
	}, payload, metadata)
}

// ConsumeDomain verifies and appends a Gateway domain mutation event into the tenant's hash chain.
func (store *Store) ConsumeDomain(
	ctx context.Context,
	payload []byte,
	metadata MessageMetadata,
) (bool, error) {
	ctx, span := observability.Start(ctx, "postgres.audit.consume", observability.SpanKindClient, map[string]any{
		"db.system": "postgresql", "db.operation": "audit.consume.domain",
	})
	defer span.End()
	event, err := DecodeDomainEvent(payload)
	if err != nil {
		return false, err
	}
	digest := sha256.Sum256(payload)
	return store.appendLedgerRecord(ctx, ledgerEntry{
		MessageID:         event.MessageID,
		SchemaVersion:     uint32(event.SchemaVersion),
		TenantID:          event.TenantID,
		AggregateType:     event.Category,
		AggregateID:       event.AggregateID(),
		AggregateVersion:  event.AggregateVersion,
		OccurredAt:        event.OccurredAt.UTC(),
		InitiatingSubject: event.InitiatingSubject,
		InitiatingIssuer:  event.InitiatingIssuer,
		ExecutingService:  event.ExecutingService,
		ExecutingSPIFFEID: event.ExecutingSPIFFEID,
		Action:            event.Action,
		Result:            event.Outcome,
		PolicyRevision:    event.PolicyRevision,
		CorrelationID:     event.CorrelationID,
		CausationID:       event.CausationID,
		TraceID:           event.TraceID,
		Traceparent:       event.Traceparent,
		PayloadSHA256:     hex.EncodeToString(digest[:]),
		ClientIP:          event.ClientIP,
		ClientKind:        event.ClientKind,
		ActorDisplay:      event.ActorDisplay,
		TargetLabel:       event.TargetLabel,
		ChangeSummary:     event.compactedChangeSummary(),
	}, payload, metadata)
}

func (store *Store) appendLedgerRecord(
	ctx context.Context,
	entry ledgerEntry,
	payload []byte,
	metadata MessageMetadata,
) (bool, error) {
	envelopeDigest := sha256.Sum256(payload)
	envelopeSHA := hex.EncodeToString(envelopeDigest[:])
	tx, err := store.consumer.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true)`, entry.TenantID); err != nil {
		return false, err
	}

	var insertedMessageID string
	err = tx.QueryRow(ctx, `
		INSERT INTO audit_ledger.inbox (
			message_id, tenant_id, topic, partition_id, offset_value, envelope_sha256, received_at
		) VALUES ($1,$2,$3,0,0,$4,$5)
		ON CONFLICT (message_id) DO NOTHING
		RETURNING message_id
	`, entry.MessageID, entry.TenantID, metadata.Topic, envelopeSHA, metadata.ReceivedAt.UTC()).Scan(&insertedMessageID)
	if errors.Is(err, pgx.ErrNoRows) {
		var existingSHA string
		if err := tx.QueryRow(ctx, `SELECT envelope_sha256 FROM audit_ledger.inbox WHERE message_id = $1`, entry.MessageID).Scan(&existingSHA); err != nil {
			return false, err
		}
		if existingSHA != envelopeSHA {
			return false, ErrEnvelopeConflict
		}
		if err := tx.Commit(ctx); err != nil {
			return false, err
		}
		return false, nil
	}
	if err != nil {
		return false, err
	}

	recordedAt := metadata.ReceivedAt.UTC()
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_ledger.tenant_heads (tenant_id, last_record_hash, updated_at)
		VALUES ($1,$2,$3)
		ON CONFLICT (tenant_id) DO NOTHING
	`, entry.TenantID, zeroRecordHash, recordedAt); err != nil {
		return false, err
	}
	var previousHash string
	if err := tx.QueryRow(ctx, `
		SELECT last_record_hash FROM audit_ledger.tenant_heads
		WHERE tenant_id = $1 FOR UPDATE
	`, entry.TenantID).Scan(&previousHash); err != nil {
		return false, err
	}
	recordHash := hashRecord(previousHash, payload)
	_, err = tx.Exec(ctx, `
		INSERT INTO audit_ledger.records (
			message_id, schema_version, tenant_id, aggregate_type, aggregate_id,
			aggregate_version, occurred_at, initiating_subject, initiating_issuer,
			executing_service, executing_spiffe_id, action, result,
			policy_revision, correlation_id, causation_id, trace_id, traceparent,
			payload_sha256, previous_record_hash, record_hash, recorded_at,
			client_ip, client_kind, actor_display, target_label, change_summary
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27)
	`, entry.MessageID, entry.SchemaVersion, entry.TenantID, entry.AggregateType,
		entry.AggregateID, entry.AggregateVersion, entry.OccurredAt,
		entry.InitiatingSubject, entry.InitiatingIssuer, entry.ExecutingService,
		entry.ExecutingSPIFFEID, entry.Action, entry.Result, entry.PolicyRevision,
		entry.CorrelationID, entry.CausationID, entry.TraceID, entry.Traceparent,
		entry.PayloadSHA256, previousHash, recordHash, recordedAt,
		nullableText(entry.ClientIP), nullableText(entry.ClientKind),
		nullableText(entry.ActorDisplay), nullableText(entry.TargetLabel),
		nullableJSON(entry.ChangeSummary))
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE audit_ledger.tenant_heads
		SET last_record_hash = $2, updated_at = $3
		WHERE tenant_id = $1
	`, entry.TenantID, recordHash, recordedAt); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (store *Store) GetRecord(ctx context.Context, tenantID, messageID string) (Record, error) {
	tx, err := store.query.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return Record{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true)`, tenantID); err != nil {
		return Record{}, err
	}
	record, err := scanRecord(tx.QueryRow(ctx, recordSelect+` WHERE message_id = $1`, messageID))
	if err != nil {
		return Record{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Record{}, err
	}
	return record, nil
}

func (store *Store) SearchRecords(ctx context.Context, tenantID string, filter SearchFilter) ([]SearchRecord, error) {
	limit := filter.Limit
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 100 {
		return nil, errors.New("audit search limit must be between 1 and 100")
	}
	if filter.From != nil && filter.To != nil && filter.To.Before(*filter.From) {
		return nil, errors.New("audit search time range is invalid")
	}
	categories, err := resolveSearchCategories(filter)
	if err != nil {
		return nil, err
	}
	tx, err := store.query.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true)`, tenantID); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `
SELECT ledger_sequence, message_id, tenant_id, initiating_subject,
       COALESCE(actor_display, ''), COALESCE(client_kind, ''),
       action, aggregate_type, aggregate_id, COALESCE(target_label, ''),
       result, policy_revision, correlation_id, change_summary, occurred_at
FROM audit_ledger.records
WHERE ($1 = '' OR initiating_subject = $1)
  AND ($2 = '' OR action = $2)
  AND ($3 = '' OR aggregate_type = $3)
  AND ($4::text[] IS NULL OR aggregate_type = ANY($4::text[]))
  AND ($5 = '' OR aggregate_id = $5)
  AND ($6 = '' OR result = $6)
  AND ($7::timestamptz IS NULL OR occurred_at >= $7::timestamptz)
  AND ($8::timestamptz IS NULL OR occurred_at <= $8::timestamptz)
ORDER BY occurred_at DESC, ledger_sequence DESC
LIMIT $9
`, filter.Actor, filter.Action, filter.ResourceType, categories, filter.ResourceID, filter.Outcome, filter.From, filter.To, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []SearchRecord{}
	for rows.Next() {
		var record SearchRecord
		var changeSummary []byte
		if err := rows.Scan(
			&record.LedgerSequence, &record.MessageID, &record.TenantID, &record.Actor,
			&record.ActorDisplay, &record.ClientKind,
			&record.Action, &record.ResourceType, &record.ResourceID, &record.TargetLabel,
			&record.Outcome, &record.PolicyRevision, &record.CorrelationID, &changeSummary, &record.OccurredAt,
		); err != nil {
			return nil, err
		}
		if len(changeSummary) > 0 {
			record.ChangeSummary = json.RawMessage(append([]byte(nil), changeSummary...))
		}
		result = append(result, record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return result, nil
}

func resolveSearchCategories(filter SearchFilter) ([]string, error) {
	if filter.Category == "" {
		return nil, nil
	}
	if len(filter.Category) > 128 {
		return nil, errors.New("audit search category is invalid")
	}
	if filter.Category == CategoryControlSchedule {
		return []string{AggregateTypeControlLock, AggregateTypeOperatingSchedule}, nil
	}
	return []string{filter.Category}, nil
}

func (store *Store) CountRecords(ctx context.Context, tenantID, messageID string) (int, error) {
	tx, err := store.query.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true)`, tenantID); err != nil {
		return 0, err
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM audit_ledger.records WHERE message_id = $1`, messageID).Scan(&count); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return count, nil
}

const recordSelect = `
	SELECT ledger_sequence, message_id, schema_version, tenant_id, aggregate_type,
	       aggregate_id, aggregate_version, occurred_at, initiating_subject,
	       initiating_issuer, executing_service, executing_spiffe_id,
	       action, result, policy_revision, correlation_id,
	       causation_id, trace_id, traceparent,
	       COALESCE(client_ip, ''), COALESCE(client_kind, ''),
	       COALESCE(actor_display, ''), COALESCE(target_label, ''), change_summary,
	       payload_sha256, previous_record_hash,
	       record_hash, recorded_at
	FROM audit_ledger.records`

type rowScanner interface {
	Scan(...any) error
}

func scanRecord(row rowScanner) (Record, error) {
	var record Record
	var changeSummary []byte
	err := row.Scan(
		&record.LedgerSequence, &record.MessageID, &record.SchemaVersion,
		&record.TenantID, &record.AggregateType, &record.AggregateID,
		&record.AggregateVersion, &record.OccurredAt, &record.InitiatingSubject,
		&record.InitiatingIssuer, &record.ExecutingService, &record.ExecutingSPIFFEID,
		&record.Action, &record.Result,
		&record.PolicyRevision, &record.CorrelationID, &record.CausationID,
		&record.TraceID, &record.Traceparent,
		&record.ClientIP, &record.ClientKind,
		&record.ActorDisplay, &record.TargetLabel, &changeSummary,
		&record.PayloadSHA256, &record.PreviousRecordHash,
		&record.RecordHash, &record.RecordedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Record{}, ErrRecordNotFound
	}
	if err == nil && len(changeSummary) > 0 {
		record.ChangeSummary = json.RawMessage(append([]byte(nil), changeSummary...))
	}
	return record, err
}

func nullableText(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableJSON(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

func hashRecord(previousHash string, payload []byte) string {
	digest := sha256.New()
	_, _ = digest.Write([]byte(previousHash))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write(payload)
	return hex.EncodeToString(digest.Sum(nil))
}

func openPool(ctx context.Context, dsn string, maxConns int32) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	config.MaxConns = maxConns
	config.MinConns = 1
	config.MaxConnLifetime = 30 * time.Minute
	config.MaxConnIdleTime = 5 * time.Minute
	config.HealthCheckPeriod = 30 * time.Second
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}
