package connectivity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

const (
	FactoryResetStatusPending      = "PENDING"
	FactoryResetStatusDispatched   = "DISPATCHED"
	FactoryResetStatusAcknowledged = "ACKNOWLEDGED"
	FactoryResetStatusFailed       = "FAILED"

	factoryResetColumns = `id::text, tenant_id::text, gateway_id::text, transfer_id::text, wipe_credentials, wipe_config_shadow, wipe_offline_buffer, reason, status, issued_by::text, dispatched_at, acknowledged_at, wiped_artifacts, error_message, created_at, updated_at`
)

// GatewayFactoryResetRecord tracks a downlink wipe instruction and edge wipe acknowledgement.
type GatewayFactoryResetRecord struct {
	ID                string     `json:"id"`
	TenantID          string     `json:"tenantId"`
	GatewayID         string     `json:"gatewayId"`
	TransferID        *string    `json:"transferId,omitempty"`
	WipeCredentials   bool       `json:"wipeCredentials"`
	WipeConfigShadow  bool       `json:"wipeConfigShadow"`
	WipeOfflineBuffer bool       `json:"wipeOfflineBuffer"`
	Reason            string     `json:"reason"`
	Status            string     `json:"status"`
	IssuedBy          string     `json:"issuedBy"`
	DispatchedAt      *time.Time `json:"dispatchedAt,omitempty"`
	AcknowledgedAt    *time.Time `json:"acknowledgedAt,omitempty"`
	WipedArtifacts    []string   `json:"wipedArtifacts"`
	ErrorMessage      *string    `json:"errorMessage,omitempty"`
	CreatedAt         time.Time  `json:"createdAt"`
	UpdatedAt         time.Time  `json:"updatedAt"`
}

// IssueFactoryResetInput configures an operator-triggered edge factory-reset command.
type IssueFactoryResetInput struct {
	WipeCredentials   *bool  `json:"wipeCredentials,omitempty"`
	WipeConfigShadow  *bool  `json:"wipeConfigShadow,omitempty"`
	WipeOfflineBuffer *bool  `json:"wipeOfflineBuffer,omitempty"`
	RevokeCredentials *bool  `json:"revokeCredentials,omitempty"`
	Reason            string `json:"reason"`
}

// DecommissionTransferInput triggers immediate revocation and optional edge wipe upon ownership transfer.
type DecommissionTransferInput struct {
	TenantID     string `json:"tenantId"`
	TransferID   string `json:"transferId"`
	WipeRequired *bool  `json:"wipeRequired,omitempty"`
	Reason       string `json:"reason"`
	ActorID      string `json:"actorId,omitempty"`
}

// DecommissionTransferResult reports whether old-tenant credentials were revoked and the reset instruction created.
type DecommissionTransferResult struct {
	GatewayID          string                     `json:"gatewayId"`
	TransferID         string                     `json:"transferId"`
	CredentialsRevoked bool                       `json:"credentialsRevoked"`
	FactoryReset       *GatewayFactoryResetRecord `json:"factoryReset,omitempty"`
}

func boolPtrDefault(ptr *bool, fallback bool) bool {
	if ptr == nil {
		return fallback
	}
	return *ptr
}

