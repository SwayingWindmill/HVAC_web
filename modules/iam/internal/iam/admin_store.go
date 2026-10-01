package iam

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const iamAdminDatabaseRole = "s1_iam_admin"

var ErrAdminRevisionConflict = errors.New("IAM admin revision conflict")
var ErrAdminResourceNotFound = errors.New("IAM admin resource not found")
var ErrAPICredentialInvalid = errors.New("API credential invalid")

type AdminActor struct {
	SubjectIssuer string
	Subject       string
	TenantID      string
	CorrelationID string
	TraceID       string
	OccurredAt    time.Time
}

type AdminMutationRequest struct {
	Operation        string     `json:"operation"`
	ExpectedRevision int64      `json:"expectedRevision"`
	ResourceID       string     `json:"resourceId,omitempty"`
	PrincipalID      string     `json:"principalId,omitempty"`
	RoleTemplateID   string     `json:"roleTemplateId,omitempty"`
	RoleKey          string     `json:"roleKey,omitempty"`
	DisplayName      string     `json:"displayName,omitempty"`
	Email            string     `json:"email,omitempty"`
	Timezone         string     `json:"timezone,omitempty"`
	Currency         string     `json:"currency,omitempty"`
	Country          string     `json:"country,omitempty"`
	Status           string     `json:"status,omitempty"`
	SiteID           string     `json:"siteId,omitempty"`
	Capabilities     []string   `json:"capabilities,omitempty"`
	DenyCapability   string     `json:"denyCapability,omitempty"`
	ReasonCode       string     `json:"reasonCode,omitempty"`
	ValidFrom        time.Time  `json:"validFrom,omitempty"`
	ValidTo          *time.Time `json:"validTo,omitempty"`
}

type AdminMutationResult struct {
	ResourceID     string `json:"resourceId"`
	Revision       int64  `json:"revision"`
	PolicyRevision string `json:"policyRevision"`
	AuditEventID   string `json:"auditEventId"`
}

type APICredentialCreateRequest struct {
	DisplayName  string    `json:"displayName"`
	Purpose      string    `json:"purpose"`
	Capabilities []string  `json:"capabilities"`
	SiteIDs      []string  `json:"siteIds"`
	ExpiresAt    time.Time `json:"expiresAt"`
}

type APICredentialIssue struct {
	ServiceAccountID string    `json:"serviceAccountId"`
	CredentialID     string    `json:"credentialId"`
	Secret           string    `json:"secret"`
	Revision         int64     `json:"revision"`
	ExpiresAt        time.Time `json:"expiresAt"`
	AuditEventID     string    `json:"auditEventId"`
}

type APICredentialVerification struct {
	TenantID         string
	ServiceAccountID string
	Capabilities     []string
	SiteIDs          []string
	ExpiresAt        time.Time
}

type AdminStore interface {
	ApplyMutation(context.Context, AdminActor, AdminMutationRequest) (AdminMutationResult, error)
	CreateAPICredential(context.Context, AdminActor, APICredentialCreateRequest) (APICredentialIssue, error)
	RotateAPICredential(context.Context, AdminActor, string, int64, time.Time) (APICredentialIssue, error)
	RevokeAPICredential(context.Context, AdminActor, string, int64) (AdminMutationResult, error)
}

type PostgresAdminStore struct {
	pool   *pgxpool.Pool
	pepper []byte
}

func OpenPostgresAdminStore(ctx context.Context, databaseURL string, pepper []byte) (*PostgresAdminStore, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, errors.New("IAM admin database URL is required")
	}
	if len(pepper) < 32 {
		return nil, errors.New("IAM API credential pepper must be at least 32 bytes")
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse IAM admin database configuration: %w", err)
	}
	config.ConnConfig.RuntimeParams["application_name"] = "iam-service-admin"
	config.ConnConfig.RuntimeParams["statement_timeout"] = "5s"
	config.ConnConfig.RuntimeParams["lock_timeout"] = "1s"
	config.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"] = "5s"
	config.AfterConnect = func(ctx context.Context, connection *pgx.Conn) error {
		var role string
		if err := connection.QueryRow(ctx, `SELECT current_user`).Scan(&role); err != nil {
			return fmt.Errorf("read IAM admin database role: %w", err)
		}
		if role != iamAdminDatabaseRole {
			return fmt.Errorf("IAM admin database role %q is not allowed", role)
		}
		return nil
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("open IAM admin store: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping IAM admin store: %w", err)
	}
	return &PostgresAdminStore{pool: pool, pepper: append([]byte(nil), pepper...)}, nil
}

func (store *PostgresAdminStore) Close() {
	if store != nil && store.pool != nil {
		store.pool.Close()
	}
}

