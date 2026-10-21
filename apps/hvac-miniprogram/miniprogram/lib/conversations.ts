// My conversations on a Site and where the Site's day began, as the 助手 tab and the history list read them.

import { listSessions } from './agent';
import { siteTodayStart } from './energy';
import { gateway } from './platform';
import type { Site } from './site';

export async function loadConversations(site: Site) {
  const [sessions, todayStart] = await Promise.all([
    listSessions(gateway, site.id),
    // The Site's midnight only dates the conversations (今天 14:20); without it they list without times.
    siteTodayStart(site, new Date()).catch(() => null),
  ]);
  return { sessions, todayStart };
}
