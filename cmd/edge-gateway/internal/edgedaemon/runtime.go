package edgedaemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/eclipse/paho.golang/paho"
	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
	"github.com/quanlaihe/hvac-web/libs/mqttclient"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/tunnel"
)

type mqttUplinkTelemetryEnvelope struct {
	SchemaVersion string                  `json:"schemaVersion"`
	MessageID     string                  `json:"messageId"`
	GatewayID     string                  `json:"gatewayId"`
	Timestamp     int64                   `json:"timestamp"`
	Sequence      uint64                  `json:"sequence"`
	Replay        bool                    `json:"replay"`
	Payload       mqttUplinkDevicePayload `json:"payload"`
}

type mqttUplinkDevicePayload struct {
	Devices []mqttUplinkDevice `json:"devices"`
}

type mqttUplinkDevice struct {
	DeviceID        string            `json:"deviceId"`
	DeviceTimestamp int64             `json:"deviceTimestamp"`
	Points          []mqttUplinkPoint `json:"points"`
}

type mqttUplinkPoint struct {
	Code    string  `json:"code"`
	Value   any     `json:"value"`
	Quality uint8   `json:"quality"`
	Unit    *string `json:"unit,omitempty"`
}

type mqttUplinkEventEnvelope struct {
	SchemaVersion string                 `json:"schemaVersion"`
	MessageID     string                 `json:"messageId"`
	GatewayID     string                 `json:"gatewayId"`
	Timestamp     int64                  `json:"timestamp"`
	Sequence      uint64                 `json:"sequence"`
	Payload       mqttUplinkEventPayload `json:"payload"`
}

type mqttUplinkEventPayload struct {
	EventType  string         `json:"eventType"`
	SourceType string         `json:"sourceType"`
	SourceID   string         `json:"sourceId"`
	EventTime  int64          `json:"eventTime"`
	Severity   string         `json:"severity"`
	Data       map[string]any `json:"data,omitempty"`
}

// DaemonOptions configures optional runtime hooks such as OTA restart triggers.
type DaemonOptions struct {
	Logger          *slog.Logger
	ProbeBinary     func(context.Context, string) error
	OnOTASwitchBoot func()
	Now             func() time.Time
}

// Daemon owns the complete Linux edge runtime lifecycle on EG8200 gateways.
type Daemon struct {
	cfg           Config
	obs           *observability.Runtime
	logger        *slog.Logger
	host          *edgecontrol.Host
	registry      *ProtocolBridgeRegistry
	interlock     *edgecontrol.HydraulicInterlockController
	fallback      *edgecontrol.AutonomousFallbackController
	ruleEngine    *edgecontrol.EdgeRuleController
	spool         *GradedSpool
	shadow        *ShadowManager
	commands      *CommandAdapter
	ota           *OTAAgent
	tunnelClient  *TunnelClient
	healthSrv     *http.Server
	healthAddr    string
	mqttSession   *mqttclient.SharedSession
	onOTASwitch   func()
	cmdSem        chan struct{}
	cmdWG         sync.WaitGroup
	sequence      atomic.Uint64
	mqttConnected atomic.Bool
	ready         atomic.Bool
	now           func() time.Time
	mu            sync.Mutex
	closed        bool
}

