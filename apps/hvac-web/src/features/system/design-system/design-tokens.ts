// The product's colour tokens as the design system names them, each with the token it is read
// against, so the page can measure the contrast the theme actually ships (WCAG 2.2, 1.4.3/1.4.11).

export interface ColourToken {
  readonly name: string;
  readonly role: string;
  /** The token this one is read against; text on a surface, or a surface behind a mark. */
  readonly against: string;
  /** Marks and large text only need 3:1; body text needs 4.5:1. */
  readonly large?: boolean;
  /** Dividers that identify nothing on their own carry no contrast requirement (WCAG 1.4.11). */
  readonly decorative?: boolean;
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
      { name: 'foreground', role: '正文', against: 'background' },
      { name: 'muted-foreground', role: '辅助文字', against: 'background' },
      { name: 'card-foreground', role: '卡片文字', against: 'card' },
      { name: 'sidebar-foreground', role: '侧栏文字', against: 'sidebar' },
      { name: 'secondary-foreground', role: '次要按钮文字', against: 'secondary' },
      { name: 'border', role: '分隔线', against: 'background', decorative: true },
    ],
  },
  {
    title: '品牌与操作',
    description: '主操作、焦点环与危险操作',
    tokens: [
      { name: 'primary', role: '主按钮、选中项', against: 'background', large: true },
      { name: 'primary-foreground', role: '主按钮文字', against: 'primary' },
      { name: 'ring', role: '键盘焦点环', against: 'background', large: true },
      { name: 'destructive', role: '危险操作、错误文字', against: 'background' },
    ],
  },
  {
    title: '状态',
    description: '状态标签用 10% 底色加同色文字，文字对比按页面底色衡量',
    tokens: [
      { name: 'success', role: '正常、已执行', against: 'background' },
      { name: 'warning', role: '需关注', against: 'background' },
      { name: 'information', role: '进行中、提示', against: 'background' },
      { name: 'unknown', role: '未知、离线', against: 'background' },
    ],
  },
  {
    title: '告警级别',
    description: '告警等级的标记色，用作圆点与色条，按图形标准 3:1 衡量',
    tokens: [
      { name: 'severity-critical', role: '紧急', against: 'background', large: true },
      { name: 'severity-major', role: '重要', against: 'background', large: true },
      { name: 'severity-minor', role: '次要', against: 'background', large: true },
      { name: 'severity-warning', role: '提示', against: 'background', large: true },
      { name: 'severity-info', role: '信息', against: 'background', large: true },
    ],
  },
  {
    title: '图表',
    description: '数据系列的顺序色，相邻系列色相区分明显；图形按 3:1 衡量',
    tokens: [
      { name: 'chart-1', role: '系列 1（用电）', against: 'card', large: true },
      { name: 'chart-2', role: '系列 2', against: 'card', large: true },
      { name: 'chart-3', role: '系列 3（能效）', against: 'card', large: true },
      { name: 'chart-4', role: '系列 4', against: 'card', large: true },
      { name: 'chart-5', role: '系列 5', against: 'card', large: true },
    ],
  },
];

function channel(value: number): number {
  const scaled = value / 255;
  return scaled <= 0.04045 ? scaled / 12.92 : ((scaled + 0.055) / 1.055) ** 2.4;
}

/** Relative luminance of an sRGB colour written #rgb or #rrggbb. */
export function luminance(hex: string): number {
  const digits = hex.replace('#', '').trim();
  const full = digits.length === 3 ? digits.split('').map((digit) => digit + digit).join('') : digits;
  const [red, green, blue] = [0, 2, 4].map((offset) => Number.parseInt(full.slice(offset, offset + 2), 16));
  return 0.2126 * channel(red) + 0.7152 * channel(green) + 0.0722 * channel(blue);
}

/** WCAG contrast ratio between two colours, from 1 to 21. */
export function contrastRatio(foreground: string, background: string): number {
  const [lighter, darker] = [luminance(foreground), luminance(background)].sort((left, right) => right - left);
  return (lighter + 0.05) / (darker + 0.05);
}

export type ContrastGrade = 'AAA' | 'AA' | '不足';

/** The level a pairing meets: text needs 4.5 (AA) or 7 (AAA); marks and large text need 3 or 4.5. */
export function contrastGrade(ratio: number, large = false): ContrastGrade {
  const [aa, aaa] = large ? [3, 4.5] : [4.5, 7];
  return ratio >= aaa ? 'AAA' : ratio >= aa ? 'AA' : '不足';
}
