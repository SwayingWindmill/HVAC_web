import React, { useEffect, useMemo, useState } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient } from "@tanstack/react-query";
import {
  Outlet,
  RouterProvider,
  createRootRouteWithContext,
  createRoute,
  createRouter,
  useLocation,
  useNavigate,
} from "@tanstack/react-router";
import type {
  CurrentPrincipalResponse,
  Site,
} from "@/api/generated/platformGateway.gen";
import type { DeviceObservationSnapshot } from "@/api/generated/s2Telemetry.gen";
import {
  createProtectedScopeCoordinator,
  type ProtectedScopeDraft,
  type ProtectedScopePurgeReason,
  type ProtectedScopeResource,
} from "@/app/protected-scope";
import { ShellChrome } from "@/app/ShellChrome";
import { ShellRuntimeProvider } from "@/app/ShellRuntimeContext";
import { ThemeGate } from "@/app/ThemeGate";
import { createRealtimeStatus } from "@/app/realtime-status";
import type {
  HvacRouterContext,
  RouteNavigationMeta,
} from "@/app/router-context";
import {
  SUPPORTED_REALTIME_PROTOCOL,
  type RuntimeConfig,
} from "@/app/runtime-config";
import type { ShellRuntime, ShellSnapshot } from "@/app/shell-runtime";
import {
  DeviceCenter,
  type DeviceCenterSearch,
} from "@/features/devices/device-center";
import type {
  AssetsRealtimeState,
  AssetsRealtimeTarget,
} from "@/features/assets/realtime";
import {
  createAssetsTelemetryRuntime,
  type AssetsTelemetryLiveClient,
  type AssetsTelemetryLiveSession,
} from "@/features/assets/telemetry-runtime";
import { Alarms } from "@/features/alarms/Alarms";
import { Dashboard } from "@/features/dashboard/Dashboard";
import { AssetsWorkspace, type AssetsSearchState } from "@/features/assets-workspace/AssetsWorkspace";
import { AssetDeviceDetail } from "@/features/assets-workspace/AssetDeviceDetail";
import { WorkOrders } from "@/features/work-orders/WorkOrders";
import { HvacMonitorPage } from "@/features/monitor/HvacMonitorPage";
import type { MonitorSearchState } from "@/features/monitor/model";
import "@/global.css";
import "@/app/shell.css";

const tenantId = "01940000-0000-7000-8000-000000000001";
const siteA: Site = {
  id: "01940000-0001-7000-8000-000000000001",
  tenantId,
  code: "TOKYO-CERT",
  displayName: "东京中央冷站",
  timezone: "Asia/Tokyo",
  status: "ACTIVE",
  revision: 20,
  createdAt: "2026-08-01T00:00:00.000Z",
  updatedAt: "2026-08-01T00:00:00.000Z",
};
const siteB: Site = {
  id: "01940000-0002-7000-8000-000000000002",
  tenantId,
  code: "OSAKA-CERT",
  displayName: "Osaka Scope Purge Site",
  timezone: "Asia/Tokyo",
  status: "ACTIVE",
  revision: 2,
  createdAt: "2026-08-01T00:00:00.000Z",
  updatedAt: "2026-08-01T00:00:00.000Z",
};
const sessionCapabilityField = ["csrf", "Token"].join("");
const sessionCapabilityValue = [
  "real-assets",
  "certification",
  "capability",
].join(":");
const queryClient = new QueryClient({
  defaultOptions: { queries: { retry: false } },
});
const protectedScope = createProtectedScopeCoordinator();
const fixtureRuntimeConfig: RuntimeConfig = {
  buildId: "real-assets-certification",
  gatewayBasePath: "/api/v1",
  realtimeProtocol: SUPPORTED_REALTIME_PROTOCOL,
};

