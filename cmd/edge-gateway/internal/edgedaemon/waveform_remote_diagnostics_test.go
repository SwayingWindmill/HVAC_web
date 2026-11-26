package edgedaemon

import (
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
	"github.com/quanlaihe/hvac-web/libs/mqttclient"
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

func TestRemoteDiagnosticsRealCPUAndLinuxProcMemory(t *testing.T) {
	t0 := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	collector := NewRemoteDiagnosticsCollector(t.TempDir(), t0)

	first := collector.SampleHostMetrics(t0, nil)
	if first.CPUPercent != 0 {
		t.Fatalf("expected first CPU sample baseline to be 0, got %v", first.CPUPercent)
	}
	if first.MemoryUsedPercent <= 0 || first.MemoryUsedPercent > 100 {
		t.Fatalf("expected realistic MemoryUsedPercent in (0, 100], got %v", first.MemoryUsedPercent)
	}

	// Execute real deterministic CPU work without sleeping so Getrusage user+system time advances.
	var acc uint64 = 1
	startWall := time.Now()
	for i := uint64(1); i < 35_000_000; i++ {
		acc = acc*6364136223846793005 + i
	}
	if acc == 0 {
		t.Fatal("unreachable")
	}
	elapsed := time.Since(startWall)
	if elapsed < time.Millisecond {
		elapsed = time.Millisecond
	}
	second := collector.SampleHostMetrics(t0.Add(elapsed), nil)
	if second.CPUPercent <= 0 || second.CPUPercent > 100 {
		t.Fatalf("expected positive real CPU percentage in (0, 100], got %v", second.CPUPercent)
	}

	// Verify /proc/self/statm + /proc/meminfo calculation math directly.
	tmpDir := t.TempDir()
	statmPath := filepath.Join(tmpDir, "statm")
	meminfoPath := filepath.Join(tmpDir, "meminfo")
	if err := os.WriteFile(statmPath, []byte("1000 512 0 0 0 0 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(meminfoPath, []byte("MemTotal:       10240 kB\nMemFree:         2048 kB\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pct, ok := readLinuxProcMemoryPercent(statmPath, meminfoPath, 4096)
	if !ok || math.Abs(pct-20.0) > 1e-6 {
		t.Fatalf("readLinuxProcMemoryPercent = (%v, %v), want (20.0, true)", pct, ok)
	}
}

func TestDaemonLoggerMQTTAndWaveformRulesSyncAndPersistence(t *testing.T) {
	const gwID = "0193f000-2000-7000-8000-000000000091"
	curTime := time.Date(2026, 11, 26, 11, 0, 0, 0, time.UTC)

	pullRule := edgecontrol.WaveformRuleConfig{
		RuleID:          "wf-pull-1",
		RuleName:        "Cloud pulled rule",
		Enabled:         true,
		DeviceCode:      "chiller-1",
		PointCode:       "evap_temp",
		TriggerType:     edgecontrol.WaveformTriggerThresholdHigh,
		ThresholdValue:  11.5,
		PreSeconds:      10,
		PostSeconds:     20,
		SampleRateHz:    2,
		PointCodes:      []string{"evap_temp"},
		CooldownSeconds: 30,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/gateway/v1/waveform-rules" || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"gatewayId": gwID,
			"rules":     []edgecontrol.WaveformRuleConfig{pullRule},
		})
	}))
	defer srv.Close()

	stateDir := t.TempDir()
	var baseLogBuf syncTestBuffer
	baseLogger := slog.New(slog.NewTextHandler(&baseLogBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	d, err := NewDaemon(Config{
		GatewayID:       gwID,
		StateDir:        stateDir,
		PlatformBaseURL: srv.URL,
		CycleIntervalMS: 1000,
		SpoolMaxBytes:   4 << 20,
		RuntimeVersion:  "1.0.0-test",
		HardwareModel:   "EG8200",
		TargetOS:        "linux",
		TargetArch:      "arm",
		OTA:             OTAConfig{Dir: filepath.Join(stateDir, "ota")},
	}, DaemonOptions{
		Logger: baseLogger,
		Now:    func() time.Time { return curTime },
	})
	if err != nil {
		t.Fatalf("NewDaemon: %v", err)
	}
	defer func() { _ = d.Close() }()

	// 1. Daemon logger wraps base handler and redacts pulled diagnostics logs.
	d.logger.Info("daemon_diag_marker", "Authorization", "Bearer top-secret-jwt-999")
	if !strings.Contains(baseLogBuf.String(), "daemon_diag_marker") {
		t.Fatalf("expected wrapped logger to still write to base handler, got %q", baseLogBuf.String())
	}
	pullWire, err := edgecontrol.EncodeDiagnosticsDownlink(gwID, edgecontrol.DiagnosticsDownlinkPayload{
		RequestID:    "019b0000-1000-7000-8000-000000000201",
		Action:       edgecontrol.DiagnosticsActionPullLogBundle,
		MaxBytes:     16384,
		SinceMinutes: 10,
	}, curTime)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.HandleDiagnosticsDownlink(context.Background(), pullWire); err != nil {
		t.Fatalf("HandleDiagnosticsDownlink PULL_LOG_BUNDLE: %v", err)
	}
	var sawDiagLog bool
	for _, it := range d.Spool().Pending() {
		if strings.HasSuffix(it.Topic, "/up/diagnostics") {
			var env edgecontrol.ShadowReplicationEnvelope
			if err := json.Unmarshal(it.Payload, &env); err == nil {
				var reply edgecontrol.DiagnosticsLogReplyPayload
				if err := json.Unmarshal(env.Payload, &reply); err == nil {
					if !strings.Contains(reply.Content, "daemon_diag_marker") || strings.Contains(reply.Content, "top-secret-jwt-999") || reply.RedactedCount < 1 {
						t.Fatalf("unexpected diagnostics log reply: %+v", reply)
					}
					sawDiagLog = true
				}
			}
		}
	}
	if !sawDiagLog {
		t.Fatal("expected PULL_LOG_BUNDLE reply captured via WrapLoggerWithDiagnostics")
	}

	// 2. MQTT session state transitions count reconnects monotonically per generation > 1 and track RTT.
	d.handleMQTTSessionState(mqttclient.SessionState{Generation: 1, Connected: true, Subscribed: true})
	d.handleMQTTSessionState(mqttclient.SessionState{Generation: 2, Connected: true, Subscribed: true})
	d.handleMQTTSessionState(mqttclient.SessionState{Generation: 2, Connected: true, Subscribed: true}) // publish notify in same gen
	d.handleMQTTSessionState(mqttclient.SessionState{Generation: 3, Connected: true, Subscribed: true})
	d.RemoteDiagnostics().RecordMQTTStatus(6.75, false)
	hm := d.RemoteDiagnostics().SampleHostMetrics(curTime, d.WaveformRing())
	if hm.MQTTReconnectCount != 2 || hm.MQTTRTTMs != 6.75 {
		t.Fatalf("expected MQTTReconnectCount=2 and MQTTRTTMs=6.75, got %+v", hm)
	}

	// 3. SYNC_WAVEFORM_RULES downlink updates live rules and persists waveform-rules.json across restart.
	syncRule := pullRule
	syncRule.RuleID = "wf-sync-1"
	syncRule.ThresholdValue = 14.2
	syncWire, err := edgecontrol.EncodeDiagnosticsDownlink(gwID, edgecontrol.DiagnosticsDownlinkPayload{
		RequestID: "019b0000-1000-7000-8000-000000000202",
		Action:    edgecontrol.DiagnosticsActionSyncWaveformRules,
		Rules:     []edgecontrol.WaveformRuleConfig{syncRule},
	}, curTime)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.HandleDiagnosticsDownlink(context.Background(), syncWire); err != nil {
		t.Fatalf("HandleDiagnosticsDownlink SYNC_WAVEFORM_RULES: %v", err)
	}
	if got := d.WaveformRing().Rules(); len(got) != 1 || got[0].RuleID != "wf-sync-1" {
		t.Fatalf("unexpected live rules after SYNC_WAVEFORM_RULES: %+v", got)
	}
	if _, statErr := os.Stat(filepath.Join(stateDir, waveformRulesFileName)); statErr != nil {
		t.Fatalf("expected persisted waveform-rules.json: %v", statErr)
	}
	_ = d.Close()

	// Reopen daemon from same StateDir: loads persisted rule on boot, then pullAndSyncWaveformRules converges to server rules.
	d2, err := NewDaemon(Config{
		GatewayID:       gwID,
		StateDir:        stateDir,
		PlatformBaseURL: srv.URL,
		CycleIntervalMS: 1000,
		SpoolMaxBytes:   4 << 20,
		RuntimeVersion:  "1.0.0-test",
		HardwareModel:   "EG8200",
		TargetOS:        "linux",
		TargetArch:      "arm",
		OTA:             OTAConfig{Dir: filepath.Join(stateDir, "ota")},
	}, DaemonOptions{
		Now: func() time.Time { return curTime },
	})
	if err != nil {
		t.Fatalf("NewDaemon reopen: %v", err)
	}
	defer func() { _ = d2.Close() }()
	if got := d2.WaveformRing().Rules(); len(got) != 1 || got[0].RuleID != "wf-sync-1" {
		t.Fatalf("expected NewDaemon to reload persisted waveform rule, got %+v", got)
	}
	if err := d2.pullAndSyncWaveformRules(context.Background()); err != nil {
		t.Fatalf("pullAndSyncWaveformRules: %v", err)
	}
	if got := d2.WaveformRing().Rules(); len(got) != 1 || got[0].RuleID != "wf-pull-1" {
		t.Fatalf("expected pullAndSyncWaveformRules to converge to %q, got %+v", "wf-pull-1", got)
	}
}

type syncTestBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncTestBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncTestBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
