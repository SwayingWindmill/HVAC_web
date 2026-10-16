package gateway

import (
	"context"
	"net/http"

	"github.com/quanlaihe/hvac-web/libs/observability"
)

func injectOperationsTrace(ctx context.Context, header http.Header) {
	observability.InjectHTTP(ctx, header)
}
