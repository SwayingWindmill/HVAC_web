package packettap

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func testGatewayUUIDv7(suffix int) string {
	return fmt.Sprintf("0194f000-2000-7000-8000-%012x", suffix)
}

func TestFormatPayloadPreview(t *testing.T) {
	t.Run("empty payload", func(t *testing.T) {
		sum := sha256.Sum256(nil)
		wantHash := hex.EncodeToString(sum[:])
		preview, encoding, truncated, shaHex := FormatPayloadPreview(nil, 0)
		if preview != "" || encoding != "empty" || truncated || shaHex != wantHash {
			t.Fatalf("got=(%q,%q,%v,%q) want=(\"\",\"empty\",false,%q)", preview, encoding, truncated, shaHex, wantHash)
		}
	})

	t.Run("ascii exact 4096 boundary and overflow", func(t *testing.T) {
		exact := bytes.Repeat([]byte("a"), MaxPayloadPreviewBytes)
		preview, encoding, truncated, _ := FormatPayloadPreview(exact, 0)
		if len(preview) != MaxPayloadPreviewBytes || encoding != "utf8" || truncated {
			t.Fatalf("exact 4096 failed: len=%d encoding=%q truncated=%v", len(preview), encoding, truncated)
		}

		over := bytes.Repeat([]byte("b"), MaxPayloadPreviewBytes+17)
		sum := sha256.Sum256(over)
		preview, encoding, truncated, shaHex := FormatPayloadPreview(over, 0)
		if len(preview) != MaxPayloadPreviewBytes || encoding != "utf8" || !truncated || shaHex != hex.EncodeToString(sum[:]) {
			t.Fatalf("over 4096 failed: len=%d encoding=%q truncated=%v sha=%q", len(preview), encoding, truncated, shaHex)
		}
	})

	t.Run("multi-byte chinese utf8 boundary", func(t *testing.T) {
		text := strings.Repeat("冷", 2000)
		raw := []byte(text)
		preview, encoding, truncated, _ := FormatPayloadPreview(raw, 10)
		if encoding != "utf8" || !truncated || !utf8.ValidString(preview) || len(preview) != 9 || preview != "冷冷冷" {
			t.Fatalf("chinese boundary failed: preview=%q len=%d encoding=%q truncated=%v", preview, len(preview), encoding, truncated)
		}

		tinyPreview, encoding, truncated, _ := FormatPayloadPreview(raw, 2)
		if encoding != "utf8" || !truncated || tinyPreview != "" {
			t.Fatalf("maxBytes smaller than single rune failed: preview=%q encoding=%q truncated=%v", tinyPreview, encoding, truncated)
		}
	})

	t.Run("nul byte switches to hex", func(t *testing.T) {
		raw := []byte("abc\x00def")
		preview, encoding, truncated, _ := FormatPayloadPreview(raw, MaxPayloadPreviewBytes)
		if encoding != "hex" || truncated || preview != hex.EncodeToString(raw) {
			t.Fatalf("nul payload failed: preview=%q encoding=%q truncated=%v", preview, encoding, truncated)
		}
	})

	t.Run("invalid utf8 switches to hex and truncates safely", func(t *testing.T) {
		raw := append([]byte{0xff, 0xfe, 0x80}, bytes.Repeat([]byte{0xab}, 3000)...)
		sum := sha256.Sum256(raw)
		preview, encoding, truncated, shaHex := FormatPayloadPreview(raw, MaxPayloadPreviewBytes)
		if encoding != "hex" || !truncated || len(preview) != MaxPayloadPreviewBytes || shaHex != hex.EncodeToString(sum[:]) {
			t.Fatalf("invalid utf8 failed: len=%d encoding=%q truncated=%v", len(preview), encoding, truncated)
		}
		if preview != hex.EncodeToString(raw[:MaxPayloadPreviewBytes/2]) {
			t.Fatalf("unexpected hex preview prefix")
		}

		oneByteHex, enc, trunc, _ := FormatPayloadPreview([]byte{0xff, 0x01}, 2)
		if oneByteHex != "ff" || enc != "hex" || !trunc {
			t.Fatalf("min 2-byte hex failed: preview=%q enc=%q trunc=%v", oneByteHex, enc, trunc)
		}
	})
}

