package edgedaemon

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
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
