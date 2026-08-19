import assert from 'node:assert/strict';
import { spawn, spawnSync } from 'node:child_process';
import { once } from 'node:events';
import { existsSync } from 'node:fs';
import { mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import { createServer as createTCPServer } from 'node:net';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import WebSocket from 'ws';
import { centralPlantDevices, centralPlantIdentity } from './central-plant-local-contract.mjs';
import {
  centralPlantAreas,
  centralPlantCalculatedPointCount,
  centralPlantDeviceEndpoints,
  centralPlantEquipment,
  centralPlantSensors,
} from './central-plant-spatial-model.mjs';
import { startCentralPlantLocalTopology } from './central-plant-local-topology.mjs';

const root = resolve(process.cwd());
const pause = (milliseconds) => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));
const durableSmokeReportPath = resolve(root, 'out/central-plant-local-smoke-report.json');

async function findAvailablePort() {
  const server = createTCPServer();
  server.listen({ host: '127.0.0.1', port: 0, exclusive: true });
  await once(server, 'listening');
  const address = server.address();
  if (!address || typeof address === 'string') throw new Error('browser port allocator failed');
  await new Promise((resolveClose, rejectClose) => server.close((error) => error ? rejectClose(error) : resolveClose()));
  return address.port;
}

function dockerQuery(container, sql) {
  const result = spawnSync('docker', ['exec', container, 'psql', '-U', 'postgres', '-d', 'hvac_s2', '-Atqc', sql], {
    cwd: root,
    encoding: 'utf8',
    windowsHide: true,
  });
  if (result.error || result.status !== 0) throw new Error(result.error?.message ?? result.stderr?.trim() ?? 'S2 query failed');
  return String(result.stdout).trim();
}

function clickHouseQuery(container, sql) {
  const result = spawnSync('docker', ['exec', container, 'clickhouse-client', '--user', 'telemetry_history', '--query', sql], {
    cwd: root,
    encoding: 'utf8',
    windowsHide: true,
  });
  if (result.error || result.status !== 0) throw new Error(result.error?.message ?? result.stderr?.trim() ?? 'ClickHouse query failed');
  return String(result.stdout).trim();
}

function queryPersistedSummary(topology) {
  const integration = centralPlantIdentity.integrationInstanceId;
  const sql = `SELECT json_build_object(
    'acceptedObservations', (SELECT count(*) FROM telemetry_runtime.source_observations WHERE integration_instance_id='${integration}'::uuid AND acceptance_status='ACCEPTED'),
    'deviceCount', (SELECT count(DISTINCT device_id) FROM telemetry_runtime.source_observations WHERE integration_instance_id='${integration}'::uuid AND acceptance_status='ACCEPTED'),
    'latestPointCount', (SELECT count(*) FROM telemetry_runtime.latest_accepted_telemetry l JOIN telemetry_runtime.registry_device_bindings b USING (device_id) WHERE b.integration_instance_id='${integration}'::uuid),
    'snapshotCount', (SELECT count(*) FROM telemetry_runtime.device_observation_snapshots s JOIN telemetry_runtime.registry_device_bindings b USING (device_id) WHERE b.integration_instance_id='${integration}'::uuid),
    'publishedPublicationCount', (SELECT count(*) FROM telemetry_runtime.telemetry_publication_outbox o JOIN telemetry_runtime.registry_device_bindings b USING (device_id) WHERE b.integration_instance_id='${integration}'::uuid AND o.delivery_state='PUBLISHED'),
    'pendingPublicationCount', (SELECT count(*) FROM telemetry_runtime.telemetry_publication_outbox o JOIN telemetry_runtime.registry_device_bindings b USING (device_id) WHERE b.integration_instance_id='${integration}'::uuid AND o.delivery_state='PENDING'),
    'quarantineCount', (SELECT count(*) FROM telemetry_runtime.ingest_quarantine WHERE integration_instance_id='${integration}'::uuid AND resolved_at IS NULL)
  )::text;`;
  return JSON.parse(dockerQuery(topology.database.s2Container, sql));
}

async function waitForPersistedLoop(topology) {
  let last = null;
  for (let attempt = 0; attempt < 120; attempt += 1) {
    last = queryPersistedSummary(topology);
    if (
      Number(last.acceptedObservations) >= topology.pointCount
      && Number(last.deviceCount) === topology.deviceCount
      && Number(last.latestPointCount) === topology.pointCount
      && Number(last.snapshotCount) === topology.deviceCount
      && Number(last.publishedPublicationCount) > 0
      && Number(last.pendingPublicationCount) === 0
      && Number(last.quarantineCount) === 0
    ) return last;
    await pause(1000);
  }
  throw new Error(`central plant telemetry did not converge: ${JSON.stringify(last)}`);
}

