package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"log/slog"
	"net"
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
	Observability     *observability.Runtime
	Logger            *slog.Logger
	Diagnostics       *http.Server
	Server            *http.Server
	HTTPWG            *sync.WaitGroup
	CancelWorkers     func()
	WorkersWG         *sync.WaitGroup
	DrainRelays       func(context.Context)
	CancelAnalytics   func()
	AnalyticsWG       *sync.WaitGroup
	HTTPTimeout       time.Duration
	WorkerTimeout     time.Duration
	DrainTimeout      time.Duration
	TelemetryTimeout  time.Duration
	CloseHistory      func()
	CloseLatestCache  func()
	CloseRateLimiter  func()
	CloseCoordination func()
	CloseStore        func()
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
		var metrics *observability.Registry
		if cfg.Observability != nil {
			metrics = cfg.Observability.Metrics
		}
		var shutdownWG sync.WaitGroup
		for _, srv := range []*http.Server{cfg.Diagnostics, cfg.Server} {
			if srv == nil {
				continue
			}
			shutdownWG.Add(1)
			go func(s *http.Server) {
				defer shutdownWG.Done()
				defer observability.Recover(cfg.Logger, metrics, "telemetry_worker.http_shutdown")
				if err := s.Shutdown(httpCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
					_ = s.Close()
				}
			}(srv)
		}
		shutdownWG.Wait()
		if !waitWorkersBounded(cfg.HTTPWG, httpTimeout) && cfg.Logger != nil {
			cfg.Logger.Warn("telemetry_http_shutdown_timed_out", "error_code", "TELEMETRY_HTTP_SHUTDOWN_TIMEOUT")
		}
		cancelHTTP()
	} else if cfg.HTTPWG != nil {
		if !waitWorkersBounded(cfg.HTTPWG, httpTimeout) && cfg.Logger != nil {
			cfg.Logger.Warn("telemetry_http_shutdown_timed_out", "error_code", "TELEMETRY_HTTP_SHUTDOWN_TIMEOUT")
		}
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
	if cfg.Observability != nil {
		telemetryTimeout := cfg.TelemetryTimeout
		if telemetryTimeout <= 0 {
			telemetryTimeout = 2 * time.Second
		}
		shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), telemetryTimeout)
		_ = cfg.Observability.Shutdown(shutdownContext)
		cancelShutdown()
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
	if cfg.CloseCoordination != nil {
		cfg.CloseCoordination()
	}
	if cfg.CloseStore != nil {
		cfg.CloseStore()
	}
}

