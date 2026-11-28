import type { CurrentPrincipalResponse } from '@/api/generated/platformGateway.gen';
import { normalizeReturnTo } from './shell-policy';
import {
  createProtectedScopeCoordinator,
  type ProtectedScopePurgeOutcome,
  type ProtectedScopeRequestToken,
  type ProtectedScopeResource,
} from './protected-scope';
import {
  assertRealtimeStatusForSite,
  combineRealtimeStates,
  createIdleRealtimeStatus,
  createRealtimeStatus,
  type RealtimeSubscriptionState,
  type RealtimeStatusUpdate,
} from './realtime-status';
import {
  buildInitialReadySnapshot,
  canSwitchTenantState,
  clearTenantSwitchSearchParams,
  createBrowserShellEnvironment,
  createSessionTimers,
  createTenantSiteMemory,
  fetchAuthorizedSites,
  hasValidSessionTiming,
  isLoggedOutLanding,
  isSignInLanding,
  mapAuthorizedSitesError,
  mapBootstrapError,
  mapLogoutError,
  mapPlatformAvailabilityError,
  mapPolicyPurgeFailure,
  mapTenantSwitchError,
  platformStateFromStatus,
  resolveLogoutRedirectUrl,
  resolveSiteNavigationPreflight,
} from './shell-runtime-helpers';
import type {
  LogoutOutcome,
  ShellRuntime,
  ShellRuntimeClient,
  ShellRuntimeEnvironment,
  ShellSiteTransitionView,
  ShellSnapshot,
  SiteNavigationOutcome,
  SwitchTenantOptions,
  TenantSwitchOutcome,
} from './shell-runtime-types';

export type {
  LogoutOutcome,
  ShellFailureView,
  ShellLogoutView,
  ShellPlatformView,
  ShellRuntime,
  ShellRuntimeEnvironment,
  ShellSiteTransitionView,
  ShellSitesView,
  ShellSnapshot,
  ShellState,
  SiteNavigationOutcome,
  SwitchTenantOptions,
  TenantSwitchOutcome,
} from './shell-runtime-types';

export { createBrowserShellEnvironment };

class BrowserShellRuntime implements ShellRuntime {
  private readonly protectedScope = createProtectedScopeCoordinator();
  private snapshot: ShellSnapshot = {
    state: 'BOOTSTRAPPING',
    protectedScope: this.protectedScope.current(),
    realtime: createIdleRealtimeStatus(),
  };
  private readonly listeners = new Set<(snapshot: ShellSnapshot) => void>();
  private readonly realtimeSources = new Map<string, RealtimeSubscriptionState>();
  private readonly siteMemory = createTenantSiteMemory();
  private readonly timers: ReturnType<typeof createSessionTimers>;
  private protectedPrincipal?: CurrentPrincipalResponse;
  private currentUrl = '/';
  private returnTo = '/';
  private bootstrapController?: AbortController;
  private sequence = 0;
  private disposed = false;

  private readonly client: ShellRuntimeClient;
  private readonly environment: ShellRuntimeEnvironment;

  constructor(
    client: ShellRuntimeClient,
    environment: ShellRuntimeEnvironment,
  ) {
    this.client = client;
    this.environment = environment;
    this.timers = createSessionTimers({
      client,
      environment,
      getSequence: () => this.sequence,
      isReady: (sequence) => this.isCurrent(sequence) && this.snapshot.state === 'READY' && Boolean(this.protectedPrincipal),
      isCurrent: (sequence) => this.isCurrent(sequence),
      onPrincipalRefreshed: (principal) => { this.protectedPrincipal = principal; },
      onExpire: (reason, beginLogin) => this.purge(reason, beginLogin),
      onIdleExpire: () => this.enterLoginRequired('SESSION_IDLE_EXPIRED', true),
    });
  }

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
    const loggedOutLanding = isLoggedOutLanding(currentUrl, this.environment.origin);
    const signInLanding = isSignInLanding(currentUrl, this.environment.origin);
    this.returnTo = loggedOutLanding || signInLanding ? '/' : normalizeReturnTo(currentUrl, this.environment.origin);
    const sequence = this.beginProtectedTransition();
    const controller = new AbortController();
    this.bootstrapController = controller;
    this.publish({ state: 'BOOTSTRAPPING' });

