import assert from 'node:assert/strict';
import test from 'node:test';

import { mergeTelemetrySourceBinding } from './phase1-thingsboard-integration.mjs';

const mqttSpiffe = 'spiffe://hvac.local/mqtt-telemetry-adapter';
const thingsBoardSpiffe = 'spiffe://hvac.local/thingsboard-reconciler';
const mqttIntegration = '018f3e00-0000-7000-8000-000000000101';
const thingsBoardIntegration = '01a05400-0000-7000-8000-000000000001';

test('ThingsBoard prepare merges its source identity without replacing existing MQTT source bindings', () => {
  const input = `HVAC_ENV=development\nTELEMETRY_SOURCE_BINDINGS_JSON={"${mqttSpiffe}":["${mqttIntegration}"]}\n`;
  const first = mergeTelemetrySourceBinding(input, thingsBoardSpiffe, thingsBoardIntegration);
  assert.equal(first.changed, true);
  const line = first.text.split('\n').find((item) => item.startsWith('TELEMETRY_SOURCE_BINDINGS_JSON='));
  const bindings = JSON.parse(line.slice('TELEMETRY_SOURCE_BINDINGS_JSON='.length));
  assert.deepEqual(bindings[mqttSpiffe], [mqttIntegration]);
  assert.deepEqual(bindings[thingsBoardSpiffe], [thingsBoardIntegration]);

  const second = mergeTelemetrySourceBinding(first.text, thingsBoardSpiffe, thingsBoardIntegration);
  assert.equal(second.changed, false);
  assert.equal(second.text, first.text);
});
