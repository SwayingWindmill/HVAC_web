package connectivity

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/services/command-dispatcher/pkg/mqttconnector"
)

var (
	ErrNotFound            = errors.New("connectivity record not found")
	ErrBindingNotFound     = errors.New("connectivity binding not found")
	ErrOwnershipHeld       = errors.New("connector ownership is held by another owner")
	ErrOwnershipLost       = errors.New("connector ownership lease is not active")
	ErrCredentialInactive  = errors.New("credential is not active")
	ErrEnrollmentInvalid   = errors.New("enrollment is invalid or already consumed")
	ErrCorrelationMismatch = errors.New("command correlation does not match durable state")

	uuidV7Pattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
)

type Store struct {
	pool     *pgxpool.Pool
	tenantID string
	now      func() time.Time
}

type IntegrationDescriptor struct {
	ID                       string
	TenantID                 string
	SiteID                   string
	GatewayExternalID        string
	IntegrationRevision      uint64
	TransportProfileID       string
	TransportProfileRevision uint64
	BrokerOrigin             string
	TopicNamespace           string
}

type ChildBinding struct {
	DeviceID          string
	ExternalDeviceID  string
	BindingRevision   uint64
	IntegrationID     string
	GatewayExternalID string
}

type OwnershipLease struct {
	OwnerID    string
	Generation uint64
	LeaseUntil time.Time
	Revision   uint64
}

type CredentialRefInput struct {
	ID                     string
	IntegrationInstanceID  string
	Kind                   string
	SecretRef              string
	CertificateFingerprint string
	TokenHash              string
	ValidFrom              time.Time
	ValidUntil             time.Time
	RotatedFromID          string
}

type SessionInput struct {
	ID                    string
	IntegrationInstanceID string
	CredentialRefID       string
	GatewayExternalID     string
	ExpiresAt             time.Time
}

type EnrollmentConsumeInput struct {
	EnrollmentID         string
	HardwareIdentityHash string
	ChallengeHash        string
	Credential           CredentialRefInput
}

func Open(ctx context.Context, databaseURL, tenantID string) (*Store, error) {
	databaseURL = strings.TrimSpace(databaseURL)
	tenantID = strings.TrimSpace(tenantID)
	if databaseURL == "" || !uuidV7Pattern.MatchString(tenantID) {
		return nil, errors.New("connectivity database configuration is invalid")
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, errors.New("parse connectivity database URL")
	}
	config.MaxConns = 8
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, errors.New("open connectivity database")
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, errors.New("connectivity database is unavailable")
	}
	return &Store{pool: pool, tenantID: tenantID, now: time.Now}, nil
}

func (store *Store) Close() {
	if store != nil && store.pool != nil {
		store.pool.Close()
	}
}

func (store *Store) LoadIntegration(ctx context.Context, integrationInstanceID string) (IntegrationDescriptor, error) {
	if store == nil || store.pool == nil || !uuidV7Pattern.MatchString(strings.TrimSpace(integrationInstanceID)) {
		return IntegrationDescriptor{}, ErrNotFound
	}
	tx, err := store.beginTenant(ctx)
	if err != nil {
		return IntegrationDescriptor{}, err
	}
	defer tx.Rollback(ctx)
	var descriptor IntegrationDescriptor
	err = tx.QueryRow(ctx, `
SELECT i.id::text, i.tenant_id::text, i.site_id::text, i.gateway_external_id, i.revision,
       p.id::text, p.revision, p.broker_origin, p.topic_namespace
FROM connectivity.integration_instances i
JOIN connectivity.transport_profiles p
  ON p.tenant_id = i.tenant_id AND p.id = i.transport_profile_id
WHERE i.tenant_id = $1::uuid AND i.id = $2::uuid
  AND i.status = 'ACTIVE' AND p.status = 'ACTIVE' AND p.protocol = 'MQTT'
`, store.tenantID, strings.TrimSpace(integrationInstanceID)).Scan(
		&descriptor.ID, &descriptor.TenantID, &descriptor.SiteID, &descriptor.GatewayExternalID, &descriptor.IntegrationRevision,
		&descriptor.TransportProfileID, &descriptor.TransportProfileRevision, &descriptor.BrokerOrigin, &descriptor.TopicNamespace,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return IntegrationDescriptor{}, ErrNotFound
	}
	if err != nil {
		return IntegrationDescriptor{}, fmt.Errorf("load IntegrationInstance: %w", err)
	}
	return descriptor, nil
}

