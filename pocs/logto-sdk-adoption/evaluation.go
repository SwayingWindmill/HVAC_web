package logtopoc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	logtoclient "github.com/logto-io/go/v2/client"
	"github.com/logto-io/go/v2/core"
)

func newClient(fixture *oidcFixture, storage logtoclient.Storage) *logtoclient.LogtoClient {
	return logtoclient.NewLogtoClient(&logtoclient.LogtoConfig{
		Endpoint: fixture.endpoint(),
		AppId:    "hvac-web",
		Scopes:   []string{"email", core.UserScopeOrganizations},
	}, storage, logtoclient.WithHttpClient(fixture.server.Client()))
}

func signIn(client *logtoclient.LogtoClient, fixture *oidcFixture) (url.Values, error) {
	signInURL, err := client.SignIn(&logtoclient.SignInOptions{
		RedirectUri: "https://app.example.test/callback",
		LoginHint:   "fixture@example.test",
	})
	if err != nil {
		return nil, err
	}
	parsed, err := url.Parse(signInURL)
	if err != nil {
		return nil, err
	}
	fixture.setExpectedChallenge(parsed.Query().Get("code_challenge"))
	return parsed.Query(), nil
}

func callbackRequest(state string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, "/callback?code=fixture-code&state="+url.QueryEscape(state), nil)
	request.Host = "app.example.test"
	request.Header.Set("X-Forwarded-Proto", "https")
	return request
}

func errorText(err error) string {
	if err == nil {
		return "no error"
	}
	return err.Error()
}

func Summary(report Report) string {
	failed := make([]string, 0, 8)
	for name, check := range map[string]Check{
		"nonce-sent":                        report.Checks.NonceSent,
		"nonce-mismatch-rejected":           report.Checks.NonceMismatchRejected,
		"token-type-enforced":               report.Checks.TokenTypeEnforced,
		"storage-write-failure-observable":  report.Checks.StorageWriteFailureObservable,
		"distributed-refresh-single-flight": report.Checks.DistributedRefreshSingleFlight,
		"revocation-failure-observable":     report.Checks.RevocationFailureObservable,
		"organization-claims-verified":      report.Checks.OrganizationClaimsVerified,
	} {
		if !check.Passed {
			failed = append(failed, name)
		}
	}
	return report.Decision.Mode + ": full client failed " + strings.Join(failed, ", ")
}

func Evaluate(_ context.Context) (Report, error) {
	fixture, err := newOIDCFixture()
	if err != nil {
		return Report{}, err
	}
	defer fixture.Close()

	checks, err := runLoginChecks(fixture)
	if err != nil {
		return Report{}, err
	}
	checks.JWKSRotation, err = runRotationCheck(fixture)
	if err != nil {
		return Report{}, err
	}
	checks.ProviderOutageFailsClosed = runOutageCheck(fixture)
	checks.StorageWriteFailureObservable = runStorageCheck(fixture)
	checks.DistributedRefreshSingleFlight, err = runRefreshCheck(fixture)
	if err != nil {
		return Report{}, err
	}
	checks.RevocationFailureObservable, checks.LocalCredentialClear = runSignOutChecks(fixture)
	checks.OrganizationClaimsVerified, err = runOrganizationClaimCheck(fixture)
	if err != nil {
		return Report{}, err
	}
	checks.PostgresStorageAdapterPossible = Check{Passed: true, Evidence: "the SDK Storage interface can be backed by one encrypted PostgreSQL session blob, but it cannot report persistence failures"}

	report := buildReport(checks)
	if err := ValidateDecision(report); err != nil {
		return Report{}, err
	}
	return report, nil
}
