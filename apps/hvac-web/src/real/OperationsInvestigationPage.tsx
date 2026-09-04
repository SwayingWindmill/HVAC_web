import {
  AgentSessionWorkspace,
  type AgentSessionWorkspaceProps,
} from '@/features/operations/AgentSessionWorkspace';

export function OperationsInvestigationPage(props: AgentSessionWorkspaceProps) {
  return <AgentSessionWorkspace {...props} />;
}
