package gateway

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/quanlaihe/hvac-web/cmd/energy-api/internal/platformapi"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
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
		{"/api/v1/sites/" + registryTestSiteID + "/tariff", registryauth.ActionTariffRead, false},
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

	waterLoop := "CHILLED_WATER"
	branchCode := "CH-A"
	valveID := "018f1e00-3000-7000-8000-000000000091"
	hdrID := "018f1e00-3000-7000-8000-000000000092"
	now := "2026-11-18T08:00:00.000Z"
	hydraulicModel := platformapi.SiteAssetModel{
		SchemaVersion: 2, TenantID: registryTestTenantID, SiteID: registryTestSiteID,
		Spaces: []platformapi.Space{},
		Assets: []platformapi.Asset{
			{
				ID: registryTestAssetID, TenantID: registryTestTenantID, SiteID: registryTestSiteID,
				Code: "chiller-a", DisplayName: "1#冷机", AssetType: "CHILLER", Status: "ACTIVE",
				Revision: 1, CreatedAt: now, UpdatedAt: now, WaterLoop: &waterLoop, BranchCode: &branchCode,
			},
			{
				ID: valveID, TenantID: registryTestTenantID, SiteID: registryTestSiteID,
				Code: "mv-chw-a", DisplayName: "A支路电动阀", AssetType: "MOTORIZED_VALVE", Status: "ACTIVE",
				Revision: 1, CreatedAt: now, UpdatedAt: now, WaterLoop: &waterLoop, BranchCode: &branchCode,
			},
			{
				ID: hdrID, TenantID: registryTestTenantID, SiteID: registryTestSiteID,
				Code: "chw-header", DisplayName: "冷冻水总管", AssetType: "CHILLED_WATER_LOOP", Status: "ACTIVE",
				Revision: 1, CreatedAt: now, UpdatedAt: now,
			},
		},
		Devices: []platformapi.Device{}, Sensors: []platformapi.Sensor{}, TelemetryPoints: []platformapi.TelemetryPoint{},
		Relationships: []platformapi.AssetRelationship{
			{
				ID: "018f1e00-7000-7000-8000-000000000091", TenantID: registryTestTenantID, SiteID: registryTestSiteID,
				FromType: "ASSET", FromID: valveID, ToType: "ASSET", ToID: registryTestAssetID,
				Role: "ISOLATES", Status: "ACTIVE", ValidFrom: now, ValidTo: nil, Revision: 1, CreatedAt: now, UpdatedAt: now,
			},
			{
				ID: "018f1e00-7000-7000-8000-000000000092", TenantID: registryTestTenantID, SiteID: registryTestSiteID,
				FromType: "ASSET", FromID: registryTestAssetID, ToType: "ASSET", ToID: hdrID,
				Role: "FEEDS_HEADER", Status: "ACTIVE", ValidFrom: now, ValidTo: nil, Revision: 1, CreatedAt: now, UpdatedAt: now,
			},
		},
		Counts: platformapi.AssetModelCounts{Assets: 3},
	}
	hydraulicRaw, err := json.Marshal(hydraulicModel)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := canonicalRegistrySuccess(registryauth.ActionAssetModelRead, registryTestSiteID, hydraulicRaw); err != nil {
		t.Fatalf("hydraulic Site Asset Model rejected: %v", err)
	}
}

