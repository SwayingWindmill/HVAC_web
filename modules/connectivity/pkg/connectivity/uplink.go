package connectivity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/adapter"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/packettap"
)

const (
	quarantineDedupWindow      = 5 * time.Minute
	quarantineCooldownDuration = 5 * time.Second
	quarantineCooldownCap      = 1024
	maxQuarantineGatewayRunes  = 128
	maxQuarantineTopicRunes    = 512
	maxQuarantineDetailRunes   = 1024
)

type quarantineDedupKey struct {
	tenantID      string
	gatewayID     string
	reasonCode    string
	topic         string
	payloadSHA256 string
}

type quarantineCooldownEntry struct {
	cooldownUntil    time.Time
	pendingDelta     int64
	lastSeenAt       time.Time
	detail           string
	payloadBytes     int
	payloadPreview   string
	payloadEncoding  string
	payloadTruncated bool
	seq              uint64
	inFlight         chan struct{}
}

type quarantineBurstCache struct {
	mu      sync.Mutex
	seq     uint64
	entries map[quarantineDedupKey]*quarantineCooldownEntry
}

type quarantinePersistInput struct {
	tenantID         string
	gatewayID        string
	topic            string
	reasonCode       string
	detail           string
	payloadSHA256    string
	payloadBytes     int
	payloadPreview   string
	payloadEncoding  string
	payloadTruncated bool
	delta            int64
	receivedAt       time.Time
}

type quarantinePersistFn func(ctx context.Context, input quarantinePersistInput) error

func (cache *quarantineBurstCache) evictIfNeededLocked() {
	if len(cache.entries) < quarantineCooldownCap {
		return
	}
	var victimZero quarantineDedupKey
	var victimZeroSeq uint64
	hasZero := false

	var victimAny quarantineDedupKey
	var victimAnySeq uint64
	hasAny := false

	for k, entry := range cache.entries {
		if entry.inFlight != nil {
			continue
		}
		if entry.pendingDelta == 0 {
			if !hasZero || entry.seq < victimZeroSeq {
				victimZero = k
				victimZeroSeq = entry.seq
				hasZero = true
			}
		}
		if !hasAny || entry.seq < victimAnySeq {
			victimAny = k
			victimAnySeq = entry.seq
			hasAny = true
		}
	}
	if hasZero {
		delete(cache.entries, victimZero)
	} else if hasAny {
		delete(cache.entries, victimAny)
	}
}

// ResolveGateway finds a Gateway in the Registry gateway directory, which every Tenant
// can read, and checks that it holds an active credential. The Gateway is returned with
// ErrGatewayCredentialInactive so quarantine evidence can carry its Tenant.
func (store *Store) ResolveGateway(ctx context.Context, gatewayID string) (adapter.Gateway, error) {
	return store.resolveGatewayCached(ctx, gatewayID)
}

// ResolveDevice returns the Device a Gateway names by a source key, or "" when that
// source key is not registered behind the Gateway.
func (store *Store) ResolveDevice(ctx context.Context, gateway adapter.Gateway, sourceKey string) (string, error) {
	return store.resolveDeviceCached(ctx, gateway, sourceKey, store.resolveDeviceUncached)
}

func matchDirectDeviceSourceKey(gateway adapter.Gateway, sourceKey string) bool {
	if !gateway.DirectDevice {
		return false
	}
	trimmed := strings.TrimSpace(sourceKey)
	if trimmed == "" {
		return false
	}
	return strings.EqualFold(trimmed, gateway.ID) ||
		strings.EqualFold(trimmed, "self") ||
		(gateway.DeviceCode != "" && trimmed == gateway.DeviceCode)
}

