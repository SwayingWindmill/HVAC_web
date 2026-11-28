package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
	"github.com/quanlaihe/hvac-web/libs/edgetunnel"
	"github.com/quanlaihe/hvac-web/libs/mqttclient"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/adapter"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/concentrator"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/connectivity"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/packettap"
)

func main() {
	os.Exit(run())
}

func run() int {
	diagnosticsAddress := flag.String("diagnostics-addr", envOr("MQTT_TELEMETRY_ADAPTER_DIAGNOSTICS_ADDR", ":19094"), "health server listen address")
	initializeCA := flag.Bool("initialize-gateway-ca", false, "initialize new Gateway CA files and exit")
	tunnelStdio := flag.String("tunnel-stdio", "", "bridge stdin/stdout to loopback tunnel address and exit")
	flag.Parse()
	if strings.TrimSpace(*tunnelStdio) != "" {
		stdioCtx, stdioCancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		err := edgetunnel.RunStdioBridge(stdioCtx, *tunnelStdio, os.Stdin, os.Stdout)
		stdioCancel()
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err.Error())
			return 1
		}
		return 0
	}

	logger := observability.NewJSONLogger(os.Stdout, slog.LevelInfo)
	var closers []func()
	addCloser := func(fn func()) {
		if fn != nil {
			closers = append(closers, fn)
		}
	}
	var shutdownRan bool
	defer func() {
		if shutdownRan {
			return
		}
		for i := len(closers) - 1; i >= 0; i-- {
			closers[i]()
		}
	}()

	telemetry := observability.NewRuntime(observability.RuntimeConfig{
		Service: "mqtt-telemetry-adapter", OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), QueueSize: 1024, ExportTimeout: 500 * time.Millisecond,
	})
	var telemetryShutdownOnce sync.Once
	shutdownTelemetry := func(ctx context.Context) error {
		var shutdownErr error
		telemetryShutdownOnce.Do(func() {
			shutdownErr = telemetry.Shutdown(ctx)
		})
		return shutdownErr
	}
	defer func() {
		if shutdownRan {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdownTelemetry(ctx)
	}()

	if *initializeCA {
		workloadCA, err := os.ReadFile(envOr("CONNECTIVITY_CA", "/run/hvac/pki/ca.crt"))
		if err == nil {
			err = connectivity.InitializeGatewayCA(envOr("GATEWAY_CA_DIR", "/run/hvac/gateway-ca"), workloadCA, time.Now().UTC())
		}
		if err != nil {
			var fileError *os.PathError
			if errors.As(err, &fileError) {
				logger.Error("gateway_ca_initialization_failed", "operation", fileError.Op, "system_error", fileError.Err.Error())
			} else {
				logger.Error("gateway_ca_initialization_failed", "error_code", "GATEWAY_CA_INITIALIZATION_FAILED")
			}
			return 1
		}
		return 0
	}
	config := uplinkConfig()
	if err := config.Validate(); err != nil {
		logger.Error("connectivity_uplink_config_invalid", "cause", err.Error())
		return 1
	}
	rateLimitMPS, rateLimitBurst, err := gatewayRateLimitConfig()
	if err != nil {
		logger.Error("connectivity_rate_limit_config_invalid", "cause", err.Error())
		return 1
	}

	sigCtx, sigCancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer sigCancel()
	ingressCtx, ingressCancel := context.WithCancel(context.Background())
	defer ingressCancel()
	drainRootCtx, drainRootCancel := context.WithCancel(context.Background())
	defer drainRootCancel()
	var shuttingDown atomic.Bool
	tap := packettap.NewManager()

	connectivityDatabaseURL := strings.TrimSpace(os.Getenv("CONNECTIVITY_DATABASE_URL"))
	connectivityStore, err := connectivity.Open(sigCtx, connectivityDatabaseURL)
	if err != nil {
		logger.Error("iot_connectivity_store_unavailable", "error", err.Error())
		return 1
	}
	addCloser(connectivityStore.Close)
	if err := connectivityStore.VerifyProductSchema(sigCtx); err != nil {
		logger.Error("connectivity_schema_compatibility_invalid", "error_code", "SCHEMA_COMPATIBILITY_INVALID", "cause", err.Error())
		return 1
	}
	telemetry.SetDependencies(observability.Dependency{
		Name:     "postgres-connectivity",
		Required: true,
		Check:    connectivityStore.Ping,
	})
	mqttSession, concentratorTransport, err := openConcentratorAndSharedSession(drainRootCtx, config.MQTT, logger, telemetry.Metrics, tap)
	if err != nil {
		logger.Error("connectivity_concentrator_mqtt_unavailable", "error_code", "CONCENTRATOR_MQTT_UNAVAILABLE", "error", err.Error())
		return 1
	}
	addCloser(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = concentratorTransport.Close(ctx)
	})
	addCloser(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = mqttSession.Close(ctx)
	})
	telemetryRuntime, err := adapter.NewTelemetryRuntimeClientWithTracer(config.TelemetryRuntime, telemetry.Tracer)
	if err != nil {
		logger.Error("mqtt_telemetry_adapter_runtime_client_invalid", "error", err.Error())
		return 1
	}
	concentratorService := concentrator.NewService(connectivityStore, concentratorTransport)
	presence := newGatewayOnlineTrackerWithMetrics(telemetry.Metrics)
	otaPublicBaseURL := strings.TrimSpace(os.Getenv("CONNECTIVITY_OTA_PUBLIC_BASE_URL"))
	diagBridge := connectivity.NewDiagnosticsMQTTBridge()
	credentials, tunnelHub, crlCtrl, err := newCredentialRuntime(credentialRuntimeConfig{
		RootCtx:             drainRootCtx,
		Logger:              logger,
		Metrics:             telemetry.Metrics,
		Presence:            presence,
		Store:               connectivityStore,
		ConcentratorService: concentratorService,
		CoverageReporter:    telemetryRuntime,
		Tap:                 tap,
		MQTTSession:         mqttSession,
		TunnelOptions:       []edgetunnel.Option{edgetunnel.WithObservability(logger, telemetry.Metrics)},
		OTAPublicBaseURL:    otaPublicBaseURL,
		DiagnosticsBridge:   diagBridge,
	})
	if err != nil {
		logger.Error("connectivity_credentials_unavailable", "error_code", "CREDENTIAL_MODULE_UNAVAILABLE", "error", err.Error())
		return 1
	}
	addCloser(tunnelHub.Close)
	addCloser(crlCtrl.Close)
	var (
		bgMu        sync.RWMutex
		bgClosed    bool
		bgWG        sync.WaitGroup
		wakeFlusher = newDeferredCommandWakeFlusher(logger, telemetry.Metrics, defaultWakeDebounce)
	)
	processor, err := adapter.NewProcessor(
		connectivityStore,
		telemetryRuntime,
		adapter.WithProcessorTracer(telemetry.Tracer),
		adapter.WithProcessorMetrics(telemetry.Metrics),
		adapter.WithGatewayRateLimit(rateLimitMPS, rateLimitBurst),
		adapter.WithAutoBackfillLagThreshold(config.AutoBackfillLagThreshold),
		adapter.WithProcessorPacketHook(tap.Record),
		adapter.WithGatewayPresenceHook(presence.SetOnline),
		adapter.WithDeviceActivityHook(wakeFlusher.NotifyDevices),
		adapter.WithShadowAckHook(func(_ context.Context, gw adapter.Gateway, ack adapter.ShadowChangeAckPayload) {
			logger.Info("mqtt_shadow_change_ack",
				"gateway_id", gw.ID,
				"status", ack.Status,
				"cursor", ack.Cursor,
				"delivery_id", ack.DeliveryID,
			)
		}),
		adapter.WithOTAResultHook(func(hookCtx context.Context, gw adapter.Gateway, res edgecontrol.OTAResultPayload) error {
			_, dispatches, applyErr := connectivityStore.ApplyGatewayOTAResult(hookCtx, gw.ID, res)
			if applyErr != nil {
				if errors.Is(applyErr, connectivity.ErrNotFound) || errors.Is(applyErr, connectivity.ErrOTAInvalid) {
					logger.Warn("mqtt_ota_result_rejected", "gateway_id", gw.ID, "task_id", res.TaskID, "error", applyErr.Error())
					return fmt.Errorf("%w: %v", adapter.ErrOTAResultRejected, applyErr)
				}
				return applyErr
			}
			if len(dispatches) > 0 && mqttSession != nil {
				observability.GoSafe(logger, telemetry.Metrics, "connectivity.ota_rollout_publish", func() {
					publishGatewayOTAStages(drainRootCtx, mqttSession, logger, telemetry.Metrics, tap, otaPublicBaseURL, dispatches)
				})
			}
			return nil
		}),
		adapter.WithWaveformBurstHook(func(hookCtx context.Context, gw adapter.Gateway, burst edgecontrol.WaveformBurstPayload) error {
			return connectivityStore.RecordWaveformBurstUplink(hookCtx, gw.TenantID, gw.SiteID, gw.ID, burst)
		}),
		adapter.WithDiagnosticsReplyHook(func(hookCtx context.Context, gw adapter.Gateway, reply edgecontrol.DiagnosticsLogReplyPayload) error {
			diagBridge.CompleteLogReply(hookCtx, gw.ID, reply)
			return nil
		}),
		adapter.WithHostMetricsHook(func(hookCtx context.Context, gw adapter.Gateway, metrics edgecontrol.GatewayHostMetricsPayload) error {
			return connectivityStore.RecordGatewayHostMetrics(hookCtx, gw.TenantID, gw.SiteID, gw.ID, metrics)
		}),
		adapter.WithFactoryResetAckHook(func(hookCtx context.Context, gatewayID string, ack edgecontrol.FactoryResetAckPayload) error {
			_, ackErr := connectivityStore.RecordFactoryResetAck(hookCtx, gatewayID, ack)
			if ackErr != nil {
				if errors.Is(ackErr, connectivity.ErrNotFound) || errors.Is(ackErr, connectivity.ErrEnrollmentRejected) {
					logger.Warn("mqtt_factory_reset_ack_rejected", "gateway_id", gatewayID, "reset_id", ack.ResetID, "error", ackErr.Error())
					return fmt.Errorf("%w: %v", adapter.ErrFactoryResetAckRejected, ackErr)
				}
				return ackErr
			}
			return nil
		}),
		adapter.WithGatewayConnectHook(func(_ context.Context, gatewayID string) {
			wakeFlusher.NotifyGateway(gatewayID)
			bgMu.RLock()
			if bgClosed || ingressCtx.Err() != nil {
				bgMu.RUnlock()
				return
			}
			bgWG.Add(2)
			bgMu.RUnlock()
			go func() {
				defer bgWG.Done()
				defer observability.Recover(logger, telemetry.Metrics, "connectivity.concentrator_shadow_reconcile")
				reconcileConcentratorShadowAsync(ingressCtx, concentratorService, logger, gatewayID)
			}()
			go func() {
				defer bgWG.Done()
				defer observability.Recover(logger, telemetry.Metrics, "connectivity.gateway_ota_reconcile")
				reconcileGatewayOTAOnConnectAsync(ingressCtx, connectivityStore, mqttSession, logger, telemetry.Metrics, tap, otaPublicBaseURL, gatewayID)
			}()
		}),
	)
	if err != nil {
		logger.Error("mqtt_telemetry_adapter_processor_invalid", "error", err.Error())
		return 1
	}
	brokerLogWatcher := adapter.NewBrokerLogWatcher(
		ingressCtx,
		func(ctx context.Context, gatewayID string, disconnectedAt time.Time) {
			if disconnectErr := processor.HandleBrokerGatewayDisconnect(ctx, gatewayID, disconnectedAt); disconnectErr != nil && ctx.Err() == nil {
				logger.Warn("broker_gateway_disconnect_handle_failed", "gateway_id", gatewayID, "error", disconnectErr.Error())
			}
		},
		adapter.WithBrokerLogObservability(logger, telemetry.Metrics),
	)
	unregisterBrokerLog, err := mqttSession.RegisterRoute("connectivity-broker-log", []string{adapter.BrokerNoticeLogTopic}, brokerLogWatcher.HandlePublish)
	if err != nil {
		brokerLogWatcher.Close()
		logger.Error("connectivity_broker_log_route_failed", "error", err.Error())
		return 1
	}
	runtimeOptions := []adapter.RuntimeOption{
		adapter.WithCoverageReporter(connectivityStore, telemetryRuntime),
		adapter.WithPacketHook(tap.Record),
		adapter.WithOnlineGatewayCount(presence.Count),
		adapter.WithSharedSession(mqttSession),
	}
	runtime, err := adapter.NewRuntime(
		config,
		processor,
		logger,
		telemetry.Metrics,
		runtimeOptions...,
	)
	if err != nil {
		logger.Error("mqtt_telemetry_adapter_runtime_invalid", "error", err.Error())
		return 1
	}

	// Commands are part of Connectivity when enabled: if they cannot start, the process
	// fails instead of reporting healthy without a command channel.
	var commands *commandRuntime
	if strings.EqualFold(strings.TrimSpace(os.Getenv("COMMAND_RUNTIME_IN_PROCESS_ENABLED")), "true") {
		if commands, err = loadCommandRuntime(drainRootCtx, connectivityStore, logger, telemetry.Metrics, telemetry.Tracer, tap); err != nil {
			logger.Error("connectivity_command_runtime_unavailable", "error_code", "COMMAND_MODULE_UNAVAILABLE", "error", err.Error())
			return 1
		}
		addCloser(func() {
			wakeFlusher.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = commands.Close(ctx)
		})
		wakeFlusher.Start(drainRootCtx, commands.dispatcherClient.WakeDeferredDispatches, connectivityStore.GatewayDeviceIDs)
	}
	telemetry.MarkReady()
	diagnostics := diagnosticsServer(*diagnosticsAddress, runtime, telemetry, &shuttingDown)
	diagnosticsErr := make(chan error, 2)
	credentialsDone := make(chan struct{})
	go func() {
		defer close(credentialsDone)
		defer observability.Recover(logger, telemetry.Metrics, "connectivity.credential_server", func(any) {
			select {
			case diagnosticsErr <- errors.New("credential server panicked"):
			default:
			}
		})
		if err := credentials.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			diagnosticsErr <- err
		}
	}()
	diagnosticsDone := make(chan struct{})
	go func() {
		defer close(diagnosticsDone)
		defer observability.Recover(logger, telemetry.Metrics, "connectivity.diagnostics_server", func(any) {
			select {
			case diagnosticsErr <- errors.New("diagnostics server panicked"):
			default:
			}
		})
		logger.Info("mqtt_telemetry_adapter_diagnostics_started", "address", *diagnosticsAddress)
		if serveErr := diagnostics.ListenAndServe(); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			diagnosticsErr <- serveErr
		}
	}()
	runtimeDone := make(chan struct{})
	go func() {
		defer close(runtimeDone)
		defer observability.Recover(logger, telemetry.Metrics, "connectivity.uplink_runtime")
		logger.Info("connectivity_uplink_started", "topic_filters", config.SubscriptionTopicFilters())
		if runErr := runtime.Run(ingressCtx); runErr != nil && ingressCtx.Err() == nil {
			// Telemetry ingress is its own fault domain. Command delivery remains
			// running; telemetry readiness becomes false until the process is repaired.
			logger.Error("mqtt_telemetry_module_stopped", "error_code", "TELEMETRY_MODULE_STOPPED", "error", runErr.Error())
		}
	}()
	retentionDone := startQuarantineRetentionLoop(ingressCtx, connectivityStore, logger, telemetry.Metrics, time.Hour, connectivity.DefaultQuarantineRetention)
	crlDone := startCRLRefreshLoop(ingressCtx, logger, telemetry.Metrics, defaultCRLRefreshInterval, crlCtrl.RefreshAndReload)
	commandsDone := make(chan struct{})
	if commands != nil {
		observability.GoSafe(logger, telemetry.Metrics, "connectivity.command_runtime", func() {
			defer close(commandsDone)
			commands.RunWithDrain(ingressCtx, drainRootCtx, logger)
		})
	} else {
		close(commandsDone)
	}

	exitCode := 0
	select {
	case <-sigCtx.Done():
	case diagnosticsRunErr := <-diagnosticsErr:
		logger.Error("mqtt_telemetry_adapter_diagnostics_failed", "error", diagnosticsRunErr.Error())
		exitCode = 1
	}
	shutdownConnectivityRuntime(connectivityShutdownDeps{
		markShuttingDown: func() {
			shuttingDown.Store(true)
			telemetry.MarkNotReady()
		},
		shutdownCredentials: func(ctx context.Context) error {
			return credentials.Shutdown(ctx)
		},
		credentialsDone: credentialsDone,
		closeTunnelHub:  tunnelHub.Close,
		closeCRL:        crlCtrl.Close,
		cancelIngress:   ingressCancel,
		cancelDrainRoot: drainRootCancel,
		runtimeDone:     runtimeDone,
		waitBackground: func(ctx context.Context) {
			bgMu.Lock()
			bgClosed = true
			bgMu.Unlock()
			done := make(chan struct{})
			go func() {
				defer close(done)
				bgWG.Wait()
			}()
			awaitSignal(ctx, done)
		},
		commandsDone: commandsDone,
		waitCommands: func(ctx context.Context) error {
			if commands == nil {
				return nil
			}
			return commands.Wait(ctx)
		},
		retentionDone:   retentionDone,
		crlDone:         crlDone,
		flushQuarantine: connectivityStore.FlushQuarantineBursts,
		closeConcentrator: func(ctx context.Context) error {
			return concentratorTransport.Close(ctx)
		},
		closeMQTTSession: func(ctx context.Context) error {
			if unregisterBrokerLog != nil {
				_ = unregisterBrokerLog(ctx)
			}
			brokerLogWatcher.Close()
			if mqttSession == nil {
				return nil
			}
			return mqttSession.Close(ctx)
		},
		closeCommands: func(ctx context.Context) error {
			if commands == nil {
				return nil
			}
			return commands.Close(ctx)
		},
		shutdownDiagnostics: func(ctx context.Context) error {
			return diagnostics.Shutdown(ctx)
		},
		diagnosticsDone:   diagnosticsDone,
		shutdownTelemetry: shutdownTelemetry,
		closeStore:        connectivityStore.Close,
	})
	shutdownRan = true
	logger.Info("mqtt_telemetry_adapter_stopped")
	return exitCode
}

