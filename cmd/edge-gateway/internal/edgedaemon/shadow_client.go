package edgedaemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

const (
	maxShadowHTTPBodyBytes = 4 << 20

	shadowStatusApplied  = "APPLIED"
	shadowStatusPartial  = "PARTIAL"
	shadowStatusRejected = "REJECTED"

	shadowDriftSectionDriverConfigs     = "DRIVER_CONFIGS"
	shadowDriftSectionAutonomyPolicy    = "AUTONOMY_POLICY"
	shadowDriftSectionEdgeRules         = "EDGE_RULES"
	shadowDriftSectionAggregationPolicy = "AGGREGATION_POLICY"
	shadowDriftSectionClusterPolicy     = "CLUSTER_POLICY"
	shadowDriftSectionEnergySchedule    = "ENERGY_SCHEDULE"
	shadowDriftSectionEdgeModels        = "EDGE_MODELS"
	shadowDriftReasonApplyFailed        = "EDGE_APPLY_FAILED"
)

// PointCopyTarget mirrors connectivity config shadow copy targets.
type PointCopyTarget struct {
	DeviceKey string `json:"deviceKey"`
	PointCode string `json:"pointCode"`
}

// GatewayPointMapping maps a gateway source channel to a canonical device point.
type GatewayPointMapping struct {
	SourceChannel    string            `json:"sourceChannel"`
	PrimaryDeviceKey string            `json:"primaryDeviceKey"`
	PrimaryPointCode string            `json:"primaryPointCode"`
	CopyTargets      []PointCopyTarget `json:"copyTargets,omitempty"`
}

// GatewaySamplingPolicy configures poll/publish intervals for a device or point.
type GatewaySamplingPolicy struct {
	DeviceKey         string `json:"deviceKey"`
	PointCode         string `json:"pointCode,omitempty"`
	SampleIntervalMS  int    `json:"sampleIntervalMs"`
	PublishIntervalMS int    `json:"publishIntervalMs"`
}

// ConcentratorShadowParams mirrors serial/LoRa concentrator parameters.
type ConcentratorShadowParams struct {
	ModbusID        int `json:"modbusId"`
	BaudRateCode    int `json:"baudRateCode"`
	OffLineTime     int `json:"offLineTime"`
	DataUplinkCycle int `json:"dataUplinkCycle"`
	LocalNet        int `json:"localNet"`
	TimeMark        int `json:"timeMark"`
	RfTxPower       int `json:"rfTxPower"`
	RfFrequency     int `json:"rfFrequency"`
	RfBandWidth     int `json:"rfBandWidth"`
	RfSf            int `json:"rfSf"`
	RfLeadCode      int `json:"rfLeadCode"`
	TxTimeout       int `json:"txTimeout"`
	RxTimeout       int `json:"rxTimeout"`
	RssiOut         int `json:"rssiOut"`
}

// ConcentratorShadowEIDSlot assigns one EID slot on an RS-485 concentrator.
type ConcentratorShadowEIDSlot struct {
	Slot int    `json:"slot"`
	EID  uint32 `json:"eid"`
}

// ConcentratorUnitShadow describes one concentrator's params and EID table.
type ConcentratorUnitShadow struct {
	Params *ConcentratorShadowParams   `json:"params,omitempty"`
	EIDs   []ConcentratorShadowEIDSlot `json:"eids,omitempty"`
}

// ShadowDriftDetail reports section-level shadow rejection/drift facts.
type ShadowDriftDetail struct {
	Section string `json:"section"`
	Target  string `json:"target"`
	Reason  string `json:"reason"`
	Detail  string `json:"detail"`
}

// GatewayDesiredConfig matches GET /gateway/v1/config-shadow desired payload.
type GatewayDesiredConfig struct {
	DesiredRevision     int64                              `json:"desiredRevision"`
	PointMappings       []GatewayPointMapping              `json:"pointMappings"`
	SamplingPolicies    []GatewaySamplingPolicy            `json:"samplingPolicies"`
	ConcentratorConfigs map[string]ConcentratorUnitShadow  `json:"concentratorConfigs"`
	DriverConfigs       []edgecontrol.ModbusDriverInput    `json:"driverConfigs,omitempty"`
	AutonomyPolicy      *edgecontrol.AutonomyPolicyConfig  `json:"autonomyPolicy,omitempty"`
	EdgeRuleBundle      *edgecontrol.EdgeRuleBundle        `json:"edgeRuleBundle,omitempty"`
	AggregationPolicy   *edgecontrol.EdgeAggregationPolicy `json:"aggregationPolicy,omitempty"`
	ClusterPolicy       *edgecontrol.ClusterControlPolicy  `json:"clusterPolicy,omitempty"`
	EnergySchedule      *edgecontrol.EdgeEnergySchedule    `json:"energySchedule,omitempty"`
	EdgeModels          []edgecontrol.EdgeModelBundle      `json:"edgeModels,omitempty"`
	UpdatedBy           string                             `json:"updatedBy,omitempty"`
	Reason              string                             `json:"reason,omitempty"`
	CreatedAt           *time.Time                         `json:"createdAt,omitempty"`
	UpdatedAt           *time.Time                         `json:"updatedAt,omitempty"`
}

// GatewayShadowPullResponse matches GET /gateway/v1/config-shadow response.
type GatewayShadowPullResponse struct {
	GatewayID       string                `json:"gatewayId"`
	DesiredRevision int64                 `json:"desiredRevision"`
	HasDelta        bool                  `json:"hasDelta"`
	Desired         *GatewayDesiredConfig `json:"desired,omitempty"`
}

