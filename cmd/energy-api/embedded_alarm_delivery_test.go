package main

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

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
	if dispatcher.Mode != notificationservice.DeliveryDryRun {
		t.Fatalf("dispatcher mode %s", dispatcher.Mode)
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
}
