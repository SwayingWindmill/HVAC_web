package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/workloadtls"
	"github.com/quanlaihe/hvac-web/modules/command/pkg/commanddispatcher"
	"github.com/quanlaihe/hvac-web/modules/command/pkg/commandservice"
	"github.com/quanlaihe/hvac-web/modules/command/pkg/mqttconnector"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/connectivity"
)

// commandRuntime dispatches and verifies commands for every Tenant with a Gateway. Which
// Devices may be controlled and where their commands go both come from the Registry.
type commandRuntime struct {
	store            *connectivity.Store
	dispatcher       *commanddispatcher.DurableDispatcher
	dispatcherWorker string
	verifier         *commanddispatcher.DurableVerificationWorker
	verifierWorker   string
	connector        *mqttconnector.Connector
}

// loadCommandRuntime builds the command runtime from defaults for the Phase 1 compose
// network; every value can be overridden by its environment variable.
func loadCommandRuntime(ctx context.Context, store *connectivity.Store) (*commandRuntime, error) {
	ca := envOr("COMMAND_RUNTIME_CA", "/run/hvac/pki/ca.crt")
	dispatcherIdentity := workloadtls.CertificateFiles{
		CertificatePath: envOr("COMMAND_DISPATCHER_CERT", "/run/hvac/pki/command-dispatcher/tls.crt"),
		PrivateKeyPath:  envOr("COMMAND_DISPATCHER_KEY", "/run/hvac/pki/command-dispatcher/tls.key"),
	}
	verifierIdentity := workloadtls.CertificateFiles{
		CertificatePath: envOr("COMMAND_VERIFIER_CERT", "/run/hvac/pki/command-verifier/tls.crt"),
		PrivateKeyPath:  envOr("COMMAND_VERIFIER_KEY", "/run/hvac/pki/command-verifier/tls.key"),
	}
	ownerURL := envOr("COMMAND_RUNTIME_URL", "https://command-service:8447")
	ownerServerName := envOr("COMMAND_RUNTIME_SERVER_NAME", "command-service")
	telemetryURL := envOr("COMMAND_TELEMETRY_URL", "https://telemetry-runtime-service:8446")
	telemetryServerName := envOr("COMMAND_TELEMETRY_SERVER_NAME", "telemetry-runtime-service")

	dispatcherRuntimeClient, err := commandRuntimeClient(dispatcherIdentity, ca, ownerURL, ownerServerName)
	if err != nil {
		return nil, err
	}
	dispatcherTelemetryClient, err := commandHTTPClient(dispatcherIdentity, ca, telemetryServerName, 10*time.Second)
	if err != nil {
		return nil, err
	}
	safetyReader, err := commanddispatcher.NewReportedStateClient(commanddispatcher.ReportedStateClientConfig{
		BaseURL: telemetryURL, HTTPClient: dispatcherTelemetryClient,
	})
	if err != nil {
		return nil, err
	}
	safetyVerifier, err := commanddispatcher.NewAuthoritativeDispatchSafetyVerifier(safetyReader, commanddispatcher.DispatchSafetyStateKey)
	if err != nil {
		return nil, err
	}
	verifierRuntimeClient, err := commandRuntimeClient(verifierIdentity, ca, ownerURL, ownerServerName)
	if err != nil {
		return nil, err
	}
	verifierTelemetryClient, err := commandHTTPClient(verifierIdentity, ca, telemetryServerName, 10*time.Second)
	if err != nil {
		return nil, err
	}
	verificationReader, err := commanddispatcher.NewReportedStateClient(commanddispatcher.ReportedStateClientConfig{
		BaseURL: telemetryURL, HTTPClient: verifierTelemetryClient,
	})
	if err != nil {
		return nil, err
	}
	// The broker ACL grants command topics to the command-dispatcher certificate CN.
	connector, err := mqttconnector.New(ctx, mqttconnector.Config{
		BrokerURL:      envOr("CONNECTIVITY_MQTT_URL", "tls://mqtt-broker:8883"),
		ClientID:       envOr("COMMAND_MQTT_CLIENT_ID", "connectivity-commands"),
		CAFile:         ca,
		CertFile:       dispatcherIdentity.CertificatePath,
		KeyFile:        dispatcherIdentity.PrivateKeyPath,
		ServerName:     envOr("CONNECTIVITY_MQTT_SERVER_NAME", "mqtt-broker"),
		TransportState: store,
		EvidenceStore:  dispatcherRuntimeClient,
		LateResultSink: dispatcherRuntimeClient,
		ReplyTimeout:   15 * time.Second,
	})
	if err != nil {
		return nil, err
	}

	dispatcherWorkerID := envOr("COMMAND_DISPATCHER_WORKER_ID", hostnameOr("connectivity-command-dispatcher"))
	verifierWorkerID := envOr("COMMAND_VERIFIER_WORKER_ID", hostnameOr("connectivity-command-verifier"))
	return &commandRuntime{
		store:            store,
		dispatcher:       commanddispatcher.NewDurable(dispatcherRuntimeClient, safetyVerifier, connector, dispatcherWorkerID, 30*time.Second),
		dispatcherWorker: dispatcherWorkerID,
		verifier:         commanddispatcher.NewDurableVerificationWorker(verifierRuntimeClient, commanddispatcher.NewAuthoritativeReportedStateVerifier(verificationReader), verifierWorkerID, 15*time.Second),
		verifierWorker:   verifierWorkerID,
		connector:        connector,
	}, nil
}

