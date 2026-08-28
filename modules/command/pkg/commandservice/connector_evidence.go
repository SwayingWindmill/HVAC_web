package commandservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

func (store *PostgresStore) PrepareConnectorEvidence(ctx context.Context, evidence commandmodel.PreparedConnectorEvidence) error {
	if store == nil || store.pool == nil {
		return errors.New("command store is closed")
	}
	if !validPreparedConnectorEvidence(evidence) {
		return ErrInvalidRequest
	}
	evidence.PreparedAt = evidence.PreparedAt.UTC().Truncate(time.Microsecond)
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin connector evidence preparation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := activateTenant(ctx, tx, evidence.TenantID); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `
INSERT INTO command_runtime.connector_evidence (
  attempt_id, execution_fence, command_id, tenant_id, site_id, device_id,
  external_device_id, payload_hash, mapping_revision, binding_revision,
  provider_endpoint, provider_method, request_sha256, prepared_at
)
SELECT
  $1::uuid, $2, $3::uuid, $4::uuid, $5::uuid, $6::uuid,
  $7, $8, $9, $10, $11, $12, $13, $14
FROM command_runtime.command_attempts AS attempt
WHERE attempt.attempt_id = $1::uuid
  AND attempt.execution_fence = $2
  AND attempt.command_id = $3::uuid
  AND attempt.tenant_id = $4::uuid
  AND attempt.site_id = $5::uuid
  AND attempt.device_id = $6::uuid
  AND attempt.payload_hash = $8
  AND attempt.status = 'PREPARED'
ON CONFLICT (attempt_id, execution_fence) DO NOTHING
`, evidence.AttemptID, evidence.ExecutionFence, evidence.CommandID, evidence.TenantID,
		evidence.SiteID, evidence.DeviceID, evidence.ExternalDeviceID, evidence.PayloadHash,
		evidence.MappingRevision, evidence.BindingRevision, evidence.ProviderEndpoint,
		evidence.ProviderMethod, evidence.RequestSHA256, evidence.PreparedAt)
	if err != nil {
		return fmt.Errorf("insert prepared connector evidence: %w", err)
	}
	if tag.RowsAffected() == 0 {
		existing, err := loadPreparedConnectorEvidence(ctx, tx, evidence.TenantID, evidence.AttemptID, evidence.ExecutionFence)
		if errors.Is(err, ErrCommandNotFound) {
			return ErrStaleFence
		}
		if err != nil {
			return err
		}
		if !samePreparedConnectorEvidence(existing, evidence) {
			return ErrStaleFence
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit connector evidence preparation: %w", err)
	}
	return nil
}

