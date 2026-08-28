// Package schemagate is the startup counterpart to the one-shot migration
// preflight. A service must refuse to start when the database it is connected
// to records a phase1_deployment.product_schema whose product_version does not
// match the version the binary was deployed for.
package schemagate

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

const productName = "hvac-web"

// Querier is the minimal database surface the gate needs. *pgxpool.Pool
// satisfies it.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// VerifyProductSchema returns nil when the database records a product_schema
// row whose product_version equals expectedVersion. A missing row or a
// version mismatch is an error; the caller must refuse to start on it.
func VerifyProductSchema(ctx context.Context, db Querier, expectedVersion string) error {
	var actual string
	err := db.QueryRow(ctx,
		"SELECT product_version FROM phase1_deployment.product_schema WHERE product = $1",
		productName,
	).Scan(&actual)
	if err != nil {
		return fmt.Errorf("schema compatibility state missing: %w", err)
	}
	if actual != expectedVersion {
		return fmt.Errorf("schema version mismatch: binary expects %s, database has %s", expectedVersion, actual)
	}
	return nil
}
