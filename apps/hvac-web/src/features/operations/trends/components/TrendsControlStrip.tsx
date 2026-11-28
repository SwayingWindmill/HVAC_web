import { useState } from 'react';
import { CalendarClock } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { Switch } from '@/components/ui/switch';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { siteMidnight, wallTime } from '@/lib/site-time';
import { cn } from '@/lib/utils';
import type { TrendGranularity } from '../timeline';
import type {
  ResolvedTrendWindow,
  TrendCatalog,
  TrendPreset,
  TrendPresetId,
  TrendWindowPreset,
} from '../trends-model';
import { PointPickerPopover } from './PointPickerPopover';

const WINDOW_ITEMS: readonly { readonly value: Exclude<TrendWindowPreset, 'custom'>; readonly label: string }[] = [
  { value: '1h', label: '1 小时' },
  { value: '6h', label: '6 小时' },
  { value: 'today', label: '今日' },
  { value: '7d', label: '7 天' },
];

const GRANULARITY_ITEMS: readonly { readonly value: TrendGranularity; readonly label: string }[] = [
  { value: 'MINUTE', label: '1 分钟' },
  { value: 'FIVE_MINUTE', label: '5 分钟' },
  { value: 'QUARTER_HOUR', label: '15 分钟' },
  { value: 'HOUR', label: '1 小时' },
  { value: 'DAY', label: '按日' },
];

function pad2(n: number): string {
  return String(n).padStart(2, '0');
}

function formatSiteLocalInput(ms: number, timezone: string): string {
  const w = wallTime(new Date(ms), timezone);
  return `${w.year}-${pad2(w.month)}-${pad2(w.day)}T${pad2(w.hour)}:${pad2(w.minute)}`;
}

function parseSiteLocalInput(raw: string, timezone: string): number | null {
  const match = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})$/.exec(raw.trim());
  if (!match) return null;
  const year = Number(match[1]);
  const month = Number(match[2]);
  const day = Number(match[3]);
  const hour = Number(match[4]);
  const minute = Number(match[5]);
  const baseMs = siteMidnight(year, month, day, timezone).getTime() + (hour * 60 + minute) * 60_000;
  return Number.isFinite(baseMs) ? baseMs : null;
}

export interface TrendsControlStripProps {
  readonly presets: readonly TrendPreset[];
  readonly activePreset: TrendPreset | null;
  readonly catalog: TrendCatalog;
  readonly selectedRefs: readonly string[];
  readonly windowState: ResolvedTrendWindow;
  readonly timezone: string;
  readonly showCompare: boolean;
  readonly alarmsForbidden: boolean;
  readonly commandsForbidden: boolean;
  readonly showAlarmsActive: boolean;
  readonly showCommandsActive: boolean;
  readonly onApplyRefs: (refs: readonly string[]) => void;
  readonly onSelectPreset: (presetId: TrendPresetId) => void;
  readonly onSelectWindow: (windowPreset: Exclude<TrendWindowPreset, 'custom'>) => void;
  readonly onApplyCustomRange: (fromIso: string, toIso: string) => void;
  readonly onSelectGranularity: (granularity: TrendGranularity) => void;
  readonly onToggleCompare: (checked: boolean) => void;
  readonly onToggleAlarms: (checked: boolean) => void;
  readonly onToggleCommands: (checked: boolean) => void;
}

