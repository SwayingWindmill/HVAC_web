package core

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

const (
	testGrantIssuer         = "spiffe://hvac.local/iam-service"
	testPresenter           = "spiffe://hvac.local/platform-gateway"
	testOperationsPresenter = "spiffe://hvac.local/operations-agent-service"
	testAudience            = "platform-core-service"
	testPolicy              = "registry-read:1"
	testPrincipal           = "018f1e00-2000-7000-8000-000000000002"
	testTenantA             = "018f1d00-0000-7000-8000-000000000001"
	testSiteA1              = "018f1e00-1000-7000-8000-000000000001"
	testSiteA2              = "018f1e00-1000-7000-8000-000000000002"
	testAssetA1             = "018f1e00-3000-7000-8000-000000000001"
	testDeviceA1            = "018f1e00-4000-7000-8000-000000000001"
	testBindingA1           = "018f1e00-5000-7000-8000-000000000001"
)

type countingGrantStatusProvider struct {
	calls  int
	status registryauth.GrantStatus
}

func (provider *countingGrantStatusProvider) Lookup(_ context.Context, _ registryauth.GrantClaims) (registryauth.GrantStatus, error) {
	provider.calls++
	return provider.status, nil
}

type fakeRegistryStore struct {
	sites            PageResult[Site]
	site             Site
	assets           PageResult[Asset]
	assetItem        Asset
	devices          PageResult[Device]
	device           Device
	bindings         PageResult[DeviceBinding]
	siteBindings     SiteMeterBindings
	totalSources     SiteTotalElectricitySources
	spaceChildren    PageResult[Space]
	devicePoints     PageResult[TelemetryPoint]
	assetModel       SiteAssetModel
	tariff           SiteTariff
	templates        PageResult[Template]
	templateRevision TemplateRevision
	templateAssigns  PageResult[TemplateAssignment]
	err              error
	lastClaims       registryauth.GrantClaims
	lastPage         PageRequest
	lastID           string
	lastAssetFilter  AssetFilter
	lastDeviceFilter DeviceFilter
	lastTemplateKind TemplateKind
	lastTargetType   TemplateKind
	lastTargetID     string
}

func (store *fakeRegistryStore) ListSites(_ context.Context, claims registryauth.GrantClaims, page PageRequest) (PageResult[Site], error) {
	store.lastClaims, store.lastPage = claims, page
	return store.sites, store.err
}
func (store *fakeRegistryStore) GetSite(_ context.Context, claims registryauth.GrantClaims, id string) (Site, error) {
	store.lastClaims, store.lastID = claims, id
	return store.site, store.err
}
func (store *fakeRegistryStore) ListAssets(_ context.Context, claims registryauth.GrantClaims, id string, page PageRequest) (PageResult[Asset], error) {
	return store.ListAssetsFiltered(context.Background(), claims, id, AssetFilter{}, page)
}
func (store *fakeRegistryStore) ListAssetsFiltered(_ context.Context, claims registryauth.GrantClaims, id string, filter AssetFilter, page PageRequest) (PageResult[Asset], error) {
	store.lastClaims, store.lastID, store.lastAssetFilter, store.lastPage = claims, id, filter, page
	return store.assets, store.err
}
func (store *fakeRegistryStore) GetAsset(_ context.Context, claims registryauth.GrantClaims, id string) (Asset, error) {
	store.lastClaims, store.lastID = claims, id
	return store.assetItem, store.err
}
func (store *fakeRegistryStore) ListDevices(_ context.Context, claims registryauth.GrantClaims, id string, page PageRequest) (PageResult[Device], error) {
	return store.ListDevicesFiltered(context.Background(), claims, id, DeviceFilter{}, page)
}
func (store *fakeRegistryStore) ListDevicesFiltered(_ context.Context, claims registryauth.GrantClaims, id string, filter DeviceFilter, page PageRequest) (PageResult[Device], error) {
	store.lastClaims, store.lastID, store.lastDeviceFilter, store.lastPage = claims, id, filter, page
	return store.devices, store.err
}
func (store *fakeRegistryStore) ListTemplates(_ context.Context, claims registryauth.GrantClaims, kind TemplateKind, page PageRequest) (PageResult[Template], error) {
	store.lastClaims, store.lastTemplateKind, store.lastPage = claims, kind, page
	return store.templates, store.err
}
func (store *fakeRegistryStore) GetTemplateRevision(_ context.Context, claims registryauth.GrantClaims, id string) (TemplateRevision, error) {
	store.lastClaims, store.lastID = claims, id
	return store.templateRevision, store.err
}
func (store *fakeRegistryStore) ListSiteTemplateAssignments(_ context.Context, claims registryauth.GrantClaims, siteID string, targetType TemplateKind, targetID string, page PageRequest) (PageResult[TemplateAssignment], error) {
	store.lastClaims, store.lastID, store.lastTargetType, store.lastTargetID, store.lastPage = claims, siteID, targetType, targetID, page
	return store.templateAssigns, store.err
}
func (store *fakeRegistryStore) GetDevice(_ context.Context, claims registryauth.GrantClaims, id string) (Device, error) {
	store.lastClaims, store.lastID = claims, id
	return store.device, store.err
}
func (store *fakeRegistryStore) ListDeviceBindings(_ context.Context, claims registryauth.GrantClaims, id string, page PageRequest) (PageResult[DeviceBinding], error) {
	store.lastClaims, store.lastID, store.lastPage = claims, id, page
	return store.bindings, store.err
}
func (store *fakeRegistryStore) ListSiteMeterBindings(_ context.Context, claims registryauth.GrantClaims, siteID string) (SiteMeterBindings, error) {
	store.lastClaims, store.lastID = claims, siteID
	return store.siteBindings, store.err
}
func (store *fakeRegistryStore) GetSiteTotalElectricitySources(_ context.Context, claims registryauth.GrantClaims, siteID string) (SiteTotalElectricitySources, error) {
	store.lastClaims, store.lastID = claims, siteID
	return store.totalSources, store.err
}
func (store *fakeRegistryStore) ListSpaceChildren(_ context.Context, claims registryauth.GrantClaims, siteID, parentSpaceID string, page PageRequest) (PageResult[Space], error) {
	store.lastClaims, store.lastID, store.lastPage = claims, siteID+"|"+parentSpaceID, page
	return store.spaceChildren, store.err
}
func (store *fakeRegistryStore) ListDevicePoints(_ context.Context, claims registryauth.GrantClaims, deviceID string, page PageRequest) (PageResult[TelemetryPoint], error) {
	store.lastClaims, store.lastID, store.lastPage = claims, deviceID, page
	return store.devicePoints, store.err
}
func (store *fakeRegistryStore) GetSiteAssetModel(_ context.Context, claims registryauth.GrantClaims, id string) (SiteAssetModel, error) {
	store.lastClaims, store.lastID = claims, id
	return store.assetModel, store.err
}

