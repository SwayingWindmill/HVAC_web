const assert = require("node:assert/strict");
const { mkdtemp, appendFile, readFile } = require("node:fs/promises");
const os = require("node:os");
const path = require("node:path");
const test = require("node:test");
const { DurableOutboxStore } = require("../lib/store");

async function temporaryStore(options) {
  const directory = await mkdtemp(path.join(os.tmpdir(), "hvac-outbox-"));
  return { directory, store: new DurableOutboxStore(directory, options) };
}

test("an enqueued telemetry record survives a new store instance", async () => {
  const { directory, store } = await temporaryStore();
  const record = {
    id: "temperature:1",
    topic: "v1/gateway/telemetry",
    payload: { temperature: [{ ts: 1, values: { t1: 13.9 } }] },
  };

  assert.equal(await store.enqueue(record), true);

  const reopened = new DurableOutboxStore(directory);
  assert.deepEqual(await reopened.listPending(), [record]);
});

test("an acknowledged telemetry record is not replayed after restart", async () => {
  const { directory, store } = await temporaryStore();
  const record = {
    id: "waterflow:2",
    topic: "v1/gateway/telemetry",
    payload: { waterflow: [{ ts: 2, values: { flowMeter: 1 } }] },
  };

  await store.enqueue(record);
  assert.equal(await store.acknowledge(record.id), true);

  const reopened = new DurableOutboxStore(directory);
  assert.deepEqual(await reopened.listPending(), []);
});

test("a power-loss-style partial trailing record does not hide earlier pending data", async () => {
  const { directory, store } = await temporaryStore();
  const record = {
    id: "et1010:3",
    topic: "v1/gateway/telemetry",
    payload: { et1010: [{ ts: 3, values: { u11: 1 } }] },
  };
  await store.enqueue(record);
  await appendFile(
    path.join(directory, "outbox.jsonl"),
    '{"type":"ack"',
    "utf8",
  );

  const reopened = new DurableOutboxStore(directory);
  assert.deepEqual(await reopened.listPending(), [record]);

  const later = {
    id: "et1010:4",
    topic: "v1/gateway/telemetry",
    payload: { et1010: [{ ts: 4, values: { u11: 0 } }] },
  };
  await reopened.enqueue(later);
  const reopenedAgain = new DurableOutboxStore(directory);
  assert.deepEqual(await reopenedAgain.listPending(), [record, later]);
});

test("atomic compaction retains only unacknowledged events", async () => {
  const { directory, store } = await temporaryStore({ compactAfterAcks: 1 });
  const first = { id: "meter:4", topic: "t", payload: { value: 4 } };
  const second = { id: "meter:5", topic: "t", payload: { value: 5 } };
  await store.enqueue(first);
  await store.enqueue(second);

  await store.acknowledge(first.id);

  const content = await readFile(path.join(directory, "outbox.jsonl"), "utf8");
  assert.equal(content.includes(first.id), false);
  assert.equal(content.includes(second.id), true);
  const reopened = new DurableOutboxStore(directory);
  assert.deepEqual(await reopened.listPending(), [second]);
});
