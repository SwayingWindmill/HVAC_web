import { mkdir, writeFile } from "node:fs/promises";
import { dirname } from "node:path";

const baseUrl = process.env.HVAC_NODE_RED_BASE_URL;
const username = process.env.HVAC_NODE_RED_USERNAME ?? "admin";
const password = process.env.HVAC_NODE_RED_PASSWORD;
const backupPath = process.env.HVAC_NODE_RED_BACKUP_PATH;
const dryRun = process.env.HVAC_NODE_RED_DRY_RUN === "true";

if (!baseUrl || !password || !backupPath) {
  throw new Error(
    "HVAC_NODE_RED_BASE_URL, HVAC_NODE_RED_PASSWORD and HVAC_NODE_RED_BACKUP_PATH are required.",
  );
}

const ids = {
  emptyTab: "77eebd16ce160f1b",
  temperatureTab: "6af049f059e4b846",
  ammeterTab: "77db25f790f2227c",
  et1010Tab: "3aeae154cf5a9750",
  waterflowTab: "5209349dda18ab9b",
  aggregateTab: "tab32ab981c95ba",
  modbus: "05c0a8d76be7c0b5",
  legacyMqttBroker: "d24a9e241a068ee0",
  thingsBoardBroker: "754e280e1d82ca7a",
  unusedTls: "c6c91e3b96a4221a",
};

const form = new URLSearchParams({
  client_id: "node-red-admin",
  grant_type: "password",
  scope: "*",
  username,
  password,
});

const authResponse = await fetch(`${baseUrl}/auth/token`, {
  method: "POST",
  body: form,
});
if (!authResponse.ok) {
  throw new Error(`Node-RED authentication failed: HTTP ${authResponse.status}`);
}
const { access_token: accessToken } = await authResponse.json();
const headers = {
  Authorization: `Bearer ${accessToken}`,
  "Node-RED-API-Version": "v2",
};

const flowsResponse = await fetch(`${baseUrl}/flows`, { headers });
if (!flowsResponse.ok) {
  throw new Error(`Reading Node-RED flows failed: HTTP ${flowsResponse.status}`);
}
const current = await flowsResponse.json();

await mkdir(dirname(backupPath), { recursive: true });
await writeFile(backupPath, `${JSON.stringify(current, null, 2)}\n`, "utf8");

const requiredIds = [
  ids.temperatureTab,
  ids.ammeterTab,
  ids.et1010Tab,
  ids.waterflowTab,
  ids.aggregateTab,
  ids.modbus,
  ids.thingsBoardBroker,
];
for (const id of requiredIds) {
  if (!current.flows.some((node) => node.id === id)) {
    throw new Error(`Expected Node-RED object is missing: ${id}`);
  }
}

const removedTabs = new Set([
  ids.emptyTab,
  ids.temperatureTab,
  ids.ammeterTab,
  ids.waterflowTab,
]);
const removedConfigs = new Set([ids.legacyMqttBroker, ids.unusedTls]);

const retained = current.flows
  .filter((node) => !removedTabs.has(node.id))
  .filter((node) => !removedTabs.has(node.z))
  .filter((node) => !removedConfigs.has(node.id))
  .filter((node) => node.z !== ids.aggregateTab && node.z !== ids.et1010Tab)
  .map((node) => {
    if (node.id === ids.aggregateTab) {
      return { ...node, label: "Edge·采集与上报" };
    }
    if (node.id === ids.et1010Tab) {
      return { ...node, label: "ET1010·状态" };
    }
    return node;
  });

function inject(id, z, name, repeat, onceDelay, x, y, target) {
  return {
    id,
    type: "inject",
    z,
    name,
    props: [{ p: "payload" }, { p: "topic", vt: "str" }],
    repeat: String(repeat),
    crontab: "",
    once: true,
    onceDelay,
    topic: "",
    payload: "",
    payloadType: "date",
    x,
    y,
    wires: [[target]],
  };
}

function modbusRead(id, z, name, uid, dataType, len, columns, x, y, target) {
  return {
    id,
    type: "modbusRead",
    z,
    name,
    uid: String(uid),
    dataType: String(dataType),
    len: String(len),
    modbus: ids.modbus,
    columns,
    x,
    y,
    wires: [[target]],
  };
}

