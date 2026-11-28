package main

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandauth"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/libs/sessionevent"
	"github.com/quanlaihe/hvac-web/libs/sessionstore"
	"github.com/quanlaihe/hvac-web/libs/workloadtls"
	"github.com/quanlaihe/hvac-web/libs/workordermodel"
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
	cancelBg   context.CancelFunc
	httpWG     sync.WaitGroup
	bgWG       sync.WaitGroup
	serveErr   chan error
}

func launchEmbeddedWorker(wg *sync.WaitGroup, fn func()) {
	if fn == nil {
		return
	}
	if wg == nil {
		go fn()
		return
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		fn()
	}()
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

type embeddedAlarmStoreRef struct {
	mu    sync.RWMutex
	store alarmservice.Store
}

func (ref *embeddedAlarmStoreRef) setStore(store alarmservice.Store) {
	if ref == nil {
		return
	}
	ref.mu.Lock()
	ref.store = store
	ref.mu.Unlock()
}

func (ref *embeddedAlarmStoreRef) getStore() alarmservice.Store {
	if ref == nil {
		return nil
	}
	ref.mu.RLock()
	defer ref.mu.RUnlock()
	return ref.store
}

type embeddedWorkOrderStoreRef struct {
	mu    sync.RWMutex
	store workorderservice.Store
}

func (ref *embeddedWorkOrderStoreRef) setStore(store workorderservice.Store) {
	if ref == nil {
		return
	}
	ref.mu.Lock()
	ref.store = store
	ref.mu.Unlock()
}

func (ref *embeddedWorkOrderStoreRef) getStore() workorderservice.Store {
	if ref == nil {
		return nil
	}
	ref.mu.RLock()
	defer ref.mu.RUnlock()
	return ref.store
}

type embeddedCommandAuthorityRef struct {
	mu        sync.RWMutex
	authority *commandservice.ControlAuthority
}

func (ref *embeddedCommandAuthorityRef) setAuthority(authority *commandservice.ControlAuthority) {
	if ref == nil {
		return
	}
	ref.mu.Lock()
	ref.authority = authority
	ref.mu.Unlock()
}

func (ref *embeddedCommandAuthorityRef) getAuthority() *commandservice.ControlAuthority {
	if ref == nil {
		return nil
	}
	ref.mu.RLock()
	defer ref.mu.RUnlock()
	return ref.authority
}

func startEmbeddedEnergyServices(ctx context.Context, logger *slog.Logger) (*embeddedEnergyServices, error) {
	owners, err := parseEmbeddedOwners(os.Getenv("ENERGY_API_EMBEDDED_OWNERS"))
	if err != nil {
		return nil, err
	}
	serviceCtx, cancelServices := context.WithCancel(ctx)
	services := &embeddedEnergyServices{
		cancelBg: cancelServices,
		serveErr: make(chan error, len(owners)),
	}
	alarmStoreRef := &embeddedAlarmStoreRef{}
	workOrderStoreRef := &embeddedWorkOrderStoreRef{}
	commandAuthorityRef := &embeddedCommandAuthorityRef{}
	fail := func(err error) (*embeddedEnergyServices, error) {
		cancelServices()
		services.bgWG.Wait()
		services.Close()
		return nil, err
	}

	for _, owner := range owners {
		switch owner {
		case "iam":
			server, telemetry, closeOwner, configureErr := newEmbeddedIAMServer(serviceCtx, logger)
			if configureErr != nil {
				return fail(fmt.Errorf("configure embedded IAM: %w", configureErr))
			}
			services.servers = append(services.servers, server)
			services.telemetry = append(services.telemetry, telemetry)
			services.closeFuncs = append(services.closeFuncs, closeOwner)
		case "audit":
			server, telemetry, closeOwner, configureErr := newEmbeddedAuditServer(serviceCtx, logger, &services.bgWG)
			if configureErr != nil {
				return fail(fmt.Errorf("configure embedded Audit: %w", configureErr))
			}
			services.servers = append(services.servers, server)
			services.telemetry = append(services.telemetry, telemetry)
			services.closeFuncs = append(services.closeFuncs, closeOwner)
		case "core":
			server, telemetry, closeOwner, configureErr := newEmbeddedCoreServer(serviceCtx, logger, &services.bgWG)
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
			server, telemetry, closeOwner, configureErr := newEmbeddedAlarmServer(serviceCtx, logger, alarmStoreRef, workOrderStoreRef, commandAuthorityRef, &services.bgWG)
			if configureErr != nil {
				return fail(fmt.Errorf("configure embedded Alarm: %w", configureErr))
			}
			services.servers = append(services.servers, server)
			services.telemetry = append(services.telemetry, telemetry)
			services.closeFuncs = append(services.closeFuncs, closeOwner)
		case "notification":
			server, telemetry, closeOwner, configureErr := newEmbeddedNotificationServer(serviceCtx, logger, &services.bgWG)
			if configureErr != nil {
				return fail(fmt.Errorf("configure embedded Notification: %w", configureErr))
			}
			services.servers = append(services.servers, server)
			services.telemetry = append(services.telemetry, telemetry)
			services.closeFuncs = append(services.closeFuncs, closeOwner)
		case "work-order":
			server, closeOwner, configureErr := newEmbeddedWorkOrderServer(serviceCtx, logger, workOrderStoreRef)
			if configureErr != nil {
				return fail(fmt.Errorf("configure embedded Work Order: %w", configureErr))
			}
			services.servers = append(services.servers, server)
			services.closeFuncs = append(services.closeFuncs, closeOwner)
		case "command":
			server, telemetry, closeOwner, configureErr := newEmbeddedCommandServer(serviceCtx, logger, alarmStoreRef, commandAuthorityRef, &services.bgWG)
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
		services.httpWG.Add(1)
		go func() {
			defer services.httpWG.Done()
			defer observability.Recover(logger, nil, "energy_api.embedded_server", func(any) {
				select {
				case services.serveErr <- fmt.Errorf("embedded service %s panicked", server.Addr):
				default:
				}
			})
			if err := server.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logger.Error("energy_api_embedded_service_stopped", "address", server.Addr, "error_code", "ENERGY_API_EMBEDDED_SERVICE_STOPPED", "cause", err.Error())
				select {
				case services.serveErr <- fmt.Errorf("embedded service %s: %w", server.Addr, err):
				default:
				}
			}
		}()
	}
	logger.Info("energy_api_embedded_services_started", "owners", strings.Join(owners, ","), "service_count", len(services.servers))
	return services, nil
}

func (services *embeddedEnergyServices) ServeErr() <-chan error {
	if services == nil {
		return nil
	}
	return services.serveErr
}

func (services *embeddedEnergyServices) MarkNotReady() {
	if services == nil {
		return
	}
	for _, runtime := range services.telemetry {
		runtime.MarkNotReady()
	}
}

func (services *embeddedEnergyServices) ShutdownHTTP(ctx context.Context) {
	if services == nil {
		return
	}
	var wg sync.WaitGroup
	for _, server := range services.servers {
		srv := server
		if srv == nil {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := srv.Shutdown(ctx); err != nil {
				_ = srv.Close()
			}
		}()
	}
	wg.Wait()
	services.httpWG.Wait()
}

func (services *embeddedEnergyServices) WaitBackground() {
	if services == nil {
		return
	}
	if services.cancelBg != nil {
		services.cancelBg()
	}
	services.bgWG.Wait()
}

func (services *embeddedEnergyServices) ShutdownTelemetry(ctx context.Context) {
	if services == nil {
		return
	}
	for _, runtime := range services.telemetry {
		_ = runtime.Shutdown(ctx)
	}
}

func (services *embeddedEnergyServices) Shutdown(ctx context.Context) {
	if services == nil {
		return
	}
	services.MarkNotReady()
	services.ShutdownHTTP(ctx)
	services.ShutdownTelemetry(ctx)
}

func (services *embeddedEnergyServices) Close() {
	if services == nil {
		return
	}
	services.WaitBackground()
	for i := len(services.closeFuncs) - 1; i >= 0; i-- {
		services.closeFuncs[i]()
	}
	services.closeFuncs = nil
}

func newEmbeddedAuditServer(ctx context.Context, logger *slog.Logger, bgWG *sync.WaitGroup) (*http.Server, *observability.Runtime, func(), error) {
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
		Service: "energy-api-audit", OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), QueueSize: 1024, ExportTimeout: 500 * time.Millisecond, Logger: logger,
	})
	handler := auditserver.NewHandler(auditserver.Config{
		Store: store, OperationsWriter: store, DomainWriter: store,
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
	launchEmbeddedWorker(bgWG, func() {
		defer observability.Recover(logger, telemetry.Metrics, "audit.outbox_relay")
		runEmbeddedAuditOutboxRelay(ctx, outbox, store, telemetry, logger)
	})
	closeAll := func() {
		outbox.Close()
		store.Close()
	}
	logger.Info("energy_api_audit_configured", "address", server.Addr, "transport", "postgres-outbox")
	return server, telemetry, closeAll, nil
}

func runEmbeddedAuditOutboxRelay(ctx context.Context, outbox *sessionstore.OutboxStore, store *auditserver.Store, telemetry *observability.Runtime, logger *slog.Logger) {
	owner := replicaWorkerID("AUDIT_OUTBOX_RELAY_OWNER", "energy-api-audit")
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
			Topic: record.Topic, ReceivedAt: time.Now().UTC(),
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

	databaseURL := energyRequiredEnv("IAM_DATABASE_URL")
	openContext, cancelOpen := context.WithTimeout(ctx, 5*time.Second)
	store, err := iamserver.OpenPostgresAuthorizationStore(openContext, databaseURL)
	cancelOpen()
	if err != nil {
		return nil, nil, func() {}, err
	}
	closeFuncs := []func(){store.Close}
	policyRevision := "database-managed"

	var adminStore *iamserver.PostgresAdminStore
	if adminDatabaseURL := strings.TrimSpace(os.Getenv("IAM_ADMIN_DATABASE_URL")); adminDatabaseURL != "" {
		openContext, cancelOpen = context.WithTimeout(ctx, 5*time.Second)
		postgresAdminStore, openErr := iamserver.OpenPostgresAdminStore(openContext, adminDatabaseURL)
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
			for _, closeStore := range closeFuncs {
				closeStore()
			}
			return nil, nil, func() {}, openErr
		}
		telemetryGrantStore = postgresGrantStore
		closeFuncs = append(closeFuncs, postgresGrantStore.Close)
	}

	telemetry := observability.NewRuntime(observability.RuntimeConfig{
		Service: "energy-api-iam", OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), QueueSize: 1024, ExportTimeout: 500 * time.Millisecond, Logger: logger,
	})
	commandRevocationRevision, err := strconv.ParseUint(energyRequiredEnv("COMMAND_EMERGENCY_REVOCATION_REVISION"), 10, 64)
	if err != nil {
		for _, closeStore := range closeFuncs {
			closeStore()
		}
		return nil, nil, func() {}, errors.New("command emergency revocation revision is invalid")
	}
	config := iamserver.Config{
		AllowedWorkloadSPIFFE:    envOr("IAM_ALLOWED_WORKLOAD_SPIFFE", "spiffe://hvac.local/platform-gateway"),
		CommandAuthoritySPIFFE:   envOr("IAM_COMMAND_AUTHORITY_SPIFFE", "spiffe://hvac.local/command-service"),
		CoreWorkloadSPIFFE:       envOr("IAM_CORE_WORKLOAD_SPIFFE", "spiffe://hvac.local/platform-core-service"),
		CredentialWorkloadSPIFFE: "spiffe://hvac.local/mqtt-telemetry-adapter",
		Audience:                 envOr("IAM_AUDIENCE", "iam-service"),
		Logger:                   logger,
		Observability:            telemetry,
		AuthorizationStore:       store,
		AdminStore:               adminStore,
		RegistryGrantSigner:      signer,
		RegistryGrantIssuer:      iamSPIFFEID,
		RegistryGrantAudience:    envOr("IAM_REGISTRY_GRANT_AUDIENCE", "platform-core-service"),
		AllowedRegistryGrantPresenters: []string{
			envOr("IAM_OPERATIONS_AGENT_SPIFFE", "spiffe://hvac.local/operations-agent-service"),
			envOr("IAM_COMMAND_AUTHORITY_SPIFFE", "spiffe://hvac.local/command-service"),
		},
		// report-service reads the Site's energy as its own Workload Principal (ADR 0017, ADR 0023).
		AnalyticsGrantAudience: envOr("QUERY_AUDIENCE", "telemetry-query-service"),
		AllowedAnalyticsWorkloads: []string{
			envOr("IAM_REPORT_SERVICE_SPIFFE", "spiffe://hvac.local/report-service"),
			envOr("IAM_OPTIMIZATION_SERVICE_SPIFFE", "spiffe://hvac.local/optimization-service"),
			envOr("IAM_MV_SERVICE_SPIFFE", "spiffe://hvac.local/mv-service"),
			// The daily WeChat summary reads yesterday's energy (docs/specs/miniprogram-wechat.md).
			envOr("GATEWAY_WORKLOAD_SPIFFE", "spiffe://hvac.local/platform-gateway"),
		},
		// Notification Service runs in the Gateway's workload and asks who may get a WeChat reminder
		// (docs/specs/miniprogram-wechat.md).
		AllowedReminderRecipientWorkloads: []string{envOr("GATEWAY_WORKLOAD_SPIFFE", "spiffe://hvac.local/platform-gateway")},
		RegistryGrantStatus:               store,
		TelemetryAuthorizationStore:       store,
		AlarmAuthorizationStore:           store,
		AlarmAuditSink:                    store,
		WorkOrderAuthorizationStore:       store,
		WorkOrderAuditSink:                store,
		TelemetryGrantSigner:              signer,
		TelemetryGrantIssuer:              iamSPIFFEID,
		TelemetryGrantAudience:            envOr("IAM_TELEMETRY_GRANT_AUDIENCE", "telemetry-runtime-service"),
		TelemetryRuntimeSPIFFE:            envOr("IAM_TELEMETRY_RUNTIME_SPIFFE", "spiffe://hvac.local/telemetry-runtime-service"),
		TelemetryGrantStore:               telemetryGrantStore,
		AllowedTelemetryGrantPresenters: []string{
			envOr("IAM_OPERATIONS_AGENT_SPIFFE", "spiffe://hvac.local/operations-agent-service"),
		},
		CommandAuthorizationStore: store.CommandAuthorization(energyRequiredEnv("COMMAND_POLICY_REVISION"), commandRevocationRevision),
		CommandGrantSigner:        signer,
		CommandGrantIssuer:        iamSPIFFEID,
		CommandGrantAudience:      envOr("IAM_COMMAND_GRANT_AUDIENCE", "command-service"),
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

func newEmbeddedCoreServer(ctx context.Context, logger *slog.Logger, bgWG *sync.WaitGroup) (*http.Server, *observability.Runtime, func(), error) {
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
		Transport: cloneDefaultTransportWithTLS(&tls.Config{
			MinVersion: tls.VersionTLS13, RootCAs: iamRoots, Certificates: []tls.Certificate{certificate}, ServerName: envOr("CORE_IAM_SERVER_NAME", "iam-service"),
		}),
	}
	grantStatus, err := registryauth.NewHTTPGrantStatusProvider(energyRequiredEnv("CORE_IAM_ENDPOINT"), iamClient)
	if err != nil {
		store.Close()
		return nil, nil, func() {}, err
	}
	telemetry := observability.NewRuntime(observability.RuntimeConfig{
		Service: "energy-api-core", OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), QueueSize: 1024, ExportTimeout: 500 * time.Millisecond, Logger: logger,
	})
	server := &http.Server{
		Addr: envOr("CORE_SERVICE_ADDR", "127.0.0.1:18445"),
		Handler: coreservice.NewHandler(coreservice.ServerConfig{
			Store: store, Writer: store, CursorCodec: cursorCodec, GrantPublicKey: grantPublicKey,
			GrantIssuer:            envOr("CORE_GRANT_ISSUER", "spiffe://hvac.local/iam-service"),
			AllowedPresenterSPIFFE: envOr("CORE_ALLOWED_WORKLOAD_SPIFFE", "spiffe://hvac.local/platform-gateway"),
			AdditionalAllowedPresenterSPIFFEs: []string{
				envOr("CORE_OPERATIONS_AGENT_SPIFFE", "spiffe://hvac.local/operations-agent-service"),
				// Phase 1 runs the energy projector inside the Telemetry Runtime (ADR 0017).
				envOr("CORE_ANALYTICS_PROJECTOR_SPIFFE", "spiffe://hvac.local/telemetry-runtime-service"),
				envOr("CORE_COMMAND_SERVICE_SPIFFE", "spiffe://hvac.local/command-service"),
			},
			AllowedCommandServiceSPIFFE: envOr("CORE_COMMAND_SERVICE_SPIFFE", "spiffe://hvac.local/command-service"),
			Audience:                    envOr("CORE_AUDIENCE", "platform-core-service"), GrantStatus: grantStatus, Logger: logger, Observability: telemetry,
		}),
		TLSConfig: &tls.Config{
			MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate}, ClientCAs: clientCAs, ClientAuth: tls.RequireAndVerifyClientCert,
		},
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second,
	}
	logger.Info("energy_api_core_configured", "address", server.Addr)
	if relay, relayErr := newEmbeddedCoreOutboxRelay(store, certificate, iamRoots, clientCAs, logger); relayErr != nil {
		store.Close()
		return nil, nil, func() {}, relayErr
	} else if relay != nil {
		launchEmbeddedWorker(bgWG, func() {
			defer observability.Recover(logger, telemetry.Metrics, "core.outbox_relay")
			relay.Run(ctx)
		})
	}
	return server, telemetry, store.Close, nil
}

