package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
	"github.com/quanlaihe/hvac-web/services/telemetry-shadow-comparator/internal/comparison"
)

type plan struct {
	SchemaVersion          int        `json:"schemaVersion"`
	AppliedProductionPhase string     `json:"appliedProductionPhase"`
	ProductionTraffic      int        `json:"productionTrafficPercent"`
	CohortGroup            string     `json:"cohortGroup"`
	CohortSalt             string     `json:"cohortSalt"`
	RouteSurfaces          []surface  `json:"routeSurfaces"`
	Revisions              []revision `json:"revisions"`
}

type surface struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

type revision struct {
	ID                string                          `json:"id"`
	FromPhase         string                          `json:"fromPhase"`
	ToPhase           string                          `json:"toPhase"`
	RegistryRevision  int64                           `json:"registryRevision"`
	RouteRevision     int64                           `json:"routeRevision"`
	Owner             string                          `json:"owner"`
	ActivationStatus  string                          `json:"activationStatus"`
	CompatibilityMode string                          `json:"compatibilityMode"`
	Rollout           ownershipregistry.RolloutPolicy `json:"rollout"`
}

func (value revision) phase() string {
	if value.ToPhase != "" {
		return value.ToPhase
	}
	return value.ID
}

type comparatorPolicy struct {
	SchemaVersion int    `json:"schemaVersion"`
	ExecutionMode string `json:"executionMode"`
	ServingPath   bool   `json:"servingPath"`
	Identity      struct {
		ServiceAccountToken bool `json:"serviceAccountToken"`
		WorkloadCertificate bool `json:"workloadCertificate"`
		TokenMintPermission bool `json:"tokenMintPermission"`
	} `json:"identity"`
	Network struct {
		Ingress string `json:"ingress"`
		Egress  string `json:"egress"`
		DNS     bool   `json:"dns"`
	} `json:"network"`
	DataAccess struct {
		DatabaseCredentials             bool `json:"databaseCredentials"`
		DatabaseWritePermission         bool `json:"databaseWritePermission"`
		LegacyWritePermission           bool `json:"legacyWritePermission"`
		TelemetryRuntimeWritePermission bool `json:"telemetryRuntimeWritePermission"`
		SharedCacheAccess               bool `json:"sharedCacheAccess"`
	} `json:"dataAccess"`
	TransportPermissions struct {
		Publish       bool `json:"publish"`
		Subscribe     bool `json:"subscribe"`
		CentrifugoAPI bool `json:"centrifugoApi"`
		Redis         bool `json:"redis"`
	} `json:"transportPermissions"`
	MutationPermissions struct {
		Authorization        bool `json:"authorization"`
		MappingRepair        bool `json:"mappingRepair"`
		RouteOwnership       bool `json:"routeOwnership"`
		CurrentStateFeedback bool `json:"currentStateFeedback"`
	} `json:"mutationPermissions"`
}

type invalidator struct {
	commands []ownershipregistry.S2SessionInvalidation
}

func (value *invalidator) InvalidateS2Sessions(_ context.Context, command ownershipregistry.S2SessionInvalidation) error {
	value.commands = append(value.commands, command)
	return nil
}

type evidence struct {
	SchemaVersion               int                                     `json:"schemaVersion"`
	Ticket                      int                                     `json:"ticket"`
	Status                      string                                  `json:"status"`
	ProductionR0ZeroTraffic     bool                                    `json:"productionR0ZeroTraffic"`
	DarkIngestLegacyReader      bool                                    `json:"darkIngestLegacyReader"`
	ShadowCompareLegacyReader   bool                                    `json:"shadowCompareLegacyReader"`
	SingleBatchLiveSameOwner    bool                                    `json:"singleBatchLiveSameOwner"`
	RouteRevisionBoundCohort    bool                                    `json:"routeRevisionBoundCohort"`
	S2CanaryAssignments         int                                     `json:"s2CanaryAssignments"`
	LegacyCanaryAssignments     int                                     `json:"legacyCanaryAssignments"`
	RollbackRestoredLegacy      bool                                    `json:"rollbackRestoredLegacy"`
	RollbackInvalidation        ownershipregistry.S2SessionInvalidation `json:"rollbackInvalidation"`
	ShadowPromotionEligible     bool                                    `json:"shadowPromotionEligible"`
	ValueAgreementRate          float64                                 `json:"valueAgreementRate"`
	TimestampAgreementRate      float64                                 `json:"timestampAgreementRate"`
	UnclassifiedDifferenceCount int                                     `json:"unclassifiedDifferenceCount"`
	ComparatorSideEffectFree    bool                                    `json:"comparatorSideEffectFree"`
	ComparatorFailureIsolated   bool                                    `json:"comparatorFailureIsolated"`
}

