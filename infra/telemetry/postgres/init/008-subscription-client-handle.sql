BEGIN;
SET LOCAL ROLE s2_telemetry_migrator;

-- A client subscription id only correlates the entries of one bootstrap request with
-- its response. Reloading a page or opening a second tab reuses it legitimately, so it
-- must not be unique for the principal across all past subscriptions.
ALTER TABLE telemetry_runtime.telemetry_subscriptions
  DROP CONSTRAINT IF EXISTS telemetry_subscriptions_principal_id_client_subscription_id_key;

RESET ROLE;
COMMIT;
