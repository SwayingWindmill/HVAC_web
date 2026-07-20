import { mkdir, readFile, readdir, writeFile } from 'node:fs/promises';
import { dirname, relative, resolve } from 'node:path';

const argument = (name) => process.argv.find((value) => value.startsWith(`--${name}=`))?.slice(name.length + 3);
const bindingsPath = argument('bindings');
const outputArgument = argument('output');
if (!bindingsPath || !outputArgument) {
  throw new Error('Usage: node scripts/render-s0-staging.mjs --bindings=<private-json> --output=<directory>');
}

const root = resolve(process.cwd());
const sourceRoot = resolve(root, 'deploy/s0/staging');
const outputRoot = resolve(root, outputArgument);
if (outputRoot.startsWith(sourceRoot)) throw new Error('Rendered staging manifests must not overwrite checked-in templates');

const bindings = JSON.parse(await readFile(resolve(root, bindingsPath), 'utf8'));
if (!bindings || typeof bindings !== 'object' || Array.isArray(bindings)) throw new Error('Bindings must be a JSON object');

async function filesUnder(directory) {
  const entries = await readdir(directory, { withFileTypes: true });
  const files = [];
  for (const entry of entries) {
    const path = resolve(directory, entry.name);
    if (entry.isDirectory()) files.push(...await filesUnder(path));
    else if (entry.name.endsWith('.yaml') || entry.name.endsWith('.yml')) files.push(path);
  }
  return files;
}

const required = new Set();
const templateFiles = await filesUnder(sourceRoot);
for (const source of templateFiles) {
  const text = await readFile(source, 'utf8');
  for (const match of text.matchAll(/"\[([A-Z0-9_]+)\]"/g)) required.add(match[1]);
}

const bindingSchema = JSON.parse(await readFile(resolve(sourceRoot, 'bindings.schema.json'), 'utf8'));
const schemaRequired = new Set(bindingSchema.required || []);
for (const name of required) {
  if (!schemaRequired.has(name)) throw new Error(`bindings.schema.json is missing ${name}`);
}
for (const name of schemaRequired) {
  if (!required.has(name)) throw new Error(`bindings.schema.json contains unused key ${name}`);
}

function descriptorFor(name) {
  const descriptor = bindingSchema.properties?.[name];
  if (!descriptor) throw new Error(`bindings.schema.json has no descriptor for ${name}`);
  if (!descriptor.$ref) return descriptor;
  const definitionName = descriptor.$ref.replace('#/$defs/', '');
  const definition = bindingSchema.$defs?.[definitionName];
  if (!definition) throw new Error(`bindings.schema.json has an unresolved definition for ${name}`);
  return definition;
}

function validateBinding(name, value) {
  const descriptor = descriptorFor(name);
  const actualType = Array.isArray(value) ? 'array' : value === null ? 'null' : typeof value;
  if (descriptor.type && actualType !== descriptor.type) {
    throw new Error(`${name} must be a ${descriptor.type}`);
  }
  if (descriptor.minLength && String(value).length < descriptor.minLength) {
    throw new Error(`${name} is shorter than the schema minimum`);
  }
  if (descriptor.pattern && !new RegExp(descriptor.pattern).test(String(value))) {
    throw new Error(`${name} does not match its schema pattern`);
  }
}

for (const name of required) {
  if (!Object.hasOwn(bindings, name)) throw new Error(`Missing staging binding: ${name}`);
  validateBinding(name, bindings[name]);
  if (name.startsWith('SIGNED_IMAGE_')) {
    const value = String(bindings[name]);
    if (!/@sha256:[a-f0-9]{64}$/.test(value)) throw new Error(`${name} must be an immutable image digest`);
  }
}
for (const name of Object.keys(bindings)) {
  if (!required.has(name)) throw new Error(`Unknown staging binding: ${name}`);
}

await mkdir(outputRoot, { recursive: true });
for (const source of templateFiles) {
  const destination = resolve(outputRoot, relative(sourceRoot, source));
  let rendered = await readFile(source, 'utf8');
  rendered = rendered.replace(/"\[([A-Z0-9_]+)\]"/g, (_match, name) => JSON.stringify(bindings[name]));
  if (/\[[A-Z0-9_]+\]/.test(rendered)) throw new Error(`Unresolved placeholder in ${relative(root, source)}`);
  await mkdir(dirname(destination), { recursive: true });
  await writeFile(destination, rendered);
}

const receipt = {
  renderedAt: new Date().toISOString(),
  source: relative(root, sourceRoot),
  files: templateFiles.map((path) => relative(sourceRoot, path)).sort(),
  bindings: [...required].sort().map((name) => ({ name, sensitive: /URL|KEY|PASSWORD|CERT|BUNDLE|MOUNTS|VOLUMES|BINDINGS/.test(name) })),
};
await writeFile(resolve(outputRoot, 'render-receipt.json'), `${JSON.stringify(receipt, null, 2)}\n`);
console.log(`Rendered ${templateFiles.length} S0 staging manifests to ${relative(root, outputRoot)} without logging binding values.`);
