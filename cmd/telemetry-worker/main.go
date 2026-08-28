package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/quanlaihe/hvac-web/libs/limitpolicy"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/modules/energy/pkg/analyticsprojector"
	"github.com/quanlaihe/hvac-web/modules/telemetry/pkg/telemetry"
	"github.com/redis/go-redis/v9"
)

func main() {
	observabilityRuntime := observability.NewRuntime(observability.RuntimeConfig{
		Service: "telemetry-runtime-service", OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
		QueueSize: 1024, ExportTimeout: 500 * time.Millisecond,
	})
	defer func() {
		shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancelShutdown()
		_ = observabilityRuntime.Shutdown(shutdownContext)
	}()
	logger := observability.NewJSONLogger(os.Stdout, slog.LevelInfo)
	certificate, err := tls.LoadX509KeyPair(requiredEnv("TELEMETRY_TLS_CERT"), requiredEnv("TELEMETRY_TLS_KEY"))
	if err != nil {
		logger.Error("telemetry_tls_identity_load_failed", "error_code", "TELEMETRY_TLS_IDENTITY_LOAD_FAILED")
		os.Exit(1)
	}
	clientCAs, err := loadCertPool(requiredEnv("TELEMETRY_CLIENT_CA"))
	if err != nil {
		logger.Error("telemetry_client_ca_invalid", "error_code", "TELEMETRY_CLIENT_CA_INVALID")
		os.Exit(1)
	}
	iamCAs, err := loadCertPool(requiredEnv("TELEMETRY_IAM_CA"))
	if err != nil {
		logger.Error("telemetry_iam_ca_invalid", "error_code", "TELEMETRY_IAM_CA_INVALID")
		os.Exit(1)
	}
	iamGrantPublicKey, err := loadCertificatePublicKey(requiredEnv("TELEMETRY_IAM_GRANT_CERT"))
	if err != nil {
		logger.Error("telemetry_iam_grant_certificate_invalid", "error_code", "TELEMETRY_IAM_GRANT_CERTIFICATE_INVALID")
		os.Exit(1)
	}

	openContext, cancelOpen := context.WithTimeout(context.Background(), 5*time.Second)
	store, err := telemetry.OpenPostgresStore(openContext, requiredEnv("TELEMETRY_DATABASE_URL"))
	cancelOpen()
	if err != nil {
		logger.Error("telemetry_store_open_failed", "error_code", "TELEMETRY_STORE_OPEN_FAILED")
		os.Exit(1)
	}
	defer store.Close()

	latestCache, latestRelay, latestContext, latestCancel, err := loadLatestCache(store)
	if err != nil {
		logger.Warn("telemetry_latest_cache_projection_unavailable", "error_code", "TELEMETRY_LATEST_CACHE_PROJECTION_UNAVAILABLE")
		latestCache, latestRelay, latestContext, latestCancel = nil, nil, context.Background(), nil
	}
	dependencies := []observability.Dependency{
		{Name: "postgres", Required: true, Check: store.Ping},
	}
	if latestCache != nil {
		// Redis 只承载可重建的 Latest 投影：不可用时降级，不影响真值与就绪判定。
		dependencies = append(dependencies, observability.Dependency{Name: "redis-latest-cache", Required: false, Check: latestCache.Ping})
	}
	observabilityRuntime.SetDependencies(dependencies...)
	if latestCache != nil {
		defer func() { _ = latestCache.Close() }()
	}
	if latestCancel != nil {
		defer latestCancel()
		go runLatestCacheRelay(latestContext, latestRelay, logger)
	}

	historyRepository, historyRelay, historyContext, historyCancel, err := loadHistoryProjection()
	if err != nil {
		logger.Error("telemetry_history_projection_configuration_invalid", "error_code", "TELEMETRY_HISTORY_PROJECTION_CONFIGURATION_INVALID")
		os.Exit(1)
	}
	if historyRepository != nil {
		defer historyRepository.Close()
	}
	if historyCancel != nil {
		defer historyCancel()
		go runHistoryProjection(historyContext, historyRelay, logger)
	}

	analyticsProjector, analyticsContext, analyticsCancel, err := loadAnalyticsProjection(certificate)
	if err != nil {
		logger.Error("analytics_projection_configuration_invalid", "error_code", "ANALYTICS_PROJECTION_CONFIGURATION_INVALID")
		os.Exit(1)
	}
	if analyticsCancel != nil {
		defer analyticsCancel()
		go runAnalyticsProjection(analyticsContext, analyticsProjector, observabilityRuntime, logger)
	}

	sourceAuthenticator, err := telemetry.ParseSourceAuthenticatorJSON(requiredEnv("TELEMETRY_SOURCE_BINDINGS_JSON"))
	if err != nil {
		logger.Error("telemetry_source_bindings_invalid", "error_code", "TELEMETRY_SOURCE_BINDINGS_INVALID")
		os.Exit(1)
	}

	iamClient := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			TLSClientConfig: &tls.Config{
				MinVersion: tls.VersionTLS13, RootCAs: iamCAs, Certificates: []tls.Certificate{certificate},
			},
			DisableCompression: true,
			ForceAttemptHTTP2:  false,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	authorizer, err := telemetry.NewHTTPGrantAuthorizer(
		requiredEnv("TELEMETRY_IAM_ENDPOINT"), iamClient, iamGrantPublicKey,
		envOr("TELEMETRY_GRANT_ISSUER", "spiffe://hvac.local/iam-service"),
		envOr("TELEMETRY_GRANT_AUDIENCE", "telemetry-runtime-service"),
	)
	if err != nil {
		logger.Error("telemetry_iam_authorizer_invalid", "error_code", "TELEMETRY_IAM_AUTHORIZER_INVALID")
		os.Exit(1)
	}

	realtimeService, realtimeContext, realtimeCancel, err := loadRealtimeService(store, certificate, observabilityRuntime.Metrics)
	if err != nil {
		logger.Error("telemetry_realtime_configuration_invalid", "error_code", "TELEMETRY_REALTIME_CONFIGURATION_INVALID")
		os.Exit(1)
	}
	if realtimeCancel != nil {
		defer realtimeCancel()
		go runRealtimeRelay(realtimeContext, realtimeService, logger)
	}

	alarmRelay, alarmContext, alarmCancel, err := loadAlarmEvaluationRelay(store, certificate)
	if err != nil {
		logger.Error("telemetry_alarm_evaluation_configuration_invalid", "error_code", "TELEMETRY_ALARM_EVALUATION_CONFIGURATION_INVALID")
		os.Exit(1)
	}
	if alarmCancel != nil {
		defer alarmCancel()
		go runAlarmEvaluationRelay(alarmContext, alarmRelay, logger)
	}

	rateLimiter, closeRateLimiter, err := loadTelemetryRateLimiter()
	if err != nil {
		logger.Error("telemetry_limit_policy_config_invalid", "error_code", "TELEMETRY_LIMIT_POLICY_CONFIG_INVALID")
		os.Exit(1)
	}
	defer closeRateLimiter()

	server := &http.Server{
		Addr: envOr("TELEMETRY_SERVICE_ADDR", "127.0.0.1:18446"),
		Handler: telemetry.NewHandler(telemetry.ServerConfig{
			Store: store, LatestCache: latestCache, Authorizer: authorizer,
			AllowedGatewaySPIFFE: envOr("TELEMETRY_ALLOWED_GATEWAY_SPIFFE", "spiffe://hvac.local/platform-gateway"),
			RuntimeAudience:      envOr("TELEMETRY_GRANT_AUDIENCE", "telemetry-runtime-service"),
			ObservationAcceptor: store, HistoricalObservationAcceptor: store,
			AllowedHistoricalReplaySPIFFE: envOr("TELEMETRY_ALLOWED_HISTORICAL_REPLAY_SPIFFE", "spiffe://hvac.local/historical-replay-runner"),
			CoverageReporter: store, MQTTEvidenceAcceptor: store, SourceAuthenticator: sourceAuthenticator,
			Realtime:                       realtimeService,
			AllowedCentrifugoSPIFFE:        envOr("TELEMETRY_ALLOWED_CENTRIFUGO_SPIFFE", "spiffe://hvac.local/centrifugo"),
			CentrifugoProxySecret:          strings.TrimSpace(os.Getenv("TELEMETRY_CENTRIFUGO_PROXY_SECRET")),
			AllowedIAMSPIFFE:               envOr("TELEMETRY_ALLOWED_IAM_SPIFFE", "spiffe://hvac.local/iam-service"),
			AllowedCommandVerifierSPIFFE:   strings.TrimSpace(os.Getenv("TELEMETRY_ALLOWED_COMMAND_VERIFIER_SPIFFE")),
			AllowedCommandDispatcherSPIFFE: strings.TrimSpace(os.Getenv("TELEMETRY_ALLOWED_COMMAND_DISPATCHER_SPIFFE")),
			CommandVerifierTenantID:        strings.TrimSpace(os.Getenv("TELEMETRY_COMMAND_VERIFIER_TENANT_ID")),
			CommandVerifierSiteID:          strings.TrimSpace(os.Getenv("TELEMETRY_COMMAND_VERIFIER_SITE_ID")),
			CommandVerifierDeviceID:        strings.TrimSpace(os.Getenv("TELEMETRY_COMMAND_VERIFIER_DEVICE_ID")),
			CommandVerifierDeviceIDs:       commaSeparated(os.Getenv("TELEMETRY_COMMAND_VERIFIER_DEVICE_IDS")),
			Metrics:                        observabilityRuntime.Metrics,
			RateLimiter:                    rateLimiter,
		}),
		TLSConfig: &tls.Config{
			MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate},
			ClientCAs: clientCAs, ClientAuth: tls.RequireAndVerifyClientCert,
		},
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	diagnostics := &http.Server{
		Addr:              envOr("TELEMETRY_DIAGNOSTICS_ADDR", "127.0.0.1:19086"),
		Handler:           observabilityRuntime.DiagnosticsHandler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if err := diagnostics.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("telemetry_diagnostics_stopped_unexpectedly", "error_code", "TELEMETRY_DIAGNOSTICS_SERVE_FAILED")
		}
	}()
	observabilityRuntime.MarkReady()

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-shutdown
		observabilityRuntime.MarkNotReady()
		context, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = diagnostics.Shutdown(context)
		_ = server.Shutdown(context)
	}()

	logger.Info("telemetry_runtime_started", "service", "telemetry-runtime-service", "address", server.Addr)
	if err := server.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("telemetry_runtime_stopped_unexpectedly", "error_code", "TELEMETRY_RUNTIME_SERVE_FAILED")
		os.Exit(1)
	}
	logger.Info("telemetry_runtime_stopped", "service", "telemetry-runtime-service")
}

