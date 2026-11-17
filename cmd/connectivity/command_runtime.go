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

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/workloadtls"
	"github.com/quanlaihe/hvac-web/modules/command/pkg/commanddispatcher"
	"github.com/quanlaihe/hvac-web/modules/command/pkg/commandservice"
	"github.com/quanlaihe/hvac-web/modules/command/pkg/mqttconnector"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/connectivity"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/packettap"
)

type commandTenantLister interface {
	Tenants(ctx context.Context) ([]string, error)
}

// commandRuntime dispatches and verifies commands for every Tenant with a Gateway. Which
// Devices may be controlled and where their commands go both come from the Registry.
type commandRuntime struct {
	store            *connectivity.Store
	tenantLister     commandTenantLister
	dispatcher       *commanddispatcher.DurableDispatcher
	dispatcherWorker string
	verifier         *commanddispatcher.DurableVerificationWorker
	verifierWorker   string
	dispatchFn       func(context.Context, string) error
	verifyFn         func(context.Context, string) error
	connector        *mqttconnector.Connector
	metrics          *observability.Registry
	doneMu           sync.Mutex
	done             chan struct{}
}

// loadCommandRuntime builds the command runtime from defaults for the Phase 1 compose
// network; every value can be overridden by its environment variable.
func loadCommandRuntime(ctx context.Context, store *connectivity.Store, logger *slog.Logger, metrics *observability.Registry, tap *packettap.Manager) (*commandRuntime, error) {
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
	var packetHook func(direction, channel, gatewayID, topic, traceID string, payload []byte)
	if tap != nil {
		packetHook = tap.Record
	}
	// The broker ACL grants command topics to the command-dispatcher certificate CN.
	connector, err := mqttconnector.New(ctx, mqttconnector.Config{
		BrokerURL:               envOr("CONNECTIVITY_MQTT_URL", "tls://mqtt-broker:8883"),
		ClientID:                defaultCommandMQTTClientID(),
		SharedSubscriptionGroup: commandSharedSubscriptionGroup(),
		CAFile:                  ca,
		CertFile:                dispatcherIdentity.CertificatePath,
		KeyFile:                 dispatcherIdentity.PrivateKeyPath,
		ServerName:              envOr("CONNECTIVITY_MQTT_SERVER_NAME", "mqtt-broker"),
		TransportState:          store,
		EvidenceStore:           dispatcherRuntimeClient,
		LateResultSink:          dispatcherRuntimeClient,
		ReplyTimeout:            25 * time.Second,
		Logger:                  logger,
		Metrics:                 metrics,
		PacketHook:              packetHook,
	})
	if err != nil {
		return nil, err
	}

	dispatcherWorkerID := envOr("COMMAND_DISPATCHER_WORKER_ID", hostnameOr("connectivity-command-dispatcher"))
	verifierWorkerID := envOr("COMMAND_VERIFIER_WORKER_ID", hostnameOr("connectivity-command-verifier"))
	return &commandRuntime{
		store:            store,
		tenantLister:     store,
		dispatcher:       commanddispatcher.NewDurable(dispatcherRuntimeClient, connector, dispatcherWorkerID, 45*time.Second),
		dispatcherWorker: dispatcherWorkerID,
		verifier:         commanddispatcher.NewDurableVerificationWorker(verifierRuntimeClient, commanddispatcher.NewAuthoritativeReportedStateVerifier(verificationReader), verifierWorkerID, 5*time.Second),
		verifierWorker:   verifierWorkerID,
		connector:        connector,
		metrics:          metrics,
	}, nil
}

func (runtime *commandRuntime) dispatchOnce() func(context.Context, string) error {
	if runtime.dispatchFn != nil {
		return runtime.dispatchFn
	}
	if runtime.dispatcher != nil {
		return runtime.dispatcher.RunOnce
	}
	return nil
}

func (runtime *commandRuntime) verifyOnce() func(context.Context, string) error {
	if runtime.verifyFn != nil {
		return runtime.verifyFn
	}
	if runtime.verifier != nil {
		return runtime.verifier.RunOnce
	}
	return nil
}

func (runtime *commandRuntime) listTenants(ctx context.Context) ([]string, error) {
	if runtime.tenantLister != nil {
		return runtime.tenantLister.Tenants(ctx)
	}
	if runtime.store != nil {
		return runtime.store.Tenants(ctx)
	}
	return nil, errors.New("command tenant lister unavailable")
}

func (runtime *commandRuntime) resetDone() chan struct{} {
	done := make(chan struct{})
	runtime.doneMu.Lock()
	runtime.done = done
	runtime.doneMu.Unlock()
	return done
}

