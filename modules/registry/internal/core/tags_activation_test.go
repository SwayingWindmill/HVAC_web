package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

func TestValidateTagsAndFilters(t *testing.T) {
	if err := ValidateTags(map[string]string{
		"zone":             "chiller-plant",
		"vendor.model/v1":  "ATV630-55kW",
		"criticality_tier": "P0",
	}); err != nil {
		t.Fatalf("expected valid tags, got %v", err)
	}

	maxTags := make(map[string]string, 64)
	for i := range 64 {
		key := "k" + string(rune('a'+i/26)) + string(rune('a'+i%26))
		maxTags[key] = "ok"
	}
	if err := ValidateTags(maxTags); err != nil {
		t.Fatalf("64 tags should pass, got %v", err)
	}
	maxTags["overflow"] = "too-many"
	if !errors.Is(ValidateTags(maxTags), ErrInvalidMutation) {
		t.Fatalf("expected ErrInvalidMutation for >64 tags")
	}

	for _, invalid := range []map[string]string{
		{"UpperCase": "v"},
		{"_leading": "v"},
		{strings.Repeat("a", 65): "v"},
		{"ok": ""},
		{"ok": " leading"},
		{"ok": "trailing "},
		{"ok": "ctrl\nchar"},
		{"ok": strings.Repeat("x", 257)},
	} {
		if err := ValidateTags(invalid); !errors.Is(err, ErrInvalidMutation) {
			t.Fatalf("expected ErrInvalidMutation for %v, got %v", invalid, err)
		}
	}

	oversized := map[string]string{}
	for i := range 40 {
		key := "tag-" + string(rune('a'+i/26)) + string(rune('a'+i%26))
		oversized[key] = strings.Repeat("v", 240)
	}
	if err := ValidateTags(oversized); !errors.Is(err, ErrInvalidMutation) {
		t.Fatalf("expected ErrInvalidMutation for >8192B serialized payload, got %v", err)
	}

	filterA := AssetFilter{
		Status:             "ACTIVE",
		AssetType:          "CHILLER",
		SpaceID:            testAssetA1,
		TemplateKey:        "chiller.centrifugal",
		TemplateRevisionID: testDeviceA1,
		Tags:               map[string]string{"b": "2", "a": "1"},
		TagKeys:            []string{"z", "m", "z"},
	}
	filterB := AssetFilter{
		Status:             "ACTIVE",
		AssetType:          "CHILLER",
		SpaceID:            testAssetA1,
		TemplateKey:        "chiller.centrifugal",
		TemplateRevisionID: testDeviceA1,
		Tags:               map[string]string{"a": "1", "b": "2"},
		TagKeys:            []string{"m", "z"},
	}
	if err := filterA.Validate(); err != nil {
		t.Fatalf("filterA should be valid, got %v", err)
	}
	if filterA.CanonicalKey() != filterB.CanonicalKey() {
		t.Fatalf("CanonicalKey mismatch: %q vs %q", filterA.CanonicalKey(), filterB.CanonicalKey())
	}

	for _, bad := range []DeviceFilter{
		{Status: "UNKNOWN"},
		{DeviceType: "  "},
		{SpaceID: "not-a-uuid"},
		{TemplateRevisionID: "not-a-uuid"},
		{TemplateKey: "Bad Key!"},
		{Tags: map[string]string{"Bad": "1"}},
		{TagKeys: []string{"_bad"}},
	} {
		if !errors.Is(bad.Validate(), ErrInvalidPage) {
			t.Fatalf("expected ErrInvalidPage for %+v", bad)
		}
	}

	k1 := DeviceFilter{Tags: map[string]string{"a": "1,b:2"}}.asResourceFilter().canonicalKey()
	k2 := DeviceFilter{Tags: map[string]string{"a": "1", "b": "2"}}.asResourceFilter().canonicalKey()
	if k1 == k2 {
		t.Fatalf("canonicalKey delimiter collision between single packed value and distinct tags: %q", k1)
	}
}