// GatewayShadowReportRequest matches POST /gateway/v1/config-shadow/report body.
type GatewayShadowReportRequest struct {
	ReportedRevision    int64                                `json:"reportedRevision"`
	AppliedStatus       string                               `json:"appliedStatus"`
	PointMappings       []GatewayPointMapping                `json:"pointMappings"`
	SamplingPolicies    []GatewaySamplingPolicy              `json:"samplingPolicies"`
	ConcentratorConfigs map[string]ConcentratorUnitShadow    `json:"concentratorConfigs"`
	DriverConfigs       []edgecontrol.ModbusDriverInput      `json:"driverConfigs,omitempty"`
	AutonomyPolicy      *edgecontrol.AutonomyPolicyConfig    `json:"autonomyPolicy,omitempty"`
	EdgeRuleBundle      *edgecontrol.EdgeRuleBundle          `json:"edgeRuleBundle,omitempty"`
	AggregationPolicy   *edgecontrol.EdgeAggregationPolicy   `json:"aggregationPolicy,omitempty"`
	ClusterPolicy       *edgecontrol.ClusterControlPolicy    `json:"clusterPolicy,omitempty"`
	EnergySchedule      *edgecontrol.EdgeEnergySchedule      `json:"energySchedule,omitempty"`
	EdgeModels          []edgecontrol.EdgeModelBundle        `json:"edgeModels,omitempty"`
	EdgeModelStatuses   []edgecontrol.EdgeModelRuntimeStatus `json:"edgeModelStatuses,omitempty"`
	DriftDetails        []ShadowDriftDetail                  `json:"driftDetails,omitempty"`
	ErrorMessage        *string                              `json:"errorMessage,omitempty"`
	ReportedAt          *time.Time                           `json:"reportedAt,omitempty"`
}

type persistedShadowState struct {
	AppliedRevision     int64                              `json:"appliedRevision"`
	PointMappings       []GatewayPointMapping              `json:"pointMappings"`
	SamplingPolicies    []GatewaySamplingPolicy            `json:"samplingPolicies"`
	ConcentratorConfigs map[string]ConcentratorUnitShadow  `json:"concentratorConfigs"`
	DriverConfigs       []edgecontrol.ModbusDriverInput    `json:"driverConfigs,omitempty"`
	AutonomyPolicy      *edgecontrol.AutonomyPolicyConfig  `json:"autonomyPolicy,omitempty"`
	EdgeRuleBundle      *edgecontrol.EdgeRuleBundle        `json:"edgeRuleBundle,omitempty"`
	AggregationPolicy   *edgecontrol.EdgeAggregationPolicy `json:"aggregationPolicy,omitempty"`
	ClusterPolicy       *edgecontrol.ClusterControlPolicy  `json:"clusterPolicy,omitempty"`
	EnergySchedule      *edgecontrol.EdgeEnergySchedule    `json:"energySchedule,omitempty"`
	EdgeModels          []edgecontrol.EdgeModelBundle      `json:"edgeModels,omitempty"`
}

// ShadowManager maintains persistent desired/reported edge configuration and
// hot-reloads drivers, autonomy bounds, edge rules, aggregation policies,
// cluster policies, and energy schedules on the running Host.
type ShadowManager struct {
	gatewayID    string
	statePath    string
	host         *edgecontrol.Host
	registry     *ProtocolBridgeRegistry
	interlock    *edgecontrol.HydraulicInterlockController
	fallback     *edgecontrol.AutonomousFallbackController
	ruleEngine   *edgecontrol.EdgeRuleController
	aggregator   *edgecontrol.WindowAggregator
	scheduleCtrl *edgecontrol.EnergyScheduleController
	clusterCtrl  *edgecontrol.ChillerPlantClusterController
	modelManager *EdgeModelManager
	fieldPlant   *FieldPlantRunner
	logger       *slog.Logger
	now          func() time.Time

	mu    sync.Mutex
	state persistedShadowState
}

// NewShadowManager constructs a ShadowManager bound to the live Host and controllers.
func NewShadowManager(
	gatewayID string,
	stateDir string,
	host *edgecontrol.Host,
	registry *ProtocolBridgeRegistry,
	interlock *edgecontrol.HydraulicInterlockController,
	fallback *edgecontrol.AutonomousFallbackController,
	ruleEngine *edgecontrol.EdgeRuleController,
	logger *slog.Logger,
) *ShadowManager {
	if logger == nil {
		logger = slog.Default()
	}
	return &ShadowManager{
		gatewayID:  strings.TrimSpace(gatewayID),
		statePath:  filepath.Join(strings.TrimSpace(stateDir), "config-shadow.json"),
		host:       host,
		registry:   registry,
		interlock:  interlock,
		fallback:   fallback,
		ruleEngine: ruleEngine,
		logger:     logger,
		now:        time.Now,
		state: persistedShadowState{
			PointMappings:       []GatewayPointMapping{},
			SamplingPolicies:    []GatewaySamplingPolicy{},
			ConcentratorConfigs: map[string]ConcentratorUnitShadow{},
			DriverConfigs:       []edgecontrol.ModbusDriverInput{},
		},
	}
}

// AttachPlantControllers attaches DATA-005 pre-aggregation, CTRL-003 energy schedule,
// and CTRL-002 multi-chiller cluster controllers onto ShadowManager without altering
// NewShadowManager's signature for existing callers.
func (m *ShadowManager) AttachPlantControllers(
	agg *edgecontrol.WindowAggregator,
	sched *edgecontrol.EnergyScheduleController,
	cluster *edgecontrol.ChillerPlantClusterController,
) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.aggregator = agg
	m.scheduleCtrl = sched
	m.clusterCtrl = cluster
}

// AttachModelManager attaches signed edge model & hyperparam governance onto ShadowManager.
func (m *ShadowManager) AttachModelManager(mgr *EdgeModelManager) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.modelManager = mgr
}

// AttachFieldPlant attaches the declarative field plant runner so config-shadow PointMappings hot-reload live.
func (m *ShadowManager) AttachFieldPlant(runner *FieldPlantRunner) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.fieldPlant = runner
	mappings := append([]GatewayPointMapping(nil), m.state.PointMappings...)
	m.mu.Unlock()
	if runner != nil && len(mappings) > 0 {
		runner.UpdatePointMappings(mappings)
	}
}

