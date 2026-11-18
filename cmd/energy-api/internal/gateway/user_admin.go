package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/sessionstore"
	"github.com/quanlaihe/hvac-web/modules/audit/pkg/auditserver"
	"github.com/quanlaihe/hvac-web/modules/iam/pkg/iamserver"
	"github.com/quanlaihe/hvac-web/services/identity-service/pkg/identityadmin"
)

// User administration (spec docs/specs/iam-roles-and-users.md) composes two owners: the identity
// directory holds accounts and passwords, IAM holds membership and Role Assignments and decides
// who may administer. Every request first reads IAM's member list as the acting administrator,
// so IAM authorizes it and an account outside the acting Tenant is never touched. Changes go to
// IAM before the directory, so a refused IAM change leaves the account as it was.
const (
	publicMembersPath        = "/api/v1/iam/members"
	publicMembersPrefix      = "/api/v1/iam/members/"
	publicOwnPasswordPath    = "/api/v1/me/password"
	internalIAMMembersPath   = "/internal/v1/admin/members"
	maximumUserAdminBodySize = 16 << 10
)

// UserDirectory is the identity directory as user administration uses it. Administrator
// arguments name who made a change, for the directory's security audit.
type UserDirectory interface {
	CreateUser(context.Context, identityadmin.NewUser) (identityadmin.User, error)
	UsersByID(context.Context, []string) ([]identityadmin.User, error)
	SetPassword(ctx context.Context, administrator, userID, password string) error
	SetEnabled(ctx context.Context, administrator, userID string, enabled bool) error
	UpdateProfile(ctx context.Context, administrator, userID, displayName, email string) error
	ChangePassword(ctx context.Context, userID, current, next string) error
}

type memberAssignment struct {
	BindingID string `json:"bindingId"`
	RoleKey   string `json:"roleKey"`
	SiteID    string `json:"siteId,omitempty"`
}

type memberView struct {
	PrincipalID string             `json:"principalId"`
	Username    string             `json:"username"`
	DisplayName string             `json:"displayName"`
	Email       string             `json:"email"`
	Enabled     bool               `json:"enabled"`
	Locked      bool               `json:"locked"`
	MFAEnabled  bool               `json:"mfaEnabled"`
	LastLoginAt *string            `json:"lastLoginAt,omitempty"`
	Self        bool               `json:"self"`
	Assignments []memberAssignment `json:"assignments"`
}

type roleView struct {
	RoleKey      string `json:"roleKey"`
	TenantScoped bool   `json:"tenantScoped"`
}

type membersResponse struct {
	Roles   []roleView   `json:"roles"`
	Members []memberView `json:"members"`
}

type assignmentInput struct {
	RoleKey string `json:"roleKey"`
	SiteID  string `json:"siteId,omitempty"`
}

type createMemberRequest struct {
	Username    string            `json:"username"`
	DisplayName string            `json:"displayName"`
	Email       string            `json:"email"`
	Password    string            `json:"password"`
	Assignments []assignmentInput `json:"assignments"`
}

// userAdminRequest is one administrator's request with IAM's view of their Tenant.
type userAdminRequest struct {
	session   bffSession
	directory iamserver.AdminDirectory
}

func (admin userAdminRequest) administrator() string { return admin.session.Principal.Subject }

// UserAdmin serves 系统管理 → 用户与角色.
func (h *handler) UserAdmin(writer http.ResponseWriter, request *http.Request) {
	path := request.URL.Path
	if path == publicMembersPath && request.Method == http.MethodGet {
		h.withUserAdmin(writer, request, h.listMembers)
		return
	}
	if request.Method != http.MethodPost {
		writeMethodNotAllowedFor(writer, request, http.MethodPost)
		return
	}
	if path == publicMembersPath {
		h.withUserAdmin(writer, request, h.createMember)
		return
	}
	parts := strings.Split(strings.TrimPrefix(path, publicMembersPrefix), "/")
	var change memberChangeFunc
	switch {
	case len(parts) == 2 && parts[1] == "password":
		change = h.resetMemberPassword
	case len(parts) == 2 && parts[1] == "status":
		change = h.setMemberStatus
	case len(parts) == 2 && parts[1] == "profile":
		change = h.updateMemberProfile
	case len(parts) == 2 && parts[1] == "assignments":
		change = h.assignMemberRole
	case len(parts) == 4 && parts[1] == "assignments" && parts[3] == "revoke":
		bindingID := parts[2]
		change = func(writer http.ResponseWriter, request *http.Request, admin userAdminRequest, member iamserver.AdminMember) {
			h.revokeMemberRole(writer, request, admin, member, bindingID)
		}
	default:
		writeIdentityFailure(writer, request, identityFailure{404, "RESOURCE_NOT_FOUND", "Not found", "The user administration route does not exist.", false})
		return
	}
	principalID := parts[0]
	h.withUserAdmin(writer, request, func(writer http.ResponseWriter, request *http.Request, admin userAdminRequest) {
		for _, member := range admin.directory.Members {
			if member.PrincipalID == principalID {
				change(writer, request, admin, member)
				return
			}
		}
		h.emitIAMAdminAudit(request, admin.session, "principal:"+principalID, "组织成员", iamAdminActionForPath(path), auditOutcomeRejected, map[string]any{"reasonCode": "RESOURCE_NOT_FOUND"})
		writeIdentityFailure(writer, request, identityFailure{404, "RESOURCE_NOT_FOUND", "User not found", "This user is not a member of the current organization.", false})
	})
}

