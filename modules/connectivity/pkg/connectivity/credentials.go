package connectivity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

var (
	ErrEnrollmentRejected  = errors.New("enrollment code or CSR rejected")
	ErrCredentialForbidden = errors.New("gateway credential scope forbidden")
	ErrCredentialRevoked   = errors.New("gateway identity has been revoked")
)

type EnrollmentCode struct {
	EnrollmentCode string    `json:"enrollmentCode"`
	ExpiresAt      time.Time `json:"expiresAt"`
}
type IssuedCredential struct {
	CredentialID   string    `json:"credentialId,omitempty"`
	SerialNumber   string    `json:"serialNumber,omitempty"`
	CertificatePEM string    `json:"certificatePem"`
	CAPEM          string    `json:"caPem"`
	BrokerURL      string    `json:"brokerUrl"`
	ExpiresAt      time.Time `json:"expiresAt"`
}
type CredentialStatus struct {
	Status    string     `json:"status"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}
type GatewayCredentialRecord struct {
	ID                string     `json:"id"`
	GatewayID         string     `json:"gatewayId"`
	FingerprintSHA256 string     `json:"fingerprintSha256"`
	SerialNumber      *string    `json:"serialNumber,omitempty"`
	Status            string     `json:"status"`
	ValidFrom         time.Time  `json:"validFrom"`
	ValidUntil        time.Time  `json:"validUntil"`
	RevokedAt         *time.Time `json:"revokedAt,omitempty"`
	RevocationReason  *string    `json:"revocationReason,omitempty"`
	CreatedAt         time.Time  `json:"createdAt"`
}
type CredentialService struct {
	store               *Store
	authority           *CertificateAuthority
	revokeMu            sync.RWMutex
	onRevoke            func(gatewayID string)
	onCertificateRevoke func(gatewayID, fingerprintSHA256 string, hasRemainingActive bool)
}

func NewCredentialService(store *Store, authority *CertificateAuthority) *CredentialService {
	return &CredentialService{store: store, authority: authority}
}

// SetRevokeHook registers a callback invoked right after a Gateway credential revocation commits.
func (service *CredentialService) SetRevokeHook(hook func(gatewayID string)) {
	service.revokeMu.Lock()
	defer service.revokeMu.Unlock()
	service.onRevoke = hook
}

// SetCertificateRevokeHook registers a callback invoked right after a single Gateway certificate
// revocation (or atomic rotation) commits, passing the revoked cert fingerprint and whether any
// active non-expired certificate still remains for that Gateway.
func (service *CredentialService) SetCertificateRevokeHook(hook func(gatewayID, fingerprintSHA256 string, hasRemainingActive bool)) {
	service.revokeMu.Lock()
	defer service.revokeMu.Unlock()
	service.onCertificateRevoke = hook
}

func (service *CredentialService) notifyCertificateRevoked(gatewayID, fingerprintSHA256 string, hasRemainingActive bool) {
	service.revokeMu.RLock()
	hook := service.onCertificateRevoke
	service.revokeMu.RUnlock()
	if hook != nil {
		hook(gatewayID, fingerprintSHA256, hasRemainingActive)
	}
}
func hashCredential(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

// Gateway identity is resolved by the owner read port, never by caller-supplied Tenant.
func (service *CredentialService) gateway(ctx context.Context, gatewayID string) (tenant, site string, err error) {
	if !gatewayIDPattern.MatchString(gatewayID) {
		return "", "", ErrEnrollmentRejected
	}
	err = service.store.pool.QueryRow(ctx, `SELECT tenant_id::text,site_id::text FROM core_registry.gateway_directory_v1 WHERE gateway_id=$1::uuid AND status='ACTIVE'`, gatewayID).Scan(&tenant, &site)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return
}
func (service *CredentialService) operator(ctx context.Context, claims registryauth.GrantClaims, gatewayID string) (pgx.Tx, error) {
	tenant, site, err := service.gateway(ctx, gatewayID)
	if err != nil {
		return nil, err
	}
	if tenant != claims.TenantID || !registryauth.ScopeAllows(claims, site) {
		return nil, ErrCredentialForbidden
	}
	return service.beginTenant(ctx, tenant)
}

// Credential mutations serialize on the one Gateway enrollment row. Read
// committed lets a waiting request observe the winner's consumed/revoked state.
func (service *CredentialService) beginTenant(ctx context.Context, tenant string) (pgx.Tx, error) {
	tx, err := service.store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, err
	}
	if err := setTenant(ctx, tx, tenant); err != nil {
		tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}
func acquireEnrollmentAdvisoryLock(ctx context.Context, tx pgx.Tx, gatewayID string) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('gateway_enrollment:' || $1, 0))`, gatewayID); err != nil {
		return fmt.Errorf("lock gateway enrollment: %w", err)
	}
	return nil
}

