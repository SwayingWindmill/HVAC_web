package migration

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresMigrationExecutionAndQuarantineResolution(t *testing.T) {
	migrationDSN := os.Getenv("S1_LEGACY_MIGRATION_DSN")
	adminDSN := os.Getenv("S1_ADMIN_DATABASE_URL")
	coreDSN := os.Getenv("S1_CORE_DATABASE_URL")
	if migrationDSN == "" || adminDSN == "" || coreDSN == "" {
		t.Skip("S1 migration PostgreSQL URLs are not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	migrator, err := OpenPostgres(ctx, migrationDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer migrator.Close()
	admin, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()

	system := fmt.Sprintf("ticket04-%d", time.Now().UnixNano())
	organization := integrationRecord(KindOrganization, system, "org-1", "a")
	site := integrationRecord(KindSite, system, "site-1", "b")
	equipment := integrationRecord(KindEquipment, system, "equipment-1", "c")
	device := integrationRecord(KindDevice, system, "device-1", "d")

	summary, err := migrator.Apply(ctx, []Record{device, equipment, site, organization})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Imported != 4 || summary.Quarantined != 0 {
		t.Fatalf("initial summary = %#v", summary)
	}
	targetBySourceKey := map[string]string{}
	for _, result := range summary.Results {
		targetBySourceKey[result.SourceKey] = result.TargetID
		if !isUUIDv7(result.TargetID) {
			t.Fatalf("target id is not owner-generated UUIDv7: %#v", result)
		}
		if result.TargetID == result.SourceKey {
			t.Fatalf("legacy source key became public id: %#v", result)
		}
	}

	replay, err := migrator.Apply(ctx, []Record{organization, site, equipment, device})
	if err != nil {
		t.Fatal(err)
	}
	if replay.Skipped != 4 || replay.Imported != 0 {
		t.Fatalf("replay summary = %#v", replay)
	}

	concurrentRecord := integrationRecord(KindOrganization, system, "concurrent-org", "7")
	concurrentResults := make(chan RecordResult, 2)
	concurrentErrors := make(chan error, 2)
	var concurrentGroup sync.WaitGroup
	for range 2 {
		concurrentGroup.Add(1)
		go func() {
			defer concurrentGroup.Done()
			result, applyErr := migrator.ApplyRecord(ctx, concurrentRecord)
			if applyErr != nil {
				concurrentErrors <- applyErr
				return
			}
			concurrentResults <- result
		}()
	}
	concurrentGroup.Wait()
	close(concurrentResults)
	close(concurrentErrors)
	for applyErr := range concurrentErrors {
		t.Fatal(applyErr)
	}
	concurrentOutcomes := map[Outcome]int{}
	for result := range concurrentResults {
		concurrentOutcomes[result.Outcome]++
	}
	if concurrentOutcomes[OutcomeImported] != 1 || concurrentOutcomes[OutcomeSkipped] != 1 {
		t.Fatalf("concurrent outcomes = %#v", concurrentOutcomes)
	}

	changedDevice := device
	changedDevice.SourceRowHash = strings.Repeat("e", 64)
	changed, err := migrator.ApplyRecord(ctx, changedDevice)
	if err != nil {
		t.Fatal(err)
	}
	if changed.Outcome != OutcomeQuarantined || changed.ReasonCode != "SOURCE_HASH_CONFLICT" {
		t.Fatalf("changed source result = %#v", changed)
	}
	assertMappingState(t, ctx, admin, system, "device", "device-1", "QUARANTINED", false)
	repeatedChanged, err := migrator.ApplyRecord(ctx, changedDevice)
	if err != nil {
		t.Fatal(err)
	}
	if repeatedChanged.Outcome != OutcomeQuarantined || repeatedChanged.ReasonCode != "SOURCE_HASH_CONFLICT" {
		t.Fatalf("repeated quarantine result = %#v", repeatedChanged)
	}
	deviceQuarantineID := openQuarantineID(t, ctx, admin, system, "device", "device-1")
	restoredDevice, err := migrator.Resolve(ctx, deviceQuarantineID, ResolutionApply, device)
	if err != nil {
		t.Fatal(err)
	}
	if restoredDevice.Outcome != OutcomeResolved || restoredDevice.TargetID != targetBySourceKey["device-1"] {
		t.Fatalf("restored source-hash conflict = %#v", restoredDevice)
	}
	assertMappingState(t, ctx, admin, system, "device", "device-1", "VERIFIED", true)

	existingRetire := integrationRecord(KindOrganization, system, "existing-retire", "5")
	existingRetireResult, err := migrator.ApplyRecord(ctx, existingRetire)
	if err != nil {
		t.Fatal(err)
	}
	existingRetireConflict := existingRetire
	existingRetireConflict.SourceRowHash = strings.Repeat("6", 64)
	if result, applyErr := migrator.ApplyRecord(ctx, existingRetireConflict); applyErr != nil {
		t.Fatal(applyErr)
	} else if result.ReasonCode != "SOURCE_HASH_CONFLICT" {
		t.Fatalf("existing retirement conflict = %#v", result)
	}
	existingRetireQuarantineID := openQuarantineID(t, ctx, admin, system, "organization", "existing-retire")
	existingRetired, err := migrator.Resolve(ctx, existingRetireQuarantineID, ResolutionRetire, existingRetireConflict)
	if err != nil {
		t.Fatal(err)
	}
	if existingRetired.Outcome != OutcomeRetired || existingRetired.TargetID != existingRetireResult.TargetID {
		t.Fatalf("existing target retirement = %#v", existingRetired)
	}
	var existingRetireStatus string
	var existingRetireRevision int64
	if err := admin.QueryRow(ctx, `SELECT status, revision FROM core_registry.organizations WHERE id=$1`, existingRetired.TargetID).Scan(&existingRetireStatus, &existingRetireRevision); err != nil {
		t.Fatal(err)
	}
	if existingRetireStatus != "RETIRED" || existingRetireRevision != 2 {
		t.Fatalf("existing retired business state = %s/%d", existingRetireStatus, existingRetireRevision)
	}
	assertMappingState(t, ctx, admin, system, "organization", "existing-retire", "RETIRED", true)

	missingParent := integrationRecord(KindEquipment, system, "missing-parent-equipment", "f")
	missingParent.SiteRef = &SourceRef{SourceSystem: system, SourceTable: "site", SourceKey: "absent-site"}
	missing, err := migrator.ApplyRecord(ctx, missingParent)
	if err != nil {
		t.Fatal(err)
	}
	if missing.Outcome != OutcomeQuarantined || missing.ReasonCode != "MISSING_SITE_PARENT" {
		t.Fatalf("missing parent result = %#v", missing)
	}

	invalidTimezone := integrationRecord(KindSite, system, "invalid-timezone", "8")
	invalidTimezone.Timezone = "Mars/Olympus"
	invalidTimezoneResult, err := migrator.ApplyRecord(ctx, invalidTimezone)
	if err != nil {
		t.Fatal(err)
	}
	if invalidTimezoneResult.Outcome != OutcomeQuarantined || invalidTimezoneResult.ReasonCode != "INVALID_TIMEZONE" {
		t.Fatalf("invalid timezone result = %#v", invalidTimezoneResult)
	}

	duplicateOrganization := integrationRecord(KindOrganization, system, "duplicate-organization", "9")
	duplicateOrganization.Code = organization.Code
	duplicateResult, err := migrator.ApplyRecord(ctx, duplicateOrganization)
	if err != nil {
		t.Fatal(err)
	}
	if duplicateResult.Outcome != OutcomeQuarantined || duplicateResult.ReasonCode != "DUPLICATE_BUSINESS_KEY" {
		t.Fatalf("duplicate business key result = %#v", duplicateResult)
	}

	ambiguous := integrationRecord(KindEquipment, system, "ambiguous-asset", "1")
	ambiguous.SourceTable = "asset"
	ambiguous.RelationEvidence = map[string]any{"verifiedEquipmentRelation": false, "candidateTypes": []any{"EQUIPMENT", "GROUP"}}
	ambiguousResult, err := migrator.ApplyRecord(ctx, ambiguous)
	if err != nil {
		t.Fatal(err)
	}
	if ambiguousResult.ReasonCode != "AMBIGUOUS_ASSET_EQUIPMENT_RELATION" {
		t.Fatalf("ambiguous result = %#v", ambiguousResult)
	}
	ambiguousQuarantineID := openQuarantineID(t, ctx, admin, system, "asset", "ambiguous-asset")
	corrected := ambiguous
	corrected.SourceRowHash = strings.Repeat("2", 64)
	corrected.RelationEvidence = map[string]any{"verifiedEquipmentRelation": true}
	resolved, err := migrator.Resolve(ctx, ambiguousQuarantineID, ResolutionApply, corrected)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Outcome != OutcomeResolved || !isUUIDv7(resolved.TargetID) {
		t.Fatalf("resolved result = %#v", resolved)
	}
	assertMappingState(t, ctx, admin, system, "asset", "ambiguous-asset", "VERIFIED", true)
	resolvedAgain, err := migrator.Resolve(ctx, ambiguousQuarantineID, ResolutionApply, corrected)
	if err != nil {
		t.Fatal(err)
	}
	if resolvedAgain.Outcome != OutcomeSkipped || resolvedAgain.TargetID != resolved.TargetID {
		t.Fatalf("idempotent resolution = %#v", resolvedAgain)
	}

	retiredSource := integrationRecord(KindOrganization, system, "retired-source", "3")
	retiredSource.Status = "BROKEN"
	retiredQuarantine, err := migrator.ApplyRecord(ctx, retiredSource)
	if err != nil {
		t.Fatal(err)
	}
	if retiredQuarantine.ReasonCode != "INVALID_STATUS" {
		t.Fatalf("retirement quarantine = %#v", retiredQuarantine)
	}
	retiredQuarantineID := openQuarantineID(t, ctx, admin, system, "organization", "retired-source")
	retiredSource.Status = "ACTIVE"
	retiredSource.SourceRowHash = strings.Repeat("4", 64)
	retired, err := migrator.Resolve(ctx, retiredQuarantineID, ResolutionRetire, retiredSource)
	if err != nil {
		t.Fatal(err)
	}
	if retired.Outcome != OutcomeRetired {
		t.Fatalf("retired result = %#v", retired)
	}
	assertMappingState(t, ctx, admin, system, "organization", "retired-source", "RETIRED", true)

	var provenanceCount int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM core_registry.migration_provenance WHERE source_system=$1`, system).Scan(&provenanceCount); err != nil {
		t.Fatal(err)
	}
	if provenanceCount < 12 {
		t.Fatalf("provenance count = %d", provenanceCount)
	}

	unactivated, err := pgxpool.New(ctx, migrationDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer unactivated.Close()
	if err := unactivated.QueryRow(ctx, `SELECT count(*) FROM core_registry.organizations`).Scan(new(int)); err == nil {
		t.Fatal("migration login read business tables without activating operator role")
	}
	if err := unactivated.QueryRow(ctx, `SELECT count(*) FROM iam.principals`).Scan(new(int)); err == nil {
		t.Fatal("migration login read IAM data")
	}

	core, err := pgxpool.New(ctx, coreDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close()
	coreTx, err := core.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer coreTx.Rollback(ctx)
	if _, err := coreTx.Exec(ctx, `SET LOCAL ROLE s1_core_runtime`); err != nil {
		t.Fatal(err)
	}
	if _, err := coreTx.Exec(ctx, `SELECT set_config('app.authorized_organization_ids',$1,true)`, `{`+summary.Results[0].TargetID+`}`); err != nil {
		t.Fatal(err)
	}
	if _, err := coreTx.Exec(ctx, `SELECT set_config('app.authorized_site_ids','{}',true)`); err != nil {
		t.Fatal(err)
	}
	var visible int
	if err := coreTx.QueryRow(ctx, `SELECT count(*) FROM core_registry.legacy_resource_maps WHERE source_system=$1 AND mapping_state='QUARANTINED'`, system).Scan(&visible); err != nil {
		t.Fatal(err)
	}
	if visible != 0 {
		t.Fatalf("Core runtime saw quarantined mappings: %d", visible)
	}
	if err := coreTx.QueryRow(ctx, `SELECT count(*) FROM core_registry.migration_quarantine`).Scan(&visible); err == nil {
		t.Fatal("Core runtime read migration quarantine")
	}
}

func integrationRecord(kind, system, key, hashCharacter string) Record {
	record := validRecord(kind)
	record.SourceSystem = system
	record.SourceTable = strings.ToLower(kind)
	record.SourceKey = key
	record.SourceRowHash = strings.Repeat(hashCharacter, 64)
	record.Code = system + "-" + key
	record.DisplayName = kind + " " + key
	record.BatchID = system + "-batch"
	record.OrganizationRef = &SourceRef{SourceSystem: system, SourceTable: "organization", SourceKey: "org-1"}
	record.SiteRef = &SourceRef{SourceSystem: system, SourceTable: "site", SourceKey: "site-1"}
	if kind == KindOrganization {
		record.OrganizationRef = nil
		record.SiteRef = nil
	}
	if kind == KindSite {
		record.SiteRef = nil
	}
	return record
}

func openQuarantineID(t *testing.T, ctx context.Context, pool *pgxpool.Pool, system, table, key string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM core_registry.migration_quarantine WHERE source_system=$1 AND source_table=$2 AND source_key=$3 AND resolved_at IS NULL`, system, table, key).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func assertMappingState(t *testing.T, ctx context.Context, pool *pgxpool.Pool, system, table, key, state string, targetExpected bool) {
	t.Helper()
	var actualState string
	var target *string
	if err := pool.QueryRow(ctx, `SELECT mapping_state, target_resource_id::text FROM core_registry.legacy_resource_maps WHERE source_system=$1 AND source_table=$2 AND source_key=$3`, system, table, key).Scan(&actualState, &target); err != nil {
		t.Fatal(err)
	}
	if actualState != state {
		t.Fatalf("mapping state = %q, want %q", actualState, state)
	}
	if targetExpected != (target != nil && *target != "") {
		t.Fatalf("mapping target present = %v, want %v", target != nil, targetExpected)
	}
}