func main() {
	planPath := flag.String("plan", "deploy/acceptance/s2-shadow-routing-revisions.v1.json", "historical rollout revision plan")
	activePath := flag.String("active", "contracts/ownership/route-ownership.v1.json", "active route registry")
	comparisonPath := flag.String("comparison", "deploy/s2/fixtures/shadow-comparison-pass.json", "comparison fixture")
	policyPath := flag.String("policy", "deploy/acceptance/s2-shadow-comparator-policy.v1.json", "historical comparator policy")
	outputPath := flag.String("output", "out/s2-shadow-routing/shadow-routing-harness.json", "evidence output")
	flag.Parse()

	var rollout plan
	mustDecode(*planPath, &rollout)
	if rollout.SchemaVersion != 1 || rollout.AppliedProductionPhase != ownershipregistry.PhaseS2ContractOnly || rollout.ProductionTraffic != 0 || rollout.CohortGroup != ownershipregistry.S2CurrentStateCohortGroup || len(rollout.RouteSurfaces) != 4 {
		fatal("rollout plan does not preserve R0/0%% production")
	}

	activeBytes := mustRead(*activePath)
	active, err := ownershipregistry.Parse(activeBytes)
	if err != nil {
		fatal("parse active route registry: %v", err)
	}
	productionR0 := activeR0(active, rollout)

	r1 := mustRevision(rollout, ownershipregistry.PhaseS2DarkIngest)
	r2 := mustRevision(rollout, ownershipregistry.PhaseS2ShadowCompare)
	r3 := mustRevision(rollout, ownershipregistry.PhaseS2InternalCanary)
	rollback := mustRevision(rollout, "R3-to-R2-rollback")
	r1Snapshot := mustSnapshot(rollout, r1)
	r2Snapshot := mustSnapshot(rollout, r2)
	r3Snapshot := mustSnapshot(rollout, r3)
	darkLegacy := groupOwner(r1Snapshot, rollout, "org-a\x00principal-a") == ownershipregistry.OwnerLegacy
	shadowLegacy := groupOwner(r2Snapshot, rollout, "org-a\x00principal-a") == ownershipregistry.OwnerLegacy

	sameOwner := true
	revisionBound := false
	s2Count := 0
	legacyCount := 0
	rebound := r3
	rebound.RegistryRevision++
	rebound.RouteRevision++
	reboundSnapshot := mustSnapshot(rollout, rebound)
	for index := 0; index < 20_000; index++ {
		businessKey := fmt.Sprintf("org-%d\x00principal-%d", index, index)
		decisions := resolveGroup(r3Snapshot, rollout, businessKey)
		if !consistent(decisions) {
			sameOwner = false
			break
		}
		if decisions[0].SelectedOwner == ownershipregistry.OwnerTelemetryRuntime {
			s2Count++
		} else if decisions[0].SelectedOwner == ownershipregistry.OwnerLegacy {
			legacyCount++
		}
		reboundDecisions := resolveGroup(reboundSnapshot, rollout, businessKey)
		if decisions[0].CohortBucket != nil && reboundDecisions[0].CohortBucket != nil && *decisions[0].CohortBucket != *reboundDecisions[0].CohortBucket {
			revisionBound = true
		}
	}

	manager := ownershipregistry.NewManager(r2Snapshot, ownershipregistry.NewMemoryAuditSink(), nil)
	if _, err := manager.ReloadS2(context.Background(), mustRegistryJSON(rollout, r3), ownershipregistry.PolicyChangeContext{ExecutingService: ownershipregistry.OwnerGateway}, &invalidator{}); err != nil {
		fatal("activate in-memory R3: %v", err)
	}
	invalidation := &invalidator{}
	rollbackResult, err := manager.ReloadS2(context.Background(), mustRegistryJSON(rollout, rollback), ownershipregistry.PolicyChangeContext{ExecutingService: ownershipregistry.OwnerGateway}, invalidation)
	if err != nil {
		fatal("exercise rollback: %v", err)
	}
	rollbackLegacy := rollbackResult.MigrationPhase == ownershipregistry.PhaseS2ShadowCompare && groupOwner(manager.Current(), rollout, "org-a\x00principal-a") == ownershipregistry.OwnerLegacy && len(invalidation.commands) == 1

	var comparisonInput comparison.Input
	mustDecode(*comparisonPath, &comparisonInput)
	comparisonReport, err := comparison.Compare(comparisonInput)
	if err != nil {
		fatal("run shadow comparison: %v", err)
	}
	var policy comparatorPolicy
	mustDecode(*policyPath, &policy)
	sideEffectFree := policy.SchemaVersion == 1 && policy.ExecutionMode == "offline-batch" && !policy.ServingPath &&
		!policy.Identity.ServiceAccountToken && !policy.Identity.WorkloadCertificate && !policy.Identity.TokenMintPermission &&
		policy.Network.Ingress == "deny-all" && policy.Network.Egress == "deny-all" && !policy.Network.DNS &&
		!policy.DataAccess.DatabaseCredentials && !policy.DataAccess.DatabaseWritePermission && !policy.DataAccess.LegacyWritePermission && !policy.DataAccess.TelemetryRuntimeWritePermission && !policy.DataAccess.SharedCacheAccess &&
		!policy.TransportPermissions.Publish && !policy.TransportPermissions.Subscribe && !policy.TransportPermissions.CentrifugoAPI && !policy.TransportPermissions.Redis &&
		!policy.MutationPermissions.Authorization && !policy.MutationPermissions.MappingRepair && !policy.MutationPermissions.RouteOwnership && !policy.MutationPermissions.CurrentStateFeedback

	if !productionR0 || !darkLegacy || !shadowLegacy || !sameOwner || !revisionBound || s2Count == 0 || legacyCount == 0 || !rollbackLegacy || !comparisonReport.PromotionEligible || !sideEffectFree {
		fatal("Ticket 08 harness invariant failed")
	}
	command := invalidation.commands[0]
	result := evidence{
		SchemaVersion: 1, Ticket: 67, Status: "passed",
		ProductionR0ZeroTraffic:     productionR0,
		DarkIngestLegacyReader:      darkLegacy,
		ShadowCompareLegacyReader:   shadowLegacy,
		SingleBatchLiveSameOwner:    sameOwner,
		RouteRevisionBoundCohort:    revisionBound,
		S2CanaryAssignments:         s2Count,
		LegacyCanaryAssignments:     legacyCount,
		RollbackRestoredLegacy:      rollbackLegacy,
		RollbackInvalidation:        command,
		ShadowPromotionEligible:     comparisonReport.PromotionEligible,
		ValueAgreementRate:          comparisonReport.AcceptedValueAgreementRate,
		TimestampAgreementRate:      comparisonReport.TimestampAgreementRate,
		UnclassifiedDifferenceCount: comparisonReport.UnclassifiedDifferenceCount,
		ComparatorSideEffectFree:    sideEffectFree,
		ComparatorFailureIsolated:   !policy.ServingPath,
	}
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		fatal("encode evidence: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(*outputPath), 0o755); err != nil {
		fatal("create evidence directory: %v", err)
	}
	if err := os.WriteFile(*outputPath, append(encoded, '\n'), 0o600); err != nil {
		fatal("write evidence: %v", err)
	}
	fmt.Printf("S2 Ticket 08 shadow-routing harness passed: %s\n", *outputPath)
}