func newEmbeddedCoreOutboxRelay(store *coreservice.PostgresStore, cert tls.Certificate, defaultRoots, fallbackRoots *x509.CertPool, logger *slog.Logger) (*coreservice.OutboxRelay, error) {
	cfg, enabled, err := buildEmbeddedCoreOutboxRelayConfig(cert, defaultRoots, fallbackRoots, logger)
	if err != nil || !enabled {
		return nil, err
	}
	return coreservice.NewOutboxRelay(store, cfg)
}

func buildEmbeddedCoreOutboxRelayConfig(cert tls.Certificate, defaultRoots, fallbackRoots *x509.CertPool, logger *slog.Logger) (coreservice.OutboxRelayConfig, bool, error) {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("CORE_OUTBOX_RELAY_ENABLED")), "false") {
		return coreservice.OutboxRelayConfig{}, false, nil
	}
	telemetryURL := envOr("CORE_TELEMETRY_RUNTIME_URL", strings.TrimSpace(os.Getenv("TELEMETRY_RUNTIME_URL")))
	connectivityURL := envOr("CORE_CONNECTIVITY_SERVICE_URL", strings.TrimSpace(os.Getenv("CONNECTIVITY_SERVICE_URL")))
	commandURL := envOr("CORE_COMMAND_SERVICE_URL", strings.TrimSpace(os.Getenv("COMMAND_SERVICE_URL")))
	if telemetryURL == "" && connectivityURL == "" && commandURL == "" {
		return coreservice.OutboxRelayConfig{}, false, nil
	}
	roots := defaultRoots
	if roots == nil {
		roots = fallbackRoots
	}
	buildClient := func(caEnv, defaultCA, serverName string, fallback *x509.CertPool) (*http.Client, error) {
		pool := fallback
		caPath := strings.TrimSpace(os.Getenv(caEnv))
		if caPath == "" {
			caPath = defaultCA
		}
		if caPath != "" {
			loaded, err := loadCertPool(caPath, caEnv)
			if err != nil {
				return nil, err
			}
			pool = loaded
		}
		if pool == nil {
			return nil, fmt.Errorf("%s root CA is required", caEnv)
		}
		return &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
			Transport: cloneDefaultTransportWithTLS(&tls.Config{
				MinVersion:   tls.VersionTLS13,
				RootCAs:      pool,
				Certificates: []tls.Certificate{cert},
				ServerName:   serverName,
			}),
		}, nil
	}
	var telClient, connClient, cmdClient *http.Client
	if telemetryURL != "" {
		var err error
		telClient, err = buildClient("TELEMETRY_RUNTIME_SERVER_CA", "", envOr("TELEMETRY_RUNTIME_SERVER_NAME", "telemetry-runtime-service"), roots)
		if err != nil {
			return coreservice.OutboxRelayConfig{}, false, err
		}
	}
	if connectivityURL != "" {
		var err error
		connClient, err = buildClient("CONNECTIVITY_SERVER_CA", "/run/hvac/gateway-ca/ca.crt", envOr("CONNECTIVITY_SERVICE_SERVER_NAME", "connectivity"), nil)
		if err != nil {
			return coreservice.OutboxRelayConfig{}, false, err
		}
	}
	if commandURL != "" {
		var err error
		cmdClient, err = buildClient("COMMAND_SERVICE_SERVER_CA", "", envOr("COMMAND_SERVICE_SERVER_NAME", "command-service"), roots)
		if err != nil {
			return coreservice.OutboxRelayConfig{}, false, err
		}
	}
	return coreservice.OutboxRelayConfig{
		TelemetryBaseURL:       telemetryURL,
		TelemetryHTTPClient:    telClient,
		ConnectivityBaseURL:    connectivityURL,
		ConnectivityHTTPClient: connClient,
		CommandBaseURL:         commandURL,
		CommandHTTPClient:      cmdClient,
		Logger:                 logger,
	}, true, nil
}

