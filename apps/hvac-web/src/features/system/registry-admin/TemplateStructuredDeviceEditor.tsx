import { Plus, Trash2 } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { isOneOf } from '@/lib/typed';
import {
  CAPABILITY_APPROVAL_POLICIES,
  CAPABILITY_RISK_LEVELS,
  CAPABILITY_STATUSES,
  createEmptyCapabilityProfileDraft,
  createEmptyStandardPointDraft,
  STANDARD_POINT_TYPES,
  STANDARD_VALUE_TYPES,
  TEMPLATE_COUNTER_DECREASE_MODES,
  type CapabilityProfileDraft,
  type StandardPointDraft,
  type StructuredDeviceTemplateDraft,
} from './template-model';

function approvalPolicyLabel(policy: string): string {
  if (policy === 'SINGLE_APPROVER') return '单人审批';
  if (policy === 'TWO_PERSON') return '双人复核';
  return '无需审批';
}

function riskLevelLabel(level: string): string {
  if (level === 'CRITICAL') return '严重风险';
  if (level === 'HIGH') return '高风险';
  if (level === 'MEDIUM') return '中风险';
  return '低风险';
}

export interface TemplateStructuredDeviceEditorProps {
  readonly draft: StructuredDeviceTemplateDraft;
  readonly allowed: boolean;
  readonly onChange: (next: StructuredDeviceTemplateDraft) => void;
}

