package operationsauditevent

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	SchemaVersion = 1
	MessageType   = "hvac.operations.audit.v1"
	Producer      = "operations-agent-service"
)

const (
	maximumIdentityLength = 768
	maximumReferences     = 32
)

type Actor struct {
	ActorType         string `json:"actorType"`
	ActorID           string `json:"actorId"`
	ActorIssuer       string `json:"actorIssuer"`
	ExecutingService  string `json:"executingService"`
	ExecutingSPIFFEID string `json:"executingSpiffeId"`
}

type RecordReference struct {
	RecordType string `json:"recordType"`
	RecordID   string `json:"recordId"`
}

type EventV1 struct {
	EventID                 string            `json:"eventId"`
	SchemaVersion           int               `json:"schemaVersion"`
	MessageType             string            `json:"messageType"`
	Producer                string            `json:"producer"`
	TenantID                 string            `json:"tenantId"`
	SiteID                  string            `json:"siteId"`
	InvestigationID         *string           `json:"investigationId"`
	RunID                   *string           `json:"runId"`
	InvestigationRevision   *uint64           `json:"investigationRevision"`
	Actor                   Actor             `json:"actor"`
	AuthorizationDecisionID string            `json:"authorizationDecisionId"`
	PolicyRevision          string            `json:"policyRevision"`
	Action                  string            `json:"action"`
	Operation               string            `json:"operation"`
	Outcome                 string            `json:"outcome"`
	OccurredAt              int64             `json:"occurredAt"`
	RecordReferences        []RecordReference `json:"recordReferences"`
}

var allowedOperations = map[string]struct{}{
	"CREATE_INVESTIGATION": {}, "LIST_INVESTIGATIONS": {}, "READ_INVESTIGATION": {},
	"START_AGENT_RUN": {}, "REOPEN_INVESTIGATION": {}, "ADVANCE_AGENT_RUN": {},
	"PLAN_READS": {}, "COMMIT_EFFECT": {}, "PAUSE_AGENT_RUN": {},
	"RESUME_AGENT_RUN": {}, "REQUEST_OPERATOR_INPUT": {}, "ACCEPT_OPERATOR_INPUT": {},
	"CANCEL_INVESTIGATION": {}, "COMPLETE_AGENT_RUN": {}, "FAIL_AGENT_RUN": {},
}

var allowedOutcomes = map[string]struct{}{
	"SUCCEEDED": {}, "DENIED": {}, "DUPLICATE": {}, "PARTIAL": {},
	"UNABLE_TO_CONCLUDE": {}, "FAILED": {},
}

var allowedRecordTypes = map[string]struct{}{
	"EVIDENCE": {}, "ANALYSIS_REFERENCE": {}, "FINDING": {},
	"TOOL_EXECUTION_RECEIPT": {}, "OPERATOR_INPUT_ACCEPTED": {},
}

func Decode(payload []byte) (EventV1, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var event EventV1
	if err := decoder.Decode(&event); err != nil {
		return EventV1{}, fmt.Errorf("decode operations audit event: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return EventV1{}, errors.New("operations audit event contains trailing JSON")
	}
	if err := event.Validate(); err != nil {
		return EventV1{}, err
	}
	return event, nil
}

func (event EventV1) Validate() error {
	if event.SchemaVersion != SchemaVersion || event.MessageType != MessageType || event.Producer != Producer {
		return errors.New("operations audit version or producer is invalid")
	}
	for name, value := range map[string]string{
		"eventId": event.EventID, "tenantId": event.TenantID, "siteId": event.SiteID,
		"authorizationDecisionId": event.AuthorizationDecisionID, "policyRevision": event.PolicyRevision,
		"actorId": event.Actor.ActorID, "actorIssuer": event.Actor.ActorIssuer,
		"executingSpiffeId": event.Actor.ExecutingSPIFFEID,
	} {
		if !bounded(value, maximumIdentityLength) {
			return fmt.Errorf("operations audit %s is invalid", name)
		}
	}
	if event.InvestigationID != nil && !bounded(*event.InvestigationID, maximumIdentityLength) {
		return errors.New("operations audit investigation identity is invalid")
	}
	if event.RunID != nil && !bounded(*event.RunID, maximumIdentityLength) {
		return errors.New("operations audit run identity is invalid")
	}
	if (event.InvestigationID == nil) != (event.InvestigationRevision == nil) {
		return errors.New("operations audit investigation correlation is incomplete")
	}
	if event.Actor.ActorType != "OPERATOR" && event.Actor.ActorType != "SERVICE" {
		return errors.New("operations audit actor type is invalid")
	}
	if event.Actor.ExecutingService != Producer || !strings.HasPrefix(event.Actor.ExecutingSPIFFEID, "spiffe://") {
		return errors.New("operations audit executing service is invalid")
	}
	if _, ok := allowedOperations[event.Operation]; !ok || event.Action != event.Operation {
		return errors.New("operations audit operation is invalid")
	}
	if _, ok := allowedOutcomes[event.Outcome]; !ok {
		return errors.New("operations audit outcome is invalid")
	}
	if event.OccurredAt < 0 || len(event.RecordReferences) > maximumReferences {
		return errors.New("operations audit time or record references are invalid")
	}
	seen := map[string]struct{}{}
	for _, reference := range event.RecordReferences {
		if _, ok := allowedRecordTypes[reference.RecordType]; !ok || !bounded(reference.RecordID, maximumIdentityLength) {
			return errors.New("operations audit record reference is invalid")
		}
		identity := reference.RecordType + ":" + reference.RecordID
		if _, ok := seen[identity]; ok {
			return errors.New("operations audit record references are duplicated")
		}
		seen[identity] = struct{}{}
	}
	if len(event.RecordReferences) > 0 && event.InvestigationID == nil {
		return errors.New("operations audit record references require an investigation")
	}
	return nil
}

func (event EventV1) AggregateID() string {
	identity := event.EventID
	if event.InvestigationID != nil {
		identity = event.TenantID + ":" + event.SiteID + ":" + *event.InvestigationID
	}
	digest := sha256.Sum256([]byte("operations-investigation:" + identity))
	return hex.EncodeToString(digest[:])
}

func (event EventV1) AggregateVersion() uint64 {
	if event.InvestigationRevision == nil {
		return 1
	}
	return *event.InvestigationRevision + 1
}

func (event EventV1) CorrelationID() string {
	identity := event.TenantID + ":" + event.SiteID
	if event.InvestigationID != nil {
		identity += ":" + *event.InvestigationID
	}
	if event.RunID != nil {
		identity += ":" + *event.RunID
	}
	digest := sha256.Sum256([]byte("operations-correlation:" + identity))
	return "sha256:" + hex.EncodeToString(digest[:16])
}

func (event EventV1) PayloadSHA256(payload []byte) string {
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func bounded(value string, maximum int) bool {
	trimmed := strings.TrimSpace(value)
	return trimmed != "" && len(value) <= maximum && !strings.ContainsAny(value, "\r\n")
}
