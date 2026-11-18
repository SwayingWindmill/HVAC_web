package simulator

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"regexp"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eclipse/paho.golang/paho"
	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
	"github.com/quanlaihe/hvac-web/libs/observability"
)

var simulatorUUIDv7Regex = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestLifecycleEventEnvelopesMatchGatewayUplinkContract(t *testing.T) {
	const gatewayID = "018f3e00-4000-7000-8000-000000000100"
	now := time.Date(2026, 11, 16, 12, 34, 56, 789000000, time.UTC)

	if got := mqttEventTopic(gatewayID); got != "hvac/v1/"+gatewayID+"/up/event" {
		t.Fatalf("mqttEventTopic = %q", got)
	}

	cases := []struct {
		eventType string
		severity  string
		data      json.RawMessage
	}{
		{"LWT", "WARNING", json.RawMessage(`{"reason":"UNEXPECTED_DISCONNECT"}`)},
		{"GATEWAY_CONNECTED", "INFO", json.RawMessage(`{}`)},
		{"GATEWAY_DISCONNECTED", "INFO", json.RawMessage(`{"clean":true}`)},
	}
	for idx, tc := range cases {
		raw, err := buildLifecycleEventPayload(gatewayID, tc.eventType, uint64(idx+1), now, tc.data)
		if err != nil {
			t.Fatalf("%s buildLifecycleEventPayload: %v", tc.eventType, err)
		}
		var env mqttEventEnvelope
		if err := json.Unmarshal(raw, &env); err != nil {
			t.Fatalf("%s decode envelope: %v", tc.eventType, err)
		}
		if env.SchemaVersion != "2.0" || env.GatewayID != gatewayID || env.Sequence != uint64(idx+1) {
			t.Fatalf("%s invalid header: %+v", tc.eventType, env)
		}
		if !simulatorUUIDv7Regex.MatchString(env.MessageID) {
			t.Fatalf("%s messageId %q is not UUIDv7", tc.eventType, env.MessageID)
		}
		if env.Payload.EventType != tc.eventType || env.Payload.SourceType != "GATEWAY" || env.Payload.SourceID != gatewayID {
			t.Fatalf("%s invalid payload identity: %+v", tc.eventType, env.Payload)
		}
		if env.Payload.Severity != tc.severity {
			t.Fatalf("%s severity = %q, want %q", tc.eventType, env.Payload.Severity, tc.severity)
		}
		if env.Payload.EventTime != now.UnixMilli() {
			t.Fatalf("%s eventTime = %d, want %d", tc.eventType, env.Payload.EventTime, now.UnixMilli())
		}
	}
}

func TestLifecycleSessionPairsWillAndConnectedAndRegeneratesOnReconnect(t *testing.T) {
	const gatewayID = "018f3e00-4000-7000-8000-000000000100"
	session := &mqttSessionState{}
	seq := &atomic.Uint64{}
	t1 := time.Date(2026, 11, 16, 13, 0, 0, 0, time.UTC)
	t2 := t1.Add(30 * time.Second)

	s1, ts1, err := session.next(t1)
	if err != nil {
		t.Fatalf("session 1 next: %v", err)
	}
	will1Raw, err := buildSessionWillPayload(gatewayID, s1, seq.Add(1), ts1)
	if err != nil {
		t.Fatalf("will1: %v", err)
	}
	curID, curTS := session.current()
	conn1Raw, err := buildSessionConnectedPayload(gatewayID, curID, seq.Add(1), curTS)
	if err != nil {
		t.Fatalf("conn1: %v", err)
	}

	var will1, conn1 mqttEventEnvelope
	_ = json.Unmarshal(will1Raw, &will1)
	_ = json.Unmarshal(conn1Raw, &conn1)
	var will1Data, conn1Data struct {
		SessionID string `json:"sessionId"`
		Reason    string `json:"reason"`
	}
	_ = json.Unmarshal(will1.Payload.Data, &will1Data)
	_ = json.Unmarshal(conn1.Payload.Data, &conn1Data)
	if !simulatorUUIDv7Regex.MatchString(will1Data.SessionID) || will1Data.SessionID != conn1Data.SessionID {
		t.Fatalf("session 1 mismatch between LWT (%+v) and GATEWAY_CONNECTED (%+v)", will1Data, conn1Data)
	}
	if will1Data.Reason != "broker_connection_lost" {
		t.Fatalf("LWT reason = %q, want broker_connection_lost", will1Data.Reason)
	}

	s2, ts2, err := session.next(t2)
	if err != nil {
		t.Fatalf("session 2 next: %v", err)
	}
	if s2 == s1 || ts2.Equal(ts1) {
		t.Fatalf("reconnect must generate fresh sessionId/sessionTime: s1=%s s2=%s", s1, s2)
	}
}

