package iam

import (
	"context"
	"log/slog"

	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

type RegistryDecisionAudit struct {
	PrincipalID            string
	ActingOrganizationID   string
	Action                 registryauth.Action
	Allowed                bool
	AllowedOrganizationIDs []string
	AllowedSiteIDs         []string
	DeniedOrganizationIDs  []string
	DeniedSiteIDs          []string
	PolicyRevision         string
	ReasonCode             registryauth.ReasonCode
	GrantSigned            bool
	DeliveryCode           string
	TraceID                string
	OccurredAt             string
}

type RegistryDecisionAuditSink interface {
	RecordRegistryDecision(context.Context, RegistryDecisionAudit) error
}

type loggerRegistryDecisionAuditSink struct {
	logger *slog.Logger
}

func newLoggerRegistryDecisionAuditSink(logger *slog.Logger) RegistryDecisionAuditSink {
	return &loggerRegistryDecisionAuditSink{logger: logger}
}

func (sink *loggerRegistryDecisionAuditSink) RecordRegistryDecision(ctx context.Context, event RegistryDecisionAudit) error {
	sink.logger.InfoContext(ctx, "iam_registry_authorization_decision",
		"trace_id", event.TraceID,
		"principal_id", event.PrincipalID,
		"acting_organization_id", event.ActingOrganizationID,
		"action", event.Action,
		"allowed", event.Allowed,
		"allowed_organization_count", len(event.AllowedOrganizationIDs),
		"allowed_site_count", len(event.AllowedSiteIDs),
		"denied_organization_count", len(event.DeniedOrganizationIDs),
		"denied_site_count", len(event.DeniedSiteIDs),
		"policy_revision", event.PolicyRevision,
		"reason_code", event.ReasonCode,
		"grant_signed", event.GrantSigned,
		"delivery_code", event.DeliveryCode,
		"occurred_at", event.OccurredAt,
	)
	return nil
}
