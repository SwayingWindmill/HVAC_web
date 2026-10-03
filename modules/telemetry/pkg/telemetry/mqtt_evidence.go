package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

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
	EventTime  time.Time       `json:"eventTime"`
	Severity   string          `json:"severity"`
	Data       json.RawMessage `json:"data"`
	ReceivedAt time.Time       `json:"receivedAt"`
}

type MQTTEvidenceAcceptor interface {
	AcceptRuntimeEvent(context.Context, RuntimeEventEvidence) error
}

func (store *PostgresStore) AcceptRuntimeEvent(ctx context.Context, evidence RuntimeEventEvidence) error {
	if err := validateRuntimeEventEvidence(evidence); err != nil {
		return err
	}
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SET LOCAL ROLE s2_telemetry_runtime`); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO telemetry_runtime.mqtt_runtime_events(
message_id,tenant_id,site_id,gateway_id,event_type,source_type,source_id,event_time,severity,source_sequence,data,received_at,created_at)
VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7,$8,$9,$10,$11::jsonb,$12,$12)
ON CONFLICT (message_id) DO NOTHING`, evidence.MessageID, evidence.TenantID, evidence.SiteID, evidence.GatewayID,
		evidence.EventType, evidence.SourceType, evidence.SourceID, evidence.EventTime.UTC(), evidence.Severity, evidence.Sequence, evidence.Data, evidence.ReceivedAt.UTC())
	if err != nil {
		return fmt.Errorf("persist MQTT runtime event: %w", err)
	}
	return tx.Commit(ctx)
}

func validateRuntimeEventEvidence(evidence RuntimeEventEvidence) error {
	if !uuidV7Pattern.MatchString(evidence.TenantID) || !uuidV7Pattern.MatchString(evidence.SiteID) || !uuidV7Pattern.MatchString(evidence.MessageID) {
		return errors.New("MQTT runtime event scope is invalid")
	}
	if evidence.Sequence < 0 || strings.TrimSpace(evidence.GatewayID) == "" || strings.TrimSpace(evidence.EventType) == "" || strings.TrimSpace(evidence.SourceType) == "" || strings.TrimSpace(evidence.SourceID) == "" || evidence.EventTime.IsZero() || evidence.ReceivedAt.IsZero() || !json.Valid(evidence.Data) {
		return errors.New("MQTT runtime event is invalid")
	}
	if evidence.Severity != "INFO" && evidence.Severity != "WARNING" && evidence.Severity != "CRITICAL" {
		return errors.New("MQTT runtime event severity is invalid")
	}
	return nil
}