type connectivityShutdownDeps struct {
	markShuttingDown    func()
	shutdownCredentials func(context.Context) error
	credentialsDone     <-chan struct{}
	closeTunnelHub      func()
	closeCRL            func()
	cancelIngress       func()
	cancelDrainRoot     func()
	runtimeDone         <-chan struct{}
	waitBackground      func(context.Context)
	commandsDone        <-chan struct{}
	waitCommands        func(context.Context) error
	retentionDone       <-chan struct{}
	crlDone             <-chan struct{}
	flushQuarantine     func(context.Context) error
	closeConcentrator   func(context.Context) error
	closeMQTTSession    func(context.Context) error
	closeCommands       func(context.Context) error
	shutdownDiagnostics func(context.Context) error
	diagnosticsDone     <-chan struct{}
	shutdownTelemetry   func(context.Context) error
	closeStore          func()
}

func shutdownConnectivityRuntime(deps connectivityShutdownDeps) {
	if deps.markShuttingDown != nil {
		deps.markShuttingDown()
	}

	credShutdownCtx, credShutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if deps.shutdownCredentials != nil {
		_ = deps.shutdownCredentials(credShutdownCtx)
	}
	awaitSignal(credShutdownCtx, deps.credentialsDone)
	credShutdownCancel()
	if deps.closeTunnelHub != nil {
		deps.closeTunnelHub()
	}
	if deps.closeCRL != nil {
		deps.closeCRL()
	}

	if deps.cancelIngress != nil {
		deps.cancelIngress()
	}
	drainCtx, drainCancel := context.WithTimeout(context.Background(), 30*time.Second)
	var stopDrain func() bool
	if deps.cancelDrainRoot != nil {
		stopDrain = context.AfterFunc(drainCtx, deps.cancelDrainRoot)
	}
	awaitSignal(drainCtx, deps.runtimeDone)
	if deps.waitBackground != nil {
		deps.waitBackground(drainCtx)
	}
	awaitSignal(drainCtx, deps.commandsDone)
	if deps.waitCommands != nil {
		_ = deps.waitCommands(drainCtx)
	}
	awaitSignal(drainCtx, deps.retentionDone)
	awaitSignal(drainCtx, deps.crlDone)
	if deps.flushQuarantine != nil {
		_ = deps.flushQuarantine(drainCtx)
	}
	if stopDrain != nil {
		stopDrain()
	}
	if deps.cancelDrainRoot != nil {
		deps.cancelDrainRoot()
	}
	drainCancel()

	shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if deps.closeConcentrator != nil {
		_ = deps.closeConcentrator(shutdownContext)
	}
	if deps.closeMQTTSession != nil {
		_ = deps.closeMQTTSession(shutdownContext)
	}
	if deps.closeCommands != nil {
		_ = deps.closeCommands(shutdownContext)
	}
	if deps.shutdownDiagnostics != nil {
		_ = deps.shutdownDiagnostics(shutdownContext)
	}
	awaitSignal(shutdownContext, deps.diagnosticsDone)
	if deps.shutdownTelemetry != nil {
		_ = deps.shutdownTelemetry(shutdownContext)
	}
	if deps.closeStore != nil {
		deps.closeStore()
	}
}

