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

	"github.com/quanlaihe/hvac-web/libs/workloadtls"
	"github.com/quanlaihe/hvac-web/services/alarm-service/pkg/alarmservice"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
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
	server := &http.Server{
		Addr: envOr("ALARM_SERVICE_ADDR", ":8448"), Handler: router, TLSConfig: serverTLSConfig,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second,
	}
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-shutdown
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()
	logger.Info("alarm_service_started", "address", server.Addr, "mode", "read-only-contract-baseline")
	if err := server.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("alarm_service_stopped_unexpectedly", "error_code", "ALARM_SERVE_FAILED")
		os.Exit(1)
	}
	logger.Info("alarm_service_stopped")
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