func activeR0(snapshot *ownershipregistry.Snapshot, rollout plan) bool {
	registry := snapshot.Registry()
	matched := 0
	for _, route := range registry.Routes {
		if route.CohortGroup != rollout.CohortGroup {
			continue
		}
		matched++
		if route.MigrationPhase != ownershipregistry.PhaseS2ContractOnly || route.Rollout.Mode != "disabled" || route.Revision != 1 {
			return false
		}
	}
	return matched == len(rollout.RouteSurfaces)
}

func mustRevision(rollout plan, id string) revision {
	for _, value := range rollout.Revisions {
		if value.ID == id {
			return value
		}
	}
	fatal("rollout revision %s is missing", id)
	return revision{}
}

func mustSnapshot(rollout plan, value revision) *ownershipregistry.Snapshot {
	input := mustRegistryJSON(rollout, value)
	snapshot, err := ownershipregistry.Parse(input)
	if err != nil {
		fatal("parse rollout revision %s: %v", value.ID, err)
	}
	return snapshot
}

func mustRegistryJSON(rollout plan, value revision) []byte {
	routes := make([]ownershipregistry.RouteEntry, 0, len(rollout.RouteSurfaces))
	for _, route := range rollout.RouteSurfaces {
		routes = append(routes, ownershipregistry.RouteEntry{
			Method: route.Method, Path: route.Path, Owner: value.Owner,
			PublicIngress:    ownershipregistry.OwnerGateway,
			ActivationStatus: value.ActivationStatus,
			Revision:         value.RouteRevision, Rollout: value.Rollout,
			CompatibilityMode:      value.CompatibilityMode,
			AllowedScopeDimensions: []string{"organization", "site", "device", "principal", "key"},
			MigrationPhase:         value.phase(), ShadowSideEffectPolicy: "NONE", ReadOnlyFallback: false,
			FallbackForbiddenResults: []string{"AUTHORIZATION_DENIED", "RESOURCE_NOT_FOUND", "REVISION_GAP", "RECOVERY_FAILED"},
			CohortGroup:              rollout.CohortGroup,
		})
	}
	encoded, err := json.Marshal(ownershipregistry.Registry{RegistryVersion: 1, RegistryRevision: value.RegistryRevision, Routes: routes})
	if err != nil {
		fatal("encode rollout registry: %v", err)
	}
	return encoded
}

