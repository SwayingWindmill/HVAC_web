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

// HydraulicGraph stores directed SERVES, ISOLATES, and FEEDS_HEADER relationships among plant units and loops.
type HydraulicGraph struct {
	unitsByKey    map[string]PlantUnit
	idByKey       map[string]string
	downstream    map[string][]string
	upstream      map[string][]string
	isolates      map[string][]string
	isolatedBy    map[string][]string
	feedsHeader   map[string][]string
	headerFeeders map[string][]string
	edgeCount     int
	hydraulicCnt  int
}

// NewHydraulicGraph validates hydraulic edges for self-loops and flow cycles and builds
// deterministic adjacency indices for SERVES, ISOLATES, and FEEDS_HEADER relationships.
func NewHydraulicGraph(units []PlantUnit, edges []HydraulicEdge) (*HydraulicGraph, error) {
	g := &HydraulicGraph{
		unitsByKey:    make(map[string]PlantUnit, len(units)),
		idByKey:       make(map[string]string, len(units)),
		downstream:    make(map[string][]string),
		upstream:      make(map[string][]string),
		isolates:      make(map[string][]string),
		isolatedBy:    make(map[string][]string),
		feedsHeader:   make(map[string][]string),
		headerFeeders: make(map[string][]string),
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
		if !IsValidRelationshipRole(role) {
			continue
		}
		fromRaw := strings.TrimSpace(edge.FromAssetID)
		toRaw := strings.TrimSpace(edge.ToAssetID)
		fromKey := normalizeID(fromRaw)
		toKey := normalizeID(toRaw)
		if fromKey == "" || toKey == "" {
			return nil, fmt.Errorf("%w: empty asset ID in %s edge", ErrInvalidTopology, role)
		}
		if fromKey == toKey {
			return nil, fmt.Errorf("%w: self-loop on asset %s (%s)", ErrHydraulicCycle, fromRaw, role)
		}
		if _, exists := g.idByKey[fromKey]; !exists {
			g.idByKey[fromKey] = fromRaw
		}
		if _, exists := g.idByKey[toKey]; !exists {
			g.idByKey[toKey] = toRaw
		}
		dedupKey := role + "\x00" + fromKey + "\x00" + toKey
		if _, dup := seenEdge[dedupKey]; dup {
			continue
		}
		seenEdge[dedupKey] = struct{}{}
		g.hydraulicCnt++
		switch role {
		case RelationshipServes:
			g.downstream[fromKey] = append(g.downstream[fromKey], toKey)
			g.upstream[toKey] = append(g.upstream[toKey], fromKey)
			g.edgeCount++
		case RelationshipIsolates:
			g.isolates[fromKey] = append(g.isolates[fromKey], toKey)
			g.isolatedBy[toKey] = append(g.isolatedBy[toKey], fromKey)
		case RelationshipFeedsHeader:
			g.feedsHeader[fromKey] = append(g.feedsHeader[fromKey], toKey)
			g.headerFeeders[toKey] = append(g.headerFeeders[toKey], fromKey)
		}
	}

	for _, adj := range []map[string][]string{g.downstream, g.upstream, g.isolates, g.isolatedBy, g.feedsHeader, g.headerFeeders} {
		for key, list := range adj {
			sort.Strings(list)
			adj[key] = list
		}
	}

	if err := g.detectCycles(); err != nil {
		return nil, err
	}
	return g, nil
}

func (g *HydraulicGraph) detectCycles() error {
	flowAdj := make(map[string][]string, len(g.downstream)+len(g.feedsHeader))
	for k, list := range g.downstream {
		flowAdj[k] = append([]string(nil), list...)
	}
	for k, list := range g.feedsHeader {
		flowAdj[k] = append(flowAdj[k], list...)
		sort.Strings(flowAdj[k])
	}

	keys := make([]string, 0, len(flowAdj))
	for k := range flowAdj {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	state := make(map[string]uint8, len(keys))
	var visit func(node string) error
	visit = func(node string) error {
		state[node] = 1
		for _, next := range flowAdj[node] {
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

// HasHydraulicTopology reports whether any SERVES, ISOLATES, or FEEDS_HEADER edge is registered.
func (g *HydraulicGraph) HasHydraulicTopology() bool {
	return g != nil && g.hydraulicCnt > 0
}

// Isolates returns asset IDs directly isolated by valveAssetID (valveAssetID -ISOLATES-> Y).
func (g *HydraulicGraph) Isolates(valveAssetID string) []string {
	if g == nil {
		return nil
	}
	return g.copyAdjList(g.isolates, valveAssetID)
}

// IsolatedBy returns isolation valve asset IDs that isolate assetID (valve -ISOLATES-> assetID).
func (g *HydraulicGraph) IsolatedBy(assetID string) []string {
	if g == nil {
		return nil
	}
	return g.copyAdjList(g.isolatedBy, assetID)
}

// FeedsHeader returns header/loop asset IDs fed directly by assetID (assetID -FEEDS_HEADER-> header).
func (g *HydraulicGraph) FeedsHeader(assetID string) []string {
	if g == nil {
		return nil
	}
	return g.copyAdjList(g.feedsHeader, assetID)
}

// HeaderFeeders returns asset IDs that directly feed headerAssetID (X -FEEDS_HEADER-> headerAssetID).
func (g *HydraulicGraph) HeaderFeeders(headerAssetID string) []string {
	if g == nil {
		return nil
	}
	return g.copyAdjList(g.headerFeeders, headerAssetID)
}

func (g *HydraulicGraph) copyAdjList(adj map[string][]string, assetID string) []string {
	if g == nil {
		return nil
	}
	keys := adj[normalizeID(assetID)]
	if len(keys) == 0 {
		return nil
	}
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = g.displayID(k)
	}
	return out
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
	seen := make(map[string]struct{})
	var candidates []string
	for _, id := range g.ReachableDownstream(chillerAssetID) {
		key := normalizeID(id)
		if _, dup := seen[key]; !dup {
			seen[key] = struct{}{}
			candidates = append(candidates, id)
		}
	}
	for _, id := range g.FeedsHeader(chillerAssetID) {
		key := normalizeID(id)
		if _, dup := seen[key]; !dup {
			seen[key] = struct{}{}
			candidates = append(candidates, id)
		}
	}
	var specific []string
	var generic []string
	for _, id := range candidates {
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
