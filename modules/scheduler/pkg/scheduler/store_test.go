package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCycleIsIdempotentAcrossRepeatedAndConcurrentReplicas(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 5, 123456789, time.UTC)
	scheduleID := "01910000-1000-7000-8000-000000000001"
	dueAt := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)

	db := newFakeSchedulerDB()
	db.addSchedule(fakeScheduleRow{
		ID:                scheduleID,
		Code:              "metric-hourly",
		Name:              "Hourly metric calc",
		TenantID:          "01910000-0000-7000-8000-000000000001",
		SiteID:            "01910000-0000-7000-8000-000000000002",
		JobType:           "METRIC_WINDOW_CALC",
		ScheduleType:      "INTERVAL",
		IntervalSeconds:   3600,
		Timezone:          "UTC",
		MisfirePolicy:     "FIRE_ONCE",
		ConcurrencyPolicy: "FORBID",
		TimeoutSeconds:    60,
		MaxAttempts:       3,
		RetryPolicy:       json.RawMessage(`{}`),
		PayloadTemplate:   json.RawMessage(`{"window":"1h"}`),
		Enabled:           true,
		NextFireAt:        dueAt,
		Version:           1,
	})

	store, err := newStoreWithDB(db)
	if err != nil {
		t.Fatal(err)
	}

	const replicas = 8
	start := make(chan struct{})
	results := make(chan ScanResult, replicas)
	errs := make(chan error, replicas)

	var wg sync.WaitGroup
	for i := 0; i < replicas; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			res, cycleErr := store.Cycle(context.Background(), now, 100, time.Minute)
			if cycleErr != nil {
				errs <- cycleErr
				return
			}
			results <- res
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	close(results)

	for cycleErr := range errs {
		t.Fatalf("concurrent Cycle failed: %v", cycleErr)
	}
	totalCreated := 0
	for res := range results {
		totalCreated += res.Created
	}
	if totalCreated != 1 {
		t.Fatalf("totalCreated across %d concurrent replicas = %d, want 1", replicas, totalCreated)
	}

	// Subsequent cycles at the same or overlapping timestamps must create zero duplicates.
	repeat, err := store.Cycle(context.Background(), now.Add(500*time.Millisecond), 100, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if repeat.Created != 0 {
		t.Fatalf("repeat Cycle created %d duplicate jobs", repeat.Created)
	}

	jobs := db.jobsSnapshot()
	if len(jobs) != 1 {
		t.Fatalf("expected exactly 1 JobInstance, got %d", len(jobs))
	}
	if jobs[0].ScheduleID != scheduleID || !jobs[0].ScheduledFor.Equal(dueAt) || jobs[0].State != "READY" {
		t.Fatalf("unexpected JobInstance: %+v", jobs[0])
	}
}

func TestCreateScheduledJobDeduplicatesSameSlotAndProtectsReplacePolicy(t *testing.T) {
	now := time.Date(2026, 8, 15, 13, 0, 5, 999999999, time.UTC)
	slot := time.Date(2026, 8, 15, 13, 0, 0, 123456789, time.UTC)
	truncatedSlot := slot.Truncate(time.Microsecond)
	scheduleID := "01910000-1000-7000-8000-000000000002"

	db := newFakeSchedulerDB()
	sched := fakeScheduleRow{
		ID:                scheduleID,
		Code:              "replace-policy",
		Name:              "Replace policy schedule",
		TenantID:          "01910000-0000-7000-8000-000000000001",
		SiteID:            "01910000-0000-7000-8000-000000000002",
		JobType:           "FORECAST_RUN",
		ScheduleType:      "INTERVAL",
		IntervalSeconds:   300,
		Timezone:          "UTC",
		MisfirePolicy:     "FIRE_ONCE",
		ConcurrencyPolicy: "REPLACE",
		TimeoutSeconds:    60,
		MaxAttempts:       3,
		RetryPolicy:       json.RawMessage(`{}`),
		PayloadTemplate:   json.RawMessage(`{}`),
		Enabled:           true,
		NextFireAt:        truncatedSlot,
		Version:           1,
	}
	db.addSchedule(sched)
	// Existing job for the same (schedule_id, scheduled_for) with a non-canonical dedup_key.
	db.addJob(fakeJobRow{
		JobID:           "01910000-2000-7000-8000-000000000001",
		ScheduleID:      scheduleID,
		JobType:         "FORECAST_RUN",
		ScheduledFor:    truncatedSlot,
		DedupKey:        "manual-seed-existing-slot",
		State:           "RUNNING",
		CancelRequested: false,
		MaxAttempts:     3,
	})

	tx, err := db.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	created, skipped, err := createScheduledJob(context.Background(), tx, sched.toSchedule(), slot, now)
	if err != nil {
		t.Fatal(err)
	}
	if created || skipped {
		t.Fatalf("duplicate (schedule_id, scheduled_for) created=%v skipped=%v, want false/false", created, skipped)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}

	jobs := db.jobsSnapshot()
	if len(jobs) != 1 {
		t.Fatalf("expected 1 JobInstance, got %d", len(jobs))
	}
	if jobs[0].CancelRequested {
		t.Fatal("REPLACE policy erroneously requested cancellation when slot was already scheduled")
	}
}

