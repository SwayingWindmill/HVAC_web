package edgedaemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
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
	TraceID       string                 `json:"traceId,omitempty"`
	Payload       mqttUplinkEventPayload `json:"payload"`
}

type mqttUplinkEventPayload struct {
	EventType  string          `json:"eventType"`
	SourceType string          `json:"sourceType"`
	SourceID   string          `json:"sourceId"`
	EventTime  int64           `json:"eventTime"`
	Severity   string          `json:"severity"`
	Data       json.RawMessage `json:"data,omitempty"`
}

var telemetryResendOrder = []edgecontrol.DataPriority{
	edgecontrol.PriorityVeryHigh,
	edgecontrol.PriorityHigh,
	edgecontrol.PriorityMedium,
	edgecontrol.PriorityLow,
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
	cfg                 Config
	obs                 *observability.Runtime
	logger              *slog.Logger
	host                *edgecontrol.Host
	registry            *ProtocolBridgeRegistry
	interlock           *edgecontrol.HydraulicInterlockController
	fallback            *edgecontrol.AutonomousFallbackController
	ruleEngine          *edgecontrol.EdgeRuleController
	aggregator          *edgecontrol.WindowAggregator
	scheduleController  *edgecontrol.EnergyScheduleController
	clusterController   *edgecontrol.ChillerPlantClusterController
	modelManager        *EdgeModelManager
	waveformRing        *WaveformRingBuffer
	remoteDiag          *RemoteDiagnosticsCollector
	lastMetricsSent     time.Time
	timedata            *edgecontrol.FileTimedata
	deadband            *edgecontrol.DeadbandFilter
	spool               *GradedSpool
	shadow              *ShadowManager
	commands            *CommandAdapter
	doExecutor          *DOSequenceExecutor
	concentratorRPC     *ConcentratorRPCExecutor
	fieldPlant          *FieldPlantRunner
	lifecycleCtx        context.Context
	lifecycleCancel     context.CancelFunc
	ota                 *OTAAgent
	tunnelClient        *TunnelClient
	healthSrv           *http.Server
	healthAddr          string
	mqttSession         *mqttclient.SharedSession
	onOTASwitch         func()
	cmdSem              chan struct{}
	cmdWG               sync.WaitGroup
	sequence            atomic.Uint64
	mqttConnected       atomic.Bool
	mqttHealthyGen      atomic.Uint64
	mqttNotifyCh        chan struct{}
	connectedSessionGen atomic.Uint64
	ready               atomic.Bool
	now                 func() time.Time
	sessionMu           sync.Mutex
	sessionID           string
	sessionStartedAt    time.Time
	otaMu               sync.Mutex
	mu                  sync.Mutex
	closed              bool
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
	aggPolicy := edgecontrol.DefaultEdgeAggregationPolicy()
	if cfg.AggregationPolicy != nil {
		aggPolicy = *cfg.AggregationPolicy
	}
	aggregator, err := edgecontrol.NewWindowAggregator(aggPolicy)
	if err != nil {
		_ = registry.Close()
		return nil, fmt.Errorf("create window aggregator: %w", err)
	}
	scheduleController := edgecontrol.NewEnergyScheduleController("")
	if cfg.EnergySchedule != nil {
		if err := scheduleController.UpdateSchedule(cfg.EnergySchedule); err != nil {
			_ = registry.Close()
			return nil, fmt.Errorf("apply bootstrap energy schedule: %w", err)
		}
	}
	clusterPolicy := edgecontrol.DefaultClusterControlPolicy()
	if cfg.ClusterPolicy != nil {
		clusterPolicy = *cfg.ClusterPolicy
	}
	clusterController, err := edgecontrol.NewChillerPlantClusterController(edgecontrol.ClusterControllerConfig{
		Policy: clusterPolicy,
	})
	if err != nil {
		_ = registry.Close()
		return nil, fmt.Errorf("create chiller plant cluster controller: %w", err)
	}
	fallback.SetScheduleActiveFn(scheduleController.IsActiveAt)
	clusterController.SetScheduleOverrideFn(scheduleController.ActiveOverrideAt)

	intentController, err := edgecontrol.NewIntentController("cloud-command", host.IntentStore())
	if err != nil {
		_ = registry.Close()
		return nil, fmt.Errorf("create intent controller: %w", err)
	}
	if err := host.Start([]edgecontrol.ControllerBinding{
		{Priority: 10, Critical: true, Controller: interlock},
		{Priority: 20, Controller: fallback},
		{Priority: 30, Controller: ruleEngine},
		{Priority: 40, Controller: intentController},
		{Priority: 50, Controller: scheduleController},
		{Priority: 60, Controller: clusterController},
	}); err != nil {
		_ = registry.Close()
		return nil, fmt.Errorf("start edgecontrol scheduler: %w", err)
	}

	spool, err := OpenGradedSpool(filepath.Join(cfg.StateDir, "spool"), cfg.SpoolMaxBytes)
	if err != nil {
		_ = registry.Close()
		return nil, fmt.Errorf("open graded spool: %w", err)
	}

	timedata, err := edgecontrol.OpenFileTimedata(filepath.Join(cfg.StateDir, "timedata"), edgecontrol.PriorityLow)
	if err != nil {
		_ = registry.Close()
		return nil, fmt.Errorf("open timedata store: %w", err)
	}
	if err := host.AttachTimedata(timedata); err != nil {
		_ = registry.Close()
		return nil, fmt.Errorf("attach timedata store: %w", err)
	}

	var deadband *edgecontrol.DeadbandFilter
	if cfg.TelemetryDeadband > 0 {
		deadband = edgecontrol.NewDeadbandFilter(
			cfg.TelemetryDeadband,
			time.Duration(cfg.TelemetryDeadbandMaxSilenceSec)*time.Second,
		)
	}

	nowFn := opts.Now
	if nowFn == nil {
		nowFn = time.Now
	}

	modelManager := NewEdgeModelManager(cfg.OTA.TrustedPublicKeys, logger)
	waveformRing := NewWaveformRingBuffer(defaultWaveformRingHorizon)
	remoteDiag := NewRemoteDiagnosticsCollector(cfg.StateDir, nowFn())
	shadow := NewShadowManager(cfg.GatewayID, cfg.StateDir, host, registry, interlock, fallback, ruleEngine, logger)
	shadow.AttachPlantControllers(aggregator, scheduleController, clusterController)
	shadow.AttachModelManager(modelManager)
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
		_ = commands.Close()
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
			_ = commands.Close()
			_ = registry.Close()
			return nil, fmt.Errorf("create tunnel client: %w", err)
		}
	}

	d := &Daemon{
		cfg:                cfg,
		obs:                obs,
		logger:             logger,
		host:               host,
		registry:           registry,
		interlock:          interlock,
		fallback:           fallback,
		ruleEngine:         ruleEngine,
		aggregator:         aggregator,
		scheduleController: scheduleController,
		clusterController:  clusterController,
		modelManager:       modelManager,
		waveformRing:       waveformRing,
		remoteDiag:         remoteDiag,
		lastMetricsSent:    nowFn().UTC(),
		timedata:           timedata,
		deadband:           deadband,
		spool:              spool,
		shadow:             shadow,
		commands:           commands,
		ota:                ota,
		tunnelClient:       tunnelClient,
		onOTASwitch:        opts.OnOTASwitchBoot,
		mqttNotifyCh:       make(chan struct{}, 1),
		cmdSem:             make(chan struct{}, 64),
		now:                nowFn,
	}

	lifecycleCtx, lifecycleCancel := context.WithCancel(context.Background())
	d.lifecycleCtx = lifecycleCtx
	d.lifecycleCancel = lifecycleCancel

	if cfg.FieldPlant != nil && cfg.FieldPlant.Enabled {
		plantCfg := cloneFieldPlantConfig(*cfg.FieldPlant)
		plantCfg.ApplyDefaults()
		rtuBridge, bridgeErr := registry.SharedRTUBridge(
			plantCfg.SerialPort,
			time.Duration(plantCfg.RequestTimeoutMS)*time.Millisecond,
			plantCfg.Retries,
		)
		if bridgeErr != nil {
			lifecycleCancel()
			_ = commands.Close()
			_ = registry.Close()
			return nil, fmt.Errorf("create field plant RTU bridge: %w", bridgeErr)
		}
		plantRunner, plantErr := NewFieldPlantRunner(FieldPlantOptions{
			GatewayID: cfg.GatewayID,
			StateDir:  cfg.StateDir,
			Config:    plantCfg,
			Bus:       ModbusFieldPlantBus{Bridge: rtuBridge},
			PublishTelemetry: func(pubCtx context.Context, payload []byte) {
				d.publishOrSpool(pubCtx, "hvac/v1/"+cfg.GatewayID+"/up/telemetry", 1, EvidenceTelemetryMedium, payload)
			},
			Now:    nowFn,
			Logger: logger,
		})
		if plantErr != nil {
			lifecycleCancel()
			_ = commands.Close()
			_ = registry.Close()
			return nil, fmt.Errorf("init field plant runner: %w", plantErr)
		}
		d.fieldPlant = plantRunner
		shadow.AttachFieldPlant(plantRunner)

		if plantCfg.IsConcentratorRPCEnabled() {
			concRPC, concErr := NewConcentratorRPCExecutor(ConcentratorExecutorOptions{
				GatewayID:     cfg.GatewayID,
				Bus:           ModbusConcentratorBus{Bridge: rtuBridge},
				SettleMS:      plantCfg.ConcentratorRPC.SettleMS,
				ProtectedUIDs: plantCfg.ProtectedRS485UnitIDs(),
				Now:           nowFn,
			})
			if concErr != nil {
				lifecycleCancel()
				_ = commands.Close()
				_ = registry.Close()
				return nil, fmt.Errorf("init concentrator RPC executor: %w", concErr)
			}
			d.concentratorRPC = concRPC
		}
		if plantCfg.IsDOControlEnabled() {
			doExec, doErr := NewDOSequenceExecutor(DOSequenceExecutorOptions{
				GatewayID:          cfg.GatewayID,
				StateDir:           cfg.StateDir,
				Config:             *plantCfg.DOControl,
				CoilBus:            ModbusDOCoilBus{Bridge: rtuBridge},
				ChillerPowerReader: plantRunner.ReadDevicePowerKW,
				PublishTelemetry: func(pubCtx context.Context, payload []byte) {
					d.publishOrSpool(pubCtx, "hvac/v1/"+cfg.GatewayID+"/up/telemetry", 1, EvidenceTelemetryHigh, payload)
				},
				PublishEvent: func(pubCtx context.Context, payload []byte) {
					d.publishOrSpool(pubCtx, "hvac/v1/"+cfg.GatewayID+"/up/event", 1, EvidenceAlarm, payload)
				},
				Now:             nowFn,
				Logger:          logger,
				SequenceContext: lifecycleCtx,
			})
			if doErr != nil {
				lifecycleCancel()
				_ = commands.Close()
				_ = registry.Close()
				return nil, fmt.Errorf("init DO sequence executor: %w", doErr)
			}
			d.doExecutor = doExec
		}
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

// Timedata returns the attached FileTimedata store.
func (d *Daemon) Timedata() *edgecontrol.FileTimedata {
	return d.timedata
}

// CommandAdapter returns the managed MQTT CommandAdapter.
func (d *Daemon) CommandAdapter() *CommandAdapter {
	return d.commands
}

// Interlock returns the Daemon's HydraulicInterlockController.
func (d *Daemon) Interlock() *edgecontrol.HydraulicInterlockController {
	return d.interlock
}

// Aggregator returns the Daemon's DATA-005 edge time-window aggregator.
func (d *Daemon) Aggregator() *edgecontrol.WindowAggregator {
	return d.aggregator
}

// ScheduleController returns the Daemon's CTRL-003 versioned energy schedule controller.
func (d *Daemon) ScheduleController() *edgecontrol.EnergyScheduleController {
	return d.scheduleController
}

// ClusterController returns the Daemon's CTRL-002 multi-chiller plant cluster controller.
func (d *Daemon) ClusterController() *edgecontrol.ChillerPlantClusterController {
	return d.clusterController
}

// ModelManager returns the Daemon's edge AI model & hyperparam governance manager.
func (d *Daemon) ModelManager() *EdgeModelManager {
	return d.modelManager
}

// WaveformRing returns the Daemon's pre-deadband 1Hz waveform capture ring buffer.
func (d *Daemon) WaveformRing() *WaveformRingBuffer {
	return d.waveformRing
}

// RemoteDiagnostics returns the Daemon's bounded remote diagnostics and host metrics collector.
func (d *Daemon) RemoteDiagnostics() *RemoteDiagnosticsCollector {
	return d.remoteDiag
}

// HandleDiagnosticsDownlink executes validated TRIGGER_WAVEFORM or PULL_LOG_BUNDLE commands.
func (d *Daemon) HandleDiagnosticsDownlink(ctx context.Context, payload []byte) error {
	_, cmd, err := edgecontrol.DecodeDiagnosticsDownlink(payload, d.cfg.GatewayID)
	if err != nil {
		return err
	}
	now := d.now().UTC()
	switch cmd.Action {
	case edgecontrol.DiagnosticsActionTriggerWaveform:
		immediate, burst := d.waveformRing.TriggerManual(cmd, now)
		if immediate && burst != nil {
			if wire, encErr := edgecontrol.EncodeWaveformBurst(d.cfg.GatewayID, *burst, now); encErr == nil {
				d.publishOrSpool(ctx, edgecontrol.WaveformUplinkTopic(d.cfg.GatewayID), 1, EvidenceDiagnostic, wire)
			}
		}
	case edgecontrol.DiagnosticsActionPullLogBundle:
		reply := d.remoteDiag.PullRedactedLogs(cmd, now)
		if wire, encErr := edgecontrol.EncodeDiagnosticsLogReply(d.cfg.GatewayID, reply, now); encErr == nil {
			d.publishOrSpool(ctx, edgecontrol.DiagnosticsUplinkTopic(d.cfg.GatewayID), 1, EvidenceDiagnostic, wire)
		}
	}
	return nil
}

// PublishHostMetricsOnce samples host, RS485 bus, and MQTT link metrics and publishes them.
func (d *Daemon) PublishHostMetricsOnce(ctx context.Context) error {
	if d == nil || d.remoteDiag == nil {
		return nil
	}
	now := d.now().UTC()
	d.lastMetricsSent = now
	hostMetrics := d.remoteDiag.SampleHostMetrics(now, d.waveformRing)
	wire, err := edgecontrol.EncodeHostMetrics(d.cfg.GatewayID, hostMetrics, now)
	if err != nil {
		return err
	}
	d.publishOrSpool(ctx, edgecontrol.MetricsUplinkTopic(d.cfg.GatewayID), 1, EvidenceDiagnostic, wire)
	return nil
}

func (d *Daemon) evaluateWaveformAndMetrics(ctx context.Context, img edgecontrol.ProcessImage, now time.Time) {
	if d.waveformRing != nil {
		bursts := d.waveformRing.ObserveSnapshots(now, img)
		for _, burst := range bursts {
			if wire, err := edgecontrol.EncodeWaveformBurst(d.cfg.GatewayID, burst, now); err == nil {
				d.publishOrSpool(ctx, edgecontrol.WaveformUplinkTopic(d.cfg.GatewayID), 1, EvidenceDiagnostic, wire)
			}
		}
	}
	if d.remoteDiag != nil {
		snaps := img.Channels()
		var errCount int64
		for _, snap := range snaps {
			if !snap.HasValue {
				errCount++
			}
		}
		d.remoteDiag.RecordCycleBusStats(int64(len(snaps)), errCount)
	}
}

// HandleCommandPayload validates a v2.0 cloud command envelope, registers its ControlIntent,
// waits for the next cycle outcome, and returns the serialized CommandReplyWire payload.
func (d *Daemon) HandleCommandPayload(ctx context.Context, payload []byte) ([]byte, bool) {
	if d == nil {
		return nil, false
	}
	if d.doExecutor != nil {
		if env, ok := decodeStrictCommandEnvelope(payload); ok && d.doExecutor.HandlesCommand(env) {
			return d.doExecutor.HandlePayload(ctx, payload)
		}
	}
	if d.commands == nil {
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
	connected := d.mqttConnected.Load()
	d.fallback.SetCloudConnected(connected, now)
	if d.scheduleController != nil {
		d.scheduleController.SetCloudConnected(connected, now)
	}
	res, err := d.host.RunCycle(ctx, now)
	if err != nil {
		return res, err
	}
	if d.commands != nil {
		d.commands.ResolveCycle(now, res.Cycle, res.WriteResults)
	}
	if d.modelManager != nil {
		d.modelManager.EvaluateCycle(now, res.Cycle.Image)
	}
	d.evaluateWaveformAndMetrics(ctx, res.Cycle.Image, now)
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
	_ = systemdNotifyReady()

	if d.doExecutor != nil {
		if initErr := d.doExecutor.Initialize(ctx); initErr != nil && ctx.Err() == nil {
			d.logger.Warn("edge_do_executor_init_failed", "error", initErr.Error())
		}
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		d.runCycleLoop(ctx)
	}()

	if d.mqttSession != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d.runMQTTMaintenanceLoop(ctx)
		}()
	}

	if d.fieldPlant != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = d.fieldPlant.Run(ctx)
		}()
	}

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
		if d.cfg.OTA.Enabled && d.ota != nil {
			wg.Add(1)
			go func() {
				defer wg.Done()
				d.runOTAPollLoop(ctx)
			}()
		}
	}

	if d.tunnelClient != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = d.tunnelClient.Run(ctx)
		}()
	}

	<-ctx.Done()
	_ = systemdNotifyStopping()
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
		if d.mqttConnected.Load() {
			sid, _ := d.currentSession()
			if sid != "" {
				if wire, err := d.buildGatewayLifecycleEventPayload("GATEWAY_DISCONNECTED", sid, d.now().UTC(), map[string]any{"clean": true}); err == nil {
					pubCtx, pubCancel := context.WithTimeout(context.Background(), 2*time.Second)
					_, _ = d.mqttSession.Publish(pubCtx, &paho.Publish{
						Topic:   "hvac/v1/" + d.cfg.GatewayID + "/up/event",
						QoS:     1,
						Payload: wire,
					})
					pubCancel()
				}
			}
		}
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = d.mqttSession.Close(shutCtx)
		cancel()
	}
	if d.lifecycleCancel != nil {
		d.lifecycleCancel()
	}
	if d.doExecutor != nil {
		d.doExecutor.Close()
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
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !d.ready.Load() {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
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
			_ = systemdNotifyWatchdog()
			if d.now().UTC().Sub(d.lastMetricsSent) >= 30*time.Second {
				_ = d.PublishHostMetricsOnce(ctx)
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

func (d *Daemon) runOTAPollLoop(ctx context.Context) {
	intervalSec := d.cfg.OTA.PollIntervalSec
	if intervalSec <= 0 {
		intervalSec = defaultOTAPollIntervalSec
	}
	ticker := time.NewTicker(time.Duration(intervalSec) * time.Second)
	defer ticker.Stop()
	pollOnce := func() {
		stage, err := d.ota.PollDesired(ctx)
		if err != nil {
			if ctx.Err() == nil {
				d.logger.Warn("edge_ota_poll_failed", "error", err.Error())
			}
			return
		}
		if stage != nil {
			d.handleOTAStage(ctx, *stage)
		}
	}
	pollOnce()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pollOnce()
		}
	}
}

func (d *Daemon) handleOTAStage(ctx context.Context, stage edgecontrol.OTAStagePayload) {
	if d == nil || d.ota == nil || !d.cfg.OTA.Enabled {
		return
	}
	d.otaMu.Lock()
	defer d.otaMu.Unlock()

	if state, err := d.ota.State(); err == nil &&
		state.Status == edgecontrol.OTAStageHealthChecking &&
		state.PendingStage != nil &&
		state.PendingStage.TaskID == stage.TaskID {
		return
	}

	var switchPending bool
	if err := d.ota.ApplyStage(ctx, stage, func(res edgecontrol.OTAResultPayload) {
		if res.Stage == edgecontrol.OTAStageHealthChecking {
			switchPending = true
		}
		wire, encErr := edgecontrol.EncodeOTAResult(d.cfg.GatewayID, res, d.now().UTC())
		if encErr == nil {
			d.publishOrSpool(ctx, edgecontrol.OTAUplinkTopic(d.cfg.GatewayID), 1, EvidenceOTAResult, wire)
		}
	}); err != nil && ctx.Err() == nil {
		d.logger.Warn("edge_ota_apply_failed", "task_id", stage.TaskID, "version", stage.Version, "error", err.Error())
	}

	if switchPending {
		if d.doExecutor != nil {
			if d.doExecutor.HasActiveSequences() {
				waitCtx, waitCancel := context.WithTimeout(ctx, 2*time.Second)
				_ = d.doExecutor.WaitForActiveSequences(waitCtx)
				waitCancel()
			}
			d.doExecutor.Close()
		}
		if d.onOTASwitch != nil {
			d.onOTASwitch()
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
	eventTopic := "hvac/v1/" + d.cfg.GatewayID + "/up/event"
	initSid, initAt := d.rotateSession(d.now().UTC())
	initWillPayload, err := d.buildGatewayLifecycleEventPayload("LWT", initSid, initAt, map[string]any{"reason": "broker_connection_lost"})
	if err != nil {
		return err
	}
	willMsg := &paho.WillMessage{
		Retain:  false,
		QoS:     1,
		Topic:   eventTopic,
		Payload: initWillPayload,
	}

	session, err := mqttclient.NewSharedSession(ctx, mqttclient.SharedSessionConfig{
		BrokerURL:   d.cfg.MQTTBrokerURL,
		ClientID:    "edge-gateway-" + d.cfg.GatewayID,
		TLS:         tlsCfg,
		WillMessage: willMsg,
		ConnectPacketBuilder: func(cp *paho.Connect, _ *url.URL) (*paho.Connect, error) {
			sid, sidAt := d.rotateSession(d.now().UTC())
			willPayload, buildErr := d.buildGatewayLifecycleEventPayload("LWT", sid, sidAt, map[string]any{"reason": "broker_connection_lost"})
			if buildErr != nil {
				return nil, buildErr
			}
			cp.WillMessage = &paho.WillMessage{
				Retain:  false,
				QoS:     1,
				Topic:   eventTopic,
				Payload: willPayload,
			}
			return cp, nil
		},
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
		d.handleOTAStage(ctx, stage)
	}); err != nil {
		return err
	}
	if d.concentratorRPC != nil {
		if _, err := session.RegisterRoute("edge-concentrator-downlink", []string{ConcentratorDownlinkTopic(d.cfg.GatewayID)}, func(pkt *paho.Publish) {
			topic := pkt.Topic
			payload := append([]byte(nil), pkt.Payload...)
			select {
			case d.cmdSem <- struct{}{}:
				d.cmdWG.Add(1)
				go func() {
					defer func() {
						<-d.cmdSem
						d.cmdWG.Done()
					}()
					rpcCtx, cancel := context.WithTimeout(ctx, 18*time.Second)
					defer cancel()
					if replyBytes, ok := d.concentratorRPC.HandlePayload(rpcCtx, topic, payload); ok && len(replyBytes) > 0 {
						d.publishOrSpool(ctx, ConcentratorUplinkTopic(d.cfg.GatewayID), 1, EvidenceControl, replyBytes)
					}
				}()
			default:
				d.logger.Warn("edge_concentrator_rpc_queue_full")
			}
		}); err != nil {
			return err
		}
	}
	if _, err := session.RegisterRoute("edge-diagnostics-downlink", []string{edgecontrol.DiagnosticsDownlinkTopic(d.cfg.GatewayID)}, func(pkt *paho.Publish) {
		payload := append([]byte(nil), pkt.Payload...)
		if err := d.HandleDiagnosticsDownlink(ctx, payload); err != nil {
			d.logger.Warn("edge_diagnostics_downlink_invalid", "error", err.Error())
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
		if connected && state.Generation > 0 {
			d.mqttHealthyGen.Store(state.Generation)
		}
		d.mqttConnected.Store(connected)
		if connected {
			d.notifyMQTTMaintenance()
		}
	})
	d.mqttSession = session
	return nil
}

func (d *Daemon) notifyMQTTMaintenance() {
	if d == nil || d.mqttNotifyCh == nil {
		return
	}
	select {
	case d.mqttNotifyCh <- struct{}{}:
	default:
	}
}

func (d *Daemon) runMQTTMaintenanceLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	d.maintainMQTTOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.mqttNotifyCh:
			d.maintainMQTTOnce(ctx)
		case <-ticker.C:
			d.maintainMQTTOnce(ctx)
		}
	}
}

func (d *Daemon) maintainMQTTOnce(ctx context.Context) {
	if d == nil || d.mqttSession == nil || !d.mqttConnected.Load() {
		return
	}
	d.maintainMQTTWithPublisher(ctx, func(pubCtx context.Context, msg *paho.Publish) error {
		_, err := d.mqttSession.Publish(pubCtx, msg)
		return err
	})
}

func (d *Daemon) maintainMQTTWithPublisher(ctx context.Context, publish func(context.Context, *paho.Publish) error) {
	if d == nil || publish == nil || !d.mqttConnected.Load() {
		return
	}
	if gen := d.mqttHealthyGen.Load(); gen > 0 && d.connectedSessionGen.Load() != gen {
		sid, sidAt := d.currentSession()
		if sid != "" {
			if wire, encErr := d.buildGatewayLifecycleEventPayload("GATEWAY_CONNECTED", sid, sidAt, nil); encErr == nil {
				pubCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
				pubErr := publish(pubCtx, &paho.Publish{
					Topic:   "hvac/v1/" + d.cfg.GatewayID + "/up/event",
					QoS:     1,
					Payload: wire,
				})
				cancel()
				if pubErr == nil {
					d.connectedSessionGen.Store(gen)
				} else if ctx.Err() == nil {
					d.logger.Warn("edge_gateway_connected_publish_failed", "error", pubErr.Error())
				}
			}
		}
	}
	_, drainErr := d.spool.Drain(ctx, 200, func(itemCtx context.Context, item SpoolItem) error {
		payload := markTelemetryEnvelopeReplay(item.Topic, item.Payload)
		pubCtx, cancel := context.WithTimeout(itemCtx, 10*time.Second)
		defer cancel()
		return publish(pubCtx, &paho.Publish{
			Topic:   item.Topic,
			QoS:     item.QoS,
			Payload: payload,
		})
	})
	if drainErr != nil && ctx.Err() == nil {
		d.logger.Warn("edge_spool_drain_failed", "error", drainErr.Error())
	}
}

func (d *Daemon) emitCycleTelemetry(ctx context.Context, img edgecontrol.ProcessImage, now time.Time) {
	snaps := img.Channels()
	if len(snaps) == 0 && d.aggregator == nil {
		return
	}
	byTier := make(map[edgecontrol.DataPriority]map[string][]mqttUplinkPoint)
	var aggOutputs []edgecontrol.AggregatedWindowOutput
	for _, snap := range snaps {
		if !snap.HasValue {
			continue
		}
		observedAt := now.UTC()
		if !snap.Sample.ObservedAt.IsZero() {
			observedAt = snap.Sample.ObservedAt.UTC()
		}
		if d.aggregator != nil {
			passRaw, flushed := d.aggregator.Observe(edgecontrol.AggregationSample{
				DeviceKey:  snap.Descriptor.ComponentID,
				PointCode:  snap.Descriptor.ChannelID,
				Address:    snap.Descriptor.Address(),
				Descriptor: snap.Descriptor,
				Value:      snap.Sample.Value,
				Quality:    snap.Sample.Quality,
				ObservedAt: observedAt,
			})
			aggOutputs = append(aggOutputs, flushed...)
			if !passRaw {
				continue
			}
		}
		if d.deadband != nil {
			sample := edgecontrol.DeadbandSample{
				Key:        snap.Descriptor.Address(),
				PointType:  pointTypeForDescriptor(snap.Descriptor),
				ValueType:  valueTypeForDescriptor(snap.Descriptor),
				Value:      snap.Sample.Value,
				Quality:    snap.Sample.Quality,
				ObservedAt: observedAt,
			}
			if !d.deadband.Allow(sample) {
				continue
			}
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
		tier := normalizeTelemetryPriority(snap.Descriptor)
		devMap := byTier[tier]
		if devMap == nil {
			devMap = make(map[string][]mqttUplinkPoint)
			byTier[tier] = devMap
		}
		devMap[snap.Descriptor.ComponentID] = append(devMap[snap.Descriptor.ComponentID], mqttUplinkPoint{
			Code:    snap.Descriptor.ChannelID,
			Value:   val,
			Quality: quality,
			Unit:    unitPtr,
		})
	}
	if d.aggregator != nil {
		aggOutputs = append(aggOutputs, d.aggregator.FlushDue(now)...)
	}
	appendAggregatedTelemetryPoints(byTier, aggOutputs)
	if len(byTier) == 0 {
		return
	}

	tsMs := max(now.UnixMilli(), 1)
	tsStr := strconv.FormatInt(tsMs, 10)
	topic := "hvac/v1/" + d.cfg.GatewayID + "/up/telemetry"

	for _, tier := range telemetryResendOrder {
		byDevice := byTier[tier]
		if len(byDevice) == 0 {
			continue
		}
		devKeys := make([]string, 0, len(byDevice))
		for k := range byDevice {
			devKeys = append(devKeys, k)
		}
		sort.Strings(devKeys)

		keyParts := make([]string, 0, 3+len(devKeys)*4)
		keyParts = append(keyParts, d.cfg.GatewayID, string(tier), tsStr)
		devices := make([]mqttUplinkDevice, 0, len(devKeys))
		for _, k := range devKeys {
			pts := byDevice[k]
			sort.Slice(pts, func(i, j int) bool {
				return pts[i].Code < pts[j].Code
			})
			devices = append(devices, mqttUplinkDevice{
				DeviceID:        k,
				DeviceTimestamp: tsMs,
				Points:          pts,
			})
			keyParts = append(keyParts, k)
			for _, pt := range pts {
				keyParts = append(keyParts, pt.Code)
			}
		}

		msgID := edgecontrol.DeterministicReplicationUUIDv7(now, keyParts...)
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
			continue
		}
		d.publishOrSpool(ctx, topic, 1, telemetryEvidenceClass(tier), wire)
	}
}

func appendAggregatedTelemetryPoints(byTier map[edgecontrol.DataPriority]map[string][]mqttUplinkPoint, outputs []edgecontrol.AggregatedWindowOutput) {
	for _, out := range outputs {
		tier := out.Priority
		if tier == "" {
			tier = edgecontrol.PriorityMedium
		}
		devMap := byTier[tier]
		if devMap == nil {
			devMap = make(map[string][]mqttUplinkPoint)
			byTier[tier] = devMap
		}
		var unitPtr *string
		if strings.TrimSpace(out.Unit) != "" {
			u := out.Unit
			unitPtr = &u
		}
		devMap[out.DeviceKey] = append(devMap[out.DeviceKey], mqttUplinkPoint{
			Code:    out.PointCode,
			Value:   out.Mean,
			Quality: 0,
			Unit:    unitPtr,
		})
		if out.EmitMinMax {
			devMap[out.DeviceKey] = append(devMap[out.DeviceKey],
				mqttUplinkPoint{Code: out.PointCode + "_min", Value: out.Min, Quality: 0, Unit: unitPtr},
				mqttUplinkPoint{Code: out.PointCode + "_max", Value: out.Max, Quality: 0, Unit: unitPtr},
			)
		}
	}
}

func (d *Daemon) emitRuleEvents(ctx context.Context, now time.Time) {
	events := d.ruleEngine.DrainEvents()
	for _, ev := range events {
		occurredAt := now.UTC()
		if parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(ev.OccurredAt)); err == nil && !parsed.IsZero() {
			occurredAt = parsed.UTC()
		}
		rawData, err := json.Marshal(ev)
		if err != nil {
			continue
		}
		msgID := edgecontrol.DeterministicReplicationUUIDv7(occurredAt, d.cfg.GatewayID, ev.EventID, ev.RuleID, ev.EventType)
		seq := d.sequence.Add(1)
		severity := strings.ToUpper(strings.TrimSpace(ev.Severity))
		if ev.EventType == edgecontrol.EdgeRuleEventCleared || severity == "" {
			severity = "INFO"
		} else if severity != "INFO" && severity != "WARNING" && severity != "CRITICAL" {
			severity = "WARNING"
		}
		tsMs := max(occurredAt.UnixMilli(), 1)
		wire, err := json.Marshal(mqttUplinkEventEnvelope{
			SchemaVersion: "2.0",
			MessageID:     msgID,
			GatewayID:     d.cfg.GatewayID,
			Timestamp:     tsMs,
			Sequence:      seq,
			Payload: mqttUplinkEventPayload{
				EventType:  ev.EventType,
				SourceType: "EDGE_RULE",
				SourceID:   ev.RuleID,
				EventTime:  tsMs,
				Severity:   severity,
				Data:       rawData,
			},
		})
		if err != nil {
			continue
		}
		d.publishOrSpool(ctx, "hvac/v1/"+d.cfg.GatewayID+"/up/event", 1, EvidenceAlarm, wire)
	}
}