type memberChangeFunc func(http.ResponseWriter, *http.Request, userAdminRequest, iamserver.AdminMember)

// withUserAdmin checks the Session and CSRF token, then reads IAM's member list as the acting
// administrator; IAM refuses anyone without iam.admin.
func (h *handler) withUserAdmin(writer http.ResponseWriter, request *http.Request, serve func(http.ResponseWriter, *http.Request, userAdminRequest)) {
	session, failure := h.identitySession(request)
	if failure == nil && request.Method != http.MethodGet {
		failure = h.identity.validateStateChange(request, session, request.Header.Get("X-CSRF-Token"))
	}
	if failure == nil && h.identity.config.UserDirectory == nil {
		failure = &identityFailure{503, "USER_ADMIN_UNAVAILABLE", "User administration unavailable", "The identity directory is not configured for this Gateway.", true}
	}
	if failure != nil {
		writeIdentityFailure(writer, request, *failure)
		return
	}
	var directory iamserver.AdminDirectory
	status, code, ok := h.callIAMAdminWithOutcome(writer, request, session, internalIAMMembersPath, nil, &directory)
	if !ok {
		if request.Method != http.MethodGet {
			h.emitDomainAudit(request.Context(), request, session, domainAuditInput{
				Category:      auditserver.AggregateTypeIAMAdmin,
				TargetID:      request.URL.Path,
				TargetLabel:   "成员与权限管理",
				Action:        "IAM_ADMIN_ACCESS",
				Outcome:       auditOutcomeForStatus(status),
				ChangeSummary: map[string]any{"reasonCode": code},
			})
		}
		return
	}
	// Only people who sign in through this platform's identity directory are users; Workload
	// Principals are members too but are not managed here.
	people := []iamserver.AdminMember{}
	for _, member := range directory.Members {
		if member.SubjectIssuer == session.Principal.Issuer {
			people = append(people, member)
		}
	}
	directory.Members = people
	serve(writer, request, userAdminRequest{session: session, directory: directory})
}

func (h *handler) listMembers(writer http.ResponseWriter, request *http.Request, admin userAdminRequest) {
	subjects := make([]string, len(admin.directory.Members))
	for index, member := range admin.directory.Members {
		subjects[index] = member.Subject
	}
	accounts, err := h.identity.config.UserDirectory.UsersByID(request.Context(), subjects)
	if err != nil {
		writeDirectoryFailure(writer, request, err)
		return
	}
	byID := make(map[string]identityadmin.User, len(accounts))
	for _, account := range accounts {
		byID[account.ID] = account
	}
	now := h.now()
	response := membersResponse{Roles: []roleView{}, Members: []memberView{}}
	for _, role := range admin.directory.Roles {
		response.Roles = append(response.Roles, roleView{RoleKey: role.RoleKey, TenantScoped: role.TenantScoped})
	}
	for _, member := range admin.directory.Members {
		account, found := byID[member.Subject]
		if !found {
			continue
		}
		// A person can sign in only while both owners let them: the membership and the account.
		view := memberView{
			PrincipalID: member.PrincipalID, Username: account.Username, DisplayName: account.DisplayName, Email: account.Email,
			Enabled:     member.MembershipStatus == "ACTIVE" && account.Status == "ACTIVE",
			Locked:      account.LockedUntil != nil && account.LockedUntil.After(now),
			MFAEnabled:  account.MFAEnabled,
			Self:        member.PrincipalID == admin.directory.ActorPrincipalID,
			Assignments: []memberAssignment{},
		}
		if account.LastLoginAt != nil {
			value := account.LastLoginAt.UTC().Format(time.RFC3339)
			view.LastLoginAt = &value
		}
		for _, assignment := range member.Assignments {
			if assignment.Status == "ACTIVE" {
				view.Assignments = append(view.Assignments, memberAssignment{BindingID: assignment.BindingID, RoleKey: assignment.RoleKey, SiteID: assignment.SiteID})
			}
		}
		response.Members = append(response.Members, view)
	}
	sort.SliceStable(response.Members, func(left, right int) bool { return response.Members[left].Username < response.Members[right].Username })
	writer.Header().Set("Cache-Control", "no-store")
	writeJSON(writer, http.StatusOK, response)
}