function column(Key, Address, Type) {
  return { Key, Address: String(Address), Type: String(Type), Tdata: "" };
}

function functionNode(id, z, name, func, x, y, target) {
  return {
    id,
    type: "function",
    z,
    name,
    func,
    outputs: 1,
    timeout: 0,
    noerr: 0,
    initialize: "",
    finalize: "",
    libs: [],
    x,
    y,
    wires: [[target]],
  };
}

const mqttOutId = "ed30000000000001";
const et1010MqttOutId = "ed30000000000002";
const temperatureColumns = Array.from({ length: 20 }, (_, index) =>
  column(`t${index + 1}`, 30401 + index, 1),
);
const et1010Keys = [
  "u11", "u12", "u13", "u14",
  "u21", "u22", "u23", "u24",
  "u31", "u32", "u33", "u34",
  "u41", "u42", "u43", "u44",
];
const et1010Columns = et1010Keys.map((key, index) => column(key, index, 23));

const temperatureFunction = `const device = "temperature";
const values = {};
for (let index = 1; index <= 20; index += 1) {
  values[\`t\${index}\`] = msg.payload[\`t\${index}\`] / 10;
}
msg.topic = "v1/gateway/telemetry";
msg.payload = { [device]: [{ ts: Date.now(), values }] };
return msg;`;

const waterflowFunction = `const device = "waterflow";
const values = {
  flowMeter: msg.payload.flowMeter / 35.315,
  flowVelocity: msg.payload.flowVelocity * 0.3048 / 3600,
};
msg.topic = "v1/gateway/telemetry";
msg.payload = { [device]: [{ ts: Date.now(), values }] };
return msg;`;

function ammeterFunction(device) {
  return `const device = "${device}";
const values = {
  powerTotal: msg.payload.powerTotal / 10,
  combinedActiveTotalElectricalEnergy: msg.payload.combinedActiveTotalElectricalEnergy,
};
msg.topic = "v1/gateway/telemetry";
msg.payload = { [device]: [{ ts: Date.now(), values }] };
return msg;`;
}

const et1010Function = `const device = "et1010";
msg.topic = "v1/gateway/telemetry";
msg.payload = { [device]: [{ ts: Date.now(), values: msg.payload }] };
return msg;`;

