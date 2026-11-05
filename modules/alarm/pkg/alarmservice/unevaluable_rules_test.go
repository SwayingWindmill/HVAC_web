package alarmservice

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestRulesUnevaluableSummaryListsTheLongestFirstAndCapsTheList(t *testing.T) {
	now := time.Date(2026, 11, 1, 7, 0, 0, 0, time.UTC)
	rules := []UnevaluableRule{
		{Title: "冷冻水供水温度过高", Blocker: "STALE_INPUT:x", IndeterminateSince: now.Add(-40 * time.Minute)},
		{Title: "冷却塔出水过高", Blocker: "MISSING_INPUT:y", IndeterminateSince: now.Add(-90 * time.Minute)},
	}
	summary := RulesUnevaluableSummary(rules, now)
	if !strings.HasPrefix(summary, "2 条告警规则已有 30 分钟以上无法判断") || strings.Index(summary, "冷却塔出水过高（缺输入，90 分钟）") > strings.Index(summary, "冷冻水供水温度过高（输入过期，40 分钟）") {
		t.Fatalf("summary = %q", summary)
	}
	many := []UnevaluableRule{}
	for index := 0; index < 13; index++ {
		many = append(many, UnevaluableRule{Title: fmt.Sprintf("规则%02d", index), Blocker: "UNTRUSTED_QUALITY:z", IndeterminateSince: now.Add(-time.Hour)})
	}
	summary = RulesUnevaluableSummary(many, now)
	if !strings.Contains(summary, "规则09") || strings.Contains(summary, "规则10") || !strings.HasSuffix(summary, "另有 3 条。") {
		t.Fatalf("summary = %q", summary)
	}
}

func TestRulesThatWatchTheDataThemselvesAreNotWatched(t *testing.T) {
	for _, alarmType := range []string{"data-quality/suspect/x", "readings-not-updated/dev", "readings-flat/dev/pt", "reports-interrupted", "rules-unevaluable"} {
		if watchedForUnevaluable(alarmType) {
			t.Fatalf("%s should not be watched", alarmType)
		}
	}
	for _, alarmType := range []string{"chilled-water-supply-high", "physics/cop-out-of-range", "probe-offset/main"} {
		if !watchedForUnevaluable(alarmType) {
			t.Fatalf("%s should be watched", alarmType)
		}
	}
}
