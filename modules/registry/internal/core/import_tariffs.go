package core

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

// ImportTariffVersion is a Tariff Version over the time it prices a Site's grid connection: the
// part where both the version and its assignment are in effect. EffectiveTo is empty while it has
// no successor.
type ImportTariffVersion struct {
	VersionID     string         `json:"versionId"`
	EffectiveFrom string         `json:"effectiveFrom"`
	EffectiveTo   *string        `json:"effectiveTo"`
	Timezone      string         `json:"timezone"`
	Currency      string         `json:"currency"`
	Periods       []TariffPeriod `json:"periods"`
}

// ImportTariffs is the Site's import Tariff timeline before an instant, oldest first, on its
// first grid connection by boundary code, as Settlement and M&V price it.
type ImportTariffs struct {
	SiteID   string                `json:"siteId"`
	Versions []ImportTariffVersion `json:"versions"`
}

func (store *PostgresStore) ListImportTariffs(ctx context.Context, claims registryauth.GrantClaims, siteID string, before time.Time) (ImportTariffs, error) {
	if !validUUIDv7(siteID) {
		return ImportTariffs{}, ErrNotFound
	}
	timeline := ImportTariffs{SiteID: siteID, Versions: []ImportTariffVersion{}}
	err := store.withReadTransaction(ctx, claims, func(transaction pgx.Tx) error {
		rows, err := transaction.Query(ctx, `
SELECT boundary.boundary_code, version.id::text,
       GREATEST(version.effective_from, assignment.effective_from),
       LEAST(version.effective_to, assignment.effective_to),
       version.timezone, version.currency
FROM core_registry.tariff_assignments AS assignment
JOIN core_registry.settlement_boundaries AS boundary
  ON boundary.tenant_id = assignment.tenant_id AND boundary.site_id = assignment.site_id AND boundary.id = assignment.boundary_id
JOIN core_registry.tariff_versions AS version
  ON version.tenant_id = assignment.tenant_id AND version.site_id = assignment.site_id AND version.tariff_id = assignment.tariff_id
WHERE assignment.site_id = $1::uuid
  AND NOT (assignment.site_id = ANY($2::uuid[]))
  AND assignment.status = 'RELEASED' AND assignment.effective_from < $3
  AND boundary.status = 'ACTIVE' AND boundary.boundary_type = 'GRID_CONNECTION' AND boundary.direction = 'IMPORT'
  AND version.status = 'RELEASED' AND version.effective_from < $3
  AND GREATEST(version.effective_from, assignment.effective_from) < COALESCE(LEAST(version.effective_to, assignment.effective_to), 'infinity'::timestamptz)
ORDER BY boundary.boundary_code, 3
`, siteID, postgresUUIDArray(claims.DeniedSiteIDs), before.UTC())
		if err != nil {
			return fmt.Errorf("query Site import Tariff versions: %w", err)
		}
		firstBoundary := ""
		for rows.Next() {
			var boundaryCode string
			var version ImportTariffVersion
			var effectiveFrom time.Time
			var effectiveTo *time.Time
			if err := rows.Scan(&boundaryCode, &version.VersionID, &effectiveFrom, &effectiveTo, &version.Timezone, &version.Currency); err != nil {
				rows.Close()
				return err
			}
			if firstBoundary == "" {
				firstBoundary = boundaryCode
			}
			if boundaryCode != firstBoundary {
				continue
			}
			version.EffectiveFrom = formatInstant(effectiveFrom)
			if effectiveTo != nil {
				formatted := formatInstant(*effectiveTo)
				version.EffectiveTo = &formatted
			}
			timeline.Versions = append(timeline.Versions, version)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for index := range timeline.Versions {
			if timeline.Versions[index].Periods, err = queryTariffPeriods(ctx, transaction, timeline.Versions[index].VersionID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return ImportTariffs{}, err
	}
	return timeline, nil
}
