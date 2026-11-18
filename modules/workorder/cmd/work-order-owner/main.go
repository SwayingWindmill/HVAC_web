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
	"github.com/quanlaihe/hvac-web/modules/workorder/pkg/workorderservice"
)

func main() {
	os.Exit(run())
}

func run() int {
	logger := observability.NewJSONLogger(os.Stdout, slog.LevelInfo)
	telemetry := observability.NewRuntime(observability.RuntimeConfig{
		Service: "work-order-service", OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), QueueSize: 1024, ExportTimeout: 500 * time.Millisecond,
	})
	var telemetryShutdownOnce sync.Once
	shutdownTelemetry := func(ctx context.Context) error {
		var shutdownErr error
		telemetryShutdownOnce.Do(func() {
			shutdownErr = telemetry.Shutdown(ctx)
		})
		return shutdownErr
	}
	var store *workorderservice.PostgresStore
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = shutdownTelemetry(ctx)
		cancel()
		if store != nil {
			store.Close()
		}
	}()

	certPath, err := requiredEnv("WORK_ORDER_TLS_CERT")
	if err != nil {
		logEnvError(logger, "work_order_tls_configuration_invalid", "WORK_ORDER_TLS_CONFIGURATION_INVALID", err)
		return 1
	}
	keyPath, err := requiredEnv("WORK_ORDER_TLS_KEY")
	if err != nil {
		logEnvError(logger, "work_order_tls_configuration_invalid", "WORK_ORDER_TLS_CONFIGURATION_INVALID", err)
		return 1
	}
	clientCAPath, err := requiredEnv("WORK_ORDER_CLIENT_CA")
	if err != nil {
		logEnvError(logger, "work_order_tls_configuration_invalid", "WORK_ORDER_TLS_CONFIGURATION_INVALID", err)
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
		logger.Error("work_order_tls_configuration_invalid", "error_code", "WORK_ORDER_TLS_CONFIGURATION_INVALID")
		return 1
	}
	delegationCertPath, err := requiredEnv("WORK_ORDER_GATEWAY_DELEGATION_CERT")
	if err != nil {
		logEnvError(logger, "work_order_gateway_key_load_failed", "WORK_ORDER_GATEWAY_KEY_LOAD_FAILED", err)
		return 1
	}
	gatewayPublicKey, err := loadCertificatePublicKey(delegationCertPath)
	if err != nil {
		logger.Error("work_order_gateway_key_load_failed", "error_code", "WORK_ORDER_GATEWAY_KEY_LOAD_FAILED")
		return 1
	}
	databaseURLFile, err := requiredEnv("WORK_ORDER_DATABASE_URL_FILE")
	if err != nil {
		logEnvError(logger, "work_order_database_reference_invalid", "WORK_ORDER_DATABASE_REFERENCE_INVALID", err)
		return 1
	}
	databaseURL, err := loadRequiredValueFile(databaseURLFile, 64<<10)
	if err != nil {
		logger.Error("work_order_database_reference_invalid", "error_code", "WORK_ORDER_DATABASE_REFERENCE_INVALID")
		return 1
	}
	mutationDatabaseURLFile, err := requiredEnv("WORK_ORDER_MUTATION_DATABASE_URL_FILE")
	if err != nil {
		logEnvError(logger, "work_order_mutation_database_reference_invalid", "WORK_ORDER_MUTATION_DATABASE_REFERENCE_INVALID", err)
		return 1
	}
	mutationDatabaseURL, err := loadRequiredValueFile(mutationDatabaseURLFile, 64<<10)
	if err != nil {
		logger.Error("work_order_mutation_database_reference_invalid", "error_code", "WORK_ORDER_MUTATION_DATABASE_REFERENCE_INVALID")
		return 1
	}
	cursorSecretFile, err := requiredEnv("WORK_ORDER_CURSOR_SECRET_FILE")
	if err != nil {
		logEnvError(logger, "work_order_cursor_secret_invalid", "WORK_ORDER_CURSOR_SECRET_INVALID", err)
		return 1
	}
	cursorSecret, err := loadRequiredValueFile(cursorSecretFile, 4<<10)
	if err != nil || len(cursorSecret) < 32 {
		logger.Error("work_order_cursor_secret_invalid", "error_code", "WORK_ORDER_CURSOR_SECRET_INVALID")
		return 1
	}
	openContext, cancelOpen := context.WithTimeout(context.Background(), 10*time.Second)
	store, err = workorderservice.OpenPostgresStoreWithMutations(openContext, databaseURL, mutationDatabaseURL, []byte(cursorSecret))
	cancelOpen()
	if err != nil {
		logger.Error("work_order_database_open_failed", "error_code", "WORK_ORDER_DATABASE_OPEN_FAILED")
		return 1
	}
	telemetry.SetDependencies(observability.Dependency{Name: "postgres", Required: true, Check: store.Ping})
	gatewaySPIFFE := envOr("WORK_ORDER_GATEWAY_SPIFFE", workorderservice.DefaultGatewaySPIFFEID)
	handler, err := workorderservice.NewHTTPHandler(workorderservice.HTTPConfig{
		Store: store, GatewayPublicKey: gatewayPublicKey, GatewaySPIFFEID: gatewaySPIFFE,
		Audience: envOr("WORK_ORDER_READ_AUDIENCE", workorderservice.DefaultAudience), MaxListLimit: 100,
	})
	if err != nil {
		logger.Error("work_order_http_configuration_invalid", "error_code", "WORK_ORDER_HTTP_CONFIGURATION_INVALID")
		return 1
	}
	router, err := workorderservice.NewOwnerRouter(workorderservice.OwnerRouterConfig{
		GatewaySPIFFE: gatewaySPIFFE,
		ReaderSPIFFE:  envOr("WORK_ORDER_OPERATIONS_AGENT_SPIFFE", "spiffe://hvac.local/operations-agent-service"),
		Handler:       handler,
	})
	if err != nil {
		logger.Error("work_order_owner_router_configuration_invalid", "error_code", "WORK_ORDER_OWNER_ROUTER_CONFIGURATION_INVALID")
		return 1
	}
	server := &http.Server{
		Addr: envOr("WORK_ORDER_SERVICE_ADDR", ":8449"), Handler: observability.InstrumentHTTP(router, telemetry, observability.HTTPInstrumentationConfig{
			Namespace: "hvac_work_order", Service: "work-order-service", SpanName: "http.work_order.request",
		}), TLSConfig: serverTLSConfig,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second,
	}
	diagnostics := newWorkOrderDiagnosticsServer(envOr("WORK_ORDER_DIAGNOSTICS_ADDR", ":19089"), telemetry)
	diagnosticsDone := make(chan struct{})
	go func() {
		defer close(diagnosticsDone)
		defer observability.Recover(logger, telemetry.Metrics, "work_order_owner.diagnostics")
		if err := diagnostics.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("work_order_diagnostics_failed", "error_code", "DIAGNOSTICS_SERVE_FAILED")
		}
	}()

	signalCtx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		defer observability.Recover(logger, telemetry.Metrics, "work_order_owner.shutdown")
		<-signalCtx.Done()
		shutdownWorkOrderOwner(workOrderShutdownDeps{
			markNotReady:      telemetry.MarkNotReady,
			shutdownServer:    server.Shutdown,
			shutdownDiag:      diagnostics.Shutdown,
			diagnosticsDone:   diagnosticsDone,
			shutdownTelemetry: shutdownTelemetry,
		})
	}()
	telemetry.MarkReady()
	logger.Info("work_order_service_started", "address", server.Addr, "diagnostics_address", diagnostics.Addr, "mode", "internal-read-only")
	exitCode := 0
	if err := server.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("work_order_service_stopped_unexpectedly", "error_code", "WORK_ORDER_SERVE_FAILED")
		exitCode = 1
	}
	stopSignals()
	<-shutdownDone
	if exitCode == 0 {
		logger.Info("work_order_service_stopped")
	}
	return exitCode
}

func newWorkOrderDiagnosticsServer(addr string, telemetry *observability.Runtime) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           telemetry.DiagnosticsHandler(),
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       3 * time.Second,
		WriteTimeout:      3 * time.Second,
	}
}

type workOrderShutdownDeps struct {
	markNotReady      func()
	shutdownServer    func(context.Context) error
	shutdownDiag      func(context.Context) error
	diagnosticsDone   <-chan struct{}
	shutdownTelemetry func(context.Context) error
}

func shutdownWorkOrderOwner(deps workOrderShutdownDeps) {
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
	if deps.shutdownTelemetry != nil {
		_ = deps.shutdownTelemetry(ctx)
	}
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
