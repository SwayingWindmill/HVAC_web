import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';

import { analyzeSiteNightEnergy, planSiteNightEnergyPeriods } from '../dist/index.js';

const benchmarkScenario = JSON.parse(await readFile(
  new URL(
    '../../../benchmarks/operations-agent/scenarios/night-energy-insufficient-attribution.v1.json',
    import.meta.url,
  ),
  'utf8',
));
const benchmarkComparison = benchmarkScenario.inputFacts.find(
  ({ kind }) => kind === 'SITE_ENERGY_PERIOD_COMPARISON',
).payload;

const organizationId = '0198f5c0-7c00-7000-8000-000000000001';
const siteId = '0198f5c0-7c00-7000-8000-000000000002';
const equipmentIds = [
  '0198f5c0-7c00-7000-8000-000000000010',
  '0198f5c0-7c00-7000-8000-000000000011',
  '0198f5c0-7c00-7000-8000-000000000012',
];

const hourlySeries = ({ from, hours, energyPerHour, datasetRevision }) => ({
  schemaVersion: 1,
  points: Array.from({ length: hours }, (_value, index) => {
    const periodStart = new Date(Date.parse(from) + index * 3_600_000);
    const periodEnd = new Date(periodStart.getTime() + 3_600_000);
    return {
      periodStart: periodStart.toISOString(),
      periodEnd: periodEnd.toISOString(),
      energyKWh: energyPerHour,
    };
  }),
  metadata: {
    requestedGranularity: 'hour',
    actualGranularity: 'hour',
    dataWatermark: new Date(Date.parse(from) + hours * 3_600_000 + 300_000).toISOString(),
    aggregateWatermark: new Date(Date.parse(from) + hours * 3_600_000 + 300_000).toISOString(),
    datasetRevision,
    partial: false,
    qualitySummary: { valid: hours, suspect: 0, invalid: 0 },
  },
});

const baseInput = {
  site: {
    organizationId,
    siteId,
    timezone: 'Asia/Shanghai',
    equipmentIds,
  },
  window: {
    startLocalTime: '22:00',
    endLocalTime: '06:00',
  },
  targetLocalDate: '2026-07-07',
  baselineLocalDate: '2026-06-30',
  increaseThresholdPercent: 10,
  qualityPolicy: 'VALID_ONLY',
  targetSeries: hourlySeries({
    from: '2026-07-07T14:00:00.000Z',
    hours: 8,
    energyPerHour: 155,
    datasetRevision: 'energy-dataset-r17',
  }),
  baselineSeries: hourlySeries({
    from: '2026-06-30T14:00:00.000Z',
    hours: 8,
    energyPerHour: 125,
    datasetRevision: 'energy-dataset-r17',
  }),
};

const analyzeChanged = (change) => {
  const input = structuredClone(baseInput);
  change(input);
  return analyzeSiteNightEnergy(input);
};

const assertUnableWith = (result, expectedCode) => {
  assert.equal(result.status, 'UNABLE_TO_CONCLUDE');
  assert.equal(result.blockers.some(({ code }) => code === expectedCode), true);
  assert.equal('siteFinding' in result, false);
  assert.equal('evidence' in result, false);
  assert.equal('comparison' in result, false);
};

test('period planning uses the same DST-aware boundaries as deterministic analysis', () => {
  const plan = planSiteNightEnergyPeriods({
    timezone: baseInput.site.timezone,
    window: baseInput.window,
    targetLocalDate: baseInput.targetLocalDate,
    baselineLocalDate: baseInput.baselineLocalDate,
  });
  const result = analyzeSiteNightEnergy(baseInput);

  assert.deepEqual(plan, {
    targetPeriod: result.analysisReference.targetPeriod,
    baselinePeriod: result.analysisReference.baselinePeriod,
  });
});