// uplinkConfig reads the uplink settings; the defaults are the in-container paths of
// Connectivity's workload identity and the Phase 1 service addresses.
func uplinkConfig() adapter.Config {
	identityCert := envOr("CONNECTIVITY_TLS_CERT", "/run/hvac/pki/mqtt-telemetry-adapter/tls.crt")
	identityKey := envOr("CONNECTIVITY_TLS_KEY", "/run/hvac/pki/mqtt-telemetry-adapter/tls.key")
	ca := envOr("CONNECTIVITY_CA", "/run/hvac/pki/ca.crt")
	return adapter.Config{
		MQTT: adapter.MQTTConfig{
			BrokerURL:               envOr("CONNECTIVITY_MQTT_URL", "tls://mqtt-broker:8883"),
			ClientID:                envOr("CONNECTIVITY_MQTT_CLIENT_ID", defaultMQTTClientID("connectivity")),
			SharedSubscriptionGroup: sharedSubscriptionGroup(),
			CAFile:                  ca, CertFile: identityCert, KeyFile: identityKey,
			ServerName: envOr("CONNECTIVITY_MQTT_SERVER_NAME", "mqtt-broker"),
		},
		TelemetryRuntime: adapter.TelemetryRuntimeConfig{
			BaseURL: envOr("CONNECTIVITY_TELEMETRY_URL", "https://telemetry-runtime-service:8446"),
			CAFile:  ca, CertFile: identityCert, KeyFile: identityKey,
			ServerName: envOr("CONNECTIVITY_TELEMETRY_SERVER_NAME", "telemetry-runtime-service"),
		},
		QueueCapacity:            queueCapacity(),
		OverflowMaxMessages:      overflowMaxMessages(),
		OverflowMaxBytes:         overflowMaxBytes(),
		OverflowDir:              strings.TrimSpace(os.Getenv("CONNECTIVITY_OVERFLOW_DIR")),
		BatchMaxMessages:         batchMaxMessages(),
		BatchFlushWindow:         batchFlushWindow(),
		AutoBackfillLagThreshold: autoBackfillLagThreshold(),
		WorkerCount:              uplinkWorkers(),
	}
}