func newEmbeddedCommandServer(ctx context.Context, logger *slog.Logger, alarmRef *embeddedAlarmStoreRef, authorityRef *embeddedCommandAuthorityRef, bgWG *sync.WaitGroup) (*http.Server, *observability.Runtime, func(), error) {
	certPath := energyRequiredEnv("COMMAND_TLS_CERT")
	keyPath := energyRequiredEnv("COMMAND_TLS_KEY")
	clientCAPath := energyRequiredEnv("COMMAND_CLIENT_CA")
	certificate, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, nil, func() {}, err
	}
	delegationSigner, ok := certificate.PrivateKey.(crypto.Signer)
	if !ok {
		return nil, nil, func() {}, errors.New("command workload key cannot sign delegation tokens")
	}
	clientCAs, err := loadCertPool(clientCAPath, "Command client CA")
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
		return nil, nil, func() {}, errors.New("command policy revision is required")
	}
	revocationRevision, err := strconv.ParseUint(energyRequiredEnv("COMMAND_EMERGENCY_REVOCATION_REVISION"), 10, 64)
	if err != nil {
		store.Close()
		return nil, nil, func() {}, errors.New("command emergency revocation revision is invalid")
	}
	gatewaySPIFFE := envOr("COMMAND_GATEWAY_SPIFFE", "spiffe://hvac.local/platform-gateway")
	commandServiceSPIFFE := envOr("COMMAND_WORKLOAD_SPIFFE", "spiffe://hvac.local/command-service")
	grantIssuer := envOr("COMMAND_GRANT_ISSUER", "spiffe://hvac.local/iam-service")
	grantAudience := envOr("COMMAND_GRANT_AUDIENCE", "command-service")
	gatewayReadAudience := envOr("COMMAND_GATEWAY_READ_AUDIENCE", "command-service")
	useChecker := func(claims commandauth.GrantClaims) (commandauth.UseStatus, error) {
		checkContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		return store.ConsumeCommandGrant(checkContext, claims, policyRevision, revocationRevision)
	}
	adapters, err := commandservice.NewWorkloadAdaptersFromEnv(commandservice.WorkloadEnvConfig{
		CertificateFiles: workloadtls.CertificateFiles{
			CertificatePath: certPath,
			PrivateKeyPath:  keyPath,
		},
		DelegationSigner:      delegationSigner,
		DefaultServerCA:       clientCAPath,
		CommandServiceSPIFFE:  commandServiceSPIFFE,
		PolicyRevision:        policyRevision,
		CommandGrantPublicKey: commandGrantPublicKey,
	})
	if err != nil {
		store.Close()
		return nil, nil, func() {}, err
	}
	alarmSender := buildEmbeddedCommandControlFailureSender(alarmRef, &certificate, logger)
	authority, err := commandservice.NewControlAuthority(commandservice.ControlAuthorityConfig{
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
		store.Close()
		return nil, nil, func() {}, err
	}
	authorityRef.setAuthority(authority)
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
		authority.Close()
		store.Close()
		return nil, nil, func() {}, err
	}
	operatorHandler, err := commandservice.NewOperatorHTTPHandler(commandservice.OperatorHTTPConfig{
		Authority:                  authority,
		GatewayDelegationPublicKey: gatewayDelegationPublicKey,
		GatewaySPIFFE:              gatewaySPIFFE,
		GatewayAudience:            gatewayReadAudience,
	})
	if err != nil {
		authority.Close()
		store.Close()
		return nil, nil, func() {}, err
	}
	telemetry := observability.NewRuntime(observability.RuntimeConfig{
		Service: "energy-api-command", OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), QueueSize: 2048, ExportTimeout: 500 * time.Millisecond, Logger: logger,
	})
	runtimeHandler, err := commandservice.NewRuntimeHTTPHandler(embeddedCommandRuntimeConfig(authority, telemetry.Metrics, nil))
	if err != nil {
		authority.Close()
		store.Close()
		return nil, nil, func() {}, err
	}
	relayCtx, cancelRelay := context.WithCancel(ctx)
	var relayWG sync.WaitGroup
	if alarmSender != nil {
		alarmRelay, relayErr := commandservice.NewControlAlarmRelay(commandservice.ControlAlarmRelayConfig{
			Store:      store,
			Sender:     alarmSender,
			LeaseOwner: commandservice.UniqueControlAlarmLeaseOwner("energy-api-command"),
			Logger:     logger,
			Metrics:    telemetry.Metrics,
		})
		if relayErr != nil {
			cancelRelay()
			if closer, ok := runtimeHandler.(interface{ Close() }); ok {
				closer.Close()
			}
			authority.Close()
			store.Close()
			return nil, nil, func() {}, relayErr
		}
		store.SetControlAlarmOutboxWakeup(alarmRelay.Notify)
		relayWG.Add(1)
		launchEmbeddedWorker(bgWG, func() {
			defer relayWG.Done()
			defer observability.Recover(logger, telemetry.Metrics, "command.alarm_relay")
			alarmRelay.Run(relayCtx)
		})
	}
	const controlAlarmDepthRefreshInterval = 15 * time.Second
	relayWG.Add(1)
	launchEmbeddedWorker(bgWG, func() {
		defer relayWG.Done()
		defer observability.Recover(logger, telemetry.Metrics, "command.control_alarm_outbox_depth")
		refreshDepth := func() {
			queryCtx, cancelQuery := context.WithTimeout(relayCtx, 5*time.Second)
			defer cancelQuery()
			counts, depthErr := store.CountControlAlarmOutboxStates(queryCtx)
			if depthErr != nil {
				if logger != nil && !errors.Is(depthErr, context.Canceled) {
					logger.Warn("command_control_alarm_outbox_depth_refresh_failed", "error_code", commandservice.ControlAlarmErrorCode(depthErr))
				}
				return
			}
			commandservice.RecordControlAlarmOutboxDepthMetrics(telemetry.Metrics, counts)
		}
		refreshDepth()
		ticker := time.NewTicker(controlAlarmDepthRefreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-relayCtx.Done():
				return
			case <-ticker.C:
				refreshDepth()
			}
		}
	})
	handler := newEmbeddedCommandHandler(gatewaySPIFFE, operatorHandler, commandHandler, runtimeHandler, telemetry)
	server := &http.Server{
		Addr: envOr("COMMAND_SERVICE_ADDR", ":8447"), Handler: handler,
		TLSConfig: &tls.Config{
			MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate}, ClientCAs: clientCAs, ClientAuth: tls.RequireAndVerifyClientCert,
		},
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second,
	}
	closeOwner := func() {
		cancelRelay()
		relayWG.Wait()
		if closer, ok := runtimeHandler.(interface{ Close() }); ok {
			closer.Close()
		}
		authority.Close()
		store.Close()
	}
	logger.Info("energy_api_command_configured", "address", server.Addr, "policy_revision", policyRevision)
	return server, telemetry, closeOwner, nil
}

