import assert from 'node:assert/strict';
import test from 'node:test';
import { createPlatformGatewayClient } from '../apps/hvac-web/src/api/generated/platformGateway.gen.ts';
import {
  collectSpaceSubtreeIds,
  formatTagEntries,
  parseTagFilterInput,
  tagMapToEntries,
  validateAndBuildTagMap,
} from '../apps/hvac-web/src/features/system/registry-admin/tags-model.ts';
import {
  buildReleaseReferencesFromEntries,
  createEmptyStructuredDeviceDraft,
  parseStructuredDevicePayload,
  releaseReferencesToEntries,
  serializeStructuredDevicePayload,
  summarizeTemplatePayload,
} from '../apps/hvac-web/src/features/system/registry-admin/template-model.ts';

test('structured DEVICE template builder validates points, quality rules, capability profiles, and release references', () => {
  const emptyDraft = createEmptyStructuredDeviceDraft();
  const validBuilt = serializeStructuredDevicePayload({
    ...emptyDraft,
    deviceType: 'CHILLER',
    standardPoints: [
      {
        pointCode: 'chw_supply_temp',
        sourceKey: 'chw_supply_temp',
        displayName: '冷冻水供水温度',
        pointType: 'TELEMETRY',
        valueType: 'NUMBER',
        unit: 'degC',
        writable: false,
        sampleIntervalMs: '5000',
        publishIntervalMs: '5000',
        staleAfterMs: '15000',
        counterDecreaseMode: '',
        counterRolloverModulus: '',
        minimumNumber: '0',
        maximumNumber: '45',
        flatlineSeconds: '300',
      },
      {
        pointCode: 'run_energy_kwh',
        sourceKey: '',
        displayName: '运行电量累计',
        pointType: 'COUNTER',
        valueType: 'NUMBER',
        unit: 'kWh',
        writable: false,
        sampleIntervalMs: '5000',
        publishIntervalMs: '10000',
        staleAfterMs: '30000',
        counterDecreaseMode: 'ROLLOVER',
        counterRolloverModulus: '100000',
        minimumNumber: '',
        maximumNumber: '',
        flatlineSeconds: '',
      },
    ],
    capabilityProfiles: [
      {
        capabilityName: 'set_chw_setpoint',
        capabilityRevision: 'v1',
        status: 'VERIFIED',
        canonicalUnit: 'degC',
        minimumValue: '5',
        maximumValue: '15',
        maximumDelta: '2',
        riskLevel: 'MEDIUM',
        approvalPolicy: 'NONE',
        connectorKind: 'MODBUS_TCP',
      },
    ],
  });
  assert.equal(validBuilt.ok, true);
  if (validBuilt.ok) {
    const parsedBack = parseStructuredDevicePayload(validBuilt.payload);
    assert.equal(parsedBack.supported, true);
    assert.equal(parsedBack.draft.deviceType, 'CHILLER');
    assert.equal(parsedBack.draft.standardPoints.length, 2);
    assert.equal(parsedBack.draft.capabilityProfiles.length, 1);

    const summary = summarizeTemplatePayload('DEVICE', validBuilt.payload);
    assert.equal(summary.standardPointCount, 2);
    assert.equal(summary.capabilityCount, 1);
  }

  const badInterval = serializeStructuredDevicePayload({
    ...emptyDraft,
    standardPoints: [
      {
        pointCode: 'supply_temp',
        sourceKey: '',
        displayName: '供水温度',
        pointType: 'TELEMETRY',
        valueType: 'NUMBER',
        unit: '',
        writable: false,
        sampleIntervalMs: '10000',
        publishIntervalMs: '5000',
        staleAfterMs: '15000',
        counterDecreaseMode: '',
        counterRolloverModulus: '',
        minimumNumber: '',
        maximumNumber: '',
        flatlineSeconds: '',
      },
    ],
  });
  assert.equal(badInterval.ok, false);

  const refs = buildReleaseReferencesFromEntries([{ key: 'registrySchema', value: 'v2' }]);
  assert.equal(refs.ok, true);
  if (refs.ok) {
    assert.deepEqual(releaseReferencesToEntries(refs.releaseReferences), [{ key: 'registrySchema', value: 'v2' }]);
  }
  assert.equal(buildReleaseReferencesFromEntries([{ key: '', value: '' }]).ok, false);
});

