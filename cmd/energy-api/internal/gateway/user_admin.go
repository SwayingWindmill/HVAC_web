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
	if !h.callIAMAdmin(writer, request, session, internalIAMMembersPath, nil, &directory) {
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
		return
	}
	if len(input.Assignments) == 0 {
		writeIdentityFailure(writer, request, identityFailure{400, "USER_ADMIN_REQUEST_INVALID", "Request invalid", "A new user needs at least one role.", false})
		return
	}
	assignments := make([]iamserver.AdminAssignment, len(input.Assignments))
	for index, input := range input.Assignments {
		role, ok := roleNamed(admin.directory, input.RoleKey)
		// IAM decides scope too, but by then the account would exist; refuse before creating it.
		if !ok || (role.TenantScoped && input.SiteID != "") {
			writeIdentityFailure(writer, request, identityFailure{400, "USER_ADMIN_REQUEST_INVALID", "Role invalid", "The role or its scope is not valid.", false})
			return
		}
		assignments[index] = iamserver.AdminAssignment{RoleTemplateID: role.RoleTemplateID, SiteID: input.SiteID}
	}
	account, err := h.identity.config.UserDirectory.CreateUser(request.Context(), identityadmin.NewUser{
		Username: input.Username, DisplayName: input.DisplayName, Email: input.Email, Password: input.Password,
	})
	if err != nil {
		writeDirectoryFailure(writer, request, err)
		return
	}
	var result iamserver.AdminMutationResult
	if !h.callIAMAdmin(writer, request, admin.session, internalIAMAdminMutationPath, iamserver.AdminMutationRequest{
		Operation: "member.create", SubjectIssuer: admin.session.Principal.Issuer, Subject: account.ID,
		DisplayName: account.DisplayName, Email: account.Email, Assignments: assignments,
	}, &result) {
		return
	}
	writeJSON(writer, http.StatusCreated, map[string]string{"principalId": result.ResourceID})
}

