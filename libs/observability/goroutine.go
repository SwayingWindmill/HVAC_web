package observability

import (
	"log/slog"
	"runtime"
	"strings"
)

const maxPanicStackFrames = 16

type stackFrame struct {
	Function string `json:"function"`
	File     string `json:"file"`
	Line     int    `json:"line"`
}

func captureStackFrames(skip int) []stackFrame {
	pcs := make([]uintptr, maxPanicStackFrames)
	count := runtime.Callers(skip, pcs)
	if count == 0 {
		return nil
	}
	frames := runtime.CallersFrames(pcs[:count])
	result := make([]stackFrame, 0, count)
	for len(result) < maxPanicStackFrames {
		frame, more := frames.Next()
		result = append(result, stackFrame{
			Function: frame.Function,
			File:     frame.File,
			Line:     frame.Line,
		})
		if !more {
			break
		}
	}
	return result
}

// RecordHTTPPanic logs a recovered HTTP handler panic with bounded structured stack frames
// without leaking raw panic payloads, and increments the shared panic counter.
func RecordHTTPPanic(logger *slog.Logger, metrics *Registry, component string) {
	recordPanic(logger, metrics, component, "http_handler_panicked", "HTTP_HANDLER_PANIC_RECOVERED", 4)
}

func recordPanic(logger *slog.Logger, metrics *Registry, component, event, errorCode string, skip int) {
	component = strings.TrimSpace(component)
	if component == "" {
		component = "unknown"
	}
	if metrics != nil {
		_ = metrics.AddCounter("hvac_goroutine_panics_total", "Recovered background goroutine panics.", nil, 1)
	}
	if logger != nil {
		logger.Error(
			event,
			"component", component,
			"error_code", errorCode,
			"error", "[REDACTED]",
			"stack", captureStackFrames(skip),
		)
	}
}

// Recover catches a panic when invoked directly via `defer observability.Recover(...)`,
// records the event without leaking raw panic payloads, and runs optional cleanup hooks.
func Recover(logger *slog.Logger, metrics *Registry, component string, onPanic ...func(any)) {
	recovered := recover()
	if recovered == nil {
		return
	}
	recordPanic(logger, metrics, component, "background_goroutine_panicked", "GOROUTINE_PANIC_RECOVERED", 4)
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