func (store *PostgresAdminStore) ApplyMutation(ctx context.Context, actor AdminActor, request AdminMutationRequest) (AdminMutationResult, error) {
	if store == nil || store.pool == nil {
		return AdminMutationResult{}, errors.New("IAM admin store is closed")
	}
	if strings.TrimSpace(actor.TenantID) == "" || strings.TrimSpace(actor.SubjectIssuer) == "" || strings.TrimSpace(actor.Subject) == "" {
		return AdminMutationResult{}, errors.New("IAM admin actor is incomplete")
	}
	tx, actorPrincipalID, err := store.beginActorTransaction(ctx, actor)
	if err != nil {
		return AdminMutationResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var resourceID string
	var revision int64
	switch request.Operation {
	case "tenant.update":
		resourceID, revision, err = applyTenantUpdate(ctx, tx, actor.TenantID, request, actor.OccurredAt)
	case "principal.update":
		resourceID, revision, err = applyPrincipalUpdate(ctx, tx, request, actor.OccurredAt)
	case "membership.upsert":
		resourceID, revision, err = applyMembership(ctx, tx, actor.TenantID, request, actor.OccurredAt)
	case "role-template.upsert":
		if err = validateCatalogCapabilities(ctx, tx, request.Capabilities); err == nil {
			resourceID, revision, err = applyRoleTemplate(ctx, tx, actor.TenantID, request, actor.OccurredAt)
		}
	case "role-binding.upsert":
		resourceID, revision, err = applyRoleBinding(ctx, tx, actor.TenantID, request, actor.OccurredAt)
	case "site-binding.upsert":
		if err = validateCatalogCapabilities(ctx, tx, request.Capabilities); err == nil {
			resourceID, revision, err = applySiteBinding(ctx, tx, actor.TenantID, request, actor.OccurredAt)
		}
	case "explicit-deny.upsert":
		if err = validateCatalogCapabilities(ctx, tx, []string{request.DenyCapability}); err == nil {
			resourceID, revision, err = applyExplicitDeny(ctx, tx, actor.TenantID, request, actor.OccurredAt)
		}
	case "service-account.status":
		resourceID, revision, err = applyServiceAccountStatus(ctx, tx, actor.TenantID, request, actor.OccurredAt)
	default:
		err = fmt.Errorf("unsupported IAM admin operation %q", request.Operation)
	}
	if err != nil {
		return AdminMutationResult{}, err
	}
	policyRevision, err := bumpAuthorizationRevision(ctx, tx, actor.TenantID, actor.OccurredAt)
	if err != nil {
		return AdminMutationResult{}, err
	}
	auditEventID, err := insertAdminAudit(ctx, tx, actor, actorPrincipalID, request.Operation, adminResourceType(request.Operation), resourceID, policyRevision)
	if err != nil {
		return AdminMutationResult{}, fmt.Errorf("write IAM admin audit/outbox: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return AdminMutationResult{}, err
	}
	return AdminMutationResult{ResourceID: resourceID, Revision: revision, PolicyRevision: policyRevision, AuditEventID: auditEventID}, nil
}

func (store *PostgresAdminStore) CreateAPICredential(ctx context.Context, actor AdminActor, request APICredentialCreateRequest) (APICredentialIssue, error) {
	if store == nil || store.pool == nil {
		return APICredentialIssue{}, errors.New("IAM admin store is closed")
	}
	tx, actorPrincipalID, err := store.beginActorTransaction(ctx, actor)
	if err != nil {
		return APICredentialIssue{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := validateCatalogCapabilities(ctx, tx, request.Capabilities); err != nil {
		return APICredentialIssue{}, err
	}
	if !request.ExpiresAt.After(actor.OccurredAt) {
		return APICredentialIssue{}, errors.New("API credential expiry must be in the future")
	}
	serviceAccountID, err := newUUIDv7(actor.OccurredAt)
	if err != nil {
		return APICredentialIssue{}, err
	}
	credentialID, err := newUUIDv7(actor.OccurredAt.Add(time.Millisecond))
	if err != nil {
		return APICredentialIssue{}, err
	}
	catalogRevision, err := activeCatalogRevision(ctx, tx)
	if err != nil {
		return APICredentialIssue{}, err
	}
	secret, err := newAPICredentialSecret(actor.TenantID, credentialID)
	if err != nil {
		return APICredentialIssue{}, err
	}
	secretHash := store.hashCredentialSecret(secret)
	if _, err := tx.Exec(ctx, `
INSERT INTO iam.service_accounts (
  id, tenant_id, display_name, purpose, status, revision, created_by_principal_id, created_at, updated_at
) VALUES ($1::uuid,$2::uuid,$3,$4,'ACTIVE',1,$5::uuid,$6,$6)
`, serviceAccountID, actor.TenantID, strings.TrimSpace(request.DisplayName), strings.TrimSpace(request.Purpose), actorPrincipalID, actor.OccurredAt.UTC()); err != nil {
		return APICredentialIssue{}, fmt.Errorf("create service account: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO iam.api_credentials (
  id, tenant_id, service_account_id, secret_hash, capabilities, site_ids, catalog_revision,
  status, expires_at, rotated_from_id, revision, created_by_principal_id, created_at, updated_at
) VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5,$6::uuid[],$7,'ACTIVE',$8,NULL,1,$9::uuid,$10,$10)
`, credentialID, actor.TenantID, serviceAccountID, secretHash, request.Capabilities, request.SiteIDs, catalogRevision, request.ExpiresAt.UTC(), actorPrincipalID, actor.OccurredAt.UTC()); err != nil {
		return APICredentialIssue{}, fmt.Errorf("create API credential: %w", err)
	}
	policyRevision, err := bumpAuthorizationRevision(ctx, tx, actor.TenantID, actor.OccurredAt)
	if err != nil {
		return APICredentialIssue{}, err
	}
	auditEventID, err := insertAdminAudit(ctx, tx, actor, actorPrincipalID, "api-credential.create", "API_CREDENTIAL", credentialID, policyRevision)
	if err != nil {
		return APICredentialIssue{}, fmt.Errorf("write API credential audit/outbox: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return APICredentialIssue{}, err
	}
	return APICredentialIssue{ServiceAccountID: serviceAccountID, CredentialID: credentialID, Secret: secret, Revision: 1, ExpiresAt: request.ExpiresAt.UTC(), AuditEventID: auditEventID}, nil
}

func (store *PostgresAdminStore) RotateAPICredential(ctx context.Context, actor AdminActor, credentialID string, expectedRevision int64, expiresAt time.Time) (APICredentialIssue, error) {
	tx, actorPrincipalID, err := store.beginActorTransaction(ctx, actor)
	if err != nil {
		return APICredentialIssue{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var serviceAccountID, status string
	var capabilities, siteIDs []string
	var revision int64
	if err := tx.QueryRow(ctx, `
SELECT service_account_id::text, capabilities, site_ids::text[], status, revision
FROM iam.api_credentials
WHERE id=$1::uuid AND tenant_id=$2::uuid
FOR UPDATE
`, credentialID, actor.TenantID).Scan(&serviceAccountID, &capabilities, &siteIDs, &status, &revision); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return APICredentialIssue{}, ErrAdminResourceNotFound
		}
		return APICredentialIssue{}, err
	}
	if revision != expectedRevision || status != "ACTIVE" {
		return APICredentialIssue{}, ErrAdminRevisionConflict
	}
	if !expiresAt.After(actor.OccurredAt) {
		return APICredentialIssue{}, errors.New("API credential expiry must be in the future")
	}
	newCredentialID, err := newUUIDv7(actor.OccurredAt)
	if err != nil {
		return APICredentialIssue{}, err
	}
	secret, err := newAPICredentialSecret(actor.TenantID, newCredentialID)
	if err != nil {
		return APICredentialIssue{}, err
	}
	catalogRevision, err := activeCatalogRevision(ctx, tx)
	if err != nil {
		return APICredentialIssue{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE iam.api_credentials SET status='REVOKED', revision=revision+1, updated_at=$3 WHERE id=$1::uuid AND tenant_id=$2::uuid`, credentialID, actor.TenantID, actor.OccurredAt.UTC()); err != nil {
		return APICredentialIssue{}, err
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO iam.api_credentials (
 id,tenant_id,service_account_id,secret_hash,capabilities,site_ids,catalog_revision,status,expires_at,
 rotated_from_id,revision,created_by_principal_id,created_at,updated_at
) VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5,$6::uuid[],$7,'ACTIVE',$8,$9::uuid,1,$10::uuid,$11,$11)
`, newCredentialID, actor.TenantID, serviceAccountID, store.hashCredentialSecret(secret), capabilities, siteIDs, catalogRevision, expiresAt.UTC(), credentialID, actorPrincipalID, actor.OccurredAt.UTC()); err != nil {
		return APICredentialIssue{}, err
	}
	policyRevision, err := bumpAuthorizationRevision(ctx, tx, actor.TenantID, actor.OccurredAt)
	if err != nil {
		return APICredentialIssue{}, err
	}
	auditEventID, err := insertAdminAudit(ctx, tx, actor, actorPrincipalID, "api-credential.rotate", "API_CREDENTIAL", newCredentialID, policyRevision)
	if err != nil {
		return APICredentialIssue{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return APICredentialIssue{}, err
	}
	return APICredentialIssue{ServiceAccountID: serviceAccountID, CredentialID: newCredentialID, Secret: secret, Revision: 1, ExpiresAt: expiresAt.UTC(), AuditEventID: auditEventID}, nil
}

func (store *PostgresAdminStore) RevokeAPICredential(ctx context.Context, actor AdminActor, credentialID string, expectedRevision int64) (AdminMutationResult, error) {
	tx, actorPrincipalID, err := store.beginActorTransaction(ctx, actor)
	if err != nil {
		return AdminMutationResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var revision int64
	var status string
	if err := tx.QueryRow(ctx, `SELECT revision,status FROM iam.api_credentials WHERE id=$1::uuid AND tenant_id=$2::uuid FOR UPDATE`, credentialID, actor.TenantID).Scan(&revision, &status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AdminMutationResult{}, ErrAdminResourceNotFound
		}
		return AdminMutationResult{}, err
	}
	if revision != expectedRevision || status != "ACTIVE" {
		return AdminMutationResult{}, ErrAdminRevisionConflict
	}
	revision++
	if _, err := tx.Exec(ctx, `UPDATE iam.api_credentials SET status='REVOKED', revision=$3, updated_at=$4 WHERE id=$1::uuid AND tenant_id=$2::uuid`, credentialID, actor.TenantID, revision, actor.OccurredAt.UTC()); err != nil {
		return AdminMutationResult{}, err
	}
	policyRevision, err := bumpAuthorizationRevision(ctx, tx, actor.TenantID, actor.OccurredAt)
	if err != nil {
		return AdminMutationResult{}, err
	}
	auditEventID, err := insertAdminAudit(ctx, tx, actor, actorPrincipalID, "api-credential.revoke", "API_CREDENTIAL", credentialID, policyRevision)
	if err != nil {
		return AdminMutationResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AdminMutationResult{}, err
	}
	return AdminMutationResult{ResourceID: credentialID, Revision: revision, PolicyRevision: policyRevision, AuditEventID: auditEventID}, nil
}

func (store *PostgresAdminStore) VerifyAPICredential(ctx context.Context, credential string, now time.Time) (APICredentialVerification, error) {
	parts := strings.Split(credential, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return APICredentialVerification{}, ErrAPICredentialInvalid
	}
	tenantID, credentialID := parts[0], parts[1]
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return APICredentialVerification{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := setAdminTenantContext(ctx, tx, tenantID, ""); err != nil {
		return APICredentialVerification{}, ErrAPICredentialInvalid
	}
	var verification APICredentialVerification
	var storedHash, status, serviceStatus string
	var catalogRevision, activeRevision int64
	if err := tx.QueryRow(ctx, `
SELECT credential.tenant_id::text, credential.service_account_id::text, credential.secret_hash,
       credential.capabilities, credential.site_ids::text[], credential.status, credential.expires_at,
       credential.catalog_revision, account.status,
       (SELECT revision FROM iam.capability_catalog_revisions WHERE status='ACTIVE')
FROM iam.api_credentials credential
JOIN iam.service_accounts account ON account.id=credential.service_account_id
WHERE credential.id=$1::uuid AND credential.tenant_id=$2::uuid
`, credentialID, tenantID).Scan(&verification.TenantID, &verification.ServiceAccountID, &storedHash, &verification.Capabilities, &verification.SiteIDs, &status, &verification.ExpiresAt, &catalogRevision, &serviceStatus, &activeRevision); err != nil {
		return APICredentialVerification{}, ErrAPICredentialInvalid
	}
	expectedHash := store.hashCredentialSecret(credential)
	if status != "ACTIVE" || serviceStatus != "ACTIVE" || !now.UTC().Before(verification.ExpiresAt.UTC()) || catalogRevision != activeRevision || subtle.ConstantTimeCompare([]byte(storedHash), []byte(expectedHash)) != 1 {
		return APICredentialVerification{}, ErrAPICredentialInvalid
	}
	return verification, nil
}

func (store *PostgresAdminStore) beginActorTransaction(ctx context.Context, actor AdminActor) (pgx.Tx, string, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, "", err
	}
	var principalID string
	var status FactStatus
	var revision int64
	if err := tx.QueryRow(ctx, `SELECT principal_id::text,principal_status,principal_revision FROM iam.resolve_principal_identity($1,$2)`, actor.SubjectIssuer, actor.Subject).Scan(&principalID, &status, &revision); err != nil || status != FactStatusActive {
		_ = tx.Rollback(ctx)
		return nil, "", errors.New("IAM admin actor is not active")
	}
	if err := setAdminTenantContext(ctx, tx, actor.TenantID, principalID); err != nil {
		_ = tx.Rollback(ctx)
		return nil, "", err
	}
	var member bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM iam.tenant_memberships WHERE tenant_id=$1::uuid AND principal_id=$2::uuid AND status='ACTIVE' AND valid_from <= $3 AND (valid_to IS NULL OR valid_to > $3))`, actor.TenantID, principalID, actor.OccurredAt.UTC()).Scan(&member); err != nil || !member {
		_ = tx.Rollback(ctx)
		return nil, "", errors.New("IAM admin actor is not an active Tenant member")
	}
	return tx, principalID, nil
}

func setAdminTenantContext(ctx context.Context, tx pgx.Tx, tenantID, principalID string) error {
	var ignored string
	if err := tx.QueryRow(ctx, `SELECT set_config('app.tenant_id',$1,true)`, tenantID).Scan(&ignored); err != nil {
		return err
	}
	if principalID != "" {
		if err := tx.QueryRow(ctx, `SELECT set_config('app.principal_id',$1,true)`, principalID).Scan(&ignored); err != nil {
			return err
		}
	}
	return nil
}

func validateCatalogCapabilities(ctx context.Context, tx pgx.Tx, capabilities []string) error {
	if len(capabilities) == 0 {
		return errors.New("at least one capability is required")
	}
	var catalog []string
	if err := tx.QueryRow(ctx, `SELECT capabilities FROM iam.capability_catalog_revisions WHERE status='ACTIVE'`).Scan(&catalog); err != nil {
		return fmt.Errorf("read active capability catalog: %w", err)
	}
	allowed := make(map[string]struct{}, len(catalog))
	for _, capability := range catalog {
		allowed[capability] = struct{}{}
	}
	seen := make(map[string]struct{}, len(capabilities))
	for _, capability := range capabilities {
		if _, ok := allowed[capability]; !ok {
			return fmt.Errorf("capability %q is not in the active catalog", capability)
		}
		if _, duplicate := seen[capability]; duplicate {
			return fmt.Errorf("capability %q is duplicated", capability)
		}
		seen[capability] = struct{}{}
	}
	return nil
}

func activeCatalogRevision(ctx context.Context, tx pgx.Tx) (int64, error) {
	var revision int64
	if err := tx.QueryRow(ctx, `SELECT revision FROM iam.capability_catalog_revisions WHERE status='ACTIVE'`).Scan(&revision); err != nil {
		return 0, err
	}
	return revision, nil
}

func applyServiceAccountStatus(ctx context.Context, tx pgx.Tx, tenantID string, request AdminMutationRequest, now time.Time) (string, int64, error) {
	if request.ResourceID == "" || (request.Status != "ACTIVE" && request.Status != "DISABLED" && request.Status != "RETIRED") {
		return "", 0, errors.New("service account status mutation is invalid")
	}
	var revision int64
	if err := tx.QueryRow(ctx, `SELECT revision FROM iam.service_accounts WHERE id=$1::uuid AND tenant_id=$2::uuid FOR UPDATE`, request.ResourceID, tenantID).Scan(&revision); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", 0, ErrAdminResourceNotFound
		}
		return "", 0, err
	}
	if revision != request.ExpectedRevision {
		return "", 0, ErrAdminRevisionConflict
	}
	revision++
	_, err := tx.Exec(ctx, `UPDATE iam.service_accounts SET status=$3,revision=$4,updated_at=$5 WHERE id=$1::uuid AND tenant_id=$2::uuid`, request.ResourceID, tenantID, request.Status, revision, now.UTC())
	return request.ResourceID, revision, err
}