func (store *Store) AuthorizeGateway(ctx context.Context, integrationInstanceID, tenantID, siteID, gatewayExternalID string) error {
	if tenantID != store.tenantID {
		return ErrBindingNotFound
	}
	tx, err := store.beginTenant(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	now := store.clock().UTC()
	var active bool
	err = tx.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1
  FROM connectivity.integration_instances i
  JOIN connectivity.sessions s
    ON s.tenant_id = i.tenant_id AND s.integration_instance_id = i.id
  JOIN connectivity.credential_refs c
    ON c.tenant_id = s.tenant_id AND c.id = s.credential_ref_id
  WHERE i.tenant_id = $1::uuid AND i.id = $2::uuid
    AND i.site_id = $3::uuid AND i.gateway_external_id = $4 AND i.status = 'ACTIVE'
    AND s.gateway_external_id = i.gateway_external_id AND s.status = 'ACTIVE'
    AND s.opened_at <= $5 AND s.expires_at > $5
    AND c.integration_instance_id = i.id AND c.status = 'ACTIVE'
    AND c.valid_from <= $5 AND c.valid_until > $5
)
`, store.tenantID, integrationInstanceID, strings.TrimSpace(siteID), strings.TrimSpace(gatewayExternalID), now).Scan(&active)
	if err != nil {
		return fmt.Errorf("authorize Gateway session: %w", err)
	}
	if !active {
		return ErrBindingNotFound
	}
	return nil
}

func (store *Store) AuthorizeGatewayChild(ctx context.Context, integrationInstanceID, gatewayExternalID, externalDeviceID string) error {
	_, err := store.ResolveGatewayChildByExternal(ctx, integrationInstanceID, gatewayExternalID, externalDeviceID)
	return err
}

func (store *Store) ResolveGatewayChildByExternal(ctx context.Context, integrationInstanceID, gatewayExternalID, externalDeviceID string) (ChildBinding, error) {
	tx, err := store.beginTenant(ctx)
	if err != nil {
		return ChildBinding{}, err
	}
	defer tx.Rollback(ctx)
	now := store.clock().UTC()
	var binding ChildBinding
	err = tx.QueryRow(ctx, `
SELECT child_device_id::text, child_external_id, revision, integration_instance_id::text, gateway_external_id
FROM connectivity.gateway_child_bindings
WHERE tenant_id = $1::uuid AND integration_instance_id = $2::uuid
  AND gateway_external_id = $3 AND child_external_id = $4
  AND status = 'ACTIVE' AND valid_from <= $5 AND (valid_to IS NULL OR valid_to > $5)
`, store.tenantID, integrationInstanceID, strings.TrimSpace(gatewayExternalID), strings.TrimSpace(externalDeviceID), now).Scan(
		&binding.DeviceID, &binding.ExternalDeviceID, &binding.BindingRevision, &binding.IntegrationID, &binding.GatewayExternalID,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return ChildBinding{}, ErrBindingNotFound
	}
	if err != nil {
		return ChildBinding{}, fmt.Errorf("resolve GatewayChildBinding: %w", err)
	}
	return binding, nil
}

func (store *Store) ResolveCommandRoute(ctx context.Context, integrationInstanceID, tenantID, siteID, gatewayID, deviceID string) (mqttconnector.DeviceRoute, error) {
	if tenantID != store.tenantID {
		return mqttconnector.DeviceRoute{}, ErrBindingNotFound
	}
	tx, err := store.beginTenant(ctx)
	if err != nil {
		return mqttconnector.DeviceRoute{}, err
	}
	defer tx.Rollback(ctx)
	now := store.clock().UTC()
	var externalDeviceID string
	var revision uint64
	err = tx.QueryRow(ctx, `
SELECT b.child_external_id, b.revision
FROM connectivity.gateway_child_bindings b
JOIN connectivity.integration_instances i
  ON i.tenant_id = b.tenant_id AND i.id = b.integration_instance_id
WHERE b.tenant_id = $1::uuid AND b.integration_instance_id = $2::uuid
  AND b.site_id = $3::uuid AND b.gateway_external_id = $4 AND b.child_device_id = $5::uuid
  AND b.status = 'ACTIVE' AND b.valid_from <= $6 AND (b.valid_to IS NULL OR b.valid_to > $6)
  AND i.status = 'ACTIVE' AND i.site_id = b.site_id AND i.gateway_external_id = b.gateway_external_id
  AND EXISTS (
    SELECT 1
    FROM connectivity.sessions s
    JOIN connectivity.credential_refs c
      ON c.tenant_id = s.tenant_id AND c.id = s.credential_ref_id
    WHERE s.tenant_id = i.tenant_id AND s.integration_instance_id = i.id
      AND s.gateway_external_id = i.gateway_external_id AND s.status = 'ACTIVE'
      AND s.opened_at <= $6 AND s.expires_at > $6
      AND c.integration_instance_id = i.id AND c.status = 'ACTIVE'
      AND c.valid_from <= $6 AND c.valid_until > $6
  )
`, store.tenantID, integrationInstanceID, siteID, strings.TrimSpace(gatewayID), deviceID, now).Scan(&externalDeviceID, &revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return mqttconnector.DeviceRoute{}, ErrBindingNotFound
	}
	if err != nil {
		return mqttconnector.DeviceRoute{}, fmt.Errorf("resolve command GatewayChildBinding: %w", err)
	}
	return mqttconnector.DeviceRoute{ExternalDeviceID: externalDeviceID, BindingRevision: revision}, nil
}

func (store *Store) ClaimConnectorOwnership(ctx context.Context, integrationInstanceID, ownerID string, leaseFor time.Duration) (OwnershipLease, error) {
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" || leaseFor < 5*time.Second || leaseFor > 2*time.Minute {
		return OwnershipLease{}, errors.New("connector ownership request is invalid")
	}
	tx, err := store.beginTenant(ctx)
	if err != nil {
		return OwnershipLease{}, err
	}
	defer tx.Rollback(ctx)
	now := store.clock().UTC()
	leaseUntil := now.Add(leaseFor)
	var lease OwnershipLease
	err = tx.QueryRow(ctx, `
INSERT INTO connectivity.connector_ownership_leases (
  integration_instance_id, tenant_id, owner_id, lease_generation, lease_until, revision, updated_at
) VALUES ($1::uuid, $2::uuid, $3, 1, $4, 1, $5)
ON CONFLICT (integration_instance_id) DO UPDATE
SET owner_id = EXCLUDED.owner_id,
    lease_generation = CASE
      WHEN connectivity.connector_ownership_leases.owner_id = EXCLUDED.owner_id THEN connectivity.connector_ownership_leases.lease_generation
      ELSE connectivity.connector_ownership_leases.lease_generation + 1
    END,
    lease_until = EXCLUDED.lease_until,
    revision = connectivity.connector_ownership_leases.revision + 1,
    updated_at = EXCLUDED.updated_at
WHERE connectivity.connector_ownership_leases.owner_id = EXCLUDED.owner_id
   OR connectivity.connector_ownership_leases.lease_until <= EXCLUDED.updated_at
RETURNING owner_id, lease_generation, lease_until, revision
`, integrationInstanceID, store.tenantID, ownerID, leaseUntil, now).Scan(&lease.OwnerID, &lease.Generation, &lease.LeaseUntil, &lease.Revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return OwnershipLease{}, ErrOwnershipHeld
	}
	if err != nil {
		return OwnershipLease{}, fmt.Errorf("claim connector ownership: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return OwnershipLease{}, fmt.Errorf("commit connector ownership: %w", err)
	}
	return lease, nil
}

func (store *Store) AssertConnectorOwnership(ctx context.Context, integrationInstanceID, ownerID string, generation uint64) error {
	tx, err := store.beginTenant(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var active bool
	err = tx.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1 FROM connectivity.connector_ownership_leases
  WHERE tenant_id = $1::uuid AND integration_instance_id = $2::uuid
    AND owner_id = $3 AND lease_generation = $4 AND lease_until > $5
)
`, store.tenantID, integrationInstanceID, strings.TrimSpace(ownerID), generation, store.clock().UTC()).Scan(&active)
	if err != nil {
		return fmt.Errorf("check connector ownership: %w", err)
	}
	if !active {
		return ErrOwnershipLost
	}
	return nil
}