func waitWorkersBounded(wg *sync.WaitGroup, timeout time.Duration) bool {
	if wg == nil {
		return true
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer observability.Recover(slog.Default(), nil, "telemetry_worker.wait_workers")
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
	var httpWG sync.WaitGroup
	var workersWG sync.WaitGroup
	var analyticsWG sync.WaitGroup
	var workerCancels []context.CancelFunc
	shutdownCfg := telemetryWorkerShutdown{
		Observability: observabilityRuntime,
		Logger:        logger,
		HTTPWG:        &httpWG,
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

	tlsCert, err := requiredEnv("TELEMETRY_TLS_CERT")
	if err != nil {
		logger.Error("telemetry_tls_identity_load_failed", "error_code", "TELEMETRY_TLS_IDENTITY_LOAD_FAILED")
		return 1
	}
	tlsKey, err := requiredEnv("TELEMETRY_TLS_KEY")
	if err != nil {
		logger.Error("telemetry_tls_identity_load_failed", "error_code", "TELEMETRY_TLS_IDENTITY_LOAD_FAILED")
		return 1
	}
	certificate, err := tls.LoadX509KeyPair(tlsCert, tlsKey)
	if err != nil {
		logger.Error("telemetry_tls_identity_load_failed", "error_code", "TELEMETRY_TLS_IDENTITY_LOAD_FAILED")
		return 1
	}
	clientCAPath, err := requiredEnv("TELEMETRY_CLIENT_CA")
	if err != nil {
		logger.Error("telemetry_client_ca_invalid", "error_code", "TELEMETRY_CLIENT_CA_INVALID")
		return 1
	}
	clientCAs, err := loadCertPool(clientCAPath)
	if err != nil {
		logger.Error("telemetry_client_ca_invalid", "error_code", "TELEMETRY_CLIENT_CA_INVALID")
		return 1
	}
	iamCAPath, err := requiredEnv("TELEMETRY_IAM_CA")
	if err != nil {
		logger.Error("telemetry_iam_ca_invalid", "error_code", "TELEMETRY_IAM_CA_INVALID")
		return 1
	}
	iamCAs, err := loadCertPool(iamCAPath)
	if err != nil {
		logger.Error("telemetry_iam_ca_invalid", "error_code", "TELEMETRY_IAM_CA_INVALID")
		return 1
	}
	iamGrantCertPath, err := requiredEnv("TELEMETRY_IAM_GRANT_CERT")
	if err != nil {
		logger.Error("telemetry_iam_grant_certificate_invalid", "error_code", "TELEMETRY_IAM_GRANT_CERTIFICATE_INVALID")
		return 1
	}
	iamGrantPublicKey, err := loadCertificatePublicKey(iamGrantCertPath)
	if err != nil {
		logger.Error("telemetry_iam_grant_certificate_invalid", "error_code", "TELEMETRY_IAM_GRANT_CERTIFICATE_INVALID")
		return 1
	}

	poolConfig, err := parseTelemetryPostgresPoolConfig(os.Getenv)
	if err != nil {
		logger.Error("telemetry_store_pool_config_invalid", "error_code", "TELEMETRY_STORE_POOL_CONFIG_INVALID")
		return 1
	}

	databaseURL, err := requiredEnv("TELEMETRY_DATABASE_URL")
	if err != nil {
		logger.Error("telemetry_store_open_failed", "error_code", "TELEMETRY_STORE_OPEN_FAILED")
		return 1
	}
	openContext, cancelOpen := context.WithTimeout(context.Background(), 5*time.Second)
	store, err := telemetry.OpenPostgresStoreWithConfig(openContext, databaseURL, poolConfig)
	cancelOpen()
	if err != nil {
		logger.Error("telemetry_store_open_failed", "error_code", "TELEMETRY_STORE_OPEN_FAILED")
		return 1
	}
	shutdownCfg.CloseStore = store.Close
	pipelineMetrics := telemetry.NewPipelineMetrics(observabilityRuntime.Metrics)
	store.SetPipelineMetrics(pipelineMetrics)
	coordinationLocker, closeCoordination, err := loadTelemetryCoordinationLocks(os.Getenv)
	if err != nil {
		logger.Error("telemetry_coordination_config_invalid", "error_code", "TELEMETRY_COORDINATION_CONFIG_INVALID")
		return 1
	}
	shutdownCfg.CloseCoordination = closeCoordination
	settlingBuffer, err := durationEnv("TELEMETRY_SCHEDULE_WATERMARK_BUFFER", 30*time.Second, 0, 10*time.Minute)
	if err != nil {
		logger.Error("telemetry_coordination_config_invalid", "error_code", "TELEMETRY_COORDINATION_CONFIG_INVALID")
		return 1
	}
	retentionContext, retentionCancel := context.WithCancel(context.Background())
	workerCancels = append(workerCancels, retentionCancel)
	runSupervised(retentionContext, &workersWG, observabilityRuntime, logger, "telemetry.runtime_retention", func() {
		runRuntimeRetentionCoordinated(retentionContext, store, settlingBuffer, coordinationLocker, logger)
	})
	runSupervised(retentionContext, &workersWG, observabilityRuntime, logger, "telemetry.presence_sweep", func() {
		runPresenceSweepCoordinated(retentionContext, store, time.Minute, settlingBuffer, coordinationLocker, logger)
	})
	runSupervised(retentionContext, &workersWG, observabilityRuntime, logger, "telemetry.probe_offset_sweep", func() {
		runProbeOffsetSweepCoordinated(retentionContext, store, 5*time.Second, coordinationLocker, logger)
	})
	clickHousePruner, clickHouseRetentionCtx, clickHouseRetentionCancel, err := loadClickHouseRetentionPruner(pipelineMetrics)
	if err != nil {
		logger.Error("telemetry_clickhouse_retention_configuration_invalid", "error_code", "TELEMETRY_CLICKHOUSE_RETENTION_CONFIGURATION_INVALID")
		return 1
	}
	if clickHouseRetentionCancel != nil {
		clickHouseRetentionInterval, err := durationEnv("TELEMETRY_RETENTION_CLICKHOUSE_INTERVAL", time.Hour, time.Minute, 24*time.Hour)
		if err != nil {
			clickHouseRetentionCancel()
			logger.Error("telemetry_clickhouse_retention_configuration_invalid", "error_code", "TELEMETRY_CLICKHOUSE_RETENTION_CONFIGURATION_INVALID")
			return 1
		}
		workerCancels = append(workerCancels, clickHouseRetentionCancel)
		runSupervised(clickHouseRetentionCtx, &workersWG, observabilityRuntime, logger, "telemetry.clickhouse_retention", func() {
			runClickHouseRetentionCoordinated(clickHouseRetentionCtx, clickHousePruner, clickHouseRetentionInterval, settlingBuffer, coordinationLocker, logger)
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
	historyRepository, historySink, historyRelay, historySettings, historyContext, historyCancel, err := loadHistoryProjection(pipelineMetrics)
	if err != nil {
		logger.Error("telemetry_history_projection_configuration_invalid", "error_code", "TELEMETRY_HISTORY_PROJECTION_CONFIGURATION_INVALID")
		return 1
	}
	if historySink != nil {
		dependencies = append(dependencies, observability.Dependency{Name: "clickhouse", Required: true, Check: historySink.Ping})
	} else if clickHousePruner != nil {
		dependencies = append(dependencies, observability.Dependency{Name: "clickhouse", Required: true, Check: clickHousePruner.Ping})
	}
	observabilityRuntime.SetDependencies(dependencies...)
	if latestCancel != nil {
		workerCancels = append(workerCancels, latestCancel)
		runSupervised(latestContext, &workersWG, observabilityRuntime, logger, "telemetry.latest_cache_relay", func() {
			runLatestCacheRelay(latestContext, latestRelay, logger)
		})
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

	analyticsProjector, priceWindows, totalSubmeters, analyticsSettings, analyticsContext, analyticsCancel, err := loadAnalyticsProjection(certificate)
	if err != nil {
		logger.Error("analytics_projection_configuration_invalid", "error_code", "ANALYTICS_PROJECTION_CONFIGURATION_INVALID")
		return 1
	}
	if analyticsCancel != nil {
		shutdownCfg.CancelAnalytics = analyticsCancel
		runSupervised(analyticsContext, &analyticsWG, observabilityRuntime, logger, "telemetry.analytics_projection", func() {
			runAnalyticsProjectionCoordinated(analyticsContext, analyticsProjector, analyticsSettings.ProjectorInterval, observabilityRuntime, coordinationLocker, logger)
		})
		runSupervised(analyticsContext, &analyticsWG, observabilityRuntime, logger, "telemetry.price_window_projection", func() {
			runPriceWindowProjectionCoordinated(analyticsContext, priceWindows, analyticsSettings.PriceWindowInterval, observabilityRuntime, settlingBuffer, coordinationLocker, logger)
		})
		runSupervised(analyticsContext, &analyticsWG, observabilityRuntime, logger, "telemetry.total_submeter_projection", func() {
			runTotalSubmeterProjectionCoordinated(analyticsContext, totalSubmeters, analyticsSettings.TotalSubmeterInterval, observabilityRuntime, settlingBuffer, coordinationLocker, logger)
		})
	}

	iamClient := &http.Client{
		Timeout: 5 * time.Second,
		Transport: newClonedTelemetryHTTPTransport(&tls.Config{
			MinVersion: tls.VersionTLS13, RootCAs: iamCAs, Certificates: []tls.Certificate{certificate},
		}, true, false, 10*time.Second),
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	iamEndpoint, err := requiredEnv("TELEMETRY_IAM_ENDPOINT")
	if err != nil {
		logger.Error("telemetry_iam_authorizer_invalid", "error_code", "TELEMETRY_IAM_AUTHORIZER_INVALID")
		return 1
	}
	authorizer, err := telemetry.NewHTTPGrantAuthorizer(
		iamEndpoint, iamClient, iamGrantPublicKey,
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
		revocationSource, err := telemetry.NewHTTPRevocationSource(iamEndpoint, iamClient)
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

	alarmRelay, alarmPollInterval, alarmContext, alarmCancel, err := loadAlarmEvaluationRelay(store, certificate)
	if err != nil {
		logger.Error("telemetry_alarm_evaluation_configuration_invalid", "error_code", "TELEMETRY_ALARM_EVALUATION_CONFIGURATION_INVALID")
		return 1
	}
	if alarmCancel != nil {
		workerCancels = append(workerCancels, alarmCancel)
		runSupervised(alarmContext, &workersWG, observabilityRuntime, logger, "telemetry.alarm_evaluation_relay", func() {
			runAlarmEvaluationRelay(alarmContext, alarmRelay, alarmPollInterval, logger)
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
	serverErr := make(chan error, 2)
	httpWG.Add(1)
	go func() {
		defer httpWG.Done()
		defer observability.Recover(logger, observabilityRuntime.Metrics, "telemetry.diagnostics", func(any) {
			select {
			case serverErr <- errors.New("diagnostics server panicked"):
			default:
			}
		})
		if err := diagnostics.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("telemetry_diagnostics_stopped_unexpectedly", "error_code", "TELEMETRY_DIAGNOSTICS_SERVE_FAILED")
			select {
			case serverErr <- err:
			default:
			}
		}
	}()
	httpWG.Add(1)
	go func() {
		defer httpWG.Done()
		defer observability.Recover(logger, observabilityRuntime.Metrics, "telemetry.http", func(any) {
			select {
			case serverErr <- errors.New("http server panicked"):
			default:
			}
		})
		logger.Info("telemetry_runtime_started", "service", "telemetry-runtime-service", "address", server.Addr)
		if serveErr := server.ListenAndServeTLS("", ""); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			serverErr <- serveErr
			return
		}
		serverErr <- nil
	}()
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
				logger.Debug("telemetry_latest_cache_relay_batch_materialized", "materialized_count", materialized)
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

func loadHistoryProjection(metrics *telemetry.PipelineMetrics) (*telemetry.HistoryPostgresRepository, *telemetry.ClickHouseHistorySink, *telemetry.HistoryRelay, historyProjectionSettings, context.Context, context.CancelFunc, error) {
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("TELEMETRY_HISTORY_PROJECTION_ENABLED")), "true") {
		return nil, nil, nil, historyProjectionSettings{}, context.Background(), nil, nil
	}
	settings, err := parseHistoryProjectionSettings(os.Getenv)
	if err != nil {
		return nil, nil, nil, historyProjectionSettings{}, nil, nil, err
	}
	historyDBURL, err := requiredEnv("TELEMETRY_HISTORY_DATABASE_URL")
	if err != nil {
		return nil, nil, nil, historyProjectionSettings{}, nil, nil, err
	}
	openContext, cancelOpen := context.WithTimeout(context.Background(), 5*time.Second)
	repository, err := telemetry.OpenHistoryPostgresRepository(openContext, historyDBURL)
	cancelOpen()
	if err != nil {
		return nil, nil, nil, historyProjectionSettings{}, nil, nil, err
	}
	clickHouseURL, err := requiredEnv("TELEMETRY_CLICKHOUSE_HTTP_URL")
	if err != nil {
		repository.Close()
		return nil, nil, nil, historyProjectionSettings{}, nil, nil, err
	}
	sink, err := telemetry.NewClickHouseHistorySink(telemetry.ClickHouseHistoryConfig{
		BaseURL:  clickHouseURL,
		Database: envOr("TELEMETRY_CLICKHOUSE_DATABASE", "telemetry_history"),
		Table:    envOr("TELEMETRY_CLICKHOUSE_TABLE", "observations"),
		Username: envOr("TELEMETRY_CLICKHOUSE_USERNAME", "telemetry_history_writer"),
		Password: os.Getenv("TELEMETRY_CLICKHOUSE_PASSWORD"),
	})
	if err != nil {
		repository.Close()
		return nil, nil, nil, historyProjectionSettings{}, nil, nil, err
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
		return nil, nil, nil, historyProjectionSettings{}, nil, nil, err
	}
	relayContext, relayCancel := context.WithCancel(context.Background())
	return repository, sink, relay, settings, relayContext, relayCancel, nil
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
			logger.Debug("telemetry_history_batch_projected", "observation_count", published)
		}
	})
}

type analyticsProjectionSettings struct {
	ProjectorInterval     time.Duration
	PriceWindowInterval   time.Duration
	TotalSubmeterInterval time.Duration
}

func loadAnalyticsProjection(certificate tls.Certificate) (*analyticsprojector.Projector, *analyticsprojector.PriceWindowProjector, *analyticsprojector.TotalSubmeterProjector, analyticsProjectionSettings, context.Context, context.CancelFunc, error) {
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("ANALYTICS_PROJECTION_ENABLED")), "true") {
		return nil, nil, nil, analyticsProjectionSettings{}, context.Background(), nil, nil
	}
	projectorInterval, err := durationEnv("ANALYTICS_PROJECTOR_POLL_INTERVAL", 500*time.Millisecond, 25*time.Millisecond, time.Minute)
	if err != nil {
		return nil, nil, nil, analyticsProjectionSettings{}, nil, nil, err
	}
	priceWindowInterval, err := durationEnv("ANALYTICS_PRICE_WINDOW_INTERVAL", 5*time.Minute, 10*time.Second, time.Hour)
	if err != nil {
		return nil, nil, nil, analyticsProjectionSettings{}, nil, nil, err
	}
	totalSubmeterInterval, err := durationEnv("ANALYTICS_TOTAL_SUBMETER_INTERVAL", time.Minute, 10*time.Second, time.Hour)
	if err != nil {
		return nil, nil, nil, analyticsProjectionSettings{}, nil, nil, err
	}
	batchSize, err := integerEnv("ANALYTICS_PROJECTOR_BATCH_SIZE", 256, 1, 4096)
	if err != nil {
		return nil, nil, nil, analyticsProjectionSettings{}, nil, nil, err
	}
	httpClient, err := newAnalyticsHTTPClient(strings.TrimSpace(os.Getenv("ANALYTICS_CLICKHOUSE_CA")))
	if err != nil {
		return nil, nil, nil, analyticsProjectionSettings{}, nil, nil, err
	}
	baseURL, err := requiredEnv("ANALYTICS_CLICKHOUSE_HTTP_URL")
	if err != nil {
		return nil, nil, nil, analyticsProjectionSettings{}, nil, nil, err
	}
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
		return nil, nil, nil, analyticsProjectionSettings{}, nil, nil, err
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
		return nil, nil, nil, analyticsProjectionSettings{}, nil, nil, err
	}
	internalCAPath, err := requiredEnv("ANALYTICS_INTERNAL_CA")
	if err != nil {
		return nil, nil, nil, analyticsProjectionSettings{}, nil, nil, err
	}
	internalRoots, err := loadCertPool(internalCAPath)
	if err != nil {
		return nil, nil, nil, analyticsProjectionSettings{}, nil, nil, err
	}
	coreRegistryURL, err := requiredEnv("ANALYTICS_CORE_REGISTRY_URL")
	if err != nil {
		return nil, nil, nil, analyticsProjectionSettings{}, nil, nil, err
	}
	iamURL, err := requiredEnv("ANALYTICS_IAM_URL")
	if err != nil {
		return nil, nil, nil, analyticsProjectionSettings{}, nil, nil, err
	}
	internalClient := &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport:     newClonedTelemetryHTTPTransport(&tls.Config{MinVersion: tls.VersionTLS13, RootCAs: internalRoots, Certificates: []tls.Certificate{certificate}}, true, true, 15*time.Second),
	}
	registryConfig := analyticsprojector.RegistryClientConfig{
		BaseURL: coreRegistryURL, IAMURL: iamURL, HTTPClient: internalClient,
	}
	bindingResolver, err := analyticsprojector.NewBindingResolver(registryConfig)
	if err != nil {
		return nil, nil, nil, analyticsProjectionSettings{}, nil, nil, err
	}
	projector, err := analyticsprojector.NewProjector(analyticsprojector.ProjectorConfig{
		CounterSource: reader, BindingResolver: bindingResolver, FactSink: writer, RebuildEvents: writer, BatchSize: batchSize,
	})
	if err != nil {
		return nil, nil, nil, analyticsProjectionSettings{}, nil, nil, err
	}
	tariffs, err := analyticsprojector.NewTariffSource(registryConfig)
	if err != nil {
		return nil, nil, nil, analyticsProjectionSettings{}, nil, nil, err
	}
	priceWindows, err := analyticsprojector.NewPriceWindowProjector(analyticsprojector.PriceWindowProjectorConfig{Reader: reader, Writer: writer, Tariffs: tariffs})
	if err != nil {
		return nil, nil, nil, analyticsProjectionSettings{}, nil, nil, err
	}
	submeterSources, err := analyticsprojector.NewTotalSubmeterSource(registryConfig)
	if err != nil {
		return nil, nil, nil, analyticsProjectionSettings{}, nil, nil, err
	}
	totalSubmeters, err := analyticsprojector.NewTotalSubmeterProjector(analyticsprojector.TotalSubmeterProjectorConfig{Reader: reader, Writer: writer, Sources: submeterSources})
	if err != nil {
		return nil, nil, nil, analyticsProjectionSettings{}, nil, nil, err
	}
	projectionContext, projectionCancel := context.WithCancel(context.Background())
	settings := analyticsProjectionSettings{
		ProjectorInterval:     projectorInterval,
		PriceWindowInterval:   priceWindowInterval,
		TotalSubmeterInterval: totalSubmeterInterval,
	}
	return projector, priceWindows, totalSubmeters, settings, projectionContext, projectionCancel, nil
}

func runAnalyticsProjectionCoordinated(ctx context.Context, projector *analyticsprojector.Projector, pollInterval time.Duration, runtime *observability.Runtime, locker leaseLocker, logger *slog.Logger) {
	if projector == nil {
		return
	}
	if pollInterval <= 0 {
		pollInterval = 500 * time.Millisecond
	}
	lastFailureLog := time.Time{}
	logger.Info("analytics_read_model_projection_started", "poll_interval", pollInterval.String(), "deployment", "telemetry-runtime-in-process")
	runPeriodicLoop(ctx, periodicLoopConfig{
		LockKey: "analytics_projection", LockTTL: 30 * time.Second, Interval: pollInterval,
		Locker: locker, Logger: logger, LockFailEvent: "analytics_projection_lock_failed", ImmediateFirstTick: true,
	}, func(runCtx context.Context, _ time.Time) {
		projectionContext, span := runtime.Tracer.Start(runCtx, "analytics.energy_interval.project", observability.SpanKindInternal, map[string]any{
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
			return
		}
		span.SetStatus("ok", "")
		span.End()
		if projected > 0 {
			_ = runtime.Metrics.AddCounter("hvac_analytics_energy_intervals_projected_total", "Projected additive energy intervals.", map[string]string{"model": "energy_interval_facts"}, float64(projected))
			logger.Debug("analytics_energy_intervals_projected", "interval_count", projected)
		}
	})
}

// runPriceWindowProjectionCoordinated keeps every priced Site's Tariff laid out as price windows for Cube
// (#575). Tariffs change rarely, so it runs at start and then every few minutes.
func runPriceWindowProjectionCoordinated(ctx context.Context, projector *analyticsprojector.PriceWindowProjector, interval time.Duration, runtime *observability.Runtime, settlingBuffer time.Duration, locker leaseLocker, logger *slog.Logger) {
	if projector == nil {
		return
	}
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	runPeriodicLoop(ctx, periodicLoopConfig{
		LockKey: "price_window_projection", LockTTL: 90 * time.Second, Interval: interval, SettlingBuffer: settlingBuffer,
		AlignToWindow: settlingBuffer > 0, ImmediateFirstTick: settlingBuffer == 0,
		Locker: locker, Logger: logger, LockFailEvent: "analytics_price_window_lock_failed",
	}, func(runCtx context.Context, _ time.Time) {
		syncContext, cancel := context.WithTimeout(runCtx, time.Minute)
		err := projector.SyncOnce(syncContext)
		cancel()
		if err != nil {
			_ = runtime.Metrics.AddCounter("hvac_analytics_projection_failures_total", "Failed analytics read-model projection batches.", map[string]string{"model": "tariff_price_windows"}, 1)
			logger.Warn("analytics_price_window_projection_failed", "error_code", "ANALYTICS_PRICE_WINDOW_PROJECTION_FAILED", "error", err.Error())
		}
	})
}

func runTotalSubmeterProjectionCoordinated(ctx context.Context, projector *analyticsprojector.TotalSubmeterProjector, interval time.Duration, runtime *observability.Runtime, settlingBuffer time.Duration, locker leaseLocker, logger *slog.Logger) {
	if projector == nil {
		return
	}
	if interval <= 0 {
		interval = time.Minute
	}
	runPeriodicLoop(ctx, periodicLoopConfig{
		LockKey: "total_submeter_projection", LockTTL: 60 * time.Second, Interval: interval, SettlingBuffer: settlingBuffer,
		AlignToWindow: settlingBuffer > 0, ImmediateFirstTick: settlingBuffer == 0,
		Locker: locker, Logger: logger, LockFailEvent: "analytics_total_submeter_lock_failed",
	}, func(runCtx context.Context, _ time.Time) {
		syncContext, cancel := context.WithTimeout(runCtx, 30*time.Second)
		err := projector.SyncOnce(syncContext)
		cancel()
		if err != nil {
			_ = runtime.Metrics.AddCounter("hvac_analytics_projection_failures_total", "Failed analytics read-model projection batches.", map[string]string{"model": "site_total_submeters"}, 1)
			logger.Warn("analytics_total_submeter_projection_failed", "error_code", "ANALYTICS_TOTAL_SUBMETER_PROJECTION_FAILED")
		}
	})
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
		Timeout:   15 * time.Second,
		Transport: newClonedTelemetryHTTPTransport(tlsConfig, false, true, 30*time.Second),
	}, nil
}

func loadRealtimeService(store *telemetry.PostgresStore, certificate tls.Certificate, metrics *observability.Registry) (*telemetry.RealtimeService, context.Context, context.CancelFunc, error) {
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("TELEMETRY_REALTIME_ENABLED")), "true") {
		return nil, context.Background(), nil, nil
	}
	centrifugoCAPath, err := requiredEnv("TELEMETRY_CENTRIFUGO_CA")
	if err != nil {
		return nil, nil, nil, err
	}
	centrifugoCAs, err := loadCertPool(centrifugoCAPath)
	if err != nil {
		return nil, nil, nil, err
	}
	transport := newClonedTelemetryHTTPTransport(&tls.Config{
		MinVersion:   tls.VersionTLS13,
		RootCAs:      centrifugoCAs,
		Certificates: []tls.Certificate{certificate},
		ServerName:   strings.TrimSpace(os.Getenv("TELEMETRY_CENTRIFUGO_SERVER_NAME")),
	}, true, false, 15*time.Second)
	centrifugoAPIURL, err := requiredEnv("TELEMETRY_CENTRIFUGO_API_URL")
	if err != nil {
		return nil, nil, nil, err
	}
	centrifugoAPIKey, err := requiredEnv("TELEMETRY_CENTRIFUGO_API_KEY")
	if err != nil {
		return nil, nil, nil, err
	}
	centrifugo, err := telemetry.NewCentrifugoTransport(telemetry.CentrifugoTransportConfig{
		BaseURL:    centrifugoAPIURL,
		APIKey:     centrifugoAPIKey,
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
	publicEndpoint, err := requiredEnv("TELEMETRY_REALTIME_ENDPOINT")
	if err != nil {
		return nil, nil, nil, err
	}
	capabilityKey, err := requiredEnv("TELEMETRY_REALTIME_CAPABILITY_HMAC_KEY")
	if err != nil {
		return nil, nil, nil, err
	}
	tokenKey, err := requiredEnv("TELEMETRY_CENTRIFUGO_TOKEN_HMAC_KEY")
	if err != nil {
		return nil, nil, nil, err
	}
	relayLeaseDuration, err := durationEnv("TELEMETRY_REALTIME_RELAY_LEASE_DURATION", 30*time.Second, 5*time.Second, 5*time.Minute)
	if err != nil {
		return nil, nil, nil, err
	}
	service, err := telemetry.NewRealtimeService(telemetry.RealtimeConfig{
		Repository:             store,
		Transport:              instrumentedTransport,
		PublicEndpoint:         publicEndpoint,
		CapabilityHMACKey:      []byte(capabilityKey),
		ConnectionTokenHMACKey: []byte(tokenKey),
		RelayWorkerID:          relayWorkerID,
		RelayLeaseDuration:     relayLeaseDuration,
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
				logger.Debug("telemetry_realtime_relay_batch_published", "publication_count", published)
			}
		}
	}
}

