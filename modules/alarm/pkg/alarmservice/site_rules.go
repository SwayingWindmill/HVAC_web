package alarmservice

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
