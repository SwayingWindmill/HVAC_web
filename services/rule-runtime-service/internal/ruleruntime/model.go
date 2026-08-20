package ruleruntime

import (
	"context"
	"encoding/json"
	"time"
)

type PortType string

const (
	PortEvent    PortType = "EVENT"
	PortNumber   PortType = "NUMBER"
	PortSnapshot PortType = "SNAPSHOT"
	PortIntent   PortType = "INTENT"
)

type RevisionState string

const (
	RevisionDraft     RevisionState = "DRAFT"
	RevisionValidated RevisionState = "VALIDATED"
	RevisionReleased  RevisionState = "RELEASED"
	RevisionRetired   RevisionState = "RETIRED"
)

type ExecutionStatus string

const (
	ExecutionReady       ExecutionStatus = "READY"
	ExecutionRunning     ExecutionStatus = "RUNNING"
	ExecutionWaiting     ExecutionStatus = "WAITING"
	ExecutionBlocked     ExecutionStatus = "BLOCKED_EFFECT"
	ExecutionSucceeded   ExecutionStatus = "SUCCEEDED"
	ExecutionDead        ExecutionStatus = "DEAD"
	ExecutionQuarantined ExecutionStatus = "QUARANTINED"
	ExecutionFailed      ExecutionStatus = "FAILED"
)

type FailureClass string

const (
	FailureNone            FailureClass = ""
	FailureValidation      FailureClass = "VALIDATION"
	FailurePolicy          FailureClass = "POLICY"
	FailureSafetyDenied    FailureClass = "SAFETY_DENIED"
	FailureTransient       FailureClass = "TRANSIENT_INFRASTRUCTURE"
	FailureAmbiguous       FailureClass = "AMBIGUOUS_EFFECT"
	FailureTimeout         FailureClass = "TIMEOUT"
	FailurePoison          FailureClass = "POISON_EVENT"
	FailureSchemaUnknown   FailureClass = "SCHEMA_UNKNOWN"
	FailureBudgetExhausted FailureClass = "BUDGET_EXHAUSTED"
)

type ExecutionMode string

const (
	ModeLive   ExecutionMode = "LIVE"
	ModeReplay ExecutionMode = "REPLAY"
)

type PortDefinition struct {
	Name PortType `json:"type"`
}

type NodeDefinition struct {
	ID                 string
	Version            int
	Inputs             map[string]PortType
	Outputs            map[string]PortType
	RequiredPermission string
	EffectOwner        string
	StateSchemaVersion int
	Deterministic      bool
	ResourceCost       int
	ValidateConfig     func(json.RawMessage) error
	Evaluate           NodeEvaluator
}

type NodeInstance struct {
	ID           string          `json:"id"`
	DefinitionID string          `json:"definitionId"`
	Config       json.RawMessage `json:"config"`
}

type Edge struct {
	FromNode string `json:"fromNode"`
	FromPort string `json:"fromPort"`
	ToNode   string `json:"toNode"`
	ToPort   string `json:"toPort"`
}

type RuleRevision struct {
	ID                 string         `json:"id"`
	RuleID             string         `json:"ruleId"`
	TenantID           string         `json:"tenantId"`
	Revision           int64          `json:"revision"`
	State              RevisionState  `json:"state"`
	CatalogVersion     string         `json:"catalogVersion"`
	EntryNodeID        string         `json:"entryNodeId"`
	Nodes              []NodeInstance `json:"nodes"`
	Edges              []Edge         `json:"edges"`
	AllowedPermissions []string       `json:"allowedPermissions"`
	MaxNodes           int            `json:"maxNodes"`
	MaxDepth           int            `json:"maxDepth"`
	MaxFanout          int            `json:"maxFanout"`
	MaxResourceCost    int            `json:"maxResourceCost"`
	MaxAttempts        int            `json:"maxAttempts"`
	Digest             string         `json:"digest"`
}

type RuleBinding struct {
	ID             string `json:"id"`
	TenantID       string `json:"tenantId"`
	SiteID         string `json:"siteId,omitempty"`
	Revision       int64  `json:"revision"`
	RuleRevisionID string `json:"ruleRevisionId"`
	Priority       int    `json:"priority"`
}

type RuleEventEnvelope struct {
	EventID          string          `json:"eventId"`
	Schema           string          `json:"schema"`
	TenantID         string          `json:"tenantId"`
	SiteID           string          `json:"siteId,omitempty"`
	SubjectType      string          `json:"subjectType"`
	SubjectID        string          `json:"subjectId"`
	AggregateVersion int64           `json:"aggregateVersion,omitempty"`
	OccurredAt       time.Time       `json:"occurredAt"`
	ReceivedAt       time.Time       `json:"receivedAt"`
	SourcePosition   string          `json:"sourcePosition,omitempty"`
	CausationID      string          `json:"causationId,omitempty"`
	TraceID          string          `json:"traceId,omitempty"`
	Payload          json.RawMessage `json:"payload"`
	PayloadDigest    string          `json:"payloadDigest"`
}

