package operationsauditevent

import (
	"encoding/json"
	"strings"
	"testing"
)

func validEvent() EventV1 {
	investigationID := "investigation-001"
	runID := "run-001"
	revision := uint64(7)
	return EventV1{
		EventID:               "operations-audit-v1:org-001:site-001:investigation-001:run-001:7:COMMIT_EFFECT:SUCCEEDED:effect-001",
		SchemaVersion:         SchemaVersion,
		MessageType:           MessageType,
		Producer:              Producer,
		TenantID:        "org-001",
		SiteID:                "site-001",
		InvestigationID:       &investigationID,
		RunID:                 &runID,
		InvestigationRevision: &revision,
		Actor: Actor{
			ActorType:         "OPERATOR",
			ActorID:           "operator-001",
			ActorIssuer:       "https://issuer.example.test",
			ExecutingService:  Producer,
			ExecutingSPIFFEID: "spiffe://hvac.local/operations-agent-service",
		},
		AuthorizationDecisionID: "decision-001",
		PolicyRevision:          "policy-v17",
		Action:                  "COMMIT_EFFECT",
		Operation:               "COMMIT_EFFECT",
		Outcome:                 "SUCCEEDED",
		OccurredAt:              1785600000000,
		RecordReferences:        []RecordReference{{RecordType: "EVIDENCE", RecordID: "evidence-001"}},
	}
}

func TestDecodeAcceptsBoundedOperationsEventAndDerivesOpaqueLedgerIdentities(t *testing.T) {
	event := validEvent()
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(payload)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.EventID != event.EventID || decoded.Operation != "COMMIT_EFFECT" {
		t.Fatalf("unexpected decoded event: %#v", decoded)
	}
	if aggregateID := decoded.AggregateID(); len(aggregateID) != 64 || strings.Contains(aggregateID, "investigation-001") {
		t.Fatalf("aggregate id is not opaque: %q", aggregateID)
	}
	if decoded.AggregateVersion() != 8 {
		t.Fatalf("aggregate version=%d", decoded.AggregateVersion())
	}
	if correlationID := decoded.CorrelationID(); !strings.HasPrefix(correlationID, "sha256:") || strings.Contains(correlationID, "run-001") {
		t.Fatalf("correlation id is not opaque: %q", correlationID)
	}
	if digest := decoded.PayloadSHA256(payload); len(digest) != 64 {
		t.Fatalf("payload digest length=%d", len(digest))
	}
}

func TestDecodeRejectsUnknownContentAndAuthorityContradictions(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "unknown prompt", mutate: func(value map[string]any) { value["prompt"] = "secret prompt" }},
		{name: "unknown owner payload", mutate: func(value map[string]any) { value["ownerPayload"] = map[string]any{"secret": true} }},
		{name: "unknown checkpoint", mutate: func(value map[string]any) { value["checkpoint"] = "opaque" }},
		{name: "operation mismatch", mutate: func(value map[string]any) { value["action"] = "FAIL_AGENT_RUN" }},
		{name: "producer mismatch", mutate: func(value map[string]any) { value["producer"] = "browser" }},
		{name: "executing service mismatch", mutate: func(value map[string]any) {
			actor := value["actor"].(map[string]any)
			actor["executingService"] = "platform-gateway"
		}},
		{name: "missing investigation revision", mutate: func(value map[string]any) { value["investigationRevision"] = nil }},
		{name: "duplicate record reference", mutate: func(value map[string]any) {
			value["recordReferences"] = []any{
				map[string]any{"recordType": "EVIDENCE", "recordId": "evidence-001"},
				map[string]any{"recordType": "EVIDENCE", "recordId": "evidence-001"},
			}
		}},
	}
	base, err := json.Marshal(validEvent())
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var value map[string]any
			if err := json.Unmarshal(base, &value); err != nil {
				t.Fatal(err)
			}
			test.mutate(value)
			payload, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Decode(payload); err == nil {
				t.Fatalf("invalid event was accepted: %s", payload)
			}
		})
	}
}

func TestDecodeRejectsTrailingJSONAndUnboundedIdentities(t *testing.T) {
	payload, err := json.Marshal(validEvent())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(append(payload, []byte("{}")...)); err == nil {
		t.Fatal("trailing JSON was accepted")
	}
	event := validEvent()
	event.Actor.ActorID = strings.Repeat("x", maximumIdentityLength+1)
	payload, err = json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(payload); err == nil {
		t.Fatal("unbounded actor identity was accepted")
	}
}
