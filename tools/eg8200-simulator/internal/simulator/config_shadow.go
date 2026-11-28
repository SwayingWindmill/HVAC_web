package simulator

import (
	"bytes"
	"context"
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
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
	shadowSyncMaxBodyBytes = 4 << 20

	shadowAppliedStatusApplied  = "APPLIED"
	shadowAppliedStatusPartial  = "PARTIAL"
	shadowAppliedStatusRejected = "REJECTED"

	shadowDriftSectionConcentrator   = "CONCENTRATOR"
	shadowDriftSectionDriverConfigs  = "DRIVER_CONFIGS"
	shadowDriftSectionAutonomyPolicy = "AUTONOMY_POLICY"
	shadowDriftSectionEdgeRules      = "EDGE_RULES"
	shadowDriftReasonApplyFailed     = "EDGE_APPLY_FAILED"
)

type (
	PointCopyTarget           = edgecontrol.PointCopyTarget
	GatewayPointMapping       = edgecontrol.GatewayPointMapping
	ConcentratorShadowParams  = edgecontrol.ConcentratorShadowParams
	ConcentratorShadowEIDSlot = edgecontrol.ConcentratorShadowEIDSlot
	ConcentratorUnitShadow    = edgecontrol.ConcentratorUnitShadow
	ShadowDriftDetail         = edgecontrol.ShadowDriftDetail
)

// GatewaySamplingPolicy extends the shared shadow contract with local simulator deadband filtering.
type GatewaySamplingPolicy struct {
	DeviceKey         string  `json:"deviceKey"`
	PointCode         string  `json:"pointCode,omitempty"`
	SampleIntervalMS  int     `json:"sampleIntervalMs"`
	PublishIntervalMS int     `json:"publishIntervalMs"`
	Deadband          float64 `json:"deadband,omitempty"`
}

type GatewayDesiredConfig struct {
	DesiredRevision     int64                             `json:"desiredRevision"`
	PointMappings       []GatewayPointMapping             `json:"pointMappings"`
	SamplingPolicies    []GatewaySamplingPolicy           `json:"samplingPolicies"`
	ConcentratorConfigs map[string]ConcentratorUnitShadow `json:"concentratorConfigs"`
	DriverConfigs       []GatewayModbusDriverConfig       `json:"driverConfigs,omitempty"`
	AutonomyPolicy      *GatewayAutonomyPolicy            `json:"autonomyPolicy,omitempty"`
	EdgeRuleBundle      *edgecontrol.EdgeRuleBundle       `json:"edgeRuleBundle,omitempty"`
	UpdatedBy           string                            `json:"updatedBy,omitempty"`
	Reason              string                            `json:"reason,omitempty"`
	CreatedAt           *time.Time                        `json:"createdAt,omitempty"`
	UpdatedAt           *time.Time                        `json:"updatedAt,omitempty"`
}

type GatewayShadowPullResponse struct {
	GatewayID       string                `json:"gatewayId"`
	DesiredRevision int64                 `json:"desiredRevision"`
	HasDelta        bool                  `json:"hasDelta"`
	Desired         *GatewayDesiredConfig `json:"desired,omitempty"`
}

type GatewayShadowReportRequest struct {
	ReportedRevision    int64                             `json:"reportedRevision"`
	AppliedStatus       string                            `json:"appliedStatus"`
	PointMappings       []GatewayPointMapping             `json:"pointMappings"`
	SamplingPolicies    []GatewaySamplingPolicy           `json:"samplingPolicies"`
	ConcentratorConfigs map[string]ConcentratorUnitShadow `json:"concentratorConfigs"`
	DriverConfigs       []GatewayModbusDriverConfig       `json:"driverConfigs,omitempty"`
	AutonomyPolicy      *GatewayAutonomyPolicy            `json:"autonomyPolicy,omitempty"`
	EdgeRuleBundle      *edgecontrol.EdgeRuleBundle       `json:"edgeRuleBundle,omitempty"`
	DriftDetails        []ShadowDriftDetail               `json:"driftDetails,omitempty"`
	ErrorMessage        *string                           `json:"errorMessage,omitempty"`
	ReportedAt          *time.Time                        `json:"reportedAt,omitempty"`
}