func TestCreateScheduledJobConcurrentDirectCallsDeduplicateOnConflict(t *testing.T) {
	now := time.Date(2026, 8, 15, 14, 0, 5, 123456789, time.UTC)
	slot := time.Date(2026, 8, 15, 14, 0, 0, 654321987, time.UTC)
	scheduleID := "01910000-1000-7000-8000-000000000003"

	db := newFakeSchedulerDB()
	sched := Schedule{
		ID:                scheduleID,
		Code:              "direct-concurrent",
		Name:              "Direct concurrent",
		TenantID:          "01910000-0000-7000-8000-000000000001",
		SiteID:            "01910000-0000-7000-8000-000000000002",
		JobType:           "REPORT_GENERATE",
		ScheduleType:      "INTERVAL",
		IntervalSeconds:   60,
		Timezone:          "UTC",
		MisfirePolicy:     "FIRE_ONCE",
		ConcurrencyPolicy: "ALLOW",
		TimeoutSeconds:    60,
		MaxAttempts:       3,
		RetryPolicy:       json.RawMessage(`{}`),
		PayloadTemplate:   json.RawMessage(`{}`),
		NextFireAt:        slot,
	}

	const workers = 6
	start := make(chan struct{})
	createdCh := make(chan bool, workers)
	errCh := make(chan error, workers)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			tx, err := db.Begin(context.Background())
			if err != nil {
				errCh <- err
				return
			}
			defer tx.Rollback(context.Background())
			created, _, err := createScheduledJob(context.Background(), tx, sched, slot, now)
			if err != nil {
				errCh <- err
				return
			}
			if err := tx.Commit(context.Background()); err != nil {
				errCh <- err
				return
			}
			createdCh <- created
		}()
	}
	close(start)
	wg.Wait()
	close(errCh)
	close(createdCh)

	for err := range errCh {
		t.Fatalf("concurrent createScheduledJob failed: %v", err)
	}
	createdCount := 0
	for created := range createdCh {
		if created {
			createdCount++
		}
	}
	if createdCount != 1 {
		t.Fatalf("createdCount across %d concurrent createScheduledJob calls = %d, want 1", workers, createdCount)
	}
	if jobs := db.jobsSnapshot(); len(jobs) != 1 {
		t.Fatalf("expected 1 persisted JobInstance, got %d", len(jobs))
	}
}

func TestRecoverExpiredLeasesConcurrentReplicasRecoverOnce(t *testing.T) {
	now := time.Date(2026, 8, 15, 15, 0, 0, 0, time.UTC)
	expiredJobID := "01910000-2000-7000-8000-000000000010"
	activeJobID := "01910000-2000-7000-8000-000000000011"

	db := newFakeSchedulerDB()
	expiredLease := now.Add(-10 * time.Second)
	validLease := now.Add(30 * time.Second)
	db.addJob(fakeJobRow{
		JobID:        expiredJobID,
		JobType:      "METRIC_WINDOW_CALC",
		DedupKey:     "expired-lease-job",
		State:        "RUNNING",
		AttemptCount: 1,
		MaxAttempts:  3,
		LeaseOwner:   "worker-dead",
		LeaseUntil:   &expiredLease,
		ScheduledFor: now.Add(-time.Minute),
	})
	db.addAttempt(fakeAttemptRow{
		JobID:     expiredJobID,
		AttemptNo: 1,
		StartedAt: now.Add(-40 * time.Second),
	})
	db.addJob(fakeJobRow{
		JobID:        activeJobID,
		JobType:      "METRIC_WINDOW_CALC",
		DedupKey:     "active-lease-job",
		State:        "RUNNING",
		AttemptCount: 1,
		MaxAttempts:  3,
		LeaseOwner:   "worker-live",
		LeaseUntil:   &validLease,
		ScheduledFor: now.Add(-time.Minute),
	})

	store, err := newStoreWithDB(db)
	if err != nil {
		t.Fatal(err)
	}

	const replicas = 6
	start := make(chan struct{})
	recoveredCh := make(chan int, replicas)
	errCh := make(chan error, replicas)
	var wg sync.WaitGroup
	for i := 0; i < replicas; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			res, err := store.Cycle(context.Background(), now, 100, time.Minute)
			if err != nil {
				errCh <- err
				return
			}
			recoveredCh <- res.Recovered
		}()
	}
	close(start)
	wg.Wait()
	close(errCh)
	close(recoveredCh)

	for err := range errCh {
		t.Fatalf("concurrent lease recovery failed: %v", err)
	}
	totalRecovered := 0
	for count := range recoveredCh {
		totalRecovered += count
	}
	if totalRecovered != 1 {
		t.Fatalf("totalRecovered = %d, want 1", totalRecovered)
	}
	expired := db.jobByID(expiredJobID)
	if expired.State != "RETRY_WAIT" || expired.LeaseOwner != "" || expired.LeaseUntil != nil || expired.ErrorCode != "LEASE_EXPIRED" {
		t.Fatalf("unexpected recovered job state: %+v", expired)
	}
	active := db.jobByID(activeJobID)
	if active.State != "RUNNING" || active.LeaseOwner != "worker-live" {
		t.Fatalf("active job lease was disturbed: %+v", active)
	}
	attempt := db.attemptByKey(expiredJobID, 1)
	if attempt.CompletedAt == nil || attempt.ResultStatus != "RETRY_WAIT" {
		t.Fatalf("attempt row not finalized cleanly: %+v", attempt)
	}
}

