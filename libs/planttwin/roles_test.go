package planttwin

import "testing"

func TestSemanticRolesCatalogAndParsing(t *testing.T) {
	all := AllSemanticRoles()
	if len(all) != 11 {
		t.Fatalf("expected 11 canonical semantic roles, got %d", len(all))
	}
	seen := make(map[SemanticRole]bool, len(all))
	for _, role := range all {
		if !role.IsValid() {
			t.Fatalf("canonical role %q reported invalid", role)
		}
		if seen[role] {
			t.Fatalf("duplicate canonical role %q", role)
		}
		seen[role] = true

		parsed, err := ParseSemanticRole("  " + string(role) + " ")
		if err != nil || parsed != role {
			t.Fatalf("ParseSemanticRole(%q) = (%q, %v), want %q", role, parsed, err, role)
		}
	}

	parsedLower, err := ParseSemanticRole(" active_power ")
	if err != nil || parsedLower != RoleActivePower {
		t.Fatalf("ParseSemanticRole lowercase = (%q, %v), want %q", parsedLower, err, RoleActivePower)
	}

	if SemanticRole("active_power").IsValid() {
		t.Fatal("non-canonical lowercase SemanticRole must not be valid without parsing")
	}
	if SemanticRole("").IsValid() || SemanticRole("UNKNOWN_ROLE").IsValid() {
		t.Fatal("empty or unknown SemanticRole must not be valid")
	}
	if _, err := ParseSemanticRole(""); err == nil {
		t.Fatal("expected ParseSemanticRole(\"\") to return error")
	}
	if _, err := ParseSemanticRole("CO2_LEVEL"); err == nil {
		t.Fatal("expected ParseSemanticRole(\"CO2_LEVEL\") to return error")
	}

	if RelationshipServes != "SERVES" || BindingDescribes != "DESCRIBES" || BindingControls != "CONTROLS" || BindingMeters != "METERS" {
		t.Fatal("unexpected relationship or binding constant value")
	}
}
