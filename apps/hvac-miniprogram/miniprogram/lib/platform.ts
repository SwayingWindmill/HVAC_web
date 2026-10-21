// The Gateway bound to WeChat: wx.request for the exchange and local storage for the Session.
// The platform's domain must be on the mini program's request allowlist in 微信公众平台.

import { createGateway, type ChunkedTransport, type SessionStore, type Transport } from './gateway';
import type { AskWeChat } from './reminders';

export const GATEWAY_ORIGIN = 'https://hvac.swayingwindmill.online';
const SESSION_KEY = 'hvac.session';

/** wx.login: the one-time code the platform trades for this person's WeChat. */
export const weChatLogin = () => new Promise<string>((resolve, reject) => {
  wx.login({ success: (result) => resolve(result.code), fail: reject });
});
const REQUEST_TIMEOUT_MS = 15_000;

const session: SessionStore = {
  get: () => (wx.getStorageSync(SESSION_KEY) as string) || null,
  set: (token) => wx.setStorageSync(SESSION_KEY, token),
  clear: () => wx.removeStorageSync(SESSION_KEY),
};

const transport: Transport = (request) => new Promise((resolve, reject) => {
  wx.request({
    url: request.url,
    method: request.method,
    header: request.header,
    data: request.data as WechatMiniprogram.IAnyObject | undefined,
    timeout: REQUEST_TIMEOUT_MS,
    success: (response) => resolve({ statusCode: response.statusCode, data: response.data }),
    fail: reject,
  });
});

// An assistant answer streams for as long as its Run lasts; the Gateway lifts its own write timeout for it.
const STREAM_TIMEOUT_MS = 600_000;

const chunked: ChunkedTransport = (request) => {
  let task: WechatMiniprogram.RequestTask | undefined;
  const done = new Promise<void>((resolve, reject) => {
    task = wx.request({
      url: request.url,
      method: 'GET',
      header: request.header,
      enableChunked: true,
      responseType: 'arraybuffer',
      timeout: STREAM_TIMEOUT_MS,
      success: () => resolve(),
      fail: reject,
    });
    task.onHeadersReceived((response) => request.onHeaders(response.statusCode));
    task.onChunkReceived((response) => request.onChunk(response.data));
  });
  return { done, abort: () => task?.abort() };
};

let signingOut = false;

export const gateway = createGateway({
  origin: GATEWAY_ORIGIN,
  transport,
  chunked,
  session,
  onSignedOut: () => {
    // Several requests can fail together when a Session ends; go to the sign-in page once.
    if (signingOut) return;
    signingOut = true;
    wx.reLaunch({ url: '/pages/login/index', complete: () => { signingOut = false; } });
  },
});

export const signOutLocally = () => session.clear();

/** The Session in use, so what was read under it is not reused under another. */
export const sessionToken = () => session.get();

/** A tab page shows nothing without a Session: it sends the person to the sign-in page instead. */
export function requireSession(): boolean {
  if (session.get() !== null) return true;
  wx.reLaunch({ url: '/pages/login/index' });
  return false;
}

/** WeChat's subscription dialog; it opens only when called straight from a tap, with nothing awaited first. */
export const askWeChat: AskWeChat = (templates) => new Promise((resolve, reject) => {
  wx.requestSubscribeMessage({ tmplIds: templates, success: (result) => resolve(result as unknown as Record<string, string>), fail: reject });
});