func TestPromotePendingJobsSerializesForbidSchedules(t *testing.T) {
	now := time.Date(2026, 8, 15, 16, 0, 0, 0, time.UTC)
	scheduleID := "01910000-1000-7000-8000-000000000004"

	db := newFakeSchedulerDB()
	db.addSchedule(fakeScheduleRow{
		ID:                scheduleID,
		Code:              "forbid-schedule",
		Name:              "Forbid overlapping",
		JobType:           "METRIC_WINDOW_CALC",
		ScheduleType:      "INTERVAL",
		IntervalSeconds:   60,
		Timezone:          "UTC",
		MisfirePolicy:     "FIRE_ONCE",
		ConcurrencyPolicy: "FORBID",
		TimeoutSeconds:    60,
		MaxAttempts:       3,
		RetryPolicy:       json.RawMessage(`{}`),
		PayloadTemplate:   json.RawMessage(`{}`),
		Enabled:           true,
		NextFireAt:        now.Add(5 * time.Minute),
		Version:           1,
	})
	db.addJob(fakeJobRow{
		JobID:        "01910000-2000-7000-8000-000000000020",
		ScheduleID:   scheduleID,
		JobType:      "METRIC_WINDOW_CALC",
		DedupKey:     "forbid-1",
		State:        "PENDING",
		ScheduledFor: now.Add(-2 * time.Minute),
	})
	db.addJob(fakeJobRow{
		JobID:        "01910000-2000-7000-8000-000000000021",
		ScheduleID:   scheduleID,
		JobType:      "METRIC_WINDOW_CALC",
		DedupKey:     "forbid-2",
		State:        "PENDING",
		ScheduledFor: now.Add(-time.Minute),
	})

	store, err := newStoreWithDB(db)
	if err != nil {
		t.Fatal(err)
	}

	res, err := store.Cycle(context.Background(), now, 100, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if res.Promoted != 1 {
		t.Fatalf("first Cycle promoted=%d, want 1", res.Promoted)
	}
	if db.jobByID("01910000-2000-7000-8000-000000000020").State != "READY" {
		t.Fatal("earliest pending job should be READY")
	}
	if db.jobByID("01910000-2000-7000-8000-000000000021").State != "PENDING" {
		t.Fatal("second pending job must stay PENDING while FORBID active job exists")
	}
}

func TestPostgresSchedulerCycleConcurrentIdempotency(t *testing.T) {
	databaseURL := os.Getenv("SCHEDULER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("Scheduler PostgreSQL integration environment is not configured")
	}
	ctx := t.Context()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	store, err := NewStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Cycle(ctx, time.Now().UTC(), 100, time.Minute); err != nil {
		t.Fatal(err)
	}
}

type fakeScheduleRow struct {
	ID                string
	Code              string
	Name              string
	TenantID          string
	SiteID            string
	JobType           string
	ScheduleType      string
	CronExpression    string
	CronFormatVersion string
	IntervalSeconds   int64
	Timezone          string
	MisfirePolicy     string
	CatchUpLimit      int
	ConcurrencyPolicy string
	TimeoutSeconds    int
	MaxAttempts       int
	RetryPolicy       json.RawMessage
	PayloadTemplate   json.RawMessage
	Enabled           bool
	NextFireAt        time.Time
	LastFireAt        *time.Time
	Version           int64
}

func (row fakeScheduleRow) toSchedule() Schedule {
	return Schedule{
		ID:                row.ID,
		Code:              row.Code,
		Name:              row.Name,
		TenantID:          row.TenantID,
		SiteID:            row.SiteID,
		JobType:           row.JobType,
		ScheduleType:      row.ScheduleType,
		CronExpression:    row.CronExpression,
		CronFormatVersion: row.CronFormatVersion,
		IntervalSeconds:   row.IntervalSeconds,
		Timezone:          row.Timezone,
		MisfirePolicy:     row.MisfirePolicy,
		CatchUpLimit:      row.CatchUpLimit,
		ConcurrencyPolicy: row.ConcurrencyPolicy,
		TimeoutSeconds:    row.TimeoutSeconds,
		MaxAttempts:       row.MaxAttempts,
		RetryPolicy:       append(json.RawMessage(nil), row.RetryPolicy...),
		PayloadTemplate:   append(json.RawMessage(nil), row.PayloadTemplate...),
		NextFireAt:        row.NextFireAt,
	}
}

type fakeJobRow struct {
	JobID           string
	ScheduleID      string
	JobType         string
	TenantID        string
	SiteID          string
	ScheduledFor    time.Time
	Timezone        string
	Priority        int
	DedupKey        string
	Payload         json.RawMessage
	State           string
	AttemptCount    int
	MaxAttempts     int
	LeaseOwner      string
	LeaseUntil      *time.Time
	NextRetryAt     *time.Time
	TimeoutSeconds  int
	CancelRequested bool
	CompletedAt     *time.Time
	ErrorCode       string
	ErrorMessage    string
	TraceID         string
}

type fakeAttemptRow struct {
	JobID        string
	AttemptNo    int
	StartedAt    time.Time
	CompletedAt  *time.Time
	ResultStatus string
	ErrorCode    string
	ErrorMessage string
}

type fakeSchedulerDB struct {
	mu            sync.Mutex
	cond          *sync.Cond
	schedules     map[string]fakeScheduleRow
	jobs          map[string]fakeJobRow
	attempts      map[string]fakeAttemptRow
	rowLocks      map[string]*fakeTx
	advisoryLocks map[string]*fakeTx
}

func newFakeSchedulerDB() *fakeSchedulerDB {
	db := &fakeSchedulerDB{
		schedules:     map[string]fakeScheduleRow{},
		jobs:          map[string]fakeJobRow{},
		attempts:      map[string]fakeAttemptRow{},
		rowLocks:      map[string]*fakeTx{},
		advisoryLocks: map[string]*fakeTx{},
	}
	db.cond = sync.NewCond(&db.mu)
	return db
}

func (db *fakeSchedulerDB) addSchedule(row fakeScheduleRow) {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.schedules[row.ID] = row
}

func (db *fakeSchedulerDB) addJob(row fakeJobRow) {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.jobs[row.JobID] = row
}

func (db *fakeSchedulerDB) addAttempt(row fakeAttemptRow) {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.attempts[fmt.Sprintf("%s:%d", row.JobID, row.AttemptNo)] = row
}

func (db *fakeSchedulerDB) jobsSnapshot() []fakeJobRow {
	db.mu.Lock()
	defer db.mu.Unlock()
	out := make([]fakeJobRow, 0, len(db.jobs))
	for _, job := range db.jobs {
		out = append(out, job)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].JobID < out[j].JobID })
	return out
}

