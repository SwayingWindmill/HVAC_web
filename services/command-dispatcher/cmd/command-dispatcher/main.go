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
	"github.com/quanlaihe/hvac-web/services/command-dispatcher/pkg/mqttconnector"
	"github.com/quanlaihe/hvac-web/services/command-service/pkg/commandservice"
)

func main() {
	logger := observability.NewJSONLogger(os.Stdout, slog.LevelInfo)
	telemetry := observability.NewRuntime(observability.RuntimeConfig{
		Service: "command-dispatcher", OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), QueueSize: 2048, ExportTimeout: 500 * time.Millisecond,
	})
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	tenantID := requiredEnv("COMMAND_RUNTIME_TENANT_ID")
	siteID := requiredEnv("COMMAND_RUNTIME_SITE_ID")
	deviceID := requiredEnv("COMMAND_RUNTIME_DEVICE_ID")
	gatewayID := requiredEnv("MQTT_COMMAND_GATEWAY_ID")
	externalDeviceID := requiredEnv("MQTT_COMMAND_EXTERNAL_DEVICE_ID")

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
		BaseURL:    requiredEnv("COMMAND_RUNTIME_URL"),
		HTTPClient: runtimeHTTPClient,
		TenantID:   tenantID,
		SiteID:     siteID,
		DeviceID:   deviceID,
	})
	if err != nil {
		logger.Error("dispatcher_runtime_client_invalid", "error_code", "COMMAND_RUNTIME_CLIENT_INVALID")
		os.Exit(1)
	}

	s2HTTPClient, err := workloadtls.NewHTTPClient(workloadtls.ClientConfig{
		CertificateFiles: &runtimeIdentity,
		ServerCAPath:     requiredEnv("S2_DISPATCH_SAFETY_SERVER_CA"),
		ServerName:       requiredEnv("S2_DISPATCH_SAFETY_SERVER_NAME"),
		Timeout:          10 * time.Second,
	})
	if err != nil {
		logger.Error("dispatcher_s2_tls_invalid", "error_code", "S2_DISPATCH_SAFETY_TLS_INVALID")
		os.Exit(1)
	}
	safetyReader, err := commanddispatcher.NewReportedStateClient(commanddispatcher.ReportedStateClientConfig{
		BaseURL: requiredEnv("S2_DISPATCH_SAFETY_URL"), HTTPClient: s2HTTPClient,
		TenantID: tenantID, SiteID: siteID, DeviceID: deviceID,
	})
	if err != nil {
		logger.Error("dispatcher_s2_client_invalid", "error_code", "S2_DISPATCH_SAFETY_CLIENT_INVALID")
		os.Exit(1)
	}
	safetyVerifier, err := commanddispatcher.NewAuthoritativeDispatchSafetyVerifier(safetyReader, requiredEnv("COMMAND_DISPATCH_SAFETY_STATE_KEY"))
	if err != nil {
		logger.Error("dispatcher_safety_verifier_invalid", "error_code", "COMMAND_DISPATCH_SAFETY_INVALID")
		os.Exit(1)
	}

	connector, err := mqttconnector.New(ctx, mqttconnector.Config{
		BrokerURL:  requiredEnv("MQTT_COMMAND_BROKER_URL"),
		ClientID:   envOr("MQTT_COMMAND_CLIENT_ID", hostnameOr("command-dispatcher")),
		CAFile:     requiredEnv("MQTT_COMMAND_CA"),
		CertFile:   requiredEnv("MQTT_COMMAND_CERT"),
		KeyFile:    requiredEnv("MQTT_COMMAND_KEY"),
		ServerName: requiredEnv("MQTT_COMMAND_SERVER_NAME"),
		TenantID:   tenantID,
		SiteID:     siteID,
		GatewayID:  gatewayID,
		DeviceExternalIDByDeviceID: map[string]string{
			deviceID: externalDeviceID,
		},
		EvidenceStore: runtimeClient,
		ReplyTimeout:  15 * time.Second,
	})
	if err != nil {
		logger.Error("dispatcher_connector_configuration_invalid", "error_code", "MQTT_COMMAND_CONNECTOR_INVALID")
		os.Exit(1)
	}

	workerID := envOr("COMMAND_DISPATCHER_WORKER_ID", hostnameOr("command-dispatcher"))
	worker := commanddispatcher.NewDurable(runtimeClient, safetyVerifier, connector, workerID, 30*time.Second)
	verifierWorker, verifierWorkerID, reportedStateKey, err := loadInProcessVerifier(tenantID, siteID, deviceID)
	if err != nil {
		logger.Error("dispatcher_verifier_configuration_invalid", "error_code", "COMMAND_VERIFIER_CONFIGURATION_INVALID")
		os.Exit(1)
	}
	diagnostics := &http.Server{
		Addr: envOr("COMMAND_DISPATCHER_DIAGNOSTICS_ADDR", ":19088"), Handler: telemetry.DiagnosticsHandler(),
		ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second,
	}
	go func() {
		if err := diagnostics.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("dispatcher_diagnostics_failed", "error_code", "DIAGNOSTICS_SERVE_FAILED")
		}
	}()

	telemetry.MarkReady()
	if verifierWorker != nil {
		logger.Info("command_verifier_started", "worker_id", verifierWorkerID, "tenant_id", tenantID, "reported_state_key", reportedStateKey, "deployment", "in-process")
		go runVerifier(ctx, logger, verifierWorker, tenantID)
	}
	logger.Info("command_dispatcher_started", "worker_id", workerID, "tenant_id", tenantID, "site_id", siteID, "gateway_id", gatewayID)
	runDispatcher(ctx, logger, worker, tenantID)
	telemetry.MarkNotReady()
	shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = connector.Disconnect(shutdownContext)
	_ = diagnostics.Shutdown(shutdownContext)
	_ = telemetry.Shutdown(shutdownContext)
	logger.Info("command_dispatcher_stopped", "worker_id", workerID)
}

