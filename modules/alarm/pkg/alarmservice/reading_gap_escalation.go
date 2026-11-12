package alarmservice

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
)

const (
	readingGapUnacknowledged24hReason = "READING_GAP_UNACKNOWLEDGED_24H"
	readingGapEscalationThreshold     = 24 * time.Hour
)

func nextReadingGapEscalationSeverity(current alarmmodel.Severity) (alarmmodel.Severity, bool) {
	switch current {
	case alarmmodel.SeverityWarning:
		return alarmmodel.SeverityMajor, true
	case alarmmodel.SeverityMajor:
		return alarmmodel.SeverityCritical, true
	default:
		return "", false
	}
}

func occurrenceInputPreservingReadingGapEscalation(current alarmmodel.Alarm, input alarmmodel.OccurrenceInput) alarmmodel.OccurrenceInput {
	if alarmmodel.IsReadingGapType(current.AlarmType) && current.Acknowledgement == nil && hasTimelineReason(current.Timeline, readingGapUnacknowledged24hReason) {
		if escalated, ok := nextReadingGapEscalationSeverity(input.Severity); ok {
			input.Severity = escalated
		}
	}
	return input
}

func hasTimelineReason(timeline []alarmmodel.TimelineEntry, reason string) bool {
	for _, entry := range timeline {
		if entry.Reason == reason {
			return true
		}
	}
	return false
}

type readingGapEscalationCandidate struct {
	siteID  string
	alarmID string
}

// escalateUnacknowledgedReadingGaps raises each active, unacknowledged reading-gap Alarm older than
// 24 hours by one severity step (WARNING -> MAJOR, MAJOR -> CRITICAL) once, and enqueues a
// SEVERITY_CHANGED outbox event so Notification pushes a reminder at the escalated level (#800).
func (store *PostgresStore) escalateUnacknowledgedReadingGaps(ctx context.Context, tenantID string, now time.Time) (int, error) {
	readTx, err := store.beginTenantTransaction(ctx, tenantID, true)
	if err != nil {
		return 0, err
	}
	rows, err := readTx.Query(ctx, `
SELECT alarm.site_id::text, alarm.alarm_id::text
FROM alarm_runtime.alarm_current AS alarm
WHERE alarm.tenant_id = $1
  AND alarm.condition = 'ACTIVE'
  AND alarm.acknowledged_at IS NULL
  AND alarm.current_severity IN ('WARNING', 'MAJOR')
  AND alarm.first_occurred_at <= $2
  AND (
    alarm.alarm_type LIKE 'reading-gap/%'
    OR alarm.alarm_type LIKE 'readings-not-updated/%'
  )
  AND NOT EXISTS (
    SELECT 1
    FROM alarm_runtime.alarm_timeline AS tl
    WHERE tl.tenant_id = alarm.tenant_id
      AND tl.site_id = alarm.site_id
      AND tl.alarm_id = alarm.alarm_id
      AND tl.reason = $3
  )
ORDER BY alarm.first_occurred_at, alarm.alarm_id
LIMIT $4`,
		tenantID, now.UTC().Add(-readingGapEscalationThreshold), readingGapUnacknowledged24hReason, dueEvaluationBatch)
	if err != nil {
		_ = readTx.Rollback(ctx)
		return 0, fmt.Errorf("find unacknowledged reading-gap Alarms due for escalation: %w", err)
	}
	var candidates []readingGapEscalationCandidate
	for rows.Next() {
		var candidate readingGapEscalationCandidate
		if err := rows.Scan(&candidate.siteID, &candidate.alarmID); err != nil {
			rows.Close()
			_ = readTx.Rollback(ctx)
			return 0, fmt.Errorf("scan unacknowledged reading-gap Alarm candidate: %w", err)
		}
		candidates = append(candidates, candidate)
	}
	rows.Close()
	_ = readTx.Rollback(ctx)
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate unacknowledged reading-gap Alarm candidates: %w", err)
	}

	escalatedCount := 0
	var failures []error
	for _, candidate := range candidates {
		escalated, err := store.escalateOneReadingGap(ctx, tenantID, candidate, now)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if escalated {
			escalatedCount++
		}
	}
	return escalatedCount, errors.Join(failures...)
}

func (store *PostgresStore) escalateOneReadingGap(ctx context.Context, tenantID string, candidate readingGapEscalationCandidate, now time.Time) (bool, error) {
	tx, err := store.beginTenantTransaction(ctx, tenantID, false)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	current, err := getAlarmRow(ctx, tx, tenantID, candidate.siteID, candidate.alarmID, true)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if current.Condition != alarmmodel.ConditionActive || current.Acknowledgement != nil || !alarmmodel.IsReadingGapType(current.AlarmType) || hasTimelineReason(current.Timeline, readingGapUnacknowledged24hReason) {
		return false, nil
	}
	nextSeverity, ok := nextReadingGapEscalationSeverity(current.CurrentSeverity)
	if !ok {
		return false, nil
	}
	firstOccurredAt, err := time.Parse(time.RFC3339Nano, current.FirstOccurredAt)
	if err != nil {
		return false, fmt.Errorf("parse reading-gap Alarm first occurrence: %w", err)
	}
	if now.UTC().Sub(firstOccurredAt) < readingGapEscalationThreshold {
		return false, nil
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, current.UpdatedAt)
	if err != nil {
		return false, fmt.Errorf("parse reading-gap Alarm updated timestamp: %w", err)
	}
	escalatedAt := now.UTC()
	if escalatedAt.Before(updatedAt) {
		return false, nil
	}

	escalated, err := alarmmodel.EscalateSeverity(current, alarmmodel.EscalateSeverityInput{
		Severity:      nextSeverity,
		Reason:        readingGapUnacknowledged24hReason,
		ActorType:     "WORKLOAD",
		ActorID:       "alarm-due-evaluations",
		CorrelationID: "reading-gap-escalation-24h:" + current.AlarmID,
		OccurredAt:    escalatedAt.Format(time.RFC3339Nano),
	})
	if err != nil {
		return false, err
	}
	if err := persistUpdatedAlarm(ctx, tx, current, escalated); err != nil {
		return false, err
	}
	if err := enqueueNotificationEvent(ctx, tx, store, escalated, NotificationSeverityChanged, escalatedAt); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit reading-gap Alarm escalation: %w", err)
	}
	return true, nil
}
