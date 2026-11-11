import { SITE_ID, principal } from './gateway';
import { expect, test } from './test';

const CHILLER_DEVICE_ID = '018f3e00-4000-7000-8000-000000000001';
const CHILLER_POWER_POINT_ID = '01910000-0000-7000-8000-010000000001';

// Pin clock to Wednesday 2026-10-14 15:00 Asia/Shanghai (2026-10-14T07:00:00Z).
// Current week is Monday 2026-10-12 through Sunday 2026-10-18.
const PINNED_NOW_ISO = '2026-10-14T07:00:00.000Z';

function buildOffWindowRunningHandler(deviationBucketsUtc: readonly [string, string][]) {
  return (query: Record<string, any>) => {
    if (query.granularity !== 'QUARTER_HOUR') return undefined;
    const buckets: Record<string, any>[] = [];
    if (query.deviceId === CHILLER_DEVICE_ID && (query.keys as string[]).includes('power')) {
      for (const [periodStart, periodEnd] of deviationBucketsUtc) {
        if (Date.parse(periodStart) >= Date.parse(query.from) && Date.parse(periodEnd) <= Date.parse(query.to)) {
          buckets.push({
            telemetryKey: 'power',
            pointId: CHILLER_POWER_POINT_ID,
            pointRevision: 1,
            pointType: 'TELEMETRY',
            unit: 'kW',
            periodStart,
            periodEnd,
            quality: { good: 3, partial: 0, estimated: 0, manual: 0, stale: 0, invalid: 0 },
            completeness: 1,
            gauge: {
              average: 186.4,
              minimum: 172.0,
              maximum: 198.2,
              first: 180.0,
              last: 190.0,
              sampleCount: 3,
            },
          });
        }
      }
    }
    return {
      body: {
        schemaVersion: 1,
        tenantId: principal.context.tenantId,
        siteId: SITE_ID,
        deviceId: query.deviceId,
        buckets,
        metadata: {
          requestedFrom: query.from,
          requestedTo: query.to,
          granularity: 'QUARTER_HOUR',
          timezone: query.timezone,
          qualityPolicy: query.qualityPolicy,
          projectionWatermark: query.to,
          returnedBuckets: buckets.length,
        },
      },
    };
  };
}

