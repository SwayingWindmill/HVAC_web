import type { FC } from 'react';
import { ThreadListItemPrimitive, ThreadListPrimitive, useAuiState } from '@assistant-ui/react';
import { formatCompactTime } from '@/lib/operator-format';
import type { AssistantConversation } from '../use-assistant-session';

// My conversations on the Site (#639), built on assistant-ui's ThreadList primitives (adapted from
// its shadcn `thread-list` registry item): search, rename, archive and delete are left out.

const STATUS_WORDS: Readonly<Record<AssistantConversation['status'], string>> = Object.freeze({
  ACTIVE: '正在回答',
  WAITING_FOR_INPUT: '等你回答',
  COMPLETED: '答完',
  FAILED: '没答完',
  CANCELLED: '已停止',
});

/** The runtime knows each conversation by id and question; the list reads its start and status from here. */
export function AssistantHistory({ conversations, timezone, onChosen }: {
  readonly conversations: readonly AssistantConversation[];
  readonly timezone: string;
  /** Any choice closes the list, also the conversation already open, which assistant-ui does not switch to. */
  readonly onChosen: () => void;
}) {
  const empty = useAuiState((state) => state.threads.threadIds.length === 0);
  const byId = new Map(conversations.map((conversation) => [conversation.id, conversation]));
  return (
    <ThreadListPrimitive.Root className="flex min-h-0 flex-1 flex-col overflow-y-auto px-2 py-2">
      {empty ? (
        <p className="m-0 px-2.5 py-4 text-sm text-muted-foreground">在这个站点还没有问过助手。</p>
      ) : (
        <ul aria-label="历史对话" className="m-0 flex list-none flex-col gap-0.5 p-0">
          <ThreadListPrimitive.Items>
            {({ threadListItem }) => <HistoryItem conversation={byId.get(threadListItem.id)!} timezone={timezone} onChosen={onChosen} />}
          </ThreadListPrimitive.Items>
        </ul>
      )}
    </ThreadListPrimitive.Root>
  );
}

type ItemProps = { readonly conversation: AssistantConversation; readonly timezone: string; readonly onChosen: () => void };

const HistoryItem: FC<ItemProps> = ({ conversation, timezone, onChosen }) => {
  return (
    <ThreadListItemPrimitive.Root asChild>
      <li className="rounded-md transition-colors hover:bg-muted data-active:bg-muted">
        <ThreadListItemPrimitive.Trigger
          onClick={onChosen}
          className="flex w-full flex-col gap-0.5 rounded-md px-2.5 py-2 text-left outline-none focus-visible:ring-3 focus-visible:ring-ring-soft"
        >
          <span className="line-clamp-2 text-sm">{conversation.question}</span>
          <span className="text-xs text-muted-foreground">
            {formatCompactTime(new Date(conversation.startedAt).toISOString(), timezone)} · {STATUS_WORDS[conversation.status]}
          </span>
        </ThreadListItemPrimitive.Trigger>
      </li>
    </ThreadListItemPrimitive.Root>
  );
};
