package core

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

func (store *PostgresStore) ListSpaceChildren(ctx context.Context, claims registryauth.GrantClaims, siteID, parentSpaceID string, page PageRequest) (PageResult[Space], error) {
	if err := ensureSiteScope(claims, siteID); err != nil {
		return PageResult[Space]{}, err
	}
	if parentSpaceID != "" && !validUUIDv7(parentSpaceID) {
		return PageResult[Space]{}, ErrInvalidPage
	}
	page, err := normalizedPageRequest(page)
	if err != nil {
		return PageResult[Space]{}, err
	}
	result := PageResult[Space]{}
	err = store.withReadTransaction(ctx, claims, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
SELECT id::text, tenant_id::text, site_id::text, parent_space_id::text, code, display_name, space_type, status, revision, created_at, updated_at
FROM core_registry.spaces
WHERE site_id=$1::uuid
  AND (($2 = '' AND parent_space_id IS NULL) OR ($2 <> '' AND parent_space_id=$2::uuid))
  AND ($3 = '' OR (display_name, id) > ($3, $4::uuid))
ORDER BY display_name, id
LIMIT $5
`, siteID, parentSpaceID, page.DisplayName, nullablePageID(page.ID), page.Limit+1)
		if err != nil {
			return fmt.Errorf("list Registry Space children: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			item, err := scanSpace(rows)
			if err != nil {
				return err
			}
			result.Items = append(result.Items, item)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate Registry Space children: %w", err)
		}
		if len(result.Items) > page.Limit {
			result.HasMore = true
			result.Items = result.Items[:page.Limit]
		}
		return nil
	})
	return result, err
}

func (store *PostgresStore) ListDevicePoints(ctx context.Context, claims registryauth.GrantClaims, deviceID string, page PageRequest) (PageResult[TelemetryPoint], error) {
	if !validUUIDv7(deviceID) {
		return PageResult[TelemetryPoint]{}, ErrNotFound
	}
	page, err := normalizedPageRequest(page)
	if err != nil {
		return PageResult[TelemetryPoint]{}, err
	}
	result := PageResult[TelemetryPoint]{}
	err = store.withReadTransaction(ctx, claims, func(tx pgx.Tx) error {
		var siteID string
		if err := tx.QueryRow(ctx, `SELECT site_id::text FROM core_registry.devices WHERE id=$1::uuid`, deviceID).Scan(&siteID); err != nil {
			if err == pgx.ErrNoRows {
				return ErrNotFound
			}
			return fmt.Errorf("resolve Registry Device Point scope: %w", err)
		}
		rows, err := tx.Query(ctx, `
SELECT id::text, tenant_id::text, site_id::text, reporting_device_id::text, sensor_id::text, point_code, source_key, display_name,
       point_type, value_type, unit, writable, sample_interval_ms, publish_interval_ms, stale_after_ms, counter_decrease_mode, counter_rollover_modulus, source_metadata, status, revision, created_at, updated_at
FROM core_registry.telemetry_points
WHERE site_id=$1::uuid AND reporting_device_id=$2::uuid
  AND ($3 = '' OR (display_name, id) > ($3, $4::uuid))
ORDER BY display_name, id
LIMIT $5
`, siteID, deviceID, page.DisplayName, nullablePageID(page.ID), page.Limit+1)
		if err != nil {
			return fmt.Errorf("list Registry Device Points: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			item, err := scanTelemetryPoint(rows)
			if err != nil {
				return err
			}
			result.Items = append(result.Items, item)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate Registry Device Points: %w", err)
		}
		if len(result.Items) > page.Limit {
			result.HasMore = true
			result.Items = result.Items[:page.Limit]
		}
		return nil
	})
	return result, err
}

func nullablePageID(value string) any {
	if value == "" {
		return nil
	}
	return value
}
