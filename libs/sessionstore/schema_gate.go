package sessionstore

import (
	"context"

	"github.com/quanlaihe/hvac-web/libs/schemagate"
)

// VerifyProductSchema validates the database schema version against PHASE1_PRODUCT_VERSION when set.
func (store *PostgresStore) VerifyProductSchema(ctx context.Context) error {
	if store == nil || store.pool == nil {
		return schemagate.VerifyEnvProductSchema(ctx, nil)
	}
	return schemagate.VerifyEnvProductSchema(ctx, store.pool)
}
