import { formatTelemetryUnit } from '../../domain/centralPlantTelemetry.ts';
import { normalizeAssistantMarkdown } from '../../lib/answer-markdown.ts';

export interface KeyFigureItem {
  readonly label: string;
  readonly value: string;
  readonly unit?: string;
}

export interface FindingChartSummary {
  readonly unit: string;
  readonly series: readonly { readonly values: readonly (number | null)[] }[];
}

export interface DeduplicatableFinding {
  readonly keyFigures?: readonly KeyFigureItem[];
  readonly chart?: FindingChartSummary;
}

export interface DeduplicatedFindingView {
  readonly visibleKeyFigures: readonly KeyFigureItem[];
  readonly suppressedCount: number;
  readonly compact: boolean;
}

const FENCE_OPEN = /^\s{0,3}(`{3,}|~{3,})(.*)$/u;
const FENCE_CLOSE = /^\s{0,3}(`{3,}|~{3,})\s*$/u;
const GFM_DELIMITER_ROW = /^\s*\|?(?:\s*:?-+:?\s*\|)+\s*(?::?-+:?\s*)?$/u;
const ISO_DATE_PATTERN = /(?:19|20)\d{2}[-/]\d{1,2}[-/]\d{1,2}/g;
const CN_DATE_PATTERN = /(?:\d{4}\s*年\s*)?\d{1,2}\s*月\s*\d{1,2}\s*日/g;
const SHORT_DATE_PATTERN = /(?<!\d)\d{1,2}\/\d{1,2}(?!\d)/g;
const CLOCK_TIME_PATTERN = /(?<!\d)\d{1,2}:\d{2}(?::\d{2})?(?!\d)/g;

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

function splitRowCells(trimmedLine: string): string[] {
  if (!trimmedLine.includes('|')) return [];
  const hasLeadingPipe = trimmedLine.startsWith('|');
  const hasTrailingPipe = trimmedLine.length > 1 && isUnescapedPipe(trimmedLine, trimmedLine.length - 1);
  const startOffset = hasLeadingPipe ? 1 : 0;
  const endOffset = hasTrailingPipe ? trimmedLine.length - 1 : trimmedLine.length;
  const inner = trimmedLine.slice(startOffset, endOffset);
  const cells: string[] = [];
  let start = 0;
  let pipeCount = 0;
  for (let index = 0; index < inner.length; index += 1) {
    if (isUnescapedPipe(inner, index)) {
      pipeCount += 1;
      cells.push(inner.slice(start, index));
      start = index + 1;
    }
  }
  if (!hasLeadingPipe && pipeCount === 0) return [];
  cells.push(inner.slice(start));
  return cells;
}

interface ExtractedTableEvidence {
  readonly hasTable: boolean;
  readonly numbers: readonly number[];
  readonly cells: ReadonlySet<string>;
}

function normalizeCellText(value: string): string {
  return value.replace(/\s+/g, '').toLowerCase();
}

function extractTableEvidence(markdown: string): ExtractedTableEvidence {
  const normalized = normalizeAssistantMarkdown(markdown);
  const lines = normalized.split(/\r?\n/);
  const bodyCells: string[] = [];
  let fence: CodeFence | null = null;
  let hasTable = false;

  for (let index = 0; index < lines.length; index += 1) {
    const line = lines[index]!;
    const nextFence = updateCodeFence(line, fence);
    if (fence !== null || nextFence !== null) {
      fence = nextFence;
      continue;
    }
    const headerCells = splitRowCells(line.trim());
    const delimiterLine = lines[index + 1];
    if (headerCells.length < 2 || delimiterLine === undefined || !GFM_DELIMITER_ROW.test(delimiterLine)) {
      continue;
    }

    let bodyCursor = index + 2;
    let bodyRowCount = 0;
    while (bodyCursor < lines.length) {
      const candidate = lines[bodyCursor]!.trim();
      const rowCells = splitRowCells(candidate);
      if (rowCells.length < 2) break;
      bodyCells.push(...rowCells);
      bodyRowCount += 1;
      bodyCursor += 1;
    }
    if (bodyRowCount > 0) {
      hasTable = true;
      index = bodyCursor - 1;
    }
  }

  const numbers: number[] = [];
  const cells = new Set<string>();
  for (const rawCell of bodyCells) {
    const cleanText = normalizeCellText(rawCell);
    if (cleanText.length > 0) cells.add(cleanText);
    const withoutDatesAndTimes = rawCell
      .replace(ISO_DATE_PATTERN, ' ')
      .replace(CN_DATE_PATTERN, ' ')
      .replace(SHORT_DATE_PATTERN, ' ')
      .replace(CLOCK_TIME_PATTERN, ' ');
    for (const match of withoutDatesAndTimes.matchAll(/[+-]?\d[\d,]*(?:\.\d+)?/g)) {
      const parsed = Number(match[0].replace(/,/g, ''));
      if (Number.isFinite(parsed)) numbers.push(parsed);
    }
  }

  return { hasTable, numbers, cells };
}

function parseFigureNumber(value: string): { readonly numeric: number | null; readonly signedDelta: boolean } {
  const trimmed = value.trim();
  const signedDelta = trimmed.startsWith('+') || trimmed.startsWith('-');
  const compact = trimmed.replace(/,/g, '');
  if (!/^[+-]?\d+(?:\.\d+)?$/.test(compact)) return { numeric: null, signedDelta };
  const parsed = Number(compact);
  return { numeric: Number.isFinite(parsed) ? parsed : null, signedDelta };
}

function normalizeUnit(unit: string | undefined): string {
  if (!unit) return '';
  return (formatTelemetryUnit(unit) ?? unit).trim();
}

export function deduplicateFindingKeyFigures({
  answerText,
  finding,
}: {
  readonly answerText: string;
  readonly finding: DeduplicatableFinding;
}): DeduplicatedFindingView {
  const keyFigures = finding.keyFigures ?? [];
  if (keyFigures.length === 0) {
    return {
      visibleKeyFigures: [],
      suppressedCount: 0,
      compact: finding.chart === undefined,
    };
  }

  const tableEvidence = answerText.trim().length > 0
    ? extractTableEvidence(answerText)
    : { hasTable: false, numbers: [], cells: new Set<string>() };

  const chart = finding.chart;
  const chartUnit = chart === undefined ? '' : normalizeUnit(chart.unit);
  const chartValues = chart === undefined
    ? []
    : chart.series.flatMap((series) =>
      series.values.filter((value): value is number => typeof value === 'number' && Number.isFinite(value)));

  const visibleKeyFigures: KeyFigureItem[] = [];
  let suppressedCount = 0;

  for (const figure of keyFigures) {
    const { numeric, signedDelta } = parseFigureNumber(figure.value);
    const normalizedFigureText = normalizeCellText(figure.value);

    const repeatedInTable = tableEvidence.hasTable && (
      (numeric !== null && tableEvidence.numbers.some((candidate) => Math.abs(candidate - numeric) <= 1e-9))
      || (normalizedFigureText.length > 0 && tableEvidence.cells.has(normalizedFigureText))
    );

    const repeatedInChart = chart !== undefined
      && !signedDelta
      && numeric !== null
      && normalizeUnit(figure.unit) === chartUnit
      && chartValues.some((candidate) => Math.abs(candidate - numeric) <= 1e-9);

    if (repeatedInTable || repeatedInChart) {
      suppressedCount += 1;
    } else {
      visibleKeyFigures.push(figure);
    }
  }

  return {
    visibleKeyFigures,
    suppressedCount,
    compact: chart === undefined && visibleKeyFigures.length === 0,
  };
}
