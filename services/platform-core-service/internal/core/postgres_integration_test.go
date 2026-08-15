package core

import (
	"context"
	"errors"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

func TestPostgresStoreAppliesTenantAndExactSiteRLSWithStablePagination(t *testing.T) {
	databaseURL := os.Getenv("S1_CORE_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("S1_CORE_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store, err := OpenPostgresStore(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	siteClaims := integrationClaims(registryauth.ActionSiteList)
	siteClaims.AllowedSiteIDs = []string{testSiteA1, testSiteA2}
	first, err := store.ListSites(ctx, siteClaims, PageRequest{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 1 || !first.HasMore || first.Items[0].ID != testSiteA1 {
		t.Fatalf("first Site page = %#v", first)
	}
	second, err := store.ListSites(ctx, siteClaims, PageRequest{Limit: 1, DisplayName: first.Items[0].DisplayName, ID: first.Items[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.HasMore || second.Items[0].ID != testSiteA2 {
		t.Fatalf("second Site page = %#v", second)
	}

	exactSiteClaims := integrationClaims(registryauth.ActionSiteList)
	exactSiteClaims.AllowedSiteIDs = []string{testSiteA1}
	sites, err := store.ListSites(ctx, exactSiteClaims, PageRequest{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(sites.Items) != 1 || sites.Items[0].ID != testSiteA1 {
		t.Fatalf("exact Site list = %#v", sites)
	}

	deniedSiteClaims := integrationClaims(registryauth.ActionSiteList)
	deniedSiteClaims.AllowedSiteIDs = []string{testSiteA1, testSiteA2}
	deniedSiteClaims.DeniedSiteIDs = []string{testSiteA2}
	allowedSites, err := store.ListSites(ctx, deniedSiteClaims, PageRequest{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(allowedSites.Items) != 1 || allowedSites.Items[0].ID != testSiteA1 {
		t.Fatalf("explicit Site deny list = %#v", allowedSites)
	}
	deniedSiteClaims.Actions = []registryauth.Action{registryauth.ActionSiteRead}
	if _, err := store.GetSite(ctx, deniedSiteClaims, testSiteA2); !errors.Is(err, ErrNotFound) {
		t.Fatalf("explicitly denied Site error = %v", err)
	}

	detailClaims := exactSiteClaims
	detailClaims.Actions = []registryauth.Action{registryauth.ActionSiteRead}
	if _, err := store.GetSite(ctx, detailClaims, testSiteA2); !errors.Is(err, ErrNotFound) {
		t.Fatalf("sibling Site error = %v", err)
	}

	equipmentClaims := exactSiteClaims
	equipmentClaims.Actions = []registryauth.Action{registryauth.ActionEquipmentList}
	equipment, err := store.ListEquipment(ctx, equipmentClaims, testSiteA1, PageRequest{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(equipment.Items) != 1 || equipment.Items[0].ID != testEquipmentA1 {
		t.Fatalf("site-scoped equipment = %#v", equipment)
	}
	deniedEquipmentClaims := integrationClaims(registryauth.ActionEquipmentList)
	deniedEquipmentClaims.AllowedSiteIDs = []string{testSiteA1}
	deniedEquipmentClaims.DeniedSiteIDs = []string{testSiteA1}
	deniedEquipment, err := store.ListEquipment(ctx, deniedEquipmentClaims, testSiteA1, PageRequest{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(deniedEquipment.Items) != 0 {
		t.Fatalf("denied equipment = %#v", deniedEquipment)
	}

	deviceClaims := exactSiteClaims
	deviceClaims.Actions = []registryauth.Action{registryauth.ActionDeviceList}
	devices, err := store.ListDevices(ctx, deviceClaims, testSiteA1, PageRequest{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(devices.Items) != 1 || devices.Items[0].ID != testDeviceA1 {
		t.Fatalf("site-scoped devices = %#v", devices)
	}
	deniedDeviceClaims := integrationClaims(registryauth.ActionDeviceList)
	deniedDeviceClaims.AllowedSiteIDs = []string{testSiteA1}
	deniedDeviceClaims.DeniedSiteIDs = []string{testSiteA1}
	deniedDevices, err := store.ListDevices(ctx, deniedDeviceClaims, testSiteA1, PageRequest{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(deniedDevices.Items) != 0 {
		t.Fatalf("denied devices = %#v", deniedDevices)
	}

	bindingClaims := exactSiteClaims
	bindingClaims.Actions = []registryauth.Action{registryauth.ActionDeviceBindingList}
	bindings, err := store.ListDeviceBindings(ctx, bindingClaims, testSiteA1, PageRequest{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings.Items) != 1 || bindings.Items[0].ID != testBindingA1 || bindings.Items[0].DeviceID != testDeviceA1 || bindings.Items[0].EquipmentID != testEquipmentA1 {
		t.Fatalf("site-scoped DeviceBindings = %#v", bindings)
	}
	deniedBindingClaims := integrationClaims(registryauth.ActionDeviceBindingList)
	deniedBindingClaims.AllowedSiteIDs = []string{testSiteA1}
	deniedBindingClaims.DeniedSiteIDs = []string{testSiteA1}
	deniedBindings, err := store.ListDeviceBindings(ctx, deniedBindingClaims, testSiteA1, PageRequest{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(deniedBindings.Items) != 0 {
		t.Fatalf("denied DeviceBindings = %#v", deniedBindings)
	}
}

func TestPostgresBackedServerRejectsWrongActionStaleAndRevokedGrants(t *testing.T) {
	databaseURL := os.Getenv("S1_CORE_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("S1_CORE_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store, err := OpenPostgresStore(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	tests := []struct {
		name   string
		claims registryauth.GrantClaims
		status GrantStatusProvider
	}{
		{"wrong action", integrationClaims(registryauth.ActionDeviceRead), StaticGrantStatusProvider{PolicyRevision: testPolicy}},
		{"stale policy", integrationClaims(registryauth.ActionSiteList), StaticGrantStatusProvider{PolicyRevision: "registry-read:2"}},
		{"revoked", integrationClaims(registryauth.ActionSiteList), StaticGrantStatusProvider{PolicyRevision: testPolicy, RevokedTokenIDs: map[string]struct{}{"grant-1": {}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			harness := newCoreHarness(t, now, store, test.status)
			response := harness.serve(t, http.MethodGet, RegistryPathPrefix+"sites", test.claims, nil)
			if response.Code != http.StatusForbidden {
				t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestCoreServiceDatabaseIdentityCannotReadIAMOrMigrationDatasets(t *testing.T) {
	databaseURL := os.Getenv("S1_CORE_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("S1_CORE_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close(ctx)

	queries := map[string]string{
		"IAM principals":       `SELECT count(*) FROM iam.principals`,
		"migration provenance": `SELECT count(*) FROM core_registry.migration_provenance`,
		"migration quarantine": `SELECT count(*) FROM core_registry.migration_quarantine`,
	}
	for name, query := range queries {
		t.Run(name, func(t *testing.T) {
			transaction, err := connection.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
			if err != nil {
				t.Fatal(err)
			}
			defer transaction.Rollback(ctx)
			if _, err := transaction.Exec(ctx, `SET LOCAL ROLE s1_core_runtime`); err != nil {
				t.Fatal(err)
			}
			if _, err := transaction.Exec(ctx, query); err == nil {
				t.Fatalf("Core runtime unexpectedly read %s", name)
			}
		})
	}
}

func integrationClaims(action registryauth.Action) registryauth.GrantClaims {
	claims := testGrantClaims(action)
	claims.IssuedAt = time.Now().Add(-time.Second).Unix()
	claims.ExpiresAt = time.Now().Add(20 * time.Second).Unix()
	return claims
}