func TestBuildFilteredEntityListSQL(t *testing.T) {
	claims := testGrantClaims(registryauth.ActionAssetList)
	claims.DeniedSiteIDs = []string{testSiteA2}

	assetSQL, assetArgs, err := buildFilteredEntityListSQL(
		TemplateAsset,
		assetColumns,
		claims,
		"",
		AssetFilter{
			Status:             "ACTIVE",
			AssetType:          "CHILLER",
			SpaceID:            "018f1e00-2000-7000-8000-000000000001",
			TemplateKey:        "tpl.chiller",
			TemplateRevisionID: "018f1e00-7000-7000-8000-000000000001",
			Tags:               map[string]string{"plant": "north"},
			TagKeys:            []string{"critical"},
		}.asResourceFilter(),
		PageRequest{Limit: 25},
		25,
	)
	if err != nil {
		t.Fatalf("buildFilteredEntityListSQL(asset): %v", err)
	}
	for _, needle := range []string{
		"WITH RECURSIVE space_subtree AS",
		"FROM core_registry.assets AS e",
		"FROM core_registry.asset_space_bindings AS asb",
		"FROM core_registry.registry_template_assignments AS ta",
		"e.tags @>",
		"e.tags ?",
	} {
		if !strings.Contains(assetSQL, needle) {
			t.Fatalf("asset SQL missing %q:\n%s", needle, assetSQL)
		}
	}
	if len(assetArgs) < 9 {
		t.Fatalf("unexpected asset arg count %d", len(assetArgs))
	}

	deviceSQL, _, err := buildFilteredEntityListSQL(
		TemplateDevice,
		deviceColumns,
		claims,
		testSiteA1,
		DeviceFilter{
			DeviceType: "VFD",
			SpaceID:    "018f1e00-2000-7000-8000-000000000001",
			Tags:       map[string]string{"vendor": "schneider"},
		}.asResourceFilter(),
		PageRequest{Limit: 10},
		10,
	)
	if err != nil {
		t.Fatalf("buildFilteredEntityListSQL(device): %v", err)
	}
	for _, needle := range []string{
		"FROM core_registry.devices AS e",
		"e.site_id =",
		"FROM core_registry.device_space_bindings AS dsb",
		"FROM core_registry.device_bindings AS db",
		"e.tags @>",
	} {
		if !strings.Contains(deviceSQL, needle) {
			t.Fatalf("device SQL missing %q:\n%s", needle, deviceSQL)
		}
	}
}

