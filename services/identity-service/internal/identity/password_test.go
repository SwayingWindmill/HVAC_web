package identity

import (
	"strings"
	"testing"
)

func TestPasswordHashRoundTrip(t *testing.T) {
	password := strings.Repeat("test-value-", 2)

	encoded, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	if !VerifyPassword(password, encoded) {
		t.Fatal("VerifyPassword() rejected the original value")
	}
	if VerifyPassword(strings.Repeat("other-value-", 2), encoded) {
		t.Fatal("VerifyPassword() accepted a different value")
	}
}
