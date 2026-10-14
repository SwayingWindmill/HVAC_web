package gateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/quanlaihe/hvac-web/cmd/energy-api/internal/gateway"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/modules/iam/pkg/iamserver"
	"github.com/quanlaihe/hvac-web/services/identity-service/pkg/identityadmin"
)

const (
	userAdminOperatorRoleID = "01a00260-0000-7000-8000-000000000002"
	userAdminAdminRoleID    = "01a00260-0000-7000-8000-000000000004"
	userAdminMemberID       = "018f3e00-2000-7000-8000-000000000010"
	userAdminSiteID         = "018f3e00-1000-7000-8000-000000000001"
)

// adminCapabilityResolver makes fixture-admin an administrator and everyone else a reader.
type adminCapabilityResolver struct{}

func (adminCapabilityResolver) ResolvePrincipalCapabilities(_ context.Context, lookup iamserver.PrincipalCapabilityLookup) (identitycontext.EffectiveAuthorization, error) {
	capabilities := []identitycontext.Capability{identitycontext.CapabilitySiteRead}
	if lookup.Subject == "fixture-admin" {
		capabilities = append(capabilities, identitycontext.CapabilityIAMAdmin)
	}
	return identitycontext.EffectiveAuthorization{CapabilitySetVersion: identitycontext.CapabilitySetVersion, PolicyRevision: "registry-read:test", Capabilities: capabilities}, nil
}

// fakeIAMAdmin answers IAM's member list and records the mutations the Gateway sends.
type fakeIAMAdmin struct {
	mu        sync.Mutex
	issuer    string
	mutations []map[string]any
}