// IssueFactoryReset persists and dispatches an edge factory-reset instruction and (by default) revokes credentials.
func (service *CredentialService) IssueFactoryReset(
	ctx context.Context,
	claims registryauth.GrantClaims,
	gatewayID string,
	input IssueFactoryResetInput,
) (GatewayFactoryResetRecord, error) {
	if service == nil || service.store == nil || service.store.pool == nil {
		return GatewayFactoryResetRecord{}, errors.New("credential service is not configured")
	}
	gateway := strings.ToLower(strings.TrimSpace(gatewayID))
	if !gatewayIDPattern.MatchString(gateway) || !gatewayIDPattern.MatchString(claims.PrincipalID) {
		return GatewayFactoryResetRecord{}, ErrEnrollmentRejected
	}
	trimmedReason := strings.TrimSpace(input.Reason)
	if trimmedReason == "" || utf8.RuneCountInString(trimmedReason) > 512 {
		return GatewayFactoryResetRecord{}, ErrEnrollmentRejected
	}
	wipeCreds := boolPtrDefault(input.WipeCredentials, true)
	wipeShadow := boolPtrDefault(input.WipeConfigShadow, true)
	wipeOffline := boolPtrDefault(input.WipeOfflineBuffer, true)
	if !wipeCreds && !wipeShadow && !wipeOffline {
		return GatewayFactoryResetRecord{}, ErrEnrollmentRejected
	}
	revokeCreds := boolPtrDefault(input.RevokeCredentials, true)

	tx, err := service.operatorForDirectoryOrGateway(ctx, claims, gateway)
	if err != nil {
		return GatewayFactoryResetRecord{}, err
	}
	defer tx.Rollback(ctx)

	now := service.store.clock().UTC()
	resetID, err := newUUIDv7(now)
	if err != nil {
		return GatewayFactoryResetRecord{}, err
	}
	row := tx.QueryRow(ctx, `
INSERT INTO connectivity.gateway_factory_resets (
  id, tenant_id, gateway_id, transfer_id,
  wipe_credentials, wipe_config_shadow, wipe_offline_buffer,
  reason, status, issued_by, wiped_artifacts, created_at, updated_at
) VALUES (
  $1::uuid, $2::uuid, $3::uuid, NULL,
  $4, $5, $6,
  $7, 'PENDING', $8::uuid, '[]'::jsonb, $9, $9
)
RETURNING `+factoryResetColumns,
		resetID, claims.TenantID, gateway,
		wipeCreds, wipeShadow, wipeOffline,
		trimmedReason, claims.PrincipalID, now,
	)
	record, err := scanFactoryResetRecord(row)
	if err != nil {
		return GatewayFactoryResetRecord{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return GatewayFactoryResetRecord{}, err
	}

	// Dispatch before revoking active credentials so an online edge node receives the MQTT command.
	record = service.dispatchFactoryResetBestEffort(ctx, claims.TenantID, record, now)

	if revokeCreds {
		revokeTx, err := service.beginTenant(ctx, claims.TenantID)
		if err != nil {
			return GatewayFactoryResetRecord{}, err
		}
		defer revokeTx.Rollback(ctx)
		if err := service.revokeGatewayWithAuditInTx(ctx, revokeTx, claims.TenantID, gateway, claims.PrincipalID, "REVOKED", "factory_reset:"+record.ID); err != nil {
			return GatewayFactoryResetRecord{}, err
		}
	}
	return record, nil
}

// ListFactoryResets returns factory-reset instructions issued for gatewayID within the caller's tenant scope.
func (service *CredentialService) ListFactoryResets(
	ctx context.Context,
	claims registryauth.GrantClaims,
	gatewayID string,
) ([]GatewayFactoryResetRecord, error) {
	if service == nil || service.store == nil || service.store.pool == nil {
		return nil, errors.New("credential service is not configured")
	}
	gateway := strings.ToLower(strings.TrimSpace(gatewayID))
	tx, err := service.operatorForDirectoryOrGateway(ctx, claims, gateway)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `
SELECT `+factoryResetColumns+`
FROM connectivity.gateway_factory_resets
WHERE tenant_id = $1::uuid AND gateway_id = $2::uuid
ORDER BY created_at DESC, id DESC
LIMIT 100
`, claims.TenantID, gateway)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := make([]GatewayFactoryResetRecord, 0)
	for rows.Next() {
		rec, scanErr := scanFactoryResetRecord(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		records = append(records, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

func (service *CredentialService) operatorForDirectoryOrGateway(ctx context.Context, claims registryauth.GrantClaims, gatewayID string) (pgx.Tx, error) {
	if !gatewayIDPattern.MatchString(gatewayID) {
		return nil, ErrEnrollmentRejected
	}
	var tenant, site string
	err := service.store.pool.QueryRow(ctx, `SELECT tenant_id::text, site_id::text FROM core_registry.gateway_directory_v1 WHERE gateway_id=$1::uuid`, gatewayID).Scan(&tenant, &site)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if tenant != claims.TenantID || !registryauth.ScopeAllows(claims, site) {
		return nil, ErrCredentialForbidden
	}
	return service.beginTenant(ctx, tenant)
}

// DecommissionTransferredGateway dispatches an optional edge factory-reset wipe and immediately revokes all
// old-tenant credentials for a transferred GATEWAY or DIRECT device (idempotent by transferId).
func (service *CredentialService) DecommissionTransferredGateway(
	ctx context.Context,
	gatewayID string,
	input DecommissionTransferInput,
) (DecommissionTransferResult, error) {
	if service == nil || service.store == nil || service.store.pool == nil {
		return DecommissionTransferResult{}, errors.New("credential service is not configured")
	}
	tenant := strings.ToLower(strings.TrimSpace(input.TenantID))
	gateway := strings.ToLower(strings.TrimSpace(gatewayID))
	transferID := strings.ToLower(strings.TrimSpace(input.TransferID))
	actor := strings.ToLower(strings.TrimSpace(input.ActorID))
	if actor == "" {
		actor = gateway
	}
	if !gatewayIDPattern.MatchString(tenant) || !gatewayIDPattern.MatchString(gateway) ||
		!gatewayIDPattern.MatchString(transferID) || !gatewayIDPattern.MatchString(actor) {
		return DecommissionTransferResult{}, ErrEnrollmentRejected
	}

	var resolvedTenant string
	err := service.store.pool.QueryRow(ctx, `SELECT tenant_id::text FROM core_registry.gateway_directory_v1 WHERE gateway_id=$1::uuid LIMIT 1`, gateway).Scan(&resolvedTenant)
	if errors.Is(err, pgx.ErrNoRows) {
		return DecommissionTransferResult{}, ErrNotFound
	}
	if err != nil {
		return DecommissionTransferResult{}, err
	}
	if strings.TrimSpace(resolvedTenant) != tenant {
		return DecommissionTransferResult{}, ErrCredentialForbidden
	}

	wipeRequired := boolPtrDefault(input.WipeRequired, true)
	reason := clampUTF8Runes(strings.TrimSpace(input.Reason), 512)
	if reason == "" {
		reason = "ownership_transfer:" + transferID
	}

	var resetRecordPtr *GatewayFactoryResetRecord
	if wipeRequired {
		now := service.store.clock().UTC()
		resetID, err := newUUIDv7(now)
		if err != nil {
			return DecommissionTransferResult{}, err
		}
		tx, err := service.beginTenant(ctx, tenant)
		if err != nil {
			return DecommissionTransferResult{}, err
		}
		defer tx.Rollback(ctx)
		if _, err := tx.Exec(ctx, `
INSERT INTO connectivity.gateway_factory_resets (
  id, tenant_id, gateway_id, transfer_id,
  wipe_credentials, wipe_config_shadow, wipe_offline_buffer,
  reason, status, issued_by, wiped_artifacts, created_at, updated_at
) VALUES (
  $1::uuid, $2::uuid, $3::uuid, $4::uuid,
  true, true, true,
  $5, 'PENDING', $6::uuid, '[]'::jsonb, $7, $7
)
ON CONFLICT (tenant_id, gateway_id, transfer_id) WHERE transfer_id IS NOT NULL DO NOTHING
`, resetID, tenant, gateway, transferID, reason, actor, now); err != nil {
			return DecommissionTransferResult{}, err
		}
		row := tx.QueryRow(ctx, `
SELECT `+factoryResetColumns+`
FROM connectivity.gateway_factory_resets
WHERE tenant_id = $1::uuid AND gateway_id = $2::uuid AND transfer_id = $3::uuid
`, tenant, gateway, transferID)
		record, err := scanFactoryResetRecord(row)
		if err != nil {
			return DecommissionTransferResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return DecommissionTransferResult{}, err
		}
		if record.Status == FactoryResetStatusPending {
			record = service.dispatchFactoryResetBestEffort(ctx, tenant, record, now)
		}
		resetRecordPtr = &record
	}

	revokeTx, err := service.beginTenant(ctx, tenant)
	if err != nil {
		return DecommissionTransferResult{}, err
	}
	defer revokeTx.Rollback(ctx)
	if err := service.revokeGatewayWithAuditInTx(ctx, revokeTx, tenant, gateway, actor, "TRANSFER_REVOKED", "OWNERSHIP_TRANSFER:"+transferID); err != nil {
		return DecommissionTransferResult{}, err
	}

	return DecommissionTransferResult{
		GatewayID:          gateway,
		TransferID:         transferID,
		CredentialsRevoked: true,
		FactoryReset:       resetRecordPtr,
	}, nil
}

func (service *CredentialService) dispatchFactoryResetBestEffort(
	ctx context.Context,
	tenantID string,
	record GatewayFactoryResetRecord,
	now time.Time,
) GatewayFactoryResetRecord {
	service.revokeMu.RLock()
	hook := service.onFactoryResetDispatch
	service.revokeMu.RUnlock()
	if hook == nil {
		return record
	}
	payload := edgecontrol.FactoryResetPayload{
		ResetID:           record.ID,
		WipeCredentials:   record.WipeCredentials,
		WipeConfigShadow:  record.WipeConfigShadow,
		WipeOfflineBuffer: record.WipeOfflineBuffer,
		Reason:            record.Reason,
		IssuedAt:          max(now.UTC().UnixMilli(), 1),
	}
	if record.TransferID != nil {
		payload.TransferID = *record.TransferID
	}
	if err := hook(record.GatewayID, payload); err != nil {
		return record
	}
	dispatchedAt := service.store.clock().UTC()
	tx, err := service.beginTenant(ctx, tenantID)
	if err != nil {
		return record
	}
	defer tx.Rollback(ctx)
	row := tx.QueryRow(ctx, `
UPDATE connectivity.gateway_factory_resets
SET status = 'DISPATCHED',
    dispatched_at = COALESCE(dispatched_at, $4),
    updated_at = $4
WHERE tenant_id = $1::uuid AND id = $2::uuid AND gateway_id = $3::uuid AND status = 'PENDING'
RETURNING `+factoryResetColumns, tenantID, record.ID, record.GatewayID, dispatchedAt)
	updated, scanErr := scanFactoryResetRecord(row)
	if scanErr == nil {
		if commitErr := tx.Commit(ctx); commitErr == nil {
			return updated
		}
	}
	return record
}

// RecordFactoryResetAck records an edge FACTORY_RESET_ACK against an open PENDING/DISPATCHED instruction.
func (store *Store) RecordFactoryResetAck(
	ctx context.Context,
	gatewayID string,
	ack edgecontrol.FactoryResetAckPayload,
) (GatewayFactoryResetRecord, error) {
	if store == nil || store.pool == nil {
		return GatewayFactoryResetRecord{}, errors.New("connectivity store is not configured")
	}
	gateway := strings.ToLower(strings.TrimSpace(gatewayID))
	if !gatewayIDPattern.MatchString(gateway) {
		return GatewayFactoryResetRecord{}, ErrEnrollmentRejected
	}
	if err := ack.Validate(); err != nil {
		return GatewayFactoryResetRecord{}, fmt.Errorf("%w: %v", ErrEnrollmentRejected, err)
	}
	resetID := strings.ToLower(strings.TrimSpace(ack.ResetID))
	var tenantIDPtr *string
	if err := store.pool.QueryRow(ctx, `SELECT connectivity.resolve_factory_reset_tenant($1::uuid, $2::uuid)::text`, resetID, gateway).Scan(&tenantIDPtr); err != nil {
		return GatewayFactoryResetRecord{}, err
	}
	if tenantIDPtr == nil || strings.TrimSpace(*tenantIDPtr) == "" {
		return GatewayFactoryResetRecord{}, ErrNotFound
	}
	tenantID := strings.TrimSpace(*tenantIDPtr)

	nextStatus := FactoryResetStatusAcknowledged
	if ack.Status == edgecontrol.FactoryResetAckStatusFailed {
		nextStatus = FactoryResetStatusFailed
	}
	artifactsBytes, err := json.Marshal(ack.WipedArtifacts)
	if err != nil {
		return GatewayFactoryResetRecord{}, fmt.Errorf("%w: %v", ErrEnrollmentRejected, err)
	}
	errMsg := optionalTrimmedPtr(ack.ErrorMessage, 1024)
	now := store.clock().UTC()

	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return GatewayFactoryResetRecord{}, err
	}
	defer tx.Rollback(ctx)
	if err := setTenant(ctx, tx, tenantID); err != nil {
		return GatewayFactoryResetRecord{}, err
	}
	row := tx.QueryRow(ctx, `
UPDATE connectivity.gateway_factory_resets
SET status = $4,
    acknowledged_at = $5,
    wiped_artifacts = $6::jsonb,
    error_message = $7,
    updated_at = $5
WHERE tenant_id = $1::uuid AND id = $2::uuid AND gateway_id = $3::uuid AND status IN ('PENDING', 'DISPATCHED')
RETURNING `+factoryResetColumns, tenantID, resetID, gateway, nextStatus, now, string(artifactsBytes), errMsg)
	record, err := scanFactoryResetRecord(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return GatewayFactoryResetRecord{}, ErrNotFound
	}
	if err != nil {
		return GatewayFactoryResetRecord{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return GatewayFactoryResetRecord{}, err
	}
	return record, nil
}

func scanFactoryResetRecord(row rowScanner) (GatewayFactoryResetRecord, error) {
	var rec GatewayFactoryResetRecord
	var dispatchedAt, acknowledgedAt *time.Time
	var wipedRaw []byte
	err := row.Scan(
		&rec.ID, &rec.TenantID, &rec.GatewayID, &rec.TransferID,
		&rec.WipeCredentials, &rec.WipeConfigShadow, &rec.WipeOfflineBuffer,
		&rec.Reason, &rec.Status, &rec.IssuedBy,
		&dispatchedAt, &acknowledgedAt, &wipedRaw, &rec.ErrorMessage,
		&rec.CreatedAt, &rec.UpdatedAt,
	)
	if err != nil {
		return GatewayFactoryResetRecord{}, err
	}
	rec.WipedArtifacts = []string{}
	if len(wipedRaw) > 0 {
		if err := json.Unmarshal(wipedRaw, &rec.WipedArtifacts); err != nil {
			return GatewayFactoryResetRecord{}, fmt.Errorf("decode wipedArtifacts: %w", err)
		}
		if rec.WipedArtifacts == nil {
			rec.WipedArtifacts = []string{}
		}
	}
	rec.CreatedAt = rec.CreatedAt.UTC()
	rec.UpdatedAt = rec.UpdatedAt.UTC()
	if dispatchedAt != nil {
		t := dispatchedAt.UTC()
		rec.DispatchedAt = &t
	}
	if acknowledgedAt != nil {
		t := acknowledgedAt.UTC()
		rec.AcknowledgedAt = &t
	}
	return rec, nil
}