func (runtime *commandRuntime) Wait(ctx context.Context) error {
	if runtime == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	runtime.doneMu.Lock()
	done := runtime.done
	runtime.doneMu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (runtime *commandRuntime) Run(ctx context.Context, logger *slog.Logger) {
	runtime.RunWithDrain(ctx, ctx, logger)
}

// RunWithDrain stops claiming new command work once claimCtx closes while letting
// in-flight dispatch and verification turns complete within execCtx.
func (runtime *commandRuntime) RunWithDrain(claimCtx, execCtx context.Context, logger *slog.Logger) {
	if runtime == nil {
		return
	}
	if logger == nil {
		logger = slog.Default()
	}
	if claimCtx == nil {
		claimCtx = context.Background()
	}
	if execCtx == nil {
		execCtx = claimCtx
	}
	done := runtime.resetDone()
	defer close(done)

	dispatchFn := runtime.dispatchOnce()
	verifyFn := runtime.verifyOnce()
	logger.Info("connectivity_command_runtime_started", "dispatcher_worker_id", runtime.dispatcherWorker, "verifier_worker_id", runtime.verifierWorker)
	var workers sync.WaitGroup
	if dispatchFn != nil {
		workers.Add(1)
		observability.GoSafe(logger, runtime.metrics, "connectivity.command_dispatch", func() {
			defer workers.Done()
			runtime.runForEveryTenantLoop(claimCtx, execCtx, logger, "dispatch", 40*time.Second, dispatchFn, commandservice.ErrNoDispatchAvailable)
		})
	}
	if verifyFn != nil {
		workers.Add(1)
		observability.GoSafe(logger, runtime.metrics, "connectivity.command_verification", func() {
			defer workers.Done()
			runtime.runForEveryTenantLoop(claimCtx, execCtx, logger, "verification", 12*time.Second, verifyFn, commandservice.ErrVerificationNotAvailable)
		})
	}
	<-claimCtx.Done()
	workers.Wait()
}

func (runtime *commandRuntime) runForEveryTenantLoop(claimCtx, execCtx context.Context, logger *slog.Logger, work string, timeout time.Duration, runOnce func(context.Context, string) error, noWork error) {
	for claimCtx.Err() == nil {
		panicked := false
		func() {
			defer observability.Recover(logger, runtime.metrics, "connectivity.command_"+work, func(any) {
				panicked = true
			})
			runtime.runForEveryTenant(claimCtx, execCtx, logger, work, timeout, runOnce, noWork)
		}()
		if !panicked || claimCtx.Err() != nil {
			return
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-claimCtx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (runtime *commandRuntime) Close(ctx context.Context) error {
	if runtime == nil || runtime.connector == nil {
		return nil
	}
	return runtime.connector.Disconnect(ctx)
}

const (
	maxConcurrentCommandTenants = 4
	commandTenantBaseBackoff    = 100 * time.Millisecond
	commandIdleMaxBackoff       = 1500 * time.Millisecond
	commandTenantMaxBackoff     = 5 * time.Second
	commandTenantsRefreshWindow = 5 * time.Second
)

type tenantRunOutcome struct {
	tenantID string
	err      error
}

// runForEveryTenant schedules each Tenant's work concurrently up to maxConcurrentCommandTenants.
func (runtime *commandRuntime) runForEveryTenant(claimCtx, execCtx context.Context, logger *slog.Logger, work string, timeout time.Duration, runOnce func(context.Context, string) error, noWork error) {
	inFlight := make(map[string]bool)
	readyAt := make(map[string]time.Time)
	backoffByTenant := make(map[string]time.Duration)
	idleBackoffByTenant := make(map[string]time.Duration)
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
			idleBackoffByTenant[outcome.tenantID] = commandTenantBaseBackoff
		case errors.Is(outcome.err, noWork):
			backoffByTenant[outcome.tenantID] = commandTenantBaseBackoff
			idle := nextIdleBackoff(idleBackoffByTenant[outcome.tenantID])
			readyAt[outcome.tenantID] = time.Now().Add(idle)
			idleBackoffByTenant[outcome.tenantID] = min(idle*2, commandIdleMaxBackoff)
		case errors.Is(outcome.err, commandservice.ErrStaleFence), errors.Is(outcome.err, commandservice.ErrCommandNotFound):
			logger.Warn("connectivity_command_stale_work_discarded", "work", work, "tenant_id", outcome.tenantID)
			readyAt[outcome.tenantID] = time.Now().Add(commandTenantBaseBackoff)
			backoffByTenant[outcome.tenantID] = commandTenantBaseBackoff
			idleBackoffByTenant[outcome.tenantID] = commandTenantBaseBackoff
		case execCtx.Err() != nil:
		default:
			backoff := backoffByTenant[outcome.tenantID]
			if backoff <= 0 {
				backoff = commandTenantBaseBackoff
			}
			logger.Error("connectivity_command_failed", "work", work, "tenant_id", outcome.tenantID, "error", outcome.err.Error())
			readyAt[outcome.tenantID] = time.Now().Add(backoff)
			backoffByTenant[outcome.tenantID] = min(backoff*2, commandTenantMaxBackoff)
			idleBackoffByTenant[outcome.tenantID] = commandTenantBaseBackoff
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

	for claimCtx.Err() == nil {
		drainDone()
		now := time.Now()
		if !now.Before(tenantsReadyAt) && (tenantsFetchedAt.IsZero() || !now.Before(tenantsFetchedAt.Add(commandTenantsRefreshWindow))) {
			tenants, err := runtime.listTenants(claimCtx)
			if err != nil {
				if claimCtx.Err() != nil {
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
				pruneTenantMaps(cachedTenants, readyAt, backoffByTenant, idleBackoffByTenant)
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
				panicked := false
				defer observability.Recover(logger, runtime.metrics, "connectivity.command_tenant_worker", func(any) {
					panicked = true
					doneCh <- tenantRunOutcome{tenantID: tid, err: errors.New("command worker panic recovered")}
				})
				runContext, cancel := context.WithTimeout(execCtx, timeout)
				err := runOnce(runContext, tid)
				cancel()
				if !panicked {
					doneCh <- tenantRunOutcome{tenantID: tid, err: err}
				}
			}(tenantID)
		}

		if len(inFlight) >= maxConcurrentCommandTenants {
			select {
			case <-claimCtx.Done():
				return
			case outcome := <-doneCh:
				applyOutcome(outcome)
			}
			continue
		}

		waitFor := nextTenantWakeDelay(now, cachedTenants, inFlight, readyAt, tenantsReadyAt, tenantsFetchedAt)
		timer := time.NewTimer(waitFor)
		select {
		case <-claimCtx.Done():
			timer.Stop()
			return
		case outcome := <-doneCh:
			timer.Stop()
			applyOutcome(outcome)
		case <-timer.C:
		}
	}
}

func nextIdleBackoff(current time.Duration) time.Duration {
	if current <= 0 {
		return commandTenantBaseBackoff
	}
	if current > commandIdleMaxBackoff {
		return commandIdleMaxBackoff
	}
	return current
}

func nextTenantWakeDelay(now time.Time, tenants []string, inFlight map[string]bool, readyAt map[string]time.Time, tenantsReadyAt, tenantsFetchedAt time.Time) time.Duration {
	waitFor := commandTenantMaxBackoff
	if !tenantsReadyAt.IsZero() && tenantsReadyAt.After(now) {
		waitFor = min(waitFor, tenantsReadyAt.Sub(now))
	} else if !tenantsFetchedAt.IsZero() {
		refreshAt := tenantsFetchedAt.Add(commandTenantsRefreshWindow)
		if !refreshAt.After(now) {
			return time.Millisecond
		}
		waitFor = min(waitFor, refreshAt.Sub(now))
	}
	if len(tenants) == 0 {
		if waitFor < time.Millisecond {
			return time.Millisecond
		}
		return waitFor
	}
	hasIdleCandidate := false
	for _, tenantID := range tenants {
		if inFlight[tenantID] {
			continue
		}
		nextAt, ok := readyAt[tenantID]
		if !ok {
			return time.Millisecond
		}
		hasIdleCandidate = true
		if d := nextAt.Sub(now); d < waitFor {
			waitFor = d
		}
	}
	if !hasIdleCandidate {
		waitFor = min(waitFor, commandTenantBaseBackoff)
	}
	if waitFor < time.Millisecond {
		return time.Millisecond
	}
	return waitFor
}

func pruneTenantMaps(tenants []string, readyAt map[string]time.Time, backoffByTenant, idleBackoffByTenant map[string]time.Duration) {
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
	for tenantID := range idleBackoffByTenant {
		if !active[tenantID] {
			delete(idleBackoffByTenant, tenantID)
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

func defaultCommandMQTTClientID() string {
	if explicit := strings.TrimSpace(os.Getenv("COMMAND_MQTT_CLIENT_ID")); explicit != "" {
		return explicit
	}
	hostname, err := os.Hostname()
	if err != nil {
		return "connectivity-commands"
	}
	return defaultCommandMQTTClientIDForHost(hostname)
}

func defaultCommandMQTTClientIDForHost(hostname string) string {
	suffix := sanitizeClientID(hostname)
	if suffix == "" {
		return "connectivity-commands"
	}
	return "connectivity-commands-" + suffix
}

func sanitizeClientID(raw string) string {
	var builder strings.Builder
	builder.Grow(len(raw))
	lastDash := false
	for _, ch := range strings.TrimSpace(raw) {
		switch {
		case (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_':
			builder.WriteRune(ch)
			lastDash = false
		default:
			if builder.Len() > 0 && !lastDash {
				builder.WriteRune('-')
				lastDash = true
			}
		}
		if builder.Len() >= 64 {
			break
		}
	}
	return strings.Trim(builder.String(), "-_")
}

func commandSharedSubscriptionGroup() string {
	raw, ok := os.LookupEnv("COMMAND_MQTT_SHARE_GROUP")
	if !ok {
		return "command-dispatcher"
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || strings.EqualFold(trimmed, "off") || strings.EqualFold(trimmed, "none") {
		return ""
	}
	return trimmed
}