// LoadOrBootstrap recovers persisted config shadow state from disk and applies it,
// or seeds initial static driver/autonomy/rule configs when no shadow file exists yet.
func (m *ShadowManager) LoadOrBootstrap(cfg Config) error {
	raw, err := os.ReadFile(m.statePath)
	if err == nil {
		var saved persistedShadowState
		if err := json.Unmarshal(raw, &saved); err != nil {
			return fmt.Errorf("decode persisted config shadow %s: %w", m.statePath, err)
		}
		m.mu.Lock()
		m.state = normalizePersistedShadowState(saved)
		snapshot := clonePersistedShadowState(m.state)
		m.mu.Unlock()
		return m.applyPersistedBootstrap(snapshot)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read config shadow %s: %w", m.statePath, err)
	}

	seed := persistedShadowState{
		PointMappings:       []GatewayPointMapping{},
		SamplingPolicies:    []GatewaySamplingPolicy{},
		ConcentratorConfigs: map[string]ConcentratorUnitShadow{},
		DriverConfigs:       cloneDriverInputs(cfg.Drivers),
		AutonomyPolicy:      cloneAutonomyPtr(cfg.AutonomyPolicy),
		EdgeRuleBundle:      cfg.EdgeRuleBundle.Clone(),
		AggregationPolicy:   cfg.AggregationPolicy.Clone(),
		ClusterPolicy:       cfg.ClusterPolicy.Clone(),
		EnergySchedule:      cfg.EnergySchedule.Clone(),
	}
	m.mu.Lock()
	m.state = clonePersistedShadowState(seed)
	m.mu.Unlock()
	return m.applyPersistedBootstrap(seed)
}

func (m *ShadowManager) applyPersistedBootstrap(state persistedShadowState) error {
	for _, drv := range state.DriverConfigs {
		adapter, err := m.registry.BuildAdapter(drv)
		if err != nil {
			return fmt.Errorf("bootstrap driver %q: %w", drv.DeviceKey, err)
		}
		if err := m.host.ReplaceOrRegisterDevice(adapter); err != nil {
			return fmt.Errorf("register bootstrap driver %q: %w", drv.DeviceKey, err)
		}
	}
	if state.AutonomyPolicy != nil {
		if err := m.applyAutonomyPolicy(state.AutonomyPolicy); err != nil {
			return err
		}
	}
	if state.EdgeRuleBundle != nil && m.ruleEngine != nil {
		if err := m.ruleEngine.UpdateBundle(state.EdgeRuleBundle.Clone()); err != nil {
			return err
		}
	}
	if state.AggregationPolicy != nil {
		if err := m.applyAggregationPolicy(state.AggregationPolicy); err != nil {
			return err
		}
	}
	if state.ClusterPolicy != nil {
		if err := m.applyClusterPolicy(state.ClusterPolicy); err != nil {
			return err
		}
	}
	if state.EnergySchedule != nil {
		if err := m.applyEnergySchedule(state.EnergySchedule); err != nil {
			return err
		}
	}
	if len(state.EdgeModels) > 0 && m.modelManager != nil {
		if _, _, err := m.modelManager.ApplyBundles(state.EdgeModels); err != nil {
			return err
		}
	}
	m.syncRuleProtectedAddresses(state.EdgeRuleBundle)
	m.refreshControllerBindings()
	m.mu.Lock()
	fp := m.fieldPlant
	m.mu.Unlock()
	if fp != nil {
		fp.UpdatePointMappings(state.PointMappings)
	}
	return nil
}

// AppliedRevision returns the latest committed shadow revision on the edge.
func (m *ShadowManager) AppliedRevision() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state.AppliedRevision
}

// PointMappingsSnapshot returns a copy of current desired/applied point mappings.
func (m *ShadowManager) PointMappingsSnapshot() []GatewayPointMapping {
	m.mu.Lock()
	defer m.mu.Unlock()
	return cloneGatewayPointMappings(m.state.PointMappings)
}

// PullAndSync pulls desired shadow changes from /gateway/v1/config-shadow and reports back.
func (m *ShadowManager) PullAndSync(ctx context.Context, cfg Config) error {
	client, id, err := newMTLSHTTPClient(cfg.CAFile, cfg.CertFile, cfg.KeyFile, cfg.ServerName, 20*time.Second)
	if err != nil {
		return err
	}
	now := m.now().UTC()
	sinceRev := m.AppliedRevision()
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.PlatformBaseURL), "/")

	pullURL := baseURL + "/gateway/v1/config-shadow?sinceRevision=" + strconv.FormatInt(sinceRev, 10)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pullURL, nil)
	if err != nil {
		return err
	}
	if err := signHTTPPoPIfPresent(req, id, now); err != nil {
		return fmt.Errorf("sign config-shadow pull PoP: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("pull config shadow: %w", err)
	}
	pullBytes, readErr := io.ReadAll(io.LimitReader(resp.Body, maxShadowHTTPBodyBytes+1))
	_ = resp.Body.Close()
	if readErr != nil {
		return fmt.Errorf("read config shadow response: %w", readErr)
	}
	if int64(len(pullBytes)) > maxShadowHTTPBodyBytes {
		return errors.New("config shadow pull response exceeds maximum size")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("pull config shadow failed with HTTP %d", resp.StatusCode)
	}

	var pull GatewayShadowPullResponse
	if err := json.Unmarshal(pullBytes, &pull); err != nil {
		return fmt.Errorf("decode config shadow pull response: %w", err)
	}
	if !pull.HasDelta || pull.Desired == nil {
		return nil
	}

	report, stale, err := m.ApplyDesired(*pull.Desired)
	if err != nil || stale {
		return err
	}
	reportBytes, err := json.Marshal(report)
	if err != nil {
		return err
	}
	reportURL := baseURL + "/gateway/v1/config-shadow/report"
	postReq, err := http.NewRequestWithContext(ctx, http.MethodPost, reportURL, bytes.NewReader(reportBytes))
	if err != nil {
		return err
	}
	postReq.Header.Set("Content-Type", "application/json")
	if err := signHTTPPoPIfPresent(postReq, id, now); err != nil {
		return fmt.Errorf("sign config-shadow report PoP: %w", err)
	}
	postResp, err := client.Do(postReq)
	if err != nil {
		return fmt.Errorf("report config shadow: %w", err)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(postResp.Body, maxShadowHTTPBodyBytes))
	_ = postResp.Body.Close()
	if postResp.StatusCode < 200 || postResp.StatusCode >= 300 {
		return fmt.Errorf("report config shadow failed with HTTP %d", postResp.StatusCode)
	}
	return nil
}

