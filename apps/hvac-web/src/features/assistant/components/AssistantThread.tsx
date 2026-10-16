import { createContext, useContext, useEffect, type FC } from 'react';
import {
  AuiIf,
  ComposerPrimitive,
  groupPartByType,
  MessagePrimitive,
  ThreadPrimitive,
  useAui,
  useAuiState,
} from '@assistant-ui/react';
import { ArrowDown, ArrowUp, Square } from 'lucide-react';
import { MarkdownText } from '@/components/assistant-ui/elements/markdown-text';
import { TooltipIconButton } from '@/components/assistant-ui/elements/tooltip-icon-button';
import { Button } from '@/components/ui/button';
import { useAssistantPanel } from '../assistant-panel-store';
import { FindingCard } from './FindingCard';
import { InputRequestCard } from './InputRequestCard';
import { QueryStep, QuerySteps } from './QuerySteps';

// The conversation in the assistant panel, built on assistant-ui's primitives
// (adapted from its shadcn `thread` registry item): attachments, voice, editing,
// branches and feedback are left out; steps, findings and questions are ours.

interface ThreadSurface {
  readonly siteId: string;
  readonly siteName: string;
  readonly answering: boolean;
  readonly onAnswer: (value: string) => void;
}

const ThreadSurfaceContext = createContext<ThreadSurface | null>(null);
const useThreadSurface = (): ThreadSurface => {
  const surface = useContext(ThreadSurfaceContext);
  if (surface === null) throw new Error('AssistantThread needs its surface.');
  return surface;
};

const groupBy = groupPartByType({ 'tool-call': ['group-tool'] });

export function AssistantThread(surface: ThreadSurface) {
  return (
    <ThreadSurfaceContext.Provider value={surface}>
      <ThreadPrimitive.Root className="flex min-h-0 flex-1 flex-col">
        <ThreadPrimitive.Viewport className="relative flex min-h-0 flex-1 flex-col overflow-y-auto px-4 pt-4" data-testid="assistant-thread">
          <AuiIf condition={(state) => state.thread.messages.length === 0}>
            <ThreadWelcome />
          </AuiIf>
          <div className="flex flex-col gap-4 pb-4">
            <ThreadPrimitive.Messages>{() => <ThreadMessage />}</ThreadPrimitive.Messages>
          </div>
          <ThreadPrimitive.ViewportFooter className="sticky bottom-0 mt-auto flex flex-col gap-2 bg-card pb-3">
            <ThreadPrimitive.ScrollToBottom asChild>
              <TooltipIconButton tooltip="回到最新" variant="outline" className="absolute -top-11 self-center rounded-full disabled:invisible">
                <ArrowDown />
              </TooltipIconButton>
            </ThreadPrimitive.ScrollToBottom>
            <FollowUps />
            <Composer />
            <p className="m-0 text-center text-[11px] text-muted-foreground">根据平台数据回答，只读取，不会操作设备</p>
          </ThreadPrimitive.ViewportFooter>
        </ThreadPrimitive.Viewport>
      </ThreadPrimitive.Root>
    </ThreadSurfaceContext.Provider>
  );
}

const ThreadWelcome: FC = () => (
  <div className="flex flex-1 flex-col justify-center gap-1.5 pb-10">
    <p className="m-0 text-base font-medium">想了解这个站点的什么？</p>
    <p className="m-0 text-sm leading-relaxed text-muted-foreground">可以问用电为什么偏高、哪台机组用得多、某段时间的运行情况。在能耗分析的图上点「解释」也会带着日期来问。</p>
  </div>
);

const ThreadMessage: FC = () => {
  const role = useAuiState((state) => state.message.role);
  return role === 'user' ? <UserMessage /> : <AssistantMessage />;
};

const UserMessage: FC = () => (
  <MessagePrimitive.Root className="flex justify-end" data-role="user">
    <div className="max-w-[85%] rounded-xl rounded-br-sm bg-muted px-3 py-2 text-sm leading-relaxed break-words">
      <MessagePrimitive.Parts />
    </div>
  </MessagePrimitive.Root>
);