func (store *fakeRegistryStore) GetSiteTariff(_ context.Context, claims registryauth.GrantClaims, id string) (SiteTariff, error) {
	store.lastClaims, store.lastID = claims, id
	return store.tariff, store.err
}

func (store *fakeRegistryStore) ListImportTariffs(_ context.Context, claims registryauth.GrantClaims, id string, _ time.Time) (ImportTariffs, error) {
	store.lastClaims, store.lastID = claims, id
	return ImportTariffs{}, store.err
}

func (store *fakeRegistryStore) GetOperatingSchedule(_ context.Context, claims registryauth.GrantClaims, id string) (OperatingSchedule, error) {
	store.lastClaims, store.lastID = claims, id
	return OperatingSchedule{}, store.err
}

func (store *fakeRegistryStore) ListOperatingExceptions(_ context.Context, claims registryauth.GrantClaims, id string, _ OperatingExceptionFilter) (OperatingExceptionList, error) {
	store.lastClaims, store.lastID = claims, id
	return OperatingExceptionList{}, store.err
}

func (store *fakeRegistryStore) ListMonthlyElectricityBudgets(_ context.Context, claims registryauth.GrantClaims, id string, _ MonthlyElectricityBudgetFilter) (MonthlyElectricityBudgetList, error) {
	store.lastClaims, store.lastID = claims, id
	return MonthlyElectricityBudgetList{}, store.err
}

func (store *fakeRegistryStore) ListUtilityBills(_ context.Context, claims registryauth.GrantClaims, id string, _ UtilityBillFilter) (UtilityBillList, error) {
	store.lastClaims, store.lastID = claims, id
	return UtilityBillList{}, store.err
}

func (store *fakeRegistryStore) GetSiteControlState(_ context.Context, claims registryauth.GrantClaims, id string) (SiteControlState, error) {
	store.lastClaims, store.lastID = claims, id
	return SiteControlState{SiteID: id, ControlMode: SiteControlModeRemoteAuto, AssetLocks: []AssetControlLockState{}}, store.err
}

func (store *fakeRegistryStore) GetSiteAutoControlLease(_ context.Context, claims registryauth.GrantClaims, id string) (SiteAutoControlLeaseState, error) {
	store.lastClaims, store.lastID = claims, id
	return SiteAutoControlLeaseState{TenantID: claims.TenantID, SiteID: id, ConfirmationMode: ConfirmationModeManual}, store.err
}

func (store *fakeRegistryStore) ListOptimizationExecutionMonitors(_ context.Context, claims registryauth.GrantClaims, id string) (OptimizationExecutionMonitorList, error) {
	store.lastClaims, store.lastID = claims, id
	return OptimizationExecutionMonitorList{TenantID: claims.TenantID, SiteID: id, Items: []OptimizationExecutionMonitor{}}, store.err
}