// HandleMQTTChangeBatch decodes a CHANGE_BATCH replication message, applies any valid
// desired payload, and returns the encoded CHANGE_ACK envelope.
func (m *ShadowManager) HandleMQTTChangeBatch(payload []byte) ([]byte, error) {
	_, batch, err := edgecontrol.DecodeShadowChangeBatch(payload, m.gatewayID)
	if err != nil {
		return nil, err
	}
	last := batch.Items[len(batch.Items)-1]
	ack := edgecontrol.ShadowChangeAckPayload{
		Cursor:        last.Cursor,
		DeliveryID:    last.DeliveryID,
		PayloadDigest: last.PayloadDigest,
		Status:        edgecontrol.ShadowAckStatusAcked,
	}

	for _, item := range batch.Items {
		if !item.Tombstone && !edgecontrol.VerifyShadowPayloadDigest(item.Payload, item.PayloadDigest) {
			ack.Status = edgecontrol.ShadowAckStatusQuarantined
			ack.Reason = "payload checksum mismatch"
			return edgecontrol.EncodeShadowChangeAck(m.gatewayID, ack, m.now().UTC())
		}
		if item.Tombstone || len(bytes.TrimSpace(item.Payload)) == 0 {
			continue
		}
		var desired GatewayDesiredConfig
		if err := json.Unmarshal(item.Payload, &desired); err != nil {
			ack.Status = edgecontrol.ShadowAckStatusQuarantined
			ack.Reason = "invalid shadow payload JSON"
			return edgecontrol.EncodeShadowChangeAck(m.gatewayID, ack, m.now().UTC())
		}
		if desired.DesiredRevision < 1 {
			desired.DesiredRevision = item.SourceRevision
		}
		report, _, applyErr := m.ApplyDesired(desired)
		if applyErr != nil || report.AppliedStatus == shadowStatusRejected {
			ack.Status = edgecontrol.ShadowAckStatusQuarantined
			if report.ErrorMessage != nil {
				ack.Reason = *report.ErrorMessage
			} else if applyErr != nil {
				ack.Reason = applyErr.Error()
			} else {
				ack.Reason = "shadow desired config rejected by edge"
			}
			return edgecontrol.EncodeShadowChangeAck(m.gatewayID, ack, m.now().UTC())
		}
		rev := item.SourceRevision
		ack.AppliedOwnerRevision = &rev
	}
	return edgecontrol.EncodeShadowChangeAck(m.gatewayID, ack, m.now().UTC())
}

