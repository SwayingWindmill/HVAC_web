package forecast

import (
	"encoding/json"
	"testing"
)

func TestForecastSchedulerPayloadContainsOnlyServerOwnedIdentity(t *testing.T) {
	job := SchedulerJob{
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
