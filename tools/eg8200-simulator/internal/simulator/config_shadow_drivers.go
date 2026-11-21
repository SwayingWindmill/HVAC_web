package simulator

import (
	"context"
	"fmt"
	"strings"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

type GatewayModbusRegisterMapping = edgecontrol.ModbusRegisterInput

type GatewayModbusDriverConfig struct {
	DeviceKey         string                         `json:"deviceKey"`
	DriverType        string                         `json:"driverType"`
	Endpoint          string                         `json:"endpoint"`
	UnitID            int                            `json:"unitId"`
	CapabilityProfile string                         `json:"capabilityProfile,omitempty"`
	TimeoutMS         int                            `json:"timeoutMs"`
	Retries           int                            `json:"retries"`
	Registers         []GatewayModbusRegisterMapping `json:"registers"`
}

type GatewayAutonomyPolicy = edgecontrol.AutonomyPolicyConfig

type edgeDriverApplyHook func(ctx context.Context, cfg GatewayModbusDriverConfig, profile edgecontrol.DeclarativeModbusProfile) error
type edgeDriverRemoveHook func(ctx context.Context, deviceKey string) error
type edgeAutonomyApplyHook func(ctx context.Context, policy *GatewayAutonomyPolicy) error
type edgeRulesApplyHook func(ctx context.Context, bundle *edgecontrol.EdgeRuleBundle) error

// ToDeclarativeProfile converts a shadow Modbus driver configuration into a validated
// DeclarativeModbusProfile using the canonical edgecontrol validator.
func (d GatewayModbusDriverConfig) ToDeclarativeProfile() (edgecontrol.DeclarativeModbusProfile, error) {
	endpoint := d.Endpoint
	unitID := d.UnitID
	timeoutMS := d.TimeoutMS
	retries := d.Retries
	return edgecontrol.ValidateModbusDriverInput(edgecontrol.ModbusDriverInput{
		DeviceKey:         d.DeviceKey,
		DriverType:        d.DriverType,
		Endpoint:          &endpoint,
		UnitID:            &unitID,
		CapabilityProfile: d.CapabilityProfile,
		TimeoutMS:         &timeoutMS,
		Retries:           &retries,
		Registers:         d.Registers,
	}, edgecontrol.ModbusDriverValidationOptions{
		RequireDeviceKey:       true,
		RequireGatewayBindings: true,
	})
}

type driverAutonomyApplyResult struct {
	acceptedDrivers    []GatewayModbusDriverConfig
	committedDrivers   []GatewayModbusDriverConfig
	reportedPolicy     *GatewayAutonomyPolicy
	committedPolicy    *GatewayAutonomyPolicy
	reportedEdgeRules  *edgecontrol.EdgeRuleBundle
	committedEdgeRules *edgecontrol.EdgeRuleBundle
	status             string
	drift              []ShadowDriftDetail
	errorMessages      []string
}

func applyDesiredDriversAndAutonomy(
	ctx context.Context,
	desired GatewayDesiredConfig,
	prevDrivers []GatewayModbusDriverConfig,
	prevPolicy *GatewayAutonomyPolicy,
	prevEdgeRules *edgecontrol.EdgeRuleBundle,
	driverHook edgeDriverApplyHook,
	removeDriverHook edgeDriverRemoveHook,
	autonomyHook edgeAutonomyApplyHook,
	rulesHook edgeRulesApplyHook,
	hasConcentratorHook bool,
	acceptedUnitsCount int,
	drift []ShadowDriftDetail,
	errorMessages []string,
) driverAutonomyApplyResult {
	acceptedDrivers := make([]GatewayModbusDriverConfig, 0, len(desired.DriverConfigs))
	seenDeviceKeys := make(map[string]struct{}, len(desired.DriverConfigs))
	for _, cfg := range desired.DriverConfigs {
		trimmedKey := strings.TrimSpace(cfg.DeviceKey)
		targetKey := clampShadowRunes("driver:"+trimmedKey, 256)
		if trimmedKey != "" {
			if _, dup := seenDeviceKeys[trimmedKey]; dup {
				msg := fmt.Sprintf("duplicate driverConfig deviceKey %q", trimmedKey)
				drift = append(drift, ShadowDriftDetail{
					Section: shadowDriftSectionDriverConfigs,
					Target:  targetKey,
					Reason:  shadowDriftReasonApplyFailed,
					Detail:  clampShadowRunes(msg, 512),
				})
				errorMessages = append(errorMessages, msg)
				continue
			}
			seenDeviceKeys[trimmedKey] = struct{}{}
		}

		profile, err := cfg.ToDeclarativeProfile()
		if err != nil {
			msg := fmt.Sprintf("driver %s validation failed: %v", cfg.DeviceKey, err)
			drift = append(drift, ShadowDriftDetail{
				Section: shadowDriftSectionDriverConfigs,
				Target:  targetKey,
				Reason:  shadowDriftReasonApplyFailed,
				Detail:  clampShadowRunes(msg, 512),
			})
			errorMessages = append(errorMessages, msg)
			continue
		}
		if driverHook != nil {
			if err := driverHook(ctx, cfg, profile); err != nil {
				msg := fmt.Sprintf("driver %s apply failed: %v", cfg.DeviceKey, err)
				drift = append(drift, ShadowDriftDetail{
					Section: shadowDriftSectionDriverConfigs,
					Target:  targetKey,
					Reason:  shadowDriftReasonApplyFailed,
					Detail:  clampShadowRunes(msg, 512),
				})
				errorMessages = append(errorMessages, msg)
				continue
			}
		}
		acceptedDrivers = append(acceptedDrivers, cloneDriverConfig(cfg))
	}

	var reportedPolicy *GatewayAutonomyPolicy
	autonomyApplied := false
	if desired.AutonomyPolicy != nil {
		if err := desired.AutonomyPolicy.Validate(); err != nil {
			msg := fmt.Sprintf("autonomyPolicy validation failed: %v", err)
			drift = append(drift, ShadowDriftDetail{
				Section: shadowDriftSectionAutonomyPolicy,
				Target:  "autonomyPolicy",
				Reason:  shadowDriftReasonApplyFailed,
				Detail:  clampShadowRunes(msg, 512),
			})
			errorMessages = append(errorMessages, msg)
		} else if autonomyHook != nil {
			policyCopy := cloneAutonomyPolicy(desired.AutonomyPolicy)
			if err := autonomyHook(ctx, policyCopy); err != nil {
				msg := fmt.Sprintf("autonomyPolicy apply failed: %v", err)
				drift = append(drift, ShadowDriftDetail{
					Section: shadowDriftSectionAutonomyPolicy,
					Target:  "autonomyPolicy",
					Reason:  shadowDriftReasonApplyFailed,
					Detail:  clampShadowRunes(msg, 512),
				})
				errorMessages = append(errorMessages, msg)
			} else {
				reportedPolicy = cloneAutonomyPolicy(desired.AutonomyPolicy)
				autonomyApplied = true
			}
		} else {
			reportedPolicy = cloneAutonomyPolicy(desired.AutonomyPolicy)
			autonomyApplied = true
		}
	}

	var reportedEdgeRules *edgecontrol.EdgeRuleBundle
	edgeRulesApplied := false
	if desired.EdgeRuleBundle != nil {
		if err := desired.EdgeRuleBundle.Validate(); err != nil {
			msg := fmt.Sprintf("edgeRuleBundle validation failed: %v", err)
			drift = append(drift, ShadowDriftDetail{
				Section: shadowDriftSectionEdgeRules,
				Target:  "edgeRuleBundle",
				Reason:  shadowDriftReasonApplyFailed,
				Detail:  clampShadowRunes(msg, 512),
			})
			errorMessages = append(errorMessages, msg)
		} else if rulesHook != nil {
			rulesCopy := desired.EdgeRuleBundle.Clone()
			if err := rulesHook(ctx, rulesCopy); err != nil {
				msg := fmt.Sprintf("edgeRuleBundle apply failed: %v", err)
				drift = append(drift, ShadowDriftDetail{
					Section: shadowDriftSectionEdgeRules,
					Target:  "edgeRuleBundle",
					Reason:  shadowDriftReasonApplyFailed,
					Detail:  clampShadowRunes(msg, 512),
				})
				errorMessages = append(errorMessages, msg)
			} else {
				reportedEdgeRules = desired.EdgeRuleBundle.Clone()
				edgeRulesApplied = true
			}
		} else {
			reportedEdgeRules = desired.EdgeRuleBundle.Clone()
			edgeRulesApplied = true
		}
	}

	status := shadowAppliedStatusApplied
	if len(drift) > 0 || len(errorMessages) > 0 {
		requestedItems := len(desired.DriverConfigs)
		if hasConcentratorHook {
			requestedItems += len(desired.ConcentratorConfigs)
		}
		if desired.AutonomyPolicy != nil {
			requestedItems++
		}
		if desired.EdgeRuleBundle != nil {
			requestedItems++
		}
		succeededItems := len(acceptedDrivers) + acceptedUnitsCount
		if autonomyApplied {
			succeededItems++
		}
		if edgeRulesApplied {
			succeededItems++
		}
		if requestedItems > 0 && succeededItems == 0 && len(desired.PointMappings) == 0 && len(desired.SamplingPolicies) == 0 {
			status = shadowAppliedStatusRejected
		} else {
			status = shadowAppliedStatusPartial
		}
	}

	committedDrivers := acceptedDrivers
	committedPolicy := reportedPolicy
	committedEdgeRules := reportedEdgeRules
	if status == shadowAppliedStatusRejected {
		committedDrivers = prevDrivers
		committedPolicy = prevPolicy
		committedEdgeRules = prevEdgeRules
	} else {
		if removeDriverHook != nil && len(prevDrivers) > 0 {
			acceptedSet := make(map[string]struct{}, len(acceptedDrivers))
			for _, cfg := range acceptedDrivers {
				acceptedSet[strings.TrimSpace(cfg.DeviceKey)] = struct{}{}
			}
			for _, prevCfg := range prevDrivers {
				prevKey := strings.TrimSpace(prevCfg.DeviceKey)
				if prevKey == "" {
					continue
				}
				if _, keep := acceptedSet[prevKey]; keep {
					continue
				}
				if err := removeDriverHook(ctx, prevKey); err != nil {
					msg := fmt.Sprintf("driver %s remove failed: %v", prevKey, err)
					drift = append(drift, ShadowDriftDetail{
						Section: shadowDriftSectionDriverConfigs,
						Target:  clampShadowRunes("driver:"+prevKey, 256),
						Reason:  shadowDriftReasonApplyFailed,
						Detail:  clampShadowRunes(msg, 512),
					})
					errorMessages = append(errorMessages, msg)
				}
			}
		}
		if desired.AutonomyPolicy == nil {
			if autonomyHook != nil {
				if err := autonomyHook(ctx, nil); err != nil {
					msg := fmt.Sprintf("autonomyPolicy clear failed: %v", err)
					drift = append(drift, ShadowDriftDetail{
						Section: shadowDriftSectionAutonomyPolicy,
						Target:  "autonomyPolicy",
						Reason:  shadowDriftReasonApplyFailed,
						Detail:  clampShadowRunes(msg, 512),
					})
					errorMessages = append(errorMessages, msg)
					committedPolicy = prevPolicy
				} else {
					committedPolicy = nil
				}
			}
		} else if !autonomyApplied {
			committedPolicy = prevPolicy
		}
		if desired.EdgeRuleBundle == nil {
			if rulesHook != nil {
				if err := rulesHook(ctx, nil); err != nil {
					msg := fmt.Sprintf("edgeRuleBundle clear failed: %v", err)
					drift = append(drift, ShadowDriftDetail{
						Section: shadowDriftSectionEdgeRules,
						Target:  "edgeRuleBundle",
						Reason:  shadowDriftReasonApplyFailed,
						Detail:  clampShadowRunes(msg, 512),
					})
					errorMessages = append(errorMessages, msg)
					committedEdgeRules = prevEdgeRules
				} else {
					committedEdgeRules = nil
				}
			}
		} else if !edgeRulesApplied {
			committedEdgeRules = prevEdgeRules
		}
		if len(drift) > 0 && status == shadowAppliedStatusApplied {
			status = shadowAppliedStatusPartial
		}
	}

	return driverAutonomyApplyResult{
		acceptedDrivers:    acceptedDrivers,
		committedDrivers:   committedDrivers,
		reportedPolicy:     reportedPolicy,
		committedPolicy:    committedPolicy,
		reportedEdgeRules:  reportedEdgeRules,
		committedEdgeRules: committedEdgeRules,
		status:             status,
		drift:              drift,
		errorMessages:      errorMessages,
	}
}

func cloneDriverConfig(src GatewayModbusDriverConfig) GatewayModbusDriverConfig {
	cp := src
	cp.Registers = make([]GatewayModbusRegisterMapping, len(src.Registers))
	for i, r := range src.Registers {
		rcp := r
		if r.WriteFunctionCode != nil {
			v := *r.WriteFunctionCode
			rcp.WriteFunctionCode = &v
		}
		if r.BitIndex != nil {
			v := *r.BitIndex
			rcp.BitIndex = &v
		}
		if len(r.EnumMap) > 0 {
			em := make(map[string]string, len(r.EnumMap))
			for k, v := range r.EnumMap {
				em[k] = v
			}
			rcp.EnumMap = em
		}
		cp.Registers[i] = rcp
	}
	return cp
}

func cloneDriverConfigs(src []GatewayModbusDriverConfig) []GatewayModbusDriverConfig {
	if len(src) == 0 {
		return []GatewayModbusDriverConfig{}
	}
	out := make([]GatewayModbusDriverConfig, len(src))
	for i, cfg := range src {
		out[i] = cloneDriverConfig(cfg)
	}
	return out
}

func cloneAutonomyPolicy(src *GatewayAutonomyPolicy) *GatewayAutonomyPolicy {
	if src == nil {
		return nil
	}
	cp := *src
	return &cp
}
