package connectivity

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRevokedCredentialInvalidatesGatewaySessionAndRoutes(t *testing.T) {
	dsn := os.Getenv("CONNECTIVITY_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("CONNECTIVITY_POSTGRES_DSN is not set")
	}

	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()

	const (
		tenantID      = "0191f000-0000-7000-8000-000000000001"
		siteID        = "0191f000-0000-7000-8000-000000000002"
		deviceID      = "0191f000-0000-7000-8000-000000000003"
		profileID     = "0191f000-0000-7000-8000-000000000004"
		integrationID = "0191f000-0000-7000-8000-000000000005"
		deviceBindID  = "0191f000-0000-7000-8000-000000000006"
		childBindID   = "0191f000-0000-7000-8000-000000000007"
		credentialID  = "0191f000-0000-7000-8000-000000000008"
		sessionID     = "0191f000-0000-7000-8000-000000000009"
		gatewayID     = "S09-GATEWAY-REVOCATION"
		externalID    = "S09-CHILD-REVOCATION"
	)

	cleanup := func() {
		_, _ = admin.Exec(ctx, `DELETE FROM connectivity.audit_facts WHERE tenant_id=$1::uuid`, tenantID)
		_, _ = admin.Exec(ctx, `DELETE FROM connectivity.command_reply_correlations WHERE tenant_id=$1::uuid`, tenantID)
		_, _ = admin.Exec(ctx, `DELETE FROM connectivity.connector_ownership_leases WHERE tenant_id=$1::uuid`, tenantID)
		_, _ = admin.Exec(ctx, `DELETE FROM connectivity.sessions WHERE tenant_id=$1::uuid`, tenantID)
		_, _ = admin.Exec(ctx, `DELETE FROM connectivity.enrollments WHERE tenant_id=$1::uuid`, tenantID)
		_, _ = admin.Exec(ctx, `DELETE FROM connectivity.gateway_child_bindings WHERE tenant_id=$1::uuid`, tenantID)
		_, _ = admin.Exec(ctx, `DELETE FROM connectivity.device_bindings WHERE tenant_id=$1::uuid`, tenantID)
		_, _ = admin.Exec(ctx, `DELETE FROM connectivity.credential_refs WHERE tenant_id=$1::uuid`, tenantID)
		_, _ = admin.Exec(ctx, `DELETE FROM connectivity.integration_instances WHERE tenant_id=$1::uuid`, tenantID)
		_, _ = admin.Exec(ctx, `DELETE FROM connectivity.transport_profiles WHERE tenant_id=$1::uuid`, tenantID)
		_, _ = admin.Exec(ctx, `DELETE FROM core_registry.devices WHERE tenant_id=$1::uuid`, tenantID)
		_, _ = admin.Exec(ctx, `DELETE FROM core_registry.sites WHERE tenant_id=$1::uuid`, tenantID)
		_, _ = admin.Exec(ctx, `DELETE FROM iam.tenants WHERE id=$1::uuid`, tenantID)
	}
	cleanup()
	t.Cleanup(cleanup)

	now := time.Now().UTC()
	statements := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO iam.tenants (id,code,display_name,timezone,currency,country,status,revision,created_at,updated_at) VALUES ($1::uuid,'s09-revocation-test','S09 revocation test','UTC','USD','US','ACTIVE',1,$2,$2)`, []any{tenantID, now}},
		{`INSERT INTO core_registry.sites (id,code,display_name,timezone,status,revision,created_at,updated_at,tenant_id) VALUES ($1::uuid,'s09-revocation-site','S09 revocation site','UTC','ACTIVE',1,$3,$3,$2::uuid)`, []any{siteID, tenantID, now}},
		{`INSERT INTO core_registry.devices (id,site_id,code,display_name,device_type,status,revision,created_at,updated_at,tenant_id) VALUES ($1::uuid,$2::uuid,'s09-revocation-device','S09 revocation device','CONTROLLER','ACTIVE',1,$4,$4,$3::uuid)`, []any{deviceID, siteID, tenantID, now}},
		{`INSERT INTO connectivity.transport_profiles (id,tenant_id,protocol,broker_origin,topic_namespace,status,revision,created_at,updated_at) VALUES ($1::uuid,$2::uuid,'MQTT','tls://mqtt-broker:8883','energy/v1','ACTIVE',1,$3,$3)`, []any{profileID, tenantID, now}},
		{`INSERT INTO connectivity.integration_instances (id,tenant_id,site_id,transport_profile_id,gateway_external_id,status,revision,created_at,updated_at) VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,'ACTIVE',1,$6,$6)`, []any{integrationID, tenantID, siteID, profileID, gatewayID, now}},
		{`INSERT INTO connectivity.device_bindings (id,tenant_id,site_id,integration_instance_id,device_id,external_device_id,status,valid_from,valid_to,revision,created_at,updated_at) VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,'ACTIVE',$7,NULL,1,$7,$7)`, []any{deviceBindID, tenantID, siteID, integrationID, deviceID, externalID, now}},
		{`INSERT INTO connectivity.gateway_child_bindings (id,tenant_id,site_id,integration_instance_id,gateway_external_id,child_device_id,child_external_id,status,valid_from,valid_to,revision,created_at,updated_at) VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6::uuid,$7,'ACTIVE',$8,NULL,1,$8,$8)`, []any{childBindID, tenantID, siteID, integrationID, gatewayID, deviceID, externalID, now}},
		{`INSERT INTO connectivity.credential_refs (id,tenant_id,integration_instance_id,credential_kind,secret_ref,certificate_fingerprint_sha256,token_hash_sha256,status,valid_from,valid_until,rotated_from_id,revoked_at,revision,created_at,updated_at) VALUES ($1::uuid,$2::uuid,$3::uuid,'MTLS_CERTIFICATE','secretref://s09/revocation','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',NULL,'ACTIVE',$4,$5,NULL,NULL,1,$4,$4)`, []any{credentialID, tenantID, integrationID, now.Add(-time.Minute), now.Add(time.Hour)}},
		{`INSERT INTO connectivity.sessions (id,tenant_id,site_id,integration_instance_id,credential_ref_id,credential_revision,gateway_external_id,status,opened_at,expires_at,closed_at,close_reason,revision,updated_at) VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,1,$6,'ACTIVE',$7,$8,NULL,NULL,1,$7)`, []any{sessionID, tenantID, siteID, integrationID, credentialID, gatewayID, now, now.Add(30 * time.Minute)}},
	}
	for _, statement := range statements {
		if _, err := admin.Exec(ctx, statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}

	store, err := Open(ctx, dsn, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := store.AuthorizeGateway(ctx, integrationID, tenantID, siteID, gatewayID); err != nil {
		t.Fatalf("active credential/session did not authorize Gateway: %v", err)
	}
	if _, err := store.ResolveCommandRoute(ctx, integrationID, tenantID, siteID, gatewayID, deviceID); err != nil {
		t.Fatalf("active credential/session did not authorize command route: %v", err)
	}

	if err := store.RevokeCredential(ctx, credentialID, "integration-test"); err != nil {
		t.Fatal(err)
	}
	if err := store.AuthorizeGateway(ctx, integrationID, tenantID, siteID, gatewayID); !errors.Is(err, ErrBindingNotFound) {
		t.Fatalf("revoked credential Gateway authorization error=%v want=%v", err, ErrBindingNotFound)
	}
	if _, err := store.ResolveCommandRoute(ctx, integrationID, tenantID, siteID, gatewayID, deviceID); !errors.Is(err, ErrBindingNotFound) {
		t.Fatalf("revoked credential command route error=%v want=%v", err, ErrBindingNotFound)
	}

	var credentialStatus, sessionStatus, closeReason string
	if err := admin.QueryRow(ctx, `
SELECT c.status, s.status, s.close_reason
FROM connectivity.credential_refs c
JOIN connectivity.sessions s ON s.tenant_id=c.tenant_id AND s.credential_ref_id=c.id
WHERE c.tenant_id=$1::uuid AND c.id=$2::uuid
`, tenantID, credentialID).Scan(&credentialStatus, &sessionStatus, &closeReason); err != nil {
		t.Fatal(err)
	}
	if credentialStatus != "REVOKED" || sessionStatus != "INVALIDATED" || closeReason != "CREDENTIAL_REVOKED:integration-test" {
		t.Fatalf("credential/session state=%s/%s reason=%q", credentialStatus, sessionStatus, closeReason)
	}
}
