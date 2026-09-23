import type { CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
import type { ProtectedScopeResource } from '@/app/protected-scope';
import { AgentSessionWorkspace } from './AgentSessionWorkspace';

interface OperationsInvestigationPageProps {
  readonly site: Readonly<Site>;
  readonly principal: CurrentPrincipalResponse;
  readonly registerProtectedResource: (resource: ProtectedScopeResource) => () => void;
}

export function OperationsInvestigationPage(props: OperationsInvestigationPageProps) {
  return <AgentSessionWorkspace {...props} />;
}
