package iam

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

type ReconciliationStatus string

type ReconciliationReason string

type PrincipalStatus string

const (
	ReconciliationApplied     ReconciliationStatus = "APPLIED"
	ReconciliationNoChange    ReconciliationStatus = "NO_CHANGE"
	ReconciliationQuarantined ReconciliationStatus = "QUARANTINED"

	ReasonReconciliationApplied       ReconciliationReason = "RECONCILIATION_APPLIED"
	ReasonInputUnchanged              ReconciliationReason = "INPUT_UNCHANGED"
	ReasonStaleSourceVersion          ReconciliationReason = "STALE_SOURCE_VERSION"
	ReasonSourceVersionConflict       ReconciliationReason = "SOURCE_VERSION_CONFLICT"
	ReasonImmutableIdentityConflict   ReconciliationReason = "IMMUTABLE_IDENTITY_CONFLICT"
	ReasonPrincipalIdentifierConflict ReconciliationReason = "PRINCIPAL_IDENTIFIER_CONFLICT"
	ReasonSourcePrincipalConflict     ReconciliationReason = "SOURCE_PRINCIPAL_CONFLICT"

	PrincipalStatusActive   PrincipalStatus = "ACTIVE"
	PrincipalStatusDisabled PrincipalStatus = "DISABLED"
	PrincipalStatusRetired  PrincipalStatus = "RETIRED"
)

type ReconciliationRequest struct {
	TenantID       string                   `json:"tenantId"`
	SourceSystem   string                   `json:"sourceSystem"`
	SourceKey      string                   `json:"sourceKey"`
	SourceVersion  int64                    `json:"sourceVersion"`
	Principal      ReconciledPrincipal      `json:"principal"`
	Memberships    []ReconciledMembership   `json:"memberships"`
	RoleBindings   []ReconciledRoleBinding  `json:"roleBindings"`
	ExplicitDenies []ReconciledExplicitDeny `json:"explicitDenies"`
}

type ReconciledPrincipal struct {
	ID            string          `json:"id"`
	SubjectIssuer string          `json:"subjectIssuer"`
	Subject       string          `json:"subject"`
	DisplayName   string          `json:"displayName"`
	Email         string          `json:"email"`
	Status        PrincipalStatus `json:"status"`
}

type ReconciledMembership struct {
	TenantID  string     `json:"tenantId"`
	Status    FactStatus `json:"status"`
	ValidFrom time.Time  `json:"validFrom"`
	ValidTo   *time.Time `json:"validTo,omitempty"`
}

// ReconciledRoleBinding assigns an existing Role (built in, or owned by the Tenant) to the whole
// Tenant, or to one Site when SiteID is set.
type ReconciledRoleBinding struct {
	TenantID  string     `json:"tenantId"`
	RoleKey   string     `json:"roleKey"`
	SiteID    string     `json:"siteId,omitempty"`
	ValidFrom time.Time  `json:"validFrom"`
	ValidTo   *time.Time `json:"validTo,omitempty"`
}

type ReconciledExplicitDeny struct {
	TenantID   string     `json:"tenantId"`
	SiteID     string     `json:"siteId,omitempty"`
	Action     string     `json:"action"`
	ReasonCode string     `json:"reasonCode"`
	ValidFrom  time.Time  `json:"validFrom"`
	ValidTo    *time.Time `json:"validTo,omitempty"`
}

type ReconciliationResult struct {
	EventID       string               `json:"eventId"`
	Status        ReconciliationStatus `json:"status"`
	ReasonCode    ReconciliationReason `json:"reasonCode"`
	PrincipalID   string               `json:"principalId,omitempty"`
	SourceVersion int64                `json:"sourceVersion"`
	InputHash     string               `json:"inputHash"`
}

type ReconciliationStore interface {
	Reconcile(context.Context, ReconciliationRequest) (ReconciliationResult, error)
}

