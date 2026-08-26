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
	"github.com/quanlaihe/hvac-web/modules/command/pkg/commanddispatcher"
	"github.com/quanlaihe/hvac-web/modules/command/pkg/mqttconnector"
	"github.com/quanlaihe/hvac-web/modules/command/pkg/commandservice"
	"github.com/quanlaihe/hvac-web/modules/iot/internal/connectivity"
)

const commandOwnershipLease = 30 * time.Second

type inProcessCommandRuntime struct {
	tenantID         string
	integrationID    string
	ownershipOwnerID string
	connectivity     *connectivity.Store
	dispatcher       *commanddispatcher.DurableDispatcher
	dispatcherWorker string
	verifier         *commanddispatcher.DurableVerificationWorker
	verifierWorker   string
	reportedStateKey string
	connector        *mqttconnector.Connector
	setReady         func(bool)
}

type commandRuntimeBinding struct {
	DeviceID       string `json:"deviceId"`
	SafetyStateKey string `json:"safetyStateKey"`
}

type commandRuntimeBindings struct {
	SchemaVersion int                     `json:"schemaVersion"`
	Devices       []commandRuntimeBinding `json:"devices"`
}

func loadInProcessCommandRuntime(ctx context.Context, store *connectivity.Store, integration connectivity.IntegrationDescriptor) (*inProcessCommandRuntime, error) {
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("COMMAND_RUNTIME_IN_PROCESS_ENABLED")), "true") {
		return nil, nil
	}
	if store == nil || strings.TrimSpace(integration.ID) == "" {
		return nil, errors.New("Command Runtime requires Connectivity owner state")
	}
	bindings, err := loadCommandRuntimeBindings()
	if err != nil {
		return nil, err
	}
	deviceIDs := make([]string, 0, len(bindings.Devices))
	safetyKeyByDevice := make(map[string]string, len(bindings.Devices))
	for _, binding := range bindings.Devices {
		if _, routeErr := store.ResolveCommandRoute(ctx, integration.ID, integration.TenantID, integration.SiteID, integration.GatewayExternalID, binding.DeviceID); routeErr != nil {
			return nil, fmt.Errorf("Command Runtime Device %s has no active GatewayChildBinding", binding.DeviceID)
		}
		deviceIDs = append(deviceIDs, binding.DeviceID)
		safetyKeyByDevice[binding.DeviceID] = binding.SafetyStateKey
	}

	dispatcherIdentity, err := commandIdentity("COMMAND_RUNTIME_CLIENT_CERT", "COMMAND_RUNTIME_CLIENT_KEY")
	if err != nil {
		return nil, err
	}
	dispatcherRuntimeClient, err := commandRuntimeClient(dispatcherIdentity, integration.TenantID, integration.SiteID, deviceIDs)
	if err != nil {
		return nil, err
	}
	dispatcherS2Client, err := commandHTTPClient(dispatcherIdentity, "S2_DISPATCH_SAFETY_SERVER_CA", "S2_DISPATCH_SAFETY_SERVER_NAME", 10*time.Second)
	if err != nil {
		return nil, err
	}
	safetyReader, err := commanddispatcher.NewReportedStateClient(commanddispatcher.ReportedStateClientConfig{
		BaseURL: mustCommandEnv("S2_DISPATCH_SAFETY_URL"), HTTPClient: dispatcherS2Client,
		TenantID: integration.TenantID, SiteID: integration.SiteID, DeviceIDs: deviceIDs,
	})
	if err != nil {
		return nil, err
	}
	safetyVerifier, err := commanddispatcher.NewMappedDispatchSafetyVerifier(safetyReader, safetyKeyByDevice)
	if err != nil {
		return nil, err
	}

	ownershipOwnerID := envOr("MQTT_COMMAND_OWNER_ID", hostnameOr("iot-service-command-owner"))
	ownership, err := store.ClaimConnectorOwnership(ctx, integration.ID, ownershipOwnerID, commandOwnershipLease)
	if err != nil {
		return nil, fmt.Errorf("claim MQTT command connector ownership: %w", err)
	}
	connector, err := mqttconnector.New(ctx, mqttconnector.Config{
		BrokerURL:             integration.BrokerOrigin,
		ClientID:              envOr("MQTT_COMMAND_CLIENT_ID", hostnameOr("iot-service-command-dispatcher")),
		CAFile:                mustCommandEnv("MQTT_COMMAND_CA"),
		CertFile:              mustCommandEnv("MQTT_COMMAND_CERT"),
		KeyFile:               mustCommandEnv("MQTT_COMMAND_KEY"),
		ServerName:            mustCommandEnv("MQTT_COMMAND_SERVER_NAME"),
		IntegrationInstanceID: integration.ID,
		TenantID:              integration.TenantID,
		SiteID:                integration.SiteID,
		GatewayID:             integration.GatewayExternalID,
		OwnerID:               ownershipOwnerID,
		OwnerGeneration:       ownership.Generation,
		TransportState:        store,
		EvidenceStore:         dispatcherRuntimeClient,
		LateResultSink:        dispatcherRuntimeClient,
		ReplyTimeout:          15 * time.Second,
	})
	if err != nil {
		return nil, err
	}

	verifierIdentity, err := commandIdentity("S2_REPORTED_STATE_CLIENT_CERT", "S2_REPORTED_STATE_CLIENT_KEY")
	if err != nil {
		_ = connector.Disconnect(context.Background())
		return nil, err
	}
	verifierRuntimeClient, err := commandRuntimeClient(verifierIdentity, integration.TenantID, integration.SiteID, deviceIDs)
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
		TenantID: integration.TenantID, SiteID: integration.SiteID, DeviceIDs: deviceIDs,
	})
	if err != nil {
		_ = connector.Disconnect(context.Background())
		return nil, err
	}

	dispatcherWorkerID := envOr("COMMAND_DISPATCHER_WORKER_ID", hostnameOr("iot-service-command-dispatcher"))
	verifierWorkerID := envOr("COMMAND_VERIFIER_WORKER_ID", hostnameOr("iot-service-command-verifier"))
	return &inProcessCommandRuntime{
		tenantID:         integration.TenantID,
		integrationID:    integration.ID,
		ownershipOwnerID: ownershipOwnerID,
		connectivity:     store,
		dispatcher:       commanddispatcher.NewDurable(dispatcherRuntimeClient, safetyVerifier, connector, dispatcherWorkerID, 30*time.Second),
		dispatcherWorker: dispatcherWorkerID,
		verifier:         commanddispatcher.NewDurableVerificationWorker(verifierRuntimeClient, commanddispatcher.NewAuthoritativeReportedStateVerifier(verificationReader), verifierWorkerID, 15*time.Second),
		verifierWorker:   verifierWorkerID,
		reportedStateKey: "per-command-authoritative-feedback",
		connector:        connector,
	}, nil
}

