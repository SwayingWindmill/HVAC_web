package edgedaemon

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/adapter"
	modbus "github.com/simonvetter/modbus"
)

type fakeModbusServerHandler struct {
	mu   sync.Mutex
	regs map[uint16]uint16
}

func (h *fakeModbusServerHandler) HandleCoils(*modbus.CoilsRequest) ([]bool, error) {
	return nil, modbus.ErrIllegalFunction
}

func (h *fakeModbusServerHandler) HandleDiscreteInputs(*modbus.DiscreteInputsRequest) ([]bool, error) {
	return nil, modbus.ErrIllegalFunction
}

func (h *fakeModbusServerHandler) HandleHoldingRegisters(req *modbus.HoldingRegistersRequest) ([]uint16, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if req.IsWrite {
		for i, v := range req.Args {
			h.regs[req.Addr+uint16(i)] = v
		}
		return nil, nil
	}
	out := make([]uint16, req.Quantity)
	for i := range req.Quantity {
		out[i] = h.regs[req.Addr+uint16(i)]
	}
	return out, nil
}

func (h *fakeModbusServerHandler) HandleInputRegisters(*modbus.InputRegistersRequest) ([]uint16, error) {
	return nil, modbus.ErrIllegalFunction
}

func startLocalModbusTCP(t *testing.T, initial map[uint16]uint16) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	h := &fakeModbusServerHandler{regs: initial}
	srv, err := modbus.NewServer(&modbus.ServerConfiguration{URL: "tcp://" + addr, Timeout: time.Second}, h)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Stop() })
	return addr
}

