package adapter

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

type Runtime struct {
	pipeline    *Pipeline
	interval    time.Duration
	pollTimeout time.Duration
	logger      *slog.Logger

	mu          sync.RWMutex
	lastSuccess time.Time
	lastError   string
}

func NewRuntime(pipeline *Pipeline, interval time.Duration, logger *slog.Logger) (*Runtime, error) {
	if pipeline == nil || interval < time.Second || interval > time.Minute {
		return nil, errors.New("adapter runtime configuration is invalid")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Runtime{
		pipeline:    pipeline,
		interval:    interval,
		pollTimeout: pollTimeoutFor(interval),
		logger:      logger,
	}, nil
}

func (runtime *Runtime) Run(ctx context.Context) error {
	runtime.poll(ctx)
	ticker := time.NewTicker(runtime.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			runtime.poll(ctx)
		}
	}
}

func (runtime *Runtime) Ready() bool {
	runtime.mu.RLock()
	defer runtime.mu.RUnlock()
	maximumAge := runtime.pollTimeout + 2*runtime.interval
	return runtime.lastError == "" && !runtime.lastSuccess.IsZero() && time.Since(runtime.lastSuccess) <= maximumAge
}

func (runtime *Runtime) poll(ctx context.Context) {
	pollContext, cancel := context.WithTimeout(ctx, runtime.pollTimeout)
	defer cancel()
	report, err := runtime.pipeline.PollOnce(pollContext)
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if err != nil {
		runtime.lastError = err.Error()
		runtime.logger.Warn("thingsboard_telemetry_poll_failed", "error", err.Error())
		return
	}
	runtime.lastSuccess = time.Now().UTC()
	runtime.lastError = ""
	runtime.logger.Info(
		"thingsboard_telemetry_poll_completed",
		"device_count", report.DeviceCount,
		"observation_count", report.ObservationCount,
		"status_counts", report.StatusCounts,
	)
}

func pollTimeoutFor(interval time.Duration) time.Duration {
	timeout := 6 * interval
	if timeout < 30*time.Second {
		return 30 * time.Second
	}
	if timeout > 5*time.Minute {
		return 5 * time.Minute
	}
	return timeout
}
