import { readFileSync } from 'node:fs';
import type { Locator } from '@playwright/test';
import { SITE_ID } from './gateway';
import { expect, test } from './test';

async function fillTemporalField(
  input: Locator,
  dateValue: string,
  dateTimeLocalValue: string,
) {
  const inputType = await input.getAttribute('type');
  await input.fill(inputType === 'date' ? dateValue : dateTimeLocalValue);
}

const DAY_MS = 86_400_000;
const principalFixture = JSON.parse(
  readFileSync(new URL('./fixtures/principal.json', import.meta.url), 'utf8'),
);
const recordedElectricityFixture = JSON.parse(
  readFileSync(new URL('./fixtures/energy-series.json', import.meta.url), 'utf8'),
).electricity;

function buildPricedDailyElectricityHandler() {
  return (query: Record<string, unknown>) => {
    const requestedFrom = String(query.from);
    const requestedTo = String(query.to);
    const fromMs = Date.parse(requestedFrom);
    const toMs = Date.parse(requestedTo);
    const points = [];
    for (let dayMs = fromMs; dayMs < toMs; dayMs += DAY_MS) {
      points.push({
        periodStart: new Date(dayMs).toISOString(),
        periodEnd: new Date(dayMs + DAY_MS).toISOString(),
        energyKWh: 4_000,
        cost: {
          amountCNY: 3_200,
          unpricedKWh: 0,
          periods: [
            { periodCode: 'SUPER_PEAK', energyKWh: 400, amountCNY: 560 },
            { periodCode: 'PEAK', energyKWh: 1_600, amountCNY: 1_600 },
            { periodCode: 'FLAT', energyKWh: 1_200, amountCNY: 780 },
            { periodCode: 'VALLEY', energyKWh: 800, amountCNY: 260 },
          ],
        },
      });
    }
    return {
      body: {
        ...recordedElectricityFixture,
        points,
        metadata: {
          ...recordedElectricityFixture.metadata,
          requestedGranularity: 'day',
          actualGranularity: 'day',
          dataWatermark: requestedTo,
          aggregateWatermark: requestedTo,
          partial: false,
          qualitySummary: { valid: points.length * 96, suspect: 0, invalid: 0 },
        },
      },
    };
  };
}

