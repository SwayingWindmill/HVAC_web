package audit

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"strings"
	"time"
)

const (
	DomainEventSchemaVersion = 1
	DomainAuditPath          = "/internal/v1/audit/domain-events"
	GatewayDomainTopic       = "gateway-domain-http"

	AggregateTypeSession                 = "bff-session"
	AggregateTypeOperationsInvestigation = "operations-investigation"
	AggregateTypeRemoteCommand           = "remote-command"
	AggregateTypeControlLock             = "control-lock"
	AggregateTypeOperatingSchedule       = "operating-schedule"
	AggregateTypeRegistryMutation        = "registry-mutation"
	AggregateTypeIAMAdmin                = "iam-admin"
	AggregateTypeGatewayCredential       = "gateway-credential"

	CategoryControlSchedule = "control-schedule"

	ClientKindBrowser     = "BROWSER"
	ClientKindMiniProgram = "MINI_PROGRAM"
	ClientKindSystem      = "SYSTEM"
)

// DomainEvent carries one tamper-evident Gateway domain mutation event into the Audit Ledger.
type DomainEvent struct {
	SchemaVersion     int             `json:"schemaVersion"`
	MessageID         string          `json:"messageId"`
	TenantID          string          `json:"tenantId"`
	Category          string          `json:"category"`
	TargetID          string          `json:"targetId"`
	AggregateVersion  uint64          `json:"aggregateVersion"`
	OccurredAt        time.Time       `json:"occurredAt"`
	InitiatingSubject string          `json:"initiatingSubject"`
	InitiatingIssuer  string          `json:"initiatingIssuer"`
	ExecutingService  string          `json:"executingService"`
	ExecutingSPIFFEID string          `json:"executingSpiffeId"`
	Action            string          `json:"action"`
	Outcome           string          `json:"outcome"`
	PolicyRevision    string          `json:"policyRevision"`
	CorrelationID     string          `json:"correlationId"`
	CausationID       string          `json:"causationId"`
	TraceID           string          `json:"traceId"`
	Traceparent       string          `json:"traceparent,omitempty"`
	ClientIP          string          `json:"clientIp,omitempty"`
	ClientKind        string          `json:"clientKind,omitempty"`
	ActorDisplay      string          `json:"actorDisplay,omitempty"`
	TargetLabel       string          `json:"targetLabel,omitempty"`
	ChangeSummary     json.RawMessage `json:"changeSummary,omitempty"`
}

// DecodeDomainEvent parses and validates a JSON domain audit event payload.
func DecodeDomainEvent(payload []byte) (DomainEvent, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var event DomainEvent
	if err := decoder.Decode(&event); err != nil {
		return DomainEvent{}, fmt.Errorf("decode domain audit event: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return DomainEvent{}, errors.New("domain audit event contains trailing JSON")
	}
	if err := event.Validate(); err != nil {
		return DomainEvent{}, err
	}
	return event, nil
}

// Validate enforces schema, identity, trace, IP, and payload size bounds for a DomainEvent.
func (event DomainEvent) Validate() error {
	if event.SchemaVersion != DomainEventSchemaVersion {
		return fmt.Errorf("unsupported domain audit schema version %d", event.SchemaVersion)
	}
	if !boundedAuditToken(event.MessageID, 128) ||
		!boundedAuditToken(event.TenantID, 128) ||
		!boundedAuditToken(event.TargetID, 256) ||
		event.AggregateVersion == 0 ||
		event.OccurredAt.IsZero() {
		return errors.New("domain audit event envelope identity is invalid")
	}
	if !isDomainAggregateType(event.Category) {
		return fmt.Errorf("unsupported domain audit category %q", event.Category)
	}
	if !boundedAuditText(event.InitiatingSubject, 256) ||
		!boundedAuditText(event.InitiatingIssuer, 512) ||
		!boundedAuditToken(event.ExecutingService, 128) ||
		!strings.HasPrefix(event.ExecutingSPIFFEID, "spiffe://") ||
		!boundedAuditToken(event.ExecutingSPIFFEID, 256) {
		return errors.New("domain audit actor chain is invalid")
	}
	if !boundedAuditToken(event.Action, 128) ||
		!boundedAuditToken(event.Outcome, 64) ||
		!boundedAuditToken(event.PolicyRevision, 128) ||
		!boundedAuditToken(event.CorrelationID, 128) ||
		!boundedAuditToken(event.CausationID, 128) {
		return errors.New("domain audit operation metadata is invalid")
	}
	if !isLowerHexOfLength(event.TraceID, 32) {
		return errors.New("domain audit traceId must be 32 lowercase hex characters")
	}
	if len(event.Traceparent) > 256 || strings.ContainsAny(event.Traceparent, "\r\n") {
		return errors.New("domain audit traceparent is invalid")
	}
	if event.ClientIP != "" {
		addr, err := netip.ParseAddr(event.ClientIP)
		if err != nil || !addr.IsValid() || len(event.ClientIP) > 128 {
			return errors.New("domain audit clientIp is invalid")
		}
	}
	if event.ClientKind != "" &&
		event.ClientKind != ClientKindBrowser &&
		event.ClientKind != ClientKindMiniProgram &&
		event.ClientKind != ClientKindSystem {
		return fmt.Errorf("unsupported domain audit clientKind %q", event.ClientKind)
	}
	if !optionalBoundedAuditText(event.ActorDisplay, 256) ||
		!optionalBoundedAuditText(event.TargetLabel, 256) {
		return errors.New("domain audit display labels are invalid")
	}
	if trimmed := bytes.TrimSpace(event.ChangeSummary); len(trimmed) > 0 {
		if len(trimmed) > 16*1024 || trimmed[0] != '{' || !json.Valid(trimmed) {
			return errors.New("domain audit changeSummary must be a valid bounded JSON object")
		}
	}
	return nil
}

// AggregateID derives a deterministic 64-character lowercase SHA-256 hex key per category/tenant/target.
func (event DomainEvent) AggregateID() string {
	digest := sha256.Sum256([]byte(event.Category + ":" + event.TenantID + ":" + event.TargetID))
	return hex.EncodeToString(digest[:])
}

func (event DomainEvent) compactedChangeSummary() []byte {
	trimmed := bytes.TrimSpace(event.ChangeSummary)
	if len(trimmed) == 0 {
		return nil
	}
	var compacted bytes.Buffer
	if err := json.Compact(&compacted, trimmed); err != nil {
		return trimmed
	}
	return compacted.Bytes()
}

func isDomainAggregateType(category string) bool {
	switch category {
	case AggregateTypeRemoteCommand,
		AggregateTypeControlLock,
		AggregateTypeOperatingSchedule,
		AggregateTypeRegistryMutation,
		AggregateTypeIAMAdmin,
		AggregateTypeGatewayCredential:
		return true
	default:
		return false
	}
}

func boundedAuditToken(value string, maximum int) bool {
	return len(value) > 0 && len(value) <= maximum && !strings.ContainsAny(value, "\r\n\x00")
}

func boundedAuditText(value string, maximum int) bool {
	return len(strings.TrimSpace(value)) > 0 && len(value) <= maximum && !strings.ContainsAny(value, "\r\n\x00")
}

func optionalBoundedAuditText(value string, maximum int) bool {
	return len(value) <= maximum && !strings.ContainsAny(value, "\r\n\x00")
}

func isLowerHexOfLength(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for index := 0; index < len(value); index++ {
		ch := value[index]
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
	}
	return true
}