func sharedSubscriptionGroup() string {
	raw, ok := os.LookupEnv("CONNECTIVITY_MQTT_SHARE_GROUP")
	if !ok {
		return "connectivity"
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || strings.EqualFold(trimmed, "off") || strings.EqualFold(trimmed, "none") {
		return ""
	}
	return trimmed
}

func overflowMaxMessages() int {
	raw := strings.TrimSpace(os.Getenv("CONNECTIVITY_OVERFLOW_MAX_MESSAGES"))
	if raw == "" {
		return 0
	}
	maxMessages, err := strconv.Atoi(raw)
	if err != nil || maxMessages < 1 {
		return -1
	}
	return maxMessages
}

func overflowMaxBytes() int64 {
	raw := strings.TrimSpace(os.Getenv("CONNECTIVITY_OVERFLOW_MAX_BYTES"))
	if raw == "" {
		return 0
	}
	maxBytes, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || maxBytes < 1 {
		return -1
	}
	return maxBytes
}

// queueCapacity bounds each Gateway's in-process backlog (CONNECTIVITY_QUEUE_CAPACITY,
// default 1024); beyond it the broker holds the Gateway's messages.
func queueCapacity() int {
	capacity, err := strconv.Atoi(envOr("CONNECTIVITY_QUEUE_CAPACITY", "1024"))
	if err != nil {
		return 0
	}
	return capacity
}

func batchMaxMessages() int {
	maxMessages, err := strconv.Atoi(envOr("CONNECTIVITY_BATCH_MAX_MESSAGES", strconv.Itoa(adapter.DefaultBatchMaxMessages)))
	if err != nil || maxMessages < 1 {
		return -1
	}
	return maxMessages
}

func batchFlushWindow() time.Duration {
	window, err := time.ParseDuration(envOr("CONNECTIVITY_BATCH_WINDOW", adapter.DefaultBatchFlushWindow.String()))
	if err != nil {
		return -1
	}
	return window
}

func autoBackfillLagThreshold() time.Duration {
	threshold, err := time.ParseDuration(envOr("CONNECTIVITY_AUTO_BACKFILL_LAG", adapter.DefaultAutoBackfillLagThreshold.String()))
	if err != nil {
		return -1
	}
	return threshold
}

func uplinkWorkers() int {
	workers, err := strconv.Atoi(envOr("CONNECTIVITY_UPLINK_WORKERS", strconv.Itoa(adapter.DefaultWorkerCount)))
	if err != nil || workers < 1 {
		return -1
	}
	return workers
}

type quarantinePruner interface {
	PruneExpiredQuarantine(ctx context.Context, retention time.Duration) (int64, error)
}

func startQuarantineRetentionLoop(
	ctx context.Context,
	store quarantinePruner,
	logger *slog.Logger,
	metrics *observability.Registry,
	interval, retention time.Duration,
) <-chan struct{} {
	done := make(chan struct{})
	if store == nil {
		close(done)
		return done
	}
	if interval <= 0 {
		interval = time.Hour
	}
	if retention <= 0 {
		retention = connectivity.DefaultQuarantineRetention
	}
	runOnce := func() {
		if ctx.Err() != nil {
			return
		}
		pruneCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		deleted, err := store.PruneExpiredQuarantine(pruneCtx, retention)
		cancel()
		if err != nil {
			if ctx.Err() == nil && logger != nil {
				logger.Warn("connectivity_quarantine_prune_failed", "cause", err.Error())
			}
			return
		}
		if deleted > 0 && logger != nil {
			logger.Info("connectivity_quarantine_pruned", "deleted", deleted)
		}
	}
	observability.GoSafe(logger, metrics, "connectivity.quarantine_retention", func() {
		defer close(done)
		runOnce()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runOnce()
			}
		}
	})
	return done
}

