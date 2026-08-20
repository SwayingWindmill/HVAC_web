package rulemanagement

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/services/rule-runtime-service/internal/ruleruntime"
)

const (
	CatalogVersion = "core.v1"
	maxDraftNodes  = 128
	maxDraftEdges  = 512
	maxDraftDepth  = 128
	maxDraftFanout = 64
	maxDraftCost   = 4096
	maxDraftTries  = 20
)

var ErrValidation = errors.New("rule management validation failed")

type ConfigField struct {
	Name     string   `json:"name"`
	Kind     string   `json:"kind"`
	Required bool     `json:"required"`
	Options  []string `json:"options,omitempty"`
}

type NodeDescriptor struct {
	ID                 string                          `json:"id"`
	Version            int                             `json:"version"`
	Inputs             map[string]ruleruntime.PortType `json:"inputs"`
	Outputs            map[string]ruleruntime.PortType `json:"outputs"`
	RequiredPermission string                          `json:"requiredPermission,omitempty"`
	EffectOwner        string                          `json:"effectOwner,omitempty"`
	StateSchemaVersion int                             `json:"stateSchemaVersion"`
	Deterministic      bool                            `json:"deterministic"`
	ResourceCost       int                             `json:"resourceCost"`
	ConfigFields       []ConfigField                   `json:"configFields"`
}

type CatalogView struct {
	Version     string           `json:"version"`
	Definitions []NodeDescriptor `json:"definitions"`
}

type Draft struct {
	RuleID             string                     `json:"ruleId,omitempty"`
	CatalogVersion     string                     `json:"catalogVersion"`
	EntryNodeID        string                     `json:"entryNodeId"`
	Nodes              []ruleruntime.NodeInstance `json:"nodes"`
	Edges              []ruleruntime.Edge         `json:"edges"`
	AllowedPermissions []string                   `json:"allowedPermissions"`
	MaxNodes           int                        `json:"maxNodes"`
	MaxDepth           int                        `json:"maxDepth"`
	MaxFanout          int                        `json:"maxFanout"`
	MaxResourceCost    int                        `json:"maxResourceCost"`
	MaxAttempts        int                        `json:"maxAttempts"`
}

type ValidationResult struct {
	Valid  bool          `json:"valid"`
	Digest string        `json:"digest,omitempty"`
	Error  *CompileIssue `json:"error,omitempty"`
}

type CompileIssue struct {
	Code   string `json:"code"`
	NodeID string `json:"nodeId,omitempty"`
	Detail string `json:"detail"`
}

type SimulationEvent struct {
	EventID     string          `json:"eventId"`
	Schema      string          `json:"schema"`
	SiteID      string          `json:"siteId,omitempty"`
	SubjectType string          `json:"subjectType"`
	SubjectID   string          `json:"subjectId"`
	OccurredAt  time.Time       `json:"occurredAt"`
	Payload     json.RawMessage `json:"payload"`
}

type FrozenFact struct {
	OwnerDomain string          `json:"ownerDomain"`
	Kind        string          `json:"kind"`
	SiteID      string          `json:"siteId,omitempty"`
	SubjectType string          `json:"subjectType"`
	SubjectID   string          `json:"subjectId"`
	Revision    int64           `json:"revision"`
	Value       json.RawMessage `json:"value"`
}

type SimulationRequest struct {
	Draft               Draft           `json:"draft"`
	Event               SimulationEvent `json:"event"`
	FrozenFactsRevision string          `json:"frozenFactsRevision,omitempty"`
	FrozenFacts         []FrozenFact    `json:"frozenFacts,omitempty"`
}

type SimulationResult struct {
	Status       ruleruntime.ExecutionStatus `json:"status"`
	TerminalCode string                      `json:"terminalCode,omitempty"`
	Trace        []ruleruntime.TraceRecord   `json:"trace"`
	Effects      []ruleruntime.EffectRecord  `json:"effects"`
}

type AssignmentRequest struct {
	BindingID      string `json:"bindingId,omitempty"`
	SiteID         string `json:"siteId"`
	RuleRevisionID string `json:"ruleRevisionId"`
	Priority       int    `json:"priority"`
}

