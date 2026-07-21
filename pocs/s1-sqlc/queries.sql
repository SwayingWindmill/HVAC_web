-- name: ListOrganizations :many
SELECT id, code, display_name, status, revision, created_at, updated_at
FROM core_registry.organizations
WHERE id = ANY (sqlc.arg(authorized_organization_ids)::uuid[])
  AND (display_name COLLATE "C", id) > (sqlc.arg(after_display_name)::text COLLATE "C", sqlc.arg(after_id)::uuid)
ORDER BY display_name COLLATE "C", id
LIMIT sqlc.arg(page_limit);

-- name: GetOrganization :one
SELECT id, code, display_name, status, revision, created_at, updated_at
FROM core_registry.organizations
WHERE id = sqlc.arg(organization_id)
  AND id = ANY (sqlc.arg(authorized_organization_ids)::uuid[]);

-- name: ListSites :many
SELECT id, organization_id, code, display_name, timezone, status, revision, created_at, updated_at
FROM core_registry.sites
WHERE organization_id = sqlc.arg(organization_id)
  AND (
    organization_id = ANY (sqlc.arg(authorized_organization_ids)::uuid[])
    OR id = ANY (sqlc.arg(authorized_site_ids)::uuid[])
  )
  AND (display_name COLLATE "C", id) > (sqlc.arg(after_display_name)::text COLLATE "C", sqlc.arg(after_id)::uuid)
ORDER BY display_name COLLATE "C", id
LIMIT sqlc.arg(page_limit);

-- name: GetSite :one
SELECT id, organization_id, code, display_name, timezone, status, revision, created_at, updated_at
FROM core_registry.sites
WHERE id = sqlc.arg(site_id)
  AND (
    organization_id = ANY (sqlc.arg(authorized_organization_ids)::uuid[])
    OR id = ANY (sqlc.arg(authorized_site_ids)::uuid[])
  );

-- name: ListEquipment :many
SELECT id, organization_id, site_id, code, display_name, equipment_type, status, revision, created_at, updated_at
FROM core_registry.equipment
WHERE organization_id = sqlc.arg(organization_id)
  AND site_id = sqlc.arg(site_id)
  AND (
    organization_id = ANY (sqlc.arg(authorized_organization_ids)::uuid[])
    OR site_id = ANY (sqlc.arg(authorized_site_ids)::uuid[])
  )
  AND (display_name COLLATE "C", id) > (sqlc.arg(after_display_name)::text COLLATE "C", sqlc.arg(after_id)::uuid)
ORDER BY display_name COLLATE "C", id
LIMIT sqlc.arg(page_limit);

-- name: GetEquipment :one
SELECT id, organization_id, site_id, code, display_name, equipment_type, status, revision, created_at, updated_at
FROM core_registry.equipment
WHERE id = sqlc.arg(equipment_id)
  AND (
    organization_id = ANY (sqlc.arg(authorized_organization_ids)::uuid[])
    OR site_id = ANY (sqlc.arg(authorized_site_ids)::uuid[])
  );

-- name: ListDevices :many
SELECT id, organization_id, site_id, code, display_name, device_type, status, revision, created_at, updated_at
FROM core_registry.devices
WHERE organization_id = sqlc.arg(organization_id)
  AND site_id = sqlc.arg(site_id)
  AND (
    organization_id = ANY (sqlc.arg(authorized_organization_ids)::uuid[])
    OR site_id = ANY (sqlc.arg(authorized_site_ids)::uuid[])
  )
  AND (display_name COLLATE "C", id) > (sqlc.arg(after_display_name)::text COLLATE "C", sqlc.arg(after_id)::uuid)
ORDER BY display_name COLLATE "C", id
LIMIT sqlc.arg(page_limit);

-- name: GetDevice :one
SELECT id, organization_id, site_id, code, display_name, device_type, status, revision, created_at, updated_at
FROM core_registry.devices
WHERE id = sqlc.arg(device_id)
  AND (
    organization_id = ANY (sqlc.arg(authorized_organization_ids)::uuid[])
    OR site_id = ANY (sqlc.arg(authorized_site_ids)::uuid[])
  );
