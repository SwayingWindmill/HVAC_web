import { useEffect, useMemo, useState, type ReactNode } from 'react';
import { CopilotKitCoreReact } from '@copilotkit/react-core/v2/headless';
import { CopilotKitContext } from '@copilotkit/react-core/v2/context';
import { COPILOTKIT_ENABLED, COPILOTKIT_RUNTIME_URL } from './config';

export default function AiProvider({ children }: { children: ReactNode }) {
  if (!COPILOTKIT_ENABLED) return <>{children}</>;
  return <HeadlessCopilotProvider>{children}</HeadlessCopilotProvider>;
}

function HeadlessCopilotProvider({ children }: { children: ReactNode }) {
  const copilotkit = useMemo(() => new CopilotKitCoreReact({
    runtimeUrl: COPILOTKIT_RUNTIME_URL,
  }), []);
  const [executingToolCallIds, setExecutingToolCallIds] = useState<ReadonlySet<string>>(() => new Set());

  useEffect(() => {
    const subscription = copilotkit.subscribe({
      onToolExecutionStart: ({ toolCallId }: { toolCallId: string }) => {
        setExecutingToolCallIds((current) => new Set([...current, toolCallId]));
      },
      onToolExecutionEnd: ({ toolCallId }: { toolCallId: string }) => {
        setExecutingToolCallIds((current) => {
          const next = new Set(current);
          next.delete(toolCallId);
          return next;
        });
      },
      onError: ({ error, code }: { error: Error; code: string }) => {
        console.error(`[CopilotKit:${code}]`, error);
      },
    });
    return () => subscription.unsubscribe();
  }, [copilotkit]);

  return (
    <CopilotKitContext.Provider value={{ copilotkit, executingToolCallIds }}>
      {children}
    </CopilotKitContext.Provider>
  );
}
