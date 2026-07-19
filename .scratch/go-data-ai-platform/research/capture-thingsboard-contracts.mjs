#!/usr/bin/env node

/**
 * Read-only ThingsBoard contract capture.
 *
 * Required environment variables:
 *   TB_HOST
 *   TB_USERNAME
 *   TB_PASSWORD
 *
 * Optional:
 *   TB_OUTPUT_DIR=.scratch/go-data-ai-platform/research/thingsboard-contract-evidence
 *   TB_MAX_ENTITIES=500
 *   TB_REPRESENTATIVES_PER_PROFILE=2
 *   TB_WINDOW_HOURS=24
 *
 * The script never calls RPC, attribute-write, entity-write, delete, or credential endpoints.
 * Output is sanitized: tenant/customer/device/asset IDs and titles are replaced with aliases.
 */

import { createHash } from 'node:crypto';
import { mkdir, writeFile } from 'node:fs/promises';
import path from 'node:path';

const host = required('TB_HOST').replace(/\/$/, '');
const username = required('TB_USERNAME');
const password = required('TB_PASSWORD');
const outputDir = process.env.TB_OUTPUT_DIR ||
  '.scratch/go-data-ai-platform/research/thingsboard-contract-evidence';
const maxEntities = positiveInt('TB_MAX_ENTITIES', 500);
const representativesPerProfile = positiveInt('TB_REPRESENTATIVES_PER_PROFILE', 2);
const windowHours = positiveInt('TB_WINDOW_HOURS', 24);
const capturedAt = new Date().toISOString();

const SAFE_SCALAR_KEY = /^(type|severity|status|method|rpcMethod|oneWay|persistent|requestTimeout|timeout|retries|units?|decimals?|transportType|relationType)$/i;
const RPC_KEY = /(rpc|command|method|oneWay|persistent|requestTimeout|timeout)/i;
const UNIT_KEY = /^(units?|decimals?)$/i;
const SENSITIVE_KEY = /(password|token|secret|credential|accessKey|privateKey|authorization)/i;
const SAFE_ENUM = /^[\w .:/+-]{1,64}$/;
const PII_LIKE = /(@|https?:\/\/|\b(?:\d{1,3}\.){3}\d{1,3}\b|bearer\s|eyJ[A-Za-z0-9_-]{10,})/i;

let token = '';
let requestCount = 0;
const warnings = [];
const coverage = {};

function required(name) {
  const value = process.env[name];
  if (!value) throw new Error(`${name} is required`);
  return value;
}

function positiveInt(name, fallback) {
  const value = Number(process.env[name] || fallback);
  if (!Number.isInteger(value) || value <= 0) throw new Error(`${name} must be a positive integer`);
  return value;
}

function stableAlias(prefix, value) {
  const digest = createHash('sha256').update(String(value ?? '')).digest('hex').slice(0, 12);
  return `${prefix}-${digest}`;
}

function entityId(entity) {
  return entity?.id?.id || entity?.id || entity?.entityId?.id || entity?.entityId || null;
}

function entityType(entity, fallback = 'ENTITY') {
  return entity?.id?.entityType || entity?.entityId?.entityType || fallback;
}

function sanitizeScalar(value) {
  if (value === null || value === undefined) return value;
  if (typeof value === 'number' || typeof value === 'boolean') return value;
  if (typeof value !== 'string') return typeof value;
  if (PII_LIKE.test(value) || value.length > 128) return '[REDACTED]';
  return value;
}

function aliasEntity(entity, kind) {
  const id = entityId(entity);
  const profileId = entity?.deviceProfileId?.id || entity?.assetProfileId?.id || null;
  return {
    alias: stableAlias(kind.toLowerCase(), id),
    entityType: entityType(entity, kind.toUpperCase()),
    profileAlias: profileId ? stableAlias('profile', profileId) : null,
    type: sanitizeScalar(entity?.type),
    createdTime: entity?.createdTime || null,
    additionalInfoKeys: objectKeys(entity?.additionalInfo),
  };
}

