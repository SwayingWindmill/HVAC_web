package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
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
		ReplyTimeout:   25 * time.Second,
	})
	if err != nil {
		return nil, err
	}

	dispatcherWorkerID := envOr("COMMAND_DISPATCHER_WORKER_ID", hostnameOr("connectivity-command-dispatcher"))
	verifierWorkerID := envOr("COMMAND_VERIFIER_WORKER_ID", hostnameOr("connectivity-command-verifier"))
	return &commandRuntime{
		store:            store,
		dispatcher:       commanddispatcher.NewDurable(dispatcherRuntimeClient, connector, dispatcherWorkerID, 45*time.Second),
		dispatcherWorker: dispatcherWorkerID,
		verifier:         commanddispatcher.NewDurableVerificationWorker(verifierRuntimeClient, commanddispatcher.NewAuthoritativeReportedStateVerifier(verificationReader), verifierWorkerID, 5*time.Second),
		verifierWorker:   verifierWorkerID,
		connector:        connector,
	}, nil
}

func (runtime *commandRuntime) Run(ctx context.Context, logger *slog.Logger) {
	logger.Info("connectivity_command_runtime_started", "dispatcher_worker_id", runtime.dispatcherWorker, "verifier_worker_id", runtime.verifierWorker)
	go runtime.runForEveryTenant(ctx, logger, "dispatch", 40*time.Second, runtime.dispatcher.RunOnce, commandservice.ErrNoDispatchAvailable)
	go runtime.runForEveryTenant(ctx, logger, "verification", 12*time.Second, runtime.verifier.RunOnce, commandservice.ErrVerificationNotAvailable)
	<-ctx.Done()
}

func (runtime *commandRuntime) Close(ctx context.Context) error {
	return runtime.connector.Disconnect(ctx)
}

const (
	maxConcurrentCommandTenants = 4
	commandTenantBaseBackoff    = 100 * time.Millisecond
	commandTenantMaxBackoff     = 5 * time.Second
	commandTenantsRefreshWindow = 500 * time.Millisecond
)

type tenantRunOutcome struct {
	tenantID string
	err      error
}