type BindingView struct {
	ruleruntime.RuleBinding
	Active    bool       `json:"active"`
	CreatedAt time.Time  `json:"createdAt"`
	RetiredAt *time.Time `json:"retiredAt,omitempty"`
}

type RetirementRequest struct {
	SiteID string `json:"siteId"`
	Reason string `json:"reason"`
}

type ExecutionEvidence struct {
	ExecutionID     string                      `json:"executionId"`
	SiteID          string                      `json:"siteId,omitempty"`
	RuleRevisionID  string                      `json:"ruleRevisionId"`
	BindingID       string                      `json:"bindingId"`
	BindingRevision int64                       `json:"bindingRevision"`
	Status          ruleruntime.ExecutionStatus `json:"status"`
	TerminalCode    string                      `json:"terminalCode,omitempty"`
	Trace           []ruleruntime.TraceRecord   `json:"trace"`
	Effects         []ruleruntime.EffectRecord  `json:"effects"`
	UpdatedAt       time.Time                   `json:"updatedAt"`
}

type Store interface {
	Release(context.Context, string, Draft, time.Time) (ruleruntime.RuleRevision, error)
	ListRevisions(context.Context, string, string) ([]ruleruntime.RuleRevision, error)
	AppendBinding(context.Context, string, AssignmentRequest, time.Time) (BindingView, error)
	ListBindings(context.Context, string, string) ([]BindingView, error)
	RetireBinding(context.Context, string, string, string, string, time.Time) (BindingView, error)
	ListExecutionEvidence(context.Context, string, string, int) ([]ExecutionEvidence, error)
}

type Manager struct {
	store Store
	now   func() time.Time
}

func NewManager(store Store, now func() time.Time) (*Manager, error) {
	if store == nil {
		return nil, errors.New("rule management store is required")
	}
	if now == nil {
		now = time.Now
	}
	return &Manager{store: store, now: now}, nil
}

func (m *Manager) Catalog() CatalogView {
	catalog := ruleruntime.CoreCatalogV1()
	ids := make([]string, 0, len(catalog.Definitions))
	for id := range catalog.Definitions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	definitions := make([]NodeDescriptor, 0, len(ids))
	for _, id := range ids {
		definition := catalog.Definitions[id]
		definitions = append(definitions, NodeDescriptor{
			ID: definition.ID, Version: definition.Version, Inputs: definition.Inputs, Outputs: definition.Outputs,
			RequiredPermission: definition.RequiredPermission, EffectOwner: definition.EffectOwner,
			StateSchemaVersion: definition.StateSchemaVersion, Deterministic: definition.Deterministic,
			ResourceCost: definition.ResourceCost, ConfigFields: configFields(id),
		})
	}
	return CatalogView{Version: catalog.Version, Definitions: definitions}
}

func (m *Manager) Validate(tenantID string, draft Draft) ValidationResult {
	normalized, err := normalizeDraft(draft)
	if err != nil {
		return ValidationResult{Valid: false, Error: compileIssue(err)}
	}
	plan, err := compileDraft(tenantID, normalized, ruleruntime.RevisionValidated)
	if err != nil {
		return ValidationResult{Valid: false, Error: compileIssue(err)}
	}
	return ValidationResult{Valid: true, Digest: plan.Digest}
}

