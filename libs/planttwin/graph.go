package planttwin

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

var (
	ErrHydraulicCycle  = errors.New("hydraulic SERVES graph contains a cycle")
	ErrInvalidTopology = errors.New("invalid hydraulic edge")
)

// HydraulicGraph stores directed SERVES relationships among plant units and loops.
type HydraulicGraph struct {
	unitsByKey map[string]PlantUnit
	idByKey    map[string]string
	downstream map[string][]string
	upstream   map[string][]string
	edgeCount  int
}

// NewHydraulicGraph validates SERVES edges for self-loops and cycles and builds
// deterministic upstream and downstream adjacency indices.
func NewHydraulicGraph(units []PlantUnit, edges []HydraulicEdge) (*HydraulicGraph, error) {
	g := &HydraulicGraph{
		unitsByKey: make(map[string]PlantUnit, len(units)),
		idByKey:    make(map[string]string, len(units)),
		downstream: make(map[string][]string),
		upstream:   make(map[string][]string),
	}
	for _, u := range units {
		key := normalizeID(u.AssetID)
		if key == "" {
			continue
		}
		g.unitsByKey[key] = clonePlantUnit(u)
		g.idByKey[key] = strings.TrimSpace(u.AssetID)
	}

	seenEdge := make(map[string]struct{}, len(edges))
	for _, edge := range edges {
		role := strings.ToUpper(strings.TrimSpace(edge.RelationshipRole))
		if role != RelationshipServes {
			continue
		}
		fromRaw := strings.TrimSpace(edge.FromAssetID)
		toRaw := strings.TrimSpace(edge.ToAssetID)
		fromKey := normalizeID(fromRaw)
		toKey := normalizeID(toRaw)
		if fromKey == "" || toKey == "" {
			return nil, fmt.Errorf("%w: empty asset ID in SERVES edge", ErrInvalidTopology)
		}
		if fromKey == toKey {
			return nil, fmt.Errorf("%w: self-loop on asset %s", ErrHydraulicCycle, fromRaw)
		}
		if _, exists := g.idByKey[fromKey]; !exists {
			g.idByKey[fromKey] = fromRaw
		}
		if _, exists := g.idByKey[toKey]; !exists {
			g.idByKey[toKey] = toRaw
		}
		dedupKey := fromKey + "\x00" + toKey
		if _, dup := seenEdge[dedupKey]; dup {
			continue
		}
		seenEdge[dedupKey] = struct{}{}
		g.downstream[fromKey] = append(g.downstream[fromKey], toKey)
		g.upstream[toKey] = append(g.upstream[toKey], fromKey)
		g.edgeCount++
	}

	for key, list := range g.downstream {
		sort.Strings(list)
		g.downstream[key] = list
	}
	for key, list := range g.upstream {
		sort.Strings(list)
		g.upstream[key] = list
	}

	if err := g.detectCycles(); err != nil {
		return nil, err
	}
	return g, nil
}

func (g *HydraulicGraph) detectCycles() error {
	keys := make([]string, 0, len(g.downstream))
	for k := range g.downstream {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	state := make(map[string]uint8, len(keys))
	var visit func(node string) error
	visit = func(node string) error {
		state[node] = 1
		for _, next := range g.downstream[node] {
			switch state[next] {
			case 1:
				return fmt.Errorf("%w: %s -> %s", ErrHydraulicCycle, g.displayID(node), g.displayID(next))
			case 0:
				if err := visit(next); err != nil {
					return err
				}
			}
		}
		state[node] = 2
		return nil
	}

	for _, k := range keys {
		if state[k] == 0 {
			if err := visit(k); err != nil {
				return err
			}
		}
	}
	return nil
}

func (g *HydraulicGraph) displayID(key string) string {
	if id, ok := g.idByKey[key]; ok {
		return id
	}
	return key
}

// HasExplicitTopology reports whether any SERVES edge is registered.
func (g *HydraulicGraph) HasExplicitTopology() bool {
	return g != nil && g.edgeCount > 0
}

// Upstream returns asset IDs X that directly SERVES assetID (X -> assetID).
func (g *HydraulicGraph) Upstream(assetID string) []string {
	if g == nil {
		return nil
	}
	keys := g.upstream[normalizeID(assetID)]
	if len(keys) == 0 {
		return nil
	}
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = g.displayID(k)
	}
	return out
}

// ServedBy is an alias for Upstream.
func (g *HydraulicGraph) ServedBy(assetID string) []string {
	return g.Upstream(assetID)
}

// Downstream returns asset IDs Y directly served by assetID (assetID -> Y).
func (g *HydraulicGraph) Downstream(assetID string) []string {
	if g == nil {
		return nil
	}
	keys := g.downstream[normalizeID(assetID)]
	if len(keys) == 0 {
		return nil
	}
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = g.displayID(k)
	}
	return out
}

// ReachableUpstream returns all unique upstream asset IDs serving rootAssetID in BFS order.
func (g *HydraulicGraph) ReachableUpstream(rootAssetID string) []string {
	if g == nil {
		return nil
	}
	return g.bfsKeys(rootAssetID, g.upstream)
}

