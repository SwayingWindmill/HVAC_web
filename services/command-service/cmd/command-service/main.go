package main

import (
	"context"
	"crypto"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandauth"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/workloadtls"
	"github.com/quanlaihe/hvac-web/services/command-service/pkg/commandservice"
)

func main() {
	logger := observability.NewJSONLogger(os.Stdout, slog.LevelInfo)
	telemetry := observability.NewRuntime(observability.RuntimeConfig{
		Service: "command-service", OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), QueueSize: 2048, ExportTimeout: 500 * time.Millisecond,
	})

	serverTLSConfig, err := workloadtls.NewServerTLSConfig(workloadtls.ServerConfig{
		CertificateFiles: workloadtls.CertificateFiles{
			CertificatePath: requiredEnv("COMMAND_TLS_CERT"),
			PrivateKeyPath:  requiredEnv("COMMAND_TLS_KEY"),
		},
		ClientCAPath: requiredEnv("COMMAND_CLIENT_CA"),
	})
	if err != nil {
		logger.Error("command_tls_configuration_invalid", "error_code", "COMMAND_TLS_CONFIGURATION_INVALID")
		os.Exit(1)
	}
	commandGrantPublicKey, err := loadCertificatePublicKey(requiredEnv("COMMAND_IAM_GRANT_CERT"))
	if err != nil {
		logger.Error("command_iam_grant_key_load_failed", "error_code", "COMMAND_IAM_GRANT_KEY_LOAD_FAILED")
		os.Exit(1)
	}
	gatewayDelegationPublicKey, err := loadCertificatePublicKey(requiredEnv("COMMAND_GATEWAY_DELEGATION_CERT"))
	if err != nil {
		logger.Error("command_gateway_delegation_key_load_failed", "error_code", "COMMAND_GATEWAY_DELEGATION_KEY_LOAD_FAILED")
		os.Exit(1)
	}

	databaseURL, err := loadRequiredValueFile(requiredEnv("COMMAND_DATABASE_URL_FILE"), 64<<10)
	if err != nil {
		logger.Error("command_database_reference_invalid", "error_code", "COMMAND_DATABASE_REFERENCE_INVALID")
		os.Exit(1)
	}
	openContext, cancelOpen := context.WithTimeout(context.Background(), 10*time.Second)
	store, err := commandservice.OpenPostgresStore(openContext, databaseURL)
	cancelOpen()
	if err != nil {
		logger.Error("command_database_open_failed", "error_code", "COMMAND_DATABASE_OPEN_FAILED")
		os.Exit(1)
	}
	defer store.Close()

	policyRevision := requiredEnv("COMMAND_POLICY_REVISION")
	revocationRevision, err := parseUint64Env("COMMAND_EMERGENCY_REVOCATION_REVISION")
	if err != nil {
		logger.Error("command_revocation_revision_invalid", "error_code", "COMMAND_REVOCATION_REVISION_INVALID")
		os.Exit(1)
	}
	gatewaySPIFFE := envOr("COMMAND_GATEWAY_SPIFFE", "spiffe://hvac.local/platform-gateway")

	commandHandler, err := commandservice.NewHTTPHandler(commandservice.HTTPConfig{
		Authority:                  store,
		CommandGrantPublicKey:      commandGrantPublicKey,
		CommandGrantIssuer:         envOr("COMMAND_GRANT_ISSUER", "spiffe://hvac.local/iam-service"),
		GatewaySPIFFE:              gatewaySPIFFE,
		CommandGrantAudience:       envOr("COMMAND_GRANT_AUDIENCE", "command-service"),
		GatewayDelegationPublicKey: gatewayDelegationPublicKey,
		GatewayReadAudience:        envOr("COMMAND_GATEWAY_READ_AUDIENCE", "command-service"),
		CommandGrantUseChecker: func(claims commandauth.GrantClaims) (commandauth.UseStatus, error) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			return store.ConsumeCommandGrant(ctx, claims, policyRevision, revocationRevision)
		},
	})
	if err != nil {
		logger.Error("command_http_configuration_invalid", "error_code", "COMMAND_HTTP_CONFIGURATION_INVALID")
		os.Exit(1)
	}
	runtimeConfig, err := loadRuntimeHTTPConfig(store)
	if err != nil {
		logger.Error("command_runtime_cohort_load_failed", "error_code", "COMMAND_RUNTIME_COHORT_INVALID")
		os.Exit(1)
	}
	runtimeHandler, err := commandservice.NewRuntimeHTTPHandler(runtimeConfig)
	if err != nil {
		logger.Error("command_runtime_configuration_invalid", "error_code", "COMMAND_RUNTIME_CONFIGURATION_INVALID")
		os.Exit(1)
	}

	router := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == commandservice.InternalCommandsPath || strings.HasPrefix(request.URL.Path, commandservice.InternalCommandsPath+"/") {
			if peerSPIFFE(request) != gatewaySPIFFE {
				writeProblem(writer, http.StatusForbidden, "COMMAND_GATEWAY_WORKLOAD_FORBIDDEN")
				return
			}
			commandHandler.ServeHTTP(writer, request)
			return
		}
		runtimeHandler.ServeHTTP(writer, request)
	})
	server := &http.Server{
		Addr:              envOr("COMMAND_SERVICE_ADDR", ":8447"),
		Handler:           router,
		TLSConfig:         serverTLSConfig,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	diagnostics := &http.Server{
		Addr: envOr("COMMAND_DIAGNOSTICS_ADDR", ":19087"), Handler: telemetry.DiagnosticsHandler(),
		ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second,
	}
	go func() {
		if err := diagnostics.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("command_diagnostics_failed", "error_code", "DIAGNOSTICS_SERVE_FAILED")
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
	logger.Info("command_service_started", "address", server.Addr, "policy_revision", policyRevision)
	if err := server.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("command_service_stopped_unexpectedly", "error_code", "COMMAND_SERVE_FAILED")
		os.Exit(1)
	}
	logger.Info("command_service_stopped")
}