type persistedEdgeShadowState struct {
	AppliedRevision     int64                             `json:"appliedRevision"`
	PointMappings       []GatewayPointMapping             `json:"pointMappings"`
	SamplingPolicies    []GatewaySamplingPolicy           `json:"samplingPolicies"`
	ConcentratorConfigs map[string]ConcentratorUnitShadow `json:"concentratorConfigs"`
	DriverConfigs       []GatewayModbusDriverConfig       `json:"driverConfigs,omitempty"`
	AutonomyPolicy      *GatewayAutonomyPolicy            `json:"autonomyPolicy,omitempty"`
	EdgeRuleBundle      *edgecontrol.EdgeRuleBundle       `json:"edgeRuleBundle,omitempty"`
}

type edgeConfigShadowApplyHook func(ctx context.Context, desired GatewayDesiredConfig) ([]ShadowDriftDetail, string, error)

type edgeConfigShadow struct {
	syncMu              sync.Mutex
	mu                  sync.Mutex
	statePath           string
	appliedRevision     int64
	pointMappings       []GatewayPointMapping
	samplingPolicies    []GatewaySamplingPolicy
	concentratorConfigs map[string]ConcentratorUnitShadow
	driverConfigs       []GatewayModbusDriverConfig
	autonomyPolicy      *GatewayAutonomyPolicy
	edgeRuleBundle      *edgecontrol.EdgeRuleBundle
	lastPublished       map[string]time.Time
	lastSyncAttempt     time.Time
	applyHook           edgeConfigShadowApplyHook
	applyDriverHook     edgeDriverApplyHook
	removeDriverHook    edgeDriverRemoveHook
	applyAutonomyHook   edgeAutonomyApplyHook
	applyEdgeRulesHook  edgeRulesApplyHook
}

func newEdgeConfigShadow(statePath string) *edgeConfigShadow {
	return &edgeConfigShadow{
		statePath:           strings.TrimSpace(statePath),
		pointMappings:       []GatewayPointMapping{},
		samplingPolicies:    []GatewaySamplingPolicy{},
		concentratorConfigs: map[string]ConcentratorUnitShadow{},
		driverConfigs:       []GatewayModbusDriverConfig{},
		lastPublished:       map[string]time.Time{},
	}
}

