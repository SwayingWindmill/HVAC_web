package commandservice

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/commandauth"
)

func (store *PostgresStore) ConsumeCommandGrant(ctx context.Context, claims commandauth.GrantClaims, currentPolicyRevision string, currentRevocationRevision uint64) (commandauth.UseStatus, error) {
	if store == nil || store.pool == nil {
		return commandauth.UseStatus{}, errors.New("command store is closed")
	}
	if strings.TrimSpace(claims.TokenID) == "" || strings.TrimSpace(claims.GrantID) == "" || strings.TrimSpace(claims.TenantID) == "" ||
		strings.TrimSpace(currentPolicyRevision) == "" {
		return commandauth.UseStatus{}, ErrInvalidRequest
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return commandauth.UseStatus{}, fmt.Errorf("begin command grant consumption: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := activateTenant(ctx, tx, claims.TenantID); err != nil {
		return commandauth.UseStatus{}, err
	}
	tag, err := tx.Exec(ctx, `
INSERT INTO command_runtime.command_grant_uses (
  token_id, grant_id, tenant_id, policy_revision, emergency_revocation_revision, used_at
) VALUES ($1, $2, $3::uuid, $4, $5, $6)
ON CONFLICT (token_id) DO NOTHING
`, claims.TokenID, claims.GrantID, claims.TenantID, claims.PolicyRevision, claims.EmergencyRevocationRevision, store.now().UTC())
	if err != nil {
		return commandauth.UseStatus{}, fmt.Errorf("consume command grant: %w", err)
	}
	status := commandauth.UseStatus{
		CurrentPolicyRevision:     currentPolicyRevision,
		CurrentRevocationRevision: currentRevocationRevision,
		Replayed:                  tag.RowsAffected() == 0,
	}
	if err := tx.Commit(ctx); err != nil {
		return commandauth.UseStatus{}, fmt.Errorf("commit command grant consumption: %w", err)
	}
	return status, nil
}
