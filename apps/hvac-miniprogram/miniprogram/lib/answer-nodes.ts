// The assistant's answer as rich-text nodes. The Agent writes a small Markdown: the bold conclusion
// on its own line, short paragraphs, sometimes a list or a table of up to five columns
// (services/operations-agent-service system policy). This reads exactly that. The nodes are built
// as data, never as an HTML string, so nothing in an answer can become markup.

import { withBoldLeadOnItsOwnLine } from '../shared/answer-markdown';

export type AnswerNode =
  | { readonly type: 'text'; readonly text: string }
  | { readonly name: string; readonly attrs?: { readonly style: string }; readonly children: readonly AnswerNode[] };

// Inline styles: page styles reach a rich-text node only through a class on that very node, and a
// table's cells cannot carry one per selector. Colours are the design system's light tokens.
export const ANSWER_STYLES = {
  paragraph: 'margin: 0 0 8px;',
  list: 'margin: 0 0 8px; padding-left: 20px;',
  table: 'width: 100%; margin: 4px 0 12px; border-collapse: collapse; font-size: 13px; line-height: 20px;',
} as const;

const CELL = {
  th: 'padding: 6px 8px; background: #f7f7f9; color: #3f3f46; font-weight: 600; border-bottom: 1px solid #e7e7ea; white-space: nowrap;',
  td: 'padding: 7px 8px; border-bottom: 1px solid #f0f0f2;',
} as const;

/** A table cell's style: the first column (what is measured) reads left, the figures after it right. */
export const cellStyle = (cell: 'th' | 'td', column: number) => `${CELL[cell]} text-align: ${column === 0 ? 'left' : 'right'};`;

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
export function answerNodes(answer: string): AnswerNode[] {
  const lines = withBoldLeadOnItsOwnLine(answer).split('\n');
  const blocks: AnswerNode[] = [];
  let index = 0;
  while (index < lines.length) {
    const line = lines[index];
    if (line.trim() === '') {
      index += 1;
    } else if (isTableRow(line)) {
      const rows: { cell: 'th' | 'td'; cells: string[] }[] = [{ cell: 'th', cells: tableCells(line) }];
      index += 1;
      while (index < lines.length && isTableRow(lines[index])) {
        if (!isDivider(lines[index])) rows.push({ cell: 'td', cells: tableCells(lines[index]) });
        index += 1;
      }
      blocks.push({
        name: 'table',
        attrs: { style: ANSWER_STYLES.table },
        children: rows.map((row) => ({
          name: 'tr',
          children: row.cells.map((cell, column) => ({ name: row.cell, attrs: { style: cellStyle(row.cell, column) }, children: inline(cell) })),
        })),
      });
    } else if (BULLET.test(line) || NUMBERED.test(line)) {
      const marker = BULLET.test(line) ? BULLET : NUMBERED;
      const items: AnswerNode[] = [];
      while (index < lines.length && marker.test(lines[index])) {
        items.push({ name: 'li', children: inline(lines[index].replace(marker, '')) });
        index += 1;
      }
      blocks.push({ name: marker === BULLET ? 'ul' : 'ol', attrs: { style: ANSWER_STYLES.list }, children: items });
    } else {
      // A paragraph runs until a blank line, a list or a table; a heading reads as a bold paragraph.
      const paragraph: string[] = [];
      while (index < lines.length && lines[index].trim() !== '' && !BULLET.test(lines[index]) && !NUMBERED.test(lines[index]) && !isTableRow(lines[index])) {
        paragraph.push(lines[index].trim());
        index += 1;
      }
      const joined = paragraph.join(' ');
      blocks.push({
        name: 'p',
        attrs: { style: ANSWER_STYLES.paragraph },
        children: HEADING.test(joined) ? [{ name: 'strong', children: inline(joined.replace(HEADING, '')) }] : inline(joined),
      });
    }
  }
  return blocks;
}