// runForEveryTenant schedules each Tenant's work concurrently up to maxConcurrentCommandTenants.
func (runtime *commandRuntime) runForEveryTenant(ctx context.Context, logger *slog.Logger, work string, timeout time.Duration, runOnce func(context.Context, string) error, noWork error) {
	inFlight := make(map[string]bool)
	readyAt := make(map[string]time.Time)
	backoffByTenant := make(map[string]time.Duration)
	doneCh := make(chan tenantRunOutcome, maxConcurrentCommandTenants)
	var wg sync.WaitGroup
	defer wg.Wait()

	var cachedTenants []string
	var tenantsFetchedAt time.Time
	var tenantsReadyAt time.Time
	tenantsBackoff := commandTenantBaseBackoff
	cursor := 0

	applyOutcome := func(outcome tenantRunOutcome) {
		delete(inFlight, outcome.tenantID)
		switch {
		case outcome.err == nil:
			delete(readyAt, outcome.tenantID)
			backoffByTenant[outcome.tenantID] = commandTenantBaseBackoff
		case errors.Is(outcome.err, noWork):
			readyAt[outcome.tenantID] = time.Now().Add(commandTenantBaseBackoff)
			backoffByTenant[outcome.tenantID] = commandTenantBaseBackoff
		case errors.Is(outcome.err, commandservice.ErrStaleFence), errors.Is(outcome.err, commandservice.ErrCommandNotFound):
			logger.Warn("connectivity_command_stale_work_discarded", "work", work, "tenant_id", outcome.tenantID)
			readyAt[outcome.tenantID] = time.Now().Add(commandTenantBaseBackoff)
			backoffByTenant[outcome.tenantID] = commandTenantBaseBackoff
		case ctx.Err() != nil:
		default:
			backoff := backoffByTenant[outcome.tenantID]
			if backoff <= 0 {
				backoff = commandTenantBaseBackoff
			}
			logger.Error("connectivity_command_failed", "work", work, "tenant_id", outcome.tenantID, "error", outcome.err.Error())
			readyAt[outcome.tenantID] = time.Now().Add(backoff)
			backoffByTenant[outcome.tenantID] = min(backoff*2, commandTenantMaxBackoff)
		}
	}

	drainDone := func() {
		for {
			select {
			case outcome := <-doneCh:
				applyOutcome(outcome)
			default:
				return
			}
		}
	}

	for ctx.Err() == nil {
		drainDone()
		now := time.Now()
		if !now.Before(tenantsReadyAt) && (tenantsFetchedAt.IsZero() || !now.Before(tenantsFetchedAt.Add(commandTenantsRefreshWindow))) {
			tenants, err := runtime.store.Tenants(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				logger.Error("connectivity_command_tenants_unavailable", "work", work, "error", err.Error())
				tenantsReadyAt = time.Now().Add(tenantsBackoff)
				tenantsBackoff = min(tenantsBackoff*2, commandTenantMaxBackoff)
			} else {
				cachedTenants = tenants
				tenantsFetchedAt = time.Now()
				tenantsReadyAt = time.Time{}
				tenantsBackoff = commandTenantBaseBackoff
				pruneTenantMaps(cachedTenants, readyAt, backoffByTenant)
			}
		}

		now = time.Now()
		for i := 0; i < len(cachedTenants) && len(inFlight) < maxConcurrentCommandTenants; i++ {
			idx := (cursor + i) % len(cachedTenants)
			tenantID := cachedTenants[idx]
			if inFlight[tenantID] {
				continue
			}
			if nextAt, ok := readyAt[tenantID]; ok && now.Before(nextAt) {
				continue
			}
			inFlight[tenantID] = true
			cursor = (idx + 1) % len(cachedTenants)
			wg.Add(1)
			go func(tid string) {
				defer wg.Done()
				runContext, cancel := context.WithTimeout(ctx, timeout)
				err := runOnce(runContext, tid)
				cancel()
				doneCh <- tenantRunOutcome{tenantID: tid, err: err}
			}(tenantID)
		}

		if len(inFlight) >= maxConcurrentCommandTenants {
			select {
			case <-ctx.Done():
				return
			case outcome := <-doneCh:
				applyOutcome(outcome)
			}
			continue
		}

		waitFor := nextTenantWakeDelay(now, cachedTenants, inFlight, readyAt, tenantsReadyAt)
		timer := time.NewTimer(waitFor)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case outcome := <-doneCh:
			timer.Stop()
			applyOutcome(outcome)
		case <-timer.C:
		}
	}
}

func nextTenantWakeDelay(now time.Time, tenants []string, inFlight map[string]bool, readyAt map[string]time.Time, tenantsReadyAt time.Time) time.Duration {
	if len(tenants) == 0 {
		if tenantsReadyAt.After(now) {
			return tenantsReadyAt.Sub(now)
		}
		return commandTenantBaseBackoff
	}
	waitFor := commandTenantBaseBackoff
	for _, tenantID := range tenants {
		if inFlight[tenantID] {
			continue
		}
		nextAt, ok := readyAt[tenantID]
		if !ok {
			return time.Millisecond
		}
		if d := nextAt.Sub(now); d < waitFor {
			waitFor = d
		}
	}
	if waitFor < time.Millisecond {
		return time.Millisecond
	}
	return waitFor
}

func pruneTenantMaps(tenants []string, readyAt map[string]time.Time, backoffByTenant map[string]time.Duration) {
	active := make(map[string]bool, len(tenants))
	for _, tenantID := range tenants {
		active[tenantID] = true
	}
	for tenantID := range readyAt {
		if !active[tenantID] {
			delete(readyAt, tenantID)
		}
	}
	for tenantID := range backoffByTenant {
		if !active[tenantID] {
			delete(backoffByTenant, tenantID)
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

func hostnameOr(fallback string) string {
	value, err := os.Hostname()
	if err != nil || strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
