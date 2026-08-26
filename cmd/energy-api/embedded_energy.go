package main

import (
	"context"
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandauth"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/sessionevent"
	"github.com/quanlaihe/hvac-web/libs/sessionstore"
	"github.com/quanlaihe/hvac-web/modules/alarm/pkg/alarmservice"
	"github.com/quanlaihe/hvac-web/modules/audit/pkg/auditserver"
	"github.com/quanlaihe/hvac-web/modules/command/pkg/commandservice"
	"github.com/quanlaihe/hvac-web/modules/iam/pkg/iamserver"
	"github.com/quanlaihe/hvac-web/modules/registry/pkg/coreservice"
	"github.com/quanlaihe/hvac-web/modules/telemetry/pkg/queryservice"
	"github.com/quanlaihe/hvac-web/modules/workorder/pkg/workorderservice"
)

type embeddedEnergyServices struct {
	servers    []*http.Server
	telemetry  []*observability.Runtime
	closeFuncs []func()
}

var embeddedOwnerNames = []string{"iam", "audit", "core", "telemetry-query", "alarm", "notification", "work-order", "command"}

func parseEmbeddedOwners(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("ENERGY_API_EMBEDDED_OWNERS is required")
	}
	if raw == "all" {
		return append([]string(nil), embeddedOwnerNames...), nil
	}
	if raw == "none" {
		return nil, nil
	}
	requested := make(map[string]struct{})
	for _, owner := range strings.Split(raw, ",") {
		owner = strings.TrimSpace(owner)
		if _, duplicate := requested[owner]; duplicate {
			return nil, fmt.Errorf("embedded owner %q is duplicated", owner)
		}
		requested[owner] = struct{}{}
	}
	owners := make([]string, 0, len(requested))
	for _, owner := range embeddedOwnerNames {
		if _, selected := requested[owner]; selected {
			owners = append(owners, owner)
			delete(requested, owner)
		}
	}
	if len(requested) > 0 {
		for owner := range requested {
			return nil, fmt.Errorf("embedded owner %q is unknown", owner)
		}
	}
	return owners, nil
}

func startEmbeddedEnergyServices(ctx context.Context, logger *slog.Logger) (*embeddedEnergyServices, error) {
	owners, err := parseEmbeddedOwners(os.Getenv("ENERGY_API_EMBEDDED_OWNERS"))
	if err != nil {
		return nil, err
	}
	services := &embeddedEnergyServices{}
	fail := func(err error) (*embeddedEnergyServices, error) {
		services.Close()
		return nil, err
	}

	for _, owner := range owners {
		switch owner {
		case "iam":
			server, telemetry, closeOwner, configureErr := newEmbeddedIAMServer(ctx, logger)
			if configureErr != nil {
				return fail(fmt.Errorf("configure embedded IAM: %w", configureErr))
			}
			services.servers = append(services.servers, server)
			services.telemetry = append(services.telemetry, telemetry)
			services.closeFuncs = append(services.closeFuncs, closeOwner)
		case "audit":
			server, telemetry, closeOwner, configureErr := newEmbeddedAuditServer(ctx, logger)
			if configureErr != nil {
				return fail(fmt.Errorf("configure embedded Audit: %w", configureErr))
			}
			services.servers = append(services.servers, server)
			services.telemetry = append(services.telemetry, telemetry)
			services.closeFuncs = append(services.closeFuncs, closeOwner)
		case "core":
			server, telemetry, closeOwner, configureErr := newEmbeddedCoreServer(ctx, logger)
			if configureErr != nil {
				return fail(fmt.Errorf("configure embedded Core: %w", configureErr))
			}
			services.servers = append(services.servers, server)
			services.telemetry = append(services.telemetry, telemetry)
			services.closeFuncs = append(services.closeFuncs, closeOwner)
		case "telemetry-query":
			server, telemetry, configureErr := newEmbeddedQueryServer(logger)
			if configureErr != nil {
				return fail(fmt.Errorf("configure embedded Telemetry Query: %w", configureErr))
			}
			services.servers = append(services.servers, server)
			services.telemetry = append(services.telemetry, telemetry)
		case "alarm":
			server, telemetry, closeOwner, configureErr := newEmbeddedAlarmServer(ctx, logger)
			if configureErr != nil {
				return fail(fmt.Errorf("configure embedded Alarm: %w", configureErr))
			}
			services.servers = append(services.servers, server)
			services.telemetry = append(services.telemetry, telemetry)
			services.closeFuncs = append(services.closeFuncs, closeOwner)
		case "notification":
			server, telemetry, closeOwner, configureErr := newEmbeddedNotificationServer(ctx, logger)
			if configureErr != nil {
				return fail(fmt.Errorf("configure embedded Notification: %w", configureErr))
			}
			services.servers = append(services.servers, server)
			services.telemetry = append(services.telemetry, telemetry)
			services.closeFuncs = append(services.closeFuncs, closeOwner)
		case "work-order":
			server, closeOwner, configureErr := newEmbeddedWorkOrderServer(ctx, logger)
			if configureErr != nil {
				return fail(fmt.Errorf("configure embedded Work Order: %w", configureErr))
			}
			services.servers = append(services.servers, server)
			services.closeFuncs = append(services.closeFuncs, closeOwner)
		case "command":
			server, telemetry, closeOwner, configureErr := newEmbeddedCommandServer(ctx, logger)
			if configureErr != nil {
				return fail(fmt.Errorf("configure embedded Command: %w", configureErr))
			}
			services.servers = append(services.servers, server)
			services.telemetry = append(services.telemetry, telemetry)
			services.closeFuncs = append(services.closeFuncs, closeOwner)
		}
	}

	for _, runtime := range services.telemetry {
		runtime.MarkReady()
	}
	for _, server := range services.servers {
		server := server
		go func() {
			if err := server.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logger.Error("energy_api_embedded_service_stopped", "address", server.Addr, "error_code", "ENERGY_API_EMBEDDED_SERVICE_STOPPED")
				os.Exit(1)
			}
		}()
	}
	logger.Info("energy_api_embedded_services_started", "owners", strings.Join(owners, ","), "service_count", len(services.servers))
	return services, nil
}

