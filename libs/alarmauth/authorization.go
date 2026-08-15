package alarmauth

import (
	"errors"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
)

type Action string

const (
	ActionRead Action = "alarm:read"
	ActionAck  Action = "alarm:ack"
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
	TenantID string `json:"tenantId"`
	SiteID   string `json:"siteId"`
	AlarmID  string `json:"alarmId,omitempty"`
	Action   Action `json:"action"`
}

func (request DecisionRequest) Validate() error {
	if !alarmmodel.IsUUIDv7(request.TenantID) || !alarmmodel.IsUUIDv7(request.SiteID) {
		return errors.New("Alarm authorization scope is invalid")
	}
	switch request.Action {
	case ActionRead:
		if strings.TrimSpace(request.AlarmID) != "" && !alarmmodel.IsUUIDv7(request.AlarmID) {
			return errors.New("Alarm read authorization has an invalid Alarm identity")
		}
	case ActionAck:
		if !alarmmodel.IsUUIDv7(request.AlarmID) {
			return errors.New("Alarm acknowledgement authorization requires Alarm identity")
		}
	default:
		return errors.New("Alarm authorization action is unsupported")
	}
	return nil
}

type Decision struct {
	Allowed        bool       `json:"allowed"`
	PrincipalID    string     `json:"principalId,omitempty"`
	SubjectIssuer  string     `json:"subjectIssuer"`
	Subject        string     `json:"subject"`
	TenantID       string     `json:"tenantId"`
	SiteID         string     `json:"siteId"`
	AlarmID        string     `json:"alarmId,omitempty"`
	Action         Action     `json:"action"`
	PolicyRevision string     `json:"policyRevision"`
	ReasonCode     ReasonCode `json:"reasonCode"`
	DecidedAt      string     `json:"decidedAt"`
}

func (decision Decision) Validate() error {
	if strings.TrimSpace(decision.SubjectIssuer) == "" || strings.TrimSpace(decision.Subject) == "" ||
		strings.TrimSpace(decision.PolicyRevision) == "" || decision.ReasonCode == "" {
		return errors.New("Alarm authorization decision evidence is incomplete")
	}
	if err := (DecisionRequest{
		TenantID: decision.TenantID,
		SiteID:   decision.SiteID,
		AlarmID:  decision.AlarmID,
		Action:   decision.Action,
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
