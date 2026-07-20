import { startS0DurableTopology } from './s0-durable-topology.mjs';

const topology = await startS0DurableTopology({
  oidcPort: 19094,
  iamPort: 18444,
  auditPort: 18446,
  gatewayPort: 8080,
  webPort: 5173,
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
console.log('Browser -> HTTPS HVAC Web -> Gateway -> PostgreSQL / mTLS IAM / mTLS Audit Ledger; Outbox -> Redpanda -> Transactional Inbox.');
await new Promise(() => {});
