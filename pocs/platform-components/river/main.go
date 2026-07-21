package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

type registryJobArgs struct {
	ResourceID string `json:"resource_id" river:"unique"`
}

func (registryJobArgs) Kind() string { return "poc_registry_job" }

type registryWorker struct {
	river.WorkerDefaults[registryJobArgs]
	pool *pgxpool.Pool
}

func (worker *registryWorker) Work(ctx context.Context, job *river.Job[registryJobArgs]) error {
	_, err := worker.pool.Exec(ctx, `
		INSERT INTO poc_job_effect (resource_id, river_job_id)
		VALUES ($1, $2)
		ON CONFLICT (resource_id) DO NOTHING
	`, job.Args.ResourceID, job.ID)
	return err
}

type report struct {
	SchemaVersion        int    `json:"schemaVersion"`
	Component            string `json:"component"`
	Status               string `json:"status"`
	CommittedDomainRows  int    `json:"committedDomainRows"`
	RolledBackDomainRows int    `json:"rolledBackDomainRows"`
	CommittedJobs        int    `json:"committedJobs"`
	RolledBackJobs       int    `json:"rolledBackJobs"`
	UniqueJobs           int    `json:"uniqueJobs"`
	BusinessEffects      int    `json:"businessEffects"`
	WorkerRestarted      bool   `json:"workerRestarted"`
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	databaseURL := os.Getenv("POC_RIVER_DATABASE_URL")
	if databaseURL == "" {
		fail(errors.New("POC_RIVER_DATABASE_URL is required"))
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		fail(err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		fail(err)
	}

	driver := riverpgxv5.New(pool)
	migrator, err := rivermigrate.New(driver, nil)
	if err != nil {
		fail(err)
	}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		fail(err)
	}
	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS poc_domain_event (
			resource_id text PRIMARY KEY,
			created_at timestamptz NOT NULL DEFAULT clock_timestamp()
		);
		CREATE TABLE IF NOT EXISTS poc_job_effect (
			resource_id text PRIMARY KEY,
			river_job_id bigint NOT NULL,
			created_at timestamptz NOT NULL DEFAULT clock_timestamp()
		);
		TRUNCATE poc_domain_event, poc_job_effect;
		DELETE FROM river_job WHERE kind = 'poc_registry_job';
	`); err != nil {
		fail(err)
	}

	workers := river.NewWorkers()
	river.AddWorker(workers, &registryWorker{pool: pool})

	firstWorker, err := newWorkerClient(driver, workers)
	if err != nil {
		fail(err)
	}
	if err := firstWorker.Start(ctx); err != nil {
		fail(err)
	}
	if err := firstWorker.Stop(ctx); err != nil {
		fail(err)
	}

	insertOnly, err := river.NewClient(driver, &river.Config{Workers: workers, TestOnly: true})
	if err != nil {
		fail(err)
	}
	unique := &river.InsertOpts{UniqueOpts: river.UniqueOpts{ByArgs: true}}

	committedTx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		fail(err)
	}
	if _, err := committedTx.Exec(ctx, `INSERT INTO poc_domain_event (resource_id) VALUES ($1)`, "committed-resource"); err != nil {
		failRollback(committedTx, err)
	}
	if _, err := insertOnly.InsertTx(ctx, committedTx, registryJobArgs{ResourceID: "committed-resource"}, unique); err != nil {
		failRollback(committedTx, err)
	}
	if _, err := insertOnly.InsertTx(ctx, committedTx, registryJobArgs{ResourceID: "committed-resource"}, unique); err != nil {
		failRollback(committedTx, err)
	}
	if err := committedTx.Commit(ctx); err != nil {
		fail(err)
	}

	rolledBackTx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		fail(err)
	}
	if _, err := rolledBackTx.Exec(ctx, `INSERT INTO poc_domain_event (resource_id) VALUES ($1)`, "rolled-back-resource"); err != nil {
		failRollback(rolledBackTx, err)
	}
	if _, err := insertOnly.InsertTx(ctx, rolledBackTx, registryJobArgs{ResourceID: "rolled-back-resource"}, unique); err != nil {
		failRollback(rolledBackTx, err)
	}
	if err := rolledBackTx.Rollback(ctx); err != nil {
		fail(err)
	}

	secondWorker, err := newWorkerClient(driver, workers)
	if err != nil {
		fail(err)
	}
	if err := secondWorker.Start(ctx); err != nil {
		fail(err)
	}
	if err := waitForEffect(ctx, pool, "committed-resource"); err != nil {
		fail(err)
	}
	if err := secondWorker.Stop(ctx); err != nil {
		fail(err)
	}

	result := report{
		SchemaVersion:        1,
		Component:            "river",
		Status:               "passed",
		CommittedDomainRows:  scalar(ctx, pool, `SELECT count(*) FROM poc_domain_event WHERE resource_id = 'committed-resource'`),
		RolledBackDomainRows: scalar(ctx, pool, `SELECT count(*) FROM poc_domain_event WHERE resource_id = 'rolled-back-resource'`),
		CommittedJobs:        scalar(ctx, pool, `SELECT count(*) FROM river_job WHERE kind = 'poc_registry_job' AND args->>'resource_id' = 'committed-resource'`),
		RolledBackJobs:       scalar(ctx, pool, `SELECT count(*) FROM river_job WHERE kind = 'poc_registry_job' AND args->>'resource_id' = 'rolled-back-resource'`),
		UniqueJobs:           scalar(ctx, pool, `SELECT count(*) FROM river_job WHERE kind = 'poc_registry_job' AND args->>'resource_id' = 'committed-resource'`),
		BusinessEffects:      scalar(ctx, pool, `SELECT count(*) FROM poc_job_effect WHERE resource_id = 'committed-resource'`),
		WorkerRestarted:      true,
	}
	if result.CommittedDomainRows != 1 || result.RolledBackDomainRows != 0 || result.CommittedJobs != 1 || result.RolledBackJobs != 0 || result.UniqueJobs != 1 || result.BusinessEffects != 1 {
		fail(fmt.Errorf("River invariants failed: %+v", result))
	}
	encoded, _ := json.Marshal(result)
	fmt.Println(string(encoded))
}

func newWorkerClient(driver *riverpgxv5.Driver, workers *river.Workers) (*river.Client[pgx.Tx], error) {
	return river.NewClient(driver, &river.Config{
		Queues: map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: 1},
		},
		Workers:           workers,
		FetchCooldown:     10 * time.Millisecond,
		FetchPollInterval: 50 * time.Millisecond,
		TestOnly:          true,
	})
}

func waitForEffect(ctx context.Context, pool *pgxpool.Pool, resourceID string) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if scalar(ctx, pool, `SELECT count(*) FROM poc_job_effect WHERE resource_id = $1`, resourceID) == 1 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func scalar(ctx context.Context, pool *pgxpool.Pool, query string, args ...any) int {
	var value int
	if err := pool.QueryRow(ctx, query, args...).Scan(&value); err != nil {
		fail(err)
	}
	return value
}

func failRollback(tx pgx.Tx, err error) {
	_ = tx.Rollback(context.Background())
	fail(err)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