func (services *embeddedEnergyServices) Shutdown(ctx context.Context) {
	if services == nil {
		return
	}
	for _, runtime := range services.telemetry {
		runtime.MarkNotReady()
	}
	for _, server := range services.servers {
		_ = server.Shutdown(ctx)
	}
	for _, runtime := range services.telemetry {
		_ = runtime.Shutdown(ctx)
	}
}

func (services *embeddedEnergyServices) Close() {
	if services == nil {
		return
	}
	for i := len(services.closeFuncs) - 1; i >= 0; i-- {
		services.closeFuncs[i]()
	}
	services.closeFuncs = nil
}

func newEmbeddedAuditServer(ctx context.Context, logger *slog.Logger) (*http.Server, *observability.Runtime, func(), error) {
	certificate, err := tls.LoadX509KeyPair(energyRequiredEnv("AUDIT_TLS_CERT"), energyRequiredEnv("AUDIT_TLS_KEY"))
	if err != nil {
		return nil, nil, func() {}, err
	}
	clientCAs, err := loadCertPool(energyRequiredEnv("AUDIT_CLIENT_CA"), "Audit client CA")
	if err != nil {
		return nil, nil, func() {}, err
	}
	openContext, cancelOpen := context.WithTimeout(ctx, 10*time.Second)
	store, err := auditserver.OpenStore(openContext, energyRequiredEnv("AUDIT_CONSUMER_DATABASE_URL"), energyRequiredEnv("AUDIT_QUERY_DATABASE_URL"))
	cancelOpen()
	if err != nil {
		return nil, nil, func() {}, err
	}
	openContext, cancelOpen = context.WithTimeout(ctx, 5*time.Second)
	outbox, err := sessionstore.OpenOutbox(openContext, energyRequiredEnv("AUDIT_OUTBOX_DATABASE_URL"))
	cancelOpen()
	if err != nil {
		store.Close()
		return nil, nil, func() {}, err
	}
	telemetry := observability.NewRuntime(observability.RuntimeConfig{
		Service: "energy-api-audit", OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), QueueSize: 1024, ExportTimeout: 500 * time.Millisecond,
	})
	handler := auditserver.NewHandler(auditserver.Config{
		Store: store, OperationsWriter: store,
		AllowedWorkloadSPIFFE:           envOr("AUDIT_ALLOWED_WORKLOAD_SPIFFE", "spiffe://hvac.local/platform-gateway"),
		AllowedOperationsProducerSPIFFE: envOr("AUDIT_OPERATIONS_PRODUCER_SPIFFE", "spiffe://hvac.local/operations-agent-service"),
		Audience:                        envOr("AUDIT_AUDIENCE", "audit-ledger-service"),
		Logger:                          logger, Observability: telemetry,
	})
	server := &http.Server{
		Addr: envOr("AUDIT_SERVICE_ADDR", ":8446"), Handler: handler,
		TLSConfig: &tls.Config{
			MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate}, ClientCAs: clientCAs, ClientAuth: tls.RequireAndVerifyClientCert,
		},
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second,
	}
	go runEmbeddedAuditOutboxRelay(ctx, outbox, store, telemetry, logger)
	closeAll := func() {
		outbox.Close()
		store.Close()
	}
	logger.Info("energy_api_audit_configured", "address", server.Addr, "transport", "postgres-outbox")
	return server, telemetry, closeAll, nil
}

