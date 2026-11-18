package main

import (
	"bytes"
	"context"
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandauth"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/workloadtls"
	"github.com/quanlaihe/hvac-web/modules/command/pkg/commandservice"
)

func main() {
	os.Exit(run())
}

func run() int {
	logger := observability.NewJSONLogger(os.Stdout, slog.LevelInfo)
	telemetry := observability.NewRuntime(observability.RuntimeConfig{
		Service: "command-service", OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), QueueSize: 2048, ExportTimeout: 500 * time.Millisecond,
	})
	var telemetryShutdownOnce sync.Once
	shutdownTelemetry := func(ctx context.Context) error {
		var shutdownErr error
		telemetryShutdownOnce.Do(func() {
			shutdownErr = telemetry.Shutdown(ctx)
		})
		return shutdownErr
	}
	var (
		store         *commandservice.PostgresStore
		authority     *commandservice.ControlAuthority
		runtimeCloser interface{ Close() }
	)
	defer func() {
		if runtimeCloser != nil {
			runtimeCloser.Close()
		}
		if authority != nil {
			authority.Close()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = shutdownTelemetry(ctx)
		cancel()
		if store != nil {
			store.Close()
		}
	}()

	certPath, err := requiredEnv("COMMAND_TLS_CERT")
	if err != nil {
		logEnvError(logger, "command_tls_configuration_invalid", "COMMAND_TLS_CONFIGURATION_INVALID", err)
		return 1
	}
	keyPath, err := requiredEnv("COMMAND_TLS_KEY")
	if err != nil {
		logEnvError(logger, "command_tls_configuration_invalid", "COMMAND_TLS_CONFIGURATION_INVALID", err)
		return 1
	}
	clientCAPath, err := requiredEnv("COMMAND_CLIENT_CA")
	if err != nil {
		logEnvError(logger, "command_tls_configuration_invalid", "COMMAND_TLS_CONFIGURATION_INVALID", err)
		return 1
	}
	certFiles := workloadtls.CertificateFiles{
		CertificatePath: certPath,
		PrivateKeyPath:  keyPath,
	}
	serverTLSConfig, err := workloadtls.NewServerTLSConfig(workloadtls.ServerConfig{
		CertificateFiles: certFiles,
		ClientCAPath:     clientCAPath,
	})
	if err != nil {
		logger.Error("command_tls_configuration_invalid", "error_code", "COMMAND_TLS_CONFIGURATION_INVALID")
		return 1
	}
	tlsCert, err := tls.LoadX509KeyPair(certFiles.CertificatePath, certFiles.PrivateKeyPath)
	if err != nil {
		logger.Error("command_tls_configuration_invalid", "error_code", "COMMAND_TLS_CONFIGURATION_INVALID")
		return 1
	}
	delegationSigner, ok := tlsCert.PrivateKey.(crypto.Signer)
	if !ok {
		logger.Error("command_tls_configuration_invalid", "error_code", "COMMAND_TLS_CONFIGURATION_INVALID")
		return 1
	}
	commandGrantCertPath, err := requiredEnv("COMMAND_IAM_GRANT_CERT")
	if err != nil {
		logEnvError(logger, "command_iam_grant_key_load_failed", "COMMAND_IAM_GRANT_KEY_LOAD_FAILED", err)
		return 1
	}
	commandGrantPublicKey, err := loadCertificatePublicKey(commandGrantCertPath)
	if err != nil {
		logger.Error("command_iam_grant_key_load_failed", "error_code", "COMMAND_IAM_GRANT_KEY_LOAD_FAILED")
		return 1
	}
	gatewayDelegationCertPath, err := requiredEnv("COMMAND_GATEWAY_DELEGATION_CERT")
	if err != nil {
		logEnvError(logger, "command_gateway_delegation_key_load_failed", "COMMAND_GATEWAY_DELEGATION_KEY_LOAD_FAILED", err)
		return 1
	}
	gatewayDelegationPublicKey, err := loadCertificatePublicKey(gatewayDelegationCertPath)
	if err != nil {
		logger.Error("command_gateway_delegation_key_load_failed", "error_code", "COMMAND_GATEWAY_DELEGATION_KEY_LOAD_FAILED")
		return 1
	}

	databaseURLFile, err := requiredEnv("COMMAND_DATABASE_URL_FILE")
	if err != nil {
		logEnvError(logger, "command_database_reference_invalid", "COMMAND_DATABASE_REFERENCE_INVALID", err)
		return 1
	}
	databaseURL, err := loadRequiredValueFile(databaseURLFile, 64<<10)
	if err != nil {
		logger.Error("command_database_reference_invalid", "error_code", "COMMAND_DATABASE_REFERENCE_INVALID")
		return 1
	}
	openContext, cancelOpen := context.WithTimeout(context.Background(), 10*time.Second)
	store, err = commandservice.OpenPostgresStore(openContext, databaseURL)
	cancelOpen()
	if err != nil {
		logger.Error("command_database_open_failed", "error_code", "COMMAND_DATABASE_OPEN_FAILED")
		return 1
	}

	policyRevision, err := requiredEnv("COMMAND_POLICY_REVISION")
	if err != nil {
		logEnvError(logger, "command_policy_revision_invalid", "COMMAND_POLICY_REVISION_INVALID", err)
		return 1
	}
	revocationRevision, err := parseUint64Env("COMMAND_EMERGENCY_REVOCATION_REVISION")
	if err != nil {
		logEnvError(logger, "command_revocation_revision_invalid", "COMMAND_REVOCATION_REVISION_INVALID", err)
		return 1
	}
	gatewaySPIFFE := envOr("COMMAND_GATEWAY_SPIFFE", "spiffe://hvac.local/platform-gateway")
	commandServiceSPIFFE := envOr("COMMAND_WORKLOAD_SPIFFE", "spiffe://hvac.local/command-service")
	grantIssuer := envOr("COMMAND_GRANT_ISSUER", "spiffe://hvac.local/iam-service")
	grantAudience := envOr("COMMAND_GRANT_AUDIENCE", "command-service")
	gatewayReadAudience := envOr("COMMAND_GATEWAY_READ_AUDIENCE", "command-service")
	useChecker := func(claims commandauth.GrantClaims) (commandauth.UseStatus, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		return store.ConsumeCommandGrant(ctx, claims, policyRevision, revocationRevision)
	}
	adapters, err := commandservice.NewWorkloadAdaptersFromEnv(commandservice.WorkloadEnvConfig{
		CertificateFiles:      certFiles,
		DelegationSigner:      delegationSigner,
		DefaultServerCA:       clientCAPath,
		CommandServiceSPIFFE:  commandServiceSPIFFE,
		PolicyRevision:        policyRevision,
		CommandGrantPublicKey: commandGrantPublicKey,
	})
	if err != nil {
		logger.Error("command_adapters_configuration_invalid", "error_code", "COMMAND_ADAPTERS_CONFIGURATION_INVALID")
		return 1
	}
	alarmSender := buildCommandControlFailureSender(logger)
	var alarmRelay *commandservice.ControlAlarmRelay
	if alarmSender != nil {
		alarmRelay, err = commandservice.NewControlAlarmRelay(commandservice.ControlAlarmRelayConfig{
			Store:      store,
			Sender:     alarmSender,
			LeaseOwner: commandservice.UniqueControlAlarmLeaseOwner("command-owner"),
			Logger:     logger,
			Metrics:    telemetry.Metrics,
		})
		if err != nil {
			logger.Error("command_alarm_relay_configuration_invalid", "error_code", "COMMAND_ALARM_RELAY_CONFIGURATION_INVALID")
			return 1
		}
		store.SetControlAlarmOutboxWakeup(alarmRelay.Notify)
	}
	authority, err = commandservice.NewControlAuthority(commandservice.ControlAuthorityConfig{
		Store:                  store,
		Runtime:                store,
		Twin:                   adapters,
		Live:                   adapters,
		IAM:                    adapters,
		CommandGrantPublicKey:  commandGrantPublicKey,
		CommandGrantIssuer:     grantIssuer,
		CommandGrantPresenter:  commandServiceSPIFFE,
		CommandGrantAudience:   grantAudience,
		CommandGrantUseChecker: useChecker,
	})
	if err != nil {
		logger.Error("command_authority_configuration_invalid", "error_code", "COMMAND_AUTHORITY_CONFIGURATION_INVALID")
		return 1
	}

	commandHandler, err := commandservice.NewHTTPHandler(commandservice.HTTPConfig{
		Authority:                  store,
		CommandGrantPublicKey:      commandGrantPublicKey,
		CommandGrantIssuer:         grantIssuer,
		GatewaySPIFFE:              gatewaySPIFFE,
		CommandGrantAudience:       grantAudience,
		GatewayDelegationPublicKey: gatewayDelegationPublicKey,
		GatewayReadAudience:        gatewayReadAudience,
		CommandGrantUseChecker:     useChecker,
	})
	if err != nil {
		logger.Error("command_http_configuration_invalid", "error_code", "COMMAND_HTTP_CONFIGURATION_INVALID")
		return 1
	}
	operatorHandler, err := commandservice.NewOperatorHTTPHandler(commandservice.OperatorHTTPConfig{
		Authority:                  authority,
		GatewayDelegationPublicKey: gatewayDelegationPublicKey,
		GatewaySPIFFE:              gatewaySPIFFE,
		GatewayAudience:            gatewayReadAudience,
	})
	if err != nil {
		logger.Error("command_operator_http_configuration_invalid", "error_code", "COMMAND_OPERATOR_HTTP_CONFIGURATION_INVALID")
		return 1
	}
	runtimeHandler, err := commandservice.NewRuntimeHTTPHandler(runtimeHTTPConfig(authority, telemetry.Metrics, nil))
	if err != nil {
		logger.Error("command_runtime_configuration_invalid", "error_code", "COMMAND_RUNTIME_CONFIGURATION_INVALID")
		return 1
	}
	if closer, ok := runtimeHandler.(interface{ Close() }); ok {
		runtimeCloser = closer
	}

	router := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == commandservice.InternalOperatorPrefix || strings.HasPrefix(request.URL.Path, commandservice.InternalOperatorPrefix+"/") {
			if peerSPIFFE(request) != gatewaySPIFFE {
				writeProblem(writer, http.StatusForbidden, "COMMAND_GATEWAY_WORKLOAD_FORBIDDEN")
				return
			}
			operatorHandler.ServeHTTP(writer, request)
			return
		}
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
	instrumentedRouter := observability.InstrumentHTTP(router, telemetry, observability.HTTPInstrumentationConfig{
		Namespace: "hvac_command", Service: "command-service", SpanName: "http.command.request", Route: commandservice.ObservabilityRoute,
	})
	server := &http.Server{
		Addr:              envOr("COMMAND_SERVICE_ADDR", ":8447"),
		Handler:           instrumentedRouter,
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
	diagnosticsDone := make(chan struct{})
	go func() {
		defer close(diagnosticsDone)
		defer observability.Recover(logger, telemetry.Metrics, "command_owner.diagnostics")
		if err := diagnostics.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("command_diagnostics_failed", "error_code", "DIAGNOSTICS_SERVE_FAILED")
		}
	}()

	relayCtx, cancelRelay := context.WithCancel(context.Background())
	defer cancelRelay()
	var relayWg sync.WaitGroup
	if alarmRelay != nil {
		relayWg.Add(1)
		go func() {
			defer relayWg.Done()
			defer observability.Recover(logger, telemetry.Metrics, "command_owner.alarm_relay")
			alarmRelay.Run(relayCtx)
		}()
	}

	signalCtx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		defer observability.Recover(logger, telemetry.Metrics, "command_owner.shutdown")
		<-signalCtx.Done()
		telemetry.MarkNotReady()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		cancelRelay()
		relayWg.Wait()
		_ = diagnostics.Shutdown(ctx)
		select {
		case <-diagnosticsDone:
		case <-ctx.Done():
		}
		_ = shutdownTelemetry(ctx)
	}()

	telemetry.MarkReady()
	logger.Info("command_service_started", "address", server.Addr, "policy_revision", policyRevision)
	exitCode := 0
	if err := server.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("command_service_stopped_unexpectedly", "error_code", "COMMAND_SERVE_FAILED")
		exitCode = 1
	}
	stopSignals()
	<-shutdownDone
	if exitCode == 0 {
		logger.Info("command_service_stopped")
	}
	return exitCode
}

