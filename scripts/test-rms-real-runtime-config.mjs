import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import vm from 'node:vm';
import ts from 'typescript';

const sourcePath = path.resolve('apps/hvac-web/src/real/runtime-config.ts');
const source = fs.readFileSync(sourcePath, 'utf8');
const compiled = ts.transpileModule(source, {
  compilerOptions: {
    module: ts.ModuleKind.CommonJS,
    target: ts.ScriptTarget.ES2020,
    strict: true,
  },
  fileName: sourcePath,
}).outputText;

function loadRuntimeConfig(overrides = {}) {
  const module = { exports: {} };
  const context = vm.createContext({
    module,
    exports: module.exports,
    URL,
    window: { location: { origin: 'https://hvac.example' } },
    __HVAC_WEB_BUILD_TARGET__: 'real',
    __HVAC_WEB_BUILD_ID__: 'real-test-build',
    __HVAC_WEB_GATEWAY_BASE_PATH__: '/api/v1',
    __HVAC_WEB_REALTIME_PROTOCOL__: 'centrifugo-v1',
    ...overrides,
  });
  vm.runInContext(compiled, context, { filename: sourcePath });
  return module.exports;
}

test('accepts the supported Real build identity, Gateway path, and realtime protocol', () => {
  const runtime = loadRuntimeConfig();
  const result = runtime.validateRealRuntimeConfig();
  assert.equal(result.ok, true);
  assert.equal(result.config.buildTarget, 'real');
  assert.equal(result.config.buildId, 'real-test-build');
  assert.equal(result.config.gatewayBasePath, '/api/v1');
  assert.equal(result.config.realtimeProtocol, 'centrifugo-v1');
});

test('fails closed for a Demo target and unsupported realtime protocol', () => {
  const runtime = loadRuntimeConfig({
    __HVAC_WEB_BUILD_TARGET__: 'demo',
    __HVAC_WEB_REALTIME_PROTOCOL__: 'socketio',
  });
  const result = runtime.validateRealRuntimeConfig();
  assert.equal(result.ok, false);
  assert.equal(result.failures.length, 2);
  assert.equal(result.failures[0].code, 'INVALID_BUILD_TARGET');
  assert.equal(result.failures[1].code, 'UNSUPPORTED_REALTIME_PROTOCOL');
});

test('rejects empty, absolute, and alternate Gateway locations', () => {
  for (const gatewayBasePath of ['', 'https://gateway.example/api/v1', '//gateway.example/api/v1', '/gateway']) {
    const runtime = loadRuntimeConfig({ __HVAC_WEB_GATEWAY_BASE_PATH__: gatewayBasePath });
    const result = runtime.validateRealRuntimeConfig();
    assert.equal(result.ok, false, gatewayBasePath);
    assert.ok(result.failures.some((failure) => failure.code === 'INVALID_GATEWAY_BASE_PATH'), gatewayBasePath);
  }
});

test('normalizes one trailing slash on the approved Gateway path', () => {
  const runtime = loadRuntimeConfig({ __HVAC_WEB_GATEWAY_BASE_PATH__: '/api/v1/' });
  const result = runtime.validateRealRuntimeConfig();
  assert.equal(result.ok, true);
  assert.equal(result.config.gatewayBasePath, '/api/v1');
});
