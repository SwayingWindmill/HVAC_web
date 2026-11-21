import assert from 'node:assert/strict';
import test from 'node:test';
import {
  createSiteMeterRatioVersion,
  deleteSiteAccountingHoliday,
  describeAccountingConfigError,
  fetchSiteCounterAudit,
  listSiteAccountingHolidays,
  listSiteMeterRatioVersions,
  retireSiteMeterRatioVersion,
  upsertSiteAccountingHoliday,
} from '../apps/hvac-web/src/api/accounting-config.ts';
import { GatewayRequestError } from '../apps/hvac-web/src/api/gateway-client.ts';
import {
  buildCounterAuditKpiMetrics,
  canRetireRatioVersion,
  counterQualityMeta,
  counterTransitionMeta,
  deriveRatioLifecycleStatus,
  formatCounterQualityReasons,
  formatWeekdayShort,
  isValidDateKey,
  parseSiteMomentInput,
  previewCloudMultiplier,
  RATIO_APPLICATION_MODE_LABEL,
  RATIO_READING_UNIT_LABEL,
  SITE_CALENDAR_DAY_TYPE_LABEL,
  validateRatioForm,
} from '../apps/hvac-web/src/features/billing/accounting-config-model.ts';

const SITE_ID = '018f3e00-1000-7000-8000-000000000001';
const TENANT_ID = '018f3d00-0000-7000-8000-000000000001';
const POINT_ID = '018f3e00-5000-7000-8000-000000000001';
const TIMEZONE = 'Asia/Shanghai';

