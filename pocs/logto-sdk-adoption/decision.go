package logtopoc

func buildReport(checks SDKChecks) Report {
	return Report{
		SchemaVersion: 1,
		SDK: SDK{
			Repository: sdkRepository,
			Version:    sdkVersion,
			License:    "MIT",
			DependencyLicenses: []string{
				"github.com/go-jose/go-jose/v4: Apache-2.0",
			},
			SecurityOverrides: []string{
				"github.com/go-jose/go-jose/v4 v4.1.4 overrides the SDK v4.0.5 requirement to remediate GO-2026-4945",
			},
		},
		Checks: checks,
		Decision: Decision{
			Mode: "partial-sdk-adoption",
			UseOfficialPackages: []string{
				"github.com/logto-io/go/v2/core for Logto discovery, code exchange, refresh, revocation and end-session protocol helpers",
				"github.com/go-jose/go-jose/v4 transitively for maintained JOSE and JWKS verification",
			},
			RetainPlatformControls: []string{
				"one-time state with TTL and local returnTo binding",
				"nonce generation and constant-time nonce validation",
				"token type, not-before, issuer, audience, expiry and immutable issuer-plus-subject validation",
				"encrypted PostgreSQL BFF Session with transactional Audit and Outbox",
				"cross-instance refresh single-flight and rotation compare-and-swap",
				"platform-visible revoke failures, local logout and Logto global logout reconciliation",
				"HVAC OrganizationMembership, RoleBinding, SiteBinding, explicit deny and policy revision",
			},
			RejectFullClientFor: []string{
				"authorization request and callback do not bind or validate nonce",
				"Storage writes cannot return persistence errors",
				"refresh has no distributed single-flight or durable compare-and-swap contract",
				"remote revocation failure is ignored during SignOut",
				"organization access claims are decoded without signature verification",
			},
			Rationale: "The official SDK contains useful maintained Logto protocol primitives, but its high-level client cannot by itself satisfy the accepted BFF Session, nonce, durable failure, revocation and HVAC authorization invariants. The selected design replaces custom protocol plumbing where the official core is stronger while retaining project-owned security and business-authorization controls.",
		},
	}
}
