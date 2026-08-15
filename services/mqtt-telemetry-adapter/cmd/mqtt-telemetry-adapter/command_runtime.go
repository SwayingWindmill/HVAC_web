package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/workloadtls"
	"github.com/quanlaihe/hvac-web/services/command-dispatcher/pkg/commanddispatcher"
	"github.com/quanlaihe/hvac-web/services/command-dispatcher/pkg/mqttconnector"
	"github.com/quanlaihe/hvac-web/services/command-service/pkg/commandservice"
)

type inProcessCommandRuntime struct {
	tenantID         string
	dispatcher       *commanddispatcher.DurableDispatcher
	dispatcherWorker string
	verifier         *commanddispatcher.DurableVerificationWorker
	verifierWorker   string
	reportedStateKey string
	connector        *mqttconnector.Connector
}

func loadInProcessCommandRuntime(ctx context.Context) (*inProcessCommandRuntime, error) {
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("COMMAND_RUNTIME_IN_PROCESS_ENABLED")), "true") {
		return nil, nil
	}
	tenantID, err := requiredCommandEnv("COMMAND_RUNTIME_TENANT_ID")
	if err != nil {
		return nil, err
	}
	siteID, err := requiredCommandEnv("COMMAND_RUNTIME_SITE_ID")
	if err != nil {
		return nil, err
	}
	deviceID, err := requiredCommandEnv("COMMAND_RUNTIME_DEVICE_ID")
	if err != nil {
		return nil, err
	}
	gatewayID, err := requiredCommandEnv("MQTT_COMMAND_GATEWAY_ID")
	if err != nil {
		return nil, err
	}
	externalDeviceID, err := requiredCommandEnv("MQTT_COMMAND_EXTERNAL_DEVICE_ID")
	if err != nil {
		return nil, err
	}

	dispatcherIdentity, err := commandIdentity("COMMAND_RUNTIME_CLIENT_CERT", "COMMAND_RUNTIME_CLIENT_KEY")
	if err != nil {
		return nil, err
	}
	dispatcherRuntimeClient, err := commandRuntimeClient(dispatcherIdentity, tenantID, siteID, deviceID)
	if err != nil {
		return nil, err
	}
	dispatcherS2Client, err := commandHTTPClient(dispatcherIdentity, "S2_DISPATCH_SAFETY_SERVER_CA", "S2_DISPATCH_SAFETY_SERVER_NAME", 10*time.Second)
	if err != nil {
		return nil, err
	}
	safetyReader, err := commanddispatcher.NewReportedStateClient(commanddispatcher.ReportedStateClientConfig{
		BaseURL: mustCommandEnv("S2_DISPATCH_SAFETY_URL"), HTTPClient: dispatcherS2Client,
		TenantID: tenantID, SiteID: siteID, DeviceID: deviceID,
	})
	if err != nil {
		return nil, err
	}
	safetyStateKey, err := requiredCommandEnv("COMMAND_DISPATCH_SAFETY_STATE_KEY")
	if err != nil {
		return nil, err
	}
	safetyVerifier, err := commanddispatcher.NewAuthoritativeDispatchSafetyVerifier(safetyReader, safetyStateKey)
	if err != nil {
		return nil, err
	}

	connector, err := mqttconnector.New(ctx, mqttconnector.Config{
		BrokerURL:  mustCommandEnv("MQTT_COMMAND_BROKER_URL"),
		ClientID:   envOr("MQTT_COMMAND_CLIENT_ID", hostnameOr("iot-service-command-dispatcher")),
		CAFile:     mustCommandEnv("MQTT_COMMAND_CA"),
		CertFile:   mustCommandEnv("MQTT_COMMAND_CERT"),
		KeyFile:    mustCommandEnv("MQTT_COMMAND_KEY"),
		ServerName: mustCommandEnv("MQTT_COMMAND_SERVER_NAME"),
		TenantID:   tenantID,
		SiteID:     siteID,
		GatewayID:  gatewayID,
		DeviceExternalIDByDeviceID: map[string]string{
			deviceID: externalDeviceID,
		},
		EvidenceStore: dispatcherRuntimeClient,
		ReplyTimeout:  15 * time.Second,
	})
	if err != nil {
		return nil, err
	}

	verifierIdentity, err := commandIdentity("S2_REPORTED_STATE_CLIENT_CERT", "S2_REPORTED_STATE_CLIENT_KEY")
	if err != nil {
		_ = connector.Disconnect(context.Background())
		return nil, err
	}
	verifierRuntimeClient, err := commandRuntimeClient(verifierIdentity, tenantID, siteID, deviceID)
	if err != nil {
		_ = connector.Disconnect(context.Background())
		return nil, err
	}
	verifierS2Client, err := commandHTTPClient(verifierIdentity, "S2_REPORTED_STATE_SERVER_CA", "S2_REPORTED_STATE_SERVER_NAME", 10*time.Second)
	if err != nil {
		_ = connector.Disconnect(context.Background())
		return nil, err
	}
	verificationReader, err := commanddispatcher.NewReportedStateClient(commanddispatcher.ReportedStateClientConfig{
		BaseURL: mustCommandEnv("S2_REPORTED_STATE_URL"), HTTPClient: verifierS2Client,
		TenantID: tenantID, SiteID: siteID, DeviceID: deviceID,
	})
	if err != nil {
		_ = connector.Disconnect(context.Background())
		return nil, err
	}
	reportedStateKey, err := requiredCommandEnv("COMMAND_VERIFICATION_REPORTED_STATE_KEY")
	if err != nil {
		_ = connector.Disconnect(context.Background())
		return nil, err
	}

	dispatcherWorkerID := envOr("COMMAND_DISPATCHER_WORKER_ID", hostnameOr("iot-service-command-dispatcher"))
	verifierWorkerID := envOr("COMMAND_VERIFIER_WORKER_ID", hostnameOr("iot-service-command-verifier"))
	return &inProcessCommandRuntime{
		tenantID:         tenantID,
		dispatcher:       commanddispatcher.NewDurable(dispatcherRuntimeClient, safetyVerifier, connector, dispatcherWorkerID, 30*time.Second),
		dispatcherWorker: dispatcherWorkerID,
		verifier:         commanddispatcher.NewDurableVerificationWorker(verifierRuntimeClient, commanddispatcher.NewAuthoritativeReportedStateVerifier(verificationReader), verifierWorkerID, 15*time.Second),
		verifierWorker:   verifierWorkerID,
		reportedStateKey: reportedStateKey,
		connector:        connector,
	}, nil
}

