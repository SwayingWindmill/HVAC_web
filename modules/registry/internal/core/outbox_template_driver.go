package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

func (r *OutboxRelay) deliverBindingRebound(ctx context.Context, event claimedOutboxEvent) error {
	if r.connectivityDriver == nil {
		return nil
	}
	if !strings.EqualFold(payloadString(event.Payload, "kind"), string(BindingGatewaySourceKey)) {
		return nil
	}
	siteID := payloadString(event.Payload, "siteId")
	if siteID == "" {
		siteID = strings.TrimSpace(event.SiteID)
	}
	deviceID := payloadString(event.Payload, "sourceId")
	gatewayID := payloadString(event.Payload, "targetId")
	newSourceKey := payloadString(event.Payload, "role")
	if siteID == "" || deviceID == "" || gatewayID == "" {
		return nil
	}
	for _, prev := range extractPreviousGatewayBindings(event.Payload) {
		if strings.EqualFold(prev.GatewayID, gatewayID) && prev.SourceKey == newSourceKey {
			continue
		}
		if err := r.connectivityDriver.UpsertGatewayTemplateDriver(ctx, prev.GatewayID, RelayGatewayTemplateDriverRequest{
			TenantID:  event.TenantID,
			DeviceKey: prev.SourceKey,
			Remove:    true,
			UpdatedBy: strings.TrimSpace(event.EventID),
			Reason:    "binding-rebound:" + event.EventID,
		}); err != nil {
			return err
		}
	}
	return r.syncDeviceTemplateDriversToBoundGateways(ctx, event.TenantID, siteID, deviceID, gatewayID, event.EventID)
}

func (r *OutboxRelay) deliverBindingClosed(ctx context.Context, event claimedOutboxEvent) error {
	if r.connectivityDriver == nil {
		return nil
	}
	if !strings.EqualFold(payloadString(event.Payload, "kind"), string(BindingGatewaySourceKey)) {
		return nil
	}
	gatewayID := payloadString(event.Payload, "gatewayId")
	if gatewayID == "" {
		gatewayID = payloadString(event.Payload, "targetId")
	}
	sourceKey := payloadString(event.Payload, "sourceKey")
	if sourceKey == "" {
		sourceKey = payloadString(event.Payload, "role")
	}
	if gatewayID == "" || sourceKey == "" {
		return fmt.Errorf("malformed %s payload: missing gatewayId or sourceKey", event.EventType)
	}
	return r.connectivityDriver.UpsertGatewayTemplateDriver(ctx, gatewayID, RelayGatewayTemplateDriverRequest{
		TenantID:  event.TenantID,
		DeviceKey: sourceKey,
		Remove:    true,
		UpdatedBy: strings.TrimSpace(event.EventID),
		Reason:    "binding-closed:" + event.EventID,
	})
}

func extractPreviousGatewayBindings(payload map[string]any) []closedGatewayBinding {
	if len(payload) == 0 {
		return nil
	}
	var out []closedGatewayBinding
	seen := make(map[string]struct{})
	appendBinding := func(gwID, srcKey string) {
		gwID = strings.TrimSpace(gwID)
		srcKey = strings.TrimSpace(srcKey)
		if gwID == "" || srcKey == "" {
			return
		}
		k := strings.ToLower(gwID) + "\x00" + srcKey
		if _, dup := seen[k]; dup {
			return
		}
		seen[k] = struct{}{}
		out = append(out, closedGatewayBinding{GatewayID: gwID, SourceKey: srcKey})
	}
	if rawList, ok := payload["previousBindings"].([]any); ok {
		for _, raw := range rawList {
			item, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			appendBinding(payloadString(item, "gatewayId"), payloadString(item, "sourceKey"))
		}
	}
	if len(out) == 0 {
		appendBinding(payloadString(payload, "previousGatewayId"), payloadString(payload, "previousSourceKey"))
	}
	return out
}

