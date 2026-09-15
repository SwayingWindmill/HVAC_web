const assert = require("node:assert/strict");
const { EventEmitter } = require("node:events");
const { mkdtemp } = require("node:fs/promises");
const os = require("node:os");
const path = require("node:path");
const test = require("node:test");
const registerHvacOutbox = require("../nodes/hvac-outbox");

async function createNode() {
  const userDir = await mkdtemp(path.join(os.tmpdir(), "hvac-outbox-node-"));
  const events = new EventEmitter();
  let Constructor;
  const RED = {
    settings: { userDir },
    nodes: {
      createNode(node) {
        node.on = events.on.bind(events);
        node.status = () => undefined;
        node.error = (error) => {
          throw error;
        };
      },
      registerType(type, implementation) {
        assert.equal(type, "hvac-outbox");
        Constructor = implementation;
      },
    },
  };
  registerHvacOutbox(RED);
  const node = new Constructor({});
  const sent = [];

  return {
    sent,
    input(message) {
      return new Promise((resolve, reject) => {
        events.emit(
          "input",
          message,
          (outputs) => sent.push(outputs),
          (error) => (error ? reject(error) : resolve()),
        );
      });
    },
    close() {
      return new Promise((resolve, reject) => {
        events.emit("close", (error) => (error ? reject(error) : resolve()));
      });
    },
  };
}

test("publishes one durable event at a time and advances only after its ack", async () => {
  const outbox = await createNode();
  const first = { id: "temperature:1", topic: "t", payload: { value: 1 } };
  const second = { id: "temperature:2", topic: "t", payload: { value: 2 } };

  await outbox.input({ outboxCommand: "enqueue", outboxRecord: first });
  await outbox.input({ outboxCommand: "enqueue", outboxRecord: second });

  assert.deepEqual(
    outbox.sent.flatMap(([published]) =>
      published ? [published.outboxId] : [],
    ),
    [first.id],
  );

  await outbox.input({ outboxCommand: "ack", outboxId: first.id });
  assert.deepEqual(
    outbox.sent.flatMap(([published]) =>
      published ? [published.outboxId] : [],
    ),
    [first.id, second.id],
  );

  await outbox.input({ outboxCommand: "ack", outboxId: second.id });
  const latestStatus = outbox.sent.at(-1)[1];
  assert.equal(latestStatus.payload.pending, 0);
  await outbox.close();
});
