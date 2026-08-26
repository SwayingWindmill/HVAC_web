package maintenance

import (
	"testing"
	"time"
)

func TestOwnerFailurePreventsTenantRetirementCompletion(t *testing.T) {
	steps := make([]OwnerStep, 0, len(requiredRetirementOwners))
	for _, owner := range requiredRetirementOwners {
		state := "SUCCEEDED"
		if owner == "TELEMETRY" {
			state = "FAILED"
		}
		steps = append(steps, OwnerStep{OwnerCode: owner, State: state})
	}
	if got := decideRetirement(steps); got != RetirementIncomplete {
		t.Fatalf("expected incomplete retirement, got %s", got)
	}
}

func TestTenantRetirementRequiresEveryOwnerProof(t *testing.T) {
	steps := make([]OwnerStep, 0, len(requiredRetirementOwners))
	for _, owner := range requiredRetirementOwners {
		steps = append(steps, OwnerStep{OwnerCode: owner, State: "SUCCEEDED"})
	}
	if got := decideRetirement(steps); got != RetirementComplete {
		t.Fatalf("expected complete retirement, got %s", got)
	}
	if got := decideRetirement(steps[:len(steps)-1]); got != RetirementWaiting {
		t.Fatalf("missing owner proof must wait, got %s", got)
	}
}

func TestCredentialExpirySeverity(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	if got := credentialSeverity(now.Add(time.Hour), now); got != "WARNING" {
		t.Fatalf("future expiry should warn, got %s", got)
	}
	if got := credentialSeverity(now, now); got != "CRITICAL" {
		t.Fatalf("expired credential should be critical, got %s", got)
	}
}