// runRuntimeRetentionCoordinated prunes superseded presence signals, delivered publications and history
// events, and week-old received observations, which nothing reads once past their window.
func runRuntimeRetentionCoordinated(ctx context.Context, store *telemetry.PostgresStore, settlingBuffer time.Duration, locker leaseLocker, logger *slog.Logger) {
	runPeriodicLoop(ctx, periodicLoopConfig{
		LockKey: "runtime_retention", LockTTL: 60 * time.Second, Interval: 5 * time.Minute, SettlingBuffer: settlingBuffer,
		AlignToWindow: settlingBuffer > 0, ImmediateFirstTick: settlingBuffer == 0,
		Locker: locker, Logger: logger, LockFailEvent: "telemetry_runtime_retention_lock_failed",
	}, func(runCtx context.Context, _ time.Time) {
		pruned, err := store.PruneRuntimeHistory(runCtx, time.Now())
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
	})
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
	runClickHouseRetentionCoordinated(ctx, pruner, interval, 0, nil, logger)
}

func runClickHouseRetentionCoordinated(ctx context.Context, pruner clickHouseRetentionExecutor, interval, settlingBuffer time.Duration, locker leaseLocker, logger *slog.Logger) {
	if pruner == nil {
		return
	}
	if interval <= 0 {
		interval = time.Hour
	}
	runPeriodicLoop(ctx, periodicLoopConfig{
		LockKey: "clickhouse_retention", LockTTL: 3 * time.Minute, Interval: interval, SettlingBuffer: settlingBuffer,
		AlignToWindow: settlingBuffer > 0, ImmediateFirstTick: settlingBuffer == 0,
		Locker: locker, Logger: logger, LockFailEvent: "telemetry_clickhouse_retention_lock_failed",
	}, func(runCtx context.Context, _ time.Time) {
		pruneCtx, cancel := context.WithTimeout(runCtx, 2*time.Minute)
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
	})
}

