import { mkdirSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { SITE_ID, principal } from './gateway';
import { expect, test } from './test';

const P1_SCREENSHOT_DIR = join(
  dirname(fileURLToPath(import.meta.url)),
  '..',
  '..',
  '..',
  '.scratch',
  'verify',
  'p1-web-governance',
);

function p1Shot(fileName: string): string {
  mkdirSync(P1_SCREENSHOT_DIR, { recursive: true });
  return join(P1_SCREENSHOT_DIR, fileName);
}

function grantRegistryAdminPrincipal() {
  return {
    ...principal,
    authorization: {
      ...principal.authorization,
      capabilities: [
        ...principal.authorization.capabilities,
        'site.write',
        'space.write',
        'asset.write',
        'device.write',
        'point.write',
        'binding.write',
        'template.manage',
        'registry.retire',
      ],
    },
  };
}

test('asset and point registration opens from the system overview', async ({ page }) => {
  await page.goto('/system');
  await page.getByRole('tab', { name: '设备与点位登记', exact: true }).click();

  await expect(page).toHaveURL(/tab=registry/);
  await expect(page.getByRole('tabpanel')).toContainText('资源与绑定');
});

test('opening system management from a Site page leaves the Site scope', async ({ page }) => {
  await page.goto(`/operations/alarms?site=${SITE_ID}`);
  await expect(page.getByTestId('real-shell-site')).toHaveText('中央机房');

  await page.getByRole('button', { name: new RegExp(principal.principal.displayName) }).click();
  await page.getByRole('menuitem', { name: '系统管理', exact: true }).click();
  await expect(page).toHaveURL(/\/system$/);
  await expect(page.getByTestId('real-shell-site')).toHaveText('平台范围');
});

test('leaving system management with an unsaved registry import asks before dropping it', async ({ page }) => {
  await page.route('**/api/v1/principal', (route) => route.fulfill({ json: {
    ...principal, authorization: { ...principal.authorization, capabilities: [...principal.authorization.capabilities, 'registry.import'] },
  } }));
  await page.goto('/system?tab=registry');
  await page.getByRole('tab', { name: '导入与导出', exact: true }).click();
  const source = page.getByRole('textbox', { name: '登记导入 JSON', exact: true });
  await source.fill('{"draft": true}');
  await expect(page.getByText('有未保存修改')).toBeVisible();

  // Switching the page's tab unmounts the form, so it is guarded like leaving the page.
  await page.getByRole('tab', { name: '系统概览', exact: true }).click();
  const confirmation = page.getByRole('alertdialog');
  await expect(confirmation).toContainText('离开会丢弃未保存内容');
  await expect(confirmation).toContainText('站点与设备登记管理');
  await confirmation.getByRole('button', { name: '继续编辑', exact: true }).click();
  await expect(page).toHaveURL(/tab=registry/);
  await expect(source).toHaveValue('{"draft": true}');

  // Away from its pages the 运维 group is folded; open it to reach 告警.
  await page.getByRole('button', { name: '运维，展开', exact: true }).click();
  await page.getByRole('link', { name: '告警', exact: true }).click();
  await expect(page.getByRole('alertdialog')).toContainText('站点与设备登记管理');
  await page.getByRole('alertdialog').getByRole('button', { name: '继续编辑', exact: true }).click();
  await expect(page).toHaveURL(/\/system\?tab=registry/);

  await page.getByRole('tab', { name: '系统概览', exact: true }).click();
  await page.getByRole('alertdialog').getByRole('button', { name: '丢弃并离开', exact: true }).click();
  await expect(page).not.toHaveURL(/tab=registry/);
  await expect(page.getByRole('region', { name: '系统概况' })).toBeVisible();
});

// #597: the operations lead reads, per day, how the assistant's answers went and which steps
// failed that day, every step named as the panel names it.
test('the AI 助手 tab shows each day of answers and that day\'s failed steps in plain words', async ({ page }) => {
  await page.route('**/api/v1/principal', (route) => route.fulfill({ json: {
    ...principal,
    authorization: { ...principal.authorization, capabilities: [...principal.authorization.capabilities, 'audit.read'] },
  } }));
  await page.goto('/system?tab=assistant');
  const usage = page.getByRole('tabpanel');
  await expect(usage).toContainText('最近 14 天回答 50 次，其中 3 次没答完');

  const days = usage.getByRole('table', { name: '每天的回答' });
  await expect(days.getByRole('row', { name: /10月18日/ })).toContainText('出错 1');
  await expect(days.getByRole('row', { name: /10月17日/ })).toContainText('手动停止 1');
  const steps = usage.getByRole('table', { name: '查询调用明细' });
  await expect(steps).toContainText('写结论');
  await days.getByRole('button', { name: '10月16日' }).click();
  await expect(usage).toContainText('10月16日的查询调用');
  await expect(steps.getByRole('row', { name: /读取当前读数/ })).toHaveText(/读取当前读数\s*16\s*2/);
  await expect(steps.getByRole('row', { name: /读取告警/ })).toHaveText(/读取告警\s*8\s*0/);
  await days.getByRole('button', { name: '10月18日' }).click();
  await expect(steps.getByRole('row', { name: /读取告警/ })).toHaveText(/读取告警\s*7\s*1/);
  // #624: why steps failed, in words; never the codes.
  await expect(steps.getByRole('row', { name: /读取告警/ })).toContainText('数据暂时读不到 1');
  await expect(usage).not.toContainText(/alarm_list|telemetry_get_current|investigation_complete|TOOL_|FINDING_/);
});

test('the AI 助手 tab says so when the account cannot read records', async ({ page }) => {
  await page.goto('/system?tab=assistant');
  await expect(page.getByRole('tabpanel')).toContainText('当前账号没有查看操作记录的权限');
});

test('站点与组织 shows authorized site summaries, registered device counts, zero overflow at 1440px, and links to 设备与点位登记', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto('/system?tab=site');

  const summary = page.getByRole('region', { name: '站点概况' });
  await expect(summary).toContainText('1个');
  await expect(summary).toContainText('1 个已启用');
  await expect(summary).toContainText('现场网关1个');
  await expect(summary).toContainText('8台');
  await expect(summary).toContainText('已登记的现场设备');
  await expect(summary).toContainText('已同步');

  const table = page.getByRole('table', { name: '授权站点' });
  const row = table.getByRole('row', { name: /中央机房/ });
  await expect(row).toContainText('central-plant');
  await expect(row).toContainText('UTC+8');
  await expect(row).toContainText('32,000 m²');
  await expect(row).toContainText('夏热冬冷地区');
  await expect(row).toContainText('8 台');
  await expect(row).toContainText('已启用');

  const hasOverflow = await page.evaluate(
    () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
  );
  expect(hasOverflow).toBe(false);

  await row.getByRole('button', { name: '设备与点位登记' }).click();
  await expect(page).toHaveURL(/tab=registry/);
  await expect(page.getByRole('tabpanel')).toContainText('资源与绑定');
});

