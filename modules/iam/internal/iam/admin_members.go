package iam

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrAdminSelfLockout refuses a change that would leave the acting administrator without
// Tenant-wide user administration.
var ErrAdminSelfLockout = errors.New("IAM admin change would remove the actor's own administration")

// AdminAssignment names a Role to assign, across the whole Tenant (SiteID "") or on one Site.
type AdminAssignment struct {
	RoleTemplateID string `json:"roleTemplateId"`
	SiteID         string `json:"siteId,omitempty"`
}

// AdminDirectory is what 系统管理 → 用户与角色 shows: the assignable built-in Roles and every
// member of the Tenant with their assignments.
type AdminDirectory struct {
	Roles   []AdminRole   `json:"roles"`
	Members []AdminMember `json:"members"`
}

type AdminRole struct {
	RoleTemplateID string `json:"roleTemplateId"`
	RoleKey        string `json:"roleKey"`
	DisplayName    string `json:"displayName"`
	TenantScoped   bool   `json:"tenantScoped"`
}

type AdminMember struct {
	PrincipalID        string                  `json:"principalId"`
	SubjectIssuer      string                  `json:"subjectIssuer"`
	Subject            string                  `json:"subject"`
	DisplayName        string                  `json:"displayName"`
	Email              string                  `json:"email"`
	PrincipalRevision  int64                   `json:"principalRevision"`
	MembershipStatus   string                  `json:"membershipStatus"`
	MembershipRevision int64                   `json:"membershipRevision"`
	Assignments        []AdminMemberAssignment `json:"assignments"`
}

type AdminMemberAssignment struct {
	BindingID      string `json:"bindingId"`
	RoleTemplateID string `json:"roleTemplateId"`
	RoleKey        string `json:"roleKey"`
	SiteID         string `json:"siteId,omitempty"`
	Status         string `json:"status"`
	Revision       int64  `json:"revision"`
}

