// The product's colour tokens as the design system names them, each with the token it is read
// against, so the page can measure the contrast the theme actually ships (WCAG 2.2, 1.4.3/1.4.11).

/**
 * What WCAG asks of a colour: text 4.5:1 (7:1 for AAA, 1.4.3/1.4.6); a mark that conveys meaning
 * 3:1 against what is next to it (1.4.11, one level only); a divider nothing (1.4.11 exempts it).
 */
export type ContrastUse = 'text' | 'graphic' | 'decorative';

export interface ColourToken {
  readonly name: string;
  readonly role: string;
  /** The token this one is read against; text on a surface, or a surface behind a mark. */
  readonly against: string;
  readonly use: ContrastUse;
  /** Text set on a tint of itself, as status labels are: the share of the token mixed into `against`. */
  readonly tint?: number;
}

export interface TokenGroup {
  readonly title: string;
  readonly description: string;
  readonly tokens: readonly ColourToken[];
}

export const TOKEN_GROUPS: readonly TokenGroup[] = [
  {
    title: '文字与界面',
    description: '页面、卡片和侧栏的底色与文字；正文对比度不低于 4.5:1',
    tokens: [
      { name: 'foreground', role: '正文', against: 'background', use: 'text' },
      { name: 'muted-foreground', role: '辅助文字', against: 'background', use: 'text' },
      { name: 'card-foreground', role: '卡片文字', against: 'card', use: 'text' },
      { name: 'sidebar-foreground', role: '侧栏文字', against: 'sidebar', use: 'text' },
      { name: 'secondary-foreground', role: '次要按钮文字', against: 'secondary', use: 'text' },
      { name: 'border', role: '分隔线', against: 'background', use: 'decorative' },
    ],
  },
  {
    title: '品牌与操作',
    description: '主操作、焦点环与危险操作',
    tokens: [
      { name: 'primary', role: '主按钮、选中项', against: 'background', use: 'graphic' },
      { name: 'primary-foreground', role: '主按钮文字', against: 'primary', use: 'text' },
      { name: 'ring', role: '键盘焦点环', against: 'background', use: 'graphic' },
      { name: 'destructive', role: '危险操作、错误文字', against: 'background', use: 'text' },
    ],
  },
  {
    title: '状态',
    description: '状态标签是同色文字压在 10% 同色底上，对比度按这层底色衡量',
    tokens: [
      { name: 'success', role: '正常、已执行', against: 'background', use: 'text', tint: 0.1 },
      { name: 'warning', role: '需关注', against: 'background', use: 'text', tint: 0.1 },
      { name: 'information', role: '进行中、提示', against: 'background', use: 'text', tint: 0.1 },
      { name: 'muted-foreground', role: '未知、计划中（灰底）', against: 'muted', use: 'text' },
    ],
  },
  {
    title: '告警级别',
    description: '告警等级的标记色，用作圆点与色条，按图形标准 3:1 衡量',
    tokens: [
      { name: 'severity-critical', role: '紧急', against: 'background', use: 'graphic' },
      { name: 'severity-major', role: '重要', against: 'background', use: 'graphic' },
      { name: 'severity-minor', role: '次要', against: 'background', use: 'graphic' },
      { name: 'severity-warning', role: '提示', against: 'background', use: 'graphic' },
      { name: 'severity-info', role: '信息', against: 'background', use: 'graphic' },
    ],
  },
  {
    title: '图表',
    description: '数据系列的顺序色，相邻系列色相区分明显；图形按 3:1 衡量',
    tokens: [
      { name: 'chart-1', role: '系列 1（用电）', against: 'card', use: 'graphic' },
      { name: 'chart-2', role: '系列 2', against: 'card', use: 'graphic' },
      { name: 'chart-3', role: '系列 3（能效）', against: 'card', use: 'graphic' },
      { name: 'chart-4', role: '系列 4', against: 'card', use: 'graphic' },
      { name: 'chart-5', role: '系列 5', against: 'card', use: 'graphic' },
    ],
  },
];

function channel(value: number): number {
  const scaled = value / 255;
  return scaled <= 0.04045 ? scaled / 12.92 : ((scaled + 0.055) / 1.055) ** 2.4;
}

function rgb(hex: string): [number, number, number] {
  const digits = hex.replace('#', '').trim();
  const full = digits.length === 3 ? digits.split('').map((digit) => digit + digit).join('') : digits;
  const [red, green, blue] = [0, 2, 4].map((offset) => Number.parseInt(full.slice(offset, offset + 2), 16));
  return [red, green, blue];
}

/** Relative luminance of an sRGB colour written #rgb or #rrggbb. */
export function luminance(hex: string): number {
  const [red, green, blue] = rgb(hex);
  return 0.2126 * channel(red) + 0.7152 * channel(green) + 0.0722 * channel(blue);
}

/** `top` laid over `base` at `share` opacity, as `bg-x/10` paints it. */
export function tinted(top: string, base: string, share: number): string {
  const [over, under] = [rgb(top), rgb(base)];
  return `#${under.map((value, index) => Math.round(over[index] * share + value * (1 - share)).toString(16).padStart(2, '0')).join('')}`;
}

/** WCAG contrast ratio between two colours, from 1 to 21. */
export function contrastRatio(foreground: string, background: string): number {
  const [lighter, darker] = [luminance(foreground), luminance(background)].sort((left, right) => right - left);
  return (lighter + 0.05) / (darker + 0.05);
}

export type ContrastGrade = 'AAA' | 'AA' | '达标' | '不足' | '不要求';

/** The level a pairing meets for its use. */
export function contrastGrade(ratio: number, use: ContrastUse): ContrastGrade {
  switch (use) {
    case 'text':
      return ratio >= 7 ? 'AAA' : ratio >= 4.5 ? 'AA' : '不足';
    case 'graphic':
      return ratio >= 3 ? '达标' : '不足';
    case 'decorative':
      return '不要求';
  }
}

/** The surface a token is actually read on: its own tint for status labels, else `against`. */
export function surfaceOf(token: Pick<ColourToken, 'tint'>, value: string, against: string): string {
  return token.tint === undefined ? against : tinted(value, against, token.tint);
}
