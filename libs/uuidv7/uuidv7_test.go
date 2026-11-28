package uuidv7

import (
	"crypto/sha256"
	"strings"
	"testing"
	"time"
)

func TestNewAndNewAtProduceValidLowercaseUUIDv7(t *testing.T) {
	id, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if !IsValid(id) {
		t.Fatalf("New() produced invalid UUIDv7 %q", id)
	}

	at := time.Date(2026, 11, 28, 5, 0, 0, 123000000, time.FixedZone("UTC+8", 8*3600))
	idAt, err := NewAt(at)
	if err != nil {
		t.Fatalf("NewAt() error = %v", err)
	}
	if !IsValid(idAt) {
		t.Fatalf("NewAt() produced invalid UUIDv7 %q", idAt)
	}
	if idAt == id {
		t.Fatalf("expected distinct random entropy across calls: %q", idAt)
	}
	// Timestamp prefix matches UTC UnixMilli regardless of input Location.
	utcAt, err := FormatAt(at.UTC(), [10]byte{})
	if err != nil {
		t.Fatalf("FormatAt() error = %v", err)
	}
	nonUTCSameInstant, err := FormatAt(at, [10]byte{})
	if err != nil {
		t.Fatalf("FormatAt(non-UTC) error = %v", err)
	}
	if utcAt != nonUTCSameInstant || idAt[:13] != utcAt[:13] {
		t.Fatalf("timestamp prefix mismatch: idAt=%q utcAt=%q nonUTC=%q", idAt, utcAt, nonUTCSameInstant)
	}
}

func TestFormatAtRejectsOutOfRangeTimestamps(t *testing.T) {
	if _, err := NewAt(time.UnixMilli(-1)); err == nil {
		t.Fatal("expected error for negative UnixMilli timestamp")
	}
	over := time.UnixMilli(maxUnixMilli).Add(time.Millisecond)
	if _, err := FormatAt(over, [10]byte{}); err == nil {
		t.Fatal("expected error for overflow 48-bit timestamp")
	}
}

func TestFormatAtAndFormatRawPreserveDeterministicBytes(t *testing.T) {
	digest := sha256.Sum256([]byte("ns|seed"))
	at := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	got := MustFormatAt(at, [10]byte(digest[6:16]))
	if !IsValid(got) {
		t.Fatalf("MustFormatAt invalid: %q", got)
	}
	if gotAgain := MustFormatAt(at, [10]byte(digest[6:16])); got != gotAgain {
		t.Fatalf("MustFormatAt non-deterministic: %q != %q", got, gotAgain)
	}
}

func TestIsValidRejectsUppercaseNonV7AndWrongVariant(t *testing.T) {
	valid := "0193f000-2000-7000-8000-000000000001"
	if !IsValid(valid) {
		t.Fatalf("expected valid UUIDv7 %q", valid)
	}
	for _, bad := range []string{
		"",
		strings.ToUpper(valid),
		" 0193f000-2000-7000-8000-000000000001",
		"0193f000-2000-4000-8000-000000000001", // v4
		"0193f000-2000-7000-0000-000000000001", // RFC NCS variant
		"0193f000-2000-7000-c000-000000000001", // Microsoft variant
		"0193f000_2000_7000_8000_000000000001",
		"0193f000-2000-7000-8000-00000000000g",
	} {
		if IsValid(bad) {
			t.Fatalf("expected invalid UUIDv7 %q", bad)
		}
	}
	if !IsValidLowerTrimmed("  " + valid + "\n") {
		t.Fatal("IsValidLowerTrimmed should accept trimmed lowercase UUIDv7")
	}
	if !IsValidAnyCase(strings.ToUpper(valid)) || !IsValidAnyCase(valid) {
		t.Fatal("IsValidAnyCase should accept uppercase and lowercase UUIDv7")
	}
	if IsValidAnyCase("0193f000-2000-7000-C000-000000000001") || IsValidAnyCase("0193f000-2000-4000-8000-000000000001") {
		t.Fatal("IsValidAnyCase must reject invalid variant or version")
	}
}
