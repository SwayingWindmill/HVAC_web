import { mkdir, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { setTimeout as pause } from 'node:timers/promises';

const root = resolve(process.cwd());
const output = resolve(root, 'out/s3-local/web-smoke-report.json');
const origin = process.env.S3_LOCAL_WEB_ORIGIN ?? 'http://127.0.0.1:5173';
const deviceID = '018f3e00-3000-7000-8000-000000000001';

async function jsonRequest(path, init = {}) {
  const response = await fetch(`${origin}${path}`, {
    ...init,
    headers: {
      Accept: 'application/json, application/problem+json',
      ...init.headers,
    },
  });
  const payload = await response.json().catch(() => ({}));
  if (!response.ok) {
    throw new Error(`${init.method ?? 'GET'} ${path} failed with ${response.status}: ${JSON.stringify(payload)}`);
  }
  return payload;
}

const principal = await jsonRequest('/api/v1/principal');
const csrfToken = principal?.session?.csrfToken;
if (typeof csrfToken !== 'string' || csrfToken.length < 16) {
  throw new Error('local principal response did not contain a usable CSRF token');
}

const created = await jsonRequest('/api/v1/commands', {
  method: 'POST',
  headers: {
    'Content-Type': 'application/json',
    Origin: origin,
    'X-CSRF-Token': csrfToken,
    'Idempotency-Key': `hvac-web-local-${crypto.randomUUID()}`,
  },
  body: JSON.stringify({
    deviceId: deviceID,
    capability: 'SET_TEMPERATURE_SETPOINT',
    parameters: { setpointC: 24 },
  }),
});
if (typeof created.commandId !== 'string' || created.deviceId !== deviceID) {
  throw new Error(`local Command create response is invalid: ${JSON.stringify(created)}`);
}

let command = created;
for (let attempt = 0; attempt < 300; attempt += 1) {
  command = await jsonRequest(`/api/v1/commands/${encodeURIComponent(created.commandId)}`);
  if (command.status === 'SUCCEEDED') break;
  if (['FAILED', 'REJECTED', 'CANCELLED', 'EXPIRED', 'OUTCOME_UNKNOWN'].includes(command.status)) {
    throw new Error(`local Web Command reached terminal failure ${command.status}: ${JSON.stringify(command)}`);
  }
  await pause(500);
}

const finalTransition = Array.isArray(command.transitions) ? command.transitions.at(-1) : null;
if (command.status !== 'SUCCEEDED' || finalTransition?.reason !== 'ACKNOWLEDGED_AND_REPORTED_STATE_VERIFIED') {
  throw new Error(`local Web Command did not reach SUCCEEDED / VERIFIED: ${JSON.stringify(command)}`);
}

const report = {
  schemaVersion: 1,
  status: 'local-web-integration-passed',
  webOrigin: origin,
  commandId: command.commandId,
  deviceId: command.deviceId,
  setpointC: command.setpointC,
  commandStatus: command.status,
  verificationReason: finalTransition.reason,
  transitionCount: command.transitions.length,
  formalCertificationClaim: false,
  completedAt: new Date().toISOString(),
};
await mkdir(resolve(root, 'out/s3-local'), { recursive: true });
await writeFile(output, `${JSON.stringify(report, null, 2)}\n`);
console.log(JSON.stringify(report, null, 2));
