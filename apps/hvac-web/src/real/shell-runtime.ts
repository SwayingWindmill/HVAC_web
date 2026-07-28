import type {
  CurrentPrincipalResponse,
  PlatformGatewayClient,
  PlatformStatusResponse,
  ProblemDetails,
  Site,
} from '@/api/generated/platformGateway.gen';
import {
  classifyBootstrapProblem,
  isAlreadyInvalidLogout,
  normalizeReturnTo,
} from './shell-policy';
import {
  createProtectedScopeCoordinator,
  type DirtyProtectedScopeDraft,
  type ProtectedScopeDraft,
  type ProtectedScopePurgeOutcome,
  type ProtectedScopeRequestToken,
  type ProtectedScopeResource,
  type ProtectedScopeSnapshot,
} from './protected-scope';
import { isUUIDv7 } from './site-routing';

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
  | 'confirmation-required'
  | 'navigated'
  | 'failed'
  | 'busy'
  | 'interrupted';

export interface ShellSiteTransitionView {
  status: 'confirmation-required' | 'purging' | 'failed';
  target: string;
  dirtyDrafts?: readonly DirtyProtectedScopeDraft[];
  failure?: ShellFailureView;
}

export interface ShellSnapshot {
  state: ShellState;
  principal?: CurrentPrincipalResponse;
  platform?: ShellPlatformView;
  sites?: ShellSitesView;
  protectedScope?: ProtectedScopeSnapshot;
  siteTransition?: ShellSiteTransitionView;
  loginUrl?: string;
  reason?: string;
  failure?: ShellFailureView;
  logout?: ShellLogoutView;
}

export interface ShellRuntime {
  current(): ShellSnapshot;
  subscribe(listener: (snapshot: ShellSnapshot) => void): () => void;
  bootstrap(currentUrl: string): Promise<void>;
  retry(): Promise<void>;
  beginLogin(): void;
  activateSiteScope(siteId: string): void;
  registerUnsavedDraft(draft: ProtectedScopeDraft): () => void;
  registerProtectedResource(resource: ProtectedScopeResource): () => void;
  protectedRequestToken(): ProtectedScopeRequestToken;
  requestSiteNavigation(target: string): Promise<SiteNavigationOutcome>;
  confirmSiteNavigation(): Promise<SiteNavigationOutcome>;
  cancelSiteNavigation(): void;
  handlePolicyRevision(policyRevision: string): Promise<'unchanged' | 'reloaded' | 'failed'>;
  logout(): Promise<LogoutOutcome>;
  purge(reason: string, beginLogin?: boolean): void;
  dispose(): void;
}

export interface ShellRuntimeEnvironment {
  origin: string;
  now(): number;
  navigate(target: string): void;
  setTimer(handler: () => void, delayMs: number): unknown;
  clearTimer(handle: unknown): void;
}

type ShellRuntimeClient = Pick<
  PlatformGatewayClient,
  'getCurrentPrincipal' | 'getPlatformStatus' | 'listOrganizationSites' | 'loginUrl' | 'logout'
>;

const MAX_TIMER_DELAY_MS = 2_147_000_000;
const SITE_PAGE_LIMIT = 100;
const MAX_SITE_PAGES = 100;

class SiteDiscoveryValidationError extends Error {}

function problemFrom(error: unknown): ProblemDetails | undefined {
  if (!error || typeof error !== 'object' || !('problem' in error)) return undefined;
  const problem = (error as { problem?: unknown }).problem;
  if (!problem || typeof problem !== 'object') return undefined;
  if (typeof (problem as { status?: unknown }).status !== 'number') return undefined;
  if (typeof (problem as { code?: unknown }).code !== 'string') return undefined;
  return problem as ProblemDetails;
}

function failureFrom(problem: ProblemDetails | undefined, fallbackCode: string, fallbackDetail: string): ShellFailureView {
  return {
    code: problem?.code ?? fallbackCode,
    detail: problem?.detail ?? fallbackDetail,
    traceId: problem?.traceId,
    retryable: problem?.retryable ?? true,
  };
}

