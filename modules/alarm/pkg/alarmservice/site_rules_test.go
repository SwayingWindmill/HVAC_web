package alarmservice

import (
	"reflect"
	"testing"
)

func ruleOnSite(assignmentID, title string) SiteRule {
	return SiteRule{AssignmentID: assignmentID, Policy: AlarmPolicyRevision{SchemaVersion: 1, PolicyID: "policy-" + assignmentID, Title: title}}
}

func assignmentIDs(rules []SiteRule) []string {
	ids := []string{}
	for _, rule := range rules {
		ids = append(ids, rule.AssignmentID)
	}
	return ids
}

// #693: the Site's rules file is the whole set. A rule the file no longer has (deleted, renamed, or
// its device has no active point any more) is retired; the rest are added, changed or unchanged.
func TestPlanSiteRulesTreatsTheFileAsTheWholeSet(t *testing.T) {
	active := []SiteRule{ruleOnSite("kept", "供水温度过高"), ruleOnSite("edited", "回水温度过高"), ruleOnSite("renamed-old", "旧名字"), ruleOnSite("device-gone", "4 号电表读数长时间没有更新")}
	edited := ruleOnSite("edited", "回水温度过高")
	edited.Policy.Summary = "门槛改成 14 ℃"
	file := []SiteRule{ruleOnSite("kept", "供水温度过高"), edited, ruleOnSite("renamed-new", "新名字")}

	plan, err := PlanSiteRules(file, active)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{
		"added": assignmentIDs(plan.Added), "changed": assignmentIDs(plan.Changed),
		"unchanged": assignmentIDs(plan.Unchanged), "retired": assignmentIDs(plan.Retired),
	}
	want := map[string][]string{
		"added": {"renamed-new"}, "changed": {"edited"}, "unchanged": {"kept"}, "retired": {"renamed-old", "device-gone"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("plan=%v", got)
	}
}

func TestSummarizeSiteRulePlanAndDuplicateRejection(t *testing.T) {
	active := []SiteRule{ruleOnSite("kept", "Keep"), ruleOnSite("edited", "Edit"), ruleOnSite("gone", "Gone")}
	changed := ruleOnSite("edited", "Edit")
	changed.Policy.Summary = "Updated threshold"
	desired := []SiteRule{ruleOnSite("kept", "Keep"), changed, ruleOnSite("new", "New")}

	plan, err := PlanSiteRules(desired, active)
	if err != nil {
		t.Fatal(err)
	}
	summary := SummarizeSiteRulePlan(plan)
	if summary.Added != 1 || summary.Changed != 1 || summary.Unchanged != 1 || summary.Retired != 1 {
		t.Fatalf("unexpected counts: %#v", summary)
	}
	if !reflect.DeepEqual(summary.AddedRuleIDs, []string{"new"}) ||
		!reflect.DeepEqual(summary.ChangedRuleIDs, []string{"edited"}) ||
		!reflect.DeepEqual(summary.UnchangedRuleIDs, []string{"kept"}) ||
		!reflect.DeepEqual(summary.RetiredRuleIDs, []string{"gone"}) {
		t.Fatalf("unexpected rule IDs: %#v", summary)
	}

	if _, err := PlanSiteRules([]SiteRule{ruleOnSite("dup", "A"), ruleOnSite("dup", "B")}, nil); err == nil {
		t.Fatal("expected duplicate assignment error from PlanSiteRules")
	}
}