func applyTenantUpdate(ctx context.Context, tx pgx.Tx, tenantID string, request AdminMutationRequest, now time.Time) (string, int64, error) {
	if request.ResourceID != "" && request.ResourceID != tenantID {
		return "", 0, ErrAdminResourceNotFound
	}
	if strings.TrimSpace(request.DisplayName) == "" || strings.TrimSpace(request.Timezone) == "" || len(request.Currency) != 3 || len(request.Country) != 2 || (request.Status != "ACTIVE" && request.Status != "SUSPENDED") {
		return "", 0, errors.New("tenant update is invalid")
	}
	var revision int64
	if err := tx.QueryRow(ctx, `SELECT revision FROM iam.tenants WHERE id=$1::uuid FOR UPDATE`, tenantID).Scan(&revision); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", 0, ErrAdminResourceNotFound
		}
		return "", 0, err
	}
	if revision != request.ExpectedRevision {
		return "", 0, ErrAdminRevisionConflict
	}
	revision++
	_, err := tx.Exec(ctx, `UPDATE iam.tenants SET display_name=$2,timezone=$3,currency=$4,country=$5,status=$6,revision=$7,updated_at=$8 WHERE id=$1::uuid`, tenantID, strings.TrimSpace(request.DisplayName), strings.TrimSpace(request.Timezone), strings.ToUpper(request.Currency), strings.ToUpper(request.Country), request.Status, revision, now.UTC())
	return tenantID, revision, err
}

