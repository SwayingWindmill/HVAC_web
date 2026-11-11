import { useMemo, useState } from 'react';
import { Plus, Search } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Input } from '@/components/ui/input';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { cn } from '@/lib/utils';
import { MAX_TREND_POINTS, type TrendCatalog, type TrendPointItem } from '../trends-model';

export function PointPickerPopover({
  catalog,
  selectedRefs,
  onApplyRefs,
}: {
  readonly catalog: TrendCatalog;
  readonly selectedRefs: readonly string[];
  readonly onApplyRefs: (refs: readonly string[]) => void;
}) {
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState('');
  const [draftRefs, setDraftRefs] = useState<readonly string[]>(selectedRefs);

  const draftSet = useMemo(() => new Set(draftRefs), [draftRefs]);
  const atCap = draftRefs.length >= MAX_TREND_POINTS;

  const grouped = useMemo(() => {
    const keyword = search.trim().toLowerCase();
    const map = new Map<string, TrendPointItem[]>();
    for (const item of catalog.points) {
      if (keyword) {
        const haystack = `${item.deviceName} ${item.pointLabel} ${item.unit}`.toLowerCase();
        if (!haystack.includes(keyword)) continue;
      }
      const list = map.get(item.deviceName) ?? [];
      list.push(item);
      map.set(item.deviceName, list);
    }
    return [...map.entries()];
  }, [catalog.points, search]);

  const toggleDraft = (ref: string) => {
    setDraftRefs((prev) => {
      if (prev.includes(ref)) {
        return prev.filter((item) => item !== ref);
      }
      if (prev.length >= MAX_TREND_POINTS) return prev;
      return [...prev, ref];
    });
  };

  return (
    <Popover
      open={open}
      onOpenChange={(nextOpen) => {
        if (nextOpen) {
          setDraftRefs(selectedRefs);
          setSearch('');
        }
        setOpen(nextOpen);
      }}
    >
      <PopoverTrigger asChild>
        <Button variant="outline" size="sm" className="h-7 gap-1 px-2.5 text-xs">
          <Plus className="size-3.5" aria-hidden="true" />
          点位 ({selectedRefs.length}/{MAX_TREND_POINTS})
        </Button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-80 p-0">
        <div className="flex flex-col gap-2 border-b border-divider p-2.5">
          <div className="flex items-center justify-between text-xs">
            <span className="font-medium">选择对比点位</span>
            <span className="text-muted-foreground tabular-nums">
              已选 {draftRefs.length} / {MAX_TREND_POINTS}
            </span>
          </div>
          <div className="relative">
            <Search
              className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground"
              aria-hidden="true"
            />
            <Input
              value={search}
              onChange={(event) => setSearch(event.target.value)}
              placeholder="搜索设备或点位名称…"
              aria-label="搜索点位"
              className="h-7 pl-8 text-xs"
            />
          </div>
        </div>
        <div className="max-h-68 overflow-y-auto p-1.5">
          {grouped.length === 0 ? (
            <p className="py-6 text-center text-xs text-muted-foreground">没有匹配的点位</p>
          ) : (
            grouped.map(([deviceName, points]) => (
              <div key={deviceName} className="mb-1.5 last:mb-0">
                <div className="px-2 py-1 text-[11px] font-medium text-muted-foreground">
                  {deviceName}
                </div>
                {points.map((point) => {
                  const checked = draftSet.has(point.ref);
                  const disabled = !checked && atCap;
                  return (
                    <label
                      key={point.ref}
                      title={disabled ? `最多同时对比 ${MAX_TREND_POINTS} 个点位` : undefined}
                      className={cn(
                        'flex cursor-pointer items-center gap-2 rounded-md px-2 py-1.5 text-xs transition-colors',
                        checked ? 'bg-accent/70 font-medium text-foreground' : 'hover:bg-accent/50',
                        disabled && 'cursor-not-allowed opacity-45 hover:bg-transparent',
                      )}
                    >
                      <Checkbox
                        checked={checked}
                        disabled={disabled}
                        onCheckedChange={() => toggleDraft(point.ref)}
                        aria-label={point.seriesLabel}
                      />
                      <span className="min-w-0 flex-1 truncate">{point.pointLabel}</span>
                      <span className="shrink-0 text-[11px] text-muted-foreground">
                        {point.kind === 'state'
                          ? '启停状态'
                          : point.isSetpoint
                            ? `设定值 ${point.unit}`.trim()
                            : point.unit || '数值'}
                      </span>
                    </label>
                  );
                })}
              </div>
            ))
          )}
        </div>
        <div className="flex items-center justify-end gap-2 border-t border-divider p-2">
          <Button
            type="button"
            variant="ghost"
            size="sm"
            className="h-7 px-2.5 text-xs"
            onClick={() => setOpen(false)}
          >
            取消
          </Button>
          <Button
            type="button"
            size="sm"
            className="h-7 px-3 text-xs"
            disabled={draftRefs.length === 0}
            onClick={() => {
              onApplyRefs(draftRefs);
              setOpen(false);
            }}
          >
            应用
          </Button>
        </div>
      </PopoverContent>
    </Popover>
  );
}
