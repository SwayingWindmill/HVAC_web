import {
  buildActionConfirmationIntent,
  buildSettingConfirmationIntent,
  canSubmitCommands,
  clampAndRoundSetting,
  CODE_PATTERN,
  createCommandSubmitMutex,
  dispatchControlCommand,
  evaluateControlGuards,
  fetchSiteCommands,
  fetchSiteControlState,
  mapCommandSubmitError,
  normalizeTotpCode,
  parseDeviceControlTargets,
  presentCommandStatus,
  startCommandPolling,
  type ActionButtonView,
  type CommandRecord,
  type DeviceControlTarget,
  type GuardSummary,
  type PendingControlIntent,
  type PresentedCommandStatus,
  type PrincipalCapabilitiesResponse,
  type SettingRowView,
  type SiteControlState,
} from '../../lib/control';
import { electricityByDevice, siteTodayStart } from '../../lib/energy';
import type { PlantDevice } from '../../lib/plant';
import { loadPlant, loadPlantDevice, loadSiteAssetModel } from '../../lib/plant-load';
import { gateway, requireSession } from '../../lib/platform';
import { whileShown } from '../../lib/refresh';
import { presentDevice, type DeviceToday } from '../../lib/run';
import { currentSite, type Site } from '../../lib/site';

interface ControlTargetView {
  readonly assetId: string;
  readonly assetName: string;
  readonly guard: GuardSummary;
  readonly actions: readonly ActionButtonView[];
  readonly settings: readonly SettingRowView[];
}

const refresher = whileShown(30_000);
const submitMutex = createCommandSubmitMutex();

// The device this page shows, kept out of setData along with active timers and unsent drafts.
let deviceId = '';
let currentSiteRef: Site | null = null;
let currentDeviceRef: PlantDevice | null = null;
let currentTargets: DeviceControlTarget[] = [];
let draftValues: Record<string, number> = {};
let inputDraftText: Record<string, string> = {};
let activePolling: { readonly cancel: () => void } | null = null;

const ELECTRICITY_UNREADABLE = '今天的用电暂时读不到。';

function stopCommandPolling(): void {
  if (activePolling !== null) {
    activePolling.cancel();
    activePolling = null;
  }
}

/** Today's electricity of the Site and of this device's own meter; a failed read leaves the chart out with a note. */
async function loadToday(site: Site, dayStart: PromiseSettledResult<string | null>, now: Date): Promise<{ today: DeviceToday | null; note: string }> {
  if (dayStart.status === 'rejected') return { today: null, note: ELECTRICITY_UNREADABLE };
  const start = dayStart.value;
  if (start === null) return { today: null, note: '' };
  try {
    const series = await electricityByDevice(site, { from: start, to: now.toISOString() });
    const own = series.devices?.find((device) => device.deviceId === deviceId);
    return { today: { start, now, site: series.points, device: own?.points ?? null }, note: '' };
  } catch {
    return { today: { start, now, site: [], device: null }, note: ELECTRICITY_UNREADABLE };
  }
}

function buildSettingDraftTextMap(targets: readonly ControlTargetView[]): Record<string, string> {
  const next: Record<string, string> = {};
  for (const target of targets) {
    for (const setting of target.settings) {
      next[setting.pointId] = inputDraftText[setting.pointId] ?? setting.draftText;
    }
  }
  return next;
}