test('complete benchmark data confirms a 24% Site increase without Equipment attribution', () => {
  const result = analyzeSiteNightEnergy(baseInput);

  assert.equal(result.status, 'SUPPORTED_SITE_FINDING');
  assert.deepEqual(result.comparison, {
    targetEnergyKWh: benchmarkComparison.targetPeriod.energyKWh,
    baselineEnergyKWh: benchmarkComparison.baselinePeriod.energyKWh,
    changeKWh: benchmarkComparison.changeKWh,
    changePercent: benchmarkComparison.changePercent,
    increaseThresholdPercent: benchmarkComparison.increaseThresholdPercent,
  });
  assert.deepEqual(result.siteFinding, {
    kind: 'SITE_NIGHT_ENERGY_INCREASE',
    scope: 'SITE',
    classification: 'INFERENCE',
    statement: 'Target night energy was 1240 kWh versus a 1000 kWh baseline, a 24% increase.',
    supportEvidenceKinds: ['FACT', 'ALGORITHM_RESULT'],
  });
  assert.equal(result.equipmentAttribution.status, 'UNABLE_TO_CONCLUDE');
  assert.deepEqual(
    result.equipmentAttribution.requiredNext.map(({ status, kind, capability }) => ({
      status,
      kind,
      capability,
    })),
    [
      {
        status: 'REQUIRED_NEXT',
        kind: 'EQUIPMENT_ENERGY_BINDINGS',
        capability: 'registry.getEquipmentEnergyBindings',
      },
      {
        status: 'REQUIRED_NEXT',
        kind: 'EQUIPMENT_ENERGY_PERIOD_COMPARISON',
        capability: 'analytics.energy.getEquipmentSeries',
      },
    ],
  );
  assert.equal(result.analysisReference.algorithmVersion, 'site-night-energy-comparison/v1');
  assert.equal(
    result.analysisReference.bucketRule,
    'START_INCLUSIVE_END_EXCLUSIVE_CONTIGUOUS_HOURLY',
  );
  assert.equal(result.analysisReference.datasetRevision, 'energy-dataset-r17');
  assert.match(result.analysisReference.digest, /^sha256:[0-9a-f]{64}$/);
  assert.equal('points' in result.analysisReference, false);
  assert.equal('proposedAction' in result, false);
  assert.equal('commandIntent' in result, false);
  assert.equal(analyzeSiteNightEnergy(baseInput).analysisReference.digest, result.analysisReference.digest);
});

test('readiness failures never produce a confirmatory Site Finding', () => {
  const cases = [
    {
      code: 'PARTIAL_DATASET',
      change(input) { input.targetSeries.metadata.partial = true; },
    },
    {
      code: 'WATERMARK_STALE',
      change(input) {
        input.targetSeries.metadata.dataWatermark = '2026-07-07T21:59:59.000Z';
      },
    },
    {
      code: 'DATASET_REVISION_MISMATCH',
      change(input) { input.baselineSeries.metadata.datasetRevision = 'energy-dataset-r18'; },
    },
    {
      code: 'DATASET_REVISION_MISSING',
      change(input) { input.targetSeries.metadata.datasetRevision = '   '; },
    },
    {
      code: 'GRANULARITY_MISMATCH',
      change(input) { input.targetSeries.metadata.actualGranularity = 'day'; },
    },
    {
      code: 'QUALITY_POLICY_EXCEEDED',
      change(input) {
        input.targetSeries.metadata.qualitySummary = { valid: 7, suspect: 1, invalid: 0 };
      },
    },
    {
      code: 'MISSING_BUCKETS',
      change(input) {
        input.targetSeries.points.splice(3, 1);
        input.targetSeries.metadata.qualitySummary = { valid: 7, suspect: 0, invalid: 0 };
      },
    },
    {
      code: 'ZERO_BASELINE',
      change(input) {
        for (const point of input.baselineSeries.points) point.energyKWh = 0;
      },
    },
    {
      code: 'NON_FINITE_ENERGY',
      change(input) { input.targetSeries.points[0].energyKWh = Number.POSITIVE_INFINITY; },
    },
  ];

  for (const scenario of cases) {
    assertUnableWith(analyzeChanged(scenario.change), scenario.code);
  }
});

test('suspect buckets are accepted only when the explicit Quality Policy allows them', () => {
  const result = analyzeChanged((input) => {
    input.qualityPolicy = 'VALID_AND_SUSPECT';
    input.targetSeries.metadata.qualitySummary = { valid: 7, suspect: 1, invalid: 0 };
  });

  assert.equal(result.status, 'SUPPORTED_SITE_FINDING');
  assert.deepEqual(result.analysisReference.qualitySummaries.target, {
    valid: 7,
    suspect: 1,
    invalid: 0,
  });
});

test('daylight-saving windows use elapsed hourly buckets rather than assuming eight hours', () => {
  const springInput = {
    ...structuredClone(baseInput),
    site: {
      ...structuredClone(baseInput.site),
      timezone: 'America/New_York',
    },
    targetLocalDate: '2026-03-07',
    baselineLocalDate: '2025-03-08',
    targetSeries: hourlySeries({
      from: '2026-03-08T03:00:00.000Z',
      hours: 7,
      energyPerHour: 100,
      datasetRevision: 'energy-dataset-dst-r1',
    }),
    baselineSeries: hourlySeries({
      from: '2025-03-09T03:00:00.000Z',
      hours: 7,
      energyPerHour: 100,
      datasetRevision: 'energy-dataset-dst-r1',
    }),
  };

  const result = analyzeSiteNightEnergy(springInput);

  assert.equal(result.status, 'SUPPORTED_SITE_FINDING');
  assert.equal(result.analysisReference.targetPeriod.expectedBuckets, 7);
  assert.equal(result.analysisReference.baselinePeriod.expectedBuckets, 7);
  assert.equal(result.comparison.changePercent, 0);
  assert.equal(result.siteFinding.kind, 'SITE_NIGHT_ENERGY_WITHIN_THRESHOLD');
});

