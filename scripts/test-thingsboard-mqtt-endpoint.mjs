import net from "node:net";
import tls from "node:tls";
import { pathToFileURL } from "node:url";

const MQTT_CONNECT = 0x10;
const MQTT_CONNACK = 0x20;
const MQTT_V3_1_1 = 4;
const CONNECT_FLAGS = 0xc2;
const CONNACK_REMAINING_LENGTH = 2;
const CONNACK_RETURN_CODE_NAMES = [
  "accepted",
  "unacceptable-protocol-version",
  "identifier-rejected",
  "server-unavailable",
  "bad-username-or-password",
  "not-authorized",
];

function mqttString(value) {
  const content = Buffer.from(value, "utf8");
  const length = Buffer.allocUnsafe(2);
  length.writeUInt16BE(content.length);
  return Buffer.concat([length, content]);
}

function remainingLength(value) {
  const bytes = [];
  do {
    let encoded = value % 128;
    value = Math.floor(value / 128);
    if (value > 0) {
      encoded |= 128;
    }
    bytes.push(encoded);
  } while (value > 0);
  return Buffer.from(bytes);
}

function buildConnectPacket({ clientId, username, password }) {
  const variableHeader = Buffer.concat([
    mqttString("MQTT"),
    Buffer.from([MQTT_V3_1_1, CONNECT_FLAGS, 0, 30]),
  ]);
  const payload = Buffer.concat([
    mqttString(clientId),
    mqttString(username),
    mqttString(password),
  ]);
  const body = Buffer.concat([variableHeader, payload]);
  return Buffer.concat([
    Buffer.from([MQTT_CONNECT]),
    remainingLength(body.length),
    body,
  ]);
}

export async function probeMqttEndpoint({
  host,
  port,
  useTls,
  username,
  password,
  timeoutMs = 10_000,
}) {
  const packet = buildConnectPacket({
    clientId: `hvac-domain-probe-${Date.now()}`,
    username,
    password,
  });

  return new Promise((resolve, reject) => {
    const socket = useTls
      ? tls.connect({
          host,
          port,
          servername: host,
          rejectUnauthorized: true,
        })
      : net.connect({ host, port });
    let response = Buffer.alloc(0);
    const timeout = setTimeout(() => {
      socket.destroy();
      reject(new Error("MQTT CONNACK timeout"));
    }, timeoutMs);

    socket.once(useTls ? "secureConnect" : "connect", () =>
      socket.write(packet),
    );
    socket.on("data", (chunk) => {
      response = Buffer.concat([response, chunk]);
      if (response.length < 4) {
        return;
      }
      clearTimeout(timeout);
      socket.end();
      if (
        response[0] !== MQTT_CONNACK ||
        response[1] !== CONNACK_REMAINING_LENGTH
      ) {
        reject(
          new Error(`Unexpected MQTT response: ${response.toString("hex")}`),
        );
        return;
      }
      resolve({
        host,
        port,
        tls: useTls,
        sessionPresent: response[2] === 1,
        returnCode: response[3],
        returnCodeName:
          CONNACK_RETURN_CODE_NAMES[response[3]] ?? "unknown-return-code",
        receivedConnack: true,
      });
    });
    socket.once("error", (error) => {
      clearTimeout(timeout);
      reject(error);
    });
  });
}

async function main() {
  const host = process.env.HVAC_MQTT_PROBE_HOST;
  if (!host) {
    throw new Error("HVAC_MQTT_PROBE_HOST is required.");
  }

  const result = await probeMqttEndpoint({
    host,
    port: Number(process.env.HVAC_MQTT_PROBE_PORT ?? "8883"),
    useTls: process.env.HVAC_MQTT_PROBE_TLS !== "false",
    username: process.env.HVAC_MQTT_PROBE_USERNAME ?? "invalid-probe",
    password: process.env.HVAC_MQTT_PROBE_PASSWORD ?? "invalid-probe",
  });
  console.log(JSON.stringify(result));
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(process.argv[1]).href
) {
  await main();
}
