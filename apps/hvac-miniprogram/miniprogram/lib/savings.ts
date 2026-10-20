// The overview's first card: what the Site saved this month, from the savings verification owner
// (mv-service), the same figures as the web overview's 节能 panel (#664).

const DAY_MS = 86_400_000;
const pad = (value: number) => String(value).padStart(2, '0');

/**
 * The month so far as Site-local dates, read from the start of today's Site day as the Energy
 * analytics owner cut it: the Site's wall clock shows 00:00 at that instant, which gives its offset
 * without the phone's time zone support.
 */
export function monthToDate(todayStart: string): { readonly from: string; readonly to: string } {
  const instant = Date.parse(todayStart);
  const intoUTCDay = ((instant % DAY_MS) + DAY_MS) % DAY_MS;
  // Offsets run from UTC−12 to UTC+14: a midnight later than 12:00Z belongs to the next UTC day.
  const offset = intoUTCDay > DAY_MS / 2 ? DAY_MS - intoUTCDay : -intoUTCDay;
  const local = new Date(instant + offset);
  const year = local.getUTCFullYear();
  const month = pad(local.getUTCMonth() + 1);
  return { from: `${year}-${month}-01`, to: `${year}-${month}-${pad(local.getUTCDate())}` };
}

export interface SavingsTotals {
  readonly days: number;
  readonly avoidedKWh: number;
  readonly avoidedCost: number;
  /** The plant's own electricity over the same days. */
  readonly plantKWh: number;
}

export interface Measure {
  readonly id: string;
  readonly name: string;
  readonly status: string;
  readonly baseline: { readonly trusted: boolean } | null;
  readonly totals: { readonly days: number; readonly avoidedKWh: number; readonly avoidedCost: number };
}

/** Why there is nothing saved to show yet, in the order an operator can act on it. */
export type NoSavingsReason = 'NO_MEASURE' | 'BASELINE_UNTRUSTED' | 'FIRST_DAY_PENDING';

export interface SavingsCard {
  /** Null until a counted measure has a verified day this month. */
  readonly saved: { readonly cost: number; readonly kWh: number; readonly rate: number | null } | null;
  readonly why: NoSavingsReason | null;
  readonly measures: readonly Measure[];
}

export function savingsCard(totals: SavingsTotals, measures: readonly Measure[]): SavingsCard {
  const counted = measures.filter((measure) => measure.status === 'IMPLEMENTED');
  if (totals.days > 0) {
    const without = totals.plantKWh + totals.avoidedKWh;
    return {
      saved: { cost: totals.avoidedCost, kWh: totals.avoidedKWh, rate: without > 0 ? Math.round((totals.avoidedKWh / without) * 1000) / 1000 : null },
      why: null,
      measures: counted,
    };
  }
  const why: NoSavingsReason = counted.length === 0 ? 'NO_MEASURE'
    : counted.some((measure) => measure.baseline?.trusted === false) ? 'BASELINE_UNTRUSTED'
      : 'FIRST_DAY_PENDING';
  return { saved: null, why, measures: counted };
}