func resolveGroup(snapshot *ownershipregistry.Snapshot, rollout plan, businessKey string) []ownershipregistry.Decision {
	decisions := make([]ownershipregistry.Decision, 0, len(rollout.RouteSurfaces))
	for _, route := range rollout.RouteSurfaces {
		actual := route.Path
		if route.Path == "/api/v1/devices/{deviceId}/observation-snapshot" {
			actual = "/api/v1/devices/018f2e00-3000-7000-8000-000000000001/observation-snapshot"
		}
		decision, err := snapshot.Resolve(route.Method, actual, businessKey)
		if err != nil {
			fatal("resolve %s %s: %v", route.Method, actual, err)
		}
		decisions = append(decisions, decision)
	}
	return decisions
}

func groupOwner(snapshot *ownershipregistry.Snapshot, rollout plan, businessKey string) string {
	return resolveGroup(snapshot, rollout, businessKey)[0].SelectedOwner
}

func consistent(decisions []ownershipregistry.Decision) bool {
	if len(decisions) == 0 || decisions[0].CohortGroup != ownershipregistry.S2CurrentStateCohortGroup {
		return false
	}
	first := decisions[0]
	for _, decision := range decisions[1:] {
		if decision.SelectedOwner != first.SelectedOwner || decision.RouteRevision != first.RouteRevision || decision.CohortGroup != first.CohortGroup {
			return false
		}
		if (decision.CohortBucket == nil) != (first.CohortBucket == nil) || (decision.CohortBucket != nil && *decision.CohortBucket != *first.CohortBucket) {
			return false
		}
	}
	return true
}

func mustDecode(path string, target any) {
	value := mustRead(path)
	decoder := json.NewDecoder(bytes.NewReader(value))
	if err := decoder.Decode(target); err != nil {
		fatal("decode %s: %v", path, err)
	}
}

func mustRead(path string) []byte {
	value, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		fatal("read %s: %v", path, err)
	}
	return value
}

func fatal(format string, arguments ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", arguments...)
	os.Exit(1)
}