test('DEVICE template summary parses optional modbusDriver metadata and preserves JSON round-trip mode', () => {
  const withTcpDriver = {
    deviceType: 'CHILLER',
    standardPoints: [
      {
        pointCode: 'chw_supply_temp',
        displayName: '冷冻水供水温度',
        pointType: 'TELEMETRY',
        valueType: 'NUMBER',
        unit: 'degC',
        writable: false,
        sampleIntervalMs: 5000,
        publishIntervalMs: 5000,
        staleAfterMs: 15000,
      },
    ],
    modbusDriver: {
      driverType: 'MODBUS_TCP',
      endpoint: '192.168.10.21:502',
      unitId: 1,
      timeoutMs: 1000,
      retries: 2,
      registers: [
        {
          pointCode: 'chw_supply_temp',
          functionCode: 3,
          address: 40001,
          registerCount: 2,
          rawType: 'FLOAT32',
          byteOrder: 'BIG_ENDIAN',
          wordOrder: 'BIG_ENDIAN',
          scale: 1,
          offset: 0,
          pollPriority: 'HIGH',
        },
        {
          pointCode: 'run_state',
          functionCode: 4,
          address: 100,
          registerCount: 1,
          rawType: 'BIT_BOOL',
          byteOrder: 'BIG_ENDIAN',
          wordOrder: 'BIG_ENDIAN',
          bitIndex: 0,
          scale: 1,
          offset: 0,
          pollPriority: 'VERY_HIGH',
        },
      ],
    },
  };

  const deviceSummary = summarizeTemplatePayload('DEVICE', withTcpDriver);
  assert.equal(deviceSummary.standardPointCount, 1);
  assert.deepEqual(deviceSummary.modbusDriverSummary, {
    driverType: 'MODBUS_TCP',
    driverTypeLabel: 'Modbus TCP',
    registerCount: 2,
  });

  const parsedFallback = parseStructuredDevicePayload(withTcpDriver);
  assert.equal(parsedFallback.supported, false);

  const withRtuDriver = {
    modbusDriver: {
      driverType: 'MODBUS_RTU',
      registers: [{ pointCode: 'active_power' }],
    },
  };
  assert.deepEqual(summarizeTemplatePayload('DEVICE', withRtuDriver).modbusDriverSummary, {
    driverType: 'MODBUS_RTU',
    driverTypeLabel: 'Modbus RTU',
    registerCount: 1,
  });

  assert.equal(summarizeTemplatePayload('ASSET', withTcpDriver).modbusDriverSummary, null);
  assert.equal(
    summarizeTemplatePayload('DEVICE', { modbusDriver: { driverType: 'BACNET', registers: [] } })
      .modbusDriverSummary,
    null,
  );
});

