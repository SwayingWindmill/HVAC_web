package rulegraph

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestValidateDAG_EmptyAndDisconnectedRoots(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input []Node
	}{
		{name: "nil slice", input: nil},
		{name: "empty slice", input: []Node{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report, err := ValidateDAG(tc.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(report.Order) != 0 {
				t.Fatalf("expected empty order, got %v", report.Order)
			}
			if report.MaxHopDepth != 0 {
				t.Fatalf("expected MaxHopDepth 0, got %d", report.MaxHopDepth)
			}
			if len(report.Nodes) != 0 {
				t.Fatalf("expected empty node metrics, got %v", report.Nodes)
			}
		})
	}

	report, err := ValidateDAG([]Node{
		{ID: "rule-c", ReadDevices: []string{"dev-3"}},
		{ID: "rule-a", ReadDevices: []string{"dev-1"}},
		{ID: "rule-b", ReadDevices: []string{"dev-2"}},
	})
	if err != nil {
		t.Fatalf("unexpected error for disconnected roots: %v", err)
	}
	if report.MaxHopDepth != 0 {
		t.Fatalf("expected MaxHopDepth 0, got %d", report.MaxHopDepth)
	}
	wantOrder := []string{"rule-a", "rule-b", "rule-c"}
	if !reflect.DeepEqual(report.Order, wantOrder) {
		t.Fatalf("order=%v, want %v", report.Order, wantOrder)
	}
	for _, id := range wantOrder {
		m, ok := report.Nodes[id]
		if !ok {
			t.Fatalf("missing metrics for %s", id)
		}
		if m.ID != id || m.HopDepth != 0 || len(m.UpstreamIDs) != 0 || len(m.DownstreamIDs) != 0 {
			t.Fatalf("unexpected root metrics for %s: %#v", id, m)
		}
	}
}

func TestValidateDAG_MaxLinkageHopsBoundary(t *testing.T) {
	// 4-node explicit chain: depth 3 == MaxLinkageHops passes, with a shortcut branch
	// verifying longest-path calculation rather than shortest-path.
	validReport, err := ValidateDAG([]Node{
		{ID: "r0"},
		{ID: "r1", DependsOn: []string{"r0"}},
		{ID: "r2", DependsOn: []string{"r1"}},
		{ID: "r3", DependsOn: []string{"r0", "r2"}},
		{ID: "side", DependsOn: []string{"r0"}},
	})
	if err != nil {
		t.Fatalf("expected depth %d DAG to pass, got error: %v", MaxLinkageHops, err)
	}
	if validReport.MaxHopDepth != MaxLinkageHops {
		t.Fatalf("MaxHopDepth=%d, want %d", validReport.MaxHopDepth, MaxLinkageHops)
	}
	if validReport.Nodes["r3"].HopDepth != 3 || validReport.Nodes["side"].HopDepth != 1 {
		t.Fatalf("unexpected hop depths: r3=%d side=%d", validReport.Nodes["r3"].HopDepth, validReport.Nodes["side"].HopDepth)
	}
	assertTopologicalInvariants(t, validReport)

	// 5-node chain (depth 4 > MaxLinkageHops) fails with ErrMaxLinkageHopsExceeded.
	_, err = ValidateDAG([]Node{
		{ID: "r0"},
		{ID: "r1", DependsOn: []string{"r0"}},
		{ID: "r2", DependsOn: []string{"r1"}},
		{ID: "r3", DependsOn: []string{"r2"}},
		{ID: "r4", DependsOn: []string{"r3"}},
	})
	if !errors.Is(err, ErrMaxLinkageHopsExceeded) {
		t.Fatalf("expected ErrMaxLinkageHopsExceeded, got %v", err)
	}

	// Implicit cross-device 4-hop chain (5 nodes) also trips ErrMaxLinkageHopsExceeded.
	_, err = ValidateDAG([]Node{
		{ID: "r0", ReadDevices: []string{"d0"}, WriteDevices: []string{"d1"}},
		{ID: "r1", ReadDevices: []string{"d1"}, WriteDevices: []string{"d2"}},
		{ID: "r2", ReadDevices: []string{"d2"}, WriteDevices: []string{"d3"}},
		{ID: "r3", ReadDevices: []string{"d3"}, WriteDevices: []string{"d4"}},
		{ID: "r4", ReadDevices: []string{"d4"}},
	})
	if !errors.Is(err, ErrMaxLinkageHopsExceeded) {
		t.Fatalf("expected ErrMaxLinkageHopsExceeded for implicit chain, got %v", err)
	}
}