// ApplyDesired atomically applies drivers, autonomy policy, and edge rules onto the runtime.
func (m *ShadowManager) ApplyDesired(desired GatewayDesiredConfig) (GatewayShadowReportRequest, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if desired.DesiredRevision < m.state.AppliedRevision {
		return GatewayShadowReportRequest{}, true, nil
	}

	var drift []ShadowDriftDetail
	var errMsgs []string

	acceptedDrivers := make([]edgecontrol.ModbusDriverInput, 0, len(desired.DriverConfigs))
	seenKeys := make(map[string]struct{}, len(desired.DriverConfigs))
	for _, cfg := range desired.DriverConfigs {
		key := strings.TrimSpace(cfg.DeviceKey)
		target := clampRunes("driver:"+key, 256)
		if key != "" {
			if _, dup := seenKeys[key]; dup {
				msg := fmt.Sprintf("duplicate driverConfig deviceKey %q", key)
				drift = append(drift, ShadowDriftDetail{Section: shadowDriftSectionDriverConfigs, Target: target, Reason: shadowDriftReasonApplyFailed, Detail: clampRunes(msg, 512)})
				errMsgs = append(errMsgs, msg)
				continue
			}
			seenKeys[key] = struct{}{}
		}
		adapter, buildErr := m.registry.BuildAdapter(cfg)
		if buildErr != nil {
			msg := fmt.Sprintf("driver %s validation/build failed: %v", key, buildErr)
			drift = append(drift, ShadowDriftDetail{Section: shadowDriftSectionDriverConfigs, Target: target, Reason: shadowDriftReasonApplyFailed, Detail: clampRunes(msg, 512)})
			errMsgs = append(errMsgs, msg)
			continue
		}
		if regErr := m.host.ReplaceOrRegisterDevice(adapter); regErr != nil {
			msg := fmt.Sprintf("driver %s registration failed: %v", key, regErr)
			drift = append(drift, ShadowDriftDetail{Section: shadowDriftSectionDriverConfigs, Target: target, Reason: shadowDriftReasonApplyFailed, Detail: clampRunes(msg, 512)})
			errMsgs = append(errMsgs, msg)
			continue
		}
		acceptedDrivers = append(acceptedDrivers, cloneDriverInput(cfg))
	}

	var reportedPolicy *edgecontrol.AutonomyPolicyConfig
	autonomyApplied := false
	if desired.AutonomyPolicy != nil {
		if err := m.applyAutonomyPolicy(desired.AutonomyPolicy); err != nil {
			msg := fmt.Sprintf("autonomyPolicy apply failed: %v", err)
			drift = append(drift, ShadowDriftDetail{Section: shadowDriftSectionAutonomyPolicy, Target: "autonomyPolicy", Reason: shadowDriftReasonApplyFailed, Detail: clampRunes(msg, 512)})
			errMsgs = append(errMsgs, msg)
		} else {
			reportedPolicy = cloneAutonomyPtr(desired.AutonomyPolicy)
			autonomyApplied = true
		}
	}

	var reportedRules *edgecontrol.EdgeRuleBundle
	rulesApplied := false
	if desired.EdgeRuleBundle != nil {
		if m.ruleEngine == nil {
			msg := "edgeRuleBundle engine unavailable"
			drift = append(drift, ShadowDriftDetail{Section: shadowDriftSectionEdgeRules, Target: "edgeRuleBundle", Reason: shadowDriftReasonApplyFailed, Detail: msg})
			errMsgs = append(errMsgs, msg)
		} else if err := m.ruleEngine.UpdateBundle(desired.EdgeRuleBundle.Clone()); err != nil {
			msg := fmt.Sprintf("edgeRuleBundle apply failed: %v", err)
			drift = append(drift, ShadowDriftDetail{Section: shadowDriftSectionEdgeRules, Target: "edgeRuleBundle", Reason: shadowDriftReasonApplyFailed, Detail: clampRunes(msg, 512)})
			errMsgs = append(errMsgs, msg)
		} else {
			reportedRules = desired.EdgeRuleBundle.Clone()
			rulesApplied = true
		}
	}

	var reportedAgg *edgecontrol.EdgeAggregationPolicy
	aggApplied := false
	if desired.AggregationPolicy != nil {
		if err := m.applyAggregationPolicy(desired.AggregationPolicy); err != nil {
			msg := fmt.Sprintf("aggregationPolicy apply failed: %v", err)
			drift = append(drift, ShadowDriftDetail{Section: shadowDriftSectionAggregationPolicy, Target: "aggregationPolicy", Reason: shadowDriftReasonApplyFailed, Detail: clampRunes(msg, 512)})
			errMsgs = append(errMsgs, msg)
		} else {
			reportedAgg = desired.AggregationPolicy.Clone()
			aggApplied = true
		}
	}

	var reportedCluster *edgecontrol.ClusterControlPolicy
	clusterApplied := false
	if desired.ClusterPolicy != nil {
		if err := m.applyClusterPolicy(desired.ClusterPolicy); err != nil {
			msg := fmt.Sprintf("clusterPolicy apply failed: %v", err)
			drift = append(drift, ShadowDriftDetail{Section: shadowDriftSectionClusterPolicy, Target: "clusterPolicy", Reason: shadowDriftReasonApplyFailed, Detail: clampRunes(msg, 512)})
			errMsgs = append(errMsgs, msg)
		} else {
			reportedCluster = desired.ClusterPolicy.Clone()
			clusterApplied = true
		}
	}

	var reportedSchedule *edgecontrol.EdgeEnergySchedule
	scheduleApplied := false
	if desired.EnergySchedule != nil {
		if err := m.applyEnergySchedule(desired.EnergySchedule); err != nil {
			msg := fmt.Sprintf("energySchedule apply failed: %v", err)
			drift = append(drift, ShadowDriftDetail{Section: shadowDriftSectionEnergySchedule, Target: "energySchedule", Reason: shadowDriftReasonApplyFailed, Detail: clampRunes(msg, 512)})
			errMsgs = append(errMsgs, msg)
		} else {
			reportedSchedule = desired.EnergySchedule.Clone()
			scheduleApplied = true
		}
	}

	var reportedModels []edgecontrol.EdgeModelBundle
	modelsApplied := false
	if len(desired.EdgeModels) > 0 {
		if m.modelManager == nil {
			msg := "edgeModels manager unavailable"
			drift = append(drift, ShadowDriftDetail{Section: shadowDriftSectionEdgeModels, Target: "edgeModels", Reason: shadowDriftReasonApplyFailed, Detail: msg})
			errMsgs = append(errMsgs, msg)
		} else {
			appliedBundles, modelDrifts, applyErr := m.modelManager.ApplyBundles(desired.EdgeModels)
			if applyErr != nil {
				drift = append(drift, modelDrifts...)
				errMsgs = append(errMsgs, applyErr.Error())
			} else {
				reportedModels = appliedBundles
				modelsApplied = true
			}
		}
	}

	status := shadowStatusApplied
	if len(drift) > 0 || len(errMsgs) > 0 {
		requested := len(desired.DriverConfigs)
		succeeded := len(acceptedDrivers)
		for _, pair := range []struct {
			present bool
			ok      bool
		}{
			{desired.AutonomyPolicy != nil, autonomyApplied},
			{desired.EdgeRuleBundle != nil, rulesApplied},
			{desired.AggregationPolicy != nil, aggApplied},
			{desired.ClusterPolicy != nil, clusterApplied},
			{desired.EnergySchedule != nil, scheduleApplied},
			{len(desired.EdgeModels) > 0, modelsApplied},
		} {
			if pair.present {
				requested++
			}
			if pair.ok {
				succeeded++
			}
		}
		if requested > 0 && succeeded == 0 && len(desired.PointMappings) == 0 && len(desired.SamplingPolicies) == 0 && len(desired.ConcentratorConfigs) == 0 {
			status = shadowStatusRejected
		} else {
			status = shadowStatusPartial
		}
	}

	now := m.now().UTC()
	report := GatewayShadowReportRequest{
		ReportedRevision:    desired.DesiredRevision,
		AppliedStatus:       status,
		PointMappings:       cloneGatewayPointMappings(desired.PointMappings),
		SamplingPolicies:    cloneGatewaySamplingPolicies(desired.SamplingPolicies),
		ConcentratorConfigs: cloneConcentratorMap(desired.ConcentratorConfigs),
		DriverConfigs:       cloneDriverInputs(acceptedDrivers),
		AutonomyPolicy:      cloneAutonomyPtr(reportedPolicy),
		EdgeRuleBundle:      reportedRules.Clone(),
		AggregationPolicy:   reportedAgg.Clone(),
		ClusterPolicy:       reportedCluster.Clone(),
		EnergySchedule:      reportedSchedule.Clone(),
		EdgeModels:          cloneEdgeModelSlice(reportedModels),
		EdgeModelStatuses:   m.modelManager.Statuses(),
		DriftDetails:        drift,
		ReportedAt:          &now,
	}
	if len(errMsgs) > 0 {
		joined := clampRunes(strings.Join(errMsgs, "; "), 1024)
		report.ErrorMessage = &joined
	}

	if status == shadowStatusRejected {
		report.DriverConfigs = cloneDriverInputs(m.state.DriverConfigs)
		report.AutonomyPolicy = cloneAutonomyPtr(m.state.AutonomyPolicy)
		report.EdgeRuleBundle = m.state.EdgeRuleBundle.Clone()
		report.AggregationPolicy = m.state.AggregationPolicy.Clone()
		report.ClusterPolicy = m.state.ClusterPolicy.Clone()
		report.EnergySchedule = m.state.EnergySchedule.Clone()
		report.EdgeModels = cloneEdgeModelSlice(m.state.EdgeModels)
		report.EdgeModelStatuses = m.modelManager.Statuses()
		return report, false, nil
	}

	// Unregister drivers omitted in acceptedDesired
	acceptedSet := make(map[string]struct{}, len(acceptedDrivers))
	for _, d := range acceptedDrivers {
		acceptedSet[strings.TrimSpace(d.DeviceKey)] = struct{}{}
	}
	for _, prev := range m.state.DriverConfigs {
		prevKey := strings.TrimSpace(prev.DeviceKey)
		if prevKey == "" {
			continue
		}
		if _, keep := acceptedSet[prevKey]; !keep {
			_ = m.host.UnregisterDevice(prevKey)
		}
	}
	if desired.AutonomyPolicy == nil {
		def := edgecontrol.DefaultAutonomyPolicyConfig()
		_ = m.applyAutonomyPolicy(&def)
	}
	if desired.EdgeRuleBundle == nil && m.ruleEngine != nil {
		_ = m.ruleEngine.UpdateBundle(nil)
	}
	if desired.AggregationPolicy == nil && m.aggregator != nil {
		_ = m.aggregator.UpdatePolicy(edgecontrol.DefaultEdgeAggregationPolicy())
	}
	if desired.ClusterPolicy == nil && m.clusterCtrl != nil {
		_ = m.clusterCtrl.UpdatePolicy(edgecontrol.DefaultClusterControlPolicy())
	}
	if desired.EnergySchedule == nil && m.scheduleCtrl != nil {
		_ = m.scheduleCtrl.UpdateSchedule(nil)
	}
	if len(desired.EdgeModels) == 0 && m.modelManager != nil {
		_, _, _ = m.modelManager.ApplyBundles(nil)
	}
	activeRules := m.state.EdgeRuleBundle
	if rulesApplied {
		activeRules = reportedRules
	} else if desired.EdgeRuleBundle == nil {
		activeRules = nil
	}
	m.syncRuleProtectedAddresses(activeRules)
	m.refreshControllerBindings()

	m.state = normalizePersistedShadowState(persistedShadowState{
		AppliedRevision:     desired.DesiredRevision,
		PointMappings:       desired.PointMappings,
		SamplingPolicies:    desired.SamplingPolicies,
		ConcentratorConfigs: desired.ConcentratorConfigs,
		DriverConfigs:       acceptedDrivers,
		AutonomyPolicy:      reportedPolicy,
		EdgeRuleBundle:      reportedRules,
		AggregationPolicy:   reportedAgg,
		ClusterPolicy:       reportedCluster,
		EnergySchedule:      reportedSchedule,
		EdgeModels:          cloneEdgeModelSlice(reportedModels),
	})
	if err := m.persistLocked(); err != nil {
		return GatewayShadowReportRequest{}, false, err
	}
	if m.fieldPlant != nil {
		m.fieldPlant.UpdatePointMappings(m.state.PointMappings)
	}
	return report, false, nil
}

