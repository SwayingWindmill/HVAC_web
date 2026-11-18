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
	"sync"
	"syscall"
	"time"

	"github.com/quanlaihe/hvac-web/libs/limitpolicy"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/modules/energy/pkg/analyticsprojector"
	"github.com/quanlaihe/hvac-web/modules/telemetry/pkg/telemetry"
	"github.com/redis/go-redis/v9"
)

func main() {
	if code := runTelemetryWorker(); code != 0 {
		os.Exit(code)
	}
}

type telemetryWorkerShutdown struct {
	Observability    *observability.Runtime
	Logger           *slog.Logger
	Diagnostics      *http.Server
	Server           *http.Server
	CancelWorkers    func()
	WorkersWG        *sync.WaitGroup
	DrainRelays      func(context.Context)
	CancelAnalytics  func()
	AnalyticsWG      *sync.WaitGroup
	HTTPTimeout      time.Duration
	WorkerTimeout    time.Duration
	DrainTimeout     time.Duration
	TelemetryTimeout time.Duration
	CloseHistory     func()
	CloseLatestCache func()
	CloseRateLimiter func()
	CloseStore       func()
}

func shutdownTelemetryWorker(cfg telemetryWorkerShutdown) {
	if cfg.Observability != nil {
		cfg.Observability.MarkNotReady()
	}
	httpTimeout := cfg.HTTPTimeout
	if httpTimeout <= 0 {
		httpTimeout = 8 * time.Second
	}
	if cfg.Diagnostics != nil || cfg.Server != nil {
		httpCtx, cancelHTTP := context.WithTimeout(context.Background(), httpTimeout)
		var httpWG sync.WaitGroup
		for _, srv := range []*http.Server{cfg.Diagnostics, cfg.Server} {
			if srv == nil {
				continue
			}
			httpWG.Add(1)
			go func(s *http.Server) {
				defer httpWG.Done()
				_ = s.Shutdown(httpCtx)
			}(srv)
		}
		httpWG.Wait()
		cancelHTTP()
	}
	if cfg.CancelWorkers != nil {
		cfg.CancelWorkers()
	}
	workerTimeout := cfg.WorkerTimeout
	if workerTimeout <= 0 {
		workerTimeout = 8 * time.Second
	}
	if !waitWorkersBounded(cfg.WorkersWG, workerTimeout) && cfg.Logger != nil {
		cfg.Logger.Warn("telemetry_worker_shutdown_timed_out", "error_code", "TELEMETRY_WORKER_SHUTDOWN_TIMEOUT")
	}
	if cfg.DrainRelays != nil {
		drainTimeout := cfg.DrainTimeout
		if drainTimeout <= 0 {
			drainTimeout = 8 * time.Second
		}
		drainCtx, cancelDrain := context.WithTimeout(context.Background(), drainTimeout)
		cfg.DrainRelays(drainCtx)
		cancelDrain()
	}
	if cfg.CancelAnalytics != nil {
		cfg.CancelAnalytics()
	}
	if !waitWorkersBounded(cfg.AnalyticsWG, workerTimeout) && cfg.Logger != nil {
		cfg.Logger.Warn("telemetry_analytics_shutdown_timed_out", "error_code", "TELEMETRY_ANALYTICS_SHUTDOWN_TIMEOUT")
	}
	if cfg.CloseHistory != nil {
		cfg.CloseHistory()
	}
	if cfg.CloseLatestCache != nil {
		cfg.CloseLatestCache()
	}
	if cfg.CloseRateLimiter != nil {
		cfg.CloseRateLimiter()
	}
	if cfg.CloseStore != nil {
		cfg.CloseStore()
	}
	if cfg.Observability != nil {
		telemetryTimeout := cfg.TelemetryTimeout
		if telemetryTimeout <= 0 {
			telemetryTimeout = 2 * time.Second
		}
		shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), telemetryTimeout)
		_ = cfg.Observability.Shutdown(shutdownContext)
		cancelShutdown()
	}
}

