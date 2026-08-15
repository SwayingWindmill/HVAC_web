package iam

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

const maximumLogtoUserIDBytes = 512

type ApprovedLogtoTenantMapping struct {
	LogtoOrganizationID    string                     `json:"logtoOrganizationId"`
	PlatformTenantID       string                     `json:"platformTenantId"`
	Roles                  []ApprovedLogtoRoleMapping `json:"roles"`
}

type ApprovedLogtoRoleMapping struct {
	LogtoRoleID string                `json:"logtoRoleId"`
	RoleKey     string                `json:"roleKey"`
	Actions     []registryauth.Action `json:"actions"`
	Effect      BindingEffect         `json:"effect"`
}

type LogtoReconciliationSeed struct {
	TenantID       string                       `json:"tenantId"`
	SourceVersion  int64                        `json:"sourceVersion"`
	PrincipalID    string                       `json:"principalId"`
	EffectiveAt    time.Time                    `json:"effectiveAt"`
	TenantMappings []ApprovedLogtoTenantMapping `json:"tenantMappings"`
	SiteBindings   []ReconciledSiteBinding      `json:"siteBindings"`
	ExplicitDenies []ReconciledExplicitDeny     `json:"explicitDenies"`
}

type LogtoManagementReader interface {
	User(context.Context, string) (LogtoUser, error)
	UserOrganizations(context.Context, string) ([]LogtoOrganization, error)
}

type LogtoReconciler struct {
	management LogtoManagementReader
	store      ReconciliationStore
	issuer     string
}

func NewLogtoReconciler(management LogtoManagementReader, store ReconciliationStore, issuer string) (*LogtoReconciler, error) {
	issuer = strings.TrimSpace(issuer)
	if management == nil || store == nil || issuer == "" {
		return nil, errors.New("Logto management reader, reconciliation store and issuer are required")
	}
	return &LogtoReconciler{management: management, store: store, issuer: issuer}, nil
}

func (reconciler *LogtoReconciler) ReconcileUser(ctx context.Context, userID string, seed LogtoReconciliationSeed) (ReconciliationResult, error) {
	if reconciler == nil || reconciler.management == nil || reconciler.store == nil || reconciler.issuer == "" {
		return ReconciliationResult{}, errors.New("Logto reconciler is not configured")
	}
	normalizedUserID, err := validateLogtoReconciliationSeed(userID, seed)
	if err != nil {
		return ReconciliationResult{}, err
	}
	preview, err := BuildLogtoReconciliationRequest(LogtoUser{
		ID: normalizedUserID, PrimaryEmail: "validation@example.invalid", Name: "validation",
	}, reconciler.issuer, nil, seed)
	if err != nil {
		return ReconciliationResult{}, err
	}
	if _, _, err := prepareReconciliationRequest(preview); err != nil {
		return ReconciliationResult{}, err
	}
	user, err := reconciler.management.User(ctx, normalizedUserID)
	if err != nil {
		return ReconciliationResult{}, fmt.Errorf("read Logto user input: %w", err)
	}
	if strings.TrimSpace(user.ID) != normalizedUserID {
		return ReconciliationResult{}, errors.New("Logto user response id did not match the requested subject")
	}
	organizations, err := reconciler.management.UserOrganizations(ctx, normalizedUserID)
	if err != nil {
		return ReconciliationResult{}, fmt.Errorf("read Logto organization inputs: %w", err)
	}
	request, err := BuildLogtoReconciliationRequest(user, reconciler.issuer, organizations, seed)
	if err != nil {
		return ReconciliationResult{}, err
	}
	return reconciler.store.Reconcile(ctx, request)
}

func validateLogtoReconciliationSeed(userID string, seed LogtoReconciliationSeed) (string, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" || len(userID) > maximumLogtoUserIDBytes {
		return "", errors.New("Logto user id is missing or too large")
	}
	seed.TenantID = strings.TrimSpace(seed.TenantID)
	if !isUUIDv7(seed.TenantID) {
		return "", errors.New("Logto reconciliation requires an approved Tenant UUIDv7")
	}
	if seed.SourceVersion <= 0 || seed.EffectiveAt.IsZero() {
		return "", errors.New("Logto reconciliation requires a positive source version and effective time")
	}
	seed.PrincipalID = strings.TrimSpace(seed.PrincipalID)
	if !isUUIDv7(seed.PrincipalID) {
		return "", errors.New("Logto reconciliation requires an approved platform Principal UUIDv7")
	}
	return userID, nil
}

