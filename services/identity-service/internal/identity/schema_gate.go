package identity

import (
	"context"

	"github.com/quanlaihe/hvac-web/libs/schemagate"
)

// VerifyEnvProductSchema validates the database schema version against PHASE1_PRODUCT_VERSION when set.
func (store *Store) VerifyEnvProductSchema(ctx context.Context) error {
	if store == nil || store.pool == nil {
		return schemagate.VerifyEnvProductSchema(ctx, nil)
	}
	return schemagate.VerifyEnvProductSchema(ctx, store.pool)
}
