import { useQuery } from "@tanstack/react-query";
import { readEnergyOverview } from "../api/overview-service";
import type { OverviewPeriod } from "../api/overview-types";
export function useOverviewDashboard(
  scopeId: string,
  scopeName: string,
  period: OverviewPeriod,
) {
  return useQuery({
    queryKey: ["energy-overview", scopeId, scopeName, period],
    queryFn: ({ signal }) =>
      readEnergyOverview(scopeId, scopeName, period, signal),
    staleTime: 30_000,
    retry: false,
  });
}
