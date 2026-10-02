package simulator

import (
	"slices"
	"testing"
)

func TestOfflinePressureShedsDiagnosticsBeforeSafetyControlAuditEvidence(t *testing.T) {
	directory := t.TempDir()
	buffer, err := OpenOfflineBuffer(directory, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []OfflineItem{
		{ID: "diag-1", Class: EvidenceDiagnostic, Payload: bytesOfSize(40)},
		{ID: "telemetry-normal", Class: EvidenceTelemetryNormal, Payload: bytesOfSize(30)},
		{ID: "audit-1", Class: EvidenceAudit, Payload: bytesOfSize(20)},
	} {
		if _, err := buffer.Admit(item); err != nil {
			t.Fatal(err)
		}
	}
	result, err := buffer.Admit(OfflineItem{ID: "safety-1", Class: EvidenceSafety, Payload: bytesOfSize(35)})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(result.ShedIDs, "diag-1") {
		t.Fatalf("expected diagnostic shedding before safety rejection, result=%+v", result)
	}
	reopened, err := OpenOfflineBuffer(directory, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !reopened.Contains("audit-1") || !reopened.Contains("safety-1") {
		t.Fatalf("high-value evidence was not durable: audit=%v safety=%v", reopened.Contains("audit-1"), reopened.Contains("safety-1"))
	}
	if reopened.Contains("diag-1") {
		t.Fatal("diagnostic evidence was retained on disk ahead of safety evidence")
	}
	pending := reopened.Pending()
	if len(pending) != 3 || pending[0].ID != "safety-1" || pending[1].ID != "audit-1" || pending[2].ID != "telemetry-normal" {
		t.Fatalf("offline flush priority is not safety/audit before telemetry: %+v", pending)
	}
}

func bytesOfSize(size int) []byte {
	value := make([]byte, size)
	for index := range value {
		value[index] = byte('a' + index%26)
	}
	return value
}