func (db *fakeSchedulerDB) jobByID(id string) fakeJobRow {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.jobs[id]
}

func (db *fakeSchedulerDB) attemptByKey(jobID string, attemptNo int) fakeAttemptRow {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.attempts[fmt.Sprintf("%s:%d", jobID, attemptNo)]
}

func (db *fakeSchedulerDB) Begin(_ context.Context) (pgx.Tx, error) {
	return &fakeTx{
		db:              db,
		stagedSchedules: map[string]fakeScheduleRow{},
		stagedJobs:      map[string]fakeJobRow{},
		stagedAttempts:  map[string]fakeAttemptRow{},
	}, nil
}

func (db *fakeSchedulerDB) QueryRow(context.Context, string, ...any) pgx.Row {
	return fakeRow{err: errors.New("unexpected pool QueryRow")}
}

func (db *fakeSchedulerDB) Ping(context.Context) error { return nil }
func (db *fakeSchedulerDB) Close()                     {}

type fakeTx struct {
	db              *fakeSchedulerDB
	closed          bool
	stagedSchedules map[string]fakeScheduleRow
	stagedJobs      map[string]fakeJobRow
	stagedAttempts  map[string]fakeAttemptRow
}

func (tx *fakeTx) Begin(context.Context) (pgx.Tx, error) {
	return nil, errors.New("nested tx unsupported")
}
func (tx *fakeTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, errors.New("CopyFrom unsupported")
}
func (tx *fakeTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults { return nil }
func (tx *fakeTx) LargeObjects() pgx.LargeObjects                         { return pgx.LargeObjects{} }
func (tx *fakeTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, errors.New("Prepare unsupported")
}
func (tx *fakeTx) Conn() *pgx.Conn { return nil }

func (tx *fakeTx) Commit(context.Context) error {
	tx.db.mu.Lock()
	defer tx.db.mu.Unlock()
	if tx.closed {
		return pgx.ErrTxClosed
	}
	for id, row := range tx.stagedJobs {
		for existingID, existing := range tx.db.jobs {
			if existingID != id && existing.DedupKey == row.DedupKey {
				tx.releaseLocksLocked()
				tx.closed = true
				return errors.New("duplicate dedup_key")
			}
		}
	}
	for id, row := range tx.stagedSchedules {
		tx.db.schedules[id] = row
	}
	for id, row := range tx.stagedJobs {
		tx.db.jobs[id] = row
	}
	for key, row := range tx.stagedAttempts {
		tx.db.attempts[key] = row
	}
	tx.releaseLocksLocked()
	tx.closed = true
	return nil
}

func (tx *fakeTx) Rollback(context.Context) error {
	tx.db.mu.Lock()
	defer tx.db.mu.Unlock()
	if tx.closed {
		return nil
	}
	tx.releaseLocksLocked()
	tx.closed = true
	return nil
}

func (tx *fakeTx) releaseLocksLocked() {
	for key, owner := range tx.db.rowLocks {
		if owner == tx {
			delete(tx.db.rowLocks, key)
		}
	}
	for key, owner := range tx.db.advisoryLocks {
		if owner == tx {
			delete(tx.db.advisoryLocks, key)
		}
	}
	tx.db.cond.Broadcast()
}

func (tx *fakeTx) viewSchedulesLocked() map[string]fakeScheduleRow {
	merged := make(map[string]fakeScheduleRow, len(tx.db.schedules)+len(tx.stagedSchedules))
	for k, v := range tx.db.schedules {
		merged[k] = v
	}
	for k, v := range tx.stagedSchedules {
		merged[k] = v
	}
	return merged
}

func (tx *fakeTx) viewJobsLocked() map[string]fakeJobRow {
	merged := make(map[string]fakeJobRow, len(tx.db.jobs)+len(tx.stagedJobs))
	for k, v := range tx.db.jobs {
		merged[k] = v
	}
	for k, v := range tx.stagedJobs {
		merged[k] = v
	}
	return merged
}

func (tx *fakeTx) viewAttemptsLocked() map[string]fakeAttemptRow {
	merged := make(map[string]fakeAttemptRow, len(tx.db.attempts)+len(tx.stagedAttempts))
	for k, v := range tx.db.attempts {
		merged[k] = v
	}
	for k, v := range tx.stagedAttempts {
		merged[k] = v
	}
	return merged
}

func (tx *fakeTx) tryLockLocked(key string) bool {
	if owner, locked := tx.db.rowLocks[key]; locked && owner != tx {
		return false
	}
	tx.db.rowLocks[key] = tx
	return true
}

func (tx *fakeTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return fakeRow{err: err}
	}
	defer rows.Close()
	if !rows.Next() {
		return fakeRow{err: pgx.ErrNoRows}
	}
	vals, err := rows.Values()
	return fakeRow{vals: vals, err: err}
}

