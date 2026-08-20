package rulemanagement

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/services/rule-runtime-service/internal/ruleruntime"
)

type fakeStore struct {
	releaseCalls int
}

func (s *fakeStore) Release(_ context.Context, tenantID string, draft Draft, _ time.Time) (ruleruntime.RuleRevision, error) {
	s.releaseCalls++
	return ruleruntime.RuleRevision{ID: "released", TenantID: tenantID, RuleID: draft.RuleID, Revision: 1, State: ruleruntime.RevisionReleased}, nil
}
func (*fakeStore) ListRevisions(context.Context, string, string) ([]ruleruntime.RuleRevision, error) {
	return nil, nil
}
func (*fakeStore) AppendBinding(context.Context, string, AssignmentRequest, time.Time) (BindingView, error) {
	return BindingView{}, nil
}
func (*fakeStore) ListBindings(context.Context, string, string) ([]BindingView, error) {
	return nil, nil
}
func (*fakeStore) RetireBinding(context.Context, string, string, string, string, time.Time) (BindingView, error) {
	return BindingView{}, nil
}
func (*fakeStore) ListExecutionEvidence(context.Context, string, string, int) ([]ExecutionEvidence, error) {
	return nil, nil
}

func TestInvalidGraphCannotRelease(t *testing.T) {
	store := &fakeStore{}
	manager, err := NewManager(store, func() time.Time { return time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC) })
	if err != nil {
		t.Fatal(err)
	}
	draft := validEventDraft()
	draft.Edges = nil
	if _, err := manager.Release(context.Background(), testTenantID, draft); err == nil {
		t.Fatal("invalid graph released")
	}
	if store.releaseCalls != 0 {
		t.Fatalf("invalid graph reached release store %d times", store.releaseCalls)
	}
}

