import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import vm from 'node:vm';
import ts from 'typescript';

const sourcePath = path.resolve('apps/hvac-web/src/real/site-routing.ts');
const source = fs.readFileSync(sourcePath, 'utf8');
const compiled = ts.transpileModule(source, {
  compilerOptions: {
    module: ts.ModuleKind.CommonJS,
    target: ts.ScriptTarget.ES2020,
    strict: true,
  },
  fileName: sourcePath,
}).outputText;

const module = { exports: {} };
vm.runInNewContext(compiled, { module, exports: module.exports }, { filename: sourcePath });
const routing = module.exports;

const tenantId = '01900000-0000-7000-8000-000000000001';
const siteAId = '01900000-0001-7000-8000-000000000001';
const siteBId = '01900000-0002-7000-8000-000000000002';
const invisibleSiteId = '01900000-0003-7000-8000-000000000003';

function site(id, code, displayName) {
  return {
    id,
    tenantId,
    code,
    displayName,
    timezone: 'Asia/Tokyo',
    status: 'ACTIVE',
    revision: 1,
    createdAt: '2026-07-28T00:00:00.000Z',
    updatedAt: '2026-07-28T00:00:00.000Z',
  };
}

const siteA = site(siteAId, 'TOKYO-1', 'Tokyo Plant');
const siteB = site(siteBId, 'OSAKA-1', 'Osaka Plant');

test('platform routes remain outside Site scope while root is the Site entry', () => {
  for (const pathname of ['/system', '/notifications', '/alarms']) {
    const decision = routing.resolveSiteRouting(pathname, [siteA, siteB]);
    assert.equal(decision.state, 'PLATFORM_ROUTE');
  }
  assert.equal(routing.resolveSiteRouting('/', [siteA, siteB]).state, 'CHOOSE_SITE');
});

test('no explicit Site resolves zero, one, and many authorized Site states', () => {
  const zero = routing.resolveSiteEntry([], '/sites');
  assert.equal(zero.state, 'NO_AUTHORIZED_SITE');

  const one = routing.resolveSiteEntry([siteA], '/sites');
  assert.equal(one.state, 'REDIRECT');
  assert.equal(one.target, `/sites/${siteAId}/dashboard`);

  const many = routing.resolveSiteEntry([siteA, siteB], '/sites');
  assert.equal(many.state, 'CHOOSE_SITE');
  assert.deepEqual(many.sites.map((item) => item.id), [siteAId, siteBId]);
});

test('an explicit authorized UUIDv7 Site wins and creates a validated SiteContext', () => {
  for (const leaf of ['dashboard', 'assets', 'energy', 'operations', 'work-orders', 'bigscreen']) {
    const decision = routing.resolveSiteRouting(`/sites/${siteBId}/${leaf}`, [siteA, siteB], ['site.read']);
    assert.equal(decision.state, 'READY');
    assert.equal(decision.route, leaf);
    assert.equal(decision.context.site.id, siteBId);
    assert.equal(decision.context.site.tenantId, tenantId);
  }
});

test('a bare authorized Site path redirects to the default Site route', () => {
  const decision = routing.resolveSiteRouting(`/sites/${siteAId}`, [siteA]);
  assert.equal(decision.state, 'REDIRECT');
  assert.equal(decision.target, `/sites/${siteAId}/dashboard`);
});

test('invalid, invisible, and local building aliases share one safe state', () => {
  for (const pathname of [
    '/sites/b1/assets',
    '/sites/b2/commands',
    '/sites/not-a-uuid/assets',
    `/sites/${invisibleSiteId}/assets`,
  ]) {
    const decision = routing.resolveSiteRouting(pathname, [siteA, siteB]);
    assert.equal(decision.state, 'SITE_NOT_VISIBLE', pathname);
    assert.equal('siteId' in decision, false, pathname);
    assert.equal('context' in decision, false, pathname);
  }
});

test('unknown Site leaves remain 404 without changing the validated Site', () => {
  const decision = routing.resolveSiteRouting(`/sites/${siteAId}/unknown`, [siteA], ['site.read']);
  assert.equal(decision.state, 'SITE_ROUTE_NOT_FOUND');
  assert.equal(decision.context.site.id, siteAId);
});

test('chooser targets are built only from Registry UUIDv7 Site identities', () => {
  assert.equal(routing.siteRoute(siteA, 'commands'), `/sites/${siteAId}/commands`);
  assert.throws(() => routing.siteRoute({ ...siteA, id: 'b1' }, 'assets'), /UUIDv7/);
});

test('an authorized Site without site.read remains generically forbidden', () => {
  const decision = routing.resolveSiteRouting(`/sites/${siteAId}/assets`, [siteA], ['site.list']);
  assert.equal(decision.state, 'FORBIDDEN');
  assert.equal('context' in decision, false);
  assert.equal('siteId' in decision, false);
});


test('Assets accepts typed Asset and Device selectors while obsolete untyped routes remain not found', () => {
  const assetId = '01900000-0011-7000-8000-000000000011';
  const deviceId = '01900000-0012-7000-8000-000000000012';
  const assetDetail = routing.resolveSiteRouting('/sites/' + siteAId + '/assets/asset/' + assetId, [siteA], ['site.read']);
  assert.equal(assetDetail.state, 'READY');
  assert.equal(assetDetail.route, 'assets');
  assert.equal(assetDetail.assetsDetail.kind, 'asset');
  assert.equal(assetDetail.assetsDetail.id, assetId);

  const deviceDetail = routing.resolveSiteRouting('/sites/' + siteAId + '/assets/device/' + deviceId, [siteA], ['site.read']);
  assert.equal(deviceDetail.state, 'READY');
  assert.equal(deviceDetail.route, 'assets');
  assert.equal(deviceDetail.assetsDetail.kind, 'device');
  assert.equal(deviceDetail.assetsDetail.id, deviceId);

  assert.equal(routing.resolveSiteRouting('/sites/' + siteAId + '/assets/' + assetId, [siteA], ['site.read']).state, 'SITE_ROUTE_NOT_FOUND');
  assert.equal(routing.resolveSiteRouting('/sites/' + siteAId + '/assets/device/' + deviceId + '/extra', [siteA], ['site.read']).state, 'SITE_ROUTE_NOT_FOUND');
  assert.equal(routing.resolveSiteRouting('/sites/' + siteAId + '/energy/' + assetId, [siteA], ['site.read']).state, 'SITE_ROUTE_NOT_FOUND');
});
