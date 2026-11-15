package optimization

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type stagingAssetRow struct {
	ID              string
	Code            string
	DisplayName     string
	AssetType       string
	RatedPowerKW    float64
	RatedCapacityKW float64
}

type stagingDeviceRow struct {
	ID         string
	Code       string
	DeviceType string
	SourceKey  string
}

type stagingDeviceBindingRow struct {
	AssetID     string
	DeviceID    string
	BindingRole string
}

type stagingCommandPointRow struct {
	PointID           string
	ReportingDeviceID string
	PointCode         string
	SourceMetadata    []byte
	BoundAssetID      string
}

// StagingTopology reads site control mode, per-asset locks, active assets, bound devices,
// command points, and interlock-derived hydraulic SERVES pairings from Postgres under RLS.
func (store *PostgresStore) StagingTopology(ctx context.Context, tenantID, siteID string, at time.Time) (StagingTopology, error) {
	if !uuidPattern.MatchString(tenantID) || !uuidPattern.MatchString(siteID) {
		return StagingTopology{}, fmt.Errorf("tenantId and siteId must be UUIDs")
	}
	evalAt := at.UTC()
	if evalAt.IsZero() {
		evalAt = time.Now().UTC()
	}

	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return StagingTopology{}, err
	}
	defer tx.Rollback(ctx)
	if err = optimizationScope(ctx, tx, tenantID, siteID); err != nil {
		return StagingTopology{}, err
	}

	var siteControlMode, siteControlReason string
	if err = tx.QueryRow(ctx, `
SELECT COALESCE((
  SELECT mode FROM core_registry.site_control_states
  WHERE tenant_id=$1::uuid AND site_id=$2::uuid
), 'REMOTE_AUTO'),
COALESCE((
  SELECT reason FROM core_registry.site_control_states
  WHERE tenant_id=$1::uuid AND site_id=$2::uuid
), '')`, tenantID, siteID).Scan(&siteControlMode, &siteControlReason); err != nil {
		return StagingTopology{}, fmt.Errorf("load site control state: %w", err)
	}

	locks := map[string]AssetControlLockInfo{}
	lockRows, err := tx.Query(ctx, `
SELECT asset_id::text, lock_mode, COALESCE(reason, '')
FROM core_registry.asset_control_locks
WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND lock_mode <> 'UNLOCKED'`, tenantID, siteID)
	if err != nil {
		return StagingTopology{}, fmt.Errorf("load asset control locks: %w", err)
	}
	for lockRows.Next() {
		var assetID, mode, reason string
		if scanErr := lockRows.Scan(&assetID, &mode, &reason); scanErr != nil {
			lockRows.Close()
			return StagingTopology{}, scanErr
		}
		locks[strings.ToLower(strings.TrimSpace(assetID))] = AssetControlLockInfo{LockMode: mode, Reason: reason}
	}
	if err = lockRows.Err(); err != nil {
		lockRows.Close()
		return StagingTopology{}, err
	}
	lockRows.Close()

	assetRows, err := tx.Query(ctx, `
SELECT id::text, code, display_name, asset_type, COALESCE(rated_power, 0), COALESCE(rated_capacity, 0)
FROM core_registry.assets
WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND status='ACTIVE'
  AND asset_type IN ('CHILLER', 'CHILLED_WATER_PUMP', 'COOLING_WATER_PUMP', 'COOLING_TOWER')
ORDER BY code, id`, tenantID, siteID)
	if err != nil {
		return StagingTopology{}, fmt.Errorf("load staging assets: %w", err)
	}
	var assets []stagingAssetRow
	for assetRows.Next() {
		var row stagingAssetRow
		if scanErr := assetRows.Scan(&row.ID, &row.Code, &row.DisplayName, &row.AssetType, &row.RatedPowerKW, &row.RatedCapacityKW); scanErr != nil {
			assetRows.Close()
			return StagingTopology{}, scanErr
		}
		assets = append(assets, row)
	}
	if err = assetRows.Err(); err != nil {
		assetRows.Close()
		return StagingTopology{}, err
	}
	assetRows.Close()

	deviceRows, err := tx.Query(ctx, `
SELECT d.id::text, d.code, d.device_type, COALESCE(sk.source_key, '')
FROM core_registry.devices d
LEFT JOIN core_registry.gateway_device_source_keys sk
  ON sk.tenant_id=d.tenant_id AND sk.site_id=d.site_id AND sk.device_id=d.id AND sk.status='ACTIVE'
WHERE d.tenant_id=$1::uuid AND d.site_id=$2::uuid AND d.status='ACTIVE'
ORDER BY d.code, d.id`, tenantID, siteID)
	if err != nil {
		return StagingTopology{}, fmt.Errorf("load staging devices: %w", err)
	}
	var devices []stagingDeviceRow
	for deviceRows.Next() {
		var row stagingDeviceRow
		if scanErr := deviceRows.Scan(&row.ID, &row.Code, &row.DeviceType, &row.SourceKey); scanErr != nil {
			deviceRows.Close()
			return StagingTopology{}, scanErr
		}
		devices = append(devices, row)
	}
	if err = deviceRows.Err(); err != nil {
		deviceRows.Close()
		return StagingTopology{}, err
	}
	deviceRows.Close()

	bindingRows, err := tx.Query(ctx, `
SELECT asset_id::text, device_id::text, binding_role
FROM core_registry.device_bindings
WHERE tenant_id=$1::uuid AND site_id=$2::uuid
  AND status='ACTIVE'
  AND valid_from <= $3
  AND (valid_to IS NULL OR valid_to > $3)
ORDER BY asset_id, binding_role, device_id`, tenantID, siteID, evalAt)
	if err != nil {
		return StagingTopology{}, fmt.Errorf("load staging device bindings: %w", err)
	}
	var bindings []stagingDeviceBindingRow
	for bindingRows.Next() {
		var row stagingDeviceBindingRow
		if scanErr := bindingRows.Scan(&row.AssetID, &row.DeviceID, &row.BindingRole); scanErr != nil {
			bindingRows.Close()
			return StagingTopology{}, scanErr
		}
		bindings = append(bindings, row)
	}
	if err = bindingRows.Err(); err != nil {
		bindingRows.Close()
		return StagingTopology{}, err
	}
	bindingRows.Close()

	pointRows, err := tx.Query(ctx, `
SELECT tp.id::text, tp.reporting_device_id::text, tp.point_code, tp.source_metadata,
       COALESCE(psb.asset_id::text, '')
FROM core_registry.telemetry_points tp
LEFT JOIN core_registry.point_subject_bindings psb
  ON psb.tenant_id=tp.tenant_id AND psb.site_id=tp.site_id
  AND psb.point_id=tp.id AND psb.binding_role='CONTROLS'
  AND psb.subject_type='ASSET' AND psb.status='ACTIVE'
  AND psb.valid_from <= $3 AND (psb.valid_to IS NULL OR psb.valid_to > $3)
WHERE tp.tenant_id=$1::uuid AND tp.site_id=$2::uuid
  AND tp.point_type='COMMAND' AND tp.writable=true AND tp.status='ACTIVE'
ORDER BY tp.reporting_device_id, tp.point_code, tp.id`, tenantID, siteID, evalAt)
	if err != nil {
		return StagingTopology{}, fmt.Errorf("load staging command points: %w", err)
	}
	var commandPoints []stagingCommandPointRow
	for pointRows.Next() {
		var row stagingCommandPointRow
		if scanErr := pointRows.Scan(&row.PointID, &row.ReportingDeviceID, &row.PointCode, &row.SourceMetadata, &row.BoundAssetID); scanErr != nil {
			pointRows.Close()
			return StagingTopology{}, scanErr
		}
		commandPoints = append(commandPoints, row)
	}
	if err = pointRows.Err(); err != nil {
		pointRows.Close()
		return StagingTopology{}, err
	}
	pointRows.Close()

	if err = tx.Commit(ctx); err != nil {
		return StagingTopology{}, err
	}

	return buildStagingTopologyFromRecords(siteControlMode, siteControlReason, locks, assets, devices, bindings, commandPoints), nil
}

