package energy

import (
	"context"
	"time"
)

type pointKey struct {
	tenantID string
	siteID   string
	deviceID string
	pointID  string
}

// MatchMeterBindings resolves the active binding at sampledAt across [EffectiveFrom, EffectiveTo) intervals.
func MatchMeterBindings(candidates []BindingResolution, sampledAt time.Time) BindingResolution {
	sampledUTC := sampledAt.UTC()
	var matched []BindingResolution
	for _, item := range candidates {
		if sampledUTC.Before(item.EffectiveFrom.UTC()) {
			continue
		}
		if item.EffectiveTo != nil && !sampledUTC.Before(item.EffectiveTo.UTC()) {
			continue
		}
		matched = append(matched, item)
	}
	switch len(matched) {
	case 0:
		return BindingResolution{Status: BindingNoMatch}
	case 1:
		chosen := matched[0]
		chosen.Status = BindingMatch
		return chosen
	default:
		return BindingResolution{Status: BindingAmbiguous}
	}
}

// StaticBindingResolver resolves meter bindings directly in memory for HTTP-free unit tests.
type StaticBindingResolver struct {
	byPoint map[pointKey][]BindingResolution
}

// NewStaticBindingResolver indexes a fixed set of meter bindings by tenant, site, device, and point.
func NewStaticBindingResolver(bindings ...BindingResolution) *StaticBindingResolver {
	byPoint := make(map[pointKey][]BindingResolution, len(bindings))
	for _, binding := range bindings {
		key := pointKey{
			tenantID: binding.TenantID,
			siteID:   binding.SiteID,
			deviceID: binding.DeviceID,
			pointID:  binding.PointID,
		}
		byPoint[key] = append(byPoint[key], binding)
	}
	return &StaticBindingResolver{byPoint: byPoint}
}

func (resolver *StaticBindingResolver) Resolve(_ context.Context, input BindingResolveInput) (BindingResolution, error) {
	if resolver == nil {
		return BindingResolution{Status: BindingNoMatch}, nil
	}
	key := pointKey{
		tenantID: input.TenantID,
		siteID:   input.SiteID,
		deviceID: input.DeviceID,
		pointID:  input.PointID,
	}
	return MatchMeterBindings(resolver.byPoint[key], input.SampledAt), nil
}

var _ BindingResolver = (*StaticBindingResolver)(nil)