func loadInProcessVerifier(tenantID, siteID, deviceID string) (*commanddispatcher.DurableVerificationWorker, string, string, error) {
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("COMMAND_VERIFIER_IN_PROCESS_ENABLED")), "true") {
		return nil, "", "", nil
	}
	reportedStateKey := requiredEnv("COMMAND_VERIFICATION_REPORTED_STATE_KEY")
	verifierIdentity := workloadtls.CertificateFiles{
		CertificatePath: requiredEnv("S2_REPORTED_STATE_CLIENT_CERT"),
		PrivateKeyPath:  requiredEnv("S2_REPORTED_STATE_CLIENT_KEY"),
	}
	runtimeHTTPClient, err := workloadtls.NewHTTPClient(workloadtls.ClientConfig{
		CertificateFiles: &verifierIdentity,
		ServerCAPath:     requiredEnv("COMMAND_RUNTIME_SERVER_CA"),
		ServerName:       requiredEnv("COMMAND_RUNTIME_SERVER_NAME"),
		Timeout:          20 * time.Second,
	})
	if err != nil {
		return nil, "", "", err
	}
	runtimeClient, err := commanddispatcher.NewRuntimeClient(commanddispatcher.RuntimeClientConfig{
		BaseURL: requiredEnv("COMMAND_RUNTIME_URL"), HTTPClient: runtimeHTTPClient,
		TenantID: tenantID, SiteID: siteID, DeviceID: deviceID,
	})
	if err != nil {
		return nil, "", "", err
	}
	s2HTTPClient, err := workloadtls.NewHTTPClient(workloadtls.ClientConfig{
		CertificateFiles: &verifierIdentity,
		ServerCAPath:     requiredEnv("S2_REPORTED_STATE_SERVER_CA"),
		ServerName:       requiredEnv("S2_REPORTED_STATE_SERVER_NAME"),
		Timeout:          10 * time.Second,
	})
	if err != nil {
		return nil, "", "", err
	}
	reader, err := commanddispatcher.NewReportedStateClient(commanddispatcher.ReportedStateClientConfig{
		BaseURL: requiredEnv("S2_REPORTED_STATE_URL"), HTTPClient: s2HTTPClient,
		TenantID: tenantID, SiteID: siteID, DeviceID: deviceID,
	})
	if err != nil {
		return nil, "", "", err
	}
	workerID := envOr("COMMAND_VERIFIER_WORKER_ID", hostnameOr("command-verifier"))
	verifier := commanddispatcher.NewAuthoritativeReportedStateVerifier(reader)
	return commanddispatcher.NewDurableVerificationWorker(runtimeClient, verifier, workerID, 15*time.Second), workerID, reportedStateKey, nil
}

func runVerifier(ctx context.Context, logger *slog.Logger, worker *commanddispatcher.DurableVerificationWorker, tenantID string) {
	backoff := 100 * time.Millisecond
	for ctx.Err() == nil {
		runContext, cancel := context.WithTimeout(ctx, 12*time.Second)
		err := worker.RunOnce(runContext, tenantID)
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

func runDispatcher(ctx context.Context, logger *slog.Logger, worker *commanddispatcher.DurableDispatcher, tenantID string) {
	backoff := 100 * time.Millisecond
	for ctx.Err() == nil {
		runContext, cancel := context.WithTimeout(ctx, 25*time.Second)
		err := worker.RunOnce(runContext, tenantID)
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
