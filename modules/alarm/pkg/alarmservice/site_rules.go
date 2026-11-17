package alarmservice

import "fmt"

// SiteRule is one rule on a Site: the policy family and the Site assignment that evaluates it.
// A rule from the rules file carries no revision identity yet; publishing gives it one.
type SiteRule struct {
	AssignmentID string
	Policy       AlarmPolicyRevision
}

// SiteRulePlan is what publishing a Site's rules file does to the rules on the Site (#693).
type SiteRulePlan struct {
	Added     []SiteRule
	Changed   []SiteRule
	Unchanged []SiteRule
	Retired   []SiteRule
}

type SiteRuleSyncSummary struct {
	Added            int      `json:"added"`
	Changed          int      `json:"changed"`
	Unchanged        int      `json:"unchanged"`
	Retired          int      `json:"retired"`
	AddedRuleIDs     []string `json:"addedRuleIds"`
	ChangedRuleIDs   []string `json:"changedRuleIds"`
	UnchangedRuleIDs []string `json:"unchangedRuleIds"`
	RetiredRuleIDs   []string `json:"retiredRuleIds"`
}

func SummarizeSiteRulePlan(plan SiteRulePlan) SiteRuleSyncSummary {
	ids := func(rules []SiteRule) []string {
		out := make([]string, 0, len(rules))
		for _, rule := range rules {
			out = append(out, rule.AssignmentID)
		}
		return out
	}
	return SiteRuleSyncSummary{
		Added:            len(plan.Added),
		Changed:          len(plan.Changed),
		Unchanged:        len(plan.Unchanged),
		Retired:          len(plan.Retired),
		AddedRuleIDs:     ids(plan.Added),
		ChangedRuleIDs:   ids(plan.Changed),
		UnchangedRuleIDs: ids(plan.Unchanged),
		RetiredRuleIDs:   ids(plan.Retired),
	}
}

// PlanSiteRules treats the rules file as the whole set: a rule on the Site that the file no longer
// has is retired, whether it was deleted, renamed or lost its device's last active point.
func PlanSiteRules(file, active []SiteRule) (SiteRulePlan, error) {
	activeByAssignment := make(map[string]SiteRule, len(active))
	for _, rule := range active {
		activeByAssignment[rule.AssignmentID] = rule
	}
	inFile := make(map[string]bool, len(file))
	var plan SiteRulePlan
	for _, rule := range file {
		if inFile[rule.AssignmentID] {
			return SiteRulePlan{}, fmt.Errorf("duplicate Site rule assignment %s", rule.AssignmentID)
		}
		inFile[rule.AssignmentID] = true
		current, ok := activeByAssignment[rule.AssignmentID]
		if !ok {
			plan.Added = append(plan.Added, rule)
			continue
		}
		unchanged, err := samePolicyContent(current.Policy, rule.Policy)
		if err != nil {
			return SiteRulePlan{}, err
		}
		if unchanged {
			plan.Unchanged = append(plan.Unchanged, rule)
		} else {
			plan.Changed = append(plan.Changed, rule)
		}
	}
	for _, rule := range active {
		if !inFile[rule.AssignmentID] {
			plan.Retired = append(plan.Retired, rule)
		}
	}
	return plan, nil
}
