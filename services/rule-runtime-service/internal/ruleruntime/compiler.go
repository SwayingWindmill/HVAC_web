package ruleruntime

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

type CompileError struct {
	Code   string
	NodeID string
	Detail string
}

func (e *CompileError) Error() string {
	if e.NodeID == "" {
		return fmt.Sprintf("%s: %s", e.Code, e.Detail)
	}
	return fmt.Sprintf("%s [%s]: %s", e.Code, e.NodeID, e.Detail)
}

func Compile(revision RuleRevision, catalog Catalog) (ExecutionPlan, error) {
	if revision.CatalogVersion != catalog.Version {
		return ExecutionPlan{}, compileErr("CATALOG_VERSION_MISMATCH", "", "revision does not pin the active catalog version")
	}
	if revision.MaxNodes <= 0 || revision.MaxDepth <= 0 || revision.MaxFanout <= 0 || revision.MaxResourceCost <= 0 || revision.MaxAttempts <= 0 {
		return ExecutionPlan{}, compileErr("BUDGET_INVALID", "", "all execution budgets and max attempts must be positive")
	}
	if len(revision.Nodes) == 0 || len(revision.Nodes) > revision.MaxNodes {
		return ExecutionPlan{}, compileErr("NODE_BUDGET_EXCEEDED", "", "node count exceeds the released budget")
	}

	permissions := make(map[string]struct{}, len(revision.AllowedPermissions))
	for _, permission := range revision.AllowedPermissions {
		permissions[permission] = struct{}{}
	}

	nodes := make(map[string]NodeInstance, len(revision.Nodes))
	resourceCost := 0
	for _, node := range revision.Nodes {
		if node.ID == "" {
			return ExecutionPlan{}, compileErr("NODE_ID_REQUIRED", "", "node id is required")
		}
		if _, exists := nodes[node.ID]; exists {
			return ExecutionPlan{}, compileErr("NODE_ID_DUPLICATE", node.ID, "node id must be unique")
		}
		definition, exists := catalog.Definitions[node.DefinitionID]
		if !exists {
			return ExecutionPlan{}, compileErr("NODE_DEFINITION_UNKNOWN", node.ID, node.DefinitionID)
		}
		if !definition.Deterministic {
			return ExecutionPlan{}, compileErr("NODE_NON_DETERMINISTIC", node.ID, "production catalog nodes must be deterministic")
		}
		if definition.RequiredPermission != "" {
			if _, allowed := permissions[definition.RequiredPermission]; !allowed {
				return ExecutionPlan{}, compileErr("PERMISSION_DENIED", node.ID, definition.RequiredPermission)
			}
		}
		if definition.ValidateConfig != nil {
			if err := definition.ValidateConfig(node.Config); err != nil {
				return ExecutionPlan{}, compileErr("NODE_CONFIG_INVALID", node.ID, err.Error())
			}
		}
		resourceCost += definition.ResourceCost
		nodes[node.ID] = node
	}
	if resourceCost > revision.MaxResourceCost {
		return ExecutionPlan{}, compileErr("RESOURCE_BUDGET_EXCEEDED", "", "catalog resource cost exceeds the released budget")
	}
	if _, exists := nodes[revision.EntryNodeID]; !exists {
		return ExecutionPlan{}, compileErr("ENTRY_NODE_INVALID", revision.EntryNodeID, "entry node is missing")
	}

	outgoing := make(map[string][]Edge, len(nodes))
	incoming := make(map[string][]Edge, len(nodes))
	for _, edge := range revision.Edges {
		fromNode, fromExists := nodes[edge.FromNode]
		toNode, toExists := nodes[edge.ToNode]
		if !fromExists || !toExists {
			return ExecutionPlan{}, compileErr("EDGE_NODE_UNKNOWN", edge.FromNode, "edge references an unknown node")
		}
		fromDefinition := catalog.Definitions[fromNode.DefinitionID]
		toDefinition := catalog.Definitions[toNode.DefinitionID]
		fromType, fromPortExists := fromDefinition.Outputs[edge.FromPort]
		toType, toPortExists := toDefinition.Inputs[edge.ToPort]
		if !fromPortExists || !toPortExists {
			return ExecutionPlan{}, compileErr("PORT_UNKNOWN", edge.FromNode, "edge references an unknown port")
		}
		if fromType != toType {
			return ExecutionPlan{}, compileErr("PORT_TYPE_MISMATCH", edge.FromNode, fmt.Sprintf("%s -> %s", fromType, toType))
		}
		outgoing[edge.FromNode] = append(outgoing[edge.FromNode], edge)
		incoming[edge.ToNode] = append(incoming[edge.ToNode], edge)
	}
	for nodeID, edges := range outgoing {
		portFanout := map[string]int{}
		for _, edge := range edges {
			portFanout[edge.FromPort]++
			if portFanout[edge.FromPort] > revision.MaxFanout {
				return ExecutionPlan{}, compileErr("FANOUT_BUDGET_EXCEEDED", nodeID, edge.FromPort)
			}
		}
	}

	state := make(map[string]uint8, len(nodes))
	reachable := make(map[string]bool, len(nodes))
	maxDepth := 0
	var visit func(string, int) error
	visit = func(nodeID string, depth int) error {
		if depth > revision.MaxDepth {
			return compileErr("DEPTH_BUDGET_EXCEEDED", nodeID, "graph depth exceeds the released budget")
		}
		if depth > maxDepth {
			maxDepth = depth
		}
		if state[nodeID] == 1 {
			return compileErr("GRAPH_CYCLE", nodeID, "cycle detected")
		}
		if state[nodeID] == 2 {
			reachable[nodeID] = true
			return nil
		}
		state[nodeID] = 1
		reachable[nodeID] = true
		for _, edge := range outgoing[nodeID] {
			if err := visit(edge.ToNode, depth+1); err != nil {
				return err
			}
		}
		state[nodeID] = 2
		return nil
	}
	if err := visit(revision.EntryNodeID, 1); err != nil {
		return ExecutionPlan{}, err
	}
	if len(incoming[revision.EntryNodeID]) != 0 {
		return ExecutionPlan{}, compileErr("ENTRY_NODE_HAS_INPUT", revision.EntryNodeID, "entry node cannot have incoming edges")
	}
	if len(reachable) != len(nodes) {
		for nodeID := range nodes {
			if !reachable[nodeID] {
				return ExecutionPlan{}, compileErr("NODE_UNREACHABLE", nodeID, "all released nodes must be reachable")
			}
		}
	}
	entryDefinition := catalog.Definitions[nodes[revision.EntryNodeID].DefinitionID]
	if entryDefinition.Inputs["in"] != PortEvent {
		return ExecutionPlan{}, compileErr("ENTRY_PORT_INVALID", revision.EntryNodeID, "entry node must accept EVENT on port in")
	}
	for nodeID, node := range nodes {
		definition := catalog.Definitions[node.DefinitionID]
		if len(outgoing[nodeID]) == 0 && len(definition.Outputs) != 0 {
			return ExecutionPlan{}, compileErr("TERMINAL_REQUIRED", nodeID, "leaf nodes must be explicit terminal nodes")
		}
	}

	digest, err := canonicalRevisionDigest(revision)
	if err != nil {
		return ExecutionPlan{}, err
	}
	if revision.Digest != "" && revision.Digest != digest {
		return ExecutionPlan{}, compileErr("REVISION_DIGEST_MISMATCH", "", "released content does not match its digest")
	}
	revision.Digest = digest
	return ExecutionPlan{Revision: revision, Catalog: catalog, Nodes: nodes, Outgoing: outgoing, Incoming: incoming, Digest: digest}, nil
}