func (runtime *inProcessCommandRuntime) Run(ctx context.Context, logger *slog.Logger) {
	if runtime == nil {
		return
	}
	logger.Info("iot_command_runtime_started", "dispatcher_worker_id", runtime.dispatcherWorker, "verifier_worker_id", runtime.verifierWorker, "tenant_id", runtime.tenantID, "reported_state_key", runtime.reportedStateKey)
	go runtime.runDispatcher(ctx, logger)
	go runtime.runVerifier(ctx, logger)
	<-ctx.Done()
}

func (runtime *inProcessCommandRuntime) Close(ctx context.Context) error {
	if runtime == nil || runtime.connector == nil {
		return nil
	}
	return runtime.connector.Disconnect(ctx)
}

func (runtime *inProcessCommandRuntime) runDispatcher(ctx context.Context, logger *slog.Logger) {
	backoff := 100 * time.Millisecond
	for ctx.Err() == nil {
		runContext, cancel := context.WithTimeout(ctx, 25*time.Second)
		err := runtime.dispatcher.RunOnce(runContext, runtime.tenantID)
		cancel()
		switch {
		case err == nil:
			backoff = 100 * time.Millisecond
		case errors.Is(err, commandservice.ErrNoDispatchAvailable):
			sleepCommandContext(ctx, 100*time.Millisecond)
		case errors.Is(err, commandservice.ErrStaleFence), errors.Is(err, commandservice.ErrCommandNotFound):
			logger.Warn("iot_command_dispatch_stale_work_discarded", "error_code", "COMMAND_RUNTIME_STALE_WORK")
		case ctx.Err() != nil:
			return
		default:
			logger.Error("iot_command_dispatch_failed", "error_code", "COMMAND_DISPATCH_FAILED")
			sleepCommandContext(ctx, backoff)
			if backoff < 5*time.Second {
				backoff *= 2
			}
		}
	}
}

func (runtime *inProcessCommandRuntime) runVerifier(ctx context.Context, logger *slog.Logger) {
	backoff := 100 * time.Millisecond
	for ctx.Err() == nil {
		runContext, cancel := context.WithTimeout(ctx, 12*time.Second)
		err := runtime.verifier.RunOnce(runContext, runtime.tenantID)
		cancel()
		switch {
		case err == nil:
			backoff = 100 * time.Millisecond
		case errors.Is(err, commandservice.ErrVerificationNotAvailable):
			sleepCommandContext(ctx, 100*time.Millisecond)
		case errors.Is(err, commandservice.ErrStaleFence), errors.Is(err, commandservice.ErrCommandNotFound):
			logger.Warn("iot_command_verifier_stale_work_discarded", "error_code", "COMMAND_VERIFICATION_STALE_WORK")
		case ctx.Err() != nil:
			return
		default:
			logger.Error("iot_command_verification_failed", "error_code", "COMMAND_VERIFICATION_FAILED")
			sleepCommandContext(ctx, backoff)
			if backoff < 5*time.Second {
				backoff *= 2
			}
		}
	}
}

func commandRuntimeClient(identity workloadtls.CertificateFiles, tenantID, siteID, deviceID string) (*commanddispatcher.RuntimeClient, error) {
	client, err := commandHTTPClient(identity, "COMMAND_RUNTIME_SERVER_CA", "COMMAND_RUNTIME_SERVER_NAME", 20*time.Second)
	if err != nil {
		return nil, err
	}
	return commanddispatcher.NewRuntimeClient(commanddispatcher.RuntimeClientConfig{
		BaseURL: mustCommandEnv("COMMAND_RUNTIME_URL"), HTTPClient: client,
		TenantID: tenantID, SiteID: siteID, DeviceID: deviceID,
	})
}

func commandHTTPClient(identity workloadtls.CertificateFiles, caEnv, serverNameEnv string, timeout time.Duration) (*http.Client, error) {
	return workloadtls.NewHTTPClient(workloadtls.ClientConfig{
		CertificateFiles: &identity,
		ServerCAPath:     mustCommandEnv(caEnv),
		ServerName:       mustCommandEnv(serverNameEnv),
		Timeout:          timeout,
	})
}

func commandIdentity(certEnv, keyEnv string) (workloadtls.CertificateFiles, error) {
	cert, err := requiredCommandEnv(certEnv)
	if err != nil {
		return workloadtls.CertificateFiles{}, err
	}
	key, err := requiredCommandEnv(keyEnv)
	if err != nil {
		return workloadtls.CertificateFiles{}, err
	}
	return workloadtls.CertificateFiles{CertificatePath: cert, PrivateKeyPath: key}, nil
}

func requiredCommandEnv(name string) (string, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return "", fmt.Errorf("%s is required for in-process Command Runtime", name)
	}
	return value, nil
}

func mustCommandEnv(name string) string {
	value, _ := requiredCommandEnv(name)
	return value
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
