// Recalculates a Site's hourly and daily Metrics over a window, after history arrived late (an import
// or a replay): first the Metrics read from points, then those computed from other Metrics, so a
// daily cost never prices hours that are still being recalculated.
//
//   node scripts/site-metric-recalc.mjs deploy/sites/songyaxuan.site.json --from 2026-08-22T00:00:00+08:00 --to 2026-10-15T00:00:00+08:00
//
// PHASE1_POSTGRES_CONTAINER selects the database container (default: the local environment's).
import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { parseArgs } from 'node:util';
import { sqlJobId, sqlLiteral } from './central-plant-local-contract.mjs';
import { localContainer } from './lib/local-environment.mjs';

const { positionals, values } = parseArgs({ allowPositionals: true, options: { from: { type: 'string' }, to: { type: 'string' } } });
const profile = JSON.parse(readFileSync(positionals[0] ?? '', 'utf8'));
const from = new Date(values.from ?? '');
const to = new Date(values.to ?? '');
if (Number.isNaN(from.getTime()) || Number.isNaN(to.getTime()) || to <= from) throw new Error('--from and --to must be instants in order');

const container = process.env.PHASE1_POSTGRES_CONTAINER || localContainer('postgres');
const psql = (sql) => execFileSync('docker', ['exec', '-i', container, 'psql', '-U', 'postgres', '-d', 'hvac_s1', '-v', 'ON_ERROR_STOP=1', '-Atq'], { input: sql }).toString().trim();
const { site, tenantId } = profile;
const zone = sqlLiteral(site.timezone);
const runStamp = Date.now().toString(36);

function queue(pass) {
  const dependsOnMetrics = `EXISTS (SELECT 1 FROM core_registry.metric_dependencies dependency
      WHERE dependency.metric_version_id = binding.metric_version_id AND dependency.dependency_type = 'METRIC')`;
  psql(`WITH bindings AS (
  SELECT binding.id, CASE binding.time_granularity WHEN 'HOUR' THEN interval '1 hour' ELSE interval '1 day' END AS step,
         CASE binding.time_granularity WHEN 'HOUR' THEN date_trunc('hour', ${sqlLiteral(from.toISOString())}::timestamptz)
              ELSE date_trunc('day', ${sqlLiteral(from.toISOString())}::timestamptz AT TIME ZONE ${zone}) AT TIME ZONE ${zone} END AS first_start
  FROM core_registry.metric_bindings binding
  WHERE binding.site_id = ${sqlLiteral(site.id)} AND binding.status = 'RELEASED' AND binding.time_granularity IN ('HOUR', 'DAY')
    AND ${pass === 'dependent' ? '' : 'NOT '}${dependsOnMetrics}
), periods AS (
  SELECT bindings.id AS binding_id, period_start, period_start + bindings.step AS period_end,
         'site-recalc-${runStamp}-${pass}-' || bindings.id || '-' || to_char(period_start AT TIME ZONE 'UTC', 'YYYYMMDDHH24') AS dedup
  FROM bindings CROSS JOIN LATERAL generate_series(bindings.first_start, ${sqlLiteral(to.toISOString())}::timestamptz - bindings.step, bindings.step) AS period_start
)
INSERT INTO core_registry.job_instances (job_id, trigger_type, job_type, tenant_id, site_id, scheduled_for, priority, dedup_key, payload, state, max_attempts, timeout_seconds, created_at, updated_at)
SELECT ${sqlJobId}, 'BACKFILL', 'METRIC_RECALC', ${sqlLiteral(tenantId)}, ${sqlLiteral(site.id)}, period_end, 20, dedup,
       jsonb_build_object('bindingId', binding_id, 'periodStart', period_start, 'periodEnd', period_end), 'READY', 3, 60, clock_timestamp(), clock_timestamp()
FROM periods;`);
}

async function settle(pass) {
  const pending = `SELECT count(*) FILTER (WHERE state NOT IN ('SUCCEEDED', 'FAILED', 'DEAD', 'CANCELLED')) || '|' || count(*) FILTER (WHERE state IN ('FAILED', 'DEAD')) || '|' || count(*)
    FROM core_registry.job_instances WHERE dedup_key LIKE 'site-recalc-${runStamp}-${pass}-%'`;
  for (;;) {
    const [open, failed, total] = psql(pending).split('|').map(Number);
    if (open === 0) {
      console.log(`${site.name}: recalculated ${total - failed} of ${total} Metric periods (${pass})${failed ? `; ${failed} failed, see core_registry.job_instances` : ''}.`);
      return;
    }
    await new Promise((resolve) => setTimeout(resolve, 10000));
  }
}

for (const pass of ['independent', 'dependent']) {
  queue(pass);
  await settle(pass);
}