func (store *Store) resolveDeviceUncached(ctx context.Context, gateway adapter.Gateway, sourceKey string) (string, error) {
	if gateway.DirectDevice {
		if matchDirectDeviceSourceKey(gateway, sourceKey) {
			return gateway.ID, nil
		}
		return "", nil
	}
	var deviceID string
	err := store.readAsTenant(ctx, gateway.TenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
SELECT device_id::text
FROM core_registry.gateway_device_source_keys_v1
WHERE gateway_id = $1::uuid AND source_key = $2
`, gateway.ID, sourceKey).Scan(&deviceID)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("resolve Registry Device source key: %w", err)
	}
	return deviceID, nil
}

// ResolveBindings resolves all Gateway source keys and their active Point bindings
// within one read-only Tenant transaction, backed by the short-TTL uplink metadata cache.
func (store *Store) ResolveBindings(ctx context.Context, gateway adapter.Gateway, queries []adapter.DevicePointQuery) (adapter.ResolvedBindings, error) {
	return store.resolveBindingsCached(ctx, gateway, queries)
}

func (store *Store) resolveBindingsUncached(
	ctx context.Context,
	gateway adapter.Gateway,
	missingSourceKeys []string,
	cachedPointQueries []resolvedPointTarget,
	allQueries []adapter.DevicePointQuery,
) (adapter.ResolvedBindings, []resolvedPointTarget, error) {
	result := adapter.ResolvedBindings{
		Devices: make(map[string]string),
		Points:  make(map[string]*adapter.ResolvedPoint),
	}
	var resolvedTargets []resolvedPointTarget
	err := store.readAsTenant(ctx, gateway.TenantID, func(tx pgx.Tx) error {
		if len(missingSourceKeys) > 0 {
			if gateway.DirectDevice {
				for _, sk := range missingSourceKeys {
					if matchDirectDeviceSourceKey(gateway, sk) {
						result.Devices[sk] = gateway.ID
					}
				}
			} else if _, err := queryGatewayDevices(ctx, tx, gateway.ID, missingSourceKeys, result.Devices); err != nil {
				return err
			}
		}
		targetSet := make(map[resolvedPointTarget]struct{}, len(cachedPointQueries)+len(allQueries))
		deviceSet := make(map[string]struct{})
		pointCodeSet := make(map[string]struct{})
		var deviceIDs []string
		var pointCodes []string
		addTarget := func(target resolvedPointTarget) {
			if target.deviceID == "" || target.pointCode == "" {
				return
			}
			if _, seen := targetSet[target]; seen {
				return
			}
			targetSet[target] = struct{}{}
			resolvedTargets = append(resolvedTargets, target)
			if _, ok := deviceSet[target.deviceID]; !ok {
				deviceSet[target.deviceID] = struct{}{}
				deviceIDs = append(deviceIDs, target.deviceID)
			}
			if _, ok := pointCodeSet[target.pointCode]; !ok {
				pointCodeSet[target.pointCode] = struct{}{}
				pointCodes = append(pointCodes, target.pointCode)
			}
		}
		for _, target := range cachedPointQueries {
			addTarget(target)
		}
		for _, query := range allQueries {
			if query.PointCode == "" {
				continue
			}
			if deviceID := result.Devices[query.SourceKey]; deviceID != "" {
				addTarget(resolvedPointTarget{deviceID: deviceID, pointCode: query.PointCode})
			}
		}
		if len(deviceIDs) == 0 || len(pointCodes) == 0 {
			return nil
		}
		return queryDevicePointBindings(ctx, tx, deviceIDs, pointCodes, result.Points)
	})
	if err != nil {
		return adapter.ResolvedBindings{}, nil, err
	}
	return result, resolvedTargets, nil
}

func queryGatewayDevices(ctx context.Context, tx pgx.Tx, gatewayID string, sourceKeys []string, devices map[string]string) ([]string, error) {
	rows, err := tx.Query(ctx, `
SELECT source_key, device_id::text
FROM core_registry.gateway_device_source_keys_v1
WHERE gateway_id = $1::uuid AND source_key = ANY($2::text[])
`, gatewayID, sourceKeys)
	if err != nil {
		return nil, fmt.Errorf("resolve Registry Device source keys: %w", err)
	}
	defer rows.Close()
	deviceIDSet := make(map[string]struct{})
	var deviceIDs []string
	for rows.Next() {
		var sourceKey, deviceID string
		if err := rows.Scan(&sourceKey, &deviceID); err != nil {
			return nil, fmt.Errorf("scan Registry Device source key: %w", err)
		}
		devices[sourceKey] = deviceID
		if _, ok := deviceIDSet[deviceID]; !ok {
			deviceIDSet[deviceID] = struct{}{}
			deviceIDs = append(deviceIDs, deviceID)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Registry Device source keys: %w", err)
	}
	return deviceIDs, nil
}

func queryDevicePointBindings(ctx context.Context, tx pgx.Tx, deviceIDs, pointCodes []string, points map[string]*adapter.ResolvedPoint) error {
	rows, err := tx.Query(ctx, `
SELECT device_id::text, point_code, point_id::text, sensor_id::text, point_type, value_type, unit,
       counter_decrease_mode, counter_rollover_modulus, point_revision
FROM core_registry.point_bindings_v1
WHERE device_id = ANY($1::uuid[]) AND point_code = ANY($2::text[])
`, deviceIDs, pointCodes)
	if err != nil {
		return fmt.Errorf("resolve Registry Point bindings: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var deviceID, pointCode string
		var point adapter.ResolvedPoint
		if err := rows.Scan(
			&deviceID, &pointCode,
			&point.PointID, &point.SensorID, &point.PointType, &point.ValueType, &point.Unit,
			&point.CounterDecreaseMode, &point.CounterRolloverModulus, &point.PointRevision,
		); err != nil {
			return fmt.Errorf("scan Registry Point binding: %w", err)
		}
		points[adapter.PointBindingKey(deviceID, pointCode)] = &point
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate Registry Point bindings: %w", err)
	}
	return nil
}

func sanitizeQuarantineFields(quarantine adapter.UplinkQuarantine, now time.Time) (tenantID, gatewayID, topic, reasonCode, detail string, receivedAt time.Time) {
	tenantID = strings.TrimSpace(quarantine.TenantID)
	gatewayID = clampUTF8Runes(quarantine.GatewayID, maxQuarantineGatewayRunes)
	if gatewayID == "" {
		gatewayID = "invalid"
	}
	topic = clampUTF8Runes(quarantine.Topic, maxQuarantineTopicRunes)
	if topic == "" {
		topic = "(empty)"
	}
	reasonCode = strings.TrimSpace(quarantine.ReasonCode)
	detail = clampUTF8Runes(quarantine.Detail, maxQuarantineDetailRunes)
	if detail == "" {
		if reasonCode != "" {
			detail = reasonCode
		} else {
			detail = "quarantined"
		}
	}
	receivedAt = quarantine.ReceivedAt.UTC()
	if receivedAt.IsZero() {
		receivedAt = now.UTC()
	}
	return tenantID, gatewayID, topic, reasonCode, detail, receivedAt
}

func (store *Store) QuarantineUplink(ctx context.Context, quarantine adapter.UplinkQuarantine) error {
	if store == nil || store.pool == nil {
		return errors.New("connectivity store is closed")
	}
	return store.quarantineUplinkWith(ctx, quarantine, store.persistQuarantineDB)
}

func (store *Store) quarantineUplinkWith(ctx context.Context, quarantine adapter.UplinkQuarantine, persist quarantinePersistFn) error {
	now := store.clock().UTC()
	tenantID, gatewayID, topic, reasonCode, detail, receivedAt := sanitizeQuarantineFields(quarantine, now)
	preview, encoding, truncated, shaHex := packettap.FormatPayloadPreview(quarantine.Payload, packettap.MaxPayloadPreviewBytes)
	key := quarantineDedupKey{
		tenantID:      tenantID,
		gatewayID:     gatewayID,
		reasonCode:    reasonCode,
		topic:         topic,
		payloadSHA256: shaHex,
	}

	cache := &store.cache.quarantine
	for {
		cache.mu.Lock()
		if cache.entries == nil {
			cache.entries = make(map[quarantineDedupKey]*quarantineCooldownEntry)
		}
		entry, exists := cache.entries[key]
		if exists && receivedAt.Before(entry.cooldownUntil) {
			entry.pendingDelta++
			if receivedAt.After(entry.lastSeenAt) {
				entry.lastSeenAt = receivedAt
			}
			entry.detail = detail
			entry.payloadBytes = len(quarantine.Payload)
			entry.payloadPreview = preview
			entry.payloadEncoding = encoding
			entry.payloadTruncated = truncated
			cache.seq++
			entry.seq = cache.seq
			cache.mu.Unlock()
			return nil
		}
		if exists && entry.inFlight != nil {
			waitCh := entry.inFlight
			cache.mu.Unlock()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-waitCh:
				continue
			}
		}

		var carriedPending int64
		done := make(chan struct{})
		if exists {
			carriedPending = entry.pendingDelta
			entry.pendingDelta = 0
			entry.cooldownUntil = receivedAt.Add(quarantineCooldownDuration)
			if receivedAt.After(entry.lastSeenAt) {
				entry.lastSeenAt = receivedAt
			}
			entry.detail = detail
			entry.payloadBytes = len(quarantine.Payload)
			entry.payloadPreview = preview
			entry.payloadEncoding = encoding
			entry.payloadTruncated = truncated
			entry.inFlight = done
			cache.seq++
			entry.seq = cache.seq
		} else {
			cache.evictIfNeededLocked()
			cache.seq++
			entry = &quarantineCooldownEntry{
				cooldownUntil:    receivedAt.Add(quarantineCooldownDuration),
				lastSeenAt:       receivedAt,
				detail:           detail,
				payloadBytes:     len(quarantine.Payload),
				payloadPreview:   preview,
				payloadEncoding:  encoding,
				payloadTruncated: truncated,
				seq:              cache.seq,
				inFlight:         done,
			}
			cache.entries[key] = entry
		}
		cache.mu.Unlock()

		err := persist(ctx, quarantinePersistInput{
			tenantID:         tenantID,
			gatewayID:        gatewayID,
			topic:            topic,
			reasonCode:       reasonCode,
			detail:           detail,
			payloadSHA256:    shaHex,
			payloadBytes:     len(quarantine.Payload),
			payloadPreview:   preview,
			payloadEncoding:  encoding,
			payloadTruncated: truncated,
			delta:            1 + carriedPending,
			receivedAt:       receivedAt,
		})

		cache.mu.Lock()
		if current, ok := cache.entries[key]; ok && current == entry {
			if err != nil {
				current.pendingDelta += carriedPending
				current.cooldownUntil = time.Time{}
			}
			current.inFlight = nil
		}
		close(done)
		cache.mu.Unlock()
		return err
	}
}

func (store *Store) FlushQuarantineBursts(ctx context.Context) error {
	if store == nil || store.pool == nil {
		return errors.New("connectivity store is closed")
	}
	return store.flushQuarantineBurstsWith(ctx, "", store.persistQuarantineDB)
}

func (store *Store) flushQuarantineBurstsForGateway(ctx context.Context, gatewayID string) error {
	if store == nil || store.pool == nil {
		return errors.New("connectivity store is closed")
	}
	return store.flushQuarantineBurstsWith(ctx, strings.TrimSpace(gatewayID), store.persistQuarantineDB)
}

func (store *Store) flushQuarantineBurstsWith(ctx context.Context, gatewayFilter string, persist quarantinePersistFn) error {
	cache := &store.cache.quarantine
	for {
		cache.mu.Lock()
		if len(cache.entries) == 0 {
			cache.mu.Unlock()
			return nil
		}
		var targetKey quarantineDedupKey
		var targetEntry *quarantineCooldownEntry
		var waitCh chan struct{}
		for k, entry := range cache.entries {
			if gatewayFilter != "" && !strings.EqualFold(k.gatewayID, gatewayFilter) {
				continue
			}
			if entry.inFlight != nil {
				waitCh = entry.inFlight
				break
			}
			if entry.pendingDelta > 0 {
				targetKey = k
				targetEntry = entry
				break
			}
		}
		if waitCh != nil {
			cache.mu.Unlock()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-waitCh:
				continue
			}
		}
		if targetEntry == nil {
			cache.mu.Unlock()
			return nil
		}

		delta := targetEntry.pendingDelta
		targetEntry.pendingDelta = 0
		done := make(chan struct{})
		targetEntry.inFlight = done
		input := quarantinePersistInput{
			tenantID:         targetKey.tenantID,
			gatewayID:        targetKey.gatewayID,
			topic:            targetKey.topic,
			reasonCode:       targetKey.reasonCode,
			detail:           targetEntry.detail,
			payloadSHA256:    targetKey.payloadSHA256,
			payloadBytes:     targetEntry.payloadBytes,
			payloadPreview:   targetEntry.payloadPreview,
			payloadEncoding:  targetEntry.payloadEncoding,
			payloadTruncated: targetEntry.payloadTruncated,
			delta:            delta,
			receivedAt:       targetEntry.lastSeenAt,
		}
		cache.mu.Unlock()

		err := persist(ctx, input)

		cache.mu.Lock()
		if current, ok := cache.entries[targetKey]; ok && current == targetEntry {
			if err != nil {
				current.pendingDelta += delta
			}
			current.inFlight = nil
		}
		close(done)
		cache.mu.Unlock()

		if err != nil {
			return err
		}
	}
}

func (store *Store) persistQuarantineDB(ctx context.Context, input quarantinePersistInput) error {
	if store == nil || store.pool == nil {
		return errors.New("connectivity store is closed")
	}
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var tenantID any
	if input.tenantID != "" {
		tenantID = input.tenantID
		if err := setTenant(ctx, tx, input.tenantID); err != nil {
			return err
		}
	} else {
		if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', '', true)`); err != nil {
			return fmt.Errorf("clear Tenant context: %w", err)
		}
	}

	windowStart := input.receivedAt.Add(-quarantineDedupWindow)
	var existingID string
	err = tx.QueryRow(ctx, `
SELECT id::text
FROM connectivity.uplink_quarantine
WHERE tenant_id IS NOT DISTINCT FROM $1::uuid
  AND gateway_id = $2
  AND reason_code = $3
  AND topic = $4
  AND payload_sha256 = $5
  AND last_seen_at >= $6
ORDER BY last_seen_at DESC, id DESC
LIMIT 1
FOR UPDATE
`, tenantID, input.gatewayID, input.reasonCode, input.topic, input.payloadSHA256, windowStart).Scan(&existingID)
	switch {
	case err == nil:
		if _, err := tx.Exec(ctx, `
UPDATE connectivity.uplink_quarantine
SET occurrence_count = occurrence_count + $1,
    last_seen_at = GREATEST(last_seen_at, $2),
    detail = $3,
    payload_preview = $4,
    payload_encoding = $5,
    payload_truncated = $6
WHERE id = $7::uuid
`, input.delta, input.receivedAt, input.detail, input.payloadPreview, input.payloadEncoding, input.payloadTruncated, existingID); err != nil {
			return fmt.Errorf("update uplink quarantine dedup row: %w", err)
		}
	case errors.Is(err, pgx.ErrNoRows):
		id, idErr := newUUIDv7(input.receivedAt)
		if idErr != nil {
			return idErr
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO connectivity.uplink_quarantine (
  id, tenant_id, gateway_id, topic, reason_code, detail,
  payload_sha256, payload_bytes, payload_preview, payload_encoding, payload_truncated,
  occurrence_count, received_at, last_seen_at
) VALUES (
  $1::uuid, $2::uuid, $3, $4, $5, $6,
  $7, $8, $9, $10, $11,
  $12, $13, $13
)
`, id, tenantID, input.gatewayID, input.topic, input.reasonCode, input.detail,
			input.payloadSHA256, input.payloadBytes, input.payloadPreview, input.payloadEncoding, input.payloadTruncated,
			input.delta, input.receivedAt); err != nil {
			return fmt.Errorf("persist uplink quarantine: %w", err)
		}
	default:
		return fmt.Errorf("lookup uplink quarantine dedup row: %w", err)
	}

	return tx.Commit(ctx)
}

func (store *Store) readAsTenant(ctx context.Context, tenantID string, read func(pgx.Tx) error) error {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := setTenant(ctx, tx, tenantID); err != nil {
		return err
	}
	return read(tx)
}

func setTenant(ctx context.Context, tx pgx.Tx, tenantID string) error {
	if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true)`, tenantID); err != nil {
		return fmt.Errorf("set Tenant context: %w", err)
	}
	return nil
}
