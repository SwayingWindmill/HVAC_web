import { createHash } from 'node:crypto';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';

const generatorVersion = '1.0.0';
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const specPath = resolve(root, 'contracts/http/platform-gateway.openapi.yaml');
const toolingLockPath = resolve(root, 'contracts/http/tooling.lock.json');
const goTemplatePath = resolve(root, 'contracts/http/templates/platformapi.go.tmpl');
const tsTemplatePath = resolve(root, 'contracts/http/templates/platformGateway.ts.tmpl');
const goOutputPath = resolve(root, 'services/platform-gateway/pkg/platformapi/api.gen.go');
const tsOutputPath = resolve(root, 'apps/hvac-web/src/api/generated/platformGateway.gen.ts');
const checkOnly = process.argv.includes('--check');

const [specText, toolingLockText, goTemplate, tsTemplate] = await Promise.all([
  readFile(specPath, 'utf8'),
  readFile(toolingLockPath, 'utf8'),
  readFile(goTemplatePath, 'utf8'),
  readFile(tsTemplatePath, 'utf8'),
]);
const spec = JSON.parse(specText);
const toolingLock = JSON.parse(toolingLockText);
const digest = createHash('sha256').update(specText).digest('hex');

function invariant(condition, message) {
  if (!condition) throw new Error(`Invalid platform Gateway OpenAPI contract: ${message}`);
}

function hasExactMembers(actual, expected) {
  return Array.isArray(actual)
    && actual.length === expected.length
    && expected.every((member) => actual.includes(member));
}

function hasExactKeys(value, expected) {
  return value !== null
    && typeof value === 'object'
    && hasExactMembers(Object.keys(value), expected);
}

invariant(toolingLock.generatorVersion === generatorVersion, 'tooling lock generator version does not match this generator');
invariant(toolingLock.generator === 'scripts/generate-platform-contracts.mjs', 'tooling lock generator path is invalid');
invariant(hasExactMembers(toolingLock.templates, [
  'contracts/http/templates/platformapi.go.tmpl',
  'contracts/http/templates/platformGateway.ts.tmpl',
]), 'tooling lock templates are invalid');
invariant(hasExactMembers(toolingLock.outputs, [
  'services/platform-gateway/pkg/platformapi/api.gen.go',
  'apps/hvac-web/src/api/generated/platformGateway.gen.ts',
]), 'tooling lock outputs are invalid');
invariant(spec.openapi === '3.1.0', 'OpenAPI version must be 3.1.0');

const pathEntries = Object.entries(spec.paths ?? {});
const healthOperation = pathEntries.find(([, pathItem]) => pathItem.get?.operationId === 'getHealth');
const versionOperation = pathEntries.find(([, pathItem]) => pathItem.get?.operationId === 'getVersion');
invariant(healthOperation, 'getHealth operation is required');
invariant(versionOperation, 'getVersion operation is required');
const [healthPath, healthPathItem] = healthOperation;
const [versionPath, versionPathItem] = versionOperation;
invariant(healthPath === '/api/v1/health', 'getHealth path is unsupported by generator version 1');
invariant(versionPath === '/api/v1/version', 'getVersion path is unsupported by generator version 1');
invariant(healthPathItem.get.responses?.['200']?.content?.['application/json']?.schema?.$ref === '#/components/schemas/HealthResponse', 'getHealth success schema is unsupported');
invariant(versionPathItem.get.responses?.['200']?.content?.['application/json']?.schema?.$ref === '#/components/schemas/BuildInfo', 'getVersion success schema is unsupported');
const includeBuildParameter = healthPathItem.get.parameters?.find((parameter) => parameter.name === 'includeBuild');
invariant(includeBuildParameter?.in === 'query' && includeBuildParameter.required === false, 'includeBuild must be an optional query parameter');
invariant(includeBuildParameter.schema?.type === 'boolean', 'includeBuild must be a boolean query parameter');

