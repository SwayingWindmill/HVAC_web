// Package rulegraph validates causal linkage graphs across alarm rules so evaluation order is
// deterministic and feedback loops or excessive cascade depth fail before activation.
package rulegraph

import (
	"container/heap"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// MaxLinkageHops caps the longest causal chain allowed across linked alarm rules.
const MaxLinkageHops = 3

var (
	// ErrDuplicateRuleNode indicates an empty or repeated rule node ID.
	ErrDuplicateRuleNode = errors.New("duplicate rule node")
	// ErrUnknownDependency indicates an explicit DependsOn reference to a missing rule ID.
	ErrUnknownDependency = errors.New("unknown rule dependency")
	// ErrSelfDependency indicates a rule explicitly lists itself in DependsOn.
	ErrSelfDependency = errors.New("rule depends on itself")
	// ErrRuleCycleDetected indicates a causal cycle via explicit dependencies or device I/O.
	ErrRuleCycleDetected = errors.New("rule cycle detected")
	// ErrMaxLinkageHopsExceeded indicates the longest causal path exceeds MaxLinkageHops.
	ErrMaxLinkageHopsExceeded = errors.New("max linkage hops exceeded")
)

// Node describes one rule and its explicit rule dependencies plus device read/write footprints.
type Node struct {
	ID           string
	Code         string
	DependsOn    []string
	ReadDevices  []string
	WriteDevices []string
}

// NodeMetrics summarizes direct causal neighbors and longest-path hop depth for one rule.
type NodeMetrics struct {
	ID            string
	HopDepth      int
	UpstreamIDs   []string
	DownstreamIDs []string
}

// GraphReport contains the validated topological evaluation order and per-node causal metrics.
type GraphReport struct {
	Order       []string
	MaxHopDepth int
	Nodes       map[string]NodeMetrics
}

// ValidateDAG validates rule identities, explicit dependencies, and cross-device causal edges,
// returning a deterministic topological evaluation order and hop-depth metrics when acyclic and
// within MaxLinkageHops.
func ValidateDAG(nodes []Node) (GraphReport, error) {
	byID, sortedIDs, err := indexNodes(nodes)
	if err != nil {
		return GraphReport{}, err
	}

	upstream, downstream, err := buildCausalGraph(byID, sortedIDs)
	if err != nil {
		return GraphReport{}, err
	}

	order, ok := topologicalSort(sortedIDs, upstream, downstream)
	if !ok {
		cycleIDs := findCycle(sortedIDs, downstream)
		if len(cycleIDs) == 0 {
			return GraphReport{}, fmt.Errorf("causal cycle detected: %w", ErrRuleCycleDetected)
		}
		labels := make([]string, len(cycleIDs))
		for i, id := range cycleIDs {
			labels[i] = nodeLabel(byID[id])
		}
		return GraphReport{}, fmt.Errorf("causal cycle detected (%s): %w", strings.Join(labels, " -> "), ErrRuleCycleDetected)
	}

	hopDepth, maxHopDepth := computeHopDepths(order, upstream)
	if maxHopDepth > MaxLinkageHops {
		return GraphReport{}, fmt.Errorf("rule linkage depth %d exceeds maximum %d: %w", maxHopDepth, MaxLinkageHops, ErrMaxLinkageHopsExceeded)
	}

	metrics := make(map[string]NodeMetrics, len(sortedIDs))
	for _, id := range sortedIDs {
		metrics[id] = NodeMetrics{
			ID:            id,
			HopDepth:      hopDepth[id],
			UpstreamIDs:   upstream[id],
			DownstreamIDs: downstream[id],
		}
	}

	return GraphReport{
		Order:       order,
		MaxHopDepth: maxHopDepth,
		Nodes:       metrics,
	}, nil
}

func indexNodes(nodes []Node) (map[string]Node, []string, error) {
	byID := make(map[string]Node, len(nodes))
	sortedIDs := make([]string, 0, len(nodes))
	for _, node := range nodes {
		if strings.TrimSpace(node.ID) == "" {
			return nil, nil, fmt.Errorf("rule node ID must not be empty: %w", ErrDuplicateRuleNode)
		}
		if _, exists := byID[node.ID]; exists {
			return nil, nil, fmt.Errorf("duplicate rule node %q: %w", node.ID, ErrDuplicateRuleNode)
		}
		byID[node.ID] = node
		sortedIDs = append(sortedIDs, node.ID)
	}
	slices.Sort(sortedIDs)
	return byID, sortedIDs, nil
}

func buildCausalGraph(byID map[string]Node, sortedIDs []string) (map[string][]string, map[string][]string, error) {
	upSets := make(map[string]map[string]struct{}, len(sortedIDs))
	downSets := make(map[string]map[string]struct{}, len(sortedIDs))
	for _, id := range sortedIDs {
		upSets[id] = make(map[string]struct{})
		downSets[id] = make(map[string]struct{})
	}

	addEdge := func(from, to string) {
		downSets[from][to] = struct{}{}
		upSets[to][from] = struct{}{}
	}

	// Validate all explicit dependencies before checking device I/O loops so missing or
	// self-referential DependsOn entries keep their dedicated sentinel errors.
	for _, id := range sortedIDs {
		node := byID[id]
		if len(node.DependsOn) == 0 {
			continue
		}
		deps := slices.Clone(node.DependsOn)
		slices.Sort(deps)
		if slices.Contains(deps, id) {
			return nil, nil, fmt.Errorf("rule %s cannot depend on itself: %w", nodeLabel(node), ErrSelfDependency)
		}
		for _, dep := range deps {
			if _, ok := byID[dep]; !ok {
				return nil, nil, fmt.Errorf("rule %s depends on unknown rule %q: %w", nodeLabel(node), dep, ErrUnknownDependency)
			}
			addEdge(dep, id)
		}
	}

	deviceReaders := make(map[string][]string)
	deviceWriters := make(map[string][]string)
	for _, id := range sortedIDs {
		node := byID[id]
		reads := dedupSortedNonEmpty(node.ReadDevices)
		writes := dedupSortedNonEmpty(node.WriteDevices)
		if overlapDev, ok := firstSortedOverlap(reads, writes); ok {
			label := nodeLabel(node)
			return nil, nil, fmt.Errorf("rule %s reads and writes device %q (%s -> %s): %w", label, overlapDev, label, label, ErrRuleCycleDetected)
		}
		for _, dev := range reads {
			deviceReaders[dev] = append(deviceReaders[dev], id)
		}
		for _, dev := range writes {
			deviceWriters[dev] = append(deviceWriters[dev], id)
		}
	}

	for dev, writers := range deviceWriters {
		readers := deviceReaders[dev]
		if len(readers) == 0 {
			continue
		}
		for _, writerID := range writers {
			for _, readerID := range readers {
				addEdge(writerID, readerID)
			}
		}
	}

	upstream := make(map[string][]string, len(sortedIDs))
	downstream := make(map[string][]string, len(sortedIDs))
	for _, id := range sortedIDs {
		upstream[id] = sortedMapKeys(upSets[id])
		downstream[id] = sortedMapKeys(downSets[id])
	}
	return upstream, downstream, nil
}

// topologicalSort runs Kahn's algorithm with a lexicographical min-heap so the resulting
// evaluation order is independent of input slice ordering.
func topologicalSort(sortedIDs []string, upstream, downstream map[string][]string) ([]string, bool) {
	inDegree := make(map[string]int, len(sortedIDs))
	ready := make(minStringHeap, 0, len(sortedIDs))
	for _, id := range sortedIDs {
		deg := len(upstream[id])
		inDegree[id] = deg
		if deg == 0 {
			ready = append(ready, id)
		}
	}
	heap.Init(&ready)

	order := make([]string, 0, len(sortedIDs))
	for ready.Len() > 0 {
		u := heap.Pop(&ready).(string)
		order = append(order, u)
		for _, v := range downstream[u] {
			inDegree[v]--
			if inDegree[v] == 0 {
				heap.Push(&ready, v)
			}
		}
	}

	if len(order) != len(sortedIDs) {
		return nil, false
	}
	return order, true
}

func findCycle(sortedIDs []string, downstream map[string][]string) []string {
	const (
		unvisited = 0
		visiting  = 1
		visited   = 2
	)
	state := make(map[string]int, len(sortedIDs))
	stack := make([]string, 0, len(sortedIDs))
	stackIndex := make(map[string]int, len(sortedIDs))

	var dfs func(u string) []string
	dfs = func(u string) []string {
		state[u] = visiting
		stackIndex[u] = len(stack)
		stack = append(stack, u)

		for _, v := range downstream[u] {
			switch state[v] {
			case visiting:
				start := stackIndex[v]
				cycle := make([]string, 0, len(stack)-start+1)
				cycle = append(cycle, stack[start:]...)
				cycle = append(cycle, v)
				return cycle
			case unvisited:
				if cycle := dfs(v); len(cycle) > 0 {
					return cycle
				}
			}
		}

		stack = stack[:len(stack)-1]
		delete(stackIndex, u)
		state[u] = visited
		return nil
	}

	for _, id := range sortedIDs {
		if state[id] != unvisited {
			continue
		}
		if cycle := dfs(id); len(cycle) > 0 {
			return cycle
		}
	}
	return nil
}

func computeHopDepths(order []string, upstream map[string][]string) (map[string]int, int) {
	hopDepth := make(map[string]int, len(order))
	maxHopDepth := 0
	for _, id := range order {
		depth := 0
		for _, upID := range upstream[id] {
			if candidate := hopDepth[upID] + 1; candidate > depth {
				depth = candidate
			}
		}
		hopDepth[id] = depth
		if depth > maxHopDepth {
			maxHopDepth = depth
		}
	}
	return hopDepth, maxHopDepth
}

func nodeLabel(node Node) string {
	if strings.TrimSpace(node.Code) != "" {
		return node.Code
	}
	return node.ID
}

func dedupSortedNonEmpty(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, v := range values {
		if strings.TrimSpace(v) == "" {
			continue
		}
		out = append(out, v)
	}
	if len(out) == 0 {
		return nil
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func firstSortedOverlap(a, b []string) (string, bool) {
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			return a[i], true
		case a[i] < b[j]:
			i++
		default:
			j++
		}
	}
	return "", false
}

func sortedMapKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

type minStringHeap []string

func (h minStringHeap) Len() int           { return len(h) }
func (h minStringHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h minStringHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func (h *minStringHeap) Push(x any) {
	*h = append(*h, x.(string))
}

func (h *minStringHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}
