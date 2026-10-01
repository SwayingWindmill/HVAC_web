package main

import "testing"

func TestParseEmbeddedOwners(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{name: "all owners", raw: "all", want: embeddedOwnerNames},
		{name: "split topology keeps notification", raw: "notification", want: []string{"notification"}},
		{name: "gateway only", raw: "none", want: nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseEmbeddedOwners(test.raw)
			if err != nil {
				t.Fatalf("parse embedded owners: %v", err)
			}
			if len(got) != len(test.want) {
				t.Fatalf("owners = %v, want %v", got, test.want)
			}
			for index := range got {
				if got[index] != test.want[index] {
					t.Fatalf("owners = %v, want %v", got, test.want)
				}
			}
		})
	}
}

func TestParseEmbeddedOwnersRejectsUnknownOrDuplicateRoles(t *testing.T) {
	for _, raw := range []string{"unknown", "iam,iam", ""} {
		if _, err := parseEmbeddedOwners(raw); err == nil {
			t.Fatalf("parseEmbeddedOwners(%q) succeeded", raw)
		}
	}
}
