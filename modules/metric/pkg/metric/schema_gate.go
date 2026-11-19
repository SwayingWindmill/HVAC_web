package metric

import (
	"context"

	"github.com/quanlaihe/hvac-web/libs/schemagate"
)

// VerifyProductSchema validates the database schema version against PHASE1_PRODUCT_VERSION when set.
func (s *PostgresStore) VerifyProductSchema(ctx context.Context) error {
	if s == nil || s.pool == nil {
		return schemagate.VerifyEnvProductSchema(ctx, nil)
	}
	return schemagate.VerifyEnvProductSchema(ctx, s.pool)
}
