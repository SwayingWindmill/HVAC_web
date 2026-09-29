import type { SiteDashboardSummary, SiteDashboardSummaryDelta } from './generated/platformGateway.gen';

const reconnectDelayMs = 3_000;
type TimerHandle = number;

export interface SiteDashboardEventSource {
  addSummaryListener(listener: (raw: string) => void): void;
  setErrorHandler(listener: () => void): void;
  close(): void;
}

export interface SiteDashboardLiveDependencies {
  readSummary: () => Promise<SiteDashboardSummary>;
  getSummary: () => SiteDashboardSummary | undefined;
  setSummary: (summary: SiteDashboardSummary) => void;
  parseDelta: (raw: string) => SiteDashboardSummaryDelta;
  openEventSource: (url: string) => SiteDashboardEventSource;
  schedule: (callback: () => void, delayMs: number) => TimerHandle;
  cancelSchedule: (handle: TimerHandle) => void;
}

export interface SiteDashboardLiveSession {
  close(): void;
}

export function siteDashboardSummaryEventsPath(siteId: string, baseGeneratedAt: string): string {
  return `/api/v1/sites/${encodeURIComponent(siteId)}/dashboard-summary/events?baseGeneratedAt=${encodeURIComponent(baseGeneratedAt)}`;
}

export function startSiteDashboardLive(
  tenantId: string,
  siteId: string,
  initialSummary: SiteDashboardSummary,
  dependencies: SiteDashboardLiveDependencies,
): SiteDashboardLiveSession {
  let closed = false;
  let source: SiteDashboardEventSource | null = null;
  let reconnectTimer: TimerHandle | null = null;
  let reconciling = false;

  const closeSource = () => {
    source?.close();
    source = null;
  };

  const validScope = (summary: SiteDashboardSummary) =>
    summary.tenantId === tenantId && summary.siteId === siteId;

  const connect = (baseGeneratedAt: string) => {
    if (closed) return;
    const nextSource = dependencies.openEventSource(siteDashboardSummaryEventsPath(siteId, baseGeneratedAt));
    source = nextSource;
    nextSource.addSummaryListener((raw) => {
      try {
        const delta = dependencies.parseDelta(raw);
        const current = dependencies.getSummary();
        if (!current
          || !validScope(current)
          || !validScope(delta.summary)
          || delta.baseGeneratedAt !== current.generatedAt) {
          scheduleReconcile(0);
          return;
        }
        dependencies.setSummary(delta.summary);
      } catch {
        scheduleReconcile(0);
      }
    });
    nextSource.setErrorHandler(() => scheduleReconcile(reconnectDelayMs));
  };

  const reconcile = async () => {
    if (closed || reconciling) return;
    reconciling = true;
    closeSource();
    try {
      const summary = await dependencies.readSummary();
      if (closed) return;
      if (!validScope(summary)) throw new Error('Site dashboard REST reconciliation changed Tenant or Site scope');
      dependencies.setSummary(summary);
      reconciling = false;
      connect(summary.generatedAt);
    } catch {
      reconciling = false;
      scheduleReconcile(reconnectDelayMs);
    }
  };

  function scheduleReconcile(delayMs: number) {
    closeSource();
    if (closed || reconciling || reconnectTimer !== null) return;
    reconnectTimer = dependencies.schedule(() => {
      reconnectTimer = null;
      void reconcile();
    }, delayMs);
  }

  if (!validScope(initialSummary)) throw new Error('Initial Site dashboard summary changed Tenant or Site scope');
  connect(initialSummary.generatedAt);

  return {
    close() {
      if (closed) return;
      closed = true;
      closeSource();
      if (reconnectTimer !== null) dependencies.cancelSchedule(reconnectTimer);
      reconnectTimer = null;
    },
  };
}
