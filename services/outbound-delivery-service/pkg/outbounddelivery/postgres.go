package outbounddelivery

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const recoveryBatch = 100

type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(pool *pgxpool.Pool) (*PostgresStore, error) {
	if pool == nil {
		return nil, errors.New("outbound delivery PostgreSQL pool is required")
	}
	return &PostgresStore{pool: pool}, nil
}

func (store *PostgresStore) PutIntegration(ctx context.Context, request PutIntegrationRequest) (IntegrationDefinition, error) {
	definition := request.Definition
	if err := definition.Validate(); err != nil {
		return IntegrationDefinition{}, err
	}
	tx, err := store.beginTenant(ctx, definition.TenantID)
	if err != nil {
		return IntegrationDefinition{}, err
	}
	defer tx.Rollback(ctx)

	if request.ExpectedRevision == 0 {
		if definition.ID == "" {
			definition.ID, err = uuidv7(definition.CreatedAt)
			if err != nil {
				return IntegrationDefinition{}, err
			}
		}
		definition.Revision = 1
		_, err = tx.Exec(ctx, `INSERT INTO outbound_delivery.integration_definitions
  (id,tenant_id,name,current_revision,created_at,updated_at)
VALUES ($1::uuid,$2::uuid,$3,$4,$5,$5)`, definition.ID, definition.TenantID, definition.Name, definition.Revision, definition.CreatedAt.UTC())
		if err != nil {
			return IntegrationDefinition{}, err
		}
		if err = insertIntegrationRevision(ctx, tx, definition); err != nil {
			return IntegrationDefinition{}, err
		}
	} else {
		var currentRevision uint64
		err = tx.QueryRow(ctx, `SELECT current_revision
FROM outbound_delivery.integration_definitions
WHERE id=$1::uuid AND tenant_id=$2::uuid
FOR UPDATE`, definition.ID, definition.TenantID).Scan(&currentRevision)
		if err != nil {
			return IntegrationDefinition{}, err
		}
		if currentRevision != request.ExpectedRevision {
			return IntegrationDefinition{}, ErrRevisionConflict
		}
		definition.Revision = currentRevision + 1
		if err = insertIntegrationRevision(ctx, tx, definition); err != nil {
			return IntegrationDefinition{}, err
		}
		_, err = tx.Exec(ctx, `UPDATE outbound_delivery.integration_definitions
SET name=$3,current_revision=$4,updated_at=$5
WHERE id=$1::uuid AND tenant_id=$2::uuid`, definition.ID, definition.TenantID, definition.Name, definition.Revision, definition.CreatedAt.UTC())
		if err != nil {
			return IntegrationDefinition{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return IntegrationDefinition{}, err
	}
	return definition, nil
}

func insertIntegrationRevision(ctx context.Context, tx pgx.Tx, definition IntegrationDefinition) error {
	_, err := tx.Exec(ctx, `INSERT INTO outbound_delivery.integration_definition_revisions
  (integration_id,tenant_id,revision,adapter_type,destination_url,allowed_hosts,credential_ref,enabled,
   max_request_bytes,max_response_bytes,timeout_ms,max_concurrency,max_attempts,retry_delay_ms,created_by_principal_id,created_at)
VALUES ($1::uuid,$2::uuid,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`,
		definition.ID, definition.TenantID, definition.Revision, definition.AdapterType, definition.DestinationURL,
		definition.AllowedHosts, nilIfEmpty(definition.CredentialRef), definition.Enabled, definition.MaxRequestBytes,
		definition.MaxResponseBytes, definition.Timeout.Milliseconds(), definition.MaxConcurrency, definition.MaxAttempts,
		definition.RetryDelay.Milliseconds(), nilIfEmpty(definition.CreatedByPrincipalID), definition.CreatedAt.UTC())
	return err
}

func (store *PostgresStore) SubmitIntent(ctx context.Context, request SubmitIntentRequest) (DeliveryIntent, error) {
	if err := request.Validate(); err != nil {
		return DeliveryIntent{}, err
	}
	now := request.CreatedAt.UTC()
	intentID, err := uuidv7(now)
	if err != nil {
		return DeliveryIntent{}, err
	}
	digest := PayloadDigest(request.Payload)
	tx, err := store.beginTenant(ctx, request.TenantID)
	if err != nil {
		return DeliveryIntent{}, err
	}
	defer tx.Rollback(ctx)

	var insertedID string
	err = tx.QueryRow(ctx, `INSERT INTO outbound_delivery.delivery_intents
  (id,tenant_id,site_id,integration_id,purpose,payload_schema,payload,payload_digest,idempotency_key,
   source_aggregate_type,source_aggregate_id,classification,state,attempt_count,created_at,updated_at)
VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6,$7::jsonb,$8,$9,$10,$11,$12,'READY',0,$13,$13)
ON CONFLICT (tenant_id,integration_id,idempotency_key) DO NOTHING
RETURNING id::text`, intentID, request.TenantID, nilUUID(request.SiteID), request.IntegrationID, request.Purpose,
		request.PayloadSchema, request.Payload, digest, request.IdempotencyKey, request.SourceAggregateType,
		request.SourceAggregateID, request.Classification, now).Scan(&insertedID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return DeliveryIntent{}, err
	}
	if errors.Is(err, pgx.ErrNoRows) {
		intent, lookupErr := loadIntentByIdempotency(ctx, tx, request.TenantID, request.IntegrationID, request.IdempotencyKey)
		if lookupErr != nil {
			return DeliveryIntent{}, lookupErr
		}
		if intent.PayloadDigest != digest || intent.Purpose != request.Purpose || intent.PayloadSchema != request.PayloadSchema ||
			intent.SiteID != request.SiteID || intent.SourceAggregateType != request.SourceAggregateType ||
			intent.SourceAggregateID != request.SourceAggregateID || intent.Classification != request.Classification {
			return DeliveryIntent{}, ErrIdempotencyConflict
		}
		if err = tx.Commit(ctx); err != nil {
			return DeliveryIntent{}, err
		}
		return intent, nil
	}
	intent, err := loadIntentByID(ctx, tx, request.TenantID, insertedID)
	if err != nil {
		return DeliveryIntent{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return DeliveryIntent{}, err
	}
	return intent, nil
}

func (store *PostgresStore) ClaimNext(ctx context.Context, scope Scope, workerID string, now time.Time, leaseDuration time.Duration) (*ClaimedDelivery, error) {
	if strings.TrimSpace(scope.TenantID) == "" || strings.TrimSpace(workerID) == "" || leaseDuration <= 0 {
		return nil, errors.New("tenant, worker id and positive lease duration are required")
	}
	tx, err := store.beginTenant(ctx, scope.TenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	claim := ClaimedDelivery{}
	var siteID *string
	var payload []byte
	var credentialRef *string
	var timeoutMS, retryDelayMS int64
	err = tx.QueryRow(ctx, `SELECT
  i.id::text,i.tenant_id::text,i.site_id::text,i.integration_id::text,i.purpose,i.payload_schema,i.payload::text,
  i.payload_digest,i.idempotency_key,i.source_aggregate_type,i.source_aggregate_id,i.classification,i.state,
  i.attempt_count,i.next_retry_at,i.created_at,i.updated_at,
  d.name,d.current_revision,r.adapter_type,r.destination_url,r.allowed_hosts,r.credential_ref,r.enabled,
  r.max_request_bytes,r.max_response_bytes,r.timeout_ms,r.max_concurrency,r.max_attempts,r.retry_delay_ms,r.created_at
FROM outbound_delivery.delivery_intents i
JOIN outbound_delivery.integration_definitions d ON d.tenant_id=i.tenant_id AND d.id=i.integration_id
JOIN outbound_delivery.integration_definition_revisions r ON r.tenant_id=d.tenant_id AND r.integration_id=d.id AND r.revision=d.current_revision
WHERE i.tenant_id=$1::uuid
  AND (i.state='READY' OR (i.state='RETRY_WAIT' AND i.next_retry_at <= $2))
  AND d.current_revision=r.revision AND r.enabled=true
ORDER BY i.created_at,i.id
FOR UPDATE OF i SKIP LOCKED
LIMIT 1`, scope.TenantID, now.UTC()).Scan(
		&claim.Intent.ID, &claim.Intent.TenantID, &siteID, &claim.Intent.IntegrationID, &claim.Intent.Purpose,
		&claim.Intent.PayloadSchema, &payload, &claim.Intent.PayloadDigest, &claim.Intent.IdempotencyKey,
		&claim.Intent.SourceAggregateType, &claim.Intent.SourceAggregateID, &claim.Intent.Classification, &claim.Intent.State,
		&claim.Intent.AttemptCount, &claim.Intent.NextRetryAt, &claim.Intent.CreatedAt, &claim.Intent.UpdatedAt,
		&claim.Integration.Name, &claim.Integration.Revision, &claim.Integration.AdapterType, &claim.Integration.DestinationURL,
		&claim.Integration.AllowedHosts, &credentialRef, &claim.Integration.Enabled, &claim.Integration.MaxRequestBytes,
		&claim.Integration.MaxResponseBytes, &timeoutMS, &claim.Integration.MaxConcurrency, &claim.Integration.MaxAttempts,
		&retryDelayMS, &claim.Integration.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNothingReady
	}
	if err != nil {
		return nil, err
	}
	claim.Intent.SiteID = deref(siteID)
	claim.Intent.Payload = append([]byte(nil), payload...)
	claim.Integration.ID = claim.Intent.IntegrationID
	claim.Integration.TenantID = claim.Intent.TenantID
	claim.Integration.CredentialRef = deref(credentialRef)
	claim.Integration.Timeout = time.Duration(timeoutMS) * time.Millisecond
	claim.Integration.RetryDelay = time.Duration(retryDelayMS) * time.Millisecond

	attemptID, err := uuidv7(now.UTC())
	if err != nil {
		return nil, err
	}
	attemptNo := claim.Intent.AttemptCount + 1
	leaseUntil := now.UTC().Add(leaseDuration)
	_, err = tx.Exec(ctx, `INSERT INTO outbound_delivery.delivery_attempts
  (id,tenant_id,intent_id,attempt_no,integration_revision,outcome,retryable,started_at,lease_owner,lease_until)
VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5,'MAYBE_SENT',false,$6,$7,$8)`,
		attemptID, scope.TenantID, claim.Intent.ID, attemptNo, claim.Integration.Revision, now.UTC(), workerID, leaseUntil)
	if err != nil {
		return nil, err
	}
	commandTag, err := tx.Exec(ctx, `UPDATE outbound_delivery.delivery_intents
SET state='LEASED',attempt_count=$3,next_retry_at=NULL,lease_owner=$4,lease_until=$5,updated_at=$6
WHERE tenant_id=$1::uuid AND id=$2::uuid AND state IN ('READY','RETRY_WAIT')`,
		scope.TenantID, claim.Intent.ID, attemptNo, workerID, leaseUntil, now.UTC())
	if err != nil {
		return nil, err
	}
	if commandTag.RowsAffected() != 1 {
		return nil, ErrLeaseLost
	}
	claim.Intent.State = IntentLeased
	claim.Intent.AttemptCount = attemptNo
	claim.Intent.NextRetryAt = nil
	claim.Intent.UpdatedAt = now.UTC()
	claim.Attempt = DeliveryAttempt{
		ID: attemptID, IntentID: claim.Intent.ID, AttemptNo: attemptNo, IntegrationRevision: claim.Integration.Revision,
		Outcome: OutcomeMaybeSent, StartedAt: now.UTC(), LeaseOwner: workerID, LeaseUntil: leaseUntil,
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &claim, nil
}

func (store *PostgresStore) CompleteAttempt(ctx context.Context, scope Scope, attemptID, workerID string, result AdapterResult, now time.Time) error {
	tx, err := store.beginTenant(ctx, scope.TenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var intentID, leaseOwner string
	var attemptNo, maxAttempts int
	var leaseUntil time.Time
	var retryDelayMS int64
	err = tx.QueryRow(ctx, `SELECT a.intent_id::text,a.attempt_no,a.lease_owner,a.lease_until,r.max_attempts,r.retry_delay_ms
FROM outbound_delivery.delivery_attempts a
JOIN outbound_delivery.delivery_intents i ON i.tenant_id=a.tenant_id AND i.id=a.intent_id
JOIN outbound_delivery.integration_definition_revisions r
  ON r.tenant_id=a.tenant_id AND r.integration_id=i.integration_id AND r.revision=a.integration_revision
WHERE a.tenant_id=$1::uuid AND a.id=$2::uuid AND a.completed_at IS NULL
FOR UPDATE OF a,i`, scope.TenantID, attemptID).Scan(&intentID, &attemptNo, &leaseOwner, &leaseUntil, &maxAttempts, &retryDelayMS)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrLeaseLost
	}
	if err != nil {
		return err
	}
	if leaseOwner != workerID || !leaseUntil.After(now.UTC()) {
		return ErrLeaseLost
	}
	decision := decideCompletion(result, attemptNo, maxAttempts, time.Duration(retryDelayMS)*time.Millisecond, now.UTC())
	commandTag, err := tx.Exec(ctx, `UPDATE outbound_delivery.delivery_attempts
SET outcome=$4,retryable=$5,error_code=$6,provider_request_id=$7,http_status=$8,response_digest=$9,
    completed_at=$10,lease_owner=NULL,lease_until=NULL
WHERE tenant_id=$1::uuid AND id=$2::uuid AND lease_owner=$3 AND completed_at IS NULL`,
		scope.TenantID, attemptID, workerID, result.Outcome, result.Retryable, nilIfEmpty(result.ErrorCode),
		nilIfEmpty(result.ProviderRequestID), nilIfZero(result.HTTPStatus), nilIfEmpty(result.ResponseDigest), now.UTC())
	if err != nil {
		return err
	}
	if commandTag.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	_, err = tx.Exec(ctx, `UPDATE outbound_delivery.delivery_intents
SET state=$3,next_retry_at=$4,lease_owner=NULL,lease_until=NULL,updated_at=$5
WHERE tenant_id=$1::uuid AND id=$2::uuid`, scope.TenantID, intentID, decision.State, decision.RetryAt, now.UTC())
	if err != nil {
		return err
	}
	if result.Outcome == OutcomeDelivered || result.Outcome == OutcomeAcceptedNotConfirmed {
		receiptID, idErr := uuidv7(now.UTC())
		if idErr != nil {
			return idErr
		}
		_, err = tx.Exec(ctx, `INSERT INTO outbound_delivery.delivery_receipts
  (id,tenant_id,intent_id,attempt_id,provider_request_id,provider_message_id,http_status,response_digest,final_outcome,accepted_at)
VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6,$7,$8,$9,$10)`,
			receiptID, scope.TenantID, intentID, attemptID, nilIfEmpty(result.ProviderRequestID), nilIfEmpty(result.ProviderMessageID),
			nilIfZero(result.HTTPStatus), nilIfEmpty(result.ResponseDigest), result.Outcome, now.UTC())
		if err != nil {
			return err
		}
	}
	if decision.DeadLetter {
		if err = insertDeadLetter(ctx, tx, scope.TenantID, intentID, attemptID, deadReason(result), decision.RequiresDuplicateRiskAck, now.UTC()); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (store *PostgresStore) RecoverExpired(ctx context.Context, scope Scope, now time.Time) (int, error) {
	tx, err := store.beginTenant(ctx, scope.TenantID)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `SELECT a.id::text,a.intent_id::text
FROM outbound_delivery.delivery_attempts a
WHERE a.tenant_id=$1::uuid AND a.completed_at IS NULL AND a.lease_until < $2
ORDER BY a.lease_until,a.id
FOR UPDATE SKIP LOCKED
LIMIT $3`, scope.TenantID, now.UTC(), recoveryBatch)
	if err != nil {
		return 0, err
	}
	type expiredAttempt struct{ attemptID, intentID string }
	var expired []expiredAttempt
	for rows.Next() {
		var item expiredAttempt
		if err = rows.Scan(&item.attemptID, &item.intentID); err != nil {
			rows.Close()
			return 0, err
		}
		expired = append(expired, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	for _, item := range expired {
		_, err = tx.Exec(ctx, `UPDATE outbound_delivery.delivery_attempts
SET outcome='MAYBE_SENT',retryable=false,error_code='LEASE_EXPIRED_OUTCOME_UNKNOWN',completed_at=$3,lease_owner=NULL,lease_until=NULL
WHERE tenant_id=$1::uuid AND id=$2::uuid AND completed_at IS NULL`, scope.TenantID, item.attemptID, now.UTC())
		if err != nil {
			return 0, err
		}
		_, err = tx.Exec(ctx, `UPDATE outbound_delivery.delivery_intents
SET state='OUTCOME_UNKNOWN',next_retry_at=NULL,lease_owner=NULL,lease_until=NULL,updated_at=$3
WHERE tenant_id=$1::uuid AND id=$2::uuid AND state='LEASED'`, scope.TenantID, item.intentID, now.UTC())
		if err != nil {
			return 0, err
		}
		if err = insertDeadLetter(ctx, tx, scope.TenantID, item.intentID, item.attemptID, "LEASE_EXPIRED_OUTCOME_UNKNOWN", true, now.UTC()); err != nil {
			return 0, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, err
	}
	return len(expired), nil
}

func (store *PostgresStore) ApproveReplay(ctx context.Context, request ReplayRequest) (ReplayApproval, error) {
	if strings.TrimSpace(request.TenantID) == "" || strings.TrimSpace(request.DeadLetterID) == "" ||
		strings.TrimSpace(request.ApprovedByPrincipal) == "" || strings.TrimSpace(request.Reason) == "" {
		return ReplayApproval{}, errors.New("tenant, dead letter, approver and replay reason are required")
	}
	tx, err := store.beginTenant(ctx, request.TenantID)
	if err != nil {
		return ReplayApproval{}, err
	}
	defer tx.Rollback(ctx)

	var intentID string
	var requiresRiskAck bool
	var state IntentState
	err = tx.QueryRow(ctx, `SELECT d.intent_id::text,d.requires_duplicate_risk_ack,i.state
FROM outbound_delivery.dead_letters d
JOIN outbound_delivery.delivery_intents i ON i.tenant_id=d.tenant_id AND i.id=d.intent_id
WHERE d.tenant_id=$1::uuid AND d.id=$2::uuid
FOR UPDATE OF i`, request.TenantID, request.DeadLetterID).Scan(&intentID, &requiresRiskAck, &state)
	if err != nil {
		return ReplayApproval{}, err
	}
	if state != IntentDead && state != IntentOutcomeUnknown {
		return ReplayApproval{}, errors.New("delivery intent is not replayable")
	}
	if requiresRiskAck && !request.AcceptDuplicateRisk {
		return ReplayApproval{}, ErrReplayRiskRequired
	}
	approvalID, err := uuidv7(request.ApprovedAt.UTC())
	if err != nil {
		return ReplayApproval{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO outbound_delivery.replay_approvals
  (id,tenant_id,dead_letter_id,intent_id,approved_by_principal_id,reason,accept_duplicate_risk,created_at)
VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,$7,$8)`,
		approvalID, request.TenantID, request.DeadLetterID, intentID, request.ApprovedByPrincipal, request.Reason,
		request.AcceptDuplicateRisk, request.ApprovedAt.UTC())
	if err != nil {
		return ReplayApproval{}, err
	}
	_, err = tx.Exec(ctx, `UPDATE outbound_delivery.delivery_intents
SET state='READY',next_retry_at=NULL,lease_owner=NULL,lease_until=NULL,updated_at=$3
WHERE tenant_id=$1::uuid AND id=$2::uuid`, request.TenantID, intentID, request.ApprovedAt.UTC())
	if err != nil {
		return ReplayApproval{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return ReplayApproval{}, err
	}
	return ReplayApproval{
		ID: approvalID, DeadLetterID: request.DeadLetterID, IntentID: intentID,
		ApprovedByPrincipal: request.ApprovedByPrincipal, Reason: request.Reason,
		AcceptDuplicateRisk: request.AcceptDuplicateRisk, CreatedAt: request.ApprovedAt.UTC(),
	}, nil
}

func (store *PostgresStore) beginTenant(ctx context.Context, tenantID string) (pgx.Tx, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, errors.New("tenant id is required")
	}
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `SELECT set_config('app.tenant_id',$1,true)`, tenantID); err != nil {
		tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}

func loadIntentByIdempotency(ctx context.Context, tx pgx.Tx, tenantID, integrationID, key string) (DeliveryIntent, error) {
	return scanIntent(tx.QueryRow(ctx, `SELECT id::text,tenant_id::text,COALESCE(site_id::text,''),integration_id::text,purpose,payload_schema,payload::text,
  payload_digest,idempotency_key,source_aggregate_type,source_aggregate_id,classification,state,attempt_count,next_retry_at,created_at,updated_at
FROM outbound_delivery.delivery_intents
WHERE tenant_id=$1::uuid AND integration_id=$2::uuid AND idempotency_key=$3`, tenantID, integrationID, key))
}

func loadIntentByID(ctx context.Context, tx pgx.Tx, tenantID, intentID string) (DeliveryIntent, error) {
	return scanIntent(tx.QueryRow(ctx, `SELECT id::text,tenant_id::text,COALESCE(site_id::text,''),integration_id::text,purpose,payload_schema,payload::text,
  payload_digest,idempotency_key,source_aggregate_type,source_aggregate_id,classification,state,attempt_count,next_retry_at,created_at,updated_at
FROM outbound_delivery.delivery_intents
WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, intentID))
}

func scanIntent(row pgx.Row) (DeliveryIntent, error) {
	var intent DeliveryIntent
	var payload []byte
	err := row.Scan(&intent.ID, &intent.TenantID, &intent.SiteID, &intent.IntegrationID, &intent.Purpose, &intent.PayloadSchema,
		&payload, &intent.PayloadDigest, &intent.IdempotencyKey, &intent.SourceAggregateType, &intent.SourceAggregateID,
		&intent.Classification, &intent.State, &intent.AttemptCount, &intent.NextRetryAt, &intent.CreatedAt, &intent.UpdatedAt)
	intent.Payload = append([]byte(nil), payload...)
	return intent, err
}

func insertDeadLetter(ctx context.Context, tx pgx.Tx, tenantID, intentID, attemptID, reason string, duplicateRisk bool, now time.Time) error {
	deadLetterID, err := uuidv7(now)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO outbound_delivery.dead_letters
  (id,tenant_id,intent_id,attempt_id,reason_code,requires_duplicate_risk_ack,created_at)
VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6,$7)`, deadLetterID, tenantID, intentID, attemptID, reason, duplicateRisk, now.UTC())
	return err
}

func deadReason(result AdapterResult) string {
	if result.ErrorCode != "" {
		return result.ErrorCode
	}
	return string(result.Outcome)
}

func nilIfEmpty(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func nilIfZero(value int) any {
	if value == 0 {
		return nil
	}
	return value
}

func nilUUID(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func uuidv7(now time.Time) (string, error) {
	if now.IsZero() {
		return "", errors.New("uuidv7 timestamp is required")
	}
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate uuidv7 randomness: %w", err)
	}
	milliseconds := uint64(now.UnixMilli())
	buffer[0], buffer[1], buffer[2], buffer[3], buffer[4], buffer[5] = byte(milliseconds>>40), byte(milliseconds>>32), byte(milliseconds>>24), byte(milliseconds>>16), byte(milliseconds>>8), byte(milliseconds)
	buffer[6] = (buffer[6] & 0x0f) | 0x70
	buffer[8] = (buffer[8] & 0x3f) | 0x80
	hexValue := hex.EncodeToString(buffer)
	return hexValue[0:8] + "-" + hexValue[8:12] + "-" + hexValue[12:16] + "-" + hexValue[16:20] + "-" + hexValue[20:32], nil
}