func BuildLogtoReconciliationRequest(user LogtoUser, issuer string, organizations []LogtoOrganization, seed LogtoReconciliationSeed) (ReconciliationRequest, error) {
	userID, err := validateLogtoReconciliationSeed(user.ID, seed)
	if err != nil {
		return ReconciliationRequest{}, err
	}
	issuer = strings.TrimSpace(issuer)
	if issuer == "" {
		return ReconciliationRequest{}, errors.New("Logto issuer is required")
	}
	email := strings.TrimSpace(user.PrimaryEmail)
	if email == "" {
		return ReconciliationRequest{}, errors.New("Logto user requires a primary email for platform onboarding")
	}
	displayName := strings.TrimSpace(user.Name)
	if displayName == "" {
		displayName = strings.TrimSpace(user.Username)
	}
	if displayName == "" {
		displayName = email
	}
	status := PrincipalStatusActive
	if user.IsSuspended {
		status = PrincipalStatusDisabled
	}
	principal := ReconciledPrincipal{
		ID: strings.TrimSpace(seed.PrincipalID), SubjectIssuer: issuer, Subject: userID,
		DisplayName: displayName, Email: email, Status: status,
	}
	organizationByID := make(map[string]LogtoOrganization, len(organizations))
	for _, organization := range organizations {
		organizationID := strings.TrimSpace(organization.ID)
		if organizationID == "" {
			continue
		}
		if _, duplicate := organizationByID[organizationID]; duplicate {
			return ReconciliationRequest{}, fmt.Errorf("duplicate Logto organization %q", organizationID)
		}
		organizationByID[organizationID] = organization
	}

	request := ReconciliationRequest{
		TenantID:       strings.TrimSpace(seed.TenantID),
		SourceSystem:   "logto",
		SourceKey:      userID,
		SourceVersion:  seed.SourceVersion,
		Principal:      principal,
		Memberships:    []ReconciledMembership{},
		RoleBindings:   []ReconciledRoleBinding{},
		SiteBindings:   append([]ReconciledSiteBinding(nil), seed.SiteBindings...),
		ExplicitDenies: append([]ReconciledExplicitDeny(nil), seed.ExplicitDenies...),
	}
	seenLogtoOrganizations := map[string]struct{}{}
	seenPlatformTenants := map[string]struct{}{}
	seenMappedRoleKeys := map[string]struct{}{}
	seenProjectedRoleBindings := map[string]struct{}{}
	for mappingIndex, mapping := range seed.TenantMappings {
		logtoOrganizationID := strings.TrimSpace(mapping.LogtoOrganizationID)
		platformTenantID := strings.TrimSpace(mapping.PlatformTenantID)
		if logtoOrganizationID == "" || !isUUIDv7(platformTenantID) {
			return ReconciliationRequest{}, fmt.Errorf("invalid approved Logto organization mapping at index %d", mappingIndex)
		}
		if _, duplicate := seenLogtoOrganizations[logtoOrganizationID]; duplicate {
			return ReconciliationRequest{}, fmt.Errorf("duplicate Logto organization mapping %q", logtoOrganizationID)
		}
		if _, duplicate := seenPlatformTenants[platformTenantID]; duplicate {
			return ReconciliationRequest{}, fmt.Errorf("duplicate platform organization mapping %q", platformTenantID)
		}
		seenLogtoOrganizations[logtoOrganizationID] = struct{}{}
		seenPlatformTenants[platformTenantID] = struct{}{}
		approvedRoles := make(map[string]ApprovedLogtoRoleMapping, len(mapping.Roles))
		for roleIndex, roleMapping := range mapping.Roles {
			roleID := strings.TrimSpace(roleMapping.LogtoRoleID)
			roleKey := strings.TrimSpace(roleMapping.RoleKey)
			if roleID == "" || roleKey == "" || !validBindingEffect(roleMapping.Effect) {
				return ReconciliationRequest{}, fmt.Errorf("invalid approved Logto role mapping at organization %d role %d", mappingIndex, roleIndex)
			}
			actions := append([]registryauth.Action{}, roleMapping.Actions...)
			if err := normalizeActions(&actions); err != nil {
				return ReconciliationRequest{}, fmt.Errorf("invalid approved Logto role mapping at organization %d role %d: %w", mappingIndex, roleIndex, err)
			}
			roleMapping.LogtoRoleID = roleID
			roleMapping.RoleKey = roleKey
			roleMapping.Actions = actions
			if _, duplicate := approvedRoles[roleID]; duplicate {
				return ReconciliationRequest{}, fmt.Errorf("duplicate Logto role mapping %q", roleID)
			}
			mappedRoleKey := platformTenantID + "\x00" + roleKey
			if _, duplicate := seenMappedRoleKeys[mappedRoleKey]; duplicate {
				return ReconciliationRequest{}, fmt.Errorf("multiple Logto roles map to platform role %q", roleKey)
			}
			seenMappedRoleKeys[mappedRoleKey] = struct{}{}
			approvedRoles[roleID] = roleMapping
		}

		organization, member := organizationByID[logtoOrganizationID]
		if !member {
			continue
		}
		request.Memberships = append(request.Memberships, ReconciledMembership{
			TenantID:  platformTenantID,
			Status:    FactStatusActive,
			ValidFrom: seed.EffectiveAt,
		})
		for _, logtoRole := range organization.OrganizationRoles {
			roleMapping, approved := approvedRoles[strings.TrimSpace(logtoRole.ID)]
			if !approved {
				continue
			}
			roleKeyIdentity := platformTenantID + "\x00" + roleMapping.RoleKey
			if _, duplicate := seenProjectedRoleBindings[roleKeyIdentity]; duplicate {
				return ReconciliationRequest{}, fmt.Errorf("duplicate projected platform role %q", roleMapping.RoleKey)
			}
			seenProjectedRoleBindings[roleKeyIdentity] = struct{}{}
			request.RoleBindings = append(request.RoleBindings, ReconciledRoleBinding{
				TenantID: platformTenantID,
				RoleKey:  roleMapping.RoleKey,
				Actions:        append([]registryauth.Action{}, roleMapping.Actions...),
				Effect:         roleMapping.Effect,
				ValidFrom:      seed.EffectiveAt,
			})
		}
	}

	sort.Slice(request.Memberships, func(i, j int) bool {
		return request.Memberships[i].TenantID < request.Memberships[j].TenantID
	})
	sort.Slice(request.RoleBindings, func(i, j int) bool {
		left, right := request.RoleBindings[i], request.RoleBindings[j]
		return left.TenantID+"\x00"+left.RoleKey < right.TenantID+"\x00"+right.RoleKey
	})
	return request, nil
}