test('操作记录 refuses to query or search when the account lacks audit.read', async ({ page, gateway }) => {
  await page.goto('/system?tab=audit');
  const panel = page.getByRole('tabpanel');
  await expect(panel).toContainText('当前账号没有查看操作记录的权限');
  await expect(panel.getByRole('textbox', { name: '搜索操作记录', exact: true })).toBeDisabled();
  expect(gateway.writes.filter((write) => write.path === '/api/v1/audit/search')).toHaveLength(0);
});

test('操作记录 renders translated audit facts with category filters, zero raw codes, zero overflow at 1440px, and supports search', async ({ page, gateway }) => {
  gateway.principalOverride = {
    ...principal,
    authorization: { ...principal.authorization, capabilities: [...principal.authorization.capabilities, 'audit.read'] },
  };
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto('/system?tab=audit');

  const panel = page.getByRole('tabpanel');
  const table = panel.getByRole('table', { name: '操作记录' });
  await expect(table).toBeVisible();

  const auditQueries = () => gateway.writes.filter((write) => write.path === '/api/v1/audit/search');
  expect(auditQueries().map((write) => write.body)).toEqual([{ limit: 100 }]);

  const bodyRows = table.locator('tbody tr');
  await expect(bodyRows).toHaveCount(9);
  await expect(bodyRows.nth(0)).toContainText(/小程序.*王值班.*下发控制指令.*远程控制.*1号冷水机组温度调节.*温度设定 23\.5℃.*成功/);
  await expect(bodyRows.nth(1)).toContainText(/网页.*张主管.*切换站点控制模式.*控制锁定.*松雅轩冷站.*模式：本地手动.*成功/);
  await expect(bodyRows.nth(4)).toContainText(/网页.*值班工程师.*变更成员状态.*成员权限.*李值班.*状态：启用 → 停用.*成功/);
  await expect(bodyRows.nth(6)).toContainText(/值班工程师.*撤销会话.*登录会话.*成功/);
  await expect(bodyRows.nth(7)).toContainText(/其他账号.*未识别的操作.*其他对象.*权限校验未通过.*拒绝/);
  await expect(bodyRows.nth(8)).toContainText(/值班工程师.*登录.*登录会话.*成功/);
  await expect(panel).not.toContainText(
    /[0-9a-f]{8}-[0-9a-f]{4}-|SESSION_|bff-session|SUCCEEDED|DENIED|REJECTED|SOMETHING_NEW|mystery|MINI_PROGRAM|BROWSER|remote-command|control-lock|operating-schedule|registry-mutation|iam-admin|gateway-credential/,
  );

  const hasOverflow = await page.evaluate(
    () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
  );
  expect(hasOverflow).toBe(false);

  const categoryFilter = panel.getByRole('radiogroup', { name: '按业务域筛选操作记录' });
  await categoryFilter.getByRole('radio', { name: '运行与锁定', exact: true }).click();
  await expect(bodyRows).toHaveCount(2);
  await expect(bodyRows.nth(0)).toContainText(/切换站点控制模式/);
  await expect(bodyRows.nth(1)).toContainText(/更新运行计划.*时段 3 条/);
  expect(auditQueries().at(-1)?.body).toEqual({ limit: 100, category: 'control-schedule' });

  await categoryFilter.getByRole('radio', { name: '设备登记', exact: true }).click();
  await expect(bodyRows).toHaveCount(1);
  await expect(bodyRows.first()).toContainText(/小程序.*王值班.*变更设备档案.*2号离心机.*变更字段：名称、状态/);

  await categoryFilter.getByRole('radio', { name: '全部', exact: true }).click();
  await expect(bodyRows).toHaveCount(9);

  const search = panel.getByRole('textbox', { name: '搜索操作记录', exact: true });
  await search.fill('撤销会话');
  await expect(bodyRows).toHaveCount(1);
  await expect(bodyRows.first()).toContainText('撤销会话');

  await search.fill('不存在的词');
  await expect(panel).toContainText('没有匹配的操作记录');
});