func (m *ShadowManager) applyAutonomyPolicy(policy *edgecontrol.AutonomyPolicyConfig) error {
	if policy == nil {
		return nil
	}
	if err := policy.Validate(); err != nil {
		return err
	}
	if m.interlock != nil {
		if err := m.interlock.UpdatePolicy(*policy); err != nil {
			return err
		}
	}
	if m.fallback != nil {
		if err := m.fallback.UpdatePolicy(*policy); err != nil {
			return err
		}
	}
	return nil
}

func (m *ShadowManager) applyAggregationPolicy(policy *edgecontrol.EdgeAggregationPolicy) error {
	if policy == nil {
		return nil
	}
	if err := policy.Validate(); err != nil {
		return err
	}
	if m.aggregator != nil {
		return m.aggregator.UpdatePolicy(*policy)
	}
	return nil
}

func (m *ShadowManager) applyClusterPolicy(policy *edgecontrol.ClusterControlPolicy) error {
	if policy == nil {
		return nil
	}
	if err := policy.Validate(); err != nil {
		return err
	}
	if m.clusterCtrl != nil {
		return m.clusterCtrl.UpdatePolicy(*policy)
	}
	return nil
}

func (m *ShadowManager) applyEnergySchedule(schedule *edgecontrol.EdgeEnergySchedule) error {
	if schedule == nil {
		return nil
	}
	if err := schedule.Validate(); err != nil {
		return err
	}
	if m.scheduleCtrl != nil {
		return m.scheduleCtrl.UpdateSchedule(schedule.Clone())
	}
	return nil
}

func (m *ShadowManager) syncRuleProtectedAddresses(bundle *edgecontrol.EdgeRuleBundle) {
	if m.aggregator == nil {
		return
	}
	m.aggregator.SetProtectedAddresses(edgecontrol.CollectEdgeRuleAddresses(bundle))
}

func (m *ShadowManager) persistLocked() error {
	if err := os.MkdirAll(filepath.Dir(m.statePath), 0o750); err != nil {
		return fmt.Errorf("create shadow state dir: %w", err)
	}
	encoded, err := json.MarshalIndent(m.state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config shadow state: %w", err)
	}
	return writeFileAtomic(m.statePath, append(encoded, '\n'), 0o600)
}