func (h *handler) createMember(writer http.ResponseWriter, request *http.Request, admin userAdminRequest) {
	var input createMemberRequest
	if !decodeUserAdminBody(writer, request, &input) {
		h.emitIAMAdminAudit(request, admin.session, publicMembersPath, "组织成员", "IAM_MEMBER_CREATED", auditOutcomeRejected, map[string]any{"reasonCode": "USER_ADMIN_REQUEST_INVALID"})
		return
	}
	targetLabel := firstNonEmpty(strings.TrimSpace(input.DisplayName), strings.TrimSpace(input.Username), "组织成员")
	if len(input.Assignments) == 0 {
		h.emitIAMAdminAudit(request, admin.session, "username:"+strings.TrimSpace(input.Username), targetLabel, "IAM_MEMBER_CREATED", auditOutcomeRejected, map[string]any{"reasonCode": "USER_ADMIN_REQUEST_INVALID"})
		writeIdentityFailure(writer, request, identityFailure{400, "USER_ADMIN_REQUEST_INVALID", "Request invalid", "A new user needs at least one role.", false})
		return
	}
	assignments := make([]iamserver.AdminAssignment, len(input.Assignments))
	roleKeys := make([]string, len(input.Assignments))
	for index, item := range input.Assignments {
		role, ok := roleNamed(admin.directory, item.RoleKey)
		// IAM decides scope too, but by then the account would exist; refuse before creating it.
		if !ok || (role.TenantScoped && item.SiteID != "") {
			h.emitIAMAdminAudit(request, admin.session, "username:"+strings.TrimSpace(input.Username), targetLabel, "IAM_MEMBER_CREATED", auditOutcomeRejected, map[string]any{"reasonCode": "USER_ADMIN_REQUEST_INVALID"})
			writeIdentityFailure(writer, request, identityFailure{400, "USER_ADMIN_REQUEST_INVALID", "Role invalid", "The role or its scope is not valid.", false})
			return
		}
		assignments[index] = iamserver.AdminAssignment{RoleTemplateID: role.RoleTemplateID, SiteID: item.SiteID}
		roleKeys[index] = item.RoleKey
	}
	account, err := h.identity.config.UserDirectory.CreateUser(request.Context(), identityadmin.NewUser{
		Username: input.Username, DisplayName: input.DisplayName, Email: input.Email, Password: input.Password,
	})
	if err != nil {
		h.emitDomainAudit(request.Context(), request, admin.session, domainAuditInput{
			Category:      auditserver.AggregateTypeIAMAdmin,
			TargetID:      "username:" + strings.TrimSpace(input.Username),
			TargetLabel:   targetLabel,
			Action:        "IAM_MEMBER_CREATED",
			Outcome:       auditOutcomeRejected,
			ChangeSummary: map[string]any{"roles": roleKeys, "username": input.Username},
		})
		writeDirectoryFailure(writer, request, err)
		return
	}
	var result iamserver.AdminMutationResult
	status, code, ok := h.callIAMAdminWithOutcome(writer, request, admin.session, internalIAMAdminMutationPath, iamserver.AdminMutationRequest{
		Operation: "member.create", SubjectIssuer: admin.session.Principal.Issuer, Subject: account.ID,
		DisplayName: account.DisplayName, Email: account.Email, Assignments: assignments,
	}, &result)
	if !ok {
		h.emitDomainAudit(request.Context(), request, admin.session, domainAuditInput{
			Category:      auditserver.AggregateTypeIAMAdmin,
			TargetID:      "subject:" + account.ID,
			TargetLabel:   firstNonEmpty(account.DisplayName, targetLabel),
			Action:        "IAM_MEMBER_CREATED",
			Outcome:       auditOutcomeForStatus(status),
			ChangeSummary: map[string]any{"roles": roleKeys, "username": input.Username, "reasonCode": code},
		})
		return
	}
	h.emitDomainAudit(request.Context(), request, admin.session, domainAuditInput{
		Category:      auditserver.AggregateTypeIAMAdmin,
		TargetID:      "principal:" + result.ResourceID,
		TargetLabel:   firstNonEmpty(account.DisplayName, targetLabel),
		Action:        "IAM_MEMBER_CREATED",
		Outcome:       auditOutcomeSucceeded,
		ChangeSummary: map[string]any{"roles": roleKeys, "username": input.Username},
	})
	writeJSON(writer, http.StatusCreated, map[string]string{"principalId": result.ResourceID})
}