func awaitSignal(ctx context.Context, ch <-chan struct{}) {
	if ch == nil {
		return
	}
	select {
	case <-ch:
	case <-ctx.Done():
	}
}

func diagnosticsServer(address string, runtime *adapter.Runtime, telemetry *observability.Runtime, shuttingDown *atomic.Bool) *http.Server {
	return diagnosticsServerWithReady(address, func() bool {
		return runtime != nil && runtime.Ready()
	}, telemetry, shuttingDown)
}

func diagnosticsServerWithReady(address string, runtimeReady func() bool, telemetry *observability.Runtime, shuttingDown *atomic.Bool) *http.Server {
	mux := http.NewServeMux()
	if telemetry != nil {
		obsHandler := telemetry.DiagnosticsHandler()
		mux.Handle("/metrics", obsHandler)
		mux.Handle("/diagnostics", obsHandler)
	}
	ready := func() bool {
		if shuttingDown != nil && shuttingDown.Load() {
			return false
		}
		return runtimeReady != nil && runtimeReady()
	}
	mux.HandleFunc("/health/live", getHealthHandler(func() bool { return true }))
	mux.HandleFunc("/health/ready", getHealthHandler(ready))
	mux.HandleFunc("/health/telemetry/ready", getHealthHandler(ready))
	return &http.Server{
		Addr:              address,
		Handler:           mux,
		ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
}

func getHealthHandler(ready func() bool) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			writer.Header().Set("Allow", http.MethodGet)
			writer.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !ready() {
			writer.WriteHeader(http.StatusServiceUnavailable)
			_, _ = writer.Write([]byte("not ready\n"))
			return
		}
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("ready\n"))
	}
}

