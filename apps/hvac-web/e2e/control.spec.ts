import { SITE_ID } from './gateway';
import { expect, test } from './test';

test('an operator sets the chilled water setpoint from the device row, confirmed before it is sent', async ({ page, gateway }) => {
  await page.goto(`/operations/control?site=${SITE_ID}`);

  await page.getByRole('button', { name: '调整 CHILLER-01 的设定值' }).click();
  const settings = page.getByRole('dialog', { name: '调整 CHILLER-01 的设定值' });
  const setpointInput = settings.getByRole('textbox', { name: '冷冻水出水设定温度目标值' });
  await setpointInput.fill('7.5');

  // Confirming an IME candidate with Enter (including Safari's post-compositionend keyCode 229) must not submit.
  await setpointInput.evaluate((element) => {
    element.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', code: 'Enter', keyCode: 229, isComposing: true, bubbles: true, cancelable: true }));
    element.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', code: 'Enter', keyCode: 229, isComposing: false, bubbles: true, cancelable: true }));
  });
  await expect(page.getByRole('alertdialog')).toHaveCount(0);

  await setpointInput.press('Enter');
  const confirm = page.getByRole('alertdialog');
  await expect(confirm).toContainText('调整 CHILLER-01 的冷冻水出水设定温度？');
  await expect(confirm).toContainText('7.5 °C');
  await expect(confirm).toContainText('可设范围');
  await expect(confirm.getByRole('textbox', { name: '动态口令' })).toHaveCount(0);
  await confirm.getByRole('button', { name: '确认下发' }).click();

  const log = page.getByRole('list', { name: '指令记录' });
  await expect(log.getByRole('listitem').first()).toContainText('CHILLER-01 · 冷冻水出水设定温度 → 7.5 °C');
  await expect(log.getByRole('listitem').first()).toContainText('已执行');
  const submitted = gateway.writes.find((write) => write.path === '/api/v1/commands');
  expect(submitted?.body).toEqual({ assetId: expect.any(String), commandPointId: expect.any(String), parameters: { setpointC: 7.5 } });
  expect(submitted?.csrfToken).toBeTruthy();
});

test('stopping a pump asks for the one-time code and a site check, and approving it asks to verify identity again', async ({ page, gateway }) => {
  await page.goto(`/operations/control?site=${SITE_ID}`);

  await page.getByRole('group', { name: 'CHWP-01' }).getByRole('button', { name: '停止' }).click();
  const confirm = page.getByRole('alertdialog', { name: '停止 CHWP-01？' });
  const submit = confirm.getByRole('button', { name: '提交停止' });
  await expect(submit).toBeDisabled();
  await confirm.getByRole('textbox', { name: '动态口令' }).fill('12a3456');
  await expect(confirm.getByRole('textbox', { name: '动态口令' })).toHaveValue('123456');
  await expect(submit).toBeDisabled();
  await confirm.getByRole('checkbox', { name: '我已和现场确认可以停' }).check();
  await submit.click();

  const log = page.getByRole('list', { name: '指令记录' });
  const stop = log.getByRole('listitem').filter({ hasText: '停止冷冻水泵' });
  await expect(stop).toContainText('待审批 · 高风险 · 需一人审批 · 0/1');
  await expect(page.getByRole('group', { name: 'CHWP-01' })).toContainText('待审批');
  const submitted = gateway.writes.find((write) => write.path === '/api/v1/commands');
  expect(submitted?.body).toMatchObject({ parameters: {}, verificationCode: '123456' });

  // The session has no fresh two-step sign-in: the approval is refused and the page offers to verify.
  await stop.getByRole('button', { name: '批准' }).click();
  const stepUp = page.getByRole('dialog', { name: '批准前要再验证一次身份' });
  await expect(stepUp).toContainText('CHWP-01 · 停止冷冻水泵');
  await stepUp.getByRole('button', { name: '取消' }).click();

  gateway.stepUpFresh = true;
  await stop.getByRole('button', { name: '批准' }).click();
  await expect(stop).toContainText('已执行');

  await stop.getByRole('button', { name: /停止冷冻水泵/ }).click();
  await expect(page).toHaveURL(new RegExp(`inspect=${gateway.commands.find((command) => command.capability === 'STOP')!.commandId}`));
  const sheet = page.getByRole('dialog');
  await expect(sheet).toContainText('需一人审批 · 1/1');
  await expect(sheet.getByRole('listitem')).toHaveText([/已提交/, /待审批/, /已批准/, /已执行/]);
});

