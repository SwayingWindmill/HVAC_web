package optimization

import (
	"encoding/json"
	"testing"
)

func TestOptimizationSchedulerPayloadContainsOnlyServerOwnedIdentity(t *testing.T) {
	reference := SchedulerOptimizationReference{
		OptimizationRunID: "01990000-1950-7000-8000-000000000101",
		InputSnapshotID:   "01990000-1930-7000-8000-000000000101",
	}
	payload, err := json.Marshal(reference)
	if err != nil {
		t.Fatal(err)
	}
	job := SchedulerJob{
		JobID: reference.OptimizationRunID, JobType: "OPTIMIZATION_RUN",
		TenantID: "01990000-3000-7000-8000-000000000001", SiteID: "01990000-5000-7000-8000-000000000001",
		Payload: payload, AttemptNo: 1, MaxAttempts: 3, TimeoutSeconds: 120,
	}
	parsed, err := ValidateOptimizationSchedulerJob(job)
	if err != nil || parsed != reference {
		t.Fatalf("reference=%#v err=%v", parsed, err)
	}

	oldPayload, err := json.Marshal(validRequest())
	if err != nil {
		t.Fatal(err)
	}
	job.Payload = oldPayload
	if _, err = ValidateOptimizationSchedulerJob(job); err == nil {
		t.Fatal("caller-authored optimization request must not be accepted as scheduler payload")
	}
}
