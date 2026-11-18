package adapter

import (
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eclipse/paho.golang/paho"
	"github.com/quanlaihe/hvac-web/libs/observability"
)

func TestGatewayMailboxByteLimitSheddingAndMidStreamCompaction(t *testing.T) {
	dir := t.TempDir()
	frameBytes := int64(len(publish(testGatewayA, 1).Topic)+len(publish(testGatewayA, 1).Payload)+2) + walFrameHeader
	mb := newGatewayMailbox(testGatewayA, 1, 64, frameBytes*3, dir)
	mb.compactMinBytes = frameBytes * 2
	t.Cleanup(func() { mb.close(true) })

	for i := 1; i <= 3; i++ {
		res, err := mb.enqueue(publish(testGatewayA, i))
		if err != nil || res.shed {
			t.Fatalf("enqueue %d unexpected res=%+v err=%v", i, res, err)
		}
	}
	shedRes, err := mb.enqueue(publish(testGatewayA, 4))
	if err != nil || !shedRes.shed {
		t.Fatalf("expected byte limit shed on msg 4, got res=%+v err=%v", shedRes, err)
	}

	batch1, _, _, errs := mb.popBatch(2)
	if errs != 0 || len(batch1) != 2 {
		t.Fatalf("popBatch=%d errs=%d, want 2 items and 0 errors", len(batch1), errs)
	}
	if got := string(batch1[0].packet.Payload); !strings.Contains(got, messageID(1)) {
		t.Fatalf("expected msg 1 first, got %s", got)
	}
	// Mark first 2 frames consumed while msg 3 still sits active in spool -> mid-stream compaction runs.
	mb.markConsumed(batch1[1].endSeq)
	stat, err := os.Stat(mb.spoolPath)
	if err != nil {
		t.Fatalf("stat spool after compaction: %v", err)
	}
	if stat.Size() != walHeaderSize+frameBytes {
		t.Fatalf("compacted spool size=%d, want %d", stat.Size(), walHeaderSize+frameBytes)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 1 {
		t.Fatalf("expected only compacted spool file in %s, entries=%v err=%v", dir, entries, err)
	}

	// Now capacity opened up for msg 4 alongside remaining msg 3; FIFO delivers 3 then 4.
	if res, err := mb.enqueue(publish(testGatewayA, 4)); err != nil || res.shed {
		t.Fatalf("enqueue msg 4 after compaction failed res=%+v err=%v", res, err)
	}
	batch2, _, _, errs := mb.popBatch(4)
	if errs != 0 || len(batch2) != 2 {
		t.Fatalf("popBatch2=%d errs=%d, want 2", len(batch2), errs)
	}
	if !strings.Contains(string(batch2[0].packet.Payload), messageID(3)) || !strings.Contains(string(batch2[1].packet.Payload), messageID(4)) {
		t.Fatalf("expected FIFO msgs [3 4], got [%s %s]", string(batch2[0].packet.Payload), string(batch2[1].packet.Payload))
	}
	mb.markConsumed(batch2[1].endSeq)
	stat, err = os.Stat(mb.spoolPath)
	if err != nil || stat.Size() != walHeaderSize {
		t.Fatalf("expected header-only spool after drain, size=%d err=%v", stat.Size(), err)
	}
}

func TestGatewayMailboxCorruptFrameHeadQuarantinedWhileIOErrorPreservesFIFO(t *testing.T) {
	dir := t.TempDir()
	mb := newGatewayMailbox(testGatewayA, 1, 8, 1<<20, dir)
	t.Cleanup(func() { mb.close(true) })

	for i := 1; i <= 5; i++ {
		if res, err := mb.enqueue(publish(testGatewayA, i)); err != nil || res.shed {
			t.Fatalf("enqueue %d unexpected res=%+v err=%v", i, res, err)
		}
	}
	// Pop msg 1 (in RAM) and promote msg 2 into RAM, leaving msgs 3, 4, 5 on disk overflow.
	first, _, ovf, errs := mb.popBatch(1)
	if errs != 0 || len(first) != 1 || ovf != 3 {
		t.Fatalf("first pop len=%d ovf=%d errs=%d", len(first), ovf, errs)
	}
	mb.markConsumed(first[0].endSeq)

	// Pop msg 2 from RAM (and promote msg 3 -> wait: corrupt msg 3 BEFORE popping msg 2,
	// Wait: popBatch(1) pops 2 and tries to promote 3). Actually, corrupt msg 3 on disk now:
	frameBytes := first[0].endSeq
	msg3Offset := walHeaderSize + 2*frameBytes
	var badCRC [4]byte
	binary.BigEndian.PutUint32(badCRC[:], crc32.ChecksumIEEE([]byte("wrong")))
	if _, err := mb.spoolFile.WriteAt(badCRC[:], msg3Offset+4); err != nil {
		t.Fatalf("corrupt crc write: %v", err)
	}

	// popBatch(1) pops msg 2 from RAM cleanly and leaves corrupt msg 3 at overflow head.
	second, _, _, errs := mb.popBatch(1)
	if len(second) != 1 || errs != 0 {
		t.Fatalf("second pop len=%d errs=%d, want len=1 errs=0", len(second), errs)
	}
	mb.markConsumed(second[0].endSeq)

	// Now overflow[0] is corrupt at batch head (len(items)==0): drop corrupt 3 and return [4, 5].
	next, _, _, errs := mb.popBatch(4)
	if errs != 1 || len(next) != 2 {
		t.Fatalf("expected 1 dropped corrupt frame + [4 5], got len=%d errs=%d", len(next), errs)
	}
	if !strings.Contains(string(next[0].packet.Payload), messageID(4)) || !strings.Contains(string(next[1].packet.Payload), messageID(5)) {
		t.Fatalf("expected FIFO [4 5] after dropping corrupt frame 3")
	}
	mb.markConsumed(next[1].endSeq)
	if stat, statErr := os.Stat(mb.spoolPath); statErr != nil || stat.Size() != walHeaderSize {
		t.Fatalf("expected header-only spool after drain, size=%v err=%v", stat, statErr)
	}

	// Verify trailing corrupt-only frame at overflow head advances consumedLogical and truncates spool.
	mbTail := newGatewayMailbox(testGatewayA+"-tail", 1, 8, 1<<20, dir)
	t.Cleanup(func() { mbTail.close(true) })
	for i := 1; i <= 3; i++ {
		if _, err := mbTail.enqueue(publish(testGatewayA, i)); err != nil {
			t.Fatal(err)
		}
	}
	b1, _, _, _ := mbTail.popBatch(1)
	mbTail.markConsumed(b1[0].endSeq)
	if _, err := mbTail.spoolFile.WriteAt(badCRC[:], walHeaderSize+2*frameBytes+4); err != nil {
		t.Fatal(err)
	}
	b2, _, _, _ := mbTail.popBatch(1)
	mbTail.markConsumed(b2[0].endSeq)
	b3, _, ovfTail, e3 := mbTail.popBatch(2)
	if len(b3) != 0 || ovfTail != 0 || e3 != 1 {
		t.Fatalf("trailing corrupt frame expected len=0 ovf=0 errs=1, got len=%d ovf=%d errs=%d", len(b3), ovfTail, e3)
	}
	mbTail.close(false)
	recovered, count, recErr := recoverSpoolMailboxes(dir, 1, 8, 1<<20)
	if recErr != nil || count != 0 || len(recovered) != 0 {
		t.Fatalf("expected 0 recovered frames after skipped trailing corrupt frame, got count=%d len=%d err=%v", count, len(recovered), recErr)
	}
	if stat, statErr := os.Stat(mbTail.spoolPath); statErr != nil || stat.Size() != walHeaderSize {
		t.Fatalf("expected truncated header-only spool after recovery of skipped tail, size=%v err=%v", stat, statErr)
	}

	// Simulate transient I/O error (closed spoolFile fd) with valid overflow entry queued behind RAM.
	mbIO := newGatewayMailbox(testGatewayB, 1, 8, 1<<20, dir)
	for i := 1; i <= 3; i++ {
		if _, err := mbIO.enqueue(publish(testGatewayB, i)); err != nil {
			t.Fatal(err)
		}
	}
	io1, _, _, _ := mbIO.popBatch(1)
	mbIO.markConsumed(io1[0].endSeq)
	_ = mbIO.spoolFile.Close()
	broken, _, ovfLeft, ioErrs := mbIO.popBatch(1)
	if len(broken) != 1 || ioErrs != 1 || ovfLeft != 1 {
		t.Fatalf("expected I/O failure on promote to halt FIFO without dropping msg 3: len=%d ioErrs=%d ovf=%d", len(broken), ioErrs, ovfLeft)
	}
}

func TestRuntimeRecoversSpoolMailboxesOnRestart(t *testing.T) {
	dir := t.TempDir()
	mb := newGatewayMailbox(testGatewayA, 2, 16, 1<<20, dir)
	for i := 1; i <= 4; i++ {
		if res, err := mb.enqueue(publish(testGatewayA, i)); err != nil || res.shed {
			t.Fatalf("enqueue %d failed res=%+v err=%v", i, res, err)
		}
	}
	first, _, _, _ := mb.popBatch(1)
	mb.markConsumed(first[0].endSeq)
	mb.close(false) // simulate abrupt shutdown with msgs 2, 3, 4 persisted unconsumed in WAL

	telemetry := &fakeTelemetry{}
	rt, err := NewRuntime(Config{
		MQTT:             MQTTConfig{BrokerURL: "tls://localhost:8883", ClientID: "connectivity-test", CAFile: "ca.pem", CertFile: "client.pem", KeyFile: "client.key", ServerName: "mqtt.local"},
		TelemetryRuntime: TelemetryRuntimeConfig{BaseURL: "https://telemetry.local", CAFile: "ca.pem", CertFile: "client.pem", KeyFile: "client.key", ServerName: "telemetry.local"},
		QueueCapacity:    2,
		OverflowDir:      dir,
	}, newTestProcessor(t, newFakeIdentities(), telemetry), slog.New(slog.NewTextHandler(io.Discard, nil)), observability.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	rt.retryDelay = func(int) time.Duration { return 5 * time.Millisecond }
	ctx, cancel := context.WithCancel(t.Context())
	var closeOnce sync.Once
	cleanup := func() {
		closeOnce.Do(func() {
			cancel()
			rt.workers.Wait()
			rt.closeMailboxes()
		})
	}
	t.Cleanup(cleanup)

	if err := rt.recoverOverflowMailboxes(ctx); err != nil {
		t.Fatalf("recoverOverflowMailboxes failed: %v", err)
	}
	waitCondition(t, 3*time.Second, func() bool {
		return len(telemetry.accepted()) == 3
	}, "recovered WAL frames drained in FIFO order")
	accepted := telemetry.accepted()
	for idx, wantOffset := range []int64{2, 3, 4} {
		if accepted[idx].SourcePosition.Offset != wantOffset {
			t.Fatalf("recovered[%d] offset=%d, want %d", idx, accepted[idx].SourcePosition.Offset, wantOffset)
		}
	}
	spoolPath := filepath.Join(dir, safeGatewayFileName(testGatewayA))
	waitCondition(t, 2*time.Second, func() bool {
		stat, err := os.Stat(spoolPath)
		return err == nil && stat.Size() == walHeaderSize
	}, "recovered spool truncated back to header after drain")
}

func testReplayPublish(gatewayID string, index int, compact bool) *paho.Publish {
	msgID := messageID(index)
	if compact {
		return &paho.Publish{
			Topic:   telemetryTopic(gatewayID),
			Payload: []byte(fmt.Sprintf(`{"v":"1.0","gw":%q,"seq":%d,"ts":1786352400000,"replay":true,"devs":{"METER-01":{"pts":{"active_power":12.5}}}}`, gatewayID, index)),
			QoS:     1,
		}
	}
	return &paho.Publish{
		Topic:   telemetryTopic(gatewayID),
		Payload: []byte(fmt.Sprintf(`{"schemaVersion":"2.0","messageId":%q,"gatewayId":%q,"timestamp":1786352400000,"sequence":%d,"replay":true,"payload":{"devices":[{"deviceId":"METER-01","deviceTimestamp":1786352399000,"points":[{"code":"active_power","value":126.4,"quality":0,"unit":"kW"}]}]}}`, msgID, gatewayID, index)),
		QoS:     1,
	}
}

func testLivePublishAt(gatewayID string, index int, sampledAt time.Time) *paho.Publish {
	ts := sampledAt.UTC().UnixMilli()
	return &paho.Publish{
		Topic:   telemetryTopic(gatewayID),
		Payload: []byte(fmt.Sprintf(`{"schemaVersion":"2.0","messageId":%q,"gatewayId":%q,"timestamp":%d,"sequence":%d,"replay":false,"payload":{"devices":[{"deviceId":"METER-01","deviceTimestamp":%d,"points":[{"code":"active_power","value":126.4,"quality":0,"unit":"kW"}]}]}}`, messageID(index), gatewayID, ts, index, ts)),
		QoS:     1,
	}
}

func TestGatewayMailboxDualLanePriorityAndStarvationGuard(t *testing.T) {
	dir := t.TempDir()
	mb := newGatewayMailbox(testGatewayA, 4, 4096, 32<<20, dir)
	mb.autoBackfillLag = 10 * time.Minute
	t.Cleanup(func() { mb.close(true) })

	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

	// Enqueue 2,500 replay messages (explicit v2 replay, compact v1 replay, and stale lag > 10m).
	const replayCount = 2500
	for i := 1; i <= replayCount; i++ {
		var pub *paho.Publish
		switch {
		case i%10 == 0:
			pub = testLivePublishAt(testGatewayA, i, now.Add(-10*time.Minute-time.Second))
		case i%5 == 0:
			pub = testReplayPublish(testGatewayA, i, true)
		default:
			pub = testReplayPublish(testGatewayA, i, false)
		}
		res, err := mb.enqueueAt(pub, now)
		if err != nil || res.shed {
			t.Fatalf("replay enqueue %d unexpected res=%+v err=%v", i, res, err)
		}
	}

	replaySpool := filepath.Join(dir, safeGatewayReplayFileName(testGatewayA))
	liveSpool := filepath.Join(dir, safeGatewayFileName(testGatewayA))
	if _, err := os.Stat(replaySpool); err != nil {
		t.Fatalf("expected replay spool %s to exist: %v", replaySpool, err)
	}
	if _, err := os.Stat(liveSpool); !os.IsNotExist(err) {
		t.Fatalf("expected live spool %s to stay absent until live enqueue, err=%v", liveSpool, err)
	}

	// Enqueue 20 live messages: event, status, exact 10m boundary telemetry, and fresh telemetry.
	const liveCount = 20
	for i := 1; i <= liveCount; i++ {
		seq := replayCount + i
		var pub *paho.Publish
		switch {
		case i == 1:
			pub = &paho.Publish{Topic: eventTopic(testGatewayA), Payload: eventPayload(testGatewayA, messageID(seq), seq), QoS: 1}
		case i == 2:
			pub = &paho.Publish{Topic: "hvac/v1/" + testGatewayA + "/up/status", Payload: []byte(`{"online":true}`), QoS: 1}
		case i == 3:
			pub = testLivePublishAt(testGatewayA, seq, now.Add(-10*time.Minute))
		default:
			pub = testLivePublishAt(testGatewayA, seq, now.Add(-time.Second))
		}
		res, err := mb.enqueueAt(pub, now)
		if err != nil || res.shed {
			t.Fatalf("live enqueue %d unexpected res=%+v err=%v", i, res, err)
		}
		if res.memDepth > mb.memCap {
			t.Fatalf("combined memDepth=%d exceeded memCap=%d", res.memDepth, mb.memCap)
		}
	}
	if _, err := os.Stat(liveSpool); err != nil {
		t.Fatalf("expected live spool %s to exist after live enqueue: %v", liveSpool, err)
	}

	// Pop the first 25 items and verify exact fairness schedule:
	// 8 live, 1 replay, 8 live, 1 replay, 4 live, then replay resumes.
	batch, _, _, errs := mb.popBatch(25)
	if errs != 0 || len(batch) != 25 {
		t.Fatalf("popBatch(25) len=%d errs=%d, want 25 and 0", len(batch), errs)
	}
	wantReplayAt := map[int]bool{8: true, 17: true, 22: true, 23: true, 24: true}
	for idx, item := range batch {
		if item.isReplay != wantReplayAt[idx] {
			t.Fatalf("batch[%d].isReplay=%v, want %v (topic=%s)", idx, item.isReplay, wantReplayAt[idx], item.packet.Topic)
		}
	}
	mb.markBatchConsumed(batch)

	// Drain remaining replay messages.
	drained := len(batch)
	for drained < replayCount+liveCount {
		more, _, _, popErrs := mb.popBatch(256)
		if popErrs != 0 {
			t.Fatalf("unexpected pop read errors: %d", popErrs)
		}
		if len(more) == 0 {
			break
		}
		for _, item := range more {
			if !item.isReplay {
				t.Fatalf("unexpected live item after all live items drained")
			}
		}
		mb.markBatchConsumed(more)
		drained += len(more)
	}
	if drained != replayCount+liveCount {
		t.Fatalf("drained=%d, want %d", drained, replayCount+liveCount)
	}

	for _, p := range []string{liveSpool, replaySpool} {
		stat, err := os.Stat(p)
		if err != nil || stat.Size() != walHeaderSize {
			t.Fatalf("spool %s size=%v err=%v, want %d", p, stat, err, walHeaderSize)
		}
	}
}

func TestGatewayMailboxRecoversBothLiveAndReplaySpoolsOnRestart(t *testing.T) {
	dir := t.TempDir()
	mb := newGatewayMailbox(testGatewayA, 4, 128, 32<<20, dir)
	for i := 1; i <= 30; i++ {
		if res, err := mb.enqueue(testReplayPublish(testGatewayA, i, false)); err != nil || res.shed {
			t.Fatalf("enqueue replay %d failed: %+v %v", i, res, err)
		}
	}
	for i := 101; i <= 112; i++ {
		if res, err := mb.enqueue(publish(testGatewayA, i)); err != nil || res.shed {
			t.Fatalf("enqueue live %d failed: %+v %v", i, res, err)
		}
	}
	// Consume first 2 live items before simulating abrupt restart.
	first, _, _, errs := mb.popBatch(2)
	if errs != 0 || len(first) != 2 {
		t.Fatalf("first pop len=%d errs=%d", len(first), errs)
	}
	mb.markBatchConsumed(first)
	mb.close(false)

	recovered, count, err := recoverSpoolMailboxes(dir, 4, 128, 32<<20)
	if err != nil {
		t.Fatalf("recoverSpoolMailboxes: %v", err)
	}
	if count != 40 || len(recovered) != 1 {
		t.Fatalf("recovered count=%d gateways=%d, want 40 and 1", count, len(recovered))
	}
	recMB := recovered[testGatewayA]
	t.Cleanup(func() { recMB.close(true) })

	memD, ovfD := recMB.depth()
	if memD > 4 || memD+ovfD != 40 {
		t.Fatalf("recovered depth=(mem=%d, ovf=%d), want mem<=4 and total=40", memD, ovfD)
	}

	batch, _, _, errs := recMB.popBatch(15)
	if errs != 0 || len(batch) != 15 {
		t.Fatalf("recovered pop len=%d errs=%d", len(batch), errs)
	}
	// Expect 8 live, 1 replay, 2 remaining live, 4 replay.
	for i := 0; i < 8; i++ {
		if batch[i].isReplay {
			t.Fatalf("batch[%d] unexpectedly replay", i)
		}
	}
	if !batch[8].isReplay {
		t.Fatalf("batch[8] expected starvation-guard replay")
	}
	if batch[9].isReplay || batch[10].isReplay {
		t.Fatalf("batch[9..10] expected remaining live items")
	}
	for i := 11; i < 15; i++ {
		if !batch[i].isReplay {
			t.Fatalf("batch[%d] expected replay", i)
		}
	}
}

func TestGatewayMailboxSharedBoundsAcrossLiveAndReplay(t *testing.T) {
	dir := t.TempDir()
	mb := newGatewayMailbox(testGatewayA, 2, 3, 1<<20, dir)
	t.Cleanup(func() { mb.close(true) })

	for i := 1; i <= 3; i++ {
		res, err := mb.enqueue(testReplayPublish(testGatewayA, i, false))
		if err != nil || res.shed {
			t.Fatalf("replay %d failed: %+v %v", i, res, err)
		}
	}
	for i := 4; i <= 5; i++ {
		res, err := mb.enqueue(publish(testGatewayA, i))
		if err != nil || res.shed {
			t.Fatalf("live %d failed: %+v %v", i, res, err)
		}
		if res.memDepth > 2 {
			t.Fatalf("memDepth=%d exceeded cap 2", res.memDepth)
		}
	}
	shedRes, err := mb.enqueue(publish(testGatewayA, 6))
	if err != nil || !shedRes.shed {
		t.Fatalf("expected 6th combined message to shed, got res=%+v err=%v", shedRes, err)
	}
}