func TestValidateDAG_ExplicitCycles(t *testing.T) {
	t.Run("2-node cycle", func(t *testing.T) {
		_, err := ValidateDAG([]Node{
			{ID: "rule-a", DependsOn: []string{"rule-b"}},
			{ID: "rule-b", DependsOn: []string{"rule-a"}},
		})
		if !errors.Is(err, ErrRuleCycleDetected) {
			t.Fatalf("expected ErrRuleCycleDetected, got %v", err)
		}
		if !strings.Contains(err.Error(), "rule-a -> rule-b -> rule-a") {
			t.Fatalf("expected readable 2-node cycle chain, got %q", err.Error())
		}
	})

	t.Run("3-node cycle excludes non-cycle lead-in and uses Code labels", func(t *testing.T) {
		_, err := ValidateDAG([]Node{
			{ID: "id-root", Code: "ROOT"},
			{ID: "id-a", Code: "R_A", DependsOn: []string{"id-root", "id-c"}},
			{ID: "id-b", Code: "R_B", DependsOn: []string{"id-a"}},
			{ID: "id-c", Code: "R_C", DependsOn: []string{"id-b"}},
			{ID: "id-leaf", Code: "LEAF", DependsOn: []string{"id-c"}},
		})
		if !errors.Is(err, ErrRuleCycleDetected) {
			t.Fatalf("expected ErrRuleCycleDetected, got %v", err)
		}
		if !strings.Contains(err.Error(), "R_A -> R_B -> R_C -> R_A") {
			t.Fatalf("expected exact 3-node cycle chain, got %q", err.Error())
		}
		if strings.Contains(err.Error(), "ROOT") || strings.Contains(err.Error(), "LEAF") {
			t.Fatalf("non-cycle nodes leaked into cycle chain: %q", err.Error())
		}
	})
}

func TestValidateDAG_SelfDependencyVsImplicitDeviceLoop(t *testing.T) {
	t.Run("explicit self dependency", func(t *testing.T) {
		_, err := ValidateDAG([]Node{
			{ID: "rule-self", Code: "SELF_RULE", DependsOn: []string{"rule-self"}},
		})
		if !errors.Is(err, ErrSelfDependency) {
			t.Fatalf("expected ErrSelfDependency, got %v", err)
		}
		if errors.Is(err, ErrRuleCycleDetected) {
			t.Fatalf("explicit self dependency should not be reported as ErrRuleCycleDetected: %v", err)
		}
	})

	t.Run("implicit same-rule read/write device loop", func(t *testing.T) {
		_, err := ValidateDAG([]Node{
			{
				ID:           "rule-rw",
				Code:         "CHILLER_LOOP",
				ReadDevices:  []string{"chiller-1", "pump-1"},
				WriteDevices: []string{"chiller-1"},
			},
		})
		if !errors.Is(err, ErrRuleCycleDetected) {
			t.Fatalf("expected ErrRuleCycleDetected, got %v", err)
		}
		if errors.Is(err, ErrSelfDependency) {
			t.Fatalf("device self-loop should not be reported as ErrSelfDependency: %v", err)
		}
		if !strings.Contains(err.Error(), "CHILLER_LOOP") || !strings.Contains(err.Error(), "chiller-1") {
			t.Fatalf("expected error to name rule and self-loop device, got %q", err.Error())
		}
	})
}