func loadLatestCache(store *telemetry.PostgresStore) (telemetry.LatestCache, *telemetry.LatestCacheRelay, context.Context, context.CancelFunc, error) {
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("TELEMETRY_LATEST_CACHE_ENABLED")), "true") {
		return nil, nil, context.Background(), nil, nil
	}
	redisURL := strings.TrimSpace(os.Getenv("TELEMETRY_LATEST_CACHE_REDIS_URL"))
	if redisURL == "" {
		return nil, nil, nil, nil, errors.New("TELEMETRY_LATEST_CACHE_REDIS_URL is required when the rebuildable Latest projection is enabled")
	}
	openContext, cancelOpen := context.WithTimeout(context.Background(), 5*time.Second)
	cache, err := telemetry.OpenRedisLatestCache(openContext, telemetry.RedisLatestCacheConfig{
		URL:       redisURL,
		KeyPrefix: strings.TrimSpace(os.Getenv("TELEMETRY_LATEST_CACHE_KEY_PREFIX")),
	})
	cancelOpen()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	rebuildContext, cancelRebuild := context.WithTimeout(context.Background(), 30*time.Second)
	_, err = telemetry.RebuildLatestCache(rebuildContext, store, cache)
	cancelRebuild()
	if err != nil {
		_ = cache.Close()
		return nil, nil, nil, nil, err
	}
	relay, err := telemetry.NewLatestCacheRelay(store, cache, time.Now)
	if err != nil {
		_ = cache.Close()
		return nil, nil, nil, nil, err
	}
	relayContext, relayCancel := context.WithCancel(context.Background())
	return cache, relay, relayContext, relayCancel, nil
}