// NewDaemon initializes the edge host, controllers, spool, shadow, OTA agent,
// optional reverse tunnel client, and optional loopback health endpoint.
func NewDaemon(cfg Config, opts DaemonOptions) (*Daemon, error) {
	cfg.ApplyDefaults(cfg.RuntimeVersion)
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	obs := observability.NewRuntime(observability.RuntimeConfig{
		Service: "edge-gateway",
		Logger:  logger,
	})

	host, err := edgecontrol.NewHost()
	if err != nil {
		return nil, fmt.Errorf("create edgecontrol host: %w", err)
	}
	registry := NewProtocolBridgeRegistry(cfg.SerialBuses)

	policy := edgecontrol.DefaultAutonomyPolicyConfig()
	if cfg.AutonomyPolicy != nil {
		policy = *cfg.AutonomyPolicy
	}
	interlock, err := edgecontrol.NewHydraulicInterlockController(edgecontrol.HydraulicInterlockConfig{
		Policy: policy,
	})
	if err != nil {
		_ = registry.Close()
		return nil, fmt.Errorf("create hydraulic interlock: %w", err)
	}
	fallback, err := edgecontrol.NewAutonomousFallbackController(edgecontrol.AutonomousFallbackConfig{
		Policy: policy,
	})
	if err != nil {
		_ = registry.Close()
		return nil, fmt.Errorf("create autonomous fallback: %w", err)
	}
	ruleEngine := edgecontrol.NewEdgeRuleController(edgecontrol.EdgeRuleControllerConfig{})
	intentController, err := edgecontrol.NewIntentController("cloud-command", host.IntentStore())
	if err != nil {
		_ = registry.Close()
		return nil, fmt.Errorf("create intent controller: %w", err)
	}
	if err := host.Start([]edgecontrol.ControllerBinding{
		{Priority: 10, Critical: true, Controller: interlock},
		{Priority: 20, Controller: fallback},
		{Priority: 30, Controller: ruleEngine},
		{Priority: 100, Controller: intentController},
	}); err != nil {
		_ = registry.Close()
		return nil, fmt.Errorf("start edgecontrol scheduler: %w", err)
	}

	spool, err := OpenGradedSpool(filepath.Join(cfg.StateDir, "spool"), cfg.SpoolMaxBytes)
	if err != nil {
		_ = registry.Close()
		return nil, fmt.Errorf("open graded spool: %w", err)
	}

	nowFn := opts.Now
	if nowFn == nil {
		nowFn = time.Now
	}

	shadow := NewShadowManager(cfg.GatewayID, cfg.StateDir, host, registry, interlock, fallback, ruleEngine, logger)
	shadow.now = nowFn
	if err := shadow.LoadOrBootstrap(cfg); err != nil {
		_ = registry.Close()
		return nil, fmt.Errorf("bootstrap config shadow: %w", err)
	}

	commands, err := NewCommandAdapter(cfg.GatewayID, cfg.StateDir, host, nowFn, logger)
	if err != nil {
		_ = registry.Close()
		return nil, fmt.Errorf("init command adapter: %w", err)
	}

	ota, err := NewOTAAgent(OTAAgentOptions{
		Dir:             cfg.OTA.Dir,
		CurrentVersion:  cfg.RuntimeVersion,
		HardwareModel:   cfg.HardwareModel,
		TargetOS:        cfg.TargetOS,
		TargetArch:      cfg.TargetArch,
		PlatformBaseURL: cfg.PlatformBaseURL,
		ServerName:      cfg.ServerName,
		CAFile:          cfg.CAFile,
		CertFile:        cfg.CertFile,
		KeyFile:         cfg.KeyFile,
		TrustedKeys:     cfg.OTA.TrustedPublicKeys,
		ProbeBinary:     opts.ProbeBinary,
		Logger:          logger,
	})
	if err != nil {
		_ = registry.Close()
		return nil, fmt.Errorf("create OTA agent: %w", err)
	}

	var tunnelClient *TunnelClient
	if cfg.Tunnel.Enabled {
		tunnelClient, err = NewTunnelClient(TunnelClientOptions{
			PlatformBaseURL:    cfg.PlatformBaseURL,
			ServerName:         cfg.ServerName,
			CAFile:             cfg.CAFile,
			CertFile:           cfg.CertFile,
			KeyFile:            cfg.KeyFile,
			AllowedTargetPorts: cfg.Tunnel.AllowedTargetPorts,
			PingInterval:       time.Duration(cfg.Tunnel.PingIntervalSec) * time.Second,
			Logger:             logger,
		})
		if err != nil {
			_ = registry.Close()
			return nil, fmt.Errorf("create tunnel client: %w", err)
		}
	}

	d := &Daemon{
		cfg:          cfg,
		obs:          obs,
		logger:       logger,
		host:         host,
		registry:     registry,
		interlock:    interlock,
		fallback:     fallback,
		ruleEngine:   ruleEngine,
		spool:        spool,
		shadow:       shadow,
		commands:     commands,
		ota:          ota,
		tunnelClient: tunnelClient,
		onOTASwitch:  opts.OnOTASwitchBoot,
		cmdSem:       make(chan struct{}, 64),
		now:          nowFn,
	}

	if strings.TrimSpace(cfg.HealthListenAddr) != "" {
		if err := d.startHealthServer(cfg.HealthListenAddr); err != nil {
			_ = commands.Close()
			_ = registry.Close()
			return nil, err
		}
	}
	return d, nil
}

