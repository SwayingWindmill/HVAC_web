import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

const workOrderApiSource = readFileSync(new URL('../apps/hvac-web/src/api/work-orders.ts', import.meta.url), 'utf8');
const workOrderPageSource = readFileSync(new URL('../apps/hvac-web/src/real/RealWorkOrders.tsx', import.meta.url), 'utf8');

test('Alarm reverse association queries the Work Order owner by canonical source identity', () => {
  assert.match(workOrderApiSource, /if \(filter\.sourceDomain\) query\.set\('sourceDomain', filter\.sourceDomain\)/);
  assert.match(workOrderApiSource, /if \(filter\.sourceRef\) query\.set\('sourceRef', filter\.sourceRef\)/);
});

test('existing Alarm-origin Work Order flow preserves ALARM origin provenance', () => {
  assert.match(workOrderPageSource, /domain: sourceAlarm \? 'ALARM' : 'MANUAL'/);
  assert.match(workOrderPageSource, /resourceId: sourceAlarm \|\| `web:\$\{crypto\.randomUUID\(\)\}`/);
  assert.match(workOrderPageSource, /relationship: 'ORIGIN'/);
});

test('EQUIPMENT remains a first-class Work Order source domain distinct from ASSET', () => {
  assert.match(workOrderApiSource, /z\.enum\(\['MANUAL', 'ALARM', 'ASSET', 'EQUIPMENT', 'INVESTIGATION', 'EXTERNAL'\]\)/);
});