func TestValidateDAG_CrossDeviceAndMixedCycles(t *testing.T) {
	t.Run("cross-device feedback cycle", func(t *testing.T) {
		_, err := ValidateDAG([]Node{
			{
				ID:           "rule-a",
				ReadDevices:  []string{"device-a"},
				WriteDevices: []string{"device-b"},
			},
			{
				ID:           "rule-b",
				ReadDevices:  []string{"device-b"},
				WriteDevices: []string{"device-a"},
			},
		})
		if !errors.Is(err, ErrRuleCycleDetected) {
			t.Fatalf("expected ErrRuleCycleDetected, got %v", err)
		}
		if !strings.Contains(err.Error(), "rule-a -> rule-b -> rule-a") {
			t.Fatalf("expected cross-device cycle chain, got %q", err.Error())
		}
	})

	t.Run("mixed explicit and implicit cycle", func(t *testing.T) {
		_, err := ValidateDAG([]Node{
			{
				ID:           "rule-a",
				DependsOn:    []string{"rule-b"},
				WriteDevices: []string{"valve-1"},
			},
			{
				ID:          "rule-b",
				ReadDevices: []string{"valve-1"},
			},
		})
		if !errors.Is(err, ErrRuleCycleDetected) {
			t.Fatalf("expected ErrRuleCycleDetected, got %v", err)
		}
		if !strings.Contains(err.Error(), "rule-a -> rule-b -> rule-a") {
			t.Fatalf("expected mixed cycle chain, got %q", err.Error())
		}
	})
}

func TestValidateDAG_NodeAndDependencyValidationErrors(t *testing.T) {
	t.Run("empty node ID", func(t *testing.T) {
		_, err := ValidateDAG([]Node{{ID: "  "}})
		if !errors.Is(err, ErrDuplicateRuleNode) {
			t.Fatalf("expected ErrDuplicateRuleNode for empty ID, got %v", err)
		}
	})

	t.Run("duplicate node ID", func(t *testing.T) {
		_, err := ValidateDAG([]Node{
			{ID: "dup-1", Code: "FIRST"},
			{ID: "dup-1", Code: "SECOND"},
		})
		if !errors.Is(err, ErrDuplicateRuleNode) {
			t.Fatalf("expected ErrDuplicateRuleNode, got %v", err)
		}
	})

	t.Run("unknown dependency ID", func(t *testing.T) {
		_, err := ValidateDAG([]Node{
			{ID: "rule-a", DependsOn: []string{"missing-rule"}},
		})
		if !errors.Is(err, ErrUnknownDependency) {
			t.Fatalf("expected ErrUnknownDependency, got %v", err)
		}
		if !strings.Contains(err.Error(), "missing-rule") {
			t.Fatalf("expected missing dependency name in error, got %q", err.Error())
		}
	})
}

