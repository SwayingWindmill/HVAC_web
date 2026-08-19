package identity

import (
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

func TestValidateTOTPRejectsExpiredCode(t *testing.T) {
	secret, _, err := GenerateTOTP("HVAC", "operator@example.com")
	if err != nil {
		t.Fatalf("generate TOTP: %v", err)
	}
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	code, err := totp.GenerateCode(secret, now)
	if err != nil {
		t.Fatalf("generate TOTP code: %v", err)
	}
	if _, ok := validateTOTP(secret, code, now.Add(2*time.Minute), -1); ok {
		t.Fatal("expected an expired TOTP code to be rejected")
	}
}

func TestValidateTOTPRejectsReplay(t *testing.T) {
	secret, _, err := GenerateTOTP("HVAC", "operator@example.com")
	if err != nil {
		t.Fatalf("generate TOTP: %v", err)
	}
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	code, err := totp.GenerateCode(secret, now)
	if err != nil {
		t.Fatalf("generate TOTP code: %v", err)
	}
	counter, ok := validateTOTP(secret, code, now, -1)
	if !ok {
		t.Fatal("expected first TOTP use to succeed")
	}
	if _, ok := validateTOTP(secret, code, now, counter); ok {
		t.Fatal("expected the same TOTP counter to be rejected as replay")
	}
}