function queryEnergySummary(topology) {
  const fields = clickHouseQuery(topology.database.clickHouseContainer, `
SELECT
  count(),
  round(sum(energy_kwh), 6),
  countIf(quality = 'VALID'),
  countIf(quality = 'SUSPECT'),
  min(period_start),
  max(period_end)
FROM analytics.energy_interval_facts
WHERE tenant_id = '${centralPlantIdentity.tenantId}'
  AND site_id = '${centralPlantIdentity.siteId}'
  AND device_id = '${centralPlantDevices.find((device) => device.name === 'METER-HVAC-TOTAL').platformDeviceId}'
  AND telemetry_key = 'hvac_meter.energy'
FORMAT TabSeparated
`).split('\t');
  return {
    factCount: Number(fields[0] ?? 0),
    totalEnergyKwh: Number(fields[1] ?? 0),
    validFactCount: Number(fields[2] ?? 0),
    suspectFactCount: Number(fields[3] ?? 0),
    firstPeriodStart: fields[4] ?? '',
    lastPeriodEnd: fields[5] ?? '',
  };
}

async function waitForEnergyFacts(topology) {
  const minimumFactCount = Math.max(1, topology.energyHistory.readingCount - 1);
  let last = null;
  for (let attempt = 0; attempt < 600; attempt += 1) {
    last = queryEnergySummary(topology);
    if (
      last.factCount >= minimumFactCount
      && last.totalEnergyKwh > 0
      && last.validFactCount >= minimumFactCount
      && last.suspectFactCount === 0
    ) return last;
    await pause(500);
  }
  throw new Error(`central plant Energy facts did not converge: ${JSON.stringify(last)}`);
}

function createCDPClient(webSocketURL) {
  return new Promise((resolveClient, rejectClient) => {
    const socket = new WebSocket(webSocketURL);
    const pending = new Map();
    let nextID = 0;
    socket.once('open', () => resolveClient({
      send(method, params = {}) {
        const id = ++nextID;
        socket.send(JSON.stringify({ id, method, params }));
        return new Promise((resolveCommand, rejectCommand) => pending.set(id, { resolveCommand, rejectCommand }));
      },
      close() { socket.close(); },
    }));
    socket.once('error', rejectClient);
    socket.on('message', (raw) => {
      const message = JSON.parse(String(raw));
      if (!message.id) return;
      const command = pending.get(message.id);
      if (!command) return;
      pending.delete(message.id);
      if (message.error) command.rejectCommand(new Error(message.error.message));
      else command.resolveCommand(message.result);
    });
  });
}

async function evaluate(client, expression) {
  const response = await client.send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
  if (response.exceptionDetails) throw new Error(response.exceptionDetails.exception?.description ?? response.exceptionDetails.text);
  return response.result.value;
}

async function waitForCondition(client, expression, label) {
  let last;
  for (let attempt = 0; attempt < 600; attempt += 1) {
    try {
      last = await evaluate(client, expression);
      if (last) return last;
    } catch {}
    await pause(100);
  }
  const diagnostic = await evaluate(client, `(async () => {
    let principal = null;
    try {
      const response = await fetch('/api/v1/principal', { credentials: 'include', headers: { Accept: 'application/json, application/problem+json' } });
      principal = { status: response.status, body: (await response.text()).slice(0, 5000) };
    } catch (error) {
      principal = { error: String(error) };
    }
    return { url: location.href, text: document.body?.innerText?.slice(0, 5000) ?? '', principal };
  })()`).catch((error) => ({ error: String(error) }));
  throw new Error(`${label} did not become ready: ${JSON.stringify({ last, diagnostic })}`);
}

function dateOnlyInTimeZone(value, timeZone) {
  const parts = new Intl.DateTimeFormat('en-US', {
    timeZone,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }).formatToParts(new Date(value));
  const fields = Object.fromEntries(parts.map((part) => [part.type, part.value]));
  return `${fields.year}-${fields.month}-${fields.day}`;
}

