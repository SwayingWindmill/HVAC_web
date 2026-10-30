package fdd

import (
	"os"
	"reflect"
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
	ruleID := strings.TrimSpace(os.Getenv("FDD_POSTGRES_INTEGRATION_RULE_ID"))
	if dsn == "" || tenantID == "" || siteID == "" || assetID == "" || ruleID == "" {
		t.Skip("FDD Postgres integration fixture is not configured")
	}

	request := validEvaluation()
	request.TenantID = tenantID
	request.SiteID = siteID
	request.AssetID = assetID
	request.At = time.Now().UTC().Truncate(time.Minute)

	store, err := OpenPostgres(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	service := newTestService(t, store, &loopEvidence{steady: true}, func() time.Time {
		return request.At.Add(time.Minute)
	})

	result, err := service.EvaluateLowDeltaT(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	// The runner seeded the Tenant's rule revision, which takes the migration's default gates; the
	// finding cites the revision and its threshold.
	if result.Status != "FINDING" || result.Finding == nil || result.Rule.ID != ruleID || result.Finding.RuleRevisionID != ruleID || result.Finding.ThresholdValue != result.Rule.MinimumDeltaTC {
		t.Fatalf("result=%#v", result)
	}
	if !reflect.DeepEqual(result.Rule.OperatingGates, testGates()) || result.Window.OperatingMinutes != 15 {
		t.Fatalf("gates=%#v window=%#v", result.Rule.OperatingGates, result.Window)
	}
	// FDD reads the Site's chillers and pumps through its own row scope (migration 033).
	topology, err := store.OperatingTopology(t.Context(), tenantID, siteID, request.At)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("topology: %d chillers, %d pumps", len(topology.Chillers), len(topology.Pumps))
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
		if !reflect.DeepEqual(finding.EvidenceIDs, result.Finding.EvidenceIDs) {
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
	later.At = request.At.Add(15 * time.Minute)
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