Page({
  data: {
    loading: true,
    failed: '',
    view: null as ReturnType<typeof presentDevice> | null,
    energyNote: '',
    hasControllablePoints: false,
    canControl: false,
    controlTargets: [] as readonly ControlTargetView[],
    settingInputs: {} as Readonly<Record<string, string>>,
    commandStatus: null as PresentedCommandStatus | null,
    pollingNote: '',
    pendingIntent: null as PendingControlIntent | null,
    confirmChecked: false,
    totpCode: '',
    submitError: '',
    showMfaHint: false,
    submitting: false,
  },

  onLoad(query: Record<string, string | undefined>) {
    deviceId = query.id ?? '';
    draftValues = {};
    inputDraftText = {};
  },

  onShow() {
    if (!requireSession()) return;
    this.refresh();
    refresher.start(() => this.refresh());
  },

  onHide() {
    refresher.stop();
    stopCommandPolling();
  },

  onUnload() {
    refresher.stop();
    stopCommandPolling();
    currentTargets = [];
    currentDeviceRef = null;
    currentSiteRef = null;
  },

  async onPullDownRefresh() {
    await this.refresh({ rereadEquipment: true });
    wx.stopPullDownRefresh();
  },

  async refresh({ rereadEquipment = false } = {}) {
    try {
      const site = await currentSite(rereadEquipment);
      if (site === null) {
        this.setData({ loading: false, failed: '这个账号还看不到任何站点，请管理员在网页「系统管理」里分配站点。' });
        return;
      }
      currentSiteRef = site;
      const now = new Date();
      const [device, dayStart, modelResult, principalResult, controlStateResult, commandsResult] = await Promise.all([
        loadPlantDevice(site, deviceId, rereadEquipment),
        Promise.allSettled([siteTodayStart(site, now)]),
        Promise.allSettled([loadSiteAssetModel(site, rereadEquipment)]),
        Promise.allSettled([gateway.get<PrincipalCapabilitiesResponse>('/api/v1/principal')]),
        Promise.allSettled([fetchSiteControlState(gateway, site.id)]),
        Promise.allSettled([fetchSiteCommands(gateway, site.id, 50)]),
      ]);
      if (device === null) {
        this.setData({ loading: false, failed: '这台设备已不在站点的设备清单里。' });
        return;
      }
      currentDeviceRef = device;

      const rawModel = modelResult[0].status === 'fulfilled' ? modelResult[0].value : null;
      const principal = principalResult[0].status === 'fulfilled' ? principalResult[0].value : null;
      const controlState: SiteControlState | null = controlStateResult[0].status === 'fulfilled' ? controlStateResult[0].value : null;
      const commands: readonly CommandRecord[] = commandsResult[0].status === 'fulfilled' ? commandsResult[0].value : [];

      const targets = rawModel !== null ? parseDeviceControlTargets(rawModel, deviceId, now) : [];
      currentTargets = targets;
      const hasControllablePoints = targets.length > 0;
      const canControl = hasControllablePoints && canSubmitCommands(principal);

      const runStateByDeviceId = new Map<string, string>([
        [device.deviceId, device.connection === 'OFFLINE' ? 'OFFLINE' : device.runState],
      ]);
      const nameByDeviceId = new Map<string, string>(
        (rawModel?.devices ?? []).map((item) => [item.id, item.displayName]),
      );

      if (canControl && targets.some((target) => target.actions.some((action) => action.interlocks.length > 0))) {
        try {
          const plant = await loadPlant(site, false);
          for (const peer of plant.devices) {
            runStateByDeviceId.set(peer.deviceId, peer.connection === 'OFFLINE' ? 'OFFLINE' : peer.runState);
            nameByDeviceId.set(peer.deviceId, peer.name);
          }
        } catch {
          // Interlocks keep conservative fail-closed evaluation when peer run states are unavailable.
        }
      }

      let openStatus: PresentedCommandStatus | null = null;
      const controlTargets: ControlTargetView[] = targets.map((target) => {
        const evaluated = evaluateControlGuards({
          target,
          device,
          controlState,
          commands,
          runStateByDeviceId,
          nameByDeviceId,
          draftValues,
        });
        if (openStatus === null && evaluated.openCommand !== null) {
          openStatus = evaluated.openCommand;
        }
        return {
          assetId: target.assetId,
          assetName: target.assetName,
          guard: evaluated.guard,
          actions: evaluated.actions,
          settings: evaluated.settings,
        };
      });

      const { today, note } = await loadToday(site, dayStart[0], now);
      wx.setNavigationBarTitle({ title: device.name });
      this.setData({
        loading: false,
        failed: '',
        view: presentDevice(device, today),
        energyNote: note,
        hasControllablePoints,
        canControl,
        controlTargets,
        settingInputs: buildSettingDraftTextMap(controlTargets),
        ...(activePolling === null && openStatus !== null ? { commandStatus: openStatus } : {}),
      });
    } catch {
      this.setData({ loading: false, failed: '这台设备的读数暂时读不到，下拉刷新再试。' });
    }
  },

  stepSetting(event: WechatMiniprogram.TouchEvent) {
    const pointId = String(event.currentTarget.dataset.pointId ?? '');
    const deltaSign = Number(event.currentTarget.dataset.delta ?? 0);
    if (!pointId || (deltaSign !== 1 && deltaSign !== -1)) return;

    for (const target of currentTargets) {
      const setting = target.settings.find((item) => item.pointId === pointId);
      if (!setting) continue;
      const viewTarget = this.data.controlTargets.find((item) => item.assetId === target.assetId);
      const row = viewTarget?.settings.find((item) => item.pointId === pointId);
      if (!row || row.disabled) return;
      const base = draftValues[pointId] ?? row.draftValue;
      const next = clampAndRoundSetting(base + deltaSign * setting.step, setting);
      draftValues[pointId] = next;
      delete inputDraftText[pointId];
      this.syncDraftToView(pointId, next);
      return;
    }
  },

  onSettingInput(event: WechatMiniprogram.Input) {
    const pointId = String(event.currentTarget.dataset.pointId ?? '');
    if (!pointId) return;
    const raw = event.detail.value;
    inputDraftText[pointId] = raw;
    this.setData({ [`settingInputs.${pointId}`]: raw });
  },

  onSettingBlur(event: WechatMiniprogram.Input) {
    const pointId = String(event.currentTarget.dataset.pointId ?? '');
    if (!pointId) return;
    const raw = (event.detail.value ?? '').trim();
    for (const target of currentTargets) {
      const setting = target.settings.find((item) => item.pointId === pointId);
      if (!setting) continue;
      const parsed = Number(raw);
      if (!raw || !Number.isFinite(parsed)) {
        delete inputDraftText[pointId];
        const fallback = draftValues[pointId] ?? setting.minimum;
        this.syncDraftToView(pointId, fallback);
        return;
      }
      if (parsed < setting.minimum || parsed > setting.maximum) {
        wx.showToast({ title: `请输入 ${setting.minimum} 至 ${setting.maximum} 之间的数值`, icon: 'none' });
        return;
      }
      const normalized = clampAndRoundSetting(parsed, setting);
      draftValues[pointId] = normalized;
      delete inputDraftText[pointId];
      this.syncDraftToView(pointId, normalized);
      return;
    }
  },

  syncDraftToView(pointId: string, value: number) {
    const nextTargets = this.data.controlTargets.map((target) => ({
      ...target,
      settings: target.settings.map((setting) => {
        if (setting.pointId !== pointId) return setting;
        const normalized = clampAndRoundSetting(value, setting);
        return {
          ...setting,
          draftValue: normalized,
          draftText: String(normalized),
        };
      }),
    }));
    const updatedRow = nextTargets.flatMap((t) => t.settings).find((s) => s.pointId === pointId);
    this.setData({
      controlTargets: nextTargets,
      [`settingInputs.${pointId}`]: updatedRow?.draftText ?? String(value),
    });
  },

  tapAction(event: WechatMiniprogram.TouchEvent) {
    if (!this.data.canControl || currentDeviceRef === null) return;
    const assetId = String(event.currentTarget.dataset.assetId ?? '');
    const pointId = String(event.currentTarget.dataset.pointId ?? '');
    const target = currentTargets.find((item) => item.assetId === assetId);
    const action = target?.actions.find((item) => item.pointId === pointId);
    const viewRow = this.data.controlTargets
      .find((item) => item.assetId === assetId)
      ?.actions.find((item) => item.pointId === pointId);
    if (!target || !action || !viewRow || viewRow.disabled) {
      if (viewRow?.disabledReason) {
        wx.showToast({ title: viewRow.disabledReason, icon: 'none' });
      }
      return;
    }

    const intent = buildActionConfirmationIntent({
      action,
      assetName: target.assetName,
      device: currentDeviceRef,
    });
    this.setData({
      pendingIntent: intent,
      confirmChecked: !intent.highRisk,
      totpCode: '',
      submitError: '',
      showMfaHint: false,
    });
  },

  tapSetting(event: WechatMiniprogram.TouchEvent) {
    if (!this.data.canControl || currentDeviceRef === null) return;
    const assetId = String(event.currentTarget.dataset.assetId ?? '');
    const pointId = String(event.currentTarget.dataset.pointId ?? '');
    const target = currentTargets.find((item) => item.assetId === assetId);
    const setting = target?.settings.find((item) => item.pointId === pointId);
    const viewRow = this.data.controlTargets
      .find((item) => item.assetId === assetId)
      ?.settings.find((item) => item.pointId === pointId);
    if (!target || !setting || !viewRow || viewRow.disabled) {
      if (viewRow?.disabledReason) {
        wx.showToast({ title: viewRow.disabledReason, icon: 'none' });
      }
      return;
    }

    const rawInput = inputDraftText[pointId];
    let targetValue = draftValues[pointId] ?? viewRow.draftValue;
    if (rawInput !== undefined) {
      const trimmed = rawInput.trim();
      const parsed = Number(trimmed);
      if (!trimmed || !Number.isFinite(parsed) || parsed < setting.minimum || parsed > setting.maximum) {
        wx.showToast({ title: `数值须在 ${setting.minimum} 至 ${setting.maximum} 之间`, icon: 'none' });
        return;
      }
      targetValue = clampAndRoundSetting(parsed, setting);
      draftValues[pointId] = targetValue;
      delete inputDraftText[pointId];
      this.syncDraftToView(pointId, targetValue);
    }

    const intent = buildSettingConfirmationIntent({
      setting,
      assetName: target.assetName,
      deviceName: currentDeviceRef.name,
      currentNumeric: viewRow.currentNumeric,
      targetValue,
    });
    this.setData({
      pendingIntent: intent,
      confirmChecked: true,
      totpCode: '',
      submitError: '',
      showMfaHint: false,
    });
  },

  toggleHighRiskConfirm() {
    if (this.data.submitting) return;
    this.setData({ confirmChecked: !this.data.confirmChecked, submitError: '' });
  },

  onTotpInput(event: WechatMiniprogram.Input) {
    const normalized = normalizeTotpCode(event.detail.value);
    this.setData({ totpCode: normalized, submitError: '' });
  },

  cancelConfirmation() {
    if (this.data.submitting) return;
    this.setData({
      pendingIntent: null,
      confirmChecked: false,
      totpCode: '',
      submitError: '',
      showMfaHint: false,
    });
  },

  noop() {},

  dismissCommandStatus() {
    if (this.data.commandStatus?.active) return;
    this.setData({ commandStatus: null, pollingNote: '' });
  },

  async confirmSubmit() {
    const intent = this.data.pendingIntent;
    if (intent === null || this.data.submitting) return;

    if (intent.highRisk && !this.data.confirmChecked) {
      this.setData({ submitError: '请先勾选核对确认，再提交这项关键控制操作。' });
      return;
    }
    const code = normalizeTotpCode(this.data.totpCode);
    if (intent.requiresTotp && !CODE_PATTERN.test(code)) {
      this.setData({ submitError: '请输入身份验证器里最新的 6 位数字动态口令。' });
      return;
    }

    await submitMutex.run(async () => {
      this.setData({ submitting: true, submitError: '', showMfaHint: false });
      try {
        const command = await dispatchControlCommand(gateway, intent, code || undefined);
        const presented = presentCommandStatus(command);
        this.setData({
          submitting: false,
          pendingIntent: null,
          confirmChecked: false,
          totpCode: '',
          submitError: '',
          showMfaHint: false,
          commandStatus: presented,
          pollingNote: '',
        });

        stopCommandPolling();
        if (presented.active) {
          activePolling = startCommandPolling({
            client: gateway,
            commandId: command.commandId,
            onUpdate: (_latest, nextPresented) => {
              this.setData({ commandStatus: nextPresented, pollingNote: '' });
              if (!nextPresented.active) {
                stopCommandPolling();
                delete draftValues[intent.pointId];
                delete inputDraftText[intent.pointId];
                this.refresh();
              }
            },
            onTimeout: () => {
              stopCommandPolling();
              this.setData({
                pollingNote: '指令已提交处理中，请稍后下拉刷新确认最终现场反馈。',
              });
              this.refresh();
            },
            onError: (message) => {
              stopCommandPolling();
              this.setData({ pollingNote: message });
            },
          });
        } else {
          delete draftValues[intent.pointId];
          delete inputDraftText[intent.pointId];
          this.refresh();
        }
      } catch (error) {
        const mapped = mapCommandSubmitError(error, CODE_PATTERN.test(code));
        const nextIntent = mapped.kind === 'step-up' && !intent.requiresTotp
          ? { ...intent, requiresTotp: true }
          : intent;
        this.setData({
          submitting: false,
          pendingIntent: nextIntent,
          submitError: mapped.message,
          showMfaHint: mapped.showMfaSetupHint,
        });
        if (mapped.shouldRefreshState && currentSiteRef !== null) {
          this.refresh();
        }
      }
    });
  },
});
