package iam

import (
	"context"
	"log/slog"

	"github.com/quanlaihe/hvac-web/libs/workorderauth"
)

type WorkOrderDecisionAudit struct {
	PrincipalID          string
	ActingOrganizationID string
	SiteID               string
	WorkOrderID          string
	TaskID               string
	AssigneeID           *string
	TeamID               *string
	Action               workorderauth.Action
	Allowed              bool
	PolicyRevision       string
	ReasonCode           workorderauth.ReasonCode
	RequestID            string
	TraceID              string
	OccurredAt           string
}

type WorkOrderDecisionAuditSink interface {
	RecordWorkOrderDecision(context.Context, WorkOrderDecisionAudit) error
}

type loggerWorkOrderDecisionAuditSink struct {
	logger *slog.Logger
}

func newLoggerWorkOrderDecisionAuditSink(logger *slog.Logger) WorkOrderDecisionAuditSink {
	return &loggerWorkOrderDecisionAuditSink{logger: logger}
}

func (sink *loggerWorkOrderDecisionAuditSink) RecordWorkOrderDecision(ctx context.Context, event WorkOrderDecisionAudit) error {
	sink.logger.InfoContext(ctx, "iam_work_order_authorization_decision",
		"trace_id", event.TraceID,
		"request_id", event.RequestID,
		"principal_id", event.PrincipalID,
		"acting_organization_id", event.ActingOrganizationID,
		"site_id", event.SiteID,
		"work_order_id", event.WorkOrderID,
		"task_id", event.TaskID,
		"assignee_id", event.AssigneeID,
		"team_id", event.TeamID,
		"action", event.Action,
		"allowed", event.Allowed,
		"policy_revision", event.PolicyRevision,
		"reason_code", event.ReasonCode,
		"occurred_at", event.OccurredAt,
	)
	return nil
}