function browserEnvironment(): ShellRuntimeEnvironment {
  return {
    origin: window.location.origin,
    now: () => Date.now(),
    navigate: (target) => window.location.assign(target),
    setTimer: (handler, delayMs) => window.setTimeout(handler, delayMs),
    clearTimer: (handle) => window.clearTimeout(handle as number),
  };
}

function normalizeSiteNavigationTarget(
  target: string,
  origin: string,
): { target: string; siteId?: string } | undefined {
  try {
    const resolved = new URL(target, origin);
    if (resolved.origin !== origin || resolved.username || resolved.password || resolved.search || resolved.hash) {
      return undefined;
    }
    const pathname = resolved.pathname.length > 1 && resolved.pathname.endsWith('/')
      ? resolved.pathname.slice(0, -1)
      : resolved.pathname;
    if (pathname === '/' || pathname === '/sites') return { target: pathname };
    const segments = pathname.split('/').filter(Boolean);
    if (segments[0] !== 'sites') return { target: pathname };
    if (!segments[1] || !isUUIDv7(segments[1])) return undefined;
    return { target: pathname, siteId: segments[1] };
  } catch {
    return undefined;
  }
}

class BrowserShellRuntime implements ShellRuntime {
  private readonly protectedScope = createProtectedScopeCoordinator();
  private snapshot: ShellSnapshot = {
    state: 'BOOTSTRAPPING',
    protectedScope: this.protectedScope.current(),
  };
  private readonly listeners = new Set<(snapshot: ShellSnapshot) => void>();
  private protectedPrincipal?: CurrentPrincipalResponse;
  private currentUrl = '/';
  private returnTo = '/';
  private bootstrapController?: AbortController;
  private expiryTimer?: unknown;
  private sequence = 0;
  private disposed = false;

  constructor(
    private readonly client: ShellRuntimeClient,
    private readonly environment: ShellRuntimeEnvironment,
  ) {}

  current(): ShellSnapshot {
    return this.snapshot;
  }

  subscribe(listener: (snapshot: ShellSnapshot) => void): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  async bootstrap(currentUrl: string): Promise<void> {
    if (this.disposed) return;
    this.currentUrl = currentUrl;
    this.returnTo = normalizeReturnTo(currentUrl, this.environment.origin);
    const sequence = this.beginProtectedTransition();
    const controller = new AbortController();
    this.bootstrapController = controller;
    this.publish({ state: 'BOOTSTRAPPING' });

    try {
      const response = await this.client.getCurrentPrincipal({ signal: controller.signal });
      if (!this.isCurrent(sequence) || controller.signal.aborted) return;

      const expiresAt = Date.parse(response.data.session.expiresAt);
      if (!Number.isFinite(expiresAt) || expiresAt <= this.environment.now()) {
        this.enterLoginRequired('SESSION_INVALID', true);
        return;
      }

      this.protectedPrincipal = response.data;
      const canListSites = response.data.authorization.capabilities.includes('site.list');
      this.publish({
        state: 'READY',
        principal: response.data,
        platform: { state: 'checking' },
        sites: { state: canListSites ? 'checking' : 'forbidden' },
        logout: { status: 'idle' },
      });
      this.scheduleExpiration(expiresAt);
      const bootstrapTasks = [this.loadPlatformAvailability(sequence, response.data, controller)];
      if (canListSites) bootstrapTasks.push(this.loadAuthorizedSites(sequence, response.data, controller));
      await Promise.all(bootstrapTasks);
      if (this.isCurrent(sequence) && this.bootstrapController === controller) {
        this.bootstrapController = undefined;
      }
    } catch (error: unknown) {
      if (!this.isCurrent(sequence) || controller.signal.aborted) return;
      this.bootstrapController = undefined;
      const problem = problemFrom(error);
      if (problem && classifyBootstrapProblem(problem) === 'LOGIN_REQUIRED') {
        this.enterLoginRequired(problem.code, true);
        return;
      }
      this.publish({
        state: 'UNAVAILABLE',
        failure: failureFrom(
          problem,
          'PRINCIPAL_BOOTSTRAP_FAILED',
          '无法建立可信的 Principal 与 Session 快照。',
        ),
      });
    }
  }

