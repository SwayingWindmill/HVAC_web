import { queryOptions } from "@tanstack/react-query";
import { MOCK_OPPORTUNITIES } from "@/features/opportunities/api/opportunity-service";
import { MOCK_PROJECTS } from "@/features/projects/api/project-service";
import { MOCK_MV_PROJECTS } from "@/features/verification/api/verification-service";

export function optimizationFlowQueryOptions(scopeId: string) {
  return queryOptions({
    queryKey: ["optimization-flow", scopeId],
    queryFn: async () => {
      if (!(__HVAC_WEB_FRONTEND_REVIEW__ || import.meta.env.DEV))
        throw new Error("节能业务数据尚未接入");
      if (scopeId !== "site:site-01")
        throw new Error("当前范围没有节能业务模拟数据，请选择上海恒隆广场");
      return {
        opportunities: MOCK_OPPORTUNITIES,
        projects: MOCK_PROJECTS,
        verifications: MOCK_MV_PROJECTS,
      };
    },
    retry: false,
  });
}