test('different DST elapsed durations are not treated as a comparable baseline', () => {
  const result = analyzeSiteNightEnergy({
    ...structuredClone(baseInput),
    site: {
      ...structuredClone(baseInput.site),
      timezone: 'America/New_York',
    },
    targetLocalDate: '2026-03-07',
    baselineLocalDate: '2026-02-28',
    targetSeries: hourlySeries({
      from: '2026-03-08T03:00:00.000Z',
      hours: 7,
      energyPerHour: 100,
      datasetRevision: 'energy-dataset-dst-r2',
    }),
    baselineSeries: hourlySeries({
      from: '2026-03-01T03:00:00.000Z',
      hours: 8,
      energyPerHour: 100,
      datasetRevision: 'energy-dataset-dst-r2',
    }),
  });

  assertUnableWith(result, 'WINDOW_DURATION_MISMATCH');
});

test('fall-back daylight-saving windows contain nine elapsed hourly buckets', () => {
  const result = analyzeSiteNightEnergy({
    ...structuredClone(baseInput),
    site: {
      ...structuredClone(baseInput.site),
      timezone: 'America/New_York',
    },
    targetLocalDate: '2026-10-31',
    baselineLocalDate: '2025-11-01',
    targetSeries: hourlySeries({
      from: '2026-11-01T02:00:00.000Z',
      hours: 9,
      energyPerHour: 100,
      datasetRevision: 'energy-dataset-dst-r3',
    }),
    baselineSeries: hourlySeries({
      from: '2025-11-02T02:00:00.000Z',
      hours: 9,
      energyPerHour: 100,
      datasetRevision: 'energy-dataset-dst-r3',
    }),
  });

  assert.equal(result.status, 'SUPPORTED_SITE_FINDING');
  assert.equal(result.analysisReference.targetPeriod.expectedBuckets, 9);
  assert.equal(result.analysisReference.baselinePeriod.expectedBuckets, 9);
});

test('semantic input equality produces the same digest regardless of JSON key order', () => {
  const reordered = structuredClone(baseInput);
  reordered.targetSeries = {
    metadata: {
      qualitySummary: {
        invalid: baseInput.targetSeries.metadata.qualitySummary.invalid,
        suspect: baseInput.targetSeries.metadata.qualitySummary.suspect,
        valid: baseInput.targetSeries.metadata.qualitySummary.valid,
      },
      partial: baseInput.targetSeries.metadata.partial,
      datasetRevision: baseInput.targetSeries.metadata.datasetRevision,
      aggregateWatermark: baseInput.targetSeries.metadata.aggregateWatermark,
      dataWatermark: baseInput.targetSeries.metadata.dataWatermark,
      actualGranularity: baseInput.targetSeries.metadata.actualGranularity,
      requestedGranularity: baseInput.targetSeries.metadata.requestedGranularity,
    },
    points: baseInput.targetSeries.points.map((point) => ({
      energyKWh: point.energyKWh,
      periodEnd: point.periodEnd,
      periodStart: point.periodStart,
    })),
    schemaVersion: 1,
  };

  assert.equal(
    analyzeSiteNightEnergy(reordered).analysisReference.digest,
    analyzeSiteNightEnergy(baseInput).analysisReference.digest,
  );
});

test('zero change is not classified as an increase at a zero-percent threshold', () => {
  const result = analyzeChanged((input) => {
    input.increaseThresholdPercent = 0;
    for (const point of input.targetSeries.points) point.energyKWh = 125;
  });

  assert.equal(result.status, 'SUPPORTED_SITE_FINDING');
  assert.equal(result.comparison.changePercent, 0);
  assert.equal(result.siteFinding.kind, 'SITE_NIGHT_ENERGY_WITHIN_THRESHOLD');
});

test('arbitrary query payloads are rejected as invalid analysis input', () => {
  assert.throws(
    () => analyzeSiteNightEnergy({
      ...structuredClone(baseInput),
      targetSeries: {
        payload: { targetEnergyKWh: 1240, baselineEnergyKWh: 1000 },
      },
    }),
    (error) => error?.name === 'NightEnergyAnalysisError'
      && error.code === 'INVALID_ANALYSIS_INPUT',
  );
});
