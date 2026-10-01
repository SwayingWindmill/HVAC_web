import type { QueryClient } from '@tanstack/react-query';
import type { Capability } from '@/api/generated/platformGateway.gen';
import type { RealtimeStatusUpdate } from './realtime-status';
import type { RuntimeConfig } from './runtime-config';
import type { ShellRuntime } from './shell-runtime';

export type RouteScope = 'platform' | 'site';

export interface RouteNavigationMeta {
  readonly id: string;
  readonly label: string;
  readonly group?: 'operate' | 'management' | 'energy' | 'automation' | 'analysis' | 'system';
  readonly order: number;
  readonly primary?: boolean;
}

export interface NavigationItem {
  readonly id: string;
  readonly label: string;
  readonly path: string;
  readonly kind: 'link';
  readonly group?: RouteNavigationMeta['group'];
  readonly degraded: boolean;
  readonly primary?: boolean;
}

export interface HvacRouterContext {
  readonly config: RuntimeConfig;
  readonly queryClient: QueryClient;
  readonly runtime: ShellRuntime;
  readonly publishRealtimeStatus: (update: RealtimeStatusUpdate) => void;
}

declare module '@tanstack/react-router' {
  interface StaticDataRouteOption {
    title?: string;
    scope?: RouteScope;
    requiredCapabilities?: readonly Capability[];
    requiresPlatform?: boolean;
    navigation?: RouteNavigationMeta;
  }
}