func (runtime *inProcessCommandRuntime) SetReadySink(setReady func(bool)) {
	if runtime == nil {
		return
	}
	runtime.setReady = setReady
	if runtime.setReady != nil {
		runtime.setReady(true)
	}
}

func (runtime *inProcessCommandRuntime) Run(ctx context.Context, logger *slog.Logger) {
	if runtime == nil {
		return
	}
	logger.Info("iot_command_runtime_started", "dispatcher_worker_id", runtime.dispatcherWorker, "verifier_worker_id", runtime.verifierWorker, "tenant_id", runtime.tenantID, "integration_instance_id", runtime.integrationID, "reported_state_key", runtime.reportedStateKey)
	go runtime.runOwnershipRenewal(ctx, logger)
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

func (runtime *inProcessCommandRuntime) runOwnershipRenewal(ctx context.Context, logger *slog.Logger) {
	ticker := time.NewTicker(commandOwnershipLease / 3)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := runtime.connectivity.ClaimConnectorOwnership(ctx, runtime.integrationID, runtime.ownershipOwnerID, commandOwnershipLease); err != nil {
				if runtime.setReady != nil {
					runtime.setReady(false)
				}
				logger.Error("iot_command_connector_ownership_renewal_failed", "error_code", "COMMAND_CONNECTOR_OWNERSHIP_LOST")
			} else if runtime.setReady != nil {
				runtime.setReady(true)
			}
		}
	}
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
		return commandRuntimeBindings{}, errors.New("COMMAND_RUNTIME_BINDINGS_FILE is required for in-process Command Runtime")
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
	if bindings.SchemaVersion != 2 || len(bindings.Devices) == 0 || len(bindings.Devices) > 64 {
		return commandRuntimeBindings{}, errors.New("command runtime bindings are incomplete")
	}
	seen := make(map[string]struct{}, len(bindings.Devices))
	for index := range bindings.Devices {
		binding := &bindings.Devices[index]
		binding.DeviceID = strings.TrimSpace(binding.DeviceID)
		binding.SafetyStateKey = strings.TrimSpace(binding.SafetyStateKey)
		if binding.DeviceID == "" || binding.SafetyStateKey == "" {
			return commandRuntimeBindings{}, errors.New("command runtime binding is incomplete")
		}
		if _, duplicate := seen[binding.DeviceID]; duplicate {
			return commandRuntimeBindings{}, errors.New("command runtime binding device is duplicated")
		}
		seen[binding.DeviceID] = struct{}{}
	}
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
