import { startS0AuthTopology } from './s0-auth-topology.mjs';

const topology = await startS0AuthTopology({ webPort: 5173, gatewayPort: 8080, iamPort: 18444, oidcPort: 19090 });
let stopping = false;

async function shutdown(exitCode = 0) {
  if (stopping) return;
  stopping = true;
  await topology.stop();
  process.exitCode = exitCode;
}

for (const child of topology.processes) {
  child.once('exit', (code, signal) => {
    if (stopping) return;
    console.error(`S0 auth process exited: ${signal ?? code}`);
    void shutdown(code ?? 1);
  });
}

process.once('SIGINT', () => void shutdown(0));
process.once('SIGTERM', () => void shutdown(0));
console.log(`S0 authenticated principal topology ready at ${topology.webURL}`);
console.log('Browser -> HTTPS HVAC Web -> platform-gateway -> mTLS iam-service; OIDC fixture is HTTPS.');
await new Promise(() => {});
