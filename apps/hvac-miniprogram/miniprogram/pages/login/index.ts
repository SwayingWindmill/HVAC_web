import { gateway } from '../../lib/platform';
import type { SignInOutcome } from '../../lib/gateway';

Page({
  data: {
    step: 'password' as 'password' | 'totp',
    username: '',
    password: '',
    code: '',
    pendingLogin: '',
    error: '',
    busy: false,
  },

  onUsername(event: WechatMiniprogram.Input) { this.setData({ username: event.detail.value, error: '' }); },
  onPassword(event: WechatMiniprogram.Input) { this.setData({ password: event.detail.value, error: '' }); },
  onCode(event: WechatMiniprogram.Input) { this.setData({ code: event.detail.value, error: '' }); },

  async signIn() {
    const { username, password, busy } = this.data;
    if (busy) return;
    if (username.trim() === '' || password === '') {
      this.setData({ error: '请填写账号和密码。' });
      return;
    }
    this.setData({ busy: true, error: '' });
    this.finish(await gateway.signIn(username.trim(), password));
  },

  async completeTotp() {
    const { code, pendingLogin, busy } = this.data;
    if (busy) return;
    if (!/^\d{6}$/.test(code)) {
      this.setData({ error: '验证码是 6 位数字。' });
      return;
    }
    this.setData({ busy: true, error: '' });
    this.finish(await gateway.completeTotp(pendingLogin, code));
  },

  startOver() {
    this.setData({ step: 'password', code: '', pendingLogin: '', error: '' });
  },

  finish(outcome: SignInOutcome) {
    if (outcome.kind === 'signed-in') {
      this.setData({ busy: false, password: '', code: '' });
      wx.switchTab({ url: '/pages/overview/index' });
      return;
    }
    if (outcome.kind === 'totp') {
      this.setData({ busy: false, step: 'totp', pendingLogin: outcome.pendingLogin, password: '' });
      return;
    }
    this.setData({ busy: false, error: outcome.message });
  },
});