type presenceSweeper interface {
	SweepDuePresence(ctx context.Context, evaluatedAt time.Time, limit int) (int, error)
}

// runPresenceSweep re-evaluates devices whose presence window elapsed without new own signals,
// so silent or copy-only devices transition to OFFLINE and publish snapshot deltas.
func runPresenceSweep(ctx context.Context, sweeper presenceSweeper, interval time.Duration, logger *slog.Logger) {
	runPresenceSweepCoordinated(ctx, sweeper, interval, 0, nil, logger)
}

func runPresenceSweepCoordinated(ctx context.Context, sweeper presenceSweeper, interval, settlingBuffer time.Duration, locker leaseLocker, logger *slog.Logger) {
	if sweeper == nil {
		return
	}
	if interval <= 0 {
		interval = time.Minute
	}
	runPeriodicLoop(ctx, periodicLoopConfig{
		LockKey: "presence_sweep", LockTTL: 30 * time.Second, Interval: interval, SettlingBuffer: settlingBuffer,
		AlignToWindow: settlingBuffer > 0, ImmediateFirstTick: settlingBuffer == 0,
		Locker: locker, Logger: logger, LockFailEvent: "telemetry_presence_sweep_lock_failed",
	}, func(runCtx context.Context, _ time.Time) {
		sweepCtx, cancel := context.WithTimeout(runCtx, 15*time.Second)
		swept, err := sweeper.SweepDuePresence(sweepCtx, time.Now(), 256)
		cancel()
		switch {
		case err != nil && ctx.Err() == nil:
			logger.Warn("telemetry_presence_sweep_failed", "error_code", "TELEMETRY_PRESENCE_SWEEP_FAILED")
		case swept > 0:
			logger.Info("telemetry_presence_swept", "device_count", swept)
		}
	})
}

