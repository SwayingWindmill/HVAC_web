package notificationservice

import (
	"context"
	"fmt"
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
	return WeChatMessage{
		TemplateID: worker.DailyTemplateID,
		Page:       "pages/overview/index",
		Data: map[string]string{
			"thing1":            clip(site.Name, 20),
			"character_string2": kWh(cooling),
			"character_string3": kWh(electricity),
			"character_string4": efficiency,
			"character_string5": "-",
		},
	}, nil
}