func applyPrincipalUpdate(ctx context.Context, tx pgx.Tx, request AdminMutationRequest, now time.Time) (string, int64, error) {
	if request.ResourceID == "" || strings.TrimSpace(request.DisplayName) == "" || strings.TrimSpace(request.Email) == "" || (request.Status != "ACTIVE" && request.Status != "SUSPENDED" && request.Status != "DISABLED") {
		return "", 0, errors.New("principal update is invalid")
	}
	var revision int64
	if err := tx.QueryRow(ctx, `SELECT revision FROM iam.principals WHERE id=$1::uuid FOR UPDATE`, request.ResourceID).Scan(&revision); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", 0, ErrAdminResourceNotFound
		}
		return "", 0, err
	}
	if revision != request.ExpectedRevision {
		return "", 0, ErrAdminRevisionConflict
	}
	revision++
	_, err := tx.Exec(ctx, `UPDATE iam.principals SET display_name=$2,email=$3,status=$4,revision=$5,updated_at=$6 WHERE id=$1::uuid`, request.ResourceID, strings.TrimSpace(request.DisplayName), strings.TrimSpace(request.Email), request.Status, revision, now.UTC())
	return request.ResourceID, revision, err
}

func applyMembership(ctx context.Context, tx pgx.Tx, tenantID string, request AdminMutationRequest, now time.Time) (string, int64, error) {
	if request.PrincipalID == "" || (request.Status != "ACTIVE" && request.Status != "SUSPENDED" && request.Status != "REVOKED") {
		return "", 0, errors.New("membership mutation is invalid")
	}
	validFrom := request.ValidFrom.UTC()
	if request.ValidFrom.IsZero() {
		validFrom = now.UTC()
	}
	var id string
	var revision int64
	err := tx.QueryRow(ctx, `SELECT id::text,revision FROM iam.tenant_memberships WHERE tenant_id=$1::uuid AND principal_id=$2::uuid FOR UPDATE`, tenantID, request.PrincipalID).Scan(&id, &revision)
	if errors.Is(err, pgx.ErrNoRows) {
		if request.ExpectedRevision != 0 {
			return "", 0, ErrAdminRevisionConflict
		}
		id, err = newUUIDv7(now)
		if err != nil {
			return "", 0, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO iam.tenant_memberships (id,tenant_id,principal_id,status,valid_from,valid_to,revision,created_at,updated_at) VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,1,$7,$7)`, id, tenantID, request.PrincipalID, request.Status, validFrom, request.ValidTo, now.UTC())
		return id, 1, err
	}
	if err != nil {
		return "", 0, err
	}
	if revision != request.ExpectedRevision {
		return "", 0, ErrAdminRevisionConflict
	}
	revision++
	_, err = tx.Exec(ctx, `UPDATE iam.tenant_memberships SET status=$3,valid_from=$4,valid_to=$5,revision=$6,updated_at=$7 WHERE tenant_id=$1::uuid AND principal_id=$2::uuid`, tenantID, request.PrincipalID, request.Status, validFrom, request.ValidTo, revision, now.UTC())
	return id, revision, err
}

func applyRoleTemplate(ctx context.Context, tx pgx.Tx, tenantID string, request AdminMutationRequest, now time.Time) (string, int64, error) {
	if strings.TrimSpace(request.RoleKey) == "" || strings.TrimSpace(request.DisplayName) == "" || (request.Status != "ACTIVE" && request.Status != "RETIRED") {
		return "", 0, errors.New("role template mutation is invalid")
	}
	var id string
	var revision int64
	err := tx.QueryRow(ctx, `SELECT id::text,revision FROM iam.role_templates WHERE tenant_id=$1::uuid AND role_key=$2 FOR UPDATE`, tenantID, request.RoleKey).Scan(&id, &revision)
	if errors.Is(err, pgx.ErrNoRows) {
		if request.ExpectedRevision != 0 {
			return "", 0, ErrAdminRevisionConflict
		}
		id, err = newUUIDv7(now)
		if err != nil {
			return "", 0, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO iam.role_templates (id,tenant_id,role_key,display_name,capabilities,status,revision,created_at,updated_at) VALUES ($1::uuid,$2::uuid,$3,$4,$5,$6,1,$7,$7)`, id, tenantID, request.RoleKey, strings.TrimSpace(request.DisplayName), request.Capabilities, request.Status, now.UTC())
		return id, 1, err
	}
	if err != nil {
		return "", 0, err
	}
	if revision != request.ExpectedRevision {
		return "", 0, ErrAdminRevisionConflict
	}
	revision++
	_, err = tx.Exec(ctx, `UPDATE iam.role_templates SET display_name=$3,capabilities=$4,status=$5,revision=$6,updated_at=$7 WHERE tenant_id=$1::uuid AND role_key=$2`, tenantID, request.RoleKey, strings.TrimSpace(request.DisplayName), request.Capabilities, request.Status, revision, now.UTC())
	return id, revision, err
}

