import React, { useState } from "react";
import type { CurrentPrincipalResponse, Site } from "../../../apps/hvac-web/src/api/generated/platformGateway.gen";
import { createAssetsTelemetryRuntime } from "../../../apps/hvac-web/src/features/assets/telemetry-runtime";
import {
  HvacMonitorPage as ProductHvacMonitorPage,
} from "../../../apps/hvac-web/src/features/monitor/HvacMonitorPage";
import type {
  MonitorEnergyView,
  MonitorOverviewView,
  MonitorPage,
  MonitorSearchState,
} from "../../../apps/hvac-web/src/features/monitor/model";

interface FixtureHvacMonitorPageProps {
  readonly site: Readonly<Site>;
  readonly principal: CurrentPrincipalResponse;
  readonly telemetryRuntime?: ReturnType<typeof createAssetsTelemetryRuntime>;
}

function parsePage(value: string | null): MonitorPage | undefined {
  return value === "plant" || value === "terminal" || value === "analysis" || value === "modes"
    ? value
    : undefined;
}

function parseView(value: string | null): MonitorOverviewView | undefined {
  return value === "anomaly" || value === "energy" ? value : undefined;
}

function parseFlow(value: string | null): MonitorEnergyView | undefined {
  return value === "power" || value === "hydraulic" ? value : undefined;
}

function readMonitorSearch(): MonitorSearchState {
  const params = new URLSearchParams(window.location.search);
  return {
    page: parsePage(params.get("page")),
    view: parseView(params.get("view")),
    flow: parseFlow(params.get("flow")),
    device: params.get("device") ?? undefined,
    analysisDevice: params.get("analysisDevice") ?? undefined,
    alarm: params.get("alarm") ?? undefined,
    building: params.get("building") ?? undefined,
    floor: params.get("floor") ?? undefined,
    zone: params.get("zone") ?? undefined,
    opportunity: params.get("opportunity") ?? undefined,
  };
}

export function HvacMonitorPage({ site, principal, telemetryRuntime }: FixtureHvacMonitorPageProps) {
  const [search, setSearch] = useState<MonitorSearchState>(() => readMonitorSearch());

  return (
    <ProductHvacMonitorPage
      site={site}
      principal={principal}
      telemetryRuntime={telemetryRuntime}
      search={search}
      onSearchChange={(patch) => {
        setSearch((current) => ({ ...current, ...patch }));
        const params = new URLSearchParams(window.location.search);
        for (const [key, value] of Object.entries(patch)) {
          if (value == null) params.delete(key);
          else params.set(key, String(value));
        }
        const query = params.toString();
        window.history.replaceState(null, "", `${window.location.pathname}${query ? `?${query}` : ""}`);
      }}
    />
  );
}