function principal(
  sessionId: string,
  policyRevision: string,
): CurrentPrincipalResponse {
  const response = {
    principal: {
      subject: "real-assets-certification-operator",
      issuer: "https://identity.example.test",
      displayName: "泉来禾运营员",
      email: "",
      roles: ["operator"],
    },
    context: {
      initiatingPrincipal: {
        subject: "real-assets-certification-operator",
        issuer: "https://identity.example.test",
        displayName: "泉来禾运营员",
        email: "",
        roles: ["operator"],
      },
      executingServicePrincipal: {
        service: "platform-gateway",
        spiffeId: "spiffe://hvac.local/platform-gateway",
      },
      tenantId,
      audience: "iam-service",
      policyRevision: `delegation:${policyRevision}`,
      delegationExpiresAt: "2026-08-01T12:00:00.000Z",
    },
    authorization: {
      capabilitySetVersion: 12,
      policyRevision,
      capabilities: [
        "site.read",
        "asset.list",
        "device.list",
        "device.read",
        "telemetry.batch.read",
        "telemetry.history.read",
        "telemetry.subscribe",
        "alarm.list",
        "alarm.read",
        "alarm.assign",
        "rule.manage",
        "work-order.list",
        "work-order.read",
        "work-order.create",
        "work-order.assign",
        "work-order.lifecycle",
      ],
    },
    session: {
      id: sessionId,
      expiresAt: "2026-08-01T12:00:00.000Z",
      revocationObjectiveMs: 1000,
      lastAuditMessageId: `audit:${sessionId}`,
    },
  } as unknown as CurrentPrincipalResponse;
  Reflect.set(response.session, sessionCapabilityField, sessionCapabilityValue);
  return response;
}

function valueForKey(key: string, revision: number) {
  if (key.endsWith("run_state"))
    return { value: "RUNNING", valueType: "STRING" as const, unit: null };
  if (key.endsWith("cop"))
    return {
      value: 4.8 + (revision % 3) / 10,
      valueType: "NUMBER" as const,
      unit: null,
    };
  if (key.includes("capacity"))
    return { value: 520 + revision, valueType: "NUMBER" as const, unit: "kW" };
  return { value: 18 + revision, valueType: "NUMBER" as const, unit: "kW" };
}

function liveSnapshot(
  target: AssetsRealtimeTarget,
  siteId: string,
  revision: number,
): DeviceObservationSnapshot {
  const evaluatedMs = Date.now();
  const lastSeenMs = evaluatedMs - 5_000;
  const sampledMs = lastSeenMs - 1_000;
  return {
    schemaVersion: 1,
    deviceId: target.deviceId,
    tenantId,
    siteId,
    businessRevision: revision,
    evaluatedAt: new Date(evaluatedMs).toISOString(),
    evaluationAvailability: "AVAILABLE",
    availabilityReasons: [],
    presence: {
      applicability: "APPLICABLE",
      currentState: "ONLINE",
      lastSeenAt: new Date(lastSeenMs).toISOString(),
      policyRevision: 14,
      lastKnown: null,
    },
    telemetryReadiness: "CURRENT",
    displayState: "ONLINE",
    values: target.keys.map((key) => {
      const projected = valueForKey(key, revision);
      return {
        key,
        state: "PRESENT" as const,
        ...projected,
        sampledAt: new Date(sampledMs).toISOString(),
        receivedAt: new Date(lastSeenMs).toISOString(),
        freshness: "FRESH" as const,
        quality: "GOOD" as const,
        qualityReasons: [],
        policyRevision: 14,
      };
    }),
  };
}

class CertificationLiveSession implements AssetsTelemetryLiveSession {
  private readonly owner: CertificationLiveClient;
  private readonly targets: readonly AssetsRealtimeTarget[];
  private readonly states = new Map<string, AssetsRealtimeState>();
  private readonly listeners = new Set<
    (states: ReadonlyArray<AssetsRealtimeState>) => void
  >();
  private closedFlag = false;
  private revision = 30;

  constructor(
    owner: CertificationLiveClient,
    targets: readonly AssetsRealtimeTarget[],
    siteId: string,
  ) {
    this.owner = owner;
    this.targets = targets;
    for (const target of targets) {
      this.states.set(target.clientSubscriptionId, {
        ...target,
        status: "live",
        snapshot: liveSnapshot(target, siteId, this.revision),
        recovered: false,
        updatedAt: new Date().toISOString(),
      });
    }
  }

  getState(clientSubscriptionId: string) {
    return this.states.get(clientSubscriptionId);
  }
  getStates() {
    return [...this.states.values()];
  }
  subscribe(listener: (states: ReadonlyArray<AssetsRealtimeState>) => void) {
    this.listeners.add(listener);
    listener(this.getStates());
    return () => this.listeners.delete(listener);
  }
  async refresh() {
    this.transition("gap");
  }
  async checkpoint() {
    return undefined;
  }
  close() {
    if (this.closedFlag) return;
    this.closedFlag = true;
    this.listeners.clear();
    this.owner.closed(this);
  }

