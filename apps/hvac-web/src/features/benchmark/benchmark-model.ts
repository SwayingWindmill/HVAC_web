import type { EnergySummary } from '../energy-analysis/consumption/model.ts';

// Sites compared on what every plant measures alike: HVAC electricity, cooling delivered and the
// plant COP between them. The fleet's COP weighs each site by its energy, not one vote per site.

export interface SiteSummary {
  readonly siteId: string;
  readonly name: string;
  readonly summary: EnergySummary;
}

export interface BenchmarkSite {
  readonly siteId: string;
  readonly name: string;
  /** 1 is the best COP; null without both energies. */
  readonly rank: number | null;
  readonly electricityKWh: number | null;
  readonly coolingKWh: number | null;
  readonly cop: number | null;
  /** Electricity per day with metering. */
  readonly dailyElectricityKWh: number | null;
  /** The site's COP against the fleet's, as a fraction; null without a COP. */
  readonly copVsFleet: number | null;
}

export type BenchmarkDay = { readonly key: string; readonly label: string } & Readonly<Record<string, number | string | null>>;

export interface Benchmark {
  readonly sites: readonly BenchmarkSite[];
  readonly fleet: { readonly electricityKWh: number | null; readonly coolingKWh: number | null; readonly cop: number | null };
  /** Each day's COP per site, keyed by site id, in time order. */
  readonly daily: readonly BenchmarkDay[];
}

export function benchmark(input: readonly SiteSummary[]): Benchmark {
  const measured = input.filter((site) => site.summary.cop !== null);
  const coolingOfMeasured = measured.reduce((sum, site) => sum + (site.summary.coolingKWh ?? 0), 0);
  const electricityOfMeasured = measured.reduce((sum, site) => sum + (site.summary.electricityKWh ?? 0), 0);
  const fleetCop = electricityOfMeasured > 0 ? coolingOfMeasured / electricityOfMeasured : null;
  const total = (key: 'electricityKWh' | 'coolingKWh') => {
    const values = input.map((site) => site.summary[key]).filter((value): value is number => value !== null);
    return values.length === 0 ? null : values.reduce((sum, value) => sum + value, 0);
  };

  const sites = input
    .map((site) => {
      const metered = site.summary.rows.filter((row) => row.electricityKWh !== null).length;
      return {
        siteId: site.siteId,
        name: site.name,
        electricityKWh: site.summary.electricityKWh,
        coolingKWh: site.summary.coolingKWh,
        cop: site.summary.cop,
        dailyElectricityKWh: site.summary.electricityKWh !== null && metered > 0 ? site.summary.electricityKWh / metered : null,
        copVsFleet: site.summary.cop !== null && fleetCop ? site.summary.cop / fleetCop - 1 : null,
      };
    })
    .sort((left, right) => (right.cop ?? -Infinity) - (left.cop ?? -Infinity))
    .map((site, index): BenchmarkSite => ({ ...site, rank: site.cop === null ? null : index + 1 }));

  const days = new Map<string, Record<string, number | string | null>>();
  for (const site of input) {
    for (const row of site.summary.rows) {
      const day = days.get(row.periodStart) ?? { key: row.periodStart, label: row.label, ...Object.fromEntries(input.map((entry) => [entry.siteId, null])) };
      day[site.siteId] = row.cop;
      days.set(row.periodStart, day);
    }
  }

  return {
    sites,
    fleet: { electricityKWh: total('electricityKWh'), coolingKWh: total('coolingKWh'), cop: fleetCop },
    daily: [...days.values()].sort((left, right) => String(left.key).localeCompare(String(right.key))) as BenchmarkDay[],
  };
}
