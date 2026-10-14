// Node-RED function node「平台·形成信封」on the Songyaxuan Gateway (tab「Edge·采集与上报」).
// Input: a device function's message { <device>: [{ ts, values }] }.
// Output: one platform telemetry envelope (Connectivity MQTT contract, schemaVersion 2.0) per device
// message, or nothing for devices the platform has no source key for (the Gateway's own status). A meter
// message also yields the plant total, below. Each goes on to「Outbox·形成事件」, which stores it in the
// durable outbox until the broker acknowledges it:
//   msg.outboxCommand = "enqueue";
//   msg.outboxRecord = { id: msg.payload.messageId, topic: msg.topic, payload: msg.payload };
// Registered devices and points: deploy/sites/songyaxuan.site.json.
const GATEWAY_ID = "01a14000-4000-7000-8000-000000000100";
const METERS = ["ammeter-unit1", "ammeter-unit2", "ammeter-unit3", "ammeter-unit4"];
const DEVICES = {
  temperature: { codes: null, unit: "Cel" },
  waterflow: { codes: { flowMeter: ["flow_meter", "m3/h"], flowVelocity: ["flow_velocity", "m/s"] } },
  et1010: { codes: null, unit: null },
};
for (const meter of METERS) {
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
function envelope(deviceId, deviceTimestamp, devicePoints) {
  const now = Date.now();
  const sequence = Math.max(now * 1000, (context.get("lastSequence") || 0) + 1);
  context.set("lastSequence", sequence);
  return {
    topic: `hvac/v1/${GATEWAY_ID}/up/telemetry`,
    qos: 1,
    retain: false,
    payload: {
      schemaVersion: "2.0",
      messageId: uuidv7(now),
      gatewayId: GATEWAY_ID,
      timestamp: now,
      sequence,
      replay: false,
      payload: { devices: [{ deviceId, deviceTimestamp, points: devicePoints }] },
    },
  };
}

const out = [envelope(device, sample.ts, points)];

// The plant room has no total meter: the four machines' meters added up stand for it
// (「空调总电表（四台机组合计）」), once each has reported since the flow started.
if (METERS.includes(device)) {
  const latest = context.get("meterLatest") || {};
  latest[device] = { power: sample.values.powerTotal, energy: sample.values.combinedActiveTotalElectricalEnergy };
  context.set("meterLatest", latest);
  const readings = METERS.map((meter) => latest[meter]);
  if (readings.every((reading) => reading && Number.isFinite(reading.power) && Number.isFinite(reading.energy))) {
    const sum = (field) => Math.round(readings.reduce((total, reading) => total + reading[field], 0) * 1000) / 1000;
    out.push(envelope("ammeter-total", sample.ts, [
      { code: "power_total", value: sum("power"), quality: 0, unit: "kW" },
      { code: "active_energy_total", value: sum("energy"), quality: 0, unit: "kWh" },
    ]));
  }
}
return [out];
