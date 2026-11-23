package connectivity

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

const (
	defaultSystemTemplateShadowActorID = "018f0000-0000-7000-8000-000000000001"
	defaultTemplateDriverTimeoutMS     = 1000
	defaultTemplateDriverRetries       = 2
)

// TemplateDriverUpsertInput merges or removes one Registry template modbusDriver definition
// inside a gateway's desired driverConfigs while preserving device-specific bindings when omitted.
type TemplateDriverUpsertInput struct {
	TenantID          string                             `json:"tenantId"`
	DeviceKey         string                             `json:"deviceKey"`
	Remove            bool                               `json:"remove,omitempty"`
	DriverType        string                             `json:"driverType,omitempty"`
	Endpoint          *string                            `json:"endpoint,omitempty"`
	UnitID            *int                               `json:"unitId,omitempty"`
	CapabilityProfile string                             `json:"capabilityProfile,omitempty"`
	TimeoutMS         *int                               `json:"timeoutMs,omitempty"`
	Retries           *int                               `json:"retries,omitempty"`
	Registers         []GatewayModbusRegisterMapping     `json:"registers,omitempty"`
	BACnet            *edgecontrol.BACnetDriverSpecInput `json:"bacnet,omitempty"`
	OPCUA             *edgecontrol.OPCUADriverSpecInput  `json:"opcua,omitempty"`
	UpdatedBy         string                             `json:"updatedBy,omitempty"`
	Reason            string                             `json:"reason"`
}

// TemplateDriverUpsertResult reports the gateway desired shadow after upserting or removing a template driver.
type TemplateDriverUpsertResult struct {
	Shadow  GatewayConfigShadow `json:"shadow"`
	Changed bool                `json:"changed"`
	Skipped bool                `json:"skipped"`
}

