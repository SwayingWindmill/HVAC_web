#!/usr/bin/env node
// Publishes the next revision of a Tenant's chilled-water low-delta-T rule on the local stack
// (spec docs/specs/fdd-rule-definition.md). Revisions are never changed: a new threshold or window
// is a new revision, in effect from --effective-from (default now); findings keep citing theirs.
// --operating-gates sets the revision's operating gates as JSON; without it the revision keeps the
// gates of the revision before it (the first takes the defaults of migration 033).
//
//   node scripts/publish-fdd-rule-revision.mjs --tenant <uuid> --minimum-delta-tc 4 --window-minutes 30 [--effective-from <ISO instant>] [--operating-gates '<json>']
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
    'operating-gates': { type: 'string' },
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
const gates = values['operating-gates'] === undefined ? undefined : operatingGates(values['operating-gates']);

// The same rules FDD applies when it reads a revision (services/fdd-service/internal/fdd/gates.go),
// so a revision FDD would refuse is never published.
function operatingGates(raw) {
  const gates = JSON.parse(raw);
  const share = (value) => typeof value === 'number' && value > 0 && value <= 1;
  const hold = (value) => Number.isInteger(value) && value >= 0 && value <= 60;
  const fraction = (value) => typeof value === 'number' && value > 0 && value < 1;
  const known = ['minimumOperatingShare', 'minimumLowShare', 'temperatureHoldMinutes', 'gateHoldMinutes', 'chillerRunning', 'loadRatio', 'chilledWaterPumpRunning'];
  if (gates === null || typeof gates !== 'object' || Array.isArray(gates) || Object.keys(gates).some((key) => !known.includes(key))) {
    throw new Error(`--operating-gates must be an object of ${known.join(', ')}`);
  }
  if (!share(gates.minimumOperatingShare) || !share(gates.minimumLowShare)) throw new Error('--operating-gates shares must be above 0 and at most 1');
  if (!hold(gates.temperatureHoldMinutes) || !hold(gates.gateHoldMinutes)) throw new Error('--operating-gates holds must be whole minutes from 0 to 60');
  if (gates.chillerRunning !== undefined && (Object.keys(gates.chillerRunning).join() !== 'minimumRatedPowerFraction' || !fraction(gates.chillerRunning.minimumRatedPowerFraction))) {
    throw new Error('--operating-gates chillerRunning.minimumRatedPowerFraction must be between 0 and 1');
  }
  if (gates.loadRatio !== undefined && (gates.chillerRunning === undefined || Object.keys(gates.loadRatio).join() !== 'minimum' || !fraction(gates.loadRatio.minimum))) {
    throw new Error('--operating-gates loadRatio.minimum must be between 0 and 1 and needs chillerRunning');
  }
  if (gates.chilledWaterPumpRunning !== undefined && JSON.stringify(gates.chilledWaterPumpRunning) !== '{}') {
    throw new Error('--operating-gates chilledWaterPumpRunning takes no settings');
  }
  return JSON.stringify(gates);
}

// The id is a v7 UUID formed the way the local seeds form job ids; the revision is the next number.
const dedup = `fdd-rule-${values.tenant}-${Date.now()}`;
// The defaults of migration 033-fdd-operating-gates.sql.
const defaultGates = '{"minimumOperatingShare":0.7,"minimumLowShare":0.7,"temperatureHoldMinutes":2,"gateHoldMinutes":10,"chillerRunning":{"minimumRatedPowerFraction":0.1},"loadRatio":{"minimum":0.15},"chilledWaterPumpRunning":{}}';
const tenant = `${sqlLiteral(values.tenant)}::uuid`;
const previousGates = `(SELECT operating_gates FROM core_registry.fdd_rule_revisions
    WHERE tenant_id = ${tenant} AND rule_code = 'CHILLED_WATER_LOW_DELTA_T' ORDER BY revision DESC LIMIT 1)`;
const gatesSQL = gates === undefined
  ? `coalesce(${previousGates}, ${sqlLiteral(defaultGates)}::jsonb)`
  : `${sqlLiteral(gates)}::jsonb`;
const sql = `WITH next AS (
  SELECT coalesce(max(revision), 0) + 1 AS revision FROM core_registry.fdd_rule_revisions
  WHERE tenant_id = ${tenant} AND rule_code = 'CHILLED_WATER_LOW_DELTA_T'
)
INSERT INTO core_registry.fdd_rule_revisions (id, tenant_id, rule_code, revision, minimum_delta_tc, window_minutes, operating_gates, effective_from, created_at)
SELECT ${sqlJobId.replaceAll('dedup', sqlLiteral(dedup))}, ${tenant}, 'CHILLED_WATER_LOW_DELTA_T', next.revision,
       ${threshold}, ${window}, ${gatesSQL}, ${sqlLiteral(effectiveFrom.toISOString())}::timestamptz, clock_timestamp()
FROM next
RETURNING 'revision ' || revision || ': ' || minimum_delta_tc || ' K over ' || window_minutes || ' min under ' || operating_gates::text || ' from ' || effective_from || ' (' || id || ')';`;
const published = execFileSync('docker', ['exec', '-i', localContainer('postgres'), 'psql', '-U', 'postgres', '-d', 'hvac_s1', '-v', 'ON_ERROR_STOP=1', '-Atq'], { input: sql }).toString().trim();
console.log(`Published low-delta-T rule ${published}`);
