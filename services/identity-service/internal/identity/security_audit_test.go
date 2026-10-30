package identity

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"testing"
)

var subjectRefShape = regexp.MustCompile(`^[0-9a-f]{64}$`)

func TestSecuritySubjectRefStaysTheSHA256OfTheNormalizedUsername(t *testing.T) {
	digest := sha256.Sum256([]byte("operator.one"))
	want := hex.EncodeToString(digest[:])
	if got := securitySubjectRef("  Operator.One "); got != want {
		t.Fatalf("known-account subject ref changed: got %s want %s", got, want)
	}
}

func TestUnknownSubjectRefIsKeyedAndNotTheBareDigest(t *testing.T) {
	typed := "Tr0ub4dor&3-typed-into-the-username-box"
	ref := unknownSubjectRef(typed)
	if !subjectRefShape.MatchString(ref) {
		t.Fatalf("unknown subject ref %q does not fit identity.security_audit_intents.subject_ref", ref)
	}
	if ref == securitySubjectRef(typed) {
		t.Fatal("unknown subject ref is the unkeyed SHA-256 of the typed input")
	}
	if again := unknownSubjectRef(" " + typed + " "); again != ref {
		t.Fatalf("unknown subject ref is not stable for the same normalized input in one process: %s vs %s", ref, again)
	}
	if other := unknownSubjectRef(typed + "x"); other == ref {
		t.Fatal("different unknown inputs share a subject ref")
	}
}

func TestUnknownSubjectKeyIsRandomPerProcess(t *testing.T) {
	if len(unknownSubjectKey) != 32 {
		t.Fatalf("unknown subject key is %d bytes, want 32", len(unknownSubjectKey))
	}
	if other := mustRandomKey(); string(other) == string(unknownSubjectKey) {
		t.Fatal("two generated audit subject keys are identical")
	}
}