func TestSimulationWithEffectUsesReplaySinkOnly(t *testing.T) {
	manager, _ := NewManager(&fakeStore{}, func() time.Time { return time.Date(2026, 8, 20, 0, 0, 1, 0, time.UTC) })
	draft := Draft{
		CatalogVersion: CatalogVersion,
		EntryNodeID:    "snapshot",
		Nodes: []ruleruntime.NodeInstance{
			{ID: "snapshot", DefinitionID: "owner_snapshot_read", Config: json.RawMessage(`{"ownerDomain":"TELEMETRY","kind":"POINT","revision":1}`)},
			{ID: "alarm", DefinitionID: "alarm_intent", Config: json.RawMessage(`{"intentType":"ALARM_PUBLICATION"}`)},
			{ID: "terminal", DefinitionID: "terminal_intent", Config: json.RawMessage(`{}`)},
		},
		Edges: []ruleruntime.Edge{
			{FromNode: "snapshot", FromPort: "snapshot", ToNode: "alarm", ToPort: "in"},
			{FromNode: "alarm", FromPort: "intent", ToNode: "terminal", ToPort: "in"},
		},
		AllowedPermissions: []string{"owner.snapshot.read", "alarm.intent.publish"},
		MaxNodes:           8, MaxDepth: 8, MaxFanout: 4, MaxResourceCost: 16, MaxAttempts: 3,
	}
	now := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	result, err := manager.Simulate(context.Background(), testTenantID, SimulationRequest{
		Draft:               draft,
		Event:               SimulationEvent{EventID: "sim-event", Schema: "telemetry.point.observed.v1", SiteID: testSiteID, SubjectType: "POINT", SubjectID: testPointID, OccurredAt: now, Payload: json.RawMessage(`{"value":42}`)},
		FrozenFactsRevision: "facts-1",
		FrozenFacts:         []FrozenFact{{OwnerDomain: "TELEMETRY", Kind: "POINT", SiteID: testSiteID, SubjectType: "POINT", SubjectID: testPointID, Revision: 1, Value: json.RawMessage(`{"quality":"GOOD","value":42}`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != ruleruntime.ExecutionSucceeded {
		t.Fatalf("simulation status = %s, want SUCCEEDED", result.Status)
	}
	if len(result.Effects) != 1 || result.Effects[0].Status != "SIMULATED" {
		t.Fatalf("simulation effects = %#v", result.Effects)
	}
}

func TestCatalogExposesOnlyTypedCoreDefinitions(t *testing.T) {
	manager, _ := NewManager(&fakeStore{}, time.Now)
	catalog := manager.Catalog()
	if catalog.Version != CatalogVersion || len(catalog.Definitions) != 9 {
		t.Fatalf("catalog = %#v", catalog)
	}
	for _, definition := range catalog.Definitions {
		if !definition.Deterministic || definition.ID == "script" || definition.ID == "tbel" || definition.ID == "class" {
			t.Fatalf("unsafe catalog definition = %#v", definition)
		}
		for _, field := range definition.ConfigFields {
			if field.Name == "credential" || field.Name == "script" || field.Name == "className" {
				t.Fatalf("unsafe config field = %#v", field)
			}
		}
	}
}

func TestDraftAuthorityIsDerivedAndPublicBudgetsAreServerEnforced(t *testing.T) {
	draft := Draft{
		CatalogVersion: CatalogVersion,
		EntryNodeID:    "snapshot",
		Nodes: []ruleruntime.NodeInstance{
			{ID: "snapshot", DefinitionID: "owner_snapshot_read", Config: json.RawMessage(`{"ownerDomain":"TELEMETRY","kind":"POINT","revision":1}`)},
			{ID: "alarm", DefinitionID: "alarm_intent", Config: json.RawMessage(`{"intentType":"ALARM_PUBLICATION"}`)},
			{ID: "terminal", DefinitionID: "terminal_intent", Config: json.RawMessage(`{}`)},
		},
		Edges: []ruleruntime.Edge{
			{FromNode: "snapshot", FromPort: "snapshot", ToNode: "alarm", ToPort: "in"},
			{FromNode: "alarm", FromPort: "intent", ToNode: "terminal", ToPort: "in"},
		},
		AllowedPermissions: []string{"browser.invented.permission"},
		MaxNodes:           8, MaxDepth: 8, MaxFanout: 4, MaxResourceCost: 16, MaxAttempts: 3,
	}
	normalized, err := normalizeDraft(draft)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := normalized.AllowedPermissions, []string{"alarm.intent.publish", "owner.snapshot.read"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("derived permissions = %#v, want %#v", got, want)
	}
	if draft.AllowedPermissions[0] != "browser.invented.permission" {
		t.Fatalf("normalization mutated caller permissions = %#v", draft.AllowedPermissions)
	}
	for _, mutate := range []func(*Draft){
		func(value *Draft) { value.MaxNodes = maxDraftNodes + 1 },
		func(value *Draft) { value.MaxDepth = maxDraftDepth + 1 },
		func(value *Draft) { value.MaxFanout = maxDraftFanout + 1 },
		func(value *Draft) { value.MaxResourceCost = maxDraftCost + 1 },
		func(value *Draft) { value.MaxAttempts = maxDraftTries + 1 },
	} {
		invalid := draft
		mutate(&invalid)
		if _, err := normalizeDraft(invalid); err == nil {
			t.Fatal("out-of-contract Rule budget was accepted")
		}
	}
}

func TestAssignmentPriorityIsServerBounded(t *testing.T) {
	manager, _ := NewManager(&fakeStore{}, time.Now)
	_, err := manager.Assign(context.Background(), testTenantID, AssignmentRequest{SiteID: testSiteID, RuleRevisionID: testPointID, Priority: 1001})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("priority error = %v, want validation error", err)
	}
}

func TestBindingStreamCannotChangeSiteOrRuleFamily(t *testing.T) {
	head := &bindingStreamHead{SiteID: testSiteID, RuleID: "0198c1e0-1004-7000-8000-000000000004", Revision: 3}
	if revision, err := nextBindingRevision(head, head.SiteID, head.RuleID); err != nil || revision != 4 {
		t.Fatalf("same binding stream next revision = %d, %v", revision, err)
	}
	if _, err := nextBindingRevision(head, "0198c1e0-1005-7000-8000-000000000005", head.RuleID); !errors.Is(err, ErrConflict) {
		t.Fatalf("Site drift error = %v, want conflict", err)
	}
	if _, err := nextBindingRevision(head, head.SiteID, "0198c1e0-1006-7000-8000-000000000006"); !errors.Is(err, ErrConflict) {
		t.Fatalf("Rule family drift error = %v, want conflict", err)
	}
}

func TestFrozenReaderRequiresExactOwnerFact(t *testing.T) {
	reader, err := newFrozenReader("facts-1", []FrozenFact{{OwnerDomain: "TELEMETRY", Kind: "POINT", SiteID: testSiteID, SubjectType: "POINT", SubjectID: testPointID, Revision: 1, Value: json.RawMessage(`{"value":1}`)}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = reader.ReadSnapshot(context.Background(), ruleruntime.SnapshotRequest{OwnerDomain: "TELEMETRY", Kind: "POINT", SiteID: testSiteID, SubjectType: "POINT", SubjectID: testPointID, Revision: 2})
	if err == nil {
		t.Fatal("missing frozen revision was accepted")
	}
}

func validEventDraft() Draft {
	return Draft{
		CatalogVersion: CatalogVersion,
		EntryNodeID:    "filter",
		Nodes: []ruleruntime.NodeInstance{
			{ID: "filter", DefinitionID: "event_type_filter", Config: json.RawMessage(`{"schemas":["telemetry.point.observed.v1"]}`)},
			{ID: "terminal-match", DefinitionID: "terminal_event", Config: json.RawMessage(`{}`)},
			{ID: "terminal-no-match", DefinitionID: "terminal_event", Config: json.RawMessage(`{}`)},
		},
		Edges: []ruleruntime.Edge{
			{FromNode: "filter", FromPort: "match", ToNode: "terminal-match", ToPort: "in"},
			{FromNode: "filter", FromPort: "no_match", ToNode: "terminal-no-match", ToPort: "in"},
		},
		MaxNodes: 8, MaxDepth: 8, MaxFanout: 4, MaxResourceCost: 16, MaxAttempts: 3,
	}
}

const (
	testTenantID = "0198c1e0-1001-7000-8000-000000000001"
	testSiteID   = "0198c1e0-1002-7000-8000-000000000002"
	testPointID  = "0198c1e0-1003-7000-8000-000000000003"
)