func (d *Daemon) rotateSession(now time.Time) (string, time.Time) {
	ts := now.UTC()
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	sid, err := edgecontrol.NewReplicationUUIDv7(ts)
	if err != nil {
		sid = edgecontrol.DeterministicReplicationUUIDv7(ts, d.cfg.GatewayID, "session")
	}
	d.sessionMu.Lock()
	d.sessionID = sid
	d.sessionStartedAt = ts
	d.sessionMu.Unlock()
	return sid, ts
}

func (d *Daemon) currentSession() (string, time.Time) {
	d.sessionMu.Lock()
	defer d.sessionMu.Unlock()
	return d.sessionID, d.sessionStartedAt
}

func (d *Daemon) buildGatewayLifecycleEventPayload(
	eventType string,
	sessionID string,
	observedAt time.Time,
	extra map[string]any,
) ([]byte, error) {
	ts := observedAt.UTC()
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	fields := make(map[string]any, len(extra)+1)
	for k, v := range extra {
		fields[k] = v
	}
	if strings.TrimSpace(sessionID) != "" {
		fields["sessionId"] = strings.TrimSpace(sessionID)
	}
	rawData, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	msgID, err := edgecontrol.NewReplicationUUIDv7(ts)
	if err != nil {
		return nil, err
	}
	severity := "INFO"
	if strings.EqualFold(strings.TrimSpace(eventType), "LWT") {
		severity = "WARNING"
	}
	tsMs := max(ts.UnixMilli(), 1)
	return json.Marshal(mqttUplinkEventEnvelope{
		SchemaVersion: "2.0",
		MessageID:     msgID,
		GatewayID:     d.cfg.GatewayID,
		Timestamp:     tsMs,
		Sequence:      d.sequence.Add(1),
		Payload: mqttUplinkEventPayload{
			EventType:  eventType,
			SourceType: "GATEWAY",
			SourceID:   d.cfg.GatewayID,
			EventTime:  tsMs,
			Severity:   severity,
			Data:       rawData,
		},
	})
}