    try {
      const response = await this.client.getCurrentPrincipal({ signal: controller.signal });
      if (!this.isCurrent(sequence) || controller.signal.aborted) return;

      if (!hasValidSessionTiming(response.data, this.environment.now())) {
        this.enterLoginRequired('SESSION_INVALID', !signInLanding);
        return;
      }

      this.protectedPrincipal = response.data;
      const canListSites = response.data.authorization.capabilities.includes('site.list');
      this.publish(buildInitialReadySnapshot(response.data, createIdleRealtimeStatus(), canListSites));
      this.timers.scheduleExpiration(Date.parse(response.data.session.expiresAt));
      this.timers.configureIdleTracking(response.data.session.idleTimeoutMs);
      const bootstrapTasks = [this.loadPlatformAvailability(sequence, response.data, controller)];
      if (canListSites) bootstrapTasks.push(this.loadAuthorizedSites(sequence, response.data, controller));
      await Promise.all(bootstrapTasks);
      if (this.isCurrent(sequence) && this.bootstrapController === controller) {
        this.bootstrapController = undefined;
      }
    } catch (error: unknown) {
      if (!this.isCurrent(sequence) || controller.signal.aborted) return;
      this.bootstrapController = undefined;
      const mapped = mapBootstrapError(error, loggedOutLanding, signInLanding);
      if (mapped.action === 'login') {
        this.enterLoginRequired(mapped.reason, mapped.navigate);
        return;
      }
      this.publish({ state: 'UNAVAILABLE', failure: mapped.failure });
    }
  }

  retry(): Promise<void> {
    return this.bootstrap(this.currentUrl);
  }

  beginLogin(): void {
    const loginUrl = this.snapshot.loginUrl ?? this.client.loginUrl({ returnTo: this.returnTo });
    this.environment.postNavigate(loginUrl);
  }

  activateSiteScope(siteId: string): void {
    if (this.snapshot.state !== 'READY' || !this.protectedPrincipal) return;
    this.protectedScope.activate(siteId);
    this.realtimeSources.clear();
    this.siteMemory.remember(this.protectedPrincipal.context.tenantId, siteId);
    this.publish({
      ...this.snapshot,
      realtime: createRealtimeStatus({ state: 'idle', siteId }),
    });
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

  publishRealtimeStatus(update: RealtimeStatusUpdate): void {
    if (this.snapshot.state !== 'READY' || !this.protectedPrincipal) {
      throw new Error('Realtime status requires an authenticated Shell.');
    }
    const activeSiteId = this.protectedScope.current().siteId;
    if (!activeSiteId) throw new Error('Realtime status requires an active Site.');
    assertRealtimeStatusForSite(createRealtimeStatus(update), activeSiteId);
    this.realtimeSources.set(update.source, update.state);
    this.publishCombinedRealtime(activeSiteId);
  }

  releaseRealtimeStatus(source: string): void {
    if (!this.realtimeSources.delete(source)) return;
    if (this.snapshot.state !== 'READY') return;
    const activeSiteId = this.protectedScope.current().siteId;
    if (!activeSiteId) return;
    this.publishCombinedRealtime(activeSiteId);
  }

  private publishCombinedRealtime(siteId: string): void {
    const state = combineRealtimeStates(this.realtimeSources.values());
    if (this.snapshot.realtime?.state === state && this.snapshot.realtime.siteId === siteId) return;
    this.publish({ ...this.snapshot, realtime: createRealtimeStatus({ state, siteId }) });
  }

  async requestSiteNavigation(target: string): Promise<SiteNavigationOutcome> {
    const preflight = resolveSiteNavigationPreflight({
      state: this.snapshot.state,
      hasPrincipal: Boolean(this.protectedPrincipal),
      transitionStatus: this.snapshot.siteTransition?.status,
      target,
      origin: this.environment.origin,
      sites: this.snapshot.sites?.items,
      activeSiteId: this.protectedScope.current().siteId,
    });
    if (preflight.action === 'done') {
      if (preflight.outcome === 'navigated' && preflight.normalizedTarget) {
        this.currentUrl = preflight.normalizedTarget;
        this.environment.navigate(preflight.normalizedTarget);
      }
      return preflight.outcome;
    }
    return this.performSiteNavigation(preflight.normalizedTarget);
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
      this.publish({ state: 'UNAVAILABLE', failure: mapPolicyPurgeFailure(outcome) });
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
      const response = await this.client.logout(principal.session.csrfToken);
      if (!this.isCurrent(sequence) || this.protectedPrincipal !== principal) return 'completed';
      const redirectUrl = resolveLogoutRedirectUrl(response.location, this.environment.origin);
      this.completeLogout('LOGOUT_COMPLETED');
      this.environment.navigate(redirectUrl);
      return 'completed';
    } catch (error: unknown) {
      if (!this.isCurrent(sequence) || this.protectedPrincipal !== principal) return 'completed';
      const mapped = mapLogoutError(error);
      if (mapped.alreadyInvalid) {
        this.completeLogout('SESSION_ALREADY_INVALID');
        return 'completed';
      }
      this.publish({
        state: 'READY',
        principal,
        platform,
        sites,
        siteTransition: this.snapshot.siteTransition,
        logout: mapped.view,
      });
      return 'failed';
    }
  }

  clearLogoutError(): void {
    if (this.snapshot.state === 'READY' && this.snapshot.logout?.status === 'failed') {
      this.publish({ ...this.snapshot, logout: { status: 'idle' } });
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
    this.timers.clearAll();
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
    const clearsSubscription = siteTransition?.status === 'purging' || siteTransition?.status === 'failed';
    if (clearsSubscription) this.realtimeSources.clear();
    this.publish({
      state: 'READY',
      principal,
      platform: this.snapshot.platform,
      sites: this.snapshot.sites,
      realtime: clearsSubscription
        ? createIdleRealtimeStatus()
        : this.snapshot.realtime ?? createIdleRealtimeStatus(),
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

    this.currentUrl = target;
    try {
      await this.environment.navigate(target);
    } finally {
      if (this.isCurrent(sequence) && this.protectedPrincipal === principal && this.snapshot.state === 'READY') {
        this.publishReadyTransition(undefined);
      }
    }
    if (!this.isCurrent(sequence) || this.protectedPrincipal !== principal || this.snapshot.state !== 'READY') {
      return 'interrupted';
    }
    return 'navigated';
  }

  private startProtectedPurge(
    reason: 'SESSION_LOSS' | 'LOGOUT' | 'POLICY_CHANGE' | 'DISPOSE',
  ): Promise<ProtectedScopePurgeOutcome> {
    const current = this.protectedScope.current();
    if (
      current.state === 'idle'
      && !current.siteId
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
    this.timers.clearAll();
    this.protectedPrincipal = undefined;
    return this.sequence;
  }

  private isCurrent(sequence: number): boolean {
    return !this.disposed && sequence === this.sequence;
  }

  private publish(snapshot: ShellSnapshot): void {
    if (this.disposed) return;
    const tenantId = snapshot.principal?.context.tenantId ?? this.protectedPrincipal?.context.tenantId;
    const visibleSites = snapshot.sites?.state === 'available' ? snapshot.sites.items : undefined;
    const activeSiteId = this.protectedScope.current()?.siteId;
    const lastSiteId = snapshot.state === 'READY' && tenantId
      ? (activeSiteId && (!visibleSites || visibleSites.some((site) => site.id === activeSiteId))
          ? activeSiteId
          : this.siteMemory.resolve(tenantId, visibleSites))
      : undefined;
    const published = snapshot.state === 'READY'
      ? {
        ...snapshot,
        protectedScope: this.protectedScope.current(),
        lastSiteId,
        realtime: snapshot.realtime ?? this.snapshot.realtime ?? createIdleRealtimeStatus(),
      }
      : snapshot;
    this.snapshot = published;
    for (const listener of this.listeners) listener(published);
  }

  private enterLoginRequired(reason: string, navigate: boolean): void {
    if (this.protectedPrincipal) {
      this.siteMemory.clear(this.protectedPrincipal.context.tenantId);
    }
    void this.startProtectedPurge('SESSION_LOSS');
    this.bootstrapController?.abort();
    this.bootstrapController = undefined;
    this.timers.clearAll();
    this.protectedPrincipal = undefined;
    const loginUrl = this.client.loginUrl({ returnTo: this.returnTo });
    this.publish({ state: 'LOGIN_REQUIRED', loginUrl, reason });
    if (navigate) this.environment.postNavigate(loginUrl);
  }

  private completeLogout(reason: string): void {
    this.siteMemory.clear();
    void this.startProtectedPurge('LOGOUT');
    this.sequence += 1;
    this.enterLoginRequired(reason, false);
  }

  async switchTenant(tenantId: string, options: SwitchTenantOptions = {}): Promise<TenantSwitchOutcome> {
    const check = canSwitchTenantState(this.snapshot.state, this.protectedPrincipal, tenantId);
    if (!check.proceed) return check.outcome;
    const { previousPrincipal } = check;
    const switchActiveTenant = this.client.switchActiveTenant;
    if (!switchActiveTenant) {
      return { state: 'rejected', code: 'TENANT_SWITCH_UNSUPPORTED', message: '当前环境不支持切换组织。' };
    }

    try {
      const response = await switchActiveTenant(tenantId, previousPrincipal.session.csrfToken);
      const nextPrincipal = response.data;
      if (this.snapshot.state !== 'READY' || this.protectedPrincipal !== previousPrincipal) {
        return { state: 'ignored', reason: 'SUPERSEDED' };
      }

      const sequence = ++this.sequence;
      this.bootstrapController?.abort();
      const controller = new AbortController();
      this.bootstrapController = controller;
      this.protectedPrincipal = nextPrincipal;

      await this.startProtectedPurge('POLICY_CHANGE');
      options.onSwitched?.();

      const target = clearTenantSwitchSearchParams(options.currentHref ?? this.returnTo, this.environment.origin);
      this.returnTo = target;
      this.realtimeSources.clear();
      const canListSites = nextPrincipal.authorization.capabilities.includes('site.list');
      this.publish(buildInitialReadySnapshot(nextPrincipal, createIdleRealtimeStatus(), canListSites));
      this.timers.configureIdleTracking(nextPrincipal.session.idleTimeoutMs);
      const tasks: Promise<void>[] = [
        this.loadPlatformAvailability(sequence, nextPrincipal, controller),
      ];
      if (canListSites) {
        tasks.push(this.loadAuthorizedSites(sequence, nextPrincipal, controller));
      }
      await Promise.all(tasks);
      if (this.isCurrent(sequence) && !controller.signal.aborted) {
        this.environment.navigate(target);
      }
      return { state: 'switched', target };
    } catch (error: unknown) {
      const mapped = mapTenantSwitchError(error);
      if (mapped.loginRequiredCode) {
        this.enterLoginRequired(mapped.loginRequiredCode, true);
      }
      return mapped.outcome;
    }
  }

  private async loadPlatformAvailability(
    sequence: number,
    principal: CurrentPrincipalResponse,
    controller: AbortController,
  ): Promise<void> {
    try {
      const response = await this.client.getPlatformStatus({ signal: controller.signal });
      if (!this.isCurrent(sequence) || controller.signal.aborted || this.protectedPrincipal !== principal) return;
      this.publish({ ...this.snapshot, state: 'READY', principal, platform: { state: platformStateFromStatus(response.data.status), status: response.data } });
    } catch (error: unknown) {
      if (!this.isCurrent(sequence) || controller.signal.aborted || this.protectedPrincipal !== principal) return;
      const mapped = mapPlatformAvailabilityError(error);
      if (mapped.loginRequiredCode) {
        this.enterLoginRequired(mapped.loginRequiredCode, true);
        return;
      }
      this.publish({ ...this.snapshot, state: 'READY', principal, platform: mapped.platform });
    }
  }

  private async loadAuthorizedSites(
    sequence: number,
    principal: CurrentPrincipalResponse,
    controller: AbortController,
  ): Promise<void> {
    try {
      const items = await fetchAuthorizedSites(
        this.client,
        principal.context.tenantId,
        controller.signal,
        () => !this.isCurrent(sequence) || controller.signal.aborted || this.protectedPrincipal !== principal,
      );
      if (!items) return;
      this.publish({ ...this.snapshot, state: 'READY', principal, sites: { state: 'available', items } });
    } catch (error: unknown) {
      if (!this.isCurrent(sequence) || controller.signal.aborted || this.protectedPrincipal !== principal) return;
      const mapped = mapAuthorizedSitesError(error);
      if (mapped.loginRequiredCode) {
        this.enterLoginRequired(mapped.loginRequiredCode, true);
        return;
      }
      this.publish({ ...this.snapshot, state: 'READY', principal, sites: mapped.sites });
    }
  }
}

export function createShellRuntime(
  client: ShellRuntimeClient,
  environment: ShellRuntimeEnvironment = createBrowserShellEnvironment(),
): ShellRuntime {
  return new BrowserShellRuntime(client, environment);
}
