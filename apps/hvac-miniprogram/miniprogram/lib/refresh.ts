// A page re-reads its data on a timer only while it is on screen, as WeChat's performance guide asks:
// start in onShow, stop in onHide and onUnload.
export function whileShown(intervalMs: number) {
  let timer: number | undefined;
  const stop = () => {
    if (timer !== undefined) clearInterval(timer);
    timer = undefined;
  };
  return {
    start(tick: () => void) {
      stop();
      timer = setInterval(tick, intervalMs);
    },
    stop,
  };
}