test('模板版本 persists template catalog, structured DEVICE activation builder, and assignment history across reloads at 1440px', async ({
  page,
  gateway,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  gateway.principalOverride = grantRegistryAdminPrincipal();
  await page.goto('/system?tab=registry');

  await page.getByRole('tab', { name: '模板版本', exact: true }).click();
  await expect(page.getByText('chiller-standard').first()).toBeVisible();

  await page.getByRole('button', { name: '预览' }).first().click();
  await expect(page.getByText('标准点位 1 项')).toBeVisible();

  // Release a structured DEVICE template with standardPoints + capabilityProfiles.
  await page.getByLabel('模板名称').fill('chiller-v2-std');
  await page.getByLabel('适用设备类型（deviceType，可选）').fill('CHILLER');
  await page.getByRole('button', { name: '新增标准点位' }).click();
  await page.getByLabel('点位编码 (pointCode)').fill('chw_return_temp');
  await page.getByLabel('显示名称').fill('冷冻水回水温度');
  await page.getByLabel('单位（可选）').fill('degC');

  await page.getByRole('button', { name: '新增控制能力' }).click();
  await page.getByLabel('能力名称').fill('set_chw_return_limit');
  await page.getByRole('button', { name: '发布新版本' }).click();

  await expect(page.getByText('chiller-v2-std').first()).toBeVisible();

  // Assign the newly released revision to CHILLER-01.
  await page.getByRole('combobox', { name: '选择应用对象' }).click();
  await page.getByRole('option', { name: /CHILLER-01/ }).click();
  await page.getByRole('button', { name: '从现在起应用' }).click();

  const assignmentsTable = page.getByRole('table', { name: '站点已应用模板' });
  await expect(assignmentsTable).toContainText('CHILLER-01');
  await expect(assignmentsTable).toContainText('chiller-v2-std · 第 1 版');
  expect(
    gateway.writes.find((write) => write.method === 'POST' && write.path === '/api/v1/templates/revisions')?.csrfToken,
  ).toBe('e2e-csrf-token');
  expect(
    gateway.writes.find((write) => write.method === 'POST' && write.path === '/api/v1/templates/assignments')?.csrfToken,
  ).toBe('e2e-csrf-token');
  await page.screenshot({ path: p1Shot('p1-templates-light.png') });

  // Reload page to verify persistent catalog and persistent site assignment history.
  await page.reload();
  await page.getByRole('tab', { name: '模板版本', exact: true }).click();
  await expect(page.getByText('chiller-v2-std').first()).toBeVisible();
  await expect(page.getByRole('table', { name: '站点已应用模板' })).toContainText('CHILLER-01');

  await page.getByRole('radiogroup', { name: '外观' }).getByRole('radio', { name: '深色' }).click();
  await expect
    .poll(() => page.evaluate(() => getComputedStyle(document.documentElement).colorScheme))
    .toBe('dark');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await page.screenshot({ path: p1Shot('p1-templates-dark.png') });
});

test('资源与绑定 supports tag CRUD, spatial subtree filter, tag filter, and cross-site search at 1440px', async ({
  page,
  gateway,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  gateway.principalOverride = grantRegistryAdminPrincipal();
  const crossSiteId = '01910000-8000-7000-8000-000000000002';
  gateway.addCrossSiteSample(
    {
      id: crossSiteId,
      tenantId: '018f3d00-0000-7000-8000-000000000001',
      code: 'south-wing',
      displayName: '南区制冷站',
      timezone: 'Asia/Shanghai',
      status: 'ACTIVE',
      floorAreaM2: 18000,
      climateZone: 'HOT_SUMMER_COLD_WINTER',
      revision: 1,
      createdAt: '2026-10-01T00:00:00.000Z',
      updatedAt: '2026-10-01T00:00:00.000Z',
    },
    undefined,
    {
      id: '01910000-8000-7000-8000-000000000022',
      tenantId: '018f3d00-0000-7000-8000-000000000001',
      siteId: crossSiteId,
      code: 'south-chiller-01',
      displayName: '南区 1 号冷机',
      deviceType: 'CHILLER_CONTROLLER',
      status: 'ACTIVE',
      tags: { vendor: 'trane' },
      revision: 1,
      createdAt: '2026-10-01T00:00:00.000Z',
      updatedAt: '2026-10-01T00:00:00.000Z',
    },
  );

  await page.goto('/system?tab=registry');
  await page.getByRole('tab', { name: /设备 / }).click();

  const deviceTable = page.getByRole('table', { name: '设备' });
  const chillerRow = deviceTable.getByRole('row', { name: /CHILLER-01/ });
  await chillerRow.getByRole('button', { name: '编辑' }).click();

  const editDialog = page.getByRole('dialog', { name: '编辑设备' });
  await editDialog.getByRole('button', { name: '添加标签' }).click();
  await editDialog.getByLabel('标签键 1').fill('vendor');
  await editDialog.getByLabel('标签值 1').fill('carrier');
  await editDialog.getByRole('button', { name: '保存' }).click();
  await expect(editDialog).toHaveCount(0);
  await expect(chillerRow).toContainText('vendor:carrier');
  expect(
    gateway.writes.find((write) => write.method === 'PATCH' && write.path.includes('/devices/'))?.csrfToken,
  ).toBe('e2e-csrf-token');

  // Filter by key:value tag and verify non-matching rows disappear.
  await page.getByRole('textbox', { name: '按标签筛选' }).fill('vendor:carrier');
  await page.getByRole('button', { name: '筛选标签' }).click();
  await expect(deviceTable).toContainText('CHILLER-01');
  await expect(deviceTable).not.toContainText('WEATHER-STATION');
  await page.screenshot({ path: p1Shot('p1-registry-tags-light.png') });

  // Reset, filter by parent space subtree, then switch to cross-site scope.
  await page.getByRole('button', { name: '重置筛选' }).click();
  await page.getByRole('button', { name: /商业办公楼/ }).click();
  await expect(page.getByText('当前空间范围：商业办公楼')).toBeVisible();
  await expect(deviceTable).toContainText('CHILLER-01');

  await page.getByRole('radio', { name: '全部授权站点' }).click();
  await expect(deviceTable).toContainText('南区制冷站');
  await expect(deviceTable).toContainText('南区 1 号冷机');
  await expect(deviceTable).toContainText('vendor:trane');

  await page.getByRole('radiogroup', { name: '外观' }).getByRole('radio', { name: '深色' }).click();
  await expect
    .poll(() => page.evaluate(() => getComputedStyle(document.documentElement).colorScheme))
    .toBe('dark');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await page.screenshot({ path: p1Shot('p1-registry-tags-dark.png') });
});

test('logical device hardware replacement preserves device identity and telemetry points at 1440px', async ({
  page,
  gateway,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  gateway.principalOverride = grantRegistryAdminPrincipal();

  await page.goto('/system?tab=registry');
  await page.getByRole('tab', { name: /设备 / }).click();

  const deviceTable = page.getByRole('table', { name: '设备' });
  const chillerRow = deviceTable.getByRole('row', { name: /CHILLER-01/ });
  await chillerRow.getByRole('button', { name: '更换硬件' }).click();

  const replaceDialog = page.getByRole('dialog', { name: '更换这台设备的现场硬件？' });
  await expect(replaceDialog).toBeVisible();
  await expect(replaceDialog).toContainText('保留原有设备与全部点位');
  await expect(replaceDialog).not.toContainText('018f3e00-4000-7000-8000-000000000001');

  await replaceDialog.getByLabel('新硬件序列号 / 外部标识').fill('SN-CHILLER-NEW-2026');
  await replaceDialog.getByLabel('上级接入网关').click();
  await page.getByRole('option', { name: 'EG8200-COMMERCIAL-001 (eg8200-commercial-001)' }).click();
  await replaceDialog.getByLabel('新采集源标识（可选）').fill('chiller01_meter_b');
  await replaceDialog.getByLabel('通信地址或端口备注（可选）').fill('Modbus 从站 03 / RS485-1');
  await replaceDialog.getByLabel('更换原因').fill('现场主控网关主板故障更换');
  await replaceDialog.getByRole('button', { name: '确认更换硬件' }).click();

  await expect(replaceDialog).toHaveCount(0);
  await expect(page.getByText(/已完成「CHILLER-01 通信端点」的硬件更换/)).toBeVisible();
  await expect(chillerRow).toContainText('硬件标识: SN-CHILLER-NEW-2026');

  const write = gateway.writes.find(
    (entry) =>
      entry.method === 'POST'
      && entry.path === `/api/v1/sites/${SITE_ID}/devices/018f3e00-4000-7000-8000-000000000001/replace-hardware`,
  );
  expect(write?.csrfToken).toBe('e2e-csrf-token');
  expect(write?.body?.newExternalId).toBe('SN-CHILLER-NEW-2026');
  expect(write?.body?.newGatewayDeviceId).toBe('018f3e00-4000-7000-8000-000000000100');
  expect(write?.body?.newSourceKey).toBe('chiller01_meter_b');

  // Verify the same logical device still exposes its registered telemetry points.
  await chillerRow.getByRole('button', { name: '查看点位' }).click();
  await expect(page.getByRole('table', { name: 'CHILLER-01 通信端点点位登记' })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await page.screenshot({ path: p1Shot('p1-hardware-replace-light.png') });

  // Reload to confirm the updated external hardware identifier persists on the same device.
  await page.reload();
  await page.getByRole('tab', { name: /设备 / }).click();
  await expect(deviceTable.getByRole('row', { name: /CHILLER-01/ })).toContainText('硬件标识: SN-CHILLER-NEW-2026');

  await page.getByRole('radiogroup', { name: '外观' }).getByRole('radio', { name: '深色' }).click();
  await expect
    .poll(() => page.evaluate(() => getComputedStyle(document.documentElement).colorScheme))
    .toBe('dark');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await page.screenshot({ path: p1Shot('p1-hardware-replace-dark.png') });
});

test('sidebar platform links omit the site query param and drop site scope cleanly', async ({ page }) => {
  await page.goto(`/operations/alarms?site=${SITE_ID}`);
  await expect(page.getByTestId('real-shell-site')).toHaveText('中央机房');

  await page.getByRole('link', { name: '系统管理', exact: true }).click();
  await expect(page).toHaveURL(/\/system$/);
  await expect(page.getByTestId('real-shell-site')).toHaveText('平台范围');

  await page.goto(`/operations/alarms?site=${SITE_ID}`);
  await page.getByRole('button', { name: '能源，展开', exact: true }).click();
  await page.getByRole('link', { name: '站点对标', exact: true }).click();
  await expect(page).toHaveURL(/\/benchmark$/);
  await expect(page.getByTestId('real-shell-site')).toHaveText('平台范围');

  await page.goto(`/operations/alarms?site=${SITE_ID}`);
  await page.getByRole('link', { name: '集成管理', exact: true }).click();
  await expect(page).toHaveURL(/\/settings\/integrations$/);
  await expect(page.getByTestId('real-shell-site')).toHaveText('平台范围');
});

test('shell sidebar and header skip alarm and work-order queries when capabilities are absent', async ({ page, gateway }) => {
  const deniedCapabilities = principal.authorization.capabilities.filter(
    (capability: string) => capability !== 'alarm.list' && capability !== 'work-order.list',
  );
  gateway.principalOverride = {
    ...principal,
    authorization: {
      ...principal.authorization,
      capabilities: deniedCapabilities,
    },
  };

  const alarmOrWorkOrderRequests: string[] = [];
  page.on('request', (request) => {
    const pathname = new URL(request.url()).pathname;
    if (pathname.endsWith('/alarms') || pathname.endsWith('/work-orders')) {
      alarmOrWorkOrderRequests.push(pathname);
    }
  });

  await page.goto(`/overview?site=${SITE_ID}`);
  await expect(page.getByTestId('real-protected-shell')).toBeVisible();
  await page.waitForTimeout(400);
  expect(alarmOrWorkOrderRequests).toEqual([]);
});
