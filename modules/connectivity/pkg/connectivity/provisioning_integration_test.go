package connectivity

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/adapter"
)

func openConnectivityIntegrationPools(t *testing.T) (context.Context, *pgxpool.Pool, *Store) {
	t.Helper()
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
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	store, err := Open(ctx, runtimeDSN)
	if err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		store.Close()
		admin.Close()
	})
	return ctx, admin, store
}

func cleanupTenantsConnectivityData(ctx context.Context, admin *pgxpool.Pool, tenants ...string) {
	_, _ = admin.Exec(ctx, `TRUNCATE TABLE connectivity.gateway_concentrator_audit, connectivity.gateway_credential_audit`)
	for _, tenant := range tenants {
		_, _ = admin.Exec(ctx, `DELETE FROM connectivity.provisioning_batch_devices WHERE tenant_id = $1::uuid`, tenant)
		_, _ = admin.Exec(ctx, `DELETE FROM connectivity.provisioning_batches WHERE tenant_id = $1::uuid`, tenant)
		_, _ = admin.Exec(ctx, `DELETE FROM connectivity.gateway_enrollments WHERE tenant_id = $1::uuid`, tenant)
		_, _ = admin.Exec(ctx, `DELETE FROM connectivity.gateway_credentials WHERE tenant_id = $1::uuid`, tenant)
		_, _ = admin.Exec(ctx, `DELETE FROM connectivity.uplink_quarantine WHERE tenant_id = $1::uuid`, tenant)
		_, _ = admin.Exec(ctx, `DELETE FROM core_registry.telemetry_points WHERE tenant_id = $1::uuid`, tenant)
		_, _ = admin.Exec(ctx, `DELETE FROM core_registry.gateway_device_source_keys WHERE tenant_id = $1::uuid`, tenant)
		_, _ = admin.Exec(ctx, `DELETE FROM core_registry.devices WHERE tenant_id = $1::uuid`, tenant)
		_, _ = admin.Exec(ctx, `DELETE FROM core_registry.sites WHERE tenant_id = $1::uuid`, tenant)
		_, _ = admin.Exec(ctx, `DELETE FROM iam.tenants WHERE id = $1::uuid`, tenant)
	}
}