test('switching to manual control withdraws the pending command, pauses every device and shows in the top bar', async ({ page, gateway }) => {
  await page.goto(`/operations/control?site=${SITE_ID}`);

  await page.getByRole('group', { name: 'CT-01' }).getByRole('button', { name: '停止' }).click();
  const confirm = page.getByRole('alertdialog');
  await confirm.getByRole('textbox', { name: '动态口令' }).fill('654321');
  await confirm.getByRole('checkbox').check();
  await confirm.getByRole('button', { name: '提交停止' }).click();
  await expect(page.getByRole('list', { name: '指令记录' }).getByRole('listitem').first()).toContainText('待审批');

  const bar = page.getByRole('region', { name: '控制方式' });
  await expect(bar).toContainText('远程自动控制');
  await bar.getByRole('button', { name: '人工接管' }).click();
  const dialog = page.getByRole('dialog', { name: '切换到人工接管？' });
  await expect(dialog).toContainText('切换后会撤回 1 条还没下发的指令');
  await expect(dialog).toContainText('CT-01 · 停止冷却塔');
  const switchButton = dialog.getByRole('button', { name: '切换到人工接管' });
  await expect(switchButton).toBeDisabled();
  await dialog.getByRole('textbox', { name: '原因' }).fill('夜班人工巡检');
  await switchButton.click();

  await expect(page.getByText('已切换到人工接管，撤回了 1 条指令')).toBeVisible();
  const write = gateway.writes.find((entry) => entry.path === `/api/v1/sites/${SITE_ID}/control-mode`);
  expect(write?.body).toEqual({ controlMode: 'MANUAL_OVERRIDE', reason: '夜班人工巡检', expectedRevision: 1 });
  await expect(bar).toContainText('人工接管');
  await expect(bar).toContainText('原因：夜班人工巡检');
  await expect(page.getByRole('banner').getByRole('link', { name: '人工接管' })).toBeVisible();
  await expect(page.getByRole('group', { name: 'CHWP-01' }).getByRole('button', { name: '停止' })).toBeDisabled();
  await expect(page.getByRole('list', { name: '指令记录' }).getByRole('listitem').first()).toContainText('已撤回');

  // Safe exit waits for a command nobody has confirmed on site.
  gateway.commands[0].status = 'OUTCOME_UNKNOWN';
  await page.getByRole('button', { name: '刷新' }).click();
  await bar.getByRole('button', { name: '安全退出' }).click();
  const exit = page.getByRole('dialog', { name: '切换到安全退出？' });
  await expect(exit).toContainText('现在不能安全退出');
  await exit.getByRole('textbox', { name: '原因' }).fill('交回现场');
  await expect(exit.getByRole('button', { name: '切换到安全退出' })).toBeDisabled();
});

test('locking a device withdraws its pending command and greys its controls until unlocked', async ({ page, gateway }) => {
  await page.goto(`/operations/control?site=${SITE_ID}`);

  const pump = page.getByRole('group', { name: 'CWP-01' });
  await pump.getByRole('button', { name: '停止' }).click();
  const confirm = page.getByRole('alertdialog');
  await confirm.getByRole('textbox', { name: '动态口令' }).fill('111222');
  await confirm.getByRole('checkbox').check();
  await confirm.getByRole('button', { name: '提交停止' }).click();
  await expect(pump).toContainText('待审批');

  await pump.getByRole('button', { name: '锁定 CWP-01' }).click();
  const dialog = page.getByRole('dialog', { name: '锁定 CWP-01？' });
  await dialog.getByText('检修锁定', { exact: true }).click();
  await expect(dialog).toContainText('锁定后会撤回这台设备 1 条还没下发的指令');
  await dialog.getByRole('textbox', { name: '原因' }).fill('轴承异响，等维保');
  await dialog.getByRole('button', { name: '检修锁定' }).click();

  await expect(page.getByText('已检修锁定 CWP-01，撤回了 1 条指令')).toBeVisible();
  expect(gateway.writes.find((entry) => entry.path.endsWith('/control-lock'))?.body).toEqual({ lockMode: 'MAINTENANCE_LOCK', reason: '轴承异响，等维保' });
  await expect(pump).toContainText('检修锁定');
  await expect(pump.getByRole('button', { name: '停止' })).toBeDisabled();
  await expect(page.getByRole('region', { name: '控制方式' })).toContainText('锁定设备1 台');

  await pump.getByRole('button', { name: '解除锁定 CWP-01' }).click();
  const unlock = page.getByRole('dialog', { name: '解除 CWP-01 的锁定？' });
  await expect(unlock).toContainText('轴承异响，等维保');
  await unlock.getByRole('textbox', { name: '原因' }).fill('维保完成');
  await unlock.getByRole('button', { name: '解除锁定' }).click();
  await expect(pump.getByRole('button', { name: '停止' })).toBeEnabled();
});