func (fake *fakeIAMAdmin) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/internal/v1/admin/members":
			fake.mu.Lock()
			issuer := fake.issuer
			fake.mu.Unlock()
			writer.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"roles": []map[string]any{
					{"roleTemplateId": userAdminOperatorRoleID, "roleKey": "operator", "displayName": "运维人员", "tenantScoped": false},
					{"roleTemplateId": userAdminAdminRoleID, "roleKey": "admin", "displayName": "管理员", "tenantScoped": true},
				},
				"members": []map[string]any{{
					"principalId": userAdminMemberID, "subjectIssuer": issuer, "subject": "fixture-user", "displayName": "Fixture User",
					"email": "fixture@example.test", "principalRevision": 1, "membershipStatus": "ACTIVE", "membershipRevision": 3, "assignments": []any{},
				}},
			})
		case "/internal/v1/admin/mutations":
			var mutation map[string]any
			_ = json.NewDecoder(request.Body).Decode(&mutation)
			fake.mu.Lock()
			fake.mutations = append(fake.mutations, mutation)
			fake.mu.Unlock()
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"resourceId":"018f3e00-2000-7000-8000-000000000011","revision":1,"policyRevision":"iam:2","auditEventId":"x"}`))
		default:
			next.ServeHTTP(writer, request)
		}
	})
}

func (fake *fakeIAMAdmin) lastMutation(t *testing.T) map[string]any {
	t.Helper()
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.mutations) == 0 {
		t.Fatal("the Gateway sent no IAM mutation")
	}
	return fake.mutations[len(fake.mutations)-1]
}

type fakeUserDirectory struct {
	mu        sync.Mutex
	createErr error
	created   []identityadmin.NewUser
	enabled   map[string]bool
}

func (directory *fakeUserDirectory) CreateUser(_ context.Context, user identityadmin.NewUser) (identityadmin.User, error) {
	directory.mu.Lock()
	defer directory.mu.Unlock()
	if directory.createErr != nil {
		return identityadmin.User{}, directory.createErr
	}
	directory.created = append(directory.created, user)
	return identityadmin.User{ID: "user-new", Username: user.Username, DisplayName: user.DisplayName, Email: user.Email, Status: "ACTIVE"}, nil
}

func (directory *fakeUserDirectory) UsersByID(_ context.Context, ids []string) ([]identityadmin.User, error) {
	users := []identityadmin.User{}
	for _, id := range ids {
		users = append(users, identityadmin.User{ID: id, Username: "fixture", DisplayName: "Fixture User", Email: "fixture@example.test", Status: "ACTIVE"})
	}
	return users, nil
}

func (directory *fakeUserDirectory) UserByUsername(context.Context, string) (identityadmin.User, error) {
	return identityadmin.User{}, identityadmin.ErrUserNotFound
}

func (directory *fakeUserDirectory) SetPassword(context.Context, string, string) error { return nil }

func (directory *fakeUserDirectory) SetEnabled(_ context.Context, userID string, enabled bool) error {
	directory.mu.Lock()
	defer directory.mu.Unlock()
	directory.enabled[userID] = enabled
	return nil
}

func (directory *fakeUserDirectory) UpdateProfile(context.Context, string, string, string) error {
	return nil
}

func (directory *fakeUserDirectory) ChangePassword(context.Context, string, string, string) error {
	return identityadmin.ErrInvalidCredentials
}

func newUserAdminHarness(t *testing.T) (*authHarness, *fakeIAMAdmin, *fakeUserDirectory) {
	t.Helper()
	iam := &fakeIAMAdmin{}
	directory := &fakeUserDirectory{enabled: map[string]bool{}}
	harness := newAuthHarnessWithIAMFactory(t, func(clientSPIFFEID string) http.Handler {
		return iam.wrap(iamserver.NewHandler(iamserver.Config{
			AllowedWorkloadSPIFFE: clientSPIFFEID, Audience: "iam-service", PrincipalCapabilityResolver: adminCapabilityResolver{},
		}))
	}, func(config *gateway.IdentityConfig) { config.UserDirectory = directory })
	iam.issuer = harness.oidcServer.URL
	return harness, iam, directory
}

func postAsUser(t *testing.T, client *http.Client, harness *authHarness, csrfToken, path, body string) *http.Response {
	t.Helper()
	request, _ := http.NewRequest(http.MethodPost, harness.gatewayURL+path, bytes.NewReader([]byte(body)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", harness.gatewayURL)
	request.Header.Set("X-CSRF-Token", csrfToken)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

func TestUserAdministrationCreatesTheAccountThenItsMembership(t *testing.T) {
	harness, iam, directory := newUserAdminHarness(t)
	admin := harness.browserClient(t)
	principal, _ := loginAndReadPrincipal(t, admin, harness.gatewayURL, "admin")

	response := postAsUser(t, admin, harness, principal.Session.CSRFToken, "/api/v1/iam/members",
		`{"username":"zhang.wei","displayName":"张伟","email":"zhang.wei@example.test","password":"chiller-night-shift","assignments":[{"roleKey":"operator","siteId":"`+userAdminSiteID+`"}]}`)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", response.StatusCode)
	}
	if len(directory.created) != 1 || directory.created[0].Username != "zhang.wei" {
		t.Fatalf("accounts created = %#v", directory.created)
	}
	mutation := iam.lastMutation(t)
	assignments, _ := mutation["assignments"].([]any)
	first, _ := assignments[0].(map[string]any)
	if mutation["operation"] != "member.create" || mutation["subject"] != "user-new" || mutation["subjectIssuer"] != harness.oidcServer.URL ||
		len(assignments) != 1 || first["roleTemplateId"] != userAdminOperatorRoleID || first["siteId"] != userAdminSiteID {
		t.Fatalf("IAM mutation = %#v", mutation)
	}

	// The admin Role is assigned only to the whole Tenant.
	siteAdmin := postAsUser(t, admin, harness, principal.Session.CSRFToken, "/api/v1/iam/members",
		`{"username":"li.na","displayName":"李娜","email":"li.na@example.test","password":"chiller-night-shift","assignments":[{"roleKey":"admin","siteId":"`+userAdminSiteID+`"}]}`)
	assertProblemCode(t, siteAdmin, http.StatusBadRequest, "USER_ADMIN_REQUEST_INVALID")

	directory.createErr = identityadmin.ErrPasswordCommon
	common := postAsUser(t, admin, harness, principal.Session.CSRFToken, "/api/v1/iam/members",
		`{"username":"wang.fang","displayName":"王芳","email":"wang.fang@example.test","password":"password1","assignments":[{"roleKey":"operator"}]}`)
	assertProblemCode(t, common, http.StatusBadRequest, "PASSWORD_TOO_COMMON")
}

func TestUserAdministrationIsForAdministratorsOnly(t *testing.T) {
	harness, _, _ := newUserAdminHarness(t)
	user := harness.browserClient(t)
	loginAndReadPrincipal(t, user, harness.gatewayURL, "")
	response, err := user.Get(harness.gatewayURL + "/api/v1/iam/members")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	assertProblemCode(t, response, http.StatusForbidden, "IAM_ADMIN_FORBIDDEN")
}

func TestDisablingAUserEndsTheirSignedInSessions(t *testing.T) {
	harness, iam, directory := newUserAdminHarness(t)
	user := harness.browserClient(t)
	loginAndReadPrincipal(t, user, harness.gatewayURL, "")
	admin := harness.browserClient(t)
	principal, _ := loginAndReadPrincipal(t, admin, harness.gatewayURL, "admin")

	response := postAsUser(t, admin, harness, principal.Session.CSRFToken, "/api/v1/iam/members/"+userAdminMemberID+"/status", `{"enabled":false}`)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d", response.StatusCode)
	}
	mutation := iam.lastMutation(t)
	if mutation["operation"] != "membership.upsert" || mutation["status"] != "SUSPENDED" || mutation["expectedRevision"] != float64(3) {
		t.Fatalf("IAM mutation = %#v", mutation)
	}
	if enabled, set := directory.enabled["fixture-user"]; !set || enabled {
		t.Fatalf("account sign-in = %#v", directory.enabled)
	}
	after, err := user.Get(harness.gatewayURL + "/api/v1/principal")
	if err != nil {
		t.Fatal(err)
	}
	defer after.Body.Close()
	assertProblemCode(t, after, http.StatusUnauthorized, "SESSION_INVALID")
	if admin, err := admin.Get(harness.gatewayURL + "/api/v1/principal"); err != nil || admin.StatusCode != http.StatusOK {
		t.Fatalf("the administrator's own session ended: %v %v", admin, err)
	}
}

func TestOwnPasswordChangeReportsAWrongCurrentPassword(t *testing.T) {
	harness, _, _ := newUserAdminHarness(t)
	user := harness.browserClient(t)
	principal, _ := loginAndReadPrincipal(t, user, harness.gatewayURL, "")
	response := postAsUser(t, user, harness, principal.Session.CSRFToken, "/api/v1/me/password", `{"currentPassword":"not-it","newPassword":"chiller-night-shift"}`)
	assertProblemCode(t, response, http.StatusBadRequest, "CURRENT_PASSWORD_INVALID")
	if strings.Contains(harness.logs.String(), "chiller-night-shift") {
		t.Fatal("a password reached the Gateway logs")
	}
}
