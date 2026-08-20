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
	"github.com/quanlaihe/hvac-web/services/forecast-service/internal/forecast"
)

func main() {
	ctx := context.Background()
	clickHouseURL := requiredEnv("FORECAST_CLICKHOUSE_HTTP_URL")
	sink, err := forecast.NewClickHouseSink(forecast.ClickHouseConfig{
		BaseURL:  clickHouseURL,
		Database: envOrDefault("FORECAST_CLICKHOUSE_DATABASE", "analytics"),
		Table:    envOrDefault("FORECAST_CLICKHOUSE_TABLE", "forecast_series"),
		Username: envOrDefault("FORECAST_CLICKHOUSE_USERNAME", "forecast_service_writer"),
		Password: os.Getenv("FORECAST_CLICKHOUSE_PASSWORD"),
	})
	if err != nil {
		log.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, requiredEnv("FORECAST_POSTGRES_DSN"))
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	publication, err := forecast.NewPostgresStore(pool)
	if err != nil {
		log.Fatal(err)
	}
	service, err := forecast.NewService(sink, publication, time.Now)
	if err != nil {
		log.Fatal(err)
	}
	handler, err := forecast.NewHTTPHandler(service)
	if err != nil {
		log.Fatal(err)
	}

	workerID := envOrDefault("FORECAST_WORKER_ID", defaultWorkerID())
	claimInterval := durationEnv("FORECAST_CLAIM_INTERVAL", 5*time.Second)
	leaseDuration := durationEnv("FORECAST_LEASE_DURATION", 2*time.Minute)
	leaseRenew := durationEnv("FORECAST_LEASE_RENEW", 30*time.Second)
	claimBatch := integerEnv("FORECAST_CLAIM_BATCH", 8)
	go runForecastWorker(ctx, publication, service, workerID, claimInterval, claimBatch, leaseDuration, leaseRenew)

	// Cross-store repair is part of the Forecast runtime itself. It only repairs
	// stale PERSISTING jobs after proving the expected result count in ClickHouse.
	reconcileEvery := 30 * time.Second
	go func() {
		ticker := time.NewTicker(reconcileEvery)
		defer ticker.Stop()
		for range ticker.C {
			reconcileCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_, reconcileErr := service.Reconcile(reconcileCtx, time.Now().UTC().Add(-2*time.Minute), 100)
			cancel()
			if reconcileErr != nil {
				log.Printf("forecast reconciliation failed: %v", reconcileErr)
			}
		}
	}()

	server := &http.Server{
		Addr:              envOrDefault("FORECAST_HTTP_ADDR", ":19092"),
		Handler:           handler.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	log.Printf("forecast-service listening on %s", server.Addr)
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

func runForecastWorker(ctx context.Context, store *forecast.PostgresStore, service *forecast.Service, workerID string, claimInterval time.Duration, claimBatch int, leaseDuration, leaseRenew time.Duration) {
	claimAndRunForecastJobs(ctx, store, service, workerID, claimBatch, leaseDuration, leaseRenew)
	ticker := time.NewTicker(claimInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			claimAndRunForecastJobs(ctx, store, service, workerID, claimBatch, leaseDuration, leaseRenew)
		}
	}
}

func claimAndRunForecastJobs(ctx context.Context, store *forecast.PostgresStore, service *forecast.Service, workerID string, claimBatch int, leaseDuration, leaseRenew time.Duration) {
	claimCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	jobs, err := store.ClaimForecastJobs(claimCtx, workerID, claimBatch, leaseDuration, time.Now().UTC())
	cancel()
	if err != nil {
		log.Printf("forecast job claim failed: %v", err)
		return
	}
	for _, job := range jobs {
		executeForecastJob(ctx, store, service, job, leaseDuration, leaseRenew)
	}
}

func executeForecastJob(ctx context.Context, store *forecast.PostgresStore, service *forecast.Service, job forecast.SchedulerJob, leaseDuration, leaseRenew time.Duration) {
	started, err := store.StartForecastJob(ctx, job, leaseDuration, time.Now().UTC())
	if err != nil {
		log.Printf("forecast job %s start failed: %v", job.JobID, err)
		return
	}
	if !started {
		return
	}
	request, validationErr := forecast.ValidateForecastSchedulerJob(job)
	if validationErr != nil {
		_ = store.FailForecastJob(context.Background(), job, "FORECAST_JOB_INVALID", validationErr, false, time.Now().UTC())
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
				cancelRequested, renewErr := store.RenewForecastJobLease(renewCtx, job, leaseDuration, time.Now().UTC())
				renewCancel()
				if renewErr != nil || cancelRequested {
					cancelJob()
					return
				}
			}
		}
	}()
	points, executionErr := service.Forecast(jobCtx, request)
	close(leaseStop)
	<-leaseDone
	if executionErr != nil {
		retryable := !errors.Is(executionErr, context.Canceled)
		errorCode := "FORECAST_EXECUTION_FAILED"
		if errors.Is(executionErr, context.DeadlineExceeded) {
			errorCode = "TIMEOUT"
		}
		if err = store.FailForecastJob(context.Background(), job, errorCode, executionErr, retryable, time.Now().UTC()); err != nil {
			log.Printf("forecast job %s failure finalization failed: %v", job.JobID, err)
		}
		return
	}
	quality := ""
	if len(points) > 0 {
		quality = points[0].Quality
	}
	output := map[string]any{"forecastSnapshotId": request.ForecastSnapshotID, "forecastJobId": request.ForecastJobID, "pointCount": len(points), "quality": quality}
	if err = store.CompleteForecastJob(context.Background(), job, output, time.Now().UTC()); err != nil {
		log.Printf("forecast job %s success finalization failed: %v", job.JobID, err)
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
		hostname = "forecast"
	}
	return fmt.Sprintf("%s-%d", hostname, os.Getpid())
}