func lockEnrollment(ctx context.Context, tx pgx.Tx, gatewayID string) error {
	if err := acquireEnrollmentAdvisoryLock(ctx, tx, gatewayID); err != nil {
		return err
	}
	var revoked *time.Time
	err := tx.QueryRow(ctx, `SELECT revoked_at FROM connectivity.gateway_enrollments WHERE gateway_id=$1::uuid FOR UPDATE`, gatewayID).Scan(&revoked)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrEnrollmentRejected
	}
	if err != nil {
		return err
	}
	if revoked != nil {
		return ErrCredentialRevoked
	}
	return nil
}

func (service *CredentialService) GenerateEnrollmentCode(ctx context.Context, claims registryauth.GrantClaims, gatewayID string) (EnrollmentCode, error) {
	tx, err := service.operator(ctx, claims, gatewayID)
	if err != nil {
		return EnrollmentCode{}, err
	}
	defer tx.Rollback(ctx)
	if err = acquireEnrollmentAdvisoryLock(ctx, tx, gatewayID); err != nil {
		return EnrollmentCode{}, err
	}
	var raw [32]byte
	if _, err = rand.Read(raw[:]); err != nil {
		return EnrollmentCode{}, err
	}
	code := base64.RawURLEncoding.EncodeToString(raw[:])
	now := service.store.clock().UTC()
	expiry := now.Add(24 * time.Hour)
	tag, err := tx.Exec(ctx, `INSERT INTO connectivity.gateway_enrollments(tenant_id,gateway_id,code_sha256,expires_at,consumed_at,revoked_at,created_at,updated_at)
VALUES($1::uuid,$2::uuid,$3,$4,NULL,NULL,$5,$5) ON CONFLICT(tenant_id,gateway_id) DO UPDATE SET code_sha256=EXCLUDED.code_sha256,expires_at=EXCLUDED.expires_at,consumed_at=NULL,updated_at=EXCLUDED.updated_at WHERE connectivity.gateway_enrollments.revoked_at IS NULL`, claims.TenantID, gatewayID, hashCredential([]byte(code)), expiry, now)
	if err != nil {
		return EnrollmentCode{}, err
	}
	if tag.RowsAffected() == 0 {
		return EnrollmentCode{}, ErrCredentialRevoked
	}
	if err = credentialAudit(ctx, tx, claims.TenantID, gatewayID, claims.PrincipalID, "CODE_GENERATED", now); err != nil {
		return EnrollmentCode{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return EnrollmentCode{}, err
	}
	return EnrollmentCode{code, expiry}, nil
}

func (service *CredentialService) Enroll(ctx context.Context, code, csrPEM string) (IssuedCredential, error) {
	if len(code) != 43 {
		return IssuedCredential{}, ErrEnrollmentRejected
	}
	csr, err := parseGatewayCSR(csrPEM)
	if err != nil {
		return IssuedCredential{}, err
	}
	gatewayID := csr.Subject.CommonName
	tenant, _, err := service.gateway(ctx, gatewayID)
	if err != nil {
		return IssuedCredential{}, ErrEnrollmentRejected
	}
	tx, err := service.beginTenant(ctx, tenant)
	if err != nil {
		return IssuedCredential{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockEnrollment(ctx, tx, gatewayID); err != nil {
		return IssuedCredential{}, err
	}
	now := service.store.clock().UTC()
	tag, err := tx.Exec(ctx, `UPDATE connectivity.gateway_enrollments SET consumed_at=$3,updated_at=$3 WHERE gateway_id=$1::uuid AND code_sha256=$2 AND consumed_at IS NULL AND expires_at>$3 AND revoked_at IS NULL`, gatewayID, hashCredential([]byte(code)), now)
	if err != nil {
		return IssuedCredential{}, err
	}
	if tag.RowsAffected() != 1 {
		return IssuedCredential{}, ErrEnrollmentRejected
	}
	return service.persistIssued(ctx, tx, tenant, gatewayID, gatewayID, "ISSUED", csr, now, "")
}

// RotateEnrollment issues a fresh one-time 24-hour enrollment code for lossless key rotation,
// keeping existing active certificates valid; if the Gateway identity was previously revoked,
// the identity is re-enabled and audited as IDENTITY_REENABLED while prior revoked certs stay REVOKED.
func (service *CredentialService) RotateEnrollment(ctx context.Context, claims registryauth.GrantClaims, gatewayID string) (EnrollmentCode, error) {
	tx, err := service.operator(ctx, claims, gatewayID)
	if err != nil {
		return EnrollmentCode{}, err
	}
	defer tx.Rollback(ctx)
	if err = acquireEnrollmentAdvisoryLock(ctx, tx, gatewayID); err != nil {
		return EnrollmentCode{}, err
	}
	var existingRevoked *time.Time
	err = tx.QueryRow(ctx, `SELECT revoked_at FROM connectivity.gateway_enrollments WHERE gateway_id=$1::uuid FOR UPDATE`, gatewayID).Scan(&existingRevoked)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return EnrollmentCode{}, err
	}
	wasRevoked := err == nil && existingRevoked != nil

	var raw [32]byte
	if _, err = rand.Read(raw[:]); err != nil {
		return EnrollmentCode{}, err
	}
	code := base64.RawURLEncoding.EncodeToString(raw[:])
	now := service.store.clock().UTC()
	expiry := now.Add(24 * time.Hour)
	_, err = tx.Exec(ctx, `INSERT INTO connectivity.gateway_enrollments(tenant_id,gateway_id,code_sha256,expires_at,consumed_at,revoked_at,created_at,updated_at)
VALUES($1::uuid,$2::uuid,$3,$4,NULL,NULL,$5,$5) ON CONFLICT(tenant_id,gateway_id) DO UPDATE SET code_sha256=EXCLUDED.code_sha256,expires_at=EXCLUDED.expires_at,consumed_at=NULL,revoked_at=NULL,updated_at=EXCLUDED.updated_at`,
		claims.TenantID, gatewayID, hashCredential([]byte(code)), expiry, now)
	if err != nil {
		return EnrollmentCode{}, err
	}
	if wasRevoked {
		if err = credentialAudit(ctx, tx, claims.TenantID, gatewayID, claims.PrincipalID, "IDENTITY_REENABLED", now); err != nil {
			return EnrollmentCode{}, err
		}
	}
	if err = credentialAudit(ctx, tx, claims.TenantID, gatewayID, claims.PrincipalID, "CODE_GENERATED", now); err != nil {
		return EnrollmentCode{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return EnrollmentCode{}, err
	}
	if service.store != nil {
		service.store.InvalidateGatewayCache(gatewayID)
	}
	return EnrollmentCode{EnrollmentCode: code, ExpiresAt: expiry}, nil
}

func (service *CredentialService) verifyPeerCertificate(ctx context.Context, tx pgx.Tx, peer *x509.Certificate, now time.Time) error {
	roots := x509.NewCertPool()
	roots.AddCert(service.authority.certificate)
	if _, err := peer.Verify(x509.VerifyOptions{Roots: roots, CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		return ErrEnrollmentRejected
	}
	var active bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM connectivity.gateway_credentials WHERE gateway_id=$1::uuid AND certificate_fingerprint_sha256=$2 AND status='ACTIVE' AND valid_from<=$3 AND valid_until>$3)`, peer.Subject.CommonName, hashCredential(peer.Raw), now).Scan(&active); err != nil {
		return err
	}
	if !active {
		return ErrEnrollmentRejected
	}
	return nil
}

func (service *CredentialService) VerifyActiveCredential(ctx context.Context, peer *x509.Certificate) (string, error) {
	if peer == nil {
		return "", ErrEnrollmentRejected
	}
	gatewayID := peer.Subject.CommonName
	tenant, _, err := service.gateway(ctx, gatewayID)
	if err != nil {
		return "", ErrEnrollmentRejected
	}
	tx, err := service.beginTenant(ctx, tenant)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	var revoked *time.Time
	err = tx.QueryRow(ctx, `SELECT revoked_at FROM connectivity.gateway_enrollments WHERE gateway_id=$1::uuid`, gatewayID).Scan(&revoked)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrEnrollmentRejected
	}
	if err != nil {
		return "", err
	}
	if revoked != nil {
		return "", ErrCredentialRevoked
	}
	now := service.store.clock().UTC()
	if err := service.verifyPeerCertificate(ctx, tx, peer, now); err != nil {
		return "", err
	}
	return gatewayID, nil
}

func (service *CredentialService) Renew(ctx context.Context, peer *x509.Certificate, csrPEM string) (IssuedCredential, error) {
	return service.RenewWithOptions(ctx, peer, csrPEM, false)
}

func (service *CredentialService) RenewWithOptions(ctx context.Context, peer *x509.Certificate, csrPEM string, revokePeer bool) (IssuedCredential, error) {
	if peer == nil {
		return IssuedCredential{}, ErrEnrollmentRejected
	}
	csr, err := parseGatewayCSR(csrPEM)
	if err != nil || csr.Subject.CommonName != peer.Subject.CommonName {
		return IssuedCredential{}, ErrEnrollmentRejected
	}
	tenant, _, err := service.gateway(ctx, peer.Subject.CommonName)
	if err != nil {
		return IssuedCredential{}, ErrEnrollmentRejected
	}
	tx, err := service.beginTenant(ctx, tenant)
	if err != nil {
		return IssuedCredential{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockEnrollment(ctx, tx, peer.Subject.CommonName); err != nil {
		return IssuedCredential{}, err
	}
	now := service.store.clock().UTC()
	if err = service.verifyPeerCertificate(ctx, tx, peer, now); err != nil {
		return IssuedCredential{}, err
	}
	revokeFingerprint := ""
	if revokePeer {
		revokeFingerprint = hashCredential(peer.Raw)
	}
	return service.persistIssued(ctx, tx, tenant, peer.Subject.CommonName, peer.Subject.CommonName, "RENEWED", csr, now, revokeFingerprint)
}

func (service *CredentialService) persistIssued(ctx context.Context, tx pgx.Tx, tenant, gateway, actor, action string, csr *x509.CertificateRequest, now time.Time, revokePeerFingerprint string) (IssuedCredential, error) {
	issued, cert, err := service.authority.issue(gateway, csr, now)
	if err != nil {
		return IssuedCredential{}, err
	}
	id, err := newUUIDv7(now)
	if err != nil {
		return IssuedCredential{}, err
	}
	issued.CredentialID = id
	_, err = tx.Exec(ctx, `INSERT INTO connectivity.gateway_credentials(id,tenant_id,gateway_id,certificate_fingerprint_sha256,certificate_serial_hex,status,valid_from,valid_until,created_at,updated_at) VALUES($1,$2,$3,$4,$5,'ACTIVE',$6,$7,$6,$6)`,
		id, tenant, gateway, hashCredential(cert.Raw), issued.SerialNumber, now, cert.NotAfter)
	if err != nil {
		return IssuedCredential{}, err
	}
	if err = credentialAuditWithID(ctx, tx, tenant, gateway, actor, action, &id, now); err != nil {
		return IssuedCredential{}, err
	}
	if revokePeerFingerprint != "" {
		var revokedID string
		err = tx.QueryRow(ctx, `UPDATE connectivity.gateway_credentials SET status='REVOKED',revoked_at=$3,revocation_reason=$4,updated_at=$3 WHERE gateway_id=$1::uuid AND certificate_fingerprint_sha256=$2 AND status='ACTIVE' RETURNING id::text`,
			gateway, revokePeerFingerprint, now, "rotated_on_renewal").Scan(&revokedID)
		if errors.Is(err, pgx.ErrNoRows) {
			return IssuedCredential{}, ErrEnrollmentRejected
		}
		if err != nil {
			return IssuedCredential{}, err
		}
		if err = credentialAuditWithID(ctx, tx, tenant, gateway, actor, "CERTIFICATE_REVOKED", &revokedID, now); err != nil {
			return IssuedCredential{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return IssuedCredential{}, err
	}
	if service.store != nil {
		service.store.InvalidateGatewayCache(gateway)
	}
	if revokePeerFingerprint != "" {
		service.notifyCertificateRevoked(gateway, revokePeerFingerprint, true)
	}
	return issued, nil
}

func (service *CredentialService) Revoke(ctx context.Context, claims registryauth.GrantClaims, gateway string) error {
	tx, err := service.operator(ctx, claims, gateway)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	return service.revokeGatewayInTx(ctx, tx, claims.TenantID, gateway, claims.PrincipalID)
}

// RevokeRetiredGateway revokes enrollments, active certificates, and tunnel sessions for a gateway
// that may already be marked RETIRED in Core Registry.
func (service *CredentialService) RevokeRetiredGateway(ctx context.Context, tenantID, gatewayID string) error {
	tenant := strings.TrimSpace(tenantID)
	gateway := strings.TrimSpace(gatewayID)
	if !tunnelAuditUUIDv7Pattern.MatchString(tenant) || !tunnelAuditUUIDv7Pattern.MatchString(gateway) {
		return ErrEnrollmentRejected
	}
	if service == nil || service.store == nil || service.store.pool == nil {
		return errors.New("credential service is not configured")
	}
	var resolvedTenant string
	err := service.store.pool.QueryRow(ctx, `SELECT tenant_id::text FROM core_registry.gateway_directory_v1 WHERE gateway_id=$1::uuid LIMIT 1`, gateway).Scan(&resolvedTenant)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if strings.TrimSpace(resolvedTenant) != tenant {
		return ErrCredentialForbidden
	}
	tx, err := service.store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT set_config('app.tenant_id',$1,true)`, tenant); err != nil {
		return err
	}
	return service.revokeGatewayInTx(ctx, tx, tenant, gateway, gateway)
}

func (service *CredentialService) revokeGatewayInTx(ctx context.Context, tx pgx.Tx, tenantID, gatewayID, actorID string) error {
	if err := acquireEnrollmentAdvisoryLock(ctx, tx, gatewayID); err != nil {
		return err
	}
	now := service.store.clock().UTC()
	_, err := tx.Exec(ctx, `INSERT INTO connectivity.gateway_enrollments(tenant_id,gateway_id,revoked_at,created_at,updated_at) VALUES($1,$2,$3,$3,$3) ON CONFLICT(tenant_id,gateway_id) DO UPDATE SET revoked_at=EXCLUDED.revoked_at,code_sha256=NULL,expires_at=NULL,consumed_at=NULL,updated_at=EXCLUDED.updated_at`, tenantID, gatewayID, now)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE connectivity.gateway_credentials SET status='REVOKED',revoked_at=$2,updated_at=$2 WHERE gateway_id=$1::uuid AND status='ACTIVE'`, gatewayID, now)
	if err != nil {
		return err
	}
	if err = credentialAudit(ctx, tx, tenantID, gatewayID, actorID, "REVOKED", now); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	if service.store != nil {
		service.store.InvalidateGatewayCache(gatewayID)
	}
	service.revokeMu.RLock()
	hook := service.onRevoke
	service.revokeMu.RUnlock()
	if hook != nil {
		hook(gatewayID)
	}
	return nil
}

func (service *CredentialService) Status(ctx context.Context, claims registryauth.GrantClaims, gateway string) (CredentialStatus, error) {
	tx, err := service.operator(ctx, claims, gateway)
	if err != nil {
		return CredentialStatus{}, err
	}
	defer tx.Rollback(ctx)
	var revoked bool
	var expiry *time.Time
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM connectivity.gateway_enrollments WHERE gateway_id=$1::uuid AND revoked_at IS NOT NULL), (SELECT max(valid_until) FROM connectivity.gateway_credentials WHERE gateway_id=$1::uuid AND status='ACTIVE')`, gateway).Scan(&revoked, &expiry)
	if err != nil {
		return CredentialStatus{}, err
	}
	if expiry != nil {
		utc := expiry.UTC()
		expiry = &utc
	}
	status := "NOT_ENROLLED"
	now := service.store.clock().UTC()
	if revoked {
		status = "REVOKED"
	} else if expiry != nil {
		if !expiry.After(now) {
			status = "EXPIRED"
		} else if expiry.Before(now.Add(14 * 24 * time.Hour)) {
			status = "EXPIRING"
		} else {
			status = "ACTIVE"
		}
	}
	return CredentialStatus{status, expiry}, nil
}

func (service *CredentialService) ListCredentials(ctx context.Context, claims registryauth.GrantClaims, gatewayID string) ([]GatewayCredentialRecord, error) {
	tx, err := service.operator(ctx, claims, gatewayID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT id::text,gateway_id::text,certificate_fingerprint_sha256,certificate_serial_hex,status,valid_from,valid_until,revoked_at,revocation_reason,created_at FROM connectivity.gateway_credentials WHERE gateway_id=$1::uuid ORDER BY created_at DESC,id DESC`, gatewayID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := make([]GatewayCredentialRecord, 0)
	for rows.Next() {
		var record GatewayCredentialRecord
		if err := rows.Scan(&record.ID, &record.GatewayID, &record.FingerprintSHA256, &record.SerialNumber, &record.Status, &record.ValidFrom, &record.ValidUntil, &record.RevokedAt, &record.RevocationReason, &record.CreatedAt); err != nil {
			return nil, err
		}
		records = append(records, normalizeCredentialRecordUTC(record))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

func normalizeCredentialRecordUTC(record GatewayCredentialRecord) GatewayCredentialRecord {
	record.ValidFrom = record.ValidFrom.UTC()
	record.ValidUntil = record.ValidUntil.UTC()
	record.CreatedAt = record.CreatedAt.UTC()
	if record.RevokedAt != nil {
		revokedUTC := record.RevokedAt.UTC()
		record.RevokedAt = &revokedUTC
	}
	return record
}

// RevokeCertificate revokes a single certificate row without mutating gateway_enrollments.revoked_at
// or other active certificates held by the same Gateway.
func (service *CredentialService) RevokeCertificate(ctx context.Context, claims registryauth.GrantClaims, gatewayID, credentialID, reason string) (GatewayCredentialRecord, error) {
	if !gatewayIDPattern.MatchString(credentialID) {
		return GatewayCredentialRecord{}, ErrEnrollmentRejected
	}
	trimmedReason := strings.TrimSpace(reason)
	if utf8.RuneCountInString(trimmedReason) > 512 {
		return GatewayCredentialRecord{}, ErrEnrollmentRejected
	}
	var reasonPtr *string
	if trimmedReason != "" {
		reasonPtr = &trimmedReason
	}
	tx, err := service.operator(ctx, claims, gatewayID)
	if err != nil {
		return GatewayCredentialRecord{}, err
	}
	defer tx.Rollback(ctx)

	// Match lock order with Renew/Revoke (advisory lock -> gateway_enrollments -> gateway_credentials).
	if lockErr := acquireEnrollmentAdvisoryLock(ctx, tx, gatewayID); lockErr != nil {
		return GatewayCredentialRecord{}, lockErr
	}
	var unusedRevoked *time.Time
	if lockErr := tx.QueryRow(ctx, `SELECT revoked_at FROM connectivity.gateway_enrollments WHERE gateway_id=$1::uuid FOR UPDATE`, gatewayID).Scan(&unusedRevoked); lockErr != nil && !errors.Is(lockErr, pgx.ErrNoRows) {
		return GatewayCredentialRecord{}, lockErr
	}

	var record GatewayCredentialRecord
	err = tx.QueryRow(ctx, `SELECT id::text,gateway_id::text,certificate_fingerprint_sha256,certificate_serial_hex,status,valid_from,valid_until,revoked_at,revocation_reason,created_at FROM connectivity.gateway_credentials WHERE id=$1::uuid AND gateway_id=$2::uuid FOR UPDATE`, credentialID, gatewayID).
		Scan(&record.ID, &record.GatewayID, &record.FingerprintSHA256, &record.SerialNumber, &record.Status, &record.ValidFrom, &record.ValidUntil, &record.RevokedAt, &record.RevocationReason, &record.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return GatewayCredentialRecord{}, ErrNotFound
	}
	if err != nil {
		return GatewayCredentialRecord{}, err
	}
	if record.Status != "ACTIVE" {
		return normalizeCredentialRecordUTC(record), nil
	}

	now := service.store.clock().UTC()
	if err := tx.QueryRow(ctx, `UPDATE connectivity.gateway_credentials SET status='REVOKED',revoked_at=$3,revocation_reason=$4,updated_at=$3 WHERE id=$1::uuid AND gateway_id=$2::uuid AND status='ACTIVE' RETURNING status,revoked_at,revocation_reason`,
		credentialID, gatewayID, now, reasonPtr).Scan(&record.Status, &record.RevokedAt, &record.RevocationReason); err != nil {
		return GatewayCredentialRecord{}, err
	}
	if err := credentialAuditWithID(ctx, tx, claims.TenantID, gatewayID, claims.PrincipalID, "CERTIFICATE_REVOKED", &record.ID, now); err != nil {
		return GatewayCredentialRecord{}, err
	}
	var hasRemainingActive bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM connectivity.gateway_credentials WHERE gateway_id=$1::uuid AND status='ACTIVE' AND valid_from<=$2 AND valid_until>$2)`, gatewayID, now).Scan(&hasRemainingActive); err != nil {
		return GatewayCredentialRecord{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return GatewayCredentialRecord{}, err
	}
	if service.store != nil {
		service.store.InvalidateGatewayCache(gatewayID)
	}
	service.notifyCertificateRevoked(gatewayID, record.FingerprintSHA256, hasRemainingActive)
	return normalizeCredentialRecordUTC(record), nil
}

// ExportCRL signs a PEM CRL containing non-expired revoked certificate serials across the fleet.
func (service *CredentialService) ExportCRL(ctx context.Context) ([]byte, error) {
	if service == nil || service.authority == nil || service.store == nil || service.store.pool == nil {
		return nil, errors.New("credential service is not configured")
	}
	now := service.store.clock().UTC()
	rows, err := service.store.pool.Query(ctx, `SELECT certificate_serial_hex, revoked_at FROM connectivity.list_revoked_certificate_serials($1)`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := make([]pkix.RevokedCertificate, 0)
	for rows.Next() {
		var serialHex string
		var revokedAt time.Time
		if err := rows.Scan(&serialHex, &revokedAt); err != nil {
			return nil, err
		}
		serial, ok := new(big.Int).SetString(strings.TrimSpace(serialHex), 16)
		if !ok || serial.Sign() <= 0 {
			continue
		}
		entries = append(entries, pkix.RevokedCertificate{
			SerialNumber:   serial,
			RevocationTime: revokedAt.UTC(),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return service.authority.CreateCRL(entries, now)
}

func credentialAudit(ctx context.Context, tx pgx.Tx, tenant, gateway, actor, action string, now time.Time) error {
	return credentialAuditWithID(ctx, tx, tenant, gateway, actor, action, nil, now)
}

func credentialAuditWithID(ctx context.Context, tx pgx.Tx, tenant, gateway, actor, action string, credentialID *string, now time.Time) error {
	id, err := newUUIDv7(now)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO connectivity.gateway_credential_audit(id,tenant_id,gateway_id,actor_id,action,credential_id,occurred_at) VALUES($1,$2,$3,$4,$5,$6::uuid,$7)`, id, tenant, gateway, actor, action, credentialID, now)
	return err
}