func TestDaemonCycleHealthEndpointSpoolAndShadowUpdate(t *testing.T) {
	endpoint := startLocalModbusTCP(t, map[uint16]uint16{100: 75})
	uid := 1
	timeoutMS := 500
	retries := 0

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	freeAddr := ln.Addr().String()
	_ = ln.Close()

	cfg := Config{
		GatewayID:        "0193f000-2000-7000-8000-000000000200",
		StateDir:         t.TempDir(),
		HealthListenAddr: freeAddr,
		Drivers: []edgecontrol.ModbusDriverInput{{
			DeviceKey:  "chiller-01",
			DriverType: "MODBUS_TCP",
			Endpoint:   &endpoint,
			UnitID:     &uid,
			TimeoutMS:  &timeoutMS,
			Retries:    &retries,
			Registers: []edgecontrol.ModbusRegisterInput{{
				PointCode:     "chw_supply_temp",
				FunctionCode:  3,
				Address:       100,
				RegisterCount: 1,
				RawType:       "UINT16",
				ByteOrder:     "BIG_ENDIAN",
				WordOrder:     "BIG_ENDIAN",
				Scale:         0.1,
				Offset:        0,
				PollPriority:  "HIGH",
			}},
		}},
	}
	cfg.ApplyDefaults("1.0.0")

	daemon, err := NewDaemon(cfg, DaemonOptions{})
	if err != nil {
		t.Fatalf("NewDaemon: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- daemon.Run(ctx)
	}()

	// Poll healthz deterministically via HTTP
	resp, err := http.Get("http://" + daemon.HealthAddr() + "/healthz")
	if err != nil {
		cancel()
		t.Fatalf("GET /healthz: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		cancel()
		t.Fatalf("/healthz status = %d, want 200", resp.StatusCode)
	}

	var readyStatus int
	var readyBody string
	deadline := time.Now().Add(2 * time.Second)
	readyTicker := time.NewTicker(10 * time.Millisecond)
	defer readyTicker.Stop()
	for {
		rResp, rErr := http.Get("http://" + daemon.HealthAddr() + "/readyz")
		if rErr == nil {
			b, _ := io.ReadAll(rResp.Body)
			_ = rResp.Body.Close()
			readyStatus = rResp.StatusCode
			readyBody = string(b)
			if readyStatus == http.StatusOK {
				break
			}
		}
		if !time.Now().Before(deadline) {
			break
		}
		<-readyTicker.C
	}
	if readyStatus != http.StatusOK || !strings.Contains(readyBody, "ok") {
		cancel()
		t.Fatalf("/readyz status=%d body=%q, want 200 ok", readyStatus, readyBody)
	}

	cycleRes, err := daemon.StepCycle(ctx)
	if err != nil {
		cancel()
		t.Fatalf("StepCycle: %v", err)
	}
	if len(cycleRes.PollResults) != 1 || cycleRes.PollResults[0].Error != nil {
		cancel()
		t.Fatalf("unexpected Modbus poll results: %+v", cycleRes.PollResults)
	}
	snap, ok := cycleRes.Cycle.Image.Get("chiller-01/chw_supply_temp")
	if !ok || !snap.HasValue || snap.Sample.Value.Double != 7.5 {
		cancel()
		t.Fatalf("unexpected polled chiller supply temp: %+v ok=%v", snap, ok)
	}
	if len(daemon.Spool().Pending()) == 0 {
		cancel()
		t.Fatal("expected telemetry uplink item buffered in graded spool while offline")
	}

	// Shadow hot-reload updates device driver dynamically
	report, stale, err := daemon.Shadow().ApplyDesired(GatewayDesiredConfig{
		DesiredRevision: 1,
		DriverConfigs: []edgecontrol.ModbusDriverInput{{
			DeviceKey:  "chiller-02",
			DriverType: "MODBUS_TCP",
			Endpoint:   &endpoint,
			UnitID:     &uid,
			TimeoutMS:  &timeoutMS,
			Retries:    &retries,
			Registers:  cfg.Drivers[0].Registers,
		}},
	})
	if err != nil || stale || report.AppliedStatus != shadowStatusApplied {
		cancel()
		t.Fatalf("ApplyDesired failed: report=%+v stale=%v err=%v", report, stale, err)
	}
	if _, ok := daemon.Host().Adapter("chiller-02"); !ok {
		cancel()
		t.Fatal("expected chiller-02 after shadow hot reload")
	}

	cancel()
	if err := <-doneCh; err != nil {
		t.Fatalf("Daemon.Run exit error: %v", err)
	}
}

func TestBuildMQTTTLSConfigOverrideAndFallback(t *testing.T) {
	override := buildMQTTTLSConfig(Config{
		ServerName:     "hvac.example.com",
		CAFile:         "/etc/ssl/certs/ca-certificates.crt",
		MQTTServerName: "mqtt-broker",
		MQTTCAFile:     "/root/hvac-platform/broker-ca.crt",
		CertFile:       "/root/hvac-platform/gateway.crt",
		KeyFile:        "/root/hvac-platform/gateway.key",
	})
	if override == nil {
		t.Fatal("expected non-nil MQTT TLS config")
	}
	if override.CACertFile != "/root/hvac-platform/broker-ca.crt" || override.ServerName != "mqtt-broker" {
		t.Fatalf("unexpected override TLS config: %+v", override)
	}

	fallback := buildMQTTTLSConfig(Config{
		ServerName: "127.0.0.1",
		CAFile:     "/tmp/ca.crt",
		CertFile:   "/tmp/client.crt",
		KeyFile:    "/tmp/client.key",
	})
	if fallback == nil {
		t.Fatal("expected non-nil fallback MQTT TLS config")
	}
	if fallback.CACertFile != "/tmp/ca.crt" || fallback.ServerName != "127.0.0.1" {
		t.Fatalf("unexpected fallback TLS config: %+v", fallback)
	}

	onlyMQTTCA := buildMQTTTLSConfig(Config{
		MQTTServerName: "mqtt-broker",
		MQTTCAFile:     "/tmp/broker-ca.crt",
		CertFile:       "/tmp/client.crt",
		KeyFile:        "/tmp/client.key",
	})
	if onlyMQTTCA == nil || onlyMQTTCA.CACertFile != "/tmp/broker-ca.crt" || onlyMQTTCA.ServerName != "mqtt-broker" {
		t.Fatalf("expected mqttCaFile alone to build TLS config, got %+v", onlyMQTTCA)
	}
}

func TestDaemonRuleEventAlignmentAndWireDecode(t *testing.T) {
	endpoint, modHandler := func() (string, *fakeModbusServerHandler) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addr := ln.Addr().String()
		_ = ln.Close()
		h := &fakeModbusServerHandler{regs: map[uint16]uint16{100: 120}}
		srv, err := modbus.NewServer(&modbus.ServerConfiguration{URL: "tcp://" + addr, Timeout: time.Second}, h)
		if err != nil {
			t.Fatal(err)
		}
		if err := srv.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = srv.Stop() })
		return addr, h
	}()

	uid, timeoutMS, retries := 1, 500, 0
	ruleID := "0193f000-3000-7000-8000-000000000099"
	gwID := "0193f000-2000-7000-8000-000000000201"
	curTime := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)

	bundle := edgecontrol.EdgeRuleBundle{
		SchemaVersion:  1,
		SiteID:         "0193f000-1000-7000-8000-000000000001",
		BundleRevision: 3,
		Rules: []edgecontrol.CompiledEdgeRule{{
			RuleID:           ruleID,
			AlarmType:        "CHW_HIGH_TEMP",
			Title:            "Chilled water supply temp high",
			Severity:         "CRITICAL",
			ExecutionTarget:  edgecontrol.EdgeTargetEdge,
			QualityPolicy:    edgecontrol.EdgeQualityValidOnly,
			FreshnessSeconds: 60,
			TriggerMode:      edgecontrol.EdgeTriggerSimple,
			Raise: edgecontrol.EdgeCondition{
				Kind:     edgecontrol.EdgeConditionCompare,
				Input:    "chiller-01/chw_supply_temp",
				Operator: edgecontrol.EdgeCompareGT,
				Value:    &edgecontrol.EdgeTypedValue{Type: edgecontrol.EdgeValueNumber, Number: 10},
			},
			Clear: edgecontrol.EdgeCondition{
				Kind:     edgecontrol.EdgeConditionCompare,
				Input:    "chiller-01/chw_supply_temp",
				Operator: edgecontrol.EdgeCompareLTE,
				Value:    &edgecontrol.EdgeTypedValue{Type: edgecontrol.EdgeValueNumber, Number: 9},
			},
		}},
	}
	digest, err := bundle.ComputeDigest()
	if err != nil {
		t.Fatalf("bundle.ComputeDigest: %v", err)
	}
	bundle.EdgeBundleDigest = digest

	cfg := Config{
		GatewayID: gwID,
		StateDir:  t.TempDir(),
		Drivers: []edgecontrol.ModbusDriverInput{{
			DeviceKey:  "chiller-01",
			DriverType: "MODBUS_TCP",
			Endpoint:   &endpoint,
			UnitID:     &uid,
			TimeoutMS:  &timeoutMS,
			Retries:    &retries,
			Registers: []edgecontrol.ModbusRegisterInput{{
				PointCode:     "chw_supply_temp",
				FunctionCode:  3,
				Address:       100,
				RegisterCount: 1,
				RawType:       "UINT16",
				ByteOrder:     "BIG_ENDIAN",
				WordOrder:     "BIG_ENDIAN",
				Scale:         0.1,
				Offset:        0,
				PollPriority:  "VERY_HIGH",
			}},
		}},
		EdgeRuleBundle: &bundle,
	}
	cfg.ApplyDefaults("0.1.2")

	daemon, err := NewDaemon(cfg, DaemonOptions{Now: func() time.Time { return curTime }})
	if err != nil {
		t.Fatalf("NewDaemon: %v", err)
	}
	defer func() { _ = daemon.Close() }()

	if _, err := daemon.StepCycle(t.Context()); err != nil {
		t.Fatalf("StepCycle raise: %v", err)
	}
	curTime = curTime.Add(5 * time.Second)
	modHandler.mu.Lock()
	modHandler.regs[100] = 80
	modHandler.mu.Unlock()
	if _, err := daemon.StepCycle(t.Context()); err != nil {
		t.Fatalf("StepCycle clear: %v", err)
	}

	var eventItems []SpoolItem
	for _, item := range daemon.Spool().Pending() {
		if item.Class == EvidenceAlarm {
			eventItems = append(eventItems, item)
		}
	}
	if len(eventItems) != 2 {
		t.Fatalf("expected 2 alarm events (RAISED + CLEARED), got %d", len(eventItems))
	}

	reg := adapter.DefaultCodecRegistry()
	var decodedTypes []string
	for _, item := range eventItems {
		evt, err := reg.DecodeEvent(item.Payload, gwID)
		if err != nil {
			t.Fatalf("DecodeEvent failed: %v", err)
		}
		decodedTypes = append(decodedTypes, evt.Payload.EventType)
		if evt.Payload.SourceType != "EDGE_RULE" || evt.Payload.SourceID != ruleID {
			t.Fatalf("unexpected source fields: sourceType=%s sourceId=%s", evt.Payload.SourceType, evt.Payload.SourceID)
		}
		if strings.HasPrefix(evt.Payload.EventType, "EDGE_RULE_EDGE_RULE_") {
			t.Fatalf("double prefix bug regressed: %s", evt.Payload.EventType)
		}
		var payload edgecontrol.EdgeRuleEvent
		if err := json.Unmarshal(evt.Payload.Data, &payload); err != nil {
			t.Fatalf("decode full EdgeRuleEvent payload: %v", err)
		}
		if payload.EventID == "" || payload.RuleID != ruleID || payload.OccurredAt == "" {
			t.Fatalf("EdgeRuleEvent missing required metadata: %+v", payload)
		}
	}
	if !slices.Contains(decodedTypes, edgecontrol.EdgeRuleEventRaised) || !slices.Contains(decodedTypes, edgecontrol.EdgeRuleEventCleared) {
		t.Fatalf("expected RAISED and CLEARED event types, got %v", decodedTypes)
	}
}

