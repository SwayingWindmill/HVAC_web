// 由 apps/hvac-miniprogram/scripts/sync-shared.ts 从 apps/hvac-web/src/features/assistant/answer-markdown.ts 复制，不要在这里改。
// An answer opens with its conclusion in bold on a line of its own (#663). CommonMark does not
// close 「**…。**」 when a character follows it directly, so the operator would see the stars:
// the panel moves that text to the next paragraph.
const BOLD_LEAD_FOLLOWED_ON_ITS_LINE = /^(\s*\*\*[^*\n]+\*\*)[ \t]*(?=[^\s])/u;

export const withBoldLeadOnItsOwnLine = (text: string): string => text.replace(BOLD_LEAD_FOLLOWED_ON_ITS_LINE, '$1\n\n');
