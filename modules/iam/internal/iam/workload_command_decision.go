package iam

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandauth"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/observability"
)

// CommandWorkloadDecisionPath lets an allowlisted automation workload request a short-lived
// Command authorization grant bound to an active Site auto-control lease and safety bounds.
const CommandWorkloadDecisionPath = "/internal/v1/command/workload-decision"

const (
	defaultOptimizationCommandWorkload = "spiffe://hvac.local/optimization-service"
	defaultCommandWorkloadPresenter    = "spiffe://hvac.local/platform-gateway"
)

type AutoControlLeaseFacts struct {
	ID                  string
	ArmedBy             string
	ExpiresAt           time.Time
	AllowedCapabilities []string
	MaxSetpointStepC    float64
	SupplyTempMinC      float64
	SupplyTempMaxC      float64
	MaxCommandsPerHour  int
	ManualBackoffUntil  *time.Time
}

type CommandWorkloadAuthorizationFacts struct {
	PrincipalFound              bool
	PrincipalID                 string
	PrincipalStatus             FactStatus
	TenantMemberActive          bool
	PolicyRevision              string
	EmergencyRevocationRevision uint64
	DeviceSiteID                string
	SiteControlMode             string
	HasAssetControlLock         bool
	ActiveLease                 *AutoControlLeaseFacts
	RecentAutoCommandsLastHour  int
	MatchingObservingRollback   bool
}

type CommandWorkloadAuthorizationLookup struct {
	SubjectIssuer     string
	Subject           string
	TenantID          string
	SiteID            string
	DeviceID          string
	IsRollback        bool
	RequestedSetpoint *float64
	WindowStart       time.Time
	Now               time.Time
}

type CommandWorkloadAuthorizationStore interface {
	LookupCommandWorkloadAuthorization(context.Context, CommandWorkloadAuthorizationLookup) (CommandWorkloadAuthorizationFacts, error)
}

type denyAllCommandWorkloadStore struct {
	policyRevision string
}

func (store denyAllCommandWorkloadStore) LookupCommandWorkloadAuthorization(_ context.Context, _ CommandWorkloadAuthorizationLookup) (CommandWorkloadAuthorizationFacts, error) {
	return CommandWorkloadAuthorizationFacts{PolicyRevision: store.policyRevision}, nil
}