func prepareReconciliationRequest(request ReconciliationRequest) (ReconciliationRequest, string, error) {
	request = cloneReconciliationRequest(request)
	request.TenantID = strings.TrimSpace(request.TenantID)
	request.SourceSystem = strings.TrimSpace(request.SourceSystem)
	request.SourceKey = strings.TrimSpace(request.SourceKey)
	request.Principal.ID = strings.TrimSpace(request.Principal.ID)
	request.Principal.SubjectIssuer = strings.TrimSpace(request.Principal.SubjectIssuer)
	request.Principal.Subject = strings.TrimSpace(request.Principal.Subject)
	request.Principal.DisplayName = strings.TrimSpace(request.Principal.DisplayName)
	request.Principal.Email = strings.TrimSpace(request.Principal.Email)
	if !isUUIDv7(request.TenantID) {
		return ReconciliationRequest{}, "", errors.New("reconciliation Tenant requires UUIDv7 id")
	}
	if request.SourceSystem == "" || request.SourceKey == "" || request.SourceVersion <= 0 {
		return ReconciliationRequest{}, "", errors.New("reconciliation source identity and positive version are required")
	}
	if !isUUIDv7(request.Principal.ID) || request.Principal.SubjectIssuer == "" || request.Principal.Subject == "" {
		return ReconciliationRequest{}, "", errors.New("reconciliation principal requires UUIDv7 id, issuer and immutable subject")
	}
	if request.Principal.DisplayName == "" || request.Principal.Email == "" {
		return ReconciliationRequest{}, "", errors.New("reconciliation principal display name and email are required")
	}
	if request.Principal.Status != PrincipalStatusActive && request.Principal.Status != PrincipalStatusDisabled && request.Principal.Status != PrincipalStatusRetired {
		return ReconciliationRequest{}, "", fmt.Errorf("unsupported principal status %q", request.Principal.Status)
	}

	seenMemberships := map[string]struct{}{}
	seenRoleBindings := map[string]struct{}{}
	seenExplicitDenies := map[string]struct{}{}

	for index := range request.Memberships {
		membership := &request.Memberships[index]
		if membership.TenantID != request.TenantID || !validFactStatus(membership.Status) || !validEffectiveRange(membership.ValidFrom, membership.ValidTo) {
			return ReconciliationRequest{}, "", fmt.Errorf("invalid membership at index %d", index)
		}
		if _, duplicate := seenMemberships[membership.TenantID]; duplicate {
			return ReconciliationRequest{}, "", fmt.Errorf("duplicate membership for organization %q", membership.TenantID)
		}
		seenMemberships[membership.TenantID] = struct{}{}
		normalizeTimes(&membership.ValidFrom, &membership.ValidTo)
	}
	for index := range request.RoleBindings {
		binding := &request.RoleBindings[index]
		binding.RoleKey = strings.TrimSpace(binding.RoleKey)
		if binding.TenantID != request.TenantID || binding.RoleKey == "" || (binding.SiteID != "" && !isUUIDv7(binding.SiteID)) || !validEffectiveRange(binding.ValidFrom, binding.ValidTo) {
			return ReconciliationRequest{}, "", fmt.Errorf("invalid role binding at index %d", index)
		}
		roleIdentity := binding.TenantID + "\x00" + binding.RoleKey + "\x00" + binding.SiteID
		if _, duplicate := seenRoleBindings[roleIdentity]; duplicate {
			return ReconciliationRequest{}, "", fmt.Errorf("duplicate role binding %q", binding.RoleKey)
		}
		seenRoleBindings[roleIdentity] = struct{}{}
		normalizeTimes(&binding.ValidFrom, &binding.ValidTo)
	}
	for index := range request.ExplicitDenies {
		deny := &request.ExplicitDenies[index]
		deny.ReasonCode = strings.TrimSpace(deny.ReasonCode)
		if deny.TenantID != request.TenantID || (deny.SiteID != "" && !isUUIDv7(deny.SiteID)) || !catalogCapabilityValid(deny.Action) || deny.ReasonCode == "" || !validEffectiveRange(deny.ValidFrom, deny.ValidTo) {
			return ReconciliationRequest{}, "", fmt.Errorf("invalid explicit deny at index %d", index)
		}
		denyIdentity := deny.TenantID + "\x00" + deny.SiteID + "\x00" + string(deny.Action)
		if _, duplicate := seenExplicitDenies[denyIdentity]; duplicate {
			return ReconciliationRequest{}, "", fmt.Errorf("duplicate explicit deny for action %q", deny.Action)
		}
		seenExplicitDenies[denyIdentity] = struct{}{}
		normalizeTimes(&deny.ValidFrom, &deny.ValidTo)
	}

	sort.Slice(request.Memberships, func(i, j int) bool {
		return request.Memberships[i].TenantID < request.Memberships[j].TenantID
	})
	sort.Slice(request.RoleBindings, func(i, j int) bool {
		left, right := request.RoleBindings[i], request.RoleBindings[j]
		return left.TenantID+"\x00"+left.RoleKey+"\x00"+left.SiteID < right.TenantID+"\x00"+right.RoleKey+"\x00"+right.SiteID
	})
	sort.Slice(request.ExplicitDenies, func(i, j int) bool {
		left, right := request.ExplicitDenies[i], request.ExplicitDenies[j]
		return left.TenantID+"\x00"+left.SiteID+"\x00"+string(left.Action) < right.TenantID+"\x00"+right.SiteID+"\x00"+string(right.Action)
	})

	encoded, err := json.Marshal(request)
	if err != nil {
		return ReconciliationRequest{}, "", fmt.Errorf("encode reconciliation input: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return request, hex.EncodeToString(digest[:]), nil
}

func cloneReconciliationRequest(request ReconciliationRequest) ReconciliationRequest {
	request.Memberships = append([]ReconciledMembership{}, request.Memberships...)
	request.RoleBindings = append([]ReconciledRoleBinding{}, request.RoleBindings...)
	request.ExplicitDenies = append([]ReconciledExplicitDeny{}, request.ExplicitDenies...)
	return request
}

func validFactStatus(status FactStatus) bool {
	return status == FactStatusActive || status == FactStatusSuspended || status == FactStatusRevoked
}

func validEffectiveRange(validFrom time.Time, validTo *time.Time) bool {
	return !validFrom.IsZero() && (validTo == nil || validTo.After(validFrom))
}

func normalizeTimes(validFrom *time.Time, validTo **time.Time) {
	*validFrom = validFrom.UTC()
	if *validTo != nil {
		value := (*validTo).UTC()
		*validTo = &value
	}
}

func isUUIDv7(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	compact := strings.ReplaceAll(value, "-", "")
	decoded := make([]byte, 16)
	if _, err := hex.Decode(decoded, []byte(compact)); err != nil {
		return false
	}
	return decoded[6]>>4 == 7 && decoded[8]>>6 == 2
}

func newUUIDv7(now time.Time) (string, error) {
	var value [16]byte
	milliseconds := uint64(now.UTC().UnixMilli())
	value[0] = byte(milliseconds >> 40)
	value[1] = byte(milliseconds >> 32)
	value[2] = byte(milliseconds >> 24)
	value[3] = byte(milliseconds >> 16)
	value[4] = byte(milliseconds >> 8)
	value[5] = byte(milliseconds)
	if _, err := rand.Read(value[6:]); err != nil {
		return "", fmt.Errorf("generate UUIDv7 entropy: %w", err)
	}
	value[6] = (value[6] & 0x0f) | 0x70
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32], nil
}