func waitWorkersBounded(wg *sync.WaitGroup, timeout time.Duration) bool {
	if wg == nil {
		return true
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		wg.Wait()
	}()
	if timeout <= 0 {
		<-done
		return true
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

func runTelemetryWorker() int {
	observabilityRuntime := observability.NewRuntime(observability.RuntimeConfig{
		Service: "telemetry-runtime-service", OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
		QueueSize: 1024, ExportTimeout: 500 * time.Millisecond,
	})
	logger := observability.NewJSONLogger(os.Stdout, slog.LevelInfo)
	var workersWG sync.WaitGroup
	var analyticsWG sync.WaitGroup
	var workerCancels []context.CancelFunc
	shutdownCfg := telemetryWorkerShutdown{
		Observability: observabilityRuntime,
		Logger:        logger,
		WorkersWG:     &workersWG,
		AnalyticsWG:   &analyticsWG,
		CancelWorkers: func() {
			for _, cancel := range workerCancels {
				if cancel != nil {
					cancel()
				}
			}
		},
		HTTPTimeout:      8 * time.Second,
		WorkerTimeout:    8 * time.Second,
		DrainTimeout:     8 * time.Second,
		TelemetryTimeout: 2 * time.Second,
	}
	var shutdownOnce sync.Once
	runShutdown := func() {
		shutdownOnce.Do(func() {
			shutdownTelemetryWorker(shutdownCfg)
		})
	}
	defer runShutdown()

	certificate, err := tls.LoadX509KeyPair(requiredEnv("TELEMETRY_TLS_CERT"), requiredEnv("TELEMETRY_TLS_KEY"))
	if err != nil {
		logger.Error("telemetry_tls_identity_load_failed", "error_code", "TELEMETRY_TLS_IDENTITY_LOAD_FAILED")
		return 1
	}
	clientCAs, err := loadCertPool(requiredEnv("TELEMETRY_CLIENT_CA"))
	if err != nil {
		logger.Error("telemetry_client_ca_invalid", "error_code", "TELEMETRY_CLIENT_CA_INVALID")
		return 1
	}
	iamCAs, err := loadCertPool(requiredEnv("TELEMETRY_IAM_CA"))
	if err != nil {
		logger.Error("telemetry_iam_ca_invalid", "error_code", "TELEMETRY_IAM_CA_INVALID")
		return 1
	}
	iamGrantPublicKey, err := loadCertificatePublicKey(requiredEnv("TELEMETRY_IAM_GRANT_CERT"))
	if err != nil {
		logger.Error("telemetry_iam_grant_certificate_invalid", "error_code", "TELEMETRY_IAM_GRANT_CERTIFICATE_INVALID")
		return 1
	}

	poolConfig, err := parseTelemetryPostgresPoolConfig(os.Getenv)
	if err != nil {
		logger.Error("telemetry_store_pool_config_invalid", "error_code", "TELEMETRY_STORE_POOL_CONFIG_INVALID")
		return 1
	}

	openContext, cancelOpen := context.WithTimeout(context.Background(), 5*time.Second)
	store, err := telemetry.OpenPostgresStoreWithConfig(openContext, requiredEnv("TELEMETRY_DATABASE_URL"), poolConfig)
	cancelOpen()
	if err != nil {
		logger.Error("telemetry_store_open_failed", "error_code", "TELEMETRY_STORE_OPEN_FAILED")
		return 1
	}
	shutdownCfg.CloseStore = store.Close
	pipelineMetrics := telemetry.NewPipelineMetrics(observabilityRuntime.Metrics)
	store.SetPipelineMetrics(pipelineMetrics)
	retentionContext, retentionCancel := context.WithCancel(context.Background())
	workerCancels = append(workerCancels, retentionCancel)
	runSupervised(retentionContext, &workersWG, observabilityRuntime, logger, "telemetry.runtime_retention", func() {
		runRuntimeRetention(retentionContext, store, logger)
	})
	runSupervised(retentionContext, &workersWG, observabilityRuntime, logger, "telemetry.presence_sweep", func() {
		runPresenceSweep(retentionContext, store, time.Minute, logger)
	})
	runSupervised(retentionContext, &workersWG, observabilityRuntime, logger, "telemetry.probe_offset_sweep", func() {
		runProbeOffsetSweep(retentionContext, store, 5*time.Second, logger)
	})
	clickHousePruner, clickHouseRetentionCtx, clickHouseRetentionCancel, err := loadClickHouseRetentionPruner(pipelineMetrics)
	if err != nil {
		logger.Error("telemetry_clickhouse_retention_configuration_invalid", "error_code", "TELEMETRY_CLICKHOUSE_RETENTION_CONFIGURATION_INVALID")
		return 1
	}
	if clickHouseRetentionCancel != nil {
		workerCancels = append(workerCancels, clickHouseRetentionCancel)
		clickHouseRetentionInterval := durationEnv("TELEMETRY_RETENTION_CLICKHOUSE_INTERVAL", time.Hour, time.Minute, 24*time.Hour)
		runSupervised(clickHouseRetentionCtx, &workersWG, observabilityRuntime, logger, "telemetry.clickhouse_retention", func() {
			runClickHouseRetention(clickHouseRetentionCtx, clickHousePruner, clickHouseRetentionInterval, logger)
		})
	}

	latestCache, latestRelay, latestContext, latestCancel, err := loadLatestCache(store, pipelineMetrics)
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
		shutdownCfg.CloseLatestCache = func() { _ = latestCache.Close() }
	}
	observabilityRuntime.SetDependencies(dependencies...)
	if latestCancel != nil {
		workerCancels = append(workerCancels, latestCancel)
		runSupervised(latestContext, &workersWG, observabilityRuntime, logger, "telemetry.latest_cache_relay", func() {
			runLatestCacheRelay(latestContext, latestRelay, logger)
		})
	}

	historyRepository, historyRelay, historySettings, historyContext, historyCancel, err := loadHistoryProjection(pipelineMetrics)
	if err != nil {
		logger.Error("telemetry_history_projection_configuration_invalid", "error_code", "TELEMETRY_HISTORY_PROJECTION_CONFIGURATION_INVALID")
		return 1
	}
	if historyRepository != nil {
		shutdownCfg.CloseHistory = historyRepository.Close
	}
	if historyCancel != nil {
		workerCancels = append(workerCancels, historyCancel)
		runSupervised(historyContext, &workersWG, observabilityRuntime, logger, "telemetry.history_projection", func() {
			runHistoryProjection(historyContext, historyRelay, historySettings, logger)
		})
	}

	analyticsProjector, priceWindows, totalSubmeters, analyticsContext, analyticsCancel, err := loadAnalyticsProjection(certificate)
	if err != nil {
		logger.Error("analytics_projection_configuration_invalid", "error_code", "ANALYTICS_PROJECTION_CONFIGURATION_INVALID")
		return 1
	}
	if analyticsCancel != nil {
		shutdownCfg.CancelAnalytics = analyticsCancel
		runSupervised(analyticsContext, &analyticsWG, observabilityRuntime, logger, "telemetry.analytics_projection", func() {
			runAnalyticsProjection(analyticsContext, analyticsProjector, observabilityRuntime, logger)
		})
		runSupervised(analyticsContext, &analyticsWG, observabilityRuntime, logger, "telemetry.price_window_projection", func() {
			runPriceWindowProjection(analyticsContext, priceWindows, observabilityRuntime, logger)
		})
		runSupervised(analyticsContext, &analyticsWG, observabilityRuntime, logger, "telemetry.total_submeter_projection", func() {
			runTotalSubmeterProjection(analyticsContext, totalSubmeters, observabilityRuntime, logger)
		})
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
		return 1
	}

	realtimeService, realtimeContext, realtimeCancel, err := loadRealtimeService(store, certificate, observabilityRuntime.Metrics)
	if err != nil {
		logger.Error("telemetry_realtime_configuration_invalid", "error_code", "TELEMETRY_REALTIME_CONFIGURATION_INVALID")
		return 1
	}
	if realtimeCancel != nil {
		workerCancels = append(workerCancels, realtimeCancel)
		runSupervised(realtimeContext, &workersWG, observabilityRuntime, logger, "telemetry.realtime_relay", func() {
			runRealtimeRelay(realtimeContext, realtimeService, logger)
		})
		revocationSource, err := telemetry.NewHTTPRevocationSource(requiredEnv("TELEMETRY_IAM_ENDPOINT"), iamClient)
		if err != nil {
			logger.Error("telemetry_revocation_source_invalid", "error_code", "TELEMETRY_REVOCATION_SOURCE_INVALID")
			return 1
		}
		revocationRelay, err := telemetry.NewRevocationRelay(revocationSource, store, realtimeService, time.Now)
		if err != nil {
			logger.Error("telemetry_revocation_relay_invalid", "error_code", "TELEMETRY_REVOCATION_RELAY_INVALID")
			return 1
		}
		runSupervised(realtimeContext, &workersWG, observabilityRuntime, logger, "telemetry.revocation_relay", func() {
			runRevocationRelay(realtimeContext, revocationRelay, logger)
		})
	}

	alarmRelay, alarmContext, alarmCancel, err := loadAlarmEvaluationRelay(store, certificate)
	if err != nil {
		logger.Error("telemetry_alarm_evaluation_configuration_invalid", "error_code", "TELEMETRY_ALARM_EVALUATION_CONFIGURATION_INVALID")
		return 1
	}
	if alarmCancel != nil {
		workerCancels = append(workerCancels, alarmCancel)
		runSupervised(alarmContext, &workersWG, observabilityRuntime, logger, "telemetry.alarm_evaluation_relay", func() {
			runAlarmEvaluationRelay(alarmContext, alarmRelay, logger)
		})
	}

	var drainTargets telemetryRelayDrainTargets
	if latestRelay != nil {
		drainTargets.LatestCache = latestRelay
	}
	if realtimeService != nil {
		drainTargets.Realtime = realtimeService
	}
	if alarmRelay != nil {
		drainTargets.Alarm = alarmRelay
	}
	if historyRelay != nil {
		drainTargets.History = historyRelay
	}
	shutdownCfg.DrainRelays = func(drainCtx context.Context) {
		drainTelemetryRelays(drainCtx, drainTargets, logger)
	}

	rateLimiter, closeRateLimiter, err := loadTelemetryRateLimiter()
	if err != nil {
		logger.Error("telemetry_limit_policy_config_invalid", "error_code", "TELEMETRY_LIMIT_POLICY_CONFIG_INVALID")
		return 1
	}
	shutdownCfg.CloseRateLimiter = closeRateLimiter

	server := &http.Server{
		Addr: envOr("TELEMETRY_SERVICE_ADDR", "127.0.0.1:18446"),
		Handler: telemetry.NewHandler(telemetry.ServerConfig{
			Store: store, LatestCache: latestCache, Authorizer: authorizer,
			AllowedGatewaySPIFFE: envOr("TELEMETRY_ALLOWED_GATEWAY_SPIFFE", "spiffe://hvac.local/platform-gateway"),
			AllowedSnapshotReaderSPIFFEs: commaSeparated(envOr(
				"TELEMETRY_ALLOWED_SNAPSHOT_READER_SPIFFES",
				"spiffe://hvac.local/operations-agent-service",
			)),
			RuntimeAudience:               envOr("TELEMETRY_GRANT_AUDIENCE", "telemetry-runtime-service"),
			HistoricalObservationAcceptor: store,
			AllowedHistoricalReplaySPIFFE: envOr("TELEMETRY_ALLOWED_HISTORICAL_REPLAY_SPIFFE", "spiffe://hvac.local/historical-replay-runner"),
			ObservationAcceptor:           store, CoverageAcceptor: store, MQTTEvidenceAcceptor: store,
			AllowedSourceSPIFFE:              envOr("TELEMETRY_ALLOWED_SOURCE_SPIFFE", "spiffe://hvac.local/mqtt-telemetry-adapter"),
			Realtime:                         realtimeService,
			AllowedCentrifugoSPIFFE:          envOr("TELEMETRY_ALLOWED_CENTRIFUGO_SPIFFE", "spiffe://hvac.local/centrifugo"),
			CentrifugoProxySecret:            strings.TrimSpace(os.Getenv("TELEMETRY_CENTRIFUGO_PROXY_SECRET")),
			AllowedIAMSPIFFE:                 envOr("TELEMETRY_ALLOWED_IAM_SPIFFE", "spiffe://hvac.local/iam-service"),
			AllowedCommandVerifierSPIFFE:     envOr("TELEMETRY_ALLOWED_COMMAND_VERIFIER_SPIFFE", "spiffe://hvac.local/command-verifier"),
			AllowedCommandDispatcherSPIFFE:   envOr("TELEMETRY_ALLOWED_COMMAND_DISPATCHER_SPIFFE", "spiffe://hvac.local/command-dispatcher"),
			AllowedRegistrySyncSPIFFEs:       commaSeparated(envOr("TELEMETRY_ALLOWED_REGISTRY_SYNC_SPIFFES", "spiffe://hvac.local/platform-core-service,spiffe://hvac.local/platform-gateway")),
			RegistrySyncer:                   store,
			PointHealthInspector:             store,
			AllowedCommandServiceSPIFFE:      envOr("TELEMETRY_ALLOWED_COMMAND_SERVICE_SPIFFE", "spiffe://hvac.local/command-service"),
			AllowedOptimizationServiceSPIFFE: envOr("TELEMETRY_ALLOWED_OPTIMIZATION_SERVICE_SPIFFE", telemetry.DefaultOptimizationServiceSPIFFE),
			Metrics:                          observabilityRuntime.Metrics,
			Tracer:                           observabilityRuntime.Tracer,
			RateLimiter:                      rateLimiter,
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
	shutdownCfg.Diagnostics = diagnostics
	shutdownCfg.Server = server
	observabilityRuntime.Go(logger, "telemetry.diagnostics", func() {
		if err := diagnostics.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("telemetry_diagnostics_stopped_unexpectedly", "error_code", "TELEMETRY_DIAGNOSTICS_SERVE_FAILED")
		}
	})
	serverErr := make(chan error, 1)
	observabilityRuntime.Go(logger, "telemetry.http", func() {
		logger.Info("telemetry_runtime_started", "service", "telemetry-runtime-service", "address", server.Addr)
		if serveErr := server.ListenAndServeTLS("", ""); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			serverErr <- serveErr
			return
		}
		serverErr <- nil
	})
	observabilityRuntime.MarkReady()

	signalCtx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()

	exitCode := 0
	select {
	case <-signalCtx.Done():
	case serveErr := <-serverErr:
		if serveErr != nil {
			logger.Error("telemetry_runtime_stopped_unexpectedly", "error_code", "TELEMETRY_RUNTIME_SERVE_FAILED")
			exitCode = 1
		}
	}
	stopSignals()
	runShutdown()
	if exitCode == 0 {
		logger.Info("telemetry_runtime_stopped", "service", "telemetry-runtime-service")
	}
	return exitCode
}

func runSupervised(ctx context.Context, wg *sync.WaitGroup, runtime *observability.Runtime, logger *slog.Logger, component string, fn func()) {
	if fn == nil {
		return
	}
	var metrics *observability.Registry
	if runtime != nil {
		metrics = runtime.Metrics
	}
	if wg != nil {
		wg.Add(1)
	}
	runtime.Go(logger, component, func() {
		if wg != nil {
			defer wg.Done()
		}
		for ctx.Err() == nil {
			panicked := false
			func() {
				defer observability.Recover(logger, metrics, component, func(any) {
					panicked = true
				})
				fn()
			}()
			if !panicked || ctx.Err() != nil {
				return
			}
			timer := time.NewTimer(time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	})
}

func loadLatestCache(store *telemetry.PostgresStore, metrics *telemetry.PipelineMetrics) (telemetry.LatestCache, *telemetry.LatestCacheRelay, context.Context, context.CancelFunc, error) {
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
		Metrics:   metrics,
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

type historyProjectionSettings struct {
	BatchSize     int
	LeaseFor      time.Duration
	RetryAfter    time.Duration
	PollInterval  time.Duration
	PipelineDepth int
}

func parseHistoryProjectionSettings(lookup func(string) string) (historyProjectionSettings, error) {
	batchSize, err := parseBoundedIntEnv(lookup, "TELEMETRY_HISTORY_BATCH_SIZE", 4096, 1, 32768)
	if err != nil {
		return historyProjectionSettings{}, err
	}
	leaseFor, err := parseBoundedDurationEnv(lookup, "TELEMETRY_HISTORY_LEASE_DURATION", 30*time.Second, time.Second, 10*time.Minute)
	if err != nil {
		return historyProjectionSettings{}, err
	}
	retryAfter, err := parseBoundedDurationEnv(lookup, "TELEMETRY_HISTORY_RETRY_DELAY", 5*time.Second, time.Second, time.Hour)
	if err != nil {
		return historyProjectionSettings{}, err
	}
	pollInterval, err := parseBoundedDurationEnv(lookup, "TELEMETRY_HISTORY_POLL_INTERVAL", 5*time.Second, 25*time.Millisecond, time.Minute)
	if err != nil {
		return historyProjectionSettings{}, err
	}
	pipelineDepth, err := parseBoundedIntEnv(lookup, "TELEMETRY_HISTORY_PIPELINE_DEPTH", 2, 1, 4)
	if err != nil {
		return historyProjectionSettings{}, err
	}
	return historyProjectionSettings{
		BatchSize:     batchSize,
		LeaseFor:      leaseFor,
		RetryAfter:    retryAfter,
		PollInterval:  pollInterval,
		PipelineDepth: pipelineDepth,
	}, nil
}

func loadHistoryProjection(metrics *telemetry.PipelineMetrics) (*telemetry.HistoryPostgresRepository, *telemetry.HistoryRelay, historyProjectionSettings, context.Context, context.CancelFunc, error) {
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("TELEMETRY_HISTORY_PROJECTION_ENABLED")), "true") {
		return nil, nil, historyProjectionSettings{}, context.Background(), nil, nil
	}
	settings, err := parseHistoryProjectionSettings(os.Getenv)
	if err != nil {
		return nil, nil, historyProjectionSettings{}, nil, nil, err
	}
	openContext, cancelOpen := context.WithTimeout(context.Background(), 5*time.Second)
	repository, err := telemetry.OpenHistoryPostgresRepository(openContext, requiredEnv("TELEMETRY_HISTORY_DATABASE_URL"))
	cancelOpen()
	if err != nil {
		return nil, nil, historyProjectionSettings{}, nil, nil, err
	}
	sink, err := telemetry.NewClickHouseHistorySink(telemetry.ClickHouseHistoryConfig{
		BaseURL:  requiredEnv("TELEMETRY_CLICKHOUSE_HTTP_URL"),
		Database: envOr("TELEMETRY_CLICKHOUSE_DATABASE", "telemetry_history"),
		Table:    envOr("TELEMETRY_CLICKHOUSE_TABLE", "observations"),
		Username: envOr("TELEMETRY_CLICKHOUSE_USERNAME", "telemetry_history_writer"),
		Password: os.Getenv("TELEMETRY_CLICKHOUSE_PASSWORD"),
	})
	if err != nil {
		repository.Close()
		return nil, nil, historyProjectionSettings{}, nil, nil, err
	}
	relay, err := telemetry.NewHistoryRelay(telemetry.HistoryRelayConfig{
		Repository:    repository,
		Sink:          sink,
		BatchSize:     settings.BatchSize,
		LeaseFor:      settings.LeaseFor,
		RetryAfter:    settings.RetryAfter,
		PipelineDepth: settings.PipelineDepth,
		Metrics:       metrics,
	})
	if err != nil {
		repository.Close()
		return nil, nil, historyProjectionSettings{}, nil, nil, err
	}
	relayContext, relayCancel := context.WithCancel(context.Background())
	return repository, relay, settings, relayContext, relayCancel, nil
}

func runHistoryProjection(ctx context.Context, relay *telemetry.HistoryRelay, settings historyProjectionSettings, logger *slog.Logger) {
	if relay == nil {
		return
	}
	pollInterval := settings.PollInterval
	if pollInterval <= 0 {
		pollInterval = 5 * time.Second
	}
	lastFailureLog := time.Time{}
	logger.Info("telemetry_history_projection_started", "poll_interval", pollInterval.String(), "pipeline_depth", settings.PipelineDepth)
	relay.Run(ctx, pollInterval, func(published int, err error) {
		if err != nil {
			if time.Since(lastFailureLog) >= time.Second {
				logger.Warn("telemetry_history_projection_failed", "error_code", "TELEMETRY_HISTORY_PROJECTION_FAILED")
				lastFailureLog = time.Now()
			}
			return
		}
		if published > 0 {
			logger.Info("telemetry_history_batch_projected", "observation_count", published)
		}
	})
}

func loadAnalyticsProjection(certificate tls.Certificate) (*analyticsprojector.Projector, *analyticsprojector.PriceWindowProjector, *analyticsprojector.TotalSubmeterProjector, context.Context, context.CancelFunc, error) {
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("ANALYTICS_PROJECTION_ENABLED")), "true") {
		return nil, nil, nil, context.Background(), nil, nil
	}
	httpClient, err := newAnalyticsHTTPClient(strings.TrimSpace(os.Getenv("ANALYTICS_CLICKHOUSE_CA")))
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	baseURL := requiredEnv("ANALYTICS_CLICKHOUSE_HTTP_URL")
	reader, err := analyticsprojector.NewReader(analyticsprojector.ReaderConfig{
		BaseURL:           baseURL,
		SourceDatabase:    envOr("ANALYTICS_SOURCE_DATABASE", "telemetry_history"),
		AnalyticsDatabase: envOr("ANALYTICS_DATABASE", "analytics"),
		AnalyticsTable:    envOr("ANALYTICS_ENERGY_TABLE", "energy_interval_facts"),
		Username:          strings.TrimSpace(os.Getenv("ANALYTICS_CLICKHOUSE_READER_USERNAME")),
		Password:          os.Getenv("ANALYTICS_CLICKHOUSE_READER_PASSWORD"),
		HTTPClient:        httpClient,
	})
	if err != nil {
		return nil, nil, nil, nil, nil, err
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
		return nil, nil, nil, nil, nil, err
	}
	internalRoots, err := loadCertPool(requiredEnv("ANALYTICS_INTERNAL_CA"))
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	internalClient := &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{
			Proxy:              http.ProxyFromEnvironment,
			TLSClientConfig:    &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: internalRoots, Certificates: []tls.Certificate{certificate}},
			DisableCompression: true,
		},
	}
	registryConfig := analyticsprojector.RegistryClientConfig{
		BaseURL: requiredEnv("ANALYTICS_CORE_REGISTRY_URL"), IAMURL: requiredEnv("ANALYTICS_IAM_URL"), HTTPClient: internalClient,
	}
	bindingResolver, err := analyticsprojector.NewBindingResolver(registryConfig)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	projector, err := analyticsprojector.NewProjector(analyticsprojector.ProjectorConfig{
		CounterSource: reader, BindingResolver: bindingResolver, FactSink: writer, RebuildEvents: writer, BatchSize: integerEnv("ANALYTICS_PROJECTOR_BATCH_SIZE", 256, 1, 4096),
	})
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	tariffs, err := analyticsprojector.NewTariffSource(registryConfig)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	priceWindows, err := analyticsprojector.NewPriceWindowProjector(analyticsprojector.PriceWindowProjectorConfig{Reader: reader, Writer: writer, Tariffs: tariffs})
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	submeterSources, err := analyticsprojector.NewTotalSubmeterSource(registryConfig)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	totalSubmeters, err := analyticsprojector.NewTotalSubmeterProjector(analyticsprojector.TotalSubmeterProjectorConfig{Reader: reader, Writer: writer, Sources: submeterSources})
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	projectionContext, projectionCancel := context.WithCancel(context.Background())
	return projector, priceWindows, totalSubmeters, projectionContext, projectionCancel, nil
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
				_ = runtime.Metrics.AddCounter("hvac_analytics_energy_intervals_projected_total", "Projected additive energy intervals.", map[string]string{"model": "energy_interval_facts"}, float64(projected))
				logger.Info("analytics_energy_intervals_projected", "interval_count", projected)
			}
		}
	}
}

