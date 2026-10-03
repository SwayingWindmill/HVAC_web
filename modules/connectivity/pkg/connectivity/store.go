package connectivity

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

var (
	ErrNotFound            = errors.New("connectivity record not found")
	ErrCorrelationMismatch = errors.New("command correlation does not match durable state")
)

// Store is Connectivity's database access for every Tenant. Each call works inside
// the Tenant it is given, resolved from the Registry gateway directory.
type Store struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

func Open(ctx context.Context, databaseURL string) (*Store, error) {
	config, err := pgxpool.ParseConfig(strings.TrimSpace(databaseURL))
	if err != nil {
		return nil, errors.New("parse connectivity database URL")
	}
	config.MaxConns = 8
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, errors.New("open connectivity database")
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, errors.New("connectivity database is unavailable")
	}
	return &Store{pool: pool, now: time.Now}, nil
}

func (store *Store) Close() {
	if store != nil && store.pool != nil {
		store.pool.Close()
	}
}

func (store *Store) PrepareCommandCorrelation(ctx context.Context, correlation commandmodel.CommandCorrelation) (commandmodel.CommandCorrelation, error) {
	if correlation.State != commandmodel.CorrelationPrepared {
		return commandmodel.CommandCorrelation{}, ErrCorrelationMismatch
	}
	tenantID := correlation.Envelope.TenantID
	tx, err := store.beginTenant(ctx, tenantID)
	if err != nil {
		return commandmodel.CommandCorrelation{}, err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `
INSERT INTO connectivity.command_reply_correlations (
  attempt_id, execution_fence, command_id, tenant_id, site_id, gateway_id,
  device_id, point_id, capability, external_device_id, payload_hash, lease_owner, lease_until,
  mapping_revision, binding_revision, provider_endpoint, provider_method,
  request_sha256, state, prepared_at, updated_at
) VALUES (
  $1::uuid, $2, $3::uuid, $4::uuid, $5::uuid, $6::uuid,
  $7::uuid, $8::uuid, $9, $10, $11, $12, $13,
  $14, $15, $16, $17, $18, 'PREPARED', $19, $19
)
ON CONFLICT (attempt_id, execution_fence) DO NOTHING
`, correlation.Envelope.AttemptID, correlation.Envelope.ExecutionFence, correlation.Envelope.CommandID,
		tenantID, correlation.Envelope.SiteID, correlation.GatewayID,
		correlation.Envelope.DeviceID, correlation.Envelope.PointID, string(correlation.Envelope.Capability), correlation.ExternalDeviceID,
		correlation.Envelope.PayloadHash, correlation.Envelope.LeaseOwner, correlation.Envelope.LeaseUntil.UTC(),
		correlation.MappingRevision, correlation.BindingRevision,
		correlation.ProviderEndpoint, correlation.ProviderMethod, correlation.RequestSHA256, correlation.PreparedAt.UTC())
	if err != nil {
		return commandmodel.CommandCorrelation{}, fmt.Errorf("prepare durable command correlation: %w", err)
	}
	existing, err := loadCorrelation(ctx, tx, tenantID, correlation.Envelope.AttemptID, correlation.Envelope.ExecutionFence)
	if err != nil {
		return commandmodel.CommandCorrelation{}, err
	}
	if !sameCorrelationIdentity(existing, correlation) {
		return commandmodel.CommandCorrelation{}, ErrCorrelationMismatch
	}
	if err := tx.Commit(ctx); err != nil {
		return commandmodel.CommandCorrelation{}, err
	}
	return existing, nil
}

// ArmCommandCorrelation records the commit point just before the MQTT publish: from
// here on the attempt may have reached the Gateway.
func (store *Store) ArmCommandCorrelation(ctx context.Context, tenantID, attemptID string, executionFence uint64, armedAt time.Time) error {
	tx, err := store.beginTenant(ctx, tenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `
UPDATE connectivity.command_reply_correlations
SET state = 'MAY_COMMIT', commit_armed_at = $4, updated_at = $4
WHERE tenant_id = $1::uuid AND attempt_id = $2::uuid AND execution_fence = $3 AND state = 'PREPARED'
`, tenantID, attemptID, executionFence, armedAt.UTC())
	if err != nil {
		return fmt.Errorf("arm durable command correlation: %w", err)
	}
	if tag.RowsAffected() == 0 {
		correlation, loadErr := loadCorrelation(ctx, tx, tenantID, attemptID, executionFence)
		if loadErr != nil || correlation.State == commandmodel.CorrelationPrepared {
			return ErrCorrelationMismatch
		}
	}
	return tx.Commit(ctx)
}

