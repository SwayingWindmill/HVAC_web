package forecast

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/quanlaihe/hvac-web/libs/schedulerjob"
)

// RunJobType is the Scheduler job that runs, or for a schedule prepares, one Forecast.
const RunJobType = "FORECAST_RUN"

func (store *PostgresStore) RunQueue() (*schedulerjob.Queue, error) {
	return schedulerjob.NewQueue(store.pool, RunJobType)
}

type SchedulerForecastReference struct {
	ForecastJobID      string `json:"forecastJobId"`
	ForecastSnapshotID string `json:"forecastSnapshotId"`
}

func ValidateForecastSchedulerJob(job schedulerjob.Job) (SchedulerForecastReference, error) {
	if job.JobType != RunJobType || !uuidPattern.MatchString(job.JobID) || !uuidPattern.MatchString(job.TenantID) || !uuidPattern.MatchString(job.SiteID) || job.TimeoutSeconds <= 0 || job.AttemptNo <= 0 || job.MaxAttempts <= 0 {
		return SchedulerForecastReference{}, errors.New("forecast scheduler job envelope is invalid")
	}
	var reference SchedulerForecastReference
	decoder := json.NewDecoder(bytes.NewReader(job.Payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&reference); err != nil {
		return SchedulerForecastReference{}, fmt.Errorf("decode FORECAST_RUN reference: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return SchedulerForecastReference{}, err
	}
	if reference.ForecastJobID != job.JobID || !uuidPattern.MatchString(reference.ForecastSnapshotID) {
		return SchedulerForecastReference{}, errors.New("FORECAST_RUN reference must identify the server-created Forecast job and result snapshot")
	}
	return reference, nil
}

// ScheduledForecastPlan is the payload of a FORECAST_RUN that a schedule fires: it names what to
// forecast, and the worker prepares a fresh Forecast from the latest metric history.
type ScheduledForecastPlan struct {
	Target      string `json:"target"`
	SubjectType string `json:"subjectType"`
	SubjectID   string `json:"subjectId"`
}

func ValidateScheduledForecastPlan(job schedulerjob.Job) (PreparationRequest, error) {
	if job.JobType != RunJobType || job.TriggerType != "SCHEDULE" {
		return PreparationRequest{}, errors.New("a Forecast plan only comes from a FORECAST_RUN schedule")
	}
	var plan ScheduledForecastPlan
	decoder := json.NewDecoder(bytes.NewReader(job.Payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&plan); err != nil {
		return PreparationRequest{}, fmt.Errorf("decode scheduled Forecast plan: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return PreparationRequest{}, err
	}
	request := PreparationRequest{TenantID: job.TenantID, SiteID: job.SiteID, SubjectType: plan.SubjectType, SubjectID: plan.SubjectID, Target: plan.Target}
	if err := request.Validate(); err != nil {
		return PreparationRequest{}, fmt.Errorf("scheduled Forecast plan: %w", err)
	}
	return request, nil
}
