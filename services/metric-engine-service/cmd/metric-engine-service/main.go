package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/services/metric-engine-service/internal/metric"
)

type request struct {
	Operation   string    `json:"operation"`
	TenantID    string    `json:"tenantId"`
	SiteID      string    `json:"siteId"`
	BindingID   string    `json:"bindingId"`
	PeriodStart time.Time `json:"periodStart"`
	PeriodEnd   time.Time `json:"periodEnd"`
	Reason      string    `json:"reason"`
	StaleBefore time.Time `json:"staleBefore"`
	Limit       int       `json:"limit"`
}

type scheduledBinding struct {
	TenantID  string `json:"tenantId"`
	SiteID    string `json:"siteId"`
	BindingID string `json:"bindingId"`
}

type runtime struct {
	store  *metric.PostgresStore
	engine *metric.Engine
	latest *metric.RedisLatestStore
}

func main() {
	logger := observability.NewJSONLogger(os.Stdout, slog.LevelInfo)
	telemetry := observability.NewRuntime(observability.RuntimeConfig{
		Service: "metric-worker", OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), QueueSize: 1024, ExportTimeout: 500 * time.Millisecond,
	})
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	runtime, err := openRuntime(ctx)
	if err != nil {
		logger.Error("metric_worker_configuration_invalid", "error_code", "METRIC_WORKER_CONFIGURATION_INVALID")
		os.Exit(1)
	}
	defer runtime.Close()

	if strings.EqualFold(strings.TrimSpace(os.Getenv("METRIC_WORKER_MODE")), "oneshot") {
		if err := runOneShot(ctx, runtime, os.Stdin, os.Stdout); err != nil {
			logger.Error("metric_oneshot_failed", "error_code", "METRIC_ONESHOT_FAILED")
			os.Exit(1)
		}
		return
	}

	bindings, err := parseScheduledBindings(requiredEnv("METRIC_WORKER_BINDINGS_JSON"))
	if err != nil {
		logger.Error("metric_worker_bindings_invalid", "error_code", "METRIC_WORKER_BINDINGS_INVALID")
		os.Exit(1)
	}
	pollInterval := durationEnv("METRIC_WORKER_POLL_INTERVAL", 30*time.Second, time.Second, 10*time.Minute)
	finalizationDelay := durationEnv("METRIC_WORKER_FINALIZATION_DELAY", 5*time.Minute, 0, 24*time.Hour)

	diagnostics := &http.Server{
		Addr: envOr("METRIC_DIAGNOSTICS_ADDR", ":19090"), Handler: telemetry.DiagnosticsHandler(),
		ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second,
	}
	go func() {
		if err := diagnostics.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("metric_diagnostics_failed", "error_code", "METRIC_DIAGNOSTICS_SERVE_FAILED")
			cancel()
		}
	}()

	telemetry.MarkReady()
	logger.Info("metric_worker_started", "binding_count", len(bindings), "poll_interval", pollInterval.String(), "finalization_delay", finalizationDelay.String())
	runWorker(ctx, runtime, bindings, pollInterval, finalizationDelay, telemetry, logger)
	telemetry.MarkNotReady()
	shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = diagnostics.Shutdown(shutdownContext)
	_ = telemetry.Shutdown(shutdownContext)
	logger.Info("metric_worker_stopped")
}

func openRuntime(ctx context.Context) (*runtime, error) {
	config, err := pgxpool.ParseConfig(requiredEnv("METRIC_POSTGRES_DSN"))
	if err != nil {
		return nil, fmt.Errorf("parse metric PostgreSQL DSN: %w", err)
	}
	if config.ConnConfig.User != "metric_engine_runtime" {
		return nil, errors.New("metric PostgreSQL identity must be metric_engine_runtime")
	}
	config.MaxConns = 8
	config.MinConns = 1
	config.MaxConnLifetime = 30 * time.Minute
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	store, err := metric.NewPostgresStore(pool)
	if err != nil {
		pool.Close()
		return nil, err
	}
	series, err := metric.NewClickHouseStore(requiredEnv("METRIC_CLICKHOUSE_URL"), os.Getenv("METRIC_CLICKHOUSE_USER"), os.Getenv("METRIC_CLICKHOUSE_PASSWORD"), nil)
	if err != nil {
		store.Close()
		return nil, err
	}
	redisDatabase := integerEnv("METRIC_REDIS_DB", 0, 0, 1024)
	latest, err := metric.NewRedisLatestStore(requiredEnv("METRIC_REDIS_ADDR"), os.Getenv("METRIC_REDIS_PASSWORD"), redisDatabase, 7*24*time.Hour)
	if err != nil {
		store.Close()
		return nil, err
	}
	engine, err := metric.New(store, series, latest)
	if err != nil {
		_ = latest.Close()
		store.Close()
		return nil, err
	}
	return &runtime{store: store, engine: engine, latest: latest}, nil
}