func evaluateWorkloadCommandAuthorization(
	ctx context.Context,
	store CommandWorkloadAuthorizationStore,
	now time.Time,
	subjectIssuer string,
	subject string,
	request commandauth.WorkloadDecisionRequest,
) (commandauth.Decision, error) {
	var requestedSetpoint *float64
	if value, ok := commandmodel.ParameterValue(request.Capability, request.Parameters); ok {
		requestedSetpoint = &value
	}
	nowUTC := now.UTC()
	facts, err := store.LookupCommandWorkloadAuthorization(ctx, CommandWorkloadAuthorizationLookup{
		SubjectIssuer:     subjectIssuer,
		Subject:           subject,
		TenantID:          request.TenantID,
		SiteID:            request.SiteID,
		DeviceID:          request.DeviceID,
		IsRollback:        request.IsRollback,
		RequestedSetpoint: requestedSetpoint,
		WindowStart:       nowUTC.Add(-time.Hour),
		Now:               nowUTC,
	})
	if err != nil {
		return commandauth.Decision{}, err
	}

	decision := commandauth.Decision{
		SubjectIssuer:               subjectIssuer,
		Subject:                     subject,
		TenantID:                    request.TenantID,
		SiteID:                      request.SiteID,
		DeviceID:                    request.DeviceID,
		Capability:                  request.Capability,
		CapabilityRevision:          request.CapabilityRevision,
		Purpose:                     request.Purpose,
		PolicyRevision:              facts.PolicyRevision,
		EmergencyRevocationRevision: facts.EmergencyRevocationRevision,
		ReasonCode:                  commandauth.ReasonDenyPrincipal,
		DecidedAt:                   commandauth.FormatInstant(nowUTC),
	}

	if !facts.PrincipalFound || facts.PrincipalStatus != FactStatusActive {
		decision.Detail = "calling workload principal is inactive or unknown"
		return decision, nil
	}
	decision.PrincipalID = facts.PrincipalID
	if !facts.TenantMemberActive {
		decision.ReasonCode = commandauth.ReasonDenyMembership
		decision.Detail = "calling workload lacks active tenant membership"
		return decision, nil
	}
	if facts.DeviceSiteID == "" || facts.DeviceSiteID != request.SiteID {
		decision.ReasonCode = commandauth.ReasonDenyScope
		decision.Detail = "target device is inactive or outside the requested site"
		return decision, nil
	}
	if (facts.SiteControlMode != "" && facts.SiteControlMode != "REMOTE_AUTO") || facts.HasAssetControlLock {
		decision.ReasonCode = commandauth.ReasonDenyControlLock
		decision.Detail = "site control exit mode or asset control lock forbids automated commands"
		return decision, nil
	}
	if facts.ActiveLease == nil {
		decision.ReasonCode = commandauth.ReasonDenyAutoLease
		decision.Detail = "no active unexpired site auto-control lease"
		return decision, nil
	}
	lease := facts.ActiveLease
	if request.IsRollback {
		if !facts.MatchingObservingRollback {
			decision.ReasonCode = commandauth.ReasonDenyAutoLease
			decision.Detail = "no observing monitor matches rollback target"
			return decision, nil
		}
	} else if !lease.ExpiresAt.After(nowUTC) {
		decision.ReasonCode = commandauth.ReasonDenyAutoLease
		decision.Detail = "site auto-control lease has expired"
		return decision, nil
	} else if lease.ManualBackoffUntil != nil && !lease.ManualBackoffUntil.Before(nowUTC) {
		decision.ReasonCode = commandauth.ReasonDenyAutoLease
		decision.Detail = "site auto-control lease is paused in manual override backoff window"
		return decision, nil
	}
	if !slices.Contains(lease.AllowedCapabilities, string(request.Capability)) {
		decision.ReasonCode = commandauth.ReasonDenyCapability
		decision.Detail = "capability is not in site auto-control lease allowlist"
		return decision, nil
	}
	profile, supported := commandmodel.CapabilityProfileFor(request.Capability)
	if !supported {
		decision.ReasonCode = commandauth.ReasonDenyCapability
		return decision, nil
	}
	if profile.ParameterKey != "" {
		if requestedSetpoint == nil || math.IsNaN(*requestedSetpoint) || math.IsInf(*requestedSetpoint, 0) {
			decision.ReasonCode = commandauth.ReasonDenyAutoLease
			decision.Detail = "numeric target parameter is required"
			return decision, nil
		}
		target := *requestedSetpoint
		if target < lease.SupplyTempMinC || target > lease.SupplyTempMaxC {
			decision.ReasonCode = commandauth.ReasonDenyAutoLease
			decision.Detail = "target setpoint falls outside lease supply temperature bounds"
			return decision, nil
		}
		if !request.IsRollback {
			if request.CurrentValue == nil || math.IsNaN(*request.CurrentValue) || math.IsInf(*request.CurrentValue, 0) {
				decision.ReasonCode = commandauth.ReasonDenyAutoLease
				decision.Detail = "current measured value is required to verify setpoint step"
				return decision, nil
			}
			if math.Abs(target-*request.CurrentValue) > lease.MaxSetpointStepC+1e-9 {
				decision.ReasonCode = commandauth.ReasonDenyAutoLease
				decision.Detail = "setpoint delta exceeds lease max_setpoint_step_c"
				return decision, nil
			}
		}
	}
	if !request.IsRollback && facts.RecentAutoCommandsLastHour >= lease.MaxCommandsPerHour {
		decision.ReasonCode = commandauth.ReasonDenyRateLimit
		decision.Detail = "hourly automated command budget reached"
		return decision, nil
	}

	riskLevel, _, ok := commandmodel.EvaluateRiskLevel(request.Capability, request.Parameters, request.CurrentValue)
	if !ok {
		if request.IsRollback && request.Capability == commandmodel.CapabilitySetChilledWaterTemperatureSetpoint {
			riskLevel = commandmodel.RiskMedium
		} else {
			decision.ReasonCode = commandauth.ReasonDenyAutoLease
			decision.Detail = "capability parameters violate command safety risk bounds"
			return decision, nil
		}
	}
	decision.Allowed = true
	decision.MaximumRisk = riskLevel
	decision.ReasonCode = commandauth.ReasonAllowAutoLease
	decision.LeaseID = lease.ID
	decision.ArmedBy = lease.ArmedBy
	return decision, nil
}