function makeJsonResponse(payload: unknown, status = 200): Response {
  return new Response(JSON.stringify(payload), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function makeProblemResponse(status: number, code: string, detail?: string): Response {
  return new Response(JSON.stringify({ status, code, detail: detail ?? code }), {
    status,
    headers: { 'Content-Type': 'application/problem+json' },
  });
}

test('accounting-config API lists, upserts, and deletes holidays with CSRF and year query', async () => {
  const seenRequests: Array<{ url: string; method: string; headers: Record<string, string>; body?: string }> = [];
  const sampleHoliday = {
    id: '01990000-1000-7000-8000-000000000001',
    tenantId: TENANT_ID,
    siteId: SITE_ID,
    holidayDate: '2026-10-01',
    dayType: 'HOLIDAY',
    label: '国庆节',
    revision: 1,
    createdAt: '2026-09-01T00:00:00.000Z',
    updatedAt: '2026-09-01T00:00:00.000Z',
  };

  const fakeFetch: typeof fetch = async (input, init) => {
    const url = typeof input === 'string' ? input : input.toString();
    const method = init?.method ?? 'GET';
    const headers: Record<string, string> = {};
    if (init?.headers && !(init.headers instanceof Headers) && !Array.isArray(init.headers)) {
      Object.assign(headers, init.headers);
    }
    const body = typeof init?.body === 'string' ? init.body : undefined;
    seenRequests.push({ url, method, headers, body });

    if (method === 'GET') {
      return makeJsonResponse({ items: [{ ...sampleHoliday, label: null }] });
    }
    if (method === 'PUT') {
      return makeJsonResponse({ ...sampleHoliday, recalcJobId: '01990000-2000-7000-8000-000000000009' });
    }
    return makeJsonResponse({ holidayDate: '2026-10-01', deleted: true, recalcJobId: '01990000-2000-7000-8000-000000000010' });
  };

  const listed = await listSiteAccountingHolidays(SITE_ID, { year: 2026, fetch: fakeFetch });
  assert.equal(listed.length, 1);
  assert.equal(listed[0]?.label, '');
  assert.equal(seenRequests[0]?.url, `/api/v1/sites/${SITE_ID}/accounting/holidays?year=2026`);

  const saved = await upsertSiteAccountingHoliday(
    SITE_ID,
    'csrf-tok-1',
    { date: '2026-10-01', dayType: 'HOLIDAY', label: '国庆节', idempotencyKey: 'idem-h-1' },
    { fetch: fakeFetch },
  );
  assert.equal(saved.recalcJobId, '01990000-2000-7000-8000-000000000009');
  assert.equal(seenRequests[1]?.method, 'PUT');
  assert.equal(seenRequests[1]?.headers['X-CSRF-Token'], 'csrf-tok-1');
  assert.equal(seenRequests[1]?.body, JSON.stringify({ dayType: 'HOLIDAY', label: '国庆节' }));

  const deleted = await deleteSiteAccountingHoliday(SITE_ID, 'csrf-tok-1', '2026-10-01', {
    idempotencyKey: 'idem-h-2',
    fetch: fakeFetch,
  });
  assert.equal(deleted.deleted, true);
  assert.equal(deleted.recalcJobId, '01990000-2000-7000-8000-000000000010');
  assert.equal(seenRequests[2]?.method, 'DELETE');
  assert.equal(seenRequests[2]?.headers['X-CSRF-Token'], 'csrf-tok-1');
});

test('accounting-config API handles meter ratios CRUD, retire, 409 METER_RATIO_OVERLAP, and error mapping', async () => {
  const versionId = '01990000-3000-7000-8000-000000000001';
  const baseRatio = {
    id: versionId,
    tenantId: TENANT_ID,
    siteId: SITE_ID,
    meterId: null,
    meterBindingId: null,
    pointId: POINT_ID,
    version: 1,
    ctRatio: 100,
    ptRatio: 1,
    meterMultiplier: 1,
    cloudMultiplier: 100,
    ratioApplicationMode: 'PLATFORM_APPLIED',
    readingUnit: 'kWh',
    effectiveFrom: '2026-10-01T00:00:00.000Z',
    effectiveTo: null,
    status: 'RELEASED',
    revision: 1,
    createdAt: '2026-10-01T00:00:00.000Z',
    updatedAt: '2026-10-01T00:00:00.000Z',
  };

  let lastUrl = '';
  const listed = await listSiteMeterRatioVersions(SITE_ID, {
    pointId: POINT_ID,
    includeSuperseded: true,
    fetch: async (input) => {
      lastUrl = typeof input === 'string' ? input : input.toString();
      return makeJsonResponse({ items: [baseRatio] });
    },
  });
  assert.equal(listed.length, 1);
  assert.equal(
    lastUrl,
    `/api/v1/sites/${SITE_ID}/accounting/meter-ratios?pointId=${POINT_ID}&includeSuperseded=true`,
  );

  const created = await createSiteMeterRatioVersion(
    SITE_ID,
    'csrf-tok-2',
    {
      pointId: POINT_ID,
      ctRatio: 100,
      ptRatio: 1,
      meterMultiplier: 1,
      ratioApplicationMode: 'PLATFORM_APPLIED',
      readingUnit: 'kWh',
      effectiveFrom: '2026-10-01T00:00:00.000Z',
      reason: '初始化倍率',
      idempotencyKey: 'idem-r-1',
    },
    { fetch: async () => makeJsonResponse({ ...baseRatio, recalcJobId: 'job-r1' }, 201) },
  );
  assert.equal(created.cloudMultiplier, 100);
  assert.equal(created.recalcJobId, 'job-r1');

  await assert.rejects(
    () =>
      createSiteMeterRatioVersion(
        SITE_ID,
        'csrf-tok-2',
        {
          pointId: POINT_ID,
          ctRatio: 200,
          ptRatio: 1,
          ratioApplicationMode: 'PLATFORM_APPLIED',
          readingUnit: 'kWh',
          effectiveFrom: '2026-10-15T00:00:00.000Z',
        },
        { fetch: async () => makeProblemResponse(409, 'METER_RATIO_OVERLAP') },
      ),
    (error: unknown) => {
      assert.ok(error instanceof GatewayRequestError);
      assert.match(error.message, /重叠/);
      assert.equal(error.message.includes('409'), false);
      assert.equal(error.message.includes('METER_RATIO_OVERLAP'), false);
      return true;
    },
  );

  const retired = await retireSiteMeterRatioVersion(
    SITE_ID,
    'csrf-tok-2',
    versionId,
    { effectiveTo: '2026-11-01T00:00:00.000Z', reason: '换表终止' },
    {
      fetch: async () =>
        makeJsonResponse({
          ...baseRatio,
          effectiveTo: '2026-11-01T00:00:00.000Z',
          status: 'RELEASED',
          revision: 2,
        }),
    },
  );
  assert.equal(retired.status, 'RELEASED');
  assert.equal(retired.effectiveTo, '2026-11-01T00:00:00.000Z');

  // Verify 403, 404, 400, 500 friendly Chinese error messages without raw codes.
  for (const [status, code, expectMatch] of [
    [403, 'BILLING_MANAGE_FORBIDDEN', /没有.*权限/],
    [403, 'ANALYTICS_ACCESS_DENIED', /没有查看.*权限/],
    [404, 'RESOURCE_NOT_FOUND', /未找到/],
    [400, 'VALIDATION_ERROR', /不符合校验要求/],
    [500, 'INTERNAL', /兜底提示/],
  ] as const) {
    await assert.rejects(
      () =>
        listSiteAccountingHolidays(SITE_ID, {
          fetch: async () => makeProblemResponse(status, code),
        }),
      (error: unknown) => {
        const message = describeAccountingConfigError(error, '兜底提示');
        assert.match(message, expectMatch);
        assert.equal(message.includes(String(status)), false);
        assert.equal(message.includes(code), false);
        return true;
      },
    );
  }
});

test('fetchSiteCounterAudit parses summary, nullable item fields, and query parameters', async () => {
  let capturedUrl = '';
  const audit = await fetchSiteCounterAudit(SITE_ID, {
    from: '2026-11-18T16:00:00.000Z',
    to: '2026-11-19T10:00:00.000Z',
    anomaliesOnly: true,
    limit: 200,
    fetch: async (input) => {
      capturedUrl = typeof input === 'string' ? input : input.toString();
      return makeJsonResponse({
        siteId: SITE_ID,
        from: '2026-11-18T16:00:00.000Z',
        to: '2026-11-19T10:00:00.000Z',
        summary: {
          totalSamples: 1440,
          anomalyCount: 5,
          zeroDropCount: 2,
          spikeCount: 1,
          resetCount: 1,
          rolloverCount: 1,
          invalidDecreaseCount: 0,
        },
        items: [
          {
            pointId: POINT_ID,
            deviceId: '018f3e00-3000-7000-8000-000000000001',
            telemetryKey: 'chiller_main_kwh',
            periodStart: '2026-11-19T01:00:00.000Z',
            periodEnd: '2026-11-19T01:15:00.000Z',
            rawPrevious: 18520.5,
            rawCurrent: 0,
            cleanedDeltaKWh: null,
            transitionType: 'INVALID_ZERO_DROP',
            quality: 'INVALID',
            qualityReasons: ['ZERO_DROP'],
            unit: 'kWh',
          },
          {
            pointId: POINT_ID,
            deviceId: null,
            telemetryKey: null,
            periodStart: null,
            periodEnd: '2026-11-19T02:00:00.000Z',
            rawPrevious: null,
            rawCurrent: 12.4,
            cleanedDeltaKWh: 12.4,
            transitionType: 'RESET',
            quality: 'SUSPECT',
            qualityReasons: null,
            unit: null,
          },
        ],
        truncated: false,
      });
    },
  });

  assert.equal(
    capturedUrl,
    `/api/v1/sites/${SITE_ID}/accounting/counter-audit?from=2026-11-18T16%3A00%3A00.000Z&to=2026-11-19T10%3A00%3A00.000Z&anomaliesOnly=true&limit=200`,
  );
  assert.equal(audit.summary.totalSamples, 1440);
  assert.equal(audit.items[0]?.cleanedDeltaKWh, null);
  assert.deepEqual(audit.items[1]?.qualityReasons, []);
  assert.equal(audit.items[1]?.unit, 'kWh');
  assert.equal(audit.truncated, false);
});

test('previewCloudMultiplier computes PLATFORM_APPLIED vs DEVICE_APPLIED/EDGE_APPLIED accurately', () => {
  assert.equal(
    previewCloudMultiplier({
      ctRatio: 100,
      ptRatio: 1,
      meterMultiplier: 1.5,
      ratioApplicationMode: 'PLATFORM_APPLIED',
    }),
    150,
  );
  assert.equal(
    previewCloudMultiplier({
      ctRatio: 1.2,
      ptRatio: 3,
      meterMultiplier: 1,
      ratioApplicationMode: 'PLATFORM_APPLIED',
    }),
    3.6,
  );
  assert.equal(
    previewCloudMultiplier({
      ctRatio: 200,
      ptRatio: 2,
      meterMultiplier: 1.5,
      ratioApplicationMode: 'DEVICE_APPLIED',
    }),
    1,
  );
  assert.equal(
    previewCloudMultiplier({
      ctRatio: 200,
      ptRatio: 2,
      meterMultiplier: 1.5,
      ratioApplicationMode: 'EDGE_APPLIED',
    }),
    1,
  );
  assert.equal(
    previewCloudMultiplier({
      ctRatio: 0,
      ptRatio: 1,
      meterMultiplier: 1,
      ratioApplicationMode: 'PLATFORM_APPLIED',
    }),
    null,
  );
});

test('deriveRatioLifecycleStatus classifies active, retired-while-RELEASED, future-ended, superseded, and draft', () => {
  const now = new Date('2026-11-19T10:00:00.000Z');

  const active = deriveRatioLifecycleStatus(
    {
      status: 'RELEASED',
      effectiveFrom: '2026-10-01T00:00:00.000Z',
      effectiveTo: null,
    },
    now,
  );
  assert.deepEqual(active, { key: 'ACTIVE', label: '● 生效中', variant: 'success' });
  assert.equal(canRetireRatioVersion({ status: 'RELEASED', effectiveTo: null }), true);

  const retired = deriveRatioLifecycleStatus(
    {
      status: 'RELEASED',
      effectiveFrom: '2026-10-01T00:00:00.000Z',
      effectiveTo: '2026-11-19T10:00:00.000Z',
    },
    now,
  );
  assert.deepEqual(retired, { key: 'RETIRED', label: '○ 已终止', variant: 'outline' });
  assert.equal(
    canRetireRatioVersion({ status: 'RELEASED', effectiveTo: '2026-11-19T10:00:00.000Z' }),
    false,
  );

  const scheduledEndActive = deriveRatioLifecycleStatus(
    {
      status: 'RELEASED',
      effectiveFrom: '2026-10-01T00:00:00.000Z',
      effectiveTo: '2026-12-01T00:00:00.000Z',
    },
    now,
  );
  assert.equal(scheduledEndActive.key, 'ACTIVE');
  assert.equal(scheduledEndActive.label, '● 生效中');

  const superseded = deriveRatioLifecycleStatus(
    {
      status: 'SUPERSEDED',
      effectiveFrom: '2026-01-01T00:00:00.000Z',
      effectiveTo: '2026-10-01T00:00:00.000Z',
    },
    now,
  );
  assert.deepEqual(superseded, { key: 'SUPERSEDED', label: '— 已替代', variant: 'outline' });

  const draft = deriveRatioLifecycleStatus(
    {
      status: 'DRAFT',
      effectiveFrom: '2026-12-01T00:00:00.000Z',
      effectiveTo: null,
    },
    now,
  );
  assert.deepEqual(draft, { key: 'DRAFT', label: '◦ 草稿', variant: 'outline' });
});

test('isValidDateKey, parseSiteMomentInput, and validateRatioForm enforce calendar and window boundaries', () => {
  assert.equal(isValidDateKey('2026-10-01'), true);
  assert.equal(isValidDateKey('2028-02-29'), true);
  assert.equal(isValidDateKey('2026-02-29'), false);
  assert.equal(isValidDateKey('2026-04-31'), false);
  assert.equal(isValidDateKey('2026-13-01'), false);
  assert.equal(isValidDateKey('not-a-date'), false);
  assert.equal(formatWeekdayShort('2026-10-01'), '周四');

  const shanghaiMidnight = parseSiteMomentInput('2026-10-01T00:00', TIMEZONE);
  assert.equal(shanghaiMidnight?.toISOString(), '2026-09-30T16:00:00.000Z');

  const validOpen = validateRatioForm(
    {
      pointId: POINT_ID,
      ctRatio: '150',
      ptRatio: '1',
      meterMultiplier: '',
      ratioApplicationMode: 'PLATFORM_APPLIED',
      readingUnit: 'kWh',
      effectiveFrom: '2026-10-01T00:00',
      effectiveTo: '',
      reason: '变更互感器',
    },
    TIMEZONE,
  );
  assert.equal(validOpen.ok, true);
  if (validOpen.ok) {
    assert.equal(validOpen.value.meterMultiplier, 1);
    assert.equal(validOpen.value.cloudMultiplier, 150);
    assert.equal(validOpen.value.effectiveFrom, '2026-09-30T16:00:00.000Z');
    assert.equal(validOpen.value.effectiveTo, undefined);
  }

  const invalidWindow = validateRatioForm(
    {
      pointId: POINT_ID,
      ctRatio: '100',
      ptRatio: '1',
      meterMultiplier: '1',
      ratioApplicationMode: 'PLATFORM_APPLIED',
      readingUnit: 'kWh',
      effectiveFrom: '2026-10-10T08:00',
      effectiveTo: '2026-10-10T08:00',
    },
    TIMEZONE,
  );
  assert.deepEqual(invalidWindow, {
    ok: false,
    field: 'effectiveTo',
    message: '生效结束时间必须晚于生效开始时间',
  });

  const invalidCt = validateRatioForm(
    {
      pointId: POINT_ID,
      ctRatio: '-5',
      ptRatio: '1',
      meterMultiplier: '1',
      ratioApplicationMode: 'PLATFORM_APPLIED',
      readingUnit: 'kWh',
      effectiveFrom: '2026-10-01T00:00',
    },
    TIMEZONE,
  );
  assert.deepEqual(invalidCt, {
    ok: false,
    field: 'ctRatio',
    message: 'CT 变比须为大于 0 的数字',
  });
});

test('dictionary labels, transition metadata, quality reasons, and 6-metric KPI strip map cleanly', () => {
  assert.equal(SITE_CALENDAR_DAY_TYPE_LABEL.HOLIDAY, '节假日');
  assert.equal(SITE_CALENDAR_DAY_TYPE_LABEL.SPECIAL_DAY, '特殊日');
  assert.equal(SITE_CALENDAR_DAY_TYPE_LABEL.WEEKDAY, '工作日');
  assert.equal(SITE_CALENDAR_DAY_TYPE_LABEL.WEEKEND, '周末');

  assert.ok(RATIO_APPLICATION_MODE_LABEL.PLATFORM_APPLIED.includes('平台'));
  assert.ok(RATIO_APPLICATION_MODE_LABEL.DEVICE_APPLIED.includes('表端'));
  assert.ok(RATIO_APPLICATION_MODE_LABEL.EDGE_APPLIED.includes('边缘'));
  assert.ok(RATIO_READING_UNIT_LABEL.RTh.includes('RTh'));

  const expectedTransitions: ReadonlyArray<readonly [string, string]> = [
    ['INVALID_ZERO_DROP', '零值通信干扰'],
    ['INVALID_SPIKE', '单步飞值尖峰'],
    ['INVALID_DECREASE', '异常回退'],
    ['RESET', '换表归零'],
    ['ROLLOVER', '满量程翻转'],
    ['RECOVERY', '回退后恢复'],
    ['METER_REPLACEMENT', '换表衔接'],
    ['INCREASE', '正常递进'],
  ];
  for (const [code, label] of expectedTransitions) {
    assert.equal(counterTransitionMeta(code).label, label);
  }
  const fallbackTransition = counterTransitionMeta('UNSEEN_INTERNAL_ENUM');
  assert.equal(fallbackTransition.label.includes('UNSEEN_INTERNAL_ENUM'), false);

  assert.equal(counterQualityMeta('GOOD').label, '有效');
  assert.equal(counterQualityMeta('SUSPECT').label, '可疑');
  assert.equal(counterQualityMeta('INVALID').label, '无效');
  assert.equal(formatCounterQualityReasons([]), '—');
  assert.equal(formatCounterQualityReasons(['ZERO_DROP', 'SPIKE']), '累计示数突降为零、单步增量超出功率上限');

  const kpis = buildCounterAuditKpiMetrics({
    totalSamples: 1200,
    anomalyCount: 4,
    zeroDropCount: 2,
    spikeCount: 1,
    resetCount: 1,
    rolloverCount: 0,
    invalidDecreaseCount: 0,
  });
  assert.deepEqual(
    kpis.map((item) => [item.label, item.value, item.tone]),
    [
      ['总采样数', 1200, 'neutral'],
      ['异常与事件', 4, 'warning'],
      ['零值干扰', 2, 'destructive'],
      ['单步飞值', 1, 'destructive'],
      ['换表归零', 1, 'warning'],
      ['满量程翻转', 0, 'neutral'],
    ],
  );
});