func newEmbeddedCommandHandler(
	gatewaySPIFFE string,
	operatorHandler http.Handler,
	commandHandler http.Handler,
	runtimeHandler http.Handler,
	telemetry *observability.Runtime,
) http.Handler {
	router := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == commandservice.InternalOperatorPrefix || strings.HasPrefix(request.URL.Path, commandservice.InternalOperatorPrefix+"/") {
			if embeddedPeerSPIFFE(request) != gatewaySPIFFE {
				embeddedWriteProblem(writer, http.StatusForbidden, "COMMAND_GATEWAY_WORKLOAD_FORBIDDEN")
				return
			}
			operatorHandler.ServeHTTP(writer, request)
			return
		}
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
	return observability.InstrumentHTTP(router, telemetry, observability.HTTPInstrumentationConfig{
		Namespace: "hvac_command",
		Service:   "command-service",
		SpanName:  "http.command.request",
		Route:     commandservice.ObservabilityRoute,
	})
}

func embeddedCommandRuntimeConfig(store commandservice.RuntimeStore, metrics *observability.Registry, notifier commandservice.ControlFailureNotifier) commandservice.RuntimeHTTPConfig {
	return commandservice.RuntimeHTTPConfig{
		Store: store, Metrics: metrics,
		DispatcherSPIFFE:       envOr("COMMAND_DISPATCHER_SPIFFE", "spiffe://hvac.local/command-dispatcher"),
		VerifierSPIFFE:         envOr("COMMAND_VERIFIER_SPIFFE", "spiffe://hvac.local/command-verifier"),
		CoreRegistrySPIFFEs:    splitNonEmptyCSV(envOr("COMMAND_CORE_REGISTRY_SPIFFES", "spiffe://hvac.local/platform-core-service,spiffe://hvac.local/platform-gateway")),
		ControlFailureNotifier: notifier,
	}
}

