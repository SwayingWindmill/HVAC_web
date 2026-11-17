package planttwin

import (
	"errors"
	"testing"
)

func TestHydraulicGraphRejectsSelfLoopAndCycles(t *testing.T) {
	units := []PlantUnit{
		{AssetID: chillerAID, AssetCode: "chiller-a", AssetType: AssetTypeChiller},
		{AssetID: chwp1ID, AssetCode: "chwp-1", AssetType: AssetTypeChilledWaterPump},
		{AssetID: chwLoopID, AssetCode: "chw-loop", AssetType: AssetTypeChilledWaterLoop},
	}

	if _, err := NewHydraulicGraph(units, []HydraulicEdge{
		{FromAssetID: chillerAID, ToAssetID: chillerAID, RelationshipRole: RelationshipServes},
	}); !errors.Is(err, ErrHydraulicCycle) {
		t.Fatalf("expected ErrHydraulicCycle for self-loop, got %v", err)
	}

	if _, err := NewHydraulicGraph(units, []HydraulicEdge{
		{FromAssetID: chwp1ID, ToAssetID: chillerAID, RelationshipRole: RelationshipServes},
		{FromAssetID: chillerAID, ToAssetID: chwLoopID, RelationshipRole: RelationshipServes},
		{FromAssetID: chwLoopID, ToAssetID: chwp1ID, RelationshipRole: RelationshipServes},
	}); !errors.Is(err, ErrHydraulicCycle) {
		t.Fatalf("expected ErrHydraulicCycle for 3-node cycle, got %v", err)
	}

	if _, err := NewHydraulicGraph(units, []HydraulicEdge{
		{FromAssetID: "", ToAssetID: chillerAID, RelationshipRole: RelationshipServes},
	}); !errors.Is(err, ErrInvalidTopology) {
		t.Fatalf("expected ErrInvalidTopology for empty asset ID, got %v", err)
	}
}