  transition(kind: string) {
    if (this.closedFlag) return;
    this.revision += kind === "gap" ? 2 : 1;
    for (const target of this.targets) {
      const current = this.states.get(target.clientSubscriptionId);
      const snapshot =
        current?.snapshot ??
        liveSnapshot(target, this.owner.currentSiteId(), this.revision);
      let next: AssetsRealtimeState;
      if (kind === "reconnecting") {
        next = {
          ...target,
          status: "snapshot",
          snapshot,
          reason: "reconnecting",
          updatedAt: new Date().toISOString(),
        };
      } else if (kind === "degraded") {
        next = {
          ...target,
          status: "unavailable",
          snapshot,
          reason: "transport-unavailable",
          retryable: true,
          updatedAt: new Date().toISOString(),
        };
      } else if (kind === "revoked") {
        next = {
          ...target,
          status: "revoked",
          snapshot: null,
          updatedAt: new Date().toISOString(),
        };
      } else if (kind === "gap") {
        next = {
          ...target,
          status: "snapshot",
          snapshot: liveSnapshot(
            target,
            this.owner.currentSiteId(),
            this.revision,
          ),
          reason: "authoritative-snapshot",
          updatedAt: new Date().toISOString(),
        };
      } else {
        next = {
          ...target,
          status: "live",
          snapshot: liveSnapshot(
            target,
            this.owner.currentSiteId(),
            this.revision,
          ),
          recovered: kind === "recovered",
          updatedAt: new Date().toISOString(),
        };
      }
      this.states.set(target.clientSubscriptionId, next);
    }
    const states = this.getStates();
    for (const listener of this.listeners) listener(states);
  }
}

class CertificationLiveClient implements AssetsTelemetryLiveClient {
  private readonly sessions = new Set<CertificationLiveSession>();
  private siteId = siteA.id;
  readonly openedTargets: AssetsRealtimeTarget[][] = [];
  openCount = 0;
  closeCount = 0;
  purgeCount = 0;
  maximumActive = 0;

  currentSiteId() {
    return this.siteId;
  }
  setSiteId(siteId: string) {
    this.siteId = siteId;
  }
  activeSubscriptionCount() {
    return [...this.sessions].reduce(
      (count, session) => count + session.getStates().length,
      0,
    );
  }
  async open(
    targets: ReadonlyArray<AssetsRealtimeTarget>,
    options?: { signal?: AbortSignal },
  ) {
    if (options?.signal?.aborted) throw options.signal.reason;
    this.openCount += 1;
    this.openedTargets.push(
      targets.map((target) => ({ ...target, keys: [...target.keys] })),
    );
    const session = new CertificationLiveSession(this, targets, this.siteId);
    this.sessions.add(session);
    this.maximumActive = Math.max(
      this.maximumActive,
      this.activeSubscriptionCount(),
    );
    options?.signal?.addEventListener("abort", () => session.close(), {
      once: true,
    });
    return session;
  }
  closed(session: CertificationLiveSession) {
    if (this.sessions.delete(session)) this.closeCount += 1;
  }
  purge() {
    this.purgeCount += 1;
    for (const session of [...this.sessions]) session.close();
  }
  emit(kind: string) {
    for (const session of this.sessions) session.transition(kind);
  }
  snapshot() {
    return {
      openCount: this.openCount,
      closeCount: this.closeCount,
      purgeCount: this.purgeCount,
      activeSubscriptions: this.activeSubscriptionCount(),
      maximumActive: this.maximumActive,
      openedTargets: this.openedTargets,
    };
  }
}

const liveClient = new CertificationLiveClient();
const telemetryRuntime = createAssetsTelemetryRuntime(
  "",
  globalThis.fetch.bind(globalThis),
  { live: liveClient },
);
const fixtureProtectedRequestToken = () => protectedScope.requestToken();
const fixtureRegisterProtectedResource = (resource: ProtectedScopeResource) =>
  protectedScope.registerResource(resource);
