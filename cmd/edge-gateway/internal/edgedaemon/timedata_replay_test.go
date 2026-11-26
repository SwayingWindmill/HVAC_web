package edgedaemon

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eclipse/paho.golang/paho"
	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/adapter"
)

type replayStubAdapter struct {
	component edgecontrol.ComponentDescriptor
	channels  []edgecontrol.ChannelDescriptor
	samples   map[string]edgecontrol.Sample
}

func (a *replayStubAdapter) Component() edgecontrol.ComponentDescriptor {
	return a.component
}

func (a *replayStubAdapter) Channels() []edgecontrol.ChannelDescriptor {
	return slices.Clone(a.channels)
}

func (a *replayStubAdapter) Poll(_ context.Context, _ time.Time) ([]edgecontrol.ChannelUpdate, error) {
	out := make([]edgecontrol.ChannelUpdate, 0, len(a.channels))
	for _, ch := range a.channels {
		if s, ok := a.samples[ch.Address()]; ok {
			out = append(out, edgecontrol.ChannelUpdate{Address: ch.Address(), Sample: s})
		}
	}
	return out, nil
}

func (a *replayStubAdapter) Apply(_ context.Context, _ edgecontrol.ProcessImage, _ []edgecontrol.Decision) ([]edgecontrol.DeviceWriteResult, error) {
	return nil, nil
}

