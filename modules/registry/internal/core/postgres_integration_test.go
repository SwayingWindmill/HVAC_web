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

	assetClaims := exactSiteClaims
	assetClaims.Actions = []registryauth.Action{registryauth.ActionAssetList}
	assets, err := store.ListAssets(ctx, assetClaims, testSiteA1, PageRequest{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(assets.Items) != 1 || assets.Items[0].ID != testAssetA1 {
		t.Fatalf("site-scoped assets = %#v", assets)
	}
	deniedAssetClaims := integrationClaims(registryauth.ActionAssetList)
	deniedAssetClaims.AllowedSiteIDs = []string{testSiteA1}
	deniedAssetClaims.DeniedSiteIDs = []string{testSiteA1}
	deniedAssets, err := store.ListAssets(ctx, deniedAssetClaims, testSiteA1, PageRequest{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(deniedAssets.Items) != 0 {
		t.Fatalf("denied assets = %#v", deniedAssets)
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
	if len(bindings.Items) != 1 || bindings.Items[0].ID != testBindingA1 || bindings.Items[0].DeviceID != testDeviceA1 || bindings.Items[0].AssetID != testAssetA1 {
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

func TestPostgresStoreResolvesEffectivePrimaryElectricityCounterBinding(t *testing.T) {
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

	const (
		meterDeviceID = "018f1e00-4000-7000-8000-000000000001"
		meterPointID  = "01990000-1340-7000-8000-000000000001"
		meterID       = "01990000-1330-7000-8000-000000000001"
		bindingID     = "01990000-1360-7000-8000-000000000001"
		topologyID    = "01990000-1300-7000-8000-000000000001"
		energyTypeID  = "01990000-0000-7000-8000-000000000001"
	)
	claims := integrationClaims(registryauth.ActionMeterBindingResolve)
	claims.AllowedSiteIDs = []string{testSiteA1}
	resolved, err := store.ResolveMeterBinding(ctx, claims, testSiteA1, MeterBindingResolveRequest{
		DeviceID: meterDeviceID, PointID: meterPointID, SampledAt: time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != "MATCH" || resolved.TenantID != testTenantA || resolved.SiteID != testSiteA1 ||
		resolved.MeterID != meterID || resolved.MeterBindingID != bindingID || resolved.TopologyVersionID != topologyID ||
		resolved.EnergyTypeID != energyTypeID || resolved.EnergyType != "electricity" || resolved.MeterRole != "PRIMARY" ||
		resolved.PointType != "COUNTER" || resolved.BindingVersion != 1 {
		t.Fatalf("resolved Meter Binding = %#v", resolved)
	}

	beforeEffective, err := store.ResolveMeterBinding(ctx, claims, testSiteA1, MeterBindingResolveRequest{
		DeviceID: meterDeviceID, PointID: meterPointID, SampledAt: time.Date(2026, 7, 31, 23, 59, 59, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if beforeEffective.Status != "NO_MATCH" {
		t.Fatalf("binding resolved before effective_from: %#v", beforeEffective)
	}

	deniedClaims := claims
	deniedClaims.AllowedSiteIDs = []string{testSiteA2}
	denied, err := store.ResolveMeterBinding(ctx, deniedClaims, testSiteA1, MeterBindingResolveRequest{
		DeviceID: meterDeviceID, PointID: meterPointID, SampledAt: time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if denied.Status != "NO_MATCH" {
		t.Fatalf("binding escaped exact Site RLS: %#v", denied)
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