  retry(): Promise<void> {
    return this.bootstrap(this.currentUrl);
  }

  beginLogin(): void {
    const loginUrl = this.snapshot.loginUrl ?? this.client.loginUrl({ returnTo: this.returnTo });
    this.environment.navigate(loginUrl);
  }

  activateSiteScope(siteId: string): void {
    if (this.snapshot.state !== 'READY' || !this.protectedPrincipal) return;
    this.protectedScope.activate(siteId);
    this.refreshProtectedScopeView();
  }

  registerUnsavedDraft(draft: ProtectedScopeDraft): () => void {
    const unregister = this.protectedScope.registerDraft(draft);
    this.refreshProtectedScopeView();
    return () => {
      unregister();
      this.refreshProtectedScopeView();
    };
  }

  registerProtectedResource(resource: ProtectedScopeResource): () => void {
    const unregister = this.protectedScope.registerResource(resource);
    this.refreshProtectedScopeView();
    return () => {
      unregister();
      this.refreshProtectedScopeView();
    };
  }

  protectedRequestToken(): ProtectedScopeRequestToken {
    return this.protectedScope.requestToken();
  }

  async requestSiteNavigation(target: string): Promise<SiteNavigationOutcome> {
    if (this.snapshot.state !== 'READY' || !this.protectedPrincipal) return 'interrupted';
    if (this.snapshot.siteTransition?.status === 'purging') return 'busy';
    const normalized = normalizeSiteNavigationTarget(target, this.environment.origin);
    if (!normalized) return 'failed';
    if (
      normalized.siteId
      && !this.snapshot.sites?.items?.some((site) => site.id === normalized.siteId)
    ) {
      return 'failed';
    }

    const activeSiteId = this.protectedScope.current().siteId;
    if (!activeSiteId || normalized.siteId === activeSiteId) {
      this.environment.navigate(normalized.target);
      return 'navigated';
    }

    const dirtyDrafts = this.protectedScope.dirtyDrafts();
    if (dirtyDrafts.length > 0) {
      this.publishReadyTransition({
        status: 'confirmation-required',
        target: normalized.target,
        dirtyDrafts,
      });
      return 'confirmation-required';
    }

    return this.performSiteNavigation(normalized.target);
  }

  confirmSiteNavigation(): Promise<SiteNavigationOutcome> {
    const transition = this.snapshot.siteTransition;
    if (!transition || transition.status !== 'confirmation-required') {
      return Promise.resolve('interrupted');
    }
    return this.performSiteNavigation(transition.target);
  }

  cancelSiteNavigation(): void {
    if (this.snapshot.siteTransition?.status !== 'confirmation-required') return;
    this.publishReadyTransition(undefined);
  }

  async handlePolicyRevision(policyRevision: string): Promise<'unchanged' | 'reloaded' | 'failed'> {
    const principal = this.protectedPrincipal;
    if (!principal || this.snapshot.state !== 'READY') return 'failed';
    if (principal.authorization.policyRevision === policyRevision) return 'unchanged';

    const sequence = this.sequence;
    this.publishReadyTransition({ status: 'purging', target: this.returnTo });
    const purgePromise = this.protectedScope.purge('POLICY_CHANGE');
    this.refreshProtectedScopeView();
    const outcome = await purgePromise;
    if (!this.isCurrent(sequence) || this.protectedPrincipal !== principal) return 'failed';
    if (outcome.status !== 'completed') {
      const failure = outcome.status === 'failed'
        ? outcome.failure
        : {
          code: 'PROTECTED_SCOPE_PURGE_FAILED' as const,
          detail: 'Protected scope purge is already in progress.',
          retryable: false as const,
        };
      this.publish({
        state: 'UNAVAILABLE',
        failure,
      });
      return 'failed';
    }

    await this.bootstrap(this.currentUrl);
    return this.snapshot.state === 'READY'
      && this.snapshot.principal?.authorization.policyRevision === policyRevision
      ? 'reloaded'
      : 'failed';
  }

