package edgedaemon

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestGradedSpoolPrioritySheddingAndRecovery(t *testing.T) {
	dir := t.TempDir()
	spool, err := OpenGradedSpool(dir, 100)
	if err != nil {
		t.Fatalf("OpenGradedSpool: %v", err)
	}

	if _, err := spool.Admit(SpoolItem{
		ID: "tel-1", Topic: "hvac/v1/gw/up/telemetry", QoS: 1, Class: EvidenceTelemetryNormal,
		Payload: bytes.Repeat([]byte("a"), 60),
	}); err != nil {
		t.Fatalf("admit tel-1: %v", err)
	}

	adm, err := spool.Admit(SpoolItem{
		ID: "ota-1", Topic: "hvac/v1/gw/up/ota", QoS: 1, Class: EvidenceOTAResult,
		Payload: bytes.Repeat([]byte("b"), 50),
	})
	if err != nil {
		t.Fatalf("admit ota-1 should shed tel-1: %v", err)
	}
	if len(adm.ShedIDs) != 1 || adm.ShedIDs[0] != "tel-1" {
		t.Fatalf("expected tel-1 to be shed, got %+v", adm.ShedIDs)
	}

	// Protected evidence cannot be shed for even higher priority if capacity insufficient beyond lower tiers.
	if _, err := spool.Admit(SpoolItem{
		ID: "safe-1", Topic: "hvac/v1/gw/up/event", QoS: 1, Class: EvidenceSafety,
		Payload: bytes.Repeat([]byte("c"), 60),
	}); !errors.Is(err, ErrSpoolCapacity) {
		t.Fatalf("expected ErrSpoolCapacity protecting ota-1, got %v", err)
	}

	reopened, err := OpenGradedSpool(dir, 100)
	if err != nil {
		t.Fatalf("reopen spool: %v", err)
	}
	if _, err := reopened.Admit(SpoolItem{
		ID: "diag-1", Topic: "hvac/v1/gw/up/event", QoS: 1, Class: EvidenceDiagnostic,
		Payload: bytes.Repeat([]byte("d"), 40),
	}); err != nil {
		t.Fatalf("admit diag-1: %v", err)
	}

	var published []string
	drained, err := reopened.Drain(t.Context(), 10, func(_ context.Context, item SpoolItem) error {
		published = append(published, item.ID)
		return nil
	})
	if err != nil || drained != 2 {
		t.Fatalf("Drain = (%d, %v), want (2, nil)", drained, err)
	}
	if len(published) != 2 || published[0] != "ota-1" || published[1] != "diag-1" {
		t.Fatalf("Drain order = %v, want [ota-1 diag-1]", published)
	}
	if reopened.UsedBytes() != 0 {
		t.Fatalf("UsedBytes after drain = %d, want 0", reopened.UsedBytes())
	}
}
