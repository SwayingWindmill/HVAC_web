import { useMemo } from 'react';
import { create } from 'zustand';
import { useAgent } from '@copilotkit/react-core/v2/headless';
import { useAiChat } from '@/api/ai';
import { useAiApplicationContext } from './context';

export type AssistantDisplayMessage = {
  id: string;
  user: boolean;
  content: string;
};

export type AssistantSession = {
  messages: AssistantDisplayMessage[];
  input: string;
  setInput: (value: string) => void;
  replaceMessages: (messages: AssistantDisplayMessage[]) => void;
  submit: (text?: string) => void | Promise<void>;
  loading: boolean;
  stop: () => void;
  clear: () => void;
  modeLabel: string;
};

const contextEnvelope = (pageTitle: string, buildingId: string, roleLabel: string, content: string) =>
  `[当前页面：${pageTitle}；建筑：${buildingId}；角色：${roleLabel}] ${content}`;

const stripContextEnvelope = (content: string) => content.replace(/^\[当前页面：[^\]]+\]\s*/, '');

type CopilotComposerState = {
  input: string;
  setInput: (value: string) => void;
};

const useCopilotComposer = create<CopilotComposerState>((set) => ({
  input: '',
  setInput: (input) => set({ input }),
}));

export function useCopilotAssistantSession(): AssistantSession {
  const context = useAiApplicationContext();
  const { agent } = useAgent({ throttleMs: 40 });
  const { input, setInput } = useCopilotComposer();

  const messages = useMemo<AssistantDisplayMessage[]>(() => agent.messages.flatMap((rawMessage) => {
    const message = rawMessage as unknown as {
      id?: string;
      role?: string;
      content?: unknown;
    };
    if (!message.id || (message.role !== 'user' && message.role !== 'assistant')) return [];
    const content = typeof message.content === 'string'
      ? message.content
      : message.content == null
        ? ''
        : JSON.stringify(message.content);
    return [{
      id: message.id,
      user: message.role === 'user',
      content: message.role === 'user' ? stripContextEnvelope(content) : content,
    }];
  }), [agent.messages]);

  const submit = async (text?: string) => {
    const content = (text ?? input).trim();
    if (!content || agent.isRunning) return;
    setInput('');
    agent.addMessage({
      id: `user-${Date.now()}`,
      role: 'user',
      content: contextEnvelope(context.pageTitle, context.buildingId, context.roleLabel, content),
    } as Parameters<typeof agent.addMessage>[0]);
    await agent.runAgent();
  };

  return {
    messages,
    input,
    setInput,
    replaceMessages: (nextMessages) => agent.setMessages(nextMessages.map((message) => ({
      id: message.id,
      role: message.user ? 'user' : 'assistant',
      content: message.content,
    })) as Parameters<typeof agent.setMessages>[0]),
    submit,
    loading: agent.isRunning,
    stop: () => agent.abortRun(),
    clear: () => agent.setMessages([]),
    modeLabel: 'CopilotKit',
  };
}

export function useMockAssistantSession(): AssistantSession {
  const context = useAiApplicationContext();
  const {
    messages: rawMessages,
    input,
    setInput,
    replaceMessages,
    send,
    isStreaming,
    stop,
    clear,
  } = useAiChat();
  const messages = useMemo<AssistantDisplayMessage[]>(() => rawMessages.map((message) => ({
    id: message.id,
    user: message.role === 'user',
    content: message.role === 'user' ? stripContextEnvelope(message.content) : message.content,
  })), [rawMessages]);

  const submit = (text?: string) => {
    const content = (text ?? input).trim();
    if (!content) return;
    return send(contextEnvelope(context.pageTitle, context.buildingId, context.roleLabel, content));
  };

  return {
    messages,
    input,
    setInput,
    replaceMessages: (nextMessages) => replaceMessages(nextMessages.map((message) => ({
      id: message.id,
      role: message.user ? 'user' : 'assistant',
      content: message.content,
    }))),
    submit,
    loading: isStreaming,
    stop,
    clear,
    modeLabel: '本地模拟',
  };
}
