package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/modules/command/pkg/commandservice"
)

func TestLoadRequiredValueFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime-value")
	if err := os.WriteFile(path, []byte("database-reference-fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	value, err := loadRequiredValueFile(path, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if value != "database-reference-fixture" {
		t.Fatalf("value=%q", value)
	}
}

func TestLoadRequiredValueFileRejectsInvalidContentAndSize(t *testing.T) {
	for _, test := range []struct {
		name    string
		content string
		maximum int64
	}{
		{name: "empty", content: "", maximum: 1024},
		{name: "multiple-lines", content: "first\nsecond", maximum: 1024},
		{name: "nul", content: "first\x00second", maximum: 1024},
		{name: "oversized", content: strings.Repeat("x", 32), maximum: 16},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "runtime-value")
			if err := os.WriteFile(path, []byte(test.content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadRequiredValueFile(path, test.maximum); err == nil {
				t.Fatal("expected value file to fail closed")
			}
		})
	}
}

func TestCommandControlFailureNotifierConfigurationAndHTTPDelivery(t *testing.T) {
	t.Setenv("ALARM_CONTROL_EVENTS_URL", "")
	t.Setenv("ALARM_SERVICE_URL", "")
	t.Setenv("ALARM_SERVICE_SERVER_CA", "")
	if sender := buildCommandControlFailureSender(slog.New(slog.NewTextHandler(io.Discard, nil))); sender != nil {
		t.Fatal("expected nil sender when no alarm endpoint env var is set")
	}

	t.Setenv("ALARM_SERVICE_URL", "https://alarm-owner:8448/")
	if got := resolveCommandControlEventsEndpoint(); got != "https://alarm-owner:8448/internal/v1/alarms/control-events" {
		t.Fatalf("resolved fallback endpoint=%q", got)
	}
	t.Setenv("ALARM_CONTROL_EVENTS_URL", "https://alarm-owner:8448/custom/control-events")
	if got := resolveCommandControlEventsEndpoint(); got != "https://alarm-owner:8448/custom/control-events" {
		t.Fatalf("resolved explicit endpoint=%q", got)
	}
	if sender := buildCommandControlFailureSender(slog.New(slog.NewTextHandler(io.Discard, nil))); sender != nil {
		t.Fatal("expected nil sender when ALARM_SERVICE_SERVER_CA is missing")
	}

	var received commandControlEventPayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/internal/v1/alarms/control-events" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	sender := newHTTPControlFailureSender(server.URL+"/internal/v1/alarms/control-events", server.Client(), nil)
	if sender == nil {
		t.Fatal("expected HTTP sender")
	}
	if err := sender(context.Background(), commandservice.ControlFailureEvent{
		Kind:       "OUTCOME_UNKNOWN",
		TenantID:   "018f3d00-0000-7000-8000-000000000001",
		SiteID:     "018f3e00-1000-7000-8000-000000000001",
		DeviceID:   "018f3e00-3000-7000-8000-000000000001",
		PointID:    "018f3e00-4000-7000-8000-000000000001",
		CommandID:  "018f3e00-9999-7000-8000-000000000001",
		Capability: commandmodel.CapabilityStart,
		ReasonCode: "LEASE_EXPIRED_WITHOUT_SEND_PROOF",
		OccurredAt: time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("HTTP sender err=%v", err)
	}
	if received.SchemaVersion != 1 || received.EventKind != "OUTCOME_UNKNOWN" || received.ActorID != "command-owner" ||
		received.DeviceID == nil || *received.DeviceID != "018f3e00-3000-7000-8000-000000000001" ||
		received.PointID == nil || *received.PointID != "018f3e00-4000-7000-8000-000000000001" {
		t.Fatalf("unexpected delivered payload: %+v", received)
	}

	cfg := runtimeHTTPConfig(commandservice.NewPostgresStore(nil, nil, nil), nil, func(ctx context.Context, event commandservice.ControlFailureEvent) {
		_ = sender(ctx, event)
	})
	if cfg.ControlFailureNotifier == nil {
		t.Fatal("expected runtimeHTTPConfig to forward ControlFailureNotifier")
	}
}

func TestCommandControlFailureSenderDrainsBodyForKeepAlive(t *testing.T) {
	t.Setenv("COMMAND_TLS_CERT", "")
	if code := run(); code != 1 {
		t.Fatalf("run() = %d, want 1 when COMMAND_TLS_CERT is missing", code)
	}
	t.Setenv("COMMAND_EMERGENCY_REVOCATION_REVISION", "not-a-uint")
	if _, err := parseUint64Env("COMMAND_EMERGENCY_REVOCATION_REVISION"); err == nil {
		t.Fatal("expected error for invalid uint64 env")
	}

	var newConns atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"status":"accepted","padding":"` + strings.Repeat("a", 1024) + `"}`))
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			newConns.Add(1)
		}
	}
	server.Start()
	defer server.Close()

	sender := newHTTPControlFailureSender(server.URL+"/internal/v1/alarms/control-events", server.Client(), nil)
	event := commandservice.ControlFailureEvent{
		Kind:       "OUTCOME_UNKNOWN",
		TenantID:   "018f3d00-0000-7000-8000-000000000001",
		SiteID:     "018f3e00-1000-7000-8000-000000000001",
		CommandID:  "018f3e00-9999-7000-8000-000000000001",
		Capability: commandmodel.CapabilityStart,
		OccurredAt: time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC),
	}
	if err := sender(context.Background(), event); err != nil {
		t.Fatalf("first send err = %v", err)
	}
	if err := sender(context.Background(), event); err != nil {
		t.Fatalf("second send err = %v", err)
	}
	if got := newConns.Load(); got != 1 {
		t.Fatalf("new connections = %d, want 1 (keep-alive connection reused after draining response body)", got)
	}
}