// runtimeHTTPConfig names the dispatcher, verifier, and core registry callers; each serves every Tenant.
func runtimeHTTPConfig(store commandservice.RuntimeStore, metrics *observability.Registry, failureNotifier commandservice.ControlFailureNotifier) commandservice.RuntimeHTTPConfig {
	return commandservice.RuntimeHTTPConfig{
		Store:                  store,
		Metrics:                metrics,
		DispatcherSPIFFE:       envOr("COMMAND_DISPATCHER_SPIFFE", "spiffe://hvac.local/command-dispatcher"),
		VerifierSPIFFE:         envOr("COMMAND_VERIFIER_SPIFFE", "spiffe://hvac.local/command-verifier"),
		CoreRegistrySPIFFEs:    parseSPIFFEAllowlist(envOr("COMMAND_CORE_REGISTRY_SPIFFES", "spiffe://hvac.local/platform-core-service,spiffe://hvac.local/platform-gateway")),
		ControlFailureNotifier: failureNotifier,
	}
}

func parseSPIFFEAllowlist(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

type commandControlEventPayload struct {
	SchemaVersion int     `json:"schemaVersion"`
	TenantID      string  `json:"tenantId"`
	SiteID        string  `json:"siteId"`
	DeviceID      *string `json:"deviceId,omitempty"`
	PointID       *string `json:"pointId,omitempty"`
	CommandID     string  `json:"commandId"`
	EventKind     string  `json:"eventKind"`
	Capability    string  `json:"capability,omitempty"`
	ReasonCode    string  `json:"reasonCode,omitempty"`
	Detail        string  `json:"detail,omitempty"`
	OccurredAt    string  `json:"occurredAt,omitempty"`
	ActorType     string  `json:"actorType,omitempty"`
	ActorID       string  `json:"actorId,omitempty"`
	CorrelationID string  `json:"correlationId,omitempty"`
}

func resolveCommandControlEventsEndpoint() string {
	const controlEventsPath = "/internal/v1/alarms/control-events"
	if explicit := strings.TrimSpace(os.Getenv("ALARM_CONTROL_EVENTS_URL")); explicit != "" {
		parsed, err := url.Parse(explicit)
		if err == nil && parsed.Scheme != "" && parsed.Host != "" && (parsed.Path == "" || parsed.Path == "/") {
			parsed.Path = controlEventsPath
			return strings.TrimRight(parsed.String(), "/")
		}
		return explicit
	}
	if base := strings.TrimRight(strings.TrimSpace(os.Getenv("ALARM_SERVICE_URL")), "/"); base != "" {
		return base + controlEventsPath
	}
	return ""
}

func buildCommandControlFailureSender(logger *slog.Logger) commandservice.ControlAlarmSender {
	endpoint := resolveCommandControlEventsEndpoint()
	if endpoint == "" {
		return nil
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		if logger != nil {
			logger.Warn("command_control_alarm_notifier_disabled", "error_code", "ALARM_CONTROL_EVENTS_URL_INVALID")
		}
		return nil
	}
	serverCA := strings.TrimSpace(os.Getenv("ALARM_SERVICE_SERVER_CA"))
	if serverCA == "" {
		if logger != nil {
			logger.Warn("command_control_alarm_notifier_disabled", "error_code", "ALARM_SERVICE_SERVER_CA_MISSING")
		}
		return nil
	}
	var clientCerts *workloadtls.CertificateFiles
	certPath := strings.TrimSpace(os.Getenv("COMMAND_TLS_CERT"))
	keyPath := strings.TrimSpace(os.Getenv("COMMAND_TLS_KEY"))
	if certPath != "" && keyPath != "" {
		clientCerts = &workloadtls.CertificateFiles{
			CertificatePath: certPath,
			PrivateKeyPath:  keyPath,
		}
	}
	client, err := workloadtls.NewHTTPClient(workloadtls.ClientConfig{
		CertificateFiles: clientCerts,
		ServerCAPath:     serverCA,
		ServerName:       envOr("ALARM_SERVICE_SERVER_NAME", "localhost"),
		Timeout:          3 * time.Second,
	})
	if err != nil {
		if logger != nil {
			logger.Warn("command_control_alarm_notifier_disabled", "error_code", "ALARM_CONTROL_EVENTS_TLS_INVALID")
		}
		return nil
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return newHTTPControlFailureSender(endpoint, client, logger)
}

func newHTTPControlFailureSender(endpoint string, client *http.Client, logger *slog.Logger) commandservice.ControlAlarmSender {
	if strings.TrimSpace(endpoint) == "" || client == nil {
		return nil
	}
	return func(ctx context.Context, event commandservice.ControlFailureEvent) error {
		actorType := strings.TrimSpace(event.ActorType)
		if actorType == "" {
			actorType = "COMMAND_RUNTIME"
		}
		actorID := strings.TrimSpace(event.ActorID)
		if actorID == "" {
			actorID = "command-owner"
		}
		payload := commandControlEventPayload{
			SchemaVersion: 1,
			TenantID:      event.TenantID,
			SiteID:        event.SiteID,
			CommandID:     event.CommandID,
			EventKind:     event.Kind,
			Capability:    string(event.Capability),
			ReasonCode:    event.ReasonCode,
			Detail:        event.Detail,
			OccurredAt:    event.OccurredAt.UTC().Format(time.RFC3339Nano),
			ActorType:     actorType,
			ActorID:       actorID,
			CorrelationID: event.CommandID,
		}
		if event.DeviceID != "" {
			deviceID := event.DeviceID
			payload.DeviceID = &deviceID
		}
		if event.PointID != "" {
			pointID := event.PointID
			payload.PointID = &pointID
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			return &commandservice.ControlAlarmDeliveryError{Code: "CONTROL_ALARM_PAYLOAD_INVALID"}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
		if err != nil {
			return &commandservice.ControlAlarmDeliveryError{Code: "CONTROL_ALARM_REQUEST_INVALID"}
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			if logger != nil {
				logger.Warn("command_control_alarm_failed", "error_code", "CONTROL_ALARM_HTTP_FAILED", "command_id", event.CommandID, "event_kind", event.Kind)
			}
			return &commandservice.ControlAlarmDeliveryError{Code: "CONTROL_ALARM_HTTP_FAILED"}
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		_ = resp.Body.Close()
		if resp.StatusCode >= http.StatusMultipleChoices {
			if logger != nil {
				logger.Warn("command_control_alarm_rejected", "error_code", "CONTROL_ALARM_HTTP_REJECTED", "status", resp.StatusCode, "command_id", event.CommandID, "event_kind", event.Kind)
			}
			return &commandservice.ControlAlarmDeliveryError{Code: "CONTROL_ALARM_HTTP_REJECTED"}
		}
		return nil
	}
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
	value, err := requiredEnv(name)
	if err != nil {
		return 0, err
	}
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