func (runtime *runtime) Close() {
	if runtime == nil {
		return
	}
	if runtime.latest != nil {
		_ = runtime.latest.Close()
	}
	if runtime.store != nil {
		runtime.store.Close()
	}
}

func runWorker(ctx context.Context, runtime *runtime, bindings []scheduledBinding, pollInterval, finalizationDelay time.Duration, telemetry *observability.Runtime, logger *slog.Logger) {
	runScheduledBindings(ctx, runtime, bindings, finalizationDelay, telemetry, logger)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runScheduledBindings(ctx, runtime, bindings, finalizationDelay, telemetry, logger)
		}
	}
}

func runScheduledBindings(ctx context.Context, runtime *runtime, bindings []scheduledBinding, finalizationDelay time.Duration, telemetry *observability.Runtime, logger *slog.Logger) {
	now := time.Now().UTC()
	runScopeReconciliation(ctx, runtime, bindings, now.Add(-5*time.Minute), telemetry, logger)
	for _, binding := range bindings {
		if ctx.Err() != nil {
			return
		}
		scheduleContext, cancel := context.WithTimeout(ctx, 10*time.Second)
		schedule, err := runtime.store.LoadSchedule(scheduleContext, binding.TenantID, binding.SiteID, binding.BindingID, now)
		cancel()
		if err != nil {
			recordMetricWorkerResult(telemetry, "schedule_error")
			logger.Warn("metric_schedule_load_failed", "binding_id", binding.BindingID, "error_code", "METRIC_SCHEDULE_LOAD_FAILED")
			continue
		}
		period, err := metric.CompletedPeriod(schedule, now, finalizationDelay)
		if err != nil {
			if strings.EqualFold(strings.TrimSpace(schedule.Granularity), "REALTIME") {
				recordMetricWorkerResult(telemetry, "realtime_skipped")
				continue
			}
			recordMetricWorkerResult(telemetry, "period_error")
			logger.Warn("metric_period_invalid", "binding_id", binding.BindingID, "granularity", schedule.Granularity, "error_code", "METRIC_PERIOD_INVALID")
			continue
		}
		checkContext, checkCancel := context.WithTimeout(ctx, 10*time.Second)
		exists, err := runtime.store.HasActiveScheduledRun(checkContext, binding.TenantID, binding.SiteID, binding.BindingID, period)
		checkCancel()
		if err != nil {
			recordMetricWorkerResult(telemetry, "dedupe_error")
			logger.Warn("metric_scheduled_dedupe_failed", "binding_id", binding.BindingID, "error_code", "METRIC_SCHEDULE_DEDUPE_FAILED")
			continue
		}
		if exists {
			recordMetricWorkerResult(telemetry, "already_complete")
			continue
		}
		runContext, runCancel := context.WithTimeout(ctx, 45*time.Second)
		result, err := runtime.engine.Execute(runContext, metric.RunRequest{
			TenantID: binding.TenantID, SiteID: binding.SiteID, BindingID: binding.BindingID,
			PeriodStart: period.Start, PeriodEnd: period.End, Reason: "SCHEDULED",
		})
		runCancel()
		if err != nil {
			recordMetricWorkerResult(telemetry, "execute_error")
			logger.Warn("metric_scheduled_execution_failed", "binding_id", binding.BindingID, "period_start", period.Start, "period_end", period.End, "error_code", "METRIC_SCHEDULED_EXECUTION_FAILED")
			continue
		}
		recordMetricWorkerResult(telemetry, "persisted")
		logger.Info("metric_scheduled_result_persisted", "binding_id", binding.BindingID, "run_id", result.RunID, "result_id", result.ResultID, "period_start", period.Start, "period_end", period.End)
	}
}