func splitNonEmptyCSV(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func buildEmbeddedCommandControlFailureSender(alarmRef *embeddedAlarmStoreRef, clientCert *tls.Certificate, logger *slog.Logger) commandservice.ControlAlarmSender {
	alarmBaseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("ALARM_SERVICE_URL")), "/")
	var remoteClient *http.Client
	if parsed, err := url.Parse(alarmBaseURL); err == nil && parsed.Scheme == "https" && parsed.Host != "" && clientCert != nil {
		if caPath := strings.TrimSpace(os.Getenv("ALARM_SERVICE_SERVER_CA")); caPath != "" {
			if roots, err := loadCertPool(caPath, "Alarm Service server CA"); err == nil {
				remoteClient = &http.Client{
					Timeout: 3 * time.Second,
					CheckRedirect: func(*http.Request, []*http.Request) error {
						return http.ErrUseLastResponse
					},
					Transport: cloneDefaultTransportWithTLS(&tls.Config{
						MinVersion:   tls.VersionTLS13,
						RootCAs:      roots,
						Certificates: []tls.Certificate{*clientCert},
						ServerName:   envOr("ALARM_SERVICE_SERVER_NAME", "localhost"),
					}),
				}
			}
		}
	}
	if alarmRef == nil && (remoteClient == nil || alarmBaseURL == "") {
		return nil
	}
	return func(ctx context.Context, event commandservice.ControlFailureEvent) error {
		actorType := strings.TrimSpace(event.ActorType)
		if actorType == "" {
			actorType = "COMMAND_RUNTIME"
		}
		actorID := strings.TrimSpace(event.ActorID)
		if actorID == "" {
			actorID = "energy-api-command"
		}
		payload := alarmservice.ControlEventRequest{
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
		if store := alarmRef.getStore(); store != nil {
			if _, err := alarmservice.PublishControlEvent(ctx, store, payload, event.OccurredAt); err != nil {
				if logger != nil {
					logger.Warn("embedded_command_control_alarm_failed", "error_code", "CONTROL_ALARM_PUBLISH_FAILED", "command_id", event.CommandID, "event_kind", event.Kind)
				}
				return &commandservice.ControlAlarmDeliveryError{Code: "CONTROL_ALARM_PUBLISH_FAILED"}
			}
			return nil
		}
		if remoteClient == nil || alarmBaseURL == "" {
			return &commandservice.ControlAlarmDeliveryError{Code: "CONTROL_ALARM_TARGET_UNAVAILABLE"}
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			return &commandservice.ControlAlarmDeliveryError{Code: "CONTROL_ALARM_PAYLOAD_INVALID"}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, alarmBaseURL+alarmservice.InternalControlEventsPath, bytes.NewReader(raw))
		if err != nil {
			return &commandservice.ControlAlarmDeliveryError{Code: "CONTROL_ALARM_REQUEST_INVALID"}
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := remoteClient.Do(req)
		if err != nil {
			if logger != nil {
				logger.Warn("embedded_command_control_alarm_failed", "error_code", "CONTROL_ALARM_HTTP_FAILED", "command_id", event.CommandID, "event_kind", event.Kind)
			}
			return &commandservice.ControlAlarmDeliveryError{Code: "CONTROL_ALARM_HTTP_FAILED"}
		}
		drainAndCloseBody(resp.Body)
		if resp.StatusCode >= http.StatusMultipleChoices {
			if logger != nil {
				logger.Warn("embedded_command_control_alarm_rejected", "error_code", "CONTROL_ALARM_HTTP_REJECTED", "status", resp.StatusCode, "command_id", event.CommandID, "event_kind", event.Kind)
			}
			return &commandservice.ControlAlarmDeliveryError{Code: "CONTROL_ALARM_HTTP_REJECTED"}
		}
		return nil
	}
}

func embeddedUUIDv7(now time.Time) (string, error) {
	millis := now.UTC().UnixMilli()
	if millis < 0 || millis > (1<<48)-1 {
		return "", errors.New("UUIDv7 timestamp is out of range")
	}
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[0] = byte(millis >> 40)
	value[1] = byte(millis >> 32)
	value[2] = byte(millis >> 24)
	value[3] = byte(millis >> 16)
	value[4] = byte(millis >> 8)
	value[5] = byte(millis)
	value[6] = (value[6] & 0x0f) | 0x70
	value[8] = (value[8] & 0x3f) | 0x80

	encoded := make([]byte, 36)
	hex.Encode(encoded[0:8], value[0:4])
	encoded[8] = '-'
	hex.Encode(encoded[9:13], value[4:6])
	encoded[13] = '-'
	hex.Encode(encoded[14:18], value[6:8])
	encoded[18] = '-'
	hex.Encode(encoded[19:23], value[8:10])
	encoded[23] = '-'
	hex.Encode(encoded[24:36], value[10:16])
	return string(encoded), nil
}

func embeddedRuleActionWorkOrderCallback(workOrderRef *embeddedWorkOrderStoreRef) func(context.Context, alarmservice.RuleActionWorkOrderRequest) error {
	return func(ctx context.Context, req alarmservice.RuleActionWorkOrderRequest) error {
		woStore := workOrderRef.getStore()
		if woStore == nil {
			return errors.New("work order store is not available")
		}
		occurredAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(req.OccurredAt))
		if err != nil {
			occurredAt = time.Now().UTC()
		}
		workOrderID, err := embeddedUUIDv7(occurredAt)
		if err != nil {
			return fmt.Errorf("allocate work order id: %w", err)
		}
		priority := workordermodel.Priority(strings.ToUpper(strings.TrimSpace(req.Priority)))
		switch priority {
		case workordermodel.PriorityLow, workordermodel.PriorityMedium, workordermodel.PriorityHigh, workordermodel.PriorityUrgent:
		default:
			priority = workordermodel.PriorityHigh
		}
		title := strings.TrimSpace(req.Title)
		if title == "" {
			title = "告警自动运维工单"
		}
		tenantCtx := identitycontext.WithTenantID(ctx, req.TenantID)
		_, err = woStore.Create(tenantCtx, req.TenantID, req.SiteID, workorderservice.CreateMutation{
			WorkOrderID: workOrderID,
			Title:       title,
			Description: fmt.Sprintf("由告警规则触发自动工单（告警编号 %s，规则编号 %s）。", req.AlarmID, req.AssignmentID),
			Priority:    priority,
			SourceReferences: []workordermodel.SourceReference{{
				Domain:       workordermodel.SourceAlarm,
				ResourceID:   req.AlarmID,
				Relationship: workordermodel.RelationshipOrigin,
			}},
			ActorType:      "SYSTEM",
			ActorID:        "alarm-rule-action-relay",
			PolicyRevision: "alarm-rule-action:v1",
			CorrelationID:  req.OutboxID,
			IdempotencyKey: req.IdempotencyKey,
			OccurredAt:     occurredAt.UTC().Format(time.RFC3339Nano),
		})
		if errors.Is(err, workordermodel.ErrInvalidCreate) {
			return alarmservice.MarkPermanentActionFailure("invalid_payload", err)
		}
		return err
	}
}

