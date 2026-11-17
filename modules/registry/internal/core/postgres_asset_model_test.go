package core

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

type assetModelTestRow struct {
	values []any
}

func (row assetModelTestRow) Scan(dest ...any) error {
	for index, target := range dest {
		value := row.values[index]
		switch ptr := target.(type) {
		case *string:
			*ptr = value.(string)
		case **string:
			*ptr = value.(*string)
		case *int64:
			*ptr = value.(int64)
		case *time.Time:
			*ptr = value.(time.Time)
		case **time.Time:
			*ptr = value.(*time.Time)
		}
	}
	return nil
}

func TestPointSubjectBindingSemanticRoleSerializationAndScan(t *testing.T) {
	now := time.Date(2026, 11, 16, 8, 0, 0, 0, time.UTC)
	assetID := "01a14000-3000-7000-8000-000000000001"
	scanned, err := scanPointSubjectBinding(assetModelTestRow{
		values: []any{
			"01a14000-6300-7000-8000-000000000001",
			"018f3d00-0000-7000-8000-000000000001",
			"01a14000-1000-7000-8000-000000000001",
			"01a14000-5000-7000-8000-000000000001",
			"ASSET",
			(*string)(nil),
			&assetID,
			"DESCRIBES",
			"ACTIVE_POWER",
			"ACTIVE",
			now,
			(*time.Time)(nil),
			int64(3),
			now,
			now,
		},
	})
	if err != nil {
		t.Fatalf("scanPointSubjectBinding error: %v", err)
	}
	if scanned.SemanticRole != "ACTIVE_POWER" {
		t.Fatalf("SemanticRole = %q, want ACTIVE_POWER", scanned.SemanticRole)
	}

	encoded, err := json.Marshal(scanned)
	if err != nil {
		t.Fatalf("json.Marshal error: %v", err)
	}
	if !strings.Contains(string(encoded), `"semanticRole":"ACTIVE_POWER"`) {
		t.Fatalf("expected semanticRole in JSON, got %s", encoded)
	}

	scanned.SemanticRole = ""
	omitted, err := json.Marshal(scanned)
	if err != nil {
		t.Fatalf("json.Marshal error: %v", err)
	}
	if strings.Contains(string(omitted), "semanticRole") {
		t.Fatalf("expected empty semanticRole omitted from JSON, got %s", omitted)
	}
}

func TestBuildAssetRelationshipsIncludesExplicitAssetServesEdges(t *testing.T) {
	now := time.Date(2026, 11, 16, 8, 0, 0, 0, time.UTC)
	edge, err := scanAssetRelationship(assetModelTestRow{
		values: []any{
			"01a14000-6600-7000-8000-000000000001",
			"018f3d00-0000-7000-8000-000000000001",
			"01a14000-1000-7000-8000-000000000001",
			"01a14000-3000-7000-8000-000000000009",
			"01a14000-3000-7000-8000-000000000001",
			"SERVES",
			"ACTIVE",
			now,
			(*time.Time)(nil),
			int64(1),
			now,
			now,
		},
	})
	if err != nil {
		t.Fatalf("scanAssetRelationship error: %v", err)
	}
	if edge.FromType != "ASSET" || edge.ToType != "ASSET" || edge.Role != "SERVES" {
		t.Fatalf("unexpected scanned AssetRelationship: %+v", edge)
	}

	relationships := buildAssetRelationships(SiteAssetModel{
		AssetRelationships: []AssetRelationship{edge},
		DeviceBindings: []DeviceBinding{
			{
				ID:          "01a14000-6400-7000-8000-000000000001",
				TenantID:    edge.TenantID,
				SiteID:      edge.SiteID,
				DeviceID:    "01a14000-4000-7000-8000-000000000006",
				AssetID:     edge.ToID,
				BindingRole: "METER",
				Status:      "ACTIVE",
				ValidFrom:   edge.ValidFrom,
				Revision:    1,
				CreatedAt:   edge.CreatedAt,
				UpdatedAt:   edge.UpdatedAt,
			},
		},
	})
	if len(relationships) != 2 {
		t.Fatalf("relationships len = %d, want 2: %+v", len(relationships), relationships)
	}
	if relationships[0].FromType != "ASSET" || relationships[0].ToType != "ASSET" || relationships[0].Role != "SERVES" ||
		relationships[0].FromID != edge.FromID || relationships[0].ToID != edge.ToID {
		t.Fatalf("first relationship = %+v, want ASSET->ASSET SERVES", relationships[0])
	}
}
