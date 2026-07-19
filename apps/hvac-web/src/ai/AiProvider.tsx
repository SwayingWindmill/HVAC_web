import { useMemo, type ReactNode } from 'react';
import { CopilotKit } from '@copilotkit/react-core/v2';
import '@copilotkit/react-core/v2/styles.css';
import {
  COPILOTKIT_RUNTIME_CONFIGURED,
  COPILOTKIT_RUNTIME_URL,
} from './config';
import { HvacMockAgent } from './HvacMockAgent';
import { AiThreadHistoryBridge } from './history';

export default function AiProvider({ children }: { children: ReactNode }) {
  const localAgents = useMemo(() => ({ default: new HvacMockAgent() }), []);

  if (COPILOTKIT_RUNTIME_CONFIGURED) {
    return (
      <CopilotKit
        runtimeUrl={COPILOTKIT_RUNTIME_URL}
        agent="default"
        enableInspector={false}
        showDevConsole={false}
      >
        <AiThreadHistoryBridge />
        {children}
      </CopilotKit>
    );
  }

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
