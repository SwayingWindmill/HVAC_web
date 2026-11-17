package notificationservice

import (
	"sort"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
)

// DeliveryKind is what one message says.
type DeliveryKind string

const (
	DeliveryFire     DeliveryKind = "FIRE"
	DeliveryEscalate DeliveryKind = "ESCALATE"
	DeliveryAck      DeliveryKind = "ACK"
	DeliveryRecovery DeliveryKind = "RECOVERY"
)

// rootCauseRecheck is how soon an Alarm held back by its root cause is looked at again.
const rootCauseRecheck = time.Minute

// DeliveryAlarm is what Notification knows of one Alarm for delivery.
type DeliveryAlarm struct {
	TenantID, AlarmID, SiteID string
	AlarmType, Title, Summary string
	DeviceID                  string
	Severity                  alarmmodel.Severity
	Version                   int64
	BeganAt                   time.Time
	AcknowledgedAt            *time.Time
	AcknowledgedBy            string
	ClearedAt                 *time.Time
	SuppressedUntil           *time.Time
}

// QueuedMessage is a message already queued for the Alarm (sent, being sent, or suppressed).
type QueuedMessage struct {
	Kind      DeliveryKind
	Step      int
	Channel   DeliveryChannel
	Recipient string
	QueuedAt  time.Time
	Reason    string
	// Reached: the message counts as told (not suppressed and not failed for good).
	Reached bool
}

// ActiveAlarmFact describes one open Alarm at the Site for evaluating rootCauseRules.
type ActiveAlarmFact struct {
	AlarmType string
	DeviceID  string
}

// DeliveryFacts are what the decision needs besides the Alarm and its routing.
type DeliveryFacts struct {
	// RootCauseActive: another Alarm of the routing's root-cause type is active at the Site.
	RootCauseActive bool
	// ActiveAlarmTypes lists open Alarm types at the Site for evaluating rootCauseRules.
	ActiveAlarmTypes []string
	// ActiveAlarms lists open Alarms (type and optional device id) at the Site for rootCauseRules.
	ActiveAlarms []ActiveAlarmFact
	Queued       []QueuedMessage
	// SentLastHour counts first and escalation messages per recipient in the last hour, at the Site.
	SentLastHour map[string]int
	// LastSameTypeNotifiedAt maps recipient to the most recent FIRE delivery attempt time for the same (site_id, alarm_type).
	LastSameTypeNotifiedAt map[string]time.Time
}

// PlannedMessage is one message to queue; Suppressed ones are recorded and never sent.
type PlannedMessage struct {
	Kind       DeliveryKind
	Step       int
	Channel    DeliveryChannel
	Recipient  string
	Suppressed bool
	Reason     string
}

// DeliveryDecision is what to queue now and when to look again (nil: not before the Alarm changes).
type DeliveryDecision struct {
	Messages  []PlannedMessage
	NextDueAt *time.Time
	// Held explains why nothing is sent yet: HOLD, SUPPRESSED, ROOT_CAUSE, QUIET_HOURS, TOO_OLD, NO_ROUTE.
	Held string
}

// DedupKey names one message: the same Alarm, kind, step, channel and recipient is queued once.
func DedupKey(alarmID string, message PlannedMessage) string {
	return strings.Join([]string{alarmID, string(message.Kind), itoa(message.Step), string(message.Channel), message.Recipient}, ":")
}