func runScopeReconciliation(ctx context.Context, runtime *runtime, bindings []scheduledBinding, staleBefore time.Time, telemetry *observability.Runtime, logger *slog.Logger) {
	type scope struct{ tenantID, siteID string }
	seen := make(map[scope]struct{}, len(bindings))
	for _, binding := range bindings {
		key := scope{tenantID: binding.TenantID, siteID: binding.SiteID}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		reconcileContext, cancel := context.WithTimeout(ctx, 20*time.Second)
		repaired, err := runtime.engine.ReconcileScope(reconcileContext, key.tenantID, key.siteID, staleBefore, 100)
		cancel()
		if err != nil {
			recordMetricWorkerResult(telemetry, "reconcile_error")
			logger.Warn("metric_publication_reconcile_failed", "site_id", key.siteID, "error_code", "METRIC_PUBLICATION_RECONCILE_FAILED")
			continue
		}
		if repaired > 0 {
			recordMetricWorkerResult(telemetry, "reconciled")
			logger.Info("metric_publications_reconciled", "site_id", key.siteID, "repaired", repaired)
		}
	}
}

func recordMetricWorkerResult(telemetry *observability.Runtime, result string) {
	if telemetry == nil {
		return
	}
	_ = telemetry.Metrics.AddCounter("hvac_metric_worker_runs_total", "Metric worker schedule outcomes.", map[string]string{"result": result}, 1)
}

func parseScheduledBindings(raw string) ([]scheduledBinding, error) {
	var bindings []scheduledBinding
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&bindings); err != nil {
		return nil, err
	}
	if len(bindings) == 0 || len(bindings) > 4096 {
		return nil, errors.New("metric worker binding allowlist must contain 1..4096 entries")
	}
	seen := make(map[string]struct{}, len(bindings))
	for i := range bindings {
		bindings[i].TenantID = strings.TrimSpace(bindings[i].TenantID)
		bindings[i].SiteID = strings.TrimSpace(bindings[i].SiteID)
		bindings[i].BindingID = strings.TrimSpace(bindings[i].BindingID)
		if bindings[i].TenantID == "" || bindings[i].SiteID == "" || bindings[i].BindingID == "" {
			return nil, errors.New("metric worker binding identity is incomplete")
		}
		key := bindings[i].TenantID + "\x00" + bindings[i].SiteID + "\x00" + bindings[i].BindingID
		if _, duplicate := seen[key]; duplicate {
			return nil, errors.New("metric worker binding allowlist contains a duplicate")
		}
		seen[key] = struct{}{}
	}
	return bindings, nil
}

func runOneShot(ctx context.Context, runtime *runtime, input *os.File, output *os.File) error {
	var request request
	if err := json.NewDecoder(input).Decode(&request); err != nil {
		return err
	}
	switch strings.ToLower(strings.TrimSpace(request.Operation)) {
	case "", "execute":
		result, err := runtime.engine.Execute(ctx, metric.RunRequest{
			TenantID: request.TenantID, SiteID: request.SiteID, BindingID: request.BindingID,
			PeriodStart: request.PeriodStart, PeriodEnd: request.PeriodEnd, Reason: request.Reason,
		})
		if err != nil {
			return err
		}
		return json.NewEncoder(output).Encode(result)
	case "reconcile":
		staleBefore := request.StaleBefore
		if staleBefore.IsZero() {
			staleBefore = time.Now().UTC().Add(-5 * time.Minute)
		}
		repaired, err := runtime.engine.Reconcile(ctx, staleBefore, request.Limit)
		if err != nil {
			return err
		}
		return json.NewEncoder(output).Encode(map[string]any{"repaired": repaired, "staleBefore": staleBefore})
	default:
		return errors.New("unsupported metric operation")
	}
}

func requiredEnv(name string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		_, _ = os.Stderr.WriteString(name + " is required\n")
		os.Exit(1)
	}
	return value
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
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
