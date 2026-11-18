package planttwin

import (
	"strings"
	"testing"
)

func TestSemanticRolesCatalogAndParsing(t *testing.T) {
	all := AllSemanticRoles()
	if len(all) != 17 {
		t.Fatalf("expected 17 canonical semantic roles, got %d", len(all))
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

	if RelationshipServes != "SERVES" || RelationshipIsolates != "ISOLATES" || RelationshipFeedsHeader != "FEEDS_HEADER" {
		t.Fatal("unexpected hydraulic relationship constant value")
	}
	if BindingDescribes != "DESCRIBES" || BindingControls != "CONTROLS" || BindingMeters != "METERS" {
		t.Fatal("unexpected binding constant value")
	}

	loops := AllWaterLoops()
	if len(loops) != 3 {
		t.Fatalf("expected 3 water loops, got %d", len(loops))
	}
	for _, loop := range loops {
		if !loop.IsValid() {
			t.Fatalf("expected loop %q to be valid", loop)
		}
		parsed, err := ParseWaterLoop(" " + strings.ToLower(string(loop)) + " ")
		if err != nil || parsed != loop {
			t.Fatalf("ParseWaterLoop(%q) = (%q, %v), want %q", loop, parsed, err, loop)
		}
	}
	if _, err := ParseWaterLoop("STEAM_LOOP"); err == nil {
		t.Fatal("expected ParseWaterLoop(\"STEAM_LOOP\") to fail")
	}

	relRoles := AllRelationshipRoles()
	if len(relRoles) != 3 {
		t.Fatalf("expected 3 relationship roles, got %d", len(relRoles))
	}
	for _, relRole := range relRoles {
		if !IsValidRelationshipRole(relRole) {
			t.Fatalf("expected relationship role %q valid", relRole)
		}
		if parsed, err := ParseRelationshipRole(" " + relRole + " "); err != nil || parsed != relRole {
			t.Fatalf("ParseRelationshipRole(%q) = (%q, %v)", relRole, parsed, err)
		}
	}
	if _, err := ParseRelationshipRole("POWERED_BY"); err == nil {
		t.Fatal("expected ParseRelationshipRole(\"POWERED_BY\") to fail")
	}
}