function objectKeys(value) {
  return value && typeof value === 'object' && !Array.isArray(value)
    ? Object.keys(value).filter((key) => !SENSITIVE_KEY.test(key)).sort()
    : [];
}

async function request(method, endpoint, { body, allow404 = false } = {}) {
  requestCount += 1;
  const response = await fetch(`${host}${endpoint}`, {
    method,
    headers: {
      accept: 'application/json',
      ...(body ? { 'content-type': 'application/json' } : {}),
      ...(token ? { 'x-authorization': `Bearer ${token}` } : {}),
    },
    body: body ? JSON.stringify(body) : undefined,
    signal: AbortSignal.timeout(30_000),
  });

  if (allow404 && response.status === 404) return null;
  const text = await response.text();
  let parsed = null;
  if (text) {
    try { parsed = JSON.parse(text); } catch { parsed = text; }
  }
  if (!response.ok) {
    const message = typeof parsed === 'string' ? parsed.slice(0, 300) : JSON.stringify(parsed)?.slice(0, 300);
    throw new Error(`${method} ${endpoint} -> ${response.status}: ${message}`);
  }
  return parsed;
}

async function getFirst(endpoints) {
  for (const endpoint of endpoints) {
    try {
      const value = await request('GET', endpoint, { allow404: true });
      if (value !== null) return { endpoint, value };
    } catch (error) {
      warnings.push(`${endpoint}: ${error.message}`);
    }
  }
  return { endpoint: null, value: null };
}

async function pageAll(endpoint, pageSize = 100) {
  const result = [];
  let page = 0;
  while (result.length < maxEntities) {
    const separator = endpoint.includes('?') ? '&' : '?';
    const payload = await request('GET', `${endpoint}${separator}pageSize=${pageSize}&page=${page}&sortOrder=ASC`);
    const data = Array.isArray(payload) ? payload : payload?.data || [];
    result.push(...data.slice(0, maxEntities - result.length));
    if (!payload?.hasNext || data.length === 0) break;
    page += 1;
  }
  return result;
}

function chunks(values, size) {
  const result = [];
  for (let i = 0; i < values.length; i += size) result.push(values.slice(i, i + size));
  return result;
}

function inferType(value) {
  if (value === null || value === undefined) return 'null';
  if (typeof value === 'boolean') return 'boolean';
  if (typeof value === 'number') return Number.isInteger(value) ? 'integer' : 'number';
  if (Array.isArray(value)) return 'array';
  if (typeof value === 'object') return 'object';
  const text = String(value).trim();
  if (/^-?\d+$/.test(text)) return 'integer-string';
  if (/^-?(?:\d+\.\d*|\d*\.\d+)$/.test(text)) return 'number-string';
  if (/^(true|false)$/i.test(text)) return 'boolean-string';
  try { JSON.parse(text); return 'json-string'; } catch { return 'string'; }
}

function safeEnum(values) {
  const unique = [...new Set(values
    .map((value) => String(value))
    .filter((value) => SAFE_ENUM.test(value) && !PII_LIKE.test(value)))];
  return unique.length > 0 && unique.length <= 12 ? unique.sort() : [];
}

function numericSummary(values) {
  const numbers = values.map(Number).filter(Number.isFinite).sort((a, b) => a - b);
  if (!numbers.length) return null;
  const quantile = (q) => numbers[Math.min(numbers.length - 1, Math.floor(q * (numbers.length - 1)))];
  const round = (n) => Number(n.toPrecision(6));
  return {
    count: numbers.length,
    min: round(numbers[0]),
    max: round(numbers[numbers.length - 1]),
    p50: round(quantile(0.5)),
    p95: round(quantile(0.95)),
  };
}