// DecideDelivery is the whole delivery policy, without the database: given the Alarm, its Site's
// routing, what was already queued and the time, which messages to queue.
func DecideDelivery(alarm DeliveryAlarm, routing SiteRouting, facts DeliveryFacts, now time.Time) DeliveryDecision {
	route, ok := routing.Route(alarm.AlarmType, alarm.Severity)
	if !ok {
		return DeliveryDecision{Held: "NO_ROUTE"}
	}
	queued := map[string]bool{}
	reached := map[[2]string]bool{}
	reachedOrder := [][2]string{}
	var firstQueued time.Time
	stepsQueued := map[int]bool{}
	for _, message := range facts.Queued {
		queued[DedupKey(alarm.AlarmID, PlannedMessage{Kind: message.Kind, Step: message.Step, Channel: message.Channel, Recipient: message.Recipient})] = true
		if message.Kind != DeliveryFire && message.Kind != DeliveryEscalate {
			continue
		}
		stepsQueued[message.Step] = true
		if message.Step == 0 && (firstQueued.IsZero() || message.QueuedAt.Before(firstQueued)) {
			firstQueued = message.QueuedAt
		}
		target := [2]string{string(message.Channel), message.Recipient}
		if message.Reached && !reached[target] {
			reached[target] = true
			reachedOrder = append(reachedOrder, target)
		}
	}
	quietUntil := routing.quietUntil(now)
	follow := func(kind DeliveryKind) DeliveryDecision {
		if len(reachedOrder) == 0 {
			return DeliveryDecision{}
		}
		if quietUntil != nil && severityRank[alarm.Severity] < severityRank[routing.QuietHours.BypassSeverity] {
			return DeliveryDecision{NextDueAt: quietUntil, Held: "QUIET_HOURS"}
		}
		var decision DeliveryDecision
		for _, target := range reachedOrder {
			message := PlannedMessage{Kind: kind, Channel: DeliveryChannel(target[0]), Recipient: target[1]}
			if !queued[DedupKey(alarm.AlarmID, message)] {
				decision.Messages = append(decision.Messages, message)
			}
		}
		return decision
	}
	if alarm.ClearedAt != nil {
		if !route.NotifyRecovery {
			return DeliveryDecision{}
		}
		return follow(DeliveryRecovery)
	}
	if alarm.AcknowledgedAt != nil {
		if !route.NotifyAck || len(reachedOrder) == 0 {
			return DeliveryDecision{}
		}
		if alarm.SuppressedUntil != nil && now.Before(*alarm.SuppressedUntil) {
			ackDecision := follow(DeliveryAck)
			if len(ackDecision.Messages) == 0 && ackDecision.Held == "" {
				return DeliveryDecision{}
			}
			until := alarm.SuppressedUntil.UTC()
			return DeliveryDecision{Held: "SUPPRESSED", NextDueAt: &until}
		}
		return follow(DeliveryAck)
	}
	if alarm.SuppressedUntil != nil && now.Before(*alarm.SuppressedUntil) {
		until := alarm.SuppressedUntil.UTC()
		return DeliveryDecision{Held: "SUPPRESSED", NextDueAt: &until}
	}

	if !stepsQueued[0] {
		maxAgeHours := routing.MaxAgeHours
		if maxAgeHours <= 0 {
			maxAgeHours = 12
		}
		if now.Sub(alarm.BeganAt) > time.Duration(maxAgeHours)*time.Hour {
			return DeliveryDecision{Held: "TOO_OLD"}
		}
		holdMinutes := max(0, routing.HoldMinutes)
		holdEnds := alarm.BeganAt.Add(time.Duration(holdMinutes) * time.Minute)
		if now.Before(holdEnds) {
			return DeliveryDecision{NextDueAt: &holdEnds, Held: "HOLD"}
		}
	}
	if routing.IsSuppressedByRootCauseForAlarm(alarm, facts) {
		next := now.Add(rootCauseRecheck)
		return DeliveryDecision{NextDueAt: &next, Held: "ROOT_CAUSE"}
	}
	if quietUntil != nil && severityRank[alarm.Severity] < severityRank[routing.QuietHours.BypassSeverity] {
		return DeliveryDecision{NextDueAt: quietUntil, Held: "QUIET_HOURS"}
	}
	start := firstQueued
	if start.IsZero() {
		start = now
	}
	var decision DeliveryDecision
	counts := map[string]int{}
	for recipient, count := range facts.SentLastHour {
		counts[recipient] = count
	}
	cooldown := effectiveCooldownMinutes(routing, route)
	step0CooldownSuppressed := false
	step0HasDelivery := false
	for _, existing := range facts.Queued {
		if existing.Kind != DeliveryFire {
			continue
		}
		if existing.Reached || existing.Reason != "COOLDOWN" {
			step0HasDelivery = true
		} else {
			step0CooldownSuppressed = true
		}
	}
	for index, step := range route.Steps {
		due := start.Add(time.Duration(step.AfterMinutes) * time.Minute)
		if due.After(now) {
			decision.NextDueAt = &due
			break
		}
		kind := DeliveryEscalate
		if index == 0 {
			kind = DeliveryFire
		}
		for _, target := range stepTargets(step) {
			message := PlannedMessage{Kind: kind, Step: index, Channel: target.channel, Recipient: target.recipient}
			key := DedupKey(alarm.AlarmID, message)
			if queued[key] {
				continue
			}
			queued[key] = true
			if kind == DeliveryFire && cooldown > 0 && alarm.Severity != alarmmodel.SeverityCritical {
				if lastAt, ok := facts.LastSameTypeNotifiedAt[target.recipient]; ok && !lastAt.IsZero() && now.Sub(lastAt) >= 0 && now.Sub(lastAt) < time.Duration(cooldown)*time.Minute {
					message.Suppressed, message.Reason = true, "COOLDOWN"
					decision.Messages = append(decision.Messages, message)
					step0CooldownSuppressed = true
					continue
				}
			}
			if routing.RateLimitPerHour > 0 && alarm.Severity != alarmmodel.SeverityCritical && counts[target.recipient] >= routing.RateLimitPerHour {
				message.Suppressed, message.Reason = true, "RATE_LIMITED"
			} else {
				counts[target.recipient]++
			}
			if index == 0 {
				step0HasDelivery = true
			}
			decision.Messages = append(decision.Messages, message)
		}
		if index == 0 && step0CooldownSuppressed && !step0HasDelivery && alarm.Severity != alarmmodel.SeverityCritical {
			return decision
		}
	}
	return decision
}