test('budget overview, cross-month whole-building allocation, chiller-plant variance, and report sidecar work end-to-end at 1440px', async ({
  page,
  gateway,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  gateway.energySeriesHandler = buildPricedDailyElectricityHandler();
  await page.route('**/api/v1/sites/*/accounting/recalculate-jobs', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ items: [] }),
    }),
  );

  await page.goto(`/energy-analysis/billing?site=${SITE_ID}&month=2026-10`);
  await expect(page.getByRole('heading', { name: '电费账单' })).toBeVisible();

  // 1440px zero horizontal overflow check on L1 overview
  const hasOverflowOverview = await page.evaluate(
    () => document.documentElement.scrollWidth > window.innerWidth,
  );
  expect(hasOverflowOverview).toBe(false);

  // Past month 2026-10 (31 days * 3,200 CNY/day = 99,200 CNY)
  await expect(page.getByText('¥99,200.00').first()).toBeVisible();

  // 1. Set monthly electricity budget
  await page.getByRole('button', { name: '设置月度预算' }).click();
  const budgetDialog = page.getByRole('dialog', { name: '如何设置月度电费预算？' });
  await expect(budgetDialog).toBeVisible();
  await budgetDialog.getByLabel('月度电费预算（元）').fill('110000');
  await budgetDialog.getByLabel('预算口径说明（选填）').fill('10 月秋季过渡季预算');
  await budgetDialog.getByRole('button', { name: '保存月度预算' }).click();
  await expect(budgetDialog).toBeHidden();
  await expect(page.getByTestId('real-protected-shell').getByText('¥110,000.00')).toBeVisible();
  await expect(page.getByText('○ 预计在预算内')).toBeVisible();

  // Verify chart <-> table view toggle
  await page.getByRole('radio', { name: '表格' }).click();
  await expect(page.getByRole('table', { name: '逐日累计估算电费与预算参考' })).toBeVisible();
  await page.getByRole('radio', { name: '图表' }).click();

  // 2. Create WHOLE_BUILDING draft utility bill with cross-month reading dates, negative power-factor reward, and optional ticket details
  await page.getByRole('button', { name: '录入结算电费单' }).first().click();
  const createDialog = page.getByRole('dialog', { name: '如何录入供电局结算电费单？' });
  await expect(createDialog).toBeVisible();

  await createDialog.getByRole('combobox', { name: '供电局表计范围' }).click();
  await page.getByRole('option', { name: '整栋总表' }).click();

  await createDialog.getByRole('textbox', { name: '抄表起始日期' }).fill('2026-09-25');
  await createDialog.getByRole('textbox', { name: '抄表截止日期（含当日）' }).fill('2026-10-24');
  // 30 days * 4,000 kWh/day = 120,000 kWh plant energy; whole building = 400,000 kWh -> 30.00% share!
  await createDialog.getByLabel('抄表结算电量（kWh）').fill('400000');
  await createDialog.getByLabel('1. 电度电费（元）').fill('310000');
  await createDialog.getByLabel('结算最大需量（kW）').fill('1800');
  await createDialog.getByLabel('基本电费金额（元）').fill('50900');
  await createDialog.getByLabel('3. 力调电费（元，奖励填负数）').fill('-1257');
  await createDialog.getByLabel('4. 其他代征及附加（元）').fill('357');
  await createDialog
    .getByRole('button', { name: /票面峰平谷分项电量、功率因数与供电户号/ })
    .click();
  await createDialog.getByLabel('供电户号（选填）').fill('0800123456');
  await createDialog.getByLabel('考核标准功率因数（选填）').fill('0.90');
  await createDialog.getByLabel('实际功率因数（选填）').fill('0.95');
  await createDialog.getByLabel('尖峰电量（kWh）').fill('40000');
  await expect(createDialog.getByLabel('深谷电量（kWh）')).toBeVisible();
  await createDialog.getByLabel('供电局发票 / 单据号（选填）').fill('GD-202610-WB01');

  // Auto-calculated payable: 310000 + 50900 - 1257 + 357 = 360,000.00
  await expect(createDialog).toContainText('¥360,000.00');
  await createDialog.getByRole('button', { name: '保存为待确认草稿' }).click();
  await expect(createDialog).toBeHidden();

  // 3. L2 Sheet opens automatically (?inspect=<billId>) showing WHOLE_BUILDING allocation & same-window plant reference
  const sheet = page.getByRole('dialog', { name: /2026 年 10 月 · 结算电费单核对/ });
  await expect(sheet).toBeVisible();
  await expect(sheet).toContainText('含首尾共 30 天');
  await expect(sheet).toContainText('户号 0800123456');
  await expect(sheet).toContainText('实际功率因数 0.950');
  await expect(sheet).toContainText('尖峰 40,000.00');
  await expect(sheet).toContainText('-¥1,257.00（奖励减免）');
  // 120,000 / 400,000 = 30.00% -> 360,000 * 30% = 108,000.00 CNY allocated to chiller plant
  await expect(sheet).toContainText('30.00%');
  await expect(sheet).toContainText('¥108,000.00');
  await expect(sheet).toContainText('整栋综合平均电度单价 ¥0.7750/kWh');
  await expect(sheet).toContainText('计量电量 120,000.00 kWh · 分时估算电度电费 ¥96,000.00');

  // 4. Edit draft to apply a 35% finance override — verify optional fields are preserved
  await sheet.getByRole('button', { name: '编辑草稿' }).click();
  const editDialog = page.getByRole('dialog', { name: '如何修改结算电费单草稿？' });
  await expect(editDialog).toBeVisible();
  await editDialog.getByLabel('财务指定冷站分摊比例（%，选填）').fill('35');
  await editDialog.getByRole('button', { name: '保存草稿修改' }).click();
  await expect(editDialog).toBeHidden();

  // Updated allocation: 360,000 * 35% = 126,000.00 CNY, and optional ticket details remain intact
  await expect(sheet).toContainText('35.00%');
  await expect(sheet).toContainText('采用财务指定比例（实测电量占比 30.00%）');
  await expect(sheet).toContainText('¥126,000.00');
  await expect(sheet).toContainText('户号 0800123456');
  await expect(sheet).toContainText('实际功率因数 0.950');

  // 5. Confirm & archive the bill -> requires confirmation checkbox & locks editing
  await sheet.getByRole('button', { name: '确认归档' }).click();
  const confirmDialog = page.getByRole('dialog', { name: '确认归档该张结算电费单吗？' });
  await expect(confirmDialog).toBeVisible();
  await expect(confirmDialog.getByRole('button', { name: '确认归档' })).toBeDisabled();
  await confirmDialog
    .getByLabel('我已核对票面四项金额与抄表起止日期，知晓确认归档后永久锁定不可修改')
    .click();
  await confirmDialog.getByRole('button', { name: '确认归档' }).click();
  await expect(confirmDialog).toBeHidden();
  await expect(sheet).toContainText('○ 已确认归档');
  await expect(sheet.getByRole('button', { name: '编辑草稿' })).toHaveCount(0);
  await page.keyboard.press('Escape');

  // 6. Create CHILLER_PLANT dedicated bill & inspect direct variance on L3 bills tab
  await page.getByRole('tab', { name: '结算电费单核对' }).click();
  await expect(page).toHaveURL(/view=bills/);

  const hasOverflowBills = await page.evaluate(
    () => document.documentElement.scrollWidth > window.innerWidth,
  );
  expect(hasOverflowBills).toBe(false);

  await page.getByRole('button', { name: '录入结算电费单' }).click();
  const plantDialog = page.getByRole('dialog', { name: '如何录入供电局结算电费单？' });
  await plantDialog.getByRole('textbox', { name: '抄表起始日期' }).fill('2026-10-01');
  await plantDialog.getByRole('textbox', { name: '抄表截止日期（含当日）' }).fill('2026-10-30');
  // 30 days * 4,000 kWh = 120,000 kWh, 30 * 3,200 = 96,000 CNY estimated energy charge
  await plantDialog.getByLabel('抄表结算电量（kWh）').fill('121800');
  await plantDialog.getByLabel('1. 电度电费（元）').fill('97500');
  await plantDialog.getByLabel('结算最大需量（kW）').fill('600');
  await plantDialog.getByLabel('基本电费金额（元）').fill('24000');
  await plantDialog.getByLabel('供电局发票 / 单据号（选填）').fill('GD-202610-CP01');
  await plantDialog.getByRole('button', { name: '保存为待确认草稿' }).click();

  await expect(sheet).toBeVisible();
  await expect(sheet).toContainText('+1,800.00 kWh');
  await expect(sheet).toContainText('+¥1,500.00');
  await page.keyboard.press('Escape');

  // 7. Navigate to /reports for 2026-10, verify confirmed bill sidecar, and click jump link back to billing sheet
  await page.goto(`/reports?site=${SITE_ID}&month=2026-10`);
  const report = page.getByRole('article', { name: /2026 年 10 月能源报告/ });
  await expect(report).toContainText('供电局结算电费单（○ 已确认归档');
  await expect(report).toContainText('整栋总表 · 供电局抄表周期：2026-09-25 至 2026-10-24');
  await expect(report).toContainText('应收总电费 ¥360,000.00');

  await report.getByRole('link', { name: '核对详情' }).click();
  await expect(page).toHaveURL(new RegExp(`/energy-analysis/billing\\?.*site=${SITE_ID}`));
  await expect(page).toHaveURL(/view=bills/);
  await expect(sheet).toBeVisible();
  await expect(sheet).toContainText('GD-202610-WB01');
});