// runPriceWindowProjection keeps every priced Site's Tariff laid out as price windows for Cube
// (#575). Tariffs change rarely, so it runs at start and then every few minutes.
func runPriceWindowProjection(ctx context.Context, projector *analyticsprojector.PriceWindowProjector, runtime *observability.Runtime, logger *slog.Logger) {
	interval := durationEnv("ANALYTICS_PRICE_WINDOW_INTERVAL", 5*time.Minute, 10*time.Second, time.Hour)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		syncContext, cancel := context.WithTimeout(ctx, time.Minute)
		err := projector.SyncOnce(syncContext)
		cancel()
		if err != nil {
			_ = runtime.Metrics.AddCounter("hvac_analytics_projection_failures_total", "Failed analytics read-model projection batches.", map[string]string{"model": "tariff_price_windows"}, 1)
			logger.Warn("analytics_price_window_projection_failed", "error_code", "ANALYTICS_PRICE_WINDOW_PROJECTION_FAILED", "error", err.Error())
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func runTotalSubmeterProjection(ctx context.Context, projector *analyticsprojector.TotalSubmeterProjector, runtime *observability.Runtime, logger *slog.Logger) {
	if projector == nil {
		return
	}
	interval := durationEnv("ANALYTICS_TOTAL_SUBMETER_INTERVAL", time.Minute, 10*time.Second, time.Hour)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		syncContext, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := projector.SyncOnce(syncContext)
		cancel()
		if err != nil {
			_ = runtime.Metrics.AddCounter("hvac_analytics_projection_failures_total", "Failed analytics read-model projection batches.", map[string]string{"model": "site_total_submeters"}, 1)
			logger.Warn("analytics_total_submeter_projection_failed", "error_code", "ANALYTICS_TOTAL_SUBMETER_PROJECTION_FAILED")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
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
	return &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			TLSClientConfig:       tlsConfig,
			MaxIdleConns:          64,
			MaxIdleConnsPerHost:   32,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: time.Second,
		},
	}, nil
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
		return nil, nil, nil, errors.New("centrifugo subscribe proxy secret must be at least 32 bytes")
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

// runRuntimeRetention prunes superseded presence signals, delivered publications and history
// events, and week-old received observations, which nothing reads once past their window.
func runRuntimeRetention(ctx context.Context, store *telemetry.PostgresStore, logger *slog.Logger) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		pruned, err := store.PruneRuntimeHistory(ctx, time.Now())
		switch {
		case err != nil && ctx.Err() == nil:
			logger.Warn("telemetry_runtime_retention_failed", "error_code", "TELEMETRY_RUNTIME_RETENTION_FAILED")
		case pruned != (telemetry.PrunedRuntimeHistory{}):
			logger.Info("telemetry_runtime_history_pruned",
				"presence_signals", pruned.PresenceSignals,
				"publications", pruned.Publications,
				"dead_publications", pruned.DeadPublications,
				"history_events", pruned.HistoryEvents,
				"dead_history_events", pruned.DeadHistoryEvents,
				"source_observations", pruned.SourceObservations,
				"mqtt_runtime_events", pruned.MQTTRuntimeEvents,
				"ingest_quarantines", pruned.IngestQuarantines,
				"recovery_cursors", pruned.RecoveryCursors,
				"subscriptions", pruned.Subscriptions,
				"replay_positions", pruned.ReplayPositions)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

type telemetryRelayBatchDrainer interface {
	RelayOnce(context.Context, int) (int, error)
}

type telemetryHistoryRelayDrainer interface {
	RelayOnce(context.Context) (int, error)
}

type telemetryRelayDrainTargets struct {
	LatestCache telemetryRelayBatchDrainer
	Realtime    telemetryRelayBatchDrainer
	Alarm       telemetryRelayBatchDrainer
	History     telemetryHistoryRelayDrainer
}

const (
	shutdownDrainBatchLimit = 64
	shutdownDrainMaxPasses  = 8
)

func drainBatchRelay(ctx context.Context, drainer telemetryRelayBatchDrainer, eventName, errorCode string, logger *slog.Logger) {
	if drainer == nil {
		return
	}
	for pass := 0; pass < shutdownDrainMaxPasses && ctx.Err() == nil; pass++ {
		published, err := drainer.RelayOnce(ctx, shutdownDrainBatchLimit)
		if err != nil {
			if logger != nil {
				logger.Warn(eventName, "error_code", errorCode)
			}
			return
		}
		if published < shutdownDrainBatchLimit {
			return
		}
	}
}

func drainTelemetryRelays(ctx context.Context, targets telemetryRelayDrainTargets, logger *slog.Logger) {
	drainBatchRelay(ctx, targets.LatestCache, "telemetry_latest_cache_shutdown_drain_failed", "TELEMETRY_LATEST_CACHE_DRAIN_FAILED", logger)
	drainBatchRelay(ctx, targets.Realtime, "telemetry_realtime_shutdown_drain_failed", "TELEMETRY_REALTIME_DRAIN_FAILED", logger)
	drainBatchRelay(ctx, targets.Alarm, "telemetry_alarm_shutdown_drain_failed", "TELEMETRY_ALARM_DRAIN_FAILED", logger)
	if targets.History != nil {
		for pass := 0; pass < shutdownDrainMaxPasses && ctx.Err() == nil; pass++ {
			published, err := targets.History.RelayOnce(ctx)
			if err != nil {
				if logger != nil {
					logger.Warn("telemetry_history_shutdown_drain_failed", "error_code", "TELEMETRY_HISTORY_DRAIN_FAILED")
				}
				return
			}
			if published == 0 {
				return
			}
		}
	}
}

func loadClickHouseRetentionPruner(metrics *telemetry.PipelineMetrics) (*telemetry.ClickHouseRetentionPruner, context.Context, context.CancelFunc, error) {
	return loadClickHouseRetentionPrunerFromEnvWithMetrics(os.Getenv, metrics)
}

func loadClickHouseRetentionPrunerFromEnv(lookup func(string) string) (*telemetry.ClickHouseRetentionPruner, context.Context, context.CancelFunc, error) {
	return loadClickHouseRetentionPrunerFromEnvWithMetrics(lookup, nil)
}

func loadClickHouseRetentionPrunerFromEnvWithMetrics(lookup func(string) string, metrics *telemetry.PipelineMetrics) (*telemetry.ClickHouseRetentionPruner, context.Context, context.CancelFunc, error) {
	username := strings.TrimSpace(lookup("TELEMETRY_RETENTION_CLICKHOUSE_USER"))
	if username == "" {
		return nil, context.Background(), nil, nil
	}
	baseURL := envOrLookup(lookup, "TELEMETRY_RETENTION_CLICKHOUSE_HTTP_URL", strings.TrimSpace(lookup("TELEMETRY_CLICKHOUSE_HTTP_URL")))
	if baseURL == "" {
		return nil, nil, nil, errors.New("TELEMETRY_CLICKHOUSE_HTTP_URL is required when ClickHouse retention is enabled")
	}
	password := lookup("TELEMETRY_RETENTION_CLICKHOUSE_PASSWORD")
	if password == "" {
		password = lookup("TELEMETRY_CLICKHOUSE_PASSWORD")
	}
	legalHolds, err := parseClickHouseLegalHolds(lookup("TELEMETRY_RETENTION_LEGAL_HOLDS"))
	if err != nil {
		return nil, nil, nil, err
	}
	archiveRequired, err := parseBoolEnv(lookup, "TELEMETRY_RETENTION_ARCHIVE_REQUIRED", false)
	if err != nil {
		return nil, nil, nil, err
	}
	archiveDir := strings.TrimSpace(lookup("TELEMETRY_COLD_ARCHIVE_DIR"))
	if archiveRequired && archiveDir == "" {
		return nil, nil, nil, errors.New("TELEMETRY_COLD_ARCHIVE_DIR is required when TELEMETRY_RETENTION_ARCHIVE_REQUIRED is enabled")
	}
	var archiveRunner telemetry.ColdArchiveExportRunner
	if archiveDir != "" {
		exporter, err := telemetry.NewColdArchiveExporter(telemetry.ColdArchiveConfig{
			BaseURL:    baseURL,
			Username:   username,
			Password:   password,
			ArchiveDir: archiveDir,
		})
		if err != nil {
			return nil, nil, nil, err
		}
		archiveRunner = exporter
	}
	pruner, err := telemetry.NewClickHouseRetentionPruner(telemetry.ClickHouseRetentionConfig{
		BaseURL:           baseURL,
		HistoryDatabase:   envOrLookup(lookup, "TELEMETRY_CLICKHOUSE_DATABASE", "telemetry_history"),
		AnalyticsDatabase: envOrLookup(lookup, "ANALYTICS_CLICKHOUSE_DATABASE", "analytics"),
		Username:          username,
		Password:          password,
		LegalHolds:        legalHolds,
		ArchiveExporter:   archiveRunner,
		ArchiveRequired:   archiveRequired,
		Metrics:           metrics,
	})
	if err != nil {
		return nil, nil, nil, err
	}
	retentionContext, retentionCancel := context.WithCancel(context.Background())
	return pruner, retentionContext, retentionCancel, nil
}

func parseClickHouseLegalHolds(raw string) ([]telemetry.ClickHouseLegalHoldScope, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	entries := strings.Split(trimmed, ",")
	holds := make([]telemetry.ClickHouseLegalHoldScope, 0, len(entries))
	for _, entry := range entries {
		item := strings.TrimSpace(entry)
		if item == "" {
			continue
		}
		tenantID, siteID, hasSite := strings.Cut(item, ":")
		scope := telemetry.ClickHouseLegalHoldScope{TenantID: strings.TrimSpace(tenantID)}
		if hasSite {
			scope.SiteID = strings.TrimSpace(siteID)
		}
		holds = append(holds, scope)
	}
	return holds, nil
}

type clickHouseRetentionExecutor interface {
	Prune(context.Context, time.Time) (telemetry.ClickHouseRetentionResult, error)
}

func runClickHouseRetention(ctx context.Context, pruner clickHouseRetentionExecutor, interval time.Duration, logger *slog.Logger) {
	if pruner == nil {
		return
	}
	if interval <= 0 {
		interval = time.Hour
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		pruneCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		result, err := pruner.Prune(pruneCtx, time.Now())
		cancel()
		switch {
		case err != nil && ctx.Err() == nil:
			if logger != nil {
				logger.Warn("telemetry_clickhouse_retention_failed", "error_code", "TELEMETRY_CLICKHOUSE_RETENTION_FAILED")
			}
		case len(result.SubmittedTables) > 0:
			if logger != nil {
				logger.Info(
					"telemetry_clickhouse_retention_submitted",
					"table_count", len(result.SubmittedTables),
					"archived_table_count", len(result.ArchivedTables),
				)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

type presenceSweeper interface {
	SweepDuePresence(ctx context.Context, evaluatedAt time.Time, limit int) (int, error)
}

// runPresenceSweep re-evaluates devices whose presence window elapsed without new own signals,
// so silent or copy-only devices transition to OFFLINE and publish snapshot deltas.
func runPresenceSweep(ctx context.Context, sweeper presenceSweeper, interval time.Duration, logger *slog.Logger) {
	if sweeper == nil {
		return
	}
	if interval <= 0 {
		interval = time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		sweepCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		swept, err := sweeper.SweepDuePresence(sweepCtx, time.Now(), 256)
		cancel()
		switch {
		case err != nil && ctx.Err() == nil:
			logger.Warn("telemetry_presence_sweep_failed", "error_code", "TELEMETRY_PRESENCE_SWEEP_FAILED")
		case swept > 0:
			logger.Info("telemetry_presence_swept", "device_count", swept)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

type probeOffsetSweeper interface {
	SweepProbeOffsets(ctx context.Context, evaluatedAt time.Time) (int, error)
}

// runProbeOffsetSweep periodically folds idle supply/return probe-offset windows outside
// the synchronous observation write path.
func runProbeOffsetSweep(ctx context.Context, sweeper probeOffsetSweeper, interval time.Duration, logger *slog.Logger) {
	if sweeper == nil {
		return
	}
	if interval <= 0 {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweepCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			estimated, err := sweeper.SweepProbeOffsets(sweepCtx, time.Now().UTC())
			cancel()
			switch {
			case err != nil && ctx.Err() == nil:
				logger.Warn("telemetry_probe_offset_sweep_failed", "error_code", "TELEMETRY_PROBE_OFFSET_SWEEP_FAILED")
			case estimated > 0:
				logger.Info("telemetry_probe_offsets_estimated", "estimate_count", estimated)
			}
		}
	}
}

// runRevocationRelay applies IAM telemetry revocations to open realtime subscriptions.
func runRevocationRelay(ctx context.Context, relay *telemetry.RevocationRelay, logger *slog.Logger) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	lastFailureLog := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			revoked, err := relay.RelayOnce(ctx)
			if err != nil {
				if time.Since(lastFailureLog) >= 30*time.Second {
					logger.Warn("telemetry_revocation_relay_failed", "error_code", "TELEMETRY_REVOCATION_RELAY_FAILED")
					lastFailureLog = time.Now()
				}
				continue
			}
			if revoked > 0 {
				logger.Info("telemetry_realtime_subscriptions_revoked", "subscription_count", revoked)
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
	deliveryTimeout := durationEnv("TELEMETRY_ALARM_RELAY_DELIVERY_TIMEOUT", telemetry.DefaultAlarmEvaluationDeliveryTimeout, 100*time.Millisecond, time.Minute)
	client := &http.Client{
		Timeout:       deliveryTimeout,
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
		Repository:      store,
		Transport:       transport,
		WorkerID:        workerID,
		LeaseDuration:   durationEnv("TELEMETRY_ALARM_RELAY_LEASE_DURATION", 30*time.Second, 5*time.Second, 5*time.Minute),
		RetryDelay:      durationEnv("TELEMETRY_ALARM_RELAY_RETRY_DELAY", 5*time.Second, time.Second, time.Hour),
		MaxAttempts:     integerEnv("TELEMETRY_ALARM_RELAY_MAX_ATTEMPTS", telemetry.DefaultAlarmEvaluationMaxAttempts, 1, 1000),
		DeliveryTimeout: deliveryTimeout,
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
			relayContext, cancel := context.WithTimeout(ctx, 30*time.Second)
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
	return envOrLookup(os.Getenv, name, fallback)
}

func envOrLookup(lookup func(string) string, name, fallback string) string {
	if value := strings.TrimSpace(lookup(name)); value != "" {
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

func parsePositiveInt32Env(lookup func(string) string, name string) (int32, bool, error) {
	raw := strings.TrimSpace(lookup(name))
	if raw == "" {
		return 0, false, nil
	}
	parsed, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || parsed < 1 {
		return 0, false, errors.New(name + " is invalid")
	}
	return int32(parsed), true, nil
}

func parsePositiveDurationEnv(lookup func(string) string, name string) (time.Duration, bool, error) {
	raw := strings.TrimSpace(lookup(name))
	if raw == "" {
		return 0, false, nil
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil || parsed <= 0 {
		return 0, false, errors.New(name + " is invalid")
	}
	return parsed, true, nil
}

func parseBoolEnv(lookup func(string) string, name string, fallback bool) (bool, error) {
	raw := strings.TrimSpace(lookup(name))
	if raw == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return false, errors.New(name + " is invalid")
	}
	return parsed, nil
}

func parseBoundedIntEnv(lookup func(string) string, name string, fallback, minimum, maximum int) (int, error) {
	raw := strings.TrimSpace(lookup(name))
	if raw == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed < minimum || parsed > maximum {
		return 0, errors.New(name + " is invalid")
	}
	return parsed, nil
}

func parseBoundedDurationEnv(lookup func(string) string, name string, fallback, minimum, maximum time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(lookup(name))
	if raw == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil || parsed < minimum || parsed > maximum {
		return 0, errors.New(name + " is invalid")
	}
	return parsed, nil
}

func parseTelemetryPostgresPoolConfig(lookup func(string) string) (telemetry.PostgresPoolConfig, error) {
	cfg := telemetry.DefaultPostgresPoolConfig()
	if maxConns, ok, err := parsePositiveInt32Env(lookup, "TELEMETRY_POSTGRES_MAX_CONNS"); err != nil {
		return telemetry.PostgresPoolConfig{}, err
	} else if ok {
		cfg.MaxConns = maxConns
	}
	minConns, minSet, err := parsePositiveInt32Env(lookup, "TELEMETRY_POSTGRES_MIN_CONNS")
	if err != nil {
		return telemetry.PostgresPoolConfig{}, err
	}
	if minSet {
		cfg.MinConns = minConns
	} else if cfg.MinConns > cfg.MaxConns {
		cfg.MinConns = cfg.MaxConns
	}
	if cfg.MinConns > cfg.MaxConns {
		return telemetry.PostgresPoolConfig{}, errors.New("TELEMETRY_POSTGRES_MIN_CONNS exceeds TELEMETRY_POSTGRES_MAX_CONNS")
	}
	if lifetime, ok, err := parsePositiveDurationEnv(lookup, "TELEMETRY_POSTGRES_MAX_CONN_LIFETIME"); err != nil {
		return telemetry.PostgresPoolConfig{}, err
	} else if ok {
		cfg.MaxConnLifetime = lifetime
	}
	if idleTime, ok, err := parsePositiveDurationEnv(lookup, "TELEMETRY_POSTGRES_MAX_CONN_IDLE_TIME"); err != nil {
		return telemetry.PostgresPoolConfig{}, err
	} else if ok {
		cfg.MaxConnIdleTime = idleTime
	}
	if healthPeriod, ok, err := parsePositiveDurationEnv(lookup, "TELEMETRY_POSTGRES_HEALTH_CHECK_PERIOD"); err != nil {
		return telemetry.PostgresPoolConfig{}, err
	} else if ok {
		cfg.HealthCheckPeriod = healthPeriod
	}
	return cfg, nil
}
