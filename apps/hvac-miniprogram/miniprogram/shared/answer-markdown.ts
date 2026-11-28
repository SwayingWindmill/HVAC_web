// 由 apps/hvac-miniprogram/scripts/sync-shared.ts 从 apps/hvac-web/src/lib/answer-markdown.ts 复制，不要在这里改。
// An answer opens with its conclusion in bold on a line of its own. CommonMark does not
// close 「**…。**」 when a character follows it directly, so the operator would see the stars:
// the panel moves that text to the next paragraph.
const BOLD_LEAD_FOLLOWED_ON_ITS_LINE = /^(\s*\*\*[^*\n]+\*\*)[ \t]*(?=[^\s])/u;

const FENCE_OPEN = /^\s{0,3}(`{3,}|~{3,})(.*)$/u;
const FENCE_CLOSE = /^\s{0,3}(`{3,}|~{3,})\s*$/u;
const GFM_DELIMITER_ROW = /^\s*\|?(?:\s*:?-+:?\s*\|)+\s*(?::?-+:?\s*)?$/u;
const STREAMING_DELIMITER_ROW = /^\|[\s|:-]*$/u;

interface CodeFence {
  readonly marker: '`' | '~';
  readonly length: number;
}

function updateCodeFence(line: string, current: CodeFence | null): CodeFence | null {
  if (current !== null) {
    const close = FENCE_CLOSE.exec(line);
    if (!close) return current;
    const token = close[1]!;
    return token[0] === current.marker && token.length >= current.length ? null : current;
  }
  const open = FENCE_OPEN.exec(line);
  if (!open) return null;
  const token = open[1]!;
  const info = open[2] ?? '';
  if (token[0] === '`' && info.includes('`')) return null;
  return { marker: token[0] === '~' ? '~' : '`', length: token.length };
}

function isUnescapedPipe(line: string, index: number): boolean {
  if (line[index] !== '|') return false;
  let backslashes = 0;
  for (let cursor = index - 1; cursor >= 0 && line[cursor] === '\\'; cursor -= 1) {
    backslashes += 1;
  }
  return backslashes % 2 === 0;
}

function splitPipeCells(trimmedLine: string): string[] {
  if (!trimmedLine.startsWith('|')) return [];
  const hasTrailingPipe = trimmedLine.length > 1 && isUnescapedPipe(trimmedLine, trimmedLine.length - 1);
  const inner = trimmedLine.slice(1, hasTrailingPipe ? -1 : undefined);
  const cells: string[] = [];
  let start = 0;
  for (let index = 0; index < inner.length; index += 1) {
    if (isUnescapedPipe(inner, index)) {
      cells.push(inner.slice(start, index));
      start = index + 1;
    }
  }
  cells.push(inner.slice(start));
  return cells;
}

function ensureBlankLineBeforeTable(output: string[]): void {
  const previous = output.at(-1);
  if (previous !== undefined && previous.trim() !== '' && !previous.trim().startsWith('|')) {
    output.push('');
  }
}

function pushContiguousPipeRows(lines: readonly string[], startIndex: number, output: string[]): number {
  let index = startIndex;
  while (index < lines.length && lines[index]!.trim().startsWith('|')) {
    output.push(lines[index]!);
    index += 1;
  }
  return index;
}

export const withBoldLeadOnItsOwnLine = (text: string): string => text.replace(BOLD_LEAD_FOLLOWED_ON_ITS_LINE, '$1\n\n');

// Models sometimes emit GFM pipe tables without the header divider row, causing remark-gfm to
// render raw pipe lines as plain text; synthesize the missing divider outside fenced code blocks.
export function withMissingTableDividers(text: string): string {
  const lines = text.split(/\r?\n/);
  const output: string[] = [];
  let fence: CodeFence | null = null;
  let index = 0;

  while (index < lines.length) {
    const line = lines[index]!;
    const nextFence = updateCodeFence(line, fence);
    if (fence !== null || nextFence !== null) {
      output.push(line);
      fence = nextFence;
      index += 1;
      continue;
    }

    const trimmed = line.trim();
    const headerCells = splitPipeCells(trimmed);
    if (headerCells.length < 2 || GFM_DELIMITER_ROW.test(line)) {
      output.push(line);
      index += 1;
      continue;
    }

    const nextLine = lines[index + 1];
    if (nextLine === undefined) {
      output.push(line);
      index += 1;
      continue;
    }

    const trimmedNext = nextLine.trim();
    const isTrailingLine = index + 1 === lines.length - 1 && !text.endsWith('\n');
    if (GFM_DELIMITER_ROW.test(nextLine)) {
      ensureBlankLineBeforeTable(output);
      output.push(line, nextLine);
      index = pushContiguousPipeRows(lines, index + 2, output);
      continue;
    }

    // Hold off while the divider row itself is still streaming at the end of the answer.
    if (isTrailingLine && STREAMING_DELIMITER_ROW.test(trimmedNext)) {
      ensureBlankLineBeforeTable(output);
      output.push(line, nextLine);
      index += 2;
      continue;
    }

    if (trimmedNext.startsWith('|')) {
      ensureBlankLineBeforeTable(output);
      output.push(line, `| ${Array(headerCells.length).fill('---').join(' | ')} |`);
      index = pushContiguousPipeRows(lines, index + 1, output);
      continue;
    }

    output.push(line);
    index += 1;
  }

  return output.join('\n');
}

export const normalizeAssistantMarkdown = (text: string): string =>
  withMissingTableDividers(withBoldLeadOnItsOwnLine(text));