func buildStagingTopologyFromRecords(
	siteControlMode, siteControlReason string,
	locks map[string]AssetControlLockInfo,
	assets []stagingAssetRow,
	devices []stagingDeviceRow,
	bindings []stagingDeviceBindingRow,
	commandPoints []stagingCommandPointRow,
) StagingTopology {
	topology := StagingTopology{
		SiteControlMode:   strings.TrimSpace(siteControlMode),
		SiteControlReason: strings.TrimSpace(siteControlReason),
		Chillers:          []ChillerMeterBinding{},
	}
	if len(locks) > 0 {
		topology.AssetControlLocks = cloneAssetControlLocks(locks)
	}

	assetsByKey := make(map[string]stagingAssetRow, len(assets))
	for _, a := range assets {
		assetsByKey[strings.ToLower(strings.TrimSpace(a.ID))] = a
	}

	devicesByKey := make(map[string]stagingDeviceRow, len(devices))
	for _, d := range devices {
		devicesByKey[strings.ToLower(strings.TrimSpace(d.ID))] = d
		if strings.EqualFold(d.DeviceType, "BTU_METER") {
			if topology.CoolingLoadDeviceID == "" || strings.EqualFold(d.SourceKey, "btu-calc") || strings.EqualFold(d.Code, "btu-calc") {
				topology.CoolingLoadDeviceID = d.ID
			}
		}
	}

	type assetDevice struct {
		device stagingDeviceRow
		role   string
	}
	assetDevices := map[string][]assetDevice{}
	deviceToAssets := map[string][]string{}
	appendMapSlice := func(target map[string][]string, key, raw string) {
		items := target[key]
		appendUniqueString(&items, raw)
		target[key] = items
	}
	for _, b := range bindings {
		dev, hasDev := devicesByKey[strings.ToLower(strings.TrimSpace(b.DeviceID))]
		if !hasDev {
			continue
		}
		aKey := strings.ToLower(strings.TrimSpace(b.AssetID))
		dKey := strings.ToLower(strings.TrimSpace(dev.ID))
		assetDevices[aKey] = append(assetDevices[aKey], assetDevice{device: dev, role: strings.ToUpper(strings.TrimSpace(b.BindingRole))})
		appendMapSlice(deviceToAssets, dKey, aKey)
	}

	commandPointIDs := map[string]string{}
	edgeSet := map[string]HydraulicEdge{}
	servesByAux := map[string][]string{}

	addServeEdge := func(auxAsset, chillerAsset stagingAssetRow) {
		key := strings.ToLower(auxAsset.ID) + "|" + strings.ToLower(chillerAsset.ID)
		if _, exists := edgeSet[key]; !exists {
			edgeSet[key] = HydraulicEdge{FromAssetID: auxAsset.ID, ToAssetID: chillerAsset.ID, Role: HydraulicEdgeServes}
		}
		appendMapSlice(servesByAux, strings.ToLower(auxAsset.ID), chillerAsset.ID)
	}

	for _, pt := range commandPoints {
		capability, peerDeviceIDs := parseCommandPointStagingMetadata(pt.SourceMetadata)
		if capability == "" {
			capability = strings.ToUpper(strings.TrimSpace(pt.PointCode))
		}
		ownerKeys := []string{}
		if cleanAsset := strings.ToLower(strings.TrimSpace(pt.BoundAssetID)); cleanAsset != "" {
			ownerKeys = append(ownerKeys, cleanAsset)
		} else {
			ownerKeys = append(ownerKeys, deviceToAssets[strings.ToLower(strings.TrimSpace(pt.ReportingDeviceID))]...)
		}
		for _, ownerKey := range ownerKeys {
			ownerAsset, hasOwner := assetsByKey[ownerKey]
			if !hasOwner {
				continue
			}
			if capability != "" && pt.PointID != "" {
				cmdKey := canonicalCommandPointKey(ownerAsset.ID, capability)
				if _, exists := commandPointIDs[cmdKey]; !exists {
					commandPointIDs[cmdKey] = pt.PointID
				}
			}
			for _, peerDevID := range peerDeviceIDs {
				for _, peerAssetKey := range deviceToAssets[strings.ToLower(strings.TrimSpace(peerDevID))] {
					peerAsset, hasPeer := assetsByKey[peerAssetKey]
					if !hasPeer || strings.EqualFold(ownerAsset.ID, peerAsset.ID) {
						continue
					}
					if isAuxiliaryAssetType(ownerAsset.AssetType) && strings.EqualFold(peerAsset.AssetType, AssetTypeChiller) {
						addServeEdge(ownerAsset, peerAsset)
					} else if strings.EqualFold(ownerAsset.AssetType, AssetTypeChiller) && isAuxiliaryAssetType(peerAsset.AssetType) {
						addServeEdge(peerAsset, ownerAsset)
					}
				}
			}
		}
	}
	if len(commandPointIDs) > 0 {
		topology.CommandPointIDs = commandPointIDs
	}

	for _, edge := range edgeSet {
		topology.Edges = append(topology.Edges, edge)
	}
	sort.Slice(topology.Edges, func(i, j int) bool {
		if topology.Edges[i].FromAssetID != topology.Edges[j].FromAssetID {
			return topology.Edges[i].FromAssetID < topology.Edges[j].FromAssetID
		}
		return topology.Edges[i].ToAssetID < topology.Edges[j].ToAssetID
	})

	for _, asset := range assets {
		bound := assetDevices[strings.ToLower(strings.TrimSpace(asset.ID))]
		var boundIDs []string
		for _, b := range bound {
			appendUniqueString(&boundIDs, b.device.ID)
			appendUniqueString(&boundIDs, b.device.SourceKey)
			appendUniqueString(&boundIDs, b.device.Code)
		}

		if strings.EqualFold(asset.AssetType, AssetTypeChiller) {
			var meterDeviceID, meterSourceKey string
			for _, b := range bound {
				if b.role == "METER" || strings.HasSuffix(strings.ToUpper(b.device.DeviceType), "METER") {
					meterDeviceID = b.device.ID
					meterSourceKey = firstNonEmptyString(b.device.SourceKey, b.device.Code)
					break
				}
			}
			if meterDeviceID == "" && len(bound) > 0 {
				meterDeviceID = bound[0].device.ID
				meterSourceKey = firstNonEmptyString(bound[0].device.SourceKey, bound[0].device.Code)
			}
			topology.Chillers = append(topology.Chillers, ChillerMeterBinding{
				AssetID:         asset.ID,
				AssetCode:       asset.Code,
				AssetName:       asset.DisplayName,
				MeterDeviceID:   meterDeviceID,
				MeterSourceKey:  meterSourceKey,
				BoundDeviceIDs:  boundIDs,
				RatedCapacityKW: asset.RatedCapacityKW,
				RatedPowerKW:    asset.RatedPowerKW,
			})
			continue
		}

		if normAuxType, isAux := normalizeAuxiliaryAssetType(asset.AssetType); isAux {
			var deviceID, deviceSourceKey string
			for _, b := range bound {
				if b.role == "CONTROLLER" {
					deviceID = b.device.ID
					deviceSourceKey = firstNonEmptyString(b.device.SourceKey, b.device.Code)
					break
				}
			}
			if deviceID == "" && len(bound) > 0 {
				deviceID = bound[0].device.ID
				deviceSourceKey = firstNonEmptyString(bound[0].device.SourceKey, bound[0].device.Code)
			}
			served := append([]string(nil), servesByAux[strings.ToLower(strings.TrimSpace(asset.ID))]...)
			sort.Strings(served)
			topology.Auxiliaries = append(topology.Auxiliaries, AuxiliaryUnitBinding{
				AssetID:          asset.ID,
				AssetCode:        asset.Code,
				AssetName:        asset.DisplayName,
				AssetType:        normAuxType,
				DeviceID:         deviceID,
				DeviceSourceKey:  deviceSourceKey,
				BoundDeviceIDs:   boundIDs,
				RatedPowerKW:     asset.RatedPowerKW,
				ServesChillerIDs: served,
			})
		}
	}

	return topology
}

func parseCommandPointStagingMetadata(raw []byte) (string, []string) {
	if len(raw) == 0 {
		return "", nil
	}
	var payload struct {
		Capability      string `json:"capability"`
		InterlockChecks []struct {
			DeviceID string `json:"deviceId"`
		} `json:"interlockChecks"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", nil
	}
	capName := strings.ToUpper(strings.TrimSpace(payload.Capability))
	var peerDevices []string
	for _, check := range payload.InterlockChecks {
		appendUniqueString(&peerDevices, check.DeviceID)
	}
	return capName, peerDevices
}

func isAuxiliaryAssetType(raw string) bool {
	_, ok := normalizeAuxiliaryAssetType(raw)
	return ok
}

func appendUniqueString(target *[]string, raw string) {
	clean := strings.TrimSpace(raw)
	if clean == "" {
		return
	}
	for _, existing := range *target {
		if strings.EqualFold(existing, clean) {
			return
		}
	}
	*target = append(*target, clean)
}

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if clean := strings.TrimSpace(v); clean != "" {
			return clean
		}
	}
	return ""
}
