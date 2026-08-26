package core

import (
	"testing"
	"time"
)

func TestFormatInstantUsesFrozenUTCMilliseconds(t *testing.T) {
	instant := time.Date(2026, 7, 22, 9, 8, 7, 654321000, time.FixedZone("offset", 9*60*60))
	if got, want := formatInstant(instant), "2026-07-22T00:08:07.654Z"; got != want {
		t.Fatalf("formatInstant() = %q, want %q", got, want)
	}
	if _, err := normalizedLimit(200); err != nil {
		t.Fatalf("maximum contract limit was rejected: %v", err)
	}
	if _, err := normalizedLimit(201); err == nil {
		t.Fatal("limit above the frozen maximum was accepted")
	}
	for name, page := range map[string]PageRequest{
		"name without id": {DisplayName: "Owner A"},
		"id without name": {ID: testAssetA1},
		"invalid id":      {DisplayName: "Owner A", ID: "not-a-uuid"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := normalizedPageRequest(page); err == nil {
				t.Fatal("invalid page request was accepted")
			}
		})
	}
}
