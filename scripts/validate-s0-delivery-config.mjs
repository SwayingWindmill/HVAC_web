import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const fileArgument = process.argv.find((value) => value.startsWith('--file='));
const filePath = resolve(process.cwd(), fileArgument?.slice('--file='.length) || 'deploy/s0/local.env.example');
const text = await readFile(filePath, 'utf8');
const config = {};

for (const rawLine of text.split(/\r?\n/)) {
  const line = rawLine.trim();
  if (!line || line.startsWith('#')) continue;
  const separator = line.indexOf('=');
  if (separator < 1) throw new Error(`Invalid environment line in ${filePath}: ${rawLine}`);
  const name = line.slice(0, separator).trim();
  const value = line.slice(separator + 1).trim().replace(/^['"]|['"]$/g, '');
  if (Object.hasOwn(config, name)) throw new Error(`Duplicate environment key: ${name}`);
  config[name] = value;
}

const required = [
  'S0_ENV',
  'S0_CONFIG_REVISION',
  'S0_TRUST_DOMAIN',
  'OIDC_ISSUER',
  'OIDC_CLIENT_ID',
  'IAM_AUDIENCE',
  'AUDIT_AUDIENCE',
  'CONTROL_BACKBONE_BROKERS',
  'OTEL_EXPORTER_OTLP_ENDPOINT',
  'POSTGRES_ENDPOINT',
  'ALLOW_PRODUCTION_EGRESS',
];
for (const name of required) {
  if (!config[name]) throw new Error(`${name} is required in ${filePath}`);
}

if (!['local', 'test', 'staging'].includes(config.S0_ENV)) {
  throw new Error('S0_ENV must be local, test or staging for this delivery profile');
}
if (!/^\d+$/.test(config.S0_CONFIG_REVISION)) throw new Error('S0_CONFIG_REVISION must be an integer');
if (!/^[a-z0-9][a-z0-9.-]{1,126}[a-z0-9]$/.test(config.S0_TRUST_DOMAIN)) {
  throw new Error('S0_TRUST_DOMAIN must be a DNS trust domain');
}
for (const name of ['OIDC_CLIENT_ID', 'IAM_AUDIENCE', 'AUDIT_AUDIENCE']) {
  if (!/^[A-Za-z0-9._:-]{3,128}$/.test(config[name])) throw new Error(`${name} is invalid`);
}
for (const name of ['OIDC_ISSUER', 'OTEL_EXPORTER_OTLP_ENDPOINT']) {
  const url = new URL(config[name]);
  if (!['http:', 'https:'].includes(url.protocol)) throw new Error(`${name} must use HTTP(S)`);
  if (url.username || url.password) throw new Error(`${name} must not embed credentials`);
}
if (!/^[A-Za-z0-9.-]+:\d+(,[A-Za-z0-9.-]+:\d+)*$/.test(config.CONTROL_BACKBONE_BROKERS)) {
  throw new Error('CONTROL_BACKBONE_BROKERS must be a comma-separated host:port list');
}
if (!/^[A-Za-z0-9.-]+:\d+$/.test(config.POSTGRES_ENDPOINT)) {
  throw new Error('POSTGRES_ENDPOINT must be host:port without credentials');
}
if (config.ALLOW_PRODUCTION_EGRESS !== 'false') {
  throw new Error('Local/test/staging profiles must set ALLOW_PRODUCTION_EGRESS=false');
}
for (const name of ['THINGSBOARD_BASE_URL', 'WEBHOOK_BASE_URL']) {
  if ((config[name] || '').trim() !== '') throw new Error(`${name} must be empty outside production`);
}
for (const [name, value] of Object.entries(config)) {
  if (/password|secret|private_key|token/i.test(name) && value) {
    throw new Error(`${name} must be supplied by a runtime Secret, not the checked-in environment contract`);
  }
  if (/\.example\.com|prod(uction)?[.-]/i.test(value)) {
    throw new Error(`${name} appears to reference a production or example external endpoint`);
  }
}

console.log(`S0 ${config.S0_ENV} delivery configuration revision ${config.S0_CONFIG_REVISION} is valid.`);