func (h *handler) resetMemberPassword(writer http.ResponseWriter, request *http.Request, admin userAdminRequest, member iamserver.AdminMember) {
	targetLabel := firstNonEmpty(member.DisplayName, "组织成员")
	if member.PrincipalID == admin.directory.ActorPrincipalID {
		h.emitDomainAudit(request.Context(), request, admin.session, domainAuditInput{
			Category:      auditserver.AggregateTypeIAMAdmin,
			TargetID:      "principal:" + member.PrincipalID,
			TargetLabel:   targetLabel,
			Action:        "IAM_MEMBER_PASSWORD_RESET",
			Outcome:       auditOutcomeRejected,
			ChangeSummary: map[string]any{"reasonCode": "OWN_PASSWORD_REQUIRES_CURRENT"},
		})
		writeIdentityFailure(writer, request, identityFailure{409, "OWN_PASSWORD_REQUIRES_CURRENT", "Use password change", "Change your own password with your current password.", false})
		return
	}
	var input struct {
		Password string `json:"password"`
	}
	if !decodeUserAdminBody(writer, request, &input) {
		h.emitIAMAdminAudit(request, admin.session, "principal:"+member.PrincipalID, targetLabel, "IAM_MEMBER_PASSWORD_RESET", auditOutcomeRejected, map[string]any{"passwordChanged": false, "reasonCode": "USER_ADMIN_REQUEST_INVALID"})
		return
	}
	if err := h.identity.config.UserDirectory.SetPassword(request.Context(), admin.administrator(), member.Subject, input.Password); err != nil {
		h.emitDomainAudit(request.Context(), request, admin.session, domainAuditInput{
			Category:      auditserver.AggregateTypeIAMAdmin,
			TargetID:      "principal:" + member.PrincipalID,
			TargetLabel:   targetLabel,
			Action:        "IAM_MEMBER_PASSWORD_RESET",
			Outcome:       auditOutcomeRejected,
			ChangeSummary: map[string]any{"passwordChanged": false},
		})
		writeDirectoryFailure(writer, request, err)
		return
	}
	h.emitDomainAudit(request.Context(), request, admin.session, domainAuditInput{
		Category:      auditserver.AggregateTypeIAMAdmin,
		TargetID:      "principal:" + member.PrincipalID,
		TargetLabel:   targetLabel,
		Action:        "IAM_MEMBER_PASSWORD_RESET",
		Outcome:       auditOutcomeSucceeded,
		ChangeSummary: map[string]any{"passwordChanged": true},
	})
	writer.WriteHeader(http.StatusNoContent)
}

func (h *handler) setMemberStatus(writer http.ResponseWriter, request *http.Request, admin userAdminRequest, member iamserver.AdminMember) {
	targetLabel := firstNonEmpty(member.DisplayName, "组织成员")
	var input struct {
		Enabled bool `json:"enabled"`
	}
	if !decodeUserAdminBody(writer, request, &input) {
		h.emitIAMAdminAudit(request, admin.session, "principal:"+member.PrincipalID, targetLabel, "IAM_MEMBER_STATUS_UPDATED", auditOutcomeRejected, map[string]any{"reasonCode": "USER_ADMIN_REQUEST_INVALID"})
		return
	}
	status := "SUSPENDED"
	if input.Enabled {
		status = "ACTIVE"
	}
	beforeEnabled := strings.EqualFold(member.MembershipStatus, "ACTIVE")
	iamStatus, code, ok := h.callIAMAdminWithOutcome(writer, request, admin.session, internalIAMAdminMutationPath, iamserver.AdminMutationRequest{
		Operation: "membership.upsert", PrincipalID: member.PrincipalID, Status: status, ExpectedRevision: member.MembershipRevision,
	}, nil)
	if !ok {
		h.emitDomainAudit(request.Context(), request, admin.session, domainAuditInput{
			Category:      auditserver.AggregateTypeIAMAdmin,
			TargetID:      "principal:" + member.PrincipalID,
			TargetLabel:   targetLabel,
			Action:        "IAM_MEMBER_STATUS_UPDATED",
			Outcome:       auditOutcomeForStatus(iamStatus),
			ChangeSummary: map[string]any{"beforeEnabled": beforeEnabled, "afterEnabled": input.Enabled, "reasonCode": code},
		})
		return
	}
	if err := h.identity.config.UserDirectory.SetEnabled(request.Context(), admin.administrator(), member.Subject, input.Enabled); err != nil {
		h.emitDomainAudit(request.Context(), request, admin.session, domainAuditInput{
			Category:      auditserver.AggregateTypeIAMAdmin,
			TargetID:      "principal:" + member.PrincipalID,
			TargetLabel:   targetLabel,
			Action:        "IAM_MEMBER_STATUS_UPDATED",
			Outcome:       auditOutcomeFailed,
			ChangeSummary: map[string]any{"beforeEnabled": beforeEnabled, "afterEnabled": input.Enabled},
		})
		writeDirectoryFailure(writer, request, err)
		return
	}
	if !input.Enabled {
		if failure := h.revokePersonSessions(request, member); failure != nil {
			h.emitIAMAdminAudit(request, admin.session, "principal:"+member.PrincipalID, targetLabel, "IAM_MEMBER_STATUS_UPDATED", auditOutcomeFailed, map[string]any{"beforeEnabled": beforeEnabled, "afterEnabled": input.Enabled, "reasonCode": failure.code})
			writeIdentityFailure(writer, request, *failure)
			return
		}
	}
	h.emitDomainAudit(request.Context(), request, admin.session, domainAuditInput{
		Category:      auditserver.AggregateTypeIAMAdmin,
		TargetID:      "principal:" + member.PrincipalID,
		TargetLabel:   targetLabel,
		Action:        "IAM_MEMBER_STATUS_UPDATED",
		Outcome:       auditOutcomeSucceeded,
		ChangeSummary: map[string]any{"beforeEnabled": beforeEnabled, "afterEnabled": input.Enabled},
	})
	writer.WriteHeader(http.StatusNoContent)
}

