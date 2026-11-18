package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/modules/telemetry/pkg/telemetryapi"
)

const (
	maximumOptimizationLiveRequestSize = 64 << 10
	maximumOptimizationLiveScopeKeys   = 256
)

// OptimizationLiveObservationReader resolves current numeric site observations
// for optimization preparation from hot cache with Postgres fallback.
type OptimizationLiveObservationReader interface {
	ReadOptimizationLiveObservations(ctx context.Context, req OptimizationLiveObservationsRequest) (OptimizationLiveObservationsResponse, error)
}

type OptimizationLiveObservationsRequest struct {
	TenantID      string   `json:"tenantId"`
	SiteID        string   `json:"siteId"`
	TelemetryKeys []string `json:"telemetryKeys,omitempty"`
	DeviceIDs     []string `json:"deviceIds,omitempty"`
}

type OptimizationLiveObservationsResponse struct {
	Observations       map[string]OptimizationLiveObservation            `json:"observations"`
	DeviceObservations map[string]map[string]OptimizationLiveObservation `json:"deviceObservations,omitempty"`
	AmbiguousKeys      []string                                          `json:"ambiguousKeys,omitempty"`
}

type resolvedNumericSitePoint struct {
	DeviceID      string
	PointID       string
	TelemetryKey  string
	PointRevision uint64
}

type LiveObservationPostgresStore interface {
	ResolveNumericSitePoints(ctx context.Context, tenantID, siteID string, telemetryKeys []string) (map[string]resolvedNumericSitePoint, []string, error)
	ReadLatestNumericObservations(ctx context.Context, tenantID, siteID string, pointIDs []string) ([]OptimizationLiveObservation, error)
}

// DeviceLiveObservationPostgresStore resolves latest device-keyed observations from Postgres
// when a device snapshot is absent from the Redis Latest cache.
type DeviceLiveObservationPostgresStore interface {
	ReadLatestDeviceObservations(ctx context.Context, tenantID, siteID string, deviceIDs []string) (map[string]map[string]OptimizationLiveObservation, error)
}

type postgresLiveObservationReader struct {
	store       LiveObservationPostgresStore
	cache       SiteLiveCache
	deviceCache LatestCache
}

func newPostgresLiveObservationReader(store LiveObservationPostgresStore, cache SiteLiveCache, deviceCache LatestCache) OptimizationLiveObservationReader {
	if store == nil {
		return nil
	}
	return &postgresLiveObservationReader{store: store, cache: cache, deviceCache: deviceCache}
}