func TestChangeOnUpdateAndPeriodicFullSnapshot(t *testing.T) {
	t.Parallel()

	shadow := newEdgeConfigShadow("")
	if _, err := shadow.applyDesired(context.Background(), GatewayDesiredConfig{
		DesiredRevision: 1,
		SamplingPolicies: []GatewaySamplingPolicy{
			{
				DeviceKey:         "CHILLER-01",
				PointCode:         "chw_temp_c",
				SampleIntervalMS:  1000,
				PublishIntervalMS: 1000,
				Deadband:          0.2,
			},
		},
	}); err != nil {
		t.Fatalf("applyDesired() error = %v", err)
	}

	publisher := &MQTTPublisher{
		gatewayID: "0193f000-2000-7000-8000-000000000002",
		pointByKey: map[string]PointConfig{
			pointReference("CHILLER-01", "chw_temp"): {
				DeviceID:     "CHILLER-01",
				TelemetryKey: "chw_temp",
				PointCode:    "chw_temp_c",
				Unit:         "degC",
			},
			pointReference("CHILLER-01", "run_state"): {
				DeviceID:     "CHILLER-01",
				TelemetryKey: "run_state",
				PointCode:    "run_state",
			},
		},
		configShadow:     shadow,
		snapshotInterval: 300 * time.Second,
		lastPointStates:  map[string]lastEmittedPointState{},
	}

	t0 := time.Date(2026, 11, 17, 18, 0, 0, 0, time.UTC)

	// Initial sample at t0: both points emit + strict v2 JSON fields check.
	env0, err := publisher.buildEnvelope([]Measurement{
		{DeviceID: "CHILLER-01", TelemetryKey: "chw_temp", Value: 7.0, Quality: "GOOD", ObservedAt: t0, Sequence: 1},
		{DeviceID: "CHILLER-01", TelemetryKey: "run_state", Value: "RUNNING", Quality: "GOOD", ObservedAt: t0, Sequence: 2},
	})
	if err != nil {
		t.Fatalf("t0 buildEnvelope() error = %v", err)
	}
	if len(env0.Payload.Devices) != 1 || len(env0.Payload.Devices[0].Points) != 2 {
		t.Fatalf("expected 2 points on initial snapshot, got %+v", env0.Payload.Devices)
	}
	raw, err := json.Marshal(env0)
	if err != nil {
		t.Fatalf("marshal env0 error = %v", err)
	}
	var rawMap map[string]any
	if err := json.Unmarshal(raw, &rawMap); err != nil {
		t.Fatalf("unmarshal rawMap error = %v", err)
	}
	for _, forbidden := range []string{"sessionId", "eventTime"} {
		if _, found := rawMap[forbidden]; found {
			t.Fatalf("v2.0 telemetry envelope must NOT include %q outside schema: %s", forbidden, string(raw))
		}
	}

	// Within deadband & unchanged quality/state at t0+5s: MUST be suppressed.
	envSuppressed, err := publisher.buildEnvelope([]Measurement{
		{DeviceID: "CHILLER-01", TelemetryKey: "chw_temp", Value: 7.15, Quality: "GOOD", ObservedAt: t0.Add(5 * time.Second), Sequence: 3},
		{DeviceID: "CHILLER-01", TelemetryKey: "run_state", Value: "RUNNING", Quality: "GOOD", ObservedAt: t0.Add(5 * time.Second), Sequence: 4},
	})
	if err != nil {
		t.Fatalf("t0+5s buildEnvelope() error = %v", err)
	}
	if len(envSuppressed.Payload.Devices) != 0 {
		t.Fatalf("expected unchanged/within-deadband points suppressed, got %+v", envSuppressed.Payload.Devices)
	}

	// Quality change with unchanged value emits immediately ONLY the quality-changed point.
	envQuality, err := publisher.buildEnvelope([]Measurement{
		{DeviceID: "CHILLER-01", TelemetryKey: "chw_temp", Value: 7.15, Quality: "GOOD", ObservedAt: t0.Add(10 * time.Second), Sequence: 5},
		{DeviceID: "CHILLER-01", TelemetryKey: "run_state", Value: "RUNNING", Quality: "BAD", ObservedAt: t0.Add(10 * time.Second), Sequence: 6},
	})
	if err != nil {
		t.Fatalf("t0+10s buildEnvelope() error = %v", err)
	}
	if len(envQuality.Payload.Devices) != 1 || len(envQuality.Payload.Devices[0].Points) != 1 ||
		envQuality.Payload.Devices[0].Points[0].Code != "run_state" || envQuality.Payload.Devices[0].Points[0].Quality != 1 {
		t.Fatalf("expected immediate quality-change emission for run_state only, got %+v", envQuality.Payload.Devices)
	}

	// Value change beyond deadband emits immediately ONLY chw_temp.
	envDelta, err := publisher.buildEnvelope([]Measurement{
		{DeviceID: "CHILLER-01", TelemetryKey: "chw_temp", Value: 7.35, Quality: "GOOD", ObservedAt: t0.Add(15 * time.Second), Sequence: 7},
		{DeviceID: "CHILLER-01", TelemetryKey: "run_state", Value: "RUNNING", Quality: "BAD", ObservedAt: t0.Add(15 * time.Second), Sequence: 8},
	})
	if err != nil {
		t.Fatalf("t0+15s buildEnvelope() error = %v", err)
	}
	if len(envDelta.Payload.Devices) != 1 || len(envDelta.Payload.Devices[0].Points) != 1 || envDelta.Payload.Devices[0].Points[0].Code != "chw_temp_c" {
		t.Fatalf("expected immediate beyond-deadband emission for chw_temp_c only, got %+v", envDelta.Payload.Devices)
	}

	// At t0+300s (unchanged since t0+15s): forced full all-point snapshot emits BOTH points.
	envFull, err := publisher.buildEnvelope([]Measurement{
		{DeviceID: "CHILLER-01", TelemetryKey: "chw_temp", Value: 7.35, Quality: "GOOD", ObservedAt: t0.Add(300 * time.Second), Sequence: 9},
		{DeviceID: "CHILLER-01", TelemetryKey: "run_state", Value: "RUNNING", Quality: "BAD", ObservedAt: t0.Add(300 * time.Second), Sequence: 10},
	})
	if err != nil {
		t.Fatalf("t0+300s buildEnvelope() error = %v", err)
	}
	if len(envFull.Payload.Devices) != 1 || len(envFull.Payload.Devices[0].Points) != 2 {
		t.Fatalf("expected forced periodic full snapshot with all 2 points at 300s, got %+v", envFull.Payload.Devices)
	}

	// Hot-update snapshotInterval to 45s via SetSnapshotInterval: triggers again at t0+345s.
	publisher.SetSnapshotInterval(45 * time.Second)
	envCustomFull, err := publisher.buildEnvelope([]Measurement{
		{DeviceID: "CHILLER-01", TelemetryKey: "chw_temp", Value: 7.35, Quality: "GOOD", ObservedAt: t0.Add(345 * time.Second), Sequence: 11},
		{DeviceID: "CHILLER-01", TelemetryKey: "run_state", Value: "RUNNING", Quality: "BAD", ObservedAt: t0.Add(345 * time.Second), Sequence: 12},
	})
	if err != nil {
		t.Fatalf("t0+345s buildEnvelope() error = %v", err)
	}
	if len(envCustomFull.Payload.Devices) != 1 || len(envCustomFull.Payload.Devices[0].Points) != 2 {
		t.Fatalf("expected hot-configured 45s full snapshot at +345s, got %+v", envCustomFull.Payload.Devices)
	}
}