func TestDaemonDeadbandTimedataPriorityAndReplay(t *testing.T) {
	endpoint, modHandler := func() (string, *fakeModbusServerHandler) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addr := ln.Addr().String()
		_ = ln.Close()
		h := &fakeModbusServerHandler{regs: map[uint16]uint16{100: 70, 101: 230}}
		srv, err := modbus.NewServer(&modbus.ServerConfiguration{URL: "tcp://" + addr, Timeout: time.Second}, h)
		if err != nil {
			t.Fatal(err)
		}
		if err := srv.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = srv.Stop() })
		return addr, h
	}()

	uid, timeoutMS, retries := 1, 500, 0
	gwID := "0193f000-2000-7000-8000-000000000202"
	curTime := time.Date(2026, 11, 22, 11, 0, 0, 0, time.UTC)

	cfg := Config{
		GatewayID:                      gwID,
		StateDir:                       t.TempDir(),
		TelemetryDeadband:              0.5,
		TelemetryDeadbandMaxSilenceSec: 60,
		Drivers: []edgecontrol.ModbusDriverInput{{
			DeviceKey:  "chiller-01",
			DriverType: "MODBUS_TCP",
			Endpoint:   &endpoint,
			UnitID:     &uid,
			TimeoutMS:  &timeoutMS,
			Retries:    &retries,
			Registers: []edgecontrol.ModbusRegisterInput{
				{PointCode: "chw_supply_temp", FunctionCode: 3, Address: 100, RegisterCount: 1, RawType: "UINT16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 0.1, PollPriority: "VERY_HIGH"},
				{PointCode: "ambient_temp", FunctionCode: 3, Address: 101, RegisterCount: 1, RawType: "UINT16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 0.1, PollPriority: "LOW"},
			},
		}},
	}
	cfg.ApplyDefaults("0.1.2")

	daemon, err := NewDaemon(cfg, DaemonOptions{Now: func() time.Time { return curTime }})
	if err != nil {
		t.Fatalf("NewDaemon: %v", err)
	}
	defer func() { _ = daemon.Close() }()

	// Cycle 1: initial observations emit in VERY_HIGH + LOW tiers (2 spool items).
	if _, err := daemon.StepCycle(t.Context()); err != nil {
		t.Fatalf("StepCycle 1: %v", err)
	}
	if got := len(daemon.Spool().Pending()); got != 2 {
		t.Fatalf("cycle 1 pending = %d, want 2 (VERY_HIGH + LOW)", got)
	}

	// Cycle 2 (+10s): small change < 0.5 deadband -> suppressed over MQTT spool, yet FileTimedata still records raw cycle.
	curTime = curTime.Add(10 * time.Second)
	modHandler.mu.Lock()
	modHandler.regs[100] = 72
	modHandler.regs[101] = 231
	modHandler.mu.Unlock()
	if _, err := daemon.StepCycle(t.Context()); err != nil {
		t.Fatalf("StepCycle 2: %v", err)
	}
	if got := len(daemon.Spool().Pending()); got != 2 {
		t.Fatalf("cycle 2 within deadband must not enqueue extra MQTT envelopes, got %d", got)
	}
	history, err := daemon.Timedata().History("chiller-01/chw_supply_temp", 10)
	if err != nil || len(history) != 2 {
		t.Fatalf("expected 2 full-resolution FileTimedata records despite deadband, got (%d, %v)", len(history), err)
	}

	// Cycles 3 & 4 (+20s, +30s): PriorityLow waits on cadence=4 and stays within MaxSilence.
	for _, delta := range []time.Duration{10 * time.Second, 10 * time.Second} {
		curTime = curTime.Add(delta)
		if _, err := daemon.StepCycle(t.Context()); err != nil {
			t.Fatalf("intermediate StepCycle: %v", err)
		}
		if got := len(daemon.Spool().Pending()); got != 2 {
			t.Fatalf("intermediate cycle within MaxSilence must keep pending=2, got %d", got)
		}
	}

	// Cycle 5 (+70s > MaxSilence 60s, pollCycle=4): both VERY_HIGH and LOW poll fresh and re-emit.
	curTime = curTime.Add(40 * time.Second)
	if _, err := daemon.StepCycle(t.Context()); err != nil {
		t.Fatalf("StepCycle 5: %v", err)
	}
	pending := daemon.Spool().Pending()
	if len(pending) != 4 {
		t.Fatalf("cycle 5 post-MaxSilence pending = %d, want 4", len(pending))
	}
	historyAfter, err := daemon.Timedata().History("chiller-01/chw_supply_temp", 10)
	if err != nil || len(historyAfter) != 5 {
		t.Fatalf("expected 5 full-resolution FileTimedata records across 5 cycles, got (%d, %v)", len(historyAfter), err)
	}
	if pending[0].Class != EvidenceTelemetryVeryHigh || pending[len(pending)-1].Class != EvidenceTelemetryLow {
		t.Fatalf("expected VERY_HIGH drained ahead of LOW, classes=%v/%v", pending[0].Class, pending[len(pending)-1].Class)
	}

	// Replayed payload flips replay=true, preserves deterministic messageId, and decodes via cloud TelemetryCodec.
	replayed := markTelemetryEnvelopeReplay(pending[0].Topic, pending[0].Payload)
	var liveEnv, replayEnv mqttUplinkTelemetryEnvelope
	if err := json.Unmarshal(pending[0].Payload, &liveEnv); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(replayed, &replayEnv); err != nil {
		t.Fatal(err)
	}
	if liveEnv.Replay || !replayEnv.Replay || liveEnv.MessageID != replayEnv.MessageID {
		t.Fatalf("replay flag or deterministic messageId mismatch: live=(replay=%v id=%s) replay=(replay=%v id=%s)", liveEnv.Replay, liveEnv.MessageID, replayEnv.Replay, replayEnv.MessageID)
	}
	decoded, err := adapter.DefaultCodecRegistry().DecodeTelemetry(replayed, gwID)
	if err != nil || !decoded.Envelope.Replay || decoded.Envelope.MessageID != liveEnv.MessageID {
		t.Fatalf("cloud TelemetryCodec decode on replayed envelope failed: decoded=%+v err=%v", decoded, err)
	}
}

func TestDaemonLifecycleLWTConnectedAndDisconnectedSessionID(t *testing.T) {
	gwID := "0193f000-2000-7000-8000-000000000203"
	ts := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)

	cfg := Config{GatewayID: gwID, StateDir: t.TempDir()}
	cfg.ApplyDefaults("0.1.2")
	daemon, err := NewDaemon(cfg, DaemonOptions{Now: func() time.Time { return ts }})
	if err != nil {
		t.Fatalf("NewDaemon: %v", err)
	}
	defer func() { _ = daemon.Close() }()

	sid, _ := daemon.rotateSession(ts)
	lwtBytes, err := daemon.buildGatewayLifecycleEventPayload("LWT", sid, ts, map[string]any{"reason": "broker_connection_lost"})
	if err != nil {
		t.Fatalf("build LWT: %v", err)
	}
	connBytes, err := daemon.buildGatewayLifecycleEventPayload("GATEWAY_CONNECTED", sid, ts.Add(time.Second), nil)
	if err != nil {
		t.Fatalf("build GATEWAY_CONNECTED: %v", err)
	}
	discBytes, err := daemon.buildGatewayLifecycleEventPayload("GATEWAY_DISCONNECTED", sid, ts.Add(2*time.Second), map[string]any{"clean": true})
	if err != nil {
		t.Fatalf("build GATEWAY_DISCONNECTED: %v", err)
	}

	reg := adapter.DefaultCodecRegistry()
	for _, tc := range []struct {
		wantType string
		raw      []byte
	}{
		{"LWT", lwtBytes},
		{"GATEWAY_CONNECTED", connBytes},
		{"GATEWAY_DISCONNECTED", discBytes},
	} {
		evt, decErr := reg.DecodeEvent(tc.raw, gwID)
		if decErr != nil {
			t.Fatalf("%s DecodeEvent failed: %v", tc.wantType, decErr)
		}
		if evt.Payload.EventType != tc.wantType || evt.Payload.SourceType != "GATEWAY" {
			t.Fatalf("%s unexpected decoded event: %+v", tc.wantType, evt)
		}
		var dataMap map[string]any
		if err := json.Unmarshal(evt.Payload.Data, &dataMap); err != nil {
			t.Fatal(err)
		}
		if dataMap["sessionId"] != sid {
			t.Fatalf("%s sessionId = %v, want %q", tc.wantType, dataMap["sessionId"], sid)
		}
	}

	badDeadband := Config{GatewayID: gwID, StateDir: t.TempDir(), TelemetryDeadband: -0.1}
	badDeadband.ApplyDefaults("0.1.2")
	if err := badDeadband.Validate(); err == nil {
		t.Fatal("expected negative telemetryDeadband validation error")
	}
}