test('resource tags helpers enforce key format, filter syntax, and space subtree expansion', () => {
  const validTags = validateAndBuildTagMap([
    { key: 'vendor', value: 'carrier' },
    { key: 'zone/plant', value: 'b1' },
    { key: '', value: '' },
  ]);
  assert.equal(validTags.ok, true);
  if (validTags.ok) {
    assert.deepEqual(validTags.tags, { vendor: 'carrier', 'zone/plant': 'b1' });
    assert.deepEqual(formatTagEntries(validTags.tags), ['vendor:carrier', 'zone/plant:b1']);
    assert.deepEqual(tagMapToEntries(validTags.tags), [
      { key: 'vendor', value: 'carrier' },
      { key: 'zone/plant', value: 'b1' },
    ]);
  }

  assert.equal(validateAndBuildTagMap([{ key: 'BadUpper', value: 'x' }]).ok, false);
  assert.equal(validateAndBuildTagMap([{ key: 'vendor', value: 'a' }, { key: 'vendor', value: 'b' }]).ok, false);

  assert.deepEqual(parseTagFilterInput('vendor=carrier'), { ok: true, tag: 'vendor:carrier' });
  assert.deepEqual(parseTagFilterInput('vendor:carrier'), { ok: true, tag: 'vendor:carrier' });
  assert.deepEqual(parseTagFilterInput('vendor'), { ok: true, tag: 'vendor' });
  assert.equal(parseTagFilterInput('InvalidKey').ok, false);

  const subtree = collectSpaceSubtreeIds(
    [
      { id: 'root', parentSpaceId: null },
      { id: 'child', parentSpaceId: 'root' },
      { id: 'grandchild', parentSpaceId: 'child' },
      { id: 'other', parentSpaceId: null },
    ],
    'root',
  );
  assert.deepEqual(Array.from(subtree).sort(), ['child', 'grandchild', 'root']);
});

