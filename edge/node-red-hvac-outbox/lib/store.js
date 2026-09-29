const { mkdir, open, readFile, rename, stat } = require("node:fs/promises");
const path = require("node:path");

const DEFAULT_MAX_BYTES = 128 * 1024 * 1024;
const DEFAULT_COMPACT_AFTER_ACKS = 256;

class DurableOutboxStore {
  constructor(directory, options = {}) {
    this.directory = directory;
    this.file = path.join(directory, "outbox.jsonl");
    this.temporaryFile = path.join(directory, "outbox.compacting.jsonl");
    this.maxBytes = options.maxBytes ?? DEFAULT_MAX_BYTES;
    this.compactAfterAcks =
      options.compactAfterAcks ?? DEFAULT_COMPACT_AFTER_ACKS;
    this.pending = new Map();
    this.bytes = 0;
    this.acksSinceCompact = 0;
    this.tail = this.#load();
  }

  async enqueue(record) {
    return this.#run(async () => {
      if (this.pending.has(record.id)) {
        return false;
      }
      const line = `${JSON.stringify({ type: "event", record })}\n`;
      await this.#append(line);
      this.pending.set(record.id, record);
      return true;
    });
  }

  async acknowledge(id) {
    return this.#run(async () => {
      if (!this.pending.has(id)) {
        return false;
      }
      await this.#append(`${JSON.stringify({ type: "ack", id })}\n`);
      this.pending.delete(id);
      this.acksSinceCompact += 1;
      if (this.acksSinceCompact >= this.compactAfterAcks) {
        await this.#compact();
      }
      return true;
    });
  }

  async listPending() {
    return this.#run(async () => Array.from(this.pending.values()));
  }

  async idle() {
    return this.#run(async () => undefined);
  }

  async #load() {
    await mkdir(this.directory, { recursive: true });
    let content = "";
    try {
      content = await readFile(this.file, "utf8");
      this.bytes = (await stat(this.file)).size;
    } catch (error) {
      if (error.code !== "ENOENT") {
        throw error;
      }
      await this.#replaceFile("");
    }

    const lines = content.split(/\r?\n/);
    let incompleteTail = false;
    for (let index = 0; index < lines.length; index += 1) {
      const line = lines[index];
      if (!line) {
        continue;
      }
      let entry;
      try {
        entry = JSON.parse(line);
      } catch (error) {
        if (index !== lines.length - 1) {
          throw error;
        }
        incompleteTail = true;
        continue;
      }
      if (entry.type === "event") {
        this.pending.set(entry.record.id, entry.record);
      } else if (entry.type === "ack") {
        this.pending.delete(entry.id);
      }
    }
    if (incompleteTail) {
      await this.#compact();
    }
  }

  async #run(operation) {
    const result = this.tail.then(operation);
    this.tail = result.catch(() => undefined);
    return result;
  }

  async #append(line) {
    const bytes = Buffer.byteLength(line);
    if (this.bytes + bytes > this.maxBytes) {
      throw new Error(`HVAC outbox capacity exceeded: ${this.maxBytes} bytes`);
    }
    const handle = await open(this.file, "a");
    try {
      await handle.writeFile(line, "utf8");
      await handle.sync();
    } finally {
      await handle.close();
    }
    this.bytes += bytes;
  }

  async #compact() {
    const content = Array.from(
      this.pending.values(),
      (record) => `${JSON.stringify({ type: "event", record })}\n`,
    ).join("");
    await this.#replaceFile(content);
    this.bytes = Buffer.byteLength(content);
    this.acksSinceCompact = 0;
  }

  async #replaceFile(content) {
    const handle = await open(this.temporaryFile, "w");
    try {
      await handle.writeFile(content, "utf8");
      await handle.sync();
    } finally {
      await handle.close();
    }
    await rename(this.temporaryFile, this.file);
    if (process.platform !== "win32") {
      const directoryHandle = await open(this.directory, "r");
      try {
        await directoryHandle.sync();
      } finally {
        await directoryHandle.close();
      }
    }
  }
}

module.exports = { DurableOutboxStore };