function seriesSummary(points = []) {
  const sorted = [...points].sort((a, b) => Number(a.ts) - Number(b.ts));
  const timestamps = sorted.map((point) => Number(point.ts)).filter(Number.isFinite);
  const values = sorted.map((point) => point.value);
  const intervals = [];
  let duplicates = 0;
  let outOfOrder = 0;
  for (let i = 1; i < points.length; i += 1) {
    if (Number(points[i].ts) < Number(points[i - 1].ts)) outOfOrder += 1;
  }
  for (let i = 1; i < timestamps.length; i += 1) {
    const delta = timestamps[i] - timestamps[i - 1];
    if (delta === 0) duplicates += 1;
    if (delta > 0) intervals.push(delta);
  }
  const types = [...new Set(values.map(inferType))].sort();
  const strings = values.filter((value) => typeof value === 'string');
  return {
    sampleCount: values.length,
    observedTypes: types,
    firstTimestamp: timestamps[0] || null,
    lastTimestamp: timestamps[timestamps.length - 1] || null,
    intervalMs: numericSummary(intervals),
    numericValues: numericSummary(values),
    enumCandidates: safeEnum(strings),
    maxStringLength: strings.length ? Math.max(...strings.map((value) => value.length)) : null,
    duplicateTimestampCount: duplicates,
    sourceOutOfOrderCount: outOfOrder,
    nullCount: values.filter((value) => value === null || value === undefined).length,
  };
}

function latestValueSummary(entries = []) {
  const values = entries.map((entry) => entry?.value);
  return {
    observedTypes: [...new Set(values.map(inferType))].sort(),
    latestTimestamp: entries.reduce((max, entry) => Math.max(max, Number(entry?.ts) || 0), 0) || null,
    enumCandidates: safeEnum(values.filter((value) => typeof value === 'string')),
    numericValues: numericSummary(values),
    detailKeys: [...new Set(entries.flatMap((entry) => objectKeys(entry)))].sort(),
  };
}

function scanObject(root, predicate, pathPrefix = '$', depth = 0, output = []) {
  if (depth > 20 || root === null || root === undefined) return output;
  if (Array.isArray(root)) {
    root.forEach((value, index) => scanObject(value, predicate, `${pathPrefix}[${index}]`, depth + 1, output));
    return output;
  }
  if (typeof root !== 'object') return output;
  for (const [key, value] of Object.entries(root)) {
    if (SENSITIVE_KEY.test(key)) continue;
    const currentPath = `${pathPrefix}.${key}`;
    if (predicate(key, value, currentPath)) {
      output.push({ path: currentPath, key, value: sanitizeScalar(value) });
    }
    scanObject(value, predicate, currentPath, depth + 1, output);
  }
  return output;
}

function profileSanitized(profile, kind) {
  const id = entityId(profile);
  return {
    alias: stableAlias(`${kind}-profile`, id),
    default: Boolean(profile?.default),
    transportType: profile?.transportType || profile?.profileData?.transportConfiguration?.type || null,
    provisionType: profile?.provisionType || null,
    profileDataKeys: objectKeys(profile?.profileData),
    alarmEvidence: scanObject(profile?.profileData, (key) => /(alarm|severity|condition|clearRule)/i.test(key)),
    rpcEvidence: scanObject(profile?.profileData, (key) => RPC_KEY.test(key) && SAFE_SCALAR_KEY.test(key)),
    telemetryMetadataEvidence: scanObject(profile?.profileData, (key) => UNIT_KEY.test(key)),
  };
}

