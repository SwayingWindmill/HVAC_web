package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
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
	ListAssetsFiltered(context.Context, registryauth.GrantClaims, string, AssetFilter, PageRequest) (PageResult[Asset], error)
	GetAsset(context.Context, registryauth.GrantClaims, string) (Asset, error)
	ListDevices(context.Context, registryauth.GrantClaims, string, PageRequest) (PageResult[Device], error)
	ListDevicesFiltered(context.Context, registryauth.GrantClaims, string, DeviceFilter, PageRequest) (PageResult[Device], error)
	GetDevice(context.Context, registryauth.GrantClaims, string) (Device, error)
	ListTemplates(context.Context, registryauth.GrantClaims, TemplateKind, PageRequest) (PageResult[Template], error)
	GetTemplateRevision(context.Context, registryauth.GrantClaims, string) (TemplateRevision, error)
	ListSiteTemplateAssignments(context.Context, registryauth.GrantClaims, string, TemplateKind, string, PageRequest) (PageResult[TemplateAssignment], error)
	ListDeviceBindings(context.Context, registryauth.GrantClaims, string, PageRequest) (PageResult[DeviceBinding], error)
	ListSiteMeterBindings(context.Context, registryauth.GrantClaims, string) (SiteMeterBindings, error)
	GetSiteTotalElectricitySources(context.Context, registryauth.GrantClaims, string) (SiteTotalElectricitySources, error)
	ListSpaceChildren(context.Context, registryauth.GrantClaims, string, string, PageRequest) (PageResult[Space], error)
	ListDevicePoints(context.Context, registryauth.GrantClaims, string, PageRequest) (PageResult[TelemetryPoint], error)
	GetSiteAssetModel(context.Context, registryauth.GrantClaims, string) (SiteAssetModel, error)
	GetSiteTariff(context.Context, registryauth.GrantClaims, string) (SiteTariff, error)
	ListImportTariffs(context.Context, registryauth.GrantClaims, string, time.Time) (ImportTariffs, error)
	GetOperatingSchedule(context.Context, registryauth.GrantClaims, string) (OperatingSchedule, error)
	ListOperatingExceptions(context.Context, registryauth.GrantClaims, string, OperatingExceptionFilter) (OperatingExceptionList, error)
	ListMonthlyElectricityBudgets(context.Context, registryauth.GrantClaims, string, MonthlyElectricityBudgetFilter) (MonthlyElectricityBudgetList, error)
	ListUtilityBills(context.Context, registryauth.GrantClaims, string, UtilityBillFilter) (UtilityBillList, error)
	GetSiteControlState(context.Context, registryauth.GrantClaims, string) (SiteControlState, error)
	GetSiteAutoControlLease(context.Context, registryauth.GrantClaims, string) (SiteAutoControlLeaseState, error)
	ListOptimizationExecutionMonitors(context.Context, registryauth.GrantClaims, string) (OptimizationExecutionMonitorList, error)
}

type PostgresStore struct {
	pool *pgxpool.Pool
}