export function TemplateStructuredDeviceEditor({
  draft,
  allowed,
  onChange,
}: TemplateStructuredDeviceEditorProps) {
  const updatePoint = (index: number, patch: Partial<StandardPointDraft>) => {
    onChange({
      ...draft,
      standardPoints: draft.standardPoints.map((row, i) => (i === index ? { ...row, ...patch } : row)),
    });
  };

  const updateCapability = (index: number, patch: Partial<CapabilityProfileDraft>) => {
    onChange({
      ...draft,
      capabilityProfiles: draft.capabilityProfiles.map((row, i) => (i === index ? { ...row, ...patch } : row)),
    });
  };

  return (
    <div className="space-y-4">
      <label className="grid gap-1.5 text-sm">
        <span>适用设备类型（deviceType，可选）</span>
        <Input
          disabled={!allowed}
          value={draft.deviceType}
          placeholder="CHILLER"
          autoCorrect="off"
          autoCapitalize="none"
          spellCheck={false}
          className="font-mono text-xs"
          onChange={(event) => onChange({ ...draft, deviceType: event.currentTarget.value })}
        />
      </label>

      <div className="space-y-2.5">
        <div className="flex items-center justify-between">
          <span className="text-xs font-semibold text-foreground">
            标准点位定义（{draft.standardPoints.length} 项）
          </span>
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={!allowed}
            onClick={() =>
              onChange({
                ...draft,
                standardPoints: [...draft.standardPoints, createEmptyStandardPointDraft()],
              })
            }
          >
            <Plus data-icon="inline-start" />
            新增标准点位
          </Button>
        </div>

        {draft.standardPoints.length === 0 ? (
          <p className="rounded-md border border-dashed px-3 py-2 text-xs text-muted-foreground">
            未配置标准点位；点击「新增标准点位」后，模板应用到设备时会自动物化点位登记与质量规则。
          </p>
        ) : (
          <div className="space-y-2.5">
            {draft.standardPoints.map((pt, index) => (
              <div key={index} className="space-y-2 rounded-lg border bg-muted/30 p-3">
                <div className="flex items-center justify-between">
                  <span className="text-xs font-medium text-muted-foreground">点位 #{index + 1}</span>
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon-xs"
                    disabled={!allowed}
                    aria-label={`删除标准点位 ${index + 1}`}
                    onClick={() =>
                      onChange({
                        ...draft,
                        standardPoints: draft.standardPoints.filter((_, i) => i !== index),
                      })
                    }
                  >
                    <Trash2 className="size-3.5" />
                  </Button>
                </div>
                <div className="grid gap-2 sm:grid-cols-3">
                  <label className="grid gap-1 text-xs">
                    <span className="text-muted-foreground">点位编码 (pointCode)</span>
                    <Input
                      disabled={!allowed}
                      value={pt.pointCode}
                      placeholder="chw_supply_temp"
                      autoCorrect="off"
                      autoCapitalize="none"
                      spellCheck={false}
                      className="h-8 font-mono text-xs"
                      onChange={(e) => updatePoint(index, { pointCode: e.currentTarget.value })}
                    />
                  </label>
                  <label className="grid gap-1 text-xs">
                    <span className="text-muted-foreground">显示名称</span>
                    <Input
                      disabled={!allowed}
                      value={pt.displayName}
                      placeholder="冷冻水供水温度"
                      className="h-8 text-xs"
                      onChange={(e) => updatePoint(index, { displayName: e.currentTarget.value })}
                    />
                  </label>
                  <label className="grid gap-1 text-xs">
                    <span className="text-muted-foreground">来源键（可选）</span>
                    <Input
                      disabled={!allowed}
                      value={pt.sourceKey}
                      placeholder="chw_supply_temp"
                      autoCorrect="off"
                      autoCapitalize="none"
                      spellCheck={false}
                      className="h-8 font-mono text-xs"
                      onChange={(e) => updatePoint(index, { sourceKey: e.currentTarget.value })}
                    />
                  </label>
                </div>
                <div className="grid gap-2 sm:grid-cols-4">
                  <label className="grid gap-1 text-xs">
                    <span className="text-muted-foreground">点位分类</span>
                    <Select
                      disabled={!allowed}
                      value={pt.pointType}
                      onValueChange={(value) => {
                        if (!isOneOf(STANDARD_POINT_TYPES, value)) return;
                        updatePoint(index, {
                          pointType: value,
                          writable: value === 'COMMAND' ? true : value === 'SETTING' ? pt.writable : false,
                          valueType: value === 'COUNTER' ? 'NUMBER' : pt.valueType,
                          counterDecreaseMode: value === 'COUNTER' ? pt.counterDecreaseMode || 'RESET_TO_ZERO' : '',
                        });
                      }}
                    >
                      <SelectTrigger className="h-8 text-xs">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        {STANDARD_POINT_TYPES.map((t) => (
                          <SelectItem key={t} value={t} className="text-xs">
                            {t}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </label>
                  <label className="grid gap-1 text-xs">
                    <span className="text-muted-foreground">值类型</span>
                    <Select
                      disabled={!allowed || pt.pointType === 'COUNTER'}
                      value={pt.valueType}
                      onValueChange={(value) => {
                        if (!isOneOf(STANDARD_VALUE_TYPES, value)) return;
                        updatePoint(index, { valueType: value });
                      }}
                    >
                      <SelectTrigger className="h-8 text-xs">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        {STANDARD_VALUE_TYPES.map((vt) => (
                          <SelectItem key={vt} value={vt} className="text-xs">
                            {vt}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </label>
                  <label className="grid gap-1 text-xs">
                    <span className="text-muted-foreground">单位（可选）</span>
                    <Input
                      disabled={!allowed}
                      value={pt.unit}
                      placeholder="degC"
                      autoCorrect="off"
                      autoCapitalize="none"
                      spellCheck={false}
                      className="h-8 font-mono text-xs"
                      onChange={(e) => updatePoint(index, { unit: e.currentTarget.value })}
                    />
                  </label>
                  <div className="flex items-end pb-1">
                    <div className="flex items-center gap-2">
                      <Checkbox
                        id={`point-writable-${index}`}
                        checked={pt.pointType === 'COMMAND' ? true : pt.writable}
                        disabled={!allowed || pt.pointType !== 'SETTING'}
                        onCheckedChange={(checked) => updatePoint(index, { writable: checked === true })}
                      />
                      <Label htmlFor={`point-writable-${index}`} className="text-xs font-normal">
                        允许写入
                      </Label>
                    </div>
                  </div>
                </div>
                {pt.pointType === 'COUNTER' ? (
                  <div className="grid gap-2 sm:grid-cols-2">
                    <label className="grid gap-1 text-xs">
                      <span className="text-muted-foreground">累计量下降处理</span>
                      <Select
                        disabled={!allowed}
                        value={pt.counterDecreaseMode || 'RESET_TO_ZERO'}
                        onValueChange={(value) => {
                          if (!isOneOf(TEMPLATE_COUNTER_DECREASE_MODES, value)) return;
                          updatePoint(index, { counterDecreaseMode: value });
                        }}
                      >
                        <SelectTrigger className="h-8 text-xs">
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          <SelectItem value="RESET_TO_ZERO">RESET_TO_ZERO</SelectItem>
                          <SelectItem value="ROLLOVER">ROLLOVER</SelectItem>
                          <SelectItem value="INVALID">INVALID</SelectItem>
                        </SelectContent>
                      </Select>
                    </label>
                    <label className="grid gap-1 text-xs">
                      <span className="text-muted-foreground">回绕模数（ROLLOVER 必填）</span>
                      <Input
                        type="number"
                        disabled={!allowed || pt.counterDecreaseMode !== 'ROLLOVER'}
                        value={pt.counterRolloverModulus}
                        className="h-8 font-mono text-xs tabular-nums"
                        onChange={(e) => updatePoint(index, { counterRolloverModulus: e.currentTarget.value })}
                      />
                    </label>
                  </div>
                ) : null}
                <div className="grid gap-2 sm:grid-cols-3">
                  <label className="grid gap-1 text-xs">
                    <span className="text-muted-foreground">采样 ms</span>
                    <Input
                      type="number"
                      min={100}
                      disabled={!allowed}
                      value={pt.sampleIntervalMs}
                      className="h-8 font-mono text-xs tabular-nums"
                      onChange={(e) => updatePoint(index, { sampleIntervalMs: e.currentTarget.value })}
                    />
                  </label>
                  <label className="grid gap-1 text-xs">
                    <span className="text-muted-foreground">发布 ms</span>
                    <Input
                      type="number"
                      min={100}
                      disabled={!allowed}
                      value={pt.publishIntervalMs}
                      className="h-8 font-mono text-xs tabular-nums"
                      onChange={(e) => updatePoint(index, { publishIntervalMs: e.currentTarget.value })}
                    />
                  </label>
                  <label className="grid gap-1 text-xs">
                    <span className="text-muted-foreground">陈旧 ms</span>
                    <Input
                      type="number"
                      min={100}
                      disabled={!allowed}
                      value={pt.staleAfterMs}
                      className="h-8 font-mono text-xs tabular-nums"
                      onChange={(e) => updatePoint(index, { staleAfterMs: e.currentTarget.value })}
                    />
                  </label>
                </div>
                <div className="grid gap-2 sm:grid-cols-3">
                  <label className="grid gap-1 text-xs">
                    <span className="text-muted-foreground">数值下限（可选）</span>
                    <Input
                      inputMode="decimal"
                      disabled={!allowed || pt.valueType !== 'NUMBER'}
                      value={pt.minimumNumber}
                      placeholder="0"
                      className="h-8 font-mono text-xs tabular-nums"
                      onChange={(e) => updatePoint(index, { minimumNumber: e.currentTarget.value })}
                    />
                  </label>
                  <label className="grid gap-1 text-xs">
                    <span className="text-muted-foreground">数值上限（可选）</span>
                    <Input
                      inputMode="decimal"
                      disabled={!allowed || pt.valueType !== 'NUMBER'}
                      value={pt.maximumNumber}
                      placeholder="100"
                      className="h-8 font-mono text-xs tabular-nums"
                      onChange={(e) => updatePoint(index, { maximumNumber: e.currentTarget.value })}
                    />
                  </label>
                  <label className="grid gap-1 text-xs">
                    <span className="text-muted-foreground">卡死告警秒数（可选）</span>
                    <Input
                      type="number"
                      min={60}
                      disabled={!allowed}
                      value={pt.flatlineSeconds}
                      placeholder="300"
                      className="h-8 font-mono text-xs tabular-nums"
                      onChange={(e) => updatePoint(index, { flatlineSeconds: e.currentTarget.value })}
                    />
                  </label>
                </div>
              </div>
            ))}
          </div>
        )}
      </div>

      <div className="space-y-2.5">
        <div className="flex items-center justify-between">
          <span className="text-xs font-semibold text-foreground">
            控制能力规格（{draft.capabilityProfiles.length} 项）
          </span>
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={!allowed}
            onClick={() =>
              onChange({
                ...draft,
                capabilityProfiles: [...draft.capabilityProfiles, createEmptyCapabilityProfileDraft()],
              })
            }
          >
            <Plus data-icon="inline-start" />
            新增控制能力
          </Button>
        </div>

        {draft.capabilityProfiles.length === 0 ? (
          <p className="rounded-md border border-dashed px-3 py-2 text-xs text-muted-foreground">
            未配置控制能力规格；适用于仅采集遥测而不下发写指令的设备。
          </p>
        ) : (
          <div className="space-y-2.5">
            {draft.capabilityProfiles.map((cap, index) => (
              <div key={index} className="space-y-2 rounded-lg border bg-muted/30 p-3">
                <div className="flex items-center justify-between">
                  <span className="text-xs font-medium text-muted-foreground">能力 #{index + 1}</span>
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon-xs"
                    disabled={!allowed}
                    aria-label={`删除控制能力 ${index + 1}`}
                    onClick={() =>
                      onChange({
                        ...draft,
                        capabilityProfiles: draft.capabilityProfiles.filter((_, i) => i !== index),
                      })
                    }
                  >
                    <Trash2 className="size-3.5" />
                  </Button>
                </div>
                <div className="grid gap-2 sm:grid-cols-3">
                  <label className="grid gap-1 text-xs">
                    <span className="text-muted-foreground">能力名称</span>
                    <Input
                      disabled={!allowed}
                      value={cap.capabilityName}
                      placeholder="set_chw_setpoint"
                      autoCorrect="off"
                      autoCapitalize="none"
                      spellCheck={false}
                      className="h-8 font-mono text-xs"
                      onChange={(e) => updateCapability(index, { capabilityName: e.currentTarget.value })}
                    />
                  </label>
                  <label className="grid gap-1 text-xs">
                    <span className="text-muted-foreground">能力版本</span>
                    <Input
                      disabled={!allowed}
                      value={cap.capabilityRevision}
                      placeholder="v1"
                      autoCorrect="off"
                      autoCapitalize="none"
                      spellCheck={false}
                      className="h-8 font-mono text-xs"
                      onChange={(e) => updateCapability(index, { capabilityRevision: e.currentTarget.value })}
                    />
                  </label>
                  <label className="grid gap-1 text-xs">
                    <span className="text-muted-foreground">连接器类型</span>
                    <Input
                      disabled={!allowed}
                      value={cap.connectorKind}
                      placeholder="MODBUS_TCP"
                      autoCorrect="off"
                      autoCapitalize="none"
                      spellCheck={false}
                      className="h-8 font-mono text-xs"
                      onChange={(e) => updateCapability(index, { connectorKind: e.currentTarget.value })}
                    />
                  </label>
                </div>
                <div className="grid gap-2 sm:grid-cols-4">
                  <label className="grid gap-1 text-xs">
                    <span className="text-muted-foreground">规范单位</span>
                    <Input
                      disabled={!allowed}
                      value={cap.canonicalUnit}
                      placeholder="degC"
                      autoCorrect="off"
                      autoCapitalize="none"
                      spellCheck={false}
                      className="h-8 font-mono text-xs"
                      onChange={(e) => updateCapability(index, { canonicalUnit: e.currentTarget.value })}
                    />
                  </label>
                  <label className="grid gap-1 text-xs">
                    <span className="text-muted-foreground">最小值</span>
                    <Input
                      inputMode="decimal"
                      disabled={!allowed}
                      value={cap.minimumValue}
                      className="h-8 font-mono text-xs tabular-nums"
                      onChange={(e) => updateCapability(index, { minimumValue: e.currentTarget.value })}
                    />
                  </label>
                  <label className="grid gap-1 text-xs">
                    <span className="text-muted-foreground">最大值</span>
                    <Input
                      inputMode="decimal"
                      disabled={!allowed}
                      value={cap.maximumValue}
                      className="h-8 font-mono text-xs tabular-nums"
                      onChange={(e) => updateCapability(index, { maximumValue: e.currentTarget.value })}
                    />
                  </label>
                  <label className="grid gap-1 text-xs">
                    <span className="text-muted-foreground">最大步长</span>
                    <Input
                      inputMode="decimal"
                      disabled={!allowed}
                      value={cap.maximumDelta}
                      className="h-8 font-mono text-xs tabular-nums"
                      onChange={(e) => updateCapability(index, { maximumDelta: e.currentTarget.value })}
                    />
                  </label>
                </div>
                <div className="grid gap-2 sm:grid-cols-3">
                  <label className="grid gap-1 text-xs">
                    <span className="text-muted-foreground">状态</span>
                    <Select
                      disabled={!allowed}
                      value={cap.status}
                      onValueChange={(value) => {
                        if (isOneOf(CAPABILITY_STATUSES, value)) updateCapability(index, { status: value });
                      }}
                    >
                      <SelectTrigger className="h-8 text-xs">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="VERIFIED">VERIFIED</SelectItem>
                        <SelectItem value="DRAFT">DRAFT</SelectItem>
                      </SelectContent>
                    </Select>
                  </label>
                  <label className="grid gap-1 text-xs">
                    <span className="text-muted-foreground">风险等级</span>
                    <Select
                      disabled={!allowed}
                      value={cap.riskLevel}
                      onValueChange={(value) => {
                        if (isOneOf(CAPABILITY_RISK_LEVELS, value)) updateCapability(index, { riskLevel: value });
                      }}
                    >
                      <SelectTrigger className="h-8 text-xs">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        {CAPABILITY_RISK_LEVELS.map((lv) => (
                          <SelectItem key={lv} value={lv}>
                            {riskLevelLabel(lv)} ({lv})
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </label>
                  <label className="grid gap-1 text-xs">
                    <span className="text-muted-foreground">审批策略</span>
                    <Select
                      disabled={!allowed}
                      value={cap.approvalPolicy}
                      onValueChange={(value) => {
                        if (isOneOf(CAPABILITY_APPROVAL_POLICIES, value)) {
                          updateCapability(index, { approvalPolicy: value });
                        }
                      }}
                    >
                      <SelectTrigger className="h-8 text-xs">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        {CAPABILITY_APPROVAL_POLICIES.map((ap) => (
                          <SelectItem key={ap} value={ap}>
                            {approvalPolicyLabel(ap)}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </label>
                </div>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