// HealthAddr returns the active loopback health listener address (or empty if disabled).
func (d *Daemon) HealthAddr() string {
	return d.healthAddr
}

// Host returns the managed edgecontrol.Host instance.
func (d *Daemon) Host() *edgecontrol.Host {
	return d.host
}

// Shadow returns the managed ShadowManager.
func (d *Daemon) Shadow() *ShadowManager {
	return d.shadow
}

// OTAAgent returns the managed OTAAgent.
func (d *Daemon) OTAAgent() *OTAAgent {
	return d.ota
}

// Spool returns the managed GradedSpool.
func (d *Daemon) Spool() *GradedSpool {
	return d.spool
}

// CommandAdapter returns the managed MQTT CommandAdapter.
func (d *Daemon) CommandAdapter() *CommandAdapter {
	return d.commands
}

// Interlock returns the Daemon's HydraulicInterlockController.
func (d *Daemon) Interlock() *edgecontrol.HydraulicInterlockController {
	return d.interlock
}

// HandleCommandPayload validates a v2.0 cloud command envelope, registers its ControlIntent,
// waits for the next cycle outcome, and returns the serialized CommandReplyWire payload.
func (d *Daemon) HandleCommandPayload(ctx context.Context, payload []byte) ([]byte, bool) {
	if d == nil || d.commands == nil {
		return nil, false
	}
	return d.commands.HandlePayload(ctx, payload)
}

// VerifyOTAOnBoot resolves any pending HEALTH_CHECKING OTA transition on startup,
// spooling or publishing the resulting SUCCEEDED or ROLLED_BACK event.
func (d *Daemon) VerifyOTAOnBoot(ctx context.Context) (bool, error) {
	if !d.cfg.OTA.Enabled || d.ota == nil {
		return false, nil
	}
	return d.ota.VerifyPendingUpdateOnBoot(ctx, d.CheckBootHealth, func(res edgecontrol.OTAResultPayload) {
		wire, err := edgecontrol.EncodeOTAResult(d.cfg.GatewayID, res, d.now().UTC())
		if err != nil {
			d.logger.Warn("edge_ota_boot_report_encode_failed", "error", err.Error())
			return
		}
		d.publishOrSpool(ctx, edgecontrol.OTAUplinkTopic(d.cfg.GatewayID), 1, EvidenceOTAResult, wire)
	})
}

// CheckBootHealth runs lightweight in-process checks used for OTA startup verification.
func (d *Daemon) CheckBootHealth(ctx context.Context) error {
	if _, err := d.StepCycle(ctx); err != nil {
		return err
	}
	return nil
}

// StepCycle synchronously runs one edge control cycle, updates cloud status,
// and emits or spools telemetry and edge rule events.
func (d *Daemon) StepCycle(ctx context.Context) (edgecontrol.HostCycleResult, error) {
	now := d.now().UTC()
	d.fallback.SetCloudConnected(d.mqttConnected.Load(), now)
	res, err := d.host.RunCycle(ctx, now)
	if err != nil {
		return res, err
	}
	if d.commands != nil {
		d.commands.ResolveCycle(now, res.Cycle, res.WriteResults)
	}
	d.emitCycleTelemetry(ctx, res.Cycle.Image, now)
	d.emitRuleEvents(ctx, now)
	return res, nil
}

// Run runs the full daemon loops (control cycle, MQTT, shadow sync, cert renewal, reverse tunnel)
// until ctx is canceled, and closes resources cleanly before returning.
func (d *Daemon) Run(ctx context.Context) error {
	if strings.TrimSpace(d.cfg.MQTTBrokerURL) != "" {
		if err := d.startMQTTSession(ctx); err != nil {
			d.logger.Warn("edge_mqtt_startup_failed", "error", err.Error())
		}
	}
	d.ready.Store(true)
	d.obs.MarkReady()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		d.runCycleLoop(ctx)
	}()

	if strings.TrimSpace(d.cfg.PlatformBaseURL) != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d.runShadowLoop(ctx)
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			d.runCertRenewLoop(ctx)
		}()
	}

	if d.tunnelClient != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = d.tunnelClient.Run(ctx)
		}()
	}

	<-ctx.Done()
	d.ready.Store(false)
	d.obs.MarkNotReady()
	wg.Wait()
	_ = d.Close()
	return nil
}

