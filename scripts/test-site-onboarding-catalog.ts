import assert from 'node:assert/strict';
import { existsSync, readdirSync, readFileSync } from 'node:fs';
import { createServer } from 'node:http';
import { join, resolve } from 'node:path';
import test from 'node:test';
import {
  buildingTerminals,
  resolveBuildingRiser,
  resolveRoofAnchors,
  type TwinBuildingPlan,
} from '../apps/hvac-web/src/features/operations/digital-twin/twin-building.ts';
import {
  expandDevicePoints,
  loadDeviceTemplateCatalog,
  validateDeviceTemplates,
} from './lib/site-onboarding-templates.ts';
import {
  createRegistryHttpClient,
  syncDeviceTemplatesViaApi,
  syncSiteViaRegistryApi,
} from './site-onboarding-http.ts';
import { generateSiteBundle } from './site-onboarding-init.ts';
import {
  buildOnboardingSql,
  checkSiteFile,
  type OnboardingProfile,
} from './site-onboarding.mjs';

test('every standard device template in deploy/templates/devices loads with valid metadata', () => {
  const catalog = loadDeviceTemplateCatalog();
  assert.ok(catalog.size >= 6, `expected >= 6 templates, got ${catalog.size}`);
  for (const key of [
    'hvac.power-meter.acrel-dtsd1352',
    'hvac.btu-meter.ultrasonic',
    'hvac.chiller.modbus-controller',
    'hvac.pump.vfd-modbus',
    'hvac.temp-module.8ch',
    'hvac.io-relay.8ch',
  ]) {
    assert.ok(catalog.has(key), `missing standard template ${key}`);
  }
});

test('every site manifest under deploy/sites/*.site.json passes checkSiteFile and compiles SQL cleanly', () => {
  const sitesDir = resolve('deploy/sites');
  const siteFiles = readdirSync(sitesDir)
    .filter((name) => name.endsWith('.site.json') && !name.endsWith('.example.site.json') && !name.endsWith('.template.site.json'))
    .sort();
  assert.ok(siteFiles.length >= 1, 'expected at least 1 site manifest under deploy/sites');

  for (const file of siteFiles) {
    const profile = JSON.parse(readFileSync(join(sitesDir, file), 'utf8')) as OnboardingProfile;
    const alarmName = profile.intelligence?.alarmRules;
    const alarmRules = alarmName && existsSync(join(sitesDir, alarmName))
      ? JSON.parse(readFileSync(join(sitesDir, alarmName), 'utf8'))
      : null;
    assert.deepEqual(checkSiteFile(profile, alarmRules), [], `${file} failed checkSiteFile`);
    const sql = buildOnboardingSql(profile);
    assert.ok(sql.deviceCount > 0 && sql.pointCount > 0, `${file} compiled empty SQL`);
  }
});

test('template expansion supports overrides, omissions, morePoints, and rejects unknown templates or override keys', () => {
  const catalog = loadDeviceTemplateCatalog();
  const expanded = expandDevicePoints(
    {
      code: 'meter-test',
      template: 'hvac.power-meter.acrel-dtsd1352',
      pointOverrides: {
        power_total: { address: 'UID9 保持寄存器 36' },
        run_state: { omit: true },
      },
      morePoints: [{ code: 'voltage_ab', name: '线电压 AB', unit: 'V', pointType: 'TELEMETRY', valueType: 'NUMBER' }],
    },
    undefined,
    catalog,
  );
  assert.deepEqual(
    expanded.map((point) => point.code),
    ['power_total', 'active_energy_total', 'voltage_ab'],
  );
  assert.equal(expanded[0]?.address, 'UID9 保持寄存器 36');
  assert.equal(expanded[0]?.semanticRole, 'ACTIVE_POWER');

  const problems: string[] = [];
  validateDeviceTemplates(
    {
      devices: [
        { code: 'd1', template: 'hvac.missing-template' },
        { code: 'd2', template: 'hvac.power-meter.acrel-dtsd1352', type: 'BTU_METER', pointOverrides: { non_existent: {} } },
      ],
    },
    problems,
    catalog,
  );
  const joined = problems.join('\n');
  assert.match(joined, /unknown template hvac\.missing-template/);
  assert.match(joined, /contradicts template/);
  assert.match(joined, /pointOverrides\.non_existent/);
});