func (r *postgresLiveObservationReader) ReadOptimizationLiveObservations(ctx context.Context, req OptimizationLiveObservationsRequest) (OptimizationLiveObservationsResponse, error) {
	if r == nil || r.store == nil {
		return OptimizationLiveObservationsResponse{}, errors.New("telemetry live observation reader is not configured")
	}
	tenantID := strings.ToLower(strings.TrimSpace(req.TenantID))
	siteID := strings.ToLower(strings.TrimSpace(req.SiteID))
	if !uuidPattern.MatchString(tenantID) || !uuidPattern.MatchString(siteID) {
		return OptimizationLiveObservationsResponse{}, errors.New("telemetry optimization live observation scope is invalid")
	}
	telemetryKeys := normalizeUniqueStrings(req.TelemetryKeys)
	deviceIDs, err := normalizeSiteDeviceIDs(req.DeviceIDs)
	if err != nil {
		return OptimizationLiveObservationsResponse{}, err
	}
	if len(telemetryKeys) == 0 && len(deviceIDs) == 0 {
		return OptimizationLiveObservationsResponse{
			Observations: map[string]OptimizationLiveObservation{},
		}, nil
	}

	response := OptimizationLiveObservationsResponse{
		Observations: map[string]OptimizationLiveObservation{},
	}
	if len(telemetryKeys) > 0 {
		resolved, ambiguousKeys, err := r.store.ResolveNumericSitePoints(ctx, tenantID, siteID, telemetryKeys)
		if err != nil {
			return OptimizationLiveObservationsResponse{}, err
		}
		response.AmbiguousKeys = ambiguousKeys
		if len(resolved) > 0 {
			pointIDs := make([]string, 0, len(resolved))
			keyByPointID := make(map[string]string, len(resolved))
			for _, key := range telemetryKeys {
				pt, ok := resolved[key]
				if !ok {
					continue
				}
				pointID := strings.ToLower(strings.TrimSpace(pt.PointID))
				if pointID == "" {
					continue
				}
				if _, dup := keyByPointID[pointID]; !dup {
					pointIDs = append(pointIDs, pointID)
					keyByPointID[pointID] = key
				}
			}

			observations := make(map[string]OptimizationLiveObservation, len(pointIDs))
			if r.cache != nil && len(pointIDs) > 0 {
				if cached, cacheErr := r.cache.GetSiteLiveObservations(ctx, tenantID, siteID, pointIDs); cacheErr == nil {
					for pointID, obs := range cached {
						expectedKey, ok := keyByPointID[pointID]
						if !ok || obs.TelemetryKey != expectedKey {
							continue
						}
						if obs.PointRevision < resolved[expectedKey].PointRevision {
							continue
						}
						observations[expectedKey] = obs
					}
				}
			}

			missingPointIDs := make([]string, 0, len(pointIDs))
			for _, pointID := range pointIDs {
				key := keyByPointID[pointID]
				if _, hit := observations[key]; !hit {
					missingPointIDs = append(missingPointIDs, pointID)
				}
			}

			if len(missingPointIDs) > 0 {
				loaded, err := r.store.ReadLatestNumericObservations(ctx, tenantID, siteID, missingPointIDs)
				if err != nil {
					return OptimizationLiveObservationsResponse{}, err
				}
				backfill := make([]OptimizationLiveObservation, 0, len(loaded))
				for _, obs := range loaded {
					observations[obs.TelemetryKey] = obs
					backfill = append(backfill, obs)
				}
				if r.cache != nil && len(backfill) > 0 {
					_ = r.cache.PutSiteLiveObservations(ctx, tenantID, siteID, backfill)
				}
			}
			response.Observations = observations
		}
	}

	if len(deviceIDs) > 0 {
		deviceObs, err := r.readDeviceObservations(ctx, tenantID, siteID, deviceIDs)
		if err != nil {
			return OptimizationLiveObservationsResponse{}, err
		}
		response.DeviceObservations = deviceObs
	}

	return response, nil
}