test('all 18 Registry mutation methods forward X-CSRF-Token while preserving custom headers', async () => {
  const id = '018f3e00-1000-7000-8000-000000000001';
  const now = '2026-10-01T00:00:00.000Z';
  const meta = { expectedRevision: 0, idempotencyKey: 'csrf-test-key-01', reason: 'CSRF verification' };
  const digest = 'a'.repeat(64);

  const siteBody = { code: 'site-1', displayName: 'Site 1', timezone: 'Asia/Shanghai', status: 'ACTIVE' as const, meta };
  const siteResp = {
    id, tenantId: id, code: 'site-1', displayName: 'Site 1', timezone: 'Asia/Shanghai',
    status: 'ACTIVE', revision: 1, createdAt: now, updatedAt: now, floorAreaM2: null, climateZone: null,
  };
  const spaceBody = { code: 'sp-1', displayName: 'Space 1', spaceType: 'PLANT_ROOM' as const, status: 'ACTIVE' as const, meta };
  const spaceResp = {
    id, tenantId: id, siteId: id, parentSpaceId: null, code: 'sp-1', displayName: 'Space 1',
    spaceType: 'PLANT_ROOM', status: 'ACTIVE', revision: 1, createdAt: now, updatedAt: now,
  };
  const assetBody = { code: 'as-1', displayName: 'Asset 1', assetType: 'CHILLER', status: 'ACTIVE' as const, meta };
  const assetResp = {
    id, tenantId: id, siteId: id, code: 'as-1', displayName: 'Asset 1', assetType: 'CHILLER',
    status: 'ACTIVE', revision: 1, createdAt: now, updatedAt: now,
  };
  const deviceBody = { code: 'dev-1', displayName: 'Device 1', deviceType: 'CHILLER', status: 'ACTIVE' as const, meta };
  const deviceResp = {
    id, tenantId: id, siteId: id, code: 'dev-1', displayName: 'Device 1', deviceType: 'CHILLER',
    status: 'ACTIVE', revision: 1, createdAt: now, updatedAt: now,
  };
  const sensorBody = { code: 'sen-1', displayName: 'Sensor 1', sensorType: 'TEMP', metadata: {}, status: 'ACTIVE' as const, meta };
  const sensorResp = {
    id, tenantId: id, siteId: id, code: 'sen-1', displayName: 'Sensor 1', sensorType: 'TEMP',
    manufacturer: null, model: null, serialNumber: null, calibrationDueAt: null,
    metadata: {}, status: 'ACTIVE', revision: 1, createdAt: now, updatedAt: now,
  };
  const pointBody = {
    reportingDeviceId: id, pointCode: 'chw_temp', sourceKey: 'modbus.40001', displayName: 'Supply Temp',
    pointType: 'TELEMETRY' as const, valueType: 'NUMBER' as const, writable: false,
    sampleIntervalMs: 5000, publishIntervalMs: 5000, staleAfterMs: 15000,
    sourceMetadata: {}, status: 'ACTIVE' as const, meta,
  };
  const pointResp = {
    id, tenantId: id, siteId: id, reportingDeviceId: id, sensorId: null, pointCode: 'chw_temp',
    sourceKey: 'modbus.40001', displayName: 'Supply Temp', pointType: 'TELEMETRY', valueType: 'NUMBER',
    unit: null, writable: false, sampleIntervalMs: 5000, publishIntervalMs: 5000, staleAfterMs: 15000,
    counterDecreaseMode: null, counterRolloverModulus: null, sourceMetadata: {},
    status: 'ACTIVE', revision: 1, createdAt: now, updatedAt: now,
  };
  const rebindBody = { kind: 'DEVICE_ASSET' as const, sourceId: id, targetId: id, role: 'PRIMARY', effectiveAt: now, meta };
  const rebindResp = { bindingId: id, siteId: id, kind: 'DEVICE_ASSET', sourceId: id, targetId: id, role: 'PRIMARY', revision: 1, validFrom: now, replayed: false };
  const releaseBody = { templateKey: 'tpl-1', templateKind: 'DEVICE' as const, payload: {}, releaseReferences: { schema: 'v1' }, meta };
  const releaseResp = { id, tenantId: id, templateId: id, templateKey: 'tpl-1', templateKind: 'DEVICE', revisionNumber: 1, status: 'RELEASED', payload: {}, releaseReferences: { schema: 'v1' }, releasedAt: now };
  const assignBody = { siteId: id, targetType: 'DEVICE' as const, targetId: id, templateRevisionId: id, effectiveAt: now, meta };
  const assignResp = { id, tenantId: id, siteId: id, targetType: 'DEVICE', targetId: id, templateRevisionId: id, validFrom: now, revision: 1 };
  const importRow = { rowNumber: 1, resourceType: 'DEVICE' as const, externalId: 'ext-1', payload: {} };
  const importResultRow = { rowNumber: 1, resourceType: 'DEVICE', externalId: 'ext-1', expectedRevision: 0, status: 'READY' };
  const planBody = { namespace: 'ns-1', rows: [importRow] };
  const planResp = { schemaVersion: 1, planId: id, tenantId: id, siteId: id, namespace: 'ns-1', rows: [importRow], results: [importResultRow], digest };
  const commitBody = {
    plan: {
      schemaVersion: 1 as const, planId: id, tenantId: id, siteId: id, namespace: 'ns-1',
      rows: [importRow], results: [{ ...importResultRow, status: 'READY' as const }], digest,
    },
    meta,
  };
  const commitResp = { planDigest: digest, results: [{ ...importResultRow, status: 'COMMITTED' }], replayed: false };
  const retireBody = { resourceType: 'DEVICE' as const, resourceId: id, meta };
  const retireResp = { sagaId: id, resourceType: 'DEVICE', resourceId: id, status: 'COMPLETED', dependencyCount: 0, revision: 1, replayed: false };

  const client = createPlatformGatewayClient('https://example.invalid');
  const csrfToken = 'csrf-token-expected';
  const init: RequestInit = { headers: { 'X-Custom': 'keep-me', 'x-csrf-token': 'stale-token' } };
  const calls: ReadonlyArray<{ name: string; expectedMethod: string; response: unknown; invoke: () => Promise<unknown> }> = [
    { name: 'createSite', expectedMethod: 'POST', response: siteResp, invoke: () => client.createSite(siteBody, csrfToken, init) },
    { name: 'updateSite', expectedMethod: 'PATCH', response: siteResp, invoke: () => client.updateSite(id, siteBody, csrfToken, init) },
    { name: 'createSiteSpace', expectedMethod: 'POST', response: spaceResp, invoke: () => client.createSiteSpace(id, spaceBody, csrfToken, init) },
    { name: 'updateSiteSpace', expectedMethod: 'PATCH', response: spaceResp, invoke: () => client.updateSiteSpace(id, id, spaceBody, csrfToken, init) },
    { name: 'createSiteAsset', expectedMethod: 'POST', response: assetResp, invoke: () => client.createSiteAsset(id, assetBody, csrfToken, init) },
    { name: 'updateSiteAsset', expectedMethod: 'PATCH', response: assetResp, invoke: () => client.updateSiteAsset(id, id, assetBody, csrfToken, init) },
    { name: 'createSiteDevice', expectedMethod: 'POST', response: deviceResp, invoke: () => client.createSiteDevice(id, deviceBody, csrfToken, init) },
    { name: 'updateSiteDevice', expectedMethod: 'PATCH', response: deviceResp, invoke: () => client.updateSiteDevice(id, id, deviceBody, csrfToken, init) },
    { name: 'createSiteSensor', expectedMethod: 'POST', response: sensorResp, invoke: () => client.createSiteSensor(id, sensorBody, csrfToken, init) },
    { name: 'updateSiteSensor', expectedMethod: 'PATCH', response: sensorResp, invoke: () => client.updateSiteSensor(id, id, sensorBody, csrfToken, init) },
    { name: 'createSitePoint', expectedMethod: 'POST', response: pointResp, invoke: () => client.createSitePoint(id, pointBody, csrfToken, init) },
    { name: 'updateSitePoint', expectedMethod: 'PATCH', response: pointResp, invoke: () => client.updateSitePoint(id, id, pointBody, csrfToken, init) },
    { name: 'rebindRegistryResource', expectedMethod: 'POST', response: rebindResp, invoke: () => client.rebindRegistryResource(id, rebindBody, csrfToken, init) },
    { name: 'releaseRegistryTemplateRevision', expectedMethod: 'POST', response: releaseResp, invoke: () => client.releaseRegistryTemplateRevision(releaseBody, csrfToken, init) },
    { name: 'assignRegistryTemplateRevision', expectedMethod: 'POST', response: assignResp, invoke: () => client.assignRegistryTemplateRevision(assignBody, csrfToken, init) },
    { name: 'planRegistryImport', expectedMethod: 'POST', response: planResp, invoke: () => client.planRegistryImport(id, planBody, csrfToken, init) },
    { name: 'commitRegistryImport', expectedMethod: 'POST', response: commitResp, invoke: () => client.commitRegistryImport(id, commitBody, csrfToken, init) },
    { name: 'beginRegistryRetirement', expectedMethod: 'POST', response: retireResp, invoke: () => client.beginRegistryRetirement(id, retireBody, csrfToken, init) },
  ];

  assert.equal(calls.length, 18);
  const previousFetch = globalThis.fetch;
  try {
    for (const entry of calls) {
      let seenInit: RequestInit | undefined;
      globalThis.fetch = async (_input, fetchInit) => {
        seenInit = fetchInit;
        return new Response(JSON.stringify(entry.response), { status: 200, headers: { 'Content-Type': 'application/json' } });
      };
      await entry.invoke();
      const headers = new Headers(seenInit?.headers);
      assert.equal(seenInit?.method, entry.expectedMethod, `${entry.name}: method`);
      assert.equal(seenInit?.credentials, 'same-origin', `${entry.name}: credentials`);
      assert.equal(headers.get('X-CSRF-Token'), csrfToken, `${entry.name}: X-CSRF-Token`);
      assert.equal(headers.get('Content-Type'), 'application/json', `${entry.name}: Content-Type`);
      assert.equal(headers.get('X-Custom'), 'keep-me', `${entry.name}: custom header preserved`);
    }
  } finally {
    globalThis.fetch = previousFetch;
  }
});