func TestTemplatePayloadActivationValidationAndExtraction(t *testing.T) {
	legacyATV630 := map[string]any{
		"deviceFamily": "ATV630",
		"parameters": []any{
			map[string]any{"code": "LFRD", "address": 8502},
		},
	}
	act, err := parseAndValidateTemplatePayload(TemplateDevice, legacyATV630)
	if err != nil || act != nil {
		t.Fatalf("legacy ATV630 payload should remain valid without activation metadata, got (%+v, %v)", act, err)
	}

	fullPayload := map[string]any{
		"deviceType": "CHILLER",
		"standardPoints": []any{
			map[string]any{
				"pointCode":         "chw_supply_temp",
				"displayName":       "冷冻水供水温度",
				"pointType":         "TELEMETRY",
				"valueType":         "NUMBER",
				"unit":              "degC",
				"sampleIntervalMs":  5000,
				"publishIntervalMs": 10000,
				"staleAfterMs":      60000,
				"minimumNumber":     -5.0,
				"maximumNumber":     35.0,
			},
			map[string]any{
				"pointCode":              "energy_kwh",
				"sourceKey":              "custom.energy_kwh",
				"displayName":            "累计电量",
				"pointType":              "COUNTER",
				"valueType":              "NUMBER",
				"unit":                   "kWh",
				"sampleIntervalMs":       10000,
				"publishIntervalMs":      10000,
				"staleAfterMs":           60000,
				"counterDecreaseMode":    "ROLLOVER",
				"counterRolloverModulus": 1000000.0,
			},
		},
		"qualityChecks": map[string]any{
			"chw_supply_temp": map[string]any{
				"maxDeltaPerMinute":     4.0,
				"unchangedAlertSeconds": 1800,
			},
		},
		"capabilityProfiles": []any{
			map[string]any{
				"capabilityName":     "SET_CHW_SETPOINT",
				"capabilityRevision": "v1",
				"status":             "VERIFIED",
				"canonicalUnit":      "degC",
				"minimumValue":       5.0,
				"maximumValue":       15.0,
				"maximumDelta":       2.0,
				"riskLevel":          "MEDIUM",
				"approvalPolicy":     "NONE",
				"retryPolicy":        "PRE_SEND_ONLY",
				"connectorKind":      "MODBUS_TCP",
			},
		},
	}
	act, err = parseAndValidateTemplatePayload(TemplateDevice, fullPayload)
	if err != nil || act == nil {
		t.Fatalf("expected valid full activation payload, got (%+v, %v)", act, err)
	}
	if len(act.StandardPoints) != 2 || len(act.CapabilityProfiles) != 1 {
		t.Fatalf("unexpected activation counts: %+v", act)
	}

	qc := act.effectiveQualityForPoint(act.StandardPoints[0])
	rawMeta, err := buildTemplatePointSourceMetadata(
		nil,
		act.StandardPoints[0],
		"tpl.chiller",
		"018f1e00-7000-7000-8000-000000000001",
		"018f1e00-7100-7000-8000-000000000001",
		qc,
	)
	if err != nil {
		t.Fatalf("buildTemplatePointSourceMetadata: %v", err)
	}
	minNum, maxNum, extractedQC := extractPointQualityFromSourceMetadata(rawMeta, "NUMBER")
	if minNum == nil || *minNum != -5.0 || maxNum == nil || *maxNum != 35.0 {
		t.Fatalf("unexpected min/max extracted: %v / %v", minNum, maxNum)
	}
	flatlineVal, hasFlatline := numericFromAny(extractedQC["flatlineSeconds"])
	if extractedQC == nil || extractedQC["maxStep"] != 4.0 || !hasFlatline || flatlineVal != 1800 {
		t.Fatalf("unexpected extracted qualityChecks: %#v", extractedQC)
	}

	nonNumMin, nonNumMax, nonNumQC := extractPointQualityFromSourceMetadata(rawMeta, "BOOLEAN")
	if nonNumMin != nil || nonNumMax != nil {
		t.Fatalf("non-NUMBER point should not emit min/max, got %v / %v", nonNumMin, nonNumMax)
	}
	nonNumFlatline, hasNonNumFlatline := numericFromAny(nonNumQC["flatlineSeconds"])
	if nonNumQC == nil || !hasNonNumFlatline || nonNumFlatline != 1800 {
		t.Fatalf("expected flatlineSeconds preserved on non-NUMBER point, got %#v", nonNumQC)
	}

	key1, err := deriveTemplatePointSourceKey("01-vfd", "freq_hz", "")
	if err != nil || key1 != "dev.01-vfd.freq_hz" {
		t.Fatalf("unexpected derived key (%q, %v)", key1, err)
	}
	key2, err := deriveTemplatePointSourceKey("chiller-1", "chw_supply_temp", "custom.key")
	if err != nil || key2 != "custom.key" {
		t.Fatalf("explicit sourceKey must be preserved, got (%q, %v)", key2, err)
	}

	for _, bad := range []struct {
		kind    TemplateKind
		payload map[string]any
	}{
		{
			kind:    TemplateAsset,
			payload: map[string]any{"deviceType": "CHILLER"},
		},
		{
			kind: TemplateAsset,
			payload: map[string]any{
				"standardPoints": []any{map[string]any{
					"pointCode": "p1", "displayName": "P1", "pointType": "TELEMETRY", "valueType": "NUMBER",
					"sampleIntervalMs": 1000, "publishIntervalMs": 1000, "staleAfterMs": 5000,
				}},
			},
		},
		{
			kind: TemplateDevice,
			payload: map[string]any{
				"standardPoints": []any{map[string]any{
					"pointCode": "p1", "displayName": "P1", "pointType": "TELEMETRY", "valueType": "NUMBER",
					"sampleIntervalMs": 10000, "publishIntervalMs": 5000, "staleAfterMs": 20000,
				}},
			},
		},
		{
			kind: TemplateDevice,
			payload: map[string]any{
				"standardPoints": []any{map[string]any{
					"pointCode": "p1", "displayName": "P1", "pointType": "TELEMETRY", "valueType": "BOOLEAN",
					"sampleIntervalMs": 1000, "publishIntervalMs": 1000, "staleAfterMs": 5000,
					"minimumNumber": 0.0,
				}},
			},
		},
		{
			kind: TemplateDevice,
			payload: map[string]any{
				"qualityChecks": map[string]any{"unchangedAlertSeconds": 10},
			},
		},
		{
			kind: TemplateDevice,
			payload: map[string]any{
				"capabilityProfiles": []any{map[string]any{
					"capabilityName": "SET_TEMP", "capabilityRevision": "v1", "status": "VERIFIED",
					"canonicalUnit": "degC", "minimumValue": 10.0, "maximumValue": 20.0, "maximumDelta": 1.0,
					"riskLevel": "LOW", "approvalPolicy": "NONE", "retryPolicy": "ALWAYS_RETRY", "connectorKind": "MODBUS_TCP",
				}},
			},
		},
	} {
		if _, err := parseAndValidateTemplatePayload(bad.kind, bad.payload); !errors.Is(err, ErrInvalidMutation) {
			t.Fatalf("expected ErrInvalidMutation for bad payload %#v, got %v", bad, err)
		}
	}
}