// revokePersonSessions ends every signed-in Session of a disabled person, each with its own audit event.
func (h *handler) revokePersonSessions(request *http.Request, member iamserver.AdminMember) *identityFailure {
	unavailable := &identityFailure{503, "SESSION_STORE_UNAVAILABLE", "Session unavailable", "The user was disabled, but their signed-in sessions could not be ended.", true}
	ids, err := h.identity.store.ActiveSessionIDs(request.Context(), member.SubjectIssuer, member.Subject)
	if err != nil {
		return unavailable
	}
	for _, id := range ids {
		revoked, err := h.identity.store.RevokeSession(request.Context(), id, h.identity.mutationContext(request, "SESSION_REVOKED"))
		if err != nil && !errors.Is(err, sessionstore.ErrSessionRevoked) {
			return unavailable
		}
		if err == nil {
			h.revokeTelemetrySessionSubscriptions(request.Context(), revoked.TenantID, revoked.ID, "USER_DISABLED")
		}
	}
	return nil
}

func (h *handler) updateMemberProfile(writer http.ResponseWriter, request *http.Request, admin userAdminRequest, member iamserver.AdminMember) {
	var input struct {
		DisplayName string `json:"displayName"`
		Email       string `json:"email"`
	}
	if !decodeUserAdminBody(writer, request, &input) {
		h.emitIAMAdminAudit(request, admin.session, "principal:"+member.PrincipalID, firstNonEmpty(member.DisplayName, "组织成员"), "IAM_MEMBER_PROFILE_UPDATED", auditOutcomeRejected, map[string]any{"reasonCode": "USER_ADMIN_REQUEST_INVALID"})
		return
	}
	afterDisplayName := strings.TrimSpace(input.DisplayName)
	afterEmail := strings.TrimSpace(input.Email)
	targetLabel := firstNonEmpty(afterDisplayName, member.DisplayName, "组织成员")
	iamStatus, code, ok := h.callIAMAdminWithOutcome(writer, request, admin.session, internalIAMAdminMutationPath, iamserver.AdminMutationRequest{
		Operation: "principal.update", ResourceID: member.PrincipalID, DisplayName: afterDisplayName,
		Email: afterEmail, Status: member.PrincipalStatus, ExpectedRevision: member.PrincipalRevision,
	}, nil)
	if !ok {
		h.emitDomainAudit(request.Context(), request, admin.session, domainAuditInput{
			Category:      auditserver.AggregateTypeIAMAdmin,
			TargetID:      "principal:" + member.PrincipalID,
			TargetLabel:   targetLabel,
			Action:        "IAM_MEMBER_PROFILE_UPDATED",
			Outcome:       auditOutcomeForStatus(iamStatus),
			ChangeSummary: map[string]any{"beforeDisplayName": member.DisplayName, "afterDisplayName": afterDisplayName, "reasonCode": code},
		})
		return
	}
	if err := h.identity.config.UserDirectory.UpdateProfile(request.Context(), admin.administrator(), member.Subject, input.DisplayName, input.Email); err != nil {
		h.emitDomainAudit(request.Context(), request, admin.session, domainAuditInput{
			Category:      auditserver.AggregateTypeIAMAdmin,
			TargetID:      "principal:" + member.PrincipalID,
			TargetLabel:   targetLabel,
			Action:        "IAM_MEMBER_PROFILE_UPDATED",
			Outcome:       auditOutcomeFailed,
			ChangeSummary: map[string]any{"beforeDisplayName": member.DisplayName, "afterDisplayName": afterDisplayName},
		})
		writeDirectoryFailure(writer, request, err)
		return
	}
	h.emitDomainAudit(request.Context(), request, admin.session, domainAuditInput{
		Category:    auditserver.AggregateTypeIAMAdmin,
		TargetID:    "principal:" + member.PrincipalID,
		TargetLabel: targetLabel,
		Action:      "IAM_MEMBER_PROFILE_UPDATED",
		Outcome:     auditOutcomeSucceeded,
		ChangeSummary: map[string]any{
			"beforeDisplayName": member.DisplayName,
			"afterDisplayName":  afterDisplayName,
			"emailChanged":      afterEmail != member.Email,
		},
	})
	writer.WriteHeader(http.StatusNoContent)
}