func runLatestCacheRelay(ctx context.Context, relay *telemetry.LatestCacheRelay, logger *slog.Logger) {
	if relay == nil {
		return
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	lastFailureLog := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			materialized, err := relay.RelayOnce(ctx, 64)
			if err != nil {
				if time.Since(lastFailureLog) >= time.Second {
					logger.Warn("telemetry_latest_cache_relay_failed", "error_code", "TELEMETRY_LATEST_CACHE_RELAY_FAILED")
					lastFailureLog = time.Now()
				}
				continue
			}
			if materialized > 0 {
				logger.Info("telemetry_latest_cache_relay_batch_materialized", "materialized_count", materialized)
			}
		}
	}
}

func loadHistoryProjection() (*telemetry.HistoryPostgresRepository, *telemetry.HistoryRelay, context.Context, context.CancelFunc, error) {
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("TELEMETRY_HISTORY_PROJECTION_ENABLED")), "true") {
		return nil, nil, context.Background(), nil, nil
	}
	openContext, cancelOpen := context.WithTimeout(context.Background(), 5*time.Second)
	repository, err := telemetry.OpenHistoryPostgresRepository(openContext, requiredEnv("TELEMETRY_HISTORY_DATABASE_URL"))
	cancelOpen()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	sink, err := telemetry.NewClickHouseHistorySink(telemetry.ClickHouseHistoryConfig{
		BaseURL:  requiredEnv("TELEMETRY_CLICKHOUSE_HTTP_URL"),
		Database: envOr("TELEMETRY_CLICKHOUSE_DATABASE", "telemetry_history"),
		Table:    envOr("TELEMETRY_CLICKHOUSE_TABLE", "observations"),
		Username: strings.TrimSpace(os.Getenv("TELEMETRY_CLICKHOUSE_USERNAME")),
		Password: os.Getenv("TELEMETRY_CLICKHOUSE_PASSWORD"),
	})
	if err != nil {
		repository.Close()
		return nil, nil, nil, nil, err
	}
	relay, err := telemetry.NewHistoryRelay(telemetry.HistoryRelayConfig{
		Repository:  repository,
		Sink:        sink,
		BatchSize:   integerEnv("TELEMETRY_HISTORY_BATCH_SIZE", 256, 1, 4096),
		LeaseFor:    durationEnv("TELEMETRY_HISTORY_LEASE_DURATION", 30*time.Second, time.Second, 10*time.Minute),
		RetryAfter:  durationEnv("TELEMETRY_HISTORY_RETRY_DELAY", 5*time.Second, time.Second, time.Hour),
		MaxAttempts: integerEnv("TELEMETRY_HISTORY_MAX_ATTEMPTS", 12, 1, 100),
	})
	if err != nil {
		repository.Close()
		return nil, nil, nil, nil, err
	}
	relayContext, relayCancel := context.WithCancel(context.Background())
	return repository, relay, relayContext, relayCancel, nil
}

