package metric

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

type fakeLifecycleGovernance struct {
	target     LifecycleTarget
	policy     LifecyclePolicy
	hold       bool
	archiveID  string
	archiveErr error
	deletion   LifecycleDeletion
	events     []string
	applyCalls int
}

func (f *fakeLifecycleGovernance) LoadLifecycleTarget(context.Context, SchedulerJob, LifecyclePayload) (LifecycleTarget, error) {
	return f.target, nil
}
func (f *fakeLifecycleGovernance) LoadLifecyclePolicy(context.Context, SchedulerJob, LifecyclePayload, time.Time) (LifecyclePolicy, error) {
	return f.policy, nil
}
func (f *fakeLifecycleGovernance) LegalHoldBlocks(context.Context, SchedulerJob, LifecyclePayload, time.Time) (bool, error) {
	return f.hold, nil
}
func (f *fakeLifecycleGovernance) VerifiedArchiveManifest(context.Context, SchedulerJob, LifecyclePayload) (string, error) {
	if f.archiveErr != nil {
		return "", f.archiveErr
	}
	return f.archiveID, nil
}
func (f *fakeLifecycleGovernance) EnsureRetentionDeletion(context.Context, SchedulerJob, LifecyclePayload, LifecycleTarget, LifecyclePolicy, string, time.Time) (LifecycleDeletion, error) {
	if f.deletion.ID == "" {
		f.deletion = LifecycleDeletion{ID: "deletion", Status: "APPROVED"}
	}
	return f.deletion, nil
}
func (f *fakeLifecycleGovernance) ApplyRetentionDeletion(context.Context, SchedulerJob, LifecyclePayload, LifecycleTarget, LifecycleDeletion, time.Time) (string, error) {
	f.applyCalls++
	return "tombstone", nil
}
func (f *fakeLifecycleGovernance) RecordLifecycleEvent(_ context.Context, _ SchedulerJob, _ LifecyclePayload, eventType string, _ map[string]any, _ time.Time) error {
	f.events = append(f.events, eventType)
	return nil
}

type fakeLifecycleData struct {
	deleteCalls int
	err         error
}

func (f *fakeLifecycleData) DeleteMetricResult(context.Context, string, string, string) error {
	f.deleteCalls++
	return f.err
}

func dueLifecycleFixture(archiveRequired bool) (*fakeLifecycleGovernance, SchedulerJob, LifecyclePayload, time.Time) {
	deleteDays := 30
	now := time.Date(2026, 8, 19, 8, 0, 0, 0, time.UTC)
	governance := &fakeLifecycleGovernance{
		target: LifecycleTarget{ResultID: "01910000-0000-7000-8000-000000000001", Revision: 4, CalculatedAt: now.AddDate(0, 0, -60)},
		policy: LifecyclePolicy{ID: "policy", DeleteAfterDays: &deleteDays, ArchiveRequired: archiveRequired},
	}
	job := SchedulerJob{JobID: "01910000-0000-7000-8000-000000000002", JobType: "DATA_RETENTION_SCAN", TenantID: "tenant", SiteID: "site", WorkerID: "worker", AttemptNo: 1, TimeoutSeconds: 60}
	payload := LifecyclePayload{DatasetCode: LifecycleMetricResultDataset, DataClass: "STANDARD", ResourceKey: governance.target.ResultID}
	return governance, job, payload, now
}

func TestLifecycleLegalHoldBlocksSourceDeletion(t *testing.T) {
	governance, job, payload, now := dueLifecycleFixture(false)
	governance.hold = true
	data := &fakeLifecycleData{}
	executor, err := NewLifecycleExecutor(governance, data)
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := executor.Execute(context.Background(), job, payload, now)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Status != "HOLD_BLOCKED" || data.deleteCalls != 0 || governance.applyCalls != 0 {
		t.Fatalf("outcome=%#v deleteCalls=%d applyCalls=%d", outcome, data.deleteCalls, governance.applyCalls)
	}
	if len(governance.events) != 2 || governance.events[0] != "CLAIMED" || governance.events[1] != "HOLD_BLOCKED" {
		t.Fatalf("events=%v", governance.events)
	}
}

func TestLifecycleCurrentResultCannotDeleteSource(t *testing.T) {
	governance, job, payload, now := dueLifecycleFixture(false)
	governance.target.IsCurrent = true
	data := &fakeLifecycleData{}
	executor, err := NewLifecycleExecutor(governance, data)
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := executor.Execute(context.Background(), job, payload, now)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Status != "CURRENT_BLOCKED" || data.deleteCalls != 0 || governance.applyCalls != 0 {
		t.Fatalf("outcome=%#v deleteCalls=%d applyCalls=%d", outcome, data.deleteCalls, governance.applyCalls)
	}
	if len(governance.events) != 2 || governance.events[1] != "CURRENT_BLOCKED" {
		t.Fatalf("events=%v", governance.events)
	}
}

func TestLifecycleArchiveFailureCannotDeleteSource(t *testing.T) {
	governance, job, payload, now := dueLifecycleFixture(true)
	governance.archiveErr = pgx.ErrNoRows
	data := &fakeLifecycleData{}
	executor, err := NewLifecycleExecutor(governance, data)
	if err != nil {
		t.Fatal(err)
	}
	_, err = executor.Execute(context.Background(), job, payload, now)
	if !errors.Is(err, ErrArchiveEvidenceRequired) {
		t.Fatalf("error=%v, want ErrArchiveEvidenceRequired", err)
	}
	if data.deleteCalls != 0 || governance.applyCalls != 0 {
		t.Fatalf("archive failure reached delete/apply: deleteCalls=%d applyCalls=%d", data.deleteCalls, governance.applyCalls)
	}
	if len(governance.events) != 2 || governance.events[1] != "ARCHIVE_FAILED" {
		t.Fatalf("events=%v", governance.events)
	}
}