export function TrendsControlStrip({
  presets,
  activePreset,
  catalog,
  selectedRefs,
  windowState,
  timezone,
  showCompare,
  alarmsForbidden,
  commandsForbidden,
  showAlarmsActive,
  showCommandsActive,
  onApplyRefs,
  onSelectPreset,
  onSelectWindow,
  onApplyCustomRange,
  onSelectGranularity,
  onToggleCompare,
  onToggleAlarms,
  onToggleCommands,
}: TrendsControlStripProps) {
  const [customOpen, setCustomOpen] = useState(false);
  const [customFromDraft, setCustomFromDraft] = useState('');
  const [customToDraft, setCustomToDraft] = useState('');

  const parsedCustomFromMs = parseSiteLocalInput(customFromDraft, timezone);
  const parsedCustomToMs = parseSiteLocalInput(customToDraft, timezone);
  const customRangeValid =
    parsedCustomFromMs !== null &&
    parsedCustomToMs !== null &&
    parsedCustomToMs > parsedCustomFromMs &&
    parsedCustomToMs - parsedCustomFromMs <= 366 * 86_400_000;

  return (
    <div className="flex flex-wrap items-center justify-between gap-3 border-b border-divider px-5 py-3">
      <div className="flex flex-wrap items-center gap-2">
        {presets.length > 0 ? (
          <ToggleGroup
            type="single"
            variant="outline"
            size="sm"
            value={activePreset?.id ?? ''}
            onValueChange={(val) => {
              const matched = presets.find((preset) => preset.id === val);
              if (matched) onSelectPreset(matched.id);
            }}
            aria-label="排障预设"
          >
            {presets.map((preset) => (
              <ToggleGroupItem key={preset.id} value={preset.id} className="h-7 px-2.5 text-xs">
                {preset.title}
              </ToggleGroupItem>
            ))}
          </ToggleGroup>
        ) : null}

        <PointPickerPopover
          catalog={catalog}
          selectedRefs={selectedRefs}
          onApplyRefs={onApplyRefs}
        />
      </div>

      <div className="flex flex-wrap items-center gap-3">
        <div className="flex items-center gap-1.5">
          <ToggleGroup
            type="single"
            variant="outline"
            size="sm"
            spacing={0}
            value={windowState.preset === 'custom' ? '' : windowState.preset}
            onValueChange={(val) => {
              const matched = WINDOW_ITEMS.find((item) => item.value === val);
              if (matched) onSelectWindow(matched.value);
            }}
            aria-label="时间范围"
          >
            {WINDOW_ITEMS.map((item) => (
              <ToggleGroupItem key={item.value} value={item.value} className="h-7 px-2.5 text-xs">
                {item.label}
              </ToggleGroupItem>
            ))}
          </ToggleGroup>

          <Popover
            open={customOpen}
            onOpenChange={(nextOpen) => {
              if (nextOpen) {
                setCustomFromDraft(formatSiteLocalInput(windowState.fromMs, timezone));
                setCustomToDraft(formatSiteLocalInput(windowState.toMs, timezone));
              }
              setCustomOpen(nextOpen);
            }}
          >
            <PopoverTrigger asChild>
              <Button
                variant="outline"
                size="sm"
                aria-pressed={windowState.preset === 'custom'}
                className={cn(
                  'h-7 gap-1 px-2.5 text-xs',
                  windowState.preset === 'custom' && 'border-input-hover bg-accent text-foreground',
                )}
              >
                <CalendarClock className="size-3.5" aria-hidden="true" />
                自定义
              </Button>
            </PopoverTrigger>
            <PopoverContent align="end" className="flex w-72 flex-col gap-3 p-3">
              <p className="text-xs font-medium">自定义时段（站点时间）</p>
              <label className="flex flex-col gap-1 text-xs text-muted-foreground">
                <span>开始时间</span>
                <Input
                  type="datetime-local"
                  aria-label="开始时间"
                  value={customFromDraft}
                  onChange={(ev) => setCustomFromDraft(ev.target.value)}
                  className="h-8 font-mono text-xs tabular-nums"
                />
              </label>
              <label className="flex flex-col gap-1 text-xs text-muted-foreground">
                <span>结束时间</span>
                <Input
                  type="datetime-local"
                  aria-label="结束时间"
                  value={customToDraft}
                  onChange={(ev) => setCustomToDraft(ev.target.value)}
                  className="h-8 font-mono text-xs tabular-nums"
                />
              </label>
              <div className="flex items-center justify-end gap-2 pt-1">
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  className="h-7 px-2.5 text-xs"
                  onClick={() => setCustomOpen(false)}
                >
                  取消
                </Button>
                <Button
                  type="button"
                  size="sm"
                  className="h-7 px-3 text-xs"
                  disabled={!customRangeValid}
                  onClick={() => {
                    if (!customRangeValid) return;
                    onApplyCustomRange(
                      new Date(parsedCustomFromMs).toISOString(),
                      new Date(parsedCustomToMs).toISOString(),
                    );
                    setCustomOpen(false);
                  }}
                >
                  应用
                </Button>
              </div>
            </PopoverContent>
          </Popover>
        </div>

        <ToggleGroup
          type="single"
          variant="outline"
          size="sm"
          spacing={0}
          value={windowState.granularity}
          onValueChange={(val) => {
            const matched = GRANULARITY_ITEMS.find((item) => item.value === val);
            if (matched) onSelectGranularity(matched.value);
          }}
          aria-label="聚合粒度"
        >
          {GRANULARITY_ITEMS.map((item) => {
            const allowed = windowState.allowedGranularities.includes(item.value);
            return (
              <ToggleGroupItem
                key={item.value}
                value={item.value}
                disabled={!allowed}
                title={!allowed ? '所选时间跨度较长，请选择更粗的聚合粒度' : undefined}
                className="h-7 px-2 text-xs"
              >
                {item.label}
              </ToggleGroupItem>
            );
          })}
        </ToggleGroup>

        <label className="inline-flex cursor-pointer items-center gap-1.5 text-xs text-muted-foreground select-none">
          <Switch
            size="sm"
            aria-label={windowState.compareLabel}
            checked={showCompare}
            onCheckedChange={onToggleCompare}
          />
          <span>{windowState.compareLabel}</span>
        </label>

        <label className="inline-flex items-center gap-1.5 text-xs text-muted-foreground select-none">
          <Switch
            size="sm"
            aria-label="叠加告警"
            disabled={alarmsForbidden}
            checked={showAlarmsActive}
            onCheckedChange={onToggleAlarms}
          />
          <span>叠加告警</span>
          {alarmsForbidden ? (
            <span className="text-[11px] text-subtle-foreground">无告警查看权限</span>
          ) : null}
        </label>

        <label className="inline-flex items-center gap-1.5 text-xs text-muted-foreground select-none">
          <Switch
            size="sm"
            aria-label="叠加控制指令"
            disabled={commandsForbidden}
            checked={showCommandsActive}
            onCheckedChange={onToggleCommands}
          />
          <span>叠加控制指令</span>
          {commandsForbidden ? (
            <span className="text-[11px] text-subtle-foreground">无指令查看权限</span>
          ) : null}
        </label>
      </div>
    </div>
  );
}