func runHistoryProjection(ctx context.Context, relay *telemetry.HistoryRelay, logger *slog.Logger) {
	if relay == nil {
		return
	}
	pollInterval := durationEnv("TELEMETRY_HISTORY_POLL_INTERVAL", 250*time.Millisecond, 25*time.Millisecond, time.Minute)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	lastFailureLog := time.Time{}
	logger.Info("telemetry_history_projection_started", "poll_interval", pollInterval.String())
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			relayContext, relayCancel := context.WithTimeout(ctx, 15*time.Second)
			published, err := relay.RelayOnce(relayContext)
			relayCancel()
			if err != nil {
				if time.Since(lastFailureLog) >= time.Second {
					logger.Warn("telemetry_history_projection_failed", "error_code", "TELEMETRY_HISTORY_PROJECTION_FAILED")
					lastFailureLog = time.Now()
				}
				continue
			}
			if published > 0 {
				logger.Info("telemetry_history_batch_projected", "observation_count", published)
			}
		}
	}
}

func loadAnalyticsProjection(certificate tls.Certificate) (*analyticsprojector.Projector, context.Context, context.CancelFunc, error) {
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("ANALYTICS_PROJECTION_ENABLED")), "true") {
		return nil, context.Background(), nil, nil
	}
	httpClient, err := newAnalyticsHTTPClient(strings.TrimSpace(os.Getenv("ANALYTICS_CLICKHOUSE_CA")))
	if err != nil {
		return nil, nil, nil, err
	}
	baseURL := requiredEnv("ANALYTICS_CLICKHOUSE_HTTP_URL")
	reader, err := analyticsprojector.NewReader(analyticsprojector.ReaderConfig{
		BaseURL:           baseURL,
		SourceDatabase:    envOr("ANALYTICS_SOURCE_DATABASE", "telemetry_history"),
		SourceTable:       envOr("ANALYTICS_SOURCE_TABLE", "counter_deltas"),
		AnalyticsDatabase: envOr("ANALYTICS_DATABASE", "analytics"),
		AnalyticsTable:    envOr("ANALYTICS_ENERGY_TABLE", "energy_interval_facts"),
		Username:          strings.TrimSpace(os.Getenv("ANALYTICS_CLICKHOUSE_READER_USERNAME")),
		Password:          os.Getenv("ANALYTICS_CLICKHOUSE_READER_PASSWORD"),
		HTTPClient:        httpClient,
	})
	if err != nil {
		return nil, nil, nil, err
	}
	writer, err := analyticsprojector.NewWriter(analyticsprojector.WriterConfig{
		BaseURL:    baseURL,
		Database:   envOr("ANALYTICS_DATABASE", "analytics"),
		Table:      envOr("ANALYTICS_ENERGY_TABLE", "energy_interval_facts"),
		Username:   strings.TrimSpace(os.Getenv("ANALYTICS_CLICKHOUSE_WRITER_USERNAME")),
		Password:   os.Getenv("ANALYTICS_CLICKHOUSE_WRITER_PASSWORD"),
		HTTPClient: httpClient,
	})
	if err != nil {
		return nil, nil, nil, err
	}
	coreRoots, err := loadCertPool(requiredEnv("ANALYTICS_CORE_CA"))
	if err != nil {
		return nil, nil, nil, err
	}
	coreClient := &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{
			Proxy:              http.ProxyFromEnvironment,
			TLSClientConfig:    &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: coreRoots, Certificates: []tls.Certificate{certificate}},
			DisableCompression: true,
		},
	}
	bindingResolver, err := analyticsprojector.NewBindingResolver(analyticsprojector.BindingResolverConfig{
		BaseURL: requiredEnv("ANALYTICS_CORE_REGISTRY_URL"), Grant: os.Getenv("ANALYTICS_CORE_REGISTRY_GRANT"), GrantFile: os.Getenv("ANALYTICS_CORE_REGISTRY_GRANT_FILE"), HTTPClient: coreClient,
	})
	if err != nil {
		return nil, nil, nil, err
	}
	projector, err := analyticsprojector.NewProjector(analyticsprojector.ProjectorConfig{
		CounterSource: reader, BindingResolver: bindingResolver, FactSink: writer, BatchSize: integerEnv("ANALYTICS_PROJECTOR_BATCH_SIZE", 256, 1, 4096),
	})
	if err != nil {
		return nil, nil, nil, err
	}
	projectionContext, projectionCancel := context.WithCancel(context.Background())
	return projector, projectionContext, projectionCancel, nil
}

