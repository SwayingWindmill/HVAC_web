package gateway

import (
	"encoding/json"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/services/platform-gateway/pkg/platformapi"
)

const (
	registryTestTenantID    = "018f1d00-1000-7000-8000-000000000001"
	registryTestSiteID      = "018f1e00-1000-7000-8000-000000000002"
	registryTestEquipmentID = "018f1e00-1000-7000-8000-000000000003"
	registryTestDeviceID    = "018f1e00-1000-7000-8000-000000000004"
	registryTestBindingID   = "018f1e00-1000-7000-8000-000000000005"
	registryTestSensorID    = "018f1e00-1000-7000-8000-000000000006"
	registryTestPointID     = "018f1e00-1000-7000-8000-000000000007"
)

func TestRegistryV2RouteSet(t *testing.T) {
	tests := []struct {
		path   string
		action registryauth.Action
		list   bool
	}{
		{platformapi.ListSitesPath, registryauth.ActionSiteList, true},
		{"/api/v1/sites/" + registryTestSiteID, registryauth.ActionSiteRead, false},
		{"/api/v1/sites/" + registryTestSiteID + "/equipment", registryauth.ActionEquipmentList, true},
		{"/api/v1/equipment/" + registryTestEquipmentID, registryauth.ActionEquipmentRead, false},
		{"/api/v1/sites/" + registryTestSiteID + "/devices", registryauth.ActionDeviceList, true},
		{"/api/v1/sites/" + registryTestSiteID + "/device-bindings", registryauth.ActionDeviceBindingList, true},
		{"/api/v1/sites/" + registryTestSiteID + "/asset-model", registryauth.ActionAssetModelRead, false},
		{"/api/v1/devices/" + registryTestDeviceID, registryauth.ActionDeviceRead, false},
	}
	for _, test := range tests {
		route, _, ok := matchPublicRegistryRoute(test.path)
		if !ok {
			t.Fatalf("V2 Registry route not matched: %s", test.path)
		}
		if route.action != test.action || route.list != test.list {
			t.Fatalf("route %s = action %q list=%t; want action %q list=%t", test.path, route.action, route.list, test.action, test.list)
		}
	}
	for _, forbidden := range []string{
		"/api/v1/organizations",
		"/api/v1/organizations/018f1e00-1000-7000-8000-000000000001",
		"/api/v1/organizations/018f1e00-1000-7000-8000-000000000001/sites",
	} {
		if _, _, ok := matchPublicRegistryRoute(forbidden); ok {
			t.Fatalf("forbidden Organization Registry route still matched: %s", forbidden)
		}
	}
}

func TestRegistryExactSiteScopeIsFailClosed(t *testing.T) {
	allowed, err := validateExactSiteRegistryScope(registryauth.Decision{AllowedSiteIDs: []string{registryTestSiteID}})
	if err != nil {
		t.Fatalf("exact Site scope rejected: %v", err)
	}
	if _, ok := allowed[registryTestSiteID]; !ok || len(allowed) != 1 {
		t.Fatalf("exact Site scope = %#v", allowed)
	}

	cases := []registryauth.Decision{
		{},
		{AllowedSiteIDs: []string{registryTestSiteID}, DeniedSiteIDs: []string{registryTestSiteID}},
	}
	for index, decision := range cases {
		if _, err := validateExactSiteRegistryScope(decision); err == nil {
			t.Fatalf("invalid Registry scope case %d was accepted: %#v", index, decision)
		}
	}
}

func TestGatewayRegistryRejectsDeviceBindingScopeDrift(t *testing.T) {
	binding := registryDeviceBinding()
	binding.SiteID = "018f1e00-1000-7000-8000-000000000008"
	raw, err := json.Marshal(platformapi.DeviceBindingCollection{Items: []platformapi.DeviceBinding{binding}, NextCursor: nil, HasMore: false})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := canonicalRegistrySuccess(registryauth.ActionDeviceBindingList, registryTestSiteID, raw); err == nil {
		t.Fatal("cross-Site DeviceBinding response was accepted")
	}
}