func TestManagerZeroAllocWhenInactive(t *testing.T) {
	mgr := NewManager()
	gw := testGatewayUUIDv7(1)
	topic := "hvac/v1/" + gw + "/up/telemetry"
	payload := []byte(`{"temp":12.3}`)
	allocs := testing.AllocsPerRun(50, func() {
		mgr.Record(DirectionUplink, ChannelTelemetry, gw, topic, "trace-1", payload)
	})
	if allocs != 0 {
		t.Fatalf("inactive Record allocs = %.1f, want 0", allocs)
	}
}

func TestManagerRingBufferTTLStopAndCapacity(t *testing.T) {
	now := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	mgr := NewManager(WithNow(func() time.Time { return now }))

	if _, err := mgr.Start("not-a-uuid", 60); !errors.Is(err, ErrInvalidCaptureGateway) {
		t.Fatalf("invalid gateway err=%v, want ErrInvalidCaptureGateway", err)
	}
	gw1 := testGatewayUUIDv7(1)
	if _, err := mgr.Start(gw1, MinCaptureDurationSeconds-1); !errors.Is(err, ErrInvalidCaptureDuration) {
		t.Fatalf("too short duration err=%v, want ErrInvalidCaptureDuration", err)
	}
	if _, err := mgr.Start(gw1, MaxCaptureDurationSeconds+1); !errors.Is(err, ErrInvalidCaptureDuration) {
		t.Fatalf("too long duration err=%v, want ErrInvalidCaptureDuration", err)
	}

	snap, err := mgr.Start(strings.ToUpper(" "+gw1+" "), 0)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if !snap.Active || snap.GatewayID != gw1 || snap.DurationSeconds == nil || *snap.DurationSeconds != 300 || snap.MaxPackets != DefaultMaxPackets || snap.Items == nil || len(snap.Items) != 0 {
		t.Fatalf("unexpected initial snapshot: %+v", snap)
	}

	for i := 1; i <= DefaultMaxPackets+25; i++ {
		now = now.Add(time.Millisecond)
		mgr.Record(DirectionUplink, ChannelTelemetry, gw1, "hvac/v1/"+gw1+"/up/telemetry", fmt.Sprintf("trace-%d", i), []byte(fmt.Sprintf(`{"seq":%d}`, i)))
	}

	snap = mgr.Snapshot(gw1)
	if !snap.Active || snap.DroppedCount != 25 || len(snap.Items) != DefaultMaxPackets {
		t.Fatalf("ring buffer state invalid: active=%v dropped=%d len=%d", snap.Active, snap.DroppedCount, len(snap.Items))
	}
	if snap.Items[0].Sequence != 26 || snap.Items[len(snap.Items)-1].Sequence != DefaultMaxPackets+25 {
		t.Fatalf("unexpected sequence bounds: first=%d last=%d", snap.Items[0].Sequence, snap.Items[len(snap.Items)-1].Sequence)
	}
	snap.Items[0].Topic = "mutated"
	snap2 := mgr.Snapshot(gw1)
	if snap2.Items[0].Topic == "mutated" {
		t.Fatal("Snapshot did not deep copy items")
	}

	stopped := mgr.Stop(gw1)
	if stopped.Active || len(stopped.Items) != DefaultMaxPackets || stopped.DroppedCount != 25 {
		t.Fatalf("stopped snapshot mismatch: %+v", stopped)
	}
	mgr.Record(DirectionUplink, ChannelTelemetry, gw1, "hvac/v1/"+gw1+"/up/telemetry", "after-stop", []byte(`{}`))
	now = now.Add(4*time.Minute + 59*time.Second)
	if stillThere := mgr.Snapshot(gw1); stillThere.Active || len(stillThere.Items) != DefaultMaxPackets {
		t.Fatalf("stopped snapshot lost before 5m: active=%v len=%d", stillThere.Active, len(stillThere.Items))
	}
	now = now.Add(time.Second)
	if pruned := mgr.Snapshot(gw1); pruned.Active || len(pruned.Items) != 0 || pruned.StartedAt != nil {
		t.Fatalf("stopped snapshot not dropped after 5m: %+v", pruned)
	}

	gwTTL := testGatewayUUIDv7(2)
	if _, err := mgr.Start(gwTTL, 30); err != nil {
		t.Fatalf("Start ttl session: %v", err)
	}
	mgr.Record(DirectionDownlink, ChannelCommand, gwTTL, "hvac/v1/"+gwTTL+"/down/command", "tr-cmd", []byte("cmd"))
	now = now.Add(30 * time.Second)
	expired := mgr.Snapshot(gwTTL)
	if expired.Active || len(expired.Items) != 1 || expired.Items[0].Direction != DirectionDownlink || expired.Items[0].Channel != ChannelCommand {
		t.Fatalf("expired snapshot mismatch: %+v", expired)
	}
	now = now.Add(5 * time.Minute)
	if afterRetention := mgr.Snapshot(gwTTL); afterRetention.StartedAt != nil || len(afterRetention.Items) != 0 {
		t.Fatalf("expired snapshot not pruned after 5m retention: %+v", afterRetention)
	}

	for i := 10; i < 10+MaxActiveCaptureSessions; i++ {
		if _, err := mgr.Start(testGatewayUUIDv7(i), 60); err != nil {
			t.Fatalf("Start session %d failed: %v", i, err)
		}
	}
	if _, err := mgr.Start(testGatewayUUIDv7(10), 120); err != nil {
		t.Fatalf("restarting active gateway should succeed under cap: %v", err)
	}
	if _, err := mgr.Start(testGatewayUUIDv7(999), 60); !errors.Is(err, ErrCaptureCapacityExceeded) {
		t.Fatalf("33rd active gateway err=%v, want ErrCaptureCapacityExceeded", err)
	}
	mgr.Stop(testGatewayUUIDv7(10))
	if _, err := mgr.Start(testGatewayUUIDv7(999), 60); err != nil {
		t.Fatalf("Start after freeing slot failed: %v", err)
	}
}