func applyRoleBinding(ctx context.Context, tx pgx.Tx, tenantID string, request AdminMutationRequest, now time.Time) (string, int64, error) {
	if request.PrincipalID == "" || request.RoleTemplateID == "" || (request.Status != "ACTIVE" && request.Status != "SUSPENDED" && request.Status != "REVOKED") {
		return "", 0, errors.New("role binding mutation is invalid")
	}
	validFrom := request.ValidFrom.UTC()
	if request.ValidFrom.IsZero() {
		validFrom = now.UTC()
	}
	var id string
	var revision int64
	err := tx.QueryRow(ctx, `SELECT id::text,revision FROM iam.role_bindings WHERE tenant_id=$1::uuid AND principal_id=$2::uuid AND role_template_id=$3::uuid FOR UPDATE`, tenantID, request.PrincipalID, request.RoleTemplateID).Scan(&id, &revision)
	if errors.Is(err, pgx.ErrNoRows) {
		if request.ExpectedRevision != 0 {
			return "", 0, ErrAdminRevisionConflict
		}
		id, err = newUUIDv7(now)
		if err != nil {
			return "", 0, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO iam.role_bindings (id,tenant_id,principal_id,role_template_id,status,valid_from,valid_to,revision,created_at,updated_at) VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6,$7,1,$8,$8)`, id, tenantID, request.PrincipalID, request.RoleTemplateID, request.Status, validFrom, request.ValidTo, now.UTC())
		return id, 1, err
	}
	if err != nil {
		return "", 0, err
	}
	if revision != request.ExpectedRevision {
		return "", 0, ErrAdminRevisionConflict
	}
	revision++
	_, err = tx.Exec(ctx, `UPDATE iam.role_bindings SET status=$4,valid_from=$5,valid_to=$6,revision=$7,updated_at=$8 WHERE tenant_id=$1::uuid AND principal_id=$2::uuid AND role_template_id=$3::uuid`, tenantID, request.PrincipalID, request.RoleTemplateID, request.Status, validFrom, request.ValidTo, revision, now.UTC())
	return id, revision, err
}

func applySiteBinding(ctx context.Context, tx pgx.Tx, tenantID string, request AdminMutationRequest, now time.Time) (string, int64, error) {
	if request.PrincipalID == "" || request.SiteID == "" || (request.Status != "ACTIVE" && request.Status != "SUSPENDED" && request.Status != "REVOKED") {
		return "", 0, errors.New("site binding mutation is invalid")
	}
	validFrom := request.ValidFrom.UTC()
	if request.ValidFrom.IsZero() {
		validFrom = now.UTC()
	}
	var id string
	var revision int64
	err := tx.QueryRow(ctx, `SELECT id::text,revision FROM iam.site_bindings WHERE tenant_id=$1::uuid AND principal_id=$2::uuid AND site_id=$3::uuid FOR UPDATE`, tenantID, request.PrincipalID, request.SiteID).Scan(&id, &revision)
	if errors.Is(err, pgx.ErrNoRows) {
		if request.ExpectedRevision != 0 {
			return "", 0, ErrAdminRevisionConflict
		}
		id, err = newUUIDv7(now)
		if err != nil {
			return "", 0, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO iam.site_bindings (id,tenant_id,site_id,principal_id,actions,effect,status,valid_from,valid_to,revision,created_at,updated_at) VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,'ALLOW',$6,$7,$8,1,$9,$9)`, id, tenantID, request.SiteID, request.PrincipalID, request.Capabilities, request.Status, validFrom, request.ValidTo, now.UTC())
		return id, 1, err
	}
	if err != nil {
		return "", 0, err
	}
	if revision != request.ExpectedRevision {
		return "", 0, ErrAdminRevisionConflict
	}
	revision++
	_, err = tx.Exec(ctx, `UPDATE iam.site_bindings SET actions=$4,effect='ALLOW',status=$5,valid_from=$6,valid_to=$7,revision=$8,updated_at=$9 WHERE tenant_id=$1::uuid AND principal_id=$2::uuid AND site_id=$3::uuid`, tenantID, request.PrincipalID, request.SiteID, request.Capabilities, request.Status, validFrom, request.ValidTo, revision, now.UTC())
	return id, revision, err
}

