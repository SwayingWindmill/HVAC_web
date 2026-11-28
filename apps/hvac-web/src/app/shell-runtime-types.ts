import type {
  CurrentPrincipalResponse,
  PlatformGatewayClient,
  PlatformStatusResponse,
  ProblemDetails,
  Site,
} from '@/api/generated/platformGateway.gen';
import type { ProtectedScopeResource, ProtectedScopeSnapshot } from './protected-scope';
import type { RealtimeStatusSnapshot, RealtimeStatusUpdate } from './realtime-status';

export type ShellState = 'BOOTSTRAPPING' | 'LOGIN_REQUIRED' | 'UNAVAILABLE' | 'READY';
export type LogoutOutcome = 'completed' | 'failed';

export interface ShellFailureView {
  code: string;
  detail: string;
  traceId?: string;
  retryable: boolean;
}

export interface ShellLogoutView {
  status: 'idle' | 'submitting' | 'failed';
  code?: string;
  detail?: string;
  traceId?: string;
  retryable?: boolean;
}

export interface ShellPlatformView {
  state: 'checking' | 'available' | 'degraded' | 'unavailable';
  status?: PlatformStatusResponse;
  failure?: ShellFailureView;
}

export interface ShellSitesView {
  state: 'checking' | 'available' | 'forbidden' | 'unavailable';
  items?: readonly Readonly<Site>[];
  failure?: ShellFailureView;
}

export type SiteNavigationOutcome =
  | 'navigated'
  | 'failed'
  | 'busy'
  | 'interrupted';

export interface ShellSiteTransitionView {
  status: 'purging' | 'failed';
  target: string;
  failure?: ShellFailureView;
}

export interface ShellSnapshot {
  state: ShellState;
  principal?: CurrentPrincipalResponse;
  platform?: ShellPlatformView;
  sites?: ShellSitesView;
  protectedScope?: ProtectedScopeSnapshot;
  lastSiteId?: string;
  siteTransition?: ShellSiteTransitionView;
  realtime?: RealtimeStatusSnapshot;
  loginUrl?: string;
  reason?: string;
  failure?: ShellFailureView;
  logout?: ShellLogoutView;
}

export type TenantSwitchOutcome =
  | { state: 'switched'; target: string }
  | { state: 'unchanged' }
  | { state: 'rejected'; code: string; message: string }
  | { state: 'ignored'; reason: string };

export interface SwitchTenantOptions {
  readonly currentHref?: string;
  readonly onSwitched?: () => void;
}

export interface ShellRuntime {
  current(): ShellSnapshot;
  subscribe(listener: (snapshot: ShellSnapshot) => void): () => void;
  bootstrap(currentUrl: string): Promise<void>;
  retry(): Promise<void>;
  beginLogin(): void;
  activateSiteScope(siteId: string): void;
  registerProtectedResource(resource: ProtectedScopeResource): () => void;
  protectedRequestToken(): import('./protected-scope').ProtectedScopeRequestToken;
  /** Records one open subscription's state; the header shows the most severe one. */
  publishRealtimeStatus(update: RealtimeStatusUpdate): void;
  /** Forgets a subscription that closed; with none left the header shows idle. */
  releaseRealtimeStatus(source: string): void;
  requestSiteNavigation(target: string): Promise<SiteNavigationOutcome>;
  handlePolicyRevision(policyRevision: string): Promise<'unchanged' | 'reloaded' | 'failed'>;
  switchTenant(tenantId: string, options?: SwitchTenantOptions): Promise<TenantSwitchOutcome>;
  logout(): Promise<LogoutOutcome>;
  clearLogoutError(): void;
  purge(reason: string, beginLogin?: boolean): void;
  dispose(): void;
}

export interface ShellRuntimeEnvironment {
  origin: string;
  now(): number;
  navigate(target: string): void | Promise<unknown>;
  postNavigate(target: string): void;
  setTimer(handler: () => void, delayMs: number): unknown;
  clearTimer(handle: unknown): void;
  listenForUserActivity?(handler: () => void): () => void;
}

export type ShellRuntimeClient = Pick<
  PlatformGatewayClient,
  'getCurrentPrincipal' | 'getPlatformStatus' | 'listSites' | 'loginUrl' | 'logout'
> & {
  readonly switchActiveTenant?: PlatformGatewayClient['switchActiveTenant'];
};

export type ShellProblem = Pick<ProblemDetails, 'status' | 'code'> &
  Partial<Pick<ProblemDetails, 'detail' | 'traceId' | 'retryable'>>;
