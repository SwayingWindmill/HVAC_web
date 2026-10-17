package core

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

// SiteTariff is the import Tariff in effect for a Site: the released version of the Tariff
// assigned to the Site's grid connection, with its time-of-use periods.
type SiteTariff struct {
	SiteID        string         `json:"siteId"`
	DisplayName   string         `json:"displayName"`
	Version       int64          `json:"version"`
	EffectiveFrom string         `json:"effectiveFrom"`
	Timezone      string         `json:"timezone"`
	Currency      string         `json:"currency"`
	Periods       []TariffPeriod `json:"periods"`
}

// TariffPeriod prices the local minutes [StartMinute, EndMinute) of a day type. Settlement
// charges demand per period, on the period's maximum demand in the billing cycle.
type TariffPeriod struct {
	PeriodCode       string  `json:"periodCode"`
	DayType          string  `json:"dayType"`
	StartMinute      int     `json:"startMinute"`
	EndMinute        int     `json:"endMinute"`
	EnergyRatePerKWh float64 `json:"energyRatePerKWh"`
	DemandRatePerKW  float64 `json:"demandRatePerKW"`
}

func (store *PostgresStore) GetSiteTariff(ctx context.Context, claims registryauth.GrantClaims, siteID string) (SiteTariff, error) {
	if !validUUIDv7(siteID) {
		return SiteTariff{}, ErrNotFound
	}
	tariff := SiteTariff{SiteID: siteID, Periods: []TariffPeriod{}}
	err := store.withReadTransaction(ctx, claims, func(transaction pgx.Tx) error {
		var versionID string
		var effectiveFrom time.Time
		err := transaction.QueryRow(ctx, `
SELECT version.id::text, tariff.display_name, version.version, version.effective_from, version.timezone, version.currency
FROM core_registry.tariff_assignments AS assignment
JOIN core_registry.settlement_boundaries AS boundary
  ON boundary.tenant_id = assignment.tenant_id AND boundary.site_id = assignment.site_id AND boundary.id = assignment.boundary_id
JOIN core_registry.tariffs AS tariff
  ON tariff.tenant_id = assignment.tenant_id AND tariff.site_id = assignment.site_id AND tariff.id = assignment.tariff_id
JOIN core_registry.tariff_versions AS version
  ON version.tenant_id = assignment.tenant_id AND version.site_id = assignment.site_id AND version.tariff_id = assignment.tariff_id
WHERE assignment.site_id = $1::uuid
  AND NOT (assignment.site_id = ANY($2::uuid[]))
  AND assignment.status = 'RELEASED' AND assignment.effective_from <= now() AND (assignment.effective_to IS NULL OR assignment.effective_to > now())
  AND boundary.status = 'ACTIVE' AND boundary.boundary_type = 'GRID_CONNECTION' AND boundary.direction = 'IMPORT'
  AND version.status = 'RELEASED' AND version.effective_from <= now() AND (version.effective_to IS NULL OR version.effective_to > now())
ORDER BY boundary.boundary_code
LIMIT 1
`, siteID, postgresUUIDArray(claims.DeniedSiteIDs)).Scan(&versionID, &tariff.DisplayName, &tariff.Version, &effectiveFrom, &tariff.Timezone, &tariff.Currency)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("query Site Tariff in effect: %w", err)
		}
		tariff.EffectiveFrom = formatInstant(effectiveFrom)
		tariff.Periods, err = queryTariffPeriods(ctx, transaction, versionID)
		return err
	})
	if err != nil {
		return SiteTariff{}, err
	}
	return tariff, nil
}

// queryTariffPeriods is a Tariff Version's time-of-use periods in clock order per day type.
func queryTariffPeriods(ctx context.Context, transaction pgx.Tx, versionID string) ([]TariffPeriod, error) {
	periods := []TariffPeriod{}
	err := queryAssetRows(ctx, transaction, `
SELECT period_code, day_type, local_start_minute, local_end_minute,
       (pricing_rule->>'energyRatePerKWh')::float8, COALESCE((pricing_rule->>'demandRatePerKW')::float8, 0)
FROM core_registry.tariff_periods
WHERE tariff_version_id = $1::uuid
ORDER BY day_type, local_start_minute
`, versionID, func(row rowScanner) error {
		var period TariffPeriod
		err := row.Scan(&period.PeriodCode, &period.DayType, &period.StartMinute, &period.EndMinute, &period.EnergyRatePerKWh, &period.DemandRatePerKW)
		if err == nil {
			periods = append(periods, period)
		}
		return err
	})
	return periods, err
}