func (m *Manager) Release(ctx context.Context, tenantID string, draft Draft) (ruleruntime.RuleRevision, error) {
	normalized, err := normalizeDraft(draft)
	if err != nil {
		return ruleruntime.RuleRevision{}, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	if _, err := compileDraft(tenantID, normalized, ruleruntime.RevisionValidated); err != nil {
		return ruleruntime.RuleRevision{}, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	return m.store.Release(ctx, tenantID, normalized, m.now().UTC())
}

func (m *Manager) ListRevisions(ctx context.Context, tenantID, ruleID string) ([]ruleruntime.RuleRevision, error) {
	return m.store.ListRevisions(ctx, tenantID, strings.TrimSpace(ruleID))
}

func (m *Manager) Assign(ctx context.Context, tenantID string, request AssignmentRequest) (BindingView, error) {
	if strings.TrimSpace(request.SiteID) == "" || strings.TrimSpace(request.RuleRevisionID) == "" {
		return BindingView{}, fmt.Errorf("%w: siteId and ruleRevisionId are required", ErrValidation)
	}
	if request.Priority < -1000 || request.Priority > 1000 {
		return BindingView{}, fmt.Errorf("%w: priority must be within -1000..1000", ErrValidation)
	}
	return m.store.AppendBinding(ctx, tenantID, request, m.now().UTC())
}

func (m *Manager) ListBindings(ctx context.Context, tenantID, siteID string) ([]BindingView, error) {
	return m.store.ListBindings(ctx, tenantID, strings.TrimSpace(siteID))
}

func (m *Manager) Retire(ctx context.Context, tenantID, bindingID string, request RetirementRequest) (BindingView, error) {
	request.SiteID = strings.TrimSpace(request.SiteID)
	request.Reason = strings.TrimSpace(request.Reason)
	if request.SiteID == "" || request.Reason == "" || len(request.Reason) > 256 {
		return BindingView{}, fmt.Errorf("%w: siteId and a retirement reason of at most 256 characters are required", ErrValidation)
	}
	return m.store.RetireBinding(ctx, tenantID, bindingID, request.SiteID, request.Reason, m.now().UTC())
}

func (m *Manager) Evidence(ctx context.Context, tenantID, siteID string, limit int) ([]ExecutionEvidence, error) {
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 100 {
		return nil, fmt.Errorf("%w: evidence limit must be within 1..100", ErrValidation)
	}
	return m.store.ListExecutionEvidence(ctx, tenantID, strings.TrimSpace(siteID), limit)
}

func (m *Manager) Simulate(ctx context.Context, tenantID string, request SimulationRequest) (SimulationResult, error) {
	normalized, err := normalizeDraft(request.Draft)
	if err != nil {
		return SimulationResult{}, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	plan, err := compileDraft(tenantID, normalized, ruleruntime.RevisionReleased)
	if err != nil {
		return SimulationResult{}, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	if request.Event.EventID == "" || request.Event.Schema == "" || request.Event.SubjectType == "" || request.Event.SubjectID == "" || request.Event.OccurredAt.IsZero() || !json.Valid(request.Event.Payload) {
		return SimulationResult{}, fmt.Errorf("%w: simulation event identity, schema, subject, occurredAt and valid payload are required", ErrValidation)
	}
	if len(request.FrozenFacts) > 100 {
		return SimulationResult{}, fmt.Errorf("%w: frozenFacts must not exceed 100 items", ErrValidation)
	}
	var snapshots ruleruntime.SnapshotReader
	if len(request.FrozenFacts) > 0 || request.FrozenFactsRevision != "" {
		reader, err := newFrozenReader(request.FrozenFactsRevision, request.FrozenFacts)
		if err != nil {
			return SimulationResult{}, fmt.Errorf("%w: %v", ErrValidation, err)
		}
		snapshots = reader
	}
	store := ruleruntime.NewMemoryStore()
	runtime, err := ruleruntime.NewRuntime(plan, store, snapshots, nil, ruleruntime.ModeReplay)
	if err != nil {
		return SimulationResult{}, err
	}
	event := ruleruntime.RuleEventEnvelope{
		EventID: request.Event.EventID, Schema: request.Event.Schema, TenantID: tenantID, SiteID: request.Event.SiteID,
		SubjectType: request.Event.SubjectType, SubjectID: request.Event.SubjectID,
		OccurredAt: request.Event.OccurredAt.UTC(), ReceivedAt: m.now().UTC(), Payload: append(json.RawMessage(nil), request.Event.Payload...),
	}
	binding := ruleruntime.RuleBinding{ID: "simulation-binding", TenantID: tenantID, SiteID: request.Event.SiteID, Revision: 1, RuleRevisionID: plan.Revision.ID, Priority: 0}
	seed, _, err := runtime.Start(ctx, binding, event, m.now().UTC())
	if err != nil {
		return SimulationResult{}, err
	}
	result, err := runtime.Run(ctx, seed.Execution.ExecutionID, "rule-management-simulator", m.now().UTC())
	if err != nil {
		return SimulationResult{}, err
	}
	return SimulationResult{Status: result.Execution.Status, TerminalCode: result.Execution.TerminalCode, Trace: result.Trace, Effects: result.Effects}, nil
}

func compileDraft(tenantID string, draft Draft, state ruleruntime.RevisionState) (ruleruntime.ExecutionPlan, error) {
	if strings.TrimSpace(tenantID) == "" {
		return ruleruntime.ExecutionPlan{}, errors.New("tenantId is required")
	}
	if draft.CatalogVersion == "" {
		draft.CatalogVersion = CatalogVersion
	}
	revision := ruleruntime.RuleRevision{
		ID: "draft-revision", RuleID: firstNonEmpty(strings.TrimSpace(draft.RuleID), "draft-rule"), TenantID: tenantID,
		Revision: 1, State: state, CatalogVersion: draft.CatalogVersion, EntryNodeID: draft.EntryNodeID,
		Nodes: cloneNodes(draft.Nodes), Edges: append([]ruleruntime.Edge(nil), draft.Edges...),
		AllowedPermissions: append([]string(nil), draft.AllowedPermissions...),
		MaxNodes:           draft.MaxNodes, MaxDepth: draft.MaxDepth, MaxFanout: draft.MaxFanout,
		MaxResourceCost: draft.MaxResourceCost, MaxAttempts: draft.MaxAttempts,
	}
	return ruleruntime.Compile(revision, ruleruntime.CoreCatalogV1())
}

func normalizeDraft(draft Draft) (Draft, error) {
	draft.RuleID = strings.TrimSpace(draft.RuleID)
	if draft.RuleID != "" && !isUUIDv7(draft.RuleID) {
		return Draft{}, ErrInvalidIdentity
	}
	if draft.CatalogVersion != CatalogVersion {
		return Draft{}, errors.New("catalogVersion must be core.v1")
	}
	if len(draft.Nodes) == 0 || len(draft.Nodes) > maxDraftNodes {
		return Draft{}, fmt.Errorf("node count must be within 1..%d", maxDraftNodes)
	}
	if len(draft.Edges) > maxDraftEdges {
		return Draft{}, fmt.Errorf("edge count must not exceed %d", maxDraftEdges)
	}
	if draft.MaxNodes < 1 || draft.MaxNodes > maxDraftNodes ||
		draft.MaxDepth < 1 || draft.MaxDepth > maxDraftDepth ||
		draft.MaxFanout < 1 || draft.MaxFanout > maxDraftFanout ||
		draft.MaxResourceCost < 1 || draft.MaxResourceCost > maxDraftCost ||
		draft.MaxAttempts < 1 || draft.MaxAttempts > maxDraftTries {
		return Draft{}, errors.New("Rule execution budgets exceed the public contract")
	}
	if len(draft.EntryNodeID) == 0 || len(draft.EntryNodeID) > 128 {
		return Draft{}, errors.New("entryNodeId must contain 1..128 characters")
	}
	catalog := ruleruntime.CoreCatalogV1()
	permissions := map[string]struct{}{}
	for _, node := range draft.Nodes {
		if len(node.ID) == 0 || len(node.ID) > 128 || len(node.DefinitionID) == 0 || len(node.DefinitionID) > 128 {
			return Draft{}, errors.New("Rule node identities must contain 1..128 characters")
		}
		definition, ok := catalog.Definitions[node.DefinitionID]
		if ok && definition.RequiredPermission != "" {
			permissions[definition.RequiredPermission] = struct{}{}
		}
	}
	for _, edge := range draft.Edges {
		if len(edge.FromNode) == 0 || len(edge.FromNode) > 128 || len(edge.FromPort) == 0 || len(edge.FromPort) > 128 ||
			len(edge.ToNode) == 0 || len(edge.ToNode) > 128 || len(edge.ToPort) == 0 || len(edge.ToPort) > 128 {
			return Draft{}, errors.New("Rule edge identities must contain 1..128 characters")
		}
	}
	draft.AllowedPermissions = make([]string, 0, len(permissions))
	for permission := range permissions {
		draft.AllowedPermissions = append(draft.AllowedPermissions, permission)
	}
	sort.Strings(draft.AllowedPermissions)
	return draft, nil
}

func compileIssue(err error) *CompileIssue {
	var issue *ruleruntime.CompileError
	if errors.As(err, &issue) {
		return &CompileIssue{Code: issue.Code, NodeID: issue.NodeID, Detail: issue.Detail}
	}
	return &CompileIssue{Code: "RULE_DRAFT_INVALID", Detail: err.Error()}
}

func configFields(definitionID string) []ConfigField {
	switch definitionID {
	case "event_type_filter":
		return []ConfigField{{Name: "schemas", Kind: "STRING_LIST", Required: true}}
	case "json_number":
		return []ConfigField{{Name: "key", Kind: "STRING", Required: true}}
	case "math_number":
		return []ConfigField{{Name: "operation", Kind: "ENUM", Required: true, Options: []string{"ADD", "SUBTRACT", "MULTIPLY", "DIVIDE"}}, {Name: "operand", Kind: "NUMBER", Required: true}}
	case "owner_snapshot_read":
		return []ConfigField{{Name: "ownerDomain", Kind: "STRING", Required: true}, {Name: "kind", Kind: "STRING", Required: true}, {Name: "revision", Kind: "POSITIVE_INTEGER", Required: true}}
	case "alarm_intent":
		return []ConfigField{{Name: "intentType", Kind: "ENUM", Required: true, Options: []string{"ALARM_CONDITION_OBSERVATION", "ALARM_PUBLICATION"}}}
	case "delay_event":
		return []ConfigField{{Name: "delayMillis", Kind: "POSITIVE_INTEGER", Required: true}}
	default:
		return []ConfigField{}
	}
}

func cloneNodes(values []ruleruntime.NodeInstance) []ruleruntime.NodeInstance {
	result := make([]ruleruntime.NodeInstance, len(values))
	for index, value := range values {
		result[index] = value
		result[index].Config = append(json.RawMessage(nil), value.Config...)
	}
	return result
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

type frozenReader struct {
	revision string
	facts    map[string]json.RawMessage
}

func newFrozenReader(revision string, facts []FrozenFact) (*frozenReader, error) {
	revision = strings.TrimSpace(revision)
	if revision == "" {
		return nil, errors.New("frozenFactsRevision is required when simulation facts are supplied")
	}
	reader := &frozenReader{revision: revision, facts: make(map[string]json.RawMessage, len(facts))}
	for _, fact := range facts {
		if fact.OwnerDomain == "" || fact.Kind == "" || fact.SubjectType == "" || fact.SubjectID == "" || fact.Revision <= 0 || !json.Valid(fact.Value) {
			return nil, errors.New("frozen simulation fact is invalid")
		}
		key := frozenFactKey(fact.OwnerDomain, fact.Kind, fact.SiteID, fact.SubjectType, fact.SubjectID, fact.Revision)
		if _, exists := reader.facts[key]; exists {
			return nil, errors.New("frozen simulation fact identity is duplicated")
		}
		reader.facts[key] = append(json.RawMessage(nil), fact.Value...)
	}
	return reader, nil
}

func (r *frozenReader) FrozenFactsRevision() string { return r.revision }

func (r *frozenReader) ReadSnapshot(_ context.Context, request ruleruntime.SnapshotRequest) (ruleruntime.TypedValue, error) {
	key := frozenFactKey(request.OwnerDomain, request.Kind, request.SiteID, request.SubjectType, request.SubjectID, request.Revision)
	value, ok := r.facts[key]
	if !ok {
		return ruleruntime.TypedValue{}, errors.New("frozen simulation fact not found")
	}
	return ruleruntime.TypedValue{Type: ruleruntime.PortSnapshot, Data: append(json.RawMessage(nil), value...)}, nil
}

func frozenFactKey(owner, kind, siteID, subjectType, subjectID string, revision int64) string {
	return fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s\x00%d", owner, kind, siteID, subjectType, subjectID, revision)
}
