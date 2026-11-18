package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestShutdownHistoryProjectorOrder(t *testing.T) {
	var order []string
	diagDone := make(chan struct{})
	shutdownHistoryProjector(historyProjectorShutdownDeps{
		markNotReady: func() { order = append(order, "mark_not_ready") },
		shutdownDiag: func(context.Context) error {
			order = append(order, "diagnostics_shutdown")
			close(diagDone)
			return nil
		},
		diagnosticsDone: diagDone,
		shutdownTelemetry: func(context.Context) error {
			order = append(order, "telemetry_shutdown")
			return nil
		},
		closeRepository: func() {
			order = append(order, "repository_close")
		},
	})
	want := "mark_not_ready,diagnostics_shutdown,telemetry_shutdown,repository_close"
	if got := strings.Join(order, ","); got != want {
		t.Fatalf("shutdown order = %s, want %s", got, want)
	}

	order = nil
	shutdownHistoryProjector(historyProjectorShutdownDeps{
		shutdownTelemetry: func(context.Context) error {
			order = append(order, "telemetry_shutdown")
			return nil
		},
		closeRepository: func() {
			order = append(order, "repository_close")
		},
	})
	wantStartupFail := "telemetry_shutdown,repository_close"
	if got := strings.Join(order, ","); got != wantStartupFail {
		t.Fatalf("startup-failure shutdown order = %s, want %s", got, wantStartupFail)
	}
}

func TestHistoryProjectorEnvHelpersAndRunFailure(t *testing.T) {
	t.Setenv("TELEMETRY_HISTORY_DATABASE_URL", "")
	if _, err := requiredEnv("TELEMETRY_HISTORY_DATABASE_URL"); err == nil {
		t.Fatal("expected error for missing required env")
	}
	if code := run(); code != 1 {
		t.Fatalf("run() = %d, want 1 when TELEMETRY_HISTORY_DATABASE_URL is missing", code)
	}

	t.Setenv("TELEMETRY_HISTORY_BATCH_SIZE", "999999")
	if _, err := integerEnv("TELEMETRY_HISTORY_BATCH_SIZE", 4096, 1, 4096); err == nil {
		t.Fatal("expected error for out-of-range integerEnv")
	}
	t.Setenv("TELEMETRY_HISTORY_POLL_INTERVAL", "bad-duration")
	if _, err := durationEnv("TELEMETRY_HISTORY_POLL_INTERVAL", 5*time.Second, 25*time.Millisecond, time.Minute); err == nil {
		t.Fatal("expected error for invalid durationEnv")
	}
}
