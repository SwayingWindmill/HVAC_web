import { useMemo, type ReactNode } from 'react';
import { CopilotKit } from '@copilotkit/react-core/v2';
import '@copilotkit/react-core/v2/styles.css';
import { HvacMockAgent } from './HvacMockAgent';
import { AiThreadHistoryBridge } from './history';

export default function AiProvider({ children }: { children: ReactNode }) {
  const localAgents = useMemo(() => ({ default: new HvacMockAgent() }), []);

  return (
    <CopilotKit
      selfManagedAgents={localAgents}
      agent="default"
      enableInspector={false}
      showDevConsole={false}
    >
      <AiThreadHistoryBridge />
      {children}
    </CopilotKit>
  );
}