func TestSnapshotJSONContract(t *testing.T) {
	now := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	mgr := NewManager(WithNow(func() time.Time { return now }))
	gw := testGatewayUUIDv7(77)

	emptyRaw, err := json.Marshal(mgr.Snapshot(gw))
	if err != nil {
		t.Fatalf("marshal empty snapshot: %v", err)
	}
	var emptyObj map[string]any
	if err := json.Unmarshal(emptyRaw, &emptyObj); err != nil {
		t.Fatalf("unmarshal empty snapshot: %v", err)
	}
	items, ok := emptyObj["items"].([]any)
	if !ok || len(items) != 0 {
		t.Fatalf("expected non-null empty items array, got %#v", emptyObj["items"])
	}
	if _, hasStarted := emptyObj["startedAt"]; hasStarted {
		t.Fatalf("inactive never-started snapshot should omit startedAt: %s", string(emptyRaw))
	}

	if _, err := mgr.Start(gw, 60); err != nil {
		t.Fatalf("Start: %v", err)
	}
	mgr.Record(DirectionUplink, ChannelQuarantine, gw, "hvac/v1/"+gw+"/up/telemetry", "", []byte("bad"))
	populatedRaw, err := json.Marshal(mgr.Snapshot(gw))
	if err != nil {
		t.Fatalf("marshal populated snapshot: %v", err)
	}
	var popObj map[string]any
	if err := json.Unmarshal(populatedRaw, &popObj); err != nil {
		t.Fatalf("unmarshal populated snapshot: %v", err)
	}
	popItems := popObj["items"].([]any)
	if len(popItems) != 1 {
		t.Fatalf("expected 1 packet, got %d", len(popItems))
	}
	pkt := popItems[0].(map[string]any)
	for _, requiredKey := range []string{"sequence", "capturedAt", "direction", "channel", "topic", "payloadBytes", "payloadSha256", "payloadPreview", "payloadEncoding", "payloadTruncated"} {
		if _, exists := pkt[requiredKey]; !exists {
			t.Fatalf("missing OpenAPI field %q in %s", requiredKey, string(populatedRaw))
		}
	}
	if _, hasTrace := pkt["traceId"]; hasTrace {
		t.Fatalf("empty traceId should be omitted: %s", string(populatedRaw))
	}
}