func (store *Store) RecordCommandReply(ctx context.Context, tenantID, gatewayID, commandID string, executionFence uint64, replySHA256, replyStatus string, replyEventTime time.Time, replyReasonCode string, edgeExecution *commandmodel.EdgeExecutionEvidence, repliedAt time.Time) (commandmodel.CommandCorrelation, error) {
	var edgeExecutionJSON []byte
	if edgeExecution != nil {
		if !edgeExecution.Valid() {
			return commandmodel.CommandCorrelation{}, ErrCorrelationMismatch
		}
		var err error
		edgeExecutionJSON, err = json.Marshal(edgeExecution)
		if err != nil {
			return commandmodel.CommandCorrelation{}, ErrCorrelationMismatch
		}
	}
	tx, err := store.beginTenant(ctx, tenantID)
	if err != nil {
		return commandmodel.CommandCorrelation{}, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `
UPDATE connectivity.command_reply_correlations
SET state = 'REPLIED', reply_sha256 = $5, reply_status = $6,
    reply_event_time = $7,
    reply_reason_code = NULLIF($8, ''), reply_execution_evidence = $9::jsonb,
    replied_at = $10, updated_at = $10
WHERE tenant_id = $1::uuid AND gateway_id = $2::uuid
  AND command_id = $3::uuid AND execution_fence = $4 AND state = 'MAY_COMMIT'
`, tenantID, gatewayID, commandID, executionFence, replySHA256, strings.TrimSpace(replyStatus), nullableTime(replyEventTime), strings.TrimSpace(replyReasonCode), nullableJSON(edgeExecutionJSON), repliedAt.UTC())
	if err != nil {
		return commandmodel.CommandCorrelation{}, fmt.Errorf("record durable command reply: %w", err)
	}
	correlation, err := scanCorrelation(tx.QueryRow(ctx, correlationSelect+`
WHERE tenant_id = $1::uuid AND gateway_id = $2::uuid AND command_id = $3::uuid AND execution_fence = $4
`, tenantID, gatewayID, commandID, executionFence))
	if err != nil {
		return commandmodel.CommandCorrelation{}, err
	}
	if tag.RowsAffected() == 0 && correlation.State != commandmodel.CorrelationReplied && correlation.State != commandmodel.CorrelationResolved {
		return commandmodel.CommandCorrelation{}, ErrCorrelationMismatch
	}
	if correlation.ReplySHA256 != replySHA256 || correlation.ReplyStatus != strings.TrimSpace(replyStatus) ||
		!sameEdgeExecution(correlation.EdgeExecution, edgeExecution) {
		return commandmodel.CommandCorrelation{}, ErrCorrelationMismatch
	}
	if err := tx.Commit(ctx); err != nil {
		return commandmodel.CommandCorrelation{}, err
	}
	return correlation, nil
}

// RecoverCommandReplies returns a Tenant's replies that arrived but were not yet
// resolved into the Command, for example because the process stopped in between.
func (store *Store) RecoverCommandReplies(ctx context.Context, tenantID string, limit int) ([]commandmodel.CommandCorrelation, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	tx, err := store.beginTenant(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, correlationSelect+`
WHERE tenant_id = $1::uuid AND state = 'REPLIED'
ORDER BY replied_at, attempt_id
LIMIT $2
`, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("load recoverable command replies: %w", err)
	}
	defer rows.Close()
	var out []commandmodel.CommandCorrelation
	for rows.Next() {
		correlation, scanErr := scanCorrelation(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, correlation)
	}
	return out, rows.Err()
}

func (store *Store) MarkCommandCorrelationResolved(ctx context.Context, tenantID, attemptID string, executionFence uint64, resolvedAt time.Time) error {
	tx, err := store.beginTenant(ctx, tenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `
UPDATE connectivity.command_reply_correlations
SET state = 'RESOLVED', resolved_at = $4, updated_at = $4
WHERE tenant_id = $1::uuid AND attempt_id = $2::uuid AND execution_fence = $3 AND state = 'REPLIED'
`, tenantID, attemptID, executionFence, resolvedAt.UTC())
	if err != nil {
		return fmt.Errorf("resolve durable command correlation: %w", err)
	}
	if tag.RowsAffected() == 0 {
		correlation, loadErr := loadCorrelation(ctx, tx, tenantID, attemptID, executionFence)
		if loadErr != nil || correlation.State != commandmodel.CorrelationResolved {
			return ErrCorrelationMismatch
		}
	}
	return tx.Commit(ctx)
}

