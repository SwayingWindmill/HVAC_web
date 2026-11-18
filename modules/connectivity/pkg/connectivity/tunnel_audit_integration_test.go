package connectivity_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/connectivity"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/tunnel"
)

func TestPostgresGatewayTunnelAuditAndRLSIsolation(t *testing.T) {
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
		t.Fatalf("open admin pool: %v", err)
	}
	defer admin.Close()

	tenantA := "0192f200-1000-7000-8000-00000000000a"
	tenantB := "0192f200-1000-7000-8000-00000000000b"
	siteA := "0192f200-2000-7000-8000-00000000000a"
	gatewayA := "0192f200-3000-7000-8000-00000000000a"
	sessionA := "0192f200-4000-7000-8000-00000000000a"
	sessionUnknown := "0192f200-4000-7000-8000-00000000000b"
	unknownGateway := "0192f200-3000-7000-8000-0000000000ff"

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = admin.Exec(cleanupCtx, `TRUNCATE TABLE connectivity.gateway_tunnel_audit`)
		_, _ = admin.Exec(cleanupCtx, `DELETE FROM core_registry.devices WHERE id = $1::uuid`, gatewayA)
		_, _ = admin.Exec(cleanupCtx, `DELETE FROM core_registry.sites WHERE id = $1::uuid`, siteA)
		_, _ = admin.Exec(cleanupCtx, `DELETE FROM iam.tenants WHERE id IN ($1::uuid, $2::uuid)`, tenantA, tenantB)
	})

	now := time.Now().UTC().Truncate(time.Millisecond)
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO iam.tenants (id, code, display_name, timezone, currency, country, status, revision, created_at, updated_at)
		  VALUES ($1::uuid,'tunnel-audit-a','Tunnel A','UTC','USD','US','ACTIVE',1,$3,$3),
		         ($2::uuid,'tunnel-audit-b','Tunnel B','UTC','USD','US','ACTIVE',1,$3,$3)
		  ON CONFLICT (id) DO NOTHING`, []any{tenantA, tenantB, now}},
		{`INSERT INTO core_registry.sites (id, code, display_name, timezone, status, revision, created_at, updated_at, tenant_id)
		  VALUES ($1::uuid,'tunnel-audit-site','Tunnel Site','UTC','ACTIVE',1,$3,$3,$2::uuid)
		  ON CONFLICT (id) DO NOTHING`, []any{siteA, tenantA, now}},
		// Intentionally RETIRED to verify tunnel audit still resolves tenant_id for retired gateways closing tunnels.
		{`INSERT INTO core_registry.devices (id, tenant_id, site_id, code, display_name, device_type, status, revision, created_at, updated_at)
		  VALUES ($1::uuid,$2::uuid,$3::uuid,'tunnel-audit-gw','Retired Tunnel GW','GATEWAY','RETIRED',2,$4,$4)
		  ON CONFLICT (id) DO UPDATE SET status = 'RETIRED'`, []any{gatewayA, tenantA, siteA, now}},
	} {
		if _, err := admin.Exec(ctx, stmt.sql, stmt.args...); err != nil {
			t.Fatalf("seed tunnel audit fixture: %v", err)
		}
	}

	store, err := connectivity.Open(ctx, runtimeDSN)
	if err != nil {
		t.Fatalf("open connectivity store: %v", err)
	}
	defer store.Close()

	streamID := int64(1)
	port := uint16(1880)
	if err := store.RecordTunnelAudit(ctx, tunnel.AuditEvent{
		GatewayID:  gatewayA,
		SessionID:  sessionA,
		EventType:  tunnel.AuditSessionOpened,
		RemoteAddr: "127.0.0.1:41001",
		OccurredAt: now,
	}); err != nil {
		t.Fatalf("record session opened for retired gateway: %v", err)
	}
	if err := store.RecordTunnelAudit(ctx, tunnel.AuditEvent{
		GatewayID:  gatewayA,
		SessionID:  sessionA,
		EventType:  tunnel.AuditStreamOpened,
		StreamID:   &streamID,
		TargetPort: &port,
		RemoteAddr: "127.0.0.1:41001",
		OccurredAt: now.Add(time.Second),
	}); err != nil {
		t.Fatalf("record stream opened: %v", err)
	}
	if err := store.RecordTunnelAudit(ctx, tunnel.AuditEvent{
		GatewayID:  gatewayA,
		SessionID:  sessionA,
		EventType:  tunnel.AuditSessionClosed,
		RemoteAddr: "127.0.0.1:41001",
		Reason:     "gateway_closed",
		BytesTX:    128,
		BytesRX:    256,
		OccurredAt: now.Add(2 * time.Second),
	}); err != nil {
		t.Fatalf("record session closed: %v", err)
	}
	if err := store.RecordTunnelAudit(ctx, tunnel.AuditEvent{
		GatewayID:  unknownGateway,
		SessionID:  sessionUnknown,
		EventType:  tunnel.AuditStreamRejected,
		TargetPort: &port,
		Reason:     "unknown_gateway",
		OccurredAt: now.Add(3 * time.Second),
	}); err != nil {
		t.Fatalf("record pre-auth unknown gateway audit: %v", err)
	}

	runtimePool, err := pgxpool.New(ctx, runtimeDSN)
	if err != nil {
		t.Fatalf("open runtime pool: %v", err)
	}
	defer runtimePool.Close()

	// Cross-tenant RLS must isolate tenantA, tenantB, and NULL-tenant rows cleanly.
	txB, err := runtimePool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = txB.Rollback(ctx) }()
	if _, err := txB.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true)`, tenantB); err != nil {
		t.Fatal(err)
	}
	var countForB int
	if err := txB.QueryRow(ctx, `SELECT count(*) FROM connectivity.gateway_tunnel_audit WHERE gateway_id IN ($1::uuid, $2::uuid)`, gatewayA, unknownGateway).Scan(&countForB); err != nil {
		t.Fatal(err)
	}
	if countForB != 0 {
		t.Fatalf("RLS leaked %d audit rows (tenantA or NULL-tenant) to tenantB", countForB)
	}

	txUnscoped, err := runtimePool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = txUnscoped.Rollback(ctx) }()
	var unscopedUnknown, unscopedTenantA int
	if err := txUnscoped.QueryRow(ctx, `SELECT count(*) FROM connectivity.gateway_tunnel_audit WHERE gateway_id = $1::uuid`, unknownGateway).Scan(&unscopedUnknown); err != nil {
		t.Fatal(err)
	}
	if unscopedUnknown != 1 {
		t.Fatalf("expected unscoped session to see 1 NULL-tenant unknownGateway row, got %d", unscopedUnknown)
	}
	if err := txUnscoped.QueryRow(ctx, `SELECT count(*) FROM connectivity.gateway_tunnel_audit WHERE gateway_id = $1::uuid`, gatewayA).Scan(&unscopedTenantA); err != nil {
		t.Fatal(err)
	}
	if unscopedTenantA != 0 {
		t.Fatalf("expected unscoped session to see 0 tenantA rows, got %d", unscopedTenantA)
	}

	txA, err := runtimePool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = txA.Rollback(ctx) }()
	if _, err := txA.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true)`, tenantA); err != nil {
		t.Fatal(err)
	}
	var countForA, countUnknownForA int
	if err := txA.QueryRow(ctx, `SELECT count(*) FROM connectivity.gateway_tunnel_audit WHERE gateway_id = $1::uuid`, gatewayA).Scan(&countForA); err != nil {
		t.Fatal(err)
	}
	if countForA != 3 {
		t.Fatalf("expected 3 tenantA tunnel audit rows (even for RETIRED gateway), got %d", countForA)
	}
	if err := txA.QueryRow(ctx, `SELECT count(*) FROM connectivity.gateway_tunnel_audit WHERE gateway_id = $1::uuid`, unknownGateway).Scan(&countUnknownForA); err != nil {
		t.Fatal(err)
	}
	if countUnknownForA != 0 {
		t.Fatalf("RLS leaked %d NULL-tenant rows to tenantA", countUnknownForA)
	}

	assertAuditMutationBlocked(t, ctx, admin, `UPDATE connectivity.gateway_tunnel_audit SET reason = 'tampered' WHERE gateway_id = $1::uuid`, gatewayA)
	assertAuditMutationBlocked(t, ctx, admin, `DELETE FROM connectivity.gateway_tunnel_audit WHERE gateway_id = $1::uuid`, gatewayA)
}

func assertAuditMutationBlocked(t *testing.T, ctx context.Context, pool *pgxpool.Pool, query string, args ...any) {
	t.Helper()
	_, err := pool.Exec(ctx, query, args...)
	if err == nil {
		t.Fatalf("expected append-only audit mutation to be rejected: %s", query)
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23000" {
		t.Fatalf("expected integrity_constraint_violation (23000) for %s, got %v", query, err)
	}
}
