// The assistant's answer as blocks the page draws: text as rich-text nodes, and each table as a table
// of its own, which the page lays out in a row that scrolls sideways (rich-text squeezes a table into
// the phone's width, breaking short words a character a line). The Agent writes a small Markdown: the
// bold conclusion on its own line, short paragraphs, sometimes a list or a table of up to five columns
// (services/operations-agent-service system policy). This reads exactly that. Nodes are built as
// data, never as an HTML string, so nothing in an answer can become markup.

import { withBoldLeadOnItsOwnLine } from '../shared/answer-markdown';

export type AnswerNode =
  | { readonly type: 'text'; readonly text: string }
  | { readonly name: string; readonly attrs?: { readonly style: string }; readonly children: readonly AnswerNode[] };

// Inline styles: page styles reach a rich-text node only through a class on that very node.
export const ANSWER_STYLES = {
  paragraph: 'margin: 0 0 8px;',
  list: 'margin: 0 0 8px; padding-left: 20px;',
} as const;

export interface AnswerCell {
  readonly nodes: readonly AnswerNode[];
  /** right for a column of figures, so they line up; left for words. */
  readonly align: 'left' | 'right';
  /** A long description wraps within a set width instead of stretching the table. */
  readonly wraps: boolean;
}

export type AnswerBlock =
  | { readonly kind: 'text'; readonly nodes: readonly AnswerNode[] }
  | { readonly kind: 'table'; readonly head: readonly AnswerCell[]; readonly rows: readonly (readonly AnswerCell[])[] };

const text = (value: string): AnswerNode => ({ type: 'text', text: value });

/** **bold** and `code` inside a line; code reads as plain text. */
function inline(line: string): AnswerNode[] {
  const nodes: AnswerNode[] = [];
  const pattern = /\*\*([^*]+)\*\*/g;
  let last = 0;
  for (let match = pattern.exec(line); match !== null; match = pattern.exec(line)) {
    if (match.index > last) nodes.push(text(line.slice(last, match.index)));
    nodes.push({ name: 'strong', children: [text(match[1])] });
    last = match.index + match[0].length;
  }
  if (last < line.length) nodes.push(text(line.slice(last)));
  return nodes.map((node) => ('type' in node ? text(node.text.replace(/`([^`]*)`/g, '$1')) : node));
}

// Cells after the first longer than this many characters wrap.
const LONG_CELL = 12;
/** A figure: a number with at most a short unit (1,372, 403 kWh, 230.09 元), not a date such as 10 月 20 日. */
const isFigure = (cell: string) => /^[-+]?[\d,.]+\s*[^\s\d]{0,4}$/.test(cell);

/**
 * The first column names each row: it reads left and never wraps. A later column reads right only
 * when every cell under its header is a figure, and its long descriptions wrap.
 */
function table(head: readonly string[], body: readonly (readonly string[])[]): AnswerBlock {
  const figures = head.map((_, column) => column > 0 && body.length > 0 && body.every((row) => isFigure(row[column] ?? '')));
  const cell = (value: string, column: number): AnswerCell => ({
    nodes: inline(value),
    align: figures[column] ? 'right' : 'left',
    wraps: column > 0 && value.length > LONG_CELL,
  });
  return { kind: 'table', head: head.map(cell), rows: body.map((row) => row.map(cell)) };
}

const BULLET = /^\s*[-*]\s+/;
const NUMBERED = /^\s*\d+[.)]\s+/;
const HEADING = /^#{1,6}\s+/;
const tableCells = (line: string) => line.trim().replace(/^\|/, '').replace(/\|$/, '').split('|').map((cell) => cell.trim());
const isTableRow = (line: string) => line.trim().startsWith('|');
// A GitHub table's divider: cells of dashes, one or more, with optional colons.
const isDivider = (line: string) => /^\s*\|?(\s*:?-+:?\s*\|)+\s*(:?-+:?\s*)?$/.test(line);

/**
 * Every branch reads at least one line, so any text finishes. A run of lines starting with `|` is a
 * table, its first line the header, whether or not the Agent wrote the divider under it.
 */
export function answerBlocks(answer: string): AnswerBlock[] {
  const lines = withBoldLeadOnItsOwnLine(answer).split('\n');
  const blocks: AnswerBlock[] = [];
  // Text between tables gathers into one rich-text block.
  let words: AnswerNode[] = [];
  const endText = () => {
    if (words.length > 0) blocks.push({ kind: 'text', nodes: words });
    words = [];
  };
  let index = 0;
  while (index < lines.length) {
    const line = lines[index];
    if (line.trim() === '') {
      index += 1;
    } else if (isTableRow(line)) {
      const head = tableCells(line);
      const body: string[][] = [];
      index += 1;
      while (index < lines.length && isTableRow(lines[index])) {
        if (!isDivider(lines[index])) body.push(tableCells(lines[index]));
        index += 1;
      }
      endText();
      blocks.push(table(head, body));
    } else if (BULLET.test(line) || NUMBERED.test(line)) {
      const marker = BULLET.test(line) ? BULLET : NUMBERED;
      const items: AnswerNode[] = [];
      while (index < lines.length && marker.test(lines[index])) {
        items.push({ name: 'li', children: inline(lines[index].replace(marker, '')) });
        index += 1;
      }
      words.push({ name: marker === BULLET ? 'ul' : 'ol', attrs: { style: ANSWER_STYLES.list }, children: items });
    } else {
      // A paragraph runs until a blank line, a list or a table; a heading reads as a bold paragraph.
      const paragraph: string[] = [];
      while (index < lines.length && lines[index].trim() !== '' && !BULLET.test(lines[index]) && !NUMBERED.test(lines[index]) && !isTableRow(lines[index])) {
        paragraph.push(lines[index].trim());
        index += 1;
      }
      const joined = paragraph.join(' ');
      words.push({
        name: 'p',
        attrs: { style: ANSWER_STYLES.paragraph },
        children: HEADING.test(joined) ? [{ name: 'strong', children: inline(joined.replace(HEADING, '')) }] : inline(joined),
      });
    }
  }
  endText();
  return blocks;
}