test('during a maintenance window only stopping and resetting stay available', async ({ page, gateway }) => {
  gateway.controlState = {
    ...gateway.controlState,
    activeMaintenanceWindow: { id: '01a19000-0000-7000-8000-0000000000aa', title: '冷却塔清洗', startDate: '2026-11-15', endDate: '2026-11-17', reason: '' },
  };
  await page.goto(`/operations/control?site=${SITE_ID}`);

  await expect(page.getByRole('region', { name: '控制方式' })).toContainText('检修窗口「冷却塔清洗」2026-11-15 至 2026-11-17，只能停机和复位');
  await expect(page.getByRole('button', { name: '调整 CHILLER-01 的设定值' })).toBeDisabled();
  await expect(page.getByRole('group', { name: 'CHWP-01' }).getByRole('button', { name: '停止' })).toBeEnabled();
});

test('a safe shutdown of the whole site asks for a reason, the code and a site check, then switches to safe exit', async ({ page, gateway }) => {
  await page.goto(`/operations/control?site=${SITE_ID}`);
  await page.getByRole('group', { name: 'CT-01' }).getByRole('button', { name: '停止' }).click();
  const confirm = page.getByRole('alertdialog');
  await confirm.getByRole('textbox', { name: '动态口令' }).fill('654321');
  await confirm.getByRole('checkbox').check();
  await confirm.getByRole('button', { name: '提交停止' }).click();
  await expect(page.getByRole('list', { name: '指令记录' }).getByRole('listitem').first()).toContainText('待审批');

  await page.getByRole('region', { name: '控制方式' }).getByRole('button', { name: '停机…' }).click();
  const dialog = page.getByRole('dialog', { name: '安全停机整个站点？' });
  // The recorded plant has no emergency release point, so only the ordered shutdown is offered.
  await expect(dialog.getByRole('radiogroup')).toHaveCount(0);
  await expect(dialog).toContainText('会撤回 1 条还没下发的指令');
  const submit = dialog.getByRole('button', { name: '执行安全停机' });
  await dialog.getByRole('textbox', { name: '原因' }).fill('冷冻水管漏水');
  await dialog.getByRole('textbox', { name: '动态口令' }).fill('112233');
  await expect(submit).toBeDisabled();
  await dialog.getByRole('checkbox', { name: '我已和现场确认可以停机' }).check();
  await submit.click();

  await expect(page.getByText('已开始安全停机：提交 1 条停机指令，撤回 1 条，3 台水泵或冷却塔等主机停稳后再停')).toBeVisible();
  const write = gateway.writes.find((entry) => entry.path === `/api/v1/sites/${SITE_ID}/control-shutdown`);
  expect(write?.body).toEqual({ mode: 'PLANT_SAFE_SHUTDOWN', reason: '冷冻水管漏水', verificationCode: '112233', expectedRevision: 1 });
  expect(write?.csrfToken).toBeTruthy();
  await expect(page.getByRole('region', { name: '控制方式' })).toContainText('安全退出');
  await expect(page.getByRole('banner').getByRole('link', { name: '安全退出' })).toBeVisible();
});