func (tx *fakeTx) Query(_ context.Context, rawSQL string, args ...any) (pgx.Rows, error) {
	sql := normalizeFakeSQL(rawSQL)
	tx.db.mu.Lock()
	defer tx.db.mu.Unlock()

	switch {
	case strings.Contains(sql, "FROM core_registry.job_instances") && strings.Contains(sql, "cancel_requested=true"):
		limit := args[0].(int)
		jobs := tx.viewJobsLocked()
		var ids []string
		for _, job := range jobs {
			if job.CancelRequested && isOneOf(job.State, "PENDING", "READY", "RETRY_WAIT") {
				ids = append(ids, job.JobID)
			}
		}
		sort.Strings(ids)
		var out [][]any
		for _, id := range ids {
			if len(out) >= limit {
				break
			}
			if !tx.tryLockLocked("job:" + id) {
				continue
			}
			out = append(out, []any{id})
		}
		return &fakeRows{rows: out}, nil

	case strings.Contains(sql, "FROM core_registry.job_instances") && strings.Contains(sql, "state='RETRY_WAIT'"):
		now := args[0].(time.Time)
		limit := args[1].(int)
		jobs := tx.viewJobsLocked()
		var ids []string
		for _, job := range jobs {
			if job.State == "RETRY_WAIT" && !job.CancelRequested && job.NextRetryAt != nil && !job.NextRetryAt.After(now) {
				ids = append(ids, job.JobID)
			}
		}
		sort.Strings(ids)
		var out [][]any
		for _, id := range ids {
			if len(out) >= limit {
				break
			}
			if !tx.tryLockLocked("job:" + id) {
				continue
			}
			out = append(out, []any{id})
		}
		return &fakeRows{rows: out}, nil

	case strings.Contains(sql, "FROM core_registry.job_instances") && strings.Contains(sql, "lease_until < $1"):
		now := args[0].(time.Time)
		limit := args[1].(int)
		jobs := tx.viewJobsLocked()
		var matched []fakeJobRow
		for _, job := range jobs {
			if isOneOf(job.State, "CLAIMED", "RUNNING") && job.LeaseUntil != nil && job.LeaseUntil.Before(now) {
				matched = append(matched, job)
			}
		}
		sort.Slice(matched, func(i, j int) bool { return matched[i].JobID < matched[j].JobID })
		var out [][]any
		for _, job := range matched {
			if len(out) >= limit {
				break
			}
			if !tx.tryLockLocked("job:" + job.JobID) {
				continue
			}
			out = append(out, []any{job.JobID, job.JobType, job.AttemptCount, job.MaxAttempts, job.CancelRequested})
		}
		return &fakeRows{rows: out}, nil

	case strings.Contains(sql, "FROM core_registry.job_instances j JOIN core_registry.schedule_definitions s"):
		if !strings.Contains(sql, "FOR UPDATE OF j, s SKIP LOCKED") {
			return nil, fmt.Errorf("promotePendingJobs must lock both j and s: %s", sql)
		}
		limit := args[0].(int)
		jobs := tx.viewJobsLocked()
		schedules := tx.viewSchedulesLocked()
		var matched []fakeJobRow
		for _, job := range jobs {
			s, ok := schedules[job.ScheduleID]
			if !ok || job.State != "PENDING" || job.CancelRequested || s.ConcurrencyPolicy != "FORBID" {
				continue
			}
			hasActive := false
			hasEarlier := false
			for _, other := range jobs {
				if other.ScheduleID != job.ScheduleID {
					continue
				}
				if other.JobID != job.JobID && isOneOf(other.State, "READY", "CLAIMED", "RUNNING", "RETRY_WAIT") {
					hasActive = true
				}
				if other.State == "PENDING" && !other.CancelRequested &&
					(other.ScheduledFor.Before(job.ScheduledFor) || (other.ScheduledFor.Equal(job.ScheduledFor) && other.JobID < job.JobID)) {
					hasEarlier = true
				}
			}
			if !hasActive && !hasEarlier {
				matched = append(matched, job)
			}
		}
		sort.Slice(matched, func(i, j int) bool { return matched[i].ScheduledFor.Before(matched[j].ScheduledFor) })
		var out [][]any
		for _, job := range matched {
			if len(out) >= limit {
				break
			}
			if owner, locked := tx.db.rowLocks["schedule:"+job.ScheduleID]; locked && owner != tx {
				continue
			}
			if owner, locked := tx.db.rowLocks["job:"+job.JobID]; locked && owner != tx {
				continue
			}
			tx.db.rowLocks["schedule:"+job.ScheduleID] = tx
			tx.db.rowLocks["job:"+job.JobID] = tx
			out = append(out, []any{job.JobID})
		}
		return &fakeRows{rows: out}, nil

	case strings.Contains(sql, "FROM core_registry.schedule_definitions WHERE enabled=true"):
		now := args[0].(time.Time)
		limit := args[1].(int)
		schedules := tx.viewSchedulesLocked()
		var matched []fakeScheduleRow
		for _, s := range schedules {
			if s.Enabled && !s.NextFireAt.IsZero() && !s.NextFireAt.After(now) {
				matched = append(matched, s)
			}
		}
		sort.Slice(matched, func(i, j int) bool { return matched[i].ID < matched[j].ID })
		var out [][]any
		for _, s := range matched {
			if len(out) >= limit {
				break
			}
			if !tx.tryLockLocked("schedule:" + s.ID) {
				continue
			}
			out = append(out, []any{
				s.ID, s.Code, s.Name, s.TenantID, s.SiteID, s.JobType, s.ScheduleType,
				s.CronExpression, s.CronFormatVersion, s.IntervalSeconds, s.Timezone, s.MisfirePolicy, s.CatchUpLimit,
				s.ConcurrencyPolicy, s.TimeoutSeconds, s.MaxAttempts, s.RetryPolicy, s.PayloadTemplate, s.NextFireAt,
			})
		}
		return &fakeRows{rows: out}, nil

	case strings.Contains(sql, "SELECT EXISTS") && strings.Contains(sql, "scheduled_for=$2"):
		scheduleID := args[0].(string)
		scheduledFor := args[1].(time.Time)
		jobs := tx.viewJobsLocked()
		exists := false
		for _, job := range jobs {
			if job.ScheduleID == scheduleID && job.ScheduledFor.Equal(scheduledFor) {
				exists = true
				break
			}
		}
		return &fakeRows{rows: [][]any{{exists}}}, nil

	case strings.Contains(sql, "SELECT EXISTS") && strings.Contains(sql, "state IN ('PENDING','READY','CLAIMED','RUNNING','RETRY_WAIT')"):
		scheduleID := args[0].(string)
		jobs := tx.viewJobsLocked()
		active := false
		for _, job := range jobs {
			if job.ScheduleID == scheduleID && isOneOf(job.State, "PENDING", "READY", "CLAIMED", "RUNNING", "RETRY_WAIT") {
				active = true
				break
			}
		}
		return &fakeRows{rows: [][]any{{active}}}, nil

	default:
		return nil, fmt.Errorf("unexpected fakeTx Query: %s", sql)
	}
}