func TestDaemonShadowAggregationClusterAndScheduleLifecycle(t *testing.T) {
	endpoint, modHandler := func() (string, *fakeModbusServerHandler) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addr := ln.Addr().String()
		_ = ln.Close()
		h := &fakeModbusServerHandler{regs: map[uint16]uint16{
			100: 70,  // chw_supply_temp 7.0
			101: 200, // ambient_temp 20.0
			102: 120, // rule_watch_temp 12.0
		}}
		srv, err := modbus.NewServer(&modbus.ServerConfiguration{URL: "tcp://" + addr, Timeout: time.Second}, h)
		if err != nil {
			t.Fatal(err)
		}
		if err := srv.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = srv.Stop() })
		return addr, h
	}()

	gwID := "0193f000-2000-7000-8000-000000000204"
	curTime := time.Date(2026, 11, 22, 11, 0, 0, 0, time.UTC)
	uid, timeoutMS, retries := 1, 500, 0
	drivers := []edgecontrol.ModbusDriverInput{
		{
			DeviceKey:  "chiller-01",
			DriverType: "MODBUS_TCP",
			Endpoint:   &endpoint,
			UnitID:     &uid,
			TimeoutMS:  &timeoutMS,
			Retries:    &retries,
			Registers: []edgecontrol.ModbusRegisterInput{
				{PointCode: "chw_supply_temp", FunctionCode: 3, Address: 100, RegisterCount: 1, RawType: "UINT16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 0.1, PollPriority: "VERY_HIGH"},
				{PointCode: "rule_watch_temp", FunctionCode: 3, Address: 102, RegisterCount: 1, RawType: "UINT16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 0.1, PollPriority: "HIGH"},
			},
		},
		{
			DeviceKey:  "weather-01",
			DriverType: "MODBUS_TCP",
			Endpoint:   &endpoint,
			UnitID:     &uid,
			TimeoutMS:  &timeoutMS,
			Retries:    &retries,
			Registers: []edgecontrol.ModbusRegisterInput{
				{PointCode: "ambient_temp", FunctionCode: 3, Address: 101, RegisterCount: 1, RawType: "UINT16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 0.1, PollPriority: "HIGH"},
			},
		},
	}

	cfg := Config{
		GatewayID: gwID,
		StateDir:  t.TempDir(),
		Drivers:   drivers,
	}
	cfg.ApplyDefaults("0.1.2")

	daemon, err := NewDaemon(cfg, DaemonOptions{Now: func() time.Time { return curTime }})
	if err != nil {
		t.Fatalf("NewDaemon: %v", err)
	}
	defer func() { _ = daemon.Close() }()

	ruleBundle := &edgecontrol.EdgeRuleBundle{
		SchemaVersion:  1,
		SiteID:         "0193f000-2000-7000-8000-000000000299",
		BundleRevision: 1,
		CompiledAt:     curTime.Format(time.RFC3339),
		Rules: []edgecontrol.CompiledEdgeRule{{
			RuleID:           "0193f000-2000-7000-8000-000000000288",
			AlarmType:        "WATCH_TEMP",
			Title:            "Watch temp high",
			Severity:         "CRITICAL",
			ExecutionTarget:  edgecontrol.EdgeTargetEdge,
			QualityPolicy:    edgecontrol.EdgeQualityValidOnly,
			FreshnessSeconds: 60,
			TriggerMode:      edgecontrol.EdgeTriggerSimple,
			Raise: edgecontrol.EdgeCondition{
				Kind:     edgecontrol.EdgeConditionCompare,
				Input:    "chiller-01/rule_watch_temp",
				Operator: edgecontrol.EdgeCompareGT,
				Value:    &edgecontrol.EdgeTypedValue{Type: edgecontrol.EdgeValueNumber, Number: 25},
			},
			Clear: edgecontrol.EdgeCondition{
				Kind:     edgecontrol.EdgeConditionCompare,
				Input:    "chiller-01/rule_watch_temp",
				Operator: edgecontrol.EdgeCompareLTE,
				Value:    &edgecontrol.EdgeTypedValue{Type: edgecontrol.EdgeValueNumber, Number: 20},
			},
		}},
	}
	digest, err := ruleBundle.ComputeDigest()
	if err != nil {
		t.Fatal(err)
	}
	ruleBundle.EdgeBundleDigest = digest

	autonomy := edgecontrol.DefaultAutonomyPolicyConfig()
	autonomy.Enabled = true
	autonomy.OfflineGraceSeconds = 60

	aggPolicy := &edgecontrol.EdgeAggregationPolicy{
		Enabled: true,
		Points:  []edgecontrol.AggregationPointPolicy{{DeviceKey: "*", PointCode: "*", WindowSeconds: 60, EmitMinMax: true}},
	}
	clusterPolicy := edgecontrol.DefaultClusterControlPolicy()
	clusterPolicy.Enabled = true

	preCoolSP := 6.0
	dayStart := time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC)
	sched := &edgecontrol.EdgeEnergySchedule{
		ScheduleID:                "sched-live-1",
		Version:                   2,
		IssuedAt:                  dayStart,
		ValidFrom:                 dayStart,
		ValidUntil:                dayStart.Add(7 * time.Hour),
		LeaseExpiresAt:            time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC),
		ContinueOfflineWhileValid: true,
		Slots: []edgecontrol.EnergyScheduleSlot{
			{SlotID: "slot-precool", StartMinute: 10 * 60, EndMinute: 14 * 60, Mode: edgecontrol.ScheduleModePreCool, SupplyTempSetpointC: &preCoolSP},
		},
	}

	report, stale, err := daemon.Shadow().ApplyDesired(GatewayDesiredConfig{
		DesiredRevision:   1,
		DriverConfigs:     drivers,
		AutonomyPolicy:    &autonomy,
		EdgeRuleBundle:    ruleBundle,
		AggregationPolicy: aggPolicy,
		ClusterPolicy:     &clusterPolicy,
		EnergySchedule:    sched,
	})
	if err != nil || stale || report.AppliedStatus != shadowStatusApplied {
		t.Fatalf("ApplyDesired rev 1 failed: report=%+v stale=%v err=%v", report, stale, err)
	}

	// Version downgrade must be rejected and retain schedule version 2.
	older := sched.Clone()
	older.Version = 1
	rejectReport, _, err := daemon.Shadow().ApplyDesired(GatewayDesiredConfig{
		DesiredRevision: 2,
		EnergySchedule:  older,
	})
	if err != nil || rejectReport.AppliedStatus != shadowStatusRejected {
		t.Fatalf("expected REJECTED on schedule version downgrade, got status=%s err=%v", rejectReport.AppliedStatus, err)
	}
	if len(rejectReport.DriftDetails) != 1 || rejectReport.DriftDetails[0].Section != shadowDriftSectionEnergySchedule {
		t.Fatalf("expected ENERGY_SCHEDULE drift detail, got %+v", rejectReport.DriftDetails)
	}

	// Cycles at 11:00:00, 11:00:20, 11:00:40: ambient_temp stays buffered, rule_watch_temp transmits raw.
	ambients := []uint16{200, 260, 230}
	for i, rawAmb := range ambients {
		if i > 0 {
			curTime = curTime.Add(20 * time.Second)
		}
		modHandler.mu.Lock()
		modHandler.regs[101] = rawAmb
		modHandler.mu.Unlock()
		if _, err := daemon.StepCycle(t.Context()); err != nil {
			t.Fatalf("StepCycle %d: %v", i, err)
		}
	}

	seenPointsBeforeFlush := map[string][]float64{}
	for _, item := range daemon.Spool().Pending() {
		if !strings.HasSuffix(item.Topic, "/up/telemetry") {
			continue
		}
		var env mqttUplinkTelemetryEnvelope
		if err := json.Unmarshal(item.Payload, &env); err != nil {
			t.Fatal(err)
		}
		for _, dev := range env.Payload.Devices {
			for _, pt := range dev.Points {
				if num, ok := pt.Value.(float64); ok {
					seenPointsBeforeFlush[dev.DeviceID+"/"+pt.Code] = append(seenPointsBeforeFlush[dev.DeviceID+"/"+pt.Code], num)
				}
			}
		}
	}
	if _, exists := seenPointsBeforeFlush["weather-01/ambient_temp"]; exists {
		t.Fatalf("ambient_temp must not emit raw mid-window: %+v", seenPointsBeforeFlush)
	}
	if len(seenPointsBeforeFlush["chiller-01/rule_watch_temp"]) != 3 {
		t.Fatalf("rule_watch_temp must transmit raw on every cycle, got %+v", seenPointsBeforeFlush["chiller-01/rule_watch_temp"])
	}
	rawHistory, err := daemon.Timedata().History("weather-01/ambient_temp", 10)
	if err != nil || len(rawHistory) != 3 {
		t.Fatalf("expected 3 full-resolution FileTimedata records before window flush, got (%d, %v)", len(rawHistory), err)
	}

	// Cycle at 11:01:00 flushes [11:00:00, 11:01:00) mean=23.0, min=20.0, max=26.0.
	curTime = time.Date(2026, 11, 22, 11, 1, 0, 0, time.UTC)
	modHandler.mu.Lock()
	modHandler.regs[101] = 290
	modHandler.mu.Unlock()
	if _, err := daemon.StepCycle(t.Context()); err != nil {
		t.Fatalf("StepCycle flush: %v", err)
	}
	aggVals := map[string]float64{}
	for _, item := range daemon.Spool().Pending() {
		if !strings.HasSuffix(item.Topic, "/up/telemetry") {
			continue
		}
		var env mqttUplinkTelemetryEnvelope
		if err := json.Unmarshal(item.Payload, &env); err != nil {
			t.Fatal(err)
		}
		for _, dev := range env.Payload.Devices {
			if dev.DeviceID != "weather-01" {
				continue
			}
			for _, pt := range dev.Points {
				if num, ok := pt.Value.(float64); ok {
					aggVals[pt.Code] = num
				}
			}
		}
	}
	if aggVals["ambient_temp"] != 23.0 || aggVals["ambient_temp_min"] != 20.0 || aggVals["ambient_temp_max"] != 26.0 {
		t.Fatalf("unexpected flushed window telemetry: %+v", aggVals)
	}

	// Offline continuation at 11:05:00 keeps schedule active and fallback standby;
	// at 12:05:00 after LeaseExpiresAt, fallback takes over cleanly.
	curTime = time.Date(2026, 11, 22, 11, 5, 0, 0, time.UTC)
	if _, err := daemon.StepCycle(t.Context()); err != nil {
		t.Fatalf("StepCycle offline valid lease: %v", err)
	}
	if !daemon.ScheduleController().IsActiveAt(curTime) || daemon.fallback.StatusAt(curTime).FallbackActive {
		t.Fatalf("expected schedule active and fallback standby during valid offline lease")
	}

	curTime = time.Date(2026, 11, 22, 12, 5, 0, 0, time.UTC)
	if _, err := daemon.StepCycle(t.Context()); err != nil {
		t.Fatalf("StepCycle expired lease: %v", err)
	}
	if daemon.ScheduleController().IsActiveAt(curTime) || !daemon.fallback.StatusAt(curTime).FallbackActive {
		t.Fatalf("expected fallback active after schedule lease expiry")
	}
}

