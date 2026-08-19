package scheduler

import "testing"

func TestMaintenanceLeaseRecoveryPolicy(t *testing.T) {
	retrySafe := []string{
		"METRIC_WINDOW_CALC",
		"DATA_RETENTION_SCAN",
		"DATA_ARCHIVE",
		"CERTIFICATE_EXPIRY_SCAN",
		"OUTBOX_CLEANUP",
		"INBOX_CLEANUP",
		"PROJECTION_REPAIR",
		"DEAD_WORK_DISPOSITION",
		"TENANT_RETIREMENT",
	}
	for _, jobType := range retrySafe {
		if !isLeaseRetrySafeJob(jobType) {
			t.Fatalf("expected %s to resume after an expired lease", jobType)
		}
	}
	if isLeaseRetrySafeJob("OPTIMIZATION_RUN") {
		t.Fatal("optimization work requires owner reconciliation after an expired lease")
	}
}