func TestTimedataReplayManagerStrictPriorityAndFailureSafety(t *testing.T) {
	stateDir := t.TempDir()
	timedataDir := filepath.Join(stateDir, "timedata")
	const gwID = "0193f000-2000-7000-8000-000000000401"

	td, err := edgecontrol.OpenFileTimedata(timedataDir, edgecontrol.PriorityLow)
	if err != nil {
		t.Fatal(err)
	}
	host, err := edgecontrol.NewHost()
	if err != nil {
		t.Fatal(err)
	}
	makeCh := func(code string, prio edgecontrol.DataPriority) edgecontrol.ChannelDescriptor {
		return edgecontrol.ChannelDescriptor{
			ComponentID:               "chiller-01",
			ChannelID:                 code,
			PointID:                   "chiller-01:" + code,
			DataType:                  edgecontrol.DataTypeDouble,
			Access:                    edgecontrol.AccessReadOnly,
			Unit:                      "kW",
			Category:                  edgecontrol.ChannelCategoryOpenemsType,
			PollPriority:              prio,
			LocalPersistencePriority:  prio,
			RemotePersistencePriority: prio,
			AggregationPriority:       prio,
			ResendPriority:            prio,
		}
	}
	stub := &replayStubAdapter{
		component: edgecontrol.ComponentDescriptor{
			ID:        "chiller-01",
			Enabled:   true,
			Kind:      edgecontrol.ComponentDeviceDriver,
			Type:      "DECLARATIVE_MODBUS_TCP",
			FactoryID: edgecontrol.DeclarativeModbusFactoryID,
			Version:   "1.0.0",
		},
		channels: []edgecontrol.ChannelDescriptor{
			makeCh("low_pt", edgecontrol.PriorityLow),
			makeCh("med_pt", edgecontrol.PriorityMedium),
			makeCh("high_pt", edgecontrol.PriorityHigh),
			makeCh("vh_pt", edgecontrol.PriorityVeryHigh),
		},
		samples: map[string]edgecontrol.Sample{},
	}
	if err := host.RegisterAdapter(stub); err != nil {
		t.Fatal(err)
	}
	if err := host.Start(nil); err != nil {
		t.Fatal(err)
	}
	if err := host.AttachTimedata(td); err != nil {
		t.Fatal(err)
	}

	curTime := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	newMgr := func() *TimedataReplayManager {
		mgr, mErr := NewTimedataReplayManager(TimedataReplayConfig{
			GatewayID: gwID,
			StateDir:  stateDir,
			Timedata:  td,
			BatchSize: 4,
			PointMetadata: func(address string) TimedataReplayPointMeta {
				desc, ok := host.ChannelDescriptor(address)
				if !ok {
					return TimedataReplayPointMeta{Priority: edgecontrol.PriorityMedium}
				}
				return TimedataReplayPointMeta{
					DeviceID:  desc.ComponentID,
					PointCode: desc.ChannelID,
					Unit:      desc.Unit,
					Priority:  normalizeTelemetryPriority(desc),
				}
			},
			Now: func() time.Time { return curTime },
		})
		if mErr != nil {
			t.Fatalf("NewTimedataReplayManager: %v", mErr)
		}
		return mgr
	}

	// Cycle 1 (online baseline): 4 records written & acked cleanly -> no replay needed.
	for _, code := range []string{"low_pt", "med_pt", "high_pt", "vh_pt"} {
		stub.samples["chiller-01/"+code] = edgecontrol.Sample{Value: edgecontrol.DoubleValue(1), Quality: edgecontrol.QualityGood, ObservedAt: curTime}
	}
	if _, err := host.RunCycle(t.Context(), curTime); err != nil {
		t.Fatal(err)
	}
	mgr := newMgr()
	if err := mgr.AdvanceBaseline(); err != nil {
		t.Fatal(err)
	}
	if snap := mgr.CursorSnapshot(); snap.AckedSequence != 4 || mgr.HasPendingReplay() {
		t.Fatalf("unexpected online baseline snapshot: %+v", snap)
	}

	// Cycles 2..4 (offline): 12 records written (seq 5..16), then spool sheds.
	for c := 1; c <= 3; c++ {
		curTime = curTime.Add(5 * time.Second)
		for _, code := range []string{"low_pt", "med_pt", "high_pt", "vh_pt"} {
			stub.samples["chiller-01/"+code] = edgecontrol.Sample{Value: edgecontrol.DoubleValue(float64(10 + c)), Quality: edgecontrol.QualityGood, ObservedAt: curTime}
		}
		if _, err := host.RunCycle(t.Context(), curTime); err != nil {
			t.Fatal(err)
		}
	}
	if err := mgr.MarkShed(); err != nil {
		t.Fatal(err)
	}
	if snap := mgr.CursorSnapshot(); len(snap.PendingWindows) != 1 || snap.PendingWindows[0] != (TimedataShedWindow{FromSeq: 5, ToSeq: 16}) {
		t.Fatalf("unexpected PendingWindows after MarkShed: %+v", snap.PendingWindows)
	}

	var mu sync.Mutex
	var published [][]byte
	failAfter := 2
	pubFn := func(_ context.Context, topic string, payload []byte) error {
		if topic != "hvac/v1/"+gwID+"/up/telemetry" {
			return errors.New("unexpected topic: " + topic)
		}
		mu.Lock()
		defer mu.Unlock()
		if failAfter > 0 && len(published) >= failAfter {
			return errors.New("simulated broker failure")
		}
		published = append(published, append([]byte(nil), payload...))
		return nil
	}

	// Fail on 3rd publish: only 2 VERY_HIGH batches succeed; 3rd VERY_HIGH batch must not advance cursor.
	if _, err := mgr.DrainBatch(t.Context(), 10, pubFn); err == nil {
		t.Fatal("expected DrainBatch to return publish error on 3rd batch")
	}
	snapMid := mgr.CursorSnapshot()
	if snapMid.AckedSequence != 4 || !mgr.HasPendingReplay() {
		t.Fatalf("AckedSequence must not advance before window finishes: %+v", snapMid)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "timedata-resend-cursor.json")); err != nil {
		t.Fatalf("expected timedata-resend-cursor.json to persist: %v", err)
	}

	// Reopen manager across simulated restart, clear failure, and drain in bounded ticks of 3 records.
	restarted := newMgr()
	mu.Lock()
	failAfter = 0
	mu.Unlock()
	for range 10 {
		res, dErr := restarted.DrainBatch(t.Context(), 3, pubFn)
		if dErr != nil {
			t.Fatalf("DrainBatch after recovery: %v", dErr)
		}
		if !res.HasMore {
			break
		}
	}
	if restarted.HasPendingReplay() {
		t.Fatal("expected all pending replay windows drained")
	}
	if snapFinal := restarted.CursorSnapshot(); snapFinal.AckedSequence != 16 {
		t.Fatalf("final AckedSequence = %d, want 16", snapFinal.AckedSequence)
	}

	// Verify strict VERY_HIGH -> HIGH -> MEDIUM -> LOW order and full Connectivity codec validity.
	codecs := adapter.DefaultCodecRegistry()
	var codes []string
	mu.Lock()
	allPayloads := append([][]byte(nil), published...)
	mu.Unlock()
	for i, raw := range allPayloads {
		decoded, decErr := codecs.DecodeTelemetry(raw, gwID)
		if decErr != nil {
			t.Fatalf("DecodeTelemetry[%d] failed: %v", i, decErr)
		}
		if decoded.Envelope.SchemaVersion != "2.0" || !decoded.Envelope.Replay {
			t.Fatalf("envelope[%d] schemaVersion=%q replay=%v", i, decoded.Envelope.SchemaVersion, decoded.Envelope.Replay)
		}
		for _, dev := range decoded.Envelope.Payload.Devices {
			for _, pt := range dev.Points {
				codes = append(codes, pt.Code)
			}
		}
	}
	wantOrder := []string{
		"vh_pt", "vh_pt", "vh_pt",
		"high_pt", "high_pt", "high_pt",
		"med_pt", "med_pt", "med_pt",
		"low_pt", "low_pt", "low_pt",
	}
	if !slices.Equal(codes, wantOrder) {
		t.Fatalf("replay priority order = %v, want %v", codes, wantOrder)
	}
}

