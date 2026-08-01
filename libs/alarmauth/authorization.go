package alarmauth

import (
	"errors"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
)

type Action string

const (
	ActionList Action = "alarm:list"
	ActionRead Action = "alarm:read"
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
	ActingOrganizationID string `json:"actingOrganizationId"`
	SiteID               string `json:"siteId"`
	AlarmID              string `json:"alarmId,omitempty"`
	Action               Action `json:"action"`
}

func (request DecisionRequest) Validate() error {
	if !alarmmodel.IsUUIDv7(request.ActingOrganizationID) || !alarmmodel.IsUUIDv7(request.SiteID) {
		return errors.New("Alarm authorization scope is invalid")
	}
	switch request.Action {
	case ActionList:
		if strings.TrimSpace(request.AlarmID) != "" {
			return errors.New("Alarm list authorization cannot include Alarm identity")
		}
	case ActionRead:
		if !alarmmodel.IsUUIDv7(request.AlarmID) {
			return errors.New("Alarm read authorization requires Alarm identity")
		}
	default:
		return errors.New("Alarm authorization action is unsupported")
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
	AlarmID              string     `json:"alarmId,omitempty"`
	Action               Action     `json:"action"`
	PolicyRevision       string     `json:"policyRevision"`
	ReasonCode           ReasonCode `json:"reasonCode"`
	DecidedAt            string     `json:"decidedAt"`
}

func (decision Decision) Validate() error {
	if strings.TrimSpace(decision.SubjectIssuer) == "" || strings.TrimSpace(decision.Subject) == "" ||
		strings.TrimSpace(decision.PolicyRevision) == "" || decision.ReasonCode == "" {
		return errors.New("Alarm authorization decision evidence is incomplete")
	}
	if err := (DecisionRequest{
		ActingOrganizationID: decision.ActingOrganizationID,
		SiteID:               decision.SiteID,
		AlarmID:              decision.AlarmID,
		Action:               decision.Action,
	}).Validate(); err != nil {
		return err
	}
	if _, err := time.Parse(time.RFC3339Nano, decision.DecidedAt); err != nil {
		return errors.New("Alarm authorization decision instant is invalid")
	}
	if decision.Allowed {
		if strings.TrimSpace(decision.PrincipalID) == "" || decision.ReasonCode != ReasonAllowExactScope {
			return errors.New("allowed Alarm authorization decision is invalid")
		}
	} else if decision.ReasonCode == ReasonAllowExactScope {
		return errors.New("denied Alarm authorization decision has an allow reason")
	}
	return nil
}

type DecisionResponse struct {
	Decision Decision `json:"decision"`
}
