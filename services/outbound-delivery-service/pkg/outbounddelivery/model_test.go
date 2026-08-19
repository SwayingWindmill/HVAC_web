package outbounddelivery

import (
	"testing"
	"time"
)

func TestCompletionDecisionRetriesOnlyProvenNotSent(t *testing.T) {
	now := time.Date(2026, 8, 19, 4, 30, 0, 0, time.UTC)

	retryable := decideCompletion(AdapterResult{Outcome: OutcomeNotSent, Retryable: true}, 1, 3, time.Minute, now)
	if retryable.State != IntentRetryWait || retryable.RetryAt == nil || !retryable.RetryAt.Equal(now.Add(time.Minute)) || retryable.DeadLetter {
		t.Fatalf("NOT_SENT retry decision = %#v", retryable)
	}

	for _, outcome := range []OutcomeClass{OutcomeMaybeSent, OutcomeAcceptedNotConfirmed} {
		decision := decideCompletion(AdapterResult{Outcome: outcome, Retryable: true}, 1, 3, time.Minute, now)
		if decision.State != IntentOutcomeUnknown || !decision.DeadLetter || !decision.RequiresDuplicateRiskAck || decision.RetryAt != nil {
			t.Fatalf("%s must require governed replay, got %#v", outcome, decision)
		}
	}

	exhausted := decideCompletion(AdapterResult{Outcome: OutcomeNotSent, Retryable: true}, 3, 3, time.Minute, now)
	if exhausted.State != IntentDead || !exhausted.DeadLetter || exhausted.RetryAt != nil {
		t.Fatalf("exhausted NOT_SENT decision = %#v", exhausted)
	}
}