type stepTarget struct {
	channel   DeliveryChannel
	recipient string
}

func stepTargets(step EscalationStep) []stepTarget {
	var targets []stepTarget
	seen := map[stepTarget]bool{}
	add := func(target stepTarget) {
		if seen[target] {
			return
		}
		seen[target] = true
		targets = append(targets, target)
	}
	for _, channel := range step.Channels {
		if channel == ChannelWeComBot {
			add(stepTarget{channel, wecomBotRecipient})
			continue
		}
		for _, recipient := range step.Recipients {
			add(stepTarget{channel, recipient})
		}
	}
	sort.SliceStable(targets, func(i, j int) bool { return targets[i].recipient < targets[j].recipient })
	return targets
}

// quietUntil is when the quiet hours around now end, nil outside them.
func (routing SiteRouting) quietUntil(now time.Time) *time.Time {
	if routing.QuietHours == nil {
		return nil
	}
	location, err := time.LoadLocation(routing.Timezone)
	if err != nil {
		return nil
	}
	start, err := clockMinutes(routing.QuietHours.Start)
	if err != nil {
		return nil
	}
	end, err := clockMinutes(routing.QuietHours.End)
	if err != nil {
		return nil
	}
	local := now.In(location)
	minute := local.Hour()*60 + local.Minute()
	inside := (start < end && minute >= start && minute < end) || (start > end && (minute >= start || minute < end))
	if !inside {
		return nil
	}
	ends := time.Date(local.Year(), local.Month(), local.Day(), end/60, end%60, 0, 0, location)
	if !ends.After(local) {
		ends = ends.AddDate(0, 0, 1)
	}
	ends = ends.UTC()
	return &ends
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := []byte{}
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}
