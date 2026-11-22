package main

import (
	"context"
	"crypto"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/workloadtls"
	"github.com/quanlaihe/hvac-web/modules/alarm/pkg/alarmservice"
)

func main() {
	os.Exit(run())
}

func run() int {
	logger := observability.NewJSONLogger(os.Stdout, slog.LevelInfo)
	telemetry := observability.NewRuntime(observability.RuntimeConfig{
		Service: "alarm-service", OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), QueueSize: 1024, ExportTimeout: 500 * time.Millisecond,
	})
	var telemetryShutdownOnce sync.Once
	shutdownTelemetry := func(ctx context.Context) error {
		var shutdownErr error
		telemetryShutdownOnce.Do(func() {
			shutdownErr = telemetry.Shutdown(ctx)
		})
		return shutdownErr
	}
	var store *alarmservice.PostgresStore
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = shutdownTelemetry(ctx)
		cancel()
		if store != nil {
			store.Close()
		}
	}()

	certPath, err := requiredEnv("ALARM_TLS_CERT")
	if err != nil {
		logEnvError(logger, "alarm_tls_configuration_invalid", "ALARM_TLS_CONFIGURATION_INVALID", err)
		return 1
	}
	keyPath, err := requiredEnv("ALARM_TLS_KEY")
	if err != nil {
		logEnvError(logger, "alarm_tls_configuration_invalid", "ALARM_TLS_CONFIGURATION_INVALID", err)
		return 1
	}
	clientCAPath, err := requiredEnv("ALARM_CLIENT_CA")
	if err != nil {
		logEnvError(logger, "alarm_tls_configuration_invalid", "ALARM_TLS_CONFIGURATION_INVALID", err)
		return 1
	}
	serverTLSConfig, err := workloadtls.NewServerTLSConfig(workloadtls.ServerConfig{
		CertificateFiles: workloadtls.CertificateFiles{
			CertificatePath: certPath,
			PrivateKeyPath:  keyPath,
		},
		ClientCAPath: clientCAPath,
	})
	if err != nil {
		logger.Error("alarm_tls_configuration_invalid", "error_code", "ALARM_TLS_CONFIGURATION_INVALID")
		return 1
	}
	delegationCertPath, err := requiredEnv("ALARM_GATEWAY_DELEGATION_CERT")
	if err != nil {
		logEnvError(logger, "alarm_gateway_key_load_failed", "ALARM_GATEWAY_KEY_LOAD_FAILED", err)
		return 1
	}
	gatewayPublicKey, err := loadCertificatePublicKey(delegationCertPath)
	if err != nil {
		logger.Error("alarm_gateway_key_load_failed", "error_code", "ALARM_GATEWAY_KEY_LOAD_FAILED")
		return 1
	}
	databaseURLFile, err := requiredEnv("ALARM_DATABASE_URL_FILE")
	if err != nil {
		logEnvError(logger, "alarm_database_reference_invalid", "ALARM_DATABASE_REFERENCE_INVALID", err)
		return 1
	}
	databaseURL, err := loadRequiredValueFile(databaseURLFile, 64<<10)
	if err != nil {
		logger.Error("alarm_database_reference_invalid", "error_code", "ALARM_DATABASE_REFERENCE_INVALID")
		return 1
	}
	openContext, cancelOpen := context.WithTimeout(context.Background(), 10*time.Second)
	store, err = alarmservice.OpenPostgresStore(openContext, databaseURL)
	cancelOpen()
	if err != nil {
		logger.Error("alarm_database_open_failed", "error_code", "ALARM_DATABASE_OPEN_FAILED")
		return 1
	}
	checker, err := alarmservice.LoadConnectivityEdgeRuleSyncChecker(logger)
	if err != nil {
		logger.Error("alarm_edge_dedup_configuration_invalid", "error_code", "ALARM_EDGE_DEDUP_CONFIGURATION_INVALID")
		return 1
	}
	store.SetEdgeRuleSyncChecker(checker)
	gatewaySPIFFE := envOr("ALARM_GATEWAY_SPIFFE", alarmservice.DefaultGatewaySPIFFEID)
	handler, err := alarmservice.NewHTTPHandler(alarmservice.HTTPConfig{
		Store: store, Rules: store, Metrics: telemetry.Metrics, GatewayPublicKey: gatewayPublicKey, GatewaySPIFFEID: gatewaySPIFFE,
		Audience: envOr("ALARM_READ_AUDIENCE", alarmservice.DefaultAudience), MaxListLimit: 100,
	})
	if err != nil {
		logger.Error("alarm_http_configuration_invalid", "error_code", "ALARM_HTTP_CONFIGURATION_INVALID")
		return 1
	}
	telemetryEvaluationHandler, err := alarmservice.NewTelemetryEvaluationHandler(store)
	if err != nil {
		logger.Error("alarm_telemetry_evaluation_configuration_invalid", "error_code", "ALARM_TELEMETRY_EVALUATION_CONFIGURATION_INVALID")
		return 1
	}
	router, err := buildAlarmOwnerRouter(store, gatewaySPIFFE, handler, telemetryEvaluationHandler)
	if err != nil {
		logger.Error("alarm_owner_router_configuration_invalid", "error_code", "ALARM_OWNER_ROUTER_CONFIGURATION_INVALID")
		return 1
	}
	instrumentedRouter := observability.InstrumentHTTP(router, telemetry, observability.HTTPInstrumentationConfig{
		Namespace: "hvac_alarm", Service: "alarm-service", SpanName: "http.alarm.request", Route: alarmObservabilityRoute,
	})
	server := &http.Server{
		Addr: envOr("ALARM_SERVICE_ADDR", ":8448"), Handler: instrumentedRouter, TLSConfig: serverTLSConfig,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second,
	}
	diagnostics := &http.Server{
		Addr: envOr("ALARM_DIAGNOSTICS_ADDR", ":19088"), Handler: telemetry.DiagnosticsHandler(),
		ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second,
	}
	diagnosticsDone := make(chan struct{})
	go func() {
		defer close(diagnosticsDone)
		defer observability.Recover(logger, telemetry.Metrics, "alarm_owner.diagnostics")
		if err := diagnostics.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("alarm_diagnostics_failed", "error_code", "DIAGNOSTICS_SERVE_FAILED")
		}
	}()
	scheduledCtx, cancelScheduled := context.WithCancel(context.Background())
	defer cancelScheduled()
	scheduledDone := alarmservice.StartScheduledEvaluations(scheduledCtx, store, logger)
	actionRelayWorkerID := envOr("ALARM_ACTION_RELAY_WORKER_ID", "alarm-service-action-relay")
	if host, hostErr := os.Hostname(); hostErr == nil && strings.TrimSpace(host) != "" {
		actionRelayWorkerID += ":" + strings.TrimSpace(host)
	}
	actionRelay, err := alarmservice.NewRuleActionRelay(alarmservice.RuleActionRelayConfig{
		Store:         store,
		WebhookSender: alarmservice.NewSSRFWebhookSender(alarmservice.NewDirWebhookSecretResolver(os.Getenv("ALARM_WEBHOOK_SECRET_DIR"))),
		Metrics:       telemetry.Metrics,
		Logger:        logger,
		WorkerID:      actionRelayWorkerID,
	})
	if err != nil {
		logger.Error("alarm_rule_action_relay_configuration_invalid", "error_code", "ALARM_RULE_ACTION_RELAY_CONFIGURATION_INVALID")
		return 1
	}
	actionRelayDone := actionRelay.Start(scheduledCtx, 5*time.Second)

	signalCtx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()

	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		defer observability.Recover(logger, telemetry.Metrics, "alarm_owner.shutdown")
		<-signalCtx.Done()
		shutdownAlarmOwnerHTTP(alarmOwnerHTTPShutdownDeps{
			markNotReady:    telemetry.MarkNotReady,
			shutdownServer:  server.Shutdown,
			shutdownDiag:    diagnostics.Shutdown,
			diagnosticsDone: diagnosticsDone,
		})
	}()
	telemetry.MarkReady()
	logger.Info("alarm_service_started", "address", server.Addr, "mode", "alarm-lifecycle")
	exitCode := 0
	if err := server.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("alarm_service_stopped_unexpectedly", "error_code", "ALARM_SERVE_FAILED")
		exitCode = 1
	}
	stopSignals()
	<-shutdownDone
	drainAlarmOwnerWorkers(alarmOwnerWorkerDrainDeps{
		cancelScheduled:   cancelScheduled,
		scheduledDone:     scheduledDone,
		actionRelayDone:   actionRelayDone,
		shutdownTelemetry: shutdownTelemetry,
	})
	if exitCode == 0 {
		logger.Info("alarm_service_stopped")
	}
	return exitCode
}

