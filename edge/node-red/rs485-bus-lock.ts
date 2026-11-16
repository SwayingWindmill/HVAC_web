export const BUS_PRIORITY_NORMAL = 0;
export const BUS_PRIORITY_PULSE_RESET = 100;

interface BusWaiter {
  priority: number;
  resolve: () => void;
}

/** Priority mutex serializing half-duplex RS485 Modbus RTU transactions across edge executors. */
export class Rs485BusLock {
  private locked = false;
  private readonly waiters: BusWaiter[] = [];

  async runExclusive<T>(priority: number, task: () => Promise<T>): Promise<T> {
    const effectivePriority = Number.isFinite(priority) ? priority : BUS_PRIORITY_NORMAL;
    await this.acquire(effectivePriority);
    try {
      return await task();
    } finally {
      this.release();
    }
  }

  private acquire(priority: number): Promise<void> {
    if (!this.locked) {
      this.locked = true;
      return Promise.resolve();
    }
    return new Promise<void>((resolve) => {
      let insertAt = this.waiters.length;
      for (let i = 0; i < this.waiters.length; i += 1) {
        if (this.waiters[i].priority < priority) {
          insertAt = i;
          break;
        }
      }
      this.waiters.splice(insertAt, 0, { priority, resolve });
    });
  }

  private release(): void {
    const next = this.waiters.shift();
    if (next) {
      next.resolve();
      return;
    }
    this.locked = false;
  }
}
