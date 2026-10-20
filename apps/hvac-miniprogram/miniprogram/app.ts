import { hasSession } from './lib/platform';

App({
  onLaunch() {
    // Signed-out people start at the sign-in page; tab pages never load without a Session.
    if (!hasSession()) wx.reLaunch({ url: '/pages/login/index' });
  },
});
