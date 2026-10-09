package commandmodel

import "testing"

// A reported 48.1 Hz against a 48 Hz target is exactly at the 0.1 Hz tolerance, even though
// 48.1-48 is 0.10000000000000142 in binary floating point (#443).
func TestScalarMatchesIncludesTheToleranceBoundary(t *testing.T) {
	if !ScalarMatches(NumberScalar(48.1), NumberScalar(48), 0.1) {
		t.Fatal("48.1 Hz must match 48 Hz within 0.1 Hz")
	}
	if ScalarMatches(NumberScalar(48.2), NumberScalar(48), 0.1) {
		t.Fatal("48.2 Hz must not match 48 Hz within 0.1 Hz")
	}
}
