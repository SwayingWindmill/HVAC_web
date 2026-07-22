package migration

import (
	"strings"
	"testing"
	"time"
)

func TestReadRecordsSortsDependencyOrder(t *testing.T) {
	input := strings.Join([]string{
		`{"kind":"DEVICE","sourceSystem":"legacy","sourceTable":"device","sourceKey":"d1","sourceWatermark":"w1","sourceRowHash":"dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd","transformationVersion":"s1-v1","batchId":"b1","code":"d1","displayName":"Device","status":"ACTIVE","resourceType":"CONTROLLER","organizationRef":{"sourceSystem":"legacy","sourceTable":"organization","sourceKey":"o1"},"siteRef":{"sourceSystem":"legacy","sourceTable":"site","sourceKey":"s1"}}`,
		`{"kind":"ORGANIZATION","sourceSystem":"legacy","sourceTable":"organization","sourceKey":"o1","sourceWatermark":"w1","sourceRowHash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","transformationVersion":"s1-v1","batchId":"b1","code":"o1","displayName":"Organization","status":"ACTIVE"}`,
		`{"kind":"SITE","sourceSystem":"legacy","sourceTable":"site","sourceKey":"s1","sourceWatermark":"w1","sourceRowHash":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","transformationVersion":"s1-v1","batchId":"b1","code":"s1","displayName":"Site","status":"ACTIVE","timezone":"Asia/Tokyo","organizationRef":{"sourceSystem":"legacy","sourceTable":"organization","sourceKey":"o1"}}`,
	}, "\n")
	records, err := ReadRecords(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 3 || records[0].Kind != KindOrganization || records[1].Kind != KindSite || records[2].Kind != KindDevice {
		t.Fatalf("unexpected dependency order: %#v", records)
	}
	if records[0].RelationEvidence == nil {
		t.Fatal("relation evidence was not normalized to an object")
	}
}

func TestReadRecordsRejectsUnknownFieldsAndCredentialMetadata(t *testing.T) {
	base := `{"kind":"ORGANIZATION","sourceSystem":"legacy","sourceTable":"organization","sourceKey":"o1","sourceWatermark":"w1","sourceRowHash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","transformationVersion":"s1-v1","batchId":"b1","code":"o1","displayName":"Organization","status":"ACTIVE"`
	for name, input := range map[string]string{
		"unknown field":       base + `,"unexpected":true}`,
		"credential metadata": base + `,"relationEvidence":{"apiToken":"opaque"}}`,
		"trailing JSON":       base + `} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ReadRecords(strings.NewReader(input)); err == nil {
				t.Fatal("expected input rejection")
			}
		})
	}
}

func TestReadRecordsRejectsUnknownKindAndOversizedLine(t *testing.T) {
	unknownKind := `{"kind":"GROUP","sourceSystem":"legacy","sourceTable":"group","sourceKey":"g1","sourceWatermark":"w1","sourceRowHash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","transformationVersion":"s1-v1","batchId":"b1","code":"g1","displayName":"Group","status":"ACTIVE"}`
	if _, err := ReadRecords(strings.NewReader(unknownKind)); err == nil {
		t.Fatal("expected unknown record kind rejection")
	}
	oversized := strings.Repeat("x", maxRecordBytes+1)
	if _, err := ReadRecords(strings.NewReader(oversized)); err == nil {
		t.Fatal("expected oversized record rejection")
	}
}

func TestBusinessReasonRejectsInvalidResourceValues(t *testing.T) {
	organization := validRecord(KindOrganization)
	organization.Status = "BROKEN"
	if reason := organization.BusinessReason(); reason != "INVALID_STATUS" {
		t.Fatalf("organization reason = %q", reason)
	}
	site := validRecord(KindSite)
	site.Timezone = ""
	if reason := site.BusinessReason(); reason != "INVALID_TIMEZONE" {
		t.Fatalf("site reason = %q", reason)
	}
	equipment := validRecord(KindEquipment)
	equipment.ResourceType = ""
	if reason := equipment.BusinessReason(); reason != "INVALID_RESOURCE_TYPE" {
		t.Fatalf("equipment reason = %q", reason)
	}
}

func TestBusinessReasonQuarantinesAmbiguousAsset(t *testing.T) {
	record := validRecord(KindEquipment)
	record.SourceTable = "asset"
	record.RelationEvidence = map[string]any{"verifiedEquipmentRelation": false}
	if reason := record.BusinessReason(); reason != "AMBIGUOUS_ASSET_EQUIPMENT_RELATION" {
		t.Fatalf("reason = %q", reason)
	}
	record.RelationEvidence["verifiedEquipmentRelation"] = true
	if reason := record.BusinessReason(); reason != "" {
		t.Fatalf("verified relation reason = %q", reason)
	}
}

func TestSourceIdentityIsLengthBoundAndUnambiguous(t *testing.T) {
	left := Record{SourceSystem: "a", SourceTable: "bc", SourceKey: "d"}
	right := Record{SourceSystem: "ab", SourceTable: "c", SourceKey: "d"}
	if left.SourceIdentity() == right.SourceIdentity() {
		t.Fatal("distinct source triples produced the same lock identity")
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
	organization := &SourceRef{SourceSystem: "legacy-test", SourceTable: "organization", SourceKey: "organization-1"}
	site := &SourceRef{SourceSystem: "legacy-test", SourceTable: "site", SourceKey: "site-1"}
	switch kind {
	case KindSite:
		record.OrganizationRef = organization
		record.Timezone = "Asia/Tokyo"
	case KindEquipment:
		record.OrganizationRef = organization
		record.SiteRef = site
		record.ResourceType = "AHU"
	case KindDevice:
		record.OrganizationRef = organization
		record.SiteRef = site
		record.ResourceType = "CONTROLLER"
	}
	return record
}