async function auditLogtoExperience(client, logto) {
  const origin = new URL(logto.coreURL).origin;
  await waitForCondition(client, `location.origin === ${JSON.stringify(origin)} && Boolean(document.querySelector('input'))`, 'Logto sign-in page');
  const signIn = await evaluate(client, `(() => {
    const controls = [...document.querySelectorAll('a, button')];
    const register = controls.find((control) => {
      const text = control.textContent?.trim() ?? '';
      const href = control instanceof HTMLAnchorElement ? control.getAttribute('href') ?? '' : '';
      return text.includes('注册') || href.includes('/register');
    });
    const submit = document.querySelector('button[type="submit"]');
    const wrapper = document.querySelector("main[class*='main'] > div[class*='wrapper']");
    const logo = [...document.querySelectorAll('img')].find((image) => image.src.includes('quanlaihe-mark.svg'));
    const bodyText = document.body.innerText;
    const promotionalFragments = ['实时设备与点位', '能源绩效与异常', '告警、工单与闭环', '建筑 → 区域 → 设备 → 点位', '设备、能耗与运维协同平台'];
    const wrapperStyle = wrapper ? getComputedStyle(wrapper) : null;
    const logoStyle = logo ? getComputedStyle(logo) : null;
    const bodyStyle = getComputedStyle(document.body);
    return {
      hasRegistrationAction: Boolean(register),
      registrationLabel: register?.textContent?.trim() ?? '',
      hasBrandName: bodyText.includes('泉来禾智慧能源'),
      hasPromotionalCopy: promotionalFragments.some((fragment) => bodyText.includes(fragment)),
      logoSrc: logo?.src ?? '',
      logoWidth: logoStyle?.width ?? '',
      primaryButtonBackground: submit ? getComputedStyle(submit).backgroundColor : '',
      wrapperRadius: wrapperStyle?.borderRadius ?? '',
      wrapperBoxShadow: wrapperStyle?.boxShadow ?? '',
      wrapperWidth: wrapper?.getBoundingClientRect().width ?? 0,
      bodyBackgroundImage: bodyStyle.backgroundImage,
      bodyBackgroundColor: bodyStyle.backgroundColor,
      clickedRegistration: Boolean(register && (register.click(), true)),
    };
  })()`);
  const signInEvidence = JSON.stringify(signIn);
  assert.equal(signIn.hasRegistrationAction, true, signInEvidence);
  assert.equal(signIn.clickedRegistration, true, signInEvidence);
  assert.equal(signIn.hasBrandName, true, signInEvidence);
  assert.equal(signIn.hasPromotionalCopy, false, signInEvidence);
  assert.ok(signIn.logoSrc.includes('quanlaihe-mark.svg'), signInEvidence);
  assert.equal(signIn.logoWidth, '36px', signInEvidence);
  assert.equal(signIn.primaryButtonBackground, 'rgb(11, 74, 76)', signInEvidence);
  assert.equal(signIn.wrapperRadius, '16px', signInEvidence);
  assert.equal(signIn.wrapperBoxShadow, 'none', signInEvidence);
  assert.ok(signIn.wrapperWidth <= 400, signInEvidence);
  assert.equal(signIn.bodyBackgroundImage, 'none', signInEvidence);
  assert.equal(signIn.bodyBackgroundColor, 'rgb(244, 246, 247)', signInEvidence);

  const registration = await waitForCondition(client, `(() => {
    if (location.origin !== ${JSON.stringify(origin)}) return false;
    const text = document.body.innerText;
    const identifier = document.querySelector('input[name="identifier"], input[name="username"], input[type="text"]');
    if (!identifier || !text.includes('注册')) return false;
    const promotionalFragments = ['实时设备与点位', '能源绩效与异常', '告警、工单与闭环', '建筑 → 区域 → 设备 → 点位', '设备、能耗与运维协同平台'];
    return {
      path: location.pathname,
      hasIdentifierField: Boolean(identifier),
      hasApprovalNotice: text.includes('注册账号需由管理员分配访问权限'),
      hasBrandName: text.includes('泉来禾智慧能源'),
      hasPromotionalCopy: promotionalFragments.some((fragment) => text.includes(fragment)),
    };
  })()`, 'Logto registration page');
  const registrationEvidence = JSON.stringify(registration);
  assert.equal(registration.hasIdentifierField, true, registrationEvidence);
  assert.equal(registration.hasApprovalNotice, true, registrationEvidence);
  assert.equal(registration.hasBrandName, true, registrationEvidence);
  assert.equal(registration.hasPromotionalCopy, false, registrationEvidence);

  await client.send('Page.navigate', { url: logto.loginURL });
  await waitForCondition(client, `location.origin === ${JSON.stringify(origin)} && Boolean(document.querySelector('input'))`, 'fresh Logto sign-in page');
  return { signIn, registration };
}

