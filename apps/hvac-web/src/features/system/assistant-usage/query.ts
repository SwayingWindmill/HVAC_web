import { useQuery } from '@tanstack/react-query';
import { getAssistantUsage } from '@/api/agent-sessions';
import type { Site } from '@/api/generated/platformGateway.gen';

/** The assistant's last 14 Site-local days on a Site; the Gateway answers only an audit.read holder. */
export const useAssistantUsage = (site: Readonly<Site> | undefined, enabled: boolean) => useQuery({
  queryKey: ['assistant', 'usage', site?.id, site?.timezone],
  queryFn: () => getAssistantUsage(site!.id, site!.timezone),
  enabled: enabled && site !== undefined,
  retry: false,
});
