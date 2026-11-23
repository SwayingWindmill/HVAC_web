package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strconv"
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

// commandRuntime dispatches and verifies commands for every Tenant with a Gateway. Which
// Devices may be controlled and where their commands go both come from the Registry.
type commandRuntime struct {
	store               *connectivity.Store
	dispatcher          *commanddispatcher.DurableDispatcher
	dispatcherClient    *commanddispatcher.RuntimeClient
	dispatcherWorker    string
	verifier            *commanddispatcher.DurableVerificationWorker
	verifierWorker      string
	dispatchAnyFn       func(context.Context, time.Duration) error
	verifyAnyFn         func(context.Context, time.Duration) error
	dispatchWorkers     int
	verificationWorkers int
	connector           *mqttconnector.Connector
	metrics             *observability.Registry
	doneMu              sync.Mutex
	done                chan struct{}
}

// loadCommandRuntime builds the command runtime from defaults for the Phase 1 compose
// network; every value can be overridden by its environment variable.
func loadCommandRuntime(ctx context.Context, store *connectivity.Store, logger *slog.Logger, metrics *observability.Registry, tracer *observability.Tracer, tap *packettap.Manager) (*commandRuntime, error) {
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
		Tracer:                  tracer,
		PacketHook:              packetHook,
	})
	if err != nil {
		return nil, err
	}

	dispatcherWorkerID := envOr("COMMAND_DISPATCHER_WORKER_ID", hostnameOr("connectivity-command-dispatcher"))
	verifierWorkerID := envOr("COMMAND_VERIFIER_WORKER_ID", hostnameOr("connectivity-command-verifier"))
	dispatcher := commanddispatcher.NewDurable(dispatcherRuntimeClient, connector, dispatcherWorkerID, 45*time.Second)
	verifier := commanddispatcher.NewDurableVerificationWorker(verifierRuntimeClient, commanddispatcher.NewAuthoritativeReportedStateVerifier(verificationReader), verifierWorkerID, 5*time.Second)
	return &commandRuntime{
		store:               store,
		dispatcher:          dispatcher,
		dispatcherClient:    dispatcherRuntimeClient,
		dispatcherWorker:    dispatcherWorkerID,
		verifier:            verifier,
		verifierWorker:      verifierWorkerID,
		dispatchAnyFn:       dispatcher.RunAnyOnce,
		verifyAnyFn:         verifier.RunAnyOnce,
		dispatchWorkers:     parseWorkerPoolCount("COMMAND_DISPATCH_WORKERS", defaultDispatchWorkers),
		verificationWorkers: parseWorkerPoolCount("COMMAND_VERIFICATION_WORKERS", defaultVerificationWorkers),
		connector:           connector,
		metrics:             metrics,
	}, nil
}

func parseWorkerPoolCount(envKey string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(envKey))
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed < 1 {
		return fallback
	}
	if parsed > maxCommandPoolWorkers {
		return maxCommandPoolWorkers
	}
	return parsed
}

func (runtime *commandRuntime) dispatchAnyOnce() func(context.Context, time.Duration) error {
	if runtime.dispatchAnyFn != nil {
		return runtime.dispatchAnyFn
	}
	if runtime.dispatcher != nil {
		return runtime.dispatcher.RunAnyOnce
	}
	return nil
}

func (runtime *commandRuntime) verifyAnyOnce() func(context.Context, time.Duration) error {
	if runtime.verifyAnyFn != nil {
		return runtime.verifyAnyFn
	}
	if runtime.verifier != nil {
		return runtime.verifier.RunAnyOnce
	}
	return nil
}

func (runtime *commandRuntime) effectiveDispatchWorkers() int {
	if runtime != nil && runtime.dispatchWorkers > 0 {
		return runtime.dispatchWorkers
	}
	return defaultDispatchWorkers
}