func markTelemetryEnvelopeReplay(topic string, payload []byte) []byte {
	if !strings.HasSuffix(topic, "/up/telemetry") {
		return payload
	}
	var env mqttUplinkTelemetryEnvelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return payload
	}
	if env.Replay {
		return payload
	}
	env.Replay = true
	rewritten, err := json.Marshal(env)
	if err != nil {
		return payload
	}
	return rewritten
}

func normalizeTelemetryPriority(desc edgecontrol.ChannelDescriptor) edgecontrol.DataPriority {
	for _, candidate := range []edgecontrol.DataPriority{desc.ResendPriority, desc.PollPriority} {
		switch candidate {
		case edgecontrol.PriorityVeryHigh, edgecontrol.PriorityHigh, edgecontrol.PriorityMedium, edgecontrol.PriorityLow:
			return candidate
		}
	}
	return edgecontrol.PriorityMedium
}

func telemetryEvidenceClass(priority edgecontrol.DataPriority) EvidenceClass {
	switch priority {
	case edgecontrol.PriorityVeryHigh:
		return EvidenceTelemetryVeryHigh
	case edgecontrol.PriorityHigh:
		return EvidenceTelemetryHigh
	case edgecontrol.PriorityLow:
		return EvidenceTelemetryLow
	default:
		return EvidenceTelemetryMedium
	}
}

func pointTypeForDescriptor(desc edgecontrol.ChannelDescriptor) string {
	switch desc.DataType {
	case edgecontrol.DataTypeBoolean, edgecontrol.DataTypeString:
		return "STATE"
	default:
		return "TELEMETRY"
	}
}

func valueTypeForDescriptor(desc edgecontrol.ChannelDescriptor) string {
	switch desc.DataType {
	case edgecontrol.DataTypeBoolean:
		return "BOOLEAN"
	case edgecontrol.DataTypeString:
		return "STRING"
	default:
		return "NUMBER"
	}
}

func (d *Daemon) publishOrSpool(ctx context.Context, topic string, qos byte, class EvidenceClass, payload []byte) {
	if d.mqttSession != nil && d.mqttConnected.Load() {
		pubCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, err := d.mqttSession.Publish(pubCtx, &paho.Publish{
			Topic:   topic,
			QoS:     qos,
			Payload: payload,
		})
		cancel()
		if err == nil {
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
