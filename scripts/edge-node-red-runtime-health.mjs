export function buildInitializeEdgeRuntimeFunction(gatewayBootTime) {
  return `global.set("gatewayBootTime", ${gatewayBootTime});
global.set("edgeRuntimeStartedAt", Date.now());
global.set("edgeModbusReadSuccess", 0);
global.set("edgeModbusReadFailure", 0);
return null;`;
}

export function instrumentModbusSuccess(source, functionBody) {
  return `global.set("edgeModbusReadSuccess", global.get("edgeModbusReadSuccess") + 1);
global.set("edgeModbusLastSuccessAt", Date.now());
global.set("edgeModbusLastSuccessSource", "${source}");
${functionBody}`;
}

export const recordModbusFailureFunction = `global.set("edgeModbusReadFailure", global.get("edgeModbusReadFailure") + 1);
global.set("edgeModbusLastFailureAt", Date.now());
global.set("edgeModbusLastFailureSource", msg.error.source.name);
return null;`;

export const edgeRuntimeTelemetryFunction = `const status = flow.get("edgeMqttStatus");
const values = {
  gatewayBootTime: global.get("gatewayBootTime"),
  edgeRuntimeStartedAt: global.get("edgeRuntimeStartedAt"),
  mqttConnected: status && status.fill === "green" && status.shape === "dot",
  mqttStatus: status ? status.text : "unknown",
  outboxPending: flow.get("edgeOutboxPending"),
  modbusReadSuccess: global.get("edgeModbusReadSuccess"),
  modbusReadFailure: global.get("edgeModbusReadFailure"),
};
const optionalValues = {
  lastAckAt: flow.get("edgeOutboxLastAckAt"),
  lastModbusSuccessAt: global.get("edgeModbusLastSuccessAt"),
  lastModbusSuccessSource: global.get("edgeModbusLastSuccessSource"),
  lastModbusFailureAt: global.get("edgeModbusLastFailureAt"),
  lastModbusFailureSource: global.get("edgeModbusLastFailureSource"),
};
for (const [key, value] of Object.entries(optionalValues)) {
  if (value !== undefined) {
    values[key] = value;
  }
}
msg.topic = "v1/gateway/telemetry";
msg.payload = { EdgeGateway: [{ ts: Date.now(), values }] };
return msg;`;