func runEmbeddedAuditOutboxRelay(ctx context.Context, outbox *sessionstore.OutboxStore, store *auditserver.Store, telemetry *observability.Runtime, logger *slog.Logger) {
	owner := envOr("AUDIT_OUTBOX_RELAY_OWNER", "energy-api-audit")
	for ctx.Err() == nil {
		now := time.Now().UTC()
		record, err := outbox.ClaimPending(ctx, owner, now, 30*time.Second)
		if errors.Is(err, sessionstore.ErrNoPendingOutbox) {
			embeddedSleepContext(ctx, 100*time.Millisecond)
			continue
		}
		if err != nil {
			logger.Warn("audit_outbox_claim_failed", "error_code", "AUDIT_OUTBOX_CLAIM_FAILED")
			_ = telemetry.Metrics.AddCounter("s0_audit_outbox_relay_total", "PostgreSQL Audit Outbox relay attempts.", map[string]string{"result": "claim_error"}, 1)
			embeddedSleepContext(ctx, time.Second)
			continue
		}
		if record.Topic != sessionevent.ControlTopic {
			_ = outbox.MarkFailed(ctx, record.MessageID, owner, "AUDIT_OUTBOX_TOPIC_INVALID", now.Add(5*time.Minute))
			logger.Error("audit_outbox_topic_invalid", "message_id", record.MessageID, "topic", record.Topic, "error_code", "AUDIT_OUTBOX_TOPIC_INVALID")
			continue
		}
		inserted, consumeErr := store.Consume(ctx, record.Payload, auditserver.MessageMetadata{
			Topic: record.Topic, Partition: 0, Offset: 0, ReceivedAt: time.Now().UTC(),
		})
		if consumeErr != nil {
			_ = outbox.MarkFailed(ctx, record.MessageID, owner, "AUDIT_LEDGER_WRITE_FAILED", now.Add(time.Second))
			_ = telemetry.Metrics.AddCounter("s0_audit_outbox_relay_total", "PostgreSQL Audit Outbox relay attempts.", map[string]string{"result": "write_error"}, 1)
			logger.Warn("audit_outbox_write_failed", "message_id", record.MessageID, "error_code", "AUDIT_LEDGER_WRITE_FAILED")
			continue
		}
		if err := outbox.MarkPublished(ctx, record.MessageID, owner, time.Now().UTC()); err != nil {
			_ = telemetry.Metrics.AddCounter("s0_audit_outbox_relay_total", "PostgreSQL Audit Outbox relay attempts.", map[string]string{"result": "commit_error"}, 1)
			logger.Warn("audit_outbox_commit_failed", "message_id", record.MessageID, "error_code", "AUDIT_OUTBOX_COMMIT_FAILED")
			continue
		}
		_ = telemetry.Metrics.AddCounter("s0_audit_outbox_relay_total", "PostgreSQL Audit Outbox relay attempts.", map[string]string{"result": "ok"}, 1)
		logger.Info("audit_outbox_delivered", "message_id", record.MessageID, "inserted", inserted)
	}
}

