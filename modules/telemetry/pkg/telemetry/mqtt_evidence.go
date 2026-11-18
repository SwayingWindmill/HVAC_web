package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/modules/telemetry/pkg/telemetryapi"
)

const maximumRuntimeEventFutureSkew = 60 * time.Second

var ErrRuntimeEventInvalid = errors.New("MQTT runtime event is invalid")

// RuntimeEventEvidence carries the Tenant, Site, and optional Device/Coverage scope
// resolved by Connectivity for a gateway or device MQTT runtime event.
type RuntimeEventEvidence struct {
	TenantID          string          `json:"tenantId"`
	SiteID            string          `json:"siteId"`
	GatewayID         string          `json:"gatewayId"`
	MessageID         string          `json:"messageId"`
	Sequence          int64           `json:"sequence"`
	EventType         string          `json:"eventType"`
	SourceType        string          `json:"sourceType"`
	SourceID          string          `json:"sourceId"`
	EventTime         time.Time       `json:"eventTime"`
	Severity          string          `json:"severity"`
	Data              json.RawMessage `json:"data"`
	ReceivedAt        time.Time       `json:"receivedAt"`
	Device            *ResolvedDevice `json:"device,omitempty"`
	CoverageDeviceIDs []string        `json:"coverageDeviceIds,omitempty"`
	TraceID           string          `json:"traceId,omitempty"`
}

type MQTTEvidenceAcceptor interface {
	AcceptRuntimeEvent(context.Context, RuntimeEventEvidence) error
}

func (store *PostgresStore) AcceptRuntimeEvent(ctx context.Context, evidence RuntimeEventEvidence) error {
	if store == nil || store.pool == nil {
		return errors.New("telemetry runtime store is closed")
	}
	normalized, err := validateAndNormalizeRuntimeEventEvidence(evidence)
	if err != nil {
		return err
	}
	for attempt := 0; attempt < store.retryAttempts(); attempt++ {
		err := store.acceptRuntimeEventOnce(ctx, normalized)
		if err == nil || !retryableTelemetryTransaction(err) {
			return err
		}
	}
	return errors.New("MQTT runtime event transaction retry budget exhausted")
}

