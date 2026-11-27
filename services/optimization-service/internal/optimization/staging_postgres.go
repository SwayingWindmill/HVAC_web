package optimization

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/planttwin"
)

// StagingTopology loads a Site's semantic Plant Twin snapshot and projects chillers,
// auxiliaries, control locks, command routes, operating schedule, and SERVES edges.
func (store *PostgresStore) StagingTopology(ctx context.Context, tenantID, siteID string, at time.Time) (StagingTopology, error) {
	if !uuidPattern.MatchString(tenantID) || !uuidPattern.MatchString(siteID) {
		return StagingTopology{}, fmt.Errorf("tenantId and siteId must be UUIDs")
	}
	evalAt := at.UTC()
	if evalAt.IsZero() {
		evalAt = time.Now().UTC()
	}
	twin, err := store.LoadSiteTwin(ctx, planttwin.SiteScope{TenantID: tenantID, SiteID: siteID}, evalAt)
	if err != nil {
		return StagingTopology{}, err
	}
	return stagingTopologyFromTwin(twin), nil
}

func stagingTopologyFromTwin(twin planttwin.SiteTwin) StagingTopology {
	if twin.Graph == nil {
		if normalized, err := planttwin.NewSiteTwin(twin); err == nil {
			twin = normalized
		}
	}
	twinCopy := twin
	topology := StagingTopology{
		SiteControlMode:   strings.TrimSpace(twin.SiteControlMode),
		SiteControlReason: strings.TrimSpace(twin.SiteControlReason),
		Chillers:          []ChillerMeterBinding{},
		Twin:              &twinCopy,
	}

	sched := twin.Schedule
	if strings.TrimSpace(sched.Timezone) == "" {
		sched.Timezone = strings.TrimSpace(twin.Timezone)
	}
	topology.OperatingSchedule = &sched

	locks := map[string]AssetControlLockInfo{}
	for _, unit := range twin.Units {
		mode := strings.ToUpper(strings.TrimSpace(unit.LockMode))
		if mode != "" && mode != AssetControlLockUnlocked {
			locks[strings.ToLower(strings.TrimSpace(unit.AssetID))] = AssetControlLockInfo{
				LockMode: mode,
				Reason:   strings.TrimSpace(unit.LockReason),
			}
		}
	}
	if len(locks) > 0 {
		topology.AssetControlLocks = locks
	}

	if loadPts := twin.PointsByRole(planttwin.RoleCoolingLoad); len(loadPts) > 0 {
		topology.CoolingLoadDeviceID = loadPts[0].DeviceID
	}

	commandPointIDs := map[string]string{}
	for _, unit := range twin.Units {
		for _, pt := range twin.CommandRoutesForAsset(unit.AssetID) {
			capability := parseCommandPointCapability(pt.SourceMetadata, pt.PointCode)
			if capability == "" || strings.TrimSpace(pt.PointID) == "" {
				continue
			}
			cmdKey := canonicalCommandPointKey(unit.AssetID, capability)
			if _, exists := commandPointIDs[cmdKey]; !exists {
				commandPointIDs[cmdKey] = pt.PointID
			}
		}
	}
	if len(commandPointIDs) > 0 {
		topology.CommandPointIDs = commandPointIDs
	}

	chillers := twin.UnitsByType(planttwin.AssetTypeChiller)
	edgeSet := map[string]HydraulicEdge{}
	servesByAux := map[string][]string{}
	addServeEdge := func(auxID, chillerID string) {
		key := strings.ToLower(auxID) + "|" + strings.ToLower(chillerID)
		if _, exists := edgeSet[key]; !exists {
			edgeSet[key] = HydraulicEdge{FromAssetID: auxID, ToAssetID: chillerID, Role: HydraulicEdgeServes}
		}
		auxKey := strings.ToLower(strings.TrimSpace(auxID))
		served := servesByAux[auxKey]
		appendUniqueString(&served, chillerID)
		servesByAux[auxKey] = served
	}

	auxTypes := []string{
		planttwin.AssetTypeChilledWaterPump,
		planttwin.AssetTypeCoolingWaterPump,
		planttwin.AssetTypeCoolingTower,
	}
	if twin.Graph != nil {
		for _, chiller := range chillers {
			for _, aux := range twin.Graph.EquipmentServingUnit(chiller.AssetID, auxTypes...) {
				addServeEdge(aux.AssetID, chiller.AssetID)
			}
		}
		// Auxiliaries wired to a shared header loop without direct chiller edges serve chillers attached to that loop.
		auxiliaries := twin.UnitsByType(auxTypes...)
		for _, aux := range auxiliaries {
			auxKey := strings.ToLower(strings.TrimSpace(aux.AssetID))
			if len(servesByAux[auxKey]) > 0 {
				continue
			}
			for _, targetID := range twin.Graph.Downstream(aux.AssetID) {
				targetUnit, ok := twin.UnitByID(targetID)
				if !ok || strings.EqualFold(targetUnit.AssetType, AssetTypeChiller) || isAuxiliaryAssetType(targetUnit.AssetType) {
					continue
				}
				for _, chiller := range chillers {
					for _, dsID := range twin.Graph.ReachableDownstream(chiller.AssetID) {
						if strings.EqualFold(dsID, targetUnit.AssetID) {
							addServeEdge(aux.AssetID, chiller.AssetID)
							break
						}
					}
				}
			}
		}
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

	for _, chiller := range chillers {
		boundDevs := twin.DevicesForAsset(chiller.AssetID)
		boundIDs := collectBoundDeviceIdentifiers(boundDevs)
		var meterDeviceID, meterSourceKey string
		if pt, ok := twin.ResolveAssetPoint(chiller.AssetID, planttwin.RoleActivePower); ok {
			meterDeviceID = pt.DeviceID
			meterSourceKey = firstNonEmptyString(pt.DeviceSourceKey, pt.DeviceCode)
			appendUniqueString(&boundIDs, pt.DeviceID)
			appendUniqueString(&boundIDs, pt.DeviceSourceKey)
			appendUniqueString(&boundIDs, pt.DeviceCode)
		} else if meterDevs := twin.DevicesForAsset(chiller.AssetID, "METER"); len(meterDevs) > 0 {
			meterDeviceID = meterDevs[0].DeviceID
			meterSourceKey = firstNonEmptyString(meterDevs[0].SourceKey, meterDevs[0].DeviceCode)
		} else if len(boundDevs) > 0 {
			meterDeviceID = boundDevs[0].DeviceID
			meterSourceKey = firstNonEmptyString(boundDevs[0].SourceKey, boundDevs[0].DeviceCode)
		}
		topology.Chillers = append(topology.Chillers, ChillerMeterBinding{
			AssetID:                chiller.AssetID,
			AssetCode:              chiller.AssetCode,
			AssetName:              chiller.AssetName,
			MeterDeviceID:          meterDeviceID,
			MeterSourceKey:         meterSourceKey,
			BoundDeviceIDs:         boundIDs,
			RatedCapacityKW:        derefFloat(chiller.RatedCapacityKW),
			RatedPowerKW:           derefFloat(chiller.RatedPowerKW),
			FeederAuxiliaryPowerKW: chiller.FeederAuxiliaryPowerKW,
		})
	}

	for _, aux := range twin.UnitsByType(auxTypes...) {
		normAuxType, isAux := normalizeAuxiliaryAssetType(aux.AssetType)
		if !isAux {
			continue
		}
		boundDevs := twin.DevicesForAsset(aux.AssetID)
		boundIDs := collectBoundDeviceIdentifiers(boundDevs)
		var deviceID, deviceSourceKey string
		if pt, ok := twin.ResolveAssetPoint(aux.AssetID, planttwin.RoleRunState); ok {
			deviceID = pt.DeviceID
			deviceSourceKey = firstNonEmptyString(pt.DeviceSourceKey, pt.DeviceCode)
			appendUniqueString(&boundIDs, pt.DeviceID)
			appendUniqueString(&boundIDs, pt.DeviceSourceKey)
			appendUniqueString(&boundIDs, pt.DeviceCode)
		} else if ctrlDevs := twin.DevicesForAsset(aux.AssetID, "CONTROLLER"); len(ctrlDevs) > 0 {
			deviceID = ctrlDevs[0].DeviceID
			deviceSourceKey = firstNonEmptyString(ctrlDevs[0].SourceKey, ctrlDevs[0].DeviceCode)
		} else if len(boundDevs) > 0 {
			deviceID = boundDevs[0].DeviceID
			deviceSourceKey = firstNonEmptyString(boundDevs[0].SourceKey, boundDevs[0].DeviceCode)
		}
		served := append([]string(nil), servesByAux[strings.ToLower(strings.TrimSpace(aux.AssetID))]...)
		sort.Strings(served)
		topology.Auxiliaries = append(topology.Auxiliaries, AuxiliaryUnitBinding{
			AssetID:          aux.AssetID,
			AssetCode:        aux.AssetCode,
			AssetName:        aux.AssetName,
			AssetType:        normAuxType,
			DeviceID:         deviceID,
			DeviceSourceKey:  deviceSourceKey,
			BoundDeviceIDs:   boundIDs,
			RatedPowerKW:     derefFloat(aux.RatedPowerKW),
			ServesChillerIDs: served,
		})
	}

	return topology
}

// ListStagingSites enumerates distinct tenant/site pairs known from scheduled or manual
// OPTIMIZATION_RUN jobs so sites whose policy/deployment/zone-temp preparation stalled still
// participate in sensor-only staging evaluation.
func (store *PostgresStore) ListStagingSites(ctx context.Context, _ time.Time) ([]StagingSiteScope, error) {
	if store == nil || store.pool == nil {
		return nil, errors.New("optimization postgres store is unavailable")
	}
	rows, err := store.pool.Query(ctx, `SELECT DISTINCT tenant_id::text, site_id::text
FROM core_registry.job_instances
WHERE job_type = 'OPTIMIZATION_RUN'
  AND tenant_id IS NOT NULL
  AND site_id IS NOT NULL
ORDER BY tenant_id::text, site_id::text
LIMIT 1000`)
	if err != nil {
		return nil, fmt.Errorf("list optimization staging sites from job_instances: %w", err)
	}
	defer rows.Close()

	var scopes []StagingSiteScope
	for rows.Next() {
		var scope StagingSiteScope
		if err := rows.Scan(&scope.TenantID, &scope.SiteID); err != nil {
			return nil, fmt.Errorf("scan optimization staging site: %w", err)
		}
		scopes = append(scopes, scope)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate optimization staging sites: %w", err)
	}
	return scopes, nil
}

func collectBoundDeviceIdentifiers(devices []planttwin.TwinDevice) []string {
	var out []string
	for _, dev := range devices {
		appendUniqueString(&out, dev.DeviceID)
		appendUniqueString(&out, dev.SourceKey)
		appendUniqueString(&out, dev.DeviceCode)
	}
	return out
}

func parseCommandPointCapability(raw []byte, pointCode string) string {
	if len(raw) > 0 {
		var payload struct {
			Capability string `json:"capability"`
		}
		if err := json.Unmarshal(raw, &payload); err == nil {
			if clean := strings.ToUpper(strings.TrimSpace(payload.Capability)); clean != "" {
				return clean
			}
		}
	}
	return strings.ToUpper(strings.TrimSpace(pointCode))
}

func derefFloat(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
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