func runAnalyticsProjection(ctx context.Context, projector *analyticsprojector.Projector, runtime *observability.Runtime, logger *slog.Logger) {
	if projector == nil {
		return
	}
	pollInterval := durationEnv("ANALYTICS_PROJECTOR_POLL_INTERVAL", 500*time.Millisecond, 25*time.Millisecond, time.Minute)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	lastFailureLog := time.Time{}
	logger.Info("analytics_read_model_projection_started", "poll_interval", pollInterval.String(), "deployment", "telemetry-runtime-in-process")
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			projectionContext, span := runtime.Tracer.Start(ctx, "analytics.energy_interval.project", observability.SpanKindInternal, map[string]any{
				"db.system": "clickhouse", "analytics.model": "energy_interval_facts",
			})
			projectContext, projectCancel := context.WithTimeout(projectionContext, 20*time.Second)
			projected, err := projector.ProjectOnce(projectContext)
			projectCancel()
			if err != nil {
				span.SetStatus("error", "projection failed")
				span.End()
				_ = runtime.Metrics.AddCounter("hvac_analytics_projection_failures_total", "Failed analytics read-model projection batches.", map[string]string{"model": "energy_interval_facts"}, 1)
				if time.Since(lastFailureLog) >= time.Second {
					logger.Warn("analytics_energy_projection_failed", "error_code", "ANALYTICS_ENERGY_PROJECTION_FAILED")
					lastFailureLog = time.Now()
				}
				continue
			}
			span.SetStatus("ok", "")
			span.End()
			if projected > 0 {
				_ = runtime.Metrics.AddCounter("hvac_analytics_energy_intervals_projected_total", "Projected additive energy intervals.", map[string]string{"energy_type": analyticsprojector.EnergyTypeElectricity}, float64(projected))
				logger.Info("analytics_energy_intervals_projected", "interval_count", projected)
			}
		}
	}
}