type ExecutionPlan struct {
	Revision RuleRevision
	Catalog  Catalog
	Nodes    map[string]NodeInstance
	Outgoing map[string][]Edge
	Incoming map[string][]Edge
	Digest   string
}

type TypedValue struct {
	Type PortType        `json:"type"`
	Data json.RawMessage `json:"data"`
}

type NodeInput struct {
	Port  string     `json:"port"`
	Value TypedValue `json:"value"`
	Path  string     `json:"path"`
}

type NodeOutput struct {
	Port  string     `json:"port"`
	Value TypedValue `json:"value"`
}

type EffectIntent struct {
	EffectID      string          `json:"effectId"`
	OutputPort    string          `json:"outputPort"`
	OwnerDomain   string          `json:"ownerDomain"`
	IntentType    string          `json:"intentType"`
	Payload       json.RawMessage `json:"payload"`
	PayloadDigest string          `json:"payloadDigest"`
}

type Continuation struct {
	ContinuationID string          `json:"continuationId"`
	WakeAt         time.Time       `json:"wakeAt"`
	OutputPort     string          `json:"outputPort"`
	Payload        json.RawMessage `json:"payload"`
}

type StateTransition struct {
	ScopeKey         string          `json:"scopeKey"`
	SchemaVersion    int             `json:"schemaVersion"`
	ExpectedRevision int64           `json:"expectedRevision"`
	Value            json.RawMessage `json:"value"`
	ExpiresAt        *time.Time      `json:"expiresAt,omitempty"`
}

type NodeOutcome struct {
	Outputs      []NodeOutput     `json:"outputs,omitempty"`
	Effects      []EffectIntent   `json:"effects,omitempty"`
	Continuation *Continuation    `json:"continuation,omitempty"`
	State        *StateTransition `json:"state,omitempty"`
	Failure      FailureClass     `json:"failure,omitempty"`
	FailureCode  string           `json:"failureCode,omitempty"`
}

type NodeContext struct {
	Mode            ExecutionMode
	TenantID        string
	SiteID          string
	SubjectType     string
	SubjectID       string
	ExecutionID     string
	RuleRevisionID  string
	NodeInstanceID  string
	BindingRevision int64
	WorkItemID      string
	OccurredAt      time.Time
	SnapshotReader  SnapshotReader
	StateReader     RuleStateReader
}

type NodeEvaluator func(context.Context, NodeContext, json.RawMessage, NodeInput) (NodeOutcome, error)

type SnapshotReader interface {
	ReadSnapshot(context.Context, SnapshotRequest) (TypedValue, error)
}

type FrozenSnapshotReader interface {
	SnapshotReader
	FrozenFactsRevision() string
}

type SnapshotRequest struct {
	OwnerDomain string
	Kind        string
	TenantID    string
	SiteID      string
	SubjectType string
	SubjectID   string
	Revision    int64
}

type EffectSink interface {
	Deliver(context.Context, EffectIntent) (string, error)
}

type RuleStateKey struct {
	TenantID       string `json:"tenantId"`
	RuleRevisionID string `json:"ruleRevisionId"`
	NodeInstanceID string `json:"nodeInstanceId"`
	ScopeKey       string `json:"scopeKey"`
}

type RuleStateReader interface {
	ReadRuleState(context.Context, RuleStateKey) (RuleStateRecord, bool, error)
}

type RuleStateRecord struct {
	TenantID       string          `json:"tenantId"`
	RuleRevisionID string          `json:"ruleRevisionId"`
	NodeInstanceID string          `json:"nodeInstanceId"`
	ScopeKey       string          `json:"scopeKey"`
	SchemaVersion  int             `json:"schemaVersion"`
	Revision       int64           `json:"revision"`
	Value          json.RawMessage `json:"value"`
	ExpiresAt      *time.Time      `json:"expiresAt,omitempty"`
}

type AppliedStateTransition struct {
	RuleStateKey
	SchemaVersion    int        `json:"schemaVersion"`
	ExpectedRevision int64      `json:"expectedRevision"`
	ResultRevision   int64      `json:"resultRevision"`
	ValueDigest      string     `json:"valueDigest"`
	ExpiresAt        *time.Time `json:"expiresAt,omitempty"`
}

type WorkRecord struct {
	WorkItemID  string
	ExecutionID string
	NodeID      string
	Input       NodeInput
	Attempt     int
	Status      string
	RetryAt     time.Time
	Failure     FailureClass
	FailureCode string
}

type ExecutionRecord struct {
	ExecutionID     string
	TenantID        string
	SiteID          string
	RuleRevisionID  string
	BindingID       string
	BindingRevision int64
	EventID         string
	OrderingKey     string
	Status          ExecutionStatus
	LeaseOwner      string
	LeaseFence      int64
	LeaseUntil      time.Time
	AttemptBudget   int
	CreatedAt       time.Time
	UpdatedAt       time.Time
	TerminalCode    string
}

type TraceRecord struct {
	ExecutionID string
	WorkItemID  string
	NodeID      string
	Attempt     int
	Outcome     NodeOutcome
}