func (h *handler) assignMemberRole(writer http.ResponseWriter, request *http.Request, admin userAdminRequest, member iamserver.AdminMember) {
	targetLabel := firstNonEmpty(member.DisplayName, "组织成员")
	var input assignmentInput
	if !decodeUserAdminBody(writer, request, &input) {
		h.emitIAMAdminAudit(request, admin.session, "principal:"+member.PrincipalID, targetLabel, "IAM_ROLE_ASSIGNED", auditOutcomeRejected, map[string]any{"reasonCode": "USER_ADMIN_REQUEST_INVALID"})
		return
	}
	role, ok := roleNamed(admin.directory, input.RoleKey)
	if !ok {
		h.emitIAMAdminAudit(request, admin.session, "principal:"+member.PrincipalID, targetLabel, "IAM_ROLE_ASSIGNED", auditOutcomeRejected, map[string]any{"roleKey": input.RoleKey, "reasonCode": "USER_ADMIN_REQUEST_INVALID"})
		writeIdentityFailure(writer, request, identityFailure{400, "USER_ADMIN_REQUEST_INVALID", "Role invalid", "The role is not valid.", false})
		return
	}
	// Re-assigning a revoked Role reactivates its row, which carries the revision to check.
	var expectedRevision int64
	for _, existing := range member.Assignments {
		if existing.RoleTemplateID == role.RoleTemplateID && existing.SiteID == input.SiteID {
			expectedRevision = existing.Revision
		}
	}
	iamStatus, code, ok := h.callIAMAdminWithOutcome(writer, request, admin.session, internalIAMAdminMutationPath, iamserver.AdminMutationRequest{
		Operation: "role-binding.upsert", PrincipalID: member.PrincipalID, RoleTemplateID: role.RoleTemplateID,
		SiteID: input.SiteID, Status: "ACTIVE", ExpectedRevision: expectedRevision,
	}, nil)
	if !ok {
		h.emitDomainAudit(request.Context(), request, admin.session, domainAuditInput{
			Category:      auditserver.AggregateTypeIAMAdmin,
			TargetID:      "principal:" + member.PrincipalID,
			TargetLabel:   targetLabel,
			Action:        "IAM_ROLE_ASSIGNED",
			Outcome:       auditOutcomeForStatus(iamStatus),
			ChangeSummary: map[string]any{"roleKey": input.RoleKey, "hasSiteScope": input.SiteID != "", "reasonCode": code},
		})
		return
	}
	h.emitDomainAudit(request.Context(), request, admin.session, domainAuditInput{
		Category:      auditserver.AggregateTypeIAMAdmin,
		TargetID:      "principal:" + member.PrincipalID,
		TargetLabel:   targetLabel,
		Action:        "IAM_ROLE_ASSIGNED",
		Outcome:       auditOutcomeSucceeded,
		ChangeSummary: map[string]any{"roleKey": input.RoleKey, "hasSiteScope": input.SiteID != ""},
	})
	writer.WriteHeader(http.StatusNoContent)
}

func (h *handler) revokeMemberRole(writer http.ResponseWriter, request *http.Request, admin userAdminRequest, member iamserver.AdminMember, bindingID string) {
	targetLabel := firstNonEmpty(member.DisplayName, "组织成员")
	for _, existing := range member.Assignments {
		if existing.BindingID == bindingID && existing.Status == "ACTIVE" {
			roleKey := roleKeyForTemplate(admin.directory, existing.RoleTemplateID)
			iamStatus, code, ok := h.callIAMAdminWithOutcome(writer, request, admin.session, internalIAMAdminMutationPath, iamserver.AdminMutationRequest{
				Operation: "role-binding.upsert", PrincipalID: member.PrincipalID, RoleTemplateID: existing.RoleTemplateID,
				SiteID: existing.SiteID, Status: "REVOKED", ExpectedRevision: existing.Revision,
			}, nil)
			if !ok {
				h.emitDomainAudit(request.Context(), request, admin.session, domainAuditInput{
					Category:      auditserver.AggregateTypeIAMAdmin,
					TargetID:      "principal:" + member.PrincipalID,
					TargetLabel:   targetLabel,
					Action:        "IAM_ROLE_REVOKED",
					Outcome:       auditOutcomeForStatus(iamStatus),
					ChangeSummary: map[string]any{"roleKey": roleKey, "hasSiteScope": existing.SiteID != "", "reasonCode": code},
				})
				return
			}
			h.emitDomainAudit(request.Context(), request, admin.session, domainAuditInput{
				Category:      auditserver.AggregateTypeIAMAdmin,
				TargetID:      "principal:" + member.PrincipalID,
				TargetLabel:   targetLabel,
				Action:        "IAM_ROLE_REVOKED",
				Outcome:       auditOutcomeSucceeded,
				ChangeSummary: map[string]any{"roleKey": roleKey, "hasSiteScope": existing.SiteID != ""},
			})
			writer.WriteHeader(http.StatusNoContent)
			return
		}
	}
	h.emitIAMAdminAudit(request, admin.session, "principal:"+member.PrincipalID, targetLabel, "IAM_ROLE_REVOKED", auditOutcomeRejected, map[string]any{"reasonCode": "RESOURCE_NOT_FOUND"})
	writeIdentityFailure(writer, request, identityFailure{404, "RESOURCE_NOT_FOUND", "Role not found", "This role is not assigned to the user.", false})
}