type fakeQoSPublisher struct {
	mu       sync.Mutex
	failNext error
	sent     []mqttTelemetryEnvelope
}

func (f *fakeQoSPublisher) Publish(_ context.Context, pub *paho.Publish) (*paho.PublishResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failNext != nil {
		err := f.failNext
		f.failNext = nil
		return nil, err
	}
	var env mqttTelemetryEnvelope
	if err := json.Unmarshal(pub.Payload, &env); err == nil {
		f.sent = append(f.sent, env)
	}
	return &paho.PublishResponse{}, nil
}

func TestMQTTPublisherShadowChannelsUnsyncedRetryAndCursorPreservation(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	at := time.Date(2026, 11, 17, 19, 0, 0, 0, time.UTC)
	host, err := edgecontrol.NewHost()
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	if err := host.Start(nil); err != nil {
		t.Fatalf("host.Start() error = %v", err)
	}
	runtime := &EdgeControlRuntime{
		host:                    host,
		bindingsByDevice:        map[string]map[edgecontrol.SemanticChannel]string{},
		commandByKey:            map[string]edgeCommandBinding{},
		addressByPointID:        map[string]string{},
		staticPointAddresses:    map[string]struct{}{},
		dynamicChannelsByDevice: map[string][]edgecontrol.ChannelDescriptor{},
		leaseByAddress:          map[string]bool{},
		pendingByAddress:        map[string]pendingEdgeCommand{},
	}
	timedata, err := edgecontrol.OpenFileTimedata(filepath.Join(dir, "timedata"), edgecontrol.PriorityLow)
	if err != nil {
		t.Fatalf("OpenFileTimedata() error = %v", err)
	}
	if err := runtime.AttachTimedata(timedata); err != nil {
		t.Fatalf("AttachTimedata() error = %v", err)
	}

	transport := &shadowStubTransport{
		holding: map[uint16]uint16{
			100: 65,  // 6.5 degC
			101: 200, // 20.0 degC
		},
	}
	runtime.SetDriverTransportFactory(func(edgecontrol.DeclarativeModbusProfile) (edgecontrol.ModbusRegisterTransport, error) {
		return transport, nil
	})

	spool, err := newMQTTEvidenceSpool(filepath.Join(dir, "outbound"), 1<<20)
	if err != nil {
		t.Fatalf("newMQTTEvidenceSpool() error = %v", err)
	}
	shadow := newEdgeConfigShadow("")
	if _, err := shadow.applyDesired(context.Background(), GatewayDesiredConfig{
		DesiredRevision: 1,
		SamplingPolicies: []GatewaySamplingPolicy{
			{
				DeviceKey:         "PLC-AUX-01",
				PointCode:         "aux_low_pt",
				SampleIntervalMS:  1000,
				PublishIntervalMS: 1000,
				Deadband:          5.0,
			},
		},
	}); err != nil {
		t.Fatalf("applyDesired() error = %v", err)
	}

	qosPub := &fakeQoSPublisher{}
	var replayMu sync.Mutex
	var replayErr error
	var replayed []mqttTelemetryEnvelope
	connected := &atomic.Bool{}
	publisher := &MQTTPublisher{
		gatewayID:               "0193f000-2000-7000-8000-000000000099",
		config:                  MQTTGatewayConfig{GatewayID: "0193f000-2000-7000-8000-000000000099", MaximumQueueBytes: 1 << 20},
		pointByKey:              map[string]PointConfig{},
		shadowPointKeysByDevice: map[string]map[string]struct{}{},
		edgeRuntime:             runtime,
		qosPub:                  qosPub,
		evidenceSpool:           spool,
		pendingObsByMsgID:       map[string][]PublishedObservation{},
		connected:               connected,
		configShadow:            shadow,
		snapshotInterval:        300 * time.Second,
		lastPointStates:         map[string]lastEmittedPointState{},
		metrics:                 observability.NewRegistry(),
	}
	resendQueue, err := NewTimedataResendQueue(TimedataResendConfig{
		GatewayID:  publisher.gatewayID,
		Timedata:   timedata,
		CursorPath: filepath.Join(dir, "resend-cursor.json"),
		Metrics:    publisher.metrics,
		PointMetadata: func(address string) TimedataResendPointMeta {
			return publisher.timedataPointMetadata(address)
		},
		Publish: func(_ context.Context, _ string, payload []byte) error {
			replayMu.Lock()
			defer replayMu.Unlock()
			if replayErr != nil {
				return replayErr
			}
			var env mqttTelemetryEnvelope
			if err := json.Unmarshal(payload, &env); err == nil {
				replayed = append(replayed, env)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("NewTimedataResendQueue() error = %v", err)
	}
	publisher.resendQueue = resendQueue

	cfg := GatewayModbusDriverConfig{
		DeviceKey:  "PLC-AUX-01",
		DriverType: "MODBUS_TCP",
		Endpoint:   "127.0.0.1:5502",
		UnitID:     3,
		TimeoutMS:  500,
		Retries:    1,
		Registers: []GatewayModbusRegisterMapping{
			{PointCode: "aux_supply_temp_c", FunctionCode: 3, Address: 100, RegisterCount: 1, RawType: "INT16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 0.1, PollPriority: "HIGH"},
			{PointCode: "aux_low_pt", FunctionCode: 3, Address: 101, RegisterCount: 1, RawType: "INT16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 0.1, PollPriority: "HIGH"},
		},
	}
	profile, err := cfg.ToDeclarativeProfile()
	if err != nil {
		t.Fatalf("ToDeclarativeProfile() error = %v", err)
	}
	if err := runtime.ApplyDriverProfile(profile); err != nil {
		t.Fatalf("ApplyDriverProfile() error = %v", err)
	}
	publisher.registerShadowDriverPoints(cfg)

	// Record offline cycle before connect so startup Drain fails transiently and must retry.
	tOffline := at.Add(-10 * time.Second)
	if res := runtime.RunCycle(context.Background(), tOffline); res.TimedataError != nil {
		t.Fatalf("offline RunCycle error = %v", res.TimedataError)
	}

	replayMu.Lock()
	replayErr = errors.New("transient broker timeout during initial drain")
	replayMu.Unlock()
	connected.Store(true)
	publisher.flushAndSyncResend(context.Background())
	if resendQueue.IsSynced() {
		t.Fatal("expected IsSynced() == false after transient Drain failure")
	}

	replayMu.Lock()
	replayErr = nil
	replayMu.Unlock()
	transport.mu.Lock()
	transport.holding[100] = 75
	transport.mu.Unlock()
	if res := runtime.RunCycle(context.Background(), at); res.TimedataError != nil {
		t.Fatalf("live RunCycle error = %v", res.TimedataError)
	}
	if err := publisher.PublishMeasurements(context.Background(), nil); err != nil {
		t.Fatalf("PublishMeasurements() error = %v", err)
	}
	if !resendQueue.IsSynced() {
		t.Fatal("expected IsSynced() == true after PublishMeasurements retried Drain")
	}

	qosPub.mu.Lock()
	if len(qosPub.sent) != 1 || len(qosPub.sent[0].Payload.Devices) != 1 || qosPub.sent[0].Payload.Devices[0].DeviceID != "PLC-AUX-01" {
		t.Fatalf("expected live MQTT envelope emitting PLC-AUX-01 shadow channels, got %+v", qosPub.sent)
	}
	qosPub.sent = nil
	qosPub.mu.Unlock()
	replayMu.Lock()
	replayed = nil
	replayMu.Unlock()

	t1 := at.Add(5 * time.Second)
	transport.mu.Lock()
	transport.holding[100] = 85
	transport.holding[101] = 205
	transport.mu.Unlock()
	highBeforeT1 := resendQueue.CursorSnapshot().ByPriority[edgecontrol.PriorityHigh]
	if res := runtime.RunCycle(context.Background(), t1); res.TimedataError != nil {
		t.Fatalf("t1 RunCycle error = %v", res.TimedataError)
	}
	if err := publisher.PublishMeasurements(context.Background(), nil); err != nil {
		t.Fatalf("t1 PublishMeasurements() error = %v", err)
	}
	highAfterT1 := resendQueue.CursorSnapshot().ByPriority[edgecontrol.PriorityHigh]
	t1LowSeq, ok := timedata.LookupSequence("PLC-AUX-01/aux_low_pt", t1, 0)
	if !ok || highAfterT1 >= t1LowSeq || highAfterT1 != highBeforeT1 {
		t.Fatalf("expected HIGH cursor frozen (%d) before suppressed aux_low_pt seq %d, got %d (ok=%v)", highBeforeT1, t1LowSeq, highAfterT1, ok)
	}
	t1HighSeq, ok := timedata.LookupSequence("PLC-AUX-01/aux_supply_temp_c", t1, 0)
	if !ok {
		t.Fatalf("missing t1 aux_supply_temp_c seq (ok=%v)", ok)
	}
	acked := resendQueue.CursorSnapshot().AckedByPriority[edgecontrol.PriorityHigh]
	ackedT1High := false
	for _, s := range acked {
		if s == t1HighSeq {
			ackedT1High = true
			break
		}
	}
	if !ackedT1High {
		t.Fatalf("expected published t1 aux_supply_temp_c seq %d recorded in AckedByPriority, got %v", t1HighSeq, acked)
	}

	qosPub.mu.Lock()
	qosPub.sent = nil
	qosPub.mu.Unlock()
	connected.Store(false)
	resendQueue.SetOnlineSynced(false)

	t2 := at.Add(10 * time.Second)
	transport.mu.Lock()
	transport.holding[100] = 95
	transport.mu.Unlock()
	if res := runtime.RunCycle(context.Background(), t2); res.TimedataError != nil {
		t.Fatalf("t2 RunCycle error = %v", res.TimedataError)
	}
	cursorBeforeDisconnectPub := resendQueue.CursorSnapshot().ByPriority
	if err := publisher.PublishMeasurements(context.Background(), nil); err != nil {
		t.Fatalf("t2 PublishMeasurements() offline error = %v", err)
	}
	if resendQueue.CursorSnapshot().ByPriority[edgecontrol.PriorityHigh] != cursorBeforeDisconnectPub[edgecontrol.PriorityHigh] {
		t.Fatalf("expected cursors frozen while disconnected, before=%+v after=%+v", cursorBeforeDisconnectPub, resendQueue.CursorSnapshot().ByPriority)
	}

	connected.Store(true)
	publisher.flushAndSyncResend(context.Background())
	if !resendQueue.IsSynced() {
		t.Fatal("expected IsSynced() == true after reconnect flush+drain")
	}

	qosPub.mu.Lock()
	liveSent := append([]mqttTelemetryEnvelope(nil), qosPub.sent...)
	qosPub.mu.Unlock()
	replayMu.Lock()
	replaySent := append([]mqttTelemetryEnvelope(nil), replayed...)
	replayMu.Unlock()

	liveT2Count := 0
	for _, env := range liveSent {
		for _, dev := range env.Payload.Devices {
			if dev.DeviceID == "PLC-AUX-01" {
				for _, pt := range dev.Points {
					if pt.Code == "aux_supply_temp_c" && pt.Value == 9.5 && !env.Replay {
						liveT2Count++
					}
				}
			}
		}
	}
	if liveT2Count != 1 {
		t.Fatalf("expected t2 aux_supply_temp_c=9.5 flushed live once, got count=%d (%+v)", liveT2Count, liveSent)
	}

	replayedSuppressedT1LowCount := 0
	replayedDuplicateSupplyCount := 0
	for _, env := range replaySent {
		for _, dev := range env.Payload.Devices {
			if dev.DeviceID == "PLC-AUX-01" {
				for _, pt := range dev.Points {
					if pt.Code == "aux_low_pt" && pt.Value == 20.5 && env.Replay && env.Timestamp == t1.UnixMilli() {
						replayedSuppressedT1LowCount++
					}
					if pt.Code == "aux_supply_temp_c" && (pt.Value == 8.5 || pt.Value == 9.5) {
						replayedDuplicateSupplyCount++
					}
				}
			}
		}
	}
	if replayedSuppressedT1LowCount != 1 {
		t.Fatalf("expected suppressed t1 aux_low_pt=20.5 replayed once on reconnect, got count=%d", replayedSuppressedT1LowCount)
	}
	if replayedDuplicateSupplyCount != 0 {
		t.Fatalf("expected already-flushed t1/t2 aux_supply_temp_c skipped by Drain without duplicate replay, got %d", replayedDuplicateSupplyCount)
	}
}

func TestMQTTPublisherAnalogDeadbandAndMaxSilenceFilter(t *testing.T) {
	t.Parallel()

	publisher := &MQTTPublisher{
		gatewayID: "0193f000-2000-7000-8000-000000000003",
		pointByKey: map[string]PointConfig{
			pointReference("CHILLER-01", "chw_temp"): {
				DeviceID:     "CHILLER-01",
				TelemetryKey: "chw_temp",
				PointCode:    "chw_temp_c",
				PointType:    "TELEMETRY",
				ValueType:    "NUMBER",
				Unit:         "degC",
			},
			pointReference("METER-01", "total_kwh"): {
				DeviceID:     "METER-01",
				TelemetryKey: "total_kwh",
				PointCode:    "total_kwh",
				PointType:    "COUNTER",
				ValueType:    "NUMBER",
				Unit:         "kWh",
			},
		},
		snapshotInterval:   300 * time.Second,
		lastPointStates:    map[string]lastEmittedPointState{},
		Deadband:           0.5,
		DeadbandMaxSilence: 60 * time.Second,
	}

	t0 := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

	env0, err := publisher.buildEnvelope([]Measurement{
		{DeviceID: "CHILLER-01", TelemetryKey: "chw_temp", Value: 7.0, Quality: "GOOD", ObservedAt: t0, Sequence: 1},
		{DeviceID: "METER-01", TelemetryKey: "total_kwh", Value: 100.0, Quality: "GOOD", ObservedAt: t0, Sequence: 2},
	})
	if err != nil {
		t.Fatalf("t0 buildEnvelope error = %v", err)
	}
	if len(env0.Payload.Devices) != 2 {
		t.Fatalf("expected 2 devices at t0, got %+v", env0.Payload.Devices)
	}

	env10, err := publisher.buildEnvelope([]Measurement{
		{DeviceID: "CHILLER-01", TelemetryKey: "chw_temp", Value: 7.2, Quality: "GOOD", ObservedAt: t0.Add(10 * time.Second), Sequence: 3},
		{DeviceID: "METER-01", TelemetryKey: "total_kwh", Value: 100.0, Quality: "GOOD", ObservedAt: t0.Add(10 * time.Second), Sequence: 4},
	})
	if err != nil {
		t.Fatalf("t0+10s buildEnvelope error = %v", err)
	}
	if len(env10.Payload.Devices) != 1 || env10.Payload.Devices[0].DeviceID != "METER-01" {
		t.Fatalf("expected only COUNTER device at t0+10s, got %+v", env10.Payload.Devices)
	}

	env60, err := publisher.buildEnvelope([]Measurement{
		{DeviceID: "CHILLER-01", TelemetryKey: "chw_temp", Value: 7.0, Quality: "GOOD", ObservedAt: t0.Add(60 * time.Second), Sequence: 5},
	})
	if err != nil {
		t.Fatalf("t0+60s buildEnvelope error = %v", err)
	}
	if len(env60.Payload.Devices) != 1 || env60.Payload.Devices[0].Points[0].Code != "chw_temp_c" {
		t.Fatalf("expected MaxSilence heartbeat for chw_temp_c at t0+60s, got %+v", env60.Payload.Devices)
	}

	env70, err := publisher.buildEnvelope([]Measurement{
		{DeviceID: "CHILLER-01", TelemetryKey: "chw_temp", Value: 7.0, Quality: "BAD", ObservedAt: t0.Add(70 * time.Second), Sequence: 6},
	})
	if err != nil {
		t.Fatalf("t0+70s buildEnvelope error = %v", err)
	}
	if len(env70.Payload.Devices) != 1 || env70.Payload.Devices[0].Points[0].Quality != 1 {
		t.Fatalf("expected non-GOOD quality emission at t0+70s, got %+v", env70.Payload.Devices)
	}

	publisher.SetDeadband(0, 0)
	env80, err := publisher.buildEnvelope([]Measurement{
		{DeviceID: "CHILLER-01", TelemetryKey: "chw_temp", Value: 7.1, Quality: "GOOD", ObservedAt: t0.Add(80 * time.Second), Sequence: 7},
	})
	if err != nil {
		t.Fatalf("t0+80s buildEnvelope error = %v", err)
	}
	if len(env80.Payload.Devices) != 1 {
		t.Fatalf("expected emission after disabling deadband filter, got %+v", env80.Payload.Devices)
	}
}
