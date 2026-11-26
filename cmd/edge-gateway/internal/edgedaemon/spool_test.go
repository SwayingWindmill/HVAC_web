package edgedaemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

func TestGradedSpoolFourTierTelemetryPriorityOrder(t *testing.T) {
	spool, err := OpenGradedSpool(t.TempDir(), 4096)
	if err != nil {
		t.Fatalf("OpenGradedSpool: %v", err)
	}
	for _, item := range []SpoolItem{
		{ID: "low-1", Topic: "hvac/v1/gw/up/telemetry", QoS: 1, Class: EvidenceTelemetryLow, Payload: []byte("low")},
		{ID: "med-1", Topic: "hvac/v1/gw/up/telemetry", QoS: 1, Class: EvidenceTelemetryMedium, Payload: []byte("med")},
		{ID: "high-1", Topic: "hvac/v1/gw/up/telemetry", QoS: 1, Class: EvidenceTelemetryHigh, Payload: []byte("high")},
		{ID: "vh-1", Topic: "hvac/v1/gw/up/telemetry", QoS: 1, Class: EvidenceTelemetryVeryHigh, Payload: []byte("vh")},
		{ID: "alarm-1", Topic: "hvac/v1/gw/up/event", QoS: 1, Class: EvidenceAlarm, Payload: []byte("alarm")},
	} {
		if _, err := spool.Admit(item); err != nil {
			t.Fatalf("admit %s: %v", item.ID, err)
		}
	}

	var order []string
	drained, err := spool.Drain(t.Context(), 10, func(_ context.Context, item SpoolItem) error {
		order = append(order, item.ID)
		return nil
	})
	if err != nil || drained != 5 {
		t.Fatalf("Drain = (%d, %v), want (5, nil)", drained, err)
	}
	want := []string{"alarm-1", "vh-1", "high-1", "med-1", "low-1"}
	for i, w := range want {
		if order[i] != w {
			t.Fatalf("drain order = %v, want %v", order, want)
		}
	}
}

func TestGradedSpoolDrainPreventsReentrancy(t *testing.T) {
	spool, err := OpenGradedSpool(t.TempDir(), 4096)
	if err != nil {
		t.Fatalf("OpenGradedSpool: %v", err)
	}
	if _, err := spool.Admit(SpoolItem{
		ID:      "tel-reentrant",
		Topic:   "hvac/v1/gw/up/telemetry",
		QoS:     1,
		Class:   EvidenceTelemetryHigh,
		Payload: []byte(`{"test":1}`),
	}); err != nil {
		t.Fatalf("Admit: %v", err)
	}

	calls := 0
	drained, err := spool.Drain(t.Context(), 10, func(ctx context.Context, _ SpoolItem) error {
		calls++
		nestedDrained, nestedErr := spool.Drain(ctx, 10, func(context.Context, SpoolItem) error {
			calls++
			return nil
		})
		if nestedErr != nil || nestedDrained != 0 {
			t.Fatalf("nested Drain = (%d, %v), want (0, nil)", nestedDrained, nestedErr)
		}
		return nil
	})
	if err != nil || drained != 1 {
		t.Fatalf("Drain = (%d, %v), want (1, nil)", drained, err)
	}
	if calls != 1 {
		t.Fatalf("publish calls = %d, want 1", calls)
	}
	if len(spool.Pending()) != 0 {
		t.Fatalf("expected spool empty after drain, got %d", len(spool.Pending()))
	}
}