func OpenPostgresStore(ctx context.Context, databaseURL string) (*PostgresStore, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, errors.New("core database URL is required")
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse Core database configuration: %w", err)
	}
	config.MaxConns = 16
	config.MinConns = 1
	config.MaxConnLifetime = 30 * time.Minute
	config.MaxConnIdleTime = 5 * time.Minute
	config.HealthCheckPeriod = 30 * time.Second
	config.ConnConfig.RuntimeParams["application_name"] = "platform-core-service"
	config.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"
	config.ConnConfig.RuntimeParams["statement_timeout"] = "5s"
	config.ConnConfig.RuntimeParams["lock_timeout"] = "1s"
	config.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"] = "5s"
	config.AfterConnect = func(ctx context.Context, connection *pgx.Conn) error {
		var sessionUser string
		var runtimeMember, writerMember bool
		if err := connection.QueryRow(ctx, `SELECT session_user, pg_has_role(session_user, 's1_core_runtime', 'MEMBER'), pg_has_role(session_user, 's1_core_writer', 'MEMBER')`).Scan(&sessionUser, &runtimeMember, &writerMember); err != nil {
			return fmt.Errorf("read Core database identity: %w", err)
		}
		if sessionUser != coreServiceDatabaseRole || !runtimeMember || !writerMember {
			return fmt.Errorf("core database identity %q is not allowed", sessionUser)
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
SELECT `+siteColumns+`
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
SELECT `+siteColumns+`
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
	return store.ListAssetsFiltered(ctx, claims, siteID, AssetFilter{}, page)
}

func (store *PostgresStore) ListAssetsFiltered(ctx context.Context, claims registryauth.GrantClaims, siteID string, filter AssetFilter, page PageRequest) (PageResult[Asset], error) {
	if siteID != "" && !validUUIDv7(siteID) {
		return PageResult[Asset]{}, ErrNotFound
	}
	if err := filter.Validate(); err != nil {
		return PageResult[Asset]{}, err
	}
	page, err := normalizedPageRequest(page)
	if err != nil {
		return PageResult[Asset]{}, err
	}
	limit := page.Limit
	query, args, err := buildFilteredEntityListSQL(TemplateAsset, assetColumns, claims, siteID, filter.asResourceFilter(), page, limit)
	if err != nil {
		return PageResult[Asset]{}, err
	}
	result := PageResult[Asset]{Items: []Asset{}}
	err = store.withReadTransaction(ctx, claims, func(transaction pgx.Tx) error {
		rows, err := transaction.Query(ctx, query, args...)
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
SELECT `+assetColumns+`
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
	return store.ListDevicesFiltered(ctx, claims, siteID, DeviceFilter{}, page)
}

func (store *PostgresStore) ListDevicesFiltered(ctx context.Context, claims registryauth.GrantClaims, siteID string, filter DeviceFilter, page PageRequest) (PageResult[Device], error) {
	if siteID != "" && !validUUIDv7(siteID) {
		return PageResult[Device]{}, ErrNotFound
	}
	if err := filter.Validate(); err != nil {
		return PageResult[Device]{}, err
	}
	page, err := normalizedPageRequest(page)
	if err != nil {
		return PageResult[Device]{}, err
	}
	limit := page.Limit
	query, args, err := buildFilteredEntityListSQL(TemplateDevice, deviceColumns, claims, siteID, filter.asResourceFilter(), page, limit)
	if err != nil {
		return PageResult[Device]{}, err
	}
	result := PageResult[Device]{Items: []Device{}}
	err = store.withReadTransaction(ctx, claims, func(transaction pgx.Tx) error {
		rows, err := transaction.Query(ctx, query, args...)
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
SELECT `+deviceColumns+`
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

func (store *PostgresStore) ListTemplates(ctx context.Context, claims registryauth.GrantClaims, templateKind TemplateKind, page PageRequest) (PageResult[Template], error) {
	switch templateKind {
	case "", TemplateAsset, TemplateDevice, TemplatePoint:
	default:
		return PageResult[Template]{}, ErrInvalidPage
	}
	page, err := normalizedPageRequest(page)
	if err != nil {
		return PageResult[Template]{}, err
	}
	limit := page.Limit
	result := PageResult[Template]{Items: []Template{}}
	err = store.withReadTransaction(ctx, claims, func(transaction pgx.Tx) error {
		rows, err := transaction.Query(ctx, `
SELECT t.id::text, t.tenant_id::text, t.template_key, t.template_kind,
       COALESCE(lr.id::text, ''), COALESCE(lr.revision_number, 0), lr.released_at, t.created_at
FROM core_registry.registry_templates AS t
LEFT JOIN LATERAL (
  SELECT r.id, r.revision_number, r.released_at
  FROM core_registry.registry_template_revisions AS r
  WHERE r.tenant_id = t.tenant_id
    AND r.template_id = t.id
    AND r.status = 'RELEASED'
  ORDER BY r.revision_number DESC
  LIMIT 1
) AS lr ON true
WHERE t.tenant_id = $1::uuid
  AND ($2 = '' OR t.template_kind = $2)
  AND ($3 = '' OR (t.template_key COLLATE "C", t.id) > ($3 COLLATE "C", $4::uuid))
ORDER BY t.template_key COLLATE "C", t.id
LIMIT $5
`, claims.TenantID, string(templateKind), page.DisplayName, nullableCursorID(page.ID), limit+1)
		if err != nil {
			return fmt.Errorf("query Registry templates: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var (
				item       Template
				releasedAt *time.Time
				createdAt  time.Time
			)
			if err := rows.Scan(&item.ID, &item.TenantID, &item.TemplateKey, &item.TemplateKind, &item.LatestRevisionID, &item.LatestRevisionNumber, &releasedAt, &createdAt); err != nil {
				return err
			}
			if releasedAt != nil {
				item.ReleasedAt = formatInstant(*releasedAt)
			}
			item.CreatedAt = formatInstant(createdAt)
			result.Items = append(result.Items, item)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate Registry templates: %w", err)
		}
		return nil
	})
	if err != nil {
		return PageResult[Template]{}, err
	}
	result.Items, result.HasMore = trimPage(result.Items, limit)
	return result, nil
}

func (store *PostgresStore) GetTemplateRevision(ctx context.Context, claims registryauth.GrantClaims, revisionID string) (TemplateRevision, error) {
	if !validUUIDv7(revisionID) {
		return TemplateRevision{}, ErrNotFound
	}
	var result TemplateRevision
	err := store.withReadTransaction(ctx, claims, func(transaction pgx.Tx) error {
		var (
			payloadRaw    []byte
			referencesRaw []byte
			releasedAt    *time.Time
		)
		err := transaction.QueryRow(ctx, `
SELECT tr.id::text, tr.tenant_id::text, tr.template_id::text, t.template_key, t.template_kind,
       tr.revision_number, tr.status, tr.payload, tr.release_references, tr.released_at
FROM core_registry.registry_template_revisions AS tr
JOIN core_registry.registry_templates AS t
  ON t.id = tr.template_id AND t.tenant_id = tr.tenant_id
WHERE tr.tenant_id = $1::uuid
  AND tr.id = $2::uuid
  AND tr.status = 'RELEASED'
`, claims.TenantID, revisionID).Scan(
			&result.ID,
			&result.TenantID,
			&result.TemplateID,
			&result.TemplateKey,
			&result.TemplateKind,
			&result.RevisionNumber,
			&result.Status,
			&payloadRaw,
			&referencesRaw,
			&releasedAt,
		)
		if errors.Is(err, pgx.ErrNoRows) || releasedAt == nil {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if err := json.Unmarshal(payloadRaw, &result.Payload); err != nil {
			return fmt.Errorf("decode template revision payload: %w", err)
		}
		if err := json.Unmarshal(referencesRaw, &result.ReleaseReferences); err != nil {
			return fmt.Errorf("decode template release references: %w", err)
		}
		result.ReleasedAt = formatInstant(*releasedAt)
		return nil
	})
	return result, err
}

func (store *PostgresStore) ListSiteTemplateAssignments(ctx context.Context, claims registryauth.GrantClaims, siteID string, targetType TemplateKind, targetID string, page PageRequest) (PageResult[TemplateAssignment], error) {
	if !validUUIDv7(siteID) {
		return PageResult[TemplateAssignment]{}, ErrNotFound
	}
	switch targetType {
	case "", TemplateAsset, TemplateDevice, TemplatePoint:
	default:
		return PageResult[TemplateAssignment]{}, ErrInvalidPage
	}
	if targetID != "" && !validUUIDv7(targetID) {
		return PageResult[TemplateAssignment]{}, ErrInvalidPage
	}
	page, err := normalizedPageRequest(page)
	if err != nil {
		return PageResult[TemplateAssignment]{}, err
	}
	limit := page.Limit
	result := PageResult[TemplateAssignment]{Items: []TemplateAssignment{}}
	err = store.withReadTransaction(ctx, claims, func(transaction pgx.Tx) error {
		rows, err := transaction.Query(ctx, `
SELECT id::text, tenant_id::text, site_id::text, target_type, target_id::text,
       template_revision_id::text, valid_from, valid_to, revision
FROM core_registry.registry_template_assignments
WHERE tenant_id = $1::uuid
  AND site_id = $2::uuid
  AND NOT (site_id = ANY($3::uuid[]))
  AND ($4 = '' OR target_type = $4)
  AND ($5::uuid IS NULL OR target_id = $5::uuid)
  AND ($6 = '' OR (target_type COLLATE "C", id) > ($6 COLLATE "C", $7::uuid))
ORDER BY target_type COLLATE "C", id
LIMIT $8
`, claims.TenantID, siteID, postgresUUIDArray(claims.DeniedSiteIDs), string(targetType), nullableCursorID(targetID), page.DisplayName, nullableCursorID(page.ID), limit+1)
		if err != nil {
			return fmt.Errorf("query Registry template assignments: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var (
				item      TemplateAssignment
				validFrom time.Time
				validTo   *time.Time
			)
			if err := rows.Scan(&item.ID, &item.TenantID, &item.SiteID, &item.TargetType, &item.TargetID, &item.TemplateRevisionID, &validFrom, &validTo, &item.Revision); err != nil {
				return err
			}
			item.ValidFrom = formatInstant(validFrom)
			if validTo != nil {
				formatted := formatInstant(*validTo)
				item.ValidTo = &formatted
			}
			result.Items = append(result.Items, item)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate Registry template assignments: %w", err)
		}
		return nil
	})
	if err != nil {
		return PageResult[TemplateAssignment]{}, err
	}
	result.Items, result.HasMore = trimPage(result.Items, limit)
	return result, nil
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

func (store *PostgresStore) ListSiteMeterBindings(ctx context.Context, claims registryauth.GrantClaims, siteID string) (SiteMeterBindings, error) {
	if !validUUIDv7(siteID) {
		return SiteMeterBindings{}, ErrInvalidBindingResolution
	}
	result := SiteMeterBindings{
		SiteID: siteID,
		Items:  []MeterBindingResolution{},
	}
	err := store.withReadTransaction(ctx, claims, func(transaction pgx.Tx) error {
		rows, err := transaction.Query(ctx, `
SELECT mb.tenant_id::text,
       mb.site_id::text,
       mb.meter_id::text,
       mb.id::text,
       mb.topology_version_id::text,
       mb.version,
       mb.revision,
       mb.energy_type_id::text,
       et.energy_code,
       mb.meter_role,
       mb.direction,
       mb.device_id::text,
       mb.point_id::text,
       mb.point_type,
       COALESCE(pt.unit, ''),
       mb.effective_from,
       mb.effective_to,
       CASE WHEN scope.branch THEN 'BRANCH' ELSE 'SITE' END,
       CASE WHEN scope.branch THEN tn.device_id::text END
FROM core_registry.meter_bindings AS mb
JOIN core_registry.energy_types AS et ON et.id = mb.energy_type_id
JOIN core_registry.energy_edges AS edge ON edge.id = mb.energy_edge_id
JOIN core_registry.energy_nodes AS fn ON fn.id = edge.from_node_id
JOIN core_registry.energy_nodes AS tn ON tn.id = edge.to_node_id
LEFT JOIN core_registry.telemetry_points AS pt ON pt.id = mb.point_id
CROSS JOIN LATERAL (SELECT tn.node_type = 'LOAD' AND tn.device_id IS NOT NULL AND fn.node_type <> 'GRID' AS branch) AS scope
WHERE mb.site_id = $1::uuid
  AND NOT (mb.site_id = ANY($2::uuid[]))
  AND mb.point_type = 'COUNTER'
  AND mb.meter_role = 'PRIMARY'
  AND mb.status IN ('RELEASED', 'ACTIVE')
ORDER BY mb.device_id ASC, mb.point_id ASC, mb.effective_from DESC, mb.version DESC, mb.id ASC
`, siteID, postgresUUIDArray(claims.DeniedSiteIDs))
		if err != nil {
			return fmt.Errorf("query Core site meter bindings: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var item MeterBindingResolution
			var loadDeviceID *string
			if err := rows.Scan(
				&item.TenantID, &item.SiteID, &item.MeterID, &item.MeterBindingID, &item.TopologyVersionID,
				&item.BindingVersion, &item.BindingRevision, &item.EnergyTypeID, &item.EnergyType, &item.MeterRole, &item.Direction,
				&item.DeviceID, &item.PointID, &item.PointType, &item.ReadingUnit, &item.EffectiveFrom, &item.EffectiveTo, &item.AccountingScope, &loadDeviceID,
			); err != nil {
				return fmt.Errorf("scan Core site meter binding: %w", err)
			}
			item.ReadingUnit = strings.TrimSpace(item.ReadingUnit)
			if loadDeviceID != nil {
				item.LoadDeviceID = *loadDeviceID
			}
			item.EffectiveFrom = item.EffectiveFrom.UTC()
			if item.EffectiveTo != nil {
				value := item.EffectiveTo.UTC()
				item.EffectiveTo = &value
			}
			result.Items = append(result.Items, item)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		rows.Close()
		if len(result.Items) == 0 {
			return nil
		}
		ratios, err := querySiteMeterRatioVersions(ctx, transaction, siteID, claims.DeniedSiteIDs)
		if err != nil {
			return err
		}
		attachMeterRatioVersions(result.Items, ratios)
		return nil
	})
	if err != nil {
		return SiteMeterBindings{}, err
	}
	return result, nil
}

func querySiteMeterRatioVersions(ctx context.Context, transaction pgx.Tx, siteID string, deniedSiteIDs []string) ([]MeterRatioVersionSummary, error) {
	var hasTable bool
	if err := transaction.QueryRow(ctx, `SELECT to_regclass('core_registry.meter_ratio_versions') IS NOT NULL`).Scan(&hasTable); err != nil {
		return nil, fmt.Errorf("check meter ratio versions table: %w", err)
	}
	if !hasTable {
		return nil, nil
	}
	rows, err := transaction.Query(ctx, `
SELECT id::text,
       tenant_id::text,
       site_id::text,
       COALESCE(meter_binding_id::text, ''),
       COALESCE(point_id::text, ''),
       ct_ratio::double precision,
       pt_ratio::double precision,
       meter_multiplier::double precision,
       ratio_application_mode,
       reading_unit,
       effective_from,
       effective_to,
       status,
       revision
FROM core_registry.meter_ratio_versions
WHERE site_id = $1::uuid
  AND NOT (site_id = ANY($2::uuid[]))
  AND status = 'RELEASED'
ORDER BY effective_from ASC, revision ASC, id ASC
`, siteID, postgresUUIDArray(deniedSiteIDs))
	if err != nil {
		return nil, fmt.Errorf("query Core site meter ratio versions: %w", err)
	}
	defer rows.Close()
	ratios := []MeterRatioVersionSummary{}
	for rows.Next() {
		var ratio MeterRatioVersionSummary
		if err := rows.Scan(
			&ratio.ID,
			&ratio.TenantID,
			&ratio.SiteID,
			&ratio.MeterBindingID,
			&ratio.PointID,
			&ratio.CTRatio,
			&ratio.PTRatio,
			&ratio.MeterMultiplier,
			&ratio.RatioApplicationMode,
			&ratio.ReadingUnit,
			&ratio.EffectiveFrom,
			&ratio.EffectiveTo,
			&ratio.Status,
			&ratio.Revision,
		); err != nil {
			return nil, fmt.Errorf("scan Core site meter ratio version: %w", err)
		}
		ratio.EffectiveFrom = ratio.EffectiveFrom.UTC()
		if ratio.EffectiveTo != nil {
			value := ratio.EffectiveTo.UTC()
			ratio.EffectiveTo = &value
		}
		ratios = append(ratios, ratio)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return ratios, nil
}

func attachMeterRatioVersions(items []MeterBindingResolution, ratios []MeterRatioVersionSummary) {
	if len(items) == 0 || len(ratios) == 0 {
		return
	}
	byPoint := make(map[string][]MeterRatioVersionSummary)
	byBinding := make(map[string][]MeterRatioVersionSummary)
	for _, ratio := range ratios {
		ratio.EffectiveFrom = ratio.EffectiveFrom.UTC()
		if ratio.EffectiveTo != nil {
			value := ratio.EffectiveTo.UTC()
			ratio.EffectiveTo = &value
		}
		if ratio.PointID != "" {
			byPoint[ratio.PointID] = append(byPoint[ratio.PointID], ratio)
		} else if ratio.MeterBindingID != "" {
			byBinding[ratio.MeterBindingID] = append(byBinding[ratio.MeterBindingID], ratio)
		}
	}
	for index := range items {
		pointMatches := byPoint[items[index].PointID]
		bindingMatches := byBinding[items[index].MeterBindingID]
		if len(pointMatches) == 0 && len(bindingMatches) == 0 {
			continue
		}
		merged := make([]MeterRatioVersionSummary, 0, len(pointMatches)+len(bindingMatches))
		merged = append(merged, pointMatches...)
		merged = append(merged, bindingMatches...)
		if len(merged) > 1 {
			sort.Slice(merged, func(i, j int) bool {
				if !merged[i].EffectiveFrom.Equal(merged[j].EffectiveFrom) {
					return merged[i].EffectiveFrom.Before(merged[j].EffectiveFrom)
				}
				if merged[i].Revision != merged[j].Revision {
					return merged[i].Revision < merged[j].Revision
				}
				return merged[i].ID < merged[j].ID
			})
		}
		items[index].RatioVersions = merged
	}
}

type rowScanner interface {
	Scan(...any) error
}

// siteColumns are what scanSite reads, in its order.
const siteColumns = `id::text, tenant_id::text, code, display_name, timezone, status, revision, created_at, updated_at, floor_area_m2, climate_zone, building_plan`

func scanSite(row rowScanner) (Site, error) {
	var item Site
	var createdAt time.Time
	var updatedAt time.Time
	var buildingPlanRaw []byte
	if err := row.Scan(&item.ID, &item.TenantID, &item.Code, &item.DisplayName, &item.Timezone, &item.Status, &item.Revision, &createdAt, &updatedAt, &item.FloorAreaM2, &item.ClimateZone, &buildingPlanRaw); err != nil {
		return Site{}, err
	}
	if len(buildingPlanRaw) > 0 && string(buildingPlanRaw) != "null" {
		var plan SiteBuildingPlan
		if err := json.Unmarshal(buildingPlanRaw, &plan); err != nil {
			return Site{}, err
		}
		item.BuildingPlan = &plan
	}
	item.CreatedAt = formatInstant(createdAt)
	item.UpdatedAt = formatInstant(updatedAt)
	return item, nil
}

// assetColumns are what scanAsset reads, in its order.
const assetColumns = `id::text, tenant_id::text, site_id::text, code, display_name, asset_type, status, revision, created_at, updated_at, rated_power, rated_capacity, tags, water_loop, branch_code`

// deviceColumns are what scanDevice reads, in its order.
const deviceColumns = `id::text, tenant_id::text, site_id::text, code, display_name, device_type, status, revision, created_at, updated_at, external_id, protocol_address, tags, connectivity_mode`

func scanAsset(row rowScanner) (Asset, error) {
	var item Asset
	var createdAt time.Time
	var updatedAt time.Time
	var tagsRaw []byte
	if err := row.Scan(&item.ID, &item.TenantID, &item.SiteID, &item.Code, &item.DisplayName, &item.AssetType, &item.Status, &item.Revision, &createdAt, &updatedAt, &item.RatedPower, &item.RatedCapacity, &tagsRaw, &item.WaterLoop, &item.BranchCode); err != nil {
		return Asset{}, err
	}
	tags, err := unmarshalTags(tagsRaw)
	if err != nil {
		return Asset{}, err
	}
	item.Tags = tags
	item.CreatedAt = formatInstant(createdAt)
	item.UpdatedAt = formatInstant(updatedAt)
	return item, nil
}

func scanDevice(row rowScanner) (Device, error) {
	var item Device
	var createdAt time.Time
	var updatedAt time.Time
	var protocolAddressRaw []byte
	var tagsRaw []byte
	if err := row.Scan(
		&item.ID,
		&item.TenantID,
		&item.SiteID,
		&item.Code,
		&item.DisplayName,
		&item.DeviceType,
		&item.Status,
		&item.Revision,
		&createdAt,
		&updatedAt,
		&item.ExternalID,
		&protocolAddressRaw,
		&tagsRaw,
		&item.ConnectivityMode,
	); err != nil {
		return Device{}, err
	}
	if len(protocolAddressRaw) > 0 && string(protocolAddressRaw) != "null" {
		if err := json.Unmarshal(protocolAddressRaw, &item.ProtocolAddress); err != nil {
			return Device{}, err
		}
	}
	tags, err := unmarshalTags(tagsRaw)
	if err != nil {
		return Device{}, err
	}
	item.Tags = tags
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