func (h *handler) handleCommandWorkloadDecision(writer http.ResponseWriter, request *http.Request) int {
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		writeProblem(writer, http.StatusMethodNotAllowed, "IAM_METHOD_NOT_ALLOWED", "This IAM route only supports POST.")
		return http.StatusMethodNotAllowed
	}
	if hasForgedIdentityHeader(request.Header) || strings.TrimSpace(request.Header.Get("X-Delegation-Grant")) != "" || strings.TrimSpace(request.Header.Get("X-Command-Grant")) != "" {
		writeProblem(writer, http.StatusBadRequest, "IAM_FORGED_IDENTITY_HEADER", "Caller-supplied identity headers are not accepted.")
		return http.StatusBadRequest
	}
	_, workload, ok := peerIdentity(request)
	if !ok || !slices.Contains(h.allowedCommandWorkloads, workload) {
		writeProblem(writer, http.StatusUnauthorized, "IAM_WORKLOAD_IDENTITY_INVALID", "The calling workload identity is not trusted.")
		return http.StatusUnauthorized
	}

	request.Body = http.MaxBytesReader(writer, request.Body, maximumDecisionRequestSize)
	var input commandauth.WorkloadDecisionRequest
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || ensureJSONEOF(decoder) != nil || input.Validate() != nil {
		writeProblem(writer, http.StatusBadRequest, "IAM_COMMAND_DECISION_REQUEST_INVALID", "The workload Command authorization request is invalid.")
		return http.StatusBadRequest
	}

	now := h.now().UTC()
	subjectIssuer := workloadSubjectIssuer(workload)
	decision, err := evaluateWorkloadCommandAuthorization(request.Context(), h.commandWorkloadAuthorizationStore, now, subjectIssuer, workload, input)
	if err != nil {
		writeProblem(writer, http.StatusServiceUnavailable, "IAM_AUTHORIZATION_UNAVAILABLE", "The Command authorization facts are unavailable.")
		return http.StatusServiceUnavailable
	}

	response := commandauth.DecisionResponse{Decision: decision}
	deliveryCode := "DECISION_DENIED"
	if decision.Allowed {
		if h.commandGrantSigner == nil {
			writeProblem(writer, http.StatusServiceUnavailable, "IAM_COMMAND_GRANT_SIGNER_UNAVAILABLE", "The Command delegation signer is unavailable.")
			return http.StatusServiceUnavailable
		}
		grantID := h.newCommandGrantID()
		if grantID == "" {
			writeProblem(writer, http.StatusServiceUnavailable, "IAM_COMMAND_GRANT_ID_UNAVAILABLE", "The Command delegation identifier is unavailable.")
			return http.StatusServiceUnavailable
		}
		lifetime := h.commandGrantLifetime
		if lifetime <= 0 || lifetime > commandauth.MaximumGrantLifetime {
			lifetime = commandauth.MaximumGrantLifetime
		}
		grant, signErr := commandauth.SignGrant(h.commandGrantSigner, commandauth.GrantClaims{
			Issuer:                      h.commandGrantIssuer,
			Presenter:                   h.commandWorkloadPresenter,
			Audience:                    h.commandGrantAudience,
			GrantID:                     grantID,
			Purpose:                     decision.Purpose,
			PrincipalID:                 decision.PrincipalID,
			TenantID:                    decision.TenantID,
			SiteID:                      decision.SiteID,
			DeviceID:                    decision.DeviceID,
			Capability:                  decision.Capability,
			MaximumRisk:                 decision.MaximumRisk,
			CapabilityRevision:          decision.CapabilityRevision,
			PolicyRevision:              decision.PolicyRevision,
			EmergencyRevocationRevision: decision.EmergencyRevocationRevision,
			IssuedAt:                    now.Unix(),
			ExpiresAt:                   now.Add(lifetime).Unix(),
			TokenID:                     grantID,
			Transitive:                  false,
			WorkloadSPIFFE:              workload,
			LeaseID:                     decision.LeaseID,
			ArmedBy:                     decision.ArmedBy,
		})
		if signErr != nil {
			writeProblem(writer, http.StatusServiceUnavailable, "IAM_COMMAND_GRANT_SIGNING_FAILED", "The Command delegation could not be signed.")
			return http.StatusServiceUnavailable
		}
		response.DelegationGrant = grant
		deliveryCode = "GRANT_SIGNED"
	}

	_ = h.observability.Metrics.AddCounter(
		"s3_iam_command_workload_decisions_total",
		"IAM automated workload Command authorization decisions.",
		map[string]string{
			"result":     decisionResult(decision.Allowed),
			"capability": string(decision.Capability),
			"reason":     string(decision.ReasonCode),
			"delivery":   deliveryCode,
		},
		1,
	)
	h.logger.InfoContext(request.Context(), "iam_command_workload_decision",
		"workload", workload,
		"principal_id", decision.PrincipalID,
		"tenant_id", decision.TenantID,
		"site_id", decision.SiteID,
		"device_id", decision.DeviceID,
		"capability", decision.Capability,
		"allowed", decision.Allowed,
		"reason", decision.ReasonCode,
		"lease_id", decision.LeaseID,
		"armed_by", decision.ArmedBy,
		"grant_signed", response.DelegationGrant != "",
		"trace_id", observability.TraceID(request.Context()),
	)
	writeJSON(writer, http.StatusOK, response)
	return http.StatusOK
}
