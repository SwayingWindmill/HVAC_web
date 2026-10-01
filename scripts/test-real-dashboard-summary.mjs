import assert from 'node:assert/strict';
import fs from 'node:fs';
import test from 'node:test';
import { startSiteDashboardLive, siteDashboardSummaryEventsPath } from '../apps/hvac-web/src/api/site-dashboard-live.ts';
import { selectDashboardAlarmCandidate } from '../apps/hvac-web/src/features/dashboard/dashboard-alarm-link.ts';

const dashboard = fs.readFileSync('apps/hvac-web/src/features/overview/Overview.tsx', 'utf8');
const productPages = fs.readFileSync('apps/hvac-web/src/features/product/ProductPages.tsx', 'utf8');
const api = fs.readFileSync('apps/hvac-web/src/api/site-dashboard.ts', 'utf8');
const generated = fs.readFileSync('apps/hvac-web/src/api/generated/platformGateway.gen.ts', 'utf8');

test('Dashboard and BigScreen consume the same authorization-scoped SiteDashboardSummary projection', () => {
  for (const source of [dashboard, productPages]) {
    assert.match(source, /useSiteDashboardSummary\(/);
    assert.match(source, /principal\.context\.tenantId/);
    assert.match(source, /principal\.session\.id/);
    assert.match(source, /principal\.authorization\.policyRevision/);
  }
  assert.match(api, /\['presentation', 'site-dashboard-summary', tenantId, siteId, authorizationScope\]/);
  assert.match(generated, /getSiteDashboardSummary:/);
});

test('Real Dashboard keeps KPI truth in SiteDashboardSummary and only reads authoritative owner detail streams', () => {
  for (const forbidden of [
    'useRegistryDevices',
    'useVisibleDevicePresence',
    'MAX_DASHBOARD_DEVICES',
    'MAX_REGISTRY_PAGES',
    'projectDashboardDevices',
    'projectDashboardEnergy',
    '24 * 60 * 60 * 1000',
    '实时连接',
  ]) {
    assert.equal(dashboard.includes(forbidden), false, `RealDashboard reintroduced ${forbidden}`);
  }
  assert.match(dashboard, /slowMetrics\.cop/);
  assert.match(dashboard, /fastMetrics\.currentPower/);
  assert.match(dashboard, /readDashboardOverview\(/);
  assert.match(dashboard, /listScopedAlarms\(/);
  assert.equal(dashboard.includes('queryEnergySeries('), false);
});

test('Dashboard priority Alarm deep links never guess between ambiguous same-title Alarm records', () => {
  const priority = { title: '冷冻水供水温度过高', severity: 'CRITICAL' };
  const unique = { alarmId: 'alarm-1', title: priority.title, currentSeverity: 'MAJOR' };
  assert.equal(selectDashboardAlarmCandidate(priority, [unique])?.alarmId, 'alarm-1');

  const sameTitleDifferentSeverity = { alarmId: 'alarm-2', title: priority.title, currentSeverity: 'CRITICAL' };
  assert.equal(selectDashboardAlarmCandidate(priority, [unique, sameTitleDifferentSeverity])?.alarmId, 'alarm-2');

  const secondCritical = { alarmId: 'alarm-3', title: priority.title, currentSeverity: 'CRITICAL' };
  assert.equal(selectDashboardAlarmCandidate(priority, [unique, sameTitleDifferentSeverity, secondCritical]), null);
  assert.equal(selectDashboardAlarmCandidate(priority, [{ alarmId: 'alarm-4', title: '其他告警', currentSeverity: 'CRITICAL' }]), null);
});

test('BigScreen does not claim unconditional READY or fabricate Site KPI values', () => {
  assert.equal(productPages.includes('testId="real-site-route-bigscreen" state="READY"'), false);
  assert.match(productPages, /state=\{summaryQuery\.isPending \? 'LOADING' : summary\?\.quality \?\? 'UNAVAILABLE'\}/);
  assert.match(productPages, /availabilityPercent/);
  assert.match(productPages, /fastMetrics\.openAlarms/);
  assert.match(productPages, /权威 Summary/);
  assert.equal(productPages.includes('bigscreen-live-dot'), false);
});

test('Dashboard keeps connectivity and telemetry condition separate instead of rebuilding an overall device state', () => {
  assert.equal(dashboard.includes('online /'), false);
  assert.match(dashboard, /population\?\.availabilityPercent/);
  assert.match(dashboard, /population\?\.stale/);
  assert.equal(dashboard.includes('overview?.deviceStatus'), false);
  assert.equal(dashboard.includes('population.offline + population.stale'), false);
});

test('dashboard live reconnect reconciles REST before reopening the delta stream', async () => {
  const tenantId = 'tenant-1';
  const siteId = 'site-1';
  const summary = (generatedAt) => ({ tenantId, siteId, generatedAt });
  let current = summary('2026-08-19T12:00:00.000Z');
  const reconciled = summary('2026-08-19T12:00:05.000Z');
  const order = [];
  const sources = [];
  const scheduled = [];

  class FakeSource {
    constructor(url) { this.url = url; this.closed = false; sources.push(this); order.push(`open:${url}`); }
    addSummaryListener(listener) { this.listener = listener; }
    setErrorHandler(listener) { this.error = listener; }
    close() { this.closed = true; order.push('close'); }
    fail() { this.error(); }
    emit(delta) { this.listener(JSON.stringify(delta)); }
  }

  const session = startSiteDashboardLive(tenantId, siteId, current, {
    readSummary: async () => { order.push('read'); return reconciled; },
    getSummary: () => current,
    setSummary: (value) => { current = value; order.push(`set:${value.generatedAt}`); },
    parseDelta: (raw) => JSON.parse(raw),
    openEventSource: (url) => new FakeSource(url),
    schedule: (callback, delayMs) => { scheduled.push({ callback, delayMs }); return scheduled.length; },
    cancelSchedule: () => undefined,
  });

  assert.equal(sources.length, 1);
  assert.equal(sources[0].url, siteDashboardSummaryEventsPath(siteId, current.generatedAt));
  sources[0].fail();
  assert.equal(scheduled.length, 1);
  assert.equal(scheduled[0].delayMs, 3_000);
  scheduled.shift().callback();
  await new Promise((resolve) => setImmediate(resolve));

  assert.equal(sources.length, 2);
  assert.ok(order.indexOf('read') < order.findIndex((item) => item.startsWith('open:') && item.includes(encodeURIComponent(reconciled.generatedAt))));
  assert.equal(current.generatedAt, reconciled.generatedAt);

  const liveSummary = summary('2026-08-19T12:00:10.000Z');
  sources[1].emit({ schemaVersion: 1, baseGeneratedAt: reconciled.generatedAt, summary: liveSummary });
  assert.equal(current.generatedAt, liveSummary.generatedAt);
  session.close();
});

test('dashboard live rejects a delta whose base does not match the current REST snapshot', () => {
  const tenantId = 'tenant-1';
  const siteId = 'site-1';
  let current = { tenantId, siteId, generatedAt: '2026-08-19T12:00:00.000Z' };
  const scheduled = [];
  let source;
  const session = startSiteDashboardLive(tenantId, siteId, current, {
    readSummary: async () => current,
    getSummary: () => current,
    setSummary: (value) => { current = value; },
    parseDelta: (raw) => JSON.parse(raw),
    openEventSource: () => {
      source = {
        addSummaryListener(listener) { this.listener = listener; },
        setErrorHandler(listener) { this.error = listener; },
        close() {},
        emit(delta) { this.listener(JSON.stringify(delta)); },
      };
      return source;
    },
    schedule: (callback, delayMs) => { scheduled.push({ callback, delayMs }); return scheduled.length; },
    cancelSchedule: () => undefined,
  });

  source.emit({
    schemaVersion: 1,
    baseGeneratedAt: '2026-08-19T11:59:59.000Z',
    summary: { tenantId, siteId, generatedAt: '2026-08-19T12:00:05.000Z' },
  });
  assert.equal(current.generatedAt, '2026-08-19T12:00:00.000Z');
  assert.equal(scheduled.length, 1);
  assert.equal(scheduled[0].delayMs, 0);
  session.close();
});