func reconcileConcentratorShadowAsync(rootCtx context.Context, service *concentrator.Service, logger *slog.Logger, gatewayID string) {
	if service == nil || strings.TrimSpace(gatewayID) == "" {
		return
	}
	reconcileCtx, cancel := context.WithTimeout(rootCtx, 30*time.Second)
	defer cancel()
	if err := service.ReconcileConcentratorShadow(reconcileCtx, gatewayID); err != nil && logger != nil {
		logger.Warn("concentrator_shadow_reconcile_failed", "gateway_id", gatewayID, "error", err.Error())
	}
}

func reconcileGatewayOTAOnConnectAsync(
	rootCtx context.Context,
	store *connectivity.Store,
	publisher shadowMQTTPublisher,
	logger *slog.Logger,
	metrics *observability.Registry,
	tap *packettap.Manager,
	publicBaseURL string,
	gatewayID string,
) {
	if store == nil || publisher == nil || strings.TrimSpace(gatewayID) == "" {
		return
	}
	lookupCtx, cancel := context.WithTimeout(rootCtx, 10*time.Second)
	defer cancel()
	stage, err := store.GetGatewayDesiredOTAStage(lookupCtx, gatewayID)
	if err != nil {
		if logger != nil {
			logger.Warn("gateway_ota_reconnect_lookup_failed", "gateway_id", gatewayID, "error", err.Error())
		}
		return
	}
	if stage != nil {
		publishGatewayOTAStages(rootCtx, publisher, logger, metrics, tap, publicBaseURL, []connectivity.OTAStageDispatch{{
			GatewayID: gatewayID,
			Stage:     *stage,
		}})
	}
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

const maxDefaultMQTTClientIDLen = 96

func defaultMQTTClientID(prefix string) string {
	host, err := os.Hostname()
	if err != nil {
		host = ""
	}
	return defaultMQTTClientIDForHost(prefix, host)
}

func defaultMQTTClientIDForHost(prefix, host string) string {
	cleanPrefix := sanitizeMQTTClientID(prefix)
	if cleanPrefix == "" {
		cleanPrefix = "connectivity"
	}
	cleanHost := sanitizeMQTTClientID(host)
	if cleanHost == "" {
		return cleanPrefix
	}
	combined := cleanPrefix + "-" + cleanHost
	if len(combined) > maxDefaultMQTTClientIDLen {
		return combined[:maxDefaultMQTTClientIDLen]
	}
	return combined
}

func sanitizeMQTTClientID(raw string) string {
	return sanitizeClientIDToken(raw, false, maxDefaultMQTTClientIDLen)
}

const (
	defaultGatewayRateLimitMPS   = 200.0
	maxGatewayRateLimitMPS       = 100000.0
	defaultGatewayRateLimitBurst = 400
	maxGatewayRateLimitBurst     = 1000000
)

func gatewayRateLimitConfig() (float64, int, error) {
	mps := defaultGatewayRateLimitMPS
	if rawMPS, ok := os.LookupEnv("CONNECTIVITY_GATEWAY_RATE_LIMIT_MPS"); ok {
		trimmed := strings.TrimSpace(rawMPS)
		if trimmed == "" || strings.EqualFold(trimmed, "off") || strings.EqualFold(trimmed, "none") {
			mps = 0
		} else {
			parsed, err := strconv.ParseFloat(trimmed, 64)
			if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) || parsed <= 0 || parsed > maxGatewayRateLimitMPS {
				return 0, 0, fmt.Errorf("CONNECTIVITY_GATEWAY_RATE_LIMIT_MPS must be between (0,%g] or off", maxGatewayRateLimitMPS)
			}
			mps = parsed
		}
	}

	burst := defaultGatewayRateLimitBurst
	if mps == 0 {
		burst = 0
	}
	if rawBurst, ok := os.LookupEnv("CONNECTIVITY_GATEWAY_RATE_LIMIT_BURST"); ok {
		trimmed := strings.TrimSpace(rawBurst)
		if trimmed != "" {
			parsed, err := strconv.Atoi(trimmed)
			if err != nil || parsed < 1 || parsed > maxGatewayRateLimitBurst {
				return 0, 0, fmt.Errorf("CONNECTIVITY_GATEWAY_RATE_LIMIT_BURST must be an integer in [1,%d]", maxGatewayRateLimitBurst)
			}
			if mps > 0 {
				burst = parsed
			}
		}
	}
	return mps, burst, nil
}

