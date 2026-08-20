package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/services/optimization-service/internal/optimization"
)

func main() {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, requiredEnv("OPTIMIZATION_POSTGRES_DSN"))
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	publication, err := optimization.NewPostgresStore(pool)
	if err != nil {
		log.Fatal(err)
	}
	evaluations, err := optimization.NewClickHouseSink(
		requiredEnv("OPTIMIZATION_CLICKHOUSE_HTTP_URL"),
		envOrDefault("OPTIMIZATION_CLICKHOUSE_USERNAME", "optimization_service_writer"),
		os.Getenv("OPTIMIZATION_CLICKHOUSE_PASSWORD"), nil,
	)
	if err != nil {
		log.Fatal(err)
	}
	service, err := optimization.NewDefaultService(publication, evaluations, time.Now)
	if err != nil {
		log.Fatal(err)
	}
	handler, err := optimization.NewHTTPHandler(service)
	if err != nil {
		log.Fatal(err)
	}

	workerID := envOrDefault("OPTIMIZATION_WORKER_ID", defaultWorkerID())
	claimInterval := durationEnv("OPTIMIZATION_CLAIM_INTERVAL", 5*time.Second)
	leaseDuration := durationEnv("OPTIMIZATION_LEASE_DURATION", 2*time.Minute)
	leaseRenew := durationEnv("OPTIMIZATION_LEASE_RENEW", 30*time.Second)
	claimBatch := integerEnv("OPTIMIZATION_CLAIM_BATCH", 8)
	go runOptimizationWorker(ctx, publication, service, workerID, claimInterval, claimBatch, leaseDuration, leaseRenew)

	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			reconcileCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_, reconcileErr := service.Reconcile(reconcileCtx, time.Now().UTC().Add(-2*time.Minute), 100)
			cancel()
			if reconcileErr != nil {
				log.Printf("optimization reconciliation failed: %v", reconcileErr)
			}
		}
	}()

	server := &http.Server{
		Addr:              envOrDefault("OPTIMIZATION_HTTP_ADDR", ":19093"),
		Handler:           handler.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	log.Printf("optimization-service listening on %s", server.Addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func requiredEnv(name string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		log.Fatalf("%s is required", name)
	}
	return value
}

func runOptimizationWorker(ctx context.Context, store *optimization.PostgresStore, service *optimization.Service, workerID string, claimInterval time.Duration, claimBatch int, leaseDuration, leaseRenew time.Duration) {
	claimAndRunOptimizationJobs(ctx, store, service, workerID, claimBatch, leaseDuration, leaseRenew)
	ticker := time.NewTicker(claimInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			claimAndRunOptimizationJobs(ctx, store, service, workerID, claimBatch, leaseDuration, leaseRenew)
		}
	}
}

func claimAndRunOptimizationJobs(ctx context.Context, store *optimization.PostgresStore, service *optimization.Service, workerID string, claimBatch int, leaseDuration, leaseRenew time.Duration) {
	claimCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	jobs, err := store.ClaimOptimizationJobs(claimCtx, workerID, claimBatch, leaseDuration, time.Now().UTC())
	cancel()
	if err != nil {
		log.Printf("optimization job claim failed: %v", err)
		return
	}
	for _, job := range jobs {
		executeOptimizationJob(ctx, store, service, job, leaseDuration, leaseRenew)
	}
}

func executeOptimizationJob(ctx context.Context, store *optimization.PostgresStore, service *optimization.Service, job optimization.SchedulerJob, leaseDuration, leaseRenew time.Duration) {
	started, err := store.StartOptimizationJob(ctx, job, leaseDuration, time.Now().UTC())
	if err != nil {
		log.Printf("optimization job %s start failed: %v", job.JobID, err)
		return
	}
	if !started {
		return
	}
	request, validationErr := optimization.ValidateOptimizationSchedulerJob(job)
	if validationErr != nil {
		_ = store.FailOptimizationJob(context.Background(), job, "OPTIMIZATION_JOB_INVALID", validationErr, false, time.Now().UTC())
		return
	}
	jobCtx, cancelJob := context.WithTimeout(ctx, time.Duration(job.TimeoutSeconds)*time.Second)
	defer cancelJob()
	leaseStop := make(chan struct{})
	leaseDone := make(chan struct{})
	go func() {
		defer close(leaseDone)
		ticker := time.NewTicker(leaseRenew)
		defer ticker.Stop()
		for {
			select {
			case <-leaseStop:
				return
			case <-jobCtx.Done():
				return
			case <-ticker.C:
				renewCtx, renewCancel := context.WithTimeout(context.Background(), 5*time.Second)
				cancelRequested, renewErr := store.RenewOptimizationJobLease(renewCtx, job, leaseDuration, time.Now().UTC())
				renewCancel()
				if renewErr != nil || cancelRequested {
					cancelJob()
					return
				}
			}
		}
	}()
	recommendation, executionErr := service.Optimize(jobCtx, request)
	close(leaseStop)
	<-leaseDone
	if executionErr != nil {
		retryable := !errors.Is(executionErr, context.Canceled)
		errorCode := "OPTIMIZATION_EXECUTION_FAILED"
		if errors.Is(executionErr, context.DeadlineExceeded) {
			errorCode = "TIMEOUT"
		}
		if err = store.FailOptimizationJob(context.Background(), job, errorCode, executionErr, retryable, time.Now().UTC()); err != nil {
			log.Printf("optimization job %s failure finalization failed: %v", job.JobID, err)
		}
		return
	}
	output := map[string]any{"optimizationRunId": request.OptimizationRunID, "recommendationId": recommendation.ID, "approvalState": recommendation.Approval}
	if err = store.CompleteOptimizationJob(context.Background(), job, output, time.Now().UTC()); err != nil {
		log.Printf("optimization job %s success finalization failed: %v", job.JobID, err)
	}
}

func durationEnv(name string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		log.Fatalf("%s must be a positive duration", name)
	}
	return parsed
}

func integerEnv(name string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 || parsed > 100 {
		log.Fatalf("%s must be an integer in [1,100]", name)
	}
	return parsed
}

func defaultWorkerID() string {
	hostname, err := os.Hostname()
	if err != nil || strings.TrimSpace(hostname) == "" {
		hostname = "optimization"
	}
	return fmt.Sprintf("%s-%d", hostname, os.Getpid())
}
