package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/modules/iam/internal/iam"
)

const maximumInputBytes = 1 << 20

type command struct {
	UserID string `json:"userId"`
	Seed   seed   `json:"seed"`
}

type seed struct {
	TenantID       string                       `json:"tenantId"`
	SourceVersion  int64                        `json:"sourceVersion"`
	PrincipalID    string                       `json:"principalId"`
	EffectiveAt    time.Time                    `json:"effectiveAt"`
	RoleBindings   []roleBinding                `json:"roleBindings"`
	SiteBindings   []iam.ReconciledSiteBinding  `json:"siteBindings"`
	ExplicitDenies []iam.ReconciledExplicitDeny `json:"explicitDenies"`
}

type roleBinding struct {
	RoleKey string                `json:"roleKey"`
	Actions []registryauth.Action `json:"actions"`
	Effect  iam.BindingEffect     `json:"effect"`
}

type directoryUser struct {
	ID          string
	DisplayName string
	Email       string
	Status      string
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := run(ctx, os.Stdin, os.Stdout, os.Getenv); err != nil {
		fmt.Fprintln(os.Stderr, "identity-reconciler failed:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, input io.Reader, output io.Writer, getenv func(string) string) error {
	decoded, err := decodeCommand(input)
	if err != nil {
		return err
	}
	identityDatabaseURL, err := requiredEnvironment(getenv, "IDENTITY_DIRECTORY_DATABASE_URL")
	if err != nil {
		return err
	}
	iamDatabaseURL, err := requiredEnvironment(getenv, "IAM_RECONCILER_DATABASE_URL")
	if err != nil {
		return err
	}
	issuer, err := requiredEnvironment(getenv, "IDENTITY_ISSUER")
	if err != nil {
		return err
	}

	user, err := readDirectoryUser(ctx, identityDatabaseURL, decoded.UserID)
	if err != nil {
		return err
	}
	principalStatus, err := mapPrincipalStatus(user.Status)
	if err != nil {
		return err
	}
	if decoded.Seed.EffectiveAt.IsZero() {
		return errors.New("identity reconciliation effectiveAt is required")
	}

	request := iam.ReconciliationRequest{
		TenantID:      decoded.Seed.TenantID,
		SourceSystem:  "identity",
		SourceKey:     user.ID,
		SourceVersion: decoded.Seed.SourceVersion,
		Principal: iam.ReconciledPrincipal{
			ID:            decoded.Seed.PrincipalID,
			SubjectIssuer: strings.TrimRight(issuer, "/"),
			Subject:       user.ID,
			DisplayName:   user.DisplayName,
			Email:         user.Email,
			Status:        principalStatus,
		},
		Memberships: []iam.ReconciledMembership{{
			TenantID:  decoded.Seed.TenantID,
			Status:    iam.FactStatusActive,
			ValidFrom: decoded.Seed.EffectiveAt,
		}},
		SiteBindings:   append([]iam.ReconciledSiteBinding(nil), decoded.Seed.SiteBindings...),
		ExplicitDenies: append([]iam.ReconciledExplicitDeny(nil), decoded.Seed.ExplicitDenies...),
	}
	for _, binding := range decoded.Seed.RoleBindings {
		request.RoleBindings = append(request.RoleBindings, iam.ReconciledRoleBinding{
			TenantID:  decoded.Seed.TenantID,
			RoleKey:   binding.RoleKey,
			Actions:   append([]registryauth.Action(nil), binding.Actions...),
			Effect:    binding.Effect,
			ValidFrom: decoded.Seed.EffectiveAt,
		})
	}

	store, err := iam.OpenPostgresReconciliationStore(ctx, iamDatabaseURL)
	if err != nil {
		return err
	}
	defer store.Close()
	result, err := store.Reconcile(ctx, request)
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(result)
}

func decodeCommand(input io.Reader) (command, error) {
	body, err := io.ReadAll(io.LimitReader(input, maximumInputBytes+1))
	if err != nil {
		return command{}, fmt.Errorf("read identity reconciliation input: %w", err)
	}
	if len(body) > maximumInputBytes {
		return command{}, errors.New("identity reconciliation input exceeded size limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var result command
	if err := decoder.Decode(&result); err != nil {
		return command{}, fmt.Errorf("decode identity reconciliation input: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return command{}, errors.New("identity reconciliation input must contain one JSON object")
	}
	result.UserID = strings.TrimSpace(result.UserID)
	if result.UserID == "" {
		return command{}, errors.New("identity reconciliation userId is required")
	}
	return result, nil
}

func readDirectoryUser(ctx context.Context, databaseURL, userID string) (directoryUser, error) {
	pool, err := pgxpool.New(ctx, strings.TrimSpace(databaseURL))
	if err != nil {
		return directoryUser{}, fmt.Errorf("open identity directory: %w", err)
	}
	defer pool.Close()
	var user directoryUser
	err = pool.QueryRow(ctx, `
SELECT id::text, display_name, email, status
FROM identity.users
WHERE id = $1::uuid
`, userID).Scan(&user.ID, &user.DisplayName, &user.Email, &user.Status)
	if err != nil {
		return directoryUser{}, fmt.Errorf("read identity directory user: %w", err)
	}
	return user, nil
}

func mapPrincipalStatus(status string) (iam.PrincipalStatus, error) {
	switch strings.TrimSpace(status) {
	case "ACTIVE":
		return iam.PrincipalStatusActive, nil
	case "DISABLED":
		return iam.PrincipalStatusDisabled, nil
	case "RETIRED":
		return iam.PrincipalStatusRetired, nil
	default:
		return "", fmt.Errorf("unsupported identity user status %q", status)
	}
}

func requiredEnvironment(getenv func(string) string, name string) (string, error) {
	value := strings.TrimSpace(getenv(name))
	if value == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return value, nil
}
