package notificationservice

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// The daily WeChat summary (#664, docs/specs/miniprogram-wechat.md). From 8 in the morning, Site
// time, everyone with a daily summary left for the Site who, as IAM says, can read its energy gets
// yesterday's electricity, cooling and efficiency, once a day; nobody gets one after noon.

const (
	dailySummaryFrom  = 8
	dailySummaryUntil = 12
)

type dailySite struct {
	tenantID string
	siteID   string
}

// planDaily notes the summaries due at each Site where it is now between 8 and noon.
func (worker *WeChatReminderWorker) planDaily(ctx context.Context, now time.Time) (bool, error) {
	holders := map[dailySite][]WeChatPerson{}
	var sites []dailySite
	rows, err := worker.Scheduler.pool.Query(ctx, `
SELECT s.tenant_id::text, s.site_id::text, s.subject_issuer, s.subject
FROM notification_runtime.wechat_daily_subscription s
JOIN notification_runtime.wechat_reminder_allowance a
  ON a.tenant_id = s.tenant_id AND a.subject_issuer = s.subject_issuer AND a.subject = s.subject AND a.kind = 'DAILY' AND a.remaining > 0
ORDER BY s.tenant_id, s.site_id, s.subject_issuer, s.subject`)
	if err != nil {
		return false, fmt.Errorf("read daily summary holders: %w", err)
	}
	for rows.Next() {
		var site dailySite
		var person WeChatPerson
		if err := rows.Scan(&site.tenantID, &site.siteID, &person.SubjectIssuer, &person.Subject); err != nil {
			rows.Close()
			return false, err
		}
		person.TenantID = site.tenantID
		if _, seen := holders[site]; !seen {
			sites = append(sites, site)
		}
		holders[site] = append(holders[site], person)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, err
	}
	worked := false
	for _, site := range sites {
		planned, err := worker.planDailyAt(ctx, site, holders[site], now)
		if err != nil {
			worker.log().Warn("wechat_daily_summary_not_planned", "error_code", "WECHAT_DAILY_SUMMARY_NOT_PLANNED", "site_id", site.siteID)
			continue
		}
		worked = worked || planned
	}
	return worked, nil
}

// planDailyAt notes today's summary at one Site for those of its holders IAM allows and who have
// none yet.
func (worker *WeChatReminderWorker) planDailyAt(ctx context.Context, site dailySite, people []WeChatPerson, now time.Time) (bool, error) {
	_, location, err := worker.siteAt(ctx, site.tenantID, site.siteID, now)
	if err != nil {
		return false, err
	}
	local := now.In(location)
	if local.Hour() < dailySummaryFrom || local.Hour() >= dailySummaryUntil {
		return false, nil
	}
	day := local.Format(time.DateOnly)
	rows, err := worker.Scheduler.pool.Query(ctx, `SELECT subject_issuer, subject FROM notification_runtime.wechat_reminder_delivery
WHERE tenant_id = $1::uuid AND kind = 'DAILY' AND reference = $2`, site.tenantID, day)
	if err != nil {
		return false, err
	}
	planned := map[[2]string]bool{}
	for rows.Next() {
		var issuer, subject string
		if err := rows.Scan(&issuer, &subject); err != nil {
			rows.Close()
			return false, err
		}
		planned[[2]string{issuer, subject}] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, err
	}
	var due []WeChatPerson
	for _, person := range people {
		if !planned[[2]string{person.SubjectIssuer, person.Subject}] {
			due = append(due, person)
		}
	}
	if len(due) == 0 {
		return false, nil
	}
	allowed, err := worker.askRecipients(ctx, ReminderDaily, site.tenantID, site.siteID, "", due)
	if err != nil {
		return false, err
	}
	isAllowed := map[[2]string]bool{}
	for _, person := range allowed {
		isAllowed[[2]string{person.SubjectIssuer, person.Subject}] = true
	}
	var refused []WeChatPerson
	for _, person := range due {
		if !isAllowed[[2]string{person.SubjectIssuer, person.Subject}] {
			refused = append(refused, person)
		}
	}
	noon := time.Date(local.Year(), local.Month(), local.Day(), dailySummaryUntil, 0, 0, 0, location)
	tx, err := worker.Scheduler.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	if err := addDeliveries(ctx, tx, ReminderDaily, day, site.siteID, "PENDING", allowed, noon, now); err != nil {
		return false, err
	}
	// Those IAM refused are not asked again today.
	if err := addDeliveries(ctx, tx, ReminderDaily, day, site.siteID, "NOT_ALLOWED", refused, noon, now); err != nil {
		return false, err
	}
	return len(allowed) > 0, tx.Commit(ctx)
}

