import { createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { existsSync } from 'node:fs';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';

const generatorVersion = '2.0.0';
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const specPath = resolve(root, 'contracts/http/platform-gateway.openapi.yaml');
const toolingLockPath = resolve(root, 'contracts/http/tooling.lock.json');
const goTemplatePath = resolve(root, 'contracts/http/templates/platformapi.go.tmpl');
const tsTemplatePath = resolve(root, 'contracts/http/templates/platformGateway.ts.tmpl');
const goOutputPath = resolve(root, 'services/platform-gateway/pkg/platformapi/api.gen.go');
const tsOutputPath = resolve(root, 'apps/hvac-web/src/api/generated/platformGateway.gen.ts');
const checkOnly = process.argv.includes('--check');
const windowsGofmtPath = 'C:\\Program Files\\Go\\bin\\gofmt.exe';
const gofmtBinary = process.env.GOFMT_BINARY ?? (process.platform === 'win32' && existsSync(windowsGofmtPath) ? windowsGofmtPath : 'gofmt');

const [specText, toolingLockText, goTemplate, tsTemplate] = await Promise.all([
  readFile(specPath, 'utf8'), readFile(toolingLockPath, 'utf8'), readFile(goTemplatePath, 'utf8'), readFile(tsTemplatePath, 'utf8'),
]);
const spec = JSON.parse(specText);
const toolingLock = JSON.parse(toolingLockText);
const digest = createHash('sha256').update(specText).digest('hex');

function invariant(condition, message) {
  if (!condition) throw new Error(`Invalid platform Gateway OpenAPI contract: ${message}`);
}
function exactMembers(actual, expected) {
  return Array.isArray(actual) && actual.length === expected.length && expected.every((member) => actual.includes(member));
}
function exactKeys(value, expected) {
  return value !== null && typeof value === 'object' && exactMembers(Object.keys(value), expected);
}
function operation(operationId) {
  for (const [path, pathItem] of Object.entries(spec.paths ?? {})) {
    for (const method of ['get', 'post', 'put', 'patch', 'delete']) {
      if (pathItem?.[method]?.operationId === operationId) return { path, method, operation: pathItem[method] };
    }
  }
  return null;
}
function schemaRef(operationValue, status, contentType = 'application/json') {
  return operationValue?.responses?.[status]?.content?.[contentType]?.schema?.$ref;
}

invariant(toolingLock.generatorVersion === generatorVersion, 'tooling lock generator version does not match this generator');
invariant(toolingLock.generator === 'scripts/generate-platform-contracts.mjs', 'tooling lock generator path is invalid');
invariant(exactMembers(toolingLock.templates, [
  'contracts/http/templates/platformapi.go.tmpl', 'contracts/http/templates/platformGateway.ts.tmpl',
]), 'tooling lock templates are invalid');
invariant(exactMembers(toolingLock.outputs, [
  'services/platform-gateway/pkg/platformapi/api.gen.go', 'apps/hvac-web/src/api/generated/platformGateway.gen.ts',
]), 'tooling lock outputs are invalid');
invariant(spec.openapi === '3.1.0', 'OpenAPI version must be 3.1.0');

const expectedOperations = {
  getHealth: ['get', '/api/v1/health'],
  getVersion: ['get', '/api/v1/version'],
  beginLogin: ['get', '/api/v1/auth/login'],
  completeLogin: ['get', '/api/v1/auth/callback'],
  getCurrentPrincipal: ['get', '/api/v1/principal'],
  logout: ['post', '/api/v1/auth/logout'],
  revokeSession: ['post', '/api/v1/auth/sessions/{sessionId}/revoke'],
};
const operations = {};
for (const [operationId, [method, path]] of Object.entries(expectedOperations)) {
  const value = operation(operationId);
  invariant(value?.method === method && value?.path === path, `${operationId} method/path is unsupported by generator version 2`);
  operations[operationId] = value;
}
invariant(schemaRef(operations.getHealth.operation, '200') === '#/components/schemas/HealthResponse', 'getHealth success schema is unsupported');
invariant(schemaRef(operations.getVersion.operation, '200') === '#/components/schemas/BuildInfo', 'getVersion success schema is unsupported');
invariant(schemaRef(operations.getCurrentPrincipal.operation, '200') === '#/components/schemas/CurrentPrincipalResponse', 'current principal success schema is unsupported');
invariant(schemaRef(operations.revokeSession.operation, '200') === '#/components/schemas/SessionRevocationResponse', 'session revocation schema is unsupported');
invariant(operations.logout.operation.responses?.['204'], 'logout must return 204');

const schemas = spec.components?.schemas ?? {};
const schemaRequirements = {
  BuildInfo: [['service', 'version', 'commit', 'builtAt'], ['service', 'version', 'commit', 'builtAt']],
  HealthResponse: [['status', 'service', 'checkedAt'], ['status', 'service', 'checkedAt', 'build']],
  UserPrincipal: [['subject', 'issuer', 'displayName', 'email', 'roles'], ['subject', 'issuer', 'displayName', 'email', 'roles']],
  ServicePrincipal: [['service', 'spiffeId'], ['service', 'spiffeId']],
  PrincipalContext: [['initiatingPrincipal', 'executingServicePrincipal', 'actingOrganizationId', 'audience', 'policyRevision', 'delegationExpiresAt'], ['initiatingPrincipal', 'executingServicePrincipal', 'actingOrganizationId', 'audience', 'policyRevision', 'delegationExpiresAt']],
  SessionView: [['id', 'expiresAt', 'csrfToken', 'revocationObjectiveMs'], ['id', 'expiresAt', 'csrfToken', 'revocationObjectiveMs']],
  CurrentPrincipalResponse: [['principal', 'context', 'session'], ['principal', 'context', 'session']],
  SessionRevocationResponse: [['sessionId', 'revokedAt', 'objectiveMs'], ['sessionId', 'revokedAt', 'objectiveMs']],
  FieldError: [['field', 'message'], ['field', 'message']],
  ProblemDetails: [['type', 'title', 'status', 'detail', 'instance', 'code', 'traceId', 'retryable'], ['type', 'title', 'status', 'detail', 'instance', 'code', 'traceId', 'retryable', 'fieldErrors']],
};
for (const [name, [required, properties]] of Object.entries(schemaRequirements)) {
  const schema = schemas[name];
  invariant(schema?.type === 'object' && schema.additionalProperties === false, `${name} must be a closed object schema`);
  invariant(exactMembers(schema.required, required), `${name} required fields are unsupported`);
  invariant(exactKeys(schema.properties, properties), `${name} properties are unsupported`);
}
invariant(schemas.BuildInfo.properties.service.const === 'platform-gateway', 'BuildInfo.service must be platform-gateway');
invariant(schemas.HealthResponse.properties.status.const === 'ok', 'HealthResponse.status must be ok');
invariant(schemas.ServicePrincipal.properties.service.const === 'platform-gateway', 'ServicePrincipal.service must be platform-gateway');
invariant(schemas.PrincipalContext.properties.audience.const === 'iam-service', 'PrincipalContext.audience must be iam-service');
invariant(schemas.ProblemDetails.properties.code.pattern === '^[A-Z][A-Z0-9_]+$', 'ProblemDetails.code pattern is unsupported');
invariant(schemas.ProblemDetails.properties.traceId.pattern === '^[a-f0-9]{32}$', 'ProblemDetails.traceId pattern is unsupported');
invariant(spec.components?.responses?.Problem?.content?.['application/problem+json']?.schema?.$ref === '#/components/schemas/ProblemDetails', 'public Problem response must use application/problem+json');

const banner = `Generator: platform-contracts@${generatorVersion}; Contract SHA-256: ${digest}`;
const replacements = {
  __CONTRACT_BANNER__: banner,
  __HEALTH_PATH__: operations.getHealth.path,
  __VERSION_PATH__: operations.getVersion.path,
  __LOGIN_PATH__: operations.beginLogin.path,
  __CALLBACK_PATH__: operations.completeLogin.path,
  __PRINCIPAL_PATH__: operations.getCurrentPrincipal.path,
  __LOGOUT_PATH__: operations.logout.path,
  __REVOKE_PATH__: operations.revokeSession.path,
};
function render(template) {
  let output = template;
  for (const [placeholder, value] of Object.entries(replacements)) {
    if (output.includes(placeholder)) output = output.replaceAll(placeholder, value);
  }
  invariant(!/__[A-Z_]+__/.test(output), 'template contains an unresolved placeholder');
  return output;
}
function formatGo(source) {
  const result = spawnSync(gofmtBinary, [], { input: source, encoding: 'utf8', windowsHide: true });
  invariant(!result.error, `gofmt could not start: ${result.error?.message ?? 'unknown error'}`);
  invariant(result.status === 0, `gofmt failed: ${result.stderr || `exit ${result.status}`}`);
  invariant(typeof result.stdout === 'string' && result.stdout.length > 0, 'gofmt returned empty output');
  return result.stdout;
}
async function emit(path, content) {
  let existing = null;
  try { existing = await readFile(path, 'utf8'); } catch (error) { if (error.code !== 'ENOENT') throw error; }
  if (checkOnly) {
    if (existing !== content) { process.stderr.write(`Generated contract drift: ${path}\n`); process.exitCode = 1; }
    return;
  }
  await mkdir(dirname(path), { recursive: true });
  if (existing !== content) await writeFile(path, content, 'utf8');
}
await Promise.all([emit(goOutputPath, formatGo(render(goTemplate))), emit(tsOutputPath, render(tsTemplate))]);
if (!checkOnly) process.stdout.write(`Generated platform contracts (${digest.slice(0, 12)}).\n`);