func (store *PostgresStore) CompleteConnectorEvidence(ctx context.Context, evidence commandmodel.CompletedConnectorEvidence) error {
	if store == nil || store.pool == nil {
		return errors.New("command store is closed")
	}
	if !validCompletedConnectorEvidence(evidence) {
		return ErrInvalidRequest
	}
	evidence.PreparedAt = evidence.PreparedAt.UTC().Truncate(time.Microsecond)
	evidence.CompletedAt = evidence.CompletedAt.UTC().Truncate(time.Microsecond)
	edgeExecutionJSON, err := marshalEdgeExecution(evidence.EdgeExecution)
	if err != nil {
		return ErrInvalidRequest
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin connector evidence completion: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := activateTenant(ctx, tx, evidence.TenantID); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `
UPDATE command_runtime.connector_evidence
SET provider_status_code = $15,
    response_sha256 = $16,
    request_written = $17,
    connector_phase = $18,
    failure_code = NULLIF($19, ''),
    completed_at = $20,
    edge_execution_evidence = $21::jsonb
WHERE attempt_id = $1::uuid
  AND execution_fence = $2
  AND command_id = $3::uuid
  AND tenant_id = $4::uuid
  AND site_id = $5::uuid
  AND device_id = $6::uuid
  AND external_device_id = $7
  AND payload_hash = $8
  AND mapping_revision = $9
  AND binding_revision = $10
  AND provider_endpoint = $11
  AND provider_method = $12
  AND request_sha256 = $13
  AND prepared_at = $14
  AND completed_at IS NULL
`, evidence.AttemptID, evidence.ExecutionFence, evidence.CommandID, evidence.TenantID,
		evidence.SiteID, evidence.DeviceID, evidence.ExternalDeviceID, evidence.PayloadHash,
		evidence.MappingRevision, evidence.BindingRevision, evidence.ProviderEndpoint,
		evidence.ProviderMethod, evidence.RequestSHA256, evidence.PreparedAt,
		nullableProviderStatus(evidence.ProviderStatusCode), evidence.ResponseSHA256, evidence.RequestWritten,
		evidence.ConnectorPhase, evidence.FailureCode, evidence.CompletedAt, nullableEdgeExecution(edgeExecutionJSON))
	if err != nil {
		return fmt.Errorf("complete connector evidence: %w", err)
	}
	if tag.RowsAffected() == 0 {
		existing, err := loadCompletedConnectorEvidence(ctx, tx, evidence.TenantID, evidence.AttemptID, evidence.ExecutionFence)
		if err != nil {
			return err
		}
		if !sameCompletedConnectorEvidence(existing, evidence) {
			return ErrStaleFence
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit connector evidence completion: %w", err)
	}
	return nil
}

func validPreparedConnectorEvidence(evidence commandmodel.PreparedConnectorEvidence) bool {
	return strings.TrimSpace(evidence.AttemptID) != "" && strings.TrimSpace(evidence.CommandID) != "" &&
		strings.TrimSpace(evidence.TenantID) != "" && strings.TrimSpace(evidence.SiteID) != "" &&
		strings.TrimSpace(evidence.DeviceID) != "" && strings.TrimSpace(evidence.ExternalDeviceID) != "" &&
		evidence.ExecutionFence > 0 && strings.TrimSpace(evidence.PayloadHash) != "" &&
		strings.TrimSpace(evidence.MappingRevision) != "" && strings.TrimSpace(evidence.BindingRevision) != "" &&
		strings.TrimSpace(evidence.ProviderEndpoint) != "" && strings.TrimSpace(evidence.ProviderMethod) != "" &&
		strings.TrimSpace(evidence.RequestSHA256) != "" && !evidence.PreparedAt.IsZero()
}

func validCompletedConnectorEvidence(evidence commandmodel.CompletedConnectorEvidence) bool {
	if !validPreparedConnectorEvidence(evidence.PreparedConnectorEvidence) || evidence.CompletedAt.IsZero() || evidence.CompletedAt.Before(evidence.PreparedAt) {
		return false
	}
	if evidence.EdgeExecution != nil && !evidence.EdgeExecution.Valid() {
		return false
	}
	switch evidence.ConnectorPhase {
	case commandmodel.ConnectorPreSendRejected:
		return !evidence.RequestWritten && evidence.ProviderStatusCode == 0 && strings.TrimSpace(evidence.ResponseSHA256) == "" && evidence.EdgeExecution == nil
	case commandmodel.ConnectorExecutionRejected:
		return evidence.RequestWritten && evidence.ProviderStatusCode == 200 && strings.TrimSpace(evidence.ResponseSHA256) != "" && strings.TrimSpace(evidence.FailureCode) != ""
	case commandmodel.ConnectorRequestCommitted:
		return evidence.RequestWritten
	case commandmodel.ConnectorAcknowledged:
		return evidence.RequestWritten && evidence.ProviderStatusCode == 200 && strings.TrimSpace(evidence.ResponseSHA256) != "" && strings.TrimSpace(evidence.FailureCode) == "" && evidence.EdgeExecution != nil && evidence.EdgeExecution.ValidExecuted()
	default:
		return false
	}
}

func loadPreparedConnectorEvidence(ctx context.Context, tx pgx.Tx, organizationID, attemptID string, fence uint64) (commandmodel.PreparedConnectorEvidence, error) {
	var evidence commandmodel.PreparedConnectorEvidence
	err := tx.QueryRow(ctx, `
SELECT attempt_id::text, command_id::text, tenant_id::text, site_id::text, device_id::text,
       external_device_id, execution_fence, payload_hash, mapping_revision, binding_revision,
       provider_endpoint, provider_method, request_sha256, prepared_at
FROM command_runtime.connector_evidence
WHERE tenant_id = $1::uuid AND attempt_id = $2::uuid AND execution_fence = $3
`, organizationID, attemptID, fence).Scan(
		&evidence.AttemptID, &evidence.CommandID, &evidence.TenantID, &evidence.SiteID, &evidence.DeviceID,
		&evidence.ExternalDeviceID, &evidence.ExecutionFence, &evidence.PayloadHash, &evidence.MappingRevision,
		&evidence.BindingRevision, &evidence.ProviderEndpoint, &evidence.ProviderMethod, &evidence.RequestSHA256,
		&evidence.PreparedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return commandmodel.PreparedConnectorEvidence{}, ErrCommandNotFound
	}
	if err != nil {
		return commandmodel.PreparedConnectorEvidence{}, fmt.Errorf("load prepared connector evidence: %w", err)
	}
	return evidence, nil
}

func loadCompletedConnectorEvidence(ctx context.Context, tx pgx.Tx, organizationID, attemptID string, fence uint64) (commandmodel.CompletedConnectorEvidence, error) {
	prepared, err := loadPreparedConnectorEvidence(ctx, tx, organizationID, attemptID, fence)
	if err != nil {
		return commandmodel.CompletedConnectorEvidence{}, err
	}
	var status *int
	var response, phase, failure *string
	var written *bool
	var completedAt *time.Time
	var edgeExecutionJSON []byte
	err = tx.QueryRow(ctx, `
SELECT provider_status_code, response_sha256, request_written, connector_phase, failure_code, completed_at, edge_execution_evidence
FROM command_runtime.connector_evidence
WHERE tenant_id = $1::uuid AND attempt_id = $2::uuid AND execution_fence = $3
`, organizationID, attemptID, fence).Scan(&status, &response, &written, &phase, &failure, &completedAt, &edgeExecutionJSON)
	if err != nil {
		return commandmodel.CompletedConnectorEvidence{}, fmt.Errorf("load completed connector evidence: %w", err)
	}
	if completedAt == nil || phase == nil || written == nil {
		return commandmodel.CompletedConnectorEvidence{}, ErrStaleFence
	}
	completed := commandmodel.CompletedConnectorEvidence{PreparedConnectorEvidence: prepared, RequestWritten: *written, ConnectorPhase: commandmodel.ConnectorPhase(*phase), CompletedAt: *completedAt}
	if status != nil {
		completed.ProviderStatusCode = *status
	}
	if response != nil {
		completed.ResponseSHA256 = *response
	}
	if failure != nil {
		completed.FailureCode = *failure
	}
	if len(edgeExecutionJSON) > 0 {
		var edge commandmodel.EdgeExecutionEvidence
		if err := json.Unmarshal(edgeExecutionJSON, &edge); err != nil || !edge.Valid() {
			return commandmodel.CompletedConnectorEvidence{}, ErrInvalidRequest
		}
		completed.EdgeExecution = &edge
	}
	return completed, nil
}

func samePreparedConnectorEvidence(left, right commandmodel.PreparedConnectorEvidence) bool {
	return left.AttemptID == right.AttemptID && left.CommandID == right.CommandID && left.TenantID == right.TenantID &&
		left.SiteID == right.SiteID && left.DeviceID == right.DeviceID && left.ExternalDeviceID == right.ExternalDeviceID &&
		left.ExecutionFence == right.ExecutionFence && left.PayloadHash == right.PayloadHash &&
		left.MappingRevision == right.MappingRevision && left.BindingRevision == right.BindingRevision &&
		left.ProviderEndpoint == right.ProviderEndpoint && left.ProviderMethod == right.ProviderMethod &&
		left.RequestSHA256 == right.RequestSHA256 && left.PreparedAt.Equal(right.PreparedAt)
}

func sameCompletedConnectorEvidence(left, right commandmodel.CompletedConnectorEvidence) bool {
	return samePreparedConnectorEvidence(left.PreparedConnectorEvidence, right.PreparedConnectorEvidence) &&
		left.ProviderStatusCode == right.ProviderStatusCode && left.ResponseSHA256 == right.ResponseSHA256 &&
		left.RequestWritten == right.RequestWritten && left.ConnectorPhase == right.ConnectorPhase &&
		left.FailureCode == right.FailureCode && left.CompletedAt.Equal(right.CompletedAt) && sameEdgeExecutionEvidence(left.EdgeExecution, right.EdgeExecution)
}

func marshalEdgeExecution(evidence *commandmodel.EdgeExecutionEvidence) ([]byte, error) {
	if evidence == nil {
		return nil, nil
	}
	if !evidence.Valid() {
		return nil, ErrInvalidRequest
	}
	return json.Marshal(evidence)
}

func nullableEdgeExecution(raw []byte) any {
	if len(raw) == 0 {
		return nil
	}
	return string(raw)
}

func sameEdgeExecutionEvidence(left, right *commandmodel.EdgeExecutionEvidence) bool {
	leftJSON, leftErr := marshalEdgeExecution(left)
	rightJSON, rightErr := marshalEdgeExecution(right)
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
}

func nullableProviderStatus(status int) any {
	if status == 0 {
		return nil
	}
	return status
}