func (store *PostgresStore) ResolveNumericSitePoints(ctx context.Context, tenantID, siteID string, telemetryKeys []string) (map[string]resolvedNumericSitePoint, []string, error) {
	if store == nil || store.pool == nil {
		return nil, nil, errors.New("telemetry store pool is not configured")
	}
	cleanTenant := strings.ToLower(strings.TrimSpace(tenantID))
	cleanSite := strings.ToLower(strings.TrimSpace(siteID))
	cleanKeys := normalizeUniqueStrings(telemetryKeys)
	if !uuidPattern.MatchString(cleanTenant) || !uuidPattern.MatchString(cleanSite) {
		return nil, nil, errors.New("telemetry optimization live observation scope is invalid")
	}
	if len(cleanKeys) == 0 {
		return map[string]resolvedNumericSitePoint{}, nil, nil
	}

	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE s2_telemetry_runtime`); err != nil {
		return nil, nil, err
	}

	rows, err := tx.Query(ctx, `
SELECT
  p.device_id::text,
  p.point_id::text,
  p.telemetry_key,
  p.point_revision
FROM telemetry_runtime.points p
JOIN telemetry_runtime.devices d
  ON d.device_id = p.device_id
WHERE d.tenant_id = $1::uuid
  AND d.site_id = $2::uuid
  AND p.tenant_id = $1::uuid
  AND p.site_id = $2::uuid
  AND p.telemetry_key = ANY($3::text[])
  AND p.value_type = 'NUMBER'
  AND EXISTS (
    SELECT 1
    FROM telemetry_runtime.latest_accepted_telemetry lat
    WHERE lat.device_id = p.device_id
      AND lat.telemetry_key = p.telemetry_key
  )
ORDER BY p.telemetry_key, p.updated_at DESC, p.point_id`, cleanTenant, cleanSite, cleanKeys)
	if err != nil {
		return nil, nil, err
	}

	resolved := make(map[string]resolvedNumericSitePoint, len(cleanKeys))
	seenPoints := make(map[string]map[string]struct{}, len(cleanKeys))
	ambiguousSet := make(map[string]struct{})
	for rows.Next() {
		var deviceID, pointID, key string
		var pointRevision int64
		if scanErr := rows.Scan(&deviceID, &pointID, &key, &pointRevision); scanErr != nil {
			rows.Close()
			return nil, nil, scanErr
		}
		if pointRevision < 1 {
			continue
		}
		lowerPointID := strings.ToLower(strings.TrimSpace(pointID))
		lowerDeviceID := strings.ToLower(strings.TrimSpace(deviceID))
		pointSet := seenPoints[key]
		if pointSet == nil {
			pointSet = make(map[string]struct{}, 2)
			seenPoints[key] = pointSet
		}
		pointSet[lowerPointID] = struct{}{}
		if len(pointSet) > 1 {
			ambiguousSet[key] = struct{}{}
			delete(resolved, key)
			continue
		}
		if _, exists := resolved[key]; !exists {
			resolved[key] = resolvedNumericSitePoint{
				DeviceID:      lowerDeviceID,
				PointID:       lowerPointID,
				TelemetryKey:  key,
				PointRevision: uint64(pointRevision),
			}
		}
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		rows.Close()
		return nil, nil, rowsErr
	}
	rows.Close()

	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}

	var ambiguousKeys []string
	if len(ambiguousSet) > 0 {
		ambiguousKeys = make([]string, 0, len(ambiguousSet))
		for key := range ambiguousSet {
			ambiguousKeys = append(ambiguousKeys, key)
		}
		slices.Sort(ambiguousKeys)
	}
	return resolved, ambiguousKeys, nil
}

func (store *PostgresStore) ReadLatestNumericObservations(ctx context.Context, tenantID, siteID string, pointIDs []string) ([]OptimizationLiveObservation, error) {
	if store == nil || store.pool == nil {
		return nil, errors.New("telemetry store pool is not configured")
	}
	cleanTenant := strings.ToLower(strings.TrimSpace(tenantID))
	cleanSite := strings.ToLower(strings.TrimSpace(siteID))
	if !uuidPattern.MatchString(cleanTenant) || !uuidPattern.MatchString(cleanSite) {
		return nil, errors.New("telemetry optimization live observation scope is invalid")
	}
	cleanPointIDs, err := normalizeSitePointIDs(pointIDs)
	if err != nil {
		return nil, err
	}
	if len(cleanPointIDs) == 0 {
		return nil, nil
	}

	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE s2_telemetry_runtime`); err != nil {
		return nil, err
	}

	rows, err := tx.Query(ctx, `
SELECT
  p.device_id::text,
  p.point_id::text,
  p.telemetry_key,
  p.point_revision,
  lat.value,
  lat.value_type,
  COALESCE(lat.unit, p.unit, ''),
  lat.sampled_at,
  lat.received_at,
  lat.quality,
  COALESCE(lat.quality_reasons, '{}'),
  COALESCE(lat.health_status, ''),
  COALESCE(lat.health_reasons, '{}'),
  COALESCE(lat.health_at, lat.sampled_at),
  so.observation_id::text,
  so.source_event_id::text,
  so.source_partition,
  so.source_offset
FROM telemetry_runtime.points p
JOIN telemetry_runtime.devices d
  ON d.device_id = p.device_id
JOIN telemetry_runtime.latest_accepted_telemetry lat
  ON lat.device_id = p.device_id
 AND lat.telemetry_key = p.telemetry_key
JOIN LATERAL (
  SELECT
    so.observation_id,
    so.source_event_id,
    so.source_partition,
    so.source_offset
  FROM telemetry_runtime.source_observations so
  WHERE so.device_id = lat.device_id
    AND so.telemetry_key = lat.telemetry_key
    AND so.sampled_at = lat.sampled_at
    AND so.acceptance_status = 'ACCEPTED'
  ORDER BY so.received_at DESC, so.source_offset DESC, so.observation_id ASC
  LIMIT 1
) so ON true
WHERE d.tenant_id = $1::uuid
  AND d.site_id = $2::uuid
  AND p.tenant_id = $1::uuid
  AND p.site_id = $2::uuid
  AND p.point_id = ANY($3::uuid[])
  AND p.value_type = 'NUMBER'
  AND lat.value_type = 'NUMBER'
ORDER BY p.telemetry_key, p.point_id`, cleanTenant, cleanSite, cleanPointIDs)
	if err != nil {
		return nil, err
	}

	out := make([]OptimizationLiveObservation, 0, len(cleanPointIDs))
	seenPoints := make(map[string]struct{}, len(cleanPointIDs))
	for rows.Next() {
		var (
			deviceID        string
			pointID         string
			telemetryKey    string
			pointRevision   int64
			rawValue        []byte
			valueType       string
			unit            string
			sampledAt       time.Time
			receivedAt      time.Time
			quality         string
			qualityReasons  []string
			healthStatus    string
			healthReasons   []string
			healthAt        time.Time
			observationID   string
			sourceEventID   string
			sourcePartition string
			sourceOffset    int64
		)
		if scanErr := rows.Scan(
			&deviceID, &pointID, &telemetryKey, &pointRevision,
			&rawValue, &valueType, &unit, &sampledAt, &receivedAt,
			&quality, &qualityReasons, &healthStatus, &healthReasons, &healthAt,
			&observationID, &sourceEventID, &sourcePartition, &sourceOffset,
		); scanErr != nil {
			rows.Close()
			return nil, scanErr
		}
		if valueType != "NUMBER" || pointRevision < 1 || sourceOffset < 0 {
			continue
		}
		kind, number, valid := observationValueType(rawValue)
		if !valid || kind != "NUMBER" || math.IsNaN(number) || math.IsInf(number, 0) {
			continue
		}
		lowerPointID := strings.ToLower(strings.TrimSpace(pointID))
		if _, dup := seenPoints[lowerPointID]; dup {
			continue
		}
		seenPoints[lowerPointID] = struct{}{}

		if qualityReasons == nil {
			qualityReasons = []string{}
		}
		if healthStatus == "" {
			typedReasons := make([]QualityReason, len(qualityReasons))
			for i, reason := range qualityReasons {
				typedReasons[i] = QualityReason(reason)
			}
			derivedHealth := HealthFor(ObservationQuality(quality), typedReasons)
			healthStatus = derivedHealth.Status
			healthReasons = qualityReasonStrings(derivedHealth.Reasons)
		} else if healthReasons == nil {
			healthReasons = []string{}
		}

		numCopy := number
		out = append(out, OptimizationLiveObservation{
			ObservationID:   strings.ToLower(strings.TrimSpace(observationID)),
			DeviceID:        strings.ToLower(strings.TrimSpace(deviceID)),
			PointID:         lowerPointID,
			TelemetryKey:    telemetryKey,
			PointRevision:   uint64(pointRevision),
			SampledAt:       sampledAt.UTC().Format(time.RFC3339Nano),
			ReceivedAt:      receivedAt.UTC().Format(time.RFC3339Nano),
			HealthAt:        healthAt.UTC().Format(time.RFC3339Nano),
			Value:           number,
			ValueType:       "NUMBER",
			ValueNumber:     &numCopy,
			Unit:            unit,
			Quality:         quality,
			QualityReasons:  qualityReasons,
			HealthStatus:    healthStatus,
			HealthReasons:   healthReasons,
			SourceEventID:   strings.ToLower(strings.TrimSpace(sourceEventID)),
			SourcePartition: sourcePartition,
			SourceOffset:    uint64(sourceOffset),
		})
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		rows.Close()
		return nil, rowsErr
	}
	rows.Close()

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *postgresLiveObservationReader) readDeviceObservations(
	ctx context.Context,
	tenantID, siteID string,
	deviceIDs []string,
) (map[string]map[string]OptimizationLiveObservation, error) {
	result := make(map[string]map[string]OptimizationLiveObservation, len(deviceIDs))
	missing := make([]string, 0, len(deviceIDs))

	if r.deviceCache != nil {
		if snapshots, cacheErr := r.deviceCache.BatchGetForDevices(ctx, tenantID, deviceIDs); cacheErr == nil {
			for _, devID := range deviceIDs {
				snapshot, hit := snapshots[devID]
				if !hit || !strings.EqualFold(strings.TrimSpace(string(snapshot.SiteId)), siteID) {
					missing = append(missing, devID)
					continue
				}
				result[devID] = snapshotToDeviceLiveObservations(devID, snapshot)
			}
		} else {
			missing = append(missing, deviceIDs...)
		}
	} else {
		missing = append(missing, deviceIDs...)
	}

	if len(missing) > 0 {
		if devStore, ok := r.store.(DeviceLiveObservationPostgresStore); ok {
			loaded, err := devStore.ReadLatestDeviceObservations(ctx, tenantID, siteID, missing)
			if err != nil {
				return nil, err
			}
			for devID, entries := range loaded {
				lowerID := strings.ToLower(strings.TrimSpace(devID))
				if lowerID == "" {
					continue
				}
				result[lowerID] = entries
			}
		}
	}
	return result, nil
}

