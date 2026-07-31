export interface BoundedRealtimePublisher<T> {
  push(value: T): void;
  cancel(): void;
  pending(): boolean;
}

type ScheduleHandle = number;
type Schedule = (callback: () => void) => ScheduleHandle;
type CancelSchedule = (handle: ScheduleHandle) => void;

export function createBoundedRealtimePublisher<T>(
  schedule: Schedule,
  cancelSchedule: CancelSchedule,
  publish: (value: T) => void,
): BoundedRealtimePublisher<T> {
  let handle: ScheduleHandle | null = null;
  let latest: T | undefined;
  return {
    push(value) {
      latest = value;
      if (handle !== null) return;
      handle = schedule(() => {
        handle = null;
        const valueToPublish = latest;
        latest = undefined;
        if (valueToPublish !== undefined) publish(valueToPublish);
      });
    },
    cancel() {
      if (handle !== null) cancelSchedule(handle);
      handle = null;
      latest = undefined;
    },
    pending: () => handle !== null,
  };
}
