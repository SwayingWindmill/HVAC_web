import { createServer } from 'node:http';
import { CopilotRuntime } from '@copilotkit/runtime/v2';
import { createCopilotNodeListener } from '@copilotkit/runtime/v2/node';
import { LangGraphAgent } from '@copilotkit/runtime/langgraph';

const host = process.env.AI_RUNTIME_HOST?.trim() || '127.0.0.1';
const port = Number.parseInt(process.env.AI_RUNTIME_PORT || '3001', 10);
const basePath = process.env.AI_RUNTIME_BASE_PATH?.trim() || '/api/v1/copilotkit';
const deploymentUrl = (
  process.env.ENERGY_AGENT_URL
  || process.env.AGENT_URL
  || 'http://127.0.0.1:8123'
).replace(/\/$/, '');
const graphId = process.env.ENERGY_AGENT_GRAPH_ID?.trim() || 'sample_agent';
const langsmithApiKey = process.env.LANGSMITH_API_KEY?.trim();

if (!Number.isInteger(port) || port < 1 || port > 65535) {
  throw new Error(`AI_RUNTIME_PORT must be a valid TCP port, received: ${process.env.AI_RUNTIME_PORT}`);
}

const runtime = new CopilotRuntime({
  agents: {
    default: new LangGraphAgent({
      deploymentUrl,
      graphId,
      ...(langsmithApiKey ? { langsmithApiKey } : {}),
    }),
  },
  a2ui: {
    agents: ['default'],
  },
});

const copilotListener = createCopilotNodeListener({
  runtime,
  basePath,
  cors: true,
});

async function writeHealth(response) {
  let agentStatus = 'unavailable';
  let agentDetail = null;

  try {
    const upstream = await fetch(`${deploymentUrl}/health`, {
      signal: AbortSignal.timeout(2_000),
    });
    agentStatus = upstream.ok ? 'ok' : 'unhealthy';
    agentDetail = await upstream.json().catch(() => null);
  } catch (error) {
    agentDetail = error instanceof Error ? error.message : String(error);
  }

  const healthy = agentStatus === 'ok';
  response.writeHead(healthy ? 200 : 503, {
    'content-type': 'application/json; charset=utf-8',
    'cache-control': 'no-store',
  });
  response.end(JSON.stringify({
    status: healthy ? 'ok' : 'degraded',
    service: 'hvac-copilot-runtime',
    agent: {
      status: agentStatus,
      url: deploymentUrl,
      graphId,
      detail: agentDetail,
    },
  }));
}

const server = createServer((request, response) => {
  const requestUrl = new URL(
    request.url || '/',
    `http://${request.headers.host || `${host}:${port}`}`,
  );

  if (request.method === 'GET' && requestUrl.pathname === '/health') {
    void writeHealth(response);
    return;
  }

  copilotListener(request, response);
});

server.on('error', (error) => {
  console.error('[ai-runtime] failed to start:', error);
  process.exitCode = 1;
});

server.listen(port, host, () => {
  console.log(`[ai-runtime] listening on http://${host}:${port}${basePath}`);
  console.log(`[ai-runtime] default -> ${deploymentUrl} (graph: ${graphId})`);
});

function shutdown(signal) {
  console.log(`[ai-runtime] received ${signal}, shutting down`);
  server.close((error) => {
    if (error) {
      console.error('[ai-runtime] shutdown failed:', error);
      process.exit(1);
    }
    process.exit(0);
  });
}

process.once('SIGINT', () => shutdown('SIGINT'));
process.once('SIGTERM', () => shutdown('SIGTERM'));
