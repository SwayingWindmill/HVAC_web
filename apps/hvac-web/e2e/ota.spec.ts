import { createHash } from 'node:crypto';
import { SITE_ID, principal } from './gateway';
import { captureVerifyScreenshot, expect, test } from './test';

function grantDeviceWritePrincipal() {
  return {
    ...principal,
    authorization: {
      ...principal.authorization,
      capabilities: [...principal.authorization.capabilities, 'device.write'],
    },
  };
}

async function switchTheme(page: import('@playwright/test').Page, themeLabel: '浅色' | '深色') {
  const targetScheme = themeLabel === '深色' ? 'dark' : 'light';
  await page.getByRole('radiogroup', { name: '外观' }).getByRole('radio', { name: themeLabel }).click();
  await expect
    .poll(() => page.evaluate(() => getComputedStyle(document.documentElement).colorScheme))
    .toBe(targetScheme);
  await page.evaluate(
    () =>
      new Promise<void>((resolve) => {
        requestAnimationFrame(() => requestAnimationFrame(() => resolve()));
      }),
  );
}

test('integrations OTA console supports 1440px light/dark views, multipart package upload, tag-filtered canary rollout, live sheet controls, and rollback confirmation', async ({
  page,
  gateway,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  gateway.principalOverride = grantDeviceWritePrincipal();

  await page.goto(`/settings/integrations?site=${SITE_ID}&view=ota`);
  await expect(page.getByRole('heading', { name: '集成管理' })).toBeVisible();
  await expect(page.getByRole('tab', { name: 'OTA 升级', selected: true })).toBeVisible();

  await expect(page.getByLabel('OTA 升级概览')).toContainText('其中 1 项触发失败率熔断');
  await expect(page.getByText('1.4.0 稳定基线发布（失败熔断示例）')).toBeVisible();
  await expect(page.getByText('失败率达到熔断阈值，已自动暂停')).toBeVisible();
  await expect(page.getByText('edge-gateway-1.4.0-linux-armv7l.tar.gz')).toBeVisible();

  const overflowLight = await page.evaluate(
    () => document.documentElement.scrollWidth > window.innerWidth,
  );
  expect(overflowLight).toBe(false);
  await captureVerifyScreenshot(page, 'ota-web-ui-920/ota-tab-light.png');

  // Switch to dark theme via the shell theme selector and confirm zero horizontal overflow.
  await switchTheme(page, '深色');
  const overflowDark = await page.evaluate(
    () => document.documentElement.scrollWidth > window.innerWidth,
  );
  expect(overflowDark).toBe(false);
  await captureVerifyScreenshot(page, 'ota-web-ui-920/ota-tab-dark.png');
  await switchTheme(page, '浅色');

  // Upload a new OTA package via multipart dialog (including inline validation before valid upload).
  await page.getByRole('button', { name: '上传升级包' }).click();
  const uploadDialog = page.getByRole('dialog', { name: '上传 OTA 升级包？' });
  await expect(uploadDialog).toBeVisible();

  await uploadDialog.getByRole('button', { name: '上传升级包' }).click();
  await expect(uploadDialog.getByText('请选择要上传的升级包文件')).toBeVisible();
  await expect(uploadDialog.getByText('请填写语义化版本号（如 1.5.0）')).toBeVisible();

  const payloadBytes = Buffer.from('hvac-edge-daemon-1.5.0-armv7l-binary-payload');
  const expectedSha256 = createHash('sha256').update(payloadBytes).digest('hex');

  await uploadDialog.getByLabel('安装包文件（最大 64 MiB）').setInputFiles({
    name: 'edge-gateway-1.5.0-linux-armv7l.tar.gz',
    mimeType: 'application/gzip',
    buffer: payloadBytes,
  });
  await uploadDialog.getByLabel('版本号（SemVer）').fill('1.5.0');
  await uploadDialog.getByLabel('更新说明（可选）').fill('修复串口断线重连延迟，补充 A/B OTA 回滚保护');
  await uploadDialog.getByRole('button', { name: '高级校验与外部 Ed25519 签名（可选）' }).click();
  await uploadDialog.getByLabel('最低兼容版本').fill('1.2.0');
  await uploadDialog.getByLabel('预期 SHA-256（64 位十六进制）').fill(expectedSha256);

  await uploadDialog.getByRole('button', { name: '上传升级包' }).click();
  await expect(uploadDialog).toBeHidden();
  const packagesTable = page.getByRole('table', { name: 'OTA 升级包' });
  await expect(packagesTable.getByText('edge-gateway-1.5.0-linux-armv7l.tar.gz')).toBeVisible();
  await expect(packagesTable.getByText('v1.5.0', { exact: true })).toBeVisible();

  const uploadWrite = gateway.writes.find((w) => w.method === 'POST' && w.path === '/api/v1/ota/packages');
  expect(uploadWrite).toBeTruthy();
  expect(uploadWrite?.csrfToken).toBe(principal.session.csrfToken);
  expect(uploadWrite?.body?.sha256).toBe(expectedSha256);

  // Create a canary campaign targeting current site + custom tag.
  await page.getByRole('button', { name: '发起发布任务' }).click();
  const createDialog = page.getByRole('dialog', { name: '发起灰度发布任务？' });
  await expect(createDialog).toBeVisible();

  await createDialog.getByLabel('发布任务名称').fill('松雅轩 1.5.0 守护进程灰度');
  await createDialog.getByRole('button', { name: '添加标签条件' }).click();
  await createDialog.getByLabel('标签第 1 行键名').fill('env');
  await createDialog.getByLabel('标签第 1 行值').fill('pilot');
  await createDialog.getByLabel('发布变更原因').fill('验证边缘网关 1.5.0 串口心跳与健康检查');

  await createDialog.getByRole('button', { name: '发起灰度发布' }).click();
  await expect(createDialog).toBeHidden();

  const createWrite = gateway.writes.find((w) => w.method === 'POST' && w.path === '/api/v1/ota/campaigns');
  expect(createWrite?.body?.targetTags).toEqual({ env: 'pilot' });
  expect(createWrite?.body?.targetSiteIds).toEqual([SITE_ID]);

  // Detail sheet opens automatically via ?view=ota&inspect=<campaignId>.
  await expect(page).toHaveURL(/view=ota/);
  await expect(page).toHaveURL(/inspect=/);
  const detailSheet = page.getByRole('dialog');
  await expect(detailSheet).toContainText('松雅轩 1.5.0 守护进程灰度');
  await expect(detailSheet).toContainText('灰度进行中');
  await expect(detailSheet).toContainText('EG8200-COMMERCIAL-001');
  await expect(detailSheet).toContainText('在线');
  await expect(detailSheet).toContainText('离线');

  const progressBars = detailSheet.getByRole('progressbar');
  await expect(progressBars).toHaveCount(2);
  await expect
    .poll(() =>
      detailSheet.evaluate((el) => Promise.all(el.getAnimations().map((a) => a.finished)).then(() => getComputedStyle(el).opacity)),
    )
    .toBe('1');
  await captureVerifyScreenshot(page, 'ota-web-ui-920/ota-detail-sheet-light.png');

  // Capture dark mode screenshot of campaign detail sheet cleanly via Escape -> theme toggle -> reopen.
  await page.keyboard.press('Escape');
  await expect(detailSheet).toBeHidden();
  await switchTheme(page, '深色');
  await page.getByRole('button', { name: '查看详情' }).first().click();
  await expect(detailSheet).toContainText('松雅轩 1.5.0 守护进程灰度');
  await expect(detailSheet.getByRole('progressbar')).toHaveCount(2);
  await expect
    .poll(() =>
      detailSheet.evaluate((el) => Promise.all(el.getAnimations().map((a) => a.finished)).then(() => getComputedStyle(el).opacity)),
    )
    .toBe('1');
  await captureVerifyScreenshot(page, 'ota-web-ui-920/ota-detail-sheet-dark.png');
  await page.keyboard.press('Escape');
  await expect(detailSheet).toBeHidden();
  await switchTheme(page, '浅色');
  await page.getByRole('button', { name: '查看详情' }).first().click();
  await expect(detailSheet).toContainText('松雅轩 1.5.0 守护进程灰度');

  // Pause campaign via confirmation AlertDialog.
  await detailSheet.getByRole('button', { name: '暂停发布' }).click();
  const pauseAlert = page.getByRole('alertdialog', { name: '暂停这次发布任务？' });
  await expect(pauseAlert).toBeVisible();
  await pauseAlert.getByLabel('暂停原因（可选）').fill('现场核对日志后继续');
  await pauseAlert.getByRole('button', { name: '确认暂停' }).click();
  await expect(pauseAlert).toBeHidden();
  await expect(detailSheet).toContainText('人工暂停');

  // Resume campaign -> ROLLOUT_RUNNING.
  await detailSheet.getByRole('button', { name: '恢复发布' }).click();
  const resumeAlert = page.getByRole('alertdialog', { name: '恢复这次发布任务？' });
  await expect(resumeAlert).toBeVisible();
  await resumeAlert.getByRole('button', { name: '恢复发布' }).click();
  await expect(resumeAlert).toBeHidden();
  await expect(detailSheet).toContainText('全量进行中');

  // Emergency rollback requires explicit checkbox confirmation.
  await detailSheet.getByRole('button', { name: '紧急回滚…' }).click();
  const rollbackAlert = page.getByRole('alertdialog', { name: '紧急回滚这次发布？' });
  await expect(rollbackAlert).toBeVisible();
  const confirmRollbackBtn = rollbackAlert.getByRole('button', { name: '确认回滚' });
  await expect(confirmRollbackBtn).toBeDisabled();
  await rollbackAlert.getByRole('checkbox').click();
  await expect(confirmRollbackBtn).toBeEnabled();
  await rollbackAlert.getByLabel('回滚原因（可选）').fill('演练紧急回滚确认');
  await confirmRollbackBtn.click();
  await expect(rollbackAlert).toBeHidden();
  await expect(detailSheet).toContainText('已回滚');
  await expect(detailSheet).toContainText('已取消');
});

test('integrations OTA console enforces read-only device.write gating and missing device.read denial', async ({
  page,
  gateway,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });

  // Read-only operator: has device.read, lacks device.write.
  gateway.principalOverride = {
    ...principal,
    authorization: {
      ...principal.authorization,
      capabilities: principal.authorization.capabilities.filter((cap: string) => cap !== 'device.write'),
    },
  };

  await page.goto(`/settings/integrations?site=${SITE_ID}&view=ota`);
  await expect(page.getByText('当前账号仅有查看权限，无法上传升级包或控制发布任务（暂停、放量与回滚）。')).toBeVisible();
  await expect(page.getByRole('button', { name: '上传升级包' })).toBeDisabled();
  await expect(page.getByRole('button', { name: '发起发布任务' })).toBeDisabled();

  await page.getByRole('button', { name: '查看详情' }).first().click();
  const detailSheet = page.getByRole('dialog');
  await expect(detailSheet).toContainText('已触发失败率自动熔断');
  await expect(detailSheet.getByRole('button', { name: '恢复发布' })).toHaveCount(0);
  await expect(detailSheet.getByRole('button', { name: '紧急回滚…' })).toHaveCount(0);

  // Missing device.read capability renders StateCard.
  gateway.principalOverride = {
    ...principal,
    authorization: {
      ...principal.authorization,
      capabilities: principal.authorization.capabilities.filter((cap: string) => cap !== 'device.read'),
    },
  };

  await page.goto(`/settings/integrations?site=${SITE_ID}&view=ota`);
  await expect(page.getByRole('heading', { name: '当前账号无权查看 OTA 升级记录' })).toBeVisible();
});
