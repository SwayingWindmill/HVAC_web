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
)

type GatewayEvidence struct {
	IntegrationInstanceID string          `json:"integrationInstanceId"`
	TenantID              string          `json:"tenantId"`
	SiteID                string          `json:"siteId"`
	GatewayID             string          `json:"gatewayId"`
	MessageID             string          `json:"messageId"`
	EvidenceType          string          `json:"evidenceType"`
	ObservedAt            time.Time       `json:"observedAt"`
	ReceivedAt            time.Time       `json:"receivedAt"`
	Sequence              int64           `json:"sequence"`
	Payload               json.RawMessage `json:"payload"`
}

type DevicePresenceEvidence struct {
	IntegrationInstanceID string    `json:"integrationInstanceId"`
	ExternalEntityType    string    `json:"externalEntityType"`
	ExternalID            string    `json:"externalId"`
	SignalType            string    `json:"signalType"`
	ObservedAt            time.Time `json:"observedAt"`
	ReceivedAt            time.Time `json:"receivedAt"`
	SourceEventID         string    `json:"sourceEventId"`
}

type RuntimeEventEvidence struct {
	IntegrationInstanceID string          `json:"integrationInstanceId"`
	TenantID              string          `json:"tenantId"`
	SiteID                string          `json:"siteId"`
	GatewayID             string          `json:"gatewayId"`
	MessageID             string          `json:"messageId"`
	Sequence              int64           `json:"sequence"`
	EventType             string          `json:"eventType"`
	SourceType            string          `json:"sourceType"`
	SourceID              string          `json:"sourceId"`
	EventTime             time.Time       `json:"eventTime"`
	Severity              string          `json:"severity"`
	Data                  json.RawMessage `json:"data"`
	ReceivedAt            time.Time       `json:"receivedAt"`
}

type PresenceEvidenceReceipt struct {
	DeviceID         string `json:"deviceId"`
	Accepted         bool   `json:"accepted"`
	Duplicate        bool   `json:"duplicate"`
	BusinessRevision int64  `json:"businessRevision,omitempty"`
	StateChanged     bool   `json:"stateChanged"`
}

type MQTTEvidenceAcceptor interface {
	AcceptGatewayEvidence(context.Context, GatewayEvidence) error
	AcceptPresenceEvidence(context.Context, DevicePresenceEvidence) (PresenceEvidenceReceipt, error)
	AcceptRuntimeEvent(context.Context, RuntimeEventEvidence) error
}

func (store *PostgresStore) AcceptGatewayEvidence(ctx context.Context, evidence GatewayEvidence) error {
	if err := validateGatewayEvidence(evidence); err != nil {
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
	if err = assertMQTTIntegrationScope(ctx, tx, evidence.IntegrationInstanceID, evidence.TenantID, evidence.SiteID); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO telemetry_runtime.mqtt_gateway_evidence(
message_id,integration_instance_id,tenant_id,site_id,gateway_id,evidence_type,observed_at,received_at,source_sequence,payload,created_at)
VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6,$7,$8,$9,$10::jsonb,$8)
ON CONFLICT (message_id) DO NOTHING`, evidence.MessageID, evidence.IntegrationInstanceID, evidence.TenantID, evidence.SiteID,
		evidence.GatewayID, evidence.EvidenceType, evidence.ObservedAt.UTC(), evidence.ReceivedAt.UTC(), evidence.Sequence, evidence.Payload)
	if err != nil {
		return fmt.Errorf("persist MQTT Gateway evidence: %w", err)
	}
	return tx.Commit(ctx)
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
	if err = assertMQTTIntegrationScope(ctx, tx, evidence.IntegrationInstanceID, evidence.TenantID, evidence.SiteID); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO telemetry_runtime.mqtt_runtime_events(
message_id,integration_instance_id,tenant_id,site_id,gateway_id,event_type,source_type,source_id,event_time,severity,source_sequence,data,received_at,created_at)
VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6,$7,$8,$9,$10,$11,$12::jsonb,$13,$13)
ON CONFLICT (message_id) DO NOTHING`, evidence.MessageID, evidence.IntegrationInstanceID, evidence.TenantID, evidence.SiteID, evidence.GatewayID,
		evidence.EventType, evidence.SourceType, evidence.SourceID, evidence.EventTime.UTC(), evidence.Severity, evidence.Sequence, evidence.Data, evidence.ReceivedAt.UTC())
	if err != nil {
		return fmt.Errorf("persist MQTT runtime event: %w", err)
	}
	return tx.Commit(ctx)
}

