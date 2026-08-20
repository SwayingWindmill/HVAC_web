package notificationservice

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
)

const ArtifactSchemaVersion = 1

var templateVariablePattern = regexp.MustCompile(`\{\{([A-Za-z0-9_.-]+)\}\}`)

type Channel string

const (
	ChannelInApp Channel = "IN_APP"
	ChannelEmail Channel = "EMAIL"
	ChannelREST  Channel = "REST"
)

type AlarmAction string

const (
	AlarmCreated         AlarmAction = "CREATED"
	AlarmSeverityChanged AlarmAction = "SEVERITY_CHANGED"
	AlarmAcknowledged    AlarmAction = "ACKNOWLEDGED"
	AlarmCleared         AlarmAction = "CLEARED"
)

type IntentStatus string

const (
	IntentScheduled         IntentStatus = "SCHEDULED"
	IntentClaimed           IntentStatus = "CLAIMED"
	IntentMaterialized      IntentStatus = "MATERIALIZED"
	IntentExternalSubmitted IntentStatus = "EXTERNAL_SUBMITTED"
	IntentDelivered         IntentStatus = "DELIVERED"
	IntentFailed            IntentStatus = "FAILED"
	IntentCancelled         IntentStatus = "CANCELLED"
)

type InboxStatus string

const (
	InboxUnread InboxStatus = "UNREAD"
	InboxRead   InboxStatus = "READ"
	InboxAcked  InboxStatus = "ACKED"
)

type DeliveryDisposition string

const (
	DispositionPending   DeliveryDisposition = "PENDING"
	DispositionDelivered DeliveryDisposition = "DELIVERED"
	DispositionUnknown   DeliveryDisposition = "OUTCOME_UNKNOWN"
	DispositionFailed    DeliveryDisposition = "FAILED"
	DispositionCancelled DeliveryDisposition = "CANCELLED"
)

type Recipient struct {
	PrincipalID string `json:"principalId"`
	Address     string `json:"address,omitempty"`
}

type TemplateRevision struct {
	SchemaVersion      int     `json:"schemaVersion"`
	TemplateID         string  `json:"templateId"`
	TemplateRevisionID string  `json:"templateRevisionId"`
	Revision           uint64  `json:"revision"`
	Digest             string  `json:"digest"`
	Channel            Channel `json:"channel"`
	Subject            string  `json:"subject"`
	Body               string  `json:"body"`
}

type AudienceRevision struct {
	SchemaVersion      int         `json:"schemaVersion"`
	AudienceID         string      `json:"audienceId"`
	AudienceRevisionID string      `json:"audienceRevisionId"`
	Revision           uint64      `json:"revision"`
	Digest             string      `json:"digest"`
	Recipients         []Recipient `json:"recipients"`
}

type EscalationStage struct {
	Stage              uint32  `json:"stage"`
	DelaySeconds       uint32  `json:"delaySeconds"`
	AudienceRevisionID string  `json:"audienceRevisionId"`
	TemplateRevisionID string  `json:"templateRevisionId"`
	Channel            Channel `json:"channel"`
	IntegrationID      string  `json:"integrationId,omitempty"`
}

type NotificationPolicyRevision struct {
	SchemaVersion    int                   `json:"schemaVersion"`
	PolicyID         string                `json:"policyId"`
	PolicyRevisionID string                `json:"policyRevisionId"`
	Revision         uint64                `json:"revision"`
	Digest           string                `json:"digest"`
	Name             string                `json:"name"`
	AlarmActions     []AlarmAction         `json:"alarmActions"`
	Severities       []alarmmodel.Severity `json:"severities,omitempty"`
	MandatorySafety  bool                  `json:"mandatorySafety"`
	Stages           []EscalationStage     `json:"stages"`
}

type PolicyAssignment struct {
	TenantID           string
	SiteID             string
	AssignmentID       string
	AssignmentRevision uint64
	PolicyRevisionID   string
	AssignedAt         time.Time
	AssignedBy         string
}

