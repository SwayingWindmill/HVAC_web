package connectivity

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/adapter"
)

// TestUplinkResolvesEveryGatewayThroughTheRegistryReadPort runs as connectivity_runtime
// against the Registry views and RLS: two Gateways in two Tenants are served by one
// store that is not pinned to either Tenant.
func TestUplinkResolvesEveryGatewayThroughTheRegistryReadPort(t *testing.T) {
	adminDSN, runtimeDSN := os.Getenv("CONNECTIVITY_ADMIN_DSN"), os.Getenv("CONNECTIVITY_POSTGRES_DSN")
	if adminDSN == "" || runtimeDSN == "" {
		t.Skip("CONNECTIVITY_ADMIN_DSN and CONNECTIVITY_POSTGRES_DSN are not set")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()

	const (
		tenantA  = "0192f000-0000-7000-8000-00000000000a"
		tenantB  = "0192f000-0000-7000-8000-00000000000b"
		siteA    = "0192f000-1000-7000-8000-00000000000a"
		siteB    = "0192f000-1000-7000-8000-00000000000b"
		gatewayA = "0192f000-2000-7000-8000-00000000000a"
		gatewayB = "0192f000-2000-7000-8000-00000000000b"
		meterA   = "0192f000-3000-7000-8000-00000000000a"
		meterB   = "0192f000-3000-7000-8000-00000000000b"
		pointA   = "0192f000-4000-7000-8000-00000000000a"
		commandA = "0192f000-4000-7000-8000-0000000000ca"
		commandB = "0192f000-4000-7000-8000-0000000000cb"
		unknown  = "0192f000-2000-7000-8000-0000000000ff"
	)
	cleanup := func() {
		for _, statement := range []string{
			`DELETE FROM connectivity.gateway_credential_audit WHERE gateway_id IN ($1::uuid,$2::uuid)`,
			`DELETE FROM connectivity.gateway_enrollments WHERE gateway_id IN ($1::uuid,$2::uuid)`,
			`DELETE FROM connectivity.uplink_quarantine WHERE gateway_id IN ($1, $2, $3)`,
			`DELETE FROM connectivity.gateway_credentials WHERE gateway_id IN ($1::uuid, $2::uuid)`,
		} {
			_, _ = admin.Exec(ctx, statement, gatewayA, gatewayB, unknown)
		}
		for _, tenant := range []string{tenantA, tenantB} {
			_, _ = admin.Exec(ctx, `DELETE FROM core_registry.telemetry_points WHERE tenant_id=$1::uuid`, tenant)
			_, _ = admin.Exec(ctx, `DELETE FROM core_registry.gateway_device_source_keys WHERE tenant_id=$1::uuid`, tenant)
			_, _ = admin.Exec(ctx, `DELETE FROM core_registry.devices WHERE tenant_id=$1::uuid`, tenant)
			_, _ = admin.Exec(ctx, `DELETE FROM core_registry.sites WHERE tenant_id=$1::uuid`, tenant)
			_, _ = admin.Exec(ctx, `DELETE FROM iam.tenants WHERE id=$1::uuid`, tenant)
		}
	}
	cleanup()
	t.Cleanup(cleanup)

	now := time.Now().UTC()
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO iam.tenants (id,code,display_name,timezone,currency,country,status,revision,created_at,updated_at) VALUES
		  ($1::uuid,'uplink-a','Uplink A','UTC','USD','US','ACTIVE',1,$3,$3), ($2::uuid,'uplink-b','Uplink B','UTC','USD','US','ACTIVE',1,$3,$3)`, []any{tenantA, tenantB, now}},
		{`INSERT INTO core_registry.sites (id,code,display_name,timezone,status,revision,created_at,updated_at,tenant_id) VALUES
		  ($1::uuid,'uplink-site-a','Site A','UTC','ACTIVE',1,$5,$5,$3::uuid), ($2::uuid,'uplink-site-b','Site B','UTC','ACTIVE',1,$5,$5,$4::uuid)`, []any{siteA, siteB, tenantA, tenantB, now}},
		{`INSERT INTO core_registry.devices (id,tenant_id,site_id,code,display_name,device_type,status,revision,created_at,updated_at) VALUES
		  ($1::uuid,$5::uuid,$7::uuid,'gateway-a','Gateway A','GATEWAY','ACTIVE',1,$9,$9),
		  ($2::uuid,$6::uuid,$8::uuid,'gateway-b','Gateway B','GATEWAY','ACTIVE',1,$9,$9),
		  ($3::uuid,$5::uuid,$7::uuid,'meter-a','Meter A','HVAC_POWER_METER','ACTIVE',1,$9,$9),
		  ($4::uuid,$6::uuid,$8::uuid,'meter-b','Meter B','HVAC_POWER_METER','ACTIVE',1,$9,$9)`, []any{gatewayA, gatewayB, meterA, meterB, tenantA, tenantB, siteA, siteB, now}},
		{`INSERT INTO core_registry.gateway_device_source_keys (id,tenant_id,site_id,gateway_device_id,source_key,device_id,status,revision,created_at,updated_at) VALUES
		  ('0192f000-5000-7000-8000-00000000000a',$1::uuid,$3::uuid,$5::uuid,'METER-01',$7::uuid,'ACTIVE',1,$9,$9),
		  ('0192f000-5000-7000-8000-00000000000b',$2::uuid,$4::uuid,$6::uuid,'METER-01',$8::uuid,'ACTIVE',1,$9,$9)`, []any{tenantA, tenantB, siteA, siteB, gatewayA, gatewayB, meterA, meterB, now}},
		{`INSERT INTO core_registry.telemetry_points (id,tenant_id,site_id,reporting_device_id,point_code,source_key,display_name,point_type,value_type,unit,sample_interval_ms,publish_interval_ms,stale_after_ms,status,revision,created_at,updated_at) VALUES
		  ($1::uuid,$2::uuid,$3::uuid,$4::uuid,'active_power','meter.active_power','Active power','TELEMETRY','NUMBER','kW',1000,5000,30000,'ACTIVE',1,$5,$5)`, []any{pointA, tenantA, siteA, meterA, now}},
		{`INSERT INTO core_registry.telemetry_points (id,tenant_id,site_id,reporting_device_id,point_code,source_key,display_name,point_type,value_type,unit,writable,sample_interval_ms,publish_interval_ms,stale_after_ms,status,revision,created_at,updated_at) VALUES
		  ($1::uuid,$3::uuid,$5::uuid,$7::uuid,'run_command','meter.run_command','Run command','COMMAND','BOOLEAN',NULL,true,1000,5000,30000,'ACTIVE',1,$9,$9),
		  ($2::uuid,$4::uuid,$6::uuid,$8::uuid,'run_command','meter.run_command','Run command','COMMAND','BOOLEAN',NULL,true,1000,5000,30000,'ACTIVE',1,$9,$9)`, []any{commandA, commandB, tenantA, tenantB, siteA, siteB, meterA, meterB, now}},
		{`INSERT INTO connectivity.gateway_credentials (id,tenant_id,gateway_id,certificate_fingerprint_sha256,status,valid_from,valid_until,revoked_at,created_at,updated_at) VALUES
		  ('0192f000-6000-7000-8000-00000000000a',$1::uuid,$3::uuid,repeat('a',64),'ACTIVE',$5,$6,NULL,$5,$5),
		  ('0192f000-6000-7000-8000-00000000000b',$2::uuid,$4::uuid,repeat('b',64),'ACTIVE',$5,$6,NULL,$5,$5)`, []any{tenantA, tenantB, gatewayA, gatewayB, now.Add(-time.Minute), now.Add(time.Hour)}},
	} {
		if _, err := admin.Exec(ctx, statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}

	store, err := Open(ctx, runtimeDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	resolvedA, err := store.ResolveGateway(ctx, gatewayA)
	if err != nil || resolvedA != (adapter.Gateway{ID: gatewayA, TenantID: tenantA, SiteID: siteA}) {
		t.Fatalf("Gateway A=%#v err=%v", resolvedA, err)
	}
	resolvedB, err := store.ResolveGateway(ctx, gatewayB)
	if err != nil || resolvedB != (adapter.Gateway{ID: gatewayB, TenantID: tenantB, SiteID: siteB}) {
		t.Fatalf("Gateway B=%#v err=%v", resolvedB, err)
	}
	if device, err := store.ResolveDevice(ctx, resolvedB, "METER-01"); err != nil || device != meterB {
		t.Fatalf("Gateway B METER-01=%q err=%v", device, err)
	}
	if device, err := store.ResolveDevice(ctx, resolvedA, "UNKNOWN"); err != nil || device != "" {
		t.Fatalf("unregistered source key=%q err=%v", device, err)
	}
	if point, err := store.ResolvePoint(ctx, resolvedA, meterA, "active_power"); err != nil || point == nil || point.PointID != pointA || point.PointRevision != 1 {
		t.Fatalf("Point=%#v err=%v", point, err)
	}
	if point, err := store.ResolvePoint(ctx, resolvedB, meterA, "active_power"); err != nil || point != nil {
		t.Fatalf("Tenant B resolved Tenant A's Point=%#v err=%v", point, err)
	}
	if _, err := store.ResolveGateway(ctx, unknown); !errors.Is(err, adapter.ErrGatewayUnknown) {
		t.Fatalf("unknown Gateway err=%v", err)
	}

	if tenants, err := store.Tenants(ctx); err != nil || !containsAll(tenants, tenantA, tenantB) {
		t.Fatalf("Tenants=%v err=%v", tenants, err)
	}
	if tenant, err := store.GatewayTenant(ctx, gatewayB); err != nil || tenant != tenantB {
		t.Fatalf("Gateway B Tenant=%q err=%v", tenant, err)
	}
	commandTo := func(tenantID, siteID, deviceID, pointID string) commandmodel.DispatchEnvelope {
		return commandmodel.DispatchEnvelope{TenantID: tenantID, SiteID: siteID, DeviceID: deviceID, PointID: pointID}
	}
	if route, err := store.ResolveCommandRoute(ctx, commandTo(tenantB, siteB, meterB, commandB)); err != nil ||
		route != (commandmodel.DeviceRoute{GatewayID: gatewayB, ExternalDeviceID: "METER-01", BindingRevision: 1}) {
		t.Fatalf("command route=%#v err=%v", route, err)
	}
	if _, err := store.ResolveCommandRoute(ctx, commandTo(tenantA, siteA, meterA, pointA)); !errors.Is(err, commandmodel.ErrCommandControlDisabled) {
		t.Fatalf("read-only Point err=%v", err)
	}
	if _, err := store.ResolveCommandRoute(ctx, commandTo(tenantA, siteA, gatewayA, commandA)); !errors.Is(err, commandmodel.ErrCommandRouteNotFound) {
		t.Fatalf("Device without a source key err=%v", err)
	}
	if _, err := store.ResolveCommandRoute(ctx, commandTo(tenantB, siteB, meterA, commandA)); !errors.Is(err, commandmodel.ErrCommandRouteNotFound) {
		t.Fatalf("Tenant B routed Tenant A's Device err=%v", err)
	}

	caPEM, keyPEM := credentialTestCA(t, now)
	authority, err := NewCertificateAuthority(caPEM, keyPEM, "tls://mqtt-broker:8883")
	if err != nil {
		t.Fatal(err)
	}
	if err := NewCredentialService(store, authority).Revoke(ctx, registryauth.GrantClaims{TenantID: tenantB, PrincipalID: gatewayB, AllowedSiteIDs: []string{siteB}}, gatewayB); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ResolveCommandRoute(ctx, commandTo(tenantB, siteB, meterB, commandB)); !errors.Is(err, commandmodel.ErrCommandGatewayCredentialInactive) {
		t.Fatalf("revoked Gateway command err=%v", err)
	}
	revoked, err := store.ResolveGateway(ctx, gatewayB)
	if !errors.Is(err, adapter.ErrGatewayCredentialInactive) || revoked.TenantID != tenantB {
		t.Fatalf("revoked Gateway=%#v err=%v", revoked, err)
	}
	for _, quarantine := range []adapter.UplinkQuarantine{
		{TenantID: tenantB, GatewayID: gatewayB, Topic: "hvac/v1/" + gatewayB + "/up/telemetry", ReasonCode: adapter.QuarantineGatewayCredentialInactive, Detail: "revoked", Payload: []byte(`{}`), ReceivedAt: now},
		{GatewayID: unknown, Topic: "hvac/v1/" + unknown + "/up/telemetry", ReasonCode: adapter.QuarantineGatewayUnknown, Detail: "unknown", Payload: []byte(`{}`), ReceivedAt: now},
	} {
		if err := store.QuarantineUplink(ctx, quarantine); err != nil {
			t.Fatalf("quarantine %s: %v", quarantine.ReasonCode, err)
		}
	}
	var evidence string
	if err := admin.QueryRow(ctx, `
SELECT string_agg(gateway_id || ':' || coalesce(tenant_id::text, '-') || ':' || reason_code, ',' ORDER BY reason_code)
FROM connectivity.uplink_quarantine WHERE gateway_id IN ($1, $2)`, gatewayB, unknown).Scan(&evidence); err != nil {
		t.Fatal(err)
	}
	if want := gatewayB + ":" + tenantB + ":GATEWAY_CREDENTIAL_INACTIVE," + unknown + ":-:GATEWAY_UNKNOWN"; evidence != want {
		t.Fatalf("quarantine evidence=%s want=%s", evidence, want)
	}
}

func containsAll(values []string, wanted ...string) bool {
	seen := map[string]bool{}
	for _, value := range values {
		seen[value] = true
	}
	for _, value := range wanted {
		if !seen[value] {
			return false
		}
	}
	return true
}