const schemas = spec.components?.schemas ?? {};
const schemaRequirements = {
  BuildInfo: {
    required: ['service', 'version', 'commit', 'builtAt'],
    properties: ['service', 'version', 'commit', 'builtAt'],
  },
  HealthResponse: {
    required: ['status', 'service', 'checkedAt'],
    properties: ['status', 'service', 'checkedAt', 'build'],
  },
  FieldError: {
    required: ['field', 'message'],
    properties: ['field', 'message'],
  },
  ProblemDetails: {
    required: ['type', 'title', 'status', 'detail', 'instance', 'code', 'traceId', 'retryable'],
    properties: ['type', 'title', 'status', 'detail', 'instance', 'code', 'traceId', 'retryable', 'fieldErrors'],
  },
};
for (const [schemaName, requirement] of Object.entries(schemaRequirements)) {
  const schema = schemas[schemaName];
  invariant(schema?.type === 'object' && schema.additionalProperties === false, `${schemaName} must be a closed object schema`);
  invariant(hasExactMembers(schema.required, requirement.required), `${schemaName} required fields are unsupported by generator version 1`);
  invariant(hasExactKeys(schema.properties, requirement.properties), `${schemaName} properties are unsupported by generator version 1`);
}
invariant(schemas.BuildInfo.properties.service.const === 'platform-gateway', 'BuildInfo.service must be platform-gateway');
invariant(schemas.HealthResponse.properties.status.const === 'ok', 'HealthResponse.status must be ok');
invariant(schemas.HealthResponse.properties.service.const === 'platform-gateway', 'HealthResponse.service must be platform-gateway');
invariant(schemas.HealthResponse.properties.checkedAt.format === 'date-time', 'HealthResponse.checkedAt must use date-time');
invariant(schemas.HealthResponse.properties.build.$ref === '#/components/schemas/BuildInfo', 'HealthResponse.build must reference BuildInfo');
invariant(schemas.ProblemDetails.properties.code.pattern === '^[A-Z][A-Z0-9_]+$', 'ProblemDetails.code pattern is unsupported');
invariant(schemas.ProblemDetails.properties.traceId.pattern === '^[a-f0-9]{32}$', 'ProblemDetails.traceId pattern is unsupported');
invariant(schemas.ProblemDetails.properties.retryable.type === 'boolean', 'ProblemDetails.retryable must be boolean');
invariant(schemas.ProblemDetails.properties.fieldErrors.items?.$ref === '#/components/schemas/FieldError', 'ProblemDetails.fieldErrors must reference FieldError');
invariant(spec.components?.responses?.Problem?.content?.['application/problem+json']?.schema?.$ref === '#/components/schemas/ProblemDetails', 'public Problem response must use application/problem+json');

const banner = `Generator: platform-contracts@${generatorVersion}; Contract SHA-256: ${digest}`;
const replacements = {
  __CONTRACT_BANNER__: banner,
  __HEALTH_PATH__: healthPath,
  __VERSION_PATH__: versionPath,
};

function render(template) {
  let output = template;
  for (const [placeholder, value] of Object.entries(replacements)) {
    invariant(output.includes(placeholder), `template is missing ${placeholder}`);
    output = output.replaceAll(placeholder, value);
  }
  invariant(!/__[A-Z_]+__/.test(output), 'template contains an unresolved placeholder');
  return output;
}

async function emit(path, content) {
  let existing = null;
  try {
    existing = await readFile(path, 'utf8');
  } catch (error) {
    if (error.code !== 'ENOENT') throw error;
  }
  if (checkOnly) {
    if (existing !== content) {
      process.stderr.write(`Generated contract drift: ${path}\n`);
      process.exitCode = 1;
    }
    return;
  }
  await mkdir(dirname(path), { recursive: true });
  if (existing !== content) await writeFile(path, content, 'utf8');
}

await Promise.all([
  emit(goOutputPath, render(goTemplate)),
  emit(tsOutputPath, render(tsTemplate)),
]);

if (!checkOnly) process.stdout.write(`Generated platform contracts (${digest.slice(0, 12)}).\n`);
