package intelligencemodel

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestExternalModelDefinitionCarriesCredentialReferenceNotSecret(t *testing.T) {
	definition := ModelDefinition{
		ID: "model-1", TenantID: "tenant-1", Name: "plant-load", Provider: "OPENAI", ModelID: "model-snapshot-2026-08",
		Capabilities: []string{"FORECAST"}, CredentialRef: "credential-42", EndpointPolicyID: "endpoint-policy-1", Status: "ACTIVE", Revision: 1,
	}
	if err := definition.Validate(); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(definition)
	if err != nil {
		t.Fatal(err)
	}
	lower := strings.ToLower(string(payload))
	for _, forbidden := range []string{"apikey", "api_key", "secretkey", "secret_key", "password"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("public model definition exposes secret-shaped field %q: %s", forbidden, lower)
		}
	}
}

func TestRecommendationCannotCreateCommandWithoutFreshIndependentRevalidation(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	minSupply := 6.0
	maxSupply := 9.0
	restoreSupply := 7.0
	recommendation := OptimizationRecommendation{
		ID: "rec-1", TenantID: "tenant-1", SiteID: "site-1", InputSnapshotID: "input-1", DeploymentRevision: "deployment-1",
		Baseline:         RecommendationBaseline{DailyEnergyKWh: 2400, DailyCost: 360, SupplyTempC: restoreSupply, ZoneTempC: 23.0},
		Objective:        RecommendationObjective{Kind: "ENERGY"},
		Constraints:      []RecommendationConstraint{{Kind: "SUPPLY_TEMP", MinC: &minSupply, MaxC: &maxSupply}},
		Candidate:        RecommendationCandidate{SupplyTempC: 8.0, ProjectedZoneTempC: 23.4, DailyEnergyKWh: 2280, DailyCost: 342},
		ExpectedImpact:   RecommendationExpectedImpact{EnergySavingKWhPerDay: 120.0, CostSavingPerDay: 18.0},
		Uncertainty:      RecommendationUncertainty{EnergyP90KWh: 40.0, ZoneTempP90C: 0.2},
		Risk:             RecommendationRisk{Level: "MEDIUM", ComfortMarginC: 1.4},
		RollbackPlan:     RecommendationRollbackPlan{RestoreSupplyTempC: &restoreSupply, Conditions: []string{"comfort_violation"}},
		VerificationPlan: RecommendationVerificationPlan{WindowMinutes: 30},
		Approval:         RecommendationApproved, CreatedAt: now.Add(-10 * time.Minute),
	}
	if err := recommendation.ValidateForApproval(); err != nil {
		t.Fatal(err)
	}
	if err := recommendation.CanCreateCommand(now); err == nil {
		t.Fatal("expected command creation to require independent revalidation")
	}
	recommendation.Revalidation = &CurrentStateRevalidation{SnapshotID: "current-2", Accepted: true, ReasonCode: "CURRENT_STATE_SAFE", ValidatedAt: now.Add(-time.Minute), ExpiresAt: now.Add(4 * time.Minute)}
	if err := recommendation.CanCreateCommand(now); err != nil {
		t.Fatalf("fresh accepted revalidation should allow command intent creation: %v", err)
	}

	// Expired recommendation (> 65m default TTL) must be rejected even with a fresh revalidation.
	expiredByDefault := recommendation
	expiredByDefault.CreatedAt = now.Add(-DefaultRecommendationTTL)
	if err := expiredByDefault.CanCreateCommand(now); err == nil {
		t.Fatal("expected recommendation past 65m TTL to be rejected")
	}

	// Explicit ValidUntil past now must also be rejected.
	expiredExplicit := recommendation
	expiredAt := now.Add(-time.Second)
	expiredExplicit.ValidUntil = &expiredAt
	if err := expiredExplicit.CanCreateCommand(now); err == nil {
		t.Fatal("expected recommendation past explicit validUntil to be rejected")
	}
}
