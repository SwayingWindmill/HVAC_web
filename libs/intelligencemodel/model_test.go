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
	recommendation := OptimizationRecommendation{
		ID: "rec-1", TenantID: "tenant-1", SiteID: "site-1", InputSnapshotID: "input-1", DeploymentRevision: "deployment-1",
		Baseline: map[string]any{"powerKw": 810.0}, Objective: map[string]any{"kind": "ENERGY"}, Constraints: []map[string]any{{"kind": "SUPPLY_TEMP", "min": 6.0, "max": 9.0}},
		Candidate: map[string]any{"supplyTempC": 8.0}, ExpectedImpact: map[string]any{"energyKwh": -120.0}, Uncertainty: map[string]any{"p90EnergyKwh": 40.0},
		Risk: map[string]any{"level": "MEDIUM"}, RollbackPlan: map[string]any{"condition": "comfort_violation"}, VerificationPlan: map[string]any{"windowMinutes": 30},
		Approval: RecommendationApproved, CreatedAt: now.Add(-10 * time.Minute),
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
}