func (s *edgeConfigShadow) load() error {
	if s == nil || s.statePath == "" {
		return nil
	}
	raw, err := os.ReadFile(s.statePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read persisted edge config shadow: %w", err)
	}
	var state persistedEdgeShadowState
	if err := json.Unmarshal(raw, &state); err != nil {
		return fmt.Errorf("decode persisted edge config shadow: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appliedRevision = state.AppliedRevision
	s.pointMappings = clonePointMappings(state.PointMappings)
	s.samplingPolicies = cloneSamplingPolicies(state.SamplingPolicies)
	s.concentratorConfigs = cloneConcentratorConfigs(state.ConcentratorConfigs)
	s.driverConfigs = cloneDriverConfigs(state.DriverConfigs)
	s.autonomyPolicy = cloneAutonomyPolicy(state.AutonomyPolicy)
	s.edgeRuleBundle = state.EdgeRuleBundle.Clone()
	return nil
}

func (s *edgeConfigShadow) persistLocked() error {
	if s == nil || s.statePath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.statePath), 0o700); err != nil {
		return fmt.Errorf("create edge config shadow directory: %w", err)
	}
	payload, err := json.MarshalIndent(persistedEdgeShadowState{
		AppliedRevision:     s.appliedRevision,
		PointMappings:       s.pointMappings,
		SamplingPolicies:    s.samplingPolicies,
		ConcentratorConfigs: s.concentratorConfigs,
		DriverConfigs:       s.driverConfigs,
		AutonomyPolicy:      s.autonomyPolicy,
		EdgeRuleBundle:      s.edgeRuleBundle,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode edge config shadow state: %w", err)
	}
	tmpPath := s.statePath + ".tmp"
	if err := os.WriteFile(tmpPath, append(payload, '\n'), 0o600); err != nil {
		return fmt.Errorf("write temporary edge config shadow state: %w", err)
	}
	if err := os.Rename(tmpPath, s.statePath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("commit edge config shadow state: %w", err)
	}
	return nil
}

func (s *edgeConfigShadow) snapshot() persistedEdgeShadowState {
	if s == nil {
		return persistedEdgeShadowState{
			PointMappings:       []GatewayPointMapping{},
			SamplingPolicies:    []GatewaySamplingPolicy{},
			ConcentratorConfigs: map[string]ConcentratorUnitShadow{},
			DriverConfigs:       []GatewayModbusDriverConfig{},
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return persistedEdgeShadowState{
		AppliedRevision:     s.appliedRevision,
		PointMappings:       clonePointMappings(s.pointMappings),
		SamplingPolicies:    cloneSamplingPolicies(s.samplingPolicies),
		ConcentratorConfigs: cloneConcentratorConfigs(s.concentratorConfigs),
		DriverConfigs:       cloneDriverConfigs(s.driverConfigs),
		AutonomyPolicy:      cloneAutonomyPolicy(s.autonomyPolicy),
		EdgeRuleBundle:      s.edgeRuleBundle.Clone(),
	}
}

type preparedEdgeShadow struct {
	revision            int64
	pointMappings       []GatewayPointMapping
	samplingPolicies    []GatewaySamplingPolicy
	concentratorConfigs map[string]ConcentratorUnitShadow
	driverConfigs       []GatewayModbusDriverConfig
	autonomyPolicy      *GatewayAutonomyPolicy
	edgeRuleBundle      *edgecontrol.EdgeRuleBundle
	report              GatewayShadowReportRequest
}

func clampShadowRunes(value string, maxRunes int) string {
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

func (s *edgeConfigShadow) prepareDesired(ctx context.Context, desired GatewayDesiredConfig) (preparedEdgeShadow, bool, error) {
	if s == nil {
		return preparedEdgeShadow{}, false, errors.New("edge config shadow is nil")
	}

	s.mu.Lock()
	if desired.DesiredRevision < s.appliedRevision {
		s.mu.Unlock()
		return preparedEdgeShadow{}, true, nil
	}
	hook := s.applyHook
	driverHook := s.applyDriverHook
	removeDriverHook := s.removeDriverHook
	autonomyHook := s.applyAutonomyHook
	rulesHook := s.applyEdgeRulesHook
	prevDrivers := cloneDriverConfigs(s.driverConfigs)
	prevPolicy := cloneAutonomyPolicy(s.autonomyPolicy)
	prevEdgeRules := s.edgeRuleBundle.Clone()
	s.mu.Unlock()

	drift := []ShadowDriftDetail{}
	var errorMessages []string
	cleanedUnits := map[string]ConcentratorUnitShadow{}

	if hook != nil {
		acceptedUnits := make(map[string]ConcentratorUnitShadow, len(desired.ConcentratorConfigs))
		for rawKey, unit := range desired.ConcentratorConfigs {
			uid, err := strconv.Atoi(rawKey)
			if err != nil || strconv.Itoa(uid) != rawKey || uid < 1 || uid > 247 || isProtectedEdgeConcentratorUID(uid) {
				drift = append(drift, ShadowDriftDetail{
					Section: shadowDriftSectionConcentrator,
					Target:  clampShadowRunes("unit:"+rawKey, 256),
					Reason:  shadowDriftReasonApplyFailed,
					Detail:  clampShadowRunes(fmt.Sprintf("edge rejected concentrator unit %q", rawKey), 512),
				})
				errorMessages = append(errorMessages, fmt.Sprintf("invalid concentrator unit %q", rawKey))
				continue
			}
			acceptedUnits[rawKey] = unit
		}
		cleanedUnits = cloneConcentratorConfigs(acceptedUnits)

		hookDrift, hookErrMsg, hookErr := hook(ctx, desired)
		for _, item := range hookDrift {
			target := clampShadowRunes(item.Target, 256)
			if target == "" {
				target = shadowDriftSectionConcentrator
			}
			detail := clampShadowRunes(item.Detail, 512)
			if detail == "" {
				detail = item.Reason
			}
			drift = append(drift, ShadowDriftDetail{
				Section: item.Section,
				Target:  target,
				Reason:  item.Reason,
				Detail:  detail,
			})
		}
		if strings.TrimSpace(hookErrMsg) != "" {
			errorMessages = append(errorMessages, strings.TrimSpace(hookErrMsg))
		}
		if hookErr != nil {
			errorMessages = append(errorMessages, hookErr.Error())
		}
	}

	res := applyDesiredDriversAndAutonomy(
		ctx,
		desired,
		prevDrivers,
		prevPolicy,
		prevEdgeRules,
		driverHook,
		removeDriverHook,
		autonomyHook,
		rulesHook,
		hook != nil,
		len(cleanedUnits),
		drift,
		errorMessages,
	)

	var errPtr *string
	if len(res.errorMessages) > 0 {
		joined := clampShadowRunes(strings.Join(res.errorMessages, "; "), 1024)
		errPtr = &joined
	}

	cleanedMappings := clonePointMappings(desired.PointMappings)
	cleanedPolicies := cloneSamplingPolicies(desired.SamplingPolicies)
	now := time.Now().UTC()

	return preparedEdgeShadow{
		revision:            desired.DesiredRevision,
		pointMappings:       cleanedMappings,
		samplingPolicies:    cleanedPolicies,
		concentratorConfigs: cleanedUnits,
		driverConfigs:       res.committedDrivers,
		autonomyPolicy:      res.committedPolicy,
		edgeRuleBundle:      res.committedEdgeRules,
		report: GatewayShadowReportRequest{
			ReportedRevision:    desired.DesiredRevision,
			AppliedStatus:       res.status,
			PointMappings:       cleanedMappings,
			SamplingPolicies:    cleanedPolicies,
			ConcentratorConfigs: cleanedUnits,
			DriverConfigs:       res.acceptedDrivers,
			AutonomyPolicy:      res.reportedPolicy,
			EdgeRuleBundle:      res.reportedEdgeRules,
			DriftDetails:        res.drift,
			ErrorMessage:        errPtr,
			ReportedAt:          &now,
		},
	}, false, nil
}

func (s *edgeConfigShadow) commitPrepared(prepared preparedEdgeShadow) error {
	if s == nil {
		return errors.New("edge config shadow is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if prepared.revision < s.appliedRevision {
		return nil
	}
	s.appliedRevision = prepared.revision
	s.pointMappings = clonePointMappings(prepared.pointMappings)
	s.samplingPolicies = cloneSamplingPolicies(prepared.samplingPolicies)
	s.concentratorConfigs = cloneConcentratorConfigs(prepared.concentratorConfigs)
	s.driverConfigs = cloneDriverConfigs(prepared.driverConfigs)
	s.autonomyPolicy = cloneAutonomyPolicy(prepared.autonomyPolicy)
	s.edgeRuleBundle = prepared.edgeRuleBundle.Clone()
	s.lastPublished = map[string]time.Time{}
	return s.persistLocked()
}

func (s *edgeConfigShadow) applyDesired(ctx context.Context, desired GatewayDesiredConfig) (GatewayShadowReportRequest, error) {
	if s == nil {
		return GatewayShadowReportRequest{}, errors.New("edge config shadow is nil")
	}
	s.syncMu.Lock()
	defer s.syncMu.Unlock()

	prepared, stale, err := s.prepareDesired(ctx, desired)
	if err != nil {
		return GatewayShadowReportRequest{}, err
	}
	if stale {
		return GatewayShadowReportRequest{}, nil
	}
	if err := s.commitPrepared(prepared); err != nil {
		return GatewayShadowReportRequest{}, err
	}
	return prepared.report, nil
}

type shadowEmitTarget struct {
	DeviceID  string
	PointCode string
}

func (s *edgeConfigShadow) resolveAndFilterTargets(sourceDeviceID, telemetryKey, defaultPointCode string, observedAt time.Time) []shadowEmitTarget {
	if s == nil {
		return []shadowEmitTarget{{DeviceID: sourceDeviceID, PointCode: defaultPointCode}}
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	targets := s.matchMappingTargetsLocked(sourceDeviceID, telemetryKey, defaultPointCode)
	if len(s.samplingPolicies) == 0 {
		return targets
	}
	emitted := make([]shadowEmitTarget, 0, len(targets))
	for _, target := range targets {
		if s.allowEmitByPolicyLocked(target.DeviceID, target.PointCode, observedAt) {
			emitted = append(emitted, target)
		}
	}
	return emitted
}

func (s *edgeConfigShadow) resolveTargetsUnthrottled(sourceDeviceID, telemetryKey, defaultPointCode string, observedAt time.Time) []shadowEmitTarget {
	if s == nil {
		return []shadowEmitTarget{{DeviceID: sourceDeviceID, PointCode: defaultPointCode}}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	targets := s.matchMappingTargetsLocked(sourceDeviceID, telemetryKey, defaultPointCode)
	for _, target := range targets {
		s.lastPublished[target.DeviceID+"\x00"+target.PointCode] = observedAt
	}
	return targets
}

func (s *edgeConfigShadow) matchMappingTargetsLocked(sourceDeviceID, telemetryKey, defaultPointCode string) []shadowEmitTarget {
	channelSlashCode := sourceDeviceID + "/" + defaultPointCode
	channelColonCode := sourceDeviceID + ":" + defaultPointCode
	channelSlashKey := sourceDeviceID + "/" + telemetryKey
	channelColonKey := sourceDeviceID + ":" + telemetryKey

	for _, mapping := range s.pointMappings {
		matchedByPrimary := mapping.PrimaryDeviceKey == sourceDeviceID && mapping.PrimaryPointCode == defaultPointCode
		matchedByChannel := mapping.SourceChannel == channelSlashCode ||
			mapping.SourceChannel == channelColonCode ||
			mapping.SourceChannel == channelSlashKey ||
			mapping.SourceChannel == channelColonKey
		if !matchedByPrimary && !matchedByChannel {
			continue
		}
		targets := make([]shadowEmitTarget, 0, 1+len(mapping.CopyTargets))
		targets = append(targets, shadowEmitTarget{
			DeviceID:  mapping.PrimaryDeviceKey,
			PointCode: mapping.PrimaryPointCode,
		})
		for _, copyTarget := range mapping.CopyTargets {
			targets = append(targets, shadowEmitTarget{
				DeviceID:  copyTarget.DeviceKey,
				PointCode: copyTarget.PointCode,
			})
		}
		return targets
	}
	return []shadowEmitTarget{{DeviceID: sourceDeviceID, PointCode: defaultPointCode}}
}

func (s *edgeConfigShadow) allowEmitByPolicyLocked(deviceID, pointCode string, observedAt time.Time) bool {
	policy, found := s.findPolicyLocked(deviceID, pointCode)
	if !found || policy.PublishIntervalMS <= 0 {
		return true
	}
	key := deviceID + "\x00" + pointCode
	if last, ok := s.lastPublished[key]; ok {
		interval := time.Duration(policy.PublishIntervalMS) * time.Millisecond
		if observedAt.Sub(last) < interval {
			return false
		}
	}
	s.lastPublished[key] = observedAt
	return true
}

func (s *edgeConfigShadow) findPolicyLocked(deviceID, pointCode string) (GatewaySamplingPolicy, bool) {
	var wildcard *GatewaySamplingPolicy
	for i := range s.samplingPolicies {
		policy := s.samplingPolicies[i]
		if policy.DeviceKey != deviceID {
			continue
		}
		trimmedCode := strings.TrimSpace(policy.PointCode)
		if trimmedCode == pointCode {
			return policy, true
		}
		if trimmedCode == "" || trimmedCode == "*" {
			cp := policy
			wildcard = &cp
		}
	}
	if wildcard != nil {
		return *wildcard, true
	}
	return GatewaySamplingPolicy{}, false
}

func (s *edgeConfigShadow) deadbandFor(deviceID, pointCode string) float64 {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	policy, ok := s.findPolicyLocked(deviceID, pointCode)
	if !ok || math.IsNaN(policy.Deadband) || math.IsInf(policy.Deadband, 0) || policy.Deadband <= 0 {
		return 0
	}
	return policy.Deadband
}

func (s *edgeConfigShadow) reapplyPersisted(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	drivers := cloneDriverConfigs(s.driverConfigs)
	policy := cloneAutonomyPolicy(s.autonomyPolicy)
	edgeRules := s.edgeRuleBundle.Clone()
	driverHook := s.applyDriverHook
	autonomyHook := s.applyAutonomyHook
	rulesHook := s.applyEdgeRulesHook
	s.mu.Unlock()

	var errs []string
	if driverHook != nil {
		for _, cfg := range drivers {
			profile, err := cfg.ToDeclarativeProfile()
			if err != nil {
				errs = append(errs, err.Error())
				continue
			}
			if err := driverHook(ctx, cfg, profile); err != nil {
				errs = append(errs, err.Error())
			}
		}
	}
	if policy != nil && autonomyHook != nil {
		if err := policy.Validate(); err != nil {
			errs = append(errs, err.Error())
		} else if err := autonomyHook(ctx, policy); err != nil {
			errs = append(errs, err.Error())
		}
	} else if policy == nil && autonomyHook != nil {
		if err := autonomyHook(ctx, nil); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if edgeRules != nil && rulesHook != nil {
		if err := edgeRules.Validate(); err != nil {
			errs = append(errs, err.Error())
		} else if err := rulesHook(ctx, edgeRules); err != nil {
			errs = append(errs, err.Error())
		}
	} else if edgeRules == nil && rulesHook != nil {
		if err := rulesHook(ctx, nil); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func (s *edgeConfigShadow) markAttemptIfDue(minInterval time.Duration, now time.Time) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if minInterval > 0 && !s.lastSyncAttempt.IsZero() && now.Sub(s.lastSyncAttempt) < minInterval {
		return false
	}
	s.lastSyncAttempt = now
	return true
}

// Reset removes persisted config shadow state, clears in-memory mappings, and unloads applied drivers/policies/rules.
func (s *edgeConfigShadow) Reset(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.syncMu.Lock()
	defer s.syncMu.Unlock()

	s.mu.Lock()
	drivers := cloneDriverConfigs(s.driverConfigs)
	removeDriverHook := s.removeDriverHook
	autonomyHook := s.applyAutonomyHook
	rulesHook := s.applyEdgeRulesHook
	statePath := s.statePath
	s.appliedRevision = 0
	s.pointMappings = nil
	s.samplingPolicies = nil
	s.concentratorConfigs = map[string]ConcentratorUnitShadow{}
	s.driverConfigs = nil
	s.autonomyPolicy = nil
	s.edgeRuleBundle = nil
	s.lastPublished = map[string]time.Time{}
	s.lastSyncAttempt = time.Time{}
	s.mu.Unlock()

	var errs []string
	if statePath != "" {
		for _, target := range []string{statePath, statePath + ".tmp"} {
			if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
				errs = append(errs, err.Error())
			}
		}
	}
	if removeDriverHook != nil {
		for _, cfg := range drivers {
			if err := removeDriverHook(ctx, cfg.DeviceKey); err != nil {
				errs = append(errs, err.Error())
			}
		}
	}
	if autonomyHook != nil {
		if err := autonomyHook(ctx, nil); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if rulesHook != nil {
		if err := rulesHook(ctx, nil); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func (publisher *MQTTPublisher) ConfigShadowState() (appliedRevision int64, pointMappings []GatewayPointMapping, samplingPolicies []GatewaySamplingPolicy, concentrators map[string]ConcentratorUnitShadow) {
	if publisher == nil || publisher.configShadow == nil {
		return 0, []GatewayPointMapping{}, []GatewaySamplingPolicy{}, map[string]ConcentratorUnitShadow{}
	}
	snap := publisher.configShadow.snapshot()
	return snap.AppliedRevision, snap.PointMappings, snap.SamplingPolicies, snap.ConcentratorConfigs
}

func (publisher *MQTTPublisher) shadowClient() (*http.Client, error) {
	if publisher == nil {
		return nil, nil
	}
	publisher.shadowClientMu.Lock()
	defer publisher.shadowClientMu.Unlock()
	if publisher.shadowHTTPClient != nil {
		return publisher.shadowHTTPClient, nil
	}
	built, err := newShadowHTTPClient(publisher.config)
	if err != nil {
		return nil, err
	}
	publisher.shadowHTTPClient = built
	return built, nil
}

func (publisher *MQTTPublisher) SyncConfigShadow(ctx context.Context) error {
	if publisher == nil || publisher.configShadow == nil || strings.TrimSpace(publisher.config.EnrollmentURL) == "" {
		return nil
	}
	client, err := publisher.shadowClient()
	if err != nil {
		return err
	}
	return syncConfigShadowHTTP(ctx, publisher.config, publisher.configShadow, client)
}

func (publisher *MQTTPublisher) SyncConfigShadowWithClient(ctx context.Context, client *http.Client) error {
	if publisher == nil || publisher.configShadow == nil {
		return nil
	}
	return syncConfigShadowHTTP(ctx, publisher.config, publisher.configShadow, client)
}

func (publisher *MQTTPublisher) MaybeSyncConfigShadow(ctx context.Context, minInterval time.Duration) error {
	if publisher == nil || publisher.configShadow == nil {
		return nil
	}
	if strings.TrimSpace(publisher.config.EnrollmentURL) == "" {
		return nil
	}
	if !publisher.configShadow.markAttemptIfDue(minInterval, time.Now().UTC()) {
		return nil
	}
	client, err := publisher.shadowClient()
	if err != nil {
		return err
	}
	return syncConfigShadowHTTP(ctx, publisher.config, publisher.configShadow, client)
}

func syncConfigShadowHTTP(ctx context.Context, config MQTTGatewayConfig, shadow *edgeConfigShadow, client *http.Client) error {
	baseURL := strings.TrimRight(strings.TrimSpace(config.EnrollmentURL), "/")
	if baseURL == "" || shadow == nil {
		return nil
	}
	shadow.syncMu.Lock()
	defer shadow.syncMu.Unlock()

	if client == nil {
		built, err := newShadowHTTPClient(config)
		if err != nil {
			return err
		}
		client = built
	}
	certDER, signer, hasSigningID := loadShadowSigningIdentity(config)
	snap := shadow.snapshot()
	pullURL := baseURL + "/gateway/v1/config-shadow?sinceRevision=" + url.QueryEscape(strconv.FormatInt(snap.AppliedRevision, 10))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pullURL, nil)
	if err != nil {
		return fmt.Errorf("build config shadow pull request: %w", err)
	}
	if hasSigningID {
		if err := signShadowRequestPoP(req, certDER, signer, time.Now().UTC()); err != nil {
			return fmt.Errorf("sign config shadow pull PoP: %w", err)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("pull gateway config shadow: %w", err)
	}
	pullBody, readErr := io.ReadAll(io.LimitReader(resp.Body, shadowSyncMaxBodyBytes+1))
	_ = resp.Body.Close()
	if readErr != nil {
		return fmt.Errorf("read config shadow pull response: %w", readErr)
	}
	if int64(len(pullBody)) > shadowSyncMaxBodyBytes {
		return fmt.Errorf("config shadow pull response exceeds %d bytes", shadowSyncMaxBodyBytes)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("pull gateway config shadow failed with HTTP %d", resp.StatusCode)
	}
	var pull GatewayShadowPullResponse
	if err := json.Unmarshal(pullBody, &pull); err != nil {
		return fmt.Errorf("decode config shadow pull response: %w", err)
	}
	if !pull.HasDelta || pull.Desired == nil {
		return nil
	}

	prepared, stale, err := shadow.prepareDesired(ctx, *pull.Desired)
	if err != nil {
		return err
	}
	if stale {
		return nil
	}
	reportBytes, err := json.Marshal(prepared.report)
	if err != nil {
		return fmt.Errorf("encode config shadow report: %w", err)
	}
	reportURL := baseURL + "/gateway/v1/config-shadow/report"
	postReq, err := http.NewRequestWithContext(ctx, http.MethodPost, reportURL, bytes.NewReader(reportBytes))
	if err != nil {
		return fmt.Errorf("build config shadow report request: %w", err)
	}
	postReq.Header.Set("Content-Type", "application/json")
	if hasSigningID {
		if err := signShadowRequestPoP(postReq, certDER, signer, time.Now().UTC()); err != nil {
			return fmt.Errorf("sign config shadow report PoP: %w", err)
		}
	}
	postResp, err := client.Do(postReq)
	if err != nil {
		return fmt.Errorf("report gateway config shadow: %w", err)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(postResp.Body, shadowSyncMaxBodyBytes))
	_ = postResp.Body.Close()
	if postResp.StatusCode < 200 || postResp.StatusCode >= 300 {
		return fmt.Errorf("report gateway config shadow failed with HTTP %d", postResp.StatusCode)
	}
	if err := shadow.commitPrepared(prepared); err != nil {
		return err
	}
	return nil
}

func newShadowHTTPClient(config MQTTGatewayConfig) (*http.Client, error) {
	caPath := strings.TrimSpace(config.EnrollmentCAFile)
	if caPath == "" {
		caPath = strings.TrimSpace(config.CAFile)
	}
	caContent, err := os.ReadFile(caPath)
	if err != nil {
		return nil, errors.New("read config shadow CA failed")
	}
	rootCAs := x509.NewCertPool()
	if !rootCAs.AppendCertsFromPEM(caContent) {
		return nil, errors.New("config shadow CA is invalid")
	}
	serverName := strings.TrimSpace(config.EnrollmentServerName)
	if serverName == "" {
		serverName = strings.TrimSpace(config.ServerName)
	}
	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS13,
		RootCAs:    rootCAs,
		ServerName: serverName,
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			identity, loadErr := tls.LoadX509KeyPair(config.CertFile, config.KeyFile)
			return &identity, loadErr
		},
	}
	return &http.Client{
		Timeout:   15 * time.Second,
		Transport: &http.Transport{TLSClientConfig: tlsCfg},
	}, nil
}

func loadShadowSigningIdentity(config MQTTGatewayConfig) ([]byte, crypto.Signer, bool) {
	if strings.TrimSpace(config.CertFile) == "" || strings.TrimSpace(config.KeyFile) == "" {
		return nil, nil, false
	}
	identity, err := tls.LoadX509KeyPair(config.CertFile, config.KeyFile)
	if err != nil || len(identity.Certificate) == 0 {
		return nil, nil, false
	}
	signer, ok := identity.PrivateKey.(crypto.Signer)
	if !ok {
		return nil, nil, false
	}
	return identity.Certificate[0], signer, true
}

func signShadowRequestPoP(req *http.Request, certDER []byte, signer crypto.Signer, now time.Time) error {
	return edgecontrol.SignGatewayRequestPoP(req, certDER, signer, now)
}

func isProtectedEdgeConcentratorUID(unitID int) bool {
	switch unitID {
	case 2, 3, 4, 5, 10, 31:
		return true
	default:
		return false
	}
}

func clonePointMappings(src []GatewayPointMapping) []GatewayPointMapping {
	if len(src) == 0 {
		return []GatewayPointMapping{}
	}
	out := make([]GatewayPointMapping, len(src))
	for i, item := range src {
		out[i] = item
		if len(item.CopyTargets) > 0 {
			copies := make([]PointCopyTarget, len(item.CopyTargets))
			copy(copies, item.CopyTargets)
			out[i].CopyTargets = copies
		}
	}
	return out
}

func cloneSamplingPolicies(src []GatewaySamplingPolicy) []GatewaySamplingPolicy {
	if len(src) == 0 {
		return []GatewaySamplingPolicy{}
	}
	out := make([]GatewaySamplingPolicy, len(src))
	copy(out, src)
	return out
}

func cloneConcentratorConfigs(src map[string]ConcentratorUnitShadow) map[string]ConcentratorUnitShadow {
	if len(src) == 0 {
		return map[string]ConcentratorUnitShadow{}
	}
	out := make(map[string]ConcentratorUnitShadow, len(src))
	for key, unit := range src {
		var copiedUnit ConcentratorUnitShadow
		if unit.Params != nil {
			cp := *unit.Params
			copiedUnit.Params = &cp
		}
		if len(unit.EIDs) > 0 {
			slots := make([]ConcentratorShadowEIDSlot, len(unit.EIDs))
			copy(slots, unit.EIDs)
			copiedUnit.EIDs = slots
		}
		out[key] = copiedUnit
	}
	return out
}
