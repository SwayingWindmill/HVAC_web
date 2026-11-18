package connectivity

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/adapter"
)

func TestGatewayCredentialEnrollmentAndLifecycle(t *testing.T) {
	adminDSN, runtimeDSN := os.Getenv("CONNECTIVITY_ADMIN_DSN"), os.Getenv("CONNECTIVITY_POSTGRES_DSN")
	if adminDSN == "" || runtimeDSN == "" {
		t.Skip("Connectivity PostgreSQL DSNs are required")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	const tenant = "0193f000-0000-7000-8000-000000000001"
	const site = "0193f000-1000-7000-8000-000000000001"
	const gateway = "0193f000-2000-7000-8000-000000000001"
	cleanup := func() {
		_, _ = admin.Exec(ctx, "TRUNCATE TABLE connectivity.gateway_concentrator_audit, connectivity.gateway_credential_audit")
		for _, table := range []string{"gateway_enrollments", "gateway_credentials"} {
			_, _ = admin.Exec(ctx, "DELETE FROM connectivity."+table+" WHERE gateway_id=$1::uuid", gateway)
		}
		_, _ = admin.Exec(ctx, "DELETE FROM core_registry.devices WHERE id=$1::uuid", gateway)
		_, _ = admin.Exec(ctx, "DELETE FROM core_registry.sites WHERE id=$1::uuid", site)
		_, _ = admin.Exec(ctx, "DELETE FROM iam.tenants WHERE id=$1::uuid", tenant)
	}
	cleanup()
	t.Cleanup(cleanup)
	for _, fixture := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO iam.tenants(id,code,display_name,timezone,currency,country,status,revision,created_at,updated_at) VALUES($1,'credential-test','Credential test','UTC','USD','US','ACTIVE',1,now(),now())`, []any{tenant}},
		{`INSERT INTO core_registry.sites(id,tenant_id,code,display_name,timezone,status,revision,created_at,updated_at) VALUES($2,$1,'credential-site','Credential site','UTC','ACTIVE',1,now(),now())`, []any{tenant, site}},
		{`INSERT INTO core_registry.devices(id,tenant_id,site_id,code,display_name,device_type,status,revision,created_at,updated_at) VALUES($3,$1,$2,'credential-gateway','Credential gateway','GATEWAY','ACTIVE',1,now(),now())`, []any{tenant, site, gateway}},
	} {
		if _, err := admin.Exec(ctx, fixture.sql, fixture.args...); err != nil {
			t.Fatal(err)
		}
	}
	store, err := Open(ctx, runtimeDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC().Truncate(time.Second)
	store.now = func() time.Time { return now }
	ca, key := credentialTestCA(t, now)
	authority, err := NewCertificateAuthority(ca, key, "tls://mqtt-broker:8883")
	if err != nil {
		t.Fatal(err)
	}
	service := NewCredentialService(store, authority)
	var revokedGateways []string
	var revokeHookMu sync.Mutex
	service.SetRevokeHook(func(id string) {
		revokeHookMu.Lock()
		revokedGateways = append(revokedGateways, id)
		revokeHookMu.Unlock()
	})
	assertCodeRejectedHTTP := func(code, csr string) {
		t.Helper()
		body, err := json.Marshal(map[string]string{"enrollmentCode": code, "csrPem": csr})
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, "/gateway/v1/enroll", bytes.NewReader(body))
		identity, _ := url.Parse("spiffe://hvac.local/platform-gateway")
		request.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{URIs: []*url.URL{identity}}}}}
		response := httptest.NewRecorder()
		NewCredentialHandler(service, nil).ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("consumed/expired code boundary: status=%d cache=%s", response.Code, response.Header().Get("Cache-Control"))
		}
	}
	claims := registryauth.GrantClaims{TenantID: tenant, PrincipalID: gateway, AllowedSiteIDs: []string{site}}
	denied := claims
	denied.AllowedSiteIDs = nil
	if _, err := service.GenerateEnrollmentCode(ctx, denied, gateway); !errors.Is(err, ErrCredentialForbidden) {
		t.Fatalf("out-of-scope generation: %v", err)
	}
	code, err := service.GenerateEnrollmentCode(ctx, claims, gateway)
	if err != nil {
		t.Fatal(err)
	}
	if !code.ExpiresAt.Equal(now.Add(24 * time.Hour)) {
		t.Fatal("wrong enrollment lifetime")
	}
	csr := credentialTestCSR(t, gateway)
	var issued IssuedCredential
	var attempts sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		attempts.Go(func() {
			value, err := service.Enroll(ctx, code.EnrollmentCode, csr)
			if err == nil {
				issued = value
			}
			results <- err
		})
	}
	attempts.Wait()
	close(results)
	success, rejected := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrEnrollmentRejected) {
			rejected++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || rejected != 1 {
		t.Fatalf("concurrent code consumption: success=%d rejected=%d", success, rejected)
	}
	block, _ := pem.Decode([]byte(issued.CertificatePEM))
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if cert.Subject.CommonName != gateway || !cert.NotAfter.Equal(now.Add(90*24*time.Hour)) || issued.BrokerURL != "tls://mqtt-broker:8883" {
		t.Fatal("certificate identity/lifetime/broker mismatch")
	}
	if _, err := service.Enroll(ctx, code.EnrollmentCode, csr); !errors.Is(err, ErrEnrollmentRejected) {
		t.Fatalf("code reused: %v", err)
	}
	assertCodeRejectedHTTP(code.EnrollmentCode, csr)
	stale, err := service.GenerateEnrollmentCode(ctx, claims, gateway)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(24 * time.Hour)
	if _, err := service.Enroll(ctx, stale.EnrollmentCode, csr); !errors.Is(err, ErrEnrollmentRejected) {
		t.Fatalf("expired code accepted: %v", err)
	}
	assertCodeRejectedHTTP(stale.EnrollmentCode, csr)
	renewed, err := service.Renew(ctx, cert, credentialTestCSR(t, gateway))
	if err != nil {
		t.Fatal(err)
	}
	if renewed.CertificatePEM == issued.CertificatePEM {
		t.Fatal("renewal returned old certificate")
	}
	if activeGateway, err := service.VerifyActiveCredential(ctx, cert); err != nil || activeGateway != gateway {
		t.Fatalf("expected active credential verification to succeed, got gw=%q err=%v", activeGateway, err)
	}
	if _, err := service.Renew(ctx, cert, csr); err != nil {
		t.Fatalf("old certificate no longer works after renewal: %v", err)
	}
	// Renewal and terminal revocation share the enrollment-row lock. Either
	// ordering must leave every certificate revoked once both requests finish.
	start := make(chan struct{})
	racingRenewal := make(chan error, 1)
	racingRevocation := make(chan error, 1)
	go func() { <-start; _, err := service.Renew(ctx, cert, csr); racingRenewal <- err }()
	go func() { <-start; racingRevocation <- service.Revoke(ctx, claims, gateway) }()
	close(start)
	if err := <-racingRenewal; err != nil && !errors.Is(err, ErrCredentialRevoked) {
		t.Fatal(err)
	}
	if err := <-racingRevocation; err != nil {
		t.Fatal(err)
	}
	var active int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM connectivity.gateway_credentials WHERE gateway_id=$1 AND status='ACTIVE'`, gateway).Scan(&active); err != nil || active != 0 {
		t.Fatalf("revocation race left active certificates=%d err=%v", active, err)
	}
	if _, err := store.ResolveGateway(ctx, gateway); !errors.Is(err, adapter.ErrGatewayCredentialInactive) {
		t.Fatalf("revoked gateway active: %v", err)
	}
	if _, err := service.Renew(ctx, cert, csr); !errors.Is(err, ErrCredentialRevoked) {
		t.Fatalf("revoked renewal: %v", err)
	}
	if _, err := service.VerifyActiveCredential(ctx, cert); !errors.Is(err, ErrCredentialRevoked) {
		t.Fatalf("revoked VerifyActiveCredential accepted: %v", err)
	}
	revokeHookMu.Lock()
	hookCount := len(revokedGateways)
	hookID := ""
	if hookCount > 0 {
		hookID = revokedGateways[0]
	}
	revokeHookMu.Unlock()
	if hookCount != 1 || hookID != gateway {
		t.Fatalf("expected revoke hook called once for %s, got count=%d id=%q", gateway, hookCount, hookID)
	}
	if _, err := service.GenerateEnrollmentCode(ctx, claims, gateway); !errors.Is(err, ErrCredentialRevoked) {
		t.Fatalf("revoked identity re-enabled: %v", err)
	}
	status, err := service.Status(ctx, claims, gateway)
	if err != nil || status.Status != "REVOKED" {
		t.Fatalf("status=%#v err=%v", status, err)
	}
	var count int
	if err := admin.QueryRow(ctx, `SELECT count(DISTINCT action) FROM connectivity.gateway_credential_audit WHERE gateway_id=$1 AND action IN ('ISSUED','RENEWED','REVOKED')`, gateway).Scan(&count); err != nil || count != 3 {
		t.Fatalf("durable audit actions=%d err=%v", count, err)
	}
	for _, stmt := range []string{
		`UPDATE connectivity.gateway_credential_audit SET action = 'REVOKED' WHERE gateway_id = $1::uuid`,
		`DELETE FROM connectivity.gateway_credential_audit WHERE gateway_id = $1::uuid`,
	} {
		_, mutErr := admin.Exec(ctx, stmt, gateway)
		var pgErr *pgconn.PgError
		if !errors.As(mutErr, &pgErr) || pgErr.Code != "23000" {
			t.Fatalf("expected integrity_constraint_violation (23000) for %s, got %v", stmt, mutErr)
		}
	}
}

