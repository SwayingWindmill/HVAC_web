package core

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestScanAssetIncludesOptionalWaterLoopAndBranchCode(t *testing.T) {
	now := time.Date(2026, 11, 18, 8, 0, 0, 0, time.UTC)
	loop := "CHILLED_WATER"
	branch := "CH-A"

	withHydraulic, err := scanAsset(assetModelTestRow{
		values: []any{
			testAssetA1,
			testTenantA,
			testSiteA1,
			"chiller-a",
			"1#冷机",
			"CHILLER",
			"ACTIVE",
			int64(2),
			now,
			now,
			nil,
			nil,
			[]byte(`{"loop":"primary"}`),
			&loop,
			&branch,
		},
	})
	if err != nil {
		t.Fatalf("scanAsset with hydraulic fields error: %v", err)
	}
	if withHydraulic.WaterLoop == nil || *withHydraulic.WaterLoop != "CHILLED_WATER" {
		t.Fatalf("WaterLoop = %v, want CHILLED_WATER", withHydraulic.WaterLoop)
	}
	if withHydraulic.BranchCode == nil || *withHydraulic.BranchCode != "CH-A" {
		t.Fatalf("BranchCode = %v, want CH-A", withHydraulic.BranchCode)
	}

	legacy, err := scanAsset(assetModelTestRow{
		values: []any{
			testAssetA1,
			testTenantA,
			testSiteA1,
			"chiller-legacy",
			"旧站点冷机",
			"CHILLER",
			"ACTIVE",
			int64(1),
			now,
			now,
			nil,
			nil,
			[]byte(`{}`),
			(*string)(nil),
			(*string)(nil),
		},
	})
	if err != nil {
		t.Fatalf("scanAsset legacy NULL hydraulic fields error: %v", err)
	}
	if legacy.WaterLoop != nil || legacy.BranchCode != nil {
		t.Fatalf("expected nil hydraulic fields for legacy asset, got %+v", legacy)
	}
	encoded, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal legacy asset: %v", err)
	}
	if strings.Contains(string(encoded), "waterLoop") || strings.Contains(string(encoded), "branchCode") {
		t.Fatalf("expected nil waterLoop/branchCode to be omitted for legacy site, got %s", string(encoded))
	}
}

func TestBuildAssetRelationshipsIncludesHydraulicRoles(t *testing.T) {
	now := time.Date(2026, 11, 18, 8, 0, 0, 0, time.UTC)
	valveID := "018f1e00-3000-7000-8000-000000000091"
	hdrID := "018f1e00-3000-7000-8000-000000000092"
	isolates, err := scanAssetRelationship(assetModelTestRow{
		values: []any{
			"018f1e00-7000-7000-8000-000000000091",
			testTenantA,
			testSiteA1,
			valveID,
			testAssetA1,
			"ISOLATES",
			"ACTIVE",
			now,
			(*time.Time)(nil),
			int64(1),
			now,
			now,
		},
	})
	if err != nil {
		t.Fatalf("scanAssetRelationship ISOLATES error: %v", err)
	}
	feedsHeader, err := scanAssetRelationship(assetModelTestRow{
		values: []any{
			"018f1e00-7000-7000-8000-000000000092",
			testTenantA,
			testSiteA1,
			testAssetA1,
			hdrID,
			"FEEDS_HEADER",
			"ACTIVE",
			now,
			(*time.Time)(nil),
			int64(1),
			now,
			now,
		},
	})
	if err != nil {
		t.Fatalf("scanAssetRelationship FEEDS_HEADER error: %v", err)
	}
	rels := buildAssetRelationships(SiteAssetModel{
		AssetRelationships: []AssetRelationship{isolates, feedsHeader},
	})
	if len(rels) != 2 || rels[0].Role != "ISOLATES" || rels[1].Role != "FEEDS_HEADER" {
		t.Fatalf("unexpected hydraulic relationships in SiteAssetModel: %+v", rels)
	}
}
