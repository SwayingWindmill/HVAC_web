package servicehost

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
)

const (
	DefaultPanicRestartDelay       = time.Second
	DefaultTelemetryShutdownPeriod = 5 * time.Second
)

// EnvOrDefault returns the trimmed value of environment variable name or fallback if unset or blank.
func EnvOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

// RequiredEnv returns the trimmed value of environment variable name or an error when missing.
func RequiredEnv(name string) (string, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return value, nil
}

// DurationEnv parses a positive time.Duration from environment variable name, or returns fallback when unset.
func DurationEnv(name string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", name)
	}
	return parsed, nil
}

// BoundedIntEnv parses an integer in [minValue, maxValue] from environment variable name, or returns fallback when unset.
func BoundedIntEnv(name string, fallback, minValue, maxValue int) (int, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < minValue || parsed > maxValue {
		return 0, fmt.Errorf("%s must be an integer in [%d,%d]", name, minValue, maxValue)
	}
	return parsed, nil
}

// DefaultWorkerID returns a per-process worker identifier prefixed by hostname or fallback.
func DefaultWorkerID(fallback string) string {
	hostname, err := os.Hostname()
	if err != nil || strings.TrimSpace(hostname) == "" {
		hostname = fallback
	}
	hostname = strings.TrimSpace(hostname)
	suffix := make([]byte, 4)
	if _, randErr := rand.Read(suffix); randErr != nil {
		return fmt.Sprintf("%s-%d", hostname, os.Getpid())
	}
	return fmt.Sprintf("%s-%d-%s", hostname, os.Getpid(), hex.EncodeToString(suffix))
}

// NewTelemetryShutdown returns a once-guarded function that flushes telemetry within period.
func NewTelemetryShutdown(telemetry *observability.Runtime, period time.Duration) func() {
	if period <= 0 {
		period = DefaultTelemetryShutdownPeriod
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			if telemetry == nil {
				return
			}
			shutdownCtx, cancel := context.WithTimeout(context.Background(), period)
			defer cancel()
			_ = telemetry.Shutdown(shutdownCtx)
		})
	}
}

// RunSupervised runs fn in a panic-recovering loop under wg until fn returns normally or ctx is canceled.
func RunSupervised(ctx context.Context, wg *sync.WaitGroup, logger *slog.Logger, metrics *observability.Registry, component string, restartDelay time.Duration, fn func()) {
	if fn == nil {
		return
	}
	if restartDelay <= 0 {
		restartDelay = DefaultPanicRestartDelay
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
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
			timer := time.NewTimer(restartDelay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()
}

// StartServerShutdown asynchronously shuts down servers within timeout under panic recovery.
func StartServerShutdown(timeout time.Duration, logger *slog.Logger, metrics *observability.Registry, component string, servers ...*http.Server) (<-chan struct{}, func() error) {
	shutdownDone := make(chan struct{})
	var (
		mu       sync.Mutex
		firstErr error
	)
	go func() {
		defer close(shutdownDone)
		defer observability.Recover(logger, metrics, component, func(r any) {
			mu.Lock()
			if firstErr == nil {
				firstErr = fmt.Errorf("shutdown panicked: %v", r)
			}
			mu.Unlock()
		})
		if err := ShutdownServers(timeout, servers...); err != nil {
			mu.Lock()
			if firstErr == nil {
				firstErr = err
			}
			mu.Unlock()
		}
	}()
	return shutdownDone, func() error {
		mu.Lock()
		defer mu.Unlock()
		return firstErr
	}
}

// ShutdownServers gracefully shuts down each non-nil server within timeout, returning the first error.
func ShutdownServers(timeout time.Duration, servers ...*http.Server) error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var (
		mu       sync.Mutex
		firstErr error
		wg       sync.WaitGroup
	)
	for _, srv := range servers {
		if srv == nil {
			continue
		}
		wg.Add(1)
		go func(server *http.Server) {
			defer wg.Done()
			if err := server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
			}
		}(srv)
	}
	wg.Wait()
	return firstErr
}

// WaitBackground waits for wg up to timeout, logging a warning if draining times out.
func WaitBackground(wg *sync.WaitGroup, timeout time.Duration, logger *slog.Logger) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		wg.Wait()
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		if logger != nil {
			logger.Warn("service_background_wait_timeout", "error_code", "SERVICE_SHUTDOWN_DRAIN_TIMEOUT", "timeout", timeout.String())
		}
	}
}

// ShutdownRuntime drains HTTP servers before canceling background workers and flushing telemetry.
func ShutdownRuntime(
	logger *slog.Logger,
	telemetry *observability.Runtime,
	shutdownTimeout time.Duration,
	drainTimeout time.Duration,
	component string,
	httpWG *sync.WaitGroup,
	cancelWorkers context.CancelFunc,
	bgWG *sync.WaitGroup,
	shutdownTelemetry func(),
	servers ...*http.Server,
) error {
	var metrics *observability.Registry
	if telemetry != nil {
		telemetry.MarkNotReady()
		metrics = telemetry.Metrics
	}
	shutdownDone, shutdownResult := StartServerShutdown(shutdownTimeout, logger, metrics, component, servers...)
	<-shutdownDone
	shutdownErr := shutdownResult()
	if httpWG != nil {
		WaitBackground(httpWG, shutdownTimeout, logger)
	}
	if cancelWorkers != nil {
		cancelWorkers()
	}
	if bgWG != nil {
		WaitBackground(bgWG, drainTimeout, logger)
	}
	if shutdownTelemetry != nil {
		shutdownTelemetry()
	}
	return shutdownErr
}

// ServeAndWait starts server and optional diagnostics server, waits for signal cancellation or server failure,
// and returns the HTTP wait group plus any unexpected server error.
func ServeAndWait(
	sigCtx context.Context,
	stopSignals context.CancelFunc,
	logger *slog.Logger,
	metrics *observability.Registry,
	servicePrefix string,
	server *http.Server,
	diagnostics *http.Server,
	tlsHTTP bool,
) (*sync.WaitGroup, error) {
	serveErr := make(chan error, 2)
	var httpWG sync.WaitGroup
	if diagnostics != nil {
		httpWG.Add(1)
		go func() {
			defer httpWG.Done()
			defer observability.Recover(logger, metrics, servicePrefix+".diagnostics_server", func(any) {
				select {
				case serveErr <- errors.New("diagnostics server panicked"):
				default:
				}
			})
			if err := diagnostics.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				serveErr <- fmt.Errorf("diagnostics server: %w", err)
			}
		}()
	}
	if server != nil {
		httpWG.Add(1)
		go func() {
			defer httpWG.Done()
			defer observability.Recover(logger, metrics, servicePrefix+".http_server", func(any) {
				select {
				case serveErr <- errors.New("http server panicked"):
				default:
				}
			})
			if logger != nil {
				diagAddr := ""
				if diagnostics != nil {
					diagAddr = diagnostics.Addr
				}
				logger.Info(servicePrefix+"_service_started", "address", server.Addr, "diagnostics_address", diagAddr)
			}
			var err error
			if tlsHTTP {
				err = server.ListenAndServeTLS("", "")
			} else {
				err = server.ListenAndServe()
			}
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				serveErr <- fmt.Errorf("http server: %w", err)
			}
		}()
	}

	var runErr error
	select {
	case <-sigCtx.Done():
		if logger != nil {
			logger.Info(servicePrefix + "_service_shutting_down")
		}
	case err := <-serveErr:
		runErr = err
		if stopSignals != nil {
			stopSignals()
		}
	}
	return &httpWG, runErr
}