async function captureRelations(entities, type) {
  const output = [];
  for (const entity of entities.slice(0, maxEntities)) {
    const id = entityId(entity);
    if (!id) continue;
    const { value } = await getFirst([
      `/api/relations/info?fromId=${encodeURIComponent(id)}&fromType=${type}`,
      `/api/relations?fromId=${encodeURIComponent(id)}&fromType=${type}`,
    ]);
    for (const relation of Array.isArray(value) ? value : []) {
      const from = relation.from || relation.fromEntity || relation.fromId;
      const to = relation.to || relation.toEntity || relation.toId;
      output.push({
        fromAlias: stableAlias('entity', from?.id || from),
        fromType: from?.entityType || null,
        toAlias: stableAlias('entity', to?.id || to),
        toType: to?.entityType || null,
        relationType: relation.type || relation.relationType || null,
        typeGroup: relation.typeGroup || null,
        additionalInfoKeys: objectKeys(relation.additionalInfo),
      });
    }
  }
  return output;
}

function selectRepresentatives(devices) {
  const groups = new Map();
  for (const device of devices) {
    const profileId = device?.deviceProfileId?.id || 'no-profile';
    const group = groups.get(profileId) || [];
    if (group.length < representativesPerProfile) group.push(device);
    groups.set(profileId, group);
  }
  return [...groups.values()].flat();
}

async function captureDeviceContract(device) {
  const id = entityId(device);
  const alias = stableAlias('device', id);
  const keys = await request('GET', `/api/plugins/telemetry/DEVICE/${id}/keys/timeseries`).catch((error) => {
    warnings.push(`${alias} telemetry keys: ${error.message}`);
    return [];
  });
  const attributeKeys = await request('GET', `/api/plugins/telemetry/DEVICE/${id}/keys/attributes`).catch((error) => {
    warnings.push(`${alias} attribute keys: ${error.message}`);
    return [];
  });

  const now = Date.now();
  const startTs = now - windowHours * 60 * 60 * 1000;
  const telemetry = {};
  for (const keyChunk of chunks(Array.isArray(keys) ? keys : [], 20)) {
    const joined = keyChunk.map(encodeURIComponent).join(',');
    const latest = await request('GET', `/api/plugins/telemetry/DEVICE/${id}/values/timeseries?keys=${joined}`).catch((error) => {
      warnings.push(`${alias} latest telemetry: ${error.message}`);
      return {};
    });
    const history = await request('GET', `/api/plugins/telemetry/DEVICE/${id}/values/timeseries?keys=${joined}&startTs=${startTs}&endTs=${now}&limit=1000&agg=NONE&orderBy=ASC`).catch((error) => {
      warnings.push(`${alias} historical telemetry: ${error.message}`);
      return {};
    });
    for (const key of keyChunk) {
      telemetry[key] = {
        latest: latestValueSummary(latest?.[key] || []),
        window: seriesSummary(history?.[key] || []),
      };
    }
  }

  const attributes = {};
  for (const scope of ['CLIENT_SCOPE', 'SHARED_SCOPE', 'SERVER_SCOPE']) {
    const value = await request('GET', `/api/plugins/telemetry/DEVICE/${id}/values/attributes?scope=${scope}`).catch((error) => {
      warnings.push(`${alias} ${scope} attributes: ${error.message}`);
      return [];
    });
    attributes[scope] = (Array.isArray(value) ? value : []).map((entry) => ({
      key: entry.key,
      lastUpdateTs: entry.lastUpdateTs || null,
      observedType: inferType(entry.value),
      valueSummary: typeof entry.value === 'number'
        ? { numeric: numericSummary([entry.value]) }
        : { enumCandidates: safeEnum([entry.value]) },
    }));
  }

  const alarms = await pageAll(`/api/alarm/DEVICE/${id}?sortProperty=createdTime&sortOrder=DESC`, 100).catch((error) => {
    warnings.push(`${alias} alarms: ${error.message}`);
    return [];
  });

  return {
    device: aliasEntity(device, 'device'),
    telemetryKeyCount: Array.isArray(keys) ? keys.length : 0,
    attributeKeyCount: Array.isArray(attributeKeys) ? attributeKeys.length : 0,
    telemetry,
    attributes,
    alarms: alarms.map((alarm) => ({
      type: alarm.type || null,
      severity: alarm.severity || null,
      status: alarm.status || null,
      acknowledged: alarm.ackTs ? true : false,
      cleared: alarm.clearTs ? true : false,
      startTs: alarm.startTs || null,
      endTs: alarm.endTs || null,
      detailKeys: objectKeys(alarm.details),
      originatorType: alarm.originator?.entityType || null,
    })),
  };
}