func TestServerReadsSiteTotalElectricitySources(t *testing.T) {
	now := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	store := &fakeRegistryStore{totalSources: SiteTotalElectricitySources{
		SiteID:      testSiteA1,
		Synthesized: true,
		SubMeters: []SiteTotalSubMeter{
			{PointID: "018f1e00-6000-7000-8000-000000000001", DeviceID: testDeviceA1, MeterName: "3号电表（A机组）", Ordinal: 0},
		},
	}}
	harness := newCoreHarness(t, now, store, registryauth.StaticGrantStatusProvider{PolicyRevision: testPolicy})
	response := harness.serve(t, http.MethodGet, RegistryPathPrefix+"sites/"+testSiteA1+"/total-electricity-sources", testGrantClaims(registryauth.ActionMeterBindingResolve), nil)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
	}
	var sources SiteTotalElectricitySources
	if err := json.NewDecoder(response.Body).Decode(&sources); err != nil {
		t.Fatal(err)
	}
	if store.lastID != testSiteA1 || !sources.Synthesized || len(sources.SubMeters) != 1 || sources.SubMeters[0].MeterName != "3号电表（A机组）" {
		t.Fatalf("sources=%#v lastID=%q", sources, store.lastID)
	}
}

func TestServerReadsTheSitesTariffInEffect(t *testing.T) {
	now := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	store := &fakeRegistryStore{tariff: SiteTariff{
		SiteID: testSiteA1, DisplayName: "两部制分时电价", Version: 1, EffectiveFrom: formatInstant(now), Timezone: "Asia/Shanghai", Currency: "CNY",
		Periods: []TariffPeriod{{PeriodCode: "PEAK", DayType: "WEEKDAY", StartMinute: 480, EndMinute: 660, EnergyRatePerKWh: 1.2, DemandRatePerKW: 38}},
	}}
	harness := newCoreHarness(t, now, store, registryauth.StaticGrantStatusProvider{PolicyRevision: testPolicy})
	response := harness.serve(t, http.MethodGet, RegistryPathPrefix+"sites/"+testSiteA1+"/tariff", testGrantClaims(registryauth.ActionTariffRead), nil)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
	}
	var tariff SiteTariff
	if err := json.NewDecoder(response.Body).Decode(&tariff); err != nil {
		t.Fatal(err)
	}
	if store.lastID != testSiteA1 || len(tariff.Periods) != 1 || tariff.Periods[0].DemandRatePerKW != 38 || tariff.Currency != "CNY" {
		t.Fatalf("tariff=%#v lastID=%q", tariff, store.lastID)
	}
}

func TestServerListsSitesAndReturnsBoundCursor(t *testing.T) {
	now := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	store := &fakeRegistryStore{sites: PageResult[Site]{
		Items:   []Site{{ID: testSiteA1, TenantID: testTenantA, DisplayName: "Site A1"}},
		HasMore: true,
	}}
	harness := newCoreHarness(t, now, store, registryauth.StaticGrantStatusProvider{PolicyRevision: testPolicy})
	claims := testGrantClaims(registryauth.ActionSiteList)
	response := harness.serve(t, http.MethodGet, RegistryPathPrefix+"sites?limit=1", claims, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
	}
	var collection Collection[Site]
	if err := json.NewDecoder(response.Body).Decode(&collection); err != nil {
		t.Fatal(err)
	}
	if len(collection.Items) != 1 || collection.NextCursor == nil || !collection.HasMore {
		t.Fatalf("collection = %#v", collection)
	}
	if store.lastPage.Limit != 1 || store.lastClaims.TokenID != claims.TokenID {
		t.Fatalf("store call page=%#v claims=%#v", store.lastPage, store.lastClaims)
	}
	page, err := harness.codec.Decode(*collection.NextCursor, "sites", "", registryauth.ActionSiteList, claims)
	if err != nil || page.ID != testSiteA1 {
		t.Fatalf("decode cursor: page=%#v err=%v", page, err)
	}
}

func TestServerListsSiteMeterBindingsWithinPrivateCoreRoute(t *testing.T) {
	now := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	store := &fakeRegistryStore{siteBindings: SiteMeterBindings{
		SiteID: testSiteA1,
		Items: []MeterBindingResolution{{
			TenantID: testTenantA, SiteID: testSiteA1, MeterID: testBindingA1,
			MeterBindingID: testBindingA1, EnergyType: "electricity", MeterRole: "PRIMARY", PointType: "COUNTER",
			DeviceID: testDeviceA1, PointID: testBindingA1, EffectiveFrom: now,
		}},
	}}
	harness := newCoreHarness(t, now, store, registryauth.StaticGrantStatusProvider{PolicyRevision: testPolicy})
	path := RegistryPathPrefix + "sites/" + testSiteA1 + "/meter-bindings"
	response := harness.serve(t, http.MethodGet, path, testGrantClaims(registryauth.ActionMeterBindingResolve), nil)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
	}
	var result SiteMeterBindings
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.SiteID != testSiteA1 || len(result.Items) != 1 || result.Items[0].MeterBindingID != testBindingA1 || store.lastID != testSiteA1 {
		t.Fatalf("result=%#v lastID=%q", result, store.lastID)
	}
	rejectedQuery := harness.serve(t, http.MethodGet, path+"?deviceId="+url.QueryEscape(testDeviceA1), testGrantClaims(registryauth.ActionMeterBindingResolve), nil)
	if rejectedQuery.Code != http.StatusBadRequest {
		t.Fatalf("unexpected query status = %d; body=%s", rejectedQuery.Code, rejectedQuery.Body.String())
	}
}