func embeddedSleepContext(ctx context.Context, duration time.Duration) {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

func newEmbeddedIAMServer(ctx context.Context, logger *slog.Logger) (*http.Server, *observability.Runtime, func(), error) {
	certificate, err := tls.LoadX509KeyPair(energyRequiredEnv("IAM_TLS_CERT"), energyRequiredEnv("IAM_TLS_KEY"))
	if err != nil {
		return nil, nil, func() {}, err
	}
	if len(certificate.Certificate) == 0 {
		return nil, nil, func() {}, errors.New("IAM certificate chain is empty")
	}
	leaf, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		return nil, nil, func() {}, err
	}
	iamSPIFFEID, err := embeddedCertificateSPIFFEID(leaf)
	if err != nil {
		return nil, nil, func() {}, err
	}
	signer, ok := certificate.PrivateKey.(crypto.Signer)
	if !ok {
		return nil, nil, func() {}, errors.New("IAM workload key cannot sign grants")
	}
	clientCAs, err := loadCertPool(energyRequiredEnv("IAM_CLIENT_CA"), "IAM client CA")
	if err != nil {
		return nil, nil, func() {}, err
	}

	policyRevision := envOr("IAM_POLICY_REVISION", "policy-unconfigured")
	databaseURL := energyRequiredEnv("IAM_DATABASE_URL")
	openContext, cancelOpen := context.WithTimeout(ctx, 5*time.Second)
	store, err := iamserver.OpenPostgresAuthorizationStore(openContext, databaseURL)
	cancelOpen()
	if err != nil {
		return nil, nil, func() {}, err
	}
	closeFuncs := []func(){store.Close}
	policyRevision = "database-managed"

	var adminStore *iamserver.PostgresAdminStore
	if adminDatabaseURL := strings.TrimSpace(os.Getenv("IAM_ADMIN_DATABASE_URL")); adminDatabaseURL != "" {
		pepper, readErr := os.ReadFile(energyRequiredEnv("IAM_API_CREDENTIAL_PEPPER_FILE"))
		if readErr != nil {
			store.Close()
			return nil, nil, func() {}, readErr
		}
		openContext, cancelOpen = context.WithTimeout(ctx, 5*time.Second)
		postgresAdminStore, openErr := iamserver.OpenPostgresAdminStore(openContext, adminDatabaseURL, pepper)
		cancelOpen()
		if openErr != nil {
			store.Close()
			return nil, nil, func() {}, openErr
		}
		adminStore = postgresAdminStore
		closeFuncs = append(closeFuncs, postgresAdminStore.Close)
	}

	var telemetryGrantStore iamserver.TelemetryGrantStore
	if grantDatabaseURL := strings.TrimSpace(os.Getenv("IAM_TELEMETRY_GRANT_DATABASE_URL")); grantDatabaseURL != "" {
		openContext, cancelOpen = context.WithTimeout(ctx, 5*time.Second)
		postgresGrantStore, openErr := iamserver.OpenPostgresTelemetryGrantStore(openContext, grantDatabaseURL)
		cancelOpen()
		if openErr != nil {
			store.Close()
			return nil, nil, func() {}, openErr
		}
		telemetryGrantStore = postgresGrantStore
		closeFuncs = append(closeFuncs, postgresGrantStore.Close)
	}

	telemetry := observability.NewRuntime(observability.RuntimeConfig{
		Service: "energy-api-iam", OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), QueueSize: 1024, ExportTimeout: 500 * time.Millisecond,
	})
	config := iamserver.Config{
		AllowedWorkloadSPIFFE: envOr("IAM_ALLOWED_WORKLOAD_SPIFFE", "spiffe://hvac.local/platform-gateway"),
		CoreWorkloadSPIFFE:    envOr("IAM_CORE_WORKLOAD_SPIFFE", "spiffe://hvac.local/platform-core-service"),
		Audience:              envOr("IAM_AUDIENCE", "iam-service"),
		Logger:                logger,
		Observability:         telemetry,
		AuthorizationStore:    store,
		AdminStore:            adminStore,
		RegistryGrantSigner:   signer,
		RegistryGrantIssuer:   iamSPIFFEID,
		RegistryGrantAudience: envOr("IAM_REGISTRY_GRANT_AUDIENCE", "platform-core-service"),
		AllowedRegistryGrantPresenters: []string{
			envOr("IAM_OPERATIONS_AGENT_SPIFFE", "spiffe://hvac.local/operations-agent-service"),
		},
		RegistryGrantStatus:         store,
		TelemetryAuthorizationStore: store,
		AlarmAuthorizationStore:     store,
		AlarmAuditSink:              store,
		WorkOrderAuthorizationStore: store,
		WorkOrderAuditSink:          store,
		TelemetryGrantSigner:        signer,
		TelemetryGrantIssuer:        iamSPIFFEID,
		TelemetryGrantAudience:      envOr("IAM_TELEMETRY_GRANT_AUDIENCE", "telemetry-runtime-service"),
		TelemetryRuntimeSPIFFE:      envOr("IAM_TELEMETRY_RUNTIME_SPIFFE", "spiffe://hvac.local/telemetry-runtime-service"),
		TelemetryGrantStore:         telemetryGrantStore,
		CommandGrantSigner:          signer,
		CommandGrantIssuer:          iamSPIFFEID,
		CommandGrantAudience:        envOr("IAM_COMMAND_GRANT_AUDIENCE", "command-service"),
	}
	server := &http.Server{
		Addr:    envOr("IAM_SERVICE_ADDR", "127.0.0.1:8444"),
		Handler: iamserver.NewHandler(config),
		TLSConfig: &tls.Config{
			MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate}, ClientCAs: clientCAs, ClientAuth: tls.RequireAndVerifyClientCert,
		},
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second,
	}
	closeAll := func() {
		for i := len(closeFuncs) - 1; i >= 0; i-- {
			closeFuncs[i]()
		}
	}
	logger.Info("energy_api_iam_configured", "address", server.Addr, "policy_revision", policyRevision)
	return server, telemetry, closeAll, nil
}

