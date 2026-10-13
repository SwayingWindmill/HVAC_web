import type { ClimateZone } from '../../lib/climate-zone.ts';
import type { EnergySummary } from '../energy-analysis/consumption/model.ts';

// Sites compared on what every plant measures alike: HVAC electricity, cooling delivered and the
// plant COP between them. The fleet's COP weighs each site by its energy, not one vote per site.
// A site with a floor area in its Registry profile is also compared on electricity per square metre,
// and, with a weather station, per square metre per cooling degree day (CDD26) to take out the weather.

/** Cooling degree days count each degree of daily mean outdoor temperature above this base. */
export const DEGREE_DAY_BASE_C = 26;

export interface SiteSummary {
  readonly siteId: string;
  readonly name: string;
  readonly floorAreaM2: number | null;
  readonly climateZone: ClimateZone | null;
  /** Each day's mean outdoor dry-bulb temperature (°C) at the site's weather station, keyed by the day's start; null without a station. */
  readonly outdoorMeanC: ReadonlyMap<string, number> | null;
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
  readonly floorAreaM2: number | null;
  readonly climateZone: ClimateZone | null;
  /** HVAC electricity over the period per square metre of floor area; null without a floor area. */
  readonly intensityKWhPerM2: number | null;
  /** 1 uses the least electricity per square metre; null without an intensity. */
  readonly intensityRank: number | null;
  /** CDD26 over the days with both electricity and a mean temperature; null without a weather station. */
  readonly degreeDays: number | null;
  /** Electricity over those days per square metre per degree day; null without a floor area or degree days. */
  readonly climateIntensity: number | null;
  /** 1 uses the least electricity per square metre per degree day; null without a climate intensity. */
  readonly climateRank: number | null;
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
      const weathered = site.outdoorMeanC === null ? null : site.summary.rows.flatMap((row) => {
        const meanC = site.outdoorMeanC!.get(row.periodStart);
        return row.electricityKWh === null || meanC === undefined ? [] : [{ electricityKWh: row.electricityKWh, degreeDays: Math.max(0, meanC - DEGREE_DAY_BASE_C) }];
      });
      const degreeDays = weathered === null ? null : weathered.reduce((sum, day) => sum + day.degreeDays, 0);
      const weatheredKWh = weathered?.reduce((sum, day) => sum + day.electricityKWh, 0) ?? 0;
      return {
        siteId: site.siteId,
        name: site.name,
        electricityKWh: site.summary.electricityKWh,
        coolingKWh: site.summary.coolingKWh,
        cop: site.summary.cop,
        dailyElectricityKWh: site.summary.electricityKWh !== null && metered > 0 ? site.summary.electricityKWh / metered : null,
        copVsFleet: site.summary.cop !== null && fleetCop ? site.summary.cop / fleetCop - 1 : null,
        floorAreaM2: site.floorAreaM2,
        climateZone: site.climateZone,
        intensityKWhPerM2: site.floorAreaM2 !== null && site.summary.electricityKWh !== null ? site.summary.electricityKWh / site.floorAreaM2 : null,
        degreeDays,
        climateIntensity: site.floorAreaM2 !== null && degreeDays ? weatheredKWh / site.floorAreaM2 / degreeDays : null,
      };
    })
    .sort((left, right) => (right.cop ?? -Infinity) - (left.cop ?? -Infinity))
    .map((site, index) => ({ ...site, rank: site.cop === null ? null : index + 1 }));
  const placeBy = (value: (site: (typeof sites)[number]) => number | null) => {
    const ordered = sites.filter((site) => value(site) !== null).sort((left, right) => value(left)! - value(right)!);
    return (site: (typeof sites)[number]) => (ordered.includes(site) ? ordered.indexOf(site) + 1 : null);
  };
  const intensityPlace = placeBy((site) => site.intensityKWhPerM2);
  const climatePlace = placeBy((site) => site.climateIntensity);
  const rankedSites = sites.map((site): BenchmarkSite => ({ ...site, intensityRank: intensityPlace(site), climateRank: climatePlace(site) }));

  const days = new Map<string, Record<string, number | string | null>>();
  for (const site of input) {
    for (const row of site.summary.rows) {
      const day = days.get(row.periodStart) ?? { key: row.periodStart, label: row.label, ...Object.fromEntries(input.map((entry) => [entry.siteId, null])) };
      day[site.siteId] = row.cop;
      days.set(row.periodStart, day);
    }
  }

  return {
    sites: rankedSites,
    fleet: { electricityKWh: total('electricityKWh'), coolingKWh: total('coolingKWh'), cop: fleetCop },
    daily: [...days.values()].sort((left, right) => String(left.key).localeCompare(String(right.key))) as BenchmarkDay[],
  };
}
