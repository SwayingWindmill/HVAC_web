package observability

import (
	"log/slog"
	"strings"
)

// Recover catches a panic when invoked directly via `defer observability.Recover(...)`,
// records the event without leaking raw panic payloads, and runs optional cleanup hooks.
func Recover(logger *slog.Logger, metrics *Registry, component string, onPanic ...func(any)) {
	recovered := recover()
	if recovered == nil {
		return
	}
	component = strings.TrimSpace(component)
	if component == "" {
		component = "unknown"
	}
	if metrics != nil {
		_ = metrics.AddCounter("hvac_goroutine_panics_total", "Recovered background goroutine panics.", nil, 1)
	}
	if logger != nil {
		logger.Error("background_goroutine_panicked", "component", component, "error_code", "GOROUTINE_PANIC_RECOVERED", "error", "[REDACTED]")
	}
	for _, hook := range onPanic {
		if hook != nil {
			hook(recovered)
		}
	}
}

// GoSafe launches fn in a goroutine guarded by Recover so a single panic cannot crash the process.
func GoSafe(logger *slog.Logger, metrics *Registry, component string, fn func()) {
	if fn == nil {
		return
	}
	go func() {
		defer Recover(logger, metrics, component)
		fn()
	}()
}

// Go launches fn in a goroutine guarded by Recover and wired to the runtime's metrics registry.
func (runtime *Runtime) Go(logger *slog.Logger, component string, fn func()) {
	var metrics *Registry
	if runtime != nil {
		metrics = runtime.Metrics
	}
	GoSafe(logger, metrics, component, fn)
}
