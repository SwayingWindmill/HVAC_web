import { spawnSync } from 'node:child_process';
import { startS0DurableTopology } from './s0-durable-topology.mjs';

const validation = spawnSync(process.execPath, [
  'scripts/validate-s0-delivery-config.mjs',
  '--file=deploy/s0/local.env.example',
], { cwd: process.cwd(), stdio: 'inherit', windowsHide: true });
if (validation.error || validation.status !== 0) {
  throw new Error(`S0 local delivery configuration is invalid: ${validation.error?.message ?? validation.status}`);
}

const topology = await startS0DurableTopology({
  oidcPort: 19094,
  iamPort: 18444,
  auditPort: 18446,
  gatewayPort: 8080,
  webPort: 5173,
  captureTelemetry: false,
});
let stopping = false;

async function shutdown(exitCode = 0) {
  if (stopping) return;
  stopping = true;
  await topology.stop();
  process.exitCode = exitCode;
}

for (const child of Object.values(topology.services)) {
  child?.once('exit', (code, signal) => {
    if (stopping) return;
    console.error(`S0 durable process exited: ${signal ?? code}`);
    void shutdown(code ?? 1);
  });
}

process.once('SIGINT', () => void shutdown(0));
process.once('SIGTERM', () => void shutdown(0));
console.log(`S0 durable Session and Audit topology ready at ${topology.webURL}`);
console.log('Browser -> HTTPS HVAC Web -> Go Gateway -> PostgreSQL / mTLS IAM / mTLS Audit Ledger; Outbox -> Redpanda -> Transactional Inbox; telemetry -> OpenTelemetry Collector.');
await new Promise(() => {});
