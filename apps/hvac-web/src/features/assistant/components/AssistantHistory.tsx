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

export function AssistantHistory({ timezone }: { readonly timezone: string }) {
  const empty = useAuiState((state) => state.threads.threadIds.length === 0);
  return (
    <ThreadListPrimitive.Root className="flex min-h-0 flex-1 flex-col overflow-y-auto px-2 py-2">
      {empty ? (
        <p className="m-0 px-2.5 py-4 text-sm text-muted-foreground">在这个站点还没有问过助手。</p>
      ) : (
        <ul aria-label="历史对话" className="m-0 flex list-none flex-col gap-0.5 p-0">
          <ThreadListPrimitive.Items>{({ threadListItem }) => <HistoryItem title={threadListItem.title} custom={threadListItem.custom} timezone={timezone} />}</ThreadListPrimitive.Items>
        </ul>
      )}
    </ThreadListPrimitive.Root>
  );
}

type ItemProps = { readonly title: string | undefined; readonly custom: Record<string, unknown> | undefined; readonly timezone: string };

const HistoryItem: FC<ItemProps> = ({ title, custom, timezone }) => {
  // The panel puts each conversation's start and status on the list item.
  const { startedAt, status } = custom as Pick<AssistantConversation, 'startedAt' | 'status'>;
  return (
    <ThreadListItemPrimitive.Root asChild>
      <li className="rounded-md transition-colors hover:bg-muted data-active:bg-muted">
        <ThreadListItemPrimitive.Trigger className="flex w-full flex-col gap-0.5 rounded-md px-2.5 py-2 text-left outline-none focus-visible:ring-3 focus-visible:ring-ring-soft">
          <span className="line-clamp-2 text-sm">{title}</span>
          <span className="text-xs text-muted-foreground">
            {formatCompactTime(new Date(startedAt).toISOString(), timezone)} · {STATUS_WORDS[status]}
          </span>
        </ThreadListItemPrimitive.Trigger>
      </li>
    </ThreadListItemPrimitive.Root>
  );
};
