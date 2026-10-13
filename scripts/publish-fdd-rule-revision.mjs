#!/usr/bin/env node
// Publishes the next revision of a Tenant's chilled-water low-delta-T rule on the local stack
// (spec docs/specs/fdd-rule-definition.md). Revisions are never changed: a new threshold or window
// is a new revision, in effect from --effective-from (default now); findings keep citing theirs.
//
//   node scripts/publish-fdd-rule-revision.mjs --tenant <uuid> --minimum-delta-tc 4 --window-minutes 30 [--effective-from <ISO instant>]
import { execFileSync } from 'node:child_process';
import { parseArgs } from 'node:util';
import { sqlJobId, sqlLiteral } from './central-plant-local-contract.mjs';
import { localContainer } from './lib/local-environment.mjs';

const { values } = parseArgs({
  options: {
    tenant: { type: 'string' },
    'minimum-delta-tc': { type: 'string' },
    'window-minutes': { type: 'string' },
    'effective-from': { type: 'string' },
  },
});
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const threshold = Number(values['minimum-delta-tc']);
const window = Number(values['window-minutes']);
const effectiveFrom = values['effective-from'] ? new Date(values['effective-from']) : new Date();
if (!uuid.test(values.tenant ?? '')) throw new Error('--tenant must be the Tenant UUID');
if (!(threshold > 0)) throw new Error('--minimum-delta-tc must be a positive number of kelvin');
if (!Number.isInteger(window) || window < 1 || window > 1440) throw new Error('--window-minutes must be a whole number from 1 to 1440');
if (Number.isNaN(effectiveFrom.getTime())) throw new Error('--effective-from must be an instant');

// The id is a v7 UUID formed the way the local seeds form job ids; the revision is the next number.
const dedup = `fdd-rule-${values.tenant}-${Date.now()}`;
const sql = `WITH next AS (
  SELECT coalesce(max(revision), 0) + 1 AS revision FROM core_registry.fdd_rule_revisions
  WHERE tenant_id = ${sqlLiteral(values.tenant)}::uuid AND rule_code = 'CHILLED_WATER_LOW_DELTA_T'
)
INSERT INTO core_registry.fdd_rule_revisions (id, tenant_id, rule_code, revision, minimum_delta_tc, window_minutes, effective_from, created_at)
SELECT ${sqlJobId.replaceAll('dedup', sqlLiteral(dedup))}, ${sqlLiteral(values.tenant)}::uuid, 'CHILLED_WATER_LOW_DELTA_T', next.revision,
       ${threshold}, ${window}, ${sqlLiteral(effectiveFrom.toISOString())}::timestamptz, clock_timestamp()
FROM next
RETURNING 'revision ' || revision || ': ' || minimum_delta_tc || ' K over ' || window_minutes || ' min from ' || effective_from || ' (' || id || ')';`;
const published = execFileSync('docker', ['exec', '-i', localContainer('postgres'), 'psql', '-U', 'postgres', '-d', 'hvac_s1', '-v', 'ON_ERROR_STOP=1', '-Atq'], { input: sql }).toString().trim();
console.log(`Published low-delta-T rule ${published}`);