func embeddedRuleActionProtectionCallback(commandRef *embeddedCommandAuthorityRef) func(context.Context, alarmservice.RuleActionProtectionCommandRequest) error {
	return func(ctx context.Context, req alarmservice.RuleActionProtectionCommandRequest) error {
		authority := commandRef.getAuthority()
		if authority == nil {
			return errors.New("command authority is not available")
		}
		_, err := authority.SubmitProtectionRuleCommand(ctx, commandservice.ProtectionRuleCommandInput{
			TenantID:        req.TenantID,
			SiteID:          req.SiteID,
			DeviceID:        req.TargetDeviceID,
			TargetPointCode: req.TargetPointCode,
			Capability:      req.Capability,
			Parameters:      req.Parameters,
			IdempotencyKey:  req.IdempotencyKey,
			TraceID:         req.TraceID,
			AssignmentID:    req.AssignmentID,
			OutboxID:        req.OutboxID,
		})
		if err != nil {
			var ce *commandservice.ControlError
			if errors.As(err, &ce) && !ce.Retryable {
				return alarmservice.MarkPermanentActionFailure("command_error", err)
			}
			return err
		}
		return nil
	}
}

func newEmbeddedAlarmServer(ctx context.Context, logger *slog.Logger, alarmRef *embeddedAlarmStoreRef, workOrderRef *embeddedWorkOrderStoreRef, commandRef *embeddedCommandAuthorityRef, bgWG *sync.WaitGroup) (*http.Server, *observability.Runtime, func(), error) {
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
	telemetry := observability.NewRuntime(observability.RuntimeConfig{
		Service: "energy-api-alarm", OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), QueueSize: 1024, ExportTimeout: 500 * time.Millisecond, Logger: logger,
	})
	handler, err := alarmservice.NewHTTPHandler(alarmservice.HTTPConfig{
		Store: store, Rules: store, Metrics: telemetry.Metrics, GatewayPublicKey: gatewayPublicKey, GatewaySPIFFEID: gatewaySPIFFE,
		Audience: envOr("ALARM_READ_AUDIENCE", alarmservice.DefaultAudience), MaxListLimit: 200,
	})
	if err != nil {
		_ = telemetry.Shutdown(ctx)
		store.Close()
		return nil, nil, func() {}, err
	}
	telemetryEvaluationHandler, err := alarmservice.NewTelemetryEvaluationHandler(store)
	if err != nil {
		_ = telemetry.Shutdown(ctx)
		store.Close()
		return nil, nil, func() {}, err
	}
	edgeDedupChecker, err := alarmservice.LoadConnectivityEdgeRuleSyncChecker(logger)
	if err != nil {
		_ = telemetry.Shutdown(ctx)
		store.Close()
		return nil, nil, func() {}, err
	}
	store.SetEdgeRuleSyncChecker(edgeDedupChecker)
	controlEventsHandler, err := alarmservice.NewControlFailureHandler(store)
	if err != nil {
		_ = telemetry.Shutdown(ctx)
		store.Close()
		return nil, nil, func() {}, err
	}
	edgeEventsHandler, err := alarmservice.NewEdgeAlarmEventHandler(store)
	if err != nil {
		_ = telemetry.Shutdown(ctx)
		store.Close()
		return nil, nil, func() {}, err
	}
	alarmRef.setStore(store)
	telemetrySPIFFE := envOr("ALARM_TELEMETRY_SPIFFE", "spiffe://hvac.local/telemetry-runtime-service")
	router, err := alarmservice.NewOwnerRouter(alarmservice.OwnerRouterConfig{
		GatewaySPIFFE:        gatewaySPIFFE,
		TelemetrySPIFFE:      telemetrySPIFFE,
		ReaderSPIFFE:         envOr("ALARM_OPERATIONS_AGENT_SPIFFE", "spiffe://hvac.local/operations-agent-service"),
		ControlEventsSPIFFEs: embeddedAlarmControlEventsSPIFFEs(),
		GatewayHandler:       handler,
		TelemetryHandler:     telemetryEvaluationHandler,
		ControlEventsHandler: controlEventsHandler,
		EdgeEventsHandler:    edgeEventsHandler,
	})
	if err != nil {
		store.Close()
		return nil, nil, func() {}, err
	}
	evalCtx, cancelEval := context.WithCancel(ctx)
	// A Site that stops reporting sends no telemetry to evaluate; only the scheduled pass notices it (#615).
	scheduledDone := alarmservice.StartScheduledEvaluations(evalCtx, store, logger)
	actionRelay, err := alarmservice.NewRuleActionRelay(alarmservice.RuleActionRelayConfig{
		Store:                   store,
		WebhookSender:           alarmservice.NewSSRFWebhookSender(alarmservice.NewDirWebhookSecretResolver(os.Getenv("ALARM_WEBHOOK_SECRET_DIR"))),
		CreateWorkOrder:         embeddedRuleActionWorkOrderCallback(workOrderRef),
		SubmitProtectionCommand: embeddedRuleActionProtectionCallback(commandRef),
		Metrics:                 telemetry.Metrics,
		Logger:                  logger,
		WorkerID:                replicaWorkerID("ALARM_ACTION_RELAY_WORKER_ID", "energy-api-alarm-action-relay"),
	})
	if err != nil {
		cancelEval()
		<-scheduledDone
		store.Close()
		return nil, nil, func() {}, err
	}
	actionRelayDone := actionRelay.Start(evalCtx, 5*time.Second)
	launchEmbeddedWorker(bgWG, func() {
		<-scheduledDone
	})
	launchEmbeddedWorker(bgWG, func() {
		<-actionRelayDone
	})
	server := &http.Server{
		Addr: envOr("ALARM_SERVICE_ADDR", ":8448"), Handler: router,
		TLSConfig: &tls.Config{
			MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate}, ClientCAs: clientCAs, ClientAuth: tls.RequireAndVerifyClientCert,
		},
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second,
	}
	closeOwner := func() {
		cancelEval()
		<-scheduledDone
		<-actionRelayDone
		store.Close()
	}
	logger.Info("energy_api_alarm_configured", "address", server.Addr)
	return server, telemetry, closeOwner, nil
}