test('the emergency cut-off is offered only where the site registers one and needs its own confirmation', async ({ page, gateway }) => {
  const model = gateway.assetModel;
  const stop = model.telemetryPoints.find((point: { pointCode: string }) => point.pointCode === 'stop');
  model.telemetryPoints.push({ ...stop, id: '0190c000-0000-7000-8000-0000000000e1', pointCode: 'emergency_stop_all', displayName: '紧急全断全部 DO 通道', sourceKey: 'controller.emergency_stop_all', sourceMetadata: { ...stop.sourceMetadata, actuationMode: 'EMERGENCY_DO_RELEASE' } });
  model.counts.points += 1;
  await page.goto(`/operations/control?site=${SITE_ID}`);

  await page.getByRole('region', { name: '控制方式' }).getByRole('button', { name: '停机…' }).click();
  await page.getByRole('dialog').getByText('紧急全断', { exact: true }).click();
  const dialog = page.getByRole('dialog', { name: '紧急全断整个站点？' });
  await dialog.getByRole('textbox', { name: '原因' }).fill('冷却塔起火');
  await dialog.getByRole('textbox', { name: '动态口令' }).fill('445566');
  await dialog.getByRole('checkbox', { name: '我已和现场确认可以停机' }).check();
  const submit = dialog.getByRole('button', { name: '立刻全断' });
  await expect(submit).toBeDisabled();
  await dialog.getByRole('checkbox', { name: '我确认要立刻断开全部输出通道' }).check();
  await submit.click();

  await expect(page.getByText('已发出紧急全断：提交 1 条停机指令')).toBeVisible();
  const write = gateway.writes.find((entry) => entry.path === `/api/v1/sites/${SITE_ID}/control-shutdown`);
  expect(write?.body).toEqual({ mode: 'EMERGENCY_DO_RELEASE', reason: '冷却塔起火', verificationCode: '445566', expectedRevision: 1, confirmEmergencyDoRelease: true });
});

test('a unit whose one-click sequence is on its way can be aborted and stopped in order', async ({ page, gateway }) => {
  const model = gateway.assetModel;
  const chiller = model.assets.find((asset: { code: string }) => asset.code === 'chiller-01');
  const stop = model.telemetryPoints.find((point: { id: string; pointCode: string }) => model.relationships.some((relationship: { fromId: string; toId: string }) => relationship.fromId === point.id && relationship.toId === chiller.id)
    && point.pointCode === 'stop');
  const sequence = { ...stop, id: '0190c000-0000-7000-8000-0000000000f1', pointCode: 'stop_unit_sequence', sourceKey: 'chiller.stop_unit_sequence', displayName: '一键停机', sourceMetadata: { ...stop.sourceMetadata, capability: 'STOP_UNIT_SEQUENCE' } };
  model.telemetryPoints.push(sequence);
  model.counts.points += 1;
  const controlsStop = model.relationships.find((relationship: { fromId: string }) => relationship.fromId === stop.id);
  model.relationships.push({ ...controlsStop, id: '0190c000-0000-7000-8000-0000000000f2', fromId: sequence.id });
  await page.goto(`/operations/control?site=${SITE_ID}`);

  const unit = page.getByRole('region', { name: 'CHILLER-01' });
  await unit.getByRole('button', { name: '一键停机' }).click();
  const confirm = page.getByRole('alertdialog');
  await confirm.getByRole('textbox', { name: '动态口令' }).fill('778899');
  await confirm.getByRole('checkbox').check();
  await confirm.getByRole('button', { name: '提交一键停机' }).click();
  await expect(unit).toContainText('一键启停 · 待审批');

  await unit.getByRole('button', { name: '中止…' }).click();
  const dialog = page.getByRole('dialog', { name: '中止CHILLER-01的一键启停？' });
  await expect(dialog).toContainText('控制方式不变，其他机组照常运行');
  await expect(dialog).toContainText('会撤回 1 条还没下发的指令');
  await dialog.getByRole('textbox', { name: '原因' }).fill('现场发现冷却水阀未开');
  await dialog.getByRole('textbox', { name: '动态口令' }).fill('990011');
  await dialog.getByRole('checkbox', { name: '我已和现场确认可以停机' }).check();
  await dialog.getByRole('button', { name: '中止并停机' }).click();

  await expect(page.getByText('已中止CHILLER-01：提交 1 条停机指令，撤回 1 条')).toBeVisible();
  const write = gateway.writes.find((entry) => entry.path === `/api/v1/sites/${SITE_ID}/control-shutdown`);
  expect(write?.body).toEqual({ mode: 'ABORT_UNIT_SEQUENCE', reason: '现场发现冷却水阀未开', verificationCode: '990011', expectedRevision: 1, unitCode: 'chiller-01' });
  await expect(page.getByRole('region', { name: '控制方式' })).toContainText('远程自动控制');
});

