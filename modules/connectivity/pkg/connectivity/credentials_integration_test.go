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
		for _, table := range []string{"gateway_credential_audit", "gateway_enrollments", "gateway_credentials"} {
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
