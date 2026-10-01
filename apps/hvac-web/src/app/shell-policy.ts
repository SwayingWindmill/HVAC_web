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
