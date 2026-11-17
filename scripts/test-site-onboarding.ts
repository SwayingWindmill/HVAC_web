import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';

import {
  ASSET_RELATIONSHIP_ROLES, SEMANTIC_ROLES, SUBJECT_BINDING_LIFECYCLE, buildOnboardingSql, checkSiteFile, expandPoints, planOnboarding, pointQuality, pointStatus, presenceApplicability, siteStateQuery, updateWhenChanged,
} from './site-onboarding.mjs';

import type { OnboardingAsset, OnboardingDevice, OnboardingPoint, OnboardingProfile, OnboardingState, PlanItem } from './site-onboarding.mjs';

const songyaxuan = JSON.parse(readFileSync(new URL('../deploy/sites/songyaxuan.site.json', import.meta.url), 'utf8')) as OnboardingProfile;
const songyaxuanAlarmRules = JSON.parse(readFileSync(new URL('../deploy/sites/songyaxuan.alarm-rules.json', import.meta.url), 'utf8'));
const copy = <T>(value: T): T => structuredClone(value);

// 松雅轩 has no room probe yet, so its manifest registers no Optimization. The Optimization checks run on a
// copy with an active room reading and the section the manifest had before the probe was found missing.
const optimized = (() => {
  const profile = copy(songyaxuan);
  const room = profile.devices.find((device) => device.sourceKey === 'room')?.points[0];
  if (!room) throw new Error('room point missing');
  delete room.status;
  room.from = ['temperature', 't12'];
  profile.intelligence.optimization = {"cron": "20 * * * *", "enabled": true, "model": "hvac-recommendation-v1", "policy": {"code": "chilled_water_supply_cost", "objective": "COST", "dispatchMode": "SHADOW", "horizon": "DAY_AHEAD", "horizonMinutes": 1440, "granularity": "15MIN", "constraints": {"safety": {"supplyTempMinC": 5, "supplyTempMaxC": 8, "maxSupplyTempStepC": 1}, "comfort": {"zoneTempMinC": 22, "zoneTempMaxC": 26}, "manualLocks": {"resources": []}, "inputMapping": {"supplyTemperatureKey": "supply_water_temperature", "zoneTemperatureKey": "room_temperature"}, "responseModel": {"dailyEnergyDeltaKWhPerSupplyTempC": -60, "zoneTempDeltaCPerSupplyTempC": 0.3, "energyUncertaintyP90KWh": 20, "zoneTempUncertaintyP90C": 0.2}, "maintenanceConstraints": {"outOfService": []}}, "responseModelSource": "人工设定", "note": "优化策略是租户共用的（同一租户只有一份生效策略）。本站和模拟冷站的室内温度读数都是 room_temperature；接入不改租户共用的策略，validate 把不一致列为「需要人决定」，策略一致之前不登记本站的模型部署和定时优化。"}};
  return profile;
})();

function bindingSection(sql: string): string {
  const start = sql.indexOf('INSERT INTO core_registry.point_subject_bindings');
  assert.notEqual(start, -1, 'point_subject_bindings insert is missing');
  return sql.slice(start, sql.indexOf('COMMIT;', start));
}

function pointRows(sql: string): string[] {
  const start = sql.indexOf('INSERT INTO core_registry.telemetry_points');
  return sql.slice(start, sql.indexOf('ON CONFLICT', start)).split('\n').filter((line) => line.startsWith('('));
}

test('each DESCRIBES and CONTROLS binding carries its point status and command metadata', () => {
  const { registry, telemetry } = buildOnboardingSql(songyaxuan);
  const points = pointRows(registry);
  const observedPoints = points.filter((row) => !row.includes("'COMMAND'"));
  const commandPoints = points.filter((row) => row.includes("'COMMAND'"));
  const describes = bindingSection(registry).split('\n').filter((line) => line.includes("'DESCRIBES'"));
  const controls = bindingSection(registry).split('\n').filter((line) => line.includes("'CONTROLS'"));
  assert.equal(describes.length, observedPoints.length);
  assert.equal(controls.length, commandPoints.length);
  assert.equal(commandPoints.length, 41);
  const inactive = points.filter((row) => row.includes("'INACTIVE'"));
  assert.ok(inactive.length >= 2, 'the songyaxuan site file marks unwired channels INACTIVE');
  for (const [index, row] of observedPoints.entries()) {
    const status = row.includes("'INACTIVE'") ? 'INACTIVE' : 'ACTIVE';
    assert.match(describes[index], new RegExp(`'DESCRIBES', '${status}', clock_timestamp\\(\\), NULL`), `DESCRIBES binding ${index + 1} should be ${status}`);
  }
  for (const [index, row] of commandPoints.entries()) {
    assert.ok(row.includes(', true, '), `COMMAND point ${index + 1} must be writable`);
    assert.match(controls[index], /'CONTROLS', 'ACTIVE', clock_timestamp\(\), NULL/);
    assert.ok(!row.includes('"interlocks"'), 'raw interlocks must be compiled into interlockChecks');
  }
  assert.ok(commandPoints.some((row) => row.includes('"interlockChecks"') && row.includes('"coilAddress":3')));
  assert.ok(commandPoints.some((row) => row.includes('"coilAddress":16')));
  const freshnessSection = telemetry.slice(telemetry.indexOf('INSERT INTO telemetry_runtime.freshness_policies'));
  assert.ok(!freshnessSection.includes('start_unit_sequence'), 'COMMAND points must not enter freshness_policies');
  const t14 = observedPoints.findIndex((row) => row.includes("'t14'"));
  assert.notEqual(t14, -1);
  assert.match(describes[t14], /'DESCRIBES', 'ACTIVE'/);
  const t20 = observedPoints.findIndex((row) => row.includes("'t20'"));
  assert.notEqual(t20, -1);
  assert.match(describes[t20], /'DESCRIBES', 'INACTIVE'/);
});

test('a rerun follows the site file instead of forcing ACTIVE', () => {
  const section = bindingSection(buildOnboardingSql(songyaxuan).registry);
  const conflict = section.slice(section.indexOf('ON CONFLICT'));
  assert.ok(!conflict.includes("status='ACTIVE'"), 'binding upsert must not hard-code ACTIVE');
  const lifecycle = new Map<string, string>(SUBJECT_BINDING_LIFECYCLE);
  assert.equal(lifecycle.get('status'), 'EXCLUDED.status');
  assert.match(lifecycle.get('valid_to') ?? '', /^CASE WHEN EXCLUDED\.status = 'ACTIVE' THEN NULL/);
  assert.match(lifecycle.get('valid_from') ?? '', /^CASE WHEN EXCLUDED\.status = 'ACTIVE' AND/);
  for (const [column, value] of SUBJECT_BINDING_LIFECYCLE) assert.ok(conflict.includes(`${column}=${value}`), `binding upsert sets ${column}`);
});

// Every ON CONFLICT ... DO UPDATE in a generated script, with the table it belongs to.
function upserts(sql: string) {
  return [...sql.matchAll(/INSERT INTO (\w+)\.(\w+) \([^)]*\)[\s\S]*?ON CONFLICT \([^)]*\) (DO UPDATE[\s\S]*?|DO NOTHING);/g)]
    .map(([, schema, table, action]) => ({ schema, table, action }))
    .filter(({ action }) => action.startsWith('DO UPDATE'));
}

const POLICY_TABLES = new Set(['presence_policies', 'freshness_policies']);

test('a rerun with an unchanged site file updates no row', () => {
  const { registry, telemetry, energy } = buildOnboardingSql(songyaxuan);
  const all = [...upserts(registry), ...upserts(telemetry), ...upserts(energy)];
  assert.ok(all.length >= 15, `expected every upsert to be found, got ${all.length}`);
  for (const { schema, table, action } of all) {
    assert.match(action, new RegExp(`\\nWHERE \\(${table}\\.[\\s\\S]*\\) IS DISTINCT FROM \\(`), `${schema}.${table} must update only rows whose values change`);
    const set = action.slice('DO UPDATE SET '.length, action.indexOf('\nWHERE'));
    if (schema === 'core_registry') assert.ok(set.includes(`revision=${table}.revision + 1`), `${table} must count a real change in revision`);
    else if (POLICY_TABLES.has(table)) assert.ok(set.includes(`policy_revision=${table}.policy_revision + 1`), `${table} must count a real change in policy_revision`);
    else assert.ok(!/(^|[ ,_])revision=/.test(set), `${schema}.${table} has no revision`);
    assert.ok(set.endsWith('updated_at=clock_timestamp()'), `${schema}.${table} stamps updated_at only with a change`);
  }
  const tables = new Set(all.map(({ schema, table }) => `${schema}.${table}`));
  for (const table of ['core_registry.telemetry_points', 'core_registry.point_subject_bindings', 'core_registry.devices', 'core_registry.tariffs', 'telemetry_runtime.freshness_policies']) assert.ok(tables.has(table), `${table} upsert is checked`);
});