func TestDaemonSpoolShedTriggersTimedataGapReplayOnReconnect(t *testing.T) {
	endpoint := startLocalModbusTCP(t, map[uint16]uint16{100: 70, 101: 120, 102: 200, 103: 300})
	uid, timeoutMS, retries := 1, 500, 0
	gwID := "0193f000-2000-7000-8000-000000000402"
	curTime := time.Date(2026, 11, 26, 11, 0, 0, 0, time.UTC)

	cfg := Config{
		GatewayID:     gwID,
		StateDir:      t.TempDir(),
		SpoolMaxBytes: 64 << 10,
		Drivers: []edgecontrol.ModbusDriverInput{{
			DeviceKey:  "chiller-01",
			DriverType: "MODBUS_TCP",
			Endpoint:   &endpoint,
			UnitID:     &uid,
			TimeoutMS:  &timeoutMS,
			Retries:    &retries,
			Registers: []edgecontrol.ModbusRegisterInput{
				{PointCode: "vh_temp", FunctionCode: 3, Address: 100, RegisterCount: 1, RawType: "UINT16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 0.1, PollPriority: "VERY_HIGH"},
				{PointCode: "high_temp", FunctionCode: 3, Address: 101, RegisterCount: 1, RawType: "UINT16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 0.1, PollPriority: "HIGH"},
				{PointCode: "med_temp", FunctionCode: 3, Address: 102, RegisterCount: 1, RawType: "UINT16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 0.1, PollPriority: "MEDIUM"},
				{PointCode: "low_temp", FunctionCode: 3, Address: 103, RegisterCount: 1, RawType: "UINT16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 0.1, PollPriority: "LOW"},
			},
		}},
	}
	cfg.ApplyDefaults("0.2.0")

	daemon, err := NewDaemon(cfg, DaemonOptions{Now: func() time.Time { return curTime }})
	if err != nil {
		t.Fatalf("NewDaemon: %v", err)
	}
	defer func() { _ = daemon.Close() }()

	// Online baseline: StepCycle + maintainMQTTWithPublisher advances AckedSequence past initial cycle.
	daemon.rotateSession(curTime)
	daemon.mqttHealthyGen.Store(1)
	daemon.mqttConnected.Store(true)
	if _, err := daemon.StepCycle(t.Context()); err != nil {
		t.Fatal(err)
	}
	daemon.maintainMQTTWithPublisher(t.Context(), func(_ context.Context, _ *paho.Publish) error {
		return nil
	})
	if daemon.TimedataReplay().HasPendingReplay() {
		t.Fatal("expected no pending gap replay while online without spool shedding")
	}

	// Disconnect: Cycle 2 spools telemetry (seq 5..8), then a large protected alarm sheds those telemetry items from GradedSpool.
	daemon.mqttConnected.Store(false)
	curTime = curTime.Add(5 * time.Second)
	if _, err := daemon.StepCycle(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(daemon.Spool().Pending()) != 4 {
		t.Fatalf("expected 4 telemetry spool items before shed, got %d", len(daemon.Spool().Pending()))
	}

	alarmPayload := append([]byte(`{"alarm":"`), append(bytes.Repeat([]byte("a"), 64*1024-100), []byte(`"}`)...)...)
	daemon.publishOrSpool(t.Context(), "hvac/v1/"+gwID+"/up/event", 1, EvidenceAlarm, alarmPayload)
	if !daemon.TimedataReplay().HasPendingReplay() {
		t.Fatal("expected spool shedding to trigger TimedataReplayManager gap replay")
	}
	for _, item := range daemon.Spool().Pending() {
		if strings.HasSuffix(item.Topic, "/up/telemetry") {
			t.Fatalf("expected all telemetry items shed from spool, still found %+v", item)
		}
	}

	// Verify failure during maintainMQTTWithPublisher does not advance replay cursor.
	daemon.mqttConnected.Store(true)
	daemon.maintainMQTTWithPublisher(t.Context(), func(_ context.Context, msg *paho.Publish) error {
		if strings.HasSuffix(msg.Topic, "/up/telemetry") {
			return errors.New("broker drop during timedata replay")
		}
		return nil
	})
	if !daemon.TimedataReplay().HasPendingReplay() {
		t.Fatal("expected pending gap replay to remain when telemetry replay publish fails")
	}
	if snap := daemon.TimedataReplay().CursorSnapshot(); snap.AckedSequence != 4 {
		t.Fatalf("expected AckedSequence=4 preserved on publish failure, got %d", snap.AckedSequence)
	}

	// Reconnect cleanly and collect replayed telemetry envelopes.
	var replayedCodes []string
	codecs := adapter.DefaultCodecRegistry()
	for range 5 {
		daemon.maintainMQTTWithPublisher(t.Context(), func(_ context.Context, msg *paho.Publish) error {
			if !strings.HasSuffix(msg.Topic, "/up/telemetry") {
				return nil
			}
			decoded, decErr := codecs.DecodeTelemetry(msg.Payload, gwID)
			if decErr != nil {
				t.Fatalf("DecodeTelemetry failed: %v", decErr)
			}
			if decoded.Envelope.SchemaVersion != "2.0" || !decoded.Envelope.Replay {
				t.Fatalf("unexpected replay envelope metadata: %+v", decoded.Envelope)
			}
			for _, dev := range decoded.Envelope.Payload.Devices {
				for _, pt := range dev.Points {
					replayedCodes = append(replayedCodes, pt.Code)
				}
			}
			return nil
		})
		if !daemon.TimedataReplay().HasPendingReplay() {
			break
		}
	}
	if daemon.TimedataReplay().HasPendingReplay() {
		t.Fatal("expected pending gap replay to finish on clean reconnect")
	}
	if snap := daemon.TimedataReplay().CursorSnapshot(); snap.AckedSequence != 8 {
		t.Fatalf("final AckedSequence = %d, want 8", snap.AckedSequence)
	}
	wantCodes := []string{"vh_temp", "high_temp", "med_temp", "low_temp"}
	if !slices.Equal(replayedCodes, wantCodes) {
		t.Fatalf("replayed telemetry codes = %v, want %v", replayedCodes, wantCodes)
	}
}