test('site-onboarding-init scaffold produces a valid site manifest, buildingPlan, alarm rules, and edge-plant config', () => {
  const bundle = generateSiteBundle({
    code: 'demo-plant',
    name: '示范冷站',
    prefix: '01a24000',
    chillers: 2,
    floors: 3,
    width: 24,
    depth: 16,
  });
  const profile = bundle.site as unknown as OnboardingProfile;
  assert.deepEqual(checkSiteFile(profile, bundle.alarmRules), []);
  const compiled = buildOnboardingSql(profile);
  assert.match(compiled.registry, /INSERT INTO core_registry\.sites/);

  const plan = profile.site.buildingPlan as unknown as TwinBuildingPlan;
  const terminals = buildingTerminals(plan);
  assert.ok(terminals.length > 0);
  const riser = resolveBuildingRiser(plan);
  const roof = resolveRoofAnchors(plan);
  assert.ok(riser.supply[0] >= 0 && riser.supply[0] <= plan.width);
  assert.ok(roof.pau[0] > 0 && roof.pau[0] < plan.width);

  const edgeKeys = new Set<string>([
    ...((bundle.edgePlant.meters as { devices: Array<{ deviceKey: string }> }).devices.map((d) => d.deviceKey)),
    (bundle.edgePlant.meters as { synthesizedTotal: { deviceKey: string } }).synthesizedTotal.deviceKey,
    (bundle.edgePlant.flowMeter as { deviceKey: string; pumpMirror: { deviceKey: string } }).deviceKey,
    (bundle.edgePlant.flowMeter as { pumpMirror: { deviceKey: string } }).pumpMirror.deviceKey,
    (bundle.edgePlant.concentrator as { tempDeviceKey: string }).tempDeviceKey,
    (bundle.edgePlant.btuCalculator as { deviceKey: string }).deviceKey,
  ]);
  const siteSourceKeys = new Set(profile.devices.map((device) => device.sourceKey));
  for (const key of edgeKeys) {
    assert.ok(siteSourceKeys.has(key), `edge-plant deviceKey ${key} missing from scaffolded site.json`);
  }
});

