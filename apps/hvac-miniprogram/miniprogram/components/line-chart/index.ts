// A finding's hourly chart as lines (lib/assistant.ts presentChart): points across and up the plot
// from 0 to 1, a gap where the read had none. Drawn on a 2D canvas, sized to the device pixel ratio.

type Point = { readonly x: number; readonly y: number } | null;

// The design system's chart colours (chart-1 … chart-4).
const COLORS = ['#3f5bd9', '#0e9384', '#8b5cf6', '#d6409f'];
const GRID = '#e7e7ea';

// The page redraws while an answer streams, handing over equal lines each time; only new ones are drawn.
const drawn = new WeakMap<object, string>();

Component({
  options: { virtualHost: true },
  properties: {
    lines: { type: Array, value: [] as Point[][] },
  },
  observers: {
    lines() {
      this.draw();
    },
  },
  lifetimes: {
    ready() {
      this.draw();
    },
  },
  methods: {
    draw() {
      const lines = this.data.lines as Point[][];
      const key = JSON.stringify(lines);
      if (drawn.get(this) === key) return;
      drawn.set(this, key);
      this.createSelectorQuery().select('.canvas').fields({ node: true, size: true }).exec((result) => {
        const target = result[0] as { node?: WechatMiniprogram.Canvas; width: number; height: number } | undefined;
        if (target?.node === undefined) return;
        const canvas = target.node;
        const ratio = wx.getWindowInfo().pixelRatio;
        canvas.width = target.width * ratio;
        canvas.height = target.height * ratio;
        const context = canvas.getContext('2d');
        context.scale(ratio, ratio);
        const inset = 4;
        const width = target.width;
        const height = target.height - inset * 2;
        context.clearRect(0, 0, width, target.height);
        context.strokeStyle = GRID;
        context.lineWidth = 1;
        for (const level of [0, 0.5, 1]) {
          const y = inset + height * (1 - level);
          context.beginPath();
          context.moveTo(0, y);
          context.lineTo(width, y);
          context.stroke();
        }
        lines.forEach((points, index) => {
          context.strokeStyle = COLORS[index % COLORS.length];
          context.lineWidth = 2;
          context.lineJoin = 'round';
          context.beginPath();
          let drawing = false;
          for (const point of points) {
            if (point === null) {
              drawing = false;
              continue;
            }
            const x = point.x * width;
            const y = inset + height * (1 - point.y);
            if (drawing) context.lineTo(x, y);
            else context.moveTo(x, y);
            drawing = true;
          }
          context.stroke();
        });
      });
    },
  },
});