func snapshotToDeviceLiveObservations(deviceID string, snapshot telemetryapi.DeviceObservationSnapshot) map[string]OptimizationLiveObservation {
	out := make(map[string]OptimizationLiveObservation, len(snapshot.Values))
	for _, entry := range snapshot.Values {
		if entry.Present == nil {
			continue
		}
		present := entry.Present
		key := strings.TrimSpace(string(present.Key))
		if key == "" {
			continue
		}
		obs := OptimizationLiveObservation{
			DeviceID:       deviceID,
			TelemetryKey:   key,
			ValueType:      present.ValueType,
			Quality:        string(present.Quality),
			QualityReasons: make([]string, 0, len(present.QualityReasons)),
			SampledAt:      string(present.SampledAt),
			ReceivedAt:     string(present.ReceivedAt),
		}
		if present.Unit != nil {
			obs.Unit = *present.Unit
		}
		for _, reason := range present.QualityReasons {
			obs.QualityReasons = append(obs.QualityReasons, string(reason))
		}
		if present.Health != nil {
			obs.HealthStatus = present.Health.Status
			obs.HealthReasons = make([]string, 0, len(present.Health.Reasons))
			for _, reason := range present.Health.Reasons {
				obs.HealthReasons = append(obs.HealthReasons, string(reason))
			}
		}
		if !decodeDeviceLiveValue(present.Value, present.ValueType, &obs) {
			continue
		}
		out[key] = obs
	}
	return out
}