// ReachableDownstream returns all unique downstream asset IDs served by rootAssetID in BFS order.
func (g *HydraulicGraph) ReachableDownstream(rootAssetID string) []string {
	if g == nil {
		return nil
	}
	return g.bfsKeys(rootAssetID, g.downstream)
}

func (g *HydraulicGraph) bfsKeys(rootAssetID string, adj map[string][]string) []string {
	rootKey := normalizeID(rootAssetID)
	if rootKey == "" {
		return nil
	}
	visited := map[string]bool{rootKey: true}
	queue := []string{rootKey}
	var out []string
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, next := range adj[cur] {
			if visited[next] {
				continue
			}
			visited[next] = true
			out = append(out, g.displayID(next))
			queue = append(queue, next)
		}
	}
	return out
}

// CoolingLoopsForChiller returns chilled-water loop asset IDs served downstream by chillerAssetID.
func (g *HydraulicGraph) CoolingLoopsForChiller(chillerAssetID string) []string {
	if g == nil {
		return nil
	}
	downstream := g.ReachableDownstream(chillerAssetID)
	var specific []string
	var generic []string
	for _, id := range downstream {
		u, ok := g.unitsByKey[normalizeID(id)]
		if !ok {
			continue
		}
		t := strings.ToUpper(strings.TrimSpace(u.AssetType))
		switch t {
		case AssetTypeChilledWaterLoop, "CHW_LOOP", "CHILLED_WATER_HEADER":
			specific = append(specific, u.AssetID)
		case "LOOP", "PLANT_LOOP", "HYDRAULIC_LOOP", "HEADER":
			generic = append(generic, u.AssetID)
		}
	}
	if len(specific) > 0 {
		sort.Strings(specific)
		return specific
	}
	sort.Strings(generic)
	return generic
}

// EquipmentServingUnit returns all upstream PlantUnits serving unitAssetID, optionally
// filtered by assetTypes, sorted deterministically by AssetCode then AssetID.
func (g *HydraulicGraph) EquipmentServingUnit(unitAssetID string, assetTypes ...string) []PlantUnit {
	if g == nil {
		return nil
	}
	allowed := makeTypeSet(assetTypes)
	upstream := g.ReachableUpstream(unitAssetID)
	var out []PlantUnit
	for _, id := range upstream {
		u, ok := g.unitsByKey[normalizeID(id)]
		if !ok {
			continue
		}
		if len(allowed) > 0 {
			if _, match := allowed[strings.ToUpper(strings.TrimSpace(u.AssetType))]; !match {
				continue
			}
		}
		out = append(out, clonePlantUnit(u))
	}
	sortPlantUnits(out)
	return out
}

// EquipmentServingLoop returns all upstream PlantUnits serving loopAssetID, optionally
// filtered by assetTypes, sorted deterministically by AssetCode then AssetID.
func (g *HydraulicGraph) EquipmentServingLoop(loopAssetID string, assetTypes ...string) []PlantUnit {
	return g.EquipmentServingUnit(loopAssetID, assetTypes...)
}

// PeersByAssetType resolves connected equipment of peerType for assetID across direct
// upstream/downstream links and shared downstream loops or served targets. It does not
// traverse upstream through another unit of assetID's own type so dedicated pumps on
// sibling chillers do not leak across a shared header loop.
func (g *HydraulicGraph) PeersByAssetType(assetID string, peerType string) []PlantUnit {
	if g == nil {
		return nil
	}
	selfKey := normalizeID(assetID)
	targetType := strings.ToUpper(strings.TrimSpace(peerType))
	if selfKey == "" || targetType == "" {
		return nil
	}
	var selfType string
	if selfUnit, ok := g.unitsByKey[selfKey]; ok {
		selfType = strings.ToUpper(strings.TrimSpace(selfUnit.AssetType))
	}

	candidates := make(map[string]PlantUnit)
	addCandidate := func(key string) {
		if key == selfKey {
			return
		}
		if u, ok := g.unitsByKey[key]; ok && strings.ToUpper(strings.TrimSpace(u.AssetType)) == targetType {
			candidates[key] = clonePlantUnit(u)
		}
	}

	for _, id := range g.ReachableUpstream(assetID) {
		addCandidate(normalizeID(id))
	}
	downstreamIDs := g.ReachableDownstream(assetID)
	for _, id := range downstreamIDs {
		addCandidate(normalizeID(id))
	}

	// Walk upstream from each downstream target (e.g., CHW loop or served chiller)
	// without expanding upstream past a sibling unit of the same type as assetID.
	visitedUp := map[string]bool{selfKey: true}
	var queue []string
	for _, id := range downstreamIDs {
		k := normalizeID(id)
		if !visitedUp[k] {
			visitedUp[k] = true
			queue = append(queue, k)
		}
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, prev := range g.upstream[cur] {
			if visitedUp[prev] {
				continue
			}
			visitedUp[prev] = true
			addCandidate(prev)
			if selfType != "" {
				if prevUnit, ok := g.unitsByKey[prev]; ok && strings.ToUpper(strings.TrimSpace(prevUnit.AssetType)) == selfType {
					continue
				}
			}
			queue = append(queue, prev)
		}
	}

	if len(candidates) == 0 {
		return nil
	}
	out := make([]PlantUnit, 0, len(candidates))
	for _, u := range candidates {
		out = append(out, u)
	}
	sortPlantUnits(out)
	return out
}