func TestPostgresJITPProvisioningAndSecurityLifecycle(t *testing.T) {
	ctx, admin, store := openConnectivityIntegrationPools(t)
	const (
		tenantA      = "0194f800-0000-7000-8000-000000000001"
		tenantB      = "0194f800-0000-7000-8000-000000000002"
		siteA1       = "0194f800-1000-7000-8000-000000000001"
		siteA2       = "0194f800-1000-7000-8000-000000000002"
		siteB1       = "0194f800-1000-7000-8000-000000000003"
		actorA       = "0194f800-9000-7000-8000-000000000001"
		actorB       = "0194f800-9000-7000-8000-000000000002"
		thermostat1  = "0194f800-3000-7000-8000-000000000001"
		thermostat2  = "0194f800-3000-7000-8000-000000000002"
		thermostat3  = "0194f800-3000-7000-8000-000000000003"
		proxiedMeter = "0194f800-3000-7000-8000-000000000004"
		gatewayA1    = "0194f800-2000-7000-8000-000000000001"
	)

	cleanupTenantsConnectivityData(ctx, admin, tenantA, tenantB)
	t.Cleanup(func() { cleanupTenantsConnectivityData(ctx, admin, tenantA, tenantB) })

	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO iam.tenants(id,code,display_name,timezone,currency,country,status,revision,created_at,updated_at) VALUES ($1,'jitp-a','JITP A','UTC','USD','US','ACTIVE',1,now(),now()), ($2,'jitp-b','JITP B','UTC','USD','US','ACTIVE',1,now(),now())`, []any{tenantA, tenantB}},
		{`INSERT INTO core_registry.sites(id,tenant_id,code,display_name,timezone,status,revision,created_at,updated_at) VALUES ($1,$4,'jitp-site-a1','Site A1','UTC','ACTIVE',1,now(),now()), ($2,$4,'jitp-site-a2','Site A2','UTC','ACTIVE',1,now(),now()), ($3,$5,'jitp-site-b1','Site B1','UTC','ACTIVE',1,now(),now())`, []any{siteA1, siteA2, siteB1, tenantA, tenantB}},
		{`INSERT INTO core_registry.devices(id,tenant_id,site_id,code,display_name,device_type,connectivity_mode,status,revision,created_at,updated_at) VALUES
			($1,$6,$7,'jitp-therm-1','Therm 1','THERMOSTAT','DIRECT','ACTIVE',1,now(),now()),
			($2,$6,$7,'jitp-therm-2','Therm 2','THERMOSTAT','DIRECT','ACTIVE',1,now(),now()),
			($3,$6,$7,'jitp-therm-3','Therm 3','THERMOSTAT','DIRECT','ACTIVE',1,now(),now()),
			($4,$6,$7,'jitp-meter-proxied','Proxied Meter','HVAC_POWER_METER','GATEWAY_PROXIED','ACTIVE',1,now(),now()),
			($5,$6,$7,'jitp-gw-1','Gateway 1','GATEWAY','GATEWAY_PROXIED','ACTIVE',1,now(),now())`,
			[]any{thermostat1, thermostat2, thermostat3, proxiedMeter, gatewayA1, tenantA, siteA1}},
	} {
		if _, err := admin.Exec(ctx, stmt.sql, stmt.args...); err != nil {
			t.Fatal(err)
		}
	}

	now := time.Now().UTC().Truncate(time.Second)
	store.now = func() time.Time { return now }
	caPEM, keyPEM := credentialTestCA(t, now)
	authority, err := NewCertificateAuthority(caPEM, keyPEM, "tls://mqtt-broker:8883")
	if err != nil {
		t.Fatal(err)
	}
	service := NewCredentialService(store, authority)

	claimsA1 := registryauth.GrantClaims{TenantID: tenantA, PrincipalID: actorA, AllowedSiteIDs: []string{siteA1}}
	claimsA2 := registryauth.GrantClaims{TenantID: tenantA, PrincipalID: actorA, AllowedSiteIDs: []string{siteA2}}
	claimsB1 := registryauth.GrantClaims{TenantID: tenantB, PrincipalID: actorB, AllowedSiteIDs: []string{siteB1}}

	if _, err := service.CreateProvisioningBatch(ctx, claimsA1, CreateProvisioningBatchRequest{
		SiteID: siteA1, BatchName: "bad", ProductKey: "BAD-PROXIED", DeviceType: "THERMOSTAT", ConnectivityMode: "GATEWAY_PROXIED", MaxDevices: 5,
	}); !errors.Is(err, ErrProvisioningInvalid) {
		t.Fatalf("non-gateway proxied JITP batch err=%v, want ErrProvisioningInvalid", err)
	}

	created, err := service.CreateProvisioningBatch(ctx, claimsA1, CreateProvisioningBatchRequest{
		SiteID:     siteA1,
		BatchName:  "Songyaxuan Thermostats",
		ProductKey: "THERM-MODEL-V1",
		DeviceType: "THERMOSTAT",
		MaxDevices: 2,
		Devices:    []JITPDeviceEntry{{DeviceID: thermostat1, HardwareSerial: "SN-THERM-0001"}},
	})
	if err != nil {
		t.Fatalf("CreateProvisioningBatch: %v", err)
	}
	if len(created.ProductSecret) != 43 || created.Batch.ConnectivityMode != "DIRECT" || len(created.Devices) != 1 {
		t.Fatalf("unexpected batch creation result: %#v", created)
	}
	var storedHash string
	if err := admin.QueryRow(ctx, `SELECT product_secret_sha256 FROM connectivity.provisioning_batches WHERE id = $1::uuid`, created.Batch.ID).Scan(&storedHash); err != nil {
		t.Fatal(err)
	}
	if storedHash == created.ProductSecret || storedHash != hashCredential([]byte(created.ProductSecret)) {
		t.Fatalf("product secret was not hashed strictly with SHA-256")
	}

	if _, err := service.RegisterBatchDevices(ctx, claimsA1, created.Batch.ID, RegisterBatchDevicesRequest{
		Devices: []JITPDeviceEntry{{DeviceID: proxiedMeter, HardwareSerial: "SN-METER-X"}},
	}); !errors.Is(err, ErrProvisioningNotFound) {
		t.Fatalf("proxied meter in JITP batch err=%v, want ErrProvisioningNotFound", err)
	}

	if _, err := service.RegisterBatchDevices(ctx, claimsA1, created.Batch.ID, RegisterBatchDevicesRequest{
		Devices: []JITPDeviceEntry{{DeviceID: thermostat2, HardwareSerial: "SN-THERM-0002"}},
	}); err != nil {
		t.Fatalf("RegisterBatchDevices #2: %v", err)
	}
	if _, err := service.RegisterBatchDevices(ctx, claimsA1, created.Batch.ID, RegisterBatchDevicesRequest{
		Devices: []JITPDeviceEntry{{DeviceID: thermostat3, HardwareSerial: "SN-THERM-0003"}},
	}); !errors.Is(err, ErrProvisioningConflict) {
		t.Fatalf("exceeding MaxDevices err=%v, want ErrProvisioningConflict", err)
	}

	if listB, err := service.ListProvisioningBatches(ctx, claimsB1, ""); err != nil || len(listB) != 0 {
		t.Fatalf("Tenant B sees batches=%#v err=%v, want empty", listB, err)
	}
	if _, _, err := service.GetProvisioningBatch(ctx, claimsB1, created.Batch.ID); !errors.Is(err, ErrProvisioningNotFound) {
		t.Fatalf("Tenant B GetProvisioningBatch err=%v, want ErrProvisioningNotFound", err)
	}
	if _, _, err := service.GetProvisioningBatch(ctx, claimsA2, created.Batch.ID); !errors.Is(err, ErrCredentialForbidden) {
		t.Fatalf("Site A2 operator GetProvisioningBatch err=%v, want ErrCredentialForbidden", err)
	}

	csr1 := credentialTestCSR(t, "SN-THERM-0001")
	var issued1 IssuedCredential
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	start := make(chan struct{})
	for range 2 {
		wg.Go(func() {
			<-start
			res, enrollErr := service.EnrollJITP(ctx, JITPEnrollRequest{
				ProductKey:     "THERM-MODEL-V1",
				ProductSecret:  created.ProductSecret,
				HardwareSerial: "SN-THERM-0001",
				CSRPEM:         csr1,
			})
			if enrollErr == nil {
				issued1 = res
			}
			errs <- enrollErr
		})
	}
	close(start)
	wg.Wait()
	close(errs)
	wins, rejects := 0, 0
	for e := range errs {
		if e == nil {
			wins++
		} else if errors.Is(e, ErrEnrollmentRejected) {
			rejects++
		} else {
			t.Fatalf("unexpected concurrent JITP error: %v", e)
		}
	}
	if wins != 1 || rejects != 1 {
		t.Fatalf("concurrent JITP wins=%d rejects=%d, want 1/1", wins, rejects)
	}

	block, _ := pem.Decode([]byte(issued1.CertificatePEM))
	cert1, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if cert1.Subject.CommonName != thermostat1 || issued1.DeviceID != thermostat1 {
		t.Fatalf("issued cert CN=%q deviceId=%q, want %s", cert1.Subject.CommonName, issued1.DeviceID, thermostat1)
	}
	if verifiedID, err := service.VerifyActiveCredential(ctx, cert1); err != nil || verifiedID != thermostat1 {
		t.Fatalf("VerifyActiveCredential after JITP=(%q, %v)", verifiedID, err)
	}

	for _, bad := range []JITPEnrollRequest{
		{ProductKey: "THERM-MODEL-V1", ProductSecret: created.ProductSecret, HardwareSerial: "SN-THERM-0001", CSRPEM: csr1},
		{ProductKey: "THERM-MODEL-V1", ProductSecret: "wrong-secret-012345678901234567890123456789", HardwareSerial: "SN-THERM-0002", CSRPEM: credentialTestCSR(t, "SN-THERM-0002")},
		{ProductKey: "THERM-MODEL-V1", ProductSecret: created.ProductSecret, HardwareSerial: "SN-THERM-0002", CSRPEM: credentialTestCSR(t, "SN-SPOOF")},
	} {
		if _, err := service.EnrollJITP(ctx, bad); !errors.Is(err, ErrEnrollmentRejected) {
			t.Fatalf("invalid JITP request %s err=%v, want ErrEnrollmentRejected", bad.HardwareSerial, err)
		}
	}

	spiffeGW, _ := url.Parse("spiffe://hvac.local/platform-gateway")
	httpHandler := NewCredentialHandler(service, nil)
	jitpBody, _ := json.Marshal(JITPEnrollRequest{
		ProductKey: "THERM-MODEL-V1", ProductSecret: created.ProductSecret, HardwareSerial: "SN-THERM-0002", CSRPEM: credentialTestCSR(t, "SN-THERM-0002"),
	})
	httpReq := httptest.NewRequest(http.MethodPost, "/gateway/v1/jitp/enroll", bytes.NewReader(jitpBody))
	httpReq.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{URIs: []*url.URL{spiffeGW}}}}}
	httpRec := httptest.NewRecorder()
	httpHandler.ServeHTTP(httpRec, httpReq)
	if httpRec.Code != http.StatusOK || httpRec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("HTTP JITP status=%d cache=%q body=%s", httpRec.Code, httpRec.Header().Get("Cache-Control"), httpRec.Body.String())
	}

	revokedBatch, err := service.RevokeProvisioningBatch(ctx, claimsA1, created.Batch.ID)
	if err != nil || revokedBatch.Status != "REVOKED" {
		t.Fatalf("RevokeProvisioningBatch=%#v err=%v", revokedBatch, err)
	}
	var auditCount int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM connectivity.gateway_credential_audit WHERE gateway_id = $1::uuid AND action = 'JITP_ACTIVATED'`, thermostat1).Scan(&auditCount); err != nil || auditCount != 1 {
		t.Fatalf("JITP_ACTIVATED audit count=%d err=%v", auditCount, err)
	}
	if err := service.Revoke(ctx, claimsA1, thermostat1); err != nil {
		t.Fatalf("Revoke direct device: %v", err)
	}
	if _, err := service.VerifyActiveCredential(ctx, cert1); !errors.Is(err, ErrCredentialRevoked) {
		t.Fatalf("revoked direct device cert accepted: %v", err)
	}
}