test('empty state configures weekly schedule (with cross-midnight slot), detects 15m unscheduled running, creates deduplicated work order, and clears deviation upon overtime approval', async ({
  page,
  gateway,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.clock.install({ time: new Date(PINNED_NOW_ISO) });

  // Simulate CHILLER-01 running on Tuesday 2026-10-13 22:00–22:30 Asia/Shanghai (14:00–14:30Z),
  // which is outside 08:00–21:00 + 30 min coast-down lag (21:30).
  gateway.aggregateHandler = buildOffWindowRunningHandler([
    ['2026-10-13T14:00:00.000Z', '2026-10-13T14:15:00.000Z'],
    ['2026-10-13T14:15:00.000Z', '2026-10-13T14:30:00.000Z'],
  ]);

  await page.goto(`/operations/realtime?site=${SITE_ID}`);
  await page.getByRole('link', { name: '运行计划' }).click();
  await expect(page).toHaveURL(/\/operations\/schedules/);

  // 1. Empty state on unconfigured weekly schedule
  await expect(page.getByRole('heading', { name: '运行计划', exact: true })).toBeVisible();
  await expect(page.getByRole('heading', { name: '尚未配置周运行计划', exact: true })).toBeVisible();

  // Open editor from empty state CTA, fill workday preset + add a Saturday cross-midnight slot
  await page.getByRole('button', { name: '配置默认周计划' }).click();
  const editorDialog = page.getByRole('dialog', { name: '如何配置站点周运行计划？' });
  await expect(editorDialog).toBeVisible();

  await editorDialog.getByRole('button', { name: '填充工作日 08:00–21:00' }).click();
  await editorDialog.getByRole('button', { name: '添加供冷时段' }).click();
  await editorDialog.getByRole('combobox', { name: '第 6 行星期' }).click();
  await page.getByRole('option', { name: '周六' }).click();
  await editorDialog.getByRole('textbox', { name: '第 6 行开始时间' }).fill('22:00');
  await editorDialog.getByRole('checkbox', { name: '第 6 行跨日' }).click();
  await editorDialog.getByRole('textbox', { name: '第 6 行结束时间' }).fill('02:00');
  await editorDialog.getByLabel('变更原因').fill('配置夏季工作日与周末夜间活动供冷窗口');
  await editorDialog.getByRole('button', { name: '保存周计划' }).click();
  await expect(editorDialog).toBeHidden();

  // 2. Verify L1 7x24h grid renders slots, cross-midnight split badge, and zero horizontal overflow at 1440px
  await expect(page.getByText('发现 1 段计划外开机，点击色块核查')).toBeVisible();
  await expect(page.getByText('跨日').first()).toBeVisible();

  const hasHorizontalOverflow = await page.evaluate(
    () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
  );
  expect(hasHorizontalOverflow).toBe(false);

  // Inspect a regular slot and verify explicit advisory temperature boundary notice
  await page.getByRole('button', { name: /2026-10-12 周一 常规供冷 08:00–21:00/ }).click();
  const slotSheet = page.getByRole('dialog', { name: '2026-10-12（周一）供冷时段' });
  await expect(slotSheet).toBeVisible();
  await expect(slotSheet.getByText(/仅作冷站值班交接参考，平台不会据此自动下发设备启停或设定值指令/)).toBeVisible();
  await slotSheet.getByRole('button', { name: 'Close' }).click();
  await expect(slotSheet).toBeHidden();

  // 3. Click the UNSCHEDULED_RUNNING deviation block -> L2 Drawer -> verify Trends link & create deduplicated EXTERNAL work order
  await page.getByRole('button', { name: /计划外开机：2026-10-13 周二 22:00–22:30/ }).click();
  const devSheet = page.getByRole('dialog', { name: '计划外开机核查' });
  await expect(devSheet).toBeVisible();
  await expect(devSheet.getByText('CHILLER-01')).toBeVisible();
  await expect(devSheet.getByText('186.4').first()).toBeVisible();
  await expect(devSheet.getByRole('link', { name: '在历史趋势查看' })).toHaveAttribute(
    'href',
    /\/operations\/trends\?.*granularity=QUARTER_HOUR/,
  );

  await devSheet.getByRole('button', { name: '创建核查工单' }).click();
  const woDialog = page.getByRole('dialog', { name: '要为此段计划外开机创建核查工单吗？' });
  await expect(woDialog).toBeVisible();
  await woDialog.getByRole('button', { name: '创建工单' }).click();
  await expect(woDialog).toBeHidden();

  // Button switches to deduplicated 查看关联工单!
  await expect(devSheet.getByRole('link', { name: /查看关联工单/ })).toBeVisible();
  await devSheet.getByRole('button', { name: 'Close' }).click();

  // 4. Switch to L3 运行例外 -> submit PENDING_APPROVAL OVERTIME_COOLING for 2026-10-13 -> approve it -> verify deviation clears!
  await page.getByRole('tab', { name: /运行例外/ }).click();
  await expect(page).toHaveURL(/view=exceptions/);

  await page.getByRole('main').getByRole('button', { name: '申请运行例外' }).first().click();
  const excDialog = page.getByRole('dialog', { name: '要申请哪几天的运行例外？' });
  await expect(excDialog).toBeVisible();
  await excDialog.getByLabel('例外名称').fill('周二夜间展厅延时供冷');
  await excDialog.getByRole('combobox', { name: '例外类型' }).click();
  await page.getByRole('option', { name: '加班供冷' }).click();
  await excDialog.getByLabel('开始日期（含当日）').fill('2026-10-13');
  await excDialog.getByLabel('结束日期（含当日）').fill('2026-10-13');
  await excDialog.getByRole('textbox', { name: '加班时段 1 开始时间' }).fill('08:00');
  await excDialog.getByRole('textbox', { name: '加班时段 1 结束时间' }).fill('23:00');
  await excDialog.getByLabel('申请原因').fill('租户夜间发布会申请延长供冷至 23:00');
  // Turn off autoApprove so we verify PENDING_APPROVAL -> APPROVE flow
  await excDialog.getByRole('checkbox', { name: '提交并立即生效' }).click();
  await excDialog.getByRole('button', { name: '提交申请' }).click();
  await expect(excDialog).toBeHidden();

  const excRow = page.getByRole('row', { name: /周二夜间展厅延时供冷/ });
  await expect(excRow).toContainText('待审批');
  await expect(excRow).toContainText('值班工程师');
  await excRow.getByRole('button', { name: '审批通过' }).click();
  await expect(excRow).toContainText('已生效');

  // Verify overlapping approved exception returns explicit 409 conflict message
  await page.getByRole('main').getByRole('button', { name: '申请运行例外' }).first().click();
  const overlapDialog = page.getByRole('dialog', { name: '要申请哪几天的运行例外？' });
  await overlapDialog.getByLabel('例外名称').fill('重叠停机测试');
  await overlapDialog.getByLabel('开始日期（含当日）').fill('2026-10-13');
  await overlapDialog.getByLabel('结束日期（含当日）').fill('2026-10-13');
  await overlapDialog.getByLabel('申请原因').fill('测试同一天已生效例外冲突拦截');
  await overlapDialog.getByRole('button', { name: '提交并生效' }).click();
  await expect(
    overlapDialog.getByText('该日期范围与已生效的运行例外重叠，请调整起止日期或先撤回已有例外'),
  ).toBeVisible();
  await overlapDialog.getByRole('button', { name: '取消' }).click();

  // Return to L1 周运行计划: Tuesday is now overridden by 加班供冷 (08:00–23:00) and UNSCHEDULED_RUNNING is cleared!
  await page.getByRole('tab', { name: '周运行计划' }).click();
  await expect(page.getByText('含 1 天例外覆盖')).toBeVisible();
  await expect(page.getByText('已核查时段无计划外开机')).toBeVisible();
  await expect(page.getByRole('button', { name: /计划外开机：2026-10-13/ })).toHaveCount(0);
});

test('operator with schedule.request (without schedule.manage) can submit and withdraw own pending exception without approval controls', async ({
  page,
  gateway,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.clock.install({ time: new Date(PINNED_NOW_ISO) });
  gateway.principalOverride = {
    ...principal,
    principalId: '018f3e00-2000-7000-8000-000000000099',
    principal: {
      ...principal.principal,
      subject: 'oidc-sub-operator-zhou',
      displayName: '周敏',
    },
    authorization: {
      ...principal.authorization,
      capabilities: principal.authorization.capabilities.filter((cap: string) => cap !== 'schedule.manage'),
    },
  };

  await page.goto(`/operations/schedules?site=${SITE_ID}&view=exceptions`);
  await expect(page.getByRole('button', { name: /配置默认周计划|编辑周计划/ })).toHaveCount(0);

  await page.getByRole('main').getByRole('button', { name: '申请运行例外' }).first().click();
  const excDialog = page.getByRole('dialog', { name: '要申请哪几天的运行例外？' });
  await expect(excDialog).toBeVisible();
  await expect(excDialog.getByRole('checkbox', { name: '提交并立即生效' })).toHaveCount(0);

  await excDialog.getByLabel('例外名称').fill('周末水泵检修停机申请');
  await excDialog.getByRole('combobox', { name: '例外类型' }).click();
  await page.getByRole('option', { name: '维保停机' }).click();
  await excDialog.getByLabel('开始日期（含当日）').fill('2026-10-18');
  await excDialog.getByLabel('结束日期（含当日）').fill('2026-10-18');
  await excDialog.getByLabel('申请原因').fill('冷冻水泵联轴器对中复检');
  await excDialog.getByRole('button', { name: '提交申请' }).click();
  await expect(excDialog).toBeHidden();

  const excRow = page.getByRole('row', { name: /周末水泵检修停机申请/ });
  await expect(excRow).toContainText('待审批');
  await expect(excRow).toContainText('周敏');
  await expect(excRow.getByRole('button', { name: /审批通过|驳回/ })).toHaveCount(0);

  await excRow.getByRole('button', { name: '撤回' }).click();
  const cancelDialog = page.getByRole('dialog', { name: '确认撤回该运行例外吗？' });
  await expect(cancelDialog).toBeVisible();
  await cancelDialog.getByLabel('处理备注（选填）').fill('检修改期至下周末');
  await cancelDialog.getByRole('button', { name: '撤回例外' }).click();
  await expect(cancelDialog).toBeHidden();
  await expect(excRow).toContainText('已撤回');
});

test('viewer without schedule.request or schedule.manage sees read-only schedule and exceptions without mutation controls', async ({
  page,
  gateway,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  gateway.principalOverride = {
    ...principal,
    authorization: {
      ...principal.authorization,
      capabilities: principal.authorization.capabilities.filter(
        (cap: string) => cap !== 'schedule.request' && cap !== 'schedule.manage',
      ),
    },
  };

  await page.goto(`/operations/schedules?site=${SITE_ID}`);
  await expect(page.getByRole('heading', { name: '运行计划', exact: true })).toBeVisible();
  await expect(page.getByText('只读查看')).toBeVisible();
  await expect(page.getByText('当前账号为只读权限，请联系运维主管或管理员配置周运行计划。')).toBeVisible();
  await expect(page.getByRole('button', { name: /配置默认周计划|编辑周计划|申请运行例外/ })).toHaveCount(0);

  await page.getByRole('tab', { name: /运行例外/ }).click();
  await expect(page.getByRole('button', { name: '申请运行例外' })).toHaveCount(0);
});
