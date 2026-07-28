import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import vm from 'node:vm';
import ts from 'typescript';

const sourcePath = path.resolve('apps/hvac-web/src/real/shell-policy.ts');
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
vm.runInNewContext(compiled, {
  module,
  exports: module.exports,
  URL,
}, { filename: sourcePath });
const policy = module.exports;

test('normalizes same-origin login return paths to relative application URLs', () => {
  assert.equal(
    policy.normalizeReturnTo('https://hvac.example/sites/abc/assets?tab=live#device', 'https://hvac.example'),
    '/sites/abc/assets?tab=live#device',
  );
  assert.equal(
    policy.normalizeReturnTo('/system?tab=overview', 'https://hvac.example'),
    '/system?tab=overview',
  );
});

test('rejects external, executable, protocol-relative, and malformed login return paths', () => {
  for (const candidate of [
    'https://attacker.example/steal',
    '//attacker.example/steal',
    'javascript:alert(1)',
    '\\\\attacker.example\\steal',
    'not a valid url%',
  ]) {
    assert.equal(policy.normalizeReturnTo(candidate, 'https://hvac.example'), '/', candidate);
  }
});

test('classifies only authentication-required and invalid-session problems as login required', () => {
  assert.equal(policy.classifyBootstrapProblem({ status: 401, code: 'AUTHENTICATION_REQUIRED' }), 'LOGIN_REQUIRED');
  assert.equal(policy.classifyBootstrapProblem({ status: 401, code: 'SESSION_INVALID' }), 'LOGIN_REQUIRED');
  assert.equal(policy.classifyBootstrapProblem({ status: 503, code: 'SESSION_STORE_UNAVAILABLE' }), 'UNAVAILABLE');
  assert.equal(policy.classifyBootstrapProblem({ status: 403, code: 'IAM_IDENTITY_REJECTED' }), 'UNAVAILABLE');
});

test('treats an explicit already-invalid session as completed logout', () => {
  assert.equal(policy.isAlreadyInvalidLogout({ status: 401, code: 'AUTHENTICATION_REQUIRED' }), true);
  assert.equal(policy.isAlreadyInvalidLogout({ status: 401, code: 'SESSION_INVALID' }), true);
  assert.equal(policy.isAlreadyInvalidLogout({ status: 503, code: 'SESSION_PERSISTENCE_FAILED' }), false);
});