func TestValidateDAG_DeterministicAcrossInputPermutations(t *testing.T) {
	// Combines explicit DependsOn, implicit WriteDevices -> ReadDevices, duplicate entries,
	// and overlapping explicit+implicit edges across multiple branches.
	variant1 := []Node{
		{ID: "a-root", WriteDevices: []string{"dev-x", "dev-y"}},
		{ID: "b-mid", DependsOn: []string{"a-root", "a-root"}, ReadDevices: []string{"dev-x", "dev-x"}, WriteDevices: []string{"dev-z"}},
		{ID: "c-mid", ReadDevices: []string{"dev-y"}},
		{ID: "d-leaf", DependsOn: []string{"c-mid", "b-mid"}, ReadDevices: []string{"dev-z"}},
	}
	variant2 := []Node{
		{ID: "d-leaf", DependsOn: []string{"b-mid", "c-mid"}, ReadDevices: []string{"dev-z"}},
		{ID: "c-mid", ReadDevices: []string{"dev-y"}},
		{ID: "b-mid", DependsOn: []string{"a-root"}, ReadDevices: []string{"dev-x"}, WriteDevices: []string{"dev-z"}},
		{ID: "a-root", WriteDevices: []string{"dev-y", "dev-x", "dev-x"}},
	}
	variant3 := []Node{
		{ID: "c-mid", ReadDevices: []string{"dev-y", ""}},
		{ID: "a-root", WriteDevices: []string{"dev-x", "dev-y"}},
		{ID: "d-leaf", DependsOn: []string{"c-mid", "b-mid"}, ReadDevices: []string{"dev-z"}},
		{ID: "b-mid", ReadDevices: []string{"dev-x"}, DependsOn: []string{"a-root"}, WriteDevices: []string{"dev-z", ""}},
	}

	baseline, err := ValidateDAG(variant1)
	if err != nil {
		t.Fatalf("unexpected error on variant1: %v", err)
	}
	assertTopologicalInvariants(t, baseline)

	if wantOrder := []string{"a-root", "b-mid", "c-mid", "d-leaf"}; !reflect.DeepEqual(baseline.Order, wantOrder) {
		t.Fatalf("order=%v, want %v", baseline.Order, wantOrder)
	}
	if baseline.MaxHopDepth != 2 {
		t.Fatalf("MaxHopDepth=%d, want 2", baseline.MaxHopDepth)
	}
	if !reflect.DeepEqual(baseline.Nodes["a-root"].DownstreamIDs, []string{"b-mid", "c-mid"}) {
		t.Fatalf("a-root DownstreamIDs=%v", baseline.Nodes["a-root"].DownstreamIDs)
	}
	if !reflect.DeepEqual(baseline.Nodes["d-leaf"].UpstreamIDs, []string{"b-mid", "c-mid"}) {
		t.Fatalf("d-leaf UpstreamIDs=%v", baseline.Nodes["d-leaf"].UpstreamIDs)
	}

	for idx, variant := range [][]Node{variant2, variant3} {
		got, err := ValidateDAG(variant)
		if err != nil {
			t.Fatalf("unexpected error on variant %d: %v", idx+2, err)
		}
		if !reflect.DeepEqual(got, baseline) {
			t.Fatalf("variant %d report differs:\ngot:  %#v\nwant: %#v", idx+2, got, baseline)
		}
	}

	// Cycle detection errors must also remain deterministic across input orderings.
	cycle1 := []Node{
		{ID: "r3", DependsOn: []string{"r2"}},
		{ID: "r2", DependsOn: []string{"r1"}},
		{ID: "r1", DependsOn: []string{"r3"}},
	}
	cycle2 := []Node{
		{ID: "r1", DependsOn: []string{"r3"}},
		{ID: "r3", DependsOn: []string{"r2"}},
		{ID: "r2", DependsOn: []string{"r1"}},
	}
	_, err1 := ValidateDAG(cycle1)
	_, err2 := ValidateDAG(cycle2)
	if !errors.Is(err1, ErrRuleCycleDetected) || !errors.Is(err2, ErrRuleCycleDetected) {
		t.Fatalf("expected ErrRuleCycleDetected, got %v / %v", err1, err2)
	}
	if err1.Error() != err2.Error() {
		t.Fatalf("cycle error not deterministic:\nerr1: %s\nerr2: %s", err1, err2)
	}
}

func assertTopologicalInvariants(t *testing.T, report GraphReport) {
	t.Helper()
	if len(report.Order) != len(report.Nodes) {
		t.Fatalf("order len %d != nodes len %d", len(report.Order), len(report.Nodes))
	}
	pos := make(map[string]int, len(report.Order))
	for i, id := range report.Order {
		if _, dup := pos[id]; dup {
			t.Fatalf("duplicate ID %s in Order", id)
		}
		if _, exists := report.Nodes[id]; !exists {
			t.Fatalf("Order references unknown node %s", id)
		}
		pos[id] = i
	}
	for id, m := range report.Nodes {
		if !slices.IsSorted(m.UpstreamIDs) {
			t.Fatalf("%s UpstreamIDs not sorted: %v", id, m.UpstreamIDs)
		}
		if !slices.IsSorted(m.DownstreamIDs) {
			t.Fatalf("%s DownstreamIDs not sorted: %v", id, m.DownstreamIDs)
		}
		for _, up := range m.UpstreamIDs {
			if pos[up] >= pos[id] {
				t.Fatalf("topological violation: upstream %s at %d after %s at %d", up, pos[up], id, pos[id])
			}
			if !slices.Contains(report.Nodes[up].DownstreamIDs, id) {
				t.Fatalf("missing symmetric downstream %s on %s", id, up)
			}
		}
		for _, down := range m.DownstreamIDs {
			if pos[id] >= pos[down] {
				t.Fatalf("topological violation: %s at %d after downstream %s at %d", id, pos[id], down, pos[down])
			}
			if !slices.Contains(report.Nodes[down].UpstreamIDs, id) {
				t.Fatalf("missing symmetric upstream %s on %s", id, down)
			}
		}
	}
}
