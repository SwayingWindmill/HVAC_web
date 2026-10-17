package analytics

import (
	"context"

	"github.com/quanlaihe/hvac-web/libs/analyticsmodel"
)

type CallerContext struct {
	PrincipalID    string
	PolicyRevision string
}

type EnergySeriesEngine interface {
	QueryEnergySeries(context.Context, CallerContext, analyticsmodel.EnergySeriesQuery) (analyticsmodel.EnergySeriesResponse, error)
	// QuerySemantic answers the Operations Agent's semantic query over the catalog's views (#573).
	QuerySemantic(context.Context, CallerContext, analyticsmodel.SemanticQuery) (analyticsmodel.SemanticQueryResponse, error)
}