type probeOffsetSweeper interface {
	SweepProbeOffsets(ctx context.Context, evaluatedAt time.Time) (int, error)
}

// runProbeOffsetSweepCoordinated periodically folds idle supply/return probe-offset windows outside
// the synchronous observation write path.
func runProbeOffsetSweepCoordinated(ctx context.Context, sweeper probeOffsetSweeper, interval time.Duration, locker leaseLocker, logger *slog.Logger) {
	if sweeper == nil {
		return
	}
	if interval <= 0 {
		interval = 5 * time.Second
	}
	runPeriodicLoop(ctx, periodicLoopConfig{
		LockKey: "probe_offset_sweep", LockTTL: 30 * time.Second, Interval: interval,
		Locker: locker, Logger: logger, LockFailEvent: "telemetry_probe_offset_sweep_lock_failed",
	}, func(runCtx context.Context, _ time.Time) {
		sweepCtx, cancel := context.WithTimeout(runCtx, 15*time.Second)
		estimated, err := sweeper.SweepProbeOffsets(sweepCtx, time.Now().UTC())
		cancel()
		switch {
		case err != nil && ctx.Err() == nil:
			logger.Warn("telemetry_probe_offset_sweep_failed", "error_code", "TELEMETRY_PROBE_OFFSET_SWEEP_FAILED")
		case estimated > 0:
			logger.Debug("telemetry_probe_offsets_estimated", "estimate_count", estimated)
		}
	})
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

func loadAlarmEvaluationRelay(store *telemetry.PostgresStore, certificate tls.Certificate) (*telemetry.AlarmEvaluationRelay, time.Duration, context.Context, context.CancelFunc, error) {
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("TELEMETRY_ALARM_EVALUATION_ENABLED")), "true") {
		return nil, 0, context.Background(), nil, nil
	}
	alarmCAPath, err := requiredEnv("TELEMETRY_ALARM_CA")
	if err != nil {
		return nil, 0, nil, nil, err
	}
	alarmCAs, err := loadCertPool(alarmCAPath)
	if err != nil {
		return nil, 0, nil, nil, err
	}
	deliveryTimeout, err := durationEnv("TELEMETRY_ALARM_RELAY_DELIVERY_TIMEOUT", telemetry.DefaultAlarmEvaluationDeliveryTimeout, 100*time.Millisecond, time.Minute)
	if err != nil {
		return nil, 0, nil, nil, err
	}
	alarmEvalURL, err := requiredEnv("TELEMETRY_ALARM_EVALUATION_URL")
	if err != nil {
		return nil, 0, nil, nil, err
	}
	client := &http.Client{
		Timeout:       deliveryTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: newClonedTelemetryHTTPTransport(&tls.Config{
			MinVersion: tls.VersionTLS13, RootCAs: alarmCAs, Certificates: []tls.Certificate{certificate},
			ServerName: strings.TrimSpace(os.Getenv("TELEMETRY_ALARM_SERVER_NAME")),
		}, true, false, deliveryTimeout),
	}
	transport, err := telemetry.NewHTTPAlarmEvaluationTransport(telemetry.HTTPAlarmEvaluationTransportConfig{
		Endpoint: alarmEvalURL, HTTPClient: client,
	})
	if err != nil {
		return nil, 0, nil, nil, err
	}
	leaseDuration, err := durationEnv("TELEMETRY_ALARM_RELAY_LEASE_DURATION", 30*time.Second, 5*time.Second, 5*time.Minute)
	if err != nil {
		return nil, 0, nil, nil, err
	}
	retryDelay, err := durationEnv("TELEMETRY_ALARM_RELAY_RETRY_DELAY", 5*time.Second, time.Second, time.Hour)
	if err != nil {
		return nil, 0, nil, nil, err
	}
	maxAttempts, err := integerEnv("TELEMETRY_ALARM_RELAY_MAX_ATTEMPTS", telemetry.DefaultAlarmEvaluationMaxAttempts, 1, 1000)
	if err != nil {
		return nil, 0, nil, nil, err
	}
	pollInterval, err := durationEnv("TELEMETRY_ALARM_RELAY_POLL_INTERVAL", 100*time.Millisecond, 25*time.Millisecond, time.Minute)
	if err != nil {
		return nil, 0, nil, nil, err
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
		LeaseDuration:   leaseDuration,
		RetryDelay:      retryDelay,
		MaxAttempts:     maxAttempts,
		DeliveryTimeout: deliveryTimeout,
	})
	if err != nil {
		return nil, 0, nil, nil, err
	}
	relayContext, relayCancel := context.WithCancel(context.Background())
	return relay, pollInterval, relayContext, relayCancel, nil
}