func newEmbeddedCoreServer(ctx context.Context, logger *slog.Logger) (*http.Server, *observability.Runtime, func(), error) {
	certificate, err := tls.LoadX509KeyPair(energyRequiredEnv("CORE_TLS_CERT"), energyRequiredEnv("CORE_TLS_KEY"))
	if err != nil {
		return nil, nil, func() {}, err
	}
	clientCAs, err := loadCertPool(energyRequiredEnv("CORE_CLIENT_CA"), "Core client CA")
	if err != nil {
		return nil, nil, func() {}, err
	}
	iamRoots, err := loadCertPool(energyRequiredEnv("CORE_IAM_CA"), "Core IAM CA")
	if err != nil {
		return nil, nil, func() {}, err
	}
	grantPublicKey, err := embeddedCertificatePublicKey(energyRequiredEnv("CORE_IAM_GRANT_CERT"))
	if err != nil {
		return nil, nil, func() {}, err
	}
	cursorKey, err := base64.RawURLEncoding.DecodeString(energyRequiredEnv("CORE_CURSOR_HMAC_KEY"))
	if err != nil {
		return nil, nil, func() {}, err
	}
	cursorCodec, err := coreservice.NewCursorCodec(cursorKey)
	if err != nil {
		return nil, nil, func() {}, err
	}
	openContext, cancelOpen := context.WithTimeout(ctx, 5*time.Second)
	store, err := coreservice.OpenPostgresStore(openContext, energyRequiredEnv("CORE_DATABASE_URL"))
	cancelOpen()
	if err != nil {
		return nil, nil, func() {}, err
	}
	iamClient := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS13, RootCAs: iamRoots, Certificates: []tls.Certificate{certificate}, ServerName: envOr("CORE_IAM_SERVER_NAME", "iam-service"),
		}},
	}
	grantStatus, err := coreservice.NewHTTPGrantStatusProvider(energyRequiredEnv("CORE_IAM_ENDPOINT"), iamClient)
	if err != nil {
		store.Close()
		return nil, nil, func() {}, err
	}
	telemetry := observability.NewRuntime(observability.RuntimeConfig{
		Service: "energy-api-core", OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), QueueSize: 1024, ExportTimeout: 500 * time.Millisecond,
	})
	server := &http.Server{
		Addr: envOr("CORE_SERVICE_ADDR", "127.0.0.1:18445"),
		Handler: coreservice.NewHandler(coreservice.ServerConfig{
			Store: store, CursorCodec: cursorCodec, GrantPublicKey: grantPublicKey,
			GrantIssuer:            envOr("CORE_GRANT_ISSUER", "spiffe://hvac.local/iam-service"),
			AllowedPresenterSPIFFE: envOr("CORE_ALLOWED_WORKLOAD_SPIFFE", "spiffe://hvac.local/platform-gateway"),
			AdditionalAllowedPresenterSPIFFEs: []string{
				envOr("CORE_OPERATIONS_AGENT_SPIFFE", "spiffe://hvac.local/operations-agent-service"),
			},
			Audience: envOr("CORE_AUDIENCE", "platform-core-service"), GrantStatus: grantStatus, Logger: logger, Observability: telemetry,
		}),
		TLSConfig: &tls.Config{
			MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate}, ClientCAs: clientCAs, ClientAuth: tls.RequireAndVerifyClientCert,
		},
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second,
	}
	logger.Info("energy_api_core_configured", "address", server.Addr)
	return server, telemetry, store.Close, nil
}

type embeddedRuntimeCohortDocument struct {
	SchemaVersion int                            `json:"schemaVersion"`
	Cohorts       []commandservice.RuntimeCohort `json:"cohorts"`
}

func newEmbeddedCommandServer(ctx context.Context, logger *slog.Logger) (*http.Server, *observability.Runtime, func(), error) {
	certificate, err := tls.LoadX509KeyPair(energyRequiredEnv("COMMAND_TLS_CERT"), energyRequiredEnv("COMMAND_TLS_KEY"))
	if err != nil {
		return nil, nil, func() {}, err
	}
	clientCAs, err := loadCertPool(energyRequiredEnv("COMMAND_CLIENT_CA"), "Command client CA")
	if err != nil {
		return nil, nil, func() {}, err
	}
	commandGrantPublicKey, err := embeddedCertificatePublicKey(energyRequiredEnv("COMMAND_IAM_GRANT_CERT"))
	if err != nil {
		return nil, nil, func() {}, err
	}
	gatewayDelegationPublicKey, err := embeddedCertificatePublicKey(energyRequiredEnv("COMMAND_GATEWAY_DELEGATION_CERT"))
	if err != nil {
		return nil, nil, func() {}, err
	}
	databaseURL, err := embeddedLoadValueFile(energyRequiredEnv("COMMAND_DATABASE_URL_FILE"), 64<<10)
	if err != nil {
		return nil, nil, func() {}, err
	}
	openContext, cancelOpen := context.WithTimeout(ctx, 10*time.Second)
	store, err := commandservice.OpenPostgresStore(openContext, databaseURL)
	cancelOpen()
	if err != nil {
		return nil, nil, func() {}, err
	}
	policyRevision := energyRequiredEnv("COMMAND_POLICY_REVISION")
	if policyRevision == "" {
		store.Close()
		return nil, nil, func() {}, errors.New("Command policy revision is required")
	}
	revocationRevision, err := strconv.ParseUint(energyRequiredEnv("COMMAND_EMERGENCY_REVOCATION_REVISION"), 10, 64)
	if err != nil {
		store.Close()
		return nil, nil, func() {}, errors.New("Command emergency revocation revision is invalid")
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
			checkContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			return store.ConsumeCommandGrant(checkContext, claims, policyRevision, revocationRevision)
		},
	})
	if err != nil {
		store.Close()
		return nil, nil, func() {}, err
	}
	telemetry := observability.NewRuntime(observability.RuntimeConfig{
		Service: "energy-api-command", OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), QueueSize: 2048, ExportTimeout: 500 * time.Millisecond,
	})
	runtimeConfig, err := embeddedCommandRuntimeConfig(store, telemetry.Metrics)
	if err != nil {
		store.Close()
		return nil, nil, func() {}, err
	}
	runtimeHandler, err := commandservice.NewRuntimeHTTPHandler(runtimeConfig)
	if err != nil {
		store.Close()
		return nil, nil, func() {}, err
	}
	router := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == commandservice.InternalCommandsPath || strings.HasPrefix(request.URL.Path, commandservice.InternalCommandsPath+"/") {
			if embeddedPeerSPIFFE(request) != gatewaySPIFFE {
				embeddedWriteProblem(writer, http.StatusForbidden, "COMMAND_GATEWAY_WORKLOAD_FORBIDDEN")
				return
			}
			commandHandler.ServeHTTP(writer, request)
			return
		}
		runtimeHandler.ServeHTTP(writer, request)
	})
	server := &http.Server{
		Addr: envOr("COMMAND_SERVICE_ADDR", ":8447"), Handler: router,
		TLSConfig: &tls.Config{
			MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate}, ClientCAs: clientCAs, ClientAuth: tls.RequireAndVerifyClientCert,
		},
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second,
	}
	logger.Info("energy_api_command_configured", "address", server.Addr, "policy_revision", policyRevision)
	return server, telemetry, store.Close, nil
}