func normalizePersistedShadowState(in persistedShadowState) persistedShadowState {
	out := persistedShadowState{
		AppliedRevision:     in.AppliedRevision,
		PointMappings:       cloneGatewayPointMappings(in.PointMappings),
		SamplingPolicies:    cloneGatewaySamplingPolicies(in.SamplingPolicies),
		ConcentratorConfigs: cloneConcentratorMap(in.ConcentratorConfigs),
		DriverConfigs:       cloneDriverInputs(in.DriverConfigs),
		AutonomyPolicy:      cloneAutonomyPtr(in.AutonomyPolicy),
		EdgeRuleBundle:      in.EdgeRuleBundle.Clone(),
		AggregationPolicy:   in.AggregationPolicy.Clone(),
		ClusterPolicy:       in.ClusterPolicy.Clone(),
		EnergySchedule:      in.EnergySchedule.Clone(),
		EdgeModels:          cloneEdgeModelSlice(in.EdgeModels),
	}
	return out
}

func clonePersistedShadowState(in persistedShadowState) persistedShadowState {
	return normalizePersistedShadowState(in)
}

func cloneDriverInput(in edgecontrol.ModbusDriverInput) edgecontrol.ModbusDriverInput {
	return edgecontrol.CloneSouthboundDriverInput(in)
}

func cloneDriverInputs(src []edgecontrol.ModbusDriverInput) []edgecontrol.ModbusDriverInput {
	if len(src) == 0 {
		return []edgecontrol.ModbusDriverInput{}
	}
	out := make([]edgecontrol.ModbusDriverInput, len(src))
	for i, d := range src {
		out[i] = cloneDriverInput(d)
	}
	return out
}

func cloneAutonomyPtr(src *edgecontrol.AutonomyPolicyConfig) *edgecontrol.AutonomyPolicyConfig {
	if src == nil {
		return nil
	}
	cp := *src
	return &cp
}

func cloneGatewayPointMappings(src []GatewayPointMapping) []GatewayPointMapping {
	if len(src) == 0 {
		return []GatewayPointMapping{}
	}
	out := make([]GatewayPointMapping, len(src))
	for i, m := range src {
		out[i] = m
		if len(m.CopyTargets) > 0 {
			out[i].CopyTargets = append([]PointCopyTarget(nil), m.CopyTargets...)
		}
	}
	return out
}

func cloneGatewaySamplingPolicies(src []GatewaySamplingPolicy) []GatewaySamplingPolicy {
	if len(src) == 0 {
		return []GatewaySamplingPolicy{}
	}
	out := make([]GatewaySamplingPolicy, len(src))
	copy(out, src)
	return out
}

func cloneConcentratorMap(src map[string]ConcentratorUnitShadow) map[string]ConcentratorUnitShadow {
	if len(src) == 0 {
		return map[string]ConcentratorUnitShadow{}
	}
	out := make(map[string]ConcentratorUnitShadow, len(src))
	for k, v := range src {
		cp := ConcentratorUnitShadow{}
		if v.Params != nil {
			p := *v.Params
			cp.Params = &p
		}
		if len(v.EIDs) > 0 {
			cp.EIDs = append([]ConcentratorShadowEIDSlot(nil), v.EIDs...)
		}
		out[k] = cp
	}
	return out
}

func clampRunes(value string, maxRunes int) string {
	trimmed := strings.TrimSpace(value)
	if maxRunes <= 0 || utf8.RuneCountInString(trimmed) <= maxRunes {
		return trimmed
	}
	count := 0
	for idx := range trimmed {
		if count == maxRunes {
			return trimmed[:idx]
		}
		count++
	}
	return trimmed
}

func hasCapabilityProfile(profiles []edgecontrol.CapabilityProfileID, target edgecontrol.CapabilityProfileID) bool {
	for _, p := range profiles {
		if p == target {
			return true
		}
	}
	return false
}

