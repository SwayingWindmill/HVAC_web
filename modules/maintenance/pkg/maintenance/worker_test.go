package maintenance

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
)

type recordedJobFailure struct {
	job       Job
	code      string
	err       error
	retryable bool
}

type fakeJobStore struct {
	mu        sync.Mutex
	jobs      []Job
	panicID   string
	failed    []recordedJobFailure
	completed []Job
}

func (f *fakeJobStore) ClaimJobs(context.Context, string, int, time.Duration, time.Time) ([]Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	claimed := append([]Job(nil), f.jobs...)
	f.jobs = nil
	return claimed, nil
}

func (f *fakeJobStore) StartJob(context.Context, Job, time.Duration, time.Time) (bool, error) {
	return true, nil
}

func (f *fakeJobStore) ExecuteJob(_ context.Context, job Job, _ time.Time) (map[string]any, error) {
	if job.ID == f.panicID {
		panic("secret-maintenance-dsn")
	}
	return map[string]any{"ok": true}, nil
}

func (f *fakeJobStore) RenewJobLease(context.Context, Job, time.Duration, time.Time) (bool, error) {
	return false, nil
}

func (f *fakeJobStore) CancelJob(context.Context, Job, time.Time) error {
	return nil
}

func (f *fakeJobStore) CompleteJob(_ context.Context, job Job, _ map[string]any, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.completed = append(f.completed, job)
	return nil
}

func (f *fakeJobStore) FailJob(_ context.Context, job Job, code string, executionErr error, retryable bool, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failed = append(f.failed, recordedJobFailure{
		job:       job,
		code:      code,
		err:       executionErr,
		retryable: retryable,
	})
	return nil
}

func TestWorkerRecoversPanickingExecuteJobAndFailsWithoutRetry(t *testing.T) {
	var logBuffer bytes.Buffer
	logger := observability.NewJSONLogger(&logBuffer, slog.LevelInfo)
	store := &fakeJobStore{
		panicID: "job-panic-1",
		jobs: []Job{
			{ID: "job-panic-1", Type: "DEAD_WORK_DISPOSITION", AttemptNo: 1, MaxAttempts: 3, TimeoutSeconds: 5, WorkerID: "worker-1"},
			{ID: "job-ok-2", Type: "DEAD_WORK_DISPOSITION", AttemptNo: 1, MaxAttempts: 3, TimeoutSeconds: 5, WorkerID: "worker-1"},
		},
	}
	worker := &Worker{
		Store:         store,
		WorkerID:      "worker-1",
		Batch:         10,
		LeaseDuration: 5 * time.Second,
		Logger:        logger,
	}

	claimed, err := worker.Cycle(context.Background(), time.Now().UTC())
	if err != nil {
		t.Fatalf("Cycle returned error: %v", err)
	}
	if claimed != 2 {
		t.Fatalf("claimed = %d, want 2", claimed)
	}

	store.mu.Lock()
	failed := append([]recordedJobFailure(nil), store.failed...)
	completed := append([]Job(nil), store.completed...)
	store.mu.Unlock()

	if len(failed) != 1 {
		t.Fatalf("failed count = %d, want 1", len(failed))
	}
	if failed[0].job.ID != "job-panic-1" || failed[0].code != "MAINTENANCE_JOB_PANIC" || failed[0].retryable {
		t.Fatalf("unexpected FailJob call: %+v", failed[0])
	}
	if len(completed) != 1 || completed[0].ID != "job-ok-2" {
		t.Fatalf("expected subsequent job to complete, got %+v", completed)
	}
	logOutput := logBuffer.String()
	if strings.Contains(logOutput, "secret-maintenance-dsn") || !strings.Contains(logOutput, `"error":"[REDACTED]"`) || !strings.Contains(logOutput, `"component":"maintenance.execute_job"`) {
		t.Fatalf("unexpected log output: %s", logOutput)
	}
}

type blockingLeaseJobStore struct {
	fakeJobStore
	renewErr      error
	executeExited atomic.Bool
}

func (b *blockingLeaseJobStore) ExecuteJob(ctx context.Context, job Job, now time.Time) (map[string]any, error) {
	defer b.executeExited.Store(true)
	<-ctx.Done()
	time.Sleep(30 * time.Millisecond)
	return nil, ctx.Err()
}

func (b *blockingLeaseJobStore) RenewJobLease(context.Context, Job, time.Duration, time.Time) (bool, error) {
	if b.renewErr != nil {
		return false, b.renewErr
	}
	return false, nil
}

func TestWorkerWaitsForExecuteJobExitOnLeaseLossAndTimeout(t *testing.T) {
	t.Run("lease renewal failure waits for goroutine exit", func(t *testing.T) {
		var logBuffer bytes.Buffer
		logger := observability.NewJSONLogger(&logBuffer, slog.LevelInfo)
		store := &blockingLeaseJobStore{
			renewErr: errors.New("lease expired"),
			fakeJobStore: fakeJobStore{
				jobs: []Job{{ID: "job-lease-1", Type: "DEAD_WORK_DISPOSITION", AttemptNo: 1, MaxAttempts: 3, TimeoutSeconds: 10, WorkerID: "worker-1"}},
			},
		}
		worker := &Worker{
			Store:         store,
			WorkerID:      "worker-1",
			Batch:         1,
			LeaseDuration: time.Second,
			Logger:        logger,
		}
		if _, err := worker.Cycle(context.Background(), time.Now().UTC()); err != nil {
			t.Fatalf("Cycle returned error: %v", err)
		}
		if !store.executeExited.Load() {
			t.Fatal("Worker.Cycle returned before ExecuteJob goroutine exited on lease loss")
		}
	})

	t.Run("execution timeout waits for goroutine exit", func(t *testing.T) {
		var logBuffer bytes.Buffer
		logger := observability.NewJSONLogger(&logBuffer, slog.LevelInfo)
		store := &blockingLeaseJobStore{
			fakeJobStore: fakeJobStore{
				jobs: []Job{{ID: "job-timeout-1", Type: "DEAD_WORK_DISPOSITION", AttemptNo: 1, MaxAttempts: 3, TimeoutSeconds: 1, WorkerID: "worker-1"}},
			},
		}
		worker := &Worker{
			Store:         store,
			WorkerID:      "worker-1",
			Batch:         1,
			LeaseDuration: 10 * time.Second,
			Logger:        logger,
		}
		if _, err := worker.Cycle(context.Background(), time.Now().UTC()); err != nil {
			t.Fatalf("Cycle returned error: %v", err)
		}
		if !store.executeExited.Load() {
			t.Fatal("Worker.Cycle returned before ExecuteJob goroutine exited on timeout")
		}
	})
}