// Close shuts down the HTTP health server, MQTT session, and shared southbound bridges.
func (d *Daemon) Close() error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	d.closed = true
	d.mu.Unlock()

	var errs []error
	if d.healthSrv != nil {
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		if err := d.healthSrv.Shutdown(shutCtx); err != nil {
			errs = append(errs, err)
		}
		cancel()
	}
	if d.mqttSession != nil {
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = d.mqttSession.Close(shutCtx)
		cancel()
	}
	if d.commands != nil {
		_ = d.commands.Close()
	}
	d.cmdWG.Wait()
	if err := d.registry.Close(); err != nil {
		errs = append(errs, err)
	}
	shutObsCtx, obsCancel := context.WithTimeout(context.Background(), 2*time.Second)
	_ = d.obs.Shutdown(shutObsCtx)
	obsCancel()
	return errors.Join(errs...)
}

func (d *Daemon) startHealthServer(listenAddr string) error {
	if err := tunnel.ValidateLoopbackAddr(listenAddr); err != nil {
		return err
	}
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("listen loopback health endpoint %s: %w", listenAddr, err)
	}
	mux := http.NewServeMux()
	diag := d.obs.DiagnosticsHandler()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		diag.ServeHTTP(w, r)
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if !d.ready.Load() {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		diag.ServeHTTP(w, r)
	})
	mux.Handle("GET /metrics", diag)

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
	}
	d.healthSrv = srv
	d.healthAddr = ln.Addr().String()
	go func() {
		if serveErr := srv.Serve(ln); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			d.logger.Warn("edge_health_server_error", "error", serveErr.Error())
		}
	}()
	return nil
}

func (d *Daemon) runCycleLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(d.cfg.CycleIntervalMS) * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := d.StepCycle(ctx); err != nil && ctx.Err() == nil {
				d.logger.Warn("edge_cycle_error", "error", err.Error())
			}
		}
	}
}

func (d *Daemon) runShadowLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(d.cfg.ShadowPollIntervalSec) * time.Second)
	defer ticker.Stop()
	syncOnce := func() {
		if err := d.shadow.PullAndSync(ctx, d.cfg); err != nil && ctx.Err() == nil {
			d.logger.Warn("edge_shadow_sync_failed", "error", err.Error())
		}
	}
	syncOnce()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			syncOnce()
		}
	}
}

func (d *Daemon) runCertRenewLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(d.cfg.CertRenewCheckIntervalSec) * time.Second)
	defer ticker.Stop()
	checkOnce := func() {
		res, err := CheckAndRenewCertificate(ctx, CertRenewOptions{
			PlatformBaseURL: d.cfg.PlatformBaseURL,
			GatewayID:       d.cfg.GatewayID,
			ServerName:      d.cfg.ServerName,
			CertFile:        d.cfg.CertFile,
			KeyFile:         d.cfg.KeyFile,
			CAFile:          d.cfg.CAFile,
			ThresholdDays:   d.cfg.CertRenewThresholdDays,
			OnRenewed: func(CertRenewResult) {
				if d.tunnelClient != nil {
					d.tunnelClient.ReloadCredentials()
				}
			},
		})
		if err != nil && ctx.Err() == nil {
			d.logger.Warn("edge_cert_renew_check_failed", "error", err.Error())
			return
		}
		if res.Renewed {
			d.logger.Info("edge_cert_renewed", "expires_at", res.ExpiresAt.Format(time.RFC3339))
		}
	}
	checkOnce()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			checkOnce()
		}
	}
}

// buildMQTTTLSConfig allows MQTT sessions to verify an internal broker CA and SNI
// independently when HTTPS platform traffic terminates behind a public CA proxy.
func buildMQTTTLSConfig(cfg Config) *mqttclient.TLSConfig {
	caFile := firstNonEmpty(cfg.MQTTCAFile, cfg.CAFile)
	certFile := strings.TrimSpace(cfg.CertFile)
	keyFile := strings.TrimSpace(cfg.KeyFile)
	if caFile == "" || certFile == "" || keyFile == "" {
		return nil
	}
	return &mqttclient.TLSConfig{
		CACertFile:     caFile,
		ClientCertFile: certFile,
		ClientKeyFile:  keyFile,
		ServerName:     firstNonEmpty(cfg.MQTTServerName, cfg.ServerName),
	}
}

