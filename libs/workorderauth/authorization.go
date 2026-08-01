package workorderauth

import (
	"errors"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/workordermodel"
)

type Action string

const (
	ActionList   Action = "work-order:list"
	ActionRead   Action = "work-order:read"
	ActionCreate Action = "work-order:create"
	ActionAssign Action = "work-order:assign"
)

type ReasonCode string

const (
	ReasonAllowExactScope ReasonCode = "ALLOW_EXACT_SCOPE"
	ReasonDenyPrincipal   ReasonCode = "DENY_PRINCIPAL"
	ReasonDenyMembership  ReasonCode = "DENY_MEMBERSHIP"
	ReasonDenyExplicit    ReasonCode = "DENY_EXPLICIT"
	ReasonDenyScope       ReasonCode = "DENY_SCOPE"
)

type DecisionRequest struct {
	ActingOrganizationID string  `json:"actingOrganizationId"`
	SiteID               string  `json:"siteId"`
	WorkOrderID          string  `json:"workOrderId,omitempty"`
	AssigneeID           *string `json:"assigneeId,omitempty"`
	TeamID               *string `json:"teamId,omitempty"`
	Action               Action  `json:"action"`
}

func (request DecisionRequest) Validate() error {
	if !workordermodel.IsUUIDv7(request.ActingOrganizationID) || !workordermodel.IsUUIDv7(request.SiteID) {
		return errors.New("Work Order authorization scope is invalid")
	}
	switch request.Action {
	case ActionList:
		if strings.TrimSpace(request.WorkOrderID) != "" || request.AssigneeID != nil || request.TeamID != nil {
			return errors.New("Work Order list authorization cannot include resource or ownership targets")
		}
	case ActionRead:
		if !workordermodel.IsUUIDv7(request.WorkOrderID) || request.AssigneeID != nil || request.TeamID != nil {
			return errors.New("Work Order read authorization requires only Work Order identity")
		}
	case ActionCreate:
		if strings.TrimSpace(request.WorkOrderID) != "" || !validOptionalTarget(request.AssigneeID) || !validOptionalTarget(request.TeamID) {
			return errors.New("Work Order create authorization scope is invalid")
		}
	case ActionAssign:
		if !workordermodel.IsUUIDv7(request.WorkOrderID) || !validOptionalTarget(request.AssigneeID) || !validOptionalTarget(request.TeamID) {
			return errors.New("Work Order assignment authorization scope is invalid")
		}
	default:
		return errors.New("Work Order authorization action is unsupported")
	}
	return nil
}

type Decision struct {
	Allowed              bool       `json:"allowed"`
	PrincipalID          string     `json:"principalId,omitempty"`
	SubjectIssuer        string     `json:"subjectIssuer"`
	Subject              string     `json:"subject"`
	ActingOrganizationID string     `json:"actingOrganizationId"`
	SiteID               string     `json:"siteId"`
	WorkOrderID          string     `json:"workOrderId,omitempty"`
	AssigneeID           *string    `json:"assigneeId,omitempty"`
	TeamID               *string    `json:"teamId,omitempty"`
	Action               Action     `json:"action"`
	PolicyRevision       string     `json:"policyRevision"`
	ReasonCode           ReasonCode `json:"reasonCode"`
	DecidedAt            string     `json:"decidedAt"`
}

func (decision Decision) Validate() error {
	if strings.TrimSpace(decision.SubjectIssuer) == "" || strings.TrimSpace(decision.Subject) == "" ||
		strings.TrimSpace(decision.PolicyRevision) == "" || decision.ReasonCode == "" {
		return errors.New("Work Order authorization decision evidence is incomplete")
	}
	if err := (DecisionRequest{
		ActingOrganizationID: decision.ActingOrganizationID,
		SiteID:               decision.SiteID,
		WorkOrderID:          decision.WorkOrderID,
		AssigneeID:           decision.AssigneeID,
		TeamID:               decision.TeamID,
		Action:               decision.Action,
	}).Validate(); err != nil {
		return err
	}
	if _, err := time.Parse(time.RFC3339Nano, decision.DecidedAt); err != nil {
		return errors.New("Work Order authorization decision instant is invalid")
	}
	if decision.Allowed {
		if strings.TrimSpace(decision.PrincipalID) == "" || decision.ReasonCode != ReasonAllowExactScope {
			return errors.New("allowed Work Order authorization decision is invalid")
		}
	} else if decision.ReasonCode == ReasonAllowExactScope {
		return errors.New("denied Work Order authorization decision has an allow reason")
	}
	return nil
}

type DecisionResponse struct {
	Decision Decision `json:"decision"`
}

func validOptionalTarget(value *string) bool {
	if value == nil {
		return true
	}
	trimmed := strings.TrimSpace(*value)
	return trimmed != "" && trimmed == *value && len(trimmed) <= 256
}