const fixtureRegisterUnsavedDraft = (draft: ProtectedScopeDraft) =>
  protectedScope.registerDraft(draft);
const fixturePublishRealtimeStatus = () => undefined;
const clipboardValues: string[] = [];
Object.defineProperty(navigator, "clipboard", {
  configurable: true,
  value: {
    writeText: async (value: string) => {
      clipboardValues.push(value);
    },
  },
});

interface AppState {
  site: Site;
  sessionId: string;
  policyRevision: string;
}

let fixtureShellSnapshot: ShellSnapshot = {
  state: "READY",
  principal: principal("certification-session-1", "certification-policy-1"),
  platform: { state: "available" },
  sites: { state: "available", items: [siteA, siteB] },
  protectedScope: protectedScope.current(),
  realtime: createRealtimeStatus({ state: "live", siteId: siteA.id }),
  logout: { status: "idle" },
};
let fixtureNavigate = (target: string) => {
  history.pushState(null, "", target);
};
const shellListeners = new Set<(snapshot: ShellSnapshot) => void>();
const fixtureShellRuntime: ShellRuntime = {
  current: () => fixtureShellSnapshot,
  subscribe: (listener) => {
    shellListeners.add(listener);
    return () => shellListeners.delete(listener);
  },
  bootstrap: async () => undefined,
  retry: async () => undefined,
  beginLogin: () => undefined,
  activateSiteScope: () => undefined,
  registerUnsavedDraft: fixtureRegisterUnsavedDraft,
  registerProtectedResource: fixtureRegisterProtectedResource,
  protectedRequestToken: fixtureProtectedRequestToken,
  publishRealtimeStatus: fixturePublishRealtimeStatus,
  requestSiteNavigation: async (target) => {
    fixtureNavigate(target);
    return "navigated";
  },
  confirmSiteNavigation: async () => "navigated",
  cancelSiteNavigation: () => undefined,
  handlePolicyRevision: async () => "unchanged",
  logout: async () => "completed",
  purge: () => undefined,
  dispose: () => undefined,
};