func (d *Daemon) startMQTTSession(ctx context.Context) error {
	tlsCfg := buildMQTTTLSConfig(d.cfg)
	session, err := mqttclient.NewSharedSession(ctx, mqttclient.SharedSessionConfig{
		BrokerURL: d.cfg.MQTTBrokerURL,
		ClientID:  "edge-gateway-" + d.cfg.GatewayID,
		TLS:       tlsCfg,
	})
	if err != nil {
		return err
	}
	if _, err := session.RegisterRoute("edge-shadow-downlink", []string{edgecontrol.ShadowDownlinkTopic(d.cfg.GatewayID)}, func(pkt *paho.Publish) {
		ackBytes, err := d.shadow.HandleMQTTChangeBatch(pkt.Payload)
		if err != nil {
			d.logger.Warn("edge_shadow_batch_error", "error", err.Error())
			return
		}
		if len(ackBytes) > 0 {
			d.publishOrSpool(ctx, edgecontrol.ShadowUplinkTopic(d.cfg.GatewayID), 1, EvidenceConfigResult, ackBytes)
		}
	}); err != nil {
		return err
	}
	if _, err := session.RegisterRoute("edge-ota-downlink", []string{edgecontrol.OTADownlinkTopic(d.cfg.GatewayID)}, func(pkt *paho.Publish) {
		_, stage, err := edgecontrol.DecodeOTAStage(pkt.Payload, d.cfg.GatewayID)
		if err != nil {
			d.logger.Warn("edge_ota_decode_error", "error", err.Error())
			return
		}
		var switchPending bool
		_ = d.ota.ApplyStage(ctx, stage, func(res edgecontrol.OTAResultPayload) {
			if res.Stage == edgecontrol.OTAStageHealthChecking {
				switchPending = true
			}
			wire, encErr := edgecontrol.EncodeOTAResult(d.cfg.GatewayID, res, d.now().UTC())
			if encErr == nil {
				d.publishOrSpool(ctx, edgecontrol.OTAUplinkTopic(d.cfg.GatewayID), 1, EvidenceOTAResult, wire)
			}
		})
		if switchPending && d.onOTASwitch != nil {
			d.onOTASwitch()
		}
	}); err != nil {
		return err
	}
	if _, err := session.RegisterRoute("edge-command-downlink", []string{CommandDownlinkTopic(d.cfg.GatewayID)}, func(pkt *paho.Publish) {
		payload := append([]byte(nil), pkt.Payload...)
		select {
		case d.cmdSem <- struct{}{}:
			d.cmdWG.Add(1)
			go func() {
				defer func() {
					<-d.cmdSem
					d.cmdWG.Done()
				}()
				cmdCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
				defer cancel()
				if replyBytes, ok := d.HandleCommandPayload(cmdCtx, payload); ok && len(replyBytes) > 0 {
					d.publishOrSpool(ctx, CommandReplyUplinkTopic(d.cfg.GatewayID), 1, EvidenceControl, replyBytes)
				}
			}()
		default:
			d.logger.Warn("edge_command_queue_full")
		}
	}); err != nil {
		return err
	}

	session.AddObserver(func(state mqttclient.SessionState) {
		connected := state.Healthy()
		d.mqttConnected.Store(connected)
		if connected {
			_, _ = d.spool.Drain(ctx, 200, func(pubCtx context.Context, item SpoolItem) error {
				_, pubErr := session.Publish(pubCtx, &paho.Publish{
					Topic:   item.Topic,
					QoS:     item.QoS,
					Payload: item.Payload,
				})
				return pubErr
			})
		}
	})
	d.mqttSession = session
	return nil
}

