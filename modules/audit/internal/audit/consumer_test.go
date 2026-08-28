package audit

import (
	"testing"

	"github.com/segmentio/kafka-go"
)

func TestAuditMessageIDUsesOnlyValidatedHeader(t *testing.T) {
	valid := "Audit_Message-0123456789"
	if actual := auditMessageID([]kafka.Header{{Key: "message-id", Value: []byte(valid)}}); actual != valid {
		t.Fatalf("validated message ID = %q, want %q", actual, valid)
	}
	if actual := auditMessageID([]kafka.Header{{Key: "message-id", Value: []byte("bad\nlog-entry")}}); actual != "invalid" {
		t.Fatalf("unsafe message ID = %q, want invalid", actual)
	}
	if actual := auditMessageID(nil); actual != "missing" {
		t.Fatalf("missing message ID = %q, want missing", actual)
	}
}
