import { AssistantRuntimeProvider, useExternalStoreRuntime, type AppendMessage, type ThreadMessageLike } from '@assistant-ui/react';
import { SquarePen, Sparkles, X } from 'lucide-react';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Spinner } from '@/components/ui/spinner';
import { useAssistantPanel } from '../assistant-panel-store';
import type { AssistantMessage } from '../thread-model';
import { useAssistantSession } from '../use-assistant-session';
import { AssistantThread } from './AssistantThread';

const asThreadMessage = (message: AssistantMessage): ThreadMessageLike => message;

const textOf = (message: AppendMessage): string => message.content
  .flatMap((part) => (part.type === 'text' ? [part.text] : []))
  .join('\n')
  .trim();

/** The right-hand assistant panel for the current Site; it pushes the page over rather than covering it. */
export function AssistantPanel({ siteId, siteName, csrfToken }: { readonly siteId: string; readonly siteName: string; readonly csrfToken: string }) {
  const close = useAssistantPanel((state) => state.close);
  const takeDraft = useAssistantPanel((state) => state.takeDraft);
  const { thread, loading, error, ask, answer, cancel, startNew } = useAssistantSession(siteId, csrfToken, true);
  const pending = thread.pendingInput;

  const runtime = useExternalStoreRuntime<AssistantMessage>({
    messages: thread.messages,
    convertMessage: asThreadMessage,
    isRunning: thread.isRunning,
    activeRunId: thread.activeRunId,
    suggestions: thread.suggestions,
    onNew: async (message) => {
      const text = textOf(message);
      if (text.length === 0) return;
      // A free-text question from the assistant is answered through the composer.
      if (pending?.request.response.kind === 'TEXT') await answer(text);
      else await ask(text);
    },
    onCancel: cancel,
  });

  return (
    <aside
      aria-label="AI 助手"
      className="sticky top-(--header-height) flex h-[calc(100svh-var(--header-height))] w-[420px] shrink-0 flex-col border-l bg-card print:hidden"
      data-testid="assistant-panel"
    >
      <div className="flex h-[52px] shrink-0 items-center gap-2 border-b pr-3 pl-4">
        <span className="flex size-[26px] items-center justify-center rounded-[7px] bg-primary-soft text-primary" aria-hidden="true">
          <Sparkles className="size-3.5" />
        </span>
        <span className="text-sm font-semibold">AI 助手</span>
        <span className="flex-1" />
        <Button variant="ghost" size="icon" className="size-[30px] text-muted-foreground" aria-label="新对话" onClick={startNew} disabled={thread.isRunning}>
          <SquarePen className="size-4" />
        </Button>
        <Button variant="ghost" size="icon" className="size-[30px] text-muted-foreground" aria-label="收起助手" onClick={close}>
          <X className="size-4" />
        </Button>
      </div>
      {error !== null ? (
        <Alert variant="destructive" className="mx-4 mt-3 w-auto" data-testid="assistant-error">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      ) : null}
      {loading ? (
        <div className="flex flex-1 items-center justify-center gap-2 text-sm text-muted-foreground" role="status">
          <Spinner className="size-4" />
          正在载入对话
        </div>
      ) : (
        <AssistantRuntimeProvider runtime={runtime}>
          <AssistantThread
            siteId={siteId}
            siteName={siteName}
            answering={pending !== null}
            onAnswer={(value) => { void answer(value); }}
            takeDraft={takeDraft}
          />
        </AssistantRuntimeProvider>
      )}
    </aside>
  );
}
