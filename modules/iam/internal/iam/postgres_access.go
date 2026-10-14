package iam

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// accessPolicy names the domain policy whose revision a decision cites. With fallback set, a
// Tenant without that policy decides under the fallback revision instead of failing.
type accessPolicy struct {
	key      string
	fallback string
}

var (
	registryAccessPolicy  = accessPolicy{key: "registry-read"}
	telemetryAccessPolicy = accessPolicy{key: "telemetry-access"}
	alarmAccessPolicy     = accessPolicy{key: "alarm-access", fallback: "alarm-access:unconfigured"}
	workOrderAccessPolicy = accessPolicy{key: "work-order-access", fallback: "work-order-access:unconfigured"}
)

// lookupAccess reads one Principal's access in one Tenant in a single repeatable-read snapshot,
// then lets within read more from the same snapshot. policy is nil when the caller supplies the
// policy revision itself.
func (store *PostgresAuthorizationStore) lookupAccess(ctx context.Context, lookup AuthorizationLookup, policy *accessPolicy, within func(pgx.Tx, *AuthorizationFacts) error) (AuthorizationFacts, error) {
	if store == nil || store.pool == nil {
		return AuthorizationFacts{}, errors.New("IAM authorization store is closed")
	}
	if strings.TrimSpace(lookup.SubjectIssuer) == "" || strings.TrimSpace(lookup.Subject) == "" || strings.TrimSpace(lookup.TenantID) == "" {
		return AuthorizationFacts{}, errors.New("IAM authorization lookup is incomplete")
	}
	transaction, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return AuthorizationFacts{}, fmt.Errorf("begin IAM authorization lookup: %w", err)
	}
	defer func() { _ = transaction.Rollback(ctx) }()

	facts := AuthorizationFacts{}
	var principalRevision int64
	err = transaction.QueryRow(ctx, `
SELECT principal_id::text, principal_status, principal_revision
FROM iam.resolve_principal_identity($1, $2)
`, lookup.SubjectIssuer, lookup.Subject).Scan(&facts.Principal.ID, &facts.Principal.Status, &principalRevision)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return AuthorizationFacts{}, fmt.Errorf("resolve IAM principal identity: %w", err)
	}
	facts.Found = err == nil
	if !facts.Found {
		facts.Principal = PrincipalRecord{}
	}
	if err := setIAMAuthorizationContext(ctx, transaction, facts.Principal.ID, lookup.TenantID); err != nil {
		return AuthorizationFacts{}, err
	}
	if policy != nil {
		if facts.PolicyRevision, err = loadAccessPolicyRevision(ctx, transaction, *policy); err != nil {
			return AuthorizationFacts{}, err
		}
	}
	if facts.Found {
		facts.Principal.SubjectIssuer = lookup.SubjectIssuer
		facts.Principal.Subject = lookup.Subject
		if err := loadPrincipalAccess(ctx, transaction, lookup.TenantID, lookupInstant(lookup.At), &facts); err != nil {
			return AuthorizationFacts{}, err
		}
	}
	if within != nil {
		if err := within(transaction, &facts); err != nil {
			return AuthorizationFacts{}, err
		}
	}
	if err := transaction.Commit(ctx); err != nil {
		return AuthorizationFacts{}, fmt.Errorf("commit IAM authorization lookup: %w", err)
	}
	return facts, nil
}

func lookupInstant(at time.Time) time.Time {
	if at.IsZero() {
		return time.Now().UTC()
	}
	return at.UTC()
}

// loadPrincipalAccess reads the memberships, assignments and denies of the Principal the RLS
// context names, and the Tenant's Sites when the membership is effective.
func loadPrincipalAccess(ctx context.Context, transaction pgx.Tx, tenantID string, at time.Time, facts *AuthorizationFacts) error {
	var err error
	if facts.Memberships, err = loadTenantMemberships(ctx, transaction); err != nil {
		return err
	}
	if active, _ := tenantMembershipState(facts.Memberships, tenantID, at); active {
		if facts.TenantSiteIDs, err = loadTenantSiteIDs(ctx, transaction, tenantID); err != nil {
			return err
		}
	}
	if facts.Assignments, err = loadRoleAssignments(ctx, transaction); err != nil {
		return err
	}
	if facts.ExplicitDenies, err = loadExplicitDenies(ctx, transaction); err != nil {
		return err
	}
	return nil
}