const AssistantMessage: FC = () => {
  const { siteId, answering, onAnswer } = useThreadSurface();
  const failed = useAuiState((state) => state.message.status?.type === 'incomplete' && state.message.status.reason === 'error');
  const cancelled = useAuiState((state) => state.message.status?.type === 'incomplete' && state.message.status.reason === 'cancelled');
  const isLast = useAuiState((state) => state.message.isLast);

  return (
    <MessagePrimitive.Root className="flex flex-col gap-3 text-sm leading-relaxed" data-role="assistant">
      <MessagePrimitive.GroupedParts groupBy={groupBy}>
        {({ part, children }) => {
          switch (part.type) {
            case 'group-tool':
              return <QuerySteps count={part.indices.length} running={part.status.type === 'running'}>{children}</QuerySteps>;
            case 'tool-call':
              return <QueryStep toolName={part.toolName} result={part.result} isError={part.isError} />;
            case 'text':
              return <div className="text-foreground"><MarkdownText /></div>;
            case 'data':
              if (part.name === 'finding') return <FindingCard {...part.data} siteId={siteId} />;
              if (part.name === 'input-request') return <InputRequestCard request={part.data} onAnswer={onAnswer} open={isLast && answering} />;
              return null;
            case 'indicator':
              return <span className="text-muted-foreground">正在思考…</span>;
            default:
              return null;
          }
        }}
      </MessagePrimitive.GroupedParts>
      {failed ? <p className="m-0 text-xs text-destructive-text">这次没有答完，可以换个问法再问一次。</p> : null}
      {cancelled ? <p className="m-0 text-xs text-muted-foreground">已停止。</p> : null}
    </MessagePrimitive.Root>
  );
};

const FollowUps: FC = () => (
  <AuiIf condition={(state) => !state.thread.isRunning && state.thread.suggestions.length > 0}>
    <div className="flex flex-col gap-1.5" data-testid="assistant-follow-ups">
      <span className="text-xs text-muted-foreground">接着问</span>
      <div className="flex flex-wrap gap-1.5">
        <ThreadPrimitive.Suggestions>
          {({ suggestion }) => (
            <ThreadPrimitive.Suggestion
              prompt={suggestion.prompt}
              send
              className="rounded-md border px-2.5 py-1 text-left text-xs transition-colors hover:border-input-hover hover:bg-muted focus-visible:ring-3 focus-visible:ring-ring-soft focus-visible:outline-none"
            >
              {suggestion.prompt}
            </ThreadPrimitive.Suggestion>
          )}
        </ThreadPrimitive.Suggestions>
      </div>
    </div>
  </AuiIf>
);

const Composer: FC = () => {
  const { siteName, answering } = useThreadSurface();
  const aui = useAui();
  const draft = useAssistantPanel((state) => state.draft);
  const clearDraft = useAssistantPanel((state) => state.clearDraft);
  // A page's 解释 hands over its question, also while the panel is open; the operator can edit it before sending.
  useEffect(() => {
    if (draft === null) return;
    aui.thread.composer().setText(draft);
    clearDraft();
  }, [aui, clearDraft, draft]);

  return (
    <ComposerPrimitive.Root className="flex flex-col gap-2 rounded-[10px] border bg-card px-3 pt-2.5 pb-2 focus-within:border-input-hover">
      <ComposerPrimitive.Input
        rows={1}
        autoFocus
        placeholder={answering ? '回答助手的问题' : '问问这个站点的用电、负荷或设备'}
        aria-label="向助手提问"
        className="max-h-40 min-h-6 w-full resize-none bg-transparent text-sm leading-6 outline-none placeholder:text-subtle-foreground"
      />
      <div className="flex items-center gap-2">
        <span className="inline-flex h-[22px] max-w-[260px] items-center truncate rounded-full bg-muted px-2 text-[11px] text-muted-foreground">{siteName}</span>
        <span className="flex-1" />
        <AuiIf condition={(state) => !state.thread.isRunning}>
          <ComposerPrimitive.Send asChild>
            <Button size="icon" className="size-7 rounded-full" aria-label="发送">
              <ArrowUp className="size-4" />
            </Button>
          </ComposerPrimitive.Send>
        </AuiIf>
        <AuiIf condition={(state) => state.thread.isRunning}>
          <ComposerPrimitive.Cancel asChild>
            <Button size="sm" className="h-7 gap-1.5 rounded-full px-2.5 text-xs" aria-label="停止回答">
              <Square className="size-2.5 fill-current" aria-hidden="true" />
              停止
            </Button>
          </ComposerPrimitive.Cancel>
        </AuiIf>
      </div>
    </ComposerPrimitive.Root>
  );
};