test('viewer without billing.manage sees read-only billing workspace and honest unpriced warning', async ({
  page,
  gateway,
}) => {
  gateway.principalOverride = {
    ...principalFixture,
    authorization: {
      ...principalFixture.authorization,
      capabilities: principalFixture.authorization.capabilities.filter(
        (cap: string) => cap !== 'billing.manage' && cap !== 'savings.manage',
      ),
    },
  };
  await page.route('**/api/v1/sites/*/accounting/recalculate-jobs', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ items: [] }),
    }),
  );

  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto(`/energy-analysis/billing?site=${SITE_ID}`);

  await expect(page.getByRole('heading', { name: '电费账单' })).toBeVisible();
  await expect(page.getByText('只读查看')).toBeVisible();
  // Default recorded energy-series fixture has 100 kWh unpriced -> honest partial unpriced banner
  await expect(page.getByText('月内部分时段用电缺少分时电价覆盖')).toBeVisible();
  await expect(page.getByRole('button', { name: /设置月度预算|调整月度预算/ })).toHaveCount(0);
  await expect(page.getByRole('button', { name: '录入结算电费单' })).toHaveCount(0);
  await expect(page.getByRole('button', { name: '重算时段数据' })).toHaveCount(0);
  await expect(page.getByTestId('accounting-recalc-status-strip')).toHaveCount(0);
});