type fakeTagsActivationStore struct {
	*fakeRegistryStore
	lastSiteID        string
	lastAssetFilter   AssetFilter
	lastDeviceFilter  DeviceFilter
	lastTemplateKind  string
	lastRevisionID    string
	lastTargetType    string
	lastTargetID      string
	assetsPage        PageResult[Asset]
	devicesPage       PageResult[Device]
	templatesPage     PageResult[Template]
	templateRev       TemplateRevision
	assignmentsPage   PageResult[TemplateAssignment]
	returnNotFoundRev bool
}

func (f *fakeTagsActivationStore) ListAssetsFiltered(_ context.Context, _ registryauth.GrantClaims, siteID string, filter AssetFilter, _ PageRequest) (PageResult[Asset], error) {
	f.lastSiteID = siteID
	f.lastAssetFilter = filter
	return f.assetsPage, nil
}

func (f *fakeTagsActivationStore) ListDevicesFiltered(_ context.Context, _ registryauth.GrantClaims, siteID string, filter DeviceFilter, _ PageRequest) (PageResult[Device], error) {
	f.lastSiteID = siteID
	f.lastDeviceFilter = filter
	return f.devicesPage, nil
}

func (f *fakeTagsActivationStore) ListTemplates(_ context.Context, _ registryauth.GrantClaims, kind TemplateKind, _ PageRequest) (PageResult[Template], error) {
	f.lastTemplateKind = string(kind)
	return f.templatesPage, nil
}

func (f *fakeTagsActivationStore) GetTemplateRevision(_ context.Context, _ registryauth.GrantClaims, revisionID string) (TemplateRevision, error) {
	f.lastRevisionID = revisionID
	if f.returnNotFoundRev {
		return TemplateRevision{}, ErrNotFound
	}
	return f.templateRev, nil
}

func (f *fakeTagsActivationStore) ListSiteTemplateAssignments(_ context.Context, _ registryauth.GrantClaims, siteID string, targetType TemplateKind, targetID string, _ PageRequest) (PageResult[TemplateAssignment], error) {
	f.lastSiteID = siteID
	f.lastTargetType = string(targetType)
	f.lastTargetID = targetID
	return f.assignmentsPage, nil
}