func newAnalyticsHTTPClient(caPath string) (*http.Client, error) {
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS13}
	if caPath != "" {
		roots, err := loadCertPool(caPath)
		if err != nil {
			return nil, err
		}
		tlsConfig.RootCAs = roots
	}
	return &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{TLSClientConfig: tlsConfig}}, nil
}

func loadRealtimeService(store *telemetry.PostgresStore, certificate tls.Certificate, metrics *observability.Registry) (*telemetry.RealtimeService, context.Context, context.CancelFunc, error) {
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("TELEMETRY_REALTIME_ENABLED")), "true") {
		return nil, context.Background(), nil, nil
	}
	centrifugoCAs, err := loadCertPool(requiredEnv("TELEMETRY_CENTRIFUGO_CA"))
	if err != nil {
		return nil, nil, nil, err
	}
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		TLSClientConfig: &tls.Config{
			MinVersion:   tls.VersionTLS13,
			RootCAs:      centrifugoCAs,
			Certificates: []tls.Certificate{certificate},
			ServerName:   strings.TrimSpace(os.Getenv("TELEMETRY_CENTRIFUGO_SERVER_NAME")),
		},
		DisableCompression: true,
		ForceAttemptHTTP2:  false,
	}
	centrifugo, err := telemetry.NewCentrifugoTransport(telemetry.CentrifugoTransportConfig{
		BaseURL:    requiredEnv("TELEMETRY_CENTRIFUGO_API_URL"),
		APIKey:     requiredEnv("TELEMETRY_CENTRIFUGO_API_KEY"),
		HTTPClient: telemetry.NewBoundedCentrifugoHTTPClient(transport),
	})
	if err != nil {
		return nil, nil, nil, err
	}
	instrumentedTransport := telemetry.InstrumentRealtimeTransport(centrifugo, metrics, time.Now)
	relayWorkerID := strings.TrimSpace(os.Getenv("TELEMETRY_REALTIME_RELAY_WORKER_ID"))
	if relayWorkerID == "" {
		hostname, _ := os.Hostname()
		relayWorkerID = "telemetry-realtime:" + hostname
	}
	service, err := telemetry.NewRealtimeService(telemetry.RealtimeConfig{
		Repository:             store,
		Transport:              instrumentedTransport,
		PublicEndpoint:         requiredEnv("TELEMETRY_REALTIME_ENDPOINT"),
		CapabilityHMACKey:      []byte(requiredEnv("TELEMETRY_REALTIME_CAPABILITY_HMAC_KEY")),
		ConnectionTokenHMACKey: []byte(requiredEnv("TELEMETRY_CENTRIFUGO_TOKEN_HMAC_KEY")),
		RelayWorkerID:          relayWorkerID,
		RelayLeaseDuration:     durationEnv("TELEMETRY_REALTIME_RELAY_LEASE_DURATION", 30*time.Second, 5*time.Second, 5*time.Minute),
	})
	if err != nil {
		return nil, nil, nil, err
	}
	if len(strings.TrimSpace(os.Getenv("TELEMETRY_CENTRIFUGO_PROXY_SECRET"))) < 32 {
		return nil, nil, nil, errors.New("Centrifugo subscribe proxy secret must be at least 32 bytes")
	}
	relayContext, relayCancel := context.WithCancel(context.Background())
	return service, relayContext, relayCancel, nil
}

