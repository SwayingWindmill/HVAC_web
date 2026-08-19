package ruleruntime

import (
	"encoding/json"
	"testing"
)

func TestCompileRejectsInvalidReleasedGraphs(t *testing.T) {
	catalog := CoreCatalogV1()
	base := validNumberRule()

	tests := []struct {
		name   string
		mutate func(*RuleRevision)
		code   string
	}{
		{
			name: "unreachable",
			mutate: func(rule *RuleRevision) {
				rule.Nodes = append(rule.Nodes, NodeInstance{ID: "orphan", DefinitionID: "terminal_number", Config: json.RawMessage(`{}`)})
			},
			code: "NODE_UNREACHABLE",
		},
		{
			name: "cycle",
			mutate: func(rule *RuleRevision) {
				rule.Nodes = []NodeInstance{
					{ID: "a", DefinitionID: "math_number", Config: json.RawMessage(`{"operation":"ADD","operand":1}`)},
					{ID: "b", DefinitionID: "math_number", Config: json.RawMessage(`{"operation":"ADD","operand":1}`)},
				}
				rule.EntryNodeID = "a"
				rule.Edges = []Edge{{FromNode: "a", FromPort: "value", ToNode: "b", ToPort: "in"}, {FromNode: "b", FromPort: "value", ToNode: "a", ToPort: "in"}}
			},
			code: "GRAPH_CYCLE",
		},
		{
			name: "type mismatch",
			mutate: func(rule *RuleRevision) {
				rule.Nodes = []NodeInstance{
					{ID: "read", DefinitionID: "owner_snapshot_read", Config: json.RawMessage(`{"ownerDomain":"TELEMETRY","kind":"POINT_CURRENT","revision":7}`)},
					{ID: "end", DefinitionID: "terminal_event", Config: json.RawMessage(`{}`)},
				}
				rule.EntryNodeID = "read"
				rule.AllowedPermissions = []string{"owner.snapshot.read"}
				rule.Edges = []Edge{{FromNode: "read", FromPort: "snapshot", ToNode: "end", ToPort: "in"}}
			},
			code: "PORT_TYPE_MISMATCH",
		},
		{
			name: "entry port",
			mutate: func(rule *RuleRevision) {
				rule.Nodes = []NodeInstance{
					{ID: "math", DefinitionID: "math_number", Config: json.RawMessage(`{"operation":"ADD","operand":1}`)},
					{ID: "end", DefinitionID: "terminal_number", Config: json.RawMessage(`{}`)},
				}
				rule.EntryNodeID = "math"
				rule.Edges = []Edge{{FromNode: "math", FromPort: "value", ToNode: "end", ToPort: "in"}}
			},
			code: "ENTRY_PORT_INVALID",
		},
		{
			name: "resource budget",
			mutate: func(rule *RuleRevision) {
				rule.MaxResourceCost = 1
			},
			code: "RESOURCE_BUDGET_EXCEEDED",
		},
		{
			name: "permission",
			mutate: func(rule *RuleRevision) {
				rule.Nodes = []NodeInstance{
					{ID: "read", DefinitionID: "owner_snapshot_read", Config: json.RawMessage(`{"ownerDomain":"TELEMETRY","kind":"POINT_CURRENT","revision":7}`)},
					{ID: "end", DefinitionID: "terminal_intent", Config: json.RawMessage(`{}`)},
				}
				rule.EntryNodeID = "read"
				rule.Edges = nil
				rule.AllowedPermissions = nil
			},
			code: "PERMISSION_DENIED",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := cloneRule(base)
			tt.mutate(&rule)
			_, err := Compile(rule, catalog)
			if got := CompileErrorCode(err); got != tt.code {
				t.Fatalf("CompileErrorCode() = %q, want %q; err=%v", got, tt.code, err)
			}
		})
	}
}

func TestCompileProducesStableDigestIndependentOfAuthorOrdering(t *testing.T) {
	catalog := CoreCatalogV1()
	first := validNumberRule()
	second := cloneRule(first)
	second.Nodes[0], second.Nodes[1] = second.Nodes[1], second.Nodes[0]

	planA, err := Compile(first, catalog)
	if err != nil {
		t.Fatal(err)
	}
	planB, err := Compile(second, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if planA.Digest != planB.Digest {
		t.Fatalf("digest changed with author ordering: %s != %s", planA.Digest, planB.Digest)
	}
}

func validNumberRule() RuleRevision {
	return RuleRevision{
		ID: "0198c1e0-rule-revision", RuleID: "0198c1e0-rule", TenantID: "0198c1e0-tenant", Revision: 1,
		State: RevisionReleased, CatalogVersion: "core.v1", EntryNodeID: "extract",
		Nodes: []NodeInstance{
			{ID: "extract", DefinitionID: "json_number", Config: json.RawMessage(`{"key":"value"}`)},
			{ID: "math", DefinitionID: "math_number", Config: json.RawMessage(`{"operation":"ADD","operand":1}`)},
			{ID: "end", DefinitionID: "terminal_number", Config: json.RawMessage(`{}`)},
		},
		Edges: []Edge{
			{FromNode: "extract", FromPort: "value", ToNode: "math", ToPort: "in"},
			{FromNode: "math", FromPort: "value", ToNode: "end", ToPort: "in"},
		},
		MaxNodes: 8, MaxDepth: 8, MaxFanout: 4, MaxResourceCost: 8, MaxAttempts: 3,
	}
}

func cloneRule(rule RuleRevision) RuleRevision {
	payload, _ := json.Marshal(rule)
	var cloned RuleRevision
	_ = json.Unmarshal(payload, &cloned)
	return cloned
}