test('the appearance switch keeps document color-scheme and meta[name="color-scheme"] in sync across system, light and dark', async ({ page }) => {
  await page.goto(`/operations/control?site=${SITE_ID}`);

  const appearance = page.getByRole('radiogroup', { name: '外观' });
  await expect(page.locator('meta[name="color-scheme"]')).toHaveAttribute('content', 'light dark');

  await appearance.getByRole('radio', { name: '深色' }).click();
  await expect(page.locator('meta[name="color-scheme"]')).toHaveAttribute('content', 'dark');
  await expect.poll(() => page.evaluate(() => getComputedStyle(document.documentElement).colorScheme)).toBe('dark');

  await appearance.getByRole('radio', { name: '浅色' }).click();
  await expect(page.locator('meta[name="color-scheme"]')).toHaveAttribute('content', 'light');
  await expect.poll(() => page.evaluate(() => getComputedStyle(document.documentElement).colorScheme)).toBe('light');

  await appearance.getByRole('radio', { name: '系统' }).click();
  await expect(page.locator('meta[name="color-scheme"]')).toHaveAttribute('content', 'light dark');
});

test('control mode and asset locks show operator display names without fallback labels', async ({ page, gateway }) => {
  const nowIso = new Date().toISOString();
  gateway.controlState = {
    ...gateway.controlState,
    controlMode: 'MANUAL_OVERRIDE',
    reason: '巡检接管',
    updatedBy: '018f3e00-2000-7000-8000-000000000099',
    updatedByName: '周敏',
    updatedAt: nowIso,
    assetLocks: [
      {
        siteId: SITE_ID,
        assetId: '01910000-0000-7000-8000-000000000006',
        lockMode: 'MAINTENANCE_LOCK',
        reason: '换密封垫',
        updatedBy: '018f3e00-2000-7000-8000-000000000099',
        updatedByName: '周敏',
        updatedAt: nowIso,
        revision: 2,
        boundDeviceIds: ['018f3e00-4000-7000-8000-000000000002'],
      },
    ],
  };

  await page.goto(`/operations/control?site=${SITE_ID}`);
  const modeRegion = page.getByRole('region', { name: '控制方式' });
  await expect(modeRegion).toContainText(/设定\s*周敏/);
  await expect(modeRegion).not.toContainText('其他人员');
  await page.getByRole('group', { name: 'CHWP-01' }).getByRole('button', { name: '解除锁定 CHWP-01' }).click();
  const lockDialog = page.getByRole('dialog', { name: '解除 CHWP-01 的锁定？' });
  await expect(lockDialog).toContainText('周敏');
  await lockDialog.getByRole('button', { name: '取消' }).click();

  await page.goto(`/operations/realtime?site=${SITE_ID}`);
  await expect(page.getByText('周敏').first()).toBeVisible();
  await expect(page.getByText('其他人员')).toHaveCount(0);

  await page.goto(`/operations/systems-devices?site=${SITE_ID}`);
  await page.getByText('CHWP-01').first().click();
  await expect(page.getByText('周敏').first()).toBeVisible();
});

