package iam

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

func (store *PostgresAuthorizationStore) LookupPrincipalTelemetryCapabilities(ctx context.Context, lookup PrincipalCapabilityLookup) (TelemetryAuthorizationFacts, error) {
	if store == nil || store.pool == nil {
		return TelemetryAuthorizationFacts{}, errors.New("IAM authorization store is closed")
	}
	if strings.TrimSpace(lookup.SubjectIssuer) == "" || strings.TrimSpace(lookup.Subject) == "" || strings.TrimSpace(lookup.ActingOrganizationID) == "" {
		return TelemetryAuthorizationFacts{}, errors.New("IAM principal telemetry capability lookup is incomplete")
	}

	transaction, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return TelemetryAuthorizationFacts{}, fmt.Errorf("begin IAM principal telemetry capability lookup: %w", err)
	}
	defer func() { _ = transaction.Rollback(ctx) }()

	facts := TelemetryAuthorizationFacts{}
	var principalRevision int64
	err = transaction.QueryRow(ctx, `
SELECT principal_id::text, principal_status, principal_revision
FROM iam.resolve_principal_identity($1, $2)
`, lookup.SubjectIssuer, lookup.Subject).Scan(&facts.Principal.ID, &facts.Principal.Status, &principalRevision)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return TelemetryAuthorizationFacts{}, fmt.Errorf("resolve IAM principal telemetry identity: %w", err)
	}
	principalID := facts.Principal.ID
	if errors.Is(err, pgx.ErrNoRows) {
		principalID = ""
	}
	if err := setIAMTelemetryAuthorizationContext(ctx, transaction, principalID, lookup.ActingOrganizationID, []string{}); err != nil {
		return TelemetryAuthorizationFacts{}, err
	}
	facts.PolicyRevision, err = loadTelemetryPolicyRevision(ctx, transaction)
	if err != nil {
		return TelemetryAuthorizationFacts{}, err
	}
	if principalID == "" {
		if err := transaction.Commit(ctx); err != nil {
			return TelemetryAuthorizationFacts{}, fmt.Errorf("commit unmapped IAM principal telemetry capability lookup: %w", err)
		}
		return facts, nil
	}

	facts.Found = true
	facts.Principal.SubjectIssuer = lookup.SubjectIssuer
	facts.Principal.Subject = lookup.Subject
	if facts.Memberships, err = loadOrganizationMemberships(ctx, transaction); err != nil {
		return TelemetryAuthorizationFacts{}, err
	}
	if facts.RoleBindings, err = loadTelemetryRoleBindings(ctx, transaction); err != nil {
		return TelemetryAuthorizationFacts{}, err
	}
	if facts.SiteBindings, err = loadTelemetrySiteBindings(ctx, transaction); err != nil {
		return TelemetryAuthorizationFacts{}, err
	}
	if facts.ExplicitDenies, err = loadTelemetryExplicitDenies(ctx, transaction); err != nil {
		return TelemetryAuthorizationFacts{}, err
	}
	if facts.ScopeBindings, err = loadTelemetryScopeBindings(ctx, transaction); err != nil {
		return TelemetryAuthorizationFacts{}, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return TelemetryAuthorizationFacts{}, fmt.Errorf("commit IAM principal telemetry capability lookup: %w", err)
	}
	return facts, nil
}
