import type { StatusTone } from '@/components/common/StatusBadge';
import type { TwinState } from './twin-model';

/** How each twin state shows in the shared status badge. */
export const TWIN_STATUS_TONE: Readonly<Record<TwinState, StatusTone>> = {
  running: 'running',
  stopped: 'neutral',
  fault: 'alarm',
  offline: 'offline',
  unknown: 'neutral',
  unconnected: 'neutral',
};