func (store *PostgresStore) AcceptPresenceEvidence(ctx context.Context, evidence DevicePresenceEvidence) (PresenceEvidenceReceipt, error) {
	if err := validateDevicePresenceEvidence(evidence); err != nil {
		return PresenceEvidenceReceipt{}, err
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return PresenceEvidenceReceipt{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SET LOCAL ROLE s2_telemetry_runtime`); err != nil {
		return PresenceEvidenceReceipt{}, err
	}
	bindings, err := queryRuntimeBindings(ctx, tx, evidence.IntegrationInstanceID, evidence.ExternalEntityType, evidence.ExternalID)
	if err != nil {
		return PresenceEvidenceReceipt{}, err
	}
	candidate := ObservationCandidate{IntegrationInstanceID: evidence.IntegrationInstanceID, ExternalEntityType: evidence.ExternalEntityType, ExternalID: evidence.ExternalID, ReceivedAt: evidence.ReceivedAt.UTC()}
	binding, quarantine := resolveRuntimeBinding(candidate, bindings)
	if quarantine != "" {
		return PresenceEvidenceReceipt{}, fmt.Errorf("MQTT Presence evidence binding rejected: %s", quarantine)
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "presence:"+binding.DeviceID); err != nil {
		return PresenceEvidenceReceipt{}, err
	}
	var policyRevision int64
	var acceptedTypes []string
	if err = tx.QueryRow(ctx, `SELECT policy_revision,accepted_signal_types FROM telemetry_runtime.presence_policies WHERE device_id=$1::uuid`, binding.DeviceID).Scan(&policyRevision, &acceptedTypes); err != nil {
		return PresenceEvidenceReceipt{}, fmt.Errorf("load Presence policy: %w", err)
	}
	accepted := slices.Contains(acceptedTypes, evidence.SignalType)
	signalID, err := store.newEventID(evidence.ReceivedAt.UTC())
	if err != nil {
		return PresenceEvidenceReceipt{}, err
	}
	tag, err := tx.Exec(ctx, `INSERT INTO telemetry_runtime.presence_signals(
signal_id,device_id,signal_type,observed_at,received_at,accepted,policy_revision,source_event_id,created_at)
VALUES($1::uuid,$2::uuid,$3,$4,$5,$6,$7,$8::uuid,$5)
ON CONFLICT (device_id,source_event_id) DO NOTHING`, signalID, binding.DeviceID, evidence.SignalType, evidence.ObservedAt.UTC(), evidence.ReceivedAt.UTC(), accepted, policyRevision, evidence.SourceEventID)
	if err != nil {
		return PresenceEvidenceReceipt{}, fmt.Errorf("persist MQTT Presence evidence: %w", err)
	}
	receipt := PresenceEvidenceReceipt{DeviceID: binding.DeviceID, Accepted: accepted, Duplicate: tag.RowsAffected() == 0}
	if accepted && !receipt.Duplicate {
		commit, evalErr := store.evaluateAndPersistDevice(ctx, tx, binding.DeviceID, nil, evidence.ReceivedAt.UTC())
		if evalErr != nil {
			return PresenceEvidenceReceipt{}, evalErr
		}
		receipt.BusinessRevision = int64(commit.Snapshot.BusinessRevision)
		receipt.StateChanged = commit.StateChanged
	}
	if err = tx.Commit(ctx); err != nil {
		return PresenceEvidenceReceipt{}, err
	}
	return receipt, nil
}

func assertMQTTIntegrationScope(ctx context.Context, tx pgx.Tx, integrationInstanceID, tenantID, siteID string) error {
	var present bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (
SELECT 1 FROM telemetry_runtime.registry_device_bindings
WHERE integration_instance_id=$1::uuid AND tenant_id=$2::uuid AND site_id=$3::uuid
  AND binding_status='ACTIVE' AND (valid_to IS NULL OR valid_to > now())
)`, integrationInstanceID, tenantID, siteID).Scan(&present); err != nil {
		return fmt.Errorf("verify MQTT Integration Tenant/Site scope: %w", err)
	}
	if !present {
		return errors.New("MQTT Integration is not authorized for Tenant/Site scope")
	}
	return nil
}

func validateGatewayEvidence(evidence GatewayEvidence) error {
	if !uuidV7Pattern.MatchString(evidence.IntegrationInstanceID) || !uuidV7Pattern.MatchString(evidence.TenantID) || !uuidV7Pattern.MatchString(evidence.SiteID) || !uuidV7Pattern.MatchString(evidence.MessageID) {
		return errors.New("MQTT Gateway evidence scope is invalid")
	}
	if strings.TrimSpace(evidence.GatewayID) == "" || evidence.Sequence < 0 || evidence.ObservedAt.IsZero() || evidence.ReceivedAt.IsZero() || !json.Valid(evidence.Payload) {
		return errors.New("MQTT Gateway evidence is invalid")
	}
	if evidence.EvidenceType != "STATE" && evidence.EvidenceType != "HEARTBEAT" && evidence.EvidenceType != "SESSION" && evidence.EvidenceType != "LWT" {
		return errors.New("MQTT Gateway evidence type is invalid")
	}
	return nil
}

func validateDevicePresenceEvidence(evidence DevicePresenceEvidence) error {
	if !uuidV7Pattern.MatchString(evidence.IntegrationInstanceID) || !uuidV7Pattern.MatchString(evidence.SourceEventID) || evidence.ExternalEntityType != "DEVICE" || strings.TrimSpace(evidence.ExternalID) == "" {
		return errors.New("MQTT Presence evidence identity is invalid")
	}
	if evidence.SignalType != "SOURCE_ACTIVITY" && evidence.SignalType != "EXPLICIT_CONNECT" && evidence.SignalType != "EXPLICIT_DISCONNECT" {
		return errors.New("MQTT Presence evidence signal type is invalid")
	}
	if evidence.ObservedAt.IsZero() || evidence.ReceivedAt.IsZero() {
		return errors.New("MQTT Presence evidence time is invalid")
	}
	return nil
}

func validateRuntimeEventEvidence(evidence RuntimeEventEvidence) error {
	if !uuidV7Pattern.MatchString(evidence.IntegrationInstanceID) || !uuidV7Pattern.MatchString(evidence.TenantID) || !uuidV7Pattern.MatchString(evidence.SiteID) || !uuidV7Pattern.MatchString(evidence.MessageID) {
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