func TestGradedSpoolWALReducesWriteAmplification(t *testing.T) {
	dir := t.TempDir()
	const count = 200
	const payloadSize = 512

	spool, err := OpenGradedSpool(dir, 256<<10)
	if err != nil {
		t.Fatalf("OpenGradedSpool: %v", err)
	}
	payload := bytes.Repeat([]byte("x"), payloadSize)
	for i := range count {
		if _, err := spool.Admit(SpoolItem{
			ID:      fmt.Sprintf("item-%03d", i),
			Topic:   "hvac/v1/gw/up/telemetry",
			QoS:     1,
			Class:   EvidenceTelemetryMedium,
			Payload: payload,
		}); err != nil {
			t.Fatalf("Admit %d: %v", i, err)
		}
	}

	drained, err := spool.Drain(t.Context(), count+10, func(context.Context, SpoolItem) error {
		return nil
	})
	if err != nil || drained != count {
		t.Fatalf("Drain = (%d, %v), want (%d, nil)", drained, err, count)
	}
	if spool.UsedBytes() != 0 || len(spool.Pending()) != 0 {
		t.Fatalf("expected empty spool after full drain, got used=%d pending=%d", spool.UsedBytes(), len(spool.Pending()))
	}
	if _, err := os.Stat(filepath.Join(dir, "mqtt-spool.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected mqtt-spool.json not to exist, got err=%v", err)
	}
	walInfo, err := os.Stat(filepath.Join(dir, "mqtt-spool.wal"))
	if err != nil {
		t.Fatalf("stat mqtt-spool.wal: %v", err)
	}
	if walInfo.Size() > 16<<10 {
		t.Fatalf("expected drained WAL size <= 16 KiB before explicit Compact, got %d", walInfo.Size())
	}
	if err := spool.Compact(); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	walInfoAfter, err := os.Stat(filepath.Join(dir, "mqtt-spool.wal"))
	if err != nil {
		t.Fatalf("stat mqtt-spool.wal after Compact: %v", err)
	}
	if walInfoAfter.Size() != 0 {
		t.Fatalf("expected drained compacted WAL to be 0 bytes, got %d", walInfoAfter.Size())
	}

	// Legacy whole-file JSON rewrite wrote O(N^2) bytes: sum_{k=1..200} (k * ~700B) twice (admit + remove) > 25 MB.
	// Append-only WAL + threshold compaction must stay well under 512 KB for 200 x 512B items.
	const maxAllowedWALBytes = 512 << 10
	if spool.diskBytesWritten == 0 || spool.diskBytesWritten >= maxAllowedWALBytes {
		t.Fatalf("diskBytesWritten = %d, expected in (0, %d)", spool.diskBytesWritten, maxAllowedWALBytes)
	}
}

