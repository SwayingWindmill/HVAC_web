package identityadmin

import (
	"context"

	"github.com/quanlaihe/hvac-web/libs/schemagate"
)

// VerifyProductSchema validates the database schema version against PHASE1_PRODUCT_VERSION when set.
func (directory *Directory) VerifyProductSchema(ctx context.Context) error {
	if directory == nil || directory.store == nil {
		return schemagate.VerifyEnvProductSchema(ctx, nil)
	}
	return directory.store.VerifyEnvProductSchema(ctx)
}