const edgeNodes = [
  inject("ed00000000000001", ids.aggregateTab, "每60s·温度", 60, 1, 130, 80, "ed10000000000001"),
  modbusRead(
    "ed10000000000001",
    ids.aggregateTab,
    "温度 UID20",
    20,
    4,
    20,
    temperatureColumns,
    360,
    80,
    "ed20000000000001",
  ),
  functionNode("ed20000000000001", ids.aggregateTab, "温度·工程值", temperatureFunction, 610, 80, mqttOutId),

  inject("ed00000000000002", ids.aggregateTab, "每60s·水流", 60, 8, 130, 180, "ed10000000000002"),
  modbusRead(
    "ed10000000000002",
    ids.aggregateTab,
    "水流 UID10",
    10,
    3,
    6,
    [column("flowMeter", "0001", 1), column("flowVelocity", "0005", 1)],
    360,
    180,
    "ed20000000000002",
  ),
  functionNode("ed20000000000002", ids.aggregateTab, "水流·工程值", waterflowFunction, 610, 180, mqttOutId),

  inject("ed00000000000003", ids.aggregateTab, "每300s·1号电表", 300, 15, 130, 280, "ed10000000000003"),
  modbusRead(
    "ed10000000000003",
    ids.aggregateTab,
    "1号电表 UID5",
    5,
    3,
    62,
    [column("powerTotal", 36, 5), column("combinedActiveTotalElectricalEnergy", 60, 5)],
    360,
    280,
    "ed20000000000003",
  ),
  functionNode("ed20000000000003", ids.aggregateTab, "1号电表·工程值", ammeterFunction("ammeter-unit1"), 610, 280, mqttOutId),

  inject("ed00000000000004", ids.aggregateTab, "每300s·2号电表", 300, 22, 130, 380, "ed10000000000004"),
  modbusRead(
    "ed10000000000004",
    ids.aggregateTab,
    "2号电表 UID2",
    2,
    3,
    62,
    [column("powerTotal", 36, 5), column("combinedActiveTotalElectricalEnergy", 60, 5)],
    360,
    380,
    "ed20000000000004",
  ),
  functionNode("ed20000000000004", ids.aggregateTab, "2号电表·工程值", ammeterFunction("ammeter-unit2"), 610, 380, mqttOutId),

  inject("ed00000000000005", ids.aggregateTab, "每300s·3号电表", 300, 29, 130, 480, "ed10000000000005"),
  modbusRead(
    "ed10000000000005",
    ids.aggregateTab,
    "3号电表 UID3",
    3,
    3,
    62,
    [column("powerTotal", 36, 5), column("combinedActiveTotalElectricalEnergy", 60, 5)],
    360,
    480,
    "ed20000000000005",
  ),
  functionNode("ed20000000000005", ids.aggregateTab, "3号电表·工程值", ammeterFunction("ammeter-unit3"), 610, 480, mqttOutId),

  inject("ed00000000000006", ids.aggregateTab, "每300s·4号电表", 300, 36, 130, 580, "ed10000000000006"),
  modbusRead(
    "ed10000000000006",
    ids.aggregateTab,
    "4号电表 UID4",
    4,
    3,
    66,
    [column("powerTotal", 36, 5), column("combinedActiveTotalElectricalEnergy", 64, 5)],
    360,
    580,
    "ed20000000000006",
  ),
  functionNode("ed20000000000006", ids.aggregateTab, "4号电表·工程值", ammeterFunction("ammeter-unit4"), 610, 580, mqttOutId),

  {
    id: mqttOutId,
    type: "mqtt out",
    z: ids.aggregateTab,
    name: "ThingsBoard·QoS1",
    topic: "",
    qos: "1",
    retain: "",
    respTopic: "",
    contentType: "",
    userProps: "",
    correl: "",
    expiry: "",
    broker: ids.thingsBoardBroker,
    x: 900,
    y: 330,
    wires: [],
  },

  inject("ed00000000000007", ids.et1010Tab, "每60s·状态", 60, 45, 130, 100, "ed10000000000007"),
  modbusRead(
    "ed10000000000007",
    ids.et1010Tab,
    "ET1010 UID31",
    31,
    1,
    16,
    et1010Columns,
    360,
    100,
    "ed20000000000007",
  ),
  functionNode("ed20000000000007", ids.et1010Tab, "运行状态->TB", et1010Function, 610, 100, et1010MqttOutId),
  {
    id: et1010MqttOutId,
    type: "mqtt out",
    z: ids.et1010Tab,
    name: "ThingsBoard·QoS1",
    topic: "",
    qos: "1",
    retain: "",
    respTopic: "",
    contentType: "",
    userProps: "",
    correl: "",
    expiry: "",
    broker: ids.thingsBoardBroker,
    x: 860,
    y: 100,
    wires: [],
  },
];

const nextFlows = [...retained, ...edgeNodes];
const serialized = JSON.stringify(nextFlows);
if (serialized.includes("tb.oidcs.com") || serialized.includes("emqx.oidcs.com")) {
  throw new Error("Legacy OIDCS endpoint remains in the transformed flows.");
}

let revision = current.rev;
if (!dryRun) {
  const deployResponse = await fetch(`${baseUrl}/flows`, {
    method: "POST",
    headers: { ...headers, "Content-Type": "application/json" },
    body: JSON.stringify({ rev: current.rev, flows: nextFlows }),
  });
  if (!deployResponse.ok) {
    throw new Error(`Deploying Node-RED flows failed: HTTP ${deployResponse.status}`);
  }
  const deployed = await deployResponse.json();
  revision = deployed.rev;
}

console.log(
  JSON.stringify({
    previousRevision: current.rev,
    revision,
    dryRun,
    backupPath,
    objectsBefore: current.flows.length,
    objectsAfter: nextFlows.length,
    scheduledPolls: edgeNodes.filter((node) => node.type === "inject").length,
    modbusReads: edgeNodes.filter((node) => node.type === "modbusRead").length,
    mqttOutputs: edgeNodes.filter((node) => node.type === "mqtt out").length,
  }),
);