test('savings.manage operator can trigger manual recalculation, see error feedback, and track live status strip transitions', async ({
  page,
  gateway,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  gateway.energySeriesHandler = buildPricedDailyElectricityHandler();
  gateway.principalOverride = {
    ...principalFixture,
    authorization: {
      ...principalFixture.authorization,
      capabilities: principalFixture.authorization.capabilities.filter(
        (cap: string) => cap !== 'billing.manage',
      ),
    },
  };

  let jobsPollCount = 0;
  let currentJobs: Array<Record<string, unknown>> = [];
  let failNextPost = true;

  await page.route('**/api/v1/sites/*/accounting/recalculate**', async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (request.method() === 'GET' && url.pathname.endsWith('/accounting/recalculate-jobs')) {
      if (currentJobs.length > 0) {
        jobsPollCount += 1;
        if (jobsPollCount === 2) {
          currentJobs = [{ ...currentJobs[0], status: 'RUNNING', updatedAt: '2026-10-16T04:00:03.000Z' }];
        } else if (jobsPollCount >= 3) {
          currentJobs = [
            {
              ...currentJobs[0],
              status: 'SUCCEEDED',
              updatedAt: '2026-10-16T04:00:06.000Z',
              finishedAt: '2026-10-16T04:00:06.000Z',
            },
          ];
        }
      }
      return route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ items: currentJobs }),
      });
    }
    if (request.method() === 'POST' && url.pathname.endsWith('/accounting/recalculate')) {
      if (failNextPost) {
        failNextPost = false;
        return route.fulfill({
          status: 409,
          contentType: 'application/problem+json',
          body: JSON.stringify({
            status: 409,
            code: 'ACCOUNTING_RECALC_IN_PROGRESS',
            detail: 'duplicate recalc window',
          }),
        });
      }
      const payload = request.postDataJSON() as {
        fromDate: string;
        toDate: string;
        granularity: 'HOUR' | 'DAY' | 'MONTH' | 'ALL';
        reason: string;
      };
      const job = {
        id: '01990000-1100-7000-8000-000000000001',
        tenantId: principalFixture.context.tenantId,
        siteId: SITE_ID,
        granularity: payload.granularity,
        windowStart: '2026-09-30T16:00:00.000Z',
        windowEnd: '2026-10-10T16:00:00.000Z',
        status: 'PENDING',
        availableAt: '2026-10-16T04:00:00.000Z',
        requestedBy: principalFixture.principalId,
        requestedByDisplay: '值班工程师',
        reason: payload.reason,
        source: 'MANUAL',
        attemptCount: 0,
        lastError: '',
        createdAt: '2026-10-16T04:00:00.000Z',
        updatedAt: '2026-10-16T04:00:00.000Z',
        finishedAt: null,
      };
      currentJobs = [job];
      jobsPollCount = 0;
      return route.fulfill({
        status: 202,
        contentType: 'application/json',
        body: JSON.stringify(job),
      });
    }
    return route.fallback();
  });

  await page.goto(`/energy-analysis/billing?site=${SITE_ID}&month=2026-10`);

  // savings.manage sees Recalculate button, while billing.manage actions stay hidden
  await expect(page.getByRole('button', { name: '重算时段数据' })).toBeVisible();
  await expect(page.getByRole('button', { name: /设置月度预算|调整月度预算/ })).toHaveCount(0);
  await expect(page.getByRole('button', { name: '录入结算电费单' })).toHaveCount(0);
  await expect(page.getByTestId('accounting-recalc-status-strip')).toHaveCount(0);

  // Open dialog, test inline refusal feedback, then submit successfully
  await page.getByRole('button', { name: '重算时段数据' }).click();
  const dialog = page.getByRole('dialog', { name: '要重新计算哪段时间的电费与能耗？' });
  await expect(dialog).toBeVisible();
  await dialog.getByLabel('开始日期').fill('2026-10-01');
  await dialog.getByLabel('结束日期（含当日）').fill('2026-10-10');
  await dialog.getByLabel('重算原因').fill('更新深谷分时电价后重算');
  await dialog.getByRole('button', { name: '开始重算' }).click();
  await expect(dialog.getByRole('alert')).toContainText('当前已有相同时段的重算任务正在执行');

  await dialog.getByRole('button', { name: '开始重算' }).click();
  await expect(dialog).toBeHidden();

  const strip = page.getByTestId('accounting-recalc-status-strip');
  await expect(strip).toBeVisible();
  await expect(strip).toContainText('■ 排队重算中');
  await expect(strip).toContainText('2026-10-01 至 2026-10-10');

  // Auto-polling advances PENDING -> RUNNING -> SUCCEEDED
  await expect(strip).toContainText('● 正在重算', { timeout: 6_000 });
  await expect(strip).toContainText('○ 最近重算完成', { timeout: 6_000 });
});