test('unit column header shows active power from bound HVAC_POWER_METER and falls back when stale', async ({ page, gateway }) => {
  await page.goto(`/operations/control?site=${SITE_ID}`);
  const unitRegion = page.getByRole('region', { name: 'CHILLER-01' });
  await expect(unitRegion).toContainText('暂无读数');

  const meter = gateway.assetModel.devices.find((d: { code: string }) => d.code === 'hvac-meter');
  meter.deviceType = 'HVAC_POWER_METER';
  gateway.assetModel.relationships.push({
    id: '01910000-0000-7000-8000-ffffffff0099',
    tenantId: gateway.assetModel.tenantId,
    siteId: SITE_ID,
    fromType: 'DEVICE',
    fromId: meter.id,
    toType: 'ASSET',
    toId: '01910000-0000-7000-8000-000000000005',
    role: 'MONITORED_BY',
    status: 'ACTIVE',
    validFrom: '2026-09-28T07:15:32.298Z',
    validTo: null,
    revision: 1,
    createdAt: '2026-09-28T07:15:32.298Z',
    updatedAt: '2026-09-29T08:51:35.738Z',
  });
  const meterSnap = gateway.snapshots.items.find((item: { deviceId: string }) => item.deviceId === meter.id);
  const powerVal = meterSnap.snapshot.values.find((entry: { key: string }) => entry.key === 'active_power');
  powerVal.value = 186.24;

  await page.reload();
  await expect(page.getByRole('region', { name: 'CHILLER-01' })).toContainText('186.2 kW');

  powerVal.freshness = 'STALE';
  await page.reload();
  await expect(page.getByRole('region', { name: 'CHILLER-01' })).toContainText('暂无读数');
});

test('compiled interlockChecks render interlock preconditions in unit group', async ({ page, gateway }) => {
  const stopPoint = gateway.assetModel.telemetryPoints.find(
    (point: { id: string }) => point.id === '01910000-0000-7000-8000-500000000002',
  );
  stopPoint.sourceMetadata.interlockChecks = [
    {
      deviceId: '018f3e00-4000-7000-8000-000000000002',
      key: 'run_state',
      expected: 'STOPPED',
      reason: '停冷机前需先停冷冻泵',
    },
  ];

  await page.goto(`/operations/control?site=${SITE_ID}`);
  const chillerGroup = page.getByRole('group', { name: 'CHILLER-01' });
  await expect(chillerGroup).toContainText('先停 CHWP-01');
  await expect(chillerGroup.getByRole('button', { name: '停止' })).toBeDisabled();
});

test('TOTP step-up failure reasons show actionable guidance and MFA enroll link', async ({ page, gateway }) => {
  await page.goto(`/operations/control?site=${SITE_ID}`);

  const submitWithReason = async (reason: 'CODE_INVALID' | 'CODE_REPLAYED' | 'MFA_LOCKED' | 'MFA_NOT_CONFIGURED') => {
    gateway.stepUpFailureReason = reason;
    await page.getByRole('group', { name: 'CT-01' }).getByRole('button', { name: '停止' }).click();
    const confirm = page.getByRole('alertdialog');
    await confirm.getByRole('textbox', { name: '动态口令' }).fill('111222');
    await confirm.getByRole('checkbox').check();
    await confirm.getByRole('button', { name: '提交停止' }).click();
    return confirm;
  };

  await page.getByRole('group', { name: 'CT-01' }).getByRole('button', { name: '停止' }).click();
  const imeConfirm = page.getByRole('alertdialog');
  const codeInput = imeConfirm.getByRole('textbox', { name: '动态口令' });
  await codeInput.fill('111222');
  await imeConfirm.getByRole('checkbox').check();
  await codeInput.dispatchEvent('keydown', { key: 'Enter', isComposing: true, keyCode: 229 });
  expect(gateway.writes.filter((write) => write.path === '/api/v1/commands')).toEqual([]);
  await imeConfirm.getByRole('button', { name: '取消' }).click();

  const invalidConfirm = await submitWithReason('CODE_INVALID');
  await expect(invalidConfirm.getByRole('alert')).toBeVisible();
  await invalidConfirm.getByRole('button', { name: '取消' }).click();

  const replayedConfirm = await submitWithReason('CODE_REPLAYED');
  await expect(replayedConfirm.getByRole('alert')).toBeVisible();
  await replayedConfirm.getByRole('button', { name: '取消' }).click();

  const lockedConfirm = await submitWithReason('MFA_LOCKED');
  await expect(lockedConfirm.getByRole('alert')).toBeVisible();
  await lockedConfirm.getByRole('button', { name: '取消' }).click();

  const mfaConfirm = await submitWithReason('MFA_NOT_CONFIGURED');
  await expect(mfaConfirm.getByRole('alert')).toBeVisible();
  const enrollLink = mfaConfirm.getByRole('link', { name: /去开通两步验证/ });
  await expect(enrollLink).toBeVisible();
  await enrollLink.click();
  await expect(page).toHaveURL(/\/system\?tab=users/);
  await expect(page.getByText(/两步验证|动态口令|认证器/).first()).toBeVisible();
});

