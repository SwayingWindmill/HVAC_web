package migration

import (
	"strings"
	"testing"
	"time"
)

func TestReadRecordsSortsTenantSiteDependencyOrder(t *testing.T) {
	input := strings.Join([]string{
		`{"tenantId":"018f1d00-0000-7000-8000-000000000001","kind":"DEVICE","sourceSystem":"legacy","sourceTable":"device","sourceKey":"d1","sourceWatermark":"w1","sourceRowHash":"dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd","transformationVersion":"s1-v1","batchId":"b1","code":"d1","displayName":"Device","status":"ACTIVE","resourceType":"CONTROLLER","siteRef":{"sourceSystem":"legacy","sourceTable":"site","sourceKey":"s1"}}`,
		`{"tenantId":"018f1d00-0000-7000-8000-000000000001","kind":"SITE","sourceSystem":"legacy","sourceTable":"site","sourceKey":"s1","sourceWatermark":"w1","sourceRowHash":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","transformationVersion":"s1-v1","batchId":"b1","code":"s1","displayName":"Site","status":"ACTIVE","timezone":"Asia/Tokyo"}`,
	}, "\n")
	records, err := ReadRecords(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[0].Kind != KindSite || records[1].Kind != KindDevice {
		t.Fatalf("unexpected dependency order: %#v", records)
	}
}

func TestReadRecordsRejectsOrganizationAndUnknownFields(t *testing.T) {
	organization := `{"tenantId":"018f1d00-0000-7000-8000-000000000001","kind":"ORGANIZATION","sourceSystem":"legacy","sourceTable":"organization","sourceKey":"o1","sourceWatermark":"w1","sourceRowHash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","transformationVersion":"s1-v1","batchId":"b1","code":"o1","displayName":"Organization","status":"ACTIVE"}`
	if _, err := ReadRecords(strings.NewReader(organization)); err == nil {
		t.Fatal("expected ORGANIZATION record rejection")
	}

	siteWithOrganizationRef := `{"tenantId":"018f1d00-0000-7000-8000-000000000001","kind":"SITE","sourceSystem":"legacy","sourceTable":"site","sourceKey":"s1","sourceWatermark":"w1","sourceRowHash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","transformationVersion":"s1-v1","batchId":"b1","code":"s1","displayName":"Site","status":"ACTIVE","timezone":"Asia/Tokyo","organizationRef":{"sourceSystem":"legacy","sourceTable":"organization","sourceKey":"o1"}}`
	if _, err := ReadRecords(strings.NewReader(siteWithOrganizationRef)); err == nil {
		t.Fatal("expected organizationRef rejection")
	}
}

func TestBusinessReasonRequiresSiteOnlyForChildren(t *testing.T) {
	site := validRecord(KindSite)
	if reason := site.BusinessReason(); reason != "" {
		t.Fatalf("site reason = %q", reason)
	}

	asset := validRecord(KindAsset)
	asset.SiteRef = nil
	if reason := asset.BusinessReason(); reason != "MISSING_SITE_PARENT" {
		t.Fatalf("asset reason = %q", reason)
	}
}

func TestUUIDv7Generation(t *testing.T) {
	value, err := newUUIDv7(time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if !isUUIDv7(value) {
		t.Fatalf("not UUIDv7: %s", value)
	}
}

func validRecord(kind string) Record {
	record := Record{
		TenantID:              "018f1d00-0000-7000-8000-000000000001",
		Kind:                  kind,
		SourceSystem:          "legacy-test",
		SourceTable:           strings.ToLower(kind),
		SourceKey:             strings.ToLower(kind) + "-1",
		SourceWatermark:       "watermark-1",
		SourceRowHash:         strings.Repeat("a", 64),
		TransformationVersion: "s1-v1",
		BatchID:               "batch-1",
		Code:                  strings.ToLower(kind) + "-1",
		DisplayName:           kind + " 1",
		Status:                "ACTIVE",
		RelationEvidence:      map[string]any{},
	}
	site := &SourceRef{SourceSystem: "legacy-test", SourceTable: "site", SourceKey: "site-1"}
	switch kind {
	case KindSite:
		record.Timezone = "Asia/Tokyo"
	case KindAsset:
		record.SiteRef = site
		record.ResourceType = "AHU"
	case KindDevice:
		record.SiteRef = site
		record.ResourceType = "CONTROLLER"
	}
	return record
}