func runAlarmEvaluationRelay(ctx context.Context, relay *telemetry.AlarmEvaluationRelay, pollInterval time.Duration, logger *slog.Logger) {
	if relay == nil {
		return
	}
	if pollInterval <= 0 {
		pollInterval = 100 * time.Millisecond
	}
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
				logger.Debug("telemetry_alarm_evaluation_batch_delivered", "publication_count", delivered)
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
	if options.DialTimeout <= 0 {
		options.DialTimeout = 5 * time.Second
	}
	if options.ReadTimeout <= 0 {
		options.ReadTimeout = 3 * time.Second
	}
	if options.WriteTimeout <= 0 {
		options.WriteTimeout = 3 * time.Second
	}
	if options.PoolTimeout <= 0 {
		options.PoolTimeout = 4 * time.Second
	}
	if options.PoolSize <= 0 || options.PoolSize > 16 {
		options.PoolSize = 16
	}
	if options.MinIdleConns <= 0 || options.MinIdleConns > options.PoolSize {
		options.MinIdleConns = 2
	}
	if options.ConnMaxIdleTime <= 0 {
		options.ConnMaxIdleTime = 5 * time.Minute
	}
	client := redis.NewClient(options)
	return limitpolicy.NewLimiter(limitpolicy.NewRedisCounter(client, "hvac:telemetry:limit"), telemetryLimitPolicy()), func() { _ = client.Close() }, nil
}