func (tx *fakeTx) Exec(_ context.Context, rawSQL string, args ...any) (pgconn.CommandTag, error) {
	sql := normalizeFakeSQL(rawSQL)
	tx.db.mu.Lock()
	defer tx.db.mu.Unlock()

	switch {
	case strings.HasPrefix(sql, "SELECT pg_advisory_xact_lock"):
		lockKey := args[0].(string)
		for {
			owner, locked := tx.db.advisoryLocks[lockKey]
			if !locked || owner == tx {
				tx.db.advisoryLocks[lockKey] = tx
				return pgconn.NewCommandTag("SELECT 1"), nil
			}
			tx.db.cond.Wait()
		}

	case strings.HasPrefix(sql, "INSERT INTO core_registry.job_instances"):
		if !strings.Contains(sql, "ON CONFLICT (dedup_key) DO NOTHING") {
			return pgconn.CommandTag{}, fmt.Errorf("insert must guard conflict: %s", sql)
		}
		jobID := args[0].(string)
		scheduleID := args[1].(string)
		jobType := args[2].(string)
		scheduledFor := args[5].(time.Time)
		timezone := args[6].(string)
		priority := args[7].(int)
		dedupKey := args[8].(string)
		payload := json.RawMessage(args[9].(string))
		maxAttempts := args[10].(int)
		timeoutSeconds := args[11].(int)
		traceID := args[12].(string)

		jobs := tx.viewJobsLocked()
		for _, existing := range jobs {
			if existing.DedupKey == dedupKey || (existing.ScheduleID == scheduleID && existing.ScheduledFor.Equal(scheduledFor)) {
				return pgconn.NewCommandTag("INSERT 0 0"), nil
			}
		}
		tx.stagedJobs[jobID] = fakeJobRow{
			JobID:          jobID,
			ScheduleID:     scheduleID,
			JobType:        jobType,
			ScheduledFor:   scheduledFor,
			Timezone:       timezone,
			Priority:       priority,
			DedupKey:       dedupKey,
			Payload:        payload,
			State:          "PENDING",
			MaxAttempts:    maxAttempts,
			TimeoutSeconds: timeoutSeconds,
			TraceID:        traceID,
		}
		return pgconn.NewCommandTag("INSERT 0 1"), nil

	case strings.HasPrefix(sql, "UPDATE core_registry.job_instances SET cancel_requested=true"):
		scheduleID := args[0].(string)
		jobs := tx.viewJobsLocked()
		updated := 0
		for id, job := range jobs {
			if job.ScheduleID == scheduleID && isOneOf(job.State, "PENDING", "READY", "CLAIMED", "RUNNING", "RETRY_WAIT") {
				job.CancelRequested = true
				tx.stagedJobs[id] = job
				updated++
			}
		}
		return pgconn.NewCommandTag(fmt.Sprintf("UPDATE %d", updated)), nil

	case strings.HasPrefix(sql, "UPDATE core_registry.job_instances SET state='READY'"):
		jobID := args[0].(string)
		jobs := tx.viewJobsLocked()
		job, ok := jobs[jobID]
		if !ok || job.CancelRequested {
			return pgconn.NewCommandTag("UPDATE 0"), nil
		}
		if strings.Contains(sql, "state='RETRY_WAIT'") {
			if job.State != "RETRY_WAIT" {
				return pgconn.NewCommandTag("UPDATE 0"), nil
			}
			job.NextRetryAt = nil
		} else if job.State != "PENDING" {
			return pgconn.NewCommandTag("UPDATE 0"), nil
		}
		job.State = "READY"
		tx.stagedJobs[jobID] = job
		return pgconn.NewCommandTag("UPDATE 1"), nil

	case strings.HasPrefix(sql, "UPDATE core_registry.job_instances SET state='CANCELLED'"):
		jobID := args[0].(string)
		completedAt := args[1].(time.Time)
		jobs := tx.viewJobsLocked()
		job, ok := jobs[jobID]
		if !ok || !job.CancelRequested || !isOneOf(job.State, "PENDING", "READY", "RETRY_WAIT") {
			return pgconn.NewCommandTag("UPDATE 0"), nil
		}
		job.State = "CANCELLED"
		job.CompletedAt = &completedAt
		tx.stagedJobs[jobID] = job
		return pgconn.NewCommandTag("UPDATE 1"), nil

	case strings.HasPrefix(sql, "UPDATE core_registry.job_instances SET state=$2"):
		jobID := args[0].(string)
		state := args[1].(string)
		now := args[5].(time.Time)
		jobs := tx.viewJobsLocked()
		job, ok := jobs[jobID]
		if !ok || !isOneOf(job.State, "CLAIMED", "RUNNING") || job.LeaseUntil == nil || !job.LeaseUntil.Before(now) {
			return pgconn.NewCommandTag("UPDATE 0"), nil
		}
		job.State = state
		job.LeaseOwner = ""
		job.LeaseUntil = nil
		if args[2] != nil {
			t := args[2].(time.Time)
			job.NextRetryAt = &t
		} else {
			job.NextRetryAt = nil
		}
		if args[3] != nil {
			t := args[3].(time.Time)
			job.CompletedAt = &t
		} else {
			job.CompletedAt = nil
		}
		job.ErrorCode = args[4].(string)
		job.ErrorMessage = args[4].(string)
		tx.stagedJobs[jobID] = job
		return pgconn.NewCommandTag("UPDATE 1"), nil

	case strings.HasPrefix(sql, "UPDATE core_registry.job_attempts"):
		jobID := args[0].(string)
		attemptNo := args[1].(int)
		resultStatus := args[2].(string)
		completedAt := args[3].(time.Time)
		errorCode := args[4].(string)
		key := fmt.Sprintf("%s:%d", jobID, attemptNo)
		attempts := tx.viewAttemptsLocked()
		attempt, ok := attempts[key]
		if !ok || attempt.CompletedAt != nil {
			return pgconn.NewCommandTag("UPDATE 0"), nil
		}
		attempt.CompletedAt = &completedAt
		attempt.ResultStatus = resultStatus
		attempt.ErrorCode = errorCode
		attempt.ErrorMessage = errorCode
		tx.stagedAttempts[key] = attempt
		return pgconn.NewCommandTag("UPDATE 1"), nil

	case strings.HasPrefix(sql, "UPDATE core_registry.schedule_definitions"):
		scheduleID := args[0].(string)
		schedules := tx.viewSchedulesLocked()
		s, ok := schedules[scheduleID]
		if !ok || !s.Enabled {
			return pgconn.NewCommandTag("UPDATE 0"), nil
		}
		if strings.Contains(sql, "enabled=false") {
			s.Enabled = false
			s.NextFireAt = time.Time{}
			if args[1] != nil {
				t := args[1].(time.Time)
				s.LastFireAt = &t
			}
		} else {
			s.NextFireAt = args[1].(time.Time)
			if args[2] != nil {
				t := args[2].(time.Time)
				s.LastFireAt = &t
			}
		}
		s.Version++
		tx.stagedSchedules[scheduleID] = s
		return pgconn.NewCommandTag("UPDATE 1"), nil

	default:
		return pgconn.CommandTag{}, fmt.Errorf("unexpected fakeTx Exec: %s", sql)
	}
}

