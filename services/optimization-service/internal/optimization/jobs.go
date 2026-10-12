package optimization

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/quanlaihe/hvac-web/libs/schedulerjob"
)

// RunJobType is the Scheduler job that runs one Optimization against its sealed input snapshot.
const RunJobType = "OPTIMIZATION_RUN"

func (store *PostgresStore) RunQueue() (*schedulerjob.Queue, error) {
	return schedulerjob.NewQueue(store.pool, RunJobType)
}

type SchedulerOptimizationReference struct {
	OptimizationRunID string `json:"optimizationRunId"`
	InputSnapshotID   string `json:"inputSnapshotId"`
}

func ValidateOptimizationSchedulerJob(job schedulerjob.Job) (SchedulerOptimizationReference, error) {
	if job.JobType != RunJobType || !uuidPattern.MatchString(job.JobID) || !uuidPattern.MatchString(job.TenantID) || !uuidPattern.MatchString(job.SiteID) || job.TimeoutSeconds <= 0 || job.AttemptNo <= 0 || job.MaxAttempts <= 0 {
		return SchedulerOptimizationReference{}, errors.New("optimization scheduler job envelope is invalid")
	}
	var reference SchedulerOptimizationReference
	decoder := json.NewDecoder(bytes.NewReader(job.Payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&reference); err != nil {
		return SchedulerOptimizationReference{}, fmt.Errorf("decode OPTIMIZATION_RUN reference: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return SchedulerOptimizationReference{}, err
	}
	if reference.OptimizationRunID != job.JobID || !uuidPattern.MatchString(reference.InputSnapshotID) {
		return SchedulerOptimizationReference{}, errors.New("OPTIMIZATION_RUN reference must identify the server-created Optimization run and SEALED input snapshot")
	}
	return reference, nil
}
