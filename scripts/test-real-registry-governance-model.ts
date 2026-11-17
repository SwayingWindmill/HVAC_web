import assert from 'node:assert/strict';
import test from 'node:test';
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
