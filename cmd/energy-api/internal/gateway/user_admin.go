package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/sessionstore"
	"github.com/quanlaihe/hvac-web/services/identity-service/pkg/identityadmin"
)

// User administration (spec docs/specs/iam-roles-and-users.md) composes two owners: the identity
// directory holds accounts and passwords, IAM holds membership and Role Assignments. The Gateway
// checks iam.admin, keeps every account it touches inside the acting Tenant's members, and
// changes IAM first so a refused IAM change never leaves the account half-updated.
const (
	publicMembersPath        = "/api/v1/iam/members"
	publicMembersPrefix      = "/api/v1/iam/members/"
	publicOwnPasswordPath    = "/api/v1/me/password"
	internalIAMMembersPath   = "/internal/v1/admin/members"
	maximumUserAdminBodySize = 16 << 10
)

// UserDirectory is the identity directory as user administration uses it.
type UserDirectory interface {
	CreateUser(context.Context, identityadmin.NewUser) (identityadmin.User, error)
	UsersByID(context.Context, []string) ([]identityadmin.User, error)
	UserByUsername(context.Context, string) (identityadmin.User, error)
	SetPassword(context.Context, string, string) error
	SetEnabled(context.Context, string, bool) error
	UpdateProfile(context.Context, string, string, string) error
	ChangePassword(context.Context, string, string, string) error
}

type iamRole struct {
	RoleTemplateID string `json:"roleTemplateId"`
	RoleKey        string `json:"roleKey"`
	DisplayName    string `json:"displayName"`
	TenantScoped   bool   `json:"tenantScoped"`
}

type iamAssignment struct {
	BindingID      string `json:"bindingId"`
	RoleTemplateID string `json:"roleTemplateId"`
	RoleKey        string `json:"roleKey"`
	SiteID         string `json:"siteId,omitempty"`
	Status         string `json:"status"`
	Revision       int64  `json:"revision"`
}

type iamMember struct {
	PrincipalID        string          `json:"principalId"`
	SubjectIssuer      string          `json:"subjectIssuer"`
	Subject            string          `json:"subject"`
	DisplayName        string          `json:"displayName"`
	Email              string          `json:"email"`
	PrincipalRevision  int64           `json:"principalRevision"`
	MembershipStatus   string          `json:"membershipStatus"`
	MembershipRevision int64           `json:"membershipRevision"`
	Assignments        []iamAssignment `json:"assignments"`
}