func (store *PostgresAdminStore) ListMembers(ctx context.Context, actor AdminActor) (AdminDirectory, error) {
	tx, _, err := store.beginActorTransaction(ctx, actor)
	if err != nil {
		return AdminDirectory{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	directory := AdminDirectory{Roles: []AdminRole{}, Members: []AdminMember{}}

	rows, err := tx.Query(ctx, `SELECT id::text, role_key, display_name, capabilities FROM iam.role_templates WHERE tenant_id IS NULL AND status = 'ACTIVE' ORDER BY cardinality(capabilities), role_key`)
	if err != nil {
		return AdminDirectory{}, fmt.Errorf("read IAM built-in Roles: %w", err)
	}
	for rows.Next() {
		var role AdminRole
		var capabilities []string
		if err := rows.Scan(&role.RoleTemplateID, &role.RoleKey, &role.DisplayName, &capabilities); err != nil {
			rows.Close()
			return AdminDirectory{}, err
		}
		role.TenantScoped = roleTenantScoped(capabilities)
		directory.Roles = append(directory.Roles, role)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return AdminDirectory{}, err
	}

	rows, err = tx.Query(ctx, `
SELECT principal.id::text, principal.external_issuer, principal.external_subject, principal.display_name, principal.email,
       principal.revision, membership.status, membership.revision
FROM iam.tenant_memberships membership
JOIN iam.principals principal ON principal.id = membership.principal_id
WHERE membership.tenant_id = $1::uuid
ORDER BY principal.display_name, principal.id
`, actor.TenantID)
	if err != nil {
		return AdminDirectory{}, fmt.Errorf("read IAM members: %w", err)
	}
	index := map[string]int{}
	for rows.Next() {
		member := AdminMember{Assignments: []AdminMemberAssignment{}}
		if err := rows.Scan(&member.PrincipalID, &member.SubjectIssuer, &member.Subject, &member.DisplayName, &member.Email,
			&member.PrincipalRevision, &member.MembershipStatus, &member.MembershipRevision); err != nil {
			rows.Close()
			return AdminDirectory{}, err
		}
		index[member.PrincipalID] = len(directory.Members)
		directory.Members = append(directory.Members, member)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return AdminDirectory{}, err
	}

	rows, err = tx.Query(ctx, `
SELECT binding.id::text, binding.principal_id::text, template.id::text, template.role_key, COALESCE(binding.site_id::text, ''),
       binding.status, binding.revision
FROM iam.role_bindings binding
JOIN iam.role_templates template ON template.id = binding.role_template_id
WHERE binding.tenant_id = $1::uuid AND binding.status <> 'REVOKED'
ORDER BY binding.site_id NULLS FIRST, template.role_key
`, actor.TenantID)
	if err != nil {
		return AdminDirectory{}, fmt.Errorf("read IAM role assignments: %w", err)
	}
	for rows.Next() {
		var assignment AdminMemberAssignment
		var principalID string
		if err := rows.Scan(&assignment.BindingID, &principalID, &assignment.RoleTemplateID, &assignment.RoleKey, &assignment.SiteID,
			&assignment.Status, &assignment.Revision); err != nil {
			rows.Close()
			return AdminDirectory{}, err
		}
		if position, ok := index[principalID]; ok {
			directory.Members[position].Assignments = append(directory.Members[position].Assignments, assignment)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return AdminDirectory{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AdminDirectory{}, err
	}
	return directory, nil
}

func roleTenantScoped(capabilities []string) bool {
	for _, capability := range capabilities {
		if capabilityTenantScoped(capability) {
			return true
		}
	}
	return false
}

// applyMemberCreate makes an account from the identity directory a member of the Tenant with its
// first Role Assignments, in one transaction. An existing Principal for the same identity is reused.
func applyMemberCreate(ctx context.Context, tx pgx.Tx, tenantID string, request AdminMutationRequest, now time.Time) (string, int64, error) {
	subjectIssuer, subject := strings.TrimSpace(request.SubjectIssuer), strings.TrimSpace(request.Subject)
	displayName, email := strings.TrimSpace(request.DisplayName), strings.TrimSpace(request.Email)
	if subjectIssuer == "" || subject == "" || displayName == "" || email == "" || len(request.Assignments) == 0 {
		return "", 0, errors.New("member creation is invalid")
	}
	var principalID string
	var status FactStatus
	var revision int64
	err := tx.QueryRow(ctx, `SELECT principal_id::text, principal_status, principal_revision FROM iam.resolve_principal_identity($1, $2)`, subjectIssuer, subject).Scan(&principalID, &status, &revision)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if principalID, err = newUUIDv7(now); err != nil {
			return "", 0, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO iam.principals (id, external_issuer, external_subject, display_name, email, status, revision, created_at, updated_at) VALUES ($1::uuid, $2, $3, $4, $5, 'ACTIVE', 1, $6, $6)`,
			principalID, subjectIssuer, subject, displayName, email, now.UTC()); err != nil {
			return "", 0, fmt.Errorf("create IAM principal: %w", err)
		}
	case err != nil:
		return "", 0, fmt.Errorf("resolve IAM principal: %w", err)
	}
	var existingMembership bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM iam.tenant_memberships WHERE tenant_id = $1::uuid AND principal_id = $2::uuid)`, tenantID, principalID).Scan(&existingMembership); err != nil {
		return "", 0, err
	}
	if existingMembership {
		return "", 0, ErrAdminRevisionConflict
	}
	if _, _, err := applyMembership(ctx, tx, tenantID, AdminMutationRequest{PrincipalID: principalID, Status: "ACTIVE"}, now); err != nil {
		return "", 0, err
	}
	for _, assignment := range request.Assignments {
		if _, _, err := applyRoleBinding(ctx, tx, tenantID, AdminMutationRequest{
			PrincipalID: principalID, RoleTemplateID: assignment.RoleTemplateID, SiteID: assignment.SiteID, Status: "ACTIVE",
		}, now); err != nil {
			return "", 0, err
		}
	}
	return principalID, 1, nil
}

// ensureActorStillAdministers refuses a mutation after which the acting Principal would no
// longer be an active member holding iam.admin across the whole Tenant.
func ensureActorStillAdministers(ctx context.Context, tx pgx.Tx, tenantID, actorPrincipalID string, now time.Time) error {
	var administers bool
	err := tx.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1
  FROM iam.tenant_memberships membership
  JOIN iam.role_bindings binding ON binding.tenant_id = membership.tenant_id AND binding.principal_id = membership.principal_id
  JOIN iam.role_templates template ON template.id = binding.role_template_id
  WHERE membership.tenant_id = $1::uuid AND membership.principal_id = $2::uuid AND membership.status = 'ACTIVE'
    AND binding.site_id IS NULL AND binding.status = 'ACTIVE' AND binding.valid_from <= $3 AND (binding.valid_to IS NULL OR binding.valid_to > $3)
    AND template.status = 'ACTIVE' AND template.capabilities @> ARRAY['iam.admin']::text[]
)`, tenantID, actorPrincipalID, now.UTC()).Scan(&administers)
	if err != nil {
		return err
	}
	if !administers {
		return ErrAdminSelfLockout
	}
	return nil
}
