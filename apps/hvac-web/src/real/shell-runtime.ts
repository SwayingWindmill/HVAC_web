import type {
  CurrentPrincipalResponse,
  PlatformGatewayClient,
  ProblemDetails,
} from '@/api/generated/platformGateway.gen';
import {
  classifyBootstrapProblem,
  isAlreadyInvalidLogout,
  normalizeReturnTo,
} from './shell-policy';

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

export interface ShellSnapshot {
  state: ShellState;
  principal?: CurrentPrincipalResponse;
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

type ShellRuntimeClient = Pick<PlatformGatewayClient, 'getCurrentPrincipal' | 'loginUrl' | 'logout'>;

const MAX_TIMER_DELAY_MS = 2_147_000_000;

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

class BrowserShellRuntime implements ShellRuntime {
  private snapshot: ShellSnapshot = { state: 'BOOTSTRAPPING' };
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
      this.bootstrapController = undefined;

      const expiresAt = Date.parse(response.data.session.expiresAt);
      if (!Number.isFinite(expiresAt) || expiresAt <= this.environment.now()) {
        this.enterLoginRequired('SESSION_INVALID', true);
        return;
      }

      this.protectedPrincipal = response.data;
      this.publish({
        state: 'READY',
        principal: response.data,
        logout: { status: 'idle' },
      });
      this.scheduleExpiration(expiresAt);
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

  async logout(): Promise<LogoutOutcome> {
    const principal = this.protectedPrincipal;
    if (!principal || this.snapshot.state !== 'READY') return 'completed';
    if (this.snapshot.logout?.status === 'submitting') return 'failed';

    const sequence = this.sequence;
    this.publish({
      state: 'READY',
      principal,
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
    this.disposed = true;
    this.sequence += 1;
    this.bootstrapController?.abort();
    this.bootstrapController = undefined;
    this.clearExpiration();
    this.protectedPrincipal = undefined;
    this.listeners.clear();
  }

  private beginProtectedTransition(): number {
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
    this.snapshot = snapshot;
    for (const listener of this.listeners) listener(snapshot);
  }

  private enterLoginRequired(reason: string, navigate: boolean): void {
    this.bootstrapController?.abort();
    this.bootstrapController = undefined;
    this.clearExpiration();
    this.protectedPrincipal = undefined;
    const loginUrl = this.client.loginUrl({ returnTo: this.returnTo });
    this.publish({ state: 'LOGIN_REQUIRED', loginUrl, reason });
    if (navigate) this.environment.navigate(loginUrl);
  }

  private completeLogout(reason: string): void {
    this.sequence += 1;
    this.enterLoginRequired(reason, false);
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