async function submitLogtoSignIn(client, logto) {
  const experience = await auditLogtoExperience(client, logto);
  const origin = new URL(logto.coreURL).origin;
  const firstStep = await evaluate(client, `(() => {
    const setValue = (input, value) => {
      const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set;
      setter?.call(input, value);
      input.dispatchEvent(new Event('input', { bubbles: true }));
      input.dispatchEvent(new Event('change', { bubbles: true }));
    };
    const identifier = document.querySelector('input[name="identifier"], input[name="username"], input[type="text"]');
    const credential = document.querySelector('input[type="password"]');
    if (identifier) setValue(identifier, ${JSON.stringify(logto.username)});
    if (credential) setValue(credential, ${JSON.stringify(logto.credential)});
    const submit = document.querySelector('button[type="submit"]') ?? [...document.querySelectorAll('button')].find((button) => !button.disabled);
    submit?.click();
    return { hadCredentialField: Boolean(credential), submitted: Boolean(submit) };
  })()`);
  assert.equal(firstStep.submitted, true, `Logto first sign-in step was not submitted: ${JSON.stringify(firstStep)}`);
  if (firstStep.hadCredentialField) return experience;
  await waitForCondition(client, `location.origin === ${JSON.stringify(origin)} && Boolean(document.querySelector('input[type="password"]'))`, 'Logto credential step');
  const secondStep = await evaluate(client, `(() => {
    const input = document.querySelector('input[type="password"]');
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set;
    setter?.call(input, ${JSON.stringify(logto.credential)});
    input.dispatchEvent(new Event('input', { bubbles: true }));
    input.dispatchEvent(new Event('change', { bubbles: true }));
    const submit = document.querySelector('button[type="submit"]') ?? [...document.querySelectorAll('button')].find((button) => !button.disabled);
    submit?.click();
    return Boolean(submit);
  })()`);
  assert.equal(secondStep, true, 'Logto credential step was not submitted');
  return experience;
}

