package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/services/notification-service/pkg/notificationservice"
)

func TestAlarmDeliveryIsOffByDefault(t *testing.T) {
	t.Setenv("ALARM_DELIVERY_MODE", "")
	dispatcher, err := newAlarmDeliveryDispatcher(slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err != nil || dispatcher != nil {
		t.Fatalf("off: %v %v", dispatcher, err)
	}
	t.Setenv("ALARM_DELIVERY_MODE", "loud")
	if _, err := newAlarmDeliveryDispatcher(slog.New(slog.NewTextHandler(io.Discard, nil)), nil); err == nil {
		t.Fatal("an unknown mode was accepted")
	}
}

func TestAlarmDeliveryRefusesToStartWithBrokenFiles(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dir := t.TempDir()
	t.Setenv("ALARM_DELIVERY_MODE", "dry-run")
	t.Setenv("ALARM_ROUTING_DIR", filepath.Join("..", "..", "deploy", "sites"))
	t.Setenv("ALARM_DELIVERY_RECIPIENTS_FILE", filepath.Join(dir, "recipients.json"))
	t.Setenv("ALARM_SEND_URL", "http://wecom-alert-adapter:9095/send")
	t.Setenv("ALARM_SEND_TOKEN_FILE", filepath.Join(dir, "alarm-send-token"))
	if err := os.WriteFile(filepath.Join(dir, "alarm-send-token"), []byte("local-only-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newAlarmDeliveryDispatcher(logger, nil); err == nil {
		t.Fatal("started without a recipients file")
	}
	// The routing files in Git must load against the example recipients, or a deploy would stop energy-api.
	example, err := os.ReadFile(filepath.Join("..", "..", "deploy", "platform", "phase1", "alarm-delivery.example", "recipients.json.example"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "recipients.json"), example, 0o600); err != nil {
		t.Fatal(err)
	}
	dispatcher, err := newAlarmDeliveryDispatcher(logger, nil)
	if err != nil {
		t.Fatalf("repository routing with the example recipients: %v", err)
	}
	if dispatcher.Mode != notificationservice.DeliveryDryRun || dispatcher.Routing.Len() == 0 {
		t.Fatalf("dispatcher: mode %s sites %d", dispatcher.Mode, dispatcher.Routing.Len())
	}
	if _, ok := dispatcher.Routing.Site("01A14000-1000-7000-8000-000000000001"); !ok {
		t.Fatal("松雅轩 is not routed")
	}
	t.Setenv("ALARM_SEND_URL", "")
	if _, err := newAlarmDeliveryDispatcher(logger, nil); err == nil {
		t.Fatal("started without ALARM_SEND_URL")
	}
	t.Setenv("ALARM_SEND_URL", "http://wecom-alert-adapter:9095/send")
	if err := os.WriteFile(filepath.Join(dir, "alarm-send-token"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newAlarmDeliveryDispatcher(logger, nil); err == nil {
		t.Fatal("started with an empty send token")
	}
	if err := os.WriteFile(filepath.Join(dir, "alarm-send-token"), []byte("local-only-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "recipients.json"), []byte(`{"recipients":{"songyaxuan-duty":{"name":"x","email":"a@example.com"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newAlarmDeliveryDispatcher(logger, nil); err == nil {
		t.Fatal("started with a routing file naming a missing recipient")
	}
}

func TestAlarmRoutingReloaderMetricsAndDispatcher(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dir := t.TempDir()
	recipientsPath := filepath.Join(dir, "recipients.json")
	routingDir := filepath.Join(dir, "routing")
	if err := os.MkdirAll(routingDir, 0o755); err != nil {
		t.Fatal(err)
	}
	exampleRecipients, err := os.ReadFile(filepath.Join("..", "..", "deploy", "platform", "phase1", "alarm-delivery.example", "recipients.json.example"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recipientsPath, exampleRecipients, 0o600); err != nil {
		t.Fatal(err)
	}
	siteRouting, err := os.ReadFile(filepath.Join("..", "..", "deploy", "sites", "songyaxuan.alarm-routing.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(routingDir, "songyaxuan.alarm-routing.json"), siteRouting, 0o600); err != nil {
		t.Fatal(err)
	}
	tokenFile := filepath.Join(dir, "alarm-send-token")
	if err := os.WriteFile(tokenFile, []byte("local-only-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("ALARM_DELIVERY_MODE", "dry-run")
	t.Setenv("ALARM_DELIVERY_RECIPIENTS_FILE", recipientsPath)
	t.Setenv("ALARM_ROUTING_DIR", routingDir)
	t.Setenv("ALARM_SEND_URL", "http://wecom-alert-adapter:9095/send")
	t.Setenv("ALARM_SEND_TOKEN_FILE", tokenFile)

	registry := observability.NewRegistry()
	reloader, err := newAlarmRoutingReloader(logger, registry)
	if err != nil || reloader == nil {
		t.Fatalf("newAlarmRoutingReloader failed: reloader=%v err=%v", reloader, err)
	}
	initRec := httptest.NewRecorder()
	registry.Handler().ServeHTTP(initRec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	initBody := initRec.Body.String()
	for _, want := range []string{
		`hvac_alarm_routing_reloads_total{outcome="success"} 0`,
		`hvac_alarm_routing_reloads_total{outcome="failed"} 0`,
	} {
		if !strings.Contains(initBody, want) {
			t.Fatalf("missing zero-initialized metric %q in:\n%s", want, initBody)
		}
	}
	dispatcher, err := newAlarmDeliveryDispatcherWithReloader(logger, nil, reloader)
	if err != nil || dispatcher == nil || dispatcher.Reloader != reloader {
		t.Fatalf("expected dispatcher with reloader, got dispatcher=%v err=%v", dispatcher, err)
	}

	// Valid change triggers success reload metric.
	updatedRouting := strings.Replace(string(siteRouting), `"holdMinutes": 3`, `"holdMinutes": 6`, 1)
	if updatedRouting == string(siteRouting) {
		updatedRouting = strings.Replace(string(siteRouting), `"holdMinutes":3`, `"holdMinutes":6`, 1)
	}
	if updatedRouting == string(siteRouting) {
		t.Fatal("failed to mutate holdMinutes in siteRouting fixture")
	}
	if err := os.WriteFile(filepath.Join(routingDir, "songyaxuan.alarm-routing.json"), []byte(updatedRouting), 0o600); err != nil {
		t.Fatal(err)
	}
	changed, _, err := reloader.ReloadNow(t.Context())
	if err != nil || !changed {
		t.Fatalf("expected changed success reload, got changed=%v err=%v", changed, err)
	}

	// Corrupt routing file triggers failed metric while preserving valid snapshot.
	if err := os.WriteFile(filepath.Join(routingDir, "songyaxuan.alarm-routing.json"), []byte("{bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := reloader.ReloadNow(t.Context()); err == nil {
		t.Fatal("expected invalid routing file error")
	}

	rec := httptest.NewRecorder()
	registry.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	for _, want := range []string{
		`hvac_alarm_routing_reloads_total{outcome="success"} 1`,
		`hvac_alarm_routing_reloads_total{outcome="failed"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing metric %q in:\n%s", want, body)
		}
	}
}

func TestAlarmRoutingReloaderSkipsMissingFilesWhenDeliveryOffEvenWithEnvVars(t *testing.T) {
	dir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	t.Setenv("ALARM_DELIVERY_MODE", "off")
	t.Setenv("ALARM_DELIVERY_RECIPIENTS_FILE", filepath.Join(dir, "missing-recipients.json"))
	t.Setenv("ALARM_ROUTING_DIR", filepath.Join(dir, "missing-routing-dir"))

	reloader, err := newAlarmRoutingReloader(logger, observability.NewRegistry())
	if err != nil || reloader != nil {
		t.Fatalf("expected (nil, nil) when delivery is off and paths do not exist, got reloader=%v err=%v", reloader, err)
	}
}
