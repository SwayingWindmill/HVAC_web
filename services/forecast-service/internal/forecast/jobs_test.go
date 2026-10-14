package forecast

import (
	"encoding/json"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/schedulerjob"
)

func TestForecastSchedulerPayloadContainsOnlyServerOwnedIdentity(t *testing.T) {
	job := schedulerjob.Job{
		JobID: "01990000-1880-7000-8000-000000000001", JobType: "FORECAST_RUN",
		TenantID: "01990000-3000-7000-8000-000000000001", SiteID: "01990000-5000-7000-8000-000000000001",
		TimeoutSeconds: 120, AttemptNo: 1, MaxAttempts: 3,
	}
	job.Payload, _ = json.Marshal(SchedulerForecastReference{
		ForecastJobID: job.JobID, ForecastSnapshotID: "01990000-1890-7000-8000-000000000001",
	})
	if _, err := ValidateForecastSchedulerJob(job); err != nil {
		t.Fatal(err)
	}

	job.Payload, _ = json.Marshal(validRequest())
	if _, err := ValidateForecastSchedulerJob(job); err == nil {
		t.Fatal("legacy caller-authored Forecast Request payload was accepted by worker")
	}
}

// A schedule names what to forecast; the worker prepares a fresh Forecast for the job's own Site.
func TestScheduledForecastPlanBecomesAPreparationForTheJobsSite(t *testing.T) {
	job := schedulerjob.Job{
		JobID: "01990000-1880-7000-8000-000000000002", JobType: "FORECAST_RUN", TriggerType: "SCHEDULE",
		TenantID: "01990000-3000-7000-8000-000000000001", SiteID: "01990000-5000-7000-8000-000000000001",
		TimeoutSeconds: 120, AttemptNo: 1, MaxAttempts: 3,
		Payload: json.RawMessage(`{"target":"SITE_LOAD","subjectType":"SITE","subjectId":"01990000-5000-7000-8000-000000000001"}`),
	}
	request, err := ValidateScheduledForecastPlan(job)
	if err != nil {
		t.Fatal(err)
	}
	if request != (PreparationRequest{TenantID: job.TenantID, SiteID: job.SiteID, SubjectType: "SITE", SubjectID: job.SiteID, Target: "SITE_LOAD"}) {
		t.Fatalf("request=%#v", request)
	}

	job.Payload = json.RawMessage(`{"target":"SITE_LOAD","subjectType":"SITE","subjectId":"01990000-5000-7000-8000-000000000009"}`)
	if _, err := ValidateScheduledForecastPlan(job); err == nil {
		t.Fatal("a plan for another Site was accepted")
	}
	job.TriggerType = "MANUAL"
	job.Payload = json.RawMessage(`{"target":"SITE_LOAD","subjectType":"SITE","subjectId":"01990000-5000-7000-8000-000000000001"}`)
	if _, err := ValidateScheduledForecastPlan(job); err == nil {
		t.Fatal("a plan outside a schedule was accepted")
	}
}