func TestPostgresRevokeRetiredGatewayRevokesWithoutActiveRegistryRow(t *testing.T) {
	adminDSN, runtimeDSN := os.Getenv("CONNECTIVITY_ADMIN_DSN"), os.Getenv("CONNECTIVITY_POSTGRES_DSN")
	if adminDSN == "" {
		adminDSN = os.Getenv("S1_ADMIN_DATABASE_URL")
	}
	if runtimeDSN == "" {
		runtimeDSN = os.Getenv("CONNECTIVITY_DATABASE_URL")
	}
	if adminDSN == "" || runtimeDSN == "" {
		t.Skip("CONNECTIVITY_ADMIN_DSN and CONNECTIVITY_POSTGRES_DSN are required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	admin, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()

	tenant := "0192f300-1000-7000-8000-00000000000a"
	otherTenant := "0192f300-1000-7000-8000-00000000000b"
	site := "0192f300-2000-7000-8000-00000000000a"
	gateway := "0192f300-3000-7000-8000-00000000000a"
	credID := "0192f300-4000-7000-8000-00000000000a"

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = admin.Exec(cleanupCtx, "TRUNCATE TABLE connectivity.gateway_concentrator_audit, connectivity.gateway_credential_audit")
		for _, table := range []string{"gateway_enrollments", "gateway_credentials"} {
			_, _ = admin.Exec(cleanupCtx, "DELETE FROM connectivity."+table+" WHERE gateway_id=$1::uuid", gateway)
		}
		_, _ = admin.Exec(cleanupCtx, `DELETE FROM core_registry.devices WHERE id=$1::uuid`, gateway)
		_, _ = admin.Exec(cleanupCtx, `DELETE FROM core_registry.sites WHERE id=$1::uuid`, site)
		_, _ = admin.Exec(cleanupCtx, `DELETE FROM iam.tenants WHERE id IN ($1::uuid, $2::uuid)`, tenant, otherTenant)
	})

	now := time.Now().UTC().Truncate(time.Millisecond)
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO iam.tenants (id, code, display_name, timezone, currency, country, status, revision, created_at, updated_at)
		  VALUES ($1::uuid,'retire-gw-a','Retire GW A','UTC','USD','US','ACTIVE',1,$3,$3),
		         ($2::uuid,'retire-gw-b','Retire GW B','UTC','USD','US','ACTIVE',1,$3,$3)
		  ON CONFLICT (id) DO NOTHING`, []any{tenant, otherTenant, now}},
		{`INSERT INTO core_registry.sites (id, code, display_name, timezone, status, revision, created_at, updated_at, tenant_id)
		  VALUES ($1::uuid,'retire-gw-site','Retire GW Site','UTC','ACTIVE',1,$3,$3,$2::uuid)
		  ON CONFLICT (id) DO NOTHING`, []any{site, tenant, now}},
		{`INSERT INTO core_registry.devices (id, tenant_id, site_id, code, display_name, device_type, status, revision, created_at, updated_at)
		  VALUES ($1::uuid,$2::uuid,$3::uuid,'retire-gw-01','Retired GW','GATEWAY','RETIRED',2,$4,$4)
		  ON CONFLICT (id) DO UPDATE SET status='RETIRED'`, []any{gateway, tenant, site, now}},
		{`INSERT INTO connectivity.gateway_credentials (id, tenant_id, gateway_id, certificate_fingerprint_sha256, status, valid_from, valid_until, revoked_at, created_at, updated_at)
		  VALUES ($1::uuid,$2::uuid,$3::uuid,repeat('c',64),'ACTIVE',$4,$5,NULL,$4,$4)
		  ON CONFLICT (id) DO UPDATE SET status='ACTIVE', revoked_at=NULL`, []any{credID, tenant, gateway, now.Add(-time.Minute), now.Add(time.Hour)}},
	} {
		if _, err := admin.Exec(ctx, stmt.sql, stmt.args...); err != nil {
			t.Fatalf("seed retired gateway fixture: %v", err)
		}
	}

	store, err := Open(ctx, runtimeDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	caCertPEM, caKeyPEM := credentialTestCA(t, now)
	authority, err := NewCertificateAuthority(caCertPEM, caKeyPEM, "tls://mqtt:8883")
	if err != nil {
		t.Fatal(err)
	}
	service := NewCredentialService(store, authority)
	var hooked string
	service.SetRevokeHook(func(id string) { hooked = id })

	if err := service.RevokeRetiredGateway(ctx, otherTenant, gateway); !errors.Is(err, ErrCredentialForbidden) {
		t.Fatalf("expected ErrCredentialForbidden on tenant mismatch, got %v", err)
	}
	if err := service.RevokeRetiredGateway(ctx, tenant, gateway); err != nil {
		t.Fatalf("RevokeRetiredGateway failed: %v", err)
	}
	if hooked != gateway {
		t.Fatalf("expected revoke hook %s, got %q", gateway, hooked)
	}

	var credStatus string
	if err := admin.QueryRow(ctx, `SELECT status FROM connectivity.gateway_credentials WHERE id=$1::uuid`, credID).Scan(&credStatus); err != nil {
		t.Fatal(err)
	}
	if credStatus != "REVOKED" {
		t.Fatalf("expected credential status REVOKED, got %q", credStatus)
	}
}

func credentialTestCA(t *testing.T, now time.Time) ([]byte, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Gateway test CA"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(365 * 24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private})
}

func credentialTestCSR(t *testing.T, gateway string) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: gateway}}, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}))
}

func TestSingleCertificateRevocationLosslessRotationAndCRL(t *testing.T) {
	adminDSN, runtimeDSN := os.Getenv("CONNECTIVITY_ADMIN_DSN"), os.Getenv("CONNECTIVITY_POSTGRES_DSN")
	if adminDSN == "" {
		adminDSN = os.Getenv("S1_ADMIN_DATABASE_URL")
	}
	if runtimeDSN == "" {
		runtimeDSN = os.Getenv("CONNECTIVITY_DATABASE_URL")
	}
	if adminDSN == "" || runtimeDSN == "" {
		t.Skip("CONNECTIVITY_ADMIN_DSN and CONNECTIVITY_POSTGRES_DSN are required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()

	const tenant = "0193f100-0000-7000-8000-000000000001"
	const site = "0193f100-1000-7000-8000-000000000001"
	const gateway = "0193f100-2000-7000-8000-000000000001"
	cleanup := func() {
		_, _ = admin.Exec(ctx, "TRUNCATE TABLE connectivity.gateway_concentrator_audit, connectivity.gateway_credential_audit")
		for _, table := range []string{"gateway_enrollments", "gateway_credentials"} {
			_, _ = admin.Exec(ctx, "DELETE FROM connectivity."+table+" WHERE gateway_id=$1::uuid", gateway)
		}
		_, _ = admin.Exec(ctx, "DELETE FROM core_registry.devices WHERE id=$1::uuid", gateway)
		_, _ = admin.Exec(ctx, "DELETE FROM core_registry.sites WHERE id=$1::uuid", site)
		_, _ = admin.Exec(ctx, "DELETE FROM iam.tenants WHERE id=$1::uuid", tenant)
	}
	cleanup()
	t.Cleanup(cleanup)

	for _, fixture := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO iam.tenants(id,code,display_name,timezone,currency,country,status,revision,created_at,updated_at) VALUES($1,'rot-test','Rotation test','UTC','USD','US','ACTIVE',1,now(),now())`, []any{tenant}},
		{`INSERT INTO core_registry.sites(id,tenant_id,code,display_name,timezone,status,revision,created_at,updated_at) VALUES($2,$1,'rot-site','Rotation site','UTC','ACTIVE',1,now(),now())`, []any{tenant, site}},
		{`INSERT INTO core_registry.devices(id,tenant_id,site_id,code,display_name,device_type,status,revision,created_at,updated_at) VALUES($3,$1,$2,'rot-gw','Rotation GW','GATEWAY','ACTIVE',1,now(),now())`, []any{tenant, site, gateway}},
	} {
		if _, err := admin.Exec(ctx, fixture.sql, fixture.args...); err != nil {
			t.Fatal(err)
		}
	}

	store, err := Open(ctx, runtimeDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now().UTC().Truncate(time.Second)
	store.now = func() time.Time { return now }

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Gateway CRL CA"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(365 * 24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTpl, caTpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	caPriv, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := NewCertificateAuthority(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: caPriv}),
		"tls://mqtt-broker:8883",
	)
	if err != nil {
		t.Fatal(err)
	}

	service := NewCredentialService(store, authority)
	type certRevokeEvent struct {
		gw, fp string
		remain bool
	}
	var certEvents []certRevokeEvent
	var eventsMu sync.Mutex
	service.SetCertificateRevokeHook(func(gw, fp string, remain bool) {
		eventsMu.Lock()
		certEvents = append(certEvents, certRevokeEvent{gw, fp, remain})
		eventsMu.Unlock()
	})

	claims := registryauth.GrantClaims{TenantID: tenant, PrincipalID: gateway, AllowedSiteIDs: []string{site}}
	codeA, err := service.GenerateEnrollmentCode(ctx, claims, gateway)
	if err != nil {
		t.Fatal(err)
	}
	issuedA, err := service.Enroll(ctx, codeA.EnrollmentCode, credentialTestCSR(t, gateway))
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)

	// RotateEnrollment keeps Cert A active while issuing a new code for Cert B.
	codeB, err := service.RotateEnrollment(ctx, claims, gateway)
	if err != nil {
		t.Fatal(err)
	}
	issuedB, err := service.Enroll(ctx, codeB.EnrollmentCode, credentialTestCSR(t, gateway))
	if err != nil {
		t.Fatal(err)
	}

	parseCert := func(pemStr string) *x509.Certificate {
		blk, _ := pem.Decode([]byte(pemStr))
		cert, err := x509.ParseCertificate(blk.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		return cert
	}
	certA := parseCert(issuedA.CertificatePEM)
	certB := parseCert(issuedB.CertificatePEM)

	// Revoke ONLY Cert A; Cert B and gateway identity stay active.
	revokedA, err := service.RevokeCertificate(ctx, claims, gateway, issuedA.CredentialID, "compromised key A")
	if err != nil || revokedA.Status != "REVOKED" || revokedA.RevocationReason == nil || *revokedA.RevocationReason != "compromised key A" {
		t.Fatalf("RevokeCertificate=%+v err=%v", revokedA, err)
	}
	if _, err := service.VerifyActiveCredential(ctx, certA); !errors.Is(err, ErrEnrollmentRejected) {
		t.Fatalf("expected revoked Cert A rejected, got %v", err)
	}
	if _, err := service.VerifyActiveCredential(ctx, certB); err != nil {
		t.Fatalf("expected sibling Cert B to stay active, got %v", err)
	}
	if _, err := store.ResolveGateway(ctx, gateway); err != nil {
		t.Fatalf("expected gateway active with Cert B remaining, got %v", err)
	}

	// Atomic lossless renewal revoking Cert B while issuing Cert C.
	now = now.Add(time.Second)
	issuedC, err := service.RenewWithOptions(ctx, certB, credentialTestCSR(t, gateway), true)
	if err != nil {
		t.Fatalf("RenewWithOptions revokePeer=true failed: %v", err)
	}
	certC := parseCert(issuedC.CertificatePEM)
	if _, err := service.VerifyActiveCredential(ctx, certB); !errors.Is(err, ErrEnrollmentRejected) {
		t.Fatalf("expected old Cert B revoked after RenewWithOptions, got %v", err)
	}
	if _, err := service.VerifyActiveCredential(ctx, certC); err != nil {
		t.Fatalf("expected new Cert C active, got %v", err)
	}

	// Export CRL contains revoked Cert A and Cert B serials without active Cert C.
	crlPEM, err := service.ExportCRL(ctx)
	if err != nil {
		t.Fatalf("ExportCRL: %v", err)
	}
	blk, _ := pem.Decode(crlPEM)
	crl, err := x509.ParseRevocationList(blk.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	revokedSerials := make(map[string]bool, len(crl.RevokedCertificateEntries))
	for _, entry := range crl.RevokedCertificateEntries {
		revokedSerials[entry.SerialNumber.Text(16)] = true
	}
	if !revokedSerials[certA.SerialNumber.Text(16)] || !revokedSerials[certB.SerialNumber.Text(16)] || revokedSerials[certC.SerialNumber.Text(16)] {
		t.Fatalf("CRL serials=%v missing revoked A/B or unexpectedly containing C (A=%s B=%s C=%s)",
			revokedSerials, certA.SerialNumber.Text(16), certB.SerialNumber.Text(16), certC.SerialNumber.Text(16))
	}

	// ListCredentials returns 3 records newest first with UTC timestamps for HTTP contract compliance.
	list, err := service.ListCredentials(ctx, claims, gateway)
	if err != nil || len(list) != 3 || list[0].ID != issuedC.CredentialID {
		t.Fatalf("ListCredentials=%+v err=%v", list, err)
	}
	for idx, rec := range list {
		if rec.ValidFrom.Location() != time.UTC || rec.ValidUntil.Location() != time.UTC || rec.CreatedAt.Location() != time.UTC {
			t.Fatalf("credential[%d] timestamps must be UTC, got %+v", idx, rec)
		}
	}

	// Terminal Revoke followed by RotateEnrollment re-enables identity while keeping old revoked certs rejected.
	if err := service.Revoke(ctx, claims, gateway); err != nil {
		t.Fatal(err)
	}
	codeReenable, err := service.RotateEnrollment(ctx, claims, gateway)
	if err != nil {
		t.Fatalf("RotateEnrollment after Revoke failed: %v", err)
	}
	if _, err := service.VerifyActiveCredential(ctx, certC); !errors.Is(err, ErrEnrollmentRejected) {
		t.Fatalf("old revoked Cert C must stay rejected after re-enable, got %v", err)
	}
	if _, err := service.Enroll(ctx, codeReenable.EnrollmentCode, credentialTestCSR(t, gateway)); err != nil {
		t.Fatalf("Enroll after RotateEnrollment failed: %v", err)
	}

	var reenabledCount int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM connectivity.gateway_credential_audit WHERE gateway_id=$1::uuid AND action='IDENTITY_REENABLED'`, gateway).Scan(&reenabledCount); err != nil || reenabledCount != 1 {
		t.Fatalf("expected IDENTITY_REENABLED audit count=1, got %d err=%v", reenabledCount, err)
	}
	eventsMu.Lock()
	eventCount := len(certEvents)
	eventsMu.Unlock()
	if eventCount != 2 || !certEvents[0].remain || !certEvents[1].remain {
		t.Fatalf("expected 2 certificate revoke hook calls with remain=true, got %+v", certEvents)
	}
}
