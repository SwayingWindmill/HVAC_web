import assert from "node:assert/strict";
import net from "node:net";
import test from "node:test";

import { probeMqttEndpoint } from "./test-thingsboard-mqtt-endpoint.mjs";

async function listen(handler) {
  const server = net.createServer(handler);
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  return server;
}

test("plain MQTT probe recognizes a CONNACK response", async (t) => {
  const server = await listen((socket) => {
    socket.once("data", (packet) => {
      assert.equal(packet[0], 0x10);
      socket.write(Buffer.from([0x20, 0x02, 0x00, 0x05]));
    });
  });
  t.after(() => server.close());

  const address = server.address();
  const result = await probeMqttEndpoint({
    host: "127.0.0.1",
    port: address.port,
    useTls: false,
    username: "invalid-probe",
    password: "invalid-probe",
  });

  assert.deepEqual(result, {
    host: "127.0.0.1",
    port: address.port,
    tls: false,
    sessionPresent: false,
    returnCode: 5,
    returnCodeName: "not-authorized",
    receivedConnack: true,
  });
});

test("HTTP response cannot be mistaken for an MQTT broker", async (t) => {
  const server = await listen((socket) => {
    socket.once("data", () => socket.write("HTTP/1.1 400 Bad Request\r\n\r\n"));
  });
  t.after(() => server.close());

  const address = server.address();
  await assert.rejects(
    probeMqttEndpoint({
      host: "127.0.0.1",
      port: address.port,
      useTls: false,
      username: "invalid-probe",
      password: "invalid-probe",
    }),
    /Unexpected MQTT response/,
  );
});