// dailyMessage fills 昨日能效数据查询提醒: 项目名称, 用冷量, 用电量, 系统EER and 主机COP for the day
// before the delivery's Site day. A character_string takes digits, letters and signs only, so
// what was not read is "-"; the platform keeps no COP for single chillers.
func (worker *WeChatReminderWorker) dailyMessage(ctx context.Context, claim claimedDelivery, now time.Time) (WeChatMessage, error) {
	site, location, err := worker.siteAt(ctx, claim.person.TenantID, claim.siteID, now)
	if err != nil {
		return WeChatMessage{}, err
	}
	day, err := time.ParseInLocation(time.DateOnly, claim.reference, location)
	if err != nil {
		return WeChatMessage{}, fmt.Errorf("the summary's day %q: %w", claim.reference, err)
	}
	electricity, cooling, err := worker.Energy.DayEnergy(ctx, claim.person.TenantID, claim.siteID, site.Timezone, day.AddDate(0, 0, -1))
	if err != nil {
		return WeChatMessage{}, err
	}
	kWh := func(value *float64) string {
		if value == nil {
			return "-"
		}
		return fmt.Sprintf("%.0fkWh", *value)
	}
	efficiency := "-"
	if electricity != nil && cooling != nil && *electricity > 0 {
		efficiency = fmt.Sprintf("%.2f", *cooling / *electricity)
	}
	projectLabel := clip(site.Name, 20)
	gapCode := "-"
	openGaps, err := worker.openReadingGapsAt(ctx, claim.person.TenantID, claim.siteID, now)
	if err != nil {
		return WeChatMessage{}, err
	}
	if len(openGaps) > 0 {
		projectLabel = formatDailyGapThing1(site.Name, openGaps, now)
		gapCode = formatDailyGapCode(openGaps, location, now)
	}
	return WeChatMessage{
		TemplateID: worker.DailyTemplateID,
		Page:       "pages/overview/index",
		Data: map[string]string{
			"thing1":            projectLabel,
			"character_string2": kWh(cooling),
			"character_string3": kWh(electricity),
			"character_string4": efficiency,
			"character_string5": gapCode,
		},
	}, nil
}

type openReadingGap struct {
	subject         string
	criticalPoint   bool
	firstOccurredAt time.Time
}

func (worker *WeChatReminderWorker) openReadingGapsAt(ctx context.Context, tenantID, siteID string, now time.Time) ([]openReadingGap, error) {
	rows, err := worker.Scheduler.pool.Query(ctx, `
SELECT alarm_type, title, first_occurred_at
FROM notification_runtime.open_reading_gap_alarm
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND condition = 'ACTIVE' AND first_occurred_at <= $3
ORDER BY first_occurred_at ASC, alarm_id ASC`, tenantID, siteID, now.UTC())
	if err != nil {
		return nil, fmt.Errorf("read open reading-gap Alarms: %w", err)
	}
	defer rows.Close()
	indexBySubject := map[string]int{}
	var gaps []openReadingGap
	for rows.Next() {
		var alarmType, title string
		var firstOccurredAt time.Time
		if err := rows.Scan(&alarmType, &title, &firstOccurredAt); err != nil {
			return nil, err
		}
		subject := readingGapSubject(title)
		critical := strings.HasPrefix(strings.TrimSpace(alarmType), "reading-gap/")
		if idx, exists := indexBySubject[subject]; exists {
			// A critical-point reading gap represents the meter's own active measurement gap; do not
			// let an older non-critical point mask its start time on the same meter.
			if critical && !gaps[idx].criticalPoint {
				gaps[idx].criticalPoint = true
				gaps[idx].firstOccurredAt = firstOccurredAt.UTC()
			}
			continue
		}
		indexBySubject[subject] = len(gaps)
		gaps = append(gaps, openReadingGap{subject: subject, criticalPoint: critical, firstOccurredAt: firstOccurredAt.UTC()})
	}
	return gaps, rows.Err()
}

func readingGapDurationLabels(elapsed time.Duration) (zhLabel, asciiCode string) {
	if elapsed < time.Hour {
		minutes := max(1, int(elapsed.Round(time.Minute)/time.Minute))
		return fmt.Sprintf("断%d分", minutes), fmt.Sprintf("%dm", minutes)
	}
	if elapsed < 24*time.Hour {
		hours := max(1, int(elapsed.Round(time.Hour)/time.Hour))
		return fmt.Sprintf("断%d小时", hours), fmt.Sprintf("%dh", hours)
	}
	days := max(1, int((elapsed+12*time.Hour)/(24*time.Hour)))
	return fmt.Sprintf("断%d天", days), fmt.Sprintf("%dd", days)
}