// UpsertGatewayTemplateDriverConfig idempotently materializes or removes a Registry DEVICE
// template's modbusDriver spec inside connectivity.gateway_desired_configs for gatewayID.
func (store *Store) UpsertGatewayTemplateDriverConfig(ctx context.Context, gatewayID string, input TemplateDriverUpsertInput) (TemplateDriverUpsertResult, error) {
	if store == nil || store.pool == nil {
		return TemplateDriverUpsertResult{}, errors.New("connectivity store is closed")
	}
	gwID := strings.TrimSpace(gatewayID)
	tenantID := strings.TrimSpace(input.TenantID)
	deviceKey := strings.TrimSpace(input.DeviceKey)
	if !gatewayIDPattern.MatchString(gwID) {
		return TemplateDriverUpsertResult{}, fmt.Errorf("%w: gatewayId must be UUIDv7", ErrConfigShadowInvalid)
	}
	if !gatewayIDPattern.MatchString(tenantID) {
		return TemplateDriverUpsertResult{}, fmt.Errorf("%w: tenantId must be UUIDv7", ErrConfigShadowInvalid)
	}
	if deviceKey == "" {
		return TemplateDriverUpsertResult{}, fmt.Errorf("%w: deviceKey is required", ErrConfigShadowInvalid)
	}
	reason, err := validateShadowReason(input.Reason)
	if err != nil {
		return TemplateDriverUpsertResult{}, err
	}
	updatedBy := strings.TrimSpace(input.UpdatedBy)
	if updatedBy == "" {
		updatedBy = defaultSystemTemplateShadowActorID
	} else if !gatewayIDPattern.MatchString(updatedBy) {
		return TemplateDriverUpsertResult{}, fmt.Errorf("%w: updatedBy must be UUIDv7", ErrConfigShadowInvalid)
	}
	if input.UnitID != nil && (*input.UnitID < 1 || *input.UnitID > 247) {
		return TemplateDriverUpsertResult{}, fmt.Errorf("%w: unitId %d must be in [1,247]", ErrConfigShadowInvalid, *input.UnitID)
	}

	activeTenant, err := store.resolveActiveGatewayTenant(ctx, gwID)
	if err != nil {
		return TemplateDriverUpsertResult{}, err
	}
	if activeTenant != tenantID {
		return TemplateDriverUpsertResult{}, ErrCredentialForbidden
	}

	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return TemplateDriverUpsertResult{}, err
	}
	defer tx.Rollback(ctx)
	if err := setTenant(ctx, tx, tenantID); err != nil {
		return TemplateDriverUpsertResult{}, err
	}

	existing, err := loadDesiredConfigInTx(ctx, tx, tenantID, gwID, true)
	if err != nil {
		return TemplateDriverUpsertResult{}, err
	}
	reported, err := loadReportedConfigInTx(ctx, tx, tenantID, gwID, false)
	if err != nil {
		return TemplateDriverUpsertResult{}, err
	}
	currentShadow := buildGatewayConfigShadow(tenantID, gwID, existing, reported)

	var existingDrivers []GatewayModbusDriverConfig
	var matchedDriver GatewayModbusDriverConfig
	hasMatched := false
	if existing != nil {
		existingDrivers = cloneDriverConfigsList(existing.DriverConfigs)
		for _, drv := range existingDrivers {
			if drv.DeviceKey == deviceKey {
				matchedDriver = drv
				hasMatched = true
				break
			}
		}
	}

	var cleanedDrivers []GatewayModbusDriverConfig
	if input.Remove {
		if !hasMatched {
			return TemplateDriverUpsertResult{Shadow: currentShadow, Changed: false, Skipped: false}, nil
		}
		cleanedDrivers = make([]GatewayModbusDriverConfig, 0, len(existingDrivers)-1)
		for _, drv := range existingDrivers {
			if drv.DeviceKey != deviceKey {
				cleanedDrivers = append(cleanedDrivers, drv)
			}
		}
	} else {
		driverType := strings.ToUpper(strings.TrimSpace(input.DriverType))
		isModbus := driverType == string(edgecontrol.ModbusDriverTCP) || driverType == string(edgecontrol.ModbusDriverRTU)

		endpoint := ""
		if input.Endpoint != nil {
			endpoint = strings.TrimSpace(*input.Endpoint)
		} else if hasMatched {
			endpoint = matchedDriver.Endpoint
		}
		if endpoint == "" {
			return TemplateDriverUpsertResult{Shadow: currentShadow, Changed: false, Skipped: true}, nil
		}

		unitID := 0
		if isModbus {
			if input.UnitID != nil {
				unitID = *input.UnitID
			} else if hasMatched {
				unitID = matchedDriver.UnitID
			}
			if unitID == 0 {
				return TemplateDriverUpsertResult{Shadow: currentShadow, Changed: false, Skipped: true}, nil
			}
		} else if input.UnitID != nil {
			unitID = *input.UnitID
		}

		bacnetSpec := input.BACnet.Clone()
		if driverType == edgecontrol.DriverTypeBACnetIP {
			if bacnetSpec == nil {
				return TemplateDriverUpsertResult{Shadow: currentShadow, Changed: false, Skipped: true}, nil
			}
			if bacnetSpec.DeviceInstance == nil && hasMatched && matchedDriver.BACnet != nil && matchedDriver.BACnet.DeviceInstance != nil {
				v := *matchedDriver.BACnet.DeviceInstance
				bacnetSpec.DeviceInstance = &v
			}
			if bacnetSpec.DeviceInstance == nil {
				return TemplateDriverUpsertResult{Shadow: currentShadow, Changed: false, Skipped: true}, nil
			}
		}

		timeoutMS := defaultTemplateDriverTimeoutMS
		if input.TimeoutMS != nil {
			timeoutMS = *input.TimeoutMS
		} else if hasMatched && matchedDriver.TimeoutMS > 0 {
			timeoutMS = matchedDriver.TimeoutMS
		}
		retries := defaultTemplateDriverRetries
		if input.Retries != nil {
			retries = *input.Retries
		} else if hasMatched {
			retries = matchedDriver.Retries
		}

		candidate := GatewayModbusDriverConfig{
			DeviceKey:         deviceKey,
			DriverType:        strings.TrimSpace(input.DriverType),
			Endpoint:          endpoint,
			UnitID:            unitID,
			CapabilityProfile: strings.TrimSpace(input.CapabilityProfile),
			TimeoutMS:         timeoutMS,
			Retries:           retries,
			Registers:         cloneModbusRegisterMappings(input.Registers),
			BACnet:            bacnetSpec,
			OPCUA:             input.OPCUA.Clone(),
		}

		mergedDrivers := make([]GatewayModbusDriverConfig, 0, len(existingDrivers)+1)
		replaced := false
		for _, drv := range existingDrivers {
			if drv.DeviceKey == deviceKey {
				mergedDrivers = append(mergedDrivers, candidate)
				replaced = true
			} else {
				mergedDrivers = append(mergedDrivers, drv)
			}
		}
		if !replaced {
			mergedDrivers = append(mergedDrivers, candidate)
		}
		cleanedDrivers, err = validateDriverConfigs(mergedDrivers)
		if err != nil {
			return TemplateDriverUpsertResult{}, err
		}

		if existing != nil && len(diffDriverConfigs(existing.DriverConfigs, cleanedDrivers)) == 0 {
			return TemplateDriverUpsertResult{Shadow: currentShadow, Changed: false, Skipped: false}, nil
		}
	}

	effectiveMappings := []GatewayPointMapping{}
	effectivePolicies := []GatewaySamplingPolicy{}
	effectiveUnits := map[string]ConcentratorUnitShadow{}
	var effectiveAutonomy *GatewayAutonomyPolicy
	var effectiveEdgeRules *edgecontrol.EdgeRuleBundle
	var effectiveEdgeModels []edgecontrol.EdgeModelBundle
	if existing != nil {
		effectiveMappings = clonePointMappingsList(existing.PointMappings)
		effectivePolicies = cloneSamplingPoliciesList(existing.SamplingPolicies)
		effectiveUnits = cloneConcentratorUnits(existing.ConcentratorConfigs)
		effectiveAutonomy = cloneAutonomyPolicy(existing.AutonomyPolicy)
		effectiveEdgeRules = existing.EdgeRuleBundle.Clone()
		effectiveEdgeModels = cloneEdgeModelBundles(existing.EdgeModels)
	}

	encoded, err := marshalShadowSections(effectiveMappings, effectivePolicies, effectiveUnits, cleanedDrivers, effectiveAutonomy, effectiveEdgeRules, effectiveEdgeModels, nil)
	if err != nil {
		return TemplateDriverUpsertResult{}, err
	}

	now := store.clock().UTC()
	isInsert := existing == nil
	revision := int64(1)
	createdAt := now
	if !isInsert {
		revision = existing.DesiredRevision + 1
		createdAt = existing.CreatedAt
	}
	desired := GatewayDesiredConfig{
		DesiredRevision:     revision,
		PointMappings:       effectiveMappings,
		SamplingPolicies:    effectivePolicies,
		ConcentratorConfigs: effectiveUnits,
		DriverConfigs:       cleanedDrivers,
		AutonomyPolicy:      effectiveAutonomy,
		EdgeRuleBundle:      effectiveEdgeRules,
		EdgeModels:          effectiveEdgeModels,
		UpdatedBy:           updatedBy,
		Reason:              reason,
		CreatedAt:           createdAt,
		UpdatedAt:           now,
	}
	if err := persistDesiredConfigInTx(ctx, tx, tenantID, gwID, desired, encoded, isInsert); err != nil {
		return TemplateDriverUpsertResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return TemplateDriverUpsertResult{}, err
	}
	return TemplateDriverUpsertResult{
		Shadow:  buildGatewayConfigShadow(tenantID, gwID, &desired, reported),
		Changed: true,
		Skipped: false,
	}, nil
}

func cloneModbusRegisterMappings(src []GatewayModbusRegisterMapping) []GatewayModbusRegisterMapping {
	if len(src) == 0 {
		return nil
	}
	out := make([]GatewayModbusRegisterMapping, len(src))
	for i, r := range src {
		if r.WriteFunctionCode != nil {
			v := *r.WriteFunctionCode
			r.WriteFunctionCode = &v
		}
		if r.BitIndex != nil {
			v := *r.BitIndex
			r.BitIndex = &v
		}
		if r.EnumMap != nil {
			m := make(map[string]string, len(r.EnumMap))
			for k, v := range r.EnumMap {
				m[k] = v
			}
			r.EnumMap = m
		}
		out[i] = r
	}
	return out
}