// loadPrincipalAccessByID switches the RLS context to another Principal of the Tenant and reads
// its access; an unknown Principal yields facts that are not Found.
func loadPrincipalAccessByID(ctx context.Context, transaction pgx.Tx, principalID, tenantID string, at time.Time) (AuthorizationFacts, error) {
	facts := AuthorizationFacts{}
	if err := setIAMAuthorizationContext(ctx, transaction, principalID, tenantID); err != nil {
		return AuthorizationFacts{}, err
	}
	err := transaction.QueryRow(ctx, `SELECT id::text, status FROM iam.principals WHERE id = $1::uuid`, principalID).
		Scan(&facts.Principal.ID, &facts.Principal.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return AuthorizationFacts{}, nil
	}
	if err != nil {
		return AuthorizationFacts{}, fmt.Errorf("read IAM principal %s: %w", principalID, err)
	}
	facts.Found = true
	if err := loadPrincipalAccess(ctx, transaction, tenantID, at, &facts); err != nil {
		return AuthorizationFacts{}, err
	}
	return facts, nil
}

func loadAccessPolicyRevision(ctx context.Context, transaction pgx.Tx, policy accessPolicy) (string, error) {
	var policyRevision, authorizationRevision int64
	err := transaction.QueryRow(ctx, `
SELECT policy.policy_revision, auth_revision.revision
FROM iam.policies policy
JOIN iam.authorization_revisions auth_revision ON auth_revision.tenant_id = policy.tenant_id
WHERE policy.status = 'ACTIVE'
  AND policy.policy_key = $1
ORDER BY policy.policy_revision DESC
LIMIT 1
`, policy.key).Scan(&policyRevision, &authorizationRevision)
	if errors.Is(err, pgx.ErrNoRows) {
		if policy.fallback != "" {
			return policy.fallback, nil
		}
		return "", fmt.Errorf("active IAM %s policy is missing", policy.key)
	}
	if err != nil {
		return "", fmt.Errorf("read active IAM %s policy: %w", policy.key, err)
	}
	return fmt.Sprintf("%s:%d/iam:%d", policy.key, policyRevision, authorizationRevision), nil
}

func loadRoleAssignments(ctx context.Context, transaction pgx.Tx) ([]RoleAssignment, error) {
	rows, err := transaction.Query(ctx, `
SELECT binding.tenant_id::text, COALESCE(binding.site_id::text, ''), template.role_key, template.capabilities,
       binding.status, binding.valid_from, binding.valid_to
FROM iam.role_bindings binding
JOIN iam.role_templates template ON template.id = binding.role_template_id
WHERE template.status = 'ACTIVE'
ORDER BY binding.tenant_id, binding.site_id NULLS FIRST, template.role_key
`)
	if err != nil {
		return nil, fmt.Errorf("query IAM role assignments: %w", err)
	}
	defer rows.Close()
	assignments := []RoleAssignment{}
	for rows.Next() {
		var assignment RoleAssignment
		if err := rows.Scan(&assignment.TenantID, &assignment.SiteID, &assignment.RoleKey, &assignment.Capabilities,
			&assignment.Status, &assignment.ValidFrom, &assignment.ValidTo); err != nil {
			return nil, fmt.Errorf("scan IAM role assignment: %w", err)
		}
		for _, capability := range assignment.Capabilities {
			if !catalogCapabilityValid(capability) {
				return nil, fmt.Errorf("validate IAM role %q: unsupported capability %q", assignment.RoleKey, capability)
			}
		}
		assignments = append(assignments, assignment)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate IAM role assignments: %w", err)
	}
	return assignments, nil
}

func loadExplicitDenies(ctx context.Context, transaction pgx.Tx) ([]ExplicitDeny, error) {
	rows, err := transaction.Query(ctx, `
SELECT tenant_id::text, COALESCE(site_id::text, ''), action, valid_from, valid_to
FROM iam.explicit_denies
ORDER BY tenant_id, site_id NULLS FIRST, action
`)
	if err != nil {
		return nil, fmt.Errorf("query IAM explicit denies: %w", err)
	}
	defer rows.Close()
	denies := []ExplicitDeny{}
	for rows.Next() {
		var deny ExplicitDeny
		if err := rows.Scan(&deny.TenantID, &deny.SiteID, &deny.Capability, &deny.ValidFrom, &deny.ValidTo); err != nil {
			return nil, fmt.Errorf("scan IAM explicit deny: %w", err)
		}
		if !catalogCapabilityValid(deny.Capability) {
			return nil, fmt.Errorf("validate IAM explicit deny: unsupported capability %q", deny.Capability)
		}
		denies = append(denies, deny)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate IAM explicit denies: %w", err)
	}
	return denies, nil
}