// OwnPassword lets any signed-in user change their own password.
func (h *handler) OwnPassword(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeMethodNotAllowedFor(writer, request, http.MethodPost)
		return
	}
	session, failure := h.identitySession(request)
	if failure == nil {
		failure = h.identity.validateStateChange(request, session, request.Header.Get("X-CSRF-Token"))
	}
	if failure == nil && h.identity.config.UserDirectory == nil {
		failure = &identityFailure{503, "USER_ADMIN_UNAVAILABLE", "Password change unavailable", "The identity directory is not configured for this Gateway.", true}
	}
	if failure == nil && session.Principal.Issuer != h.identity.config.OIDCIssuer {
		failure = &identityFailure{403, "PASSWORD_CHANGE_FORBIDDEN", "Password change unavailable", "This account's password is not managed by the platform.", false}
	}
	if failure != nil {
		if session.TenantID != "" {
			h.emitDomainAudit(request.Context(), request, session, domainAuditInput{
				Category:      auditserver.AggregateTypeIAMAdmin,
				TargetID:      "principal:" + session.Principal.Subject,
				TargetLabel:   firstNonEmpty(session.Principal.DisplayName, "当前账号"),
				Action:        "IAM_OWN_PASSWORD_CHANGED",
				Outcome:       auditOutcomeForStatus(failure.status),
				ChangeSummary: map[string]any{"passwordChanged": false, "reasonCode": failure.code},
			})
		}
		writeIdentityFailure(writer, request, *failure)
		return
	}
	var input struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	targetLabel := firstNonEmpty(session.Principal.DisplayName, "当前账号")
	if !decodeUserAdminBody(writer, request, &input) {
		h.emitIAMAdminAudit(request, session, "principal:"+session.Principal.Subject, targetLabel, "IAM_OWN_PASSWORD_CHANGED", auditOutcomeRejected, map[string]any{"passwordChanged": false, "reasonCode": "USER_ADMIN_REQUEST_INVALID"})
		return
	}
	err := h.identity.config.UserDirectory.ChangePassword(request.Context(), session.Principal.Subject, input.CurrentPassword, input.NewPassword)
	if errors.Is(err, identityadmin.ErrInvalidCredentials) {
		h.emitDomainAudit(request.Context(), request, session, domainAuditInput{
			Category:      auditserver.AggregateTypeIAMAdmin,
			TargetID:      "principal:" + session.Principal.Subject,
			TargetLabel:   targetLabel,
			Action:        "IAM_OWN_PASSWORD_CHANGED",
			Outcome:       auditOutcomeRejected,
			ChangeSummary: map[string]any{"passwordChanged": false, "reasonCode": "CURRENT_PASSWORD_INVALID"},
		})
		writeIdentityFailure(writer, request, identityFailure{400, "CURRENT_PASSWORD_INVALID", "Current password incorrect", "The current password is incorrect, or the account is locked for 15 minutes after repeated attempts.", false})
		return
	}
	if err != nil {
		h.emitDomainAudit(request.Context(), request, session, domainAuditInput{
			Category:      auditserver.AggregateTypeIAMAdmin,
			TargetID:      "principal:" + session.Principal.Subject,
			TargetLabel:   targetLabel,
			Action:        "IAM_OWN_PASSWORD_CHANGED",
			Outcome:       auditOutcomeRejected,
			ChangeSummary: map[string]any{"passwordChanged": false},
		})
		writeDirectoryFailure(writer, request, err)
		return
	}
	h.emitDomainAudit(request.Context(), request, session, domainAuditInput{
		Category:      auditserver.AggregateTypeIAMAdmin,
		TargetID:      "principal:" + session.Principal.Subject,
		TargetLabel:   targetLabel,
		Action:        "IAM_OWN_PASSWORD_CHANGED",
		Outcome:       auditOutcomeSucceeded,
		ChangeSummary: map[string]any{"passwordChanged": true},
	})
	writer.WriteHeader(http.StatusNoContent)
}

func roleNamed(directory iamserver.AdminDirectory, roleKey string) (iamserver.AdminRole, bool) {
	for _, role := range directory.Roles {
		if role.RoleKey == roleKey {
			return role, true
		}
	}
	return iamserver.AdminRole{}, false
}

func roleKeyForTemplate(directory iamserver.AdminDirectory, roleTemplateID string) string {
	for _, role := range directory.Roles {
		if role.RoleTemplateID == roleTemplateID {
			return role.RoleKey
		}
	}
	return ""
}

// callIAMAdminWithOutcome sends one IAM administration request as the acting administrator and
// decodes the reply into result when it is not nil, returning the public error status and code on refusal.
func (h *handler) callIAMAdminWithOutcome(writer http.ResponseWriter, request *http.Request, session bffSession, internalPath string, body any, result any) (int, string, bool) {
	var payload []byte
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	status, reply, failure := h.identity.forwardIAMAdmin(request.Context(), session, internalPath, payload)
	if failure != nil {
		writeIdentityFailure(writer, request, *failure)
		return failure.status, failure.code, false
	}
	if status != http.StatusOK {
		publicStatus, code := writeIAMProblem(writer, request, status, reply)
		return publicStatus, code, false
	}
	if result != nil {
		if err := json.Unmarshal(reply, result); err != nil {
			writeIdentityFailure(writer, request, identityFailure{503, "IAM_RESPONSE_INVALID", "IAM response invalid", "IAM returned an unreadable reply.", true})
			return http.StatusServiceUnavailable, "IAM_RESPONSE_INVALID", false
		}
	}
	return http.StatusOK, "", true
}

