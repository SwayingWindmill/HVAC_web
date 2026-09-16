import assert from "node:assert/strict";
import test from "node:test";

import {
  buildInitializeEdgeRuntimeFunction,
  edgeRuntimeTelemetryFunction,
  instrumentModbusSuccess,
  recordModbusFailureFunction,
} from "./edge-node-red-runtime-health.mjs";

function execute(source, state, msg = {}) {
  const flow = {
    get: (key) => state.get(key),
    set: (key, value) => state.set(key, value),
  };
  const global = flow;
  return Function("msg", "flow", "global", source)(msg, flow, global);
}

test("reports edge runtime, MQTT, Outbox and Modbus health without inventing absent timestamps", () => {
  const state = new Map([
    ["edgeRuntimeStartedAt", 100],
    ["edgeMqttStatus", { fill: "green", shape: "dot", text: "connected" }],
    ["edgeOutboxPending", 2],
    ["edgeOutboxLastAckAt", 200],
    ["edgeModbusReadSuccess", 7],
    ["edgeModbusReadFailure", 1],
    ["edgeModbusLastSuccessAt", 300],
    ["edgeModbusLastSuccessSource", "temperature"],
  ]);

  const result = execute(edgeRuntimeTelemetryFunction, state, {});
  const values = result.payload.EdgeGateway[0].values;

  assert.equal(result.topic, "v1/gateway/telemetry");
  assert.equal(values.mqttConnected, true);
  assert.equal(values.outboxPending, 2);
  assert.equal(values.lastAckAt, 200);
  assert.equal(values.modbusReadSuccess, 7);
  assert.equal(values.modbusReadFailure, 1);
  assert.equal("lastModbusFailureAt" in values, false);
});

test("counts successful and failed Modbus reads from initialized runtime state", () => {
  const state = new Map();
  execute(buildInitializeEdgeRuntimeFunction(123), state);
  execute(instrumentModbusSuccess("temperature", "return msg;"), state, {
    payload: {},
  });
  execute(recordModbusFailureFunction, state, {
    error: { source: { name: "waterflow" } },
  });

  assert.equal(state.get("edgeModbusReadSuccess"), 1);
  assert.equal(state.get("edgeModbusReadFailure"), 1);
  assert.equal(state.get("edgeModbusLastSuccessSource"), "temperature");
  assert.equal(state.get("edgeModbusLastFailureSource"), "waterflow");
});

test("initializes the gateway boot time derived from Node-RED diagnostics", () => {
  const state = new Map();

  execute(buildInitializeEdgeRuntimeFunction(123_456), state);

  assert.equal(state.get("gatewayBootTime"), 123_456);
  assert.equal(state.get("edgeModbusReadSuccess"), 0);
  assert.equal(state.get("edgeModbusReadFailure"), 0);
});
