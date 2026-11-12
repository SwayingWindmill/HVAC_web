package coreclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/modules/energy/internal/energy"
)

type totalElectricitySourcesResponse struct {
	SiteID      string                 `json:"siteId"`
	Synthesized bool                   `json:"synthesized"`
	SubMeters   []energy.TotalSubMeter `json:"subMeters"`
}

// TotalElectricitySources reads whether a Site synthesizes its total electricity from branch
// sub-meters in Core Registry using the energy-projection workload's meter-binding.resolve grant.
func (resolver *Resolver) TotalElectricitySources(ctx context.Context, tenantID, siteID string) (energy.SiteTotalElectricitySources, error) {
	grant, err := resolver.grantFor(ctx, tenantID, registryauth.ActionMeterBindingResolve)
	if err != nil {
		return energy.SiteTotalElectricitySources{}, err
	}
	endpoint := *resolver.endpoint
	endpoint.Path = "/internal/v1/registry/sites/" + url.PathEscape(siteID) + "/total-electricity-sources"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return energy.SiteTotalElectricitySources{}, fmt.Errorf("create Core total electricity sources request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-Delegation-Grant", grant)
	payload, err := resolver.exchange(request, "Core total electricity sources read")
	if err != nil {
		return energy.SiteTotalElectricitySources{}, err
	}
	var decoded totalElectricitySourcesResponse
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return energy.SiteTotalElectricitySources{}, fmt.Errorf("decode Core total electricity sources: %w", err)
	}
	return energy.SiteTotalElectricitySources{
		Synthesized: decoded.Synthesized,
		SubMeters:   decoded.SubMeters,
	}, nil
}

var _ energy.TotalSubmeterSource = (*Resolver)(nil)
