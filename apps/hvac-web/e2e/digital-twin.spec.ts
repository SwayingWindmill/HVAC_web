import { SITE_ID } from './gateway';
import { expect, test } from './test';

// The scene renders on a software GPU in CI; with reduced motion nothing animates, as for any viewer who asks.
test.use({ reducedMotion: 'reduce' });

test('the digital twin shows the plant in the corners and keeps view and selection in the URL', async ({ page }) => {
  test.slow();
  await page.goto(`/operations/digital-twin?site=${SITE_ID}`);
  await expect(page.locator('canvas')).toBeVisible();
  await expect(page.getByText('冷站功率')).toBeVisible();
  await expect(page.getByRole('heading', { name: '冷站', exact: true })).toBeVisible();
  await page.getByRole('button', { name: '冷站', exact: true }).click();
  await expect(page).toHaveURL(/view=plant/);
  // Picking a machine in the list turns the context card to it; nothing pops up in the scene.
  await page.getByRole('button', { name: /CHILLER-01/ }).click();
  await expect(page).toHaveURL(/inspect=/);
  await expect(page.getByRole('heading', { name: 'CHILLER-01' })).toBeVisible();
});
