// Tab pages check the Session themselves (lib/platform requireSession), so the app needs no
// launch logic of its own.

// The assistant's thread model, shared with the web, reads the last item with Array.prototype.at,
// which older iOS WeChat clients lack.
if (!Array.prototype.at) {
  Object.defineProperty(Array.prototype, 'at', {
    configurable: true,
    writable: true,
    value: function at<T>(this: T[], index: number): T | undefined {
      const position = Math.trunc(index) || 0;
      return this[position < 0 ? this.length + position : position];
    },
  });
}

App({});
