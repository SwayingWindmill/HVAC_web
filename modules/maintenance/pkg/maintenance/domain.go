package maintenance

import "time"

var requiredRetirementOwners = []string{"IAM", "REGISTRY", "TELEMETRY", "METRIC", "ALARM", "OUTBOUND_DELIVERY"}

type OwnerStep struct {
	OwnerCode string
	State     string
}

type RetirementDecision string

const (
	RetirementWaiting    RetirementDecision = "WAITING"
	RetirementIncomplete RetirementDecision = "INCOMPLETE"
	RetirementComplete   RetirementDecision = "COMPLETE"
)

func decideRetirement(steps []OwnerStep) RetirementDecision {
	if len(steps) != len(requiredRetirementOwners) {
		return RetirementWaiting
	}
	seen := make(map[string]string, len(steps))
	for _, step := range steps {
		seen[step.OwnerCode] = step.State
		if step.State == "FAILED" {
			return RetirementIncomplete
		}
	}
	for _, owner := range requiredRetirementOwners {
		if seen[owner] != "SUCCEEDED" {
			return RetirementWaiting
		}
	}
	return RetirementComplete
}

func credentialSeverity(expiresAt, now time.Time) string {
	if !expiresAt.After(now) {
		return "CRITICAL"
	}
	return "WARNING"
}
