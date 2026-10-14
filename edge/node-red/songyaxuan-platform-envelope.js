// Node-RED function node「平台·形成信封」on the Songyaxuan Gateway (tab「Edge·采集与上报」).
// Input: the outbox event whose payload is { <device>: [{ ts, values }] } as the device functions build it.
// Output: one platform telemetry envelope (Connectivity MQTT contract, schemaVersion 2.0) per message,
// or nothing for devices the platform has no source key for (the Gateway's own status).
// Registered devices and points: deploy/sites/songyaxuan.site.json.
const GATEWAY_ID = "01a14000-4000-7000-8000-000000000100";
const DEVICES = {
  temperature: { codes: null, unit: "Cel" },
  waterflow: { codes: { flowMeter: ["flow_meter", "m3/h"], flowVelocity: ["flow_velocity", "m/s"] } },
  et1010: { codes: null, unit: null },
};
for (const meter of ["ammeter-unit1", "ammeter-unit2", "ammeter-unit3", "ammeter-unit4"]) {
  DEVICES[meter] = { codes: { powerTotal: ["power_total", "kW"], combinedActiveTotalElectricalEnergy: ["active_energy_total", "kWh"] } };
}

const source = msg.payload;
const device = Object.keys(source)[0];
const mapping = DEVICES[device];
if (!mapping) return null;
const sample = source[device][0];

const points = [];
for (const [key, value] of Object.entries(sample.values)) {
  let code = key;
  let unit = mapping.unit;
  if (mapping.codes) {
    if (!mapping.codes[key]) continue;
    [code, unit] = mapping.codes[key];
  }
  if (typeof value !== "number" || !Number.isFinite(value)) continue;
  // An unconnected temperature input reads exactly 0; it is not a 0 °C measurement.
  const quality = device === "temperature" && value === 0 ? 1 : 0;
  const point = { code, value, quality };
  if (unit) point.unit = unit;
  points.push(point);
}
if (points.length === 0) return null;

// UUIDv7: 48-bit Unix milliseconds, version 7, variant 10, random rest.
function uuidv7(ms) {
  const hex = ms.toString(16).padStart(12, "0");
  const random = () => Math.floor(Math.random() * 0x10000).toString(16).padStart(4, "0");
  const variant = (0x8000 | (Math.floor(Math.random() * 0x4000))).toString(16);
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-7${random().slice(1)}-${variant}-${random()}${random()}${random()}`;
}

// The platform drops any message whose sequence does not exceed the last one it saw, so the
// sequence follows the clock and survives a restart: milliseconds × 1000 plus a counter.
const now = Date.now();
const last = context.get("lastSequence") || 0;
const sequence = Math.max(now * 1000, last + 1);
context.set("lastSequence", sequence);

delete msg.outboxCommand;
delete msg.outboxRecord;
msg.topic = `hvac/v1/${GATEWAY_ID}/up/telemetry`;
msg.qos = 1;
msg.retain = false;
msg.payload = {
  schemaVersion: "2.0",
  messageId: uuidv7(now),
  gatewayId: GATEWAY_ID,
  timestamp: now,
  sequence,
  replay: false,
  payload: { devices: [{ deviceId: device, deviceTimestamp: sample.ts, points }] },
};
return msg;
