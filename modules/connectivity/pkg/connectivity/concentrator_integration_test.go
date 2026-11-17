package connectivity_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/adapter"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/connectivity"
)

func TestConcentratorAuditPostgresRLSAndConstraints(t *testing.T) {
	adminDSN, runtimeDSN := os.Getenv("CONNECTIVITY_ADMIN_DSN"), os.Getenv("CONNECTIVITY_POSTGRES_DSN")
	if adminDSN == "" || runtimeDSN == "" {
		t.Skip("CONNECTIVITY_ADMIN_DSN and CONNECTIVITY_POSTGRES_DSN required")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatalf("open admin pool: %v", err)
	}
	defer admin.Close()

	const tenantA = "0194f000-1000-7000-8000-000000000001"
	const tenantB = "0194f000-1000-7000-8000-000000000002"
	const siteA = "0194f000-2000-7000-8000-000000000001"
	const siteOther = "0194f000-2000-7000-8000-000000000002"
	const gatewayA = "0194f000-3000-7000-8000-000000000001"
	const actorA = "0194f000-4000-7000-8000-000000000001"
	const reqID1 = "0194f000-5000-7000-8000-000000000001"
	const reqID2 = "0194f000-5000-7000-8000-000000000002"

	cleanup := func() {
		for _, table := range []string{"gateway_concentrator_audit", "gateway_credential_audit", "gateway_enrollments", "gateway_credentials"} {
			_, _ = admin.Exec(ctx, "DELETE FROM connectivity."+table+" WHERE gateway_id = $1::uuid", gatewayA)
		}
		_, _ = admin.Exec(ctx, "DELETE FROM core_registry.devices WHERE id = $1::uuid", gatewayA)
		_, _ = admin.Exec(ctx, "DELETE FROM core_registry.sites WHERE id IN ($1::uuid, $2::uuid)", siteA, siteOther)
		_, _ = admin.Exec(ctx, "DELETE FROM iam.tenants WHERE id IN ($1::uuid, $2::uuid)", tenantA, tenantB)
	}
	cleanup()
	t.Cleanup(cleanup)

	for _, step := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO iam.tenants(id,code,display_name,timezone,currency,country,status,revision,created_at,updated_at) VALUES($1,'conc-test-a','Conc Test A','UTC','USD','US','ACTIVE',1,now(),now())`, []any{tenantA}},
		{`INSERT INTO iam.tenants(id,code,display_name,timezone,currency,country,status,revision,created_at,updated_at) VALUES($1,'conc-test-b','Conc Test B','UTC','USD','US','ACTIVE',1,now(),now())`, []any{tenantB}},
		{`INSERT INTO core_registry.sites(id,tenant_id,code,display_name,timezone,status,revision,created_at,updated_at) VALUES($2,$1,'conc-site-a','Conc Site A','UTC','ACTIVE',1,now(),now())`, []any{tenantA, siteA}},
		{`INSERT INTO core_registry.sites(id,tenant_id,code,display_name,timezone,status,revision,created_at,updated_at) VALUES($2,$1,'conc-site-other','Conc Site Other','UTC','ACTIVE',1,now(),now())`, []any{tenantA, siteOther}},
		{`INSERT INTO core_registry.devices(id,tenant_id,site_id,code,display_name,device_type,status,revision,created_at,updated_at) VALUES($3,$1,$2,'conc-gw-a','Conc GW A','GATEWAY','ACTIVE',1,now(),now())`, []any{tenantA, siteA, gatewayA}},
	} {
		if _, err := admin.Exec(ctx, step.sql, step.args...); err != nil {
			t.Fatalf("seed %s: %v", step.sql, err)
		}
	}

	now := time.Now().UTC().Truncate(time.Second)
	store, err := connectivity.Open(ctx, runtimeDSN)
	if err != nil {
		t.Fatalf("open runtime store: %v", err)
	}
	defer store.Close()

	allowedClaims := registryauth.GrantClaims{TenantID: tenantA, AllowedSiteIDs: []string{siteA}}
	if _, err := store.ResolveGatewayForOperator(ctx, allowedClaims, gatewayA); !errors.Is(err, adapter.ErrGatewayCredentialInactive) {
		t.Fatalf("missing credential got %v, want ErrGatewayCredentialInactive", err)
	}

	const expiredCredID = "0194f000-6000-7000-8000-000000000001"
	const activeCredID = "0194f000-6000-7000-8000-000000000002"
	const revokedCredID = "0194f000-6000-7000-8000-000000000003"
	if _, err := admin.Exec(ctx, `
INSERT INTO connectivity.gateway_credentials(id,tenant_id,gateway_id,certificate_fingerprint_sha256,status,valid_from,valid_until,revoked_at,created_at,updated_at)
VALUES($1,$2,$3,repeat('1',64),'ACTIVE',$4,$5,NULL,$4,$4)`,
		expiredCredID, tenantA, gatewayA, now.Add(-2*time.Hour), now.Add(-time.Hour)); err != nil {
		t.Fatalf("seed expired credential: %v", err)
	}
	if _, err := store.ResolveGatewayForOperator(ctx, allowedClaims, gatewayA); !errors.Is(err, adapter.ErrGatewayCredentialInactive) {
		t.Fatalf("expired credential got %v, want ErrGatewayCredentialInactive", err)
	}

	if _, err := admin.Exec(ctx, `
INSERT INTO connectivity.gateway_credentials(id,tenant_id,gateway_id,certificate_fingerprint_sha256,status,valid_from,valid_until,revoked_at,created_at,updated_at)
VALUES($1,$2,$3,repeat('2',64),'ACTIVE',$4,$5,NULL,$4,$4)`,
		activeCredID, tenantA, gatewayA, now.Add(-time.Hour), now.Add(24*time.Hour)); err != nil {
		t.Fatalf("seed active credential: %v", err)
	}

	gotTenant, err := store.ResolveGatewayForOperator(ctx, allowedClaims, gatewayA)
	if err != nil || gotTenant != tenantA {
		t.Fatalf("ResolveGatewayForOperator got (%q, %v), want (%q, nil)", gotTenant, err, tenantA)
	}

	if _, err := admin.Exec(ctx, `
INSERT INTO connectivity.gateway_credentials(id,tenant_id,gateway_id,certificate_fingerprint_sha256,status,valid_from,valid_until,revoked_at,created_at,updated_at)
VALUES($1,$2,$3,repeat('3',64),'REVOKED',$4,$5,$6,$4,$6)`,
		revokedCredID, tenantA, gatewayA, now.Add(-2*time.Hour), now.Add(24*time.Hour), now.Add(-time.Minute)); err != nil {
		t.Fatalf("seed rotated revoked credential: %v", err)
	}
	if gotTenant, err := store.ResolveGatewayForOperator(ctx, allowedClaims, gatewayA); err != nil || gotTenant != tenantA {
		t.Fatalf("rotated credential ResolveGatewayForOperator got (%q, %v), want (%q, nil)", gotTenant, err, tenantA)
	}

	if _, err := store.ResolveGatewayForOperator(ctx, registryauth.GrantClaims{TenantID: tenantA, AllowedSiteIDs: []string{siteOther}}, gatewayA); !errors.Is(err, connectivity.ErrCredentialForbidden) {
		t.Fatalf("wrong site scope got %v, want ErrCredentialForbidden", err)
	}
	if _, err := store.ResolveGatewayForOperator(ctx, registryauth.GrantClaims{TenantID: tenantB, AllowedSiteIDs: []string{siteA}}, gatewayA); !errors.Is(err, connectivity.ErrCredentialForbidden) {
		t.Fatalf("wrong tenant got %v, want ErrCredentialForbidden", err)
	}

	inserted, err := store.RecordConcentratorAudit(ctx, tenantA, gatewayA, connectivity.ConcentratorAuditEntry{
		UnitID:     1,
		ActorID:    actorA,
		Operation:  "WRITE_PARAMS",
		RequestID:  reqID1,
		Outcome:    "APPLIED",
		Reason:     "enable ambient probe RF settings",
		Diff:       map[string]any{"rfTxPower": 20},
		OccurredAt: now,
	})
	if err != nil || inserted.ID == "" {
		t.Fatalf("RecordConcentratorAudit: %+v, err=%v", inserted, err)
	}

	listedA, err := store.ListConcentratorAudits(ctx, tenantA, gatewayA, 1, 20)
	if err != nil || len(listedA) != 1 || listedA[0].RequestID != reqID1 || listedA[0].Reason != "enable ambient probe RF settings" {
		t.Fatalf("ListConcentratorAudits(tenantA): len=%d items=%+v err=%v", len(listedA), listedA, err)
	}

	listedB, err := store.ListConcentratorAudits(ctx, tenantB, gatewayA, 1, 20)
	if err != nil || len(listedB) != 0 {
		t.Fatalf("RLS leak! ListConcentratorAudits(tenantB) returned %d items: %+v (err=%v)", len(listedB), listedB, err)
	}

	for _, protectedUID := range []int{2, 3, 4, 5, 10, 31} {
		_, err := store.RecordConcentratorAudit(ctx, tenantA, gatewayA, connectivity.ConcentratorAuditEntry{
			UnitID:    protectedUID,
			ActorID:   actorA,
			Operation: "WRITE_EIDS",
			RequestID: reqID2,
			Outcome:   "REJECTED",
			Reason:    "db check guard",
		})
		if err == nil || !strings.Contains(err.Error(), "gateway_concentrator_audit_unit_id_check") {
			t.Fatalf("expected DB unit_id check violation for uid=%d, got %v", protectedUID, err)
		}
	}

	runtimePool, err := pgxpool.New(ctx, runtimeDSN)
	if err != nil {
		t.Fatalf("open runtime pool: %v", err)
	}
	defer runtimePool.Close()
	var count int
	if err := runtimePool.QueryRow(ctx, `SELECT count(*) FROM connectivity.gateway_concentrator_audit`).Scan(&count); err != nil {
		t.Fatalf("query without app.tenant_id: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 visible rows without app.tenant_id RLS session setting, got %d", count)
	}

	if _, err := admin.Exec(ctx, `UPDATE connectivity.gateway_credentials SET status='REVOKED', revoked_at=$2, updated_at=$2 WHERE id=$1::uuid`, activeCredID, now); err != nil {
		t.Fatalf("revoke active credential: %v", err)
	}
	if _, err := store.ResolveGatewayForOperator(ctx, allowedClaims, gatewayA); !errors.Is(err, connectivity.ErrCredentialRevoked) {
		t.Fatalf("revoked credential only got %v, want ErrCredentialRevoked", err)
	}

	if _, err := admin.Exec(ctx, `UPDATE connectivity.gateway_credentials SET status='ACTIVE', revoked_at=NULL, updated_at=$2 WHERE id=$1::uuid`, activeCredID, now); err != nil {
		t.Fatalf("reactivate credential for enrollment test: %v", err)
	}
	if _, err := admin.Exec(ctx, `
INSERT INTO connectivity.gateway_enrollments(tenant_id,gateway_id,revoked_at,created_at,updated_at)
VALUES($1,$2,$3,$3,$3)`, tenantA, gatewayA, now); err != nil {
		t.Fatalf("seed revoked enrollment: %v", err)
	}
	if _, err := store.ResolveGatewayForOperator(ctx, allowedClaims, gatewayA); !errors.Is(err, connectivity.ErrCredentialRevoked) {
		t.Fatalf("revoked enrollment got %v, want ErrCredentialRevoked", err)
	}
}
