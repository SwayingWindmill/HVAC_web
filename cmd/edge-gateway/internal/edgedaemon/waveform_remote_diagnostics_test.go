package edgedaemon

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
	"github.com/simonvetter/modbus"
)

func TestDaemonWaveformRingCapturesPreDeadbandAndRedactsLogs(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	modHandler := &fakeModbusServerHandler{regs: map[uint16]uint16{100: 70}}
	srv, err := modbus.NewServer(&modbus.ServerConfiguration{URL: "tcp://" + addr, Timeout: time.Second}, modHandler)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Stop() })

	uid, timeoutMS, retries := 1, 500, 0
	gwID := "0194f000-2000-7000-8000-000000000301"
	curTime := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)

	cfg := Config{
		GatewayID:                      gwID,
		StateDir:                       t.TempDir(),
		TelemetryDeadband:              0.5,
		TelemetryDeadbandMaxSilenceSec: 60,
		Drivers: []edgecontrol.ModbusDriverInput{{
			DeviceKey:  "chiller-01",
			DriverType: "MODBUS_TCP",
			Endpoint:   &addr,
			UnitID:     &uid,
			TimeoutMS:  &timeoutMS,
			Retries:    &retries,
			Registers: []edgecontrol.ModbusRegisterInput{
				{PointCode: "chw_supply_temp", FunctionCode: 3, Address: 100, RegisterCount: 1, RawType: "UINT16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 0.1, PollPriority: "HIGH"},
			},
		}},
	}
	cfg.ApplyDefaults("0.1.4")

	daemon, err := NewDaemon(cfg, DaemonOptions{Now: func() time.Time { return curTime }})
	if err != nil {
		t.Fatalf("NewDaemon: %v", err)
	}
	defer func() { _ = daemon.Close() }()

	if err := daemon.WaveformRing().SetRules([]edgecontrol.WaveformRuleConfig{{
		RuleID:          "0194f000-9900-7000-8000-000000000001",
		RuleName:        "冷冻水温度阶跃抓拍",
		Enabled:         true,
		DeviceCode:      "chiller-01",
		PointCode:       "chw_supply_temp",
		TriggerType:     edgecontrol.WaveformTriggerThresholdHigh,
		ThresholdValue:  12.0,
		PreSeconds:      5,
		PostSeconds:     5,
		SampleRateHz:    1,
		PointCodes:      []string{"chiller-01.chw_supply_temp"},
		CooldownSeconds: 60,
	}}); err != nil {
		t.Fatalf("SetRules: %v", err)
	}

	// Drive 4 baseline cycles (within 0.5 deadband after first cycle), then spike >= 12.0 and post-window cycles.
	vals := []uint16{70, 71, 72, 71, 135, 132, 129, 125, 121, 118, 115}
	for i, rawVal := range vals {
		modHandler.regs[100] = rawVal
		curTime = curTime.Add(time.Second)
		if _, err := daemon.StepCycle(t.Context()); err != nil {
			t.Fatalf("StepCycle[%d]: %v", i, err)
		}
	}

	var waveCount, metricsCount int
	for _, item := range daemon.Spool().Pending() {
		if item.Topic == edgecontrol.WaveformUplinkTopic(gwID) {
			waveCount++
			_, burst, err := edgecontrol.DecodeWaveformBurst(item.Payload, gwID)
			if err != nil {
				t.Fatalf("DecodeWaveformBurst: %v", err)
			}
			if len(burst.Samples) < 5 {
				t.Fatalf("expected pre/post high-freq samples even under deadband, got %d", len(burst.Samples))
			}
		}
		if item.Topic == edgecontrol.MetricsUplinkTopic(gwID) {
			metricsCount++
		}
	}
	if waveCount != 1 {
		t.Fatalf("expected 1 waveform burst, got %d", waveCount)
	}
	curTime = curTime.Add(30 * time.Second)
	if err := daemon.PublishHostMetricsOnce(t.Context()); err != nil {
		t.Fatalf("PublishHostMetricsOnce: %v", err)
	}
	metricsCount = 0
	for _, item := range daemon.Spool().Pending() {
		if item.Topic == edgecontrol.MetricsUplinkTopic(gwID) {
			metricsCount++
		}
	}
	if metricsCount < 1 {
		t.Fatalf("expected host metrics uplink, got %d", metricsCount)
	}

	// Test remote log pull with sensitive secret redaction + maxBytes cap.
	daemon.RemoteDiagnostics().RecordLogLine(curTime, "auth token=secret-gateway-jwt Bearer supersecrettoken")
	daemon.RemoteDiagnostics().RecordLogLine(curTime, "normal rs485 cycle ok")
	cmdBytes, err := edgecontrol.EncodeDiagnosticsDownlink(gwID, edgecontrol.DiagnosticsDownlinkPayload{
		RequestID:    "0194f000-9900-7000-8000-000000000009",
		Action:       edgecontrol.DiagnosticsActionPullLogBundle,
		MaxBytes:     2048,
		SinceMinutes: 15,
	}, curTime)
	if err != nil {
		t.Fatalf("EncodeDiagnosticsDownlink: %v", err)
	}
	if err := daemon.HandleDiagnosticsDownlink(t.Context(), cmdBytes); err != nil {
		t.Fatalf("HandleDiagnosticsDownlink: %v", err)
	}

	var diagReplyCount int
	for _, item := range daemon.Spool().Pending() {
		if item.Topic == edgecontrol.DiagnosticsUplinkTopic(gwID) {
			diagReplyCount++
			_, reply, err := edgecontrol.DecodeDiagnosticsLogReply(item.Payload, gwID)
			if err != nil {
				t.Fatalf("DecodeDiagnosticsLogReply: %v", err)
			}
			if strings.Contains(reply.Content, "secret-gateway-jwt") || strings.Contains(reply.Content, "supersecrettoken") {
				t.Fatalf("unredacted secret in log bundle: %s", reply.Content)
			}
			if reply.RedactedCount < 2 {
				t.Fatalf("expected >= 2 redactions, got %d", reply.RedactedCount)
			}
		}
	}
	if diagReplyCount != 1 {
		t.Fatalf("expected 1 diagnostics log reply, got %d", diagReplyCount)
	}
}