func (runtime *commandRuntime) effectiveVerificationWorkers() int {
	if runtime != nil && runtime.verificationWorkers > 0 {
		return runtime.verificationWorkers
	}
	return defaultVerificationWorkers
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

	dispatchAnyFn := runtime.dispatchAnyOnce()
	verifyAnyFn := runtime.verifyAnyOnce()
	logger.Info(
		"connectivity_command_runtime_started",
		"dispatcher_worker_id", runtime.dispatcherWorker,
		"verifier_worker_id", runtime.verifierWorker,
		"dispatch_workers", runtime.effectiveDispatchWorkers(),
		"verification_workers", runtime.effectiveVerificationWorkers(),
	)
	var workers sync.WaitGroup
	if dispatchAnyFn != nil {
		count := runtime.effectiveDispatchWorkers()
		for i := 0; i < count; i++ {
			workers.Add(1)
			observability.GoSafe(logger, runtime.metrics, "connectivity.command_dispatch", func() {
				defer workers.Done()
				runtime.runAnyWorkerLoop(claimCtx, execCtx, logger, "dispatch", 40*time.Second, commandAnyClaimWait, dispatchAnyFn, commandservice.ErrNoDispatchAvailable)
			})
		}
	}
	if verifyAnyFn != nil {
		count := runtime.effectiveVerificationWorkers()
		for i := 0; i < count; i++ {
			workers.Add(1)
			observability.GoSafe(logger, runtime.metrics, "connectivity.command_verification", func() {
				defer workers.Done()
				runtime.runAnyWorkerLoop(claimCtx, execCtx, logger, "verification", 12*time.Second, commandAnyClaimWait, verifyAnyFn, commandservice.ErrVerificationNotAvailable)
			})
		}
	}
	<-claimCtx.Done()
	workers.Wait()
}

func (runtime *commandRuntime) runAnyWorkerLoop(
	claimCtx, execCtx context.Context,
	logger *slog.Logger,
	work string,
	timeout, waitWindow time.Duration,
	runAny func(context.Context, time.Duration) error,
	noWork error,
) {
	backoff := commandTenantBaseBackoff
	for claimCtx.Err() == nil {
		turnStart := time.Now()
		panicked := false
		var err error
		func() {
			defer observability.Recover(logger, runtime.metrics, "connectivity.command_"+work, func(any) {
				panicked = true
			})
			turnCtx, cancel := context.WithTimeout(execCtx, timeout)
			defer cancel()
			err = runAny(turnCtx, waitWindow)
		}()
		if claimCtx.Err() != nil {
			return
		}
		if panicked {
			backoff = commandTenantBaseBackoff
			commandWaitOnError(claimCtx, time.Second)
			continue
		}
		if err == nil {
			backoff = commandTenantBaseBackoff
			continue
		}
		if errors.Is(err, noWork) {
			backoff = commandTenantBaseBackoff
			if time.Since(turnStart) < waitWindow/4 {
				commandWaitOnError(claimCtx, commandTenantBaseBackoff)
			}
			continue
		}
		if logger != nil {
			logger.Warn("command_"+work+"_worker_failed", "error", err.Error())
		}
		delay := backoff
		if backoff < commandTenantMaxBackoff {
			backoff *= 2
			if backoff > commandTenantMaxBackoff {
				backoff = commandTenantMaxBackoff
			}
		}
		commandWaitOnError(claimCtx, delay)
	}
}

func commandWaitOnError(ctx context.Context, delay time.Duration) {
	if delay <= 0 {
		return
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

func (runtime *commandRuntime) Close(ctx context.Context) error {
	if runtime == nil || runtime.connector == nil {
		return nil
	}
	return runtime.connector.Disconnect(ctx)
}

const (
	defaultDispatchWorkers     = 16
	defaultVerificationWorkers = 8
	maxCommandPoolWorkers      = 256
	commandAnyClaimWait        = 2 * time.Second
	commandTenantBaseBackoff   = 100 * time.Millisecond
	commandTenantMaxBackoff    = 5 * time.Second
)

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
	return sanitizeClientIDToken(raw, true, 64)
}

func sanitizeClientIDToken(raw string, replaceInvalidWithDash bool, maxLen int) string {
	input := raw
	if replaceInvalidWithDash {
		input = strings.TrimSpace(raw)
	}
	var builder strings.Builder
	builder.Grow(min(len(input), maxLen))
	lastDash := false
	for _, ch := range input {
		isWord := (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_'
		if isWord || (!replaceInvalidWithDash && ch == '-') {
			builder.WriteRune(ch)
			lastDash = false
		} else if replaceInvalidWithDash && builder.Len() > 0 && !lastDash {
			builder.WriteRune('-')
			lastDash = true
		}
		if builder.Len() >= maxLen {
			break
		}
	}
	out := builder.String()
	if replaceInvalidWithDash {
		return strings.Trim(out, "-_")
	}
	return out
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
