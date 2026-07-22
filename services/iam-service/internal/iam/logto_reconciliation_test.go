package iam

import (
	"context"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

const testLogtoIssuer = "https://identity.example.test/oidc"
const testPlatformPrincipalID = "018f1e00-2000-7000-8000-000000000099"

func TestBuildLogtoReconciliationRequestRequiresExplicitMappings(t *testing.T) {
	effectiveAt := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	request, err := BuildLogtoReconciliationRequest(LogtoUser{
		ID: "user-123", PrimaryEmail: "provider@example.test", Name: "Provider Name",
	}, testLogtoIssuer, []LogtoOrganization{
		{
			ID: "approved-logto-org",
			OrganizationRoles: []LogtoOrganizationRole{
				{ID: "approved-role", Name: "reader"},
				{ID: "unapproved-role", Name: "admin"},
			},
		},
		{ID: "unapproved-logto-org", OrganizationRoles: []LogtoOrganizationRole{{ID: "other-role", Name: "owner"}}},
	}, LogtoReconciliationSeed{
		SourceVersion: 4,
		PrincipalID:   testPlatformPrincipalID,
		EffectiveAt:   effectiveAt,
		OrganizationMappings: []ApprovedLogtoOrganizationMapping{{
			LogtoOrganizationID:    "approved-logto-org",
			PlatformOrganizationID: "018f1e00-0000-7000-8000-000000000003",
			Roles: []ApprovedLogtoRoleMapping{{
				LogtoRoleID: "approved-role",
				RoleKey:     "registry-reader",
				Actions:     []registryauth.Action{registryauth.ActionSiteRead},
				Effect:      BindingEffectAllow,
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if request.SourceSystem != "logto" || request.SourceKey != "user-123" || request.SourceVersion != 4 {
		t.Fatalf("unexpected source identity: %#v", request)
	}
	if request.Principal.SubjectIssuer != testLogtoIssuer || request.Principal.Subject != "user-123" {
		t.Fatalf("provider subject was not projected immutably: %#v", request.Principal)
	}
	if request.Principal.Email != "provider@example.test" || request.Principal.DisplayName != "Provider Name" {
		t.Fatalf("provider mutable profile was not projected: %#v", request.Principal)
	}
	if len(request.Memberships) != 1 || request.Memberships[0].OrganizationID != "018f1e00-0000-7000-8000-000000000003" {
		t.Fatalf("unapproved organization was projected: %#v", request.Memberships)
	}
	if len(request.RoleBindings) != 1 || request.RoleBindings[0].RoleKey != "registry-reader" {
		t.Fatalf("unapproved role was projected: %#v", request.RoleBindings)
	}
}

func TestBuildLogtoReconciliationRequestProjectsSuspensionAndDeparture(t *testing.T) {
	request, err := BuildLogtoReconciliationRequest(LogtoUser{
		ID: "user-123", PrimaryEmail: "provider@example.test", Username: "provider-user", IsSuspended: true,
	}, testLogtoIssuer, nil, LogtoReconciliationSeed{
		SourceVersion: 5,
		PrincipalID:   testPlatformPrincipalID,
		EffectiveAt:   time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC),
		OrganizationMappings: []ApprovedLogtoOrganizationMapping{{
			LogtoOrganizationID:    "approved-logto-org",
			PlatformOrganizationID: "018f1e00-0000-7000-8000-000000000003",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Memberships) != 0 || len(request.RoleBindings) != 0 {
		t.Fatalf("departed Logto membership remained active: %#v", request)
	}
	if request.Principal.Status != PrincipalStatusDisabled || request.Principal.DisplayName != "provider-user" {
		t.Fatalf("provider suspension/profile fallback was not projected: %#v", request.Principal)
	}
}

func TestBuildLogtoReconciliationRequestRejectsMissingProviderEmail(t *testing.T) {
	_, err := BuildLogtoReconciliationRequest(LogtoUser{ID: "user-123"}, testLogtoIssuer, nil, LogtoReconciliationSeed{
		SourceVersion: 1,
		PrincipalID:   testPlatformPrincipalID,
		EffectiveAt:   time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC),
	})
	if err == nil {
		t.Fatal("Logto user without primary email was accepted")
	}
}

type staticLogtoManagementReader struct {
	user          LogtoUser
	organizations []LogtoOrganization
	called        *bool
}

func (reader staticLogtoManagementReader) User(context.Context, string) (LogtoUser, error) {
	if reader.called != nil {
		*reader.called = true
	}
	return reader.user, nil
}

func (reader staticLogtoManagementReader) UserOrganizations(context.Context, string) ([]LogtoOrganization, error) {
	if reader.called != nil {
		*reader.called = true
	}
	return reader.organizations, nil
}

type capturingReconciliationStore struct {
	request ReconciliationRequest
}

func (store *capturingReconciliationStore) Reconcile(_ context.Context, request ReconciliationRequest) (ReconciliationResult, error) {
	store.request = request
	return ReconciliationResult{Status: ReconciliationApplied, PrincipalID: request.Principal.ID}, nil
}

func TestLogtoReconcilerValidatesBeforeCallingManagementAPI(t *testing.T) {
	called := false
	store := &capturingReconciliationStore{}
	reconciler, err := NewLogtoReconciler(staticLogtoManagementReader{called: &called}, store, testLogtoIssuer)
	if err != nil {
		t.Fatal(err)
	}
	_, err = reconciler.ReconcileUser(context.Background(), "user-123", LogtoReconciliationSeed{
		SourceVersion: 1,
		EffectiveAt:   time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC),
	})
	if err == nil {
		t.Fatal("invalid seed was accepted")
	}
	if called {
		t.Fatal("management API was called before seed validation")
	}
}

func TestLogtoReconcilerReadsUserAndOrganizationsThenAppliesApprovedProjection(t *testing.T) {
	store := &capturingReconciliationStore{}
	reconciler, err := NewLogtoReconciler(staticLogtoManagementReader{
		user: LogtoUser{ID: "user-123", PrimaryEmail: "provider@example.test", Name: "Provider Name"},
		organizations: []LogtoOrganization{{
			ID:                "approved-logto-org",
			OrganizationRoles: []LogtoOrganizationRole{{ID: "approved-role", Name: "reader"}},
		}},
	}, store, testLogtoIssuer)
	if err != nil {
		t.Fatal(err)
	}
	result, err := reconciler.ReconcileUser(context.Background(), "user-123", LogtoReconciliationSeed{
		SourceVersion: 6,
		PrincipalID:   testPlatformPrincipalID,
		EffectiveAt:   time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC),
		OrganizationMappings: []ApprovedLogtoOrganizationMapping{{
			LogtoOrganizationID:    "approved-logto-org",
			PlatformOrganizationID: "018f1e00-0000-7000-8000-000000000003",
			Roles: []ApprovedLogtoRoleMapping{{
				LogtoRoleID: "approved-role", RoleKey: "registry-reader",
				Actions: []registryauth.Action{registryauth.ActionSiteRead}, Effect: BindingEffectAllow,
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != ReconciliationApplied || len(store.request.Memberships) != 1 || len(store.request.RoleBindings) != 1 {
		t.Fatalf("unexpected coordinated reconciliation: %#v %#v", result, store.request)
	}
	if store.request.Principal.Email != "provider@example.test" || store.request.Principal.SubjectIssuer != testLogtoIssuer {
		t.Fatalf("provider user profile was not applied: %#v", store.request.Principal)
	}
}
