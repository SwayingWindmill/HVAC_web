-- Expose active COMMAND point source_metadata through the Registry read port so
-- Connectivity resolves real-site Edge actuation routes and pre-dispatch interlocks
-- without reading Registry tables directly (ADR 0016).
\set ON_ERROR_STOP on

BEGIN;

SET LOCAL ROLE s1_core_migrator;

GRANT SELECT (source_metadata) ON core_registry.telemetry_points TO core_registry_read_port;

CREATE VIEW core_registry.command_point_routes_v1 WITH (security_barrier) AS
SELECT point.tenant_id,
       point.site_id,
       point.reporting_device_id AS device_id,
       point.id AS point_id,
       point.point_code,
       point.source_key,
       point.writable,
       point.source_metadata,
       point.revision AS point_revision
FROM core_registry.telemetry_points AS point
JOIN core_registry.devices AS device
  ON device.id = point.reporting_device_id
 AND device.status = 'ACTIVE'
WHERE point.status = 'ACTIVE'
  AND point.point_type = 'COMMAND';

RESET ROLE;

ALTER VIEW core_registry.command_point_routes_v1 OWNER TO core_registry_read_port;
GRANT SELECT ON core_registry.command_point_routes_v1 TO connectivity_runtime;

COMMIT;
