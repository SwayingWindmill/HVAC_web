export interface ShellProblemLike {
  status: number;
  code: string;
}

export type BootstrapFailureState = 'LOGIN_REQUIRED' | 'UNAVAILABLE';

const LOGIN_REQUIRED_CODES = new Set(['AUTHENTICATION_REQUIRED', 'SESSION_INVALID']);

export function normalizeReturnTo(candidate: string, origin: string): string {
  const value = candidate.trim();
  if (!value) return '/';
  if (!(value.startsWith('/') || value.startsWith(origin))) return '/';

  try {
    const base = new URL(origin);
    const target = new URL(value, base);
    if (target.origin !== base.origin) return '/';
    if (!target.pathname.startsWith('/')) return '/';
    return `${target.pathname}${target.search}${target.hash}`;
  } catch {
    return '/';
  }
}

export function classifyBootstrapProblem(problem: ShellProblemLike): BootstrapFailureState {
  if (problem.status === 401 && LOGIN_REQUIRED_CODES.has(problem.code)) return 'LOGIN_REQUIRED';
  return 'UNAVAILABLE';
}

export function isAlreadyInvalidLogout(problem: ShellProblemLike): boolean {
  return problem.status === 401 && LOGIN_REQUIRED_CODES.has(problem.code);
}

export interface RouterInvalidationSnapshot {
  readonly state: string;
  readonly principal?: {
    readonly principalId: string;
    readonly context: { readonly tenantId: string };
    readonly authorization: {
      readonly policyRevision: string;
      readonly capabilities: readonly string[];
    };
  };
  readonly platform?: { readonly state: string };
  readonly sites?: {
    readonly state: string;
    readonly items?: readonly { readonly id: string }[];
  };
}

function sameStringList(left: readonly string[] | undefined, right: readonly string[] | undefined): boolean {
  if (left === right) return true;
  if (!left || !right || left.length !== right.length) return false;
  for (let index = 0; index < left.length; index += 1) {
    if (left[index] !== right[index]) return false;
  }
  return true;
}

/**
 * Only snapshots affecting route guards (`beforeLoad`) need router invalidation; live telemetry,
 * protected-scope counters, logout state, and site transition chrome update via React context alone.
 */
export function shouldInvalidateRouter(
  previous: RouterInvalidationSnapshot | undefined,
  next: RouterInvalidationSnapshot,
): boolean {
  if (next.state !== 'READY' || next.sites?.state === 'checking') return false;
  if (!previous || previous.state !== 'READY' || previous.sites?.state === 'checking') return true;
  if (previous.principal?.principalId !== next.principal?.principalId) return true;
  if (previous.principal?.context.tenantId !== next.principal?.context.tenantId) return true;
  if (previous.principal?.authorization.policyRevision !== next.principal?.authorization.policyRevision) return true;
  if (!sameStringList(previous.principal?.authorization.capabilities, next.principal?.authorization.capabilities)) return true;
  if (previous.platform?.state !== next.platform?.state) return true;
  if (previous.sites?.state !== next.sites?.state) return true;
  if (previous.sites?.items !== next.sites?.items) return true;
  return false;
}