func (r *OutboxRelay) removeDeviceTemplateDriversFromBoundGateways(
	ctx context.Context,
	tenantID, siteID, deviceID, causationEventID, reason string,
) error {
	if r.connectivityDriver == nil || tenantID == "" || siteID == "" || deviceID == "" {
		return nil
	}
	type boundGatewayTarget struct {
		gatewayID string
		sourceKey string
	}
	var targets []boundGatewayTarget
	err := r.withTenantWriterTx(ctx, tenantID, pgx.ReadOnly, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
SELECT sk.gateway_device_id::text, sk.source_key
FROM core_registry.gateway_device_source_keys AS sk
JOIN core_registry.devices AS gw
  ON gw.tenant_id = sk.tenant_id AND gw.site_id = sk.site_id AND gw.id = sk.gateway_device_id
WHERE sk.tenant_id = $1::uuid
  AND sk.site_id = $2::uuid
  AND sk.device_id = $3::uuid
  AND sk.status = 'ACTIVE'
  AND gw.device_type = 'GATEWAY'
  AND gw.status = 'ACTIVE'
ORDER BY sk.gateway_device_id ASC, sk.source_key ASC
`, tenantID, siteID, deviceID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var target boundGatewayTarget
			if scanErr := rows.Scan(&target.gatewayID, &target.sourceKey); scanErr != nil {
				return scanErr
			}
			targets = append(targets, target)
		}
		return rows.Err()
	})
	if err != nil {
		return err
	}
	for _, target := range targets {
		if err := r.connectivityDriver.UpsertGatewayTemplateDriver(ctx, target.gatewayID, RelayGatewayTemplateDriverRequest{
			TenantID:  tenantID,
			DeviceKey: target.sourceKey,
			Remove:    true,
			UpdatedBy: strings.TrimSpace(causationEventID),
			Reason:    reason,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (r *OutboxRelay) syncDeviceTemplateDriversToBoundGateways(
	ctx context.Context,
	tenantID, siteID, deviceID, gatewayFilterID, causationEventID string,
) error {
	if r.connectivityDriver == nil || tenantID == "" || siteID == "" || deviceID == "" {
		return nil
	}
	type boundGatewayTarget struct {
		gatewayID string
		sourceKey string
	}
	var (
		templateKey   string
		revisionID    string
		driver        *TemplateModbusDriver
		deviceStatus  string
		deviceTagsRaw []byte
		boundGateways []boundGatewayTarget
	)
	err := r.withTenantWriterTx(ctx, tenantID, pgx.ReadOnly, func(tx pgx.Tx) error {
		var payloadRaw []byte
		qErr := tx.QueryRow(ctx, `
SELECT tr.id::text, t.template_key, tr.payload
FROM core_registry.registry_template_assignments AS ta
JOIN core_registry.registry_template_revisions AS tr
  ON tr.tenant_id = ta.tenant_id AND tr.id = ta.template_revision_id
JOIN core_registry.registry_templates AS t
  ON t.tenant_id = tr.tenant_id AND t.id = tr.template_id
WHERE ta.tenant_id = $1::uuid
  AND ta.site_id = $2::uuid
  AND ta.target_type = 'DEVICE'
  AND ta.target_id = $3::uuid
  AND ta.status = 'ACTIVE'
  AND ta.valid_to IS NULL
ORDER BY ta.valid_from DESC, ta.created_at DESC
LIMIT 1
`, tenantID, siteID, deviceID).Scan(&revisionID, &templateKey, &payloadRaw)
		if qErr != nil && !errors.Is(qErr, pgx.ErrNoRows) {
			return qErr
		}
		if qErr == nil && len(payloadRaw) > 0 {
			var payloadMap map[string]any
			if err := json.Unmarshal(payloadRaw, &payloadMap); err != nil {
				return err
			}
			activation, err := parseAndValidateTemplatePayload(TemplateDevice, payloadMap)
			if err != nil {
				return err
			}
			if activation != nil && activation.ModbusDriver != nil {
				devErr := tx.QueryRow(ctx, `
SELECT status, COALESCE(tags, '{}'::jsonb)
FROM core_registry.devices
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND id = $3::uuid
`, tenantID, siteID, deviceID).Scan(&deviceStatus, &deviceTagsRaw)
				if devErr != nil && !errors.Is(devErr, pgx.ErrNoRows) {
					return devErr
				}
				if devErr == nil && strings.EqualFold(deviceStatus, "ACTIVE") {
					driver = activation.ModbusDriver
				}
			}
		}

		rows, err := tx.Query(ctx, `
SELECT sk.gateway_device_id::text, sk.source_key
FROM core_registry.gateway_device_source_keys AS sk
JOIN core_registry.devices AS gw
  ON gw.tenant_id = sk.tenant_id AND gw.site_id = sk.site_id AND gw.id = sk.gateway_device_id
WHERE sk.tenant_id = $1::uuid
  AND sk.site_id = $2::uuid
  AND sk.device_id = $3::uuid
  AND sk.status = 'ACTIVE'
  AND gw.device_type = 'GATEWAY'
  AND gw.status = 'ACTIVE'
ORDER BY sk.gateway_device_id ASC, sk.source_key ASC
`, tenantID, siteID, deviceID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var target boundGatewayTarget
			if scanErr := rows.Scan(&target.gatewayID, &target.sourceKey); scanErr != nil {
				return scanErr
			}
			if gatewayFilterID == "" || strings.EqualFold(target.gatewayID, gatewayFilterID) {
				boundGateways = append(boundGateways, target)
			}
		}
		return rows.Err()
	})
	if err != nil || len(boundGateways) == 0 {
		return err
	}
	if driver == nil {
		for _, target := range boundGateways {
			if err := r.connectivityDriver.UpsertGatewayTemplateDriver(ctx, target.gatewayID, RelayGatewayTemplateDriverRequest{
				TenantID:  tenantID,
				DeviceKey: target.sourceKey,
				Remove:    true,
				UpdatedBy: strings.TrimSpace(causationEventID),
				Reason:    "template-removed:" + causationEventID,
			}); err != nil {
				return err
			}
		}
		return nil
	}

	overrides, err := parseDriverTagOverrides(deviceTagsRaw)
	if err != nil {
		return err
	}
	endpoint := driver.Endpoint
	unitID := driver.UnitID
	bacnetSpec := driver.BACnet.Clone()
	opcuaSpec := driver.OPCUA.Clone()

	switch strings.ToUpper(strings.TrimSpace(driver.DriverType)) {
	case edgecontrol.DriverTypeBACnetIP:
		if endpoint == nil {
			endpoint = overrides.bacnetEndpoint
		}
		if bacnetSpec != nil && bacnetSpec.DeviceInstance == nil && overrides.bacnetDeviceInstance != nil {
			inst := *overrides.bacnetDeviceInstance
			bacnetSpec.DeviceInstance = &inst
		}
	case edgecontrol.DriverTypeOPCUA:
		if endpoint == nil {
			endpoint = overrides.opcuaEndpoint
		}
	default:
		if endpoint == nil {
			endpoint = overrides.modbusEndpoint
		}
		if unitID == nil {
			unitID = overrides.modbusUnitID
		}
	}

	reason := "template:" + templateKey + "@" + revisionID
	for _, target := range boundGateways {
		req := RelayGatewayTemplateDriverRequest{
			TenantID:          tenantID,
			DeviceKey:         target.sourceKey,
			DriverType:        driver.DriverType,
			Endpoint:          endpoint,
			UnitID:            unitID,
			CapabilityProfile: driver.CapabilityProfile,
			TimeoutMS:         driver.TimeoutMS,
			Retries:           driver.Retries,
			Registers:         edgecontrol.CloneModbusRegisters(driver.Registers),
			BACnet:            bacnetSpec.Clone(),
			OPCUA:             opcuaSpec.Clone(),
			UpdatedBy:         strings.TrimSpace(causationEventID),
			Reason:            reason,
		}
		if err := r.connectivityDriver.UpsertGatewayTemplateDriver(ctx, target.gatewayID, req); err != nil {
			return err
		}
	}
	return nil
}

type driverTagOverrides struct {
	modbusEndpoint       *string
	modbusUnitID         *int
	bacnetEndpoint       *string
	bacnetDeviceInstance *int
	opcuaEndpoint        *string
}

func parseModbusTagOverrides(raw []byte) (*string, *int, error) {
	parsed, err := parseDriverTagOverrides(raw)
	if err != nil {
		return nil, nil, err
	}
	return parsed.modbusEndpoint, parsed.modbusUnitID, nil
}

func parseDriverTagOverrides(raw []byte) (driverTagOverrides, error) {
	var out driverTagOverrides
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return out, nil
	}
	var tags map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &tags); err != nil {
		return out, fmt.Errorf("parse device tags: %w", err)
	}
	var err error
	if out.modbusEndpoint, err = parseOptionalStringTag(tags, "modbus_endpoint"); err != nil {
		return out, err
	}
	if out.modbusUnitID, err = parseOptionalIntTag(tags, "modbus_unit_id", 1, 247); err != nil {
		return out, err
	}
	if out.bacnetEndpoint, err = parseOptionalStringTag(tags, "bacnet_endpoint"); err != nil {
		return out, err
	}
	if out.bacnetDeviceInstance, err = parseOptionalIntTag(tags, "bacnet_device_instance", 0, 4194302); err != nil {
		return out, err
	}
	if out.opcuaEndpoint, err = parseOptionalStringTag(tags, "opcua_endpoint"); err != nil {
		return out, err
	}
	return out, nil
}

func parseOptionalStringTag(tags map[string]json.RawMessage, key string) (*string, error) {
	rawVal, ok := tags[key]
	if !ok || bytes.Equal(bytes.TrimSpace(rawVal), []byte("null")) {
		return nil, nil
	}
	var s string
	if err := json.Unmarshal(rawVal, &s); err != nil {
		return nil, fmt.Errorf("%s tag must be a string: %w", key, err)
	}
	clean := strings.TrimSpace(s)
	if clean == "" {
		return nil, fmt.Errorf("%s tag must not be blank", key)
	}
	return &clean, nil
}

func parseOptionalIntTag(tags map[string]json.RawMessage, key string, minVal, maxVal int) (*int, error) {
	rawVal, ok := tags[key]
	if !ok || bytes.Equal(bytes.TrimSpace(rawVal), []byte("null")) {
		return nil, nil
	}
	parsed := 0
	var iVal int
	if err := json.Unmarshal(rawVal, &iVal); err == nil {
		parsed = iVal
	} else {
		var sVal string
		if strErr := json.Unmarshal(rawVal, &sVal); strErr != nil {
			return nil, fmt.Errorf("%s tag must be an integer or numeric string: %w", key, strErr)
		}
		v, parseErr := strconv.Atoi(strings.TrimSpace(sVal))
		if parseErr != nil {
			return nil, fmt.Errorf("invalid %s tag %q: %w", key, sVal, parseErr)
		}
		parsed = v
	}
	if parsed < minVal || parsed > maxVal {
		return nil, fmt.Errorf("%s tag %d out of range [%d,%d]", key, parsed, minVal, maxVal)
	}
	return &parsed, nil
}