func (store *PostgresStore) acceptRuntimeEventOnce(ctx context.Context, evidence RuntimeEventEvidence) error {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return fmt.Errorf("begin MQTT runtime event transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `SET LOCAL ROLE s2_telemetry_runtime`); err != nil {
		return fmt.Errorf("activate telemetry runtime database identity: %w", err)
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		"mqtt-gateway:"+evidence.TenantID+":"+evidence.GatewayID); err != nil {
		return fmt.Errorf("lock gateway MQTT runtime event stream: %w", err)
	}
	tag, err := tx.Exec(ctx, `INSERT INTO telemetry_runtime.mqtt_runtime_events(
message_id,tenant_id,site_id,gateway_id,event_type,source_type,source_id,event_time,severity,source_sequence,data,received_at,created_at)
VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7,$8,$9,$10,$11::jsonb,$12,$12)
ON CONFLICT (message_id) DO NOTHING`, evidence.MessageID, evidence.TenantID, evidence.SiteID, evidence.GatewayID,
		evidence.EventType, evidence.SourceType, evidence.SourceID, evidence.EventTime, evidence.Severity, evidence.Sequence, evidence.Data, evidence.ReceivedAt)
	if err != nil {
		return fmt.Errorf("persist MQTT runtime event: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return tx.Commit(ctx)
	}
	if evidence.EventTime.After(evidence.ReceivedAt.Add(maximumRuntimeEventFutureSkew)) {
		return tx.Commit(ctx)
	}

	signalType, hasSignal := classifyPresenceSignalType(evidence.EventType)
	if !hasSignal {
		return tx.Commit(ctx)
	}

	newerExists, err := hasNewerGatewayLifecycleEvent(ctx, tx, evidence)
	if err != nil {
		return err
	}
	if newerExists {
		return tx.Commit(ctx)
	}

	// observation_coverage represents Connectivity's server-side observation path,
	// whereas stale earlier-session events were already suppressed above against
	// mqtt_runtime_events.event_time. Using ReceivedAt preserves FIFO recovery order
	// when queued LWT and reconnect events drain after an adapter outage.
	coverageObservedAt := evidence.ReceivedAt.UTC()

	targetDeviceID := candidateTargetDeviceID(evidence)
	candidateLockIDs := collectRuntimeEventLockIDs(targetDeviceID, evidence.CoverageDeviceIDs)
	existingLockedIDs, err := findExistingTelemetryDevices(ctx, tx, candidateLockIDs)
	if err != nil {
		return err
	}
	if len(existingLockedIDs) == 0 {
		return tx.Commit(ctx)
	}
	for _, deviceID := range existingLockedIDs {
		if err := lockDevice(ctx, tx, deviceID); err != nil {
			return err
		}
	}
	existingSet := make(map[string]struct{}, len(existingLockedIDs))
	for _, deviceID := range existingLockedIDs {
		existingSet[deviceID] = struct{}{}
	}

	dirtySet := make(map[string]struct{}, len(existingLockedIDs))
	if targetDeviceID != "" {
		if _, exists := existingSet[targetDeviceID]; exists {
			recorded, err := applyRuntimePresenceSignal(ctx, tx, store, evidence, targetDeviceID, signalType)
			if err != nil {
				return err
			}
			if recorded {
				dirtySet[targetDeviceID] = struct{}{}
				if signalType == "EXPLICIT_CONNECT" {
					if _, err := applyObservationCoverage(
						ctx, tx, []string{targetDeviceID}, true, nil, coverageObservedAt, max(evidence.Sequence, 1),
					); err != nil {
						return err
					}
				}
			}
		}
	}

	// Gateway connectivity loss marks downstream devices UNAVAILABLE via
	// observation_coverage rather than writing EXPLICIT_DISCONNECT presence signals
	// onto child devices (ADR 0002).
	if len(evidence.CoverageDeviceIDs) > 0 {
		existingCoverageIDs := make([]string, 0, len(evidence.CoverageDeviceIDs))
		for _, deviceID := range evidence.CoverageDeviceIDs {
			if deviceID == targetDeviceID {
				continue
			}
			if _, exists := existingSet[deviceID]; exists {
				existingCoverageIDs = append(existingCoverageIDs, deviceID)
			}
		}
		if len(existingCoverageIDs) > 0 {
			available := signalType == "EXPLICIT_CONNECT"
			var reasonCode *string
			if !available {
				code := string(telemetryapi.AvailabilityReasonCodeSourceUnavailable)
				reasonCode = &code
			}
			changedIDs, err := applyObservationCoverage(
				ctx, tx, existingCoverageIDs, available, reasonCode, coverageObservedAt, max(evidence.Sequence, 1),
			)
			if err != nil {
				return err
			}
			for _, deviceID := range changedIDs {
				dirtySet[deviceID] = struct{}{}
			}
		}
	}

	if len(dirtySet) > 0 {
		dirtyIDs := make([]string, 0, len(dirtySet))
		for deviceID := range dirtySet {
			dirtyIDs = append(dirtyIDs, deviceID)
		}
		slices.Sort(dirtyIDs)
		for _, deviceID := range dirtyIDs {
			if _, err := store.evaluateAndPersistDevice(ctx, tx, deviceID, nil, evidence.ReceivedAt); err != nil {
				return err
			}
		}
	}
	return tx.Commit(ctx)
}

func candidateTargetDeviceID(evidence RuntimeEventEvidence) string {
	if evidence.Device != nil {
		return evidence.Device.DeviceID
	}
	sourceType := strings.ToUpper(strings.TrimSpace(evidence.SourceType))
	sourceID := strings.ToLower(strings.TrimSpace(evidence.SourceID))
	if !uuidV7Pattern.MatchString(sourceID) {
		return ""
	}
	if sourceType == "GATEWAY" && sourceID != strings.ToLower(strings.TrimSpace(evidence.GatewayID)) {
		return ""
	}
	if sourceType != "GATEWAY" && sourceType != "DEVICE" {
		return ""
	}
	return sourceID
}

func collectRuntimeEventLockIDs(targetDeviceID string, coverageDeviceIDs []string) []string {
	total := len(coverageDeviceIDs)
	if targetDeviceID != "" {
		total++
	}
	if total == 0 {
		return nil
	}
	seen := make(map[string]struct{}, total)
	ids := make([]string, 0, total)
	if targetDeviceID != "" {
		seen[targetDeviceID] = struct{}{}
		ids = append(ids, targetDeviceID)
	}
	for _, deviceID := range coverageDeviceIDs {
		if _, dup := seen[deviceID]; dup {
			continue
		}
		seen[deviceID] = struct{}{}
		ids = append(ids, deviceID)
	}
	slices.Sort(ids)
	return ids
}

func applyRuntimePresenceSignal(
	ctx context.Context,
	tx pgx.Tx,
	store *PostgresStore,
	evidence RuntimeEventEvidence,
	targetDeviceID string,
	signalType string,
) (bool, error) {
	observedAt := evidence.EventTime.UTC()
	receivedAt := evidence.ReceivedAt.UTC()
	// DB constraint requires received_at >= observed_at - 24h on presence_signals.
	if receivedAt.Before(observedAt.Add(-24 * time.Hour)) {
		return false, nil
	}
	var tenantID, siteID string
	err := tx.QueryRow(ctx, `
SELECT tenant_id::text, site_id::text
FROM telemetry_runtime.devices
WHERE device_id = $1::uuid
`, targetDeviceID).Scan(&tenantID, &siteID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("verify runtime event Device scope: %w", err)
	}
	if tenantID != evidence.TenantID || siteID != evidence.SiteID {
		if evidence.Device != nil {
			return false, fmt.Errorf("%w: runtime event device scope mismatch", ErrRuntimeEventInvalid)
		}
		return false, nil
	}

	var policyRevision int64
	var acceptedSignalTypes []string
	var maxFutureSkewSeconds int
	err = tx.QueryRow(ctx, `
SELECT policy_revision, accepted_signal_types, max_future_clock_skew_seconds
FROM telemetry_runtime.presence_policies
WHERE device_id = $1::uuid
`, targetDeviceID).Scan(&policyRevision, &acceptedSignalTypes, &maxFutureSkewSeconds)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read telemetry Presence Policy for runtime event: %w", err)
	}
	if policyRevision < 1 {
		return false, nil
	}
	withinSkew := maxFutureSkewSeconds <= 0 || !observedAt.After(receivedAt.Add(time.Duration(maxFutureSkewSeconds)*time.Second))
	accepted := withinSkew && signalTypeAllowed(signalType, acceptedSignalTypes)

	signalObservedAt := observedAt
	if isLWTEventType(evidence.EventType) {
		var maxObservedAt *time.Time
		err = tx.QueryRow(ctx, `
SELECT MAX(observed_at)
FROM telemetry_runtime.presence_signals
WHERE device_id = $1::uuid AND accepted
`, targetDeviceID).Scan(&maxObservedAt)
		if err != nil {
			return false, fmt.Errorf("read latest telemetry Presence Signal for LWT: %w", err)
		}
		if maxObservedAt != nil && !maxObservedAt.Before(signalObservedAt) {
			advanced := maxObservedAt.UTC().Add(time.Microsecond)
			if advanced.After(receivedAt) {
				advanced = receivedAt
			}
			signalObservedAt = advanced
		}
	}

	signalID, err := store.newEventID(receivedAt)
	if err != nil {
		return false, fmt.Errorf("generate MQTT runtime Presence Signal ID: %w", err)
	}
	var insertedID string
	err = tx.QueryRow(ctx, `
INSERT INTO telemetry_runtime.presence_signals (
  signal_id, device_id, signal_type, observed_at, received_at, accepted,
  policy_revision, source_event_id, created_at
) VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8::uuid, $5)
ON CONFLICT (device_id, source_event_id) DO NOTHING
RETURNING signal_id::text
`, signalID, targetDeviceID, signalType, signalObservedAt, receivedAt, accepted, policyRevision, evidence.MessageID).Scan(&insertedID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("persist MQTT runtime Presence Signal: %w", err)
	}
	return accepted, nil
}

