package fdd

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPostgresServicePersistsAuthoritativeHistoryFinding(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("FDD_POSTGRES_INTEGRATION_DSN"))
	tenantID := strings.TrimSpace(os.Getenv("FDD_POSTGRES_INTEGRATION_TENANT_ID"))
	siteID := strings.TrimSpace(os.Getenv("FDD_POSTGRES_INTEGRATION_SITE_ID"))
	assetID := strings.TrimSpace(os.Getenv("FDD_POSTGRES_INTEGRATION_ASSET_ID"))
	if dsn == "" || tenantID == "" || siteID == "" || assetID == "" {
		t.Skip("FDD Postgres integration fixture is not configured")
	}

	request := validEvaluation()
	request.TenantID = tenantID
	request.SiteID = siteID
	request.AssetID = assetID
	request.EvaluationTo = time.Now().UTC()
	request.EvaluationFrom = request.EvaluationTo.Add(-15 * time.Minute)
	history := &historySource{response: validHistoryResponse(request)}

	store, err := OpenPostgres(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	service := newTestService(t, store, history, func() time.Time {
		return request.EvaluationTo.Add(time.Minute)
	})

	result, err := service.EvaluateLowDeltaT(t.Context(), request, "history-grant")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "FINDING" || result.Finding == nil {
		t.Fatalf("result=%#v", result)
	}
	findingID := result.Finding.ID
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		tx, cleanupErr := store.pool.Begin(ctx)
		if cleanupErr != nil {
			t.Errorf("begin FDD integration cleanup: %v", cleanupErr)
			return
		}
		defer tx.Rollback(ctx)
		if cleanupErr = fddScope(ctx, tx, tenantID, siteID); cleanupErr == nil {
			_, cleanupErr = tx.Exec(ctx, `DELETE FROM core_registry.fdd_findings WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND id=$3::uuid`, tenantID, siteID, findingID)
		}
		if cleanupErr == nil {
			cleanupErr = tx.Commit(ctx)
		}
		if cleanupErr != nil {
			t.Errorf("clean FDD integration finding: %v", cleanupErr)
		}
	})

	findings, err := service.ListFindings(t.Context(), tenantID, siteID, 200)
	if err != nil {
		t.Fatal(err)
	}
	for _, finding := range findings {
		if finding.ID != findingID {
			continue
		}
		if len(finding.EvidenceIDs) != 2 || finding.EvidenceIDs[0] != result.Finding.EvidenceIDs[0] || finding.EvidenceIDs[1] != result.Finding.EvidenceIDs[1] {
			t.Fatalf("persisted evidence IDs=%#v", finding.EvidenceIDs)
		}
		return
	}
	t.Fatalf("persisted FDD finding %s was not readable through PostgresStore", findingID)
}