async function main() {
  await mkdir(outputDir, { recursive: true });

  const login = await request('POST', '/api/auth/login', { body: { username, password } });
  token = login?.token;
  if (!token) throw new Error('ThingsBoard login response did not contain token');

  const currentUser = await request('GET', '/api/auth/user');
  const tenantId = currentUser?.tenantId?.id || currentUser?.tenantId || null;

  const systemInfo = await getFirst([
    '/api/noauth/systemInfo',
    '/api/system/info',
    '/api/systemInfo',
  ]);

  const [customers, devices, assets, deviceProfiles, assetProfiles, dashboards, ruleChains] = await Promise.all([
    pageAll('/api/customers').catch((error) => { warnings.push(`customers: ${error.message}`); return []; }),
    pageAll('/api/tenant/devices').catch((error) => { warnings.push(`devices: ${error.message}`); return []; }),
    pageAll('/api/tenant/assets').catch((error) => { warnings.push(`assets: ${error.message}`); return []; }),
    pageAll('/api/deviceProfiles').catch((error) => { warnings.push(`device profiles: ${error.message}`); return []; }),
    pageAll('/api/assetProfiles').catch((error) => { warnings.push(`asset profiles: ${error.message}`); return []; }),
    pageAll('/api/tenant/dashboards').catch((error) => { warnings.push(`dashboards: ${error.message}`); return []; }),
    pageAll('/api/ruleChains').catch((error) => { warnings.push(`rule chains: ${error.message}`); return []; }),
  ]);

  coverage.customers = customers.length;
  coverage.devices = devices.length;
  coverage.assets = assets.length;
  coverage.deviceProfiles = deviceProfiles.length;
  coverage.assetProfiles = assetProfiles.length;
  coverage.dashboards = dashboards.length;
  coverage.ruleChains = ruleChains.length;

  const representatives = selectRepresentatives(devices);
  coverage.representativeDevices = representatives.length;

  const deviceContracts = [];
  for (const device of representatives) deviceContracts.push(await captureDeviceContract(device));

  const relations = [
    ...(await captureRelations(assets, 'ASSET')),
    ...(await captureRelations(devices, 'DEVICE')),
  ];

  const dashboardEvidence = [];
  for (const dashboard of dashboards.slice(0, maxEntities)) {
    const id = entityId(dashboard);
    if (!id) continue;
    const detail = await request('GET', `/api/dashboard/${id}`).catch((error) => {
      warnings.push(`dashboard ${stableAlias('dashboard', id)}: ${error.message}`);
      return null;
    });
    if (!detail) continue;
    dashboardEvidence.push({
      dashboardAlias: stableAlias('dashboard', id),
      rpc: scanObject(detail, (key) => RPC_KEY.test(key)),
      units: scanObject(detail, (key) => UNIT_KEY.test(key)),
    });
  }

  const ruleChainEvidence = [];
  for (const chain of ruleChains.slice(0, maxEntities)) {
    const id = entityId(chain);
    if (!id) continue;
    const { value: detail } = await getFirst([
      `/api/ruleChain/${id}/metadata`,
      `/api/ruleChain/${id}`,
    ]);
    if (!detail) continue;
    ruleChainEvidence.push({
      ruleChainAlias: stableAlias('rule-chain', id),
      rpc: scanObject(detail, (key) => RPC_KEY.test(key)),
      alarm: scanObject(detail, (key) => /(alarm|severity|clearAlarm)/i.test(key)),
      telemetry: scanObject(detail, (key) => /(telemetry|timeseries|attribute)/i.test(key) && SAFE_SCALAR_KEY.test(key)),
    });
  }

  const inventory = {
    capturedAt,
    host,
    authority: currentUser?.authority || null,
    tenantAlias: tenantId ? stableAlias('tenant', tenantId) : null,
    systemInfoEndpoint: systemInfo.endpoint,
    systemInfo: sanitizeSystemInfo(systemInfo.value),
    coverage,
    customers: customers.map((entity) => aliasEntity(entity, 'customer')),
    devices: devices.map((entity) => aliasEntity(entity, 'device')),
    assets: assets.map((entity) => aliasEntity(entity, 'asset')),
    deviceProfiles: deviceProfiles.map((profile) => profileSanitized(profile, 'device')),
    assetProfiles: assetProfiles.map((profile) => profileSanitized(profile, 'asset')),
    relations,
  };

  const rpcEvidence = {
    capturedAt,
    statement: 'Read-only evidence only. No RPC was sent by this capture.',
    fromDeviceProfiles: inventory.deviceProfiles.flatMap((profile) => profile.rpcEvidence.map((item) => ({ profileAlias: profile.alias, ...item }))),
    fromDashboards: dashboardEvidence.flatMap((entry) => entry.rpc.map((item) => ({ dashboardAlias: entry.dashboardAlias, ...item }))),
    fromRuleChains: ruleChainEvidence.flatMap((entry) => entry.rpc.map((item) => ({ ruleChainAlias: entry.ruleChainAlias, ...item }))),
    ackSamplesObserved: 0,
    controlledTestRequired: true,
  };

  const alarmEvidence = {
    capturedAt,
    fromDeviceProfiles: inventory.deviceProfiles.flatMap((profile) => profile.alarmEvidence.map((item) => ({ profileAlias: profile.alias, ...item }))),
    fromRuleChains: ruleChainEvidence.flatMap((entry) => entry.alarm.map((item) => ({ ruleChainAlias: entry.ruleChainAlias, ...item }))),
    observed: deviceContracts.flatMap((contract) => contract.alarms.map((alarm) => ({ deviceAlias: contract.device.alias, ...alarm }))),
  };

  const telemetryMetadata = dashboardEvidence.flatMap((entry) => entry.units.map((item) => ({ dashboardAlias: entry.dashboardAlias, ...item })));

  await writeJson('instance-and-entity-inventory.json', inventory);
  await writeJson('representative-device-contracts.json', { capturedAt, windowHours, devices: deviceContracts });
  await writeJson('telemetry-metadata-evidence.json', { capturedAt, dashboardEvidence: telemetryMetadata });
  await writeJson('alarm-contract-evidence.json', alarmEvidence);
  await writeJson('rpc-contract-evidence.json', rpcEvidence);
  await writeJson('capture-manifest.json', {
    capturedAt,
    host,
    requestCount,
    coverage,
    warnings,
    readOnly: true,
    credentialsPersisted: false,
    mutationsPerformed: [],
  });
  await writeFile(path.join(outputDir, 'capture-report.md'), buildReport({
    inventory,
    deviceContracts,
    rpcEvidence,
    alarmEvidence,
    telemetryMetadata,
  }), 'utf8');

  console.log(JSON.stringify({
    ok: true,
    outputDir,
    requestCount,
    coverage,
    warningCount: warnings.length,
    rpcMethodsFound: uniqueRpcMethods(rpcEvidence).length,
    ackSamplesObserved: rpcEvidence.ackSamplesObserved,
  }, null, 2));
}

