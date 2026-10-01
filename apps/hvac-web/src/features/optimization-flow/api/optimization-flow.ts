import { queryOptions } from "@tanstack/react-query";
import { MOCK_OPPORTUNITIES } from "@/features/opportunities/api/opportunity-service";
import { MOCK_PROJECTS } from "@/features/projects/api/project-service";
import { MOCK_MV_PROJECTS } from "@/features/verification/api/verification-service";

export function optimizationFlowQueryOptions(scopeId: string) {
  return queryOptions({
    queryKey: ["optimization-flow", scopeId],
    queryFn: async () => {
      if (!__HVAC_WEB_FRONTEND_REVIEW__)
        throw new Error("节能业务数据尚未接入");
      return {
        opportunities: MOCK_OPPORTUNITIES,
        projects: MOCK_PROJECTS,
        verifications: MOCK_MV_PROJECTS,
      };
    },
    retry: false,
  });
}
