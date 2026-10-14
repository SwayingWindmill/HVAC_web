package iam

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

type postgresCommandAuthorizationStore struct {
	store                       *PostgresAuthorizationStore
	policyRevision              string
	emergencyRevocationRevision uint64
}

// CommandAuthorization uses the consumer's declared grant revisions; configuration
// supplies protocol metadata, while database facts alone supply permission.
func (store *PostgresAuthorizationStore) CommandAuthorization(policyRevision string, emergencyRevocationRevision uint64) CommandAuthorizationStore {
	return &postgresCommandAuthorizationStore{store, policyRevision, emergencyRevocationRevision}
}

func (adapter *postgresCommandAuthorizationStore) LookupCommandAuthorization(ctx context.Context, lookup CommandAuthorizationLookup) (CommandAuthorizationFacts, error) {
	result := CommandAuthorizationFacts{EmergencyRevocationRevision: adapter.emergencyRevocationRevision}
	facts, err := adapter.store.lookupAccess(ctx, lookup.AuthorizationLookup, nil, func(transaction pgx.Tx, _ *AuthorizationFacts) error {
		if err := setRequestedDevices(ctx, transaction, []string{lookup.DeviceID}); err != nil {
			return err
		}
		err := transaction.QueryRow(ctx, `
SELECT site_id::text
FROM core_registry.devices
WHERE id = $1::uuid AND tenant_id = $2::uuid AND status = 'ACTIVE'
`, lookup.DeviceID, lookup.TenantID).Scan(&result.DeviceSiteID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read IAM Command target Device: %w", err)
		}
		return nil
	})
	if err != nil {
		return CommandAuthorizationFacts{}, err
	}
	facts.PolicyRevision = adapter.policyRevision
	result.AuthorizationFacts = facts
	return result, nil
}