func TestServerSerializesMeterBindingReadingUnitAndRatioVersions(t *testing.T) {
	now := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	effectiveFrom := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	effectiveTo := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	store := &fakeRegistryStore{siteBindings: SiteMeterBindings{
		SiteID: testSiteA1,
		Items: []MeterBindingResolution{
			{
				TenantID:          testTenantA,
				SiteID:            testSiteA1,
				MeterID:           testBindingA1,
				MeterBindingID:    testBindingA1,
				TopologyVersionID: "01990000-1300-7000-8000-000000000001",
				BindingVersion:    2,
				BindingRevision:   3,
				EnergyTypeID:      "01990000-0000-7000-8000-000000000001",
				EnergyType:        "electricity",
				MeterRole:         "PRIMARY",
				Direction:         "IMPORT",
				DeviceID:          testDeviceA1,
				PointID:           "01990000-1340-7000-8000-000000000001",
				PointType:         "COUNTER",
				ReadingUnit:       "MWh",
				RatioVersions: []MeterRatioVersionSummary{{
					ID:                   "01990000-1350-7000-8000-000000000001",
					TenantID:             testTenantA,
					SiteID:               testSiteA1,
					MeterBindingID:       testBindingA1,
					PointID:              "01990000-1340-7000-8000-000000000001",
					CTRatio:              20,
					PTRatio:              1.5,
					MeterMultiplier:      2,
					RatioApplicationMode: "PLATFORM_APPLIED",
					ReadingUnit:          "kWh",
					EffectiveFrom:        effectiveFrom,
					EffectiveTo:          &effectiveTo,
					Status:               "RELEASED",
					Revision:             1,
				}},
				EffectiveFrom: effectiveFrom,
			},
		},
	}}
	harness := newCoreHarness(t, now, store, registryauth.StaticGrantStatusProvider{PolicyRevision: testPolicy})
	response := harness.serve(t, http.MethodGet, RegistryPathPrefix+"sites/"+testSiteA1+"/meter-bindings", testGrantClaims(registryauth.ActionMeterBindingResolve), nil)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
	}
	var decoded struct {
		SiteID string `json:"siteId"`
		Items  []struct {
			ReadingUnit   string                     `json:"readingUnit"`
			RatioVersions []MeterRatioVersionSummary `json:"ratioVersions"`
		} `json:"items"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Items) != 1 || decoded.Items[0].ReadingUnit != "MWh" || len(decoded.Items[0].RatioVersions) != 1 {
		t.Fatalf("unexpected decoded meter bindings: %s", response.Body.String())
	}
	gotRatio := decoded.Items[0].RatioVersions[0]
	if gotRatio.CTRatio != 20 || gotRatio.PTRatio != 1.5 || gotRatio.MeterMultiplier != 2 ||
		gotRatio.RatioApplicationMode != "PLATFORM_APPLIED" || gotRatio.ReadingUnit != "kWh" ||
		gotRatio.Status != "RELEASED" || gotRatio.Revision != 1 ||
		!gotRatio.EffectiveFrom.Equal(effectiveFrom) || gotRatio.EffectiveTo == nil || !gotRatio.EffectiveTo.Equal(effectiveTo) {
		t.Fatalf("unexpected decoded ratio version: %#v", gotRatio)
	}
}

func TestAttachMeterRatioVersionsMatchesByPointOrBindingFallbackAndNormalizesUTC(t *testing.T) {
	offsetPlus8 := time.FixedZone("CST", 8*3600)
	t1 := time.Date(2026, 1, 1, 8, 0, 0, 0, offsetPlus8)
	t2 := time.Date(2026, 2, 1, 8, 0, 0, 0, offsetPlus8)
	t3 := time.Date(2026, 3, 1, 8, 0, 0, 0, offsetPlus8)

	items := []MeterBindingResolution{
		{MeterBindingID: "binding-1", PointID: "point-1"},
		{MeterBindingID: "binding-2", PointID: "point-2"},
		{MeterBindingID: "binding-3", PointID: "point-3"},
	}
	ratios := []MeterRatioVersionSummary{
		// Deliberately out of order & non-UTC to verify sorting and UTC normalization.
		{ID: "rv-p1-rev2", PointID: "point-1", MeterBindingID: "binding-1", CTRatio: 40, PTRatio: 1, MeterMultiplier: 1, ReadingUnit: "kWh", EffectiveFrom: t2, EffectiveTo: &t3, Status: "RELEASED", Revision: 2},
		{ID: "rv-p1-rev1", PointID: "point-1", MeterBindingID: "binding-1", CTRatio: 20, PTRatio: 1, MeterMultiplier: 1, ReadingUnit: "kWh", EffectiveFrom: t1, EffectiveTo: &t2, Status: "RELEASED", Revision: 1},
		// Binding fallback when point_id is empty.
		{ID: "rv-b2-fallback", MeterBindingID: "binding-2", CTRatio: 10, PTRatio: 1, MeterMultiplier: 1, ReadingUnit: "Wh", EffectiveFrom: t1, Status: "RELEASED", Revision: 1},
		// Unmatched point/binding must not attach anywhere.
		{ID: "rv-other", PointID: "point-999", MeterBindingID: "binding-999", CTRatio: 99, PTRatio: 1, MeterMultiplier: 1, ReadingUnit: "kWh", EffectiveFrom: t1, Status: "RELEASED", Revision: 1},
	}

	attachMeterRatioVersions(items, ratios)

	if len(items[0].RatioVersions) != 2 {
		t.Fatalf("items[0] ratios=%#v, want 2", items[0].RatioVersions)
	}
	if items[0].RatioVersions[0].ID != "rv-p1-rev1" || items[0].RatioVersions[1].ID != "rv-p1-rev2" {
		t.Fatalf("items[0] ratios out of order: %#v", items[0].RatioVersions)
	}
	if items[0].RatioVersions[0].EffectiveFrom.Location() != time.UTC ||
		items[0].RatioVersions[0].EffectiveTo == nil ||
		items[0].RatioVersions[0].EffectiveTo.Location() != time.UTC ||
		!items[0].RatioVersions[0].EffectiveFrom.Equal(t1.UTC()) {
		t.Fatalf("items[0] times not normalized to UTC: %#v", items[0].RatioVersions[0])
	}
	if len(items[1].RatioVersions) != 1 || items[1].RatioVersions[0].ID != "rv-b2-fallback" {
		t.Fatalf("items[1] binding fallback failed: %#v", items[1].RatioVersions)
	}
	if items[2].RatioVersions != nil {
		t.Fatalf("items[2] should have nil RatioVersions, got %#v", items[2].RatioVersions)
	}
	if !reflect.DeepEqual([]string{items[0].RatioVersions[0].ID, items[0].RatioVersions[1].ID}, []string{"rv-p1-rev1", "rv-p1-rev2"}) {
		t.Fatalf("unexpected ratio IDs")
	}
}

func TestServerRejectsInternalDirtyWindowWhenWriterNotConfigured(t *testing.T) {
	now := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	harness := newCoreHarness(t, now, &fakeRegistryStore{}, registryauth.StaticGrantStatusProvider{PolicyRevision: testPolicy})

	claims := testGrantClaims(registryauth.ActionMeterBindingResolve)
	claims.IssuedAt = now.Unix()
	claims.ExpiresAt = now.Add(20 * time.Second).Unix()
	claims.Presenter = testPresenter
	grant, err := registryauth.SignGrant(harness.signer, claims)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, RegistryPathPrefix+"sites/"+testSiteA1+"/accounting/dirty-windows", strings.NewReader(`{"granularity":"HOUR","windowStart":"2026-07-21T00:00:00Z","windowEnd":"2026-07-21T01:00:00Z","reason":"backfill","source":"BACKFILL"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Delegation-Grant", grant)
	peerURI, _ := url.Parse(testPresenter)
	peer := &x509.Certificate{URIs: []*url.URL{peerURI}}
	req.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{peer}, VerifiedChains: [][]*x509.Certificate{{peer}}}

	rec := httptest.NewRecorder()
	harness.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"code":"REGISTRY_WRITER_UNAVAILABLE"`) {
		t.Fatalf("status=%d, body=%s", rec.Code, rec.Body.String())
	}

	// Writer that satisfies RegistryWriter without InternalDirtyWindowWriter also returns 503 cleanly.
	backend := newMemoryScheduleBackend()
	client := newScheduleHTTPClient(t, backend)
	resp := client.do(t, http.MethodPost, RegistryPathPrefix+"sites/"+testSiteA1+"/accounting/dirty-windows", testGrantClaims(registryauth.ActionMeterBindingResolve), "", map[string]any{
		"granularity": "HOUR",
		"windowStart": "2026-07-21T00:00:00Z",
		"windowEnd":   "2026-07-21T01:00:00Z",
		"reason":      "backfill",
		"source":      "BACKFILL",
	})
	if resp.Code != http.StatusServiceUnavailable || !strings.Contains(resp.Body.String(), `"code":"REGISTRY_WRITER_UNAVAILABLE"`) {
		t.Fatalf("non-implementing writer status=%d, body=%s", resp.Code, resp.Body.String())
	}
}

func TestServerAllowsAssetModelReadGrantOnTotalElectricitySources(t *testing.T) {
	now := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	store := &fakeRegistryStore{totalSources: SiteTotalElectricitySources{
		SiteID:      testSiteA1,
		Synthesized: true,
		SubMeters:   []SiteTotalSubMeter{{PointID: testBindingA1, DeviceID: testDeviceA1, MeterName: "D 机组电表", Ordinal: 0}},
	}}
	harness := newCoreHarness(t, now, store, registryauth.StaticGrantStatusProvider{PolicyRevision: testPolicy})
	path := RegistryPathPrefix + "sites/" + testSiteA1 + "/total-electricity-sources"
	for _, action := range []registryauth.Action{registryauth.ActionMeterBindingResolve, registryauth.ActionAssetModelRead} {
		response := harness.serve(t, http.MethodGet, path, testGrantClaims(action), nil)
		if response.Code != http.StatusOK {
			t.Fatalf("action %s: status = %d; body=%s", action, response.Code, response.Body.String())
		}
	}
	rejected := harness.serve(t, http.MethodGet, path, testGrantClaims(registryauth.ActionDeviceRead), nil)
	if rejected.Code != http.StatusForbidden {
		t.Fatalf("unexpected status for wrong action = %d; body=%s", rejected.Code, rejected.Body.String())
	}
}

func TestServerAcceptsIAMGrantBoundToOperationsAgentPresenter(t *testing.T) {
	now := time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC)
	store := &fakeRegistryStore{site: Site{ID: testSiteA1, TenantID: testTenantA}}
	harness := newCoreHarness(t, now, store, registryauth.StaticGrantStatusProvider{PolicyRevision: testPolicy})
	claims := testGrantClaims(registryauth.ActionSiteRead)
	response, _ := harness.serveAndGrantAsPresenter(
		t,
		http.MethodGet,
		RegistryPathPrefix+"sites/"+testSiteA1,
		claims,
		nil,
		testOperationsPresenter,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
	}
	if store.lastClaims.Presenter != testOperationsPresenter {
		t.Fatalf("presenter = %q", store.lastClaims.Presenter)
	}
}

func TestServerRoutesAllRegistryReadsThroughConcreteGrantActions(t *testing.T) {
	now := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	store := &fakeRegistryStore{
		sites:     PageResult[Site]{Items: []Site{}},
		site:      Site{ID: testSiteA1},
		assets:    PageResult[Asset]{Items: []Asset{}},
		assetItem: Asset{ID: testAssetA1},
		devices:   PageResult[Device]{Items: []Device{}},
		device:    Device{ID: testDeviceA1},
		bindings:  PageResult[DeviceBinding]{Items: []DeviceBinding{{ID: testBindingA1}}},
	}
	harness := newCoreHarness(t, now, store, registryauth.StaticGrantStatusProvider{PolicyRevision: testPolicy})
	tests := []struct {
		path   string
		action registryauth.Action
	}{
		{RegistryPathPrefix + "sites", registryauth.ActionSiteList},
		{RegistryPathPrefix + "sites/" + testSiteA1, registryauth.ActionSiteRead},
		{RegistryPathPrefix + "sites/" + testSiteA1 + "/assets", registryauth.ActionAssetList},
		{RegistryPathPrefix + "assets/" + testAssetA1, registryauth.ActionAssetRead},
		{RegistryPathPrefix + "sites/" + testSiteA1 + "/devices", registryauth.ActionDeviceList},
		{RegistryPathPrefix + "sites/" + testSiteA1 + "/device-bindings", registryauth.ActionDeviceBindingList},
		{RegistryPathPrefix + "devices/" + testDeviceA1, registryauth.ActionDeviceRead},
	}
	for _, test := range tests {
		t.Run(string(test.action), func(t *testing.T) {
			response := harness.serve(t, http.MethodGet, test.path, testGrantClaims(test.action), nil)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestServerFailsClosedForWrongActionRevocationStalePolicyAndForgedHeaders(t *testing.T) {
	now := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	store := &fakeRegistryStore{sites: PageResult[Site]{Items: []Site{}}}
	tests := []struct {
		name       string
		claims     registryauth.GrantClaims
		status     registryauth.GrantStatusProvider
		headers    http.Header
		wantStatus int
	}{
		{"wrong action", testGrantClaims(registryauth.ActionDeviceRead), registryauth.StaticGrantStatusProvider{PolicyRevision: testPolicy}, nil, http.StatusForbidden},
		{"revoked", testGrantClaims(registryauth.ActionSiteList), registryauth.StaticGrantStatusProvider{PolicyRevision: testPolicy, RevokedTokenIDs: map[string]struct{}{"grant-1": {}}}, nil, http.StatusForbidden},
		{"stale policy", testGrantClaims(registryauth.ActionSiteList), registryauth.StaticGrantStatusProvider{PolicyRevision: "registry-read:2"}, nil, http.StatusForbidden},
		{"status unavailable", testGrantClaims(registryauth.ActionSiteList), registryauth.StaticGrantStatusProvider{Err: errors.New("offline")}, nil, http.StatusServiceUnavailable},
		{"forged scope", testGrantClaims(registryauth.ActionSiteList), registryauth.StaticGrantStatusProvider{PolicyRevision: testPolicy}, http.Header{"X-Site-ID": []string{testSiteA1}}, http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			harness := newCoreHarness(t, now, store, test.status)
			response := harness.serve(t, http.MethodGet, RegistryPathPrefix+"sites", test.claims, test.headers)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, test.wantStatus, response.Body.String())
			}
		})
	}
}

func TestServerRejectsInvalidGrantBeforeOnlineStatusLookup(t *testing.T) {
	now := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	status := &countingGrantStatusProvider{status: registryauth.GrantStatus{CurrentPolicyRevision: testPolicy}}
	harness := newCoreHarness(t, now, &fakeRegistryStore{}, status)
	response := harness.serve(t, http.MethodGet, RegistryPathPrefix+"sites", testGrantClaims(registryauth.Action("organization.read")), nil)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
	}
	if status.calls != 0 {
		t.Fatalf("online status calls = %d", status.calls)
	}
}

func TestServerLogsExcludeGrantAndRequestCredentialMaterial(t *testing.T) {
	now := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	store := &fakeRegistryStore{sites: PageResult[Site]{Items: []Site{}}}
	harness := newCoreHarnessWithLogger(t, now, store, registryauth.StaticGrantStatusProvider{PolicyRevision: testPolicy}, logger)
	claims := testGrantClaims(registryauth.ActionSiteList)
	response, grant := harness.serveAndGrant(t, http.MethodGet, RegistryPathPrefix+"sites", claims, http.Header{
		"Cookie":        []string{"session=opaque-cookie-value"},
		"Authorization": []string{"Bearer opaque-auth-value"},
	})
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
	}
	output := logs.String()
	for _, forbidden := range []string{grant, "opaque-cookie-value", "opaque-auth-value", claims.Subject, claims.SessionID, claims.TokenID} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("Core log contains request credential material %q: %s", forbidden, output)
		}
	}
}

func TestServerReturnsSameNotFoundForUnauthorizedAndMissingDetail(t *testing.T) {
	now := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	store := &fakeRegistryStore{err: ErrNotFound}
	harness := newCoreHarness(t, now, store, registryauth.StaticGrantStatusProvider{PolicyRevision: testPolicy})
	response := harness.serve(t, http.MethodGet, RegistryPathPrefix+"sites/"+testSiteA2, testGrantClaims(registryauth.ActionSiteRead), nil)
	if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), `"code":"RESOURCE_NOT_FOUND"`) {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
	}
}

type coreHarness struct {
	handler http.Handler
	signer  crypto.Signer
	peer    *x509.Certificate
	codec   *CursorCodec
	now     time.Time
}

func newCoreHarness(t *testing.T, now time.Time, store RegistryStore, status registryauth.GrantStatusProvider) coreHarness {
	return newCoreHarnessWithLogger(t, now, store, status, nil)
}

func newCoreHarnessWithLogger(t *testing.T, now time.Time, store RegistryStore, status registryauth.GrantStatusProvider, logger *slog.Logger) coreHarness {
	t.Helper()
	signer, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	uri, err := url.Parse(testPresenter)
	if err != nil {
		t.Fatal(err)
	}
	peer := &x509.Certificate{URIs: []*url.URL{uri}, PublicKey: &signer.PublicKey}
	codec, err := NewCursorCodec([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	return coreHarness{
		handler: NewHandler(ServerConfig{
			Store:                             store,
			CursorCodec:                       codec,
			GrantPublicKey:                    &signer.PublicKey,
			GrantIssuer:                       testGrantIssuer,
			AllowedPresenterSPIFFE:            testPresenter,
			AdditionalAllowedPresenterSPIFFEs: []string{testOperationsPresenter},
			Audience:                          testAudience,
			GrantStatus:                       status,
			Logger:                            logger,
			Now:                               func() time.Time { return now },
		}),
		signer: signer,
		peer:   peer,
		codec:  codec,
		now:    now,
	}
}

func (harness coreHarness) serve(t *testing.T, method, path string, claims registryauth.GrantClaims, headers http.Header) *httptest.ResponseRecorder {
	recorder, _ := harness.serveAndGrant(t, method, path, claims, headers)
	return recorder
}

func (harness coreHarness) serveAndGrant(t *testing.T, method, path string, claims registryauth.GrantClaims, headers http.Header) (*httptest.ResponseRecorder, string) {
	t.Helper()
	return harness.serveAndGrantAsPresenter(t, method, path, claims, headers, testPresenter)
}

func (harness coreHarness) serveAndGrantAsPresenter(
	t *testing.T,
	method,
	path string,
	claims registryauth.GrantClaims,
	headers http.Header,
	presenterSPIFFE string,
) (*httptest.ResponseRecorder, string) {
	t.Helper()
	claims.IssuedAt = harness.now.Unix()
	claims.ExpiresAt = harness.now.Add(20 * time.Second).Unix()
	claims.Presenter = presenterSPIFFE
	grant, err := registryauth.SignGrant(harness.signer, claims)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(method, path, nil)
	request.Header.Set("X-Delegation-Grant", grant)
	for name, values := range headers {
		request.Header[name] = append([]string(nil), values...)
	}
	peerURI, err := url.Parse(presenterSPIFFE)
	if err != nil {
		t.Fatal(err)
	}
	peer := &x509.Certificate{URIs: []*url.URL{peerURI}}
	request.TLS = &tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{peer},
		VerifiedChains:   [][]*x509.Certificate{{peer}},
	}
	recorder := httptest.NewRecorder()
	harness.handler.ServeHTTP(recorder, request)
	return recorder, grant
}

func testGrantClaims(action registryauth.Action) registryauth.GrantClaims {
	return registryauth.GrantClaims{
		Issuer:         testGrantIssuer,
		Presenter:      testPresenter,
		Audience:       testAudience,
		PrincipalID:    testPrincipal,
		SubjectIssuer:  "https://identity.example.test/oidc",
		Subject:        "delegated",
		TenantID:       testTenantA,
		AllowedSiteIDs: []string{testSiteA1},
		DeniedSiteIDs:  []string{},
		Actions:        []registryauth.Action{action},
		PolicyRevision: testPolicy,
		DecisionReason: registryauth.ReasonAllowSiteRole,
		SessionID:      "session-1",
		ParentTokenID:  "parent-1",
		TokenID:        "grant-1",
		Transitive:     false,
	}
}

func TestServerRejectsAccountingConfigRoutesWhenStoreOrWriterNotConfigured(t *testing.T) {
	now := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	harness := newCoreHarness(t, now, &fakeRegistryStore{}, registryauth.StaticGrantStatusProvider{PolicyRevision: testPolicy})

	readRoutes := []string{
		RegistryPathPrefix + "sites/" + testSiteA1 + "/accounting/holidays",
		RegistryPathPrefix + "sites/" + testSiteA1 + "/accounting/meter-ratios",
	}
	for _, path := range readRoutes {
		rec := harness.serve(t, http.MethodGet, path, testGrantClaims(registryauth.ActionSiteRead), nil)
		if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"code":"REGISTRY_UNAVAILABLE"`) {
			t.Fatalf("GET %s without AccountingConfigStore status=%d, body=%s", path, rec.Code, rec.Body.String())
		}
	}

	// Nil writer and non-implementing writer both return 503 REGISTRY_WRITER_UNAVAILABLE.
	backend := newMemoryScheduleBackend()
	client := newScheduleHTTPClient(t, backend)
	for _, tc := range []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodPut, RegistryPathPrefix + "sites/" + testSiteA1 + "/accounting/holidays/2026-10-01", map[string]any{"dayType": "HOLIDAY"}},
		{http.MethodDelete, RegistryPathPrefix + "sites/" + testSiteA1 + "/accounting/holidays/2026-10-01", nil},
		{http.MethodPost, RegistryPathPrefix + "sites/" + testSiteA1 + "/accounting/meter-ratios", map[string]any{"pointId": testBindingA1, "ctRatio": 10, "ptRatio": 1, "ratioApplicationMode": "PLATFORM_APPLIED", "effectiveFrom": "2026-08-01T00:00:00Z"}},
		{http.MethodPost, RegistryPathPrefix + "sites/" + testSiteA1 + "/accounting/meter-ratios/" + testBindingA1 + "/retire", map[string]any{"effectiveTo": "2026-09-01T00:00:00Z"}},
	} {
		recNoWriter := harness.serve(t, tc.method, tc.path, testGrantClaims(registryauth.ActionBillingManage), nil)
		if recNoWriter.Code != http.StatusServiceUnavailable || !strings.Contains(recNoWriter.Body.String(), `"code":"REGISTRY_WRITER_UNAVAILABLE"`) {
			t.Fatalf("%s %s with nil writer status=%d, body=%s", tc.method, tc.path, recNoWriter.Code, recNoWriter.Body.String())
		}
		respNonImpl := client.do(t, tc.method, tc.path, testGrantClaims(registryauth.ActionBillingManage), "", tc.body)
		if respNonImpl.Code != http.StatusServiceUnavailable || !strings.Contains(respNonImpl.Body.String(), `"code":"REGISTRY_WRITER_UNAVAILABLE"`) {
			t.Fatalf("%s %s with non-implementing writer status=%d, body=%s", tc.method, tc.path, respNonImpl.Code, respNonImpl.Body.String())
		}
	}
}

