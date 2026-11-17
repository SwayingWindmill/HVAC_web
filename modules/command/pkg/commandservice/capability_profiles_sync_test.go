package commandservice

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRuntimeHTTPCapabilityProfilesSync(t *testing.T) {
	stub := &runtimeStoreStub{}
	handler := runtimeTestHandler(t, stub)

	validPayload := `{
		"tenantId": "` + runtimeTestOrganization + `",
		"profiles": [{
			"capabilityName": "template.chiller.head_pressure",
			"capabilityRevision": "v1",
			"status": "VERIFIED",
			"canonicalUnit": "kPa",
			"minimumValue": 100.0,
			"maximumValue": 1500.0,
			"maximumDelta": 50.0,
			"riskLevel": "MEDIUM",
			"approvalPolicy": "NONE",
			"retryPolicy": "PRE_SEND_ONLY",
			"connectorKind": "MODBUS_TCP"
		}]
	}`

	for _, deniedSPIFFE := range []string{
		"spiffe://hvac.local/command-dispatcher",
		"spiffe://hvac.local/command-verifier",
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, runtimeRequest(t, http.MethodPost, InternalCapabilityProfilesSyncPath, validPayload, deniedSPIFFE))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for %s, got %d (%s)", deniedSPIFFE, rec.Code, rec.Body.String())
		}
	}

	for _, invalidPayload := range []string{
		`{"profiles":[{"capabilityName":"missing_tenant","capabilityRevision":"v1","status":"VERIFIED","canonicalUnit":"kPa","minimumValue":1,"maximumValue":2,"maximumDelta":1,"riskLevel":"LOW","approvalPolicy":"NONE","retryPolicy":"PRE_SEND_ONLY","connectorKind":"MODBUS_TCP"}]}`,
		`{"tenantId":"` + runtimeTestOrganization + `","profiles":[]}`,
		`{"tenantId":"` + runtimeTestOrganization + `","profiles":[{"capabilityName":"bad","capabilityRevision":"v1","status":"INVALID","canonicalUnit":"kPa","minimumValue":1,"maximumValue":2,"maximumDelta":1,"riskLevel":"LOW","approvalPolicy":"NONE","retryPolicy":"PRE_SEND_ONLY","connectorKind":"MODBUS_TCP"}]}`,
		`{"tenantId":"` + runtimeTestOrganization + `","profiles":[{"capabilityName":"bad_range","capabilityRevision":"v1","status":"VERIFIED","canonicalUnit":"kPa","minimumValue":10,"maximumValue":5,"maximumDelta":1,"riskLevel":"LOW","approvalPolicy":"NONE","retryPolicy":"PRE_SEND_ONLY","connectorKind":"MODBUS_TCP"}]}`,
		`{"tenantId":"` + runtimeTestOrganization + `","profiles":[{"capabilityName":"bad_retry","capabilityRevision":"v1","status":"VERIFIED","canonicalUnit":"kPa","minimumValue":1,"maximumValue":5,"maximumDelta":1,"riskLevel":"LOW","approvalPolicy":"NONE","retryPolicy":"RETRY","connectorKind":"MODBUS_TCP"}]}`,
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, runtimeRequest(t, http.MethodPost, InternalCapabilityProfilesSyncPath, invalidPayload, "spiffe://hvac.local/platform-core-service"))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for payload %s, got %d (%s)", invalidPayload, rec.Code, rec.Body.String())
		}
	}

	stub.syncErr = ErrCapabilityProfileConflict
	conflictRec := httptest.NewRecorder()
	handler.ServeHTTP(conflictRec, runtimeRequest(t, http.MethodPost, InternalCapabilityProfilesSyncPath, validPayload, "spiffe://hvac.local/platform-core-service"))
	if conflictRec.Code != http.StatusConflict {
		t.Fatalf("expected 409 on capability conflict, got %d (%s)", conflictRec.Code, conflictRec.Body.String())
	}
	stub.syncErr = nil

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, runtimeRequest(t, http.MethodPost, InternalCapabilityProfilesSyncPath, validPayload, "spiffe://hvac.local/platform-core-service"))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d (%s)", rec.Code, rec.Body.String())
	}
	var resp struct {
		SyncedCount int `json:"syncedCount"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil || resp.SyncedCount != 1 {
		t.Fatalf("unexpected response (%+v, %v)", resp, err)
	}
	if stub.syncTenantID != runtimeTestOrganization || len(stub.syncedProfiles) != 1 || stub.syncedProfiles[0].CapabilityName != "template.chiller.head_pressure" {
		t.Fatalf("unexpected stub syncedProfiles: tenant=%s profiles=%+v", stub.syncTenantID, stub.syncedProfiles)
	}
}

func TestPostgresSyncCapabilityProfilesUpsertsAndIsIdempotent(t *testing.T) {
	runtimeURL, adminURL := commandPostgresTestURLs(t)
	ctx := t.Context()

	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()

	opened, err := OpenPostgresStore(ctx, runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	store := NewPostgresStore(opened.pool, fixedPostgresClock(), nil)

	const tenantA = "018f2e00-0000-7000-8000-000000000001"
	const tenantB = "018f2e00-0000-7000-8000-000000000002"
	const capName = "template.p1_test.chiller_setpoint"
	const capRev = "v1"
	cleanup := func() {
		_, _ = admin.Exec(ctx, `DELETE FROM command_runtime.capability_profiles WHERE capability_name = $1 AND capability_revision = $2`, capName, capRev)
	}
	cleanup()
	t.Cleanup(cleanup)

	first := CapabilityProfileSyncItem{
		CapabilityName:     capName,
		CapabilityRevision: capRev,
		Status:             "DRAFT",
		CanonicalUnit:      "degC",
		MinimumValue:       5.0,
		MaximumValue:       15.0,
		MaximumDelta:       2.0,
		RiskLevel:          "LOW",
		ApprovalPolicy:     "NONE",
		RetryPolicy:        "PRE_SEND_ONLY",
		ConnectorKind:      "MODBUS_TCP",
	}
	t1 := store.now().UTC()
	t2 := t1.Add(time.Minute)
	synced, err := store.SyncCapabilityProfiles(ctx, tenantA, t1, []CapabilityProfileSyncItem{first})
	if err != nil || synced != 1 {
		t.Fatalf("initial SyncCapabilityProfiles failed: synced=%d err=%v", synced, err)
	}

	updated := first
	updated.Status = "VERIFIED"
	updated.MaximumValue = 18.0
	synced, err = store.SyncCapabilityProfiles(ctx, tenantA, t2, []CapabilityProfileSyncItem{updated})
	if err != nil || synced != 1 {
		t.Fatalf("idempotent upsert SyncCapabilityProfiles failed: synced=%d err=%v", synced, err)
	}

	// Stale out-of-order retry with earlier timestamp must not overwrite newer profile state.
	if staleSynced, err := store.SyncCapabilityProfiles(ctx, tenantA, t1, []CapabilityProfileSyncItem{first}); err != nil || staleSynced != 0 {
		t.Fatalf("stale retry should no-op cleanly, got synced=%d err=%v", staleSynced, err)
	}

	// Cross-tenant overwrite of another tenant's profile must be rejected.
	if _, err := store.SyncCapabilityProfiles(ctx, tenantB, t2, []CapabilityProfileSyncItem{first}); err == nil {
		t.Fatal("expected cross-tenant capability profile overwrite to fail")
	}

	// Overwrite of seeded platform capability (owner_tenant_id IS NULL) must be rejected.
	platformCap := first
	platformCap.CapabilityName = "SET_TEMPERATURE_SETPOINT"
	platformCap.CapabilityRevision = "capability:set-temperature-setpoint:v1"
	if _, err := store.SyncCapabilityProfiles(ctx, tenantA, t2, []CapabilityProfileSyncItem{platformCap}); err == nil {
		t.Fatal("expected tenant overwrite of built-in platform capability to fail")
	}

	var status string
	var maxVal float64
	if err := admin.QueryRow(ctx, `
SELECT status, maximum_value
FROM command_runtime.capability_profiles
WHERE capability_name = $1 AND capability_revision = $2
`, capName, capRev).Scan(&status, &maxVal); err != nil {
		t.Fatalf("query synced capability_profiles row: %v", err)
	}
	if status != "VERIFIED" || maxVal != 18.0 {
		t.Fatalf("expected updated profile (VERIFIED, 18.0), got (%q, %v)", status, maxVal)
	}
}
