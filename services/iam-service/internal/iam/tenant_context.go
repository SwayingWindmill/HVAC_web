package iam

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
)

const TenantContextsPath = "/internal/v1/principal/tenant-contexts"

type TenantContext struct {
	TenantID    string `json:"tenantId"`
	Code        string `json:"code,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
}

type TenantContextResolver interface {
	ListTenantContexts(context.Context, string, string, time.Time) ([]TenantContext, error)
}

func (store *staticAuthorizationStore) ListTenantContexts(_ context.Context, subjectIssuer, subject string, now time.Time) ([]TenantContext, error) {
	facts, ok := store.facts[authorizationIdentityKey(subjectIssuer, subject)]
	if !ok || facts.Principal.Status != FactStatusActive {
		return []TenantContext{}, nil
	}
	contexts := make([]TenantContext, 0, len(facts.Memberships))
	for _, membership := range facts.Memberships {
		if membership.Status == FactStatusActive && factEffective(membership.ValidFrom, membership.ValidTo, now) {
			contexts = append(contexts, TenantContext{TenantID: membership.TenantID})
		}
	}
	sort.Slice(contexts, func(i, j int) bool { return contexts[i].TenantID < contexts[j].TenantID })
	return contexts, nil
}

func (store *PostgresAuthorizationStore) ListTenantContexts(ctx context.Context, subjectIssuer, subject string, now time.Time) ([]TenantContext, error) {
	if store == nil || store.pool == nil {
		return nil, errors.New("IAM authorization store is closed")
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("begin IAM tenant context lookup: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var principalID string
	var status FactStatus
	var revision int64
	if err := tx.QueryRow(ctx, `SELECT principal_id::text, principal_status, principal_revision FROM iam.resolve_principal_identity($1,$2)`, subjectIssuer, subject).Scan(&principalID, &status, &revision); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return []TenantContext{}, nil
		}
		return nil, fmt.Errorf("resolve IAM principal for tenant contexts: %w", err)
	}
	if status != FactStatusActive {
		return []TenantContext{}, nil
	}
	var configured string
	if err := tx.QueryRow(ctx, `SELECT set_config('app.principal_id', $1, true)`, principalID).Scan(&configured); err != nil {
		return nil, fmt.Errorf("set IAM principal context: %w", err)
	}
	rows, err := tx.Query(ctx, `
SELECT membership.tenant_id::text, tenant.code, tenant.display_name
FROM iam.tenant_memberships membership
JOIN iam.tenants tenant ON tenant.id = membership.tenant_id
WHERE membership.status = 'ACTIVE'
  AND membership.valid_from <= $1
  AND (membership.valid_to IS NULL OR membership.valid_to > $1)
  AND tenant.status = 'ACTIVE'
ORDER BY tenant.code, membership.tenant_id
`, now.UTC())
	if err != nil {
		return nil, fmt.Errorf("query IAM tenant contexts: %w", err)
	}
	defer rows.Close()
	contexts := []TenantContext{}
	for rows.Next() {
		var item TenantContext
		if err := rows.Scan(&item.TenantID, &item.Code, &item.DisplayName); err != nil {
			return nil, fmt.Errorf("scan IAM tenant context: %w", err)
		}
		contexts = append(contexts, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate IAM tenant contexts: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit IAM tenant context lookup: %w", err)
	}
	return contexts, nil
}