func decodeDeviceLiveValue(raw []byte, valueType string, obs *OptimizationLiveObservation) bool {
	if obs == nil || len(raw) == 0 {
		return false
	}
	switch strings.ToUpper(strings.TrimSpace(valueType)) {
	case "NUMBER":
		kind, number, valid := observationValueType(raw)
		if !valid || kind != "NUMBER" || math.IsNaN(number) || math.IsInf(number, 0) {
			return false
		}
		numCopy := number
		obs.ValueType = "NUMBER"
		obs.Value = number
		obs.ValueNumber = &numCopy
		return true
	case "STRING":
		var strVal string
		if err := json.Unmarshal(raw, &strVal); err != nil {
			return false
		}
		obs.ValueType = "STRING"
		obs.ValueString = &strVal
		return true
	case "BOOLEAN":
		var boolVal bool
		if err := json.Unmarshal(raw, &boolVal); err != nil {
			return false
		}
		obs.ValueType = "BOOLEAN"
		obs.ValueBoolean = &boolVal
		return true
	default:
		return false
	}
}

func normalizeSiteDeviceIDs(deviceIDs []string) ([]string, error) {
	if len(deviceIDs) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(deviceIDs))
	clean := make([]string, 0, len(deviceIDs))
	for _, raw := range deviceIDs {
		id := strings.ToLower(strings.TrimSpace(raw))
		if !uuidPattern.MatchString(id) {
			return nil, errors.New("every deviceId in optimization live observation requests must be a UUID")
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		clean = append(clean, id)
	}
	return clean, nil
}

