package main

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestShutdownEnergyProjectorWaitsForSubmeterAndDiagnostics(t *testing.T) {
	var (
		mu    sync.Mutex
		order []string
	)
	record := func(step string) {
		mu.Lock()
		defer mu.Unlock()
		order = append(order, step)
	}
	submeterReady := make(chan struct{})
	submeterDone := make(chan struct{})
	diagDone := make(chan struct{})
	go func() {
		<-submeterReady
		time.Sleep(10 * time.Millisecond)
		record("submeter_exited")
		close(submeterDone)
	}()

	shutdownEnergyProjector(energyProjectorShutdownDeps{
		markNotReady: func() {
			record("mark_not_ready")
			close(submeterReady)
		},
		submeterDone: submeterDone,
		shutdownDiag: func(context.Context) error {
			record("diagnostics_shutdown")
			close(diagDone)
			return nil
		},
		diagnosticsDone: diagDone,
		shutdownTelemetry: func(context.Context) error {
			record("telemetry_shutdown")
			return nil
		},
	})

	want := "mark_not_ready,submeter_exited,diagnostics_shutdown,telemetry_shutdown"
	mu.Lock()
	got := strings.Join(order, ",")
	mu.Unlock()
	if got != want {
		t.Fatalf("shutdown order = %s, want %s", got, want)
	}
}

func TestEnergyProjectorEnvHelpersAndRunFailure(t *testing.T) {
	t.Setenv("ANALYTICS_CLICKHOUSE_HTTP_URL", "")
	if _, err := requiredEnv("ANALYTICS_CLICKHOUSE_HTTP_URL"); err == nil {
		t.Fatal("expected error for missing required env")
	}
	if code := run(); code != 1 {
		t.Fatalf("run() = %d, want 1 when ANALYTICS_CLICKHOUSE_HTTP_URL is missing", code)
	}

	t.Setenv("ANALYTICS_PROJECTOR_BATCH_SIZE", "0")
	if _, err := integerEnv("ANALYTICS_PROJECTOR_BATCH_SIZE", 256, 1, 4096); err == nil {
		t.Fatal("expected error for out-of-range batch size")
	}
	t.Setenv("ANALYTICS_TOTAL_SUBMETER_INTERVAL", "1s")
	if _, err := durationEnv("ANALYTICS_TOTAL_SUBMETER_INTERVAL", 5*time.Minute, 10*time.Second, time.Hour); err == nil {
		t.Fatal("expected error for submeter interval below minimum")
	}
}