func embeddedCommandRuntimeConfig(store commandservice.RuntimeStore, metrics *observability.Registry) (commandservice.RuntimeHTTPConfig, error) {
	path := strings.TrimSpace(os.Getenv("COMMAND_RUNTIME_COHORTS_FILE"))
	if path == "" {
		return commandservice.RuntimeHTTPConfig{
			Store: store, Metrics: metrics,
			DispatcherSPIFFE: envOr("COMMAND_DISPATCHER_SPIFFE", "spiffe://hvac.local/command-dispatcher"),
			VerifierSPIFFE:   envOr("COMMAND_VERIFIER_SPIFFE", "spiffe://hvac.local/command-verifier"),
			TenantID:         energyRequiredEnv("COMMAND_APPROVED_TENANT_ID"),
			SiteID:           energyRequiredEnv("COMMAND_APPROVED_SITE_ID"),
			DeviceID:         energyRequiredEnv("COMMAND_APPROVED_DEVICE_ID"),
			Capability:       commandmodel.Capability(energyRequiredEnv("COMMAND_APPROVED_CAPABILITY")),
		}, nil
	}
	if !filepath.IsAbs(path) {
		return commandservice.RuntimeHTTPConfig{}, errors.New("Command runtime cohort path must be absolute")
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() <= 0 || info.Size() > 64<<10 {
		return commandservice.RuntimeHTTPConfig{}, errors.New("Command runtime cohort file size is invalid")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return commandservice.RuntimeHTTPConfig{}, err
	}
	var document embeddedRuntimeCohortDocument
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return commandservice.RuntimeHTTPConfig{}, errors.New("Command runtime cohort document is invalid")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) || document.SchemaVersion != 1 || len(document.Cohorts) == 0 {
		return commandservice.RuntimeHTTPConfig{}, errors.New("Command runtime cohort document is invalid")
	}
	return commandservice.RuntimeHTTPConfig{Store: store, Metrics: metrics, Cohorts: document.Cohorts}, nil
}

func newEmbeddedAlarmServer(ctx context.Context, logger *slog.Logger) (*http.Server, *observability.Runtime, func(), error) {
	certificate, err := tls.LoadX509KeyPair(energyRequiredEnv("ALARM_TLS_CERT"), energyRequiredEnv("ALARM_TLS_KEY"))
	if err != nil {
		return nil, nil, func() {}, err
	}
	clientCAs, err := loadCertPool(energyRequiredEnv("ALARM_CLIENT_CA"), "Alarm client CA")
	if err != nil {
		return nil, nil, func() {}, err
	}
	gatewayPublicKey, err := embeddedCertificatePublicKey(energyRequiredEnv("ALARM_GATEWAY_DELEGATION_CERT"))
	if err != nil {
		return nil, nil, func() {}, err
	}
	databaseURL, err := embeddedLoadValueFile(energyRequiredEnv("ALARM_DATABASE_URL_FILE"), 64<<10)
	if err != nil {
		return nil, nil, func() {}, err
	}
	openContext, cancelOpen := context.WithTimeout(ctx, 10*time.Second)
	store, err := alarmservice.OpenPostgresStore(openContext, databaseURL)
	cancelOpen()
	if err != nil {
		return nil, nil, func() {}, err
	}
	gatewaySPIFFE := envOr("ALARM_GATEWAY_SPIFFE", alarmservice.DefaultGatewaySPIFFEID)
	handler, err := alarmservice.NewHTTPHandler(alarmservice.HTTPConfig{
		Store: store, GatewayPublicKey: gatewayPublicKey, GatewaySPIFFEID: gatewaySPIFFE,
		Audience: envOr("ALARM_READ_AUDIENCE", alarmservice.DefaultAudience), MaxListLimit: 200,
	})
	if err != nil {
		store.Close()
		return nil, nil, func() {}, err
	}
	telemetry := observability.NewRuntime(observability.RuntimeConfig{
		Service: "energy-api-alarm", OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), QueueSize: 1024, ExportTimeout: 500 * time.Millisecond,
	})
	router := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if embeddedPeerSPIFFE(request) != gatewaySPIFFE {
			embeddedWriteProblem(writer, http.StatusForbidden, "ALARM_GATEWAY_WORKLOAD_FORBIDDEN")
			return
		}
		handler.ServeHTTP(writer, request)
	})
	server := &http.Server{
		Addr: envOr("ALARM_SERVICE_ADDR", ":8448"), Handler: router,
		TLSConfig: &tls.Config{
			MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate}, ClientCAs: clientCAs, ClientAuth: tls.RequireAndVerifyClientCert,
		},
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second,
	}
	logger.Info("energy_api_alarm_configured", "address", server.Addr)
	return server, telemetry, store.Close, nil
}