type AlarmEvent struct {
	TenantID              string               `json:"tenantId"`
	SiteID                string               `json:"siteId"`
	SourceEventID         string               `json:"sourceEventId"`
	AlarmID               string               `json:"alarmId"`
	IncidentCorrelationID string               `json:"incidentCorrelationId"`
	Action                AlarmAction          `json:"action"`
	CurrentSeverity       alarmmodel.Severity  `json:"currentSeverity"`
	PeakSeverity          alarmmodel.Severity  `json:"peakSeverity"`
	Condition             alarmmodel.Condition `json:"condition"`
	OccurredAt            time.Time            `json:"occurredAt"`
	Attributes            map[string]string    `json:"attributes,omitempty"`
}

type NotificationIntent struct {
	IntentID                 string              `json:"intentId"`
	TenantID                 string              `json:"tenantId"`
	SiteID                   string              `json:"siteId"`
	SourceEventID            string              `json:"sourceEventId"`
	AlarmID                  string              `json:"alarmId"`
	IncidentCorrelationID    string              `json:"incidentCorrelationId"`
	SourceAction             AlarmAction         `json:"sourceAction"`
	CurrentSeverity          alarmmodel.Severity `json:"currentSeverity"`
	PolicyRevisionID         string              `json:"policyRevisionId"`
	AssignmentID             string              `json:"assignmentId"`
	AssignmentRevision       uint64              `json:"assignmentRevision"`
	Stage                    uint32              `json:"stage"`
	Channel                  Channel             `json:"channel"`
	IntegrationID            string              `json:"integrationId,omitempty"`
	MandatorySafety          bool                `json:"mandatorySafety"`
	Recipients               []Recipient         `json:"recipients"`
	TemplateRevisionID       string              `json:"templateRevisionId"`
	RenderedSubject          string              `json:"renderedSubject"`
	RenderedBody             string              `json:"renderedBody"`
	DueAt                    time.Time           `json:"dueAt"`
	Status                   IntentStatus        `json:"status"`
	Disposition              DeliveryDisposition `json:"disposition"`
	ExternalDeliveryIntentID string              `json:"externalDeliveryIntentId,omitempty"`
}

type IntentClaim struct {
	NotificationIntent
	WorkerID   string    `json:"workerId"`
	LeaseUntil time.Time `json:"leaseUntil"`
	Fence      uint64    `json:"fence"`
}

type InboxItem struct {
	InboxItemID           string              `json:"inboxItemId"`
	IntentID              string              `json:"intentId"`
	TenantID              string              `json:"tenantId"`
	SiteID                string              `json:"siteId"`
	PrincipalID           string              `json:"principalId"`
	AlarmID               string              `json:"alarmId"`
	IncidentCorrelationID string              `json:"incidentCorrelationId"`
	SourceAction          AlarmAction         `json:"sourceAction"`
	Severity              alarmmodel.Severity `json:"severity"`
	Subject               string              `json:"subject"`
	Body                  string              `json:"body"`
	Status                InboxStatus         `json:"status"`
	CreatedAt             time.Time           `json:"createdAt"`
	ReadAt                *time.Time          `json:"readAt,omitempty"`
}

func (template TemplateRevision) Validate() error {
	if template.SchemaVersion != ArtifactSchemaVersion || !alarmmodel.IsUUIDv7(template.TemplateID) || !alarmmodel.IsUUIDv7(template.TemplateRevisionID) || template.Revision == 0 {
		return errors.New("notification template revision identity is invalid")
	}
	if template.Channel != ChannelInApp && template.Channel != ChannelEmail && template.Channel != ChannelREST {
		return errors.New("notification template channel is invalid")
	}
	if strings.TrimSpace(template.Subject) == "" || len(template.Subject) > 512 || strings.TrimSpace(template.Body) == "" || len(template.Body) > 16<<10 {
		return errors.New("notification template content is invalid")
	}
	return validateDigest(template.Digest, TemplateDigest(template))
}