test('Registry HTTP sync publishes templates, creates/patches sites, commits 2-wave imports, and fails closed on dry-run error', async () => {
  const requests: Array<{ method: string; url: string; body: Record<string, unknown> | null }> = [];
  const sites: Array<Record<string, unknown>> = [];
  let seq = 1;
  let rejectWave2 = false;

  const server = createServer((req, res) => {
    const chunks: Buffer[] = [];
    req.on('data', (chunk: Buffer) => chunks.push(chunk));
    req.on('end', () => {
      const raw = Buffer.concat(chunks).toString('utf8');
      const body = raw ? (JSON.parse(raw) as Record<string, unknown>) : null;
      requests.push({ method: req.method ?? 'GET', url: req.url ?? '/', body });
      res.setHeader('Content-Type', 'application/json');

      if (req.method === 'GET' && req.url === '/api/v1/sites') {
        res.end(JSON.stringify({ items: sites, hasMore: false, nextCursor: null }));
        return;
      }
      if (req.method === 'POST' && req.url === '/api/v1/templates/revisions') {
        res.statusCode = 201;
        res.end(JSON.stringify({ id: `01900000-7100-7000-8000-${(seq++).toString(16).padStart(12, '0')}`, revisionNumber: 1, templateKey: body?.templateKey }));
        return;
      }
      if (req.method === 'POST' && req.url === '/api/v1/sites') {
        const created = {
          id: '01a24000-1000-7000-8000-000000000001',
          code: body?.code,
          displayName: body?.displayName,
          timezone: body?.timezone,
          buildingPlan: body?.buildingPlan ?? null,
          revision: 1,
        };
        sites.push(created);
        res.statusCode = 201;
        res.end(JSON.stringify(created));
        return;
      }
      if (req.method === 'PATCH' && req.url?.startsWith('/api/v1/sites/')) {
        sites[0] = {
          ...sites[0],
          displayName: body?.displayName,
          timezone: body?.timezone,
          buildingPlan: body?.buildingPlan ?? sites[0]?.buildingPlan ?? null,
          revision: Number(sites[0]?.revision ?? 1) + 1,
        };
        res.end(JSON.stringify(sites[0]));
        return;
      }
      if (req.method === 'POST' && req.url?.endsWith('/imports/dry-run')) {
        const rows = (body?.rows as Array<{ rowNumber: number; resourceType: string; externalId: string }>) ?? [];
        const isWave2 = rows[0]?.resourceType === 'POINT';
        const results = rows.map((row) => ({
          rowNumber: row.rowNumber,
          resourceType: row.resourceType,
          externalId: row.externalId,
          targetId: `01a24000-5000-7000-8000-${row.rowNumber.toString(16).padStart(12, '0')}`,
          expectedRevision: 0,
          status: isWave2 && rejectWave2 ? 'ERROR' : 'READY',
        }));
        res.end(JSON.stringify({ schemaVersion: 1, planId: '01a24000-8000-7000-8000-000000000001', tenantId: 't', siteId: body?.siteId, namespace: body?.namespace, rows, results, digest: 'abc123' }));
        return;
      }
      if (req.method === 'POST' && req.url?.endsWith('/imports/commit')) {
        const plan = body?.plan as { results: Array<{ externalId: string; targetId: string }> };
        res.statusCode = 201;
        res.end(JSON.stringify({ planDigest: 'abc123', results: plan.results, replayed: false }));
        return;
      }
      if (req.method === 'POST' && req.url === '/api/v1/templates/assignments') {
        res.statusCode = 201;
        res.end(JSON.stringify({ id: '01a24000-9000-7000-8000-000000000001', revision: 1 }));
        return;
      }
      res.statusCode = 404;
      res.end(JSON.stringify({ detail: 'not found' }));
    });
  });

  await new Promise<void>((resolveListen) => server.listen(0, '127.0.0.1', () => resolveListen()));
  try {
    const address = server.address() as { port: number };
    const client = createRegistryHttpClient({ baseUrl: `http://127.0.0.1:${address.port}`, bearerToken: 'test-token' });
    const bundle = generateSiteBundle({ code: 'demo-api', name: '接口测试站', prefix: '01a34000', chillers: 1, floors: 1 });
    const profile = bundle.site as unknown as OnboardingProfile;

    const revisions = await syncDeviceTemplatesViaApi(client);
    assert.ok(revisions.size >= 6);

    const first = await syncSiteViaRegistryApi(client, profile, { alarmRules: bundle.alarmRules, templateRevisions: revisions });
    assert.equal(first.siteAction, 'created');
    assert.ok(first.wave1Rows > 0 && first.wave2Rows > 0 && first.templateAssignments > 0);

    // Rerun without changes leaves site unchanged.
    const second = await syncSiteViaRegistryApi(client, profile, { alarmRules: bundle.alarmRules, templateRevisions: revisions });
    assert.equal(second.siteAction, 'unchanged');

    // Drifted buildingPlan triggers PATCH with expectedRevision = 1.
    const drifted = structuredClone(profile);
    drifted.site.buildingPlan!.width = 26;
    const third = await syncSiteViaRegistryApi(client, drifted, { alarmRules: bundle.alarmRules });
    assert.equal(third.siteAction, 'updated');
    const patchReq = requests.find((item) => item.method === 'PATCH');
    assert.equal((patchReq?.body?.meta as { expectedRevision: number })?.expectedRevision, 1);

    // Wave 2 dry-run error fails closed before Wave 2 commit.
    rejectWave2 = true;
    const commitCountBefore = requests.filter((item) => item.url.endsWith('/imports/commit')).length;
    await assert.rejects(
      () => syncSiteViaRegistryApi(client, drifted, { alarmRules: bundle.alarmRules }),
      /Wave 2 import dry-run rejected rows/,
    );
    const commitCountAfter = requests.filter((item) => item.url.endsWith('/imports/commit')).length;
    // Only Wave 1 committed before Wave 2 aborted.
    assert.equal(commitCountAfter - commitCountBefore, 1);
  } finally {
    server.close();
  }
});