func newEmbeddedWorkOrderServer(ctx context.Context, logger *slog.Logger) (*http.Server, func(), error) {
	certificate, err := tls.LoadX509KeyPair(energyRequiredEnv("WORK_ORDER_TLS_CERT"), energyRequiredEnv("WORK_ORDER_TLS_KEY"))
	if err != nil {
		return nil, func() {}, err
	}
	clientCAs, err := loadCertPool(energyRequiredEnv("WORK_ORDER_CLIENT_CA"), "Work Order client CA")
	if err != nil {
		return nil, func() {}, err
	}
	gatewayPublicKey, err := embeddedCertificatePublicKey(energyRequiredEnv("WORK_ORDER_GATEWAY_DELEGATION_CERT"))
	if err != nil {
		return nil, func() {}, err
	}
	databaseURL, err := embeddedLoadValueFile(energyRequiredEnv("WORK_ORDER_DATABASE_URL_FILE"), 64<<10)
	if err != nil {
		return nil, func() {}, err
	}
	mutationDatabaseURL, err := embeddedLoadValueFile(energyRequiredEnv("WORK_ORDER_MUTATION_DATABASE_URL_FILE"), 64<<10)
	if err != nil {
		return nil, func() {}, err
	}
	cursorMaterial, err := embeddedLoadValueFile(energyRequiredEnv("WORK_ORDER_CURSOR_SECRET_FILE"), 4<<10)
	if err != nil || len(cursorMaterial) < 32 {
		return nil, func() {}, errors.New("Work Order cursor configuration is invalid")
	}
	openContext, cancelOpen := context.WithTimeout(ctx, 10*time.Second)
	store, err := workorderservice.OpenPostgresStoreWithMutations(openContext, databaseURL, mutationDatabaseURL, []byte(cursorMaterial))
	cancelOpen()
	if err != nil {
		return nil, func() {}, err
	}
	gatewaySPIFFE := envOr("WORK_ORDER_GATEWAY_SPIFFE", workorderservice.DefaultGatewaySPIFFEID)
	handler, err := workorderservice.NewHTTPHandler(workorderservice.HTTPConfig{
		Store: store, GatewayPublicKey: gatewayPublicKey, GatewaySPIFFEID: gatewaySPIFFE,
		Audience: envOr("WORK_ORDER_READ_AUDIENCE", workorderservice.DefaultAudience), MaxListLimit: 100,
	})
	if err != nil {
		store.Close()
		return nil, func() {}, err
	}
	router := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if embeddedPeerSPIFFE(request) != gatewaySPIFFE {
			embeddedWriteProblem(writer, http.StatusForbidden, "WORK_ORDER_GATEWAY_WORKLOAD_FORBIDDEN")
			return
		}
		handler.ServeHTTP(writer, request)
	})
	server := &http.Server{
		Addr: envOr("WORK_ORDER_SERVICE_ADDR", ":8449"), Handler: router,
		TLSConfig: &tls.Config{
			MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate}, ClientCAs: clientCAs, ClientAuth: tls.RequireAndVerifyClientCert,
		},
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second,
	}
	logger.Info("energy_api_work_order_configured", "address", server.Addr)
	return server, store.Close, nil
}