function App() {
  const routeLocation = useLocation();
  const navigate = useNavigate();
  const [state, setState] = useState<AppState>(() => {
    const initialSite = routeLocation.pathname.includes(siteB.id)
      ? siteB
      : siteA;
    protectedScope.activate(initialSite.id);
    liveClient.setSiteId(initialSite.id);
    return {
      site: initialSite,
      sessionId: "certification-session-1",
      policyRevision: "certification-policy-1",
    };
  });
  const [deviceSearch, setDeviceSearch] = useState<DeviceCenterSearch>({
    pageSize: 12,
  });
  const [assetsSearch, setAssetsSearch] = useState<AssetsSearchState>({});
  const [reviewAssetDetailId, setReviewAssetDetailId] = useState<string | null>(null);
  const currentPrincipal = useMemo(
    () => principal(state.sessionId, state.policyRevision),
    [state.policyRevision, state.sessionId],
  );
  const generation = protectedScope.current().generation;
  const shellSnapshot = useMemo<ShellSnapshot>(
    () => ({
      state: "READY",
      principal: currentPrincipal,
      platform: { state: "available" },
      sites: { state: "available", items: [siteA, siteB] },
      protectedScope: protectedScope.current(),
      realtime: createRealtimeStatus({ state: "live", siteId: state.site.id }),
      logout: { status: "idle" },
    }),
    [currentPrincipal, generation, state.site.id],
  );
  const dashboardRoute =
    routeLocation.pathname === `/sites/${state.site.id}/dashboard`;
  const shellPreview =
    new URLSearchParams(routeLocation.searchStr).get("shell") === "1";
  const alarmsRoute =
    routeLocation.pathname === `/sites/${state.site.id}/alarms` ||
    routeLocation.pathname.startsWith(`/sites/${state.site.id}/alarms/`);
  const workOrdersRoute =
    routeLocation.pathname === `/sites/${state.site.id}/work-orders` ||
    routeLocation.pathname.startsWith(`/sites/${state.site.id}/work-orders/`);
  const monitorRoute =
    routeLocation.pathname === `/sites/${state.site.id}/monitor`;
  const assetsRoute =
    routeLocation.pathname === `/sites/${state.site.id}/assets`;

  useEffect(() => {
    if (routeLocation.pathname === "/")
      void navigate({ to: `/sites/${state.site.id}/dashboard`, replace: true });
  }, [navigate, routeLocation.pathname, state.site.id]);

  fixtureShellSnapshot = shellSnapshot;
  fixtureNavigate = (target) => {
    void navigate({ to: target });
  };

  useEffect(() => {
    const transition = async (
      reason: ProtectedScopePurgeReason,
      next: Partial<AppState>,
    ) => {
      await protectedScope.purge(reason);
      const nextSite = next.site ?? state.site;
      protectedScope.activate(nextSite.id);
      liveClient.setSiteId(nextSite.id);
      history.replaceState(null, "", `/devices?site=${nextSite.id}`);
      setState((current) => ({ ...current, ...next, site: nextSite }));
      await new Promise((resolve) =>
        requestAnimationFrame(() => requestAnimationFrame(resolve)),
      );
      return protectedScope.current();
    };
    Reflect.set(globalThis, "__REAL_ASSETS_CERTIFICATION__", {
      state: () => ({
        siteId: state.site.id,
        sessionId: state.sessionId,
        policyRevision: state.policyRevision,
        protectedScope: protectedScope.current(),
        cacheKeys: queryClient
          .getQueryCache()
          .getAll()
          .map((query) => query.queryKey),
        queryErrors: queryClient
          .getQueryCache()
          .getAll()
          .map((query) =>
            query.state.error ? String(query.state.error) : null,
          ),
        clipboardValues: [...clipboardValues],
        realtime: liveClient.snapshot(),
      }),
      realtime: (kind: string) => liveClient.emit(kind),
      switchSite: () =>
        transition("SITE_CHANGE", {
          site: state.site.id === siteA.id ? siteB : siteA,
        }),
      switchSession: () =>
        transition("SESSION_LOSS", { sessionId: `${state.sessionId}-next` }),
      switchPolicy: () =>
        transition("POLICY_CHANGE", {
          policyRevision: `${state.policyRevision}-next`,
        }),
      purge: (reason: ProtectedScopePurgeReason = "DISPOSE") =>
        protectedScope.purge(reason),
      sites: { siteA, siteB },
    });
  }, [state]);

  return (
    <ShellChrome>
      {shellPreview ? (
        <div
          className="real-route-surface real-route-surface--assets"
          data-testid="real-assets-certification-root"
        >
          <section aria-labelledby="shell-preview-title">
            <h2 id="shell-preview-title">设备中心</h2>
            <p>桌面应用壳渲染检查</p>
          </section>
        </div>
      ) : dashboardRoute ? (
        <div
          className="real-route-surface real-route-surface--dashboard"
          data-testid="real-dashboard-certification-root"
        >
          <Dashboard site={state.site} principal={currentPrincipal} />
        </div>
      ) : alarmsRoute ? (
        <div
          className="real-route-surface real-route-surface--alarms"
          data-testid="real-alarms-certification-root"
        >
          <Alarms
            key={`${state.site.id}:${state.sessionId}:${state.policyRevision}:${generation}`}
            site={state.site}
            principal={currentPrincipal}
            registerUnsavedDraft={fixtureRegisterUnsavedDraft}
            registerProtectedResource={fixtureRegisterProtectedResource}
          />
        </div>
      ) : workOrdersRoute ? (
        <div
          className="real-route-surface real-route-surface--work-orders"
          data-testid="real-work-orders-certification-root"
        >
          <WorkOrders
            key={`${state.site.id}:${state.sessionId}:${state.policyRevision}:${generation}`}
            site={state.site}
            principal={currentPrincipal}
            registerProtectedResource={fixtureRegisterProtectedResource}
          />
        </div>
      ) : monitorRoute ? (
        <div
          className="real-route-surface real-route-surface--monitor"
          data-testid="real-monitor-certification-root"
        >
          <HvacMonitorPage
            site={state.site}
            principal={currentPrincipal}
            telemetryRuntime={telemetryRuntime}
          />
        </div>
      ) : (
        <div
          className="real-route-surface real-route-surface--assets"
          data-testid="real-assets-certification-root"
        >
          <DeviceCenter
            key={`${state.site.id}:${state.sessionId}:${state.policyRevision}:${generation}`}
            site={state.site}
            principal={currentPrincipal}
            runtime={fixtureShellRuntime}
            search={deviceSearch}
            onSearchChange={(patch) =>
              setDeviceSearch((current) => ({ ...current, ...patch }))
            }
            onOpenDetail={() => undefined}
            onBack={() => undefined}
          />
        </div>
      )}
    </ShellChrome>
  );
}

