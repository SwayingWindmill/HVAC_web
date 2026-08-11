package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/workloadtls"
	"github.com/quanlaihe/hvac-web/services/command-dispatcher/pkg/commanddispatcher"
	"github.com/quanlaihe/hvac-web/services/command-service/pkg/commandservice"
	"github.com/quanlaihe/hvac-web/services/thingsboard-connector-control/pkg/controlconnector"
)

func main() {
	logger := observability.NewJSONLogger(os.Stdout, slog.LevelInfo)
	telemetry := observability.NewRuntime(observability.RuntimeConfig{
		Service: "command-verifier", OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), QueueSize: 2048, ExportTimeout: 500 * time.Millisecond,
	})
	cohort, err := controlconnector.LoadApprovedCohort(requiredEnv("S3_APPROVED_COHORT_FILE"))
	if err != nil {
		logger.Error("verifier_cohort_load_failed", "error_code", "S3_APPROVED_COHORT_INVALID")
		os.Exit(1)
	}

	workloadIdentity := workloadtls.CertificateFiles{
		CertificatePath: requiredEnv("COMMAND_RUNTIME_CLIENT_CERT"),
		PrivateKeyPath:  requiredEnv("COMMAND_RUNTIME_CLIENT_KEY"),
	}
	runtimeHTTPClient, err := workloadtls.NewHTTPClient(workloadtls.ClientConfig{
		CertificateFiles: &workloadIdentity,
		ServerCAPath:     requiredEnv("COMMAND_RUNTIME_SERVER_CA"),
		ServerName:       requiredEnv("COMMAND_RUNTIME_SERVER_NAME"),
		Timeout:          20 * time.Second,
	})
	if err != nil {
		logger.Error("verifier_runtime_tls_invalid", "error_code", "COMMAND_RUNTIME_TLS_INVALID")
		os.Exit(1)
	}
	runtimeClient, err := commanddispatcher.NewRuntimeClient(commanddispatcher.RuntimeClientConfig{
		BaseURL:        requiredEnv("COMMAND_RUNTIME_URL"),
		HTTPClient:     runtimeHTTPClient,
		OrganizationID: cohort.OrganizationID,
		SiteID:         cohort.SiteID,
		DeviceID:       cohort.DeviceID,
	})
	if err != nil {
		logger.Error("verifier_runtime_client_invalid", "error_code", "COMMAND_RUNTIME_CLIENT_INVALID")
		os.Exit(1)
	}
	s2Identity := workloadtls.CertificateFiles{
		CertificatePath: requiredEnv("S2_REPORTED_STATE_CLIENT_CERT"),
		PrivateKeyPath:  requiredEnv("S2_REPORTED_STATE_CLIENT_KEY"),
	}
	s2HTTPClient, err := workloadtls.NewHTTPClient(workloadtls.ClientConfig{
		CertificateFiles: &s2Identity,
		ServerCAPath:     requiredEnv("S2_REPORTED_STATE_SERVER_CA"),
		ServerName:       requiredEnv("S2_REPORTED_STATE_SERVER_NAME"),
		Timeout:          10 * time.Second,
	})
	if err != nil {
		logger.Error("verifier_s2_tls_invalid", "error_code", "S2_REPORTED_STATE_TLS_INVALID")
		os.Exit(1)
	}
	reader, err := commanddispatcher.NewReportedStateClient(commanddispatcher.ReportedStateClientConfig{
		BaseURL: requiredEnv("S2_REPORTED_STATE_URL"), HTTPClient: s2HTTPClient,
		OrganizationID: cohort.OrganizationID, SiteID: cohort.SiteID, DeviceID: cohort.DeviceID,
	})
	if err != nil {
		logger.Error("verifier_s2_client_invalid", "error_code", "S2_REPORTED_STATE_CLIENT_INVALID")
		os.Exit(1)
	}
	workerID := envOr("COMMAND_VERIFIER_WORKER_ID", hostnameOr("command-verifier"))
	verifier := commanddispatcher.NewAuthoritativeReportedStateVerifier(reader)
	worker := commanddispatcher.NewDurableVerificationWorker(runtimeClient, verifier, workerID, 15*time.Second)

	diagnostics := &http.Server{
		Addr: envOr("COMMAND_VERIFIER_DIAGNOSTICS_ADDR", ":19089"), Handler: telemetry.DiagnosticsHandler(),
		ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second,
	}
	go func() {
		if err := diagnostics.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("verifier_diagnostics_failed", "error_code", "DIAGNOSTICS_SERVE_FAILED")
		}
	}()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	telemetry.MarkReady()
	logger.Info("command_verifier_started", "worker_id", workerID, "organization_id", cohort.OrganizationID, "reported_state_key", cohort.ReportedStateKey)
	runVerifier(ctx, logger, worker, cohort.OrganizationID)
	telemetry.MarkNotReady()
	shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = diagnostics.Shutdown(shutdownContext)
	_ = telemetry.Shutdown(shutdownContext)
	logger.Info("command_verifier_stopped", "worker_id", workerID)
}

func runVerifier(ctx context.Context, logger *slog.Logger, worker *commanddispatcher.DurableVerificationWorker, organizationID string) {
	backoff := 100 * time.Millisecond
	for ctx.Err() == nil {
		runContext, cancel := context.WithTimeout(ctx, 12*time.Second)
		err := worker.RunOnce(runContext, organizationID)
		cancel()
		switch {
		case err == nil:
			backoff = 100 * time.Millisecond
		case errors.Is(err, commandservice.ErrVerificationNotAvailable):
			sleepContext(ctx, 100*time.Millisecond)
		case errors.Is(err, commandservice.ErrStaleFence), errors.Is(err, commandservice.ErrCommandNotFound):
			logger.Warn("verifier_stale_work_discarded", "error_code", "COMMAND_VERIFICATION_STALE_WORK")
		case ctx.Err() != nil:
			return
		default:
			logger.Error("verifier_run_failed", "error_code", "COMMAND_VERIFICATION_FAILED")
			sleepContext(ctx, backoff)
			if backoff < 5*time.Second {
				backoff *= 2
			}
		}
	}
}

func sleepContext(ctx context.Context, duration time.Duration) {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

func hostnameOr(fallback string) string {
	value, err := os.Hostname()
	if err != nil || strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func requiredEnv(name string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		_, _ = os.Stderr.WriteString(name + " is required\n")
		os.Exit(1)
	}
	return value
}
