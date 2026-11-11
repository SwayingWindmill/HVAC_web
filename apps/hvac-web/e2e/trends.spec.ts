import { SITE_ID } from './gateway';
import { expect, test } from './test';

test('sidebar opens 历史趋势 with default preset, unit-grouped charts, and Chinese point labels', async ({ page }) => {
  await page.goto(`/operations/realtime?site=${SITE_ID}`);
  await page.getByRole('link', { name: '历史趋势' }).click();
  await expect(page).toHaveURL(/\/operations\/trends/);

  await expect(page.getByRole('heading', { name: '历史趋势' })).toBeVisible();
  await expect(page.getByRole('radio', { name: '冷站功率与供回水' })).toHaveAttribute('aria-checked', 'true');

  // Separate physical unit lanes: kW and °C
  await expect(page.getByRole('region', { name: /功率 \/ 负荷 \(kW\)/ })).toBeVisible();
  await expect(page.getByRole('region', { name: /温度 \/ 温差 \(°C\)/ })).toBeVisible();

  // Chinese domain labels, never raw English source keys
  await expect(page.getByText('中央空调总功率').first()).toBeVisible();
  await expect(page.getByText('供水温度').first()).toBeVisible();
  await expect(page.getByText('chiller leaving chilled water temperature')).toHaveCount(0);
});

test('window presets, 1-minute granularity, previous-day comparison, and URL sharing stay in sync', async ({ page }) => {
  await page.goto(`/operations/trends?site=${SITE_ID}`);

  await page.getByRole('radiogroup', { name: '时间范围' }).getByRole('radio', { name: '1 小时' }).click();
  await expect(page).toHaveURL(/window=1h/);
  await expect(page.getByRole('radiogroup', { name: '聚合粒度' }).getByRole('radio', { name: '1 分钟' })).toHaveAttribute('aria-checked', 'true');

  const compareToggle = page.getByRole('switch', { name: '对比昨日同时段' });
  await compareToggle.click();
  await expect(page).toHaveURL(/compare=prev/);

  await page.getByRole('radio', { name: '水泵流量与温差' }).click();
  await expect(page).toHaveURL(/preset=pump-hydraulics/);
  await expect(page.getByRole('region', { name: /流量 \(m³\/h\)/ })).toBeVisible();
});

test('switching to 统计表 displays latest, average, extremes with timestamps, and coverage ratio', async ({ page }) => {
  await page.goto(`/operations/trends?site=${SITE_ID}&window=1h`);

  await page.getByRole('radio', { name: '统计表' }).click();
  await expect(page).toHaveURL(/view=table/);

  const table = page.getByRole('table', { name: '趋势统计表' });
  await expect(table).toBeVisible();
  await expect(table.getByRole('columnheader', { name: '最新值' })).toBeVisible();
  await expect(table.getByRole('columnheader', { name: '平均值' })).toBeVisible();
  await expect(table.getByRole('columnheader', { name: '最小值' })).toBeVisible();
  await expect(table.getByRole('columnheader', { name: '最大值' })).toBeVisible();
  await expect(table.getByRole('columnheader', { name: '覆盖率' })).toBeVisible();
  await expect(table.getByRole('row', { name: /中央空调总功率/ })).toContainText(/100%/);
});

test('point picker enforces the 8-point cap and disables unchecked points at capacity', async ({ page }) => {
  await page.goto(`/operations/trends?site=${SITE_ID}`);

  await page.getByRole('button', { name: /点位 \(\d+\/8\)/ }).click();
  const dialog = page.getByRole('dialog');
  await expect(dialog).toBeVisible();

  // Select unchecked points until reaching 8/8
  while (true) {
    const statusText = await dialog.getByText(/已选 \d+ \/ 8/).textContent();
    if (statusText?.includes('已选 8 / 8')) break;
    const nextUnchecked = dialog.locator('[role="checkbox"][aria-checked="false"]:not([disabled])').first();
    await nextUnchecked.click();
  }

  await expect(dialog.getByText('已选 8 / 8')).toBeVisible();
  const remainingUnchecked = dialog.locator('[role="checkbox"][aria-checked="false"]');
  await expect(remainingUnchecked.first()).toBeDisabled();
});