function sanitizeSystemInfo(value) {
  if (!value || typeof value !== 'object') return value ? '[NON_JSON_RESPONSE]' : null;
  return Object.fromEntries(Object.entries(value)
    .filter(([key]) => !SENSITIVE_KEY.test(key))
    .map(([key, item]) => [key, sanitizeScalar(item)]));
}

function uniqueRpcMethods(rpcEvidence) {
  const candidates = [...rpcEvidence.fromDeviceProfiles, ...rpcEvidence.fromDashboards, ...rpcEvidence.fromRuleChains]
    .filter((item) => /method/i.test(item.key) && typeof item.value === 'string' && item.value !== '[REDACTED]')
    .map((item) => item.value);
  return [...new Set(candidates)].sort();
}

async function writeJson(name, value) {
  await writeFile(path.join(outputDir, name), `${JSON.stringify(value, null, 2)}\n`, 'utf8');
}

function buildReport({ inventory, deviceContracts, rpcEvidence, alarmEvidence, telemetryMetadata }) {
  const telemetryKeys = [...new Set(deviceContracts.flatMap((contract) => Object.keys(contract.telemetry)))].sort();
  const attributeKeys = [...new Set(deviceContracts.flatMap((contract) =>
    Object.values(contract.attributes).flatMap((entries) => entries.map((entry) => entry.key))))].sort();
  const alarmTypes = [...new Set(alarmEvidence.observed.map((alarm) => alarm.type).filter(Boolean))].sort();
  const rpcMethods = uniqueRpcMethods(rpcEvidence);
  const unresolved = [];
  if (!telemetryKeys.length) unresolved.push('No telemetry keys were observed on representative devices.');
  if (!telemetryMetadata.length) unresolved.push('No dashboard/profile unit metadata was found; engineering units require device documentation or manual confirmation.');
  if (!alarmTypes.length && alarmEvidence.fromDeviceProfiles.length === 0) unresolved.push('No actual or configured alarm types were observed.');
  if (!rpcMethods.length) unresolved.push('No RPC method names were discoverable from profiles, dashboards, or rule chains.');
  if (rpcEvidence.ackSamplesObserved === 0) unresolved.push('No real RPC ACK sample was captured; a separately approved non-destructive command test is required.');

  return `# ThingsBoard Contract Capture Report\n\n` +
    `Captured: ${capturedAt}\n\n` +
    `Host: ${host}\n\n` +
    `Mode: read-only; credentials were not persisted; no RPC or mutation endpoint was called.\n\n` +
    `## Coverage\n\n` +
    `- Customers: ${inventory.coverage.customers}\n` +
    `- Devices: ${inventory.coverage.devices}\n` +
    `- Assets: ${inventory.coverage.assets}\n` +
    `- Device profiles: ${inventory.coverage.deviceProfiles}\n` +
    `- Asset profiles: ${inventory.coverage.assetProfiles}\n` +
    `- Representative devices inspected: ${inventory.coverage.representativeDevices}\n` +
    `- Dashboards inspected: ${inventory.coverage.dashboards}\n` +
    `- Rule chains inspected: ${inventory.coverage.ruleChains}\n\n` +
    `## Observed contract surface\n\n` +
    `- Telemetry keys (${telemetryKeys.length}): ${telemetryKeys.join(', ') || 'none'}\n` +
    `- Attribute keys (${attributeKeys.length}): ${attributeKeys.join(', ') || 'none'}\n` +
    `- Alarm types (${alarmTypes.length}): ${alarmTypes.join(', ') || 'none'}\n` +
    `- RPC methods discoverable without sending commands (${rpcMethods.length}): ${rpcMethods.join(', ') || 'none'}\n\n` +
    `## Remaining evidence gaps\n\n` +
    (unresolved.length ? unresolved.map((item) => `- ${item}`).join('\n') : '- None detected by the capture harness.') +
    `\n\n## Safety\n\n` +
    `This report is not proof of command execution semantics. HTTP timeout, device offline, lost ACK, and executed-but-unacknowledged outcomes remain distinct until a controlled test proves the target device behavior.\n`;
}

main().catch((error) => {
  console.error(`capture failed: ${error.message}`);
  process.exitCode = 1;
});