func runRealtimeRelay(ctx context.Context, service *telemetry.RealtimeService, logger *slog.Logger) {
	if service == nil {
		return
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	lastFailureLog := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			published, err := service.RelayOnce(ctx, 64)
			if err != nil {
				if time.Since(lastFailureLog) >= time.Second {
					logger.Warn("telemetry_realtime_relay_failed", "error_code", "TELEMETRY_REALTIME_RELAY_FAILED")
					lastFailureLog = time.Now()
				}
				continue
			}
			if published > 0 {
				logger.Info("telemetry_realtime_relay_batch_published", "publication_count", published)
			}
		}
	}
}

func loadAlarmEvaluationRelay(store *telemetry.PostgresStore, certificate tls.Certificate) (*telemetry.AlarmEvaluationRelay, context.Context, context.CancelFunc, error) {
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("TELEMETRY_ALARM_EVALUATION_ENABLED")), "true") {
		return nil, context.Background(), nil, nil
	}
	alarmCAs, err := loadCertPool(requiredEnv("TELEMETRY_ALARM_CA"))
	if err != nil {
		return nil, nil, nil, err
	}
	client := &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			TLSClientConfig: &tls.Config{
				MinVersion: tls.VersionTLS13, RootCAs: alarmCAs, Certificates: []tls.Certificate{certificate},
				ServerName: strings.TrimSpace(os.Getenv("TELEMETRY_ALARM_SERVER_NAME")),
			},
			DisableCompression: true,
			ForceAttemptHTTP2:  false,
		},
	}
	transport, err := telemetry.NewHTTPAlarmEvaluationTransport(telemetry.HTTPAlarmEvaluationTransportConfig{
		Endpoint: requiredEnv("TELEMETRY_ALARM_EVALUATION_URL"), HTTPClient: client,
	})
	if err != nil {
		return nil, nil, nil, err
	}
	workerID := strings.TrimSpace(os.Getenv("TELEMETRY_ALARM_RELAY_WORKER_ID"))
	if workerID == "" {
		hostname, _ := os.Hostname()
		workerID = "telemetry-alarm:" + hostname
	}
	relay, err := telemetry.NewAlarmEvaluationRelay(telemetry.AlarmEvaluationRelayConfig{
		Repository: store, Transport: transport, WorkerID: workerID,
		LeaseDuration: durationEnv("TELEMETRY_ALARM_RELAY_LEASE_DURATION", 30*time.Second, 5*time.Second, 5*time.Minute),
		RetryDelay:    durationEnv("TELEMETRY_ALARM_RELAY_RETRY_DELAY", 5*time.Second, time.Second, time.Hour),
	})
	if err != nil {
		return nil, nil, nil, err
	}
	relayContext, relayCancel := context.WithCancel(context.Background())
	return relay, relayContext, relayCancel, nil
}

