package iam

import (
	"context"
	"log/slog"

	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
)

type TelemetryDecisionAudit struct {
	PrincipalID          string
	ActingOrganizationID string
	Action               telemetryauth.Action
	Allowed              bool
	TargetCount          int
	KeyCount             int
	ScopeDigest          string
	PolicyRevision       string
	ReasonCode           telemetryauth.ReasonCode
	GrantSigned          bool
	DeliveryCode         string
	RequestID            string
	TraceID              string
	OccurredAt           string
}

type TelemetryDecisionAuditSink interface {
	RecordTelemetryDecision(context.Context, TelemetryDecisionAudit) error
}

type loggerTelemetryDecisionAuditSink struct {
	logger *slog.Logger
}

func newLoggerTelemetryDecisionAuditSink(logger *slog.Logger) TelemetryDecisionAuditSink {
	return &loggerTelemetryDecisionAuditSink{logger: logger}
}

func (sink *loggerTelemetryDecisionAuditSink) RecordTelemetryDecision(ctx context.Context, event TelemetryDecisionAudit) error {
	sink.logger.InfoContext(ctx, "iam_telemetry_authorization_decision",
		"trace_id", event.TraceID,
		"request_id", event.RequestID,
		"principal_id", event.PrincipalID,
		"acting_organization_id", event.ActingOrganizationID,
		"action", event.Action,
		"allowed", event.Allowed,
		"target_count", event.TargetCount,
		"key_count", event.KeyCount,
		"scope_digest", event.ScopeDigest,
		"policy_revision", event.PolicyRevision,
		"reason_code", event.ReasonCode,
		"grant_signed", event.GrantSigned,
		"delivery_code", event.DeliveryCode,
		"occurred_at", event.OccurredAt,
	)
	return nil
}
