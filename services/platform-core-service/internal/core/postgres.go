package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

const coreServiceDatabaseRole = "s1_core_service"

type RegistryStore interface {
	ListSites(context.Context, registryauth.GrantClaims, PageRequest) (PageResult[Site], error)
	GetSite(context.Context, registryauth.GrantClaims, string) (Site, error)
	ListAssets(context.Context, registryauth.GrantClaims, string, PageRequest) (PageResult[Asset], error)
	GetAsset(context.Context, registryauth.GrantClaims, string) (Asset, error)
	ListDevices(context.Context, registryauth.GrantClaims, string, PageRequest) (PageResult[Device], error)
	GetDevice(context.Context, registryauth.GrantClaims, string) (Device, error)
	ListDeviceBindings(context.Context, registryauth.GrantClaims, string, PageRequest) (PageResult[DeviceBinding], error)
	GetSiteAssetModel(context.Context, registryauth.GrantClaims, string) (SiteAssetModel, error)
}

type PostgresStore struct {
	pool *pgxpool.Pool
}

func OpenPostgresStore(ctx context.Context, databaseURL string) (*PostgresStore, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, errors.New("Core database URL is required")
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse Core database configuration: %w", err)
	}
	config.ConnConfig.RuntimeParams["application_name"] = "platform-core-service"
	config.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"
	config.ConnConfig.RuntimeParams["statement_timeout"] = "5s"
	config.ConnConfig.RuntimeParams["lock_timeout"] = "1s"
	config.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"] = "5s"
	config.AfterConnect = func(ctx context.Context, connection *pgx.Conn) error {
		var sessionUser string
		var member bool
		if err := connection.QueryRow(ctx, `SELECT session_user, pg_has_role(session_user, 's1_core_runtime', 'MEMBER')`).Scan(&sessionUser, &member); err != nil {
			return fmt.Errorf("read Core database identity: %w", err)
		}
		if sessionUser != coreServiceDatabaseRole || !member {
			return fmt.Errorf("Core database identity %q is not allowed", sessionUser)
		}
		return nil
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("open Core Registry store: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping Core Registry store: %w", err)
	}
	return &PostgresStore{pool: pool}, nil
}

func (store *PostgresStore) Close() {
	if store != nil && store.pool != nil {
		store.pool.Close()
	}
}

func (store *PostgresStore) withReadTransaction(ctx context.Context, claims registryauth.GrantClaims, query func(pgx.Tx) error) error {
	if store == nil || store.pool == nil {
		return ErrStoreClosed
	}
	if err := validateGrantScopeIDs(claims); err != nil {
		return err
	}
	transaction, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return fmt.Errorf("begin Core Registry read: %w", err)
	}
	defer func() { _ = transaction.Rollback(ctx) }()
	if _, err := transaction.Exec(ctx, `SET LOCAL ROLE s1_core_runtime`); err != nil {
		return fmt.Errorf("activate Core runtime role: %w", err)
	}
	var configuredTenant string
	var configuredSites string
	if err := transaction.QueryRow(ctx, `
SELECT set_config('app.tenant_id', $1, true),
       set_config('app.authorized_site_ids', $2, true)
`, claims.TenantID, postgresUUIDArray(claims.AllowedSiteIDs)).Scan(&configuredTenant, &configuredSites); err != nil {
		return fmt.Errorf("set Core Registry RLS context: %w", err)
	}
	if err := query(transaction); err != nil {
		return err
	}
	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("commit Core Registry read: %w", err)
	}
	return nil
}

