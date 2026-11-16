package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrRuntimeEventInvalid = errors.New("MQTT runtime event is invalid")

// GatewayEvidence and RuntimeEventEvidence carry the Tenant and Site that Connectivity
// resolved for the Gateway; Telemetry trusts Connectivity's workload identity for them.
type RuntimeEventEvidence struct {
	TenantID   string          `json:"tenantId"`
	SiteID     string          `json:"siteId"`
	GatewayID  string          `json:"gatewayId"`
	MessageID  string          `json:"messageId"`
	Sequence   int64           `json:"sequence"`
	EventType  string          `json:"eventType"`
	SourceType string          `json:"sourceType"`
	SourceID   string          `json:"sourceId"`
	Device     *ResolvedDevice `json:"device,omitempty"`
	EventTime  time.Time       `json:"eventTime"`
	Severity   string          `json:"severity"`
	Data       json.RawMessage `json:"data"`
	ReceivedAt time.Time       `json:"receivedAt"`
}

type MQTTEvidenceAcceptor interface {
	AcceptRuntimeEvent(context.Context, RuntimeEventEvidence) error
}

func (store *PostgresStore) AcceptRuntimeEvent(ctx context.Context, evidence RuntimeEventEvidence) error {
	if store == nil || store.pool == nil {
		return errors.New("telemetry runtime store is closed")
	}
	if err := validateRuntimeEventEvidence(evidence); err != nil {
		return err
	}
	for attempt := 0; attempt < 3; attempt++ {
		err := store.acceptRuntimeEventOnce(ctx, evidence)
		if err == nil || !retryableTelemetryTransaction(err) {
			return err
		}
	}
	return errors.New("MQTT runtime event transaction retry budget exhausted")
}

func (store *PostgresStore) acceptRuntimeEventOnce(ctx context.Context, evidence RuntimeEventEvidence) error {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `SET LOCAL ROLE s2_telemetry_runtime`); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `INSERT INTO telemetry_runtime.mqtt_runtime_events(
message_id,tenant_id,site_id,gateway_id,event_type,source_type,source_id,event_time,severity,source_sequence,data,received_at,created_at)
VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7,$8,$9,$10,$11::jsonb,$12,$12)
ON CONFLICT (message_id) DO NOTHING`, evidence.MessageID, evidence.TenantID, evidence.SiteID, evidence.GatewayID,
		evidence.EventType, evidence.SourceType, evidence.SourceID, evidence.EventTime.UTC(), evidence.Severity, evidence.Sequence, evidence.Data, evidence.ReceivedAt.UTC())
	if err != nil {
		return fmt.Errorf("persist MQTT runtime event: %w", err)
	}
	if tag.RowsAffected() > 0 {
		if signalType, ok := classifyPresenceSignalType(evidence.EventType); ok {
			if err := store.applyPresenceRuntimeEvent(ctx, tx, evidence, signalType); err != nil {
				return err
			}
		}
	}
	return tx.Commit(ctx)
}

func (store *PostgresStore) applyPresenceRuntimeEvent(ctx context.Context, tx pgx.Tx, evidence RuntimeEventEvidence, signalType string) error {
	observedAt := evidence.EventTime.UTC()
	receivedAt := evidence.ReceivedAt.UTC()
	// DB constraint requires received_at >= observed_at - 24h on presence_signals.
	if receivedAt.Before(observedAt.Add(-24 * time.Hour)) {
		return nil
	}
	deviceID, err := resolveRuntimeEventDevice(ctx, tx, evidence)
	if err != nil || deviceID == "" {
		return err
	}
	if err := lockDevice(ctx, tx, deviceID); err != nil {
		return err
	}
	var policyRevision int64
	var acceptedSignalTypes []string
	var maxFutureSkew int
	err = tx.QueryRow(ctx, `
SELECT policy_revision, accepted_signal_types, max_future_clock_skew_seconds
FROM telemetry_runtime.presence_policies
WHERE device_id = $1::uuid
`, deviceID).Scan(&policyRevision, &acceptedSignalTypes, &maxFutureSkew)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("query telemetry presence policy for runtime event: %w", err)
	}
	if policyRevision < 1 {
		return nil
	}
	withinSkew := maxFutureSkew <= 0 || !observedAt.After(receivedAt.Add(time.Duration(maxFutureSkew)*time.Second))
	accepted := withinSkew && signalTypeAllowed(signalType, acceptedSignalTypes)
	signalID, err := store.newEventID(receivedAt)
	if err != nil {
		return fmt.Errorf("generate telemetry Presence Signal ID: %w", err)
	}
	sigTag, err := tx.Exec(ctx, `
INSERT INTO telemetry_runtime.presence_signals (
  signal_id, device_id, signal_type, observed_at, received_at, accepted,
  policy_revision, source_event_id, created_at
) VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8::uuid, $5)
ON CONFLICT (device_id, source_event_id) DO NOTHING
`, signalID, deviceID, signalType, observedAt, receivedAt, accepted, policyRevision, evidence.MessageID)
	if err != nil {
		return fmt.Errorf("persist MQTT runtime Presence Signal: %w", err)
	}
	if accepted && sigTag.RowsAffected() > 0 {
		if _, err := store.evaluateAndPersistDevice(ctx, tx, deviceID, nil, receivedAt); err != nil {
			return err
		}
	}
	return nil
}