// writeIAMProblem re-issues an IAM refusal as a Gateway problem the browser understands.
func writeIAMProblem(writer http.ResponseWriter, request *http.Request, status int, body []byte) (int, string) {
	var problem struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(body, &problem)
	switch problem.Code {
	case "IAM_ADMIN_SELF_LOCKOUT":
		writeIdentityFailure(writer, request, identityFailure{409, problem.Code, "Change refused", "You cannot disable yourself or remove your own administrator role.", false})
		return http.StatusConflict, problem.Code
	case "IAM_ADMIN_REVISION_CONFLICT":
		writeIdentityFailure(writer, request, identityFailure{409, problem.Code, "Changed elsewhere", "This user was changed by someone else. Reload and try again.", false})
		return http.StatusConflict, problem.Code
	case "IAM_ADMIN_FORBIDDEN":
		writeIdentityFailure(writer, request, identityFailure{403, problem.Code, "User administration forbidden", "Only administrators can manage users.", false})
		return http.StatusForbidden, problem.Code
	case "IAM_ADMIN_REQUEST_REJECTED", "IAM_ADMIN_REQUEST_INVALID", "IAM_ADMIN_RESOURCE_NOT_FOUND":
		writeIdentityFailure(writer, request, identityFailure{400, problem.Code, "Request refused", "The change was not accepted.", false})
		return http.StatusBadRequest, problem.Code
	default:
		writeIdentityFailure(writer, request, identityFailure{503, "IAM_UNAVAILABLE", "IAM unavailable", "IAM could not complete the request.", status >= 500})
		return http.StatusServiceUnavailable, "IAM_UNAVAILABLE"
	}
}

func writeDirectoryFailure(writer http.ResponseWriter, request *http.Request, err error) {
	if rule, rejected := identityadmin.RejectedPasswordRule(err); rejected {
		writeIdentityFailure(writer, request, identityFailure{400, "PASSWORD_" + rule, "Password not accepted", err.Error(), false})
		return
	}
	switch {
	case errors.Is(err, identityadmin.ErrUsernameTaken):
		writeIdentityFailure(writer, request, identityFailure{409, "USERNAME_TAKEN", "Username taken", "This username is already in use.", false})
	case errors.Is(err, identityadmin.ErrProfileInvalid):
		writeIdentityFailure(writer, request, identityFailure{400, "USER_ADMIN_REQUEST_INVALID", "Request invalid", "The username, name or email is not valid.", false})
	case errors.Is(err, identityadmin.ErrUserNotFound):
		writeIdentityFailure(writer, request, identityFailure{404, "RESOURCE_NOT_FOUND", "User not found", "The account does not exist.", false})
	default:
		writeIdentityFailure(writer, request, identityFailure{503, "IDENTITY_DIRECTORY_UNAVAILABLE", "Identity directory unavailable", "The identity directory could not complete the request.", true})
	}
}

func decodeUserAdminBody(writer http.ResponseWriter, request *http.Request, target any) bool {
	request.Body = http.MaxBytesReader(writer, request.Body, maximumUserAdminBodySize)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil || ensureJSONEOF(decoder) != nil {
		writeIdentityFailure(writer, request, identityFailure{400, "USER_ADMIN_REQUEST_INVALID", "Request invalid", "The request body is invalid.", false})
		return false
	}
	return true
}

func (h *handler) emitIAMAdminAudit(
	request *http.Request,
	session bffSession,
	targetID, targetLabel, action, outcome string,
	summary map[string]any,
) {
	h.emitDomainAudit(request.Context(), request, session, domainAuditInput{
		Category:      auditserver.AggregateTypeIAMAdmin,
		TargetID:      targetID,
		TargetLabel:   targetLabel,
		Action:        action,
		Outcome:       outcome,
		ChangeSummary: summary,
	})
}

func iamAdminActionForPath(path string) string {
	parts := strings.Split(strings.TrimPrefix(path, publicMembersPrefix), "/")
	switch {
	case len(parts) == 2 && parts[1] == "password":
		return "IAM_MEMBER_PASSWORD_RESET"
	case len(parts) == 2 && parts[1] == "status":
		return "IAM_MEMBER_STATUS_UPDATED"
	case len(parts) == 2 && parts[1] == "profile":
		return "IAM_MEMBER_PROFILE_UPDATED"
	case len(parts) == 2 && parts[1] == "assignments":
		return "IAM_ROLE_ASSIGNED"
	case len(parts) == 4 && parts[1] == "assignments" && parts[3] == "revoke":
		return "IAM_ROLE_REVOKED"
	default:
		return "IAM_ADMIN_ACCESS"
	}
}