type fakeRow struct {
	vals []any
	err  error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	return scanFakeValues(r.vals, dest)
}

type fakeRows struct {
	rows   [][]any
	idx    int
	closed bool
}

func (r *fakeRows) Close()                                       { r.closed = true }
func (r *fakeRows) Err() error                                   { return nil }
func (r *fakeRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *fakeRows) Conn() *pgx.Conn                              { return nil }
func (r *fakeRows) RawValues() [][]byte                          { return nil }

func (r *fakeRows) Next() bool {
	if r.closed || r.idx >= len(r.rows) {
		return false
	}
	r.idx++
	return true
}

func (r *fakeRows) Values() ([]any, error) {
	if r.idx == 0 || r.idx > len(r.rows) {
		return nil, errors.New("no current row")
	}
	return r.rows[r.idx-1], nil
}

func (r *fakeRows) Scan(dest ...any) error {
	vals, err := r.Values()
	if err != nil {
		return err
	}
	return scanFakeValues(vals, dest)
}

func scanFakeValues(vals []any, dest []any) error {
	if len(vals) != len(dest) {
		return fmt.Errorf("column count %d != dest count %d", len(vals), len(dest))
	}
	for i, val := range vals {
		switch target := dest[i].(type) {
		case *string:
			*target = val.(string)
		case *int:
			*target = val.(int)
		case *int64:
			*target = val.(int64)
		case *bool:
			*target = val.(bool)
		case *time.Time:
			*target = val.(time.Time)
		case *json.RawMessage:
			*target = append((*target)[:0], val.(json.RawMessage)...)
		default:
			return fmt.Errorf("unsupported scan target %T at index %d", dest[i], i)
		}
	}
	return nil
}

func normalizeFakeSQL(sql string) string {
	return strings.Join(strings.Fields(sql), " ")
}

func isOneOf(value string, choices ...string) bool {
	for _, choice := range choices {
		if value == choice {
			return true
		}
	}
	return false
}
