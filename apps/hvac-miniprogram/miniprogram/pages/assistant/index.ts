import { answerQuestion, askInSession, assistantErrorText, followSession, listSessions, startSession, stopRun } from '../../lib/agent';
import type { AgentSessionEvent, AgentSessionSnapshot } from '../../lib/agent-types';
import { presentConversation, presentHistory } from '../../lib/assistant';
import { chooseConversation, chosenConversation, takePendingQuestion } from '../../lib/assistant-choice';
import { loadConversations } from '../../lib/conversations';
import { gateway, requireSession } from '../../lib/platform';
import { currentSite, type Site } from '../../lib/site';
import { selectTab } from '../../lib/tab';
import { assistantThreadFrom } from '../../shared/thread-model';

// Questions the assistant can answer from the data it reads, to start a conversation with.
const STARTERS = [
  { topic: '电费', prompt: '今天电费比昨天多还是少？' },
  { topic: '能效', prompt: '今天哪几个小时能效最低？' },
  { topic: '设备', prompt: '哪台机组今天用电最多？' },
  { topic: '告警', prompt: '现在有哪些告警要处理？' },
] as const;

const RECENT = 3;
// Streamed words arrive many times a second; the page redraws at most this often.
const REDRAW_MS = 150;

// The tab has one page instance; its conversation state lives here between shows.
let site: Site | null = null;
let todayStart: string | null = null;
let sessions: AgentSessionSnapshot[] = [];
let live: { sessionId: string; events: AgentSessionEvent[] } | null = null;
let stopFollowing: (() => void) | null = null;
let redrawTimer: number | undefined;
// Whether new words keep the latest message in view; scrolling back up to read lets go.
let pinned = true;
let lastScrollTop = 0;

const selected = () => sessions.find((item) => item.session.id === chosenConversation()) ?? null;

const thread = () => {
  const snapshot = selected();
  return assistantThreadFrom(snapshot, live !== null && live.sessionId === snapshot?.session.id ? live.events : []);
};

/** Stops following and drops what was followed; nothing the stopped stream does afterwards reaches the page. */
const halt = () => {
  stopFollowing?.();
  stopFollowing = null;
  live = null;
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
    /** Answers whose data reads are unfolded. */
    openSteps: {} as Record<string, boolean>,
    draft: '',
    sending: false,
  },

  onShow() {
    if (!requireSession()) return;
    selectTab(this);
    this.load();
  },

  // A conversation still running when the tab shows again is followed again; its stream opens with the whole snapshot.
  onHide: halt,

  onUnload: halt,

  onPageScroll(event: WechatMiniprogram.Page.IPageScrollOption) {
    if (event.scrollTop < lastScrollTop - 8) pinned = false;
    else if (event.scrollTop > lastScrollTop) pinned = true;
    lastScrollTop = event.scrollTop;
  },

  async onPullDownRefresh() {
    this.setData({ error: '' });
    await this.load(true);
    wx.stopPullDownRefresh();
  },

  /** `rereadSites` reads the Site list again (a pull-down refresh). */
  async load(rereadSites = false) {
    // Taken first, so a question another page asked is asked now or not at all, never on a later visit.
    const question = takePendingQuestion();
    try {
      const visible = await currentSite(rereadSites);
      if (visible === null) {
        this.setData({ loading: false, noSite: true });
        return;
      }
      if (site !== null && site.id !== visible.id) {
        halt();
        chooseConversation(null);
      }
      site = visible;
      const loaded = await loadConversations(visible);
      sessions = loaded.sessions;
      todayStart = loaded.todayStart;
      // Opened for the first time, the tab shows the latest conversation if it is still answering or waiting for an answer.
      if (chosenConversation() === undefined) {
        const latest = sessions[0];
        chooseConversation(latest !== undefined && (latest.session.status === 'ACTIVE' || latest.session.status === 'WAITING_FOR_INPUT') ? latest.session.id : null);
      }
      this.setData({ loading: false, noSite: false, siteName: visible.displayName });
      // Another page (an alarm's 问助手) asked to start a conversation with its question.
      if (question !== null) {
        halt();
        chooseConversation(null);
        await this.ask(question);
        return;
      }
      this.render();
      const running = selected();
      if (running?.session.status === 'ACTIVE' && live?.sessionId !== running.session.id) this.follow(running.session.id);
    } catch (error) {
      this.setData({ loading: false, error: assistantErrorText(error) });
    }
  },

  /** Sends only what is on screen: the open conversation, or the welcome with the latest conversations. */
  render() {
    if (chosenConversation() === null || chosenConversation() === undefined) {
      this.setData({ conversation: null, recent: presentHistory(sessions.slice(0, RECENT), todayStart), hasMore: sessions.length > RECENT });
      return;
    }
    this.setData({ conversation: presentConversation(thread()) });
    if (pinned) wx.pageScrollTo({ selector: '#latest', duration: 0 });
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
    const following = { sessionId, events: [] as AgentSessionEvent[] };
    live = following;
    const siteId = site.id;
    stopFollowing = followSession(gateway, siteId, sessionId, (event) => {
      following.events.push(event);
      this.scheduleRender();
    }, (failed) => {
      if (live !== following) return;
      stopFollowing = null;
      if (failed) this.setData({ error: '与助手的连接中断了，已重新载入对话。' });
      // What the Agent keeps is what stays: read the conversations again once the answer ends.
      listSessions(gateway, siteId).then((list) => {
        if (live !== following) return;
        sessions = list;
        live = null;
        this.render();
      }, (error: unknown) => this.setData({ error: assistantErrorText(error) }));
    });
  },

  /** Sends a request that returns the conversation's new snapshot, then follows it while it runs. */
  async run(request: () => Promise<AgentSessionSnapshot>) {
    this.setData({ sending: true, error: '' });
    pinned = true;
    try {
      const next = await request();
      sessions = [next, ...sessions.filter((item) => item.session.id !== next.session.id)];
      chooseConversation(next.session.id);
      this.render();
      if (next.session.status === 'ACTIVE') this.follow(next.session.id);
    } catch (error) {
      const text = assistantErrorText(error);
      // Reload the conversation as it now stands, and keep saying what went wrong.
      await this.load();
      this.setData({ error: text });
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

  choose(event: WechatMiniprogram.TouchEvent<Record<string, never>, Record<string, never>, { value: string; open: boolean }>) {
    if (this.data.sending || !event.currentTarget.dataset.open) return;
    this.answer(event.currentTarget.dataset.value);
  },

  toggleSteps(event: WechatMiniprogram.TouchEvent<Record<string, never>, Record<string, never>, { id: string }>) {
    const id = event.currentTarget.dataset.id;
    this.setData({ [`openSteps.${id}`]: !this.data.openSteps[id] });
  },

  stop() {
    const current = thread();
    if (site === null || current.sessionId === null || current.revision === null) return;
    const [siteId, sessionId, revision] = [site.id, current.sessionId, current.revision];
    this.run(() => stopRun(gateway, siteId, sessionId, revision));
  },

  startNew() {
    if (this.data.conversation?.running) return;
    halt();
    chooseConversation(null);
    this.setData({ error: '', openSteps: {} });
    this.render();
  },

  open(event: WechatMiniprogram.TouchEvent<Record<string, never>, Record<string, never>, { id: string }>) {
    const id = event.currentTarget.dataset.id;
    halt();
    chooseConversation(id);
    pinned = true;
    this.setData({ error: '', openSteps: {} });
    this.render();
    if (selected()?.session.status === 'ACTIVE') this.follow(id);
  },

  openHistory() {
    wx.navigateTo({ url: '/pages/assistant-history/index' });
  },
});
