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

func TestPostgresStoreAppliesOrganizationAndSiteRLSWithStablePagination(t *testing.T) {
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

	organizationClaims := integrationClaims(registryauth.ActionOrganizationList)
	organizationClaims.AllowedOrganizationIDs = []string{testOrganizationA, testOrganizationB}
	first, err := store.ListOrganizations(ctx, organizationClaims, PageRequest{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 1 || !first.HasMore || first.Items[0].ID != testOrganizationA {
		t.Fatalf("first page = %#v", first)
	}
	second, err := store.ListOrganizations(ctx, organizationClaims, PageRequest{Limit: 1, DisplayName: first.Items[0].DisplayName, ID: first.Items[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.HasMore || second.Items[0].ID != testOrganizationB {
		t.Fatalf("second page = %#v", second)
	}

	siteClaims := integrationClaims(registryauth.ActionSiteList)
	siteClaims.AllowedOrganizationIDs = nil
	siteClaims.AllowedSiteIDs = []string{testSiteA1}
	sites, err := store.ListSites(ctx, siteClaims, testOrganizationA, PageRequest{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(sites.Items) != 1 || sites.Items[0].ID != testSiteA1 {
		t.Fatalf("site-scoped list = %#v", sites)
	}

	deniedSiteClaims := integrationClaims(registryauth.ActionSiteList)
	deniedSiteClaims.AllowedOrganizationIDs = []string{testOrganizationA}
	deniedSiteClaims.AllowedSiteIDs = nil
	deniedSiteClaims.DeniedSiteIDs = []string{testSiteA2}
	allowedSites, err := store.ListSites(ctx, deniedSiteClaims, testOrganizationA, PageRequest{Limit: 10})
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

	deniedOrganizationClaims := integrationClaims(registryauth.ActionSiteRead)
	deniedOrganizationClaims.AllowedOrganizationIDs = nil
	deniedOrganizationClaims.AllowedSiteIDs = []string{testSiteA1}
	deniedOrganizationClaims.DeniedOrganizationIDs = []string{testOrganizationA}
	if _, err := store.GetSite(ctx, deniedOrganizationClaims, testSiteA1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("organization-denied Site error = %v", err)
	}

	detailClaims := siteClaims
	detailClaims.Actions = []registryauth.Action{registryauth.ActionSiteRead}
	if _, err := store.GetSite(ctx, detailClaims, testSiteA2); !errors.Is(err, ErrNotFound) {
		t.Fatalf("sibling Site error = %v", err)
	}

	equipmentClaims := siteClaims
	equipmentClaims.Actions = []registryauth.Action{registryauth.ActionEquipmentList}
	equipment, err := store.ListEquipment(ctx, equipmentClaims, testSiteA1, PageRequest{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(equipment.Items) != 1 || equipment.Items[0].ID != testEquipmentA1 {
		t.Fatalf("site-scoped equipment = %#v", equipment)
	}
	deniedEquipmentClaims := integrationClaims(registryauth.ActionEquipmentList)
	deniedEquipmentClaims.AllowedOrganizationIDs = []string{testOrganizationA}
	deniedEquipmentClaims.DeniedSiteIDs = []string{testSiteA1}
	deniedEquipment, err := store.ListEquipment(ctx, deniedEquipmentClaims, testSiteA1, PageRequest{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(deniedEquipment.Items) != 0 {
		t.Fatalf("denied equipment = %#v", deniedEquipment)
	}

	deviceClaims := siteClaims
	deviceClaims.Actions = []registryauth.Action{registryauth.ActionDeviceList}
	devices, err := store.ListDevices(ctx, deviceClaims, testSiteA1, PageRequest{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(devices.Items) != 1 || devices.Items[0].ID != testDeviceA1 {
		t.Fatalf("site-scoped devices = %#v", devices)
	}
	deniedDeviceClaims := integrationClaims(registryauth.ActionDeviceList)
	deniedDeviceClaims.AllowedOrganizationIDs = []string{testOrganizationA}
	deniedDeviceClaims.DeniedSiteIDs = []string{testSiteA1}
	deniedDevices, err := store.ListDevices(ctx, deniedDeviceClaims, testSiteA1, PageRequest{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(deniedDevices.Items) != 0 {
		t.Fatalf("denied devices = %#v", deniedDevices)
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
		{"wrong action", integrationClaims(registryauth.ActionOrganizationRead), StaticGrantStatusProvider{PolicyRevision: testPolicy}},
		{"stale policy", integrationClaims(registryauth.ActionOrganizationList), StaticGrantStatusProvider{PolicyRevision: "registry-read:2"}},
		{"revoked", integrationClaims(registryauth.ActionOrganizationList), StaticGrantStatusProvider{PolicyRevision: testPolicy, RevokedTokenIDs: map[string]struct{}{"grant-1": {}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			harness := newCoreHarness(t, now, store, test.status)
			response := harness.serve(t, http.MethodGet, RegistryPathPrefix+"organizations", test.claims, nil)
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
