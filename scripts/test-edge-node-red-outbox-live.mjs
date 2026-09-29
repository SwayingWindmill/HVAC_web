const baseUrl = process.env.HVAC_NODE_RED_BASE_URL;
const username = process.env.HVAC_NODE_RED_USERNAME;
const password = process.env.HVAC_NODE_RED_PASSWORD;

if (!baseUrl || !username || !password) {
  throw new Error(
    "HVAC_NODE_RED_BASE_URL, HVAC_NODE_RED_USERNAME and HVAC_NODE_RED_PASSWORD are required.",
  );
}

const brokerId = "754e280e1d82ca7a";
const aggregateTabId = "tab32ab981c95ba";
const outagePort = "1884";
const healthyPort = "1883";
const pollIntervalMs = 5_000;
const outageDeadlineMs = 55_000;
const recoveryDeadlineMs = 30_000;

const sleep = (durationMs) =>
  new Promise((resolve) => setTimeout(resolve, durationMs));

const authForm = new URLSearchParams({
  client_id: "node-red-admin",
  grant_type: "password",
  scope: "*",
  username,
  password,
});
const authResponse = await fetch(`${baseUrl}/auth/token`, {
  method: "POST",
  body: authForm,
});
if (!authResponse.ok) {
  throw new Error(
    `Node-RED authentication failed: HTTP ${authResponse.status}`,
  );
}
const { access_token: accessToken } = await authResponse.json();
const headers = {
  Authorization: `Bearer ${accessToken}`,
  "Node-RED-API-Version": "v2",
};

async function readFlows() {
  const response = await fetch(`${baseUrl}/flows`, { headers });
  if (!response.ok) {
    throw new Error(`Reading Node-RED flows failed: HTTP ${response.status}`);
  }
  return response.json();
}

async function deployBrokerPort(port) {
  const current = await readFlows();
  let found = false;
  const flows = current.flows.map((node) => {
    if (node.id !== brokerId) {
      return node;
    }
    found = true;
    return { ...node, port };
  });
  if (!found) {
    throw new Error(`MQTT broker configuration is missing: ${brokerId}`);
  }
  const response = await fetch(`${baseUrl}/flows`, {
    method: "POST",
    headers: { ...headers, "Content-Type": "application/json" },
    body: JSON.stringify({ rev: current.rev, flows }),
  });
  if (!response.ok) {
    throw new Error(
      `Deploying MQTT port ${port} failed: HTTP ${response.status}`,
    );
  }
  const deployed = await response.json();
  console.log(
    JSON.stringify({ event: "deployed", port, revision: deployed.rev }),
  );
}

async function readOutboxContext() {
  const response = await fetch(`${baseUrl}/context/flow/${aggregateTabId}`, {
    headers,
  });
  if (!response.ok) {
    throw new Error(
      `Reading Node-RED flow context failed: HTTP ${response.status}`,
    );
  }
  const { memory: stored = {} } = await response.json();
  const context = Object.fromEntries(
    Object.entries(stored).map(([key, value]) => {
      if (value.format === "Object") {
        return [key, JSON.parse(value.msg)];
      }
      if (value.format === "number") {
        return [key, Number(value.msg)];
      }
      return [key, value.msg];
    }),
  );
  return {
    pending: Number(context.edgeOutboxPending ?? 0),
    mqttStatus: context.edgeMqttStatus ?? null,
    lastAckAt: Number(context.edgeOutboxLastAckAt ?? 0),
    lastReplayAt: Number(context.edgeOutboxLastReplayAt ?? 0),
    lastError: context.edgeOutboxLastError ?? null,
  };
}

async function waitFor(description, deadlineMs, predicate) {
  const deadline = Date.now() + deadlineMs;
  let latest;
  while (Date.now() < deadline) {
    await sleep(pollIntervalMs);
    latest = await readOutboxContext();
    console.log(
      JSON.stringify({ event: "context", phase: description, ...latest }),
    );
    if (predicate(latest)) {
      return latest;
    }
  }
  throw new Error(
    `${description} was not observed before the deadline: ${JSON.stringify(latest)}`,
  );
}

const baseline = await readOutboxContext();
let restored = false;
try {
  await deployBrokerPort(outagePort);
  const outage = await waitFor(
    "durable backlog",
    outageDeadlineMs,
    (context) => context.pending > 0,
  );

  await deployBrokerPort(healthyPort);
  restored = true;
  const recovered = await waitFor(
    "automatic replay",
    recoveryDeadlineMs,
    (context) =>
      context.pending === 0 && context.lastAckAt > baseline.lastAckAt,
  );

  console.log(JSON.stringify({ event: "passed", baseline, outage, recovered }));
} finally {
  if (!restored) {
    await deployBrokerPort(healthyPort);
    console.log(
      JSON.stringify({ event: "restored-after-failure", port: healthyPort }),
    );
  }
}