test('control mode bar displays active auto-control lease badge and link when armed', async ({ page, gateway }) => {
  const now = Date.now();
  const expiresAt = new Date(now + 8 * 3_600_000).toISOString();
  const activeLease = {
    id: '01990000-8800-7000-8000-000000000091',
    tenantId: gateway.assetModel.tenantId,
    siteId: SITE_ID,
    status: 'ACTIVE',
    armedBy: '018f3e00-2000-7000-8000-000000000001',
    armedAt: new Date(now - 600_000).toISOString(),
    expiresAt,
    allowedCapabilities: ['SET_CHILLED_WATER_TEMPERATURE_SETPOINT'],
    maxSetpointStepC: 1.0,
    supplyTempMinC: 6.0,
    supplyTempMaxC: 12.0,
    zoneTempMaxC: 26.0,
    minRunningChillers: 1,
    maxCommandsPerHour: 2,
    revision: 1,
    createdAt: new Date(now - 600_000).toISOString(),
    updatedAt: new Date(now - 600_000).toISOString(),
  };
  gateway.autoControlLeaseState = {
    tenantId: gateway.assetModel.tenantId,
    siteId: SITE_ID,
    confirmationMode: 'AUTO_WITH_LEASE',
    activeLease,
    latestLease: activeLease,
  };

  await page.goto(`/operations/control?site=${SITE_ID}`);
  const modeBar = page.getByRole('region', { name: '控制方式' });
  await expect(modeBar).toContainText('自动控制租约生效中');
  const manageLink = modeBar.getByRole('link', { name: '管理自动租约 ›' });
  await expect(manageLink).toBeVisible();
  await manageLink.click();
  await expect(page).toHaveURL(/\/energy-analysis\/optimization/);
});

test('command log and drawer show Chinese priority tier and translate DEVICE_PRIORITY_LOCKED refusals', async ({ page, gateway }) => {
  await page.goto(`/operations/control?site=${SITE_ID}`);

  await page.getByRole('button', { name: '调整 CHILLER-01 的设定值' }).click();
  const settings = page.getByRole('dialog', { name: '调整 CHILLER-01 的设定值' });
  const setpointInput = settings.getByRole('textbox', { name: '冷冻水出水设定温度目标值' });
  await setpointInput.fill('8.0');
  await setpointInput.press('Enter');
  await page.getByRole('alertdialog').getByRole('button', { name: '确认下发' }).click();

  const logItem = page.getByRole('list', { name: '指令记录' }).getByRole('listitem').first();
  await expect(logItem).toContainText('人工干预');
  await expect(logItem).not.toContainText(/MANUAL_OVERRIDE/);
  await logItem.getByRole('button', { name: /冷冻水出水设定温度/ }).click();
  const sheet = page.getByRole('dialog');
  await expect(sheet).toContainText('控制优先级');
  await expect(sheet).toContainText('人工干预');
  await expect(sheet).toContainText('值班人员操作');
  await page.keyboard.press('Escape');
  await expect(sheet).toHaveCount(0);

  const chillerPoint = gateway.assetModel.telemetryPoints.find((candidate: { reportingDeviceId: string; pointType: string }) => candidate.pointType === 'COMMAND');
  gateway.priorityLockedDeviceIds.add(chillerPoint.reportingDeviceId);
  await page.getByRole('button', { name: '调整 CHILLER-01 的设定值' }).click();
  await page.getByRole('dialog', { name: '调整 CHILLER-01 的设定值' }).getByRole('textbox', { name: '冷冻水出水设定温度目标值' }).fill('8.5');
  await page.getByRole('dialog', { name: '调整 CHILLER-01 的设定值' }).getByRole('textbox', { name: '冷冻水出水设定温度目标值' }).press('Enter');
  await page.getByRole('alertdialog').getByRole('button', { name: '确认下发' }).click();
  await expect(page.getByText('这台设备正由更高优先级的安全联锁或人工指令锁定，当前指令不能下发。').first()).toBeVisible();
});