func (runtime *commandRuntime) Run(ctx context.Context, logger *slog.Logger) {
	logger.Info("connectivity_command_runtime_started", "dispatcher_worker_id", runtime.dispatcherWorker, "verifier_worker_id", runtime.verifierWorker)
	go runtime.runForEveryTenant(ctx, logger, "dispatch", 25*time.Second, runtime.dispatcher.RunOnce, commandservice.ErrNoDispatchAvailable)
	go runtime.runForEveryTenant(ctx, logger, "verification", 12*time.Second, runtime.verifier.RunOnce, commandservice.ErrVerificationNotAvailable)
	<-ctx.Done()
}

func (runtime *commandRuntime) Close(ctx context.Context) error {
	return runtime.connector.Disconnect(ctx)
}

// runForEveryTenant drains each Tenant's work in turn and idles briefly once no Tenant
// has any.
func (runtime *commandRuntime) runForEveryTenant(ctx context.Context, logger *slog.Logger, work string, timeout time.Duration, runOnce func(context.Context, string) error, noWork error) {
	backoff := 100 * time.Millisecond
	for ctx.Err() == nil {
		tenants, err := runtime.store.Tenants(ctx)
		if err != nil {
			logger.Error("connectivity_command_tenants_unavailable", "work", work, "error", err.Error())
			sleepCommandContext(ctx, backoff)
			backoff = min(backoff*2, 5*time.Second)
			continue
		}
		found := false
		for _, tenantID := range tenants {
			runContext, cancel := context.WithTimeout(ctx, timeout)
			err := runOnce(runContext, tenantID)
			cancel()
			switch {
			case err == nil:
				found = true
				backoff = 100 * time.Millisecond
			case errors.Is(err, noWork):
			case errors.Is(err, commandservice.ErrStaleFence), errors.Is(err, commandservice.ErrCommandNotFound):
				logger.Warn("connectivity_command_stale_work_discarded", "work", work, "tenant_id", tenantID)
			case ctx.Err() != nil:
				return
			default:
				logger.Error("connectivity_command_failed", "work", work, "tenant_id", tenantID, "error", err.Error())
				sleepCommandContext(ctx, backoff)
				backoff = min(backoff*2, 5*time.Second)
			}
		}
		if !found {
			sleepCommandContext(ctx, 100*time.Millisecond)
		}
	}
}

func commandRuntimeClient(identity workloadtls.CertificateFiles, ca, baseURL, serverName string) (*commanddispatcher.RuntimeClient, error) {
	client, err := commandHTTPClient(identity, ca, serverName, 20*time.Second)
	if err != nil {
		return nil, err
	}
	return commanddispatcher.NewRuntimeClient(commanddispatcher.RuntimeClientConfig{BaseURL: baseURL, HTTPClient: client})
}

func commandHTTPClient(identity workloadtls.CertificateFiles, ca, serverName string, timeout time.Duration) (*http.Client, error) {
	return workloadtls.NewHTTPClient(workloadtls.ClientConfig{
		CertificateFiles: &identity,
		ServerCAPath:     ca,
		ServerName:       serverName,
		Timeout:          timeout,
	})
}

func sleepCommandContext(ctx context.Context, duration time.Duration) {
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