async function browserAudit(topology) {
  const browserCandidates = [
    process.env.BROWSER_BINARY,
    process.env['PROGRAMFILES(X86)'] ? join(process.env['PROGRAMFILES(X86)'], 'Microsoft', 'Edge', 'Application', 'msedge.exe') : null,
    process.env.PROGRAMFILES ? join(process.env.PROGRAMFILES, 'Microsoft', 'Edge', 'Application', 'msedge.exe') : null,
    'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe',
    'C:\\Program Files\\Microsoft\\Edge\\Application\\msedge.exe',
    '/usr/bin/google-chrome', '/usr/bin/google-chrome-stable', '/usr/bin/chromium',
  ].filter(Boolean);
  const browserPath = browserCandidates.find((candidate) => existsSync(candidate));
  if (!browserPath) throw new Error('A CDP-compatible browser is required for the central plant smoke test');
  const profileDirectory = join(tmpdir(), `central-plant-local-${process.pid}`);
  await mkdir(profileDirectory, { recursive: true });
  const debugPort = await findAvailablePort();
  const browser = spawn(browserPath, [
    '--headless=new', '--disable-gpu', '--no-sandbox', '--no-first-run', '--no-default-browser-check',
    '--ignore-certificate-errors', `--remote-debugging-port=${debugPort}`, `--user-data-dir=${profileDirectory}`, 'about:blank',
  ], { stdio: 'ignore', windowsHide: true });
  let client;
  try {
    for (let attempt = 0; attempt < 300; attempt += 1) {
      try { if ((await fetch(`http://127.0.0.1:${debugPort}/json/version`)).ok) break; } catch {}
      if (attempt === 299) throw new Error('browser debugger did not become ready');
      await pause(100);
    }
    const pages = await fetch(`http://127.0.0.1:${debugPort}/json/list`).then((response) => response.json());
    const page = pages.find((candidate) => candidate.type === 'page');
    assert(page?.webSocketDebuggerUrl, 'browser page is unavailable');
    client = await createCDPClient(page.webSocketDebuggerUrl);
    await client.send('Runtime.enable');
    await client.send('Page.enable');
    await client.send('Emulation.setDeviceMetricsOverride', {
      width: 1600,
      height: 1000,
      deviceScaleFactor: 1,
      mobile: false,
    });
    const sitePath = `/sites/${centralPlantIdentity.siteId}/assets`;
    const expectedAssetCounts = {
      areas: centralPlantAreas.length,
      equipment: centralPlantEquipment.length,
      deviceEndpoints: centralPlantDeviceEndpoints.length,
      sensors: centralPlantSensors.length,
      telemetryPoints: topology.pointCount,
      calculatedPoints: centralPlantCalculatedPointCount,
      independentSensorDevices: centralPlantSensors.filter((sensor) => sensor.mode === 'INDEPENDENT_DEVICE').length,
    };
    await client.send('Page.navigate', {
      url: `${topology.webURL}/api/v1/auth/login?returnTo=${encodeURIComponent(sitePath)}`,
    });
    const logtoExperience = await submitLogtoSignIn(client, topology.logto);
    await waitForCondition(client, `(() => {
      const root = document.querySelector('[data-testid="real-site-route-assets"]');
      return location.pathname === ${JSON.stringify(sitePath)}
        && root?.dataset.areaCount === ${JSON.stringify(String(expectedAssetCounts.areas))}
        && root?.dataset.equipmentCount === ${JSON.stringify(String(expectedAssetCounts.equipment))}
        && root?.dataset.deviceEndpointCount === ${JSON.stringify(String(expectedAssetCounts.deviceEndpoints))}
        && root?.dataset.sensorCount === ${JSON.stringify(String(expectedAssetCounts.sensors))}
        && root?.dataset.telemetryPointCount === ${JSON.stringify(String(expectedAssetCounts.telemetryPoints))}
        && root?.dataset.independentSensorDeviceCount === ${JSON.stringify(String(expectedAssetCounts.independentSensorDevices))}
        && root?.dataset.calculatedPointCount === ${JSON.stringify(String(expectedAssetCounts.calculatedPoints))}
        && root?.dataset.pointLedgerCount === ${JSON.stringify(String(expectedAssetCounts.telemetryPoints))}
        && root?.dataset.filteredPointCount === ${JSON.stringify(String(expectedAssetCounts.telemetryPoints))}
        && root?.dataset.ledgerMode === 'points'
        && document.querySelectorAll('[data-testid="real-assets-table-wrap"] [data-point-id]').length >= ${JSON.stringify(expectedAssetCounts.telemetryPoints)}
        && document.body.innerText.includes('中央机房')
        && document.body.innerText.includes('资产导航')
        && document.body.innerText.includes('点位');
    })()`, 'authenticated central plant atomic Asset Model shell');

    const navigation = await waitForCondition(client, `(() => {
      const hierarchyCard = document.querySelector('.assets-hierarchy-card');
      const ledgerCard = document.querySelector('.assets-ledger-card');
      const hierarchyScroll = document.querySelector('.assets-hierarchy-card__scroll');
      if (!hierarchyCard || !ledgerCard || !hierarchyScroll) return false;
      const hierarchyRect = hierarchyCard.getBoundingClientRect();
      const ledgerRect = ledgerCard.getBoundingClientRect();
      return {
        hasSwitcher: Boolean(document.querySelector('.real-assets-tree-switcher')),
        hasMeta: Boolean(document.querySelector('.real-assets-tree-node__meta')),
        overflowY: getComputedStyle(hierarchyScroll).overflowY,
        topDelta: Math.abs(hierarchyRect.top - ledgerRect.top),
        heightDelta: Math.abs(hierarchyRect.height - ledgerRect.height),
        hierarchyHeight: hierarchyRect.height,
        ledgerHeight: ledgerRect.height,
      };
    })()`, 'compact Asset navigation layout');
    const navigationEvidence = JSON.stringify(navigation);
    assert.equal(navigation.hasSwitcher, true, navigationEvidence);
    assert.equal(navigation.hasMeta, false, navigationEvidence);
    assert.ok(navigation.overflowY === 'auto' || navigation.overflowY === 'scroll', navigationEvidence);
    assert.ok(navigation.topDelta < 2, navigationEvidence);
    assert.ok(navigation.heightDelta < 2, navigationEvidence);

    const chiller = centralPlantDevices.find((device) => device.name === 'CHILLER-01');
    assert(chiller, 'CHILLER-01 contract is unavailable');
    const requestedKeys = ['chiller.cop', 'chiller.power', 'chiller.cooling_capacity'];
    const authority = await waitForCondition(client, `(async () => {
      try {
        const assetModelResponse = await fetch(
          ${JSON.stringify(`/api/v1/sites/${centralPlantIdentity.siteId}/asset-model`)},
          { credentials: 'include', headers: { Accept: 'application/json, application/problem+json' } },
        );
        const assetModel = await assetModelResponse.json();
        const expectedCounts = ${JSON.stringify(expectedAssetCounts)};
        if (!assetModelResponse.ok || assetModel?.schemaVersion !== 1 || assetModel?.siteId !== ${JSON.stringify(centralPlantIdentity.siteId)}) return false;
        if (!assetModel?.counts || Object.entries(expectedCounts).some(([key, value]) => assetModel.counts[key] !== value)) return false;
        const areas = Array.isArray(assetModel.areas) ? assetModel.areas : [];
        const equipment = Array.isArray(assetModel.equipment) ? assetModel.equipment : [];
        const devices = Array.isArray(assetModel.devices) ? assetModel.devices : [];
        const sensors = Array.isArray(assetModel.sensors) ? assetModel.sensors : [];
        const telemetryPoints = Array.isArray(assetModel.telemetryPoints) ? assetModel.telemetryPoints : [];
        const relationships = Array.isArray(assetModel.relationships) ? assetModel.relationships : [];
        if (areas.length !== expectedCounts.areas
          || equipment.length !== expectedCounts.equipment
          || devices.length !== expectedCounts.deviceEndpoints
          || sensors.length !== expectedCounts.sensors
          || telemetryPoints.length !== expectedCounts.telemetryPoints
          || relationships.length === 0) return false;
        const chiller = devices.find((device) => device.id === ${JSON.stringify(chiller.platformDeviceId)} && device.displayName.includes('CHILLER-01'));
        if (!chiller) return false;
        const query = new URLSearchParams({ keys: ${JSON.stringify(requestedKeys.join(','))} });
        const snapshotResponse = await fetch(
          '/api/v1/devices/' + encodeURIComponent(chiller.id) + '/observation-snapshot?' + query.toString(),
          { credentials: 'include', headers: { Accept: 'application/json, application/problem+json' } },
        );
        const snapshot = await snapshotResponse.json();
        if (!snapshotResponse.ok || snapshot?.deviceId !== chiller.id || !Array.isArray(snapshot?.values)) return false;
        const values = Object.fromEntries(snapshot.values.map((value) => [value.key, value]));
        if (!${JSON.stringify(requestedKeys)}.every((key) => values[key]?.state === 'PRESENT')) return false;
        return {
          sitePath: location.pathname,
          assetModel: {
            counts: assetModel.counts,
            deviceNames: devices.map((device) => device.displayName).sort(),
            relationshipCount: relationships.length,
          },
          snapshot: {
            deviceId: snapshot.deviceId,
            businessRevision: snapshot.businessRevision,
            displayState: snapshot.displayState,
            telemetryReadiness: snapshot.telemetryReadiness,
            values: ${JSON.stringify(requestedKeys)}.map((key) => ({
              key,
              value: values[key].value,
              unit: values[key].unit ?? null,
              freshness: values[key].freshness,
              quality: values[key].quality,
            })),
          },
        };
      } catch {
        return false;
      }
    })()`, 'Gateway Registry and S2 authority');
    assert.deepEqual(authority.assetModel.counts, expectedAssetCounts);
    assert.deepEqual(authority.assetModel.deviceNames, centralPlantDevices.map((device) => device.name).sort());
    assert.ok(authority.assetModel.relationshipCount > 0);
    assert.equal(authority.snapshot.deviceId, chiller.platformDeviceId);
    assert.equal(authority.snapshot.values.length, requestedKeys.length);

    const energyAnchor = dateOnlyInTimeZone(topology.energyHistory.to, 'Asia/Shanghai');
    const energyPath = `/sites/${centralPlantIdentity.siteId}/energy/day?period=day&anchor=${energyAnchor}&quality=VALID_ONLY`;
    await client.send('Page.navigate', { url: `${topology.webURL}${energyPath}` });
    const energy = await waitForCondition(client, `(() => {
      const errorRoot = document.querySelector('[data-testid="real-energy-error"]');
      if (errorRoot) {
        const now = Date.now();
        const lastRetry = Number(globalThis.__centralPlantEnergyRetryAt ?? 0);
        const retry = errorRoot.querySelector('button');
        if (retry && now - lastRetry >= 2000) {
          globalThis.__centralPlantEnergyRetryAt = now;
          retry.click();
        }
        return false;
      }
      const root = document.querySelector('[data-testid="real-energy-dashboard"]');
      if (location.pathname + location.search !== ${JSON.stringify(energyPath)} || !root) return false;
      const text = root.innerText ?? '';
      const returnedPeriods = Number(text.match(/(\\d+) 个权威返回桶/)?.[1] ?? 0);
      const state = root.getAttribute('data-business-state') ?? '';
      const datasetRevision = root.getAttribute('data-dataset-revision') ?? '';
      const hourCells = root.querySelectorAll('.real-energy__hour-cell').length;
      const canvasCount = root.querySelectorAll('canvas').length;
      if (returnedPeriods < 1
        || state === 'EMPTY'
        || !datasetRevision
        || hourCells !== 24
        || canvasCount < 1
        || !text.includes('周期总电能')
        || !text.includes('日度电能趋势与基期对比')
        || !text.includes('24 小时电量分布')
        || !text.includes('导出真实数据')
        || !text.includes('尚未接入权威模型的 Energy 能力')) return false;
      return { path: location.pathname + location.search, state, datasetRevision, returnedPeriods, hourCells, canvasCount };
    })()`, 'converged Energy day workspace with historical meter data');
    const energyEvidence = JSON.stringify(energy);
    assert.ok(energy.returnedPeriods > 0, energyEvidence);
    assert.equal(energy.hourCells, 24, energyEvidence);
    assert.ok(energy.canvasCount > 0, energyEvidence);
    assert.notEqual(energy.state, 'EMPTY', energyEvidence);
    assert.ok(energy.datasetRevision.length > 0, energyEvidence);

    const energyMonthPath = `/sites/${centralPlantIdentity.siteId}/energy/month?period=month&anchor=${energyAnchor.slice(0, 7)}-01&quality=VALID_ONLY`;
    await client.send('Page.navigate', { url: `${topology.webURL}${energyMonthPath}` });
    const energyMonth = await waitForCondition(client, `(() => {
      const root = document.querySelector('[data-testid="real-energy-dashboard"]');
      if (location.pathname + location.search !== ${JSON.stringify(energyMonthPath)} || !root) return false;
      const text = root.innerText ?? '';
      const calendarCells = root.querySelectorAll('.real-energy__calendar-cell').length;
      const measuredCalendarCells = root.querySelectorAll('.real-energy__calendar-cell:not(:disabled)').length;
      if (!text.includes('月度能耗日历') || calendarCells !== 42 || measuredCalendarCells < 1) return false;
      return { path: location.pathname + location.search, calendarCells, measuredCalendarCells };
    })()`, 'converged Energy month calendar');
    assert.equal(energyMonth.calendarCells, 42, JSON.stringify(energyMonth));
    assert.ok(energyMonth.measuredCalendarCells > 0, JSON.stringify(energyMonth));

    const logoutStarted = await evaluate(client, `(() => {
      const button = document.querySelector('[data-testid="real-logout-button"]');
      button?.click();
      return Boolean(button);
    })()`);
    assert.equal(logoutStarted, true, 'authenticated shell did not expose the logout action');
    const loggedOut = await waitForCondition(client, `(() => {
      if (location.origin !== ${JSON.stringify(new URL(topology.webURL).origin)}) return false;
      const params = new URLSearchParams(location.search);
      const card = document.querySelector('[data-testid="real-shell-login-required"]');
      if (params.get('logged_out') !== '1' || !card) return false;
      return {
        path: location.pathname + location.search,
        title: card.querySelector('h1, h2')?.textContent?.trim() ?? '',
        hasReauthenticationAction: Boolean(card.querySelector('button')),
        assetMounted: Boolean(document.querySelector('[data-testid="real-site-route-assets"]')),
        energyMounted: Boolean(document.querySelector('[data-testid="real-energy-dashboard"]')),
      };
    })()`, 'provider-backed logged-out landing');
    const loggedOutEvidence = JSON.stringify(loggedOut);
    assert.equal(loggedOut.title, '已退出登录', loggedOutEvidence);
    assert.equal(loggedOut.hasReauthenticationAction, true, loggedOutEvidence);
    assert.equal(loggedOut.assetMounted, false, loggedOutEvidence);
    assert.equal(loggedOut.energyMounted, false, loggedOutEvidence);

    const reauthenticationStarted = await evaluate(client, `(() => {
      const button = document.querySelector('[data-testid="real-shell-login-required"] button');
      button?.click();
      return Boolean(button);
    })()`);
    assert.equal(reauthenticationStarted, true, 'logged-out landing did not expose reauthentication');
    const reauthentication = await waitForCondition(client, `(() => {
      if (location.origin !== ${JSON.stringify(new URL(topology.logto.coreURL).origin)}) return false;
      const input = document.querySelector('input[name="identifier"], input[name="username"], input[type="password"], input[type="text"]');
      if (!input) return false;
      return {
        path: location.pathname,
        hasCredentialInput: true,
        assetMounted: Boolean(document.querySelector('[data-testid="real-site-route-assets"]')),
      };
    })()`, 'fresh Logto credentials after logout');
    const reauthenticationEvidence = JSON.stringify(reauthentication);
    assert.equal(reauthentication.hasCredentialInput, true, reauthenticationEvidence);
    assert.equal(reauthentication.assetMounted, false, reauthenticationEvidence);

    return {
      browser: browserPath,
      logtoExperience,
      logout: { loggedOut, reauthentication },
      navigation,
      energy,
      energyMonth,
      ...authority,
    };
  } finally {
    client?.close();
    if (browser.exitCode === null) {
      if (process.platform === 'win32') spawnSync('taskkill', ['/PID', String(browser.pid), '/T', '/F'], { stdio: 'ignore', windowsHide: true });
      else browser.kill('SIGTERM');
    }
    await rm(profileDirectory, { recursive: true, force: true, maxRetries: 10, retryDelay: 100 });
  }
}

