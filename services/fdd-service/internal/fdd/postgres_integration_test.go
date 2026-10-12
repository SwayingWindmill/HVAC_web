package fdd

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/schedulerjob"
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

	store, err := OpenPostgres(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	service := newTestService(t, store, &loopEvidence{readings: validReadings(request)}, func() time.Time {
		return request.EvaluationTo.Add(time.Minute)
	})

	result, err := service.EvaluateLowDeltaT(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "FINDING" || result.Finding == nil {
		t.Fatalf("result=%#v", result)
	}
	findingID := result.Finding.ID
	// The FDD runtime cannot delete findings, so the gate runs this against a disposable database.
	findings, err := service.ListFindings(t.Context(), tenantID, siteID, FindingFilter{Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	var persisted bool
	for _, finding := range findings {
		if finding.ID != findingID {
			continue
		}
		if len(finding.EvidenceIDs) != 2 || finding.EvidenceIDs[0] != result.Finding.EvidenceIDs[0] || finding.EvidenceIDs[1] != result.Finding.EvidenceIDs[1] {
			t.Fatalf("persisted evidence IDs=%#v", finding.EvidenceIDs)
		}
		if finding.ObservedValue != result.Finding.ObservedValue || finding.ThresholdValue != result.Finding.ThresholdValue || finding.ValueUnit != "K" {
			t.Fatalf("persisted measurement=%v/%v %q", finding.ObservedValue, finding.ThresholdValue, finding.ValueUnit)
		}
		persisted = true
	}
	if !persisted {
		t.Fatalf("persisted FDD finding %s was not readable through PostgresStore", findingID)
	}

	later := request
	later.EvaluationFrom, later.EvaluationTo = request.EvaluationFrom.Add(15*time.Minute), request.EvaluationTo.Add(15*time.Minute)
	ongoing, err := service.EvaluateLowDeltaT(t.Context(), later)
	if err != nil || ongoing.Status != "ONGOING" || ongoing.Finding == nil || ongoing.Finding.ID != findingID {
		t.Fatalf("the same condition a quarter later=%#v err=%v", ongoing, err)
	}
}

// The runner seeds one READY FDD_EVALUATION job as the Scheduler would; the FDD runtime role can
// claim, start and complete it.
func TestPostgresFDDRuntimeWorksItsScheduledEvaluations(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("FDD_POSTGRES_INTEGRATION_DSN"))
	jobID := strings.TrimSpace(os.Getenv("FDD_POSTGRES_INTEGRATION_JOB_ID"))
	if dsn == "" || jobID == "" {
		t.Skip("FDD Postgres scheduled-job fixture is not configured")
	}
	store, err := OpenPostgres(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	queue, err := schedulerjob.NewQueue(store.pool, EvaluationJobType)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	jobs, err := queue.Claim(t.Context(), "fdd-integration", 10, time.Minute, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].JobID != jobID || jobs[0].JobType != EvaluationJobType {
		t.Fatalf("claimed=%#v", jobs)
	}
	started, err := queue.Start(t.Context(), jobs[0], time.Minute, now)
	if err != nil || !started {
		t.Fatalf("started=%v err=%v", started, err)
	}
	if err = queue.Complete(t.Context(), jobs[0], map[string]any{"status": "CLEAR"}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
}
