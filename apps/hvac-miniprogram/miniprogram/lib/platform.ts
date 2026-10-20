// The Gateway bound to WeChat: wx.request for the exchange and local storage for the Session.
// The platform's domain must be on the mini program's request allowlist in 微信公众平台.

import { createGateway, type SessionStore, type Transport } from './gateway';

export const GATEWAY_ORIGIN = 'https://hvac.swayingwindmill.online';
const SESSION_KEY = 'hvac.session';
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

let signingOut = false;

export const gateway = createGateway({
  origin: GATEWAY_ORIGIN,
  transport,
  session,
  onSignedOut: () => {
    // Several requests can fail together when a Session ends; go to the sign-in page once.
    if (signingOut) return;
    signingOut = true;
    wx.reLaunch({ url: '/pages/login/index', complete: () => { signingOut = false; } });
  },
});

export const hasSession = () => session.get() !== null;
export const signOutLocally = () => session.clear();
