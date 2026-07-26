package commandmodel

import "testing"

func TestIsUUIDv7(t *testing.T) {
	for _, test := range []struct {
		value string
		valid bool
	}{
		{"018f3e00-0000-7000-8000-000000000001", true},
		{"018F3E00-0000-7000-8000-000000000001", false},
		{"018f3e00-0000-6000-8000-000000000001", false},
		{"018f3e00-0000-7000-7000-000000000001", false},
		{"not-a-uuid", false},
	} {
		if got := IsUUIDv7(test.value); got != test.valid {
			t.Fatalf("IsUUIDv7(%q)=%v want=%v", test.value, got, test.valid)
		}
	}
}