func buildSharedUplinkSessionConfig(cfg adapter.MQTTConfig, logger *slog.Logger, metrics *observability.Registry) mqttclient.SharedSessionConfig {
	return mqttclient.SharedSessionConfig{
		BrokerURL: cfg.BrokerURL,
		ClientID:  cfg.ClientID,
		TLS: &mqttclient.TLSConfig{
			CACertFile:     cfg.CAFile,
			ClientCertFile: cfg.CertFile,
			ClientKeyFile:  cfg.KeyFile,
			ServerName:     cfg.ServerName,
		},
		KeepAlive:                     30,
		ReceiveMaximum:                cfg.EffectiveReceiveMaximum(),
		CleanStartOnInitialConnection: false,
		SessionExpiryInterval:         24 * 60 * 60,
		DialTimeout:                   10 * time.Second,
		OnConnectError: func(connectErr error) {
			if metrics != nil {
				_ = metrics.AddCounter("hvac_mqtt_connections_total", "MQTT connection attempts by outcome.", map[string]string{"outcome": "failed"}, 1)
			}
			if logger != nil && connectErr != nil {
				logger.Warn("mqtt_uplink_connect_failed", "error_code", "MQTT_UPLINK_CONNECT_FAILED", "error", connectErr.Error())
			}
		},
		OnSubscribeError: func(subErr error) {
			if metrics != nil {
				_ = metrics.AddCounter("hvac_mqtt_subscriptions_total", "MQTT subscription attempts by outcome.", map[string]string{"outcome": "failed"}, 1)
			}
			if logger != nil && subErr != nil {
				logger.Warn("mqtt_uplink_subscribe_failed", "error_code", "MQTT_UPLINK_SUBSCRIBE_FAILED", "error", subErr.Error())
			}
		},
	}
}

func openConcentratorAndSharedSession(
	ctx context.Context,
	cfg adapter.MQTTConfig,
	logger *slog.Logger,
	metrics *observability.Registry,
	tap *packettap.Manager,
) (*mqttclient.SharedSession, *concentrator.MQTTTransport, error) {
	transportCfg := concentrator.TransportConfig{
		BrokerURL:  cfg.BrokerURL,
		ClientID:   cfg.ClientID,
		CAFile:     cfg.CAFile,
		CertFile:   cfg.CertFile,
		KeyFile:    cfg.KeyFile,
		ServerName: cfg.ServerName,
		RPCTimeout: 18 * time.Second,
		Logger:     logger,
		Metrics:    metrics,
		PacketHook: tap.Record,
	}

	session, err := mqttclient.NewSharedSession(ctx, buildSharedUplinkSessionConfig(cfg, logger, metrics))
	if err != nil {
		return nil, nil, err
	}
	transport, err := concentrator.NewSharedMQTTTransport(ctx, session, transportCfg)
	if err != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = session.Close(shutdownCtx)
		cancel()
		return nil, nil, err
	}
	return session, transport, nil
}