func TestPostgresDirectDeviceUplinkCoverageAndCommandRoute(t *testing.T) {
	ctx, admin, store := openConnectivityIntegrationPools(t)
	const (
		tenantA      = "0194f900-0000-7000-8000-000000000001"
		siteA        = "0194f900-1000-7000-8000-000000000001"
		actorA       = "0194f900-9000-7000-8000-000000000001"
		directDev    = "0194f900-2000-7000-8000-000000000001"
		siblingGW    = "0194f900-2000-7000-8000-000000000002"
		proxiedMeter = "0194f900-3000-7000-8000-000000000001"
		directTempPt = "0194f900-4000-7000-8000-000000000001"
		directCmdPt  = "0194f900-4000-7000-8000-000000000002"
		proxiedPt    = "0194f900-4000-7000-8000-000000000003"
	)

	cleanupTenantsConnectivityData(ctx, admin, tenantA)
	t.Cleanup(func() { cleanupTenantsConnectivityData(ctx, admin, tenantA) })

	now := time.Now().UTC().Truncate(time.Second)
	store.now = func() time.Time { return now }

	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO iam.tenants(id,code,display_name,timezone,currency,country,status,revision,created_at,updated_at) VALUES ($1,'direct-t','Direct Tenant','UTC','USD','US','ACTIVE',1,$2,$2)`, []any{tenantA, now}},
		{`INSERT INTO core_registry.sites(id,tenant_id,code,display_name,timezone,status,revision,created_at,updated_at) VALUES ($1,$2,'direct-s','Direct Site','UTC','ACTIVE',1,$3,$3)`, []any{siteA, tenantA, now}},
		{`INSERT INTO core_registry.devices(id,tenant_id,site_id,code,display_name,device_type,connectivity_mode,status,revision,created_at,updated_at) VALUES
			($1,$4,$5,'thermostat-direct-01','Direct Thermostat','THERMOSTAT','DIRECT','ACTIVE',1,$6,$6),
			($2,$4,$5,'gw-sibling','Sibling Gateway','GATEWAY','GATEWAY_PROXIED','ACTIVE',1,$6,$6),
			($3,$4,$5,'meter-proxied','Proxied Meter','HVAC_POWER_METER','GATEWAY_PROXIED','ACTIVE',1,$6,$6)`,
			[]any{directDev, siblingGW, proxiedMeter, tenantA, siteA, now}},
		{`INSERT INTO core_registry.gateway_device_source_keys(id,tenant_id,site_id,gateway_device_id,source_key,device_id,status,revision,created_at,updated_at) VALUES
			('0194f900-5000-7000-8000-000000000001',$1,$2,$3,'METER-01',$4,'ACTIVE',1,$5,$5)`,
			[]any{tenantA, siteA, siblingGW, proxiedMeter, now}},
		{`INSERT INTO core_registry.telemetry_points(id,tenant_id,site_id,reporting_device_id,point_code,source_key,display_name,point_type,value_type,unit,writable,sample_interval_ms,publish_interval_ms,stale_after_ms,source_metadata,status,revision,created_at,updated_at) VALUES
			($1,$4,$5,$6,'zone_temp','zone_temp','Zone Temp','TELEMETRY','NUMBER','Cel',false,1000,5000,30000,'{}'::jsonb,'ACTIVE',1,$8,$8),
			($2,$4,$5,$6,'setpoint_cmd','setpoint_cmd','Setpoint Cmd','COMMAND','NUMBER','Cel',true,1000,5000,30000,'{"actuationMode":"ANALOG","channel":"ao1"}'::jsonb,'ACTIVE',1,$8,$8),
			($3,$4,$5,$7,'active_power','active_power','Active Power','TELEMETRY','NUMBER','kW',false,1000,5000,30000,'{}'::jsonb,'ACTIVE',1,$8,$8)`,
			[]any{directTempPt, directCmdPt, proxiedPt, tenantA, siteA, directDev, proxiedMeter, now}},
		{`INSERT INTO connectivity.gateway_credentials(id,tenant_id,gateway_id,certificate_fingerprint_sha256,status,valid_from,valid_until,revoked_at,created_at,updated_at) VALUES
			('0194f900-6000-7000-8000-000000000001',$1,$2,repeat('d',64),'ACTIVE',$3,$4,NULL,$3,$3)`,
			[]any{tenantA, directDev, now.Add(-time.Minute), now.Add(time.Hour)}},
	} {
		if _, err := admin.Exec(ctx, stmt.sql, stmt.args...); err != nil {
			t.Fatal(err)
		}
	}

	resolved, err := store.ResolveGateway(ctx, directDev)
	if err != nil {
		t.Fatalf("ResolveGateway(directDev): %v", err)
	}
	wantGW := adapter.Gateway{ID: directDev, TenantID: tenantA, SiteID: siteA, DirectDevice: true, DeviceCode: "thermostat-direct-01"}
	if resolved != wantGW {
		t.Fatalf("ResolveGateway=%#v, want %#v", resolved, wantGW)
	}

	if _, err := store.ResolveGateway(ctx, proxiedMeter); !errors.Is(err, adapter.ErrGatewayUnknown) {
		t.Fatalf("proxied meter ResolveGateway err=%v, want ErrGatewayUnknown", err)
	}

	for _, selfKey := range []string{directDev, "self", "thermostat-direct-01"} {
		if got, err := store.ResolveDevice(ctx, resolved, selfKey); err != nil || got != directDev {
			t.Fatalf("ResolveDevice(%q)=(%q, %v), want %s", selfKey, got, err, directDev)
		}
	}
	for _, foreignKey := range []string{"METER-01", proxiedMeter, siblingGW} {
		if got, err := store.ResolveDevice(ctx, resolved, foreignKey); err != nil || got != "" {
			t.Fatalf("ResolveDevice(foreign=%q)=(%q, %v), want empty", foreignKey, got, err)
		}
	}

	bindings, err := store.ResolveBindings(ctx, resolved, []adapter.DevicePointQuery{
		{SourceKey: "thermostat-direct-01", PointCode: "zone_temp"},
		{SourceKey: "METER-01", PointCode: "active_power"},
	})
	if err != nil {
		t.Fatalf("ResolveBindings: %v", err)
	}
	if bindings.Device("thermostat-direct-01") != directDev || bindings.Point(directDev, "zone_temp") == nil || bindings.Device("METER-01") != "" {
		t.Fatalf("unexpected direct device bindings: %#v", bindings)
	}

	scopes, err := store.ListActiveManagedGatewayScopes(ctx)
	if err != nil {
		t.Fatalf("ListActiveManagedGatewayScopes: %v", err)
	}
	foundDirectScope := false
	for _, sc := range scopes {
		if sc.GatewayID == directDev && len(sc.DeviceIDs) == 1 && sc.DeviceIDs[0] == directDev {
			foundDirectScope = true
		}
	}
	if !foundDirectScope {
		t.Fatalf("expected directDev self-coverage scope in %#v", scopes)
	}

	route, err := store.ResolveCommandRoute(ctx, commandmodel.DispatchEnvelope{
		TenantID: tenantA,
		SiteID:   siteA,
		DeviceID: directDev,
		PointID:  directCmdPt,
	})
	if err != nil || route.GatewayID != directDev || route.ExternalDeviceID != "thermostat-direct-01" {
		t.Fatalf("direct ResolveCommandRoute=%#v err=%v", route, err)
	}

	caPEM, keyPEM := credentialTestCA(t, now)
	authority, err := NewCertificateAuthority(caPEM, keyPEM, "tls://mqtt-broker:8883")
	if err != nil {
		t.Fatal(err)
	}
	svc := NewCredentialService(store, authority)
	if err := svc.Revoke(ctx, registryauth.GrantClaims{TenantID: tenantA, PrincipalID: actorA, AllowedSiteIDs: []string{siteA}}, directDev); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ResolveCommandRoute(ctx, commandmodel.DispatchEnvelope{
		TenantID: tenantA, SiteID: siteA, DeviceID: directDev, PointID: directCmdPt,
	}); !errors.Is(err, commandmodel.ErrCommandGatewayCredentialInactive) {
		t.Fatalf("revoked direct command route err=%v, want ErrCommandGatewayCredentialInactive", err)
	}
}
