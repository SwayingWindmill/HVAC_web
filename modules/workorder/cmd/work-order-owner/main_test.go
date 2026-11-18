package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/observability"
)

func TestWorkOrderDiagnosticsReadinessAndDependencyProbe(t *testing.T) {
	telemetry := observability.NewRuntime(observability.RuntimeConfig{Service: "work-order-service"})
	defer func() { _ = telemetry.Shutdown(context.Background()) }()

	var pgErr atomic.Bool
	telemetry.SetDependencies(observability.Dependency{
		Name:     "postgres",
		Required: true,
		Check: func(context.Context) error {
			if pgErr.Load() {
				return errors.New("postgres down")
			}
			return nil
		},
	})

	srv := newWorkOrderDiagnosticsServer(":19089", telemetry)
	if srv.Addr != ":19089" {
		t.Fatalf("diagnostics addr = %q, want :19089", srv.Addr)
	}

	req := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}

	if rec := req("/health/ready"); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("initial /health/ready status = %d, want 503", rec.Code)
	}

	telemetry.MarkReady()
	if rec := req("/health/ready"); rec.Code != http.StatusOK {
		t.Fatalf("ready /health/ready status = %d, want 200", rec.Code)
	}
	diagRec := req("/diagnostics")
	if diagRec.Code != http.StatusOK {
		t.Fatalf("/diagnostics status = %d, want 200", diagRec.Code)
	}
	var diag map[string]any
	if err := json.Unmarshal(diagRec.Body.Bytes(), &diag); err != nil {
		t.Fatalf("decode /diagnostics: %v", err)
	}
	if diag["service"] != "work-order-service" {
		t.Fatalf("service = %v, want work-order-service", diag["service"])
	}

	pgErr.Store(true)
	if rec := req("/health/ready"); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("degraded /health/ready status = %d, want 503", rec.Code)
	}

	pgErr.Store(false)
	telemetry.MarkNotReady()
	if rec := req("/health/ready"); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("not-ready /health/ready status = %d, want 503", rec.Code)
	}
}

func TestShutdownWorkOrderOwnerReverseOrder(t *testing.T) {
	var order []string
	diagDone := make(chan struct{})
	shutdownWorkOrderOwner(workOrderShutdownDeps{
		markNotReady:   func() { order = append(order, "mark_not_ready") },
		shutdownServer: func(context.Context) error { order = append(order, "server_shutdown"); return nil },
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
	})
	want := "mark_not_ready,server_shutdown,diagnostics_shutdown,telemetry_shutdown"
	if got := strings.Join(order, ","); got != want {
		t.Fatalf("shutdown order = %s, want %s", got, want)
	}
}

func TestWorkOrderOwnerRequiredEnvAndRunFailure(t *testing.T) {
	t.Setenv("WORK_ORDER_TLS_CERT", "")
	if _, err := requiredEnv("WORK_ORDER_TLS_CERT"); err == nil {
		t.Fatal("expected error for missing WORK_ORDER_TLS_CERT")
	}
	if code := run(); code != 1 {
		t.Fatalf("run() = %d, want 1 when WORK_ORDER_TLS_CERT is missing", code)
	}
}
