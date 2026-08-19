package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

type commandRuntimeBinding struct {
	DeviceID         string `json:"deviceId"`
	ExternalDeviceID string `json:"externalDeviceId"`
	SafetyStateKey   string `json:"safetyStateKey"`
}

type commandRuntimeBindings struct {
	SchemaVersion int                     `json:"schemaVersion"`
	TenantID      string                  `json:"tenantId"`
	SiteID        string                  `json:"siteId"`
	GatewayID     string                  `json:"gatewayId"`
	Devices       []commandRuntimeBinding `json:"devices"`
}

func loadInProcessCommandRuntime(ctx context.Context) (*inProcessCommandRuntime, error) {
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("COMMAND_RUNTIME_IN_PROCESS_ENABLED")), "true") {
		return nil, nil
	}
	bindings, err := loadCommandRuntimeBindings()
	if err != nil {
		return nil, err
	}
	tenantID := bindings.TenantID
	siteID := bindings.SiteID
	gatewayID := bindings.GatewayID
	deviceIDs := make([]string, 0, len(bindings.Devices))
	externalByDevice := make(map[string]string, len(bindings.Devices))
	safetyKeyByDevice := make(map[string]string, len(bindings.Devices))
	for _, binding := range bindings.Devices {
		deviceIDs = append(deviceIDs, binding.DeviceID)
		externalByDevice[binding.DeviceID] = binding.ExternalDeviceID
		safetyKeyByDevice[binding.DeviceID] = binding.SafetyStateKey
	}

	dispatcherIdentity, err := commandIdentity("COMMAND_RUNTIME_CLIENT_CERT", "COMMAND_RUNTIME_CLIENT_KEY")
	if err != nil {
		return nil, err
	}
	dispatcherRuntimeClient, err := commandRuntimeClient(dispatcherIdentity, tenantID, siteID, deviceIDs)
	if err != nil {
		return nil, err
	}
	dispatcherS2Client, err := commandHTTPClient(dispatcherIdentity, "S2_DISPATCH_SAFETY_SERVER_CA", "S2_DISPATCH_SAFETY_SERVER_NAME", 10*time.Second)
	if err != nil {
		return nil, err
	}
	safetyReader, err := commanddispatcher.NewReportedStateClient(commanddispatcher.ReportedStateClientConfig{
		BaseURL: mustCommandEnv("S2_DISPATCH_SAFETY_URL"), HTTPClient: dispatcherS2Client,
		TenantID: tenantID, SiteID: siteID, DeviceIDs: deviceIDs,
	})
	if err != nil {
		return nil, err
	}
	safetyVerifier, err := commanddispatcher.NewMappedDispatchSafetyVerifier(safetyReader, safetyKeyByDevice)
	if err != nil {
		return nil, err
	}

	connector, err := mqttconnector.New(ctx, mqttconnector.Config{
		BrokerURL:                  mustCommandEnv("MQTT_COMMAND_BROKER_URL"),
		ClientID:                   envOr("MQTT_COMMAND_CLIENT_ID", hostnameOr("iot-service-command-dispatcher")),
		CAFile:                     mustCommandEnv("MQTT_COMMAND_CA"),
		CertFile:                   mustCommandEnv("MQTT_COMMAND_CERT"),
		KeyFile:                    mustCommandEnv("MQTT_COMMAND_KEY"),
		ServerName:                 mustCommandEnv("MQTT_COMMAND_SERVER_NAME"),
		TenantID:                   tenantID,
		SiteID:                     siteID,
		GatewayID:                  gatewayID,
		DeviceExternalIDByDeviceID: externalByDevice,
		EvidenceStore:              dispatcherRuntimeClient,
		ReplyTimeout:               15 * time.Second,
	})
	if err != nil {
		return nil, err
	}

	verifierIdentity, err := commandIdentity("S2_REPORTED_STATE_CLIENT_CERT", "S2_REPORTED_STATE_CLIENT_KEY")
	if err != nil {
		_ = connector.Disconnect(context.Background())
		return nil, err
	}
	verifierRuntimeClient, err := commandRuntimeClient(verifierIdentity, tenantID, siteID, deviceIDs)
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
		TenantID: tenantID, SiteID: siteID, DeviceIDs: deviceIDs,
	})
	if err != nil {
		_ = connector.Disconnect(context.Background())
		return nil, err
	}
	reportedStateKey := "per-command-authoritative-feedback"

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

