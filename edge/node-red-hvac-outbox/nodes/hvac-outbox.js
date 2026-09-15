const path = require("node:path");
const { DurableOutboxStore } = require("../lib/store");

module.exports = function registerHvacOutbox(RED) {
  function HvacOutboxNode(config) {
    RED.nodes.createNode(this, config);
    const node = this;
    const directory = path.join(RED.settings.userDir, "hvac-edge-outbox");
    const store = new DurableOutboxStore(directory);
    let inFlightId = null;

    function updateStatus(pending) {
      node.status({
        fill: pending === 0 ? "green" : "yellow",
        shape: "dot",
        text: `待发送 ${pending}`,
      });
    }

    async function reportStatus(extra = {}) {
      const pending = await store.listPending();
      updateStatus(pending.length);
      return {
        pending,
        message: { payload: { pending: pending.length, ...extra } },
      };
    }

    async function publishNext(send) {
      if (inFlightId !== null) {
        return;
      }
      const [record] = await store.listPending();
      if (!record) {
        return;
      }
      inFlightId = record.id;
      send([
        {
          outboxId: record.id,
          topic: record.topic,
          payload: record.payload,
          qos: 1,
          retain: false,
        },
        null,
      ]);
    }

    node.on("input", async (msg, send, done) => {
      try {
        if (msg.outboxCommand === "enqueue") {
          const record = msg.outboxRecord;
          const inserted = await store.enqueue(record);
          const status = await reportStatus();
          send([null, status.message]);
          if (inserted) {
            await publishNext(send);
          }
        } else if (msg.outboxCommand === "ack") {
          await store.acknowledge(msg.outboxId);
          if (msg.outboxId === inFlightId) {
            inFlightId = null;
          }
          const status = await reportStatus({ lastAckAt: Date.now() });
          send([null, status.message]);
          await publishNext(send);
        } else if (msg.outboxCommand === "reset") {
          inFlightId = null;
        } else if (msg.outboxCommand === "replay") {
          const status = await reportStatus({ lastReplayAt: Date.now() });
          send([null, status.message]);
          await publishNext(send);
        } else {
          throw new Error(`Unknown HVAC outbox command: ${msg.outboxCommand}`);
        }
        done();
      } catch (error) {
        done(error);
      }
    });

    node.on("close", (done) => {
      store.idle().then(() => done(), done);
    });

    store
      .listPending()
      .then((pending) => updateStatus(pending.length))
      .catch((error) => node.error(error));
  }

  RED.nodes.registerType("hvac-outbox", HvacOutboxNode);
};