func TestGatewayRegistryAcceptsV2SiteAssetModel(t *testing.T) {
	model := platformapi.SiteAssetModel{
		SchemaVersion: 2,
		TenantID:      registryTestTenantID,
		SiteID:        registryTestSiteID,
		Areas:         []platformapi.Area{},
		Equipment:     []platformapi.Equipment{},
		Devices:       []platformapi.Device{},
		Sensors:       []platformapi.Sensor{},
		TelemetryPoints: []platformapi.TelemetryPoint{},
		Relationships: []platformapi.AssetRelationship{},
		Counts:        platformapi.AssetModelCounts{},
	}
	raw, err := json.Marshal(model)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := canonicalRegistrySuccess(registryauth.ActionAssetModelRead, registryTestSiteID, raw); err != nil {
		t.Fatalf("V2 Site Asset Model rejected: %v", err)
	}
	model.SchemaVersion = 1
	raw, _ = json.Marshal(model)
	if _, err := canonicalRegistrySuccess(registryauth.ActionAssetModelRead, registryTestSiteID, raw); err == nil {
		t.Fatal("V1 Site Asset Model was accepted")
	}
}

func TestRegistryV2TelemetryPointValidation(t *testing.T) {
	unit := "Cel"
	point := platformapi.TelemetryPoint{
		ID: registryTestPointID, TenantID: registryTestTenantID, SiteID: registryTestSiteID,
		ReportingDeviceID: registryTestDeviceID, SensorID: nil,
		PointCode: "zone_temperature", SourceKey: "zone.temperature", DisplayName: "Zone temperature",
		PointType: "TELEMETRY", ValueType: "NUMBER", Unit: &unit, Writable: false,
		SampleIntervalMS: 1000, PublishIntervalMS: 1000, StaleAfterMS: 3000,
		SourceMetadata: map[string]any{}, Status: "ACTIVE", Revision: 1,
		CreatedAt: "2026-07-22T12:00:00.000Z", UpdatedAt: "2026-07-22T12:00:00.000Z",
	}
	if err := validateTelemetryPoint(point, registryTestSiteID); err != nil {
		t.Fatalf("valid V2 Point rejected: %v", err)
	}
	point.PointCode = "zone.temperature"
	if err := validateTelemetryPoint(point, registryTestSiteID); err == nil {
		t.Fatal("non-lower_snake_case Point Code was accepted")
	}
	point.PointCode = "zone_temperature"
	point.Writable = true
	if err := validateTelemetryPoint(point, registryTestSiteID); err == nil {
		t.Fatal("writable TELEMETRY Point was accepted")
	}
	point.PointType = "COMMAND"
	if err := validateTelemetryPoint(point, registryTestSiteID); err != nil {
		t.Fatalf("writable COMMAND Point rejected: %v", err)
	}
}

func registrySite() platformapi.Site {
	return platformapi.Site{
		ID: registryTestSiteID, TenantID: registryTestTenantID, Code: "site", DisplayName: "Site", Timezone: "UTC", Status: "ACTIVE", Revision: 1,
		CreatedAt: "2026-07-22T12:00:00.000Z", UpdatedAt: "2026-07-22T12:00:00.000Z",
	}
}

func registryEquipment() platformapi.Equipment {
	return platformapi.Equipment{
		ID: registryTestEquipmentID, TenantID: registryTestTenantID, SiteID: registryTestSiteID, Code: "equipment", DisplayName: "Equipment", EquipmentType: "AHU", Status: "ACTIVE", Revision: 1,
		CreatedAt: "2026-07-22T12:00:00.000Z", UpdatedAt: "2026-07-22T12:00:00.000Z",
	}
}

func registryDevice() platformapi.Device {
	return platformapi.Device{
		ID: registryTestDeviceID, TenantID: registryTestTenantID, SiteID: registryTestSiteID, Code: "device", DisplayName: "Device", DeviceType: "CONTROLLER", Status: "ACTIVE", Revision: 1,
		CreatedAt: "2026-07-22T12:00:00.000Z", UpdatedAt: "2026-07-22T12:00:00.000Z",
	}
}

func registryDeviceBinding() platformapi.DeviceBinding {
	return platformapi.DeviceBinding{
		ID: registryTestBindingID, TenantID: registryTestTenantID, SiteID: registryTestSiteID, DeviceID: registryTestDeviceID, EquipmentID: registryTestEquipmentID,
		BindingRole: "PRIMARY_CONTROLLER", Status: "ACTIVE", ValidFrom: "2026-07-22T12:00:00.000Z", Revision: 1,
		CreatedAt: "2026-07-22T12:00:00.000Z", UpdatedAt: "2026-07-22T12:00:00.000Z",
	}
}
