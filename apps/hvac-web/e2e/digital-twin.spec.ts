import { SITE_ID } from './gateway';
import { expect, test } from './test';

// The scene renders on a software GPU in CI; with reduced motion nothing animates, as for any viewer who asks.
test.use({ reducedMotion: 'reduce' });

test('the digital twin shows the plant in the corners and keeps view and selection in the URL', async ({ page }) => {
  test.slow();
  await page.goto(`/operations/digital-twin?site=${SITE_ID}`);
  // test.slow() only lengthens the test. The canvas node appears after the WebGL context, which on a
  // busy CI runner can take longer than the shared 15s expect timeout. The HTML corners do not wait on it.
  await expect(page.locator('canvas')).toBeVisible({ timeout: 45_000 });
  await expect(page.getByText('冷站功率')).toBeVisible();
  await expect(page.getByRole('heading', { name: '冷站', exact: true })).toBeVisible();
  await page.getByRole('button', { name: '冷站', exact: true }).click();
  await expect(page).toHaveURL(/view=plant/);
  // Picking a machine in the list turns the context card to it; nothing pops up in the scene.
  await page.getByRole('button', { name: /CHILLER-01/ }).click();
  await expect(page).toHaveURL(/inspect=/);
  await expect(page.getByRole('heading', { name: 'CHILLER-01' })).toBeVisible();
});

test('a site buildingPlan from Registry drives the terminal building view and floor cuts', async ({ page, gateway }) => {
  test.slow();
  gateway.sites[0] = {
    ...gateway.sites[0],
    buildingPlan: {
      name: '测试楼宇',
      width: 20,
      depth: 14,
      floors: [
        {
          level: 1,
          label: '1F 餐厅',
          height: 4.5,
          rooms: [
            { id: '1-core', name: '楼梯', kind: 'core', x: [16, 20], z: [0, 5] },
            { id: '1-shaft', name: '管井', kind: 'shaft', x: [18, 20], z: [5, 7] },
            { id: '1-ahu', name: '空调机房', kind: 'plantRoom', x: [16, 20], z: [7, 11] },
            { id: '1-hall', name: '大厅', kind: 'hall', x: [0, 16], z: [0, 14] },
          ],
        },
        {
          level: 2,
          label: '2F 包间',
          height: 4.0,
          rooms: [
            { id: '2-core', name: '楼梯', kind: 'core', x: [16, 20], z: [0, 5] },
            { id: '2-shaft', name: '管井', kind: 'shaft', x: [18, 20], z: [5, 7] },
            { id: '2-corridor', name: '走廊', kind: 'corridor', x: [0, 18], z: [6, 8] },
            { id: '2-r1', name: '201 包间', kind: 'private', x: [0, 8], z: [0, 6] },
            { id: '2-r2', name: '202 小包间', kind: 'smallPrivate', x: [8, 16], z: [8, 14] },
          ],
        },
      ],
    },
  };

  await page.goto(`/operations/digital-twin?site=${SITE_ID}`);
  await expect(page.locator('canvas')).toBeVisible({ timeout: 45_000 });
  await page.getByRole('button', { name: '末端楼宇', exact: true }).click();
  await expect(page).toHaveURL(/view=terminal/);
  await expect(page.getByText('末端楼宇 · 测试楼宇')).toBeVisible();
  await expect(page.getByRole('tab', { name: /风机盘管/ })).toBeVisible();
  await expect(page.getByRole('tab', { name: /变风量末端/ })).toBeVisible();
  await expect(page.getByRole('tab', { name: /空调箱/ })).toBeVisible();
  await page.getByRole('button', { name: '2F', exact: true }).click();
  await expect(page).toHaveURL(/floor=2/);
  await expect(page.getByRole('tab', { name: /风机盘管/ })).toBeVisible();
  await expect(page.getByRole('tab', { name: /空调箱/ })).toBeHidden();
});
