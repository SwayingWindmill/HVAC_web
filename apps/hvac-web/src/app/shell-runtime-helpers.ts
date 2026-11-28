import type { CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
import { classifyBootstrapProblem, isAlreadyInvalidLogout } from './shell-policy';
import { isUUIDv7 } from './router-paths';
import type {
  ShellFailureView,
  ShellLogoutView,
  ShellPlatformView,
  ShellProblem,
  ShellRuntimeClient,
  ShellRuntimeEnvironment,
  ShellSitesView,
  SiteNavigationOutcome,
  TenantSwitchOutcome,
} from './shell-runtime-types';

const LAST_SITE_STORAGE_PREFIX = 'hvac:last-site:';
const SITE_PAGE_LIMIT = 100;
const MAX_SITE_PAGES = 100;
const USER_ACTIVITY_SYNC_INTERVAL_MS = 60_000;
const IDLE_RETRY_DELAY_MS = 60_000;

export const MAX_TIMER_DELAY_MS = 2_147_000_000;

export class SiteDiscoveryValidationError extends Error {}

export function clearTenantSwitchSearchParams(target: string, origin: string): string {
  const url = new URL(target, origin);
  url.searchParams.delete('site');
  url.searchParams.delete('inspect');
  return `${url.pathname}${url.search}${url.hash}`;
}

export function readStoredSiteId(tenantId: string): string | undefined {
  if (!tenantId || typeof sessionStorage === 'undefined') return undefined;
  try {
    const raw = sessionStorage.getItem(`${LAST_SITE_STORAGE_PREFIX}${tenantId}`);
    const trimmed = raw?.trim();
    return trimmed ? trimmed : undefined;
  } catch {
    return undefined;
  }
}

export function writeStoredSiteId(tenantId: string, siteId?: string): void {
  if (!tenantId || typeof sessionStorage === 'undefined') return;
  const key = `${LAST_SITE_STORAGE_PREFIX}${tenantId}`;
  try {
    if (siteId) {
      sessionStorage.setItem(key, siteId);
    } else {
      sessionStorage.removeItem(key);
    }
  } catch {
    // Ignore storage quota or restricted context errors.
  }
}

export function createTenantSiteMemory() {
  const lastSiteByTenant = new Map<string, string>();
  return {
    remember(tenantId: string, siteId: string): void {
      const trimmed = siteId.trim();
      if (!tenantId || !trimmed) return;
      lastSiteByTenant.set(tenantId, trimmed);
      writeStoredSiteId(tenantId, trimmed);
    },
    resolve(tenantId: string, sites?: ReadonlyArray<{ readonly id: string }>): string | undefined {
      if (!tenantId) return undefined;
      const candidate = lastSiteByTenant.get(tenantId) ?? readStoredSiteId(tenantId);
      if (!candidate) return undefined;
      if (sites && !sites.some((site) => site.id === candidate)) {
        lastSiteByTenant.delete(tenantId);
        writeStoredSiteId(tenantId, undefined);
        return undefined;
      }
      lastSiteByTenant.set(tenantId, candidate);
      return candidate;
    },
    clear(tenantId?: string): void {
      if (tenantId) {
        lastSiteByTenant.delete(tenantId);
        writeStoredSiteId(tenantId, undefined);
        return;
      }
      for (const key of lastSiteByTenant.keys()) {
        writeStoredSiteId(key, undefined);
      }
      lastSiteByTenant.clear();
    },
  };
}

export function problemFrom(error: unknown): ShellProblem | undefined {
  if (!error || typeof error !== 'object' || !('problem' in error)) return undefined;
  const candidate = error.problem;
  if (!candidate || typeof candidate !== 'object') return undefined;
  if (!('status' in candidate) || typeof candidate.status !== 'number') return undefined;
  if (!('code' in candidate) || typeof candidate.code !== 'string') return undefined;
  return {
    status: candidate.status,
    code: candidate.code,
    detail: 'detail' in candidate && typeof candidate.detail === 'string' ? candidate.detail : undefined,
    traceId: 'traceId' in candidate && typeof candidate.traceId === 'string' ? candidate.traceId : undefined,
    retryable: 'retryable' in candidate && typeof candidate.retryable === 'boolean' ? candidate.retryable : undefined,
  };
}

export function failureFrom(
  problem: ShellProblem | undefined,
  fallbackCode: string,
  fallbackDetail: string,
): ShellFailureView {
  const detail = problem?.detail && /[\u4e00-\u9fff]/.test(problem.detail) ? problem.detail : fallbackDetail;
  return { code: problem?.code ?? fallbackCode, detail, traceId: problem?.traceId, retryable: problem?.retryable ?? true };
}

export function isLoggedOutLanding(candidate: string, origin: string): boolean {
  try {
    const base = new URL(origin);
    const target = new URL(candidate, base);
    return target.origin === base.origin
      && target.pathname === '/'
      && target.searchParams.get('logged_out') === '1';
  } catch {
    return false;
  }
}

export function isSignInLanding(candidate: string, origin: string): boolean {
  try {
    const base = new URL(origin);
    const target = new URL(candidate, base);
    return target.origin === base.origin && target.pathname === '/sign-in';
  } catch {
    return false;
  }
}

export function hasValidSessionTiming(principal: CurrentPrincipalResponse, nowMs: number): boolean {
  const expiresAt = Date.parse(principal.session.expiresAt);
  const idleTimeoutMs = principal.session.idleTimeoutMs;
  return Number.isFinite(expiresAt) && expiresAt > nowMs && Number.isFinite(idleTimeoutMs) && idleTimeoutMs > 0;
}

export function resolveLogoutRedirectUrl(location: string | null | undefined, origin: string): string {
  if (!location) {
    throw new Error('logout response did not include the identity provider redirect');
  }
  const providerLogoutURL = new URL(location, origin);
  if (providerLogoutURL.protocol !== 'https:' && providerLogoutURL.protocol !== 'http:') {
    throw new Error('logout response used an unsupported redirect protocol');
  }
  return providerLogoutURL.toString();
}

export function mapLogoutError(
  error: unknown,
): { readonly alreadyInvalid: true } | { readonly alreadyInvalid: false; readonly view: ShellLogoutView } {
  const problem = problemFrom(error);
  if (problem && isAlreadyInvalidLogout(problem)) {
    return { alreadyInvalid: true };
  }
  const failure = failureFrom(problem, 'LOGOUT_FAILED', '服务器还没确认退出登录，请重试。');
  return {
    alreadyInvalid: false,
    view: { status: 'failed', code: failure.code, detail: failure.detail, traceId: failure.traceId, retryable: failure.retryable },
  };
}

export function mapPlatformAvailabilityError(
  error: unknown,
): { readonly loginRequiredCode?: string; readonly platform: ShellPlatformView } {
  const problem = problemFrom(error);
  if (problem && classifyBootstrapProblem(problem) === 'LOGIN_REQUIRED') {
    return { loginRequiredCode: problem.code, platform: { state: 'checking' } };
  }
  return {
    platform: {
      state: 'unavailable',
      failure: failureFrom(problem, 'PLATFORM_STATUS_UNAVAILABLE', '平台网关状态暂时不可用。'),
    },
  };
}

export function mapAuthorizedSitesError(
  error: unknown,
): { readonly loginRequiredCode?: string; readonly sites: ShellSitesView } {
  const problem = problemFrom(error);
  if (problem && classifyBootstrapProblem(problem) === 'LOGIN_REQUIRED') {
    return { loginRequiredCode: problem.code, sites: { state: 'checking' } };
  }
  const invalid = error instanceof SiteDiscoveryValidationError;
  return {
    sites: {
      state: 'unavailable',
      failure: failureFrom(
        problem,
        invalid ? 'SITE_DISCOVERY_INVALID' : 'SITE_DISCOVERY_UNAVAILABLE',
        invalid ? '读到的站点列表格式不对。' : '读不到你能看的站点。',
      ),
    },
  };
}

export function mapBootstrapError(
  error: unknown,
  loggedOutLanding: boolean,
  signInLanding: boolean,
):
  | { readonly action: 'login'; readonly reason: string; readonly navigate: boolean }
  | { readonly action: 'unavailable'; readonly failure: ShellFailureView } {
  const problem = problemFrom(error);
  if (problem && classifyBootstrapProblem(problem) === 'LOGIN_REQUIRED') {
    return {
      action: 'login',
      reason: loggedOutLanding ? 'LOGOUT_COMPLETED' : problem.code,
      navigate: !loggedOutLanding && !signInLanding,
    };
  }
  return {
    action: 'unavailable',
    failure: failureFrom(problem, 'PRINCIPAL_BOOTSTRAP_FAILED', '无法确认登录身份和会话。'),
  };
}

export function mapPolicyPurgeFailure(outcome: {
  readonly status: string;
  readonly failure?: ShellFailureView;
}): ShellFailureView {
  return outcome.status === 'failed' && outcome.failure
    ? outcome.failure
    : {
      code: 'PROTECTED_SCOPE_PURGE_FAILED',
      detail: '正在切换权限范围，请稍候。',
      retryable: false,
    };
}

export function mapTenantSwitchError(
  error: unknown,
): { readonly loginRequiredCode?: string; readonly outcome: TenantSwitchOutcome } {
  const problem = problemFrom(error);
  if (problem && classifyBootstrapProblem(problem) === 'LOGIN_REQUIRED') {
    return {
      loginRequiredCode: problem.code,
      outcome: { state: 'rejected', code: problem.code, message: '登录状态已失效，请重新登录。' },
    };
  }
  return {
    outcome: {
      state: 'rejected',
      code: problem?.code ?? 'TENANT_SWITCH_FAILED',
      message: '切换组织失败，请稍后重试。',
    },
  };
}

export function resolveSiteNavigationPreflight(input: {
  readonly state: string;
  readonly hasPrincipal: boolean;
  readonly transitionStatus?: string;
  readonly target: string;
  readonly origin: string;
  readonly sites?: ReadonlyArray<{ readonly id: string }>;
  readonly activeSiteId?: string;
}):
  | { readonly action: 'done'; readonly outcome: SiteNavigationOutcome; readonly normalizedTarget?: string }
  | { readonly action: 'purge'; readonly normalizedTarget: string } {
  if (input.state !== 'READY' || !input.hasPrincipal) {
    return { action: 'done', outcome: 'interrupted' };
  }
  if (input.transitionStatus === 'purging') {
    return { action: 'done', outcome: 'busy' };
  }
  const normalized = normalizeSiteNavigationTarget(input.target, input.origin);
  if (!normalized) {
    return { action: 'done', outcome: 'failed' };
  }
  if (normalized.siteId && !input.sites?.some((site) => site.id === normalized.siteId)) {
    return { action: 'done', outcome: 'failed' };
  }
  if (!input.activeSiteId || normalized.siteId === input.activeSiteId) {
    return { action: 'done', outcome: 'navigated', normalizedTarget: normalized.target };
  }
  return { action: 'purge', normalizedTarget: normalized.target };
}

export function platformStateFromStatus(status: string): ShellPlatformView['state'] {
  return status === 'degraded' ? 'degraded' : 'available';
}

export function canSwitchTenantState(
  state: string,
  principal: CurrentPrincipalResponse | undefined,
  tenantId: string,
): { readonly proceed: false; readonly outcome: TenantSwitchOutcome } | { readonly proceed: true; readonly previousPrincipal: CurrentPrincipalResponse } {
  if (state !== 'READY' || !principal) {
    return { proceed: false, outcome: { state: 'ignored', reason: 'UNAUTHENTICATED' } };
  }
  if (tenantId === principal.context.tenantId) {
    return { proceed: false, outcome: { state: 'unchanged' } };
  }
  return { proceed: true, previousPrincipal: principal };
}

export function buildInitialReadySnapshot(
  principal: CurrentPrincipalResponse,
  realtime: NonNullable<import('./shell-runtime-types').ShellSnapshot['realtime']>,
  canListSites: boolean,
): import('./shell-runtime-types').ShellSnapshot {
  return {
    state: 'READY',
    principal,
    platform: { state: 'checking' },
    sites: canListSites ? { state: 'checking' } : { state: 'forbidden' },
    siteTransition: undefined,
    realtime,
    logout: { status: 'idle' },
  };
}

export function createBrowserShellEnvironment(
  overrides: Partial<ShellRuntimeEnvironment> = {},
): ShellRuntimeEnvironment {
  const environment: ShellRuntimeEnvironment = {
    origin: window.location.origin,
    now: () => Date.now(),
    navigate: (target) => window.location.assign(target),
    postNavigate: (target) => {
      const form = document.createElement('form');
      form.method = 'POST';
      form.action = target;
      form.style.display = 'none';
      document.body.appendChild(form);
      form.submit();
    },
    setTimer: (handler, delayMs) => window.setTimeout(handler, delayMs),
    clearTimer: (handle) => {
      if (typeof handle === 'number') window.clearTimeout(handle);
    },
    listenForUserActivity: (handler) => {
      let lastSignalAt = 0;
      const listener = () => {
        const now = Date.now();
        if (now - lastSignalAt < 1_000) return;
        lastSignalAt = now;
        handler();
      };
      window.addEventListener('pointerdown', listener, { passive: true });
      window.addEventListener('pointermove', listener, { passive: true });
      window.addEventListener('keydown', listener);
      window.addEventListener('wheel', listener, { passive: true });
      return () => {
        window.removeEventListener('pointerdown', listener);
        window.removeEventListener('pointermove', listener);
        window.removeEventListener('keydown', listener);
        window.removeEventListener('wheel', listener);
      };
    },
  };
  return { ...environment, ...overrides };
}

export function normalizeSiteNavigationTarget(
  target: string,
  origin: string,
): { target: string; siteId?: string } | undefined {
  try {
    const resolved = new URL(target, origin);
    if (resolved.origin !== origin || resolved.username || resolved.password) {
      return undefined;
    }
    const pathname = resolved.pathname.length > 1 && resolved.pathname.endsWith('/')
      ? resolved.pathname.slice(0, -1)
      : resolved.pathname;
    const normalizedTarget = `${pathname}${resolved.search}${resolved.hash}`;
    const siteParam = resolved.searchParams.get('site');
    if (siteParam === null) return { target: normalizedTarget };
    return isUUIDv7(siteParam) ? { target: normalizedTarget, siteId: siteParam } : undefined;
  } catch {
    return undefined;
  }
}

export async function fetchAuthorizedSites(
  client: Pick<ShellRuntimeClient, 'listSites'>,
  tenantId: string,
  signal: AbortSignal,
  isStale: () => boolean,
): Promise<readonly Readonly<Site>[] | null> {
  const sites: Readonly<Site>[] = [];
  const seenSiteIDs = new Set<string>();
  const seenCursors = new Set<string>();
  let cursor: string | undefined;
  let completed = false;

  for (let page = 0; page < MAX_SITE_PAGES; page += 1) {
    const params = cursor ? { limit: SITE_PAGE_LIMIT, cursor } : { limit: SITE_PAGE_LIMIT };
    const response = await client.listSites(params, { signal });
    if (isStale()) return null;

    for (const site of response.data.items) {
      if (
        !isUUIDv7(site.id)
        || !isUUIDv7(site.tenantId)
        || site.tenantId !== tenantId
        || seenSiteIDs.has(site.id)
      ) {
        throw new SiteDiscoveryValidationError('返回的授权站点列表格式无效。');
      }
      seenSiteIDs.add(site.id);
      sites.push(Object.freeze({ ...site }));
    }

    if (!response.data.hasMore) {
      if (response.data.nextCursor !== null) {
        throw new SiteDiscoveryValidationError('站点列表最后一页仍带有下一页标记。');
      }
      completed = true;
      break;
    }

    const nextCursor = response.data.nextCursor;
    if (!nextCursor || seenCursors.has(nextCursor)) {
      throw new SiteDiscoveryValidationError('返回的站点分页标记无效。');
    }
    seenCursors.add(nextCursor);
    cursor = nextCursor;
  }

  if (!completed) {
    throw new SiteDiscoveryValidationError('站点列表分页超过最大页数上限。');
  }

  return Object.freeze([...sites]);
}

export function createSessionTimers(opts: {
  readonly client: Pick<ShellRuntimeClient, 'getCurrentPrincipal'>;
  readonly environment: ShellRuntimeEnvironment;
  readonly getSequence: () => number;
  readonly isReady: (sequence: number) => boolean;
  readonly isCurrent: (sequence: number) => boolean;
  readonly onPrincipalRefreshed: (principal: CurrentPrincipalResponse) => void;
  readonly onExpire: (reason: string, beginLogin: boolean) => void;
  readonly onIdleExpire: () => void;
}) {
  let expiryTimer: unknown;
  let idleTimer: unknown;
  let stopUserActivity: (() => void) | undefined;
  let idleTimeoutMs = 0;
  let lastActivitySyncAt = 0;
  let activitySyncInFlight = false;

  const scheduleIdleCheck = (delayMs: number) => {
    if (idleTimer !== undefined) opts.environment.clearTimer(idleTimer);
    idleTimer = opts.environment.setTimer(() => {
      idleTimer = undefined;
      void verifyIdleSession();
    }, Math.min(delayMs, MAX_TIMER_DELAY_MS));
  };

  const verifyIdleSession = async () => {
    const sequence = opts.getSequence();
    if (!opts.isReady(sequence) || idleTimeoutMs <= 0) return;
    try {
      const response = await opts.client.getCurrentPrincipal();
      if (!opts.isReady(sequence)) return;
      opts.onPrincipalRefreshed(response.data);
      scheduleIdleCheck(Math.min(idleTimeoutMs, IDLE_RETRY_DELAY_MS));
    } catch (error: unknown) {
      if (!opts.isCurrent(sequence)) return;
      const problem = problemFrom(error);
      if (problem && classifyBootstrapProblem(problem) === 'LOGIN_REQUIRED') {
        opts.onIdleExpire();
        return;
      }
      scheduleIdleCheck(Math.min(idleTimeoutMs, IDLE_RETRY_DELAY_MS));
    }
  };

  const recordUserActivity = () => {
    const sequence = opts.getSequence();
    if (!opts.isReady(sequence) || idleTimeoutMs <= 0) return;
    scheduleIdleCheck(idleTimeoutMs);
    const now = opts.environment.now();
    if (activitySyncInFlight || now - lastActivitySyncAt < USER_ACTIVITY_SYNC_INTERVAL_MS) return;
    lastActivitySyncAt = now;
    activitySyncInFlight = true;
    void opts.client.getCurrentPrincipal({ headers: { 'X-HVAC-User-Activity': '1' } })
      .then((response) => {
        if (!opts.isReady(sequence)) return;
        opts.onPrincipalRefreshed(response.data);
      })
      .catch((error: unknown) => {
        if (!opts.isCurrent(sequence)) return;
        const problem = problemFrom(error);
        if (problem && classifyBootstrapProblem(problem) === 'LOGIN_REQUIRED') {
          opts.onIdleExpire();
        }
      })
      .finally(() => {
        if (opts.isCurrent(sequence)) activitySyncInFlight = false;
      });
  };

  const clearExpiration = () => {
    if (expiryTimer === undefined) return;
    opts.environment.clearTimer(expiryTimer);
    expiryTimer = undefined;
  };

  const clearIdleTracking = () => {
    if (idleTimer !== undefined) {
      opts.environment.clearTimer(idleTimer);
      idleTimer = undefined;
    }
    stopUserActivity?.();
    stopUserActivity = undefined;
    idleTimeoutMs = 0;
    lastActivitySyncAt = 0;
    activitySyncInFlight = false;
  };

  const scheduleExpiration = (expiresAt: number) => {
    clearExpiration();
    const remaining = expiresAt - opts.environment.now();
    if (remaining <= 0) {
      opts.onExpire('SESSION_EXPIRED', true);
      return;
    }
    const delay = Math.min(remaining, MAX_TIMER_DELAY_MS);
    expiryTimer = opts.environment.setTimer(() => {
      expiryTimer = undefined;
      if (delay < remaining) {
        scheduleExpiration(expiresAt);
        return;
      }
      opts.onExpire('SESSION_EXPIRED', true);
    }, delay);
  };

  return {
    configureIdleTracking(timeoutMs: number) {
      clearIdleTracking();
      idleTimeoutMs = timeoutMs;
      lastActivitySyncAt = opts.environment.now();
      scheduleIdleCheck(timeoutMs);
      stopUserActivity = opts.environment.listenForUserActivity?.(recordUserActivity);
    },
    scheduleExpiration,
    clearAll() {
      clearExpiration();
      clearIdleTracking();
    },
  };
}