var (
	lifecycleConnectEventTypes    = []string{"GATEWAY_CONNECTED", "DEVICE_CONNECTED", "CONNECTED", "CONNECT", "ONLINE", "SESSION_CONNECTED", "EXPLICIT_CONNECT"}
	lifecycleDisconnectEventTypes = []string{"GATEWAY_DISCONNECTED", "DEVICE_DISCONNECTED", "DISCONNECTED", "DISCONNECT", "OFFLINE", "SESSION_DISCONNECTED", "LWT", "LAST_WILL", "EXPLICIT_DISCONNECT"}
	allLifecycleEventTypes        = append(slices.Clone(lifecycleConnectEventTypes), lifecycleDisconnectEventTypes...)
)

func isLWTEventType(eventType string) bool {
	switch strings.ToUpper(strings.TrimSpace(eventType)) {
	case "LWT", "LAST_WILL":
		return true
	default:
		return false
	}
}

func extractEventSessionID(data json.RawMessage) string {
	var fields struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(data, &fields); err != nil {
		return ""
	}
	return strings.TrimSpace(fields.SessionID)
}

func hasNewerGatewayLifecycleEvent(ctx context.Context, tx pgx.Tx, evidence RuntimeEventEvidence) (bool, error) {
	sessionID := extractEventSessionID(evidence.Data)
	eventTypes := allLifecycleEventTypes
	checkTie := false
	if isLWTEventType(evidence.EventType) {
		eventTypes = lifecycleConnectEventTypes
		checkTie = true
	}
	var newer bool
	err := tx.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1
  FROM telemetry_runtime.mqtt_runtime_events
  WHERE tenant_id = $1::uuid
    AND site_id = $2::uuid
    AND gateway_id = $3
    AND UPPER(TRIM(source_type)) = UPPER(TRIM($4))
    AND TRIM(source_id) = TRIM($5)
    AND message_id <> $6::uuid
    AND UPPER(TRIM(event_type)) = ANY($7::text[])
    AND (
      (event_time > $8 AND ($9 = '' OR COALESCE(data ->> 'sessionId', '') <> $9))
      OR ($10 AND $9 <> '' AND COALESCE(data ->> 'sessionId', '') <> '' AND COALESCE(data ->> 'sessionId', '') <> $9 AND event_time >= $8)
    )
)
`, evidence.TenantID, evidence.SiteID, evidence.GatewayID, evidence.SourceType, evidence.SourceID,
		evidence.MessageID, eventTypes, evidence.EventTime, sessionID, checkTie).Scan(&newer)
	if err != nil {
		return false, fmt.Errorf("check newer gateway lifecycle events: %w", err)
	}
	return newer, nil
}

func classifyPresenceSignalType(eventType string) (string, bool) {
	switch strings.ToUpper(strings.TrimSpace(eventType)) {
	case "EXPLICIT_CONNECT", "CONNECTED", "CONNECT", "GATEWAY_CONNECTED", "DEVICE_CONNECTED", "ONLINE", "SESSION_CONNECTED":
		return "EXPLICIT_CONNECT", true
	case "EXPLICIT_DISCONNECT", "DISCONNECTED", "DISCONNECT", "GATEWAY_DISCONNECTED", "DEVICE_DISCONNECTED", "LWT", "LAST_WILL", "OFFLINE", "SESSION_DISCONNECTED":
		return "EXPLICIT_DISCONNECT", true
	default:
		return "", false
	}
}

func validateAndNormalizeRuntimeEventEvidence(evidence RuntimeEventEvidence) (RuntimeEventEvidence, error) {
	tenantID := strings.ToLower(strings.TrimSpace(evidence.TenantID))
	siteID := strings.ToLower(strings.TrimSpace(evidence.SiteID))
	messageID := strings.ToLower(strings.TrimSpace(evidence.MessageID))
	gatewayID := strings.TrimSpace(evidence.GatewayID)
	eventType := strings.TrimSpace(evidence.EventType)
	sourceType := strings.TrimSpace(evidence.SourceType)
	sourceID := strings.TrimSpace(evidence.SourceID)
	severity := strings.ToUpper(strings.TrimSpace(evidence.Severity))

	if !uuidV7Pattern.MatchString(tenantID) || !uuidV7Pattern.MatchString(siteID) || !uuidV7Pattern.MatchString(messageID) {
		return RuntimeEventEvidence{}, fmt.Errorf("%w: scope is invalid", ErrRuntimeEventInvalid)
	}
	if evidence.Sequence < 0 || gatewayID == "" || eventType == "" || sourceType == "" || sourceID == "" || evidence.EventTime.IsZero() || evidence.ReceivedAt.IsZero() || !json.Valid(evidence.Data) {
		return RuntimeEventEvidence{}, fmt.Errorf("%w: required fields are invalid", ErrRuntimeEventInvalid)
	}
	if severity != "INFO" && severity != "WARNING" && severity != "CRITICAL" {
		return RuntimeEventEvidence{}, fmt.Errorf("%w: severity is invalid", ErrRuntimeEventInvalid)
	}

	var normalizedDevice *ResolvedDevice
	if evidence.Device != nil {
		devTenant := strings.ToLower(strings.TrimSpace(evidence.Device.TenantID))
		devSite := strings.ToLower(strings.TrimSpace(evidence.Device.SiteID))
		devID := strings.ToLower(strings.TrimSpace(evidence.Device.DeviceID))
		if !uuidV7Pattern.MatchString(devTenant) || !uuidV7Pattern.MatchString(devSite) || !uuidV7Pattern.MatchString(devID) {
			return RuntimeEventEvidence{}, fmt.Errorf("%w: device scope must be UUIDv7", ErrRuntimeEventInvalid)
		}
		if devTenant != tenantID || devSite != siteID {
			return RuntimeEventEvidence{}, fmt.Errorf("%w: device tenant or site mismatches event scope", ErrRuntimeEventInvalid)
		}
		normalizedDevice = &ResolvedDevice{
			TenantID: devTenant,
			SiteID:   devSite,
			DeviceID: devID,
		}
	}

	if len(evidence.CoverageDeviceIDs) > maximumCoverageDeviceIDs {
		return RuntimeEventEvidence{}, fmt.Errorf("%w: coverageDeviceIds exceeds %d", ErrRuntimeEventInvalid, maximumCoverageDeviceIDs)
	}
	var coverageIDs []string
	if len(evidence.CoverageDeviceIDs) > 0 {
		var err error
		coverageIDs, err = canonicalDeviceIDs(evidence.CoverageDeviceIDs)
		if err != nil {
			return RuntimeEventEvidence{}, fmt.Errorf("%w: %v", ErrRuntimeEventInvalid, err)
		}
		if normalizedDevice != nil {
			filtered := make([]string, 0, len(coverageIDs))
			for _, id := range coverageIDs {
				if id == normalizedDevice.DeviceID {
					continue
				}
				filtered = append(filtered, id)
			}
			coverageIDs = filtered
		}
	}

	return RuntimeEventEvidence{
		TenantID:          tenantID,
		SiteID:            siteID,
		GatewayID:         gatewayID,
		MessageID:         messageID,
		Sequence:          evidence.Sequence,
		EventType:         eventType,
		SourceType:        sourceType,
		SourceID:          sourceID,
		EventTime:         evidence.EventTime.UTC(),
		Severity:          severity,
		Data:              append(json.RawMessage(nil), evidence.Data...),
		ReceivedAt:        evidence.ReceivedAt.UTC(),
		Device:            normalizedDevice,
		CoverageDeviceIDs: coverageIDs,
		TraceID:           normalizeTraceID(evidence.TraceID),
	}, nil
}