func (audience AudienceRevision) Validate() error {
	if audience.SchemaVersion != ArtifactSchemaVersion || !alarmmodel.IsUUIDv7(audience.AudienceID) || !alarmmodel.IsUUIDv7(audience.AudienceRevisionID) || audience.Revision == 0 || len(audience.Recipients) == 0 || len(audience.Recipients) > 500 {
		return errors.New("notification audience revision identity is invalid")
	}
	seen := map[string]struct{}{}
	for _, recipient := range audience.Recipients {
		principal := strings.TrimSpace(recipient.PrincipalID)
		if principal == "" || len(principal) > 256 || len(recipient.Address) > 512 {
			return errors.New("notification audience recipient is invalid")
		}
		if _, exists := seen[principal]; exists {
			return errors.New("notification audience contains duplicate principal")
		}
		seen[principal] = struct{}{}
	}
	return validateDigest(audience.Digest, AudienceDigest(audience))
}

func (policy NotificationPolicyRevision) Validate() error {
	if policy.SchemaVersion != ArtifactSchemaVersion || !alarmmodel.IsUUIDv7(policy.PolicyID) || !alarmmodel.IsUUIDv7(policy.PolicyRevisionID) || policy.Revision == 0 || strings.TrimSpace(policy.Name) == "" || len(policy.Name) > 256 {
		return errors.New("notification policy revision identity is invalid")
	}
	if len(policy.AlarmActions) == 0 || len(policy.Stages) == 0 || len(policy.Stages) > 16 {
		return errors.New("notification policy trigger or escalation stages are invalid")
	}
	actions := map[AlarmAction]struct{}{}
	for _, action := range policy.AlarmActions {
		if !validAlarmAction(action) {
			return errors.New("notification policy Alarm action is invalid")
		}
		actions[action] = struct{}{}
	}
	if len(actions) != len(policy.AlarmActions) {
		return errors.New("notification policy Alarm actions contain duplicates")
	}
	lastDelay := uint32(0)
	for index, stage := range policy.Stages {
		if stage.Stage != uint32(index) || !alarmmodel.IsUUIDv7(stage.AudienceRevisionID) || !alarmmodel.IsUUIDv7(stage.TemplateRevisionID) {
			return errors.New("notification escalation stage identity is invalid")
		}
		if index > 0 && stage.DelaySeconds <= lastDelay {
			return errors.New("notification escalation delays must increase")
		}
		lastDelay = stage.DelaySeconds
		if stage.Channel != ChannelInApp && stage.Channel != ChannelEmail && stage.Channel != ChannelREST {
			return errors.New("notification escalation channel is invalid")
		}
		if stage.Channel == ChannelInApp && strings.TrimSpace(stage.IntegrationID) != "" {
			return errors.New("in-app notification stage must not reference external integration")
		}
		if stage.Channel != ChannelInApp && !alarmmodel.IsUUIDv7(stage.IntegrationID) {
			return errors.New("external notification stage requires S15 integration identity")
		}
	}
	return validateDigest(policy.Digest, PolicyDigest(policy))
}

func (event AlarmEvent) Validate() error {
	if !alarmmodel.IsUUIDv7(event.TenantID) || !alarmmodel.IsUUIDv7(event.SiteID) || !alarmmodel.IsUUIDv7(event.SourceEventID) || !alarmmodel.IsUUIDv7(event.AlarmID) || !alarmmodel.IsUUIDv7(event.IncidentCorrelationID) || event.OccurredAt.IsZero() {
		return errors.New("notification Alarm event identity is invalid")
	}
	if !validAlarmAction(event.Action) {
		return errors.New("notification Alarm action is invalid")
	}
	if !validSeverity(event.CurrentSeverity) || !validSeverity(event.PeakSeverity) || (event.Condition != alarmmodel.ConditionActive && event.Condition != alarmmodel.ConditionCleared) {
		return errors.New("notification Alarm state projection is invalid")
	}
	if event.Action == AlarmCleared && event.Condition != alarmmodel.ConditionCleared {
		return errors.New("cleared notification event must carry CLEARED condition")
	}
	if event.Action != AlarmCleared && event.Condition == alarmmodel.ConditionCleared && event.Action != AlarmAcknowledged {
		return errors.New("non-clear notification event has inconsistent condition")
	}
	return nil
}

