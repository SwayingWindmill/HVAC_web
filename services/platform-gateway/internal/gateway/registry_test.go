package gateway

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/services/platform-gateway/pkg/platformapi"
)

const (
	registryTestTenantID  = "018f1d00-1000-7000-8000-000000000001"
	registryTestSiteID    = "018f1e00-1000-7000-8000-000000000002"
	registryTestAssetID   = "018f1e00-1000-7000-8000-000000000003"
	registryTestDeviceID  = "018f1e00-1000-7000-8000-000000000004"
	registryTestBindingID = "018f1e00-1000-7000-8000-000000000005"
	registryTestPointID   = "018f1e00-1000-7000-8000-000000000007"
)

func TestRegistryCanonicalRoutes(t *testing.T) {
	tests := []struct {
		path   string
		action registryauth.Action
		list   bool
	}{
		{platformapi.ListSitesPath, registryauth.ActionSiteList, true},
		{"/api/v1/sites/" + registryTestSiteID, registryauth.ActionSiteRead, false},
		{"/api/v1/sites/" + registryTestSiteID + "/assets", registryauth.ActionAssetList, true},
		{"/api/v1/assets/" + registryTestAssetID, registryauth.ActionAssetRead, false},
		{"/api/v1/sites/" + registryTestSiteID + "/devices", registryauth.ActionDeviceList, true},
		{"/api/v1/sites/" + registryTestSiteID + "/device-bindings", registryauth.ActionDeviceBindingList, true},
		{"/api/v1/sites/" + registryTestSiteID + "/asset-model", registryauth.ActionAssetModelRead, false},
		{"/api/v1/devices/" + registryTestDeviceID, registryauth.ActionDeviceRead, false},
	}
	for _, test := range tests {
		route, _, ok := matchPublicRegistryRoute(http.MethodGet, test.path)
		if !ok || route.action != test.action || route.list != test.list {
			t.Fatalf("route %s did not resolve to action=%q list=%t", test.path, test.action, test.list)
		}
	}

	for _, forbidden := range []string{
		"/api/v1/organizations",
		"/api/v1/sites/" + registryTestSiteID + "/equipment",
		"/api/v1/equipment/" + registryTestAssetID,
	} {
		if _, _, ok := matchPublicRegistryRoute(http.MethodGet, forbidden); ok {
			t.Fatalf("legacy Registry route still matched: %s", forbidden)
		}
	}
}

func TestRegistryExactSiteScopeIsFailClosed(t *testing.T) {
	allowed, err := validateExactSiteRegistryScope(registryauth.Decision{AllowedSiteIDs: []string{registryTestSiteID}}, registryauth.ActionSiteRead)
	if err != nil || len(allowed) != 1 {
		t.Fatalf("exact Site scope rejected: allowed=%#v err=%v", allowed, err)
	}
	if _, ok := allowed[registryTestSiteID]; !ok {
		t.Fatalf("exact Site scope missing %s", registryTestSiteID)
	}
	if _, err := validateExactSiteRegistryScope(registryauth.Decision{}, registryauth.ActionSiteRead); err == nil {
		t.Fatal("empty Registry Site scope was accepted")
	}
	if allowed, err := validateExactSiteRegistryScope(registryauth.Decision{}, registryauth.ActionSiteWrite); err != nil || len(allowed) != 0 {
		t.Fatalf("Tenant-scoped Registry action rejected empty Site scope: allowed=%#v err=%v", allowed, err)
	}
}

func TestGatewayRegistryRejectsDeviceBindingScopeDrift(t *testing.T) {
	binding := platformapi.DeviceBinding{
		ID: registryTestBindingID, TenantID: registryTestTenantID, SiteID: "018f1e00-1000-7000-8000-000000000008",
		DeviceID: registryTestDeviceID, AssetID: registryTestAssetID, BindingRole: "PRIMARY_CONTROLLER", Status: "ACTIVE",
		ValidFrom: "2026-07-22T12:00:00.000Z", Revision: 1, CreatedAt: "2026-07-22T12:00:00.000Z", UpdatedAt: "2026-07-22T12:00:00.000Z",
	}
	raw, err := json.Marshal(platformapi.DeviceBindingCollection{Items: []platformapi.DeviceBinding{binding}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := canonicalRegistrySuccess(registryauth.ActionDeviceBindingList, registryTestSiteID, raw); err == nil {
		t.Fatal("cross-Site DeviceBinding response was accepted")
	}
}

func TestGatewayRegistryAcceptsCurrentSiteAssetModel(t *testing.T) {
	model := platformapi.SiteAssetModel{
		SchemaVersion: 2, TenantID: registryTestTenantID, SiteID: registryTestSiteID,
		Spaces: []platformapi.Space{}, Assets: []platformapi.Asset{}, Devices: []platformapi.Device{}, Sensors: []platformapi.Sensor{},
		TelemetryPoints: []platformapi.TelemetryPoint{}, Relationships: []platformapi.AssetRelationship{}, Counts: platformapi.AssetModelCounts{},
	}
	raw, err := json.Marshal(model)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := canonicalRegistrySuccess(registryauth.ActionAssetModelRead, registryTestSiteID, raw); err != nil {
		t.Fatalf("current Site Asset Model rejected: %v", err)
	}
}

func TestRegistryTelemetryPointCommandWriteInvariant(t *testing.T) {
	unit := "Cel"
	point := platformapi.TelemetryPoint{
		ID: registryTestPointID, TenantID: registryTestTenantID, SiteID: registryTestSiteID, ReportingDeviceID: registryTestDeviceID,
		PointCode: "zone_temperature", SourceKey: "zone.temperature", DisplayName: "Zone temperature", PointType: "TELEMETRY",
		ValueType: "NUMBER", Unit: &unit, Writable: false, SampleIntervalMS: 1000, PublishIntervalMS: 1000, StaleAfterMS: 3000,
		SourceMetadata: map[string]any{}, Status: "ACTIVE", Revision: 1, CreatedAt: "2026-07-22T12:00:00.000Z", UpdatedAt: "2026-07-22T12:00:00.000Z",
	}
	if err := validateTelemetryPoint(point, registryTestSiteID); err != nil {
		t.Fatalf("valid telemetry Point rejected: %v", err)
	}
	point.Writable = true
	if err := validateTelemetryPoint(point, registryTestSiteID); err == nil {
		t.Fatal("writable TELEMETRY Point was accepted")
	}
	point.PointType = "COMMAND"
	if err := validateTelemetryPoint(point, registryTestSiteID); err != nil {
		t.Fatalf("writable COMMAND Point rejected: %v", err)
	}
}