func canonicalRevisionDigest(revision RuleRevision) (string, error) {
	type canonicalNode struct {
		ID           string          `json:"id"`
		DefinitionID string          `json:"definitionId"`
		Config       json.RawMessage `json:"config"`
	}
	type canonicalEdge struct {
		FromNode string `json:"fromNode"`
		FromPort string `json:"fromPort"`
		ToNode   string `json:"toNode"`
		ToPort   string `json:"toPort"`
	}
	nodes := make([]canonicalNode, 0, len(revision.Nodes))
	for _, node := range revision.Nodes {
		nodes = append(nodes, canonicalNode{ID: node.ID, DefinitionID: node.DefinitionID, Config: node.Config})
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	edges := make([]canonicalEdge, 0, len(revision.Edges))
	for _, edge := range revision.Edges {
		edges = append(edges, canonicalEdge(edge))
	}
	sort.Slice(edges, func(i, j int) bool {
		left := edges[i].FromNode + "\x00" + edges[i].FromPort + "\x00" + edges[i].ToNode + "\x00" + edges[i].ToPort
		right := edges[j].FromNode + "\x00" + edges[j].FromPort + "\x00" + edges[j].ToNode + "\x00" + edges[j].ToPort
		return left < right
	})
	permissions := append([]string(nil), revision.AllowedPermissions...)
	sort.Strings(permissions)
	canonical := struct {
		ID                 string          `json:"id"`
		RuleID             string          `json:"ruleId"`
		TenantID           string          `json:"tenantId"`
		Revision           int64           `json:"revision"`
		State              RevisionState   `json:"state"`
		CatalogVersion     string          `json:"catalogVersion"`
		EntryNodeID        string          `json:"entryNodeId"`
		Nodes              []canonicalNode `json:"nodes"`
		Edges              []canonicalEdge `json:"edges"`
		AllowedPermissions []string        `json:"allowedPermissions"`
		MaxNodes           int             `json:"maxNodes"`
		MaxDepth           int             `json:"maxDepth"`
		MaxFanout          int             `json:"maxFanout"`
		MaxResourceCost    int             `json:"maxResourceCost"`
		MaxAttempts        int             `json:"maxAttempts"`
	}{revision.ID, revision.RuleID, revision.TenantID, revision.Revision, revision.State, revision.CatalogVersion, revision.EntryNodeID, nodes, edges, permissions, revision.MaxNodes, revision.MaxDepth, revision.MaxFanout, revision.MaxResourceCost, revision.MaxAttempts}
	payload, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("marshal canonical rule revision: %w", err)
	}
	return PayloadDigest(payload), nil
}

func compileErr(code, nodeID, detail string) error {
	return &CompileError{Code: code, NodeID: nodeID, Detail: detail}
}

func CompileErrorCode(err error) string {
	var target *CompileError
	if errors.As(err, &target) {
		return target.Code
	}
	return ""
}