func TestHydraulicGraphTraversalsLoopsAndPeerIsolation(t *testing.T) {
	const (
		loop2ID    = "019a0000-3000-7000-8000-000000000099"
		chillerCID = "019a0000-3000-7000-8000-000000000098"
		chwpCID    = "019a0000-3000-7000-8000-000000000097"
		headerPump = "019a0000-3000-7000-8000-000000000096"
	)

	units := []PlantUnit{
		{AssetID: chillerAID, AssetCode: "chiller-a", AssetType: AssetTypeChiller},
		{AssetID: chillerBID, AssetCode: "chiller-b", AssetType: AssetTypeChiller},
		{AssetID: chwp1ID, AssetCode: "chwp-1", AssetType: AssetTypeChilledWaterPump},
		{AssetID: chwp2ID, AssetCode: "chwp-2", AssetType: AssetTypeChilledWaterPump},
		{AssetID: headerPump, AssetCode: "chwp-hdr", AssetType: AssetTypeChilledWaterPump},
		{AssetID: cwp1ID, AssetCode: "cwp-1", AssetType: AssetTypeCoolingWaterPump},
		{AssetID: tower1ID, AssetCode: "ct-1", AssetType: AssetTypeCoolingTower},
		{AssetID: chwLoopID, AssetCode: "chw-loop-1", AssetType: AssetTypeChilledWaterLoop},
		{AssetID: chillerCID, AssetCode: "chiller-c", AssetType: AssetTypeChiller},
		{AssetID: chwpCID, AssetCode: "chwp-c", AssetType: AssetTypeChilledWaterPump},
		{AssetID: loop2ID, AssetCode: "chw-loop-2", AssetType: AssetTypeChilledWaterLoop},
	}

	edges := []HydraulicEdge{
		// Plant loop 1: dedicated chwp-1 -> chiller-a, dedicated chwp-2 -> chiller-b, header chwp -> chw-loop-1,
		// multi-hop tower -> cwp-1 -> chiller-a, plus non-SERVES edge ignored and duplicate edge deduplicated.
		{FromAssetID: chwp1ID, ToAssetID: chillerAID, RelationshipRole: RelationshipServes},
		{FromAssetID: chwp1ID, ToAssetID: chillerAID, RelationshipRole: RelationshipServes},
		{FromAssetID: cwp1ID, ToAssetID: chillerAID, RelationshipRole: RelationshipServes},
		{FromAssetID: tower1ID, ToAssetID: cwp1ID, RelationshipRole: RelationshipServes},
		{FromAssetID: chillerAID, ToAssetID: chwLoopID, RelationshipRole: RelationshipServes},
		{FromAssetID: chwp2ID, ToAssetID: chillerBID, RelationshipRole: RelationshipServes},
		{FromAssetID: chillerBID, ToAssetID: chwLoopID, RelationshipRole: RelationshipServes},
		{FromAssetID: headerPump, ToAssetID: chwLoopID, RelationshipRole: RelationshipServes},
		// Disconnected Plant loop 2.
		{FromAssetID: chwpCID, ToAssetID: chillerCID, RelationshipRole: RelationshipServes},
		{FromAssetID: chillerCID, ToAssetID: loop2ID, RelationshipRole: RelationshipServes},
		// Non-SERVES edge should not count toward SERVES topology.
		{FromAssetID: chillerAID, ToAssetID: chillerCID, RelationshipRole: "POWERED_BY"},
	}

	g, err := NewHydraulicGraph(units, edges)
	if err != nil {
		t.Fatalf("NewHydraulicGraph error: %v", err)
	}
	if !g.HasExplicitTopology() {
		t.Fatal("expected HasExplicitTopology() to be true")
	}

	if up := g.Upstream(chillerAID); len(up) != 2 {
		t.Fatalf("expected 2 direct upstream units for chiller-a, got %v", up)
	}
	if down := g.Downstream(chillerAID); len(down) != 1 || down[0] != chwLoopID {
		t.Fatalf("expected [%s] downstream of chiller-a, got %v", chwLoopID, down)
	}
	if loops := g.CoolingLoopsForChiller(chillerAID); len(loops) != 1 || loops[0] != chwLoopID {
		t.Fatalf("expected CoolingLoopsForChiller=[%s], got %v", chwLoopID, loops)
	}

	// ReachableUpstream from chiller-a includes multi-hop tower-1 -> cwp-1 -> chiller-a.
	reachUp := g.ReachableUpstream(chillerAID)
	if len(reachUp) != 3 {
		t.Fatalf("expected 3 reachable upstream units (chwp-1, cwp-1, ct-1), got %v", reachUp)
	}

	// EquipmentServingLoop must find all chillers and auxiliaries on loop 1, excluding loop 2.
	loopChillers := g.EquipmentServingLoop(chwLoopID, AssetTypeChiller)
	if len(loopChillers) != 2 || loopChillers[0].AssetCode != "chiller-a" || loopChillers[1].AssetCode != "chiller-b" {
		t.Fatalf("unexpected chillers serving loop 1: %+v", loopChillers)
	}
	allLoopEquip := g.EquipmentServingLoop(chwLoopID)
	if len(allLoopEquip) != 7 {
		t.Fatalf("expected 7 units serving loop 1, got %d (%+v)", len(allLoopEquip), allLoopEquip)
	}
	if reachDown := g.ReachableDownstream(tower1ID); len(reachDown) != 3 || reachDown[0] != cwp1ID || reachDown[1] != chillerAID || reachDown[2] != chwLoopID {
		t.Fatalf("unexpected ReachableDownstream for tower-1: %v", reachDown)
	}

	// PeersByAssetType for chiller-a finds its dedicated chwp-1 AND shared headerPump, NOT chwp-2 (chiller-b) or chwp-c (loop 2).
	aPumps := g.PeersByAssetType(chillerAID, AssetTypeChilledWaterPump)
	if len(aPumps) != 2 || aPumps[0].AssetCode != "chwp-1" || aPumps[1].AssetCode != "chwp-hdr" {
		t.Fatalf("expected [chwp-1, chwp-hdr] peers for chiller-a, got %+v", aPumps)
	}
	// PeersByAssetType for chiller-a CHILLER excludes chiller-a itself and loop 2 chiller-c.
	aPeers := g.PeersByAssetType(chillerAID, AssetTypeChiller)
	if len(aPeers) != 1 || aPeers[0].AssetCode != "chiller-b" {
		t.Fatalf("expected [chiller-b] peer chiller for chiller-a, got %+v", aPeers)
	}
	// Multi-hop tower peer lookup for chiller-a.
	aTowers := g.PeersByAssetType(chillerAID, AssetTypeCoolingTower)
	if len(aTowers) != 1 || aTowers[0].AssetCode != "ct-1" {
		t.Fatalf("expected [ct-1] for chiller-a, got %+v", aTowers)
	}

	// Empty graph & nil graph safety.
	emptyGraph, err := NewHydraulicGraph(units, nil)
	if err != nil {
		t.Fatalf("empty graph error: %v", err)
	}
	if emptyGraph.HasExplicitTopology() {
		t.Fatal("expected HasExplicitTopology false when no SERVES edges exist")
	}
	var nilGraph *HydraulicGraph
	if nilGraph.HasExplicitTopology() || nilGraph.Upstream(chillerAID) != nil || nilGraph.PeersByAssetType(chillerAID, AssetTypeChiller) != nil {
		t.Fatal("expected nil HydraulicGraph methods to be safe no-ops")
	}
}
