package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
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
		Service: "command-dispatcher", OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), QueueSize: 2048, ExportTimeout: 500 * time.Millisecond,
	})

	cohort, err := controlconnector.LoadApprovedCohort(requiredEnv("S3_APPROVED_COHORT_FILE"))
	if err != nil {
		logger.Error("dispatcher_cohort_load_failed", "error_code", "S3_APPROVED_COHORT_INVALID")
		os.Exit(1)
	}
	targetResolver, err := controlconnector.NewApprovedCohortTargetResolver(cohort)
	if err != nil {
		logger.Error("dispatcher_target_resolver_invalid", "error_code", "S3_TARGET_RESOLVER_INVALID")
		os.Exit(1)
	}
	credentialProvider, err := controlconnector.NewFileCredentialProvider(controlconnector.FileCredentialConfig{
		Path: requiredEnv("THINGSBOARD_CREDENTIAL_FILE"), CredentialReference: cohort.CredentialReference, IntegrationID: cohort.IntegrationID,
	})
	if err != nil {
		logger.Error("dispatcher_credential_provider_invalid", "error_code", "S3_CREDENTIAL_PROVIDER_INVALID")
		os.Exit(1)
	}

	runtimeIdentity := workloadtls.CertificateFiles{
		CertificatePath: requiredEnv("COMMAND_RUNTIME_CLIENT_CERT"),
		PrivateKeyPath:  requiredEnv("COMMAND_RUNTIME_CLIENT_KEY"),
	}
	runtimeHTTPClient, err := workloadtls.NewHTTPClient(workloadtls.ClientConfig{
		CertificateFiles: &runtimeIdentity,
		ServerCAPath:     requiredEnv("COMMAND_RUNTIME_SERVER_CA"),
		ServerName:       requiredEnv("COMMAND_RUNTIME_SERVER_NAME"),
		Timeout:          20 * time.Second,
	})
	if err != nil {
		logger.Error("dispatcher_runtime_tls_invalid", "error_code", "COMMAND_RUNTIME_TLS_INVALID")
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
		logger.Error("dispatcher_runtime_client_invalid", "error_code", "COMMAND_RUNTIME_CLIENT_INVALID")
		os.Exit(1)
	}
	providerHTTPClient, err := workloadtls.NewHTTPClient(workloadtls.ClientConfig{
		ServerCAPath: requiredEnv("THINGSBOARD_SERVER_CA"),
		ServerName:   requiredEnv("THINGSBOARD_SERVER_NAME"),
		Timeout:      10 * time.Second,
	})
	if err != nil {
		logger.Error("dispatcher_provider_tls_invalid", "error_code", "THINGSBOARD_TLS_INVALID")
		os.Exit(1)
	}
	thingsBoardBaseURL, err := requireHTTPSOrigin(requiredEnv("THINGSBOARD_BASE_URL"))
	if err != nil {
		logger.Error("dispatcher_provider_url_invalid", "error_code", "THINGSBOARD_URL_INVALID")
		os.Exit(1)
	}
	connector, err := controlconnector.NewThingsBoard(controlconnector.ThingsBoardConfig{
		BaseURL: thingsBoardBaseURL, HTTPClient: providerHTTPClient,
		TargetResolver: targetResolver, CredentialProvider: credentialProvider, EvidenceStore: runtimeClient,
		Mappings: []controlconnector.Mapping{cohort.Mapping()}, AllowLocalVerified: false, AllowProductionVerified: true,
	})
	if err != nil {
		logger.Error("dispatcher_connector_configuration_invalid", "error_code", "THINGSBOARD_CONNECTOR_INVALID")
		os.Exit(1)
	}

	workerID := envOr("COMMAND_DISPATCHER_WORKER_ID", hostnameOr("command-dispatcher"))
	worker := commanddispatcher.NewDurable(runtimeClient, connector, workerID, 30*time.Second)
	diagnostics := &http.Server{
		Addr: envOr("COMMAND_DISPATCHER_DIAGNOSTICS_ADDR", ":19088"), Handler: telemetry.DiagnosticsHandler(),
		ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second,
	}
	go func() {
		if err := diagnostics.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("dispatcher_diagnostics_failed", "error_code", "DIAGNOSTICS_SERVE_FAILED")
		}
	}()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	telemetry.MarkReady()
	logger.Info("command_dispatcher_started", "worker_id", workerID, "organization_id", cohort.OrganizationID, "credential_reference", credentialProvider.CredentialReference())
	runDispatcher(ctx, logger, worker, cohort.OrganizationID)
	telemetry.MarkNotReady()
	shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = diagnostics.Shutdown(shutdownContext)
	_ = telemetry.Shutdown(shutdownContext)
	logger.Info("command_dispatcher_stopped", "worker_id", workerID)
}

func runDispatcher(ctx context.Context, logger *slog.Logger, worker *commanddispatcher.DurableDispatcher, organizationID string) {
	backoff := 100 * time.Millisecond
	for ctx.Err() == nil {
		runContext, cancel := context.WithTimeout(ctx, 25*time.Second)
		err := worker.RunOnce(runContext, organizationID)
		cancel()
		switch {
		case err == nil:
			backoff = 100 * time.Millisecond
		case errors.Is(err, commandservice.ErrNoDispatchAvailable):
			sleepContext(ctx, 100*time.Millisecond)
		case errors.Is(err, commandservice.ErrStaleFence), errors.Is(err, commandservice.ErrCommandNotFound):
			logger.Warn("dispatcher_stale_work_discarded", "error_code", "COMMAND_RUNTIME_STALE_WORK")
		case ctx.Err() != nil:
			return
		default:
			logger.Error("dispatcher_run_failed", "error_code", "COMMAND_DISPATCH_FAILED")
			sleepContext(ctx, backoff)
			if backoff < 5*time.Second {
				backoff *= 2
			}
		}
	}
}

func requireHTTPSOrigin(value string) (string, error) {
	value = strings.TrimRight(strings.TrimSpace(value), "/")
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", errors.New("provider URL must be an HTTPS service origin")
	}
	return value, nil
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