type iamDirectory struct {
	Roles   []iamRole   `json:"roles"`
	Members []iamMember `json:"members"`
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
	DisplayName  string `json:"displayName"`
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

type userAdminContext struct {
	session bffSession
	actorID string
}

// UserAdmin serves 系统管理 → 用户与角色.
func (h *handler) UserAdmin(writer http.ResponseWriter, request *http.Request) {
	path := request.URL.Path
	if path == publicMembersPath && request.Method == http.MethodGet {
		h.listMembers(writer, request)
		return
	}
	if request.Method != http.MethodPost {
		writeMethodNotAllowedFor(writer, request, http.MethodPost)
		return
	}
	if path == publicMembersPath {
		h.createMember(writer, request)
		return
	}
	parts := strings.Split(strings.TrimPrefix(path, publicMembersPrefix), "/")
	switch {
	case len(parts) == 2 && parts[1] == "password":
		h.memberChange(writer, request, parts[0], h.resetMemberPassword)
	case len(parts) == 2 && parts[1] == "status":
		h.memberChange(writer, request, parts[0], h.setMemberStatus)
	case len(parts) == 2 && parts[1] == "profile":
		h.memberChange(writer, request, parts[0], h.updateMemberProfile)
	case len(parts) == 2 && parts[1] == "assignments":
		h.memberChange(writer, request, parts[0], h.assignMemberRole)
	case len(parts) == 4 && parts[1] == "assignments" && parts[3] == "revoke":
		bindingID := parts[2]
		h.memberChange(writer, request, parts[0], func(writer http.ResponseWriter, request *http.Request, admin userAdminContext, directory iamDirectory, member iamMember) {
			h.revokeMemberRole(writer, request, admin, directory, member, bindingID)
		})
	default:
		writeIdentityFailure(writer, request, identityFailure{404, "RESOURCE_NOT_FOUND", "Not found", "The user administration route does not exist.", false})
	}
}

func (h *handler) userAdminContext(writer http.ResponseWriter, request *http.Request) (userAdminContext, bool) {
	session, failure := h.identitySession(request)
	if failure == nil && request.Method != http.MethodGet {
		failure = h.identity.validateStateChange(request, session, request.Header.Get("X-CSRF-Token"))
	}
	if failure == nil && h.identity.config.UserDirectory == nil {
		failure = &identityFailure{503, "USER_ADMIN_UNAVAILABLE", "User administration unavailable", "The identity directory is not configured for this Gateway.", true}
	}
	var principal identitycontext.InternalPrincipalResponse
	if failure == nil {
		principal, failure = h.identity.fetchPrincipal(request.Context(), session)
	}
	if failure == nil && !principal.Authorization.Has(identitycontext.CapabilityIAMAdmin) {
		failure = &identityFailure{403, "IAM_ADMIN_FORBIDDEN", "User administration forbidden", "Only administrators can manage users.", false}
	}
	if failure != nil {
		writeIdentityFailure(writer, request, *failure)
		return userAdminContext{}, false
	}
	return userAdminContext{session: session, actorID: principal.PrincipalID}, true
}

func (h *handler) loadIAMDirectory(writer http.ResponseWriter, request *http.Request, admin userAdminContext) (iamDirectory, bool) {
	status, body, _, failure := h.identity.forwardIAMAdmin(request.Context(), admin.session, iamAdminRoute{internalPath: internalIAMMembersPath, action: "iam:admin"}, nil)
	if failure != nil {
		writeIdentityFailure(writer, request, *failure)
		return iamDirectory{}, false
	}
	if status != http.StatusOK {
		writeIAMProblem(writer, request, status, body)
		return iamDirectory{}, false
	}
	var directory iamDirectory
	if err := json.Unmarshal(body, &directory); err != nil {
		writeIdentityFailure(writer, request, identityFailure{503, "IAM_RESPONSE_INVALID", "IAM response invalid", "IAM returned an unreadable member list.", true})
		return iamDirectory{}, false
	}
	// Only people who sign in through this platform's identity directory are users; Workload
	// Principals are members too but are not managed here.
	people := directory.Members[:0]
	for _, member := range directory.Members {
		if member.SubjectIssuer == admin.session.Principal.Issuer {
			people = append(people, member)
		}
	}
	directory.Members = people
	return directory, true
}

func (h *handler) listMembers(writer http.ResponseWriter, request *http.Request) {
	admin, ok := h.userAdminContext(writer, request)
	if !ok {
		return
	}
	directory, ok := h.loadIAMDirectory(writer, request, admin)
	if !ok {
		return
	}
	subjects := make([]string, len(directory.Members))
	for index, member := range directory.Members {
		subjects[index] = member.Subject
	}
	accounts, err := h.identity.config.UserDirectory.UsersByID(request.Context(), subjects)
	if err != nil {
		writeIdentityFailure(writer, request, identityFailure{503, "IDENTITY_DIRECTORY_UNAVAILABLE", "Identity directory unavailable", "The identity directory could not be read.", true})
		return
	}
	byID := make(map[string]identityadmin.User, len(accounts))
	for _, account := range accounts {
		byID[account.ID] = account
	}
	now := h.now()
	response := membersResponse{Roles: []roleView{}, Members: []memberView{}}
	for _, role := range directory.Roles {
		response.Roles = append(response.Roles, roleView{RoleKey: role.RoleKey, DisplayName: role.DisplayName, TenantScoped: role.TenantScoped})
	}
	for _, member := range directory.Members {
		account, found := byID[member.Subject]
		if !found {
			continue
		}
		view := memberView{
			PrincipalID: member.PrincipalID, Username: account.Username, DisplayName: account.DisplayName, Email: account.Email,
			Enabled:     member.MembershipStatus == "ACTIVE" && account.Status == "ACTIVE",
			Locked:      account.LockedUntil != nil && account.LockedUntil.After(now),
			Self:        member.PrincipalID == admin.actorID,
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

func (h *handler) createMember(writer http.ResponseWriter, request *http.Request) {
	admin, ok := h.userAdminContext(writer, request)
	if !ok {
		return
	}
	var input createMemberRequest
	if !decodeUserAdminBody(writer, request, &input) {
		return
	}
	directory, ok := h.loadIAMDirectory(writer, request, admin)
	if !ok {
		return
	}
	assignments, ok := resolveAssignments(writer, request, directory, input.Assignments)
	if !ok {
		return
	}
	if len(assignments) == 0 {
		writeIdentityFailure(writer, request, identityFailure{400, "USER_ADMIN_REQUEST_INVALID", "Request invalid", "A new user needs at least one role.", false})
		return
	}
	users := h.identity.config.UserDirectory
	account, err := users.CreateUser(request.Context(), identityadmin.NewUser{Username: input.Username, DisplayName: input.DisplayName, Email: input.Email, Password: input.Password})
	if errors.Is(err, identityadmin.ErrUsernameTaken) {
		// A username without a membership here is left over from an attempt IAM refused; take it over.
		existing, lookupErr := users.UserByUsername(request.Context(), input.Username)
		if lookupErr != nil {
			writeDirectoryFailure(writer, request, lookupErr)
			return
		}
		for _, member := range directory.Members {
			if member.Subject == existing.ID {
				writeIdentityFailure(writer, request, identityFailure{409, "USERNAME_TAKEN", "Username taken", "This username is already in use.", false})
				return
			}
		}
		if err = users.SetPassword(request.Context(), existing.ID, input.Password); err == nil {
			err = users.UpdateProfile(request.Context(), existing.ID, input.DisplayName, input.Email)
		}
		account = existing
	}
	if err != nil {
		writeDirectoryFailure(writer, request, err)
		return
	}
	result, ok := h.applyIAMMutation(writer, request, admin, map[string]any{
		"operation": "member.create", "subjectIssuer": admin.session.Principal.Issuer, "subject": account.ID,
		"displayName": strings.TrimSpace(input.DisplayName), "email": strings.TrimSpace(input.Email), "assignments": assignments,
	})
	if !ok {
		return
	}
	writeJSON(writer, http.StatusCreated, map[string]string{"principalId": result.ResourceID})
}

type memberChangeFunc func(http.ResponseWriter, *http.Request, userAdminContext, iamDirectory, iamMember)

// memberChange resolves the target as a member of the acting Tenant before any change.
func (h *handler) memberChange(writer http.ResponseWriter, request *http.Request, principalID string, change memberChangeFunc) {
	admin, ok := h.userAdminContext(writer, request)
	if !ok {
		return
	}
	directory, ok := h.loadIAMDirectory(writer, request, admin)
	if !ok {
		return
	}
	for _, member := range directory.Members {
		if member.PrincipalID == principalID {
			change(writer, request, admin, directory, member)
			return
		}
	}
	writeIdentityFailure(writer, request, identityFailure{404, "RESOURCE_NOT_FOUND", "User not found", "This user is not a member of the current organization.", false})
}

func (h *handler) resetMemberPassword(writer http.ResponseWriter, request *http.Request, _ userAdminContext, _ iamDirectory, member iamMember) {
	var input struct {
		Password string `json:"password"`
	}
	if !decodeUserAdminBody(writer, request, &input) {
		return
	}
	if err := h.identity.config.UserDirectory.SetPassword(request.Context(), member.Subject, input.Password); err != nil {
		writeDirectoryFailure(writer, request, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (h *handler) setMemberStatus(writer http.ResponseWriter, request *http.Request, admin userAdminContext, _ iamDirectory, member iamMember) {
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
	if _, ok := h.applyIAMMutation(writer, request, admin, map[string]any{
		"operation": "membership.upsert", "principalId": member.PrincipalID, "status": status, "expectedRevision": member.MembershipRevision,
	}); !ok {
		return
	}
	if err := h.identity.config.UserDirectory.SetEnabled(request.Context(), member.Subject, input.Enabled); err != nil {
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
func (h *handler) revokePersonSessions(request *http.Request, member iamMember) *identityFailure {
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

func (h *handler) updateMemberProfile(writer http.ResponseWriter, request *http.Request, admin userAdminContext, _ iamDirectory, member iamMember) {
	var input struct {
		DisplayName string `json:"displayName"`
		Email       string `json:"email"`
	}
	if !decodeUserAdminBody(writer, request, &input) {
		return
	}
	if _, ok := h.applyIAMMutation(writer, request, admin, map[string]any{
		"operation": "principal.update", "resourceId": member.PrincipalID, "displayName": strings.TrimSpace(input.DisplayName),
		"email": strings.TrimSpace(input.Email), "status": "ACTIVE", "expectedRevision": member.PrincipalRevision,
	}); !ok {
		return
	}
	if err := h.identity.config.UserDirectory.UpdateProfile(request.Context(), member.Subject, input.DisplayName, input.Email); err != nil {
		writeDirectoryFailure(writer, request, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (h *handler) assignMemberRole(writer http.ResponseWriter, request *http.Request, admin userAdminContext, directory iamDirectory, member iamMember) {
	var input assignmentInput
	if !decodeUserAdminBody(writer, request, &input) {
		return
	}
	assignments, ok := resolveAssignments(writer, request, directory, []assignmentInput{input})
	if !ok {
		return
	}
	target := assignments[0]
	// Re-assigning a revoked Role reactivates its row, which carries the revision to check.
	var expectedRevision int64
	for _, existing := range member.Assignments {
		if existing.RoleTemplateID == target["roleTemplateId"] && existing.SiteID == target["siteId"] {
			expectedRevision = existing.Revision
		}
	}
	h.writeIAMMutation(writer, request, admin, map[string]any{
		"operation": "role-binding.upsert", "principalId": member.PrincipalID, "roleTemplateId": target["roleTemplateId"],
		"siteId": target["siteId"], "status": "ACTIVE", "expectedRevision": expectedRevision,
	})
}

func (h *handler) revokeMemberRole(writer http.ResponseWriter, request *http.Request, admin userAdminContext, _ iamDirectory, member iamMember, bindingID string) {
	for _, existing := range member.Assignments {
		if existing.BindingID == bindingID {
			h.writeIAMMutation(writer, request, admin, map[string]any{
				"operation": "role-binding.upsert", "principalId": member.PrincipalID, "roleTemplateId": existing.RoleTemplateID,
				"siteId": existing.SiteID, "status": "REVOKED", "expectedRevision": existing.Revision,
			})
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

// resolveAssignments turns built-in Role keys into IAM assignments. The admin Role, and any Role
// with Tenant-scoped capabilities, is assigned only to the whole Tenant.
func resolveAssignments(writer http.ResponseWriter, request *http.Request, directory iamDirectory, inputs []assignmentInput) ([]map[string]string, bool) {
	assignments := []map[string]string{}
	for _, input := range inputs {
		var role *iamRole
		for index := range directory.Roles {
			if directory.Roles[index].RoleKey == input.RoleKey {
				role = &directory.Roles[index]
			}
		}
		if role == nil || (role.TenantScoped && input.SiteID != "") || (input.SiteID != "" && !isLowerUUIDv7(input.SiteID)) {
			writeIdentityFailure(writer, request, identityFailure{400, "USER_ADMIN_REQUEST_INVALID", "Role invalid", "The role or its scope is not valid.", false})
			return nil, false
		}
		assignments = append(assignments, map[string]string{"roleTemplateId": role.RoleTemplateID, "siteId": input.SiteID})
	}
	return assignments, true
}

type iamMutationResult struct {
	ResourceID string `json:"resourceId"`
}

func (h *handler) applyIAMMutation(writer http.ResponseWriter, request *http.Request, admin userAdminContext, mutation map[string]any) (iamMutationResult, bool) {
	payload, _ := json.Marshal(mutation)
	status, body, _, failure := h.identity.forwardIAMAdmin(request.Context(), admin.session, iamAdminRoute{internalPath: internalIAMAdminMutationPath, action: "iam:admin"}, payload)
	if failure != nil {
		writeIdentityFailure(writer, request, *failure)
		return iamMutationResult{}, false
	}
	if status != http.StatusOK {
		writeIAMProblem(writer, request, status, body)
		return iamMutationResult{}, false
	}
	var result iamMutationResult
	_ = json.Unmarshal(body, &result)
	return result, true
}

func (h *handler) writeIAMMutation(writer http.ResponseWriter, request *http.Request, admin userAdminContext, mutation map[string]any) {
	if _, ok := h.applyIAMMutation(writer, request, admin, mutation); ok {
		writer.WriteHeader(http.StatusNoContent)
	}
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
		writeIdentityFailure(writer, request, identityFailure{503, "IAM_UNAVAILABLE", "IAM unavailable", "IAM could not complete the change.", status >= 500})
	}
}

func writeDirectoryFailure(writer http.ResponseWriter, request *http.Request, err error) {
	switch {
	case identityadmin.PasswordRejected(err):
		code := map[error]string{
			identityadmin.ErrPasswordTooShort: "PASSWORD_TOO_SHORT", identityadmin.ErrPasswordTooLong: "PASSWORD_TOO_LONG",
			identityadmin.ErrPasswordCommon: "PASSWORD_TOO_COMMON", identityadmin.ErrPasswordMatchesUsername: "PASSWORD_MATCHES_USERNAME",
		}
		for rule, value := range code {
			if errors.Is(err, rule) {
				writeIdentityFailure(writer, request, identityFailure{400, value, "Password not accepted", err.Error(), false})
				return
			}
		}
	case errors.Is(err, identityadmin.ErrUsernameTaken):
		writeIdentityFailure(writer, request, identityFailure{409, "USERNAME_TAKEN", "Username taken", "This username is already in use.", false})
	case errors.Is(err, identityadmin.ErrUserNotFound):
		writeIdentityFailure(writer, request, identityFailure{404, "RESOURCE_NOT_FOUND", "User not found", "The account does not exist.", false})
	default:
		if strings.Contains(err.Error(), "invalid") || strings.Contains(err.Error(), "required") {
			writeIdentityFailure(writer, request, identityFailure{400, "USER_ADMIN_REQUEST_INVALID", "Request invalid", err.Error(), false})
			return
		}
		writeIdentityFailure(writer, request, identityFailure{503, "IDENTITY_DIRECTORY_UNAVAILABLE", "Identity directory unavailable", "The identity directory could not complete the change.", true})
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
