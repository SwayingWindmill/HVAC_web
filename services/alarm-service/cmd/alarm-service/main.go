package main

import (
	"context"
	"crypto"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/workloadtls"
	"github.com/quanlaihe/hvac-web/services/alarm-service/pkg/alarmservice"
)

func main() {
	logger := observability.NewJSONLogger(os.Stdout, slog.LevelInfo)
	telemetry := observability.NewRuntime(observability.RuntimeConfig{
		Service: "alarm-service", OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), QueueSize: 1024, ExportTimeout: 500 * time.Millisecond,
	})
	serverTLSConfig, err := workloadtls.NewServerTLSConfig(workloadtls.ServerConfig{
		CertificateFiles: workloadtls.CertificateFiles{
			CertificatePath: requiredEnv("ALARM_TLS_CERT"),
			PrivateKeyPath:  requiredEnv("ALARM_TLS_KEY"),
		},
		ClientCAPath: requiredEnv("ALARM_CLIENT_CA"),
	})
	if err != nil {
		logger.Error("alarm_tls_configuration_invalid", "error_code", "ALARM_TLS_CONFIGURATION_INVALID")
		os.Exit(1)
	}
	gatewayPublicKey, err := loadCertificatePublicKey(requiredEnv("ALARM_GATEWAY_DELEGATION_CERT"))
	if err != nil {
		logger.Error("alarm_gateway_key_load_failed", "error_code", "ALARM_GATEWAY_KEY_LOAD_FAILED")
		os.Exit(1)
	}
	databaseURL, err := loadRequiredValueFile(requiredEnv("ALARM_DATABASE_URL_FILE"), 64<<10)
	if err != nil {
		logger.Error("alarm_database_reference_invalid", "error_code", "ALARM_DATABASE_REFERENCE_INVALID")
		os.Exit(1)
	}
	openContext, cancelOpen := context.WithTimeout(context.Background(), 10*time.Second)
	store, err := alarmservice.OpenPostgresStore(openContext, databaseURL)
	cancelOpen()
	if err != nil {
		logger.Error("alarm_database_open_failed", "error_code", "ALARM_DATABASE_OPEN_FAILED")
		os.Exit(1)
	}
	defer store.Close()
	gatewaySPIFFE := envOr("ALARM_GATEWAY_SPIFFE", alarmservice.DefaultGatewaySPIFFEID)
	handler, err := alarmservice.NewHTTPHandler(alarmservice.HTTPConfig{
		Store: store, GatewayPublicKey: gatewayPublicKey, GatewaySPIFFEID: gatewaySPIFFE,
		Audience: envOr("ALARM_READ_AUDIENCE", alarmservice.DefaultAudience), MaxListLimit: 100,
	})
	if err != nil {
		logger.Error("alarm_http_configuration_invalid", "error_code", "ALARM_HTTP_CONFIGURATION_INVALID")
		os.Exit(1)
	}
	router := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if peerSPIFFE(request) != gatewaySPIFFE {
			writeProblem(writer, http.StatusForbidden, "ALARM_GATEWAY_WORKLOAD_FORBIDDEN")
			return
		}
		handler.ServeHTTP(writer, request)
	})
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
	go func() {
		if err := diagnostics.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("alarm_diagnostics_failed", "error_code", "DIAGNOSTICS_SERVE_FAILED")
		}
	}()
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-shutdown
		telemetry.MarkNotReady()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		_ = diagnostics.Shutdown(ctx)
		_ = telemetry.Shutdown(ctx)
	}()
	telemetry.MarkReady()
	logger.Info("alarm_service_started", "address", server.Addr, "mode", "alarm-lifecycle")
	if err := server.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("alarm_service_stopped_unexpectedly", "error_code", "ALARM_SERVE_FAILED")
		os.Exit(1)
	}
	logger.Info("alarm_service_stopped")
}

func alarmObservabilityRoute(request *http.Request) string {
	if request == nil {
		return "unknown"
	}
	path := request.URL.Path
	if !strings.HasPrefix(path, alarmservice.InternalSiteAlarmsPrefix) {
		return "unknown"
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

func peerSPIFFE(request *http.Request) string {
	if request == nil || request.TLS == nil || len(request.TLS.PeerCertificates) == 0 {
		return ""
	}
	leaf := request.TLS.PeerCertificates[0]
	if leaf == nil || len(leaf.URIs) != 1 || leaf.URIs[0] == nil {
		return ""
	}
	identity := leaf.URIs[0].String()
	if !strings.HasPrefix(identity, "spiffe://") {
		return ""
	}
	return identity
}

func writeProblem(writer http.ResponseWriter, status int, code string) {
	writer.Header().Set("Content-Type", "application/problem+json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]any{"code": code, "retryable": false})
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func requiredEnv(name string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		_, _ = os.Stderr.WriteString(name + " is required\n")
		os.Exit(1)
	}
	return value
}