func newClonedTelemetryHTTPTransport(tlsConfig *tls.Config, disableCompression, allowHTTP2 bool, responseHeaderTimeout time.Duration) *http.Transport {
	var transport *http.Transport
	if base, ok := http.DefaultTransport.(*http.Transport); ok && base != nil {
		transport = base.Clone()
	} else {
		transport = &http.Transport{}
	}
	transport.Proxy = http.ProxyFromEnvironment
	transport.DialContext = (&net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext
	transport.TLSClientConfig = tlsConfig
	transport.DisableCompression = disableCompression
	if !allowHTTP2 {
		transport.ForceAttemptHTTP2 = false
	}
	transport.MaxConnsPerHost = 16
	transport.MaxIdleConns = 64
	transport.MaxIdleConnsPerHost = 16
	transport.IdleConnTimeout = 90 * time.Second
	transport.TLSHandshakeTimeout = 10 * time.Second
	transport.ResponseHeaderTimeout = responseHeaderTimeout
	transport.ExpectContinueTimeout = time.Second
	return transport
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

func requiredEnv(name string) (string, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return "", errors.New(name + " is required")
	}
	return value, nil
}

func integerEnv(name string, fallback, minimum, maximum int) (int, error) {
	return parseBoundedIntEnv(os.Getenv, name, fallback, minimum, maximum)
}

func durationEnv(name string, fallback, minimum, maximum time.Duration) (time.Duration, error) {
	return parseBoundedDurationEnv(os.Getenv, name, fallback, minimum, maximum)
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

type leaseLocker interface {
	Acquire(ctx context.Context, resource string, ttl time.Duration) (release func(context.Context), acquired bool, err error)
}

type localLeaseEntry struct {
	token     string
	expiresAt time.Time
}

type localLeaseLocker struct {
	mu     sync.Mutex
	now    func() time.Time
	leases map[string]localLeaseEntry
}

func newLocalLeaseLocker(now func() time.Time) *localLeaseLocker {
	if now == nil {
		now = time.Now
	}
	return &localLeaseLocker{now: now, leases: make(map[string]localLeaseEntry)}
}

func (l *localLeaseLocker) Acquire(_ context.Context, resource string, ttl time.Duration) (func(context.Context), bool, error) {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	token, err := newLeaseToken()
	if err != nil {
		return nil, false, err
	}
	now := l.now().UTC()
	l.mu.Lock()
	defer l.mu.Unlock()
	if existing, exists := l.leases[resource]; exists && existing.expiresAt.After(now) {
		return nil, false, nil
	}
	l.leases[resource] = localLeaseEntry{token: token, expiresAt: now.Add(ttl)}
	return func(context.Context) {
		l.mu.Lock()
		defer l.mu.Unlock()
		if current, exists := l.leases[resource]; exists && current.token == token {
			delete(l.leases, resource)
		}
	}, true, nil
}

var (
	releaseRedisLeaseScript = redis.NewScript(`if redis.call("GET", KEYS[1]) == ARGV[1] then return redis.call("DEL", KEYS[1]) else return 0 end`)
	renewRedisLeaseScript   = redis.NewScript(`if redis.call("GET", KEYS[1]) == ARGV[1] then return redis.call("PEXPIRE", KEYS[1], ARGV[2]) else return 0 end`)
)

type redisLeaseLocker struct {
	client    *redis.Client
	keyPrefix string
}

func newRedisLeaseLocker(client *redis.Client, keyPrefix string) *redisLeaseLocker {
	prefix := strings.TrimSpace(keyPrefix)
	if prefix == "" {
		prefix = "hvac:telemetry:lock:"
	}
	if !strings.HasSuffix(prefix, ":") {
		prefix += ":"
	}
	return &redisLeaseLocker{client: client, keyPrefix: prefix}
}

func (l *redisLeaseLocker) Acquire(ctx context.Context, resource string, ttl time.Duration) (func(context.Context), bool, error) {
	if l == nil || l.client == nil {
		return nil, false, errors.New("redis lease locker is not initialized")
	}
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	token, err := newLeaseToken()
	if err != nil {
		return nil, false, err
	}
	key := l.keyPrefix + resource
	acquired, err := l.client.SetNX(ctx, key, token, ttl).Result()
	if err != nil || !acquired {
		return nil, false, err
	}

	renewCtx, stopRenew := context.WithCancel(context.Background())
	renewDone := make(chan struct{})
	renewEvery := ttl / 3
	if renewEvery < 10*time.Millisecond {
		renewEvery = 10 * time.Millisecond
	}
	ttlMs := strconv.FormatInt(max(ttl.Milliseconds(), 1), 10)
	go func() {
		defer close(renewDone)
		ticker := time.NewTicker(renewEvery)
		defer ticker.Stop()
		for {
			select {
			case <-renewCtx.Done():
				return
			case <-ticker.C:
				callCtx, cancelCall := context.WithTimeout(renewCtx, min(ttl/2, 2*time.Second))
				res, renewErr := renewRedisLeaseScript.Run(callCtx, l.client, []string{key}, token, ttlMs).Int64()
				cancelCall()
				if renewErr == nil && res == 0 {
					return
				}
			}
		}
	}()

	var releaseOnce sync.Once
	return func(releaseCtx context.Context) {
		releaseOnce.Do(func() {
			stopRenew()
			<-renewDone
			if releaseCtx == nil || releaseCtx.Err() != nil {
				var cancelRelease context.CancelFunc
				releaseCtx, cancelRelease = context.WithTimeout(context.Background(), 2*time.Second)
				defer cancelRelease()
			}
			_, _ = releaseRedisLeaseScript.Run(releaseCtx, l.client, []string{key}, token).Result()
		})
	}, true, nil
}

func newLeaseToken() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func loadTelemetryCoordinationLocks(lookup func(string) string) (leaseLocker, func(), error) {
	for _, envName := range []string{
		"TELEMETRY_COORDINATION_REDIS_URL",
		"TELEMETRY_LATEST_CACHE_REDIS_URL",
		"TELEMETRY_LIMIT_POLICY_REDIS_URL",
	} {
		rawURL := strings.TrimSpace(lookup(envName))
		if rawURL == "" {
			continue
		}
		options, err := redis.ParseURL(rawURL)
		if err != nil {
			return nil, func() {}, err
		}
		client := redis.NewClient(options)
		return newRedisLeaseLocker(client, "hvac:telemetry:lock:"), func() { _ = client.Close() }, nil
	}
	return newLocalLeaseLocker(time.Now), func() {}, nil
}

func effectiveSettlingBuffer(period, configured time.Duration) time.Duration {
	if period <= 0 || configured <= 0 {
		return 0
	}
	half := period / 2
	if half > 0 && configured > half {
		return half
	}
	return configured
}

// nextSettledTick aligns periodic execution to period boundaries plus settlingBuffer so
// in-flight gateway messages and memory batcher flushes settle before aggregation runs.
func nextSettledTick(now time.Time, period, settlingBuffer time.Duration) (fireAt time.Time, boundary time.Time) {
	nowUTC := now.UTC()
	if period <= 0 {
		return nowUTC, nowUTC
	}
	buffer := max(settlingBuffer, 0)
	boundary = nowUTC.Truncate(period)
	candidate := boundary.Add(buffer)
	if !candidate.After(nowUTC) {
		boundary = boundary.Add(period)
		candidate = boundary.Add(buffer)
	}
	return candidate, boundary
}

type periodicLoopConfig struct {
	LockKey            string
	LockTTL            time.Duration
	Interval           time.Duration
	SettlingBuffer     time.Duration
	AlignToWindow      bool
	ImmediateFirstTick bool
	Locker             leaseLocker
	Logger             *slog.Logger
	LockFailEvent      string
	Now                func() time.Time
	WaitUntil          func(ctx context.Context, target time.Time) bool
}

func runPeriodicLoop(ctx context.Context, cfg periodicLoopConfig, fn func(runCtx context.Context, boundary time.Time)) {
	if fn == nil {
		return
	}
	nowFn := cfg.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	waitUntil := cfg.WaitUntil
	if waitUntil == nil {
		waitUntil = waitUntilTimer
	}
	interval := cfg.Interval
	if interval <= 0 {
		interval = time.Minute
	}
	buffer := effectiveSettlingBuffer(interval, cfg.SettlingBuffer)
	lastLockFailureLog := time.Time{}

	runAttempt := func(boundary time.Time) {
		if ctx.Err() != nil {
			return
		}
		if cfg.Locker != nil && cfg.LockKey != "" {
			lockCtx, cancelLock := context.WithTimeout(ctx, 5*time.Second)
			release, acquired, err := cfg.Locker.Acquire(lockCtx, cfg.LockKey, cfg.LockTTL)
			cancelLock()
			if err != nil {
				if cfg.Logger != nil && ctx.Err() == nil && time.Since(lastLockFailureLog) >= 10*time.Second {
					event := cfg.LockFailEvent
					if event == "" {
						event = "telemetry_scheduled_lock_failed"
					}
					cfg.Logger.Warn(event, "lock_key", cfg.LockKey, "error_code", "TELEMETRY_COORDINATION_LOCK_FAILED")
					lastLockFailureLog = time.Now()
				}
				return
			}
			if !acquired {
				return
			}
			defer release(ctx)
		}
		fn(ctx, boundary)
	}

	if !cfg.AlignToWindow && cfg.ImmediateFirstTick {
		runAttempt(nowFn().UTC())
	}

	for {
		var boundary time.Time
		var waitTarget time.Time
		if cfg.AlignToWindow && buffer > 0 {
			waitTarget, boundary = nextSettledTick(nowFn(), interval, buffer)
		} else {
			waitTarget = nowFn().UTC().Add(interval)
			boundary = waitTarget
		}
		if !waitUntil(ctx, waitTarget) {
			return
		}
		runAttempt(boundary)
	}
}

func waitUntilTimer(ctx context.Context, target time.Time) bool {
	delay := time.Until(target)
	if delay <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
