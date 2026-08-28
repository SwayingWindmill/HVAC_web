package iam

import (
	"context"
	"log/slog"

	"github.com/quanlaihe/hvac-web/libs/alarmauth"
)

type AlarmDecisionAudit struct {
	PrincipalID          string
	TenantID string
	SiteID               string
	AlarmID              string
	Action               alarmauth.Action
	Allowed              bool
	PolicyRevision       string
	ReasonCode           alarmauth.ReasonCode
	RequestID            string
	TraceID              string
	OccurredAt           string
}

type AlarmDecisionAuditSink interface {
	RecordAlarmDecision(context.Context, AlarmDecisionAudit) error
}

type loggerAlarmDecisionAuditSink struct {
	logger *slog.Logger
}

func newLoggerAlarmDecisionAuditSink(logger *slog.Logger) AlarmDecisionAuditSink {
	return &loggerAlarmDecisionAuditSink{logger: logger}
}

func (sink *loggerAlarmDecisionAuditSink) RecordAlarmDecision(ctx context.Context, event AlarmDecisionAudit) error {
	sink.logger.InfoContext(ctx, "iam_alarm_authorization_decision",
		"trace_id", event.TraceID,
		"request_id", event.RequestID,
		"principal_id", event.PrincipalID,
		"tenant_id", event.TenantID,
		"site_id", event.SiteID,
		"alarm_id", event.AlarmID,
		"action", event.Action,
		"allowed", event.Allowed,
		"policy_revision", event.PolicyRevision,
		"reason_code", event.ReasonCode,
		"occurred_at", event.OccurredAt,
	)
	return nil
}