// formatDailyGapThing1 fits Site name, open meter names, and open duration inside WeChat thing1's
// 20-character cap by clipping the Site/meter prefix first so the duration suffix is never chopped.
func formatDailyGapThing1(siteName string, openGaps []openReadingGap, now time.Time) string {
	zhDuration, _ := readingGapDurationLabels(now.Sub(openGaps[0].firstOccurredAt))
	if len(openGaps) > 1 {
		subjects := make([]string, 0, len(openGaps))
		for _, gap := range openGaps {
			subjects = append(subjects, gap.subject)
		}
		joinedFull := fmt.Sprintf("%s·%s%s", siteName, strings.Join(subjects, "、"), zhDuration)
		if len([]rune(joinedFull)) <= 20 {
			return joinedFull
		}
	}
	tail := zhDuration
	if len(openGaps) > 1 {
		tail = fmt.Sprintf("等%d台%s", len(openGaps), zhDuration)
	}
	full := fmt.Sprintf("%s·%s%s", siteName, openGaps[0].subject, tail)
	if len([]rune(full)) <= 20 {
		return full
	}
	tailRunes := len([]rune(tail))
	// Reserve 1 rune for "·" plus tailRunes; split remaining budget between Site and meter subject.
	budget := 20 - 1 - tailRunes
	if budget <= 2 {
		return clip(openGaps[0].subject+tail, 20)
	}
	subjectBudget := min(len([]rune(openGaps[0].subject)), max(4, (budget*2)/3))
	siteBudget := max(2, budget-subjectBudget)
	subjectPart := clip(openGaps[0].subject, min(len([]rune(openGaps[0].subject)), 20-1-tailRunes-min(len([]rune(siteName)), siteBudget)))
	sitePart := clip(siteName, 20-1-tailRunes-len([]rune(subjectPart)))
	return fmt.Sprintf("%s·%s%s", sitePart, subjectPart, tail)
}

// formatDailyGapCode formats start times and open durations into WeChat character_string5 (max 32
// ASCII characters: digits, letters, and punctuation).
func formatDailyGapCode(openGaps []openReadingGap, location *time.Location, now time.Time) string {
	if len(openGaps) == 1 {
		_, asciiDur := readingGapDurationLabels(now.Sub(openGaps[0].firstOccurredAt))
		return fmt.Sprintf("GAP-%s-%s", openGaps[0].firstOccurredAt.In(location).Format("01/02-15:04"), asciiDur)
	}
	parts := make([]string, 0, len(openGaps))
	for _, gap := range openGaps {
		_, asciiDur := readingGapDurationLabels(now.Sub(gap.firstOccurredAt))
		parts = append(parts, fmt.Sprintf("%s-%s", gap.firstOccurredAt.In(location).Format("01/02-15:04"), asciiDur))
	}
	joined := strings.Join(parts, ",")
	if len(joined) <= 32 {
		return joined
	}
	// Fit as many individual meter start/duration entries as possible within 32 ASCII chars.
	kept := parts[0]
	for i := 1; i < len(parts); i++ {
		restSuffix := fmt.Sprintf("+%d", len(parts)-i)
		candidate := kept + "," + parts[i]
		if i+1 < len(parts) && len(candidate)+len(fmt.Sprintf("+%d", len(parts)-i-1)) <= 32 {
			kept = candidate
			continue
		}
		if i+1 == len(parts) && len(candidate) <= 32 {
			return candidate
		}
		if len(kept)+len(restSuffix) <= 32 {
			return kept + restSuffix
		}
		break
	}
	return kept + fmt.Sprintf("+%d", len(parts)-1)
}

func readingGapSubject(title string) string {
	trimmed := strings.TrimSpace(title)
	if before, _, ok := strings.Cut(trimmed, "："); ok && strings.TrimSpace(before) != "" {
		trimmed = strings.TrimSpace(before)
	} else {
		for _, suffix := range []string{"读数长时间没有更新", "读数中断"} {
			if candidate := strings.TrimSpace(strings.TrimSuffix(trimmed, suffix)); candidate != "" {
				trimmed = candidate
				break
			}
		}
	}
	return strings.Join(strings.Fields(trimmed), "")
}
