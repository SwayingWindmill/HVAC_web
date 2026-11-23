package connectivity

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/adapter"
)

func TestPostgresTransferDecommissionRevocationAndFactoryResetLifecycle(t *testing.T) {
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

	const (
		tenant        = "0195f300-1000-7000-8000-000000000001"
		otherTenant   = "0195f300-1000-7000-8000-000000000002"
		site          = "0195f300-2000-7000-8000-000000000001"
		gateway       = "0195f300-3000-7000-8000-000000000001"
		directDevice  = "0195f300-3000-7000-8000-000000000002"
		actorID       = "0195f300-4000-7000-8000-000000000001"
		transferID    = "0195f300-5000-7000-8000-000000000001"
		otherTransfer = "0195f300-5000-7000-8000-000000000002"
	)

	cleanup := func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = admin.Exec(cleanupCtx, "TRUNCATE TABLE connectivity.gateway_concentrator_audit, connectivity.gateway_credential_audit")
		for _, id := range []string{gateway, directDevice} {
			_, _ = admin.Exec(cleanupCtx, "DELETE FROM connectivity.gateway_factory_resets WHERE gateway_id=$1::uuid", id)
			_, _ = admin.Exec(cleanupCtx, "DELETE FROM connectivity.gateway_enrollments WHERE gateway_id=$1::uuid", id)
			_, _ = admin.Exec(cleanupCtx, "DELETE FROM connectivity.gateway_credentials WHERE gateway_id=$1::uuid", id)
			_, _ = admin.Exec(cleanupCtx, "DELETE FROM core_registry.devices WHERE id=$1::uuid", id)
		}
		_, _ = admin.Exec(cleanupCtx, "DELETE FROM core_registry.sites WHERE id=$1::uuid", site)
		_, _ = admin.Exec(cleanupCtx, "DELETE FROM iam.tenants WHERE id IN ($1::uuid, $2::uuid)", tenant, otherTenant)
	}
	cleanup()
	t.Cleanup(cleanup)

	now := time.Date(2026, 11, 23, 1, 0, 0, 0, time.UTC)
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO iam.tenants (id, code, display_name, timezone, currency, country, status, revision, created_at, updated_at)
		  VALUES ($1::uuid,'transfer-a','Transfer Tenant A','UTC','USD','US','ACTIVE',1,$3,$3),
		         ($2::uuid,'transfer-b','Transfer Tenant B','UTC','USD','US','ACTIVE',1,$3,$3)
		  ON CONFLICT (id) DO NOTHING`, []any{tenant, otherTenant, now}},
		{`INSERT INTO core_registry.sites (id, code, display_name, timezone, status, revision, created_at, updated_at, tenant_id)
		  VALUES ($1::uuid,'transfer-site','Transfer Site','UTC','ACTIVE',1,$3,$3,$2::uuid)
		  ON CONFLICT (id) DO NOTHING`, []any{site, tenant, now}},
		{`INSERT INTO core_registry.devices (id, tenant_id, site_id, code, display_name, device_type, connectivity_mode, status, revision, created_at, updated_at)
		  VALUES ($1::uuid,$2::uuid,$3::uuid,'transfer-gw-01','Transfer Gateway','GATEWAY','GATEWAY_PROXIED','ACTIVE',1,$4,$4),
		         ($5::uuid,$2::uuid,$3::uuid,'transfer-direct-01','Transfer Direct Sensor','THERMOSTAT','DIRECT','ACTIVE',1,$4,$4)
		  ON CONFLICT (id) DO UPDATE SET status='ACTIVE'`, []any{gateway, tenant, site, now, directDevice}},
	} {
		if _, err := admin.Exec(ctx, stmt.sql, stmt.args...); err != nil {
			t.Fatalf("seed transfer fixture: %v", err)
		}
	}

	store, err := Open(ctx, runtimeDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	store.now = func() time.Time { return now }

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Transfer CRL Test CA"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(365 * 24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTpl, caTpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caPriv, err := x509.MarshalPKCS8PrivateKey(caKey)
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

	var (
		mu              sync.Mutex
		order           []string
		dispatchedItems []edgecontrol.FactoryResetPayload
		revokedIDs      []string
	)
	service.SetFactoryResetDispatchHook(func(gwID string, payload edgecontrol.FactoryResetPayload) error {
		mu.Lock()
		defer mu.Unlock()
		order = append(order, "dispatch:"+gwID)
		dispatchedItems = append(dispatchedItems, payload)
		return nil
	})
	service.SetRevokeHook(func(gwID string) {
		mu.Lock()
		defer mu.Unlock()
		order = append(order, "revoke:"+gwID)
		revokedIDs = append(revokedIDs, gwID)
	})

	claims := registryauth.GrantClaims{
		TenantID:       tenant,
		PrincipalID:    actorID,
		AllowedSiteIDs: []string{site},
	}
	otherClaims := registryauth.GrantClaims{
		TenantID:       otherTenant,
		PrincipalID:    actorID,
		AllowedSiteIDs: []string{site},
	}

	// Enroll gateway so it holds an active client cert.
	code, err := service.GenerateEnrollmentCode(ctx, claims, gateway)
	if err != nil {
		t.Fatalf("GenerateEnrollmentCode: %v", err)
	}
	issued, err := service.Enroll(ctx, code.EnrollmentCode, credentialTestCSR(t, gateway))
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	block, _ := pem.Decode([]byte(issued.CertificatePEM))
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}

	// Validate invalid inputs on service & store methods.
	falseVal := false
	if _, err := service.IssueFactoryReset(ctx, claims, gateway, IssueFactoryResetInput{
		WipeCredentials:   &falseVal,
		WipeConfigShadow:  &falseVal,
		WipeOfflineBuffer: &falseVal,
		Reason:            "bad reset",
	}); !errors.Is(err, ErrEnrollmentRejected) {
		t.Fatalf("all false wipe flags got %v, want ErrEnrollmentRejected", err)
	}
	if _, err := service.DecommissionTransferredGateway(ctx, gateway, DecommissionTransferInput{
		TenantID:   otherTenant,
		TransferID: transferID,
		Reason:     "wrong tenant",
	}); !errors.Is(err, ErrCredentialForbidden) {
		t.Fatalf("wrong tenant decommission got %v, want ErrCredentialForbidden", err)
	}

	// Simulate source registry device retired just before transfer decommission.
	if _, err := admin.Exec(ctx, `UPDATE core_registry.devices SET status='RETIRED', revision=2 WHERE id=$1::uuid`, gateway); err != nil {
		t.Fatal(err)
	}

	// Execute DecommissionTransferredGateway with WipeRequired=true.
	wipeTrue := true
	res, err := service.DecommissionTransferredGateway(ctx, gateway, DecommissionTransferInput{
		TenantID:     tenant,
		TransferID:   transferID,
		WipeRequired: &wipeTrue,
		Reason:       "site transfer to site B",
		ActorID:      actorID,
	})
	if err != nil {
		t.Fatalf("DecommissionTransferredGateway: %v", err)
	}
	if !res.CredentialsRevoked || res.FactoryReset == nil || res.FactoryReset.Status != FactoryResetStatusDispatched {
		t.Fatalf("unexpected DecommissionTransferResult: %+v", res)
	}
	if res.FactoryReset.TransferID == nil || *res.FactoryReset.TransferID != transferID {
		t.Fatalf("expected transferId=%s, got %+v", transferID, res.FactoryReset.TransferID)
	}
	if !res.FactoryReset.WipeCredentials || !res.FactoryReset.WipeConfigShadow || !res.FactoryReset.WipeOfflineBuffer {
		t.Fatalf("expected all wipe flags true, got %+v", res.FactoryReset)
	}

	// Verify dispatch occurred BEFORE revoke.
	mu.Lock()
	wantOrder := []string{"dispatch:" + gateway, "revoke:" + gateway}
	if !slices.Equal(order, wantOrder) {
		mu.Unlock()
		t.Fatalf("hook order=%v, want %v", order, wantOrder)
	}
	if len(dispatchedItems) != 1 || dispatchedItems[0].ResetID != res.FactoryReset.ID || dispatchedItems[0].TransferID != transferID {
		mu.Unlock()
		t.Fatalf("dispatched payload mismatch: %+v", dispatchedItems)
	}
	mu.Unlock()

	// Retired source device is rejected by VerifyActiveCredential/ResolveGateway/Renew, credentials+enrollment are
	// immediately REVOKED in PostgreSQL, and revoked serial is present in CRL.
	if _, err := service.VerifyActiveCredential(ctx, cert); !errors.Is(err, ErrEnrollmentRejected) {
		t.Fatalf("VerifyActiveCredential after retired transfer decommission=%v, want ErrEnrollmentRejected", err)
	}
	if _, err := store.ResolveGateway(ctx, gateway); !errors.Is(err, adapter.ErrGatewayUnknown) {
		t.Fatalf("ResolveGateway after retired transfer decommission=%v, want ErrGatewayUnknown", err)
	}
	if _, err := service.Renew(ctx, cert, credentialTestCSR(t, gateway)); !errors.Is(err, ErrEnrollmentRejected) {
		t.Fatalf("Renew after retired transfer decommission=%v, want ErrEnrollmentRejected", err)
	}
	var credStatus string
	if err := admin.QueryRow(ctx, `SELECT status FROM connectivity.gateway_credentials WHERE gateway_id=$1::uuid LIMIT 1`, gateway).Scan(&credStatus); err != nil || credStatus != "REVOKED" {
		t.Fatalf("gateway_credentials.status=%q err=%v, want REVOKED", credStatus, err)
	}
	var enrollmentRevoked bool
	if err := admin.QueryRow(ctx, `SELECT revoked_at IS NOT NULL FROM connectivity.gateway_enrollments WHERE gateway_id=$1::uuid`, gateway).Scan(&enrollmentRevoked); err != nil || !enrollmentRevoked {
		t.Fatalf("gateway_enrollments.revoked_at IS NOT NULL=%v err=%v", enrollmentRevoked, err)
	}
	crlPEM, err := service.ExportCRL(ctx)
	if err != nil {
		t.Fatalf("ExportCRL: %v", err)
	}
	crlBlock, _ := pem.Decode(crlPEM)
	crl, err := x509.ParseRevocationList(crlBlock.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	foundCRL := false
	for _, entry := range crl.RevokedCertificateEntries {
		if entry.SerialNumber.Cmp(cert.SerialNumber) == 0 {
			foundCRL = true
			break
		}
	}
	if !foundCRL {
		t.Fatalf("expected revoked cert serial %s in CRL", cert.SerialNumber.Text(16))
	}

	// Exactly one TRANSFER_REVOKED audit row + reason set on credential.
	var auditCount int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM connectivity.gateway_credential_audit WHERE gateway_id=$1::uuid AND action='TRANSFER_REVOKED'`, gateway).Scan(&auditCount); err != nil || auditCount != 1 {
		t.Fatalf("TRANSFER_REVOKED audit count=%d err=%v, want 1", auditCount, err)
	}
	var credRevReason *string
	if err := admin.QueryRow(ctx, `SELECT revocation_reason FROM connectivity.gateway_credentials WHERE gateway_id=$1::uuid LIMIT 1`, gateway).Scan(&credRevReason); err != nil || credRevReason == nil || *credRevReason != "OWNERSHIP_TRANSFER:"+transferID {
		t.Fatalf("credential revocation_reason=%v err=%v", credRevReason, err)
	}

	// Idempotent repeat decommission with same transferId does not re-dispatch, duplicate rows, duplicate audit, or duplicate onRevoke.
	res2, err := service.DecommissionTransferredGateway(ctx, gateway, DecommissionTransferInput{
		TenantID:     tenant,
		TransferID:   transferID,
		WipeRequired: &wipeTrue,
		Reason:       "site transfer to site B",
		ActorID:      actorID,
	})
	if err != nil {
		t.Fatalf("idempotent DecommissionTransferredGateway: %v", err)
	}
	if res2.FactoryReset == nil || res2.FactoryReset.ID != res.FactoryReset.ID {
		t.Fatalf("idempotent decommission reset=%+v, want ID=%s", res2.FactoryReset, res.FactoryReset.ID)
	}
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM connectivity.gateway_credential_audit WHERE gateway_id=$1::uuid AND action='TRANSFER_REVOKED'`, gateway).Scan(&auditCount); err != nil || auditCount != 1 {
		t.Fatalf("idempotent TRANSFER_REVOKED audit count=%d err=%v, want 1", auditCount, err)
	}
	mu.Lock()
	if len(dispatchedItems) != 1 || len(revokedIDs) != 1 {
		mu.Unlock()
		t.Fatalf("idempotent decommission re-invoked hooks: dispatch=%d revoke=%d", len(dispatchedItems), len(revokedIDs))
	}
	mu.Unlock()

	// Wrong gateway cannot acknowledge the reset instruction.
	if _, err := store.RecordFactoryResetAck(ctx, directDevice, edgecontrol.FactoryResetAckPayload{
		ResetID:        res.FactoryReset.ID,
		Status:         edgecontrol.FactoryResetAckStatusWiped,
		WipedArtifacts: []string{"credentials"},
		OccurredAt:     now.Add(time.Second).UnixMilli(),
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong-gateway ACK got %v, want ErrNotFound", err)
	}

	// Revoked gateway CAN acknowledge FACTORY_RESET_ACK via RecordFactoryResetAck (SECURITY DEFINER resolves tenant under RLS).
	now = now.Add(5 * time.Second)
	acked, err := store.RecordFactoryResetAck(ctx, gateway, edgecontrol.FactoryResetAckPayload{
		ResetID:        res.FactoryReset.ID,
		Status:         edgecontrol.FactoryResetAckStatusWiped,
		WipedArtifacts: []string{"credentials", "config_shadow", "offline_buffer"},
		OccurredAt:     now.UnixMilli(),
	})
	if err != nil {
		t.Fatalf("RecordFactoryResetAck after revocation: %v", err)
	}
	if acked.Status != FactoryResetStatusAcknowledged || acked.AcknowledgedAt == nil || acked.AcknowledgedAt.Location() != time.UTC ||
		!slices.Equal(acked.WipedArtifacts, []string{"credentials", "config_shadow", "offline_buffer"}) {
		t.Fatalf("unexpected ACK record: %+v", acked)
	}

	// Duplicate ACK rejected once already ACKNOWLEDGED.
	if _, err := store.RecordFactoryResetAck(ctx, gateway, edgecontrol.FactoryResetAckPayload{
		ResetID:        res.FactoryReset.ID,
		Status:         edgecontrol.FactoryResetAckStatusWiped,
		WipedArtifacts: []string{"credentials"},
		OccurredAt:     now.Add(time.Second).UnixMilli(),
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("duplicate ACK got %v, want ErrNotFound", err)
	}

	// Tenant RLS & operator IssueFactoryReset / ListFactoryResets on retired gateway + FAILED ACK status.
	if _, err := service.ListFactoryResets(ctx, otherClaims, gateway); !errors.Is(err, ErrCredentialForbidden) {
		t.Fatalf("cross-tenant ListFactoryResets got %v, want ErrCredentialForbidden", err)
	}
	manualReset, err := service.IssueFactoryReset(ctx, claims, gateway, IssueFactoryResetInput{
		Reason: "post-retirement verification wipe",
	})
	if err != nil {
		t.Fatalf("operator IssueFactoryReset on retired gateway: %v", err)
	}
	failedAck, err := store.RecordFactoryResetAck(ctx, gateway, edgecontrol.FactoryResetAckPayload{
		ResetID:        manualReset.ID,
		Status:         edgecontrol.FactoryResetAckStatusFailed,
		WipedArtifacts: []string{"config_shadow"},
		ErrorMessage:   "readonly flash on cert partition",
		OccurredAt:     now.Add(2 * time.Second).UnixMilli(),
	})
	if err != nil {
		t.Fatalf("RecordFactoryResetAck FAILED: %v", err)
	}
	if failedAck.Status != FactoryResetStatusFailed || failedAck.ErrorMessage == nil || *failedAck.ErrorMessage != "readonly flash on cert partition" {
		t.Fatalf("unexpected FAILED ack record: %+v", failedAck)
	}
	listed, err := service.ListFactoryResets(ctx, claims, gateway)
	if err != nil || len(listed) != 2 || listed[0].ID != manualReset.ID || listed[1].ID != res.FactoryReset.ID {
		t.Fatalf("ListFactoryResets=%+v err=%v", listed, err)
	}

	// DIRECT mode device is eligible for transfer decommission and while still ACTIVE rejects old cert with ErrCredentialRevoked / ErrGatewayCredentialInactive.
	directCode, err := service.GenerateEnrollmentCode(ctx, claims, directDevice)
	if err != nil {
		t.Fatalf("GenerateEnrollmentCode direct: %v", err)
	}
	directIssued, err := service.Enroll(ctx, directCode.EnrollmentCode, credentialTestCSR(t, directDevice))
	if err != nil {
		t.Fatalf("Enroll direct: %v", err)
	}
	directBlock, _ := pem.Decode([]byte(directIssued.CertificatePEM))
	directCert, err := x509.ParseCertificate(directBlock.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	directRes, err := service.DecommissionTransferredGateway(ctx, directDevice, DecommissionTransferInput{
		TenantID:     tenant,
		TransferID:   otherTransfer,
		WipeRequired: &falseVal,
		Reason:       "direct device transfer without wipe",
		ActorID:      actorID,
	})
	if err != nil || !directRes.CredentialsRevoked || directRes.FactoryReset != nil {
		t.Fatalf("direct transfer decommission=%+v err=%v", directRes, err)
	}
	if _, err := service.VerifyActiveCredential(ctx, directCert); !errors.Is(err, ErrCredentialRevoked) {
		t.Fatalf("VerifyActiveCredential after active transfer decommission=%v, want ErrCredentialRevoked", err)
	}
	if _, err := store.ResolveGateway(ctx, directDevice); !errors.Is(err, adapter.ErrGatewayCredentialInactive) {
		t.Fatalf("ResolveGateway after active transfer decommission=%v, want ErrGatewayCredentialInactive", err)
	}
	if _, err := service.Renew(ctx, directCert, credentialTestCSR(t, directDevice)); !errors.Is(err, ErrCredentialRevoked) {
		t.Fatalf("Renew after active transfer decommission=%v, want ErrCredentialRevoked", err)
	}
}