func TestGatewayRegistryAcceptsOnlyTheRequestedSitesTariff(t *testing.T) {
	tariff := platformapi.SiteTariff{
		SiteID: registryTestSiteID, DisplayName: "两部制分时电价", Version: 1, EffectiveFrom: "2026-01-01T00:00:00.000Z", Timezone: "Asia/Shanghai", Currency: "CNY",
		Periods: []platformapi.TariffPeriod{{PeriodCode: "PEAK", DayType: "WEEKDAY", StartMinute: 480, EndMinute: 660, EnergyRatePerKWh: 1.2, DemandRatePerKW: 38}},
	}
	raw, err := json.Marshal(tariff)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := canonicalRegistrySuccess(registryauth.ActionTariffRead, registryTestSiteID, raw); err != nil {
		t.Fatalf("the Site's Tariff was rejected: %v", err)
	}
	if _, err := canonicalRegistrySuccess(registryauth.ActionTariffRead, "018f1e00-1000-7000-8000-000000000008", raw); err == nil {
		t.Fatal("another Site's Tariff was accepted")
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
	// Registry includes both nullable counter fields for every telemetry point.
	raw, err := json.Marshal(point)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	payload["counterDecreaseMode"] = nil
	payload["counterRolloverModulus"] = nil
	raw, err = json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeCanonical[platformapi.TelemetryPoint](raw, func(value platformapi.TelemetryPoint) error {
		return validateTelemetryPoint(value, registryTestSiteID)
	}); err != nil {
		t.Fatalf("Registry point with canonical counter fields rejected: %v", err)
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

func TestGatewayRegistryAcceptsDeviceConnectivityMode(t *testing.T) {
	now := "2026-11-23T06:00:00.000Z"
	makeDevice := func(mode string) platformapi.Device {
		return platformapi.Device{
			ID: registryTestDeviceID, TenantID: registryTestTenantID, SiteID: registryTestSiteID,
			Code: "chiller-gateway-01", DisplayName: "1#冷机采集网关", DeviceType: "GATEWAY",
			Status: "ACTIVE", Revision: 1, CreatedAt: now, UpdatedAt: now,
			ConnectivityMode: mode,
		}
	}
	for _, mode := range []string{"", "GATEWAY_PROXIED", "DIRECT"} {
		device := makeDevice(mode)
		deviceRaw, err := json.Marshal(device)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := canonicalRegistrySuccess(registryauth.ActionDeviceRead, registryTestSiteID, deviceRaw); err != nil {
			t.Fatalf("ActionDeviceRead rejected ConnectivityMode=%q: %v", mode, err)
		}
		listRaw, err := json.Marshal(platformapi.DeviceCollection{Items: []platformapi.Device{device}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := canonicalRegistrySuccess(registryauth.ActionDeviceList, registryTestSiteID, listRaw); err != nil {
			t.Fatalf("ActionDeviceList rejected ConnectivityMode=%q: %v", mode, err)
		}
		modelRaw, err := json.Marshal(platformapi.SiteAssetModel{
			SchemaVersion: 2, TenantID: registryTestTenantID, SiteID: registryTestSiteID,
			Spaces: []platformapi.Space{}, Assets: []platformapi.Asset{},
			Devices: []platformapi.Device{device}, Sensors: []platformapi.Sensor{},
			TelemetryPoints: []platformapi.TelemetryPoint{}, Relationships: []platformapi.AssetRelationship{},
			Counts: platformapi.AssetModelCounts{DeviceEndpoints: 1},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := canonicalRegistrySuccess(registryauth.ActionAssetModelRead, registryTestSiteID, modelRaw); err != nil {
			t.Fatalf("ActionAssetModelRead rejected ConnectivityMode=%q: %v", mode, err)
		}
	}

	badRaw, err := json.Marshal(makeDevice("UNSUPPORTED"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := canonicalRegistrySuccess(registryauth.ActionDeviceRead, registryTestSiteID, badRaw); err == nil {
		t.Fatal("unsupported Device ConnectivityMode was accepted")
	}
}

func TestPlatformAPIRegistryStructsMatchOpenAPI(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "contracts", "http", "platform-gateway.openapi.yaml"))
	if err != nil {
		t.Fatalf("read platform-gateway OpenAPI contract: %v", err)
	}
	var spec struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("parse platform-gateway OpenAPI contract: %v", err)
	}

	schemaTypes := map[string]reflect.Type{
		"Site":              reflect.TypeOf(platformapi.Site{}),
		"Asset":             reflect.TypeOf(platformapi.Asset{}),
		"Device":            reflect.TypeOf(platformapi.Device{}),
		"DeviceBinding":     reflect.TypeOf(platformapi.DeviceBinding{}),
		"Space":             reflect.TypeOf(platformapi.Space{}),
		"Sensor":            reflect.TypeOf(platformapi.Sensor{}),
		"TelemetryPoint":    reflect.TypeOf(platformapi.TelemetryPoint{}),
		"AssetRelationship": reflect.TypeOf(platformapi.AssetRelationship{}),
		"AssetModelCounts":  reflect.TypeOf(platformapi.AssetModelCounts{}),
		"SiteAssetModel":    reflect.TypeOf(platformapi.SiteAssetModel{}),
	}

	for schemaName, structType := range schemaTypes {
		schema, ok := spec.Components.Schemas[schemaName]
		if !ok {
			t.Fatalf("OpenAPI contract missing schema %s", schemaName)
		}
		structFields := make(map[string]struct{}, structType.NumField())
		for i := 0; i < structType.NumField(); i++ {
			tag := structType.Field(i).Tag.Get("json")
			jsonName := strings.Split(tag, ",")[0]
			if jsonName == "" || jsonName == "-" {
				continue
			}
			structFields[jsonName] = struct{}{}
		}
		for prop := range schema.Properties {
			if _, exists := structFields[prop]; !exists {
				t.Errorf("platformapi.%s missing OpenAPI field %q", schemaName, prop)
			}
		}
		for jsonName := range structFields {
			if _, exists := schema.Properties[jsonName]; !exists {
				t.Errorf("platformapi.%s has extra field %q not in OpenAPI schema", schemaName, jsonName)
			}
		}
	}
}
