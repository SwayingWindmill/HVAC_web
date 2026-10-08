package iam

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

type postgresCommandAuthorizationStore struct {
	store                       *PostgresAuthorizationStore
	policyRevision              string
	emergencyRevocationRevision uint64
}

// CommandAuthorization uses the consumer's declared grant revisions; configuration
// supplies protocol metadata, while database facts alone supply permission.
func (store *PostgresAuthorizationStore) CommandAuthorization(policyRevision string, emergencyRevocationRevision uint64) CommandAuthorizationStore {
	return &postgresCommandAuthorizationStore{store, policyRevision, emergencyRevocationRevision}
}

func (adapter *postgresCommandAuthorizationStore) LookupCommandAuthorization(ctx context.Context, lookup AuthorizationLookup) (CommandAuthorizationFacts, error) {
	transaction, err := adapter.store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return CommandAuthorizationFacts{}, fmt.Errorf("begin IAM Command authorization: %w", err)
	}
	defer func() { _ = transaction.Rollback(ctx) }()
	facts := CommandAuthorizationFacts{PolicyRevision: adapter.policyRevision, EmergencyRevocationRevision: adapter.emergencyRevocationRevision}
	var principalRevision int64
	err = transaction.QueryRow(ctx, `SELECT principal_id::text, principal_status, principal_revision FROM iam.resolve_principal_identity($1,$2)`, lookup.SubjectIssuer, lookup.Subject).Scan(&facts.Principal.ID, &facts.Principal.Status, &principalRevision)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return CommandAuthorizationFacts{}, fmt.Errorf("resolve IAM Command principal: %w", err)
	}
	if err := setIAMAuthorizationContext(ctx, transaction, facts.Principal.ID, lookup.TenantID); err != nil {
		return CommandAuthorizationFacts{}, err
	}
	if facts.Principal.ID != "" {
		facts.Found = true
		facts.Principal.SubjectIssuer = lookup.SubjectIssuer
		facts.Principal.Subject = lookup.Subject
		if facts.Memberships, err = loadTenantMemberships(ctx, transaction); err != nil {
			return CommandAuthorizationFacts{}, err
		}
		rows, err := transaction.Query(ctx, `SELECT tenant_id::text,site_id::text,device_id::text,capability,capability_revision,purpose,maximum_risk,effect,status,valid_from,valid_to FROM iam.command_permissions ORDER BY site_id,device_id,capability,purpose,effect`)
		if err != nil {
			return CommandAuthorizationFacts{}, fmt.Errorf("query IAM Command permissions: %w", err)
		}
		for rows.Next() {
			var permission CommandPermission
			if err := rows.Scan(&permission.TenantID, &permission.SiteID, &permission.DeviceID, &permission.Capability, &permission.CapabilityRevision, &permission.Purpose, &permission.MaximumRisk, &permission.Effect, &permission.Status, &permission.ValidFrom, &permission.ValidTo); err != nil {
				rows.Close()
				return CommandAuthorizationFacts{}, fmt.Errorf("scan IAM Command permission: %w", err)
			}
			facts.Permissions = append(facts.Permissions, permission)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return CommandAuthorizationFacts{}, fmt.Errorf("read IAM Command permissions: %w", err)
		}
	}
	if err := transaction.Commit(ctx); err != nil {
		return CommandAuthorizationFacts{}, fmt.Errorf("commit IAM Command authorization: %w", err)
	}
	return facts, nil
}