func (store *Store) RevokeCredential(ctx context.Context, credentialRefID, reason string) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return errors.New("credential revocation reason is required")
	}
	tx, err := store.beginTenant(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	now := store.clock().UTC()
	var integrationID, siteID string
	var revision uint64
	err = tx.QueryRow(ctx, `
UPDATE connectivity.credential_refs c
SET status = 'REVOKED', revoked_at = $3, revision = revision + 1, updated_at = $3
FROM connectivity.integration_instances i
WHERE c.tenant_id = $1::uuid AND c.id = $2::uuid AND c.status = 'ACTIVE'
  AND i.tenant_id = c.tenant_id AND i.id = c.integration_instance_id
RETURNING c.integration_instance_id::text, i.site_id::text, c.revision
`, store.tenantID, credentialRefID, now).Scan(&integrationID, &siteID, &revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrCredentialInactive
	}
	if err != nil {
		return fmt.Errorf("revoke CredentialRef: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE connectivity.sessions
SET status = 'INVALIDATED', closed_at = $3, close_reason = $4, revision = revision + 1, updated_at = $3
WHERE tenant_id = $1::uuid AND credential_ref_id = $2::uuid AND status = 'ACTIVE'
`, store.tenantID, credentialRefID, now, "CREDENTIAL_REVOKED:"+reason); err != nil {
		return fmt.Errorf("invalidate credential sessions: %w", err)
	}
	if err := store.insertAudit(ctx, tx, siteID, integrationID, "CREDENTIAL_REVOKED", credentialRefID, revision, map[string]any{"reason": reason}, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (store *Store) ExpireDueCredentials(ctx context.Context) (int64, error) {
	tx, err := store.beginTenant(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	now := store.clock().UTC()
	tag, err := tx.Exec(ctx, `
UPDATE connectivity.credential_refs
SET status = 'EXPIRED', revision = revision + 1, updated_at = $2
WHERE tenant_id = $1::uuid AND status = 'ACTIVE' AND valid_until <= $2
`, store.tenantID, now)
	if err != nil {
		return 0, fmt.Errorf("expire CredentialRef: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE connectivity.sessions s
SET status = 'EXPIRED', closed_at = $2, close_reason = 'CREDENTIAL_EXPIRED', revision = revision + 1, updated_at = $2
WHERE s.tenant_id = $1::uuid AND s.status = 'ACTIVE'
  AND (s.expires_at <= $2 OR EXISTS (
    SELECT 1 FROM connectivity.credential_refs c
    WHERE c.tenant_id = s.tenant_id AND c.id = s.credential_ref_id AND c.status <> 'ACTIVE'
  ))
`, store.tenantID, now); err != nil {
		return 0, fmt.Errorf("expire connectivity sessions: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (store *Store) OpenSession(ctx context.Context, input SessionInput) error {
	if !uuidV7Pattern.MatchString(input.ID) || !uuidV7Pattern.MatchString(input.CredentialRefID) || input.ExpiresAt.IsZero() {
		return errors.New("session input is invalid")
	}
	tx, err := store.beginTenant(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	now := store.clock().UTC()
	var siteID string
	var credentialRevision uint64
	var credentialValidUntil time.Time
	err = tx.QueryRow(ctx, `
SELECT i.site_id::text, c.revision, c.valid_until
FROM connectivity.credential_refs c
JOIN connectivity.integration_instances i ON i.tenant_id = c.tenant_id AND i.id = c.integration_instance_id
WHERE c.tenant_id = $1::uuid AND c.id = $2::uuid AND c.integration_instance_id = $3::uuid
  AND c.status = 'ACTIVE' AND c.valid_from <= $4 AND c.valid_until > $4
  AND i.status = 'ACTIVE' AND i.gateway_external_id = $5
`, store.tenantID, input.CredentialRefID, input.IntegrationInstanceID, now, strings.TrimSpace(input.GatewayExternalID)).Scan(&siteID, &credentialRevision, &credentialValidUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrCredentialInactive
	}
	if err != nil {
		return fmt.Errorf("validate session CredentialRef: %w", err)
	}
	if !input.ExpiresAt.After(now) || input.ExpiresAt.After(credentialValidUntil) {
		return errors.New("session expiry must be within the active credential lifetime")
	}
	if _, err := tx.Exec(ctx, `
UPDATE connectivity.sessions
SET status = 'CLOSED', closed_at = $4, close_reason = 'SESSION_REPLACED', revision = revision + 1, updated_at = $4
WHERE tenant_id = $1::uuid AND integration_instance_id = $2::uuid AND gateway_external_id = $3 AND status = 'ACTIVE'
`, store.tenantID, input.IntegrationInstanceID, strings.TrimSpace(input.GatewayExternalID), now); err != nil {
		return fmt.Errorf("close prior Gateway session: %w", err)
	}
	_, err = tx.Exec(ctx, `
INSERT INTO connectivity.sessions (
  id, tenant_id, site_id, integration_instance_id, credential_ref_id, credential_revision,
  gateway_external_id, status, opened_at, expires_at, closed_at, close_reason, revision, updated_at
) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, $6, $7, 'ACTIVE', $8, $9, NULL, NULL, 1, $8)
`, input.ID, store.tenantID, siteID, input.IntegrationInstanceID, input.CredentialRefID, credentialRevision, strings.TrimSpace(input.GatewayExternalID), now, input.ExpiresAt.UTC())
	if err != nil {
		return fmt.Errorf("open connectivity session: %w", err)
	}
	return tx.Commit(ctx)
}

func (store *Store) ConsumeEnrollment(ctx context.Context, input EnrollmentConsumeInput) error {
	if !uuidV7Pattern.MatchString(input.EnrollmentID) || !validCredentialInput(input.Credential) ||
		!isSHA256(input.HardwareIdentityHash) || !isSHA256(input.ChallengeHash) {
		return ErrEnrollmentInvalid
	}
	tx, err := store.beginTenant(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	now := store.clock().UTC()
	var siteID, integrationID, deviceID string
	var gatewayExternalID *string
	err = tx.QueryRow(ctx, `
SELECT e.site_id::text, e.integration_instance_id::text, e.device_id::text, e.gateway_external_id
FROM connectivity.enrollments e
WHERE e.tenant_id = $1::uuid AND e.id = $2::uuid
  AND e.consumed_at IS NULL AND e.expires_at > $3
  AND e.hardware_identity_sha256 = $4 AND e.challenge_hash_sha256 = $5
  AND EXISTS (
    SELECT 1 FROM connectivity.device_bindings d
    WHERE d.tenant_id = e.tenant_id AND d.integration_instance_id = e.integration_instance_id
      AND d.site_id = e.site_id AND d.device_id = e.device_id
      AND d.status = 'ACTIVE' AND d.valid_from <= $3 AND (d.valid_to IS NULL OR d.valid_to > $3)
    UNION ALL
    SELECT 1 FROM connectivity.gateway_child_bindings g
    WHERE g.tenant_id = e.tenant_id AND g.integration_instance_id = e.integration_instance_id
      AND g.site_id = e.site_id AND g.child_device_id = e.device_id
      AND e.gateway_external_id IS NOT NULL AND g.gateway_external_id = e.gateway_external_id
      AND g.status = 'ACTIVE' AND g.valid_from <= $3 AND (g.valid_to IS NULL OR g.valid_to > $3)
  )
FOR UPDATE
`, store.tenantID, input.EnrollmentID, now, input.HardwareIdentityHash, input.ChallengeHash).Scan(&siteID, &integrationID, &deviceID, &gatewayExternalID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrEnrollmentInvalid
	}
	if err != nil {
		return fmt.Errorf("load Enrollment: %w", err)
	}
	if input.Credential.IntegrationInstanceID != integrationID {
		return ErrEnrollmentInvalid
	}
	if err := store.insertCredential(ctx, tx, input.Credential, now); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `
UPDATE connectivity.enrollments
SET consumed_at = $3, credential_ref_id = $4::uuid, revision = revision + 1, updated_at = $3
WHERE tenant_id = $1::uuid AND id = $2::uuid AND consumed_at IS NULL
`, store.tenantID, input.EnrollmentID, now, input.Credential.ID)
	if err != nil || tag.RowsAffected() != 1 {
		return ErrEnrollmentInvalid
	}
	gateway := ""
	if gatewayExternalID != nil {
		gateway = *gatewayExternalID
	}
	if err := store.insertAudit(ctx, tx, siteID, integrationID, "ENROLLMENT_CONSUMED", deviceID, 1, map[string]any{
		"enrollmentId": input.EnrollmentID, "credentialRefId": input.Credential.ID, "gatewayExternalId": gateway,
	}, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (store *Store) RotateCredential(ctx context.Context, oldCredentialRefID string, replacement CredentialRefInput, overlapUntil time.Time) error {
	if !validCredentialInput(replacement) || strings.TrimSpace(oldCredentialRefID) == "" || replacement.RotatedFromID != oldCredentialRefID ||
		overlapUntil.Before(replacement.ValidFrom) || overlapUntil.After(replacement.ValidFrom.Add(15*time.Minute)) {
		return errors.New("credential rotation input or overlap window is invalid")
	}
	tx, err := store.beginTenant(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	now := store.clock().UTC()
	var siteID, integrationID string
	var oldRevision uint64
	err = tx.QueryRow(ctx, `
SELECT i.site_id::text, c.integration_instance_id::text, c.revision
FROM connectivity.credential_refs c
JOIN connectivity.integration_instances i ON i.tenant_id = c.tenant_id AND i.id = c.integration_instance_id
WHERE c.tenant_id = $1::uuid AND c.id = $2::uuid AND c.status = 'ACTIVE'
FOR UPDATE OF c
`, store.tenantID, oldCredentialRefID).Scan(&siteID, &integrationID, &oldRevision)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrCredentialInactive
	}
	if err != nil {
		return fmt.Errorf("load rotation source CredentialRef: %w", err)
	}
	if integrationID != replacement.IntegrationInstanceID {
		return errors.New("replacement CredentialRef changes IntegrationInstance")
	}
	if err := store.insertCredential(ctx, tx, replacement, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
UPDATE connectivity.credential_refs
SET valid_until = LEAST(valid_until, $3), revision = revision + 1, updated_at = $4
WHERE tenant_id = $1::uuid AND id = $2::uuid
`, store.tenantID, oldCredentialRefID, overlapUntil.UTC(), now); err != nil {
		return fmt.Errorf("bound credential rotation overlap: %w", err)
	}
	if err := store.insertAudit(ctx, tx, siteID, integrationID, "CREDENTIAL_ROTATED", replacement.ID, oldRevision+1, map[string]any{
		"rotatedFromCredentialRefId": oldCredentialRefID,
	}, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (store *Store) PrepareCommandCorrelation(ctx context.Context, correlation mqttconnector.CommandCorrelation) (mqttconnector.CommandCorrelation, error) {
	if correlation.Envelope.TenantID != store.tenantID || correlation.State != mqttconnector.CorrelationPrepared {
		return mqttconnector.CommandCorrelation{}, ErrCorrelationMismatch
	}
	tx, err := store.beginTenant(ctx)
	if err != nil {
		return mqttconnector.CommandCorrelation{}, err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `
INSERT INTO connectivity.command_reply_correlations (
  attempt_id, execution_fence, command_id, tenant_id, site_id, integration_instance_id,
  device_id, point_id, capability, external_device_id, payload_hash, lease_owner, lease_until,
  owner_generation, mapping_revision, binding_revision, provider_endpoint, provider_method,
  request_sha256, state, prepared_at, updated_at
) VALUES (
  $1::uuid, $2, $3::uuid, $4::uuid, $5::uuid, $6::uuid,
  $7::uuid, $8::uuid, $9, $10, $11, $12, $13,
  $14, $15, $16, $17, $18, $19, 'PREPARED', $20, $20
)
ON CONFLICT (attempt_id, execution_fence) DO NOTHING
`, correlation.Envelope.AttemptID, correlation.Envelope.ExecutionFence, correlation.Envelope.CommandID,
		store.tenantID, correlation.Envelope.SiteID, correlation.IntegrationInstanceID,
		correlation.Envelope.DeviceID, correlation.Envelope.PointID, string(correlation.Envelope.Capability), correlation.ExternalDeviceID,
		correlation.Envelope.PayloadHash, correlation.Envelope.LeaseOwner, correlation.Envelope.LeaseUntil.UTC(),
		correlation.OwnerGeneration, correlation.MappingRevision, correlation.BindingRevision,
		correlation.ProviderEndpoint, correlation.ProviderMethod, correlation.RequestSHA256, correlation.PreparedAt.UTC())
	if err != nil {
		return mqttconnector.CommandCorrelation{}, fmt.Errorf("prepare durable command correlation: %w", err)
	}
	existing, err := loadCorrelation(ctx, tx, store.tenantID, correlation.Envelope.AttemptID, correlation.Envelope.ExecutionFence)
	if err != nil {
		return mqttconnector.CommandCorrelation{}, err
	}
	if !sameCorrelationIdentity(existing, correlation) {
		return mqttconnector.CommandCorrelation{}, ErrCorrelationMismatch
	}
	if err := tx.Commit(ctx); err != nil {
		return mqttconnector.CommandCorrelation{}, err
	}
	return existing, nil
}

func (store *Store) ArmCommandCorrelation(ctx context.Context, attemptID string, executionFence, ownerGeneration uint64, armedAt time.Time) error {
	tx, err := store.beginTenant(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `
UPDATE connectivity.command_reply_correlations c
SET state = 'MAY_COMMIT', commit_armed_at = $5, updated_at = $5
FROM connectivity.connector_ownership_leases l
WHERE c.tenant_id = $1::uuid AND c.attempt_id = $2::uuid AND c.execution_fence = $3
  AND c.owner_generation = $4 AND c.state = 'PREPARED'
  AND l.tenant_id = c.tenant_id AND l.integration_instance_id = c.integration_instance_id
  AND l.lease_generation = c.owner_generation AND l.lease_until > $5
`, store.tenantID, attemptID, executionFence, ownerGeneration, armedAt.UTC())
	if err != nil {
		return fmt.Errorf("arm durable command correlation: %w", err)
	}
	if tag.RowsAffected() == 0 {
		correlation, loadErr := loadCorrelation(ctx, tx, store.tenantID, attemptID, executionFence)
		if loadErr != nil || correlation.OwnerGeneration != ownerGeneration || correlation.State == mqttconnector.CorrelationPrepared {
			return ErrCorrelationMismatch
		}
	}
	return tx.Commit(ctx)
}

func (store *Store) RecordCommandReply(ctx context.Context, integrationInstanceID, commandID string, executionFence uint64, replySHA256, replyStatus string, replyEventTime time.Time, replyReasonCode string, edgeExecution *commandmodel.EdgeExecutionEvidence, repliedAt time.Time) (mqttconnector.CommandCorrelation, error) {
	var edgeExecutionJSON []byte
	if edgeExecution != nil {
		if !edgeExecution.Valid() {
			return mqttconnector.CommandCorrelation{}, ErrCorrelationMismatch
		}
		var err error
		edgeExecutionJSON, err = json.Marshal(edgeExecution)
		if err != nil {
			return mqttconnector.CommandCorrelation{}, ErrCorrelationMismatch
		}
	}
	tx, err := store.beginTenant(ctx)
	if err != nil {
		return mqttconnector.CommandCorrelation{}, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `
UPDATE connectivity.command_reply_correlations
SET state = 'REPLIED', reply_sha256 = $5, reply_status = $6,
    reply_event_time = $7,
    reply_reason_code = NULLIF($8, ''), reply_execution_evidence = $9::jsonb,
    replied_at = $10, updated_at = $10
WHERE tenant_id = $1::uuid AND integration_instance_id = $2::uuid
  AND command_id = $3::uuid AND execution_fence = $4 AND state = 'MAY_COMMIT'
`, store.tenantID, integrationInstanceID, commandID, executionFence, replySHA256, strings.TrimSpace(replyStatus), nullableTime(replyEventTime), strings.TrimSpace(replyReasonCode), nullableJSON(edgeExecutionJSON), repliedAt.UTC())
	if err != nil {
		return mqttconnector.CommandCorrelation{}, fmt.Errorf("record durable command reply: %w", err)
	}
	correlation, err := loadCorrelationByCommand(ctx, tx, store.tenantID, integrationInstanceID, commandID, executionFence)
	if err != nil {
		return mqttconnector.CommandCorrelation{}, err
	}
	if tag.RowsAffected() == 0 && correlation.State != mqttconnector.CorrelationReplied && correlation.State != mqttconnector.CorrelationResolved {
		return mqttconnector.CommandCorrelation{}, ErrCorrelationMismatch
	}
	if correlation.ReplySHA256 != replySHA256 || correlation.ReplyStatus != strings.TrimSpace(replyStatus) ||
		!sameEdgeExecution(correlation.EdgeExecution, edgeExecution) {
		return mqttconnector.CommandCorrelation{}, ErrCorrelationMismatch
	}
	if err := tx.Commit(ctx); err != nil {
		return mqttconnector.CommandCorrelation{}, err
	}
	return correlation, nil
}

func (store *Store) RecoverCommandReplies(ctx context.Context, integrationInstanceID string, limit int) ([]mqttconnector.CommandCorrelation, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	tx, err := store.beginTenant(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, correlationSelect+`
WHERE tenant_id = $1::uuid AND integration_instance_id = $2::uuid AND state = 'REPLIED'
ORDER BY replied_at, attempt_id
LIMIT $3
`, store.tenantID, integrationInstanceID, limit)
	if err != nil {
		return nil, fmt.Errorf("load recoverable command replies: %w", err)
	}
	defer rows.Close()
	var out []mqttconnector.CommandCorrelation
	for rows.Next() {
		correlation, scanErr := scanCorrelation(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, correlation)
	}
	return out, rows.Err()
}

func (store *Store) MarkCommandCorrelationResolved(ctx context.Context, attemptID string, executionFence uint64, resolvedAt time.Time) error {
	tx, err := store.beginTenant(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `
UPDATE connectivity.command_reply_correlations
SET state = 'RESOLVED', resolved_at = $4, updated_at = $4
WHERE tenant_id = $1::uuid AND attempt_id = $2::uuid AND execution_fence = $3 AND state = 'REPLIED'
`, store.tenantID, attemptID, executionFence, resolvedAt.UTC())
	if err != nil {
		return fmt.Errorf("resolve durable command correlation: %w", err)
	}
	if tag.RowsAffected() == 0 {
		correlation, loadErr := loadCorrelation(ctx, tx, store.tenantID, attemptID, executionFence)
		if loadErr != nil || correlation.State != mqttconnector.CorrelationResolved {
			return ErrCorrelationMismatch
		}
	}
	return tx.Commit(ctx)
}

func (store *Store) beginTenant(ctx context.Context) (pgx.Tx, error) {
	if store == nil || store.pool == nil {
		return nil, errors.New("connectivity store is closed")
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true)`, store.tenantID); err != nil {
		tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}

func (store *Store) insertCredential(ctx context.Context, tx pgx.Tx, credential CredentialRefInput, now time.Time) error {
	_, err := tx.Exec(ctx, `
INSERT INTO connectivity.credential_refs (
  id, tenant_id, integration_instance_id, credential_kind, secret_ref,
  certificate_fingerprint_sha256, token_hash_sha256, status,
  valid_from, valid_until, rotated_from_id, revoked_at, revision, created_at, updated_at
) VALUES ($1::uuid, $2::uuid, $3::uuid, $4, NULLIF($5, ''), NULLIF($6, ''), NULLIF($7, ''), 'ACTIVE', $8, $9, NULLIF($10, '')::uuid, NULL, 1, $11, $11)
`, credential.ID, store.tenantID, credential.IntegrationInstanceID, credential.Kind, strings.TrimSpace(credential.SecretRef),
		strings.ToLower(strings.TrimSpace(credential.CertificateFingerprint)), strings.ToLower(strings.TrimSpace(credential.TokenHash)),
		credential.ValidFrom.UTC(), credential.ValidUntil.UTC(), strings.TrimSpace(credential.RotatedFromID), now)
	if err != nil {
		return fmt.Errorf("insert CredentialRef: %w", err)
	}
	return nil
}

func validCredentialInput(input CredentialRefInput) bool {
	if !uuidV7Pattern.MatchString(strings.TrimSpace(input.ID)) || !uuidV7Pattern.MatchString(strings.TrimSpace(input.IntegrationInstanceID)) ||
		input.ValidFrom.IsZero() || input.ValidUntil.IsZero() || !input.ValidUntil.After(input.ValidFrom) {
		return false
	}
	if input.RotatedFromID != "" && !uuidV7Pattern.MatchString(strings.TrimSpace(input.RotatedFromID)) {
		return false
	}
	switch strings.TrimSpace(input.Kind) {
	case "MTLS_CERTIFICATE":
		return isSHA256(input.CertificateFingerprint) && strings.TrimSpace(input.TokenHash) == ""
	case "TOKEN_HASH":
		return isSHA256(input.TokenHash) && strings.TrimSpace(input.CertificateFingerprint) == ""
	default:
		return false
	}
}

func isSHA256(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

const correlationSelect = `
SELECT attempt_id::text, execution_fence, command_id::text, tenant_id::text, site_id::text,
       integration_instance_id::text, device_id::text, point_id::text, capability, external_device_id,
       payload_hash, lease_owner, lease_until, owner_generation, mapping_revision, binding_revision,
       provider_endpoint, provider_method, request_sha256, prepared_at, state,
       COALESCE(reply_sha256, ''), COALESCE(reply_status, ''), reply_event_time,
       COALESCE(reply_reason_code, ''), reply_execution_evidence, replied_at
FROM connectivity.command_reply_correlations
`

type rowScanner interface {
	Scan(dest ...any) error
}

func loadCorrelation(ctx context.Context, tx pgx.Tx, tenantID, attemptID string, fence uint64) (mqttconnector.CommandCorrelation, error) {
	return scanCorrelation(tx.QueryRow(ctx, correlationSelect+`
WHERE tenant_id = $1::uuid AND attempt_id = $2::uuid AND execution_fence = $3
`, tenantID, attemptID, fence))
}

func loadCorrelationByCommand(ctx context.Context, tx pgx.Tx, tenantID, integrationID, commandID string, fence uint64) (mqttconnector.CommandCorrelation, error) {
	return scanCorrelation(tx.QueryRow(ctx, correlationSelect+`
WHERE tenant_id = $1::uuid AND integration_instance_id = $2::uuid AND command_id = $3::uuid AND execution_fence = $4
`, tenantID, integrationID, commandID, fence))
}

func scanCorrelation(row rowScanner) (mqttconnector.CommandCorrelation, error) {
	var correlation mqttconnector.CommandCorrelation
	var state, capability string
	var replyEventTime, repliedAt *time.Time
	var edgeExecutionJSON []byte
	err := row.Scan(
		&correlation.Envelope.AttemptID, &correlation.Envelope.ExecutionFence, &correlation.Envelope.CommandID,
		&correlation.Envelope.TenantID, &correlation.Envelope.SiteID, &correlation.IntegrationInstanceID,
		&correlation.Envelope.DeviceID, &correlation.Envelope.PointID, &capability, &correlation.ExternalDeviceID,
		&correlation.Envelope.PayloadHash, &correlation.Envelope.LeaseOwner, &correlation.Envelope.LeaseUntil,
		&correlation.OwnerGeneration, &correlation.MappingRevision, &correlation.BindingRevision,
		&correlation.ProviderEndpoint, &correlation.ProviderMethod, &correlation.RequestSHA256, &correlation.PreparedAt,
		&state, &correlation.ReplySHA256, &correlation.ReplyStatus, &replyEventTime, &correlation.ReplyReasonCode, &edgeExecutionJSON, &repliedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return mqttconnector.CommandCorrelation{}, ErrNotFound
	}
	if err != nil {
		return mqttconnector.CommandCorrelation{}, fmt.Errorf("scan command correlation: %w", err)
	}
	correlation.Envelope.Capability = commandmodel.Capability(capability)
	correlation.State = mqttconnector.CorrelationState(state)
	if len(edgeExecutionJSON) > 0 {
		var evidence commandmodel.EdgeExecutionEvidence
		if err := json.Unmarshal(edgeExecutionJSON, &evidence); err != nil || !evidence.Valid() {
			return mqttconnector.CommandCorrelation{}, ErrCorrelationMismatch
		}
		correlation.EdgeExecution = &evidence
	}
	if replyEventTime != nil {
		correlation.ReplyEventTime = replyEventTime.UTC()
	}
	if repliedAt != nil {
		correlation.RepliedAt = repliedAt.UTC()
	}
	return correlation, nil
}

func sameCorrelationIdentity(left, right mqttconnector.CommandCorrelation) bool {
	return left.Envelope.AttemptID == right.Envelope.AttemptID && left.Envelope.ExecutionFence == right.Envelope.ExecutionFence &&
		left.Envelope.CommandID == right.Envelope.CommandID && left.Envelope.TenantID == right.Envelope.TenantID &&
		left.Envelope.SiteID == right.Envelope.SiteID && left.Envelope.DeviceID == right.Envelope.DeviceID &&
		left.Envelope.PointID == right.Envelope.PointID && left.Envelope.Capability == right.Envelope.Capability &&
		left.Envelope.PayloadHash == right.Envelope.PayloadHash &&
		left.Envelope.LeaseOwner == right.Envelope.LeaseOwner && left.IntegrationInstanceID == right.IntegrationInstanceID &&
		left.ExternalDeviceID == right.ExternalDeviceID && left.OwnerGeneration == right.OwnerGeneration &&
		left.MappingRevision == right.MappingRevision && left.BindingRevision == right.BindingRevision &&
		left.ProviderEndpoint == right.ProviderEndpoint && left.ProviderMethod == right.ProviderMethod &&
		left.RequestSHA256 == right.RequestSHA256
}

func (store *Store) insertAudit(ctx context.Context, tx pgx.Tx, siteID, integrationID, eventType, subjectID string, revision uint64, evidence map[string]any, occurredAt time.Time) error {
	eventID, err := newUUIDv7(occurredAt)
	if err != nil {
		return err
	}
	evidenceJSON, err := json.Marshal(evidence)
	if err != nil {
		return fmt.Errorf("marshal connectivity audit evidence: %w", err)
	}
	_, err = tx.Exec(ctx, `
INSERT INTO connectivity.audit_facts (
  event_id, tenant_id, site_id, integration_instance_id, event_type, subject_id, revision, evidence, occurred_at
) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6, $7, $8::jsonb, $9)
`, eventID, store.tenantID, siteID, integrationID, eventType, subjectID, revision, evidenceJSON, occurredAt.UTC())
	if err != nil {
		return fmt.Errorf("insert connectivity audit fact: %w", err)
	}
	return nil
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.UTC()
}

func nullableJSON(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return string(value)
}

func sameEdgeExecution(left, right *commandmodel.EdgeExecutionEvidence) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
}

func newUUIDv7(now time.Time) (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	millis := uint64(now.UTC().UnixMilli())
	raw[0] = byte(millis >> 40)
	raw[1] = byte(millis >> 32)
	raw[2] = byte(millis >> 24)
	raw[3] = byte(millis >> 16)
	raw[4] = byte(millis >> 8)
	raw[5] = byte(millis)
	raw[6] = (raw[6] & 0x0f) | 0x70
	raw[8] = (raw[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(raw[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32], nil
}

func (store *Store) clock() time.Time {
	if store != nil && store.now != nil {
		return store.now()
	}
	return time.Now()
}