  async logout(): Promise<LogoutOutcome> {
    const principal = this.protectedPrincipal;
    if (!principal || this.snapshot.state !== 'READY') return 'completed';
    if (this.snapshot.logout?.status === 'submitting') return 'failed';

    const sequence = this.sequence;
    const platform = this.snapshot.platform;
    const sites = this.snapshot.sites;
    this.publish({
      state: 'READY',
      principal,
      platform,
      sites,
      siteTransition: this.snapshot.siteTransition,
      logout: { status: 'submitting' },
    });

    try {
      await this.client.logout(principal.session.csrfToken);
      if (!this.isCurrent(sequence) || this.protectedPrincipal !== principal) return 'completed';
      this.completeLogout('LOGOUT_COMPLETED');
      return 'completed';
    } catch (error: unknown) {
      if (!this.isCurrent(sequence) || this.protectedPrincipal !== principal) return 'completed';
      const problem = problemFrom(error);
      if (problem && isAlreadyInvalidLogout(problem)) {
        this.completeLogout('SESSION_ALREADY_INVALID');
        return 'completed';
      }
      const failure = failureFrom(
        problem,
        'LOGOUT_FAILED',
        '服务器尚未确认 Session 撤销，请重试。',
      );
      this.publish({
        state: 'READY',
        principal,
        platform,
        sites,
        siteTransition: this.snapshot.siteTransition,
        logout: {
          status: 'failed',
          code: failure.code,
          detail: failure.detail,
          traceId: failure.traceId,
          retryable: failure.retryable,
        },
      });
      return 'failed';
    }
  }

  purge(reason: string, beginLogin = false): void {
    if (this.disposed) return;
    this.beginProtectedTransition();
    this.enterLoginRequired(reason, beginLogin);
  }

  dispose(): void {
    if (this.disposed) return;
    void this.startProtectedPurge('DISPOSE');
    this.disposed = true;
    this.sequence += 1;
    this.bootstrapController?.abort();
    this.bootstrapController = undefined;
    this.clearExpiration();
    this.protectedPrincipal = undefined;
    this.listeners.clear();
  }

  private refreshProtectedScopeView(): void {
    if (this.snapshot.state !== 'READY' || !this.protectedPrincipal) return;
    this.publish({ ...this.snapshot });
  }

  private publishReadyTransition(siteTransition: ShellSiteTransitionView | undefined): void {
    const principal = this.protectedPrincipal;
    if (!principal || this.snapshot.state !== 'READY') return;
    this.publish({
      state: 'READY',
      principal,
      platform: this.snapshot.platform,
      sites: this.snapshot.sites,
      logout: this.snapshot.logout ?? { status: 'idle' },
      siteTransition,
    });
  }

  private async performSiteNavigation(target: string): Promise<SiteNavigationOutcome> {
    const principal = this.protectedPrincipal;
    if (!principal || this.snapshot.state !== 'READY') return 'interrupted';
    const sequence = this.sequence;
    this.publishReadyTransition({ status: 'purging', target });
    const purgePromise = this.protectedScope.purge('SITE_CHANGE');
    this.refreshProtectedScopeView();
    const outcome = await purgePromise;

    if (!this.isCurrent(sequence) || this.protectedPrincipal !== principal || this.snapshot.state !== 'READY') {
      return 'interrupted';
    }
    if (outcome.status === 'busy') return 'busy';
    if (outcome.status === 'failed') {
      this.publishReadyTransition({
        status: 'failed',
        target,
        failure: outcome.failure,
      });
      return 'failed';
    }

    this.environment.navigate(target);
    return 'navigated';
  }