func TestDaemonMixedModbusBACnetOPCUAShadowHotReload(t *testing.T) {
	modEP := startLocalModbusTCP(t, map[uint16]uint16{100: 72})

	bacConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bacConn.Close() })
	go func() {
		buf := make([]byte, 2048)
		for {
			n, raddr, rErr := bacConn.ReadFromUDP(buf)
			if rErr != nil {
				return
			}
			pkt, dErr := edgecontrol.DecodeBACnetPacket(buf[:n])
			if dErr != nil || pkt.PDUType != edgecontrol.BACnetAPDUTypeConfirmedRequest {
				continue
			}
			if pkt.Service == edgecontrol.BACnetServiceReadProperty {
				objID, propID, _ := edgecontrol.DecodeBACnetReadPropertyRequest(pkt.Payload)
				ack, _ := edgecontrol.EncodeBACnetReadPropertyACK(objID, propID, edgecontrol.BACnetAppValue{Type: edgecontrol.DataTypeFloat, Real: 12.5})
				resp, _ := edgecontrol.EncodeBACnetPacket(edgecontrol.BACnetPacket{PDUType: edgecontrol.BACnetAPDUTypeComplexACK, InvokeID: pkt.InvokeID, Service: pkt.Service, Payload: ack})
				_, _ = bacConn.WriteToUDP(resp, raddr)
			}
		}
	}()

	uaLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = uaLn.Close() })
	go func() {
		for {
			c, aErr := uaLn.Accept()
			if aErr != nil {
				return
			}
			go func(conn net.Conn) {
				defer func() { _ = conn.Close() }()
				for {
					mType, frame, fErr := edgecontrol.ReadOPCUAFrame(conn)
					if fErr != nil {
						return
					}
					switch mType {
					case edgecontrol.OPCUAMsgHello:
						_, _ = conn.Write(edgecontrol.EncodeOPCUAAcknowledge())
					case edgecontrol.OPCUAMsgOpenChannel:
						opn, _ := edgecontrol.DecodeOPCUAOpenRequest(frame)
						_, _ = conn.Write(edgecontrol.EncodeOPCUAOpenResponse(edgecontrol.OPCUAOpenChannelFrame{
							SecurityPolicy: edgecontrol.OPCUASecurityPolicyNone, SeqNum: opn.SeqNum, RequestID: opn.RequestID, TokenChannelID: 11, TokenID: 1, RevisedLifetime: 3600000,
						}))
					case edgecontrol.OPCUAMsgMessage:
						chID, tokID, seq, reqID, body, _ := edgecontrol.DecodeOPCUASecureMessage(frame, edgecontrol.OPCUASecurityModeNone, nil)
						svc, _ := edgecontrol.PeekOPCUAServiceID(body)
						var resp []byte
						switch svc {
						case edgecontrol.OPCUAServiceCreateSessionReq:
							resp = edgecontrol.EncodeOPCUACreateSessionResponse(edgecontrol.OPCUAStatusGood, 21, nil)
						case edgecontrol.OPCUAServiceActivateSessionReq:
							resp = edgecontrol.EncodeOPCUAActivateSessionResponse(edgecontrol.OPCUAStatusGood)
						case edgecontrol.OPCUAServiceReadReq:
							resp = edgecontrol.EncodeOPCUAReadResponse(edgecontrol.OPCUAStatusGood, []edgecontrol.OPCUADataValue{{HasValue: true, Value: edgecontrol.DoubleValue(25.4), StatusCode: edgecontrol.OPCUAStatusGood}})
						}
						wire, _ := edgecontrol.EncodeOPCUASecureMessage(edgecontrol.OPCUAMsgMessage, chID, tokID, seq, reqID, resp, edgecontrol.OPCUASecurityModeNone, nil)
						_, _ = conn.Write(wire)
					}
				}
			}(c)
		}
	}()

	now := time.Date(2026, 11, 22, 13, 0, 0, 0, time.UTC)
	cfg := Config{GatewayID: "0193f000-2000-7000-8000-000000000299", StateDir: t.TempDir()}
	cfg.ApplyDefaults("0.1.2")
	daemon, err := NewDaemon(cfg, DaemonOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("NewDaemon: %v", err)
	}
	defer func() { _ = daemon.Close() }()

	uid, tmo, ret, devInst := 1, 500, 0, 501
	bacEP := bacConn.LocalAddr().String()
	uaEP := "opc.tcp://" + uaLn.Addr().String()
	rep, stale, err := daemon.Shadow().ApplyDesired(GatewayDesiredConfig{
		DesiredRevision: 1,
		DriverConfigs: []edgecontrol.ModbusDriverInput{
			{
				DeviceKey: "chiller-mod", DriverType: "MODBUS_TCP", Endpoint: &modEP, UnitID: &uid, TimeoutMS: &tmo, Retries: &ret,
				Registers: []edgecontrol.ModbusRegisterInput{{PointCode: "chw_temp", FunctionCode: 3, Address: 100, RegisterCount: 1, RawType: "UINT16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 0.1, PollPriority: "HIGH"}},
			},
			{
				DeviceKey: "ahu-bac", DriverType: edgecontrol.DriverTypeBACnetIP, Endpoint: &bacEP, TimeoutMS: &tmo, Retries: &ret,
				BACnet: &edgecontrol.BACnetDriverSpecInput{DeviceInstance: &devInst, Objects: []edgecontrol.BACnetObjectInput{{PointCode: "sa_temp", ObjectType: "ANALOG_INPUT", ObjectInstance: 1, PollPriority: "HIGH"}}},
			},
			{
				DeviceKey: "bms-ua", DriverType: edgecontrol.DriverTypeOPCUA, Endpoint: &uaEP, TimeoutMS: &tmo, Retries: &ret,
				OPCUA: &edgecontrol.OPCUADriverSpecInput{SecurityPolicy: edgecontrol.OPCUASecurityPolicyNone, SecurityMode: edgecontrol.OPCUASecurityModeNone, Nodes: []edgecontrol.OPCUANodeInput{{PointCode: "oat_temp", NodeID: "ns=2;i=10", PollPriority: "HIGH"}}},
			},
		},
	})
	if err != nil || stale || rep.AppliedStatus != shadowStatusApplied || len(rep.DriverConfigs) != 3 {
		t.Fatalf("ApplyDesired mixed protocols failed: rep=%+v stale=%v err=%v", rep, stale, err)
	}

	res, err := daemon.StepCycle(t.Context())
	if err != nil {
		t.Fatalf("StepCycle: %v", err)
	}
	for _, tc := range []struct {
		addr string
		want float64
	}{
		{"chiller-mod/chw_temp", 7.2},
		{"ahu-bac/sa_temp", 12.5},
		{"bms-ua/oat_temp", 25.4},
	} {
		snap, ok := res.Cycle.Image.Get(tc.addr)
		if !ok || !snap.HasValue || snap.Sample.Quality != edgecontrol.QualityGood {
			t.Fatalf("missing good sample for %s: %+v", tc.addr, snap)
		}
		if diff := snap.Sample.Value.Double - tc.want; diff < -0.01 || diff > 0.01 {
			t.Fatalf("%s = %v, want %v", tc.addr, snap.Sample.Value.Double, tc.want)
		}
	}
}