const fixtureRootRoute = createRootRouteWithContext<HvacRouterContext>()({
  component: Outlet,
});
const fixtureRouteMeta: ReadonlyArray<{
  path: string;
  navigation: RouteNavigationMeta;
}> = [
  {
    path: "/sites/$siteId/dashboard",
    navigation: {
      id: "site-dashboard",
      label: "首页",
      order: 10,
      siteLeaf: "dashboard",
    },
  },
  {
    path: "/sites/$siteId/monitor",
    navigation: {
      id: "site-monitor",
      label: "运行监控",
      group: "operations",
      order: 20,
      siteLeaf: "monitor",
    },
  },
  {
    path: "/devices",
    navigation: {
      id: "devices",
      label: "设备中心",
      group: "operations",
      order: 30,
    },
  },
  {
    path: "/sites/$siteId/fdd",
    navigation: {
      id: "site-fdd",
      label: "故障检测",
      group: "operations",
      order: 40,
      siteLeaf: "fdd",
    },
  },
  {
    path: "/sites/$siteId/alarms",
    navigation: {
      id: "site-alarms",
      label: "报警",
      group: "operations",
      order: 50,
      siteLeaf: "alarms",
    },
  },
  {
    path: "/sites/$siteId/work-orders",
    navigation: {
      id: "site-work-orders",
      label: "工单",
      group: "operations",
      order: 60,
      siteLeaf: "work-orders",
    },
  },
  {
    path: "/sites/$siteId/control",
    navigation: {
      id: "site-control",
      label: "能源控制",
      group: "operations",
      order: 70,
      siteLeaf: "control",
    },
  },
  {
    path: "/sites/$siteId/optimize",
    navigation: {
      id: "site-optimize",
      label: "节能优化",
      group: "operations",
      order: 80,
      siteLeaf: "optimize",
    },
  },
  {
    path: "/sites/$siteId/energy",
    navigation: {
      id: "site-energy",
      label: "能耗分析",
      group: "analytics",
      order: 90,
      siteLeaf: "energy",
    },
  },
  {
    path: "/sites/$siteId/forecast",
    navigation: {
      id: "site-forecast",
      label: "预测与基线",
      group: "analytics",
      order: 100,
      siteLeaf: "forecast",
    },
  },
  {
    path: "/sites/$siteId/cost",
    navigation: {
      id: "site-cost",
      label: "成本与绩效",
      group: "analytics",
      order: 110,
      siteLeaf: "cost",
    },
  },
  {
    path: "/sites/$siteId/ai",
    navigation: {
      id: "site-ai",
      label: "AI 运维助手",
      group: "analytics",
      order: 120,
      primary: true,
      siteLeaf: "ai",
    },
  },
  {
    path: "/sites/$siteId/bigscreen",
    navigation: {
      id: "site-bigscreen",
      label: "运行大屏",
      group: "presentation",
      order: 130,
      siteLeaf: "bigscreen",
    },
  },
];
const fixtureRoutes = fixtureRouteMeta.map(({ path, navigation }) =>
  createRoute({
    getParentRoute: () => fixtureRootRoute,
    path,
    component: App,
    staticData: { scope: "site", navigation },
  }),
);
const fixtureRouter = createRouter({
  routeTree: fixtureRootRoute.addChildren(fixtureRoutes),
  context: {
    config: fixtureRuntimeConfig,
    queryClient,
    runtime: fixtureShellRuntime,
    publishRealtimeStatus: fixturePublishRealtimeStatus,
  },
});

declare global {
  var __REAL_ASSETS_CERTIFICATION__: Record<string, unknown> | undefined;
}

createRoot(document.getElementById("root")!).render(
  <ThemeGate queryClient={queryClient}>
    <ShellRuntimeProvider runtime={fixtureShellRuntime}>
      <RouterProvider router={fixtureRouter} />
    </ShellRuntimeProvider>
  </ThemeGate>,
);