  private startProtectedPurge(
    reason: 'SESSION_LOSS' | 'LOGOUT' | 'DISPOSE',
  ): Promise<ProtectedScopePurgeOutcome> {
    const current = this.protectedScope.current();
    if (
      current.state === 'idle'
      && !current.siteId
      && current.draftCount === 0
      && current.resourceCount === 0
    ) {
      return Promise.resolve({ status: 'completed' });
    }
    return this.protectedScope.purge(reason);
  }

  private beginProtectedTransition(): number {
    void this.startProtectedPurge('SESSION_LOSS');
    this.sequence += 1;
    this.bootstrapController?.abort();
    this.bootstrapController = undefined;
    this.clearExpiration();
    this.protectedPrincipal = undefined;
    return this.sequence;
  }

  private isCurrent(sequence: number): boolean {
    return !this.disposed && sequence === this.sequence;
  }

  private publish(snapshot: ShellSnapshot): void {
    if (this.disposed) return;
    const published = snapshot.state === 'READY'
      ? { ...snapshot, protectedScope: this.protectedScope.current() }
      : snapshot;
    this.snapshot = published;
    for (const listener of this.listeners) listener(published);
  }

  private enterLoginRequired(reason: string, navigate: boolean): void {
    void this.startProtectedPurge('SESSION_LOSS');
    this.bootstrapController?.abort();
    this.bootstrapController = undefined;
    this.clearExpiration();
    this.protectedPrincipal = undefined;
    const loginUrl = this.client.loginUrl({ returnTo: this.returnTo });
    this.publish({ state: 'LOGIN_REQUIRED', loginUrl, reason });
    if (navigate) this.environment.navigate(loginUrl);
  }

  private completeLogout(reason: string): void {
    void this.startProtectedPurge('LOGOUT');
    this.sequence += 1;
    this.enterLoginRequired(reason, false);
  }

  private async loadPlatformAvailability(
    sequence: number,
    principal: CurrentPrincipalResponse,
    controller: AbortController,
  ): Promise<void> {
    try {
      const response = await this.client.getPlatformStatus({ signal: controller.signal });
      if (!this.isCurrent(sequence) || controller.signal.aborted || this.protectedPrincipal !== principal) return;
      this.publish({
        state: 'READY',
        principal,
        platform: {
          state: response.data.status === 'degraded' ? 'degraded' : 'available',
          status: response.data,
        },
        sites: this.snapshot.sites,
        siteTransition: this.snapshot.siteTransition,
        logout: this.snapshot.logout ?? { status: 'idle' },
      });
    } catch (error: unknown) {
      if (!this.isCurrent(sequence) || controller.signal.aborted || this.protectedPrincipal !== principal) return;
      const problem = problemFrom(error);
      if (problem && classifyBootstrapProblem(problem) === 'LOGIN_REQUIRED') {
        this.enterLoginRequired(problem.code, true);
        return;
      }
      this.publish({
        state: 'READY',
        principal,
        platform: {
          state: 'unavailable',
          failure: failureFrom(
            problem,
            'PLATFORM_STATUS_UNAVAILABLE',
            'Platform Gateway 状态暂时不可用。',
          ),
        },
        sites: this.snapshot.sites,
        siteTransition: this.snapshot.siteTransition,
        logout: this.snapshot.logout ?? { status: 'idle' },
      });
    }
  }