func applyExplicitDeny(ctx context.Context, tx pgx.Tx, tenantID string, request AdminMutationRequest, now time.Time) (string, int64, error) {
	if request.PrincipalID == "" || request.DenyCapability == "" || strings.TrimSpace(request.ReasonCode) == "" {
		return "", 0, errors.New("explicit deny mutation is invalid")
	}
	validFrom := request.ValidFrom.UTC()
	if request.ValidFrom.IsZero() {
		validFrom = now.UTC()
	}
	if request.ResourceID == "" {
		if request.ExpectedRevision != 0 {
			return "", 0, ErrAdminRevisionConflict
		}
		id, err := newUUIDv7(now)
		if err != nil {
			return "", 0, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO iam.explicit_denies (id,tenant_id,site_id,principal_id,action,reason_code,valid_from,valid_to,revision,created_at,updated_at) VALUES ($1::uuid,$2::uuid,NULLIF($3,'')::uuid,$4::uuid,$5,$6,$7,$8,1,$9,$9)`, id, tenantID, request.SiteID, request.PrincipalID, request.DenyCapability, request.ReasonCode, validFrom, request.ValidTo, now.UTC())
		return id, 1, err
	}
	var revision int64
	if err := tx.QueryRow(ctx, `SELECT revision FROM iam.explicit_denies WHERE id=$1::uuid AND tenant_id=$2::uuid FOR UPDATE`, request.ResourceID, tenantID).Scan(&revision); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", 0, ErrAdminResourceNotFound
		}
		return "", 0, err
	}
	if revision != request.ExpectedRevision {
		return "", 0, ErrAdminRevisionConflict
	}
	revision++
	_, err := tx.Exec(ctx, `UPDATE iam.explicit_denies SET site_id=NULLIF($3,'')::uuid,principal_id=$4::uuid,action=$5,reason_code=$6,valid_from=$7,valid_to=$8,revision=$9,updated_at=$10 WHERE id=$1::uuid AND tenant_id=$2::uuid`, request.ResourceID, tenantID, request.SiteID, request.PrincipalID, request.DenyCapability, request.ReasonCode, validFrom, request.ValidTo, revision, now.UTC())
	return request.ResourceID, revision, err
}

func bumpAuthorizationRevision(ctx context.Context, tx pgx.Tx, tenantID string, now time.Time) (string, error) {
	var revision int64
	if err := tx.QueryRow(ctx, `UPDATE iam.authorization_revisions SET revision=revision+1,updated_at=$2 WHERE tenant_id=$1::uuid RETURNING revision`, tenantID, now.UTC()).Scan(&revision); err != nil {
		return "", err
	}
	return fmt.Sprintf("iam:%d", revision), nil
}

func insertAdminAudit(ctx context.Context, tx pgx.Tx, actor AdminActor, actorPrincipalID, action, resourceType, resourceID, policyRevision string) (string, error) {
	eventID, err := newUUIDv7(actor.OccurredAt)
	if err != nil {
		return "", err
	}
	payload := map[string]string{"eventId": eventID, "tenantId": actor.TenantID, "actorPrincipalId": actorPrincipalID, "action": action, "resourceType": resourceType, "resourceId": resourceID, "outcome": "SUCCEEDED", "policyRevision": policyRevision, "occurredAt": actor.OccurredAt.UTC().Format(time.RFC3339Nano)}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	if _, err := tx.Exec(ctx, `INSERT INTO iam.admin_audit_intents (event_id,tenant_id,actor_principal_id,action,resource_type,resource_id,outcome,policy_revision,correlation_id,trace_id,payload_sha256,occurred_at) VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,'SUCCEEDED',$7,$8,$9,$10,$11)`, eventID, actor.TenantID, actorPrincipalID, action, resourceType, resourceID, policyRevision, actor.CorrelationID, actor.TraceID, hex.EncodeToString(digest[:]), actor.OccurredAt.UTC()); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO iam.admin_outbox (message_id,tenant_id,topic,payload,created_at) VALUES ($1::uuid,$2::uuid,'audit.events',$3::jsonb,$4)`, eventID, actor.TenantID, encoded, actor.OccurredAt.UTC()); err != nil {
		return "", err
	}
	return eventID, nil
}

func adminResourceType(operation string) string {
	switch operation {
	case "tenant.update":
		return "TENANT"
	case "principal.update":
		return "PRINCIPAL"
	case "membership.upsert":
		return "TENANT_MEMBERSHIP"
	case "role-template.upsert":
		return "ROLE_TEMPLATE"
	case "role-binding.upsert":
		return "ROLE_BINDING"
	case "site-binding.upsert":
		return "SITE_BINDING"
	case "explicit-deny.upsert":
		return "EXPLICIT_DENY"
	case "service-account.status":
		return "SERVICE_ACCOUNT"
	default:
		return "IAM_RESOURCE"
	}
}

func newAPICredentialSecret(tenantID, credentialID string) (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return tenantID + "." + credentialID + "." + base64.RawURLEncoding.EncodeToString(buffer), nil
}

func (store *PostgresAdminStore) hashCredentialSecret(secret string) string {
	mac := hmac.New(sha256.New, store.pepper)
	_, _ = mac.Write([]byte(secret))
	return hex.EncodeToString(mac.Sum(nil))
}
