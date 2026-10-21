// The assistant's answer as rich-text nodes. The Agent writes a small Markdown: the bold conclusion
// on its own line, short paragraphs, sometimes a list or a table of up to five columns
// (services/operations-agent-service system policy). This reads exactly that. The nodes are built
// as data, never as an HTML string, so nothing in an answer can become markup.

import { withBoldLeadOnItsOwnLine } from '../shared/answer-markdown';

export type AnswerNode =
  | { readonly type: 'text'; readonly text: string }
  | { readonly name: string; readonly attrs?: { readonly class: string }; readonly children: readonly AnswerNode[] };

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
const isDivider = (line: string) => /^\s*\|?(\s*:?-{3,}:?\s*\|)+\s*(:?-{3,}:?\s*)?$/.test(line);

export function answerNodes(answer: string): AnswerNode[] {
  const lines = withBoldLeadOnItsOwnLine(answer).split('\n');
  const blocks: AnswerNode[] = [];
  let index = 0;
  while (index < lines.length) {
    const line = lines[index];
    if (line.trim() === '') {
      index += 1;
    } else if (isTableRow(line) && index + 1 < lines.length && isDivider(lines[index + 1])) {
      const rows = [{ cell: 'th', cells: tableCells(line) }];
      index += 2;
      while (index < lines.length && isTableRow(lines[index])) {
        rows.push({ cell: 'td', cells: tableCells(lines[index]) });
        index += 1;
      }
      blocks.push({
        name: 'table',
        attrs: { class: 'answer-table' },
        children: rows.map((row) => ({ name: 'tr', children: row.cells.map((cell) => ({ name: row.cell, children: inline(cell) })) })),
      });
    } else if (BULLET.test(line) || NUMBERED.test(line)) {
      const marker = BULLET.test(line) ? BULLET : NUMBERED;
      const items: AnswerNode[] = [];
      while (index < lines.length && marker.test(lines[index])) {
        items.push({ name: 'li', children: inline(lines[index].replace(marker, '')) });
        index += 1;
      }
      blocks.push({ name: marker === BULLET ? 'ul' : 'ol', attrs: { class: 'answer-list' }, children: items });
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
        attrs: { class: 'answer-p' },
        children: HEADING.test(joined) ? [{ name: 'strong', children: inline(joined.replace(HEADING, '')) }] : inline(joined),
      });
    }
  }
  return blocks;
}
