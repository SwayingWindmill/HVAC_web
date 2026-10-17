package coreclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/libs/tariffmodel"
	"github.com/quanlaihe/hvac-web/modules/energy/internal/energy"
)

type importTariffsResponse struct {
	SiteID   string `json:"siteId"`
	Versions []struct {
		VersionID     string     `json:"versionId"`
		EffectiveFrom time.Time  `json:"effectiveFrom"`
		EffectiveTo   *time.Time `json:"effectiveTo"`
		Timezone      string     `json:"timezone"`
		Currency      string     `json:"currency"`
		Periods       []struct {
			PeriodCode       string  `json:"periodCode"`
			DayType          string  `json:"dayType"`
			StartMinute      int     `json:"startMinute"`
			EndMinute        int     `json:"endMinute"`
			EnergyRatePerKWh float64 `json:"energyRatePerKWh"`
		} `json:"periods"`
	} `json:"versions"`
}

// ImportTariffs reads the Site's import Tariff timeline from Core Registry. Cube prices in CNY
// only, so a version in another currency fails the Site rather than being priced as CNY.
func (resolver *Resolver) ImportTariffs(ctx context.Context, tenantID, siteID string, before time.Time) ([]tariffmodel.Version, error) {
	grant, err := resolver.grantFor(ctx, tenantID, registryauth.ActionTariffRead)
	if err != nil {
		return nil, err
	}
	endpoint := *resolver.endpoint
	endpoint.Path = "/internal/v1/registry/sites/" + url.PathEscape(siteID) + "/import-tariffs"
	endpoint.RawQuery = url.Values{"before": {before.UTC().Format(time.RFC3339Nano)}}.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create Core import Tariff request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-Delegation-Grant", grant)
	payload, err := resolver.exchange(request, "Core import Tariff read")
	if err != nil {
		return nil, err
	}
	var decoded importTariffsResponse
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return nil, fmt.Errorf("decode Core import Tariffs: %w", err)
	}
	versions := make([]tariffmodel.Version, 0, len(decoded.Versions))
	for _, each := range decoded.Versions {
		if each.Currency != "CNY" {
			return nil, fmt.Errorf("import Tariff version %s is priced in %s, not CNY", each.VersionID, each.Currency)
		}
		location, err := time.LoadLocation(each.Timezone)
		if err != nil {
			return nil, fmt.Errorf("import Tariff version %s timezone: %w", each.VersionID, err)
		}
		version := tariffmodel.Version{ID: each.VersionID, EffectiveFrom: each.EffectiveFrom, EffectiveTo: each.EffectiveTo, Location: location}
		for _, period := range each.Periods {
			version.Periods = append(version.Periods, tariffmodel.Period{
				Code: period.PeriodCode, DayType: period.DayType, StartMinute: period.StartMinute, EndMinute: period.EndMinute, EnergyRatePerKWh: period.EnergyRatePerKWh,
			})
		}
		versions = append(versions, version)
	}
	return versions, nil
}

var _ energy.TariffSource = (*Resolver)(nil)