test('the change guard compares exactly the columns it sets', () => {
  const action = updateWhenChanged('devices', [['code', 'EXCLUDED.code'], ['status', "'ACTIVE'"]]);
  assert.equal(action, `DO UPDATE SET code=EXCLUDED.code, status='ACTIVE', revision=devices.revision + 1, updated_at=clock_timestamp()
WHERE (devices.code, devices.status) IS DISTINCT FROM (EXCLUDED.code, 'ACTIVE')`);
  assert.equal(updateWhenChanged('observation_coverage', [['available', 'true']], { revision: false }), `DO UPDATE SET available=true, updated_at=clock_timestamp()
WHERE (observation_coverage.available) IS DISTINCT FROM (true)`);
  assert.equal(updateWhenChanged('presence_policies', [['online_within_seconds', 'EXCLUDED.online_within_seconds']], { revision: 'policy_revision' }), `DO UPDATE SET online_within_seconds=EXCLUDED.online_within_seconds, policy_revision=presence_policies.policy_revision + 1, updated_at=clock_timestamp()
WHERE (presence_policies.online_within_seconds) IS DISTINCT FROM (EXCLUDED.online_within_seconds)`);
});

test('a policy that changes gets a new policy_revision; the inserted revision stays 1', () => {
  const { telemetry } = buildOnboardingSql(songyaxuan);
  const policies = upserts(telemetry).filter(({ table }) => POLICY_TABLES.has(table));
  assert.deepEqual(policies.map(({ table }) => table).sort(), ['freshness_policies', 'presence_policies']);
  for (const { table, action } of policies) {
    const guarded = action.slice(action.indexOf('\nWHERE'));
    assert.ok(!guarded.includes('policy_revision'), `${table} compares policy values, not its revision`);
  }
  assert.match(telemetry, /INSERT INTO telemetry_runtime\.presence_policies [^\n]*\n\('[^']+', 1, /, 'a new presence policy starts at revision 1');
});

test('songyaxuan outdoor temperature carries its quality profile into the freshness policy', () => {
  const { telemetry } = buildOnboardingSql(songyaxuan);
  const row = telemetry.split('\n').find((line) => line.includes("'t15'"));
  if (!row) throw new Error('t15 freshness row missing');
  assert.match(row, /, -25, 50, '/);
  assert.match(row, /38\.4/);
  assert.match(row, /"flatlineSeconds":10800/);
  const inactive = telemetry.split('\n').find((line) => line.includes("'t20'"));
  assert.ok(inactive);
  assert.doesNotMatch(inactive, /38\.4/);
});

test('songyaxuan cooling load carries probe accuracy and the header offset pair is stored', () => {
  const { telemetry } = buildOnboardingSql(songyaxuan);
  const cooling = telemetry.split('\n').find((line) => line.includes("'instant_cooling_capacity'"));
  if (!cooling) throw new Error('cooling freshness row missing');
  assert.match(cooling, /"kind":"cooling"/);
  assert.match(cooling, /"supplyAccuracyK":0\.1/);
  assert.match(cooling, /"returnAccuracyK":0\.1/);
  assert.match(cooling, /"flowAccuracyFraction":0\.02/);
  assert.match(cooling, /"powerAccuracyFraction":0\.01/);
  assert.match(cooling, /"lowConfidenceRelative":0\.1/);
  assert.match(telemetry, /probe_pairs/);
  assert.match(telemetry, /'header-chw'/);
  assert.match(telemetry, /'chiller-a-chw'/);
  assert.match(telemetry, /'chiller-b-chw'/);
  assert.match(telemetry, /'chiller-c-chw'/);
  assert.match(telemetry, /'chiller-d-chw'/);
  assert.equal(checkSiteFile(songyaxuan, songyaxuanAlarmRules).length, 0);
});

test('a confirmed chiller nameplate is written and an unconfirmed pump power is not', () => {
  const profile = copy(songyaxuan);
  for (const asset of profile.assets) delete asset.nameplate;
  profile.physicsChecks = [];
  profile.nameplateCatalog = {
    hitachi: { manufacturer: '日立', model: 'RCU180SC2', year: 2007, refrigerant: 'R22', ratedCapacityKw: 572, ratedPowerKw: 127.5, ratedCurrentA: 225 },
    pump: { manufacturer: '凯泉', speed: 'FIXED', role: 'UNKNOWN', unconfirmed: ['role', 'ratedPowerKw'], candidates: [{ model: 'YE3-180L-4', ratedPowerKw: 22, efficiency: 0.93 }] },
  };
  const chillerC = profile.assets.find((asset) => asset.code === 'chiller-c');
  const chwp1 = profile.assets.find((asset) => asset.code === 'chwp-1');
  if (!chillerC || !chwp1) throw new Error('expected assets missing');
  chillerC.nameplate = 'hitachi';
  chwp1.nameplate = 'pump';
  profile.physicsChecks = [{
    code: 'chiller-c-power', kind: 'POWER_NAMEPLATE', asset: 'chiller-c', aboveRatedFraction: 0.15,
    subject: { device: 'meter-uid3', point: 'power_total' },
    alarm: { title: 'C 机组功率超过铭牌', summary: 'C 机组电表功率超过铭牌输入功率 127.5 kW 的 15%。', severity: 'MAJOR' },
  }];
  assert.deepEqual(checkSiteFile(profile, songyaxuanAlarmRules), []);
  const { registry, telemetry } = buildOnboardingSql(profile);
  const chiller = registry.split('\n').find((line) => line.includes("'chiller-c'"));
  const pump = registry.split('\n').find((line) => line.includes("'chwp-1'"));
  assert.match(chiller ?? '', /, 572, 127\.5, '日立', 'RCU180SC2', /);
  assert.match(pump ?? '', /clock_timestamp\(\), clock_timestamp\(\), '[^']+', NULL, NULL, '凯泉', NULL, /);
  assert.match(pump ?? '', /"unconfirmed":\["role","ratedPowerKw"\]/);
  const power = telemetry.split('\n').find((line) => line.startsWith("('01a14000-4000-7000-8000-000000000005', 'power_total'"));
  assert.match(power ?? '', /"kind":"POWER_NAMEPLATE"/);
  assert.match(power ?? '', /"ratedPowerKw":127\.5/);
  assert.match(power ?? '', /"aboveRatedFraction":0\.15/);
});

test('songyaxuan nameplates are the photographed plates and the pump motors stay unassigned', () => {
  const { registry, telemetry } = buildOnboardingSql(songyaxuan);
  for (const code of ['chiller-a', 'chiller-b', 'chiller-c', 'chiller-d']) {
    const line = registry.split('\n').find((row) => row.includes(`'${code}'`));
    assert.match(line ?? '', /, 572, 127\.5, '日立', 'RCU180SC2',/);
  }
  for (const code of ['chwp-1', 'chwp-2', 'chwp-3', 'chwp-4', 'cwp-1', 'cwp-2', 'cwp-3', 'cwp-4']) {
    const line = registry.split('\n').find((row) => row.includes(`'${code}'`));
    assert.match(line ?? '', /NULL, NULL, '凯泉', NULL/);
    assert.match(line ?? '', /"role":"UNKNOWN"/);
  }
  const chillerALeaving = telemetry.split('\n').find((row) => row.startsWith("('01a14000-4000-7000-8000-000000000001', 't1'"));
  assert.match(chillerALeaving ?? '', /CHW_DIRECTION/);
  const copied = telemetry.split('\n').find((row) => row.startsWith("('01a14000-4000-7000-8000-000000000006', 'chw_leaving_temperature'"));
  assert.match(copied ?? '', /CHW_DIRECTION/);
  const chillerACondenser = telemetry.split('\n').find((row) => row.startsWith("('01a14000-4000-7000-8000-000000000001', 't6'"));
  assert.match(chillerACondenser ?? '', /CW_DIRECTION/);
  const inactiveWetBulb = telemetry.split('\n').find((row) => row.includes("'ambient_wet_bulb_temperature'"));
  assert.doesNotMatch(inactiveWetBulb ?? '', /WET_BULB/);
  const cooling = telemetry.split('\n').find((row) => row.includes("'instant_cooling_capacity'"));
  assert.match(cooling ?? '', /COP_CARNOT/);
  assert.doesNotMatch(cooling ?? '', /HEAT_BALANCE/);
});

test('point status defaults to ACTIVE and rejects unknown values', () => {
  assert.equal(pointStatus({ code: 'a' }), 'ACTIVE');
  assert.equal(pointStatus({ code: 'a', status: 'INACTIVE' }), 'INACTIVE');
  assert.equal(pointStatus({ code: 'a', status: 'RETIRED' }), 'RETIRED');
  assert.throws(() => pointStatus({ code: 'a', status: 'inactive' }), /expected ACTIVE, INACTIVE or RETIRED/);
});

// A database that already holds everything the songyaxuan manifest describes.
function completeState(profile: OnboardingProfile): OnboardingState {
  const synthesizedTotal = Boolean(profile.energy?.totalMeter && typeof profile.energy.totalMeter === 'object' && profile.energy.totalMeter.kind === 'sum-of-sub-meters');
  const state: OnboardingState = { site: 'ACTIVE', devices: {}, points: {}, assets: {}, pointSemanticRoles: {}, assetRelationships: [], schedules: {}, topology: true, synthesizedTotal, tariff: true, forecast: true };
  for (const match of buildOnboardingSql(profile).registry.matchAll(/'(01a14000-4000-7000-8000-[0-9a-f]{12})'/g)) state.devices[match[1]] = 'ACTIVE';
  for (const row of pointRows(buildOnboardingSql(profile).registry)) {
    const [, id] = row.match(/^\('([0-9a-f-]{36})'/) ?? [];
    if (id) state.points[id] = row.includes("'INACTIVE'") ? 'INACTIVE' : 'ACTIVE';
  }
  const assetIdByRef = new Map<string, string>();
  for (const asset of profile.assets) {
    state.assets[asset.id] = 'ACTIVE';
    assetIdByRef.set(asset.id, asset.id);
    assetIdByRef.set(asset.code, asset.id);
  }
  for (const [deviceOffset, device] of profile.devices.entries()) {
    for (const [pointIndex, point] of expandPoints(device, profile.meterPoints).entries()) {
      if (pointStatus(point) === 'ACTIVE' && point.semanticRole) {
        const pointId = profile.site.id.replace(
          /^([0-9a-f]{8})-[0-9a-f]{4}-7000-8000-[0-9a-f]{12}$/,
          (_, head) => `${head}-5000-7000-8000-${((deviceOffset + 1) * 0x10000 + pointIndex + 1).toString(16).padStart(12, '0')}`,
        );
        if (state.pointSemanticRoles) state.pointSemanticRoles[pointId] = point.semanticRole;
      }
    }
  }
  state.assetRelationships = (profile.assetRelationships ?? []).map((rel) => ({
    fromAssetId: assetIdByRef.get(rel.from) ?? rel.from,
    toAssetId: assetIdByRef.get(rel.to) ?? rel.to,
    role: rel.role,
  }));
  for (const { item } of planOnboarding(profile, { ...state, schedules: {}, policy: null, modelRevision: null, optimizationBinding: null, lowDeltaTRule: null })) {
    const code = item.match(/^调度定义 (.+)$/)?.[1];
    if (code) state.schedules[code] = { jobType: '', cron: '', enabled: true, payload: {} };
  }
  const { lowDeltaT } = profile.intelligence.diagnosis;
  const lowDeltaTAsset = profile.assets.find((asset) => asset.code === lowDeltaT.asset);
  const lowDeltaTDevice = profile.devices.find((device) => device.code === lowDeltaT.device);
  if (!lowDeltaTAsset || !lowDeltaTDevice) throw new Error('lowDeltaT asset or device missing');
  state.schedules[`chilled-water-low-delta-t-${profile.site.code}`] = {
    cron: lowDeltaT.cron, enabled: lowDeltaT.enabled,
    payload: { assetId: lowDeltaTAsset.id, deviceId: lowDeltaTDevice.id },
  };
  const { optimization } = profile.intelligence;
  if (optimization) {
    state.schedules[`supply-temperature-optimization-${profile.site.code}`] = { cron: optimization.cron, enabled: optimization.enabled, payload: {} };
    const { policy } = optimization;
    state.policy = { constraints: copy(policy.constraints), objective: policy.objective, dispatchMode: policy.dispatchMode, horizon: policy.horizon, horizonMinutes: policy.horizonMinutes, granularity: policy.granularity };
    state.modelRevision = '01910000-0000-7000-8000-80000000000a';
    state.optimizationBinding = { status: 'ACTIVE', enabled: true };
  }
  state.lowDeltaTRule = { revision: 1, minimumDeltaTC: 3.5, windowMinutes: 15 };
  return state;
}

const byStatus = (items: PlanItem[], status: string) => items.filter((item) => item.status === status).map((item) => item.item);

test('the songyaxuan manifest and its alarm rules pass the file check', () => {
  assert.deepEqual(checkSiteFile(songyaxuan, songyaxuanAlarmRules), []);
  assert.deepEqual(checkSiteFile(optimized, songyaxuanAlarmRules), []);
  const meterPoints = expandPoints(songyaxuan.devices.find((device) => device.code === 'meter-uid2'), songyaxuan.meterPoints);
  assert.equal(meterPoints.find((point) => point.code === 'active_energy_total')?.critical, true);
  assert.equal(meterPoints.find((point) => point.code === 'power_total')?.critical, true);
  assert.equal(meterPoints.find((point) => point.code === 'run_state')?.critical, undefined);
});

test('critical points must be active own readings and readingGaps must have positive ageSeconds', () => {
  const profile = copy(songyaxuan);
  const meter = profile.devices.find((device) => device.code === 'meter-uid2');
  const temp = profile.devices.find((device) => device.code === 'temperature-uid20');
  const meterChw = meter?.morePoints?.find((point) => point.code === 'chw_leaving_temperature');
  const temp20 = temp?.points.find((point) => point.code === 't20');
  const temp1 = temp?.points.find((point) => point.code === 't1');
  if (!meterChw || !temp20 || !temp1) throw new Error('expected points missing');
  meterChw.critical = true;
  temp20.critical = true;
  temp1.critical = 'yes';
  const rules = copy(songyaxuanAlarmRules);
  rules.readingGaps = { title: '', summary: '', severity: 'UNKNOWN', ageSeconds: 0 };
  const problems = checkSiteFile(profile, rules).join('\n');
  assert.match(problems, /meter-uid2\/chw_leaving_temperature: critical point/);
  assert.match(problems, /temperature-uid20\/t20: critical point/);
  assert.match(problems, /temperature-uid20\/t1: critical must be true or false/);
  assert.match(problems, /readingGaps\.title/);
  assert.match(problems, /readingGaps\.summary/);
  assert.match(problems, /readingGaps\.ageSeconds/);
  assert.match(problems, /readingGaps\.severity/);

  const missingGaps = copy(songyaxuanAlarmRules);
  delete missingGaps.readingGaps;
  assert.match(checkSiteFile(songyaxuan, missingGaps).join('\n'), /readingGaps is required/);
});

test('the file check names alarm inputs, keys and fields that do not match the site', () => {
  const profile = copy(optimized);
  if (!profile.intelligence.optimization) throw new Error('optimization missing');
  profile.intelligence.optimization.policy.constraints.inputMapping.zoneTemperatureKey = 'temperature';
  delete profile.intelligence.optimization.policy.responseModelSource;
  profile.intelligence.diagnosis.lowDeltaT.asset = 'no-such-asset';
  const rules = copy(songyaxuanAlarmRules);
  rules.rules[0].raise = { op: 'AND', children: [{ op: 'COMPARE', input: 'meter-uid5/no_such_point' }, { op: 'COMPARE', input: 'no-such-device/run_state' }] };
  const problems = checkSiteFile(profile, rules).join('\n');
  assert.match(problems, /meter-uid5\/no_such_point: meter-uid5 has no active point no_such_point/);
  assert.match(problems, /no-such-device\/run_state: no device no-such-device/);
  assert.match(problems, /zoneTemperatureKey temperature is on 0 active points/);
  assert.match(problems, /responseModelSource/);
  assert.match(problems, /diagnosis.lowDeltaT: no asset no-such-asset/);
  assert.match(checkSiteFile(songyaxuan, null).join('\n'), /alarm rules file songyaxuan.alarm-rules.json was not found/);
});

test('validate on a complete site lists only what a person still decides', () => {
  const items = planOnboarding(songyaxuan, completeState(songyaxuan));
  assert.deepEqual(byStatus(items, 'missing'), []);
  assert.deepEqual(byStatus(items, 'differs'), []);
  assert.deepEqual(byStatus(items, 'decide'), ['告警规则文件 songyaxuan.alarm-rules.json', '低温差诊断未启用']);
  assert.deepEqual(byStatus(planOnboarding(optimized, completeState(optimized)), 'decide'), ['告警规则文件 songyaxuan.alarm-rules.json', '低温差诊断未启用', '响应系数是人工设定']);
});

test('the disabled low ΔT hint names the revision in force, not a number in the note', () => {
  const state = completeState(songyaxuan);
  state.lowDeltaTRule = { revision: 2, minimumDeltaTC: 1.5, windowMinutes: 15 };
  const hint = (items: PlanItem[]) => items.find((item) => item.item === '低温差诊断未启用')?.detail ?? '';
  assert.match(hint(planOnboarding(songyaxuan, state)), /（现在生效的是第 2 版：温差低于 1\.5 K 持续 15 分钟）$/);
  assert.doesNotMatch(songyaxuan.intelligence.diagnosis.lowDeltaT.note, /第 \d+ 版|\d K/, 'the note leaves the threshold to the database');
  state.lowDeltaTRule = null;
  assert.match(hint(planOnboarding(songyaxuan, state)), /（租户还没有规则版本，apply 建第 1 版：1\.5 K、15 分钟）$/);
});

test('a Tenant policy that disagrees blocks the Optimization deployment and is left for a person', () => {
  const state = completeState(optimized);
  if (!state.policy) throw new Error('policy missing');
  state.policy.constraints.inputMapping.zoneTemperatureKey = 'temperature';
  state.optimizationBinding = null;
  delete state.schedules['supply-temperature-optimization-songyaxuan'];
  delete state.assets[optimized.assets[optimized.assets.length - 1].id];
  const items = planOnboarding(optimized, state);
  assert.deepEqual(byStatus(items, 'missing'), ['资产 17 台']);
  const decide = items.filter((item) => item.status === 'decide');
  assert.match(decide.find((item) => item.item === '优化策略和清单不一致')?.detail ?? '', /zoneTemperatureKey: 清单 "room_temperature"，现在 "temperature"/);
  assert.ok(decide.some((item) => item.item === '租户策略的 zoneTemperatureKey 是 temperature'));
  assert.ok(decide.some((item) => item.item === '本站的模型部署'));
  assert.ok(decide.some((item) => item.item === '调度定义 supply-temperature-optimization-songyaxuan'));
});

test('a Tenant without a policy or rule gets them from the manifest', () => {
  const state = completeState(optimized);
  state.policy = null;
  state.lowDeltaTRule = null;
  state.optimizationBinding = null;
  delete state.schedules['supply-temperature-optimization-songyaxuan'];
  const missing = byStatus(planOnboarding(optimized, state), 'missing');
  assert.deepEqual(missing, ['低温差诊断规则版本', '优化策略', '本站的模型部署', '调度定义 supply-temperature-optimization-songyaxuan']);
});

test('apply never rewrites what the Tenant shares and registers Optimization only under an agreeing policy', () => {
  const { intelligence } = buildOnboardingSql(optimized);
  assert.ok(!/UPDATE\s+core_registry\.(optimization_polic|fdd_rule_revisions)|ON CONFLICT[^;]*DO UPDATE SET[^;]*constraints/.test(intelligence), 'shared policy and rule are never updated');
  assert.match(intelligence, /INSERT INTO core_registry\.fdd_rule_revisions[\s\S]*?WHERE NOT EXISTS \(SELECT 1 FROM core_registry\.fdd_rule_revisions WHERE tenant_id = '[^']+' AND rule_code = 'CHILLED_WATER_LOW_DELTA_T'\);/);
  assert.match(intelligence, /INSERT INTO core_registry\.optimization_policies[\s\S]*?WHERE NOT EXISTS \(SELECT 1 FROM core_registry\.optimization_policies WHERE tenant_id = '[^']+' AND resource_type = 'HVAC' AND status = 'ACTIVE'\);/);
  for (const table of ['ai_deployment_bindings', 'schedule_definitions (schedule_id, schedule_code, schedule_name, tenant_id, site_id, job_type, schedule_type, cron_expression, cron_format_version, timezone, misfire_policy, timeout_seconds, payload_template, enabled, next_fire_at)\nSELECT \'01a14000-7b00']) {
    const start = intelligence.indexOf(`INSERT INTO core_registry.${table}`);
    assert.notEqual(start, -1, table);
    const statement = intelligence.slice(start, intelligence.indexOf(';\n', start));
    assert.match(statement, /(?:WHERE|AND) EXISTS \(SELECT 1 FROM \(SELECT pv\.constraints/, `${table} waits for the policy to agree`);
    assert.ok(statement.includes('"zoneTemperatureKey":"room_temperature"'), `${table} compares against the manifest's constraints`);
  }
  assert.match(intelligence, /'chilled-water-low-delta-t-songyaxuan'[^\n]*'FDD_EVALUATION'[^\n]*, false, /, 'the diagnosis schedule follows enabled: false');
  for (const statement of intelligence.split(/;\n/).filter((sql) => sql.includes('DO UPDATE SET'))) {
    assert.match(statement, /version=schedule_definitions\.version \+ 1/);
    assert.match(statement, /\nWHERE \(schedule_definitions\.schedule_name, schedule_definitions\.cron_expression, schedule_definitions\.payload_template, schedule_definitions\.enabled\) IS DISTINCT FROM/);
  }
});

test('copied points mark quality_checks.copied and copy-only or inactive devices set presence_applicability NOT_APPLICABLE', () => {
  const devicesByCode = new Map<string, OnboardingDevice>(songyaxuan.devices.map((device) => [device.code, { ...device, points: expandPoints(device, songyaxuan.meterPoints) }]));
  const meterUid2 = devicesByCode.get('meter-uid2');
  const ownEnergy = meterUid2?.points.find((point) => point.code === 'active_energy_total');
  const rawCopiedChw = meterUid2?.points.find((point) => point.code === 'chw_leaving_temperature');
  if (!meterUid2 || !ownEnergy || !rawCopiedChw) throw new Error('expected meterUid2 points missing');
  const copiedChw: OnboardingPoint = { ...rawCopiedChw, device: meterUid2 };
  assert.equal(pointQuality({ ...ownEnergy, device: meterUid2 }, songyaxuan, devicesByCode).checks, null);
  assert.match(pointQuality(copiedChw, songyaxuan, devicesByCode).checks ?? '', /"copied":true/);

  const chwpMain = devicesByCode.get('chwp-main');
  const rawCopiedFlow = chwpMain?.points.find((point) => point.code === 'flow_rate');
  if (!chwpMain || !rawCopiedFlow) throw new Error('expected chwpMain flow missing');
  const copiedFlow: OnboardingPoint = { ...rawCopiedFlow, device: chwpMain };
  assert.equal(pointQuality(copiedFlow, songyaxuan, devicesByCode).checks, '\'{"copied":true}\'::jsonb');

  for (const device of songyaxuan.devices) {
    const expected = ['cooling-tower', 'outdoor', 'room'].includes(device.code) ? 'NOT_APPLICABLE' : 'APPLICABLE';
    assert.equal(presenceApplicability(expandPoints(device, songyaxuan.meterPoints)), expected, `${device.code} applicability`);
  }
  const optimizedRoom = optimized.devices.find((device) => device.code === 'room');
  assert.equal(presenceApplicability(expandPoints(optimizedRoom, optimized.meterPoints)), 'NOT_APPLICABLE');

  const { telemetry } = buildOnboardingSql(songyaxuan);
  const devicesSection = telemetry.slice(
    telemetry.indexOf('INSERT INTO telemetry_runtime.devices'),
    telemetry.indexOf('INSERT INTO telemetry_runtime.presence_policies'),
  );
  for (const device of songyaxuan.devices) {
    const expected = ['cooling-tower', 'outdoor', 'room'].includes(device.code) ? 'NOT_APPLICABLE' : 'APPLICABLE';
    assert.match(devicesSection, new RegExp(`\\('${device.id}', '[^']+', '[^']+', '${expected}', clock_timestamp\\(\\)\\)`), `${device.code} SQL row`);
  }
  assert.match(devicesSection, /presence_applicability=EXCLUDED\.presence_applicability/);
  assert.ok(!devicesSection.includes("presence_applicability='APPLICABLE'"), 'devices upsert must follow EXCLUDED.presence_applicability');
});

test('validate reads the database with one read-only SELECT', () => {
  const query = siteStateQuery(songyaxuan);
  assert.ok(query.startsWith('BEGIN READ ONLY;\nSELECT json_build_object('));
  assert.ok(query.endsWith('ROLLBACK;'));
  assert.ok(!/\b(INSERT|UPDATE|DELETE|TRUNCATE|ALTER|DROP)\b/i.test(query));
});

test('songyaxuan synthesizes total electricity from the four chiller sub-meters and maps meters to A/B/C/D chillers', () => {
  assert.deepEqual(songyaxuan.energy?.totalMeter, { kind: 'sum-of-sub-meters' });
  assert.deepEqual(songyaxuan.energy?.checkMeters, ['ammeter-total']);
  const meterTotal = songyaxuan.devices.find((device) => device.code === 'meter-total');
  if (!meterTotal) throw new Error('meter-total missing');
  for (const point of meterTotal.points) assert.equal(point.critical, undefined, `meter-total/${point.code} must not be critical`);

  const expectedAssetByMeter: Record<string, string> = {
    'meter-uid3': '01a14000-3000-7000-8000-000000000002',
    'meter-uid4': '01a14000-3000-7000-8000-000000000001',
    'meter-uid5': '01a14000-3000-7000-8000-000000000003',
    'meter-uid2': '01a14000-3000-7000-8000-000000000004',
  };
  for (const [code, assetId] of Object.entries(expectedAssetByMeter)) {
    assert.equal(songyaxuan.devices.find((device) => device.code === code)?.asset, assetId, `${code} asset binding`);
  }

  const { registry, energy } = buildOnboardingSql(songyaxuan);
  assert.match(energy, /INSERT INTO core_registry\.virtual_meters[\s\S]*?'site_total_electricity', '合成总电表'/);
  assert.match(energy, /INSERT INTO core_registry\.virtual_meter_sources/);
  assert.match(energy, /'01a14000-4000-7000-8000-000000000008'::uuid, '01a14000-5000-7000-8000-000000080002'::uuid, 'COUNTER', 'CHECK', 'IMPORT'/);
  assert.match(energy, /SET meter_role = 'CHECK'/);
  assert.match(energy, /"site_energy":\["01a14000-5000-7000-8000-000000030002","01a14000-5000-7000-8000-000000040002","01a14000-5000-7000-8000-000000050002","01a14000-5000-7000-8000-000000060002"\]/);
  assert.match(energy, /"site_load":"01a14000-5000-7000-8000-000000080001"/);
  assert.match(energy, /'15MIN', 'AVG', 'IDENTITY', NULL, 'STRICT'/);

  // Adding a point to meter-uid3 must not shift later devices' fallback point_subject_binding ids.
  const btuPoints = pointRows(registry).filter((row) => row.includes("'01a14000-4000-7000-8000-000000000009'"));
  assert.ok(btuPoints.length >= 1);
  assert.match(bindingSection(registry), /'01a14000-6300-7000-8000-000000090001'/);

  // A synthesized site without checkMeters sums sub-meter power with TOLERANT quality policy.
  const noCheck = copy(songyaxuan);
  delete noCheck.energy?.checkMeters;
  const noCheckEnergy = buildOnboardingSql(noCheck).energy;
  assert.match(noCheckEnergy, /"site_load":\["01a14000-5000-7000-8000-000000030001","01a14000-5000-7000-8000-000000040001","01a14000-5000-7000-8000-000000050001","01a14000-5000-7000-8000-000000060001"\]/);
  assert.match(noCheckEnergy, /'15MIN', 'AVG', 'IDENTITY', NULL, 'TOLERANT'/);

  // A site with a physical total meter binds single-point site_energy and supersedes any prior virtual meter.
  const physical = copy(songyaxuan);
  if (!physical.energy) throw new Error('physical energy missing');
  physical.energy.totalMeter = 'ammeter-total';
  delete physical.energy.checkMeters;
  const physicalEnergy = buildOnboardingSql(physical).energy;
  assert.match(physicalEnergy, /"site_energy":"01a14000-5000-7000-8000-000000080002"/);
  assert.match(physicalEnergy, /UPDATE core_registry\.virtual_meters[\s\S]*?SET status = 'SUPERSEDED'/);
});

test('concentrator voltage, rssi and status devices preserve ids and respect alarm/telemetry caps', () => {
  const diagnosticCodes = ['temperature-voltage-uid20', 'temperature-rssi-uid20', 'temperature-status-uid20'];
  const activeDevicesWithPoints = songyaxuan.devices.filter((device) =>
    expandPoints(device, songyaxuan.meterPoints).some((point) => pointStatus(point) === 'ACTIVE'));
  assert.ok(activeDevicesWithPoints.length <= 32, `active-point device count ${activeDevicesWithPoints.length} exceeds 32`);

  for (const code of diagnosticCodes) {
    const device = songyaxuan.devices.find((candidate) => candidate.code === code);
    assert.ok(device, `${code} missing`);
    const expanded = expandPoints(device, songyaxuan.meterPoints);
    const activeOwn = expanded.filter((point) => pointStatus(point) === 'ACTIVE' && !point.from && !point.critical);
    assert.ok(expanded.length <= 64, `${code} exceeds 64 points`);
    assert.ok(activeOwn.length <= 32, `${code} active non-critical own points (${activeOwn.length}) exceed 32`);
    const inactiveCodes = expanded.filter((point) => pointStatus(point) === 'INACTIVE').map((point) => point.code);
    assert.equal(inactiveCodes.length, 2, `${code} must keep channels 20 and 51 INACTIVE`);
    assert.ok(inactiveCodes.every((pointCode) => pointCode.endsWith('20') || pointCode.endsWith('51')));
  }
});

test('songyaxuan Node-RED envelope stays under edge byte cap and aligns with site manifest', () => {
  const envelopeSource = readFileSync(new URL('../edge/node-red/songyaxuan-platform-envelope.js', import.meta.url), 'utf8');
  assert.ok(Buffer.byteLength(envelopeSource, 'utf8') <= 12288, 'songyaxuan-platform-envelope.js exceeds 12288 byte governance cap');

  const flowContext = new Map<string, unknown>();
  const runEnvelope = (payload: Record<string, unknown>): {
    payload: {
      schemaVersion: string;
      gatewayId: string;
      payload: {
        devices: Array<{
          deviceId: string;
          deviceTimestamp: number;
          points: Array<{ code: string; value: number | string; quality: number; unit?: string }>;
        }>;
      };
    };
  } | null => {
    const fn = new vm.Script(`(function(msg, context) { ${envelopeSource}\n })`).runInThisContext() as (
      msg: { payload: Record<string, unknown> },
      context: { get: (key: string) => unknown; set: (key: string, value: unknown) => void },
    ) => ReturnType<typeof runEnvelope>;
    return fn({ payload }, {
      get: (key) => flowContext.get(key),
      set: (key, value) => { flowContext.set(key, value); },
    });
  };

  const ts = 1794643200000;
  for (const unit of ['ammeter-unit1', 'ammeter-unit2', 'ammeter-unit3', 'ammeter-unit4']) {
    runEnvelope({ [unit]: [{ ts, values: { powerTotal: 50, combinedActiveTotalElectricalEnergy: unit === 'ammeter-unit4' ? 77000 : 100 } }] });
  }
  runEnvelope({ waterflow: [{ ts, values: { flowMeter: 120, flowVelocity: 1.5 } }] });

  const temps: Record<string, number> = {};
  const volts: Record<string, number> = {};
  const rssis: Record<string, number> = {};
  const statuses: Record<string, number | string> = {
    online_count: 17,
    offline_count: 1,
    low_battery_count: 1,
    min_voltage_mv: 2650,
    min_rssi_dbm: -88,
  };
  for (const ch of [...Array.from({ length: 20 }, (_, i) => i + 1), 51, 52, 53, 54, 55]) {
    temps[`t${ch}`] = ch === 20 || ch === 51 || ch === 52 || ch === 8 ? 0 : ch === 18 ? 7.0 : ch === 16 ? 12.0 : 10 + ch / 10;
    volts[`v${ch}`] = ch === 8 || ch === 51 || ch === 52 ? 0 : ch === 1 ? 2650 : 3550;
    rssis[`rssi${ch}`] = ch === 8 || ch === 51 || ch === 52 ? 0 : ch === 1 ? -88 : -62;
    statuses[`status${ch}`] = ch === 8 || ch === 51 || ch === 52 ? 'OFFLINE' : 'ONLINE';
  }

  const out = runEnvelope({
    temperature: [{ ts, values: temps }],
    'temperature-voltage': [{ ts, values: volts }],
    'temperature-rssi': [{ ts, values: rssis }],
    'temperature-status': [{ ts, values: statuses }],
  });
  assert.ok(out, 'expected combined envelope');
  assert.equal(out.payload.schemaVersion, '2.0');

  const activeBySourceKey = new Map(
    songyaxuan.devices.map((device) => [
      device.sourceKey,
      new Map(expandPoints(device, songyaxuan.meterPoints).filter((p) => pointStatus(p) === 'ACTIVE').map((p) => [p.code, p])),
    ]),
  );

  const byDevice = new Map(out.payload.payload.devices.map((entry) => [entry.deviceId, entry]));
  for (const key of ['temperature', 'temperature-voltage', 'temperature-rssi', 'temperature-status', 'btu-calc', 'ammeter-unit1', 'cooling-tower', 'outdoor', 'room']) {
    assert.ok(byDevice.has(key), `missing ${key} in combined envelope`);
  }

  for (const deviceEntry of out.payload.payload.devices) {
    const manifestPoints = activeBySourceKey.get(deviceEntry.deviceId);
    assert.ok(manifestPoints, `unknown deviceId ${deviceEntry.deviceId} emitted by envelope`);
    for (const point of deviceEntry.points) {
      const def = manifestPoints.get(point.code);
      assert.ok(def, `inactive or unregistered point ${deviceEntry.deviceId}/${point.code}`);
      if (def.unit) assert.equal(point.unit, def.unit, `${deviceEntry.deviceId}/${point.code} unit mismatch`);
      else assert.equal(point.unit, undefined, `${deviceEntry.deviceId}/${point.code} unexpected unit`);
    }
  }

  const voltPoints = new Map(byDevice.get('temperature-voltage')?.points.map((p) => [p.code, p]));
  assert.equal(voltPoints.get('v8')?.quality, 1, 'offline probe voltage must carry quality=1');
  assert.equal(voltPoints.get('v1')?.quality, 0);
  assert.equal(voltPoints.get('v51'), undefined, 'empty slot 51 must not be reported');
  assert.equal(voltPoints.get('v52'), undefined, 'disabled spare channel 52 must not be reported');
  assert.equal(voltPoints.get('v55')?.quality, 0);
  const rssiPoints = new Map(byDevice.get('temperature-rssi')?.points.map((p) => [p.code, p]));
  assert.equal(rssiPoints.get('rssi8')?.quality, 1, 'offline probe rssi must carry quality=1');
  assert.equal(rssiPoints.get('rssi1')?.value, -88);
  const roomPoints = new Map(byDevice.get('room')?.points.map((p) => [p.code, p]));
  assert.equal(roomPoints.get('room_temperature')?.value, 15.3);
  assert.equal(roomPoints.get('banquet_room_temperature')?.value, 15.4);
  const towerPoints = byDevice.get('cooling-tower')?.points ?? [];
  const towerLeavingPoints = towerPoints.filter((p) => p.code === 'cooling_tower_leaving_temperature');
  assert.equal(towerLeavingPoints.length, 1, 'coexisting t55 and t12 must emit only one cooling_tower_leaving_temperature');
  assert.equal(towerLeavingPoints[0]?.value, 15.5, 'cooling_tower_leaving_temperature must prefer t55');
  const meter1Points = new Map(byDevice.get('ammeter-unit1')?.points.map((p) => [p.code, p]));
  assert.equal(meter1Points.get('cw_leaving_temperature'), undefined, 'offline t8 must not copy into ammeter-unit1');
  const statusPoints = new Map(byDevice.get('temperature-status')?.points.map((p) => [p.code, p]));
  assert.equal(statusPoints.get('status8')?.value, 'OFFLINE');
  assert.equal(statusPoints.get('status8')?.quality, 0);
  assert.equal(statusPoints.get('status51'), undefined, 'empty slot 51 status must not be reported');
  assert.equal(statusPoints.get('min_voltage_mv')?.unit, 'mV');
  assert.equal(statusPoints.get('min_rssi_dbm')?.unit, 'dBm');

  // Verify coexistence between t55 and t12: t12 fallback when t55 is offline, and t55 when t12 is offline.
  const fallbackOut = runEnvelope({
    temperature: [{ ts: ts + 60000, values: { ...temps, t55: 0, t12: 28.4 } }],
    'temperature-status': [{ ts: ts + 60000, values: { ...statuses, status55: 'OFFLINE', status12: 'ONLINE' } }],
  });
  assert.equal(
    fallbackOut?.payload.payload.devices.find((d) => d.deviceId === 'cooling-tower')?.points.find((p) => p.code === 'cooling_tower_leaving_temperature')?.value,
    28.4,
  );
  const primaryOut = runEnvelope({
    temperature: [{ ts: ts + 120000, values: { ...temps, t55: 26.7, t12: 0 } }],
    'temperature-status': [{ ts: ts + 120000, values: { ...statuses, status55: 'ONLINE', status12: 'OFFLINE' } }],
  });
  assert.equal(
    primaryOut?.payload.payload.devices.find((d) => d.deviceId === 'cooling-tower')?.points.find((p) => p.code === 'cooling_tower_leaving_temperature')?.value,
    26.7,
  );
});

test('songyaxuan.site.json aligns chiller controller sampleSeconds and includes et1010 run_state and emergency_stop_all', () => {
  interface SitePoint {
    code: string;
    pointType: string;
    valueType: string;
    writable?: boolean;
    sourceMetadata?: Record<string, unknown>;
  }
  interface SiteDevice {
    code?: string;
    sourceKey?: string;
    sampleSeconds?: number;
    points: SitePoint[];
  }
  const site = JSON.parse(readFileSync(new URL('../deploy/sites/songyaxuan.site.json', import.meta.url), 'utf8')) as { devices: SiteDevice[] };
  for (const code of ['ammeter-unit1', 'ammeter-unit2', 'ammeter-unit3', 'ammeter-unit4', 'ctrl-chiller-a', 'ctrl-chiller-b', 'ctrl-chiller-c', 'ctrl-chiller-d']) {
    const dev = site.devices.find((d) => d.code === code || d.sourceKey === code);
    assert.ok(dev, `missing device ${code}`);
    assert.equal(dev.sampleSeconds, 300, `${code} sampleSeconds must be 300`);
  }
  const et1010 = site.devices.find((d) => d.sourceKey === 'et1010');
  assert.ok(et1010, 'missing et1010 device');
  const runStatePt = et1010.points.find((p) => p.code === 'run_state');
  assert.equal(runStatePt?.pointType, 'STATE');
  assert.equal(runStatePt?.valueType, 'STRING');
  const emergencyPt = et1010.points.find((p) => p.code === 'emergency_stop_all');
  assert.equal(emergencyPt?.pointType, 'COMMAND');
  assert.equal(emergencyPt?.valueType, 'BOOLEAN');
  assert.equal(emergencyPt?.writable, true);
  assert.equal(emergencyPt?.sourceMetadata?.capability, 'STOP');
  assert.equal(emergencyPt?.sourceMetadata?.capabilityRevision, 'capability:stop:v1');
  assert.equal(emergencyPt?.sourceMetadata?.feedbackPointKey, 'run_state');
  assert.equal(emergencyPt?.sourceMetadata?.actuationMode, 'EMERGENCY_DO_RELEASE');
  assert.equal(emergencyPt?.sourceMetadata?.channel, 'all-do');
});

test('songyaxuan-platform-envelope applies chiller power hysteresis and et1010 run_state', () => {
  const script = readFileSync(new URL('../edge/node-red/songyaxuan-platform-envelope.js', import.meta.url), 'utf8');
  const store = new Map<string, unknown>();
  const runEnvelope = (payload: Record<string, Array<{ ts: number; values: Record<string, unknown> }>>) => {
    const ctx = {
      msg: { payload },
      context: {
        get: (key: string) => store.get(key),
        set: (key: string, value: unknown) => {
          store.set(key, value);
        },
      },
      Date,
      Math,
    };
    const out = vm.runInNewContext(`(() => { ${script} })()`, ctx) as {
      payload: { payload: { devices: Array<{ deviceId: string; points: Array<{ code: string; value: unknown }> }> } };
    };
    return out.payload.payload.devices;
  };
  const readPoint = (devices: Array<{ deviceId: string; points: Array<{ code: string; value: unknown }> }>, deviceId: string, code: string) =>
    devices.find((d) => d.deviceId === deviceId)?.points.find((p) => p.code === code)?.value;

  const powerSteps: Array<[number, string]> = [
    [0, 'STOPPED'],
    [8, 'STOPPED'],
    [12.75, 'STOPPED'],
    [13, 'RUNNING'],
    [8, 'RUNNING'],
    [5, 'STOPPED'],
    [8, 'STOPPED'],
    [13, 'RUNNING'],
    [4.8, 'STOPPED'],
  ];
  let ts = 1_760_000_000_000;
  for (const [powerTotal, expected] of powerSteps) {
    ts += 1000;
    const devices = runEnvelope({
      'ammeter-unit4': [{ ts, values: { powerTotal, combinedActiveTotalElectricalEnergy: 100 } }],
    });
    assert.equal(readPoint(devices, 'ammeter-unit4', 'run_state'), expected, `ammeter-unit4 at ${powerTotal}kW`);
    assert.equal(readPoint(devices, 'ctrl-chiller-a', 'run_state'), expected, `ctrl-chiller-a at ${powerTotal}kW`);
  }

  const etStopped = runEnvelope({ et1010: [{ ts: ts + 1000, values: { u11: 0, u12: 0, u13: 0 } }] });
  assert.equal(etStopped.filter((d) => d.deviceId === 'et1010').length, 1);
  assert.equal(readPoint(etStopped, 'et1010', 'run_state'), 'STOPPED');
  assert.equal(readPoint(etStopped, 'ctrl-chwp-1', 'run_state'), 'STOPPED');

  const etRunning = runEnvelope({ et1010: [{ ts: ts + 2000, values: { u11: 1, u12: 0, u13: 0 } }] });
  assert.equal(etRunning.filter((d) => d.deviceId === 'et1010').length, 1);
  assert.equal(readPoint(etRunning, 'et1010', 'run_state'), 'RUNNING');
  assert.equal(readPoint(etRunning, 'ctrl-chwp-1', 'run_state'), 'RUNNING');

  const etPulseOnly = runEnvelope({ et1010: [{ ts: ts + 3000, values: { u14: 0, u51: 0 } }] });
  assert.equal(etPulseOnly.filter((d) => d.deviceId === 'et1010').length, 1);
  assert.equal(readPoint(etPulseOnly, 'et1010', 'run_state'), undefined);
});

test('feederAuxiliaryPowerKw, point running override, stabilizeSeconds and flatlineStabilizeSeconds compile and validate', () => {
  const { registry, telemetry } = buildOnboardingSql(songyaxuan);
  const chillerCPower = telemetry.split('\n').find((line) => line.startsWith("('01a14000-4000-7000-8000-000000000003', 'power_total'"));
  const chillerAPower = telemetry.split('\n').find((line) => line.startsWith("('01a14000-4000-7000-8000-000000000006', 'power_total'"));
  assert.match(chillerCPower ?? '', /"ratedPowerKw":157\.5/);
  assert.doesNotMatch(chillerCPower ?? '', /"stabilizeSeconds":/);
  assert.match(chillerAPower ?? '', /"ratedPowerKw":127\.5/);
  assert.doesNotMatch(chillerAPower ?? '', /"stabilizeSeconds":/);

  const chillerCAsset = registry.split('\n').find((line) => line.includes("'chiller-c'"));
  assert.match(chillerCAsset ?? '', /"feederAuxiliaryPowerKw":30/);
  const chillerCMeterPowerRow = registry.split('\n').find((line) => line.includes("'01a14000-4000-7000-8000-000000000003'") && line.includes("'power_total'"));
  assert.match(chillerCMeterPowerRow ?? '', /"ratedPowerKw":127\.5/);
  assert.match(chillerCMeterPowerRow ?? '', /"feederAuxiliaryPowerKw":30/);

  const devicesByCode = new Map<string, OnboardingDevice>(
    songyaxuan.devices.map((device) => [device.code, { ...device, points: expandPoints(device, songyaxuan.meterPoints) }]),
  );
  const btu = devicesByCode.get('btu-calc');
  assert.ok(btu);
  const coolCap = btu.points.find((candidate) => candidate.code === 'instant_cooling_capacity');
  assert.ok(coolCap);
  const copChecks = pointQuality({ ...coolCap, device: btu }, songyaxuan, devicesByCode).checks ?? '';
  assert.match(copChecks, /"kind":"COP_CARNOT"/);
  assert.match(copChecks, /"stabilizeSeconds":600/);
  assert.match(copChecks, /"deviceId":"01a14000-4000-7000-8000-000000000003","key":"run_state","equals":"RUNNING"/);
  const temp = devicesByCode.get('temperature-uid20');
  assert.ok(temp);
  const findTemp = (code: string) => {
    const pt = temp.points.find((candidate) => candidate.code === code);
    if (!pt) throw new Error(`missing temperature-uid20/${code}`);
    return pointQuality({ ...pt, device: temp }, songyaxuan, devicesByCode);
  };

  const t1Checks = findTemp('t1').checks ?? '';
  assert.match(t1Checks, /"flatlineStabilizeSeconds":600/);
  assert.match(t1Checks, /"stabilizeSeconds":600/);
  assert.match(t1Checks, /"running":\[\{"deviceId":"01a14000-4000-7000-8000-000000000006","key":"run_state","equals":"RUNNING"\}\]/);
  assert.doesNotMatch(t1Checks, /01a14000-4000-7000-8000-000000000003/);

  const t3Checks = findTemp('t3').checks ?? '';
  assert.match(t3Checks, /"running":\[\{"deviceId":"01a14000-4000-7000-8000-000000000003","key":"run_state","equals":"RUNNING"\}\]/);
  assert.doesNotMatch(t3Checks, /01a14000-4000-7000-8000-000000000006/);

  const t16Checks = findTemp('t16').checks ?? '';
  assert.match(t16Checks, /"flatlineStabilizeSeconds":600/);
  assert.match(t16Checks, /01a14000-4000-7000-8000-000000000003/);
  assert.match(t16Checks, /01a14000-4000-7000-8000-000000000006/);

  for (const device of devicesByCode.values()) {
    for (const point of device.points) {
      if (point.pointType === 'COMMAND') continue;
      const quality = pointQuality({ ...point, device }, songyaxuan, devicesByCode);
      if (quality.checks) {
        const rawJson = quality.checks.slice(1, quality.checks.lastIndexOf("'::jsonb")).replaceAll("''", "'");
        assert.ok(Buffer.byteLength(rawJson, 'utf8') <= 4096, `${device.code}/${point.code} exceeds 4096 bytes`);
      }
    }
  }

  const invalid = copy(songyaxuan);
  const catalogPlate = invalid.nameplateCatalog?.['hitachi-rcu180sc2-2007-unit-feeder'];
  if (!catalogPlate) throw new Error('unit-feeder catalog entry missing');
  catalogPlate.feederAuxiliaryPowerKw = -5;
  if (invalid.qualityProfiles?.['chilled-water']) {
    invalid.qualityProfiles['chilled-water'].flatlineStabilizeSeconds = -1;
  }
  if (invalid.physicsChecks?.[0]) {
    invalid.physicsChecks[0].stabilizeSeconds = -10;
  }
  const copCheck = invalid.physicsChecks?.find((c) => c.code === 'plant-cop-carnot');
  if (copCheck) {
    delete (copCheck as Record<string, unknown>).running;
  }
  const invalidTemp = invalid.devices.find((d) => d.code === 'temperature-uid20')?.points.find((p) => p.code === 't1');
  if (!invalidTemp) throw new Error('temperature-uid20/t1 missing');
  invalidTemp.running = [{ device: 'no-such-device', point: 'run_state', equals: 'RUNNING' }];

  const invalidRules = copy(songyaxuanAlarmRules);
  invalidRules.deviceUntrusted = { ...invalidRules.deviceUntrusted, minPoints: 1, durationSeconds: -1, reasons: ['NOT_A_REASON'] };
  const problems = checkSiteFile(invalid, invalidRules).join('\n');
  assert.match(problems, /feederAuxiliaryPowerKw/);
  assert.match(problems, /flatlineStabilizeSeconds/);
  assert.match(problems, /physics check .*stabilizeSeconds must be a non-negative integer/);
  assert.match(problems, /plant-cop-carnot: stabilizeSeconds requires running criteria/);
  assert.match(problems, /temperature-uid20\/t1: running names unknown device no-such-device/);
  assert.match(problems, /deviceUntrusted\.minPoints/);
  assert.match(problems, /deviceUntrusted\.durationSeconds/);
  assert.match(problems, /deviceUntrusted\.reasons/);
});

test('onboarding converges presence signal types, optimistic siteRevision lock, and audit/outbox emission', () => {
  const query = siteStateQuery(songyaxuan);
  assert.match(query, /'siteRevision', \(SELECT revision FROM core_registry\.sites WHERE id = /);

  const base = buildOnboardingSql(songyaxuan);
  assert.match(base.telemetry, /ARRAY\['SOURCE_ACTIVITY', 'EXPLICIT_CONNECT', 'EXPLICIT_DISCONNECT'\]::text\[\]/);
  assert.match(base.registry, /CREATE TEMP TABLE _onboarding_before /);
  assert.match(base.registry, /CREATE TEMP TABLE _onboarding_changes ON COMMIT DROP AS/);
  assert.match(base.registry, /INSERT INTO core_registry\.registry_audit_facts/);
  assert.match(base.registry, /INSERT INTO core_registry\.domain_outbox_events/);
  assert.match(base.registry, /ON CONFLICT \(tenant_id, aggregate_type, aggregate_id, aggregate_version, event_type\) DO NOTHING/);
  assert.doesNotMatch(base.registry, /site revision conflict/);

  const guardedExisting = buildOnboardingSql(songyaxuan, { expectedSiteRevision: 3 });
  assert.match(guardedExisting.registry, /IF current_rev IS DISTINCT FROM 3::bigint THEN/);
  assert.match(guardedExisting.registry, /USING ERRCODE = '40001'/);

  const guardedNew = buildOnboardingSql(songyaxuan, { expectedSiteRevision: null });
  assert.match(guardedNew.registry, /IF current_rev IS NOT NULL THEN/);
  assert.match(guardedNew.registry, /USING ERRCODE = '40001'/);

  assert.throws(() => buildOnboardingSql(songyaxuan, { expectedSiteRevision: 0 }), /expectedSiteRevision/);
});

test('semanticRole and assetRelationships validate, compile idempotently, and detect drift', () => {
  assert.deepEqual(checkSiteFile(songyaxuan, songyaxuanAlarmRules), []);
  assert.equal(songyaxuan.assetRelationships?.length, 16);

  const annotatedRoles = new Set<string>();
  for (const device of songyaxuan.devices) {
    for (const point of expandPoints(device, songyaxuan.meterPoints)) {
      if (point.semanticRole) {
        assert.notEqual(point.pointType, 'COMMAND', `${device.code}/${point.code} COMMAND must not carry semanticRole`);
        annotatedRoles.add(point.semanticRole);
      }
    }
  }
  for (const role of SEMANTIC_ROLES) {
    assert.ok(annotatedRoles.has(role), `songyaxuan.site.json missing semanticRole ${role}`);
  }
  assert.deepEqual([...ASSET_RELATIONSHIP_ROLES], ['SERVES']);

  const { registry } = buildOnboardingSql(songyaxuan);
  assert.match(registry, /UPDATE core_registry\.asset_relationships\nSET status = 'RETIRED'/);
  assert.doesNotMatch(registry, /DELETE FROM core_registry\.asset_relationships/);
  assert.match(registry, /INSERT INTO core_registry\.asset_relationships \([^)]*from_asset_id, to_asset_id, relationship_role/);
  const relationshipRows = registry
    .slice(registry.indexOf('INSERT INTO core_registry.asset_relationships'))
    .split('\n')
    .filter((line) => line.includes("'SERVES'"));
  assert.equal(relationshipRows.length, 16);
  assert.match(bindingSection(registry), /semantic_role\)/);
  assert.match(bindingSection(registry), /'ACTIVE_POWER'\)/);
  assert.match(bindingSection(registry), /NULL\)$|NULL\),$/m);

  const query = siteStateQuery(songyaxuan);
  assert.match(query, /'pointSemanticRoles'/);
  assert.match(query, /'assetRelationships'/);

  const invalid = copy(songyaxuan);
  const powerPoint = invalid.meterPoints?.find((point) => point.code === 'power_total');
  if (!powerPoint) throw new Error('meterPoints.power_total missing');
  powerPoint.semanticRole = 'BOGUS_ROLE';
  const ctrlStart = invalid.devices.find((device) => device.code === 'ctrl-chiller-a')?.points.find((point) => point.code === 'start');
  if (!ctrlStart) throw new Error('ctrl-chiller-a/start missing');
  ctrlStart.semanticRole = 'RUN_STATE';
  invalid.assetRelationships = [
    ...(invalid.assetRelationships ?? []),
    { from: 'chiller-a', to: 'chiller-a', role: 'SERVES' },
    { from: 'chwp-1', to: 'chiller-a', role: 'SERVES' },
    { from: 'chiller-a', to: 'chwp-1', role: 'SERVES' },
    { from: 'missing-asset', to: 'chiller-a', role: 'BAD_ROLE' },
  ];
  const problems = checkSiteFile(invalid, songyaxuanAlarmRules).join('\n');
  assert.match(problems, /unknown semanticRole BOGUS_ROLE/);
  assert.match(problems, /ctrl-chiller-a\/start: COMMAND point cannot declare semanticRole/);
  assert.match(problems, /cannot serve itself/);
  assert.match(problems, /duplicate edge chwp-1 -> chiller-a/);
  assert.match(problems, /hydraulic SERVES graph contains a cycle/);
  assert.match(problems, /unknown from asset missing-asset/);
  assert.match(problems, /unsupported role BAD_ROLE/);

  const driftedState = completeState(songyaxuan);
  const firstPointId = Object.keys(driftedState.pointSemanticRoles ?? {})[0];
  if (!firstPointId || !driftedState.pointSemanticRoles) throw new Error('expected semantic role in completeState');
  driftedState.pointSemanticRoles[firstPointId] = 'ZONE_TEMP';
  driftedState.assetRelationships = (driftedState.assetRelationships ?? []).slice(1);
  const driftPlan = planOnboarding(songyaxuan, driftedState);
  assert.equal(driftPlan.find((item) => item.item.startsWith('语义角色 '))?.status, 'differs');
  assert.equal(driftPlan.find((item) => item.item.startsWith('资产水力关系 '))?.status, 'missing');
});