func TestParseRegistryRouteAccountingConfigRoutes(t *testing.T) {
	tests := []struct {
		method     string
		path       string
		wantOK     bool
		wantAction registryauth.Action
		wantWrite  bool
	}{
		{http.MethodGet, RegistryPathPrefix + "sites/" + testSiteA1 + "/accounting/holidays", true, registryauth.ActionSiteRead, false},
		{http.MethodPut, RegistryPathPrefix + "sites/" + testSiteA1 + "/accounting/holidays/2026-10-01", true, registryauth.ActionBillingManage, true},
		{http.MethodDelete, RegistryPathPrefix + "sites/" + testSiteA1 + "/accounting/holidays/2026-10-01", true, registryauth.ActionBillingManage, true},
		{http.MethodGet, RegistryPathPrefix + "sites/" + testSiteA1 + "/accounting/meter-ratios", true, registryauth.ActionSiteRead, false},
		{http.MethodPost, RegistryPathPrefix + "sites/" + testSiteA1 + "/accounting/meter-ratios", true, registryauth.ActionBillingManage, true},
		{http.MethodPost, RegistryPathPrefix + "sites/" + testSiteA1 + "/accounting/meter-ratios/" + testBindingA1 + "/retire", true, registryauth.ActionBillingManage, true},
		{http.MethodPost, RegistryPathPrefix + "sites/" + testSiteA1 + "/accounting/holidays/2026-10-01", false, "", false},
	}
	for _, tc := range tests {
		route, ok := parseRegistryRoute(tc.method, tc.path)
		if ok != tc.wantOK {
			t.Fatalf("%s %s ok=%v, want %v", tc.method, tc.path, ok, tc.wantOK)
		}
		if ok && (route.action != tc.wantAction || route.write != tc.wantWrite) {
			t.Fatalf("%s %s route=%#v, want action=%s write=%v", tc.method, tc.path, route, tc.wantAction, tc.wantWrite)
		}
	}
}
