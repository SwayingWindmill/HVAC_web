import { useQuery } from '@tanstack/react-query';
import {
  createPlatformGatewayClient,
  type AuditSearchFilter,
  type AuditSearchRecord,
} from '../../../api/generated/platformGateway.gen';

const client = createPlatformGatewayClient();

// Newest audit records of the caller's Tenant, read from the Audit Ledger through the Gateway.
export function useAuditSearch(filter: AuditSearchFilter, enabled: boolean) {
  return useQuery<readonly AuditSearchRecord[]>({
    queryKey: ['audit', 'search', filter],
    queryFn: async ({ signal }) => (await client.searchAudit(filter, { signal })).data.items,
    enabled,
    retry: false,
  });
}