func TemplateDigest(template TemplateRevision) string {
	copyTemplate := template
	copyTemplate.Digest = ""
	return digestJSON(copyTemplate)
}

func AudienceDigest(audience AudienceRevision) string {
	copyAudience := audience
	copyAudience.Digest = ""
	copyAudience.Recipients = append([]Recipient(nil), audience.Recipients...)
	sort.Slice(copyAudience.Recipients, func(i, j int) bool {
		return copyAudience.Recipients[i].PrincipalID < copyAudience.Recipients[j].PrincipalID
	})
	return digestJSON(copyAudience)
}

func PolicyDigest(policy NotificationPolicyRevision) string {
	copyPolicy := policy
	copyPolicy.Digest = ""
	copyPolicy.AlarmActions = append([]AlarmAction(nil), policy.AlarmActions...)
	copyPolicy.Severities = append([]alarmmodel.Severity(nil), policy.Severities...)
	sort.Slice(copyPolicy.AlarmActions, func(i, j int) bool { return copyPolicy.AlarmActions[i] < copyPolicy.AlarmActions[j] })
	sort.Slice(copyPolicy.Severities, func(i, j int) bool { return copyPolicy.Severities[i] < copyPolicy.Severities[j] })
	return digestJSON(copyPolicy)
}

func RenderTemplate(template TemplateRevision, values map[string]string) (string, string, error) {
	render := func(input string) (string, error) {
		missing := ""
		output := templateVariablePattern.ReplaceAllStringFunc(input, func(token string) string {
			key := templateVariablePattern.FindStringSubmatch(token)[1]
			value, ok := values[key]
			if !ok && missing == "" {
				missing = key
			}
			return value
		})
		if missing != "" {
			return "", fmt.Errorf("notification template variable %q is missing", missing)
		}
		return output, nil
	}
	subject, err := render(template.Subject)
	if err != nil {
		return "", "", err
	}
	body, err := render(template.Body)
	if err != nil {
		return "", "", err
	}
	return subject, body, nil
}

func EventTemplateValues(event AlarmEvent) map[string]string {
	values := map[string]string{
		"alarmId":               event.AlarmID,
		"incidentCorrelationId": event.IncidentCorrelationID,
		"action":                string(event.Action),
		"currentSeverity":       string(event.CurrentSeverity),
		"peakSeverity":          string(event.PeakSeverity),
		"condition":             string(event.Condition),
		"occurredAt":            event.OccurredAt.UTC().Format(time.RFC3339Nano),
		"siteId":                event.SiteID,
	}
	for key, value := range event.Attributes {
		values["attributes."+key] = value
	}
	return values
}

func policyMatches(policy NotificationPolicyRevision, event AlarmEvent) bool {
	actionMatch := false
	for _, action := range policy.AlarmActions {
		if action == event.Action {
			actionMatch = true
			break
		}
	}
	if !actionMatch {
		return false
	}
	if len(policy.Severities) == 0 {
		return true
	}
	for _, severity := range policy.Severities {
		if severity == event.CurrentSeverity {
			return true
		}
	}
	return false
}

func validAlarmAction(action AlarmAction) bool {
	switch action {
	case AlarmCreated, AlarmSeverityChanged, AlarmAcknowledged, AlarmCleared:
		return true
	default:
		return false
	}
}

func validSeverity(severity alarmmodel.Severity) bool {
	switch severity {
	case alarmmodel.SeverityInfo, alarmmodel.SeverityWarning, alarmmodel.SeverityMinor, alarmmodel.SeverityMajor, alarmmodel.SeverityCritical:
		return true
	default:
		return false
	}
}

func validateDigest(actual, expected string) error {
	if len(actual) != 64 || actual != expected {
		return errors.New("notification released artifact digest is invalid")
	}
	return nil
}

func digestJSON(value any) string {
	encoded, _ := json.Marshal(value)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}