func newEmbeddedWorkOrderServer(ctx context.Context, logger *slog.Logger, workOrderRef *embeddedWorkOrderStoreRef) (*http.Server, func(), error) {
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
		return nil, func() {}, errors.New("work order cursor configuration is invalid")
	}
	openContext, cancelOpen := context.WithTimeout(ctx, 10*time.Second)
	store, err := workorderservice.OpenPostgresStoreWithMutations(openContext, databaseURL, mutationDatabaseURL, []byte(cursorMaterial))
	cancelOpen()
	if err != nil {
		return nil, func() {}, err
	}
	workOrderRef.setStore(store)
	gatewaySPIFFE := envOr("WORK_ORDER_GATEWAY_SPIFFE", workorderservice.DefaultGatewaySPIFFEID)
	handler, err := workorderservice.NewHTTPHandler(workorderservice.HTTPConfig{
		Store: store, GatewayPublicKey: gatewayPublicKey, GatewaySPIFFEID: gatewaySPIFFE,
		Audience: envOr("WORK_ORDER_READ_AUDIENCE", workorderservice.DefaultAudience), MaxListLimit: 100,
	})
	if err != nil {
		store.Close()
		return nil, func() {}, err
	}
	router, err := workorderservice.NewOwnerRouter(workorderservice.OwnerRouterConfig{
		GatewaySPIFFE: gatewaySPIFFE,
		ReaderSPIFFE:  envOr("WORK_ORDER_OPERATIONS_AGENT_SPIFFE", "spiffe://hvac.local/operations-agent-service"),
		Handler:       handler,
	})
	if err != nil {
		store.Close()
		return nil, func() {}, err
	}
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
	// IAM's grant certificate, the one Registry verifies: Workload Principals' Energy Series grants carry it.
	workloadGrantPublicKey, err := embeddedCertificatePublicKey(energyRequiredEnv("CORE_IAM_GRANT_CERT"))
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
		Service: "energy-api-query", OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), QueueSize: 1024, ExportTimeout: 500 * time.Millisecond, Logger: logger,
	})
	server := &http.Server{
		Addr: envOr("QUERY_SERVICE_ADDR", "127.0.0.1:18447"),
		Handler: queryservice.NewHandler(queryservice.ServerConfig{
			Engine: cubeClient, HistoryEngine: historyClient, DelegationPublicKey: delegationPublicKey,
			DelegationIssuerSPIFFE:    envOr("QUERY_DELEGATION_ISSUER_SPIFFE", "spiffe://hvac.local/platform-gateway"),
			WorkloadGrantPublicKey:    workloadGrantPublicKey,
			WorkloadGrantIssuerSPIFFE: envOr("CORE_GRANT_ISSUER", "spiffe://hvac.local/iam-service"),
			AllowedPresenterSPIFFE:    envOr("QUERY_ALLOWED_WORKLOAD_SPIFFE", "spiffe://hvac.local/platform-gateway"),
			AdditionalAllowedPresenterSPIFFEs: []string{
				envOr("QUERY_OPERATIONS_AGENT_SPIFFE", "spiffe://hvac.local/operations-agent-service"),
				envOr("QUERY_FDD_SERVICE_SPIFFE", "spiffe://hvac.local/fdd-service"),
				envOr("QUERY_REPORT_SERVICE_SPIFFE", "spiffe://hvac.local/report-service"),
				envOr("QUERY_OPTIMIZATION_SERVICE_SPIFFE", "spiffe://hvac.local/optimization-service"),
				envOr("QUERY_MV_SERVICE_SPIFFE", "spiffe://hvac.local/mv-service"),
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
	return &http.Client{Timeout: 15 * time.Second, Transport: cloneDefaultTransportWithTLS(tlsConfig)}, nil
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

func embeddedAlarmControlEventsSPIFFEs() []string {
	return []string{
		envOr("ALARM_COMMAND_SPIFFE", "spiffe://hvac.local/command-service"),
		envOr("ALARM_CONNECTIVITY_SPIFFE", "spiffe://hvac.local/connectivity-service"),
		envOr("ALARM_OPTIMIZATION_SPIFFE", "spiffe://hvac.local/optimization-service"),
	}
}

func energyRequiredEnv(name string) string {
	return strings.TrimSpace(os.Getenv(name))
}