async function runSmoke() {
  await rm(durableSmokeReportPath, { force: true });
  const topology = await startCentralPlantLocalTopology({ quiet: true });
  try {
    const persisted = await waitForPersistedLoop(topology);
    const analytics = await waitForEnergyFacts(topology);
    const browser = await browserAudit(topology);
    const report = {
      schemaVersion: 1,
      status: 'passed',
      topology: 'EG8200 -> MQTT -> MQTT Adapter -> S2 -> Gateway -> HVAC Web Real',
      webURL: topology.webURL,
      mqttBrokerURL: topology.mqttBrokerURL,
      logto: {
        issuer: topology.logto.issuer,
        clientId: topology.logto.clientId,
        subject: topology.logto.subject,
        username: topology.logto.username,
        registrationEnabled: topology.logto.registrationEnabled,
      },
      persisted,
      analytics,
      browser,
      verifiedAt: new Date().toISOString(),
    };
    const serialized = `${JSON.stringify(report, null, 2)}\n`;
    await writeFile(resolve(root, 'out/central-plant-local/smoke-report.json'), serialized, 'utf8');
    await writeFile(durableSmokeReportPath, serialized, 'utf8');
    console.log(JSON.stringify(report, null, 2));
  } catch (error) {
    const report = {
      schemaVersion: 1,
      status: 'failed',
      webURL: topology.webURL,
      logto: {
        issuer: topology.logto.issuer,
        clientId: topology.logto.clientId,
        subject: topology.logto.subject,
        username: topology.logto.username,
        registrationEnabled: topology.logto.registrationEnabled,
      },
      error: error instanceof Error ? error.message : String(error),
      failedAt: new Date().toISOString(),
    };
    await writeFile(durableSmokeReportPath, `${JSON.stringify(report, null, 2)}\n`, 'utf8');
    throw error;
  } finally {
    await topology.stop();
  }
}