func (h *handler) resetMemberPassword(writer http.ResponseWriter, request *http.Request, admin userAdminRequest, member iamserver.AdminMember) {
	if member.PrincipalID == admin.directory.ActorPrincipalID {
		writeIdentityFailure(writer, request, identityFailure{409, "OWN_PASSWORD_REQUIRES_CURRENT", "Use password change", "Change your own password with your current password.", false})
		return
	}
	var input struct {
		Password string `json:"password"`
	}
	if !decodeUserAdminBody(writer, request, &input) {
		return
	}
	if err := h.identity.config.UserDirectory.SetPassword(request.Context(), admin.administrator(), member.Subject, input.Password); err != nil {
		writeDirectoryFailure(writer, request, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (h *handler) setMemberStatus(writer http.ResponseWriter, request *http.Request, admin userAdminRequest, member iamserver.AdminMember) {
	var input struct {
		Enabled bool `json:"enabled"`
	}
	if !decodeUserAdminBody(writer, request, &input) {
		return
	}
	status := "SUSPENDED"
	if input.Enabled {
		status = "ACTIVE"
	}
	if !h.callIAMAdmin(writer, request, admin.session, internalIAMAdminMutationPath, iamserver.AdminMutationRequest{
		Operation: "membership.upsert", PrincipalID: member.PrincipalID, Status: status, ExpectedRevision: member.MembershipRevision,
	}, nil) {
		return
	}
	if err := h.identity.config.UserDirectory.SetEnabled(request.Context(), admin.administrator(), member.Subject, input.Enabled); err != nil {
		writeDirectoryFailure(writer, request, err)
		return
	}
	if !input.Enabled {
		if failure := h.revokePersonSessions(request, member); failure != nil {
			writeIdentityFailure(writer, request, *failure)
			return
		}
	}
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
		_, err := h.identity.store.RevokeSession(request.Context(), id, h.identity.mutationContext(request, "SESSION_REVOKED"))
		if err != nil && !errors.Is(err, sessionstore.ErrSessionRevoked) {
			return unavailable
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
		return
	}
	if !h.callIAMAdmin(writer, request, admin.session, internalIAMAdminMutationPath, iamserver.AdminMutationRequest{
		Operation: "principal.update", ResourceID: member.PrincipalID, DisplayName: strings.TrimSpace(input.DisplayName),
		Email: strings.TrimSpace(input.Email), Status: member.PrincipalStatus, ExpectedRevision: member.PrincipalRevision,
	}, nil) {
		return
	}
	if err := h.identity.config.UserDirectory.UpdateProfile(request.Context(), admin.administrator(), member.Subject, input.DisplayName, input.Email); err != nil {
		writeDirectoryFailure(writer, request, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (h *handler) assignMemberRole(writer http.ResponseWriter, request *http.Request, admin userAdminRequest, member iamserver.AdminMember) {
	var input assignmentInput
	if !decodeUserAdminBody(writer, request, &input) {
		return
	}
	role, ok := roleNamed(admin.directory, input.RoleKey)
	if !ok {
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
	if h.callIAMAdmin(writer, request, admin.session, internalIAMAdminMutationPath, iamserver.AdminMutationRequest{
		Operation: "role-binding.upsert", PrincipalID: member.PrincipalID, RoleTemplateID: role.RoleTemplateID,
		SiteID: input.SiteID, Status: "ACTIVE", ExpectedRevision: expectedRevision,
	}, nil) {
		writer.WriteHeader(http.StatusNoContent)
	}
}

func (h *handler) revokeMemberRole(writer http.ResponseWriter, request *http.Request, admin userAdminRequest, member iamserver.AdminMember, bindingID string) {
	for _, existing := range member.Assignments {
		if existing.BindingID == bindingID && existing.Status == "ACTIVE" {
			if h.callIAMAdmin(writer, request, admin.session, internalIAMAdminMutationPath, iamserver.AdminMutationRequest{
				Operation: "role-binding.upsert", PrincipalID: member.PrincipalID, RoleTemplateID: existing.RoleTemplateID,
				SiteID: existing.SiteID, Status: "REVOKED", ExpectedRevision: existing.Revision,
			}, nil) {
				writer.WriteHeader(http.StatusNoContent)
			}
			return
		}
	}
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
		writeIdentityFailure(writer, request, *failure)
		return
	}
	var input struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if !decodeUserAdminBody(writer, request, &input) {
		return
	}
	err := h.identity.config.UserDirectory.ChangePassword(request.Context(), session.Principal.Subject, input.CurrentPassword, input.NewPassword)
	if errors.Is(err, identityadmin.ErrInvalidCredentials) {
		writeIdentityFailure(writer, request, identityFailure{400, "CURRENT_PASSWORD_INVALID", "Current password incorrect", "The current password is incorrect, or the account is locked for 15 minutes after repeated attempts.", false})
		return
	}
	if err != nil {
		writeDirectoryFailure(writer, request, err)
		return
	}
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

// callIAMAdmin sends one IAM administration request as the acting administrator and decodes the
// reply into result when it is not nil. It writes the failure and returns false when IAM refuses.
func (h *handler) callIAMAdmin(writer http.ResponseWriter, request *http.Request, session bffSession, internalPath string, body any, result any) bool {
	var payload []byte
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	status, reply, _, failure := h.identity.forwardIAMAdmin(request.Context(), session, iamAdminRoute{internalPath: internalPath, action: "iam:admin"}, payload)
	if failure != nil {
		writeIdentityFailure(writer, request, *failure)
		return false
	}
	if status != http.StatusOK {
		writeIAMProblem(writer, request, status, reply)
		return false
	}
	if result != nil {
		if err := json.Unmarshal(reply, result); err != nil {
			writeIdentityFailure(writer, request, identityFailure{503, "IAM_RESPONSE_INVALID", "IAM response invalid", "IAM returned an unreadable reply.", true})
			return false
		}
	}
	return true
}

// writeIAMProblem re-issues an IAM refusal as a Gateway problem the browser understands.
func writeIAMProblem(writer http.ResponseWriter, request *http.Request, status int, body []byte) {
	var problem struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(body, &problem)
	switch problem.Code {
	case "IAM_ADMIN_SELF_LOCKOUT":
		writeIdentityFailure(writer, request, identityFailure{409, problem.Code, "Change refused", "You cannot disable yourself or remove your own administrator role.", false})
	case "IAM_ADMIN_REVISION_CONFLICT":
		writeIdentityFailure(writer, request, identityFailure{409, problem.Code, "Changed elsewhere", "This user was changed by someone else. Reload and try again.", false})
	case "IAM_ADMIN_FORBIDDEN":
		writeIdentityFailure(writer, request, identityFailure{403, problem.Code, "User administration forbidden", "Only administrators can manage users.", false})
	case "IAM_ADMIN_REQUEST_REJECTED", "IAM_ADMIN_REQUEST_INVALID", "IAM_ADMIN_RESOURCE_NOT_FOUND":
		writeIdentityFailure(writer, request, identityFailure{400, problem.Code, "Request refused", "The change was not accepted.", false})
	default:
		writeIdentityFailure(writer, request, identityFailure{503, "IAM_UNAVAILABLE", "IAM unavailable", "IAM could not complete the request.", status >= 500})
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