func (store *PostgresStore) ListSites(ctx context.Context, claims registryauth.GrantClaims, page PageRequest) (PageResult[Site], error) {
	page, err := normalizedPageRequest(page)
	if err != nil {
		return PageResult[Site]{}, err
	}
	limit := page.Limit
	result := PageResult[Site]{Items: []Site{}}
	err = store.withReadTransaction(ctx, claims, func(transaction pgx.Tx) error {
		rows, err := transaction.Query(ctx, `
SELECT id::text, tenant_id::text, code, display_name, timezone, status, revision, created_at, updated_at
FROM core_registry.sites
WHERE NOT (id = ANY($1::uuid[]))
  AND ($2 = '' OR (display_name COLLATE "C", id) > ($2 COLLATE "C", $3::uuid))
ORDER BY display_name COLLATE "C", id
LIMIT $4
`, postgresUUIDArray(claims.DeniedSiteIDs), page.DisplayName, nullableCursorID(page.ID), limit+1)
		if err != nil {
			return fmt.Errorf("query Registry sites: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			item, err := scanSite(rows)
			if err != nil {
				return err
			}
			result.Items = append(result.Items, item)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate Registry sites: %w", err)
		}
		return nil
	})
	if err != nil {
		return PageResult[Site]{}, err
	}
	result.Items, result.HasMore = trimPage(result.Items, limit)
	return result, nil
}

func (store *PostgresStore) GetSite(ctx context.Context, claims registryauth.GrantClaims, id string) (Site, error) {
	if !validUUIDv7(id) {
		return Site{}, ErrNotFound
	}
	var result Site
	err := store.withReadTransaction(ctx, claims, func(transaction pgx.Tx) error {
		item, err := scanSite(transaction.QueryRow(ctx, `
SELECT id::text, tenant_id::text, code, display_name, timezone, status, revision, created_at, updated_at
FROM core_registry.sites
WHERE id = $1::uuid
  AND NOT (id = ANY($2::uuid[]))
`, id, postgresUUIDArray(claims.DeniedSiteIDs)))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		result = item
		return nil
	})
	return result, err
}

func (store *PostgresStore) ListAssets(ctx context.Context, claims registryauth.GrantClaims, siteID string, page PageRequest) (PageResult[Asset], error) {
	if !validUUIDv7(siteID) {
		return PageResult[Asset]{}, ErrNotFound
	}
	page, err := normalizedPageRequest(page)
	if err != nil {
		return PageResult[Asset]{}, err
	}
	limit := page.Limit
	result := PageResult[Asset]{Items: []Asset{}}
	err = store.withReadTransaction(ctx, claims, func(transaction pgx.Tx) error {
		rows, err := transaction.Query(ctx, `
SELECT id::text, tenant_id::text, site_id::text, code, display_name, asset_type, status, revision, created_at, updated_at
FROM core_registry.assets
WHERE site_id = $1::uuid
  AND NOT (site_id = ANY($2::uuid[]))
  AND ($3 = '' OR (display_name COLLATE "C", id) > ($3 COLLATE "C", $4::uuid))
ORDER BY display_name COLLATE "C", id
LIMIT $5
`, siteID, postgresUUIDArray(claims.DeniedSiteIDs), page.DisplayName, nullableCursorID(page.ID), limit+1)
		if err != nil {
			return fmt.Errorf("query Registry asset: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			item, err := scanAsset(rows)
			if err != nil {
				return err
			}
			result.Items = append(result.Items, item)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate Registry asset: %w", err)
		}
		return nil
	})
	if err != nil {
		return PageResult[Asset]{}, err
	}
	result.Items, result.HasMore = trimPage(result.Items, limit)
	return result, nil
}

func (store *PostgresStore) GetAsset(ctx context.Context, claims registryauth.GrantClaims, id string) (Asset, error) {
	if !validUUIDv7(id) {
		return Asset{}, ErrNotFound
	}
	var result Asset
	err := store.withReadTransaction(ctx, claims, func(transaction pgx.Tx) error {
		item, err := scanAsset(transaction.QueryRow(ctx, `
SELECT id::text, tenant_id::text, site_id::text, code, display_name, asset_type, status, revision, created_at, updated_at
FROM core_registry.assets
WHERE id = $1::uuid
  AND NOT (site_id = ANY($2::uuid[]))
`, id, postgresUUIDArray(claims.DeniedSiteIDs)))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		result = item
		return nil
	})
	return result, err
}

func (store *PostgresStore) ListDevices(ctx context.Context, claims registryauth.GrantClaims, siteID string, page PageRequest) (PageResult[Device], error) {
	if !validUUIDv7(siteID) {
		return PageResult[Device]{}, ErrNotFound
	}
	page, err := normalizedPageRequest(page)
	if err != nil {
		return PageResult[Device]{}, err
	}
	limit := page.Limit
	result := PageResult[Device]{Items: []Device{}}
	err = store.withReadTransaction(ctx, claims, func(transaction pgx.Tx) error {
		rows, err := transaction.Query(ctx, `
SELECT id::text, tenant_id::text, site_id::text, code, display_name, device_type, status, revision, created_at, updated_at
FROM core_registry.devices
WHERE site_id = $1::uuid
  AND NOT (site_id = ANY($2::uuid[]))
  AND ($3 = '' OR (display_name COLLATE "C", id) > ($3 COLLATE "C", $4::uuid))
ORDER BY display_name COLLATE "C", id
LIMIT $5
`, siteID, postgresUUIDArray(claims.DeniedSiteIDs), page.DisplayName, nullableCursorID(page.ID), limit+1)
		if err != nil {
			return fmt.Errorf("query Registry devices: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			item, err := scanDevice(rows)
			if err != nil {
				return err
			}
			result.Items = append(result.Items, item)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate Registry devices: %w", err)
		}
		return nil
	})
	if err != nil {
		return PageResult[Device]{}, err
	}
	result.Items, result.HasMore = trimPage(result.Items, limit)
	return result, nil
}

func (store *PostgresStore) GetDevice(ctx context.Context, claims registryauth.GrantClaims, id string) (Device, error) {
	if !validUUIDv7(id) {
		return Device{}, ErrNotFound
	}
	var result Device
	err := store.withReadTransaction(ctx, claims, func(transaction pgx.Tx) error {
		item, err := scanDevice(transaction.QueryRow(ctx, `
SELECT id::text, tenant_id::text, site_id::text, code, display_name, device_type, status, revision, created_at, updated_at
FROM core_registry.devices
WHERE id = $1::uuid
  AND NOT (site_id = ANY($2::uuid[]))
`, id, postgresUUIDArray(claims.DeniedSiteIDs)))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		result = item
		return nil
	})
	return result, err
}

func (store *PostgresStore) ListDeviceBindings(ctx context.Context, claims registryauth.GrantClaims, siteID string, page PageRequest) (PageResult[DeviceBinding], error) {
	if !validUUIDv7(siteID) {
		return PageResult[DeviceBinding]{}, ErrNotFound
	}
	page, err := normalizedPageRequest(page)
	if err != nil {
		return PageResult[DeviceBinding]{}, err
	}
	limit := page.Limit
	result := PageResult[DeviceBinding]{Items: []DeviceBinding{}}
	err = store.withReadTransaction(ctx, claims, func(transaction pgx.Tx) error {
		rows, err := transaction.Query(ctx, `
SELECT binding.id::text,
       binding.tenant_id::text,
       binding.site_id::text,
       binding.device_id::text,
       binding.asset_id::text,
       binding.binding_role,
       binding.status,
       binding.valid_from,
       binding.valid_to,
       binding.revision,
       binding.created_at,
       binding.updated_at
FROM core_registry.device_bindings AS binding
JOIN core_registry.devices AS device
  ON device.tenant_id = binding.tenant_id
 AND device.site_id = binding.site_id
 AND device.id = binding.device_id
JOIN core_registry.assets AS asset
  ON asset.tenant_id = binding.tenant_id
 AND asset.site_id = binding.site_id
 AND asset.id = binding.asset_id
WHERE binding.site_id = $1::uuid
  AND NOT (binding.site_id = ANY($2::uuid[]))
  AND ($3 = '' OR (binding.binding_role COLLATE "C", binding.id) > ($3 COLLATE "C", $4::uuid))
ORDER BY binding.binding_role COLLATE "C", binding.id
LIMIT $5
`, siteID, postgresUUIDArray(claims.DeniedSiteIDs), page.DisplayName, nullableCursorID(page.ID), limit+1)
		if err != nil {
			return fmt.Errorf("query Registry DeviceBindings: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			item, err := scanDeviceBinding(rows)
			if err != nil {
				return err
			}
			result.Items = append(result.Items, item)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate Registry DeviceBindings: %w", err)
		}
		return nil
	})
	if err != nil {
		return PageResult[DeviceBinding]{}, err
	}
	result.Items, result.HasMore = trimPage(result.Items, limit)
	return result, nil
}

type rowScanner interface {
	Scan(...any) error
}

func scanSite(row rowScanner) (Site, error) {
	var item Site
	var createdAt time.Time
	var updatedAt time.Time
	if err := row.Scan(&item.ID, &item.TenantID, &item.Code, &item.DisplayName, &item.Timezone, &item.Status, &item.Revision, &createdAt, &updatedAt); err != nil {
		return Site{}, err
	}
	item.CreatedAt = formatInstant(createdAt)
	item.UpdatedAt = formatInstant(updatedAt)
	return item, nil
}

func scanAsset(row rowScanner) (Asset, error) {
	var item Asset
	var createdAt time.Time
	var updatedAt time.Time
	if err := row.Scan(&item.ID, &item.TenantID, &item.SiteID, &item.Code, &item.DisplayName, &item.AssetType, &item.Status, &item.Revision, &createdAt, &updatedAt); err != nil {
		return Asset{}, err
	}
	item.CreatedAt = formatInstant(createdAt)
	item.UpdatedAt = formatInstant(updatedAt)
	return item, nil
}

func scanDevice(row rowScanner) (Device, error) {
	var item Device
	var createdAt time.Time
	var updatedAt time.Time
	if err := row.Scan(&item.ID, &item.TenantID, &item.SiteID, &item.Code, &item.DisplayName, &item.DeviceType, &item.Status, &item.Revision, &createdAt, &updatedAt); err != nil {
		return Device{}, err
	}
	item.CreatedAt = formatInstant(createdAt)
	item.UpdatedAt = formatInstant(updatedAt)
	return item, nil
}

func scanDeviceBinding(row rowScanner) (DeviceBinding, error) {
	var item DeviceBinding
	var validFrom time.Time
	var validTo *time.Time
	var createdAt time.Time
	var updatedAt time.Time
	if err := row.Scan(
		&item.ID,
		&item.TenantID,
		&item.SiteID,
		&item.DeviceID,
		&item.AssetID,
		&item.BindingRole,
		&item.Status,
		&validFrom,
		&validTo,
		&item.Revision,
		&createdAt,
		&updatedAt,
	); err != nil {
		return DeviceBinding{}, err
	}
	item.ValidFrom = formatInstant(validFrom)
	if validTo != nil {
		formatted := formatInstant(*validTo)
		item.ValidTo = &formatted
	}
	item.CreatedAt = formatInstant(createdAt)
	item.UpdatedAt = formatInstant(updatedAt)
	return item, nil
}

func nullableCursorID(id string) any {
	if id == "" {
		return nil
	}
	return id
}

func trimPage[T any](items []T, limit int) ([]T, bool) {
	if len(items) <= limit {
		return items, false
	}
	return items[:limit], true
}

var _ RegistryStore = (*PostgresStore)(nil)