type alarmOwnerHTTPShutdownDeps struct {
	markNotReady    func()
	shutdownServer  func(context.Context) error
	shutdownDiag    func(context.Context) error
	diagnosticsDone <-chan struct{}
}

func shutdownAlarmOwnerHTTP(deps alarmOwnerHTTPShutdownDeps) {
	if deps.markNotReady != nil {
		deps.markNotReady()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if deps.shutdownServer != nil {
		_ = deps.shutdownServer(ctx)
	}
	if deps.shutdownDiag != nil {
		_ = deps.shutdownDiag(ctx)
	}
	if deps.diagnosticsDone != nil {
		select {
		case <-deps.diagnosticsDone:
		case <-ctx.Done():
		}
	}
}

type alarmOwnerWorkerDrainDeps struct {
	cancelScheduled   func()
	scheduledDone     <-chan struct{}
	actionRelayDone   <-chan struct{}
	shutdownTelemetry func(context.Context) error
}

func drainAlarmOwnerWorkers(deps alarmOwnerWorkerDrainDeps) {
	if deps.cancelScheduled != nil {
		deps.cancelScheduled()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if deps.scheduledDone != nil {
		select {
		case <-deps.scheduledDone:
		case <-ctx.Done():
		}
	}
	if deps.actionRelayDone != nil {
		select {
		case <-deps.actionRelayDone:
		case <-ctx.Done():
		}
	}
	if deps.shutdownTelemetry != nil {
		_ = deps.shutdownTelemetry(ctx)
	}
}

func alarmObservabilityRoute(request *http.Request) string {
	if request == nil {
		return "unknown"
	}
	path := request.URL.Path
	if path == alarmservice.InternalTelemetryEvaluationPath {
		return "alarms.evaluate_telemetry"
	}
	if path == alarmservice.InternalControlEventsPath {
		return "alarms.publish_control_event"
	}
	if path == alarmservice.InternalEdgeAlarmEventsPath {
		return "alarms.ingest_edge_event"
	}
	if !strings.HasPrefix(path, alarmservice.InternalSiteAlarmsPrefix) {
		return "unknown"
	}
	if strings.HasSuffix(path, "/alarm-rules") ||
		strings.HasSuffix(path, "/alarm-rules:simulate") ||
		strings.HasSuffix(path, "/alarm-rules:diff") ||
		strings.HasSuffix(path, "/alarm-rules/revisions") ||
		(strings.Contains(path, "/alarm-rules/revisions/") && strings.HasSuffix(path, ":rollback")) {
		return "alarms.rules"
	}
	if strings.Contains(path, "/alarm-rules/dead-letters") {
		return "alarms.dead_letters"
	}
	for suffix, operation := range map[string]string{
		":acknowledge": "alarms.acknowledge",
		":assign":      "alarms.assign",
		":unassign":    "alarms.unassign",
		":suppress":    "alarms.suppress",
		":unsuppress":  "alarms.unsuppress",
		":close":       "alarms.close",
		":reopen":      "alarms.reopen",
	} {
		if strings.HasSuffix(path, suffix) {
			return operation
		}
	}
	if strings.HasSuffix(path, "/alarms") {
		return "alarms.list"
	}
	if strings.Contains(path, "/alarms/") {
		return "alarms.get"
	}
	return "unknown"
}

func buildAlarmOwnerRouter(store alarmservice.Store, gatewaySPIFFE string, gatewayHandler, telemetryHandler http.Handler) (http.Handler, error) {
	controlEventsHandler, err := alarmservice.NewControlFailureHandler(store)
	if err != nil {
		return nil, err
	}
	var edgeEventsHandler http.Handler
	if ingestor, ok := store.(alarmservice.EdgeAlarmEventIngestor); ok {
		edgeEventsHandler, err = alarmservice.NewEdgeAlarmEventHandler(ingestor)
		if err != nil {
			return nil, err
		}
	}
	return alarmservice.NewOwnerRouter(alarmservice.OwnerRouterConfig{
		GatewaySPIFFE:   gatewaySPIFFE,
		TelemetrySPIFFE: envOr("ALARM_TELEMETRY_SPIFFE", "spiffe://hvac.local/telemetry-runtime-service"),
		ReaderSPIFFE:    envOr("ALARM_OPERATIONS_AGENT_SPIFFE", "spiffe://hvac.local/operations-agent-service"),
		ControlEventsSPIFFEs: []string{
			envOr("ALARM_COMMAND_SPIFFE", "spiffe://hvac.local/command-service"),
			envOr("ALARM_CONNECTIVITY_SPIFFE", "spiffe://hvac.local/connectivity-service"),
			envOr("ALARM_OPTIMIZATION_SPIFFE", "spiffe://hvac.local/optimization-service"),
		},
		GatewayHandler:       gatewayHandler,
		TelemetryHandler:     telemetryHandler,
		ControlEventsHandler: controlEventsHandler,
		EdgeEventsHandler:    edgeEventsHandler,
	})
}

func loadCertificatePublicKey(path string) (crypto.PublicKey, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(body)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("public key certificate is invalid")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}
	return certificate.PublicKey, nil
}

func loadRequiredValueFile(path string, maximumBytes int64) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" || maximumBytes <= 0 {
		return "", errors.New("value file configuration is invalid")
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() <= 0 || info.Size() > maximumBytes {
		return "", errors.New("value file size is invalid")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(body))
	if value == "" || strings.ContainsAny(value, "\r\n\x00") {
		return "", errors.New("value file content is invalid")
	}
	return value, nil
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func logEnvError(logger *slog.Logger, msg, code string, err error) {
	if err != nil {
		_, _ = os.Stderr.WriteString(err.Error() + "\n")
	}
	if logger != nil {
		logger.Error(msg, "error_code", code)
	}
}

func requiredEnv(name string) (string, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return value, nil
}