  private async loadAuthorizedSites(
    sequence: number,
    principal: CurrentPrincipalResponse,
    controller: AbortController,
  ): Promise<void> {
    const actingOrganizationId = principal.context.actingOrganizationId;
    try {
      const sites: Readonly<Site>[] = [];
      const seenSiteIDs = new Set<string>();
      const seenCursors = new Set<string>();
      let cursor: string | undefined;
      let completed = false;

      for (let page = 0; page < MAX_SITE_PAGES; page += 1) {
        const params = cursor ? { limit: SITE_PAGE_LIMIT, cursor } : { limit: SITE_PAGE_LIMIT };
        const response = await this.client.listOrganizationSites(
          actingOrganizationId,
          params,
          { signal: controller.signal },
        );
        if (!this.isCurrent(sequence) || controller.signal.aborted || this.protectedPrincipal !== principal) return;

        for (const site of response.data.items) {
          if (
            !isUUIDv7(site.id)
            || site.owningOrganizationId !== actingOrganizationId
            || seenSiteIDs.has(site.id)
          ) {
            throw new SiteDiscoveryValidationError('Registry returned an invalid authorized Site collection.');
          }
          seenSiteIDs.add(site.id);
          sites.push(Object.freeze({ ...site }));
        }

        if (!response.data.hasMore) {
          if (response.data.nextCursor !== null) {
            throw new SiteDiscoveryValidationError('Registry returned a cursor after the final Site page.');
          }
          completed = true;
          break;
        }

        const nextCursor = response.data.nextCursor;
        if (!nextCursor || seenCursors.has(nextCursor)) {
          throw new SiteDiscoveryValidationError('Registry returned an invalid Site pagination cursor.');
        }
        seenCursors.add(nextCursor);
        cursor = nextCursor;
      }

      if (!completed) {
        throw new SiteDiscoveryValidationError('Registry Site pagination exceeded the bounded page count.');
      }

      this.publish({
        state: 'READY',
        principal,
        platform: this.snapshot.platform,
        sites: {
          state: 'available',
          items: Object.freeze([...sites]),
        },
        siteTransition: this.snapshot.siteTransition,
        logout: this.snapshot.logout ?? { status: 'idle' },
      });
    } catch (error: unknown) {
      if (!this.isCurrent(sequence) || controller.signal.aborted || this.protectedPrincipal !== principal) return;
      const problem = problemFrom(error);
      if (problem && classifyBootstrapProblem(problem) === 'LOGIN_REQUIRED') {
        this.enterLoginRequired(problem.code, true);
        return;
      }
      const invalid = error instanceof SiteDiscoveryValidationError;
      this.publish({
        state: 'READY',
        principal,
        platform: this.snapshot.platform,
        sites: {
          state: 'unavailable',
          failure: failureFrom(
            problem,
            invalid ? 'SITE_DISCOVERY_INVALID' : 'SITE_DISCOVERY_UNAVAILABLE',
            invalid
              ? 'Registry 返回了无效的授权 Site 集合。'
              : '无法读取当前 Organization 的授权 Site。',
          ),
        },
        siteTransition: this.snapshot.siteTransition,
        logout: this.snapshot.logout ?? { status: 'idle' },
      });
    }
  }

  private scheduleExpiration(expiresAt: number): void {
    this.clearExpiration();
    const remaining = expiresAt - this.environment.now();
    if (remaining <= 0) {
      this.purge('SESSION_EXPIRED', true);
      return;
    }
    const delay = Math.min(remaining, MAX_TIMER_DELAY_MS);
    this.expiryTimer = this.environment.setTimer(() => {
      this.expiryTimer = undefined;
      if (delay < remaining) {
        this.scheduleExpiration(expiresAt);
        return;
      }
      this.purge('SESSION_EXPIRED', true);
    }, delay);
  }

  private clearExpiration(): void {
    if (this.expiryTimer === undefined) return;
    this.environment.clearTimer(this.expiryTimer);
    this.expiryTimer = undefined;
  }
}

export function createShellRuntime(
  client: ShellRuntimeClient,
  environment: ShellRuntimeEnvironment = browserEnvironment(),
): ShellRuntime {
  return new BrowserShellRuntime(client, environment);
}