func runAlarmEvaluationRelay(ctx context.Context, relay *telemetry.AlarmEvaluationRelay, logger *slog.Logger) {
	if relay == nil {
		return
	}
	pollInterval := durationEnv("TELEMETRY_ALARM_RELAY_POLL_INTERVAL", 100*time.Millisecond, 25*time.Millisecond, time.Minute)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	lastFailureLog := time.Time{}
	logger.Info("telemetry_alarm_evaluation_relay_started", "poll_interval", pollInterval.String())
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			relayContext, cancel := context.WithTimeout(ctx, 15*time.Second)
			delivered, err := relay.RelayOnce(relayContext, 64)
			cancel()
			if err != nil {
				if time.Since(lastFailureLog) >= time.Second {
					logger.Warn("telemetry_alarm_evaluation_relay_failed", "error_code", "TELEMETRY_ALARM_EVALUATION_RELAY_FAILED")
					lastFailureLog = time.Now()
				}
				continue
			}
			if delivered > 0 {
				logger.Info("telemetry_alarm_evaluation_batch_delivered", "publication_count", delivered)
			}
		}
	}
}

func loadCertPool(path string) (*x509.CertPool, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(content) {
		return nil, errors.New("certificate pool is empty")
	}
	return pool, nil
}

func loadCertificatePublicKey(path string) (any, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(content)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("IAM grant certificate is invalid")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}
	return certificate.PublicKey, nil
}

// loadTelemetryRateLimiter uses a shared Redis counter when configured so replica rate limits stay consistent.
// Without Redis, single-node/dev keeps the existing in-memory counter.
func loadTelemetryRateLimiter() (*limitpolicy.Limiter, func(), error) {
	rawURL := strings.TrimSpace(os.Getenv("TELEMETRY_LIMIT_POLICY_REDIS_URL"))
	if rawURL == "" {
		return limitpolicy.NewLimiter(limitpolicy.NewMemoryCounter(100000), telemetryLimitPolicy()), func() {}, nil
	}
	options, err := redis.ParseURL(rawURL)
	if err != nil {
		return nil, func() {}, err
	}
	client := redis.NewClient(options)
	return limitpolicy.NewLimiter(limitpolicy.NewRedisCounter(client, "hvac:telemetry:limit"), telemetryLimitPolicy()), func() { _ = client.Close() }, nil
}

// telemetryLimitPolicy keeps telemetry ingest fail-open: Redis loss must not drop source observations.
func telemetryLimitPolicy() *limitpolicy.Policy {
	return &limitpolicy.Policy{
		Version: 1,
		Limits: []limitpolicy.Limit{
			{Dimension: limitpolicy.DimensionTelemetryIngest, Window: time.Minute, Burst: 6000, FailClosed: false},
		},
	}
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func commaSeparated(raw string) []string {
	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			values = append(values, value)
		}
	}
	return values
}

func requiredEnv(name string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		_, _ = os.Stderr.WriteString(name + " is required\n")
		os.Exit(1)
	}
	return value
}

func integerEnv(name string, fallback, minimum, maximum int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < minimum || parsed > maximum {
		_, _ = os.Stderr.WriteString(name + " is invalid\n")
		os.Exit(1)
	}
	return parsed
}

func durationEnv(name string, fallback, minimum, maximum time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed < minimum || parsed > maximum {
		_, _ = os.Stderr.WriteString(name + " is invalid\n")
		os.Exit(1)
	}
	return parsed
}
