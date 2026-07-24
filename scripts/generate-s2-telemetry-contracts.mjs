import { createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { existsSync } from 'node:fs';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import process from 'node:process';

const root = resolve(process.cwd());
const generatorVersion = '1.0.0';
const checkOnly = process.argv.includes('--check');
const openAPIPath = resolve(root, 'contracts/http/s2-telemetry-public.openapi.json');
const eventPath = resolve(root, 'contracts/events/s2-device-observation-publication.v1.schema.json');
const toolingLockPath = resolve(root, 'contracts/http/s2-tooling.lock.json');
const compatibilityPath = resolve(root, 'contracts/telemetry/s2-baseline-compatibility.v1.json');
const goOutputPath = resolve(root, 'services/telemetry-runtime-service/pkg/telemetryapi/api.gen.go');
const gatewayGoOutputPath = resolve(root, 'services/platform-gateway/pkg/s2telemetryapi/api.gen.go');
const tsOutputPath = resolve(root, 'apps/hvac-web/src/api/generated/s2Telemetry.gen.ts');
const windowsGofmtPath = 'C:\\Program Files\\Go\\bin\\gofmt.exe';
const gofmtBinary = process.env.GOFMT_BINARY ?? (process.platform === 'win32' && existsSync(windowsGofmtPath) ? windowsGofmtPath : 'gofmt');

const [openAPIText, eventText, toolingLockText, compatibilityText] = await Promise.all([
  readFile(openAPIPath, 'utf8'),
  readFile(eventPath, 'utf8'),
  readFile(toolingLockPath, 'utf8'),
  readFile(compatibilityPath, 'utf8'),
]);
const openAPI = JSON.parse(openAPIText);
const eventContract = JSON.parse(eventText);
const toolingLock = JSON.parse(toolingLockText);
const compatibility = JSON.parse(compatibilityText);
const openAPIDigest = createHash('sha256').update(openAPIText).digest('hex');
const eventDigest = createHash('sha256').update(eventText).digest('hex');

function invariant(condition, message) {
  if (!condition) throw new Error(`Invalid S2 telemetry contract: ${message}`);
}

function exact(actual, expected) {
  return Array.isArray(actual)
    && actual.length === expected.length
    && actual.every((value, index) => value === expected[index]);
}

function pascalCase(value) {
  return String(value)
    .replace(/(^|[^A-Za-z0-9]+)([A-Za-z0-9])/g, (_, _separator, character) => character.toUpperCase())
    .replace(/^([a-z])/, (character) => character.toUpperCase());
}

function constantName(typeName, value) {
  const suffix = String(value)
    .toLowerCase()
    .replace(/[^a-z0-9]+(.)/g, (_, character) => character.toUpperCase())
    .replace(/^./, (character) => character.toUpperCase());
  return `${typeName}${suffix}`;
}

function refName(ref) {
  return String(ref).split('/').at(-1);
}

function normalizeEventSchema(schema) {
  if (Array.isArray(schema)) return schema.map(normalizeEventSchema);
  if (!schema || typeof schema !== 'object') return schema;
  const output = {};
  for (const [key, value] of Object.entries(schema)) {
    if (key === '$ref' && typeof value === 'string' && value.startsWith('#/$defs/')) {
      output[key] = value.replace('#/$defs/', '#/components/schemas/');
    } else {
      output[key] = normalizeEventSchema(value);
    }
  }
  return output;
}

function findOperation(operationId) {
  for (const [path, pathItem] of Object.entries(openAPI.paths ?? {})) {
    for (const method of ['get', 'post', 'put', 'patch', 'delete']) {
      if (pathItem?.[method]?.operationId === operationId) {
        return { operationId, method: method.toUpperCase(), path, operation: pathItem[method] };
      }
    }
  }
  return null;
}

invariant(toolingLock.schemaVersion === 1, 'tooling lock schema version drifted');
invariant(toolingLock.generatorVersion === generatorVersion, 'tooling lock generator version drifted');
invariant(toolingLock.generator === 'scripts/generate-s2-telemetry-contracts.mjs', 'tooling lock generator path drifted');
invariant(exact(toolingLock.inputs, [
  'contracts/http/s2-telemetry-public.openapi.json',
  'contracts/events/s2-device-observation-publication.v1.schema.json',
  'contracts/telemetry/s2-baseline-compatibility.v1.json',
]), 'tooling lock inputs drifted');
invariant(exact(toolingLock.outputs, [
  'services/telemetry-runtime-service/pkg/telemetryapi/api.gen.go',
  'services/platform-gateway/pkg/s2telemetryapi/api.gen.go',
  'apps/hvac-web/src/api/generated/s2Telemetry.gen.ts',
]), 'tooling lock outputs drifted');
invariant(openAPI.openapi === '3.1.0', 'OpenAPI must remain 3.1.0');
invariant(openAPI.info?.version === compatibility.currentContractVersion, 'OpenAPI version differs from compatibility lock');
invariant(openAPI['x-activation-status'] === 'expand-baseline', 'OpenAPI must be in expand-baseline state');
invariant(eventContract['x-activation-status'] === 'expand-baseline', 'publication schema must be in expand-baseline state');
invariant(compatibility.activationStatus === 'expand-baseline', 'compatibility lock must be in expand-baseline state');

const operations = compatibility.operations.map((expected) => {
  const actual = findOperation(expected.operationId);
  invariant(actual, `${expected.operationId} is missing`);
  invariant(actual.method === expected.method && actual.path === expected.path, `${expected.operationId} method/path drifted`);
  const requestRef = actual.operation.requestBody?.content?.['application/json']?.schema?.$ref;
  if (expected.requestSchema) invariant(refName(requestRef) === expected.requestSchema, `${expected.operationId} request schema drifted`);
  const responseRef = actual.operation.responses?.['200']?.content?.['application/json']?.schema?.$ref
    ?? openAPI.components?.responses?.[refName(actual.operation.responses?.['200']?.$ref)]?.content?.['application/json']?.schema?.$ref;
  invariant(refName(responseRef) === expected.responseSchema, `${expected.operationId} response schema drifted`);
  return { ...expected, operation: actual.operation };
});

const openAPISchemas = openAPI.components?.schemas ?? {};
for (const [schemaName, requiredProperties] of Object.entries(compatibility.requiredSchemaProperties ?? {})) {
  const schema = openAPISchemas[schemaName];
  invariant(schema?.type === 'object' && schema.additionalProperties === false, `${schemaName} must remain a closed object`);
  for (const property of requiredProperties) {
    invariant(schema.required?.includes(property), `${schemaName}.${property} is no longer required`);
    invariant(schema.properties?.[property], `${schemaName}.${property} was removed`);
  }
}
invariant(eventContract.title === compatibility.publication.title, 'publication title drifted');
invariant(eventContract.properties?.kind?.const === compatibility.publication.kind, 'publication kind drifted');
for (const property of compatibility.publication.requiredProperties) {
  invariant(eventContract.required?.includes(property) && eventContract.properties?.[property], `publication.${property} was removed`);
}
for (const property of compatibility.publication.forbiddenProperties) {
  invariant(!eventContract.properties?.[property], `publication must not expose ${property}`);
}

for (const [name, eventDefinition] of Object.entries(eventContract.$defs ?? {})) {
  if (!openAPISchemas[name]) continue;
  invariant(
    JSON.stringify(normalizeEventSchema(eventDefinition)) === JSON.stringify(openAPISchemas[name]),
    `shared schema ${name} differs between OpenAPI and publication`,
  );
}

const schemas = {
  ...openAPISchemas,
  DeviceObservationPublication: normalizeEventSchema({
    type: eventContract.type,
    additionalProperties: eventContract.additionalProperties,
    required: eventContract.required,
    properties: eventContract.properties,
  }),
};

function schemaIsNullable(schema) {
  if (!schema || typeof schema !== 'object') return false;
  if (Array.isArray(schema.type) && schema.type.includes('null')) return true;
  if (Array.isArray(schema.oneOf) && schema.oneOf.some((member) => member.type === 'null')) return true;
  if (schema.$ref) return schemaIsNullable(schemas[refName(schema.$ref)]);
  return false;
}

function stripNullable(schema) {
  if (Array.isArray(schema.type)) return { ...schema, type: schema.type.find((value) => value !== 'null') };
  if (Array.isArray(schema.oneOf)) {
    const nonNull = schema.oneOf.filter((member) => member.type !== 'null');
    if (nonNull.length === 1) return nonNull[0];
  }
  return schema;
}

function goBaseType(schema) {
  if (schema.$ref) return refName(schema.$ref);
  const normalized = stripNullable(schema);
  if (normalized !== schema) return goBaseType(normalized);
  if (Array.isArray(schema.oneOf)) return 'json.RawMessage';
  if (schema.type === 'string') return 'string';
  if (schema.type === 'integer') return schema.format === 'int64' ? 'int64' : 'int';
  if (schema.type === 'number') return 'float64';
  if (schema.type === 'boolean') return 'bool';
  if (schema.type === 'array') return `[]${goBaseType(schema.items ?? {})}`;
  if (schema.type === 'object') return 'map[string]any';
  return 'json.RawMessage';
}

function goPropertyType(schema, required) {
  const nullable = schemaIsNullable(schema);
  const base = goBaseType(schema);
  const pointerEligible = !base.startsWith('[]') && !base.startsWith('map[') && base !== 'json.RawMessage';
  if ((nullable || !required) && pointerEligible && !base.startsWith('*')) return `*${base}`;
  return base;
}

function tsType(schema) {
  if (schema.$ref) return refName(schema.$ref);
  if (Array.isArray(schema.oneOf)) return schema.oneOf.map(tsType).join(' | ');
  if (Array.isArray(schema.type)) return schema.type.map((value) => value === 'null' ? 'null' : tsType({ ...schema, type: value })).join(' | ');
  if (schema.const !== undefined) return JSON.stringify(schema.const);
  if (Array.isArray(schema.enum)) return schema.enum.map((value) => JSON.stringify(value)).join(' | ');
  if (schema.type === 'string') return 'string';
  if (schema.type === 'integer' || schema.type === 'number') return 'number';
  if (schema.type === 'boolean') return 'boolean';
  if (schema.type === 'array') return `Array<${tsType(schema.items ?? {})}>`;
  if (schema.type === 'object') return 'Record<string, unknown>';
  if (schema.type === 'null') return 'null';
  return 'unknown';
}

function renderGoNamedSchema(name, schema) {
  if (name === 'TelemetryValue') return `type ${name} = json.RawMessage\n`;
  if (name === 'TelemetryKeyState') {
    return `type TelemetryKeyState struct {\n\tPresent *TelemetryPresentState\n\tMissing *TelemetryMissingState\n}\n\nfunc (value TelemetryKeyState) MarshalJSON() ([]byte, error) {\n\tswitch {\n\tcase value.Present != nil && value.Missing == nil:\n\t\treturn json.Marshal(value.Present)\n\tcase value.Missing != nil && value.Present == nil:\n\t\treturn json.Marshal(value.Missing)\n\tdefault:\n\t\treturn nil, fmt.Errorf("telemetry key state must contain exactly one variant")\n\t}\n}\n\nfunc (value *TelemetryKeyState) UnmarshalJSON(data []byte) error {\n\tvar discriminator struct { State string \`json:"state"\` }\n\tif err := json.Unmarshal(data, &discriminator); err != nil { return err }\n\tswitch discriminator.State {\n\tcase "PRESENT":\n\t\tvar present TelemetryPresentState\n\t\tif err := json.Unmarshal(data, &present); err != nil { return err }\n\t\tvalue.Present, value.Missing = &present, nil\n\tcase "MISSING":\n\t\tvar missing TelemetryMissingState\n\t\tif err := json.Unmarshal(data, &missing); err != nil { return err }\n\t\tvalue.Present, value.Missing = nil, &missing\n\tdefault:\n\t\treturn fmt.Errorf("unsupported telemetry key state %q", discriminator.State)\n\t}\n\treturn nil\n}\n`;
  }
  if (name === 'BatchObservationResult') {
    return `type BatchObservationResult struct {\n\tSuccess *BatchObservationSuccess\n\tFailure *BatchObservationFailure\n}\n\nfunc (value BatchObservationResult) MarshalJSON() ([]byte, error) {\n\tswitch {\n\tcase value.Success != nil && value.Failure == nil:\n\t\treturn json.Marshal(value.Success)\n\tcase value.Failure != nil && value.Success == nil:\n\t\treturn json.Marshal(value.Failure)\n\tdefault:\n\t\treturn nil, fmt.Errorf("batch observation result must contain exactly one variant")\n\t}\n}\n\nfunc (value *BatchObservationResult) UnmarshalJSON(data []byte) error {\n\tvar discriminator struct { Status string \`json:"status"\` }\n\tif err := json.Unmarshal(data, &discriminator); err != nil { return err }\n\tswitch discriminator.Status {\n\tcase "OK":\n\t\tvar success BatchObservationSuccess\n\t\tif err := json.Unmarshal(data, &success); err != nil { return err }\n\t\tvalue.Success, value.Failure = &success, nil\n\tcase "ERROR":\n\t\tvar failure BatchObservationFailure\n\t\tif err := json.Unmarshal(data, &failure); err != nil { return err }\n\t\tvalue.Success, value.Failure = nil, &failure\n\tdefault:\n\t\treturn fmt.Errorf("unsupported batch observation result %q", discriminator.Status)\n\t}\n\treturn nil\n}\n`;
  }
  const enumValues = schema.enum?.filter((value) => value !== null) ?? schema['x-known-codes'];
  if (schema.type === 'string' || (Array.isArray(schema.type) && schema.type.includes('string'))) {
    let output = `type ${name} string\n`;
    if (Array.isArray(enumValues) && enumValues.length > 0) {
      output += `\nconst (\n${enumValues.map((value) => `\t${constantName(name, value)} ${name} = ${JSON.stringify(value)}`).join('\n')}\n)\n`;
    }
    return output;
  }
  if (schema.type === 'integer') return `type ${name} ${schema.format === 'int64' ? 'int64' : 'int'}\n`;
  if (Array.isArray(schema.oneOf)) return `type ${name} = json.RawMessage\n`;
  if (schema.type === 'object') {
    const required = new Set(schema.required ?? []);
    const fields = Object.entries(schema.properties ?? {}).map(([propertyName, propertySchema]) => {
      const isRequired = required.has(propertyName);
      const tag = isRequired ? propertyName : `${propertyName},omitempty`;
      return `\t${pascalCase(propertyName)} ${goPropertyType(propertySchema, isRequired)} \`json:"${tag}"\``;
    });
    return `type ${name} struct {\n${fields.join('\n')}\n}\n`;
  }
  return `type ${name} = ${goBaseType(schema)}\n`;
}

function renderTSNamedSchema(name, schema) {
  if (name === 'TelemetryValue') {
    return `export type TelemetryValue = number | string | boolean | Record<string, unknown> | Array<number | string | boolean>;\n`;
  }
  if (Array.isArray(schema.oneOf)) return `export type ${name} = ${schema.oneOf.map(tsType).join(' | ')};\n`;
  const enumValues = schema.enum ?? schema['x-known-codes'];
  if (schema.type === 'string' || (Array.isArray(schema.type) && schema.type.includes('string'))) {
    const type = Array.isArray(enumValues) && enumValues.length > 0
      ? enumValues.map((value) => JSON.stringify(value)).join(' | ')
      : (Array.isArray(schema.type) && schema.type.includes('null') ? 'string | null' : 'string');
    return `export type ${name} = ${type};\n`;
  }
  if (schema.type === 'integer' || schema.type === 'number') return `export type ${name} = number;\n`;
  if (schema.type === 'object') {
    const required = new Set(schema.required ?? []);
    const fields = Object.entries(schema.properties ?? {}).map(([propertyName, propertySchema]) => {
      const optional = required.has(propertyName) ? '' : '?';
      return `  ${propertyName}${optional}: ${tsType(propertySchema)};`;
    });
    return `export interface ${name} {\n${fields.join('\n')}\n}\n`;
  }
  return `export type ${name} = ${tsType(schema)};\n`;
}

const banner = `OpenAPI SHA-256: ${openAPIDigest}; Publication SHA-256: ${eventDigest}; Generator: s2-telemetry-contracts@${generatorVersion}`;
const goSchemas = Object.entries(schemas).map(([name, schema]) => renderGoNamedSchema(name, schema)).join('\n');
const goOperations = operations.map((operation) => `\t{OperationID: ${JSON.stringify(operation.operationId)}, Method: ${JSON.stringify(operation.method)}, Path: ${JSON.stringify(operation.path)}}`).join(',\n');
const goAliases = operations.flatMap((operation) => {
  const name = pascalCase(operation.operationId);
  const aliases = [];
  if (operation.requestSchema) aliases.push(`type ${name}Request = ${operation.requestSchema}`);
  aliases.push(`type ${name}Response = ${operation.responseSchema}`);
  return aliases;
}).join('\n');
function renderGoSource(packageName, includeServerInterface = false) {
  const imports = ['"encoding/json"', '"fmt"'];
  if (includeServerInterface) imports.push('"net/http"');
  const serverInterface = includeServerInterface ? `
const (
\tGetDeviceObservationSnapshotPathTemplate = "/api/v1/devices/{deviceId}/observation-snapshot"
\tBatchGetDeviceObservationSnapshotsPath = "/api/v1/telemetry/observation-snapshots:batchGet"
\tBootstrapTelemetrySubscriptionsPath = "/api/v1/telemetry/subscriptions:bootstrap"
\tCheckpointTelemetryRecoveryCursorsPath = "/api/v1/telemetry/recovery-cursors:checkpoint"
)

type GetDeviceObservationSnapshotParams struct {
\tKeys []TelemetryKey
}

type ServerInterface interface {
\tGetDeviceObservationSnapshot(http.ResponseWriter, *http.Request, string, GetDeviceObservationSnapshotParams)
\tBatchGetDeviceObservationSnapshots(http.ResponseWriter, *http.Request, BatchGetObservationSnapshotsRequest)
\tBootstrapTelemetrySubscriptions(http.ResponseWriter, *http.Request, SubscriptionBootstrapRequest)
\tCheckpointTelemetryRecoveryCursors(http.ResponseWriter, *http.Request, RecoveryCursorCheckpointRequest)
}
` : '';
  return `// Code generated by scripts/generate-s2-telemetry-contracts.mjs; DO NOT EDIT.\n// ${banner} // gitleaks:allow\n\npackage ${packageName}\n\nimport (\n\t${imports.join('\n\t')}\n)\n\nconst (\n\tOpenAPIContractSHA256 = ${JSON.stringify(openAPIDigest)} // gitleaks:allow\n\tPublicationContractSHA256 = ${JSON.stringify(eventDigest)} // gitleaks:allow\n)\n\ntype OperationDescriptor struct {\n\tOperationID string\n\tMethod string\n\tPath string\n}\n\nvar Operations = [...]OperationDescriptor{\n${goOperations},\n}\n${serverInterface}\n${goAliases}\n\n${goSchemas}`;
}
const goSource = renderGoSource('telemetryapi');
const gatewayGoSource = renderGoSource('s2telemetryapi', true);

const tsSchemas = Object.entries(schemas).map(([name, schema]) => renderTSNamedSchema(name, schema)).join('\n');
const tsOperations = operations.map((operation) => `  ${operation.operationId}: { method: ${JSON.stringify(operation.method)}, path: ${JSON.stringify(operation.path)} },`).join('\n');
const tsOperationMap = operations.map((operation) => {
  const request = operation.requestSchema ?? 'never';
  return `  ${operation.operationId}: { request: ${request}; response: ${operation.responseSchema} };`;
}).join('\n');
const tsSource = `// Code generated by scripts/generate-s2-telemetry-contracts.mjs; DO NOT EDIT.\n// ${banner} // gitleaks:allow\n\nexport const S2_TELEMETRY_OPENAPI_SHA256 = ${JSON.stringify(openAPIDigest)} as const; // gitleaks:allow\nexport const S2_TELEMETRY_PUBLICATION_SHA256 = ${JSON.stringify(eventDigest)} as const; // gitleaks:allow\n\nexport const S2_TELEMETRY_OPERATIONS = {\n${tsOperations}\n} as const;\n\nexport interface S2TelemetryOperationMap {\n${tsOperationMap}\n}\n\n${tsSchemas}\nexport interface S2TelemetryRequestOptions {\n  signal?: AbortSignal;\n  requestId?: string;\n  csrfToken?: string;\n}\n\nexport class S2TelemetryClientError extends Error {\n  readonly problem: ProblemDetails;\n\n  constructor(problem: ProblemDetails) {\n    super(problem.detail);\n    this.name = "S2TelemetryClientError";\n    this.problem = problem;\n  }\n}\n\nexport interface S2TelemetryClient {\n  getDeviceObservationSnapshot(deviceId: string, keys?: Array<string>, options?: S2TelemetryRequestOptions): Promise<DeviceObservationSnapshot>;\n  batchGetDeviceObservationSnapshots(request: BatchGetObservationSnapshotsRequest, options?: S2TelemetryRequestOptions): Promise<BatchGetObservationSnapshotsResponse>;\n  bootstrapTelemetrySubscriptions(request: SubscriptionBootstrapRequest, options?: S2TelemetryRequestOptions): Promise<SubscriptionBootstrapResponse>;\n  checkpointTelemetryRecoveryCursors(request: RecoveryCursorCheckpointRequest, options?: S2TelemetryRequestOptions): Promise<RecoveryCursorCheckpointResponse>;\n}\n\nexport function createS2TelemetryClient(baseURL = "", fetchImplementation: typeof fetch = fetch): S2TelemetryClient {\n  const requestJSON = async <T>(path: string, init: RequestInit): Promise<T> => {\n    const response = await fetchImplementation(baseURL + path, { credentials: "include", ...init });\n    const payload = await response.json() as T | ProblemDetails;\n    if (!response.ok) throw new S2TelemetryClientError(payload as ProblemDetails);\n    return payload as T;\n  };\n  const headers = (options?: S2TelemetryRequestOptions, post = false): Record<string, string> => {\n    const value: Record<string, string> = { Accept: "application/json, application/problem+json" };\n    if (post) value["Content-Type"] = "application/json";\n    if (options?.requestId) value["X-Request-ID"] = options.requestId;\n    if (post && options?.csrfToken) value["X-CSRF-Token"] = options.csrfToken;\n    return value;\n  };\n  return {\n    getDeviceObservationSnapshot: (deviceId, keys = [], options) => {\n      const query = keys.length > 0 ? "?keys=" + encodeURIComponent(keys.join(",")) : "";\n      return requestJSON<DeviceObservationSnapshot>("/api/v1/devices/" + encodeURIComponent(deviceId) + "/observation-snapshot" + query, { method: "GET", headers: headers(options), signal: options?.signal });\n    },\n    batchGetDeviceObservationSnapshots: (request, options) => requestJSON<BatchGetObservationSnapshotsResponse>(\n      "/api/v1/telemetry/observation-snapshots:batchGet",\n      { method: "POST", headers: headers(options, true), body: JSON.stringify(request), signal: options?.signal },\n    ),\n    bootstrapTelemetrySubscriptions: (request, options) => requestJSON<SubscriptionBootstrapResponse>(\n      "/api/v1/telemetry/subscriptions:bootstrap",\n      { method: "POST", headers: headers(options, true), body: JSON.stringify(request), signal: options?.signal },\n    ),\n    checkpointTelemetryRecoveryCursors: (request, options) => requestJSON<RecoveryCursorCheckpointResponse>(\n      "/api/v1/telemetry/recovery-cursors:checkpoint",\n      { method: "POST", headers: headers(options, true), body: JSON.stringify(request), signal: options?.signal },\n    ),\n  };\n}\n`;

function formatGo(source) {
  const result = spawnSync(gofmtBinary, [], { input: source, encoding: 'utf8', windowsHide: true });
  invariant(!result.error, `gofmt could not start: ${result.error?.message ?? 'unknown error'}`);
  invariant(result.status === 0, `gofmt failed: ${result.stderr || result.status}`);
  return result.stdout;
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
      process.stderr.write(`Generated S2 contract drift: ${path}\n`);
      process.exitCode = 1;
    }
    return;
  }
  await mkdir(dirname(path), { recursive: true });
  if (existing !== content) await writeFile(path, content, 'utf8');
}

await Promise.all([
  emit(goOutputPath, formatGo(goSource)),
  emit(gatewayGoOutputPath, formatGo(gatewayGoSource)),
  emit(tsOutputPath, tsSource),
]);
if (!checkOnly) process.stdout.write(`Generated S2 telemetry contracts (${openAPIDigest.slice(0, 12)} / ${eventDigest.slice(0, 12)}).\n`);