func (d *Daemon) emitCycleTelemetry(ctx context.Context, img edgecontrol.ProcessImage, now time.Time) {
	snaps := img.Channels()
	if len(snaps) == 0 {
		return
	}
	byDevice := make(map[string][]mqttUplinkPoint)
	for _, snap := range snaps {
		if !snap.HasValue {
			continue
		}
		val, ok := valueToTelemetryJSON(snap.Sample.Value)
		if !ok {
			continue
		}
		quality := uint8(0)
		if snap.Sample.Quality != edgecontrol.QualityGood {
			quality = 1
		}
		var unitPtr *string
		if strings.TrimSpace(snap.Descriptor.Unit) != "" {
			u := snap.Descriptor.Unit
			unitPtr = &u
		}
		byDevice[snap.Descriptor.ComponentID] = append(byDevice[snap.Descriptor.ComponentID], mqttUplinkPoint{
			Code:    snap.Descriptor.ChannelID,
			Value:   val,
			Quality: quality,
			Unit:    unitPtr,
		})
	}
	if len(byDevice) == 0 {
		return
	}

	devKeys := make([]string, 0, len(byDevice))
	for k := range byDevice {
		devKeys = append(devKeys, k)
	}
	sort.Strings(devKeys)

	devices := make([]mqttUplinkDevice, 0, len(devKeys))
	tsMs := max(now.UnixMilli(), 1)
	for _, k := range devKeys {
		devices = append(devices, mqttUplinkDevice{
			DeviceID:        k,
			DeviceTimestamp: tsMs,
			Points:          byDevice[k],
		})
	}
	msgID, err := edgecontrol.NewReplicationUUIDv7(now)
	if err != nil {
		return
	}
	seq := d.sequence.Add(1)
	wire, err := json.Marshal(mqttUplinkTelemetryEnvelope{
		SchemaVersion: "2.0",
		MessageID:     msgID,
		GatewayID:     d.cfg.GatewayID,
		Timestamp:     tsMs,
		Sequence:      seq,
		Replay:        false,
		Payload:       mqttUplinkDevicePayload{Devices: devices},
	})
	if err != nil {
		return
	}
	d.publishOrSpool(ctx, "hvac/v1/"+d.cfg.GatewayID+"/up/telemetry", 1, EvidenceTelemetryNormal, wire)
}

func (d *Daemon) emitRuleEvents(ctx context.Context, now time.Time) {
	events := d.ruleEngine.DrainEvents()
	for _, ev := range events {
		msgID, err := edgecontrol.NewReplicationUUIDv7(now)
		if err != nil {
			continue
		}
		seq := d.sequence.Add(1)
		severity := strings.ToUpper(strings.TrimSpace(ev.Severity))
		if severity != "INFO" && severity != "WARNING" && severity != "CRITICAL" {
			severity = "WARNING"
		}
		wire, err := json.Marshal(mqttUplinkEventEnvelope{
			SchemaVersion: "2.0",
			MessageID:     msgID,
			GatewayID:     d.cfg.GatewayID,
			Timestamp:     max(now.UnixMilli(), 1),
			Sequence:      seq,
			Payload: mqttUplinkEventPayload{
				EventType:  "EDGE_RULE_" + ev.EventType,
				SourceType: "RULE",
				SourceID:   ev.RuleID,
				EventTime:  max(now.UnixMilli(), 1),
				Severity:   severity,
				Data: map[string]any{
					"title":            ev.Title,
					"alarmType":        ev.AlarmType,
					"bundleRevision":   ev.BundleRevision,
					"edgeBundleDigest": ev.EdgeBundleDigest,
					"offline":          ev.Offline,
					"reason":           ev.Reason,
				},
			},
		})
		if err != nil {
			continue
		}
		d.publishOrSpool(ctx, "hvac/v1/"+d.cfg.GatewayID+"/up/event", 1, EvidenceAlarm, wire)
	}
}

func (d *Daemon) publishOrSpool(ctx context.Context, topic string, qos byte, class EvidenceClass, payload []byte) {
	if d.mqttSession != nil && d.mqttConnected.Load() {
		if _, err := d.mqttSession.Publish(ctx, &paho.Publish{
			Topic:   topic,
			QoS:     qos,
			Payload: payload,
		}); err == nil {
			return
		}
	}
	id, err := edgecontrol.NewReplicationUUIDv7(d.now().UTC())
	if err != nil {
		return
	}
	_, _ = d.spool.Admit(SpoolItem{
		ID:      id,
		Topic:   topic,
		QoS:     qos,
		Class:   class,
		Payload: payload,
	})
}

func valueToTelemetryJSON(v edgecontrol.Value) (any, bool) {
	switch v.Type {
	case edgecontrol.DataTypeBoolean:
		return v.Boolean, true
	case edgecontrol.DataTypeString:
		return v.String, true
	case edgecontrol.DataTypeInteger:
		return v.Integer, true
	case edgecontrol.DataTypeLong:
		return v.Long, true
	case edgecontrol.DataTypeFloat:
		return float64(v.Float), true
	case edgecontrol.DataTypeDouble:
		return v.Double, true
	default:
		return nil, false
	}
}