func (store *Store) beginTenant(ctx context.Context, tenantID string) (pgx.Tx, error) {
	if store == nil || store.pool == nil {
		return nil, errors.New("connectivity store is closed")
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, err
	}
	if err := setTenant(ctx, tx, tenantID); err != nil {
		tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}

const correlationSelect = `
SELECT attempt_id::text, execution_fence, command_id::text, tenant_id::text, site_id::text,
       gateway_id::text, device_id::text, point_id::text, capability, external_device_id,
       payload_hash, lease_owner, lease_until, mapping_revision, binding_revision,
       provider_endpoint, provider_method, request_sha256, prepared_at, state,
       COALESCE(reply_sha256, ''), COALESCE(reply_status, ''), reply_event_time,
       COALESCE(reply_reason_code, ''), reply_execution_evidence, replied_at
FROM connectivity.command_reply_correlations
`

type rowScanner interface {
	Scan(dest ...any) error
}

func loadCorrelation(ctx context.Context, tx pgx.Tx, tenantID, attemptID string, fence uint64) (commandmodel.CommandCorrelation, error) {
	return scanCorrelation(tx.QueryRow(ctx, correlationSelect+`
WHERE tenant_id = $1::uuid AND attempt_id = $2::uuid AND execution_fence = $3
`, tenantID, attemptID, fence))
}

func scanCorrelation(row rowScanner) (commandmodel.CommandCorrelation, error) {
	var correlation commandmodel.CommandCorrelation
	var state, capability string
	var replyEventTime, repliedAt *time.Time
	var edgeExecutionJSON []byte
	err := row.Scan(
		&correlation.Envelope.AttemptID, &correlation.Envelope.ExecutionFence, &correlation.Envelope.CommandID,
		&correlation.Envelope.TenantID, &correlation.Envelope.SiteID, &correlation.GatewayID,
		&correlation.Envelope.DeviceID, &correlation.Envelope.PointID, &capability, &correlation.ExternalDeviceID,
		&correlation.Envelope.PayloadHash, &correlation.Envelope.LeaseOwner, &correlation.Envelope.LeaseUntil,
		&correlation.MappingRevision, &correlation.BindingRevision,
		&correlation.ProviderEndpoint, &correlation.ProviderMethod, &correlation.RequestSHA256, &correlation.PreparedAt,
		&state, &correlation.ReplySHA256, &correlation.ReplyStatus, &replyEventTime, &correlation.ReplyReasonCode, &edgeExecutionJSON, &repliedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return commandmodel.CommandCorrelation{}, ErrNotFound
	}
	if err != nil {
		return commandmodel.CommandCorrelation{}, fmt.Errorf("scan command correlation: %w", err)
	}
	correlation.Envelope.Capability = commandmodel.Capability(capability)
	correlation.State = commandmodel.CorrelationState(state)
	if len(edgeExecutionJSON) > 0 {
		var evidence commandmodel.EdgeExecutionEvidence
		if err := json.Unmarshal(edgeExecutionJSON, &evidence); err != nil || !evidence.Valid() {
			return commandmodel.CommandCorrelation{}, ErrCorrelationMismatch
		}
		correlation.EdgeExecution = &evidence
	}
	if replyEventTime != nil {
		correlation.ReplyEventTime = replyEventTime.UTC()
	}
	if repliedAt != nil {
		correlation.RepliedAt = repliedAt.UTC()
	}
	return correlation, nil
}

func sameCorrelationIdentity(left, right commandmodel.CommandCorrelation) bool {
	return left.Envelope.AttemptID == right.Envelope.AttemptID && left.Envelope.ExecutionFence == right.Envelope.ExecutionFence &&
		left.Envelope.CommandID == right.Envelope.CommandID && left.Envelope.TenantID == right.Envelope.TenantID &&
		left.Envelope.SiteID == right.Envelope.SiteID && left.Envelope.DeviceID == right.Envelope.DeviceID &&
		left.Envelope.PointID == right.Envelope.PointID && left.Envelope.Capability == right.Envelope.Capability &&
		left.Envelope.PayloadHash == right.Envelope.PayloadHash &&
		left.Envelope.LeaseOwner == right.Envelope.LeaseOwner && left.GatewayID == right.GatewayID &&
		left.ExternalDeviceID == right.ExternalDeviceID &&
		left.MappingRevision == right.MappingRevision && left.BindingRevision == right.BindingRevision &&
		left.ProviderEndpoint == right.ProviderEndpoint && left.ProviderMethod == right.ProviderMethod &&
		left.RequestSHA256 == right.RequestSHA256
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.UTC()
}

func nullableJSON(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return string(value)
}

func sameEdgeExecution(left, right *commandmodel.EdgeExecutionEvidence) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
}

func newUUIDv7(now time.Time) (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	millis := uint64(now.UTC().UnixMilli())
	raw[0] = byte(millis >> 40)
	raw[1] = byte(millis >> 32)
	raw[2] = byte(millis >> 24)
	raw[3] = byte(millis >> 16)
	raw[4] = byte(millis >> 8)
	raw[5] = byte(millis)
	raw[6] = (raw[6] & 0x0f) | 0x70
	raw[8] = (raw[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(raw[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32], nil
}

func (store *Store) clock() time.Time {
	if store != nil && store.now != nil {
		return store.now()
	}
	return time.Now()
}