test('billing config tab manages site calendar holidays, enforces meter ratio overlap 409, retires active version, and creates replacement version', async ({
  page,
  gateway,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  gateway.energySeriesHandler = buildPricedDailyElectricityHandler();

  await page.goto(`/energy-analysis/billing?site=${SITE_ID}&month=2026-10`);
  await page.getByRole('tab', { name: '计量与日历配置' }).click();

  const holidayRegion = page.getByRole('region', { name: '站点节假日日历' });
  const ratioRegion = page.getByRole('region', { name: '电表 CT/PT 倍率版本' });
  await expect(holidayRegion).toBeVisible();
  await expect(ratioRegion).toBeVisible();

  // 1. Site calendar holidays: verify seed, create override, and delete with confirmation
  const holidayTable = holidayRegion.getByRole('table', { name: '节假日日历列表' });
  await expect(holidayTable).toBeVisible();
  await expect(holidayTable.getByRole('row', { name: /2026-10-01/ })).toContainText('国庆节');

  const activeRatioRow = ratioRegion.getByRole('row', { name: /生效中/ });
  await expect(activeRatioRow).toBeVisible();
  await expect(activeRatioRow).toContainText('40');

  const hasOverflowConfig = await page.evaluate(
    () => document.documentElement.scrollWidth > window.innerWidth,
  );
  expect(hasOverflowConfig).toBe(false);

  await holidayRegion.getByRole('button', { name: '新增或更新节假日' }).click();
  const holidayDialog = page.getByRole('dialog');
  await expect(holidayDialog).toBeVisible();
  await holidayDialog.getByLabel('日期', { exact: true }).fill('2026-10-18');
  await holidayDialog.getByRole('combobox', { name: '日期类型' }).click();
  await page.getByRole('option', { name: /特殊调休日/ }).click();
  await holidayDialog.getByLabel('说明').fill('秋检停机调试日');
  await holidayDialog.getByRole('button', { name: '保存节假日' }).click();
  await expect(holidayDialog).toBeHidden();

  const createdHolidayRow = holidayTable.getByRole('row', { name: /2026-10-18/ });
  await expect(createdHolidayRow).toBeVisible();
  await expect(createdHolidayRow).toContainText('秋检停机调试日');

  await createdHolidayRow.getByRole('button', { name: /删除/ }).click();
  const deleteHolidayDialog = page
    .getByRole('dialog', { name: '确认删除节假日' })
    .or(page.getByRole('alertdialog', { name: '确认删除节假日' }));
  await expect(deleteHolidayDialog).toBeVisible();
  await deleteHolidayDialog.getByRole('button', { name: '确认删除' }).click();
  await expect(deleteHolidayDialog).toBeHidden();
  await expect(holidayTable.getByRole('row', { name: /2026-10-18/ })).toHaveCount(0);

  // 2. Meter CT/PT ratios: verify active seed, trigger 409 overlap error, retire active version, then create CT=60
  await expect(activeRatioRow).toContainText('40');

  await ratioRegion.getByRole('button', { name: '新增倍率版本' }).click();
  const createRatioDialog = page.getByRole('dialog');
  await expect(createRatioDialog).toBeVisible();
  await createRatioDialog.getByRole('combobox', { name: '测点 ID' }).click();
  await page.getByRole('option', { name: /hvac meter energy/ }).click();
  await createRatioDialog.getByLabel('CT 变比').fill('60');
  await fillTemporalField(
    createRatioDialog.getByLabel('生效开始时间'),
    '2026-07-01',
    '2026-07-01T00:00',
  );
  await createRatioDialog.getByRole('button', { name: '创建倍率版本' }).click();
  await expect(createRatioDialog.getByRole('alert')).toContainText('重叠');
  await createRatioDialog.getByRole('button', { name: '取消' }).click();
  await expect(createRatioDialog).toBeHidden();

  await activeRatioRow.getByRole('button', { name: '终止生效' }).click();
  const retireDialog = page
    .getByRole('dialog', { name: '终止倍率版本' })
    .or(page.getByRole('alertdialog', { name: '终止倍率版本' }));
  await expect(retireDialog).toBeVisible();
  await fillTemporalField(
    retireDialog.getByLabel('生效结束时间'),
    '2026-10-01',
    '2026-10-01T00:00',
  );
  await retireDialog.getByRole('button', { name: '确认终止' }).click();
  await expect(retireDialog).toBeHidden();
  await expect(ratioRegion.getByRole('row', { name: /生效中/ })).toHaveCount(0);
  await expect(ratioRegion.getByRole('row', { name: /已终止/ })).toHaveCount(2);

  await ratioRegion.getByRole('button', { name: '新增倍率版本' }).click();
  await expect(createRatioDialog).toBeVisible();
  await createRatioDialog.getByRole('combobox', { name: '测点 ID' }).click();
  await page.getByRole('option', { name: /hvac meter energy/ }).click();
  await createRatioDialog.getByLabel('CT 变比').fill('60');
  await fillTemporalField(
    createRatioDialog.getByLabel('生效开始时间'),
    '2026-10-02',
    '2026-10-02T00:00',
  );
  await createRatioDialog.getByRole('button', { name: '创建倍率版本' }).click();
  await expect(createRatioDialog).toBeHidden();

  const replacementRow = ratioRegion.getByRole('row', { name: /生效中/ });
  await expect(replacementRow).toBeVisible();
  await expect(replacementRow).toContainText('60');

  // Verify CSRF tokens and recalc side effects recorded on gateway
  const holidayPut = gateway.writes.find(
    (write) => write.method === 'PUT' && write.path === `/api/v1/sites/${SITE_ID}/accounting/holidays/2026-10-18`,
  );
  expect(holidayPut?.csrfToken).toBe('e2e-csrf-token');
  expect(holidayPut?.body).toMatchObject({ dayType: 'SPECIAL_DAY', label: '秋检停机调试日' });

  const holidayDelete = gateway.writes.find(
    (write) => write.method === 'DELETE' && write.path === `/api/v1/sites/${SITE_ID}/accounting/holidays/2026-10-18`,
  );
  expect(holidayDelete?.csrfToken).toBe('e2e-csrf-token');

  const ratioCreates = gateway.writes.filter(
    (write) => write.method === 'POST' && write.path === `/api/v1/sites/${SITE_ID}/accounting/meter-ratios`,
  );
  expect(ratioCreates).toHaveLength(2);
  expect(ratioCreates[1]?.csrfToken).toBe('e2e-csrf-token');
  expect(ratioCreates[1]?.body).toMatchObject({ ctRatio: 60 });

  const ratioRetire = gateway.writes.find(
    (write) => write.method === 'POST' && write.path.endsWith('/retire'),
  );
  expect(ratioRetire?.csrfToken).toBe('e2e-csrf-token');
  expect(gateway.accountingRecalcJobs.map((job) => job.source)).toEqual([
    'RATIO_CHANGE',
    'RATIO_CHANGE',
    'TARIFF_CHANGE',
    'TARIFF_CHANGE',
  ]);
});