func TestGradedSpoolMigratesLegacyJSONSnapshot(t *testing.T) {
	dir := t.TempDir()
	legacy := spoolDiskState{
		Items: map[string]SpoolItem{
			"tel-1": {
				ID:      "tel-1",
				Topic:   "hvac/v1/gw/up/telemetry",
				QoS:     1,
				Class:   EvidenceTelemetryMedium,
				Payload: []byte(`{"v":1}`),
			},
			"ota-1": {
				ID:      "ota-1",
				Topic:   "hvac/v1/gw/up/ota",
				QoS:     1,
				Class:   EvidenceOTAResult,
				Payload: []byte(`{"ota":"ok"}`),
			},
		},
		Order: []string{"tel-1", "ota-1"},
	}
	raw, err := json.MarshalIndent(legacy, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(dir, "mqtt-spool.json")
	if err := os.WriteFile(legacyPath, append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	spool, err := OpenGradedSpool(dir, 4096)
	if err != nil {
		t.Fatalf("OpenGradedSpool migration failed: %v", err)
	}
	if _, err := os.Stat(legacyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected legacy mqtt-spool.json to be removed after migration, got err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "mqtt-spool.wal")); err != nil {
		t.Fatalf("expected mqtt-spool.wal after migration: %v", err)
	}

	pending := spool.Pending()
	if len(pending) != 2 || pending[0].ID != "ota-1" || pending[1].ID != "tel-1" {
		t.Fatalf("migrated Pending = %+v, want [ota-1 tel-1]", pending)
	}
	if spool.UsedBytes() != int64(len(`{"v":1}`)+len(`{"ota":"ok"}`)) {
		t.Fatalf("UsedBytes after migration = %d", spool.UsedBytes())
	}

	reopened, err := OpenGradedSpool(dir, 4096)
	if err != nil {
		t.Fatalf("reopen migrated WAL failed: %v", err)
	}
	if len(reopened.Pending()) != 2 {
		t.Fatalf("expected 2 items after reopening migrated WAL, got %d", len(reopened.Pending()))
	}
}

func TestGradedSpoolRecoversTruncatedTrailingWALLines(t *testing.T) {
	dir := t.TempDir()
	spool, err := OpenGradedSpool(dir, 4096)
	if err != nil {
		t.Fatalf("OpenGradedSpool: %v", err)
	}
	for _, id := range []string{"tel-1", "tel-2", "tel-3"} {
		if _, err := spool.Admit(SpoolItem{
			ID:      id,
			Topic:   "hvac/v1/gw/up/telemetry",
			QoS:     1,
			Class:   EvidenceTelemetryHigh,
			Payload: []byte(`{"id":"` + id + `"}`),
		}); err != nil {
			t.Fatalf("Admit %s: %v", id, err)
		}
	}
	if _, err := spool.Remove("tel-2"); err != nil {
		t.Fatalf("Remove tel-2: %v", err)
	}

	walPath := filepath.Join(dir, "mqtt-spool.wal")
	f, err := os.OpenFile(walPath, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"op":"admit","item":{"id":"corrupt-partial"`); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	_ = f.Close()

	reopened, err := OpenGradedSpool(dir, 4096)
	if err != nil {
		t.Fatalf("OpenGradedSpool on truncated WAL failed: %v", err)
	}
	pending := reopened.Pending()
	if len(pending) != 2 || pending[0].ID != "tel-1" || pending[1].ID != "tel-3" {
		t.Fatalf("recovered Pending = %+v, want [tel-1 tel-3]", pending)
	}

	if _, err := reopened.Admit(SpoolItem{
		ID:      "tel-4",
		Topic:   "hvac/v1/gw/up/telemetry",
		QoS:     1,
		Class:   EvidenceTelemetryHigh,
		Payload: []byte(`{"id":"tel-4"}`),
	}); err != nil {
		t.Fatalf("Admit tel-4 after recovery: %v", err)
	}
	reopened2, err := OpenGradedSpool(dir, 4096)
	if err != nil {
		t.Fatalf("second reopen failed: %v", err)
	}
	if len(reopened2.Pending()) != 3 {
		t.Fatalf("expected 3 items after append post-recovery, got %d", len(reopened2.Pending()))
	}
	raw, err := os.ReadFile(walPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "corrupt-partial") {
		t.Fatalf("truncated tail was not cleaned from WAL:\n%s", string(raw))
	}
}

func TestGradedSpoolWALCompactionThresholdAndShedPersistence(t *testing.T) {
	dir := t.TempDir()
	spool, err := OpenGradedSpool(dir, 120)
	if err != nil {
		t.Fatalf("OpenGradedSpool: %v", err)
	}
	if _, err := spool.Admit(SpoolItem{
		ID:      "low-shed-1",
		Topic:   "hvac/v1/gw/up/telemetry",
		QoS:     1,
		Class:   EvidenceTelemetryLow,
		Payload: bytes.Repeat([]byte("l"), 70),
	}); err != nil {
		t.Fatal(err)
	}
	adm, err := spool.Admit(SpoolItem{
		ID:      "high-1",
		Topic:   "hvac/v1/gw/up/telemetry",
		QoS:     1,
		Class:   EvidenceTelemetryHigh,
		Payload: bytes.Repeat([]byte("h"), 70),
	})
	if err != nil {
		t.Fatalf("Admit high-1: %v", err)
	}
	if len(adm.ShedIDs) != 1 || adm.ShedIDs[0] != "low-shed-1" {
		t.Fatalf("expected low-shed-1 shed, got %+v", adm.ShedIDs)
	}

	// Reopening after shed must keep low-shed-1 absent.
	reopened, err := OpenGradedSpool(dir, 120)
	if err != nil {
		t.Fatal(err)
	}
	pending := reopened.Pending()
	if len(pending) != 1 || pending[0].ID != "high-1" {
		t.Fatalf("expected only high-1 after WAL shed recovery, got %+v", pending)
	}

	// Exercise compaction threshold (>= 256 ops).
	bigSpool, err := OpenGradedSpool(t.TempDir(), 4096)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bigSpool.Admit(SpoolItem{
		ID: "steady-1", Topic: "hvac/v1/gw/up/telemetry", QoS: 1, Class: EvidenceTelemetryHigh, Payload: []byte("steady"),
	}); err != nil {
		t.Fatal(err)
	}
	for i := range 130 {
		id := fmt.Sprintf("churn-%03d", i)
		if _, err := bigSpool.Admit(SpoolItem{
			ID: id, Topic: "hvac/v1/gw/up/telemetry", QoS: 1, Class: EvidenceTelemetryMedium, Payload: []byte("p"),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := bigSpool.Remove(id); err != nil {
			t.Fatal(err)
		}
	}
	walRaw, err := os.ReadFile(filepath.Join(bigSpool.stateDir, "mqtt-spool.wal"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(walRaw)), "\n")
	if len(lines) >= 256 {
		t.Fatalf("expected WAL compaction to keep lines < 256 after churn, got %d lines", len(lines))
	}
	if len(bigSpool.Pending()) != 1 || bigSpool.Pending()[0].ID != "steady-1" {
		t.Fatalf("expected steady-1 preserved across compaction, got %+v", bigSpool.Pending())
	}
}