async function runUp() {
  const topology = await startCentralPlantLocalTopology({ quiet: false });
  console.log(JSON.stringify({ status: 'ready', webURL: topology.webURL, mqttBrokerURL: topology.mqttBrokerURL }, null, 2));
  await new Promise((resolveSignal) => {
    const shutdown = () => resolveSignal();
    process.once('SIGINT', shutdown);
    process.once('SIGTERM', shutdown);
  });
  await topology.stop();
}

async function runExistingBrowserAudit() {
  const keepAlive = setInterval(() => {}, 1000);
  try {
    const topology = JSON.parse(await readFile(resolve(root, 'out/central-plant-local/stack-report.json'), 'utf8'));
    if (topology?.status !== 'ready' || !topology.webURL || !topology.logto?.credential) {
      throw new Error('central plant stack report is not ready for browser audit');
    }
    const browser = await browserAudit(topology);
    console.log(JSON.stringify({ schemaVersion: 1, status: 'passed', webURL: topology.webURL, browser }, null, 2));
  } finally {
    clearInterval(keepAlive);
  }
}

const action = process.argv[2] ?? 'up';
if (action === 'up') await runUp();
else if (action === 'smoke') await runSmoke();
else if (action === 'browser') await runExistingBrowserAudit();
else throw new Error('usage: node scripts/central-plant-local.mjs {up|smoke|browser}');
