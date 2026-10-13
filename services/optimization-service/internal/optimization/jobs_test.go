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

// A Site's schedule fires a job that names no run; the worker prepares one on the whole Site and
// queues it as SCHEDULE. The run's own job carries its reference and no schedule.
func TestAScheduleFiringPreparesARunOnTheWholeSite(t *testing.T) {
	firing := SchedulerJob{
		JobID: "01990000-1950-7000-8000-000000000201", ScheduleID: "01990000-1960-7000-8000-000000000001", TriggerType: "SCHEDULE", JobType: "OPTIMIZATION_RUN",
		TenantID: "01990000-3000-7000-8000-000000000001", SiteID: "01990000-5000-7000-8000-000000000001",
		Payload: json.RawMessage(`{}`), AttemptNo: 1, MaxAttempts: 3, TimeoutSeconds: 120,
	}
	if !IsScheduledPreparation(firing) {
		t.Fatal("a schedule firing was taken for a prepared run")
	}
	request, err := ScheduledPreparationRequest(firing)
	if err != nil || request.Trigger != TriggerSchedule || request.SubjectType != "SITE" || request.SubjectID != firing.SiteID || request.TenantID != firing.TenantID {
		t.Fatalf("request=%#v err=%v", request, err)
	}
	run := firing
	run.ScheduleID, run.TriggerType = "", "SCHEDULE"
	if IsScheduledPreparation(run) {
		t.Fatal("a prepared run was taken for a schedule firing")
	}
}
