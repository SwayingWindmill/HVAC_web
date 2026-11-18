package telemetry

import (
	"strings"
	"testing"
)

func TestRuntimeRetentionSQLCoversExtendedLifecycleTablesAndSafetyGuards(t *testing.T) {
	if !strings.Contains(pruneDeadHistoryEventsSQL, "dead_lettered_at < $1") || !strings.Contains(pruneDeadHistoryEventsSQL, "delivery_state = 'DEAD'") {
		t.Fatalf("unexpected dead history prune SQL: %s", pruneDeadHistoryEventsSQL)
	}
	if !strings.Contains(pruneDeadPublicationsSQL, "delivery_state = 'DEAD'") || !strings.Contains(pruneDeadPublicationsSQL, "alarm_dead_lettered_at < $1") {
		t.Fatalf("unexpected dead publication prune SQL: %s", pruneDeadPublicationsSQL)
	}
	if !strings.Contains(pruneMQTTRuntimeEventsSQL, "mqtt_runtime_events") || !strings.Contains(pruneMQTTRuntimeEventsSQL, "received_at < $1") {
		t.Fatalf("unexpected mqtt_runtime_events prune SQL: %s", pruneMQTTRuntimeEventsSQL)
	}
	if !strings.Contains(pruneIngestQuarantinesSQL, "ingest_quarantine") || !strings.Contains(pruneIngestQuarantinesSQL, "detected_at < $1") {
		t.Fatalf("unexpected ingest_quarantine prune SQL: %s", pruneIngestQuarantinesSQL)
	}
	if !strings.Contains(pruneRecoveryCursorsSQL, "recovery_cursors") || !strings.Contains(pruneRecoveryCursorsSQL, "expires_at < $1") {
		t.Fatalf("unexpected recovery_cursors prune SQL: %s", pruneRecoveryCursorsSQL)
	}
	if !strings.Contains(pruneSubscriptionsSQL, "NOT EXISTS") ||
		!strings.Contains(pruneSubscriptionsSQL, "telemetry_runtime.recovery_cursors") ||
		!strings.Contains(pruneSubscriptionsSQL, "telemetry_runtime.telemetry_publication_outbox") {
		t.Fatalf("telemetry_subscriptions prune SQL must guard FK references: %s", pruneSubscriptionsSQL)
	}
}