// ReadLatestDeviceObservations resolves device-scoped latest observations directly from
// telemetry_runtime.latest_accepted_telemetry when Redis does not hold a warm device snapshot.
func (store *PostgresStore) ReadLatestDeviceObservations(ctx context.Context, tenantID, siteID string, deviceIDs []string) (map[string]map[string]OptimizationLiveObservation, error) {
	if store == nil || store.pool == nil {
		return nil, errors.New("telemetry store pool is not configured")
	}
	cleanTenant := strings.ToLower(strings.TrimSpace(tenantID))
	cleanSite := strings.ToLower(strings.TrimSpace(siteID))
	if !uuidPattern.MatchString(cleanTenant) || !uuidPattern.MatchString(cleanSite) {
		return nil, errors.New("telemetry optimization live observation scope is invalid")
	}
	cleanDeviceIDs, err := normalizeSiteDeviceIDs(deviceIDs)
	if err != nil {
		return nil, err
	}
	if len(cleanDeviceIDs) == 0 {
		return map[string]map[string]OptimizationLiveObservation{}, nil
	}

	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE s2_telemetry_runtime`); err != nil {
		return nil, err
	}

	rows, err := tx.Query(ctx, `
SELECT
  lat.device_id::text,
  COALESCE(d.source_id, ''),
  COALESCE(p.point_id::text, ''),
  lat.telemetry_key,
  COALESCE(p.point_revision, 0),
  lat.value,
  lat.value_type,
  COALESCE(lat.unit, p.unit, ''),
  lat.sampled_at,
  lat.received_at,
  lat.quality,
  COALESCE(lat.quality_reasons, '{}'),
  COALESCE(lat.health_status, ''),
  COALESCE(lat.health_reasons, '{}'),
  COALESCE(lat.health_at, lat.sampled_at)
FROM telemetry_runtime.latest_accepted_telemetry lat
JOIN telemetry_runtime.devices d
  ON d.device_id = lat.device_id
LEFT JOIN telemetry_runtime.points p
  ON p.device_id = lat.device_id
 AND p.telemetry_key = lat.telemetry_key
 AND p.tenant_id = $1::uuid
 AND p.site_id = $2::uuid
WHERE d.tenant_id = $1::uuid
  AND d.site_id = $2::uuid
  AND lat.device_id = ANY($3::uuid[])
ORDER BY lat.device_id, lat.telemetry_key`, cleanTenant, cleanSite, cleanDeviceIDs)
	if err != nil {
		return nil, err
	}

	out := make(map[string]map[string]OptimizationLiveObservation, len(cleanDeviceIDs))
	for rows.Next() {
		var (
			deviceID       string
			sourceID       string
			pointID        string
			telemetryKey   string
			pointRevision  int64
			rawValue       []byte
			valueType      string
			unit           string
			sampledAt      time.Time
			receivedAt     time.Time
			quality        string
			qualityReasons []string
			healthStatus   string
			healthReasons  []string
			healthAt       time.Time
		)
		if scanErr := rows.Scan(
			&deviceID, &sourceID, &pointID, &telemetryKey, &pointRevision,
			&rawValue, &valueType, &unit, &sampledAt, &receivedAt,
			&quality, &qualityReasons, &healthStatus, &healthReasons, &healthAt,
		); scanErr != nil {
			rows.Close()
			return nil, scanErr
		}
		if qualityReasons == nil {
			qualityReasons = []string{}
		}
		if healthReasons == nil {
			healthReasons = []string{}
		}
		lowerDev := strings.ToLower(strings.TrimSpace(deviceID))
		var rev uint64
		if pointRevision > 0 {
			rev = uint64(pointRevision)
		}
		obs := OptimizationLiveObservation{
			DeviceID:       lowerDev,
			SourceID:       strings.TrimSpace(sourceID),
			PointID:        strings.ToLower(strings.TrimSpace(pointID)),
			TelemetryKey:   telemetryKey,
			PointRevision:  rev,
			SampledAt:      sampledAt.UTC().Format(time.RFC3339Nano),
			ReceivedAt:     receivedAt.UTC().Format(time.RFC3339Nano),
			HealthAt:       healthAt.UTC().Format(time.RFC3339Nano),
			Unit:           unit,
			Quality:        quality,
			QualityReasons: qualityReasons,
			HealthStatus:   healthStatus,
			HealthReasons:  healthReasons,
		}
		if !decodeDeviceLiveValue(rawValue, valueType, &obs) {
			continue
		}
		byKey := out[lowerDev]
		if byKey == nil {
			byKey = make(map[string]OptimizationLiveObservation)
			out[lowerDev] = byKey
		}
		byKey[telemetryKey] = obs
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		rows.Close()
		return nil, rowsErr
	}
	rows.Close()

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

func (h *handler) handleOptimizationLiveObservations(writer http.ResponseWriter, request *http.Request) {
	if !h.authenticateOptimizationServicePost(writer, request, "The calling workload identity is not trusted for optimization live observation reads.") {
		return
	}
	if h.liveObservationReader == nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "TELEMETRY_LIVE_OBSERVATIONS_UNAVAILABLE", "Optimization live observation reader is not configured.", true)
		return
	}
	req, ok := decodeOptimizationLiveObservationsRequest(writer, request)
	if !ok {
		return
	}
	result, err := h.liveObservationReader.ReadOptimizationLiveObservations(request.Context(), req)
	if err != nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "TELEMETRY_LIVE_OBSERVATIONS_UNAVAILABLE", "Optimization live observations are temporarily unavailable.", true)
		return
	}
	if result.Observations == nil {
		result.Observations = map[string]OptimizationLiveObservation{}
	}
	writeJSON(writer, http.StatusOK, result)
}

func decodeOptimizationLiveObservationsRequest(writer http.ResponseWriter, request *http.Request) (OptimizationLiveObservationsRequest, bool) {
	var req OptimizationLiveObservationsRequest
	if !decodeOptimizationJSONRequest(
		writer,
		request,
		&req,
		maximumOptimizationLiveRequestSize,
		"TELEMETRY_LIVE_OBSERVATIONS_REQUEST_INVALID",
		"The optimization live observations request must use application/json.",
		"The optimization live observations request is malformed.",
	) {
		return OptimizationLiveObservationsRequest{}, false
	}
	req.TenantID = strings.ToLower(strings.TrimSpace(req.TenantID))
	req.SiteID = strings.ToLower(strings.TrimSpace(req.SiteID))
	if !uuidPattern.MatchString(req.TenantID) || !uuidPattern.MatchString(req.SiteID) {
		writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_LIVE_OBSERVATIONS_REQUEST_INVALID", "The optimization live observations tenantId and siteId must be UUIDs.", false)
		return OptimizationLiveObservationsRequest{}, false
	}
	if len(req.TelemetryKeys) > maximumOptimizationLiveScopeKeys || len(req.DeviceIDs) > maximumOptimizationLiveScopeKeys {
		writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_LIVE_OBSERVATIONS_REQUEST_INVALID", "The optimization live observations request exceeds maximum scope size.", false)
		return OptimizationLiveObservationsRequest{}, false
	}
	for _, raw := range req.TelemetryKeys {
		clean := strings.TrimSpace(raw)
		if clean == "" || len(clean) > 256 {
			writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_LIVE_OBSERVATIONS_REQUEST_INVALID", "Every telemetryKey in optimization live observation requests must be non-empty.", false)
			return OptimizationLiveObservationsRequest{}, false
		}
	}
	cleanDeviceIDs, err := normalizeSiteDeviceIDs(req.DeviceIDs)
	if err != nil {
		writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_LIVE_OBSERVATIONS_REQUEST_INVALID", "Every deviceId in optimization live observation requests must be a UUID.", false)
		return OptimizationLiveObservationsRequest{}, false
	}
	req.TelemetryKeys = normalizeUniqueStrings(req.TelemetryKeys)
	req.DeviceIDs = cleanDeviceIDs
	return req, true
}

var (
	_ LiveObservationPostgresStore       = (*PostgresStore)(nil)
	_ DeviceLiveObservationPostgresStore = (*PostgresStore)(nil)
)