func resolveRuntimeEventDevice(ctx context.Context, tx pgx.Tx, evidence RuntimeEventEvidence) (string, error) {
	if device := evidence.Device; device != nil {
		var knownTenant, knownSite string
		err := tx.QueryRow(ctx, `
SELECT tenant_id::text, site_id::text
FROM telemetry_runtime.devices
WHERE device_id = $1::uuid
`, device.DeviceID).Scan(&knownTenant, &knownSite)
		if err == nil {
			if knownTenant != device.TenantID || knownSite != device.SiteID {
				return "", nil
			}
			return device.DeviceID, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("query telemetry runtime event Device identity: %w", err)
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO telemetry_runtime.devices (device_id, tenant_id, site_id, updated_at)
VALUES ($1::uuid, $2::uuid, $3::uuid, $4)
ON CONFLICT (device_id) DO NOTHING
`, device.DeviceID, device.TenantID, device.SiteID, evidence.ReceivedAt.UTC()); err != nil {
			return "", fmt.Errorf("record telemetry runtime event Device identity: %w", err)
		}
		return device.DeviceID, nil
	}

	sourceType := strings.ToUpper(strings.TrimSpace(evidence.SourceType))
	sourceID := strings.ToLower(strings.TrimSpace(evidence.SourceID))
	if !uuidV7Pattern.MatchString(sourceID) {
		return "", nil
	}
	if sourceType == "GATEWAY" && sourceID != strings.ToLower(strings.TrimSpace(evidence.GatewayID)) {
		return "", nil
	}
	if sourceType != "GATEWAY" && sourceType != "DEVICE" {
		return "", nil
	}
	var resolved string
	err := tx.QueryRow(ctx, `
SELECT device_id::text
FROM telemetry_runtime.devices
WHERE device_id = $1::uuid AND tenant_id = $2::uuid AND site_id = $3::uuid
`, sourceID, evidence.TenantID, evidence.SiteID).Scan(&resolved)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("lookup telemetry runtime event Device: %w", err)
	}
	return resolved, nil
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

func validateRuntimeEventEvidence(evidence RuntimeEventEvidence) error {
	if !uuidV7Pattern.MatchString(evidence.TenantID) || !uuidV7Pattern.MatchString(evidence.SiteID) || !uuidV7Pattern.MatchString(evidence.MessageID) {
		return fmt.Errorf("%w: scope is invalid", ErrRuntimeEventInvalid)
	}
	if evidence.Sequence < 0 || strings.TrimSpace(evidence.GatewayID) == "" || strings.TrimSpace(evidence.EventType) == "" || strings.TrimSpace(evidence.SourceType) == "" || strings.TrimSpace(evidence.SourceID) == "" || evidence.EventTime.IsZero() || evidence.ReceivedAt.IsZero() || !json.Valid(evidence.Data) {
		return ErrRuntimeEventInvalid
	}
	if evidence.Severity != "INFO" && evidence.Severity != "WARNING" && evidence.Severity != "CRITICAL" {
		return fmt.Errorf("%w: severity is invalid", ErrRuntimeEventInvalid)
	}
	if device := evidence.Device; device != nil {
		if !uuidV7Pattern.MatchString(device.TenantID) || !uuidV7Pattern.MatchString(device.SiteID) || !uuidV7Pattern.MatchString(device.DeviceID) ||
			device.TenantID != evidence.TenantID || device.SiteID != evidence.SiteID {
			return fmt.Errorf("%w: resolved Device is invalid", ErrRuntimeEventInvalid)
		}
	}
	return nil
}