func newEmbeddedQueryServer(logger *slog.Logger) (*http.Server, *observability.Runtime, error) {
	certificate, err := tls.LoadX509KeyPair(energyRequiredEnv("QUERY_TLS_CERT"), energyRequiredEnv("QUERY_TLS_KEY"))
	if err != nil {
		return nil, nil, err
	}
	clientCAs, err := loadCertPool(energyRequiredEnv("QUERY_CLIENT_CA"), "Query client CA")
	if err != nil {
		return nil, nil, err
	}
	delegationPublicKey, err := embeddedCertificatePublicKey(energyRequiredEnv("QUERY_GATEWAY_DELEGATION_CERT"))
	if err != nil {
		return nil, nil, err
	}
	accessFactory, err := queryservice.NewCubeAccessFactory([]byte(energyRequiredEnv("QUERY_CUBE_API_SECRET")), time.Now)
	if err != nil {
		return nil, nil, err
	}
	cubeHTTPClient, err := embeddedQueryHTTPClient(strings.TrimSpace(os.Getenv("QUERY_CUBE_CA")))
	if err != nil {
		return nil, nil, err
	}
	cubeClient, err := queryservice.NewCubeClient(queryservice.CubeConfig{
		BaseURL: energyRequiredEnv("QUERY_CUBE_ENDPOINT"), DatasetRevision: energyRequiredEnv("QUERY_DATASET_REVISION"),
		TokenFactory: accessFactory, HTTPClient: cubeHTTPClient,
	})
	if err != nil {
		return nil, nil, err
	}
	historyHTTPClient, err := embeddedQueryHTTPClient(strings.TrimSpace(os.Getenv("QUERY_HISTORY_CLICKHOUSE_CA")))
	if err != nil {
		return nil, nil, err
	}
	historyClient, err := queryservice.NewHistoryClient(queryservice.HistoryConfig{
		BaseURL: energyRequiredEnv("QUERY_HISTORY_CLICKHOUSE_ENDPOINT"), Database: envOr("QUERY_HISTORY_CLICKHOUSE_DATABASE", "telemetry_history"),
		Table: envOr("QUERY_HISTORY_CLICKHOUSE_TABLE", "observations"), Username: envOr("QUERY_HISTORY_CLICKHOUSE_USERNAME", "telemetry_query_history_reader"),
		Password: os.Getenv("QUERY_HISTORY_CLICKHOUSE_PASSWORD"), HTTPClient: historyHTTPClient,
	})
	if err != nil {
		return nil, nil, err
	}
	telemetry := observability.NewRuntime(observability.RuntimeConfig{
		Service: "energy-api-query", OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), QueueSize: 1024, ExportTimeout: 500 * time.Millisecond,
	})
	server := &http.Server{
		Addr: envOr("QUERY_SERVICE_ADDR", "127.0.0.1:18447"),
		Handler: queryservice.NewHandler(queryservice.ServerConfig{
			Engine: cubeClient, HistoryEngine: historyClient, DelegationPublicKey: delegationPublicKey,
			DelegationIssuerSPIFFE: envOr("QUERY_DELEGATION_ISSUER_SPIFFE", "spiffe://hvac.local/platform-gateway"),
			AllowedPresenterSPIFFE: envOr("QUERY_ALLOWED_WORKLOAD_SPIFFE", "spiffe://hvac.local/platform-gateway"),
			AdditionalAllowedPresenterSPIFFEs: []string{
				envOr("QUERY_OPERATIONS_AGENT_SPIFFE", "spiffe://hvac.local/operations-agent-service"),
			},
			Audience: envOr("QUERY_AUDIENCE", "telemetry-query-service"), Logger: logger, Observability: telemetry,
		}),
		TLSConfig: &tls.Config{
			MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate}, ClientCAs: clientCAs, ClientAuth: tls.RequireAndVerifyClientCert,
		},
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second,
	}
	logger.Info("energy_api_query_configured", "address", server.Addr)
	return server, telemetry, nil
}

func embeddedQueryHTTPClient(caPath string) (*http.Client, error) {
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS13}
	if caPath != "" {
		roots, err := loadCertPool(caPath, "Query data backend CA")
		if err != nil {
			return nil, err
		}
		tlsConfig.RootCAs = roots
	}
	return &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{TLSClientConfig: tlsConfig}}, nil
}

func embeddedLoadValueFile(path string, maximumBytes int64) (string, error) {
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

func embeddedPeerSPIFFE(request *http.Request) string {
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

func embeddedWriteProblem(writer http.ResponseWriter, status int, code string) {
	writer.Header().Set("Content-Type", "application/problem+json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]any{"code": code, "retryable": false})
}

func embeddedCertificateSPIFFEID(certificate *x509.Certificate) (string, error) {
	if certificate == nil || len(certificate.URIs) != 1 || certificate.URIs[0] == nil {
		return "", errors.New("certificate must contain exactly one URI identity")
	}
	identity := certificate.URIs[0].String()
	if !strings.HasPrefix(identity, "spiffe://") {
		return "", errors.New("certificate URI is not a SPIFFE identity")
	}
	return identity, nil
}

func embeddedCertificatePublicKey(path string) (crypto.PublicKey, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(body)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("certificate public key source is invalid")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}
	return certificate.PublicKey, nil
}

func energyRequiredEnv(name string) string {
	return strings.TrimSpace(os.Getenv(name))
}

func envEnabled(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
