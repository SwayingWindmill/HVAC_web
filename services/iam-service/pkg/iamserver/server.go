package iamserver

import (
	"context"
	"net/http"

	"github.com/quanlaihe/hvac-web/services/iam-service/internal/iam"
)

const (
	CurrentPrincipalPath     = iam.CurrentPrincipalPath
	RegistryReadDecisionPath = iam.RegistryReadDecisionPath
)

type Config = iam.Config
type AuthorizationStore = iam.AuthorizationStore
type AuthorizationLookup = iam.AuthorizationLookup
type PrincipalCapabilityResolver = iam.PrincipalCapabilityResolver
type PrincipalCapabilityLookup = iam.PrincipalCapabilityLookup
type BindingEffect = iam.BindingEffect
type PostgresAuthorizationStore = iam.PostgresAuthorizationStore
type PostgresTelemetryGrantStore = iam.PostgresTelemetryGrantStore
type TelemetryGrantStore = iam.TelemetryGrantStore
type StaticRegistryGrantStatusStore = iam.StaticRegistryGrantStatusStore

const (
	BindingEffectAllow = iam.BindingEffectAllow
	BindingEffectDeny  = iam.BindingEffectDeny
)

func NewHandler(config Config) http.Handler {
	return iam.NewHandler(config)
}

func NewS1FixtureAuthorizationStore(subjectIssuer string) AuthorizationStore {
	return iam.NewS1FixtureAuthorizationStore(subjectIssuer)
}

func NewDenyAllAuthorizationStore(policyRevision string) AuthorizationStore {
	return iam.NewDenyAllAuthorizationStore(policyRevision)
}

func OpenPostgresAuthorizationStore(ctx context.Context, databaseURL string) (*PostgresAuthorizationStore, error) {
	return iam.OpenPostgresAuthorizationStore(ctx, databaseURL)
}

func OpenPostgresTelemetryGrantStore(ctx context.Context, databaseURL string) (*PostgresTelemetryGrantStore, error) {
	return iam.OpenPostgresTelemetryGrantStore(ctx, databaseURL)
}
