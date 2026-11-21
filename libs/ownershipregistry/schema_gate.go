package ownershipregistry

import (
	"context"

	"github.com/quanlaihe/hvac-web/libs/schemagate"
)

// VerifyProductSchema validates the database schema version against PHASE1_PRODUCT_VERSION when set.
func (sink *PostgresAuditSink) VerifyProductSchema(ctx context.Context) error {
	if sink == nil || sink.pool == nil {
		return schemagate.VerifyEnvProductSchema(ctx, nil)
	}
	return schemagate.VerifyEnvProductSchema(ctx, sink.pool)
}