func (m *ShadowManager) refreshControllerBindings() {
	if m == nil || m.host == nil {
		return
	}
	manifest, err := m.host.Manifest(m.gatewayID, "runtime", m.now().UTC())
	if err != nil {
		return
	}

	var chillers []edgecontrol.ChillerInterlockBinding
	var pumps []edgecontrol.PumpInterlockBinding
	var pumpFreqAddrs []string
	var fallbackChillers []edgecontrol.ChillerFallbackTarget
	var fallbackPumps []edgecontrol.PumpFallbackTarget
	var clusterChillers []edgecontrol.ClusterChillerBinding
	var clusterPumps []edgecontrol.ClusterPumpBinding
	var clusterTowers []edgecontrol.ClusterTowerBinding
	var plantLoadAddr string
	channelSemantics := make(map[string]edgecontrol.SemanticChannel)

	for _, comp := range manifest.Components {
		if !comp.Enabled {
			continue
		}
		for sem, addr := range comp.ChannelBindings {
			if trimmed := strings.TrimSpace(addr); trimmed != "" {
				channelSemantics[trimmed] = sem
			}
		}
		if hasCapabilityProfile(comp.Profiles, edgecontrol.ProfileChiller) {
			spAddr := comp.ChannelBindings[edgecontrol.SemanticTemperatureSetpoint]
			loadLimitAddr := comp.ChannelBindings[edgecontrol.SemanticLoadLimit]
			localRemoteAddr := resolveLocalRemoteModeChannel(comp, manifest.Channels)
			chillers = append(chillers, edgecontrol.ChillerInterlockBinding{
				ComponentID:      comp.ID,
				StartCommand:     comp.ChannelBindings[edgecontrol.SemanticStartCommand],
				StopCommand:      comp.ChannelBindings[edgecontrol.SemanticStopCommand],
				SetpointCommand:  spAddr,
				LoadLimitCommand: loadLimitAddr,
				RunState:         comp.ChannelBindings[edgecontrol.SemanticRunState],
				FaultChannel:     comp.ChannelBindings[edgecontrol.SemanticFaultCode],
				LocalRemoteMode:  localRemoteAddr,
			})
			if spAddr != "" {
				fallbackChillers = append(fallbackChillers, edgecontrol.ChillerFallbackTarget{
					ComponentID:        comp.ID,
					SupplySetpointAddr: spAddr,
					RunStateAddr:       comp.ChannelBindings[edgecontrol.SemanticRunState],
					FaultAddr:          comp.ChannelBindings[edgecontrol.SemanticFaultCode],
				})
			}
			clusterChillers = append(clusterChillers, edgecontrol.ClusterChillerBinding{
				ComponentID:         comp.ID,
				RatedCoolingKW:      parseComponentRatedCoolingKW(comp.Properties),
				StartAddr:           comp.ChannelBindings[edgecontrol.SemanticStartCommand],
				StopAddr:            comp.ChannelBindings[edgecontrol.SemanticStopCommand],
				RunStateAddr:        comp.ChannelBindings[edgecontrol.SemanticRunState],
				FaultAddr:           comp.ChannelBindings[edgecontrol.SemanticFaultCode],
				SetpointAddr:        spAddr,
				LoadLimitAddr:       loadLimitAddr,
				CoolingCapacityAddr: comp.ChannelBindings[edgecontrol.SemanticCoolingCapacity],
				PowerAddr:           comp.ChannelBindings[edgecontrol.SemanticPower],
			})
		}
		if hasCapabilityProfile(comp.Profiles, edgecontrol.ProfileVariableSpeedPump) {
			freqAddr := comp.ChannelBindings[edgecontrol.SemanticFrequencySetpoint]
			runAddr := comp.ChannelBindings[edgecontrol.SemanticRunState]
			flowAddr := comp.ChannelBindings[edgecontrol.SemanticFlow]
			faultAddr := comp.ChannelBindings[edgecontrol.SemanticFaultCode]
			localRemoteAddr := resolveLocalRemoteModeChannel(comp, manifest.Channels)
			pumps = append(pumps, edgecontrol.PumpInterlockBinding{
				ComponentID:      comp.ID,
				StartCommand:     comp.ChannelBindings[edgecontrol.SemanticStartCommand],
				StopCommand:      comp.ChannelBindings[edgecontrol.SemanticStopCommand],
				RunState:         runAddr,
				FlowFeedback:     flowAddr,
				FaultChannel:     faultAddr,
				FrequencyCommand: freqAddr,
				LocalRemoteMode:  localRemoteAddr,
			})
			if freqAddr != "" {
				pumpFreqAddrs = append(pumpFreqAddrs, freqAddr)
				fallbackPumps = append(fallbackPumps, edgecontrol.PumpFallbackTarget{
					ComponentID:   comp.ID,
					FrequencyAddr: freqAddr,
					RunStateAddr:  runAddr,
					FaultAddr:     faultAddr,
				})
			}
			clusterPumps = append(clusterPumps, edgecontrol.ClusterPumpBinding{
				ComponentID:   comp.ID,
				FrequencyAddr: freqAddr,
				RunStateAddr:  runAddr,
				FaultAddr:     faultAddr,
				FlowAddr:      flowAddr,
			})
		}
		if hasCapabilityProfile(comp.Profiles, edgecontrol.ProfileCoolingTower) {
			clusterTowers = append(clusterTowers, edgecontrol.ClusterTowerBinding{
				ComponentID:  comp.ID,
				FanSpeedAddr: comp.ChannelBindings[edgecontrol.SemanticFanSpeedSetpoint],
				RunStateAddr: comp.ChannelBindings[edgecontrol.SemanticRunState],
				FaultAddr:    comp.ChannelBindings[edgecontrol.SemanticFaultCode],
			})
		}
		if plantLoadAddr == "" && hasCapabilityProfile(comp.Profiles, edgecontrol.ProfileThermalEnergyMeter) {
			plantLoadAddr = comp.ChannelBindings[edgecontrol.SemanticCoolingCapacity]
		}
	}

	if m.interlock != nil {
		m.interlock.UpdateBindings(chillers, pumps, pumpFreqAddrs)
	}
	if m.fallback != nil {
		m.fallback.UpdateTargets(fallbackChillers, fallbackPumps)
	}
	if m.scheduleCtrl != nil {
		m.scheduleCtrl.UpdateTargets(fallbackChillers, fallbackPumps, clusterTowers)
	}
	if m.clusterCtrl != nil {
		m.clusterCtrl.UpdateBindings(clusterChillers, clusterPumps, clusterTowers, plantLoadAddr)
	}
	if m.aggregator != nil {
		m.aggregator.SetChannelSemantics(channelSemantics)
	}
}

func resolveLocalRemoteModeChannel(comp edgecontrol.ManifestComponent, channels []edgecontrol.ChannelDescriptor) string {
	if addr := strings.TrimSpace(comp.ChannelBindings[edgecontrol.SemanticLocalRemoteMode]); addr != "" {
		return addr
	}
	for _, chID := range comp.Channels {
		if strings.EqualFold(strings.TrimSpace(chID), "local_remote_mode") {
			return comp.ID + "/" + chID
		}
	}
	for _, ch := range channels {
		if ch.ComponentID != comp.ID {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(ch.ChannelID), "local_remote_mode") ||
			strings.EqualFold(strings.TrimSpace(ch.PointID), "local_remote_mode") ||
			strings.HasSuffix(strings.ToLower(strings.TrimSpace(ch.PointID)), ":local_remote_mode") {
			return ch.Address()
		}
	}
	return ""
}

func parseComponentRatedCoolingKW(props map[string]json.RawMessage) float64 {
	if len(props) == 0 {
		return 0
	}
	raw, ok := props["ratedCoolingKw"]
	if !ok {
		return 0
	}
	var val float64
	if err := json.Unmarshal(raw, &val); err != nil || val <= 0 {
		return 0
	}
	return val
}
