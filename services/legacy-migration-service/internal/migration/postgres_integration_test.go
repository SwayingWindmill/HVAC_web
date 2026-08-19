package migration

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresMigrationUsesTenantSiteHierarchy(t *testing.T) {
	migrationDSN := os.Getenv("S1_LEGACY_MIGRATION_DSN")
	adminDSN := os.Getenv("S1_ADMIN_DATABASE_URL")
	if migrationDSN == "" || adminDSN == "" {
		t.Skip("S1 migration PostgreSQL URLs are not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
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

	system := fmt.Sprintf("v2-site-%d", time.Now().UnixNano())
	site := integrationRecord(KindSite, system, "site-1", "b")
	asset := integrationRecord(KindAsset, system, "asset-1", "c")
	device := integrationRecord(KindDevice, system, "device-1", "d")

	summary, err := migrator.Apply(ctx, []Record{device, asset, site})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Imported != 3 || summary.Quarantined != 0 {
		t.Fatalf("initial summary = %#v", summary)
	}

	replay, err := migrator.Apply(ctx, []Record{site, asset, device})
	if err != nil {
		t.Fatal(err)
	}
	if replay.Skipped != 3 || replay.Imported != 0 {
		t.Fatalf("replay summary = %#v", replay)
	}

	missingParent := integrationRecord(KindAsset, system, "missing-parent", "e")
	missingParent.SiteRef = &SourceRef{SourceSystem: system, SourceTable: "site", SourceKey: "absent-site"}
	missing, err := migrator.ApplyRecord(ctx, missingParent)
	if err != nil {
		t.Fatal(err)
	}
	if missing.Outcome != OutcomeQuarantined || missing.ReasonCode != "MISSING_SITE_PARENT" {
		t.Fatalf("missing parent result = %#v", missing)
	}

	var organizationColumns int
	if err := admin.QueryRow(ctx, `
		SELECT count(*) FROM information_schema.columns
		WHERE table_schema='core_registry'
		  AND table_name IN ('legacy_resource_maps','migration_provenance','migration_quarantine')
		  AND column_name='organization_id'`).Scan(&organizationColumns); err != nil {
		t.Fatal(err)
	}
	if organizationColumns != 0 {
		t.Fatalf("legacy migration tables still contain organization_id: %d", organizationColumns)
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
	record.SiteRef = &SourceRef{SourceSystem: system, SourceTable: "site", SourceKey: "site-1"}
	if kind == KindSite {
		record.SiteRef = nil
	}
	return record
}