func TestCoreHTTPCrossSiteSearchCursorAndTemplateRead(t *testing.T) {
	now := time.Date(2026, 11, 17, 2, 0, 0, 0, time.UTC)
	nowStr := formatInstant(now)
	revID := "018f1e00-7000-7000-8000-000000000001"
	spaceID := "018f1e00-2000-7000-8000-000000000001"
	store := &fakeTagsActivationStore{
		fakeRegistryStore: &fakeRegistryStore{},
		assetsPage: PageResult[Asset]{
			Items: []Asset{{
				ID: testAssetA1, TenantID: testTenantA, SiteID: testSiteA1, Code: "ch-1", DisplayName: "Chiller 1",
				AssetType: "CHILLER", Status: "ACTIVE", Tags: map[string]string{"plant": "north"},
				Revision: 1, CreatedAt: nowStr, UpdatedAt: nowStr,
			}},
			HasMore: true,
		},
		devicesPage: PageResult[Device]{
			Items: []Device{{
				ID: testDeviceA1, TenantID: testTenantA, SiteID: testSiteA2, Code: "vfd-1", DisplayName: "VFD 1",
				DeviceType: "VFD", Status: "ACTIVE", Tags: map[string]string{"vendor": "schneider"},
				Revision: 1, CreatedAt: nowStr, UpdatedAt: nowStr,
			}},
		},
		templatesPage: PageResult[Template]{
			Items: []Template{{
				ID: revID, TenantID: testTenantA, TemplateKind: TemplateDevice, TemplateKey: "chiller.v1",
				LatestRevisionID: revID, LatestRevisionNumber: 2, ReleasedAt: nowStr, CreatedAt: nowStr,
			}},
		},
		templateRev: TemplateRevision{
			ID: revID, TenantID: testTenantA, TemplateID: revID, TemplateKey: "chiller.v1", TemplateKind: TemplateDevice,
			RevisionNumber: 2, Status: "RELEASED", Payload: map[string]any{"deviceType": "CHILLER"},
			ReleaseReferences: map[string]string{"doc": "v2"}, ReleasedAt: nowStr,
		},
		assignmentsPage: PageResult[TemplateAssignment]{
			Items: []TemplateAssignment{{
				ID: revID, TenantID: testTenantA, SiteID: testSiteA1, TargetType: TemplateDevice, TargetID: testDeviceA1,
				TemplateRevisionID: revID, ValidFrom: nowStr, Revision: 1,
			}},
		},
	}

	harness := newCoreHarness(t, now, store, StaticGrantStatusProvider{PolicyRevision: testPolicy})

	crossClaims := testGrantClaims(registryauth.ActionAssetList)
	crossClaims.AllowedSiteIDs = []string{testSiteA1, testSiteA2}
	assetPath := RegistryPathPrefix + "assets?siteId=" + testSiteA1 +
		"&status=ACTIVE&assetType=CHILLER&spaceId=" + spaceID +
		"&templateKey=chiller.v1&templateRevisionId=" + revID +
		"&tag=plant:north&tag=critical"
	rec := harness.serve(t, http.MethodGet, assetPath, crossClaims, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("cross-site assets status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.lastSiteID != testSiteA1 ||
		store.lastAssetFilter.Status != "ACTIVE" ||
		store.lastAssetFilter.AssetType != "CHILLER" ||
		store.lastAssetFilter.SpaceID != spaceID ||
		store.lastAssetFilter.TemplateKey != "chiller.v1" ||
		store.lastAssetFilter.TemplateRevisionID != revID ||
		store.lastAssetFilter.Tags["plant"] != "north" ||
		!slices.Equal(store.lastAssetFilter.TagKeys, []string{"critical"}) {
		t.Fatalf("unexpected captured asset filter: site=%q filter=%+v", store.lastSiteID, store.lastAssetFilter)
	}

	var assetResp struct {
		Items      []Asset `json:"items"`
		NextCursor string  `json:"nextCursor"`
		HasMore    bool    `json:"hasMore"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&assetResp); err != nil {
		t.Fatalf("decode asset response: %v", err)
	}
	if !assetResp.HasMore || assetResp.NextCursor == "" || len(assetResp.Items) != 1 || assetResp.Items[0].Tags["plant"] != "north" {
		t.Fatalf("unexpected assetResp: %+v", assetResp)
	}

	// Replaying cursor under different filter must fail with 400 CURSOR_INVALID.
	mismatchPath := RegistryPathPrefix + "assets?siteId=" + testSiteA1 +
		"&status=ACTIVE&assetType=CHILLER&spaceId=" + spaceID +
		"&templateKey=chiller.v1&templateRevisionId=" + revID +
		"&tag=plant:south&tag=critical&cursor=" + url.QueryEscape(assetResp.NextCursor)
	mismatchRec := harness.serve(t, http.MethodGet, mismatchPath, crossClaims, nil)
	if mismatchRec.Code != http.StatusBadRequest || !strings.Contains(mismatchRec.Body.String(), "CURSOR_INVALID") {
		t.Fatalf("expected 400 CURSOR_INVALID on cursor filter change, got %d (%s)", mismatchRec.Code, mismatchRec.Body.String())
	}

	// Invalid tag syntax -> 400 CURSOR_INVALID.
	badTagRec := harness.serve(t, http.MethodGet, RegistryPathPrefix+"devices?tag=BadKey:val", testGrantClaims(registryauth.ActionDeviceList), nil)
	if badTagRec.Code != http.StatusBadRequest || !strings.Contains(badTagRec.Body.String(), "CURSOR_INVALID") {
		t.Fatalf("expected 400 CURSOR_INVALID on invalid tag query, got %d (%s)", badTagRec.Code, badTagRec.Body.String())
	}

	// Template read routes allow ActionTemplateManage, ActionAssetModelRead, and ActionDeviceList.
	for _, action := range []registryauth.Action{
		registryauth.ActionTemplateManage,
		registryauth.ActionAssetModelRead,
		registryauth.ActionDeviceList,
	} {
		tplRec := harness.serve(t, http.MethodGet, RegistryPathPrefix+"templates?templateKind=DEVICE", testGrantClaims(action), nil)
		if tplRec.Code != http.StatusOK || store.lastTemplateKind != "DEVICE" {
			t.Fatalf("action %s failed GET templates: status=%d kind=%q body=%s", action, tplRec.Code, store.lastTemplateKind, tplRec.Body.String())
		}
		revRec := harness.serve(t, http.MethodGet, RegistryPathPrefix+"templates/revisions/"+revID, testGrantClaims(action), nil)
		if revRec.Code != http.StatusOK || store.lastRevisionID != revID {
			t.Fatalf("action %s failed GET template revision: status=%d body=%s", action, revRec.Code, revRec.Body.String())
		}
		assignRec := harness.serve(t, http.MethodGet, RegistryPathPrefix+"sites/"+testSiteA1+"/template-assignments?targetType=DEVICE&targetId="+testDeviceA1, testGrantClaims(action), nil)
		if assignRec.Code != http.StatusOK || store.lastTargetType != "DEVICE" || store.lastTargetID != testDeviceA1 {
			t.Fatalf("action %s failed GET site template assignments: status=%d body=%s", action, assignRec.Code, assignRec.Body.String())
		}
	}

	deniedTplRec := harness.serve(t, http.MethodGet, RegistryPathPrefix+"templates", testGrantClaims(registryauth.ActionAssetWrite), nil)
	if deniedTplRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for ActionAssetWrite on GET templates, got %d", deniedTplRec.Code)
	}
}

func TestOutboxRelayCapabilityHTTPClient(t *testing.T) {
	var captured struct {
		Path     string
		TenantID string
		Profiles []TemplateCapabilityProfile
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.Path = r.URL.Path
		var body struct {
			TenantID       string                      `json:"tenantId"`
			SourceSyncedAt *time.Time                  `json:"sourceSyncedAt,omitempty"`
			Profiles       []TemplateCapabilityProfile `json:"profiles"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		captured.TenantID = body.TenantID
		captured.Profiles = body.Profiles
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"syncedCount":1}`))
	}))
	defer server.Close()

	client := &httpDownstreamClient{baseURL: server.URL, client: server.Client()}
	input := []TemplateCapabilityProfile{{
		CapabilityName:     "SET_CHW_SETPOINT",
		CapabilityRevision: "v1",
		Status:             "VERIFIED",
		CanonicalUnit:      "degC",
		MinimumValue:       5.0,
		MaximumValue:       15.0,
		MaximumDelta:       2.0,
		RiskLevel:          "MEDIUM",
		ApprovalPolicy:     "NONE",
		RetryPolicy:        "PRE_SEND_ONLY",
		ConnectorKind:      "MODBUS_TCP",
	}}
	if err := client.SyncCapabilityProfiles(t.Context(), testTenantA, time.Now().UTC(), input); err != nil {
		t.Fatalf("SyncCapabilityProfiles failed: %v", err)
	}
	if captured.Path != "/internal/v1/capability-profiles:sync" || captured.TenantID != testTenantA || len(captured.Profiles) != 1 || captured.Profiles[0].CapabilityName != "SET_CHW_SETPOINT" {
		t.Fatalf("unexpected sync HTTP payload: %+v", captured)
	}
}