test('operators can acquire and release device priority locks and replay control alarm dead letters', async ({ page, gateway }) => {
  await page.goto(`/operations/control?site=${SITE_ID}`);

  const chillerCard = page.getByRole('group', { name: 'CHILLER-01' });
  await chillerCard.getByRole('button', { name: '设置 CHILLER-01 优先级锁定' }).click();
  const lockDialog = page.getByRole('dialog', { name: /CHILLER-01 优先级仲裁锁/ });
  await lockDialog.getByText('安全联锁', { exact: true }).click();
  await lockDialog.getByRole('button', { name: '30 分钟' }).click();
  await lockDialog.getByRole('textbox', { name: '锁定原因' }).fill('冷凝器压差高，高优先级保护锁机');
  await lockDialog.getByRole('button', { name: '确认锁定' }).click();

  await expect(page.getByText('已为 CHILLER-01 设置安全联锁锁定')).toBeVisible();
  await expect(chillerCard).toContainText('安全联锁 · 剩余');
  await expect(chillerCard).not.toContainText(/SAFETY_INTERLOCK/);

  const lockPut = gateway.writes.find((entry) => entry.method === 'PUT' && entry.path.endsWith('/priority-lock'));
  expect(lockPut?.body).toEqual({
    lockedPriority: 'SAFETY_INTERLOCK',
    durationMinutes: 30,
    reason: '冷凝器压差高，高优先级保护锁机',
  });

  await chillerCard.getByRole('button', { name: '管理 CHILLER-01 优先级锁定' }).click();
  const editDialog = page.getByRole('dialog', { name: /CHILLER-01 优先级仲裁锁/ });
  await expect(editDialog).toContainText('冷凝器压差高，高优先级保护锁机');
  await editDialog.getByRole('textbox', { name: '更新或解除原因' }).fill('压差恢复正常');
  await editDialog.getByRole('button', { name: '解除锁定' }).click();
  await expect(page.getByText('已解除 CHILLER-01 的优先级锁定')).toBeVisible();
  await expect(chillerCard).not.toContainText('安全联锁 · 剩余');

  const dlqButton = page.getByRole('button', { name: /控制告警死信 \(1\)/ });
  await dlqButton.click();
  const dlqDialog = page.getByRole('dialog', { name: '控制告警投递死信' });
  const dlqTable = dlqDialog.getByRole('table', { name: '控制告警投递死信列表' });
  await expect(dlqTable).toContainText('CHILLER-01');
  await expect(dlqTable).toContainText('执行结果未知');
  await expect(dlqTable).toContainText('下游告警服务通信失败');
  await dlqTable.getByRole('button', { name: '重新投递' }).click();
  await expect(page.getByText('已重新提交到控制告警投递队列')).toBeVisible();
  await expect(dlqDialog).toContainText('当前没有待重投的控制告警死信记录');
  await dlqDialog.getByRole('button', { name: 'Close' }).click();
  await expect(dlqDialog).toBeHidden();
});

test('sidebar shows live pending control count and layout stacks without horizontal overflow when assistant opens at 1280px', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.goto(`/operations/control?site=${SITE_ID}`);

  const controlLink = page.getByRole('link', { name: /运行控制/ });
  await expect(controlLink).toBeVisible();
  await expect(controlLink).not.toContainText('需审批');

  await page.getByRole('group', { name: 'CHWP-01' }).getByRole('button', { name: '停止' }).click();
  const confirm = page.getByRole('alertdialog', { name: '停止 CHWP-01？' });
  await confirm.getByRole('textbox', { name: '动态口令' }).fill('123456');
  await confirm.getByRole('checkbox', { name: '我已和现场确认可以停' }).check();
  await confirm.getByRole('button', { name: '提交停止' }).click();

  await expect(page.getByRole('listitem').filter({ hasText: '停止冷冻水泵' })).toContainText('待审批');
  await expect(controlLink.locator('..')).toContainText('1');

  await page.getByRole('button', { name: 'AI 助手' }).click();
  await expect(page.getByRole('complementary', { name: 'AI 助手' })).toBeVisible();
  await expect
    .poll(() => page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth))
    .toBe(true);
});
