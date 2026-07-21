package logtopoc

import "fmt"

const (
	sdkRepository = "logto-io/go"
	sdkVersion    = "v2.2.0"
)

type Check struct {
	Passed   bool   `json:"passed"`
	Evidence string `json:"evidence"`
}

type SDKChecks struct {
	AuthorizationCodePKCE          Check `json:"authorizationCodePkce"`
	StateValidation                Check `json:"stateValidation"`
	StateOneTimeUse                Check `json:"stateOneTimeUse"`
	NonceSent                      Check `json:"nonceSent"`
	NonceMismatchRejected          Check `json:"nonceMismatchRejected"`
	TokenTypeEnforced              Check `json:"tokenTypeEnforced"`
	JWKSRotation                   Check `json:"jwksRotation"`
	ProviderOutageFailsClosed      Check `json:"providerOutageFailsClosed"`
	StorageWriteFailureObservable  Check `json:"storageWriteFailureObservable"`
	DistributedRefreshSingleFlight Check `json:"distributedRefreshSingleFlight"`
	RevocationFailureObservable    Check `json:"revocationFailureObservable"`
	LocalCredentialClear           Check `json:"localCredentialClear"`
	OrganizationClaimsVerified     Check `json:"organizationClaimsVerified"`
	PostgresStorageAdapterPossible Check `json:"postgresStorageAdapterPossible"`
}

type Decision struct {
	Mode                   string   `json:"mode"`
	UseOfficialPackages    []string `json:"useOfficialPackages"`
	RetainPlatformControls []string `json:"retainPlatformControls"`
	RejectFullClientFor    []string `json:"rejectFullClientFor"`
	Rationale              string   `json:"rationale"`
}

type Report struct {
	SchemaVersion int       `json:"schemaVersion"`
	SDK           SDK       `json:"sdk"`
	Checks        SDKChecks `json:"checks"`
	Decision      Decision  `json:"decision"`
}

type SDK struct {
	Repository         string   `json:"repository"`
	Version            string   `json:"version"`
	License            string   `json:"license"`
	DependencyLicenses []string `json:"dependencyLicenses"`
	SecurityOverrides  []string `json:"securityOverrides"`
}

func AllCriticalFullClientChecksPass(checks SDKChecks) bool {
	return checks.AuthorizationCodePKCE.Passed &&
		checks.StateValidation.Passed &&
		checks.StateOneTimeUse.Passed &&
		checks.NonceSent.Passed &&
		checks.NonceMismatchRejected.Passed &&
		checks.TokenTypeEnforced.Passed &&
		checks.JWKSRotation.Passed &&
		checks.ProviderOutageFailsClosed.Passed &&
		checks.StorageWriteFailureObservable.Passed &&
		checks.DistributedRefreshSingleFlight.Passed &&
		checks.RevocationFailureObservable.Passed &&
		checks.LocalCredentialClear.Passed &&
		checks.OrganizationClaimsVerified.Passed
}

func ValidateDecision(report Report) error {
	if report.Decision.Mode != "partial-sdk-adoption" {
		return fmt.Errorf("unexpected decision mode %q", report.Decision.Mode)
	}
	if AllCriticalFullClientChecksPass(report.Checks) {
		return fmt.Errorf("full SDK client passed all critical gates; partial-adoption rejection is unsupported")
	}
	if !report.Checks.AuthorizationCodePKCE.Passed || !report.Checks.StateValidation.Passed || !report.Checks.JWKSRotation.Passed || !report.Checks.ProviderOutageFailsClosed.Passed {
		return fmt.Errorf("official SDK core capabilities required for partial adoption did not pass")
	}
	return nil
}
