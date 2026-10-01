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
}
