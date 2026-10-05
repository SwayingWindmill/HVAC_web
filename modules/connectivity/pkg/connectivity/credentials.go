package connectivity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

var (
	ErrEnrollmentRejected  = errors.New("enrollment code or CSR rejected")
	ErrCredentialForbidden = errors.New("Gateway credential scope forbidden")
	ErrCredentialRevoked   = errors.New("Gateway identity has been revoked")
)

type EnrollmentCode struct {
	EnrollmentCode string    `json:"enrollmentCode"`
	ExpiresAt      time.Time `json:"expiresAt"`
}
type IssuedCredential struct {
	CertificatePEM string    `json:"certificatePem"`
	CAPEM          string    `json:"caPem"`
	BrokerURL      string    `json:"brokerUrl"`
	ExpiresAt      time.Time `json:"expiresAt"`
}
type CredentialStatus struct {
	Status    string     `json:"status"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}
type CredentialService struct {
	store     *Store
	authority *CertificateAuthority
}

func NewCredentialService(store *Store, authority *CertificateAuthority) *CredentialService {
	return &CredentialService{store, authority}
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
func lockEnrollment(ctx context.Context, tx pgx.Tx, gatewayID string) error {
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
	return service.persistIssued(ctx, tx, tenant, gatewayID, gatewayID, "ISSUED", csr, now)
}

func (service *CredentialService) Renew(ctx context.Context, peer *x509.Certificate, csrPEM string) (IssuedCredential, error) {
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
	roots := x509.NewCertPool()
	roots.AddCert(service.authority.certificate)
	if _, err = peer.Verify(x509.VerifyOptions{Roots: roots, CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		return IssuedCredential{}, ErrEnrollmentRejected
	}
	var active bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM connectivity.gateway_credentials WHERE gateway_id=$1::uuid AND certificate_fingerprint_sha256=$2 AND status='ACTIVE' AND valid_from<=$3 AND valid_until>$3)`, peer.Subject.CommonName, hashCredential(peer.Raw), now).Scan(&active); err != nil {
		return IssuedCredential{}, err
	}
	if !active {
		return IssuedCredential{}, ErrEnrollmentRejected
	}
	return service.persistIssued(ctx, tx, tenant, peer.Subject.CommonName, peer.Subject.CommonName, "RENEWED", csr, now)
}

func (service *CredentialService) persistIssued(ctx context.Context, tx pgx.Tx, tenant, gateway, actor, action string, csr *x509.CertificateRequest, now time.Time) (IssuedCredential, error) {
	issued, cert, err := service.authority.issue(gateway, csr, now)
	if err != nil {
		return IssuedCredential{}, err
	}
	id, err := newUUIDv7(now)
	if err != nil {
		return IssuedCredential{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO connectivity.gateway_credentials(id,tenant_id,gateway_id,certificate_fingerprint_sha256,status,valid_from,valid_until,created_at,updated_at) VALUES($1,$2,$3,$4,'ACTIVE',$5,$6,$5,$5)`, id, tenant, gateway, hashCredential(cert.Raw), now, cert.NotAfter)
	if err != nil {
		return IssuedCredential{}, err
	}
	if err = credentialAudit(ctx, tx, tenant, gateway, actor, action, now); err != nil {
		return IssuedCredential{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return IssuedCredential{}, err
	}
	return issued, nil
}

func (service *CredentialService) Revoke(ctx context.Context, claims registryauth.GrantClaims, gateway string) error {
	tx, err := service.operator(ctx, claims, gateway)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	now := service.store.clock().UTC()
	_, err = tx.Exec(ctx, `INSERT INTO connectivity.gateway_enrollments(tenant_id,gateway_id,revoked_at,created_at,updated_at) VALUES($1,$2,$3,$3,$3) ON CONFLICT(tenant_id,gateway_id) DO UPDATE SET revoked_at=EXCLUDED.revoked_at,code_sha256=NULL,expires_at=NULL,consumed_at=NULL,updated_at=EXCLUDED.updated_at`, claims.TenantID, gateway, now)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE connectivity.gateway_credentials SET status='REVOKED',revoked_at=$2,updated_at=$2 WHERE gateway_id=$1::uuid AND status='ACTIVE'`, gateway, now)
	if err != nil {
		return err
	}
	if err = credentialAudit(ctx, tx, claims.TenantID, gateway, claims.PrincipalID, "REVOKED", now); err != nil {
		return err
	}
	return tx.Commit(ctx)
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

func credentialAudit(ctx context.Context, tx pgx.Tx, tenant, gateway, actor, action string, now time.Time) error {
	id, err := newUUIDv7(now)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO connectivity.gateway_credential_audit(id,tenant_id,gateway_id,actor_id,action,occurred_at) VALUES($1,$2,$3,$4,$5,$6)`, id, tenant, gateway, actor, action, now)
	return err
}