type runtimeCohortDocument struct {
	SchemaVersion int                            `json:"schemaVersion"`
	Cohorts       []commandservice.RuntimeCohort `json:"cohorts"`
}

func loadRuntimeHTTPConfig(store commandservice.RuntimeStore) (commandservice.RuntimeHTTPConfig, error) {
	path := strings.TrimSpace(os.Getenv("COMMAND_RUNTIME_COHORTS_FILE"))
	if path == "" {
		return commandservice.RuntimeHTTPConfig{
			Store:            store,
			DispatcherSPIFFE: envOr("COMMAND_DISPATCHER_SPIFFE", "spiffe://hvac.local/command-dispatcher"),
			VerifierSPIFFE:   envOr("COMMAND_VERIFIER_SPIFFE", "spiffe://hvac.local/command-verifier"),
			OrganizationID:   requiredEnv("COMMAND_APPROVED_ORGANIZATION_ID"),
			SiteID:           requiredEnv("COMMAND_APPROVED_SITE_ID"),
			DeviceID:         requiredEnv("COMMAND_APPROVED_DEVICE_ID"),
		}, nil
	}
	if !filepath.IsAbs(path) {
		return commandservice.RuntimeHTTPConfig{}, errors.New("runtime cohort path must be absolute")
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() <= 0 || info.Size() > 64<<10 {
		return commandservice.RuntimeHTTPConfig{}, errors.New("runtime cohort file size is invalid")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return commandservice.RuntimeHTTPConfig{}, err
	}
	var document runtimeCohortDocument
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return commandservice.RuntimeHTTPConfig{}, errors.New("runtime cohort document is invalid")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) || document.SchemaVersion != 1 || len(document.Cohorts) == 0 {
		return commandservice.RuntimeHTTPConfig{}, errors.New("runtime cohort document is invalid")
	}
	return commandservice.RuntimeHTTPConfig{Store: store, Cohorts: document.Cohorts}, nil
}

func loadRequiredValueFile(path string, maximumBytes int64) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" || maximumBytes <= 0 {
		return "", errors.New("value file configuration is invalid")
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.Size() <= 0 || info.Size() > maximumBytes {
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
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]any{"code": code, "retryable": false})
}

func parseUint64Env(name string) (uint64, error) {
	value := requiredEnv(name)
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s is invalid", name)
	}
	return parsed, nil
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
