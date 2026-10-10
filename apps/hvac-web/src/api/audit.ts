import { useQuery } from '@tanstack/react-query';
import {
  createPlatformGatewayClient,
  type AuditSearchFilter,
  type AuditSearchRecord,
} from './generated/platformGateway.gen';

const client = createPlatformGatewayClient();

// Newest audit records of the caller's Tenant, read from the Audit Ledger through the Gateway.
// Review builds have no Audit Ledger and never query it.
export function useAuditSearch(filter: AuditSearchFilter, enabled: boolean) {
  return useQuery<readonly AuditSearchRecord[]>({
    queryKey: ['audit', 'search', filter],
    queryFn: async ({ signal }) => (await client.searchAudit(filter, { signal })).data.items,
    enabled: enabled && !__HVAC_WEB_FRONTEND_REVIEW__,
    retry: false,
  });
}