func commandRuntimeClient(identity workloadtls.CertificateFiles, tenantID, siteID string, deviceIDs []string) (*commanddispatcher.RuntimeClient, error) {
	client, err := commandHTTPClient(identity, "COMMAND_RUNTIME_SERVER_CA", "COMMAND_RUNTIME_SERVER_NAME", 20*time.Second)
	if err != nil {
		return nil, err
	}
	return commanddispatcher.NewRuntimeClient(commanddispatcher.RuntimeClientConfig{
		BaseURL: mustCommandEnv("COMMAND_RUNTIME_URL"), HTTPClient: client,
		TenantID: tenantID, SiteID: siteID, DeviceIDs: deviceIDs,
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

func loadCommandRuntimeBindings() (commandRuntimeBindings, error) {
	path := strings.TrimSpace(os.Getenv("COMMAND_RUNTIME_BINDINGS_FILE"))
	if path == "" {
		tenantID, err := requiredCommandEnv("COMMAND_RUNTIME_TENANT_ID")
		if err != nil {
			return commandRuntimeBindings{}, err
		}
		siteID, err := requiredCommandEnv("COMMAND_RUNTIME_SITE_ID")
		if err != nil {
			return commandRuntimeBindings{}, err
		}
		deviceID, err := requiredCommandEnv("COMMAND_RUNTIME_DEVICE_ID")
		if err != nil {
			return commandRuntimeBindings{}, err
		}
		gatewayID, err := requiredCommandEnv("MQTT_COMMAND_GATEWAY_ID")
		if err != nil {
			return commandRuntimeBindings{}, err
		}
		externalDeviceID, err := requiredCommandEnv("MQTT_COMMAND_EXTERNAL_DEVICE_ID")
		if err != nil {
			return commandRuntimeBindings{}, err
		}
		safetyStateKey, err := requiredCommandEnv("COMMAND_DISPATCH_SAFETY_STATE_KEY")
		if err != nil {
			return commandRuntimeBindings{}, err
		}
		return commandRuntimeBindings{
			SchemaVersion: 1, TenantID: tenantID, SiteID: siteID, GatewayID: gatewayID,
			Devices: []commandRuntimeBinding{{DeviceID: deviceID, ExternalDeviceID: externalDeviceID, SafetyStateKey: safetyStateKey}},
		}, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return commandRuntimeBindings{}, fmt.Errorf("open command runtime bindings: %w", err)
	}
	defer file.Close()
	var bindings commandRuntimeBindings
	decoder := json.NewDecoder(io.LimitReader(file, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&bindings); err != nil {
		return commandRuntimeBindings{}, errors.New("command runtime bindings are invalid")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return commandRuntimeBindings{}, errors.New("command runtime bindings contain trailing JSON")
	}
	if bindings.SchemaVersion != 1 || strings.TrimSpace(bindings.TenantID) == "" || strings.TrimSpace(bindings.SiteID) == "" || strings.TrimSpace(bindings.GatewayID) == "" || len(bindings.Devices) == 0 || len(bindings.Devices) > 64 {
		return commandRuntimeBindings{}, errors.New("command runtime bindings are incomplete")
	}
	seen := make(map[string]struct{}, len(bindings.Devices))
	for index := range bindings.Devices {
		binding := &bindings.Devices[index]
		binding.DeviceID = strings.TrimSpace(binding.DeviceID)
		binding.ExternalDeviceID = strings.TrimSpace(binding.ExternalDeviceID)
		binding.SafetyStateKey = strings.TrimSpace(binding.SafetyStateKey)
		if binding.DeviceID == "" || binding.ExternalDeviceID == "" || binding.SafetyStateKey == "" {
			return commandRuntimeBindings{}, errors.New("command runtime binding is incomplete")
		}
		if _, duplicate := seen[binding.DeviceID]; duplicate {
			return commandRuntimeBindings{}, errors.New("command runtime binding device is duplicated")
		}
		seen[binding.DeviceID] = struct{}{}
	}
	bindings.TenantID = strings.TrimSpace(bindings.TenantID)
	bindings.SiteID = strings.TrimSpace(bindings.SiteID)
	bindings.GatewayID = strings.TrimSpace(bindings.GatewayID)
	return bindings, nil
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
