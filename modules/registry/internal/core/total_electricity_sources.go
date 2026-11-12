package core

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

// SiteTotalSubMeter is one branch electricity meter whose Counter Point contributes to a Site's
// synthesized total electricity.
type SiteTotalSubMeter struct {
	PointID   string `json:"pointId"`
	DeviceID  string `json:"deviceId"`
	MeterName string `json:"meterName"`
	Ordinal   int    `json:"ordinal"`
}

// SiteTotalElectricitySources reports whether a Site synthesizes its total electricity from branch
// sub-meters (virtual_meter_code = 'site_total_electricity') and, if so, lists those sub-meters in
// display order.
type SiteTotalElectricitySources struct {
	SiteID      string              `json:"siteId"`
	Synthesized bool                `json:"synthesized"`
	SubMeters   []SiteTotalSubMeter `json:"subMeters"`
}

func (store *PostgresStore) GetSiteTotalElectricitySources(ctx context.Context, claims registryauth.GrantClaims, siteID string) (SiteTotalElectricitySources, error) {
	if !validUUIDv7(siteID) {
		return SiteTotalElectricitySources{}, ErrNotFound
	}
	result := SiteTotalElectricitySources{
		SiteID:    siteID,
		SubMeters: []SiteTotalSubMeter{},
	}
	err := store.withReadTransaction(ctx, claims, func(transaction pgx.Tx) error {
		var exists int
		err := transaction.QueryRow(ctx, `
SELECT 1
FROM core_registry.sites
WHERE id = $1::uuid
  AND NOT (id = ANY($2::uuid[]))
`, siteID, postgresUUIDArray(claims.DeniedSiteIDs)).Scan(&exists)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("check Site visibility for total electricity sources: %w", err)
		}

		var virtualMeterID string
		err = transaction.QueryRow(ctx, `
SELECT vm.id::text
FROM core_registry.virtual_meters AS vm
JOIN core_registry.energy_types AS et ON et.id = vm.energy_type_id
WHERE vm.site_id = $1::uuid
  AND vm.virtual_meter_code = 'site_total_electricity'
  AND vm.calculation_type = 'SUM'
  AND vm.direction = 'IMPORT'
  AND et.energy_code = 'electricity'
  AND vm.status IN ('RELEASED', 'ACTIVE')
  AND vm.effective_from <= now()
  AND (vm.effective_to IS NULL OR now() < vm.effective_to)
ORDER BY vm.effective_from DESC, vm.version DESC, vm.id
LIMIT 1
`, siteID).Scan(&virtualMeterID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("query Site total virtual meter: %w", err)
		}

		rows, err := transaction.Query(ctx, `
SELECT mb.point_id::text,
       mb.device_id::text,
       d.display_name,
       vms.ordinal
FROM core_registry.virtual_meter_sources AS vms
JOIN core_registry.meter_bindings AS mb
  ON mb.tenant_id = vms.tenant_id
 AND mb.site_id = vms.site_id
 AND mb.id = vms.source_meter_binding_id
JOIN core_registry.devices AS d
  ON d.tenant_id = mb.tenant_id
 AND d.site_id = mb.site_id
 AND d.id = mb.device_id
WHERE vms.site_id = $1::uuid
  AND vms.virtual_meter_id = $2::uuid
  AND vms.source_type = 'METER_BINDING'
  AND vms.coefficient = 1
  AND mb.point_type = 'COUNTER'
  AND mb.status IN ('RELEASED', 'ACTIVE')
  AND mb.effective_from <= now()
  AND (mb.effective_to IS NULL OR now() < mb.effective_to)
ORDER BY vms.ordinal ASC, mb.id ASC
`, siteID, virtualMeterID)
		if err != nil {
			return fmt.Errorf("query Site total virtual meter sources: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var item SiteTotalSubMeter
			if err := rows.Scan(&item.PointID, &item.DeviceID, &item.MeterName, &item.Ordinal); err != nil {
				return fmt.Errorf("scan Site total virtual meter source: %w", err)
			}
			result.SubMeters = append(result.SubMeters, item)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		result.Synthesized = len(result.SubMeters) > 0
		return nil
	})
	if err != nil {
		return SiteTotalElectricitySources{}, err
	}
	return result, nil
}
