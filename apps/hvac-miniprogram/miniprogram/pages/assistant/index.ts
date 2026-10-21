import { answerQuestion, askInSession, assistantErrorText, followSession, listSessions, startSession, stopRun } from '../../lib/agent';
import type { AgentSessionEvent, AgentSessionSnapshot } from '../../lib/agent-types';
import { presentConversation, presentHistory } from '../../lib/assistant';
import { chooseConversation, chosenConversation } from '../../lib/assistant-choice';
import { siteTodayStart } from '../../lib/energy';
import { gateway, requireSession } from '../../lib/platform';
import { currentSite, type Site } from '../../lib/site';
import { selectTab } from '../../lib/tab';
import { assistantThreadFrom } from '../../shared/thread-model';

// Questions the assistant can answer from the data it reads, to start a conversation with.
const STARTERS = [
  { topic: '用电', prompt: '今天比昨天多用了多少电？' },
  { topic: '能效', prompt: '今天哪几个小时能效最低？' },
  { topic: '设备', prompt: '哪台机组今天用电最多？' },
  { topic: '告警', prompt: '现在有哪些告警要处理？' },
] as const;

const RECENT = 3;
// Streamed words arrive many times a second; the page redraws at most this often.
const REDRAW_MS = 150;

let site: Site | null = null;
let todayStart: string | null = null;
let sessions: AgentSessionSnapshot[] = [];
let live: { sessionId: string; events: AgentSessionEvent[] } | null = null;
let stopFollowing: (() => void) | null = null;
let redrawTimer: number | undefined;

const selected = () => sessions.find((item) => item.session.id === chosenConversation()) ?? null;

const thread = () => {
  const snapshot = selected();
  return assistantThreadFrom(snapshot, live !== null && live.sessionId === snapshot?.session.id ? live.events : []);
};

const halt = () => {
  stopFollowing?.();
  stopFollowing = null;
  if (redrawTimer !== undefined) clearTimeout(redrawTimer);
  redrawTimer = undefined;
};

Page({
  data: {
    loading: true,
    noSite: false,
    siteName: '',
    error: '',
    starters: STARTERS,
    recent: [] as ReturnType<typeof presentHistory>,
    hasMore: false,
    conversation: null as ReturnType<typeof presentConversation> | null,
    draft: '',
    sending: false,
  },

  onShow() {
    if (!requireSession()) return;
    selectTab(this);
    this.load();
  },

  // A conversation found running when the tab shows again is followed again; its stream opens with the full snapshot.
  onHide: halt,

  onUnload: halt,

  async onPullDownRefresh() {
    await this.load();
    wx.stopPullDownRefresh();
  },

  async load() {
    try {
      const visible = await currentSite();
      if (visible === null) {
        this.setData({ loading: false, noSite: true });
        return;
      }
      if (site?.id !== visible.id) {
        chooseConversation(null);
        halt();
        live = null;
      }
      site = visible;
      const [list, start] = await Promise.all([listSessions(gateway, visible.id), siteTodayStart(visible, new Date()).catch(() => null)]);
      sessions = list;
      todayStart = start;
      this.setData({ loading: false, noSite: false, siteName: visible.displayName, error: '' });
      this.render();
      const running = selected();
      if (running?.session.status === 'ACTIVE') this.follow(running.session.id);
    } catch (error) {
      this.setData({ loading: false, error: assistantErrorText(error) });
    }
  },

  render() {
    const conversation = chosenConversation() === null ? null : presentConversation(thread());
    this.setData({
      recent: presentHistory(sessions.slice(0, RECENT), todayStart),
      hasMore: sessions.length > RECENT,
      conversation,
    });
    if (conversation !== null) wx.pageScrollTo({ selector: '#latest', duration: 0 });
  },

  /** Redraws soon, once, however many events arrive meanwhile. */
  scheduleRender() {
    if (redrawTimer !== undefined) return;
    redrawTimer = setTimeout(() => {
      redrawTimer = undefined;
      this.render();
    }, REDRAW_MS);
  },

  follow(sessionId: string) {
    if (site === null) return;
    halt();
    live = { sessionId, events: [] };
    const siteId = site.id;
    stopFollowing = followSession(gateway, siteId, sessionId, (event) => {
      if (live?.sessionId !== sessionId) return;
      live.events.push(event);
      this.scheduleRender();
    }, (failed) => {
      stopFollowing = null;
      if (failed) this.setData({ error: '与助手的连接中断了，已重新载入对话。' });
      // The Agent's own record is what stays: read it again once the answer ends.
      listSessions(gateway, siteId).then((list) => {
        sessions = list;
        live = null;
        this.render();
      }, (error: unknown) => this.setData({ error: assistantErrorText(error) }));
    });
  },

  /** Sends a request that returns the conversation's new snapshot, then follows it while it runs. */
  async run(request: () => Promise<AgentSessionSnapshot>) {
    this.setData({ sending: true, error: '' });
    try {
      const next = await request();
      sessions = [next, ...sessions.filter((item) => item.session.id !== next.session.id)];
      chooseConversation(next.session.id);
      this.render();
      if (next.session.status === 'ACTIVE') this.follow(next.session.id);
    } catch (error) {
      this.setData({ error: assistantErrorText(error) });
      await this.load();
    } finally {
      this.setData({ sending: false });
    }
  },

  ask(text: string) {
    if (site === null) return;
    const siteId = site.id;
    const current = thread();
    return this.run(() => (current.sessionId === null || current.revision === null
      ? startSession(gateway, siteId, text)
      : askInSession(gateway, siteId, current.sessionId, current.revision, text)));
  },

  answer(value: string) {
    const current = thread();
    const pending = current.pendingInput;
    if (site === null || pending === null || current.sessionId === null || current.revision === null) return;
    const [siteId, sessionId, revision] = [site.id, current.sessionId, current.revision];
    return this.run(() => answerQuestion(gateway, siteId, sessionId, revision, pending.id, value));
  },

  onDraft(event: WechatMiniprogram.Input) {
    this.setData({ draft: event.detail.value });
  },

  send() {
    const text = this.data.draft.trim();
    if (text === '' || this.data.sending || this.data.conversation?.running) return;
    this.setData({ draft: '' });
    // A question from the assistant asking for text is answered through the input box.
    if (this.data.conversation?.answeringByText) this.answer(text);
    else this.ask(text);
  },

  askThis(event: WechatMiniprogram.TouchEvent<Record<string, never>, Record<string, never>, { prompt: string }>) {
    if (this.data.sending || this.data.conversation?.running) return;
    this.ask(event.currentTarget.dataset.prompt);
  },

  choose(event: WechatMiniprogram.TouchEvent<Record<string, never>, Record<string, never>, { value: string }>) {
    if (this.data.sending) return;
    this.answer(event.currentTarget.dataset.value);
  },

  stop() {
    const current = thread();
    if (site === null || current.sessionId === null || current.revision === null) return;
    const [siteId, sessionId, revision] = [site.id, current.sessionId, current.revision];
    this.run(() => stopRun(gateway, siteId, sessionId, revision));
  },

  startNew() {
    halt();
    live = null;
    chooseConversation(null);
    this.setData({ error: '' });
    this.render();
  },

  open(event: WechatMiniprogram.TouchEvent<Record<string, never>, Record<string, never>, { id: string }>) {
    halt();
    live = null;
    chooseConversation(event.currentTarget.dataset.id);
    this.render();
    if (selected()?.session.status === 'ACTIVE') this.follow(event.currentTarget.dataset.id);
  },

  openHistory() {
    wx.navigateTo({ url: '/pages/assistant-history/index' });
  },
});
